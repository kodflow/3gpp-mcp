// Command merge fuses several partial DuckDB snapshots (one per matrix shard —
// e.g. one release or one series) into a single consolidated DB.
//
//	merge --out data/3gpp.duckdb shard1.duckdb shard2.duckdb ...
//
// Shards are disjoint (each (spec,release) is produced by exactly one shard), so
// the merge is a concatenation: synthetic primary keys (chunk_id, op_id,
// schema_id) are offset to stay unique; natural-key tables dedup via ON CONFLICT
// DO NOTHING (a spec row repeats across release shards); the curated evolutions
// seed — identical in every shard — is taken from the first shard only. FTS is
// rebuilt on the merged DB (per-shard FTS indexes can't be concatenated).
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/kodflow/3gpp-mcp/internal/store"
)

var Version = "dev"

func main() {
	out := flag.String("out", "data/3gpp.duckdb", "merged output DuckDB path")
	fts := flag.Bool("fts", true, "rebuild the BM25 FTS index on the merged DB")
	indexOut := flag.String("index-out", "", "also write a corpus-index.json (spec_id -> latest version) for incremental discover")
	base := flag.String("base", "", "existing DB to start from (incremental): each shard's whole series REPLACES the base's")
	flag.Parse()
	inputs := flag.Args()
	if len(inputs) == 0 {
		fmt.Fprintln(os.Stderr, "merge: no input DBs given")
		os.Exit(2)
	}
	if err := run(context.Background(), *out, inputs, *fts, *indexOut, *base); err != nil {
		fmt.Fprintln(os.Stderr, "merge:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, out string, inputs []string, fts bool, indexOut, base string) error {
	_ = os.Remove(out)
	db, err := store.Open(out)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	if err := db.Reset(ctx); err != nil {
		return err
	}
	sqldb := db.DB()

	// Build the fold list. With --base (incremental), the existing DB is folded
	// first as the accumulator; each subsequent shard's whole series REPLACES the
	// base's copy of that series (a shard is one complete series). Without --base
	// (full rebuild), the shards are folded fresh.
	folds := inputs
	incremental := base != ""
	if incremental {
		folds = append([]string{base}, inputs...)
	}

	for i, in := range folds {
		if _, err := os.Stat(in); err != nil {
			return fmt.Errorf("input %s: %w", in, err)
		}
		// ATTACH is a parser-level statement — it takes no bind parameters, so the
		// path is inlined (single-quote-escaped; inputs are local CLI args).
		attach := "ATTACH '" + strings.ReplaceAll(in, "'", "''") + "' AS src (READ_ONLY)"
		if _, err := sqldb.ExecContext(ctx, attach); err != nil {
			return fmt.Errorf("attach %s: %w", in, err)
		}
		// Incremental: drop the base's copy of every series this shard carries
		// before folding it (the base is fold 0 and is never self-purged).
		if incremental && i > 0 {
			if err := purgeShardSeries(ctx, sqldb); err != nil {
				return fmt.Errorf("purge series for %s: %w", in, err)
			}
		}
		if err := mergeOne(ctx, sqldb, i == 0); err != nil {
			return fmt.Errorf("merge %s: %w", in, err)
		}
		if _, err := sqldb.ExecContext(ctx, `DETACH src`); err != nil {
			return fmt.Errorf("detach %s: %w", in, err)
		}
		fmt.Fprintf(os.Stderr, "[merge] folded %s\n", in)
	}

	if fts {
		if err := db.EnableFTS(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "[merge] FTS unavailable (lexical degrades to LIKE): %v\n", err)
		}
	}
	var specs, clauses int
	_ = sqldb.QueryRowContext(ctx, `SELECT count(*) FROM specs`).Scan(&specs)
	_ = sqldb.QueryRowContext(ctx, `SELECT count(*) FROM clauses`).Scan(&clauses)
	fmt.Fprintf(os.Stderr, "[merge] done: %s — specs=%d clauses=%d\n", out, specs, clauses)

	if indexOut != "" {
		n, err := writeIndex(ctx, sqldb, indexOut)
		if err != nil {
			return fmt.Errorf("write index %s: %w", indexOut, err)
		}
		fmt.Fprintf(os.Stderr, "[merge] index: %s — %d specs\n", indexOut, n)
	}
	return nil
}

// writeIndex emits corpus-index.json = {spec_id: latest version}, the small
// manifest `discover` diffs against the live 3GPP status report to size the
// next matrix. "Latest" = the highest numeric X.Y.Z across the spec's versions.
func writeIndex(ctx context.Context, sqldb *sql.DB, path string) (int, error) {
	rows, err := sqldb.QueryContext(ctx, `SELECT spec_id, version FROM spec_versions`)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	idx := map[string]string{}
	for rows.Next() {
		var spec, ver string
		if err := rows.Scan(&spec, &ver); err != nil {
			return 0, err
		}
		if cmpVer(ver, idx[spec]) > 0 {
			idx[spec] = ver
		}
	}
	b, err := json.MarshalIndent(idx, "", " ")
	if err != nil {
		return 0, err
	}
	return len(idx), os.WriteFile(path, b, 0o644)
}

// cmpVer compares "X.Y.Z" numerically; empty sorts lowest.
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
	for i, p := range strings.SplitN(s, ".", 3) {
		if i > 2 {
			break
		}
		t[i], _ = strconv.Atoi(p)
	}
	return t
}

// purgeShardSeries removes, from the main DB, every row of the series carried by
// the ATTACHed `src` shard — so the shard's fresh, complete series replaces the
// base's stale copy (no per-version supersede needed: a shard is a whole series).
func purgeShardSeries(ctx context.Context, sqldb *sql.DB) error {
	rows, err := sqldb.QueryContext(ctx, `SELECT DISTINCT substr(spec_id,1,2) FROM src.specs`)
	if err != nil {
		return err
	}
	var series []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			_ = rows.Close()
			return err
		}
		series = append(series, "'"+strings.ReplaceAll(s, "'", "''")+"'")
	}
	_ = rows.Close()
	if len(series) == 0 {
		return nil
	}
	in := strings.Join(series, ",")
	// Every spec-keyed table (acronyms/evolutions are global → left intact).
	for _, t := range []string{
		"specs", "spec_versions", "clauses", "changes",
		"li_events", "li_event_fields", "li_nf_clauses",
		"api_operations", "api_schemas", "asn1_types",
	} {
		if _, err := sqldb.ExecContext(ctx, `DELETE FROM `+t+` WHERE substr(spec_id,1,2) IN (`+in+`)`); err != nil {
			return fmt.Errorf("%s: %w", t, err)
		}
	}
	return nil
}

// tableSpec: idCol != "" => synthetic PK offset; conflict => dedup natural key.
type tableSpec struct {
	name     string
	idCol    string
	conflict bool
}

// mergeTables is the fold order. acronyms/evolutions are global (no spec_id).
var mergeTables = []tableSpec{
	{"specs", "", true},
	{"spec_versions", "", true},
	{"acronyms", "", true},
	{"li_events", "", true},
	{"li_event_fields", "", true},
	{"li_nf_clauses", "", true},
	{"asn1_types", "", true},
	{"releases", "", true},
	{"changes", "", false},
	{"clauses", "chunk_id", false},
	{"api_operations", "op_id", false},
	{"api_schemas", "schema_id", false},
}

// mergeOne folds the ATTACHed `src` DB into the main DB, column-by-column on the
// INTERSECTION of src/main columns so it tolerates schema drift (an older base
// DB with fewer columns merges fine; new columns just take their default).
// first=true copies the curated evolutions seed (identical in every shard).
func mergeOne(ctx context.Context, sqldb *sql.DB, first bool) error {
	for _, t := range mergeTables {
		if err := foldTable(ctx, sqldb, t); err != nil {
			return fmt.Errorf("fold %s: %w", t.name, err)
		}
	}
	if first {
		if err := foldTable(ctx, sqldb, tableSpec{"evolutions", "", false}); err != nil {
			return fmt.Errorf("fold evolutions: %w", err)
		}
	}
	return nil
}

func foldTable(ctx context.Context, sqldb *sql.DB, t tableSpec) error {
	outCols, err := cols(ctx, sqldb, t.name)
	if err != nil {
		return err
	}
	srcCols, err := cols(ctx, sqldb, "src."+t.name)
	if err != nil {
		return nil // src lacks this table (older shard) — nothing to fold
	}
	srcSet := map[string]bool{}
	for _, c := range srcCols {
		srcSet[c] = true
	}
	var off int64
	if t.idCol != "" {
		if err := sqldb.QueryRowContext(ctx, `SELECT COALESCE(MAX(`+t.idCol+`),0) FROM `+t.name).Scan(&off); err != nil {
			return err
		}
	}
	var ins, sel []string
	for _, c := range outCols { // preserve main column order; keep only common cols
		if !srcSet[c] {
			continue
		}
		ins = append(ins, c)
		if c == t.idCol {
			sel = append(sel, fmt.Sprintf("%s + %d", c, off))
		} else {
			sel = append(sel, c)
		}
	}
	if len(ins) == 0 {
		return nil
	}
	q := fmt.Sprintf("INSERT INTO %s (%s) SELECT %s FROM src.%s",
		t.name, strings.Join(ins, ","), strings.Join(sel, ","), t.name)
	if t.conflict {
		q += " ON CONFLICT DO NOTHING"
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
		if name, ok := cells[0].(string); ok { // first DESCRIBE column = column_name
			out = append(out, name)
		}
	}
	return out, rows.Err()
}
