package ingest

import (
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// TestClassifyFileLegacyGSM pins the §0-approved labelling: modern 5-digit specs
// decode to a real release; legacy 4-digit GSM specs get the honest "GSM" bucket
// (never a guessed release line) while keeping an exact spec_id + literal version.
func TestClassifyFileLegacyGSM(t *testing.T) {
	cases := []struct {
		base                       string
		wantSpec, wantRel, wantVer string
		wantOK                     bool
	}{
		{"23501-i60", "23.501", "Rel-18", "18.6.0", true},      // modern TS 23.501 v18.6.0
		{"24008-k00", "24.008", "Rel-20", "20.0.0", true},      // modern, newest
		{"0408-720", "04.08", model.ReleaseGSM, "7.2.0", true}, // legacy GSM 04.08 → bucket
		{"0512-800", "05.12", model.ReleaseGSM, "8.0.0", true}, // legacy GSM 05.12 → bucket
		{"23501-xx", "", "", "", false},                        // bad code
		{"notaspec", "", "", "", false},                        // no match
	}
	for _, c := range cases {
		specID, _, rel, ver, ok := classifyFile(c.base)
		if ok != c.wantOK {
			t.Errorf("%s: ok=%v want %v", c.base, ok, c.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if specID != c.wantSpec || rel != c.wantRel || ver != c.wantVer {
			t.Errorf("%s: got (%s, %s, %s) want (%s, %s, %s)",
				c.base, specID, rel, ver, c.wantSpec, c.wantRel, c.wantVer)
		}
	}
}

// TestGSMBelowEmbedFloor proves the safety property that makes "lexical-only Phase
// 1/2" automatic: ReleaseOrdinal("GSM") is false, so a GSM clause is below ANY embed
// floor and is never selected for vectorisation — it is indexed lexically only.
func TestGSMBelowEmbedFloor(t *testing.T) {
	if _, ok := model.ReleaseOrdinal(model.ReleaseGSM); ok {
		t.Fatalf("ReleaseOrdinal(%q) must be (_,false) so GSM stays below every embed floor", model.ReleaseGSM)
	}
	if !model.IsLegacyGSM("0408") || model.IsLegacyGSM("23501") {
		t.Errorf("IsLegacyGSM: 0408 should be legacy, 23501 should not")
	}
}
