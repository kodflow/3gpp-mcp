# Service Mesh Pattern

> Dedicated infrastructure for inter-service communication with observability, security, and resilience.

---

## Principle

```
┌─────────────────────────────────────────────────────────────────┐
│                      SERVICE MESH                                │
│                                                                  │
│  Without Service Mesh:        With Service Mesh:                │
│                                                                  │
│  ┌─────┐    ┌─────┐           ┌─────┐    ┌─────┐               │
│  │Svc A│───►│Svc B│           │Svc A│    │Svc B│               │
│  └─────┘    └─────┘           └──┬──┘    └──┬──┘               │
│                                  │          │                    │
│  Each service manages:          ┌▼──────────▼┐                  │
│  - Retry                        │  Sidecar   │                  │
│  - Timeout                      │  Proxies   │                  │
│  - TLS                          └──────┬─────┘                  │
│  - Logging                             │                         │
│  - Tracing                      ┌──────▼─────┐                  │
│                                 │Control Plane│                  │
│                                 └────────────┘                  │
│                                                                  │
│                                 Infrastructure manages everything│
└─────────────────────────────────────────────────────────────────┘
```

---

## Components

| Component | Role |
|-----------|------|
| **Data Plane** | Sidecars that intercept traffic |
| **Control Plane** | Centralized configuration and policies |
| **Sidecar Proxy** | Envoy, Linkerd-proxy |
| **Service Discovery** | Service location |
| **Load Balancer** | Traffic distribution |

---

## Features

| Category | Features |
|----------|----------|
| **Traffic** | Load balancing, routing, retries, timeouts |
| **Security** | mTLS, authorization, encryption |
| **Observability** | Metrics, tracing, logging |
| **Resilience** | Circuit breaker, rate limiting, fault injection |

---

## Implementation with Istio (Kubernetes)

### Installation

```yaml
# istio-operator.yaml
apiVersion: install.istio.io/v1alpha1
kind: IstioOperator
metadata:
  name: istio-control-plane
spec:
  profile: default
  components:
    pilot:
      k8s:
        resources:
          requests:
            cpu: 100m
            memory: 128Mi
  meshConfig:
    accessLogFile: /dev/stdout
    enableTracing: true
```

---

### Namespace Configuration

```yaml
# Enable automatic sidecar injection
apiVersion: v1
kind: Namespace
metadata:
  name: my-app
  labels:
    istio-injection: enabled
```

---

### Virtual Service (Routing)

```yaml
apiVersion: networking.istio.io/v1beta1
kind: VirtualService
metadata:
  name: product-service
spec:
  hosts:
    - product-service
  http:
    # Canary deployment: 90% v1, 10% v2
    - match:
        - headers:
            x-canary:
              exact: "true"
      route:
        - destination:
            host: product-service
            subset: v2
    - route:
        - destination:
            host: product-service
            subset: v1
          weight: 90
        - destination:
            host: product-service
            subset: v2
          weight: 10
      retries:
        attempts: 3
        perTryTimeout: 2s
        retryOn: connect-failure,refused-stream,5xx
      timeout: 10s
```

---

### Destination Rule (Load Balancing & TLS)

```yaml
apiVersion: networking.istio.io/v1beta1
kind: DestinationRule
metadata:
  name: product-service
spec:
  host: product-service
  trafficPolicy:
    connectionPool:
      tcp:
        maxConnections: 100
      http:
        h2UpgradePolicy: UPGRADE
        http1MaxPendingRequests: 100
        http2MaxRequests: 1000
    loadBalancer:
      simple: LEAST_CONN
    outlierDetection:
      consecutive5xxErrors: 5
      interval: 30s
      baseEjectionTime: 30s
      maxEjectionPercent: 50
    tls:
      mode: ISTIO_MUTUAL
  subsets:
    - name: v1
      labels:
        version: v1
    - name: v2
      labels:
        version: v2
```

---

### Authorization Policy (Security)

```yaml
apiVersion: security.istio.io/v1beta1
kind: AuthorizationPolicy
metadata:
  name: product-service-policy
  namespace: my-app
spec:
  selector:
    matchLabels:
      app: product-service
  rules:
    # Allow only calls from order-service
    - from:
        - source:
            principals:
              - cluster.local/ns/my-app/sa/order-service
      to:
        - operation:
            methods:
              - GET
              - POST
            paths:
              - /api/products/*
    # Allow health checks
    - to:
        - operation:
            methods:
              - GET
            paths:
              - /health/*
```

---

### Peer Authentication (mTLS)

```yaml
apiVersion: security.istio.io/v1beta1
kind: PeerAuthentication
metadata:
  name: default
  namespace: my-app
spec:
  mtls:
    mode: STRICT  # Enforce mTLS for all services
```

---

## Go Implementation (Dapr)

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	dapr "github.com/dapr/go-sdk/client"
)

// Order represents an order entity.
type Order struct {
	ID        string
	ProductID string
	Quantity  int
	Total     float64
}

// Product represents a product entity.
type Product struct {
	ID    string
	Name  string
	Price float64
}

// OrderService handles order operations via Dapr.
type OrderService struct {
	daprClient dapr.Client
	logger     *slog.Logger
}

// NewOrderService creates a new order service.
func NewOrderService(logger *slog.Logger) (*OrderService, error) {
	if logger == nil {
		logger = slog.Default()
	}

	client, err := dapr.NewClient()
	if err != nil {
		return nil, fmt.Errorf("creating dapr client: %w", err)
	}

	return &OrderService{
		daprClient: client,
		logger:     logger,
	}, nil
}

// CreateOrder creates a new order with Dapr service invocation.
func (s *OrderService) CreateOrder(ctx context.Context, order *Order) (*Order, error) {
	// Call product-service via Dapr sidecar
	// Automatically benefits from:
	// - Service discovery
	// - mTLS
	// - Retries
	// - Tracing
	content := &dapr.DataContent{
		ContentType: "application/json",
	}

	resp, err := s.daprClient.InvokeMethod(
		ctx,
		"product-service",              // App ID
		fmt.Sprintf("products/%s", order.ProductID), // Method
		"get",                           // HTTP method
	)
	if err != nil {
		return nil, fmt.Errorf("invoking product service: %w", err)
	}

	var product Product
	if err := json.Unmarshal(resp, &product); err != nil {
		return nil, fmt.Errorf("unmarshaling product: %w", err)
	}

	order.Total = product.Price * float64(order.Quantity)

	// Publish event via Dapr pub/sub
	orderData, err := json.Marshal(map[string]interface{}{
		"orderId": order.ID,
		"product": product,
	})
	if err != nil {
		return nil, fmt.Errorf("marshaling order data: %w", err)
	}

	if err := s.daprClient.PublishEvent(
		ctx,
		"order-pubsub",   // Pub/sub component
		"order-created",  // Topic
		orderData,
		dapr.PublishEventWithContentType("application/json"),
	); err != nil {
		return nil, fmt.Errorf("publishing event: %w", err)
	}

	// Store state via Dapr state store
	orderJSON, err := json.Marshal(order)
	if err != nil {
		return nil, fmt.Errorf("marshaling order: %w", err)
	}

	if err := s.daprClient.SaveState(
		ctx,
		"order-store",           // State store component
		fmt.Sprintf("order-%s", order.ID), // Key
		orderJSON,               // Value
		nil,                     // Metadata
	); err != nil {
		return nil, fmt.Errorf("saving state: %w", err)
	}

	s.logger.Info("order created",
		"order_id", order.ID,
		"product_id", order.ProductID,
		"total", order.Total,
	)

	return order, nil
}

// GetOrder retrieves an order from Dapr state store.
func (s *OrderService) GetOrder(ctx context.Context, orderID string) (*Order, error) {
	item, err := s.daprClient.GetState(
		ctx,
		"order-store",
		fmt.Sprintf("order-%s", orderID),
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("getting state: %w", err)
	}

	if item.Value == nil {
		return nil, fmt.Errorf("order not found")
	}

	var order Order
	if err := json.Unmarshal(item.Value, &order); err != nil {
		return nil, fmt.Errorf("unmarshaling order: %w", err)
	}

	return &order, nil
}

// Close closes the Dapr client.
func (s *OrderService) Close() error {
	return s.daprClient.Close()
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	service, err := NewOrderService(logger)
	if err != nil {
		logger.Error("creating order service", "error", err)
		os.Exit(1)
	}
	defer service.Close()

	ctx := context.Background()

	order := &Order{
		ID:        "order-123",
		ProductID: "product-456",
		Quantity:  2,
	}

	createdOrder, err := service.CreateOrder(ctx, order)
	if err != nil {
		logger.Error("creating order", "error", err)
		os.Exit(1)
	}

	logger.Info("order created successfully", "order", createdOrder)
}

// Dapr configuration (components)
/*
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: order-store
spec:
  type: state.redis
  version: v1
  metadata:
    - name: redisHost
      value: redis:6379
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: order-pubsub
spec:
  type: pubsub.rabbitmq
  version: v1
  metadata:
    - name: host
      value: amqp://rabbitmq:5672
*/
```

---

## Observability with Service Mesh

```go
package observability

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("order-service")

// ProcessOrder processes an order with custom tracing.
func ProcessOrder(ctx context.Context, order *Order) error {
	// Create a custom span (the mesh already adds HTTP spans)
	ctx, span := tracer.Start(ctx, "process-order",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("order.id", order.ID),
			attribute.Float64("order.total", order.Total),
		),
	)
	defer span.End()

	if err := validateOrder(ctx, order); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	if err := chargePayment(ctx, order); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	if err := updateInventory(ctx, order); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	span.SetStatus(codes.Ok, "order processed successfully")
	return nil
}

func validateOrder(ctx context.Context, order *Order) error {
	ctx, span := tracer.Start(ctx, "validate-order")
	defer span.End()

	// Validation logic
	return nil
}

func chargePayment(ctx context.Context, order *Order) error {
	ctx, span := tracer.Start(ctx, "charge-payment")
	defer span.End()

	// Payment logic
	return nil
}

func updateInventory(ctx context.Context, order *Order) error {
	ctx, span := tracer.Start(ctx, "update-inventory")
	defer span.End()

	// Inventory logic
	return nil
}
```

---

## Solution Comparison

| Feature | Istio | Linkerd | Consul Connect | Dapr |
|---------|-------|---------|----------------|------|
| Complexity | High | Medium | Medium | Low |
| Performance | Good | Excellent | Good | Good |
| mTLS | Yes | Yes | Yes | Yes |
| Traffic Management | Advanced | Basic | Medium | Basic |
| Multi-cluster | Yes | Yes | Yes | Limited |
| Non-Kubernetes | Limited | No | Yes | Yes |

---

## When to Use

- Microservices with complex communication
- Need for zero-trust mTLS
- Distributed observability (tracing)
- Canary deployments, A/B testing
- Mature DevOps team

---

## When NOT to Use

- Only a few services
- Small team without K8s expertise
- Critical latency (proxy overhead)
- Non-Kubernetes environment (limited)

---

## Related Patterns

| Pattern | Relation |
|---------|----------|
| [Sidecar](sidecar.md) | Data plane implementation |
| [Circuit Breaker](../resilience/circuit-breaker.md) | Managed by the mesh |
| [Rate Limiting](../resilience/rate-limiting.md) | Managed by the mesh |
| [API Gateway](api-gateway.md) | Mesh ingress |

---

## Sources

- [Istio Documentation](https://istio.io/latest/docs/)
- [Linkerd Documentation](https://linkerd.io/docs/)
- [Dapr Documentation](https://docs.dapr.io/)
- [CNCF Service Mesh](https://www.cncf.io/projects/)
