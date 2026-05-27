# Producer-Consumer

Pattern separating data production and consumption via a queue.

---

## What is Producer-Consumer?

> Decouple producers from consumers with an intermediate queue.

```
+--------------------------------------------------------------+
|                   Producer-Consumer                           |
|                                                               |
|  Producers              Queue              Consumers          |
|                                                               |
|  +----------+      +------------+       +----------+          |
|  |Producer 1|--+   |            |   +---|Consumer 1|          |
|  +----------+  |   | [item][..] |   |   +----------+          |
|                +-->|    -->     |---+                         |
|  +----------+  |   |            |   |   +----------+          |
|  |Producer 2|--+   +------------+   +---|Consumer 2|          |
|  +----------+                       |   +----------+          |
|                    Bounded Queue    |                         |
|                    (backpressure)   |   +----------+          |
|                                     +---|Consumer 3|          |
|                                         +----------+          |
|                                                               |
|  Decoupling:                                                  |
|  - Producers independent from consumers                       |
|  - Different speeds managed by the queue                      |
|  - Independent scalability                                    |
+--------------------------------------------------------------+
```

**Why:**

- Decouple production/consumption
- Handle speed differences
- Enable buffering

---

## Go Implementation

### Basic Queue with channels

```go
package queue

import (
	"context"
	"sync"
)

// Queue is a producer-consumer queue.
type Queue[T any] struct {
	ch     chan T
	closed bool
	mu     sync.RWMutex
}

// NewQueue creates a new unbounded queue.
func NewQueue[T any](bufferSize int) *Queue[T] {
	return &Queue[T]{
		ch: make(chan T, bufferSize),
	}
}

// Produce sends an item to the queue.
func (q *Queue[T]) Produce(ctx context.Context, item T) error {
	q.mu.RLock()
	defer q.mu.RUnlock()

	if q.closed {
		return fmt.Errorf("queue is closed")
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case q.ch <- item:
		return nil
	}
}

// Consume receives an item from the queue.
func (q *Queue[T]) Consume(ctx context.Context) (T, error) {
	select {
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	case item, ok := <-q.ch:
		if !ok {
			var zero T
			return zero, fmt.Errorf("queue is closed")
		}
		return item, nil
	}
}

// Close closes the queue.
func (q *Queue[T]) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()

	if !q.closed {
		q.closed = true
		close(q.ch)
	}
}

// Size returns the current queue size.
func (q *Queue[T]) Size() int {
	return len(q.ch)
}
```

---

## Multi-Consumer Pattern

```go
package worker

import (
	"context"
	"fmt"
	"sync"
)

// Task represents a unit of work.
type Task[T, R any] struct {
	Input  T
	Result chan<- R
	Err    chan<- error
}

// WorkerPool processes tasks concurrently.
type WorkerPool[T, R any] struct {
	tasks      chan Task[T, R]
	processor  func(context.Context, T) (R, error)
	numWorkers int
	wg         sync.WaitGroup
	ctx        context.Context
	cancel     context.CancelFunc
}

// NewWorkerPool creates a worker pool.
func NewWorkerPool[T, R any](
	numWorkers int,
	queueSize int,
	processor func(context.Context, T) (R, error),
) *WorkerPool[T, R] {
	ctx, cancel := context.WithCancel(context.Background())

	wp := &WorkerPool[T, R]{
		tasks:      make(chan Task[T, R], queueSize),
		processor:  processor,
		numWorkers: numWorkers,
		ctx:        ctx,
		cancel:     cancel,
	}

	// Start workers
	for i := 0; i < numWorkers; i++ {
		wp.wg.Add(1)
		go wp.worker(i)
	}

	return wp
}

// worker processes tasks.
func (wp *WorkerPool[T, R]) worker(id int) {
	defer wp.wg.Done()

	for {
		select {
		case <-wp.ctx.Done():
			return
		case task, ok := <-wp.tasks:
			if !ok {
				return
			}

			result, err := wp.processor(wp.ctx, task.Input)
			if err != nil {
				select {
				case task.Err <- err:
				case <-wp.ctx.Done():
				}
			} else {
				select {
				case task.Result <- result:
				case <-wp.ctx.Done():
				}
			}
		}
	}
}

// Submit submits a task to the pool.
func (wp *WorkerPool[T, R]) Submit(ctx context.Context, input T) (R, error) {
	resultCh := make(chan R, 1)
	errCh := make(chan error, 1)

	task := Task[T, R]{
		Input:  input,
		Result: resultCh,
		Err:    errCh,
	}

	select {
	case <-ctx.Done():
		var zero R
		return zero, ctx.Err()
	case <-wp.ctx.Done():
		var zero R
		return zero, fmt.Errorf("pool is closed")
	case wp.tasks <- task:
	}

	select {
	case <-ctx.Done():
		var zero R
		return zero, ctx.Err()
	case result := <-resultCh:
		return result, nil
	case err := <-errCh:
		var zero R
		return zero, err
	}
}

// Shutdown gracefully stops the pool.
func (wp *WorkerPool[T, R]) Shutdown() {
	close(wp.tasks)
	wp.wg.Wait()
	wp.cancel()
}
```

**Usage:**

```go
package main

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

func main() {
	// Create worker pool for fetching URLs
	pool := NewWorkerPool(
		4, // 4 workers
		100, // queue size
		func(ctx context.Context, url string) (*http.Response, error) {
			req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
			if err != nil {
				return nil, err
			}
			return http.DefaultClient.Do(req)
		},
	)
	defer pool.Shutdown()

	urls := []string{
		"https://api.example.com/1",
		"https://api.example.com/2",
		"https://api.example.com/3",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for _, url := range urls {
		resp, err := pool.Submit(ctx, url)
		if err != nil {
			fmt.Printf("Error fetching %s: %v\n", url, err)
			continue
		}
		resp.Body.Close()
		fmt.Printf("Fetched %s: %d\n", url, resp.StatusCode)
	}
}
```

---

## Distribution Patterns

```
1. Competing Consumers (Work Queue):
   Producer --> [Queue] --> Consumer 1 (processes one message)
                       --> Consumer 2 (processes one message)
   Each message processed by ONE consumer only

2. Publish-Subscribe:
   Producer --> [Topic] --> Consumer 1 (receives all)
                       --> Consumer 2 (receives all)
   Each message processed by ALL consumers

3. Fan-Out:
   Producer --> [Router] --> Queue 1 --> Consumer type A
                        --> Queue 2 --> Consumer type B
   Messages routed by their type
```

### Fan-Out Implementation

```go
package fanout

import (
	"context"
	"sync"
)

// FanOut distributes items to multiple consumers.
type FanOut[T any] struct {
	outputs []chan T
	mu      sync.RWMutex
}

// NewFanOut creates a fan-out distributor.
func NewFanOut[T any]() *FanOut[T] {
	return &FanOut[T]{
		outputs: make([]chan T, 0),
	}
}

// AddConsumer registers a new consumer.
func (f *FanOut[T]) AddConsumer(bufferSize int) <-chan T {
	f.mu.Lock()
	defer f.mu.Unlock()

	ch := make(chan T, bufferSize)
	f.outputs = append(f.outputs, ch)
	return ch
}

// Send broadcasts item to all consumers.
func (f *FanOut[T]) Send(ctx context.Context, item T) error {
	f.mu.RLock()
	defer f.mu.RUnlock()

	for _, out := range f.outputs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case out <- item:
		}
	}

	return nil
}

// Close closes all output channels.
func (f *FanOut[T]) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, out := range f.outputs {
		close(out)
	}
}
```

---

## Complexity and Trade-offs

| Aspect | Value |
|--------|-------|
| Produce | O(1) or O(wait) if bounded |
| Consume | O(1) or O(wait) if empty |
| Memory | O(queue_size) |

### Advantages

- Temporal decoupling
- Spike absorption
- Independent scalability
- Native channels in Go

### Disadvantages

- Added latency
- Queue management complexity
- Possible loss on crash

---

## When to Use

| Situation | Recommended |
|-----------|-------------|
| Different production/consumption speeds | Yes |
| Background async processing | Yes |
| Load spikes | Yes |
| Minimal latency critical | No |

---

## Related Patterns

| Pattern | Relation |
|---------|----------|
| **Thread Pool** | Consumers = pool workers |
| **Buffer** | Queue = buffer |
| **Pipeline** | Chain of producer-consumer |
| **Fan-Out/Fan-In** | Distribution/aggregation |

---

## Sources

- [Go Concurrency Patterns](https://go.dev/blog/pipelines)
- [Enterprise Integration Patterns](https://www.enterpriseintegrationpatterns.com/)
- [Effective Go - Channels](https://go.dev/doc/effective_go#channels)
