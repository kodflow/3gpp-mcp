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
	"runtime"
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

// gpuSession is one ORT session pinned to one device (one CUDA GPU, or the single
// CPU session). Each carries its OWN mutex + reusable output buffer, so N of them on
// N devices run fully in parallel: the multi-GPU path (Kaggle's 2×T4) is just N
// gpuSessions, one per CUDA device, fed from a shared work queue. With one device it
// is byte-identical to the old single-session embedder.
type gpuSession struct {
	session *ort.DynamicAdvancedSession
	mu      sync.Mutex // ORT session Run is not guaranteed concurrent-safe
	device  int        // CUDA device id (0 for CPU / single-GPU)
	// outBuf is a reusable host backing store for the output tensor (cap
	// batchSize*Dim). ort.NewTensor ALIASES its Go slice (no copy — see the binding's
	// CreateOrtTensorWithShape), so the output tensor can share this buffer across
	// runs as long as every Run + copy-out happens under mu (serialised). This drops
	// one tensor alloc+free per batch. NOTE: the binding's ortMemoryInfo is CPU-pinned
	// (no device-tensor API), so true GPU-resident IO-binding / cudaGraph capture is
	// unreachable here — buffer reuse is the achievable part.
	outBuf []float32
}

type onnxEmbedder struct {
	// toks is the tokeniser POOL — one instance per producer goroutine, so batches
	// tokenise in parallel (the CPU bottleneck) without sharing a non-concurrency-safe
	// tokenizer. len>=1.
	toks []*tokenizer.Tokenizer
	// sessions is one gpuSession per device: len==1 on CPU / a single GPU, len==N
	// across N CUDA devices (auto-detected). Embed shards its batches across them.
	sessions  []*gpuSession
	windowing string // "" = truncate at maxTokens (default); "mean_pool" = window long clauses + mean-pool (EMBED_WINDOWING)
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
	// Tokenisation is the embed bottleneck (sugarme/tokenizer is pure-Go ~18 clauses/s
	// single-thread, vs the GPU doing far more), so load a POOL of tokenizer instances
	// — one per producer goroutine — to tokenise batches in parallel and keep the GPU
	// fed. The sugarme tokenizer is not concurrency-safe, hence one instance per
	// goroutine rather than one shared. Count = EMBED_TOKENIZERS or NumCPU (capped).
	nTok := tokenizerCount()
	toks := make([]*tokenizer.Tokenizer, 0, nTok)
	for i := 0; i < nTok; i++ {
		t, err := pretrained.FromFile(tokPath)
		if err != nil {
			if i == 0 {
				return Disabled{}
			}
			break // one tokenizer is enough to run (degrade to fewer producers)
		}
		toks = append(toks, t)
	}
	if len(toks) == 0 {
		return Disabled{}
	}
	if err := onnxrt.Init(); err != nil {
		return Disabled{}
	}
	// Build one ORT session per device. On the CUDA path we auto-detect the GPU
	// count (Kaggle's "T4" accelerator is 2×T4) and pin one session to each device,
	// so both GPUs run concurrently instead of device 0 doing everything while
	// device 1 sits idle. CPU / single-GPU → exactly one session (unchanged).
	ep := ExecutionProvider()
	devices := deviceIDs(ep)
	var sessions []*gpuSession
	for _, dev := range devices {
		gs, err := newGPUSession(modelPath, spec, ep, dev)
		if err != nil {
			// Device 0 must work — its failure is the original "disable vectors" path.
			// A SECONDARY device failing (e.g. the box really has fewer GPUs than
			// nvidia-smi reported) is non-fatal: keep the sessions already built and
			// run on those (degrade to fewer GPUs, never block).
			if dev == devices[0] {
				log.Printf("embed: model %q session load failed on device %d (%v) — check inputs/output in the registry match the export", spec.Name, dev, err)
				for _, s := range sessions {
					_ = s.session.Destroy()
				}
				return Disabled{}
			}
			log.Printf("embed: secondary GPU device %d unavailable (%v) — continuing on %d device(s)", dev, err, len(sessions))
			break
		}
		sessions = append(sessions, gs)
	}
	if len(sessions) == 0 {
		return Disabled{}
	}
	log.Printf("embed: ready with %d device session(s), %d tokeniser(s) (ep=%s)", len(sessions), len(toks), ep)
	return &onnxEmbedder{
		toks:      toks,
		sessions:  sessions,
		windowing: envOr("EMBED_WINDOWING", ""),
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
// With more than one batch and the pipeline enabled, a POOL of tokeniser goroutines
// (one per tokeniser instance) packs batches in parallel — tokenisation is the CPU
// bottleneck — feeding a channel drained by one runner goroutine PER DEVICE. Each
// producer owns its tokeniser (never shared), each batch writes a disjoint out[]
// range, so completion order is irrelevant and the result is identical to the serial
// path. With one tokeniser + one device this is the previous single-consumer pipeline.
func (e *onnxEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if e.windowing == "mean_pool" {
		return e.embedWindowed(texts)
	}
	out := make([][]float32, len(texts))
	if len(texts) == 0 {
		return out, nil
	}
	// One batch (or pipeline disabled): nothing to overlap — run serially on dev 0.
	if !pipelineOn() || len(texts) <= batchSize {
		for start := 0; start < len(texts); start += batchSize {
			end := min(start+batchSize, len(texts))
			if err := e.embedBatch(texts[start:end], out[start:end]); err != nil {
				return nil, err
			}
		}
		return out, nil
	}

	// PRODUCERS: tokenisation is the bottleneck (pure-Go ~18 clauses/s/thread), so run
	// one producer goroutine per tokeniser, each packing a ROUND-ROBIN slice of the
	// batches in parallel — N producers ≈ N× tokenise throughput, enough to keep the
	// GPU fed. Each owns its tokeniser (sugarme is not concurrency-safe). Batches are
	// independent and write disjoint out[] ranges, so completion order is irrelevant
	// and the result is identical to the serial path. prepCh closes once all are done.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type batchRange struct{ start, end int }
	var batches []batchRange
	for start := 0; start < len(texts); start += batchSize {
		batches = append(batches, batchRange{start, min(start+batchSize, len(texts))})
	}
	prepCh := make(chan preparedBatch, len(e.toks)+len(e.sessions))
	var prodWg sync.WaitGroup
	var tokErr error
	var tokMu sync.Mutex
	for ti := range e.toks {
		prodWg.Add(1)
		go func(ti int) {
			defer prodWg.Done()
			defer func() {
				if r := recover(); r != nil {
					tokMu.Lock()
					if tokErr == nil {
						tokErr = fmt.Errorf("tokenizer goroutine panicked: %v", r)
					}
					tokMu.Unlock()
					cancel()
				}
			}()
			for bi := ti; bi < len(batches); bi += len(e.toks) {
				br := batches[bi]
				pb := e.prepareBatch(e.toks[ti], texts[br.start:br.end], br.start)
				select {
				case prepCh <- pb:
				case <-ctx.Done():
					return
				}
			}
		}(ti)
	}
	go func() { prodWg.Wait(); close(prepCh) }()

	// One runner per device: each pulls prepared batches and runs them on its own
	// session/GPU, writing vectors to their output positions. The first run error
	// cancels the producer + sibling runners.
	var wg sync.WaitGroup
	var errMu sync.Mutex
	var runErr error
	for _, s := range e.sessions {
		wg.Add(1)
		go func(s *gpuSession) {
			defer wg.Done()
			for pb := range prepCh {
				if err := s.runPrepared(pb, out); err != nil {
					errMu.Lock()
					if runErr == nil {
						runErr = err
					}
					errMu.Unlock()
					cancel()
					return
				}
			}
		}(s)
	}
	wg.Wait()
	if runErr != nil {
		return nil, runErr
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
func (e *onnxEmbedder) encodeIDs(tok *tokenizer.Tokenizer, text string) ([]int, error) {
	enc, err := tok.EncodeSingle(text, true)
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
func (e *onnxEmbedder) tokenizeSafe(tok *tokenizer.Tokenizer, text string) []int {
	enc := func(s string) ([]int, error) { return e.encodeIDs(tok, s) }
	ids, err := safeEncode(enc, text)
	if err == nil && len(ids) > 0 {
		return ids
	}
	if err != nil {
		log.Printf("embed: tokenize failed (len=%d, hash=%s): %v — fallback to single space",
			len(text), snippetHash(text), err)
	}
	ids, err = safeEncode(enc, " ")
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
func (e *onnxEmbedder) prepareBatch(tok *tokenizer.Tokenizer, texts []string, start int) preparedBatch {
	b := len(texts)
	rows := make([][]int64, b)
	maxLen := 1
	for i, t := range texts {
		ids := e.tokenizeSafe(tok, t)
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

// runPrepared is the GPU half: build the tensors, run BGE-M3 under s.mu (ORT
// session.Run is not guaranteed concurrent-safe), L2-normalise, and write the
// vectors to out[pb.start : pb.start+pb.n]. Padding is attention-masked, so a
// padded row yields the same vector as if embedded alone (the A2 invariant).
// It is a gpuSession method so each device runs concurrently under its OWN mutex
// and output buffer — the only cross-device sharing is the disjoint out[] writes.
func (s *gpuSession) runPrepared(pb preparedBatch, out [][]float32) error {
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
	// The output tensor aliases the reusable s.outBuf (cap batchSize*Dim). Because
	// the tensor shares that buffer across calls, the lock must span NewTensor + Run
	// + the copy-out, so concurrent Embed callers never read each other's results.
	s.mu.Lock()
	defer s.mu.Unlock()
	outT, err := ort.NewTensor(ort.NewShape(int64(pb.n), int64(Dim)), s.outBuf[:pb.n*Dim])
	if err != nil {
		return err
	}
	defer func() { _ = outT.Destroy() }()
	if err := s.session.Run([]ort.Value{idsT, maskT}, []ort.Value{outT}); err != nil {
		return fmt.Errorf("onnx run: %w", err)
	}
	data := outT.GetData() // flat [n * Dim], aliasing s.outBuf
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
	return e.sessions[0].runPrepared(e.prepareBatch(e.toks[0], texts, 0), dst)
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
//   - CUDA: appends the CUDA EP pinned to `device` after tuning it for steady-state
//     batch embedding (heuristic conv search, same-stream copies, stable arena;
//     gpu_mem_limit only when EMBED_GPU_MEM is set so small GPUs aren't starved).
//     `device` is the CUDA device id — one session is built per device so a 2-GPU
//     box runs both in parallel.
//
// Every ORT call that can fail on a CUDA-less / older runtime tears down and
// returns an error, so the embedder degrades to Disabled{} instead of crashing.
func sessionOptionsFor(ep string, device int) (*ort.SessionOptions, error) {
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
			"device_id":                 strconv.Itoa(device), // pin this session to one GPU (multi-GPU: one session per device)
			"cudnn_conv_algo_search":    "HEURISTIC",          // skip slow per-shape conv autotune across length buckets
			"do_copy_in_default_stream": "1",                  // serialise H2D/D2H on the compute stream
			"arena_extend_strategy":     "kSameAsRequested",   // steady-state allocations, less fragmentation
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
		log.Printf("embed: ONNX execution provider = cuda (device %d)", device)
	}
	return opts, nil
}

// deviceIDs returns the CUDA device ids to build a session on, AUTO-DETECTED:
//   - non-CUDA ep → [0] (the single CPU session; device id is ignored there).
//   - CUDA → one id per detected GPU (Kaggle's "T4" accelerator is 2×T4 → [0,1]).
//
// EMBED_GPUS overrides the count (e.g. "1" to force single-GPU, useful for an
// A/B throughput comparison or to dodge a flaky second device). Detection reads
// `nvidia-smi -L` (one line per GPU); on any failure it falls back to a single
// device — degrade, never block.
func deviceIDs(ep string) []int {
	if ep != EPCUDA {
		return []int{0}
	}
	n := gpuCount()
	ids := make([]int, n)
	for i := range ids {
		ids[i] = i
	}
	return ids
}

// gpuCount is how many ORT CUDA sessions to run. DEFAULT 1 — the proven single-GPU
// path (s33 embedded fully this way). In-process multi-GPU is DISABLED by default:
// running >1 ORT CUDA session concurrently in one process throws "CUDA failure 400:
// invalid resource handle" at cudaEventRecord (verified on Kaggle 2×T4), because the
// onnxruntime_go binding exposes no cudaSetDevice, so the two devices' CUDA streams/
// events collide on whichever OS thread the goroutine lands on. EMBED_GPUS>1 force-
// enables the multi-session path for experimentation only; the real 2-GPU speedup is
// a separate 2-PROCESS design (one process per GPU via CUDA_VISIBLE_DEVICES — each is
// then the single-device path that works — sharding the work-list and merging).
func gpuCount() int {
	if v := os.Getenv("EMBED_GPUS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 {
			return min(n, 8)
		}
	}
	return 1
}

// tokenizerCount is the size of the tokeniser pool = number of parallel producer
// goroutines. Tokenisation (pure-Go sugarme, ~18 clauses/s single-thread) is the
// embed bottleneck, so this is the PRIMARY throughput lever: N tokenisers ≈ N×
// throughput until the GPU saturates. Default = runtime.NumCPU(), capped to [1,16];
// EMBED_TOKENIZERS overrides (e.g. to leave a core free, or to A/B).
func tokenizerCount() int {
	if v := os.Getenv("EMBED_TOKENIZERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 {
			return min(n, 16)
		}
	}
	if n := runtime.NumCPU(); n >= 1 {
		return min(n, 16)
	}
	return 1
}

// newGPUSession builds one ORT session for the given device. Each session owns its
// SessionOptions lifetime (created, used, Destroyed here) and its own reusable
// output buffer so N sessions run concurrently with zero shared mutable state.
func newGPUSession(modelPath string, spec ModelSpec, ep string, device int) (*gpuSession, error) {
	opts, err := sessionOptionsFor(ep, device)
	if err != nil {
		return nil, err
	}
	if opts != nil {
		defer func() { _ = opts.Destroy() }()
	}
	// I/O node names come from the model spec, so a different export (e.g. an fp16
	// one whose nodes are named differently) is wired by config, not code. A wrong
	// name simply fails NewDynamicAdvancedSession → caller degrades.
	sess, err := ort.NewDynamicAdvancedSession(modelPath,
		[]string{spec.Inputs[0], spec.Inputs[1]}, []string{spec.Output}, opts)
	if err != nil {
		return nil, err
	}
	return &gpuSession{session: sess, device: device, outBuf: make([]float32, batchSize*Dim)}, nil
}
