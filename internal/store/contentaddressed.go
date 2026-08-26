package store

import (
	"context"
	"fmt"
	"sort"
	"strings"
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
