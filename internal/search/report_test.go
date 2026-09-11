package search

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/rerank"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// reportStore is a store with three clauses a lexical query for "registration"
// ranks, so a reranker has a window to reorder.
func reportStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.InsertClauses([]model.Clause{
		{ChunkID: 1, SpecID: "23.502", Release: "Rel-19", Version: "19.4.0", ClausePath: "4.2.2", Heading: "Registration", Text: "the UE registration procedure"},
		{ChunkID: 2, SpecID: "23.502", Release: "Rel-19", Version: "19.4.0", ClausePath: "4.2.3", Heading: "Service request", Text: "registration first"},
		{ChunkID: 3, SpecID: "23.502", Release: "Rel-19", Version: "19.4.0", ClausePath: "4.3", Heading: "Sessions", Text: "after registration"},
	}); err != nil {
		t.Fatal(err)
	}
	return st
}

// slowReranker takes d per call and ignores the context, as an in-flight ONNX
// Run does.
type slowReranker struct{ d time.Duration }

func (slowReranker) Enabled() bool { return true }
func (s slowReranker) Score(_ context.Context, _ string, p []string) ([]float64, error) {
	time.Sleep(s.d)
	out := make([]float64, len(p))
	for i := range out {
		out[i] = float64(i) // reverses nothing in particular; the order is not under test
	}
	return out, nil
}

type failingReranker struct{}

func (failingReranker) Enabled() bool { return true }
func (failingReranker) Score(context.Context, string, []string) ([]float64, error) {
	return nil, errors.New("onnx run: out of memory")
}

func armOf(t *testing.T, tr *Trace, i int, arm string) ArmRun {
	t.Helper()
	reps := tr.Reports()
	if len(reps) <= i {
		t.Fatalf("%d report(s) recorded, want at least %d", len(reps), i+1)
	}
	for _, a := range reps[i].Arms {
		if a.Arm == arm {
			return a
		}
	}
	t.Fatalf("report %d has no %s arm: %+v", i, arm, reps[i])
	return ArmRun{}
}

// A CROSS-ENCODER THAT FAILS IS NAMED, not hidden behind the fused order it falls
// back to. Engine.rerank keeps the RRF order on any error — right — and nothing
// said so; the served gate could only infer it from a page that never moved.
func TestAFailingRerankIsReportedWithItsError(t *testing.T) {
	t.Setenv("EMBEDDER", "off")
	t.Setenv("SEARCH_BUDGET", "0")
	eng := New(reportStore(t))
	eng.SetName("3gpp")
	eng.rr = failingReranker{}
	ctx, tr := WithTrace(context.Background())
	hits, err := eng.Search(ctx, Request{Text: "registration", Mode: "lexical", TopK: 3, Rerank: true})
	if err != nil || len(hits) == 0 {
		t.Fatalf("a failing reranker must degrade the answer, not fail it: %v, %d hits", err, len(hits))
	}
	a := armOf(t, tr, 0, ArmRerank)
	if a.Ran || !strings.Contains(a.Skipped, "out of memory") {
		t.Fatalf("rerank arm = %+v, want skipped with the cross-encoder's error", a)
	}
	deg := Degraded(tr.Reports())
	if len(deg) != 1 || !strings.HasPrefix(deg[0], "rerank (3gpp): ") {
		t.Fatalf("Degraded = %q, want the one skipped arm, named with its corpus", deg)
	}
}

// ONE BUDGET PER REQUEST. search_spec runs up to three searches for one call (the
// 3GPP half, then the ETSI half once per normative type); each used to start a
// SEARCH_BUDGET of its own, so a "20 s" call could spend three. Under WithBudget
// the second search inherits what the first left: here nothing, so its rerank is
// skipped — and says why.
//
// Falsified: with WithBudget marking the context but starting no deadline, the
// second search runs its rerank and the test fails. (The handler side — that
// search_spec starts ONE budget for all its passes — is
// TestAFederatedCallSpendsOneBudget in internal/mcp.)
func TestSearchesUnderOneRequestShareItsBudget(t *testing.T) {
	t.Setenv("EMBEDDER", "off")
	t.Setenv("SEARCH_BUDGET", "150ms")
	first := New(reportStore(t))
	first.rr = slowReranker{d: 300 * time.Millisecond}
	second := NewSharing(reportStore(t), first)

	ctx, tr := WithTrace(context.Background())
	ctx, cancel := WithBudget(ctx)
	defer cancel()
	for _, e := range []*Engine{first, second} {
		if _, err := e.Search(ctx, Request{Text: "registration", Mode: "lexical", TopK: 3, Rerank: true}); err != nil {
			t.Fatal(err)
		}
	}
	if a := armOf(t, tr, 0, ArmRerank); !a.Ran {
		t.Fatalf("the first search's rerank started inside the budget and must have run: %+v", a)
	}
	a := armOf(t, tr, 1, ArmRerank)
	if a.Ran || !strings.Contains(a.Skipped, "SEARCH_BUDGET=150ms") {
		t.Fatalf("the second search's rerank = %+v, want skipped on the request's spent budget", a)
	}
	if l := armOf(t, tr, 1, ArmLexical); !l.Ran {
		t.Fatalf("the lexical arm must run whatever the budget: %+v", l)
	}
}

// Without a request budget, Search still bounds itself: the pre-existing contract
// (degrade, never block) is unchanged for every caller that does not start one.
func TestASearchWithoutARequestBudgetStartsItsOwn(t *testing.T) {
	t.Setenv("EMBEDDER", "off")
	t.Setenv("SEARCH_BUDGET", "1ns")
	eng := New(reportStore(t))
	eng.rr = rerank.Lexical{}
	ctx, tr := WithTrace(context.Background())
	hits, err := eng.Search(ctx, Request{Text: "registration", Mode: "lexical", TopK: 3, Rerank: true})
	if err != nil || len(hits) == 0 {
		t.Fatalf("expired budget: %v, %d hits", err, len(hits))
	}
	if a := armOf(t, tr, 0, ArmRerank); a.Ran || !strings.Contains(a.Skipped, "SEARCH_BUDGET=1ns") {
		t.Fatalf("rerank arm = %+v, want skipped on this search's own budget", a)
	}
}

// Every requested arm is in the report, and an arm the engine cannot run says so:
// a hybrid request on a server with no embedder names the dense and sparse arms
// it did not get, instead of leaving them out.
func TestEveryRequestedArmIsReported(t *testing.T) {
	t.Setenv("EMBEDDER", "off")
	t.Setenv("RERANKER", "off")
	t.Setenv("SEARCH_BUDGET", "0")
	eng := New(reportStore(t))
	ctx, tr := WithTrace(context.Background())
	if _, err := eng.Search(ctx, Request{Text: "registration", Mode: "hybrid", TopK: 3, Rerank: true}); err != nil {
		t.Fatal(err)
	}
	if a := armOf(t, tr, 0, ArmLexical); !a.Ran || a.Hits == 0 {
		t.Errorf("lexical = %+v", a)
	}
	for _, arm := range []string{ArmDense, ArmSparse, ArmRerank} {
		if a := armOf(t, tr, 0, arm); a.Ran || a.Skipped == "" {
			t.Errorf("%s = %+v, want skipped with a reason", arm, a)
		}
	}
	if got := len(Degraded(tr.Reports())); got != 3 {
		t.Errorf("Degraded names %d arms, want dense, sparse and rerank", got)
	}

	// A lexical request without rerank asks for one arm, and reports one.
	ctx, tr = WithTrace(context.Background())
	if _, err := eng.Search(ctx, Request{Text: "registration", Mode: "lexical", TopK: 3}); err != nil {
		t.Fatal(err)
	}
	if reps := tr.Reports(); len(reps) != 1 || len(reps[0].Arms) != 1 || len(Degraded(reps)) != 0 {
		t.Errorf("lexical-only report = %+v", reps)
	}
}

// The ETSI engine shares the 3GPP engine's models rather than loading a second
// cross-encoder; its store and name stay its own.
func TestNewSharingSharesTheModelsOnly(t *testing.T) {
	t.Setenv("EMBEDDER", "local")
	t.Setenv("RERANKER", "lexical")
	a := New(reportStore(t))
	a.SetName("3gpp")
	a.rr = &slowReranker{d: time.Nanosecond} // a pointer: identity, not value equality
	b := NewSharing(reportStore(t), a)
	if b.rr != a.rr || b.emb != a.emb || b.sp != a.sp {
		t.Fatal("NewSharing loaded its own models")
	}
	if b.st == a.st || b.name != "" {
		t.Fatal("NewSharing shared the store or the name")
	}
}
