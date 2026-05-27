# Domain Service Pattern

> Encapsulates domain logic that doesn't naturally fit within an Entity or Value Object, representing operations or business rules involving multiple objects.

## Definition

A **Domain Service** encapsulates domain logic that doesn't naturally fit within an Entity or Value Object. It represents operations or business rules that involve multiple domain objects or require external coordination.

```
Domain Service = Stateless + Domain Logic + Cross-Entity Operations
```

**Key characteristics:**

- **Stateless**: No internal state, operates on domain objects
- **Domain-focused**: Contains business logic, not infrastructure
- **Operation-centric**: Named after domain actions (verbs)
- **Cross-aggregate**: Coordinates between multiple aggregates
- **Interface-driven**: Often defined as interfaces for DI

## Go Implementation

```go
package domain

import (
	"context"
	"errors"
	"fmt"
)

// TransferService handles money transfers between accounts.
type TransferService interface {
	Transfer(ctx context.Context, from, to *Account, amount Money) (*Transfer, error)
}

// MoneyTransferService implements TransferService.
type MoneyTransferService struct {
	exchangeRateService  ExchangeRateService
	transferPolicyService TransferPolicyService
}

// NewMoneyTransferService creates a new transfer service.
func NewMoneyTransferService(
	exchangeRateService ExchangeRateService,
	transferPolicyService TransferPolicyService,
) *MoneyTransferService {
	return &MoneyTransferService{
		exchangeRateService:  exchangeRateService,
		transferPolicyService: transferPolicyService,
	}
}

// Transfer executes a money transfer with domain validation.
func (s *MoneyTransferService) Transfer(
	ctx context.Context,
	from, to *Account,
	amount Money,
) (*Transfer, error) {
	// Validate transfer policy
	if err := s.transferPolicyService.Validate(ctx, from, to, amount); err != nil {
		return nil, fmt.Errorf("policy validation failed: %w", err)
	}
	
	// Handle currency conversion if needed
	transferAmount := amount
	if from.Currency() != to.Currency() {
		converted, err := s.exchangeRateService.Convert(ctx, amount, to.Currency())
		if err != nil {
			return nil, fmt.Errorf("currency conversion failed: %w", err)
		}
		transferAmount = converted
	}
	
	// Perform the transfer (domain logic)
	if err := from.Debit(amount); err != nil {
		return nil, fmt.Errorf("debit failed: %w", err)
	}
	
	if err := to.Credit(transferAmount); err != nil {
		// Rollback debit
		_ = from.Credit(amount)
		return nil, fmt.Errorf("credit failed: %w", err)
	}
	
	// Create transfer record
	return NewTransfer(from.ID(), to.ID(), amount, transferAmount)
}

// PricingService calculates order totals and discounts.
type PricingService interface {
	CalculateTotal(ctx context.Context, order *Order, customer *Customer) (Money, error)
	ApplyDiscount(ctx context.Context, order *Order, code DiscountCode) (Money, error)
}

// OrderPricingService implements PricingService.
type OrderPricingService struct {
	discountRepo DiscountRepository
	taxService   TaxService
}

// NewOrderPricingService creates a new pricing service.
func NewOrderPricingService(
	discountRepo DiscountRepository,
	taxService TaxService,
) *OrderPricingService {
	return &OrderPricingService{
		discountRepo: discountRepo,
		taxService:   taxService,
	}
}

// CalculateTotal computes the total order price with discounts and tax.
func (s *OrderPricingService) CalculateTotal(
	ctx context.Context,
	order *Order,
	customer *Customer,
) (Money, error) {
	total := order.Subtotal()
	
	// Apply customer tier discount
	tierDiscount := s.calculateTierDiscount(customer.Tier(), total)
	total, err := total.Subtract(tierDiscount)
	if err != nil {
		return Money{}, err
	}
	
	// Apply bulk discount
	if order.ItemCount() >= 10 {
		bulkDiscount, err := total.Multiply(0.05)
		if err != nil {
			return Money{}, err
		}
		total, err = total.Subtract(bulkDiscount)
		if err != nil {
			return Money{}, err
		}
	}
	
	// Calculate tax
	tax, err := s.taxService.Calculate(ctx, total, customer.Address())
	if err != nil {
		return Money{}, fmt.Errorf("tax calculation failed: %w", err)
	}
	
	total, err = total.Add(tax)
	if err != nil {
		return Money{}, err
	}
	
	return total, nil
}

// ApplyDiscount validates and applies a discount code.
func (s *OrderPricingService) ApplyDiscount(
	ctx context.Context,
	order *Order,
	code DiscountCode,
) (Money, error) {
	discount, err := s.discountRepo.FindByCode(ctx, code)
	if err != nil {
		return Money{}, errors.New("invalid discount code")
	}
	
	if discount.IsExpired() {
		return Money{}, errors.New("discount code expired")
	}
	
	if !discount.IsApplicableTo(order) {
		return Money{}, errors.New("discount not applicable to order")
	}
	
	discountAmount := discount.Calculate(order.Subtotal())
	return order.Subtotal().Subtract(discountAmount)
}

func (s *OrderPricingService) calculateTierDiscount(
	tier CustomerTier,
	amount Money,
) Money {
	rates := map[CustomerTier]float64{
		CustomerTierBronze:   0,
		CustomerTierSilver:   0.05,
		CustomerTierGold:     0.10,
		CustomerTierPlatinum: 0.15,
	}
	
	rate := rates[tier]
	discount, _ := amount.Multiply(rate)
	return discount
}

// InventoryAllocationService allocates inventory to orders.
type InventoryAllocationService interface {
	Allocate(ctx context.Context, order *Order, inventory *Inventory) ([]*Allocation, error)
	Deallocate(ctx context.Context, allocation *Allocation) error
}

// FIFOInventoryAllocationService uses First-In-First-Out strategy.
type FIFOInventoryAllocationService struct{}

// NewFIFOInventoryAllocationService creates a new allocation service.
func NewFIFOInventoryAllocationService() *FIFOInventoryAllocationService {
	return &FIFOInventoryAllocationService{}
}

// Allocate assigns inventory batches to order items using FIFO.
func (s *FIFOInventoryAllocationService) Allocate(
	ctx context.Context,
	order *Order,
	inventory *Inventory,
) ([]*Allocation, error) {
	var allocations []*Allocation
	
	for _, item := range order.Items() {
		batches := inventory.GetBatchesForProduct(item.ProductID())
		remainingQty := item.Quantity().Value()
		
		// FIFO allocation strategy
		for _, batch := range batches {
			if remainingQty <= 0 {
				break
			}
			
			allocateQty := min(remainingQty, batch.AvailableQuantity())
			
			quantity, err := NewQuantity(allocateQty)
			if err != nil {
				// Rollback previous allocations
				s.rollbackAllocations(ctx, allocations)
				return nil, err
			}
			
			allocation, err := NewAllocation(
				order.ID(),
				batch.ID(),
				item.ProductID(),
				quantity,
			)
			if err != nil {
				s.rollbackAllocations(ctx, allocations)
				return nil, err
			}
			
			if err := batch.Reserve(allocateQty); err != nil {
				s.rollbackAllocations(ctx, allocations)
				return nil, err
			}
			
			allocations = append(allocations, allocation)
			remainingQty -= allocateQty
		}
		
		if remainingQty > 0 {
			s.rollbackAllocations(ctx, allocations)
			return nil, fmt.Errorf("insufficient stock for %s", item.ProductID().Value())
		}
	}
	
	return allocations, nil
}

// Deallocate releases an allocation.
func (s *FIFOInventoryAllocationService) Deallocate(
	ctx context.Context,
	allocation *Allocation,
) error {
	return allocation.Batch().Release(allocation.Quantity().Value())
}

func (s *FIFOInventoryAllocationService) rollbackAllocations(
	ctx context.Context,
	allocations []*Allocation,
) {
	for _, a := range allocations {
		_ = s.Deallocate(ctx, a)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
```

## OOP vs FP Comparison

```go
// FP-style Domain Service using pure functions

// Transfer executes a pure transfer operation.
func Transfer(
	exchangeRate func(Money, Currency) (Money, error),
	validatePolicy func(*Account, *Account, Money) error,
) func(*Account, *Account, Money) (*Transfer, error) {
	return func(from, to *Account, amount Money) (*Transfer, error) {
		// Validate policy
		if err := validatePolicy(from, to, amount); err != nil {
			return nil, err
		}
		
		// Convert currency if needed
		transferAmount := amount
		if from.Currency() != to.Currency() {
			converted, err := exchangeRate(amount, to.Currency())
			if err != nil {
				return nil, err
			}
			transferAmount = converted
		}
		
		// Debit and credit
		if err := from.Debit(amount); err != nil {
			return nil, err
		}
		
		if err := to.Credit(transferAmount); err != nil {
			_ = from.Credit(amount) // Rollback
			return nil, err
		}
		
		return NewTransfer(from.ID(), to.ID(), amount, transferAmount)
	}
}
```

## Domain Service vs Application Service

| Aspect | Domain Service | Application Service |
|--------|---------------|---------------------|
| Layer | Domain | Application |
| Focus | Business rules | Use case orchestration |
| Dependencies | Domain objects only | Repositories, external services |
| Stateless | Yes | Yes |
| Example | `PricingService` | `OrderApplicationService` |

```go
// Application Service (uses Domain Service)
type OrderApplicationService struct {
	orderRepo    OrderRepository
	customerRepo CustomerRepository
	pricingService PricingService // Domain Service
	eventBus     EventBus
}

// Checkout orchestrates the checkout use case.
func (s *OrderApplicationService) Checkout(
	ctx context.Context,
	orderID OrderID,
) error {
	order, err := s.orderRepo.FindByID(ctx, orderID)
	if err != nil {
		return fmt.Errorf("finding order: %w", err)
	}
	
	customer, err := s.customerRepo.FindByID(ctx, order.CustomerID())
	if err != nil {
		return fmt.Errorf("finding customer: %w", err)
	}
	
	// Delegate to domain service
	total, err := s.pricingService.CalculateTotal(ctx, order, customer)
	if err != nil {
		return fmt.Errorf("calculating total: %w", err)
	}
	
	if err := order.SetTotal(total); err != nil {
		return err
	}
	
	if err := order.Confirm(); err != nil {
		return err
	}
	
	return s.orderRepo.Save(ctx, order)
}
```

## Recommended Libraries

| Library | Purpose | Link |
|---------|---------|------|
| **uber-go/fx** | Dependency injection | `go get go.uber.org/fx` |
| **google/wire** | Compile-time DI | `go get github.com/google/wire` |

## Anti-patterns

1. **Stateful Service**: Maintaining internal state

   ```go
   // BAD
   type PricingService struct {
       cachedRates map[string]float64 // State!
   }
   ```

2. **Anemic Service**: Just delegates to entities

   ```go
   // BAD - No actual domain logic
   func (s *OrderService) AddItem(order *Order, item *Item) {
       order.AddItem(item) // Just delegation
   }
   ```

3. **Infrastructure in Domain Service**: Database or API calls

   ```go
   // BAD - Infrastructure concern
   func (s *PricingService) Calculate(order *Order) (Money, error) {
       resp, err := http.Get("/api/rates") // Infrastructure!
       // ...
   }
   ```

4. **God Service**: Too many responsibilities

   ```go
   // BAD - Too broad
   type OrderDomainService struct{}
   func (s *OrderDomainService) CalculatePrice() {}
   func (s *OrderDomainService) ValidateInventory() {}
   func (s *OrderDomainService) ProcessPayment() {}
   func (s *OrderDomainService) SendNotification() {}
   ```

## When to Use

- Logic involves multiple aggregates
- The operation doesn't belong to any single entity
- Complex calculations or transformations
- Business rules that span multiple domain objects

## Related Patterns

- [Entity](./entity.md) - Primary domain logic holder
- [Aggregate](./aggregate.md) - Coordinates entities
- [Domain Event](./domain-event.md) - Published by services
