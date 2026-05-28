package store

import (
	"context"
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
