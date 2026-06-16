package li

import (
	"strings"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

func TestCuratedETSILinks(t *testing.T) {
	all := CuratedETSILinks("")
	if len(all) < 5 {
		t.Fatalf("expected the curated LI→ETSI map, got %d", len(all))
	}
	// X1 must map to ETSI TS 103 221-1 with a citable deliver URL.
	var x1 *ETSILink
	for i := range all {
		if all[i].Interface == "LI_X1" {
			x1 = &all[i]
		}
	}
	if x1 == nil || x1.SpecID != "ETSI TS 103 221-1" {
		t.Fatalf("LI_X1 should map to ETSI TS 103 221-1, got %+v", x1)
	}
	if !strings.HasPrefix(x1.URL, "https://www.etsi.org/deliver/") {
		t.Errorf("X1 link missing deliver URL: %q", x1.URL)
	}
	// Filtering narrows to the requested interface only.
	x3 := CuratedETSILinks("LI_X3")
	if len(x3) == 0 {
		t.Fatal("LI_X3 filter returned nothing")
	}
	for _, l := range x3 {
		if l.Interface != "LI_X3" {
			t.Errorf("filter leaked %s", l.Interface)
		}
	}
}

func TestMineCitedETSI(t *testing.T) {
	clauses := []model.Clause{
		{Heading: "Scope", Text: "This references ETSI TS 103 221-1 and TS 102 232-2 for delivery."},
		{Heading: "X", Text: "see ETSI TS 103221-1 again (dup) and TR 101 331."},
		{Heading: "noise", Text: "TS 33.128 is 3GPP, not ETSI, must not match the 1NN NNN shape."},
	}
	got := MineCitedETSI(clauses)
	want := map[string]bool{"ETSI TS 103 221-1": true, "ETSI TS 102 232-2": true, "ETSI TS 101 331": true}
	if len(got) != len(want) {
		t.Fatalf("mined %v, want %d distinct ids", got, len(want))
	}
	for _, g := range got {
		if !want[g] {
			t.Errorf("unexpected mined id %q", g)
		}
	}
}
