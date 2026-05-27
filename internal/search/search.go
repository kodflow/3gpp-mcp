// Package search hosts the intent router and retrieval fusion (CLAUDE.md §3).
//
// V1 is lexical-first: BM25 when the FTS index is live (else a LIKE fallback in
// the store), with RRF available to fuse multiple ranked lists once a vector
// backend is added. Version ordering lives in the store (ListReleases), using
// (release, version, freeze_date) — never semver alone (CLAUDE.md §8.3).
package search

import (
	"context"
	"regexp"
	"sort"

	"github.com/kodflow/3gpp-mcp/internal/embed"
	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/rerank"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// rerankWindow is how many fused candidates the cross-encoder re-scores before
// the engine narrows to TopK (axis #7: retrieve broad → rerank → return narrow).
const rerankWindow = 20

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
type Engine struct {
	st  *store.Store
	emb embed.Embedder
	rr  rerank.Reranker
}

// New builds an Engine over a store, picking the embedder + reranker from the
// environment (both default to disabled — degrade, never block).
func New(st *store.Store) *Engine {
	return &Engine{st: st, emb: embed.New(), rr: rerank.New()}
}

// Request parameterises a search.
type Request struct {
	Text   string
	Filter store.SpecFilter
	TopK   int
	Rerank bool // when true and a reranker is enabled, re-score the fused window
}

// Search runs lexical retrieval (BM25/LIKE) for the request. When a vector
// backend is wired, its ranked list is fused here via RRF; today there is one
// list so RRF is a no-op identity.
func (e *Engine) Search(ctx context.Context, r Request) ([]model.SearchHit, error) {
	topK := max(r.TopK, 10)
	lex, err := e.st.SearchClauses(ctx, store.SearchQuery{Text: r.Text, Filter: r.Filter, TopK: topK})
	if err != nil {
		return nil, err
	}
	lists := [][]model.SearchHit{lex}
	// Hybrid: add the vector ranked list when embeddings are available. Both
	// query embedding and HNSW search are best-effort — failures (no vectors,
	// no VSS) just fall back to the lexical list.
	if e.emb.Enabled() {
		if vecs, err := e.emb.Embed(ctx, []string{r.Text}); err == nil && len(vecs) == 1 {
			var vhits []model.SearchHit
			var verr error
			if e.st.VSSAvailable() {
				vhits, verr = e.st.SearchVectors(ctx, vecs[0], r.Filter, topK) // HNSW fast path
			} else {
				// No HNSW: exact cosine over the BM25 candidate set only (bounded),
				// never a full-corpus scan. One extra wide lexical fetch.
				cand, cerr := e.st.SearchClauses(ctx, store.SearchQuery{Text: r.Text, Filter: r.Filter, TopK: vecCandidateN})
				if cerr == nil {
					vhits, verr = e.st.SearchVectorsAmong(ctx, vecs[0], chunkIDsOf(cand), topK)
				}
			}
			if verr == nil && len(vhits) > 0 {
				lists = append(lists, vhits)
			}
		}
	}
	hits := RRF(60, lists...)

	// Optional cross-encoder rerank: re-score a broad window of fused candidates
	// then narrow to TopK. Best-effort — a reranker error keeps the RRF order.
	if r.Rerank && e.rr.Enabled() && len(hits) > 1 {
		window := min(rerankWindow, len(hits))
		if reordered, err := e.rerank(ctx, r.Text, hits[:window]); err == nil {
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

// RRF fuses ranked lists by Reciprocal Rank Fusion (CLAUDE.md §3, k=60).
// score(doc) = Σ 1/(k + rank_i(doc)). Lists are ranked best-first. With a
// single list it preserves order (monotonic in rank).
func RRF(k float64, lists ...[]model.SearchHit) []model.SearchHit {
	type agg struct {
		hit   model.SearchHit
		score float64
	}
	byID := map[uint64]*agg{}
	for _, list := range lists {
		for rank, h := range list {
			a, ok := byID[h.Clause.ChunkID]
			if !ok {
				a = &agg{hit: h}
				byID[h.Clause.ChunkID] = a
			}
			a.score += 1.0 / (k + float64(rank+1))
		}
	}
	out := make([]model.SearchHit, 0, len(byID))
	for _, a := range byID {
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
