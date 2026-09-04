package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// seedIndexedCorpus writes a one-clause corpus that is complete by every measure
// --require-etsi used to take: a vector, an embedding identity, and a genuinely
// built + frozen HNSW index.
func seedIndexedCorpus(t *testing.T, name, identity string) string {
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

// TestRequireETSIAsksWhatTheServerAsks is the cmd/validate half of the same
// property cmd/server/checkdata_etsi_index_test.go pins: the two gates must not
// disagree about what "the ETSI corpus is complete" means, because
// scripts/data-contract.sh hands them the SAME flag string on purpose.
//
// Clauses, vectors and a matching identity are all properties of the data.
// store.LoadVSS — the question internal/mcp actually asks, per store — also
// compares schema_meta.embedding_count against the vectors present, and refuses
// the index when they drift. An ETSI half whose index was frozen over an earlier,
// smaller corpus therefore satisfied this gate completely and was still answered
// lexically at serve time, with no error anywhere.
func TestRequireETSIAsksWhatTheServerAsks(t *testing.T) {
	ctx := context.Background()
	const identity = "38067f8c6efe"
	main3gpp := seedIndexedCorpus(t, "3gpp.duckdb", identity)
	etsi := seedIndexedCorpus(t, "etsi.duckdb", identity)

	// Both halves genuinely indexed: PASS, or the negative below proves nothing.
	if res := runChecks(ctx, checkCfg{db: main3gpp, requireETSI: etsi}); !res.OK {
		t.Fatalf("a fully indexed ETSI half should PASS, got %+v", res.Checks)
	}

	// The ONLY change: the ETSI index now describes a corpus that has since grown.
	// Vectors, identity and hnsw_state all stay exactly as they were.
	db, err := store.Open(etsi)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetMeta("embedding_count", "1042"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	res := runChecks(ctx, checkCfg{db: main3gpp, requireETSI: etsi})
	if res.OK {
		t.Fatal("validate passed an ETSI half whose index the server would refuse — " +
			"the bake gate would promote an image serving half its corpus by BM25 alone")
	}
	var detail string
	for _, c := range res.Checks {
		if c.Name == "require-etsi" {
			detail = c.Detail
		}
	}
	if !strings.Contains(detail, "REFUSE") {
		t.Errorf("require-etsi must say the server would refuse the index, got %q", detail)
	}
}
