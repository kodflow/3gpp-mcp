# Anti-Corruption Layer (ACL) Pattern

> Isolate the business domain from legacy or external systems to prevent model pollution.

---

## Principle

```
┌─────────────────────────────────────────────────────────────────┐
│                  ANTI-CORRUPTION LAYER                           │
│                                                                  │
│   New Domain                 ACL                Legacy System    │
│   (Clean Model)           (Translator)         (Messy Model)    │
│                                                                  │
│  ┌───────────────┐      ┌──────────────┐      ┌──────────────┐  │
│  │               │      │              │      │              │  │
│  │   Customer    │      │   Adapter    │      │   CUST_TBL   │  │
│  │   Order       │◄────►│   Facade     │◄────►│   ORD_HDR    │  │
│  │   Product     │      │   Translator │      │   ITEM_MST   │  │
│  │               │      │              │      │              │  │
│  └───────────────┘      └──────────────┘      └──────────────┘  │
│                                                                  │
│  ┌─────────────────────────────────────────────────────────────┐│
│  │ The ACL translates between the two worlds:                  ││
│  │ - Field names (customerId ↔ CUST_ID)                        ││
│  │ - Data formats (ISO date ↔ YYYYMMDD)                        ││
│  │ - Business logic (status enum ↔ numeric codes)              ││
│  │ - Protocols (REST ↔ SOAP/XML)                               ││
│  └─────────────────────────────────────────────────────────────┘│
└─────────────────────────────────────────────────────────────────┘
```

---

## ACL Components

| Component | Role |
|-----------|------|
| **Facade** | Simplified interface to the legacy system |
| **Adapter** | Converts incompatible interfaces |
| **Translator** | Transforms data between models |
| **Repository** | Abstracts access to legacy data |

---

## Go Implementation

### Domain Models (clean)

```go
package domain

import "time"

// Customer represents a clean domain customer model.
type Customer struct {
	ID        string
	Email     string
	FullName  string
	CreatedAt time.Time
	Status    CustomerStatus
	Address   Address
}

// CustomerStatus represents customer account status.
type CustomerStatus string

const (
	CustomerStatusActive    CustomerStatus = "ACTIVE"
	CustomerStatusSuspended CustomerStatus = "SUSPENDED"
	CustomerStatusClosed    CustomerStatus = "CLOSED"
)

// Address represents a postal address.
type Address struct {
	Street     string
	City       string
	Country    string
	PostalCode string
}

// Order represents a customer order.
type Order struct {
	ID         string
	CustomerID string
	Items      []OrderItem
	Total      Money
	Status     OrderStatus
	CreatedAt  time.Time
}

// Money represents a monetary amount.
type Money struct {
	Amount   float64
	Currency string
}

// OrderItem represents a single order line item.
type OrderItem struct {
	ProductID string
	Quantity  int
	Price     Money
}

// OrderStatus represents order processing status.
type OrderStatus string

const (
	OrderStatusNew        OrderStatus = "NEW"
	OrderStatusProcessing OrderStatus = "PROCESSING"
	OrderStatusShipped    OrderStatus = "SHIPPED"
	OrderStatusCancelled  OrderStatus = "CANCELLED"
)
```

---

### Legacy Model (what we receive)

```go
package legacy

// LegacyCustomerRecord represents the legacy database customer record.
type LegacyCustomerRecord struct {
	CUST_ID      string
	CUST_EMAIL   string
	CUST_FNAME   string
	CUST_LNAME   string
	CUST_CREATED string // Format: YYYYMMDD
	CUST_STATUS  int    // 1=active, 2=suspended, 9=closed
	ADDR_LINE1   string
	ADDR_CITY    string
	ADDR_CNTRY   string
	ADDR_ZIP     string
}

// LegacyOrderRecord represents the legacy database order record.
type LegacyOrderRecord struct {
	ORDER_NBR  string
	CUST_ID    string
	ORDER_AMT  int    // Cents
	ORDER_CCY  string
	ORDER_DT   string // YYYYMMDD
	ORDER_STAT string // 'N', 'P', 'S', 'C'
	ITEMS      []LegacyOrderItem
}

// LegacyOrderItem represents a legacy order line item.
type LegacyOrderItem struct {
	ITEM_ID  string
	QUANTITY int
	PRICE    int // Cents
}
```

---

### Translator (ACL core)

```go
package acl

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"example.com/app/domain"
	"example.com/app/legacy"
)

// CustomerTranslator translates between legacy and domain customer models.
type CustomerTranslator struct{}

// NewCustomerTranslator creates a new customer translator.
func NewCustomerTranslator() *CustomerTranslator {
	return &CustomerTranslator{}
}

// ToDomain converts a legacy customer record to domain model.
func (t *CustomerTranslator) ToDomain(legacy *legacy.LegacyCustomerRecord) (*domain.Customer, error) {
	createdAt, err := t.parseDate(legacy.CUST_CREATED)
	if err != nil {
		return nil, fmt.Errorf("parsing created date: %w", err)
	}

	status, err := t.mapStatus(legacy.CUST_STATUS)
	if err != nil {
		return nil, fmt.Errorf("mapping status: %w", err)
	}

	fullName := strings.TrimSpace(legacy.CUST_FNAME + " " + legacy.CUST_LNAME)

	return &domain.Customer{
		ID:        legacy.CUST_ID,
		Email:     legacy.CUST_EMAIL,
		FullName:  fullName,
		CreatedAt: createdAt,
		Status:    status,
		Address: domain.Address{
			Street:     legacy.ADDR_LINE1,
			City:       legacy.ADDR_CITY,
			Country:    legacy.ADDR_CNTRY,
			PostalCode: legacy.ADDR_ZIP,
		},
	}, nil
}

// ToLegacy converts a domain customer to legacy record.
func (t *CustomerTranslator) ToLegacy(customer *domain.Customer) (*legacy.LegacyCustomerRecord, error) {
	parts := strings.SplitN(customer.FullName, " ", 2)
	firstName := parts[0]
	lastName := ""
	if len(parts) > 1 {
		lastName = parts[1]
	}

	status, err := t.reverseMapStatus(customer.Status)
	if err != nil {
		return nil, fmt.Errorf("reverse mapping status: %w", err)
	}

	return &legacy.LegacyCustomerRecord{
		CUST_ID:      customer.ID,
		CUST_EMAIL:   customer.Email,
		CUST_FNAME:   firstName,
		CUST_LNAME:   lastName,
		CUST_CREATED: t.formatDate(customer.CreatedAt),
		CUST_STATUS:  status,
		ADDR_LINE1:   customer.Address.Street,
		ADDR_CITY:    customer.Address.City,
		ADDR_CNTRY:   customer.Address.Country,
		ADDR_ZIP:     customer.Address.PostalCode,
	}, nil
}

func (t *CustomerTranslator) parseDate(legacyDate string) (time.Time, error) {
	if len(legacyDate) != 8 {
		return time.Time{}, fmt.Errorf("invalid date format: %s", legacyDate)
	}

	year, err := strconv.Atoi(legacyDate[0:4])
	if err != nil {
		return time.Time{}, fmt.Errorf("parsing year: %w", err)
	}

	month, err := strconv.Atoi(legacyDate[4:6])
	if err != nil {
		return time.Time{}, fmt.Errorf("parsing month: %w", err)
	}

	day, err := strconv.Atoi(legacyDate[6:8])
	if err != nil {
		return time.Time{}, fmt.Errorf("parsing day: %w", err)
	}

	return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC), nil
}

func (t *CustomerTranslator) formatDate(date time.Time) string {
	return date.Format("20060102")
}

func (t *CustomerTranslator) mapStatus(legacyStatus int) (domain.CustomerStatus, error) {
	mapping := map[int]domain.CustomerStatus{
		1: domain.CustomerStatusActive,
		2: domain.CustomerStatusSuspended,
		9: domain.CustomerStatusClosed,
	}

	status, ok := mapping[legacyStatus]
	if !ok {
		return domain.CustomerStatusActive, nil // Default fallback
	}
	return status, nil
}

func (t *CustomerTranslator) reverseMapStatus(status domain.CustomerStatus) (int, error) {
	mapping := map[domain.CustomerStatus]int{
		domain.CustomerStatusActive:    1,
		domain.CustomerStatusSuspended: 2,
		domain.CustomerStatusClosed:    9,
	}

	legacyStatus, ok := mapping[status]
	if !ok {
		return 0, fmt.Errorf("unknown status: %s", status)
	}
	return legacyStatus, nil
}

// OrderTranslator translates between legacy and domain order models.
type OrderTranslator struct {
	ct *CustomerTranslator
}

// NewOrderTranslator creates a new order translator.
func NewOrderTranslator() *OrderTranslator {
	return &OrderTranslator{
		ct: NewCustomerTranslator(),
	}
}

// ToDomain converts a legacy order record to domain model.
func (t *OrderTranslator) ToDomain(legacy *legacy.LegacyOrderRecord) (*domain.Order, error) {
	createdAt, err := t.ct.parseDate(legacy.ORDER_DT)
	if err != nil {
		return nil, fmt.Errorf("parsing order date: %w", err)
	}

	status, err := t.mapOrderStatus(legacy.ORDER_STAT)
	if err != nil {
		return nil, fmt.Errorf("mapping order status: %w", err)
	}

	items := make([]domain.OrderItem, len(legacy.ITEMS))
	for i, item := range legacy.ITEMS {
		items[i] = t.translateItem(&item)
	}

	return &domain.Order{
		ID:         legacy.ORDER_NBR,
		CustomerID: legacy.CUST_ID,
		Items:      items,
		Total: domain.Money{
			Amount:   float64(legacy.ORDER_AMT) / 100, // Cents to dollars
			Currency: legacy.ORDER_CCY,
		},
		Status:    status,
		CreatedAt: createdAt,
	}, nil
}

func (t *OrderTranslator) mapOrderStatus(status string) (domain.OrderStatus, error) {
	mapping := map[string]domain.OrderStatus{
		"N": domain.OrderStatusNew,
		"P": domain.OrderStatusProcessing,
		"S": domain.OrderStatusShipped,
		"C": domain.OrderStatusCancelled,
	}

	orderStatus, ok := mapping[status]
	if !ok {
		return domain.OrderStatusNew, nil // Default fallback
	}
	return orderStatus, nil
}

func (t *OrderTranslator) translateItem(item *legacy.LegacyOrderItem) domain.OrderItem {
	return domain.OrderItem{
		ProductID: item.ITEM_ID,
		Quantity:  item.QUANTITY,
		Price: domain.Money{
			Amount:   float64(item.PRICE) / 100,
			Currency: "USD", // Would come from order record
		},
	}
}
```

---

### Adapter for the legacy system

```go
package acl

import (
	"context"
	"fmt"

	"example.com/app/domain"
	"example.com/app/legacy"
)

// LegacySystemClient defines the interface for legacy system access.
type LegacySystemClient interface {
	ExecuteQuery(ctx context.Context, query string) ([]legacy.LegacyCustomerRecord, error)
	ExecuteTransaction(ctx context.Context, commands []string) error
}

// LegacyCustomerAdapter adapts the legacy system to domain customer operations.
type LegacyCustomerAdapter struct {
	client     LegacySystemClient
	translator *CustomerTranslator
}

// NewLegacyCustomerAdapter creates a new legacy customer adapter.
func NewLegacyCustomerAdapter(client LegacySystemClient) *LegacyCustomerAdapter {
	return &LegacyCustomerAdapter{
		client:     client,
		translator: NewCustomerTranslator(),
	}
}

// FindByID retrieves a customer by ID from legacy system.
func (a *LegacyCustomerAdapter) FindByID(ctx context.Context, id string) (*domain.Customer, error) {
	query := fmt.Sprintf("SELECT * FROM CUST_TBL WHERE CUST_ID = '%s'", id)
	results, err := a.client.ExecuteQuery(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("executing query: %w", err)
	}

	if len(results) == 0 {
		return nil, nil
	}

	customer, err := a.translator.ToDomain(&results[0])
	if err != nil {
		return nil, fmt.Errorf("translating to domain: %w", err)
	}

	return customer, nil
}

// FindByEmail retrieves a customer by email from legacy system.
func (a *LegacyCustomerAdapter) FindByEmail(ctx context.Context, email string) (*domain.Customer, error) {
	query := fmt.Sprintf("SELECT * FROM CUST_TBL WHERE CUST_EMAIL = '%s'", email)
	results, err := a.client.ExecuteQuery(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("executing query: %w", err)
	}

	if len(results) == 0 {
		return nil, nil
	}

	customer, err := a.translator.ToDomain(&results[0])
	if err != nil {
		return nil, fmt.Errorf("translating to domain: %w", err)
	}

	return customer, nil
}

// Save persists a customer to the legacy system.
func (a *LegacyCustomerAdapter) Save(ctx context.Context, customer *domain.Customer) error {
	legacyRecord, err := a.translator.ToLegacy(customer)
	if err != nil {
		return fmt.Errorf("translating to legacy: %w", err)
	}

	existing, err := a.FindByID(ctx, customer.ID)
	if err != nil {
		return fmt.Errorf("checking existing customer: %w", err)
	}

	if existing != nil {
		return a.update(ctx, legacyRecord)
	}
	return a.insert(ctx, legacyRecord)
}

func (a *LegacyCustomerAdapter) insert(ctx context.Context, record *legacy.LegacyCustomerRecord) error {
	cmd := fmt.Sprintf("INSERT INTO CUST_TBL (CUST_ID, CUST_EMAIL, ...) VALUES ('%s', ...)", record.CUST_ID)
	if err := a.client.ExecuteTransaction(ctx, []string{cmd}); err != nil {
		return fmt.Errorf("inserting customer: %w", err)
	}
	return nil
}

func (a *LegacyCustomerAdapter) update(ctx context.Context, record *legacy.LegacyCustomerRecord) error {
	cmd := fmt.Sprintf("UPDATE CUST_TBL SET CUST_EMAIL = '%s', ... WHERE CUST_ID = '%s'", record.CUST_EMAIL, record.CUST_ID)
	if err := a.client.ExecuteTransaction(ctx, []string{cmd}); err != nil {
		return fmt.Errorf("updating customer: %w", err)
	}
	return nil
}

// MarkAsDeleted marks a customer as deleted in the legacy system.
func (a *LegacyCustomerAdapter) MarkAsDeleted(ctx context.Context, id string) error {
	cmd := fmt.Sprintf("UPDATE CUST_TBL SET CUST_STATUS = 9 WHERE CUST_ID = '%s'", id)
	if err := a.client.ExecuteTransaction(ctx, []string{cmd}); err != nil {
		return fmt.Errorf("marking customer as deleted: %w", err)
	}
	return nil
}
```

---

### Facade (simplified interface)

```go
package repository

import (
	"context"
	"errors"
	"fmt"

	"example.com/app/domain"
)

var (
	// ErrCustomerNotFound is returned when a customer is not found.
	ErrCustomerNotFound = errors.New("customer not found")
)

// CustomerRepository defines the clean domain interface for customer operations.
type CustomerRepository interface {
	FindByID(ctx context.Context, id string) (*domain.Customer, error)
	FindByEmail(ctx context.Context, email string) (*domain.Customer, error)
	Save(ctx context.Context, customer *domain.Customer) error
	Delete(ctx context.Context, id string) error
}

// CustomerAdapter defines the interface for ACL adapter.
type CustomerAdapter interface {
	FindByID(ctx context.Context, id string) (*domain.Customer, error)
	FindByEmail(ctx context.Context, email string) (*domain.Customer, error)
	Save(ctx context.Context, customer *domain.Customer) error
	MarkAsDeleted(ctx context.Context, id string) error
}

// LegacyCustomerRepository implements CustomerRepository using the ACL.
type LegacyCustomerRepository struct {
	adapter CustomerAdapter
}

// NewLegacyCustomerRepository creates a new repository backed by legacy system.
func NewLegacyCustomerRepository(adapter CustomerAdapter) *LegacyCustomerRepository {
	return &LegacyCustomerRepository{
		adapter: adapter,
	}
}

// FindByID retrieves a customer by ID.
func (r *LegacyCustomerRepository) FindByID(ctx context.Context, id string) (*domain.Customer, error) {
	return r.adapter.FindByID(ctx, id)
}

// FindByEmail retrieves a customer by email.
func (r *LegacyCustomerRepository) FindByEmail(ctx context.Context, email string) (*domain.Customer, error) {
	return r.adapter.FindByEmail(ctx, email)
}

// Save persists a customer.
func (r *LegacyCustomerRepository) Save(ctx context.Context, customer *domain.Customer) error {
	return r.adapter.Save(ctx, customer)
}

// Delete removes a customer.
func (r *LegacyCustomerRepository) Delete(ctx context.Context, id string) error {
	return r.adapter.MarkAsDeleted(ctx, id)
}

// CustomerService provides customer business operations.
type CustomerService struct {
	repository CustomerRepository
}

// NewCustomerService creates a new customer service.
func NewCustomerService(repository CustomerRepository) *CustomerService {
	return &CustomerService{
		repository: repository,
	}
}

// GetCustomer retrieves a customer by ID.
func (s *CustomerService) GetCustomer(ctx context.Context, id string) (*domain.Customer, error) {
	customer, err := s.repository.FindByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("finding customer: %w", err)
	}

	if customer == nil {
		return nil, ErrCustomerNotFound
	}

	return customer, nil
}
```

---

### ACL for External API

```go
package acl

import (
	"context"
	"fmt"
	"strings"

	"example.com/app/domain"
	"github.com/stripe/stripe-go/v76"
	"github.com/stripe/stripe-go/v76/paymentintent"
)

// StripePaymentACL provides anti-corruption layer for Stripe payments.
type StripePaymentACL struct {
	client *stripe.Client
}

// NewStripePaymentACL creates a new Stripe payment ACL.
func NewStripePaymentACL(apiKey string) *StripePaymentACL {
	client := stripe.Client{}
	client.Init(apiKey, nil)
	return &StripePaymentACL{
		client: &client,
	}
}

// CreatePayment creates a payment from an order.
func (acl *StripePaymentACL) CreatePayment(ctx context.Context, order *domain.Order) (*domain.Payment, error) {
	params := &stripe.PaymentIntentParams{
		Amount:   stripe.Int64(int64(order.Total.Amount * 100)),
		Currency: stripe.String(strings.ToLower(order.Total.Currency)),
	}
	params.AddMetadata("orderId", order.ID)

	intent, err := paymentintent.New(params)
	if err != nil {
		return nil, fmt.Errorf("creating payment intent: %w", err)
	}

	payment, err := acl.toDomain(intent)
	if err != nil {
		return nil, fmt.Errorf("converting to domain: %w", err)
	}

	return payment, nil
}

// GetPayment retrieves a payment by ID.
func (acl *StripePaymentACL) GetPayment(ctx context.Context, paymentID string) (*domain.Payment, error) {
	intent, err := paymentintent.Get(paymentID, nil)
	if err != nil {
		return nil, fmt.Errorf("retrieving payment intent: %w", err)
	}

	payment, err := acl.toDomain(intent)
	if err != nil {
		return nil, fmt.Errorf("converting to domain: %w", err)
	}

	return payment, nil
}

func (acl *StripePaymentACL) toDomain(intent *stripe.PaymentIntent) (*domain.Payment, error) {
	status, err := acl.mapStatus(intent.Status)
	if err != nil {
		return nil, fmt.Errorf("mapping status: %w", err)
	}

	return &domain.Payment{
		ID:      intent.ID,
		OrderID: intent.Metadata["orderId"],
		Amount: domain.Money{
			Amount:   float64(intent.Amount) / 100,
			Currency: strings.ToUpper(string(intent.Currency)),
		},
		Status: status,
	}, nil
}

func (acl *StripePaymentACL) mapStatus(stripeStatus stripe.PaymentIntentStatus) (domain.PaymentStatus, error) {
	mapping := map[stripe.PaymentIntentStatus]domain.PaymentStatus{
		stripe.PaymentIntentStatusRequiresPaymentMethod: domain.PaymentStatusPending,
		stripe.PaymentIntentStatusSucceeded:             domain.PaymentStatusCompleted,
		stripe.PaymentIntentStatusCanceled:              domain.PaymentStatusFailed,
	}

	status, ok := mapping[stripeStatus]
	if !ok {
		return domain.PaymentStatusPending, nil // Default fallback
	}
	return status, nil
}
```

---

## When to Use

- Integration with legacy systems
- Third-party APIs with different models
- Progressive migration (Strangler Fig)
- Different bounded contexts (DDD)
- Protection against external changes

---

## Related Patterns

| Pattern | Relation |
|---------|----------|
| Adapter | ACL component |
| Facade | Simplified interface |
| Translator | Data conversion |
| Repository | Persistence abstraction |
| Strangler Fig | Migration with ACL |

---

## Sources

- [Eric Evans - DDD](https://domainlanguage.com/ddd/)
- [Microsoft - ACL Pattern](https://learn.microsoft.com/en-us/azure/architecture/patterns/anti-corruption-layer)
- [Martin Fowler - Legacy Systems](https://martinfowler.com/bliki/StranglerFigApplication.html)
