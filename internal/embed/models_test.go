package embed

import "testing"

// TestEmbeddedRegistryMatchesPins keeps the embedded default registry in lockstep
// with the identity consts (which the bootstrap-pin test ties to the real download).
// If models.yaml drifts from the pinned BGE-M3 revision/dim/normalization, the
// published identity would silently disagree with what bootstrap fetches.
func TestEmbeddedRegistryMatchesPins(t *testing.T) {
	t.Setenv("EMBED_MODELS_CONFIG", "")
	t.Setenv("EMBED_MODEL", "bge-m3")
	m := ActiveModel()
	if m.Family != bgeModelName {
		t.Errorf("registry bge-m3 family=%q, want %q", m.Family, bgeModelName)
	}
	if m.Revision != BGEModelRevision {
		t.Errorf("registry bge-m3 revision=%q, want %q (lockstep with bootstrap pin)", m.Revision, BGEModelRevision)
	}
	if m.TokenizerRevision != BGETokenizerRevision {
		t.Errorf("registry bge-m3 tokenizer_revision=%q, want %q", m.TokenizerRevision, BGETokenizerRevision)
	}
	if m.Dim != Dim {
		t.Errorf("registry bge-m3 dim=%d, want %d", m.Dim, Dim)
	}
	if m.Normalization != bgeNormalization {
		t.Errorf("registry bge-m3 normalization=%q, want %q", m.Normalization, bgeNormalization)
	}
	if m.Precision != PrecisionFP32 {
		t.Errorf("registry bge-m3 precision=%q, want fp32", m.Precision)
	}
}
