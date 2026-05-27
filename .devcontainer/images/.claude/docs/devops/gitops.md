# GitOps

> Git as source of truth for infrastructure and applications.

## Principle

```
┌─────────────────────────────────────────────────────────────────┐
│                        GIT REPOSITORY                            │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │  infrastructure/                                         │    │
│  │  ├── kubernetes/                                        │    │
│  │  │   ├── deployment.yaml                                │    │
│  │  │   └── service.yaml                                   │    │
│  │  └── terraform/                                         │    │
│  │      └── main.tf                                        │    │
│  └─────────────────────────────────────────────────────────┘    │
└─────────────────────────────────┬───────────────────────────────┘
                                  │
                                  │ sync
                                  ▼
┌─────────────────────────────────────────────────────────────────┐
│                      GITOPS OPERATOR                             │
│              (Argo CD, Flux, Jenkins X)                          │
└─────────────────────────────────┬───────────────────────────────┘
                                  │
                                  │ apply
                                  ▼
┌─────────────────────────────────────────────────────────────────┐
│                    KUBERNETES CLUSTER                            │
│              (Actual state = Desired state in Git)               │
└─────────────────────────────────────────────────────────────────┘
```

## Core Principles

1. **Declarative**: Describe the desired state, not the actions
2. **Versioned**: Everything in Git (history, rollback)
3. **Automated**: Continuous reconciliation
4. **Pull-based**: The operator pulls changes

## Workflow

```
Developer                    Git                     Cluster
    │                         │                         │
    │  1. Push manifest       │                         │
    │ ───────────────────────▶│                         │
    │                         │                         │
    │                         │  2. Detect change       │
    │                         │ ◀───────────────────────│
    │                         │                         │
    │                         │  3. Apply               │
    │                         │ ───────────────────────▶│
    │                         │                         │
    │                         │  4. Report status       │
    │                         │ ◀───────────────────────│
    │                         │                         │
```

## Repo Structure

### Mono-repo

```
gitops-repo/
├── apps/
│   ├── frontend/
│   │   ├── base/
│   │   │   ├── deployment.yaml
│   │   │   └── service.yaml
│   │   └── overlays/
│   │       ├── dev/
│   │       ├── staging/
│   │       └── prod/
│   └── backend/
│       └── ...
├── infrastructure/
│   ├── monitoring/
│   └── ingress/
└── clusters/
    ├── dev/
    ├── staging/
    └── prod/
```

### Multi-repo

```
app-frontend/        # Code + Dockerfile
app-backend/         # Code + Dockerfile
gitops-config/       # Manifests Kubernetes
infrastructure/      # Terraform
```

## Tools

| Tool | Type | Description |
|-------|------|-------------|
| **Argo CD** | Kubernetes GitOps | Rich UI, sync status |
| **Flux** | Kubernetes GitOps | Modular, lightweight |
| **Jenkins X** | CI/CD GitOps | Full pipeline |
| **Terraform** | IaC | Infrastructure cloud |

## Argo CD Example

```yaml
# Application Argo CD
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: my-app
  namespace: argocd
spec:
  project: default
  source:
    repoURL: https://github.com/org/gitops-repo
    targetRevision: main
    path: apps/my-app/overlays/prod
  destination:
    server: https://kubernetes.default.svc
    namespace: production
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
```

## Flux Example

```yaml
# GitRepository
apiVersion: source.toolkit.fluxcd.io/v1
kind: GitRepository
metadata:
  name: my-repo
  namespace: flux-system
spec:
  interval: 1m
  url: https://github.com/org/gitops-repo
  ref:
    branch: main
---
# Kustomization
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: my-app
  namespace: flux-system
spec:
  interval: 10m
  sourceRef:
    kind: GitRepository
    name: my-repo
  path: ./apps/my-app
  prune: true
```

## Advantages

- **Audit**: Complete Git history
- **Rollback**: `git revert`
- **Review**: Pull Request for changes
- **Security**: No direct kubectl access
- **DR**: Rebuild cluster from Git

## Challenges

| Challenge | Solution |
|-----------|----------|
| Secrets | Sealed Secrets, SOPS, Vault |
| Deployment order | Sync waves, dependencies |
| Environments | Kustomize overlays |
| Drift detection | Reconciliation loop |

## Related Patterns

| Pattern | Relation |
|---------|----------|
| Infrastructure as Code | GitOps pour IaC |
| Immutable Infrastructure | Declarative deployment |
| Blue-Green | Via Git branches |

## Sources

- [GitOps - Weaveworks](https://www.weave.works/technologies/gitops/)
- [Argo CD](https://argo-cd.readthedocs.io/)
- [Flux](https://fluxcd.io/)
