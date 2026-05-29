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
		embFlr  = flag.String("embed-floor", "", "embed ONLY clauses at/above this release (e.g. Rel-15); empty = embed all. Lexical coverage is unaffected.")
		reqSem  = flag.Bool("require-semantic", false, "fail (exit 1) if the embedder is not enabled (also honours SEMANTIC_REQUIRED=1) — guards against publishing a NULL-vector DB")
		report  = flag.String("report", "text", "end-of-run summary format: text | json")
		resume  = flag.Bool("resume", false, "resume into the existing --out DB: skip (spec,version) tuples whose ingest_log row is 'done' under the current pipeline_version; purge + redo 'started' rows; wipe stale-pipeline rows first")
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
		EmbedFloor: *embFlr,
		Embedder:   embed.New(),
		Logf:       logf,
		Resume:     *resume,
	}

	// Hard contract: when semantic is required, refuse to silently produce a
	// NULL-vector DB (e.g. a non-onnx build or a missing model degraded to Disabled).
	if (*reqSem || os.Getenv("SEMANTIC_REQUIRED") == "1") && !opt.Embedder.Enabled() {
		fmt.Fprintln(os.Stderr, "semantic required but the embedder is disabled "+
			"(need a -tags onnx build + model, or EMBEDDER=local) — refusing to write a NULL-vector DB")
		os.Exit(1)
	}

	start := time.Now()
	st, err := ingest.Run(context.Background(), *out, opt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ingest failed: %v\n", err)
		os.Exit(1)
	}
	if *report == "json" {
		// Machine summary for CI gates (e.g. jq '.model=="bge-m3" and .embedded_clauses>0').
		// null_embeddings is the TOTAL null count — with --embed-floor it legitimately
		// includes below-floor clauses, so gate on embedded_clauses>0 + model, not null==0.
		nullEmb := 0
		if rdb, e := store.OpenReadOnly(*out); e == nil {
			nullEmb, _ = rdb.CountNullEmbeddings(context.Background())
			_ = rdb.Close()
		}
		rep := map[string]any{
			"embedder_enabled": opt.Embedder.Enabled(),
			"model":            opt.Embedder.ModelID(),
			"clauses":          st.Clauses,
			"embedded_clauses": st.Clauses - nullEmb,
			"null_embeddings":  nullEmb,
			"fts":              st.FTS,
			"hnsw":             st.HNSW,
			"version":          Version,
		}
		b, _ := json.MarshalIndent(rep, "", "  ")
		fmt.Println(string(b))
		return
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
