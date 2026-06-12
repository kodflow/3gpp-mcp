// Package ooxml parses a 3GPP spec .docx directly from word/document.xml
// (archive/zip + encoding/xml — the stack CLAUDE.md §2 prescribed), producing
// the same ParsedSpec shape as internal/htmlparse so internal/ingest is a
// drop-in swap. Unlike the LibreOffice→HTML round-trip it reconstructs merged
// table geometry (w:gridSpan horizontal, w:vMerge vertical), keeps full
// Heading1..9 depth from w:pStyle, and preserves pStyle="PL" ASN.1/protocol
// blocks verbatim (axis #4).
package ooxml

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// ParsedSpec mirrors htmlparse.ParsedSpec exactly (drop-in for ingest).
type ParsedSpec struct {
	Spec     model.Spec
	Version  model.SpecVersion
	Clauses  []model.Clause
	Changes  []model.Change
	Degraded bool
	// SawChangeHistory mirrors htmlparse: a matched "Change History" heading,
	// regardless of captured row count (true + 0 changes == parse miss).
	SawChangeHistory bool
}

// Shared with htmlparse's grammar (kept local so ooxml has no HTML dependency;
// htmlparse is removed in the switch-over commit, resolving the duplication).
var (
	reNumeric  = regexp.MustCompile(`^([0-9]+(?:\.[0-9A-Za-z]+)*)\s+(.+)$`)
	reAnnexSub = regexp.MustCompile(`^([A-Z]\.[0-9]+(?:\.[0-9A-Za-z]+)*)\s+(.+)$`)
	reAnnex    = regexp.MustCompile(`(?i)^Annex\s+([A-Z][0-9]*)\s*[:.)]?\s*(.*)$`)
	// 4 or 5 digits: legacy GSM is 4 (0388 = GSM 03.88), modern is 5 (23501 = TS
	// 23.501). 5-digit names still match {4,5} greedily, so modern parsing is
	// unchanged — admits the previously-rejected 4-digit GSM (#129). Twin of the
	// htmlparse regex; kept in lockstep.
	reFileName  = regexp.MustCompile(`^([0-9]{4,5})-([0-9a-z]{3})(?:_.*)?$`)
	reDate      = regexp.MustCompile(`\b(\d{4})-(\d{2})-(\d{2})\b|\b(\d{2})/(\d{4})\b`)
	titleBoiler = regexp.MustCompile(`(?i)copyright|reproduc|notification|all rights|organizational partners|foreword|^3gpp t[sr]\b|^contents$|^scope$|^introduction$|^general$|trademark`)
)

// headingStyles maps the w:pStyle values 3GPP uses for clause headings to a
// depth. Heading1..9 are standard; "H6"/"TH" are 3GPP custom deep-outline styles.
func headingDepth(style string) int {
	switch {
	case strings.HasPrefix(style, "Heading"):
		if n, err := strconv.Atoi(strings.TrimPrefix(style, "Heading")); err == nil {
			return n
		}
		return 1
	case style == "H6":
		return 6
	}
	return 0
}

// ParseFile parses the .docx at path (…/<Rel>/<num>-<code>.docx).
func ParseFile(path string) (*ParsedSpec, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = zr.Close() }()

	var doc *zip.File
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			doc = f
			break
		}
	}
	if doc == nil {
		return nil, fmt.Errorf("%s: no word/document.xml (not a .docx)", filepath.Base(path))
	}
	rc, err := doc.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return Parse(path, rc)
}

// Parse reads document.xml from r; path is used only to derive spec id/version.
func Parse(path string, r io.Reader) (*ParsedSpec, error) {
	ps := &ParsedSpec{}
	if err := ps.metaFromFilename(path); err != nil {
		return nil, err
	}
	w := &builder{ps: ps}
	if err := w.run(r); err != nil {
		return nil, fmt.Errorf("parse docx %s: %w", filepath.Base(path), err)
	}
	w.flush()
	w.finishChangeHistory()
	ps.deriveTitleAndType(w)
	return ps, nil
}

func (ps *ParsedSpec) metaFromFilename(path string) error {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	m := reFileName.FindStringSubmatch(base)
	if m == nil {
		return fmt.Errorf("filename %q is not <4-5 digits>-<3code>", base)
	}
	num, code := m[1], m[2]
	specID := num[:2] + "." + num[2:]
	release, version, ok := model.DecodeVersionCode(code)
	if !ok {
		return fmt.Errorf("bad version code %q in %q", code, base)
	}
	series := model.SeriesOf(specID)
	ps.Spec = model.Spec{SpecID: specID, Series: series, DocType: "TS",
		WorkingGroup: model.WorkingGroupForSeries(series)}
	ps.Version = model.SpecVersion{SpecID: specID, Release: release, Version: version,
		DocxURL: model.ArchiveURL(specID, version)}
	return nil
}

// builder accumulates clauses while streaming document.xml in document order —
// the same flush model as the htmlparse walker.
type builder struct {
	ps               *ParsedSpec
	cur              *model.Clause
	buf              strings.Builder
	id               uint64
	headings         []string
	informativeAnnex bool
	inChangeHistory  bool
	chTables         [][][]string
}

// run streams body-level children: paragraphs (manual, tab-aware) and tables
// (decoded as a subtree, then grid-reconstructed).
func (w *builder) run(r io.Reader) error {
	dec := xml.NewDecoder(r)
	inBody := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "body":
			inBody = true
		case "p":
			if !inBody {
				_ = dec.Skip()
				continue
			}
			style, text := readParagraph(dec)
			w.onParagraph(style, text)
		case "tbl":
			if !inBody {
				_ = dec.Skip()
				continue
			}
			var t wTbl
			if dec.DecodeElement(&t, &se) == nil {
				w.onTable(t)
			}
		default:
			if inBody {
				_ = dec.Skip() // sectPr and other body-level metadata
			}
		}
	}
}

// onParagraph routes a paragraph by its style: heading (new clause), PL
// (verbatim ASN.1/protocol block) or body text.
func (w *builder) onParagraph(style, text string) {
	if d := headingDepth(style); d > 0 {
		w.onHeading(collapse(text))
		return
	}
	if style == "PL" {
		// Preserve the block verbatim (no whitespace collapse): protocol/ASN.1.
		if text != "" {
			w.buf.WriteString(text)
			w.buf.WriteString("\n")
		}
		return
	}
	if t := collapse(text); t != "" {
		w.buf.WriteString(t)
		w.buf.WriteString("\n")
	}
}

func (w *builder) onHeading(text string) {
	if text == "" {
		return
	}
	w.headings = append(w.headings, text)
	if isChangeHistory(text) {
		w.flush()
		w.inChangeHistory = true
		w.ps.SawChangeHistory = true
		w.cur = nil
		return
	}
	w.inChangeHistory = false

	path, heading, informative := classifyHeading(text)
	if reAnnex.MatchString(text) {
		w.informativeAnnex = informative
	} else if path != "" && !strings.ContainsRune(path, '.') {
		w.informativeAnnex = false
	}
	w.flush()
	w.id++
	w.cur = &model.Clause{
		ChunkID: w.id, SpecID: w.ps.Spec.SpecID, Release: w.ps.Version.Release,
		Version: w.ps.Version.Version, ClausePath: path, Heading: heading,
		IsNormative: !w.informativeAnnex,
	}
}

// onTable reconstructs the rectangular grid (gridSpan + vMerge) and appends it
// as TSV to the clause buffer; change-history tables are also kept structured.
func (w *builder) onTable(t wTbl) {
	rows := t.reconstruct()
	if w.inChangeHistory {
		w.chTables = append(w.chTables, rows)
	}
	for _, r := range rows {
		w.buf.WriteString(strings.Join(r, "\t"))
		w.buf.WriteString("\n")
	}
}

func (w *builder) flush() {
	if w.cur == nil {
		w.buf.Reset()
		return
	}
	w.cur.Text = strings.TrimSpace(w.buf.String())
	w.buf.Reset()
	if w.cur.ClausePath != "" || w.cur.Heading != "" || w.cur.Text != "" {
		w.ps.Clauses = append(w.ps.Clauses, *w.cur)
	}
	w.cur = nil
}

func classifyHeading(text string) (path, heading string, informative bool) {
	if m := reAnnex.FindStringSubmatch(text); m != nil {
		inf := strings.Contains(strings.ToLower(text), "informative")
		h := strings.TrimSpace(m[2])
		if h == "" {
			h = "Annex " + m[1]
		}
		return "Annex " + m[1], h, inf
	}
	if m := reAnnexSub.FindStringSubmatch(text); m != nil {
		return m[1], strings.TrimSpace(m[2]), false
	}
	if m := reNumeric.FindStringSubmatch(text); m != nil {
		return m[1], strings.TrimSpace(m[2]), false
	}
	return "", text, false
}

func (ps *ParsedSpec) deriveTitleAndType(w *builder) {
	head := strings.ToLower(strings.Join(w.headings, " ") + " " + ps.firstClausesText(3))
	if strings.Contains(head, "technical report") || strings.Contains(head, "tr "+ps.Spec.SpecID) {
		ps.Spec.DocType = "TR"
	} else {
		ps.Spec.DocType = "TS"
	}
	for _, h := range w.headings {
		if p, _, _ := classifyHeading(h); p != "" {
			continue
		}
		t := collapse(h)
		if len(t) < 6 || len(t) > 140 || titleBoiler.MatchString(t) {
			continue
		}
		ps.Spec.Title = t
		return
	}
}

func (ps *ParsedSpec) firstClausesText(n int) string {
	var sb strings.Builder
	for i, c := range ps.Clauses {
		if i >= n {
			break
		}
		sb.WriteString(c.Heading)
		sb.WriteString(" ")
		sb.WriteString(c.Text)
		sb.WriteString(" ")
	}
	return sb.String()
}

func (w *builder) finishChangeHistory() {
	var latest time.Time
	for _, rows := range w.chTables {
		w.ps.Changes = append(w.ps.Changes, parseChangeTable(rows, w.ps.Spec.SpecID)...)
		for _, r := range rows {
			for _, cell := range r {
				if d := parseDate(cell); !d.IsZero() && d.After(latest) {
					latest = d
				}
			}
		}
	}
	if !latest.IsZero() {
		w.ps.Version.FreezeDate = &latest
	}
}

func parseChangeTable(rows [][]string, specID string) []model.Change {
	if len(rows) < 2 {
		return nil
	}
	// 3GPP change-history tables open with a merged "Change history" title row
	// that gridSpan-expands to fill every column; the real header (Date/Meeting/
	// TDoc/CR/...) is the first row matching ≥2 known keywords. Skip to it.
	hdr := 0
	for i, r := range rows {
		if changeHeaderScore(r) >= 2 {
			hdr = i
			break
		}
	}
	rows = rows[hdr:]
	col := map[string]int{}
	for i, h := range rows[0] {
		h = strings.ToLower(collapse(h))
		switch {
		case strings.Contains(h, "date"):
			col["date"] = i + 1
		case strings.Contains(h, "meeting") || strings.Contains(h, "tsg"):
			col["meeting"] = i + 1
		case strings.Contains(h, "tdoc") || strings.Contains(h, "doc"):
			col["tdoc"] = i + 1
		case h == "cr":
			col["cr"] = i + 1
		case h == "rev":
			col["rev"] = i + 1
		case h == "cat":
			col["cat"] = i + 1
		case strings.Contains(h, "new version") || h == "new":
			col["new"] = i + 1
		case strings.Contains(h, "subject") || strings.Contains(h, "comment") || strings.Contains(h, "change"):
			col["subject"] = i + 1
		}
	}
	get := func(r []string, key string) string {
		if v, ok := col[key]; ok && v > 0 && v-1 < len(r) {
			return collapse(r[v-1])
		}
		return ""
	}
	var out []model.Change
	for _, r := range rows[1:] {
		if len(r) == 0 {
			continue
		}
		ch := model.Change{
			SpecID: specID, CRNumber: get(r, "cr"),
			Meeting:  firstNonEmpty(get(r, "meeting"), get(r, "date")),
			Category: get(r, "cat"), ToVersion: get(r, "new"), Summary: get(r, "subject"),
		}
		if rev := get(r, "rev"); rev != "" {
			ch.CRRevision, _ = strconv.Atoi(strings.TrimSpace(rev))
		}
		if ch.CRNumber == "" && ch.Summary == "" && ch.ToVersion == "" {
			continue
		}
		out = append(out, ch)
	}
	return out
}

// isChangeHistory matches the annex heading even when it carries the "Annex Z
// (informative):" prefix (the runs are joined across a <w:br/>).
// changeHeaderScore counts how many change-history column keywords a row's
// cells contain — used to find the real header past any merged title row.
func changeHeaderScore(row []string) int {
	n := 0
	for _, c := range row {
		switch h := strings.ToLower(collapse(c)); {
		case h == "date", h == "meeting", h == "tdoc", h == "cr", h == "rev",
			h == "cat", strings.Contains(h, "new version"),
			strings.Contains(h, "subject"):
			n++
		}
	}
	return n
}

func isChangeHistory(text string) bool {
	return strings.Contains(strings.ToLower(collapse(text)), "change history")
}

func collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func firstNonEmpty(a ...string) string {
	for _, s := range a {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func parseDate(s string) time.Time {
	m := reDate.FindStringSubmatch(s)
	if m == nil {
		return time.Time{}
	}
	if m[1] != "" { // YYYY-MM-DD
		y, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		d, _ := strconv.Atoi(m[3])
		return time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
	}
	// MM/YYYY
	mo, _ := strconv.Atoi(m[4])
	y, _ := strconv.Atoi(m[5])
	return time.Date(y, time.Month(mo), 1, 0, 0, 0, 0, time.UTC)
}
