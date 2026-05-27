# Lazy Loading

Pattern deferring resource initialization until its first use.

---

## What is Lazy Loading?

> Only load/initialize a resource when it is actually needed.

```
+--------------------------------------------------------------+
|                     Lazy Loading                              |
|                                                               |
|  First access:                                                |
|                                                               |
|  get() --> [null?] --> YES --> create() --> cache --> return  |
|                |                                              |
|                NO                                             |
|                |                                              |
|                +-----> return cached                          |
|                                                               |
|  Subsequent accesses:                                         |
|                                                               |
|  get() --> [cached] --> return (instantaneous)                |
|                                                               |
+--------------------------------------------------------------+
```

**Why:**

- Reduce startup time
- Save memory (unused resources)
- Avoid side effects at load time

---

## Go Implementation

### Lazy Value with sync.OnceValue (Go 1.21+ - RECOMMENDED)

```go
package lazy

import (
	"sync"
)

// For simple cases without reset or error, use sync.OnceValue directly:
var expensiveResource = sync.OnceValue(func() *Dataset {
	log.Println("Creating expensive resource...")
	return loadHugeDataset()
})

// With error handling, use sync.OnceValues:
var config = sync.OnceValues(func() (*Config, error) {
	return loadConfig()
})

// Usage:
// dataset := expensiveResource()  // First call: loads
// dataset2 := expensiveResource() // Subsequent calls: cached
//
// cfg, err := config()
// if err != nil { ... }
```

### Lazy Value struct (if reset is needed)

```go
package lazy

import (
	"sync"
)

// Value holds a lazily-initialized value with reset capability.
// For simple cases without reset, prefer sync.OnceValue.
type Value[T any] struct {
	factory func() T
	value   T
	once    sync.Once
}

// New creates a new lazy value.
func New[T any](factory func() T) *Value[T] {
	return &Value[T]{
		factory: factory,
	}
}

// Get returns the value, initializing it if needed.
func (lv *Value[T]) Get() T {
	lv.once.Do(func() {
		lv.value = lv.factory()
	})
	return lv.value
}

// Usage
// expensiveResource := lazy.New(func() *Dataset {
//     log.Println("Creating expensive resource...")
//     return loadHugeDataset()
// })
//
// No loading here
// log.Println("App started")
//
// Loading on first access
// data := expensiveResource.Get()
```

### Lazy Async

```go
package lazy

import (
	"context"
	"sync"
)

// AsyncValue holds a lazily-initialized async value.
type AsyncValue[T any] struct {
	factory func(context.Context) (T, error)
	value   T
	err     error
	once    sync.Once
}

// NewAsync creates a new async lazy value.
func NewAsync[T any](factory func(context.Context) (T, error)) *AsyncValue[T] {
	return &AsyncValue[T]{
		factory: factory,
	}
}

// Get returns the value, initializing it if needed.
func (lv *AsyncValue[T]) Get(ctx context.Context) (T, error) {
	lv.once.Do(func() {
		lv.value, lv.err = lv.factory(ctx)
	})
	return lv.value, lv.err
}

// Usage
// lazyDb := lazy.NewAsync(func(ctx context.Context) (*sql.DB, error) {
//     db, err := sql.Open("postgres", connString)
//     if err != nil {
//         return nil, err
//     }
//     return db, db.PingContext(ctx)
// })
//
// Connection only on first call
// db, err := lazyDb.Get(ctx)
```

### Lazy with Reset

```go
package lazy

import "sync"

// Resettable is a lazy value that can be reset.
type Resettable[T any] struct {
	factory     func() T
	value       T
	initialized bool
	mu          sync.RWMutex
}

// NewResettable creates a new resettable lazy value.
func NewResettable[T any](factory func() T) *Resettable[T] {
	return &Resettable[T]{
		factory: factory,
	}
}

// Get returns the value, initializing if needed.
func (r *Resettable[T]) Get() T {
	r.mu.RLock()
	if r.initialized {
		val := r.value
		r.mu.RUnlock()
		return val
	}
	r.mu.RUnlock()

	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.initialized {
		r.value = r.factory()
		r.initialized = true
	}
	return r.value
}

// IsInitialized returns true if the value has been initialized.
func (r *Resettable[T]) IsInitialized() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.initialized
}

// Reset clears the cached value.
func (r *Resettable[T]) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	var zero T
	r.value = zero
	r.initialized = false
}
```

---

## Pattern Variants

### 1. Virtual Proxy

```go
package proxy

import (
	"log"
	"sync"
)

// Image interface.
type Image interface {
	Display()
	GetWidth() int
}

// RealImage is the actual image implementation.
type RealImage struct {
	filename string
	width    int
}

// NewRealImage loads an image from disk.
func NewRealImage(filename string) *RealImage {
	log.Printf("Loading image: %s", filename)
	return &RealImage{
		filename: filename,
		width:    1920,
	}
}

// Display shows the image.
func (ri *RealImage) Display() {
	log.Printf("Displaying %s", ri.filename)
}

// GetWidth returns image width.
func (ri *RealImage) GetWidth() int {
	return ri.width
}

// LazyImageProxy delays image loading until first use.
type LazyImageProxy struct {
	filename string
	image    *RealImage
	once     sync.Once
}

// NewLazyImageProxy creates a new lazy image proxy.
func NewLazyImageProxy(filename string) *LazyImageProxy {
	return &LazyImageProxy{
		filename: filename,
	}
}

func (lip *LazyImageProxy) loadImage() *RealImage {
	lip.once.Do(func() {
		lip.image = NewRealImage(lip.filename)
	})
	return lip.image
}

// Display shows the image.
func (lip *LazyImageProxy) Display() {
	lip.loadImage().Display()
}

// GetWidth returns image width.
func (lip *LazyImageProxy) GetWidth() int {
	return lip.loadImage().GetWidth()
}
```

### 2. Ghost Object

```go
package domain

import (
	"context"
	"sync"
)

// UserProfile represents user profile data.
type UserProfile struct {
	Bio    string
	Avatar string
}

// LazyUser is a user that loads data on demand.
type LazyUser struct {
	id      string
	email   string
	profile *UserProfile
	loaded  bool
	mu      sync.RWMutex
}

// NewLazyUser creates a new lazy user with just an ID.
func NewLazyUser(id string) *LazyUser {
	return &LazyUser{
		id: id,
	}
}

func (lu *LazyUser) ensureLoaded(ctx context.Context) error {
	lu.mu.RLock()
	if lu.loaded {
		lu.mu.RUnlock()
		return nil
	}
	lu.mu.RUnlock()

	lu.mu.Lock()
	defer lu.mu.Unlock()

	if lu.loaded {
		return nil
	}

	data, err := fetchUserFromDB(ctx, lu.id)
	if err != nil {
		return err
	}

	lu.email = data.Email
	lu.profile = data.Profile
	lu.loaded = true
	return nil
}

// GetEmail returns the user's email.
func (lu *LazyUser) GetEmail(ctx context.Context) (string, error) {
	if err := lu.ensureLoaded(ctx); err != nil {
		return "", err
	}
	lu.mu.RLock()
	defer lu.mu.RUnlock()
	return lu.email, nil
}

// GetProfile returns the user's profile.
func (lu *LazyUser) GetProfile(ctx context.Context) (*UserProfile, error) {
	if err := lu.ensureLoaded(ctx); err != nil {
		return nil, err
	}
	lu.mu.RLock()
	defer lu.mu.RUnlock()
	return lu.profile, nil
}

type userData struct {
	Email   string
	Profile *UserProfile
}

func fetchUserFromDB(ctx context.Context, id string) (*userData, error) {
	// Implementation
	return &userData{}, nil
}
```

### 3. Lazy Collection

```go
package collection

import "sync"

// LazyArray loads items on demand.
type LazyArray[T any] struct {
	length int
	loader func(int) T
	items  map[int]T
	mu     sync.RWMutex
}

// NewLazyArray creates a new lazy array.
func NewLazyArray[T any](length int, loader func(int) T) *LazyArray[T] {
	return &LazyArray[T]{
		length: length,
		loader: loader,
		items:  make(map[int]T),
	}
}

// Get returns the item at index, loading if needed.
func (la *LazyArray[T]) Get(index int) T {
	la.mu.RLock()
	if item, ok := la.items[index]; ok {
		la.mu.RUnlock()
		return item
	}
	la.mu.RUnlock()

	la.mu.Lock()
	defer la.mu.Unlock()

	if item, ok := la.items[index]; ok {
		return item
	}

	item := la.loader(index)
	la.items[index] = item
	return item
}

// Len returns the array length.
func (la *LazyArray[T]) Len() int {
	return la.length
}
```

---

## Complexity and Trade-offs

| Aspect | Value |
|--------|-------|
| First access | O(init) |
| Subsequent accesses | O(1) |
| Memory before init | O(1) |
| Memory after init | O(resource) |

### Advantages

- Fast startup
- Memory savings if unused
- On-demand loading

### Disadvantages

- Latency on first access
- Code complexity
- Deferred error handling

---

## When to Use

| Situation | Recommended |
|-----------|------------|
| Expensive optional resource | Yes |
| Optimize startup time | Yes |
| Circular dependencies | Yes (breaks the cycle) |
| Resource always used | No (unnecessary overhead) |
| Critical real-time access | No (first access latency) |

---

## Related Patterns

| Pattern | Relation |
|---------|----------|
| **Proxy** | Encapsulates lazy loading |
| **Singleton** | Often combined with lazy |
| **Factory** | Creates the object during init |
| **Memoization** | Similar result caching |

---

## Sources

- [Martin Fowler - Lazy Load](https://martinfowler.com/eaaCatalog/lazyLoad.html)
- [Patterns of Enterprise Application Architecture](https://www.martinfowler.com/books/eaa.html)
