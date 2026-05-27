# Rolling Update

> Progressive update of instances without service interruption.

## Principle

```
┌─────────────────────────────────────────────────────────────────┐
│                    ROLLING UPDATE SEQUENCE                       │
│                                                                  │
│  Initial state     Step 1          Step 2          Final        │
│  ┌─┬─┬─┬─┐        ┌─┬─┬─┬─┐      ┌─┬─┬─┬─┐      ┌─┬─┬─┬─┐     │
│  │1│1│1│1│        │N│1│1│1│      │N│N│1│1│      │N│N│N│N│     │
│  └─┴─┴─┴─┘        └─┴─┴─┴─┘      └─┴─┴─┴─┘      └─┴─┴─┴─┘     │
│  v1 v1 v1 v1      v2 v1 v1 v1    v2 v2 v1 v1    v2 v2 v2 v2   │
│                                                                  │
│  ───────────────────────────────────────────────────────────▶   │
│                          Temps                                   │
└─────────────────────────────────────────────────────────────────┘

Legend: │1│ = v1 (old)  │N│ = v2 (new)
```

## Detailed Workflow

```
┌────────────────────────────────────────────────────────────────┐
│                      LOAD BALANCER                              │
│                           │                                     │
│                           ▼                                     │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │                    REPLICA SET                           │   │
│  │                                                          │   │
│  │   ┌──────┐   ┌──────┐   ┌──────┐   ┌──────┐            │   │
│  │   │Pod 1 │   │Pod 2 │   │Pod 3 │   │Pod 4 │            │   │
│  │   │ v1.0 │   │ v1.0 │   │ v1.0 │   │ v1.0 │            │   │
│  │   │READY │   │READY │   │READY │   │READY │            │   │
│  │   └──────┘   └──────┘   └──────┘   └──────┘            │   │
│  │       │                                                  │   │
│  │       ▼ (1) Terminate                                    │   │
│  │   ┌──────┐                          ┌──────┐            │   │
│  │   │Pod 1 │ ─── (2) Create ────────▶ │Pod 5 │            │   │
│  │   │TERM  │                          │ v1.1 │            │   │
│  │   └──────┘                          │START │            │   │
│  │                                     └──────┘            │   │
│  │                                         │                │   │
│  │                            (3) Ready ───┘                │   │
│  │                                                          │   │
│  └─────────────────────────────────────────────────────────┘   │
└────────────────────────────────────────────────────────────────┘
```

## Configuration Kubernetes

```yaml
# deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: myapp
spec:
  replicas: 4
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxSurge: 1        # Max pods above replicas
      maxUnavailable: 1  # Max unavailable pods
  selector:
    matchLabels:
      app: myapp
  template:
    metadata:
      labels:
        app: myapp
    spec:
      containers:
      - name: app
        image: myapp:1.1.0
        ports:
        - containerPort: 8080

        # Health checks critiques
        readinessProbe:
          httpGet:
            path: /health/ready
            port: 8080
          initialDelaySeconds: 5
          periodSeconds: 5
          failureThreshold: 3

        livenessProbe:
          httpGet:
            path: /health/live
            port: 8080
          initialDelaySeconds: 15
          periodSeconds: 20

        # Graceful shutdown
        lifecycle:
          preStop:
            exec:
              command: ["/bin/sh", "-c", "sleep 10"]

      terminationGracePeriodSeconds: 30
```

## maxSurge / maxUnavailable Strategies

### Conservative (safe default)

```yaml
strategy:
  rollingUpdate:
    maxSurge: 1
    maxUnavailable: 0  # Always full capacity
```

```
Sequence: 4 → 5 → 4 → 5 → 4 → 5 → 4 → 5 → 4
Pods:     [1111] [1111N] [111N] [111NN] [11NN] ...
```

### Aggressive (faster)

```yaml
strategy:
  rollingUpdate:
    maxSurge: 2
    maxUnavailable: 2
```

```
Sequence: 4 → 4 → 4 → 4
Pods:     [1111] [11NN] [NNNN]
          (2 terminated, 2 created simultaneously)
```

### Proportional (large deployments)

```yaml
strategy:
  rollingUpdate:
    maxSurge: 25%
    maxUnavailable: 25%
```

## Error Handling

```yaml
# PodDisruptionBudget for protection
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: myapp-pdb
spec:
  minAvailable: 2  # Always minimum 2 pods
  selector:
    matchLabels:
      app: myapp
```

### Automatic Rollback

```bash
# Check history
kubectl rollout history deployment/myapp

# Rollback to previous version
kubectl rollout undo deployment/myapp

# Rollback to specific version
kubectl rollout undo deployment/myapp --to-revision=2

# Status du rollout
kubectl rollout status deployment/myapp
```

## Essential Health Checks

```go
package health

import (
	"context"
	"encoding/json"
	"net/http"
)

// Status represents health check status.
type Status string

const (
	StatusAlive    Status = "alive"
	StatusReady    Status = "ready"
	StatusNotReady Status = "not_ready"
)

// Check represents a single health check result.
type Check struct {
	Name    string `json:"name"`
	Healthy bool   `json:"healthy"`
}

// Response represents health check response.
type Response struct {
	Status Status  `json:"status"`
	Checks []Check `json:"checks,omitempty"`
}

// Checker defines health check interface.
type Checker interface {
	Check(ctx context.Context) bool
}

// Handler provides HTTP health check endpoints.
type Handler struct {
	database     Checker
	cache        Checker
	dependencies Checker
}

// NewHandler creates a new health check handler.
func NewHandler(database, cache, dependencies Checker) *Handler {
	return &Handler{
		database:     database,
		cache:        cache,
		dependencies: dependencies,
	}
}

// LivenessHandler handles liveness probe requests.
func (h *Handler) LivenessHandler(w http.ResponseWriter, r *http.Request) {
	// Process is alive?
	response:= Response{
		Status: StatusAlive,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}

// ReadinessHandler handles readiness probe requests.
func (h *Handler) ReadinessHandler(w http.ResponseWriter, r *http.Request) {
	ctx:= r.Context()

	// Ready to receive traffic?
	checks:= []Check{
		{Name: "database", Healthy: h.database.Check(ctx)},
		{Name: "cache", Healthy: h.cache.Check(ctx)},
		{Name: "dependencies", Healthy: h.dependencies.Check(ctx)},
	}

	allHealthy:= true
	for _, check:= range checks {
		if !check.Healthy {
			allHealthy = false
			break
		}
	}

	status:= StatusReady
	httpStatus:= http.StatusOK
	if !allHealthy {
		status = StatusNotReady
		httpStatus = http.StatusServiceUnavailable
	}

	response:= Response{
		Status: status,
		Checks: checks,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	_ = json.NewEncoder(w).Encode(response)
}

// Example checker implementations

// DatabaseChecker checks database connectivity.
type DatabaseChecker struct {
	// db connection
}

// Check verifies database is accessible.
func (c *DatabaseChecker) Check(ctx context.Context) bool {
	// Implement database ping
	return true
}

// CacheChecker checks cache connectivity.
type CacheChecker struct {
	// cache connection
}

// Check verifies cache is accessible.
func (c *CacheChecker) Check(ctx context.Context) bool {
	// Implement cache ping
	return true
}

// DependenciesChecker checks external dependencies.
type DependenciesChecker struct {
	// dependency clients
}

// Check verifies dependencies are accessible.
func (c *DependenciesChecker) Check(ctx context.Context) bool {
	// Implement dependency checks
	return true
}
```

## When to Use

| Use | Avoid |
|-----|-------|
| Standard deployments | Breaking DB changes |
| Native Kubernetes/ECS | Instant rollback required |
| Limited resources | Pre-production tests needed |
| Small/medium teams | Metrics validation required |
| Frequent updates | Very risky changes |

## Advantages

- **Simplicity**: Native Kubernetes/ECS
- **Zero-downtime**: Progressive update
- **Resources**: No double infrastructure
- **Automatic**: Built-in health checks
- **Rollback**: History preserved

## Disadvantages

- **Slow rollback**: Not instant
- **Mixed states**: v1 and v2 simultaneously
- **No validation**: No metrics analysis
- **Sessions**: Can be lost
- **DB migrations**: Must be compatible

## Comparison with Other Strategies

```
┌─────────────────────────────────────────────────────────────┐
│                     ROLLOUT TIME                             │
│                                                              │
│  Rolling    ████████████████████████████░░░░░░░░░░░         │
│  Update     (progressive, pods one by one)                   │
│                                                              │
│  Blue-Green ████████████████████████████▌                   │
│             (instant switch)                                 │
│                                                              │
│  Canary     ██████░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░      │
│             (progressive with analysis pauses)               │
└─────────────────────────────────────────────────────────────┘
```

## Real-World Examples

| Company | Usage |
|---------|-------|
| **Kubernetes** | Default strategy |
| **AWS ECS** | Native rolling update |
| **Docker Swarm** | Update policy |
| **GCP Cloud Run** | Traffic migration |

## Best Practices

```yaml
# 1. Always define resource limits
resources:
  requests:
    memory: "128Mi"
    cpu: "250m"
  limits:
    memory: "256Mi"
    cpu: "500m"

# 2. Configure appropriate probes
readinessProbe:
  httpGet:
    path: /ready
    port: 8080
  initialDelaySeconds: 5
  periodSeconds: 5

# 3. Graceful shutdown
lifecycle:
  preStop:
    exec:
      command: ["/bin/sh", "-c", "sleep 10"]

# 4. PodDisruptionBudget
minAvailable: 50%
```

## Migration path

### To Blue-Green

```
1. Duplicate environment
2. Configure traffic switch
3. Automate switchover
```

### To Canary

```
1. Add service mesh (Istio/Linkerd)
2. Configure traffic splitting
3. Add metrics analysis
```

## Related Patterns

| Pattern | Relation |
|---------|----------|
| Blue-Green | Alternative with instant rollback |
| Canary | Evolution with metrics |
| Health Check | Essential for rolling |
| Graceful Shutdown | Avoid request loss |

## Sources

- [Kubernetes Deployments](https://kubernetes.io/docs/concepts/workloads/controllers/deployment/)
- [AWS ECS Rolling Update](https://docs.aws.amazon.com/AmazonECS/latest/developerguide/update-service.html)
- [Container Health Checks](https://kubernetes.io/docs/tasks/configure-pod-container/configure-liveness-readiness-startup-probes/)
