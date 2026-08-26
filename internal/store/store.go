// Package store wraps the DuckDB persistence layer (CLAUDE.md §4).
//
// Writes go through the ingest pipeline (batched inserts in a transaction).
// Reads expose lexical search (BM25 when the FTS extension loads, else a LIKE
// fallback) plus structured catalogue/clause/changelog queries. Vector (HNSW)
// search is added in Phase 4 once embeddings are populated.
//
// Doctrine: optional extensions (fts, vss) are best-effort. If they cannot
// load (e.g. offline), the store still works lexically — it degrades visibly
// (FTSAvailable() == false) but never blocks (mirrors the corpus.sh policy).
package store

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "github.com/marcboeker/go-duckdb/v2"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

//go:embed schema.sql
var schemaSQL string

// arraySep separates list elements packed into a single bound parameter before
// DuckDB string_split() turns them back into a VARCHAR[] (avoids array binding).
const arraySep = "\x1f"

// Store is a handle on a DuckDB database.
type Store struct {
	db              *sql.DB
	ftsAvailable    bool
	vssAvailable    bool
	sparseAvailable bool
	// contentAddressed: this corpus stores paragraphs once and points at them
	// (ADR 0004). A capability, not an assumption — etsi.duckdb and any
	// published lexical snapshot still carry the old `clauses` table, and the
	// same Store serves both.
	contentAddressed bool
}

// Open opens (or creates) the DuckDB file at path and applies the schema.
// Use ":memory:" for an ephemeral database (tests).
func Open(path string) (*Store, error) {
	db, err := sql.Open("duckdb", path)
	if err != nil {
		return nil, fmt.Errorf("open duckdb %q: %w", path, err)
	}
	db.SetMaxOpenConns(1) // DuckDB is single-writer; serialize to be safe
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	s.probeContentAddressed(context.Background())
	return s, nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the underlying handle for advanced/ad-hoc queries (POC, tests).
func (s *Store) DB() *sql.DB { return s.db }

// FTSAvailable reports whether BM25 ranking is active (FTS extension loaded
// and index built). When false, SearchClauses falls back to LIKE scoring.
func (s *Store) FTSAvailable() bool { return s.ftsAvailable }

// migrate applies the embedded schema idempotently and records the version.
func (s *Store) migrate() error {
	if _, err := s.db.Exec(schemaSQL); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	if _, err := s.db.Exec(
		`INSERT INTO schema_meta (key, value) VALUES ('schema_version', '1')
		 ON CONFLICT (key) DO NOTHING`); err != nil {
		return fmt.Errorf("record schema version: %w", err)
	}
	// Additive upgrade: a DB built before embedding_hash existed (a lexical
	// snapshot from an earlier binary) gets the column added in place — no
	// rebuild, no data loss. DuckDB ADD COLUMN IF NOT EXISTS is a no-op when the
	// column is already present (fresh DBs created from schema.sql above). This
	// is metadata ABOUT embeddings, so it deliberately does NOT bump
	// schema_version / SchemaVersion (lexical content is unchanged).
	if _, err := s.db.Exec(
		`ALTER TABLE clauses ADD COLUMN IF NOT EXISTS embedding_hash VARCHAR`); err != nil {
		return fmt.Errorf("add embedding_hash column: %w", err)
	}
	// Additive upgrade (plan PR-4): provenance on the subject-owned global
	// `acronyms` table. Without an owning-series column the incremental merge
	// cannot scope-purge a changed/regressed glossary subject's rows (acronyms
	// has no spec_id/release), so a corrected/removed term's stale row survives
	// forever (ON CONFLICT DO NOTHING on the fold can't overwrite it). The column
	// is metadata ABOUT provenance, not lexical content, so it deliberately does
	// NOT bump schema_version. A pre-PR-4 DB gets the column added in place; its
	// existing rows carry NULL source_series until the owning series is re-ingested.
	if _, err := s.db.Exec(
		`ALTER TABLE acronyms ADD COLUMN IF NOT EXISTS source_series VARCHAR`); err != nil {
		return fmt.Errorf("add acronyms source_series column: %w", err)
	}
	return nil
}

// SetMeta upserts a provenance/bookkeeping key (e.g. ingest hash, corpus date).
func (s *Store) SetMeta(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO schema_meta (key, value) VALUES (?, ?)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// ---- ingest_log (axis #15: --resume checkpoint) -------------------------
//
// MarkIngestStarted is called BEFORE InsertClauses + SetEmbedding for a spec.
// A row with status='started' survives a runner kill; on resume that spec is
// PURGED + re-ingested (a half-written spec would otherwise poison vectors).
// The pipeline_version column stamps the row with the caller's resume gate —
// since plan PR-3 that gate is model.SpecIngestIdentity (parser/chunking/schema +
// subject footprints), so a subject/parser change invalidates the row while a
// model bump does not. The column name is retained for read-compat; it carries
// whatever identity the caller passes.
func (s *Store) MarkIngestStarted(ctx context.Context, specID, version, pipelineVersion string) error {
	// Timestamps are passed as bound parameters (time.Now()) rather than the
	// SQL keyword CURRENT_TIMESTAMP because go-duckdb v1.x mis-parses the
	// keyword in INSERT … ON CONFLICT clauses (treats it as a column ref;
	// "Table ingest_log does not have a column named CURRENT_TIMESTAMP").
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO ingest_log (spec_id, version, status, pipeline_version, started_at)
		VALUES (?, ?, 'started', ?, ?)
		ON CONFLICT (spec_id, version) DO UPDATE SET
		  status='started', pipeline_version=excluded.pipeline_version,
		  started_at=excluded.started_at, completed_at=NULL`,
		specID, version, pipelineVersion, now)
	return err
}

// MarkIngestDone flips the spec to status='done' once every step (parse,
// upsert, clauses, embed, changes, subjects) succeeded. Only 'done' rows are
// skipped on resume.
func (s *Store) MarkIngestDone(ctx context.Context, specID, version string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		UPDATE ingest_log SET status='done', completed_at=?
		WHERE spec_id=? AND version=?`, now, specID, version)
	return err
}

// IngestProgress returns the per-status counts of the ingest_log table for the
// given pipeline_version (rows from a stale pipeline are ignored — the caller
// is expected to have wiped them via ResetIngestLog when the version drifts).
// Used by --resume + the progress display to show {planned, done, in-flight}.
func (s *Store) IngestProgress(ctx context.Context, pipelineVersion string) (done, started int, err error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT
		  COALESCE(SUM(CASE WHEN status='done'    THEN 1 ELSE 0 END), 0),
		  COALESCE(SUM(CASE WHEN status='started' THEN 1 ELSE 0 END), 0)
		FROM ingest_log
		WHERE pipeline_version = ?`, pipelineVersion)
	err = row.Scan(&done, &started)
	return done, started, err
}

// IsIngestDone reports whether (spec, version) finished cleanly under the SAME
// resume gate (SpecIngestIdentity since plan PR-3) — the gate the resume loop
// uses to skip work. A row stamped under a different identity counts as not-done.
func (s *Store) IsIngestDone(ctx context.Context, specID, version, pipelineVersion string) (bool, error) {
	var status, pv string
	err := s.db.QueryRowContext(ctx,
		`SELECT status, pipeline_version FROM ingest_log WHERE spec_id=? AND version=?`,
		specID, version).Scan(&status, &pv)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// A row from a stale pipeline counts as not-done (caller will wipe + re-ingest).
	return status == "done" && pv == pipelineVersion, nil
}

// PurgeSpecScope removes EVERY row carrying a given (spec_id, version) tuple
// from the writable tables — used on resume to clear a half-ingested spec
// (status='started' but no 'done') before re-running it. Scoped strictly by
// (spec_id, version): the `specs` row is keyed by spec_id alone and may be
// shared across versions, so we don't touch it (the re-ingest UPSERTs it).
//
// `changes` is INTENTIONALLY NOT purged here: rows carry `to_version` that
// spans many historical versions (the whole change-history annex is folded
// into the table at the LATEST version's ingest), so a (spec, to_version)
// delete would drop unrelated historical rows. Idempotency on changes is
// handled at insert time by ReplaceChanges (delete-then-insert scoped to
// spec_id) in the latest-version branch of the ingest loop.
func (s *Store) PurgeSpecScope(ctx context.Context, specID, version string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM clauses WHERE spec_id=? AND version=?`, specID, version); err != nil {
		return fmt.Errorf("purge clauses: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM spec_versions WHERE spec_id=? AND version=?`, specID, version); err != nil {
		return fmt.Errorf("purge spec_versions: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM ingest_log WHERE spec_id=? AND version=?`, specID, version); err != nil {
		return fmt.Errorf("purge ingest_log row: %w", err)
	}
	return nil
}

// ReplaceChanges wipes every existing `changes` row for spec_id and re-inserts
// the given batch. Used by the ingest loop at the LATEST-version branch (the
// only branch that writes changes) so a --resume retry of that same spec
// doesn't append duplicates of the cumulative change history. Idempotent on
// re-runs that produce the same set.
func (s *Store) ReplaceChanges(ctx context.Context, specID string, changes []model.Change) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM changes WHERE spec_id=?`, specID); err != nil {
		return fmt.Errorf("clear changes for %s: %w", specID, err)
	}
	if len(changes) == 0 {
		return nil
	}
	return s.InsertChanges(changes)
}

// ReplaceEvolutions wipes the evolutions table then re-inserts the given seed.
// Used by the ingest in BOTH fresh and resume modes — InsertEvolutions has no
// uniqueness constraint, so a --resume run would otherwise APPEND a duplicate
// curated edge set. The seed is small + deterministic; the truncate-then-load
// keeps the table in a known canonical state.
func (s *Store) ReplaceEvolutions(ctx context.Context, evos []model.Evolution) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM evolutions`); err != nil {
		return fmt.Errorf("clear evolutions: %w", err)
	}
	return s.InsertEvolutions(evos)
}

// ResetIngestLog drops every checkpoint row that doesn't match the current
// resume gate — invariant #2: when the parser / chunking / schema / subject
// footprint changes (the SpecIngestIdentity since plan PR-3), the on-disk log is
// no longer comparable so the resume contract must restart for those rows. A
// legacy DB stamped only with the old pipeline_version necessarily mismatches the
// SpecIngestIdentity and is wiped here — the "incompatible / migrate once" path.
func (s *Store) ResetIngestLog(ctx context.Context, currentPipelineVersion string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM ingest_log WHERE pipeline_version IS NULL OR pipeline_version <> ?`,
		currentPipelineVersion)
	return err
}

// MaxChunkID returns the largest chunk_id present in `clauses`, used on resume
// to continue the synthetic-key sequence without colliding with rows already
// inserted by an earlier attempt. Returns 0 on an empty table or any error
// (the caller treats that as "start at 1" which matches the fresh-run default).
func (s *Store) MaxChunkID(ctx context.Context) (uint64, error) {
	var n uint64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(chunk_id), 0) FROM clauses`).Scan(&n)
	return n, err
}

// ---- writes -------------------------------------------------------------

// UpsertSpec inserts or updates a catalogue entry.
func (s *Store) UpsertSpec(sp model.Spec) error {
	_, err := s.db.Exec(
		`INSERT INTO specs (spec_id, series, title, doc_type, working_group)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (spec_id) DO UPDATE SET
		   series=excluded.series, title=excluded.title,
		   doc_type=excluded.doc_type, working_group=excluded.working_group`,
		sp.SpecID, sp.Series, sp.Title, sp.DocType, sp.WorkingGroup)
	return err
}

// UpsertVersion inserts or updates a (spec, release, version) row.
func (s *Store) UpsertVersion(v model.SpecVersion) error {
	var freeze any
	if v.FreezeDate != nil {
		freeze = v.FreezeDate.Format("2006-01-02")
	}
	_, err := s.db.Exec(
		`INSERT INTO spec_versions (spec_id, release, version, freeze_date, docx_url)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (spec_id, release, version) DO UPDATE SET
		   freeze_date=excluded.freeze_date, docx_url=excluded.docx_url`,
		v.SpecID, v.Release, v.Version, freeze, v.DocxURL)
	return err
}

// InsertClauses bulk-inserts clauses in a single transaction. The embedding
// column is left NULL here; it is populated later by the embed phase.
func (s *Store) InsertClauses(clauses []model.Clause) error {
	if len(clauses) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(
		`INSERT INTO clauses
		 (chunk_id, spec_id, release, version, clause_path, heading, text, is_normative)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	for _, c := range clauses {
		if _, err := stmt.Exec(c.ChunkID, c.SpecID, c.Release, c.Version,
			c.ClausePath, c.Heading, c.Text, c.IsNormative); err != nil {
			_ = stmt.Close()
			_ = tx.Rollback()
			return fmt.Errorf("insert clause %d (%s %s): %w", c.ChunkID, c.SpecID, c.ClausePath, err)
		}
	}
	_ = stmt.Close()
	return tx.Commit()
}

// InsertChanges bulk-inserts change-history rows. clauses[] is packed via a
// separator param + string_split so we never bind a native array.
func (s *Store) InsertChanges(changes []model.Change) error {
	if len(changes) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(
		`INSERT INTO changes
		 (cr_number, cr_revision, spec_id, from_version, to_version, meeting,
		  category, clauses, summary, tdoc_url)
		 VALUES (?, ?, ?, ?, ?, ?, ?, string_split(?, '` + arraySep + `'), ?, ?)`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	for _, c := range changes {
		if _, err := stmt.Exec(c.CRNumber, c.CRRevision, c.SpecID, c.FromVersion,
			c.ToVersion, c.Meeting, c.Category, strings.Join(c.Clauses, arraySep),
			c.Summary, c.TDocURL); err != nil {
			_ = stmt.Close()
			_ = tx.Rollback()
			return err
		}
	}
	_ = stmt.Close()
	return tx.Commit()
}

// UpsertAcronym inserts or updates a glossary entry.
func (s *Store) UpsertAcronym(a model.Acronym) error {
	_, err := s.db.Exec(
		`INSERT INTO acronyms (term, expansion, domain, first_release, last_release, source_series)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (term, expansion, domain) DO UPDATE SET
		   first_release=excluded.first_release, last_release=excluded.last_release,
		   source_series=excluded.source_series`,
		a.Term, a.Expansion, a.Domain, a.FirstRelease, a.LastRelease, a.SourceSeries)
	return err
}

// InsertEvolutions bulk-inserts NE<->NF evolution edges (the V1 relational
// stand-in for the V2 KuzuDB graph).
func (s *Store) InsertEvolutions(evos []model.Evolution) error {
	if len(evos) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(
		`INSERT INTO evolutions
		 (from_term, to_term, evolution_type, justification_spec, justification_clause, confidence)
		 VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	for _, e := range evos {
		if _, err := stmt.Exec(e.FromTerm, e.ToTerm, e.EvolutionType,
			e.JustificationSpec, e.JustificationClause, e.Confidence); err != nil {
			_ = stmt.Close()
			_ = tx.Rollback()
			return err
		}
	}
	_ = stmt.Close()
	return tx.Commit()
}

// GetEvolutions returns evolution edges touching entity (as source or target).
func (s *Store) GetEvolutions(ctx context.Context, entity string) ([]model.Evolution, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT from_term, to_term, evolution_type, justification_spec, justification_clause, confidence
		 FROM evolutions WHERE upper(from_term) = upper(?) OR upper(to_term) = upper(?)
		 ORDER BY from_term, to_term`, entity, entity)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.Evolution
	for rows.Next() {
		var e model.Evolution
		if err := rows.Scan(&e.FromTerm, &e.ToTerm, &e.EvolutionType,
			&e.JustificationSpec, &e.JustificationClause, &e.Confidence); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---- LI events (authoritative, from TS 33.128 ASN.1) --------------------

// tx runs fn in a transaction, rolling back on error.
func (s *Store) tx(fn func(*sql.Tx) error) error {
	t, err := s.db.Begin()
	if err != nil {
		return err
	}
	if err := fn(t); err != nil {
		_ = t.Rollback()
		return err
	}
	return t.Commit()
}

// ---- FTS (best-effort) --------------------------------------------------

// EnableFTS loads the FTS extension and (re)builds the BM25 index on clauses.
// On any failure it leaves FTSAvailable() false and returns the error so the
// caller can log a degraded mode — it never panics the pipeline.
func (s *Store) EnableFTS(ctx context.Context) error {
	for _, q := range []string{`INSTALL fts`, `LOAD fts`} {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("fts %q: %w", q, err)
		}
	}
	// create_fts_index signature/named-arg syntax varies across DuckDB FTS
	// versions; try overwrite first, then the bare 4-arg form (fresh DB).
	var ferr error
	for _, q := range []string{
		`PRAGMA create_fts_index('clauses', 'chunk_id', 'heading', 'text', overwrite=1)`,
		`PRAGMA create_fts_index('clauses', 'chunk_id', 'heading', 'text')`,
	} {
		if _, ferr = s.db.ExecContext(ctx, q); ferr == nil {
			break
		}
	}
	if ferr != nil {
		return fmt.Errorf("create_fts_index: %w", ferr)
	}
	s.ftsAvailable = true
	return nil
}

// LoadFTS loads the FTS extension and marks BM25 available IF a persisted index
// already exists (built during ingest). It never rebuilds — cheap at serve time
// even on a multi-hundred-thousand-clause corpus. Returns an error (caller logs
// degraded mode) when no index is present.
func (s *Store) LoadFTS(ctx context.Context) error {
	for _, q := range []string{`INSTALL fts`, `LOAD fts`} {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("fts %q: %w", q, err)
		}
	}
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM information_schema.schemata WHERE schema_name = 'fts_main_clauses'`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("no persisted FTS index (ingest with --fts)")
	}
	s.ftsAvailable = true
	return nil
}

// BuildHNSW loads the VSS extension and builds the cosine HNSW index on
// embeddings. Best-effort: returns an error (caller logs degraded mode) but
// only makes sense once embeddings are populated.
func (s *Store) BuildHNSW(ctx context.Context) error {
	if err := s.EnableVSS(ctx); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`CREATE INDEX IF NOT EXISTS clauses_hnsw ON clauses USING HNSW (embedding) WITH (metric = 'cosine')`)
	return err
}

// EnableVSS loads the VSS extension so a persisted HNSW index can be queried
// and maintained. Best-effort; required at query time when embeddings exist
// (and before any DML on a table that already carries a persisted HNSW index).
func (s *Store) EnableVSS(ctx context.Context) error {
	for _, q := range []string{
		`INSTALL vss`, `LOAD vss`,
		`SET hnsw_enable_experimental_persistence = true`,
	} {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("vss %q: %w", q, err)
		}
	}
	return nil
}

// SetEmbedding stores a dense vector for a clause. The vector is passed as a
// DuckDB array literal cast to FLOAT[1024] (avoids native array binding).
func (s *Store) SetEmbedding(ctx context.Context, chunkID uint64, vec []float32) error {
	if len(vec) == 0 {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE clauses SET embedding = CAST(? AS FLOAT[1024]) WHERE chunk_id = ?`,
		vecLiteral(vec), chunkID)
	return err
}

// SetEmbeddingWithHash stores the vector AND the per-clause embedding hash in one
// UPDATE, so the (vector, hash) pair stays consistent. The hash is the fingerprint
// of (embedded text + model id): the decoupled embed step re-embeds a clause only
// when its current hash differs from the stored one, making repeat embed runs
// proportional to the clause delta rather than the whole spec/corpus.
func (s *Store) SetEmbeddingWithHash(ctx context.Context, chunkID uint64, vec []float32, hash string) error {
	if len(vec) == 0 {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE clauses SET embedding = CAST(? AS FLOAT[1024]), embedding_hash = ? WHERE chunk_id = ?`,
		vecLiteral(vec), hash, chunkID)
	return err
}

// SetEmbeddingsBatch writes a whole batch of (vector, hash) pairs in ONE
// transaction instead of one autocommit per clause. On DuckDB every autocommit
// UPDATE forces a WAL flush, so per-row writes dominate the embed wall-clock at
// scale; folding a window's writes into a single BEGIN…COMMIT amortises that to
// one flush per window. Each row's vector and hash still land in the same UPDATE,
// so a mid-window crash either has both or neither for a given clause — the resume
// marker is never torn from its vector. ids/vecs/hashes must be equal length; an
// empty vector skips that row.
func (s *Store) SetEmbeddingsBatch(ctx context.Context, ids []uint64, vecs [][]float32, hashes []string) error {
	if len(ids) == 0 {
		return nil
	}
	if len(vecs) != len(ids) || len(hashes) != len(ids) {
		return fmt.Errorf("SetEmbeddingsBatch: ragged input ids=%d vecs=%d hashes=%d", len(ids), len(vecs), len(hashes))
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx,
		`UPDATE clauses SET embedding = CAST(? AS FLOAT[1024]), embedding_hash = ? WHERE chunk_id = ?`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer func() { _ = stmt.Close() }()
	for i := range ids {
		if len(vecs[i]) == 0 {
			continue
		}
		if _, err := stmt.ExecContext(ctx, vecLiteral(vecs[i]), hashes[i], ids[i]); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// EmbedCandidate is a clause the embed step may need to (re)embed: the text to
// embed plus the hash currently stored against its vector ("" when never
// embedded). The caller recomputes the expected hash and skips rows where it
// already matches StoredHash, so only genuinely-changed clauses are embedded.
type EmbedCandidate struct {
	ChunkID    uint64
	Heading    string
	Text       string
	Release    string
	StoredHash string
}

// EmbedScan parametrises the work-list query of ClausesNeedingEmbedding. Pushing
// the floor / series / order / limit into SQL (rather than streaming the whole
// table into a Go slice and filtering there) is what keeps the embed step flat on
// a 10M-clause DB.
type EmbedScan struct {
	// FloorOrd > 0 restricts to clauses whose release recency >= FloorOrd (vectors
	// are recent-only; lexical coverage is unaffected). 0 = all releases.
	FloorOrd int
	// SeriesPrefix != "" restricts to spec_ids in that 2-digit series ("23"), so a
	// bounded Kaggle kernel can embed one series-shard at a time. "" = all series.
	SeriesPrefix string
	// Releases, when non-empty, restricts to clauses whose release label is in this
	// exact set ("Rel-19","Rel-16",…), so a balanced per-RELEASE-lot Kaggle kernel can
	// embed an arbitrary subset of releases concurrently with its sibling lot. Matched
	// verbatim against clauses.release (NOT a recency range), AND-combined with the
	// other predicates. nil/empty = all releases.
	Releases []string
	// Limit > 0 caps the work-list — a bounded session (Kaggle 12h cap) embeds the
	// top-Limit rows of the order below, then resumes next run. 0 = no limit.
	Limit int
	// OldestFirst flips the order to chunk_id ASC. Default (false) is
	// RECENT-RELEASE-FIRST (Rel-20 → … → Rel-99), so a session killed mid-run leaves
	// the most-recent — and most-queried — releases embedded, not a random prefix.
	OldestFirst bool
	// ResumeOnly returns ONLY rows with no VECTOR yet (embedding IS NULL) — the fast
	// resume path that fills gaps in a partial run. We key on the embedding column,
	// NOT embedding_hash: the vector is the ground truth for "embedded", and a partial
	// DB round-tripped through an external store (e.g. a Kaggle resume dataset) can
	// surface a stale/empty hash even when the vector is present — which would make a
	// hash-keyed resume re-embed everything. Default (false) streams all candidates so
	// embed.Apply's Go hash-compare still re-embeds a clause whose text OR model changed
	// (which SQL alone can't detect); that change-detection path is unaffected.
	ResumeOnly bool
}

// embeddableTextSQL is the predicate for "this clause CAN carry a dense vector":
// its text is non-empty after trimming. Heading-only / "void" / table-stripped
// clauses (parse.go emits a clause for a bare heading) have empty text and are
// skipped by the embedder itself (rust/embedder main.rs: text.trim().is_empty()).
// Counting them as "needs embedding" makes resume re-select the same rows forever
// and the completeness gate unreachable, so every needs-/lacks-embedding query
// ANDs this in. parse.go stores TrimSpace'd text, so DuckDB trim() (spaces only)
// matches the embedder's Unicode-whitespace trim in practice.
const embeddableTextSQL = `length(trim(text)) > 0`

// ClausesNeedingEmbedding streams the clauses that might need a vector. By default
// every clause with embeddable text is surfaced and the caller's hash-compare
// (embed.Apply) decides what to (re)embed — we keep the hash logic in one place
// (Go) so a model change, invisible to SQL, still re-embeds. ResumeOnly narrows to
// never-embedded rows for a fast resume. Empty-text clauses are excluded in both
// modes (the embedder cannot vectorise them). Rows are streamed in RECENT-RELEASE-
// FIRST order (the user-facing priority) unless OldestFirst is set. The caller must
// drain the rows.
func (s *Store) ClausesNeedingEmbedding(ctx context.Context, scan EmbedScan) (*sql.Rows, error) {
	var b strings.Builder
	b.WriteString(`SELECT chunk_id, heading, text, release, COALESCE(embedding_hash, '') FROM clauses`)
	var (
		conds []string
		args  []any
	)
	// Empty-text clauses can never be vectorised — exclude them in every mode so a
	// resume work-list converges to zero instead of re-selecting them each run.
	conds = append(conds, embeddableTextSQL)
	if scan.ResumeOnly {
		conds = append(conds, `embedding IS NULL`)
	}
	if scan.SeriesPrefix != "" {
		conds = append(conds, `substr(spec_id, 1, 2) = ?`)
		args = append(args, scan.SeriesPrefix)
	}
	if len(scan.Releases) > 0 {
		ph := make([]string, len(scan.Releases))
		for i, r := range scan.Releases {
			ph[i] = "?"
			args = append(args, r)
		}
		conds = append(conds, `release IN (`+strings.Join(ph, ", ")+`)`)
	}
	if scan.FloorOrd > 0 {
		conds = append(conds, `(`+releaseRecencySQL("release")+`) >= ?`)
		args = append(args, scan.FloorOrd)
	}
	if len(conds) > 0 {
		b.WriteString(" WHERE " + strings.Join(conds, " AND "))
	}
	if scan.OldestFirst {
		b.WriteString(" ORDER BY chunk_id ASC")
	} else {
		// Recency DESC then chunk_id ASC: a deterministic tiebreak so a --limit
		// prefix is stable across resume runs.
		b.WriteString(" ORDER BY (" + releaseRecencySQL("release") + ") DESC, chunk_id ASC")
	}
	if scan.Limit > 0 {
		fmt.Fprintf(&b, " LIMIT %d", scan.Limit)
	}
	return s.db.QueryContext(ctx, b.String(), args...)
}

// BackfillDuplicateEmbeddings copies an existing vector onto every still-unembedded
// clause whose (heading, text) is byte-identical to an already-embedded clause. 3GPP
// reproduces a clause verbatim across releases (and shares boilerplate like Foreword/
// Scope across specs), so on a DB that already holds SOME vectors this fills the
// duplicates for free instead of re-embedding them on the GPU. Idempotent: a clause that
// already has a vector is never touched, and a second call is a no-op. Returns the number
// of clauses backfilled. Safe on a DB with no vectors yet (affects nothing).
//
// This is the cross-release/cross-series complement to the embedder's intra-work-list
// dedup: run it on the merged corpus (or before exporting a delta work-list) so a future
// incremental run never re-embeds a text whose twin is already vectorised.
func (s *Store) BackfillDuplicateEmbeddings(ctx context.Context) (int64, error) {
	// Pick ONE donor row per (heading,text) group via row_number, not two independent
	// any_value() aggregates: any_value(embedding) and any_value(embedding_hash) carry
	// no same-row guarantee, so on a (rare) mixed-identity group they could pair a
	// vector from one identity with a hash from another — a corrupt (vector,hash) pair.
	// Selecting a single donor row keeps the copied embedding and its hash consistent.
	res, err := s.db.ExecContext(ctx, `
		UPDATE clauses
		SET embedding = d.embedding, embedding_hash = d.embedding_hash
		FROM (
			SELECT heading, text, embedding, embedding_hash
			FROM (
				SELECT heading, text, embedding, embedding_hash,
				       row_number() OVER (PARTITION BY heading, text) AS rn
				FROM clauses
				WHERE embedding IS NOT NULL AND `+embeddableTextSQL+`
			)
			WHERE rn = 1
		) d
		WHERE clauses.embedding IS NULL
		  AND clauses.heading = d.heading
		  AND clauses.text = d.text`)
	if err != nil {
		return 0, fmt.Errorf("backfill duplicate embeddings: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// Reset truncates all data tables (full deterministic rebuild).
func (s *Store) Reset(ctx context.Context) error {
	for _, t := range []string{"clauses", "changes", "spec_versions", "specs", "acronyms", "evolutions",
		"li_events", "li_event_fields", "li_nf_clauses", "api_operations", "api_schemas", "releases",
		"asn1_types"} {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM `+t); err != nil {
			return fmt.Errorf("reset %s: %w", t, err)
		}
	}
	return nil
}

// ---- reads --------------------------------------------------------------

// SpecFilter narrows catalogue and clause queries.
type SpecFilter struct {
	Release      string // "Rel-18"
	Series       string // "33"
	DocType      string // "TS" | "TR"
	WorkingGroup string
	SpecID       string
}

// SearchQuery parameterises a retrieval call.
type SearchQuery struct {
	Text   string
	Filter SpecFilter
	TopK   int
}

// SearchClauses runs lexical retrieval: BM25 if the FTS index is live, else a
// LIKE-scored fallback. Results carry citations.
func (s *Store) SearchClauses(ctx context.Context, q SearchQuery) ([]model.SearchHit, error) {
	if q.TopK <= 0 {
		q.TopK = 10
	}
	filterSQL, filterArgs := filterClause(q.Filter)

	var sql string
	var args []any
	if s.ftsAvailable {
		sql = `SELECT chunk_id, spec_id, release, version, clause_path, heading, text, is_normative, score FROM (
			SELECT chunk_id, spec_id, release, version, clause_path, heading, text, is_normative,
			       fts_main_clauses.match_bm25(chunk_id, ?) AS score
			FROM clauses) sq WHERE score IS NOT NULL` + filterSQL +
			` ORDER BY score DESC, spec_id, clause_path LIMIT ?`
		args = append(args, q.Text)
		args = append(args, filterArgs...)
		args = append(args, q.TopK)
	} else {
		// LIKE fallback, tokenised like BM25: a clause matches if ANY query word
		// appears (heading weighted 2, text 1); score = number of word matches.
		toks := likeTokens(q.Text)
		var score, whereC strings.Builder
		var scoreArgs, whereArgs []any
		for i, tk := range toks {
			like := "%" + tk + "%"
			if i > 0 {
				score.WriteString(" + ")
				whereC.WriteString(" OR ")
			}
			score.WriteString(`(CASE WHEN lower(heading) LIKE ? THEN 2.0 ELSE 0 END + CASE WHEN lower(text) LIKE ? THEN 1.0 ELSE 0 END)`)
			whereC.WriteString(`lower(heading) LIKE ? OR lower(text) LIKE ?`)
			scoreArgs = append(scoreArgs, like, like)
			whereArgs = append(whereArgs, like, like)
		}
		sql = `SELECT chunk_id, spec_id, release, version, clause_path, heading, text, is_normative, (` +
			score.String() + `) AS score FROM clauses WHERE (` + whereC.String() + `)` + filterSQL +
			` ORDER BY score DESC, spec_id, clause_path LIMIT ?`
		args = append(args, scoreArgs...)
		args = append(args, whereArgs...)
		args = append(args, filterArgs...)
		args = append(args, q.TopK)
	}

	rows, err := s.db.QueryContext(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanHits(rows)
}

// ClauseRel is a clause and the set of releases it appears in.
type ClauseRel struct {
	Path     string   `json:"clause_path"`
	Heading  string   `json:"heading"`
	Releases []string `json:"releases"`
}

// ClauseAvailability returns, for a spec (optionally under a clause prefix),
// every clause path with the releases it appears in, plus the spec's releases
// ordered oldest-first. This is the basis for release-scoped answers and the
// forward/backward delta annexes (what a later release adds, what the target
// release introduced).
func (s *Store) ClauseAvailability(ctx context.Context, specID, prefix string) ([]ClauseRel, []string, error) {
	ordered, err := s.releasesOrdered(ctx, specID)
	if err != nil {
		return nil, nil, err
	}
	// On a content-addressed corpus this question never touches the text: which
	// releases carry a clause IS clause_occ. Strictly less work than the scan
	// below, and the reason the migration makes lineage exact rather than
	// derived.
	if s.contentAddressed {
		out, aErr := s.availabilityCA(ctx, specID, prefix)
		return out, ordered, aErr
	}
	q := `SELECT clause_path, max(heading), list(DISTINCT release)
	      FROM clauses WHERE spec_id = ?`
	args := []any{specID}
	if prefix != "" {
		q += ` AND (clause_path = ? OR clause_path LIKE ?)`
		args = append(args, prefix, prefix+".%")
	}
	q += ` GROUP BY clause_path ORDER BY len(clause_path), clause_path`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ClauseRel
	for rows.Next() {
		var cr ClauseRel
		var rels []any
		if err := rows.Scan(&cr.Path, &cr.Heading, &rels); err != nil {
			return nil, nil, err
		}
		for _, r := range rels {
			if rs, ok := r.(string); ok {
				cr.Releases = append(cr.Releases, rs)
			}
		}
		out = append(out, cr)
	}
	return out, ordered, rows.Err()
}

// ClauseLineage returns, per clause path under prefix, its release lineage
// (present_in oldest-first, introduced, last_seen, obsolete-vs-newest-release).
func (s *Store) ClauseLineage(ctx context.Context, specID, prefix string) (map[string]model.Lineage, error) {
	avail, ordered, err := s.ClauseAvailability(ctx, specID, prefix)
	if err != nil {
		return nil, err
	}
	rank := make(map[string]int, len(ordered))
	for i, r := range ordered {
		rank[r] = i
	}
	newest := ""
	if len(ordered) > 0 {
		newest = ordered[len(ordered)-1]
	}
	out := make(map[string]model.Lineage, len(avail))
	for _, cr := range avail {
		present := append([]string(nil), cr.Releases...)
		sort.Slice(present, func(i, j int) bool { return rank[present[i]] < rank[present[j]] })
		if len(present) == 0 {
			continue
		}
		last := present[len(present)-1]
		out[cr.Path] = model.Lineage{
			PresentIn:  present,
			Introduced: present[0],
			LastSeen:   last,
			Obsolete:   newest != "" && last != newest,
		}
	}
	return out, nil
}

// releaseRecencySQL builds a SQL expression yielding a release label's recency
// ordinal, mirroring model.ReleaseOrdinal: 'Rel-99' -> 3 (the OLDEST 3GPP
// release — Release 1999, version major 3), 'Rel-4'..'Rel-20' -> 4..20, and
// everything else (drafts, GSM Phase-1/2, unparseable) -> -1 (sorted oldest).
//
// A bare regexp_extract(release,'[0-9]+') is WRONG here: 'Rel-99' carries the
// digits 99 and would sort as the NEWEST release, when it is in fact the oldest.
// That naive form previously sat in releasesOrdered + ListReleases; this is the
// single correct definition. col must be a trusted column identifier (the caller
// controls it — never interpolate user input here).
func releaseRecencySQL(col string) string {
	return `CASE
		WHEN ` + col + ` = 'Rel-99' THEN 3
		WHEN regexp_full_match(` + col + `, 'Rel-[0-9]+')
		     AND CAST(regexp_extract(` + col + `, '[0-9]+') AS INTEGER) >= 4
		  THEN CAST(regexp_extract(` + col + `, '[0-9]+') AS INTEGER)
		ELSE -1
	END`
}

// versionRecencySQL orders a dotted "X.Y.Z" version NEWEST-first NUMERICALLY.
// A raw `version DESC` is a STRING sort, so "18.9.0" wrongly sorts AFTER "18.10.0"
// (9 > 1 lexically) → LatestVersion/VersionForRelease/get_changelog/get_spec would
// cite a STALE version once a release reaches its 10th minor/patch. Compare each
// component as an integer (TRY_CAST → NULL on a non-numeric/missing part, coalesced
// to 0). col must be a trusted column identifier (never user input).
func versionRecencySQL(col string) string { return versionOrderSQL(col, "DESC") }

// versionOrderSQL is versionRecencySQL with an explicit direction ("DESC" =
// newest-first, "ASC" = oldest-first). dir must be a literal "ASC"/"DESC" set by
// the caller, never user input.
func versionOrderSQL(col, dir string) string {
	c := func(n int) string {
		return `COALESCE(TRY_CAST(split_part(` + col + `, '.', ` + strconv.Itoa(n) + `) AS INTEGER), 0) ` + dir
	}
	return c(1) + `, ` + c(2) + `, ` + c(3)
}

// releasesOrdered returns a spec's releases oldest-first by release recency
// (Rel-99 first, then Rel-4..Rel-20), via the shared releaseRecencySQL.
func (s *Store) releasesOrdered(ctx context.Context, specID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT release FROM spec_versions WHERE spec_id = ?
		 ORDER BY `+releaseRecencySQL("release"), specID)
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
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetClauses returns clauses for a spec/version, optionally restricted to a
// clause-path prefix (e.g. "6.2" for one NF section), ordered by clause path.
func (s *Store) GetClauses(ctx context.Context, specID, version, clausePrefix string) ([]model.Clause, error) {
	if s.contentAddressed {
		return s.getClausesCA(ctx, specID, version, clausePrefix)
	}
	q := `SELECT chunk_id, spec_id, release, version, clause_path, heading, text, is_normative
	      FROM clauses WHERE spec_id = ?`
	args := []any{specID}
	if version != "" {
		q += ` AND version = ?`
		args = append(args, version)
	}
	if clausePrefix != "" {
		q += ` AND (clause_path = ? OR clause_path LIKE ?)`
		args = append(args, clausePrefix, clausePrefix+".%")
	}
	q += ` ORDER BY len(clause_path), clause_path`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanClauses(rows)
}

// ListSpecs returns the catalogue, filtered.
func (s *Store) ListSpecs(ctx context.Context, f SpecFilter) ([]model.Spec, error) {
	q := `SELECT spec_id, series, title, doc_type, working_group FROM specs WHERE 1=1`
	var args []any
	if f.Series != "" {
		q += ` AND series = ?`
		args = append(args, f.Series)
	}
	if f.DocType != "" {
		q += ` AND doc_type = ?`
		args = append(args, f.DocType)
	}
	if f.WorkingGroup != "" {
		q += ` AND working_group = ?`
		args = append(args, f.WorkingGroup)
	}
	if f.Release != "" {
		q += ` AND spec_id IN (SELECT spec_id FROM spec_versions WHERE release = ?)`
		args = append(args, f.Release)
	}
	q += ` ORDER BY series, spec_id`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.Spec
	for rows.Next() {
		var sp model.Spec
		if err := rows.Scan(&sp.SpecID, &sp.Series, &sp.Title, &sp.DocType, &sp.WorkingGroup); err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

// ListReleases returns all (release, version, freeze_date) of a spec, ordered
// newest-first by release ordinal then version then freeze date (CLAUDE.md §8.3).
func (s *Store) ListReleases(ctx context.Context, specID string) ([]model.SpecVersion, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT spec_id, release, version, freeze_date, docx_url
		 FROM spec_versions WHERE spec_id = ?
		 ORDER BY `+releaseRecencySQL("release")+` DESC,
		          `+versionRecencySQL("version")+`, freeze_date DESC NULLS LAST`, specID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.SpecVersion
	for rows.Next() {
		var v model.SpecVersion
		var freeze sql.NullTime
		if err := rows.Scan(&v.SpecID, &v.Release, &v.Version, &freeze, &v.DocxURL); err != nil {
			return nil, err
		}
		if freeze.Valid {
			t := freeze.Time
			v.FreezeDate = &t
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// LatestVersion returns the newest (release, version) of a spec, PREFERRING the
// latest STABLE (published) version over a newer DRAFT. ListReleases is
// newest-first, so the first stable entry is the latest stable; only when a spec
// has no stable version at all (drafts only) do we fall back to the newest draft.
// This is what makes get_spec default to stable content (CLAUDE.md §8 piège #8:
// e.g. prefer 23.501 v19.x over a freshly-opened v2.0.0 draft of a later release).
func (s *Store) LatestVersion(ctx context.Context, specID string) (release, version string, ok bool, err error) {
	vs, err := s.ListReleases(ctx, specID)
	if err != nil {
		return "", "", false, err
	}
	if len(vs) == 0 {
		return "", "", false, nil
	}
	for _, v := range vs {
		if model.IsStableVersion(v.Version) {
			return v.Release, v.Version, true, nil
		}
	}
	return vs[0].Release, vs[0].Version, true, nil // drafts-only spec: best available
}

// VersionForRelease returns the highest version of a spec within a release
// (e.g. "Rel-17" -> "17.20.0"). An empty release falls back to the overall
// latest version. ok is false when nothing matches.
func (s *Store) VersionForRelease(ctx context.Context, specID, release string) (string, bool, error) {
	if release == "" {
		_, v, ok, err := s.LatestVersion(ctx, specID)
		return v, ok, err
	}
	vs, err := s.ListReleases(ctx, specID) // newest-first
	if err != nil {
		return "", false, err
	}
	// Prefer the highest STABLE version in the release; fall back to the highest
	// version overall only when the release has drafts only (best available).
	fallback, haveFallback := "", false
	for _, v := range vs {
		if v.Release != release {
			continue
		}
		if model.IsStableVersion(v.Version) {
			return v.Version, true, nil
		}
		if !haveFallback {
			fallback, haveFallback = v.Version, true // first (highest) match in the release
		}
	}
	return fallback, haveFallback, nil
}

// ResolveTerm returns glossary entries for a term (case-insensitive).
func (s *Store) ResolveTerm(ctx context.Context, term string) ([]model.Acronym, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT term, expansion, domain, first_release, last_release
		 FROM acronyms WHERE lower(term) = lower(?) ORDER BY domain`, term)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.Acronym
	for rows.Next() {
		var a model.Acronym
		if err := rows.Scan(&a.Term, &a.Expansion, &a.Domain, &a.FirstRelease, &a.LastRelease); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetChangelog lists change records for a spec between two releases (inclusive),
// ordered by target version.
func (s *Store) GetChangelog(ctx context.Context, specID, fromRel, toRel string) ([]model.Change, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT cr_number, cr_revision, spec_id, from_version, to_version,
		        meeting, category, clauses, summary, tdoc_url
		 FROM changes WHERE spec_id = ? ORDER BY `+versionOrderSQL("to_version", "ASC"), specID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.Change
	for rows.Next() {
		var c model.Change
		var clauses []any
		if err := rows.Scan(&c.CRNumber, &c.CRRevision, &c.SpecID, &c.FromVersion,
			&c.ToVersion, &c.Meeting, &c.Category, &clauses, &c.Summary, &c.TDocURL); err != nil {
			return nil, err
		}
		for _, v := range clauses {
			if sv, ok := v.(string); ok {
				c.Clauses = append(c.Clauses, sv)
			}
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CountSpecs / CountClauses are small helpers for ingest reporting and tests.
func (s *Store) CountSpecs(ctx context.Context) (int, error)   { return s.count(ctx, "specs") }
func (s *Store) CountClauses(ctx context.Context) (int, error) { return s.count(ctx, "clauses") }

// CountSpecVersions / CountAPIOperations expose the two corpus-coverage counters
// the CI pre-publish guard reads (PR-1): a delta publish must never SHRINK either
// vs the base it is replacing. They are the most direct "did we lose normative
// rows" / "did we lose API rows" signals — spec_versions tracks every indexed
// (spec, release, version); api_operations tracks every 5GC OpenAPI operation.
func (s *Store) CountSpecVersions(ctx context.Context) (int, error) {
	return s.count(ctx, "spec_versions")
}
func (s *Store) CountAPIOperations(ctx context.Context) (int, error) {
	return s.count(ctx, "api_operations")
}

func (s *Store) count(ctx context.Context, table string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&n)
	return n, err
}

// CountNullEmbeddings returns how many EMBEDDABLE clauses have no embedding (the
// authoritative post-ingest check that the embedder actually populated vectors —
// st.Vectors is only a write-side flag). Empty-text clauses are excluded: they are
// correctly NULL by design (the embedder skips them), so counting them would make
// this never reach zero.
func (s *Store) CountNullEmbeddings(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM clauses WHERE embedding IS NULL AND `+embeddableTextSQL).Scan(&n)
	return n, err
}

// Checkpoint forces a DuckDB CHECKPOINT so in-progress embed writes are flushed
// durably to the file. A bounded embed session killed by the Kaggle 12h cap then
// resumes from the last checkpoint (the embedding_hash markers survive) instead of
// losing everything written since the run began.
func (s *Store) Checkpoint(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `CHECKPOINT`)
	return err
}

// CountNullAtFloor counts clauses that SHOULD carry a vector but do not: those at/
// above the release floor and, when seriesPrefix != "", in that 2-digit series,
// still NULL. floorOrd <= 0 means "all releases". This is the series-aware
// completeness oracle the per-shard CI gate keys on — unlike the global
// CountNullEmbeddings it never counts intentionally-skipped below-floor or
// other-series clauses as a failure.
func (s *Store) CountNullAtFloor(ctx context.Context, floorOrd int, seriesPrefix string) (int, error) {
	var b strings.Builder
	b.WriteString(`SELECT count(*) FROM clauses WHERE embedding IS NULL AND ` + embeddableTextSQL)
	var args []any
	if seriesPrefix != "" {
		b.WriteString(` AND substr(spec_id, 1, 2) = ?`)
		args = append(args, seriesPrefix)
	}
	if floorOrd > 0 {
		b.WriteString(` AND (` + releaseRecencySQL("release") + `) >= ?`)
		args = append(args, floorOrd)
	}
	var n int
	err := s.db.QueryRowContext(ctx, b.String(), args...).Scan(&n)
	return n, err
}

// NullEmbeddingsByRelease returns, per release, how many clauses still lack a
// vector. The caller sums only at/above its embed floor: a floored embed run
// LEGITIMATELY leaves below-floor clauses NULL, so the global
// CountNullEmbeddings never reaches 0 and is the wrong completeness signal for
// a floored build. Releases are not SQL-orderable (Rel-99 sorts before Rel-4),
// so the floor comparison is done in Go via model.ReleaseOrdinal.
func (s *Store) NullEmbeddingsByRelease(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT COALESCE(release, '') AS release, count(*)
		   FROM clauses WHERE embedding IS NULL AND `+embeddableTextSQL+` GROUP BY release`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string]int)
	for rows.Next() {
		var rel string
		var n int
		if err := rows.Scan(&rel, &n); err != nil {
			return nil, err
		}
		out[rel] = n
	}
	return out, rows.Err()
}

// vecOverFetch / vecMaxFetch bound how many extra neighbours SearchVectors pulls
// when a SpecFilter is set, so the Go post-filter can still return topK.
const (
	vecOverFetch = 8
	vecMaxFetch  = 500
)

// SearchVectors runs HNSW cosine k-NN over clause embeddings (Phase 4). The SQL
// is deliberately the bare `ORDER BY array_cosine_distance(col, q) ASC LIMIT n`
// shape with NO WHERE: that is the ONLY form DuckDB VSS accelerates with the
// HNSW index. A WHERE predicate or `1 - dist DESC` makes the planner fall back
// to a full sequential scan over every vector — so the SpecFilter is applied as
// a Go post-filter over an over-fetched neighbour set instead. NULL-embedding
// rows are dropped in Go (they sort last and carry a NULL distance).
func (s *Store) SearchVectors(ctx context.Context, vec []float32, f SpecFilter, topK int) ([]model.SearchHit, error) {
	if topK <= 0 {
		topK = 10
	}
	fetch := topK
	if !f.IsZero() {
		if fetch = topK * vecOverFetch; fetch > vecMaxFetch {
			fetch = vecMaxFetch
		}
	}
	const q = `SELECT chunk_id, spec_id, release, version, clause_path, heading, text, is_normative,
	       array_cosine_distance(embedding, CAST(? AS FLOAT[1024])) AS dist
	      FROM clauses
	      ORDER BY dist ASC
	      LIMIT ?`
	// DocType can't be a WHERE on the k-NN (it would defeat the index) and needs a
	// specs join — resolve the allowed spec_ids once, then enforce in the post-filter.
	allowed, err := s.specIDsForDocType(ctx, f.DocType)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, q, vecLiteral(vec), fetch)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanVecHits(rows, f, allowed, topK)
}

// specIDsForDocType returns the set of spec_ids of the given doc_type ("TS"|"TR"),
// or nil when docType is empty (no constraint). Used by the vector post-filter,
// where DocType can't be pushed into the index-eligible k-NN SQL.
func (s *Store) specIDsForDocType(ctx context.Context, docType string) (map[string]struct{}, error) {
	if docType == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT spec_id FROM specs WHERE doc_type = ?`, docType)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	allowed := map[string]struct{}{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		allowed[id] = struct{}{}
	}
	return allowed, rows.Err()
}

// SearchVectorsAmong ranks vec by exact cosine ONLY over candidateIDs (e.g. the
// BM25 top-N chunk_ids). This is the no-HNSW fallback: O(len(candidateIDs)), a
// bounded exact scan — never the full corpus. The WHERE+IN shape intentionally
// does NOT use the HNSW index (none is available in this path).
func (s *Store) SearchVectorsAmong(ctx context.Context, vec []float32, candidateIDs []uint64, topK int) ([]model.SearchHit, error) {
	if topK <= 0 {
		topK = 10
	}
	if len(candidateIDs) == 0 {
		return nil, nil
	}
	ph := make([]string, len(candidateIDs))
	args := make([]any, 0, len(candidateIDs)+2)
	args = append(args, vecLiteral(vec))
	for i, id := range candidateIDs {
		ph[i] = "?"
		args = append(args, id)
	}
	args = append(args, topK)
	q := `SELECT chunk_id, spec_id, release, version, clause_path, heading, text, is_normative,
	       1.0 - array_cosine_distance(embedding, CAST(? AS FLOAT[1024])) AS score
	      FROM clauses
	      WHERE embedding IS NOT NULL AND chunk_id IN (` + strings.Join(ph, ",") + `)
	      ORDER BY score DESC LIMIT ?`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanHits(rows)
}

// vecLiteral renders a float vector as a DuckDB array literal "[a,b,...]".
func vecLiteral(vec []float32) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, f := range vec {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(f), 'g', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}

// ---- helpers ------------------------------------------------------------

// filterClause builds the shared SQL fragment + args for a SpecFilter. The
// returned string starts with " AND ..." (or is empty) so it can be appended.
func filterClause(f SpecFilter) (string, []any) {
	var sb strings.Builder
	var args []any
	if f.SpecID != "" {
		sb.WriteString(` AND spec_id = ?`)
		args = append(args, f.SpecID)
	}
	if f.Release != "" {
		sb.WriteString(` AND release = ?`)
		args = append(args, f.Release)
	}
	if f.Series != "" {
		sb.WriteString(` AND spec_id LIKE ?`)
		args = append(args, f.Series+".%")
	}
	if f.DocType != "" {
		sb.WriteString(` AND spec_id IN (SELECT spec_id FROM specs WHERE doc_type = ?)`)
		args = append(args, f.DocType)
	}
	return sb.String(), args
}

// IsZero reports whether the filter constrains nothing.
func (f SpecFilter) IsZero() bool {
	return f.SpecID == "" && f.Release == "" && f.Series == "" && f.DocType == "" && f.WorkingGroup == ""
}

// matchFilter applies a SpecFilter to a clause in Go (the vector k-NN path keeps
// its SQL WHERE-free so the HNSW index fires; filtering happens after over-fetch).
// allowed is the spec_id set for f.DocType (nil when DocType is unset), resolved
// once by the caller — so an explicit spec_type constraint can't leak through the
// vector list into the RRF-fused result.
func matchFilter(c model.Clause, f SpecFilter, allowed map[string]struct{}) bool {
	if f.SpecID != "" && c.SpecID != f.SpecID {
		return false
	}
	if f.Release != "" && c.Release != f.Release {
		return false
	}
	if f.Series != "" && !strings.HasPrefix(c.SpecID, f.Series+".") {
		return false
	}
	if f.DocType != "" {
		if _, ok := allowed[c.SpecID]; !ok {
			return false
		}
	}
	return true
}

// likeTokens lowercases a query and splits it into distinct word tokens (len>=2)
// for the LIKE fallback, capped to keep the SQL bounded. Empty input falls back
// to the whole (lowered) string.
func likeTokens(q string) []string {
	isAlnum := func(r rune) bool { return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') }
	fields := strings.FieldsFunc(strings.ToLower(q), func(r rune) bool { return !isAlnum(r) })
	seen := map[string]bool{}
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if len(f) >= 2 && !seen[f] {
			seen[f] = true
			out = append(out, f)
			if len(out) == 12 {
				break
			}
		}
	}
	if len(out) == 0 {
		out = []string{strings.ToLower(strings.TrimSpace(q))}
	}
	return out
}

func scanClauses(rows *sql.Rows) ([]model.Clause, error) {
	var out []model.Clause
	for rows.Next() {
		var c model.Clause
		if err := rows.Scan(&c.ChunkID, &c.SpecID, &c.Release, &c.Version,
			&c.ClausePath, &c.Heading, &c.Text, &c.IsNormative); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func scanHits(rows *sql.Rows) ([]model.SearchHit, error) {
	var out []model.SearchHit
	for rows.Next() {
		var c model.Clause
		var score float64
		if err := rows.Scan(&c.ChunkID, &c.SpecID, &c.Release, &c.Version,
			&c.ClausePath, &c.Heading, &c.Text, &c.IsNormative, &score); err != nil {
			return nil, err
		}
		out = append(out, model.SearchHit{Clause: c, Score: score, Citation: c.Cite()})
	}
	return out, rows.Err()
}

// scanVecHits reads the bare-k-NN rows (distance, not score), drops NULL-embedding
// rows (NULL distance), applies the SpecFilter in Go, converts distance→cosine
// similarity, and stops at topK. See SearchVectors for why the SQL is WHERE-free.
func scanVecHits(rows *sql.Rows, f SpecFilter, allowed map[string]struct{}, topK int) ([]model.SearchHit, error) {
	out := make([]model.SearchHit, 0, topK)
	for rows.Next() {
		var c model.Clause
		var dist sql.NullFloat64
		if err := rows.Scan(&c.ChunkID, &c.SpecID, &c.Release, &c.Version,
			&c.ClausePath, &c.Heading, &c.Text, &c.IsNormative, &dist); err != nil {
			return nil, err
		}
		if !dist.Valid || !matchFilter(c, f, allowed) {
			continue
		}
		out = append(out, model.SearchHit{Clause: c, Score: 1.0 - dist.Float64, Citation: c.Cite()})
		if len(out) >= topK {
			break
		}
	}
	return out, rows.Err()
}
