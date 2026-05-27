# Domain Event Pattern

> Captures something significant that happened in the domain - an immutable record of a past occurrence that domain experts care about.

## Definition

A **Domain Event** captures something significant that happened in the domain. It represents a fact - an immutable record of a past occurrence that domain experts care about.

```
Domain Event = Past Tense + Immutable + Business Significance + Timestamp
```

**Key characteristics:**

- **Past tense naming**: `OrderConfirmed`, not `ConfirmOrder`
- **Immutable**: Once created, never modified
- **Contains all context**: Self-sufficient information
- **Business-relevant**: Named in ubiquitous language
- **Decoupling mechanism**: Enables loose coupling between aggregates

## Go Implementation

```go
package domain

import (
	"time"

	"github.com/google/uuid"
)

// DomainEvent is the base interface for all domain events.
type DomainEvent interface {
	EventID() string
	OccurredAt() time.Time
	EventType() string
}

// BaseDomainEvent provides common event fields.
type BaseDomainEvent struct {
	eventID    string
	occurredAt time.Time
}

// NewBaseDomainEvent creates a new base event.
func NewBaseDomainEvent() BaseDomainEvent {
	return BaseDomainEvent{
		eventID:    uuid.New().String(),
		occurredAt: time.Now(),
	}
}

// EventID returns the unique event identifier.
func (e BaseDomainEvent) EventID() string {
	return e.eventID
}

// OccurredAt returns when the event occurred.
func (e BaseDomainEvent) OccurredAt() time.Time {
	return e.occurredAt
}

// OrderCreatedEvent represents an order creation.
type OrderCreatedEvent struct {
	BaseDomainEvent
	OrderID     OrderID
	CustomerID  CustomerID
	Items       []OrderItemSnapshot
	TotalAmount Money
}

// NewOrderCreatedEvent creates a new order created event.
func NewOrderCreatedEvent(
	orderID OrderID,
	customerID CustomerID,
	items []OrderItemSnapshot,
	totalAmount Money,
) *OrderCreatedEvent {
	return &OrderCreatedEvent{
		BaseDomainEvent: NewBaseDomainEvent(),
		OrderID:         orderID,
		CustomerID:      customerID,
		Items:           items,
		TotalAmount:     totalAmount,
	}
}

// EventType returns the event type.
func (e *OrderCreatedEvent) EventType() string {
	return "OrderCreated"
}

// OrderConfirmedEvent represents order confirmation.
type OrderConfirmedEvent struct {
	BaseDomainEvent
	OrderID              OrderID
	ConfirmedAt          time.Time
	ExpectedDeliveryDate time.Time
}

// NewOrderConfirmedEvent creates a new order confirmed event.
func NewOrderConfirmedEvent(
	orderID OrderID,
	confirmedAt, expectedDeliveryDate time.Time,
) *OrderConfirmedEvent {
	return &OrderConfirmedEvent{
		BaseDomainEvent:      NewBaseDomainEvent(),
		OrderID:              orderID,
		ConfirmedAt:          confirmedAt,
		ExpectedDeliveryDate: expectedDeliveryDate,
	}
}

// EventType returns the event type.
func (e *OrderConfirmedEvent) EventType() string {
	return "OrderConfirmed"
}

// OrderShippedEvent represents order shipment.
type OrderShippedEvent struct {
	BaseDomainEvent
	OrderID        OrderID
	TrackingNumber string
	Carrier        string
	ShippedAt      time.Time
}

// NewOrderShippedEvent creates a new order shipped event.
func NewOrderShippedEvent(
	orderID OrderID,
	trackingNumber, carrier string,
	shippedAt time.Time,
) *OrderShippedEvent {
	return &OrderShippedEvent{
		BaseDomainEvent: NewBaseDomainEvent(),
		OrderID:         orderID,
		TrackingNumber:  trackingNumber,
		Carrier:         carrier,
		ShippedAt:       shippedAt,
	}
}

// EventType returns the event type.
func (e *OrderShippedEvent) EventType() string {
	return "OrderShipped"
}

// PaymentReceivedEvent represents payment receipt.
type PaymentReceivedEvent struct {
	BaseDomainEvent
	OrderID       OrderID
	PaymentID     PaymentID
	Amount        Money
	PaymentMethod PaymentMethod
}

// NewPaymentReceivedEvent creates a new payment received event.
func NewPaymentReceivedEvent(
	orderID OrderID,
	paymentID PaymentID,
	amount Money,
	paymentMethod PaymentMethod,
) *PaymentReceivedEvent {
	return &PaymentReceivedEvent{
		BaseDomainEvent: NewBaseDomainEvent(),
		OrderID:         orderID,
		PaymentID:       paymentID,
		Amount:          amount,
		PaymentMethod:   paymentMethod,
	}
}

// EventType returns the event type.
func (e *PaymentReceivedEvent) EventType() string {
	return "PaymentReceived"
}

// Aggregate raising events
type Order struct {
	AggregateRoot[OrderID]
	domainEvents []DomainEvent
	// ... other fields
}

// Create creates a new order and raises a creation event.
func CreateOrder(
	customerID CustomerID,
	items []OrderItem,
	shippingAddress Address,
) (*Order, error) {
	order := &Order{
		AggregateRoot: NewAggregateRoot(NewOrderID()),
		// ... initialize fields
	}

	// Raise creation event
	itemSnapshots := make([]OrderItemSnapshot, len(items))
	for i, item := range items {
		itemSnapshots[i] = item.ToSnapshot()
	}

	order.addDomainEvent(NewOrderCreatedEvent(
		order.ID(),
		customerID,
		itemSnapshots,
		order.totalAmount(),
	))

	return order, nil
}

// Confirm confirms the order and raises an event.
func (o *Order) Confirm() error {
	if o.status != OrderStatusPending {
		return errors.New("order cannot be confirmed")
	}

	o.status = OrderStatusConfirmed
	o.confirmedAt = time.Now()

	// Raise confirmation event
	o.addDomainEvent(NewOrderConfirmedEvent(
		o.ID(),
		o.confirmedAt,
		o.calculateExpectedDelivery(),
	))

	return nil
}

func (o *Order) addDomainEvent(event DomainEvent) {
	o.domainEvents = append(o.domainEvents, event)
}

// PullDomainEvents retrieves and clears domain events.
func (o *Order) PullDomainEvents() []DomainEvent {
	events := make([]DomainEvent, len(o.domainEvents))
	copy(events, o.domainEvents)
	o.domainEvents = nil
	return events
}
```

## Event Handlers

```go
// DomainEventHandler handles a specific event type.
type DomainEventHandler[T DomainEvent] interface {
	Handle(ctx context.Context, event T) error
}

// OrderConfirmedHandler handles order confirmation events.
type OrderConfirmedHandler struct {
	inventoryService    InventoryService
	notificationService NotificationService
}

// NewOrderConfirmedHandler creates a new handler.
func NewOrderConfirmedHandler(
	inventoryService InventoryService,
	notificationService NotificationService,
) *OrderConfirmedHandler {
	return &OrderConfirmedHandler{
		inventoryService:    inventoryService,
		notificationService: notificationService,
	}
}

// Handle processes the order confirmed event.
func (h *OrderConfirmedHandler) Handle(
	ctx context.Context,
	event *OrderConfirmedEvent,
) error {
	// Reserve inventory
	if err := h.inventoryService.ReserveForOrder(ctx, event.OrderID); err != nil {
		return fmt.Errorf("reserving inventory: %w", err)
	}

	// Send confirmation email
	if err := h.notificationService.SendOrderConfirmation(ctx, event.OrderID); err != nil {
		return fmt.Errorf("sending confirmation: %w", err)
	}

	return nil
}

// PaymentReceivedHandler handles payment events.
type PaymentReceivedHandler struct {
	orderRepo    OrderRepository
	invoiceService InvoiceService
}

// NewPaymentReceivedHandler creates a new handler.
func NewPaymentReceivedHandler(
	orderRepo OrderRepository,
	invoiceService InvoiceService,
) *PaymentReceivedHandler {
	return &PaymentReceivedHandler{
		orderRepo:    orderRepo,
		invoiceService: invoiceService,
	}
}

// Handle processes the payment received event.
func (h *PaymentReceivedHandler) Handle(
	ctx context.Context,
	event *PaymentReceivedEvent,
) error {
	order, err := h.orderRepo.FindByID(ctx, event.OrderID)
	if err != nil {
		return fmt.Errorf("finding order: %w", err)
	}

	if err := order.MarkAsPaid(event.PaymentID); err != nil {
		return err
	}

	if err := h.orderRepo.Save(ctx, order); err != nil {
		return err
	}

	// Generate invoice
	return h.invoiceService.Generate(ctx, event.OrderID, event.PaymentID)
}

// EventBus dispatches domain events to handlers.
type EventBus interface {
	Subscribe(eventType string, handler interface{})
	Publish(ctx context.Context, event DomainEvent) error
	PublishAll(ctx context.Context, events []DomainEvent) error
}

// InMemoryEventBus is a simple in-memory event bus.
type InMemoryEventBus struct {
	handlers map[string][]interface{}
	mu       sync.RWMutex
}

// NewInMemoryEventBus creates a new event bus.
func NewInMemoryEventBus() *InMemoryEventBus {
	return &InMemoryEventBus{
		handlers: make(map[string][]interface{}),
	}
}

// Subscribe registers a handler for an event type.
func (b *InMemoryEventBus) Subscribe(eventType string, handler interface{}) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.handlers[eventType] = append(b.handlers[eventType], handler)
}

// Publish publishes a single event.
func (b *InMemoryEventBus) Publish(ctx context.Context, event DomainEvent) error {
	b.mu.RLock()
	handlers := b.handlers[event.EventType()]
	b.mu.RUnlock()

	for _, h := range handlers {
		// Type-safe handler invocation would require reflection or type switching
		if err := b.invokeHandler(ctx, h, event); err != nil {
			return err
		}
	}

	return nil
}

// PublishAll publishes multiple events.
func (b *InMemoryEventBus) PublishAll(ctx context.Context, events []DomainEvent) error {
	for _, event := range events {
		if err := b.Publish(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

func (b *InMemoryEventBus) invokeHandler(
	ctx context.Context,
	handler interface{},
	event DomainEvent,
) error {
	// Type switching for different handler types
	switch event.EventType() {
	case "OrderConfirmed":
		if h, ok := handler.(DomainEventHandler[*OrderConfirmedEvent]); ok {
			return h.Handle(ctx, event.(*OrderConfirmedEvent))
		}
	case "PaymentReceived":
		if h, ok := handler.(DomainEventHandler[*PaymentReceivedEvent]); ok {
			return h.Handle(ctx, event.(*PaymentReceivedEvent))
		}
	}
	return nil
}
```

## Event Sourcing Integration

```go
// EventStore persists domain events.
type EventStore interface {
	Append(ctx context.Context, aggregateID string, events []DomainEvent) error
	GetEvents(ctx context.Context, aggregateID string) ([]DomainEvent, error)
	GetEventsAfter(ctx context.Context, aggregateID string, version int) ([]DomainEvent, error)
}

// EventSourcedAggregate is an aggregate reconstituted from events.
type EventSourcedAggregate[TID comparable] struct {
	AggregateRoot[TID]
	version           int
	uncommittedEvents []DomainEvent
}

// Apply applies an event and records it as uncommitted.
func (a *EventSourcedAggregate[TID]) Apply(event DomainEvent) {
	a.when(event)
	a.uncommittedEvents = append(a.uncommittedEvents, event)
}

// When applies the event to the aggregate state.
func (a *EventSourcedAggregate[TID]) when(event DomainEvent) {
	// Subclass implements state transitions
}

// LoadFromHistory reconstitutes an aggregate from event history.
func LoadFromHistory[T EventSourcedAggregate[TID], TID comparable](
	events []DomainEvent,
) T {
	var aggregate T

	for _, event := range events {
		aggregate.when(event)
		aggregate.version++
	}

	return aggregate
}

// UncommittedEvents returns events not yet persisted.
func (a *EventSourcedAggregate[TID]) UncommittedEvents() []DomainEvent {
	events := make([]DomainEvent, len(a.uncommittedEvents))
	copy(events, a.uncommittedEvents)
	return events
}

// MarkEventsAsCommitted clears uncommitted events.
func (a *EventSourcedAggregate[TID]) MarkEventsAsCommitted() {
	a.uncommittedEvents = nil
}
```

## Recommended Libraries

| Library | Purpose | Link |
|---------|---------|------|
| **nats-io/nats.go** | Event messaging | `go get github.com/nats-io/nats.go` |
| **ThreeDotsLabs/watermill** | Event-driven | `go get github.com/ThreeDotsLabs/watermill` |
| **segmentio/kafka-go** | Kafka client | `go get github.com/segmentio/kafka-go` |

## Anti-patterns

1. **Technical Events**: Events about infrastructure, not domain

   ```go
   // BAD - Technical concern
   type DatabaseUpdatedEvent struct{}

   // GOOD - Business meaning
   type OrderPlacedEvent struct{}
   ```

2. **Mutable Events**: Modifying events after creation

   ```go
   // BAD
   event.OrderID = newOrderID // Mutation!

   // GOOD
   // All fields are read-only after construction
   ```

3. **Missing Context**: Event without enough information

   ```go
   // BAD - Not self-sufficient
   type OrderCreatedEvent struct {
       OrderID OrderID
   }

   // GOOD - Contains all needed context
   type OrderCreatedEvent struct {
       OrderID     OrderID
       CustomerID  CustomerID
       Items       []OrderItemSnapshot
       TotalAmount Money
   }
   ```

4. **Coupling via Events**: Handler knowing too much about producer

   ```go
   // BAD - Tight coupling
   func (h *OrderHandler) Handle(event *OrderCreatedEvent) error {
       order, _ := h.orderRepo.FindByID(event.OrderID) // Fetching more data
       // ...
   }
   ```

## When to Use

- Communication between aggregates
- Triggering side effects after state changes
- Building audit trails
- Implementing eventual consistency
- Enabling event sourcing

## Related Patterns

- [Aggregate](./aggregate.md) - Raises domain events
- [Repository](./repository.md) - Publishes events after save
- [Domain Service](./domain-service.md) - Can handle events
