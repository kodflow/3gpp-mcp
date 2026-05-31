package model

import (
	"reflect"
	"testing"
)

func TestPipelineVersion(t *testing.T) {
	lexical := PipelineVersion("")
	bge := PipelineVersion("bge-m3")
	local := PipelineVersion("hash-local")

	if lexical == bge || bge == local || lexical == local {
		t.Fatalf("embedding model must change the pipeline version: lexical=%s bge=%s local=%s", lexical, bge, local)
	}
	if PipelineVersion("bge-m3") != bge {
		t.Fatal("PipelineVersion must be deterministic")
	}
	if len(bge) != 12 {
		t.Fatalf("want a 12-char digest, got %d (%q)", len(bge), bge)
	}
}

// TestSpecIngestIdentitySplit locks the PR-3 keystone: the SpecIngestIdentity
// (the resume/ingest_log gate) shifts on a parser/chunking/schema/subject/ASN.1
// change, but is INVARIANT to the embedding model — because a model bump must
// re-embed, never re-ingest. The base SpecIngestParts excludes a model field
// entirely, so this is enforced structurally; the test pins the behaviour against
// regression (e.g. someone re-folding the model into the ingest ledger).
func TestSpecIngestIdentitySplit(t *testing.T) {
	base := SpecIngestParts{
		ParserVersion:     "html-v1",
		ChunkingVersion:   "clause-leaf-v1",
		SchemaVersion:     "1",
		SubjectFootprints: []string{"liFP", "glossFP"},
		ASN1ScannerHash:   "asn1-v1",
	}
	id := base.Identity()
	if len(id) != 12 {
		t.Fatalf("identity want 12-char digest, got %q", id)
	}
	// Determinism + order-independence of the footprint set.
	reordered := base
	reordered.SubjectFootprints = []string{"glossFP", "liFP"}
	if reordered.Identity() != id {
		t.Fatalf("identity must be order-independent over subject footprints: %q != %q", reordered.Identity(), id)
	}

	// Each content input flips the identity.
	for name, mut := range map[string]func(p *SpecIngestParts){
		"parser":   func(p *SpecIngestParts) { p.ParserVersion = "html-v2" },
		"chunking": func(p *SpecIngestParts) { p.ChunkingVersion = "clause-leaf-v2" },
		"schema":   func(p *SpecIngestParts) { p.SchemaVersion = "2" },
		"subject":  func(p *SpecIngestParts) { p.SubjectFootprints = []string{"liFP2", "glossFP"} },
		"asn1":     func(p *SpecIngestParts) { p.ASN1ScannerHash = "asn1-v2" },
	} {
		p := base
		mut(&p)
		if p.Identity() == id {
			t.Errorf("%s change did NOT flip SpecIngestIdentity", name)
		}
	}
}

// TestEmbedIdentitySeparate locks that the embedding model lives in EmbedIdentity
// (re-embed gate) and is NOT part of the ingest ledger gate. A model change flips
// EmbedIdentity (so vectors recompute) while SpecIngestIdentity is unaffected.
func TestEmbedIdentitySeparate(t *testing.T) {
	bge := EmbedIdentity(EmbedParts{ModelID: "bge-m3"})
	local := EmbedIdentity(EmbedParts{ModelID: "hash-local"})
	lexical := EmbedIdentity(EmbedParts{})
	if bge == local || bge == lexical || local == lexical {
		t.Fatalf("model id must change EmbedIdentity: bge=%s local=%s lexical=%s", bge, local, lexical)
	}
	// A revision/dim/precision bump must also flip it (PR-6 populates these).
	if EmbedIdentity(EmbedParts{ModelID: "bge-m3", ModelRevision: "aaa"}) == EmbedIdentity(EmbedParts{ModelID: "bge-m3", ModelRevision: "bbb"}) {
		t.Error("model revision must flip EmbedIdentity")
	}

	// The model must NOT touch SpecIngestIdentity — compose two build indexes that
	// differ only by the embed model and assert the spec-ingest identity is equal.
	a := CurrentBuildIndex([]string{"liFP", "glossFP"}, "asn1-v1", "bge-m3", GlobalEnrichmentParts{})
	b := CurrentBuildIndex([]string{"liFP", "glossFP"}, "asn1-v1", "bge-m3-v2", GlobalEnrichmentParts{})
	if a.SpecIngestIdentity != b.SpecIngestIdentity {
		t.Error("a model bump invalidated SpecIngestIdentity — would force a full re-ingest (regression)")
	}
	if a.EmbedIdentity == b.EmbedIdentity {
		t.Error("a model bump did NOT change EmbedIdentity — re-embed would be skipped")
	}
}

// TestGlobalEnrichmentIdentity locks that each enricher input flips the identity
// and that it is independent of the spec/embed identities.
func TestGlobalEnrichmentIdentity(t *testing.T) {
	base := GlobalEnrichmentParts{CatalogFootprint: "c1", EvolutionsSeedHash: "e1"}
	id := base.Identity()
	for name, mut := range map[string]func(p *GlobalEnrichmentParts){
		"catalog":    func(p *GlobalEnrichmentParts) { p.CatalogFootprint = "c2" },
		"catalogsrc": func(p *GlobalEnrichmentParts) { p.CatalogSourceHash = "cs2" },
		"openapiver": func(p *GlobalEnrichmentParts) { p.OpenAPIIngestVersion = "o2" },
		"openapisrc": func(p *GlobalEnrichmentParts) { p.OpenAPISourceHash = "os2" },
		"evolutions": func(p *GlobalEnrichmentParts) { p.EvolutionsSeedHash = "e2" },
	} {
		p := base
		mut(&p)
		if p.Identity() == id {
			t.Errorf("%s change did NOT flip GlobalEnrichmentIdentity", name)
		}
	}
}

// TestBuildIndexDiffers locks the discover comparison: a mismatch on any of the
// three identities is reported by name, and an empty (legacy/missing) published
// index reports all three as drifted so it self-heals.
func TestBuildIndexDiffers(t *testing.T) {
	cur := CurrentBuildIndex([]string{"liFP"}, "asn1-v1", "bge-m3", GlobalEnrichmentParts{CatalogFootprint: "c1"})
	if d := cur.Differs(cur); len(d) != 0 {
		t.Fatalf("identical build indexes must not differ, got %v", d)
	}
	var empty BuildIndex
	if d := empty.Differs(cur); !reflect.DeepEqual(d, []string{"spec_ingest_identity", "global_enrichment_identity", "embed_identity"}) {
		t.Fatalf("empty published index must drift on all three, got %v", d)
	}
	// Only the embed model changed → only embed_identity drifts.
	other := CurrentBuildIndex([]string{"liFP"}, "asn1-v1", "bge-m3-v2", GlobalEnrichmentParts{CatalogFootprint: "c1"})
	if d := other.Differs(cur); !reflect.DeepEqual(d, []string{"embed_identity"}) {
		t.Fatalf("model-only change must drift on embed_identity alone, got %v", d)
	}
}
