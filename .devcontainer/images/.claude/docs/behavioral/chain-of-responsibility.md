# Chain of Responsibility Pattern

> Pass a request along a chain of handlers.

## Intent

Avoid coupling the sender of a request to its receiver by allowing
multiple objects to handle the request. Chain the receiver objects
and pass the request until an object handles it.

## Structure

```go
package main

import (
	"fmt"
	"strings"
	"time"
)

// Handler defines the interface for handling requests.
type Handler[T, R any] interface {
	SetNext(handler Handler[T, R]) Handler[T, R]
	Handle(request T) (R, error)
}

// AbstractHandler provides base functionality for handlers.
type AbstractHandler[T, R any] struct {
	next Handler[T, R]
}

// SetNext sets the next handler in the chain.
func (h *AbstractHandler[T, R]) SetNext(handler Handler[T, R]) Handler[T, R] {
	h.next = handler
	return handler
}

// Handle delegates to the next handler if it exists.
func (h *AbstractHandler[T, R]) Handle(request T) (R, error) {
	if h.next != nil {
		return h.next.Handle(request)
	}
	var zero R
	return zero, nil
}

// User represents an authenticated user.
type User struct {
	ID   string
	Role string
}

// HttpRequest represents an HTTP request.
type HttpRequest struct {
	Method  string
	Path    string
	Headers map[string]string
	Body    interface{}
	User    *User
}

// HttpResponse represents an HTTP response.
type HttpResponse struct {
	Status int
	Body   interface{}
}

// AuthenticationHandler handles authentication.
type AuthenticationHandler struct {
	AbstractHandler[*HttpRequest, *HttpResponse]
}

// Handle authenticates the request.
func (h *AuthenticationHandler) Handle(request *HttpRequest) (*HttpResponse, error) {
	token, ok:= request.Headers["authorization"]
	if !ok || token == "" {
		return &HttpResponse{
			Status: 401,
			Body:   map[string]string{"error": "No token provided"},
		}, nil
	}

	token = strings.TrimPrefix(token, "Bearer ")
	user, err:= h.verifyToken(token)
	if err != nil {
		return &HttpResponse{
			Status: 401,
			Body:   map[string]string{"error": "Invalid token"},
		}, nil
	}

	request.User = user
	return h.AbstractHandler.Handle(request)
}

func (h *AuthenticationHandler) verifyToken(token string) (*User, error) {
	// JWT verification logic
	return &User{ID: "1", Role: "user"}, nil
}

// AuthorizationHandler handles authorization.
type AuthorizationHandler struct {
	AbstractHandler[*HttpRequest, *HttpResponse]
	allowedRoles []string
}

// NewAuthorizationHandler creates a new authorization handler.
func NewAuthorizationHandler(allowedRoles []string) *AuthorizationHandler {
	return &AuthorizationHandler{allowedRoles: allowedRoles}
}

// Handle checks user authorization.
func (h *AuthorizationHandler) Handle(request *HttpRequest) (*HttpResponse, error) {
	if request.User == nil {
		return &HttpResponse{
			Status: 401,
			Body:   map[string]string{"error": "Not authenticated"},
		}, nil
	}

	allowed:= false
	for _, role:= range h.allowedRoles {
		if role == request.User.Role {
			allowed = true
			break
		}
	}

	if !allowed {
		return &HttpResponse{
			Status: 403,
			Body:   map[string]string{"error": "Access denied"},
		}, nil
	}

	return h.AbstractHandler.Handle(request)
}

// Schema represents a validation schema.
type Schema interface {
	Validate(body interface{}) []string
}

// ValidationHandler handles request validation.
type ValidationHandler struct {
	AbstractHandler[*HttpRequest, *HttpResponse]
	schema Schema
}

// NewValidationHandler creates a new validation handler.
func NewValidationHandler(schema Schema) *ValidationHandler {
	return &ValidationHandler{schema: schema}
}

// Handle validates the request body.
func (h *ValidationHandler) Handle(request *HttpRequest) (*HttpResponse, error) {
	errors:= h.schema.Validate(request.Body)
	if len(errors) > 0 {
		return &HttpResponse{
			Status: 400,
			Body:   map[string]interface{}{"errors": errors},
		}, nil
	}

	return h.AbstractHandler.Handle(request)
}

// RateLimitHandler handles rate limiting.
type RateLimitHandler struct {
	AbstractHandler[*HttpRequest, *HttpResponse]
	requests map[string][]int64
	limit    int
	windowMs int64
}

// NewRateLimitHandler creates a new rate limit handler.
func NewRateLimitHandler(limit int, windowMs int64) *RateLimitHandler {
	return &RateLimitHandler{
		requests: make(map[string][]int64),
		limit:    limit,
		windowMs: windowMs,
	}
}

// Handle checks rate limits.
func (h *RateLimitHandler) Handle(request *HttpRequest) (*HttpResponse, error) {
	clientID:= request.Headers["x-client-id"]
	if clientID == "" {
		clientID = "anonymous"
	}

	now:= time.Now().UnixMilli()
	windowStart:= now - h.windowMs

	// Clean old requests
	clientRequests:= []int64{}
	for _, reqTime:= range h.requests[clientID] {
		if reqTime > windowStart {
			clientRequests = append(clientRequests, reqTime)
		}
	}

	if len(clientRequests) >= h.limit {
		return &HttpResponse{
			Status: 429,
			Body:   map[string]string{"error": "Too many requests"},
		}, nil
	}

	clientRequests = append(clientRequests, now)
	h.requests[clientID] = clientRequests

	return h.AbstractHandler.Handle(request)
}

// LoggingHandler handles logging.
type LoggingHandler struct {
	AbstractHandler[*HttpRequest, *HttpResponse]
}

// Handle logs the request and response.
func (h *LoggingHandler) Handle(request *HttpRequest) (*HttpResponse, error) {
	fmt.Printf("[%s] %s %s\n", time.Now().Format(time.RFC3339), request.Method, request.Path)

	response, err:= h.AbstractHandler.Handle(request)
	if err != nil {
		return nil, err
	}

	if response != nil {
		fmt.Printf("Response: %d\n", response.Status)
	}

	return response, nil
}

// RequestHandler is the final handler that processes the request.
type RequestHandler struct {
	AbstractHandler[*HttpRequest, *HttpResponse]
	controller func(*HttpRequest) (*HttpResponse, error)
}

// NewRequestHandler creates a new request handler.
func NewRequestHandler(controller func(*HttpRequest) (*HttpResponse, error)) *RequestHandler {
	return &RequestHandler{controller: controller}
}

// Handle executes the controller function.
func (h *RequestHandler) Handle(request *HttpRequest) (*HttpResponse, error) {
	return h.controller(request)
}
```

## Usage

```go
// CreateUserSchema is a mock schema.
type CreateUserSchema struct{}

func (s *CreateUserSchema) Validate(body interface{}) []string {
	return []string{}
}

func main() {
	schema:= &CreateUserSchema{}

	// Build the chain
	chain:= &LoggingHandler{}
	chain.
		SetNext(NewRateLimitHandler(100, 60000)).
		SetNext(&AuthenticationHandler{}).
		SetNext(NewAuthorizationHandler([]string{"admin"})).
		SetNext(NewValidationHandler(schema)).
		SetNext(NewRequestHandler(func(req *HttpRequest) (*HttpResponse, error) {
			return &HttpResponse{
				Status: 201,
				Body: map[string]interface{}{
					"message": "User created",
					"userId":  "123",
				},
			}, nil
		}))

	// Process a request
	request:= &HttpRequest{
		Method: "POST",
		Path:   "/api/users",
		Headers: map[string]string{
			"authorization": "Bearer valid-token",
			"x-client-id":   "client-1",
		},
		Body: map[string]string{
			"name":  "John",
			"email": "john@example.com",
		},
	}

	response, err:= chain.Handle(request)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	fmt.Printf("Response: %+v\n", response)
}
```

## Variants

### Chain with Explicit next Function

```go
// Middleware is a function that processes a request.
type Middleware func(request *HttpRequest, response *HttpResponse, next func())

// MiddlewareChain manages a chain of middleware.
type MiddlewareChain struct {
	middlewares []Middleware
}

// Use adds a middleware to the chain.
func (m *MiddlewareChain) Use(middleware Middleware) *MiddlewareChain {
	m.middlewares = append(m.middlewares, middleware)
	return m
}

// Execute runs all middleware in order.
func (m *MiddlewareChain) Execute(request *HttpRequest) *HttpResponse {
	response:= &HttpResponse{Status: 200, Body: nil}
	index:= 0

	var next func()
	next = func() {
		if index < len(m.middlewares) {
			middleware:= m.middlewares[index]
			index++
			middleware(request, response, next)
		}
	}

	next()
	return response
}

// Express-like usage
func exampleMiddleware() {
	app:= &MiddlewareChain{}

	app.Use(func(req *HttpRequest, res *HttpResponse, next func()) {
		fmt.Println("Logging...")
		next()
	})

	app.Use(func(req *HttpRequest, res *HttpResponse, next func()) {
		if req.Headers["authorization"] == "" {
			res.Status = 401
			return // Stop chain
		}
		next()
	})

	app.Use(func(req *HttpRequest, res *HttpResponse, next func()) {
		res.Body = map[string]string{"message": "Success"}
		next()
	})
}
```

### Async Chain

```go
import "context"

// AsyncHandler handles requests asynchronously.
type AsyncHandler[T, R any] func(ctx context.Context, request T) (R, error)

// AsyncChain manages async handlers.
type AsyncChain[T, R any] struct {
	handlers []AsyncHandler[T, R]
}

// Use adds a handler to the chain.
func (c *AsyncChain[T, R]) Use(handler AsyncHandler[T, R]) *AsyncChain[T, R] {
	c.handlers = append(c.handlers, handler)
	return c
}

// Handle executes handlers until one returns a non-nil result.
func (c *AsyncChain[T, R]) Handle(ctx context.Context, request T) (R, error) {
	var zero R
	for _, handler:= range c.handlers {
		result, err:= handler(ctx, request)
		if err != nil {
			return zero, err
		}
		// Handler has processed the request
		if !isZero(result) {
			return result, nil
		}
	}
	return zero, nil // Aucun handler n'a traite
}

func isZero[T any](v T) bool {
	var zero T
	return fmt.Sprintf("%v", v) == fmt.Sprintf("%v", zero)
}

// Usage
func asyncExample() {
	type Cache interface {
		Get(ctx context.Context, key string) (interface{}, error)
		Set(ctx context.Context, key string, val interface{}) error
	}

	type DB interface {
		Query(ctx context.Context, path string) (interface{}, error)
	}

	asyncChain:= &AsyncChain[*HttpRequest, *HttpResponse]{}

	// Check cache
	asyncChain.Use(func(ctx context.Context, req *HttpRequest) (*HttpResponse, error) {
		var cache Cache
		cached, err:= cache.Get(ctx, req.Path)
		if err == nil && cached != nil {
			return &HttpResponse{Status: 200, Body: cached}, nil
		}
		var zero *HttpResponse
		return zero, nil
	})

	// Fetch from database
	asyncChain.Use(func(ctx context.Context, req *HttpRequest) (*HttpResponse, error) {
		var db DB
		var cache Cache
		data, err:= db.Query(ctx, req.Path)
		if err != nil {
			return nil, err
		}
		cache.Set(ctx, req.Path, data)
		return &HttpResponse{Status: 200, Body: data}, nil
	})
}
```

### Chain with Priority

```go
// PriorityHandler handles requests with priority.
type PriorityHandler[T, R any] struct {
	Priority  int
	CanHandle func(request T) bool
	Handle    func(request T) (R, error)
}

// PriorityChain manages prioritized handlers.
type PriorityChain[T, R any] struct {
	handlers []*PriorityHandler[T, R]
}

// Register adds a handler and sorts by priority.
func (c *PriorityChain[T, R]) Register(handler *PriorityHandler[T, R]) {
	c.handlers = append(c.handlers, handler)
	// Sort by priority (descending)
	for i:= len(c.handlers) - 1; i > 0; i-- {
		if c.handlers[i].Priority > c.handlers[i-1].Priority {
			c.handlers[i], c.handlers[i-1] = c.handlers[i-1], c.handlers[i]
		}
	}
}

// Handle finds the first handler that can process the request.
func (c *PriorityChain[T, R]) Handle(request T) (R, error) {
	var zero R
	for _, handler:= range c.handlers {
		if handler.CanHandle(request) {
			return handler.Handle(request)
		}
	}
	return zero, nil
}
```

## Anti-patterns

```go
// BAD: Chain too long
func badLongChain() {
	chain:= &Handler1{}
	chain.
		SetNext(&Handler2{}).
		SetNext(&Handler3{}).
		// ... 20 handlers ...
		SetNext(&Handler20{}) // Difficile a debugger
}

// BAD: Handler that never passes to the next
type GreedyHandler struct {
	AbstractHandler[*HttpRequest, *HttpResponse]
}

func (h *GreedyHandler) Handle(request *HttpRequest) (*HttpResponse, error) {
	// ALWAYS processes, never passes to the next
	return &HttpResponse{Status: 200, Body: "Always me"}, nil
}

// BAD: Undocumented order dependency
type OrderDependentHandler struct {
	AbstractHandler[*HttpRequest, *HttpResponse]
}

func (h *OrderDependentHandler) Handle(request *HttpRequest) (*HttpResponse, error) {
	// Assumes AuthHandler has already been executed
	// Without documentation, this is fragile
	user:= request.User // Can be nil!
	_ = user
	return h.AbstractHandler.Handle(request)
}

// BAD: Modifying the chain during execution
type DynamicHandler struct {
	AbstractHandler[*HttpRequest, *HttpResponse]
	someCondition bool
}

func (h *DynamicHandler) Handle(request *HttpRequest) (*HttpResponse, error) {
	if h.someCondition {
		h.SetNext(&AnotherHandler{}) // Dangereux!
	}
	return h.AbstractHandler.Handle(request)
}

type AnotherHandler struct {
	AbstractHandler[*HttpRequest, *HttpResponse]
}
```

## Unit Tests

```go
package main

import (
	"testing"
)

func TestAuthenticationHandler(t *testing.T) {
	t.Run("should reject requests without token", func(t *testing.T) {
		handler:= &AuthenticationHandler{}
		request:= &HttpRequest{
			Method:  "GET",
			Path:    "/api/data",
			Headers: map[string]string{},
		}

		response, err:= handler.Handle(request)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if response.Status != 401 {
			t.Errorf("expected status 401, got %d", response.Status)
		}
	})

	t.Run("should pass authenticated requests to next", func(t *testing.T) {
		nextCalled:= false
		nextHandler:= NewRequestHandler(func(req *HttpRequest) (*HttpResponse, error) {
			nextCalled = true
			return &HttpResponse{Status: 200, Body: "ok"}, nil
		})

		handler:= &AuthenticationHandler{}
		handler.SetNext(nextHandler)

		request:= &HttpRequest{
			Method: "GET",
			Path:   "/api/data",
			Headers: map[string]string{
				"authorization": "Bearer valid-token",
			},
		}

		_, err:= handler.Handle(request)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !nextCalled {
			t.Error("next handler was not called")
		}

		if request.User == nil {
			t.Error("user should be set")
		}
	})
}

func TestRateLimitHandler(t *testing.T) {
	t.Run("should allow requests within limit", func(t *testing.T) {
		handler:= NewRateLimitHandler(3, 1000)
		handler.SetNext(NewRequestHandler(func(req *HttpRequest) (*HttpResponse, error) {
			return &HttpResponse{Status: 200, Body: "ok"}, nil
		}))

		request:= &HttpRequest{
			Method:  "GET",
			Path:    "/api",
			Headers: map[string]string{"x-client-id": "test"},
		}

		r1, _:= handler.Handle(request)
		r2, _:= handler.Handle(request)
		r3, _:= handler.Handle(request)

		if r1.Status != 200 || r2.Status != 200 || r3.Status != 200 {
			t.Error("requests within limit should succeed")
		}
	})

	t.Run("should reject requests over limit", func(t *testing.T) {
		handler:= NewRateLimitHandler(2, 10000)
		handler.SetNext(NewRequestHandler(func(req *HttpRequest) (*HttpResponse, error) {
			return &HttpResponse{Status: 200, Body: "ok"}, nil
		}))

		request:= &HttpRequest{
			Method:  "GET",
			Path:    "/api",
			Headers: map[string]string{"x-client-id": "test"},
		}

		handler.Handle(request)
		handler.Handle(request)
		response, _:= handler.Handle(request)

		if response.Status != 429 {
			t.Errorf("expected status 429, got %d", response.Status)
		}
	})
}

func TestFullChain(t *testing.T) {
	t.Run("should process request through all handlers", func(t *testing.T) {
		finalCalled:= false
		finalHandler:= NewRequestHandler(func(req *HttpRequest) (*HttpResponse, error) {
			finalCalled = true
			return &HttpResponse{Status: 200, Body: "done"}, nil
		})

		chain:= &LoggingHandler{}
		chain.
			SetNext(&AuthenticationHandler{}).
			SetNext(finalHandler)

		request:= &HttpRequest{
			Method: "GET",
			Path:   "/api/data",
			Headers: map[string]string{
				"authorization": "Bearer token",
			},
		}

		response, err:= chain.Handle(request)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if response.Status != 200 {
			t.Errorf("expected status 200, got %d", response.Status)
		}

		if !finalCalled {
			t.Error("final handler was not called")
		}
	})

	t.Run("should stop at first error", func(t *testing.T) {
		shouldNotBeCalled:= false
		chain:= &AuthenticationHandler{}
		chain.SetNext(NewRequestHandler(func(req *HttpRequest) (*HttpResponse, error) {
			shouldNotBeCalled = true
			return &HttpResponse{Status: 200, Body: "ok"}, nil
		}))

		request:= &HttpRequest{
			Method:  "GET",
			Path:    "/api/data",
			Headers: map[string]string{}, // No auth
		}

		response, _:= chain.Handle(request)

		if response.Status != 401 {
			t.Errorf("expected status 401, got %d", response.Status)
		}

		if shouldNotBeCalled {
			t.Error("handler should not have been called")
		}
	})
}
```

## When to Use

- Multiple handlers can process a request
- The set of handlers is not known in advance
- Processing order matters
- Middleware pattern (HTTP, message queues)

## Related Patterns

- **Decorator**: Similar structure, but enriches vs processes
- **Composite**: Can combine chains
- **Command**: Can be combined to queue handlers

## Sources

- [Refactoring Guru - Chain of Responsibility](https://refactoring.guru/design-patterns/chain-of-responsibility)
