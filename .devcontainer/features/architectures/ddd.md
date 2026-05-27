# DDD - Domain-Driven Design

> Eric Evans - Business domain modeling at the center

## Variants

| Variant | Focus |
|----------|-------|
| **Tactical** | Patterns (Entity, VO, Aggregate) |
| **Strategic** | Bounded Contexts, Ubiquitous Language |
| **+ CQRS** | Separates read/write |
| **+ Event Sourcing** | State via events |

## Recommended Languages

| Language | Suitability |
|---------|-----------|
| **Java** | Excellent |
| **C#** | Excellent |
| **Scala** | Excellent |
| **TypeScript** | Very good |
| **Go** | Good |
| **Python** | Good |

## Structure (Strategic + Tactical)

```
/src
├── contexts/                    # Bounded Contexts
│   └── <context>/
│       ├── domain/
│       │   ├── aggregates/      # Aggregate roots
│       │   ├── entities/        # Entities
│       │   ├── valueobjects/    # Value Objects
│       │   ├── events/          # Domain Events
│       │   ├── repositories/    # Interfaces
│       │   └── services/        # Domain Services
│       ├── application/
│       │   ├── commands/
│       │   ├── queries/
│       │   └── handlers/
│       └── infrastructure/
│           └── persistence/
└── shared/
    └── kernel/                  # Shared Kernel
```

## Advantages

- Business/code alignment
- Ubiquitous language
- Clear boundaries
- Evolvable
- Managed complexity

## Disadvantages

- Complex to learn
- Over-engineering if misused
- Domain experts required
- Verbose

## Constraints

- Aggregate = unit of consistency
- Entity = own identity
- Value Object = immutable, no identity
- Repository = Aggregate persistence only
- Domain Service = logic without entity

## Rules

1. One Aggregate = one transaction
2. Reference Aggregates by ID only
3. Invariants in Aggregate Root
4. Events for inter-context communication
5. Anti-corruption layer between contexts

## When to Use

- Complex business domain
- Rich business logic
- Team with domain experts
- Long-term (>3 years)

## When to Avoid

- Simple CRUD -> MVC
- No domain expert available
- POC/MVP -> Flat or MVC
- Junior team
