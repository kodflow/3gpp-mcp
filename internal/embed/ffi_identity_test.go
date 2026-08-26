package embed

import (
	"regexp"
	"testing"
)

// digest12 is the shape EmbedIdentity/SparseIdentity produce: 12 lowercase hex.
var digest12 = regexp.MustCompile(`^[0-9a-f]{12}$`)

// TestFFIModelIDMatchesStampedIdentity pins THE contract that, when broken, makes
// cmd/server call store.DisableVSS() on every correctly-vectorised corpus.
//
// The corpus stamps schema_meta.embedding_model with what cmd/embedid prints —
// ResolveModelID("bge-m3"). cmd/server compares that stamp against the live
// embedder's ModelID(). The two MUST be the same string.
//
// Regression guarded: ffiEmbedder.ModelID() used to return the bare family name
// "bge-m3" while the stamp is a 12-hex digest, so the guard fired for every valid
// DB and vector search was disabled silently.
func TestFFIModelIDMatchesStampedIdentity(t *testing.T) {
	// What cmd/embedid writes into the DB (see cmd/embedid/main.go).
	stamped := ResolveModelID("bge-m3")
	if !digest12.MatchString(stamped) {
		t.Fatalf("ResolveModelID(%q) = %q, want a 12-hex EmbedIdentity digest", "bge-m3", stamped)
	}

	// What the embed_ffi query embedder reports for the real ONNX backend.
	// rust/embed-core reports "bge-m3-onnx" when built with the `ort` feature.
	got := ffiModelID("bge-m3-onnx")
	if got != stamped {
		t.Fatalf("ffiModelID(%q) = %q, want %q (== what cmd/embedid stamps).\n"+
			"A mismatch makes cmd/server call DisableVSS() and serve a fully "+
			"vectorised corpus as pure lexical, silently.", "bge-m3-onnx", got, stamped)
	}

	// The specific regression: never the bare family name.
	if got == "bge-m3" {
		t.Fatal("ffiModelID returned the bare family name instead of the identity digest")
	}
}

// TestFFIModelIDHashSeamUnchanged keeps the no-model seam working: a corpus
// embedded by embed.Local carries "hash-local", and the cdylib built WITHOUT the
// ort feature must report exactly that, or the seam validation breaks.
func TestFFIModelIDHashSeamUnchanged(t *testing.T) {
	if got, want := ffiModelID(backendHash), (Local{}).ModelID(); got != want {
		t.Fatalf("ffiModelID(%q) = %q, want %q", backendHash, got, want)
	}
	if got := ffiModelID(backendHash); got != "hash-local" {
		t.Fatalf("hash seam identity drifted: got %q, want %q", got, "hash-local")
	}
}

// TestFFIModelIDUnknownBackendIsTreatedAsReal asserts the default branch: any
// backend string that is not the hash fallback is a real model path and must
// resolve to the BGE-M3 identity rather than to "" (which would read as
// "lexical/disabled" and skip the guard entirely).
func TestFFIModelIDUnknownBackendIsTreatedAsReal(t *testing.T) {
	for _, backend := range []string{"bge-m3-onnx", "bge-m3-onnx-cuda", "something-new"} {
		if got := ffiModelID(backend); got != ResolveModelID("bge-m3") {
			t.Fatalf("ffiModelID(%q) = %q, want the BGE-M3 identity %q", backend, got, ResolveModelID("bge-m3"))
		}
	}
}

// TestFFISparseModelIDMatchesStampedIdentity is the sparse twin of the dense
// contract: the corpus stamps schema_meta.sparse_model with `cmd/embedid --sparse`
// (ResolveSparseID("bge-m3")), and warnIfSparseMissing / cmd/validate compare
// against it. The old code returned the literal "bge-m3-sparse".
func TestFFISparseModelIDMatchesStampedIdentity(t *testing.T) {
	// No sparse head on the loaded model → the arm is simply not offered.
	if got := ffiSparseModelID("bge-m3-onnx", false); got != "" {
		t.Fatalf("ffiSparseModelID(hasSparse=false) = %q, want %q", got, "")
	}

	stamped := ResolveSparseID("bge-m3")
	got := ffiSparseModelID("bge-m3-onnx", true)
	if got != stamped {
		t.Fatalf("ffiSparseModelID = %q, want %q (== what cmd/embedid --sparse stamps)", got, stamped)
	}
	if got == "bge-m3-sparse" {
		t.Fatal("ffiSparseModelID returned the bare literal instead of the identity")
	}
	// When the active model declares a sparse head, the stamp is a digest; when it
	// does not, ResolveSparseID is "" and there is nothing to match. Both are valid
	// states — assert only that the shape is one of the two.
	if stamped != "" && !digest12.MatchString(stamped) {
		t.Fatalf("ResolveSparseID(%q) = %q, want a 12-hex digest or %q", "bge-m3", stamped, "")
	}

	if got, want := ffiSparseModelID(backendHash, true), (Local{}).SparseModelID(); got != want {
		t.Fatalf("ffiSparseModelID(hash) = %q, want %q", got, want)
	}
}
