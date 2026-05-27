# Architectural Patterns

Software architecture patterns for structuring applications.

---

## Files

| File | Content | Usage |
|---------|---------|-------|
| [monolith.md](monolith.md) | Monolithic architecture | Simple, quick start |
| [modular-monolith.md](modular-monolith.md) | Modular monolith | Structure without distribution |
| [layered.md](layered.md) | Layered architecture | Separation of responsibilities |
| [hexagonal.md](hexagonal.md) | Ports & Adapters | Domain isolation |
| [microservices.md](microservices.md) | Distributed services | Scale and autonomy |
| [cqrs.md](cqrs.md) | Command Query Separation | Separate Read/Write |
| [event-sourcing.md](event-sourcing.md) | Event history | Audit, replay |
| [event-driven.md](event-driven.md) | Event-driven architecture | Asynchronous decoupling |
| [serverless.md](serverless.md) | FaaS / Event-driven | Pay-per-use, auto-scale |

---

## Decision Table

| Architecture | Team | Domain Complexity | Scalability | DevOps |
|--------------|--------|-------------------|-------------|--------|
| **Monolith** | 1-10 | Simple/Medium | Vertical | Basic |
| **Modular Monolith** | 5-30 | Medium/Complex | Vertical | Basic |
| **Layered (N-tier)** | 5-20 | Medium | Vertical | Basic |
| **Hexagonal** | 5-30 | Complex | Vertical | Medium |
| **Microservices** | 20+ | Complex | Horizontal | Advanced |
| **Event Sourcing** | 10+ | Audit required | Horizontal | Advanced |
| **Event-Driven** | 10+ | Asynchronous | Horizontal | Advanced |
| **Serverless** | 1-50 | Variable | Auto | Medium |

---

## Overview

```
┌─────────────────────────────────────────────────────────────────┐
│                 ARCHITECTURAL SPECTRUM                            │
│                                                                  │
│  Monolith ──────────────────────────────────▶ Microservices     │
│                                                                  │
│  ┌─────────┐   ┌─────────┐   ┌─────────┐   ┌─────────────────┐ │
│  │Monolith │   │Modular  │   │Hexagonal│   │  Microservices  │ │
│  │         │   │Monolith │   │         │   │                 │ │
│  │ ┌─────┐ │   │┌─┐┌─┐┌─┐│   │  ┌───┐  │   │ ┌─┐ ┌─┐ ┌─┐    │ │
│  │ │     │ │   ││A││B││C││   │  │ D │  │   │ │S│ │S│ │S│    │ │
│  │ │     │ │   │└─┘└─┘└─┘│   │  │   │  │   │ │1│ │2│ │3│    │ │
│  │ └─────┘ │   │   │     │   │  └───┘  │   │ └─┘ └─┘ └─┘    │ │
│  └─────────┘   └─────────┘   └─────────┘   └─────────────────┘ │
│                                                                  │
│  Simple ◀───────────────────────────────────────────▶ Complex   │
│  Coupled ◀──────────────────────────────────────────▶ Decoupled │
└─────────────────────────────────────────────────────────────────┘
```

---

## Decision Flow

```
                        New project?
                             │
                             ▼
              ┌─── Domain well understood? ───┐
              │                               │
             No                              Yes
              │                               │
              ▼                               ▼
          Monolith              ┌── Team > 20 devs? ──┐
          (explore)             │                      │
                               No                    Yes
                                │                      │
                                ▼                      ▼
                    ┌── Audit/Replay required? ──┐  Microservices
                    │                            │
                   Yes                          No
                    │                            │
                    ▼                            ▼
              Event Sourcing        ┌── Critical domain tests? ──┐
                    +               │                             │
              Event-Driven         Yes                           No
                                    │                             │
                                    ▼                             ▼
                              Hexagonal /                  Modular Monolith
                              Clean Arch                   or Layered
```

---

## Architecture Comparison

### Coupling & Cohesion

| Architecture | Coupling | Cohesion | Testability |
|--------------|----------|----------|-------------|
| Monolith | Strong | Variable | Difficult |
| Modular Monolith | Medium | High | Good |
| Layered | Medium | Medium | Medium |
| Hexagonal | Low | High | Excellent |
| Microservices | Low | High | Excellent |
| Event-Driven | Very low | High | Complex |

### Cost & Complexity

| Architecture | Initial Cost | Maintenance Cost | Ops Complexity |
|--------------|--------------|------------------|----------------|
| Monolith | Low | Growing | Low |
| Modular Monolith | Medium | Stable | Low |
| Layered | Low | Medium | Low |
| Hexagonal | Medium | Stable | Medium |
| Microservices | High | Distributed | High |
| Serverless | Low | Pay-per-use | Medium |

---

## Migration Paths

### Monolith to Microservices

```
Monolith → Modular Monolith → Microservices
    │              │                 │
    ▼              ▼                 ▼
1. Identify       2. Separate into  3. Extract
   bounded           modules with      services
   contexts          clear             one by one
                     interfaces       (Strangler Fig)
```

### To Event Sourcing

```
Traditional CRUD → CQRS → Event Sourcing
       │              │           │
       ▼              ▼           ▼
   1. Separate      2. Add       3. Replace
      read/write      events       state with
      models          as           event
                      side-effect   stream
```

---

## Patterns by Problem

| Problem | Recommended Architecture |
|----------|-------------------------|
| MVP / Startup | Monolith |
| Complex domain | Hexagonal |
| Team > 20 devs | Microservices |
| Audit/Compliance | Event Sourcing |
| High availability | Event-Driven |
| Variable workloads | Serverless |
| Legacy modernization | Modular Monolith |
| Simple API | Layered |

---

## Common Combinations

### Modern Backend

```
Hexagonal + CQRS + Event-Driven
           │
           ▼
┌──────────────────────────────────┐
│  ┌─────────────────────────────┐ │
│  │        API Layer            │ │
│  │  (REST / GraphQL / gRPC)    │ │
│  └─────────────────────────────┘ │
│              │                   │
│  ┌───────────┴───────────┐      │
│  │ Commands    Queries   │      │
│  │    │           │      │      │
│  │ Write DB   Read DB    │      │
│  │    │           │      │      │
│  │    └─── Events ───┘   │      │
│  └───────────────────────┘      │
└──────────────────────────────────┘
```

### Full Serverless

```
Serverless + Event-Driven
           │
           ▼
┌──────────────────────────────────┐
│  API Gateway                     │
│       │                          │
│  ┌────┴────┐                    │
│  │ Lambda  │◀── Events ──┐      │
│  └────┬────┘              │      │
│       │                   │      │
│  ┌────▼────┐    ┌────────▼────┐ │
│  │  DynamoDB   │ EventBridge  │ │
│  └─────────┘    └─────────────┘ │
└──────────────────────────────────┘
```

---

## Related Patterns by Category

| Category | Patterns |
|-----------|----------|
| **Design** | DDD, Clean Architecture |
| **Communication** | REST, gRPC, GraphQL, Events |
| **Data** | CQRS, Event Sourcing, Saga |
| **Resilience** | Circuit Breaker, Bulkhead, Retry |
| **DevOps** | GitOps, Blue-Green, Canary |

---

## Sources

- [Martin Fowler - Software Architecture](https://martinfowler.com/architecture/)
- [Sam Newman - Building Microservices](https://samnewman.io/)
- [Alistair Cockburn - Hexagonal Architecture](https://alistair.cockburn.us/)
- [Eric Evans - Domain-Driven Design](https://domainlanguage.com/)
- [Microsoft - Architecture Patterns](https://docs.microsoft.com/en-us/azure/architecture/patterns/)
