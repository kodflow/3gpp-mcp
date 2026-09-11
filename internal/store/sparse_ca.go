package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// sparseQueryValues renders the query's positive term weights as the rows of an
// inline VALUES list, with the arguments that fill it. CAST so the literals match
// clause_sparse's UINTEGER/FLOAT columns. n == 0 means the query has nothing to
// score.
func sparseQueryValues(query model.SparseVec) (values string, args []any, n int) {
	var vb strings.Builder
	args = make([]any, 0, len(query)*2)
	for term, w := range query {
		if w <= 0 {
			continue
		}
		if n > 0 {
			vb.WriteString(",")
		}
		vb.WriteString("(CAST(? AS UINTEGER), CAST(? AS FLOAT))")
		args = append(args, int64(term), float64(w))
		n++
	}
	return vb.String(), args, n
}

// searchSparseCA is the sparse arm over a content-addressed corpus, and it never
// reads the `clauses` view.
//
// WHY. SearchSparse joined its scores to `clauses`, and on a converted corpus that
// name is the compatibility VIEW, whose text column rebuilds every body with a
// correlated string_agg over body_seq ⨝ paragraphs. DuckDB cannot push the page's
// LIMIT through `ORDER BY score` into that join, so every search rebuilt the text
// of every clause that shares a single query term with the question — the "97 s
// in its join form" bodyTexts' comment measured when ADR 0004 was written, paid on
// every hybrid query, and the buffer-pool pressure that came with it. Measured on
// the served path (see the PR that introduced this file for the numbers).
//
// The shape is the one searchClausesCA and searchVectorsCA already use: score the
// occurrences, join clause_occ and bodies for the citation and the filter, take
// the page, and only then rebuild the text of the bodies ON the page (bodyTexts).
// The rows, their order and their text are those the view-join returned: the view
// is clause_occ ⨝ bodies with the same string_agg that bodyTexts runs.
//
// The ORDER BY keeps (score DESC, spec_id, clause_path) and adds (release,
// version, chunk_id) after them. Two occurrences of one body carry the same
// postings, so equal scores within one clause path are common, and with the old
// key they came back in whatever order the parallel scan produced — the page
// could change between two identical calls. Every order the old key decided is
// kept; only its exact ties now have one answer.
func (s *Store) searchSparseCA(ctx context.Context, values string, vargs []any, f SpecFilter, topK int) ([]model.SearchHit, error) {
	filterSQL, fargs := filterClause(f)
	// sub exposes ONLY chunk_id + score and bodies carries no spec/release column,
	// so the unqualified columns in filterSQL bind to clause_occ.
	q := `SELECT o.chunk_id, o.spec_id, o.release, o.version, o.clause_path,
	             b.heading, o.is_normative, o.body_id, sub.score
	      FROM (
	        SELECT cs.chunk_id AS chunk_id, SUM(q.qw * cs.weight) AS score
	        FROM clause_sparse cs
	        JOIN (VALUES ` + values + `) AS q(term_id, qw) ON cs.term_id = q.term_id
	        GROUP BY cs.chunk_id
	      ) sub
	      JOIN clause_occ o ON o.chunk_id = sub.chunk_id
	      JOIN bodies b ON b.body_id = o.body_id
	      WHERE 1=1` + filterSQL + `
	      ORDER BY sub.score DESC, o.spec_id, o.clause_path, o.release, o.version, o.chunk_id
	      LIMIT ?`
	args := append(append(append([]any{}, vargs...), fargs...), topK)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("sparse search (content-addressed): %w", err)
	}
	defer func() { _ = rows.Close() }()

	var hits []model.SearchHit
	var owner, want []int64
	seen := map[int64]bool{}
	for rows.Next() {
		var h model.SearchHit
		var bodyID int64
		if err := rows.Scan(&h.Clause.ChunkID, &h.Clause.SpecID, &h.Clause.Release,
			&h.Clause.Version, &h.Clause.ClausePath, &h.Clause.Heading,
			&h.Clause.IsNormative, &bodyID, &h.Score); err != nil {
			return nil, err
		}
		hits = append(hits, h)
		owner = append(owner, bodyID)
		if !seen[bodyID] {
			seen[bodyID] = true
			want = append(want, bodyID)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	texts, err := s.bodyTexts(ctx, want)
	if err != nil {
		return nil, err
	}
	for i := range hits {
		hits[i].Clause.Text = texts[owner[i]]
		hits[i].Citation = hits[i].Clause.Cite()
	}
	return hits, nil
}
