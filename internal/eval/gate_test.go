package eval

import "testing"

func TestGate(t *testing.T) {
	base := Baseline{
		"lexical": {NDCG10: 0.80, Recall10: 0.70, MRR: 0.75, Success1: 0.60},
		"hybrid":  {NDCG10: 0.85, Recall10: 0.78, MRR: 0.80, Success1: 0.66},
	}

	t.Run("no regression within tolerance", func(t *testing.T) {
		// A small dip (0.01) under tol=0.02 must NOT fail; an improvement never does.
		cur := Baseline{
			"lexical": {NDCG10: 0.79, Recall10: 0.70, MRR: 0.75, Success1: 0.60},
			"hybrid":  {NDCG10: 0.90, Recall10: 0.78, MRR: 0.80, Success1: 0.66},
		}
		if regs := Gate(cur, base, 0.02); len(regs) != 0 {
			t.Fatalf("expected no regressions, got %+v", regs)
		}
	})

	t.Run("drop beyond tolerance fails", func(t *testing.T) {
		cur := Baseline{
			"lexical": {NDCG10: 0.80, Recall10: 0.50, MRR: 0.75, Success1: 0.60}, // recall@10 -0.20
			"hybrid":  base["hybrid"],
		}
		regs := Gate(cur, base, 0.02)
		if len(regs) != 1 {
			t.Fatalf("expected 1 regression, got %d: %+v", len(regs), regs)
		}
		if regs[0].System != "lexical" || regs[0].Metric != "recall@10" {
			t.Fatalf("unexpected regression target: %+v", regs[0])
		}
		if regs[0].Delta >= 0 {
			t.Fatalf("delta must be negative for a regression, got %v", regs[0].Delta)
		}
	})

	t.Run("missing system is a regression", func(t *testing.T) {
		cur := Baseline{"lexical": base["lexical"]} // hybrid dropped entirely
		regs := Gate(cur, base, 0.02)
		if len(regs) != 1 || regs[0].System != "hybrid" {
			t.Fatalf("expected a single 'hybrid not scored' regression, got %+v", regs)
		}
	})

	t.Run("deterministic order", func(t *testing.T) {
		// Two systems both regress: keys must come out sorted (hybrid before lexical).
		cur := Baseline{
			"lexical": {NDCG10: 0.50, Recall10: 0.70, MRR: 0.75, Success1: 0.60},
			"hybrid":  {NDCG10: 0.50, Recall10: 0.78, MRR: 0.80, Success1: 0.66},
		}
		regs := Gate(cur, base, 0.02)
		if len(regs) != 2 || regs[0].System != "hybrid" || regs[1].System != "lexical" {
			t.Fatalf("expected sorted [hybrid, lexical], got %+v", regs)
		}
	})
}
