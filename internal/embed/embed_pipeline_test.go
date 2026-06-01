//go:build onnx

package embed

import (
	"context"
	"fmt"
	"testing"
)

// TestPipelineMatchesSerial proves the 2-stage tokenise/run pipeline is an
// optimisation, not a behaviour change: with the pipeline enabled vs disabled the
// SAME multi-batch input must yield byte-identical vectors. Skips without the model.
func TestPipelineMatchesSerial(t *testing.T) {
	e := New()
	if !e.Enabled() {
		t.Skip("BGE-M3 model/runtime absent — set up via `make model`")
	}
	ctx := context.Background()

	// > batchSize so the input spans multiple batches and the pipeline engages.
	// Short, varied texts keep CPU inference quick.
	var texts []string
	for i := 0; i < batchSize+8; i++ {
		texts = append(texts, fmt.Sprintf("clause %d about AMF SMF UPF interface N%d", i, i%9))
	}

	t.Setenv("EMBED_PIPELINE", "1")
	piped, err := e1(t).Embed(ctx, texts)
	if err != nil {
		t.Fatalf("pipelined embed: %v", err)
	}
	t.Setenv("EMBED_PIPELINE", "0")
	serial, err := e1(t).Embed(ctx, texts)
	if err != nil {
		t.Fatalf("serial embed: %v", err)
	}

	if len(piped) != len(serial) || len(piped) != len(texts) {
		t.Fatalf("length drift: piped=%d serial=%d want %d", len(piped), len(serial), len(texts))
	}
	for i := range piped {
		if len(piped[i]) != Dim {
			t.Fatalf("text %d dim=%d want %d", i, len(piped[i]), Dim)
		}
		for k := range piped[i] {
			if piped[i][k] != serial[i][k] {
				t.Fatalf("text %d vector differs at %d: pipeline=%v serial=%v (must be identical)",
					i, k, piped[i][k], serial[i][k])
			}
		}
	}
}

// e1 builds a fresh embedder so the EMBED_PIPELINE env flip is read at Embed time
// (it is read per call via pipelineOn, so one embedder would also do — but a fresh
// one keeps the two runs fully independent).
func e1(t *testing.T) Embedder {
	t.Helper()
	e := New()
	if !e.Enabled() {
		t.Skip("model absent")
	}
	return e
}
