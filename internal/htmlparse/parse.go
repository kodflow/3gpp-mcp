// Package htmlparse turns a LibreOffice-converted 3GPP spec (HTML) into the
// domain model: a Spec, its SpecVersion, clause-level chunks, and the Change
// History rows.
//
// Why HTML and not DOCX: ~55% of the corpus is legacy binary .doc that
// encoding/xml cannot read, so scripts/corpus.sh converts every spec to HTML
// via LibreOffice (DECISION 2026-05-25, overrides CLAUDE.md §13). LibreOffice
// renders Heading1..Heading7 styles as <h1>..<h6> whose text is
// "<clause-number>\t<title>" (e.g. "6.2.3.1\tHandover command"); body text is
// <p>/<li>/<td>; tables are <table>. We chunk by clause (never a token window,
// CLAUDE.md §13): one leaf heading + the text until the next heading.
package htmlparse

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// ParsedSpec is the full result of parsing one HTML file.
type ParsedSpec struct {
	Spec     model.Spec
	Version  model.SpecVersion
	Clauses  []model.Clause
	Changes  []model.Change
	Degraded bool // converted with EMF/WMF stripped (figures missing)
	// SawChangeHistory is true when a "Change History" heading was matched,
	// regardless of how many rows the following table yielded. A true value with
	// len(Changes)==0 signals a parse miss (heading found, table not captured) —
	// the ingest loop surfaces it as a degraded signal.
	SawChangeHistory bool
}

var (
	reNumeric  = regexp.MustCompile(`^([0-9]+(?:\.[0-9A-Za-z]+)*)\s+(.+)$`)
	reAnnexSub = regexp.MustCompile(`^([A-Z]\.[0-9]+(?:\.[0-9A-Za-z]+)*)\s+(.+)$`)
	reAnnex    = regexp.MustCompile(`(?i)^Annex\s+([A-Z][0-9]*)\s*[:.)]?\s*(.*)$`)
	reWS       = regexp.MustCompile(`[ \t\x{00a0}]+`)
	// num = 4 or 5 digits (legacy GSM is 4: 0388 = GSM 03.88; modern is 5: 23501 =
	// TS 23.501), optionally a "-N" multi-part suffix (36521-1, 34123-2 …); code =
	// the 3-char compact OR 6-digit decimal version code (083700 = 8.37.0; legacy
	// GSM 500 = 5.0.0). 5-digit names still match {4,5} greedily, so modern parsing
	// is unchanged — this only ADMITS the previously-rejected 4-digit GSM (#129).
	reFileName = regexp.MustCompile(`^([0-9]{4,5}(?:-[0-9]+)?)-([0-9a-z]{3}|[0-9]{6})(?:_.*)?$`)
	// A release directory name in …/convert/<Rel>/<file>; the AUTHORITATIVE release
	// (from the status-report-driven worklist), not the version-major.
	reReleaseDir = regexp.MustCompile(`^(Rel-[0-9]+|GSM|Phase[0-9]+)$`)
	reDate       = regexp.MustCompile(`\b(\d{4})-(\d{2})-(\d{2})\b|\b(\d{2})/(\d{4})\b`)
	// ETSI provenance header (prepended by scripts/etsi-corpus.sh): ETSI deliverables
	// have no 3GPP "<num>-<code>" filename, so the SDO/id/version travel in the HTML
	// itself, e.g. "<!-- ETSI-SPEC: 103 221-1 | 1.21.1 -->". This is what lets the SAME
	// htmlparse → ingest path serve both corpora (the user's "process identique").
	reEtsiHeader = regexp.MustCompile(`<!--\s*ETSI-SPEC:\s*([0-9][0-9 ]+[0-9](?:-[0-9]+)?)\s*\|\s*([0-9]+\.[0-9]+\.[0-9]+)\s*-->`)
)

// ParseFile parses the HTML spec at path (…/convert/<Rel>/<num>-<code>.html).
func ParseFile(path string) (*ParsedSpec, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return Parse(path, f)
}

// Parse reads HTML from r; path is used only to derive spec id / version (3GPP path),
// UNLESS the HTML carries an ETSI provenance header, in which case the id/version come
// from there (ETSI deliverables have no 3GPP "<num>-<code>" filename).
func Parse(path string, r io.Reader) (*ParsedSpec, error) {
	ps := &ParsedSpec{}
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read html %s: %w", path, err)
	}
	// ETSI provenance header wins over the 3GPP filename derivation (process identique:
	// same parser, the SDO/id/version just arrive in the body for ETSI).
	if !ps.metaFromETSIHeader(raw) {
		if err := ps.metaFromFilename(path); err != nil {
			return nil, err
		}
	}
	root, err := html.Parse(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("parse html %s: %w", path, err)
	}

	w := &walker{ps: ps}
	w.walk(root)
	w.flush() // emit the final accumulated clause

	w.finishChangeHistory()
	ps.deriveTitleAndType(w)
	return ps, nil
}

// metaFromFilename fills spec_id / series / release / version / url from the
// "<num>-<code>" base name.
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
	// The release is the worklist's (the …/convert/<Rel>/ directory), NOT the
	// version-major: a draft v1.x.x of a Rel-20 spec lives under Rel-20, and the
	// status report agrees — deriving it from the major would mis-file it as Rel-1
	// and discover would re-flag the (spec,Rel-20) key forever. Fall back to the
	// version-major decode only when the parent dir is not a release (e.g. a test
	// fixture writing a bare file).
	if dir := filepath.Base(filepath.Dir(path)); reReleaseDir.MatchString(dir) {
		release = dir
	}
	series := model.SeriesOf(specID)
	ps.Spec = model.Spec{
		SpecID:       specID,
		Series:       series,
		DocType:      "TS", // refined in deriveTitleAndType
		WorkingGroup: model.WorkingGroupForSeries(series),
	}
	ps.Version = model.SpecVersion{
		SpecID:  specID,
		Release: release,
		Version: version,
		DocxURL: model.ArchiveURL(specID, version),
	}
	return nil
}

// metaFromETSIHeader fills the Spec/Version from an ETSI provenance comment in the
// HTML body ("<!-- ETSI-SPEC: 103 221-1 | 1.21.1 -->"), returning true when present.
// ETSI has no 3GPP "<num>-<code>" filename, so the id/version travel in the body; the
// release bucket is the constant "ETSI" (ETSI versions are V<maj>.<min>.<ed>, not
// Rel-NN), and the citation URL is the deterministic deliver-archive PDF.
func (ps *ParsedSpec) metaFromETSIHeader(raw []byte) bool {
	m := reEtsiHeader.FindSubmatch(raw)
	if m == nil {
		return false
	}
	id := reWS.ReplaceAllString(strings.TrimSpace(string(m[1])), " ")
	version := string(m[2])
	ps.Spec = model.Spec{
		SpecID:       "ETSI TS " + id,
		Series:       "ETSI",
		DocType:      "TS",
		WorkingGroup: "ETSI",
	}
	ps.Version = model.SpecVersion{
		SpecID:  "ETSI TS " + id,
		Release: "ETSI",
		Version: version,
		DocxURL: model.EtsiDeliverURL(id, version),
	}
	return true
}

// walker accumulates clauses while traversing the DOM in document order.
type walker struct {
	ps  *ParsedSpec
	cur *model.Clause // clause currently being filled
	buf strings.Builder
	id  uint64

	headings []string // collected heading texts (for title/type heuristics)

	informativeAnnex bool

	inChangeHistory bool
	chTables        [][][]string // raw tables captured while in the change-history section
}

func (w *walker) walk(n *html.Node) {
	switch n.Type {
	case html.CommentNode:
		if strings.Contains(n.Data, "3GPP-MCP-DEGRADED") {
			w.ps.Degraded = true
		}
	case html.ElementNode:
		switch n.Data {
		case "h1", "h2", "h3", "h4", "h5", "h6":
			w.onHeading(nodeText(n))
			return // don't descend; heading text already captured
		case "table":
			w.onTable(n)
			return // table text captured; don't double-walk
		case "p", "li", "pre", "dd", "dt":
			txt := nodeText(n)
			if txt != "" {
				w.buf.WriteString(txt)
				w.buf.WriteString("\n")
			}
			return
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		w.walk(c)
	}
}

// onHeading flushes the previous clause and starts a new one.
func (w *walker) onHeading(text string) {
	text = collapse(text)
	if text == "" {
		return
	}
	w.headings = append(w.headings, text)

	// Detect the Change History section so the following table is captured.
	if isChangeHistory(text) {
		w.flush()
		w.inChangeHistory = true
		w.ps.SawChangeHistory = true
		w.cur = nil
		return
	}
	w.inChangeHistory = false

	path, heading, informative := classifyHeading(text)
	// Track informative-annex state so clauses inherit is_normative.
	if reAnnex.MatchString(text) {
		w.informativeAnnex = informative
	} else if path != "" && !strings.ContainsRune(path, '.') {
		// a new top-level numbered clause resets annex context
		w.informativeAnnex = false
	}

	w.flush()
	w.id++
	w.cur = &model.Clause{
		ChunkID:     w.id,
		SpecID:      w.ps.Spec.SpecID,
		Release:     w.ps.Version.Release,
		Version:     w.ps.Version.Version,
		ClausePath:  path,
		Heading:     heading,
		IsNormative: !w.informativeAnnex,
	}
}

// onTable serializes a table to tab/newline text (searchable) and, when inside
// the change-history section, also keeps the raw rows for structured parsing.
func (w *walker) onTable(n *html.Node) {
	rows := tableRows(n)
	if w.inChangeHistory {
		w.chTables = append(w.chTables, rows)
	}
	for _, r := range rows {
		w.buf.WriteString(strings.Join(r, "\t"))
		w.buf.WriteString("\n")
	}
}

// flush finalises the current clause (if any) into the result set.
func (w *walker) flush() {
	if w.cur == nil {
		w.cur = nil
		w.buf.Reset()
		return
	}
	w.cur.Text = strings.TrimSpace(w.buf.String())
	w.buf.Reset()
	// Keep clauses that carry either a path or some text.
	if w.cur.ClausePath != "" || w.cur.Heading != "" || w.cur.Text != "" {
		w.ps.Clauses = append(w.ps.Clauses, *w.cur)
	}
	w.cur = nil
}

// classifyHeading splits "6.2.3 Title" into ("6.2.3","Title",informative?).
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
	return "", text, false // unnumbered front matter (Foreword, cover, …)
}

func (ps *ParsedSpec) deriveTitleAndType(w *walker) {
	// doc_type: scan the first headings/front-matter for an explicit marker.
	head := strings.ToLower(strings.Join(w.headings, " ") + " " + ps.firstClausesText(3))
	if strings.Contains(head, "technical report") || strings.Contains(head, "tr "+ps.Spec.SpecID) {
		ps.Spec.DocType = "TR"
	} else {
		ps.Spec.DocType = "TS"
	}
	// title: first unnumbered front-matter heading that looks like a real
	// title — short, and free of cover boilerplate (copyright, logos, ToC).
	for _, h := range w.headings {
		if classify, _, _ := classifyHeading(h); classify != "" {
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

// titleBoiler matches cover-page boilerplate that must not be taken as a title.
var titleBoiler = regexp.MustCompile(`(?i)copyright|reproduc|notification|all rights|organizational partners|foreword|^3gpp t[sr]\b|^contents$|^scope$|^introduction$|^general$|trademark`)

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

// ---- change-history table parsing ---------------------------------------

func (w *walker) finishChangeHistory() {
	var latest time.Time
	for _, rows := range w.chTables {
		w.ps.Changes = append(w.ps.Changes, parseChangeTable(rows, w.ps.Spec.SpecID)...)
		// Freeze-date proxy: the most recent date anywhere in the change history.
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

// parseChangeTable maps a captured change-history table to Change rows. Column
// order varies across specs, so we map by header keywords and tolerate misses.
func parseChangeTable(rows [][]string, specID string) []model.Change {
	if len(rows) < 2 {
		return nil
	}
	col := map[string]int{}
	for i, h := range rows[0] {
		h = strings.ToLower(collapse(h))
		switch {
		case strings.Contains(h, "date"):
			col["date"] = i + 0
			col["date_set"] = i + 1
		case strings.Contains(h, "meeting") || h == "tsg #" || strings.Contains(h, "tsg"):
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
			SpecID:    specID,
			CRNumber:  get(r, "cr"),
			Meeting:   firstNonEmpty(get(r, "meeting"), get(r, "date")),
			Category:  get(r, "cat"),
			ToVersion: get(r, "new"),
			Summary:   get(r, "subject"),
		}
		if rev := get(r, "rev"); rev != "" {
			ch.CRRevision, _ = strconv.Atoi(strings.TrimSpace(rev))
		}
		if ch.CRNumber == "" && ch.Summary == "" && ch.ToVersion == "" {
			continue // junk row
		}
		out = append(out, ch)
	}
	return out
}

// ---- text helpers -------------------------------------------------------

func nodeText(n *html.Node) string {
	var sb strings.Builder
	var rec func(*html.Node)
	rec = func(nd *html.Node) {
		if nd.Type == html.TextNode {
			sb.WriteString(nd.Data)
		}
		for c := nd.FirstChild; c != nil; c = c.NextSibling {
			rec(c)
		}
	}
	rec(n)
	return sb.String()
}

func tableRows(n *html.Node) [][]string {
	var rows [][]string
	var rec func(*html.Node)
	rec = func(nd *html.Node) {
		if nd.Type == html.ElementNode && nd.Data == "tr" {
			var cells []string
			for c := nd.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.ElementNode && (c.Data == "td" || c.Data == "th") {
					cells = append(cells, collapse(nodeText(c)))
				}
			}
			if len(cells) > 0 {
				rows = append(rows, cells)
			}
			return
		}
		for c := nd.FirstChild; c != nil; c = c.NextSibling {
			rec(c)
		}
	}
	rec(n)
	return rows
}

func collapse(s string) string {
	s = strings.ReplaceAll(s, " ", " ")
	s = reWS.ReplaceAllString(s, " ")
	return strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
}

func isChangeHistory(text string) bool {
	t := strings.ToLower(collapse(text))
	return strings.Contains(t, "change history") || strings.HasSuffix(t, "change history")
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
	if m[5] != "" { // MM/YYYY
		mo, _ := strconv.Atoi(m[4])
		y, _ := strconv.Atoi(m[5])
		return time.Date(y, time.Month(mo), 1, 0, 0, 0, 0, time.UTC)
	}
	return time.Time{}
}
