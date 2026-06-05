package store

import (
	"context"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// TestReleaseRecencyOrdering pins the lineage sort against the Rel-99 trap: a
// naive regexp_extract(release,'[0-9]+') sorts 'Rel-99' as the NEWEST release
// (digits 99), when it is in fact the OLDEST (Release 1999, version major 3).
// releaseRecencySQL must put Rel-99 oldest and Rel-20 newest in both the
// oldest-first (releasesOrdered) and newest-first (ListReleases) paths.
func TestReleaseRecencyOrdering(t *testing.T) {
	ctx := context.Background()
	s := newMem(t)

	if err := s.UpsertSpec(model.Spec{SpecID: "23.501", Series: "23", DocType: "TS", WorkingGroup: "SA2"}); err != nil {
		t.Fatal(err)
	}
	// Insert in a deliberately scrambled order so the ORDER BY is what sorts them,
	// not insertion order. Rel-99 is the oldest; Rel-20 the newest.
	for _, vv := range []struct{ rel, ver string }{
		{"Rel-18", "18.5.0"},
		{"Rel-99", "3.12.0"},
		{"Rel-20", "20.0.0"},
		{"Rel-4", "4.8.0"},
		{"Rel-19", "19.6.0"},
	} {
		if err := s.UpsertVersion(model.SpecVersion{SpecID: "23.501", Release: vv.rel, Version: vv.ver}); err != nil {
			t.Fatal(err)
		}
	}

	// newest-first
	want := []string{"Rel-20", "Rel-19", "Rel-18", "Rel-4", "Rel-99"}
	vs, err := s.ListReleases(ctx, "23.501")
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != len(want) {
		t.Fatalf("ListReleases len = %d, want %d (%+v)", len(vs), len(want), vs)
	}
	for i, w := range want {
		if vs[i].Release != w {
			t.Errorf("ListReleases[%d] = %q, want %q (Rel-99 must NOT sort as newest)", i, vs[i].Release, w)
		}
	}

	// oldest-first (releasesOrdered is unexported; assert via the reverse of want)
	got, err := s.releasesOrdered(ctx, "23.501")
	if err != nil {
		t.Fatal(err)
	}
	wantOldestFirst := []string{"Rel-99", "Rel-4", "Rel-18", "Rel-19", "Rel-20"}
	if len(got) != len(wantOldestFirst) {
		t.Fatalf("releasesOrdered len = %d, want %d (%v)", len(got), len(wantOldestFirst), got)
	}
	for i, w := range wantOldestFirst {
		if got[i] != w {
			t.Errorf("releasesOrdered[%d] = %q, want %q (Rel-99 must sort oldest)", i, got[i], w)
		}
	}
}

// TestVersionRecencyOrdering pins the WITHIN-release version sort against the
// string-sort trap: a raw `version DESC` orders "18.9.0" AFTER "18.10.0" (because
// '9' > '1' lexically), so LatestVersion/VersionForRelease would cite the STALE
// v18.9.0 once a release reaches its 10th minor. versionRecencySQL must compare
// each dotted component as an integer so 18.10.0 > 18.9.0 > 18.2.0.
func TestVersionRecencyOrdering(t *testing.T) {
	ctx := context.Background()
	s := newMem(t)

	if err := s.UpsertSpec(model.Spec{SpecID: "23.501", Series: "23", DocType: "TS", WorkingGroup: "SA2"}); err != nil {
		t.Fatal(err)
	}
	// All within Rel-18, inserted scrambled. Mix one- and two-digit minors AND a
	// two-digit patch to exercise every component of the numeric comparison.
	for _, ver := range []string{"18.2.0", "18.10.0", "18.9.0", "18.10.1", "18.9.10"} {
		if err := s.UpsertVersion(model.SpecVersion{SpecID: "23.501", Release: "Rel-18", Version: ver}); err != nil {
			t.Fatal(err)
		}
	}

	want := []string{"18.10.1", "18.10.0", "18.9.10", "18.9.0", "18.2.0"}
	vs, err := s.ListReleases(ctx, "23.501")
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != len(want) {
		t.Fatalf("ListReleases len = %d, want %d (%+v)", len(vs), len(want), vs)
	}
	for i, w := range want {
		if vs[i].Version != w {
			t.Errorf("ListReleases[%d].Version = %q, want %q (numeric, not string, order)", i, vs[i].Version, w)
		}
	}

	// LatestVersion / VersionForRelease must surface the numerically-highest version.
	if _, v, ok, err := s.LatestVersion(ctx, "23.501"); err != nil || !ok || v != "18.10.1" {
		t.Errorf("LatestVersion = (%q, ok=%v, err=%v), want 18.10.1", v, ok, err)
	}
	if v, ok, err := s.VersionForRelease(ctx, "23.501", "Rel-18"); err != nil || !ok || v != "18.10.1" {
		t.Errorf("VersionForRelease(Rel-18) = (%q, ok=%v, err=%v), want 18.10.1", v, ok, err)
	}
}
