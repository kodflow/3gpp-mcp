package store

import (
	"context"
	"testing"
)

// TestLineageFollowsTheAxisThatActuallyVARIES is the regression test for lineage
// that answered nothing on half the corpus.
//
// ParagraphLineage and ClauseAvailability were written for 3GPP, where a spec is
// republished per release and `release` is what moves. ETSI has no releases:
// parse_etsi_meta stamps the constant "ETSI" on every deliverable, and it is
// `version` that moves — TS 103 221-1 has 23 published versions, TS 102 221 has
// 126. Grouping an ETSI clause by release therefore reported "present in [ETSI]"
// for every paragraph of every clause: not a wrong answer so much as no answer,
// on the corpus whose entire purpose is showing what changed between versions.
//
// Downloading every published version and then tracing it by a column that never
// varies would have bought storage and no feature at all.
func TestLineageFollowsTheAxisThatActuallyVARIES(t *testing.T) {
	ctx := context.Background()
	s := caFixture(t)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	// An ETSI deliverable: ONE release, several versions. The second version drops
	// a paragraph and adds another, which is exactly the evolution a user asks
	// about — and exactly what the release axis cannot see.
	//
	// The versions are chosen so a STRING sort disagrees with a numeric one:
	// 1.9.1 sorts after 1.21.1 lexically. Lineage that ordered them as strings
	// would report the paragraph dropped in 1.21.1 as the NEWEST state.
	for _, v := range []string{"1.9.1", "1.21.1"} {
		exec(`INSERT INTO spec_versions (spec_id, release, version) VALUES (?, 'ETSI', ?)`,
			"ETSI TS 103 221-1", v)
	}
	exec(`INSERT INTO paragraphs VALUES (50, 'the ADMF sends an X1 ActivateTask'), (51, 'legacy wording'), (52, 'added later')`)
	exec(`INSERT INTO bodies (body_id, heading) VALUES (50, 'X1 interface'), (51, 'X1 interface')`)
	exec(`INSERT INTO body_seq VALUES (50, 1, 50), (50, 2, 51), (51, 1, 50), (51, 2, 52)`)
	exec(`INSERT INTO clause_occ VALUES (5001, 'ETSI TS 103 221-1', 'ETSI', '1.9.1',  '6.2.1', true, 50)`)
	exec(`INSERT INTO clause_occ VALUES (5002, 'ETSI TS 103 221-1', 'ETSI', '1.21.1', '6.2.1', true, 51)`)

	axis, ordered, err := s.LineageAxis(ctx, "ETSI TS 103 221-1")
	if err != nil {
		t.Fatal(err)
	}
	if axis != "version" {
		t.Fatalf("axis = %q; an ETSI deliverable has one release and many versions, "+
			"so tracing it by release reports [ETSI] and nothing else", axis)
	}
	if len(ordered) != 2 || ordered[0] != "1.9.1" || ordered[1] != "1.21.1" {
		t.Fatalf("ordered = %v, want [1.9.1 1.21.1] oldest-first "+
			"(a string sort puts 1.9.1 last)", ordered)
	}

	traces, err := s.ParagraphLineage(ctx, "ETSI TS 103 221-1", "6.2.1")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, tr := range traces {
		got[tr.Text] = tr.PresentIn
	}
	if len(got) != 3 {
		t.Fatalf("lineage returned %d paragraph(s), want 3: %v", len(got), got)
	}
	// The point of the whole exercise: each statement is attributed to the
	// version(s) that carry it, not to a constant.
	for part, want := range map[string]int{
		"the ADMF sends an X1 ActivateTask": 2, // in both
		"legacy wording":                    1, // dropped
		"added later":                       1, // introduced
	} {
		if n := len(got[part]); n != want {
			t.Errorf("%q carried by %d version(s) %v, want %d", part, n, got[part], want)
		}
		for _, v := range got[part] {
			if v == "ETSI" {
				t.Fatalf("%q is still attributed to the release constant %q — the axis did not switch", part, v)
			}
		}
	}

	// The 3GPP half must be untouched: several releases, so the axis stays release.
	axis3, ordered3, err := s.LineageAxis(ctx, "23.501")
	if err != nil {
		t.Fatal(err)
	}
	if axis3 != "release" {
		t.Errorf("a spec republished across releases must keep the release axis, got %q", axis3)
	}
	if len(ordered3) != 4 || ordered3[0] != "Rel-16" {
		t.Errorf("3GPP ordering changed: %v", ordered3)
	}
}
