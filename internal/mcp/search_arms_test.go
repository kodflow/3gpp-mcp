package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	mcpgo "github.com/mark3labs/mcp-go/mcp"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/search"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// armsClient serves both halves with several clauses each, so a search reaches
// the cross-encoder on both (a page of one hit has nothing to reorder). The
// environment is read when the server is built, so callers set it first.
func armsClient(t *testing.T) (*client.Client, context.Context) {
	t.Helper()
	ctx := context.Background()
	open := func() *store.Store {
		s, err := store.Open(":memory:")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	}
	st := open()
	_ = st.UpsertSpec(model.Spec{SpecID: "23.502", Series: "23", DocType: "TS"})
	_ = st.UpsertVersion(model.SpecVersion{SpecID: "23.502", Release: "Rel-19", Version: "19.4.0"})
	_ = st.InsertClauses([]model.Clause{
		{ChunkID: 1, SpecID: "23.502", Release: "Rel-19", Version: "19.4.0", ClausePath: "4.2.2",
			Heading: "Registration procedures", Text: "the UE registration procedure with the AMF"},
		{ChunkID: 2, SpecID: "23.502", Release: "Rel-19", Version: "19.4.0", ClausePath: "4.2.3",
			Heading: "Service request", Text: "registration is a prerequisite"},
		{ChunkID: 3, SpecID: "23.502", Release: "Rel-19", Version: "19.4.0", ClausePath: "4.3",
			Heading: "Session management", Text: "after registration the SMF"},
	})
	etsi := open()
	_ = etsi.UpsertSpec(model.Spec{SpecID: "ETSI TS 103 221-1", DocType: "TS"})
	_ = etsi.UpsertVersion(model.SpecVersion{SpecID: "ETSI TS 103 221-1", Release: "ETSI", Version: "1.23.1"})
	_ = etsi.InsertClauses([]model.Clause{
		{ChunkID: 1, SpecID: "ETSI TS 103 221-1", Release: "ETSI", Version: "1.23.1", ClausePath: "6.2",
			Heading: "Task activation", Text: "registration of a target identity"},
		{ChunkID: 2, SpecID: "ETSI TS 103 221-1", Release: "ETSI", Version: "1.23.1", ClausePath: "6.3",
			Heading: "Task deactivation", Text: "registration withdrawn"},
	})
	srv, _ := New(st, "test", "", nil, etsi)
	c, err := client.NewInProcessClient(srv)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	var ir mcpgo.InitializeRequest
	ir.Params.ProtocolVersion = mcpgo.LATEST_PROTOCOL_VERSION
	ir.Params.ClientInfo = mcpgo.Implementation{Name: "test", Version: "1"}
	if _, err := c.Initialize(ctx, ir); err != nil {
		t.Fatal(err)
	}
	return c, ctx
}

func reportsOf(t *testing.T, out map[string]any) []search.Report {
	t.Helper()
	var reps []search.Report
	b, _ := json.Marshal(out["arms"])
	if err := json.Unmarshal(b, &reps); err != nil {
		t.Fatalf("arms is not a list of reports: %s", b)
	}
	return reps
}

// A CLIENT NEVER BELIEVES IT GOT A RERANK IT DID NOT GET. The served image runs a
// 20 s SEARCH_BUDGET and the served hybrid took 80-120 s per call (#340): the
// cross-encoder was skipped on every such call and the answer said nothing. Here
// the budget is spent before the rerank can start, on both halves, and the
// answer must name the arm, the half and the reason — in mode_degraded, which is
// the field the served gate and clients already read.
//
// Falsified: with the handler's degraded block removed, mode_degraded is absent
// and the page is served as if reranked.
func TestARerankTheBudgetSkippedIsNamedInTheAnswer(t *testing.T) {
	t.Setenv("RERANKER", "lexical")
	t.Setenv("EMBEDDER", "off")
	t.Setenv("SEARCH_BUDGET", "1ns")
	c, ctx := armsClient(t)
	out := call(t, c, ctx, "search_spec", map[string]any{
		"query": "registration", "mode": "lexical", "rerank": true, "spec_type": "any"})

	if n, _ := out["count"].(float64); n == 0 {
		t.Fatal("the search answered nothing — the budget must degrade the answer, never empty it")
	}
	md, _ := out["mode_degraded"].(string)
	if !strings.Contains(md, "rerank (federated)") || !strings.Contains(md, "SEARCH_BUDGET") {
		t.Fatalf("mode_degraded = %q — it must name the skipped rerank and the budget that skipped it", md)
	}
	var corpora []string
	for _, r := range reportsOf(t, out) {
		corpora = append(corpora, r.Corpus)
		for _, a := range r.Arms {
			if a.Arm == search.ArmRerank && a.Ran {
				t.Errorf("%s reports a rerank that ran under a spent budget: %+v", r.Corpus, a)
			}
		}
	}
	if got := strings.Join(corpora, ","); !strings.Contains(got, "3gpp") || !strings.Contains(got, "etsi") ||
		!strings.Contains(got, "federated") {
		t.Errorf("arms came from %q, want a report per half and one for the fused rerank", got)
	}
}

// WithWarmup runs one search through each half as soon as the server is built,
// and says what each took — so a session's first query does not pay the cold
// HNSW load inside its budget (search.Engine.Warm).
func TestWarmupRunsEachHalfAndSaysSo(t *testing.T) {
	t.Setenv("EMBEDDER", "off")
	t.Setenv("RERANKER", "off")
	open := func() *store.Store {
		s, err := store.Open(":memory:")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		_ = s.InsertClauses([]model.Clause{{ChunkID: 1, SpecID: "23.502", Release: "Rel-19", Version: "19.4.0",
			ClausePath: "4.2.2", Heading: "Registration", Text: "the registration procedure"}})
		return s
	}
	lines := make(chan string, 4)
	var warmed sync.WaitGroup
	ctx, stop := context.WithCancel(context.Background())
	defer func() { stop(); warmed.Wait() }()
	New(open(), "test", "", nil, open(),
		WithWarmup(ctx, &warmed, func(f string, a ...any) { lines <- fmt.Sprintf(f, a...) }))
	for _, half := range []string{"3gpp", "etsi"} {
		select {
		case l := <-lines:
			if !strings.Contains(l, "warm-up of the "+half+" half") || !strings.Contains(l, "lexical") {
				t.Fatalf("warm-up line %q, want the %s half with its arms", l, half)
			}
		case <-time.After(30 * time.Second):
			t.Fatalf("no warm-up line for the %s half", half)
		}
	}
}

// slowStore answers its lexical query after d, as a cold, loaded corpus does.
type slowStore struct {
	store.Reader
	d time.Duration
}

func (s slowStore) SearchClauses(ctx context.Context, q store.SearchQuery) ([]model.SearchHit, error) {
	time.Sleep(s.d)
	return s.Reader.SearchClauses(ctx, q)
}

// ONE BUDGET PER CALL, ACROSS BOTH HALVES AND THE PASS THAT FOLLOWS THEM. The
// 3GPP half here takes longer than the whole budget; what comes after it — the
// ETSI pass and the cross-encoder over the merged head — must find the budget
// spent and say so, rather than each starting a fresh SEARCH_BUDGET of its own,
// which is how a "20 s" federated call used to be able to spend three.
//
// Falsified: without search.WithBudget in searchSpec, the fused rerank gets its
// own budget, runs, and this fails.
func TestAFederatedCallSpendsOneBudget(t *testing.T) {
	t.Setenv("EMBEDDER", "off")
	t.Setenv("RERANKER", "lexical")
	t.Setenv("SEARCH_BUDGET", "300ms")
	ctx := context.Background()
	open := func(spec, rel string) *store.Store {
		s, err := store.Open(":memory:")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		_ = s.UpsertSpec(model.Spec{SpecID: spec, DocType: "TS"})
		_ = s.InsertClauses([]model.Clause{
			{ChunkID: 1, SpecID: spec, Release: rel, Version: "1.0.0", ClausePath: "1", Heading: "A", Text: "registration one"},
			{ChunkID: 2, SpecID: spec, Release: rel, Version: "1.0.0", ClausePath: "2", Heading: "B", Text: "registration two"},
		})
		return s
	}
	srv, _ := New(slowStore{Reader: open("23.502", "Rel-19"), d: 500 * time.Millisecond},
		"test", "", nil, open("ETSI TS 103 221-1", "ETSI"))
	c, err := client.NewInProcessClient(srv)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	var ir mcpgo.InitializeRequest
	ir.Params.ProtocolVersion = mcpgo.LATEST_PROTOCOL_VERSION
	ir.Params.ClientInfo = mcpgo.Implementation{Name: "test", Version: "1"}
	if _, err := c.Initialize(ctx, ir); err != nil {
		t.Fatal(err)
	}
	out := call(t, c, ctx, "search_spec", map[string]any{
		"query": "registration", "mode": "lexical", "rerank": true, "spec_type": "any"})

	if n, _ := out["count"].(float64); n == 0 {
		t.Fatal("the call answered nothing — a spent budget degrades the answer, never empties it")
	}
	arms := map[string]search.ArmRun{}
	for _, r := range reportsOf(t, out) {
		for _, a := range r.Arms {
			arms[r.Corpus+"/"+a.Arm] = a
		}
	}
	if a := arms["federated/rerank"]; a.Ran || !strings.Contains(a.Skipped, "SEARCH_BUDGET=300ms") {
		t.Fatalf("the fused rerank = %+v, want skipped on the call's spent budget", a)
	}
	if md, _ := out["mode_degraded"].(string); !strings.Contains(md, "rerank (federated)") {
		t.Fatalf("mode_degraded = %q, want the fused rerank named", md)
	}
	// The lexical arm of the second half still runs: degrade, never block.
	if a := arms["etsi/lexical"]; !a.Ran {
		t.Fatalf("the ETSI lexical arm = %+v, want it to run whatever the budget", a)
	}
}
