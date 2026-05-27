# Splitter-Aggregator Pattern

Split a composite message into parts and recombine them.

## Overview

```
                    +----------+
                    | Splitter |
+------------+      |          |      +-----------+
| Composite  |----->|  Split   |----->| Message 1 |--+
| Message    |      |          |      +-----------+  |
| [A, B, C]  |      +----------+      +-----------+  |    +------------+
+------------+                   +--->| Message 2 |--+--->| Aggregator |
                                      +-----------+  |    |            |
                                      +-----------+  |    | Combine    |
                                 +--->| Message 3 |--+    +-----+------+
                                      +-----------+             |
                                                                v
                                                         +------------+
                                                         | Result     |
                                                         | [R1,R2,R3] |
                                                         +------------+
```

---

## Splitter Pattern

> Splits a message into multiple individual messages.

### Schema

```
Order { items: [A, B, C] }
           |
           v
    +-----------+
    | Splitter  |
    +-----------+
     /    |    \
    v     v     v
Item A  Item B  Item C
(+ metadata correlation)
```

### Implementation

```go
package messaging

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

type SplitResult[T any] struct {
	CorrelationID     string `json:"correlationId"`
	SequenceNumber    int    `json:"sequenceNumber"`
	SequenceSize      int    `json:"sequenceSize"`
	IsLast            bool   `json:"isLast"`
	Payload           T      `json:"payload"`
	OriginalMessageID string `json:"originalMessageId"`
}

type Splitter[TComposite any, TPart any] struct {
	extractParts func(composite TComposite) []TPart
	enrichPart   func(part TPart, composite TComposite, index int) TPart
}

func NewSplitter[TComposite any, TPart any](
	extractParts func(composite TComposite) []TPart,
	enrichPart func(part TPart, composite TComposite, index int) TPart,
) *Splitter[TComposite, TPart] {
	return &Splitter[TComposite, TPart]{
		extractParts: extractParts,
		enrichPart:   enrichPart,
	}
}

type Message[T any] struct {
	ID      string
	Payload T
}

func (s *Splitter[TComposite, TPart]) Split(message Message[TComposite]) []SplitResult[TPart] {
	parts:= s.extractParts(message.Payload)
	correlationID:= uuid.New().String()
	results:= make([]SplitResult[TPart], len(parts))

	for i, part:= range parts {
		enriched:= part
		if s.enrichPart != nil {
			enriched = s.enrichPart(part, message.Payload, i)
		}

		results[i] = SplitResult[TPart]{
			CorrelationID:     correlationID,
			SequenceNumber:    i,
			SequenceSize:      len(parts),
			IsLast:            i == len(parts)-1,
			OriginalMessageID: message.ID,
			Payload:           enriched,
		}
	}

	return results
}

// Example: Order splitter
type Order struct {
	OrderID         string
	CustomerID      string
	Items           []OrderItem
	ShippingAddress Address
}

type OrderItem struct {
	ProductID string
	Quantity  int
	Price     float64
}

type Address struct {
	Street  string
	City    string
	ZipCode string
	Country string
}

type OrderItemMessage struct {
	OrderID         string
	CustomerID      string
	Item            OrderItem
	ShippingAddress Address
}

func NewOrderSplitter() *Splitter[Order, OrderItemMessage] {
	return NewSplitter(
		func(order Order) []OrderItemMessage {
			messages:= make([]OrderItemMessage, len(order.Items))
			for i, item:= range order.Items {
				messages[i] = OrderItemMessage{
					OrderID:         order.OrderID,
					CustomerID:      order.CustomerID,
					Item:            item,
					ShippingAddress: order.ShippingAddress,
				}
			}
			return messages
		},
		nil, // No enrichment needed
	)
}

// Usage with RabbitMQ
func SplitAndPublish(ctx context.Context, order Order, channel MessagePublisher) error {
	splitter:= NewOrderSplitter()
	splitMessages:= splitter.Split(Message[Order]{
		ID:      order.OrderID,
		Payload: order,
	})

	for _, msg:= range splitMessages {
		headers:= map[string]interface{}{
			"x-correlation-id":  msg.CorrelationID,
			"x-sequence-number": msg.SequenceNumber,
			"x-sequence-size":   msg.SequenceSize,
		}

		if err:= channel.Publish(ctx, "order-items", msg, headers); err != nil {
			return fmt.Errorf("publishing split message: %w", err)
		}
	}

	return nil
}

type MessagePublisher interface {
	Publish(ctx context.Context, queue string, message interface{}, headers map[string]interface{}) error
}
```

**When:** Parallel processing, load distribution, batch processing.
**Related to:** Aggregator, Scatter-Gather.

---

## Aggregator Pattern

> Combines multiple related messages into one.

### Aggregator Schema

```
Result A --+
           |    +------------+
Result B --+--->| Aggregator |---> Combined Result
           |    +------------+
Result C --+         |
                     v
              Completion Strategy:
              - All received?
              - Timeout?
              - First N?
```

### Aggregator Implementation

```go
package messaging

import (
	"context"
	"sync"
	"time"
)

type AggregationContext[T any, R any] struct {
	CorrelationID string
	ExpectedCount int
	ReceivedParts []T
	StartedAt     time.Time
	TimeoutMs     int
}

type CompletionStrategy[T any] func(ctx *AggregationContext[T, any]) bool

type AggregationFunction[T any, R any] func(parts []T) R

type Aggregator[TPart any, TResult any] struct {
	contexts          map[string]*AggregationContext[TPart, TResult]
	mu                sync.RWMutex
	completionStrategy CompletionStrategy[TPart]
	aggregateFn       AggregationFunction[TPart, TResult]
	defaultTimeout    time.Duration
	cleanupInterval   time.Duration
	stopCleanup       chan struct{}
}

func NewAggregator[TPart any, TResult any](
	completionStrategy CompletionStrategy[TPart],
	aggregateFn AggregationFunction[TPart, TResult],
	defaultTimeout time.Duration,
) *Aggregator[TPart, TResult] {
	if defaultTimeout == 0 {
		defaultTimeout = 30 * time.Second
	}

	agg:= &Aggregator[TPart, TResult]{
		contexts:           make(map[string]*AggregationContext[TPart, TResult]),
		completionStrategy: completionStrategy,
		aggregateFn:        aggregateFn,
		defaultTimeout:     defaultTimeout,
		cleanupInterval:    5 * time.Second,
		stopCleanup:        make(chan struct{}),
	}

	go agg.startCleanup()
	return agg
}

func (a *Aggregator[TPart, TResult]) Add(message SplitResult[TPart]) *TResult {
	a.mu.Lock()
	defer a.mu.Unlock()

	correlationID:= message.CorrelationID

	if _, exists:= a.contexts[correlationID]; !exists {
		a.contexts[correlationID] = &AggregationContext[TPart, TResult]{
			CorrelationID: correlationID,
			ExpectedCount: message.SequenceSize,
			ReceivedParts: make([]TPart, 0, message.SequenceSize),
			StartedAt:     time.Now(),
			TimeoutMs:     int(a.defaultTimeout.Milliseconds()),
		}
	}

	ctx:= a.contexts[correlationID]
	ctx.ReceivedParts = append(ctx.ReceivedParts, message.Payload)

	if a.completionStrategy((*AggregationContext[TPart, any])(unsafe.Pointer(ctx))) {
		result:= a.aggregateFn(ctx.ReceivedParts)
		delete(a.contexts, correlationID)
		return &result
	}

	return nil
}

func (a *Aggregator[TPart, TResult]) startCleanup() {
	ticker:= time.NewTicker(a.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-a.stopCleanup:
			return
		case <-ticker.C:
			a.cleanupExpired()
		}
	}
}

func (a *Aggregator[TPart, TResult]) cleanupExpired() {
	a.mu.Lock()
	defer a.mu.Unlock()

	now:= time.Now()
	for id, ctx:= range a.contexts {
		if now.Sub(ctx.StartedAt) > a.defaultTimeout {
			a.handleTimeout(ctx)
			delete(a.contexts, id)
		}
	}
}

func (a *Aggregator[TPart, TResult]) handleTimeout(ctx *AggregationContext[TPart, TResult]) {
	fmt.Printf("Aggregation timeout: %s (received: %d, expected: %d)\n",
		ctx.CorrelationID, len(ctx.ReceivedParts), ctx.ExpectedCount)
}

func (a *Aggregator[TPart, TResult]) Stop() {
	close(a.stopCleanup)
}

// Completion strategies
func AllReceivedStrategy[T any](ctx *AggregationContext[T, any]) bool {
	return len(ctx.ReceivedParts) >= ctx.ExpectedCount
}

func MajorityStrategy[T any](ctx *AggregationContext[T, any]) bool {
	return len(ctx.ReceivedParts) > ctx.ExpectedCount/2
}

func TimeoutOrAllStrategy[T any](timeoutMs int) CompletionStrategy[T] {
	return func(ctx *AggregationContext[T, any]) bool {
		return len(ctx.ReceivedParts) >= ctx.ExpectedCount ||
			time.Since(ctx.StartedAt).Milliseconds() > int64(timeoutMs)
	}
}
```

### Complete Example

```go
package messaging

import (
	"context"
	"encoding/json"
	"time"
)

// Aggregation of item processing results
type ItemProcessingResult struct {
	ItemID            string `json:"itemId"`
	Success           bool   `json:"success"`
	WarehouseLocation string `json:"warehouseLocation,omitempty"`
	Error             string `json:"error,omitempty"`
}

type OrderProcessingResult struct {
	OrderID       string                 `json:"orderId"`
	AllSuccessful bool                   `json:"allSuccessful"`
	ItemResults   []ItemProcessingResult `json:"itemResults"`
	ProcessedAt   time.Time              `json:"processedAt"`
}

func NewOrderResultAggregator() *Aggregator[SplitResult[ItemProcessingResult], OrderProcessingResult] {
	return NewAggregator(
		AllReceivedStrategy[SplitResult[ItemProcessingResult]],
		func(parts []SplitResult[ItemProcessingResult]) OrderProcessingResult {
			results:= make([]ItemProcessingResult, len(parts))
			allSuccessful:= true
			var orderID string

			for i, part:= range parts {
				results[i] = part.Payload
				if !part.Payload.Success {
					allSuccessful = false
				}
				if i == 0 && len(results) > 0 {
					// Get orderID from first result
					orderID = part.Payload.OrderID
				}
			}

			return OrderProcessingResult{
				OrderID:       orderID,
				AllSuccessful: allSuccessful,
				ItemResults:   results,
				ProcessedAt:   time.Now(),
			}
		},
		30*time.Second,
	)
}

// Consumer
func ConsumeItemResults(ctx context.Context, channel <-chan []byte, publisher MessagePublisher) error {
	aggregator:= NewOrderResultAggregator()
	defer aggregator.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msgBytes:= <-channel:
			var result SplitResult[ItemProcessingResult]
			if err:= json.Unmarshal(msgBytes, &result); err != nil {
				continue
			}

			if aggregated:= aggregator.Add(result); aggregated != nil {
				// Order complete, publish result
				if err:= publisher.Publish(ctx, "order-results", aggregated, nil); err != nil {
					fmt.Printf("Error publishing aggregated result: %v\n", err)
				}
			}
		}
	}
}
```

**When:** After Splitter, collect responses, batch results.
**Related to:** Splitter, Scatter-Gather.

---

## Error Cases

```go
package messaging

import (
	"fmt"
)

type DeadLetterQueue interface {
	Send(ctx context.Context, message interface{}) error
}

type ResilientAggregator[T any, R any] struct {
	*Aggregator[T, R]
	deadLetterQueue DeadLetterQueue
}

func NewResilientAggregator[T any, R any](
	completionStrategy CompletionStrategy[T],
	aggregateFn AggregationFunction[T, R],
	defaultTimeout time.Duration,
	dlq DeadLetterQueue,
) *ResilientAggregator[T, R] {
	return &ResilientAggregator[T, R]{
		Aggregator:      NewAggregator(completionStrategy, aggregateFn, defaultTimeout),
		deadLetterQueue: dlq,
	}
}

func (r *ResilientAggregator[T, R]) handleTimeout(ctx *AggregationContext[T, R]) {
	// Option 1: Aggregate with what we have
	if len(ctx.ReceivedParts) > 0 {
		partialResult:= r.aggregateFn(ctx.ReceivedParts)
		r.publishPartialResult(partialResult, ctx)
	}

	// Option 2: Send to dead letter
	r.deadLetterQueue.Send(context.Background(), map[string]interface{}{
		"type":          "aggregation_timeout",
		"correlationId": ctx.CorrelationID,
		"received":      len(ctx.ReceivedParts),
		"expected":      ctx.ExpectedCount,
		"partialData":   ctx.ReceivedParts,
	})
}

func (r *ResilientAggregator[T, R]) publishPartialResult(result R, ctx *AggregationContext[T, R]) {
	fmt.Printf("Publishing partial result for %s\n", ctx.CorrelationID)
	// Implementation depends on message broker
}

func (r *ResilientAggregator[T, R]) handleDuplicate(message SplitResult[T]) {
	fmt.Printf("Duplicate message received: %s:%d\n", message.CorrelationID, message.SequenceNumber)
	// Ignore the duplicate - idempotency
}
```

---

## Decision Table

| Scenario | Pattern | Strategy |
|----------|---------|-----------|
| Batch processing | Splitter | Per item |
| Collect all results | Aggregator | Wait all |
| Partial results OK | Aggregator | Timeout |
| Best effort | Aggregator | Majority |

---

## When to Use

- Parallel collection processing (batch processing)
- Splitting composite messages for distributed processing
- Aggregation of results from multiple sources
- Fan-out/Fan-in for processing acceleration
- Decomposition of complex commands into subtasks

## Related Patterns

- [Scatter-Gather](./scatter-gather.md) - Combined Splitter and Aggregator
- [Pipes and Filters](./pipes-filters.md) - Processing pipeline
- [Message Channel](./message-channel.md) - Part transport
- [Idempotent Receiver](./idempotent-receiver.md) - Duplication handling

## Complementary Patterns

- **Scatter-Gather** - Splitter + Aggregator combined
- **Composed Message Processor** - Pipeline transformation
- **Correlation Identifier** - Link the parts
- **Resequencer** - Reorder messages
