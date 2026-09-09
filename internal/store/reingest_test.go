package store

import (
	"strings"
	"testing"
)

// TestSummariseReingestedCountsAllButNamesFew pins the reporting defect: the
// sample was used as the total.
//
// The detail line reported len(offenders) as the number of deliverables while the
// excess was summed over every scanned group, so with more than ten offenders it
// said "10 deliverable(s)" beside an excess taken from all of them — a message
// that contradicts itself exactly when the damage is worst.
func TestSummariseReingestedCountsAllButNamesFew(t *testing.T) {
	var rs []Reingested
	for i := 0; i < 30; i++ {
		// 10 rows at 2 copies → 5 excess each.
		rs = append(rs, Reingested{SpecID: "23.50" + string(rune('0'+i%10)), Release: "Rel-18", Version: "18.0.0", Copies: 2, Held: 10})
	}
	groups, excess, named := SummariseReingested(rs, 10)
	if groups != 30 {
		t.Errorf("groups = %d, want 30 — the count must be the total, not the sample", groups)
	}
	if excess != 150 {
		t.Errorf("excess = %d, want 150 (30 groups × 5)", excess)
	}
	if !strings.Contains(named, "and 20 more") {
		t.Errorf("a truncated sample must say how many it left out: %q", named)
	}
	if strings.Count(named, "copies") != 10 {
		t.Errorf("want 10 named offenders, got %d: %q", strings.Count(named, "copies"), named)
	}
}

// TestExcessIsPerExtraWriteNotPerDuplicateRow pins the arithmetic.
//
// A document that legitimately repeats a clause carries those repeats in EVERY
// copy, so counting distinct rows over-reports what the extra writes added. Here:
// a 3-row document (one clause twice) written twice is 6 rows held, of which 3
// are excess — not 6 - 2 = 4, which is what distinct-row counting gives.
func TestExcessIsPerExtraWriteNotPerDuplicateRow(t *testing.T) {
	r := Reingested{SpecID: "23.501", Release: "Rel-18", Version: "18.0.0", Copies: 2, Held: 6}
	if got := r.Excess(); got != 3 {
		t.Errorf("Excess() = %d, want 3 — one of the two writes is the excess", got)
	}
	if !strings.Contains(r.String(), "2 copies") {
		t.Errorf("the multiplicity is the diagnosis and must be rendered: %q", r.String())
	}
}
