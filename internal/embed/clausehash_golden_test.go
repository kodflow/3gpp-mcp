package embed_test

import (
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/embed"
)

// TestClauseHashGoldenCrossRuntime pins the EXACT clause fingerprint for fixed
// inputs. The IDENTICAL constant is asserted by the Rust embedder unit test
// (rust/embedder/src/hash.rs::clause_hash_golden_cross_runtime), so if both tests
// pass, the Go ClauseHash and Rust clause_hash agree byte-for-byte.
//
// This is the regression that motivated Phase −1: Go used to re-wrap modelID in
// model.EmbedIdentity (a digest-of-a-digest) while Rust hashed modelID raw, so the
// two diverged and a Go embed pass over a Rust-embedded DB re-embedded the whole
// corpus. The constant below is sha256("6 X1\nbody|embedv1-deadbeef0000")[:16] with
// modelID hashed RAW — recompute with `printf '6 X1\nbody|embedv1-deadbeef0000' |
// sha256sum` if you ever need to re-derive it.
func TestClauseHashGoldenCrossRuntime(t *testing.T) {
	const (
		heading  = "6 X1"
		text     = "body"
		identity = "embedv1-deadbeef0000"
		want     = "a2e0978ed24f247a"
	)
	if got := embed.ClauseHash(heading, text, identity); got != want {
		t.Fatalf("ClauseHash golden mismatch: got %q want %q — Go/Rust hash parity is broken; do NOT re-wrap modelID in EmbedIdentity", got, want)
	}
	// EmbedText is the canonical model input both runtimes feed the hasher.
	if got := embed.EmbedText(heading, text); got != "6 X1\nbody" {
		t.Fatalf("EmbedText = %q, want %q", got, "6 X1\nbody")
	}
}
