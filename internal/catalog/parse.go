package catalog

import (
	"io"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// reRelAnchor matches the per-release section anchors in status-report.htm,
// e.g. <a name="activeRel-19"> / <a name="deadRel-16">.
var reRelAnchor = regexp.MustCompile(`(?:active|dead)(Rel-\d+)`)

// reFreeze splits "2024-06-21 (SA#104)" into the date and the meeting tag.
var reFreeze = regexp.MustCompile(`(\d{4}-\d{2}-\d{2})(?:\s*\(([^)]+)\))?`)

// ParseStatusReport parses dynareport status-report.htm into authoritative
// per-spec metadata and the latest (spec, release, version) per section. A spec
// recurs across release sections; SpecMeta is deduped (newest release wins),
// VersionRel keeps every (spec, release) latest-version pair.
func ParseStatusReport(r io.Reader) ([]SpecMeta, []VersionRel, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return nil, nil, err
	}
	specByID := map[string]SpecMeta{}
	var order []string
	var vers []VersionRel
	curRelease := ""

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if n.Data == "a" {
				if m := reRelAnchor.FindStringSubmatch(attr(n, "name") + " " + attr(n, "id")); m != nil {
					curRelease = m[1]
				}
			}
			if n.Data == "table" {
				parseStatusTable(n, curRelease, specByID, &order, &vers)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	specs := make([]SpecMeta, 0, len(order))
	for _, id := range order {
		specs = append(specs, specByID[id])
	}
	return specs, vers, nil
}

// parseStatusTable parses one section table if its header matches the catalogue
// signature (type / spec num / title / vers / WG), attributing rows to release.
func parseStatusTable(t *html.Node, release string, specByID map[string]SpecMeta, order *[]string, vers *[]VersionRel) {
	rows := tableRows(t)
	if len(rows) < 2 {
		return
	}
	idx := headerIndex(rows[0])
	ti, si, tti, vi, wi := idx["type"], idx["spec num"], idx["title"], idx["vers"], idx["wg"]
	if si < 0 || ti < 0 { // not the catalogue table
		return
	}
	for _, row := range rows[1:] {
		if len(row) <= max4(ti, si, tti, vi) {
			continue
		}
		specID := normSpecID(cell(row, si))
		if specID == "" {
			continue
		}
		if _, seen := specByID[specID]; !seen {
			*order = append(*order, specID)
		}
		// Title/type/WG are identical across sections; keep the first (newest
		// section, since the report lists active releases high-to-low).
		if cur, ok := specByID[specID]; !ok || cur.Title == "" {
			specByID[specID] = SpecMeta{
				SpecID:       specID,
				Series:       seriesOf(specID),
				DocType:      strings.ToUpper(cell(row, ti)),
				Title:        cell(row, tti),
				WorkingGroup: cell(row, wi),
			}
		}
		if release != "" && vi >= 0 {
			if v := cell(row, vi); v != "" {
				*vers = append(*vers, VersionRel{SpecID: specID, Release: release, Version: v})
			}
		}
	}
}

// ParseReleases parses portal Releases.aspx. The header is unreliable (ASP.NET
// RadGrid renders blank <th>), so columns are taken by position, matching the
// stable layout: code | name | status | start | end(+meeting) | closure.
func ParseReleases(r io.Reader) ([]ReleaseMeta, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return nil, err
	}
	var out []ReleaseMeta
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "table" {
			for _, row := range tableRows(n) {
				if len(row) < 5 {
					continue
				}
				code := strings.TrimSpace(row[0])
				if !strings.HasPrefix(code, "Rel-") {
					continue
				}
				rm := ReleaseMeta{
					Code:      code,
					Name:      strings.TrimSpace(row[1]),
					Status:    strings.TrimSpace(row[2]),
					StartDate: parseDate(row[3]),
				}
				if d, meeting := splitFreeze(row[4]); d != nil {
					rm.FreezeDate, rm.FreezeMeeting = d, meeting
				}
				out = append(out, rm)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return out, nil
}

// ---- helpers ----

func splitFreeze(s string) (*time.Time, string) {
	m := reFreeze.FindStringSubmatch(s)
	if m == nil {
		return nil, ""
	}
	return parseDate(m[1]), m[2]
}

func headerIndex(header []string) map[string]int {
	idx := map[string]int{"type": -1, "spec num": -1, "title": -1, "vers": -1, "wg": -1}
	for i, h := range header {
		key := strings.ToLower(collapse(h))
		if _, ok := idx[key]; ok {
			idx[key] = i
		}
	}
	return idx
}

// normSpecID extracts "23.501" from a cell that may wrap it in a link/text.
var reSpecID = regexp.MustCompile(`\b(\d{2}\.\d{3})\b`)

func normSpecID(s string) string {
	if m := reSpecID.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

func seriesOf(specID string) string {
	if i := strings.Index(specID, "."); i > 0 {
		return specID[:i]
	}
	return ""
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func cell(row []string, i int) string {
	if i < 0 || i >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[i])
}

func max4(a, b, c, d int) int {
	m := a
	for _, v := range []int{b, c, d} {
		if v > m {
			m = v
		}
	}
	return m
}

// tableRows extracts a table's rows as trimmed cell-text matrices (th or td).
func tableRows(t *html.Node) [][]string {
	var rows [][]string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "tr" {
			var cells []string
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.ElementNode && (c.Data == "td" || c.Data == "th") {
					cells = append(cells, collapse(nodeText(c)))
				}
			}
			if len(cells) > 0 {
				rows = append(rows, cells)
			}
			return // don't descend into nested tables of a row here
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(t)
	return rows
}

func nodeText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

func collapse(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, " ", " ")), " ")
}

func parseDate(s string) *time.Time {
	s = strings.TrimSpace(s)
	if m := reFreeze.FindStringSubmatch(s); m != nil {
		s = m[1]
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil
	}
	return &t
}
