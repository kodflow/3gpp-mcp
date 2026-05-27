# Debounce and Throttle

Patterns for limiting function execution frequency.

---

## Overview

```
+--------------------------------------------------------------+
|  Events:  X X X X X    X X X X X         X X              |
|               |-wait-|    |-wait-|                            |
|                                                               |
|  Debounce:              X              X              X       |
|                         ^              ^              ^       |
|                 (after silence)  (after silence)              |
|                                                               |
|  Throttle:    X         X         X         X         X       |
|               ^---------^---------^---------^---------^       |
|               (regular interval, max 1 per period)            |
+--------------------------------------------------------------+
```

| Pattern | Behavior | Use Case |
|---------|----------|----------|
| **Debounce** | Executes after an inactivity delay | Search, resize |
| **Throttle** | Executes max 1 time per interval | Scroll, animations |

---

## Debounce

> Wait until the user stops acting before executing.

```go
package debounce

import (
	"sync"
	"time"
)

// Debouncer delays function execution until after delay.
type Debouncer struct {
	delay time.Duration
	timer *time.Timer
	mu    sync.Mutex
}

// New creates a new debouncer with given delay.
func New(delay time.Duration) *Debouncer {
	return &Debouncer{
		delay: delay,
	}
}

// Debounce schedules fn to execute after delay of inactivity.
func (d *Debouncer) Debounce(fn func()) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.timer != nil {
		d.timer.Stop()
	}

	d.timer = time.AfterFunc(d.delay, fn)
}

// Cancel cancels any pending execution.
func (d *Debouncer) Cancel() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
}

// Usage
// debouncer := debounce.New(300 * time.Millisecond)
//
// for event := range events {
//     debouncer.Debounce(func() {
//         search(event.Query)
//     })
// }
```

### Debounce with Options

```go
package debounce

import (
	"sync"
	"time"
)

// Options configure debounce behavior.
type Options struct {
	Leading  bool          // Execute at start
	Trailing bool          // Execute at end
	MaxWait  time.Duration // Force execution after max wait
}

// Advanced is a debouncer with advanced options.
type Advanced struct {
	delay       time.Duration
	opts        Options
	timer       *time.Timer
	maxTimer    *time.Timer
	lastCallTime time.Time
	mu          sync.Mutex
}

// NewAdvanced creates a new advanced debouncer.
func NewAdvanced(delay time.Duration, opts Options) *Advanced {
	if !opts.Leading && !opts.Trailing {
		opts.Trailing = true
	}
	return &Advanced{
		delay: delay,
		opts:  opts,
	}
}

// Debounce schedules fn execution with options.
func (d *Advanced) Debounce(fn func()) {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()
	isFirstCall := d.lastCallTime.IsZero()
	d.lastCallTime = now

	// Leading edge
	if d.opts.Leading && isFirstCall {
		fn()
	}

	// Cancel existing timers
	if d.timer != nil {
		d.timer.Stop()
	}
	if d.maxTimer != nil {
		d.maxTimer.Stop()
		d.maxTimer = nil
	}

	// Trailing edge
	if d.opts.Trailing {
		d.timer = time.AfterFunc(d.delay, func() {
			d.mu.Lock()
			fn()
			d.lastCallTime = time.Time{}
			d.mu.Unlock()
		})
	}

	// Max wait
	if d.opts.MaxWait > 0 && d.maxTimer == nil {
		d.maxTimer = time.AfterFunc(d.opts.MaxWait, func() {
			d.mu.Lock()
			defer d.mu.Unlock()
			if d.timer != nil {
				d.timer.Stop()
			}
			fn()
		})
	}
}
```

---

## Throttle

> Limit to one execution per time interval.

```go
package throttle

import (
	"sync"
	"time"
)

// Throttler limits function execution rate.
type Throttler struct {
	limit    time.Duration
	lastRun  time.Time
	timer    *time.Timer
	pending  func()
	mu       sync.Mutex
}

// New creates a new throttler with given limit.
func New(limit time.Duration) *Throttler {
	return &Throttler{
		limit: limit,
	}
}

// Throttle executes fn at most once per limit period.
func (t *Throttler) Throttle(fn func()) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()

	if now.Sub(t.lastRun) >= t.limit {
		// Execute immediately
		fn()
		t.lastRun = now
		return
	}

	// Schedule for later
	t.pending = fn

	if t.timer == nil {
		remaining := t.limit - now.Sub(t.lastRun)
		t.timer = time.AfterFunc(remaining, func() {
			t.mu.Lock()
			defer t.mu.Unlock()

			if t.pending != nil {
				t.pending()
				t.lastRun = time.Now()
				t.pending = nil
			}
			t.timer = nil
		})
	}
}

// Usage
// throttler := throttle.New(100 * time.Millisecond)
//
// window.OnScroll(func() {
//     throttler.Throttle(func() {
//         updateNavbar()
//         loadMoreIfNeeded()
//     })
// })
```

### Throttle with Options

```go
package throttle

import (
	"sync"
	"time"
)

// Options configure throttle behavior.
type Options struct {
	Leading  bool // Execute at start of period
	Trailing bool // Execute at end of period
}

// Advanced is a throttler with advanced options.
type Advanced struct {
	limit    time.Duration
	opts     Options
	lastRun  time.Time
	timer    *time.Timer
	pending  func()
	mu       sync.Mutex
}

// NewAdvanced creates a new advanced throttler.
func NewAdvanced(limit time.Duration, opts Options) *Advanced {
	if !opts.Leading && !opts.Trailing {
		opts.Leading = true
	}
	return &Advanced{
		limit: limit,
		opts:  opts,
	}
}

// Throttle executes fn according to options.
func (t *Advanced) Throttle(fn func()) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	remaining := t.limit - now.Sub(t.lastRun)

	t.pending = fn

	if remaining <= 0 || remaining > t.limit {
		if t.timer != nil {
			t.timer.Stop()
			t.timer = nil
		}
		if t.opts.Leading {
			fn()
			t.lastRun = now
		}
	} else if t.timer == nil && t.opts.Trailing {
		t.timer = time.AfterFunc(remaining, func() {
			t.mu.Lock()
			defer t.mu.Unlock()

			if t.pending != nil {
				t.pending()
				t.lastRun = time.Now()
			}
			t.timer = nil
		})
	}
}
```

---

## Visual Comparison

```
Events: | | | | |     | | | |       | |

Debounce (300ms):
                    X           X         X
                    ^-- 300ms after last event

Throttle (300ms):
            X       X     X     X       X
            ^-------^-----^-----^-------^-- max 1 per 300ms
```

---

## Use Cases

### Debounce Use Cases

```go
package examples

import (
	"time"
)

// Form validation
func validateEmailDebounced(email string) {
	debouncer := debounce.New(500 * time.Millisecond)
	debouncer.Debounce(func() {
		checkEmailAvailable(email)
	})
}

// Auto-save
func autoSave(content string) {
	saver := debounce.New(1 * time.Second)
	saver.Debounce(func() {
		saveDraft(content)
	})
}

func checkEmailAvailable(email string) {}
func saveDraft(content string)         {}
```

### Throttle Use Cases

```go
package examples

import "time"

// Infinite scroll
func loadMoreThrottled() {
	throttler := throttle.New(200 * time.Millisecond)
	throttler.Throttle(func() {
		if isNearBottom() {
			fetchNextPage()
		}
	})
}

// Analytics (60fps = ~16ms)
func trackScrollThrottled(depth int) {
	tracker := throttle.New(16 * time.Millisecond)
	tracker.Throttle(func() {
		trackEvent("scroll", depth)
	})
}

func isNearBottom() bool       { return false }
func fetchNextPage()           {}
func trackEvent(s string, i int) {}
```

---

## Complexity and Trade-offs

| Aspect | Debounce | Throttle |
|--------|----------|----------|
| Latency | Guaranteed delay | Immediate execution possible |
| Frequency | Variable | Bounded |
| Memory | O(1) | O(1) |

### When to Use Which

| Situation | Pattern |
|-----------|---------|
| Wait for end of input | Debounce |
| Limit API requests | Debounce |
| Smooth animation | Throttle |
| High-frequency events | Throttle |
| Auto-save | Debounce + maxWait |

---

## When to Use

- Instant search (autocomplete) with debounce to avoid excessive requests
- Automatic form saving with debounce to wait for end of input
- Infinite scroll management with throttle to limit loading calls
- Analytics tracking with throttle to avoid server overload
- Window resizing with debounce for expensive recalculations

---

## Related Patterns

| Pattern | Relation |
|---------|----------|
| **Rate Limiter** | Server-side throttle |
| **Circuit Breaker** | Overload protection |
| **Batch Processing** | Group instead of limit |

---

## Sources

- [Lodash debounce/throttle](https://lodash.com/docs/4.17.15#debounce)
- [CSS-Tricks - Debouncing and Throttling](https://css-tricks.com/debouncing-throttling-explained-examples/)
