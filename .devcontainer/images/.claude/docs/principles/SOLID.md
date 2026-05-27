# SOLID Principles

5 fundamental principles of object-oriented programming by Robert C. Martin.

## The 5 Principles

### S - Single Responsibility Principle (SRP)

> A class should have only one reason to change.

**Problem:**

```go
// ❌ Bad - Multiple responsibilities
type User struct {
	ID    string
	Email string
}

func (u *User) Save() error { /* DB logic */ }
func (u *User) Validate() error { /* Validation logic */ }
func (u *User) SendEmail() error { /* Email logic */ }
```

**Solution:**

```go
// ✅ Good - One responsibility per type
type User struct {
	ID    string
	Email string
}

type UserRepository struct {
	db *sql.DB
}

func (r *UserRepository) Save(user *User) error { /* DB logic */ }

type UserValidator struct{}

func (v *UserValidator) Validate(user *User) error { /* Validation logic */ }

type UserNotifier struct {
	mailer Mailer
}

func (n *UserNotifier) SendEmail(user *User) error { /* Email logic */ }
```

**When to apply:** Always. This is the most fundamental principle.

---

### O - Open/Closed Principle (OCP)

> Open for extension, closed for modification.

**Problem:**

```go
// ❌ Bad - Modify to add
type PaymentProcessor struct{}

func (p *PaymentProcessor) Process(paymentType string) error {
	switch paymentType {
	case "card":
		// ... card logic
	case "paypal":
		// ... paypal logic
	// Adding here = modifying
	default:
		return errors.New("unknown payment type")
	}
	return nil
}
```

**Solution:**

```go
// ✅ Good - Extend without modifying
type PaymentMethod interface {
	Process() error
}

type CardPayment struct {
	CardNumber string
}

func (c *CardPayment) Process() error {
	// Card processing logic
	return nil
}

type PayPalPayment struct {
	Email string
}

func (p *PayPalPayment) Process() error {
	// PayPal processing logic
	return nil
}

// Adding = new struct implementing PaymentMethod
type CryptoPayment struct {
	WalletAddress string
}

func (c *CryptoPayment) Process() error {
	// Crypto processing logic
	return nil
}

// Usage
type PaymentProcessor struct{}

func (p *PaymentProcessor) ProcessPayment(method PaymentMethod) error {
	return method.Process()
}
```

**When to apply:** When the code changes often to add variants.

---

### L - Liskov Substitution Principle (LSP)

> Subtypes must be substitutable for their base types.

**Problem:**

```go
// ❌ Bad - Square is not a Rectangle
type Rectangle struct {
	width  float64
	height float64
}

func (r *Rectangle) SetWidth(w float64)  { r.width = w }
func (r *Rectangle) SetHeight(h float64) { r.height = h }
func (r *Rectangle) Area() float64       { return r.width * r.height }

type Square struct {
	Rectangle
}

func (s *Square) SetWidth(w float64) {
	s.width = w
	s.height = w // Violates LSP - unexpected behavior
}

func (s *Square) SetHeight(h float64) {
	s.width = h
	s.height = h // Violates LSP - unexpected behavior
}
```

**Solution:**

```go
// ✅ Good - Common abstraction
type Shape interface {
	Area() float64
}

type Rectangle struct {
	width  float64
	height float64
}

func NewRectangle(width, height float64) *Rectangle {
	return &Rectangle{width: width, height: height}
}

func (r *Rectangle) Area() float64 {
	return r.width * r.height
}

type Square struct {
	side float64
}

func NewSquare(side float64) *Square {
	return &Square{side: side}
}

func (s *Square) Area() float64 {
	return s.side * s.side
}

// Usage - both are substitutable
func PrintArea(s Shape) {
	fmt.Printf("Area: %.2f\n", s.Area())
}
```

**When to apply:** Before each inheritance/composition, verify substitution.

---

### I - Interface Segregation Principle (ISP)

> Multiple specific interfaces are better than one general interface.

**Problem:**

```go
// ❌ Bad - Interface too broad
type Worker interface {
	Work()
	Eat()
	Sleep()
}

type Robot struct{}

func (r *Robot) Work() {
	// OK
}

func (r *Robot) Eat() {
	// Robots don't eat - forced method
	panic("robots don't eat")
}

func (r *Robot) Sleep() {
	// Robots don't sleep - forced method
	panic("robots don't sleep")
}
```

**Solution:**

```go
// ✅ Good - Specific interfaces
type Workable interface {
	Work()
}

type Eatable interface {
	Eat()
}

type Sleepable interface {
	Sleep()
}

type Robot struct{}

func (r *Robot) Work() {
	// OK - Robot implements only Workable
}

type Human struct{}

func (h *Human) Work()  { /* ... */ }
func (h *Human) Eat()   { /* ... */ }
func (h *Human) Sleep() { /* ... */ }

// Usage
func DoWork(w Workable) {
	w.Work()
}

func TakeCareOf(e Eatable, s Sleepable) {
	e.Eat()
	s.Sleep()
}
```

**When to apply:** When implementers have to leave methods empty or panic.

---

### D - Dependency Inversion Principle (DIP)

> Depend on abstractions, not on concrete implementations.

**Problem:**

```go
// ❌ Bad - Concrete dependency
type MySQLDatabase struct{}

func (db *MySQLDatabase) Query(sql string) ([]byte, error) {
	// MySQL-specific query
	return nil, nil
}

type UserService struct {
	db *MySQLDatabase // Tight coupling to MySQL
}

func NewUserService() *UserService {
	return &UserService{
		db: &MySQLDatabase{}, // Hard-coded dependency
	}
}

func (s *UserService) GetUser(ctx context.Context, id string) (*User, error) {
	data, err := s.db.Query(ctx, "SELECT * FROM users WHERE id = $1", id)
	if err != nil {
		return nil, fmt.Errorf("querying user: %w", err)
	}
	// ... parse data
	return nil, nil
}
```

**Solution:**

```go
// ✅ Good - Dependency on abstraction
type Database interface {
	Query(ctx context.Context, sql string, args ...interface{}) ([]byte, error)
}

type MySQLDatabase struct{}

func (db *MySQLDatabase) Query(ctx context.Context, sql string, args ...interface{}) ([]byte, error) {
	// MySQL-specific implementation
	return nil, nil
}

type PostgresDatabase struct{}

func (db *PostgresDatabase) Query(ctx context.Context, sql string, args ...interface{}) ([]byte, error) {
	// Postgres-specific implementation
	return nil, nil
}

type UserService struct {
	db Database // Depends on the abstraction
}

func NewUserService(db Database) *UserService {
	return &UserService{db: db} // Dependency injection
}

func (s *UserService) GetUser(ctx context.Context, id string) (*User, error) {
	data, err := s.db.Query(ctx, "SELECT * FROM users WHERE id = $1", id)
	if err != nil {
		return nil, fmt.Errorf("querying user: %w", err)
	}
	// ... parse data
	return nil, nil
}

// Usage
func main() {
	// Easily interchangeable
	mysqlDB := &MySQLDatabase{}
	service1 := NewUserService(mysqlDB)

	postgresDB := &PostgresDatabase{}
	service2 := NewUserService(postgresDB)

	_, _ = service1, service2
}
```

**When to apply:** For everything external (DB, API, filesystem).

---

## Visual Summary

```
┌─────────────────────────────────────────────────────────────┐
│  S  │ One struct/package = One responsibility               │
├─────────────────────────────────────────────────────────────┤
│  O  │ Add code, don't modify                                │
├─────────────────────────────────────────────────────────────┤
│  L  │ Subtype = parent behavior preserved                   │
├─────────────────────────────────────────────────────────────┤
│  I  │ Small and specific interfaces                         │
├─────────────────────────────────────────────────────────────┤
│  D  │ Depend on interfaces, not structs                     │
└─────────────────────────────────────────────────────────────┘
```

## When to Use

- When designing classes and interfaces (SRP, ISP)
- When adding new variants without modifying existing code (OCP)
- Before using inheritance or composition (LSP)
- To decouple modules and facilitate testing (DIP)
- During code reviews to evaluate architectural quality

## Related Patterns

- [GRASP](./GRASP.md) - Complementary for responsibility assignment
- [DRY](./DRY.md) - SRP helps centralize responsibilities
- [Defensive Programming](./defensive.md) - DIP facilitates mock injection
- **Factory**: Respects OCP for creation
- **Strategy**: Respects OCP for algorithms
- **Adapter**: Helps respect DIP
- **Facade**: Helps respect ISP

## Sources

- [Robert C. Martin - Clean Architecture](https://blog.cleancoder.com/)
- [SOLID Principles - Wikipedia](https://en.wikipedia.org/wiki/SOLID)
