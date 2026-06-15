package search

import (
	"context"
	"testing"
	"time"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// TestSearchBudgetFor pins SEARCH_BUDGET parsing: a Go duration, a bare integer
// of seconds, "0"/negative (disabled), and the unset default.
func TestSearchBudgetFor(t *testing.T) {
	cases := []struct {
		env  string
		want time.Duration
	}{
		{"", defaultSearchBudget},
		{"8s", 8 * time.Second},
		{"500ms", 500 * time.Millisecond},
		{"5", 5 * time.Second},
		{"0", 0},
		{"-3s", -3 * time.Second},
		{"garbage", defaultSearchBudget},
	}
	for _, c := range cases {
		// An empty value is treated as "unset" by searchBudgetFor → default.
		t.Setenv("SEARCH_BUDGET", c.env)
		if got := searchBudgetFor(); got != c.want {
			t.Errorf("SEARCH_BUDGET=%q: got %v, want %v", c.env, got, c.want)
		}
	}
}

// TestSearchBudgetDegradesNotBlocks: an already-expired budget must still return
// the lexical results (which run before any expensive arm) rather than nothing or
// an error — degrade, never block (CLAUDE.md §1).
func TestSearchBudgetDegradesNotBlocks(t *testing.T) {
	t.Setenv("EMBEDDER", "off")
	t.Setenv("SEARCH_BUDGET", "1ns") // expires essentially immediately
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if err := st.InsertClauses([]model.Clause{
		{ChunkID: 1, SpecID: "23.501", Release: "Rel-19", Version: "19.6.0", ClausePath: "5.2", Heading: "Registration", Text: "the ue registration procedure"},
	}); err != nil {
		t.Fatal(err)
	}
	eng := New(st)
	hits, err := eng.Search(ctx, Request{Text: "registration", Mode: "hybrid", TopK: 5})
	if err != nil {
		t.Fatalf("expired-budget search errored: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("expired-budget search returned no hits (degrade-don't-block violated)")
	}
}
