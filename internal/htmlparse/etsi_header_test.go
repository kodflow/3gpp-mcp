package htmlparse

import (
	"strings"
	"testing"
)

// TestParseETSIHeader: an ETSI provenance comment drives the id/version/url (the
// 3GPP filename derivation is bypassed), and clauses still parse via the same walker.
func TestParseETSIHeader(t *testing.T) {
	doc := `<!-- ETSI-SPEC: 103 221-1 | 1.21.1 -->
<html><body>
<h1>1	Scope</h1>
<p>The present document specifies the X1 interface.</p>
<h1>6	X1 task object</h1>
<p>The ADMF provisions tasks over X1.</p>
</body></html>`
	// A deliberately non-3GPP path: the ETSI header must win regardless.
	ps, err := Parse("/tmp/whatever.html", strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	if ps.Spec.SpecID != "ETSI TS 103 221-1" {
		t.Errorf("spec_id=%q, want ETSI TS 103 221-1", ps.Spec.SpecID)
	}
	if ps.Version.Version != "1.21.1" || ps.Version.Release != "ETSI" {
		t.Errorf("version=%q release=%q, want 1.21.1/ETSI", ps.Version.Version, ps.Version.Release)
	}
	want := "https://www.etsi.org/deliver/etsi_ts/103200_103299/10322101/01.21.01_60/ts_10322101v012101p.pdf"
	if ps.Version.DocxURL != want {
		t.Errorf("url=%q, want %q", ps.Version.DocxURL, want)
	}
	if len(ps.Clauses) == 0 {
		t.Fatal("no clauses parsed from ETSI HTML")
	}
}
