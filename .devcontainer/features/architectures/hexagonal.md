# Hexagonal / Ports & Adapters

> Alistair Cockburn - Domain at the center, isolated from the outside world

## Concept

The domain is at the center, communicating via ports (interfaces) implemented by adapters.

## Recommended Languages

| Language | Suitability |
|---------|-----------|
| **Go** | Excellent |
| **Java** | Excellent |
| **TypeScript** | Very good |
| **Rust** | Very good |
| **Python** | Good |
| **Scala** | Very good |

## Structure

```
/src
├── domain/              # Business core (no dependencies)
│   ├── model/           # Entities, Value Objects
│   ├── services/        # Domain services
│   └── events/          # Domain events
├── ports/               # Interfaces (contracts)
│   ├── inbound/         # Driven by (API, CLI)
│   │   └── user_service.go
│   └── outbound/        # Driving (DB, external)
│       └── user_repository.go
└── adapters/            # Implementations
    ├── inbound/         # HTTP, gRPC, CLI
    │   └── http/
    └── outbound/        # Postgres, Redis, APIs
        └── postgres/
```

## Advantages

- Domain completely isolated
- Testable without infrastructure
- Adaptable (change DB = change adapter)
- Explicit ports
- In/out symmetry

## Disadvantages

- Much indirection
- Verbose
- Complex for small projects
- Discipline required

## Constraints

- Domain = ZERO external dependencies
- Ports = interfaces in domain
- Adapters = implement ports
- Dependency injection mandatory

## Rules

1. Domain knows only itself
2. Inbound ports = what the app offers
3. Outbound ports = what the app needs
4. One adapter per technology
5. Domain tests without infrastructure mocks

## When to Use

- Apps with rich business logic
- Need for technical flexibility
- Critical tests
- Medium to large team

## When to Avoid

- Simple CRUD -> MVC
- Scripts -> Flat
- Rapid prototyping
