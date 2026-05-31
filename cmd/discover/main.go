// Command discover decides WHICH 3GPP series need (re)indexing, so the CI
// matrix is sized to the actual delta instead of being frozen or always-full.
//
// It fetches the 3GPP global status report (one ~5 MB GET that lists the latest
// version of every spec PER RELEASE section), compares each (spec, release)'s
// site version against a small corpus-index.json ("spec_id|release" -> indexed
// version, published alongside the DB), and prints a JSON array of the series
// that changed — for a GitHub Actions matrix (fromJSON). No index (first run) or
// --all => every series (full build).
//
//	discover --index corpus-index.json --floor Rel-4    # delta
//	discover --all                                       # full (all series)
//
// The index key is per-(spec, release) — NOT one scalar per spec (plan PR-5,
// finding delta-blind-to-nonmonotonic-lower-release-update). A frozen release
// keeps receiving maintenance versions (CLAUDE.md §8 #8), so collapsing a spec to
// its single highest X.Y.Z hid a Rel-16 bump whenever Rel-19 was higher. We reuse
// catalog.ParseStatusReport (the same per-(spec,release) parser the metadata
// overlay uses) instead of a flat regex, so the release attribution is shared.
//
// Pure Go (no CGO): the heavy DuckDB work stays in ingest/merge. 3gpp.org needs
// a browser User-Agent (it 403s bots), so we send one.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kodflow/3gpp-mcp/internal/catalog"
	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/subjectmeta"
)

func main() {
	statusURL := flag.String("status-url", "https://www.3gpp.org/DynaReport/status-report.htm", "3GPP global status report")
	indexPath := flag.String("index", "", "corpus-index.json (spec_id -> indexed version); empty/missing => full")
	subjectIndexPath := flag.String("subject-index", "", "subject-index.json (subject -> footprint); a changed subject forces its series into the delta")
	buildIndexPath := flag.String("build-index", "", "build-index.json (the three canonical identities); a drift vs current code forces a full rebuild")
	expectModel := flag.String("embed-model", "", "the embedder ModelID the current build will use; compared against the published EmbedIdentity")
	floor := flag.String("floor", "Rel-99", "lowest release (Rel-99 = all real 3GPP releases; pre-Rel-99 drafts dropped)")
	all := flag.Bool("all", false, "force a full build (every series), ignoring the index")
	flag.Parse()

	site, err := fetchStatus(*statusURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "discover:", err)
		os.Exit(1)
	}
	idx := loadIndex(*indexPath)
	full := *all || len(idx) == 0
	floorMajor := major(*floor)

	series := deltaSeries(site, idx, floorMajor, full)

	// Subject delta (plan TROU #1): a subject whose code changed (footprint shifts
	// vs the published subject-index.json) forces its owning series back into the
	// matrix, so its shard rebuilds and the normal ingest pass re-runs the subject.
	// Only meaningful on a delta — a full build already rebuilds everything.
	var subjChanged []string
	if !full {
		subjChanged = subjectmeta.ChangedSeries(loadIndex(*subjectIndexPath))
		for _, s := range subjChanged {
			series[s] = true
		}
	}

	// Build-identity delta (plan PR-3): a parser/chunking/schema change
	// (SpecIngestIdentity) or a model change (EmbedIdentity) moves NO spec version
	// and NO subject footprint, so the checks above are blind to it. Compare the
	// published build-index against the current code's identities; a drift in the
	// spec-ingest or embed identity is corpus-global (it affects every series'
	// content or vectors), so force EVERY site series above the floor into the
	// matrix. The global-enrichment identity is published for completeness but is
	// owned by PR-7's enricher refresh, so it does not size the per-spec matrix here.
	var identityDrift []string
	if !full && *buildIndexPath != "" {
		published := loadBuildIndex(*buildIndexPath)
		current := model.CurrentBuildIndex(
			subjectmeta.IngestFootprints(), subjectmeta.ASN1ScannerVersion, *expectModel,
			model.GlobalEnrichmentParts{},
		)
		identityDrift = published.Differs(current)
		forceAll := false
		for _, d := range identityDrift {
			if d == "spec_ingest_identity" || d == "embed_identity" {
				forceAll = true
			}
		}
		if forceAll {
			for key := range site {
				spec, rel := splitKey(key)
				if major(rel) >= floorMajor {
					series[spec[:2]] = true
				}
			}
		}
	}

	out := make([]string, 0, len(series))
	for s := range series {
		out = append(out, s)
	}
	sort.Strings(out)
	mode := "delta"
	if full {
		mode = "full"
	}
	fmt.Fprintf(os.Stderr, "discover: mode=%s site_keys=%d indexed=%d subject-changed=%v identity-drift=%v -> %d series: %v\n",
		mode, len(site), len(idx), subjChanged, identityDrift, len(out), out)
	b, _ := json.Marshal(out)
	fmt.Println(string(b)) // stdout = the JSON matrix
}

// fetchStatus GETs the status report and returns the highest version per
// (spec, release), keyed "spec_id|Rel-NN". It reuses catalog.ParseStatusReport —
// the same per-release parser the metadata overlay uses — so release attribution
// (the activeRel-/deadRel- section anchors) is shared, not re-derived from a flat
// regex that only saw the single highest version of each spec.
func fetchStatus(url string) (map[string]string, error) {
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) discover")
	c := &http.Client{Timeout: 60 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status-report GET %s: %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return parseStatus(body)
}

// parseStatus turns the status-report HTML into a "spec_id|Rel-NN" -> highest
// version map. Split from fetchStatus so tests can exercise the per-(spec,release)
// collapse without a network round-trip.
func parseStatus(body []byte) (map[string]string, error) {
	_, vers, err := catalog.ParseStatusReport(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, vr := range vers {
		if vr.Release == "" || vr.Version == "" {
			continue
		}
		key := vr.SpecID + "|" + vr.Release
		if cmpVer(vr.Version, out[key]) > 0 { // keep the highest version per (spec,release)
			out[key] = vr.Version
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("parsed 0 (spec,release) rows from status report (layout changed?)")
	}
	return out, nil
}

// deltaSeries returns the set of 2-digit series that need (re)indexing, comparing
// the live site versions against the published index PER (spec, release). This is
// the keystone of plan PR-5: because both site and idx are keyed "spec_id|Rel-NN",
// a maintenance bump to a LOWER release (e.g. Rel-16 16.16.0) is detected even
// when a HIGHER release of the same spec (e.g. Rel-19) is present and unchanged —
// the old scalar-per-spec collapse to the highest X.Y.Z hid exactly that case.
// The release floor is applied per-key on the release, not after a max-version
// collapse. On a full build every above-floor series is selected unconditionally.
func deltaSeries(site, idx map[string]string, floorMajor int, full bool) map[string]bool {
	series := map[string]bool{}
	for key, ver := range site {
		spec, rel := splitKey(key)
		if major(rel) < floorMajor {
			continue // draft / release below floor — not indexed
		}
		// idx is keyed per-(spec,release); a missing key (new release, or a
		// lower-release maintenance bump never indexed) => idx[key]=="" => changed.
		if full || cmpVer(ver, idx[key]) > 0 {
			series[spec[:2]] = true
		}
	}
	return series
}

// splitKey splits a corpus-index / site key "spec_id|Rel-NN" into its parts. A
// key without the separator (legacy flat index, or a malformed entry) yields an
// empty release, whose major()==0 falls below any real floor → treated as draft
// and skipped, which is the safe direction (the next full rebuild reseeds it).
func splitKey(key string) (spec, release string) {
	if i := strings.IndexByte(key, '|'); i >= 0 {
		return key[:i], key[i+1:]
	}
	return key, ""
}

func loadIndex(path string) map[string]string {
	m := map[string]string{}
	if path == "" {
		return m
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return m // missing => full
	}
	_ = json.Unmarshal(b, &m)
	return m
}

// loadBuildIndex reads the published build-index.json (the three canonical
// identities). A missing/unreadable file returns the zero BuildIndex, whose ""
// fields make model.BuildIndex.Differs report a drift on every identity — so a
// legacy publish with no build index self-heals by forcing a rebuild.
func loadBuildIndex(path string) model.BuildIndex {
	var bi model.BuildIndex
	if path == "" {
		return bi
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return bi
	}
	_ = json.Unmarshal(b, &bi)
	return bi
}

// major returns the leading integer of "Rel-19" or "19.6.0" (0 on parse error).
// Special case: Rel-99 IS version major 3 (not 99) in 3GPP's scheme.
func major(s string) int {
	if s == "Rel-99" {
		return 3
	}
	s = strings.TrimPrefix(s, "Rel-")
	if i := strings.IndexAny(s, ".-"); i >= 0 {
		s = s[:i]
	}
	n, _ := strconv.Atoi(s)
	return n
}

// cmpVer compares "X.Y.Z" numerically. Empty sorts lowest.
func cmpVer(a, b string) int {
	pa, pb := triple(a), triple(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			if pa[i] > pb[i] {
				return 1
			}
			return -1
		}
	}
	return 0
}

func triple(s string) [3]int {
	var t [3]int
	if s == "" {
		return t
	}
	for i, p := range strings.SplitN(s, ".", 3) {
		if i > 2 {
			break
		}
		t[i], _ = strconv.Atoi(p)
	}
	return t
}
