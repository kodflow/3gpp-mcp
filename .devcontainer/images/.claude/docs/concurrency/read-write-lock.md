# Read-Write Lock

Pattern allowing multiple simultaneous readers but a single exclusive writer.

---

## What is a Read-Write Lock?

> Optimizes concurrent access by allowing parallel reads while guaranteeing exclusive writes.

```
+--------------------------------------------------------------+
|                    Read-Write Lock                            |
|                                                               |
|  Read Mode:                    Write Mode:                   |
|                                                               |
|  +--------+                    +--------+                     |
|  |Resource|                    |Resource|                     |
|  +---+----+                    +---+----+                     |
|      |                             |                          |
|  +---+---+---+                     |                          |
|  |   |   |   |                     |                          |
|  R1  R2  R3  R4               W (exclusive)                   |
|                                                               |
|  Rules:                                                       |
|  - Multiple readers in parallel OK                            |
|  - Only one writer at a time                                  |
|  - Writer blocks all readers                                  |
|  - Readers block writers                                      |
|                                                               |
|  Timeline:                                                    |
|  R1 ====                                                      |
|  R2    ====                                                   |
|  W        ====  (waits for reads to finish)                   |
|  R3            ====  (waits for write to finish)              |
+--------------------------------------------------------------+
```

**Why:**

- Frequent reads, rare writes
- Maximize read throughput
- Guarantee write consistency

---

## Go Implementation

### ReadWriteLock with sync.RWMutex

```go
package rwlock

import (
	"context"
	"sync"
)

// RWLock wraps sync.RWMutex with context support.
type RWLock struct {
	mu sync.RWMutex
}

// NewRWLock creates a new read-write lock.
func NewRWLock() *RWLock {
	return &RWLock{}
}

// RLock acquires a read lock.
func (rw *RWLock) RLock() {
	rw.mu.RLock()
}

// RUnlock releases a read lock.
func (rw *RWLock) RUnlock() {
	rw.mu.RUnlock()
}

// Lock acquires a write lock.
func (rw *RWLock) Lock() {
	rw.mu.Lock()
}

// Unlock releases a write lock.
func (rw *RWLock) Unlock() {
	rw.mu.Unlock()
}

// WithRead executes fn with a read lock.
func (rw *RWLock) WithRead(fn func() error) error {
	rw.mu.RLock()
	defer rw.mu.RUnlock()
	return fn()
}

// WithWrite executes fn with a write lock.
func (rw *RWLock) WithWrite(fn func() error) error {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	return fn()
}
```

---

## Use Case: Thread-Safe Cache

```go
package cache

import (
	"context"
	"sync"
)

// ThreadSafeCache is a concurrent-safe cache.
type ThreadSafeCache[K comparable, V any] struct {
	data map[K]V
	mu   sync.RWMutex
}

// NewThreadSafeCache creates a new thread-safe cache.
func NewThreadSafeCache[K comparable, V any]() *ThreadSafeCache[K, V] {
	return &ThreadSafeCache[K, V]{
		data: make(map[K]V),
	}
}

// Get retrieves a value from the cache.
func (c *ThreadSafeCache[K, V]) Get(key K) (V, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	val, ok := c.data[key]
	return val, ok
}

// Set stores a value in the cache.
func (c *ThreadSafeCache[K, V]) Set(key K, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.data[key] = value
}

// GetOrSet retrieves or creates a value.
func (c *ThreadSafeCache[K, V]) GetOrSet(
	ctx context.Context,
	key K,
	factory func(context.Context) (V, error),
) (V, error) {
	// Try read first
	c.mu.RLock()
	val, ok := c.data[key]
	c.mu.RUnlock()

	if ok {
		return val, nil
	}

	// Upgrade to write lock
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if val, ok := c.data[key]; ok {
		return val, ok
	}

	// Create new value
	val, err := factory(ctx)
	if err != nil {
		var zero V
		return zero, err
	}

	c.data[key] = val
	return val, nil
}

// Delete removes a value from the cache.
func (c *ThreadSafeCache[K, V]) Delete(key K) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	_, ok := c.data[key]
	delete(c.data, key)
	return ok
}

// Size returns the number of cached items.
func (c *ThreadSafeCache[K, V]) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return len(c.data)
}

// Clear removes all items.
func (c *ThreadSafeCache[K, V]) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.data = make(map[K]V)
}
```

**Usage:**

```go
package main

import (
	"context"
	"fmt"
	"time"
)

func main() {
	cache := NewThreadSafeCache[string, string]()

	// Concurrent writes
	for i := 0; i < 10; i++ {
		i := i
		go cache.Set(fmt.Sprintf("key%d", i), fmt.Sprintf("value%d", i))
	}

	time.Sleep(100 * time.Millisecond)

	// Concurrent reads
	for i := 0; i < 10; i++ {
		i := i
		go func() {
			val, ok := cache.Get(fmt.Sprintf("key%d", i))
			if ok {
				fmt.Printf("key%d = %s\n", i, val)
			}
		}()
	}

	time.Sleep(100 * time.Millisecond)
	fmt.Printf("Cache size: %d\n", cache.Size())
}
```

---

## Strategy Comparison

```
Reader Preference (sync.RWMutex default):
  Readers: ========    ========    ========
  Writers:         ====        ====
  (Writers can starve if reads are continuous)

Writer Preference:
  Readers: ====              ====
  Writers:     ====    ====
  (Readers wait if writers are queued)

Fair (FIFO):
  Queue:  R1, R2, W1, R3, R4, W2
  Exec:   R1 R2  W1  R3 R4  W2
  (Arrival order respected)
```

---

## Complexity and Trade-offs

| Operation | Complexity |
|-----------|------------|
| RLock (no contention) | O(1) |
| Lock (no contention) | O(1) |
| Unlock | O(1) |

### Advantages

- Maximized read throughput
- Guaranteed write consistency
- Better than mutex for read-heavy workloads
- Native in Go stdlib

### Disadvantages

- More complex than simple mutex
- Risk of starvation (reader or writer)
- Overhead if few reads

---

## When to Use

| Situation | Recommended |
|-----------|-------------|
| Reads >> Writes | Yes |
| In-memory cache | Yes |
| Shared configuration | Yes |
| Frequent writes | No (mutex suffices) |
| Short operations | No (overhead) |

---

## Related Patterns

| Pattern | Relation |
|---------|----------|
| **Mutex** | Special case with 1 reader/writer |
| **Semaphore** | RWLock = specialized Semaphore |
| **Copy-on-Write** | Lock-free alternative |
| **sync.Map** | Alternative for simple cases |

---

## Sources

- [Go sync.RWMutex](https://pkg.go.dev/sync#RWMutex)
- [Wikipedia - Readers-Writer Lock](https://en.wikipedia.org/wiki/Readers%E2%80%93writer_lock)
- [Effective Go - Share by Communicating](https://go.dev/doc/effective_go#sharing)
