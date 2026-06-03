//go:build onnx

// Throwaway diagnostic micro-benchmark (onnx tag). Isolates the BGE-M3 ONNX
// session.Run cost from tokenisation, and measures the dynamic-vs-fixed input
// shape penalty that the embed post-mortem suspects. Run (CPU):
//
//	CGO_ENABLED=1 \
//	ONNXRUNTIME_SHARED_LIBRARY_PATH=data/models/onnxruntime/lib/libonnxruntime.so \
//	LD_LIBRARY_PATH=data/models/onnxruntime/lib \
//	go test ./internal/embed -tags onnx -run TestInferProfile -v -timeout 30m
//
// It builds ONE session on the active EP (CPU unless ORT_EP=cuda) and times raw
// Run() for several (batch, seqLen) shapes — first warm (same shape repeated) to
// expose steady-state compute, then a "dynamic" sweep (a different seqLen every
// call) to expose ORT's per-shape replanning overhead. Pure measurement, no asserts.
package embed

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	ort "github.com/yalue/onnxruntime_go"

	"github.com/kodflow/3gpp-mcp/internal/onnxrt"
)

func TestInferProfile(t *testing.T) {
	if os.Getenv("EMBED_MICROBENCH") == "" {
		t.Skip("set EMBED_MICROBENCH=1 to run the inference profiler")
	}
	spec := ActiveModel()
	modelDir := activeModelDir(spec)
	modelPath := filepath.Join(modelDir, "model.onnx")
	if !fileExists(onnxrt.LibPath()) || !fileExists(modelPath) {
		t.Skipf("model/lib absent (model=%s lib=%s)", modelPath, onnxrt.LibPath())
	}
	if err := onnxrt.Init(); err != nil {
		t.Fatalf("ort init: %v", err)
	}
	ep := ExecutionProvider()
	opts, err := sessionOptionsFor(ep, 0)
	if err != nil {
		t.Fatalf("session opts: %v", err)
	}
	if opts != nil {
		defer func() { _ = opts.Destroy() }()
	}
	sess, err := ort.NewDynamicAdvancedSession(modelPath,
		[]string{spec.Inputs[0], spec.Inputs[1]}, []string{spec.Output}, opts)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	defer func() { _ = sess.Destroy() }()

	// run times one Run() for a (batch, seq) shape; ids/mask are all-real (mask=1)
	// so this is the worst-case compute (no padding shortcut).
	run := func(batch, seq int) time.Duration {
		n := batch * seq
		ids := make([]int64, n)
		mask := make([]int64, n)
		for i := range ids {
			ids[i] = 5 // arbitrary real token
			mask[i] = 1
		}
		shape := ort.NewShape(int64(batch), int64(seq))
		idsT, _ := ort.NewTensor(shape, ids)
		defer func() { _ = idsT.Destroy() }()
		maskT, _ := ort.NewTensor(shape, mask)
		defer func() { _ = maskT.Destroy() }()
		outBuf := make([]float32, batch*Dim)
		outT, _ := ort.NewTensor(ort.NewShape(int64(batch), int64(Dim)), outBuf)
		defer func() { _ = outT.Destroy() }()
		start := time.Now()
		if err := sess.Run([]ort.Value{idsT, maskT}, []ort.Value{outT}); err != nil {
			t.Fatalf("run b=%d s=%d: %v", batch, seq, err)
		}
		return time.Since(start)
	}

	// fmt.Printf (not t.Logf) so every line STREAMS immediately — go test buffers
	// t.Logf until the test returns, which hides progress on a multi-minute run.
	p := func(format string, a ...any) { fmt.Printf(format+"\n", a...); _ = os.Stdout.Sync() }
	p("PROFILE EP=%s  model=%s  dim=%d", ep, spec.Name, Dim)

	// Per-shape reference: one cold + one warm of the same shape exposes plan-build.
	for _, sh := range []struct{ b, s int }{{96, 128}, {96, 256}, {96, 512}} {
		cold := run(sh.b, sh.s)
		warm := run(sh.b, sh.s)
		p("REF    b=%-3d s=%-3d  cold=%7s warm=%7s  %9.0f tok/s (warm)",
			sh.b, sh.s, cold.Round(time.Millisecond), warm.Round(time.Millisecond),
			float64(sh.b*sh.s)/warm.Seconds())
	}

	// A/B at EQUAL total tokens (6 runs, batch 96, mean seq 256):
	//   FIXED  = same shape (96,256) every call  → 1 distinct shape, plan reused.
	//   DYN    = a different seq every call       → 6 distinct shapes, replan each.
	// If DYN total >> FIXED total, ORT pays a per-shape replanning tax — the exact
	// cost apply.go's ascending length-sort inflicts today, and what a fixed ladder
	// removes. (CPU EP understates the CUDA cuDNN/cuBLAS algo-search cost, so a gap
	// here is a LOWER bound on the GPU win.)
	dynSeqs := []int{136, 200, 256, 312, 376, 184} // mean ~244, all distinct
	var fixedT, dynT time.Duration
	for range dynSeqs {
		fixedT += run(96, 256)
	}
	for _, s := range dynSeqs {
		dynT += run(96, s)
	}
	p("AB FIXED(96x256 x%d) total=%s  | DYN(distinct x%d) total=%s  | DYN/FIXED=%.2fx",
		len(dynSeqs), fixedT.Round(time.Millisecond), len(dynSeqs), dynT.Round(time.Millisecond),
		float64(dynT)/float64(fixedT))
}
