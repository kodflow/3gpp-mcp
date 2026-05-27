# State Pattern

> Allow an object to change its behavior when its state changes.

## Intent

Allow an object to change its behavior when its internal state
changes. The object will appear to change its class.

## Structure

```go
package main

import (
	"fmt"
)

// OrderState defines the interface for order states.
type OrderState interface {
	Name() string
	Confirm(order *Order) error
	Ship(order *Order) error
	Deliver(order *Order) error
	Cancel(order *Order) error
}

// OrderItem represents an item in an order.
type OrderItem struct {
	Product string
	Qty     int
}

// Order is the context that changes state.
type Order struct {
	state OrderState
	ID    string
	Items []OrderItem
}

// NewOrder creates a new order in pending state.
func NewOrder(id string, items []OrderItem) *Order {
	return &Order{
		ID:    id,
		Items: items,
		state: &PendingState{},
	}
}

// SetState changes the order state.
func (o *Order) SetState(state OrderState) {
	fmt.Printf("Order %s: %s -> %s\n", o.ID, o.state.Name(), state.Name())
	o.state = state
}

// GetState returns the current state name.
func (o *Order) GetState() string {
	return o.state.Name()
}

// Confirm delegates to the current state.
func (o *Order) Confirm() error {
	return o.state.Confirm(o)
}

// Ship delegates to the current state.
func (o *Order) Ship() error {
	return o.state.Ship(o)
}

// Deliver delegates to the current state.
func (o *Order) Deliver() error {
	return o.state.Deliver(o)
}

// Cancel delegates to the current state.
func (o *Order) Cancel() error {
	return o.state.Cancel(o)
}

// PendingState represents the pending state.
type PendingState struct{}

func (s *PendingState) Name() string { return "Pending" }

func (s *PendingState) Confirm(order *Order) error {
	fmt.Println("Payment confirmed, preparing order...")
	order.SetState(&ConfirmedState{})
	return nil
}

func (s *PendingState) Ship(order *Order) error {
	return fmt.Errorf("cannot ship: order not confirmed yet")
}

func (s *PendingState) Deliver(order *Order) error {
	return fmt.Errorf("cannot deliver: order not shipped yet")
}

func (s *PendingState) Cancel(order *Order) error {
	fmt.Println("Order cancelled before confirmation")
	order.SetState(&CancelledState{})
	return nil
}

// ConfirmedState represents the confirmed state.
type ConfirmedState struct{}

func (s *ConfirmedState) Name() string { return "Confirmed" }

func (s *ConfirmedState) Confirm(order *Order) error {
	fmt.Println("Order already confirmed")
	return nil
}

func (s *ConfirmedState) Ship(order *Order) error {
	fmt.Println("Order shipped!")
	order.SetState(&ShippedState{})
	return nil
}

func (s *ConfirmedState) Deliver(order *Order) error {
	return fmt.Errorf("cannot deliver: order not shipped yet")
}

func (s *ConfirmedState) Cancel(order *Order) error {
	fmt.Println("Order cancelled, initiating refund...")
	order.SetState(&CancelledState{})
	return nil
}

// ShippedState represents the shipped state.
type ShippedState struct{}

func (s *ShippedState) Name() string { return "Shipped" }

func (s *ShippedState) Confirm(order *Order) error {
	fmt.Println("Order already confirmed and shipped")
	return nil
}

func (s *ShippedState) Ship(order *Order) error {
	fmt.Println("Order already shipped")
	return nil
}

func (s *ShippedState) Deliver(order *Order) error {
	fmt.Println("Order delivered!")
	order.SetState(&DeliveredState{})
	return nil
}

func (s *ShippedState) Cancel(order *Order) error {
	return fmt.Errorf("cannot cancel: order already shipped")
}

// DeliveredState represents the delivered state.
type DeliveredState struct{}

func (s *DeliveredState) Name() string { return "Delivered" }

func (s *DeliveredState) Confirm(order *Order) error {
	fmt.Println("Order already delivered")
	return nil
}

func (s *DeliveredState) Ship(order *Order) error {
	fmt.Println("Order already delivered")
	return nil
}

func (s *DeliveredState) Deliver(order *Order) error {
	fmt.Println("Order already delivered")
	return nil
}

func (s *DeliveredState) Cancel(order *Order) error {
	return fmt.Errorf("cannot cancel: order already delivered")
}

// CancelledState represents the cancelled state.
type CancelledState struct{}

func (s *CancelledState) Name() string { return "Cancelled" }

func (s *CancelledState) Confirm(order *Order) error {
	return fmt.Errorf("cannot confirm: order is cancelled")
}

func (s *CancelledState) Ship(order *Order) error {
	return fmt.Errorf("cannot ship: order is cancelled")
}

func (s *CancelledState) Deliver(order *Order) error {
	return fmt.Errorf("cannot deliver: order is cancelled")
}

func (s *CancelledState) Cancel(order *Order) error {
	fmt.Println("Order already cancelled")
	return nil
}
```

## Usage

```go
func main() {
	order:= NewOrder("ORD-001", []OrderItem{{Product: "Laptop", Qty: 1}})

	fmt.Println(order.GetState()) // Pending

	order.Confirm() // Payment confirmed, preparing order...
	fmt.Println(order.GetState()) // Confirmed

	order.Ship() // Order shipped!
	fmt.Println(order.GetState()) // Shipped

	if err:= order.Cancel(); err != nil {
		fmt.Println(err) // Error: Cannot cancel: order already shipped
	}

	order.Deliver() // Order delivered!
	fmt.Println(order.GetState()) // Delivered
}
```

## State Machine with Explicit Transitions

```go
// StateType represents possible states.
type StateType string

const (
	StateIdle    StateType = "idle"
	StateLoading StateType = "loading"
	StateSuccess StateType = "success"
	StateError   StateType = "error"
)

// EventType represents possible events.
type EventType string

const (
	EventFetch   EventType = "FETCH"
	EventSuccess EventType = "SUCCESS"
	EventError   EventType = "ERROR"
	EventRetry   EventType = "RETRY"
	EventReset   EventType = "RESET"
)

// StateConfig defines state configuration.
type StateConfig struct {
	On      map[EventType]StateType
	OnEnter func()
	OnExit  func()
}

// MachineConfig is the state machine configuration.
type MachineConfig map[StateType]*StateConfig

// StateMachine manages state transitions.
type StateMachine struct {
	state  StateType
	config MachineConfig
}

// NewStateMachine creates a new state machine.
func NewStateMachine(initialState StateType, config MachineConfig) *StateMachine {
	machine:= &StateMachine{
		state:  initialState,
		config: config,
	}
	if cfg:= config[initialState]; cfg != nil && cfg.OnEnter != nil {
		cfg.OnEnter()
	}
	return machine
}

// GetState returns the current state.
func (m *StateMachine) GetState() StateType {
	return m.state
}

// Send sends an event to the state machine.
func (m *StateMachine) Send(event EventType) {
	currentConfig:= m.config[m.state]
	if currentConfig == nil {
		fmt.Printf("No config for state %s\n", m.state)
		return
	}

	nextState, ok:= currentConfig.On[event]
	if !ok {
		fmt.Printf("No transition for %s from %s\n", event, m.state)
		return
	}

	// Execute exit action
	if currentConfig.OnExit != nil {
		currentConfig.OnExit()
	}

	// Transition
	fmt.Printf("%s --(%s)--> %s\n", m.state, event, nextState)
	m.state = nextState

	// Execute enter action
	if nextConfig:= m.config[nextState]; nextConfig != nil && nextConfig.OnEnter != nil {
		nextConfig.OnEnter()
	}
}

// Can checks if an event is valid in the current state.
func (m *StateMachine) Can(event EventType) bool {
	if cfg:= m.config[m.state]; cfg != nil {
		_, ok:= cfg.On[event]
		return ok
	}
	return false
}

// Declarative configuration
func stateMachineExample() {
	fetchMachine:= NewStateMachine(StateIdle, MachineConfig{
		StateIdle: {
			On:      map[EventType]StateType{EventFetch: StateLoading},
			OnEnter: func() { fmt.Println("Ready to fetch") },
		},
		StateLoading: {
			On:      map[EventType]StateType{EventSuccess: StateSuccess, EventError: StateError},
			OnEnter: func() { fmt.Println("Fetching data...") },
		},
		StateSuccess: {
			On:      map[EventType]StateType{EventReset: StateIdle},
			OnEnter: func() { fmt.Println("Data loaded!") },
		},
		StateError: {
			On:      map[EventType]StateType{EventRetry: StateLoading, EventReset: StateIdle},
			OnEnter: func() { fmt.Println("Fetch failed") },
		},
	})

	fetchMachine.Send(EventFetch)   // idle --(FETCH)--> loading
	fetchMachine.Send(EventSuccess) // loading --(SUCCESS)--> success
	fetchMachine.Send(EventReset)   // success --(RESET)--> idle
}
```

## State with History

```go
// StateWithHistory is a state that can be saved in history.
type StateWithHistory interface {
	Name() string
	Handle(context *DocumentContext) error
}

// DocumentContext manages state with history.
type DocumentContext struct {
	state   StateWithHistory
	history []StateWithHistory
}

// NewDocumentContext creates a new document context.
func NewDocumentContext(initialState StateWithHistory) *DocumentContext {
	return &DocumentContext{
		state: initialState,
	}
}

// SetState changes the state and optionally saves history.
func (d *DocumentContext) SetState(state StateWithHistory, saveHistory bool) {
	if saveHistory {
		d.history = append(d.history, d.state)
	}
	d.state = state
}

// GoBack reverts to the previous state.
func (d *DocumentContext) GoBack() error {
	if len(d.history) == 0 {
		return fmt.Errorf("no history available")
	}

	d.state = d.history[len(d.history)-1]
	d.history = d.history[:len(d.history)-1]
	return nil
}

// Process delegates to the current state.
func (d *DocumentContext) Process() error {
	return d.state.Handle(d)
}

// DraftState is an example state.
type DraftState struct{}

func (s *DraftState) Name() string { return "Draft" }

func (s *DraftState) Handle(context *DocumentContext) error {
	fmt.Println("Handling draft state")
	return nil
}
```

## State with Persistence

```go
// SerializableState represents a state that can be persisted.
type SerializableState struct {
	Name string
	Data map[string]interface{}
}

// DB is a mock database interface.
type DB interface {
	Save(key string, value interface{}) error
	Get(key string) (*SerializableState, error)
}

// PersistentStateMachine is a state machine that can be persisted.
type PersistentStateMachine struct {
	state     OrderState
	stateData map[string]interface{}
	db        DB
}

// NewPersistentStateMachine creates a new persistent state machine.
func NewPersistentStateMachine(serialized *SerializableState, db DB) *PersistentStateMachine {
	machine:= &PersistentStateMachine{
		stateData: make(map[string]interface{}),
		db:        db,
	}

	if serialized != nil {
		machine.state = machine.deserializeState(serialized.Name)
		machine.stateData = serialized.Data
	} else {
		machine.state = &PendingState{}
	}

	return machine
}

func (m *PersistentStateMachine) deserializeState(name string) OrderState {
	states:= map[string]OrderState{
		"Pending":   &PendingState{},
		"Confirmed": &ConfirmedState{},
		"Shipped":   &ShippedState{},
		"Delivered": &DeliveredState{},
		"Cancelled": &CancelledState{},
	}

	if state, ok:= states[name]; ok {
		return state
	}
	return &PendingState{}
}

// Serialize converts the state to a serializable format.
func (m *PersistentStateMachine) Serialize() *SerializableState {
	return &SerializableState{
		Name: m.state.Name(),
		Data: m.stateData,
	}
}

// Persist saves the state to the database.
func (m *PersistentStateMachine) Persist() error {
	return m.db.Save("state", m.Serialize())
}

// Load loads the state from the database.
func (m *PersistentStateMachine) Load() error {
	data, err:= m.db.Get("state")
	if err != nil {
		return err
	}
	m.state = m.deserializeState(data.Name)
	m.stateData = data.Data
	return nil
}
```

## Anti-patterns

```go
// BAD: Transition logic in the context
type BadContext struct {
	state string
}

func (c *BadContext) Process() {
	// Logic should be in the states
	if c.state == "pending" {
		// ...
		c.state = "processing"
	} else if c.state == "processing" {
		// ...
		c.state = "completed"
	}
}

// BAD: States that know too much context
type TightlyCoupledState struct{}

func (s *TightlyCoupledState) Handle(order *Order) error {
	// Direct access to internal properties
	// Encapsulation violation
	return nil
}

// BAD: State with internal state
type StatefulState struct {
	attempts int // Etat dans le state = problemes
}

func (s *StatefulState) Handle(order *Order) error {
	s.attempts++
	// The state is shared between all orders!
	return nil
}
```

## Unit Tests

```go
package main

import (
	"testing"
)

func TestOrderStateMachine(t *testing.T) {
	t.Run("PendingState should transition to Confirmed on confirm", func(t *testing.T) {
		order:= NewOrder("1", []OrderItem{})
		if order.GetState() != "Pending" {
			t.Errorf("expected Pending, got %s", order.GetState())
		}

		order.Confirm()

		if order.GetState() != "Confirmed" {
			t.Errorf("expected Confirmed, got %s", order.GetState())
		}
	})

	t.Run("PendingState should transition to Cancelled on cancel", func(t *testing.T) {
		order:= NewOrder("1", []OrderItem{})

		order.Cancel()

		if order.GetState() != "Cancelled" {
			t.Errorf("expected Cancelled, got %s", order.GetState())
		}
	})

	t.Run("PendingState should error on ship", func(t *testing.T) {
		order:= NewOrder("1", []OrderItem{})

		err:= order.Ship()

		if err == nil {
			t.Error("expected error when shipping pending order")
		}
	})

	t.Run("ShippedState should transition to Delivered on deliver", func(t *testing.T) {
		order:= NewOrder("1", []OrderItem{})
		order.Confirm()
		order.Ship()

		order.Deliver()

		if order.GetState() != "Delivered" {
			t.Errorf("expected Delivered, got %s", order.GetState())
		}
	})

	t.Run("ShippedState should error on cancel", func(t *testing.T) {
		order:= NewOrder("1", []OrderItem{})
		order.Confirm()
		order.Ship()

		err:= order.Cancel()

		if err == nil {
			t.Error("expected error when cancelling shipped order")
		}
	})

	t.Run("Full workflow should complete happy path", func(t *testing.T) {
		order:= NewOrder("1", []OrderItem{})

		order.Confirm()
		order.Ship()
		order.Deliver()

		if order.GetState() != "Delivered" {
			t.Errorf("expected Delivered, got %s", order.GetState())
		}
	})

	t.Run("Full workflow should handle cancellation path", func(t *testing.T) {
		order:= NewOrder("1", []OrderItem{})

		order.Confirm()
		order.Cancel()

		if order.GetState() != "Cancelled" {
			t.Errorf("expected Cancelled, got %s", order.GetState())
		}
	})
}

func TestStateMachine(t *testing.T) {
	t.Run("should transition on valid events", func(t *testing.T) {
		machine:= NewStateMachine(StateIdle, MachineConfig{
			StateIdle:    {On: map[EventType]StateType{EventFetch: StateLoading}},
			StateLoading: {On: map[EventType]StateType{EventSuccess: StateSuccess}},
			StateSuccess: {On: map[EventType]StateType{}},
		})

		machine.Send(EventFetch)
		if machine.GetState() != StateLoading {
			t.Errorf("expected loading, got %s", machine.GetState())
		}

		machine.Send(EventSuccess)
		if machine.GetState() != StateSuccess {
			t.Errorf("expected success, got %s", machine.GetState())
		}
	})

	t.Run("should ignore invalid transitions", func(t *testing.T) {
		machine:= NewStateMachine(StateIdle, MachineConfig{
			StateIdle:    {On: map[EventType]StateType{EventFetch: StateLoading}},
			StateLoading: {On: map[EventType]StateType{}},
		})

		machine.Send(EventSuccess) // Invalid from idle

		if machine.GetState() != StateIdle {
			t.Errorf("expected idle, got %s", machine.GetState())
		}
	})

	t.Run("should call onEnter/onExit hooks", func(t *testing.T) {
		enterCalled:= false
		exitCalled:= false

		machine:= NewStateMachine(StateIdle, MachineConfig{
			StateIdle: {
				On:     map[EventType]StateType{EventFetch: StateLoading},
				OnExit: func() { exitCalled = true },
			},
			StateLoading: {
				On:      map[EventType]StateType{},
				OnEnter: func() { enterCalled = true },
			},
		})

		machine.Send(EventFetch)

		if !exitCalled {
			t.Error("onExit should have been called")
		}
		if !enterCalled {
			t.Error("onEnter should have been called")
		}
	})
}
```

## When to Use

- Behavior depends on state
- Many states with complex transitions
- Conditional logic on state
- Workflow or business process

## Related Patterns

- **Strategy**: Changes algorithm (explicit) vs behavior (implicit)
- **Flyweight**: Share State instances
- **Singleton**: Stateless states can be singletons

## Sources

- [Refactoring Guru - State](https://refactoring.guru/design-patterns/state)
- [XState](https://xstate.js.org/)
