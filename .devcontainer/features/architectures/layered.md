# Layered / N-Tier

> Traditional horizontal layered architecture

## Concept

Stacked layers: presentation -> business -> data. Each layer only communicates with the one below.

## Recommended Languages

| Language | Suitability |
|---------|-----------|
| **Java** | Excellent (classic) |
| **C#** | Excellent (.NET) |
| **Python** | Good |
| **PHP** | Good |
| **Node.js** | Good |

## Structure

```
/src
├── presentation/        # UI, API controllers
│   ├── controllers/
│   └── views/
├── business/            # Business logic
│   ├── services/
│   └── validators/
└── data/                # Data access
    ├── repositories/
    └── entities/
```

## Advantages

- Easy to understand
- Well known (classic)
- Clear separation
- Easy to debug

## Disadvantages

- Rigid (changes traverse all layers)
- DB-centric (data drives everything)
- Diluted business logic
- Average testability

## Constraints

- Layer N calls layer N-1 only
- No layer skipping
- No reverse references

## Rules

1. Presentation -> Business -> Data
2. No business logic in presentation
3. No DB access in presentation
4. Business does not know about presentation

## When to Use

- Traditional CRUD apps
- Junior teams
- Legacy code migration
- Enterprise constraints

## When to Avoid

- Complex business logic -> Clean/Hexagonal
- Need for flexibility -> Hexagonal
- Scaling -> Sliceable Monolith
