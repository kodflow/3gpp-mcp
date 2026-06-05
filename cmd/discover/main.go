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
	"github.com/kodflow/3gpp-mcp/internal/embed"
	"github.com/kodflow/3gpp-mcp/internal/enrichmeta"
	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/subjectmeta"
)

// legacyGSMSeries is the GSM Phase-1/2 series (2-digit, < 21) the modern status
// report does not list, added explicitly when --include-legacy-gsm is set.
//
// Scoped to "03" by design (C3): GSM Phase-1/2 is frozen 1992 history that never
// grows, and a coverage audit found the full 01–13 sweep yields exactly ONE stable
// downloadable doc (03.071) while spinning up ~12 empty matrix shards every build
// (wasted scrape/convert/index churn). corpus.sh enumerates the 03_series dir and
// picks up 03.071; the other ranges are dropped. Re-add a series here only if 3GPP
// ever republishes its Phase-1/2 archive (it has not in 30+ years).
var legacyGSMSeries = []string{"03"}

func main() {
	statusURL := flag.String("status-url", "https://www.3gpp.org/DynaReport/status-report.htm", "3GPP global status report")
	indexPath := flag.String("index", "", "corpus-index.json (spec_id -> indexed version); empty/missing => full")
	subjectIndexPath := flag.String("subject-index", "", "subject-index.json (subject -> footprint); a changed subject forces its series into the delta")
	buildIndexPath := flag.String("build-index", "", "build-index.json (the three canonical identities); a drift vs current code forces a full rebuild")
	expectModel := flag.String("embed-model", "", "the embedder FAMILY the current build will use (e.g. bge-m3); resolved to the canonical EmbedIdentity and compared against the published one")
	floor := flag.String("floor", "Rel-99", "lowest release (Rel-99 = all real 3GPP releases; pre-Rel-99 drafts dropped)")
	all := flag.Bool("all", false, "force a full build (every series), ignoring the index")
	includeLegacy := flag.Bool("include-legacy-gsm", false, "also enumerate legacy GSM Phase-1/2 series (2-digit < 21). The status report omits their 4-digit specs and the release floor would drop them, so they are added explicitly; corpus.sh enumerates each series' specs over FTP and skips any that don't exist.")
	listDrift := flag.Bool("list-drift", false, "DIAGNOSTIC: instead of the matrix, print TSV of every (spec,release) whose site version is newer than the index — spec_id<TAB>release<TAB>site_version<TAB>indexed_version<TAB>state (missing|stale). This is the perpetual delta the build never closes; use it to find specs that never index (fetch/convert failures), not to exclude them.")
	emitWL := flag.Bool("emit-worklist", false, "instead of the matrix, print the FETCH worklist '<release> <archive-url> <name>' for EVERY (spec,release) the status report lists at/above the floor — drafts (v0/v1/v2) INCLUDED. This drives corpus.sh from the same source discover diffs against, so the builder and the index agree by construction (closes the attribution gap). One line per (spec,release).")
	seriesFilter := flag.String("series", "", "with --emit-worklist: restrict to these 2-digit series (space/comma separated, e.g. '23 33'); empty = all")
	absentPath := flag.String("absent-index", "", "absent-index.json (spec|rel -> version 3GPP lists but never published as a downloadable file). A key absent at the SAME version is treated as ACCOUNTED (not re-flagged) — this is what lets the perpetual residue reach 0 actionable drift. A higher site version re-flags it (maybe now published). Accepts a COMMA-SEPARATED list of files (e.g. the genuine-absent ledger + the draft ledger), merged with the highest version winning.")
	emitDrafts := flag.Bool("emit-draft-ledger", false, "instead of the matrix, print absent-index-format JSON for every status-report key at a DRAFT version (major < 3) and an in-scope release. Merge into --absent-index so v0/v1/v2 drafts 3GPP never publishes stop re-flagging as missing, without excluding them from --emit-worklist.")
	flag.Parse()

	site, err := fetchStatus(*statusURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "discover:", err)
		os.Exit(1)
	}
	idx := loadIndex(*indexPath)
	full := *all || len(idx) == 0
	floorMajor := major(*floor)

	// "have" = what needs no fetch: a key is covered when it is INDEXED at the site
	// version OR LEDGERED-ABSENT at the site version (3GPP never published it). The
	// drift/worklist compare against `have`, so genuinely-absent specs stop being
	// re-flagged every run — the residue the user saw as eternal FAILDL/drift. A
	// NEWER site version still re-flags (it may have been published since).
	absent := loadMergedIndex(*absentPath)
	have := make(map[string]string, len(idx)+len(absent))
	for k, v := range idx {
		have[k] = v
	}
	for k, v := range absent {
		if cmpVer(v, have[k]) > 0 {
			have[k] = v
		}
	}

	// Diagnostic dump: every key the site has newer/new than the index, split into
	// missing (never indexed) vs stale (indexed at an older version). This is the
	// raw material for fixing the chronic gap — emitted, never silently dropped.
	if *listDrift {
		dumpDrift(site, have, floorMajor)
		return
	}

	// Fetch worklist (the FIX for the chronic gap): emit one line per (spec,release)
	// the status report lists at/above the floor, INCLUDING drafts. corpus.sh
	// consumes this instead of guessing versions from the archive listing + the
	// version-major, so the builder fetches exactly what discover diffs against.
	if *emitWL {
		emitWorklist(site, floorMajor, *seriesFilter)
		return
	}

	// Draft ledger (C1): emit the v0/v1/v2 draft keys so they can be merged into
	// --absent-index and stop re-flagging. Offline, deterministic.
	if *emitDrafts {
		emitDraftLedger(site, floorMajor, *seriesFilter)
		return
	}

	series := deltaSeries(site, have, floorMajor, full)

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

	// Build-identity delta (plan PR-3 + PR-7): a parser/chunking/schema change
	// (SpecIngestIdentity), a model change (EmbedIdentity), or a global-enricher
	// change (GlobalEnrichmentIdentity — catalog overlay code, OpenAPI extraction,
	// or the evolutions seed) moves NO spec version and NO subject footprint, so the
	// checks above are blind to it. Compare the published build-index against the
	// current code's identities; any of the three drifts is corpus-global, so force
	// EVERY site series above the floor into the matrix (a spec-ingest/embed drift
	// re-ingests/re-embeds; a global-enrichment drift makes the merge job run so its
	// idempotent overlays + the authoritative evolutions reseed refresh).
	var identityDrift []string
	if !full && *buildIndexPath != "" {
		published := loadBuildIndex(*buildIndexPath)
		// Resolve the embedder FAMILY (e.g. "bge-m3") to the canonical ModelID the
		// real backend would report (the full EmbedIdentity digest), so the drift
		// compare keys on the SAME identity merge stamps and serve checks — a weight/
		// tokenizer/dim/precision bump then forces a re-embed even though no spec
		// version moved (finding model-change-needs-full-flag-not-auto-detected).
		current := model.CurrentBuildIndex(
			subjectmeta.IngestFootprints(), subjectmeta.ASN1ScannerVersion,
			embed.ResolveModelID(*expectModel),
			enrichmeta.Current(),
		)
		identityDrift = published.Differs(current)
		forceAll := false
		for _, d := range identityDrift {
			// A spec-ingest or embed drift is per-spec/per-vector (re-ingest /
			// re-embed). A global-enrichment drift (catalog overlay code, OpenAPI
			// extraction, or the evolutions seed — PR-7) is corpus-global and runs in
			// the merge job, which is gated on a non-empty matrix; forcing every
			// above-floor series in makes merge run so the idempotent overlays + the
			// authoritative evolutions reseed refresh the published DB even though no
			// spec version moved (catalog-source-change-not-detected,
			// openapi-not-in-any-footprint, evolutions-seed-never-propagates-in-delta).
			if d == "spec_ingest_identity" || d == "embed_identity" || d == "global_enrichment_identity" {
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

	// Legacy GSM Phase-1/2 (§0 opt-in): add the 2-digit GSM series explicitly. They
	// carry 4-digit specs that the modern status report omits, and their releases are
	// below the floor, so deltaSeries never surfaces them. corpus.sh enumerates each
	// series over FTP and a non-existent one yields an empty (no-op) shard, so adding
	// the whole documented GSM range is safe. The ingest stamps release="GSM" (below
	// any embed floor → lexical-only, never vectorised).
	if *includeLegacy {
		for _, s := range legacyGSMSeries {
			series[s] = true
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
	fmt.Fprintf(os.Stderr, "discover: mode=%s site_keys=%d indexed=%d absent-ledger=%d subject-changed=%v identity-drift=%v -> %d series: %v\n",
		mode, len(site), len(idx), len(absent), subjChanged, identityDrift, len(out), out)
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

// dumpDrift prints, per (spec,release) where the site version is newer than the
// index, a TSV row "spec<TAB>release<TAB>site_ver<TAB>idx_ver<TAB>state". state is
// "missing" when the key was never indexed (idx==""), "stale" when it is indexed
// at an older version. The trailing stderr summary counts each state and the
// affected series. This is the perpetual delta the build re-flags every run; the
// rows are the work-list for closing it (fetch/convert fixes), not an exclude-list.
func dumpDrift(site, idx map[string]string, floorMajor int) {
	type entry struct{ spec, rel, sv, iv, state string }
	var es []entry
	missing, stale := 0, 0
	series := map[string]bool{}
	for key, ver := range site {
		spec, rel := splitKey(key)
		if major(rel) < floorMajor {
			continue
		}
		iv := idx[key]
		if cmpVer(ver, iv) <= 0 {
			continue
		}
		state := "stale"
		if iv == "" {
			state = "missing"
			missing++
		} else {
			stale++
		}
		series[spec[:2]] = true
		es = append(es, entry{spec, rel, ver, iv, state})
	}
	sort.Slice(es, func(i, j int) bool {
		if es[i].spec != es[j].spec {
			return es[i].spec < es[j].spec
		}
		return es[i].rel < es[j].rel
	})
	for _, e := range es {
		fmt.Printf("%s\t%s\t%s\t%s\t%s\n", e.spec, e.rel, e.sv, e.iv, e.state)
	}
	fmt.Fprintf(os.Stderr, "drift: total=%d missing=%d stale=%d series=%d\n",
		len(es), missing, stale, len(series))
}

// statusBase is the 3GPP archive root; the per-spec directory + version-coded zip
// name reconstruct the exact download URL corpus.sh's process_spec already fetches.
const statusBase = "https://www.3gpp.org/ftp/Specs/archive"

// emitWorklist prints the fetch worklist "<release> <url> <name>" for every
// (spec,release) the status report lists at/above the floor (by RELEASE, not by
// version-major — so v0/v1/v2 drafts of an in-scope release are kept). The version
// is encoded back to the 3-char archive code (X.Y.Z -> base36 each). A version
// whose component exceeds 35 (un-encodable in the 3-char scheme) is counted and
// skipped — it falls to the small fetch/convert residual, never silently dropped.
func emitWorklist(site map[string]string, floorMajor int, seriesFilter string) {
	allow := seriesSet(seriesFilter) // nil => all series
	keys := make([]string, 0, len(site))
	for k := range site {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	n, skipped := 0, 0
	for _, key := range keys {
		spec, rel := splitKey(key)
		if rel == "" || len(spec) < 2 || major(rel) < floorMajor {
			continue
		}
		if allow != nil && !allow[spec[:2]] {
			continue
		}
		code, ok := encodeVerCode(site[key])
		if !ok {
			// A version component > 35 has no single-char code in the 3GPP 3-char
			// scheme — 3GPP names these files with an irregular code, so we cannot
			// reconstruct the URL. Logged (never silently dropped) so the residual
			// is auditable; the build's archive-dir fallback still picks them up by
			// listing (they exist, just not at a code we can compute).
			fmt.Fprintf(os.Stderr, "emit-worklist SKIP-unencodable %s %s\n", key, site[key])
			skipped++
			continue
		}
		num := strings.Replace(spec, ".", "", 1)
		name := num + "-" + code + ".zip"
		url := statusBase + "/" + spec[:2] + "_series/" + spec + "/" + name
		fmt.Printf("%s %s %s\n", rel, url, name)
		n++
	}
	fmt.Fprintf(os.Stderr, "emit-worklist: %d entries (%d un-encodable versions skipped)\n", n, skipped)
}

// encodeVerCode turns "X.Y.Z" into the 3GPP archive's 3-char version code, where
// each component 0..35 maps to '0'..'9','a'..'z' (the inverse of corpus.sh's
// decode_char). Missing components default to 0. Returns false if a component is
// not a number or exceeds 35 (outside the single-char encoding).
func encodeVerCode(ver string) (string, bool) {
	parts := strings.SplitN(ver, ".", 3)
	var n [3]int
	for i := 0; i < 3; i++ {
		if i < len(parts) && parts[i] != "" {
			v, err := strconv.Atoi(parts[i])
			if err != nil || v < 0 {
				return "", false
			}
			n[i] = v
		}
	}
	// 3GPP uses the compact 3-char base36 code (one char per component, 0..35)
	// when EVERY component fits; otherwise the 6-DIGIT zero-padded decimal form,
	// two digits per component (8.37.0 → "083700", 1.62.0 → "016200"). The latter
	// is what high-minor maintenance versions use — we must emit it or those specs
	// (24.229, 30.531, …) are never fetched.
	if n[0] <= 35 && n[1] <= 35 && n[2] <= 35 {
		var code [3]byte
		for i, v := range n {
			if v < 10 {
				code[i] = byte('0' + v)
			} else {
				code[i] = byte('a' + v - 10)
			}
		}
		return string(code[:]), true
	}
	if n[0] <= 99 && n[1] <= 99 && n[2] <= 99 {
		return fmt.Sprintf("%02d%02d%02d", n[0], n[1], n[2]), true
	}
	return "", false // a component ≥ 100 — outside even the 6-digit scheme (vanishingly rare)
}

// seriesSet parses a "23 33" / "23,33" / "24|Rel-19" filter into a set of 2-digit
// series prefixes. Empty input returns nil (= no filter, all series).
func seriesSet(filter string) map[string]bool {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return nil
	}
	set := map[string]bool{}
	for _, tok := range strings.FieldsFunc(filter, func(r rune) bool { return r == ' ' || r == ',' }) {
		if i := strings.IndexByte(tok, '|'); i >= 0 { // "24|Rel-19" -> "24"
			tok = tok[:i]
		}
		if len(tok) >= 2 {
			set[tok[:2]] = true
		}
	}
	if len(set) == 0 {
		return nil
	}
	return set
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

// loadMergedIndex loads one or more comma-separated ledger files, merging them
// keyed "spec|Rel" with the HIGHEST version winning (the same "newer re-flags"
// rule used for `have`). Lets the genuine-absent ledger (from verify-coverage)
// and the draft ledger (from --emit-draft-ledger) be passed together as one
// --absent-index without a separate merge step. A missing file is skipped.
func loadMergedIndex(spec string) map[string]string {
	out := map[string]string{}
	for _, p := range strings.Split(spec, ",") {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		for k, v := range loadIndex(p) {
			if cmpVer(v, out[k]) > 0 {
				out[k] = v
			}
		}
	}
	return out
}

// emitDraftLedger prints the absent-index-format JSON for every status-report key
// whose VERSION is a draft (major < 3, i.e. v0/v1/v2) at an in-scope release. 3GPP
// routinely lists a spec at a draft version it never publishes as a stable file, so
// these keys re-flag as "missing" on every run. Ledgering them at their draft
// version silences that perpetual noise WITHOUT excluding them from the fetch
// worklist (which still includes drafts — "index everything"): when 3GPP later
// freezes the spec the version major becomes >= 3, the key changes, and it re-enters
// drift naturally. Deterministic + offline (status report only; no network).
func emitDraftLedger(site map[string]string, floorMajor int, seriesFilter string) {
	allow := seriesSet(seriesFilter)
	keys := make([]string, 0, len(site))
	for k := range site {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Println("{")
	first := true
	n := 0
	for _, key := range keys {
		spec, rel := splitKey(key)
		if rel == "" || len(spec) < 2 || major(rel) < floorMajor {
			continue
		}
		if allow != nil && !allow[spec[:2]] {
			continue
		}
		if major(site[key]) >= 3 { // not a draft
			continue
		}
		if !first {
			fmt.Println(",")
		}
		first = false
		fmt.Printf("  %q: %q", key, site[key])
		n++
	}
	if !first {
		fmt.Println()
	}
	fmt.Println("}")
	fmt.Fprintf(os.Stderr, "draft-ledger: %d draft keys (version major < 3) at release-major >= %d\n", n, floorMajor)
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
