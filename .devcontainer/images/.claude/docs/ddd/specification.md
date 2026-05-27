# Specification Pattern

> Encapsulates composable and reusable business rules, separating the matching logic from the candidate object itself.

## Definition

A **Specification** encapsulates business rules that can be combined and reused. It separates the statement of how to match a candidate from the candidate object itself, enabling composable and testable query/validation logic.

```
Specification = Business Rule + Composability + Reusability + Testability
```

**Key characteristics:**

- **Single responsibility**: One rule per specification
- **Composable**: AND, OR, NOT operations
- **Reusable**: Same spec for queries and validation
- **Testable**: Rules isolated and unit-testable
- **Domain-focused**: Named in ubiquitous language

## Go Implementation

```go
package domain

// Specification defines a business rule that can be evaluated.
type Specification[T any] interface {
	IsSatisfiedBy(candidate T) bool
	And(other Specification[T]) Specification[T]
	Or(other Specification[T]) Specification[T]
	Not() Specification[T]
}

// BaseSpecification provides common composition operations.
type BaseSpecification[T any] struct {
	isSatisfiedBy func(T) bool
}

// NewSpecification creates a new specification from a predicate.
func NewSpecification[T any](fn func(T) bool) Specification[T] {
	return &BaseSpecification[T]{isSatisfiedBy: fn}
}

// IsSatisfiedBy checks if the candidate satisfies the specification.
func (s *BaseSpecification[T]) IsSatisfiedBy(candidate T) bool {
	return s.isSatisfiedBy(candidate)
}

// And combines two specifications with logical AND.
func (s *BaseSpecification[T]) And(other Specification[T]) Specification[T] {
	return &andSpecification[T]{left: s, right: other}
}

// Or combines two specifications with logical OR.
func (s *BaseSpecification[T]) Or(other Specification[T]) Specification[T] {
	return &orSpecification[T]{left: s, right: other}
}

// Not negates the specification.
func (s *BaseSpecification[T]) Not() Specification[T] {
	return &notSpecification[T]{spec: s}
}

// andSpecification combines two specs with AND.
type andSpecification[T any] struct {
	left, right Specification[T]
}

func (s *andSpecification[T]) IsSatisfiedBy(candidate T) bool {
	return s.left.IsSatisfiedBy(candidate) && s.right.IsSatisfiedBy(candidate)
}

func (s *andSpecification[T]) And(other Specification[T]) Specification[T] {
	return &andSpecification[T]{left: s, right: other}
}

func (s *andSpecification[T]) Or(other Specification[T]) Specification[T] {
	return &orSpecification[T]{left: s, right: other}
}

func (s *andSpecification[T]) Not() Specification[T] {
	return &notSpecification[T]{spec: s}
}

// orSpecification combines two specs with OR.
type orSpecification[T any] struct {
	left, right Specification[T]
}

func (s *orSpecification[T]) IsSatisfiedBy(candidate T) bool {
	return s.left.IsSatisfiedBy(candidate) || s.right.IsSatisfiedBy(candidate)
}

func (s *orSpecification[T]) And(other Specification[T]) Specification[T] {
	return &andSpecification[T]{left: s, right: other}
}

func (s *orSpecification[T]) Or(other Specification[T]) Specification[T] {
	return &orSpecification[T]{left: s, right: other}
}

func (s *orSpecification[T]) Not() Specification[T] {
	return &notSpecification[T]{spec: s}
}

// notSpecification negates a spec.
type notSpecification[T any] struct {
	spec Specification[T]
}

func (s *notSpecification[T]) IsSatisfiedBy(candidate T) bool {
	return !s.spec.IsSatisfiedBy(candidate)
}

func (s *notSpecification[T]) And(other Specification[T]) Specification[T] {
	return &andSpecification[T]{left: s, right: other}
}

func (s *notSpecification[T]) Or(other Specification[T]) Specification[T] {
	return &orSpecification[T]{left: s, right: other}
}

func (s *notSpecification[T]) Not() Specification[T] {
	return &notSpecification[T]{spec: s}
}

// Domain Specifications - Order Examples

// OrderIsConfirmedSpec checks if order is confirmed.
type OrderIsConfirmedSpec struct {
	BaseSpecification[*Order]
}

// NewOrderIsConfirmedSpec creates a new specification.
func NewOrderIsConfirmedSpec() Specification[*Order] {
	return NewSpecification(func(order *Order) bool {
		return order.Status() == OrderStatusConfirmed
	})
}

// OrderHasMinimumValueSpec checks minimum order value.
type OrderHasMinimumValueSpec struct {
	minimumValue Money
}

// NewOrderHasMinimumValueSpec creates a new specification.
func NewOrderHasMinimumValueSpec(minimumValue Money) Specification[*Order] {
	spec := &OrderHasMinimumValueSpec{minimumValue: minimumValue}
	return NewSpecification(spec.isSatisfiedBy)
}

func (s *OrderHasMinimumValueSpec) isSatisfiedBy(order *Order) bool {
	total := order.TotalAmount()
	return total.Amount() >= s.minimumValue.Amount()
}

// OrderIsFromPremiumCustomerSpec checks customer tier.
type OrderIsFromPremiumCustomerSpec struct {
	customerRepo CustomerRepository
}

// NewOrderIsFromPremiumCustomerSpec creates a new specification.
func NewOrderIsFromPremiumCustomerSpec(
	customerRepo CustomerRepository,
) Specification[*Order] {
	spec := &OrderIsFromPremiumCustomerSpec{customerRepo: customerRepo}
	return NewSpecification(spec.isSatisfiedBy)
}

func (s *OrderIsFromPremiumCustomerSpec) isSatisfiedBy(order *Order) bool {
	customer, err := s.customerRepo.FindByID(context.Background(), order.CustomerID())
	if err != nil {
		return false
	}
	return customer.Tier() == CustomerTierPremium
}

// OrderIsEligibleForFreeShippingSpec composes multiple specs.
type OrderIsEligibleForFreeShippingSpec struct {
	customerRepo CustomerRepository
}

// NewOrderIsEligibleForFreeShippingSpec creates a new specification.
func NewOrderIsEligibleForFreeShippingSpec(
	customerRepo CustomerRepository,
) Specification[*Order] {
	hasMinValue := NewOrderHasMinimumValueSpec(NewMoney(100, CurrencyUSD))
	isPremium := NewOrderIsFromPremiumCustomerSpec(customerRepo)
	
	// Free shipping: order >= $100 OR premium customer
	return hasMinValue.Or(isPremium)
}

// Product Specifications

// ProductIsInStockSpec checks if product has stock.
func NewProductIsInStockSpec() Specification[*Product] {
	return NewSpecification(func(product *Product) bool {
		return product.StockQuantity() > 0
	})
}

// ProductIsInCategorySpec checks product category.
func NewProductIsInCategorySpec(categoryID CategoryID) Specification[*Product] {
	return NewSpecification(func(product *Product) bool {
		return product.CategoryID().Equals(categoryID)
	})
}

// ProductPriceInRangeSpec checks price range.
func NewProductPriceInRangeSpec(minPrice, maxPrice Money) Specification[*Product] {
	return NewSpecification(func(product *Product) bool {
		price := product.Price()
		return price.Amount() >= minPrice.Amount() &&
			price.Amount() <= maxPrice.Amount()
	})
}
```

## Usage Examples

```go
// In-memory filtering
func FilterOrders(orders []*Order, spec Specification[*Order]) []*Order {
	var result []*Order
	for _, order := range orders {
		if spec.IsSatisfiedBy(order) {
			result = append(result, order)
		}
	}
	return result
}

// Validation
type OrderService struct {
	freeShippingSpec Specification[*Order]
}

func (s *OrderService) CalculateShipping(order *Order) Money {
	if s.freeShippingSpec.IsSatisfiedBy(order) {
		return NewMoney(0, CurrencyUSD)
	}
	return s.calculateStandardShipping(order)
}

// Complex business rule
func GetEligibleOrders(
	orders []*Order,
	customerRepo CustomerRepository,
) []*Order {
	confirmed := NewOrderIsConfirmedSpec()
	minValue := NewOrderHasMinimumValueSpec(NewMoney(200, CurrencyUSD))
	premium := NewOrderIsFromPremiumCustomerSpec(customerRepo)
	
	// Complex rule: confirmed AND (value >= $200 AND premium customer)
	spec := confirmed.And(minValue.And(premium))
	
	return FilterOrders(orders, spec)
}
```

## Recommended Libraries

| Library | Purpose | Link |
|---------|---------|------|
| **samber/lo** | Functional helpers | `go get github.com/samber/lo` |
| **go-playground/validator** | Validation | `go get github.com/go-playground/validator/v10` |

## Anti-patterns

1. **God Specification**: Too many rules in one spec

   ```go
   // BAD
   func (s *OrderSpec) IsSatisfiedBy(order *Order) bool {
       return order.Status == "confirmed" &&
              order.Total > 0 &&
              len(order.Items) > 0
              // ... 20 more conditions
   }
   ```

2. **Leaking Implementation**: Exposing internal details

   ```go
   // BAD
   type OrderSpec struct {
       statusToCheck OrderStatus // Exposed!
   }
   ```

3. **Non-Composable**: Specifications that can't be combined

   ```go
   // BAD - No composition support
   type OrderSpec struct{}
   func (s *OrderSpec) Check(order *Order) bool { return true }
   ```

4. **Side Effects**: Modifying state in specification

   ```go
   // BAD
   func (s *Spec) IsSatisfiedBy(order *Order) bool {
       order.MarkAsChecked() // Side effect!
       return order.IsValid
   }
   ```

## When to Use

- Complex business rules requiring composition
- Rules reused for validation and queries
- Domain logic that must be testable in isolation
- Filtering collections by business criteria

## Related Patterns

- [Repository](./repository.md) - Uses specifications for queries
- [Value Object](./value-object.md) - Rules often involve value objects
- [Domain Service](./domain-service.md) - Uses specifications for decisions
