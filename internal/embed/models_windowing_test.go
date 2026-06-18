package embed

import "testing"

// TestModelSpecWindowingMaxTokensDefaults locks the coherence guarantee that makes
// the new identity components safe across registries: a ModelSpec that OMITS
// windowing / max_tokens (the Kaggle fp16 registry and the data-image registry both
// omit them) must yield the SAME EmbedIdentity as one that declares the canonical
// defaults (truncate / 1024). Without this, adding the components would silently
// fork the identity between the embedded default and those external registries.
func TestModelSpecWindowingMaxTokensDefaults(t *testing.T) {
	omit := ModelSpec{Family: "bge-m3", Dim: 1024, Normalization: "l2", Precision: "fp16", Revision: "r", TokenizerRevision: "r"}
	explicit := omit
	explicit.Windowing = WindowingTruncate
	explicit.MaxTokens = DefaultMaxTokens
	if omit.embedParts().Identity() != explicit.embedParts().Identity() {
		t.Fatal("omitting windowing/max_tokens must yield the canonical truncate/1024 identity — else registries that omit them diverge from the embedded default")
	}

	p := omit.embedParts()
	if p.Windowing != WindowingTruncate || p.MaxTokens != "1024" {
		t.Fatalf("defaults: windowing=%q max_tokens=%q, want %q/1024", p.Windowing, p.MaxTokens, WindowingTruncate)
	}

	// A mean_pool spec must produce a DIFFERENT identity (the urgent Rust port will
	// flip the identity and force a clean re-embed when it lands).
	mp := omit
	mp.Windowing = WindowingMeanPool
	if mp.embedParts().Identity() == omit.embedParts().Identity() {
		t.Fatal("mean_pool spec must differ from the truncate default identity")
	}
}
