package etsicat

import "testing"

// TestThreeGPPRepublication pins the ETSI<->3GPP numbering map. Each expected-true
// case is a spec this corpus already holds on the 3GPP side across many releases,
// which is the whole reason for skipping it.
func TestThreeGPPRepublication(t *testing.T) {
	republished := []string{
		"123 501",   // 3GPP TS 23.501, in this corpus Rel-15..Rel-20
		"126 978",   // 3GPP TR 26.978, in this corpus across 15 releases
		"133 128",   // 3GPP TS 33.128
		"129 518",   // 3GPP TS 29.518
		"121 905",   // 3GPP TR 21.905 (the vocabulary)
		"138 331",   // 3GPP TS 38.331, top of the 21-38 band
		"155 216",   // 3GPP TS 55.216, in the 41-55 band
		"136 213-1", // a part suffix must not change the verdict
	}
	for _, id := range republished {
		if !ThreeGPPRepublication(id) {
			t.Errorf("ThreeGPPRepublication(%q) = false, want true", id)
		}
	}

	own := []string{
		"103 221-1", // the LI X1 base — ETSI's own, and the reason this corpus exists
		"103 280",
		"102 232-1",
		"101 331",
		"301 893", // an EN
		"300 328",
		"182 007",  // TISPAN, above the 3GPP bands
		"139 999",  // just above the 21-38 band
		"120 999",  // just below it
		"140 999",  // just below the 41-55 band
		"156 000",  // just above it
	}
	for _, id := range own {
		if ThreeGPPRepublication(id) {
			t.Errorf("ThreeGPPRepublication(%q) = true, want false", id)
		}
	}

	// Not an archive id at all.
	for _, id := range []string{"", "abc", "23.501"} {
		if ThreeGPPRepublication(id) {
			t.Errorf("ThreeGPPRepublication(%q) = true for a non-archive id", id)
		}
	}
}
