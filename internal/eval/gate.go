package eval

import (
	"encoding/json"
	"os"
	"sort"
)

// Baseline maps a system key (lexical / hybrid / rerank) to its macro-averaged
// metrics. It is the committed reference the regression gate compares against —
// the GPU-free "suivi" of retrieval quality that runs on every PR against a
// lexical DuckDB snapshot (no Kaggle, no embeddings, no external service).
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

// LoadBaseline reads a committed baseline. A non-existent file is the first-run
// signal (callers seed from the current metrics): test it with os.IsNotExist.
func LoadBaseline(path string) (Baseline, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var bl Baseline
	if err := json.Unmarshal(b, &bl); err != nil {
		return nil, err
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
