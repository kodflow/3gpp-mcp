# Ambassador Pattern

> Create proxy services to manage communications between clients and services.

## Principle

```
                          ┌──────────────────────────────┐
                          │         AMBASSADOR           │
                          │                              │
┌─────────┐               │  ┌─────────┐   ┌─────────┐  │   ┌─────────┐
│  Client │ ─────────────▶│  │  Proxy  │───│ Logging │  │──▶│ Service │
└─────────┘               │  └─────────┘   └─────────┘  │   └─────────┘
                          │       │                     │
                          │       ▼                     │
                          │  ┌─────────┐   ┌─────────┐  │
                          │  │ Retry   │   │ Monitor │  │
                          │  └─────────┘   └─────────┘  │
                          └──────────────────────────────┘
```

The Ambassador acts as a sidecar that offloads cross-cutting functionality from the main service.

## Responsibilities

| Function | Description |
|----------|-------------|
| **Logging** | Request/response logging |
| **Retry** | Automatic retries |
| **Circuit Breaking** | Cascade failure protection |
| **Authentication** | Token verification |
| **Rate Limiting** | Throughput control |
| **Monitoring** | Metrics and traces |

## Go Example

```go
package ambassador

import (
	"context"
	"fmt"
	"log"
	"math"
	"net/http"
	"time"
)

// CircuitBreakerConfig configure circuit breaker parameters.
type CircuitBreakerConfig struct {
	FailureThreshold int
	Timeout          time.Duration
}

// AmbassadorConfig defines configuration for the Ambassador.
type AmbassadorConfig struct {
	Retries        int
	Timeout        time.Duration
	Logging        bool
	CircuitBreaker *CircuitBreakerConfig
}

// Ambassador handles cross-cutting concerns like retry, logging, and circuit breaking.
type Ambassador struct {
	targetURL      string
	config         AmbassadorConfig
	circuitBreaker *CircuitBreaker
	client         *http.Client
}

// NewAmbassador creates a new Ambassador instance.
func NewAmbassador(targetURL string, config AmbassadorConfig) *Ambassador {
	a := &Ambassador{
		targetURL: targetURL,
		config:    config,
		client: &http.Client{
			Timeout: config.Timeout,
		},
	}

	if config.CircuitBreaker != nil {
		a.circuitBreaker = NewCircuitBreaker(*config.CircuitBreaker)
	}

	return a
}

// Forward forwards a request with retry logic and logging.
func (a *Ambassador) Forward(ctx context.Context, req *http.Request) (*http.Response, error) {
	startTime := time.Now()

	// Logging entry
	if a.config.Logging {
		log.Printf("[Ambassador] %s %s", req.Method, req.URL.Path)
	}

	// Retry wrapper
	var lastErr error
	for attempt := 0; attempt <= a.config.Retries; attempt++ {
		resp, err := a.executeWithTimeout(ctx, req)
		if err == nil {
			// Logging output
			if a.config.Logging {
				log.Printf("[Ambassador] Response in %v", time.Since(startTime))
			}
			return resp, nil
		}

		lastErr = err
		if attempt < a.config.Retries {
			// Exponential backoff
			backoff := time.Duration(math.Pow(2, float64(attempt))) * 100 * time.Millisecond
			time.Sleep(backoff)
		}
	}

	return nil, fmt.Errorf("all retries failed: %w", lastErr)
}

func (a *Ambassador) executeWithTimeout(ctx context.Context, req *http.Request) (*http.Response, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, a.config.Timeout)
	defer cancel()

	req = req.WithContext(timeoutCtx)

	if a.circuitBreaker != nil {
		return a.circuitBreaker.Call(func() (*http.Response, error) {
			return a.client.Do(req)
		})
	}

	return a.client.Do(req)
}
```

## Usage with Kubernetes Sidecar

```yaml
# Deployment with Ambassador sidecar
apiVersion: apps/v1
kind: Deployment
spec:
  template:
    spec:
      containers:
        - name: main-service
          image: my-service:latest
          ports:
            - containerPort: 8080
        - name: ambassador
          image: envoy:latest
          ports:
            - containerPort: 9000
```

## Use Cases

| Scenario | Benefit |
|----------|---------|
| Legacy microservices | Add resilience without modifying code |
| Multi-cloud | Cloud-specific abstraction |
| Compliance | Centralized logging for audit |
| Migration | Progressive transition to new protocols |

## Anti-patterns

| Anti-pattern | Problem | Solution |
|--------------|---------|----------|
| Heavyweight ambassador | Excessive latency | Keep lightweight, delegate to mesh |
| Business logic | Tight coupling | Ambassador = cross-cutting only |
| No monitoring | Difficult debugging | Always expose metrics |

## When to Use

- Legacy services requiring cross-cutting functionality without code modification
- Need to add retry, circuit breaker or logging transparently
- Multi-cloud environments requiring cloud-specific abstraction
- Progressive migration to new protocols or communication patterns
- Compliance and audit requiring centralized communication logging

## Related Patterns

| Pattern | Relation |
|---------|----------|
| Sidecar | Concrete implementation |
| Circuit Breaker | Embedded functionality |
| Gateway | Centralized alternative |
| Service Mesh | Large-scale evolution |

## Sources

- [Microsoft - Ambassador Pattern](https://learn.microsoft.com/en-us/azure/architecture/patterns/ambassador)
- [Envoy Proxy](https://www.envoyproxy.io/)
