# Event Sourcing

> Persist state as a sequence of events instead of a snapshot.

**Authors:** Martin Fowler, Greg Young

## Principle

```
┌─────────────────────────────────────────────────────────────────┐
│                    TRADITIONAL vs EVENT SOURCING                 │
│                                                                  │
│  TRADITIONAL (CRUD)              EVENT SOURCING                 │
│  ┌─────────────────┐            ┌─────────────────────────────┐ │
│  │  Current State  │            │       Event Stream           │ │
│  │                 │            │                              │ │
│  │  Balance: $100  │            │  [AccountCreated: $0]       │ │
│  │                 │            │  [MoneyDeposited: +$150]    │ │
│  │  (only latest)  │            │  [MoneyWithdrawn: -$50]     │ │
│  │                 │            │  [MoneyDeposited: +$20]     │ │
│  │                 │            │  [MoneyWithdrawn: -$20]     │ │
│  └─────────────────┘            │                              │ │
│                                 │  -> Replay = Balance: $100   │ │
│  No history                     └─────────────────────────────┘ │
│                                  Complete history                │
└─────────────────────────────────────────────────────────────────┘
```

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                      EVENT SOURCING SYSTEM                       │
│                                                                  │
│  ┌──────────┐    ┌──────────────┐    ┌──────────────────────┐  │
│  │ Command  │───►│   Aggregate  │───►│    Event Store       │  │
│  │          │    │              │    │                      │  │
│  │CreateOrder    │  Order       │    │  ┌────────────────┐  │  │
│  └──────────┘    │  ├─validate()│    │  │ OrderCreated   │  │  │
│                  │  └─apply()   │    │  │ ItemAdded      │  │  │
│                  └──────────────┘    │  │ ItemRemoved    │  │  │
│                                      │  │ OrderShipped   │  │  │
│                                      │  └────────────────┘  │  │
│                                      └──────────────────────┘  │
│                                               │                 │
│                                               │ Project         │
│                                               ▼                 │
│                                      ┌──────────────────────┐  │
│                                      │    Projections       │  │
│                                      │  ┌────────────────┐  │  │
│                                      │  │ OrderView (SQL)│  │  │
│                                      │  │ OrderSearch    │  │  │
│                                      │  │ (Elastic)      │  │  │
│                                      │  │ Analytics      │  │  │
│                                      │  └────────────────┘  │  │
│                                      └──────────────────────┘  │
└─────────────────────────────────────────────────────────────────┘
```

## Implementation

### Events

```go
package events

import "time"

// DomainEvent is the interface for all domain events.
type DomainEvent interface {
	GetID() string
	GetAggregateID() string
	GetAggregateType() string
	GetVersion() int
	GetTimestamp() time.Time
}

// BaseEvent is the base struct for domain events.
type BaseEvent struct {
	ID            string
	AggregateID   string
	AggregateType string
	Version       int
	Timestamp     time.Time
}

func (e BaseEvent) GetID() string            { return e.ID }
func (e BaseEvent) GetAggregateID() string   { return e.AggregateID }
func (e BaseEvent) GetAggregateType() string { return e.AggregateType }
func (e BaseEvent) GetVersion() int          { return e.Version }
func (e BaseEvent) GetTimestamp() time.Time  { return e.Timestamp }

// OrderItem represents an item in an order.
type OrderItem struct {
	ProductID string
	Quantity  int
	Price     float64
}

// OrderCreatedEvent represents an order creation event.
type OrderCreatedEvent struct {
	BaseEvent
	CustomerID string
	Items      []OrderItem
}

// ItemAddedEvent represents an item added to an order.
type ItemAddedEvent struct {
	BaseEvent
	ProductID string
	Quantity  int
	Price     float64
}

// OrderShippedEvent represents an order shipment event.
type OrderShippedEvent struct {
	BaseEvent
	TrackingNumber string
	Carrier        string
}
```

### Aggregate

```go
package domain

import (
	"fmt"
	"time"
)

// OrderStatus represents the status of an order.
type OrderStatus string

const (
	OrderStatusPending  OrderStatus = "pending"
	OrderStatusShipped  OrderStatus = "shipped"
	OrderStatusCanceled OrderStatus = "canceled"
)

// Order is the aggregate root for orders.
type Order struct {
	id                string
	customerID        string
	items             []OrderItem
	status            OrderStatus
	version           int
	uncommittedEvents []DomainEvent
}

// FromEvents reconstructs an order from events.
func FromEvents(events []DomainEvent) *Order {
	order := &Order{
		items:  make([]OrderItem, 0),
		status: OrderStatusPending,
	}

	for _, event := range events {
		order.apply(event)
		order.version = event.GetVersion()
	}

	return order
}

// AddItem adds an item to the order.
func (o *Order) AddItem(productID string, quantity int, price float64) error {
	if o.status != OrderStatusPending {
		return fmt.Errorf("cannot add items to %s order", o.status)
	}

	event := &ItemAddedEvent{
		BaseEvent: BaseEvent{
			ID:            GenerateID(),
			AggregateID:   o.id,
			AggregateType: "Order",
			Version:       o.version + 1,
			Timestamp:     time.Now(),
		},
		ProductID: productID,
		Quantity:  quantity,
		Price:     price,
	}

	o.apply(event)
	o.uncommittedEvents = append(o.uncommittedEvents, event)

	return nil
}

// Ship ships the order.
func (o *Order) Ship(trackingNumber, carrier string) error {
	if o.status != OrderStatusPending {
		return fmt.Errorf("order already %s", o.status)
	}

	event := &OrderShippedEvent{
		BaseEvent: BaseEvent{
			ID:            GenerateID(),
			AggregateID:   o.id,
			AggregateType: "Order",
			Version:       o.version + 1,
			Timestamp:     time.Now(),
		},
		TrackingNumber: trackingNumber,
		Carrier:        carrier,
	}

	o.apply(event)
	o.uncommittedEvents = append(o.uncommittedEvents, event)

	return nil
}

// apply applies an event to the aggregate (modifies state).
func (o *Order) apply(event DomainEvent) {
	switch e := event.(type) {
	case *OrderCreatedEvent:
		o.id = e.GetAggregateID()
		o.customerID = e.CustomerID
		o.items = e.Items

	case *ItemAddedEvent:
		o.items = append(o.items, OrderItem{
			ProductID: e.ProductID,
			Quantity:  e.Quantity,
			Price:     e.Price,
		})

	case *OrderShippedEvent:
		o.status = OrderStatusShipped
	}

	o.version = event.GetVersion()
}

// GetUncommittedEvents returns uncommitted events.
func (o *Order) GetUncommittedEvents() []DomainEvent {
	events := make([]DomainEvent, len(o.uncommittedEvents))
	copy(events, o.uncommittedEvents)
	return events
}

// MarkEventsAsCommitted marks all uncommitted events as committed.
func (o *Order) MarkEventsAsCommitted() {
	o.uncommittedEvents = make([]DomainEvent, 0)
}
```

### Event Store

```go
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// EventStore defines the interface for event storage.
type EventStore interface {
	Append(ctx context.Context, events []DomainEvent) error
	GetEvents(ctx context.Context, aggregateID string) ([]DomainEvent, error)
	GetEventsAfter(ctx context.Context, position int) ([]DomainEvent, error)
	Subscribe(handler func(DomainEvent))
}

// ConcurrencyError indicates a version conflict.
type ConcurrencyError struct {
	AggregateID string
	Expected    int
	Actual      int
}

func (e *ConcurrencyError) Error() string {
	return fmt.Sprintf("concurrency error for aggregate %s: expected version %d, got %d",
		e.AggregateID, e.Expected, e.Actual)
}

// PostgresEventStore implements EventStore with PostgreSQL.
type PostgresEventStore struct {
	db *sql.DB
}

// NewPostgresEventStore creates a new PostgreSQL event store.
func NewPostgresEventStore(db *sql.DB) *PostgresEventStore {
	return &PostgresEventStore{db: db}
}

// Append appends events to the store with optimistic concurrency.
func (s *PostgresEventStore) Append(ctx context.Context, events []DomainEvent) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	for _, event := range events {
		payload, err := json.Marshal(event)
		if err != nil {
			return fmt.Errorf("marshaling event: %w", err)
		}

		query := `
			INSERT INTO events (
				id, aggregate_id, aggregate_type, type, version, timestamp, payload
			) VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (aggregate_id, version) DO NOTHING
			RETURNING id
		`

		var returnedID string
		err = tx.QueryRowContext(ctx, query,
			event.GetID(),
			event.GetAggregateID(),
			event.GetAggregateType(),
			fmt.Sprintf("%T", event),
			event.GetVersion(),
			event.GetTimestamp(),
			payload,
		).Scan(&returnedID)

		if err == sql.ErrNoRows {
			return &ConcurrencyError{
				AggregateID: event.GetAggregateID(),
				Expected:    event.GetVersion(),
			}
		}
		if err != nil {
			return fmt.Errorf("inserting event: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}

	return nil
}

// GetEvents retrieves all events for an aggregate.
func (s *PostgresEventStore) GetEvents(ctx context.Context, aggregateID string) ([]DomainEvent, error) {
	query := `
		SELECT id, aggregate_id, aggregate_type, type, version, timestamp, payload
		FROM events
		WHERE aggregate_id = $1
		ORDER BY version ASC
	`

	rows, err := s.db.QueryContext(ctx, query, aggregateID)
	if err != nil {
		return nil, fmt.Errorf("querying events: %w", err)
	}
	defer rows.Close()

	events := make([]DomainEvent, 0)
	for rows.Next() {
		event, err := s.scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating events: %w", err)
	}

	return events, nil
}

// GetEventsAfter retrieves events after a position.
func (s *PostgresEventStore) GetEventsAfter(ctx context.Context, position int) ([]DomainEvent, error) {
	query := `
		SELECT id, aggregate_id, aggregate_type, type, version, timestamp, payload
		FROM events
		WHERE position > $1
		ORDER BY position ASC
	`

	rows, err := s.db.QueryContext(ctx, query, position)
	if err != nil {
		return nil, fmt.Errorf("querying events: %w", err)
	}
	defer rows.Close()

	events := make([]DomainEvent, 0)
	for rows.Next() {
		event, err := s.scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}

	return events, nil
}

func (s *PostgresEventStore) scanEvent(rows *sql.Rows) (DomainEvent, error) {
	var (
		id            string
		aggregateID   string
		aggregateType string
		eventType     string
		version       int
		timestamp     time.Time
		payload       []byte
	)

	if err := rows.Scan(&id, &aggregateID, &aggregateType, &eventType, &version, &timestamp, &payload); err != nil {
		return nil, fmt.Errorf("scanning event: %w", err)
	}

	// Deserialize based on event type
	var event DomainEvent
	switch eventType {
	case "*events.OrderCreatedEvent":
		var e OrderCreatedEvent
		if err := json.Unmarshal(payload, &e); err != nil {
			return nil, fmt.Errorf("unmarshaling OrderCreatedEvent: %w", err)
		}
		event = &e
	case "*events.ItemAddedEvent":
		var e ItemAddedEvent
		if err := json.Unmarshal(payload, &e); err != nil {
			return nil, fmt.Errorf("unmarshaling ItemAddedEvent: %w", err)
		}
		event = &e
	case "*events.OrderShippedEvent":
		var e OrderShippedEvent
		if err := json.Unmarshal(payload, &e); err != nil {
			return nil, fmt.Errorf("unmarshaling OrderShippedEvent: %w", err)
		}
		event = &e
	default:
		return nil, fmt.Errorf("unknown event type: %s", eventType)
	}

	return event, nil
}

// Subscribe subscribes to events (simplified implementation).
func (s *PostgresEventStore) Subscribe(handler func(DomainEvent)) {
	// Implementation would use LISTEN/NOTIFY or polling
}
```

### Projections

```go
package projections

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// OrderView is the denormalized read model for orders.
type OrderView struct {
	ID         string
	CustomerID string
	Status     string
	TotalAmount float64
	CreatedAt  time.Time
	ShippedAt  *time.Time
}

// OrderProjection projects events to the read model.
type OrderProjection struct {
	eventStore EventStore
	readDB     *sql.DB
}

// NewOrderProjection creates a new order projection.
func NewOrderProjection(eventStore EventStore, readDB *sql.DB) *OrderProjection {
	p := &OrderProjection{
		eventStore: eventStore,
		readDB:     readDB,
	}

	eventStore.Subscribe(func(event DomainEvent) {
		if err := p.handle(context.Background(), event); err != nil {
			fmt.Printf("error handling event: %v\n", err)
		}
	})

	return p
}

// handle handles domain events.
func (p *OrderProjection) handle(ctx context.Context, event DomainEvent) error {
	switch e := event.(type) {
	case *OrderCreatedEvent:
		return p.handleOrderCreated(ctx, e)
	case *ItemAddedEvent:
		return p.handleItemAdded(ctx, e)
	case *OrderShippedEvent:
		return p.handleOrderShipped(ctx, e)
	default:
		return nil
	}
}

func (p *OrderProjection) handleOrderCreated(ctx context.Context, event *OrderCreatedEvent) error {
	query := `
		INSERT INTO orders (id, customer_id, status, total_amount, created_at)
		VALUES ($1, $2, $3, $4, $5)
	`

	_, err := p.readDB.ExecContext(ctx, query,
		event.GetAggregateID(),
		event.CustomerID,
		"pending",
		0.0,
		event.GetTimestamp(),
	)

	return err
}

func (p *OrderProjection) handleItemAdded(ctx context.Context, event *ItemAddedEvent) error {
	// Insert order item
	query := `
		INSERT INTO order_items (order_id, product_id, quantity, price)
		VALUES ($1, $2, $3, $4)
	`

	_, err := p.readDB.ExecContext(ctx, query,
		event.GetAggregateID(),
		event.ProductID,
		event.Quantity,
		event.Price,
	)
	if err != nil {
		return err
	}

	// Update total
	updateQuery := `
		UPDATE orders
		SET total_amount = (
			SELECT SUM(price * quantity)
			FROM order_items
			WHERE order_id = $1
		)
		WHERE id = $1
	`

	_, err = p.readDB.ExecContext(ctx, updateQuery, event.GetAggregateID())
	return err
}

func (p *OrderProjection) handleOrderShipped(ctx context.Context, event *OrderShippedEvent) error {
	query := `
		UPDATE orders
		SET status = $1, shipped_at = $2
		WHERE id = $3
	`

	_, err := p.readDB.ExecContext(ctx, query,
		"shipped",
		event.GetTimestamp(),
		event.GetAggregateID(),
	)

	return err
}

// Rebuild rebuilds the projection from scratch.
func (p *OrderProjection) Rebuild(ctx context.Context) error {
	// Truncate read model
	if _, err := p.readDB.ExecContext(ctx, "TRUNCATE orders, order_items"); err != nil {
		return fmt.Errorf("truncating tables: %w", err)
	}

	// Replay all events
	events, err := p.eventStore.GetEventsAfter(ctx, 0)
	if err != nil {
		return fmt.Errorf("getting events: %w", err)
	}

	for _, event := range events {
		if err := p.handle(ctx, event); err != nil {
			return fmt.Errorf("handling event: %w", err)
		}
	}

	return nil
}
```

## When to Use

| Use | Avoid |
|----------|--------|
| Audit trail required | Simple CRUD |
| Compliance (finance, health) | No history needed |
| Debug / Replay | Inexperienced team |
| Temporal analytics | Critical performance |
| Undo/Redo needed | Enormous volume |

## Advantages

- **Complete audit**: Every change tracked
- **Replay**: Reconstruct state at any time
- **Debug**: Understand what happened
- **Multiple projections**: Optimized views
- **Temporal queries**: "State at date X?"
- **Event-driven**: React to changes

## Disadvantages

- **Complexity**: Advanced pattern
- **Volume**: Many events
- **Schema evolution**: Immutable events
- **Eventual consistency**: Asynchronous projections
- **Learning curve**: Different paradigm

## Real-world Examples

| Company | Usage |
|------------|-------|
| **LMAX** | Trading (millions events/sec) |
| **Microsoft** | Azure (Event Grid) |
| **LinkedIn** | Kafka (event backbone) |
| **Netflix** | Zuul (request events) |
| **Uber** | Trip events |

## Migration Path

### From CRUD

```
Phase 1: Dual-write (CRUD + Events)
Phase 2: Event-first (CRUD from projection)
Phase 3: Pure Event Sourcing
```

### Complementary Patterns

```
Event Sourcing + CQRS
         │
         ├── Commands -> Write side -> Events
         │
         └── Queries -> Read side <- Projections
```

## Related Patterns

| Pattern | Relationship |
|---------|----------|
| CQRS | Often combined |
| Saga | Distributed transactions |
| Event-Driven | Underlying architecture |
| Snapshot | Performance optimization |

## Sources

- [Martin Fowler - Event Sourcing](https://martinfowler.com/eaaDev/EventSourcing.html)
- [Greg Young - Event Sourcing](https://cqrs.files.wordpress.com/2010/11/cqrs_documents.pdf)
- [EventStoreDB](https://www.eventstore.com/)
- [Axon Framework](https://axoniq.io/)
