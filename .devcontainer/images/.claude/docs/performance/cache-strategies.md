# Cache Strategies

Write and read strategies for cache systems.

---

## Strategy Overview

```
+--------------------------------------------------------------+
|                    Cache Strategies                           |
|                                                               |
|  Read Strategies:          Write Strategies:                  |
|                                                               |
|  +------------------+      +------------------+               |
|  | Cache-Aside      |      | Write-Through    |               |
|  | Read-Through     |      | Write-Behind     |               |
|  +------------------+      | Write-Around     |               |
|                            +------------------+               |
|                                                               |
|  Invalidation:             Eviction:                          |
|                                                               |
|  +------------------+      +------------------+               |
|  | TTL              |      | LRU              |               |
|  | Event-based      |      | LFU              |               |
|  | Version-based    |      | FIFO             |               |
|  +------------------+      +------------------+               |
+--------------------------------------------------------------+
```

---

## Read Strategies

### Cache-Aside (Lazy Loading)

> The application manages the cache explicitly.

```go
package cache

import (
	"context"
	"errors"
)

var ErrNotFound = errors.New("not found")

// Cache represents a cache interface.
type Cache[K comparable, V any] interface {
	Get(ctx context.Context, key K) (V, bool)
	Set(ctx context.Context, key K, value V) error
	Delete(ctx context.Context, key K) error
}

// Database represents a database interface.
type Database[K comparable, V any] interface {
	FindByID(ctx context.Context, id K) (V, error)
	Update(ctx context.Context, id K, data V) error
}

// CacheAsideRepository implements cache-aside pattern.
type CacheAsideRepository[K comparable, V any] struct {
	cache Cache[K, V]
	db    Database[K, V]
}

// NewCacheAsideRepository creates a new cache-aside repository.
func NewCacheAsideRepository[K comparable, V any](
	cache Cache[K, V],
	db Database[K, V],
) *CacheAsideRepository[K, V] {
	return &CacheAsideRepository[K, V]{
		cache: cache,
		db:    db,
	}
}

// Get retrieves data with cache-aside strategy.
func (r *CacheAsideRepository[K, V]) Get(ctx context.Context, id K) (V, error) {
	// 1. Check cache
	if cached, ok := r.cache.Get(ctx, id); ok {
		return cached, nil
	}

	// 2. Load from DB
	data, err := r.db.FindByID(ctx, id)
	if err != nil {
		var zero V
		return zero, err
	}

	// 3. Store in cache
	if err := r.cache.Set(ctx, id, data); err != nil {
		// Log error but return data
	}

	return data, nil
}

// Update writes to DB then invalidates cache.
func (r *CacheAsideRepository[K, V]) Update(ctx context.Context, id K, data V) error {
	if err := r.db.Update(ctx, id, data); err != nil {
		return err
	}
	return r.cache.Delete(ctx, id)
}
```

**Advantages:** Full control, resilient if cache is down
**Disadvantages:** Duplicated code, risk of inconsistency

### Read-Through

> The cache loads automatically from the source.

```go
package cache

import (
	"context"
	"sync"
	"time"
)

// Loader loads data from source.
type Loader[K comparable, V any] func(ctx context.Context, key K) (V, error)

// ReadThroughCache implements read-through caching.
type ReadThroughCache[K comparable, V any] struct {
	cache  map[K]V
	loader Loader[K, V]
	ttl    time.Duration
	mu     sync.RWMutex
}

// NewReadThroughCache creates a new read-through cache.
func NewReadThroughCache[K comparable, V any](
	loader Loader[K, V],
	ttl time.Duration,
) *ReadThroughCache[K, V] {
	return &ReadThroughCache[K, V]{
		cache:  make(map[K]V),
		loader: loader,
		ttl:    ttl,
	}
}

// Get retrieves from cache, loading if missing.
func (c *ReadThroughCache[K, V]) Get(ctx context.Context, key K) (V, error) {
	c.mu.RLock()
	if value, ok := c.cache[key]; ok {
		c.mu.RUnlock()
		return value, nil
	}
	c.mu.RUnlock()

	// Load automatically
	value, err := c.loader(ctx, key)
	if err != nil {
		var zero V
		return zero, err
	}

	c.mu.Lock()
	c.cache[key] = value
	c.mu.Unlock()

	// TTL
	if c.ttl > 0 {
		time.AfterFunc(c.ttl, func() {
			c.mu.Lock()
			delete(c.cache, key)
			c.mu.Unlock()
		})
	}

	return value, nil
}
```

**Advantages:** Centralized logic, transparent
**Disadvantages:** Cache-source coupling

---

## Write Strategies

### Write-Through

> Synchronous write to both cache AND source.

```go
package cache

import (
	"context"
	"sync"
)

// WriteThroughCache implements write-through caching.
type WriteThroughCache[K comparable, V any] struct {
	cache Cache[K, V]
	db    Database[K, V]
}

// NewWriteThroughCache creates a new write-through cache.
func NewWriteThroughCache[K comparable, V any](
	cache Cache[K, V],
	db Database[K, V],
) *WriteThroughCache[K, V] {
	return &WriteThroughCache[K, V]{
		cache: cache,
		db:    db,
	}
}

// Write writes to both cache and database synchronously.
func (c *WriteThroughCache[K, V]) Write(ctx context.Context, key K, value V) error {
	var wg sync.WaitGroup
	var cacheErr, dbErr error

	wg.Go(func() {
		cacheErr = c.cache.Set(ctx, key, value)
	})

	wg.Go(func() {
		dbErr = c.db.Update(ctx, key, value)
	})

	wg.Wait()

	if dbErr != nil {
		return dbErr
	}
	return cacheErr
}

// Read reads from cache (always up-to-date).
func (c *WriteThroughCache[K, V]) Read(ctx context.Context, key K) (V, bool) {
	return c.cache.Get(ctx, key)
}
```

```
Write-Through:
  App --write--> Cache --write--> DB
                   |
                   +--- response only after DB commit
```

**Advantages:** Strong consistency
**Disadvantages:** Doubled write latency

### Write-Behind (Write-Back)

> Asynchronous write to the source.

```go
package cache

import (
	"context"
	"sync"
	"time"
)

// WriteBehindCache implements write-behind caching.
type WriteBehindCache[K comparable, V any] struct {
	cache         Cache[K, V]
	db            Database[K, V]
	pending       map[K]V
	flushInterval time.Duration
	mu            sync.Mutex
	done          chan struct{}
}

// NewWriteBehindCache creates a new write-behind cache.
func NewWriteBehindCache[K comparable, V any](
	cache Cache[K, V],
	db Database[K, V],
	flushInterval time.Duration,
) *WriteBehindCache[K, V] {
	wbc := &WriteBehindCache[K, V]{
		cache:         cache,
		db:            db,
		pending:       make(map[K]V),
		flushInterval: flushInterval,
		done:          make(chan struct{}),
	}

	go wbc.flushLoop()
	return wbc
}

// Write writes to cache immediately, DB asynchronously.
func (c *WriteBehindCache[K, V]) Write(ctx context.Context, key K, value V) error {
	// Immediate cache write
	if err := c.cache.Set(ctx, key, value); err != nil {
		return err
	}

	// Mark for deferred write
	c.mu.Lock()
	c.pending[key] = value
	c.mu.Unlock()

	return nil
}

func (c *WriteBehindCache[K, V]) flushLoop() {
	ticker := time.NewTicker(c.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.flush()
		case <-c.done:
			c.flush()
			return
		}
	}
}

func (c *WriteBehindCache[K, V]) flush() {
	c.mu.Lock()
	if len(c.pending) == 0 {
		c.mu.Unlock()
		return
	}

	entries := make(map[K]V, len(c.pending))
	for k, v := range c.pending {
		entries[k] = v
	}
	c.pending = make(map[K]V)
	c.mu.Unlock()

	// Batch write to DB
	ctx := context.Background()
	for k, v := range entries {
		c.db.Update(ctx, k, v)
	}
}

// Close flushes pending writes and stops the cache.
func (c *WriteBehindCache[K, V]) Close() error {
	close(c.done)
	return nil
}
```

```
Write-Behind:
  App --write--> Cache ---(async)---> DB
         |
         +--- immediate response
```

**Advantages:** Minimal write latency, batching
**Disadvantages:** Risk of data loss, complexity

### Write-Around

> Direct write to DB, cache only on read.

```go
package cache

import "context"

// WriteAroundCache implements write-around caching.
type WriteAroundCache[K comparable, V any] struct {
	cache Cache[K, V]
	db    Database[K, V]
}

// NewWriteAroundCache creates a new write-around cache.
func NewWriteAroundCache[K comparable, V any](
	cache Cache[K, V],
	db Database[K, V],
) *WriteAroundCache[K, V] {
	return &WriteAroundCache[K, V]{
		cache: cache,
		db:    db,
	}
}

// Write writes directly to DB.
func (c *WriteAroundCache[K, V]) Write(ctx context.Context, key K, value V) error {
	// Direct DB write
	if err := c.db.Update(ctx, key, value); err != nil {
		return err
	}
	// Optionally invalidate cache
	return c.cache.Delete(ctx, key)
}

// Read uses cache-aside for reads.
func (c *WriteAroundCache[K, V]) Read(ctx context.Context, key K) (V, error) {
	if cached, ok := c.cache.Get(ctx, key); ok {
		return cached, nil
	}

	value, err := c.db.FindByID(ctx, key)
	if err != nil {
		var zero V
		return zero, err
	}

	c.cache.Set(ctx, key, value)
	return value, nil
}
```

**Advantages:** No cache pollution for rarely read data
**Disadvantages:** Cache miss after write

---

## Eviction Strategies

### LRU (Least Recently Used)

```go
package cache

import (
	"container/list"
	"sync"
)

// LRUCache implements LRU eviction.
type LRUCache[K comparable, V any] struct {
	maxSize int
	cache   map[K]*list.Element
	lru     *list.List
	mu      sync.RWMutex
}

type lruEntry[K comparable, V any] struct {
	key   K
	value V
}

// NewLRUCache creates a new LRU cache.
func NewLRUCache[K comparable, V any](maxSize int) *LRUCache[K, V] {
	return &LRUCache[K, V]{
		maxSize: maxSize,
		cache:   make(map[K]*list.Element),
		lru:     list.New(),
	}
}

// Get retrieves a value and marks it as recently used.
func (c *LRUCache[K, V]) Get(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.cache[key]; ok {
		c.lru.MoveToFront(elem)
		return elem.Value.(*lruEntry[K, V]).value, true
	}

	var zero V
	return zero, false
}

// Set adds or updates a value.
func (c *LRUCache[K, V]) Set(key K, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.cache[key]; ok {
		c.lru.MoveToFront(elem)
		elem.Value.(*lruEntry[K, V]).value = value
		return
	}

	if c.lru.Len() >= c.maxSize {
		// Evict oldest
		oldest := c.lru.Back()
		if oldest != nil {
			c.lru.Remove(oldest)
			delete(c.cache, oldest.Value.(*lruEntry[K, V]).key)
		}
	}

	elem := c.lru.PushFront(&lruEntry[K, V]{key: key, value: value})
	c.cache[key] = elem
}
```

### LFU (Least Frequently Used)

```go
package cache

import "sync"

// LFUCache implements LFU eviction.
type LFUCache[K comparable, V any] struct {
	maxSize int
	cache   map[K]*lfuEntry[V]
	freqMap map[int]map[K]struct{}
	minFreq int
	mu      sync.RWMutex
}

type lfuEntry[V any] struct {
	value V
	freq  int
}

// NewLFUCache creates a new LFU cache.
func NewLFUCache[K comparable, V any](maxSize int) *LFUCache[K, V] {
	return &LFUCache[K, V]{
		maxSize: maxSize,
		cache:   make(map[K]*lfuEntry[V]),
		freqMap: make(map[int]map[K]struct{}),
	}
}

// Get retrieves a value and increments frequency.
func (c *LFUCache[K, V]) Get(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.cache[key]
	if !ok {
		var zero V
		return zero, false
	}

	c.updateFrequency(key, entry)
	entry.freq++
	return entry.value, true
}

func (c *LFUCache[K, V]) updateFrequency(key K, entry *lfuEntry[V]) {
	oldFreq := entry.freq

	if set, ok := c.freqMap[oldFreq]; ok {
		delete(set, key)
		if len(set) == 0 && oldFreq == c.minFreq {
			c.minFreq = oldFreq + 1
		}
	}

	newFreq := oldFreq + 1
	if c.freqMap[newFreq] == nil {
		c.freqMap[newFreq] = make(map[K]struct{})
	}
	c.freqMap[newFreq][key] = struct{}{}
}

// Set adds or updates a value.
func (c *LFUCache[K, V]) Set(key K, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.maxSize <= 0 {
		return
	}

	if entry, ok := c.cache[key]; ok {
		entry.value = value
		c.updateFrequency(key, entry)
		entry.freq++
		return
	}

	if len(c.cache) >= c.maxSize {
		// Evict LFU
		if lfuKeys, ok := c.freqMap[c.minFreq]; ok {
			for k := range lfuKeys {
				delete(c.cache, k)
				delete(lfuKeys, k)
				break
			}
		}
	}

	c.cache[key] = &lfuEntry[V]{value: value, freq: 1}
	if c.freqMap[1] == nil {
		c.freqMap[1] = make(map[K]struct{})
	}
	c.freqMap[1][key] = struct{}{}
	c.minFreq = 1
}
```

---

## Decision Table

| Scenario | Recommended Strategy |
|----------|---------------------|
| Frequent reads, rare writes | Cache-Aside + LRU |
| Critical consistency | Write-Through |
| Critical write performance | Write-Behind |
| Rarely re-read data | Write-Around |
| Predictable working set | LFU |
| Important recent access | LRU |
| Data with natural expiration | TTL |

---

## When to Use

- Data read frequently but rarely modified (user profiles, configurations)
- APIs with high read load requiring latency reduction
- User sessions and temporary authentication tokens
- Results of expensive queries (reports, aggregations, searches)
- Static or semi-static content (pages, assets, metadata)

---

## Related Patterns

| Pattern | Relation |
|---------|----------|
| **Memoization** | Function-level cache |
| **Proxy** | Encapsulates cache access |
| **Circuit Breaker** | Source protection when down |
| **CQRS** | Read/write separation |

---

## Sources

- [Caching Strategies](https://docs.aws.amazon.com/AmazonElastiCache/latest/mem-ug/Strategies.html)
- [Redis Patterns](https://redis.io/docs/manual/patterns/)
- [Facebook TAO](https://www.usenix.org/conference/atc13/technical-sessions/presentation/bronson)
