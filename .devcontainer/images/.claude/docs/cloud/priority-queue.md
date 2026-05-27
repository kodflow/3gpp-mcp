# Priority Queue Pattern

> Process messages according to their priority rather than their arrival order.

## Principle

```
                    ┌─────────────────────────────────────────────┐
                    │              PRIORITY QUEUE                  │
                    └─────────────────────────────────────────────┘

  Standard FIFO:
  ┌───┬───┬───┬───┬───┐
  │ 1 │ 2 │ 3 │ 4 │ 5 │ ──▶ Processing: 1, 2, 3, 4, 5
  └───┴───┴───┴───┴───┘

  Priority Queue:
  ┌─────────────────────────────────────┐
  │  HIGH   │ ██ ██ ██                  │ ──▶ Processed first
  ├─────────┼───────────────────────────┤
  │  MEDIUM │ ░░ ░░ ░░ ░░ ░░           │ ──▶ Processed next
  ├─────────┼───────────────────────────┤
  │  LOW    │ ▒▒ ▒▒ ▒▒ ▒▒ ▒▒ ▒▒ ▒▒    │ ──▶ Processed last
  └─────────┴───────────────────────────┘

  Implementation:
  ┌──────────┐      ┌─────────────┐      ┌──────────┐
  │ Producer │ ───▶ │  Router     │ ───▶ │ Queue Hi │ ───┐
  └──────────┘      │ (priority)  │      └──────────┘    │
                    │             │      ┌──────────┐    │ ┌──────────┐
                    │             │ ───▶ │ Queue Med│ ───┼▶│ Consumer │
                    │             │      └──────────┘    │ └──────────┘
                    │             │      ┌──────────┐    │
                    │             │ ───▶ │ Queue Low│ ───┘
                    └─────────────┘      └──────────┘
```

## Go Example

```go
package priorityqueue

import (
	"container/heap"
	"sync"
	"time"
)

// Priority levels
const (
	PriorityHigh   = "high"
	PriorityMedium = "medium"
	PriorityLow    = "low"
)

// Message represents a message with priority.
type Message struct {
	ID        string
	Priority  string
	Payload   interface{}
	CreatedAt time.Time
	Attempts  int
	index     int // index in heap
}

// PriorityQueue implements a priority queue.
type PriorityQueue struct {
	mu     sync.Mutex
	queues map[string][]Message
	order  []string
}

// NewPriorityQueue creates a new PriorityQueue.
func NewPriorityQueue() *PriorityQueue {
	return &PriorityQueue{
		queues: map[string][]Message{
			PriorityHigh:   make([]Message, 0),
			PriorityMedium: make([]Message, 0),
			PriorityLow:    make([]Message, 0),
		},
		order: []string{PriorityHigh, PriorityMedium, PriorityLow},
	}
}

// Enqueue adds a message to the queue.
func (pq *PriorityQueue) Enqueue(payload interface{}, priority string) string {
	pq.mu.Lock()
	defer pq.mu.Unlock()

	if priority == "" {
		priority = PriorityMedium
	}

	msg := Message{
		ID:        generateID(),
		Priority:  priority,
		Payload:   payload,
		CreatedAt: time.Now(),
		Attempts:  0,
	}

	pq.queues[priority] = append(pq.queues[priority], msg)
	return msg.ID
}

// Dequeue removes and returns the highest priority message.
func (pq *PriorityQueue) Dequeue() *Message {
	pq.mu.Lock()
	defer pq.mu.Unlock()

	// Check queues in priority order
	for _, priority := range pq.order {
		queue := pq.queues[priority]
		if len(queue) > 0 {
			msg := queue[0]
			pq.queues[priority] = queue[1:]
			msg.Attempts++
			return &msg
		}
	}

	return nil
}

// DequeueWeighted uses weighted fair queuing to avoid starvation.
func (pq *PriorityQueue) DequeueWeighted() *Message {
	pq.mu.Lock()
	defer pq.mu.Unlock()

	weights := map[string]int{
		PriorityHigh:   6, // 60%
		PriorityMedium: 3, // 30%
		PriorityLow:    1, // 10%
	}

	totalWeight := 10
	random := time.Now().UnixNano() % int64(totalWeight)

	cumulative := int64(0)
	for _, priority := range pq.order {
		cumulative += int64(weights[priority])
		if random < cumulative && len(pq.queues[priority]) > 0 {
			queue := pq.queues[priority]
			msg := queue[0]
			pq.queues[priority] = queue[1:]
			msg.Attempts++
			return &msg
		}
	}

	// Fallback: any available message
	return pq.dequeueAny()
}

func (pq *PriorityQueue) dequeueAny() *Message {
	for _, priority := range pq.order {
		queue := pq.queues[priority]
		if len(queue) > 0 {
			msg := queue[0]
			pq.queues[priority] = queue[1:]
			msg.Attempts++
			return &msg
		}
	}
	return nil
}

// GetStats returns queue statistics.
func (pq *PriorityQueue) GetStats() map[string]int {
	pq.mu.Lock()
	defer pq.mu.Unlock()

	stats := make(map[string]int)
	for priority, queue := range pq.queues {
		stats[priority] = len(queue)
	}
	return stats
}

func generateID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
```

## Redis Implementation (Go)

```go
// This example follows the same idiomatic Go patterns
// as the main example above.
// Specific implementation based on interfaces and
// standard Go conventions.
```

## Consumer with priority

```go
// This example follows the same idiomatic Go patterns
// as the main example above.
// Specific implementation based on interfaces and
// standard Go conventions.
```

## Real-world Use Cases

| Domain | High Priority | Medium | Low |
|--------|---------------|--------|-----|
| E-commerce | Failed payment | Order confirmation | Newsletter |
| Support | Critical incident | Customer ticket | Analytics |
| CI/CD | Production hotfix | Feature branch | Nightly builds |
| Notifications | Security alert | Transaction | Marketing |

## Avoiding Starvation

```go
// This example follows the same idiomatic Go patterns
// as the main example above.
// Specific implementation based on interfaces and
// standard Go conventions.
```

## When to Use

| Situation | Recommended |
|-----------|-------------|
| Differentiated SLAs per client | Yes |
| Batch vs real-time tasks | Yes |
| Limited resources | Yes |
| Fair processing required | No (or with anti-starvation) |
| Strict FIFO order | No |

## Related Patterns

| Pattern | Relation |
|---------|----------|
| Queue Load Leveling | Load smoothing |
| Competing Consumers | Processing parallelization |
| Throttling | Rate limiting |
| Circuit Breaker | Error handling |

## Sources

- [Microsoft - Priority Queue](https://learn.microsoft.com/en-us/azure/architecture/patterns/priority-queue)
- [AWS SQS Message Priority](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-message-priority.html)
- [RabbitMQ Priority Queues](https://www.rabbitmq.com/priority.html)
