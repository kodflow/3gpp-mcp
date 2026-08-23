package goal

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// newTestCtx builds a runner over a throwaway tree. The steps are synthetic on
// purpose: these tests pin the STATE MACHINE's contract, not the corpus.
func newTestCtx(t *testing.T) (*Ctx, *Store) {
	t.Helper()
	root := t.TempDir()
	local := filepath.Join(root, ".local")
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(local)
	if err != nil {
		t.Fatal(err)
	}
	log, err := NewStepLog(local, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(log.Close)
	return &Ctx{
		Context: context.Background(),
		Root:    root,
		Local:   local,
		Data:    filepath.Join(root, "data"),
		Config:  map[string]string{},
		Log:     log,
	}, store
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// counter builds a step that records how many times it actually ran.
func counter(name string, deps []string, impl []string, out string, runs *int) *Step {
	return &Step{
		Name:    name,
		Version: 1,
		Doc:     "test step " + name,
		Deps:    deps,
		Impl:    impl,
		Heavy:   true,
		Outputs: func(c *Ctx) []string { return []string{filepath.Join(c.Root, out)} },
		Run: func(c *Ctx) error {
			*runs++
			return os.WriteFile(filepath.Join(c.Root, out), []byte("produced by "+name), 0o644)
		},
	}
}

// TestSkipRequiresAllFourConditions is the core contract: a step is skipped only
// when the previous run succeeded AND the fingerprint matches AND the outputs
// exist AND they validate. Each condition is broken in turn.
func TestSkipRequiresAllFourConditions(t *testing.T) {
	ctx, store := newTestCtx(t)
	write(t, filepath.Join(ctx.Root, "src", "a.go"), "package a")

	var runs int
	step := counter("a", nil, []string{"src/a.go"}, "out-a", &runs)
	r, err := NewRunner([]*Step{step}, ctx, store, func() string { return "tc" })
	if err != nil {
		t.Fatal(err)
	}

	if _, err := r.Execute(nil, false); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if runs != 1 {
		t.Fatalf("first run executed %d times, want 1", runs)
	}

	// (1) nothing changed -> skip
	if _, err := r.Execute(nil, false); err != nil {
		t.Fatal(err)
	}
	if runs != 1 {
		t.Fatalf("unchanged rerun executed the step again (runs=%d)", runs)
	}

	// (2) implementation changed -> run
	write(t, filepath.Join(ctx.Root, "src", "a.go"), "package a // edited")
	if _, err := r.Execute(nil, false); err != nil {
		t.Fatal(err)
	}
	if runs != 2 {
		t.Fatalf("implementation change did not trigger a replay (runs=%d)", runs)
	}

	// (3) output deleted -> run, even though the fingerprint still matches.
	if err := os.Remove(filepath.Join(ctx.Root, "out-a")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Execute(nil, false); err != nil {
		t.Fatal(err)
	}
	if runs != 3 {
		t.Fatalf("a missing output did not trigger a replay (runs=%d)", runs)
	}

	// (4) previous status not success -> run
	rec, _ := store.Load("a")
	rec.Status = StatusFailed
	if err := store.Save(rec); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Execute(nil, false); err != nil {
		t.Fatal(err)
	}
	if runs != 4 {
		t.Fatalf("a failed previous attempt did not trigger a replay (runs=%d)", runs)
	}
}

// TestCorruptOutputIsNotTrustedByFingerprint is the mission's cache test: an
// output whose CONTENT is broken must invalidate its step even though the
// fingerprint (which describes the inputs) still matches.
func TestCorruptOutputIsNotTrustedByFingerprint(t *testing.T) {
	ctx, store := newTestCtx(t)
	write(t, filepath.Join(ctx.Root, "src", "b.go"), "package b")

	var runs int
	step := counter("b", nil, []string{"src/b.go"}, "out-b", &runs)
	step.Validate = func(c *Ctx) error {
		b, err := os.ReadFile(filepath.Join(c.Root, "out-b"))
		if err != nil {
			return err
		}
		if !strings.HasPrefix(string(b), "produced by") {
			return errCorrupt
		}
		return nil
	}
	r, err := NewRunner([]*Step{step}, ctx, store, func() string { return "tc" })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Execute(nil, false); err != nil {
		t.Fatal(err)
	}
	if runs != 1 {
		t.Fatalf("runs=%d, want 1", runs)
	}

	// Corrupt the output in place: same size class, same fingerprint inputs.
	write(t, filepath.Join(ctx.Root, "out-b"), "GARBAGE")
	if _, err := r.Execute(nil, false); err != nil {
		t.Fatal(err)
	}
	if runs != 2 {
		t.Fatalf("a corrupt output was trusted because its fingerprint matched (runs=%d)", runs)
	}
}

var errCorrupt = &corruptErr{}

type corruptErr struct{}

func (*corruptErr) Error() string { return "output content is not what this step produces" }

// TestInvalidationIsTransitiveAndMinimal pins the A->B->C->D behaviour the
// mission spells out: changing C replays C and D, and leaves A and B alone.
func TestInvalidationIsTransitiveAndMinimal(t *testing.T) {
	ctx, store := newTestCtx(t)
	for _, n := range []string{"a", "b", "c", "d"} {
		write(t, filepath.Join(ctx.Root, "src", n+".go"), "package "+n)
	}
	var ra, rb, rc, rd int
	steps := []*Step{
		counter("a", nil, []string{"src/a.go"}, "out-a", &ra),
		counter("b", []string{"a"}, []string{"src/b.go"}, "out-b", &rb),
		counter("c", []string{"b"}, []string{"src/c.go"}, "out-c", &rc),
		counter("d", []string{"c"}, []string{"src/d.go"}, "out-d", &rd),
	}
	r, err := NewRunner(steps, ctx, store, func() string { return "tc" })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Execute(nil, false); err != nil {
		t.Fatal(err)
	}
	if ra+rb+rc+rd != 4 {
		t.Fatalf("first pass ran %d steps, want 4", ra+rb+rc+rd)
	}

	// Change ONLY c.
	write(t, filepath.Join(ctx.Root, "src", "c.go"), "package c // edited")
	res, err := r.Execute(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if ra != 1 {
		t.Errorf("a replayed (%d) although nothing it depends on changed", ra)
	}
	if rb != 1 {
		t.Errorf("b replayed (%d) although nothing it depends on changed", rb)
	}
	if rc != 2 {
		t.Errorf("c did not replay (%d)", rc)
	}
	if rd != 2 {
		t.Errorf("d did not replay after its dependency changed (%d)", rd)
	}
	if got := len(res.Ran); got != 2 {
		t.Errorf("ran %d steps, want exactly 2 (c and d): %v", got, res.Ran)
	}
}

// TestUnrelatedChangeDoesNotInvalidate is scenario B: editing a file no step
// declares must not replay anything.
func TestUnrelatedChangeDoesNotInvalidate(t *testing.T) {
	ctx, store := newTestCtx(t)
	write(t, filepath.Join(ctx.Root, "src", "a.go"), "package a")
	write(t, filepath.Join(ctx.Root, "README.md"), "hello")

	var runs int
	r, err := NewRunner([]*Step{counter("a", nil, []string{"src/a.go"}, "out-a", &runs)}, ctx, store, func() string { return "tc" })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Execute(nil, false); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(ctx.Root, "README.md"), "hello, edited")
	if _, err := r.Execute(nil, false); err != nil {
		t.Fatal(err)
	}
	if runs != 1 {
		t.Fatalf("an unrelated file edit replayed the step (runs=%d)", runs)
	}
}

// TestInterruptedStepIsNeverTrusted covers the crash-resume contract: a record
// left in `running` (the process died mid-step) must always replay, whatever its
// fingerprint says.
func TestInterruptedStepIsNeverTrusted(t *testing.T) {
	ctx, store := newTestCtx(t)
	write(t, filepath.Join(ctx.Root, "src", "a.go"), "package a")
	var runs int
	step := counter("a", nil, []string{"src/a.go"}, "out-a", &runs)
	r, err := NewRunner([]*Step{step}, ctx, store, func() string { return "tc" })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Execute(nil, false); err != nil {
		t.Fatal(err)
	}

	// Simulate the kill: the record keeps a valid fingerprint but says `running`.
	rec, _ := store.Load("a")
	rec.Status = StatusRunning
	if err := store.Save(rec); err != nil {
		t.Fatal(err)
	}
	plan, err := r.Plan(nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan[0].Action != ActionRun {
		t.Fatalf("an interrupted step was skipped: %s", plan[0].Reason)
	}
	if !strings.Contains(plan[0].Reason, "interrupted") {
		t.Errorf("reason should name the interruption, got %q", plan[0].Reason)
	}
}

// TestFailedStepKeepsItsCheckpoint proves a failure leaves resume information
// behind instead of losing the position.
func TestFailedStepKeepsItsCheckpoint(t *testing.T) {
	ctx, store := newTestCtx(t)
	write(t, filepath.Join(ctx.Root, "src", "a.go"), "package a")
	step := &Step{
		Name: "a", Version: 1, Impl: []string{"src/a.go"},
		Run: func(c *Ctx) error {
			c.Checkpoint("done", "584921")
			return errCorrupt
		},
	}
	r, err := NewRunner([]*Step{step}, ctx, store, func() string { return "tc" })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Execute(nil, false); err == nil {
		t.Fatal("a failing step should fail the run")
	}
	rec, err := store.Load("a")
	if err != nil || rec == nil {
		t.Fatalf("no record persisted after the failure: %v", err)
	}
	if rec.Status != StatusFailed {
		t.Errorf("status = %q, want failed", rec.Status)
	}
	if rec.Checkpoint["done"] != "584921" {
		t.Errorf("checkpoint lost: %v", rec.Checkpoint)
	}
	if rec.Error == "" {
		t.Error("the failure reason was not persisted")
	}
}

// TestOutputMissingAfterSuccessIsAFailure guards the exact trap the retired CI
// fell into: a worker that returns 0 while producing nothing must NOT be
// recorded as a success.
func TestOutputMissingAfterSuccessIsAFailure(t *testing.T) {
	ctx, store := newTestCtx(t)
	write(t, filepath.Join(ctx.Root, "src", "a.go"), "package a")
	step := &Step{
		Name: "a", Version: 1, Impl: []string{"src/a.go"},
		Outputs: func(c *Ctx) []string { return []string{filepath.Join(c.Root, "never-written")} },
		Run:     func(c *Ctx) error { return nil }, // "succeeds" without producing anything
	}
	r, err := NewRunner([]*Step{step}, ctx, store, func() string { return "tc" })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Execute(nil, false); err == nil {
		t.Fatal("a step that produced no output was accepted as successful")
	}
	rec, _ := store.Load("a")
	if rec.Status == StatusSuccess {
		t.Fatal("the record claims success although the declared output is missing")
	}
}

// TestToolchainOnlyAffectsDeclaredSteps pins the precision requirement: a
// compiler upgrade rebuilds what it compiles and nothing else.
func TestToolchainOnlyAffectsDeclaredSteps(t *testing.T) {
	ctx, store := newTestCtx(t)
	write(t, filepath.Join(ctx.Root, "src", "a.go"), "package a")
	write(t, filepath.Join(ctx.Root, "src", "b.go"), "package b")

	var rBuild, rData int
	build := counter("build", nil, []string{"src/a.go"}, "out-build", &rBuild)
	build.Toolchain = true
	data := counter("data", nil, []string{"src/b.go"}, "out-data", &rData)

	tc := "go1.0"
	r, err := NewRunner([]*Step{build, data}, ctx, store, func() string { return tc })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Execute(nil, false); err != nil {
		t.Fatal(err)
	}

	tc = "go2.0" // compiler upgraded
	if _, err := r.Execute(nil, false); err != nil {
		t.Fatal(err)
	}
	if rBuild != 2 {
		t.Errorf("the build step did not react to the toolchain change (runs=%d)", rBuild)
	}
	if rData != 1 {
		t.Errorf("a toolchain change replayed a data step (runs=%d) — it must not", rData)
	}
}

// TestPipelineShapeIsValid runs the real pipeline through the graph validator:
// no duplicate name, no unknown dependency, no cycle.
func TestPipelineShapeIsValid(t *testing.T) {
	ctx, store := newTestCtx(t)
	r, err := NewRunner(Pipeline(), ctx, store, func() string { return "tc" })
	if err != nil {
		t.Fatalf("the real pipeline is malformed: %v", err)
	}
	order := r.Order()
	if len(order) != len(Pipeline()) {
		t.Fatalf("topological order covers %d of %d steps", len(order), len(Pipeline()))
	}
	// Every dependency must appear before its dependant.
	pos := map[string]int{}
	for i, n := range order {
		pos[n] = i
	}
	for _, s := range Pipeline() {
		for _, d := range s.Deps {
			if pos[d] >= pos[s.Name] {
				t.Errorf("%s runs before its dependency %s", s.Name, d)
			}
		}
	}
	// The documented invariant: embed must come after merge, never before.
	if pos["embed"] < pos["merge"] {
		t.Error("embed is ordered before merge — shard-local chunk_ids would collide in the shared ledger")
	}
}

// TestStaleLockIsReclaimedButLiveLockIsNot pins the locking contract.
func TestStaleLockIsReclaimedButLiveLockIsNot(t *testing.T) {
	ctx, _ := newTestCtx(t)

	l1, err := AcquireLock(ctx.Local, "first")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	l1.Release()

	// A lock held by ANOTHER live process must be respected, however old it is —
	// a multi-hour GPU pass is normal and must never be stomped. We borrow a pid
	// that is always alive and never ours: init on POSIX, System on Windows.
	alive := 1
	if runtime.GOOS == "windows" {
		alive = 4
	}
	if !processAlive(alive) {
		t.Skipf("cannot probe liveness of pid %d on this platform", alive)
	}
	writeLockPID(t, ctx.Local, alive)
	if _, err := AcquireLock(ctx.Local, "second"); err == nil {
		t.Error("acquired a lock still held by a live process")
	} else if !strings.Contains(err.Error(), "already running") {
		t.Errorf("error should say the pipeline is already running, got: %v", err)
	}

	// A dead owner must be reclaimed: that is what unblocks the project after a
	// killed run, without ever needing a manual `rm lockfile`.
	writeLockPID(t, ctx.Local, 999999)
	if processAlive(999999) {
		t.Skip("pid 999999 happens to exist on this machine")
	}
	l2, err := AcquireLock(ctx.Local, "third")
	if err != nil {
		t.Fatalf("a stale lock was not reclaimed: %v", err)
	}
	l2.Release()
}

func writeLockPID(t *testing.T, local string, pid int) {
	t.Helper()
	p := filepath.Join(local, "locks", "goal.lock")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	host, _ := os.Hostname()
	body := `{"pid":` + itoa(pid) + `,"host":"` + host + `","started_at":"2020-01-01T00:00:00Z","command":"test"}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// TestWriteAtomicLeavesNoPartialFile checks the publish discipline used for
// every artefact the pipeline produces.
func TestWriteAtomicLeavesNoPartialFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "artefact.json")
	if err := WriteAtomic(p, []byte(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"ok":true}` {
		t.Fatalf("content = %q", b)
	}
	ents, _ := os.ReadDir(filepath.Dir(p))
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("a temporary file was left behind: %s", e.Name())
		}
	}
}

// TestImplHashIgnoresLineEndings proves a fingerprint is stable across a Windows
// and a Linux checkout of the same content.
func TestImplHashIgnoresLineEndings(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "f.go"), "package a\nfunc main() {}\n")
	lf, _, err := implHash(root, []string{"f.go"})
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "f.go"), "package a\r\nfunc main() {}\r\n")
	crlf, _, err := implHash(root, []string{"f.go"})
	if err != nil {
		t.Fatal(err)
	}
	if lf != crlf {
		t.Fatalf("the same content hashes differently with CRLF (%s) and LF (%s)", crlf, lf)
	}
}

// TestImplPathTypoIsAnError guards against the silent worst case: a step whose
// Impl points at nothing would have a constant fingerprint and would never
// replay again.
func TestImplPathTypoIsAnError(t *testing.T) {
	root := t.TempDir()
	if _, _, err := implHash(root, []string{"does/not/exist"}); err == nil {
		t.Fatal("a non-existent Impl path was accepted")
	}
}
