//go:build onnx

// This file is the real BGE-M3 dense embedder, compiled only with `-tags onnx`
// (CLAUDE.md §2: ONNX Runtime via github.com/yalue/onnxruntime_go, 1024-dim).
// It needs the ONNX Runtime shared library + the model, fetched once by
// scripts/fetch-model.sh into data/models/ (not committed). If either is
// absent it degrades to Disabled{} so even the onnx build never hard-fails.
//
// Model contract (BGE-M3 sentence-transformers ONNX export, verified at runtime):
//
//	inputs : input_ids[int64], attention_mask[int64]  (shape [batch, seq])
//	output : sentence_embedding[float32]               (shape [batch, 1024])
//
// The tokenizer is the model's own tokenizer.json (XLM-RoBERTa SentencePiece),
// loaded by the pure-Go github.com/sugarme/tokenizer — no extra native lib.
package embed

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

// maxTokens bounds the sequence length (BGE-M3 supports 8192; 512 keeps CPU
// inference fast and covers virtually every 3GPP clause).
const maxTokens = 512

type onnxEmbedder struct {
	tok     *tokenizer.Tokenizer
	session *ort.DynamicAdvancedSession
	mu      sync.Mutex // ORT session Run is not guaranteed concurrent-safe
}

// newEmbedder (onnx build) returns the BGE-M3 embedder, or Disabled{} if the
// runtime/model is not present (degrade, never block).
func newEmbedder() Embedder {
	modelDir := envOr("BGE_M3_DIR", "data/models/bge-m3")
	modelPath := filepath.Join(modelDir, "model.onnx")
	tokPath := filepath.Join(modelDir, "tokenizer.json")
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
	sess, err := ort.NewDynamicAdvancedSession(modelPath,
		[]string{"input_ids", "attention_mask"}, []string{"sentence_embedding"}, nil)
	if err != nil {
		return Disabled{}
	}
	return &onnxEmbedder{tok: tok, session: sess}
}

func (*onnxEmbedder) Enabled() bool { return true }
func (*onnxEmbedder) Dim() int      { return Dim }

// batchSize bounds how many clauses go into one ONNX call. 32 (CLAUDE.md §2)
// amortises the per-call overhead while keeping the padded tensor small.
// Overridable via BGE_BATCH for tuning.
var batchSize = envInt("BGE_BATCH", 32)

// padID is the XLM-RoBERTa padding token (<pad>). Padded positions carry
// attention_mask=0, so the export's mask-aware pooling ignores them — a padded
// batch yields the same vectors as one-at-a-time (asserted in the A2 test).
const padID int64 = 1

// Embed tokenises and runs BGE-M3 in padded batches of batchSize, returning
// L2-normalised 1024-dim dense vectors (cosine/HNSW expect unit norm).
func (e *onnxEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for start := 0; start < len(texts); start += batchSize {
		end := start + batchSize
		if end > len(texts) {
			end = len(texts)
		}
		if err := e.embedBatch(texts[start:end], out[start:end]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// embedBatch embeds one batch (len <= batchSize) into dst (same length). It
// pads every row to the batch's longest sequence and lets attention_mask zero
// the padding, so batching never changes a vector — only the throughput.
func (e *onnxEmbedder) embedBatch(texts []string, dst [][]float32) error {
	b := len(texts)
	// 1. tokenise + truncate; track the longest row for padding.
	rows := make([][]int64, b)
	maxLen := 1
	for i, t := range texts {
		enc, err := e.tok.EncodeSingle(t, true)
		if err != nil {
			return fmt.Errorf("tokenize: %w", err)
		}
		ids := enc.Ids
		if len(ids) > maxTokens {
			ids = ids[:maxTokens]
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
	// 2. pack into padded [b, maxLen] input_ids + attention_mask.
	flatIDs := make([]int64, b*maxLen)
	flatMask := make([]int64, b*maxLen)
	for i, row := range rows {
		base := i * maxLen
		for j := 0; j < maxLen; j++ {
			if j < len(row) {
				flatIDs[base+j] = row[j]
				flatMask[base+j] = 1
			} else {
				flatIDs[base+j] = padID // masked out below
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
	outT, err := ort.NewEmptyTensor[float32](ort.NewShape(int64(b), int64(Dim)))
	if err != nil {
		return err
	}
	defer func() { _ = outT.Destroy() }()

	e.mu.Lock()
	err = e.session.Run([]ort.Value{idsT, maskT}, []ort.Value{outT})
	e.mu.Unlock()
	if err != nil {
		return fmt.Errorf("onnx run: %w", err)
	}
	data := outT.GetData() // flat [b * Dim]
	for i := 0; i < b; i++ {
		vec := append([]float32(nil), data[i*Dim:(i+1)*Dim]...)
		l2normalize(vec)
		dst[i] = vec
	}
	return nil
}

func l2normalize(v []float32) {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return
	}
	inv := float32(1.0 / math.Sqrt(sum))
	for i := range v {
		v[i] *= inv
	}
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
