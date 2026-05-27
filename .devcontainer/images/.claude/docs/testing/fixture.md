# Test Fixtures

> Shared configuration and data for tests.

## Principle

```
┌─────────────────────────────────────────────────────────────────┐
│                      Test Fixture Lifecycle                      │
│                                                                  │
│   setup ──► beforeEach ──► Test ──► afterEach ──► teardown      │
│     │           │           │           │             │          │
│     ▼           ▼           ▼           ▼             ▼          │
│   Setup once  Reset state Execute   Cleanup       Teardown       │
│   (DB, server) (clear data) test    (rollback)    (close)       │
└─────────────────────────────────────────────────────────────────┘
```

## Fixture Struct Pattern

```go
package integration_test

import (
	"context"
	"database/sql"
	"testing"
)

// TestFixture interface
type TestFixture interface {
	Setup(ctx context.Context) error
	Teardown(ctx context.Context) error
}

// DatabaseFixture manages database test setup
type DatabaseFixture struct {
	DB        *sql.DB
	UserRepo  *UserRepository
	OrderRepo *OrderRepository
}

func NewDatabaseFixture(dbURL string) *DatabaseFixture {
	return &DatabaseFixture{}
}

func (f *DatabaseFixture) Setup(ctx context.Context) error {
	var err error
	f.DB, err = sql.Open("postgres", os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		return err
	}

	if err := f.DB.PingContext(ctx); err != nil {
		return err
	}

	// Run migrations
	if err := runMigrations(f.DB); err != nil {
		return err
	}

	f.UserRepo = NewUserRepository(f.DB)
	f.OrderRepo = NewOrderRepository(f.DB)

	// Seed initial data
	return f.seed(ctx)
}

func (f *DatabaseFixture) Teardown(ctx context.Context) error {
	if f.DB != nil {
		return f.DB.Close()
	}
	return nil
}

func (f *DatabaseFixture) seed(ctx context.Context) error {
	if err := f.UserRepo.Save(ctx, UserMother{}.John()); err != nil {
		return err
	}
	if err := f.UserRepo.Save(ctx, UserMother{}.Jane()); err != nil {
		return err
	}
	return nil
}

func (f *DatabaseFixture) Reset(ctx context.Context) error {
	if err := f.truncateAll(ctx); err != nil {
		return err
	}
	return f.seed(ctx)
}

func (f *DatabaseFixture) truncateAll(ctx context.Context) error {
	tables := []string{"orders", "users"}
	for _, table := range tables {
		_, err := f.DB.ExecContext(ctx, "TRUNCATE TABLE "+table+" CASCADE")
		if err != nil {
			return err
		}
	}
	return nil
}

// Usage
func TestOrderService(t *testing.T) {
	ctx := context.Background()
	fixture := NewDatabaseFixture(os.Getenv("TEST_DATABASE_URL"))

	if err := fixture.Setup(ctx); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	defer fixture.Teardown(ctx)

	t.Run("should create order", func(t *testing.T) {
		if err := fixture.Reset(ctx); err != nil {
			t.Fatalf("reset failed: %v", err)
		}

		service := NewOrderService(fixture.OrderRepo, fixture.UserRepo)
		order, err := service.Create(ctx, "john-id", []OrderItem{{ProductID: "1", Qty: 2}})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if order.Status != "pending" {
			t.Errorf("order.Status = %q; want %q", order.Status, "pending")
		}
	})
}
```

## Composable Fixtures

```go
package integration_test

import (
	"context"
	"net/http"
	"testing"
)

// HTTPFixture manages HTTP server
type HTTPFixture struct {
	Server  *http.Server
	BaseURL string
}

func NewHTTPFixture() *HTTPFixture {
	return &HTTPFixture{}
}

func (f *HTTPFixture) Setup(ctx context.Context) error {
	app := createApp()
	f.Server = &http.Server{
		Addr:    ":0",
		Handler: app,
	}

	listener, err := net.Listen("tcp", f.Server.Addr)
	if err != nil {
		return err
	}

	f.BaseURL = "http://localhost:" + strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)

	go f.Server.Serve(listener)
	return nil
}

func (f *HTTPFixture) Teardown(ctx context.Context) error {
	if f.Server != nil {
		return f.Server.Shutdown(ctx)
	}
	return nil
}

// CompositeFixture combines multiple fixtures
type CompositeFixture struct {
	fixtures []TestFixture
}

func NewCompositeFixture(fixtures ...TestFixture) *CompositeFixture {
	return &CompositeFixture{fixtures: fixtures}
}

func (f *CompositeFixture) Setup(ctx context.Context) error {
	for _, fixture := range f.fixtures {
		if err := fixture.Setup(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (f *CompositeFixture) Teardown(ctx context.Context) error {
	// Teardown in reverse order
	for i := len(f.fixtures) - 1; i >= 0; i-- {
		if err := f.fixtures[i].Teardown(ctx); err != nil {
			return err
		}
	}
	return nil
}

// Usage
func TestIntegration(t *testing.T) {
	ctx := context.Background()

	dbFixture := NewDatabaseFixture(os.Getenv("TEST_DATABASE_URL"))
	httpFixture := NewHTTPFixture()
	fixture := NewCompositeFixture(dbFixture, httpFixture)

	if err := fixture.Setup(ctx); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	defer fixture.Teardown(ctx)

	t.Run("API should return users", func(t *testing.T) {
		resp, err := http.Get(httpFixture.BaseURL + "/users")
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d; want %d", resp.StatusCode, http.StatusOK)
		}
	})
}
```

## Embedded Test Data

```go
package testdata

import (
	"embed"
	"encoding/json"
)

//go:embed fixtures/*.json
var fixturesFS embed.FS

// FixtureLoader loads test data from embedded files
type FixtureLoader struct{}

func NewFixtureLoader() *FixtureLoader {
	return &FixtureLoader{}
}

func (l *FixtureLoader) Load(name string, v interface{}) error {
	data, err := fixturesFS.ReadFile("fixtures/" + name + ".json")
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func (l *FixtureLoader) LoadUsers() ([]*User, error) {
	var users []*User
	if err := l.Load("users", &users); err != nil {
		return nil, err
	}
	return users, nil
}

func (l *FixtureLoader) LoadOrders() ([]*Order, error) {
	var orders []*Order
	if err := l.Load("orders", &orders); err != nil {
		return nil, err
	}
	return orders, nil
}

// Usage
func TestWithJSONFixtures(t *testing.T) {
	loader := NewFixtureLoader()

	users, err := loader.LoadUsers()
	if err != nil {
		t.Fatalf("failed to load users: %v", err)
	}

	if len(users) != 2 {
		t.Errorf("len(users) = %d; want 2", len(users))
	}
	if users[0].Name != "John Doe" {
		t.Errorf("users[0].Name = %q; want %q", users[0].Name, "John Doe")
	}
}
```

## Scoped Fixtures (Per-test cleanup)

```go
package user_test

import (
	"context"
	"testing"
)

// ScopedFixture manages per-test resources
type ScopedFixture[T any] struct {
	data    T
	cleanup func() error
}

func NewScopedFixture[T any]() *ScopedFixture[T] {
	return &ScopedFixture[T]{}
}

func (f *ScopedFixture[T]) Use(factory func() (T, func() error, error)) (T, error) {
	data, cleanup, err := factory()
	if err != nil {
		return *new(T), err
	}
	f.data = data
	f.cleanup = cleanup
	return data, nil
}

func (f *ScopedFixture[T]) Get() T {
	return f.data
}

func (f *ScopedFixture[T]) Dispose() error {
	if f.cleanup != nil {
		return f.cleanup()
	}
	return nil
}

// Usage
func TestOrderService(t *testing.T) {
	ctx := context.Background()
	userFixture := NewScopedFixture[*User]()
	defer userFixture.Dispose()

	t.Run("admin can delete orders", func(t *testing.T) {
		user, err := userFixture.Use(func() (*User, func() error, error) {
			u, err := createUser(ctx, &User{Role: "admin"})
			if err != nil {
				return nil, nil, err
			}
			cleanup := func() error {
				return deleteUser(ctx, u.ID)
			}
			return u, cleanup, nil
		})
		if err != nil {
			t.Fatalf("failed to create user: %v", err)
		}

		service := NewOrderService()
		err = service.DeleteOrder(ctx, "order-1", user)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		order, err := service.GetOrder(ctx, "order-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if order != nil {
			t.Errorf("order should be deleted")
		}
	})

	t.Run("member cannot delete orders", func(t *testing.T) {
		user, err := userFixture.Use(func() (*User, func() error, error) {
			u, err := createUser(ctx, &User{Role: "member"})
			if err != nil {
				return nil, nil, err
			}
			cleanup := func() error {
				return deleteUser(ctx, u.ID)
			}
			return u, cleanup, nil
		})
		if err != nil {
			t.Fatalf("failed to create user: %v", err)
		}

		service := NewOrderService()
		err = service.DeleteOrder(ctx, "order-1", user)

		if err == nil {
			t.Errorf("expected error; got nil")
		}
	})
}
```

## Transaction Rollback Fixture

```go
package repo_test

import (
	"context"
	"database/sql"
	"testing"
)

// TransactionFixture manages test transactions
type TransactionFixture struct {
	db *sql.DB
	tx *sql.Tx
}

func NewTransactionFixture(dbURL string) *TransactionFixture {
	return &TransactionFixture{}
}

func (f *TransactionFixture) Setup(ctx context.Context) error {
	var err error
	f.db, err = sql.Open("postgres", os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		return err
	}

	f.tx, err = f.db.BeginTx(ctx, nil)
	return err
}

func (f *TransactionFixture) Teardown(ctx context.Context) error {
	if f.tx != nil {
		f.tx.Rollback() // Always rollback
	}
	if f.db != nil {
		return f.db.Close()
	}
	return nil
}

func (f *TransactionFixture) GetDB() *sql.Tx {
	return f.tx
}

// Usage - Each test is isolated via transaction rollback
func TestUserRepository(t *testing.T) {
	ctx := context.Background()
	fixture := NewTransactionFixture(os.Getenv("TEST_DATABASE_URL"))

	if err := fixture.Setup(ctx); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	defer fixture.Teardown(ctx)

	t.Run("should create user", func(t *testing.T) {
		repo := NewUserRepository(fixture.GetDB())
		err := repo.Save(ctx, &User{ID: "1", Name: "Test"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		user, err := repo.FindByID(ctx, "1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if user.Name != "Test" {
			t.Errorf("user.Name = %q; want %q", user.Name, "Test")
		}
		// Transaction will be rolled back - no cleanup needed
	})
}
```

## TestMain Pattern

```go
package integration_test

import (
	"context"
	"os"
	"testing"
)

var globalFixture *DatabaseFixture

func TestMain(m *testing.M) {
	ctx := context.Background()

	// Setup
	globalFixture = NewDatabaseFixture(os.Getenv("TEST_DATABASE_URL"))
	if err := globalFixture.Setup(ctx); err != nil {
		panic(err)
	}

	// Run tests
	code := m.Run()

	// Teardown
	if err := globalFixture.Teardown(ctx); err != nil {
		panic(err)
	}

	os.Exit(code)
}

func TestWithGlobalFixture(t *testing.T) {
	ctx := context.Background()

	// Reset before each test
	if err := globalFixture.Reset(ctx); err != nil {
		t.Fatalf("reset failed: %v", err)
	}

	// Use globalFixture...
}
```

## Recommended Libraries

| Package | Usage |
|---------|-------|
| `testing` | Built-in testing package |
| `github.com/testcontainers/testcontainers-go` | Container-based fixtures |
| `github.com/stretchr/testify/suite` | Test suite pattern |

## Common Mistakes

| Mistake | Impact | Solution |
|---------|--------|----------|
| Setup in each test | Slow tests | TestMain for expensive setup |
| No cleanup | State leak between tests | defer Teardown() |
| Shared mutable fixtures | Dependent tests | Reset or deep copy |
| Test order matters | Flaky tests | Complete isolation |
| Oversized fixtures | Slow tests | Minimal fixtures per suite |

## When to Use

| Scenario | Fixture Type |
|----------|-----------------|
| Database tests | Transaction rollback |
| API tests | HTTP server fixture |
| Complex object graphs | Embedded JSON fixtures |
| Per-test isolation | Scoped fixtures |
| Shared expensive resources | TestMain setup |

## Related Patterns

- **Object Mother**: Factory for fixture objects
- **Test Data Builder**: Fluent fixture construction
- **Test Containers**: Fixtures with Docker containers

## Sources

- [xUnit Test Patterns - Fixtures](http://xunitpatterns.com/test%20fixture%20-%20xUnit.html)
- [Go Testing Documentation](https://pkg.go.dev/testing)
