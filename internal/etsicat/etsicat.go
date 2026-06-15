// Package etsicat is the ETSI-side analogue of 3GPP's discover/status-report logic:
// the ETSI /deliver archive has NO single status page, so the directory TREE itself
// is the index. This package holds the pure, offline-testable core — parse a
// directory-listing HTML page into its entries, select the latest PUBLISHED version
// of a spec, and diff the live tree against a persisted index — exactly mirroring
// what cmd/discover does for 3GPP. The HTTP crawl that feeds these functions lives in
// cmd/discover-etsi; keeping the parsing/selection/diff here makes it unit-testable
// with fixtures and free of network flakiness.
//
// ETSI deliver layout (deterministic, see internal/model/etsi.go):
//
//	/deliver/<typedir>/<range>/<token>/<VV.VV.VV_milestone>/<file>.pdf
//	          etsi_ts   103200_103299  10322101  01.21.01_60   ts_10322101v012101p.pdf
//
// milestone 60 = PUBLISHED, 20/30 = draft/approval — only 60 is a citable release.
package etsicat

import (
	"io"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// PublishedMilestone is the ETSI deliver milestone code for a published deliverable.
// Draft/approval milestones (20/30/…) are NOT citable and are skipped.
const PublishedMilestone = 60

// ExtractLinks returns the href targets of every <a> in a directory-listing page,
// in document order, with the parent ("../", "..") and absolute/query links dropped.
// It is intentionally agnostic to the exact autoindex style (Apache, nginx, the ETSI
// portal): it only reads anchors, so a layout change never silently drops entries.
func ExtractLinks(r io.Reader) ([]string, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return nil, err
	}
	var out []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			for _, a := range n.Attr {
				if a.Key != "href" {
					continue
				}
				h := strings.TrimSpace(a.Val)
				// Skip parent links, anchors, queries, and absolute URLs (sort headers
				// in autoindex are "?C=N;O=D"); we only want child names.
				if h == "" || h == "../" || h == ".." || strings.HasPrefix(h, "?") ||
					strings.HasPrefix(h, "#") || strings.Contains(h, "://") || strings.HasPrefix(h, "/") {
					continue
				}
				out = append(out, h)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return out, nil
}

// reVersionDir matches an ETSI version sub-directory "VV.VV.VV_NN" (trailing slash
// optional): three 2-digit components + the milestone code.
var reVersionDir = regexp.MustCompile(`^(\d{2})\.(\d{2})\.(\d{2})_(\d+)/?$`)

// Version is a parsed ETSI version directory.
type Version struct {
	Major, Minor, Editorial, Milestone int
}

// String renders the dotted version "1.21.1" (no milestone) — the form
// model.EtsiDeliverURL consumes.
func (v Version) String() string {
	return strconv.Itoa(v.Major) + "." + strconv.Itoa(v.Minor) + "." + strconv.Itoa(v.Editorial)
}

// rank orders versions for "latest" selection (major, then minor, then editorial).
func (v Version) rank() int { return v.Major*1_000_000 + v.Minor*1_000 + v.Editorial }

// ParseVersionDir parses a "VV.VV.VV_NN" directory name. ok is false for anything
// else (the parent link, a file, a stray dir), so callers can filter a raw link list.
func ParseVersionDir(name string) (Version, bool) {
	m := reVersionDir.FindStringSubmatch(strings.TrimSpace(name))
	if m == nil {
		return Version{}, false
	}
	maj, _ := strconv.Atoi(m[1])
	mnr, _ := strconv.Atoi(m[2])
	ed, _ := strconv.Atoi(m[3])
	ms, _ := strconv.Atoi(m[4])
	return Version{Major: maj, Minor: mnr, Editorial: ed, Milestone: ms}, true
}

// LatestPublished scans a spec directory's entries (links) and returns the highest
// PUBLISHED version (milestone 60). ok is false when the directory carries no
// published version (only drafts, or empty) — the caller then skips that spec
// (cite-or-silent: never cite a draft as a release).
func LatestPublished(links []string) (Version, bool) {
	var best Version
	found := false
	for _, l := range links {
		v, ok := ParseVersionDir(l)
		if !ok || v.Milestone != PublishedMilestone {
			continue
		}
		if !found || v.rank() > best.rank() {
			best, found = v, true
		}
	}
	return best, found
}

// Diff returns the spec ids whose live (site) version is newer than — or absent
// from — the persisted index, i.e. the ETSI work-list for this run. Same contract as
// 3GPP's deltaSeries: an unchanged spec is skipped, a new/bumped spec is selected.
// Versions compare by (major, minor, editorial); a malformed index entry counts as
// "absent" (re-fetch, fail-safe). Output is sorted for determinism by the caller.
func Diff(site, index map[string]string) []string {
	var changed []string
	for id, sv := range site {
		iv, ok := index[id]
		if !ok || newer(sv, iv) {
			changed = append(changed, id)
		}
	}
	return changed
}

// Fetcher fetches a URL and returns its body. Injected so the crawl is unit-testable
// with fixtures (no network) — the CLI passes an http.Get-backed implementation.
type Fetcher func(url string) (io.ReadCloser, error)

// ResolveLatest fetches a spec's deliver directory and returns its latest PUBLISHED
// version string ("1.21.1"). ok is false (no error) when the spec dir has no published
// version (drafts only / empty) — the caller skips it. A fetch/parse error is returned
// so the CLI can retry (the resume mechanic), never silently drop a spec.
func ResolveLatest(fetch Fetcher, id string) (version string, ok bool, err error) {
	dir := model.EtsiDeliverURL(id, "") // version-less ⇒ the spec's directory URL
	if dir == "" {
		return "", false, nil // not an ETSI-shaped id
	}
	body, err := fetch(dir)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = body.Close() }()
	links, err := ExtractLinks(body)
	if err != nil {
		return "", false, err
	}
	v, found := LatestPublished(links)
	if !found {
		return "", false, nil
	}
	return v.String(), true, nil
}

// BuildSite resolves the latest published version of every id, returning the live
// "site" map (id -> version) plus the ids that errored (for the caller to retry/report).
// Ids with no published version are simply omitted (cite-or-silent).
func BuildSite(fetch Fetcher, ids []string) (site map[string]string, failed []string) {
	site = make(map[string]string, len(ids))
	for _, id := range ids {
		v, ok, err := ResolveLatest(fetch, id)
		switch {
		case err != nil:
			failed = append(failed, id)
		case ok:
			site[id] = v
		}
	}
	return site, failed
}

// newer reports whether ETSI version a is strictly newer than b (major/minor/edit).
// An unparseable b is treated as oldest (so a wins → re-fetch).
func newer(a, b string) bool {
	am, an, ae, aok := model.ParseEtsiVersion(a)
	if !aok {
		return false
	}
	bm, bn, be, bok := model.ParseEtsiVersion(b)
	if !bok {
		return true
	}
	if am != bm {
		return am > bm
	}
	if an != bn {
		return an > bn
	}
	return ae > be
}
