# Retry Pattern

> Automatically retry failed operations with exponential backoff and jitter.

---

## Principle

```
┌────────────────────────────────────────────────────────────────┐
│                      RETRY WITH BACKOFF                         │
│                                                                 │
│  Attempt 1    Attempt 2      Attempt 3        Attempt 4        │
│     │            │              │                │             │
│     ▼            ▼              ▼                ▼             │
│  ┌─────┐      ┌─────┐        ┌─────┐          ┌─────┐         │
│  │FAIL │─────►│FAIL │───────►│FAIL │─────────►│ OK  │         │
│  └─────┘      └─────┘        └─────┘          └─────┘         │
│     │            │              │                              │
│     ▼            ▼              ▼                              │
│   100ms        200ms          400ms     (exponential backoff)  │
│   ±50ms        ±100ms         ±200ms    (jitter)               │
└────────────────────────────────────────────────────────────────┘
```

---

## Backoff Strategies

| Strategy | Formula | Usage |
|----------|---------|-------|
| **Constant** | `delay` | Tests, rate limiting |
| **Linear** | `delay * attempt` | Gentle progression |
| **Exponential** | `delay * 2^attempt` | Recommended standard |
| **Exponential + Jitter** | `delay * 2^attempt * random(0.5-1.5)` | Production (avoids thundering herd) |

---

## Go Implementation

```go
package retry

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"time"
)

// RetryError wraps errors after all retry attempts failed.
type RetryError struct {
	Attempts  int
	LastError error
}

func (e *RetryError) Error() string {
	return fmt.Sprintf("failed after %d attempts: %v", e.Attempts, e.LastError)
}

func (e *RetryError) Unwrap() error {
	return e.LastError
}

// RetryOptions configures retry behavior.
type RetryOptions struct {
	MaxAttempts       int
	BaseDelay         time.Duration
	MaxDelay          time.Duration
	BackoffMultiplier float64
	Jitter            bool
	RetryableErrors   func(error) bool
}

// DefaultRetryOptions returns default retry configuration.
func DefaultRetryOptions() RetryOptions {
	return RetryOptions{
		MaxAttempts:       3,
		BaseDelay:         100 * time.Millisecond,
		MaxDelay:          10 * time.Second,
		BackoffMultiplier: 2,
		Jitter:            true,
		RetryableErrors:   nil, // Retry all errors by default
	}
}

// Retry executes fn with exponential backoff.
func Retry(ctx context.Context, fn func() error, options RetryOptions) error {
	var lastErr error

	for attempt := 1; attempt <= options.MaxAttempts; attempt++ {
		if err := fn(); err == nil {
			return nil
		} else {
			lastErr = err

			// Check if error is retryable
			if options.RetryableErrors != nil && !options.RetryableErrors(err) {
				return err
			}

			// Last attempt: return error
			if attempt == options.MaxAttempts {
				return &RetryError{
					Attempts:  attempt,
					LastError: lastErr,
				}
			}

			// Calculate backoff delay
			delay := calculateDelay(attempt, options)

			// Wait with context cancellation support
			select {
			case <-time.After(delay):
				// Continue to next attempt
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}

	return lastErr
}

func calculateDelay(attempt int, opts RetryOptions) time.Duration {
	// Exponential backoff
	delay := float64(opts.BaseDelay) * math.Pow(opts.BackoffMultiplier, float64(attempt-1))

	// Apply max delay
	if delay > float64(opts.MaxDelay) {
		delay = float64(opts.MaxDelay)
	}

	// Add jitter (+-50%)
	if opts.Jitter {
		jitterFactor := 0.5 + rand.Float64()
		delay = delay * jitterFactor
	}

	return time.Duration(delay)
}

// Usage
func example(ctx context.Context) error {
	options := RetryOptions{
		MaxAttempts:       5,
		BaseDelay:         200 * time.Millisecond,
		MaxDelay:          10 * time.Second,
		BackoffMultiplier: 2,
		Jitter:            true,
	}

	return Retry(ctx, func() error {
		return fetchData("https://api.example.com/data")
	}, options)
}
```

---

## Retryable Errors

```go
package retry

import (
	"errors"
	"fmt"
	"net/http"
)

// HTTPError represents an HTTP error.
type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.Status, e.Body)
}

// IsRetryable determines if an error should be retried.
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}

	// Context errors are not retryable
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return false
	}

	// HTTP specific errors
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		retryableCodes := map[int]bool{
			http.StatusRequestTimeout:      true, // 408
			http.StatusTooManyRequests:     true, // 429
			http.StatusInternalServerError: true, // 500
			http.StatusBadGateway:          true, // 502
			http.StatusServiceUnavailable:  true, // 503
			http.StatusGatewayTimeout:      true, // 504
		}
		return retryableCodes[httpErr.Status]
	}

	// Network errors (simplified check)
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ETIMEDOUT) {
		return true
	}

	// Database lock/conflict errors
	errMsg := err.Error()
	if contains(errMsg, "lock") || contains(errMsg, "conflict") || contains(errMsg, "deadlock") {
		return true
	}

	return false
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) &&
		(s[:len(substr)] == substr || s[len(s)-len(substr):] == substr ||
		 containsSubstring(s, substr)))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Usage
func fetchUserWithRetry(ctx context.Context, userID string) (*User, error) {
	options := DefaultRetryOptions()
	options.RetryableErrors = IsRetryable

	var user *User
	err := Retry(ctx, func() error {
		var err error
		user, err = apiClient.GetUser(userID)
		return err
	}, options)

	return user, err
}
```

---

## Retry with Cancellation

```go
package retry

import (
	"context"
	"time"
)

// RetryWithAbort executes fn with retry and abort support.
func RetryWithAbort(ctx context.Context, fn func(context.Context) error, options RetryOptions) error {
	var lastErr error

	for attempt := 1; attempt <= options.MaxAttempts; attempt++ {
		// Check cancellation before each attempt
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := fn(ctx); err == nil {
			return nil
		} else {
			lastErr = err

			if options.RetryableErrors != nil && !options.RetryableErrors(err) {
				return err
			}

			if attempt == options.MaxAttempts {
				return &RetryError{
					Attempts:  attempt,
					LastError: lastErr,
				}
			}

			delay := calculateDelay(attempt, options)

			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}

	return lastErr
}

// Usage with global timeout
func example() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	options := RetryOptions{
		MaxAttempts: 5,
		BaseDelay:   200 * time.Millisecond,
		MaxDelay:    10 * time.Second,
	}

	return RetryWithAbort(ctx, func(ctx context.Context) error {
		req, err := http.NewRequestWithContext(ctx, "GET", "/api/data", nil)
		if err != nil {
			return err
		}
		_, err = http.DefaultClient.Do(req)
		return err
	}, options)
}
```

---

## Retry with Generics

```go
package retry

import (
	"context"
)

// RetryFunc executes fn with retry and returns the result.
func RetryFunc[T any](ctx context.Context, fn func() (T, error), options RetryOptions) (T, error) {
	var (
		result  T
		lastErr error
	)

	for attempt := 1; attempt <= options.MaxAttempts; attempt++ {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}

		var err error
		result, err = fn()
		if err == nil {
			return result, nil
		}

		lastErr = err

		if options.RetryableErrors != nil && !options.RetryableErrors(err) {
			return result, err
		}

		if attempt == options.MaxAttempts {
			return result, &RetryError{
				Attempts:  attempt,
				LastError: lastErr,
			}
		}

		delay := calculateDelay(attempt, options)
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return result, ctx.Err()
		}
	}

	return result, lastErr
}

// Usage with typed return value
func fetchUserWithTypedRetry(ctx context.Context, userID string) (*User, error) {
	return RetryFunc(ctx, func() (*User, error) {
		return apiClient.GetUser(userID)
	}, DefaultRetryOptions())
}
```

---

## Recommended Configuration

| Scenario | maxAttempts | baseDelay | maxDelay |
|----------|-------------|-----------|----------|
| Internal API | 3 | 100ms | 1s |
| External API | 5 | 200ms | 10s |
| Database | 3 | 50ms | 500ms |
| File system | 3 | 100ms | 1s |
| Message queue | 5 | 500ms | 30s |

---

## When to Use

- Transient errors (network, timeouts)
- Rate limiting (429 Too Many Requests)
- Temporarily unavailable services (503)
- Optimistic lock conflicts
- Interrupted database connections

---

## When NOT to Use

- Validation errors (400)
- Authentication failure (401, 403)
- Resource not found (404)
- Business logic errors
- Non-idempotent operations without precaution

---

## Related Patterns

| Pattern | Relation |
|---------|----------|
| [Circuit Breaker](circuit-breaker.md) | Use together (retry before circuit) |
| [Timeout](timeout.md) | Limit each attempt |
| [Rate Limiting](rate-limiting.md) | Respect service limits |
| Idempotency | Prerequisite for safe retry |

---

## Sources

- [AWS - Exponential Backoff](https://docs.aws.amazon.com/general/latest/gr/api-retries.html)
- [Google Cloud - Retry Strategy](https://cloud.google.com/storage/docs/retry-strategy)
- [Microsoft - Retry Pattern](https://learn.microsoft.com/en-us/azure/architecture/patterns/retry)
