# Modular Monolith

> A monolith structured into independent modules with clear boundaries.

**Position:** Between classic Monolith and Microservices

## Principle

```
┌─────────────────────────────────────────────────────────────────┐
│                     MODULAR MONOLITH                             │
│                                                                  │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │                    SHARED KERNEL                         │    │
│  │              (Common types, Utilities)                   │    │
│  └─────────────────────────────────────────────────────────┘    │
│                              │                                   │
│       ┌──────────────────────┼──────────────────────┐           │
│       │                      │                      │           │
│  ┌────┴────┐           ┌─────┴────┐           ┌─────┴────┐     │
│  │  ORDER  │           │   USER   │           │ INVENTORY│     │
│  │ MODULE  │           │  MODULE  │           │  MODULE  │     │
│  │         │           │          │           │          │     │
│  │┌───────┐│   API     │┌────────┐│   API     │┌────────┐│     │
│  ││Domain ││◄─────────►││ Domain ││◄─────────►││ Domain ││     │
│  ││       ││           ││        ││           ││        ││     │
│  │├───────┤│           │├────────┤│           │├────────┤│     │
│  ││  App  ││           ││  App   ││           ││  App   ││     │
│  │├───────┤│           │├────────┤│           │├────────┤│     │
│  ││Infra  ││           ││ Infra  ││           ││ Infra  ││     │
│  │└───────┘│           │└────────┘│           │└────────┘│     │
│  │    │    │           │    │     │           │    │     │     │
│  │    ▼    │           │    ▼     │           │    ▼     │     │
│  │┌───────┐│           │┌────────┐│           │┌────────┐│     │
│  ││Order  ││           ││ User   ││           ││Inventory│     │
│  ││Schema ││           ││ Schema ││           ││ Schema ││     │
│  │└───────┘│           │└────────┘│           │└────────┘│     │
│  └─────────┘           └──────────┘           └──────────┘     │
│                                                                  │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │                    SHARED DATABASE                       │    │
│  └─────────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────┘
```

## File Structure

```
src/
├── modules/
│   ├── orders/                    # Orders module
│   │   ├── domain/
│   │   │   ├── Order.go
│   │   │   ├── OrderItem.go
│   │   │   └── OrderService.go
│   │   ├── application/
│   │   │   ├── CreateOrderUseCase.go
│   │   │   └── GetOrderUseCase.go
│   │   ├── infrastructure/
│   │   │   ├── OrderRepository.go
│   │   │   └── OrderController.go
│   │   ├── api/                   # Module public API
│   │   │   ├── OrderModuleAPI.go  # Interface
│   │   │   └── OrderModuleImpl.go # Implementation
│   │   └── module.go              # Public export
│   │
│   ├── users/                     # Users module
│   │   ├── domain/
│   │   ├── application/
│   │   ├── infrastructure/
│   │   ├── api/
│   │   │   └── UserModuleAPI.go
│   │   └── module.go
│   │
│   └── inventory/                 # Inventory module
│       └── ...
│
├── shared/                        # Shared Kernel
│   ├── types/
│   ├── errors/
│   └── utils/
│
└── main.go                        # Composition root
```

## Inter-module Communication

### Via Public API

```go
package api

import "context"

// CreateOrderRequest is the request to create an order.
type CreateOrderRequest struct {
	UserID string
	Items  []OrderItem
}

// OrderResponse is the response for order queries.
type OrderResponse struct {
	ID        string
	UserID    string
	Items     []OrderItem
	Total     float64
	CreatedAt time.Time
}

// OrderModuleAPI is the public API for the order module.
type OrderModuleAPI interface {
	CreateOrder(ctx context.Context, req CreateOrderRequest) (*OrderResponse, error)
	GetOrder(ctx context.Context, orderID string) (*OrderResponse, error)
	CancelOrder(ctx context.Context, orderID string) error
}

// OrderModuleImpl implements OrderModuleAPI.
type OrderModuleImpl struct {
	createOrderUseCase CreateOrderUseCase
	getOrderUseCase    GetOrderUseCase
	cancelOrderUseCase CancelOrderUseCase
}

// NewOrderModule creates a new order module.
func NewOrderModule(
	createOrderUseCase CreateOrderUseCase,
	getOrderUseCase GetOrderUseCase,
	cancelOrderUseCase CancelOrderUseCase,
) OrderModuleAPI {
	return &OrderModuleImpl{
		createOrderUseCase: createOrderUseCase,
		getOrderUseCase:    getOrderUseCase,
		cancelOrderUseCase: cancelOrderUseCase,
	}
}

// CreateOrder creates a new order.
func (m *OrderModuleImpl) CreateOrder(ctx context.Context, req CreateOrderRequest) (*OrderResponse, error) {
	order, err := m.createOrderUseCase.Execute(ctx, req)
	if err != nil {
		return nil, err
	}

	return &OrderResponse{
		ID:        order.ID,
		UserID:    order.UserID,
		Items:     order.Items,
		Total:     order.Total,
		CreatedAt: order.CreatedAt,
	}, nil
}

// GetOrder retrieves an order by ID.
func (m *OrderModuleImpl) GetOrder(ctx context.Context, orderID string) (*OrderResponse, error) {
	return m.getOrderUseCase.Execute(ctx, orderID)
}

// Usage from another module
type ShippingService struct {
	orderModule OrderModuleAPI
}

func (s *ShippingService) ShipOrder(ctx context.Context, orderID string) error {
	order, err := s.orderModule.GetOrder(ctx, orderID)
	if err != nil {
		return err
	}

	// Process shipping
	return nil
}
```

### Via Internal Events

```go
package events

import "context"

// DomainEvent represents a domain event.
type DomainEvent struct {
	Type    string
	Payload interface{}
}

// EventHandler handles domain events.
type EventHandler func(context.Context, DomainEvent) error

// InternalEventBus is the internal event bus for module communication.
type InternalEventBus interface {
	Publish(ctx context.Context, event DomainEvent) error
	Subscribe(eventType string, handler EventHandler)
}

// OrderService publishes events.
type OrderService struct {
	eventBus InternalEventBus
	orderRepo OrderRepository
}

// CreateOrder creates an order and publishes an event.
func (s *OrderService) CreateOrder(ctx context.Context, req CreateOrderRequest) (*Order, error) {
	order := &Order{
		ID:     GenerateID(),
		UserID: req.UserID,
		Items:  req.Items,
	}

	if err := s.orderRepo.Save(ctx, order); err != nil {
		return nil, err
	}

	// Publish internal event
	event := DomainEvent{
		Type: "order.created",
		Payload: map[string]interface{}{
			"orderID":    order.ID,
			"customerID": order.UserID,
		},
	}

	if err := s.eventBus.Publish(ctx, event); err != nil {
		return nil, err
	}

	return order, nil
}

// OrderCreatedHandler handles order created events in inventory module.
type OrderCreatedHandler struct {
	inventoryService InventoryService
}

// NewOrderCreatedHandler creates a new handler and subscribes to events.
func NewOrderCreatedHandler(eventBus InternalEventBus, inventoryService InventoryService) *OrderCreatedHandler {
	handler := &OrderCreatedHandler{inventoryService: inventoryService}
	eventBus.Subscribe("order.created", handler.Handle)
	return handler
}

// Handle handles the order created event.
func (h *OrderCreatedHandler) Handle(ctx context.Context, event DomainEvent) error {
	payload := event.Payload.(map[string]interface{})
	items := payload["items"].([]OrderItem)

	// Reserve inventory
	return h.inventoryService.Reserve(ctx, items)
}
```

## Isolation Rules

```go
package validation

// Module boundary rules (enforced by linters like arch-go)
const (
	// A module can only import:
	// - Its own packages
	// - The shared kernel
	// - Public APIs from other modules (modules/*/api)

	// FORBIDDEN:
	// - Direct import of domain/ from other modules
	// - Direct import of infrastructure/ from other modules
)

// Example violations:

// FORBIDDEN: Direct access to internal infrastructure
// import "app/modules/users/infrastructure"

// ALLOWED: Use public API
// import "app/modules/users/api"
```

## Composition Root

```go
package main

import (
	"database/sql"
	"log"
	"net/http"
)

func main() {
	// Infrastructure
	db, err := sql.Open("postgres", config.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	eventBus := NewInMemoryEventBus()

	// Modules
	userModule := users.NewModule(db, eventBus)
	inventoryModule := inventory.NewModule(db, eventBus)
	orderModule := orders.NewModule(
		db,
		eventBus,
		userModule.API(),      // Inject API
		inventoryModule.API(), // Inject API
	)

	// HTTP Server
	mux := http.NewServeMux()
	mux.Handle("/api/users/", userModule.Router())
	mux.Handle("/api/orders/", orderModule.Router())
	mux.Handle("/api/inventory/", inventoryModule.Router())

	log.Fatal(http.ListenAndServe(":8080", mux))
}
```

## When to Use

| Use | Avoid |
|----------|--------|
| Team 5-30 devs | Team < 5 (overkill) |
| Complex domain | Simple CRUD |
| Not ready for microservices | Mature DevOps |
| Preparing future split | Already microservices |
| Important tests | Quick prototype |

## Advantages

- **Operational simplicity**: Single deployment
- **Clear structure**: Explicit boundaries
- **Easy refactoring**: Everything in one repo
- **Integrated tests**: Simple end-to-end tests
- **Microservices preparation**: Easy future extraction
- **Transactions**: ACID possible

## Disadvantages

- **Discipline required**: Respect boundaries
- **Uniform scaling**: No per-module scale
- **All-or-nothing deploy**: One module = redeploy everything
- **Technical coupling**: Same stack
- **Shared base**: Schema can become coupled

## Real-world Examples

| Company | Usage |
|------------|-------|
| **Shopify** | Modular monolith Ruby |
| **Basecamp** | Majestic Monolith |
| **GitHub** | Modular Rails |
| **GitLab** | Modular Ruby monolith |
| **Stripe** | Ruby modules |

## Migration Path

### From Classic Monolith

```
Phase 1: Identify bounded contexts (DDD)
Phase 2: Extract modules with interfaces
Phase 3: Add isolation rules (linting)
Phase 4: Migrate data to separate schemas
Phase 5: Implement internal event bus
```

### To Microservices

```
Phase 1: Each module has its own DB schema
Phase 2: Replace sync calls with async (events)
Phase 3: Containerize modules independently
Phase 4: Extract into separate services
Phase 5: Add API Gateway
```

## Comparison

```
┌───────────────────────────────────────────────────────────────┐
│                                                                │
│  Monolith         Modular Monolith      Microservices         │
│                                                                │
│  ┌─────────┐     ┌─────────────────┐   ┌───┐ ┌───┐ ┌───┐    │
│  │         │     │ ┌───┐ ┌───┐     │   │ A │ │ B │ │ C │    │
│  │   ALL   │     │ │ A │ │ B │     │   └───┘ └───┘ └───┘    │
│  │   IN    │     │ └───┘ └───┘     │     │     │     │      │
│  │   ONE   │     │ ┌───┐           │     └─────┼─────┘      │
│  │         │     │ │ C │           │           │            │
│  └─────────┘     │ └───┘           │      [Network]         │
│                  └─────────────────┘                         │
│                                                                │
│  No boundaries   Clear boundaries     Separate processes     │
│  1 deploy        1 deploy            N deploys               │
│  1 DB            1 DB (schemas)       N DBs                   │
└───────────────────────────────────────────────────────────────┘
```

## Related Patterns

| Pattern | Relationship |
|---------|----------|
| Hexagonal | Architecture of each module |
| DDD | Bounded Contexts = Modules |
| Microservices | Possible evolution |
| Event-Driven | Inter-module communication |

## Sources

- [Kamil Grzybek - Modular Monolith](https://www.kamilgrzybek.com/design/modular-monolith-primer/)
- [Simon Brown - Modular Monoliths](https://www.youtube.com/watch?v=5OjqD-ow8GE)
- [Shopify Engineering](https://shopify.engineering/deconstructing-monolith-designing-software-maximizes-developer-productivity)
- [DHH - The Majestic Monolith](https://m.signalvnoise.com/the-majestic-monolith/)
