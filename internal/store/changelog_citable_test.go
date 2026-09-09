package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// TestGetChangelogDropsTheTableHeader pins the defect the container test found on
// the published image: get_changelog answered TS 23.501 with count 1 and summary
// "Date" — the change-history table's HEADER row, read positionally like a body
// row and stored as a change record.
//
// The published corpus holds 3 352 such rows, 3 026 of them summarised "Date".
// They name no CR and no version transition, so nothing a caller can check.
//
// The test also pins the OTHER side, which is what makes the predicate the weak
// one rather than the obvious one: an MCC editorial row carries a real version
// transition and NO CR number, and 1 047 of those are in the corpus. A filter
// that demanded a CR number would discard them and take the citable spec count
// from 311 down to 289.
func TestGetChangelogDropsTheTableHeader(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "c.duckdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if err := st.Reset(ctx); err != nil {
		t.Fatal(err)
	}

	rows := []model.Change{
		// The header row, exactly as it lands: every field empty but the column title.
		{SpecID: "23.501", Summary: "Date"},
		{SpecID: "23.501", Summary: "TSG SA#"},
		// A real CR.
		{CRNumber: "0123", CRRevision: 1, SpecID: "23.501", FromVersion: "17.0.0", ToVersion: "17.1.0", Category: "F", Summary: "Correct the NSSF discovery"},
		// An MCC editorial update: no CR number, a real transition. MUST survive.
		{SpecID: "23.501", FromVersion: "17.1.0", ToVersion: "17.2.0", Summary: "Update to Rel-17 version (MCC)"},
	}
	if err := st.InsertChanges(rows); err != nil {
		t.Fatal(err)
	}

	got, err := st.GetChangelog(ctx, "23.501", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("GetChangelog returned %d record(s), want 2 — the two header rows must be dropped and the two real ones kept", len(got))
	}
	for _, c := range got {
		if c.Summary == "Date" || c.Summary == "TSG SA#" {
			t.Errorf("a column header survived as a change record: %q", c.Summary)
		}
		if !c.Citable() {
			t.Errorf("an uncitable record survived: %+v", c)
		}
	}
	// The MCC row is the one a stricter filter would have eaten.
	var sawMCC bool
	for _, c := range got {
		if c.CRNumber == "" && c.ToVersion == "17.2.0" {
			sawMCC = true
		}
	}
	if !sawMCC {
		t.Error("the MCC editorial row was dropped; the predicate is stricter than it should be")
	}
}
