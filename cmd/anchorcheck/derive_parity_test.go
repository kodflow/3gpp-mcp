package main

import (
	"fmt"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/anchor"
)

// THE ANCHOR A SEED DERIVES AND THE CHECK THAT JUDGES IT USE ONE ORDER.
//
// cmd/derive-anchor writes the anchor with anchor.CmpVer; this command judges an
// anchor with its own CmpVer. Two copies, deliberately for now: moving this
// command onto the package would change the binary `validate` runs and replay
// the 8-minute gate for nothing. What must not happen is that the two disagree —
// the derivation would keep a version the check calls older, and every freshly
// seeded anchor would open with a false over-claim. So they are held to each
// other here, exhaustively over the shapes 3GPP versions take.
func TestTheDerivedAnchorAndTheCheckOrderVersionsAlike(t *testing.T) {
	var vs []string
	for _, v := range []string{"", "0", "1.0", "1.0.0", "19.x", "x", "2.9.0", "2.10.0", "19.14.0", "19.9.0", "18.5.1", "20.0.0", "0.3.0", "1.2.3.4"} {
		vs = append(vs, v)
	}
	for a := 0; a < 25; a++ {
		for b := 0; b < 25; b += 4 {
			vs = append(vs, fmt.Sprintf("%d.%d.0", a, b))
		}
	}
	for _, x := range vs {
		for _, y := range vs {
			if got, want := anchor.CmpVer(x, y), CmpVer(x, y); got != want {
				t.Fatalf("anchor.CmpVer(%q,%q)=%d, anchorcheck CmpVer=%d", x, y, got, want)
			}
		}
	}
	// And the reduction: keepHighest (what the check calls the catalogue) and
	// Index.Add (what the derivation writes) keep the same version per key.
	rows := [][3]string{{"22.261", "Rel-19", "19.9.0"}, {"22.261", "Rel-19", "19.14.0"}, {"23.501", "Rel-18", "18.5.1"}, {"23.501", "Rel-18", "18.5.0"}}
	ix, kh := anchor.Index{}, map[string]string{}
	for _, r := range rows {
		ix.Add(r[0], r[1], r[2])
		keepHighest(kh, r[0]+"|"+r[1], r[2])
	}
	for k, v := range kh {
		if ix[k] != v {
			t.Errorf("%s: derived %q, the check's catalogue %q", k, ix[k], v)
		}
	}
}
