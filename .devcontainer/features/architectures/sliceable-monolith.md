# Sliceable Monolith

> **RECOMMENDED DEFAULT** for scalable backend/API projects

## Concept

Modular monolith where each domain can be extracted and deployed independently.

## Recommended Languages

| Language | Suitability |
|---------|-----------|
| **Go** | Excellent - native modules |
| **Java** | Excellent - Spring Modulith |
| **Node.js/TS** | Very good - workspaces |
| **Rust** | Very good - Cargo workspace |
| **Python** | Good - packages |
| **Scala** | Good - sbt multi-project |
| **Elixir** | Good - umbrella apps |

## Structure

```
/src
├── shared/                     # Shared Kernel
│   ├── kernel/                 # Types, interfaces
│   └── infra/                  # DB, messaging, config
│
├── domains/                    # Bounded Contexts
│   └── <domain>/
│       ├── api/                # HTTP/gRPC handlers
│       ├── application/        # Use cases
│       ├── domain/             # Business logic
│       ├── infrastructure/     # Implementations
│       ├── Dockerfile          # Standalone deploy
│       └── main.go             # Isolated entry
│
├── cmd/
│   ├── monolith/               # All domains
│   └── <domain>/               # Single domain
│
└── deployments/
    ├── docker-compose.yml
    └── k8s/
```

## Advantages

- Simple dev (monorepo, one build)
- Granular scaling (extract what needs it)
- No duplication (shared kernel)
- Progressive migration (no big bang)
- Easy integrated tests
- Safe refactoring (everything in one repo)

## Disadvantages

- Discipline required (strict boundaries)
- Higher initial complexity
- Requires team conventions
- Shared kernel = potential coupling

## Constraints

- Each domain MUST be autonomous
- Inter-domain communication via events/interfaces
- No direct imports between domains
- Shared kernel minimal and stable
- Each domain has its own Dockerfile

## Rules

1. A domain CANNOT directly import another domain
2. Communication via shared kernel (events, interfaces)
3. Each domain exposes a clear public API
4. Infrastructure per domain (no shared DB)
5. Tests per domain + global integration tests

## Commands

```bash
make build                    # Full monolith
make build-domain D=billing   # Single domain
make extract D=billing        # Prepare extraction
make test                     # Tests all domains
make test-domain D=billing    # Tests one domain
```

## When to Use

- Project that will scale but uncertain where
- Medium team (3-15 devs)
- Need for deployment flexibility
- Clear business domains
- Variable K8s/infra budget

## When to Avoid

- Small project/POC (too much structure)
- Huge team (>30) with clear ownership -> microservices
- Simple script/CLI -> Flat
- Traditional web PHP/Ruby -> MVC
