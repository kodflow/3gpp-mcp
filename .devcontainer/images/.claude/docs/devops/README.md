# DevOps Patterns

Deployment strategies and modern infrastructure practices.

---

## Files

### Deployment Strategies

| File | Content | Usage |
|---------|---------|-------|
| [gitops.md](gitops.md) | Git as source of truth | Declarative deployment |
| [iac.md](iac.md) | Infrastructure as Code | Infrastructure management |
| [feature-toggles.md](feature-toggles.md) | Feature Flags | Dynamic activation |
| [blue-green.md](blue-green.md) | Blue-Green Deployment | Zero-downtime |
| [canary.md](canary.md) | Canary Deployment | Progressive rollout |
| [rolling-update.md](rolling-update.md) | Rolling Update | Progressive update |
| [immutable-infrastructure.md](immutable-infrastructure.md) | Immutable infrastructure | Disposable servers |
| [ab-testing.md](ab-testing.md) | A/B Testing | Experimentation |

### Infrastructure & Tools

| File | Content | Usage |
|---------|---------|-------|
| [vault-patterns.md](vault-patterns.md) | HashiCorp Vault | PKI, VSO, AppRole |
| [terragrunt-patterns.md](terragrunt-patterns.md) | Terragrunt | Multi-environment IaC |
| [terraform-documentation.md](terraform-documentation.md) | Terraform docs | Structure & terraform-docs |
| [cilium-l2-loadbalancer.md](cilium-l2-loadbalancer.md) | Cilium CNI | L2 LoadBalancer bare-metal |
| [ansible-roles-structure.md](ansible-roles-structure.md) | Ansible roles | Validate-first pattern |
| [mcp-optimization.md](mcp-optimization.md) | MCP Context | Tool Search optimization |

---

## Decision Table - Deployment Strategies

| Strategy | Downtime | Risk | Rollback | Infra Cost | Complexity |
|-----------|----------|--------|----------|------------|------------|
| **Recreate** | Yes | High | Slow | Low | Simple |
| **Rolling Update** | No | Medium | Medium | Low | Simple |
| **Blue-Green** | No | Low | Instant | Double | Medium |
| **Canary** | No | Very Low | Fast | +10-20% | High |
| **A/B Testing** | No | Low | Fast | +10-20% | High |

---

## Overview

```
┌─────────────────────────────────────────────────────────────────┐
│                    DEPLOYMENT STRATEGIES                          │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  Recreate     Rolling      Blue-Green    Canary      A/B Test  │
│  ┌───┐        ┌─┬─┬─┐      ┌───┬───┐    ┌───┬─┐    ┌───┬───┐  │
│  │old│        │o│o│n│      │ B │ G │    │99%│1%│   │50%│50%│  │
│  │ ↓ │        │ │n│n│      │   │   │    │old│new   │ A │ B │  │
│  │new│        │n│n│n│      │   │   │    └───┴─┘    └───┴───┘  │
│  └───┘        └─┴─┴─┘      └───┴───┘                           │
│                                                                  │
│  Simple       Progressive  Instant      Progressive Experience │
│  Downtime     No downtime  Rollback     Metrics     Metrics    │
└─────────────────────────────────────────────────────────────────┘
```

---

## When to Use Which Strategy

| Need | Recommended Strategy |
|--------|----------------------|
| MVP / Dev environment | Recreate |
| Production standard | Rolling Update |
| Zero-downtime critical | Blue-Green |
| Metrics validation before rollout | Canary |
| Test UX / Conversion | A/B Testing |
| Infrastructure changes | Immutable Infrastructure |
| Declarative management | GitOps + IaC |
| Dynamic activation/deactivation | Feature Toggles |

---

## Recommended Combinations

### Modern Stack (recommended)

```
GitOps + IaC + Canary + Feature Toggles
         │
         ▼
┌─────────────────────────────────────┐
│  Git Repository (Source of Truth)    │
│  ├── infrastructure/ (Terraform)     │
│  ├── kubernetes/ (manifests)         │
│  └── config/ (feature flags)         │
└─────────────────────────────────────┘
```

### By Team Size

| Team Size | Strategy |
|---------------|-----------|
| Solo / Startup | Recreate + Feature Toggles |
| Small (5-10) | Rolling Update + GitOps |
| Medium (10-50) | Blue-Green + IaC |
| Large (50+) | Canary + A/B + Full GitOps |

---

## Decision Flow

```
                    Need to deploy
                           │
                           ▼
              ┌─── Downtime tolerance? ───┐
              │                            │
            Yes                           No
              │                            │
              ▼                            ▼
          Recreate              ┌── Fast rollback? ──┐
                                │                     │
                              Yes                    No
                                │                     │
                                ▼                     ▼
                ┌── Metrics validation? ──┐    Rolling Update
                │                          │
              Yes                         No
                │                          │
                ▼                          ▼
            Canary                    Blue-Green
```

---

## Tools by Strategy

| Strategy | Tools |
|-----------|--------|
| Blue-Green | AWS CodeDeploy, Kubernetes, Istio |
| Canary | Argo Rollouts, Flagger, Spinnaker |
| Rolling | Kubernetes native, ECS |
| A/B Testing | LaunchDarkly, Split.io, Optimizely |
| GitOps | Argo CD, Flux, Jenkins X |
| IaC | Terraform, Pulumi, CloudFormation |

---

## Related Patterns

| Pattern | Category | Relation |
|---------|-----------|----------|
| Circuit Breaker | cloud/ | Service protection |
| Saga | cloud/ | Distributed transactions |
| Feature Toggles | devops/ | Feature activation |
| Immutable Infrastructure | devops/ | Disposable servers |

---

## Sources

- [Martin Fowler - Deployment Strategies](https://martinfowler.com/bliki/BlueGreenDeployment.html)
- [Kubernetes Deployment Strategies](https://kubernetes.io/docs/concepts/workloads/controllers/deployment/)
- [Argo Rollouts](https://argoproj.github.io/argo-rollouts/)
