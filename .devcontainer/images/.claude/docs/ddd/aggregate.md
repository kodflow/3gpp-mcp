# Aggregate Pattern

> Cluster of domain objects treated as a unit for data modifications, with a root Entity that controls access and maintains invariants.

## Definition

An **Aggregate** is a cluster of domain objects (Entities and Value Objects) treated as a single unit for data changes. It has a root Entity (Aggregate Root) that controls access and maintains invariants across the cluster.

```
Aggregate = Root Entity + Child Entities + Value Objects + Invariants + Consistency Boundary
```

**Key characteristics:**

- **Aggregate Root**: Single entry point for all modifications
- **Consistency Boundary**: Transactional consistency within aggregate
- **Invariants**: Business rules enforced across the cluster
- **Identity**: Referenced only by root's identity
- **Encapsulation**: Internal structure hidden from outside

## Go Implementation

```go
package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// AggregateRoot is a base type for aggregate roots.
type AggregateRoot[TID comparable] struct {
	Entity[TID]
	domainEvents []DomainEvent
	version      int
}

// NewAggregateRoot creates a new aggregate root.
func NewAggregateRoot[TID comparable](id TID) AggregateRoot[TID] {
	return AggregateRoot[TID]{
		Entity:       NewEntity(id),
		domainEvents: make([]DomainEvent, 0),
		version:      0,
	}
}

// Version returns the aggregate version for optimistic locking.
func (a AggregateRoot[TID]) Version() int {
	return a.version
}

// AddDomainEvent adds a domain event to be published.
func (a *AggregateRoot[TID]) AddDomainEvent(event DomainEvent) {
	a.domainEvents = append(a.domainEvents, event)
}

// PullDomainEvents retrieves and clears domain events.
func (a *AggregateRoot[TID]) PullDomainEvents() []DomainEvent {
	events := make([]DomainEvent, len(a.domainEvents))
	copy(events, a.domainEvents)
	a.domainEvents = nil
	return events
}

// IncrementVersion increments the version for optimistic locking.
func (a *AggregateRoot[TID]) IncrementVersion() {
	a.version++
}

// OrderID is a strongly-typed order identifier.
type OrderID struct {
	value string
}

// NewOrderID generates a new order ID.
func NewOrderID() OrderID {
	return OrderID{value: uuid.New().String()}
}

// OrderIDFrom creates an OrderID from a string.
func OrderIDFrom(value string) (OrderID, error) {
	if value == "" {
		return OrderID{}, errors.New("orderID cannot be empty")
	}
	return OrderID{value: value}, nil
}

// Value returns the underlying string value.
func (id OrderID) Value() string {
	return id.value
}

// Equals checks OrderID equality.
func (id OrderID) Equals(other OrderID) bool {
	return id.value == other.value
}

// OrderStatus represents the order lifecycle state.
type OrderStatus string

const (
	OrderStatusDraft     OrderStatus = "draft"
	OrderStatusConfirmed OrderStatus = "confirmed"
	OrderStatusShipped   OrderStatus = "shipped"
	OrderStatusCancelled OrderStatus = "cancelled"
)

// Order is the aggregate root for order management.
type Order struct {
	AggregateRoot[OrderID]
	customerID      CustomerID
	items           []OrderItem
	status          OrderStatus
	shippingAddress Address
	createdAt       time.Time
}

// NewOrder creates a new order in draft status.
func NewOrder(customerID CustomerID, shippingAddress Address) (*Order, error) {
	id := NewOrderID()
	order := &Order{
		AggregateRoot:   NewAggregateRoot(id),
		customerID:      customerID,
		shippingAddress: shippingAddress,
		status:          OrderStatusDraft,
		createdAt:       time.Now(),
		items:           make([]OrderItem, 0),
	}

	order.AddDomainEvent(NewOrderCreatedEvent(id, customerID))

	return order, nil
}

// AddItem adds an item to the order with invariant enforcement.
func (o *Order) AddItem(
	productID ProductID,
	quantity Quantity,
	unitPrice Money,
) error {
	// Invariant: Cannot modify confirmed orders
	if o.status != OrderStatusDraft {
		return errors.New("cannot add items to a non-draft order")
	}

	// Invariant: Maximum 10 items per order
	if len(o.items) >= 10 {
		return errors.New("order cannot have more than 10 items")
	}

	// Check if item already exists
	for i := range o.items {
		if o.items[i].ProductID().Equals(productID) {
			return o.items[i].IncreaseQuantity(quantity)
		}
	}

	// Add new item
	item, err := NewOrderItem(productID, quantity, unitPrice)
	if err != nil {
		return err
	}

	o.items = append(o.items, item)
	o.AddDomainEvent(NewOrderItemAddedEvent(o.ID(), productID, quantity))

	return nil
}

// RemoveItem removes an item from the order.
func (o *Order) RemoveItem(productID ProductID) error {
	if o.status != OrderStatusDraft {
		return errors.New("cannot remove items from a non-draft order")
	}

	for i, item := range o.items {
		if item.ProductID().Equals(productID) {
			o.items = append(o.items[:i], o.items[i+1:]...)
			o.AddDomainEvent(NewOrderItemRemovedEvent(o.ID(), productID))
			return nil
		}
	}

	return errors.New("item not found")
}

// Confirm confirms the order.
func (o *Order) Confirm() error {
	// Invariant: Order must have items
	if len(o.items) == 0 {
		return errors.New("cannot confirm empty order")
	}

	// Invariant: Must be in Draft status
	if o.status != OrderStatusDraft {
		return errors.New("order already confirmed")
	}

	o.status = OrderStatusConfirmed
	o.AddDomainEvent(NewOrderConfirmedEvent(o.ID(), o.TotalAmount()))

	return nil
}

// Cancel cancels the order with a reason.
func (o *Order) Cancel(reason string) error {
	if o.status == OrderStatusShipped {
		return errors.New("cannot cancel shipped order")
	}

	o.status = OrderStatusCancelled
	o.AddDomainEvent(NewOrderCancelledEvent(o.ID(), reason))

	return nil
}

// TotalAmount calculates the total order amount.
func (o *Order) TotalAmount() Money {
	total, _ := NewMoney(0, CurrencyUSD)

	for _, item := range o.items {
		subtotal := item.Subtotal()
		total, _ = total.Add(subtotal)
	}

	return total
}

// ItemCount returns the total quantity of items.
func (o *Order) ItemCount() int {
	count := 0
	for _, item := range o.items {
		count += item.Quantity().Value()
	}
	return count
}

// Items returns a read-only copy of items.
func (o *Order) Items() []OrderItem {
	items := make([]OrderItem, len(o.items))
	copy(items, o.items)
	return items
}

// Getters for aggregate state
func (o *Order) Status() OrderStatus            { return o.status }
func (o *Order) CustomerID() CustomerID         { return o.customerID }
func (o *Order) ShippingAddress() Address       { return o.shippingAddress }
func (o *Order) CreatedAt() time.Time           { return o.createdAt }

// OrderItemID is a strongly-typed order item identifier.
type OrderItemID struct {
	value string
}

// NewOrderItemID generates a new order item ID.
func NewOrderItemID() OrderItemID {
	return OrderItemID{value: uuid.New().String()}
}

// OrderItemIDFrom creates an OrderItemID from a string.
func OrderItemIDFrom(value string) (OrderItemID, error) {
	if value == "" {
		return OrderItemID{}, errors.New("orderItemID cannot be empty")
	}
	return OrderItemID{value: value}, nil
}

// Value returns the underlying string value.
func (id OrderItemID) Value() string {
	return id.value
}

// Equals checks OrderItemID equality.
func (id OrderItemID) Equals(other OrderItemID) bool {
	return id.value == other.value
}

// OrderItem is a child entity within the Order aggregate.
type OrderItem struct {
	Entity[OrderItemID]
	productID ProductID
	quantity  Quantity
	unitPrice Money
}

// NewOrderItem creates a new order item.
func NewOrderItem(
	productID ProductID,
	quantity Quantity,
	unitPrice Money,
) (OrderItem, error) {
	id := NewOrderItemID()
	return OrderItem{
		Entity:    NewEntity(id),
		productID: productID,
		quantity:  quantity,
		unitPrice: unitPrice,
	}, nil
}

// ReconstituteOrderItem reconstitutes an order item from persistence.
func ReconstituteOrderItem(
	id OrderItemID,
	productID ProductID,
	quantity Quantity,
	unitPrice Money,
) OrderItem {
	return OrderItem{
		Entity:    NewEntity(id),
		productID: productID,
		quantity:  quantity,
		unitPrice: unitPrice,
	}
}

// IncreaseQuantity increases the item quantity (only accessible through aggregate).
func (i *OrderItem) IncreaseQuantity(additional Quantity) error {
	newValue := i.quantity.Value() + additional.Value()
	newQuantity, err := NewQuantity(newValue)
	if err != nil {
		return err
	}
	i.quantity = newQuantity
	return nil
}

// Subtotal calculates the item subtotal.
func (i OrderItem) Subtotal() Money {
	result, _ := i.unitPrice.Multiply(float64(i.quantity.Value()))
	return result
}

// Getters
func (i OrderItem) ProductID() ProductID { return i.productID }
func (i OrderItem) Quantity() Quantity   { return i.quantity }
func (i OrderItem) UnitPrice() Money     { return i.unitPrice }
```

## Aggregate Design Rules

1. **Reference by Identity Only**: External aggregates reference only by root ID
2. **Modify One Aggregate Per Transaction**: Eventual consistency between aggregates
3. **Keep Aggregates Small**: Prefer smaller aggregates for concurrency
4. **Use Domain Events**: For cross-aggregate communication

```go
// Cross-aggregate reference - by ID only
type Order struct {
	AggregateRoot[OrderID]
	customerID CustomerID // Reference by ID, not Customer object

	// NOT this:
	// customer *Customer // BAD - crosses aggregate boundary
}

// Cross-aggregate communication via events
type OrderConfirmedHandler struct {
	inventoryService InventoryService
}

// NewOrderConfirmedHandler creates a new handler.
func NewOrderConfirmedHandler(
	inventoryService InventoryService,
) *OrderConfirmedHandler {
	return &OrderConfirmedHandler{
		inventoryService: inventoryService,
	}
}

// Handle processes the order confirmed event.
func (h *OrderConfirmedHandler) Handle(
	ctx context.Context,
	event *OrderConfirmedEvent,
) error {
	// Update another aggregate based on event
	return h.inventoryService.ReserveStock(ctx, event.OrderID, event.Items)
}
```

## Recommended Libraries

| Library | Purpose | Link |
|---------|---------|------|
| **google/uuid** | ID generation | `go get github.com/google/uuid` |
| **ThreeDotsLabs/watermill** | Event sourcing | `go get github.com/ThreeDotsLabs/watermill` |
| **nats-io/nats.go** | Event streaming | `go get github.com/nats-io/nats.go` |

## Anti-patterns

1. **God Aggregate**: Too many entities in one aggregate

   ```go
   // BAD - Too large, concurrency issues
   type Customer struct {
       AggregateRoot[CustomerID]
       orders    []*Order
       reviews   []*Review
       wishlist  []*WishlistItem
   }
   ```

2. **Anemic Aggregate**: No business logic, just data container

   ```go
   // BAD - Logic in services instead of aggregate
   type Order struct {
       Items  []OrderItem
       Status string
   }

   type OrderService struct{}

   func (s *OrderService) AddItem(order *Order, item OrderItem) {
       // Logic here instead of in aggregate
   }
   ```

3. **Cross-Aggregate Transaction**: Modifying multiple aggregates in one transaction

   ```go
   // BAD
   func (s *OrderService) ConfirmOrder(ctx context.Context, orderID OrderID) error {
       order, _ := s.orderRepo.FindByID(ctx, orderID)
       customer, _ := s.customerRepo.FindByID(ctx, order.CustomerID())

       order.Confirm()
       customer.AddLoyaltyPoints(100) // Different aggregate!

       // Single transaction - BAD
       tx, _ := s.db.Begin()
       s.orderRepo.SaveTx(tx, order)
       s.customerRepo.SaveTx(tx, customer)
       return tx.Commit()
   }
   ```

4. **Exposing Internals**: Returning mutable collections

   ```go
   // BAD
   func (o *Order) Items() []OrderItem {
       return o.items // Returns mutable slice!
   }

   // GOOD
   func (o *Order) Items() []OrderItem {
       items := make([]OrderItem, len(o.items))
       copy(items, o.items)
       return items // Returns copy
   }
   ```

## When to Use

- Group of objects that change together
- Business rules that span multiple entities
- Need for transactional consistency for a set of objects
- Complex domain with many relationships

## Related Patterns

- [Entity](./entity.md) - Aggregate root is an entity
- [Value Object](./value-object.md) - Aggregates contain value objects
- [Repository](./repository.md) - Persists aggregates
- [Domain Event](./domain-event.md) - Cross-aggregate communication
