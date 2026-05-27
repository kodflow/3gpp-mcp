# Message Router Patterns

Dynamic message routing patterns.

## Overview

```
                    +-------------------+
                    |   MESSAGE ROUTER  |
                    |                   |
Message In -------->|  [Route Logic]    |
                    |                   |
                    +---+-------+-------+
                        |       |       |
                        v       v       v
                    Queue A  Queue B  Queue C
```

---

## Content-Based Router

> Routes messages based on their content.

### Schema

```
             +------------------------+
             |  Content-Based Router  |
Message ---->|                        |
             |  if type == "order"    |---> Order Queue
             |  if type == "payment"  |---> Payment Queue
             |  if type == "shipping" |---> Shipping Queue
             +------------------------+
```

### Implementation

```go
package router

import (
	"context"
	"fmt"
)

// RoutingRule defines a routing predicate and destination.
type RoutingRule[T any] struct {
	Predicate   func(T) bool
	Destination string
}

// ContentBasedRouter routes messages based on content.
type ContentBasedRouter[T any] struct {
	rules              []RoutingRule[T]
	defaultDestination string
}

// NewContentBasedRouter creates a new content-based router.
func NewContentBasedRouter[T any](defaultDest string) *ContentBasedRouter[T] {
	return &ContentBasedRouter[T]{
		rules:              make([]RoutingRule[T], 0),
		defaultDestination: defaultDest,
	}
}

// AddRule adds a routing rule.
func (cbr *ContentBasedRouter[T]) AddRule(predicate func(T) bool, destination string) *ContentBasedRouter[T] {
	cbr.rules = append(cbr.rules, RoutingRule[T]{
		Predicate:   predicate,
		Destination: destination,
	})
	return cbr
}

// Route determines the destination for a message.
func (cbr *ContentBasedRouter[T]) Route(ctx context.Context, message T) string {
	for _, rule:= range cbr.rules {
		if rule.Predicate(message) {
			return rule.Destination
		}
	}
	return cbr.defaultDestination
}

// Order represents an order message.
type Order struct {
	ID       string
	Priority string
	Total    float64
	Type     string
	Region   string
}

// OrderRouter routes orders to appropriate queues.
type OrderRouter struct {
	router   *ContentBasedRouter[Order]
	channels map[string]chan<- Order
}

// NewOrderRouter creates a new order router.
func NewOrderRouter(channels map[string]chan<- Order) *OrderRouter {
	router:= NewContentBasedRouter[Order]("default-queue")
	
	router.
		AddRule(func(o Order) bool {
			return o.Priority == "urgent" && o.Total > 10000
		}, "vip-express-queue").
		AddRule(func(o Order) bool {
			return o.Priority == "urgent"
		}, "express-queue").
		AddRule(func(o Order) bool {
			return o.Type == "subscription"
		}, "subscription-queue").
		AddRule(func(o Order) bool {
			return o.Region == "EU"
		}, "eu-orders-queue")

	return &OrderRouter{
		router:   router,
		channels: channels,
	}
}

// RouteOrder routes an order to the appropriate channel.
func (or *OrderRouter) RouteOrder(ctx context.Context, order Order) error {
	destination:= or.router.Route(ctx, order)
	
	ch, exists:= or.channels[destination]
	if !exists {
		return fmt.Errorf("destination channel not found: %s", destination)
	}

	select {
	case ch <- order:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// RoutingPipeline routes messages through channels.
type RoutingPipeline[T any] struct {
	router   *ContentBasedRouter[T]
	inputCh  <-chan T
	channels map[string]chan<- T
}

// NewRoutingPipeline creates a new routing pipeline.
func NewRoutingPipeline[T any](
	router *ContentBasedRouter[T],
	inputCh <-chan T,
	channels map[string]chan<- T,
) *RoutingPipeline[T] {
	return &RoutingPipeline[T]{
		router:   router,
		inputCh:  inputCh,
		channels: channels,
	}
}

// Start begins routing messages.
func (rp *RoutingPipeline[T]) Start(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok:= <-rp.inputCh:
			if !ok {
				return nil
			}

			dest:= rp.router.Route(ctx, msg)
			ch, exists:= rp.channels[dest]
			if !exists {
				continue // Log error
			}

			select {
			case ch <- msg:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}
```

**When:** Routing by business rules, load segregation.
**Related to:** Message Filter, Recipient List.

---

## Dynamic Router

> Destination determined at runtime from an external source.

### Dynamic Router Schema

```
             +-------------------+     +----------------+
             |  Dynamic Router   |<--->| Routing Config |
Message ---->|                   |     | (DB/Service)   |
             +--------+----------+     +----------------+
                      |
         +------------+------------+
         v            v            v
      Dest A       Dest B       Dest C
```

### Dynamic Router Implementation

```go
package dynamic

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// RoutingConfig provides routing configuration.
type RoutingConfig interface {
	GetDestination(ctx context.Context, messageType string, metadata map[string]interface{}) (string, error)
}

// DynamicRouter routes messages based on external configuration.
type DynamicRouter[T any] struct {
	config         RoutingConfig
	typeExtractor  func(T) string
	metaExtractor  func(T) map[string]interface{}
	channels       map[string]chan<- T
	mu             sync.RWMutex
}

// NewDynamicRouter creates a new dynamic router.
func NewDynamicRouter[T any](
	config RoutingConfig,
	typeExtractor func(T) string,
	metaExtractor func(T) map[string]interface{},
	channels map[string]chan<- T,
) *DynamicRouter[T] {
	return &DynamicRouter[T]{
		config:        config,
		typeExtractor: typeExtractor,
		metaExtractor: metaExtractor,
		channels:      channels,
	}
}

// Route routes a message to the appropriate destination.
func (dr *DynamicRouter[T]) Route(ctx context.Context, message T) error {
	msgType:= dr.typeExtractor(message)
	metadata:= dr.metaExtractor(message)

	destination, err:= dr.config.GetDestination(ctx, msgType, metadata)
	if err != nil {
		return fmt.Errorf("getting destination: %w", err)
	}

	dr.mu.RLock()
	ch, exists:= dr.channels[destination]
	dr.mu.RUnlock()

	if !exists {
		return fmt.Errorf("channel not found: %s", destination)
	}

	select {
	case ch <- message:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// CachedRoutingConfig caches routing decisions.
type CachedRoutingConfig struct {
	delegate    RoutingConfig
	cache       sync.Map
	cacheTTL    time.Duration
}

type cacheEntry struct {
	destination string
	expiresAt   time.Time
}

// NewCachedRoutingConfig creates a cached routing config.
func NewCachedRoutingConfig(delegate RoutingConfig, ttl time.Duration) *CachedRoutingConfig {
	return &CachedRoutingConfig{
		delegate: delegate,
		cacheTTL: ttl,
	}
}

// GetDestination gets destination with caching.
func (crc *CachedRoutingConfig) GetDestination(
	ctx context.Context,
	messageType string,
	metadata map[string]interface{},
) (string, error) {
	cacheKey:= fmt.Sprintf("%s:%v", messageType, metadata)

	if entry, ok:= crc.cache.Load(cacheKey); ok {
		cached:= entry.(cacheEntry)
		if time.Now().Before(cached.expiresAt) {
			return cached.destination, nil
		}
		crc.cache.Delete(cacheKey)
	}

	destination, err:= crc.delegate.GetDestination(ctx, messageType, metadata)
	if err != nil {
		return "", err
	}

	crc.cache.Store(cacheKey, cacheEntry{
		destination: destination,
		expiresAt:   time.Now().Add(crc.cacheTTL),
	})

	return destination, nil
}
```

**When:** Changing rules, A/B testing, feature flags.
**Related to:** Content-Based Router.

---

## Recipient List

> Sends the message to a dynamic list of recipients.

### Recipient List Schema

```
             +------------------+
             |  Recipient List  |
Message ---->|                  |
             |  Recipients:     |
             |  - Service A     |---> Service A
             |  - Service B     |---> Service B
             |  - Service C     |---> Service C
             +------------------+
```

### Recipient List Implementation

```go
package recipient

import (
	"context"
	"fmt"
	"sync"
)

// RecipientResolver resolves recipients for a message.
type RecipientResolver[T any] func(T) []string

// DistributionResult contains distribution results.
type DistributionResult struct {
	Total      int
	Successful int
	Failed     []FailedRecipient
}

// FailedRecipient represents a failed delivery.
type FailedRecipient struct {
	Recipient string
	Error     error
}

// RecipientList distributes messages to multiple recipients.
type RecipientList[T any] struct {
	resolver RecipientResolver[T]
	channels map[string]chan<- T
	mu       sync.RWMutex
}

// NewRecipientList creates a new recipient list.
func NewRecipientList[T any](
	resolver RecipientResolver[T],
	channels map[string]chan<- T,
) *RecipientList[T] {
	return &RecipientList[T]{
		resolver: resolver,
		channels: channels,
	}
}

// Distribute sends message to all recipients.
func (rl *RecipientList[T]) Distribute(ctx context.Context, message T) (*DistributionResult, error) {
	recipients:= rl.resolver(message)
	
	result:= &DistributionResult{
		Total:  len(recipients),
		Failed: make([]FailedRecipient, 0),
	}

	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, recipient:= range recipients {
		rcpt:= recipient
		wg.Go(func() {
			rl.mu.RLock()
			ch, exists:= rl.channels[rcpt]
			rl.mu.RUnlock()

			if !exists {
				mu.Lock()
				result.Failed = append(result.Failed, FailedRecipient{
					Recipient: rcpt,
					Error:     fmt.Errorf("channel not found"),
				})
				mu.Unlock()
				return
			}

			select {
			case ch <- message:
				mu.Lock()
				result.Successful++
				mu.Unlock()
			case <-ctx.Done():
				mu.Lock()
				result.Failed = append(result.Failed, FailedRecipient{
					Recipient: rcpt,
					Error:     ctx.Err(),
				})
				mu.Unlock()
			}
		})
	}

	wg.Wait()
	return result, nil
}

// GuaranteedRecipientList ensures delivery with retries.
type GuaranteedRecipientList[T any] struct {
	recipientList *RecipientList[T]
	maxRetries    int
}

// NewGuaranteedRecipientList creates a guaranteed recipient list.
func NewGuaranteedRecipientList[T any](
	rl *RecipientList[T],
	maxRetries int,
) *GuaranteedRecipientList[T] {
	return &GuaranteedRecipientList[T]{
		recipientList: rl,
		maxRetries:    maxRetries,
	}
}

// DistributeWithRetry distributes with retry logic.
func (grl *GuaranteedRecipientList[T]) DistributeWithRetry(
	ctx context.Context,
	message T,
) error {
	var lastResult *DistributionResult
	var err error

	for attempt:= 0; attempt < grl.maxRetries; attempt++ {
		lastResult, err = grl.recipientList.Distribute(ctx, message)
		if err != nil {
			return fmt.Errorf("distribution failed: %w", err)
		}

		if len(lastResult.Failed) == 0 {
			return nil
		}

		// Backoff before retry
		select {
		case <-time.After(time.Second * time.Duration(1<<attempt)):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	if len(lastResult.Failed) > 0 {
		return fmt.Errorf("partial delivery failure: %d failed", len(lastResult.Failed))
	}

	return nil
}
```

**When:** Multicast, multiple notifications, fan-out.
**Related to:** Publish-Subscribe, Scatter-Gather.

---

## Resilient Router

> Router with fallback and dead letter channel.

```go
package resilient

import (
	"context"
	"errors"
	"fmt"
)

var (
	// ErrRoutingConfigFailed indicates routing config failure.
	ErrRoutingConfigFailed = errors.New("routing config failed")
	// ErrDestinationUnavailable indicates destination unavailable.
	ErrDestinationUnavailable = errors.New("destination unavailable")
)

// ResilientRouter routes with fallback mechanisms.
type ResilientRouter[T any] struct {
	dynamicRouter *DynamicRouter[T]
	staticRouter  *ContentBasedRouter[T]
	parkingCh     chan<- T
	deadLetterCh  chan<- T
}

// NewResilientRouter creates a resilient router.
func NewResilientRouter[T any](
	dynamicRouter *DynamicRouter[T],
	staticRouter *ContentBasedRouter[T],
	parkingCh, deadLetterCh chan<- T,
) *ResilientRouter[T] {
	return &ResilientRouter[T]{
		dynamicRouter: dynamicRouter,
		staticRouter:  staticRouter,
		parkingCh:     parkingCh,
		deadLetterCh:  deadLetterCh,
	}
}

// RouteWithFallback routes with fallback logic.
func (rr *ResilientRouter[T]) RouteWithFallback(ctx context.Context, message T) error {
	// Try dynamic routing first
	err:= rr.dynamicRouter.Route(ctx, message)
	if err == nil {
		return nil
	}

	// Check error type and apply fallback
	if errors.Is(err, ErrRoutingConfigFailed) {
		// Fallback to static routing
		destination:= rr.staticRouter.Route(ctx, message)
		// Send to static destination (implementation specific)
		_ = destination
		return nil
	}

	if errors.Is(err, ErrDestinationUnavailable) {
		// Park message for retry
		select {
		case rr.parkingCh <- message:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	// Send to dead letter channel
	select {
	case rr.deadLetterCh <- message:
		return fmt.Errorf("message sent to DLQ: %w", err)
	case <-ctx.Done():
		return ctx.Err()
	}
}
```

---

## Decision Table

| Pattern | Use Case | Flexibility | Complexity |
|---------|-------------|-------------|------------|
| Content-Based | Fixed rules | Medium | Low |
| Dynamic | Changing rules | High | Medium |
| Recipient List | Multi-dest | High | Medium |

---

## When to Use

- Conditional routing based on message content or headers
- Message distribution to different destinations based on business rules
- A/B testing or feature flags with dynamic routing
- Multicast to multiple recipients simultaneously
- Load segregation between different processing queues

## Related Patterns

- [Message Channel](./message-channel.md) - Target communication channels
- [Pipes and Filters](./pipes-filters.md) - Routing in a pipeline
- [Scatter-Gather](./scatter-gather.md) - Route then collect responses
- [Dead Letter Channel](./dead-letter.md) - Routing failure handling

## Complementary Patterns

- **Message Filter** - Filter before routing
- **Scatter-Gather** - Route then collect
- **Process Manager** - Orchestrate routing
- **Dead Letter Channel** - Handle routing failures
