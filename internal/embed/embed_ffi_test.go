//go:build embed_ffi

package embed

import (
	"context"
	"math"
	"testing"
)

// TestFFIEmbedderSeam exercises the cdylib → cgo → Go seam end to end: it builds an embedder
// over rust/embed-core and checks the vector crosses the FFI boundary intact (Dim 1024,
// deterministic per query, L2-normalised). Run with: go test -tags embed_ffi ./internal/embed/
// after `cargo build --release --manifest-path rust/embed-core/Cargo.toml`.
func TestFFIEmbedderSeam(t *testing.T) {
	e := newEmbedder()
	if !e.Enabled() {
		t.Fatal("ffi embedder should be enabled")
	}
	if got := e.Dim(); got != 1024 {
		t.Fatalf("Dim()=%d, want 1024", got)
	}
	if e.ModelID() == "" {
		t.Fatal("ModelID() empty")
	}

	vs, err := e.Embed(context.Background(), []string{"AMF registration procedure", "AMF registration procedure", "a different query"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vs) != 3 || len(vs[0]) != 1024 {
		t.Fatalf("got %d vectors of len %d, want 3×1024", len(vs), len(vs[0]))
	}
	// Deterministic: same text → identical vector across the boundary.
	for i := range vs[0] {
		if vs[0][i] != vs[1][i] {
			t.Fatalf("non-deterministic FFI embed at dim %d: %v != %v", i, vs[0][i], vs[1][i])
		}
	}
	// L2-normalised.
	var sum float64
	for _, x := range vs[0] {
		sum += float64(x) * float64(x)
	}
	if math.Abs(math.Sqrt(sum)-1.0) > 1e-4 {
		t.Fatalf("vector not L2-normalised: |v|=%v", math.Sqrt(sum))
	}
	// Different text → different vector.
	same := true
	for i := range vs[0] {
		if vs[0][i] != vs[2][i] {
			same = false
			break
		}
	}
	if same {
		t.Fatal("different queries produced identical vectors")
	}
	t.Logf("FFI seam OK: backend=%s, 1024-dim normalised vectors crossed cdylib→cgo→Go", e.ModelID())
}
