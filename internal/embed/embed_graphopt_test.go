//go:build onnx

package embed

import (
	"context"
	"testing"
)

// TestGraphOptMatchesDefault checks that enabling ORT graph optimisation
// (EMBED_GRAPH_OPT=1) does not change retrieval semantics: graph fusion is
// algebraic, so the optimised vectors must stay essentially identical to the
// default session (cosine ~1.0). Runs on CPU; skips without the model.
func TestGraphOptMatchesDefault(t *testing.T) {
	if New(); !New().Enabled() {
		t.Skip("BGE-M3 model/runtime absent — set up via `make model`")
	}
	ctx := context.Background()
	texts := []string{
		"Generation of xIRI at the AMF over LI_X2 during UE registration",
		"Service Area Restriction defines allowed and non-allowed areas",
		"AMF",
	}

	t.Setenv("EMBED_GRAPH_OPT", "")
	base := New()
	if !base.Enabled() {
		t.Skip("model absent")
	}
	bvecs, err := base.Embed(ctx, texts)
	if err != nil {
		t.Fatalf("default embed: %v", err)
	}

	t.Setenv("EMBED_GRAPH_OPT", "1")
	opt := New()
	if !opt.Enabled() {
		t.Fatalf("graph-opt embedder disabled — option wiring rejected the session")
	}
	ovecs, err := opt.Embed(ctx, texts)
	if err != nil {
		t.Fatalf("graph-opt embed: %v", err)
	}

	for i := range texts {
		cos := dot(bvecs[i], ovecs[i])
		if cos < 0.9999 {
			t.Errorf("text %d: graph-opt vs default cos=%.6f, want ~1.0 (graph fusion must be algebraic)", i, cos)
		}
	}
}
