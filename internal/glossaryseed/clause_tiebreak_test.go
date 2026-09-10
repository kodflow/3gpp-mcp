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
