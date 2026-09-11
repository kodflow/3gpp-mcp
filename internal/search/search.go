// Package search hosts the intent router and retrieval fusion (CLAUDE.md §3).
//
// V1 is lexical-first: BM25 when the FTS index is live (else a LIKE fallback in
// the store), with RRF available to fuse multiple ranked lists once a vector
// backend is added. Version ordering lives in the store (ListReleases), using
// (release, version, freeze_date) — never semver alone (CLAUDE.md §8.3).
package search

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/kodflow/3gpp-mcp/internal/embed"
	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/rerank"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// rerankWindow is the DEFAULT number of fused candidates the cross-encoder
// re-scores before the engine narrows to TopK (axis #7: retrieve broad → rerank →
// return narrow). Overridable via RERANK_WINDOW: on a CPU-only box each reranked
// candidate is a cross-encoder forward pass, so the window is the rerank
// latency/quality dial — widen it for recall, narrow it to protect p99.
const rerankWindow = 12

// rerankWindowFor returns the effective rerank window: RERANK_WINDOW when set to a
// positive int (clamped to a sane ceiling so a fat-fingered value can't turn the
// reranker into a full-corpus cross-encoder), else the default.
func rerankWindowFor() int {
	if v := os.Getenv("RERANK_WINDOW"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 {
			return min(n, 200)
		}
	}
	return rerankWindow
}

// defaultSearchBudget caps the wall-clock a single query may spend across all
// arms. On a CPU-only box each ONNX pass (query embed, cross-encoder rerank) is
// multi-second and the ORT session is mutex-serialised, so a burst of concurrent
// queries can queue into the minute range and hit the edge proxy timeout. The
// budget makes a query DEGRADE (return the fusion it already has) instead of
// running unbounded — degrade, never block (CLAUDE.md §1).
const defaultSearchBudget = 20 * time.Second

// searchBudgetFor returns the per-request budget: SEARCH_BUDGET when set (a Go
// duration like "8s" or a bare integer of seconds), else the default. A value of
// "0" (or negative) disables the budget entirely.
func searchBudgetFor() time.Duration {
	v := os.Getenv("SEARCH_BUDGET")
	if v == "" {
		return defaultSearchBudget
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d
	}
	if n, err := strconv.Atoi(v); err == nil {
		return time.Duration(n) * time.Second
	}
	return defaultSearchBudget
}

// vecCandidateN bounds the BM25 candidate pool the no-HNSW vector fallback scores
// exactly (so it stays O(N), never a full-corpus cosine scan).
const vecCandidateN = 200

// chunkIDsOf extracts the chunk ids of a ranked hit list (candidate set for the
// no-HNSW exact-rerank fallback).
func chunkIDsOf(hits []model.SearchHit) []uint64 {
	ids := make([]uint64, len(hits))
	for i, h := range hits {
		ids[i] = h.Clause.ChunkID
	}
	return ids
}

// Intent is the routing decision for a query (CLAUDE.md §3).
type Intent string

const (
	IntentSpecLookup Intent = "spec_lookup" // "TS 33.128"
	IntentChangelog  Intent = "changelog"   // "diff between Rel-18 and Rel-19"
	IntentGlossary   Intent = "glossary"    // "definition of AMF"
	IntentGraph      Intent = "graph"       // "what replaces MME" (V2)
	IntentHybrid     Intent = "hybrid"      // default: BM25 (+vector when present)
)

var (
	reSpec      = regexp.MustCompile(`\bT[SR]\s?\d\d\.\d{3}\b`)
	reChangelog = regexp.MustCompile(`(?i)\b(diff|change|évolution|difference|différence)\b.*\brel-?\d+\b.*\brel-?\d+\b`)
	reGlossary  = regexp.MustCompile(`(?i)\b(defined|definition|définition|expansion|stands for|signifie)\b`)
	reGraph     = regexp.MustCompile(`(?i)\b(replace[sd]?|remplace|équivalent|evolution|migration|maps to)\b`)
	reAcronym   = regexp.MustCompile(`\b[A-Z][A-Z0-9]{1,7}\b`)
)

// Classify picks the backend for a free-text query.
func Classify(q string) Intent {
	switch {
	case reChangelog.MatchString(q):
		return IntentChangelog
	case reGraph.MatchString(q):
		return IntentGraph
	case reGlossary.MatchString(q) && reAcronym.MatchString(q):
		return IntentGlossary
	case reSpec.MatchString(q):
		return IntentSpecLookup
	default:
		return IntentHybrid
	}
}

// Engine ties the router to the store, the (optional) embedder, and the
// (optional) reranker.
//
// The off*/rerankAll atomics are RUNTIME overrides (flipped live by the HTTP
// dashboard, process-global since serve is one engine) so an operator can A/B the
// retrieval arms and watch the latency impact without a redeploy. Zero value =
// the normal default: lexical ON, vector ON, HNSW used (not forced exact-scan),
// per-request rerank only. They only ever turn a CAPABLE arm down/up — they can't
// conjure a vector arm with no embedder.
type Engine struct {
	st        store.Reader
	emb       embed.Embedder
	sp        embed.SparseEmbedder // non-nil when the embedder also produces sparse weights
	rr        rerank.Reranker
	vecShards []string // Option B: attached sub-base aliases; empty = single-DB vectors
	name      string   // the corpus this engine answers for, as reports name it ("3gpp", "etsi")

	offLexical atomic.Bool // true → skip the BM25 arm
	offVector  atomic.Bool // true → skip the vector (dense) arm
	offSparse  atomic.Bool // true → skip the sparse (learned-lexical) arm
	offHNSW    atomic.Bool // true → force exact-scan even when a frozen HNSW exists
	rerankAll  atomic.Bool // true → cross-encoder rerank EVERY query (not just r.Rerank)
}

// New builds an Engine over a store, picking the embedder + reranker from the
// environment (both default to disabled — degrade, never block). The query
// embedder is wrapped in a bounded LRU (serve repeats queries; zero quality loss),
// and RERANK_ALL=1 turns on always-rerank at startup so the deploy can ship the
// reranker on every query without a per-request flag.
func New(st store.Reader) *Engine {
	base := embed.New()
	e := &Engine{
		st:  st,
		emb: withQueryCache(base, queryCacheSize()),
		rr:  rerank.New(),
	}
	// Sparse capability comes from the SAME model (BGE-M3 emits dense + sparse). We
	// assert the BASE embedder (the cache wrapper only fronts the dense path).
	if sp, ok := base.(embed.SparseEmbedder); ok {
		e.sp = sp
	}
	if v := strings.ToLower(os.Getenv("RERANK_ALL")); v == "1" || v == "true" || v == "on" {
		e.rerankAll.Store(true)
	}
	return e
}

// SetLexical/SetVector/SetHNSW/SetRerank flip the runtime overrides (dashboard).
func (e *Engine) SetLexical(on bool) { e.offLexical.Store(!on) }
func (e *Engine) SetVector(on bool)  { e.offVector.Store(!on) }
func (e *Engine) SetSparse(on bool)  { e.offSparse.Store(!on) }
func (e *Engine) SetHNSW(on bool)    { e.offHNSW.Store(!on) }
func (e *Engine) SetRerank(on bool)  { e.rerankAll.Store(on) }

// State is a live snapshot of capabilities (what the engine CAN do) and the
// current runtime toggles (what is ON right now) — the dashboard reads this.
type State struct {
	EmbedderEnabled bool   `json:"embedder_enabled"` // capability
	FTSEnabled      bool   `json:"fts_enabled"`      // capability
	HNSWFrozen      bool   `json:"hnsw_frozen"`      // capability (a frozen index exists)
	SparseEnabled   bool   `json:"sparse_enabled"`   // capability (sparse embedder + clause_sparse populated)
	RerankerEnabled bool   `json:"reranker_enabled"` // capability
	LexicalOn       bool   `json:"lexical_on"`       // toggle
	VectorOn        bool   `json:"vector_on"`        // toggle
	SparseOn        bool   `json:"sparse_on"`        // toggle (sparse arm)
	HNSWOn          bool   `json:"hnsw_on"`          // toggle (false = forced exact-scan)
	RerankOn        bool   `json:"rerank_on"`        // toggle (rerank every query)
	EmbedderModelID string `json:"embedder_model_id"`
}

// State returns the live capability + toggle snapshot.
func (e *Engine) State() State {
	return State{
		EmbedderEnabled: e.emb.Enabled(),
		FTSEnabled:      e.st.FTSAvailable(),
		HNSWFrozen:      e.st.VSSAvailable(),
		SparseEnabled:   e.sp != nil && e.st.SparseAvailable(),
		RerankerEnabled: e.rr.Enabled(),
		LexicalOn:       !e.offLexical.Load(),
		VectorOn:        !e.offVector.Load(),
		SparseOn:        !e.offSparse.Load(),
		HNSWOn:          !e.offHNSW.Load(),
		RerankOn:        e.rerankAll.Load(),
		EmbedderModelID: e.emb.ModelID(),
	}
}

// UseVectorShards routes the vector arm through the scatter-gather over these
// attached sub-base aliases (Option B) instead of the single-DB HNSW. Pass the
// aliases returned by store.AttachShards; empty restores single-DB behaviour.
func (e *Engine) UseVectorShards(aliases []string) { e.vecShards = aliases }

// SetName names the corpus this engine answers for, as its Reports say it.
func (e *Engine) SetName(name string) { e.name = name }

// SetReranker replaces the cross-encoder New picked from the environment.
func (e *Engine) SetReranker(r rerank.Reranker) { e.rr = r }

// NewSharing builds an Engine over st that SHARES o's models — the query embedder
// (with its cache) and the cross-encoder — instead of loading its own.
//
// The ETSI half is a second engine over a second store, and New loads a second
// cross-encoder for it: another ONNX session holding the same bge-reranker-v2-m3
// weights (2.3 GB of fp32 on disk) and its own arena, for a model that is
// read-only and whose Run is already serialised by its own mutex. The two halves
// answer one request one after the other, so a second copy bought no concurrency
// — only memory, on the process that has to fit both corpora's HNSW indexes
// besides. The scores are the same model's on the same inputs either way.
//
// Only the models are shared: the store, the vector shards, the name and the
// runtime toggles stay the engine's own.
func NewSharing(st store.Reader, o *Engine) *Engine {
	e := &Engine{st: st, emb: o.emb, sp: o.sp, rr: o.rr}
	e.rerankAll.Store(o.rerankAll.Load())
	return e
}

// EmbedderEnabled reports whether this engine can vectorise a query (so the
// server can tell, and report, whether semantic search is actually reachable).
func (e *Engine) EmbedderEnabled() bool { return e.emb.Enabled() }

// EmbedderModelID is the model id of the query embedder ("" when disabled).
func (e *Engine) EmbedderModelID() string { return e.emb.ModelID() }

// RerankerEnabled reports whether the cross-encoder reranker is available.
func (e *Engine) RerankerEnabled() bool { return e.rr.Enabled() }

// ModeServed reports the retrieval mode this engine can ACTUALLY run for a
// requested one, so a caller can say which it got instead of implying it got
// what it asked for.
//
// Search degrades on purpose — a "semantic" request with no embedder falls back
// to lexical rather than returning nothing. That is the right behaviour and the
// wrong silence: the results come back shaped exactly like semantic ones, and
// nothing in the payload says the requested arm never ran. For a server whose
// whole doctrine is "cite, never guess", answering a different question than the
// one asked without saying so is the same failure in a smaller box.
//
// This reports what the ENGINE knows: whether a query can be vectorised at all.
// It deliberately does not try to predict an empty vector arm on a corpus with
// no vectors — that is what server_info's `hnsw` field is for.
func (e *Engine) ModeServed(requested string) string {
	lex := !e.offLexical.Load()
	vec := e.emb.Enabled() && !e.offVector.Load()

	switch requested {
	case "lexical":
		return "lexical"
	case "semantic":
		if vec {
			return "semantic"
		}
		return "lexical"
	default: // "" and "hybrid"
		switch {
		case vec && lex:
			return "hybrid"
		case vec:
			return "semantic"
		default:
			return "lexical"
		}
	}
}

// Request parameterises a search. Mode selects which retrieval arms run:
// "" / "hybrid" = lexical ⊕ vector, "lexical" = BM25 only, "semantic" = vector
// only (degrades to lexical when no embedder/vectors — never returns nothing).
type Request struct {
	Text   string
	Filter store.SpecFilter
	TopK   int
	Mode   string // "" | "hybrid" | "lexical" | "semantic"
	Rerank bool   // when true and a reranker is enabled, re-score the fused window
}

// Search retrieves and fuses (RRF) the lexical and/or vector ranked lists per
// r.Mode. Each arm is best-effort; a "semantic" request with no usable vectors
// degrades to lexical rather than returning nothing (degrade, never block).
//
// What each arm did is recorded in a Report, appended to the Trace the context
// carries (WithTrace): an arm that is skipped says so there, never only by its
// absence from the fusion.
func (e *Engine) Search(ctx context.Context, r Request) ([]model.SearchHit, error) {
	began := time.Now()
	rep := Report{Corpus: e.name, DocType: r.Filter.DocType}
	hits, err := e.search(ctx, r, &rep)
	rep.Ms = msSince(began)
	if t := traceFrom(ctx); t != nil {
		t.add(rep)
	}
	return hits, err
}

func (e *Engine) search(ctx context.Context, r Request, rep *Report) ([]model.SearchHit, error) {
	// Per-request time budget: cap the wall-clock the EXPENSIVE arms (CPU query
	// embed, sparse, cross-encoder rerank) may spend, so a slow pass under
	// concurrency degrades to whatever it has rather than running to the edge
	// timeout. The cheap lexical arm always runs on the caller's ctx so an
	// already-expired budget still returns BM25 results (degrade, never block).
	// storeCtxNote — WHY THE BUDGET NEVER REACHES A DuckDB QUERY.
	//
	// Cancelling a running DuckDB query makes it raise duckdb::InterruptException,
	// and on Linux that C++ exception crosses cgo and ABORTS THE PROCESS. It never
	// becomes a Go error, so there is no degraded answer, no message to the client
	// and no server left. Measured 2026-09-06 against the published image, on the
	// published corpus:
	//
	//	SEARCH_BUDGET default (20s)   terminate called after throwing an instance
	//	                              of 'duckdb::InterruptException' — SIGABRT
	//	SEARCH_BUDGET=900s            exit 0, citations returned, 203 s
	//
	// A budget whose expiry kills the server is worse than no budget: the whole
	// point of it is "degrade, never block", and aborting is the one outcome that
	// is neither.
	//
	// So bctx keeps exactly the job the paragraph above describes — bounding the
	// EXPENSIVE ONNX passes (query embed, sparse embed, cross-encoder rerank),
	// which are Go-side and cancel safely — and it keeps gating whether each arm
	// is entered at all.
	//
	// AND THE STORE CALLS TAKE A CONTEXT THAT CANNOT BE CANCELLED AT ALL. Handing
	// them the caller's ctx was not enough and the first version of this fix did
	// exactly that: an HTTP client that disconnects, or a request deadline in the
	// transport, cancels the caller's ctx just as effectively as the budget did,
	// and the abort comes back. context.WithoutCancel keeps the VALUES — tracing,
	// deadlines other code may read — and drops only the cancellation signal.
	//
	// What is lost is the ability to cut a long DuckDB query short by any means.
	// What is kept is a process that survives a client pressing Ctrl-C.
	dbctx := context.WithoutCancel(ctx)
	// The budget is the request's when the caller started one (WithBudget, so a
	// federated call spends ONE budget across its passes), else this Search's own.
	bctx, cancel, budgetSpent := budgetCtx(ctx)
	defer cancel()

	topK := max(r.TopK, 10)
	wantLex := r.Mode != "semantic" && !e.offLexical.Load()
	wantVec := r.Mode != "lexical" && !e.offVector.Load()

	// Whether the sparse arm runs is decided HERE, before the dense arm, so the two
	// can share one forward pass when both are wanted. Its own gating is unchanged.
	wantSparse := r.Mode != "lexical" && !e.offSparse.Load() && e.sp != nil && e.st.SparseAvailable() && bctx.Err() == nil

	// ONE PASS FOR TWO HEADS. BGE-M3 emits the dense embedding and the learned
	// lexical weights from the same encoder, which ONNX Runtime computes whether
	// one output is read or two — so calling Embed and then EmbedSparse ran the
	// transformer twice over the same query string for nothing. Measured: ~166 ms
	// for the pair against a BM25 arm costing ~10 ms.
	//
	// The result is precomputed rather than the calls being merged inline, because
	// the two arms are structurally independent (each degrades on its own) and
	// should stay that way. A nil pair simply means the combined path was
	// unavailable and each arm embeds as before.
	var preDense []float32
	var preSparse model.SparseVec
	var embedTook time.Duration // the combined pass, charged to the dense arm below
	if wantVec && wantSparse && e.emb.Enabled() && bctx.Err() == nil {
		if dual, ok := e.emb.(embed.DualEmbedder); ok {
			t0 := time.Now()
			if d, s, ok := dual.EmbedBoth(bctx, r.Text); ok {
				preDense, preSparse = d, s
			}
			embedTook = time.Since(t0)
		}
	}

	var lists [][]model.SearchHit
	if wantLex {
		// dbctx here too, and this is the arm that made it obvious. The comment above
		// says the cheap lexical arm runs on the CALLER's ctx so an expired budget
		// still returns BM25 — true, and it quietly assumed the caller's ctx is
		// alive. It is not when the client has disconnected, and then this call
		// hands a cancelled context to DuckDB: an error on Windows, a process abort
		// on Linux. The arm that exists to guarantee "degrade, never block" was the
		// one that could block hardest.
		t0 := time.Now()
		lex, err := e.st.SearchClauses(dbctx, store.SearchQuery{Text: r.Text, Filter: r.Filter, TopK: topK})
		if err != nil {
			rep.skip(ArmLexical, failed("lexical search failed", err), t0)
			return nil, err
		}
		rep.ran(ArmLexical, len(lex), t0)
		lists = append(lists, lex)
	}
	if r.Mode != "lexical" {
		t0 := time.Now().Add(-embedTook)
		switch {
		case e.offVector.Load():
			rep.skip(ArmDense, "turned off at runtime (dashboard)", t0)
		case !e.emb.Enabled():
			rep.skip(ArmDense, "no query embedder in this server (call server_info for why)", t0)
		case bctx.Err() != nil:
			rep.skip(ArmDense, budgetSpent(), t0)
		default:
			vecs, err := [][]float32{preDense}, error(nil)
			if preDense == nil {
				vecs, err = e.emb.Embed(bctx, []string{r.Text})
			}
			switch {
			case err != nil:
				rep.skip(ArmDense, failed("the query embedding failed", err), t0)
			case len(vecs) != 1:
				rep.skip(ArmDense, fmt.Sprintf("the query embedder returned %d vectors for one query", len(vecs)), t0)
			default:
				var vhits []model.SearchHit
				var verr error
				// dbctx, NOT bctx AND NOT ctx — see storeCtxNote above.
				switch {
				case len(e.vecShards) > 0:
					// Option B: scatter-gather across the attached per-series sub-bases.
					vhits, verr = e.st.SearchVectorsSharded(dbctx, vecs[0], e.vecShards, r.Filter, topK)
				case e.st.VSSAvailable() && !e.offHNSW.Load():
					vhits, verr = e.st.SearchVectors(dbctx, vecs[0], r.Filter, topK) // single-DB HNSW
				default:
					// No HNSW: exact cosine over the BM25 candidate set only (bounded),
					// never a full-corpus scan.
					cand, cerr := e.st.SearchClauses(dbctx, store.SearchQuery{Text: r.Text, Filter: r.Filter, TopK: vecCandidateN})
					if cerr != nil {
						verr = cerr
					} else {
						vhits, verr = e.st.SearchVectorsAmong(dbctx, vecs[0], chunkIDsOf(cand), topK)
					}
				}
				if verr != nil {
					rep.skip(ArmDense, failed("the vector search failed", verr), t0)
				} else {
					rep.ran(ArmDense, len(vhits), t0)
					if len(vhits) > 0 {
						lists = append(lists, vhits)
					}
				}
			}
		}
	}
	// Sparse (learned-lexical) arm: same gating as the dense arm (any non-"lexical"
	// mode), independent toggle. Best-effort — embed/score failure just omits the
	// list (degrade, never block). Fuses into the same RRF as BM25 + dense.
	// The budget is re-checked HERE, not only where wantSparse was decided. That
	// decision now happens before the combined embed and the whole dense retrieval,
	// so the budget can expire in between — and the sparse implementations ignore
	// the context, so entering the arm would run inference past the deadline the
	// budget exists to hold. Skipping is free: RRF already fuses whatever arrived.
	if wantSparse && bctx.Err() != nil {
		wantSparse = false
	}
	if r.Mode != "lexical" {
		t0 := time.Now()
		switch {
		case wantSparse:
			svecs, err := []model.SparseVec{preSparse}, error(nil)
			if preSparse == nil {
				svecs, err = e.sp.EmbedSparse(bctx, []string{r.Text})
			}
			switch {
			case err != nil:
				rep.skip(ArmSparse, failed("the sparse query embedding failed", err), t0)
			case len(svecs) != 1:
				rep.skip(ArmSparse, fmt.Sprintf("the sparse embedder returned %d vectors for one query", len(svecs)), t0)
			case len(svecs[0]) == 0:
				rep.ran(ArmSparse, 0, t0) // a query with no weighted term matches nothing
			default:
				// dbctx: the store call must never be interrupted mid-query, by anyone.
				shits, serr := e.st.SearchSparse(dbctx, svecs[0], r.Filter, topK)
				if serr != nil {
					rep.skip(ArmSparse, failed("the sparse search failed", serr), t0)
				} else {
					rep.ran(ArmSparse, len(shits), t0)
					if len(shits) > 0 {
						lists = append(lists, shits)
					}
				}
			}
		case e.offSparse.Load():
			rep.skip(ArmSparse, "turned off at runtime (dashboard)", t0)
		case e.sp == nil:
			rep.skip(ArmSparse, "the query embedder has no sparse head (call server_info for why)", t0)
		case !e.st.SparseAvailable():
			rep.skip(ArmSparse, "this corpus carries no sparse postings", t0)
		default:
			rep.skip(ArmSparse, budgetSpent(), t0)
		}
	}
	// Degrade: a mode that produced no list (e.g. "semantic" with no embedder /
	// vectors) falls back to lexical so a query never silently returns nothing.
	// dbctx, like every other store call here: ctx is the caller's, and a caller
	// that has gone — or a request budget that has run out — must not reach DuckDB
	// (storeCtxNote).
	if len(lists) == 0 {
		t0 := time.Now()
		lex, err := e.st.SearchClauses(dbctx, store.SearchQuery{Text: r.Text, Filter: r.Filter, TopK: topK})
		if err != nil {
			rep.skip(ArmLexical, failed("lexical search failed", err), t0)
			return nil, err
		}
		rep.ran(ArmLexical, len(lex), t0)
		lists = append(lists, lex)
	}
	hits := RRF(60, lists...)

	// Optional cross-encoder rerank: re-score a broad window of fused candidates
	// then narrow to TopK. Best-effort — a reranker error keeps the RRF order, and
	// the report says the order is the fused one.
	if r.Rerank || e.rerankAll.Load() {
		t0 := time.Now()
		switch {
		case !e.rr.Enabled():
			rep.skip(ArmRerank, "no cross-encoder in this server: "+rerank.Reason(), t0)
		case len(hits) <= 1:
			rep.ran(ArmRerank, len(hits), t0) // nothing to reorder
		case bctx.Err() != nil:
			rep.skip(ArmRerank, budgetSpent(), t0)
		default:
			window := min(rerankWindowFor(), len(hits))
			reordered, err := e.rerank(bctx, r.Text, hits[:window])
			switch {
			case err == nil:
				hits = append(reordered, hits[window:]...)
				rep.ran(ArmRerank, window, t0)
			case bctx.Err() != nil:
				// The budget ran out BETWEEN the cross-encoder's batches.
				rep.skip(ArmRerank, budgetSpent()+" — it expired while the cross-encoder was running; the page keeps the fused order", t0)
			default:
				rep.skip(ArmRerank, failed("the cross-encoder failed, the page keeps the fused order", err), t0)
			}
		}
	}

	if len(hits) > topK {
		hits = hits[:topK]
	}
	return hits, nil
}

// rerank re-scores cand with the cross-encoder over (query, heading+text)
// passages and returns them sorted best-first. Citations are untouched — only
// the order changes (no hallucination surface).
func (e *Engine) rerank(ctx context.Context, query string, cand []model.SearchHit) ([]model.SearchHit, error) {
	passages := make([]string, len(cand))
	for i, h := range cand {
		passages[i] = h.Clause.Heading + "\n" + h.Clause.Text
	}
	scores, err := e.rr.Score(ctx, query, passages)
	switch {
	case err != nil:
		return nil, fmt.Errorf("%w: %v", errRerank, err)
	case len(scores) != len(cand):
		return nil, fmt.Errorf("%w: %d scores for %d passages", errRerank, len(scores), len(cand))
	}
	out := make([]model.SearchHit, len(cand))
	copy(out, cand)
	idx := make([]int, len(out))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return scores[idx[a]] > scores[idx[b]] })
	reordered := make([]model.SearchHit, len(out))
	for newPos, oldPos := range idx {
		reordered[newPos] = out[oldPos]
		reordered[newPos].Score = scores[oldPos]
	}
	return reordered, nil
}

var errRerank = errReason("reranker produced no usable scores")

type errReason string

func (e errReason) Error() string { return string(e) }

// rrfKey is the LOGICAL identity of a clause for fusion. It must NOT be the
// store's chunk_id: chunk_id is a per-DB counter (ingest.go restarts it at 1 in
// every shard), so two distinct clauses from two sub-bases collide on the same
// chunk_id. Keying RRF on chunk_id then silently drops one clause and
// misattributes its rank mass to the other — a cite-or-silent hallucination
// (finding rrf-chunkid-collision-across-shards). The 3GPP-canonical identity is
// (spec_id, release, version, clause_path); that tuple is globally unique across
// shards and is exactly the citation the server returns.
func rrfKey(c model.Clause) string {
	return c.SpecID + "\x00" + c.Release + "\x00" + c.Version + "\x00" + c.ClausePath
}

// RRF fuses ranked lists by Reciprocal Rank Fusion (CLAUDE.md §3, k=60).
// score(doc) = Σ 1/(k + rank_i(doc)). Lists are ranked best-first. With a
// single list it preserves order (monotonic in rank).
func RRF(k float64, lists ...[]model.SearchHit) []model.SearchHit {
	type agg struct {
		hit   model.SearchHit
		score float64
	}
	byKey := map[string]*agg{}
	for _, list := range lists {
		for rank, h := range list {
			key := rrfKey(h.Clause)
			a, ok := byKey[key]
			if !ok {
				a = &agg{hit: h}
				byKey[key] = a
			}
			a.score += 1.0 / (k + float64(rank+1))
		}
	}
	out := make([]model.SearchHit, 0, len(byKey))
	for _, a := range byKey {
		a.hit.Score = a.score
		out = append(out, a.hit)
	}
	// A TOTAL ORDER over the fusion key. The tie-break stopped at (spec_id,
	// clause_path) while the key also carries release and version, so two versions
	// of one clause with equal fused scores — rank r in one arm each, common when
	// the arms surface different versions of the same text — were ordered by Go's
	// randomised map iteration, and the served page could change between two
	// identical calls. Found by the served retrieval gate (2026-09-11): one query's
	// hybrid nDCG@10 read 0.50, then 0.43, on the same corpus. Release and version
	// come AFTER clause_path, so every order the old comparison decided is kept.
	// This is a DETERMINISTIC order among exact ties, not a recency order: nothing
	// here claims one version is newer (that is (release, version, freeze_date),
	// CLAUDE.md §8.3, and the store's job) — it only stops the page from changing
	// between two identical calls.
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].Clause, out[j].Clause
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if a.SpecID != b.SpecID {
			return a.SpecID < b.SpecID
		}
		if a.ClausePath != b.ClausePath {
			return a.ClausePath < b.ClausePath
		}
		if a.Release != b.Release {
			return a.Release < b.Release
		}
		return a.Version < b.Version
	})
	return out
}
