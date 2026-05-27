# Gateway Aggregation Pattern

> Aggregate multiple backend requests into a single client request.

## Principle

```
                                    ┌─────────────────────────┐
                                    │    API GATEWAY          │
                                    │    (Aggregation)        │
┌─────────┐    1 request            │                         │
│  Client │ ───────────────────────▶│  ┌───────────────────┐  │
└─────────┘                         │  │   Orchestrator    │  │
     ▲                              │  └─────────┬─────────┘  │
     │                              │            │            │
     │    1 aggregated response     │    ┌───────┼───────┐    │
     └──────────────────────────────│    │       │       │    │
                                    │    ▼       ▼       ▼    │
                                    │  ┌───┐   ┌───┐   ┌───┐  │
                                    │  │ A │   │ B │   │ C │  │
                                    │  └───┘   └───┘   └───┘  │
                                    └─────────────────────────┘
                                           │       │       │
                                           ▼       ▼       ▼
                                    ┌─────────────────────────┐
                                    │      Backend Services    │
                                    └─────────────────────────┘
```

## Problem Solved

```
BEFORE (N client requests):
┌────────┐                 ┌─────────┐
│ Client │ ──────────────▶ │ User    │
│        │ ──────────────▶ │ Orders  │
│        │ ──────────────▶ │ Payment │
│        │ ──────────────▶ │ Reviews │
└────────┘                 └─────────┘

AFTER (1 aggregated request):
┌────────┐        ┌─────────┐        ┌─────────┐
│ Client │ ──────▶│ Gateway │ ──────▶│ Backend │
└────────┘        └─────────┘        └─────────┘
```

## Go Example

```go
package gateway

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// EndpointConfig defines an endpoint to aggregate.
type EndpointConfig struct {
	Name     string
	URL      string
	Timeout  time.Duration
	Required bool
}

// AggregationConfig configures the aggregation behavior.
type AggregationConfig struct {
	Endpoints         []EndpointConfig
	ParallelExecution bool
}

// RequestContext provides context for the aggregation request.
type RequestContext struct {
	AuthToken string
	Params    map[string]string
}

// GatewayAggregator aggregates multiple backend requests.
type GatewayAggregator struct {
	config AggregationConfig
	client *http.Client
}

// NewGatewayAggregator creates a new GatewayAggregator.
func NewGatewayAggregator(config AggregationConfig) *GatewayAggregator {
	return &GatewayAggregator{
		config: config,
		client: &http.Client{},
	}
}

// Aggregate aggregates responses from multiple endpoints.
func (ga *GatewayAggregator) Aggregate(ctx context.Context, reqCtx RequestContext) (map[string]interface{}, error) {
	if ga.config.ParallelExecution {
		return ga.aggregateParallel(ctx, reqCtx)
	}
	return ga.aggregateSequential(ctx, reqCtx)
}

type endpointResult struct {
	Name  string
	Data  interface{}
	Error error
}

func (ga *GatewayAggregator) aggregateParallel(ctx context.Context, reqCtx RequestContext) (map[string]interface{}, error) {
	results := make(chan endpointResult, len(ga.config.Endpoints))

	for _, endpoint := range ga.config.Endpoints {
		go func(ep EndpointConfig) {
			data, err := ga.fetchWithTimeout(ctx, ep.URL, ep.Timeout, reqCtx)
			results <- endpointResult{
				Name:  ep.Name,
				Data:  data,
				Error: err,
			}
		}(endpoint)
	}

	// Collect results
	aggregated := make(map[string]interface{})
	for i := 0; i < len(ga.config.Endpoints); i++ {
		result := <-results

		if result.Error != nil {
			// Check if endpoint is required
			for _, ep := range ga.config.Endpoints {
				if ep.Name == result.Name && ep.Required {
					return nil, fmt.Errorf("required endpoint %s failed: %w", result.Name, result.Error)
				}
			}
			aggregated[result.Name] = nil
		} else {
			aggregated[result.Name] = result.Data
		}
	}

	return aggregated, nil
}

func (ga *GatewayAggregator) aggregateSequential(ctx context.Context, reqCtx RequestContext) (map[string]interface{}, error) {
	result := make(map[string]interface{})

	for _, endpoint := range ga.config.Endpoints {
		data, err := ga.fetchWithTimeout(ctx, endpoint.URL, endpoint.Timeout, reqCtx)
		if err != nil {
			if endpoint.Required {
				return nil, fmt.Errorf("required endpoint %s failed: %w", endpoint.Name, err)
			}
			result[endpoint.Name] = nil
		} else {
			result[endpoint.Name] = data
		}
	}

	return result, nil
}

func (ga *GatewayAggregator) fetchWithTimeout(ctx context.Context, url string, timeout time.Duration, reqCtx RequestContext) (interface{}, error) {
	if timeout == 0 {
		timeout = 5 * time.Second
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(timeoutCtx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Authorization", reqCtx.AuthToken)

	resp, err := ga.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	// Parse response (simplified - should decode JSON)
	return resp.Body, nil
}
```

## Usage

```go
// This example follows the same idiomatic Go patterns
// as the main example above.
// Specific implementation based on standard Go
// interfaces and conventions.
```

## Error Handling Strategies

| Strategy | Description | Use Case |
|----------|-------------|----------|
| **Fail Fast** | Fail if a required service fails | Critical data |
| **Partial Response** | Return available data | Dashboard |
| **Fallback** | Use cache/default on failure | Optimal UX |
| **Timeout Racing** | Return what arrives before timeout | Performance |

## Anti-patterns

| Anti-pattern | Problem | Solution |
|--------------|---------|----------|
| Synchronous aggregation | Latency = sum(latencies) | Parallel execution |
| No timeout | Request blocked indefinitely | Timeout per endpoint |
| Too many services | Fragility, slowness | Limit to 5-7 max |
| Tight coupling | Gateway depends on format | Flexible transformation |

## When to Use

- Mobile clients requiring reduced number of network requests
- Pages or screens aggregating data from multiple microservices
- Public APIs requiring a simplified facade
- Reducing perceived latency through parallel aggregation
- Backend for Frontend (BFF) patterns

## Related Patterns

| Pattern | Relation |
|---------|----------|
| Gateway Routing | Complementary |
| Backend for Frontend (BFF) | Specialization |
| Facade | Similar GoF pattern |
| Circuit Breaker | Call protection |

## Sources

- [Microsoft - Gateway Aggregation](https://learn.microsoft.com/en-us/azure/architecture/patterns/gateway-aggregation)
- [Netflix Zuul](https://github.com/Netflix/zuul)
