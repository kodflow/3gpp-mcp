package goal

import (
	"slices"
	"testing"
)

// TestValidateDeclaresEveryPackageItsBinariesLink holds both validate arms to the
// package graphs of the two commands the step runs, cmd/validate and
// cmd/anchorcheck, as build-go builds them.
//
// THE DEFECT THIS PINS, found by review on 2026-09-11. validate declared the two
// commands and nothing they import, while its verdict is computed in what they
// import: the embed floor through model.ReleaseOrdinal, the sparse stamp against
// embed.SparseModelID(), the re-ingest check in store.SummariseReingested. build-go
// relinks both binaries on an edit there and is a Tool dep, which invalidates no
// consumer, so validate SKIPped with the new check never run while smoke and
// publish, which name the same packages, replayed and recorded the image as having
// passed it. go.mod and go.sum reached the gate the same way.
//
// Both arms share one Impl, so both carry cmd/anchorcheck although only the 3GPP
// arm runs it (the anchor is a 3GPP artefact); the graphs judged are the same for
// both, or the backward check would call the ETSI arm's anchorcheck stale.
func TestValidateDeclaresEveryPackageItsBinariesLink(t *testing.T) {
	requireGo(t)
	builds := buildGoSpecs(t, "validate", "anchorcheck")
	for _, target := range []corpusTarget{corpus3GPP(), corpusETSI()} {
		s := stepValidate(target)
		checkDeclaresWhatItsBinariesLink(t, s.Name, s.Impl, builds,
			"a change there relinks the binary through build-go, a Tool dep, and the gate SKIPs with the "+
				"check it changed never run, while smoke and publish replay over it")
		for _, f := range []string{"go.mod", "go.sum"} {
			if !slices.Contains(s.Impl, f) {
				t.Errorf("%s does not declare %s: a dependency bump relinks both binaries through build-go "+
					"and the gate keeps the verdict the old ones gave", s.Name, f)
			}
		}
		if !s.ExcludeTests {
			t.Errorf("%s names Go packages as directories and counts their _test.go: internal/store alone "+
				"holds 45, and each test-only commit would replay the gate, smoke and the image", s.Name)
		}
	}
}
