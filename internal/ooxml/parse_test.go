package ooxml

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildDocx writes a minimal .docx (zip with just word/document.xml — all
// ParseFile needs) wrapping body in the right namespace, named so
// metaFromFilename resolves spec/version.
func buildDocx(t *testing.T, body string) string {
	t.Helper()
	doc := `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body>` + body + `</w:body></w:document>`
	dir := t.TempDir()
	// 33.128 Rel-18 code "if0" -> exercises metaFromFilename too.
	path := filepath.Join(dir, "33128-if0.docx")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(doc)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func heading(level, num, title string) string {
	return `<w:p><w:pPr><w:pStyle w:val="Heading` + level + `"/></w:pPr>` +
		`<w:r><w:t>` + num + `</w:t></w:r><w:tab/><w:r><w:t>` + title + `</w:t></w:r></w:p>`
}

func para(text string) string {
	return `<w:p><w:r><w:t>` + text + `</w:t></w:r></w:p>`
}

func TestParseHeadingsAndStyles(t *testing.T) {
	body := heading("1", "5", "Functional description") +
		heading("2", "5.1", "General") +
		para("Some normative body text.") +
		// PL block: preserved verbatim (whitespace kept).
		`<w:p><w:pPr><w:pStyle w:val="PL"/></w:pPr><w:r><w:t>PATCH /nf-instances/{id}</w:t></w:r></w:p>`

	ps, err := ParseFile(buildDocx(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if ps.Spec.SpecID != "33.128" || ps.Version.Release != "Rel-18" {
		t.Errorf("meta = %+v / %+v", ps.Spec, ps.Version)
	}
	byPath := map[string]string{}
	for _, c := range ps.Clauses {
		byPath[c.ClausePath] = c.Heading
	}
	if byPath["5"] != "Functional description" {
		t.Errorf("clause 5 heading = %q", byPath["5"])
	}
	if byPath["5.1"] != "General" {
		t.Errorf("clause 5.1 heading = %q", byPath["5.1"])
	}
	// PL block text lands in clause 5.1's buffer, verbatim.
	var txt51 string
	for _, c := range ps.Clauses {
		if c.ClausePath == "5.1" {
			txt51 = c.Text
		}
	}
	if !strings.Contains(txt51, "PATCH /nf-instances/{id}") {
		t.Errorf("PL block not preserved in 5.1: %q", txt51)
	}
}

func TestTableGridSpanVMerge(t *testing.T) {
	// 3 logical columns.
	tbl := `<w:tbl>
<w:tblGrid><w:gridCol w:w="100"/><w:gridCol w:w="100"/><w:gridCol w:w="100"/></w:tblGrid>
<w:tr>
  <w:tc><w:tcPr><w:gridSpan w:val="3"/></w:tcPr><w:p><w:r><w:t>Spanned Header</w:t></w:r></w:p></w:tc>
</w:tr>
<w:tr>
  <w:tc><w:tcPr><w:vMerge w:val="restart"/></w:tcPr><w:p><w:r><w:t>Label</w:t></w:r></w:p></w:tc>
  <w:tc><w:p><w:r><w:t>B1</w:t></w:r></w:p></w:tc>
  <w:tc><w:p><w:r><w:t>C1</w:t></w:r></w:p></w:tc>
</w:tr>
<w:tr>
  <w:tc><w:tcPr><w:vMerge/></w:tcPr><w:p/></w:tc>
  <w:tc><w:p><w:r><w:t>B2</w:t></w:r></w:p></w:tc>
  <w:tc><w:p><w:r><w:t>C2</w:t></w:r></w:p></w:tc>
</w:tr>
</w:tbl>`
	body := heading("1", "7", "Tables") + tbl
	ps, err := ParseFile(buildDocx(t, body))
	if err != nil {
		t.Fatal(err)
	}
	var txt string
	for _, c := range ps.Clauses {
		if c.ClausePath == "7" {
			txt = c.Text
		}
	}
	// gridSpan: the header fills all 3 columns.
	if !strings.Contains(txt, "Spanned Header\tSpanned Header\tSpanned Header") {
		t.Errorf("gridSpan not expanded:\n%s", txt)
	}
	// vMerge: the continuation row inherits "Label" from the restart cell above.
	if !strings.Contains(txt, "Label\tB2\tC2") {
		t.Errorf("vMerge not filled down:\n%s", txt)
	}
}

func TestChangeHistory(t *testing.T) {
	// Title row merged across 8 cols (gridSpan), then the real header, then data.
	tbl := `<w:tbl>
<w:tblGrid>` + strings.Repeat(`<w:gridCol w:w="50"/>`, 8) + `</w:tblGrid>
<w:tr><w:tc><w:tcPr><w:gridSpan w:val="8"/></w:tcPr><w:p><w:r><w:t>Change history</w:t></w:r></w:p></w:tc></w:tr>
<w:tr>` +
		cell("Date") + cell("Meeting") + cell("TDoc") + cell("CR") +
		cell("Rev") + cell("Cat") + cell("Subject/Comment") + cell("New version") + `</w:tr>
<w:tr>` +
		cell("2024-06-21") + cell("SA#104") + cell("SP-240001") + cell("0042") +
		cell("2") + cell("F") + cell("Fix the thing") + cell("18.5.0") + `</w:tr>
</w:tbl>`
	// "Annex Z (informative):" <br/> "Change history" — the real-world heading shape.
	chHeading := `<w:p><w:pPr><w:pStyle w:val="Heading8"/></w:pPr>` +
		`<w:r><w:t>Annex Z (informative):</w:t></w:r><w:br/><w:r><w:t>Change history</w:t></w:r></w:p>`
	body := heading("1", "5", "Body") + para("text") + chHeading + tbl

	ps, err := ParseFile(buildDocx(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if len(ps.Changes) != 1 {
		t.Fatalf("changes = %d, want 1: %+v", len(ps.Changes), ps.Changes)
	}
	c := ps.Changes[0]
	if c.CRNumber != "0042" || c.CRRevision != 2 || c.Meeting != "SA#104" ||
		c.Category != "F" || c.ToVersion != "18.5.0" || c.Summary != "Fix the thing" {
		t.Errorf("change row mis-mapped: %+v", c)
	}
	// freeze-date proxy = latest date in the annex.
	if ps.Version.FreezeDate == nil || ps.Version.FreezeDate.Format("2006-01-02") != "2024-06-21" {
		t.Errorf("freeze proxy = %v, want 2024-06-21", ps.Version.FreezeDate)
	}
}

func cell(text string) string {
	return `<w:tc><w:p><w:r><w:t>` + text + `</w:t></w:r></w:p></w:tc>`
}
