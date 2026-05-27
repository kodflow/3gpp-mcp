# Gateway Offloading Pattern

> Offload shared functionality from services to the gateway.

## Principle

```
┌────────────────────────────────────────────────────────────────────┐
│                         API GATEWAY                                 │
│                                                                     │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐  ┌───────────┐  │
│  │    SSL      │  │    Auth     │  │   Logging   │  │   Rate    │  │
│  │ Termination │  │   (OAuth)   │  │  & Tracing  │  │  Limiting │  │
│  └─────────────┘  └─────────────┘  └─────────────┘  └───────────┘  │
│                                                                     │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐  ┌───────────┐  │
│  │   Caching   │  │ Compression │  │    CORS     │  │  Metrics  │  │
│  └─────────────┘  └─────────────┘  └─────────────┘  └───────────┘  │
└────────────────────────────────────────────────────────────────────┘
                              │
                              ▼
     ┌────────────────────────┬────────────────────────┐
     │                        │                        │
     ▼                        ▼                        ▼
┌─────────┐             ┌─────────┐             ┌─────────┐
│ Service │             │ Service │             │ Service │
│    A    │             │    B    │             │    C    │
│ (light) │             │ (light) │             │ (light) │
└─────────┘             └─────────┘             └─────────┘
```

## Offloadable Functionality

| Functionality | Gateway Advantage | Complexity |
|---------------|-------------------|------------|
| **SSL Termination** | Centralized certificates | Low |
| **Authentication** | Uniform policy | Medium |
| **Rate Limiting** | Global protection | Low |
| **Caching** | Reduced backend load | Medium |
| **Compression** | Optimized bandwidth | Low |
| **CORS** | Single configuration | Low |
| **Request Validation** | Early rejection | Medium |
| **Response Transformation** | Uniform format | High |

## Go Example

```go
package gateway

import (
	"context"
	"net/http"
)

// GatewayContext provides context for middleware execution.
type GatewayContext struct {
	Request  *http.Request
	Response http.ResponseWriter
	User     *User
}

// User represents an authenticated user.
type User struct {
	ID   string
	Name string
}

// OffloadingMiddleware defines a middleware function.
type OffloadingMiddleware struct {
	Name    string
	Execute func(ctx context.Context, gc *GatewayContext, next func() error) error
}

// GatewayOffloader manages middleware chain.
type GatewayOffloader struct {
	middlewares []OffloadingMiddleware
}

// NewGatewayOffloader creates a new GatewayOffloader.
func NewGatewayOffloader() *GatewayOffloader {
	return &GatewayOffloader{
		middlewares: make([]OffloadingMiddleware, 0),
	}
}

// Use adds a middleware to the chain.
func (gw *GatewayOffloader) Use(middleware OffloadingMiddleware) *GatewayOffloader {
	gw.middlewares = append(gw.middlewares, middleware)
	return gw
}

// Handle executes the middleware chain.
func (gw *GatewayOffloader) Handle(ctx context.Context, r *http.Request, w http.ResponseWriter) error {
	gc := &GatewayContext{
		Request:  r,
		Response: w,
	}

	return gw.executeMiddleware(ctx, gc, 0)
}

func (gw *GatewayOffloader) executeMiddleware(ctx context.Context, gc *GatewayContext, index int) error {
	if index >= len(gw.middlewares) {
		return nil
	}

	middleware := gw.middlewares[index]
	return middleware.Execute(ctx, gc, func() error {
		return gw.executeMiddleware(ctx, gc, index+1)
	})
}

// Example middlewares

// SSLTerminationMiddleware handles SSL termination.
var SSLTerminationMiddleware = OffloadingMiddleware{
	Name: "ssl-termination",
	Execute: func(ctx context.Context, gc *GatewayContext, next func() error) error {
		// SSL handled by load balancer/gateway
		gc.Request.Header.Set("X-Forwarded-Proto", "https")
		return next()
	},
}

// AuthMiddleware handles authentication.
var AuthMiddleware = OffloadingMiddleware{
	Name: "authentication",
	Execute: func(ctx context.Context, gc *GatewayContext, next func() error) error {
		token := gc.Request.Header.Get("Authorization")

		if token == "" {
			http.Error(gc.Response, "Unauthorized", http.StatusUnauthorized)
			return nil
		}

		// Validate token (simplified)
		user := &User{ID: "user123", Name: "John"}
		gc.User = user
		gc.Request.Header.Set("X-User-Id", user.ID)

		return next()
	},
}
```

## Gateway Configuration

```go
// This example follows the same idiomatic Go patterns
// as the main example above.
// Specific implementation based on standard Go
// interfaces and conventions.
```

## Benefits

| Aspect | Without Offloading | With Offloading |
|--------|-------------------|-----------------|
| **Service code** | Complex | Simple |
| **SSL certificates** | N services | 1 gateway |
| **Auth policies** | Duplicated | Centralized |
| **Updates** | N deployments | 1 deployment |
| **Monitoring** | Fragmented | Unified |

## Anti-patterns

| Anti-pattern | Problem | Solution |
|--------------|---------|----------|
| Overloaded gateway | SPOF, latency | Distribute, scale |
| Business logic | Coupling | Keep cross-cutting only |
| No fallback | Gateway down = all down | Resilience, multi-instance |
| Over-caching | Stale data | Adapted TTL, invalidation |

## When to Use

- Centralizing SSL termination to simplify certificate management
- Uniform authentication and authorization across all services
- Rate limiting and abuse protection at the API level
- Centralized logging and tracing for observability
- Backend services that should remain lightweight and focused on business logic

## Related Patterns

| Pattern | Relation |
|---------|----------|
| Gateway Routing | Complementary |
| Gateway Aggregation | Complementary |
| Ambassador | Distributed alternative |
| Service Mesh | Large-scale evolution |

## Sources

- [Microsoft - Gateway Offloading](https://learn.microsoft.com/en-us/azure/architecture/patterns/gateway-offloading)
- [Kong Gateway](https://konghq.com/)
- [Nginx](https://nginx.org/en/docs/)
