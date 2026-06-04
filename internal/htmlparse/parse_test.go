package htmlparse

import (
	"strings"
	"testing"
)

// a minimal LibreOffice-shaped 3GPP HTML (headings "num<TAB>title", a change
// history table, a degraded marker).
const sampleHTML = "<!-- 3GPP-MCP-DEGRADED: emf-wmf-stripped; 2 figure(s) omitted -->" +
	"<html><head><title>x</title></head><body>" +
	"<h1>Foreword</h1><p>This clause is informative.</p>" +
	"<h1>1\tScope</h1><p>The present document specifies lawful interception.</p>" +
	"<h2>6.2.2.2\tGeneration of xIRI over LI_X2</h2><p>intro</p>" +
	"<h3>6.2.2.2.1\tGeneral</h3><p>general</p>" +
	"<h3>6.2.2.2.2\tRegistration</h3><p>registration event text</p>" +
	"<h3>6.2.2.2.3\tHandovers</h3><p>handover event text</p>" +
	"<h1>Change history</h1>" +
	"<table><tr><th>Date</th><th>Meeting</th><th>CR</th><th>Cat</th><th>New version</th><th>Subject</th></tr>" +
	"<tr><td>2024-06-01</td><td>SA3#100</td><td>0123</td><td>F</td><td>19.6.0</td><td>Fix X2 handling</td></tr></table>" +
	"</body></html>"

// TestMetaReleaseFromDirAndMultipart locks the ingest-attribution fix: the release
// comes from the …/convert/<Rel>/ directory (the worklist), the version from the
// code, and multi-part / 6-digit filenames parse. Before the fix a draft v1.x.x of
// a Rel-20 spec was filed as "Rel-1" and multi-part / high-minor specs were dropped.
func TestMetaReleaseFromDirAndMultipart(t *testing.T) {
	const body = "<html><body><h1>1\tScope</h1><p>text body of the clause.</p></body></html>"
	cases := []struct {
		path, spec, rel, ver string
	}{
		{"/x/convert/Rel-20/21802-111.html", "21.802", "Rel-20", "1.1.1"},      // draft major≠release
		{"/x/convert/Rel-19/21919-100.html", "21.919", "Rel-19", "1.0.0"},      // draft of a current release
		{"/x/convert/Rel-18/36521-1-i00.html", "36.521-1", "Rel-18", "18.0.0"}, // multi-part
		{"/x/convert/Rel-8/24229-083700.html", "24.229", "Rel-8", "8.37.0"},    // 6-digit high-minor
		{"/x/convert/Rel-99/23501-300.html", "23.501", "Rel-99", "3.0.0"},      // major 3 = Rel-99
	}
	for _, c := range cases {
		ps, err := Parse(c.path, strings.NewReader(body))
		if err != nil {
			t.Fatalf("%s: %v", c.path, err)
		}
		if ps.Spec.SpecID != c.spec || ps.Version.Release != c.rel || ps.Version.Version != c.ver {
			t.Errorf("%s → spec=%q rel=%q ver=%q; want %q/%q/%q",
				c.path, ps.Spec.SpecID, ps.Version.Release, ps.Version.Version, c.spec, c.rel, c.ver)
		}
	}
}

func TestParse(t *testing.T) {
	ps, err := Parse("/x/convert/Rel-19/33128-j60.html", strings.NewReader(sampleHTML))
	if err != nil {
		t.Fatal(err)
	}

	// metadata from filename
	if ps.Spec.SpecID != "33.128" || ps.Spec.Series != "33" || ps.Spec.WorkingGroup != "SA3" {
		t.Errorf("spec meta wrong: %+v", ps.Spec)
	}
	if ps.Version.Release != "Rel-19" || ps.Version.Version != "19.6.0" {
		t.Errorf("version wrong: %+v", ps.Version)
	}
	if !strings.Contains(ps.Version.DocxURL, "33128-j60.zip") {
		t.Errorf("docx url wrong: %q", ps.Version.DocxURL)
	}
	if !ps.Degraded {
		t.Error("degraded marker not detected")
	}

	// clause-aware chunking with dotted paths
	byPath := map[string]string{}
	for _, c := range ps.Clauses {
		byPath[c.ClausePath] = c.Heading
	}
	for _, p := range []string{"1", "6.2.2.2", "6.2.2.2.2", "6.2.2.2.3"} {
		if _, ok := byPath[p]; !ok {
			t.Errorf("missing clause %s; got paths %v", p, keysOf(byPath))
		}
	}
	if byPath["6.2.2.2.2"] != "Registration" {
		t.Errorf("clause 6.2.2.2.2 heading = %q, want Registration", byPath["6.2.2.2.2"])
	}

	// change history parsed
	if len(ps.Changes) != 1 {
		t.Fatalf("want 1 change, got %d", len(ps.Changes))
	}
	ch := ps.Changes[0]
	if ch.CRNumber != "0123" || ch.Category != "F" || ch.ToVersion != "19.6.0" || ch.Summary != "Fix X2 handling" {
		t.Errorf("change parsed wrong: %+v", ch)
	}
	// freeze_date derived from the change date
	if ps.Version.FreezeDate == nil || ps.Version.FreezeDate.Year() != 2024 {
		t.Errorf("freeze date not derived: %v", ps.Version.FreezeDate)
	}
}

func TestParseBadFilename(t *testing.T) {
	if _, err := Parse("/x/notaspec.html", strings.NewReader("<html></html>")); err == nil {
		t.Error("expected error on non-spec filename")
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
