package ingest

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/subject"
	"github.com/kodflow/3gpp-mcp/internal/subjectmeta"

	"github.com/kodflow/3gpp-mcp/internal/store"
)

// TestSubjectBumpInvalidatesResume locks the PR-3 keystone (group A): a changed
// subject footprint (here a li-v1 → li-v2 bump, which also models an ASN.1
// scanner change since the scanner tag is folded into the LI footprint) must make
// the resume gate re-queue the owning spec — even though the spec was previously
// stamped 'done'. Before the SpecIngestIdentity split the gate keyed only on
// parser/chunking/schema/model, so a subject bump left every 'done' row valid and
// --resume silently skipped the very change that triggered the rebuild.
func TestSubjectBumpInvalidatesResume(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "shard.duckdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if err := st.Reset(ctx); err != nil {
		t.Fatal(err)
	}

	// Stamp 33.128 'done' under the CURRENT (li-v1) ingest identity.
	gateV1 := specIngestIdentity()
	if err := st.MarkIngestStarted(ctx, "33.128", "19.6.0", gateV1); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkIngestDone(ctx, "33.128", "19.6.0"); err != nil {
		t.Fatal(err)
	}

	reg := subject.New(&fakeSubject{spec: "33.128"})
	jobs := []ingestJob{{path: "x", specID: "33.128", release: "Rel-19", version: "19.6.0"}}

	// Sanity: under the unchanged identity the 'done' spec is skipped.
	out, skipped, err := filterResumeJobs(ctx, st, reg, jobs, true, gateV1, func(string, ...any) {})
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 1 || len(out) != 0 {
		t.Fatalf("unchanged identity should skip the done spec: out=%d skipped=%d", len(out), skipped)
	}
	// Re-stamp 'done' (the sanity call above purged nothing because it skipped).

	// Bump the LI subject footprint (extraction logic / ASN.1 scanner change).
	restore := subjectmeta.All
	defer func() { subjectmeta.All = restore }()
	bumped := make([]subjectmeta.Meta, len(restore))
	copy(bumped, restore)
	for i := range bumped {
		if bumped[i].Name == "li" {
			bumped[i].Version = "li-v2"
		}
	}
	subjectmeta.All = bumped

	gateV2 := specIngestIdentity()
	if gateV2 == gateV1 {
		t.Fatal("a subject footprint bump must change the SpecIngestIdentity gate")
	}

	out, skipped, err = filterResumeJobs(ctx, st, reg, jobs, true, gateV2, func(string, ...any) {})
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 0 || len(out) != 1 || out[0].specID != "33.128" {
		t.Fatalf("a subject bump must re-queue the owning spec, not skip it: out=%v skipped=%d", out, skipped)
	}
	// And the stale 'done' row must have been wiped by ResetIngestLog under the new
	// gate, so the redo is clean.
	done, err := st.IsIngestDone(ctx, "33.128", "19.6.0", gateV2)
	if err != nil {
		t.Fatal(err)
	}
	if done {
		t.Fatal("stale 'done' row survived the identity bump")
	}
}

// TestModelBumpDoesNotInvalidateIngestLog locks the OTHER half of the split: a
// model change must NOT invalidate the ingest_log — re-embed, never re-ingest.
// specIngestIdentity is model-independent by construction (it takes no model
// input), so the same gate value is produced regardless of the configured
// embedder, and a spec stamped 'done' under it stays skip-eligible across a model
// bump. (Re-embedding is driven separately by embed.ClauseHash / EmbedIdentity.)
func TestModelBumpDoesNotInvalidateIngestLog(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "shard.duckdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if err := st.Reset(ctx); err != nil {
		t.Fatal(err)
	}

	gate := specIngestIdentity()
	if err := st.MarkIngestStarted(ctx, "23.501", "19.0.0", gate); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkIngestDone(ctx, "23.501", "19.0.0"); err != nil {
		t.Fatal(err)
	}

	// The gate does not depend on the embedding model: a "model bump" produces the
	// identical SpecIngestIdentity, so the 'done' row is still valid and skipped.
	reg := subject.New()
	jobs := []ingestJob{{path: "x", specID: "23.501", release: "Rel-19", version: "19.0.0"}}
	out, skipped, err := filterResumeJobs(ctx, st, reg, jobs, true, specIngestIdentity(), func(string, ...any) {})
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 1 || len(out) != 0 {
		t.Fatalf("a model bump must NOT re-ingest a done spec (re-embed only): out=%d skipped=%d", len(out), skipped)
	}
}
