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
	"os"
	"path/filepath"
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

// runSynthHNSW populates a throwaway DB with N random Dim-d vectors generated
// ENTIRELY inside DuckDB (random() is volatile → distinct per element and row),
// so there is no Go float formatting and no multi-MB SQL — a single INSERT. It
// then runs the real BuildAndFreezeHNSW; the outer `/usr/bin/time -v` captures
// the build peak RSS (the go/no-go RAM gate). Vectors are NOT L2-normalised:
// irrelevant for a build-RSS probe (the index stores the vectors either way).
// The DB is removed on exit.
func runSynthHNSW(n int, outPath string, _ int) error {
	// --synth-out is operator input fed to os.Remove/store.Open; refuse anything
	// that isn't a plain relative .duckdb path so a stray/hostile value can never
	// delete or overwrite an unrelated file.
	outPath = filepath.Clean(outPath)
	if !strings.HasSuffix(outPath, ".duckdb") || filepath.IsAbs(outPath) || strings.HasPrefix(outPath, "..") {
		return fmt.Errorf("--synth-out must be a relative *.duckdb path, got %q", outPath)
	}
	_ = os.Remove(outPath) // deterministic: no stale index
	st, err := store.Open(outPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", outPath, err)
	}
	defer func() { _ = st.Close(); _ = os.Remove(outPath) }()

	ctx := context.Background()
	db := st.DB()

	insStart := time.Now()
	q := fmt.Sprintf(`
		INSERT INTO clauses (chunk_id, spec_id, release, version, clause_path, heading, text, is_normative, embedding)
		SELECT i, '00.000', 'Rel-18', '18.0.0', '1', 'synth', 'synth', false,
		       CAST([random()::FLOAT FOR x IN range(%d)] AS FLOAT[%d])
		FROM range(1, CAST(? AS BIGINT) + 1) t(i)`, embed.Dim, embed.Dim)
	if _, err := db.ExecContext(ctx, q, n); err != nil {
		return fmt.Errorf("synth insert: %w", err)
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
