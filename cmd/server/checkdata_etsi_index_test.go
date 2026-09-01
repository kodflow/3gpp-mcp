package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// seedServedCorpus writes a one-clause corpus that is complete by every measure
// the ETSI gate used to take: a vector, an embedding identity, and a genuinely
// built + frozen HNSW index.
func seedServedCorpus(t *testing.T, name, identity string) string {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), name)
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.InsertClauses([]model.Clause{
		{ChunkID: 1, SpecID: "103.221-1", Release: "ETSI", Version: "1.20.1",
			ClausePath: "6.2.1", Heading: "X1 interface", Text: "the ADMF activates a task"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetEmbedding(ctx, 1, make([]float32, 1024)); err != nil {
		t.Fatal(err)
	}
	if err := db.BuildAndFreezeHNSW(ctx, identity); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	return path
}

// TestCheckDataRefusesAnETSIIndexTheServerCannotUse pins the third condition on
// --require-etsi.
//
// The gate asked for clauses, vectors and a matching embedding identity. All
// three are properties of the DATA; none of them is the question the server asks.
// internal/mcp calls store.LoadVSS per store, and LoadVSS refuses an index whose
// schema_meta.embedding_count disagrees with the vectors actually present — so an
// ETSI half whose index was frozen over an earlier, smaller corpus satisfies every
// check here and is still answered lexically, with no error anywhere.
//
// That is not hypothetical: the 2026-09-01 corpus carried 510 384 ETSI vectors at
// the same identity as the 3GPP half, with embedding_count still reading 1042 from
// the fourteen-deliverable era, because `index-etsi` had been recorded VALID in
// 1.3s over an hnsw_state that already said "frozen".
func TestCheckDataRefusesAnETSIIndexTheServerCannotUse(t *testing.T) {
	const identity = "38067f8c6efe"
	main3gpp := seedServedCorpus(t, "3gpp.duckdb", identity)
	etsi := seedServedCorpus(t, "etsi.duckdb", identity)

	args := []string{"--db", main3gpp, "--require-fts=false", "--require-hnsw=false", "--require-etsi", etsi}

	// Both halves genuinely indexed: the gate must pass, or the test below would
	// prove nothing about the new condition.
	if err := checkData(args); err != nil {
		t.Fatalf("check-data refused a fully indexed pair: %v", err)
	}

	// Now the ONLY change: the ETSI half's index describes a corpus that has since
	// grown. Vectors, identity and hnsw_state are all still exactly as before.
	db, err := store.Open(etsi)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetMeta("embedding_count", "1042"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	err = checkData(args)
	if err == nil {
		t.Fatal("check-data passed an ETSI half whose index the server would refuse — " +
			"the image would ship half its corpus answerable only by BM25")
	}
	if !strings.Contains(err.Error(), "cannot use") {
		t.Errorf("the failure must name the unusable vectors, got %q", err)
	}
}
