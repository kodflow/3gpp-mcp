package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// AttachShards attaches per-series vectorized sub-bases (Option B) read-only
// under aliases vs0, vs1, … and loads the VSS extension so each shard's frozen
// HNSW is usable. Returns the aliases for SearchVectorsSharded. Best-effort per
// the store doctrine: a shard that fails to attach aborts the whole call (the
// caller decides whether to degrade to the single-DB / lexical path).
func (s *Store) AttachShards(ctx context.Context, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	if err := s.EnableVSS(ctx); err != nil {
		return nil, err
	}
	aliases := make([]string, 0, len(paths))
	for i, p := range paths {
		alias := fmt.Sprintf("vs%d", i)
		// ATTACH takes no bind params; inline the (local) path, single-quote-escaped.
		esc := strings.ReplaceAll(p, "'", "''")
		if _, err := s.db.ExecContext(ctx, "ATTACH '"+esc+"' AS "+alias+" (READ_ONLY)"); err != nil {
			return nil, fmt.Errorf("attach shard %s: %w", p, err)
		}
		aliases = append(aliases, alias)
	}
	return aliases, nil
}

// ShardsCoherent checks that every attached sub-base was built with wantModel
// (the client embedder's ModelID). A mismatch means cross-model cosine scores
// would be silently wrong — the caller must NOT route queries to these shards.
// Returns (false, detail) on the first mismatch. Aliases are internal (vs0,…),
// not user input, so they're safe to interpolate.
func (s *Store) ShardsCoherent(ctx context.Context, aliases []string, wantModel string) (bool, string) {
	for _, a := range aliases {
		var m string
		err := s.db.QueryRowContext(ctx, "SELECT value FROM "+a+".schema_meta WHERE key = 'embedding_model'").Scan(&m)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			// Can't read the marker → fail closed (don't route to an unverifiable shard).
			return false, fmt.Sprintf("%s: read embedding_model: %v", a, err)
		}
		if m != wantModel {
			return false, fmt.Sprintf("%s embedding_model=%q != client %q", a, m, wantModel)
		}
	}
	return true, ""
}

// SearchVectorsSharded is the Option-B scatter-gather: it runs the k-NN against
// each attached shard with the bare index-eligible shape
// (`ORDER BY array_cosine_distance ASC LIMIT topK`, no WHERE, so each shard's HNSW
// fires), unions the candidates, and re-ranks to a global topK by distance. A
// SpecFilter is applied as a Go post-filter (mirrors SearchVectors). NULL-distance
// rows (no embedding) are dropped.
func (s *Store) SearchVectorsSharded(ctx context.Context, vec []float32, aliases []string, f SpecFilter, topK int) ([]model.SearchHit, error) {
	if topK <= 0 {
		topK = 10
	}
	if len(aliases) == 0 {
		return nil, nil
	}
	// Over-fetch (per shard AND in the merge) when filtering, so the Go post-filter
	// can still fill topK after dropping out-of-filter neighbours.
	fetch := topK
	if !f.IsZero() {
		if fetch = topK * vecOverFetch; fetch > vecMaxFetch {
			fetch = vecMaxFetch
		}
	}
	const cols = `chunk_id, spec_id, release, version, clause_path, heading, text, is_normative`
	parts := make([]string, 0, len(aliases))
	args := make([]any, 0, len(aliases)+1)
	for _, a := range aliases {
		// Parenthesise each arm: a SELECT carrying its own ORDER BY/LIMIT must be
		// wrapped before UNION ALL (DuckDB syntax).
		parts = append(parts, fmt.Sprintf(
			`(SELECT %s, array_cosine_distance(embedding, CAST(? AS FLOAT[1024])) AS dist FROM %s.clauses ORDER BY dist ASC LIMIT %d)`,
			cols, a, fetch))
		args = append(args, vecLiteral(vec))
	}
	q := "SELECT * FROM (" + strings.Join(parts, " UNION ALL ") + ") ORDER BY dist ASC LIMIT ?"
	args = append(args, fetch)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	// DocType isn't enforceable here without a specs join across shards (the
	// lexical list + RRF cover doc-type intent); strip it before the post-filter.
	fNoDoc := f
	fNoDoc.DocType = ""
	return scanVecHits(rows, fNoDoc, nil, topK)
}
