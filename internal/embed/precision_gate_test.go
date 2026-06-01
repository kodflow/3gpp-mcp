//go:build onnx

package embed

import (
	"context"
	"path/filepath"
	"testing"
)

// TestFP16GateVsFP32 is the acceptance gate for an fp16 artifact: its vectors must
// stay essentially identical to fp32 (mean cosine ≥ 0.9995) before fp16 may become
// the canonical corpus precision — otherwise retrieval silently degrades and a 10M
// re-embed is wasted. Skips unless both the fp32 (model "bge-m3") and an fp16 model
// ("bge-m3-fp16") are present, i.e. once an operator has provided a validated fp16
// export (WITH_FP16=1 make model) at the registry's fp16 `dir`.
func TestFP16GateVsFP32(t *testing.T) {
	t.Setenv("EMBED_MODEL_DIR", "") // multi-model run resolves dirs via EMBED_MODELS_BASE
	t.Setenv("EMBED_MODEL", "bge-m3-fp16")
	fp16Dir := resolveBase(ActiveModel().Dir)
	if !fileExists(filepath.Join(fp16Dir, "model.onnx")) {
		t.Skipf("no fp16 model at %s — run `WITH_FP16=1 make model` to validate the fp16 artifact", fp16Dir)
	}

	t.Setenv("EMBED_MODEL", "bge-m3")
	fp32 := New()
	if !fp32.Enabled() {
		t.Skip("fp32 model absent")
	}
	t.Setenv("EMBED_MODEL", "bge-m3-fp16")
	fp16 := New()
	if !fp16.Enabled() {
		t.Fatalf("fp16 model present but embedder disabled — check the registry inputs/output match the export")
	}

	ctx := context.Background()
	texts := []string{
		"Generation of xIRI at the AMF over LI_X2 during UE registration",
		"The SMF selects a UPF for the PDU session and configures N4 rules",
		"Service Area Restriction defines allowed and non-allowed tracking areas",
		"AMF",
	}
	a, err := fp32.Embed(ctx, texts)
	if err != nil {
		t.Fatalf("fp32 embed: %v", err)
	}
	b, err := fp16.Embed(ctx, texts)
	if err != nil {
		t.Fatalf("fp16 embed: %v", err)
	}

	var sum float32
	for i := range texts {
		cos := dot(a[i], b[i])
		t.Logf("text %d: cos(fp32, fp16) = %.6f", i, cos)
		sum += cos
	}
	if mean := sum / float32(len(texts)); mean < 0.9995 {
		t.Errorf("mean cos(fp32, fp16) = %.6f < 0.9995 — fp16 artifact REJECTED (would degrade retrieval)", mean)
	}
}
