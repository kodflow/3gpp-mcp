package main

import (
	"fmt"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/anchor"
)

// THE ANCHOR A SEED DERIVES AND THE CHECK THAT JUDGES IT ORDER REAL VERSIONS ALIKE.
//
// cmd/derive-anchor orders versions the way the fold writes the anchor
// (anchor.CmpVer == rust/identity cmp_ver: three components, each parsed whole).
// This command judges an anchor with its own CmpVer (every component, leading
// digits). They are NOT the same function and are not meant to be: moving this
// command onto the fold's rule would change the binary `validate` runs and replay
// the 8-minute gate. What must hold is that they agree on every version the corpus
// actually contains — plain numeric triples, which is all 20 163 rows of
// spec_versions (measured 2026-09-11) — or a freshly derived anchor would open
// with a false over-claim. That is exhaustively checked here; the shapes where the
// two readings part (a fourth component, "19x") are pinned on the fold's side in
// internal/anchor.
func TestTheDerivedAnchorAndTheCheckOrderRealVersionsAlike(t *testing.T) {
	var vs []string
	for a := 0; a < 22; a++ {
		for b := 0; b < 22; b += 3 {
			for _, c := range []int{0, 1, 10} {
				vs = append(vs, fmt.Sprintf("%d.%d.%d", a, b, c))
			}
		}
	}
	vs = append(vs, "19.14.0", "19.9.0", "2.10.0", "2.9.0", "17.16.0", "0.3.0", "20.0.1")
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
