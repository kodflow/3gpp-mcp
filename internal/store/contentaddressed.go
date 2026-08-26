package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// paraSep is the separator a body was split on, and the one it is rebuilt with.
// It must match cmd/migrate-paragraphs and the write side exactly: the split is
// only reversible because both sides agree on it, empty parts included.
const paraSep = "\n\n"

// ContentAddressed reports whether this corpus stores its text content-addressed
// (ADR 0004): paragraphs once, bodies once, occurrences pointing at them.
//
// It is a capability, not an assumption, for the same reason FTS and VSS are:
// the ETSI corpus and any published lexical snapshot still carry the old
// `clauses` table, and the same Store serves both. A reader that assumed the new
// shape would fail on etsi.duckdb — which is served ALONGSIDE 3gpp.duckdb by
// design, never merged into it.
func (s *Store) ContentAddressed() bool { return s.contentAddressed }

// probeContentAddressed marks the new shape available iff clause_occ carries a
// row. Best-effort: a missing table means an old-shape corpus, which is a fact
// about the corpus and not an error (degrade, never block).
func (s *Store) probeContentAddressed(ctx context.Context) {
	var present bool
	if err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM clause_occ LIMIT 1)`).Scan(&present); err != nil {
		s.contentAddressed = false
		return
	}
	s.contentAddressed = present
	if !present {
		return
	}
	// Whether BM25 exists over `paragraphs` is a SEPARATE fact from whether it
	// exists over `clauses`: a corpus can be migrated and not yet re-indexed.
	// Asking costs one query; assuming costs a search that fails on the first
	// real request.
	var probe sql.NullFloat64
	s.paraFTS = s.db.QueryRowContext(ctx,
		`SELECT fts_main_paragraphs.match_bm25(para_id, 'probe') FROM paragraphs LIMIT 1`).Scan(&probe) == nil
}

// bodyTexts rebuilds the text of the given bodies, and only those.
//
// This is the two-step read ADR 0004 settled on after measuring. A `clauses`
// VIEW rebuilding on the fly cost 5.34 s where the old table cost 0.146 s — and
// 97 s in its join form — because nothing pushes the caller's filter down into
// body_seq. Bounded, the same work is 0.035 ms per body. So the caller filters
// first and hands the ids here.
func (s *Store) bodyTexts(ctx context.Context, ids []int64) (map[int64]string, error) {
	out := make(map[int64]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	ph := make([]string, len(ids))
	args := make([]any, 0, len(ids)+1)
	args = append(args, paraSep)
	for i, id := range ids {
		ph[i] = "?"
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT s.body_id, string_agg(p.part, ? ORDER BY s.ord)
		FROM body_seq s JOIN paragraphs p USING (para_id)
		WHERE s.body_id IN (`+strings.Join(ph, ",")+`)
		GROUP BY s.body_id`, args...)
	if err != nil {
		return nil, fmt.Errorf("rebuild bodies: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var text string
		if err := rows.Scan(&id, &text); err != nil {
			return nil, err
		}
		out[id] = text
	}
	return out, rows.Err()
}

// ParagraphTrace is one paragraph of a clause, and the releases carrying it.
//
// This is the granularity the content-addressed corpus exists to expose. Clause
// lineage answers "this clause runs from Rel-16 to Rel-18"; a clause that
// changed one sentence looks entirely new to it. Paragraph lineage answers what
// is actually asked of a spec corpus: which statements arrived when, and which
// stopped being made.
type ParagraphTrace struct {
	Ord        int      `json:"ord"`  // position within the body carrying it
	Text       string   `json:"text"` //
	PresentIn  []string `json:"present_in"`
	Introduced string   `json:"introduced"`
	LastSeen   string   `json:"last_seen"`
	Obsolete   bool     `json:"obsolete"` // gone before the spec's newest release
}

// ParagraphLineage returns, for one clause of one spec, the release lineage of
// every paragraph it is built from.
//
// Nothing is diffed: the paragraphs are already deduplicated, so "which releases
// carry this paragraph" is a GROUP BY over the occurrences.
func (s *Store) ParagraphLineage(ctx context.Context, specID, clausePath string) ([]ParagraphTrace, error) {
	if !s.contentAddressed {
		return nil, fmt.Errorf("this corpus is not content-addressed: paragraph lineage needs clause_occ/body_seq (ADR 0004)")
	}
	ordered, err := s.releasesOrdered(ctx, specID)
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

	rows, err := s.db.QueryContext(ctx, `
		SELECT p.para_id, min(s.ord), any_value(p.part), list(DISTINCT o.release)
		FROM clause_occ o
		JOIN body_seq s ON s.body_id = o.body_id
		JOIN paragraphs p USING (para_id)
		WHERE o.spec_id = ? AND o.clause_path = ?
		GROUP BY p.para_id
		ORDER BY min(s.ord)`, specID, clausePath)
	if err != nil {
		return nil, fmt.Errorf("paragraph lineage: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []ParagraphTrace
	for rows.Next() {
		var paraID int64
		var ord int
		var part string
		var rels []any
		if err := rows.Scan(&paraID, &ord, &part, &rels); err != nil {
			return nil, err
		}
		tr := ParagraphTrace{Ord: ord, Text: part}
		for _, r := range rels {
			if rs, ok := r.(string); ok {
				tr.PresentIn = append(tr.PresentIn, rs)
			}
		}
		if len(tr.PresentIn) == 0 {
			continue
		}
		sort.Slice(tr.PresentIn, func(i, j int) bool { return rank[tr.PresentIn[i]] < rank[tr.PresentIn[j]] })
		tr.Introduced = tr.PresentIn[0]
		tr.LastSeen = tr.PresentIn[len(tr.PresentIn)-1]
		tr.Obsolete = newest != "" && tr.LastSeen != newest
		out = append(out, tr)
	}
	return out, rows.Err()
}

// ClauseDelta reports the paragraphs a clause gained and lost between two
// releases, and how many it kept.
//
// It is a set operation on para_id, not a text diff: the corpus already knows
// which paragraphs are the same because storing them once is what makes them
// the same. Nothing is reconstructed and nothing is compared character by
// character.
func (s *Store) ClauseDelta(ctx context.Context, specID, clausePath, from, to string) (added, removed []string, kept int, err error) {
	if !s.contentAddressed {
		return nil, nil, 0, fmt.Errorf("this corpus is not content-addressed: clause deltas need clause_occ/body_seq (ADR 0004)")
	}
	rows, qErr := s.db.QueryContext(ctx, `
		WITH side AS (
			SELECT o.release, s.ord, p.para_id, p.part
			FROM clause_occ o
			JOIN body_seq s ON s.body_id = o.body_id
			JOIN paragraphs p USING (para_id)
			WHERE o.spec_id = ? AND o.clause_path = ? AND o.release IN (?, ?)
		),
		a AS (SELECT DISTINCT para_id, part, ord FROM side WHERE release = ?),
		b AS (SELECT DISTINCT para_id, part, ord FROM side WHERE release = ?)
		SELECT CASE WHEN a.para_id IS NULL THEN 'added'
		            WHEN b.para_id IS NULL THEN 'removed'
		            ELSE 'kept' END,
		       COALESCE(b.part, a.part),
		       COALESCE(b.ord, a.ord)
		FROM a FULL OUTER JOIN b USING (para_id)
		ORDER BY 3`, specID, clausePath, from, to, from, to)
	if qErr != nil {
		return nil, nil, 0, fmt.Errorf("clause delta: %w", qErr)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var verdict, part string
		var ord int
		if err := rows.Scan(&verdict, &part, &ord); err != nil {
			return nil, nil, 0, err
		}
		switch verdict {
		case "added":
			added = append(added, part)
		case "removed":
			removed = append(removed, part)
		default:
			kept++
		}
	}
	return added, removed, kept, rows.Err()
}

// availabilityCA is ClauseAvailability over the content-addressed tables. It
// touches no text at all — release presence lives entirely in clause_occ — so it
// is strictly cheaper than the version that had to scan `clauses`.
func (s *Store) availabilityCA(ctx context.Context, specID, prefix string) ([]ClauseRel, error) {
	q := `SELECT o.clause_path, max(b.heading), list(DISTINCT o.release)
	      FROM clause_occ o JOIN bodies b USING (body_id)
	      WHERE o.spec_id = ?`
	args := []any{specID}
	if prefix != "" {
		q += ` AND (o.clause_path = ? OR o.clause_path LIKE ?)`
		args = append(args, prefix, prefix+".%")
	}
	q += ` GROUP BY o.clause_path ORDER BY len(o.clause_path), o.clause_path`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ClauseRel
	for rows.Next() {
		var cr ClauseRel
		var rels []any
		if err := rows.Scan(&cr.Path, &cr.Heading, &rels); err != nil {
			return nil, err
		}
		for _, r := range rels {
			if rs, ok := r.(string); ok {
				cr.Releases = append(cr.Releases, rs)
			}
		}
		out = append(out, cr)
	}
	return out, rows.Err()
}

// getClausesCA is GetClauses over the content-addressed tables, in the two steps
// ADR 0004 measured: select the occurrences, then rebuild only the bodies they
// point at.
//
// Distinct bodies are rebuilt ONCE even when many occurrences share them, which
// is the common case — 2 752 688 occurrences resolve to 897 556 bodies. Asking
// for a whole spec-version therefore rebuilds far fewer bodies than it returns
// clauses.
func (s *Store) getClausesCA(ctx context.Context, specID, version, clausePrefix string) ([]model.Clause, error) {
	q := `SELECT o.chunk_id, o.spec_id, o.release, o.version, o.clause_path,
	             b.heading, o.is_normative, o.body_id
	      FROM clause_occ o JOIN bodies b USING (body_id)
	      WHERE o.spec_id = ?`
	args := []any{specID}
	if version != "" {
		q += ` AND o.version = ?`
		args = append(args, version)
	}
	if clausePrefix != "" {
		q += ` AND (o.clause_path = ? OR o.clause_path LIKE ?)`
		args = append(args, clausePrefix, clausePrefix+".%")
	}
	q += ` ORDER BY len(o.clause_path), o.clause_path`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []model.Clause
	var owner []int64 // owner[i] is the body of out[i] — kept parallel, never re-queried
	var want []int64
	seen := map[int64]bool{}
	for rows.Next() {
		var c model.Clause
		var bodyID int64
		if err := rows.Scan(&c.ChunkID, &c.SpecID, &c.Release, &c.Version,
			&c.ClausePath, &c.Heading, &c.IsNormative, &bodyID); err != nil {
			return nil, err
		}
		out = append(out, c)
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
	for i := range out {
		out[i].Text = texts[owner[i]]
	}
	return out, nil
}

// searchClausesCA is lexical search over the content-addressed corpus.
//
// BM25 lives on `paragraphs`, not on the occurrences, and that is the point.
// Ranking the occurrence table means ranking VERSIONS: measured on this corpus,
// the entire twelve-hit window for "CHECK_IMEI" was ONE clause repeated across
// twelve releases, while the spec that actually answers it never entered the
// window at all. Scoring deduplicated text and collapsing to one hit per clause
// puts that spec at rank 3.
//
// Three stages, each doing one thing:
//
//	paragraphs  BM25 over text that is stored once
//	bodies      a body scores as the sum of its paragraphs
//	clause_occ  one row per clause, carrying its best-scoring release
func (s *Store) searchClausesCA(ctx context.Context, q SearchQuery) ([]model.SearchHit, error) {
	if q.TopK <= 0 {
		q.TopK = 10
	}
	filterSQL, filterArgs := filterClause(q.Filter)

	// The scoring half is BM25 when the extension is loaded, and a term-count
	// fallback otherwise — same degradation contract as the old path: a corpus
	// without FTS still answers, just less well.
	var scoreSQL string
	args := []any{}
	if s.paraFTS {
		scoreSQL = `SELECT para_id, fts_main_paragraphs.match_bm25(para_id, ?) AS s FROM paragraphs`
		args = append(args, q.Text)
	} else {
		toks := likeTokens(q.Text)
		if len(toks) == 0 {
			return nil, nil
		}
		var sc, wh strings.Builder
		for i := range toks {
			if i > 0 {
				sc.WriteString(" + ")
				wh.WriteString(" OR ")
			}
			sc.WriteString(`(CASE WHEN lower(part) LIKE ? THEN 1.0 ELSE 0 END)`)
			wh.WriteString(`lower(part) LIKE ?`)
		}
		scoreSQL = `SELECT para_id, (` + sc.String() + `) AS s FROM paragraphs WHERE (` + wh.String() + `)`
		for _, tk := range toks {
			args = append(args, "%"+tk+"%")
		}
		for _, tk := range toks {
			args = append(args, "%"+tk+"%")
		}
	}

	full := `
		WITH hits AS (` + scoreSQL + `),
		-- max, NOT sum. Summing paragraph scores rewards long bodies: BM25
		-- normalises length PER PARAGRAPH, so adding them back reintroduces
		-- exactly the bias BM25 exists to remove. Measured on this corpus, sum
		-- put the tables of contents of the largest specs at the top and drove
		-- nDCG@10 to 0.000; max takes it to 0.072, against 0.014 for the BM25
		-- over the clauses table it replaces.
		scored AS (
			SELECT bs.body_id, max(h.s) AS sc
			FROM hits h JOIN body_seq bs USING (para_id)
			WHERE h.s IS NOT NULL AND h.s > 0
			GROUP BY bs.body_id
		),
		pick AS (
			SELECT sc.sc, o.chunk_id, o.spec_id, o.release, o.version, o.clause_path,
			       o.is_normative, o.body_id, b.heading,
			       row_number() OVER (PARTITION BY o.spec_id, o.clause_path
			                          ORDER BY sc.sc DESC, o.version DESC) AS rn
			FROM scored sc
			JOIN clause_occ o USING (body_id)
			JOIN bodies b USING (body_id)
			WHERE 1=1` + filterSQL + `
		)
		SELECT chunk_id, spec_id, release, version, clause_path, heading, is_normative, body_id, sc
		FROM pick WHERE rn = 1 ORDER BY sc DESC, spec_id, clause_path LIMIT ?`
	args = append(args, filterArgs...)
	args = append(args, q.TopK)

	rows, err := s.db.QueryContext(ctx, full, args...)
	if err != nil {
		return nil, fmt.Errorf("search (content-addressed): %w", err)
	}
	defer func() { _ = rows.Close() }()

	var hits []model.SearchHit
	var owner []int64
	var want []int64
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
