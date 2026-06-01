//go:build onnx

package embed

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentEmbedNoCorruption guards the shared, reusable output buffer
// (onnxEmbedder.outBuf): concurrent Embed calls must each get their OWN vectors,
// never a neighbour's. Run with -race. Compares every goroutine's output to a
// serial reference; skips without the model.
func TestConcurrentEmbedNoCorruption(t *testing.T) {
	e := New()
	if !e.Enabled() {
		t.Skip("BGE-M3 model/runtime absent — set up via `make model`")
	}
	ctx := context.Background()

	// Distinct text sets per worker, each spanning >1 batch so outBuf is reused.
	sets := make([][]string, 6)
	want := make([][][]float32, len(sets))
	for w := range sets {
		var ts []string
		for i := 0; i < batchSize+4; i++ {
			ts = append(ts, fmt.Sprintf("worker %d clause %d about NF interface N%d", w, i, (w+i)%9))
		}
		sets[w] = ts
		ref, err := e.Embed(ctx, ts) // serial reference
		if err != nil {
			t.Fatalf("reference embed: %v", err)
		}
		want[w] = ref
	}

	var wg sync.WaitGroup
	errs := make([]error, len(sets))
	got := make([][][]float32, len(sets))
	for w := range sets {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			got[w], errs[w] = e.Embed(ctx, sets[w])
		}(w)
	}
	wg.Wait()

	for w := range sets {
		if errs[w] != nil {
			t.Fatalf("worker %d: %v", w, errs[w])
		}
		if len(got[w]) != len(want[w]) {
			t.Fatalf("worker %d: len %d != %d", w, len(got[w]), len(want[w]))
		}
		for i := range got[w] {
			for k := range got[w][i] {
				if got[w][i][k] != want[w][i][k] {
					t.Fatalf("worker %d text %d differs at %d: concurrent=%v serial=%v (outBuf corruption)",
						w, i, k, got[w][i][k], want[w][i][k])
				}
			}
		}
	}
}
