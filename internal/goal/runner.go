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
	// ForceOnly downgrades precondition FAILURES to loud warnings. It exists so
	// that deliberately operating on unverified state stays possible AND stays
	// visibly different from ordinary operation — the previous arrangement, where
	// `--only` quietly implied both, is what let two sessions embed against a
	// corpus no producer had made.
	ForceOnly bool
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
		for _, d := range s.AnyDeps {
			if _, ok := r.byName[d]; !ok {
				return nil, fmt.Errorf("step %q has unknown alternative dependency %q", s.Name, d)
			}
		}
		if len(s.AnyDeps) == 1 {
			// One alternative is not an alternative; it is a Dep wearing a
			// disguise, and it would silently lose the satisfaction check.
			return nil, fmt.Errorf("step %q declares a single AnyDeps %q — use Deps", s.Name, s.AnyDeps[0])
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

// isTool reports whether a step name denotes a binary producer rather than a
// data producer. An unknown name is not a tool: NewRunner has already rejected
// unknown dependencies, so this can only be a caller passing a typo, and
// treating that as "not a tool" keeps the conservative behaviour.
func (r *Runner) isTool(name string) bool {
	s := r.byName[name]
	return s != nil && s.Tool
}

// WithToolDeps closes a step selection over the tool steps it needs, transitively.
//
// This is what makes `--only` honest. Selecting a step used to run that step and
// nothing else, so a corrected sparse_embed.rs sat on disk while `--only sparse`
// launched YESTERDAY's binary and reported success — the "a fix that was not
// built is inert" failure, which this repository's own runbook records happening
// five separate times, to five different binaries, before the cause was found.
// The cause was never carelessness; it was that `--only` skipped the build.
//
// Data dependencies stay out. Asking for one step must not silently re-ingest a
// corpus — that is the whole point of asking for one step. Tool dependencies are
// different in kind: they are cheap, idempotent, and skip in milliseconds when
// nothing changed, so including them costs nothing and removes the trap.
func (r *Runner) WithToolDeps(sel map[string]bool) map[string]bool {
	if sel == nil {
		return nil
	}
	out := make(map[string]bool, len(sel))
	for n := range sel {
		out[n] = true
	}
	var add func(name string)
	add = func(name string) {
		s := r.byName[name]
		if s == nil {
			return
		}
		for _, d := range append(append([]string(nil), s.Deps...), s.AnyDeps...) {
			if !r.isTool(d) || out[d] {
				continue
			}
			out[d] = true
			add(d) // a tool's own tool deps: build-go needs toolchain
		}
	}
	for n := range sel {
		add(n)
	}
	return out
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
		// Alternatives order exactly like Deps: any of them may write the shared
		// artefact, so all must be decided before this step is.
		for _, d := range append(append([]string(nil), s.Deps...), s.AnyDeps...) {
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
//
// A dirty TOOL dependency is deliberately not one of them. Tool steps produce
// binaries, not provenance, so "cargo relinked" is not a reason to replay a GPU
// pass — and since a Tool contributes nothing to the fingerprint either, letting
// it short-circuit here would have reinstated the whole cascade one layer down,
// where it would have been much harder to see.
func (r *Runner) decide(s *Step, dirty map[string]bool) (Decision, error) {
	fp, rec, err := r.Fingerprint(s, r.ctx)
	if err != nil {
		return Decision{}, err
	}
	d := Decision{Step: s, Fingerprint: fp, Action: ActionRun}

	for _, dep := range s.Deps {
		if dirty[dep] && !r.isTool(dep) {
			d.Reason = "re-checked after " + dep + "; runs only if it changed something"
			d.Conditional = true
			return d, nil
		}
	}
	for _, dep := range s.AnyDeps {
		if dirty[dep] && !r.isTool(dep) {
			d.Reason = "re-checked after producer " + dep + "; runs only if it changed something"
			d.Conditional = true
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
	case prev.Fingerprint != fp && !onlyDroppedDeterminants(prev, rec):
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

// onlyDroppedDeterminants reports whether the fingerprint moved for one reason
// alone: something that USED to be a determinant no longer is.
//
// The fingerprint is a hash of the record's components, so comparing components
// is the same test with more resolution — and the extra resolution matters
// exactly once, when the set of determinants itself changes. Making build steps
// ordering-only dropped `dep:build-go` from every data step's fingerprint, and
// the plan then offered to replay a finished corpus with the reason "dependency
// output changed: build-go (removed)". That is not a reason. A determinant nobody
// counts any more cannot have changed anything; re-running fifteen heavy steps to
// discover that would have cost days and risked a 22 GB artefact.
//
// Everything that still IS a determinant is compared exactly, so a real change —
// a new version, an edited source, a moved input, a dependency that genuinely
// produced something different — invalidates as before. Only keys absent from the
// current record are forgiven, and only when every remaining one agrees.
func onlyDroppedDeterminants(prev, cur *Record) bool {
	if prev.StepVersion != cur.StepVersion ||
		!sameMap(prev.Impl, cur.Impl) ||
		!sameMap(prev.Environment, cur.Environment) ||
		!sameMap(prev.Inputs, cur.Inputs) {
		return false
	}
	dropped := false
	for k, v := range cur.Deps {
		if prev.Deps[k] != v {
			return false // a dependency that still counts, and it moved
		}
	}
	for k := range prev.Deps {
		if _, ok := cur.Deps[k]; !ok {
			dropped = true
		}
	}
	return dropped
}

func sameMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
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

// contentIdentityMax is the size below which an output is identified by its
// CONTENT rather than by size and mtime.
//
// The distinction pays for itself on the small, regenerated artefacts: a work
// list, a series list, a state JSON. `discover-etsi` rewrites its work list on
// every run, so mtime always moves, so `corpus-etsi` — hours of download and PDF
// conversion — was replayed by a file that came back byte-for-byte identical.
// Hashing 64 MiB costs a fraction of a second; the corpus and the ledgers are
// orders of magnitude above it and keep the cheap identity.
const contentIdentityMax = 64 << 20

// outputIdentity describes an output well enough to answer one question: did this
// run change it? Content where content is affordable, size AND mtime where it is
// not — size alone would call a same-size rewrite "unchanged" and let a stale
// index be served over changed data, the one direction this must never be wrong
// in. A file that cannot be read falls back to the cheap identity rather than
// claiming an identity it does not have.
func outputIdentity(path string, st os.FileInfo) string {
	if st.Size() <= contentIdentityMax {
		if h, err := fileContentHash(path); err == nil {
			return fmt.Sprintf("%d bytes sha=%s", st.Size(), h)
		}
	}
	return fmt.Sprintf("%d bytes @%d", st.Size(), st.ModTime().UTC().UnixNano())
}

// publishedProvenance is what a dependant folds in for this record. The empty
// Provenance of every pre-existing state file means "the fingerprint", so the
// field could be introduced without invalidating a single recorded step.
func publishedProvenance(rec *Record) string {
	if rec.Provenance != "" {
		return rec.Provenance
	}
	return rec.Fingerprint
}

// carriedProvenance keeps a step's published identity across a run that changed
// nothing. With no previous record there is nothing to carry: a first run is a
// change by definition.
func carriedProvenance(prev *Record) string {
	if prev == nil || prev.Status != StatusSuccess {
		return ""
	}
	return publishedProvenance(prev)
}

// sameOutputs reports whether two recorded output sets describe the same
// artefacts.
//
// It has to cope with records written before outputs carried a content hash,
// which said only "%d bytes". Discarding those as incomparable is not free: it
// would have made the first run under the new scheme replay `fetch` — 20 163
// specs and roughly thirty hours of LibreOffice — to reproduce a work list this
// session had already diffed byte for byte. So a legacy value is compared on the
// one thing it recorded, its size. That is weaker than the new comparison and
// strictly stronger than the old behaviour, which compared nothing at all, and
// the window closes the first time each step runs.
//
// A qualifier that merely CHANGED (sha to mtime, because the output crossed
// contentIdentityMax) is not the legacy case and is treated as a difference.
func sameOutputs(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		w, ok := b[k]
		if !ok {
			return false
		}
		if v == w {
			continue
		}
		if isLegacyIdentity(v) || isLegacyIdentity(w) {
			if sizeOf(v) != "" && sizeOf(v) == sizeOf(w) {
				continue
			}
		}
		return false
	}
	return true
}

// isLegacyIdentity reports whether a recorded identity predates the qualifier —
// "12345 bytes" and nothing more.
func isLegacyIdentity(v string) bool {
	return !strings.Contains(v, " sha=") && !strings.Contains(v, " @")
}

// sizeOf returns the leading "<n> bytes" of an identity, or "" if it has none.
func sizeOf(v string) string {
	i := strings.Index(v, " bytes")
	if i < 0 {
		return ""
	}
	return v[:i]
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

	// dirty is a PREDICTION — "this dependency is going to re-run, so assume the
	// worst" — and it is needed only while nothing actually runs. During a real
	// execution every upstream step has already finished and saved its record by
	// the time this step is decided, so the recomputed fingerprint is not a guess:
	// it is the answer. Consulting the prediction as well would override it, and
	// would override it in the one direction that costs hours — a dependency that
	// ran and changed NOTHING (it declined, or it rewrote its output identically)
	// would still replay everything behind it.
	//
	// A nil map reads as all-false, so passing nil is what turns the prediction
	// off; Plan and the dry run keep it.
	var dirty map[string]bool
	if dryRun {
		dirty = map[string]bool{}
	}

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
		if dirty != nil {
			dirty[name] = true
		}

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
	// Selecting a step is not the same as vouching for it. `--only` restricts what
	// RUNS; it must not also decide that the things the step depends on are fine.
	// Before AnyDeps existed, `--only embed` reached a step whose producer had never
	// run and left `"merge": "missing"` in the state file as the only trace.
	if err := r.checkPreconditions(s); err != nil {
		if !r.ForceOnly {
			return err
		}
		fmt.Fprintf(os.Stderr,
			"\n\033[1;31mgoal: --force-only OVERRIDES A FAILED PRECONDITION on %q\033[0m\n  %v\n"+
				"  The step is running against state nothing verified. Whatever it produces\n"+
				"  is not reproducible from this repository's own definition of done.\n\n",
			s.Name, err)
	}

	log, err := NewStepLog(r.ctx.Local, s.Name)
	if err != nil {
		return err
	}
	defer log.Close()

	// The record as it stood BEFORE this attempt: the only thing that can say
	// whether this run actually changed anything, and so whether dependants have
	// any reason to replay. Loaded here rather than taken from the Decision,
	// because decide returns early — without it — on a dirty dependency.
	prev, _ := r.store.Load(s.Name)

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

	// A decline is not a failure. The step ran, found the conditions for its work
	// absent, and produced none of its outputs on purpose — so the gates below,
	// which describe a run that DID the work, must not be applied to it.
	if Declined(runErr) {
		rec.Status = StatusSuccess
		rec.Declined = true
		rec.Error = ""
		// Publishing a fresh provenance here would be the same gate, worn as a
		// number: `embed` declines the moment every clause already carries a
		// vector, and that decline used to re-freeze the HNSW index and re-compact
		// 22 GB to reproduce bytes nobody had touched.
		rec.Provenance = carriedProvenance(prev)
		if err := r.store.Save(rec); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "  status       \033[33mDECLINED\033[0m after %.1fs — %s\n",
			rec.DurationSec, firstLine(runErr.Error()))
		return nil
	}

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
	comparable := s.Outputs != nil
	if s.Outputs != nil {
		declared := s.Outputs(&stepCtx)
		comparable = len(declared) > 0
		for _, o := range declared {
			st, err := os.Stat(o)
			if err != nil {
				rec.Status = StatusFailed
				rec.Error = "declared output missing after a successful run: " + o
				_ = r.store.Save(rec)
				return fmt.Errorf("%s", rec.Error)
			}
			// A directory's own size says nothing about its contents, so "unchanged"
			// could not be established for one. No step declares a directory output
			// today; if one ever does, it falls back to always propagating rather
			// than silently vouching for bytes nobody compared.
			if st.IsDir() {
				comparable = false
			}
			outs[filepath.ToSlash(rel(r.ctx.Root, o))] = outputIdentity(o, st)
		}
	}
	rec.Outputs = outs

	// Did this run change anything a dependant could observe? Only a step that has
	// declared its outputs to BE its effect may answer that from them; for every
	// other step, having run at all is the honest answer.
	rec.Provenance = rec.Fingerprint
	if s.OutputsComplete && comparable && prev != nil && prev.Status == StatusSuccess &&
		!prev.Declined && len(prev.Outputs) > 0 && sameOutputs(prev.Outputs, outs) {
		rec.Provenance = carriedProvenance(prev)
	}

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

// checkPreconditions verifies, at EXECUTION time, that everything this step is
// declared to stand on actually exists. Planning time is too early: `--only` and
// `--from` bypass the plan, and those are exactly the paths that need checking.
//
// An Optional dependency is exempt. `build-embedder` is Optional by design — a
// machine with no GPU completes every other step — so requiring it would make the
// declared graceful path impossible.
func (r *Runner) checkPreconditions(s *Step) error {
	for _, name := range s.Deps {
		if dep := r.byName[name]; dep != nil && dep.Optional {
			continue
		}
		prev, _ := r.store.Load(name)
		if prev == nil {
			return fmt.Errorf("step %q requires %q, which has never run", s.Name, name)
		}
		if prev.Status != StatusSuccess {
			return fmt.Errorf("step %q requires %q, whose last run is %s", s.Name, name, prev.Status)
		}
	}
	return r.checkAnyDeps(s)
}

// checkAnyDeps enforces the satisfaction half of AnyDeps: at least one of the
// alternative producers must have SUCCEEDED. It is checked at execution time
// rather than at planning time on purpose — `--only` bypasses the plan, and this
// is precisely the case the guard exists for.
func (r *Runner) checkAnyDeps(s *Step) error {
	if len(s.AnyDeps) == 0 {
		return nil
	}
	states := make([]string, 0, len(s.AnyDeps))
	for _, name := range s.AnyDeps {
		prev, _ := r.store.Load(name)
		switch {
		case prev == nil:
			states = append(states, name+"=never run")
		case prev.Status == StatusSuccess:
			return nil
		default:
			states = append(states, name+"="+string(prev.Status))
		}
	}
	return fmt.Errorf(
		"step %q needs one of [%s] to have produced its artefact, and none has (%s)",
		s.Name, strings.Join(s.AnyDeps, " | "), strings.Join(states, ", "))
}
