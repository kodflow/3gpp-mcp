# Singleton Pattern

> Guarantee a unique instance of a class with a global access point.

## Intention

Ensure that a class has only one instance and provide a global
access point to that instance.

## Classic Structure

```go
package main

import (
	"fmt"
	"sync"
)

// Connection represents a database connection.
type Connection struct {
	host string
}

// Execute executes an SQL query.
func (c *Connection) Execute(sql string) string {
	return fmt.Sprintf("Executing: %s on %s", sql, c.host)
}

// Database manages the singleton connection.
type Database struct {
	connection *Connection
}

var (
	instance *Database
	once     sync.Once
)

// getInstance returns the unique instance (thread-safe with sync.Once).
func getInstance() *Database {
	once.Do(func() {
		fmt.Println("Connecting to database...")
		instance = &Database{
			connection: &Connection{host: "localhost:5432"},
		}
	})
	return instance
}

// Query executes an SQL query.
func (db *Database) Query(sql string) string {
	return db.connection.Execute(sql)
}

// Usage
func ExampleDatabase() {
	db1 := getInstance()
	db2 := getInstance()
	fmt.Println(db1 == db2) // true
}
```

## Variants

### Thread-safe Singleton (with sync.Once)

```go
package main

import (
	"fmt"
	"sync"
)

// ThreadSafeDatabase guarantees a unique instance in a concurrent environment.
type ThreadSafeDatabase struct {
	connectionString string
}

var (
	safeInstance *ThreadSafeDatabase
	safeOnce     sync.Once
)

// GetInstance returns the unique instance in a thread-safe manner.
func GetInstance() *ThreadSafeDatabase {
	safeOnce.Do(func() {
		fmt.Println("Initializing database connection...")
		safeInstance = &ThreadSafeDatabase{
			connectionString: "postgres://localhost:5432/mydb",
		}
	})
	return safeInstance
}

// Query executes a query.
func (db *ThreadSafeDatabase) Query(sql string) string {
	return fmt.Sprintf("Query on %s: %s", db.connectionString, sql)
}
```

### Singleton with sync.OnceValue (Go 1.21+ - RECOMMENDED)

```go
package main

import (
	"fmt"
	"sync"
)

// Database represents a singleton connection.
type Database struct {
	connectionString string
}

// NewDatabase creates a new Database instance.
func newDatabase() *Database {
	fmt.Println("Initializing database connection...")
	return &Database{
		connectionString: "postgres://localhost:5432/mydb",
	}
}

// GetDB returns the singleton instance in a type-safe manner.
// sync.OnceValue (Go 1.21+) is more concise and type-safe than sync.Once.
var GetDB = sync.OnceValue(newDatabase)

// Query executes a query.
func (db *Database) Query(sql string) string {
	return fmt.Sprintf("Query on %s: %s", db.connectionString, sql)
}

// Usage
func ExampleOnceValue() {
	db1 := GetDB()
	db2 := GetDB()
	fmt.Println(db1 == db2) // true

	result := db1.Query("SELECT * FROM users")
	fmt.Println(result)
}
```

### Singleton with sync.OnceValues (for value + error)

```go
package main

import (
	"errors"
	"os"
	"sync"
)

// Config represents the application configuration.
type Config struct {
	DatabaseURL string
	APIKey      string
}

// loadConfig loads the configuration from the environment.
func loadConfig() (*Config, error) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return nil, errors.New("DATABASE_URL not set")
	}

	apiKey := os.Getenv("API_KEY")
	if apiKey == "" {
		return nil, errors.New("API_KEY not set")
	}

	return &Config{
		DatabaseURL: dbURL,
		APIKey:      apiKey,
	}, nil
}

// GetConfig returns the singleton configuration with error handling.
// sync.OnceValues (Go 1.21+) allows returning a value AND an error.
var GetConfig = sync.OnceValues(loadConfig)

// Usage
func ExampleOnceValues() {
	config, err := GetConfig()
	if err != nil {
		panic(err)
	}

	// Subsequent calls return the same result (cached)
	config2, _ := GetConfig()
	println(config == config2) // true
}
```

### Singleton with Lazy Initialization

```go
package main

import (
	"fmt"
	"sync"
)

// LazyLogger implements a singleton logger with lazy initialization.
type LazyLogger struct {
	logLevel string
}

var (
	loggerInstance *LazyLogger
	loggerOnce     sync.Once
)

// GetLogger returns the logger instance (initialized on first call).
func GetLogger() *LazyLogger {
	loggerOnce.Do(func() {
		loggerInstance = &LazyLogger{
			logLevel: "INFO",
		}
	})
	return loggerInstance
}

// Log writes a log message.
func (l *LazyLogger) Log(message string) {
	fmt.Printf("[%s] %s\n", l.logLevel, message)
}
```

### Singleton with Configuration

```go
package main

import (
	"errors"
	"sync"
)

// ConfigOptions defines the configuration options.
type ConfigOptions struct {
	Host  string
	Port  int
	Debug bool
}

// ConfigManager manages the singleton configuration.
type ConfigManager struct {
	config ConfigOptions
}

var (
	configInstance *ConfigManager
	configOnce     sync.Once
	configMu       sync.RWMutex
	initialized    bool
)

// Initialize initializes the ConfigManager with the given options.
func Initialize(options ConfigOptions) error {
	configMu.Lock()
	defer configMu.Unlock()

	if initialized {
		return errors.New("ConfigManager already initialized")
	}

	configOnce.Do(func() {
		configInstance = &ConfigManager{
			config: options,
		}
		initialized = true
	})

	return nil
}

// GetConfigManager returns the ConfigManager instance.
func GetConfigManager() (*ConfigManager, error) {
	configMu.RLock()
	defer configMu.RUnlock()

	if !initialized {
		return nil, errors.New("ConfigManager not initialized")
	}
	return configInstance, nil
}

// GetHost returns the configured host.
func (cm *ConfigManager) GetHost() string {
	return cm.config.Host
}

// GetPort returns the configured port.
func (cm *ConfigManager) GetPort() int {
	return cm.config.Port
}

// IsDebug returns whether debug mode is enabled.
func (cm *ConfigManager) IsDebug() bool {
	return cm.config.Debug
}

// Usage
func ExampleConfigManager() {
	err := Initialize(ConfigOptions{
		Host:  "localhost",
		Port:  3000,
		Debug: true,
	})
	if err != nil {
		panic(err)
	}

	config, err := GetConfigManager()
	if err != nil {
		panic(err)
	}
	println(config.GetHost()) // localhost
}
```

## Why Singleton Is Often an Anti-pattern

```go
// PROBLEMS:

// 1. Hidden global state - hard to trace
type OrderService struct{}

func (s *OrderService) Process(order Order) error {
	// Where does this dependency come from? Invisible in the signature
	db := getInstance()
	db.Query("INSERT INTO orders...")
	logger := GetLogger()
	logger.Log("Order processed")
	return nil
}

// 2. Tight coupling - hard to test
type UserService struct{}

func (s *UserService) GetUser(id string) (*User, error) {
	// How to mock Database in tests?
	db := getInstance()
	result := db.Query("SELECT * FROM users WHERE id=" + id)
	return &User{}, nil
}

// 3. SRP violation - manages its lifecycle + its logic
type BadSingleton struct {
	data string
}

var badInstance *BadSingleton
var badOnce sync.Once

func getBadSingleton() *BadSingleton { // Responsibility 1: lifecycle
	badOnce.Do(func() {
		badInstance = &BadSingleton{}
	})
	return badInstance
}

func (b *BadSingleton) ProcessData() { // Responsibility 2: business logic
	// ...
}

// 4. Concurrency issues in tests
// Tests share the same instance = side effects
```

## Modern Alternatives

### Dependency Injection (recommended)

```go
package main

import (
	"context"
	"database/sql"
	"fmt"
)

// 1. Interface for abstraction
type IDatabase interface {
	Query(ctx context.Context, sql string) (string, error)
}

// 2. Concrete implementation
type Database struct {
	connectionString string
}

// NewDatabase creates a new Database instance.
func NewDatabase(connectionString string) *Database {
	return &Database{
		connectionString: connectionString,
	}
}

func (db *Database) Query(ctx context.Context, sql string) (string, error) {
	return fmt.Sprintf("Query result for: %s", sql), nil
}

// 3. DI Container
type Container struct {
	mu       sync.RWMutex
	services map[string]interface{}
}

// NewContainer creates a new DI container.
func NewContainer() *Container {
	return &Container{
		services: make(map[string]interface{}),
	}
}

// RegisterSingleton registers a singleton service.
func (c *Container) RegisterSingleton(token string, instance interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.services[token] = instance
}

// Resolve resolves a service by its token.
func (c *Container) Resolve(token string) (interface{}, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	service, exists := c.services[token]
	if !exists {
		return nil, fmt.Errorf("service %s not found", token)
	}
	return service, nil
}

// 4. Configuration
func ExampleDI() {
	container := NewContainer()
	container.RegisterSingleton("database", NewDatabase("postgres://localhost:5432"))

	// 5. Usage - explicit dependencies
	type UserService struct {
		db IDatabase
	}

	dbService, err := container.Resolve("database")
	if err != nil {
		panic(err)
	}

	userService := &UserService{
		db: dbService.(IDatabase),
	}
	_ = userService
}
```

### Module pattern (package-level variables)

```go
package database

import (
	"context"
	"fmt"
	"sync"
)

var (
	connection *Connection
	once       sync.Once
)

// Connection represents a database connection.
type Connection struct {
	host string
}

// init initializes the connection at package startup.
func init() {
	once.Do(func() {
		connection = &Connection{
			host: "localhost:5432",
		}
	})
}

// Query executes an SQL query (direct access to the singleton connection).
func Query(ctx context.Context, sql string) (string, error) {
	return fmt.Sprintf("Executing: %s", sql), nil
}

// Close closes the connection.
func Close() error {
	// Close implementation
	return nil
}

// Usage - the package is naturally a singleton
// import "yourproject/database"
// result, err := database.Query(ctx, "SELECT * FROM users")
```

### Factory with Scope

```go
package main

import (
	"sync"
)

// Scope defines the scope of a service.
type Scope string

const (
	ScopeSingleton Scope = "singleton"
	ScopeTransient Scope = "transient"
	ScopeScoped    Scope = "scoped"
)

// ServiceFactory manages service creation with different scopes.
type ServiceFactory struct {
	mu        sync.RWMutex
	instances map[string]interface{}
}

// NewServiceFactory creates a new factory.
func NewServiceFactory() *ServiceFactory {
	return &ServiceFactory{
		instances: make(map[string]interface{}),
	}
}

// Singleton returns or creates a singleton instance.
func (f *ServiceFactory) Singleton(key string, factory func() interface{}) interface{} {
	f.mu.Lock()
	defer f.mu.Unlock()

	if instance, exists := f.instances[key]; exists {
		return instance
	}

	instance := factory()
	f.instances[key] = instance
	return instance
}

// Transient always creates a new instance.
func (f *ServiceFactory) Transient(factory func() interface{}) interface{} {
	return factory()
}

// Scoped returns an instance limited to a given scope.
func (f *ServiceFactory) Scoped(scope, key string, factory func() interface{}) interface{} {
	scopeKey := fmt.Sprintf("%s:%s", scope, key)
	f.mu.Lock()
	defer f.mu.Unlock()

	if instance, exists := f.instances[scopeKey]; exists {
		return instance
	}

	instance := factory()
	f.instances[scopeKey] = instance
	return instance
}
```

## Unit Tests

```go
package main

import (
	"context"
	"sync"
	"testing"
)

// Classic Singleton test
func TestDatabase_Singleton(t *testing.T) {
	// Reset needed between tests (use build tags or interfaces)
	once = sync.Once{}
	instance = nil

	db1 := getInstance()
	db2 := getInstance()

	if db1 != db2 {
		t.Error("expected same instance")
	}
}

// Test with DI (easy)
type mockDatabase struct{}

func (m *mockDatabase) Query(ctx context.Context, sql string) (string, error) {
	return "mock result", nil
}

func TestUserService_WithDI(t *testing.T) {
	type UserService struct {
		db IDatabase
	}

	mockDB := &mockDatabase{}
	service := &UserService{db: mockDB}

	result, err := service.db.Query(context.Background(), "SELECT * FROM users")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != "mock result" {
		t.Errorf("expected 'mock result', got %s", result)
	}
}

// Module pattern test
func TestDatabaseModule_Query(t *testing.T) {
	ctx := context.Background()
	result, err := Query(ctx, "SELECT 1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty result")
	}
}

// Thread-safety test
func TestGetInstance_Concurrent(t *testing.T) {
	once = sync.Once{}
	instance = nil

	var wg sync.WaitGroup
	instances := make([]*Database, 100)

	for i := 0; i < 100; i++ {
		idx := i // Capture for closure
		wg.Go(func() { // Go 1.25: handles Add/Done internally
			instances[idx] = getInstance()
		})
	}

	wg.Wait()

	// All instances must be identical
	for i := 1; i < len(instances); i++ {
		if instances[i] != instances[0] {
			t.Error("expected all instances to be the same")
		}
	}
}
```

## When to Use (really)

- Expensive shared resources (connection pool)
- Global application configuration
- Application cache
- Logger (but prefer DI)

## When to Avoid

- When testability is important
- When multiple configurations are possible
- In libraries (imposing a singleton on users)
- When global state creates coupling

## Decision: Singleton vs DI

| Criterion | Singleton | DI Container |
|---------|-----------|--------------|
| Initial simplicity | Yes | No |
| Testability | Hard | Easy |
| Flexibility | Low | High |
| Coupling | Tight | Loose |
| Configuration | Static | Dynamic |

## Related Patterns

- **Factory**: Controls Singleton creation
- **Facade**: Often implemented as Singleton
- **Service Locator**: Alternative to DI (but similar anti-pattern)

## Sources

- [Refactoring Guru - Singleton](https://refactoring.guru/design-patterns/singleton)
- [Mark Seemann - Service Locator is an Anti-Pattern](https://blog.ploeh.dk/2010/02/03/ServiceLocatorisanAnti-Pattern/)
