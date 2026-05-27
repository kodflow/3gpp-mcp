# Repository (PoEAA)

> "Mediates between the domain and data mapping layers using a collection-like interface for accessing domain objects." - Martin Fowler, PoEAA

## Concept

The Repository acts as an in-memory collection of domain objects. It hides data access details and provides a domain-oriented interface for persistence.

## Key Principles

1. **Collection-like**: Interface like a collection (add, remove, find)
2. **Domain-centric**: Search methods based on the domain
3. **Encapsulation**: Hides persistence details
4. **One per Aggregate**: In DDD, one Repository per Aggregate Root

## Go Implementation

```go
package repository

import (
	"context"
	"database/sql"
	"fmt"
)

// Repository is a generic repository interface.
type Repository[T Entity, ID comparable] interface {
	FindByID(ctx context.Context, id ID) (T, error)
	FindAll(ctx context.Context) ([]T, error)
	Save(ctx context.Context, entity T) error
	Delete(ctx context.Context, entity T) error
	Exists(ctx context.Context, id ID) (bool, error)
}

// Entity represents a domain entity.
type Entity interface {
	GetID() string
}

// OrderID represents an order identifier.
type OrderID struct {
	value string
}

// NewOrderID creates a new order ID.
func NewOrderID(value string) OrderID {
	return OrderID{value: value}
}

func (id OrderID) String() string { return id.value }

// OrderStatus represents an order status.
type OrderStatus string

const (
	OrderStatusDraft     OrderStatus = "draft"
	OrderStatusSubmitted OrderStatus = "submitted"
	OrderStatusPaid      OrderStatus = "paid"
)

// OrderRepository is a domain-specific repository.
type OrderRepository interface {
	FindByID(ctx context.Context, id OrderID) (*Order, error)
	FindAll(ctx context.Context) ([]*Order, error)
	FindByCustomerID(ctx context.Context, customerID CustomerID) ([]*Order, error)
	FindByStatus(ctx context.Context, status OrderStatus) ([]*Order, error)
	FindPendingOlderThan(ctx context.Context, date time.Time) ([]*Order, error)
	Save(ctx context.Context, order *Order) error
	Delete(ctx context.Context, order *Order) error
	Exists(ctx context.Context, id OrderID) (bool, error)
	NextID() OrderID
}

// PostgresOrderRepository is a PostgreSQL implementation.
type PostgresOrderRepository struct {
	db          *sql.DB
	mapper      *OrderDataMapper
	identityMap *IdentityMap[*Order]
}

// NewPostgresOrderRepository creates a new PostgreSQL order repository.
func NewPostgresOrderRepository(
	db *sql.DB,
	mapper *OrderDataMapper,
	identityMap *IdentityMap[*Order],
) *PostgresOrderRepository {
	return &PostgresOrderRepository{
		db:          db,
		mapper:      mapper,
		identityMap: identityMap,
	}
}

// FindByID finds an order by ID.
func (r *PostgresOrderRepository) FindByID(ctx context.Context, id OrderID) (*Order, error) {
	// Check identity map first
	if order, ok := r.identityMap.Get(id.String()); ok {
		return order, nil
	}

	// Load from database
	order, err := r.mapper.FindByID(ctx, id.String())
	if err != nil {
		return nil, fmt.Errorf("mapper find by id: %w", err)
	}
	if order == nil {
		return nil, nil
	}

	r.identityMap.Add(order)
	return order, nil
}

// FindAll returns all orders.
func (r *PostgresOrderRepository) FindAll(ctx context.Context) ([]*Order, error) {
	return r.mapper.FindAll(ctx)
}

// FindByCustomerID finds orders by customer ID.
func (r *PostgresOrderRepository) FindByCustomerID(ctx context.Context, customerID CustomerID) ([]*Order, error) {
	return r.mapper.FindByCustomerID(ctx, customerID.String())
}

// FindByStatus finds orders by status.
func (r *PostgresOrderRepository) FindByStatus(ctx context.Context, status OrderStatus) ([]*Order, error) {
	return r.mapper.FindByStatus(ctx, status)
}

// FindPendingOlderThan finds pending orders older than a date.
func (r *PostgresOrderRepository) FindPendingOlderThan(ctx context.Context, date time.Time) ([]*Order, error) {
	query := `SELECT * FROM orders WHERE status = 'pending' AND created_at < ?`
	rows, err := r.db.QueryContext(ctx, query, date)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var orders []*Order
	for rows.Next() {
		order, err := r.mapper.ScanRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		orders = append(orders, order)
	}

	return orders, rows.Err()
}

// Save saves an order.
func (r *PostgresOrderRepository) Save(ctx context.Context, order *Order) error {
	exists, err := r.Exists(ctx, NewOrderID(order.GetID()))
	if err != nil {
		return fmt.Errorf("check exists: %w", err)
	}

	if exists {
		if err := r.mapper.Update(ctx, order); err != nil {
			return fmt.Errorf("update: %w", err)
		}
	} else {
		if err := r.mapper.Insert(ctx, order); err != nil {
			return fmt.Errorf("insert: %w", err)
		}
	}

	r.identityMap.Add(order)
	return nil
}

// Delete deletes an order.
func (r *PostgresOrderRepository) Delete(ctx context.Context, order *Order) error {
	if err := r.mapper.Delete(ctx, order.GetID()); err != nil {
		return fmt.Errorf("mapper delete: %w", err)
	}
	r.identityMap.Remove(order.GetID())
	return nil
}

// Exists checks if an order exists.
func (r *PostgresOrderRepository) Exists(ctx context.Context, id OrderID) (bool, error) {
	var exists bool
	err := r.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM orders WHERE id = ?)`,
		id.String(),
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("query exists: %w", err)
	}
	return exists, nil
}

// NextID generates a new order ID.
func (r *PostgresOrderRepository) NextID() OrderID {
	return NewOrderID(uuid.New().String())
}

// InMemoryOrderRepository is an in-memory implementation for testing.
type InMemoryOrderRepository struct {
	orders    map[string]*Order
	idCounter int
	mu        sync.RWMutex
}

// NewInMemoryOrderRepository creates a new in-memory repository.
func NewInMemoryOrderRepository() *InMemoryOrderRepository {
	return &InMemoryOrderRepository{
		orders: make(map[string]*Order),
	}
}

// FindByID finds an order by ID.
func (r *InMemoryOrderRepository) FindByID(ctx context.Context, id OrderID) (*Order, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	order, ok := r.orders[id.String()]
	if !ok {
		return nil, nil
	}
	return order, nil
}

// FindAll returns all orders.
func (r *InMemoryOrderRepository) FindAll(ctx context.Context) ([]*Order, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	orders := make([]*Order, 0, len(r.orders))
	for _, order := range r.orders {
		orders = append(orders, order)
	}
	return orders, nil
}

// FindByCustomerID finds orders by customer ID.
func (r *InMemoryOrderRepository) FindByCustomerID(ctx context.Context, customerID CustomerID) ([]*Order, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var orders []*Order
	for _, order := range r.orders {
		if order.CustomerID() == customerID.String() {
			orders = append(orders, order)
		}
	}
	return orders, nil
}

// FindByStatus finds orders by status.
func (r *InMemoryOrderRepository) FindByStatus(ctx context.Context, status OrderStatus) ([]*Order, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var orders []*Order
	for _, order := range r.orders {
		if order.Status() == status {
			orders = append(orders, order)
		}
	}
	return orders, nil
}

// Save saves an order.
func (r *InMemoryOrderRepository) Save(ctx context.Context, order *Order) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.orders[order.GetID()] = order
	return nil
}

// Delete deletes an order.
func (r *InMemoryOrderRepository) Delete(ctx context.Context, order *Order) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.orders, order.GetID())
	return nil
}

// Exists checks if an order exists.
func (r *InMemoryOrderRepository) Exists(ctx context.Context, id OrderID) (bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, exists := r.orders[id.String()]
	return exists, nil
}

// NextID generates a new order ID.
func (r *InMemoryOrderRepository) NextID() OrderID {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.idCounter++
	return NewOrderID(fmt.Sprintf("order-%d", r.idCounter))
}

// Clear clears all orders (test helper).
func (r *InMemoryOrderRepository) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.orders = make(map[string]*Order)
	r.idCounter = 0
}

// Count returns the number of orders (test helper).
func (r *InMemoryOrderRepository) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.orders)
}
```

## Comparison with Alternatives

| Aspect | Repository | DAO | Active Record |
|--------|------------|-----|---------------|
| Abstraction | Collection | CRUD table | Self-persisting |
| Focus | Domain | Data | Convenience |
| Queries | Domain-centric | SQL-centric | Mixed |
| Testability | Excellent | Medium | Medium |
| DDD compatible | Yes | No | No |

## When to Use

**Use Repository when:**

- Domain Model with aggregates
- Need for testability (in-memory repos)
- Domain-oriented queries
- Multiple possible data sources
- DDD architecture

**Avoid Repository when:**

- Simple CRUD (overkill)
- Complex SQL queries (use Query Objects)
- No Domain Model

## Related Patterns

- [Data Mapper](./data-mapper.md) - Mapping between domain and database
- [Unit of Work](./unit-of-work.md) - Transactional management with Repository
- [Identity Map](./identity-map.md) - Cache of loaded entities
- [Domain Model](./domain-model.md) - Aggregates accessed via Repository

## Sources

- Martin Fowler, PoEAA, Chapter 10
- Eric Evans, DDD - Repositories
- [Repository - martinfowler.com](https://martinfowler.com/eaaCatalog/repository.html)
