// Command bench is the offline retrieval benchmark (axis #7): it scores
// retrieval systems (lexical / hybrid / hybrid+rerank) on a graded query set
// against a built DuckDB snapshot and prints macro-averaged IR metrics. It is
// read-only and never shipped in mcp-3gpp — it exists to pick the V2 semantic
// stack from data. Embedder/reranker are chosen via EMBEDDER/RERANKER env.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/kodflow/3gpp-mcp/internal/eval"
	"github.com/kodflow/3gpp-mcp/internal/search"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

func main() {
	dbPath := flag.String("db", "data/3gpp.duckdb", "DuckDB snapshot (read-only)")
	setPath := flag.String("set", "docs/inputs/eval/li_5gc_queries.json", "graded query set")
	flag.Parse()

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
