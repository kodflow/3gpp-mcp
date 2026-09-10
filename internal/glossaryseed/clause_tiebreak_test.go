package glossaryseed

import "testing"

// TestEarlierKeepsLengthFirst pins the half of the order that was already there.
//
// readSpec used to take the FIRST candidate GetClauses handed back, which made the
// store's `ORDER BY len(clause_path), clause_path` the de-facto rule. Replacing
// that with an explicit comparison is only safe if the explicit comparison is the
// SAME rule: compared as plain strings "10.2" sorts before "3.2", so dropping the
// length key would quietly re-seed the glossary from a different clause for every
// spec that has more than one — a silent content change introduced by a fix for
// silent content changes.
func TestEarlierKeepsLengthFirst(t *testing.T) {
	if !earlier("3.2", 1, "10.2", 1) {
		t.Error(`"3.2" must sort before "10.2": the store orders by len(clause_path) first, ` +
			`and plain string comparison reverses exactly this pair`)
	}
	if earlier("10.2", 1, "3.2", 1) {
		t.Error(`"10.2" must NOT sort before "3.2"`)
	}
	if !earlier("3.2", 1, "3.3", 1) {
		t.Error("at equal length the comparison is lexicographic")
	}
}

// TestEarlierBreaksTheTieThatCompactCouldFlip is the point of the function.
//
// Two rows with the same clause_path — the same clause catalogued under more than
// one release, or a document that repeats it — were left tied by the store's
// ORDER BY, so the winner was whatever physical order the table happened to be in.
// Measured on the corpus published 2026-09-10: 41 such ties, 8 of them with rows
// carrying DIFFERENT TEXT.
func TestEarlierBreaksTheTieThatCompactCouldFlip(t *testing.T) {
	if !earlier("3.2", 7, "3.2", 9) {
		t.Error("identical paths must be settled by the chunk id, which is a stored " +
			"value compaction preserves")
	}
	if earlier("3.2", 9, "3.2", 7) {
		t.Error("the chunk-id comparison must be strict, not reversed")
	}
	if earlier("3.2", 7, "3.2", 7) {
		t.Error("a row must not sort before itself, or the selection loop can oscillate")
	}
}

// TestEarlierCountsCharactersNotBytes pins the half of the order that has to match
// SQL rather than Go.
//
// GetClauses orders by `length(clause_path)`, which in DuckDB counts CHARACTERS.
// Go's len() counts bytes. They agree on ASCII and part company on anything else:
// "3.١" is three characters and four bytes, so SQL places it before "3.10"
// and a byte comparison places it after. Measured 2026-09-10: zero non-ASCII clause
// paths in either corpus — so this is not today's drift, but rust/parse's salvage
// path captures `\d`, which Unicode digits satisfy, so it is reachable.
func TestEarlierCountsCharactersNotBytes(t *testing.T) {
	const arabicIndicOne = "3.١" // 3 runes, 4 bytes
	if got := len(arabicIndicOne); got != 4 {
		t.Fatalf("the fixture must be a multi-byte path, got %d bytes", got)
	}
	if !earlier(arabicIndicOne, 1, "3.10", 1) {
		t.Error("a 3-character path must sort before a 4-character one, as SQL length() " +
			"orders them; comparing bytes reverses this pair")
	}
}

// TestBetterCandidatePrefersTheClauseThatDeclaresMore pins the correction that the
// adversarial review forced.
//
// Ranking tied clauses by position alone is deterministic and demonstrably picks
// worse text. Measured on the corpus published 2026-09-10: for 38.475 v0.3.0 §3.2
// the lowest chunk is the clause's introduction while its tied sibling defines
// gNB-CU, gNB-DU and gNB; for 33.802 v0.2.0 §3.3 the lowest chunk is an
// "<ACRONYM> <Explanation>" template. This function exists to mine abbreviations,
// so the clause that declares more of them is more of what was asked for.
func TestBetterCandidatePrefersTheClauseThatDeclaresMore(t *testing.T) {
	// Same path, and the richer clause sits at the HIGHER chunk — which is exactly
	// the shape both measured cases have, and the one position alone gets wrong.
	if !betterCandidate(12, "3.2", 9752140, 0, "3.2", 9752097) {
		t.Error("a clause yielding 12 entries must beat one yielding none, whatever their " +
			"chunk ids")
	}
	if betterCandidate(0, "3.2", 9752097, 12, "3.2", 9752140) {
		t.Error("and the poorer clause must not win by sitting earlier")
	}
	// Position still decides, and only when the yields tie.
	if !betterCandidate(5, "3.2", 1, 5, "3.2", 2) {
		t.Error("equal yields must fall back to the positional order")
	}
	if betterCandidate(5, "3.2", 2, 5, "3.2", 1) {
		t.Error("and that fallback must stay strict")
	}
}

// TestEarlierIsATotalOrder is what makes "same corpus, same answer" true rather
// than likely.
//
// The selection loop keeps the minimum under `earlier`. A minimum is only
// well-defined if the relation is a strict total order: if any pair compared both
// ways came out true, the winner would depend on the order the candidates arrived
// in — which is precisely the defect being removed, reintroduced one level down.
func TestEarlierIsATotalOrder(t *testing.T) {
	type cand struct {
		path  string
		chunk uint64
	}
	all := []cand{
		{"3", 1}, {"3.2", 1}, {"3.2", 2}, {"3.3", 1},
		{"10.2", 1}, {"10.2", 2}, {"4.1.2", 5}, {"", 0},
	}
	for _, a := range all {
		for _, b := range all {
			ab := earlier(a.path, a.chunk, b.path, b.chunk)
			ba := earlier(b.path, b.chunk, a.path, a.chunk)
			same := a == b
			switch {
			case same && (ab || ba):
				t.Errorf("%v is not earlier than itself", a)
			case !same && ab == ba:
				t.Errorf("%v and %v compare the same way in both directions (%v); "+
					"the minimum is then whatever order they arrive in", a, b, ab)
			}
		}
	}
	// Transitivity, checked rather than assumed: without it the loop's running
	// minimum can be beaten by a candidate that a later one does not beat.
	for _, a := range all {
		for _, b := range all {
			for _, c := range all {
				if earlier(a.path, a.chunk, b.path, b.chunk) &&
					earlier(b.path, b.chunk, c.path, c.chunk) &&
					!earlier(a.path, a.chunk, c.path, c.chunk) {
					t.Errorf("not transitive: %v < %v < %v but not %v < %v", a, b, c, a, c)
				}
			}
		}
	}
}
