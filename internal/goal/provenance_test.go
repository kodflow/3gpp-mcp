package goal

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// tool returns a build-like step: it produces a binary, and depending on it is
// an availability constraint rather than a provenance one.
func tool(name string, impl []string, out string, runs *int) *Step {
	s := counter(name, nil, impl, out, runs)
	s.Tool = true
	return s
}

// TestARebuiltToolDoesNotReplayTheCorpus is the whole point of Step.Tool.
//
// Before it, `build-go` declared Impl{"cmd", "internal"} and `seed` depended on
// it, so editing ANY Go file under internal/ changed seed's fingerprint, and
// through seed's the fingerprint of discover, fetch, ingest, merge and embed.
// One comment in an orchestration file scheduled a 22 GB rebuild and hours of
// GPU. The package doc promised the opposite in as many words — "a change to
// cmd/server never invalidates the embeddings" — so this is the test that makes
// the promise true.
func TestARebuiltToolDoesNotReplayTheCorpus(t *testing.T) {
	ctx, store := newTestCtx(t)
	write(t, filepath.Join(ctx.Root, "src", "compiler.go"), "package build")
	write(t, filepath.Join(ctx.Root, "src", "data.go"), "package data")

	var built, ingested int
	steps := []*Step{
		tool("build", []string{"src/compiler.go"}, "out-build", &built),
		counter("ingest", []string{"build"}, []string{"src/data.go"}, "out-ingest", &ingested),
	}
	r, err := NewRunner(steps, ctx, store, func() string { return "tc" })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Execute(nil, false); err != nil {
		t.Fatal(err)
	}
	if built != 1 || ingested != 1 {
		t.Fatalf("first pass: built=%d ingested=%d, want 1 and 1", built, ingested)
	}

	// Edit ONLY the tool's own source. The tool must relink; the corpus must not
	// be touched.
	write(t, filepath.Join(ctx.Root, "src", "compiler.go"), "package build // edited")
	res, err := r.Execute(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if built != 2 {
		t.Errorf("the tool did not rebuild after its source changed (built=%d)", built)
	}
	if ingested != 1 {
		t.Errorf("a rebuilt tool replayed the data step (ingested=%d) — the cascade is back", ingested)
	}
	if len(res.Ran) != 1 || res.Ran[0] != "build" {
		t.Errorf("ran %v, want exactly [build]", res.Ran)
	}
}

// TestAToolDependencyStillOrdersAndGates: excluding a tool from the fingerprint
// must not demote it to a suggestion. It still sorts first, and a step whose tool
// never ran still refuses to run.
func TestAToolDependencyStillOrdersAndGates(t *testing.T) {
	ctx, store := newTestCtx(t)
	write(t, filepath.Join(ctx.Root, "src", "compiler.go"), "package build")
	write(t, filepath.Join(ctx.Root, "src", "data.go"), "package data")

	var built, ingested int
	steps := []*Step{
		tool("build", []string{"src/compiler.go"}, "out-build", &built),
		counter("ingest", []string{"build"}, []string{"src/data.go"}, "out-ingest", &ingested),
	}
	r, err := NewRunner(steps, ctx, store, func() string { return "tc" })
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Order(); got[0] != "build" {
		t.Fatalf("order %v does not put the tool first", got)
	}
	// Reaching the data step alone, with no tool ever built, must fail closed.
	if _, err := r.Execute(map[string]bool{"ingest": true}, false); err == nil {
		t.Fatal("ingest ran with its tool never built")
	}
	if ingested != 0 {
		t.Fatalf("ingest executed (%d) despite the failed precondition", ingested)
	}
}

// TestOnlyForceBuildsItsToolsButNotItsData is the fix for "a fix that was not
// built is inert", which this repository's runbook records happening five times
// to five different binaries. `--only sparse` must relink the sparse binary from
// the sources on disk, and must NOT drag a corpus rebuild in behind it.
func TestOnlyForceBuildsItsToolsButNotItsData(t *testing.T) {
	ctx, store := newTestCtx(t)
	for _, n := range []string{"compiler", "data", "index"} {
		write(t, filepath.Join(ctx.Root, "src", n+".go"), "package "+n)
	}
	var built, ingested, indexed int
	steps := []*Step{
		tool("build", []string{"src/compiler.go"}, "out-build", &built),
		counter("ingest", []string{"build"}, []string{"src/data.go"}, "out-ingest", &ingested),
		counter("index", []string{"ingest", "build"}, []string{"src/index.go"}, "out-index", &indexed),
	}
	r, err := NewRunner(steps, ctx, store, func() string { return "tc" })
	if err != nil {
		t.Fatal(err)
	}

	sel := r.WithToolDeps(map[string]bool{"index": true})
	if !sel["build"] {
		t.Error("--only index did not pull in the tool it launches")
	}
	if sel["ingest"] {
		t.Error("--only index pulled in a DATA dependency; asking for one step must stay one step")
	}

	// End to end, on the shape this actually failed in: a built corpus, a source
	// fix on disk, and an operator asking for the one step that consumes it.
	if _, err := r.Execute(nil, false); err != nil {
		t.Fatal(err)
	}
	if built != 1 || ingested != 1 || indexed != 1 {
		t.Fatalf("first pass: built=%d ingested=%d indexed=%d", built, ingested, indexed)
	}
	write(t, filepath.Join(ctx.Root, "src", "compiler.go"), "package compiler // the fix")
	if _, err := r.Execute(sel, false); err != nil {
		t.Fatal(err)
	}
	if built != 2 {
		t.Errorf("the fix was not built before the step that runs it (built=%d) — "+
			"this is the inert-fix trap", built)
	}
	if ingested != 1 {
		t.Errorf("--only index rebuilt the corpus (ingested=%d)", ingested)
	}
}

// TestToolDepsAreClosedTransitively: build-sparse needs toolchain, so asking for
// sparse must reach both.
func TestToolDepsAreClosedTransitively(t *testing.T) {
	ctx, store := newTestCtx(t)
	for _, n := range []string{"tc", "compiler", "data"} {
		write(t, filepath.Join(ctx.Root, "src", n+".go"), "package "+n)
	}
	var a, b, c int
	steps := []*Step{
		tool("toolchain", []string{"src/tc.go"}, "out-tc", &a),
		func() *Step {
			s := tool("build", []string{"src/compiler.go"}, "out-build", &b)
			s.Deps = []string{"toolchain"}
			return s
		}(),
		counter("sparse", []string{"build"}, []string{"src/data.go"}, "out-sparse", &c),
	}
	r, err := NewRunner(steps, ctx, store, func() string { return "tc" })
	if err != nil {
		t.Fatal(err)
	}
	sel := r.WithToolDeps(map[string]bool{"sparse": true})
	for _, want := range []string{"sparse", "build", "toolchain"} {
		if !sel[want] {
			t.Errorf("selection is missing %q: %v", want, sel)
		}
	}
}

// declining returns a step that always declines: it looked, found nothing to do,
// and produced none of its outputs on purpose — `embed` on a corpus where every
// clause already carries a vector.
func declining(name string, deps []string, impl []string, runs *int) *Step {
	return &Step{
		Name:    name,
		Version: 1,
		Doc:     "declining step " + name,
		Deps:    deps,
		Impl:    impl,
		Heavy:   true,
		Run: func(c *Ctx) error {
			*runs++
			return fmt.Errorf("nothing to embed: %w", ErrDeclined)
		},
	}
}

// TestADeclinedStepDoesNotReplayItsDependants.
//
// `embed` declines as soon as every clause carries a vector. That decline used
// to publish a fresh fingerprint, which re-froze the HNSW index and re-compacted
// 22 GB to reproduce bytes nobody had touched. A step that did nothing cannot
// have changed anything.
func TestADeclinedStepDoesNotReplayItsDependants(t *testing.T) {
	ctx, store := newTestCtx(t)
	write(t, filepath.Join(ctx.Root, "src", "embed.go"), "package embed")
	write(t, filepath.Join(ctx.Root, "src", "index.go"), "package index")

	var embedded, indexed int
	steps := []*Step{
		declining("embed", nil, []string{"src/embed.go"}, &embedded),
		counter("index", []string{"embed"}, []string{"src/index.go"}, "out-index", &indexed),
	}
	r, err := NewRunner(steps, ctx, store, func() string { return "tc" })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Execute(nil, false); err != nil {
		t.Fatal(err)
	}
	if embedded != 1 || indexed != 1 {
		t.Fatalf("first pass: embed=%d index=%d, want 1 and 1", embedded, indexed)
	}

	// Bump the declining step's implementation: it must re-evaluate (a decline is
	// never cached), and it must still declare nothing changed downstream.
	write(t, filepath.Join(ctx.Root, "src", "embed.go"), "package embed // edited")
	if _, err := r.Execute(nil, false); err != nil {
		t.Fatal(err)
	}
	if embedded != 2 {
		t.Errorf("the declining step was cached (embed=%d); a decline must be re-evaluated", embedded)
	}
	if indexed != 1 {
		t.Errorf("a decline replayed the index (index=%d) to reproduce bytes nobody wrote", indexed)
	}
}

// rewriter writes `content` to its output every run.
func rewriter(name string, deps []string, impl []string, out string, content *string, runs *int) *Step {
	return &Step{
		Name:    name,
		Version: 1,
		Doc:     "rewriting step " + name,
		Deps:    deps,
		Impl:    impl,
		Heavy:   true,
		Outputs: func(c *Ctx) []string { return []string{filepath.Join(c.Root, out)} },
		Run: func(c *Ctx) error {
			*runs++
			return os.WriteFile(filepath.Join(c.Root, out), []byte(*content), 0o644)
		},
	}
}

// TestAStepThatRewroteItsOutputDOESReplayItsDependants is the negative control,
// and it is the more important half. Suppressing propagation is only safe while
// a real rewrite is still detected: the alternative is a stale index served over
// changed data, silently.
func TestAStepThatRewroteItsOutputDOESReplayItsDependants(t *testing.T) {
	ctx, store := newTestCtx(t)
	write(t, filepath.Join(ctx.Root, "src", "merge.go"), "package merge")
	write(t, filepath.Join(ctx.Root, "src", "index.go"), "package index")

	content := "corpus v1"
	var merged, indexed int
	steps := []*Step{
		rewriter("merge", nil, []string{"src/merge.go"}, "out-merge", &content, &merged),
		counter("index", []string{"merge"}, []string{"src/index.go"}, "out-index", &indexed),
	}
	r, err := NewRunner(steps, ctx, store, func() string { return "tc" })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Execute(nil, false); err != nil {
		t.Fatal(err)
	}
	if merged != 1 || indexed != 1 {
		t.Fatalf("first pass: merge=%d index=%d", merged, indexed)
	}

	// merge re-runs and genuinely produces a DIFFERENT corpus.
	content = "corpus v2 — longer, and different"
	write(t, filepath.Join(ctx.Root, "src", "merge.go"), "package merge // edited")
	if _, err := r.Execute(nil, false); err != nil {
		t.Fatal(err)
	}
	if merged != 2 {
		t.Fatalf("merge did not replay (%d)", merged)
	}
	if indexed != 2 {
		t.Fatalf("merge rewrote the corpus and the index was NOT replayed (index=%d) — "+
			"this is the stale-index-over-changed-data failure", indexed)
	}
}

// TestDroppingADeterminantIsNotAChange.
//
// Making build steps ordering-only removed `dep:build-go` from every data step's
// fingerprint. The plan then offered to replay a finished 22 GB corpus, naming as
// its reason "dependency output changed: build-go (removed)" — a determinant
// nobody counts any more, which cannot have changed anything.
func TestDroppingADeterminantIsNotAChange(t *testing.T) {
	prev := &Record{
		StepVersion: 1,
		Impl:        map[string]string{"a.rs": "h1"},
		Deps:        map[string]string{"build-go": "old", "merge": "m1"},
		Inputs:      map[string]string{"in": "1:2"},
	}
	cur := &Record{
		StepVersion: 1,
		Impl:        map[string]string{"a.rs": "h1"},
		Deps:        map[string]string{"merge": "m1"}, // build-go is no longer a determinant
		Inputs:      map[string]string{"in": "1:2"},
	}
	if !onlyDroppedDeterminants(prev, cur) {
		t.Error("dropping a determinant was read as a change")
	}

	// Everything that IS still a determinant must be compared exactly.
	for _, tc := range []struct {
		name string
		mut  func(*Record)
	}{
		{"a dependency that still counts moved", func(r *Record) { r.Deps["merge"] = "m2" }},
		{"an implementation file changed", func(r *Record) { r.Impl["a.rs"] = "h2" }},
		{"an input moved", func(r *Record) { r.Inputs["in"] = "9:9" }},
		{"the step version was bumped", func(r *Record) { r.StepVersion = 2 }},
		{"a new determinant appeared", func(r *Record) { r.Deps["enrich"] = "e1" }},
		{"the environment changed", func(r *Record) { r.Environment = map[string]string{"floor": "Rel-15"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Record{
				StepVersion: cur.StepVersion,
				Impl:        map[string]string{"a.rs": "h1"},
				Deps:        map[string]string{"merge": "m1"},
				Inputs:      map[string]string{"in": "1:2"},
			}
			tc.mut(c)
			if onlyDroppedDeterminants(prev, c) {
				t.Errorf("%s was forgiven as a dropped determinant", tc.name)
			}
		})
	}

	// And an identical record is not a "drop" — nothing was removed, so this path
	// must not be the one that answers.
	if onlyDroppedDeterminants(prev, prev) {
		t.Error("an unchanged record took the dropped-determinant path")
	}
}
