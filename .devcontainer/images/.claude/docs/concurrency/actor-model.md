# Actor Model

Concurrency pattern based on isolated entities communicating via messages.

---

## What is the Actor Model?

> Each actor is an independent unit with its own private state, processing messages sequentially.

```
+--------------------------------------------------------------+
|                       Actor Model                             |
|                                                               |
|  +------------------+         +------------------+            |
|  |    Actor A       |         |    Actor B       |            |
|  |------------------|         |------------------|            |
|  | State (private)  |         | State (private)  |            |
|  | +-------------+  |         | +-------------+  |            |
|  | | count: 42   |  |         | | items: []   |  |            |
|  | +-------------+  |         | +-------------+  |            |
|  |                  |         |                  |            |
|  | Mailbox:         |         | Mailbox:         |            |
|  | [msg1][msg2][..] |         | [msgX][msgY]     |            |
|  +--------+---------+         +---------+--------+            |
|           |                             |                     |
|           |        send(message)        |                     |
|           +---------------------------->|                     |
|           |                             |                     |
|           |<----------------------------+                     |
|                    reply(result)                              |
|                                                               |
|  Guarantees:                                                  |
|  - No shared state                                            |
|  - Messages processed one at a time                           |
|  - Asynchronous communication                                 |
+--------------------------------------------------------------+
```

**Why:**

- Eliminates race conditions (no shared state)
- Natural scalability (distributed actors)
- Fault tolerance (supervision)

---

## Go Implementation

### Basic Actor with channels

```go
package actor

import (
	"context"
	"fmt"
	"sync"
)

// Message represents an actor message.
type Message[T any] struct {
	Type    string
	Payload T
	Reply   chan<- interface{}
}

// Actor processes messages sequentially.
type Actor[S any, M any] struct {
	state   S
	mailbox chan Message[M]
	handler func(*S, Message[M]) error
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// NewActor creates a new actor.
func NewActor[S any, M any](
	initialState S,
	bufferSize int,
	handler func(*S, Message[M]) error,
) *Actor[S, M] {
	ctx, cancel := context.WithCancel(context.Background())

	a := &Actor[S, M]{
		state:   initialState,
		mailbox: make(chan Message[M], bufferSize),
		handler: handler,
		ctx:     ctx,
		cancel:  cancel,
	}

	a.wg.Add(1)
	go a.run()

	return a
}

// run processes messages from the mailbox.
func (a *Actor[S, M]) run() {
	defer a.wg.Done()

	for {
		select {
		case <-a.ctx.Done():
			return
		case msg, ok := <-a.mailbox:
			if !ok {
				return
			}

			if err := a.handler(&a.state, msg); err != nil {
				// Log error or send to supervisor
				fmt.Printf("Actor error: %v\n", err)
			}
		}
	}
}

// Send sends a message asynchronously.
func (a *Actor[S, M]) Send(ctx context.Context, msg Message[M]) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-a.ctx.Done():
		return fmt.Errorf("actor is stopped")
	case a.mailbox <- msg:
		return nil
	}
}

// Ask sends a message and waits for reply.
func (a *Actor[S, M]) Ask(ctx context.Context, msgType string, payload M) (interface{}, error) {
	reply := make(chan interface{}, 1)

	msg := Message[M]{
		Type:    msgType,
		Payload: payload,
		Reply:   reply,
	}

	if err := a.Send(ctx, msg); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-reply:
		return result, nil
	}
}

// Stop gracefully stops the actor.
func (a *Actor[S, M]) Stop() {
	a.cancel()
	close(a.mailbox)
	a.wg.Wait()
}
```

---

### Example: Counter Actor

```go
package main

import (
	"context"
	"fmt"
)

type CounterState struct {
	count int
}

type CounterMsg struct {
	amount int
}

func main() {
	counter := NewActor(
		CounterState{count: 0},
		10,
		func(state *CounterState, msg Message[CounterMsg]) error {
			switch msg.Type {
			case "increment":
				state.count += msg.Payload.amount

			case "decrement":
				state.count -= msg.Payload.amount

			case "get":
				if msg.Reply != nil {
					msg.Reply <- state.count
				}

			case "reset":
				state.count = 0
			}

			return nil
		},
	)
	defer counter.Stop()

	ctx := context.Background()

	// Send messages
	counter.Send(ctx, Message[CounterMsg]{
		Type:    "increment",
		Payload: CounterMsg{amount: 5},
	})

	counter.Send(ctx, Message[CounterMsg]{
		Type:    "increment",
		Payload: CounterMsg{amount: 3},
	})

	// Ask for value
	value, err := counter.Ask(ctx, "get", CounterMsg{})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("Count: %v\n", value)
}
```

---

## Actor System

```go
package actor

import (
	"context"
	"fmt"
	"sync"
)

// ActorRef is a reference to an actor.
type ActorRef interface {
	Send(context.Context, interface{}) error
	Ask(context.Context, interface{}) (interface{}, error)
	Stop()
}

// System manages actors.
type System struct {
	actors map[string]ActorRef
	mu     sync.RWMutex
}

// NewSystem creates an actor system.
func NewSystem() *System {
	return &System{
		actors: make(map[string]ActorRef),
	}
}

// Register registers an actor with a name.
func (s *System) Register(name string, actor ActorRef) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.actors[name] = actor
}

// Lookup finds an actor by name.
func (s *System) Lookup(name string) (ActorRef, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	actor, ok := s.actors[name]
	return actor, ok
}

// Send sends a message to a named actor.
func (s *System) Send(ctx context.Context, actorName string, msg interface{}) error {
	actor, ok := s.Lookup(actorName)
	if !ok {
		return fmt.Errorf("actor not found: %s", actorName)
	}

	return actor.Send(ctx, msg)
}

// Ask sends a request to a named actor.
func (s *System) Ask(ctx context.Context, actorName string, msg interface{}) (interface{}, error) {
	actor, ok := s.Lookup(actorName)
	if !ok {
		return nil, fmt.Errorf("actor not found: %s", actorName)
	}

	return actor.Ask(ctx, msg)
}

// Shutdown stops all actors.
func (s *System) Shutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, actor := range s.actors {
		actor.Stop()
	}
}
```

---

## Typed Actor (better type-safety)

```go
package actor

import (
	"context"
	"sync"
)

// TypedActor is a type-safe actor.
type TypedActor[S any, M any] struct {
	state    S
	mailbox  chan M
	handlers map[string]func(*S, M) (interface{}, error)
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

// NewTypedActor creates a typed actor.
func NewTypedActor[S any, M any](initialState S, bufferSize int) *TypedActor[S, M] {
	ctx, cancel := context.WithCancel(context.Background())

	ta := &TypedActor[S, M]{
		state:    initialState,
		mailbox:  make(chan M, bufferSize),
		handlers: make(map[string]func(*S, M) (interface{}, error)),
		ctx:      ctx,
		cancel:   cancel,
	}

	ta.wg.Add(1)
	go ta.run()

	return ta
}

// Handle registers a message handler.
func (ta *TypedActor[S, M]) Handle(msgType string, handler func(*S, M) (interface{}, error)) {
	ta.handlers[msgType] = handler
}

// run processes messages.
func (ta *TypedActor[S, M]) run() {
	defer ta.wg.Done()

	for {
		select {
		case <-ta.ctx.Done():
			return
		case msg, ok := <-ta.mailbox:
			if !ok {
				return
			}

			// Process message with registered handler
			// Implementation depends on how you identify message types
		}
	}
}

// Send sends a message.
func (ta *TypedActor[S, M]) Send(ctx context.Context, msg M) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-ta.ctx.Done():
		return fmt.Errorf("actor stopped")
	case ta.mailbox <- msg:
		return nil
	}
}

// Stop stops the actor.
func (ta *TypedActor[S, M]) Stop() {
	ta.cancel()
	close(ta.mailbox)
	ta.wg.Wait()
}
```

---

## Supervision (Fault Tolerance)

```go
package actor

import (
	"context"
	"fmt"
)

// SupervisionStrategy defines how to handle failures.
type SupervisionStrategy int

const (
	Restart SupervisionStrategy = iota
	Stop
	Escalate
)

// Supervisor manages child actors.
type Supervisor struct {
	*Actor[SupervisorState, SupervisorMsg]
	strategy SupervisionStrategy
}

type SupervisorState struct {
	children map[string]ActorRef
}

type SupervisorMsg struct {
	action   string
	name     string
	actor    ActorRef
	error    error
	response chan<- error
}

// NewSupervisor creates a supervisor actor.
func NewSupervisor(strategy SupervisionStrategy) *Supervisor {
	s := &Supervisor{
		strategy: strategy,
	}

	s.Actor = NewActor(
		SupervisorState{
			children: make(map[string]ActorRef),
		},
		10,
		s.handleMessage,
	)

	return s
}

// handleMessage processes supervisor messages.
func (s *Supervisor) handleMessage(state *SupervisorState, msg Message[SupervisorMsg]) error {
	switch msg.Payload.action {
	case "spawn":
		state.children[msg.Payload.name] = msg.Payload.actor
		if msg.Payload.response != nil {
			msg.Payload.response <- nil
		}

	case "failure":
		return s.handleFailure(state, msg.Payload.name, msg.Payload.error)

	case "stop":
		if child, ok := state.children[msg.Payload.name]; ok {
			child.Stop()
			delete(state.children, msg.Payload.name)
		}
	}

	return nil
}

// handleFailure applies supervision strategy.
func (s *Supervisor) handleFailure(state *SupervisorState, childName string, err error) error {
	fmt.Printf("Child %s failed: %v\n", childName, err)

	switch s.strategy {
	case Restart:
		// Restart logic here
		return nil

	case Stop:
		if child, ok := state.children[childName]; ok {
			child.Stop()
			delete(state.children, childName)
		}
		return nil

	case Escalate:
		return fmt.Errorf("escalating failure from %s: %w", childName, err)

	default:
		return nil
	}
}

// Spawn creates a child actor.
func (s *Supervisor) Spawn(ctx context.Context, name string, actor ActorRef) error {
	response := make(chan error, 1)

	msg := Message[SupervisorMsg]{
		Type: "spawn",
		Payload: SupervisorMsg{
			action:   "spawn",
			name:     name,
			actor:    actor,
			response: response,
		},
	}

	if err := s.Send(ctx, msg); err != nil {
		return err
	}

	return <-response
}
```

---

## Complexity and Trade-offs

| Aspect | Value |
|--------|-------|
| Message send | O(1) |
| Processing | Sequential per actor |
| Memory | O(actors * mailbox_size) |

### Advantages

- No locks / race conditions
- Error isolation
- Horizontal scalability
- Simple mental model
- Native Go channels

### Disadvantages

- Message overhead vs direct calls
- More complex debugging
- Latency (asynchronous)
- Mailbox can overflow

---

## When to Use

| Situation | Recommended |
|-----------|-------------|
| Concurrent shared state | Yes |
| Distributed systems | Yes |
| High resilience required | Yes |
| Minimal latency critical | No |
| Simple logic without concurrency | No |

---

## Related Patterns

| Pattern | Relation |
|---------|----------|
| **Message Queue** | Messaging infrastructure |
| **Event Sourcing** | Actors can log messages |
| **CQRS** | Actors for read/write |
| **CSP** | Go channels = CSP |

---

## Sources

- [Go Concurrency Patterns](https://go.dev/blog/pipelines)
- [Akka Documentation](https://akka.io/docs/)
- [Actor Model - Wikipedia](https://en.wikipedia.org/wiki/Actor_model)
- [Erlang/OTP](https://www.erlang.org/)
