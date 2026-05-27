# Monolithic Architecture

> A single application containing all the business logic.

## Principle

```
┌─────────────────────────────────────────────────────────────────┐
│                        MONOLITH                                  │
│                                                                  │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐              │
│  │    User     │  │    Order    │  │   Product   │              │
│  │   Module    │  │   Module    │  │   Module    │              │
│  └─────────────┘  └─────────────┘  └─────────────┘              │
│         │               │               │                        │
│         └───────────────┴───────────────┘                        │
│                         │                                        │
│                         ▼                                        │
│                  ┌─────────────┐                                 │
│                  │   Shared    │                                 │
│                  │  Database   │                                 │
│                  └─────────────┘                                 │
└─────────────────────────────────────────────────────────────────┘
```

## Monolith Types

### 1. Classic Monolith (to avoid)

```
Big Ball of Mud
┌─────────────────────────────────────┐
│  Spaghetti code, no structure       │
│  Everything depends on everything   │
└─────────────────────────────────────┘
```

### 2. Modular Monolith (recommended)

```
Well structured
┌─────────────────────────────────────────────────────────────┐
│                     MODULAR MONOLITH                         │
│                                                              │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐          │
│  │    Users    │  │   Orders    │  │  Products   │          │
│  │  ┌───────┐  │  │  ┌───────┐  │  │  ┌───────┐  │          │
│  │  │Domain │  │  │  │Domain │  │  │  │Domain │  │          │
│  │  ├───────┤  │  │  ├───────┤  │  │  ├───────┤  │          │
│  │  │  API  │  │  │  │  API  │  │  │  │  API  │  │          │
│  │  ├───────┤  │  │  ├───────┤  │  │  ├───────┤  │          │
│  │  │  DB   │  │  │  │  DB   │  │  │  │  DB   │  │          │
│  │  └───────┘  │  │  └───────┘  │  │  └───────┘  │          │
│  └─────────────┘  └─────────────┘  └─────────────┘          │
│        │                │                │                   │
│        └────── Public APIs between modules ──────┘          │
└─────────────────────────────────────────────────────────────┘
```

## Recommended Structure

```
src/
├── modules/
│   ├── users/
│   │   ├── domain/
│   │   │   ├── User.go
│   │   │   └── UserService.go
│   │   ├── api/
│   │   │   └── UserController.go
│   │   ├── infra/
│   │   │   └── UserRepository.go
│   │   └── module.go          # Module public API
│   │
│   ├── orders/
│   │   ├── domain/
│   │   ├── api/
│   │   ├── infra/
│   │   └── module.go
│   │
│   └── products/
│       └── ...
│
├── shared/                    # Truly shared code
│   ├── database/
│   └── utils/
│
└── main.go
```

## Modular Monolith Rules

### 1. Module Encapsulation

```go
// Direct access to internals
// import "app/modules/users/infra"

// Use public API
import "app/modules/users"

func main() {
	user, err := users.GetUser(ctx, id)
	if err != nil {
		log.Fatal(err)
	}
}
```

### 2. Communication Through Interfaces

```go
package users

import "context"

// UserModule is the public API for the user module.
type UserModule interface {
	GetUser(ctx context.Context, id string) (*User, error)
	CreateUser(ctx context.Context, data CreateUserDTO) (*User, error)
}

type userModule struct {
	service *UserService
}

// NewUserModule creates a new user module.
func NewUserModule(db *sql.DB) UserModule {
	repo := NewUserRepository(db)
	service := NewUserService(repo)
	return &userModule{service: service}
}

func (m *userModule) GetUser(ctx context.Context, id string) (*User, error) {
	return m.service.FindByID(ctx, id)
}

func (m *userModule) CreateUser(ctx context.Context, data CreateUserDTO) (*User, error) {
	return m.service.Create(ctx, data)
}
```

### 3. Database Per Schema

```sql
-- Separate schemas per module
CREATE SCHEMA users;
CREATE SCHEMA orders;
CREATE SCHEMA products;

-- Each module only accesses its own schema
```

## When to Use

| Use | Avoid |
|-------------|-----------|
| Startup / MVP | Team > 20 devs |
| Team < 10 people | Different scale needs |
| Domain not yet clear | Obvious bounded contexts |
| Need for speed | Autonomous teams required |
| Limited infra budget | Critical high availability |

## Advantages

- **Simplicity**: Single deployment
- **Performance**: In-process calls
- **Transactions**: Native ACID
- **Debugging**: Complete stack trace
- **Cost**: Less infrastructure

## Disadvantages

- **Scalability**: Everything scales together
- **Deployment**: Redeploy everything
- **Technology**: Single stack
- **Teams**: Coordination necessary

## Migration to Microservices

```
Step 1: Monolith -> Modular Monolith
Step 2: Define bounded contexts
Step 3: Strangler Fig (one module at a time)
Step 4: Full Microservices
```

## Anti-patterns

### Module Coupling

```go
// Modules too coupled
type OrderService struct {
	userRepo    *UserRepository    // Direct access
	productRepo *ProductRepository // Direct access
}

// Communication through events/API
type OrderService struct {
	userModule    users.UserModule
	productModule products.ProductModule
}

func (s *OrderService) CreateOrder(ctx context.Context, userID, productID string) (*Order, error) {
	user, err := s.userModule.GetUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	product, err := s.productModule.GetProduct(ctx, productID)
	if err != nil {
		return nil, err
	}

	// Create order
	return nil, nil
}
```

## Related Patterns

| Pattern | Relationship |
|---------|----------|
| Hexagonal | Internal structure of modules |
| CQRS | Applicable per module |
| Event Sourcing | For communication between modules |
| Strangler Fig | Migration to microservices |

## Sources

- [Modular Monolith - Kamil Grzybek](https://www.kamilgrzybek.com/design/modular-monolith-primer/)
- [Martin Fowler - Monolith First](https://martinfowler.com/bliki/MonolithFirst.html)
