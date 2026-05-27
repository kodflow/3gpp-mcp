# Clean Architecture

> Uncle Bob - Framework independence and maximum testability

## Concept

Concentric layers with dependencies pointing inward only.

## Recommended Languages

| Language | Suitability |
|---------|-----------|
| **Go** | Excellent |
| **Java** | Excellent |
| **TypeScript** | Very good |
| **Kotlin** | Very good |
| **C#** | Excellent |
| **Python** | Good |

## Structure

```
/src
├── entities/            # Business objects (center)
│   └── user.go
├── usecases/            # Application logic
│   ├── create_user.go
│   └── interfaces.go    # Port definitions
├── adapters/            # Interface adapters
│   ├── controllers/     # HTTP handlers
│   ├── presenters/      # Output formatting
│   └── gateways/        # Repository impl
└── frameworks/          # External (DB, Web)
    ├── database/
    └── web/
```

## Advantages

- Framework independent
- Excellent testability
- Protected business logic
- Interchangeability (DB, UI, etc.)
- Long-term maintainability

## Disadvantages

- Verbose
- Over-engineering for small projects
- Learning curve
- Many interfaces

## Constraints

- Dependencies point inward only
- Entities know NOTHING about the outside
- Use cases define interfaces (ports)
- Adapters implement interfaces

## Rules

1. Dependency Rule: dependencies point inward
2. Entities = universal business logic
3. Use Cases = application logic
4. Adapters = in/out translation
5. Frameworks = details (interchangeable)

## When to Use

- Complex applications
- Long-term (>2 years)
- Frequent framework changes
- Critical tests
- Experienced team

## When to Avoid

- Quick POC/MVP
- Simple CRUD -> MVC
- Scripts -> Flat
- Junior team without mentoring
