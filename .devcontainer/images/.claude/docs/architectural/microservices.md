# Microservices Architecture

> Decompose an application into independent, separately deployable services.

## Principle

```
┌─────────────────────────────────────────────────────────────────┐
│                          API Gateway                             │
└─────────────────────────────────────────────────────────────────┘
         │              │              │              │
         ▼              ▼              ▼              ▼
    ┌─────────┐   ┌─────────┐   ┌─────────┐   ┌─────────┐
    │  User   │   │  Order  │   │ Product │   │ Payment │
    │ Service │   │ Service │   │ Service │   │ Service │
    └─────────┘   └─────────┘   └─────────┘   └─────────┘
         │              │              │              │
         ▼              ▼              ▼              ▼
    ┌─────────┐   ┌─────────┐   ┌─────────┐   ┌─────────┐
    │ User DB │   │Order DB │   │Product  │   │Payment  │
    │(Postgres)│  │(MongoDB)│   │DB(Redis)│   │DB (SQL) │
    └─────────┘   └─────────┘   └─────────┘   └─────────┘
```

## Characteristics

| Aspect | Microservices |
|--------|---------------|
| Deployment | Independent per service |
| Data | Database per service |
| Communication | API (REST, gRPC, Events) |
| Teams | Autonomous per service |
| Scalability | Horizontal per service |
| Technology | Polyglot possible |

## When to Use

| Use | Avoid |
|-------------|-----------|
| Large team (>20 devs) | Small team (<5) |
| Well-defined domains | Unclear domain |
| Different scale needs | Uniform load |
| Autonomous teams | Centralized team |
| DevOps maturity | No CI/CD |

## Associated Patterns

### Communication

```
┌──────────────────────────────────────────────────────────┐
│                    Synchronous                             │
│  ┌─────────┐        REST/gRPC         ┌─────────┐        │
│  │Service A│ ─────────────────────►  │Service B│        │
│  └─────────┘                          └─────────┘        │
└──────────────────────────────────────────────────────────┘

┌──────────────────────────────────────────────────────────┐
│                    Asynchronous                            │
│  ┌─────────┐     ┌─────────┐          ┌─────────┐        │
│  │Service A│ ──► │  Queue  │ ──►     │Service B│        │
│  └─────────┘     └─────────┘          └─────────┘        │
└──────────────────────────────────────────────────────────┘
```

### Service Discovery

```go
package discovery

import (
	"context"
	"fmt"
	"net/http"
)

// ServiceDiscovery discovers services (Consul, Kubernetes DNS, etc.).
type ServiceDiscovery interface {
	GetService(ctx context.Context, name string) (*ServiceInfo, error)
}

// ServiceInfo contains service location information.
type ServiceInfo struct {
	Name string
	URL  string
	Port int
}

// Example usage
func CallUserService(ctx context.Context, discovery ServiceDiscovery, userID string) error {
	userService, err := discovery.GetService(ctx, "user-service")
	if err != nil {
		return fmt.Errorf("discovering user-service: %w", err)
	}

	url := fmt.Sprintf("%s/users/%s", userService.URL, userID)
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("calling user service: %w", err)
	}
	defer resp.Body.Close()

	return nil
}
```

### Circuit Breaker

```go
package resilience

// See cloud/circuit-breaker.md for the complete implementation

// CircuitBreaker prevents cascading failures.
type CircuitBreaker struct {
	// Implementation details
}

// Example usage
func UseCircuitBreaker() error {
	breaker := NewCircuitBreaker(/* config */)

	result, err := breaker.Execute(func() (interface{}, error) {
		return callUserService()
	})

	if err != nil {
		return err
	}

	// Use result
	return nil
}
```

### Saga Pattern

```go
// See cloud/saga.md for the complete implementation
```

## Microservice Structure

```
user-service/
├── src/
│   ├── domain/           # Business logic
│   ├── application/      # Use cases
│   ├── infrastructure/   # DB, HTTP, Messaging
│   └── main.go
├── Dockerfile
├── k8s/
│   ├── deployment.yaml
│   └── service.yaml
├── tests/
└── go.mod
```

## Anti-patterns

### Distributed Monolith

```
Services too coupled = worse than monolith

┌─────────┐   sync   ┌─────────┐   sync   ┌─────────┐
│Service A│ ◄──────► │Service B│ ◄──────► │Service C│
└─────────┘          └─────────┘          └─────────┘
     │                    │                    │
     └────────────────────┴────────────────────┘
              All depend on each other
```

### Shared Database

```
Shared base = hidden coupling

┌─────────┐   ┌─────────┐   ┌─────────┐
│Service A│   │Service B│   │Service C│
└────┬────┘   └────┬────┘   └────┬────┘
     │             │             │
     └─────────────┼─────────────┘
                   ▼
              ┌─────────┐
              │Shared DB│
              └─────────┘
```

## Migration from Monolith

```
Phase 1: Identify bounded contexts
Phase 2: Strangler Fig pattern
Phase 3: Extract service by service
Phase 4: Decouple the data

┌─────────────────────────────────────────────┐
│               MONOLITH                       │
│  ┌───────┐  ┌───────┐  ┌───────┐           │
│  │ User  │  │ Order │  │Product│           │
│  │Module │  │Module │  │Module │           │
│  └───────┘  └───────┘  └───────┘           │
│                │                            │
│                ▼                            │
│           ┌─────────┐                       │
│           │   DB    │                       │
│           └─────────┘                       │
└─────────────────────────────────────────────┘
                    │
                    │ Strangler Fig
                    ▼
┌──────────┐  ┌──────────┐  ┌─────────────────┐
│  User    │  │  Order   │  │   MONOLITH      │
│ Service  │  │ Service  │  │  (shrinking)    │
└──────────┘  └──────────┘  └─────────────────┘
```

## Checklist Before Adoption

- [ ] Team > 10 people?
- [ ] Clearly delimited domains?
- [ ] Kubernetes/Docker infrastructure?
- [ ] Mature CI/CD?
- [ ] Monitoring/Observability in place?
- [ ] Distributed systems experience?

## Related Patterns

| Pattern | Relationship |
|---------|----------|
| Modular Monolith | Simpler alternative, microservices preparation |
| Event-Driven | Asynchronous communication between services |
| CQRS | Read/write separation per service |
| Saga | Distributed transaction management |

## Sources

- [microservices.io](https://microservices.io/)
- [Martin Fowler - Microservices](https://martinfowler.com/articles/microservices.html)
- [Sam Newman - Building Microservices](https://samnewman.io/books/building_microservices/)
