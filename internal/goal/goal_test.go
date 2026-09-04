package goal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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
	lf, _, err := implHash(root, []string{"f.go"}, false)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "f.go"), "package a\r\nfunc main() {}\r\n")
	crlf, _, err := implHash(root, []string{"f.go"}, false)
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
	if _, _, err := implHash(root, []string{"does/not/exist"}, false); err == nil {
		t.Fatal("a non-existent Impl path was accepted")
	}
}

// TestCargoBinarySourcesAreFingerprinted pins the narrowing of skipDirs["bin"].
// A directory named bin holds build OUTPUT at ./bin and .local/bin, but Cargo
// puts the SOURCE of a crate's extra binaries in src/bin. Pruning by basename
// hid rust/store/src/bin/embed_io.rs, so a fix to the vector import left
// build-rust reporting VALID, cargo never re-ran, and the stale .exe kept being
// launched for another six hours.
func TestCargoBinarySourcesAreFingerprinted(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "rust", "store", "src", "lib.rs"), "pub fn a() {}\n")
	write(t, filepath.Join(root, "rust", "store", "src", "bin", "embed_io.rs"), "fn main() {}\n")
	write(t, filepath.Join(root, "rust", "bin", "prebuilt.exe"), "output, not source\n")

	before, per, err := implHash(root, []string{"rust"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := per["rust/store/src/bin/embed_io.rs"]; !ok {
		t.Fatalf("src/bin was pruned — a binary's source is invisible to the fingerprint; hashed %v", per)
	}
	if _, ok := per["rust/bin/prebuilt.exe"]; ok {
		t.Fatal("an output bin/ directory was folded into the fingerprint — every build would dirty the step")
	}

	write(t, filepath.Join(root, "rust", "store", "src", "bin", "embed_io.rs"), "fn main() { let _ = 1; }\n")
	after, _, err := implHash(root, []string{"rust"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("changing a Cargo binary source did NOT change the fingerprint — the step stays VALID with a stale binary")
	}
}

// TestPublishingTheCorpusDropsTheReplacedWAL pins the sidecar rule: a DuckDB WAL
// belongs to the FILE it was written for, and publishing a new corpus over that
// path orphans it. Leaving it made merge fail its own output validation with
// "Conflict on tuple deletion!" while the freshly merged DB sat on disk, intact
// and unopenable.
func TestPublishingTheCorpusDropsTheReplacedWAL(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "3gpp.duckdb")
	tmp := db + ".new"
	write(t, db, "the old database")
	write(t, db+".wal", "a WAL describing the OLD database")
	write(t, tmp, "the freshly merged database")

	if err := publishCorpus(tmp, db); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(db); err != nil || string(b) != "the freshly merged database" {
		t.Fatalf("the merged corpus was not published: %q %v", b, err)
	}
	if _, err := os.Stat(db + ".wal"); !os.IsNotExist(err) {
		t.Fatal("the replaced database's WAL survived — the next open replays it against a corpus it never belonged to")
	}

	// The common case is no WAL at all (a cleanly checkpointed corpus). That must
	// publish silently, not error on the missing sidecar.
	write(t, tmp, "a later merge")
	if err := publishCorpus(tmp, db); err != nil {
		t.Fatalf("publishing with no WAL beside the corpus must succeed: %v", err)
	}
}

// anyDepStep builds a consumer whose artefact has two alternative producers.
func anyDepStep(name string, anyDeps []string, out string, runs *int) *Step {
	s := counter(name, nil, nil, out, runs)
	s.AnyDeps = anyDeps
	return s
}

// TestAnyDepsIsSatisfiedByEitherProducer pins the reason AnyDeps exists:
// data/3gpp.duckdb is produced by `merge` OR by `seed`, and `embed` must be able
// to run on whichever one actually ran. Declaring only `merge` made the graph
// claim a seeded corpus can never be vectorised, which pushed operators onto
// `--only` — and `--only` skips dependency checking altogether.
func TestAnyDepsIsSatisfiedByEitherProducer(t *testing.T) {
	ctx, store := newTestCtx(t)
	var seedRuns, mergeRuns, consumerRuns int
	seed := counter("seed", nil, nil, "out-seed", &seedRuns)
	merge := counter("merge", nil, nil, "out-merge", &mergeRuns)
	consumer := anyDepStep("consumer", []string{"merge", "seed"}, "out-consumer", &consumerRuns)

	// Only `seed` is selected: `merge` never runs, yet the consumer must.
	r, err := NewRunner([]*Step{seed, merge, consumer}, ctx, store, func() string { return "tc" })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Execute(map[string]bool{"seed": true, "consumer": true}, false); err != nil {
		t.Fatalf("seed-only run: %v", err)
	}
	if mergeRuns != 0 {
		t.Fatalf("merge ran %d times; the seeded path must not require it", mergeRuns)
	}
	if consumerRuns != 1 {
		t.Fatalf("consumer ran %d times on a seeded corpus, want 1", consumerRuns)
	}
}

// TestAnyDepsRefusesWhenNoProducerRan is the half that makes it a guard rather
// than a relaxation. Before AnyDeps, `--only embed` ran happily against a DB
// nobody had produced and left `"merge": "missing"` in the state file as the only
// trace. Bypassing the ORDER must not also buy the right to skip the artefact.
func TestAnyDepsRefusesWhenNoProducerRan(t *testing.T) {
	ctx, store := newTestCtx(t)
	var seedRuns, mergeRuns, consumerRuns int
	seed := counter("seed", nil, nil, "out-seed", &seedRuns)
	merge := counter("merge", nil, nil, "out-merge", &mergeRuns)
	consumer := anyDepStep("consumer", []string{"merge", "seed"}, "out-consumer", &consumerRuns)

	r, err := NewRunner([]*Step{seed, merge, consumer}, ctx, store, func() string { return "tc" })
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Execute(map[string]bool{"consumer": true}, false)
	if err == nil {
		t.Fatal("consumer ran with neither producer materialised; the guard did not fire")
	}
	if consumerRuns != 0 {
		t.Fatalf("consumer body executed %d times despite the refusal", consumerRuns)
	}
	for _, want := range []string{"merge", "seed", "never run"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q — it must say what is missing", err, want)
		}
	}
}

// TestAnyDepsFoldsIntoTheFingerprint: a seeded corpus and a merged one are
// different corpora. Switching producer must replay the consumer, or the vectors
// would describe a corpus that no longer exists.
func TestAnyDepsFoldsIntoTheFingerprint(t *testing.T) {
	ctx, store := newTestCtx(t)
	var seedRuns, mergeRuns, consumerRuns int
	seed := counter("seed", nil, nil, "out-seed", &seedRuns)
	merge := counter("merge", nil, nil, "out-merge", &mergeRuns)
	consumer := anyDepStep("consumer", []string{"merge", "seed"}, "out-consumer", &consumerRuns)

	r, err := NewRunner([]*Step{seed, merge, consumer}, ctx, store, func() string { return "tc" })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Execute(map[string]bool{"seed": true, "consumer": true}, false); err != nil {
		t.Fatal(err)
	}
	if consumerRuns != 1 {
		t.Fatalf("consumer runs=%d after seeding, want 1", consumerRuns)
	}
	// Now the other producer materialises: the corpus changed underneath.
	if _, err := r.Execute(nil, false); err != nil {
		t.Fatal(err)
	}
	if mergeRuns != 1 {
		t.Fatalf("merge runs=%d, want 1", mergeRuns)
	}
	if consumerRuns != 2 {
		t.Fatalf("consumer runs=%d after the other producer ran, want 2 — a merged corpus is not the seeded one", consumerRuns)
	}
}

// TestSingleAnyDepIsRejected: one alternative is a Dep wearing a disguise, and it
// would quietly lose the ordinary dependency semantics.
func TestSingleAnyDepIsRejected(t *testing.T) {
	ctx, store := newTestCtx(t)
	var a, b int
	seed := counter("seed", nil, nil, "out-seed", &a)
	consumer := anyDepStep("consumer", []string{"seed"}, "out-consumer", &b)
	if _, err := NewRunner([]*Step{seed, consumer}, ctx, store, func() string { return "tc" }); err == nil {
		t.Fatal("a single AnyDeps entry was accepted; it must be rejected in favour of Deps")
	}
}

// TestUnknownAnyDepIsRejected keeps the same fail-closed contract Deps has.
func TestUnknownAnyDepIsRejected(t *testing.T) {
	ctx, store := newTestCtx(t)
	var a int
	consumer := anyDepStep("consumer", []string{"ghost", "phantom"}, "out-consumer", &a)
	if _, err := NewRunner([]*Step{consumer}, ctx, store, func() string { return "tc" }); err == nil {
		t.Fatal("an unknown alternative dependency was accepted")
	}
}

// TestOnlyStillChecksPreconditions: selecting a step restricts what RUNS. It must
// not also assert that what the step stands on is fine. Two sessions reached
// `embed` past `merge` with `--only` and nothing objected.
func TestOnlyStillChecksPreconditions(t *testing.T) {
	ctx, store := newTestCtx(t)
	var producerRuns, consumerRuns int
	producer := counter("producer", nil, nil, "out-producer", &producerRuns)
	consumer := counter("consumer", []string{"producer"}, nil, "out-consumer", &consumerRuns)

	r, err := NewRunner([]*Step{producer, consumer}, ctx, store, func() string { return "tc" })
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Execute(map[string]bool{"consumer": true}, false)
	if err == nil {
		t.Fatal("--only reached a step whose dependency never ran")
	}
	if consumerRuns != 0 {
		t.Fatalf("consumer body ran %d times despite the unmet precondition", consumerRuns)
	}
	if !strings.Contains(err.Error(), "producer") {
		t.Errorf("error %q must name the unmet dependency", err)
	}
}

// TestForceOnlyOverridesPreconditionsLoudly keeps the escape hatch usable while
// making it a different act from ordinary selection.
func TestForceOnlyOverridesPreconditionsLoudly(t *testing.T) {
	ctx, store := newTestCtx(t)
	var producerRuns, consumerRuns int
	producer := counter("producer", nil, nil, "out-producer", &producerRuns)
	consumer := counter("consumer", []string{"producer"}, nil, "out-consumer", &consumerRuns)

	r, err := NewRunner([]*Step{producer, consumer}, ctx, store, func() string { return "tc" })
	if err != nil {
		t.Fatal(err)
	}
	r.ForceOnly = true
	if _, err := r.Execute(map[string]bool{"consumer": true}, false); err != nil {
		t.Fatalf("--force-only must still run the step: %v", err)
	}
	if consumerRuns != 1 {
		t.Fatalf("consumer runs=%d under --force-only, want 1", consumerRuns)
	}
	if producerRuns != 0 {
		t.Fatalf("--force-only must not silently run the dependency (runs=%d)", producerRuns)
	}
}

// TestOptionalDependencyIsNotAPrecondition guards the declared graceful path: a
// machine with no GPU never builds the embedder, and requiring it would turn the
// designed degradation into a hard stop.
func TestOptionalDependencyIsNotAPrecondition(t *testing.T) {
	ctx, store := newTestCtx(t)
	var optRuns, consumerRuns int
	opt := counter("optional-producer", nil, nil, "out-opt", &optRuns)
	opt.Optional = true
	consumer := counter("consumer", []string{"optional-producer"}, nil, "out-consumer", &consumerRuns)

	r, err := NewRunner([]*Step{opt, consumer}, ctx, store, func() string { return "tc" })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Execute(map[string]bool{"consumer": true}, false); err != nil {
		t.Fatalf("an Optional dependency must not block the consumer: %v", err)
	}
	if consumerRuns != 1 {
		t.Fatalf("consumer runs=%d, want 1", consumerRuns)
	}
}

// The data contract must be checked against the SAME population `embed` was asked
// to fill. --require-embed-complete is floor-aware, and validate defaults its floor
// to "" (= every clause). Running embed at embed_floor="Rel-99" and then validating
// at "" failed a complete corpus on 413 GSM-era LCS clauses that are deliberately
// NULL — the contract was measuring rows nobody ever asked to vectorise.
func TestValidateInheritsTheFloorEmbedRanWith(t *testing.T) {
	ctx, _ := newTestCtx(t)
	ctx.Config["contract_flags"] = "--require-fts --require-hnsw --require-embed-complete"
	ctx.Config["embed_floor"] = "Rel-99"

	args := validateArgs(ctx)
	if !hasFlag(args, "--embed-floor") {
		t.Fatalf("validate must pass the embed floor, got %v", args)
	}
	for i, a := range args {
		if a == "--embed-floor" {
			if i+1 >= len(args) || args[i+1] != "Rel-99" {
				t.Fatalf("--embed-floor must carry embed's own floor, got %v", args)
			}
		}
	}
}

// No floor configured means "vectorise everything", and there the contract's own
// default ("" = count every clause) is already right. Passing an empty --embed-floor
// would be noise at best and, if validate ever tightened its parsing, a failure.
func TestValidateOmitsTheFloorWhenEmbedHasNone(t *testing.T) {
	ctx, _ := newTestCtx(t)
	ctx.Config["contract_flags"] = "--require-embed-complete"

	if args := validateArgs(ctx); hasFlag(args, "--embed-floor") {
		t.Fatalf("no embed_floor configured, so none should be passed, got %v", args)
	}
}

// An operator who writes the floor into contract_flags is making a decision. The
// step must not append a second one behind their back.
func TestAnExplicitContractFloorIsNotOverridden(t *testing.T) {
	ctx, _ := newTestCtx(t)
	ctx.Config["contract_flags"] = "--require-embed-complete --embed-floor=Rel-15"
	ctx.Config["embed_floor"] = "Rel-99"

	args := validateArgs(ctx)
	n := 0
	for _, a := range args {
		if strings.HasPrefix(strings.TrimLeft(a, "-"), "embed-floor") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("the operator's floor must stand alone, found %d occurrences in %v", n, args)
	}
}

func TestHasFlagAcceptsEverySpellingGoDoes(t *testing.T) {
	for _, spelling := range []string{"-embed-floor", "--embed-floor", "-embed-floor=Rel-9", "--embed-floor=Rel-9"} {
		if !hasFlag([]string{"--report", "text", spelling}, "--embed-floor") {
			t.Errorf("hasFlag missed %q — the caller would pass the flag twice", spelling)
		}
	}
	if hasFlag([]string{"--embed-floor-extra"}, "--embed-floor") {
		t.Error("hasFlag matched a different flag by prefix")
	}
}

// What the repair ACQUIRES must be what ingest READS.
//
// series.json (from the delta) and worklist.txt (from the repair plan) are two
// independent computations, and ingest walks series.json. On 2026-08-25 the repair
// work list carried six 34.123-1 entries, the series list carried no "34", and the
// six converted HTML files sat on disk unread — a fully "successful" repair that
// closed none of those holes, with nothing in any log to say so.
func TestTheSeriesListCoversWhatTheWorkListReaches(t *testing.T) {
	wl := "Rel-10 https://www.3gpp.org/ftp/Specs/archive/34_series/34.123-1/34123-1-a70.zip 34123-1-a70.zip\n" +
		"Rel-19 https://www.3gpp.org/ftp/Specs/archive/23_series/23.501/23501-j50.zip 23501-j50.zip\n"

	reached := seriesInWorklist(wl)
	if len(reached) != 2 || reached[0] != "23" || reached[1] != "34" {
		t.Fatalf("series reached by the work list = %v, want [23 34]", reached)
	}

	// The delta flagged only 23. The repair reaches 34 as well, so 34 must be added.
	extra := seriesNotIn([]string{"23"}, reached)
	if len(extra) != 1 || extra[0] != "34" {
		t.Fatalf("missing series = %v, want [34]", extra)
	}

	// And a series the delta already carries must not be duplicated.
	if got := seriesNotIn([]string{"23", "34"}, reached); len(got) != 0 {
		t.Fatalf("nothing should be added twice, got %v", got)
	}
}

// The series number comes from the ARCHIVE DIRECTORY, never from the file name:
// deciding where "34.123-1" ends is exactly the parsing that gets sub-part specs
// wrong, and the directory already says it unambiguously.
func TestTheSeriesComesFromTheArchiveDirectory(t *testing.T) {
	wl := "Rel-10 https://www.3gpp.org/ftp/Specs/archive/38_series/38.760-1/38760-1-030.zip 38760-1-030.zip\n"
	if got := seriesInWorklist(wl); len(got) != 1 || got[0] != "38" {
		t.Fatalf("a sub-part spec must still resolve to its series, got %v", got)
	}
	if got := seriesInWorklist("garbage with no archive path\n"); len(got) != 0 {
		t.Fatalf("a line with no archive path reaches no series, got %v", got)
	}
}

// The index step must supply the ceilings freeze-hnsw reads, because the DuckDB
// defaults are the slow path AND the failing one.
//
// Unset, the spill budget is 90% of whatever the disk happens to have free, which
// races the corpus file it is being written into: the 2026-08-25 run reported
// "Espace insuffisant sur le disque" with 57 GB free. With both ceilings set, the
// same 2 748 971 vectors froze in 1m46 instead of 19m05.
func TestTheIndexStepSuppliesItsOwnCeilings(t *testing.T) {
	t.Setenv("HNSW_BUILD_MEMORY_LIMIT", "")
	t.Setenv("HNSW_BUILD_TEMP_LIMIT", "")
	if got := envOr("HNSW_BUILD_MEMORY_LIMIT", "8GB"); got != "8GB" {
		t.Fatalf("unset must fall back to the step's own default, got %q", got)
	}
	if got := envOr("HNSW_BUILD_TEMP_LIMIT", "20GB"); got != "20GB" {
		t.Fatalf("unset must fall back to the step's own default, got %q", got)
	}
}

// An operator overriding a ceiling must win: the machine they are on is not
// necessarily the one these defaults were measured for.
func TestAnOperatorCeilingOverridesTheDefault(t *testing.T) {
	t.Setenv("HNSW_BUILD_MEMORY_LIMIT", "24GB")
	if got := envOr("HNSW_BUILD_MEMORY_LIMIT", "8GB"); got != "24GB" {
		t.Fatalf("the operator's value must win, got %q", got)
	}
	// Whitespace is not a value — it is an unset variable that went through a shell.
	t.Setenv("HNSW_BUILD_TEMP_LIMIT", "   ")
	if got := envOr("HNSW_BUILD_TEMP_LIMIT", "20GB"); got != "20GB" {
		t.Fatalf("a blank value must fall back, got %q", got)
	}
}

// Shell tests must be RUN, not merely written.
//
// scripts/etsi-corpus_test.sh and scripts/kaggle-gpu-check_test.sh were both
// written, both green, and both invisible to `go test ./...` — so they protected
// nothing. The mktemp break one of them guards is exactly the sort that only
// appears on one platform, which is the sort a forgotten test never catches.
func TestShellTestsAreDiscoveredAndRun(t *testing.T) {
	ctx, _ := newTestCtx(t)
	scripts := filepath.Join(ctx.Root, "scripts")
	if err := os.MkdirAll(scripts, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(scripts, "a_test.sh"), "#!/usr/bin/env bash\nexit 0\n")
	write(t, filepath.Join(scripts, "b_test.sh"), "#!/usr/bin/env bash\nexit 0\n")
	// Not a test: must be ignored rather than executed.
	write(t, filepath.Join(scripts, "helper.sh"), "#!/usr/bin/env bash\nexit 1\n")

	n, err := runShellTests(ctx)
	if err != nil {
		t.Fatalf("both shell tests pass, so the step must: %v", err)
	}
	if n != 2 {
		t.Fatalf("ran %d shell test(s), want 2 (helper.sh is not one)", n)
	}
}

// A failing shell test must FAIL the step. A runner that swallows the result is
// worse than no runner: it reports green over a broken script.
func TestAFailingShellTestFailsTheStep(t *testing.T) {
	ctx, _ := newTestCtx(t)
	scripts := filepath.Join(ctx.Root, "scripts")
	if err := os.MkdirAll(scripts, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(scripts, "broken_test.sh"), "#!/usr/bin/env bash\nexit 1\n")

	if _, err := runShellTests(ctx); err == nil {
		t.Fatal("a failing shell test must fail the step")
	}
}

// No shell tests at all is not an error — it is a repository that has none yet.
func TestNoShellTestsIsNotAFailure(t *testing.T) {
	ctx, _ := newTestCtx(t)
	n, err := runShellTests(ctx)
	if err != nil || n != 0 {
		t.Fatalf("an empty scripts/ must be silent, got n=%d err=%v", n, err)
	}
}

// Shell tests live in scripts/ AND in scripts/<pkg>/. A single-level glob would
// silently ignore scripts/lib/convert_test.sh — which is precisely the "written,
// green, executed by nobody" failure the runner exists to end.
func TestShellTestsAreFoundInSubdirectoriesToo(t *testing.T) {
	ctx, _ := newTestCtx(t)
	lib := filepath.Join(ctx.Root, "scripts", "lib")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(ctx.Root, "scripts", "top_test.sh"), "#!/usr/bin/env bash\nexit 0\n")
	write(t, filepath.Join(lib, "nested_test.sh"), "#!/usr/bin/env bash\nexit 0\n")

	n, err := runShellTests(ctx)
	if err != nil {
		t.Fatalf("both pass, so the step must: %v", err)
	}
	if n != 2 {
		t.Fatalf("ran %d shell test(s), want 2 — the nested one must not be missed", n)
	}
}

// --- the smoke postmortem -------------------------------------------------
//
// The 2026-08-26 03:50 run reported `initialize failed: EOF (server stderr: )`.
// An empty stderr and no exit code is a report that says nothing, and it sent
// the next reader looking for a bug in the server that may never have existed.

func TestThePostmortemNamesTheExitStatusAndKeepsTheStderr(t *testing.T) {
	cmd := helperProcess(t, "die")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	got := serverPostmortem(cmd, &stderr)
	if !strings.Contains(got, "the server died") {
		t.Fatalf("the postmortem must say the process died, got %q", got)
	}
	if !strings.Contains(got, "a word before dying") {
		t.Fatalf("the postmortem must carry the stderr the process wrote, got %q", got)
	}
}

// A process that says nothing must be REPORTED as saying nothing, rather than
// leaving an empty parenthesis for the reader to interpret.
func TestThePostmortemSaysWhenTheServerWroteNothing(t *testing.T) {
	cmd := helperProcess(t, "silent")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	got := serverPostmortem(cmd, &stderr)
	if !strings.Contains(got, "wrote nothing to stderr") {
		t.Fatalf("silence must be stated, not implied, got %q", got)
	}
	if !strings.Contains(got, "status 0") {
		t.Fatalf("a clean exit must still be named, got %q", got)
	}
}

// helperProcess re-executes this test binary as a child, the portable way to get
// a real process to autopsy without assuming any shell or coreutils exists —
// this toolchain has burnt us on `mktemp`, `sed` and `make` already.
func helperProcess(t *testing.T, mode string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestGoalHelperProcess")
	cmd.Env = append(os.Environ(), "GOAL_HELPER_MODE="+mode)
	return cmd
}

func TestGoalHelperProcess(t *testing.T) {
	mode := os.Getenv("GOAL_HELPER_MODE")
	if mode == "" {
		t.Skip("not the helper process")
	}
	switch mode {
	case "die":
		os.Stderr.WriteString("a word before dying\n")
		os.Exit(3)
	default:
		os.Exit(0)
	}
}

// --- the LI registry ------------------------------------------------------

// Rel-17 sorts before Rel-19 and the walk is lexical, so "the first match" is
// the WRONG match: it would document the LI events of a spec three releases
// behind the text the corpus serves.
func TestTheLIRegistryComesFromTheNewestRelease(t *testing.T) {
	c, _ := newTestCtx(t)
	for _, rel := range []string{"Rel-17", "Rel-18", "Rel-19", "Rel-9"} {
		dir := filepath.Join(c.dataPath("sources"), "asn", rel)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "TS33128Payloads.asn"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := findASN(c)
	if !strings.Contains(filepath.ToSlash(got), "/Rel-19/") {
		t.Fatalf("the newest release must win, got %q", got)
	}
}

func TestNoRegistryIsNotAnError(t *testing.T) {
	c, _ := newTestCtx(t)
	if got := findASN(c); got != "" {
		t.Fatalf("an absent registry must read as absent, got %q", got)
	}
}

func TestAReleaseNumberIsReadFromThePathNotTheName(t *testing.T) {
	for path, want := range map[string]int{
		"data/sources/asn/Rel-19/TS33128Payloads.asn": 19,
		"data/sources/asn/Rel-9/TS33128Payloads.asn":  9,
		"data/asn/TS33128Payloads.asn":                -1,
		"data/sources/asn/Rel-XX/TS33128Payloads.asn": -1,
	} {
		if got := releaseNumber(filepath.FromSlash(path)); got != want {
			t.Fatalf("releaseNumber(%q) = %d, want %d", path, got, want)
		}
	}
}

// --- what belongs in a fingerprint -----------------------------------------

// A fingerprint must capture what changes the OUTPUT, not the rate at which it
// is produced. `jobs` was in fetch's Extra, so raising the conversion
// parallelism replayed fetch and cascaded through ingest, merge, embed and
// index — half an hour of rework to answer a scheduling question.
func TestParallelismIsNotPartOfWhatFetchProduces(t *testing.T) {
	c, _ := newTestCtx(t)
	step := stepFetch()

	c.Config["floor"] = "Rel-99"
	c.Config["repair"] = "0"
	c.Config["jobs"] = "4"
	four, err := step.Extra(c)
	if err != nil {
		t.Fatal(err)
	}
	c.Config["jobs"] = "6"
	six, err := step.Extra(c)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(four, six) {
		t.Fatalf("jobs must not change the fetch fingerprint: %v vs %v", four, six)
	}
}

// The two knobs that DO change which specs are acquired must still replay it.
func TestTheAcquiredSetIsPartOfWhatFetchProduces(t *testing.T) {
	c, _ := newTestCtx(t)
	step := stepFetch()
	c.Config["floor"] = "Rel-99"
	c.Config["repair"] = "0"
	base, err := step.Extra(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"floor", "repair"} {
		save := c.Config[k]
		c.Config[k] = "changed"
		got, err := step.Extra(c)
		if err != nil {
			t.Fatal(err)
		}
		if reflect.DeepEqual(base, got) {
			t.Fatalf("%q changes which specs are fetched and must replay the step", k)
		}
		c.Config[k] = save
	}
}

// --- the external overlays acquire themselves -------------------------------

// They used to be a log line naming a script. A fresh clone therefore completed
// all 19 steps, reported success, and served an empty search_api and an empty
// li_events — the pipeline had named the command instead of running it.
func TestTheOverlayFetchScriptsArePartOfEnrich(t *testing.T) {
	step := stepEnrich()
	var got string
	for _, i := range step.Impl {
		got += i + " "
	}
	for _, want := range []string{"scripts/fetch-5g-apis.sh", "scripts/fetch-li-asn.sh"} {
		if !strings.Contains(got, want) {
			t.Errorf("enrich runs %s but does not list it as implementation: changing how an overlay is acquired would not replay the overlay", want)
		}
	}
}

// Acquiring the LI registry has to make the step dirty, or the corpus keeps
// whatever li_events it already had.
//
// This used to assert that the two DIRECTORIES were named, which is the one form
// that cannot work: inputsHash records a directory as the constant string "dir",
// so the trees were declared and never watched — the test passed for two months
// over a step that could not see a single file arrive. What has to hold is that a
// file acquired into either tree is fingerprinted, so that is what is asserted.
func TestTheAcquiredOverlaysAreInputsOfEnrich(t *testing.T) {
	c, _ := newTestCtx(t)
	api := filepath.Join(c.Data, "sources", "5g-apis", "Namf_Communication.yaml")
	asn := filepath.Join(c.Data, "sources", "asn", "TS33128Payloads.asn")
	for _, p := range []string{api, asn} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	in, err := stepEnrich().Inputs(c)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, i := range in {
		got[filepath.Clean(i)] = true
	}
	for _, want := range []string{api, asn} {
		if !got[filepath.Clean(want)] {
			t.Errorf("enrich does not take %s as an input: acquiring it would not re-run the overlay",
				filepath.Base(want))
		}
	}
}

// Refreshing is deliberate. Going to the network on every enrich re-downloads a
// release archive to discover nothing moved, and makes the step fail whenever
// the network does.
func TestOverlaysAreNotRefetchedUnlessAsked(t *testing.T) {
	t.Setenv("OVERLAY_REFRESH", "")
	if refreshOverlays() {
		t.Error("the default must not re-fetch overlays that are already on disk")
	}
	for _, v := range []string{"1", "true", "YES", " yes "} {
		t.Setenv("OVERLAY_REFRESH", v)
		if !refreshOverlays() {
			t.Errorf("OVERLAY_REFRESH=%q must force a refresh", v)
		}
	}
	t.Setenv("OVERLAY_REFRESH", "0")
	if refreshOverlays() {
		t.Error("OVERLAY_REFRESH=0 must not force a refresh")
	}
}

// --- the corpus shape is produced by the pipeline, not by remembering --------

// migrate-paragraphs existed as a tool someone had to run. A fresh clone would
// therefore complete all its steps and rebuild the OLD shape — the same failure
// as enrich printing the name of a script instead of running it.
func TestTheParagraphConversionIsAStepNotATool(t *testing.T) {
	var found *Step
	for _, s := range Pipeline() {
		if s.Name == "paragraphs" {
			found = s
		}
	}
	if found == nil {
		t.Fatal("the pipeline has no `paragraphs` step: a fresh clone rebuilds the pre-ADR-0004 corpus")
	}
	// After the vectors exist, or it would carry none across to the bodies.
	var deps string
	for _, d := range found.Deps {
		deps += d + " "
	}
	for _, want := range []string{"embed", "enrich"} {
		if !strings.Contains(deps, want) {
			t.Errorf("paragraphs must run after %s — it carries that step's output onto the bodies", want)
		}
	}
	var inGoBins bool
	for _, b := range goBins {
		if b == "migrate-paragraphs" {
			inGoBins = true
		}
	}
	if !inGoBins {
		t.Error("build-go does not build migrate-paragraphs: the step would run a binary that is never produced")
	}
}

// The index must be built AFTER the conversion, or it indexes the table the
// conversion is about to drop.
func TestTheIndexIsBuiltAfterTheConversion(t *testing.T) {
	var deps string
	for _, s := range Pipeline() {
		if s.Name == "index" {
			for _, d := range s.Deps {
				deps += d + " "
			}
		}
	}
	if !strings.Contains(deps, "paragraphs") {
		t.Fatalf("index deps = %q — it would index `clauses` and lose the index when that table goes", deps)
	}
}

// TestADeclinedStepDoesNotBlockWhatDependsOnIt pins the no-credential seed path.
//
// `seed` declines when there is no GHCR credential, so the corpus gets built
// from 3gpp.org instead — slower and equally correct. But its declared output,
// data/3gpp.duckdb, is then legitimately absent, and the runner called that
// "declared output missing after a successful run". The step was recorded
// FAILED and `discover`, which depends on it, never ran: the fallback the log
// message promised could not execute at all.
func TestADeclinedStepDoesNotBlockWhatDependsOnIt(t *testing.T) {
	ctx, store := newTestCtx(t)
	write(t, filepath.Join(ctx.Root, "src", "a.go"), "package a")
	write(t, filepath.Join(ctx.Root, "src", "b.go"), "package b")

	var ranA, ranB int
	declining := &Step{
		Name: "a", Version: 1, Impl: []string{"src/a.go"}, Heavy: true,
		Outputs: func(c *Ctx) []string { return []string{filepath.Join(c.Root, "never-written")} },
		// Validation describes a run that did the work; a decline must not reach it.
		Validate: func(c *Ctx) error { return errors.New("Validate ran on a declined step") },
		Run: func(c *Ctx) error {
			ranA++
			return fmt.Errorf("%w: no credential in this test", ErrDeclined)
		},
	}
	dependant := counter("b", []string{"a"}, []string{"src/b.go"}, "out-b", &ranB)

	r, err := NewRunner([]*Step{declining, dependant}, ctx, store, func() string { return "tc" })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Execute(nil, false); err != nil {
		t.Fatalf("a declined step failed the run: %v", err)
	}

	rec, err := store.Load("a")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != StatusSuccess || !rec.Declined {
		t.Fatalf("declined step recorded status=%q declined=%v, want success/true", rec.Status, rec.Declined)
	}
	if len(rec.Outputs) != 0 {
		t.Fatalf("a declined step claimed outputs: %v", rec.Outputs)
	}
	if ranB != 1 {
		t.Fatalf("the dependant ran %d times, want 1 — a decline must not block it", ranB)
	}

	// A decline is not cached as done. Its outputs are still absent, so the next
	// run re-evaluates it — which is how a credential that appears later is used.
	if _, err := r.Execute(nil, false); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if ranA != 2 {
		t.Fatalf("the declined step ran %d times over two runs, want 2 — a decline must not be treated as done", ranA)
	}
}
