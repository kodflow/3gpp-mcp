// Package search hosts the intent router and retrieval fusion (CLAUDE.md §3).
//
// V1 is lexical-first: BM25 when the FTS index is live (else a LIKE fallback in
// the store), with RRF available to fuse multiple ranked lists once a vector
// backend is added. Version ordering lives in the store (ListReleases), using
// (release, version, freeze_date) — never semver alone (CLAUDE.md §8.3).
package search

import (
	"context"
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
	st        *store.Store
	emb       embed.Embedder
	sp        embed.SparseEmbedder // non-nil when the embedder also produces sparse weights
	rr        rerank.Reranker
	vecShards []string // Option B: attached sub-base aliases; empty = single-DB vectors

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
func New(st *store.Store) *Engine {
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

// EmbedderEnabled reports whether this engine can vectorise a query (so the
// server can tell, and report, whether semantic search is actually reachable).
func (e *Engine) EmbedderEnabled() bool { return e.emb.Enabled() }

// EmbedderModelID is the model id of the query embedder ("" when disabled).
func (e *Engine) EmbedderModelID() string { return e.emb.ModelID() }

// RerankerEnabled reports whether the cross-encoder reranker is available.
func (e *Engine) RerankerEnabled() bool { return e.rr.Enabled() }

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
func (e *Engine) Search(ctx context.Context, r Request) ([]model.SearchHit, error) {
	// Per-request time budget: cap the wall-clock the EXPENSIVE arms (CPU query
	// embed, sparse, cross-encoder rerank) may spend, so a slow pass under
	// concurrency degrades to whatever it has rather than running to the edge
	// timeout. The cheap lexical arm always runs on the caller's ctx so an
	// already-expired budget still returns BM25 results (degrade, never block).
	bctx := ctx
	if budget := searchBudgetFor(); budget > 0 {
		var cancel context.CancelFunc
		bctx, cancel = context.WithTimeout(ctx, budget)
		defer cancel()
	}

	topK := max(r.TopK, 10)
	wantLex := r.Mode != "semantic" && !e.offLexical.Load()
	wantVec := r.Mode != "lexical" && !e.offVector.Load()

	var lists [][]model.SearchHit
	if wantLex {
		lex, err := e.st.SearchClauses(ctx, store.SearchQuery{Text: r.Text, Filter: r.Filter, TopK: topK})
		if err != nil {
			return nil, err
		}
		lists = append(lists, lex)
	}
	if wantVec && e.emb.Enabled() && bctx.Err() == nil {
		if vecs, err := e.emb.Embed(bctx, []string{r.Text}); err == nil && len(vecs) == 1 {
			var vhits []model.SearchHit
			var verr error
			switch {
			case len(e.vecShards) > 0:
				// Option B: scatter-gather across the attached per-series sub-bases.
				vhits, verr = e.st.SearchVectorsSharded(bctx, vecs[0], e.vecShards, r.Filter, topK)
			case e.st.VSSAvailable() && !e.offHNSW.Load():
				vhits, verr = e.st.SearchVectors(bctx, vecs[0], r.Filter, topK) // single-DB HNSW
			default:
				// No HNSW: exact cosine over the BM25 candidate set only (bounded),
				// never a full-corpus scan.
				cand, cerr := e.st.SearchClauses(bctx, store.SearchQuery{Text: r.Text, Filter: r.Filter, TopK: vecCandidateN})
				if cerr == nil {
					vhits, verr = e.st.SearchVectorsAmong(bctx, vecs[0], chunkIDsOf(cand), topK)
				}
			}
			if verr == nil && len(vhits) > 0 {
				lists = append(lists, vhits)
			}
		}
	}
	// Sparse (learned-lexical) arm: same gating as the dense arm (any non-"lexical"
	// mode), independent toggle. Best-effort — embed/score failure just omits the
	// list (degrade, never block). Fuses into the same RRF as BM25 + dense.
	wantSparse := r.Mode != "lexical" && !e.offSparse.Load() && e.sp != nil && e.st.SparseAvailable() && bctx.Err() == nil
	if wantSparse {
		if svecs, err := e.sp.EmbedSparse(bctx, []string{r.Text}); err == nil && len(svecs) == 1 && len(svecs[0]) > 0 {
			if shits, serr := e.st.SearchSparse(bctx, svecs[0], r.Filter, topK); serr == nil && len(shits) > 0 {
				lists = append(lists, shits)
			}
		}
	}
	// Degrade: a mode that produced no list (e.g. "semantic" with no embedder /
	// vectors) falls back to lexical so a query never silently returns nothing.
	if len(lists) == 0 {
		lex, err := e.st.SearchClauses(ctx, store.SearchQuery{Text: r.Text, Filter: r.Filter, TopK: topK})
		if err != nil {
			return nil, err
		}
		lists = append(lists, lex)
	}
	hits := RRF(60, lists...)

	// Optional cross-encoder rerank: re-score a broad window of fused candidates
	// then narrow to TopK. Best-effort — a reranker error keeps the RRF order.
	if (r.Rerank || e.rerankAll.Load()) && e.rr.Enabled() && len(hits) > 1 && bctx.Err() == nil {
		window := min(rerankWindowFor(), len(hits))
		if reordered, err := e.rerank(bctx, r.Text, hits[:window]); err == nil {
			hits = append(reordered, hits[window:]...)
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
	if err != nil || len(scores) != len(cand) {
		return nil, errRerank
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
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if out[i].Clause.SpecID != out[j].Clause.SpecID {
			return out[i].Clause.SpecID < out[j].Clause.SpecID
		}
		return out[i].Clause.ClausePath < out[j].Clause.ClausePath
	})
	return out
}
