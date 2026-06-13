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
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	ort "github.com/yalue/onnxruntime_go"

	"github.com/kodflow/3gpp-mcp/internal/onnxrt"
)

// maxTokens bounds the sequence length (BGE-M3 supports 8192; 512 keeps CPU
// inference fast and covers virtually every 3GPP clause).
const maxTokens = 512

// gpuSession is one ORT session pinned to one device. Each carries its OWN mutex
// + a pool of output buffers. N sessions run fully in parallel: across N CUDA
// devices, OR several on ONE device (EMBED_SESSIONS_PER_GPU) so their Run() calls
// overlap on the GPU instead of one session idling the device between batches.
// With one session it is byte-identical to the old single-session embedder.
type gpuSession struct {
	session *ort.DynamicAdvancedSession
	mu      sync.Mutex // serialises THIS session's ort.Run; held ONLY around Run
	device  int        // CUDA device id (0 for CPU / single-GPU)
	// bufPool recycles host backing stores for the output tensor (each cap
	// batchSize*Dim). ort.NewTensor ALIASES its Go slice, so giving every run its
	// OWN pooled buffer (instead of one shared s.outBuf) lets the lock cover ONLY
	// session.Run — the copy-out + L2-normalise then run lock-free, and sibling
	// sessions on the same GPU overlap instead of serialising on a shared buffer.
	// Pointer-like pooling avoids a ~1 MB alloc per batch without re-aliasing.
	// NOTE: the binding's ortMemoryInfo is CPU-pinned (no device-tensor API), so
	// true GPU-resident IO-binding / cudaGraph capture is unreachable here.
	bufPool sync.Pool
}

// getOutBuf returns a pointer to a recycled output buffer of cap maxBatchRows*Dim
// (allocating on a cold pool). Recycle it with s.bufPool.Put. It is sized for the
// LARGEST possible batch (token-budget batching makes a short-clause batch hold many
// more than batchSize rows), so runPrepared's (*buf)[:pb.n*Dim] never overruns.
func (s *gpuSession) getOutBuf() *[]float32 {
	if v := s.bufPool.Get(); v != nil {
		return v.(*[]float32)
	}
	b := make([]float32, maxBatchRows()*Dim)
	return &b
}

type onnxEmbedder struct {
	// toks is the tokeniser POOL — one instance per producer goroutine, so batches
	// tokenise in parallel (the CPU bottleneck) without sharing a non-concurrency-safe
	// tokenizer. The concrete type is chosen by build tag (sugarme vs HF Rust); see
	// the clauseTokenizer seam. len>=1.
	toks []clauseTokenizer
	// sessions is one gpuSession per device: len==1 on CPU / a single GPU, len==N
	// across N CUDA devices (auto-detected). Embed shards its batches across them.
	sessions  []*gpuSession
	windowing string // "" = truncate at maxTokens (default); "mean_pool" = window long clauses + mean-pool (EMBED_WINDOWING)

	// Lazy, ISOLATED sparse session (embed_onnx_sparse.go). Built on first
	// EmbedSparse, separate from the dense `sessions` so the production dense path is
	// never touched. nil/err when the active model has no sparse head.
	sparseOnce sync.Once
	sparseSess *gpuSession
	sparseErr  error
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
	// Tokenisation is the embed bottleneck (the 2×T4 probe measured GPU util at 3-5%
	// — starved by the CPU), so load a POOL of tokenizer instances — one per producer
	// goroutine — to tokenise batches in parallel and keep the GPU fed. The concrete
	// tokenizer is chosen by build tag via the clauseTokenizer seam: pure-Go sugarme
	// by default, HF Rust (orders of magnitude faster) under -tags fasttok. Neither is
	// assumed concurrency-safe, hence one instance per goroutine. Count =
	// EMBED_TOKENIZERS or NumCPU (capped).
	nTok := tokenizerCount()
	toks, err := newClauseTokenizers(tokPath, nTok)
	if err != nil {
		log.Printf("embed: tokenizer load failed (%v) — disabling", err)
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
	batchSize = resolveBatchSize() // size the output buffers + Embed's batching for this device class
	devices := deviceIDs(ep)
	perGPU := sessionsPerGPU()
	var sessions []*gpuSession
	for _, dev := range devices {
		for k := 0; k < perGPU; k++ {
			gs, err := newGPUSession(modelPath, spec, ep, dev)
			if err != nil {
				// The FIRST session must work — its failure is the original "disable
				// vectors" path. Any later session failing (a flaky 2nd GPU, or VRAM
				// exhausted at the Kth concurrent session on one GPU) is non-fatal:
				// keep what we have and run on fewer (degrade, never block).
				if len(sessions) == 0 {
					log.Printf("embed: model %q session load failed on device %d (%v) — check inputs/output in the registry match the export", spec.Name, dev, err)
					return Disabled{}
				}
				log.Printf("embed: session %d on device %d unavailable (%v) — continuing on %d session(s)", k, dev, err, len(sessions))
				break
			}
			sessions = append(sessions, gs)
		}
	}
	if len(sessions) == 0 {
		return Disabled{}
	}
	log.Printf("embed: ready with %d session(s) across %d device(s), %d %s tokeniser(s), batch=%d (ep=%s)", len(sessions), len(devices), len(toks), tokenizerImpl(), batchSize, ep)
	e := &onnxEmbedder{
		toks:      toks,
		sessions:  sessions,
		windowing: envOr("EMBED_WINDOWING", ""),
	}
	e.warmup(ep)
	return e
}

// warmupOn reports whether shape warmup runs (CUDA only, EMBED_WARMUP == "1").
// Default OFF: the probe showed warmup raised steady GPU util a little (30→37%) but
// its init cost (dummy runs over every ladder rung) cut the AVERAGE throughput on a
// real-sized run, and steady-state did not beat the no-warmup path. Pointless on CPU
// (no per-shape cuDNN/cuBLAS plan to cache). Opt-in for a replanning-dominated regime
// (pair with EMBED_SHAPE_LADDER=1).
func warmupOn(ep string) bool { return ep == EPCUDA && os.Getenv("EMBED_WARMUP") == "1" }

// warmup runs ONE dummy batch per ladder rung on each session BEFORE real work, so
// the CUDA EP builds and caches its per-shape execution plan / cuDNN-cuBLAS algorithm
// choices up front. This matters now that the fast tokenizer makes the run GPU-bound:
// the first batch of each new shape otherwise pays a one-off replanning stall (the
// "cold window" the post-mortem measured at CPU speed). With the fixed ladder there
// are only len(shapeLadder) shapes, so a handful of throwaway runs warm them all.
// Best-effort: any failure is skipped (warmup never blocks readiness). It calls
// session.Run directly (not runPrepared) so it does not pollute the profiler.
func (e *onnxEmbedder) warmup(ep string) {
	if !warmupOn(ep) {
		return
	}
	for _, s := range e.sessions {
		for _, rung := range shapeLadder {
			// Match the row count token-budget batching will actually use for this
			// rung, so the warmed shape is the one that occurs.
			rows := tokenBudget() / rung
			if rows < 1 {
				rows = 1
			}
			if rows > maxBatchRows() {
				rows = maxBatchRows()
			}
			ids := make([]int64, rows*rung)
			mask := make([]int64, rows*rung)
			for i := 0; i < rows; i++ {
				base := i * rung
				ids[base], mask[base] = xlmrCLS, 1
				if rung > 1 {
					ids[base+1], mask[base+1] = xlmrSEP, 1
				}
				for j := 2; j < rung; j++ {
					ids[base+j] = padID
				}
			}
			e.warmupRun(s, rows, rung, ids, mask)
		}
	}
	log.Printf("embed: warmed %d shape(s) × %d session(s)", len(shapeLadder), len(e.sessions))
}

// warmupRun executes one throwaway batch and destroys its tensors. Isolated so the
// tensor lifetimes are scoped (defer) and any error is contained per shape.
func (e *onnxEmbedder) warmupRun(s *gpuSession, rows, seq int, ids, mask []int64) {
	shape := ort.NewShape(int64(rows), int64(seq))
	idsT, err := ort.NewTensor(shape, ids)
	if err != nil {
		return
	}
	defer func() { _ = idsT.Destroy() }()
	maskT, err := ort.NewTensor(shape, mask)
	if err != nil {
		return
	}
	defer func() { _ = maskT.Destroy() }()
	buf := make([]float32, rows*Dim)
	outT, err := ort.NewTensor(ort.NewShape(int64(rows), int64(Dim)), buf)
	if err != nil {
		return
	}
	defer func() { _ = outT.Destroy() }()
	s.mu.Lock()
	_ = s.session.Run([]ort.Value{idsT, maskT}, []ort.Value{outT})
	s.mu.Unlock()
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

// batchSize bounds how many clauses go into one ONNX call. The package default
// (32, the CPU value) is REPLACED at embedder init by resolveBatchSize(), which
// scales the batch to the device class (a T4 feeds on far bigger batches than a
// CPU). BGE_BATCH overrides everything for tuning. It also sizes each session's
// pooled output buffer (batchSize*Dim), so it must be set before sessions build.
var batchSize = envInt("BGE_BATCH", 32)

// resolveBatchSize picks the ONNX batch for the active device class. CPU stays
// small (32) — a big batch there just bloats latency. On CUDA the batch scales
// with VRAM (the dominant GPU-utilisation lever for a 560M model: more rows per
// Run = fewer kernel launches and higher occupancy), capped so the activations
// of a 512-token batch still fit. BGE_BATCH overrides everything.
func resolveBatchSize() int {
	if v := os.Getenv("BGE_BATCH"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	if ExecutionProvider() != EPCUDA {
		return 32
	}
	switch mb := gpuMemTotalMB(); {
	case mb >= 70000: // A100/H200 80 GB+
		return 384
	case mb >= 38000: // A100 40 GB
		return 256
	case mb >= 22000: // L4 24 GB
		return 128
	case mb >= 14000: // T4 16 GB
		return 96
	default: // CUDA, unknown or small VRAM — modest but still GPU-sized
		return 64
	}
}

// maxBatchRows bounds the row count of any one batch (and hence the pooled output
// buffer). Token-budget batching packs short clauses into batches far larger than
// batchSize; this cap keeps tensor allocation and the per-batch copy-out bounded
// while still giving the GPU ample parallelism (8× the full-length batch). It is the
// ceiling tokenBudgetBatches must not exceed.
func maxBatchRows() int { return 8 * batchSize }

// tokenBudgetBatchingOn reports whether variable-size, token-budget batches are
// used on the pipelined path (EMBED_TOKEN_BUDGET_BATCHING == "1"). Default OFF:
// once the HF Rust tokenizer made the run GPU-bound, the 2×T4 probe showed this
// REGRESSED throughput (~59→53 cl/s on series 21) — the byte→token estimate
// oversizes batches for short-clause series, and the GPU ceiling is memory-transfer
// (not occupancy) bound through this binding, so bigger batches don't help. Kept as
// an opt-in tunable (may help a long-clause corpus or a stronger GPU); the default
// fixed batchSize-row path is the validated best.
func tokenBudgetBatchingOn() bool { return os.Getenv("EMBED_TOKEN_BUDGET_BATCHING") == "1" }

// tokenBudget is the target padded-token count (rows × rung) per ONNX Run. Sizing
// batches to a constant TOKEN budget instead of a constant ROW count keeps the GPU's
// work-per-Run uniform across length buckets — the saturation lever for a corpus
// whose clauses span 10 to 512 tokens: short clauses pack into big batches (high
// occupancy, fewer kernel launches), long clauses fall back to small ones (bounded
// VRAM). Default = batchSize×maxTokens, i.e. the VRAM-sized full-length batch the
// device class already proved it can hold; EMBED_TOKEN_BUDGET overrides for tuning.
func tokenBudget() int {
	if v := os.Getenv("EMBED_TOKEN_BUDGET"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return batchSize * maxTokens
}

// bytesPerToken converts a clause's byte length to an APPROXIMATE token count, used
// only to SIZE its batch (never to pad it — prepareBatch pads to the real tokenised
// rung, so a wrong estimate costs a little occupancy, never correctness). 3GPP
// technical prose runs denser than plain English; ~3 bytes/token is conservative.
const bytesPerToken = 3

// estTokens approximates text's token count from its byte length, clamped to
// [1, maxTokens]. Cheap (no tokenise) so batch formation stays off the hot path.
func estTokens(text string) int {
	n := len(text) / bytesPerToken
	if n < 1 {
		return 1
	}
	if n > maxTokens {
		return maxTokens
	}
	return n
}

// tokenBudgetBatches splits a LENGTH-SORTED slice (apply.go sorts the window
// ascending by byte length) into variable-size [start,end) ranges, each carrying
// ~budget padded tokens. Because the input is sorted, a batch's rung (ladderCeil of
// its longest est-length) is non-decreasing as rows are added, so padding stays
// tight. Row count is capped at maxBatchRows so a batch of tiny clauses cannot
// allocate an unbounded tensor. Always emits at least one row per batch.
func tokenBudgetBatches(texts []string, budget int) [][2]int {
	var batches [][2]int
	cap := maxBatchRows()
	i, n := 0, len(texts)
	for i < n {
		start := i
		rung := ladderCeil(estTokens(texts[i]))
		i++ // the first row is always included
		for i < n {
			// Candidate batch rung if this row joins. Sorted ascending ⇒ usually
			// >= rung; the max() is defensive since a byte-sort isn't a perfect
			// token-sort (a longer-byte clause can tokenise shorter).
			r := max(rung, ladderCeil(estTokens(texts[i])))
			rows := i - start
			if rows >= cap || (rows+1)*r > budget {
				break
			}
			rung = r
			i++
		}
		batches = append(batches, [2]int{start, i})
	}
	return batches
}

// gpuMemTotalMB returns device-0 total VRAM in MiB via nvidia-smi, or 0 if the
// query fails (no driver, tool absent) — best-effort, never fatal.
func gpuMemTotalMB() int {
	out, err := exec.Command("nvidia-smi", "--query-gpu=memory.total", "--format=csv,noheader,nounits").Output()
	if err != nil {
		return 0
	}
	line := strings.TrimSpace(string(out))
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i] // first GPU only
	}
	n, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		return 0
	}
	return n
}

// sessionsPerGPU is how many independent ORT sessions to run PER device. Default
// 1 (behaviour unchanged). >1 runs K sessions on the SAME device so their Run()
// calls overlap on the GPU (copy/compute concurrency) instead of the device
// idling between one session's batches — a single-GPU saturation lever, distinct
// from the multi-DEVICE path. Same-device multi-session does NOT hit the
// cross-device cudaSetDevice bug (that is about >1 CUDA device in one process).
// Each session holds its own model copy, so more sessions cost more VRAM; a Kth
// session that fails to load degrades to fewer. Opt-in, capped [1,8];
// EMBED_SESSIONS_PER_GPU overrides.
func sessionsPerGPU() int {
	if v := os.Getenv("EMBED_SESSIONS_PER_GPU"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 {
			return min(n, 8)
		}
	}
	return 1
}

// padID is the XLM-RoBERTa padding token (<pad>). Padded positions carry
// attention_mask=0, so the export's mask-aware pooling ignores them — a padded
// batch yields the same vectors as one-at-a-time (asserted in the A2 test).
const padID int64 = 1

// shapeLadder is the fixed set of padded sequence lengths a batch may take. Every
// batch's padded length is rounded UP to the next rung, so the ONNX session ever
// sees only len(shapeLadder) distinct (batch, seq) shapes instead of one per
// distinct clause length. This is the dominant GPU-throughput lever: the CUDA EP
// re-plans memory and re-selects cuDNN/cuBLAS algorithms on every NEW input shape,
// and apply.go's ascending length-sort otherwise feeds a DIFFERENT length to almost
// every batch — maximising replanning (measured: the cold window ran at CPU speed,
// ~300 tok/s, while warm shapes ran ~5× faster). Snapping to a small ladder lets
// ORT cache and reuse those plans. It is IDENTITY-SAFE: the extra pad positions
// carry attention_mask=0 and the export's pooling is mask-aware, so a vector is
// byte-identical regardless of how far its row was padded (the A2 invariant). The
// ladder is denser below the corpus median (~150 tok) where most clauses live, so
// padding waste stays small. Capped at maxTokens.
var shapeLadder = []int{16, 32, 64, 96, 128, 192, 256, 384, maxTokens}

// ladderCeil rounds n UP to the next shapeLadder rung (never below n, never above
// maxTokens). A batch whose longest row is n is padded to ladderCeil(n).
func ladderCeil(n int) int {
	if n >= maxTokens {
		return maxTokens
	}
	for _, r := range shapeLadder {
		if r >= n {
			return r
		}
	}
	return maxTokens
}

// shapeLadderOn reports whether fixed-shape padding is enabled (EMBED_SHAPE_LADDER
// == "1"). Default OFF: the dynamic-shape replanning it targets turned out NOT to be
// the bottleneck (the 2×T4 probe was CPU-tokenise bound, then memory-transfer bound
// once the fast tokenizer landed — daulet "plain" with per-batch dynamic shapes was
// the fastest config). Kept as an opt-in tunable for a regime where per-shape
// replanning actually dominates.
func shapeLadderOn() bool { return os.Getenv("EMBED_SHAPE_LADDER") == "1" }

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
	if tokenBudgetBatchingOn() {
		// Variable-size batches at a constant token budget: short clauses pack into
		// big batches (occupancy), long ones into small (bounded VRAM). texts arrive
		// length-sorted (apply.go) so each batch's rung stays tight.
		for _, r := range tokenBudgetBatches(texts, tokenBudget()) {
			batches = append(batches, batchRange{r[0], r[1]})
		}
	} else {
		for start := 0; start < len(texts); start += batchSize {
			batches = append(batches, batchRange{start, min(start+batchSize, len(texts))})
		}
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

// XLM-RoBERTa (BGE-M3) special tokens. [CLS]=0, [SEP]=2 form a valid 2-token
// sequence with a non-empty attention mask — used as the row for a legitimately
// empty clause and as the discarded placeholder for a degraded (untokenisable)
// row, so the ONNX session never sees an all-padded row with an all-zero mask.
const (
	xlmrCLS = 0
	xlmrSEP = 2
)

// encodeIDs is the recoverable tokenize step (extracted so safeEncode in
// embed_safe.go can host the panic-recovery contract and test it without a
// real tokenizer).
func (e *onnxEmbedder) encodeIDs(tok clauseTokenizer, text string) ([]int, error) {
	return tok.EncodeIDs(text)
}

// tokenizeSafe tokenises one clause, returning (ids, degraded). It first
// SANITISES the text (sanitizeForTokenizer: strip Private-Use-Area glyphs +
// collapse whitespace runs), which removes the conversion artefacts that trip
// sugarme/tokenizer v0.3.0's Metaspace pretokenizer into a "slice bounds out of
// range [2n+1:2n]" panic AND keeps the model fed real text instead of layout
// noise.
//
//   - clean == "" (clause was pure layout/glyph noise): return {CLS,SEP},
//     degraded=false — a stable minimal vector for genuinely empty content, NOT
//     flagged for endless retry.
//   - tokenises cleanly: return its ids, degraded=false.
//   - STILL panics/errors after sanitising (a new/unknown trigger): return
//     (nil, true). The caller leaves the clause UNembedded (NULL) so it is
//     retried and counted by the completeness gate — never silently persisted
//     with a placeholder vector (the old single-space fallback corrupted the
//     corpus invisibly). Logged once with a stable hash, never the text itself
//     (it can be a user query — Qodo finding #3: privacy).
func (e *onnxEmbedder) tokenizeSafe(tok clauseTokenizer, text string) ([]int, bool) {
	clean := sanitizeForTokenizer(text)
	if clean == "" {
		return []int{xlmrCLS, xlmrSEP}, false
	}
	ids, err := safeEncode(func(s string) ([]int, error) { return e.encodeIDs(tok, s) }, clean)
	if err == nil && len(ids) > 0 {
		return ids, false
	}
	if err != nil {
		log.Printf("embed: tokenize failed after sanitise (len=%d, hash=%s): %v — clause left unembedded for retry",
			len(text), snippetHash(text), err)
	}
	return nil, true
}

// preparedBatch is a tokenised, padded batch ready for the GPU, plus where its
// vectors belong in the caller's output slice (out[start : start+n]).
type preparedBatch struct {
	start    int     // output offset
	n        int     // batch size (rows)
	maxLen   int     // padded sequence length
	flatIDs  []int64 // [n*maxLen] input_ids
	flatMask []int64 // [n*maxLen] attention_mask
	degraded []bool  // [n] true where the clause could not be tokenised → output discarded (nil)
}

// prepareBatch is the CPU half of an embed: tokenise + truncate + pad one batch
// (len <= batchSize) into flat input_ids / attention_mask tensors. No GPU, no
// shared mutable state beyond the tokenizer — so it can run on the producer
// goroutine while the GPU runs an earlier batch. start is the output offset.
func (e *onnxEmbedder) prepareBatch(tok clauseTokenizer, texts []string, start int) preparedBatch {
	tStart := time.Now()
	b := len(texts)
	rows := make([][]int64, b)
	degraded := make([]bool, b)
	maxLen := 1
	for i, t := range texts {
		ids, deg := e.tokenizeSafe(tok, t)
		if deg {
			// Untokenisable even after sanitising: keep a minimal valid row so the
			// batch tensor is well-formed, but flag it so runPrepared discards the
			// output (out[pos]=nil) and the clause stays NULL for retry.
			degraded[i] = true
			ids = []int{xlmrCLS, xlmrSEP}
		}
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
	// Snap the padded length to the fixed ladder so the ONNX session sees a handful
	// of shapes (plans cached) instead of one per distinct length (replan storm).
	// Mask-aware pooling makes the extra padding identity-neutral (A2 invariant).
	if shapeLadderOn() {
		maxLen = ladderCeil(maxLen)
	}
	defer func() { prof.addTokenize(time.Since(tStart)) }()
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
	return preparedBatch{start: start, n: b, maxLen: maxLen, flatIDs: flatIDs, flatMask: flatMask, degraded: degraded}
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
	// The output tensor aliases a PER-RUN pooled buffer (not a shared one), so the
	// lock need only cover session.Run (ORT Run is not concurrent-safe for one
	// session). Building the tensor and the copy-out + L2-normalise run lock-free,
	// so sibling sessions on the same GPU overlap instead of serialising.
	buf := s.getOutBuf()
	defer s.bufPool.Put(buf)
	outT, err := ort.NewTensor(ort.NewShape(int64(pb.n), int64(Dim)), (*buf)[:pb.n*Dim])
	if err != nil {
		return err
	}
	defer func() { _ = outT.Destroy() }()
	s.mu.Lock()
	runStart := time.Now()
	err = s.session.Run([]ort.Value{idsT, maskT}, []ort.Value{outT})
	runEnd := time.Now()
	s.mu.Unlock()
	if err != nil {
		return fmt.Errorf("onnx run: %w", err)
	}
	prof.addRun(runEnd.Sub(runStart), pb.n, pb.maxLen)
	data := outT.GetData() // flat [n * Dim], aliasing buf (this run's private buffer)
	for i := 0; i < pb.n; i++ {
		if pb.degraded != nil && pb.degraded[i] {
			out[pb.start+i] = nil // untokenisable: leave NULL for retry + completeness count
			continue
		}
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
// provider. It ALWAYS allocates a SessionOptions (caller owns Destroy()) and sets
// an EXPLICIT graph-optimisation level — never leaving it unset.
//
// Why explicit, always: when no level is set, ORT's DEFAULT is ORT_ENABLE_ALL,
// which runs the EXTENDED fusions. One of them, SimplifiedLayerNormFusion, throws
// on the bge-m3-fp16 export under onnxruntime 1.26 ("GetIndexFromName … node which
// does not exist"), so the first session creation failed and the embedder
// degraded to Disabled{} in prod — lexical-only despite the vectors being baked
// (issue: embedder-disabled-extended-fusion). The historical "return (nil,nil) on
// CPU" path was exactly that ORT-default-ALL trap. We now default to ENABLE_BASIC
// (level 1: safe constant-folding/redundant-node passes, SKIPS the extended
// fusion). Graph optimisation is numerically equivalent, so query vectors stay
// cosine-compatible with the baked DB vectors — no re-embed.
//
//   - EMBED_GRAPH_OPT (opt-in): escalate to ENABLE_ALL + optional warm-start cache
//     (EMBED_GRAPH_CACHE) for a model KNOWN compatible (e.g. a future re-export).
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
	opts, err := ort.NewSessionOptions()
	if err != nil {
		return nil, err
	}
	// BASIC by default (overrides ORT's ENABLE_ALL default); ALL only on opt-in.
	var level ort.GraphOptimizationLevel = ort.GraphOptimizationLevelEnableBasic
	if graphOpt {
		level = ort.GraphOptimizationLevelEnableAll
	}
	if err := opts.SetGraphOptimizationLevel(level); err != nil {
		_ = opts.Destroy()
		return nil, err
	}
	if graphOpt {
		if cache := os.Getenv("EMBED_GRAPH_CACHE"); cache != "" {
			// Best-effort: a cache-path error must not sink the whole session.
			if err := opts.SetOptimizedModelFilePath(cache); err != nil {
				log.Printf("embed: EMBED_GRAPH_CACHE %q ignored: %v", cache, err)
			}
		}
		log.Printf("embed: ONNX graph optimisation = ENABLE_ALL")
	}
	// CPU thread tuning. On a CPU-only serve box the BGE-M3 forward pass dominates
	// per-query latency, and ORT's default thread counts can over- or
	// under-subscribe the cores. ORT_INTRA_OP_THREADS sizes the per-operator
	// parallelism (the primary single-query dial — set it to the physical core
	// count); ORT_INTER_OP_THREADS sizes cross-operator parallelism. Both UNSET ⇒
	// ORT's own defaults (behaviour unchanged), so this is purely opt-in tuning.
	if n := envInt("ORT_INTRA_OP_THREADS", 0); n > 0 {
		if err := opts.SetIntraOpNumThreads(n); err != nil {
			_ = opts.Destroy()
			return nil, err
		}
		log.Printf("embed: ORT intra-op threads = %d", n)
	}
	if n := envInt("ORT_INTER_OP_THREADS", 0); n > 0 {
		if err := opts.SetInterOpNumThreads(n); err != nil {
			_ = opts.Destroy()
			return nil, err
		}
		log.Printf("embed: ORT inter-op threads = %d", n)
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
	return &gpuSession{session: sess, device: device}, nil
}
