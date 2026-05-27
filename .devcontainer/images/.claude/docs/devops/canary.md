# Canary Deployment

> Progressive deployment to a subset of users for validation.

**Origin:** Canaries in coal mines (early warning)

## Principle

```
┌─────────────────────────────────────────────────────────────────┐
│                         LOAD BALANCER                            │
│                              │                                   │
│                    ┌─────────┴─────────┐                        │
│                    │   Traffic Split   │                        │
│                    └─────────┬─────────┘                        │
│                              │                                   │
│              ┌───────────────┴───────────────┐                  │
│              │ 95%                       5%  │                  │
│              ▼                               ▼                  │
│     ┌─────────────┐                 ┌─────────────┐            │
│     │   STABLE    │                 │   CANARY    │            │
│     │   (v1.0)    │                 │   (v1.1)    │            │
│     │  3 replicas │                 │  1 replica  │            │
│     └─────────────┘                 └─────────────┘            │
│                                            │                    │
│                                     ┌──────┴──────┐            │
│                                     │   Metrics   │            │
│                                     │  Monitoring │            │
│                                     └─────────────┘            │
└─────────────────────────────────────────────────────────────────┘
```

## Rollout Phases

```
Phase 1: Canary 1%          Phase 2: Canary 10%
┌──────────────────┐        ┌──────────────────┐
│ Stable   │Canary │        │ Stable  │ Canary │
│  99%     │  1%   │        │  90%    │  10%   │
│  v1.0    │ v1.1  │        │  v1.0   │  v1.1  │
└──────────────────┘        └──────────────────┘
     │                           │
     │ Metrics OK?               │ Metrics OK?
     ▼                           ▼

Phase 3: Canary 50%          Phase 4: Full rollout
┌──────────────────┐        ┌──────────────────┐
│ Stable  │ Canary │        │      Canary      │
│  50%    │  50%   │        │      100%        │
│  v1.0   │  v1.1  │        │      v1.1        │
└──────────────────┘        └──────────────────┘
```

## Implementation with Argo Rollouts

```yaml
# rollout.yaml
apiVersion: argoproj.io/v1alpha1
kind: Rollout
metadata:
  name: myapp
spec:
  replicas: 10
  strategy:
    canary:
      steps:
      # Phase 1: 5% traffic
      - setWeight: 5
      - pause: {duration: 10m}

      # Phase 2: 25% traffic
      - setWeight: 25
      - pause: {duration: 10m}

      # Phase 3: 50% traffic
      - setWeight: 50
      - pause: {duration: 10m}

      # Phase 4: 100% (automatic)

      # Automatic analysis
      analysis:
        templates:
        - templateName: success-rate
        startingStep: 1

      # Anti-affinity for resilience
      canaryMetadata:
        labels:
          role: canary
      stableMetadata:
        labels:
          role: stable

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
---
# analysis-template.yaml
apiVersion: argoproj.io/v1alpha1
kind: AnalysisTemplate
metadata:
  name: success-rate
spec:
  metrics:
  - name: success-rate
    interval: 1m
    successCondition: result[0] >= 0.95
    failureLimit: 3
    provider:
      prometheus:
        address: http://prometheus:9090
        query: |
          sum(rate(http_requests_total{status=~"2.*",app="myapp",role="canary"}[5m]))
          /
          sum(rate(http_requests_total{app="myapp",role="canary"}[5m]))
```

## Decision Metrics

```go
package canary

import (
	"fmt"
)

// CanaryMetrics holds key metrics for canary evaluation.
type CanaryMetrics struct {
	ErrorRate      float64  `json:"error_rate"`       // Should be < 0.01 (1%)
	LatencyP99     float64  `json:"latency_p99"`      // Should be < 500ms
	SuccessRate    float64  `json:"success_rate"`     // Should be > 0.99 (99%)
	ConversionRate *float64 `json:"conversion_rate,omitempty"`
	RevenuePerUser *float64 `json:"revenue_per_user,omitempty"`
}

// Action defines the canary deployment action.
type Action string

const (
	ActionPromote  Action = "promote"
	ActionPause    Action = "pause"
	ActionRollback Action = "rollback"
)

// Decision represents a canary deployment decision.
type Decision struct {
	Action Action `json:"action"`
	Reason string `json:"reason"`
}

// EvaluateCanary determines the action based on metrics comparison.
func EvaluateCanary(canaryMetrics, baselineMetrics CanaryMetrics) Decision {
	// Rollback if error rate too high
	if canaryMetrics.ErrorRate > 0.01 {
		return Decision{
			Action: ActionRollback,
			Reason: fmt.Sprintf("Error rate too high: %.2f%%", canaryMetrics.ErrorRate*100),
		}
	}

	// Pause if latency degraded significantly
	threshold:= baselineMetrics.LatencyP99 * 1.2
	if canaryMetrics.LatencyP99 > threshold {
		degradation:= ((canaryMetrics.LatencyP99 - baselineMetrics.LatencyP99) / baselineMetrics.LatencyP99) * 100
		return Decision{
			Action: ActionPause,
			Reason: fmt.Sprintf("Latency degraded %.1f%% (threshold: 20%%)", degradation),
		}
	}

	// Rollback if success rate too low
	if canaryMetrics.SuccessRate < 0.99 {
		return Decision{
			Action: ActionRollback,
			Reason: fmt.Sprintf("Success rate too low: %.2f%%", canaryMetrics.SuccessRate*100),
		}
	}

	// Promote if all metrics healthy
	return Decision{
		Action: ActionPromote,
		Reason: "All metrics healthy",
	}
}
```

## Routing Strategies

### By percentage (standard)

```yaml
# Istio VirtualService
apiVersion: networking.istio.io/v1beta1
kind: VirtualService
metadata:
  name: myapp
spec:
  hosts:
  - myapp
  http:
  - route:
    - destination:
        host: myapp
        subset: stable
      weight: 95
    - destination:
        host: myapp
        subset: canary
      weight: 5
```

### By header (internal testing)

```yaml
http:
- match:
  - headers:
      x-canary:
        exact: "true"
  route:
  - destination:
      host: myapp
      subset: canary
- route:
  - destination:
      host: myapp
      subset: stable
```

### By geographic region

```yaml
http:
- match:
  - headers:
      x-region:
        exact: "eu-west-1"
  route:
  - destination:
      host: myapp
      subset: canary
```

## When to Use

| Use | Avoid |
|-----|-------|
| High-traffic applications | Low volume (not enough data) |
| Risky changes | Trivial changes |
| Metrics validation needed | No monitoring |
| Mature DevOps teams | Teams without observability |
| Critical services | Prototypes/MVPs |

## Advantages

- **Minimized risk**: Limited impact if problem occurs
- **Real validation**: Metrics in production
- **Automatic rollback**: Based on metrics
- **Gradual confidence**: Progressive increase
- **Implicit A/B testing**: Version comparison

## Disadvantages

- **Complexity**: Routing infrastructure
- **Observability required**: Essential metrics
- **Deployment time**: Longer than Blue-Green
- **Minimum volume**: Need significant traffic
- **Shared state**: Complex with data

## Real-World Examples

| Company | Implementation |
|---------|----------------|
| **Google** | Progressive rollout GKE |
| **Netflix** | Spinnaker + Kayenta |
| **LinkedIn** | LiX (A/B + Canary) |
| **Facebook** | Gatekeeper system |
| **Spotify** | Backstage + Argo |

## Migration path

### From Blue-Green

```
1. Implement split traffic (Istio, NGINX, etc.)
2. Add Prometheus/Datadog metrics
3. Configure decision thresholds
4. Automate promote/rollback
```

### To Progressive Delivery

```
1. Integrate feature flags
2. Add A/B testing
3. Automate metrics analysis
4. GitOps for configuration
```

## Related Patterns

| Pattern | Relation |
|---------|----------|
| Blue-Green | Predecessor, binary switch |
| A/B Testing | Canary + experimentation |
| Feature Toggles | Granular alternative |
| Circuit Breaker | Automatic protection |

## Checklist

- [ ] Metrics defined (SLI/SLO)
- [ ] Rollback thresholds configured
- [ ] Alerting in place
- [ ] Rollback runbook documented
- [ ] Traffic splitting configured
- [ ] Canary vs stable dashboard

## Sources

- [Argo Rollouts](https://argoproj.github.io/argo-rollouts/)
- [Flagger](https://flagger.app/)
- [Netflix Kayenta](https://netflixtechblog.com/automated-canary-analysis-at-netflix-with-kayenta-3260bc7acc69)
- [Google SRE Book](https://sre.google/sre-book/release-engineering/)
