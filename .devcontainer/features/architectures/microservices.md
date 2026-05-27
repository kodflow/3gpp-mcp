# Microservices

> Independent services, separately deployable

## Concept

Each service = one repo, one team, one deployment.

## Recommended Languages

| Language | Suitability |
|---------|-----------|
| **Go** | Excellent |
| **Java** | Excellent |
| **Node.js** | Very good |
| **Python** | Good |
| **Rust** | Good |

## Structure (multi-repo)

```
# Repo: user-service
/src
├── api/
├── domain/
├── infrastructure/
├── Dockerfile
└── k8s/

# Repo: order-service
/src
├── api/
├── domain/
├── infrastructure/
├── Dockerfile
└── k8s/

# Repo: shared-libs (optional)
/packages
├── auth-client/
└── common-types/
```

## Advantages

- Independent scaling
- Independent deployment
- Autonomous teams
- Technology per service
- Fault isolation

## Disadvantages

- Operational complexity
- Network latency
- Distributed debugging
- Distributed transactions
- Possible duplication

## Constraints

- One service = one responsibility
- Async communication preferred
- No shared DB
- Strict API contracts
- Observability mandatory

## Rules

1. One service <= 1 team (2 pizza rule)
2. API first (OpenAPI, gRPC)
3. Always backward compatible
4. Circuit breakers mandatory
5. Distributed tracing required

## When to Use

- Large organization (>30 devs)
- Massive scaling required
- Independent teams
- Polyglot assumed

## When to Avoid

- Small team (<10) -> Sliceable Monolith
- MVP/POC -> Monolith
- Limited ops budget
- No mature DevOps
