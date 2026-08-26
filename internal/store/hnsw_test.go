package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// seedEmbedded builds a small embedded corpus: 3 clauses with distinct one-hot
// vectors so k-NN self-match is unambiguous.
func seedEmbedded(t *testing.T, st *Store) {
	t.Helper()
	ctx := context.Background()
	_ = st.UpsertSpec(model.Spec{SpecID: "33.128", Series: "33", DocType: "TS"})
	_ = st.UpsertVersion(model.SpecVersion{SpecID: "33.128", Release: "Rel-19", Version: "19.6.0"})
	clauses := []model.Clause{
		{ChunkID: 1, SpecID: "33.128", Release: "Rel-19", Version: "19.6.0", ClausePath: "6.1", Heading: "A", Text: "alpha"},
		{ChunkID: 2, SpecID: "33.128", Release: "Rel-19", Version: "19.6.0", ClausePath: "6.2", Heading: "B", Text: "beta"},
		{ChunkID: 3, SpecID: "33.128", Release: "Rel-19", Version: "19.6.0", ClausePath: "6.3", Heading: "C", Text: "gamma"},
	}
	if err := st.InsertClauses(clauses); err != nil {
		t.Fatal(err)
	}
	for i := uint64(1); i <= 3; i++ {
		v := make([]float32, 1024)
		v[i*10] = 1 // distinct direction per clause
		if err := st.SetEmbedding(ctx, i, v); err != nil {
			t.Fatal(err)
		}
	}
}

func oneHot(pos int) []float32 {
	v := make([]float32, 1024)
	v[pos] = 1
	return v
}

func TestHNSWBuildFreezeAndReadOnly(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "hnsw.duckdb")

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	seedEmbedded(t, st)

	if err := st.BuildAndFreezeHNSW(ctx, "bge-m3"); err != nil {
		t.Fatalf("build-then-freeze: %v", err)
	}
	if st.GetMeta(ctx, "hnsw_state") != "frozen" {
		t.Errorf("hnsw_state = %q, want frozen", st.GetMeta(ctx, "hnsw_state"))
	}
	if st.GetMeta(ctx, "embedding_count") != "3" {
		t.Errorf("embedding_count = %q, want 3", st.GetMeta(ctx, "embedding_count"))
	}
	if !st.indexExists(ctx, "clauses_hnsw") {
		t.Error("index missing after freeze")
	}
	// Self-match: clause 2's own vector ranks clause 2 top-1.
	hits, err := st.SearchVectors(ctx, oneHot(20), SpecFilter{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Clause.ClausePath != "6.2" {
		t.Errorf("self-match top-1 = %+v, want clause 6.2", hits)
	}
	_ = st.Close()

	// Reopen read-only: the serve posture. VSS must load the frozen index.
	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ro.Close() }()
	if err := ro.LoadVSS(ctx); err != nil {
		t.Fatalf("read-only LoadVSS: %v", err)
	}
	if !ro.VSSAvailable() {
		t.Error("VSSAvailable false after read-only load of a frozen index")
	}
	hits, err = ro.SearchVectors(ctx, oneHot(30), SpecFilter{}, 1)
	if err != nil || len(hits) != 1 || hits[0].Clause.ClausePath != "6.3" {
		t.Errorf("read-only k-NN = %+v err=%v, want clause 6.3", hits, err)
	}
}

func TestHNSWRebuildDeterminism(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "rebuild.duckdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	seedEmbedded(t, st)
	if err := st.BuildAndFreezeHNSW(ctx, "bge-m3"); err != nil {
		t.Fatal(err)
	}

	before, _ := st.SearchVectors(ctx, oneHot(20), SpecFilter{}, 3)
	if err := st.RebuildHNSW(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	after, _ := st.SearchVectors(ctx, oneHot(20), SpecFilter{}, 3)

	if len(before) != len(after) {
		t.Fatalf("rebuild changed result count: %d -> %d", len(before), len(after))
	}
	for i := range before {
		if before[i].Clause.ClausePath != after[i].Clause.ClausePath {
			t.Errorf("rebuild reordered top-k at %d: %s != %s", i,
				before[i].Clause.ClausePath, after[i].Clause.ClausePath)
		}
	}
}

func TestHNSWCorruptionDegrades(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "corrupt.duckdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	seedEmbedded(t, st)
	if err := st.BuildAndFreezeHNSW(ctx, "bge-m3"); err != nil {
		t.Fatal(err)
	}

	// Simulate count drift (column and index out of phase).
	_ = st.SetMeta("embedding_count", "999")
	if err := st.LoadVSS(ctx); err == nil {
		t.Error("LoadVSS should fail on embedding-count drift")
	}
	if st.VSSAvailable() {
		t.Error("VSSAvailable must be false when corruption is suspected")
	}
	// Exact scan still works (correctness preserved, just no index).
	hits, err := st.SearchVectors(ctx, oneHot(10), SpecFilter{}, 1)
	if err != nil || len(hits) != 1 || hits[0].Clause.ClausePath != "6.1" {
		t.Errorf("exact-scan fallback = %+v err=%v, want clause 6.1", hits, err)
	}
}

// The serve-time guard has to look for the index where the vectors are.
//
// LoadVSS checked `clauses_hnsw` by name while BuildAndFreezeHNSW had already
// been taught to build `bodies_hnsw` on a content-addressed corpus. The result
// was a server that reported "hnsw index absent" over a corpus carrying a
// perfectly good index, fell back to an exact scan across every vector, and said
// nothing else: correct answers, O(N), no error. The same shape of miss then hid
// behind it — schema_meta.embedding_count still held the pre-conversion count, so
// even with the right name the guard failed on "embedding count drift".
//
// Both are checked here on a corpus in the shape the pipeline actually ships.
func TestTheServeGuardFindsTheIndexOnAContentAddressedCorpus(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "ca-hnsw.duckdb")

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// A converted corpus in miniature: the vectors live on `bodies`, the
	// occurrences point at them, and `clauses` is the compatibility view.
	seed := []string{
		`INSERT INTO spec_versions (spec_id, release, version) VALUES ('33.128','Rel-19','19.6.0')`,
		`INSERT INTO paragraphs VALUES (1,'alpha'),(2,'beta'),(3,'gamma')`,
		`INSERT INTO bodies (body_id, heading) VALUES (10,'A'),(11,'B'),(12,'C')`,
		`INSERT INTO body_seq VALUES (10,1,1),(11,1,2),(12,1,3)`,
		`INSERT INTO clause_occ VALUES (1,'33.128','Rel-19','19.6.0','6.1',true,10),
		                              (2,'33.128','Rel-19','19.6.0','6.2',true,11),
		                              (3,'33.128','Rel-19','19.6.0','6.3',true,12)`,
	}
	for _, q := range seed {
		if _, err := st.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for i, body := range []int{10, 11, 12} {
		v := oneHot((i + 1) * 10)
		if _, err := st.db.Exec(
			`UPDATE bodies SET embedding = CAST(? AS FLOAT[1024]) WHERE body_id = ?`,
			vecLiteral(v), body); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.db.Exec(`DROP TABLE clauses;
		CREATE VIEW clauses AS
		  SELECT o.chunk_id, o.spec_id, o.release, o.version, o.clause_path, b.heading,
		         (SELECT string_agg(p.part, chr(10)||chr(10) ORDER BY s.ord)
		            FROM body_seq s JOIN paragraphs p USING (para_id)
		           WHERE s.body_id = o.body_id) AS text,
		         o.is_normative, b.embedding, b.embedding_hash
		  FROM clause_occ o JOIN bodies b USING (body_id)`); err != nil {
		t.Fatal(err)
	}
	st.probeContentAddressed(ctx)
	if !st.ContentAddressed() {
		t.Fatal("the fixture was not detected as content-addressed")
	}

	if err := st.BuildAndFreezeHNSW(ctx, "bge-m3"); err != nil {
		t.Fatalf("build-then-freeze on a converted corpus: %v", err)
	}
	if !st.indexExists(ctx, "bodies_hnsw") {
		t.Fatal("the index was not built on `bodies` — it followed the name, not the vectors")
	}
	if got := st.GetMeta(ctx, "embedding_count"); got != "3" {
		t.Errorf("embedding_count = %q, want 3 (the bodies, not the occurrences)", got)
	}
	_ = st.Close()

	// The serve posture. This is the assertion the missing bug report was:
	// a frozen, present, count-consistent index must be USED.
	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ro.Close() }()
	if err := ro.LoadVSS(ctx); err != nil {
		t.Fatalf("read-only LoadVSS on a converted corpus: %v", err)
	}
	if !ro.VSSAvailable() {
		t.Fatal("VSSAvailable false — the server would silently exact-scan every vector")
	}
	hits, err := ro.SearchVectors(ctx, oneHot(30), SpecFilter{}, 1)
	if err != nil || len(hits) != 1 || hits[0].Clause.ClausePath != "6.3" {
		t.Errorf("read-only k-NN = %+v err=%v, want clause 6.3", hits, err)
	}
}
