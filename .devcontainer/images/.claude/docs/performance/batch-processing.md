# Batch Processing

Pattern for grouping operations to reduce overhead.

---

## What is Batch Processing?

> Collect multiple operations and execute them all at once.

```
+--------------------------------------------------------------+
|                    Batch Processing                           |
|                                                               |
|  Without batch:  Op1 --> DB                                   |
|                  Op2 --> DB                                   |
|                  Op3 --> DB     (3 round-trips)               |
|                                                               |
|  With batch:     Op1 -+                                       |
|                  Op2 -+--> [Batch] --> DB  (1 round-trip)     |
|                  Op3 -+                                       |
|                                                               |
|  Timeline:                                                    |
|                                                               |
|  add() add() add()                  flush()                   |
|    |     |     |                       |                      |
|    v     v     v                       v                      |
|  +---+ +---+ +---+                 +-------+                  |
|  | 1 | | 2 | | 3 | === batch ===> | 1,2,3 | --> process()    |
|  +---+ +---+ +---+                 +-------+                  |
+--------------------------------------------------------------+
```

**Why:**

- Reduce network/DB round-trips
- Amortize fixed costs (connection, headers)
- Optimize throughput

---

## Go Implementation

### Basic BatchProcessor

```go
package batch

import (
	"context"
	"sync"
	"time"
)

// Processor processes a batch of items.
type Processor[T any] func(context.Context, []T) error

// Options configure batch processing.
type Options struct {
	MaxSize int
	MaxWait time.Duration
}

// BatchProcessor collects and processes items in batches.
type BatchProcessor[T any] struct {
	processor  Processor[T]
	opts       Options
	batch      []T
	timer      *time.Timer
	processing bool
	mu         sync.Mutex
	done       chan struct{}
}

// New creates a new batch processor.
func New[T any](processor Processor[T], opts Options) *BatchProcessor[T] {
	return &BatchProcessor[T]{
		processor: processor,
		opts:      opts,
		batch:     make([]T, 0, opts.MaxSize),
		done:      make(chan struct{}),
	}
}

// Add adds an item to the batch.
func (bp *BatchProcessor[T]) Add(item T) {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	bp.batch = append(bp.batch, item)

	if len(bp.batch) >= bp.opts.MaxSize {
		go bp.Flush()
	} else if bp.timer == nil {
		bp.timer = time.AfterFunc(bp.opts.MaxWait, func() {
			bp.Flush()
		})
	}
}

// Flush processes the current batch.
func (bp *BatchProcessor[T]) Flush() {
	bp.mu.Lock()

	if bp.timer != nil {
		bp.timer.Stop()
		bp.timer = nil
	}

	if len(bp.batch) == 0 || bp.processing {
		bp.mu.Unlock()
		return
	}

	bp.processing = true
	items := bp.batch
	bp.batch = make([]T, 0, bp.opts.MaxSize)
	bp.mu.Unlock()

	ctx := context.Background()
	if err := bp.processor(ctx, items); err != nil {
		// Log error
	}

	bp.mu.Lock()
	bp.processing = false
	bp.mu.Unlock()
}

// Pending returns the number of pending items.
func (bp *BatchProcessor[T]) Pending() int {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	return len(bp.batch)
}

// Close flushes remaining items and stops the processor.
func (bp *BatchProcessor[T]) Close() error {
	bp.Flush()
	close(bp.done)
	return nil
}

// Usage
// logBatcher := batch.New(
//     func(ctx context.Context, entries []LogEntry) error {
//         return db.Logs.InsertMany(ctx, entries)
//     },
//     batch.Options{MaxSize: 100, MaxWait: 1 * time.Second},
// )
//
// logBatcher.Add(LogEntry{Level: "info", Message: "User logged in"})
```

### BatchProcessor with Results

```go
package batch

import (
	"context"
	"sync"
	"time"
)

// ProcessorWithResults processes items and returns results.
type ProcessorWithResults[TInput, TResult any] func(context.Context, []TInput) ([]TResult, error)

// BatchItem holds an item and its completion channel.
type BatchItem[TInput, TResult any] struct {
	input   TInput
	resultC chan Result[TResult]
}

// Result holds a result or error.
type Result[T any] struct {
	Value T
	Error error
}

// BatchProcessorWithResults processes items and returns individual results.
type BatchProcessorWithResults[TInput, TResult any] struct {
	processor ProcessorWithResults[TInput, TResult]
	opts      Options
	batch     []BatchItem[TInput, TResult]
	timer     *time.Timer
	mu        sync.Mutex
}

// NewWithResults creates a new batch processor that returns results.
func NewWithResults[TInput, TResult any](
	processor ProcessorWithResults[TInput, TResult],
	opts Options,
) *BatchProcessorWithResults[TInput, TResult] {
	return &BatchProcessorWithResults[TInput, TResult]{
		processor: processor,
		opts:      opts,
		batch:     make([]BatchItem[TInput, TResult], 0, opts.MaxSize),
	}
}

// Add adds an item and returns a result channel.
func (bp *BatchProcessorWithResults[TInput, TResult]) Add(
	ctx context.Context,
	input TInput,
) (TResult, error) {
	resultC := make(chan Result[TResult], 1)

	bp.mu.Lock()
	bp.batch = append(bp.batch, BatchItem[TInput, TResult]{
		input:   input,
		resultC: resultC,
	})

	if len(bp.batch) >= bp.opts.MaxSize {
		bp.mu.Unlock()
		go bp.flush()
	} else {
		if bp.timer == nil {
			bp.timer = time.AfterFunc(bp.opts.MaxWait, func() {
				bp.flush()
			})
		}
		bp.mu.Unlock()
	}

	select {
	case result := <-resultC:
		return result.Value, result.Error
	case <-ctx.Done():
		var zero TResult
		return zero, ctx.Err()
	}
}

func (bp *BatchProcessorWithResults[TInput, TResult]) flush() {
	bp.mu.Lock()

	if bp.timer != nil {
		bp.timer.Stop()
		bp.timer = nil
	}

	if len(bp.batch) == 0 {
		bp.mu.Unlock()
		return
	}

	items := bp.batch
	bp.batch = make([]BatchItem[TInput, TResult], 0, bp.opts.MaxSize)
	bp.mu.Unlock()

	inputs := make([]TInput, len(items))
	for i, item := range items {
		inputs[i] = item.input
	}

	ctx := context.Background()
	results, err := bp.processor(ctx, inputs)

	if err != nil {
		// Send error to all
		for _, item := range items {
			var zero TResult
			item.resultC <- Result[TResult]{Value: zero, Error: err}
			close(item.resultC)
		}
		return
	}

	// Send individual results
	for i, item := range items {
		item.resultC <- Result[TResult]{Value: results[i], Error: nil}
		close(item.resultC)
	}
}

// Usage - DataLoader pattern
// userLoader := batch.NewWithResults(
//     func(ctx context.Context, ids []string) ([]*User, error) {
//         users, err := db.Users.FindByIDs(ctx, ids)
//         if err != nil {
//             return nil, err
//         }
//         // Maintain order
//         result := make([]*User, len(ids))
//         userMap := make(map[string]*User)
//         for _, u := range users {
//             userMap[u.ID] = u
//         }
//         for i, id := range ids {
//             result[i] = userMap[id]
//         }
//         return result, nil
//     },
//     batch.Options{MaxSize: 100, MaxWait: 10 * time.Millisecond},
// )
//
// user1, err := userLoader.Add(ctx, "user-1")
// user2, err := userLoader.Add(ctx, "user-2")
```

---

## Batching Strategies

### 1. Time-based

```go
// Flush every N milliseconds
ticker := time.NewTicker(1 * time.Second)
go func() {
	for range ticker.C {
		batcher.Flush()
	}
}()
```

### 2. Size-based

```go
// Flush when batch reaches N items
if len(batch) >= maxSize {
	flush()
}
```

### 3. Hybrid (recommended)

```go
// Flush on first of: maxSize or maxWait
func (bp *BatchProcessor[T]) Add(item T) {
	bp.batch = append(bp.batch, item)

	if len(bp.batch) >= bp.maxSize {
		bp.Flush() // Size trigger
	} else if bp.timer == nil {
		bp.timer = time.AfterFunc(bp.maxWait, func() {
			bp.Flush() // Time trigger
		})
	}
}
```

### 4. Backpressure

```go
package batch

import (
	"context"
	"sync"
)

// BackpressureBatcher handles backpressure with queue.
type BackpressureBatcher[T any] struct {
	processor  Processor[T]
	maxSize    int
	batch      []T
	queue      [][]T
	processing bool
	mu         sync.Mutex
}

// NewBackpressure creates a backpressure-aware batcher.
func NewBackpressure[T any](processor Processor[T], maxSize int) *BackpressureBatcher[T] {
	return &BackpressureBatcher[T]{
		processor: processor,
		maxSize:   maxSize,
		batch:     make([]T, 0, maxSize),
		queue:     make([][]T, 0),
	}
}

// Add adds an item with backpressure handling.
func (bb *BackpressureBatcher[T]) Add(ctx context.Context, item T) error {
	bb.mu.Lock()
	bb.batch = append(bb.batch, item)

	if len(bb.batch) >= bb.maxSize {
		items := bb.batch
		bb.batch = make([]T, 0, bb.maxSize)

		if bb.processing {
			// Queue if already processing
			bb.queue = append(bb.queue, items)
			bb.mu.Unlock()
			return nil
		}

		bb.mu.Unlock()
		return bb.processWithQueue(ctx, items)
	}

	bb.mu.Unlock()
	return nil
}

func (bb *BackpressureBatcher[T]) processWithQueue(ctx context.Context, items []T) error {
	bb.mu.Lock()
	bb.processing = true
	bb.mu.Unlock()

	if err := bb.processor(ctx, items); err != nil {
		bb.mu.Lock()
		bb.processing = false
		bb.mu.Unlock()
		return err
	}

	for {
		bb.mu.Lock()
		if len(bb.queue) == 0 {
			bb.processing = false
			bb.mu.Unlock()
			break
		}

		next := bb.queue[0]
		bb.queue = bb.queue[1:]
		bb.mu.Unlock()

		if err := bb.processor(ctx, next); err != nil {
			bb.mu.Lock()
			bb.processing = false
			bb.mu.Unlock()
			return err
		}
	}

	return nil
}
```

---

## Use Cases

```go
package examples

import (
	"context"
	"time"
)

// 1. Bulk DB insertion
func insertBatcher() {
	batcher := batch.New(
		func(ctx context.Context, records []Record) error {
			return db.Collection.InsertMany(ctx, records)
		},
		batch.Options{MaxSize: 1000, MaxWait: 100 * time.Millisecond},
	)
	_ = batcher
}

// 2. Email sending
func emailBatcher() {
	batcher := batch.New(
		func(ctx context.Context, emails []Email) error {
			return emailService.SendBulk(ctx, emails)
		},
		batch.Options{MaxSize: 50, MaxWait: 5 * time.Second},
	)
	_ = batcher
}

// 3. Metrics/Analytics
func metricsBatcher() {
	batcher := batch.New(
		func(ctx context.Context, metrics []Metric) error {
			return analytics.TrackBatch(ctx, metrics)
		},
		batch.Options{MaxSize: 100, MaxWait: 10 * time.Second},
	)
	_ = batcher
}

type Record struct{}
type Email struct{}
type Metric struct{}

var (
	db            interface{ Collection interface{ InsertMany(context.Context, []Record) error } }
	emailService  interface{ SendBulk(context.Context, []Email) error }
	analytics     interface{ TrackBatch(context.Context, []Metric) error }
)
```

---

## Complexity and Trade-offs

| Aspect | Value |
|--------|-------|
| Added latency | maxWait (worst case) |
| Round-trip reduction | ~N/maxSize |
| Memory | O(maxSize) |

### Advantages

- Improved throughput
- Fewer connections/requests
- Better network utilization

### Disadvantages

- Added latency
- Partial error complexity
- Risk of data loss if crash before flush

---

## When to Use

- Bulk database insertions (bulk insert)
- Mass email or notification sending (email marketing, alerts)
- Metrics and analytics collection (aggregation before sending)
- Data synchronization between systems (ETL, data pipelines)
- High-frequency event processing (logs, IoT, streaming)

---

## Related Patterns

| Pattern | Relation |
|---------|----------|
| **Buffer** | Similar temporary storage |
| **DataLoader** | Batch + cache for GraphQL |
| **Producer-Consumer** | Queue between production and processing |
| **Debounce** | Group by time, not by count |

---

## Sources

- [DataLoader](https://github.com/graphql/dataloader)
- [Batch Processing - Enterprise Patterns](https://www.enterpriseintegrationpatterns.com/patterns/messaging/BatchSequence.html)
