# Blue-Green Deployment

> Two identical environments enabling instant switchover.

## Principle

```
┌─────────────────────────────────────────────────────────────────┐
│                         LOAD BALANCER                            │
│                              │                                   │
│                    ┌─────────┴─────────┐                        │
│                    │   Router/Switch   │                        │
│                    └─────────┬─────────┘                        │
│                              │                                   │
│              ┌───────────────┼───────────────┐                  │
│              │               │               │                  │
│              ▼               │               ▼                  │
│     ┌─────────────┐          │      ┌─────────────┐            │
│     │    BLUE     │          │      │    GREEN    │            │
│     │   (v1.0)    │ ◀────────┘      │   (v1.1)    │            │
│     │   ACTIVE    │                 │   STANDBY   │            │
│     └─────────────┘                 └─────────────┘            │
│            │                               │                    │
│            ▼                               ▼                    │
│     ┌─────────────┐                 ┌─────────────┐            │
│     │   Blue DB   │                 │  Green DB   │            │
│     └─────────────┘                 └─────────────┘            │
└─────────────────────────────────────────────────────────────────┘
```

## Deployment Workflow

```
Phase 1: Initial state               Phase 2: Deploy to Green
┌──────┐        ┌──────┐            ┌──────┐        ┌──────┐
│ Blue │ ◀─100%─│Router│            │ Blue │ ◀─100%─│Router│
│ v1.0 │        └──────┘            │ v1.0 │        └──────┘
├──────┤                            ├──────┤
│Green │ (idle)                     │Green │ ← Deploy v1.1
│ v1.0 │                            │ v1.1 │
└──────┘                            └──────┘

Phase 3: Tests Green                 Phase 4: Switch traffic
┌──────┐        ┌──────┐            ┌──────┐        ┌──────┐
│ Blue │ ◀─100%─│Router│            │ Blue │        │Router│─100%─▶ │Green│
│ v1.0 │        └──────┘            │ v1.0 │        └──────┘         │ v1.1│
├──────┤            │               ├──────┤                         └──────┘
│Green │ ◀─ Test ──┘                │Green │ ◀─ ACTIVE
│ v1.1 │   (internal)               │ v1.1 │
└──────┘                            └──────┘
```

## Kubernetes Implementation

```yaml
# blue-deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: app-blue
  labels:
    app: myapp
    version: blue
spec:
  replicas: 3
  selector:
    matchLabels:
      app: myapp
      version: blue
  template:
    metadata:
      labels:
        app: myapp
        version: blue
    spec:
      containers:
      - name: app
        image: myapp:1.0.0
        ports:
        - containerPort: 8080
---
# green-deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: app-green
  labels:
    app: myapp
    version: green
spec:
  replicas: 3
  selector:
    matchLabels:
      app: myapp
      version: green
  template:
    metadata:
      labels:
        app: myapp
        version: green
    spec:
      containers:
      - name: app
        image: myapp:1.1.0
        ports:
        - containerPort: 8080
---
# service.yaml - Switch via selector
apiVersion: v1
kind: Service
metadata:
  name: myapp
spec:
  selector:
    app: myapp
    version: blue  # ← Change to "green" to switch
  ports:
  - port: 80
    targetPort: 8080
```

## Switch Script

```bash
#!/bin/bash
# blue-green-switch.sh

CURRENT=$(kubectl get svc myapp -o jsonpath='{.spec.selector.version}')

if [ "$CURRENT" == "blue" ]; then
  NEW="green"
else
  NEW="blue"
fi

echo "Switching from $CURRENT to $NEW..."

# Switch traffic
kubectl patch svc myapp -p "{\"spec\":{\"selector\":{\"version\":\"$NEW\"}}}"

echo "Traffic now routing to $NEW"

# Verify
kubectl get svc myapp -o wide
```

## Go Implementation

```go
package bluegreen

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"
)

// Environment represents a Blue or Green environment.
type Environment string

const (
	Blue  Environment = "blue"
	Green Environment = "green"
)

// Deployment represents a deployment in an environment.
type Deployment struct {
	Env       Environment
	Version   string
	Healthy   bool
	Instances int
}

// BlueGreenController manages switchover between environments.
type BlueGreenController struct {
	blue    atomic.Pointer[Deployment]
	green   atomic.Pointer[Deployment]
	active  atomic.Value // Environment
	router  Router
	checker HealthChecker
}

// Router defines the traffic routing interface.
type Router interface {
	SwitchTo(ctx context.Context, env Environment) error
	GetActiveEnvironment(ctx context.Context) (Environment, error)
}

// HealthChecker verifies the health of a deployment.
type HealthChecker interface {
	Check(ctx context.Context, env Environment) (bool, error)
}

// NewController creates a new Blue-Green controller.
func NewController(router Router, checker HealthChecker) *BlueGreenController {
	c:= &BlueGreenController{
		router:  router,
		checker: checker,
	}
	c.active.Store(Blue)
	return c
}

// Deploy deploys a new version to the inactive environment.
func (c *BlueGreenController) Deploy(ctx context.Context, version string) error {
	inactive:= c.getInactiveEnv()

	deployment:= &Deployment{
		Env:       inactive,
		Version:   version,
		Instances: 3,
	}

	// Store the deployment
	if inactive == Blue {
		c.blue.Store(deployment)
	} else {
		c.green.Store(deployment)
	}

	// Wait for the environment to be healthy
	if err:= c.waitHealthy(ctx, inactive); err != nil {
		return fmt.Errorf("deployment unhealthy: %w", err)
	}

	return nil
}

// Switch routes traffic to the inactive environment.
func (c *BlueGreenController) Switch(ctx context.Context) error {
	inactive:= c.getInactiveEnv()

	// Verify health before switch
	healthy, err:= c.checker.Check(ctx, inactive)
	if err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}
	if !healthy {
		return errors.New("cannot switch: target environment unhealthy")
	}

	// Switch the traffic
	if err:= c.router.SwitchTo(ctx, inactive); err != nil {
		return fmt.Errorf("router switch failed: %w", err)
	}

	c.active.Store(inactive)
	return nil
}

// Rollback reverts to the previous environment.
func (c *BlueGreenController) Rollback(ctx context.Context) error {
	return c.Switch(ctx) // Switch inverse automatiquement
}

func (c *BlueGreenController) getInactiveEnv() Environment {
	if c.active.Load().(Environment) == Blue {
		return Green
	}
	return Blue
}

func (c *BlueGreenController) waitHealthy(ctx context.Context, env Environment) error {
	ticker:= time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			healthy, err:= c.checker.Check(ctx, env)
			if err != nil {
				continue
			}
			if healthy {
				return nil
			}
		}
	}
}
```

## Database Management

### Option 1: Shared database (simple)

```
┌──────┐     ┌──────┐
│ Blue │────▶│  DB  │◀────│Green │
└──────┘     └──────┘     └──────┘

Constraint: Backward-compatible migrations
```

### Option 2: Separate databases with sync

```
┌──────┐     ┌────────┐     ┌──────┐
│ Blue │────▶│Blue DB │     │Green │
└──────┘     └────────┘     └──────┘
                  │              │
                  │ sync         │
                  ▼              ▼
             ┌────────┐     ┌────────┐
             │Replica │────▶│Green DB│
             └────────┘     └────────┘
```

## When to Use

| Use | Avoid |
|-----|-------|
| Critical zero-downtime | Limited budget (double infra) |
| Instant rollback required | Real-time data (DB sync) |
| Mature teams | Incompatible DB schemas |
| Stateless applications | Highly stateful systems |
| Compliance/Audit | Small projects/MVPs |

## Advantages

- **Instant rollback**: Switch back in seconds
- **Zero-downtime**: No service interruption
- **Production testing**: Validate on Green before switch
- **Confidence**: Identical tested environment
- **Conceptual simplicity**: Easy to understand

## Disadvantages

- **Cost**: Permanent double infrastructure
- **DB synchronization**: Complex with data
- **User sessions**: Lost on switch
- **Cold start**: Green may be "cold"
- **DB schemas**: Delicate migrations

## Real-World Examples

| Company | Usage |
|---------|-------|
| **Netflix** | Regional deployments |
| **Amazon** | Critical services |
| **Etsy** | Continuous deploy |
| **Facebook** | Massive infrastructure |

## Migration path

### From Rolling Update

```
1. Create second environment
2. Configure load balancer with routing
3. Automate switch in CI/CD
4. Implement pre-switch health checks
```

### To Canary

```
1. Add progressive routing (1%, 10%, 50%, 100%)
2. Integrate metrics for automatic decisions
3. Keep Blue-Green as fallback
```

## Related Patterns

| Pattern | Relation |
|---------|----------|
| Canary | Evolution with progressive routing |
| Immutable Infrastructure | Blue/Green are immutable |
| Feature Toggles | Alternative for small changes |
| GitOps | Declarative environment management |

## Pre-deployment Checklist

- [ ] Green deployment created and healthy
- [ ] Automated tests passed on Green
- [ ] Database migrated (if applicable)
- [ ] Health checks configured
- [ ] Rollback plan documented
- [ ] Monitoring in place
- [ ] Team alert during switch

## Sources

- [Martin Fowler - Blue Green Deployment](https://martinfowler.com/bliki/BlueGreenDeployment.html)
- [Kubernetes Blue-Green](https://kubernetes.io/blog/2018/04/30/zero-downtime-deployment-kubernetes-jenkins/)
- [AWS Blue-Green](https://docs.aws.amazon.com/whitepapers/latest/blue-green-deployments/welcome.html)
