package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// TestChangelogNoteTellsTheTwoSilencesApart holds the 3GPP half to the standard
// the ETSI half was already held to: a count is not an answer on its own.
//
// Found by running the PUBLISHED image (sha256:ba1d95b7…) against all 13 tools in
// a chroot: get_changelog on TS 23.501 answered count 1, summary "Date". After the
// header rows are dropped it answers 0 — and a bare 0 reads as "this spec never
// changed", which is false. The table has had no writer since the ingest
// write-side moved to Rust (Phase 11b, c635038); it covers 311 of 3 568 specs and
// 3 326 of the 3 452 specs with records now hold a version newer than their
// newest recorded change.
func TestChangelogNoteTellsTheTwoSilencesApart(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// A spec the corpus holds at 18.5.0, whose change history stops at 17.2.0.
	_ = st.UpsertSpec(model.Spec{SpecID: "23.501", Series: "23", DocType: "TS"})
	_ = st.UpsertVersion(model.SpecVersion{SpecID: "23.501", Release: "Rel-17", Version: "17.2.0"})
	_ = st.UpsertVersion(model.SpecVersion{SpecID: "23.501", Release: "Rel-18", Version: "18.5.0"})
	// A spec whose ONLY records are the table header.
	_ = st.UpsertSpec(model.Spec{SpecID: "23.502", Series: "23", DocType: "TS"})
	_ = st.UpsertVersion(model.SpecVersion{SpecID: "23.502", Release: "Rel-18", Version: "18.4.0"})
	if err := st.InsertChanges([]model.Change{
		{CRNumber: "0123", SpecID: "23.501", FromVersion: "17.1.0", ToVersion: "17.2.0", Summary: "Correct NSSF discovery"},
		{SpecID: "23.502", Summary: "Date"},
	}); err != nil {
		t.Fatal(err)
	}

	// 1. Records that stop short must SAY where they stop.
	changes, err := st.GetChangelog(ctx, "23.501", "", "")
	if err != nil {
		t.Fatal(err)
	}
	note := changelogNote(ctx, st, "23.501", changes)
	if !strings.Contains(note, "17.2.0") || !strings.Contains(note, "18.5.0") {
		t.Errorf("a changelog that stops at 17.2.0 on a corpus holding 18.5.0 must name both versions; got %q", note)
	}

	// 2. A spec whose only rows were headers must not answer a bare 0.
	changes, err = st.GetChangelog(ctx, "23.502", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 0 {
		t.Fatalf("the header row survived GetChangelog: %+v", changes)
	}
	note = changelogNote(ctx, st, "23.502", changes)
	if note == "" || !strings.Contains(note, "trace_clause") {
		t.Errorf("an empty changelog must say what the zero means and name the tool that does answer; got %q", note)
	}

	// 3. A changelog level with the corpus says nothing extra — a note that always
	//    fires is noise, and stops being read exactly when it matters.
	_ = st.UpsertSpec(model.Spec{SpecID: "24.501", Series: "24", DocType: "TS"})
	_ = st.UpsertVersion(model.SpecVersion{SpecID: "24.501", Release: "Rel-18", Version: "18.3.0"})
	if err := st.InsertChanges([]model.Change{
		{CRNumber: "0900", SpecID: "24.501", FromVersion: "18.2.0", ToVersion: "18.3.0", Summary: "up to date"},
	}); err != nil {
		t.Fatal(err)
	}
	changes, _ = st.GetChangelog(ctx, "24.501", "", "")
	if note := changelogNote(ctx, st, "24.501", changes); note != "" {
		t.Errorf("a current changelog must carry no staleness note; got %q", note)
	}
}

// TestCompareVersionsIsNumeric pins the trap the staleness check would otherwise
// walk into: lexically "9.0.0" is greater than "18.0.0", so a string comparison
// would report a changelog stuck at 9.0.0 as AHEAD of a corpus holding 18.0.0 —
// silent precisely where the gap is largest.
func TestCompareVersionsIsNumeric(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"18.0.0", "9.0.0", 1},
		{"9.0.0", "18.0.0", -1},
		{"17.2.0", "17.2.0", 0},
		{"18.1", "18.1.0", 0},   // a missing field counts as 0, like versionOrderSQL
		{"18.x.0", "18.0.0", 0}, // a non-integer field counts as 0, like TRY_CAST
		{"", "0.0.0", 0},
	} {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}
