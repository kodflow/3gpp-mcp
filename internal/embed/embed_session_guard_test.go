//go:build onnx

package embed

import (
	"path/filepath"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/onnxrt"
)

// TestActiveModelSessionLoads is the anti-regression guard for the
// embedder-disabled-extended-fusion incident: under onnxruntime 1.26 the DEFAULT
// graph-optimisation level (ORT_ENABLE_ALL) ran SimplifiedLayerNormFusion on the
// bge-m3-fp16 export, the first ORT session threw, newEmbedder() degraded to
// Disabled{}, and prod served lexical-only despite the vectors being baked.
//
// The contract this locks: if the active model's artifacts (ORT lib + model.onnx +
// tokenizer.json) are ALL present, the default embedder MUST load. It catches a
// future ORT/opset/model incompatibility in CI / the image build (where the model
// and ORT are present), not in production. It SKIPS when the artifacts are absent
// (a plain `go test` without `make model`), so it never flakes the unit suite.
func TestActiveModelSessionLoads(t *testing.T) {
	spec := ActiveModel()
	modelPath := filepath.Join(activeModelDir(spec), "model.onnx")
	tokPath := filepath.Join(activeTokenizerDir(spec), "tokenizer.json")
	if !fileExists(onnxrt.LibPath()) || !fileExists(modelPath) || !fileExists(tokPath) {
		t.Skipf("active-model artifacts absent (ort=%s model=%s tok=%s) — set up via `make model`",
			onnxrt.LibPath(), modelPath, tokPath)
	}
	// Exercise the DEFAULT serve path (no EMBED_GRAPH_OPT, no EMBEDDER override):
	// this is exactly what the deployed `serve` binary runs.
	t.Setenv("EMBEDDER", "")
	t.Setenv("EMBED_GRAPH_OPT", "")
	if e := New(); !e.Enabled() {
		t.Fatalf("active model %q present but embedder DISABLED — the first ORT session failed to load "+
			"(e.g. an extended graph-opt fusion throws on this export); serve would silently run lexical-only", spec.Name)
	}
}
