# Hexagonal Architecture (Ports & Adapters)

> Isolate the business core from technical details.

**Author:** Alistair Cockburn (2005)

## Principle

```
                    ┌─────────────────────────────────────┐
                    │           ADAPTERS (Driving)         │
                    │  REST API │ CLI │ gRPC │ GraphQL    │
                    └─────────────────────────────────────┘
                                      │
                                      ▼
                    ┌─────────────────────────────────────┐
                    │             PORTS (In)               │
                    │        Input interfaces              │
                    └─────────────────────────────────────┘
                                      │
                                      ▼
          ┌───────────────────────────────────────────────────────┐
          │                                                       │
          │                    DOMAIN CORE                        │
          │                                                       │
          │   ┌─────────────┐   ┌─────────────┐   ┌───────────┐  │
          │   │   Entities  │   │   Services  │   │   Rules   │  │
          │   └─────────────┘   └─────────────┘   └───────────┘  │
          │                                                       │
          └───────────────────────────────────────────────────────┘
                                      │
                                      ▼
                    ┌─────────────────────────────────────┐
                    │             PORTS (Out)              │
                    │        Output interfaces             │
                    └─────────────────────────────────────┘
                                      │
                                      ▼
                    ┌─────────────────────────────────────┐
                    │          ADAPTERS (Driven)           │
                    │  PostgreSQL │ Redis │ S3 │ Email    │
                    └─────────────────────────────────────┘
```

## File Structure

```
src/
├── domain/                    # Business core (NO external dependencies)
│   ├── entities/
│   │   └── User.go
│   ├── services/
│   │   └── UserService.go
│   ├── repositories/          # Interfaces (Ports Out)
│   │   └── UserRepository.go
│   └── errors/
│       └── UserNotFoundError.go
│
├── application/               # Use Cases / Ports In
│   ├── commands/
│   │   └── CreateUserCommand.go
│   ├── queries/
│   │   └── GetUserQuery.go
│   └── handlers/
│       └── CreateUserHandler.go
│
├── infrastructure/            # Adapters (implementations)
│   ├── persistence/
│   │   ├── PostgresUserRepository.go
│   │   └── InMemoryUserRepository.go
│   ├── http/
│   │   └── UserController.go
│   └── messaging/
│       └── RabbitMQPublisher.go
│
└── main.go                    # Composition root (DI)
```

## Example

### Port (Interface)

```go
package repositories

import "context"

// UserRepository is the port for user persistence.
type UserRepository interface {
	FindByID(ctx context.Context, id string) (*User, error)
	Save(ctx context.Context, user *User) error
	Delete(ctx context.Context, id string) error
}
```

### Domain Service

```go
package services

import (
	"context"
	"fmt"
)

// UserService handles user business logic.
type UserService struct {
	userRepo UserRepository
}

// NewUserService creates a new user service.
func NewUserService(userRepo UserRepository) *UserService {
	return &UserService{userRepo: userRepo}
}

// CreateUser creates a new user.
func (s *UserService) CreateUser(ctx context.Context, email, name string) (*User, error) {
	existing, err := s.userRepo.FindByEmail(ctx, email)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("finding user by email: %w", err)
	}
	if existing != nil {
		return nil, &UserAlreadyExistsError{Email: email}
	}

	user := &User{
		ID:    GenerateID(),
		Email: email,
		Name:  name,
	}

	if err := s.userRepo.Save(ctx, user); err != nil {
		return nil, fmt.Errorf("saving user: %w", err)
	}

	return user, nil
}
```

### Adapter (Implementation)

```go
package persistence

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/lib/pq"
)

// PostgresUserRepository is the PostgreSQL adapter for UserRepository.
type PostgresUserRepository struct {
	db *sql.DB
}

// NewPostgresUserRepository creates a new PostgreSQL user repository.
func NewPostgresUserRepository(db *sql.DB) *PostgresUserRepository {
	return &PostgresUserRepository{db: db}
}

// FindByID finds a user by ID.
func (r *PostgresUserRepository) FindByID(ctx context.Context, id string) (*User, error) {
	query := "SELECT id, email, name FROM users WHERE id = $1"

	var user User
	err := r.db.QueryRowContext(ctx, query, id).Scan(&user.ID, &user.Email, &user.Name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("querying user: %w", err)
	}

	return &user, nil
}

// Save saves a user.
func (r *PostgresUserRepository) Save(ctx context.Context, user *User) error {
	query := "INSERT INTO users (id, email, name) VALUES ($1, $2, $3)"

	_, err := r.db.ExecContext(ctx, query, user.ID, user.Email, user.Name)
	if err != nil {
		return fmt.Errorf("inserting user: %w", err)
	}

	return nil
}
```

### Test (with Mock Adapter)

```go
package services_test

import (
	"context"
	"testing"
)

// InMemoryUserRepository is a mock repository for testing.
type InMemoryUserRepository struct {
	users map[string]*User
}

// NewInMemoryUserRepository creates a new in-memory user repository.
func NewInMemoryUserRepository() *InMemoryUserRepository {
	return &InMemoryUserRepository{
		users: make(map[string]*User),
	}
}

// FindByID finds a user by ID.
func (r *InMemoryUserRepository) FindByID(ctx context.Context, id string) (*User, error) {
	user, ok := r.users[id]
	if !ok {
		return nil, nil
	}
	return user, nil
}

// Save saves a user.
func (r *InMemoryUserRepository) Save(ctx context.Context, user *User) error {
	r.users[user.ID] = user
	return nil
}

func TestUserService_CreateUser(t *testing.T) {
	mockRepo := NewInMemoryUserRepository()
	service := NewUserService(mockRepo)

	user, err := service.CreateUser(context.Background(), "test@example.com", "Test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if user.Email != "test@example.com" {
		t.Errorf("expected email test@example.com, got %s", user.Email)
	}

	found, err := mockRepo.FindByID(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found == nil {
		t.Error("user not found in repository")
	}
}
```

## When to Use

| Use | Avoid |
|-------------|-----------|
| Complex business applications | Simple CRUD |
| Long-lived applications | Prototypes/MVPs |
| Important tests | One-shot scripts |
| Multiple teams | Short solo projects |
| Foreseeable infra changes | Fixed stack |

## Advantages

- **Testability**: Domain testable without DB/HTTP
- **Flexibility**: Changing DB = one adapter
- **Clarity**: Clear separation of responsibilities
- **Independence**: Business depends on nothing

## Disadvantages

- **Verbosity**: More files/interfaces
- **Overhead**: Mapping between layers
- **Learning curve**: Concepts to master

## Related Patterns

| Pattern | Relationship |
|---------|----------|
| Clean Architecture | Evolution with more layers |
| DIP (SOLID) | Foundation of the pattern |
| Adapter (GoF) | Implementation of ports |
| Repository | Typical port for persistence |

## Frameworks Supporting Hexagonal

| Language | Framework |
|---------|-----------|
| TypeScript | NestJS, ts-arch |
| Java | Spring (modules) |
| Go | go-kit, manual structure |
| Python | FastAPI + manual structure |

## Sources

- [Alistair Cockburn - Hexagonal Architecture](https://alistair.cockburn.us/hexagonal-architecture/)
- [Netflix Tech Blog](https://netflixtechblog.com/)
- [microservices.io](https://microservices.io/patterns/microservices.html)
