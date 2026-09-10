package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// TestGetChangelogHonoursTheReleaseRange pins a defect that was invisible for as
// long as the `changes` table had no writer.
//
// from_release and to_release have been in this signature, and in the tool's
// schema, since the changelog existed — and the query filtered on spec_id alone.
// While the table was a fossil the specs that had any records had a handful each,
// so "every record" and "the records in this range" were the same answer. With the
// CR database behind it, TS 23.501 holds 3 064 records spanning Rel-15 to Rel-20:
// a request for Rel-18..Rel-19 was returning all of them, and changelogNote then
// computed "this history stops at …" from a set the caller never asked for.
func TestGetChangelogHonoursTheReleaseRange(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "c.duckdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	if err := st.InsertChanges([]model.Change{
		{SpecID: "23.501", CRNumber: "0001", FromVersion: "15.0.0", ToVersion: "15.1.0", Summary: "in Rel-15"},
		{SpecID: "23.501", CRNumber: "0002", FromVersion: "17.5.0", ToVersion: "18.0.0", Summary: "into Rel-18"},
		{SpecID: "23.501", CRNumber: "0003", FromVersion: "18.2.0", ToVersion: "19.1.0", Summary: "into Rel-19"},
		{SpecID: "23.501", CRNumber: "0004", FromVersion: "19.4.0", ToVersion: "20.2.0", Summary: "into Rel-20"},
	}); err != nil {
		t.Fatal(err)
	}

	summaries := func(from, to string) []string {
		got, err := st.GetChangelog(ctx, "23.501", from, to)
		if err != nil {
			t.Fatalf("GetChangelog(%q,%q): %v", from, to, err)
		}
		var out []string
		for _, c := range got {
			out = append(out, c.Summary)
		}
		return out
	}

	// The bounds are INCLUSIVE and they are read against the version a change
	// LANDED IN, which is the version a caller asking "what changed in Rel-18"
	// means.
	if got := summaries("Rel-18", "Rel-19"); len(got) != 2 ||
		got[0] != "into Rel-18" || got[1] != "into Rel-19" {
		t.Errorf("Rel-18..Rel-19 = %v, want the two records that landed in those releases", got)
	}
	if got := summaries("Rel-20", "Rel-20"); len(got) != 1 || got[0] != "into Rel-20" {
		t.Errorf("Rel-20..Rel-20 = %v, want only the Rel-20 record", got)
	}
	// An absent bound is UNBOUNDED, not zero — the difference between "no filter"
	// and "matches nothing".
	if got := summaries("", ""); len(got) != 4 {
		t.Errorf("unbounded = %v, want all four", got)
	}
	if got := summaries("Rel-19", ""); len(got) != 2 {
		t.Errorf("Rel-19.. = %v, want the Rel-19 and Rel-20 records", got)
	}
	// A label this store does not recognise must not silently become a bound of 0,
	// which would match nothing and look like "this spec never changed".
	if got := summaries("not-a-release", ""); len(got) != 4 {
		t.Errorf("unparseable bound = %v, want it ignored rather than matching nothing", got)
	}
}

// TestReleaseMajorHandlesTheOneException pins the mapping the range filter rests
// on, including the case that is unreachable on the corpus that ships today.
//
// From Rel-4 onwards a release publishes under its own number: Rel-15 ships
// 15.x.y. Rel-99 ships 3.x.y — it was named for the year, and the numbering only
// lined up afterwards. The corpus carries Rel-4 → Rel-20 and no Rel-99 (PR #320),
// so that branch cannot fire on today's data; it exists because the alternative is
// a silent off-by-twelve the first time a Phase-2 deliverable is indexed.
func TestReleaseMajorHandlesTheOneException(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
		ok   bool
	}{
		{"Rel-15", 15, true},
		{"Rel-4", 4, true},
		{"Rel-20", 20, true},
		{"rel-18", 18, true},
		{" Rel-19 ", 19, true},
		{"18", 18, true},
		{"Rel-99", 3, true},
		{"", 0, false},
		{"latest", 0, false},
		{"Rel-", 0, false},
	} {
		got, ok := releaseMajor(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("releaseMajor(%q) = (%d,%v), want (%d,%v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}
