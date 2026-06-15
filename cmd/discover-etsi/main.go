// Command discover-etsi is the ETSI analogue of cmd/discover: it decides which ETSI
// deliverables to (re)fetch by crawling the deterministic /deliver directory tree and
// diffing the live PUBLISHED versions against a persisted etsi-index.json. It feeds
// the SAME shape of work-list scripts/etsi-corpus.sh consumes, so the ETSI builder and
// its index agree by construction (the property that closes 3GPP's attribution gap).
//
//	discover-etsi --emit-worklist [--specs "103 221-1,103 280,…"] [--index etsi-index.json]
//
// Scope: the LI suite (X1/X2/X3 + handover/delivery/requirements) by default, widened
// by --specs. ETSI is tens of thousands of PDFs across off-domain ranges; the scope is
// an explicit, scalable list (add ids) rather than a blind full-archive sweep that
// would never fit CI/Kaggle. All deliverables here are ETSI TS.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/kodflow/3gpp-mcp/internal/etsicat"
	"github.com/kodflow/3gpp-mcp/internal/model"
)

// defaultLISpecs is the ETSI Lawful-Interception suite the MCP cares about: the
// X1/X2/X3 base (103 221-1/-2), the common identifiers (103 280), the HI1/ADMF task
// interface (103 120), the HI2/HI3 delivery family (102 232-x), legacy handover
// (101 671) and requirements (101 331). Widen with --specs; the pipeline is otherwise
// scope-agnostic (any ETSI TS id resolves through the same deliver-archive logic).
var defaultLISpecs = []string{
	"103 221-1", "103 221-2", "103 280", "103 120",
	"102 232-1", "102 232-2", "102 232-3", "102 232-4", "102 232-5", "102 232-6", "102 232-7",
	"101 671", "101 331", "103 462",
}

func main() {
	specsFlag := flag.String("specs", "", "comma/space-separated ETSI TS ids to scope (e.g. '103 221-1,103 280'); empty = the built-in LI suite")
	indexPath := flag.String("index", "", "etsi-index.json (id -> indexed version); empty/missing => full (every scoped spec selected)")
	emitWL := flag.Bool("emit-worklist", false, "print the FETCH worklist '<id>\\t<pdf-url>\\t<version>' for every CHANGED/new scoped spec (drives etsi-corpus.sh)")
	report := flag.String("report", "matrix", "matrix (JSON array of changed ids, for the CI matrix) | worklist")
	timeout := flag.Duration("timeout", 30*time.Second, "per-request HTTP timeout")
	flag.Parse()

	specs := defaultLISpecs
	if s := splitList(*specsFlag); len(s) > 0 {
		specs = s
	}

	client := &http.Client{Timeout: *timeout}
	fetch := func(url string) (io.ReadCloser, error) {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		// A browser UA: some ETSI endpoints 403 a bare Go client.
		req.Header.Set("User-Agent", "Mozilla/5.0 (3gpp-mcp discover-etsi)")
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
		}
		return resp.Body, nil
	}

	site, failed := etsicat.BuildSite(fetch, specs)
	for _, id := range failed {
		fmt.Fprintf(os.Stderr, "discover-etsi: WARN could not resolve %q (will retry next run)\n", id)
	}
	// Loud, machine-greppable resolution summary on stderr. A green-but-empty run
	// (the absolute-href bug fixed here) MUST be visible, not silent.
	fmt.Fprintf(os.Stderr, "discover-etsi: resolved %d/%d scoped spec(s)\n", len(site), len(specs))
	// If NOTHING resolved, exit non-zero so CI fails fast instead of publishing an
	// empty corpus — a total resolution failure is a crawler regression, not "no work".
	if len(site) == 0 && len(specs) > 0 {
		fmt.Fprintln(os.Stderr, "discover-etsi: FATAL resolved 0 specs — crawl/version-resolution broken (not an empty delta)")
		os.Exit(1)
	}

	index := loadIndex(*indexPath)
	changed := etsicat.Diff(site, index)
	sort.Strings(changed)

	if *emitWL || *report == "worklist" {
		for _, id := range changed {
			fmt.Printf("%s\t%s\t%s\n", id, model.EtsiDeliverURL(id, site[id]), site[id])
		}
		return
	}
	// matrix: JSON array of changed ids (the CI fans out one shard per id/group).
	b, _ := json.Marshal(changed)
	fmt.Printf("matrix=%s\n", string(b))
}

// splitList parses a comma/space-separated flag into a trimmed, empty-dropped slice.
// Commas separate ids; an id itself may contain a single space ("103 221-1"), so we
// split on commas first, then trim — never on the intra-id space.
func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// loadIndex reads etsi-index.json (id -> version). A missing/empty/invalid file yields
// an empty index ⇒ a full build (every scoped spec is "new"), fail-safe.
func loadIndex(path string) map[string]string {
	if path == "" {
		return map[string]string{}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return map[string]string{}
	}
	var m map[string]string
	if json.Unmarshal(b, &m) != nil || m == nil {
		return map[string]string{}
	}
	return m
}
