package store

import (
	"context"
	"database/sql"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// Reader is the read-only surface of a Store. The served binary (cmd/server →
// internal/{mcp,search} and the subject query tools) consumes the store ONLY
// through this interface, so the migration's core invariant — "the served Go
// binary never writes the DuckDB" (plan rust-writeside-go-readside §11a, finding
// A14) — is enforced by the type-checker, not merely by the lexical archguard
// (internal/archguard): a write method that is not on Reader is simply not
// callable from the request path, no matter how the code is refactored.
//
// *Store satisfies Reader (assertion below). Write methods (Insert*, Upsert*,
// Set*, Build*, Replace*, plus the raw DB()/Begin/Exec handle) stay on the
// concrete *Store for the offline producer/utility tools (cmd/{split,
// export-delta,bench}, scripts/smoke-seed); those are NOT the served binary and
// legitimately open store.Open(rw). The Rust write-side owns the production
// ingest/embed/index pipeline (CLAUDE.md §2).
//
// QueryContext/QueryRowContext are deliberately the ONLY raw-SQL accessors on
// Reader: read-intent SQL for subject query tools (e.g. li's ASN.1 registry
// lookups) without exposing DB() (which yields Begin/Exec and thus a write path).
type Reader interface {
	// Availability probes (capabilities of the loaded extensions/indexes).
	FTSAvailable() bool
	VSSAvailable() bool
	SparseAvailable() bool

	// Catalogue + clause/version lookups.
	GetClauses(ctx context.Context, specID, version, clausePrefix string) ([]model.Clause, error)
	GetChangelog(ctx context.Context, specID, fromRel, toRel string) ([]model.Change, error)
	GetEvolutions(ctx context.Context, entity string) ([]model.Evolution, error)
	GetMeta(ctx context.Context, key string) string
	ListReleases(ctx context.Context, specID string) ([]model.SpecVersion, error)
	ListSpecs(ctx context.Context, f SpecFilter) ([]model.Spec, error)
	LatestVersion(ctx context.Context, specID string) (release, version string, ok bool, err error)
	VersionForRelease(ctx context.Context, specID, release string) (string, bool, error)
	ResolveTerm(ctx context.Context, term string) ([]model.Acronym, error)
	ClauseAvailability(ctx context.Context, specID, prefix string) ([]ClauseRel, []string, error)
	// LineageAxis names the column a spec actually evolves along — "release" for
	// a 3GPP spec republished per release, "version" for an ETSI deliverable,
	// whose release column is the constant "ETSI". Callers must SAY which one a
	// payload is listing: a field named for releases that carries versions is
	// the silent kind of wrong.
	LineageAxis(ctx context.Context, specID string) (string, []string, error)
	ClauseLineage(ctx context.Context, specID, prefix string) (map[string]model.Lineage, error)

	// Paragraph-level provenance (ADR 0004). ContentAddressed reports whether a
	// given corpus can answer these at all: etsi.duckdb is served ALONGSIDE and
	// has not been converted, so a caller must ask rather than assume.
	ContentAddressed() bool
	ParagraphLineage(ctx context.Context, specID, clausePath string) ([]ParagraphTrace, error)
	ClauseDelta(ctx context.Context, specID, clausePath, from, to string) (added, removed []string, kept int, err error)

	// Retrieval arms.
	SearchClauses(ctx context.Context, q SearchQuery) ([]model.SearchHit, error)
	SearchSparse(ctx context.Context, query model.SparseVec, f SpecFilter, topK int) ([]model.SearchHit, error)
	SearchVectors(ctx context.Context, vec []float32, f SpecFilter, topK int) ([]model.SearchHit, error)
	SearchVectorsAmong(ctx context.Context, vec []float32, candidateIDs []uint64, topK int) ([]model.SearchHit, error)
	SearchVectorsSharded(ctx context.Context, vec []float32, aliases []string, f SpecFilter, topK int) ([]model.SearchHit, error)
	SearchAPI(ctx context.Context, q APISearchQuery) ([]APIHit, error)
	SpecsWithAPI(ctx context.Context) map[string]bool

	// Read-only raw-SQL accessors for subject query tools (no Exec/Begin/DB()).
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// QueryContext runs a read-intent query against the underlying DuckDB handle.
// It exists so subject query tools can read via Reader without DB() (which would
// expose Begin/Exec and break the read-only guarantee).
func (s *Store) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return s.db.QueryContext(ctx, query, args...)
}

// QueryRowContext runs a read-intent single-row query (see QueryContext).
func (s *Store) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return s.db.QueryRowContext(ctx, query, args...)
}

// compile-time proof that the concrete store implements the read-only surface.
var _ Reader = (*Store)(nil)
