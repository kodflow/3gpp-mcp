package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
)

// HNSW "build-then-freeze" (axis #6): the index is a disposable cache over the
// clauses.embedding source of truth. Ingest builds + verifies + freezes it once;
// serve opens read-only and only reads it, degrading to an exact scan if it is
// missing/stale. These methods add the CHECKPOINT fencing, verification, and
// freeze markers around the raw BuildHNSW/EnableVSS already in store.go.

// VectorMetaKeys is the complete set of schema_meta keys that describe vector
// capability. BuildAndFreezeHNSW WRITES every one (except hnsw_state, which it
// flips last as the gate) and a strip-to-lexical path must DELETE every one — so
// the slim DB never advertises semantic capability it can no longer realise.
// Sourcing both the write and the purge from this single slice is what stops the
// two lists from drifting (the omission that left hnsw_metric behind:
// hnsw-metric-omitted-from-strip-cleanup / strip-meta-test-tautological).
var VectorMetaKeys = []string{
	"hnsw_state", "hnsw_metric", "embedding_dim", "embedding_count", "embedding_model",
}

// OpenReadOnly opens the DuckDB file in read-only mode — the serve posture: no
// writes ⇒ no WAL ⇒ the unsupported "custom index + WAL replay" path can't occur.
func OpenReadOnly(path string) (*Store, error) {
	db, err := sql.Open("duckdb", path+"?access_mode=read_only")
	if err != nil {
		return nil, fmt.Errorf("open duckdb read-only %q: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	ctx := context.Background()
	// Read-only: schema already exists; just detect FTS presence like Open does.
	_ = s.LoadFTS(ctx)
	// Same idea one shape up: whether this corpus stores its text
	// content-addressed decides which query the availability and lineage paths
	// run. etsi.duckdb is served alongside and has NOT been migrated, so this is
	// asked of each database rather than assumed of the build.
	s.probeContentAddressed(ctx)
	// Producer marker (migration Phase 11a A14): a DB built by the Rust write-side stamps
	// schema_meta.producer + .schema_version. Warn (never fail) on a schema_version the
	// read side wasn't built for — a self-describing guard that the served corpus matches.
	if v := s.GetMeta(ctx, "schema_version"); v != "" && v != ExpectedSchemaVersion {
		fmt.Fprintf(os.Stderr, "[store] WARNING: corpus schema_version=%q != expected %q (producer=%q) — read side may be stale\n",
			v, ExpectedSchemaVersion, s.GetMeta(ctx, "producer"))
	}
	return s, nil
}

// ExpectedSchemaVersion is the schema_meta.schema_version the read side is built for; a
// producer (Go ingest or the Rust write-side) stamps the DB with its version.
const ExpectedSchemaVersion = "1"

// VSSAvailable reports whether a verified, frozen HNSW index is usable. When
// false, vector search degrades to an exact full-scan (still correct, just O(N)).
func (s *Store) VSSAvailable() bool { return s.vssAvailable }

// DisableVSS turns vector search off at serve time (e.g. when the client embedder
// disagrees with the DB's indexed model — see the coherence guard in cmd/server).
// Lexical retrieval is unaffected.
func (s *Store) DisableVSS() { s.vssAvailable = false }

// GetMeta reads a schema_meta value ("" if absent).
func (s *Store) GetMeta(ctx context.Context, key string) string {
	var v string
	_ = s.db.QueryRowContext(ctx, `SELECT value FROM schema_meta WHERE key = ?`, key).Scan(&v)
	return v
}

// HNSWIndexPresent reports whether the corpus actually CARRIES the vector index,
// rather than merely claiming it in schema_meta.
//
// hnsw_state is a row in schema_meta, and rows travel. `COPY FROM DATABASE` — which
// merge uses to compact the corpus — copies the data and deliberately does NOT copy
// custom indexes, so the flag arrives at the new file saying "frozen" while the
// index it describes was left behind. Anything that trusts the flag then reports a
// vector-searchable corpus that can only answer lexically.
//
// This is the same failure `smoke` exists to catch, and smoke runs at the very end
// of the pipeline, after validate. A guard that reads the claim instead of the fact
// is not a guard.
func (s *Store) HNSWIndexPresent(ctx context.Context) bool {
	var n int
	idx, _ := s.hnswTarget()
	err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM duckdb_indexes() WHERE index_name = ?`, idx).Scan(&n)
	return err == nil && n > 0
}

// BuildAndFreezeHNSW runs the non-negotiable build sequence (axis #6 §2):
// CHECKPOINT → enable VSS → CREATE INDEX → CHECKPOINT → verify → freeze markers.
// It is called by ingest after embeddings are populated. A failed verification
// is a hard error (the caller leaves hnsw_state='building' for serve to detect).
func (s *Store) BuildAndFreezeHNSW(ctx context.Context, model string) error {
	count, err := s.embeddingCount(ctx)
	if err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("no embeddings to index")
	}
	_ = s.SetMeta("hnsw_state", "building")

	if err := s.checkpoint(ctx); err != nil { // (4) clean WAL before custom index
		return err
	}
	if err := s.EnableVSS(ctx); err != nil { // (5) extension + persistence flag
		return err
	}
	// The index follows the vectors. On a content-addressed corpus they live on
	// `bodies`, one per distinct text: indexing the occurrences would index
	// 2 752 688 copies of 897 556 vectors, paying the duplication again in RAM
	// and in build time.
	idx, table := s.hnswTarget()
	if _, err := s.db.ExecContext(ctx,
		`CREATE INDEX IF NOT EXISTS `+idx+` ON `+table+` USING HNSW (embedding) WITH (metric = 'cosine')`); err != nil {
		return fmt.Errorf("create hnsw on %s: %w", table, err) // (6)
	}
	if err := s.checkpoint(ctx); err != nil { // (7) serialise the index into the file
		return err
	}
	if !s.indexExists(ctx, idx) { // (8) verify
		return fmt.Errorf("hnsw index %s missing after build", idx)
	}

	// (9) freeze markers — self-describing state for serve to trust without guessing
	for k, v := range map[string]string{
		"hnsw_metric": "cosine", "embedding_dim": "1024",
		"embedding_count": fmt.Sprintf("%d", count), "embedding_model": model,
	} {
		_ = s.SetMeta(k, v)
	}
	return s.SetMeta("hnsw_state", "frozen")
}

// RebuildHNSW drops and recreates the index from the embedding column — the
// single shared rebuild path (ingest + serve fallback). Deterministic in k-NN
// behaviour (same embeddings + default params); the serialized bytes may differ
// but are excluded from the ingestion hash.
func (s *Store) RebuildHNSW(ctx context.Context) error {
	if err := s.EnableVSS(ctx); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DROP INDEX IF EXISTS clauses_hnsw; DROP INDEX IF EXISTS bodies_hnsw`); err != nil {
		return err
	}
	if err := s.checkpoint(ctx); err != nil {
		return err
	}
	return s.BuildAndFreezeHNSW(ctx, s.GetMeta(ctx, "embedding_model"))
}

// PrepareForEmbedUpdate makes the clauses table writable for embedding updates.
// DuckDB forbids modifying a table while an HNSW index references it ("Cannot
// bind index 'clauses', unknown index type 'HNSW'" / dependency error), so the
// decoupled embed step must drop the index before UPDATE-ing the embedding
// column and let BuildAndFreezeHNSW recreate it afterwards. Loading VSS first is
// required for DROP INDEX on the custom index type to be recognised. Safe to call
// on a DB that was never embedded (DROP IF EXISTS is a no-op); best-effort on VSS
// so a build without the extension still allows plain UPDATEs.
func (s *Store) PrepareForEmbedUpdate(ctx context.Context) error {
	// VSS may be unavailable (extension not installed); that's fine for a DB with
	// no HNSW index — the DROP becomes a no-op and UPDATEs work normally.
	_ = s.EnableVSS(ctx)
	if _, err := s.db.ExecContext(ctx, `DROP INDEX IF EXISTS clauses_hnsw; DROP INDEX IF EXISTS bodies_hnsw`); err != nil {
		return fmt.Errorf("drop hnsw before embed update: %w", err)
	}
	// Clear the frozen markers so a crash mid-embed doesn't leave serve trusting a
	// now-dropped index. BuildAndFreezeHNSW re-stamps them on success.
	_ = s.SetMeta("hnsw_state", "building")
	return nil
}

// LoadVSS is the serve-side counterpart of LoadFTS: it loads the extension and
// marks vssAvailable=true ONLY when a frozen, present, count-consistent index is
// found (the corruption gate, axis #6 §6). It never creates an index.
func (s *Store) LoadVSS(ctx context.Context) error {
	s.vssAvailable = false
	for _, q := range []string{`INSTALL vss`, `LOAD vss`, `SET hnsw_enable_experimental_persistence = true`} {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("vss unavailable: %w", err)
		}
	}
	if st := s.GetMeta(ctx, "hnsw_state"); st != "frozen" {
		return fmt.Errorf("hnsw not frozen (state=%q)", st)
	}
	if s.GetMeta(ctx, "hnsw_metric") != "cosine" || s.GetMeta(ctx, "embedding_dim") != "1024" {
		return fmt.Errorf("hnsw guard mismatch (metric/dim)")
	}
	// The index follows the vectors, and so must the name this looks for. Left
	// hardcoded, this guard reported "hnsw index absent" on a corpus carrying a
	// perfectly good bodies_hnsw, and the server fell back to an exact scan over
	// every vector — correct answers, O(N), and no error anywhere. That is the
	// exact failure the freeze markers exist to prevent, arriving through the
	// check meant to prevent it.
	idx, _ := s.hnswTarget()
	if !s.indexExists(ctx, idx) {
		return fmt.Errorf("hnsw index %s absent", idx)
	}
	want := s.GetMeta(ctx, "embedding_count")
	have, err := s.embeddingCount(ctx)
	if err != nil {
		return err
	}
	if want != fmt.Sprintf("%d", have) {
		return fmt.Errorf("embedding count drift (meta=%s have=%d)", want, have)
	}
	s.vssAvailable = true
	return nil
}

func (s *Store) checkpoint(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CHECKPOINT`); err != nil {
		return fmt.Errorf("checkpoint: %w", err)
	}
	return nil
}

func (s *Store) indexExists(ctx context.Context, name string) bool {
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM duckdb_indexes() WHERE index_name = ?`, name).Scan(&n); err != nil {
		return false
	}
	return n > 0
}

func (s *Store) embeddingCount(ctx context.Context) (int, error) {
	var n int
	// Count where the vectors actually are. On a content-addressed corpus that
	// is `bodies`, and the number is the honest one: 897 556 vectors, not
	// 2 752 688 references to them. embedding_count is written into schema_meta
	// from here, so counting the wrong table would make the corpus advertise a
	// vector population it does not hold.
	_, table := s.hnswTarget()
	err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM `+table+` WHERE embedding IS NOT NULL`).Scan(&n)
	return n, err
}
