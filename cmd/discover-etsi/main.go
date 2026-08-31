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
	"context"
	"crypto/tls"
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
	"github.com/kodflow/3gpp-mcp/internal/retry"
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
	allFlag := flag.Bool("all", false, "enumerate the WHOLE ETSI /deliver corpus (etsi_ts+etsi_tr+etsi_en) — the latest PUBLISHED version of EVERY deliverable, not just the LI suite. Tens of thousands of specs; pair with --report worklist + a chunked CI matrix.")
	typeDirsFlag := flag.String("type-dirs", strings.Join(etsicat.DeliverTypeDirs, ","), "with --all: which /deliver document-type folders to crawl (comma/space-separated)")
	withRepub := flag.Bool("include-3gpp-republications", false, "with --all: also index ETSI's republications of 3GPP specs (121 000-138 999, 141 000-155 999). Off by default: the 3GPP half of this corpus already holds those, in EVERY release, while ETSI publishes one version of each")
	timeout := flag.Duration("timeout", 30*time.Second, "per-request HTTP timeout")
	flag.Parse()

	// Force HTTP/1.1: the ETSI CDN intermittently returns "HTTP2 framing layer"
	// errors under the thousands of requests an --all crawl makes — disabling h2
	// (empty TLSNextProto + ForceAttemptHTTP2=false) sidesteps it. Each GET is
	// retried with backoff so a transient hiccup mid-crawl never aborts the run.
	client := &http.Client{
		Timeout: *timeout,
		Transport: &http.Transport{
			ForceAttemptHTTP2: false,
			TLSNextProto:      map[string]func(string, *tls.Conn) http.RoundTripper{},
		},
	}
	fetch := func(url string) (io.ReadCloser, error) {
		var body io.ReadCloser
		err := retry.Do(context.Background(), 5, time.Second, 20*time.Second, func() error {
			req, rerr := http.NewRequest(http.MethodGet, url, nil)
			if rerr != nil {
				return rerr
			}
			// A browser UA: some ETSI endpoints 403 a bare Go client.
			req.Header.Set("User-Agent", "Mozilla/5.0 (3gpp-mcp discover-etsi)")
			resp, derr := client.Do(req)
			if derr != nil {
				return derr
			}
			if resp.StatusCode != http.StatusOK {
				_ = resp.Body.Close()
				return fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
			}
			body = resp.Body
			return nil
		})
		return body, err
	}

	// Scope: --all enumerates the WHOLE /deliver corpus (the 3GPP-parity completeness:
	// latest published version of every deliverable); --specs scopes explicitly; else
	// the built-in LI suite.
	// Every scoped deliverable carries the /deliver folder it lives in. --specs and
	// the built-in LI suite are TS by construction; --all learns each one's folder
	// from the crawl that found it, which is the only place that knowledge exists.
	var deliverables []etsicat.Deliverable
	switch {
	case *allFlag:
		var enumFailed []string
		dirs := splitList(*typeDirsFlag)
		perDir := map[string]int{}
		for _, td := range dirs {
			ds, f := etsicat.EnumerateDeliverables(fetch, td)
			deliverables = append(deliverables, ds...)
			perDir[td] = len(ds)
			enumFailed = append(enumFailed, f...)
		}
		fmt.Fprintf(os.Stderr, "discover-etsi: enumerated %d deliverable(s) across %v %v (%d range-fetch failures)\n",
			len(deliverables), dirs, perDir, len(enumFailed))
		if len(deliverables) == 0 {
			fmt.Fprintln(os.Stderr, "discover-etsi: FATAL --all enumerated 0 deliverables — crawl broken")
			os.Exit(1)
		}
		// ETSI's republications of 3GPP specs are dropped unless asked for, and the
		// count is printed either way: a scope decision that changes a third of the
		// corpus must be visible in the log, not inferred from a total.
		kept := deliverables[:0]
		repub := 0
		for _, d := range deliverables {
			if !*withRepub && etsicat.ThreeGPPRepublication(d.ID) {
				repub++
				continue
			}
			kept = append(kept, d)
		}
		deliverables = kept
		if *withRepub {
			fmt.Fprintln(os.Stderr, "discover-etsi: including ETSI's republications of 3GPP specs (--include-3gpp-republications)")
		} else {
			fmt.Fprintf(os.Stderr, "discover-etsi: %d ETSI-own deliverable(s); skipped %d republication(s) of 3GPP specs, "+
				"which the 3GPP corpus already holds in every release (pass --include-3gpp-republications to index them anyway)\n",
				len(deliverables), repub)
		}
	default:
		specs := defaultLISpecs
		if len(splitList(*specsFlag)) > 0 {
			specs = splitList(*specsFlag)
		}
		for _, id := range specs {
			deliverables = append(deliverables, etsicat.Deliverable{TypeDir: model.EtsiTypeTS, ID: id})
		}
	}

	// id -> folder, so the work list can name the right tree and the right file
	// prefix. Without it every emitted URL was an etsi_ts one, and a TR's etsi_ts
	// URL is a 404 rather than a redirect.
	typeOf := make(map[string]string, len(deliverables))
	for _, d := range deliverables {
		typeOf[d.ID] = d.TypeDir
	}
	specs := make([]string, 0, len(deliverables))
	for _, d := range deliverables {
		specs = append(specs, d.ID)
	}

	site, failed := etsicat.BuildSiteIn(fetch, deliverables)
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
			td := typeOf[id]
			if td == "" {
				td = model.EtsiTypeTS
			}
			// Fourth column: the document type ("TS"/"TR"/"EN"). scripts/etsi-corpus.sh
			// puts it in the provenance header so the corpus can call a TR a TR
			// instead of filing every deliverable as "ETSI TS". A reader of an older
			// three-column list still parses (the field is simply empty) and the
			// parser defaults to TS, which is what those lists all were.
			fmt.Printf("%s\t%s\t%s\t%s\n", id, model.EtsiDeliverURLIn(td, id, site[id]), site[id], docTypeOf(td))
		}
		return
	}
	// matrix: JSON array of changed ids (the CI fans out one shard per id/group).
	b, _ := json.Marshal(changed)
	fmt.Printf("matrix=%s\n", string(b))
}

// docTypeOf renders a /deliver folder as the document type the corpus labels a
// deliverable with: etsi_ts -> "TS". Unknown folders fall back to TS, which is what
// the pipeline assumed unconditionally before the type was carried at all.
func docTypeOf(typeDir string) string {
	switch typeDir {
	case model.EtsiTypeTR:
		return "TR"
	case model.EtsiTypeEN:
		return "EN"
	default:
		return "TS"
	}
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
