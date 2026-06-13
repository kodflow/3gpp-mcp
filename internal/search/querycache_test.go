package search

import (
	"context"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/embed"
)

// countingEmbedder is a deterministic fake that counts forward passes, so a test
// can assert the cache actually skips recomputation.
type countingEmbedder struct {
	calls int // number of texts actually embedded (cache misses)
}

func (c *countingEmbedder) Enabled() bool   { return true }
func (c *countingEmbedder) Dim() int        { return embed.Dim }
func (c *countingEmbedder) ModelID() string { return "fake" }
func (c *countingEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		c.calls++
		v := make([]float32, embed.Dim)
		v[0] = float32(len(t)) // deterministic, content-dependent
		out[i] = v
	}
	return out, nil
}

func TestQueryCacheHitsAndEvicts(t *testing.T) {
	fake := &countingEmbedder{}
	e := withQueryCache(fake, 2)
	ctx := context.Background()

	// Same query twice → one real forward pass, identical vector both times.
	a1, _ := e.Embed(ctx, []string{"amf registration"})
	a2, _ := e.Embed(ctx, []string{"amf registration"})
	if fake.calls != 1 {
		t.Fatalf("calls=%d, want 1 (second served from cache)", fake.calls)
	}
	if a1[0][0] != a2[0][0] {
		t.Fatalf("cached vector differs from computed (%v vs %v) — cache must be byte-identical", a1[0][0], a2[0][0])
	}

	// Fill past the cap (2) to force eviction of the oldest ("amf registration").
	_, _ = e.Embed(ctx, []string{"smf session"})      // miss → 2
	_, _ = e.Embed(ctx, []string{"pcf policy"})       // miss → 3, evicts "amf registration"
	_, _ = e.Embed(ctx, []string{"amf registration"}) // evicted → miss → 4
	if fake.calls != 4 {
		t.Fatalf("calls=%d, want 4 (LRU evicted the oldest entry)", fake.calls)
	}
}

func TestQueryCacheBypassMultiTextAndDisabled(t *testing.T) {
	ctx := context.Background()

	// Multi-text batch is never cached (not the query path).
	fake := &countingEmbedder{}
	e := withQueryCache(fake, 8)
	_, _ = e.Embed(ctx, []string{"a", "b"})
	_, _ = e.Embed(ctx, []string{"a", "b"})
	if fake.calls != 4 {
		t.Fatalf("multi-text calls=%d, want 4 (batches bypass the cache)", fake.calls)
	}

	// size 0 → unwrapped: every call recomputes.
	bare := &countingEmbedder{}
	u := withQueryCache(bare, 0)
	_, _ = u.Embed(ctx, []string{"x"})
	_, _ = u.Embed(ctx, []string{"x"})
	if bare.calls != 2 {
		t.Fatalf("disabled-cache calls=%d, want 2 (no caching)", bare.calls)
	}
}
