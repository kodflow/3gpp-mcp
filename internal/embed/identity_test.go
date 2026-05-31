package embed_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/bootstrap"
	"github.com/kodflow/3gpp-mcp/internal/embed"
	"github.com/kodflow/3gpp-mcp/internal/model"
)

// TestBGEModelRevisionMatchesPinnedCommit is the coupling guard the plan PR-6
// names ("declared revision == real pinned weights"): the model revision baked
// into the EmbedIdentity (embed.BGEModelRevision) MUST equal the 7-hex prefix of
// the commit bootstrap actually downloads (bootstrap.BGECommit). A future weight
// bump that updates the download pin but forgets the identity-side revision would
// otherwise ship vectors from new weights stamped with the OLD identity — the
// exact silent-stale-vector failure of finding model-commit-not-in-identity.
func TestBGEModelRevisionMatchesPinnedCommit(t *testing.T) {
	if want := bootstrap.BGECommit[:7]; embed.BGEModelRevision != want {
		t.Fatalf("embed.BGEModelRevision=%q must equal bootstrap.BGECommit[:7]=%q — bump them in lockstep",
			embed.BGEModelRevision, want)
	}
	// The tokenizer ships in the same BGE-M3 commit today; if it is ever split to
	// a different revision this assertion documents the intent (still a prefix of
	// the model commit unless deliberately decoupled).
	if !strings.HasPrefix(bootstrap.BGECommit, embed.BGETokenizerRevision) {
		t.Fatalf("embed.BGETokenizerRevision=%q is not a prefix of bootstrap.BGECommit=%q",
			embed.BGETokenizerRevision, bootstrap.BGECommit)
	}
}

// TestBGEEmbedPartsCarriesAllComponents asserts the canonical EmbedParts the
// production backend reports is FULLY populated (plan PR-6: model_id,
// model_revision, tokenizer_revision, vector_dim, normalization_mode, precision).
// A missing component would silently collapse two genuinely different models to
// one identity.
func TestBGEEmbedPartsCarriesAllComponents(t *testing.T) {
	p := embed.BGEEmbedParts()
	if p.ModelID == "" {
		t.Error("ModelID empty")
	}
	if p.ModelRevision == "" {
		t.Error("ModelRevision empty")
	}
	if p.TokenizerRevision == "" {
		t.Error("TokenizerRevision empty")
	}
	if p.VectorDim != strconv.Itoa(embed.Dim) {
		t.Errorf("VectorDim=%q, want %q", p.VectorDim, strconv.Itoa(embed.Dim))
	}
	if p.NormalizationMode == "" {
		t.Error("NormalizationMode empty")
	}
	if p.Precision == "" {
		t.Error("Precision empty")
	}
}

// TestEmbedIdentitySensitiveToEveryComponent locks that EVERY identity component
// changes the digest — so a revision/tokenizer/dim/normalisation/precision bump
// flips EmbedIdentity (forcing re-embed + serve refusal) instead of mixing
// vectors from different models under one identity.
func TestEmbedIdentitySensitiveToEveryComponent(t *testing.T) {
	base := embed.BGEEmbedParts()
	baseID := model.EmbedIdentity(base)

	mut := []struct {
		name  string
		apply func(p *model.EmbedParts)
	}{
		{"model_id", func(p *model.EmbedParts) { p.ModelID = "bge-large" }},
		{"model_revision", func(p *model.EmbedParts) { p.ModelRevision = "deadbee" }},
		{"tokenizer_revision", func(p *model.EmbedParts) { p.TokenizerRevision = "feedfac" }},
		{"vector_dim", func(p *model.EmbedParts) { p.VectorDim = "768" }},
		{"normalization_mode", func(p *model.EmbedParts) { p.NormalizationMode = "none" }},
		{"precision", func(p *model.EmbedParts) { p.Precision = "fp16" }},
	}
	for _, m := range mut {
		p := base
		m.apply(&p)
		if got := model.EmbedIdentity(p); got == baseID {
			t.Errorf("changing %s did NOT change EmbedIdentity (%q) — components are not all folded in", m.name, got)
		}
	}
}

// TestClauseHashReembedsOnRevisionBump proves the per-clause re-embed gate fires
// on a weight revision change: the same text under two different model revisions
// yields different ClauseHashes, so embed.Apply re-embeds every clause WITHOUT
// re-ingesting (the model bump must not invalidate ingest_log, plan PR-3 split).
func TestClauseHashReembedsOnRevisionBump(t *testing.T) {
	oldID := model.EmbedIdentity(model.EmbedParts{ModelID: "bge-m3", ModelRevision: "5617a9f"})
	newID := model.EmbedIdentity(model.EmbedParts{ModelID: "bge-m3", ModelRevision: "abcdef0"})
	if oldID == newID {
		t.Fatal("two revisions collapsed to one EmbedIdentity")
	}
	hOld := embed.ClauseHash("Heading", "body text", oldID)
	hNew := embed.ClauseHash("Heading", "body text", newID)
	if hOld == hNew {
		t.Fatalf("ClauseHash identical across model revisions (%q) — re-embed would never fire", hOld)
	}
}

// TestResolveModelIDMatchesBackendModelID locks that the CGO-free family→identity
// resolver discover uses returns the SAME string the lexical backend's ModelID()
// returns, so discover's model-drift compare keys on the identity merge stamps
// and serve checks. (The onnx BGE-M3 ModelID() lives behind -tags onnx; the
// resolver reproduces it from the canonical parts, asserted equal to the digest.)
func TestResolveModelIDMatchesBackendModelID(t *testing.T) {
	if got := embed.ResolveModelID("local"); got != (embed.Local{}).ModelID() {
		t.Errorf("ResolveModelID(local)=%q, want %q", got, embed.Local{}.ModelID())
	}
	if got := embed.ResolveModelID(""); got != "" {
		t.Errorf("ResolveModelID(\"\")=%q, want empty (lexical/disabled)", got)
	}
	// bge-m3 resolves to the canonical EmbedIdentity digest (the same value the
	// onnx backend's ModelID returns) — verified against the parts here.
	if got, want := embed.ResolveModelID("bge-m3"), model.EmbedIdentity(embed.BGEEmbedParts()); got != want {
		t.Errorf("ResolveModelID(bge-m3)=%q, want canonical EmbedIdentity %q", got, want)
	}
}
