# Layered Architecture (N-Tier)

> Organize code into horizontal layers with distinct responsibilities.

**Also called:** N-Tier, Multi-tier, Onion (variant)

## Principle

```
┌─────────────────────────────────────────────────────────────────┐
│                    LAYERED ARCHITECTURE                          │
│                                                                  │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │                  PRESENTATION LAYER                      │    │
│  │              (Controllers, Views, APIs)                  │    │
│  │                                                          │    │
│  │  • Handles HTTP requests                                │    │
│  │  • Validates inputs                                     │    │
│  │  • Formats responses                                    │    │
│  └──────────────────────────┬──────────────────────────────┘    │
│                             │ Depends on                         │
│                             ▼                                    │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │                   BUSINESS LAYER                         │    │
│  │              (Services, Use Cases, Logic)                │    │
│  │                                                          │    │
│  │  • Business logic                                       │    │
│  │  • Validation rules                                     │    │
│  │  • Orchestration                                        │    │
│  └──────────────────────────┬──────────────────────────────┘    │
│                             │ Depends on                         │
│                             ▼                                    │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │                 PERSISTENCE LAYER                        │    │
│  │              (Repositories, DAOs, ORM)                   │    │
│  │                                                          │    │
│  │  • Data access                                          │    │
│  │  • Object-relational mapping                            │    │
│  │  • Queries                                              │    │
│  └──────────────────────────┬──────────────────────────────┘    │
│                             │ Depends on                         │
│                             ▼                                    │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │                   DATABASE LAYER                         │    │
│  │              (PostgreSQL, MongoDB, Redis)                │    │
│  └─────────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────┘

Rule: A layer can only call the layer immediately below
```

## File Structure

```
src/
├── presentation/                # Presentation layer
│   ├── controllers/
│   │   ├── UserController.go
│   │   └── OrderController.go
│   ├── middleware/
│   │   ├── AuthMiddleware.go
│   │   └── ValidationMiddleware.go
│   ├── dto/                     # Data Transfer Objects
│   │   ├── CreateUserDTO.go
│   │   └── OrderResponseDTO.go
│   └── routes/
│       └── routes.go
│
├── business/                    # Business layer
│   ├── services/
│   │   ├── UserService.go
│   │   └── OrderService.go
│   ├── validators/
│   │   └── OrderValidator.go
│   └── rules/
│       └── PricingRules.go
│
├── persistence/                 # Persistence layer
│   ├── repositories/
│   │   ├── UserRepository.go
│   │   └── OrderRepository.go
│   ├── entities/
│   │   ├── UserEntity.go
│   │   └── OrderEntity.go
│   └── migrations/
│       └── ...
│
└── shared/                      # Cross-cutting concerns
    ├── config/
    ├── utils/
    └── types/
```

## Implementation

### Presentation Layer

```go
package controllers

import (
	"encoding/json"
	"net/http"
)

// CreateUserDTO is the data transfer object for creating a user.
type CreateUserDTO struct {
	Email string `json:"email"`
	Name  string `json:"name"`
}

// UserResponseDTO is the data transfer object for user responses.
type UserResponseDTO struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

// UserController handles user HTTP requests.
type UserController struct {
	userService UserService
}

// NewUserController creates a new user controller.
func NewUserController(userService UserService) *UserController {
	return &UserController{userService: userService}
}

// Create handles user creation requests.
func (c *UserController) Create(w http.ResponseWriter, r *http.Request) {
	var dto CreateUserDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	user, err := c.userService.CreateUser(r.Context(), dto)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	response := UserResponseDTO{
		ID:    user.ID,
		Email: user.Email,
		Name:  user.Name,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteStatus(http.StatusCreated)
	json.NewEncoder(w).Encode(response)
}

// GetByID handles get user by ID requests.
func (c *UserController) GetByID(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")

	user, err := c.userService.GetUserByID(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if user == nil {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}

	response := UserResponseDTO{
		ID:    user.ID,
		Email: user.Email,
		Name:  user.Name,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
```

### Business Layer

```go
package services

import (
	"context"
	"fmt"
	"time"
)

// User represents a user entity.
type User struct {
	ID        string
	Email     string
	Name      string
	CreatedAt time.Time
}

// UserService handles user business logic.
type UserService struct {
	userRepository UserRepository
	emailService   EmailService
}

// NewUserService creates a new user service.
func NewUserService(userRepository UserRepository, emailService EmailService) *UserService {
	return &UserService{
		userRepository: userRepository,
		emailService:   emailService,
	}
}

// CreateUser creates a new user.
func (s *UserService) CreateUser(ctx context.Context, dto CreateUserDTO) (*User, error) {
	// Business validation
	if err := s.validateEmail(ctx, dto.Email); err != nil {
		return nil, err
	}

	user := &User{
		ID:        GenerateID(),
		Email:     dto.Email,
		Name:      dto.Name,
		CreatedAt: time.Now(),
	}

	// Persistence
	if err := s.userRepository.Save(ctx, user); err != nil {
		return nil, fmt.Errorf("saving user: %w", err)
	}

	// Side effects
	if err := s.emailService.SendWelcome(ctx, user.Email); err != nil {
		// Log error but don't fail
		fmt.Printf("failed to send welcome email: %v\n", err)
	}

	return user, nil
}

// GetUserByID retrieves a user by ID.
func (s *UserService) GetUserByID(ctx context.Context, id string) (*User, error) {
	return s.userRepository.FindByID(ctx, id)
}

func (s *UserService) validateEmail(ctx context.Context, email string) error {
	existing, err := s.userRepository.FindByEmail(ctx, email)
	if err != nil {
		return fmt.Errorf("finding user by email: %w", err)
	}
	if existing != nil {
		return &DuplicateEmailError{Email: email}
	}
	return nil
}
```

### Persistence Layer

```go
package repositories

import (
	"context"
	"database/sql"
	"fmt"
)

// UserRepository handles user data access.
type UserRepository struct {
	db *sql.DB
}

// NewUserRepository creates a new user repository.
func NewUserRepository(db *sql.DB) *UserRepository {
	return &UserRepository{db: db}
}

// Save saves a user to the database.
func (r *UserRepository) Save(ctx context.Context, user *User) error {
	query := `
		INSERT INTO users (id, email, name, created_at)
		VALUES ($1, $2, $3, $4)
	`

	_, err := r.db.ExecContext(ctx, query, user.ID, user.Email, user.Name, user.CreatedAt)
	if err != nil {
		return fmt.Errorf("executing insert: %w", err)
	}

	return nil
}

// FindByID finds a user by ID.
func (r *UserRepository) FindByID(ctx context.Context, id string) (*User, error) {
	query := "SELECT id, email, name, created_at FROM users WHERE id = $1"

	var user User
	err := r.db.QueryRowContext(ctx, query, id).Scan(&user.ID, &user.Email, &user.Name, &user.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("querying user: %w", err)
	}

	return &user, nil
}

// FindByEmail finds a user by email.
func (r *UserRepository) FindByEmail(ctx context.Context, email string) (*User, error) {
	query := "SELECT id, email, name, created_at FROM users WHERE email = $1"

	var user User
	err := r.db.QueryRowContext(ctx, query, email).Scan(&user.ID, &user.Email, &user.Name, &user.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("querying user: %w", err)
	}

	return &user, nil
}
```

## Variants

### Classic 3-Tier

```
┌───────────────────┐
│   Presentation    │  UI / API
├───────────────────┤
│     Business      │  Business logic
├───────────────────┤
│       Data        │  Database
└───────────────────┘
```

### 4-Tier with Integration

```
┌───────────────────┐
│   Presentation    │  UI / API
├───────────────────┤
│     Business      │  Business logic
├───────────────────┤
│   Integration     │  External APIs, messaging
├───────────────────┤
│       Data        │  Database
└───────────────────┘
```

### Onion / Clean Architecture

```
        ┌───────────────────────────────────┐
        │           Infrastructure          │
        │  ┌───────────────────────────┐   │
        │  │       Application         │   │
        │  │  ┌───────────────────┐   │   │
        │  │  │      Domain       │   │   │
        │  │  │                   │   │   │
        │  │  │   (Entities)      │   │   │
        │  │  │                   │   │   │
        │  │  └───────────────────┘   │   │
        │  │    (Use Cases)           │   │
        │  └───────────────────────────┘   │
        │  (DB, Web, External Services)    │
        └───────────────────────────────────┘

Dependencies: toward the center (Domain)
```

## When to Use

| Use | Avoid |
|----------|--------|
| CRUD applications | Very complex domain |
| Traditional teams | Microservices |
| Simple APIs | High performance |
| Evolvable prototypes | Horizontal scaling |
| Classic web applications | Event-driven |

## Advantages

- **Simplicity**: Easy to understand
- **Separation**: Clear responsibilities
- **Testability**: Isolatable layers
- **Maintainability**: Localized changes
- **Standard**: Well-known pattern

## Disadvantages

- **Overhead**: Mapping between layers
- **Rigidity**: Sometimes constraining structure
- **Performance**: Layer traversal
- **Coupling**: Downward dependencies
- **Monolith**: Tendency toward monolith

## Real-world Examples

| Framework | Architecture |
|-----------|--------------|
| **Spring MVC** | Controller-Service-Repository |
| **ASP.NET MVC** | Controller-Service-Data |
| **Django** | Views-Models-Templates |
| **Rails** | Traditional MVC |
| **NestJS** | Controller-Service-Repository |

## Migration Path

### To Hexagonal

```
1. Extract repository interfaces
2. Invert dependencies (DIP)
3. Create a real Domain layer
4. Separate ports (interfaces) and adapters (impl)
```

### To Microservices

```
1. Identify bounded contexts
2. Separate into independent modules
3. Extract into services
4. Replace calls with API/Events
```

## Related Patterns

| Pattern | Relationship |
|---------|----------|
| Hexagonal | Evolution with dependency inversion |
| Clean Architecture | Variant with circles |
| MVC | Presentation sub-pattern |
| Repository | Data layer pattern |

## Sources

- [Martin Fowler - PresentationDomainDataLayering](https://martinfowler.com/bliki/PresentationDomainDataLayering.html)
- [Microsoft - N-tier Architecture](https://docs.microsoft.com/en-us/azure/architecture/guide/architecture-styles/n-tier)
- [Clean Architecture - Robert C. Martin](https://blog.cleancoder.com/uncle-bob/2012/08/13/the-clean-architecture.html)
