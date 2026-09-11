package store

// stableScore wraps a summed relevance score so that two scores equal in exact
// arithmetic compare equal in the ORDER BY — and the ranking stops changing from
// one run to the next.
//
// WHY. BM25 (the FTS macro) and the sparse dot product are SUMS of floating-point
// terms, and DuckDB aggregates them in parallel, in whatever order its threads
// deliver: the same paragraph scores 11.238970209866642 on one run and
// 11.23897020986664 on the next. Templated clauses are common in this corpus —
// TS 33.128 6.3.3.2.2, 6.3.3.2.5 and 6.3.3.2.6 each carry a paragraph of the same
// BM25 score for "SMF PDU session establishment xIRI" — so the last bit decided
// their order, and the tie-breaks written after the score (spec_id, clause_path)
// never got a say. Measured on the published 3GPP half, 2026-09-11: four identical
// lexical calls gave three different orders of those three clauses, and through
// RRF a different hybrid page (6.3.3.2.6 moved from rank 5 to rank 3). A served
// page that changes between two identical calls cannot be proven equal to anything,
// and the served gate's own history shows it (0.50 then 0.43 on one query, fixed
// at the fusion in #340 — the arms upstream of it had the same defect).
//
// Nine decimals: these scores are O(1)-O(100), a summation-order error is ~1e-15
// relative, and no two genuinely different scores the ranking could care about are
// closer than 1e-9. Rounding only merges scores that differ by less than that; the
// order among them is then the tie-break's, which is the point.
func stableScore(sumExpr string) string {
	return "round(" + sumExpr + ", 9)"
}
