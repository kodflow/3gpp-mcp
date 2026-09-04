package model

import "strings"
import "testing"

// TestEtsiSpecURLMatchesTheCrawledURL pins the reconstruction against URLs the
// crawler actually fetched — the value the corpus stores in spec_versions.docx_url
// for that exact (spec_id, version). A reconstruction that merely "looks right"
// would send a reader to a 404 while the citation claimed to be stable.
//
// These four cover the three document trees and a multi-part id, because the tree
// selects BOTH the folder and the file-name prefix: /deliver/etsi_ts/.../103101/
// is a 404 while /deliver/etsi_tr/.../103101/ is TR 103 101.
func TestEtsiSpecURLMatchesTheCrawledURL(t *testing.T) {
	for _, tc := range []struct{ spec, version, want string }{
		{"ETSI TS 102 221", "18.4.0",
			"https://www.etsi.org/deliver/etsi_ts/102200_102299/102221/18.04.00_60/ts_102221v180400p.pdf"},
		{"ETSI TR 103 101", "1.1.1",
			"https://www.etsi.org/deliver/etsi_tr/103100_103199/103101/01.01.01_60/tr_103101v010101p.pdf"},
		{"ETSI EN 301 893", "2.2.1",
			"https://www.etsi.org/deliver/etsi_en/301800_301899/301893/02.02.01_60/en_301893v020201p.pdf"},
		{"ETSI TS 103 221-1", "1.21.1",
			"https://www.etsi.org/deliver/etsi_ts/103200_103299/10322101/01.21.01_60/ts_10322101v012101p.pdf"},
	} {
		if got := EtsiSpecURL(tc.spec, tc.version); got != tc.want {
			t.Errorf("EtsiSpecURL(%q,%q) = %q, want %q", tc.spec, tc.version, got, tc.want)
		}
	}
}

// TestEtsiSpecURLLeavesThe3GPPHalfAlone — SpecURL serves both halves from one
// process, so the ETSI branch must not claim a 3GPP spec. It also must reach an
// EN, which the "1NN"-anchored citation recogniser refuses on purpose.
func TestEtsiSpecURLLeavesThe3GPPHalfAlone(t *testing.T) {
	if got := EtsiSpecURL("23.501", "18.5.0"); got != "" {
		t.Errorf("EtsiSpecURL on a 3GPP id = %q, want empty", got)
	}
	if got, want := SpecURL("33.128", "19.6.0"), ArchiveURL("33.128", "19.6.0"); got != want {
		t.Errorf("SpecURL(3GPP) = %q, want the 3GPP archive URL %q", got, want)
	}
	if got := SpecURL("ETSI EN 301 893", "2.2.1"); !strings.Contains(got, "en_301893") {
		t.Errorf("SpecURL must reach an EN through EtsiSpecURL, got %q", got)
	}
}

// TestEtsiSpecURLCitesTheFolderWhenTheVersionIsUnusable — cite the pointer rather
// than fabricate a version. A citation naming a version the archive does not hold
// is worse than one that names the deliverable and stops.
func TestEtsiSpecURLCitesTheFolderWhenTheVersionIsUnusable(t *testing.T) {
	want := "https://www.etsi.org/deliver/etsi_ts/102200_102299/102221/"
	for _, v := range []string{"", "not-a-version"} {
		if got := EtsiSpecURL("ETSI TS 102 221", v); got != want {
			t.Errorf("EtsiSpecURL(version=%q) = %q, want the folder %q", v, got, want)
		}
	}
}

// TestClauseCiteCarriesAnEtsiURL is the defect this change fixes, at the level the
// server actually uses: every citation the ETSI half produced had a blank pointer.
func TestClauseCiteCarriesAnEtsiURL(t *testing.T) {
	c := Clause{SpecID: "ETSI TS 102 221", Release: "ETSI", Version: "18.4.0", ClausePath: "11.1.5.1"}
	if got := c.Cite().URL; !strings.HasSuffix(got, "ts_102221v180400p.pdf") {
		t.Errorf("Clause.Cite().URL = %q, want the ETSI deliver PDF", got)
	}
}
