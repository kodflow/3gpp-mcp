//go:build onnx

// This file is the real bge-reranker-v2-m3 cross-encoder, compiled only with
// `-tags onnx`. It is a sequence-classification head over XLM-RoBERTa: each
// (query, passage) pair is tokenised together, run through the encoder, and the
// single output logit is turned into a relevance score via sigmoid → [0,1].
//
// Model contract (verified from the ONNX graph, celinehoang/bge-reranker-v2-m3-onnx):
//
//	inputs : input_ids[int64], attention_mask[int64]  (shape [batch, seq])
//	output : logits[float32]                           (shape [batch, 1])
//
// Model + ORT come from scripts/fetch-model.sh (WITH_RERANKER=1). Absent → Disabled{}.
package rerank

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/sugarme/tokenizer"
	"github.com/sugarme/tokenizer/pretrained"
	ort "github.com/yalue/onnxruntime_go"

	"github.com/kodflow/3gpp-mcp/internal/onnxrt"
)

const (
	rrMaxTokens = 512 // query+passage truncated to this combined length
	rrPadID     = 1   // XLM-RoBERTa <pad>; masked out, so the value is irrelevant
)

// rrBatch bounds pairs per ONNX call. 16 (< the embedder's 32) because each
// reranker row is a query+passage pair, so sequences run longer. Override via
// RERANKER_BATCH.
var rrBatch = envInt("RERANKER_BATCH", 16)

type onnxReranker struct {
	tok     *tokenizer.Tokenizer
	session *ort.DynamicAdvancedSession
	mu      sync.Mutex // ORT Run is not guaranteed concurrent-safe
}

// newReranker (onnx build) returns the cross-encoder, or Disabled{} if the
// model/runtime is absent (degrade, never block).
func newReranker() Reranker {
	dir := envOr("BGE_RERANKER_DIR", "data/models/bge-reranker-v2-m3")
	modelPath := filepath.Join(dir, "model.onnx")
	tokPath := filepath.Join(dir, "tokenizer.json")
	if !fileExists(onnxrt.LibPath()) || !fileExists(modelPath) || !fileExists(tokPath) {
		return Disabled{}
	}
	tok, err := pretrained.FromFile(tokPath)
	if err != nil {
		return Disabled{}
	}
	if err := onnxrt.Init(); err != nil {
		return Disabled{}
	}
	// Override ORT's default ENABLE_ALL with ENABLE_BASIC: the extended
	// SimplifiedLayerNormFusion throws on these transformer exports under
	// onnxruntime 1.26 — the exact crash that disabled the embedder
	// (internal/embed/embed_onnx.go). BASIC keeps the safe passes and skips the
	// extended fusion, so the reranker session loads instead of degrading to off.
	opts, err := ort.NewSessionOptions()
	if err != nil {
		return Disabled{}
	}
	defer func() { _ = opts.Destroy() }()
	if err := opts.SetGraphOptimizationLevel(ort.GraphOptimizationLevelEnableBasic); err != nil {
		return Disabled{}
	}
	sess, err := ort.NewDynamicAdvancedSession(modelPath,
		[]string{"input_ids", "attention_mask"}, []string{"logits"}, opts)
	if err != nil {
		return Disabled{}
	}
	return &onnxReranker{tok: tok, session: sess}
}

func (*onnxReranker) Enabled() bool { return true }

// Score runs the cross-encoder on each (query, passage) pair in padded batches
// and returns sigmoid(logit) ∈ [0,1] — higher = more relevant.
func (r *onnxReranker) Score(ctx context.Context, query string, passages []string) ([]float64, error) {
	out := make([]float64, len(passages))
	for start := 0; start < len(passages); start += rrBatch {
		// Honour the per-request budget between batches: an in-flight ONNX Run is a
		// blocking CGO call we cannot preempt, but checking the deadline here bounds
		// the reranker to at most one extra batch past expiry. On cancellation we
		// return the error so Engine.rerank keeps the RRF order (degrade, never block).
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		end := start + rrBatch
		if end > len(passages) {
			end = len(passages)
		}
		if err := r.scoreBatch(query, passages[start:end], out[start:end]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (r *onnxReranker) scoreBatch(query string, passages []string, dst []float64) error {
	b := len(passages)
	rows := make([][]int64, b)
	maxLen := 1
	for i, p := range passages {
		enc, err := r.tok.EncodePair(query, p, true)
		if err != nil {
			return fmt.Errorf("tokenize pair: %w", err)
		}
		ids := enc.Ids
		if len(ids) > rrMaxTokens {
			ids = ids[:rrMaxTokens]
		}
		row := make([]int64, len(ids))
		for j, id := range ids {
			row[j] = int64(id)
		}
		rows[i] = row
		if len(row) > maxLen {
			maxLen = len(row)
		}
	}
	flatIDs := make([]int64, b*maxLen)
	flatMask := make([]int64, b*maxLen)
	for i, row := range rows {
		base := i * maxLen
		for j := 0; j < maxLen; j++ {
			if j < len(row) {
				flatIDs[base+j] = row[j]
				flatMask[base+j] = 1
			} else {
				flatIDs[base+j] = rrPadID
			}
		}
	}
	shape := ort.NewShape(int64(b), int64(maxLen))
	idsT, err := ort.NewTensor(shape, flatIDs)
	if err != nil {
		return err
	}
	defer func() { _ = idsT.Destroy() }()
	maskT, err := ort.NewTensor(shape, flatMask)
	if err != nil {
		return err
	}
	defer func() { _ = maskT.Destroy() }()
	outT, err := ort.NewEmptyTensor[float32](ort.NewShape(int64(b), 1))
	if err != nil {
		return err
	}
	defer func() { _ = outT.Destroy() }()

	r.mu.Lock()
	err = r.session.Run([]ort.Value{idsT, maskT}, []ort.Value{outT})
	r.mu.Unlock()
	if err != nil {
		return fmt.Errorf("onnx run: %w", err)
	}
	logits := outT.GetData() // flat [b]
	for i := 0; i < b; i++ {
		dst[i] = 1.0 / (1.0 + math.Exp(-float64(logits[i])))
	}
	return nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}
