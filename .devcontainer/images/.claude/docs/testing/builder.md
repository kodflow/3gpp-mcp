# Test Data Builder

> Fluent construction of test objects with sensible default values.

## Principle

```
┌─────────────────────────────────────────────────────────────────┐
│                    Test Data Builder                             │
│                                                                  │
│   NewUserBuilder()                                               │
│     .WithName("John")      ◄── Override specific fields         │
│     .WithEmail("j@t.com")                                        │
│     .AsAdmin()             ◄── Semantic presets                  │
│     .Build()               ◄── Create instance                   │
│                                                                  │
│   Result: User with defaults + overrides                         │
└─────────────────────────────────────────────────────────────────┘
```

## Go Implementation (Fluent Builder)

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
	Metadata  map[string]interface{}
}

// UserBuilder builds User instances
type UserBuilder struct {
	user *User
}

// NewUserBuilder creates a new builder with sensible defaults
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

// WithID sets the ID
func (b *UserBuilder) WithID(id string) *UserBuilder {
	b.user.ID = id
	return b
}

// WithEmail sets the email
func (b *UserBuilder) WithEmail(email string) *UserBuilder {
	b.user.Email = email
	return b
}

// WithName sets the name
func (b *UserBuilder) WithName(name string) *UserBuilder {
	b.user.Name = name
	return b
}

// WithRole sets the role
func (b *UserBuilder) WithRole(role string) *UserBuilder {
	b.user.Role = role
	return b
}

// WithMetadata sets metadata
func (b *UserBuilder) WithMetadata(metadata map[string]interface{}) *UserBuilder {
	b.user.Metadata = metadata
	return b
}

// AsAdmin sets role to admin
func (b *UserBuilder) AsAdmin() *UserBuilder {
	b.user.Role = "admin"
	return b
}

// AsGuest sets role to guest and inactive
func (b *UserBuilder) AsGuest() *UserBuilder {
	b.user.Role = "guest"
	b.user.Active = false
	return b
}

// Inactive sets active to false
func (b *UserBuilder) Inactive() *UserBuilder {
	b.user.Active = false
	return b
}

// Build returns the constructed User
func (b *UserBuilder) Build() *User {
	// Return copy to prevent mutation
	userCopy := *b.user
	if b.user.Metadata != nil {
		userCopy.Metadata = make(map[string]interface{})
		for k, v := range b.user.Metadata {
			userCopy.Metadata[k] = v
		}
	}
	return &userCopy
}

// BuildMany builds multiple users
func (b *UserBuilder) BuildMany(count int) []*User {
	users := make([]*User, count)
	for i := 0; i < count; i++ {
		users[i] = NewUserBuilder().
			WithID(fmt.Sprintf("user-%d", i)).
			WithEmail(fmt.Sprintf("user%d@test.com", i)).
			WithName(fmt.Sprintf("User %d", i)).
			Build()
	}
	return users
}

// Usage
func TestUserBuilder(t *testing.T) {
	admin := NewUserBuilder().
		WithName("Admin").
		AsAdmin().
		Build()

	inactiveUser := NewUserBuilder().
		WithEmail("old@test.com").
		Inactive().
		Build()

	users := NewUserBuilder().BuildMany(5)

	// ...
}
```

## Functional Options Pattern (Idiomatic Go)

```go
package testdata

// UserOption is a functional option for User
type UserOption func(*User)

// WithUserID sets the user ID
func WithUserID(id string) UserOption {
	return func(u *User) {
		u.ID = id
	}
}

// WithUserEmail sets the email
func WithUserEmail(email string) UserOption {
	return func(u *User) {
		u.Email = email
	}
}

// WithUserName sets the name
func WithUserName(name string) UserOption {
	return func(u *User) {
		u.Name = name
	}
}

// WithUserRole sets the role
func WithUserRole(role string) UserOption {
	return func(u *User) {
		u.Role = role
	}
}

// WithAdminRole sets admin role
func WithAdminRole() UserOption {
	return func(u *User) {
		u.Role = "admin"
	}
}

// WithInactiveStatus sets inactive status
func WithInactiveStatus() UserOption {
	return func(u *User) {
		u.Active = false
	}
}

// NewUser creates a User with options
func NewUser(opts ...UserOption) *User {
	user := &User{
		ID:        uuid.NewString(),
		Email:     "default@test.com",
		Name:      "Default User",
		Role:      "member",
		Active:    true,
		CreatedAt: time.Now(),
	}

	for _, opt := range opts {
		opt(user)
	}

	return user
}

// Usage
func TestFunctionalOptions(t *testing.T) {
	admin := NewUser(
		WithUserName("Admin"),
		WithAdminRole(),
	)

	inactiveUser := NewUser(
		WithUserEmail("old@test.com"),
		WithInactiveStatus(),
	)

	customUser := NewUser(
		WithUserID("custom-id"),
		WithUserName("John"),
		WithUserEmail("john@test.com"),
	)

	// ...
}
```

## Builder with Relationships

```go
package testdata

// Order domain model
type Order struct {
	ID        string
	UserID    string
	Items     []OrderItem
	Status    string
	Total     float64
	CreatedAt time.Time
}

type OrderItem struct {
	ProductID string
	Quantity  int
	UnitPrice float64
}

// OrderItemBuilder builds OrderItem instances
type OrderItemBuilder struct {
	item *OrderItem
}

func NewOrderItemBuilder() *OrderItemBuilder {
	return &OrderItemBuilder{
		item: &OrderItem{
			ProductID: "default-product",
			Quantity:  1,
			UnitPrice: 10.0,
		},
	}
}

func (b *OrderItemBuilder) ForProduct(productID string, price float64) *OrderItemBuilder {
	b.item.ProductID = productID
	b.item.UnitPrice = price
	return b
}

func (b *OrderItemBuilder) WithQuantity(quantity int) *OrderItemBuilder {
	b.item.Quantity = quantity
	return b
}

func (b *OrderItemBuilder) Build() OrderItem {
	return *b.item
}

// OrderBuilder builds Order instances
type OrderBuilder struct {
	order *Order
	items []OrderItem
}

func NewOrderBuilder() *OrderBuilder {
	return &OrderBuilder{
		order: &Order{
			ID:        uuid.NewString(),
			UserID:    "default-user",
			Status:    "pending",
			CreatedAt: time.Now(),
		},
		items: []OrderItem{},
	}
}

func (b *OrderBuilder) ForUser(userID string) *OrderBuilder {
	b.order.UserID = userID
	return b
}

func (b *OrderBuilder) WithItem(item OrderItem) *OrderBuilder {
	b.items = append(b.items, item)
	return b
}

func (b *OrderBuilder) WithItemBuilder(builder *OrderItemBuilder) *OrderBuilder {
	b.items = append(b.items, builder.Build())
	return b
}

func (b *OrderBuilder) WithItems(items []OrderItem) *OrderBuilder {
	b.items = items
	return b
}

func (b *OrderBuilder) WithStatus(status string) *OrderBuilder {
	b.order.Status = status
	return b
}

func (b *OrderBuilder) Confirmed() *OrderBuilder {
	return b.WithStatus("confirmed")
}

func (b *OrderBuilder) Shipped() *OrderBuilder {
	return b.WithStatus("shipped")
}

func (b *OrderBuilder) Build() *Order {
	total := 0.0
	for _, item := range b.items {
		total += float64(item.Quantity) * item.UnitPrice
	}

	order := *b.order
	order.Items = make([]OrderItem, len(b.items))
	copy(order.Items, b.items)
	order.Total = total

	return &order
}

// Usage
func TestOrderBuilder(t *testing.T) {
	order := NewOrderBuilder().
		ForUser("user-123").
		WithItemBuilder(
			NewOrderItemBuilder().
				ForProduct("product-1", 29.99).
				WithQuantity(2),
		).
		WithItemBuilder(
			NewOrderItemBuilder().
				ForProduct("product-2", 49.99),
		).
		Confirmed().
		Build()

	if order.Total != 109.97 {
		t.Errorf("order.Total = %.2f; want 109.97", order.Total)
	}
}
```

## Builder with Validation

```go
package testdata

import (
	"errors"
	"strings"
)

// ValidatingUserBuilder validates on build
type ValidatingUserBuilder struct {
	*UserBuilder
}

func NewValidatingUserBuilder() *ValidatingUserBuilder {
	return &ValidatingUserBuilder{
		UserBuilder: NewUserBuilder(),
	}
}

func (b *ValidatingUserBuilder) Build() (*User, error) {
	user := b.UserBuilder.Build()

	// Validation
	if !strings.Contains(user.Email, "@") {
		return nil, errors.New("invalid email in test data")
	}
	if len(user.Name) < 2 {
		return nil, errors.New("name too short in test data")
	}

	return user, nil
}

// MustBuild panics on validation error (useful in tests)
func (b *ValidatingUserBuilder) MustBuild() *User {
	user, err := b.Build()
	if err != nil {
		panic(err)
	}
	return user
}

// Usage
func TestValidation(t *testing.T) {
	user := NewValidatingUserBuilder().
		WithEmail("valid@test.com").
		WithName("John").
		MustBuild()

	// This would panic:
	// invalid := NewValidatingUserBuilder().
	// 	WithEmail("invalid").
	// 	MustBuild()

	_ = user
}
```

## Generic Builder

```go
package testdata

// Builder is a generic builder
type Builder[T any] struct {
	value    T
	modifiers []func(*T)
}

// NewBuilder creates a new generic builder
func NewBuilder[T any](defaults T) *Builder[T] {
	return &Builder[T]{
		value: defaults,
	}
}

// With applies a modifier function
func (b *Builder[T]) With(modifier func(*T)) *Builder[T] {
	b.modifiers = append(b.modifiers, modifier)
	return b
}

// Build constructs the value
func (b *Builder[T]) Build() T {
	result := b.value
	for _, mod := range b.modifiers {
		mod(&result)
	}
	return result
}

// Usage
type Product struct {
	ID    string
	Name  string
	Price float64
	Stock int
}

func TestGenericBuilder(t *testing.T) {
	productDefaults := Product{
		ID:    "default-id",
		Name:  "Default Product",
		Price: 0,
		Stock: 0,
	}

	product := NewBuilder(productDefaults).
		With(func(p *Product) { p.Name = "Widget" }).
		With(func(p *Product) { p.Price = 29.99 }).
		With(func(p *Product) { p.Stock = 100 }).
		Build()

	if product.Name != "Widget" {
		t.Errorf("product.Name = %q; want %q", product.Name, "Widget")
	}
}
```

## Simple Factory Functions (Alternative)

```go
package testdata

// Factory function with overrides
func CreateUser(overrides func(*User)) *User {
	user := &User{
		ID:        uuid.NewString(),
		Email:     "default@test.com",
		Name:      "Default User",
		Role:      "member",
		Active:    true,
		CreatedAt: time.Now(),
	}

	if overrides != nil {
		overrides(user)
	}

	return user
}

// Preset factories
func CreateAdmin(overrides func(*User)) *User {
	return CreateUser(func(u *User) {
		u.Role = "admin"
		if overrides != nil {
			overrides(u)
		}
	})
}

func CreateInactiveUser(overrides func(*User)) *User {
	return CreateUser(func(u *User) {
		u.Active = false
		if overrides != nil {
			overrides(u)
		}
	})
}

// Usage
func TestFactoryFunctions(t *testing.T) {
	user := CreateUser(func(u *User) {
		u.Name = "John"
		u.Email = "john@test.com"
	})

	admin := CreateAdmin(func(u *User) {
		u.Name = "Admin"
	})

	inactive := CreateInactiveUser(nil)

	// ...
}
```

## Recommended Libraries

| Package | Usage |
|---------|-------|
| `github.com/google/uuid` | UUID generation |
| `github.com/brianvoe/gofakeit/v6` | Fake data |

## Common Mistakes

| Mistake | Impact | Solution |
|---------|--------|----------|
| Mutable defaults | Shared state | Always copy in Build() |
| Too many methods | Complex API | Semantic methods |
| No randomization | ID collisions | UUID by default |
| Builder without Build() | Forgotten call | MustBuild() helper |
| Validation in tests | Fragile tests | Optional validation |

## When to Use

| Scenario | Recommended |
|----------|------------|
| Objects with many fields | Yes |
| Frequent variations | Yes |
| Complex relationships | Yes |
| Simple objects (2-3 fields) | Factory function is enough |
| Fixed data | JSON fixtures |

## Related Patterns

- **Object Mother**: Pre-configured factories based on Builders
- **Fixture**: Builders populating fixtures
- **Factory**: Underlying creation pattern

## Sources

- [Test Data Builders - Nat Pryce](http://www.natpryce.com/articles/000714.html)
- [Functional Options - Dave Cheney](https://dave.cheney.net/2014/10/17/functional-options-for-friendly-apis)
- [Growing Object-Oriented Software](http://www.growing-object-oriented-software.com/)
