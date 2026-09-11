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
// costs a third of that and is the only shape in which a cross-encoded page over
// both halves fits any budget at all.
//
// WHAT CHANGES, AND WHAT DOES NOT. Nothing changes for a server with one half, or
// for an ETSI-scoped query (one pass either way): the rerank stays inside
// Engine.Search, over the same window, and the served retrieval gate — 3GPP half
// only — scores exactly what it scored before (verified bit for bit, page and
// score, on the judged queries). On a federated call the window is now the head
// of the merged list rather than each half's own head, so the cross-encoder
// compares 3GPP and ETSI candidates against each other instead of ranking them
// separately and interleaving them. That is a ranking change on the federated
// path, deliberately: it is the arm doing what the answer says it does.
// RERANK_WINDOW widens the window for an operator who wants the old breadth.

// armRerank reports whether each half's own Search should run the cross-encoder,
// or whether this call reranks once after the halves are merged.
func (h *handlers) armRerank(rerank, federated, etsiScoped bool) bool {
	return rerank && !h.rerankFusedWanted(rerank, federated, etsiScoped)
}

// rerankFusedWanted is true when more than one search feeds the page: the 3GPP
// half plus the ETSI half.
func (h *handlers) rerankFusedWanted(rerank, federated, etsiScoped bool) bool {
	return rerank && h.etsiEng != nil && federated && !etsiScoped
}

// rerankFused re-scores the head of the merged list with the cross-encoder, and
// reports the arm under the corpus "federated" so the answer says which pass ran.
func (h *handlers) rerankFused(ctx context.Context, query string, hits []model.SearchHit) []model.SearchHit {
	return h.eng.RerankFused(ctx, query, hits, "federated")
}
