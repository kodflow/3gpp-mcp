package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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
	if !strings.Contains(md, "rerank (3gpp") || !strings.Contains(md, "rerank (etsi") || !strings.Contains(md, "SEARCH_BUDGET") {
		t.Fatalf("mode_degraded = %q — it must name the skipped rerank on each half and the budget that skipped it", md)
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
	if got := strings.Join(corpora, ","); !strings.Contains(got, "3gpp") || !strings.Contains(got, "etsi") {
		t.Errorf("arms came from %q, want a report per half", got)
	}
}

// The control: the same call with no budget pressure carries no mode_degraded,
// and the arms say the rerank ran on each half — the note is not a constant.
func TestARerankThatRanIsReportedAndNothingIsDegraded(t *testing.T) {
	t.Setenv("RERANKER", "lexical")
	t.Setenv("EMBEDDER", "off")
	t.Setenv("SEARCH_BUDGET", "0")
	c, ctx := armsClient(t)
	out := call(t, c, ctx, "search_spec", map[string]any{
		"query": "registration", "mode": "lexical", "rerank": true, "spec_type": "any"})

	if md, ok := out["mode_degraded"]; ok {
		t.Fatalf("mode_degraded = %v on a call where every requested arm ran", md)
	}
	ran := map[string]bool{}
	for _, r := range reportsOf(t, out) {
		for _, a := range r.Arms {
			if a.Arm == search.ArmRerank && a.Ran {
				ran[r.Corpus] = true
			}
		}
	}
	if !ran["3gpp"] || !ran["etsi"] {
		t.Fatalf("rerank ran on %v, want both halves", ran)
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
	New(open(), "test", "", nil, open(), WithWarmup(func(f string, a ...any) { lines <- fmt.Sprintf(f, a...) }))
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
