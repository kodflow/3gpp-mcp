# Bounded Context Pattern

> Semantic boundary within which a domain model is defined and applicable, representing a linguistic boundary with unambiguous terminology.

## Definition

A **Bounded Context** is a semantic boundary within which a domain model is defined and applicable. It represents a linguistic boundary where terms have specific, unambiguous meanings, and models are internally consistent.

```
Bounded Context = Model Boundary + Ubiquitous Language + Team Ownership + Integration Points
```

**Key characteristics:**

- **Linguistic boundary**: Same term can mean different things in different contexts
- **Model consistency**: One model per context, no ambiguity
- **Team alignment**: Often maps to team ownership
- **Explicit boundaries**: Clear interfaces between contexts
- **Autonomous**: Can evolve independently

## Context Map Relationships

```
┌─────────────────────────────────────────────────────────────────┐
│                        CONTEXT MAP                               │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  ┌──────────────┐    Shared Kernel    ┌──────────────┐          │
│  │   Orders     │◄──────────────────►│   Shipping   │          │
│  │   Context    │                     │   Context    │          │
│  └──────┬───────┘                     └──────────────┘          │
│         │                                                        │
│         │ Customer/Supplier                                      │
│         │                                                        │
│         ▼                                                        │
│  ┌──────────────┐                     ┌──────────────┐          │
│  │   Billing    │                     │   Catalog    │          │
│  │   Context    │    Conformist       │   Context    │          │
│  └──────┬───────┘◄────────────────────┴──────────────┘          │
│         │                                                        │
│         │ Anti-Corruption Layer                                  │
│         ▼                                                        │
│  ┌──────────────┐                                                │
│  │   External   │                                                │
│  │   Payment    │                                                │
│  └──────────────┘                                                │
└─────────────────────────────────────────────────────────────────┘
```

## Go Implementation

### Context Definition

```go
// Each context has its own domain model
// orders/domain/order.go
package orders

// Order in Orders context - full order management
type Order struct {
	id              OrderID
	customerID      CustomerID
	items           []OrderItem
	status          OrderStatus
	shippingAddress Address
}

// NewOrder creates a new order.
func NewOrder(
	customerID CustomerID,
	shippingAddress Address,
) (*Order, error) {
	return &Order{
		id:              NewOrderID(),
		customerID:      customerID,
		status:          OrderStatusDraft,
		shippingAddress: shippingAddress,
		items:           make([]OrderItem, 0),
	}, nil
}

// Confirm confirms the order.
func (o *Order) Confirm() error {
	if o.status != OrderStatusDraft {
		return errors.New("order cannot be confirmed")
	}
	o.status = OrderStatusConfirmed
	return nil
}

// Customer in Orders context - minimal, order-focused
type Customer struct {
	id              CustomerID
	name            string
	shippingAddress Address
}

// billing/domain/customer.go
package billing

// Customer in Billing context - billing-focused
type Customer struct {
	id             CustomerID
	billingAddress Address
	paymentMethods []PaymentMethod
	creditLimit    Money
}

// NewCustomer creates a new customer.
func NewCustomer(
	id CustomerID,
	billingAddress Address,
	creditLimit Money,
) (*Customer, error) {
	return &Customer{
		id:             id,
		billingAddress: billingAddress,
		creditLimit:    creditLimit,
		paymentMethods: make([]PaymentMethod, 0),
	}, nil
}

// Charge charges the customer.
func (c *Customer) Charge(amount Money) (*Payment, error) {
	if amount.Amount() > c.creditLimit.Amount() {
		return nil, errors.New("exceeds credit limit")
	}
	// Charge logic
	return nil, nil
}

// AddPaymentMethod adds a payment method.
func (c *Customer) AddPaymentMethod(method PaymentMethod) error {
	c.paymentMethods = append(c.paymentMethods, method)
	return nil
}

// OrderReference in Billing context - just for invoicing
type OrderReference struct {
	OrderID     string
	TotalAmount Money
	OrderDate   time.Time
}
```

### Anti-Corruption Layer (ACL)

```go
// Protect your domain from external/legacy systems
// billing/infrastructure/payment_gateway_acl.go
package infrastructure

import (
	"context"
	"encoding/json"
	"fmt"
)

// ExternalPaymentResponse is the external payment gateway response.
type ExternalPaymentResponse struct {
	TransactionID string `json:"transaction_id"`
	StatusCode    int    `json:"status_code"`
	AmountCents   int    `json:"amount_cents"`
	CurrencyISO   string `json:"currency_iso"`
	ErrorMsg      string `json:"error_msg,omitempty"`
}

// PaymentResult is our domain model.
type PaymentResult struct {
	TransactionID TransactionID
	Status        PaymentStatus
	Amount        Money
	Error         error
}

// PaymentGatewayACL translates between external and domain models.
type PaymentGatewayACL struct {
	gateway ExternalPaymentGateway
}

// NewPaymentGatewayACL creates a new ACL.
func NewPaymentGatewayACL(gateway ExternalPaymentGateway) *PaymentGatewayACL {
	return &PaymentGatewayACL{gateway: gateway}
}

// ProcessPayment processes a payment through the external gateway.
func (acl *PaymentGatewayACL) ProcessPayment(
	ctx context.Context,
	amount Money,
	method PaymentMethod,
) (*PaymentResult, error) {
	// Translate to external format
	request := acl.toExternalRequest(amount, method)

	// Call external service
	response, err := acl.gateway.Charge(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("gateway communication failed: %w", err)
	}

	// Translate back to our domain model
	return acl.toDomainResult(response)
}

func (acl *PaymentGatewayACL) toExternalRequest(
	amount Money,
	method PaymentMethod,
) ExternalPaymentRequest {
	return ExternalPaymentRequest{
		AmountCents: int(amount.Amount() * 100),
		CurrencyISO: amount.Currency().Code(),
		PaymentToken: method.Token(),
	}
}

func (acl *PaymentGatewayACL) toDomainResult(
	response *ExternalPaymentResponse,
) (*PaymentResult, error) {
	if response.StatusCode != 200 {
		return &PaymentResult{
			Status: PaymentStatusFailed,
			Error:  errors.New(response.ErrorMsg),
		}, nil
	}

	transactionID, err := NewTransactionID(response.TransactionID)
	if err != nil {
		return nil, err
	}

	amount, err := NewMoney(
		float64(response.AmountCents)/100,
		CurrencyFromCode(response.CurrencyISO),
	)
	if err != nil {
		return nil, err
	}

	return &PaymentResult{
		TransactionID: transactionID,
		Status:        PaymentStatusCompleted,
		Amount:        amount,
	}, nil
}
```

### Context Integration via Events

```go
// Shared integration events (in shared kernel or separate package)
// integration/events/order_confirmed.go
package events

import "time"

// OrderConfirmedIntegrationEvent is published across contexts.
type OrderConfirmedIntegrationEvent struct {
	EventID    string                 `json:"event_id"`
	OccurredAt time.Time              `json:"occurred_at"`
	OrderID    string                 `json:"order_id"`
	CustomerID string                 `json:"customer_id"`
	TotalAmount MoneyDTO               `json:"total_amount"`
	Items      []OrderItemDTO         `json:"items"`
}

// MoneyDTO is a data transfer object for money.
type MoneyDTO struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

// OrderItemDTO is a data transfer object for order items.
type OrderItemDTO struct {
	ProductID string `json:"product_id"`
	Quantity  int    `json:"quantity"`
}

// Orders Context - publishes event
// orders/infrastructure/event_publisher.go
package infrastructure

type OrdersEventPublisher struct {
	messageBus MessageBus
}

// NewOrdersEventPublisher creates a new event publisher.
func NewOrdersEventPublisher(messageBus MessageBus) *OrdersEventPublisher {
	return &OrdersEventPublisher{messageBus: messageBus}
}

// PublishOrderConfirmed publishes an order confirmed event.
func (p *OrdersEventPublisher) PublishOrderConfirmed(
	ctx context.Context,
	event *OrderConfirmedEvent,
) error {
	// Translate domain event to integration event
	integrationEvent := &OrderConfirmedIntegrationEvent{
		EventID:    event.EventID(),
		OccurredAt: event.OccurredAt(),
		OrderID:    event.OrderID.Value(),
		CustomerID: event.CustomerID.Value(),
		TotalAmount: MoneyDTO{
			Amount:   event.TotalAmount.Amount(),
			Currency: event.TotalAmount.Currency().Code(),
		},
		Items: make([]OrderItemDTO, len(event.Items)),
	}

	for i, item := range event.Items {
		integrationEvent.Items[i] = OrderItemDTO{
			ProductID: item.ProductID,
			Quantity:  item.Quantity,
		}
	}

	return p.messageBus.Publish(ctx, "orders.order-confirmed", integrationEvent)
}

// Billing Context - consumes event
// billing/infrastructure/event_handler.go
package infrastructure

type BillingEventHandler struct {
	invoiceService     InvoiceService
	customerRepository CustomerRepository
}

// NewBillingEventHandler creates a new event handler.
func NewBillingEventHandler(
	invoiceService InvoiceService,
	customerRepository CustomerRepository,
) *BillingEventHandler {
	return &BillingEventHandler{
		invoiceService:     invoiceService,
		customerRepository: customerRepository,
	}
}

// HandleOrderConfirmed handles order confirmed events.
func (h *BillingEventHandler) HandleOrderConfirmed(
	ctx context.Context,
	event *OrderConfirmedIntegrationEvent,
) error {
	// Translate integration event to billing domain
	customerID, err := NewCustomerID(event.CustomerID)
	if err != nil {
		return err
	}

	customer, err := h.customerRepository.FindByID(ctx, customerID)
	if err != nil {
		return err
	}

	amount, err := NewMoney(
		event.TotalAmount.Amount,
		CurrencyFromCode(event.TotalAmount.Currency),
	)
	if err != nil {
		return err
	}

	orderRef := OrderReference{
		OrderID:     event.OrderID,
		TotalAmount: amount,
		OrderDate:   event.OccurredAt,
	}

	// Use billing domain logic
	return h.invoiceService.CreateInvoice(ctx, customer, orderRef)
}
```

### Shared Kernel

```go
// shared/domain/money.go
// Shared between contexts that need identical money handling
package domain

import (
	"errors"
	"math"
)

// Money is a shared value object.
type Money struct {
	amount   float64
	currency Currency
}

// NewMoney creates a new Money value object.
func NewMoney(amount float64, currency Currency) (Money, error) {
	rounded := math.Round(amount*100) / 100
	return Money{
		amount:   rounded,
		currency: currency,
	}, nil
}

// Amount returns the monetary amount.
func (m Money) Amount() float64 {
	return m.amount
}

// Currency returns the currency.
func (m Money) Currency() Currency {
	return m.currency
}

// Add adds two money amounts.
func (m Money) Add(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, errors.New("cannot add different currencies")
	}
	return NewMoney(m.amount+other.amount, m.currency)
}

// shared/domain/address.go
type Address struct {
	Street     string
	City       string
	PostalCode string
	Country    Country
}

// NewAddress creates a new address.
func NewAddress(street, city, postalCode string, country Country) Address {
	return Address{
		Street:     street,
		City:       city,
		PostalCode: postalCode,
		Country:    country,
	}
}
```

## Context Mapping Patterns

| Pattern | Description | Use When |
|---------|-------------|----------|
| **Shared Kernel** | Shared code between contexts | Close collaboration needed |
| **Customer/Supplier** | Upstream serves downstream | Clear dependency direction |
| **Conformist** | Downstream adopts upstream model | No negotiation power |
| **Anti-Corruption Layer** | Translation layer | Protecting from external/legacy |
| **Open Host Service** | Published API for multiple consumers | Many downstream contexts |
| **Published Language** | Shared schema/protocol | Standard integration format |
| **Separate Ways** | No integration | Contexts truly independent |

## Module Structure

```
src/
├── orders/
│   ├── domain/
│   │   ├── order.go
│   │   ├── order_item.go
│   │   └── customer.go          # Orders' view of Customer
│   ├── application/
│   │   └── order_service.go
│   ├── infrastructure/
│   │   ├── repository.go
│   │   └── event_publisher.go
│   └── api/
│       └── handler.go
│
├── billing/
│   ├── domain/
│   │   ├── invoice.go
│   │   ├── customer.go          # Billing's view of Customer
│   │   └── payment.go
│   ├── application/
│   │   └── invoice_service.go
│   ├── infrastructure/
│   │   ├── payment_gateway_acl.go  # Anti-Corruption Layer
│   │   └── event_handler.go
│   └── api/
│       └── handler.go
│
├── shared/
│   ├── domain/
│   │   ├── money.go
│   │   └── address.go
│   └── infrastructure/
│       └── message_bus.go
│
└── integration/
    └── events/
        ├── order_confirmed.go
        └── payment_received.go
```

## Recommended Libraries

| Library | Purpose | Link |
|---------|---------|------|
| **go-kit/kit** | Microservices toolkit | `go get github.com/go-kit/kit` |
| **nats-io/nats.go** | Event messaging | `go get github.com/nats-io/nats.go` |
| **segmentio/kafka-go** | Event streaming | `go get github.com/segmentio/kafka-go` |
| **grpc/grpc-go** | RPC communication | `go get google.golang.org/grpc` |

## Anti-patterns

1. **Shared Database**: Multiple contexts writing to same tables

   ```
   // BAD - Tight coupling via database
   Orders Context ──┐
                    ├──► customers table
   Billing Context ─┘
   ```

2. **Model Bleeding**: Using another context's internal model

   ```go
   // BAD - Billing using Orders' internal model
   import "myapp/orders/domain"

   func (s *BillingService) Process(order *domain.Order) {}
   ```

3. **Big Ball of Mud**: No clear boundaries

   ```go
   // BAD - Everything in one package
   type OrderBillingShippingService struct { }
   ```

4. **Sync Integration**: Direct synchronous calls between contexts

   ```go
   // BAD - Tight coupling
   func (s *OrderService) Confirm(order *Order) error {
       return s.billingService.CreateInvoice(order) // Direct call
   }
   ```

## When to Use

- Large domains with distinct subdomains
- Multiple teams working on the same system
- Different parts of the system evolve at different rates
- Need for integration with external systems
- Terms have different meanings in different parts of the business

## Related Patterns

- [Aggregate](./aggregate.md) - Lives within bounded context
- [Domain Event](./domain-event.md) - Cross-context communication
- [Repository](./repository.md) - Context-specific persistence
