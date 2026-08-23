package goal

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Runner owns the pipeline: the ordered steps, the state store, and the decision
// logic that turns "what changed" into "what must run".
type Runner struct {
	steps   []*Step
	byName  map[string]*Step
	store   *Store
	ctx     *Ctx
	order   []string
	toolID  func() string
	Verbose bool
}

// NewRunner validates the pipeline shape and returns a ready runner. It fails
// closed on a malformed pipeline (unknown dependency, cycle, duplicate name):
// those are programming errors that would otherwise surface as silently skipped
// work.
func NewRunner(steps []*Step, ctx *Ctx, store *Store, toolID func() string) (*Runner, error) {
	r := &Runner{steps: steps, byName: map[string]*Step{}, store: store, ctx: ctx, toolID: toolID}
	for _, s := range steps {
		if _, dup := r.byName[s.Name]; dup {
			return nil, fmt.Errorf("duplicate step %q", s.Name)
		}
		r.byName[s.Name] = s
	}
	for _, s := range steps {
		for _, d := range s.Deps {
			if _, ok := r.byName[d]; !ok {
				return nil, fmt.Errorf("step %q depends on unknown step %q", s.Name, d)
			}
		}
	}
	order, err := topoSort(steps)
	if err != nil {
		return nil, err
	}
	r.order = order
	return r, nil
}

func (r *Runner) toolchainIdentity() string {
	if r.toolID == nil {
		return "unknown"
	}
	return r.toolID()
}

// topoSort returns a deterministic dependency order (Kahn, ties broken by the
// declaration order so the plan reads the same way every time).
func topoSort(steps []*Step) ([]string, error) {
	indeg := map[string]int{}
	adj := map[string][]string{}
	pos := map[string]int{}
	for i, s := range steps {
		pos[s.Name] = i
		if _, ok := indeg[s.Name]; !ok {
			indeg[s.Name] = 0
		}
		for _, d := range s.Deps {
			adj[d] = append(adj[d], s.Name)
			indeg[s.Name]++
		}
	}
	var ready []string
	for n, d := range indeg {
		if d == 0 {
			ready = append(ready, n)
		}
	}
	sort.Slice(ready, func(i, j int) bool { return pos[ready[i]] < pos[ready[j]] })

	var out []string
	for len(ready) > 0 {
		n := ready[0]
		ready = ready[1:]
		out = append(out, n)
		next := adj[n]
		sort.Slice(next, func(i, j int) bool { return pos[next[i]] < pos[next[j]] })
		for _, m := range next {
			indeg[m]--
			if indeg[m] == 0 {
				ready = append(ready, m)
			}
		}
		sort.Slice(ready, func(i, j int) bool { return pos[ready[i]] < pos[ready[j]] })
	}
	if len(out) != len(steps) {
		return nil, fmt.Errorf("dependency cycle in the pipeline (sorted %d of %d steps)", len(out), len(steps))
	}
	return out, nil
}

// decide computes the verdict for one step. dirty holds the steps already known
// to be re-running in THIS plan, which is what propagates invalidation forward
// before their new fingerprints exist.
//
// The four skip conditions are evaluated in the order that gives the most useful
// reason: "never ran" beats "fingerprint changed" beats "output missing" beats
// "validation failed".
func (r *Runner) decide(s *Step, dirty map[string]bool) (Decision, error) {
	fp, rec, err := r.Fingerprint(s, r.ctx)
	if err != nil {
		return Decision{}, err
	}
	d := Decision{Step: s, Fingerprint: fp, Action: ActionRun}

	for _, dep := range s.Deps {
		if dirty[dep] {
			d.Reason = "dependency " + dep + " is re-running"
			return d, nil
		}
	}

	prev, err := r.store.Load(s.Name)
	if err != nil {
		d.Reason = err.Error()
		return d, nil
	}
	d.Previous = prev

	switch {
	case prev == nil:
		d.Reason = "never run"
		return d, nil
	case prev.Status == StatusRunning:
		// A record stuck in `running` means the process died mid-step. It is the
		// one state that must never be trusted, whatever its fingerprint says.
		d.Reason = "previous attempt was interrupted mid-step"
		return d, nil
	case prev.Status != StatusSuccess:
		d.Reason = "previous attempt " + string(prev.Status)
		if prev.Error != "" {
			d.Reason += ": " + firstLine(prev.Error)
		}
		return d, nil
	case prev.Fingerprint != fp:
		d.Reason = explainDrift(prev, rec)
		return d, nil
	}

	// A matching fingerprint is NOT sufficient. The outputs must still be there,
	// and they must still be sane: a truncated DuckDB or a half-written JSONL
	// keeps its fingerprint (which describes the INPUTS) while being unusable.
	if s.Outputs != nil {
		for _, o := range s.Outputs(r.ctx) {
			st, err := os.Stat(o)
			if err != nil {
				d.Reason = "output missing: " + filepath.ToSlash(rel(r.ctx.Root, o))
				return d, nil
			}
			if !st.IsDir() && st.Size() == 0 {
				d.Reason = "output is empty: " + filepath.ToSlash(rel(r.ctx.Root, o))
				return d, nil
			}
		}
	}
	if s.Validate != nil {
		if err := s.Validate(r.ctx); err != nil {
			d.Reason = "validation failed: " + firstLine(err.Error())
			return d, nil
		}
	}

	d.Action = ActionSkip
	d.Reason = "fingerprint unchanged, outputs present and valid"
	return d, nil
}

// explainDrift names WHAT changed rather than just reporting that something did.
// "17 new content hashes" or "rust/parse/src/lib.rs changed" is actionable;
// "fingerprint differs" is not.
func explainDrift(prev, cur *Record) string {
	if prev.StepVersion != cur.StepVersion {
		return fmt.Sprintf("step version %d -> %d", prev.StepVersion, cur.StepVersion)
	}
	if changed := diffKeys(prev.Impl, cur.Impl); len(changed) > 0 {
		return "implementation changed: " + summarise(changed)
	}
	if changed := diffKeys(prev.Deps, cur.Deps); len(changed) > 0 {
		return "dependency output changed: " + summarise(changed)
	}
	if changed := diffKeys(prev.Environment, cur.Environment); len(changed) > 0 {
		return "configuration changed: " + summarise(changed)
	}
	if changed := diffKeys(prev.Inputs, cur.Inputs); len(changed) > 0 {
		return fmt.Sprintf("%d input(s) changed: %s", len(changed), summarise(changed))
	}
	return "fingerprint changed"
}

func diffKeys(a, b map[string]string) []string {
	var out []string
	for k, v := range b {
		if a[k] != v {
			out = append(out, k)
		}
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			out = append(out, k+" (removed)")
		}
	}
	sort.Strings(out)
	return out
}

func summarise(keys []string) string {
	const max = 3
	if len(keys) <= max {
		return strings.Join(keys, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(keys[:max], ", "), len(keys)-max)
}

func rel(root, p string) string {
	if r, err := filepath.Rel(root, p); err == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	return p
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// Plan computes the full differential plan WITHOUT running anything. This is
// what `goal plan` prints and what a human reads before a long run.
func (r *Runner) Plan(only map[string]bool) ([]Decision, error) {
	dirty := map[string]bool{}
	var out []Decision
	for _, name := range r.order {
		s := r.byName[name]
		if only != nil && !only[name] {
			continue
		}
		d, err := r.decide(s, dirty)
		if err != nil {
			return nil, err
		}
		if d.Action == ActionRun {
			dirty[name] = true
		}
		out = append(out, d)
	}
	return out, nil
}

// Result is the outcome of an Execute pass.
type Result struct {
	Decisions   []Decision
	Ran         []string
	Skipped     []string
	Failed      []string
	HeavyRan    int
	TotalTimeMs int64
}

// Execute walks the pipeline in dependency order. Each step is re-decided
// IMMEDIATELY before it would run, so a dependency that just produced a new
// fingerprint is taken into account rather than a stale plan being replayed.
func (r *Runner) Execute(only map[string]bool, dryRun bool) (*Result, error) {
	res := &Result{}
	start := time.Now()
	dirty := map[string]bool{}

	for _, name := range r.order {
		s := r.byName[name]
		if only != nil && !only[name] {
			continue
		}
		d, err := r.decide(s, dirty)
		if err != nil {
			return res, err
		}
		res.Decisions = append(res.Decisions, d)

		if d.Action == ActionSkip {
			res.Skipped = append(res.Skipped, name)
			reportSkip(s, d)
			continue
		}
		dirty[name] = true

		if dryRun {
			reportPlanned(s, d)
			res.Ran = append(res.Ran, name)
			continue
		}

		if err := r.runStep(s, d, res); err != nil {
			res.Failed = append(res.Failed, name)
			if s.Optional {
				// Optional is an explicit, declared property of the step — never
				// an inline "|| true". The failure is still recorded and shown.
				fmt.Fprintf(os.Stderr, "goal: optional step %q failed, continuing: %v\n", name, err)
				continue
			}
			res.TotalTimeMs = time.Since(start).Milliseconds()
			return res, fmt.Errorf("step %q failed: %w", name, err)
		}
		res.Ran = append(res.Ran, name)
		if s.Heavy {
			res.HeavyRan++
		}
	}
	res.TotalTimeMs = time.Since(start).Milliseconds()
	return res, nil
}

// runStep executes one step under the full contract: mark running, do the work,
// verify the outputs, and only then record success. The ordering is the point —
// a success written before validation would make the next run skip broken work.
func (r *Runner) runStep(s *Step, d Decision, _ *Result) error {
	log, err := NewStepLog(r.ctx.Local, s.Name)
	if err != nil {
		return err
	}
	defer log.Close()

	_, rec, err := r.Fingerprint(s, r.ctx)
	if err != nil {
		return err
	}
	rec.Status = StatusRunning
	rec.StartedAt = time.Now().UTC()
	rec.LogFile = rel(r.ctx.Root, log.Path())
	if err := r.store.Save(rec); err != nil {
		return err
	}

	stepCtx := *r.ctx
	stepCtx.Log = log
	stepCtx.record = rec

	fmt.Fprintf(os.Stderr, "\n\033[1mSTEP %s\033[0m — %s\n", s.Name, s.Doc)
	fmt.Fprintf(os.Stderr, "  reason       %s\n", d.Reason)
	fmt.Fprintf(os.Stderr, "  fingerprint  %s\n", d.Fingerprint)
	fmt.Fprintf(os.Stderr, "  log          %s\n", rec.LogFile)

	begin := time.Now()
	runErr := s.Run(&stepCtx)
	rec.DurationSec = time.Since(begin).Seconds()
	rec.FinishedAt = time.Now().UTC()

	if runErr != nil {
		rec.Status = StatusFailed
		rec.Error = runErr.Error()
		_ = r.store.Save(rec)
		fmt.Fprintf(os.Stderr, "  status       \033[31mFAILED\033[0m after %.1fs — %s\n", rec.DurationSec, firstLine(runErr.Error()))
		if len(rec.Checkpoint) > 0 {
			fmt.Fprintf(os.Stderr, "  checkpoint   %v (state saved; the next run resumes here)\n", rec.Checkpoint)
		}
		return runErr
	}

	// Outputs and validation BEFORE success. A step that returned nil but left no
	// usable artefact is a failure, and calling it a success is exactly the
	// "orchestrator green while the worker failed" trap this replaces.
	outs := map[string]string{}
	if s.Outputs != nil {
		for _, o := range s.Outputs(&stepCtx) {
			st, err := os.Stat(o)
			if err != nil {
				rec.Status = StatusFailed
				rec.Error = "declared output missing after a successful run: " + o
				_ = r.store.Save(rec)
				return fmt.Errorf("%s", rec.Error)
			}
			outs[filepath.ToSlash(rel(r.ctx.Root, o))] = fmt.Sprintf("%d bytes", st.Size())
		}
	}
	rec.Outputs = outs

	if s.Validate != nil {
		if err := s.Validate(&stepCtx); err != nil {
			rec.Status = StatusFailed
			rec.Error = "output validation failed: " + err.Error()
			_ = r.store.Save(rec)
			return fmt.Errorf("%s", rec.Error)
		}
		rec.Validation = map[string]string{"result": "pass"}
	}

	rec.Status = StatusSuccess
	rec.Error = ""
	if err := r.store.Save(rec); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "  status       \033[32mSUCCESS\033[0m in %s\n", humanDuration(rec.DurationSec))
	for k, v := range outs {
		fmt.Fprintf(os.Stderr, "  output       %s (%s)\n", k, v)
	}
	return nil
}

func reportSkip(s *Step, d Decision) {
	fmt.Fprintf(os.Stderr, "\n\033[1mSTEP %s\033[0m — %s\n", s.Name, s.Doc)
	fmt.Fprintf(os.Stderr, "  status       \033[2mSKIP\033[0m\n")
	fmt.Fprintf(os.Stderr, "  reason       %s\n", d.Reason)
}

func reportPlanned(s *Step, d Decision) {
	fmt.Fprintf(os.Stderr, "\n\033[1mSTEP %s\033[0m — %s\n", s.Name, s.Doc)
	fmt.Fprintf(os.Stderr, "  status       WOULD RUN (dry run)\n")
	fmt.Fprintf(os.Stderr, "  reason       %s\n", d.Reason)
}

func humanDuration(sec float64) string {
	switch {
	case sec < 60:
		return fmt.Sprintf("%.1fs", sec)
	case sec < 3600:
		return fmt.Sprintf("%dm%02ds", int(sec)/60, int(sec)%60)
	default:
		return fmt.Sprintf("%dh%02dm", int(sec)/3600, (int(sec)%3600)/60)
	}
}

// Steps exposes the pipeline for reporting.
func (r *Runner) Steps() []*Step { return r.steps }

// Order exposes the resolved dependency order.
func (r *Runner) Order() []string { return r.order }
