# Test Containers

> Real infrastructure in Docker containers for integration tests.

## Principle

```
┌─────────────────────────────────────────────────────────────────┐
│                    Testcontainers Architecture                   │
│                                                                  │
│   Test Suite                                                     │
│       │                                                          │
│       ├── setup: Start containers                               │
│       │       │                                                  │
│       │       ▼                                                  │
│       │   ┌─────────────┐  ┌─────────────┐  ┌─────────────┐    │
│       │   │  PostgreSQL │  │    Redis    │  │    Kafka    │    │
│       │   │  :5432      │  │  :6379      │  │  :9092      │    │
│       │   └─────────────┘  └─────────────┘  └─────────────┘    │
│       │                                                          │
│       ├── Tests run against real infrastructure                 │
│       │                                                          │
│       └── cleanup: Stop and cleanup containers                  │
└─────────────────────────────────────────────────────────────────┘
```

## Basic Setup

```go
package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestDatabaseIntegration(t *testing.T) {
	ctx := context.Background()

	// Start PostgreSQL container
	req := testcontainers.ContainerRequest{
		Image:        "postgres:15",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_USER":     "test",
			"POSTGRES_PASSWORD": "test",
			"POSTGRES_DB":       "testdb",
		},
		WaitingFor: wait.ForLog("database system is ready to accept connections").
			WithOccurrence(2).
			WithStartupTimeout(60 * time.Second),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("failed to start container: %v", err)
	}
	defer container.Terminate(ctx)

	// Get connection details
	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("failed to get host: %v", err)
	}

	port, err := container.MappedPort(ctx, "5432")
	if err != nil {
		t.Fatalf("failed to get port: %v", err)
	}

	// Connect to the container
	db, err := Database.Connect(DatabaseConfig{
		Host:     host,
		Port:     port.Int(),
		User:     "test",
		Password: "test",
		Database: "testdb",
	})
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer db.Close()

	// Run migrations
	if err := runMigrations(db); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	// Test
	repo := NewUserRepository(db)
	err = repo.Save(ctx, &User{
		ID:    "1",
		Name:  "John",
		Email: "john@example.com",
	})
	if err != nil {
		t.Fatalf("failed to save user: %v", err)
	}

	user, err := repo.FindByID(ctx, "1")
	if err != nil {
		t.Fatalf("failed to find user: %v", err)
	}
	if user.Name != "John" {
		t.Errorf("user.Name = %q; want %q", user.Name, "John")
	}
}
```

## Pre-built Modules

```go
package integration_test

import (
	"context"
	"testing"

	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/modules/kafka"
	"github.com/testcontainers/testcontainers-go/modules/mongodb"
)

func TestWithPostgres(t *testing.T) {
	ctx := context.Background()

	// PostgreSQL
	postgresContainer, err := postgres.RunContainer(ctx,
		testcontainers.WithImage("postgres:15"),
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
	)
	if err != nil {
		t.Fatalf("failed to start postgres: %v", err)
	}
	defer postgresContainer.Terminate(ctx)

	connStr, err := postgresContainer.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("failed to get connection string: %v", err)
	}

	// Use connStr...
	_ = connStr
}

func TestWithRedis(t *testing.T) {
	ctx := context.Background()

	// Redis
	redisContainer, err := redis.RunContainer(ctx,
		testcontainers.WithImage("redis:7"),
	)
	if err != nil {
		t.Fatalf("failed to start redis: %v", err)
	}
	defer redisContainer.Terminate(ctx)

	endpoint, err := redisContainer.Endpoint(ctx, "")
	if err != nil {
		t.Fatalf("failed to get endpoint: %v", err)
	}

	// Use endpoint...
	_ = endpoint
}

func TestWithMongoDB(t *testing.T) {
	ctx := context.Background()

	// MongoDB
	mongoContainer, err := mongodb.RunContainer(ctx,
		testcontainers.WithImage("mongo:6"),
	)
	if err != nil {
		t.Fatalf("failed to start mongodb: %v", err)
	}
	defer mongoContainer.Terminate(ctx)

	connStr, err := mongoContainer.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("failed to get connection string: %v", err)
	}

	// Use connStr...
	_ = connStr
}

func TestWithKafka(t *testing.T) {
	ctx := context.Background()

	// Kafka
	kafkaContainer, err := kafka.RunContainer(ctx,
		testcontainers.WithImage("confluentinc/cp-kafka:7.5.0"),
		kafka.WithClusterID("test-cluster"),
	)
	if err != nil {
		t.Fatalf("failed to start kafka: %v", err)
	}
	defer kafkaContainer.Terminate(ctx)

	brokers, err := kafkaContainer.Brokers(ctx)
	if err != nil {
		t.Fatalf("failed to get brokers: %v", err)
	}

	// Use brokers...
	_ = brokers
}
```

## Docker Compose

```go
package integration_test

import (
	"context"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestFullStackIntegration(t *testing.T) {
	ctx := context.Background()

	compose := testcontainers.NewLocalDockerCompose(
		[]string{"docker-compose.test.yml"},
		"test-project",
	)

	err := compose.
		WithCommand([]string{"up", "-d"}).
		Invoke()
	if err != nil {
		t.Fatalf("failed to start compose: %v", err)
	}
	defer compose.Down()

	// Wait for services to be ready
	apiContainer := compose.GetContainerByName("api")
	host, _ := apiContainer.Host(ctx)
	port, _ := apiContainer.MappedPort(ctx, "3000")

	// Test API
	resp, err := http.Get(fmt.Sprintf("http://%s:%s/health", host, port.Port()))
	if err != nil {
		t.Fatalf("failed to call API: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d; want %d", resp.StatusCode, http.StatusOK)
	}
}
```

## Reusable Containers

```go
package testutil

import (
	"context"
	"sync"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// ContainerManager manages reusable test containers
type ContainerManager struct {
	mu       sync.Mutex
	postgres testcontainers.Container
	redis    testcontainers.Container
}

var manager = &ContainerManager{}

// GetPostgres returns a reusable PostgreSQL container
func (m *ContainerManager) GetPostgres(ctx context.Context) (testcontainers.Container, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.postgres != nil {
		return m.postgres, nil
	}

	container, err := postgres.RunContainer(ctx,
		testcontainers.WithImage("postgres:15"),
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithReuse(true),
	)
	if err != nil {
		return nil, err
	}

	m.postgres = container
	return container, nil
}

// GetRedis returns a reusable Redis container
func (m *ContainerManager) GetRedis(ctx context.Context) (testcontainers.Container, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.redis != nil {
		return m.redis, nil
	}

	container, err := redis.RunContainer(ctx,
		testcontainers.WithImage("redis:7"),
		testcontainers.WithReuse(true),
	)
	if err != nil {
		return nil, err
	}

	m.redis = container
	return container, nil
}

// StopAll stops all containers
func (m *ContainerManager) StopAll(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var errs []error
	if m.postgres != nil {
		if err := m.postgres.Terminate(ctx); err != nil {
			errs = append(errs, err)
		}
		m.postgres = nil
	}
	if m.redis != nil {
		if err := m.redis.Terminate(ctx); err != nil {
			errs = append(errs, err)
		}
		m.redis = nil
	}

	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// Usage in TestMain
func TestMain(m *testing.M) {
	ctx := context.Background()

	// Start reusable containers
	_, _ = manager.GetPostgres(ctx)
	_, _ = manager.GetRedis(ctx)

	code := m.Run()

	// Cleanup
	_ = manager.StopAll(ctx)

	os.Exit(code)
}
```

## Wait Strategies

```go
package integration_test

import (
	"time"

	"github.com/testcontainers/testcontainers-go/wait"
)

// Wait for log message
req := testcontainers.ContainerRequest{
	Image: "custom-service",
	WaitingFor: wait.ForLog("Server started").
		WithStartupTimeout(30 * time.Second),
}

// Wait for HTTP endpoint
req := testcontainers.ContainerRequest{
	Image:        "api-service",
	ExposedPorts: []string{"8080/tcp"},
	WaitingFor: wait.ForHTTP("/health").
		WithPort("8080/tcp").
		WithStatusCodeMatcher(func(status int) bool {
			return status == 200
		}).
		WithStartupTimeout(60 * time.Second),
}

// Wait for listening ports
req := testcontainers.ContainerRequest{
	Image:        "service",
	ExposedPorts: []string{"8080/tcp"},
	WaitingFor:   wait.ForListeningPort("8080/tcp"),
}

// Wait for healthcheck
req := testcontainers.ContainerRequest{
	Image: "service",
	HealthCheck: testcontainers.HealthCheck{
		Test:     []string{"CMD", "curl", "-f", "http://localhost:8080/health"},
		Interval: 1 * time.Second,
		Timeout:  3 * time.Second,
		Retries:  5,
	},
	WaitingFor: wait.ForHealthCheck().
		WithStartupTimeout(30 * time.Second),
}

// Combined wait strategies
req := testcontainers.ContainerRequest{
	Image: "service",
	WaitingFor: wait.ForAll(
		wait.ForListeningPort("8080/tcp"),
		wait.ForLog("Ready"),
	).WithDeadline(60 * time.Second),
}
```

## Network and Volume

```go
package integration_test

import (
	"context"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
)

func TestMultiContainerSetup(t *testing.T) {
	ctx := context.Background()

	// Create network
	net, err := network.New(ctx)
	if err != nil {
		t.Fatalf("failed to create network: %v", err)
	}
	defer net.Remove(ctx)

	// Start database with network alias
	dbReq := testcontainers.ContainerRequest{
		Image:        "postgres:15",
		Networks:     []string{net.Name},
		NetworkAliases: map[string][]string{
			net.Name: {"database"},
		},
		Env: map[string]string{
			"POSTGRES_PASSWORD": "test",
		},
	}

	db, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: dbReq,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("failed to start db: %v", err)
	}
	defer db.Terminate(ctx)

	// Start API connected to database
	apiReq := testcontainers.ContainerRequest{
		Image:        "my-api:test",
		Networks:     []string{net.Name},
		ExposedPorts: []string{"3000/tcp"},
		Env: map[string]string{
			"DATABASE_URL": "postgres://postgres:test@database:5432/postgres",
		},
	}

	api, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: apiReq,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("failed to start api: %v", err)
	}
	defer api.Terminate(ctx)

	// Test API
	host, _ := api.Host(ctx)
	port, _ := api.MappedPort(ctx, "3000")

	resp, err := http.Get(fmt.Sprintf("http://%s:%s/users", host, port.Port()))
	if err != nil {
		t.Fatalf("failed to call API: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d; want %d", resp.StatusCode, http.StatusOK)
	}
}

// With volumes
func TestWithVolumes(t *testing.T) {
	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		Image: "my-app",
		Mounts: testcontainers.Mounts(
			testcontainers.BindMount("./fixtures", "/data"),
		),
		Tmpfs: map[string]string{
			"/tmp": "rw,noexec",
		},
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("failed to start container: %v", err)
	}
	defer container.Terminate(ctx)
}
```

## Test Isolation

```go
package user_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// Per-test database isolation with transactions
func TestUserService(t *testing.T) {
	ctx := context.Background()

	// Start container once for all tests
	container, err := postgres.RunContainer(ctx,
		testcontainers.WithImage("postgres:15"),
		postgres.WithDatabase("testdb"),
	)
	if err != nil {
		t.Fatalf("failed to start postgres: %v", err)
	}
	defer container.Terminate(ctx)

	connStr, err := container.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("failed to get connection string: %v", err)
	}

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()

	// Run migrations
	if err := runMigrations(db); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	t.Run("creates user", func(t *testing.T) {
		tx, err := db.Begin()
		if err != nil {
			t.Fatalf("failed to begin tx: %v", err)
		}
		defer tx.Rollback()

		repo := NewUserRepository(tx)
		err = repo.Create(ctx, &User{Name: "John"})
		if err != nil {
			t.Fatalf("failed to create user: %v", err)
		}

		users, err := repo.FindAll(ctx)
		if err != nil {
			t.Fatalf("failed to find users: %v", err)
		}
		if len(users) != 1 {
			t.Errorf("len(users) = %d; want 1", len(users))
		}
	})

	t.Run("isolated from other tests", func(t *testing.T) {
		tx, err := db.Begin()
		if err != nil {
			t.Fatalf("failed to begin tx: %v", err)
		}
		defer tx.Rollback()

		repo := NewUserRepository(tx)
		users, err := repo.FindAll(ctx)
		if err != nil {
			t.Fatalf("failed to find users: %v", err)
		}

		// Previous test's data was rolled back
		if len(users) != 0 {
			t.Errorf("len(users) = %d; want 0", len(users))
		}
	})
}
```

## Recommended Libraries

| Package | Usage |
|---------|-------|
| `github.com/testcontainers/testcontainers-go` | Core library |
| `github.com/testcontainers/testcontainers-go/modules/postgres` | PostgreSQL module |
| `github.com/testcontainers/testcontainers-go/modules/redis` | Redis module |
| `github.com/testcontainers/testcontainers-go/modules/kafka` | Kafka module |
| `github.com/testcontainers/testcontainers-go/modules/mongodb` | MongoDB module |

## Common Mistakes

| Mistake | Impact | Solution |
|---------|--------|----------|
| Too short timeout | Tests fail at startup | Increase wait strategy timeout |
| No cleanup | Resource leak | defer container.Terminate() |
| Port conflicts | Tests fail | Use dynamic ports |
| Slow tests | Slow CI | Container reuse |
| No wait strategy | Connection errors | Proper wait strategies |

## When to Use

| Scenario | Recommended |
|----------|------------|
| DB integration tests | Yes |
| Tests with message queues | Yes |
| Local E2E tests | Yes |
| Unit tests | No (overkill) |
| CI without Docker | Not possible |

## CI Configuration

```yaml
# .github/workflows/integration.yml
name: Integration Tests

on: [push, pull_request]

jobs:
  test:
    runs-on: ubuntu-latest

    steps:
      - uses: actions/checkout@v4

      - name: Setup Go
        uses: actions/setup-go@v5
        with:
          go-version: '1.25.0'

      - name: Install dependencies
        run: go mod download

      - name: Run integration tests
        run: go test -v ./... -tags=integration
        env:
          TESTCONTAINERS_RYUK_DISABLED: false
```

## Related Patterns

- **Fixture**: Data setup in containers
- **Contract Testing**: API verification
- **Fake**: Lighter alternative

## Sources

- [Testcontainers Documentation](https://testcontainers.com/)
- [Testcontainers Go](https://golang.testcontainers.org/)
- [Integration Testing Best Practices](https://martinfowler.com/bliki/IntegrationTest.html)
