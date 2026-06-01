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
