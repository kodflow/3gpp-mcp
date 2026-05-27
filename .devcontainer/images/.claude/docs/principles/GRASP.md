# GRASP Patterns

General Responsibility Assignment Software Patterns - Craig Larman.

9 fundamental patterns for responsibility assignment in OOP.

---

## 1. Information Expert

> Assign the responsibility to the class that has the necessary information.

```go
// ❌ BAD - Logic away from the data
type OrderService struct{}

func (s *OrderService) CalculateTotal(order *Order) float64 {
	total := 0.0
	for _, item := range order.Items {
		total += item.Price * float64(item.Quantity)
	}
	return total
}

// ✅ GOOD - Order has the data, Order calculates
type Order struct {
	Items []*OrderItem
}

// Total calculates the order total.
func (o *Order) Total() float64 {
	total := 0.0
	for _, item := range o.Items {
		total += item.Subtotal()
	}
	return total
}

type OrderItem struct {
	Price    float64
	Quantity int
}

// Subtotal calculates the item subtotal.
func (i *OrderItem) Subtotal() float64 {
	return i.Price * float64(i.Quantity)
}
```

**Rule:** Whoever has the data does the calculation.

---

## 2. Creator

> Assign the responsibility of creating an object to the class that:
>
> - Contains or aggregates the object
> - Records the object
> - Closely uses the object
> - Has the initialization data

```go
// ❌ BAD - External factory without reason
type OrderItemFactory struct{}

func (f *OrderItemFactory) Create(product *Product, qty int) *OrderItem {
	return &OrderItem{
		ProductID: product.ID,
		Price:     product.Price,
		Quantity:  qty,
	}
}

// ✅ GOOD - Order creates its OrderItems (it contains them)
type Order struct {
	Items []*OrderItem
}

// AddItem creates and adds an OrderItem to the order.
func (o *Order) AddItem(product *Product, quantity int) {
	// Order creates OrderItem because it aggregates them
	item := &OrderItem{
		ProductID: product.ID,
		Price:     product.Price,
		Quantity:  quantity,
	}
	o.Items = append(o.Items, item)
}

// ✅ ALSO GOOD - Factory method when creation is complex
func NewOrder(customer *Customer, cartItems []*CartItem) (*Order, error) {
	// Order creates itself with complex logic
	order := &Order{
		CustomerID: customer.ID,
		Items:      make([]*OrderItem, 0, len(cartItems)),
	}

	for _, cartItem := range cartItems {
		order.AddItem(cartItem.Product, cartItem.Quantity)
	}

	return order, nil
}
```

---

## 3. Controller

> First object after the UI that receives and coordinates system operations.

```go
// Facade Controller - One controller per use case
type PlaceOrderController struct {
	orderService        *OrderService
	paymentService      *PaymentService
	notificationService *NotificationService
}

func NewPlaceOrderController(
	orderService *OrderService,
	paymentService *PaymentService,
	notificationService *NotificationService,
) *PlaceOrderController {
	return &PlaceOrderController{
		orderService:        orderService,
		paymentService:      paymentService,
		notificationService: notificationService,
	}
}

// Execute coordinates the place order use case.
func (c *PlaceOrderController) Execute(ctx context.Context, req *PlaceOrderRequest) (*PlaceOrderResponse, error) {
	// Coordinates but does not contain business logic
	order, err := c.orderService.Create(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("creating order: %w", err)
	}

	if err := c.paymentService.Charge(ctx, order); err != nil {
		return nil, fmt.Errorf("charging payment: %w", err)
	}

	if err := c.notificationService.SendConfirmation(ctx, order); err != nil {
		// Log but don't fail
		fmt.Printf("failed to send confirmation: %v\n", err)
	}

	return &PlaceOrderResponse{OrderID: order.ID}, nil
}

// Use Case Controller - One controller per aggregate
type OrderController struct {
	service *OrderService
}

func (c *OrderController) Place(ctx context.Context, req *http.Request) (*http.Response, error) {
	// ... place order logic
	return nil, nil
}

func (c *OrderController) Cancel(ctx context.Context, req *http.Request) (*http.Response, error) {
	// ... cancel order logic
	return nil, nil
}

func (c *OrderController) Update(ctx context.Context, req *http.Request) (*http.Response, error) {
	// ... update order logic
	return nil, nil
}
```

**Rule:** The controller coordinates, it does not do the work.

---

## 4. Low Coupling

> Minimize dependencies between classes.

```go
// ❌ BAD - Tight coupling
type OrderService struct {
	db     *PostgresDatabase  // Coupled to Postgres
	mailer *SendGridMailer    // Coupled to SendGrid
	logger *WinstonLogger     // Coupled to Winston
}

// ✅ GOOD - Loose coupling via interfaces
type Database interface {
	Query(ctx context.Context, sql string, args ...interface{}) (*sql.Rows, error)
}

type Mailer interface {
	Send(ctx context.Context, to, subject, body string) error
}

type Logger interface {
	Log(message string)
	Error(message string)
}

type OrderService struct {
	db     Database  // Coupled to the interface, not the implementation
	mailer Mailer
	logger Logger
}

func NewOrderService(db Database, mailer Mailer, logger Logger) *OrderService {
	return &OrderService{
		db:     db,
		mailer: mailer,
		logger: logger,
	}
}
```

**Metrics:**

- Number of imports
- Dependency depth
- Fan-out (classes used)

---

## 5. High Cohesion

> A class does one thing well, all its members are related.

```go
// ❌ BAD - Low cohesion (does too many things)
type UserManager struct {
	db *sql.DB
}

func (m *UserManager) CreateUser(user *User) error        { /* ... */ }
func (m *UserManager) DeleteUser(id string) error         { /* ... */ }
func (m *UserManager) SendEmail(to, subject string) error { /* ... */ }      // Not related to users
func (m *UserManager) GenerateReport() ([]byte, error)    { /* ... */ }  // Not related to users
func (m *UserManager) BackupDatabase() error              { /* ... */ }  // Really not related

// ✅ GOOD - High cohesion (one responsibility)
type UserRepository struct {
	db *sql.DB
}

func (r *UserRepository) Create(ctx context.Context, user *User) error {
	// ...
	return nil
}

func (r *UserRepository) Delete(ctx context.Context, id string) error {
	// ...
	return nil
}

func (r *UserRepository) Find(ctx context.Context, id string) (*User, error) {
	// ...
	return nil, nil
}

func (r *UserRepository) FindByEmail(ctx context.Context, email string) (*User, error) {
	// ...
	return nil, nil
}

type EmailService struct {
	mailer Mailer
}

func (s *EmailService) Send(ctx context.Context, to, subject, body string) error {
	return s.mailer.Send(ctx, to, subject, body)
}

func (s *EmailService) SendTemplate(ctx context.Context, to, template string, data map[string]interface{}) error {
	// ...
	return nil
}

type ReportGenerator struct{}

func (g *ReportGenerator) Generate(reportType string, data interface{}) ([]byte, error) {
	// ...
	return nil, nil
}
```

**Test:** Can you describe the class in one sentence without "and"?

---

## 6. Polymorphism

> Use polymorphism rather than type-based conditionals.

```go
// ❌ BAD - Switch on type
type PaymentProcessor struct{}

func (p *PaymentProcessor) Process(payment *Payment) error {
	switch payment.Type {
	case "credit_card":
		return p.processCreditCard(payment)
	case "paypal":
		return p.processPaypal(payment)
	case "crypto":
		return p.processCrypto(payment)
	default:
		return errors.New("unknown payment type")
	}
}

// ✅ GOOD - Polymorphism
type PaymentMethod interface {
	Process(ctx context.Context, amount float64) (*PaymentResult, error)
}

type CreditCardPayment struct {
	CardNumber string
	CVV        string
}

func (c *CreditCardPayment) Process(ctx context.Context, amount float64) (*PaymentResult, error) {
	// Credit card logic
	return &PaymentResult{Success: true}, nil
}

type PaypalPayment struct {
	Email string
}

func (p *PaypalPayment) Process(ctx context.Context, amount float64) (*PaymentResult, error) {
	// PayPal logic
	return &PaymentResult{Success: true}, nil
}

type CryptoPayment struct {
	WalletAddress string
}

func (c *CryptoPayment) Process(ctx context.Context, amount float64) (*PaymentResult, error) {
	// Crypto logic
	return &PaymentResult{Success: true}, nil
}

// Usage - no switch
type PaymentProcessor struct{}

func (p *PaymentProcessor) ProcessPayment(ctx context.Context, method PaymentMethod, amount float64) (*PaymentResult, error) {
	return method.Process(ctx, amount)
}
```

---

## 7. Pure Fabrication

> Create an artificial class to maintain cohesion and coupling.

```go
// Problem: where to put Order persistence?
// - Order? No, would violate cohesion (business logic + DB)
// - Database? No, too generic

// ✅ Pure Fabrication - Artificial struct
type OrderRepository struct {
	db Database
}

func NewOrderRepository(db Database) *OrderRepository {
	return &OrderRepository{db: db}
}

// Save persists an order to the database.
func (r *OrderRepository) Save(ctx context.Context, order *Order) error {
	row := r.toRow(order)
	_, err := r.db.Query(ctx, "INSERT INTO orders (id, customer_id, total) VALUES ($1, $2, $3)",
		row["id"], row["customer_id"], row["total"])
	return err
}

// FindByID retrieves an order by ID.
func (r *OrderRepository) FindByID(ctx context.Context, id string) (*Order, error) {
	rows, err := r.db.Query(ctx, "SELECT id, customer_id, total FROM orders WHERE id = $1", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, nil
	}

	return r.toDomain(rows)
}

func (r *OrderRepository) toRow(order *Order) map[string]interface{} {
	return map[string]interface{}{
		"id":          order.ID,
		"customer_id": order.CustomerID,
		"total":       order.Total(),
	}
}

func (r *OrderRepository) toDomain(rows *sql.Rows) (*Order, error) {
	var order Order
	err := rows.Scan(&order.ID, &order.CustomerID)
	return &order, err
}

// Other common Pure Fabrications:
// - Services (OrderService, PaymentService)
// - Factories (OrderFactory)
// - Strategies (PricingStrategy)
// - Adapters (EmailAdapter)
```

**Rule:** If no existing struct fits, create one.

---

## 8. Indirection

> Add an intermediary to decouple.

```go
// ❌ Direct coupling
type TaxJarAPI struct{}

func (api *TaxJarAPI) Calculate(amount float64, state string) (float64, error) {
	// TaxJar-specific API call
	return 0, nil
}

type OrderService struct {
	taxApi *TaxJarAPI // Coupled to TaxJar
}

func (s *OrderService) CalculateTax(order *Order) (float64, error) {
	return s.taxApi.Calculate(order.Total(), order.State)
}

// ✅ Indirection via interface
type TaxCalculator interface {
	Calculate(ctx context.Context, amount float64, state string) (float64, error)
}

type TaxJarAdapter struct {
	api *TaxJarAPI
}

func (a *TaxJarAdapter) Calculate(ctx context.Context, amount float64, state string) (float64, error) {
	return a.api.Calculate(amount, state)
}

type OrderService struct {
	taxCalculator TaxCalculator // Decoupled
}

func NewOrderService(taxCalculator TaxCalculator) *OrderService {
	return &OrderService{taxCalculator: taxCalculator}
}

func (s *OrderService) CalculateTax(ctx context.Context, order *Order) (float64, error) {
	return s.taxCalculator.Calculate(ctx, order.Total(), order.State)
}
```

**Forms of indirection:**

- Adapter
- Facade
- Proxy
- Mediator

---

## 9. Protected Variations

> Protect elements from variations in other elements.

```go
// The problem: code using PaymentGateway
// should not be affected if a new payment type is added

// ✅ Protected Variations via stable interface
type PaymentGateway interface {
	Charge(ctx context.Context, amount float64, method PaymentMethod) (*Transaction, error)
	Refund(ctx context.Context, transactionID string) error
}

// Variations are encapsulated in implementations
type StripeGateway struct {
	apiKey string
}

func (g *StripeGateway) Charge(ctx context.Context, amount float64, method PaymentMethod) (*Transaction, error) {
	// Stripe-specific implementation
	return &Transaction{ID: "stripe-123"}, nil
}

func (g *StripeGateway) Refund(ctx context.Context, transactionID string) error {
	// Stripe-specific implementation
	return nil
}

type PayPalGateway struct {
	clientID string
}

func (g *PayPalGateway) Charge(ctx context.Context, amount float64, method PaymentMethod) (*Transaction, error) {
	// PayPal-specific implementation
	return &Transaction{ID: "paypal-456"}, nil
}

func (g *PayPalGateway) Refund(ctx context.Context, transactionID string) error {
	// PayPal-specific implementation
	return nil
}

// Client code is protected from variations
type CheckoutService struct {
	gateway PaymentGateway
}

func NewCheckoutService(gateway PaymentGateway) *CheckoutService {
	return &CheckoutService{gateway: gateway}
}

func (s *CheckoutService) Checkout(ctx context.Context, cart *Cart) (*Transaction, error) {
	// Does not know and does not care about the implementation
	transaction, err := s.gateway.Charge(ctx, cart.Total, cart.PaymentMethod)
	if err != nil {
		return nil, fmt.Errorf("charging payment: %w", err)
	}
	return transaction, nil
}
```

**Protected variation points:**

```go
// 1. Data source variations
type Repository[T any] interface {
	Find(ctx context.Context, id string) (*T, error)
	Save(ctx context.Context, entity *T) error
}
// Implementations: PostgresRepository, MongoRepository, InMemoryRepository

// 2. External service variations
type NotificationService interface {
	Send(ctx context.Context, notification *Notification) error
}
// Implementations: EmailNotification, SMSNotification, PushNotification

// 3. Algorithm variations
type PricingStrategy interface {
	Calculate(basePrice float64, context *PricingContext) float64
}
// Implementations: RegularPricing, DiscountPricing, MemberPricing

// 4. Platform variations
type FileStorage interface {
	Upload(ctx context.Context, file []byte, path string) (string, error)
	Download(ctx context.Context, path string) ([]byte, error)
}
// Implementations: LocalStorage, S3Storage, GCSStorage
```

**Techniques:**

- Interfaces
- Dependency Injection
- External configuration
- Plugins / Extensions

---

## Summary Table

| Pattern | Question | Answer |
|---------|----------|--------|
| Information Expert | Who should do X? | The one who has the data |
| Creator | Who should create X? | The one who contains/uses X |
| Controller | Who receives requests? | A dedicated coordinator |
| Low Coupling | How to reduce dependencies? | Interfaces, DI |
| High Cohesion | How to keep focus? | One responsibility per struct |
| Polymorphism | How to avoid switch on type? | Interfaces + implementations |
| Pure Fabrication | Where to put orphan logic? | Create a dedicated struct |
| Indirection | How to decouple A from B? | Add an intermediary |
| Protected Variations | How to isolate changes? | Stable interfaces |

## Relationships with Other Patterns

| GRASP | GoF Equivalent |
|-------|----------------|
| Polymorphism | Strategy, State |
| Pure Fabrication | Service, Repository |
| Indirection | Adapter, Facade, Proxy |
| Protected Variations | Abstract Factory, Bridge |

## When to Use

- When designing classes and assigning responsibilities
- When unsure about "where to place this method or behavior"
- To evaluate the quality of an object-oriented architecture
- When refactoring to improve cohesion and reduce coupling
- Before creating a new class or interface

## Related Patterns

- [SOLID](./SOLID.md) - Complementary for OOP principles
- [DRY](./DRY.md) - Pure Fabrication helps centralize logic
- [Defensive Programming](./defensive.md) - Controller coordinates validations

## Sources

- [GRASP - Craig Larman](https://en.wikipedia.org/wiki/GRASP_(object-oriented_design))
- [Applying UML and Patterns](https://www.amazon.com/Applying-UML-Patterns-Introduction-Object-Oriented/dp/0131489062)
