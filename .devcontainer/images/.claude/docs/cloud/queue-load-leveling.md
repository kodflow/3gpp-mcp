# Queue-Based Load Leveling Pattern

> Use a queue as a buffer to smooth out traffic spikes.

## Principle

```
                    ┌─────────────────────────────────────────────┐
                    │          QUEUE LOAD LEVELING                 │
                    └─────────────────────────────────────────────┘

  WITHOUT QUEUE (spikes saturate the service):
                                           ┌─────────┐
  ████████████                            │ Service │
  ██  PEAK  ██ ─────────────────────────▶ │ OVERLOAD│
  ████████████                            │   !!!   │
       │                                   └─────────┘
       │ Max capacity
       ▼
  ═══════════════

  WITH QUEUE (smoothed load):
                    ┌─────────────┐        ┌─────────┐
  ████████████      │             │        │ Service │
  ██  PEAK  ██ ───▶ │    QUEUE    │ ─────▶ │ Stable  │
  ████████████      │   (buffer)  │        │  Load   │
                    └─────────────┘        └─────────┘
                          │                     │
  Incoming load           │    Constant rate    │
  ════════════════════════════════════════════════
```

## Pattern Comparison

```
  INPUT RATE        QUEUE DEPTH         OUTPUT RATE
       │                 │                   │
  100  │  ████           │    ████           │
       │  ██████         │      ████████     │ ════════
   50  │    ████████     │        ████████   │ Constant
       │      ████       │          ████     │
    0  └──────────────   └──────────────     └──────────
       Time              Time                Time
```

## Go Example

```go
package queueloadleveling

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Task represents a task to be processed.
type Task struct {
	ID        string
	Type      string
	Payload   interface{}
	CreatedAt time.Time
	Attempts  int
}

// QueueService defines queue operations.
type QueueService interface {
	Push(ctx context.Context, task *Task) error
	Pop(ctx context.Context) (*Task, error)
	Length(ctx context.Context) (int, error)
}

// LoadLevelingQueue manages task queuing with load leveling.
type LoadLevelingQueue struct {
	queue          QueueService
	maxConcurrent  int
	processingDelay time.Duration
}

// NewLoadLevelingQueue creates a new LoadLevelingQueue.
func NewLoadLevelingQueue(queue QueueService, maxConcurrent int, processingDelay time.Duration) *LoadLevelingQueue {
	return &LoadLevelingQueue{
		queue:          queue,
		maxConcurrent:  maxConcurrent,
		processingDelay: processingDelay,
	}
}

// Enqueue adds a task to the queue.
func (llq *LoadLevelingQueue) Enqueue(ctx context.Context, task *Task) error {
	if err := llq.queue.Push(ctx, task); err != nil {
		return fmt.Errorf("enqueuing task: %w", err)
	}

	depth, _ := llq.queue.Length(ctx)
	fmt.Printf("Task %s queued. Queue depth: %d
", task.ID, depth)

	return nil
}

// Depth returns the current queue depth.
func (llq *LoadLevelingQueue) Depth(ctx context.Context) (int, error) {
	return llq.queue.Length(ctx)
}

// LeveledConsumer processes tasks with controlled concurrency.
type LeveledConsumer struct {
	queue           QueueService
	handler         func(context.Context, *Task) error
	maxConcurrent   int
	pollInterval    time.Duration
	running         bool
	active          int
	mu              sync.Mutex
	wg              sync.WaitGroup
}

// NewLeveledConsumer creates a new LeveledConsumer.
func NewLeveledConsumer(
	queue QueueService,
	handler func(context.Context, *Task) error,
	maxConcurrent int,
	pollInterval time.Duration,
) *LeveledConsumer {
	return &LeveledConsumer{
		queue:         queue,
		handler:       handler,
		maxConcurrent: maxConcurrent,
		pollInterval:  pollInterval,
	}
}

// Start starts the consumer.
func (lc *LeveledConsumer) Start(ctx context.Context) error {
	lc.mu.Lock()
	lc.running = true
	lc.mu.Unlock()

	ticker := time.NewTicker(lc.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			lc.Stop()
			lc.wg.Wait()
			return ctx.Err()
		case <-ticker.C:
			lc.processAvailable(ctx)
		}
	}
}

func (lc *LeveledConsumer) processAvailable(ctx context.Context) {
	lc.mu.Lock()
	running := lc.running
	active := lc.active
	lc.mu.Unlock()

	if !running {
		return
	}

	// Process up to maxConcurrent tasks
	for active < lc.maxConcurrent {
		task, err := lc.queue.Pop(ctx)
		if err != nil || task == nil {
			break
		}

		lc.mu.Lock()
		lc.active++
		active = lc.active
		lc.mu.Unlock()

		lc.wg.Go(func() { // Go 1.25: handles Add/Done internally
			lc.processTask(ctx, task)
		})
	}
}

func (lc *LeveledConsumer) processTask(ctx context.Context, task *Task) {
	defer func() {
		lc.mu.Lock()
		lc.active--
		lc.mu.Unlock()
	}()

	if err := lc.handler(ctx, task); err != nil {
		fmt.Printf("Task %s failed: %v
", task.ID, err)

		// Optionally re-queue for retry
		task.Attempts++
		if task.Attempts < 3 {
			lc.queue.Push(ctx, task)
		}
	}
}

// Stop stops the consumer.
func (lc *LeveledConsumer) Stop() {
	lc.mu.Lock()
	lc.running = false
	lc.mu.Unlock()
}
```

## Implementation with rate limiting

```go
// This example follows the same idiomatic Go patterns
// as the main example above.
// Specific implementation based on interfaces and
// standard Go conventions.
```

## Queue-based auto-scaling

```go
// This example follows the same idiomatic Go patterns
// as the main example above.
// Specific implementation based on interfaces and
// standard Go conventions.
```

## Key Metrics

```go
// This example follows the same idiomatic Go patterns
// as the main example above.
// Specific implementation based on interfaces and
// standard Go conventions.
```

## Cloud Services

| Service | Provider | Characteristics |
|---------|----------|-----------------|
| SQS | AWS | Serverless, auto-scale, 14d retention |
| Azure Queue | Azure | Integrated with Functions, 7d retention |
| Cloud Tasks | GCP | HTTP targets, scheduling |
| RabbitMQ | Self-hosted | Advanced features, clustering |
| Redis Streams | Redis | Ultra-fast, persistence |

## When to Use

| Situation | Recommended |
|-----------|-------------|
| Predictable traffic spikes | Yes |
| Decouple producer/consumer | Yes |
| Slow downstream service | Yes |
| Critical real-time latency | No (adds delay) |
| Strict order required | With guaranteed FIFO |

## Related Patterns

| Pattern | Relation |
|---------|----------|
| Priority Queue | Processing by importance |
| Competing Consumers | Parallelization |
| Throttling | Rate limiting |
| Circuit Breaker | If consumer fails |

## Sources

- [Microsoft - Queue-Based Load Leveling](https://learn.microsoft.com/en-us/azure/architecture/patterns/queue-based-load-leveling)
- [AWS SQS Best Practices](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-best-practices.html)
- [Martin Fowler - Messaging](https://martinfowler.com/articles/integration-patterns.html)
