package embed

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// collectApply runs Apply with the local embedder and returns chunk_id -> vector.
func collectApply(t *testing.T, items []Item) (map[uint64][]float32, int) {
	t.Helper()
	got := map[uint64][]float32{}
	n, err := Apply(context.Background(), Local{}, items, func(_ context.Context, id uint64, v []float32, _ string) error {
		got[id] = append([]float32(nil), v...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return got, n
}

// TestLengthBucketingIsVectorIdentical is the identity-safety proof for the
// length-bucketing optimisation: reordering clauses into length buckets must yield
// byte-identical per-chunk vectors and the same embedded count as the legacy
// in-order path — padding is attention-masked, so a vector never depends on its
// batch-mates.
func TestLengthBucketingIsVectorIdentical(t *testing.T) {
	// Build items with a wide, scrambled length spread (short headings vs long
	// table-like bodies) so bucketing actually reorders them across batches.
	var items []Item
	for i := 0; i < 200; i++ {
		body := strings.Repeat("word ", (i*37)%500+1) // 1..500 words, non-monotonic
		items = append(items, Item{
			ChunkID: uint64(i + 1),
			Heading: fmt.Sprintf("clause %d", i),
			Text:    body,
		})
	}

	t.Setenv("EMBED_BUCKET_WINDOW", "64") // bucketing ON (window spans many batches)
	bucketed, nb := collectApply(t, items)

	t.Setenv("EMBED_BUCKET_WINDOW", "1") // bucketing OFF (legacy in-order batches)
	plain, np := collectApply(t, items)

	if nb != np || nb != len(items) {
		t.Fatalf("embedded count drift: bucketed=%d plain=%d want %d", nb, np, len(items))
	}
	if len(bucketed) != len(plain) {
		t.Fatalf("vector-set size drift: bucketed=%d plain=%d", len(bucketed), len(plain))
	}
	for id, vb := range bucketed {
		vp, ok := plain[id]
		if !ok {
			t.Fatalf("chunk %d embedded under bucketing but not plain", id)
		}
		if len(vb) != len(vp) {
			t.Fatalf("chunk %d dim drift: %d vs %d", id, len(vb), len(vp))
		}
		for k := range vb {
			if vb[k] != vp[k] {
				t.Fatalf("chunk %d vector differs at %d: %v vs %v (bucketing must be identity-safe)", id, k, vb[k], vp[k])
			}
		}
	}
}
