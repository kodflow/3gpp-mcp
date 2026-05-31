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
	buildIndexOut := flag.String("build-index-out", "", "also write a build-index.json (the three canonical identities) so discover can force ingest/enricher/embed refresh on an identity drift")
	base := flag.String("base", "", "existing DB to start from (incremental): each shard's (series,release) buckets REPLACE the base's")
	stripEmbeddings := flag.Bool("strip-embeddings", false,
		"after merge, DROP COLUMN embedding so the GitHub Release asset stays slim (vectors live in the GHCR per-series sub-bases)")
	flag.Parse()
	inputs := flag.Args()
	if len(inputs) == 0 {
		fmt.Fprintln(os.Stderr, "merge: no input DBs given")
		os.Exit(2)
	}
	if err := run(context.Background(), *out, inputs, *fts, *indexOut, *base, *stripEmbeddings, *subjectIndexOut, *buildIndexOut); err != nil {
		fmt.Fprintln(os.Stderr, "merge:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, out string, inputs []string, fts bool, indexOut, base string, stripEmbeddings bool, subjectIndexOut, buildIndexOut string) error {
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

	// DEFENSIVE one-shot dedup of `changes` (plan PR-4). The delete-before-fold
	// purge above is the real idempotency contract; this is a SAFETY NET that
	// heals a base ALREADY corrupted by the prior (series, major) purge — those
	// duplicate sub-floor CRs predate this build and can't be undone by a clean
	// fold. We collapse to one row per (spec_id, cr_number, cr_revision,
	// to_version). It is a no-op on a base built under the fixed contract, so it
	// never masks a regression of the delete-before-fold guarantee.
	if err := dedupChanges(ctx, sqldb); err != nil {
		return fmt.Errorf("dedup changes: %w", err)
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
	// Stamp each subject's EFFECTIVE footprint into the output meta so the DB
	// self-describes which subject versions actually produced its content. A
	// subject's footprint may advance to the current code's value ONLY if every
	// series it owns was rebuilt in this run (or this is a full rebuild) — because
	// the subject re-extracts during the per-series ingest, so a series that
	// wasn't rebuilt still holds the OLD subject's rows. If we blindly stamped the
	// current footprint, an operator dispatch with an explicit series_scope that
	// excludes a changed subject's series would publish a subject-index claiming
	// the new version while the DB still holds the old extraction — and the next
	// auto-delta would see "unchanged" and never rebuild it (a footprint that
	// lies). When not advancing, we carry the base's stored footprint forward, so
	// the next auto-delta still detects the pending change and rebuilds it.
	//
	// subject-fp honesty (plan PR-4): the carry-forward reads the BASE's footprint
	// only when the base actually contributed its rows to the merged DB. When the
	// pipeline_version gate above dropped the base (incremental=false over a narrow
	// discover-sized matrix — a FORCED full rebuild, not a real --all), the merged
	// DB holds ONLY the shards: an unrebuilt subject's rows (li_events, acronyms)
	// are GONE. Carrying the dropped base's footprint forward would publish a
	// subject-index claiming the subject is up-to-date while its data is absent,
	// and the next discover would see "unchanged" and never heal it. Passing
	// fpBase="" makes an unrebuilt subject's effective footprint "" → discover
	// treats it as changed → its series is rebuilt next run (self-healing), exactly
	// as the corpus-index path already self-heals from a shrunken post-fold DB.
	fpBase := base
	if !incremental {
		fpBase = ""
	}
	effFP, err := effectiveSubjectFootprints(ctx, sqldb, inputs, fpBase)
	if err != nil {
		return fmt.Errorf("compute subject footprints: %w", err)
	}
	for name, fp := range effFP {
		if err := db.SetMeta("subject_fp_"+name, fp); err != nil {
			return fmt.Errorf("stamp subject_fp_%s: %w", name, err)
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
	// discover can detect a changed subject and re-index only its series. This is
	// the SAME effective-footprint map stamped into the DB meta above, so the
	// published index never disagrees with the DB it describes.
	if subjectIndexOut != "" {
		b, err := json.MarshalIndent(effFP, "", " ")
		if err != nil {
			return fmt.Errorf("marshal subject index: %w", err)
		}
		if err := os.WriteFile(subjectIndexOut, b, 0o644); err != nil {
			return fmt.Errorf("write subject index %s: %w", subjectIndexOut, err)
		}
		fmt.Fprintf(os.Stderr, "[merge] subject-index: %s — %d subjects\n", subjectIndexOut, len(effFP))
	}
	// Stamp + publish the three canonical build identities (plan PR-3). The
	// SpecIngestIdentity describes the spec content the merged DB carries (its
	// subject footprints are the EFFECTIVE ones, so an unrebuilt subject keeps the
	// base's footprint and the published identity never claims a version whose rows
	// are absent); EmbedIdentity reflects the output's actual model (lexical "" when
	// vectors were stripped, mirroring pipeline_version above). discover reads this
	// manifest and forces the matching refresh (re-ingest / re-embed / enricher) on
	// a drift, so an identity change is detected even when no spec version moved.
	bi := buildIndexFor(effFP, outModelID)
	if err := db.SetMeta("spec_ingest_identity", bi.SpecIngestIdentity); err != nil {
		return fmt.Errorf("stamp spec_ingest_identity: %w", err)
	}
	if err := db.SetMeta("global_enrichment_identity", bi.GlobalEnrichmentIdentity); err != nil {
		return fmt.Errorf("stamp global_enrichment_identity: %w", err)
	}
	if err := db.SetMeta("embed_identity", bi.EmbedIdentity); err != nil {
		return fmt.Errorf("stamp embed_identity: %w", err)
	}
	if buildIndexOut != "" {
		b, err := json.MarshalIndent(bi, "", " ")
		if err != nil {
			return fmt.Errorf("marshal build index: %w", err)
		}
		if err := os.WriteFile(buildIndexOut, b, 0o644); err != nil {
			return fmt.Errorf("write build index %s: %w", buildIndexOut, err)
		}
		fmt.Fprintf(os.Stderr, "[merge] build-index: %s\n", buildIndexOut)
	}
	return nil
}

// buildIndexFor composes the merged DB's three canonical identities (plan PR-3).
// The SpecIngestIdentity uses the EFFECTIVE subject footprints (effFP) — the ones
// actually backing the merged DB's rows — so a subject that wasn't rebuilt this
// run keeps its base footprint and the published identity never overstates what
// the DB contains. The ASN.1 scanner tag comes from the current code: it is folded
// into the LI footprint already (subjectmeta.Footprint), so effFP carries it for a
// rebuilt LI and the base value for an unrebuilt one — passing the current tag
// here is consistent because the LI footprint is the authoritative carrier.
// outModelID is "" for a lexical (stripped) output, mirroring pipeline_version.
func buildIndexFor(effFP map[string]string, outModelID string) model.BuildIndex {
	fps := make([]string, 0, len(effFP))
	for _, fp := range effFP {
		fps = append(fps, fp)
	}
	return model.CurrentBuildIndex(fps, subjectmeta.ASN1ScannerVersion, outModelID, model.GlobalEnrichmentParts{})
}

// effectiveSubjectFootprints returns name->footprint for every subject, where a
// subject advances to the current code's footprint ONLY if every series it owns
// was present in this run's shards (i.e. its rows were actually re-extracted).
// Otherwise it carries the base's stored footprint forward — which is "" when
// there is no base or the base predates the stamp, and that's safe: discover
// then treats the subject as changed and rebuilds it. This uniform rule covers
// both the explicit-narrow-scope delta AND the first-build-with-narrow-scope
// case; a real full build (--all) includes every series, so every subject
// advances. See the call site for why a blind current-footprint stamp lies.
func effectiveSubjectFootprints(ctx context.Context, sqldb *sql.DB, inputs []string, base string) (map[string]string, error) {
	rebuilt, err := shardSeries(ctx, sqldb, inputs)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(subjectmeta.All))
	for _, m := range subjectmeta.All {
		advance := true
		for _, s := range m.Series {
			if !rebuilt[s] {
				advance = false
				break
			}
		}
		if advance {
			out[m.Name] = subjectmeta.Footprint(m)
		} else {
			out[m.Name] = metaValue(ctx, sqldb, base, "subject_fp_"+m.Name)
		}
	}
	return out, nil
}

// shardSeries returns the set of 2-digit series (substr(spec_id,1,2)) present in
// the given shard DBs — i.e. the series actually rebuilt this run.
func shardSeries(ctx context.Context, sqldb *sql.DB, paths []string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, p := range paths {
		esc := strings.ReplaceAll(p, "'", "''")
		if _, err := sqldb.ExecContext(ctx, "ATTACH '"+esc+"' AS ss (READ_ONLY)"); err != nil {
			return nil, fmt.Errorf("attach %s: %w", p, err)
		}
		rows, err := sqldb.QueryContext(ctx, `SELECT DISTINCT substr(spec_id,1,2) FROM ss.spec_versions`)
		if err != nil {
			_, _ = sqldb.ExecContext(ctx, "DETACH ss")
			return nil, err
		}
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				_ = rows.Close()
				_, _ = sqldb.ExecContext(ctx, "DETACH ss")
				return nil, err
			}
			out[s] = true
		}
		_ = rows.Close()
		if _, err := sqldb.ExecContext(ctx, "DETACH ss"); err != nil {
			return nil, err
		}
	}
	return out, nil
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

// writeIndex emits corpus-index.json = {"spec_id|release": latest version}, the
// small manifest `discover` diffs against the live 3GPP status report to size the
// next matrix. The key is per-(spec_id, release) — NOT a single scalar per spec
// (plan PR-5, finding delta-blind-to-nonmonotonic-lower-release-update): a frozen
// release keeps getting maintenance versions (CLAUDE.md §8 #8), so collapsing a
// spec to its single highest X.Y.Z hid a Rel-16 bump whenever Rel-19 was higher.
// "Latest" = the highest numeric X.Y.Z within each (spec, release) bucket.
func writeIndex(ctx context.Context, sqldb *sql.DB, path string) (int, error) {
	rows, err := sqldb.QueryContext(ctx, `SELECT spec_id, release, version FROM spec_versions`)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	idx := map[string]string{}
	for rows.Next() {
		var spec, rel, ver string
		if err := rows.Scan(&spec, &rel, &ver); err != nil {
			return 0, err
		}
		key := spec + "|" + rel
		if cmpVer(ver, idx[key]) > 0 {
			idx[key] = ver
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

// purgeShardScope removes, from the main DB, exactly the rows the ATTACHed `src`
// shard is about to re-fold — so a delta replaces its own buckets without
// clobbering siblings AND without leaving duplicates. The purge SET must equal
// (or contain) the fold's WRITE set, per table:
//
//   - the 8 release-bearing tables → scoped by (spec_id, release) read from the
//     shard's actual spec_versions. NOT (series, release): a sub-shard may carry
//     a SUBSET of a series' specs (operator series_scope, a partial corpus on
//     disk, or future per-spec sharding), and a series-wide delete would wipe
//     sibling specs the shard never re-inserts (sibling-spec-loss).
//   - `changes` → scoped by spec_id. The Change-History annex is CUMULATIVE per
//     spec (every CR since inception, to_version spanning many majors) and is
//     re-folded WHOLE for each spec the shard carries. Purging by major-of-
//     to_version (the prior behaviour) deleted only the shard's release-major
//     while the fold re-inserted ALL majors → sub-floor CRs duplicated on every
//     delta. Deleting the whole spec_id bucket guarantees delete ⊇ insert.
//   - `acronyms` → scoped by source_series (the owning subject's series). It is a
//     global subject-owned table with no spec_id/release, so a changed/regressed
//     glossary subject's stale rows would otherwise survive the ON-CONFLICT-DO-
//     NOTHING fold forever. Rows without provenance (pre-PR-4 base) are left
//     alone — they are healed once the owning series is re-ingested with the
//     provenance column populated.
//   - `specs` (no release; one row shared across releases) → NOT purged: a
//     sub-shard may not carry every spec, so deleting by series would drop
//     siblings. ON CONFLICT DO NOTHING on re-fold keeps it.
func purgeShardScope(ctx context.Context, sqldb *sql.DB) error {
	rows, err := sqldb.QueryContext(ctx, `SELECT DISTINCT spec_id, release FROM src.spec_versions`)
	if err != nil {
		return err
	}
	type pair struct{ specID, release string }
	var pairs []pair
	specIDSet := map[string]bool{}
	seriesSet := map[string]bool{}
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.specID, &p.release); err != nil {
			_ = rows.Close()
			return err
		}
		pairs = append(pairs, p)
		specIDSet[p.specID] = true
		if len(p.specID) >= 2 {
			seriesSet[p.specID[:2]] = true
		}
	}
	_ = rows.Close()
	if len(pairs) == 0 {
		return nil
	}

	// (a) release-bearing tables: DELETE WHERE (spec_id, release) matches a bucket
	// the shard carries — never a broad series sweep (sibling-spec-loss).
	relPred := make([]string, 0, len(pairs))
	relArgs := make([]any, 0, len(pairs)*2)
	for _, p := range pairs {
		relPred = append(relPred, "(spec_id = ? AND release = ?)")
		relArgs = append(relArgs, p.specID, p.release)
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

	// (b) changes: delete-before-fold by spec_id (the cumulative annex is
	// re-folded whole per spec). This is the PRIMARY idempotency contract.
	specArgs := make([]any, 0, len(specIDSet))
	specPlace := make([]string, 0, len(specIDSet))
	for id := range specIDSet {
		specArgs = append(specArgs, id)
		specPlace = append(specPlace, "?")
	}
	if _, err := sqldb.ExecContext(ctx,
		`DELETE FROM changes WHERE spec_id IN (`+strings.Join(specPlace, ",")+`)`, specArgs...); err != nil {
		return fmt.Errorf("changes: %w", err)
	}

	// (c) acronyms: scope-purge the subject-owned global table by the series this
	// shard rebuilds, so a corrected/removed glossary term replaces the stale base
	// row instead of coexisting with it (ON CONFLICT DO NOTHING can't overwrite).
	serArgs := make([]any, 0, len(seriesSet))
	serPlace := make([]string, 0, len(seriesSet))
	for s := range seriesSet {
		serArgs = append(serArgs, s)
		serPlace = append(serPlace, "?")
	}
	if len(serArgs) > 0 {
		if _, err := sqldb.ExecContext(ctx,
			`DELETE FROM acronyms WHERE source_series IN (`+strings.Join(serPlace, ",")+`)`, serArgs...); err != nil {
			return fmt.Errorf("acronyms: %w", err)
		}
	}
	return nil
}

// dedupChanges collapses `changes` to one row per natural key
// (spec_id, cr_number, cr_revision, to_version), keeping the first occurrence.
// It is the defensive heal for bases corrupted by the pre-PR-4 (series, major)
// purge, which duplicated sub-floor CRs on every delta. The `changes` table has
// no unique constraint (a CR can legitimately touch several specs, but the same
// CR row for the SAME spec at the SAME to_version is a duplicate), so DuckDB
// can't dedup it via ON CONFLICT; we rebuild the table keeping ROW_NUMBER()=1.
// rowid (DuckDB's implicit row identifier) gives a stable tie-break so the pass
// is deterministic. On a base built under the fixed delete-before-fold contract
// every group already has exactly one row, so this is a no-op.
func dedupChanges(ctx context.Context, sqldb *sql.DB) error {
	tx, err := sqldb.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin dedup tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	// COALESCE on the nullable key parts so NULLs group together (a NULL
	// cr_revision must not split an otherwise-identical duplicate into two rows).
	const ddl = `CREATE TABLE changes_dedup AS
		SELECT * EXCLUDE (rn) FROM (
			SELECT *, ROW_NUMBER() OVER (
				PARTITION BY spec_id,
				             COALESCE(cr_number, ''),
				             COALESCE(CAST(cr_revision AS VARCHAR), ''),
				             COALESCE(to_version, '')
				ORDER BY rowid
			) AS rn
			FROM changes
		) WHERE rn = 1`
	if _, err := tx.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("build changes_dedup: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE changes`); err != nil {
		return fmt.Errorf("drop changes: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `ALTER TABLE changes_dedup RENAME TO changes`); err != nil {
		return fmt.Errorf("rename changes_dedup: %w", err)
	}
	// Re-create the non-unique secondary indexes schema.sql guarantees (the
	// CREATE-AS-SELECT path does not carry them over).
	for _, ddl := range []string{
		`CREATE INDEX IF NOT EXISTS changes_spec ON changes (spec_id)`,
		`CREATE INDEX IF NOT EXISTS changes_cr   ON changes (cr_number)`,
	} {
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("recreate changes index: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit dedup tx: %w", err)
	}
	committed = true
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
	// 2. Slim copy: same rows, every column EXCEPT the vector ones (the dense
	//    vector AND its per-clause fingerprint). Dropping embedding_hash too keeps
	//    the lexical asset free of any vector metadata, and means a future embed
	//    run over this slim DB sees every clause as "never embedded" (correct: it
	//    has no vectors).
	if _, err := tx.ExecContext(ctx, `CREATE TABLE clauses_slim AS SELECT * EXCLUDE (embedding, embedding_hash) FROM clauses`); err != nil {
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
