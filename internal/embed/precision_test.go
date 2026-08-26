package embed

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRegistrySelectsAndFlipsIdentity proves the model registry: selecting a
// different model (here the fp16 variant via EMBED_MODEL) resolves a different
// ModelSpec AND flips the EmbedIdentity — so the serve guard refuses to score an
// fp16 query against an fp32 corpus, or merge mixed-precision shards. CGO-free
// (default build, no model needed).
func TestRegistrySelectsAndFlipsIdentity(t *testing.T) {
	t.Setenv("EMBED_MODELS_CONFIG", "") // ignore any operator file
	t.Setenv("EMBED_MODEL", "")

	m := ActiveModel()
	if m.Name != "bge-m3" || m.Precision != PrecisionFP32 {
		t.Fatalf("default active = %q/%q, want bge-m3/fp32", m.Name, m.Precision)
	}
	fp32 := bgeModelID()

	t.Setenv("EMBED_MODEL", "bge-m3-fp16")
	m16 := ActiveModel()
	if m16.Name != "bge-m3-fp16" || m16.Precision != PrecisionFP16 {
		t.Fatalf("EMBED_MODEL=bge-m3-fp16 active = %q/%q, want bge-m3-fp16/fp16", m16.Name, m16.Precision)
	}
	if bgeModelID() == fp32 {
		t.Errorf("fp16 identity == fp32 identity — precision not folded into EmbedIdentity")
	}

	// Unknown selection falls back (never panics, never empty identity).
	t.Setenv("EMBED_MODEL", "does-not-exist")
	if id := bgeModelID(); id == "" {
		t.Errorf("unknown model yielded empty identity")
	}
}

// TestRegistryFileOverride proves an external YAML overlays the embedded default:
// it can add a model and switch `active`, and EMBED_MODEL still wins over the file.
func TestRegistryFileOverride(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "models.yaml")
	yaml := `
active: my-model
models:
  - name: my-model
    family: custom
    dir: /models/custom
    precision: fp32
    dim: 1024
    normalization: l2
    revision: abc1234
    tokenizer_revision: abc1234
    inputs: [ids, mask]
    output: out
`
	if err := os.WriteFile(cfg, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EMBED_MODELS_CONFIG", cfg)

	t.Setenv("EMBED_MODEL", "")
	if m := ActiveModel(); m.Name != "my-model" || m.Family != "custom" || m.Output != "out" {
		t.Fatalf("file active not honoured: %+v", m)
	}
	// the embedded default models are still present (merge, not replace).
	// models.yaml declare le repertoire en slashes POSIX ("data/models/bge-m3") et
	// le registre le conserve tel quel — c'est valide, les API fichier de Go
	// acceptent les slashes sur toutes les plateformes. Comparer via
	// filepath.ToSlash plutot qu'a un filepath.Join dependant de l'OS.
	t.Setenv("EMBED_MODEL", "bge-m3")
	if m := ActiveModel(); m.Name != "bge-m3" || filepath.ToSlash(m.Dir) != "data/models/bge-m3" {
		t.Errorf("embedded bge-m3 lost after override: %+v", m)
	}
}
