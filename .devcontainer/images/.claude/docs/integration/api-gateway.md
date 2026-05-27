# API Gateway Pattern

> Single entry point for all clients, centralizing authentication, routing, and policies.

---

## Principle

```
┌─────────────────────────────────────────────────────────────────┐
│                      API GATEWAY                                 │
│                                                                  │
│   Clients              Gateway                Services           │
│                                                                  │
│  ┌────────┐         ┌─────────────┐        ┌──────────┐         │
│  │  Web   │────────►│             │───────►│ Users    │         │
│  └────────┘         │             │        └──────────┘         │
│                     │             │                              │
│  ┌────────┐         │  ┌───────┐  │        ┌──────────┐         │
│  │ Mobile │────────►│  │ Auth  │  │───────►│ Orders   │         │
│  └────────┘         │  │ Rate  │  │        └──────────┘         │
│                     │  │ Route │  │                              │
│  ┌────────┐         │  │ Cache │  │        ┌──────────┐         │
│  │  IoT   │────────►│  └───────┘  │───────►│ Products │         │
│  └────────┘         │             │        └──────────┘         │
│                     └─────────────┘                              │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

---

## Responsibilities

| Function | Description |
|----------|-------------|
| **Routing** | Direct requests to the correct services |
| **Authentication** | Validate tokens, API keys |
| **Authorization** | Verify permissions |
| **Rate Limiting** | Limit throughput per client |
| **Caching** | Cache responses |
| **Request/Response Transformation** | Adapt formats |
| **Load Balancing** | Distribute load |
| **SSL Termination** | Manage certificates |
| **Logging/Metrics** | Centralized observability |

---

## Go Implementation

### Basic Gateway

```go
package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"time"
)

// RouteConfig defines configuration for a single route.
type RouteConfig struct {
	Path      string
	Target    string
	Auth      bool
	RateLimit *RateLimitConfig
	Cache     *CacheConfig
}

// RateLimitConfig defines rate limiting parameters.
type RateLimitConfig struct {
	WindowMs int
	Max      int
}

// CacheConfig defines caching parameters.
type CacheConfig struct {
	TTL time.Duration
}

// APIGateway implements a basic API gateway.
type APIGateway struct {
	mux    *http.ServeMux
	server *http.Server
	logger *slog.Logger
	routes []RouteConfig
}

// NewAPIGateway creates a new API gateway.
func NewAPIGateway(port int, logger *slog.Logger) *APIGateway {
	if logger == nil {
		logger = slog.Default()
	}

	mux := http.NewServeMux()

	return &APIGateway{
		mux: mux,
		server: &http.Server{
			Addr:         fmt.Sprintf(":%d", port),
			Handler:      mux,
			ReadTimeout:  15 * time.Second,
			WriteTimeout: 15 * time.Second,
			IdleTimeout:  60 * time.Second,
		},
		logger: logger,
		routes: make([]RouteConfig, 0),
	}
}

// RegisterRoute registers a new route with the gateway.
func (g *APIGateway) RegisterRoute(config RouteConfig) error {
	g.routes = append(g.routes, config)

	targetURL, err := url.Parse(config.Target)
	if err != nil {
		return fmt.Errorf("parsing target URL: %w", err)
	}

	proxy := httputil.NewSingleHostReverseProxy(targetURL)

	// Build middleware chain
	handler := g.proxyHandler(proxy, config.Path)

	if config.Cache != nil {
		handler = g.cacheMiddleware(config.Cache)(handler)
	}

	if config.RateLimit != nil {
		handler = g.rateLimitMiddleware(config.RateLimit)(handler)
	}

	if config.Auth {
		handler = g.authMiddleware(handler)
	}

	handler = g.loggingMiddleware(handler)
	handler = g.corsMiddleware(handler)

	g.mux.Handle(config.Path, handler)
	return nil
}

// Start starts the API gateway server.
func (g *APIGateway) Start() error {
	g.logger.Info("starting API gateway", "addr", g.server.Addr)
	return g.server.ListenAndServe()
}

// Shutdown gracefully shuts down the gateway.
func (g *APIGateway) Shutdown(ctx context.Context) error {
	return g.server.Shutdown(ctx)
}

func (g *APIGateway) proxyHandler(proxy *httputil.ReverseProxy, pathPrefix string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = r.URL.Path[len(pathPrefix):]
		if r.URL.Path == "" {
			r.URL.Path = "/"
		}
		proxy.ServeHTTP(w, r)
	})
}

func (g *APIGateway) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		lrw := &loggingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(lrw, r)

		duration := time.Since(start)
		g.logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", lrw.statusCode,
			"duration_ms", duration.Milliseconds(),
			"remote_addr", r.RemoteAddr,
		)
	})
}

type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (lrw *loggingResponseWriter) WriteHeader(code int) {
	lrw.statusCode = code
	lrw.ResponseWriter.WriteHeader(code)
}

func (g *APIGateway) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// User represents an authenticated user.
type User struct {
	ID    string
	Email string
}

func (g *APIGateway) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			g.writeError(w, http.StatusUnauthorized, "missing authorization token")
			return
		}

		token := authHeader
		if len(authHeader) > 7 && authHeader[:7] == "Bearer " {
			token = authHeader[7:]
		}

		user, err := g.validateToken(r.Context(), token)
		if err != nil {
			g.writeError(w, http.StatusUnauthorized, "invalid token")
			return
		}

		ctx := context.WithValue(r.Context(), "user", user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (g *APIGateway) validateToken(ctx context.Context, token string) (*User, error) {
	// Call authentication service
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://auth-service/validate", nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("validating token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("invalid token")
	}

	var user User
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, fmt.Errorf("decoding user: %w", err)
	}

	return &user, nil
}

type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string][]int64
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{
		buckets: make(map[string][]int64),
	}
}

func (g *APIGateway) rateLimitMiddleware(config *RateLimitConfig) func(http.Handler) http.Handler {
	limiter := newRateLimiter()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.RemoteAddr
			now := time.Now().UnixMilli()
			windowStart := now - int64(config.WindowMs)

			limiter.mu.Lock()
			timestamps := limiter.buckets[key]

			// Remove expired timestamps
			valid := make([]int64, 0, len(timestamps))
			for _, ts := range timestamps {
				if ts > windowStart {
					valid = append(valid, ts)
				}
			}

			if len(valid) >= config.Max {
				limiter.mu.Unlock()
				g.writeError(w, http.StatusTooManyRequests, "too many requests")
				return
			}

			valid = append(valid, now)
			limiter.buckets[key] = valid
			limiter.mu.Unlock()

			next.ServeHTTP(w, r)
		})
	}
}

type cacheEntry struct {
	data    []byte
	expires int64
}

type cache struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry
}

func newCache() *cache {
	return &cache{
		entries: make(map[string]*cacheEntry),
	}
}

func (g *APIGateway) cacheMiddleware(config *CacheConfig) func(http.Handler) http.Handler {
	c := newCache()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				next.ServeHTTP(w, r)
				return
			}

			key := r.URL.String()

			c.mu.RLock()
			entry, ok := c.entries[key]
			c.mu.RUnlock()

			now := time.Now().UnixNano()
			if ok && entry.expires > now {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Cache", "HIT")
				w.Write(entry.data)
				return
			}

			crw := &cachingResponseWriter{
				ResponseWriter: w,
				statusCode:     http.StatusOK,
			}

			next.ServeHTTP(crw, r)

			if crw.statusCode == http.StatusOK && len(crw.body) > 0 {
				c.mu.Lock()
				c.entries[key] = &cacheEntry{
					data:    crw.body,
					expires: now + config.TTL.Nanoseconds(),
				}
				c.mu.Unlock()
			}
		})
	}
}

type cachingResponseWriter struct {
	http.ResponseWriter
	body       []byte
	statusCode int
}

func (crw *cachingResponseWriter) Write(b []byte) (int, error) {
	crw.body = append(crw.body, b...)
	return crw.ResponseWriter.Write(b)
}

func (crw *cachingResponseWriter) WriteHeader(code int) {
	crw.statusCode = code
	crw.ResponseWriter.WriteHeader(code)
}

func (g *APIGateway) writeError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
```

---

### Configuration and Usage

```go
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"example.com/app/gateway"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	gw := gateway.NewAPIGateway(3000, logger)

	// Public routes
	if err := gw.RegisterRoute(gateway.RouteConfig{
		Path:   "/api/products/",
		Target: "http://product-service:8080",
		Auth:   false,
		Cache: &gateway.CacheConfig{
			TTL: 60 * time.Second,
		},
	}); err != nil {
		logger.Error("registering route", "error", err)
		os.Exit(1)
	}

	// Authenticated routes
	if err := gw.RegisterRoute(gateway.RouteConfig{
		Path:   "/api/users/",
		Target: "http://user-service:8080",
		Auth:   true,
		RateLimit: &gateway.RateLimitConfig{
			WindowMs: 60000,
			Max:      100,
		},
	}); err != nil {
		logger.Error("registering route", "error", err)
		os.Exit(1)
	}

	if err := gw.RegisterRoute(gateway.RouteConfig{
		Path:   "/api/orders/",
		Target: "http://order-service:8080",
		Auth:   true,
		RateLimit: &gateway.RateLimitConfig{
			WindowMs: 60000,
			Max:      50,
		},
	}); err != nil {
		logger.Error("registering route", "error", err)
		os.Exit(1)
	}

	// Admin routes (strict rate limit)
	if err := gw.RegisterRoute(gateway.RouteConfig{
		Path:   "/api/admin/",
		Target: "http://admin-service:8080",
		Auth:   true,
		RateLimit: &gateway.RateLimitConfig{
			WindowMs: 60000,
			Max:      10,
		},
	}); err != nil {
		logger.Error("registering route", "error", err)
		os.Exit(1)
	}

	// Graceful shutdown
	go func() {
		if err := gw.Start(); err != nil {
			logger.Error("gateway error", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := gw.Shutdown(ctx); err != nil {
		logger.Error("shutdown error", "error", err)
	}
}
```

---

### Request/Response Transformation

```go
package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
)

// TransformConfig defines request/response transformation functions.
type TransformConfig struct {
	Request  func(*http.Request) error
	Response func([]byte) ([]byte, error)
}

// TransformingGateway extends APIGateway with transformation support.
type TransformingGateway struct {
	*APIGateway
}

// NewTransformingGateway creates a new transforming gateway.
func NewTransformingGateway(port int, logger *slog.Logger) *TransformingGateway {
	return &TransformingGateway{
		APIGateway: NewAPIGateway(port, logger),
	}
}

// RegisterTransformRoute registers a route with transformations.
func (g *TransformingGateway) RegisterTransformRoute(config RouteConfig, transform TransformConfig) error {
	// First register the base route
	if err := g.RegisterRoute(config); err != nil {
		return err
	}

	// Wrap with transformation middleware
	handler := g.transformMiddleware(transform)(g.mux)
	g.mux.Handle(config.Path, handler)

	return nil
}

func (g *TransformingGateway) transformMiddleware(transform TransformConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Transform request
			if transform.Request != nil {
				if err := transform.Request(r); err != nil {
					g.writeError(w, http.StatusBadRequest, "request transformation failed")
					return
				}
			}

			// Transform response
			if transform.Response != nil {
				trw := &transformingResponseWriter{
					ResponseWriter: w,
					transform:      transform.Response,
				}
				next.ServeHTTP(trw, r)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

type transformingResponseWriter struct {
	http.ResponseWriter
	transform func([]byte) ([]byte, error)
	buf       bytes.Buffer
}

func (trw *transformingResponseWriter) Write(b []byte) (int, error) {
	return trw.buf.Write(b)
}

func (trw *transformingResponseWriter) Flush() {
	if trw.transform != nil {
		transformed, err := trw.transform(trw.buf.Bytes())
		if err == nil {
			trw.ResponseWriter.Write(transformed)
			return
		}
	}
	trw.ResponseWriter.Write(trw.buf.Bytes())
}

// Usage example: Adapt a legacy API
func setupLegacyRoute(gw *TransformingGateway) error {
	return gw.RegisterTransformRoute(
		RouteConfig{
			Path:   "/api/v2/users/",
			Target: "http://legacy-user-service:8080",
			Auth:   true,
		},
		TransformConfig{
			Request: func(r *http.Request) error {
				// Convert request body format
				if r.Body != nil {
					body, err := io.ReadAll(r.Body)
					if err != nil {
						return err
					}
					// Transform body (e.g., snake_case to camelCase)
					r.Body = io.NopCloser(bytes.NewBuffer(body))
				}
				return nil
			},
			Response: func(data []byte) ([]byte, error) {
				var response map[string]interface{}
				if err := json.Unmarshal(data, &response); err != nil {
					return data, err
				}

				// Add fields, mask others
				response["_links"] = map[string]string{
					"self": "/api/v2/users/" + response["id"].(string),
				}

				return json.Marshal(response)
			},
		},
	)
}
```

---

### API Aggregation

```go
package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
)

// AggregatingGateway extends APIGateway with aggregation support.
type AggregatingGateway struct {
	*APIGateway
}

// NewAggregatingGateway creates a new aggregating gateway.
func NewAggregatingGateway(port int, logger *slog.Logger) *AggregatingGateway {
	return &AggregatingGateway{
		APIGateway: NewAPIGateway(port, logger),
	}
}

// AggregatorFunc defines a function that aggregates data from multiple sources.
type AggregatorFunc func(context.Context, *http.Request) (interface{}, error)

// RegisterAggregateRoute registers a route that aggregates data.
func (g *AggregatingGateway) RegisterAggregateRoute(path string, aggregator AggregatorFunc) {
	g.mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		data, err := aggregator(r.Context(), r)
		if err != nil {
			g.writeError(w, http.StatusInternalServerError, "aggregation failed")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(data)
	})
}

// DashboardData represents aggregated dashboard data.
type DashboardData struct {
	User                map[string]interface{}   `json:"user"`
	RecentOrders        []map[string]interface{} `json:"recentOrders"`
	UnreadNotifications int                      `json:"unreadNotifications"`
	LastLogin           string                   `json:"lastLogin"`
}

// Usage: Dashboard aggregation
func setupDashboard(gw *AggregatingGateway) {
	gw.RegisterAggregateRoute("/api/dashboard", func(ctx context.Context, r *http.Request) (interface{}, error) {
		user := r.Context().Value("user").(*User)

		var wg sync.WaitGroup
		var mu sync.Mutex
		var errs []error

		var userData map[string]interface{}
		var orders []map[string]interface{}
		var notifications map[string]interface{}

		// Parallel calls to multiple services
		wg.Go(func() {
			data, err := fetchJSON(ctx, fmt.Sprintf("http://user-service/users/%s", user.ID))
			if err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
				return
			}
			userData = data
		})

		wg.Go(func() {
			data, err := fetchJSONArray(ctx, fmt.Sprintf("http://order-service/users/%s/orders?limit=5", user.ID))
			if err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
				return
			}
			orders = data
		})

		wg.Go(func() {
			data, err := fetchJSON(ctx, fmt.Sprintf("http://notification-service/users/%s/unread", user.ID))
			if err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
				return
			}
			notifications = data
		})

		wg.Wait()

		if len(errs) > 0 {
			return nil, errs[0]
		}

		return &DashboardData{
			User: map[string]interface{}{
				"id":   userData["id"],
				"name": userData["name"],
			},
			RecentOrders:        orders,
			UnreadNotifications: int(notifications["count"].(float64)),
			LastLogin:           userData["lastLoginAt"].(string),
		}, nil
	})
}

func fetchJSON(ctx context.Context, url string) (map[string]interface{}, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var data map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}

	return data, nil
}

func fetchJSONArray(ctx context.Context, url string) ([]map[string]interface{}, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var data []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}

	return data, nil
}
```

---

## Technologies

| Technology | Type | Usage |
|------------|------|-------|
| Kong | Open Source | Feature-rich, plugins |
| AWS API Gateway | Managed | Serverless, AWS integration |
| Apigee | Enterprise | Google Cloud, analytics |
| Traefik | Cloud Native | Docker/K8s native |
| NGINX | Web Server | Reverse proxy, load balancing |
| Express Gateway | Node.js | JavaScript ecosystem |

---

## When to Use

- Microservices with multiple clients
- Need to centralize authentication
- Rate limiting and quotas per client
- Data aggregation from multiple services
- API versioning

---

## When NOT to Use

- Simple monolithic application
- Service-to-service communication only
- Critical latency (each hop adds delay)
- Team too small to maintain

---

## Related Patterns

| Pattern | Relation |
|---------|----------|
| [BFF](bff.md) | BFF behind the gateway |
| [Sidecar](sidecar.md) | Alternative for certain functions |
| [Rate Limiting](../resilience/rate-limiting.md) | Implemented in the gateway |
| [Circuit Breaker](../resilience/circuit-breaker.md) | Backend protection |

---

## Sources

- [Microsoft - API Gateway](https://learn.microsoft.com/en-us/azure/architecture/microservices/design/gateway)
- [Kong - What is an API Gateway](https://konghq.com/learning-center/api-gateway)
- [NGINX - API Gateway](https://www.nginx.com/learn/api-gateway/)
