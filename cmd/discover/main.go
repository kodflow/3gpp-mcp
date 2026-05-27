// Command discover decides WHICH 3GPP series need (re)indexing, so the CI
// matrix is sized to the actual delta instead of being frozen or always-full.
//
// It fetches the 3GPP global status report (one ~5 MB GET that lists the latest
// version of every spec), compares each spec's site version against a small
// corpus-index.json (spec_id -> indexed version, published alongside the DB),
// and prints a JSON array of the series that changed — for a GitHub Actions
// matrix (fromJSON). No index (first run) or --all => every series (full build).
//
//	discover --index corpus-index.json --floor Rel-4    # delta
//	discover --all                                       # full (all series)
//
// Pure Go (no CGO): the heavy DuckDB work stays in ingest/merge. 3gpp.org needs
// a browser User-Agent (it 403s bots), so we send one.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// One status-report row: <td>TS|TR</td><td><a..>NN.NNN[-p]</a></td><td>title</td><td>X.Y.Z</td>...
var rowRE = regexp.MustCompile(`(?s)<td>(?:TS|TR)</td>\s*<td><a[^>]*>(\d{2}\.\d{3})(?:-\d+)?</a></td>\s*<td>.*?</td>\s*<td>(\d+\.\d+\.\d+)</td>`)

func main() {
	statusURL := flag.String("status-url", "https://www.3gpp.org/DynaReport/status-report.htm", "3GPP global status report")
	indexPath := flag.String("index", "", "corpus-index.json (spec_id -> indexed version); empty/missing => full")
	floor := flag.String("floor", "Rel-4", "lowest release to consider (drops drafts/ancient below this major)")
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

	series := map[string]bool{}
	for spec, ver := range site {
		if major(ver) < floorMajor {
			continue // draft / below floor — not indexed
		}
		if full || cmpVer(ver, idx[spec]) > 0 { // missing in index => idx[spec]=="" => changed
			series[spec[:2]] = true
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
	fmt.Fprintf(os.Stderr, "discover: mode=%s site_specs=%d indexed=%d -> %d series: %v\n",
		mode, len(site), len(idx), len(out), out)
	b, _ := json.Marshal(out)
	fmt.Println(string(b)) // stdout = the JSON matrix
}

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
	out := map[string]string{}
	for _, m := range rowRE.FindAllStringSubmatch(string(body), -1) {
		spec, ver := m[1], m[2]
		if cmpVer(ver, out[spec]) > 0 { // keep the highest version per spec
			out[spec] = ver
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("parsed 0 specs from status report (layout changed?)")
	}
	return out, nil
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

// major returns the leading integer of "Rel-19" or "19.6.0" (0 on parse error).
func major(s string) int {
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
