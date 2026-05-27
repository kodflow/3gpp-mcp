# Observer Pattern

> Define a one-to-many dependency between objects to notify changes.

## Intent

Define a subscription mechanism to notify multiple objects of any
state change in the object they observe.

## Classic Structure

```go
package main

import (
	"fmt"
	"sync"
)

// Observer is notified of changes.
type Observer[T any] interface {
	Update(data T)
}

// Subject manages observers and notifies them.
type Subject[T any] interface {
	Subscribe(observer Observer[T])
	Unsubscribe(observer Observer[T])
	Notify(data T)
}

// EventEmitter is a basic subject implementation.
type EventEmitter[T any] struct {
	mu        sync.RWMutex
	observers map[Observer[T]]struct{}
}

// NewEventEmitter creates a new event emitter.
func NewEventEmitter[T any]() *EventEmitter[T] {
	return &EventEmitter[T]{
		observers: make(map[Observer[T]]struct{}),
	}
}

// Subscribe adds an observer.
func (e *EventEmitter[T]) Subscribe(observer Observer[T]) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.observers[observer] = struct{}{}
}

// Unsubscribe removes an observer.
func (e *EventEmitter[T]) Unsubscribe(observer Observer[T]) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.observers, observer)
}

// Notify sends data to all observers.
func (e *EventEmitter[T]) Notify(data T) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for observer:= range e.observers {
		observer.Update(data)
	}
}

// PriceDisplay observes price changes.
type PriceDisplay struct {
	name string
}

// NewPriceDisplay creates a new price display.
func NewPriceDisplay(name string) *PriceDisplay {
	return &PriceDisplay{name: name}
}

// Update handles price updates.
func (p *PriceDisplay) Update(price float64) {
	fmt.Printf("%s: Price updated to $%.2f\n", p.name, price)
}

// Stock is an observable with state.
type Stock struct {
	*EventEmitter[float64]
	price float64
}

// NewStock creates a new stock.
func NewStock() *Stock {
	return &Stock{
		EventEmitter: NewEventEmitter[float64](),
	}
}

// Price returns the current price.
func (s *Stock) Price() float64 {
	return s.price
}

// SetPrice updates the price and notifies observers.
func (s *Stock) SetPrice(value float64) {
	s.price = value
	s.Notify(value)
}

// Usage
func classicExample() {
	apple:= NewStock()
	display1:= NewPriceDisplay("Terminal 1")
	display2:= NewPriceDisplay("Terminal 2")

	apple.Subscribe(display1)
	apple.Subscribe(display2)
	apple.SetPrice(150) // Les deux displays sont notifies
}
```

## Modern Event Emitter (type-safe)

```go
// EventCallback is a typed event callback.
type EventCallback[T any] func(data T)

// TypedEventEmitter manages typed events.
type TypedEventEmitter[K comparable, V any] struct {
	mu        sync.RWMutex
	listeners map[K][]EventCallback[V]
}

// NewTypedEventEmitter creates a new typed event emitter.
func NewTypedEventEmitter[K comparable, V any]() *TypedEventEmitter[K, V] {
	return &TypedEventEmitter[K, V]{
		listeners: make(map[K][]EventCallback[V]),
	}
}

// On registers an event listener and returns an unsubscribe function.
func (e *TypedEventEmitter[K, V]) On(event K, callback EventCallback[V]) func() {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.listeners[event] = append(e.listeners[event], callback)

	// Return unsubscribe function
	return func() {
		e.Off(event, callback)
	}
}

// Off removes an event listener.
func (e *TypedEventEmitter[K, V]) Off(event K, callback EventCallback[V]) {
	e.mu.Lock()
	defer e.mu.Unlock()

	callbacks:= e.listeners[event]
	for i, cb:= range callbacks {
		// Note: Function comparison in Go is limited
		// This is a simplified version
		if &cb == &callback {
			e.listeners[event] = append(callbacks[:i], callbacks[i+1:]...)
			break
		}
	}
}

// Emit triggers all listeners for an event.
func (e *TypedEventEmitter[K, V]) Emit(event K, data V) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	for _, callback:= range e.listeners[event] {
		callback(data)
	}
}

// Once registers a one-time listener.
func (e *TypedEventEmitter[K, V]) Once(event K, callback EventCallback[V]) func() {
	var unsubscribe func()
	wrapper:= func(data V) {
		unsubscribe()
		callback(data)
	}
	unsubscribe = e.On(event, wrapper)
	return unsubscribe
}

// Usage with types
type UserLoginData struct {
	UserID    string
	Timestamp string
}

type UserLogoutData struct {
	UserID string
}

func typedExample() {
	userEvents:= NewTypedEventEmitter[string, interface{}]()

	userEvents.On("login", func(data interface{}) {
		if loginData, ok:= data.(UserLoginData); ok {
			fmt.Printf("User %s logged in at %s\n", loginData.UserID, loginData.Timestamp)
		}
	})

	userEvents.Emit("login", UserLoginData{
		UserID:    "123",
		Timestamp: "2025-01-11T10:00:00Z",
	})
}
```

## Observable (RxJS-like)

```go
import "context"

// Subscriber handles observable values.
type Subscriber[T any] struct {
	Next     func(value T)
	Error    func(err error)
	Complete func()
}

// Unsubscribe is a function to cancel a subscription.
type Unsubscribe func()

// Observable represents a stream of values.
type Observable[T any] struct {
	producer func(subscriber *Subscriber[T]) Unsubscribe
}

// NewObservable creates a new observable.
func NewObservable[T any](producer func(*Subscriber[T]) Unsubscribe) *Observable[T] {
	return &Observable[T]{producer: producer}
}

// Subscribe subscribes to the observable.
func (o *Observable[T]) Subscribe(subscriber *Subscriber[T]) Unsubscribe {
	if cleanup:= o.producer(subscriber); cleanup != nil {
		return cleanup
	}
	return func() {}
}

// Map transforms values.
func (o *Observable[T]) Map[R any](fn func(T) R) *Observable[R] {
	return NewObservable(func(subscriber *Subscriber[R]) Unsubscribe {
		return o.Subscribe(&Subscriber[T]{
			Next: func(value T) {
				subscriber.Next(fn(value))
			},
			Error:    subscriber.Error,
			Complete: subscriber.Complete,
		})
	})
}

// Filter filters values.
func (o *Observable[T]) Filter(predicate func(T) bool) *Observable[T] {
	return NewObservable(func(subscriber *Subscriber[T]) Unsubscribe {
		return o.Subscribe(&Subscriber[T]{
			Next: func(value T) {
				if predicate(value) {
					subscriber.Next(value)
				}
			},
			Error:    subscriber.Error,
			Complete: subscriber.Complete,
		})
	})
}

// Debounce debounces values.
func (o *Observable[T]) Debounce(ctx context.Context, duration int64) *Observable[T] {
	return NewObservable(func(subscriber *Subscriber[T]) Unsubscribe {
		var timer *time.Timer

		unsubscribe:= o.Subscribe(&Subscriber[T]{
			Next: func(value T) {
				if timer != nil {
					timer.Stop()
				}
				timer = time.AfterFunc(time.Duration(duration)*time.Millisecond, func() {
					subscriber.Next(value)
				})
			},
			Error:    subscriber.Error,
			Complete: subscriber.Complete,
		})

		return func() {
			if timer != nil {
				timer.Stop()
			}
			unsubscribe()
		}
	})
}

// Interval creates an observable that emits values at intervals.
func Interval(ctx context.Context, ms int64) *Observable[int] {
	return NewObservable(func(subscriber *Subscriber[int]) Unsubscribe {
		count:= 0
		ticker:= time.NewTicker(time.Duration(ms) * time.Millisecond)

		go func() {
			for {
				select {
				case <-ticker.C:
					subscriber.Next(count)
					count++
				case <-ctx.Done():
					ticker.Stop()
					subscriber.Complete()
					return
				}
			}
		}()

		return func() {
			ticker.Stop()
		}
	})
}
```

## PubSub (decoupled)

```go
// PubSub provides publish-subscribe functionality.
type PubSub struct {
	mu       sync.RWMutex
	channels map[string][]func(interface{})
}

// NewPubSub creates a new pub/sub instance.
func NewPubSub() *PubSub {
	return &PubSub{
		channels: make(map[string][]func(interface{})),
	}
}

// Subscribe registers a callback for a channel.
func (p *PubSub) Subscribe(channel string, callback func(interface{})) func() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.channels[channel] = append(p.channels[channel], callback)

	return func() {
		p.Unsubscribe(channel, callback)
	}
}

// Unsubscribe removes a callback from a channel.
func (p *PubSub) Unsubscribe(channel string, callback func(interface{})) {
	p.mu.Lock()
	defer p.mu.Unlock()

	callbacks:= p.channels[channel]
	for i, cb:= range callbacks {
		if &cb == &callback {
			p.channels[channel] = append(callbacks[:i], callbacks[i+1:]...)
			break
		}
	}
}

// Publish sends data to all subscribers of a channel.
func (p *PubSub) Publish(channel string, data interface{}) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	for _, callback:= range p.channels[channel] {
		callback(data)
	}
}

// Clear removes all subscribers from a channel or all channels.
func (p *PubSub) Clear(channel string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if channel != "" {
		delete(p.channels, channel)
	} else {
		p.channels = make(map[string][]func(interface{}))
	}
}

// Usage - decoupled components
func pubsubExample() {
	ps:= NewPubSub()

	// Component A
	ps.Publish("user:updated", map[string]string{"id": "123", "name": "John"})

	// Component B (does not know A)
	ps.Subscribe("user:updated", func(data interface{}) {
		if user, ok:= data.(map[string]string); ok {
			fmt.Printf("User updated: %+v\n", user)
		}
	})
}
```

## Anti-patterns

```go
// BAD: Observer that modifies the subject
type BadObserver struct {
	stock *Stock
}

func (o *BadObserver) Update(price float64) {
	if price > 100 {
		o.stock.SetPrice(100) // Boucle infinie potentielle!
	}
}

// BAD: Memory leak - forgetting to unsubscribe
type LeakyComponent struct{}

func NewLeakyComponent(emitter *EventEmitter[string]) *LeakyComponent {
	comp:= &LeakyComponent{}
	emitter.Subscribe(comp) // Jamais unsubscribe = fuite memoire
	return comp
}

func (l *LeakyComponent) Update(data string) {}

// BAD: Notification order matters
type OrderDependentObserver struct{}

func (o *OrderDependentObserver) Update(data interface{}) {
	// Depends on another observer executed before
	// Order is not guaranteed!
}

// BAD: Blocking synchronous observer
type SlowObserver struct{}

func (o *SlowObserver) Update(data interface{}) {
	// Blocks all other observers
	time.Sleep(5 * time.Second) // 5 secondes...
}
```

## Unit Tests

```go
package main

import (
	"testing"
)

func TestEventEmitter(t *testing.T) {
	t.Run("should notify all subscribers", func(t *testing.T) {
		emitter:= NewEventEmitter[string]()
		called1:= false
		called2:= false

		observer1:= &testObserver{callback: func(s string) { called1 = true }}
		observer2:= &testObserver{callback: func(s string) { called2 = true }}

		emitter.Subscribe(observer1)
		emitter.Subscribe(observer2)
		emitter.Notify("hello")

		if !called1 || !called2 {
			t.Error("all observers should be notified")
		}
	})

	t.Run("should allow unsubscribe", func(t *testing.T) {
		emitter:= NewEventEmitter[string]()
		called:= false

		observer:= &testObserver{callback: func(s string) { called = true }}

		emitter.Subscribe(observer)
		emitter.Unsubscribe(observer)
		emitter.Notify("hello")

		if called {
			t.Error("unsubscribed observer should not be notified")
		}
	})
}

type testObserver struct {
	callback func(string)
}

func (t *testObserver) Update(data string) {
	t.callback(data)
}

func TestTypedEventEmitter(t *testing.T) {
	t.Run("should handle typed events", func(t *testing.T) {
		emitter:= NewTypedEventEmitter[string, string]()
		received:= ""

		emitter.On("message", func(data string) {
			received = data
		})

		emitter.Emit("message", "hello")

		if received != "hello" {
			t.Errorf("expected 'hello', got '%s'", received)
		}
	})

	t.Run("should return unsubscribe function", func(t *testing.T) {
		emitter:= NewTypedEventEmitter[string, string]()
		called:= false

		unsubscribe:= emitter.On("test", func(data string) {
			called = true
		})

		unsubscribe()
		emitter.Emit("test", "data")

		if called {
			t.Error("unsubscribed callback should not be called")
		}
	})

	t.Run("should support once", func(t *testing.T) {
		emitter:= NewTypedEventEmitter[string, string]()
		count:= 0

		emitter.Once("test", func(data string) {
			count++
		})

		emitter.Emit("test", "first")
		emitter.Emit("test", "second")

		if count != 1 {
			t.Errorf("expected 1 call, got %d", count)
		}
	})
}

func TestObservable(t *testing.T) {
	t.Run("should support map operator", func(t *testing.T) {
		results:= []int{}

		source:= NewObservable(func(subscriber *Subscriber[int]) Unsubscribe {
			subscriber.Next(1)
			subscriber.Next(2)
			subscriber.Next(3)
			return nil
		})

		source.Map(func(x int) int {
			return x * 2
		}).Subscribe(&Subscriber[int]{
			Next: func(value int) {
				results = append(results, value)
			},
		})

		expected:= []int{2, 4, 6}
		for i, v:= range results {
			if v != expected[i] {
				t.Errorf("expected %d, got %d at index %d", expected[i], v, i)
			}
		}
	})

	t.Run("should support filter operator", func(t *testing.T) {
		results:= []int{}

		source:= NewObservable(func(subscriber *Subscriber[int]) Unsubscribe {
			for i:= 1; i <= 5; i++ {
				subscriber.Next(i)
			}
			return nil
		})

		source.Filter(func(x int) bool {
			return x%2 == 0
		}).Subscribe(&Subscriber[int]{
			Next: func(value int) {
				results = append(results, value)
			},
		})

		expected:= []int{2, 4}
		for i, v:= range results {
			if v != expected[i] {
				t.Errorf("expected %d, got %d at index %d", expected[i], v, i)
			}
		}
	})
}
```

## When to Use

- Event systems
- Reactive UI (state -> view)
- Real-time notifications
- Decoupling between modules

## Related Patterns

- **Mediator**: Centralizes communication
- **Event Sourcing**: Stores events
- **CQRS**: Separates read/write with events

## Sources

- [Refactoring Guru - Observer](https://refactoring.guru/design-patterns/observer)
- [ReactiveX](https://reactivex.io/)
