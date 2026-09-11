package mcp

import (
	"context"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// ONE CROSS-ENCODER PASS PER CALL, ON THE PAGE THE CLIENT WILL SEE.
//
// A federated search_spec runs the 3GPP engine and then the ETSI engine (once per
// normative document type), and each of them reranked its OWN window before the
// halves were merged. Three cross-encoder passes for one call — and the merge is
// RRF, which is rank-based, so the scores those passes computed were thrown away
// and the page came back interleaved 1:1 between the halves. A client that asked
// for a reranked page got three reranked lists shuffled together by rank.
//
// Measured on this machine (4 cores, both halves, SEARCH_BUDGET off, 2026-09-11):
// hybrid+rerank took 67-86 s per call, of which 60-62 s were the three passes —
// against the image's 20 s budget, under which the ETSI passes are skipped
// anyway, now visibly (internal/search/report.go). One pass over the FUSED head
// costs a third of that, and on the judged queries it ranks BETTER: nDCG@5 0.388
// → 0.660, nDCG@10 0.525 → 0.660, MRR@10 0.667 → 0.750 (both halves, budget off).
//
// WHAT CHANGES, AND WHAT DOES NOT. Nothing changes for a server with one half, or
// for an ETSI-scoped query (one pass either way): the rerank stays inside
// Engine.Search, over the same window, and the served retrieval gate — 3GPP half
// only — scores exactly what it scored before (verified bit for bit, page and
// score, on the judged queries). On a federated call the window is now the head
// of the merged list rather than each half's own head, so the cross-encoder
// compares 3GPP and ETSI candidates against each other instead of ranking them
// separately and interleaving them. RERANK_WINDOW widens the window.

// rerankPlan says where the cross-encoder runs for one call.
//
// THE ALWAYS-RERANK TOGGLE IS PART OF THE DECISION, not just the request flag
// (Qodo, #348): Engine.Search reranks when `r.Rerank || rerankAll`, so a
// federated call under RERANK_ALL=1 — the profile deploy/labs-8c32g-env.conf
// ships — would have run a pass per half AND the fused one, four passes where
// there used to be three. The per-half searches are told to DEFER, which
// suppresses both, and the decision is applied once, here.
type rerankPlan struct {
	arm    bool // each half's own Search reranks its window (one search feeds the page)
	defer_ bool // the halves rerank nothing; this call reranks the merged head
	fused  bool // …and it is wanted (asked for, or always-on)
}

// planRerank takes `federated` — the caller's own predicate for "this call asks
// both halves" — and nothing else about the query. An ETSI-scoped call is never
// federated (it names a spec_id), so it lands in the single-search branch by that
// fact rather than by a second test of its own.
func (h *handlers) planRerank(rerank, federated bool) rerankPlan {
	if h.etsiEng == nil || !federated {
		// One search feeds the page: it reranks its own window, as it always did —
		// including for the always-rerank toggle, which Search reads itself.
		return rerankPlan{arm: rerank}
	}
	return rerankPlan{defer_: true, fused: rerank || h.eng.RerankEvery() || h.etsiEng.RerankEvery()}
}

// rerankFused re-scores the head of the merged list with the cross-encoder, and
// reports the arm under the corpus "federated" so the answer says which pass ran.
func (h *handlers) rerankFused(ctx context.Context, query string, hits []model.SearchHit) []model.SearchHit {
	return h.eng.RerankFused(ctx, query, hits, "federated")
}
