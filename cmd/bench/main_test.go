package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/eval"
)

// lexicalToday is docs/inputs/eval/baseline.json, re-measured on 2026-09-10 on
// the 23.1 GB corpus: nDCG@10 0.072, Recall@10 0.167, MRR@10 0.042, Success@1 0.
var lexicalToday = eval.Metrics{
	NDCG5: 0.07177942634556551, NDCG10: 0.07177942634556551,
	Recall5: 0.16666666666666666, Recall10: 0.16666666666666666, Recall20: 0.25,
	MRR: 0.041666666666666664, Success1: 0,
}

// A MISSING BASELINE FAILS THE BENCH, AND THE BENCH DOES NOT WRITE ONE.
//
// This is the exact behaviour gateAgainstBaseline had until 2026-09-10: it wrote
// the current metrics to the missing path and returned true, printing "SEEDED
// from this run (advisory, gate passes)". Any caller that trusted exit 0 — the
// deleted CI job, and now the smoke step — was told a comparison had passed when
// none had happened, and was left with a baseline equal to whatever it had just
// measured.
func TestTheBenchNeverSeedsAMissingBaseline(t *testing.T) {
	p := filepath.Join(t.TempDir(), "baseline.json")
	if gateAgainstBaseline(p, eval.Baseline{"lexical": lexicalToday}, 0.02) {
		t.Error("the gate passed with no baseline on disk: it compared against nothing")
	}
	if _, err := os.Stat(p); err == nil {
		t.Errorf("the gate wrote %s from the run it was judging — a missing baseline was seeded", p)
	}
}

// A RANKING THAT FINDS NOTHING FAILS THE BENCH, and one within tolerance of the
// committed numbers does not. The exit code is the only thing the smoke step
// reads, so both directions are pinned at the function that decides it.
func TestTheBenchFailsADropBeyondTolerance(t *testing.T) {
	p := filepath.Join(t.TempDir(), "baseline.json")
	if err := eval.WriteBaseline(p, eval.Baseline{"lexical": lexicalToday}); err != nil {
		t.Fatal(err)
	}
	if gateAgainstBaseline(p, eval.Baseline{"lexical": {}}, 0.02) {
		t.Error("nDCG@10, Recall@10 and MRR@10 fell to zero and the gate passed")
	}
	if !gateAgainstBaseline(p, eval.Baseline{"lexical": lexicalToday}, 0.02) {
		t.Error("the committed numbers, re-measured unchanged, failed the gate: a gate that fails " +
			"a correct corpus is one the operator learns to skip")
	}
}
