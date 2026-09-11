package search

import "context"

// warmText is the query Warm runs: ordinary corpus vocabulary, so every arm has
// postings and neighbours to touch.
const warmText = "registration procedure"

// Warm runs one hybrid search through every arm this engine serves and returns
// what it did — so the FIRST client query of a session does not pay the cold
// start, and the start is paid where it is logged rather than inside a budget.
//
// WHY. The frozen HNSW index is read from the corpus file into the buffer pool on
// the first k-NN, and the full-text and sparse tables are paged in on their first
// query. Measured on the 3GPP half (server-full, 2026-09-11): the first dense arm
// took 21-28 s, the second 0.5 s; the first lexical arm 6.6 s, the second 0.9 s.
// Under the served SEARCH_BUDGET of 20 s, a session's first hybrid query spent
// its whole budget on the index load, and every later arm was skipped.
//
// It runs WITHOUT a budget (a budget would skip the very arms it exists to warm)
// and without the cross-encoder (twelve 512-token passages are CPU the client's
// first query would queue behind, for a session the ONNX runtime already built).
// A client query arriving meanwhile waits for the store's connection, so it is
// never slower than it would have been cold.
func (e *Engine) Warm(ctx context.Context) Report {
	ctx, tr := WithTrace(context.WithValue(ctx, budgetKey{}, budgetStart{}))
	_, _ = e.Search(ctx, Request{Text: warmText, Mode: "hybrid", TopK: 10})
	if reps := tr.Reports(); len(reps) == 1 {
		return reps[0]
	}
	return Report{Corpus: e.name}
}
