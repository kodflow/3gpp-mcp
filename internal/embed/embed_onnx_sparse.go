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
	"os"
	"path/filepath"
	"strconv"

	ort "github.com/yalue/onnxruntime_go"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// maxSparseSeq bounds the per-run sequence length of the sparse pass. BGE-M3's
// self-attention is O(seq²): a single ~7.4k-token 3GPP clause needs a ~3.5 GB
// attention buffer (16 heads × 7376² × 4 B) and OOMs a 16 GB T4. We WINDOW longer
// clauses into ≤maxSparseSeq chunks and merge their term weights (max per token id),
// so no term is lost and memory stays ~16 MB/run. Override via SPARSE_MAX_SEQ.
func maxSparseSeq() int {
	if v := os.Getenv("SPARSE_MAX_SEQ"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 16 {
			return n
		}
	}
	return 512
}

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
// SparseModelID returns the active model's sparse identity (the registry-derived
// digest, "" when no sparse head) so a --sparse-only run stamps the DB with the
// exact sparse layer it produced.
func (*onnxEmbedder) SparseModelID() string { return SparseModelID() }

func (e *onnxEmbedder) EmbedSparse(_ context.Context, texts []string) ([]model.SparseVec, error) {
	e.initSparse()
	if e.sparseErr != nil {
		return nil, e.sparseErr
	}
	out := make([]model.SparseVec, len(texts))
	tok := e.toks[0]
	win := maxSparseSeq()
	for i, t := range texts {
		ids, err := e.encodeIDs(tok, t)
		if err != nil {
			return nil, err
		}
		if len(ids) == 0 {
			out[i] = model.SparseVec{}
			continue
		}
		// Window long clauses into ≤win-token chunks and merge term weights (max per
		// token id) so a single huge clause never builds an O(seq²) attention buffer.
		merged := model.SparseVec{}
		for start := 0; start < len(ids); start += win {
			end := start + win
			if end > len(ids) {
				end = len(ids)
			}
			sv, err := e.runSparseChunk(ids[start:end])
			if err != nil {
				return nil, err
			}
			for id, w := range sv {
				if w > merged[id] {
					merged[id] = w
				}
			}
		}
		out[i] = merged
	}
	return out, nil
}

// runSparseChunk runs the sparse session on ONE ≤win-token id window and returns the
// deduped term→weight map (specials + non-positive dropped via toSparse).
func (e *onnxEmbedder) runSparseChunk(ids []int) (model.SparseVec, error) {
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
	// Dynamic output: a nil Value tells the binding to allocate the [1, seq] tensor.
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
	sv := toSparse(idsU, wt.GetData(), nil)
	_ = wt.Destroy()
	return sv, nil
}
