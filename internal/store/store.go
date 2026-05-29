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
	db           *sql.DB
	ftsAvailable bool
	vssAvailable bool
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
// pipeline_version stamps the row so a later algorithm change invalidates
// every row (the caller checks model.PipelineVersion at resume time).
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

// IsIngestDone reports whether (spec, version) finished cleanly under the
// SAME pipeline_version — the gate the resume loop uses to skip work.
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
func (s *Store) PurgeSpecScope(ctx context.Context, specID, version string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM clauses WHERE spec_id=? AND version=?`, specID, version); err != nil {
		return fmt.Errorf("purge clauses: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM changes WHERE spec_id=? AND to_version=?`, specID, version); err != nil {
		return fmt.Errorf("purge changes: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM spec_versions WHERE spec_id=? AND version=?`, specID, version); err != nil {
		return fmt.Errorf("purge spec_versions: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM ingest_log WHERE spec_id=? AND version=?`, specID, version); err != nil {
		return fmt.Errorf("purge ingest_log row: %w", err)
	}
	return nil
}

// ResetIngestLog drops every checkpoint row that doesn't match the current
// pipeline_version — invariant #2: when the chunker / embedder / schema
// changes, the on-disk log is no longer comparable so the resume contract
// must restart from scratch.
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
		`INSERT INTO acronyms (term, expansion, domain, first_release, last_release)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (term, expansion, domain) DO UPDATE SET
		   first_release=excluded.first_release, last_release=excluded.last_release`,
		a.Term, a.Expansion, a.Domain, a.FirstRelease, a.LastRelease)
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

// releasesOrdered returns a spec's releases oldest-first by major ordinal.
func (s *Store) releasesOrdered(ctx context.Context, specID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT release FROM spec_versions WHERE spec_id = ?
		 ORDER BY CAST(regexp_extract(release, '[0-9]+') AS INTEGER)`, specID)
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
		 ORDER BY CAST(regexp_extract(release, '[0-9]+') AS INTEGER) DESC,
		          version DESC, freeze_date DESC NULLS LAST`, specID)
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

// LatestVersion returns the newest (release, version) of a spec using the same
// ordering as ListReleases. ok is false when the spec is unknown.
func (s *Store) LatestVersion(ctx context.Context, specID string) (release, version string, ok bool, err error) {
	vs, err := s.ListReleases(ctx, specID)
	if err != nil {
		return "", "", false, err
	}
	if len(vs) == 0 {
		return "", "", false, nil
	}
	return vs[0].Release, vs[0].Version, true, nil
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
	for _, v := range vs {
		if v.Release == release {
			return v.Version, true, nil // first match = highest version in that release
		}
	}
	return "", false, nil
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
		 FROM changes WHERE spec_id = ? ORDER BY to_version`, specID)
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

func (s *Store) count(ctx context.Context, table string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&n)
	return n, err
}

// CountNullEmbeddings returns how many clauses have no embedding (the authoritative
// post-ingest check that the embedder actually populated vectors — st.Vectors is
// only a write-side flag).
func (s *Store) CountNullEmbeddings(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM clauses WHERE embedding IS NULL`).Scan(&n)
	return n, err
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
