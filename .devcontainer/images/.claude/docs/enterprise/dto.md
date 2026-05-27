# Data Transfer Object (DTO)

> "An object that carries data between processes in order to reduce the number of method calls." - Martin Fowler, PoEAA

## Concept

The DTO is a simple object that transports data between layers or processes. It has no business logic, only data and optionally serialization methods.

## Objectives

1. **Reduce calls**: Aggregate data into a single object
2. **Decouple**: Separate the domain model from the API
3. **Serialization**: Format suited for transfer (JSON, XML)
4. **Security**: Do not expose internal details
5. **Grouping**: Allow multiple DTOs in a single file

## Tag Convention

**Required format:** `dto:"<direction>,<context>,<security>"`

The `dto:` tag allows:

- Exempting structs from KTN-STRUCT-ONEFILE (grouping)
- Exempting structs from KTN-STRUCT-CTOR (no constructor required)
- Documenting the flow and data sensitivity

### Values

| Position | Values | Description |
|----------|--------|-------------|
| direction | `in`, `out`, `inout` | Flow direction |
| context | `api`, `cmd`, `query`, `event`, `msg`, `priv` | DTO type |
| security | `pub`, `priv`, `pii`, `secret` | Classification |

### Security Classification

| Value | Description | Logging | Marshaling |
|-------|-------------|---------|------------|
| `pub` | Public data | Displayed | Included |
| `priv` | Internal (IDs, timestamps) | Displayed | Included |
| `pii` | GDPR (email, name, address) | Masked | Conditional |
| `secret` | Credentials (password, token) | REDACTED | Omitted |

**Reference:** [conventions/dto-tags.md](../conventions/dto-tags.md)

## Go Implementation

```go
package dto

import (
	"time"
)

// File: order_dto.go
// MULTIPLE DTOs grouped thanks to dto:"..." tag

// CreateOrderRequest is an input DTO for order creation.
type CreateOrderRequest struct {
    CustomerID      string             `dto:"in,api,priv" json:"customerId" validate:"required,uuid"`
    Items           []OrderItemRequest `dto:"in,api,pub" json:"items" validate:"required,min=1,dive"`
    ShippingAddress AddressRequest     `dto:"in,api,pii" json:"shippingAddress" validate:"required"`
    Notes           string             `dto:"in,api,pub" json:"notes,omitempty" validate:"max=500"`
}

// OrderItemRequest represents an order item in request.
type OrderItemRequest struct {
    ProductID string `dto:"in,api,pub" json:"productId" validate:"required,uuid"`
    Quantity  int    `dto:"in,api,pub" json:"quantity" validate:"required,min=1,max=100"`
}

// AddressRequest represents an address in request.
type AddressRequest struct {
    Street     string `dto:"in,api,pii" json:"street" validate:"required,max=200"`
    City       string `dto:"in,api,pii" json:"city" validate:"required,max=100"`
    PostalCode string `dto:"in,api,pii" json:"postalCode" validate:"required"`
    Country    string `dto:"in,api,pub" json:"country" validate:"required,iso3166_1_alpha2"`
}

// OrderResponse is an output DTO for order details.
type OrderResponse struct {
    ID                string              `dto:"out,api,pub" json:"id"`
    Status            string              `dto:"out,api,pub" json:"status"`
    CustomerName      string              `dto:"out,api,pii" json:"customerName"`
    Items             []OrderItemResponse `dto:"out,api,pub" json:"items"`
    Subtotal          float64             `dto:"out,api,pub" json:"subtotal"`
    Tax               float64             `dto:"out,api,pub" json:"tax"`
    Total             float64             `dto:"out,api,pub" json:"total"`
    CreatedAt         time.Time           `dto:"out,api,pub" json:"createdAt"`
    EstimatedDelivery time.Time           `dto:"out,api,pub" json:"estimatedDelivery"`
}

// OrderItemResponse represents an order item in response.
type OrderItemResponse struct {
    ProductID   string  `dto:"out,api,pub" json:"productId"`
    ProductName string  `dto:"out,api,pub" json:"productName"`
    Quantity    int     `dto:"out,api,pub" json:"quantity"`
    UnitPrice   float64 `dto:"out,api,pub" json:"unitPrice"`
    Subtotal    float64 `dto:"out,api,pub" json:"subtotal"`
}

// OrderSummaryDTO is a lightweight DTO for listings.
type OrderSummaryDTO struct {
    ID        string    `dto:"out,api,pub" json:"id"`
    Status    string    `dto:"out,api,pub" json:"status"`
    Total     float64   `dto:"out,api,pub" json:"total"`
    ItemCount int       `dto:"out,api,pub" json:"itemCount"`
    CreatedAt time.Time `dto:"out,api,pub" json:"createdAt"`
}

// ListOrdersQuery represents query parameters for listing orders.
type ListOrdersQuery struct {
    Status   string `dto:"in,query,pub" json:"status,omitempty"`
    FromDate string `dto:"in,query,pub" json:"fromDate,omitempty"`
    ToDate   string `dto:"in,query,pub" json:"toDate,omitempty"`
    PageSize int    `dto:"in,query,pub" json:"pageSize,omitempty"`
    Page     int    `dto:"in,query,pub" json:"page,omitempty"`
}

// Defaults sets default values for the query.
func (q *ListOrdersQuery) Defaults() {
    if q.PageSize == 0 {
        q.PageSize = 20
    }
    if q.Page == 0 {
        q.Page = 1
    }
}

// PaginatedResponse represents a paginated result.
type PaginatedResponse[T any] struct {
    Items    []T `dto:"out,api,pub" json:"items"`
    Total    int `dto:"out,api,pub" json:"total"`
    Page     int `dto:"out,api,pub" json:"page"`
    PageSize int `dto:"out,api,pub" json:"pageSize"`
}
```

## Assembler Pattern

```go
// OrderAssembler converts between domain and DTO.
type OrderAssembler struct{}

// NewOrderAssembler creates a new assembler.
func NewOrderAssembler() *OrderAssembler {
    return &OrderAssembler{}
}

// ToDTO converts domain Order to OrderResponse.
func (a *OrderAssembler) ToDTO(order *Order, customer *Customer) *OrderResponse {
    items := make([]OrderItemResponse, len(order.Items))
    for i, item := range order.Items {
        items[i] = a.itemToDTO(item)
    }

    return &OrderResponse{
        ID:                order.ID,
        Status:            order.Status,
        CustomerName:      customer.Name,
        Items:             items,
        Subtotal:          order.Subtotal,
        Tax:               order.Tax,
        Total:             order.Total,
        CreatedAt:         order.CreatedAt,
        EstimatedDelivery: order.EstimatedDelivery,
    }
}

func (a *OrderAssembler) itemToDTO(item *OrderItem) OrderItemResponse {
    return OrderItemResponse{
        ProductID:   item.ProductID,
        ProductName: item.ProductName,
        Quantity:    item.Quantity,
        UnitPrice:   item.UnitPrice,
        Subtotal:    item.Subtotal,
    }
}

// ToDomain converts CreateOrderRequest to domain parameters.
func (a *OrderAssembler) ToDomain(dto *CreateOrderRequest) *OrderCreationParams {
    items := make([]OrderItemParams, len(dto.Items))
    for i, item := range dto.Items {
        items[i] = OrderItemParams{
            ProductID: item.ProductID,
            Quantity:  item.Quantity,
        }
    }

    return &OrderCreationParams{
        CustomerID: dto.CustomerID,
        Items:      items,
        ShippingAddress: Address{
            Street:     dto.ShippingAddress.Street,
            City:       dto.ShippingAddress.City,
            PostalCode: dto.ShippingAddress.PostalCode,
            Country:    dto.ShippingAddress.Country,
        },
        Notes: dto.Notes,
    }
}
```

## DTOs vs Domain Objects

```go
// Domain Object - Business logic, invariants (NO tags)
type DomainOrder struct {
    status OrderStatus
    items  []*OrderItem
}

func (o *DomainOrder) Submit() error {
    if len(o.items) == 0 {
        return fmt.Errorf("cannot submit empty order")
    }
    o.status = OrderStatusSubmitted
    return nil
}

func (o *DomainOrder) Total() float64 {
    var total float64
    for _, item := range o.items {
        total += item.Subtotal()
    }
    return total
}

// DTO - No logic, just data (WITH dto:"..." tags)
type OrderDTO struct {
    ID     string         `dto:"out,api,pub" json:"id"`
    Status string         `dto:"out,api,pub" json:"status"`
    Items  []OrderItemDTO `dto:"out,api,pub" json:"items"`
    Total  float64        `dto:"out,api,pub" json:"total"`
    // No business methods!
}
```

## Decision Guide

```text
DIRECTION:
  - User input -> in
  - Output to client -> out
  - Update/Patch -> inout

CONTEXT:
  - REST/GraphQL API -> api
  - CQRS Command -> cmd
  - CQRS Query -> query
  - Event sourcing -> event
  - Message queue -> msg
  - Internal -> priv

SECURITY:
  - Product name, status -> pub
  - IDs, timestamps -> priv
  - Email, name, address -> pii
  - Password, token, key -> secret
```

## Comparison with Alternatives

| Aspect | DTO | Domain Object | Map/Record |
|--------|-----|---------------|------------|
| Type safety | Strong | Strong | Weak |
| Serialization | Easy | Complex | Native |
| Validation | Explicit | Invariants | Manual |
| Logic | None | Rich | None |
| Versioning | Easy | Difficult | Easy |
| Grouping | Yes (dto:) | No | N/A |

## When to Use

**Use DTO when:**

- REST/GraphQL API (input/output)
- Communication between services
- Domain/presentation separation
- API versioning
- Specific serialization
- Grouping related structs

**Avoid DTO when:**

- Excessive duplication (1:1 with domain)
- Simple/CRUD applications
- Critical performance (mapping overhead)

## Related Patterns

- [Remote Facade](./remote-facade.md) - Uses DTO for coarse-grained transfer
- [Service Layer](./service-layer.md) - Converts domain to DTO
- [Domain Model](./domain-model.md) - Source model for DTOs
- [Data Mapper](./data-mapper.md) - Similar but for persistence
- [CQRS](../architectural/cqrs.md) - Separate DTOs for Command/Query

## Sources

- Martin Fowler, PoEAA, Chapter 15
- [Data Transfer Object - martinfowler.com](https://martinfowler.com/eaaCatalog/dataTransferObject.html)
- [conventions/dto-tags.md](../conventions/dto-tags.md) - Internal convention
