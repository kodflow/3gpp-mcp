# Repository Pattern (DDD)

> Mediator between the domain and data mapping layers, acting as an in-memory collection of domain objects.

## Definition

A **Repository** mediates between the domain and data mapping layers, acting as an in-memory collection of domain objects. It encapsulates persistence logic while providing a collection-like interface for accessing aggregates.

```
Repository = Collection Abstraction + Persistence Encapsulation + Query Isolation
```

**Key characteristics:**

- **Aggregate-centric**: One repository per aggregate root
- **Collection semantics**: Acts like an in-memory collection
- **Persistence ignorance**: Domain doesn't know about storage
- **Query encapsulation**: Complex queries hidden behind methods
- **Unit of Work integration**: Transaction boundary awareness

## Go Implementation

```go
package domain

import (
	"context"
	"errors"
)

// Repository provides generic repository operations.
type Repository[T AggregateRoot[TID], TID comparable] interface {
	FindByID(ctx context.Context, id TID) (T, error)
	Save(ctx context.Context, aggregate T) error
	Delete(ctx context.Context, aggregate T) error
	Exists(ctx context.Context, id TID) (bool, error)
}

// OrderRepository defines domain-specific repository operations.
type OrderRepository interface {
	FindByID(ctx context.Context, id OrderID) (*Order, error)
	FindByCustomer(ctx context.Context, customerID CustomerID) ([]*Order, error)
	FindPendingOrders(ctx context.Context) ([]*Order, error)
	FindByStatus(ctx context.Context, status OrderStatus) ([]*Order, error)
	Save(ctx context.Context, order *Order) error
	Delete(ctx context.Context, order *Order) error
	Exists(ctx context.Context, id OrderID) (bool, error)
}

// PostgresOrderRepository implements OrderRepository using PostgreSQL.
type PostgresOrderRepository struct {
	db       *sql.DB
	eventBus EventBus
}

// NewPostgresOrderRepository creates a new repository instance.
func NewPostgresOrderRepository(db *sql.DB, eventBus EventBus) *PostgresOrderRepository {
	return &PostgresOrderRepository{
		db:       db,
		eventBus: eventBus,
	}
}

// FindByID retrieves an order by ID.
func (r *PostgresOrderRepository) FindByID(ctx context.Context, id OrderID) (*Order, error) {
	query := `
		SELECT id, customer_id, status, shipping_address, created_at, version
		FROM orders WHERE id = $1
	`

	var entity OrderEntity
	err := r.db.QueryRowContext(ctx, query, id.Value()).Scan(
		&entity.ID,
		&entity.CustomerID,
		&entity.Status,
		&entity.ShippingAddress,
		&entity.CreatedAt,
		&entity.Version,
	)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrOrderNotFound
		}
		return nil, fmt.Errorf("finding order %s: %w", id.Value(), err)
	}

	// Load items
	items, err := r.loadOrderItems(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("loading order items: %w", err)
	}
	entity.Items = items

	return r.toDomain(entity)
}

// FindByCustomer retrieves all orders for a customer.
func (r *PostgresOrderRepository) FindByCustomer(
	ctx context.Context,
	customerID CustomerID,
) ([]*Order, error) {
	query := `
		SELECT id, customer_id, status, shipping_address, created_at, version
		FROM orders
		WHERE customer_id = $1
		ORDER BY created_at DESC
	`

	rows, err := r.db.QueryContext(ctx, query, customerID.Value())
	if err != nil {
		return nil, fmt.Errorf("querying orders by customer: %w", err)
	}
	defer rows.Close()

	var orders []*Order
	for rows.Next() {
		var entity OrderEntity
		if err := rows.Scan(
			&entity.ID,
			&entity.CustomerID,
			&entity.Status,
			&entity.ShippingAddress,
			&entity.CreatedAt,
			&entity.Version,
		); err != nil {
			return nil, err
		}

		items, err := r.loadOrderItems(ctx, mustParseOrderID(entity.ID))
		if err != nil {
			return nil, err
		}
		entity.Items = items

		order, err := r.toDomain(entity)
		if err != nil {
			return nil, err
		}
		orders = append(orders, order)
	}

	return orders, rows.Err()
}

// Save persists an order and publishes domain events.
func (r *PostgresOrderRepository) Save(ctx context.Context, order *Order) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	entity := r.toEntity(order)

	// Optimistic locking
	query := `
		INSERT INTO orders (id, customer_id, status, shipping_address, created_at, version)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (id) DO UPDATE SET
			customer_id = EXCLUDED.customer_id,
			status = EXCLUDED.status,
			shipping_address = EXCLUDED.shipping_address,
			version = EXCLUDED.version
		WHERE orders.version = $6
	`

	result, err := tx.ExecContext(ctx, query,
		entity.ID,
		entity.CustomerID,
		entity.Status,
		entity.ShippingAddress,
		entity.CreatedAt,
		entity.Version,
	)
	if err != nil {
		return fmt.Errorf("saving order: %w", err)
	}

	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrOptimisticLockConflict
	}

	// Save items
	if err := r.saveOrderItems(ctx, tx, order); err != nil {
		return fmt.Errorf("saving order items: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}

	// Publish domain events after successful save
	events := order.PullDomainEvents()
	for _, event := range events {
		if err := r.eventBus.Publish(ctx, event); err != nil {
			return fmt.Errorf("publishing event: %w", err)
		}
	}

	order.IncrementVersion()
	return nil
}

// Delete removes an order.
func (r *PostgresOrderRepository) Delete(ctx context.Context, order *Order) error {
	query := `DELETE FROM orders WHERE id = $1`
	_, err := r.db.ExecContext(ctx, query, order.ID().Value())
	if err != nil {
		return fmt.Errorf("deleting order: %w", err)
	}
	return nil
}

// Exists checks if an order exists.
func (r *PostgresOrderRepository) Exists(ctx context.Context, id OrderID) (bool, error) {
	query := `SELECT EXISTS(SELECT 1 FROM orders WHERE id = $1)`

	var exists bool
	err := r.db.QueryRowContext(ctx, query, id.Value()).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("checking order existence: %w", err)
	}

	return exists, nil
}

// Mapper: Entity -> Domain
func (r *PostgresOrderRepository) toDomain(entity OrderEntity) (*Order, error) {
	items := make([]OrderItem, len(entity.Items))
	for i, item := range entity.Items {
		itemID, err := OrderItemIDFrom(item.ID)
		if err != nil {
			return nil, err
		}

		productID, err := ProductIDFrom(item.ProductID)
		if err != nil {
			return nil, err
		}

		quantity, err := NewQuantity(item.Quantity)
		if err != nil {
			return nil, err
		}

		money, err := NewMoney(item.UnitPrice, CurrencyUSD)
		if err != nil {
			return nil, err
		}

		items[i] = ReconstituteOrderItem(itemID, productID, quantity, money)
	}

	orderID, err := OrderIDFrom(entity.ID)
	if err != nil {
		return nil, err
	}

	customerID, err := CustomerIDFrom(entity.CustomerID)
	if err != nil {
		return nil, err
	}

	address, err := AddressFromJSON(entity.ShippingAddress)
	if err != nil {
		return nil, err
	}

	return ReconstituteOrder(
		orderID,
		customerID,
		items,
		OrderStatus(entity.Status),
		address,
		entity.CreatedAt,
		entity.Version,
	), nil
}

// Mapper: Domain -> Entity
func (r *PostgresOrderRepository) toEntity(order *Order) OrderEntity {
	items := make([]OrderItemEntity, len(order.Items()))
	for i, item := range order.Items() {
		items[i] = OrderItemEntity{
			ID:        item.ID().Value(),
			ProductID: item.ProductID().Value(),
			Quantity:  item.Quantity().Value(),
			UnitPrice: item.UnitPrice().Amount(),
		}
	}

	return OrderEntity{
		ID:              order.ID().Value(),
		CustomerID:      order.CustomerID().Value(),
		Status:          string(order.Status()),
		ShippingAddress: order.ShippingAddress().ToJSON(),
		Version:         order.Version(),
		Items:           items,
		CreatedAt:       order.CreatedAt(),
	}
}
```

## In-Memory Repository (Testing)

```go
// InMemoryOrderRepository provides an in-memory implementation for testing.
type InMemoryOrderRepository struct {
	orders          map[string]*Order
	publishedEvents []DomainEvent
	mu              sync.RWMutex
}

// NewInMemoryOrderRepository creates a new in-memory repository.
func NewInMemoryOrderRepository() *InMemoryOrderRepository {
	return &InMemoryOrderRepository{
		orders:          make(map[string]*Order),
		publishedEvents: make([]DomainEvent, 0),
	}
}

// FindByID retrieves an order by ID.
func (r *InMemoryOrderRepository) FindByID(ctx context.Context, id OrderID) (*Order, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	order, ok := r.orders[id.Value()]
	if !ok {
		return nil, ErrOrderNotFound
	}

	return order, nil
}

// FindByCustomer retrieves orders for a customer.
func (r *InMemoryOrderRepository) FindByCustomer(
	ctx context.Context,
	customerID CustomerID,
) ([]*Order, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var orders []*Order
	for _, order := range r.orders {
		if order.CustomerID().Equals(customerID) {
			orders = append(orders, order)
		}
	}

	return orders, nil
}

// Save persists an order.
func (r *InMemoryOrderRepository) Save(ctx context.Context, order *Order) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Clone to simulate persistence behavior
	r.orders[order.ID().Value()] = order

	// Collect events for testing
	events := order.PullDomainEvents()
	r.publishedEvents = append(r.publishedEvents, events...)

	order.IncrementVersion()
	return nil
}

// Delete removes an order.
func (r *InMemoryOrderRepository) Delete(ctx context.Context, order *Order) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.orders, order.ID().Value())
	return nil
}

// Exists checks if an order exists.
func (r *InMemoryOrderRepository) Exists(ctx context.Context, id OrderID) (bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, ok := r.orders[id.Value()]
	return ok, nil
}

// Clear removes all orders (test helper).
func (r *InMemoryOrderRepository) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.orders = make(map[string]*Order)
	r.publishedEvents = make([]DomainEvent, 0)
}

// PublishedEvents returns all published events (test helper).
func (r *InMemoryOrderRepository) PublishedEvents() []DomainEvent {
	r.mu.RLock()
	defer r.mu.RUnlock()

	events := make([]DomainEvent, len(r.publishedEvents))
	copy(events, r.publishedEvents)
	return events
}
```

## OOP vs FP Comparison

```go
// FP-style Repository using functional patterns

// OrderRepository is a collection of repository functions.
type OrderRepository struct {
	FindByID func(ctx context.Context, id OrderID) (*Order, error)
	Save     func(ctx context.Context, order *Order) error
	Delete   func(ctx context.Context, order *Order) error
}

// NewOrderRepository creates a repository with dependencies injected.
func NewOrderRepository(db *sql.DB, eventBus EventBus) OrderRepository {
	return OrderRepository{
		FindByID: func(ctx context.Context, id OrderID) (*Order, error) {
			// Implementation
			return nil, nil
		},
		Save: func(ctx context.Context, order *Order) error {
			// Implementation
			return nil
		},
		Delete: func(ctx context.Context, order *Order) error {
			// Implementation
			return nil
		},
	}
}
```

## Recommended Libraries

| Library | Purpose | Link |
|---------|---------|------|
| **sqlx** | SQL extensions | `go get github.com/jmoiron/sqlx` |
| **pgx** | PostgreSQL driver | `go get github.com/jackc/pgx/v5` |
| **ent** | ORM with code generation | `go get entgo.io/ent` |
| **gorm** | Full-featured ORM | `go get gorm.io/gorm` |

## Anti-patterns

1. **Generic Repository**: Over-abstracting with generic CRUD

   ```go
   // BAD - Not domain-driven
   type Repository[T any] interface {
       Find(criteria map[string]interface{}) ([]T, error)
       Save(entity T) error
   }
   ```

2. **Exposing Query Details**: Leaking ORM into domain

   ```go
   // BAD - ORM concepts in domain
   type OrderRepository interface {
       FindByQuery(query *sql.Rows) ([]*Order, error)
   }
   ```

3. **Multiple Aggregates**: One repository for multiple roots

   ```go
   // BAD
   type OrderCustomerRepository interface {
       FindOrder(id OrderID) (*Order, error)
       FindCustomer(id CustomerID) (*Customer, error)
   }
   ```

4. **Missing Domain Events**: Not publishing events after save

   ```go
   // BAD - Events lost
   func (r *Repository) Save(ctx context.Context, order *Order) error {
       // Save to database
       // Missing: order.PullDomainEvents() and publish
       return nil
   }
   ```

## When to Use

- Persistence and retrieval of aggregate roots
- Encapsulation of complex query logic
- Abstraction of data access technology
- Testing with in-memory implementations

## Related Patterns

- [Aggregate](./aggregate.md) - Repository per aggregate root
- [Specification](./specification.md) - Query composition
- [Domain Event](./domain-event.md) - Published after save
