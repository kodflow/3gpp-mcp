# Object Pool

Resource management pattern reusing expensive objects instead of recreating them.

---

## What is the Object Pool?

> Pre-allocate and reuse objects to avoid the cost of creation/destruction.

```
+-------------------------------------------------------------+
|                     Object Pool                             |
|                                                             |
|  acquire()                         release(obj)             |
|      |                                  |                   |
|      v                                  v                   |
|  +-------+    +-------+    +-------+    +-------+           |
|  |  obj  |    |  obj  |    |  obj  |    |  obj  |           |
|  | (used)|    |(avail)|    |(avail)|    | (used)|           |
|  +-------+    +-------+    +-------+    +-------+           |
|      ^            |            |            ^               |
|      |            +-----+------+            |               |
|      |                  |                   |               |
|  Client A          Available           Client B             |
|                                                             |
+-------------------------------------------------------------+
```

**Why:**

- Avoid expensive allocations (GC pressure)
- Limit system resources (connections, threads)
- Reduce acquisition latency

---

## Go Implementation

```go
package pool

import (
	"context"
	"errors"
	"sync"
)

var (
	ErrPoolExhausted = errors.New("pool exhausted")
	ErrNotFromPool   = errors.New("object not from this pool")
)

// Poolable defines objects that can be pooled.
type Poolable interface {
	Reset() error
}

// ObjectPool manages a pool of reusable objects.
type ObjectPool[T Poolable] struct {
	pool    *sync.Pool
	inUse   map[*T]struct{}
	maxSize int
	mu      sync.Mutex
}

// New creates a new ObjectPool.
func New[T Poolable](factory func() T, maxSize int) *ObjectPool[T] {
	return &ObjectPool[T]{
		pool: &sync.Pool{
			New: func() any {
				obj := factory()
				return &obj
			},
		},
		inUse:   make(map[*T]struct{}),
		maxSize: maxSize,
	}
}

// Acquire gets an object from the pool.
func (op *ObjectPool[T]) Acquire(ctx context.Context) (*T, error) {
	op.mu.Lock()
	defer op.mu.Unlock()

	if op.maxSize > 0 && len(op.inUse) >= op.maxSize {
		return nil, ErrPoolExhausted
	}

	obj := op.pool.Get().(*T)
	op.inUse[obj] = struct{}{}
	return obj, nil
}

// Release returns an object to the pool.
func (op *ObjectPool[T]) Release(obj *T) error {
	op.mu.Lock()
	defer op.mu.Unlock()

	if _, ok := op.inUse[obj]; !ok {
		return ErrNotFromPool
	}

	if err := (*obj).Reset(); err != nil {
		delete(op.inUse, obj)
		return err
	}

	delete(op.inUse, obj)
	op.pool.Put(obj)
	return nil
}

// WithObject executes a function with a pooled object.
func (op *ObjectPool[T]) WithObject(ctx context.Context, fn func(*T) error) error {
	obj, err := op.Acquire(ctx)
	if err != nil {
		return err
	}
	defer op.Release(obj)
	return fn(obj)
}

// Stats returns pool statistics.
func (op *ObjectPool[T]) Stats() (inUse, total int) {
	op.mu.Lock()
	defer op.mu.Unlock()
	return len(op.inUse), len(op.inUse)
}
```

---

## Usage Example

```go
package main

import (
	"context"
	"fmt"
)

// ReusableBuffer is a poolable buffer.
type ReusableBuffer struct {
	data     []byte
	position int
}

// NewReusableBuffer creates a new reusable buffer.
func NewReusableBuffer(size int) *ReusableBuffer {
	return &ReusableBuffer{
		data: make([]byte, size),
	}
}

// Write writes bytes to the buffer.
func (rb *ReusableBuffer) Write(bytes []byte) error {
	if rb.position+len(bytes) > len(rb.data) {
		return fmt.Errorf("buffer overflow")
	}
	copy(rb.data[rb.position:], bytes)
	rb.position += len(bytes)
	return nil
}

// Reset resets the buffer for reuse.
func (rb *ReusableBuffer) Reset() error {
	rb.position = 0
	return nil
}

// Usage example
func main() {
	bufferPool := pool.New(
		func() *ReusableBuffer { return NewReusableBuffer(8192) },
		100,
	)

	ctx := context.Background()

	// Use buffer with automatic cleanup
	err := bufferPool.WithObject(ctx, func(buffer *ReusableBuffer) error {
		data := []byte("hello world")
		return buffer.Write(data)
	})

	if err != nil {
		panic(err)
	}
}
```

---

## Complexity and Trade-offs

| Aspect | Value |
|--------|-------|
| Acquire time complexity | O(1) |
| Release time complexity | O(1) |
| Memory | O(maxSize) pre-allocated |

### Advantages

- Reduced allocations/GC
- Predictable latency
- Resource control

### Disadvantages

- Memory reserved even if unused
- Lifecycle management complexity
- Leak risk if release is forgotten

---

## When to Use

| Situation | Recommended |
|-----------|------------|
| Expensive objects to create | Yes |
| High frequency creation/destruction | Yes |
| Limited system resources | Yes |
| Lightweight and simple objects | No |
| Objects with complex state | Caution |

---

## Related Patterns

| Pattern | Relation |
|---------|----------|
| **Flyweight** | State sharing, no acquire/release cycle |
| **Singleton** | Single instance vs pool of instances |
| **Connection Pool** | Specialization for connections |
| **Factory** | Creates pool objects |

---

## Sources

- [Game Programming Patterns - Object Pool](https://gameprogrammingpatterns.com/object-pool.html)
- [Apache Commons Pool](https://commons.apache.org/proper/commons-pool/)
