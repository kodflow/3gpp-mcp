# Copy-on-Write (COW)

Optimization pattern deferring the copy until modification.

---

## What is Copy-on-Write?

> Lazy copy strategy: share data for reads, copy only on write.

```
+--------------------------------------------------------------+
|                    Copy-on-Write                              |
|                                                               |
|  Initial:                     After Write to B:               |
|                                                               |
|  +-------+                    +-------+                       |
|  |   A   |--+                 |   A   |---> Data (original)   |
|  +-------+  |                 +-------+                       |
|             +---> Data                                        |
|  +-------+  |                 +-------+                       |
|  |   B   |--+                 |   B   |---> Data' (copy)      |
|  +-------+                    +-------+                       |
|                                                               |
|  A and B share                 B has its own copy             |
+--------------------------------------------------------------+
```

**Why:**

- Save memory (no copy if no modification)
- Improve performance (deferred copy)
- Snapshot safety (consistent state)
- Efficient sharing between threads

---

## Go Implementation

### Immutable List (COW)

```go
package cow

// ImmutableList is a copy-on-write list.
type ImmutableList[T any] struct {
	data []T
}

// NewImmutableList creates a new immutable list.
func NewImmutableList[T any](initial ...T) *ImmutableList[T] {
	data := make([]T, len(initial))
	copy(data, initial)

	return &ImmutableList[T]{
		data: data,
	}
}

// Get retrieves an element (no copy).
func (l *ImmutableList[T]) Get(index int) T {
	return l.data[index]
}

// Len returns the length (no copy).
func (l *ImmutableList[T]) Len() int {
	return len(l.data)
}

// Set creates a new list with modified value.
func (l *ImmutableList[T]) Set(index int, value T) *ImmutableList[T] {
	newData := make([]T, len(l.data))
	copy(newData, l.data)
	newData[index] = value

	return &ImmutableList[T]{data: newData}
}

// Append creates a new list with appended value.
func (l *ImmutableList[T]) Append(value T) *ImmutableList[T] {
	newData := make([]T, len(l.data)+1)
	copy(newData, l.data)
	newData[len(l.data)] = value

	return &ImmutableList[T]{data: newData}
}

// Filter creates a new filtered list.
func (l *ImmutableList[T]) Filter(predicate func(T) bool) *ImmutableList[T] {
	filtered := make([]T, 0, len(l.data))
	for _, item := range l.data {
		if predicate(item) {
			filtered = append(filtered, item)
		}
	}

	return &ImmutableList[T]{data: filtered}
}

// Map creates a new mapped list.
func (l *ImmutableList[T]) Map(fn func(T) T) *ImmutableList[T] {
	mapped := make([]T, len(l.data))
	for i, item := range l.data {
		mapped[i] = fn(item)
	}

	return &ImmutableList[T]{data: mapped}
}

// ToSlice returns a copy of the underlying slice.
func (l *ImmutableList[T]) ToSlice() []T {
	result := make([]T, len(l.data))
	copy(result, l.data)
	return result
}
```

**Usage:**

```go
package main

import "fmt"

func main() {
	list1 := NewImmutableList(1, 2, 3)
	list2 := list1.Append(4)        // list1 unchanged
	list3 := list2.Set(0, 10)       // list2 unchanged

	fmt.Println(list1.ToSlice())    // [1, 2, 3]
	fmt.Println(list2.ToSlice())    // [1, 2, 3, 4]
	fmt.Println(list3.ToSlice())    // [10, 2, 3, 4]
}
```

---

### Immutable Map

```go
package cow

// ImmutableMap is a copy-on-write map.
type ImmutableMap[K comparable, V any] struct {
	data map[K]V
}

// NewImmutableMap creates a new immutable map.
func NewImmutableMap[K comparable, V any]() *ImmutableMap[K, V] {
	return &ImmutableMap[K, V]{
		data: make(map[K]V),
	}
}

// Get retrieves a value (no copy).
func (m *ImmutableMap[K, V]) Get(key K) (V, bool) {
	val, ok := m.data[key]
	return val, ok
}

// Has checks if key exists (no copy).
func (m *ImmutableMap[K, V]) Has(key K) bool {
	_, ok := m.data[key]
	return ok
}

// Size returns the map size (no copy).
func (m *ImmutableMap[K, V]) Size() int {
	return len(m.data)
}

// Set creates a new map with the key-value pair.
func (m *ImmutableMap[K, V]) Set(key K, value V) *ImmutableMap[K, V] {
	newData := make(map[K]V, len(m.data)+1)
	for k, v := range m.data {
		newData[k] = v
	}
	newData[key] = value

	return &ImmutableMap[K, V]{data: newData}
}

// Delete creates a new map without the key.
func (m *ImmutableMap[K, V]) Delete(key K) *ImmutableMap[K, V] {
	newData := make(map[K]V, len(m.data))
	for k, v := range m.data {
		if k != key {
			newData[k] = v
		}
	}

	return &ImmutableMap[K, V]{data: newData}
}

// Merge creates a new map with merged values.
func (m *ImmutableMap[K, V]) Merge(other *ImmutableMap[K, V]) *ImmutableMap[K, V] {
	newData := make(map[K]V, len(m.data)+len(other.data))

	for k, v := range m.data {
		newData[k] = v
	}

	for k, v := range other.data {
		newData[k] = v
	}

	return &ImmutableMap[K, V]{data: newData}
}
```

---

## COW for Thread Safety (sync.Map alternative)

```go
package cow

import (
	"sync"
	"sync/atomic"
)

// ConcurrentMap is a thread-safe COW map.
type ConcurrentMap[K comparable, V any] struct {
	data atomic.Value // stores *ImmutableMap[K, V]
	mu   sync.Mutex
}

// NewConcurrentMap creates a concurrent COW map.
func NewConcurrentMap[K comparable, V any]() *ConcurrentMap[K, V] {
	cm := &ConcurrentMap[K, V]{}
	cm.data.Store(NewImmutableMap[K, V]())
	return cm
}

// Get retrieves a value (lock-free read).
func (cm *ConcurrentMap[K, V]) Get(key K) (V, bool) {
	current := cm.data.Load().(*ImmutableMap[K, V])
	return current.Get(key)
}

// Set stores a value (synchronized write).
func (cm *ConcurrentMap[K, V]) Set(key K, value V) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	current := cm.data.Load().(*ImmutableMap[K, V])
	updated := current.Set(key, value)
	cm.data.Store(updated)
}

// Delete removes a value (synchronized write).
func (cm *ConcurrentMap[K, V]) Delete(key K) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	current := cm.data.Load().(*ImmutableMap[K, V])
	updated := current.Delete(key)
	cm.data.Store(updated)
}

// Snapshot returns a consistent snapshot.
func (cm *ConcurrentMap[K, V]) Snapshot() *ImmutableMap[K, V] {
	return cm.data.Load().(*ImmutableMap[K, V])
}
```

**Usage:**

```go
package main

import (
	"fmt"
	"sync"
)

func main() {
	cm := NewConcurrentMap[string, int]()

	var wg sync.WaitGroup

	// Concurrent writes
	for i := 0; i < 10; i++ {
		n := i // Capture for closure
		wg.Go(func() { // Go 1.25: handles Add/Done internally
			cm.Set(fmt.Sprintf("key%d", n), n)
		})
	}

	wg.Wait()

	// Lock-free reads
	for i := 0; i < 10; i++ {
		val, ok := cm.Get(fmt.Sprintf("key%d", i))
		if ok {
			fmt.Printf("key%d = %d\n", i, val)
		}
	}

	// Snapshot
	snapshot := cm.Snapshot()
	fmt.Printf("Snapshot size: %d\n", snapshot.Size())
}
```

---

## COW for Snapshots

```go
package snapshot

import (
	"sync"
)

// DocumentStore manages versioned documents.
type DocumentStore[K comparable, V any] struct {
	current  *ImmutableMap[K, V]
	history  []*ImmutableMap[K, V]
	mu       sync.Mutex
}

// NewDocumentStore creates a document store.
func NewDocumentStore[K comparable, V any]() *DocumentStore[K, V] {
	return &DocumentStore[K, V]{
		current: NewImmutableMap[K, V](),
		history: make([]*ImmutableMap[K, V], 0),
	}
}

// CreateSnapshot creates a snapshot of current state.
func (ds *DocumentStore[K, V]) CreateSnapshot() int {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	ds.history = append(ds.history, ds.current)
	return len(ds.history) - 1
}

// Update modifies a document.
func (ds *DocumentStore[K, V]) Update(key K, value V) {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	ds.current = ds.current.Set(key, value)
}

// GetFromSnapshot retrieves from a specific snapshot.
func (ds *DocumentStore[K, V]) GetFromSnapshot(snapshotID int, key K) (V, bool) {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	if snapshotID < 0 || snapshotID >= len(ds.history) {
		var zero V
		return zero, false
	}

	return ds.history[snapshotID].Get(key)
}

// Rollback reverts to a snapshot.
func (ds *DocumentStore[K, V]) Rollback(snapshotID int) error {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	if snapshotID < 0 || snapshotID >= len(ds.history) {
		return fmt.Errorf("invalid snapshot ID")
	}

	ds.current = ds.history[snapshotID]
	ds.history = ds.history[:snapshotID+1]

	return nil
}
```

---

## Typical Use Cases

### 1. Undo/Redo

```go
package undo

// UndoManager manages undo/redo history.
type UndoManager[T any] struct {
	past    []T
	current T
	future  []T
}

// NewUndoManager creates an undo manager.
func NewUndoManager[T any](initial T) *UndoManager[T] {
	return &UndoManager[T]{
		past:    make([]T, 0),
		current: initial,
		future:  make([]T, 0),
	}
}

// Update records a new state.
func (um *UndoManager[T]) Update(newState T) {
	um.past = append(um.past, um.current)
	um.current = newState
	um.future = um.future[:0] // Clear future
}

// Undo reverts to previous state.
func (um *UndoManager[T]) Undo() (T, bool) {
	if len(um.past) == 0 {
		return um.current, false
	}

	um.future = append(um.future, um.current)
	um.current = um.past[len(um.past)-1]
	um.past = um.past[:len(um.past)-1]

	return um.current, true
}

// Redo moves forward in history.
func (um *UndoManager[T]) Redo() (T, bool) {
	if len(um.future) == 0 {
		return um.current, false
	}

	um.past = append(um.past, um.current)
	um.current = um.future[len(um.future)-1]
	um.future = um.future[:len(um.future)-1]

	return um.current, true
}

// Current returns the current state.
func (um *UndoManager[T]) Current() T {
	return um.current
}
```

---

## Advantages and Disadvantages

### Advantages

| Advantage | Explanation |
|-----------|-------------|
| Memory | No copy if no modification |
| Read performance | Lock-free reads with atomic |
| Free snapshots | Just copy the pointers |
| Thread-safe | Immutable references |
| Easy undo/redo | Keep old versions |

### Disadvantages

| Disadvantage | Mitigation |
|--------------|------------|
| Write cost | Batch the modifications |
| GC pressure | Use sync.Pool for buffers |
| Complexity | Encapsulate in simple API |

---

## When to Use

- Frequent reads, rare writes (read-heavy workloads)
- Need consistent snapshots without blocking readers
- History/versioning with undo/redo
- Data sharing between threads without read locks
- Persistent/immutable data structures

---

## Related Patterns

| Pattern | Relation |
|---------|----------|
| **Immutability** | Foundation of COW |
| **Structural Sharing** | COW optimization |
| **Snapshot** | Primary use case |
| **Flyweight** | Similar data sharing |
| **sync.Map** | Alternative for simple cases |

---

## Sources

- [Copy-on-write - Wikipedia](https://en.wikipedia.org/wiki/Copy-on-write)
- [Go sync/atomic](https://pkg.go.dev/sync/atomic)
- [Persistent Data Structures](https://en.wikipedia.org/wiki/Persistent_data_structure)
