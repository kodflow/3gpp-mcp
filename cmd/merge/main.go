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

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
	"github.com/kodflow/3gpp-mcp/internal/subjectmeta"
)

var Version = "dev"

func main() {
	out := flag.String("out", "data/3gpp.duckdb", "merged output DuckDB path")
	fts := flag.Bool("fts", true, "rebuild the BM25 FTS index on the merged DB")
	indexOut := flag.String("index-out", "", "also write a corpus-index.json (spec_id -> latest version) for incremental discover")
	subjectIndexOut := flag.String("subject-index-out", "", "also write a subject-index.json (subject -> footprint) so discover can detect a changed subject")
	base := flag.String("base", "", "existing DB to start from (incremental): each shard's (series,release) buckets REPLACE the base's")
	stripEmbeddings := flag.Bool("strip-embeddings", false,
		"after merge, DROP COLUMN embedding so the GitHub Release asset stays slim (vectors live in the GHCR per-series sub-bases)")
	flag.Parse()
	inputs := flag.Args()
	if len(inputs) == 0 {
		fmt.Fprintln(os.Stderr, "merge: no input DBs given")
		os.Exit(2)
	}
	if err := run(context.Background(), *out, inputs, *fts, *indexOut, *base, *stripEmbeddings, *subjectIndexOut); err != nil {
		fmt.Fprintln(os.Stderr, "merge:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, out string, inputs []string, fts bool, indexOut, base string, stripEmbeddings bool, subjectIndexOut string) error {
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

	// The merged output's identity is its OWN pipeline_version — the digest of the
	// indexing mechanics that produced its CONTENT (parser, chunking, schema,
	// embedding model). It is lexical (empty model id) when we strip vectors,
	// otherwise it mirrors the model the shards carry. This is the value the NEXT
	// delta run compares its --base against, so it must describe the output, not
	// the raw shard: a bge-m3 shard whose vectors we strip yields a LEXICAL DB and
	// must be tagged as such, or the following lexical delta would see a spurious
	// mismatch and full-rebuild (clobbering the corpus).
	outModelID := ""
	if !stripEmbeddings && len(inputs) > 0 {
		outModelID = metaValue(ctx, sqldb, inputs[0], "embedding_model")
	}
	outPV := model.PipelineVersion(outModelID)

	// Build the fold list. With --base (incremental), the existing DB is folded
	// first as the accumulator; each subsequent shard's whole series REPLACES the
	// base's copy of that series (a shard is one complete series). Without --base
	// (full rebuild), the shards are folded fresh.
	folds := inputs
	incremental := base != ""
	if incremental {
		// Invariant #2 (plan §15): a delta is only valid if the base was built by
		// the SAME indexing pipeline as the output we're about to stamp. If the
		// base's pipeline_version differs (parser/chunking/schema/embedding-model
		// change, or a legacy base that predates the stamp → ""), it is
		// incompatible → rebuild fresh from the shards.
		basePV := metaValue(ctx, sqldb, base, "pipeline_version")
		if basePV != outPV {
			fmt.Fprintf(os.Stderr, "[merge] pipeline_version mismatch (base=%q output=%q) — full rebuild, ignoring --base\n", basePV, outPV)
			incremental = false
		}
	}
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
		// Incremental: drop the base's copy of the (series, release) buckets this
		// shard carries before folding it (the base is fold 0 and is never
		// self-purged). (series,release) — not whole series — so a per-release
		// sub-shard can't clobber sibling buckets of the same series.
		if incremental && i > 0 {
			if err := purgeShardScope(ctx, sqldb); err != nil {
				return fmt.Errorf("purge scope for %s: %w", in, err)
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

	// Strip vectors BEFORE the FTS rebuild + before counting/index-out, so the
	// downstream artifact (e.g. the `latest` GitHub Release asset, capped at 2 GB)
	// only carries the lexical surface. The per-series vectorised sub-bases
	// (with their HNSW) are distributed separately on GHCR by `publish-vec` —
	// this strip keeps the lexical channel slim while embed=true is in flight.
	if stripEmbeddings {
		if err := stripEmbeddingColumn(ctx, sqldb); err != nil {
			return fmt.Errorf("strip embeddings: %w", err)
		}
		// Drop any leftover meta that's vector-specific so the slim DB doesn't
		// claim semantic capability the consumer can't realise. Errors are
		// SURFACED (a silent drop would let the slim DB advertise stale
		// embedding_model/dim/count that no longer match its schema).
		if _, err := sqldb.ExecContext(ctx, `DELETE FROM schema_meta WHERE key IN ('embedding_model','embedding_dim','embedding_count','hnsw_state')`); err != nil {
			return fmt.Errorf("clear vector meta: %w", err)
		}
		// Reclaim space so the .duckdb file shrinks before zstd. Same reasoning
		// for error surfacing: a failed CHECKPOINT means the file still carries
		// the dropped column's pages and the Release-asset size gate stops working.
		if _, err := sqldb.ExecContext(ctx, `CHECKPOINT`); err != nil {
			return fmt.Errorf("checkpoint after strip: %w", err)
		}
		fmt.Fprintln(os.Stderr, "[merge] stripped embeddings + vector meta (lexical-only output)")
	}
	// Stamp the output's pipeline_version (lost otherwise — schema_meta is NOT in
	// the fold list, by design, so each provenance key is set deliberately here).
	// WITHOUT this, the published DB carries an empty pipeline_version; the next
	// delta run's --base gate then sees a mismatch, abandons the incremental fold,
	// and republishes ONLY the changed series — destroying the rest of the corpus.
	// This is the single line that makes cross-run append actually engage.
	if err := db.SetMeta("pipeline_version", outPV); err != nil {
		return fmt.Errorf("stamp pipeline_version: %w", err)
	}
	// Stamp each subject's current footprint into the output meta so the DB
	// self-describes which subject versions produced it. merge is built from the
	// same commit as the shards in this run, so subjectmeta (current code) is the
	// authoritative footprint for the merged content. discover diffs the published
	// subject-index.json (below) against this same code on the NEXT run; a changed
	// subject then forces its series back into the matrix (plan TROU #1).
	for _, m := range subjectmeta.All {
		if err := db.SetMeta("subject_fp_"+m.Name, subjectmeta.Footprint(m)); err != nil {
			return fmt.Errorf("stamp subject_fp_%s: %w", m.Name, err)
		}
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
	// Publish the subject footprints alongside corpus-index.json so the next
	// discover can detect a changed subject and re-index only its series.
	if subjectIndexOut != "" {
		b, err := json.MarshalIndent(subjectmeta.Index(), "", " ")
		if err != nil {
			return fmt.Errorf("marshal subject index: %w", err)
		}
		if err := os.WriteFile(subjectIndexOut, b, 0o644); err != nil {
			return fmt.Errorf("write subject index %s: %w", subjectIndexOut, err)
		}
		fmt.Fprintf(os.Stderr, "[merge] subject-index: %s — %d subjects\n", subjectIndexOut, len(subjectmeta.All))
	}
	return nil
}

// metaValue reads one schema_meta value from a DuckDB file (attach read-only →
// select → detach). Returns "" on any error or missing key. Used to compare the
// base's pipeline_version against an incoming shard's.
func metaValue(ctx context.Context, sqldb *sql.DB, path, key string) string {
	attach := "ATTACH '" + strings.ReplaceAll(path, "'", "''") + "' AS mv (READ_ONLY)"
	if _, err := sqldb.ExecContext(ctx, attach); err != nil {
		return ""
	}
	defer func() { _, _ = sqldb.ExecContext(ctx, "DETACH mv") }()
	var v string
	_ = sqldb.QueryRowContext(ctx, "SELECT value FROM mv.schema_meta WHERE key = ?", key).Scan(&v)
	return v
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

// purgeShardScope removes, from the main DB, only the (series, release) buckets
// the ATTACHed `src` shard actually carries — so a per-release sub-shard replaces
// its own buckets without clobbering sibling sub-shards of the same series.
//
//   - the 8 release-bearing tables → scoped by (series, release);
//   - `changes` (no release column) → scoped by (series, major-of-to_version),
//     mapping each release to its version major via ReleaseOrdinal (drafts that
//     don't map are skipped, so unrelated CRs are never deleted);
//   - `specs` (no release; one row shared across releases) → NOT purged: a
//     release sub-shard may not carry every spec of the series, so deleting by
//     series would drop siblings. ON CONFLICT DO NOTHING on re-fold keeps it.
func purgeShardScope(ctx context.Context, sqldb *sql.DB) error {
	rows, err := sqldb.QueryContext(ctx, `SELECT DISTINCT substr(spec_id,1,2) AS series, release FROM src.spec_versions`)
	if err != nil {
		return err
	}
	type pair struct{ series, release string }
	var pairs []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.series, &p.release); err != nil {
			_ = rows.Close()
			return err
		}
		pairs = append(pairs, p)
	}
	_ = rows.Close()
	if len(pairs) == 0 {
		return nil
	}

	// (a) release-bearing tables: DELETE WHERE (series, release) matches a bucket.
	relPred := make([]string, 0, len(pairs))
	relArgs := make([]any, 0, len(pairs)*2)
	for _, p := range pairs {
		relPred = append(relPred, "(substr(spec_id,1,2) = ? AND release = ?)")
		relArgs = append(relArgs, p.series, p.release)
	}
	relWhere := strings.Join(relPred, " OR ")
	for _, t := range []string{
		"spec_versions", "clauses",
		"li_events", "li_event_fields", "li_nf_clauses",
		"api_operations", "api_schemas", "asn1_types",
	} {
		if _, err := sqldb.ExecContext(ctx, `DELETE FROM `+t+` WHERE `+relWhere, relArgs...); err != nil {
			return fmt.Errorf("%s: %w", t, err)
		}
	}

	// (b) changes: no release column → scope by (series, major-of-to_version).
	seen := map[string]bool{}
	chPred := make([]string, 0, len(pairs))
	chArgs := make([]any, 0, len(pairs)*2)
	for _, p := range pairs {
		ord, ok := model.ReleaseOrdinal(p.release)
		if !ok {
			continue // draft / unmapped — don't risk deleting unrelated CRs
		}
		maj := strconv.Itoa(ord)
		key := p.series + "/" + maj
		if seen[key] {
			continue
		}
		seen[key] = true
		chPred = append(chPred, "(substr(spec_id,1,2) = ? AND split_part(to_version,'.',1) = ?)")
		chArgs = append(chArgs, p.series, maj)
	}
	if len(chPred) > 0 {
		if _, err := sqldb.ExecContext(ctx, `DELETE FROM changes WHERE `+strings.Join(chPred, " OR "), chArgs...); err != nil {
			return fmt.Errorf("changes: %w", err)
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

// stripEmbeddingColumn removes the FLOAT[1024] `embedding` column from `clauses`.
//
// DuckDB refuses ALTER TABLE … DROP COLUMN as soon as ANY index references the
// table (it errors with "Dependency Error" even when the indexed columns are
// unrelated — conservative engine policy). The portable way out is the classic
// CREATE-AS-SELECT + RENAME swap: build the slim copy, drop the old table, then
// rename the slim into place. SELECT … EXCLUDE (embedding) is supported on
// DuckDB ≥ 0.8 and avoids enumerating every other column by hand.
//
// The whole sequence runs inside a transaction so a failure mid-way leaves the
// DB exactly as it was (rollback drops the partial clauses_slim, restores the
// original clauses + indices). Without the BEGIN/COMMIT a crash between DROP
// TABLE and RENAME would brick the artifact and require a manual rebuild.
func stripEmbeddingColumn(ctx context.Context, sqldb *sql.DB) error {
	tx, err := sqldb.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin strip tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback() // best-effort cleanup; the real error is already returned to caller
		}
	}()

	// 1. Drop the indices that reference the table (the HNSW one mentions
	//    `embedding` explicitly; the lexical ones don't but DuckDB refuses
	//    ALTER on ANY indexed table — see func doc). DROP INDEX errors are
	//    SURFACED: an unexpected schema (e.g. an extra index from a future
	//    schema change) is signal we want to diagnose, not silently ignore.
	for _, idx := range []string{"clauses_hnsw", "clauses_spec", "clauses_rel", "clauses_path"} {
		if _, err := tx.ExecContext(ctx, `DROP INDEX IF EXISTS `+idx); err != nil {
			return fmt.Errorf("drop index %s: %w", idx, err)
		}
	}
	// 2. Slim copy: same rows, every column EXCEPT embedding.
	if _, err := tx.ExecContext(ctx, `CREATE TABLE clauses_slim AS SELECT * EXCLUDE (embedding) FROM clauses`); err != nil {
		return fmt.Errorf("create clauses_slim: %w", err)
	}
	// 3. Swap.
	if _, err := tx.ExecContext(ctx, `DROP TABLE clauses`); err != nil {
		return fmt.Errorf("drop old clauses: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `ALTER TABLE clauses_slim RENAME TO clauses`); err != nil {
		return fmt.Errorf("rename clauses_slim: %w", err)
	}
	// 4. Re-create the lexical indices the schema.sql guarantees. (PK on chunk_id
	//    is dropped by the CREATE-AS-SELECT path; queries don't rely on the PK
	//    being declared at the DDL level — chunk_id is already unique by ingest
	//    construction — so we skip re-asserting it here to keep DuckDB happy.)
	for _, ddl := range []string{
		`CREATE INDEX IF NOT EXISTS clauses_spec ON clauses (spec_id)`,
		`CREATE INDEX IF NOT EXISTS clauses_rel  ON clauses (release)`,
		`CREATE INDEX IF NOT EXISTS clauses_path ON clauses (spec_id, clause_path)`,
	} {
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("recreate index: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit strip tx: %w", err)
	}
	committed = true
	return nil
}
