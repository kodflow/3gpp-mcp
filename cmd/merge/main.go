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
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/kodflow/3gpp-mcp/internal/store"
)

var Version = "dev"

func main() {
	out := flag.String("out", "data/3gpp.duckdb", "merged output DuckDB path")
	fts := flag.Bool("fts", true, "rebuild the BM25 FTS index on the merged DB")
	flag.Parse()
	inputs := flag.Args()
	if len(inputs) == 0 {
		fmt.Fprintln(os.Stderr, "merge: no input DBs given")
		os.Exit(2)
	}
	if err := run(context.Background(), *out, inputs, *fts); err != nil {
		fmt.Fprintln(os.Stderr, "merge:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, out string, inputs []string, fts bool) error {
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

	for i, in := range inputs {
		if _, err := os.Stat(in); err != nil {
			return fmt.Errorf("input %s: %w", in, err)
		}
		// ATTACH is a parser-level statement — it takes no bind parameters, so the
		// path is inlined (single-quote-escaped; inputs are local CLI args).
		attach := "ATTACH '" + strings.ReplaceAll(in, "'", "''") + "' AS src (READ_ONLY)"
		if _, err := sqldb.ExecContext(ctx, attach); err != nil {
			return fmt.Errorf("attach %s: %w", in, err)
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
