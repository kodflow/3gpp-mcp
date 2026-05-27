# Timeout Pattern

> Limit the wait time of an operation to prevent resource blocking.

---

## Principle

```
┌─────────────────────────────────────────────────────────────┐
│                       TIMEOUT PATTERN                        │
│                                                              │
│  ┌─────────┐                    ┌─────────────────┐         │
│  │ Caller  │───────────────────►│ Remote Service  │         │
│  └─────────┘                    └─────────────────┘         │
│       │                               │                      │
│       │         Timeout!              │                      │
│       │◄──────────────────────────────│                      │
│       │                               │                      │
│       ▼                               │  (Still processing)  │
│  Handle timeout                       │                      │
│  (fallback, error)                    ▼                      │
└─────────────────────────────────────────────────────────────┘
```

---

## Types of Timeouts

| Type | Description | Usage |
|------|-------------|-------|
| **Connection timeout** | Time to establish the connection | HTTP, TCP, DB |
| **Read timeout** | Time to receive data | APIs, streams |
| **Request timeout** | Total request time | End-to-end |
| **Idle timeout** | Acceptable inactivity time | Persistent connections |

---

## Go Implementation

### Basic Timeout with context.Context

```go
package timeout

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// TimeoutError indicates an operation timed out.
type TimeoutError struct {
	Operation string
	Duration  time.Duration
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("%s timed out after %v", e.Operation, e.Duration)
}

// WithTimeout executes fn with a timeout.
func WithTimeout[T any](ctx context.Context, timeout time.Duration, fn func(context.Context) (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	type result struct {
		value T
		err   error
	}

	ch := make(chan result, 1)
	go func() {
		val, err := fn(ctx)
		ch <- result{value: val, err: err}
	}()

	select {
	case res := <-ch:
		return res.value, res.err
	case <-ctx.Done():
		var zero T
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return zero, &TimeoutError{
				Operation: "operation",
				Duration:  timeout,
			}
		}
		return zero, ctx.Err()
	}
}

// Usage
func example() error {
	ctx := context.Background()

	result, err := WithTimeout(ctx, 5*time.Second, func(ctx context.Context) (string, error) {
		// Simulated API call
		return fetchData(ctx, "https://api.example.com/data")
	})
	if err != nil {
		var timeoutErr *TimeoutError
		if errors.As(err, &timeoutErr) {
			return fmt.Errorf("API call timed out: %w", err)
		}
		return err
	}

	fmt.Println("Result:", result)
	return nil
}
```

---

### HTTP Client with Timeout

```go
package timeout

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

// HTTPClient wraps http.Client with timeout.
type HTTPClient struct {
	client *http.Client
}

// NewHTTPClient creates an HTTP client with timeout.
func NewHTTPClient(timeout time.Duration) *HTTPClient {
	return &HTTPClient{
		client: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				DialContext: (&net.Dialer{
					Timeout:   5 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 10 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
			},
		},
	}
}

// Get performs a GET request with timeout.
func (c *HTTPClient) Get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, &TimeoutError{
				Operation: "HTTP GET",
				Duration:  c.client.Timeout,
			}
		}
		return nil, fmt.Errorf("executing request: %w", err)
	}

	return resp, nil
}

// Usage
func fetchWithTimeout() error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	client := NewHTTPClient(5 * time.Second)
	resp, err := client.Get(ctx, "https://api.example.com/users")
	if err != nil {
		var timeoutErr *TimeoutError
		if errors.As(err, &timeoutErr) {
			log.Println("Request timed out, using cached data")
			return getCachedData()
		}
		return err
	}
	defer resp.Body.Close()

	// Process response
	return nil
}
```

---

### Timeout with Deadline Propagation

```go
package timeout

import (
	"context"
	"fmt"
	"time"
)

// ProcessWithDeadline executes fn within the context's deadline.
func ProcessWithDeadline[T any](ctx context.Context, fn func(context.Context) (T, error)) (T, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return fn(ctx)
	}

	if time.Now().After(deadline) {
		var zero T
		return zero, &TimeoutError{
			Operation: "deadline check",
			Duration:  0,
		}
	}

	return fn(ctx)
}

// HandleRequest processes a request with multiple stages sharing the same deadline.
func HandleRequest(ctx context.Context, data interface{}) error {
	// Stage 1: Validate
	validationResult, err := ProcessWithDeadline(ctx, func(ctx context.Context) (interface{}, error) {
		return validate(ctx, data)
	})
	if err != nil {
		return fmt.Errorf("validation: %w", err)
	}

	// Stage 2: Persist (uses remaining time from context)
	_, err = ProcessWithDeadline(ctx, func(ctx context.Context) (interface{}, error) {
		return database.Save(ctx, validationResult)
	})
	if err != nil {
		return fmt.Errorf("persistence: %w", err)
	}

	return nil
}

// Usage - Context with 5s deadline shared across stages
func example() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return HandleRequest(ctx, someData)
}
```

---

### Hierarchical Timeout

```go
package timeout

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// TimeoutManager manages timeouts for different operations.
type TimeoutManager struct {
	mu                sync.RWMutex
	globalTimeout     time.Duration
	operationTimeouts map[string]time.Duration
}

// NewTimeoutManager creates a new timeout manager.
func NewTimeoutManager(globalTimeout time.Duration) *TimeoutManager {
	return &TimeoutManager{
		globalTimeout: globalTimeout,
		operationTimeouts: map[string]time.Duration{
			"database":     5 * time.Second,
			"external_api": 10 * time.Second,
			"file_io":      3 * time.Second,
			"cache":        1 * time.Second,
		},
	}
}

// GetTimeout returns the timeout for an operation.
func (tm *TimeoutManager) GetTimeout(operation string) time.Duration {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	if timeout, ok := tm.operationTimeouts[operation]; ok {
		return timeout
	}
	return tm.globalTimeout
}

// Execute runs fn with the appropriate timeout for the operation.
func (tm *TimeoutManager) Execute(ctx context.Context, operation string, fn func(context.Context) error) error {
	timeout := tm.GetTimeout(operation)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- fn(ctx)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return &TimeoutError{
				Operation: operation,
				Duration:  timeout,
			}
		}
		return ctx.Err()
	}
}

// Usage
func processOrder(ctx context.Context, order *Order) error {
	tm := NewTimeoutManager(30 * time.Second)

	// Each operation has its own timeout
	if err := tm.Execute(ctx, "database", func(ctx context.Context) error {
		_, err := db.FindUser(ctx, order.UserID)
		return err
	}); err != nil {
		return fmt.Errorf("finding user: %w", err)
	}

	if err := tm.Execute(ctx, "external_api", func(ctx context.Context) error {
		return inventoryService.Check(ctx, order.Items)
	}); err != nil {
		return fmt.Errorf("checking inventory: %w", err)
	}

	if err := tm.Execute(ctx, "cache", func(ctx context.Context) error {
		return redis.Set(ctx, fmt.Sprintf("order:%s", order.ID), order)
	}); err != nil {
		// Cache errors are non-fatal
		log.Printf("cache error: %v", err)
	}

	return nil
}
```

---

### Timeout with Cleanup

```go
package timeout

import (
	"context"
	"fmt"
	"time"
)

// Cleaner defines resource cleanup.
type Cleaner interface {
	Cleanup(context.Context) error
}

// WithTimeoutAndCleanup executes fn with timeout and cleanup on failure.
func WithTimeoutAndCleanup[T Cleaner](ctx context.Context, timeout time.Duration, fn func(context.Context) (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result, err := fn(ctx)
	if err != nil {
		// Cleanup on error (with a separate timeout)
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()

		if cleanupErr := result.Cleanup(cleanupCtx); cleanupErr != nil {
			return result, fmt.Errorf("operation failed: %w; cleanup failed: %v", err, cleanupErr)
		}
		return result, err
	}

	return result, nil
}

// Transaction represents a database transaction with cleanup.
type Transaction struct {
	tx interface{}
}

// Cleanup rolls back the transaction.
func (t *Transaction) Cleanup(ctx context.Context) error {
	return t.Rollback(ctx)
}

// Usage with database transaction
func executeTransaction(ctx context.Context) error {
	txn, err := WithTimeoutAndCleanup(ctx, 5*time.Second, func(ctx context.Context) (*Transaction, error) {
		tx, err := db.BeginTransaction(ctx)
		if err != nil {
			return nil, err
		}

		if err := tx.Execute(ctx, "INSERT INTO orders ..."); err != nil {
			return &Transaction{tx: tx}, err
		}

		if err := tx.Execute(ctx, "UPDATE inventory ..."); err != nil {
			return &Transaction{tx: tx}, err
		}

		if err := tx.Commit(ctx); err != nil {
			return &Transaction{tx: tx}, err
		}

		return &Transaction{tx: tx}, nil
	})
	if err != nil {
		return err
	}

	return nil
}
```

---

## Recommended Configuration

| Operation | Timeout | Justification |
|-----------|---------|---------------|
| Health check | 1-2s | Must be fast |
| Cache lookup | 100-500ms | In memory |
| Database query | 3-10s | Depends on complexity |
| Internal API | 5-10s | Same network |
| External API | 10-30s | Variable latency |
| File upload | 60-300s | Depends on size |

---

## When to Use

- Any network call (HTTP, gRPC, TCP)
- Database queries
- Remote file operations
- Third-party service calls
- Any operation that could block indefinitely

---

## Best Practices

| Practice | Reason |
|----------|--------|
| Always set a timeout | Avoid infinite blocking |
| Propagate deadlines | End-to-end consistency |
| Timeout < keep-alive | Avoid zombie connections |
| Cleanup on timeout | Release resources |
| Log timeouts | Debugging and alerting |

---

## Related Patterns

| Pattern | Relation |
|---------|----------|
| [Circuit Breaker](circuit-breaker.md) | Timeout contributes to failures |
| [Retry](retry.md) | Timeout per attempt |
| [Bulkhead](bulkhead.md) | Limit blocked threads |
| Graceful Degradation | Fallback on timeout |

---

## Sources

- [Google SRE - Handling Overload](https://sre.google/sre-book/handling-overload/)
- [AWS - Timeouts and Retries](https://aws.amazon.com/builders-library/timeouts-retries-and-backoff-with-jitter/)
- [Microservices Patterns - Chris Richardson](https://microservices.io/patterns/reliability/circuit-breaker.html)
