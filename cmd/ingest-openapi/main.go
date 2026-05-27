// Command ingest-openapi loads the 5GC OpenAPI corpus (the canonical YAML from
// 3GPP Forge, fetched by scripts/fetch-5g-apis.sh) into an existing DuckDB
// snapshot. It is ADDITIVE to the HTML ingest (cmd/ingest): it clears only the
// api_* tables, never clauses — run it after cmd/ingest. See axis #2.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/kodflow/3gpp-mcp/internal/openapi"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

var Version = "dev"

func main() {
	var (
		src     = flag.String("src", "data/sources/5g-apis", "5GC OpenAPI corpus dir")
		out     = flag.String("out", "data/3gpp.duckdb", "DuckDB path (must already exist)")
		release = flag.String("release", "", "release filter, comma-separated (e.g. Rel-18,Rel-19)")
		quiet   = flag.Bool("quiet", false, "suppress per-release progress")
	)
	flag.Parse()

	logf := func(f string, a ...any) { log.Printf(f, a...) }
	if *quiet {
		logf = func(string, ...any) {}
	}

	db, err := store.Open(*out)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open %s: %v\n", *out, err)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()

	start := time.Now()
	st, err := openapi.IngestDir(context.Background(), db, *src, splitCSV(*release), logf)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ingest-openapi failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("ingest-openapi done in %s (version=%s)\n", time.Since(start).Round(time.Millisecond), Version)
	fmt.Printf("  db=%s\n  releases=%d files=%d operations=%d schemas=%d\n",
		*out, st.Releases, st.Files, st.Operations, st.Schemas)
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
