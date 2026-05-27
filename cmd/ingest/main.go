// Command ingest builds the deterministic DuckDB snapshot from the
// LibreOffice-converted 3GPP HTML corpus (data/sources/convert).
//
// See CLAUDE.md §6 for the pipeline contract. The query server (cmd/server)
// stays pure-Go; LibreOffice + this ingest run are the only offline steps.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/kodflow/3gpp-mcp/internal/embed"
	"github.com/kodflow/3gpp-mcp/internal/ingest"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

var Version = "dev"

func main() {
	var (
		convert = flag.String("convert", "data/sources/convert", "converted-HTML corpus dir")
		origin  = flag.String("origin", "", "origin zips dir (ASN.1 attachments); empty = derive from -convert")
		out     = flag.String("out", "data/3gpp.duckdb", "output DuckDB path")
		release = flag.String("release", "", "release filter, comma-separated (e.g. Rel-18,Rel-19)")
		series  = flag.String("series", "", "series filter, comma-separated (e.g. 23,33)")
		spec    = flag.String("spec", "", "spec-id filter, comma-separated (e.g. 33.128,33.127)")
		parser  = flag.String("parser", "html", "spec parser: html (LibreOffice) | ooxml (direct .docx)")
		fts     = flag.Bool("fts", true, "build the BM25 FTS index after load")
		quiet   = flag.Bool("quiet", false, "suppress per-spec progress")
		count   = flag.Bool("count-only", false, "Phase-0: open -out read-only, print clause embedded/null counts by series as JSON, then exit")
	)
	flag.Parse()

	if *count {
		if err := runCountOnly(*out); err != nil {
			fmt.Fprintf(os.Stderr, "count-only: %v\n", err)
			os.Exit(1)
		}
		return
	}

	logf := func(f string, a ...any) { log.Printf(f, a...) }
	if *quiet {
		logf = func(string, ...any) {}
	}

	opt := ingest.Options{
		ConvertDir: *convert,
		OriginDir:  *origin,
		Parser:     *parser,
		Releases:   splitCSV(*release),
		Series:     splitCSV(*series),
		SpecIDs:    splitCSV(*spec),
		EnableFTS:  *fts,
		Embedder:   embed.New(),
		Logf:       logf,
	}

	start := time.Now()
	st, err := ingest.Run(context.Background(), *out, opt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ingest failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("ingest done in %s (version=%s)\n", time.Since(start).Round(time.Millisecond), Version)
	fmt.Printf("  db=%s\n  specs=%d versions=%d clauses=%d changes=%d evolutions=%d degraded=%d\n",
		*out, st.Specs, st.Versions, st.Clauses, st.Changes, st.Evolutions, st.Degraded)
	fmt.Printf("  subjects=%v\n", st.SubjectAdded)
	fmt.Printf("  fts=%v vectors=%v hnsw=%v embedder=%v\n", st.FTS, st.Vectors, st.HNSW, opt.Embedder.Enabled())
}

// runCountOnly opens the built DB read-only and prints, as JSON, the total /
// embedded / null clause counts plus a per-(series,release) breakdown. Phase-0
// uses it to size sub-sharding (clauses per series) and to verify how many
// clauses a real-shard dry-run actually vectorised. Read-only: no product change.
func runCountOnly(dbPath string) error {
	st, err := store.OpenReadOnly(dbPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", dbPath, err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()

	type bucket struct {
		Series  string `json:"series"`
		Release string `json:"release"`
		Clauses int    `json:"clauses"`
		Nulls   int    `json:"null_embeddings"`
	}
	report := struct {
		DB       string   `json:"db"`
		Clauses  int      `json:"clauses"`
		Embedded int      `json:"embedded_clauses"`
		Nulls    int      `json:"null_embeddings"`
		BySeries []bucket `json:"by_series_release"`
	}{DB: dbPath}

	if err := st.DB().QueryRowContext(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE embedding IS NOT NULL),
		       count(*) FILTER (WHERE embedding IS NULL)
		FROM clauses`).Scan(&report.Clauses, &report.Embedded, &report.Nulls); err != nil {
		return fmt.Errorf("count clauses: %w", err)
	}

	rows, err := st.DB().QueryContext(ctx, `
		SELECT substr(spec_id,1,2) AS series, release,
		       count(*) AS n,
		       count(*) FILTER (WHERE embedding IS NULL) AS nulls
		FROM clauses
		GROUP BY 1,2 ORDER BY n DESC`)
	if err != nil {
		return fmt.Errorf("breakdown: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var b bucket
		if err := rows.Scan(&b.Series, &b.Release, &b.Clauses, &b.Nulls); err != nil {
			return err
		}
		report.BySeries = append(report.BySeries, b)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
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
