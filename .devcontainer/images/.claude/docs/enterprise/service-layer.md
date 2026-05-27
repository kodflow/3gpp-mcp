# Service Layer

> "Defines an application's boundary with a layer of services that establishes a set of available operations and coordinates the application's response in each operation." - Martin Fowler, PoEAA

## Concept

The Service Layer is a coordination layer that defines the application's boundary. It orchestrates business operations without containing business logic itself (that remains in the Domain Model).

## Responsibilities

1. **Coordination**: Orchestrate calls between domain and infrastructure
2. **Transaction**: Manage transactional boundaries
3. **Security**: Apply authorization rules
4. **DTO Conversion**: Transform between domain and presentation
5. **Facade**: Expose a simplified API

## Go Implementation

```go
package service

import (
	"context"
	"fmt"
	"time"
)

// PlaceOrderRequest is the input DTO.
type PlaceOrderRequest struct {
	CustomerID        string
	Items             []OrderItemRequest
	ShippingAddressID string
}

// OrderItemRequest represents an order item request.
type OrderItemRequest struct {
	ProductID string
	Quantity  int
}

// PlaceOrderResponse is the output DTO.
type PlaceOrderResponse struct {
	OrderID           string
	Total             float64
	EstimatedDelivery time.Time
}

// OrderApplicationService coordinates order operations.
type OrderApplicationService struct {
	orderRepo        OrderRepository
	customerRepo     CustomerRepository
	productRepo      ProductRepository
	inventoryService InventoryService
	paymentService   PaymentService
	notificationSvc  NotificationService
	eventPublisher   DomainEventPublisher
}

// NewOrderApplicationService creates a new order service.
func NewOrderApplicationService(
	orderRepo OrderRepository,
	customerRepo CustomerRepository,
	productRepo ProductRepository,
	inventoryService InventoryService,
	paymentService PaymentService,
	notificationSvc NotificationService,
	eventPublisher DomainEventPublisher,
) *OrderApplicationService {
	return &OrderApplicationService{
		orderRepo:        orderRepo,
		customerRepo:     customerRepo,
		productRepo:      productRepo,
		inventoryService: inventoryService,
		paymentService:   paymentService,
		notificationSvc:  notificationSvc,
		eventPublisher:   eventPublisher,
	}
}

// PlaceOrder places an order - coordination without business logic.
func (s *OrderApplicationService) PlaceOrder(
	ctx context.Context,
	request PlaceOrderRequest,
	currentUser *User,
) (*PlaceOrderResponse, error) {
	// 1. Load domain entities
	customer, err := s.customerRepo.FindByID(ctx, request.CustomerID)
	if err != nil {
		return nil, fmt.Errorf("find customer: %w", err)
	}
	if customer == nil {
		return nil, ErrNotFound("customer not found")
	}

	address := customer.GetAddress(request.ShippingAddressID)
	if address == nil {
		return nil, ErrNotFound("address not found")
	}

	// 2. Create Order aggregate via factory
	order := NewOrder(customer.ID)
	order.SetShippingAddress(address)

	// 3. Add items (business logic in Order)
	for _, item := range request.Items {
		product, err := s.productRepo.FindByID(ctx, item.ProductID)
		if err != nil {
			return nil, fmt.Errorf("find product %s: %w", item.ProductID, err)
		}
		if product == nil {
			return nil, ErrNotFound(fmt.Sprintf("product %s not found", item.ProductID))
		}

		// Validation in Order aggregate
		if err := order.AddItem(product, item.Quantity); err != nil {
			return nil, fmt.Errorf("add item: %w", err)
		}
	}

	// 4. Submit order (business logic in Order)
	if err := order.Submit(); err != nil {
		return nil, fmt.Errorf("submit order: %w", err)
	}

	// 5. Coordinate with infrastructure services
	if err := s.inventoryService.Reserve(ctx, order.Items()); err != nil {
		return nil, fmt.Errorf("reserve inventory: %w", err)
	}

	// 6. Persist
	if err := s.orderRepo.Save(ctx, order); err != nil {
		// Compensate on failure
		_ = s.inventoryService.Release(ctx, order.Items())
		return nil, fmt.Errorf("save order: %w", err)
	}

	// 7. Publish domain events
	events := order.PullEvents()
	if err := s.eventPublisher.PublishAll(ctx, events); err != nil {
		// Log error but don't fail
		fmt.Printf("failed to publish events: %v\n", err)
	}

	// 8. Notifications (side effect)
	if err := s.notificationSvc.NotifyOrderPlaced(ctx, order, customer); err != nil {
		// Log error but don't fail
		fmt.Printf("failed to send notification: %v\n", err)
	}

	// 9. Return DTO response
	return &PlaceOrderResponse{
		OrderID:           order.ID,
		Total:             order.Total(),
		EstimatedDelivery: s.calculateDeliveryDate(address),
	}, nil
}

// CancelOrder cancels an order.
func (s *OrderApplicationService) CancelOrder(
	ctx context.Context,
	orderID string,
	reason string,
	currentUser *User,
) error {
	order, err := s.orderRepo.FindByID(ctx, orderID)
	if err != nil {
		return fmt.Errorf("find order: %w", err)
	}
	if order == nil {
		return ErrNotFound("order not found")
	}

	// Verify permissions
	if !s.canCancelOrder(order, currentUser) {
		return ErrForbidden("cannot cancel this order")
	}

	// Business logic in domain
	if err := order.Cancel(reason); err != nil {
		return fmt.Errorf("cancel order: %w", err)
	}

	// Coordination compensation
	if order.IsPaid() {
		if err := s.paymentService.Refund(ctx, order.PaymentID()); err != nil {
			return fmt.Errorf("refund payment: %w", err)
		}
	}

	if err := s.inventoryService.Release(ctx, order.Items()); err != nil {
		return fmt.Errorf("release inventory: %w", err)
	}

	// Persist
	if err := s.orderRepo.Save(ctx, order); err != nil {
		return fmt.Errorf("save order: %w", err)
	}

	// Events
	events := order.PullEvents()
	if err := s.eventPublisher.PublishAll(ctx, events); err != nil {
		fmt.Printf("failed to publish events: %v\n", err)
	}

	return nil
}

// GetCustomerOrders retrieves customer orders.
func (s *OrderApplicationService) GetCustomerOrders(
	ctx context.Context,
	customerID string,
	pagination PaginationParams,
) (*PaginatedResult[*OrderSummaryDTO], error) {
	orders, err := s.orderRepo.FindByCustomerID(ctx, customerID, pagination)
	if err != nil {
		return nil, fmt.Errorf("find orders: %w", err)
	}

	dtos := make([]*OrderSummaryDTO, len(orders.Items))
	for i, order := range orders.Items {
		dtos[i] = OrderSummaryDTOFromDomain(order)
	}

	return &PaginatedResult[*OrderSummaryDTO]{
		Items:    dtos,
		Total:    orders.Total,
		Page:     pagination.Page,
		PageSize: pagination.PageSize,
	}, nil
}

func (s *OrderApplicationService) canCancelOrder(order *Order, user *User) bool {
	return order.CustomerID() == user.ID || user.HasRole("admin")
}

func (s *OrderApplicationService) calculateDeliveryDate(address *Address) time.Time {
	return time.Now().Add(5 * 24 * time.Hour)
}

// PaginationParams represents pagination parameters.
type PaginationParams struct {
	Page     int
	PageSize int
}

// PaginatedResult represents a paginated result.
type PaginatedResult[T any] struct {
	Items    []T
	Total    int
	Page     int
	PageSize int
}

// Domain interfaces
type OrderRepository interface {
	FindByID(ctx context.Context, id string) (*Order, error)
	FindByCustomerID(ctx context.Context, customerID string, pagination PaginationParams) (*PaginatedResult[*Order], error)
	Save(ctx context.Context, order *Order) error
}

type CustomerRepository interface {
	FindByID(ctx context.Context, id string) (*Customer, error)
}

type ProductRepository interface {
	FindByID(ctx context.Context, id string) (*Product, error)
}

type InventoryService interface {
	Reserve(ctx context.Context, items []*OrderItem) error
	Release(ctx context.Context, items []*OrderItem) error
}

type PaymentService interface {
	Refund(ctx context.Context, paymentID string) error
}

type NotificationService interface {
	NotifyOrderPlaced(ctx context.Context, order *Order, customer *Customer) error
}

type DomainEventPublisher interface {
	PublishAll(ctx context.Context, events []DomainEvent) error
}

// Helper types
type User struct {
	ID string
}

func (u *User) HasRole(role string) bool {
	return false // Implement
}

type DomainEvent interface {
	EventType() string
}

type OrderSummaryDTO struct {
	ID     string
	Status string
	Total  float64
}

func OrderSummaryDTOFromDomain(order *Order) *OrderSummaryDTO {
	return &OrderSummaryDTO{
		ID:     order.ID,
		Status: order.Status(),
		Total:  order.Total(),
	}
}

func ErrNotFound(msg string) error {
	return fmt.Errorf("not found: %s", msg)
}

func ErrForbidden(msg string) error {
	return fmt.Errorf("forbidden: %s", msg)
}
```

## Service Layer vs Domain Service

```go
// Application Service (Service Layer)
// - Coordination, transactions, security
// - NO business logic
type OrderApplicationService struct {
	// Coordinates but does not decide
}

func (s *OrderApplicationService) PlaceOrder(ctx context.Context, req PlaceOrderRequest) error {
	order := NewOrder(req.CustomerID)
	order.AddItem(product, qty) // Delegates to domain
	return s.orderRepo.Save(ctx, order)
}

// Domain Service
// - Business logic that doesn't belong in an entity
// - Cross-aggregate operations
type PricingDomainService struct{}

func (s *PricingDomainService) CalculateDiscount(order *Order, customer *Customer) float64 {
	// Pure business logic
	if customer.IsVIP() && order.Total() > 1000 {
		return order.Total() * 0.15
	}
	return 0
}
```

## Comparison with Alternatives

| Aspect | Service Layer | Transaction Script | Facade |
|--------|--------------|-------------------|--------|
| Business logic | In Domain Model | In the script | In the Facade |
| Coordination | Yes | Yes | No |
| Transactions | Yes | Yes | No |
| Granularity | Use cases | Operations | Simplification |

## When to Use

**Use Service Layer when:**

- Application with Domain Model
- Need for transactional coordination
- Multiple clients (web, API, CLI)
- Security at use case level
- Important integration tests

**Avoid Service Layer when:**

- Simple CRUD (use Transaction Script)
- Single user interface
- No Domain Model

## Related Patterns

- [Domain Model](./domain-model.md) - Contains the business logic orchestrated by Service Layer
- [Repository](./repository.md) - Access to aggregates from Service Layer
- [Unit of Work](./unit-of-work.md) - Transactional management in Service Layer
- [DTO](./dto.md) - Transfer objects for inputs/outputs

## Sources

- Martin Fowler, PoEAA, Chapter 9
- [Service Layer - martinfowler.com](https://martinfowler.com/eaaCatalog/serviceLayer.html)
- Eric Evans, DDD - Application Layer
