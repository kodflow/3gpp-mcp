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
	"log"
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
	tok       *tokenizer.Tokenizer
	session   *ort.DynamicAdvancedSession
	mu        sync.Mutex // ORT session Run is not guaranteed concurrent-safe
	windowing string     // "" = truncate at maxTokens (default); "mean_pool" = window long clauses + mean-pool (EMBED_WINDOWING)
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
	return &onnxEmbedder{tok: tok, session: sess, windowing: envOr("EMBED_WINDOWING", "")}
}

func (*onnxEmbedder) Enabled() bool   { return true }
func (*onnxEmbedder) Dim() int        { return Dim }
func (*onnxEmbedder) ModelID() string { return "bge-m3" }

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
	if e.windowing == "mean_pool" {
		return e.embedWindowed(texts)
	}
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

// embedWindowed splits each text into ≤defaultWindowWords word-windows, embeds
// every window (batched), and mean-pools each text's windows into one vector —
// so a long clause (tables, ASN.1) contributes all its content instead of being
// silently truncated at maxTokens. Enabled by EMBED_WINDOWING=mean_pool.
func (e *onnxEmbedder) embedWindowed(texts []string) ([][]float32, error) {
	var flat []string
	owner := make([][]int, len(texts))
	for i, t := range texts {
		for _, w := range windowText(t, defaultWindowWords) {
			owner[i] = append(owner[i], len(flat))
			flat = append(flat, w)
		}
	}
	vecs := make([][]float32, len(flat))
	for start := 0; start < len(flat); start += batchSize {
		end := start + batchSize
		if end > len(flat) {
			end = len(flat)
		}
		if err := e.embedBatch(flat[start:end], vecs[start:end]); err != nil {
			return nil, err
		}
	}
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = meanPoolL2(vecs, owner[i])
	}
	return out, nil
}

// XLM-RoBERTa (BGE-M3) special tokens — used as a last-resort floor when even
// the space fallback fails. [CLS]=0, [SEP]=2 produce a valid 2-token sequence
// with a non-empty attention mask, so the ONNX session always sees a tensor it
// can run instead of an all-padded row with an all-zero mask.
const (
	xlmrCLS = 0
	xlmrSEP = 2
)

// encodeIDs is the recoverable tokenize step (extracted so safeEncode in
// embed_safe.go can host the panic-recovery contract and test it without a
// real tokenizer).
func (e *onnxEmbedder) encodeIDs(text string) ([]int, error) {
	enc, err := e.tok.EncodeSingle(text, true)
	if err != nil {
		return nil, err
	}
	return enc.Ids, nil
}

// tokenizeSafe runs the BGE-M3 tokenizer with a three-tier degradation:
//  1. Normal call. On panic (sugarme/tokenizer v0.3.0 Metaspace off-by-one) or
//     error, fall back to ► 2.
//  2. EncodeSingle(" "). Single space tokenizes cleanly across XLM-RoBERTa
//     vocab. On panic/error, fall back to ► 3.
//  3. Hardcoded {CLS, SEP}. Guarantees a non-empty token sequence so embedBatch
//     never builds an all-padding row with an all-zero attention_mask.
//
// On any fallback we log {len, sha256-prefix} of the input — never the input
// itself, which can be a user search query (Qodo finding #3: privacy).
func (e *onnxEmbedder) tokenizeSafe(text string) []int {
	ids, err := safeEncode(e.encodeIDs, text)
	if err == nil && len(ids) > 0 {
		return ids
	}
	if err != nil {
		log.Printf("embed: tokenize failed (len=%d, hash=%s): %v — fallback to single space",
			len(text), snippetHash(text), err)
	}
	ids, err = safeEncode(e.encodeIDs, " ")
	if err == nil && len(ids) > 0 {
		return ids
	}
	log.Printf("embed: space-fallback also failed (orig len=%d, hash=%s): %v — using hardcoded CLS+SEP",
		len(text), snippetHash(text), err)
	return []int{xlmrCLS, xlmrSEP}
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
		ids := e.tokenizeSafe(t)
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
