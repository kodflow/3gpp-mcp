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

// mergeOne folds the ATTACHed `src` DB into the main DB. first=true means this is
// the first shard, so the curated global seeds (evolutions) are copied from it;
// later shards skip them to avoid N-fold duplication.
func mergeOne(ctx context.Context, sqldb *sql.DB, first bool) error {
	maxID := func(table, col string) (int64, error) {
		var n int64
		err := sqldb.QueryRowContext(ctx, `SELECT COALESCE(MAX(`+col+`),0) FROM `+table).Scan(&n)
		return n, err
	}

	// Synthetic-PK tables: offset ids so shards never collide.
	cOff, err := maxID("clauses", "chunk_id")
	if err != nil {
		return err
	}
	opOff, err := maxID("api_operations", "op_id")
	if err != nil {
		return err
	}
	scOff, err := maxID("api_schemas", "schema_id")
	if err != nil {
		return err
	}

	stmts := []string{
		// Natural-key dimension/overlay tables: dedup on conflict.
		`INSERT INTO specs SELECT * FROM src.specs ON CONFLICT DO NOTHING`,
		`INSERT INTO spec_versions SELECT * FROM src.spec_versions ON CONFLICT DO NOTHING`,
		`INSERT INTO acronyms SELECT * FROM src.acronyms ON CONFLICT DO NOTHING`,
		`INSERT INTO li_events SELECT * FROM src.li_events ON CONFLICT DO NOTHING`,
		`INSERT INTO li_event_fields SELECT * FROM src.li_event_fields ON CONFLICT DO NOTHING`,
		`INSERT INTO li_nf_clauses SELECT * FROM src.li_nf_clauses ON CONFLICT DO NOTHING`,
		`INSERT INTO asn1_types SELECT * FROM src.asn1_types ON CONFLICT DO NOTHING`,
		`INSERT INTO releases SELECT * FROM src.releases ON CONFLICT DO NOTHING`,
		// No-PK fact table: disjoint across shards, plain append.
		`INSERT INTO changes SELECT * FROM src.changes`,
		// Synthetic-PK fact tables: append with an id offset.
		fmt.Sprintf(`INSERT INTO clauses
			SELECT chunk_id + %d, spec_id, release, version, clause_path, heading, text, is_normative, embedding
			FROM src.clauses`, cOff),
		fmt.Sprintf(`INSERT INTO api_operations
			SELECT op_id + %d, spec_id, release, version, api_doc_version, service, service_family,
			       api_root, path, method, operation_id, summary, tags, request_schema, response_codes,
			       yaml_file, forge_sha, forge_url
			FROM src.api_operations`, opOff),
		fmt.Sprintf(`INSERT INTO api_schemas
			SELECT schema_id + %d, spec_id, release, version, service, schema_name, kind, description,
			       properties, enum_values, refs_out, yaml_file, forge_sha, forge_url
			FROM src.api_schemas`, scOff),
	}
	if first {
		// evolutions is a curated seed, identical in every shard — keep one copy.
		stmts = append(stmts, `INSERT INTO evolutions SELECT * FROM src.evolutions`)
	}
	for _, q := range stmts {
		if _, err := sqldb.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("%s: %w", firstLine(q), err)
		}
	}
	return nil
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}
