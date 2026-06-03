// Command split partitions a multi-release DuckDB snapshot into ONE DB per 3GPP
// release — the inverse of cmd/merge. It is the publishing granularity decided for
// the corpus: every 3GPP release (Phase1, Phase2, Rel-99, Rel-4 … Rel-20) becomes
// its own GitHub Release asset, and the image-build CI later pulls them all back and
// re-merges into the full corpus.
//
//	split --db lotA.duckdb --out-dir dist [--zstd]
//
// For each distinct release found in `clauses`, an output DB is created with the
// canonical schema (store.Open) and every source table is copied into it:
//   - tables that HAVE a `release` column (clauses, spec_versions, …) are FILTERED
//     to that release (so a Rel-18 shard describes only Rel-18, and — crucially —
//     carries only Rel-18's embedding-bearing clauses, the bulk of the bytes);
//   - global tables (specs, acronyms, evolutions, asn1_types, …) are copied WHOLE
//     and dedup on the re-merge (their natural-key ON CONFLICT / authoritative
//     reseed already handle the repetition across shards).
//
// No FTS or HNSW index is built: per-shard indexes are not concatenable and
// cmd/merge rebuilds both on the union. Skipping them also keeps each per-release
// asset slim enough to stay under the 2 GB GitHub Release asset cap (the largest
// release, Rel-19 ≈ 1.14 GB of fp16-origin FLOAT[1024] vectors, fits once the
// index overhead is dropped).
//
// Output is disjoint by release, so the set of emitted DBs is exactly a valid
// shard set for `merge --out full.duckdb dist/*.duckdb`.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/kodflow/3gpp-mcp/internal/store"
)

var Version = "dev"

// releaseBearingTables are copied with a WHERE release = ? filter; everything else
// in the canonical schema is copied whole. We detect the `release` column at
// runtime (DESCRIBE) so the list is advisory — any table that grows a release
// column is filtered automatically — but it documents intent and fold order.
var allTables = []string{
	"specs", "spec_versions", "acronyms", "li_events", "li_event_fields",
	"li_nf_clauses", "asn1_types", "releases", "changes", "clauses",
	"api_operations", "api_schemas", "evolutions",
}

// safeRelease guards against SQL injection / bad filenames: release labels are
// model-controlled (Rel-NN, Phase1, …) but we validate before interpolating into
// the ATTACH-free COPY statements and the output path.
var safeRelease = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func main() {
	dbPath := flag.String("db", "", "source multi-release DuckDB to split (required)")
	outDir := flag.String("out-dir", "dist", "directory to write one 3gpp-<release>.duckdb per release")
	zstd := flag.Bool("zstd", false, "also write a zstd -19 --long=27 compressed sidecar per output DB")
	flag.Parse()
	if *dbPath == "" {
		fmt.Fprintln(os.Stderr, "split: --db is required")
		os.Exit(2)
	}
	if err := run(context.Background(), *dbPath, *outDir, *zstd); err != nil {
		fmt.Fprintln(os.Stderr, "split:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, dbPath, outDir string, zstd bool) error {
	if _, err := os.Stat(dbPath); err != nil {
		return fmt.Errorf("source --db %s: %w", dbPath, err)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	releases, err := distinctReleases(ctx, dbPath)
	if err != nil {
		return err
	}
	if len(releases) == 0 {
		return fmt.Errorf("no releases found in %s (empty clauses table?)", dbPath)
	}
	fmt.Fprintf(os.Stderr, "split: %s → %d release(s): %s\n", dbPath, len(releases), strings.Join(releases, ", "))

	for _, rel := range releases {
		out := filepath.Join(outDir, "3gpp-"+rel+".duckdb")
		n, err := splitOne(ctx, dbPath, out, rel)
		if err != nil {
			return fmt.Errorf("release %s: %w", rel, err)
		}
		fmt.Fprintf(os.Stderr, "split: %s → %s (%d clauses)\n", rel, out, n)
		if zstd {
			if err := zstdSidecar(out); err != nil {
				return fmt.Errorf("zstd %s: %w", out, err)
			}
		}
	}
	return nil
}

// distinctReleases reads the release labels present in the source clauses table.
func distinctReleases(ctx context.Context, dbPath string) ([]string, error) {
	db, err := store.Open(dbPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	rows, err := db.DB().QueryContext(ctx, `SELECT DISTINCT release FROM clauses WHERE release IS NOT NULL AND release <> '' ORDER BY release`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			return nil, err
		}
		if !safeRelease.MatchString(r) {
			return nil, fmt.Errorf("unsafe release label %q", r)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// splitOne writes a single per-release DB and returns its clause count.
func splitOne(ctx context.Context, srcPath, outPath, rel string) (int64, error) {
	_ = os.Remove(outPath)
	db, err := store.Open(outPath) // creates the canonical empty schema
	if err != nil {
		return 0, err
	}
	defer func() { _ = db.Close() }()
	sqldb := db.DB()

	// ATTACH takes no bind params; srcPath is operator-provided and rel is validated.
	attach := "ATTACH '" + strings.ReplaceAll(srcPath, "'", "''") + "' AS src (READ_ONLY)"
	if _, err := sqldb.ExecContext(ctx, attach); err != nil {
		return 0, err
	}
	defer func() { _, _ = sqldb.ExecContext(ctx, "DETACH src") }()

	for _, t := range allTables {
		if err := copyTable(ctx, sqldb, t, rel); err != nil {
			return 0, fmt.Errorf("copy %s: %w", t, err)
		}
	}

	var n int64
	if err := sqldb.QueryRowContext(ctx, `SELECT count(*) FROM clauses`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// copyTable copies src.<t> into the output, filtered by release when the table has
// a `release` column, whole otherwise. Columns are intersected (src ∩ dst) so an
// older source schema still copies cleanly.
func copyTable(ctx context.Context, sqldb *sql.DB, t, rel string) error {
	dstCols, err := cols(ctx, sqldb, t)
	if err != nil {
		return err
	}
	srcCols, err := cols(ctx, sqldb, "src."+t)
	if err != nil {
		return nil // source lacks this table — nothing to copy
	}
	srcSet := map[string]bool{}
	hasRelease := false
	for _, c := range srcCols {
		srcSet[c] = true
		if c == "release" {
			hasRelease = true
		}
	}
	var common []string
	for _, c := range dstCols {
		if srcSet[c] {
			common = append(common, c)
		}
	}
	if len(common) == 0 {
		return nil
	}
	list := strings.Join(common, ",")
	q := fmt.Sprintf("INSERT INTO %s (%s) SELECT %s FROM src.%s", t, list, list, t)
	if hasRelease {
		q += " WHERE release = '" + rel + "'" // rel validated by safeRelease
	}
	_, err = sqldb.ExecContext(ctx, q)
	return err
}

// cols returns a table's column names (rel may be "clauses" or "src.clauses").
func cols(ctx context.Context, sqldb *sql.DB, rel string) ([]string, error) {
	rows, err := sqldb.QueryContext(ctx, "DESCRIBE "+rel)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	colNames, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []string
	for rows.Next() {
		cells := make([]any, len(colNames))
		ptrs := make([]any, len(colNames))
		for i := range cells {
			ptrs[i] = &cells[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		if name, ok := cells[0].(string); ok {
			out = append(out, name)
		}
	}
	sort.Strings(out) // deterministic; order is irrelevant for an explicit column list
	return out, rows.Err()
}

// zstdSidecar writes <out>.zst next to the DB using the same recipe as the corpus CI.
func zstdSidecar(out string) error {
	cmd := exec.Command("zstd", "-19", "-T0", "--long=27", "-f", out, "-o", out+".zst")
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
