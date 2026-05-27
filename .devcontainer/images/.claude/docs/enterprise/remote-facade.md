# Remote Facade

> "Provides a coarse-grained facade on fine-grained objects to improve efficiency over a network." - Martin Fowler, PoEAA

## Concept

Remote Facade is a simplified interface that exposes coarse-grained operations to reduce the number of network calls. It encapsulates multiple fine-grained calls into a single operation.

## Problem Solved

```go
// PROBLEM: Fine-grained API = many network calls
customer := api.GetCustomer(ctx, id)                    // Call 1
address := api.GetAddress(ctx, customer.AddressID)      // Call 2
orders := api.GetOrders(ctx, customer.ID)               // Call 3
// ... N more calls

// SOLUTION: Coarse-grained Remote Facade
customerProfile := api.GetCustomerProfile(ctx, id)      // 1 single call
// Contains: customer, address, recentOrders, etc.
```

## Go Implementation

```go
package facade

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// PlaceOrderRequest contains all order data in one request.
type PlaceOrderRequest struct {
	CustomerID      string
	Items           []OrderItemRequest
	ShippingAddress Address
	BillingAddress  *Address
	DiscountCode    *string
	PaymentMethod   PaymentMethod
}

// OrderItemRequest represents an order item.
type OrderItemRequest struct {
	ProductID string
	Quantity  int
}

// Address represents a shipping/billing address.
type Address struct {
	Street     string
	City       string
	PostalCode string
	Country    string
}

// PaymentMethod represents a payment method.
type PaymentMethod struct {
	Type  string
	Token string
}

// PlaceOrderResponse contains complete order information.
type PlaceOrderResponse struct {
	OrderID             string
	OrderNumber         string
	Total               float64
	Currency            string
	EstimatedDelivery   time.Time
	PaymentConfirmation string
}

// OrderFacade provides coarse-grained operations.
type OrderFacade struct {
	orderService       *OrderService
	paymentService     *PaymentService
	notificationService *NotificationService
	customerRepo       CustomerRepository
	productRepo        ProductRepository
}

// NewOrderFacade creates a new order facade.
func NewOrderFacade(
	orderService *OrderService,
	paymentService *PaymentService,
	notificationService *NotificationService,
	customerRepo CustomerRepository,
	productRepo ProductRepository,
) *OrderFacade {
	return &OrderFacade{
		orderService:       orderService,
		paymentService:     paymentService,
		notificationService: notificationService,
		customerRepo:       customerRepo,
		productRepo:        productRepo,
	}
}

// PlaceOrder places a complete order in one call.
// Replaces 5-10 fine-grained calls with single coarse-grained operation.
func (f *OrderFacade) PlaceOrder(ctx context.Context, req PlaceOrderRequest) (*PlaceOrderResponse, error) {
	// Validation
	customer, err := f.customerRepo.FindByID(ctx, req.CustomerID)
	if err != nil {
		return nil, fmt.Errorf("find customer: %w", err)
	}
	if customer == nil {
		return nil, fmt.Errorf("customer not found")
	}

	// Create order
	order, err := f.orderService.Create(ctx, req.CustomerID)
	if err != nil {
		return nil, fmt.Errorf("create order: %w", err)
	}

	// Add items
	for _, item := range req.Items {
		product, err := f.productRepo.FindByID(ctx, item.ProductID)
		if err != nil {
			return nil, fmt.Errorf("find product: %w", err)
		}
		if product == nil {
			return nil, fmt.Errorf("product not found: %s", item.ProductID)
		}

		if err := f.orderService.AddItem(ctx, order.ID, item.ProductID, item.Quantity); err != nil {
			return nil, fmt.Errorf("add item: %w", err)
		}
	}

	// Set addresses
	if err := f.orderService.SetShippingAddress(ctx, order.ID, req.ShippingAddress); err != nil {
		return nil, fmt.Errorf("set shipping address: %w", err)
	}

	billingAddr := req.BillingAddress
	if billingAddr == nil {
		billingAddr = &req.ShippingAddress
	}
	if err := f.orderService.SetBillingAddress(ctx, order.ID, *billingAddr); err != nil {
		return nil, fmt.Errorf("set billing address: %w", err)
	}

	// Apply discount if provided
	if req.DiscountCode != nil {
		if err := f.orderService.ApplyDiscount(ctx, order.ID, *req.DiscountCode); err != nil {
			// Log but don't fail
			fmt.Printf("failed to apply discount: %v\n", err)
		}
	}

	// Submit order
	if err := f.orderService.Submit(ctx, order.ID); err != nil {
		return nil, fmt.Errorf("submit order: %w", err)
	}

	// Process payment
	paymentIntent, err := f.paymentService.CreatePaymentIntent(ctx, order.ID)
	if err != nil {
		return nil, fmt.Errorf("create payment intent: %w", err)
	}

	paymentResult, err := f.paymentService.ProcessPayment(ctx, paymentIntent.ID, req.PaymentMethod)
	if err != nil {
		return nil, fmt.Errorf("process payment: %w", err)
	}
	if !paymentResult.Success {
		return nil, fmt.Errorf("payment failed: %s", paymentResult.Error)
	}

	// Send notification (async, best effort)
	go func() {
		if err := f.notificationService.SendOrderConfirmation(context.Background(), order.ID); err != nil {
			fmt.Printf("failed to send notification: %v\n", err)
		}
	}()

	// Fetch updated order
	updatedOrder, err := f.orderService.FindByID(ctx, order.ID)
	if err != nil {
		return nil, fmt.Errorf("find order: %w", err)
	}

	// Build response DTO
	return &PlaceOrderResponse{
		OrderID:             order.ID,
		OrderNumber:         updatedOrder.OrderNumber,
		Total:               updatedOrder.Total,
		Currency:            updatedOrder.Currency,
		EstimatedDelivery:   f.calculateDeliveryDate(req.ShippingAddress),
		PaymentConfirmation: paymentResult.ConfirmationNumber,
	}, nil
}

// GetCustomerProfile retrieves complete customer profile in one call.
func (f *OrderFacade) GetCustomerProfile(ctx context.Context, customerID string) (*CustomerProfileResponse, error) {
	// Parallel fetching
	type result struct {
		customer    *Customer
		addresses   []*Address
		orders      []*Order
		preferences *Preferences
		err         error
	}

	ch := make(chan result, 1)

	go func() {
		customer, err := f.customerRepo.FindByID(ctx, customerID)
		if err != nil {
			ch <- result{err: err}
			return
		}
		if customer == nil {
			ch <- result{err: fmt.Errorf("customer not found")}
			return
		}

		// Continue fetching other data...
		ch <- result{customer: customer}
	}()

	r := <-ch
	if r.err != nil {
		return nil, r.err
	}

	return &CustomerProfileResponse{
		ID:           r.customer.ID,
		Name:         r.customer.Name,
		Email:        r.customer.Email,
		MemberSince:  r.customer.CreatedAt,
		LoyaltyPoints: r.customer.LoyaltyPoints,
		Tier:         r.customer.LoyaltyTier,
	}, nil
}

func (f *OrderFacade) calculateDeliveryDate(address Address) time.Time {
	// Business logic for delivery estimation
	return time.Now().Add(5 * 24 * time.Hour)
}

// CustomerProfileResponse aggregates customer data.
type CustomerProfileResponse struct {
	ID            string
	Name          string
	Email         string
	MemberSince   time.Time
	LoyaltyPoints int
	Tier          string
}

// Service interfaces
type OrderService struct{}

func (s *OrderService) Create(ctx context.Context, customerID string) (*Order, error) {
	return nil, nil
}

func (s *OrderService) AddItem(ctx context.Context, orderID, productID string, qty int) error {
	return nil
}

func (s *OrderService) SetShippingAddress(ctx context.Context, orderID string, addr Address) error {
	return nil
}

func (s *OrderService) SetBillingAddress(ctx context.Context, orderID string, addr Address) error {
	return nil
}

func (s *OrderService) ApplyDiscount(ctx context.Context, orderID, code string) error {
	return nil
}

func (s *OrderService) Submit(ctx context.Context, orderID string) error {
	return nil
}

func (s *OrderService) FindByID(ctx context.Context, orderID string) (*Order, error) {
	return nil, nil
}

type PaymentService struct{}

type PaymentIntent struct {
	ID string
}

type PaymentResult struct {
	Success            bool
	Error              string
	ConfirmationNumber string
}

func (s *PaymentService) CreatePaymentIntent(ctx context.Context, orderID string) (*PaymentIntent, error) {
	return nil, nil
}

func (s *PaymentService) ProcessPayment(ctx context.Context, intentID string, method PaymentMethod) (*PaymentResult, error) {
	return nil, nil
}

type NotificationService struct{}

func (s *NotificationService) SendOrderConfirmation(ctx context.Context, orderID string) error {
	return nil
}
```

## Comparison with Alternatives

| Aspect | Remote Facade | Fine-grained API | BFF |
|--------|--------------|------------------|-----|
| Network calls | Few | Many | Few |
| Client coupling | Low | High | Low |
| Flexibility | Medium | High | High |
| Server complexity | High | Low | Medium |
| Performance | Better | Variable | Good |

## When to Use

**Use Remote Facade when:**

- Expensive network communication (latency)
- Remote clients (mobile, SPA, microservices)
- Complex multi-step operations
- Need to reduce bandwidth

**Avoid Remote Facade when:**

- Local clients (monolith)
- Simple operations
- Need for maximum flexibility (GraphQL)

## Related Patterns

- [DTO](./dto.md) - Transfer objects for coarse-grained operations
- [Service Layer](./service-layer.md) - Business logic called by Remote Facade
- [Gateway](./gateway.md) - Access to external systems
- [Data Transfer Object](./dto.md) - Transport of aggregated data

## Sources

- Martin Fowler, PoEAA, Chapter 15
- [Remote Facade - martinfowler.com](https://martinfowler.com/eaaCatalog/remoteFacade.html)
