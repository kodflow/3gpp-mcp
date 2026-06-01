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
	"strings"
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
	// outBuf is a reusable host backing store for the output tensor (cap
	// batchSize*Dim). ort.NewTensor ALIASES its Go slice (no copy — see the binding's
	// CreateOrtTensorWithShape), so the output tensor can share this buffer across
	// runs as long as every Run + copy-out happens under mu (serialised). This drops
	// one tensor alloc+free per batch. NOTE: the binding's ortMemoryInfo is CPU-pinned
	// (no device-tensor API), so true GPU-resident IO-binding / cudaGraph capture is
	// unreachable here — buffer reuse is the achievable part.
	outBuf []float32
}

// newEmbedder (onnx build) returns the BGE-M3 embedder, or Disabled{} if the
// runtime/model is not present (degrade, never block).
func newEmbedder() Embedder {
	// The active model (registry / EMBED_MODEL) declares the dir, I/O node names,
	// precision and dim. If its files are absent we DISABLE rather than silently fall
	// back to another model — running fp32 weights under an fp16 identity (or any
	// model under another's identity) would poison the serve-time coherence guard.
	spec := ActiveModel()
	if len(spec.Inputs) != 2 || spec.Output == "" {
		log.Printf("embed: model %q has invalid I/O wiring (inputs=%v output=%q) — disabling", spec.Name, spec.Inputs, spec.Output)
		return Disabled{}
	}
	// This build's tensor plumbing is fixed at Dim; a model declaring a different dim
	// would publish vectors whose identity (dim component) disagrees with the stored
	// FLOAT[Dim] column — refuse instead of corrupting the HNSW.
	if spec.Dim != Dim {
		log.Printf("embed: model %q dim=%d but this build is fixed at %d — disabling", spec.Name, spec.Dim, Dim)
		return Disabled{}
	}
	modelDir := activeModelDir(spec)
	modelPath := filepath.Join(modelDir, "model.onnx")
	// Tokenizer follows the SAME EMBED_MODEL_DIR override as the model (unless the
	// spec pins an explicit TokenizerDir) — see activeTokenizerDir. Using the bare
	// relative spec.Dir here was the bug that disabled the Kaggle GPU embedder:
	// model.onnx resolved via the override but tokenizer.json did not.
	tokPath := filepath.Join(activeTokenizerDir(spec), "tokenizer.json")
	if !fileExists(onnxrt.LibPath()) || !fileExists(modelPath) || !fileExists(tokPath) {
		// Name the EXACT missing artifact(s) and their resolved paths. The generic
		// "embedder disabled" cost a long investigation when only tokenizer.json was
		// misplaced; this turns the next occurrence into a one-line diagnosis.
		var missing []string
		if !fileExists(onnxrt.LibPath()) {
			missing = append(missing, "ort_lib="+onnxrt.LibPath())
		}
		if !fileExists(modelPath) {
			missing = append(missing, "model="+modelPath)
		}
		if !fileExists(tokPath) {
			missing = append(missing, "tokenizer="+tokPath)
		}
		fp16 := ""
		if spec.Precision == PrecisionFP16 {
			fp16 = " (refusing to run another model under an fp16 identity)"
		}
		log.Printf("embed: model %q disabled%s — missing %s", spec.Name, fp16, strings.Join(missing, " "))
		return Disabled{}
	}
	tok, err := pretrained.FromFile(tokPath)
	if err != nil {
		return Disabled{}
	}
	if err := onnxrt.Init(); err != nil {
		return Disabled{}
	}
	// Execution provider: CPU (default) needs no SessionOptions (nil). CUDA
	// builds a SessionOptions and appends the CUDA EP; this compiles on any host
	// but only succeeds at runtime on a GPU box with the CUDA-enabled ORT lib.
	// On any failure (no GPU, CPU-only lib, EP error) we degrade to Disabled{} —
	// the embedder seam never hard-fails (embed.go doctrine).
	opts, err := sessionOptionsFor(ExecutionProvider())
	if err != nil {
		log.Printf("embed: execution-provider %q unavailable (%v) — disabling vectors", ExecutionProvider(), err)
		return Disabled{}
	}
	if opts != nil {
		defer func() { _ = opts.Destroy() }()
	}
	// I/O node names come from the model spec, so a different export (e.g. an fp16
	// one whose nodes are named differently) is wired by config, not code. A wrong
	// name simply fails NewDynamicAdvancedSession → Disabled (degrade).
	sess, err := ort.NewDynamicAdvancedSession(modelPath,
		[]string{spec.Inputs[0], spec.Inputs[1]}, []string{spec.Output}, opts)
	if err != nil {
		log.Printf("embed: model %q session load failed (%v) — check inputs/output in the registry match the export", spec.Name, err)
		return Disabled{}
	}
	return &onnxEmbedder{
		tok:       tok,
		session:   sess,
		windowing: envOr("EMBED_WINDOWING", ""),
		outBuf:    make([]float32, batchSize*Dim),
	}
}

func (*onnxEmbedder) Enabled() bool { return true }
func (*onnxEmbedder) Dim() int      { return Dim }

// ModelID returns the canonical BGE-M3 EmbedIdentity (digest of model family +
// pinned weight revision + tokenizer revision + dim + normalisation + precision),
// NOT the bare family name. This is what is stamped into DB meta (embedding_model),
// folded into ClauseHash, and compared at serve time — so a weight/tokenizer/dim/
// precision change flips it and the re-embed + serve-compat gates fire instead of
// silently scoring a fresh query vector against corpus vectors from another model.
func (*onnxEmbedder) ModelID() string { return bgeModelID() }

// batchSize bounds how many clauses go into one ONNX call. 32 (CLAUDE.md §2)
// amortises the per-call overhead while keeping the padded tensor small.
// Overridable via BGE_BATCH for tuning.
var batchSize = envInt("BGE_BATCH", 32)

// padID is the XLM-RoBERTa padding token (<pad>). Padded positions carry
// attention_mask=0, so the export's mask-aware pooling ignores them — a padded
// batch yields the same vectors as one-at-a-time (asserted in the A2 test).
const padID int64 = 1

// pipelineOn reports whether the 2-stage tokenise/run pipeline is enabled
// (EMBED_PIPELINE != "0", default on). When off, Embed runs the legacy serial
// loop — byte-identical output, just no CPU/GPU overlap.
func pipelineOn() bool { return envOr("EMBED_PIPELINE", "1") != "0" }

// Embed tokenises and runs BGE-M3 in padded batches of batchSize, returning
// L2-normalised 1024-dim dense vectors (cosine/HNSW expect unit norm).
//
// With more than one batch and the pipeline enabled, a SINGLE tokeniser goroutine
// prepares batch N+1 (pure CPU) while the caller goroutine runs batch N on the GPU
// under e.mu — overlapping the otherwise-serial tokenise and inference. One
// tokeniser goroutine (never a pool) means e.tok is still touched serially within
// a call, so this introduces no new concurrency on the tokenizer; the output is
// byte-identical to the serial path (same tokens, padding, runs, by-index writes).
func (e *onnxEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if e.windowing == "mean_pool" {
		return e.embedWindowed(texts)
	}
	out := make([][]float32, len(texts))
	if len(texts) == 0 {
		return out, nil
	}
	// One batch (or pipeline disabled): nothing to overlap — run serially.
	if !pipelineOn() || len(texts) <= batchSize {
		for start := 0; start < len(texts); start += batchSize {
			end := min(start+batchSize, len(texts))
			if err := e.embedBatch(texts[start:end], out[start:end]); err != nil {
				return nil, err
			}
		}
		return out, nil
	}

	// Producer: tokenise+pack each batch in order on its own goroutine, feeding a
	// small buffered channel. A defer-recover converts any unexpected tokenizer
	// panic into an error (degrade-never-block) instead of crashing the process.
	prepCh := make(chan preparedBatch, 2)
	var tokErr error
	go func() {
		defer close(prepCh)
		defer func() {
			if r := recover(); r != nil {
				tokErr = fmt.Errorf("tokenizer goroutine panicked: %v", r)
			}
		}()
		for start := 0; start < len(texts); start += batchSize {
			end := min(start+batchSize, len(texts))
			pb := e.prepareBatch(texts[start:end], start)
			select {
			case prepCh <- pb:
			case <-ctx.Done():
				tokErr = ctx.Err()
				return
			}
		}
	}()

	// Consumer (this goroutine): run each prepared batch on the GPU and write its
	// vectors to their output positions. Serialised by e.mu inside runPrepared.
	for pb := range prepCh {
		if err := e.runPrepared(pb, out); err != nil {
			return nil, err
		}
	}
	return out, tokErr
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

// preparedBatch is a tokenised, padded batch ready for the GPU, plus where its
// vectors belong in the caller's output slice (out[start : start+n]).
type preparedBatch struct {
	start    int     // output offset
	n        int     // batch size (rows)
	maxLen   int     // padded sequence length
	flatIDs  []int64 // [n*maxLen] input_ids
	flatMask []int64 // [n*maxLen] attention_mask
}

// prepareBatch is the CPU half of an embed: tokenise + truncate + pad one batch
// (len <= batchSize) into flat input_ids / attention_mask tensors. No GPU, no
// shared mutable state beyond the tokenizer — so it can run on the producer
// goroutine while the GPU runs an earlier batch. start is the output offset.
func (e *onnxEmbedder) prepareBatch(texts []string, start int) preparedBatch {
	b := len(texts)
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
	flatIDs := make([]int64, b*maxLen)
	flatMask := make([]int64, b*maxLen)
	for i, row := range rows {
		base := i * maxLen
		for j := 0; j < maxLen; j++ {
			if j < len(row) {
				flatIDs[base+j] = row[j]
				flatMask[base+j] = 1
			} else {
				flatIDs[base+j] = padID // masked out by attention_mask
			}
		}
	}
	return preparedBatch{start: start, n: b, maxLen: maxLen, flatIDs: flatIDs, flatMask: flatMask}
}

// runPrepared is the GPU half: build the tensors, run BGE-M3 under e.mu (ORT
// session.Run is not guaranteed concurrent-safe), L2-normalise, and write the
// vectors to out[pb.start : pb.start+pb.n]. Padding is attention-masked, so a
// padded row yields the same vector as if embedded alone (the A2 invariant).
func (e *onnxEmbedder) runPrepared(pb preparedBatch, out [][]float32) error {
	shape := ort.NewShape(int64(pb.n), int64(pb.maxLen))
	idsT, err := ort.NewTensor(shape, pb.flatIDs)
	if err != nil {
		return err
	}
	defer func() { _ = idsT.Destroy() }()
	maskT, err := ort.NewTensor(shape, pb.flatMask)
	if err != nil {
		return err
	}
	defer func() { _ = maskT.Destroy() }()
	// The output tensor aliases the reusable e.outBuf (cap batchSize*Dim). Because
	// the tensor shares that buffer across calls, the lock must span NewTensor + Run
	// + the copy-out, so concurrent Embed callers never read each other's results.
	e.mu.Lock()
	defer e.mu.Unlock()
	outT, err := ort.NewTensor(ort.NewShape(int64(pb.n), int64(Dim)), e.outBuf[:pb.n*Dim])
	if err != nil {
		return err
	}
	defer func() { _ = outT.Destroy() }()
	if err := e.session.Run([]ort.Value{idsT, maskT}, []ort.Value{outT}); err != nil {
		return fmt.Errorf("onnx run: %w", err)
	}
	data := outT.GetData() // flat [n * Dim], aliasing e.outBuf
	for i := 0; i < pb.n; i++ {
		vec := append([]float32(nil), data[i*Dim:(i+1)*Dim]...)
		l2normalize(vec)
		out[pb.start+i] = vec
	}
	return nil
}

// embedBatch embeds one batch (len <= batchSize) into dst (same length) — the
// serial prepare+run used by the non-pipelined path and the windowed path.
func (e *onnxEmbedder) embedBatch(texts []string, dst [][]float32) error {
	return e.runPrepared(e.prepareBatch(texts, 0), dst)
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

// graphOptOn reports whether ORT graph optimisation + EP tuning is requested
// (EMBED_GRAPH_OPT set and != "0"). Default OFF so the CPU path stays (nil,nil) —
// byte-identical to today. Graph-opt is algebraic and bit-stable in principle, but
// gating it keeps the default reproducible and lets the operator opt in for a bulk
// GPU run (guarded by the A2/cosine invariants).
func graphOptOn() bool {
	v := os.Getenv("EMBED_GRAPH_OPT")
	return v != "" && v != "0"
}

// sessionOptionsFor builds the ORT SessionOptions for the requested execution
// provider. With neither CUDA nor EMBED_GRAPH_OPT it returns (nil, nil) — the ORT
// default, byte-identical to the historical CPU path. Otherwise it allocates a
// SessionOptions (caller owns Destroy()) and, best-effort:
//   - EMBED_GRAPH_OPT: ENABLE_ALL graph fusion + optional warm-start cache
//     (EMBED_GRAPH_CACHE) so the fused graph is reused across cold starts.
//   - CUDA: appends the CUDA EP (device 0) after tuning it for steady-state batch
//     embedding (heuristic conv search, same-stream copies, stable arena;
//     gpu_mem_limit only when EMBED_GPU_MEM is set so small GPUs aren't starved).
//
// Every ORT call that can fail on a CUDA-less / older runtime tears down and
// returns an error, so the embedder degrades to Disabled{} instead of crashing.
func sessionOptionsFor(ep string) (*ort.SessionOptions, error) {
	graphOpt := graphOptOn()
	if ep != EPCUDA && !graphOpt {
		return nil, nil
	}
	opts, err := ort.NewSessionOptions()
	if err != nil {
		return nil, err
	}
	if graphOpt {
		if err := opts.SetGraphOptimizationLevel(ort.GraphOptimizationLevelEnableAll); err != nil {
			_ = opts.Destroy()
			return nil, err
		}
		if cache := os.Getenv("EMBED_GRAPH_CACHE"); cache != "" {
			// Best-effort: a cache-path error must not sink the whole session.
			if err := opts.SetOptimizedModelFilePath(cache); err != nil {
				log.Printf("embed: EMBED_GRAPH_CACHE %q ignored: %v", cache, err)
			}
		}
		log.Printf("embed: ONNX graph optimisation = ENABLE_ALL")
	}
	if ep == EPCUDA {
		cuda, err := ort.NewCUDAProviderOptions()
		if err != nil {
			_ = opts.Destroy()
			return nil, err
		}
		defer func() { _ = cuda.Destroy() }()
		cudaOpts := map[string]string{
			"cudnn_conv_algo_search":    "HEURISTIC",        // skip slow per-shape conv autotune across length buckets
			"do_copy_in_default_stream": "1",                // serialise H2D/D2H on the compute stream
			"arena_extend_strategy":     "kSameAsRequested", // steady-state allocations, less fragmentation
		}
		if lim := os.Getenv("EMBED_GPU_MEM"); lim != "" {
			cudaOpts["gpu_mem_limit"] = lim
		}
		if err := cuda.Update(cudaOpts); err != nil {
			// Tuning is best-effort; a bad key shouldn't kill the GPU path.
			log.Printf("embed: CUDA EP option tuning skipped: %v", err)
		}
		if err := opts.AppendExecutionProviderCUDA(cuda); err != nil {
			_ = opts.Destroy()
			return nil, err
		}
		log.Printf("embed: ONNX execution provider = cuda (device 0)")
	}
	return opts, nil
}
