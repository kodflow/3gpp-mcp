package store

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// sparseCAFixture is a converted corpus with sparse postings: four occurrences of
// three bodies, one body shared by two releases of the same clause (so equal
// scores within one clause path exist), and the compatibility view `clauses`
// exactly as migrate-paragraphs installs it.
func sparseCAFixture(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	for _, q := range []string{
		`INSERT INTO specs (spec_id, series, doc_type) VALUES ('23.501','23','TS'), ('23.700','23','TR')`,
		`INSERT INTO paragraphs VALUES (1,'AMF registration'), (2,'shall accept'), (3,'SMF session'), (4,'study item')`,
		`INSERT INTO bodies (body_id, heading) VALUES (10,'Registration'), (11,'Session'), (12,'Study')`,
		`INSERT INTO body_seq VALUES (10,1,1), (10,2,2), (11,1,3), (12,1,4)`,
		`INSERT INTO clause_occ VALUES
		   (1,'23.501','Rel-18','18.0.0','5.2',true,10),
		   (2,'23.501','Rel-19','19.0.0','5.2',true,10),
		   (3,'23.501','Rel-19','19.0.0','5.3',true,11),
		   (4,'23.700','Rel-19','19.0.0','6.1',false,12)`,
		// term 100 "registration", 200 "session", 300 "study". Chunks 1 and 2 are
		// one body in two releases: identical postings, identical scores.
		`INSERT INTO clause_sparse VALUES
		   (1,100,0.9),(1,200,0.1),
		   (2,100,0.9),(2,200,0.1),
		   (3,200,0.8),(3,100,0.05),
		   (4,300,0.7),(4,100,0.2)`,
		`DROP TABLE clauses`,
		`CREATE VIEW clauses AS
		   SELECT o.chunk_id, o.spec_id, o.release, o.version, o.clause_path, b.heading,
		          (SELECT string_agg(p.part, chr(10)||chr(10) ORDER BY s.ord)
		             FROM body_seq s JOIN paragraphs p USING (para_id)
		            WHERE s.body_id = o.body_id) AS text,
		          o.is_normative, b.embedding, b.embedding_hash
		   FROM clause_occ o JOIN bodies b USING (body_id)`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	s.probeContentAddressed(context.Background())
	if !s.ContentAddressed() {
		t.Fatal("the fixture was not detected as content-addressed")
	}
	if err := s.LoadSparse(context.Background()); err != nil || !s.SparseAvailable() {
		t.Fatalf("sparse arm not available on the fixture: %v", err)
	}
	return s
}

// viewJoinSparse is the query SearchSparse ran on every corpus before
// searchSparseCA: the scores joined to `clauses`, which on a converted corpus is
// the view. It is the reference the new path must reproduce — with the new
// path's tie-break appended, so that exact ties compare too.
func viewJoinSparse(t *testing.T, s *Store, query model.SparseVec, f SpecFilter, topK int) []model.SearchHit {
	t.Helper()
	values, args, _ := sparseQueryValues(query)
	filterSQL, fargs := filterClause(f)
	rows, err := s.db.Query(`SELECT cl.chunk_id, cl.spec_id, cl.release, cl.version, cl.clause_path,
	               cl.heading, cl.text, cl.is_normative, sub.score
	        FROM (
	          SELECT cs.chunk_id AS chunk_id, SUM(q.qw * cs.weight) AS score
	          FROM clause_sparse cs
	          JOIN (VALUES `+values+`) AS q(term_id, qw) ON cs.term_id = q.term_id
	          GROUP BY cs.chunk_id
	        ) sub
	        JOIN clauses cl ON cl.chunk_id = sub.chunk_id
	        WHERE 1=1`+filterSQL+`
	        ORDER BY sub.score DESC, cl.spec_id, cl.clause_path, cl.release, cl.version, cl.chunk_id
	        LIMIT ?`, append(append(args, fargs...), topK)...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	hits, err := scanHits(rows)
	if err != nil {
		t.Fatal(err)
	}
	return hits
}

// THE SPARSE ARM ON A CONVERTED CORPUS RETURNS WHAT THE VIEW-JOIN RETURNED —
// rows, order, text, score and citation — under every filter shape, and it gets
// there WITHOUT computing the view's text. The second half is the point: the
// view's text column is a correlated string_agg that DuckDB evaluated for every
// scored clause before the page's LIMIT (the served hybrid spent its time there).
// Poisoning that column makes any query that still computes it fail, so the test
// cannot pass by reading the view and happening to be fast on four rows.
func TestSparseArmOnAConvertedCorpusNeverRebuildsTheViewText(t *testing.T) {
	ctx := context.Background()
	s := sparseCAFixture(t)
	query := model.SparseVec{100: 1.0, 200: 0.5, 300: 0.25}
	filters := []SpecFilter{
		{},
		{Release: "Rel-19"},
		{SpecID: "23.501"},
		{Series: "23"},
		{DocType: "TS"},
		{DocType: "TR"},
	}
	want := make([][]model.SearchHit, len(filters))
	for i, f := range filters {
		want[i] = viewJoinSparse(t, s, query, f, 10)
		if len(want[i]) == 0 {
			t.Fatalf("filter %+v: the reference returned nothing — the fixture no longer exercises it", f)
		}
	}

	if _, err := s.db.Exec(`CREATE OR REPLACE VIEW clauses AS
		SELECT o.chunk_id, o.spec_id, o.release, o.version, o.clause_path, b.heading,
		       error('the sparse arm rebuilt the text of the clauses view') AS text,
		       o.is_normative, b.embedding, b.embedding_hash
		FROM clause_occ o JOIN bodies b USING (body_id)`); err != nil {
		t.Fatal(err)
	}
	for i, f := range filters {
		got, err := s.SearchSparse(ctx, query, f, 10)
		if err != nil {
			t.Fatalf("filter %+v: %v", f, err)
		}
		if len(got) != len(want[i]) {
			t.Fatalf("filter %+v: %d hits, the view-join returned %d", f, len(got), len(want[i]))
		}
		for j := range got {
			g, w := got[j], want[i][j]
			g.Clause.Embedding, w.Clause.Embedding = nil, nil // neither path reads it
			if !reflect.DeepEqual(g.Clause, w.Clause) || g.Score != w.Score || !reflect.DeepEqual(g.Citation, w.Citation) {
				t.Errorf("filter %+v, rank %d:\n got  %+v score=%v\n want %+v score=%v", f, j+1,
					g.Clause, g.Score, w.Clause, w.Score)
			}
		}
	}

	// The rebuilt text is the whole body, paragraphs joined as the view joins them.
	got, _ := s.SearchSparse(ctx, model.SparseVec{100: 1.0}, SpecFilter{Release: "Rel-18"}, 1)
	if len(got) != 1 || got[0].Clause.Text != "AMF registration\n\nshall accept" {
		t.Fatalf("text of the Rel-18 registration clause = %+v", got)
	}
}

// Equal scores within one clause path — one body, two releases — come back in ONE
// order, newest-labelled last by (release, version, chunk_id), every time.
func TestSparseArmOrdersExactTiesTheSameWayEveryTime(t *testing.T) {
	ctx := context.Background()
	s := sparseCAFixture(t)
	for i := 0; i < 20; i++ {
		got, err := s.SearchSparse(ctx, model.SparseVec{100: 1.0}, SpecFilter{SpecID: "23.501"}, 2)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, h := range got {
			ids = append(ids, h.Clause.Release)
		}
		key := strings.Join(ids, ",")
		if key != "Rel-18,Rel-19" {
			t.Fatalf("run %d: the tied pair came back %q, want Rel-18,Rel-19", i, key)
		}
	}
}
