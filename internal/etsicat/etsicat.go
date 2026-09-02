// Package etsicat is the ETSI-side analogue of 3GPP's discover/status-report logic:
// the ETSI /deliver archive has NO single status page, so the directory TREE itself
// is the index. This package holds the pure, offline-testable core — parse a
// directory-listing HTML page into its entries, select the latest PUBLISHED version
// of a spec, and diff the live tree against a persisted index — exactly mirroring
// what rust/discover does for 3GPP. The HTTP crawl that feeds these functions lives in
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
	"errors"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/net/html"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// PublishedMilestone is the ETSI deliver milestone code for a published deliverable.
// Draft/approval milestones (20/30/…) are NOT citable and are skipped.
const PublishedMilestone = 60

// ExtractLinks returns the CHILD NAME of every <a> in a directory-listing page, in
// document order. The real ETSI portal renders version sub-directories as ABSOLUTE
// hrefs ("/deliver/etsi_ts/103200_103299/10322101/01.21.01_60/"), while a plain
// Apache/nginx autoindex renders them RELATIVE ("01.21.01_60/"). To be agnostic to
// both, every href is reduced to its last path segment (the child name) rather than
// dropped when absolute — dropping absolute hrefs was the bug that made the live
// crawl resolve zero versions (the parent range dir reduces to "103200_103299", which
// simply fails ParseVersionDir, so it is harmless). Sort headers ("?C=N"), anchors,
// and external scheme URLs are still dropped.
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
				// Skip parent links, anchors, queries, and external (scheme) URLs.
				if h == "" || h == "../" || h == ".." || strings.HasPrefix(h, "?") ||
					strings.HasPrefix(h, "#") || strings.Contains(h, "://") {
					continue
				}
				if name := lastSegment(h); name != "" {
					out = append(out, name)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return out, nil
}

// lastSegment reduces a directory-listing href to its final path component, so an
// absolute portal href and a relative autoindex name both yield the same child name:
//
//	"/deliver/etsi_ts/103200_103299/10322101/01.21.01_60/" -> "01.21.01_60"
//	"01.21.01_60/"                                          -> "01.21.01_60"
//
// A trailing slash is trimmed first so the basename is the directory name, not "".
func lastSegment(href string) string {
	h := strings.TrimSuffix(href, "/")
	if i := strings.LastIndex(h, "/"); i >= 0 {
		h = h[i+1:]
	}
	return h
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

// ErrNotInArchive marks a deliverable the /deliver archive does not hold: the
// crawl enumerated its id, and its folder answers 404 in the tree it was
// enumerated from. That is a DEFINITIVE answer, not a transient one.
//
// Why it needs a name. The fetcher retried every non-200 five times with backoff,
// and the caller then reported "will retry next run" — so 90 ids enumerated from
// the ETSI index but never published (historical ETS/I-ETS numbers; the whole
// 100000_100099 range folder is a 404 in all three trees, checked 2026-09-02)
// cost five requests each, every run, forever, and left the operator reading a
// completeness report that promised work which can never complete. A corpus is
// only "complete" if the tool can say WHICH of the two reasons a document is
// missing for.
var ErrNotInArchive = errors.New("not published in the ETSI /deliver archive")

// Fetcher fetches a URL and returns its body. Injected so the crawl is unit-testable
// with fixtures (no network) — the CLI passes an http.Get-backed implementation.
type Fetcher func(url string) (io.ReadCloser, error)

// ResolveLatest resolves a TS. Kept as the TS-only form for the built-in LI suite
// and the citation path, both of which are TS by construction.
func ResolveLatest(fetch Fetcher, id string) (version string, ok bool, err error) {
	return ResolveLatestIn(fetch, model.EtsiTypeTS, id)
}

// ResolveLatestIn fetches a deliverable's deliver directory IN A GIVEN document-type
// folder and returns its latest PUBLISHED version string ("1.21.1"). ok is false (no
// error) when the directory carries no published version (drafts only / empty) — the
// caller skips it. A fetch/parse error is returned so the CLI can retry (the resume
// mechanic), never silently drop a deliverable.
//
// The folder is a parameter because the archive is three trees, not one. Resolving
// every enumerated id under etsi_ts made each TR and EN 404, five times over (the
// retry budget), and then land in "failed" — which is how --all could crawl the
// whole archive, report 7 501 deliverables, and still index only the TS ones.
func ResolveLatestIn(fetch Fetcher, typeDir, id string) (version string, ok bool, err error) {
	dir := model.EtsiDeliverURLIn(typeDir, id, "") // version-less ⇒ the deliverable's directory URL
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

// BuildSiteWorkers bounds how many deliverables BuildSite resolves at once.
//
// One at a time is fine for the fourteen-spec LI suite and hopeless for the whole
// archive: --all enumerates ~7 500 deliverables, each needing its own directory
// GET, so a sequential resolve is one round-trip deep, 7 500 times — an hour or
// more of pure latency, which is why the --all flag existed but was never usable.
// The work is entirely network-bound, so a small pool collapses that to minutes.
// Small on purpose: this is someone else's CDN, and cmd/discover-etsi already had
// to disable HTTP/2 because ETSI's edge falls over under a heavy crawl.
const BuildSiteWorkers = 12

// Deliverable is an ETSI deliverable's archive identity: its canonical number id
// plus the /deliver document-type folder it actually lives in. The folder is not
// derivable from the id — the number alone does not say whether 103 101 is a TS or
// a TR — so it has to travel with it from enumeration all the way to the URL.
type Deliverable struct {
	TypeDir string // model.EtsiTypeTS | EtsiTypeTR | EtsiTypeEN
	ID      string // "103 221-1"
}

// BuildSite resolves the latest published version of every TS id. The TS-only form,
// for the built-in LI suite and --specs; its result is keyed by id because within
// one document type an id IS the identity.
func BuildSite(fetch Fetcher, ids []string) (site map[string]string, failed []string) {
	ds := make([]Deliverable, 0, len(ids))
	for _, id := range ids {
		ds = append(ds, Deliverable{TypeDir: model.EtsiTypeTS, ID: id})
	}
	typed, failed, absent := BuildSiteIn(fetch, ds)
	failed = append(failed, absent...)
	sort.Strings(failed)
	site = make(map[string]string, len(typed))
	for d, v := range typed {
		site[d.ID] = v
	}
	return site, failed
}

// BuildSiteIn resolves the latest published version of every deliverable, returning
// the live "site" map (deliverable -> version) plus the ids that errored (for the
// caller to retry/report). Deliverables with no published version are simply
// omitted (cite-or-silent).
//
// THE KEY IS THE WHOLE DELIVERABLE, not its number. Archive identity is (folder,
// id), and the same number can exist as a TS and as a TR — /deliver/etsi_ts/…/
// 103101/ is a 404 while the etsi_tr one is TR 103 101, but nothing stops ETSI
// from filling both. Keyed by id alone, two concurrent workers would race to
// overwrite each other, and a later lookup could pair one document's version with
// another's folder and URL. There are no such collisions in the archive today;
// keying on the identity rather than on that fact is what keeps it from mattering.
//
// Resolution is concurrent (BuildSiteWorkers at a time) but the RESULT is not
// order-dependent: site is a map, and failed is sorted before it is returned, so
// two runs over the same archive produce byte-identical output. Fetcher is called
// from several goroutines, so an implementation must be safe for concurrent use —
// the http.Client-backed one in cmd/discover-etsi is.
func BuildSiteIn(fetch Fetcher, ds []Deliverable) (site map[Deliverable]string, failed, absent []string) {
	site = make(map[Deliverable]string, len(ds))
	if len(ds) == 0 {
		return site, nil, nil
	}

	workers := BuildSiteWorkers
	if len(ds) < workers {
		workers = len(ds)
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	jobs := make(chan Deliverable)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for d := range jobs {
				v, ok, err := ResolveLatestIn(fetch, d.TypeDir, d.ID)
				mu.Lock()
				switch {
				case errors.Is(err, ErrNotInArchive):
					absent = append(absent, d.ID)
				case err != nil:
					failed = append(failed, d.ID)
				case ok:
					site[d] = v
				}
				mu.Unlock()
			}
		}()
	}
	for _, d := range ds {
		jobs <- d
	}
	close(jobs)
	wg.Wait()

	sort.Strings(failed)
	sort.Strings(absent)
	return site, failed, absent
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

// AllPublished returns EVERY published version (milestone 60) in a deliverable's
// directory, oldest first, deduplicated.
//
// LatestPublished answers "what should we serve"; this answers "what is there".
// The distinction is the difference between a corpus that can show how a
// deliverable changed and one that can only show where it ended up: TS 103 221-1
// alone carries 23 published versions, and the 3GPP half of this corpus already
// keeps every release of every spec for exactly that reason.
//
// Oldest first because that is the order a reader follows a history in, and
// because the ingest attributes each file independently — nothing depends on the
// order, so it may as well be the useful one.
func AllPublished(links []string) []Version {
	seen := map[int]bool{}
	var out []Version
	for _, l := range links {
		v, ok := ParseVersionDir(l)
		if !ok || v.Milestone != PublishedMilestone || seen[v.rank()] {
			continue
		}
		seen[v.rank()] = true
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].rank() < out[j].rank() })
	return out
}

// ResolveAllIn lists every published version of one deliverable. Same contract as
// ResolveLatestIn: a fetch/parse error is returned so the caller can retry, and a
// directory with no published version yields an empty slice rather than an error.
func ResolveAllIn(fetch Fetcher, typeDir, id string) ([]string, error) {
	dir := model.EtsiDeliverURLIn(typeDir, id, "")
	if dir == "" {
		return nil, nil
	}
	body, err := fetch(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = body.Close() }()
	links, err := ExtractLinks(body)
	if err != nil {
		return nil, err
	}
	vs := AllPublished(links)
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, v.String())
	}
	return out, nil
}

// BuildHistory resolves EVERY published version of every deliverable, concurrently.
// The map is keyed by the whole deliverable for the same reason BuildSiteIn's is.
func BuildHistory(fetch Fetcher, ds []Deliverable) (history map[Deliverable][]string, failed, absent []string) {
	history = make(map[Deliverable][]string, len(ds))
	if len(ds) == 0 {
		return history, nil, nil
	}
	workers := BuildSiteWorkers
	if len(ds) < workers {
		workers = len(ds)
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	jobs := make(chan Deliverable)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for d := range jobs {
				vs, err := ResolveAllIn(fetch, d.TypeDir, d.ID)
				mu.Lock()
				switch {
				case errors.Is(err, ErrNotInArchive):
					absent = append(absent, d.ID)
				case err != nil:
					failed = append(failed, d.ID)
				case len(vs) > 0:
					history[d] = vs
				}
				mu.Unlock()
			}
		}()
	}
	for _, d := range ds {
		jobs <- d
	}
	close(jobs)
	wg.Wait()
	sort.Strings(failed)
	sort.Strings(absent)
	return history, failed, absent
}
