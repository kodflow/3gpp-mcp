package eval

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
)

// Baseline maps a system key (lexical / hybrid / rerank) to its macro-averaged
// metrics. It is the committed reference the regression gate compares against —
// the GPU-free "suivi" of retrieval quality. It ran on every PR until CI was
// deleted on 2026-08-31 (208f23b); it now runs in the local pipeline, inside the
// `smoke` step, against the built corpus and before every publish (no
// embeddings, no external service).
type Baseline map[string]Metrics

// TrackedMetric names one macro metric the gate enforces, with an accessor onto
// Metrics. The gate fails ONLY on these: a curated subset keeps strongly
// correlated or noisy metrics from flapping the build, while the JSON sidecar
// still records the full Metrics for offline inspection.
type TrackedMetric struct {
	Name string
	Get  func(Metrics) float64
}

// Tracked is the ordered set of metrics the regression gate guards. ndcg@10 and
// mrr@10 capture ranking quality, recall@10 capture coverage, success@1 the
// "first hit is the exact clause" case that matters most for cited retrieval.
var Tracked = []TrackedMetric{
	{"ndcg@10", func(m Metrics) float64 { return m.NDCG10 }},
	{"recall@10", func(m Metrics) float64 { return m.Recall10 }},
	{"mrr@10", func(m Metrics) float64 { return m.MRR }},
	{"success@1", func(m Metrics) float64 { return m.Success1 }},
}

// LoadBaseline reads a committed baseline. A non-existent file is returned as the
// error it is — NOT a first-run signal to seed from, which is how the gate this
// feeds could never fail. Judge turns it into ErrNoBaseline.
func LoadBaseline(path string) (Baseline, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var bl Baseline
	if err := json.Unmarshal(b, &bl); err != nil {
		return nil, err
	}
	// AN ABSENT METRIC IS NOT A ZERO BAR, THOUGH JSON DECODES IT AS ONE.
	//
	// Metrics is a struct of float64s, so `{"lexical":{}}` unmarshals into an entry
	// whose every tracked metric is 0 — and a baseline of zeros is a bar no ranking
	// can fall below. Judge already refused a system with no entry at all; an entry
	// with no numbers in it was the same hole one level down, found by review of
	// #324. So every tracked metric must be PRESENT and a number. An explicit 0 is
	// kept, and must be: success@1 is legitimately 0 on the committed baseline.
	// `null` is refused for the same reason an absent key is — it decodes to 0.
	var raw map[string]map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("baseline %s: each system must map to an object of metrics: %w", path, err)
	}
	var incomplete []string
	for sys, fields := range raw {
		var missing []string
		for _, tm := range Tracked {
			v, ok := fields[tm.Name]
			if !ok || strings.TrimSpace(string(v)) == "null" {
				missing = append(missing, tm.Name)
			}
		}
		if len(missing) > 0 {
			incomplete = append(incomplete, fmt.Sprintf("%s (no %s)", sys, strings.Join(missing, ", ")))
		}
	}
	if len(incomplete) > 0 {
		sort.Strings(incomplete)
		return nil, fmt.Errorf("baseline %s is incomplete: %s — a missing metric decodes as 0, a bar "+
			"nothing can fall below, so the gate would pass on any ranking",
			path, strings.Join(incomplete, "; "))
	}
	return bl, nil
}

// WriteBaseline persists metrics as a baseline (also used for the -json sidecar:
// today's metrics ARE tomorrow's candidate baseline). Map keys are emitted in
// sorted order by encoding/json, so the file is deterministic and diff-friendly.
func WriteBaseline(path string, b Baseline) error {
	out, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}

// Regression is one tracked metric that dropped below (baseline - tol) on a
// system. Delta is current-baseline (always negative for a real regression).
type Regression struct {
	System   string
	Metric   string
	Baseline float64
	Current  float64
	Delta    float64
}

// Gate compares current metrics to the baseline and returns every tracked metric
// that regressed by more than tol (absolute). tol absorbs run-to-run float noise
// so only a genuine quality drop fails the build. A system present in baseline
// but missing from current is itself a regression: the gate must never pass
// silently because a system stopped being scored (e.g. someone dropped it from
// -systems). Improvements never appear here — the gate is one-sided.
func Gate(current, baseline Baseline, tol float64) []Regression {
	keys := make([]string, 0, len(baseline))
	for k := range baseline {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var regs []Regression
	for _, sys := range keys {
		cur, ok := current[sys]
		if !ok {
			regs = append(regs, Regression{
				System: sys, Metric: "(system not scored)",
				Baseline: 1, Current: 0, Delta: -1,
			})
			continue
		}
		base := baseline[sys]
		for _, t := range Tracked {
			b, c := t.Get(base), t.Get(cur)
			if c < b-tol {
				regs = append(regs, Regression{
					System: sys, Metric: t.Name,
					Baseline: b, Current: c, Delta: c - b,
				})
			}
		}
	}
	return regs
}

// ErrNoBaseline is the gate refusing to compare against nothing. Test for it
// with errors.Is: a caller that wants to CREATE a baseline does so on purpose,
// with -json, never as a side effect of asking the gate for a verdict.
var ErrNoBaseline = errors.New("no committed retrieval baseline")

// Judge is the WHOLE verdict of the regression gate, in one function, so the
// bench CLI and the local pipeline cannot come to ask two different questions.
// It returns the tracked-metric regressions of a real comparison, or an error
// when no comparison could be made — and an error is a failed gate, never a
// pass.
//
// THE GATE THIS REPLACES COULD NOT FAIL. cmd/bench treated a missing baseline
// as a first run: it SEEDED the file from the current metrics and exited 0. So
// the first run of the gate on any tree passed by construction, and so did every
// run after the file was deleted — the regression being gated was written into
// the baseline it was then compared with. Commit 3bfab2a said it in as many
// words: until baseline.json was committed, "CI's retrieval gate passed
// unconditionally -- it was advisory dressed as a gate".
//
// Two more ways the comparison can be empty, and each is refused for the same
// reason:
//
//   - a scored system the baseline has no entry for. Gate walks the BASELINE's
//     keys, so against `{}`, or against a baseline written before that system
//     was scored, the system is compared with nothing and cannot regress. A
//     ranking that scores zero would ship under a line that says "gate OK".
//   - nothing scored at all. A mistyped -systems value selects no system, and
//     held against an empty baseline that is nothing compared with nothing.
//
// A baseline system that was NOT scored remains Gate's to report, as a
// regression: it is a comparison that stopped happening, not one that never
// could.
func Judge(path string, current Baseline, tol float64) ([]Regression, error) {
	base, err := LoadBaseline(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s does not exist — the gate will not seed one from the run it is "+
			"judging; write a candidate with -json and commit it deliberately", ErrNoBaseline, path)
	}
	if err != nil {
		return nil, fmt.Errorf("load baseline %s: %w", path, err)
	}
	if len(current) == 0 {
		return nil, fmt.Errorf("no system was scored, so there is nothing to hold against %s", path)
	}
	var uncovered []string
	for sys := range current {
		if _, ok := base[sys]; !ok {
			uncovered = append(uncovered, sys)
		}
	}
	if len(uncovered) > 0 {
		sort.Strings(uncovered)
		return nil, fmt.Errorf("%s has no entry for %s — a system with no baseline is compared against "+
			"nothing and can never regress; commit its metrics or stop scoring it",
			path, strings.Join(uncovered, ", "))
	}
	return Gate(current, base, tol), nil
}
