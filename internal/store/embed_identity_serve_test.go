package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/embed"
	"github.com/kodflow/3gpp-mcp/internal/model"
)

// buildVecShardModel writes a one-clause vectorized sub-base stamped with the
// given embedding_model (the canonical EmbedIdentity in production). Mirrors
// buildVecShard but lets the test pick the identity so it can simulate a sub-base
// built by an OLDER model revision than the current client.
func buildVecShardModel(t *testing.T, path, spec, embModel string) {
	t.Helper()
	ctx := context.Background()
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	_ = st.UpsertSpec(model.Spec{SpecID: spec, Series: spec[:2], DocType: "TS"})
	_ = st.UpsertVersion(model.SpecVersion{SpecID: spec, Release: "Rel-18", Version: "18.0.0"})
	if err := st.InsertClauses([]model.Clause{
		{ChunkID: 1, SpecID: spec, Release: "Rel-18", Version: "18.0.0", ClausePath: "1", Heading: "h", Text: "t"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetEmbedding(ctx, 1, oneHot(5)); err != nil {
		t.Fatal(err)
	}
	if err := st.BuildAndFreezeHNSW(ctx, embModel); err != nil {
		t.Fatal(err)
	}
}

// TestServeRefusesMismatchedEmbedIdentity is the PR-6 serve-time guard: a vector
// sub-base whose embedding_model is a DIFFERENT EmbedIdentity than the current
// client's is refused (would yield silently-wrong cosine scores from mixing model
// revisions). The identities here are the REAL canonical ones (full digest over
// model+revision+tokenizer+dim+normalisation+precision), so this locks that a
// weight-revision bump — which flips EmbedIdentity — is caught at serve, not
// scored across revisions.
func TestServeRefusesMismatchedEmbedIdentity(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	current := embed.ResolveModelID("bge-m3") // current code's BGE-M3 identity
	// Same family/dim but an OLDER weight revision → a DIFFERENT EmbedIdentity.
	stale := model.EmbedIdentity(model.EmbedParts{
		ModelID:           "bge-m3",
		ModelRevision:     "0000000", // pretend a prior pinned commit
		TokenizerRevision: embed.BGETokenizerRevision,
		VectorDim:         "1024",
		NormalizationMode: "l2",
		Precision:         "fp32",
	})
	if current == stale {
		t.Fatal("test precondition: stale identity must differ from current")
	}

	fresh := filepath.Join(dir, "fresh.duckdb")
	old := filepath.Join(dir, "old.duckdb")
	buildVecShardModel(t, fresh, "23.501", current)
	buildVecShardModel(t, old, "24.501", stale)

	// A serve process that ONLY has the fresh sub-base (current identity) is
	// coherent and routes queries.
	stFresh, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stFresh.Close() }()
	freshAliases, err := stFresh.AttachShards(ctx, []string{fresh})
	if err != nil {
		t.Fatal(err)
	}
	if ok, why := stFresh.ShardsCoherent(ctx, freshAliases, current); !ok {
		t.Fatalf("current-identity sub-base must be coherent, got %q", why)
	}

	// A serve process whose manifest includes a stale-revision sub-base is REFUSED
	// (would mix model revisions in one HNSW). Fresh DB so aliases start at vs0.
	stOld, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stOld.Close() }()
	oldAliases, err := stOld.AttachShards(ctx, []string{fresh, old})
	if err != nil {
		t.Fatal(err)
	}
	if ok, why := stOld.ShardsCoherent(ctx, oldAliases, current); ok || why == "" {
		t.Fatalf("manifest with a stale-revision sub-base must be refused at serve, got ok=%v why=%q", ok, why)
	}
}
