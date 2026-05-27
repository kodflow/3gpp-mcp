// Command bench is the offline retrieval benchmark (axis #7): it scores
// retrieval systems (lexical / hybrid / hybrid+rerank) on a graded query set
// against a built DuckDB snapshot and prints macro-averaged IR metrics. It is
// read-only and never shipped in mcp-3gpp — it exists to pick the V2 semantic
// stack from data. Embedder/reranker are chosen via EMBEDDER/RERANKER env.
//
// Phase-0 (throwaway, --synth-hnsw): insert N random Dim-d vectors and run the
// real BuildAndFreezeHNSW so an outer `/usr/bin/time -v` captures the peak RSS
// of the global HNSW build at scope N — the go/no-go RAM gate for the semantic
// layer. Uses random distinct unit vectors (a degenerate all-equal set would
// give an unrepresentative trivial graph). No model, no ONNX needed: the build
// is pure DuckDB VSS.
package main

import (
	"context"
	"flag"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/kodflow/3gpp-mcp/internal/embed"
	"github.com/kodflow/3gpp-mcp/internal/eval"
	"github.com/kodflow/3gpp-mcp/internal/search"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

func main() {
	dbPath := flag.String("db", "data/3gpp.duckdb", "DuckDB snapshot (read-only)")
	setPath := flag.String("set", "docs/inputs/eval/li_5gc_queries.json", "graded query set")
	synth := flag.Int("synth-hnsw", 0, "Phase-0: insert N random vectors, build+freeze HNSW, report build time; then exit (measure peak RSS via /usr/bin/time -v)")
	synthOut := flag.String("synth-out", "data/phase0-synth.duckdb", "output DB for --synth-hnsw (created then removed)")
	synthBatch := flag.Int("synth-batch", 512, "rows per INSERT for --synth-hnsw")
	flag.Parse()

	if *synth > 0 {
		if err := runSynthHNSW(*synth, *synthOut, *synthBatch); err != nil {
			fmt.Fprintf(os.Stderr, "synth-hnsw: %v\n", err)
			os.Exit(1)
		}
		return
	}

	set, err := eval.Load(*setPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load eval set: %v\n", err)
		os.Exit(1)
	}
	st, err := store.OpenReadOnly(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open %s: %v\n", *dbPath, err)
		os.Exit(1)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	_ = st.LoadFTS(ctx)
	_ = st.LoadVSS(ctx)
	eng := search.New(st)

	systems := []struct {
		name string
		rank eval.Ranker
	}{
		{"lexical (BM25)", func(ctx context.Context, q eval.Query) ([]eval.Ref, error) {
			hits, err := st.SearchClauses(ctx, store.SearchQuery{
				Text: q.Query, Filter: store.SpecFilter{Release: q.Release}, TopK: 20})
			return eval.HitsToRefs(hits), err
		}},
		{"hybrid RRF", func(ctx context.Context, q eval.Query) ([]eval.Ref, error) {
			hits, err := eng.Search(ctx, search.Request{
				Text: q.Query, Filter: store.SpecFilter{Release: q.Release}, TopK: 20})
			return eval.HitsToRefs(hits), err
		}},
		{"hybrid + rerank", func(ctx context.Context, q eval.Query) ([]eval.Ref, error) {
			hits, err := eng.Search(ctx, search.Request{
				Text: q.Query, Filter: store.SpecFilter{Release: q.Release}, TopK: 20, Rerank: true})
			return eval.HitsToRefs(hits), err
		}},
	}

	fmt.Printf("bench: %d queries, db=%s\n\n", len(set), *dbPath)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "system\tnDCG@5\tnDCG@10\tRecall@10\tMRR@10\tSuccess@1")
	for _, s := range systems {
		_, avg, err := eval.Run(ctx, set, s.rank)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", s.name, err)
			os.Exit(1)
		}
		_, _ = fmt.Fprintf(w, "%s\t%.3f\t%.3f\t%.3f\t%.3f\t%.3f\n",
			s.name, avg.NDCG5, avg.NDCG10, avg.Recall10, avg.MRR, avg.Success1)
	}
	_ = w.Flush()
}

// runSynthHNSW populates a throwaway DB with N random unit vectors (inserted
// with the embedding inline, so no slow per-row columnar UPDATE) and then runs
// BuildAndFreezeHNSW. The peak RSS is observed by the outer time(1); we also
// print the build wall-time. The DB is removed on exit.
func runSynthHNSW(n int, outPath string, batch int) error {
	if batch < 1 {
		batch = 512
	}
	_ = os.Remove(outPath) // deterministic: no stale index
	st, err := store.Open(outPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", outPath, err)
	}
	defer func() { _ = st.Close(); _ = os.Remove(outPath) }()

	ctx := context.Background()
	rng := rand.New(rand.NewPCG(1, 2)) // fixed seed → reproducible peak RSS
	db := st.DB()

	const cols = "INSERT INTO clauses (chunk_id,spec_id,release,version,clause_path,heading,text,is_normative,embedding) VALUES "
	insStart := time.Now()
	for base := 0; base < n; base += batch {
		end := base + batch
		if end > n {
			end = n
		}
		var sb strings.Builder
		args := make([]any, 0, (end-base)*9)
		sb.WriteString(cols)
		for i := base; i < end; i++ {
			if i > base {
				sb.WriteByte(',')
			}
			sb.WriteString("(?,?,?,?,?,?,?,?,CAST(? AS FLOAT[1024]))")
			args = append(args, uint64(i+1), "00.000", "Rel-18", "18.0.0", "1", "synth", "synth", false, randUnitLiteral(rng))
		}
		if _, err := db.ExecContext(ctx, sb.String(), args...); err != nil {
			return fmt.Errorf("insert rows [%d,%d): %w", base, end, err)
		}
	}
	fmt.Printf("synth-hnsw: inserted N=%d dim=%d in %s\n", n, embed.Dim, time.Since(insStart).Round(time.Millisecond))

	buildStart := time.Now()
	if err := st.BuildAndFreezeHNSW(ctx, "synth-bge-m3"); err != nil {
		return fmt.Errorf("build hnsw: %w", err)
	}
	fmt.Printf("synth-hnsw: build+freeze N=%d in %s (peak RSS = /usr/bin/time -v Maximum resident set size)\n",
		n, time.Since(buildStart).Round(time.Millisecond))
	return nil
}

// randUnitLiteral returns a DuckDB array literal of Dim random L2-normalised
// floats, e.g. "[0.01,-0.03,...]" — cast to FLOAT[1024] by the caller.
func randUnitLiteral(rng *rand.Rand) string {
	v := make([]float64, embed.Dim)
	var norm float64
	for i := range v {
		v[i] = rng.NormFloat64()
		norm += v[i] * v[i]
	}
	inv := 1.0 / math.Sqrt(norm)
	var sb strings.Builder
	sb.WriteByte('[')
	for i := range v {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(strconv.FormatFloat(v[i]*inv, 'g', 6, 32))
	}
	sb.WriteByte(']')
	return sb.String()
}
