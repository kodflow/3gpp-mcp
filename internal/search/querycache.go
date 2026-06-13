package search

// querycache.go — a bounded LRU over QUERY embeddings, wrapping the engine's
// Embedder. It exists purely for the serve path: on a CPU-only box the BGE-M3
// forward pass is the per-query latency floor, and real traffic repeats queries
// (retries, health/probe loops, popular questions, the dashboard's own calls).
// Caching the (text → dense vector) result is a ZERO-quality-loss latency win: the
// vector returned is byte-identical to recomputing it, so recall/precision are
// untouched — only the redundant forward pass is skipped.
//
// It deliberately lives in the engine layer, NOT embed.New(): cmd/embed vectorises
// the whole corpus where every clause is unique, so a cache there would only waste
// memory. Queries are the opposite — few, short, and repeated.
//
// Disabled (size 0) → the underlying embedder is returned unwrapped. Single-text
// Embed (the query path) is cached; any multi-text batch bypasses the cache.

import (
	"container/list"
	"context"
	"os"
	"strconv"
	"sync"

	"github.com/kodflow/3gpp-mcp/internal/embed"
)

// defaultQueryCacheSize is the entry cap when EMBED_QUERY_CACHE is unset. 512 short
// query vectors (1024 float32 ≈ 4 KiB each ⇒ ~2 MiB) is negligible on the 32 GB box.
const defaultQueryCacheSize = 512

// queryCacheSize reads EMBED_QUERY_CACHE: a non-negative entry cap, 0 disables.
func queryCacheSize() int {
	if v := os.Getenv("EMBED_QUERY_CACHE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return defaultQueryCacheSize
}

// withQueryCache wraps e in an LRU cache of size entries. size<=0 or a disabled
// embedder returns e unchanged (nothing to cache).
func withQueryCache(e embed.Embedder, size int) embed.Embedder {
	if size <= 0 || !e.Enabled() {
		return e
	}
	return &cachingEmbedder{
		Embedder: e,
		cap:      size,
		items:    make(map[string]*list.Element),
		order:    list.New(),
	}
}

type cacheEntry struct {
	key string
	vec []float32
}

// cachingEmbedder is an Embedder decorator: it serves repeated single-query
// embeddings from an LRU and delegates everything else.
type cachingEmbedder struct {
	embed.Embedder
	cap   int
	mu    sync.Mutex
	items map[string]*list.Element // key → *list.Element holding *cacheEntry
	order *list.List               // front = most-recently used
}

// Embed serves a single-text query from the LRU when present; otherwise it
// delegates, caches the result, and evicts the least-recently used over cap.
// Multi-text batches are never cached (not the query path).
func (c *cachingEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) != 1 {
		return c.Embedder.Embed(ctx, texts)
	}
	key := texts[0]
	if v, ok := c.get(key); ok {
		return [][]float32{v}, nil
	}
	out, err := c.Embedder.Embed(ctx, texts)
	if err != nil || len(out) != 1 {
		return out, err
	}
	c.put(key, out[0])
	return out, nil
}

func (c *cachingEmbedder) get(key string) ([]float32, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.order.MoveToFront(el)
		return el.Value.(*cacheEntry).vec, true
	}
	return nil, false
}

func (c *cachingEmbedder) put(key string, vec []float32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.order.MoveToFront(el)
		el.Value.(*cacheEntry).vec = vec
		return
	}
	el := c.order.PushFront(&cacheEntry{key: key, vec: vec})
	c.items[key] = el
	for c.order.Len() > c.cap {
		back := c.order.Back()
		if back == nil {
			break
		}
		c.order.Remove(back)
		delete(c.items, back.Value.(*cacheEntry).key)
	}
}
