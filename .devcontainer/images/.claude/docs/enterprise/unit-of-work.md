# Unit of Work

> "Maintains a list of objects affected by a business transaction and coordinates the writing out of changes and the resolution of concurrency problems." - Martin Fowler, PoEAA

## Concept

Unit of Work is a pattern that keeps track of all modifications made during a business transaction and coordinates writing those changes in a single atomic operation.

## Responsibilities

1. **Tracking**: Track new, modified, and deleted objects
2. **Commit**: Persist all changes in a single transaction
3. **Rollback**: Undo changes in case of error
4. **Concurrency**: Handle concurrency conflicts

## Go Implementation

```go
package uow

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
)

// Entity represents a domain entity with an ID.
type Entity interface {
	GetID() string
}

// DataMapper handles persistence for a specific entity type.
type DataMapper[T Entity] interface {
	Insert(ctx context.Context, tx *sql.Tx, entity T) error
	Update(ctx context.Context, tx *sql.Tx, entity T) error
	Delete(ctx context.Context, tx *sql.Tx, entity T) error
}

// MapperRegistry stores mappers by entity type.
type MapperRegistry struct {
	mu      sync.RWMutex
	mappers map[string]any
}

// NewMapperRegistry creates a new mapper registry.
func NewMapperRegistry() *MapperRegistry {
	return &MapperRegistry{
		mappers: make(map[string]any),
	}
}

// Register registers a mapper for an entity type.
func Register[T Entity](r *MapperRegistry, typeName string, mapper DataMapper[T]) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mappers[typeName] = mapper
}

// GetMapper retrieves a mapper for an entity type.
func GetMapper[T Entity](r *MapperRegistry, typeName string) (DataMapper[T], error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	mapper, ok := r.mappers[typeName]
	if !ok {
		return nil, fmt.Errorf("no mapper registered for %s", typeName)
	}

	dm, ok := mapper.(DataMapper[T])
	if !ok {
		return nil, fmt.Errorf("mapper type mismatch for %s", typeName)
	}

	return dm, nil
}

// UnitOfWork tracks changes to entities.
type UnitOfWork struct {
	db            *sql.DB
	tx            *sql.Tx
	mappers       *MapperRegistry
	newEntities   map[string]Entity
	dirtyEntities map[string]Entity
	deletedEntities map[string]Entity
	cleanEntities map[string]Entity
	mu            sync.Mutex
}

// NewUnitOfWork creates a new unit of work.
func NewUnitOfWork(db *sql.DB, mappers *MapperRegistry) *UnitOfWork {
	return &UnitOfWork{
		db:              db,
		mappers:         mappers,
		newEntities:     make(map[string]Entity),
		dirtyEntities:   make(map[string]Entity),
		deletedEntities: make(map[string]Entity),
		cleanEntities:   make(map[string]Entity),
	}
}

// RegisterNew registers a new entity.
func (u *UnitOfWork) RegisterNew(entity Entity) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	id := entity.GetID()
	if id == "" {
		return fmt.Errorf("entity must have an ID")
	}

	if _, exists := u.deletedEntities[id]; exists {
		return fmt.Errorf("cannot register deleted entity as new")
	}

	if _, exists := u.dirtyEntities[id]; exists {
		return fmt.Errorf("entity already registered as dirty")
	}

	if _, exists := u.cleanEntities[id]; exists {
		return fmt.Errorf("entity already registered as clean")
	}

	u.newEntities[id] = entity
	return nil
}

// RegisterDirty registers a modified entity.
func (u *UnitOfWork) RegisterDirty(entity Entity) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	id := entity.GetID()
	if id == "" {
		return fmt.Errorf("entity must have an ID")
	}

	if _, exists := u.deletedEntities[id]; exists {
		return fmt.Errorf("cannot register deleted entity as dirty")
	}

	// Don't track if already new
	if _, exists := u.newEntities[id]; !exists {
		if _, exists := u.dirtyEntities[id]; !exists {
			u.dirtyEntities[id] = entity
		}
	}

	return nil
}

// RegisterClean registers a clean entity.
func (u *UnitOfWork) RegisterClean(entity Entity) {
	u.mu.Lock()
	defer u.mu.Unlock()

	id := entity.GetID()
	u.cleanEntities[id] = entity
}

// RegisterDeleted registers a deleted entity.
func (u *UnitOfWork) RegisterDeleted(entity Entity) {
	u.mu.Lock()
	defer u.mu.Unlock()

	id := entity.GetID()

	// If new, just remove from tracking
	if _, exists := u.newEntities[id]; exists {
		delete(u.newEntities, id)
		return
	}

	delete(u.dirtyEntities, id)
	delete(u.cleanEntities, id)
	u.deletedEntities[id] = entity
}

// Commit persists all changes in a transaction.
func (u *UnitOfWork) Commit(ctx context.Context) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	tx, err := u.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	u.tx = tx

	// 1. Insert new entities
	for _, entity := range u.newEntities {
		if err := u.insertEntity(ctx, entity); err != nil {
			return fmt.Errorf("insert entity: %w", err)
		}
	}

	// 2. Update dirty entities
	for _, entity := range u.dirtyEntities {
		if err := u.updateEntity(ctx, entity); err != nil {
			return fmt.Errorf("update entity: %w", err)
		}
	}

	// 3. Delete removed entities
	for _, entity := range u.deletedEntities {
		if err := u.deleteEntity(ctx, entity); err != nil {
			return fmt.Errorf("delete entity: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	u.clear()
	return nil
}

// Rollback clears all tracked changes.
func (u *UnitOfWork) Rollback() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.clear()
}

func (u *UnitOfWork) clear() {
	u.newEntities = make(map[string]Entity)
	u.dirtyEntities = make(map[string]Entity)
	u.deletedEntities = make(map[string]Entity)
	u.cleanEntities = make(map[string]Entity)
	u.tx = nil
}

func (u *UnitOfWork) insertEntity(ctx context.Context, entity Entity) error {
	// Type assertion per concrete type - simplified example
	// In production, use reflection or type registry
	return fmt.Errorf("insertEntity: implement per concrete type")
}

func (u *UnitOfWork) updateEntity(ctx context.Context, entity Entity) error {
	return fmt.Errorf("updateEntity: implement per concrete type")
}

func (u *UnitOfWork) deleteEntity(ctx context.Context, entity Entity) error {
	return fmt.Errorf("deleteEntity: implement per concrete type")
}
```

## Unit of Work with Repositories

```go
package repository

import (
	"context"
	"fmt"
)

// Order represents an order entity.
type Order struct {
	ID         string
	CustomerID string
	Items      []OrderItem
	Status     string
}

func (o *Order) GetID() string { return o.ID }

// OrderItem represents an order item.
type OrderItem struct {
	ProductID string
	Quantity  int
}

// OrderDataMapper handles order persistence.
type OrderDataMapper struct {
	db *sql.DB
}

// FindByID loads an order by ID.
func (m *OrderDataMapper) FindByID(ctx context.Context, id string) (*Order, error) {
	// Implementation omitted
	return nil, nil
}

// OrderRepository uses Unit of Work for tracking.
type OrderRepository struct {
	uow    *UnitOfWork
	mapper *OrderDataMapper
}

// NewOrderRepository creates a new order repository.
func NewOrderRepository(uow *UnitOfWork, mapper *OrderDataMapper) *OrderRepository {
	return &OrderRepository{
		uow:    uow,
		mapper: mapper,
	}
}

// FindByID finds an order and registers it as clean.
func (r *OrderRepository) FindByID(ctx context.Context, id string) (*Order, error) {
	order, err := r.mapper.FindByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("find order: %w", err)
	}

	if order != nil {
		r.uow.RegisterClean(order)
	}

	return order, nil
}

// Add registers a new order.
func (r *OrderRepository) Add(order *Order) error {
	return r.uow.RegisterNew(order)
}

// Remove registers an order for deletion.
func (r *OrderRepository) Remove(order *Order) {
	r.uow.RegisterDeleted(order)
}

// OrderService coordinates operations.
type OrderService struct {
	orderRepo   *OrderRepository
	productRepo *ProductRepository
	uow         *UnitOfWork
}

// NewOrderService creates a new order service.
func NewOrderService(
	orderRepo *OrderRepository,
	productRepo *ProductRepository,
	uow *UnitOfWork,
) *OrderService {
	return &OrderService{
		orderRepo:   orderRepo,
		productRepo: productRepo,
		uow:         uow,
	}
}

// PlaceOrder places a new order.
func (s *OrderService) PlaceOrder(ctx context.Context, customerID string, items []CartItem) (*Order, error) {
	order := &Order{
		ID:         generateID(),
		CustomerID: customerID,
		Status:     "draft",
	}

	for _, item := range items {
		product, err := s.productRepo.FindByID(ctx, item.ProductID)
		if err != nil {
			return nil, fmt.Errorf("find product: %w", err)
		}
		if product == nil {
			return nil, fmt.Errorf("product not found: %s", item.ProductID)
		}

		if err := product.ReduceStock(item.Quantity); err != nil {
			return nil, fmt.Errorf("reduce stock: %w", err)
		}
		// Product becomes dirty automatically

		order.Items = append(order.Items, OrderItem{
			ProductID: product.ID,
			Quantity:  item.Quantity,
		})
	}

	order.Status = "submitted"
	if err := s.orderRepo.Add(order); err != nil {
		return nil, fmt.Errorf("add order: %w", err)
	}

	// Single commit for all changes
	if err := s.uow.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	return order, nil
}

// CartItem represents an item in a shopping cart.
type CartItem struct {
	ProductID string
	Quantity  int
}

func generateID() string {
	return "order-" + fmt.Sprint(time.Now().UnixNano())
}
```

## Comparison with Alternatives

| Aspect | Unit of Work | Transaction Script | Active Record |
|--------|--------------|-------------------|---------------|
| Tracking | Automatic | Manual | In the object |
| Atomicity | Guaranteed | Manual | Per object |
| Performance | Batch operations | Individual | Individual |
| Complexity | High | Low | Low |

## When to Use

**Use Unit of Work when:**

- Transactions involving multiple entities
- Need for batch inserts/updates
- ORM with change tracking
- Complex optimistic locking

**Avoid Unit of Work when:**

- Simple CRUD
- Single entity per transaction
- No Domain Model

## Relationship with DDD

Unit of Work aligns with **Aggregate boundaries**.

## Frameworks and ORMs

| Framework | Unit of Work |
|-----------|--------------|
| GORM | Transaction callbacks |
| sqlx | Manual with sql.Tx |
| ent | Transaction API |

## Related Patterns

- **Identity Map**: Cache of loaded objects
- **Data Mapper**: Entity persistence
- **Repository**: Collection-like interface
- **Domain Events**: Published at commit

## Sources

- Martin Fowler, PoEAA, Chapter 11
- [Unit of Work - martinfowler.com](https://martinfowler.com/eaaCatalog/unitOfWork.html)
