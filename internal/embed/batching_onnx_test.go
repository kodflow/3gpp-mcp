//go:build onnx

package embed

import "testing"

// TestLadderCeil pins the fixed-shape rung rounding: every length maps UP to a
// ladder rung (never below the input, never above maxTokens), so the ONNX session
// only ever sees len(shapeLadder) distinct sequence lengths.
func TestLadderCeil(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, 16}, {1, 16}, {16, 16}, {17, 32}, {64, 64}, {65, 96},
		{129, 192}, {200, 256}, {300, 384}, {385, maxTokens}, {512, maxTokens},
		{maxTokens, maxTokens}, {maxTokens + 50, maxTokens},
	}
	for _, c := range cases {
		if got := ladderCeil(c.in); got != c.want {
			t.Errorf("ladderCeil(%d)=%d want %d", c.in, got, c.want)
		}
		if got := ladderCeil(c.in); got < c.in && c.in <= maxTokens {
			t.Errorf("ladderCeil(%d)=%d padded BELOW input", c.in, got)
		}
	}
}

// TestTokenBudgetBatchesCoverage asserts the variable-size batcher partitions the
// input EXACTLY (contiguous, no gap/overlap, every clause assigned once) — the
// property that keeps output positions correct regardless of how rows are grouped.
func TestTokenBudgetBatchesCoverage(t *testing.T) {
	// Ascending-length inputs (as apply.go feeds): many short, a few long.
	texts := make([]string, 0, 500)
	for i := 0; i < 400; i++ {
		texts = append(texts, "short clause") // ~est 4 tokens
	}
	for i := 0; i < 100; i++ {
		texts = append(texts, string(make([]byte, 1600))) // est=512 (capped)
	}
	batches := tokenBudgetBatches(texts, batchSize*maxTokens)
	if len(batches) == 0 {
		t.Fatal("no batches produced")
	}
	prev := 0
	covered := 0
	for bi, b := range batches {
		if b[0] != prev {
			t.Fatalf("batch %d starts at %d, expected %d (gap/overlap)", bi, b[0], prev)
		}
		if b[1] <= b[0] {
			t.Fatalf("batch %d empty/inverted: [%d,%d)", bi, b[0], b[1])
		}
		rows := b[1] - b[0]
		if rows > maxBatchRows() {
			t.Fatalf("batch %d has %d rows > cap %d", bi, rows, maxBatchRows())
		}
		covered += rows
		prev = b[1]
	}
	if covered != len(texts) {
		t.Fatalf("covered %d clauses, want %d", covered, len(texts))
	}
}

// TestTokenBudgetPacksShortClauses checks the occupancy lever: a batch of short
// clauses holds MANY more than batchSize rows (packing to the token budget), while
// full-length clauses fall back to ~batchSize — the whole point of size-adaptive
// batching.
func TestTokenBudgetPacksShortClauses(t *testing.T) {
	short := make([]string, 1000)
	for i := range short {
		short[i] = "x" // est 1 token → rung 16
	}
	b := tokenBudgetBatches(short, batchSize*maxTokens)
	rows := b[0][1] - b[0][0]
	if rows <= batchSize {
		t.Errorf("short-clause batch packed only %d rows (<= batchSize %d); token budget not exploited", rows, batchSize)
	}

	long := make([]string, 1000)
	for i := range long {
		long[i] = string(make([]byte, 2000)) // est capped at maxTokens → rung maxTokens
	}
	lb := tokenBudgetBatches(long, batchSize*maxTokens)
	lrows := lb[0][1] - lb[0][0]
	if lrows > batchSize {
		t.Errorf("full-length batch packed %d rows (> batchSize %d); should fall back to ~batchSize", lrows, batchSize)
	}
}
