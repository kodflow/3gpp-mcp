# MVVM - Model View ViewModel

> **DEFAULT** for mobile projects (Flutter, SwiftUI, Compose)

## Concept

Separation with ViewModel as a reactive intermediary between View and Model.

## Recommended Languages

| Language | Framework | Suitability |
|---------|-----------|-----------|
| **Dart** | Flutter | Excellent |
| **Swift** | SwiftUI | Excellent |
| **Kotlin** | Compose | Excellent |
| **TypeScript** | Vue.js, Angular | Very good |
| **C#** | WPF, MAUI | Excellent |

## Structure

```
/src
├── models/              # Data, entities
│   └── user.dart
├── views/               # UI widgets/components
│   └── user_view.dart
├── viewmodels/          # State + presentation logic
│   └── user_viewmodel.dart
├── services/            # API, persistence
│   └── user_service.dart
└── di/                  # Dependency injection
```

## Advantages

- Bidirectional binding
- Testability (isolated ViewModel)
- Clear UI/Logic separation
- Native reactivity
- Simplified UI code

## Disadvantages

- Verbose (many files)
- Binding learning curve
- Possible over-engineering
- Complex state management

## Constraints

- ViewModel does NOT know View
- View observes ViewModel
- Model is passive (data)
- No logic in View

## Rules

1. One ViewModel per View (or feature)
2. ViewModel exposes observables
3. View ONLY displays and captures events
4. Model = pure data
5. Services for side effects (API, DB)

## When to Use

- Native/cross-platform mobile apps
- Desktop apps (WPF, MAUI)
- Reactive SPA frontend
- Complex UI with state

## When to Avoid

- Backend/API -> no View
- Static sites
- CLI apps
- Scripts
