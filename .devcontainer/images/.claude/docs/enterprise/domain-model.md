# Domain Model

> "An object model of the domain that incorporates both behavior and data." - Martin Fowler, PoEAA

## Concept

The Domain Model is an object model that represents business concepts with their behaviors and rules. Unlike the Anemic Domain Model (anti-pattern), a Rich Domain Model encapsulates business logic directly in the entities.

## Rich vs Anemic Domain Model

```go
// ANTI-PATTERN: Anemic Domain Model
// Entity without behavior = simple data structure
type AnemicOrder struct {
	ID     string
	Items  []*OrderItem
	Status string
	Total  float64
}

// Business logic external in a service
type OrderService struct{}

func (s *OrderService) AddItem(order *AnemicOrder, product *Product, qty int) {
	order.Items = append(order.Items, &OrderItem{Product: product, Qty: qty})
	order.Total = s.recalculate(order)
}

// CORRECT: Rich Domain Model
// Entity with behavior and invariants
type Order struct {
	items  []*OrderItem
	status OrderStatus
}

func (o *Order) AddItem(product *Product, quantity int) error {
	if err := o.ensureDraft(); err != nil {
		return err
	}
	if err := o.ensureValidQuantity(quantity); err != nil {
		return err
	}

	existing := o.findItem(product.ID)
	if existing != nil {
		existing.IncreaseQuantity(quantity)
	} else {
		o.items = append(o.items, NewOrderItem(product, quantity))
	}

	return nil
}

func (o *Order) ensureDraft() error {
	if o.status != OrderStatusDraft {
		return fmt.Errorf("cannot modify non-draft order")
	}
	return nil
}
```

## Complete Go Implementation

```go
package domain

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Money is a value object (immutable, compared by value).
type Money struct {
	amount   int64  // In cents
	currency string
}

// NewMoney creates a new money value.
func NewMoney(amount int64, currency string) (*Money, error) {
	if amount < 0 {
		return nil, fmt.Errorf("amount cannot be negative")
	}
	return &Money{amount: amount, currency: currency}, nil
}

// Zero returns zero money.
func Zero(currency string) *Money {
	return &Money{amount: 0, currency: currency}
}

// Amount returns the amount.
func (m *Money) Amount() int64 { return m.amount }

// Currency returns the currency.
func (m *Money) Currency() string { return m.currency }

// Add adds two money values.
func (m *Money) Add(other *Money) (*Money, error) {
	if err := m.ensureSameCurrency(other); err != nil {
		return nil, err
	}
	return &Money{amount: m.amount + other.amount, currency: m.currency}, nil
}

// Multiply multiplies money by a factor.
func (m *Money) Multiply(factor int) *Money {
	return &Money{amount: m.amount * int64(factor), currency: m.currency}
}

// Equals checks if two money values are equal.
func (m *Money) Equals(other *Money) bool {
	return m.amount == other.amount && m.currency == other.currency
}

func (m *Money) ensureSameCurrency(other *Money) error {
	if m.currency != other.currency {
		return fmt.Errorf("currency mismatch: %s vs %s", m.currency, other.currency)
	}
	return nil
}

// OrderItem is an entity (unique identity, mutable).
type OrderItem struct {
	id          string
	productID   string
	productName string
	quantity    int
	unitPrice   *Money
}

// NewOrderItem creates a new order item.
func NewOrderItem(product *Product, quantity int) *OrderItem {
	return &OrderItem{
		id:          uuid.New().String(),
		productID:   product.ID,
		productName: product.Name,
		quantity:    quantity,
		unitPrice:   product.Price,
	}
}

// ID returns the item ID.
func (i *OrderItem) ID() string { return i.id }

// Quantity returns the quantity.
func (i *OrderItem) Quantity() int { return i.quantity }

// Subtotal calculates the item subtotal.
func (i *OrderItem) Subtotal() *Money {
	return i.unitPrice.Multiply(i.quantity)
}

// IncreaseQuantity increases the item quantity.
func (i *OrderItem) IncreaseQuantity(amount int) error {
	if amount <= 0 {
		return fmt.Errorf("amount must be positive")
	}
	i.quantity += amount
	return nil
}

// DecreaseQuantity decreases the item quantity.
func (i *OrderItem) DecreaseQuantity(amount int) error {
	if amount <= 0 {
		return fmt.Errorf("amount must be positive")
	}
	if amount > i.quantity {
		return fmt.Errorf("cannot decrease below zero")
	}
	i.quantity -= amount
	return nil
}

// OrderStatus represents order status.
type OrderStatus string

const (
	OrderStatusDraft     OrderStatus = "draft"
	OrderStatusSubmitted OrderStatus = "submitted"
	OrderStatusPaid      OrderStatus = "paid"
	OrderStatusShipped   OrderStatus = "shipped"
	OrderStatusDelivered OrderStatus = "delivered"
	OrderStatusCancelled OrderStatus = "cancelled"
)

// DomainEvent represents a domain event.
type DomainEvent interface {
	EventType() string
	OccurredAt() time.Time
}

// OrderCreated event.
type OrderCreated struct {
	OrderID    string
	CustomerID string
	occurredAt time.Time
}

func (e OrderCreated) EventType() string      { return "OrderCreated" }
func (e OrderCreated) OccurredAt() time.Time  { return e.occurredAt }

// ItemAddedToOrder event.
type ItemAddedToOrder struct {
	OrderID    string
	ProductID  string
	Quantity   int
	occurredAt time.Time
}

func (e ItemAddedToOrder) EventType() string     { return "ItemAddedToOrder" }
func (e ItemAddedToOrder) OccurredAt() time.Time { return e.occurredAt }

// OrderSubmitted event.
type OrderSubmitted struct {
	OrderID    string
	Total      *Money
	occurredAt time.Time
}

func (e OrderSubmitted) EventType() string     { return "OrderSubmitted" }
func (e OrderSubmitted) OccurredAt() time.Time { return e.occurredAt }

// Order is an aggregate root (entry point, protects invariants).
type Order struct {
	id         string
	customerID string
	items      []*OrderItem
	status     OrderStatus
	createdAt  time.Time
	events     []DomainEvent
}

// NewOrder creates a new order.
func NewOrder(customerID string) *Order {
	order := &Order{
		id:         uuid.New().String(),
		customerID: customerID,
		items:      make([]*OrderItem, 0),
		status:     OrderStatusDraft,
		createdAt:  time.Now(),
		events:     make([]DomainEvent, 0),
	}

	order.events = append(order.events, OrderCreated{
		OrderID:    order.id,
		CustomerID: customerID,
		occurredAt: time.Now(),
	})

	return order
}

// ID returns the order ID.
func (o *Order) ID() string { return o.id }

// Status returns the order status.
func (o *Order) Status() OrderStatus { return o.status }

// IsDraft checks if the order is in draft status.
func (o *Order) IsDraft() bool { return o.status == OrderStatusDraft }

// AddItem adds an item to the order.
func (o *Order) AddItem(product *Product, quantity int) error {
	if err := o.ensureDraft(); err != nil {
		return err
	}
	if quantity <= 0 {
		return fmt.Errorf("quantity must be positive")
	}
	if !product.IsAvailable {
		return fmt.Errorf("product %s is not available", product.Name)
	}

	// Check if item already exists
	for _, item := range o.items {
		if item.productID == product.ID {
			if err := item.IncreaseQuantity(quantity); err != nil {
				return err
			}
			return nil
		}
	}

	// Add new item
	o.items = append(o.items, NewOrderItem(product, quantity))

	o.events = append(o.events, ItemAddedToOrder{
		OrderID:    o.id,
		ProductID:  product.ID,
		Quantity:   quantity,
		occurredAt: time.Now(),
	})

	return nil
}

// RemoveItem removes an item from the order.
func (o *Order) RemoveItem(productID string) error {
	if err := o.ensureDraft(); err != nil {
		return err
	}

	for i, item := range o.items {
		if item.productID == productID {
			o.items = append(o.items[:i], o.items[i+1:]...)
			return nil
		}
	}

	return fmt.Errorf("item not found")
}

// Submit submits the order.
func (o *Order) Submit() error {
	if err := o.ensureDraft(); err != nil {
		return err
	}
	if len(o.items) == 0 {
		return fmt.Errorf("cannot submit empty order")
	}

	o.status = OrderStatusSubmitted

	o.events = append(o.events, OrderSubmitted{
		OrderID:    o.id,
		Total:      o.Total(),
		occurredAt: time.Now(),
	})

	return nil
}

// Total calculates the order total.
func (o *Order) Total() *Money {
	total := Zero("USD")
	for _, item := range o.items {
		subtotal := item.Subtotal()
		total, _ = total.Add(subtotal)
	}
	return total
}

// ItemCount returns the total number of items.
func (o *Order) ItemCount() int {
	count := 0
	for _, item := range o.items {
		count += item.Quantity()
	}
	return count
}

// PullEvents returns and clears domain events.
func (o *Order) PullEvents() []DomainEvent {
	events := o.events
	o.events = make([]DomainEvent, 0)
	return events
}

func (o *Order) ensureDraft() error {
	if !o.IsDraft() {
		return fmt.Errorf("order is not in draft status")
	}
	return nil
}

// Product represents a product.
type Product struct {
	ID          string
	Name        string
	Price       *Money
	IsAvailable bool
}
```

## Comparison with Alternatives

| Aspect | Domain Model | Transaction Script | Active Record |
|--------|--------------|-------------------|---------------|
| Encapsulation | Strong | None | Partial |
| Testability | Excellent | Medium | Medium |
| Initial complexity | High | Low | Low |
| Evolution | Easy | Difficult | Medium |
| Persistence | Separated | In the script | In the object |

## When to Use

**Use Domain Model when:**

- Complex business logic with multiple rules
- Invariants to strictly protect
- Domain rich in behaviors
- Frequent rule changes
- Team experienced in OOP/DDD
- Important unit tests

**Avoid Domain Model when:**

- Simple CRUD without logic
- Rapid prototype
- Junior team without DDD training
- Stable and simple domain

## Related Patterns

- [Service Layer](./service-layer.md) - Orchestration of operations on Domain Model
- [Repository](./repository.md) - Persistence of Domain Model aggregates
- [Data Mapper](./data-mapper.md) - Mapping between Domain Model and database
- [Unit of Work](./unit-of-work.md) - Transactional management of Domain Model

## Sources

- Martin Fowler, PoEAA, Chapter 9
- Eric Evans, Domain-Driven Design (Blue Book)
- Vaughn Vernon, Implementing Domain-Driven Design
- [Domain Model - martinfowler.com](https://martinfowler.com/eaaCatalog/domainModel.html)
