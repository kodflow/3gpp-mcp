package goal

import (
	"path/filepath"
	"testing"
)

// TestAnEnumeratorThatFoundTheSameThingDoesNotReplayTheDownload.
//
// `discover` and `discover-etsi` rewrite their work list on every run, so the
// file's mtime always moved even when the catalogue upstream had not. That was
// enough to replay fetch, ingest and merge on the 3GPP side, and hours of PDF
// conversion on the ETSI side — over a corpus that had since been converted to
// the content-addressed layout and compacted, which is the state this repository
// has already destroyed a corpus from once.
func TestAnEnumeratorThatFoundTheSameThingDoesNotReplayTheDownload(t *testing.T) {
	ctx, store := newTestCtx(t)
	write(t, filepath.Join(ctx.Root, "src", "discover.go"), "package discover")
	write(t, filepath.Join(ctx.Root, "src", "fetch.go"), "package fetch")

	worklist := "spec-a\nspec-b\n"
	var discovered, fetched int
	enumerate := rewriter("discover", nil, []string{"src/discover.go"}, "worklist.txt", &worklist, &discovered)
	enumerate.OutputsComplete = true
	steps := []*Step{
		enumerate,
		counter("fetch", []string{"discover"}, []string{"src/fetch.go"}, "out-fetch", &fetched),
	}
	r, err := NewRunner(steps, ctx, store, func() string { return "tc" })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Execute(nil, false); err != nil {
		t.Fatal(err)
	}
	if discovered != 1 || fetched != 1 {
		t.Fatalf("first pass: discover=%d fetch=%d", discovered, fetched)
	}

	// The enumerator is asked again — its own source changed — and upstream still
	// holds exactly the same deliverables.
	write(t, filepath.Join(ctx.Root, "src", "discover.go"), "package discover // edited")
	if _, err := r.Execute(nil, false); err != nil {
		t.Fatal(err)
	}
	if discovered != 2 {
		t.Fatalf("the enumerator did not re-run (%d)", discovered)
	}
	if fetched != 1 {
		t.Errorf("an unchanged work list replayed the download (fetch=%d)", fetched)
	}

	// And the moment it enumerates something DIFFERENT, the download must follow.
	worklist = "spec-a\nspec-b\nspec-c\n"
	write(t, filepath.Join(ctx.Root, "src", "discover.go"), "package discover // again")
	if _, err := r.Execute(nil, false); err != nil {
		t.Fatal(err)
	}
	if fetched != 2 {
		t.Fatalf("a CHANGED work list did not replay the download (fetch=%d)", fetched)
	}
}

// TestOutputsCompleteIsOptIn: the same shape, without the assertion, must keep
// propagating. Most steps of this pipeline write more than they declare — embed
// declares its ledger and writes vectors into the DuckDB — so the safe reading of
// an undeclared step is that running it changed something.
func TestOutputsCompleteIsOptIn(t *testing.T) {
	ctx, store := newTestCtx(t)
	write(t, filepath.Join(ctx.Root, "src", "produce.go"), "package produce")
	write(t, filepath.Join(ctx.Root, "src", "consume.go"), "package consume")

	same := "identical every time"
	var produced, consumed int
	steps := []*Step{
		rewriter("produce", nil, []string{"src/produce.go"}, "out-produce", &same, &produced),
		counter("consume", []string{"produce"}, []string{"src/consume.go"}, "out-consume", &consumed),
	}
	r, err := NewRunner(steps, ctx, store, func() string { return "tc" })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Execute(nil, false); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(ctx.Root, "src", "produce.go"), "package produce // edited")
	if _, err := r.Execute(nil, false); err != nil {
		t.Fatal(err)
	}
	if produced != 2 {
		t.Fatalf("the producer did not re-run (%d)", produced)
	}
	if consumed != 2 {
		t.Errorf("a step that did not assert OutputsComplete suppressed propagation "+
			"(consume=%d) — undeclared writes would be served as unchanged", consumed)
	}
}

// TestEveryOutputsCompleteStepDeclaresOutputs guards the one way the assertion
// can be vacuously true: claiming the outputs are the whole effect while
// declaring none. That would read as "nothing changed" forever.
func TestEveryOutputsCompleteStepDeclaresOutputs(t *testing.T) {
	for _, s := range Pipeline() {
		if s.OutputsComplete && s.Outputs == nil {
			t.Errorf("step %q asserts OutputsComplete but declares no Outputs", s.Name)
		}
	}
}

// TestALegacyOutputRecordIsComparedOnWhatItRecorded.
//
// Records written before outputs carried a content hash said only "N bytes".
// Treating them as incomparable would have made the first run under the new
// scheme replay the whole acquisition chain — thirty hours of LibreOffice — to
// reproduce a work list that had not moved. Size is what those records knew, so
// size is what they are held to; the window closes after one run.
func TestALegacyOutputRecordIsComparedOnWhatItRecorded(t *testing.T) {
	legacy := map[string]string{"worklist.txt": "1842192 bytes"}
	fresh := map[string]string{"worklist.txt": "1842192 bytes sha=2f898e6b1bc29f25"}
	if !sameOutputs(legacy, fresh) {
		t.Error("a legacy record of the same size was judged different")
	}
	grown := map[string]string{"worklist.txt": "1842193 bytes sha=aaaaaaaaaaaaaaaa"}
	if sameOutputs(legacy, grown) {
		t.Error("a legacy record of a DIFFERENT size was judged the same")
	}
	// Two modern records must still be held to their full identity: same size,
	// different content is a change.
	sameSize := map[string]string{"worklist.txt": "1842192 bytes sha=ffffffffffffffff"}
	if sameOutputs(fresh, sameSize) {
		t.Error("two content hashes that differ were judged the same")
	}
	// A qualifier that merely switched kind is not the legacy case.
	mtime := map[string]string{"worklist.txt": "1842192 bytes @1788419264580003400"}
	if sameOutputs(fresh, mtime) {
		t.Error("a sha identity and an mtime identity were judged the same")
	}
	// And a missing key is never a match.
	if sameOutputs(legacy, map[string]string{"other.txt": "1842192 bytes"}) {
		t.Error("a different output path was judged the same")
	}
}
