# Process Manager Pattern

Complex workflow orchestration and Saga pattern.

## Overview

```
+------------------+     +------------------+
|  Process Manager |<--->|  State Store     |
+--------+---------+     +------------------+
         |
    Orchestrates
         |
    +----+----+----+----+
    |    |    |    |    |
    v    v    v    v    v
  Step  Step Step Step Step
   1     2    3    4    5

  Events flow back to Process Manager
  which decides next step based on state
```

---

## Process Manager

> Coordinates the execution of a multi-step workflow.

### Workflow Schema

```
+--------+     +----------+     +---------+     +---------+
| Create |---->| Validate |---->| Reserve |---->| Payment |
| Order  |     | Order    |     |Inventory|     | Process |
+--------+     +----+-----+     +----+----+     +----+----+
                   |                 |               |
                   v                 v               v
              [Validated]      [Reserved]      [Paid/Failed]
                   |                 |               |
                   +--------+--------+-------+-------+
                            |                |
                            v                v
                       +--------+       +--------+
                       |  Ship  |       | Cancel |
                       +--------+       +--------+
```

### Implementation

```go
package processmanager

import (
	"context"
	"fmt"
	"time"
)

// ProcessState represents the current state of a process.
type ProcessState struct {
	ProcessID   string                 `json:"processId"`
	ProcessType string                 `json:"processType"`
	CurrentStep string                 `json:"currentStep"`
	Status      string                 `json:"status"` // running, completed, failed, compensating
	Data        map[string]interface{} `json:"data"`
	History     []StepExecution        `json:"history"`
	StartedAt   time.Time              `json:"startedAt"`
	UpdatedAt   time.Time              `json:"updatedAt"`
}

// StepExecution tracks individual step execution.
type StepExecution struct {
	Step        string                 `json:"step"`
	Status      string                 `json:"status"` // pending, completed, failed
	StartedAt   time.Time              `json:"startedAt"`
	CompletedAt *time.Time             `json:"completedAt,omitempty"`
	Result      map[string]interface{} `json:"result,omitempty"`
	Error       string                 `json:"error,omitempty"`
}

// ProcessEvent represents an event in the process.
type ProcessEvent struct {
	ProcessID string
	Success   bool
	Payload   map[string]interface{}
	Error     string
}

// ProcessStateStore manages process state persistence.
type ProcessStateStore interface {
	Save(ctx context.Context, state *ProcessState) error
	Load(ctx context.Context, processID string) (*ProcessState, error)
	FindByStatus(ctx context.Context, status string) ([]*ProcessState, error)
}

// Step represents a process step.
type Step interface {
	Execute(ctx context.Context, processID string, data map[string]interface{}) error
}

// ProcessManager orchestrates workflow execution.
type ProcessManager struct {
	stateStore    ProcessStateStore
	steps         map[string]Step
	compensations map[string]Step
	eventsCh      chan ProcessEvent
}

// NewProcessManager creates a new process manager.
func NewProcessManager(store ProcessStateStore, bufferSize int) *ProcessManager {
	return &ProcessManager{
		stateStore:    store,
		steps:         make(map[string]Step),
		compensations: make(map[string]Step),
		eventsCh:      make(chan ProcessEvent, bufferSize),
	}
}

// RegisterStep registers a process step.
func (pm *ProcessManager) RegisterStep(name string, step Step) {
	pm.steps[name] = step
}

// RegisterCompensation registers a compensation step.
func (pm *ProcessManager) RegisterCompensation(name string, step Step) {
	pm.compensations[name] = step
}

// Start initiates a new process.
func (pm *ProcessManager) Start(ctx context.Context, processID string, initialData map[string]interface{}) error {
	state:= &ProcessState{
		ProcessID:   processID,
		ProcessType: "ProcessManager",
		CurrentStep: "start",
		Status:      "running",
		Data:        initialData,
		History:     []StepExecution{},
		StartedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	if err:= pm.stateStore.Save(ctx, state); err != nil {
		return fmt.Errorf("saving initial state: %w", err)
	}

	return pm.executeNextStep(ctx, state)
}

// HandleEvent processes an event from a step.
func (pm *ProcessManager) HandleEvent(ctx context.Context, event ProcessEvent) error {
	state, err:= pm.stateStore.Load(ctx, event.ProcessID)
	if err != nil {
		return fmt.Errorf("loading process state: %w", err)
	}
	if state == nil {
		return fmt.Errorf("process not found: %s", event.ProcessID)
	}

	// Update history
	for i:= range state.History {
		if state.History[i].Step == state.CurrentStep && state.History[i].Status == "pending" {
			if event.Success {
				state.History[i].Status = "completed"
			} else {
				state.History[i].Status = "failed"
				state.History[i].Error = event.Error
			}
			now:= time.Now()
			state.History[i].CompletedAt = &now
			state.History[i].Result = event.Payload
			break
		}
	}

	// Determine next action
	if event.Success {
		for k, v:= range event.Payload {
			state.Data[k] = v
		}
		return pm.executeNextStep(ctx, state)
	}

	return pm.handleFailure(ctx, state, event)
}

// EventChannel returns the event channel.
func (pm *ProcessManager) EventChannel() chan<- ProcessEvent {
	return pm.eventsCh
}

// Run starts the process manager event loop.
func (pm *ProcessManager) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event:= <-pm.eventsCh:
			if err:= pm.HandleEvent(ctx, event); err != nil {
				// Log error but continue processing
				continue
			}
		}
	}
}

func (pm *ProcessManager) executeNextStep(ctx context.Context, state *ProcessState) error {
	nextStep:= pm.determineNextStep(state)

	if nextStep == "" {
		state.Status = "completed"
		state.UpdatedAt = time.Now()
		return pm.stateStore.Save(ctx, state)
	}

	state.CurrentStep = nextStep
	state.History = append(state.History, StepExecution{
		Step:      nextStep,
		Status:    "pending",
		StartedAt: time.Now(),
	})
	state.UpdatedAt = time.Now()

	if err:= pm.stateStore.Save(ctx, state); err != nil {
		return fmt.Errorf("saving state: %w", err)
	}

	step, exists:= pm.steps[nextStep]
	if !exists {
		return fmt.Errorf("step not found: %s", nextStep)
	}

	return step.Execute(ctx, state.ProcessID, state.Data)
}

func (pm *ProcessManager) determineNextStep(state *ProcessState) string {
	// Override in concrete implementation
	return ""
}

func (pm *ProcessManager) handleFailure(ctx context.Context, state *ProcessState, event ProcessEvent) error {
	// Start compensation
	state.Status = "compensating"
	if err:= pm.stateStore.Save(ctx, state); err != nil {
		return fmt.Errorf("saving compensating state: %w", err)
	}

	return pm.startCompensation(ctx, state)
}

func (pm *ProcessManager) startCompensation(ctx context.Context, state *ProcessState) error {
	completedSteps:= []string{}
	for _, h:= range state.History {
		if h.Status == "completed" {
			completedSteps = append(completedSteps, h.Step)
		}
	}

	// Compensate in reverse order
	for i:= len(completedSteps) - 1; i >= 0; i-- {
		stepName:= completedSteps[i]
		compensation, exists:= pm.compensations[stepName]
		if !exists {
			continue
		}

		if err:= compensation.Execute(ctx, state.ProcessID, state.Data); err != nil {
			// Log error but continue compensations
			continue
		}
	}

	state.Status = "failed"
	return pm.stateStore.Save(ctx, state)
}
```

---

## Saga Pattern

> Distributed transactions pattern with compensation.

### Schema

```
+--------+     +----------+     +---------+
| Step 1 |---->|  Step 2  |---->| Step 3  |---> SUCCESS
+---+----+     +----+-----+     +----+----+
    |               |                |
    |  COMPENSATE   |  COMPENSATE    | FAIL
    v               v                |
+--------+     +----------+          |
|Undo 1  |<----|  Undo 2  |<---------+
+--------+     +----------+
```

### Orchestrated Saga Implementation

```go
package saga

import (
	"context"
	"fmt"
)

// SagaStep represents a step in a saga.
type SagaStep struct {
	Name       string
	Execute    func(context.Context, map[string]interface{}) (map[string]interface{}, error)
	Compensate func(context.Context, map[string]interface{}) error
}

// SagaOrchestrator manages saga execution.
type SagaOrchestrator struct {
	steps      []SagaStep
	stateStore ProcessStateStore
}

// NewSagaOrchestrator creates a new saga orchestrator.
func NewSagaOrchestrator(steps []SagaStep, store ProcessStateStore) *SagaOrchestrator {
	return &SagaOrchestrator{
		steps:      steps,
		stateStore: store,
	}
}

// Execute runs the saga.
func (so *SagaOrchestrator) Execute(ctx context.Context, sagaID string, initialData map[string]interface{}) error {
	executedSteps:= []string{}
	currentData:= initialData

	for _, step:= range so.steps {
		if err:= so.saveState(ctx, sagaID, step.Name, "executing", currentData); err != nil {
			return fmt.Errorf("saving state: %w", err)
		}

		result, err:= step.Execute(ctx, currentData)
		if err != nil {
			if compErr:= so.compensate(ctx, sagaID, executedSteps, currentData); compErr != nil {
				return fmt.Errorf("compensation failed: %w", compErr)
			}
			return fmt.Errorf("step %s failed: %w", step.Name, err)
		}

		executedSteps = append(executedSteps, step.Name)
		for k, v:= range result {
			currentData[k] = v
		}

		if err:= so.saveState(ctx, sagaID, step.Name, "completed", currentData); err != nil {
			return fmt.Errorf("saving state: %w", err)
		}
	}

	return nil
}

func (so *SagaOrchestrator) compensate(ctx context.Context, sagaID string, executedSteps []string, data map[string]interface{}) error {
	// Compensate in reverse order
	for i:= len(executedSteps) - 1; i >= 0; i-- {
		stepName:= executedSteps[i]
		var step *SagaStep
		for j:= range so.steps {
			if so.steps[j].Name == stepName {
				step = &so.steps[j]
				break
			}
		}

		if step == nil {
			continue
		}

		if err:= so.saveState(ctx, sagaID, stepName, "compensating", data); err != nil {
			return fmt.Errorf("saving compensation state: %w", err)
		}

		if err:= step.Compensate(ctx, data); err != nil {
			_ = so.saveState(ctx, sagaID, stepName, "compensation_failed", data)
			continue // Log but continue compensations
		}

		if err:= so.saveState(ctx, sagaID, stepName, "compensated", data); err != nil {
			return fmt.Errorf("saving compensated state: %w", err)
		}
	}

	return nil
}

func (so *SagaOrchestrator) saveState(ctx context.Context, sagaID, step, status string, data map[string]interface{}) error {
	state:= &ProcessState{
		ProcessID:   sagaID,
		ProcessType: "Saga",
		CurrentStep: step,
		Status:      status,
		Data:        data,
		UpdatedAt:   time.Now(),
	}
	return so.stateStore.Save(ctx, state)
}

// OrderSaga example.
type OrderData struct {
	OrderID        string
	Items          []string
	CustomerID     string
	Total          float64
	ReservationID  string
	PaymentID      string
	ShipmentID     string
}

// BuildOrderSaga creates an order processing saga.
func BuildOrderSaga(
	inventoryService InventoryService,
	paymentService PaymentService,
	shippingService ShippingService,
) []SagaStep {
	return []SagaStep{
		{
			Name: "reserve_inventory",
			Execute: func(ctx context.Context, data map[string]interface{}) (map[string]interface{}, error) {
				orderData:= data["order"].(OrderData)
				result, err:= inventoryService.Reserve(ctx, orderData.Items)
				if err != nil {
					return nil, err
				}
				return map[string]interface{}{
					"reservationId": result.ID,
				}, nil
			},
			Compensate: func(ctx context.Context, data map[string]interface{}) error {
				reservationID:= data["reservationId"].(string)
				return inventoryService.Release(ctx, reservationID)
			},
		},
		{
			Name: "process_payment",
			Execute: func(ctx context.Context, data map[string]interface{}) (map[string]interface{}, error) {
				orderData:= data["order"].(OrderData)
				result, err:= paymentService.Charge(ctx, orderData.CustomerID, orderData.Total)
				if err != nil {
					return nil, err
				}
				return map[string]interface{}{
					"paymentId": result.ID,
				}, nil
			},
			Compensate: func(ctx context.Context, data map[string]interface{}) error {
				paymentID:= data["paymentId"].(string)
				return paymentService.Refund(ctx, paymentID)
			},
		},
		{
			Name: "create_shipment",
			Execute: func(ctx context.Context, data map[string]interface{}) (map[string]interface{}, error) {
				orderData:= data["order"].(OrderData)
				result, err:= shippingService.CreateShipment(ctx, orderData)
				if err != nil {
					return nil, err
				}
				return map[string]interface{}{
					"shipmentId": result.ID,
				}, nil
			},
			Compensate: func(ctx context.Context, data map[string]interface{}) error {
				shipmentID:= data["shipmentId"].(string)
				return shippingService.CancelShipment(ctx, shipmentID)
			},
		},
	}
}

// Service interfaces.
type InventoryService interface {
	Reserve(ctx context.Context, items []string) (*ReservationResult, error)
	Release(ctx context.Context, reservationID string) error
}

type PaymentService interface {
	Charge(ctx context.Context, customerID string, amount float64) (*PaymentResult, error)
	Refund(ctx context.Context, paymentID string) error
}

type ShippingService interface {
	CreateShipment(ctx context.Context, order OrderData) (*ShipmentResult, error)
	CancelShipment(ctx context.Context, shipmentID string) error
}

type ReservationResult struct {
	ID string
}

type PaymentResult struct {
	ID string
}

type ShipmentResult struct {
	ID string
}
```

---

## Choreographed Saga

```go
package choreography

import (
	"context"
)

// Event represents a domain event.
type Event struct {
	Type    string
	Payload map[string]interface{}
}

// EventBus publishes and subscribes to events.
type EventBus interface {
	Publish(ctx context.Context, topic string, event Event) error
	Subscribe(ctx context.Context, topic string) (<-chan Event, error)
}

// OrderService handles order events.
type OrderService struct {
	eventBus EventBus
	orderCh  <-chan Event
}

// NewOrderService creates a new order service.
func NewOrderService(bus EventBus) *OrderService {
	return &OrderService{
		eventBus: bus,
	}
}

// Start starts the order service event loop.
func (os *OrderService) Start(ctx context.Context) error {
	orderCh, err:= os.eventBus.Subscribe(ctx, "orders")
	if err != nil {
		return err
	}
	os.orderCh = orderCh

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event:= <-os.orderCh:
			if err:= os.handleEvent(ctx, event); err != nil {
				// Log error but continue
				continue
			}
		}
	}
}

func (os *OrderService) handleEvent(ctx context.Context, event Event) error {
	switch event.Type {
	case "OrderCreated":
		return os.handleOrderCreated(ctx, event)
	case "InventoryReserved":
		return os.handleInventoryReserved(ctx, event)
	case "PaymentFailed":
		return os.handlePaymentFailed(ctx, event)
	}
	return nil
}

func (os *OrderService) handleOrderCreated(ctx context.Context, event Event) error {
	return os.eventBus.Publish(ctx, "inventory", Event{
		Type: "ReserveInventory",
		Payload: map[string]interface{}{
			"orderId": event.Payload["orderId"],
			"items":   event.Payload["items"],
		},
	})
}

func (os *OrderService) handleInventoryReserved(ctx context.Context, event Event) error {
	return os.eventBus.Publish(ctx, "payment", Event{
		Type: "ProcessPayment",
		Payload: map[string]interface{}{
			"orderId": event.Payload["orderId"],
			"amount":  event.Payload["totalAmount"],
		},
	})
}

func (os *OrderService) handlePaymentFailed(ctx context.Context, event Event) error {
	// Trigger compensation
	if err:= os.eventBus.Publish(ctx, "inventory", Event{
		Type: "ReleaseInventory",
		Payload: map[string]interface{}{
			"orderId":       event.Payload["orderId"],
			"reservationId": event.Payload["reservationId"],
		},
	}); err != nil {
		return err
	}

	// Update order status
	return os.updateOrderStatus(ctx, event.Payload["orderId"].(string), "failed")
}

func (os *OrderService) updateOrderStatus(ctx context.Context, orderID, status string) error {
	// Implementation specific
	return nil
}
```

---

## Persistent State Management

```go
package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// PostgresProcessStateStore implements ProcessStateStore with PostgreSQL.
type PostgresProcessStateStore struct {
	db *sql.DB
}

// NewPostgresProcessStateStore creates a new PostgreSQL state store.
func NewPostgresProcessStateStore(db *sql.DB) *PostgresProcessStateStore {
	return &PostgresProcessStateStore{
		db: db,
	}
}

// Save saves process state.
func (ps *PostgresProcessStateStore) Save(ctx context.Context, state *ProcessState) error {
	dataJSON, err:= json.Marshal(state.Data)
	if err != nil {
		return fmt.Errorf("marshaling data: %w", err)
	}

	historyJSON, err:= json.Marshal(state.History)
	if err != nil {
		return fmt.Errorf("marshaling history: %w", err)
	}

	query:= `
		INSERT INTO process_states (
			process_id, process_type, current_step, status, 
			data, history, started_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (process_id) DO UPDATE SET
			current_step = $3,
			status = $4,
			data = $5,
			history = $6,
			updated_at = $8
	`

	_, err = ps.db.ExecContext(
		ctx,
		query,
		state.ProcessID,
		state.ProcessType,
		state.CurrentStep,
		state.Status,
		dataJSON,
		historyJSON,
		state.StartedAt,
		time.Now(),
	)

	if err != nil {
		return fmt.Errorf("executing query: %w", err)
	}

	return nil
}

// Load loads process state.
func (ps *PostgresProcessStateStore) Load(ctx context.Context, processID string) (*ProcessState, error) {
	query:= `
		SELECT process_id, process_type, current_step, status,
		       data, history, started_at, updated_at
		FROM process_states
		WHERE process_id = $1
	`

	var state ProcessState
	var dataJSON, historyJSON []byte

	err:= ps.db.QueryRowContext(ctx, query, processID).Scan(
		&state.ProcessID,
		&state.ProcessType,
		&state.CurrentStep,
		&state.Status,
		&dataJSON,
		&historyJSON,
		&state.StartedAt,
		&state.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying process state: %w", err)
	}

	if err:= json.Unmarshal(dataJSON, &state.Data); err != nil {
		return nil, fmt.Errorf("unmarshaling data: %w", err)
	}

	if err:= json.Unmarshal(historyJSON, &state.History); err != nil {
		return nil, fmt.Errorf("unmarshaling history: %w", err)
	}

	return &state, nil
}

// FindByStatus finds processes by status.
func (ps *PostgresProcessStateStore) FindByStatus(ctx context.Context, status string) ([]*ProcessState, error) {
	query:= `
		SELECT process_id, process_type, current_step, status,
		       data, history, started_at, updated_at
		FROM process_states
		WHERE status = $1
	`

	rows, err:= ps.db.QueryContext(ctx, query, status)
	if err != nil {
		return nil, fmt.Errorf("querying process states: %w", err)
	}
	defer rows.Close()

	states:= []*ProcessState{}
	for rows.Next() {
		var state ProcessState
		var dataJSON, historyJSON []byte

		if err:= rows.Scan(
			&state.ProcessID,
			&state.ProcessType,
			&state.CurrentStep,
			&state.Status,
			&dataJSON,
			&historyJSON,
			&state.StartedAt,
			&state.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}

		if err:= json.Unmarshal(dataJSON, &state.Data); err != nil {
			return nil, fmt.Errorf("unmarshaling data: %w", err)
		}

		if err:= json.Unmarshal(historyJSON, &state.History); err != nil {
			return nil, fmt.Errorf("unmarshaling history: %w", err)
		}

		states = append(states, &state)
	}

	return states, nil
}

// RecoverStuckProcesses recovers stuck processes.
func (ps *PostgresProcessStateStore) RecoverStuckProcesses(ctx context.Context, timeout time.Duration) ([]*ProcessState, error) {
	query:= `
		SELECT process_id, process_type, current_step, status,
		       data, history, started_at, updated_at
		FROM process_states
		WHERE status = 'running'
		  AND updated_at < $1
	`

	cutoff:= time.Now().Add(-timeout)
	rows, err:= ps.db.QueryContext(ctx, query, cutoff)
	if err != nil {
		return nil, fmt.Errorf("querying stuck processes: %w", err)
	}
	defer rows.Close()

	states:= []*ProcessState{}
	for rows.Next() {
		var state ProcessState
		var dataJSON, historyJSON []byte

		if err:= rows.Scan(
			&state.ProcessID,
			&state.ProcessType,
			&state.CurrentStep,
			&state.Status,
			&dataJSON,
			&historyJSON,
			&state.StartedAt,
			&state.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}

		if err:= json.Unmarshal(dataJSON, &state.Data); err != nil {
			return nil, fmt.Errorf("unmarshaling data: %w", err)
		}

		if err:= json.Unmarshal(historyJSON, &state.History); err != nil {
			return nil, fmt.Errorf("unmarshaling history: %w", err)
		}

		states = append(states, &state)
	}

	return states, nil
}
```

---

## When to Use

- Multi-step workflow orchestration with state management
- Distributed transactions requiring compensations (Saga pattern)
- Complex business processes with conditional branching
- Distributed service coordination with progress tracking
- Long-running process management with failure recovery

## Related Patterns

- [Transactional Outbox](./transactional-outbox.md) - Message reliability
- [Idempotent Receiver](./idempotent-receiver.md) - Avoid duplications
- [Dead Letter Channel](./dead-letter.md) - Process failure handling
- [Message Router](./message-router.md) - Workflow step routing

## Complementary Patterns

- **Routing Slip** - Dynamic workflow
- **Dead Letter Channel** - Process failures
- **Idempotent Receiver** - Avoid duplications
- **Transactional Outbox** - Message reliability
