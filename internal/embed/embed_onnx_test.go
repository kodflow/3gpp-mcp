//go:build onnx

package embed

import (
	"context"
	"math"
	"testing"
)

// TestBatchMatchesSingle is the A2 correctness guard: padding + attention_mask
// must make a batched embedding identical to embedding the same text alone.
// If the mask were wrong, the padded (shorter) rows in a mixed-length batch
// would drift. Skips when the BGE-M3 model isn't present (CI without models).
func TestBatchMatchesSingle(t *testing.T) {
	e := New()
	if !e.Enabled() {
		t.Skip("BGE-M3 model/runtime absent — set up via `make model`")
	}
	ctx := context.Background()
	// Deliberately mixed lengths so the batch must pad the short ones.
	texts := []string{
		"AMF",
		"Generation of xIRI at the AMF over LI_X2 during UE registration and handover procedures in the 5G system",
		"Service Area Restriction defines allowed and non-allowed areas",
	}

	batched, err := e.Embed(ctx, texts) // padded batch
	if err != nil {
		t.Fatalf("batched embed: %v", err)
	}
	for i, txt := range texts {
		solo, err := e.Embed(ctx, []string{txt}) // b=1, no padding
		if err != nil {
			t.Fatalf("solo embed: %v", err)
		}
		if len(batched[i]) != Dim || len(solo[0]) != Dim {
			t.Fatalf("dim mismatch: %d/%d want %d", len(batched[i]), len(solo[0]), Dim)
		}
		cos := dot(batched[i], solo[0])
		if cos < 0.999 {
			t.Errorf("text %d: batched vs solo cos=%.5f, want ~1.0 (padding/mask bug)", i, cos)
		}
		if n := norm(solo[0]); math.Abs(float64(n)-1) > 1e-3 {
			t.Errorf("text %d: not unit-norm: |v|=%.5f", i, n)
		}
	}
}

func dot(a, b []float32) float32 {
	var s float32
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}

func norm(v []float32) float32 {
	var s float64
	for _, x := range v {
		s += float64(x) * float64(x)
	}
	return float32(math.Sqrt(s))
}
