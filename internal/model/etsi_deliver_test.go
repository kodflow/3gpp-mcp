package model

import "testing"

// TestEtsiDeliverURLInCoversEveryDocumentType pins the URLs against the live
// archive layout, verified by hand on www.etsi.org before this test was written.
//
// The regression it guards is not cosmetic. The single hardcoded "etsi_ts" folder
// meant a TR resolved to a path that 404s (checked: /deliver/etsi_ts/103100_103199/
// 103101/ is 404, /deliver/etsi_tr/… is TR 103 101), and the file prefix "ts_" was
// wrong for every non-TS deliverable — so widening the crawl to etsi_tr + etsi_en
// silently produced a work list of dead links.
func TestEtsiDeliverURLInCoversEveryDocumentType(t *testing.T) {
	cases := []struct {
		name, typeDir, id, version, want string
	}{
		{
			"TS with a part number",
			EtsiTypeTS, "103 221-1", "1.21.1",
			"https://www.etsi.org/deliver/etsi_ts/103200_103299/10322101/01.21.01_60/ts_10322101v012101p.pdf",
		},
		{
			"TS without a part number",
			EtsiTypeTS, "103 280", "2.19.1",
			"https://www.etsi.org/deliver/etsi_ts/103200_103299/103280/02.19.01_60/ts_103280v021901p.pdf",
		},
		{
			"TR lands in the TR tree with the tr_ prefix",
			EtsiTypeTR, "103 101", "1.1.1",
			"https://www.etsi.org/deliver/etsi_tr/103100_103199/103101/01.01.01_60/tr_103101v010101p.pdf",
		},
		{
			"EN is numbered 3NN NNN and lands in the EN tree",
			EtsiTypeEN, "301 893", "2.2.1",
			"https://www.etsi.org/deliver/etsi_en/301800_301899/301893/02.02.01_60/en_301893v020201p.pdf",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EtsiDeliverURLIn(tc.typeDir, tc.id, tc.version); got != tc.want {
				t.Errorf("EtsiDeliverURLIn(%q,%q,%q)\n got %q\nwant %q", tc.typeDir, tc.id, tc.version, got, tc.want)
			}
		})
	}
}

// TestEtsiDeliverURLDefaultsToTS — the citation path (internal/mcp) calls the
// two-argument form for ids mined out of 3GPP clause text, which are always TS.
func TestEtsiDeliverURLDefaultsToTS(t *testing.T) {
	if got, want := EtsiDeliverURL("103 280", "2.19.1"), EtsiDeliverURLIn(EtsiTypeTS, "103 280", "2.19.1"); got != want {
		t.Errorf("EtsiDeliverURL = %q, want the etsi_ts form %q", got, want)
	}
}

// TestEtsiDeliverURLInVersionlessCitesTheFolder — cite-the-pointer: with no usable
// version the exact file cannot be named, so the deliverable's folder is returned
// rather than a fabricated version. ResolveLatest relies on this to find the folder
// it must list.
func TestEtsiDeliverURLInVersionlessCitesTheFolder(t *testing.T) {
	want := "https://www.etsi.org/deliver/etsi_tr/103100_103199/103101/"
	if got := EtsiDeliverURLIn(EtsiTypeTR, "103 101", ""); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := EtsiDeliverURLIn(EtsiTypeTR, "103 101", "not-a-version"); got != want {
		t.Errorf("unparseable version: got %q, want the folder %q", got, want)
	}
}

// TestEtsiDeliverURLInRejectsAnUnknownTree — an unrecognised document-type folder
// must return "", not a plausible URL into a tree that does not exist.
func TestEtsiDeliverURLInRejectsAnUnknownTree(t *testing.T) {
	for _, bad := range []string{"", "etsi_es", "etsi_ts/", "ts"} {
		if got := EtsiDeliverURLIn(bad, "103 280", "2.19.1"); got != "" {
			t.Errorf("EtsiDeliverURLIn(%q, …) = %q, want \"\"", bad, got)
		}
	}
}

// TestEtsiArchiveTokenIsTheInverseOfTokenToID — the archive parser must accept
// every id shape the crawl's TokenToID can produce, including the 3NN prefix that
// the RECOGNISER (reEtsiID) deliberately rejects and multi-part ids.
func TestEtsiArchiveTokenIsTheInverseOfTokenToID(t *testing.T) {
	cases := []struct{ id, token, base6 string }{
		{"103 280", "103280", "103280"},
		{"103 221-1", "10322101", "103221"},
		{"103 192-6", "10319206", "103192"},
		{"301 893", "301893", "301893"},   // EN: reEtsiID rejects this, on purpose
		{"300 328", "300328", "300328"},   // EN
		{"102 232-3-1", "1022320301", "102232"}, // chained parts
	}
	for _, tc := range cases {
		token, base6, ok := etsiArchiveToken(tc.id)
		if !ok || token != tc.token || base6 != tc.base6 {
			t.Errorf("etsiArchiveToken(%q) = (%q,%q,%v), want (%q,%q,true)", tc.id, token, base6, ok, tc.token, tc.base6)
		}
	}
	for _, bad := range []string{"23.501", "", "abc", "10 20"} {
		if _, _, ok := etsiArchiveToken(bad); ok {
			t.Errorf("etsiArchiveToken(%q) accepted a non-archive id", bad)
		}
	}
}

// TestRecogniserStaysStrict — widening the ARCHIVE parser must not widen the
// citation RECOGNISER: "301 893" appearing in 3GPP prose is not an ETSI citation.
func TestRecogniserStaysStrict(t *testing.T) {
	if _, ok := NormalizeEtsiID("301 893"); ok {
		t.Error("NormalizeEtsiID must not recognise a 3NN id out of running prose")
	}
	if _, ok := NormalizeEtsiID("103 221-1"); !ok {
		t.Error("NormalizeEtsiID must still recognise a 1NN id")
	}
}
