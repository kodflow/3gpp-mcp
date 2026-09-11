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
	"strings"
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
	// SAY WHICH FILE IS MISSING. All three absences used to collapse into one
	// silent Disabled{}, so "reranker": false on a box whose model is on disk and
	// whose embedder shares the same runtime told the operator nothing.
	for _, m := range []struct{ what, path string }{
		{"the ONNX runtime library", onnxrt.LibPath()},
		{"model.onnx", modelPath},
		{"tokenizer.json", tokPath},
	} {
		if !fileExists(m.path) {
			reason = m.what + " is missing at " + m.path + " (BGE_RERANKER_DIR=" + dir + ")"
			return Disabled{}
		}
	}
	tok, err := pretrained.FromFile(tokPath)
	if err != nil {
		reason = "tokenizer.json would not load: " + err.Error()
		return Disabled{}
	}
	if err := onnxrt.Init(); err != nil {
		reason = "the ONNX runtime would not initialise: " + err.Error()
		return Disabled{}
	}
	// Override ORT's default ENABLE_ALL with ENABLE_BASIC: the extended
	// SimplifiedLayerNormFusion throws on these transformer exports under
	// onnxruntime 1.26 — the exact crash that disabled the embedder
	// (internal/embed/embed_onnx.go). BASIC keeps the safe passes and skips the
	// extended fusion, so the reranker session loads instead of degrading to off.
	opts, err := ort.NewSessionOptions()
	if err != nil {
		reason = "ORT session options: " + err.Error()
		return Disabled{}
	}
	defer func() { _ = opts.Destroy() }()
	if err := opts.SetGraphOptimizationLevel(ort.GraphOptimizationLevelEnableBasic); err != nil {
		reason = "ORT optimisation level: " + err.Error()
		return Disabled{}
	}
	sess, err := ort.NewDynamicAdvancedSession(modelPath,
		[]string{"input_ids", "attention_mask"}, []string{"logits"}, opts)
	if err != nil {
		reason = "the cross-encoder session would not build: " + err.Error()
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
	// Tokenised side by side: the tokenizer is pure Go and its only shared state,
	// the Unigram cache, is a synchronised go-cache — and it is by far the slower
	// half of a pair on a long passage (see windowIDs).
	errs := make([]error, b)
	var wg sync.WaitGroup
	for i, p := range passages {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ids, err := windowIDs(r.tok, query, p)
			if err != nil {
				errs[i] = fmt.Errorf("tokenize pair %d: %w", i, err)
				return
			}
			row := make([]int64, len(ids))
			for j, id := range ids {
				row[j] = int64(id)
			}
			rows[i] = row
		}()
	}
	wg.Wait()
	for i := range rows {
		if errs[i] != nil {
			return errs[i]
		}
		if len(rows[i]) > maxLen {
			maxLen = len(rows[i])
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

// windowIDs is the token ids the cross-encoder sees for one pair: the first
// rrMaxTokens of encodePair(query, passage) — computed WITHOUT tokenizing the part
// of the passage that falls past them.
//
// WHY. The window is 512 tokens and a clause is often thousands of words, and
// github.com/sugarme/tokenizer is superlinear in its input: one pair costs 0.26 s
// at 2.5 KB, 0.67 s at 5 KB, 1.95 s at 10 KB, and twelve 3 000-word passages took
// 76 s to tokenize before the model ran at all (measured 2026-09-11, this
// machine). All of it was then cut to 512 ids and thrown away.
//
// WHY THE IDS DO NOT CHANGE. The passage is cut at a word end (windowPrefix: an
// ASCII non-space byte followed by an ASCII space), and bge-reranker-v2-m3's
// tokenizer.json is Precompiled + Strip(right) + Replace(" {2,}") normalisation,
// a Metaspace pre-tokenizer that splits on those spaces, and a Unigram model that
// tokenizes each word on its own — every step local to a word, none reaching
// across a space into the next. So the prefix encodes to a prefix of the whole
// passage's ids, and once it encodes to MORE than the window, the first
// rrMaxTokens ids are the ones the whole passage would have given. When the cut
// yields too few ids the prefix doubles, up to the whole passage: the answer is
// the old one either way, only the work differs. TestTheWindowIsTheWholePassages
// holds it on the passages that exercise each rule; the PR that introduced it
// checked it on real corpus passages.
//
// Two inputs the argument does not cover, because the LIBRARY is not local there:
//
//   - the text of a special token ("<s>", "<mask>", …). The library extracts them
//     with one regex per token and then sorts the matches by token id rather than
//     by position (added-vocabulary.go, findMatches), so which occurrences it
//     keeps depends on the whole string: a passage mixing "<mask>" and "<s>"
//     tokenized differently from its own prefix at id 396 (window_onnx_test.go).
//     Such a passage is tokenized whole, as before.
//   - one the library PANICS on: the fallback folds whitespace (forTokenizer), and
//     a panic caused by a shape past the cut would have folded the whole passage
//     where the prefix is encoded as is. The shape measured to cause it at the END
//     of a passage — trailing whitespace, "Foreword\n" — is carried over: such a
//     passage's prefix is folded too.
func windowIDs(tok *tokenizer.Tokenizer, query, passage string) ([]int, error) {
	whole := false
	for _, s := range tok.GetSpecialTokens() {
		if strings.Contains(passage, s) {
			whole = true
			break
		}
	}
	for n := rrPrefixBytes; ; n *= 2 {
		prefix, cut := windowPrefix(passage, n)
		if !cut || whole {
			enc, err := encodePair(tok, query, passage)
			if err != nil {
				return nil, err
			}
			return clipIDs(enc.Ids), nil
		}
		if endsInSpace(passage) {
			prefix = forTokenizer(prefix)
		}
		enc, err := encodePair(tok, query, prefix)
		if err != nil {
			return nil, err
		}
		// MORE than the window, and by two: the last id of a pair is its closing
		// </s>, which the whole passage would have put further out.
		if len(enc.Ids) > rrMaxTokens+1 {
			return clipIDs(enc.Ids), nil
		}
	}
}

func clipIDs(ids []int) []int {
	if len(ids) > rrMaxTokens {
		return ids[:rrMaxTokens]
	}
	return ids
}

// encodePair tokenizes one (query, passage) pair, and survives the tokenizer's
// panics.
//
// The pair is encoded AS IS first, so every input the library can encode gets
// exactly the tokens it always got — the ranking the served baseline records does
// not move. Only when that panics (see forTokenizer for the shapes and the
// measurement) is it retried with its whitespace folded; and a panic on the folded
// pair too becomes an error, never an unwinding: a panic here reaches mcp-go's
// stdio worker, which recovers it and sends NO response, so the client hangs,
// whereas an error makes Engine.rerank keep the fused order and the call answers.
func encodePair(tok *tokenizer.Tokenizer, query, passage string) (*tokenizer.Encoding, error) {
	if enc, panicked, err := tryEncodePair(tok, query, passage); !panicked {
		return enc, err
	}
	// The passage first, alone: it is what the engine builds with a trailing
	// "\n", and folding the QUERY too would change the query's tokens for every
	// candidate of this call that reaches here (review of #340).
	if enc, panicked, err := tryEncodePair(tok, query, forTokenizer(passage)); !panicked {
		return enc, err
	}
	enc, panicked, err := tryEncodePair(tok, forTokenizer(query), forTokenizer(passage))
	if panicked {
		return nil, fmt.Errorf("the tokenizer panicked on a %d-byte passage, folded or not: %w", len(passage), err)
	}
	return enc, err
}

func tryEncodePair(tok *tokenizer.Tokenizer, query, passage string) (enc *tokenizer.Encoding, panicked bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			enc, panicked, err = nil, true, fmt.Errorf("%v", r)
		}
	}()
	enc, err = tok.EncodePair(query, passage, true)
	return enc, false, err
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
