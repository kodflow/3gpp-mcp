# MVP - Model View Presenter

> MVC variant with Presenter instead of Controller

## Concept

The Presenter contains all the presentation logic, the View is passive.

## Recommended Languages

| Language | Framework | Suitability |
|---------|-----------|-----------|
| **Kotlin** | Android (legacy) | Good |
| **Java** | Android (legacy) | Good |
| **C#** | WinForms | Good |

## Structure

```
/src
├── models/              # Data
├── views/               # Passive UI
│   └── interfaces/      # View contracts
└── presenters/          # Presentation logic
```

## Advantages

- Testable View (mock presenter)
- Clear separation
- Passive View = simple

## Disadvantages

- Presenter can grow large
- Boilerplate interfaces
- Less popular today

## When to Use

- Android legacy
- Migration from MVC

## When to Avoid

- New mobile project -> MVVM
- Web -> MVC
- Backend -> Clean/Hexagonal
