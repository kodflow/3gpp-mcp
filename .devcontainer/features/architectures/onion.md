# Onion Architecture

> Jeffrey Palermo - Domain at the center, concentric layers

## Concept

Similar to Clean/Hexagonal but with differently named layers.

## Recommended Languages

| Language | Suitability |
|---------|-----------|
| **C#** | Excellent (.NET) |
| **Java** | Very good |
| **TypeScript** | Good |

## Structure

```
/src
├── core/                # Center - Domain Model
│   └── entities/
├── domain/              # Domain Services
│   └── services/
├── application/         # Use Cases
│   ├── interfaces/
│   └── services/
└── infrastructure/      # External
    ├── persistence/
    └── external/
```

## Advantages

- Isolated domain
- Testability
- Dependencies point toward the center

## Disadvantages

- Confusion with Clean/Hexagonal
- Verbose
- Less documented

## When to Use

- .NET teams
- Enterprise standards

## When to Avoid

- Other languages -> prefer Clean or Hexagonal
- Small projects
