package mcp

// rerank_test.go — where the cross-encoder runs for one search_spec call.
//
// The routing decision is in internal/mcp (federated_rerank.go): a call that
// fuses more than one search reranks the merged head ONCE, a call that runs a
// single search reranks inside it, as it always did. The engine half of the same
// route is internal/search/rerank_test.go.

import (
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/search"
)

// The control: the same call with no budget pressure carries no mode_degraded,
// and the arms say the rerank ran, once, over the merged head — the note is not
// a constant, and the fused pass is where the cross-encoder now runs.
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
	n := 0
	for _, r := range reportsOf(t, out) {
		for _, a := range r.Arms {
			if a.Arm == search.ArmRerank {
				n++
				if a.Ran {
					ran[r.Corpus] = true
				}
			}
		}
	}
	if !ran["federated"] || n != 1 {
		t.Fatalf("rerank arms = %v over %d pass(es), want exactly one, over the fused head", ran, n)
	}
}

// ALWAYS-ON RERANK GOES THROUGH THE SAME ONE PASS. Engine.Search reranks when
// `r.Rerank || rerankAll`, so suppressing only the request flag would leave
// RERANK_ALL=1 — the profile deploy/labs-8c32g-env.conf ships — running a
// cross-encoder pass per half AND the fused one: four passes for one call, worse
// than the three this PR removes (Qodo, #348).
//
// Falsified two ways: with planRerank ignoring RerankEvery, no pass runs at all;
// with the halves not told to defer, three do.
func TestAlwaysOnRerankStillRunsExactlyOncePerFederatedCall(t *testing.T) {
	t.Setenv("RERANKER", "lexical")
	t.Setenv("EMBEDDER", "off")
	t.Setenv("SEARCH_BUDGET", "0")
	t.Setenv("RERANK_ALL", "1")
	c, ctx := armsClient(t)
	for _, asked := range []bool{false, true} {
		out := call(t, c, ctx, "search_spec", map[string]any{
			"query": "registration", "mode": "lexical", "rerank": asked, "spec_type": "any"})
		ran, n := map[string]bool{}, 0
		for _, r := range reportsOf(t, out) {
			for _, a := range r.Arms {
				if a.Arm == search.ArmRerank {
					n++
					if a.Ran {
						ran[r.Corpus] = true
					}
				}
			}
		}
		if !ran["federated"] || n != 1 {
			t.Fatalf("rerank=%v with RERANK_ALL=1: arms %v over %d pass(es), want exactly one, over the fused head",
				asked, ran, n)
		}
	}
}

// A SCOPED CALL KEEPS ITS OWN PASS. One search feeds the page when the query
// names an ETSI deliverable, so the cross-encoder runs inside that search, over
// that half's window — the fused route is for calls that merge halves, and
// routing a single search through it would rerank a list nothing had fused.
func TestAScopedSearchRerankInsideItsOwnHalf(t *testing.T) {
	t.Setenv("RERANKER", "lexical")
	t.Setenv("EMBEDDER", "off")
	t.Setenv("SEARCH_BUDGET", "0")
	c, ctx := armsClient(t)
	out := call(t, c, ctx, "search_spec", map[string]any{
		"query": "registration", "spec_id": "ETSI TS 103 221-1", "mode": "lexical", "rerank": true})

	ran, n := map[string]bool{}, 0
	for _, r := range reportsOf(t, out) {
		for _, a := range r.Arms {
			if a.Arm == search.ArmRerank {
				n++
				if a.Ran {
					ran[r.Corpus] = true
				}
			}
		}
	}
	if !ran["etsi"] || ran["federated"] || n != 1 {
		t.Fatalf("rerank arms = %v over %d pass(es), want exactly one, inside the ETSI half", ran, n)
	}
}
