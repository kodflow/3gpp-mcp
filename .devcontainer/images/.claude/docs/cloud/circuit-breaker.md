# Circuit Breaker Pattern

> Prevent cascade failures in distributed systems.

## Principle

```
        ┌──────────────────────────────────────────────────────┐
        │                    CIRCUIT BREAKER                    │
        │                                                       │
        │   CLOSED ──────▶ OPEN ──────▶ HALF-OPEN              │
        │     │              │              │                   │
        │     │ failures     │ timeout      │ success           │
        │     │ > threshold  │ expires      │ → CLOSED          │
        │     │              │              │ failure           │
        │     │              │              │ → OPEN            │
        │     ▼              ▼              ▼                   │
        └──────────────────────────────────────────────────────┘

┌─────────┐         ┌──────────────┐         ┌─────────┐
│ Service │ ──────▶ │Circuit Breaker│ ──────▶ │ Remote  │
│   A     │         │              │         │ Service │
└─────────┘         └──────────────┘         └─────────┘
```

## States

| State | Behavior |
|-------|----------|
| **CLOSED** | Requests pass normally. Counts failures. |
| **OPEN** | Requests fail immediately (fail fast). |
| **HALF-OPEN** | Allows a few test requests. |

## Go Example

```go
package circuitbreaker

import (
	"errors"
	"sync"
	"time"
)

// State represents the circuit breaker state.
type State string

const (
	StateClosed   State = "CLOSED"
	StateOpen     State = "OPEN"
	StateHalfOpen State = "HALF_OPEN"
)

// CircuitOpenError is returned when circuit is open.
var CircuitOpenError = errors.New("circuit breaker is open")

// CircuitBreaker implements the circuit breaker pattern.
type CircuitBreaker struct {
	mu            sync.RWMutex
	state         State
	failures      int
	lastFailure   time.Time
	threshold     int
	timeout       time.Duration
}

// NewCircuitBreaker creates a new CircuitBreaker.
func NewCircuitBreaker(threshold int, timeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		state:     StateClosed,
		threshold: threshold,
		timeout:   timeout,
	}
}

// Call executes the function with circuit breaker protection.
func (cb *CircuitBreaker) Call(fn func() error) error {
	cb.mu.RLock()
	state := cb.state
	lastFailure := cb.lastFailure
	cb.mu.RUnlock()

	if state == StateOpen {
		if time.Since(lastFailure) > cb.timeout {
			cb.mu.Lock()
			cb.state = StateHalfOpen
			cb.mu.Unlock()
		} else {
			return CircuitOpenError
		}
	}

	err := fn()
	if err != nil {
		cb.onFailure()
		return err
	}

	cb.onSuccess()
	return nil
}

func (cb *CircuitBreaker) onSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures = 0
	cb.state = StateClosed
}

func (cb *CircuitBreaker) onFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures++
	cb.lastFailure = time.Now()

	if cb.failures >= cb.threshold {
		cb.state = StateOpen
	}
}

// State returns the current circuit breaker state.
func (cb *CircuitBreaker) State() State {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}
```

## Usage

```go
// This example follows the same idiomatic Go patterns
// as the main example above.
// Specific implementation based on standard Go
// interfaces and conventions.
```

## Recommended Configuration

| Parameter | Typical Value | Description |
|-----------|---------------|-------------|
| `threshold` | 5-10 | Failures before opening |
| `timeout` | 30-60s | Time before HALF_OPEN |
| `halfOpenRequests` | 1-3 | Test requests in HALF_OPEN |

## Libraries

| Language | Library |
|----------|---------|
| Node.js | `opossum`, `cockatiel` |
| Java | Resilience4j, Hystrix (deprecated) |
| Go | `sony/gobreaker` |
| Python | `pybreaker` |

## When to Use

- Calls to external services that may fail or be slow
- Prevention of cascade failures in distributed systems
- Protection of limited resources against overload
- Services with critical SLAs requiring graceful degradation
- Systems requiring automatic recovery after incidents

## Related Patterns

| Pattern | Relation |
|---------|----------|
| Retry | Before circuit breaker |
| Bulkhead | Resource isolation |
| Fallback | Alternative when circuit open |
| Health Check | Circuit monitoring |

## Sources

- [Microsoft - Circuit Breaker](https://learn.microsoft.com/en-us/azure/architecture/patterns/circuit-breaker)
- [Martin Fowler - Circuit Breaker](https://martinfowler.com/bliki/CircuitBreaker.html)
