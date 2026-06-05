package store

import (
	"context"
	"testing"
	"time"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

func TestCatalogOverlay(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// Seed a spec + two versions as content ingest would (filename-guessed WG).
	_ = st.UpsertSpec(model.Spec{SpecID: "33.128", Series: "33", DocType: "TS", WorkingGroup: "SA3"})
	_ = st.UpsertVersion(model.SpecVersion{SpecID: "33.128", Release: "Rel-18", Version: "18.15.0"})
	_ = st.UpsertVersion(model.SpecVersion{SpecID: "33.128", Release: "Rel-19", Version: "19.6.0"})

	freeze18 := time.Date(2024, 6, 21, 0, 0, 0, 0, time.UTC)
	freeze19 := time.Date(2025, 12, 12, 0, 0, 0, 0, time.UTC)
	rels := []ReleaseRow{
		{Code: "Rel-18", Name: "Release 18", Status: "Frozen", FreezeDate: &freeze18, FreezeMeeting: "SA#104"},
		{Code: "Rel-19", Name: "Release 19", Status: "Frozen", FreezeDate: &freeze19, FreezeMeeting: "SA#110"},
	}
	if err := st.UpsertReleases(rels); err != nil {
		t.Fatal(err)
	}
	n, err := st.ApplyReleaseFreeze(ctx, rels)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("stamped %d version rows, want 2", n)
	}

	// Authoritative metadata overlay (SA3 -> S3).
	ok, err := st.UpdateSpecMeta(ctx, "33.128", "Security; ... Lawful Interception (LI); Stage 3", "TS", "S3")
	if err != nil || !ok {
		t.Fatalf("UpdateSpecMeta ok=%v err=%v", ok, err)
	}
	// A spec not on disk must NOT be created by the overlay.
	if ok, _ := st.UpdateSpecMeta(ctx, "99.999", "x", "TS", "S9"); ok {
		t.Error("overlay invented a catalogue-only spec")
	}
	if err := st.SetVersionMetadataSource(ctx, "dynareport"); err != nil {
		t.Fatal(err)
	}

	var wg, src, status string
	var fz time.Time
	if err := st.DB().QueryRowContext(ctx,
		`SELECT s.working_group, v.metadata_source, v.status, v.freeze_date
		 FROM specs s JOIN spec_versions v USING (spec_id)
		 WHERE s.spec_id='33.128' AND v.release='Rel-18'`).Scan(&wg, &src, &status, &fz); err != nil {
		t.Fatal(err)
	}
	if wg != "S3" {
		t.Errorf("working_group = %q, want S3", wg)
	}
	if src != "dynareport" {
		t.Errorf("metadata_source = %q, want dynareport", src)
	}
	if status != "Frozen" {
		t.Errorf("status = %q, want Frozen", status)
	}
	if fz.Format("2006-01-02") != "2024-06-21" {
		t.Errorf("freeze_date = %v, want 2024-06-21", fz)
	}
}

// TestUpdateSpecMetaNoClobber pins B2: a later overlay carrying EMPTY title/WG
// (a partial DynaReport row) must NOT erase metadata a fuller overlay wrote,
// while a non-empty value always wins.
func TestUpdateSpecMetaNoClobber(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	_ = st.UpsertSpec(model.Spec{SpecID: "23.501", Series: "23", DocType: "TS", WorkingGroup: "SA2"})

	// Full overlay writes good metadata.
	if ok, err := st.UpdateSpecMeta(ctx, "23.501", "System architecture for the 5G System", "TS", "S2"); err != nil || !ok {
		t.Fatalf("first overlay ok=%v err=%v", ok, err)
	}
	// Partial overlay: empty title + empty WG must be ignored; non-empty doc_type kept.
	if ok, err := st.UpdateSpecMeta(ctx, "23.501", "", "TS", ""); err != nil || !ok {
		t.Fatalf("partial overlay ok=%v err=%v", ok, err)
	}

	var title, dt, wg string
	if err := st.DB().QueryRowContext(ctx,
		`SELECT title, doc_type, working_group FROM specs WHERE spec_id='23.501'`).
		Scan(&title, &dt, &wg); err != nil {
		t.Fatal(err)
	}
	if title != "System architecture for the 5G System" {
		t.Errorf("title clobbered by empty overlay: %q", title)
	}
	if wg != "S2" {
		t.Errorf("working_group clobbered by empty overlay: %q", wg)
	}
	if dt != "TS" {
		t.Errorf("doc_type = %q, want TS", dt)
	}

	// A non-empty value still wins (authoritative correction).
	if _, err := st.UpdateSpecMeta(ctx, "23.501", "", "", "S2x"); err != nil {
		t.Fatal(err)
	}
	if err := st.DB().QueryRowContext(ctx,
		`SELECT working_group FROM specs WHERE spec_id='23.501'`).Scan(&wg); err != nil {
		t.Fatal(err)
	}
	if wg != "S2x" {
		t.Errorf("non-empty WG did not win: %q", wg)
	}
}
