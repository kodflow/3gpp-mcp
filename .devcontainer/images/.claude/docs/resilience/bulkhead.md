# Bulkhead Pattern

> Isolate resources to prevent a failure from spreading to the entire system.

---

## Principle

```
┌─────────────────────────────────────────────────────────────────┐
│                    BULKHEAD PATTERN                              │
│                                                                  │
│  Without Bulkhead:               With Bulkhead:                  │
│                                                                  │
│  ┌──────────────────┐          ┌───────┐ ┌───────┐ ┌───────┐   │
│  │   Shared Pool    │          │Pool A │ │Pool B │ │Pool C │   │
│  │ ┌──┐┌──┐┌──┐┌──┐ │          │ ┌──┐  │ │ ┌──┐  │ │ ┌──┐  │   │
│  │ │T1││T2││T3││T4│ │          │ │T1│  │ │ │T2│  │ │ │T3│  │   │
│  │ └──┘└──┘└──┘└──┘ │          │ └──┘  │ │ └──┘  │ │ └──┘  │   │
│  └──────────────────┘          └───────┘ └───────┘ └───────┘   │
│         │                           │         │         │       │
│         ▼                           ▼         ▼         ▼       │
│  If one service blocks,       Slow service A does not affect    │
│  the ENTIRE pool is exhausted B and C (isolation)               │
└─────────────────────────────────────────────────────────────────┘
```

---

## Types of Bulkhead

| Type | Description | Usage |
|------|-------------|-------|
| **Thread Pool** | Dedicated thread pool per service | Java, .NET |
| **Semaphore** | Limits the number of concurrent calls | Node.js, async |
| **Connection Pool** | Dedicated connection pool | Database, HTTP |
| **Process Isolation** | Separate processes | Microservices |

---

## Go Implementation - Semaphore

```go
package bulkhead

import (
	"context"
	"sync"
)

// Semaphore implements a counting semaphore.
type Semaphore struct {
	permits chan struct{}
}

// NewSemaphore creates a semaphore with max permits.
func NewSemaphore(maxPermits int) *Semaphore {
	return &Semaphore{
		permits: make(chan struct{}, maxPermits),
	}
}

// Acquire acquires a permit.
func (s *Semaphore) Acquire(ctx context.Context) error {
	select {
	case s.permits <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Release releases a permit.
func (s *Semaphore) Release() {
	<-s.permits
}

// Execute runs fn with semaphore protection.
func (s *Semaphore) Execute(ctx context.Context, fn func() error) error {
	if err := s.Acquire(ctx); err != nil {
		return err
	}
	defer s.Release()

	return fn()
}

// AvailablePermits returns available permits.
func (s *Semaphore) AvailablePermits() int {
	return cap(s.permits) - len(s.permits)
}

// WaitingCount returns number of waiting goroutines (approximation).
func (s *Semaphore) WaitingCount() int {
	// Note: This is an approximation
	return len(s.permits)
}
```

---

## Bulkhead with Timeout and Rejection

```go
package bulkhead

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrBulkheadRejected is returned when bulkhead rejects a request.
var ErrBulkheadRejected = errors.New("bulkhead rejected request")

// BulkheadRejectedError provides detailed rejection information.
type BulkheadRejectedError struct {
	Reason string
}

func (e *BulkheadRejectedError) Error() string {
	return fmt.Sprintf("bulkhead rejected: %s", e.Reason)
}

// Options configures bulkhead behavior.
type Options struct {
	MaxConcurrent int           // Max concurrent executions
	MaxWaiting    int           // Max queue size
	WaitTimeout   time.Duration // Max wait time in queue
}

// Bulkhead implements the bulkhead pattern with queuing.
type Bulkhead struct {
	mu      sync.RWMutex
	running int
	queue   chan *request
	options Options
}

type request struct {
	ctx     context.Context
	ready   chan error
	timeout *time.Timer
}

// NewBulkhead creates a new bulkhead.
func NewBulkhead(options Options) *Bulkhead {
	return &Bulkhead{
		queue:   make(chan *request, options.MaxWaiting),
		options: options,
	}
}

// Execute runs fn with bulkhead protection.
func (b *Bulkhead) Execute(ctx context.Context, fn func() error) error {
	b.mu.Lock()

	// Check if we can execute immediately
	if b.running < b.options.MaxConcurrent {
		b.running++
		b.mu.Unlock()

		defer func() {
			b.mu.Lock()
			b.running--
			b.mu.Unlock()
			b.processQueue()
		}()

		return fn()
	}

	// Check if queue is full
	if len(b.queue) >= b.options.MaxWaiting {
		b.mu.Unlock()
		return &BulkheadRejectedError{
			Reason: fmt.Sprintf("queue full (%d waiting)", b.options.MaxWaiting),
		}
	}

	b.mu.Unlock()

	// Wait in queue
	req := &request{
		ctx:     ctx,
		ready:   make(chan error, 1),
		timeout: time.NewTimer(b.options.WaitTimeout),
	}

	select {
	case b.queue <- req:
	case <-ctx.Done():
		return ctx.Err()
	}

	// Wait for permission or timeout
	select {
	case err := <-req.ready:
		req.timeout.Stop()
		if err != nil {
			return err
		}
	case <-req.timeout.C:
		return &BulkheadRejectedError{
			Reason: fmt.Sprintf("timeout waiting for bulkhead (%v)", b.options.WaitTimeout),
		}
	case <-ctx.Done():
		req.timeout.Stop()
		return ctx.Err()
	}

	defer func() {
		b.mu.Lock()
		b.running--
		b.mu.Unlock()
		b.processQueue()
	}()

	return fn()
}

func (b *Bulkhead) processQueue() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.queue) > 0 && b.running < b.options.MaxConcurrent {
		req := <-b.queue
		b.running++
		req.ready <- nil
	}
}

// GetMetrics returns current bulkhead metrics.
func (b *Bulkhead) GetMetrics() (running, waiting int) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.running, len(b.queue)
}
```

---

## Bulkhead per Service

```go
package bulkhead

import (
	"context"
	"sync"
)

// ServiceBulkheads manages bulkheads for multiple services.
type ServiceBulkheads struct {
	mu        sync.RWMutex
	bulkheads map[string]*Bulkhead
	configs   map[string]Options
	defaults  Options
}

// NewServiceBulkheads creates a service bulkheads manager.
func NewServiceBulkheads(defaults Options) *ServiceBulkheads {
	return &ServiceBulkheads{
		bulkheads: make(map[string]*Bulkhead),
		configs:   make(map[string]Options),
		defaults:  defaults,
	}
}

// Configure sets configuration for a service.
func (sb *ServiceBulkheads) Configure(serviceName string, options Options) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	sb.configs[serviceName] = options
}

// GetBulkhead returns bulkhead for a service.
func (sb *ServiceBulkheads) GetBulkhead(serviceName string) *Bulkhead {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	if bulkhead, ok := sb.bulkheads[serviceName]; ok {
		return bulkhead
	}

	// Create new bulkhead with service-specific or default config
	options := sb.defaults
	if config, ok := sb.configs[serviceName]; ok {
		options = config
	}

	bulkhead := NewBulkhead(options)
	sb.bulkheads[serviceName] = bulkhead
	return bulkhead
}

// ExecuteFor runs fn with the bulkhead for the specified service.
func (sb *ServiceBulkheads) ExecuteFor(ctx context.Context, serviceName string, fn func() error) error {
	return sb.GetBulkhead(serviceName).Execute(ctx, fn)
}

// GetAllMetrics returns metrics for all bulkheads.
func (sb *ServiceBulkheads) GetAllMetrics() map[string]struct{ Running, Waiting int } {
	sb.mu.RLock()
	defer sb.mu.RUnlock()

	metrics := make(map[string]struct{ Running, Waiting int })
	for name, bulkhead := range sb.bulkheads {
		running, waiting := bulkhead.GetMetrics()
		metrics[name] = struct{ Running, Waiting int }{running, waiting}
	}
	return metrics
}

// Usage
func processOrder(ctx context.Context, order *Order) error {
	bulkheads := NewServiceBulkheads(Options{
		MaxConcurrent: 10,
		MaxWaiting:    100,
		WaitTimeout:   5 * time.Second,
	})

	// Configure service-specific limits
	bulkheads.Configure("payment-service", Options{
		MaxConcurrent: 5,
		MaxWaiting:    20,
		WaitTimeout:   5 * time.Second,
	})
	bulkheads.Configure("inventory-service", Options{
		MaxConcurrent: 20,
		MaxWaiting:    50,
		WaitTimeout:   5 * time.Second,
	})
	bulkheads.Configure("notification-service", Options{
		MaxConcurrent: 50,
		MaxWaiting:    200,
		WaitTimeout:   5 * time.Second,
	})

	// Each service has its own bulkhead
	payment, err := executeWithBulkhead(ctx, bulkheads, "payment-service", func() (interface{}, error) {
		return paymentClient.Charge(ctx, order)
	})
	if err != nil {
		return err
	}

	inventory, err := executeWithBulkhead(ctx, bulkheads, "inventory-service", func() (interface{}, error) {
		return inventoryClient.Reserve(ctx, order.Items)
	})
	if err != nil {
		return err
	}

	if err := bulkheads.ExecuteFor(ctx, "notification-service", func() error {
		return notifyUser(ctx, order.UserID, "Order confirmed")
	}); err != nil {
		// Notification errors are non-fatal
		log.Printf("notification error: %v", err)
	}

	return nil
}
```

---

## Bulkhead with Connection Pool

```go
package bulkhead

import (
	"context"
	"sync"
)

// Connection represents a pooled connection.
type Connection interface {
	Execute(context.Context, string) error
	Close() error
}

// ConnectionPool implements a connection pool bulkhead.
type ConnectionPool struct {
	mu          sync.Mutex
	available   []Connection
	inUse       map[Connection]bool
	maxConn     int
	factory     func() (Connection, error)
	waiting     []chan Connection
}

// NewConnectionPool creates a connection pool.
func NewConnectionPool(maxConn int, factory func() (Connection, error)) *ConnectionPool {
	return &ConnectionPool{
		available: make([]Connection, 0, maxConn),
		inUse:     make(map[Connection]bool),
		maxConn:   maxConn,
		factory:   factory,
		waiting:   make([]chan Connection, 0),
	}
}

// Acquire gets a connection from the pool.
func (cp *ConnectionPool) Acquire(ctx context.Context) (Connection, error) {
	cp.mu.Lock()

	// Connection available
	if len(cp.available) > 0 {
		conn := cp.available[len(cp.available)-1]
		cp.available = cp.available[:len(cp.available)-1]
		cp.inUse[conn] = true
		cp.mu.Unlock()
		return conn, nil
	}

	// Create new connection if possible
	if len(cp.inUse) < cp.maxConn {
		cp.mu.Unlock()
		conn, err := cp.factory()
		if err != nil {
			return nil, err
		}
		cp.mu.Lock()
		cp.inUse[conn] = true
		cp.mu.Unlock()
		return conn, nil
	}

	// Wait for available connection
	ch := make(chan Connection, 1)
	cp.waiting = append(cp.waiting, ch)
	cp.mu.Unlock()

	select {
	case conn := <-ch:
		return conn, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Release returns a connection to the pool.
func (cp *ConnectionPool) Release(conn Connection) {
	cp.mu.Lock()
	defer cp.mu.Unlock()

	delete(cp.inUse, conn)

	// Give to waiting goroutine
	if len(cp.waiting) > 0 {
		ch := cp.waiting[0]
		cp.waiting = cp.waiting[1:]
		cp.inUse[conn] = true
		ch <- conn
		return
	}

	// Return to pool
	cp.available = append(cp.available, conn)
}

// Execute runs fn with a pooled connection.
func (cp *ConnectionPool) Execute(ctx context.Context, fn func(Connection) error) error {
	conn, err := cp.Acquire(ctx)
	if err != nil {
		return err
	}
	defer cp.Release(conn)

	return fn(conn)
}
```

---

## Recommended Configuration

| Service | maxConcurrent | maxWaiting | Justification |
|---------|---------------|------------|---------------|
| Payment Gateway | 5-10 | 20-50 | Critical service, limit |
| Database | 10-20 | 50-100 | Depends on DB pool |
| Cache | 50-100 | 200 | Fast, more permissive |
| External API | 10-20 | 30-50 | External rate limiting |
| File I/O | 5-10 | 20 | I/O bound |

---

## When to Use

- Services with different SLAs
- Protection against slow consumers
- Isolation of critical dependencies
- Prevention of resource exhaustion
- Microservices with multiple dependencies

---

## Related Patterns

| Pattern | Relation |
|---------|----------|
| [Circuit Breaker](circuit-breaker.md) | Use together |
| [Timeout](timeout.md) | Timeout within the bulkhead |
| [Rate Limiting](rate-limiting.md) | Different limit (throughput vs concurrency) |
| Thread Pool | Alternative implementation |

---

## Sources

- [Microsoft - Bulkhead Pattern](https://learn.microsoft.com/en-us/azure/architecture/patterns/bulkhead)
- [Release It! - Michael Nygard](https://pragprog.com/titles/mnee2/release-it-second-edition/)
- [Resilience4j - Bulkhead](https://resilience4j.readme.io/docs/bulkhead)
