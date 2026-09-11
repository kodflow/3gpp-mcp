package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/glossaryseed"
)

// capture runs fn with os.Stdout and os.Stderr replaced by pipes and returns what
// each received. emit writes the log to one and the warnings to the other, and
// which one a line lands on is part of what is tested.
func capture(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()
	read := func(f **os.File) func() string {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		saved := *f
		*f = w
		done := make(chan string, 1)
		go func() {
			var b strings.Builder
			_, _ = io.Copy(&b, r)
			done <- b.String()
		}()
		return func() string {
			*f = saved
			_ = w.Close()
			out := <-done
			_ = r.Close()
			return out
		}
	}
	endOut, endErr := read(&os.Stdout), read(&os.Stderr)
	fn()
	return endOut(), endErr()
}

// WITHHELD ROWS ARE SAID APART, AND LOUDLY — and the guard's line counts only what
// it counts.
//
// The guard stopped counting withheld rows (they are not deletions; counted, they
// refused every run for as long as TS 21.905 stayed unread). What keeps them from
// passing unnoticed is this warning, on stderr: it says how many rows are held
// in place and why. The guard's own line says "removed" again, as it did before
// withheld rows existed, because removals are all it measures.
func TestWithheldRowsAreWarnedOnStderrApartFromTheGuard(t *testing.T) {
	rep := glossaryseed.Report{Applied: true, OK: true, Guard: "pass", Owned: 61, RemovalBound: 53,
		Withheld: 60, WithheldRows: []glossaryseed.RemovedRow{{Term: "S001", Expansion: "Stale entry 001",
			Source: "23.501"}},
		General: glossaryseed.GeneralReport{Spec: "21.905", Unread: "not in this corpus"}}
	stdout, stderr := capture(t, func() { emit(rep, "text") })

	for _, want := range []string{"WARNING", "60 seeded row(s)", "WITHHELD", "TS 21.905 could not be read (not in this corpus)"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr does not say %q about the withheld rows:\n%s", want, stderr)
		}
	}
	if want := "mass-removal guard: pass — 0 of 61 seeded row(s) removed (bound 53)"; !strings.Contains(stdout, want) {
		t.Errorf("the guard's line does not report removals alone; want %q in:\n%s", want, stdout)
	}
	if !strings.Contains(stdout, `withheld S001 = "Stale entry 001" (23.501, TS 21.905 unread)`) {
		t.Errorf("the withheld rows are not named in the log:\n%s", stdout)
	}

	// Nothing withheld, nothing to warn about.
	rep.Withheld, rep.WithheldRows, rep.General = 0, nil, glossaryseed.GeneralReport{Spec: "21.905",
		Version: "19.2.0", Release: "Rel-19", Pairs: 1300}
	if _, stderr := capture(t, func() { emit(rep, "text") }); strings.Contains(stderr, "WARNING") {
		t.Errorf("a run that withheld nothing warned anyway:\n%s", stderr)
	}
}
