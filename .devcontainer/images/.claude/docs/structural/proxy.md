# Proxy Pattern

> Provide a substitute or placeholder to control access to an object.

## Intent

Provide an intermediary for another object to control access,
reduce cost, or add functionality without modifying the original object.

## Proxy Types

### 1. Virtual Proxy (Lazy Loading)

```go
package main

import (
	"fmt"
)

type Image interface {
	Display()
	GetSize() (width, height int)
}

type RealImage struct {
	filename string
	data     []byte
}

func NewRealImage(filename string) *RealImage {
	img := &RealImage{filename: filename}
	img.loadFromDisk()
	return img
}

func (r *RealImage) loadFromDisk() {
	fmt.Printf("Loading image: %s\n", r.filename)
	// Heavy loading simulation
	r.data = make([]byte, 1024*1024*10) // 10MB
}

func (r *RealImage) Display() {
	fmt.Printf("Displaying: %s\n", r.filename)
}

func (r *RealImage) GetSize() (width, height int) {
	return 1920, 1080
}

type ImageProxy struct {
	filename  string
	realImage *RealImage
}

func NewImageProxy(filename string) *ImageProxy {
	return &ImageProxy{filename: filename}
}

func (i *ImageProxy) ensureLoaded() *RealImage {
	if i.realImage == nil {
		i.realImage = NewRealImage(i.filename)
	}
	return i.realImage
}

func (i *ImageProxy) Display() {
	i.ensureLoaded().Display()
}

// Metadata accessible without loading the image
func (i *ImageProxy) GetSize() (width, height int) {
	// Read only the file headers
	return 1920, 1080
}
```

### 2. Protection Proxy (Access Control)

```go
package main

import (
	"fmt"
)

type Document interface {
	Read() string
	Write(content string) error
	Delete() error
}

type UserRole string

const (
	RoleAdmin  UserRole = "admin"
	RoleEditor UserRole = "editor"
	RoleViewer UserRole = "viewer"
)

type User struct {
	ID   string
	Role UserRole
}

type RealDocument struct {
	id      string
	content string
}

func NewRealDocument(id, content string) *RealDocument {
	return &RealDocument{id: id, content: content}
}

func (r *RealDocument) Read() string {
	return r.content
}

func (r *RealDocument) Write(content string) error {
	r.content = content
	return nil
}

func (r *RealDocument) Delete() error {
	fmt.Printf("Document %s deleted\n", r.id)
	return nil
}

type ProtectedDocument struct {
	document    Document
	currentUser *User
}

func NewProtectedDocument(doc Document, user *User) *ProtectedDocument {
	return &ProtectedDocument{
		document:    doc,
		currentUser: user,
	}
}

func (p *ProtectedDocument) Read() string {
	// Everyone can read
	return p.document.Read()
}

func (p *ProtectedDocument) Write(content string) error {
	if p.currentUser.Role == RoleViewer {
		return fmt.Errorf("permission denied: viewers cannot write")
	}
	return p.document.Write(content)
}

func (p *ProtectedDocument) Delete() error {
	if p.currentUser.Role != RoleAdmin {
		return fmt.Errorf("permission denied: only admins can delete")
	}
	return p.document.Delete()
}
```

### 3. Remote Proxy (RPC/API)

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

type UserService interface {
	GetUser(ctx context.Context, id string) (*User, error)
	UpdateUser(ctx context.Context, id string, data map[string]interface{}) (*User, error)
}

type User struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type RemoteUserService struct {
	baseURL string
	client  *http.Client
}

func NewRemoteUserService(baseURL string) *RemoteUserService {
	return &RemoteUserService{
		baseURL: baseURL,
		client:  http.DefaultClient,
	}
}

func (r *RemoteUserService) GetUser(ctx context.Context, id string) (*User, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/users/%s", r.baseURL, id), nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("making request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("user not found: %s", id)
	}

	var user User
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	return &user, nil
}

func (r *RemoteUserService) UpdateUser(ctx context.Context, id string, data map[string]interface{}) (*User, error) {
	// Similar implementation
	return &User{ID: id}, nil
}

// The client uses the interface as if it were local
type UserController struct {
	userService UserService
}

func NewUserController(service UserService) *UserController {
	return &UserController{userService: service}
}

func (u *UserController) ShowProfile(ctx context.Context, userID string) error {
	user, err := u.userService.GetUser(ctx, userID)
	if err != nil {
		return err
	}
	fmt.Printf("Profile: %s\n", user.Name)
	return nil
}
```

### 4. Cache Proxy

```go
package main

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type Data struct {
	Value string
}

type DataService interface {
	FetchData(ctx context.Context, key string) (*Data, error)
}

type cacheEntry struct {
	data    *Data
	expires time.Time
}

type CachedDataService struct {
	service DataService
	ttl     time.Duration
	cache   map[string]*cacheEntry
	mu      sync.RWMutex
}

func NewCachedDataService(service DataService, ttl time.Duration) *CachedDataService {
	if ttl == 0 {
		ttl = 60 * time.Second
	}
	return &CachedDataService{
		service: service,
		ttl:     ttl,
		cache:   make(map[string]*cacheEntry),
	}
}

func (c *CachedDataService) FetchData(ctx context.Context, key string) (*Data, error) {
	c.mu.RLock()
	cached, found := c.cache[key]
	c.mu.RUnlock()

	if found && cached.expires.After(time.Now()) {
		fmt.Printf("Cache hit: %s\n", key)
		return cached.data, nil
	}

	fmt.Printf("Cache miss: %s\n", key)
	data, err := c.service.FetchData(ctx, key)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.cache[key] = &cacheEntry{
		data:    data,
		expires: time.Now().Add(c.ttl),
	}
	c.mu.Unlock()

	return data, nil
}

func (c *CachedDataService) Invalidate(key string) {
	c.mu.Lock()
	delete(c.cache, key)
	c.mu.Unlock()
}

func (c *CachedDataService) Clear() {
	c.mu.Lock()
	c.cache = make(map[string]*cacheEntry)
	c.mu.Unlock()
}
```

### 5. Logging Proxy

```go
package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

type Database interface {
	Query(ctx context.Context, sql string) (interface{}, error)
	Execute(ctx context.Context, sql string) (int, error)
}

type LoggingDatabaseProxy struct {
	db     Database
	logger *slog.Logger
}

func NewLoggingDatabaseProxy(db Database, logger *slog.Logger) *LoggingDatabaseProxy {
	return &LoggingDatabaseProxy{
		db:     db,
		logger: logger,
	}
}

func (l *LoggingDatabaseProxy) Query(ctx context.Context, sql string) (interface{}, error) {
	start := time.Now()
	l.logger.Debug("Query", "sql", sql)

	result, err := l.db.Query(ctx, sql)
	duration := time.Since(start)

	if err != nil {
		l.logger.Error("Query failed", "error", err, "sql", sql)
		return nil, err
	}

	l.logger.Info("Query completed", "duration_ms", duration.Milliseconds())
	return result, nil
}

func (l *LoggingDatabaseProxy) Execute(ctx context.Context, sql string) (int, error) {
	l.logger.Debug("Execute", "sql", sql)
	return l.db.Execute(ctx, sql)
}
```

## Advanced Variants

### Smart Reference Proxy

```go
package main

import (
	"fmt"
	"sync"
)

type SmartReference[T any] struct {
	factory    func() T
	instance   *T
	references int
	mu         sync.Mutex
}

func NewSmartReference[T any](factory func() T) *SmartReference[T] {
	return &SmartReference[T]{
		factory: factory,
	}
}

func (s *SmartReference[T]) Acquire() T {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.instance == nil {
		instance := s.factory()
		s.instance = &instance
	}
	s.references++
	return *s.instance
}

func (s *SmartReference[T]) Release() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.references--
	if s.references == 0 && s.instance != nil {
		fmt.Println("No more references, cleaning up")
		s.instance = nil
	}
}
```

### Validation Proxy

```go
package main

import (
	"fmt"
	"reflect"
)

type Validator interface {
	Validate(field string, value interface{}) error
}

type FieldRule struct {
	Type     string
	Required bool
	Min      *int
	Max      *int
}

type Schema map[string]FieldRule

type ValidatingProxy struct {
	target interface{}
	schema Schema
}

func NewValidatingProxy(target interface{}, schema Schema) *ValidatingProxy {
	return &ValidatingProxy{
		target: target,
		schema: schema,
	}
}

func (v *ValidatingProxy) Set(field string, value interface{}) error {
	rule, exists := v.schema[field]
	if !exists {
		return fmt.Errorf("unknown field: %s", field)
	}

	if rule.Required && value == nil {
		return fmt.Errorf("%s is required", field)
	}

	valueType := reflect.TypeOf(value).Kind().String()
	if valueType != rule.Type {
		return fmt.Errorf("%s must be %s, got %s", field, rule.Type, valueType)
	}

	if rule.Type == "int" {
		intVal := value.(int)
		if rule.Min != nil && intVal < *rule.Min {
			return fmt.Errorf("%s must be >= %d", field, *rule.Min)
		}
		if rule.Max != nil && intVal > *rule.Max {
			return fmt.Errorf("%s must be <= %d", field, *rule.Max)
		}
	}

	// Set value using reflection
	return nil
}
```

## Anti-patterns

```go
// BAD: Proxy that changes behavior
type BadProxy struct {
	service UserService
}

func (b *BadProxy) GetUser(ctx context.Context, id string) (*User, error) {
	// Modifies data = not a proxy!
	user, err := b.service.GetUser(ctx, id)
	if err != nil {
		return nil, err
	}
	user.Name = fmt.Sprintf("Modified: %s", user.Name) // Transformation
	return user, nil
}

// BAD: Proxy with business logic
type Order struct {
	Items []OrderItem
	Total float64
}

type OrderItem struct {
	Price float64
}

type OrderService interface {
	CreateOrder(ctx context.Context, order *Order) (*Order, error)
}

type BusinessLogicProxy struct {
	service OrderService
}

func (b *BusinessLogicProxy) CreateOrder(ctx context.Context, order *Order) (*Order, error) {
	// Price calculation = business logic, not proxy
	var total float64
	for _, item := range order.Items {
		total += item.Price
	}
	order.Total = total
	return b.service.CreateOrder(ctx, order)
}
```

## Unit Tests

```go
package main

import (
	"context"
	"testing"
)

func TestImageProxy_LazyLoad(t *testing.T) {
	proxy := NewImageProxy("test.jpg")

	// Not loaded yet - verify with a counter in RealImage

	// Loaded on first access
	proxy.Display()
}

func TestImageProxy_Metadata(t *testing.T) {
	proxy := NewImageProxy("test.jpg")
	width, height := proxy.GetSize()

	if width != 1920 || height != 1080 {
		t.Errorf("Expected 1920x1080, got %dx%d", width, height)
	}
}

func TestProtectedDocument_ViewerCanRead(t *testing.T) {
	doc := NewRealDocument("1", "content")
	viewer := &User{ID: "1", Role: RoleViewer}
	protected := NewProtectedDocument(doc, viewer)

	content := protected.Read()
	if content != "content" {
		t.Errorf("Expected 'content', got %s", content)
	}
}

func TestProtectedDocument_ViewerCannotWrite(t *testing.T) {
	doc := NewRealDocument("1", "content")
	viewer := &User{ID: "1", Role: RoleViewer}
	protected := NewProtectedDocument(doc, viewer)

	err := protected.Write("new content")
	if err == nil {
		t.Error("Expected error for viewer writing")
	}
}

func TestCachedDataService_CachesResponses(t *testing.T) {
	callCount := 0
	mockService := &mockDataService{
		fetchFunc: func(ctx context.Context, key string) (*Data, error) {
			callCount++
			return &Data{Value: "test"}, nil
		},
	}

	cached := NewCachedDataService(mockService, 10*time.Second)

	_, _ = cached.FetchData(context.Background(), "key1")
	_, _ = cached.FetchData(context.Background(), "key1")

	if callCount != 1 {
		t.Errorf("Expected 1 call, got %d", callCount)
	}
}

type mockDataService struct {
	fetchFunc func(context.Context, string) (*Data, error)
}

func (m *mockDataService) FetchData(ctx context.Context, key string) (*Data, error) {
	return m.fetchFunc(ctx, key)
}
```

## When to Use

| Type | Use Case |
|------|----------|
| Virtual | Expensive objects to create |
| Protection | Access control/permissions |
| Remote | Network/RPC calls |
| Cache | Performance optimization |
| Logging | Debug, monitoring |

## Related Patterns

- **Decorator**: Adds behaviors vs controls access
- **Adapter**: Changes the interface vs same interface
- **Facade**: Simplifies vs controls

## Sources

- [Refactoring Guru - Proxy](https://refactoring.guru/design-patterns/proxy)
