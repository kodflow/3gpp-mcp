# Memoization

Pattern for caching function results to avoid recomputation.

---

## What is Memoization?

> Store the result of a function call and return it directly on identical calls.

```
+--------------------------------------------------------------+
|                      Memoization                              |
|                                                               |
|  fn(a, b) ----+                                               |
|               |                                               |
|               v                                               |
|         +-----------+                                         |
|         |   Cache   |                                         |
|         |-----------|                                         |
|         | key(a,b)  |--- HIT ---> return cached result        |
|         +-----------+                                         |
|               |                                               |
|             MISS                                              |
|               |                                               |
|               v                                               |
|         compute(a, b)                                         |
|               |                                               |
|               v                                               |
|         store in cache                                        |
|               |                                               |
|               v                                               |
|         return result                                         |
+--------------------------------------------------------------+
```

**Prerequisite:** The function must be **pure** (same inputs = same output).

---

## Go Implementation

### Simple Memoize

```go
package memoize

import (
	"fmt"
	"sync"
)

// Func1 represents a function with one parameter.
type Func1[T, R any] func(T) R

// Memoize1 memoizes a function with one parameter.
func Memoize1[T comparable, R any](fn Func1[T, R]) Func1[T, R] {
	cache := make(map[T]R)
	var mu sync.RWMutex

	return func(arg T) R {
		mu.RLock()
		if result, ok := cache[arg]; ok {
			mu.RUnlock()
			return result
		}
		mu.RUnlock()

		mu.Lock()
		defer mu.Unlock()

		// Double-check after acquiring write lock
		if result, ok := cache[arg]; ok {
			return result
		}

		result := fn(arg)
		cache[arg] = result
		return result
	}
}

// Usage
func Factorial(n int) int {
	if n <= 1 {
		return 1
	}
	return n * FactorialMemo(n-1)
}

var FactorialMemo = Memoize1(Factorial)

// Example
// FactorialMemo(100) // Computes
// FactorialMemo(100) // Cache hit
// FactorialMemo(99)  // Cache hit (computed during FactorialMemo(100))
```

### Memoize with Options

```go
package memoize

import (
	"sync"
	"time"
)

// Options configure memoization behavior.
type Options struct {
	MaxSize int
	TTL     time.Duration
}

// Cache entry with timestamp.
type entry[R any] struct {
	value     R
	timestamp time.Time
}

// MemoizeWithOptions memoizes with TTL and size limits.
func MemoizeWithOptions[T comparable, R any](
	fn Func1[T, R],
	opts Options,
) Func1[T, R] {
	cache := make(map[T]entry[R])
	var mu sync.RWMutex

	return func(arg T) R {
		mu.RLock()
		if e, ok := cache[arg]; ok {
			if opts.TTL == 0 || time.Since(e.timestamp) < opts.TTL {
				mu.RUnlock()
				return e.value
			}
		}
		mu.RUnlock()

		mu.Lock()
		defer mu.Unlock()

		// Double-check
		if e, ok := cache[arg]; ok {
			if opts.TTL == 0 || time.Since(e.timestamp) < opts.TTL {
				return e.value
			}
			delete(cache, arg)
		}

		result := fn(arg)

		// LRU eviction if needed
		if opts.MaxSize > 0 && len(cache) >= opts.MaxSize {
			// Remove oldest entry (simple FIFO for example)
			for k := range cache {
				delete(cache, k)
				break
			}
		}

		cache[arg] = entry[R]{
			value:     result,
			timestamp: time.Now(),
		}

		return result
	}
}

// Usage with TTL
// fetchUserCached := MemoizeWithOptions(
//     fetchUser,
//     Options{TTL: 60 * time.Second},
// )
```

### Async Memoize

```go
package memoize

import (
	"context"
	"sync"
)

// AsyncFunc1 represents an async function with one parameter.
type AsyncFunc1[T, R any] func(context.Context, T) (R, error)

// MemoizeAsync memoizes an async function.
func MemoizeAsync[T comparable, R any](
	fn AsyncFunc1[T, R],
) AsyncFunc1[T, R] {
	type result struct {
		value R
		err   error
	}
	type pending struct {
		done chan struct{}
		res  result
	}

	cache := make(map[T]*pending)
	var mu sync.Mutex

	return func(ctx context.Context, arg T) (R, error) {
		mu.Lock()
		if p, ok := cache[arg]; ok {
			mu.Unlock()
			<-p.done
			return p.res.value, p.res.err
		}

		p := &pending{
			done: make(chan struct{}),
		}
		cache[arg] = p
		mu.Unlock()

		// Compute
		value, err := fn(ctx, arg)
		p.res = result{value: value, err: err}

		// Clean up on error
		if err != nil {
			mu.Lock()
			delete(cache, arg)
			mu.Unlock()
		}

		close(p.done)
		return value, err
	}
}

// Usage
// getUserProfile := MemoizeAsync(func(ctx context.Context, userID string) (*User, error) {
//     return api.GetUser(ctx, userID)
// })
//
// Two simultaneous calls = a single request
// profile1, _ := getUserProfile(ctx, "123")
// profile2, _ := getUserProfile(ctx, "123")
```

---

## Classic Use Cases

### Fibonacci

```go
package main

import "fmt"

func fibonacci(n int) int {
	if n <= 1 {
		return n
	}
	return fibonacciMemo(n-1) + fibonacciMemo(n-2)
}

var fibonacciMemo = memoize.Memoize1(fibonacci)

// Without memoization: O(2^n)
// With memoization: O(n)
func main() {
	fmt.Println(fibonacciMemo(50)) // Instantaneous
}
```

### Expensive Parsing

```go
package parser

import "html"

func parseMarkdown(content string) string {
	// Expensive parsing
	return html.EscapeString(content)
}

var ParseMarkdownMemo = memoize.Memoize1(parseMarkdown)
```

### Derived Computations

```go
package stats

import "math"

// Stats represents statistical measures.
type Stats struct {
	Mean   float64
	Median float64
	StdDev float64
}

// DataProcessor computes statistics.
type DataProcessor struct {
	computeStats func([]float64) Stats
}

// NewDataProcessor creates a new processor with memoization.
func NewDataProcessor() *DataProcessor {
	return &DataProcessor{
		computeStats: memoize.MemoizeWithOptions(
			computeStatsImpl,
			memoize.Options{MaxSize: 100},
		),
	}
}

func computeStatsImpl(data []float64) Stats {
	return Stats{
		Mean:   mean(data),
		Median: median(data),
		StdDev: stdDev(data),
	}
}

func mean(data []float64) float64 {
	var sum float64
	for _, v := range data {
		sum += v
	}
	return sum / float64(len(data))
}

func median(data []float64) float64 {
	// Implementation
	return 0
}

func stdDev(data []float64) float64 {
	m := mean(data)
	var variance float64
	for _, v := range data {
		variance += math.Pow(v-m, 2)
	}
	return math.Sqrt(variance / float64(len(data)))
}

// GetStats returns cached statistics.
func (dp *DataProcessor) GetStats(data []float64) Stats {
	return dp.computeStats(data)
}
```

---

## Complexity and Trade-offs

| Aspect | Without memo | With memo |
|--------|-----------|-----------|
| Time (n identical calls) | O(n * compute) | O(compute + n) |
| Memory | O(1) | O(unique_calls) |

### Advantages

- Dramatic speedup for repeated computations
- Transparent to the caller
- Simple to implement

### Disadvantages

- Growing memory consumption
- Pure functions only
- Cache key can be expensive (JSON.stringify)

---

## When to Use

| Situation | Recommended |
|-----------|------------|
| Expensive pure function | Yes |
| Repeated calls with same args | Yes |
| Recursive computations | Yes |
| Functions with side effects | No |
| Results changing over time | No (or with TTL) |

---

## Related Patterns

| Pattern | Relation |
|---------|----------|
| **Cache** | More general, not tied to a function |
| **Lazy Loading** | Similar deferred initialization |
| **Flyweight** | Object sharing vs result sharing |
| **Decorator** | Wraps the original function |

---

## Sources

- [Wikipedia - Memoization](https://en.wikipedia.org/wiki/Memoization)
- [Lodash memoize](https://lodash.com/docs/4.17.15#memoize)
- [React useMemo](https://react.dev/reference/react/useMemo)
