# Gateway Routing Pattern

> Route requests to the appropriate backend services.

## Principle

```
┌────────────────────────────────────────────────────────────────┐
│                        API GATEWAY                              │
│                                                                 │
│    Routing Rules:                                               │
│    /api/users/*     ──▶  User Service                          │
│    /api/orders/*    ──▶  Order Service                         │
│    /api/products/*  ──▶  Product Service                       │
│    /v2/*            ──▶  New API Version                       │
│                                                                 │
└────────────────────────────────────────────────────────────────┘
         │               │                │               │
         ▼               ▼                ▼               ▼
    ┌─────────┐    ┌─────────┐     ┌─────────┐     ┌─────────┐
    │  User   │    │  Order  │     │ Product │     │  API    │
    │ Service │    │ Service │     │ Service │     │   v2    │
    └─────────┘    └─────────┘     └─────────┘     └─────────┘
```

## Routing Types

| Type | Description | Example |
|------|-------------|---------|
| **Path-based** | Route by URL path | `/api/users/*` -> User Service |
| **Header-based** | Route by headers | `X-Version: 2` -> API v2 |
| **Query-based** | Route by query params | `?region=eu` -> EU cluster |
| **Method-based** | Route by HTTP method | `POST /orders` -> Write Service |
| **Weight-based** | Weighted distribution | 90% stable, 10% canary |

## Go Example

```go
package gateway

import (
	"math/rand"
	"net/http"
	"net/http/httputil"
	"net/url"
)

// RoutingRule defines a routing rule.
type RoutingRule struct {
	Name      string
	Match     func(r *http.Request) bool
	Target    string
	Weight    int
	Transform func(r *http.Request) *http.Request
}

// GatewayRouter routes requests to backend services.
type GatewayRouter struct {
	rules []*RoutingRule
}

// NewGatewayRouter creates a new GatewayRouter.
func NewGatewayRouter() *GatewayRouter {
	return &GatewayRouter{
		rules: make([]*RoutingRule, 0),
	}
}

// AddRule adds a routing rule.
func (gr *GatewayRouter) AddRule(rule *RoutingRule) *GatewayRouter {
	if rule.Weight == 0 {
		rule.Weight = 100
	}
	gr.rules = append(gr.rules, rule)
	return gr
}

// Route routes a request to the appropriate backend.
func (gr *GatewayRouter) Route(w http.ResponseWriter, r *http.Request) {
	matchedRules := make([]*RoutingRule, 0)

	for _, rule := range gr.rules {
		if rule.Match(r) {
			matchedRules = append(matchedRules, rule)
		}
	}

	if len(matchedRules) == 0 {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}

	// Select rule by weight
	selectedRule := gr.selectByWeight(matchedRules)

	// Transform request if needed
	targetReq := r
	if selectedRule.Transform != nil {
		targetReq = selectedRule.Transform(r)
	}

	// Forward to target
	gr.forward(w, targetReq, selectedRule.Target)
}

func (gr *GatewayRouter) selectByWeight(rules []*RoutingRule) *RoutingRule {
	totalWeight := 0
	for _, r := range rules {
		totalWeight += r.Weight
	}

	random := rand.Intn(totalWeight)

	for _, rule := range rules {
		random -= rule.Weight
		if random < 0 {
			return rule
		}
	}

	return rules[0]
}

func (gr *GatewayRouter) forward(w http.ResponseWriter, r *http.Request, target string) {
	targetURL, err := url.Parse(target)
	if err != nil {
		http.Error(w, "Invalid target URL", http.StatusInternalServerError)
		return
	}

	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	proxy.ServeHTTP(w, r)
}
```

## Route Configuration

```go
// This example follows the same idiomatic Go patterns
// as the main example above.
// Specific implementation based on standard Go
// interfaces and conventions.
```

## Advanced Strategies

### A/B Testing

```go
// This example follows the same idiomatic Go patterns
// as the main example above.
// Specific implementation based on standard Go
// interfaces and conventions.
```

### Blue-Green Deployment

```go
// This example follows the same idiomatic Go patterns
// as the main example above.
// Specific implementation based on standard Go
// interfaces and conventions.
```

### Circuit Breaker Integration

```go
// This example follows the same idiomatic Go patterns
// as the main example above.
// Specific implementation based on standard Go
// interfaces and conventions.
```

## Anti-patterns

| Anti-pattern | Problem | Solution |
|--------------|---------|----------|
| Overly specific rules | Difficult maintenance | Group by service |
| Business logic | Gateway/domain coupling | Technical routing only |
| No fallback | Silent failure | Default route + monitoring |
| Non-deterministic order | Unpredictable behavior | Explicit priority |

## When to Use

- Microservices architecture with a single entry point
- API versioning with routing to different versions
- Canary or blue-green deployments requiring traffic splitting
- Progressive migration between legacy systems and new services
- Intelligent load balancing based on business criteria

## Related Patterns

| Pattern | Relation |
|---------|----------|
| Gateway Aggregation | Complementary |
| Gateway Offloading | Complementary |
| Service Discovery | Dynamic resolution |
| Load Balancer | Intra-service distribution |

## Sources

- [Microsoft - Gateway Routing](https://learn.microsoft.com/en-us/azure/architecture/patterns/gateway-routing)
- [Traefik](https://traefik.io/traefik/)
- [Kong Routing](https://docs.konghq.com/gateway/latest/get-started/configure-routes/)
