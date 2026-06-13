//go:build onnx

package embed

// embed_onnx_sparse.go — the real BGE-M3 sparse (learned-lexical) reader, ISOLATED
// from the dense path. It lazily builds its OWN ORT session declaring the sparse
// output node (ModelSpec.SparseOutput, e.g. "sparse_weights"); the production dense
// `sessions` are never modified, so adding sparse cannot regress dense retrieval.
//
// Activation is gated on a model EXPORTED WITH the sparse head
// (scripts/export-bge-m3-sparse.py + a registry entry setting sparse_output). On a
// dense-only model SparseOutput is "" → EmbedSparse returns an error and the engine
// degrades (no sparse arm). Per-text batch=1 keeps the dynamic [1, seq] output and
// the toSparse dedup unambiguous; sparse runs offline (bake) or once per query.

import (
	"context"
	"fmt"
	"path/filepath"

	ort "github.com/yalue/onnxruntime_go"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// initSparse builds the isolated sparse session once. e.sparseErr stays non-nil
// when the active model has no sparse head (the common, dense-only case).
func (e *onnxEmbedder) initSparse() {
	e.sparseOnce.Do(func() {
		spec := ActiveModel()
		if spec.SparseOutput == "" {
			e.sparseErr = fmt.Errorf("model %q has no sparse head (set sparse_output in the registry for a sparse-exported model)", spec.Name)
			return
		}
		modelPath := filepath.Join(activeModelDir(spec), "model.onnx")
		opts, err := sessionOptionsFor(ExecutionProvider(), 0)
		if err != nil {
			e.sparseErr = err
			return
		}
		defer func() { _ = opts.Destroy() }()
		sess, err := ort.NewDynamicAdvancedSession(modelPath,
			[]string{spec.Inputs[0], spec.Inputs[1]}, []string{spec.SparseOutput}, opts)
		if err != nil {
			e.sparseErr = fmt.Errorf("sparse session (%s): %w", spec.SparseOutput, err)
			return
		}
		e.sparseSess = &gpuSession{session: sess}
	})
}

// EmbedSparse makes the onnx backend a SparseEmbedder: per text, tokenise to ids,
// run the sparse session for the [1, seq] ReLU weights, and dedup via toSparse
// (max per token id; specials + non-positive dropped) — identical post-processing
// to FlagEmbedding. Returns an error (engine degrades to dense+BM25) when the model
// has no sparse head.
func (e *onnxEmbedder) EmbedSparse(_ context.Context, texts []string) ([]model.SparseVec, error) {
	e.initSparse()
	if e.sparseErr != nil {
		return nil, e.sparseErr
	}
	out := make([]model.SparseVec, len(texts))
	tok := e.toks[0]
	for i, t := range texts {
		ids, err := e.encodeIDs(tok, t)
		if err != nil {
			return nil, err
		}
		if len(ids) == 0 {
			out[i] = model.SparseVec{}
			continue
		}
		ids64 := make([]int64, len(ids))
		mask := make([]int64, len(ids))
		idsU := make([]uint32, len(ids))
		for j, id := range ids {
			ids64[j], mask[j], idsU[j] = int64(id), 1, uint32(id) //nolint:gosec // vocab ids are small, non-negative
		}
		shape := ort.NewShape(1, int64(len(ids)))
		idsT, err := ort.NewTensor(shape, ids64)
		if err != nil {
			return nil, err
		}
		maskT, err := ort.NewTensor(shape, mask)
		if err != nil {
			_ = idsT.Destroy()
			return nil, err
		}
		// Dynamic output: a nil Value tells the binding to allocate the [1, seq]
		// result tensor; we read + destroy it per text.
		outVals := []ort.Value{nil}
		e.sparseSess.mu.Lock()
		err = e.sparseSess.session.Run([]ort.Value{idsT, maskT}, outVals)
		e.sparseSess.mu.Unlock()
		_ = idsT.Destroy()
		_ = maskT.Destroy()
		if err != nil {
			return nil, fmt.Errorf("sparse run: %w", err)
		}
		wt, ok := outVals[0].(*ort.Tensor[float32])
		if !ok {
			if outVals[0] != nil {
				_ = outVals[0].Destroy()
			}
			return nil, fmt.Errorf("sparse output is not float32")
		}
		out[i] = toSparse(idsU, wt.GetData(), nil)
		_ = wt.Destroy()
	}
	return out, nil
}
