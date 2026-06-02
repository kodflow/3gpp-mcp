package embed

import (
	"context"
	"strings"
	"testing"
)

// degradeEmbedder returns a nil vector for any text containing "DEGRADE" — the
// contract a real backend uses to signal an untokenisable clause — and a unit
// vector otherwise.
type degradeEmbedder struct{ dim int }

func (degradeEmbedder) Enabled() bool   { return true }
func (e degradeEmbedder) Dim() int      { return e.dim }
func (degradeEmbedder) ModelID() string { return "degrade-test" }
func (e degradeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		if strings.Contains(t, "DEGRADE") {
			out[i] = nil // untokenisable: must NOT be persisted
			continue
		}
		v := make([]float32, e.dim)
		v[0] = 1
		out[i] = v
	}
	return out, nil
}

// TestApplyBatchedSkipsDegraded proves a nil (degraded) vector is never
// persisted: it is excluded from the batchSet ids, not counted as embedded, and
// its clause is left for retry (StoredHash stays "").
func TestApplyBatchedSkipsDegraded(t *testing.T) {
	t.Setenv("EMBED_BUCKET_WINDOW", "0") // deterministic order, no length sort
	items := []Item{
		{ChunkID: 1, Heading: "ok", Text: "alpha"},
		{ChunkID: 2, Heading: "bad", Text: "DEGRADE me"},
		{ChunkID: 3, Heading: "ok", Text: "beta"},
	}
	var persisted []uint64
	batchSet := func(_ context.Context, ids []uint64, vecs [][]float32, hashes []string) error {
		if len(ids) != len(vecs) || len(ids) != len(hashes) {
			t.Fatalf("ragged batch: ids=%d vecs=%d hashes=%d", len(ids), len(vecs), len(hashes))
		}
		for _, v := range vecs {
			if v == nil {
				t.Fatalf("a nil (degraded) vector reached batchSet — corpus would be corrupted")
			}
		}
		persisted = append(persisted, ids...)
		return nil
	}

	embedded, err := ApplyBatched(context.Background(), degradeEmbedder{dim: 8}, items, batchSet)
	if err != nil {
		t.Fatalf("ApplyBatched: %v", err)
	}
	if embedded != 2 {
		t.Errorf("embedded = %d, want 2 (the degraded clause must not count)", embedded)
	}
	if len(persisted) != 2 {
		t.Fatalf("persisted %v, want exactly chunks 1 and 3", persisted)
	}
	for _, id := range persisted {
		if id == 2 {
			t.Errorf("degraded chunk 2 was persisted — it must stay NULL for retry")
		}
	}
}
