package eval

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

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

// committedLexical is docs/inputs/eval/baseline.json as measured on 2026-08-27 and
// re-measured on 2026-09-10 against the 23.1 GB corpus: the one system the local
// gate scores, at the level it actually stands.
var committedLexical = Metrics{
	NDCG5: 0.07177942634556551, NDCG10: 0.07177942634556551,
	Recall5: 0.16666666666666666, Recall10: 0.16666666666666666, Recall20: 0.25,
	MRR: 0.041666666666666664, Success1: 0,
}

func writeBaselineFile(t *testing.T, b Baseline) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "baseline.json")
	if err := WriteBaseline(p, b); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestJudgeFailsOnAMissingBaselineAndNeverWritesOne pins the defect Judge exists
// to end. cmd/bench SEEDED a missing baseline from the run it was judging and
// exited 0, so the gate passed by construction whenever the file was absent —
// including the day someone deleted it.
func TestJudgeFailsOnAMissingBaselineAndNeverWritesOne(t *testing.T) {
	p := filepath.Join(t.TempDir(), "baseline.json")
	regs, err := Judge(p, Baseline{"lexical": committedLexical}, 0.02)
	if !errors.Is(err, ErrNoBaseline) {
		t.Fatalf("Judge with no baseline on disk returned regs=%+v err=%v; it must refuse with "+
			"ErrNoBaseline — a gate with nothing to compare against has not passed", regs, err)
	}
	if _, statErr := os.Stat(p); statErr == nil {
		t.Fatalf("Judge wrote %s: the run being judged became the standard it is judged by", p)
	}
}

// TestJudgeFailsOnADropBeyondTolerance is the gate doing its job on the numbers
// the corpus actually has. Wrecking the ranking takes nDCG@10, Recall@10 and
// MRR@10 to zero; each of the three is more than tol above zero today, so each
// must be reported. Success@1 is already 0.000 and cannot regress — that is a
// statement about current quality, recorded here so nobody reads its silence as
// coverage.
func TestJudgeFailsOnADropBeyondTolerance(t *testing.T) {
	p := writeBaselineFile(t, Baseline{"lexical": committedLexical})

	regs, err := Judge(p, Baseline{"lexical": {}}, 0.02)
	if err != nil {
		t.Fatalf("a real comparison was possible and Judge refused it: %v", err)
	}
	got := map[string]bool{}
	for _, r := range regs {
		got[r.Metric] = true
	}
	for _, want := range []string{"ndcg@10", "recall@10", "mrr@10"} {
		if !got[want] {
			t.Errorf("a ranking that finds nothing did not regress %s (regressions: %+v)", want, regs)
		}
	}

	// The control: a dip smaller than tol is noise, and a gate that fails on
	// noise is one the operator learns to skip.
	dip := committedLexical
	dip.NDCG10 -= 0.01
	dip.Recall10 -= 0.01
	dip.MRR -= 0.01
	if regs, err := Judge(p, Baseline{"lexical": dip}, 0.02); err != nil || len(regs) != 0 {
		t.Errorf("a 0.01 dip under tol=0.02 was judged a regression: regs=%+v err=%v", regs, err)
	}
}

// TestJudgeRefusesAComparisonAgainstNothing covers the other ways a comparison
// can be EMPTY while the gate prints "gate OK". Gate walks the BASELINE's keys, so
// a scored system the baseline does not name is compared with nothing: here it
// scores zero on every metric and would ship. Each case is one that returned no
// regression and no error before Judge refused it.
func TestJudgeRefusesAComparisonAgainstNothing(t *testing.T) {
	for _, tc := range []struct {
		name     string
		baseline Baseline
		current  Baseline
	}{
		{"an empty baseline", Baseline{}, Baseline{"lexical": {}}},
		{"a scored system the baseline does not name",
			Baseline{"lexical": committedLexical},
			Baseline{"lexical": committedLexical, "hybrid": {}}},
		{"nothing scored", Baseline{}, Baseline{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := writeBaselineFile(t, tc.baseline)
			regs, err := Judge(p, tc.current, 0.02)
			if err == nil && len(regs) == 0 {
				t.Fatalf("Judge held %v against %v and found nothing wrong: a system the baseline "+
					"does not name was compared with nothing, and the gate would print \"gate OK\"",
					tc.current, tc.baseline)
			}
			if err == nil {
				t.Fatalf("Judge reported %+v as regressions; an empty comparison must be REFUSED, "+
					"not dressed as a metric drop", regs)
			}
		})
	}
}
