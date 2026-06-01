//go:build onnx

package embed

import "testing"

// TestFP16AbsentDisables proves the safety invariant via the registry: selecting a
// model whose files are absent DISABLES the embedder rather than falling back to a
// present model under the absent one's identity (which would publish vectors stamped
// with the wrong precision/model). EMBED_MODEL_DIR points the active model at an
// empty temp, so its files are guaranteed absent even after `make model`.
func TestFP16AbsentDisables(t *testing.T) {
	t.Setenv("EMBED_MODELS_CONFIG", "")
	t.Setenv("EMBED_MODEL", "bge-m3-fp16")
	t.Setenv("EMBED_MODEL_DIR", t.TempDir()) // empty → model absent

	if e := New(); e.Enabled() {
		t.Errorf("fp16 model absent but embedder Enabled() — it must Disable, not run another model under an fp16 identity")
	}
}
