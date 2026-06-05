package ingest

import "testing"

// TestClassifyFileMultipartAnd6Digit locks the job-list filename parser (reFile /
// classifyFile) against the MULTI-PART + 6-digit forms — it MUST mirror
// htmlparse.reFileName, else files parse in htmlparse but are dropped from the job
// list here (the multi-part-0-keys bug: 36.521-1 etc. never ingested).
func TestClassifyFileMultipartAnd6Digit(t *testing.T) {
	cases := []struct {
		base, spec string
		ok         bool
	}{
		{"23501-h60", "23.501", true},                     // normal 5-digit
		{"36521-1-i00", "36.521-1", true},                 // multi-part spec id
		{"36521-1-i00_s00-s05", "36.521-1", true},         // multi-part + _section
		{"34123-1-a70_sAnnexA-sAnnexE", "34.123-1", true}, // multi-part + section
		{"24229-083700", "24.229", true},                  // 6-digit high-minor
		{"30531-016200", "30.531", true},                  // 6-digit
		{"0408-720", "04.08", true},                       // legacy 4-digit GSM
		{"36213-j30_s10-s13", "36.213", true},             // single-part + _section
		{"media-image1", "", false},                       // junk inner doc
	}
	for _, c := range cases {
		spec, _, _, _, ok := classifyFile(c.base)
		if ok != c.ok || (ok && spec != c.spec) {
			t.Errorf("classifyFile(%q) = spec=%q ok=%v; want spec=%q ok=%v", c.base, spec, ok, c.spec, c.ok)
		}
	}
}
