// Command export-delta extracts the EMBEDDING WORK-LIST from a full DuckDB
// snapshot into a small, standalone "delta" DB suitable for a GPU embed run
// (e.g. on Kaggle). The delta carries ONLY the clauses that still need a vector —
// the rows WHERE embedding IS NULL — plus the spec catalogue context, and NO
// vectors / FTS / HNSW. It is the inverse companion of cmd/overlay: export-delta
// ships the text out, overlay merges the returned (chunk_id → embedding) back in.
//
//	export-delta --db data/3gpp.duckdb --out data/3gpp.delta.duckdb [--series 23] [--zstd]
//
// Why this exists: re-ingesting the corpus leaves changed/new clauses with
// embedding IS NULL (the embedding_hash skip-if-unchanged path keeps unchanged
// vectors). On a fresh lexical DB EVERY clause is NULL, so the delta is the whole
// corpus; on an incremental re-ingest it is only what moved. Sending just the
// delta to the GPU (instead of the full DB) is what makes the cycle cheap and the
// output small enough to retrieve reliably.
//
// chunk_id is the join key for the round-trip: the delta carries each clause's
// chunk_id so the embedded vectors overlay back onto the SAME rows. This is only
// valid while the source DB is not renumbered between export and overlay — guard
// the round-trip with the pipeline's generation lock.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/kodflow/3gpp-mcp/internal/store"
)

var Version = "dev"

// contextTables are copied WHOLE into the delta so a consumer can still cite a
// clause (spec title / version / freeze date) without the full catalogue. They
// are tiny next to clauses. clauses itself is copied FILTERED (embedding IS NULL).
var contextTables = []string{"specs", "spec_versions"}

// safeSeries guards the optional --series prefix before it is interpolated.
var safeSeries = regexp.MustCompile(`^[0-9]{2}$`)

func main() {
	dbPath := flag.String("db", "", "source full DuckDB to export the embedding work-list from (required)")
	out := flag.String("out", "data/3gpp.delta.duckdb", "output delta DuckDB (clauses needing embedding + catalogue)")
	series := flag.String("series", "", "optional 2-digit series filter (e.g. 23) — scope the delta to one series")
	zstd := flag.Bool("zstd", false, "also write a zstd -19 --long=27 compressed sidecar (<out>.zst)")
	flag.Parse()
	if *dbPath == "" {
		fmt.Fprintln(os.Stderr, "export-delta: --db is required")
		os.Exit(2)
	}
	if *series != "" && !safeSeries.MatchString(*series) {
		fmt.Fprintln(os.Stderr, "export-delta: --series must be 2 digits (e.g. 23)")
		os.Exit(2)
	}
	if err := run(context.Background(), *dbPath, *out, *series, *zstd); err != nil {
		fmt.Fprintln(os.Stderr, "export-delta:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, dbPath, outPath, series string, zstd bool) error {
	if _, err := os.Stat(dbPath); err != nil {
		return fmt.Errorf("source --db %s: %w", dbPath, err)
	}
	_ = os.Remove(outPath)
	db, err := store.Open(outPath) // creates the canonical empty schema
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	sqldb := db.DB()

	// ATTACH takes no bind params; dbPath is operator-provided, series is validated.
	attach := "ATTACH '" + strings.ReplaceAll(dbPath, "'", "''") + "' AS src (READ_ONLY)"
	if _, err := sqldb.ExecContext(ctx, attach); err != nil {
		return fmt.Errorf("attach source: %w", err)
	}
	defer func() { _, _ = sqldb.ExecContext(ctx, "DETACH src") }()

	// clauses: only the work-list (embedding IS NULL), optionally one series.
	where := "src.clauses.embedding IS NULL"
	if series != "" {
		where += " AND substr(src.clauses.spec_id, 1, 2) = '" + series + "'"
	}
	n, err := copyTable(ctx, sqldb, "clauses", where)
	if err != nil {
		return fmt.Errorf("copy clauses: %w", err)
	}
	for _, t := range contextTables {
		if _, err := copyTable(ctx, sqldb, t, ""); err != nil {
			return fmt.Errorf("copy %s: %w", t, err)
		}
	}

	if _, err := sqldb.ExecContext(ctx, `CHECKPOINT`); err != nil {
		return fmt.Errorf("checkpoint: %w", err)
	}
	fmt.Fprintf(os.Stderr, "export-delta: %s → %s (%d clauses needing embedding%s)\n",
		dbPath, outPath, n, seriesNote(series))

	if zstd {
		if err := zstdSidecar(outPath); err != nil {
			return fmt.Errorf("zstd %s: %w", outPath, err)
		}
		fmt.Fprintf(os.Stderr, "export-delta: wrote %s.zst\n", outPath)
	}
	return nil
}

func seriesNote(s string) string {
	if s == "" {
		return ""
	}
	return ", series " + s
}

// copyTable copies src.<t> into the output over the intersection of columns
// (src ∩ dst), so an older source schema still copies cleanly. A non-empty
// `where` is appended verbatim (callers must validate any interpolated value).
// Returns the number of rows inserted.
func copyTable(ctx context.Context, sqldb *sql.DB, t, where string) (int64, error) {
	dstCols, err := cols(ctx, sqldb, t)
	if err != nil {
		return 0, err
	}
	srcCols, err := cols(ctx, sqldb, "src."+t)
	if err != nil {
		return 0, nil // source lacks this table — nothing to copy
	}
	srcSet := map[string]bool{}
	for _, c := range srcCols {
		srcSet[c] = true
	}
	var common []string
	for _, c := range dstCols {
		if srcSet[c] {
			common = append(common, c)
		}
	}
	if len(common) == 0 {
		return 0, nil
	}
	list := strings.Join(common, ",")
	q := fmt.Sprintf("INSERT INTO %s (%s) SELECT %s FROM src.%s", t, list, list, t)
	if where != "" {
		q += " WHERE " + where
	}
	res, err := sqldb.ExecContext(ctx, q)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// cols returns a table's column names (name may be "clauses" or "src.clauses").
func cols(ctx context.Context, sqldb *sql.DB, name string) ([]string, error) {
	rows, err := sqldb.QueryContext(ctx, "DESCRIBE "+name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		// DESCRIBE returns (column_name, column_type, null, key, default, extra);
		// scan the first column, ignore the rest.
		cs, err := rows.Columns()
		if err != nil {
			return nil, err
		}
		dst := make([]any, len(cs))
		var name string
		dst[0] = &name
		for i := 1; i < len(cs); i++ {
			var sink any
			dst[i] = &sink
		}
		if err := rows.Scan(dst...); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// zstdSidecar compresses outPath → outPath.zst with the same settings the rest of
// the pipeline uses (high ratio + long window), via the system zstd binary.
func zstdSidecar(path string) error {
	cmd := exec.Command("zstd", "-19", "--long=27", "-f", "-q", path, "-o", path+".zst")
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
