package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPointModelsAtCacheFollowsTheActiveModel is the regression test for a defect
// that would have shipped a semantically dead image.
//
// The two halves of the embedder resolve the model differently. The Go side reads
// the registry; the Rust cdylib (rust/embed-core's ort backend) reads
// EMBED_MODEL_DIR and, when it is unset, falls back to the LITERAL relative path
// "data/models/bge-m3". pointModelsAtCache is the seam that sets it, and it used
// to look for a directory called "bge-m3" and nothing else.
//
// The image ships the dual-head export INSTEAD of the dense-only one, because
// SparseCapable() reads the ACTIVE registry entry and a dense-only active model
// drops the learned-lexical arm. So on that image there is no bge-m3 directory:
// EMBED_MODEL_DIR stayed unset, the cdylib looked for "data/models/bge-m3"
// relative to a working directory that has no such path, and the first semantic
// query failed — in a container whose corpus is full of vectors and whose every
// build-time check passed.
func TestPointModelsAtCacheFollowsTheActiveModel(t *testing.T) {
	cache := t.TempDir()
	models := filepath.Join(cache, "models")
	sparseDir := filepath.Join(models, "bge-m3-sparse")
	if err := os.MkdirAll(sparseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The sentinel setIfPresent looks for. Only the dual-head model is present,
	// exactly as in the image.
	if err := os.WriteFile(filepath.Join(sparseDir, "model.onnx"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	registry := filepath.Join(cache, "models.yaml")
	if err := os.WriteFile(registry, []byte(
		"active: bge-m3-sparse\n"+
			"models:\n"+
			"  - name: bge-m3-sparse\n"+
			"    family: bge-m3\n"+
			"    dir: "+filepath.ToSlash(sparseDir)+"\n"+
			"    precision: fp32\n"+
			"    dim: 1024\n"+
			"    normalization: l2\n"+
			"    windowing: mean_pool\n"+
			"    max_tokens: 2048\n"+
			"    revision: 5617a9f\n"+
			"    tokenizer_revision: 5617a9f\n"+
			"    inputs: [input_ids, attention_mask]\n"+
			"    output: sentence_embedding\n"+
			"    sparse_output: sparse_weights\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("MCP3GPP_CACHE", cache)
	t.Setenv("EMBED_MODELS_CONFIG", registry)
	t.Setenv("EMBED_MODEL_DIR", "") // the state a fresh container starts in

	pointModelsAtCache()

	got := os.Getenv("EMBED_MODEL_DIR")
	if want := sparseDir; got != want {
		t.Errorf("EMBED_MODEL_DIR = %q, want %q.\n"+
			"The cdylib would fall back to the relative path \"data/models/bge-m3\", "+
			"which does not exist in the image, and semantic search would fail at the first query.",
			got, want)
	}
}

// TestPointModelsAtCacheKeepsAnExplicitValue — an operator who exported the path
// has said what they mean, and the cache must not overrule it.
func TestPointModelsAtCacheKeepsAnExplicitValue(t *testing.T) {
	cache := t.TempDir()
	dir := filepath.Join(cache, "models", "bge-m3")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "model.onnx"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("MCP3GPP_CACHE", cache)
	t.Setenv("EMBED_MODEL_DIR", "/somewhere/deliberate")

	pointModelsAtCache()

	if got := os.Getenv("EMBED_MODEL_DIR"); got != "/somewhere/deliberate" {
		t.Errorf("EMBED_MODEL_DIR = %q, want the operator's own value", got)
	}
}
