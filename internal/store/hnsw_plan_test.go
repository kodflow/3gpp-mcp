package store

import (
	"context"
	"strings"
	"testing"
)

// THE DENSE ARM MUST REACH THE INDEX, AND ONLY THE PLAN SAYS SO.
//
// An HNSW index scan and a full cosine scan return the SAME rows: one in
// milliseconds, the other after reading every vector in the corpus — 897 556 of
// them on the 3GPP half, 3.6 GB. Nothing in a result, a count or a gate can tell
// the two apart, so a query written out of the shape DuckDB's vss optimizer
// rewrites (a JOIN before the ORDER BY, `1 - dist DESC`, a filter it cannot
// lift, a different distance function) degrades the served path silently and
// permanently. That is the failure this file exists to make loud.
//
// It EXPLAINs the SQL serve actually runs — vectorsCASQL for a converted corpus
// and SearchVectors' own form for the old shape — with the arguments serve binds,
// and requires HNSW_INDEX_SCAN in the plan.
func TestTheDenseArmReachesTheHNSWIndex(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if err := st.EnableVSS(ctx); err != nil {
		t.Skipf("no vss extension here: %v", err)
	}
	// Enough vectors that the optimizer has something to index, in the shape a
	// converted corpus has: the vectors on `bodies`, the occurrences beside them.
	for _, q := range []string{
		`INSERT INTO paragraphs SELECT i, 'p' || i FROM range(1, 400) t(i)`,
		`INSERT INTO bodies (body_id, heading) SELECT i, 'h' || i FROM range(1, 400) t(i)`,
		`INSERT INTO body_seq SELECT i, 1, i FROM range(1, 400) t(i)`,
		`INSERT INTO clause_occ SELECT i, '23.501', 'Rel-19', '19.0.0', '5.' || i, true, i FROM range(1, 400) t(i)`,
		`UPDATE bodies SET embedding = list_transform(range(1024), x -> ((body_id * 7 + x) % 97)::FLOAT)::FLOAT[1024]`,
		`CREATE INDEX bodies_hnsw ON bodies USING HNSW (embedding) WITH (metric = 'cosine')`,
	} {
		if _, err := st.db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	vec := make([]float32, 1024)
	for i := range vec {
		vec[i] = float32((3*7 + i) % 97)
	}
	filterSQL, filterArgs := filterClause(SpecFilter{Release: "Rel-19"})

	for _, c := range []struct {
		name string
		sql  string
		args []any
	}{
		{"content-addressed (bodies)", vectorsCASQL(filterSQL),
			append(append([]any{vecLiteral(vec), 80}, filterArgs...), 10)},
		{"old shape (clauses)", `SELECT chunk_id, spec_id, release, version, clause_path, heading, text, is_normative,
		       array_cosine_distance(embedding, CAST(? AS FLOAT[1024])) AS dist
		      FROM clauses
		      ORDER BY dist ASC
		      LIMIT ?`, []any{vecLiteral(vec), 10}},
	} {
		if c.name == "old shape (clauses)" {
			// The old shape indexes `clauses`; give it its own index to look for.
			if _, err := st.db.ExecContext(ctx, `INSERT INTO clauses (chunk_id, spec_id, release, version, clause_path, heading, text, embedding)
				SELECT body_id, '23.501', 'Rel-19', '19.0.0', '5.' || body_id, heading, 'x', embedding FROM bodies`); err != nil {
				t.Fatalf("seed clauses: %v", err)
			}
			if _, err := st.db.ExecContext(ctx, `CREATE INDEX clauses_hnsw ON clauses USING HNSW (embedding) WITH (metric = 'cosine')`); err != nil {
				t.Fatalf("index clauses: %v", err)
			}
		}
		rows, err := st.db.QueryContext(ctx, `EXPLAIN `+c.sql, c.args...)
		if err != nil {
			t.Fatalf("%s: EXPLAIN: %v", c.name, err)
		}
		var plan strings.Builder
		for rows.Next() {
			var k, p string
			if err := rows.Scan(&k, &p); err != nil {
				t.Fatal(err)
			}
			plan.WriteString(p)
		}
		_ = rows.Close()
		if !strings.Contains(plan.String(), "HNSW_INDEX_SCAN") {
			t.Errorf("%s: the plan has no HNSW_INDEX_SCAN — this query scans every vector in the corpus "+
				"and answers correctly while doing it:\n%s", c.name, plan.String())
		}
	}
}
