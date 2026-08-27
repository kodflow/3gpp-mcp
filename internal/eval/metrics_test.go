package eval

import "testing"

func refs(pairs ...string) []Ref {
	out := make([]Ref, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, Ref{SpecID: "33.128", Clause: p})
	}
	return out
}

func TestPerfectRanking(t *testing.T) {
	rels := []Relevant{{SpecID: "33.128", Clause: "6.2.2.2.2", Grade: 2}}
	m := Score(refs("6.2.2.2.2", "9.9", "8.8"), rels)
	if m.NDCG5 != 1 {
		t.Errorf("nDCG@5 = %v, want 1", m.NDCG5)
	}
	if m.MRR != 1 {
		t.Errorf("MRR = %v, want 1", m.MRR)
	}
	if m.Success1 != 1 {
		t.Errorf("Success@1 = %v, want 1", m.Success1)
	}
	if m.Recall5 != 1 {
		t.Errorf("Recall@5 = %v, want 1", m.Recall5)
	}
}

// Regression: multiple retrieved descendants of ONE target must not push nDCG
// above 1 (each judgement is credited at most once).
func TestNDCGNeverExceedsOne(t *testing.T) {
	rels := []Relevant{{SpecID: "33.128", Clause: "6.2.2.2", Grade: 2}}
	m := Score(refs("6.2.2.2.2", "6.2.2.2.3", "6.2.2.2.4", "6.2.2.2"), rels)
	if m.NDCG5 > 1.0000001 {
		t.Errorf("nDCG@5 = %v, must be <= 1", m.NDCG5)
	}
	if m.NDCG10 > 1.0000001 {
		t.Errorf("nDCG@10 = %v, must be <= 1", m.NDCG10)
	}
}

func TestMissAndDescendant(t *testing.T) {
	rels := []Relevant{{SpecID: "33.128", Clause: "6.2.2.2", Grade: 2}}
	// Only a descendant retrieved (grade capped to 1), at rank 1.
	m := Score(refs("6.2.2.2.2"), rels)
	if m.Success1 != 0 {
		t.Errorf("Success@1 = %v, want 0 (descendant is not the exact grade-2 hit)", m.Success1)
	}
	if m.MRR != 1 {
		t.Errorf("MRR = %v, want 1 (descendant is relevant)", m.MRR)
	}
	// The metric that used to disagree: recall credited only an EXACT clause
	// string, so this same ranking scored MRR 1 / nDCG > 0 next to Recall 0.
	if m.Recall5 != 1 {
		t.Errorf("Recall@5 = %v, want 1 — recall must use the same relevance as nDCG/MRR, "+
			"which credit a descendant of the target", m.Recall5)
	}

	// Nothing relevant retrieved.
	none := Score(refs("9.9", "8.8"), rels)
	if none.MRR != 0 || none.NDCG5 != 0 || none.Recall5 != 0 {
		t.Errorf("irrelevant ranking should score 0: %+v", none)
	}
}

// TestRecallNeverExceedsOne is the counterpart of TestNDCGNeverExceedsOne: with
// descendants now credited, several of them under ONE target must still count
// once. A recall above 1 would silently inflate the gate's baseline.
func TestRecallNeverExceedsOne(t *testing.T) {
	rels := []Relevant{{SpecID: "33.128", Clause: "6.2.2.2", Grade: 2}}
	m := Score(refs("6.2.2.2.2", "6.2.2.2.3", "6.2.2.2.4", "6.2.2.2"), rels)
	for name, got := range map[string]float64{"Recall@5": m.Recall5, "Recall@10": m.Recall10, "Recall@20": m.Recall20} {
		if got > 1.0000001 {
			t.Errorf("%s = %v, must be <= 1", name, got)
		}
	}
}

// TestRecallAndNDCGAgreeOnEmptiness pins the invariant the LI/5GC set violated
// in production: over the same ranking, nDCG and recall must be zero together.
// One of them reporting a hit while the other reports none means they are
// scoring different notions of relevance.
func TestRecallAndNDCGAgreeOnEmptiness(t *testing.T) {
	rels := []Relevant{
		{SpecID: "33.128", Clause: "6.2.2.2.2", Grade: 2},
		{SpecID: "33.128", Clause: "6.2.2.2", Grade: 1},
	}
	for _, ranked := range [][]Ref{
		refs("6.2.2.2"),              // ancestor of one target, exact for the other
		refs("6.2.2.2.2.1"),          // descendant only
		refs("9.9", "6.2.2.2.4"),     // sibling under a judged parent
		refs("9.9", "8.8", "7.7"),    // nothing
		refs("6.2.2.2.2", "6.2.2.2"), // both, exactly
	} {
		m := Score(ranked, rels)
		if (m.NDCG10 == 0) != (m.Recall10 == 0) {
			t.Errorf("nDCG@10 = %v but Recall@10 = %v for %v — the two metrics disagree on what is relevant",
				m.NDCG10, m.Recall10, ranked)
		}
	}
}

func TestMean(t *testing.T) {
	got := Mean([]Metrics{{NDCG5: 1, MRR: 1}, {NDCG5: 0, MRR: 0}})
	if got.NDCG5 != 0.5 || got.MRR != 0.5 {
		t.Errorf("Mean = %+v, want 0.5/0.5", got)
	}
}
