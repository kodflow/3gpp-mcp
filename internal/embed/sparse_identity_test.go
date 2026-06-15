package embed

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// TestSparseIdentitySeparateFromDense pins the core guarantee: the sparse identity
// is empty for the dense-only default (no sparse head) and, when present, is
// computed independently of the dense EmbedIdentity — so producing sparse never
// invalidates the (already-computed) dense vectors.
func TestSparseIdentityDefaultDenseOnly(t *testing.T) {
	// Default registry (bge-m3 / bge-m3-fp16) declares NO sparse_output.
	if got := SparseModelID(); got != "" {
		t.Errorf("SparseModelID()=%q, want \"\" (dense-only default has no sparse head)", got)
	}
	if SparseCapable() {
		t.Error("SparseCapable()=true, want false for the dense-only default")
	}
	if got := ResolveSparseID("bge-m3"); got != "" {
		t.Errorf("ResolveSparseID(bge-m3)=%q, want \"\" with no sparse head", got)
	}
	if got := ResolveSparseID("local"); got != "hash-local-sparse" {
		t.Errorf("ResolveSparseID(local)=%q, want hash-local-sparse", got)
	}
	if got := ResolveSparseID(""); got != "" {
		t.Errorf("ResolveSparseID(\"\")=%q, want \"\"", got)
	}
	if got := (Local{}).SparseModelID(); got != "hash-local-sparse" {
		t.Errorf("Local.SparseModelID()=%q, want hash-local-sparse", got)
	}
}

func TestSparsePartsIdentity(t *testing.T) {
	none := model.SparseParts{ModelID: "bge-m3", ModelRevision: "5617a9f", SparseOutput: ""}
	if none.Identity() != "" {
		t.Errorf("Identity with empty SparseOutput=%q, want \"\"", none.Identity())
	}
	a := model.SparseParts{ModelID: "bge-m3", ModelRevision: "5617a9f", TokenizerRevision: "5617a9f", SparseOutput: "sparse_weights"}
	if a.Identity() == "" {
		t.Fatal("Identity with a sparse head must be non-empty")
	}
	// A head/revision change must flip the identity (so the bake re-runs sparse-only).
	b := a
	b.ModelRevision = "deadbee"
	if a.Identity() == b.Identity() {
		t.Error("sparse identity must change when the model revision changes")
	}
}

// TestSparseModelIDActivatesViaRegistry: making a sparse-exported model active
// (via an EMBED_MODELS_CONFIG override declaring sparse_output) lights up the
// sparse identity WITHOUT any code change — the registry-driven activation path.
func TestSparseModelIDActivatesViaRegistry(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "models.yaml")
	yaml := "" +
		"active: bge-m3-sparse\n" +
		"models:\n" +
		"  - name: bge-m3-sparse\n" +
		"    family: bge-m3\n" +
		"    dir: data/models/bge-m3-sparse\n" +
		"    precision: fp32\n" +
		"    dim: 1024\n" +
		"    normalization: l2\n" +
		"    revision: 5617a9f\n" +
		"    tokenizer_revision: 5617a9f\n" +
		"    inputs: [input_ids, attention_mask]\n" +
		"    output: sentence_embedding\n" +
		"    sparse_output: sparse_weights\n"
	if err := os.WriteFile(cfg, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EMBED_MODELS_CONFIG", cfg)
	if !SparseCapable() {
		t.Fatal("SparseCapable()=false after activating a sparse-exported model")
	}
	if SparseModelID() == "" {
		t.Fatal("SparseModelID() empty after activating a sparse-exported model")
	}
	if ResolveSparseID("bge-m3") != SparseModelID() {
		t.Error("ResolveSparseID(bge-m3) must equal SparseModelID() (single-sourced)")
	}
}
