//go:build embed_ffi

package embed

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// TestEmbedBothMatchesTheSeparateCalls is the equivalence proof for the combined
// forward pass. Producing two heads from one session.run is only worth anything if
// the answer is IDENTICAL to producing them from two, and "identical" here means
// bit-for-bit: the same graph, the same inputs, the same outputs read by name. A
// tolerance would hide exactly the mistake worth catching — reading the wrong
// output tensor, or windowing differently.
//
// Skipped when the dual-head model is not on this machine; there is nothing to
// compare without it, and a test that silently passes on a missing model would be
// worse than no test.
func TestEmbedBothMatchesTheSeparateCalls(t *testing.T) {
	dir := os.Getenv("EMBED_MODEL_DIR")
	if dir == "" {
		t.Skip("EMBED_MODEL_DIR unset — the dual-head model is not available here")
	}
	if _, err := os.Stat(filepath.Join(dir, "model.onnx")); err != nil {
		t.Skipf("no model.onnx under %s", dir)
	}
	e := ffiEmbedder{}
	if !e.Enabled() {
		t.Skip("the ffi embedder is not enabled")
	}

	ctx := context.Background()
	for _, q := range []string{
		"AMF registration event reported over LI_X2",
		"PDU session establishment",
		"X1 ActivateTask",
	} {
		dense, sparse, ok := e.EmbedBoth(ctx, q)
		if !ok {
			t.Skipf("no combined path for %q (dense-only model?)", q)
		}

		want, err := e.Embed(ctx, []string{q})
		if err != nil || len(want) != 1 {
			t.Fatalf("Embed(%q): %v", q, err)
		}
		if len(dense) != len(want[0]) {
			t.Fatalf("dense length %d, want %d", len(dense), len(want[0]))
		}
		for i := range dense {
			if dense[i] != want[0][i] {
				t.Errorf("%q dense[%d] = %v, want %v (Δ=%g)",
					q, i, dense[i], want[0][i], math.Abs(float64(dense[i]-want[0][i])))
				break
			}
		}

		wantSparse, err := e.EmbedSparse(ctx, []string{q})
		if err != nil || len(wantSparse) != 1 {
			t.Fatalf("EmbedSparse(%q): %v", q, err)
		}
		if len(sparse) != len(wantSparse[0]) {
			t.Fatalf("%q sparse has %d terms, want %d", q, len(sparse), len(wantSparse[0]))
		}
		for id, w := range wantSparse[0] {
			if got, present := sparse[id]; !present || got != w {
				t.Errorf("%q sparse[%d] = (%v, present=%v), want %v", q, id, got, present, w)
				break
			}
		}
	}
}
