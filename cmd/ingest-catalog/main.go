// Command ingest-catalog overlays authoritative 3GPP metadata from the
// DynaReport HTML reports onto an existing DuckDB snapshot (axis #3): real
// per-spec working group + title + TS/TR, and release freeze_date/status (the
// non-monotonic-version ordering fix). It is ADDITIVE — run it after cmd/ingest;
// it only updates specs/spec_versions rows that already exist on disk.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kodflow/3gpp-mcp/internal/catalog"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

var Version = "dev"

func main() {
	var (
		out     = flag.String("out", "data/3gpp.duckdb", "DuckDB path (must already exist)")
		cache   = flag.String("cache", "data/catalog", "dir to cache/read the report HTML")
		offline = flag.Bool("offline", false, "parse cached HTML only (no network)")
		quiet   = flag.Bool("quiet", false, "suppress progress")
	)
	flag.Parse()

	logf := func(f string, a ...any) { log.Printf(f, a...) }
	if *quiet {
		logf = func(string, ...any) {}
	}

	statusHTML, err := load("status-report.htm", catalog.StatusReportURL, *cache, *offline)
	if err != nil {
		fmt.Fprintf(os.Stderr, "status-report: %v\n", err)
		os.Exit(1)
	}
	releasesHTML, err := load("releases.htm", catalog.ReleasesURL, *cache, *offline)
	if err != nil {
		fmt.Fprintf(os.Stderr, "releases: %v\n", err)
		os.Exit(1)
	}

	specs, _, err := catalog.ParseStatusReport(bytes.NewReader(statusHTML))
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse status-report: %v\n", err)
		os.Exit(1)
	}
	releases, err := catalog.ParseReleases(bytes.NewReader(releasesHTML))
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse releases: %v\n", err)
		os.Exit(1)
	}
	logf("parsed %d specs, %d releases", len(specs), len(releases))

	db, err := store.Open(*out)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open %s: %v\n", *out, err)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()

	// Release calendar + freeze/status overlay.
	rows := make([]store.ReleaseRow, len(releases))
	for i, r := range releases {
		rows[i] = store.ReleaseRow{
			Code: r.Code, Name: r.Name, Status: r.Status,
			StartDate: r.StartDate, FreezeDate: r.FreezeDate, FreezeMeeting: r.FreezeMeeting,
		}
	}
	if err := db.UpsertReleases(rows); err != nil {
		fmt.Fprintf(os.Stderr, "upsert releases: %v\n", err)
		os.Exit(1)
	}
	stamped, err := db.ApplyReleaseFreeze(ctx, rows)
	if err != nil {
		fmt.Fprintf(os.Stderr, "apply freeze: %v\n", err)
		os.Exit(1)
	}

	// Per-spec metadata overlay (only specs present on disk). A spec the report
	// lists but the DB lacks (never ingested) is an enrich MISS — collect the ids
	// so the omission is visible in CI logs, not hidden behind a bare count.
	updated := 0
	var missed []string
	for _, s := range specs {
		ok, err := db.UpdateSpecMeta(ctx, s.SpecID, s.Title, s.DocType, s.WorkingGroup)
		if err != nil {
			fmt.Fprintf(os.Stderr, "update %s: %v\n", s.SpecID, err)
			os.Exit(1)
		}
		if ok {
			updated++
		} else {
			missed = append(missed, s.SpecID)
		}
	}
	if err := db.SetVersionMetadataSource(ctx, "dynareport"); err != nil {
		fmt.Fprintf(os.Stderr, "set provenance: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("ingest-catalog done (version=%s)\n", Version)
	fmt.Printf("  db=%s\n  releases=%d specs_updated=%d/%d version_rows_stamped=%d specs_missed=%d\n",
		*out, len(rows), updated, len(specs), stamped, len(missed))
	if len(missed) > 0 {
		sort.Strings(missed)
		const sample = 60
		shown := missed
		if len(shown) > sample {
			shown = shown[:sample]
		}
		fmt.Fprintf(os.Stderr, "[catalog] %d report specs absent from DB (not ingested), e.g.: %s\n",
			len(missed), strings.Join(shown, " "))
	}
}

// load returns the report bytes: cached file when offline, else fetch + cache.
func load(name, url, cacheDir string, offline bool) ([]byte, error) {
	path := filepath.Join(cacheDir, name)
	if offline {
		return os.ReadFile(path)
	}
	b, err := catalog.FetchToCache(url, cacheDir, name)
	if err != nil {
		// degrade: fall back to a previously cached copy if present
		if cached, e := os.ReadFile(path); e == nil {
			log.Printf("[catalog] %s fetch failed (%v) — using cached copy", name, err)
			return cached, nil
		}
		return nil, err
	}
	return b, nil
}
