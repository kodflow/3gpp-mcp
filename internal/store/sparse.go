package store

// sparse.go — the BGE-M3 sparse (learned-lexical) retrieval arm's persistence.
//
// Storage is an inverted index in plain SQL: clause_sparse(chunk_id, term_id,
// weight), indexed on term_id. DuckDB has no native sparse-vector index (unlike
// HNSW for dense), so scoring a query is a GROUP-BY dot product over the shared
// term ids — exactly FlagEmbedding's compute_lexical_matching_score: score(clause)
// = Σ query_weight[t] · clause_weight[t]. Weights are raw ReLU(linear) floats,
// never normalized (index or query). Absent table/rows → the arm is simply not
// offered (degrade, never block — CLAUDE.md §1).

import (
	"context"
	"fmt"
	"strings"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// SparseAvailable reports whether a sparse arm can serve (clause_sparse is
// populated). Set by LoadSparse at serve time; false on a dense-only / lexical DB.
func (s *Store) SparseAvailable() bool { return s.sparseAvailable }

// LoadSparse marks the sparse arm available iff clause_sparse carries any row. It
// never builds anything (the index is the table + its term_id index, created by
// the schema). Cheap: EXISTS short-circuits. Best-effort — an error leaves the arm
// off (degrade, never block).
func (s *Store) LoadSparse(ctx context.Context) error {
	var present bool
	if err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM clause_sparse LIMIT 1)`).Scan(&present); err != nil {
		return err
	}
	s.sparseAvailable = present
	return nil
}

// SetSparse replaces the sparse posting for one clause (idempotent re-embed: a
// changed clause's stale terms are deleted first). An empty vector clears the
// clause's terms. term ids are stored as the bge-m3 (XLM-R) vocab ids.
func (s *Store) SetSparse(ctx context.Context, chunkID uint64, vec model.SparseVec) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM clause_sparse WHERE chunk_id = ?`, chunkID); err != nil {
		return fmt.Errorf("clear sparse %d: %w", chunkID, err)
	}
	if len(vec) == 0 {
		return nil
	}
	var vb strings.Builder
	args := make([]any, 0, len(vec)*3)
	i := 0
	for term, w := range vec {
		if w <= 0 { // raw ReLU output is non-negative; skip zeros defensively
			continue
		}
		if i > 0 {
			vb.WriteString(",")
		}
		vb.WriteString("(?,?,?)")
		args = append(args, int64(chunkID), int64(term), float64(w))
		i++
	}
	if i == 0 {
		return nil
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO clause_sparse (chunk_id, term_id, weight) VALUES `+vb.String(), args...); err != nil {
		return fmt.Errorf("insert sparse %d: %w", chunkID, err)
	}
	return nil
}

// SearchSparse ranks clauses by the sparse lexical-match score (Σ qw·dw over shared
// term ids) and returns hydrated hits with citations. The query's term weights are
// fed as an inline VALUES list joined against the term_id-indexed inverted index;
// the SpecFilter applies on the clauses table after scoring.
func (s *Store) SearchSparse(ctx context.Context, query model.SparseVec, f SpecFilter, topK int) ([]model.SearchHit, error) {
	if topK <= 0 {
		topK = 10
	}
	if len(query) == 0 {
		return nil, nil
	}
	var vb strings.Builder
	args := make([]any, 0, len(query)*2+4)
	i := 0
	for term, w := range query {
		if w <= 0 {
			continue
		}
		if i > 0 {
			vb.WriteString(",")
		}
		// CAST so the VALUES literals match clause_sparse's UINTEGER/FLOAT columns.
		vb.WriteString("(CAST(? AS UINTEGER), CAST(? AS FLOAT))")
		args = append(args, int64(term), float64(w))
		i++
	}
	if i == 0 {
		return nil, nil
	}
	filterSQL, fargs := filterClause(f)
	// Scored subquery exposes ONLY chunk_id + score, so the unqualified columns in
	// filterSQL (spec_id/release/…) bind unambiguously to the clauses row.
	sql := `SELECT cl.chunk_id, cl.spec_id, cl.release, cl.version, cl.clause_path,
	               cl.heading, cl.text, cl.is_normative, sub.score
	        FROM (
	          SELECT cs.chunk_id AS chunk_id, SUM(q.qw * cs.weight) AS score
	          FROM clause_sparse cs
	          JOIN (VALUES ` + vb.String() + `) AS q(term_id, qw) ON cs.term_id = q.term_id
	          GROUP BY cs.chunk_id
	        ) sub
	        JOIN clauses cl ON cl.chunk_id = sub.chunk_id
	        WHERE 1=1` + filterSQL + `
	        ORDER BY sub.score DESC, cl.spec_id, cl.clause_path
	        LIMIT ?`
	args = append(args, fargs...)
	args = append(args, topK)

	rows, err := s.db.QueryContext(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanHits(rows)
}
