# Mutex and Semaphore

Synchronization primitives for controlling access to shared resources.

---

## Overview

```
+--------------------------------------------------------------+
|              Mutex vs Semaphore                               |
|                                                               |
|  MUTEX (Mutual Exclusion)         SEMAPHORE                   |
|  Permits: 1                       Permits: N                  |
|                                                               |
|  +--------+                       +--------+                  |
|  |Resource|                       |Resource|                  |
|  +----+---+                       +----+---+                  |
|       |                                |                      |
|       v                                v                      |
|  [1 thread]                       [N threads max]             |
|                                                               |
|  Usage:                           Usage:                      |
|  - Critical section               - Connection pool           |
|  - Single file write              - Rate limiting             |
|  - Singleton init                 - Resource limiting         |
|                                                               |
+--------------------------------------------------------------+
```

---

## Mutex

> Ensures that only one task can access a resource at a time.

```go
package mutex

import (
	"sync"
)

// Mutex wraps sync.Mutex with helpers.
type Mutex struct {
	mu sync.Mutex
}

// NewMutex creates a new mutex.
func NewMutex() *Mutex {
	return &Mutex{}
}

// Lock acquires the mutex.
func (m *Mutex) Lock() {
	m.mu.Lock()
}

// Unlock releases the mutex.
func (m *Mutex) Unlock() {
	m.mu.Unlock()
}

// WithLock executes fn while holding the lock.
func (m *Mutex) WithLock(fn func() error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return fn()
}

// TryLock attempts to acquire the lock without blocking.
func (m *Mutex) TryLock() bool {
	return m.mu.TryLock()
}
```

**Usage:**

```go
package main

import (
	"fmt"
	"os"
	"sync"
)

var fileMutex sync.Mutex

func writeToFile(content string) error {
	fileMutex.Lock()
	defer fileMutex.Unlock()

	f, err := os.OpenFile("log.txt", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.WriteString(content)
	return err
}

func main() {
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		n := i // Capture for closure
		wg.Go(func() { // Go 1.25: handles Add/Done internally
			writeToFile(fmt.Sprintf("Line %d\n", n))
		})
	}

	wg.Wait()
}
```

---

## Semaphore

> Limits concurrent access to N resources.

```go
package semaphore

import (
	"context"
	"fmt"
)

// Semaphore limits concurrent access.
type Semaphore struct {
	permits chan struct{}
}

// NewSemaphore creates a semaphore with n permits.
func NewSemaphore(n int) *Semaphore {
	if n < 1 {
		panic("permits must be >= 1")
	}

	return &Semaphore{
		permits: make(chan struct{}, n),
	}
}

// Acquire acquires n permits.
func (s *Semaphore) Acquire(ctx context.Context, n int) error {
	for i := 0; i < n; i++ {
		select {
		case <-ctx.Done():
			// Release already acquired permits
			for j := 0; j < i; j++ {
				s.Release(1)
			}
			return ctx.Err()
		case s.permits <- struct{}{}:
		}
	}
	return nil
}

// TryAcquire tries to acquire without blocking.
func (s *Semaphore) TryAcquire(n int) bool {
	for i := 0; i < n; i++ {
		select {
		case s.permits <- struct{}{}:
		default:
			// Release already acquired permits
			for j := 0; j < i; j++ {
				s.Release(1)
			}
			return false
		}
	}
	return true
}

// Release releases n permits.
func (s *Semaphore) Release(n int) {
	for i := 0; i < n; i++ {
		<-s.permits
	}
}

// WithPermit executes fn while holding a permit.
func (s *Semaphore) WithPermit(ctx context.Context, fn func() error) error {
	if err := s.Acquire(ctx, 1); err != nil {
		return err
	}
	defer s.Release(1)

	return fn()
}

// Available returns the number of available permits.
func (s *Semaphore) Available() int {
	return cap(s.permits) - len(s.permits)
}
```

**Usage - Limiting API requests:**

```go
package main

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"
)

func main() {
	// Max 5 concurrent requests
	apiSemaphore := NewSemaphore(5)

	urls := make([]string, 100)
	for i := range urls {
		urls[i] = fmt.Sprintf("https://api.example.com/%d", i)
	}

	var wg sync.WaitGroup
	ctx := context.Background()

	for _, url := range urls {
		u := url // Capture for closure
		wg.Go(func() { // Go 1.25: handles Add/Done internally
			err := apiSemaphore.WithPermit(ctx, func() error {
				resp, err := http.Get(u)
				if err != nil {
					return err
				}
				defer resp.Body.Close()

				fmt.Printf("Fetched %s: %d\n", u, resp.StatusCode)
				return nil
			})

			if err != nil {
				fmt.Printf("Error: %v\n", err)
			}
		})
	}

	wg.Wait()
}
```

---

## Semaphore with golang.org/x/sync

Go provides an official implementation:

```go
package main

import (
	"context"
	"fmt"

	"golang.org/x/sync/semaphore"
)

func main() {
	// Create weighted semaphore
	sem := semaphore.NewWeighted(5)

	ctx := context.Background()

	// Acquire
	if err := sem.Acquire(ctx, 1); err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	defer sem.Release(1)

	// Do work
	fmt.Println("Processing...")
}
```

---

## Common Patterns

### Rate Limiter

```go
package ratelimit

import (
	"context"
	"time"
)

// RateLimiter limits operations per time window.
type RateLimiter struct {
	sem      *Semaphore
	refill   *time.Ticker
	maxRate  int
	done     chan struct{}
}

// NewRateLimiter creates a rate limiter.
func NewRateLimiter(maxRequests int, window time.Duration) *RateLimiter {
	rl := &RateLimiter{
		sem:     NewSemaphore(maxRequests),
		refill:  time.NewTicker(window),
		maxRate: maxRequests,
		done:    make(chan struct{}),
	}

	go rl.refiller()

	return rl
}

// refiller periodically refills permits.
func (rl *RateLimiter) refiller() {
	for {
		select {
		case <-rl.refill.C:
			available := rl.sem.Available()
			toRefill := rl.maxRate - available
			if toRefill > 0 {
				rl.sem.Release(toRefill)
			}
		case <-rl.done:
			return
		}
	}
}

// Acquire acquires a permit for rate limiting.
func (rl *RateLimiter) Acquire(ctx context.Context) error {
	return rl.sem.Acquire(ctx, 1)
}

// Stop stops the rate limiter.
func (rl *RateLimiter) Stop() {
	rl.refill.Stop()
	close(rl.done)
}
```

### Resource Guard

```go
package guard

import (
	"sync"
)

// ResourceGuard protects a resource with a mutex.
type ResourceGuard[T any] struct {
	resource T
	mu       sync.Mutex
}

// NewResourceGuard creates a guarded resource.
func NewResourceGuard[T any](resource T) *ResourceGuard[T] {
	return &ResourceGuard[T]{
		resource: resource,
	}
}

// Use executes fn with exclusive access to the resource.
func (rg *ResourceGuard[T]) Use(fn func(*T) error) error {
	rg.mu.Lock()
	defer rg.mu.Unlock()

	return fn(&rg.resource)
}
```

---

## Complexity and Trade-offs

| Operation | Mutex | Semaphore |
|-----------|-------|-----------|
| acquire (no wait) | O(1) | O(n) n=permits |
| release | O(1) | O(n) |
| Memory | O(1) | O(capacity) |

### Mutex vs Semaphore

| Criterion | Mutex | Semaphore |
|-----------|-------|-----------|
| Permits | 1 | N |
| Owner | Yes (the one who locks) | No |
| Usage | Critical section | Resource pool |

### Advantages

- Race condition prevention
- Concurrency control
- Simple to understand
- Native in Go stdlib

### Disadvantages

- Risk of deadlock
- Contention = latency
- Priority inversion possible

---

## When to Use

| Situation | Recommended |
|-----------|-------------|
| Exclusive access to a resource | Mutex |
| Pool of N resources | Semaphore(N) |
| Rate limiting | Semaphore + timer |
| Read-heavy workload | sync.RWMutex |

---

## Related Patterns

| Pattern | Relation |
|---------|----------|
| **Read-Write Lock** | Specialization for reads |
| **Monitor** | Mutex + conditions |
| **Object Pool** | Semaphore for limiting |
| **Circuit Breaker** | Different protection |

---

## Sources

- [Go sync.Mutex](https://pkg.go.dev/sync#Mutex)
- [golang.org/x/sync/semaphore](https://pkg.go.dev/golang.org/x/sync/semaphore)
- [The Little Book of Semaphores](https://greenteapress.com/wp/semaphores/)
