# Object Mother

> Centralized factory for pre-configured test objects.

## Principle

```
┌─────────────────────────────────────────────────────────────────┐
│                      Object Mother Pattern                       │
│                                                                  │
│   UserMother.John()        ──► Predefined "John" user           │
│   UserMother.Admin()       ──► Any admin user                   │
│   UserMother.Random()      ──► Random valid user                │
│   UserMother.WithOrders()  ──► User with related objects        │
│                                                                  │
│   Benefits: Named scenarios, Consistent test data, Readable     │
└─────────────────────────────────────────────────────────────────┘
```

## Go Implementation

```go
package testdata

import (
	"time"

	"github.com/google/uuid"
)

// User domain model
type User struct {
	ID        string
	Email     string
	Name      string
	Role      string
	Active    bool
	CreatedAt time.Time
}

// UserMother provides predefined test users
type UserMother struct{}

// John returns a predefined "John" user
func (UserMother) John() *User {
	return &User{
		ID:        "john-id",
		Email:     "john.doe@test.com",
		Name:      "John Doe",
		Role:      "member",
		Active:    true,
		CreatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

// Jane returns a predefined "Jane" user
func (UserMother) Jane() *User {
	return &User{
		ID:        "jane-id",
		Email:     "jane.smith@test.com",
		Name:      "Jane Smith",
		Role:      "member",
		Active:    true,
		CreatedAt: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
	}
}

// Admin returns an admin user
func (UserMother) Admin() *User {
	return &User{
		ID:        uuid.NewString(),
		Email:     "admin@test.com",
		Name:      "Admin User",
		Role:      "admin",
		Active:    true,
		CreatedAt: time.Now(),
	}
}

// Guest returns a guest user
func (UserMother) Guest() *User {
	return &User{
		ID:        uuid.NewString(),
		Email:     "guest@test.com",
		Name:      "Guest User",
		Role:      "guest",
		Active:    false,
		CreatedAt: time.Now(),
	}
}

// Inactive returns an inactive user
func (UserMother) Inactive() *User {
	john := UserMother{}.John()
	john.ID = "inactive-user-id"
	john.Active = false
	return john
}

// Random returns a random valid user
func (UserMother) Random() *User {
	return &User{
		ID:        uuid.NewString(),
		Email:     faker.Email(),
		Name:      faker.Name(),
		Role:      randomRole(),
		Active:    randomBool(),
		CreatedAt: faker.PastTime(),
	}
}

// RandomList returns multiple random users
func (UserMother) RandomList(count int) []*User {
	users := make([]*User, count)
	for i := 0; i < count; i++ {
		users[i] = UserMother{}.Random()
	}
	return users
}

// With returns a user with custom overrides
func (UserMother) With(overrides func(*User)) *User {
	user := UserMother{}.John()
	overrides(user)
	return user
}

// Helper functions
func randomRole() string {
	roles := []string{"admin", "member", "guest"}
	return roles[rand.Intn(len(roles))]
}

func randomBool() bool {
	return rand.Intn(2) == 1
}
```

## Object Mother with Relationships

```go
package testdata

import (
	"github.com/google/uuid"
)

// Order domain model
type Order struct {
	ID     string
	UserID string
	Items  []OrderItem
	Status string
	Total  float64
}

type OrderItem struct {
	ProductID string
	Name      string
	Quantity  int
	Price     float64
}

// ProductMother provides predefined products
type ProductMother struct{}

func (ProductMother) Widget() OrderItem {
	return OrderItem{
		ProductID: "widget-id",
		Name:      "Widget",
		Quantity:  1,
		Price:     29.99,
	}
}

func (ProductMother) Gadget() OrderItem {
	return OrderItem{
		ProductID: "gadget-id",
		Name:      "Gadget",
		Quantity:  1,
		Price:     49.99,
	}
}

func (ProductMother) Random() OrderItem {
	return OrderItem{
		ProductID: uuid.NewString(),
		Name:      faker.ProductName(),
		Quantity:  rand.Intn(10) + 1,
		Price:     faker.Price(),
	}
}

// OrderMother provides predefined orders
type OrderMother struct{}

func (OrderMother) Pending() *Order {
	items := []OrderItem{ProductMother{}.Widget()}
	return &Order{
		ID:     "pending-order-id",
		UserID: UserMother{}.John().ID,
		Items:  items,
		Status: "pending",
		Total:  calculateTotal(items),
	}
}

func (OrderMother) Confirmed() *Order {
	order := OrderMother{}.Pending()
	order.ID = "confirmed-order-id"
	order.Status = "confirmed"
	return order
}

func (OrderMother) Shipped() *Order {
	order := OrderMother{}.Pending()
	order.ID = "shipped-order-id"
	order.Status = "shipped"
	return order
}

func (OrderMother) ForUser(user *User) *Order {
	order := OrderMother{}.Pending()
	order.ID = uuid.NewString()
	order.UserID = user.ID
	return order
}

func (OrderMother) WithItems(items []OrderItem) *Order {
	return &Order{
		ID:     uuid.NewString(),
		UserID: UserMother{}.John().ID,
		Items:  items,
		Status: "pending",
		Total:  calculateTotal(items),
	}
}

func (OrderMother) Expensive() *Order {
	items := make([]OrderItem, 10)
	for i := 0; i < 10; i++ {
		item := ProductMother{}.Random()
		item.Price = 999.99
		item.Quantity = 5
		items[i] = item
	}
	return OrderMother{}.WithItems(items)
}

func calculateTotal(items []OrderItem) float64 {
	total := 0.0
	for _, item := range items {
		total += float64(item.Quantity) * item.Price
	}
	return total
}
```

## Combining with Builder

```go
package testdata

// UserBuilder for flexible construction
type UserBuilder struct {
	user *User
}

func NewUserBuilder() *UserBuilder {
	return &UserBuilder{
		user: &User{
			ID:        uuid.NewString(),
			Email:     "default@test.com",
			Name:      "Default User",
			Role:      "member",
			Active:    true,
			CreatedAt: time.Now(),
		},
	}
}

func (b *UserBuilder) WithID(id string) *UserBuilder {
	b.user.ID = id
	return b
}

func (b *UserBuilder) WithEmail(email string) *UserBuilder {
	b.user.Email = email
	return b
}

func (b *UserBuilder) WithName(name string) *UserBuilder {
	b.user.Name = name
	return b
}

func (b *UserBuilder) AsAdmin() *UserBuilder {
	b.user.Role = "admin"
	return b
}

func (b *UserBuilder) Build() *User {
	return b.user
}

// Enhanced UserMother using Builder
func (UserMother) Builder() *UserBuilder {
	return NewUserBuilder()
}

func (UserMother) JohnAsAdmin() *User {
	return NewUserBuilder().
		WithID("john-id").
		WithEmail("john.doe@test.com").
		WithName("John Doe").
		AsAdmin().
		Build()
}

func (UserMother) Custom() *UserBuilder {
	return NewUserBuilder()
}

// Usage
func TestWithCustomUser(t *testing.T) {
	admin := UserMother{}.Admin()
	customUser := UserMother{}.Custom().
		WithEmail("custom@test.com").
		AsAdmin().
		Build()

	// ...
}
```

## Scenario-Based Mothers

```go
package testdata

// ScenarioMother provides complete test scenarios
type ScenarioMother struct{}

// UserWithPendingOrders returns a user with pending orders
func (ScenarioMother) UserWithPendingOrders() (user *User, orders []*Order) {
	user = UserMother{}.John()
	orders = []*Order{
		OrderMother{}.ForUser(user),
		OrderMother{}.ForUser(user),
	}
	return
}

// UserWithNoOrders returns a user with no orders
func (ScenarioMother) UserWithNoOrders() (user *User, orders []*Order) {
	user = UserMother{}.Jane()
	orders = []*Order{}
	return
}

// AdminWithFullAccess returns an admin with all permissions
func (ScenarioMother) AdminWithFullAccess() (user *User, permissions []string, token string) {
	user = UserMother{}.Admin()
	permissions = []string{"read", "write", "delete", "admin"}
	token = "admin-token-123"
	return
}

// ExpiredSession returns a user with expired session
func (ScenarioMother) ExpiredSession() (user *User, session *Session) {
	user = UserMother{}.John()
	session = &Session{
		ID:        "expired-session",
		UserID:    user.ID,
		ExpiresAt: time.Now().Add(-time.Hour), // Expired
	}
	return
}

// Usage in tests
func TestOrderService(t *testing.T) {
	t.Run("should list user orders", func(t *testing.T) {
		user, orders := ScenarioMother{}.UserWithPendingOrders()

		// Setup
		for _, order := range orders {
			_ = orderRepo.Save(context.Background(), order)
		}

		service := NewOrderService(orderRepo)
		result, err := service.ListForUser(context.Background(), user.ID)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result) != 2 {
			t.Errorf("len(result) = %d; want 2", len(result))
		}
	})

	t.Run("should return empty for user with no orders", func(t *testing.T) {
		user, _ := ScenarioMother{}.UserWithNoOrders()

		service := NewOrderService(orderRepo)
		result, err := service.ListForUser(context.Background(), user.ID)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result) != 0 {
			t.Errorf("len(result) = %d; want 0", len(result))
		}
	})
}
```

## Database Seeding with Mothers

```go
package testutil

import (
	"context"
)

// TestDatabaseSeeder seeds test data using Object Mothers
type TestDatabaseSeeder struct {
	userRepo  UserRepository
	orderRepo OrderRepository
}

func NewTestDatabaseSeeder(userRepo UserRepository, orderRepo OrderRepository) *TestDatabaseSeeder {
	return &TestDatabaseSeeder{
		userRepo:  userRepo,
		orderRepo: orderRepo,
	}
}

func (s *TestDatabaseSeeder) SeedBasic(ctx context.Context) error {
	if err := s.userRepo.Save(ctx, UserMother{}.John()); err != nil {
		return err
	}
	if err := s.userRepo.Save(ctx, UserMother{}.Jane()); err != nil {
		return err
	}
	if err := s.userRepo.Save(ctx, UserMother{}.Admin()); err != nil {
		return err
	}
	return nil
}

func (s *TestDatabaseSeeder) SeedWithOrders(ctx context.Context) error {
	if err := s.SeedBasic(ctx); err != nil {
		return err
	}

	john := UserMother{}.John()
	if err := s.orderRepo.Save(ctx, OrderMother{}.ForUser(john)); err != nil {
		return err
	}
	if err := s.orderRepo.Save(ctx, OrderMother{}.ForUser(john)); err != nil {
		return err
	}
	return nil
}

func (s *TestDatabaseSeeder) SeedStressTest(ctx context.Context) error {
	users := UserMother{}.RandomList(100)
	for _, user := range users {
		if err := s.userRepo.Save(ctx, user); err != nil {
			return err
		}
		orderCount := rand.Intn(11) // 0-10 orders
		for i := 0; i < orderCount; i++ {
			if err := s.orderRepo.Save(ctx, OrderMother{}.ForUser(user)); err != nil {
				return err
			}
		}
	}
	return nil
}
```

## Recommended Libraries

| Package | Usage |
|---------|-------|
| `github.com/google/uuid` | UUID generation |
| `github.com/brianvoe/gofakeit/v6` | Fake data generation |
| `github.com/icrowley/fake` | Alternative faker |

## Common Mistakes

| Mistake | Impact | Solution |
|---------|--------|----------|
| Duplicate IDs | DB conflicts | UUIDs or unique IDs per scenario |
| Mutable data | Dependent tests | Always return new instances |
| Too specific Mothers | Method explosion | Combine with Builder |
| No scenarios | Repeated setup | Add ScenarioMother |
| Unrealistic data | Undetected bugs | Faker for valid data |

## When to Use

| Scenario | Recommended |
|----------|------------|
| Recurring personas (John, Jane) | Yes |
| Typical business scenarios | Yes |
| Consistent test data | Yes |
| Highly variable objects | Builder preferred |
| Unique data per test | Random() methods |

## Related Patterns

- **Test Data Builder**: Complementary flexibility
- **Fixture**: Object Mothers populate fixtures
- **Factory**: Underlying pattern

## Sources

- [Object Mother - Martin Fowler](https://martinfowler.com/bliki/ObjectMother.html)
- [Growing Object-Oriented Software](http://www.growing-object-oriented-software.com/)
