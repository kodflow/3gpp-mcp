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
	// Nothing relevant retrieved.
	none := Score(refs("9.9", "8.8"), rels)
	if none.MRR != 0 || none.NDCG5 != 0 {
		t.Errorf("irrelevant ranking should score 0: %+v", none)
	}
}

func TestMean(t *testing.T) {
	got := Mean([]Metrics{{NDCG5: 1, MRR: 1}, {NDCG5: 0, MRR: 0}})
	if got.NDCG5 != 0.5 || got.MRR != 0.5 {
		t.Errorf("Mean = %+v, want 0.5/0.5", got)
	}
}
