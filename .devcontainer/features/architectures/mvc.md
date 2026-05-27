# MVC - Model View Controller

> **DEFAULT** for web scripting projects (PHP, Ruby, Python/Django)

## Concept

Separation into 3 layers: data, presentation, control logic.

## Recommended Languages

| Language | Framework | Suitability |
|---------|-----------|-----------|
| **PHP** | Laravel, Symfony | Excellent |
| **Ruby** | Rails | Excellent |
| **Python** | Django | Excellent |
| **Node.js** | Express | Good |
| **Java** | Spring MVC | Good |

## Structure

```
/src
├── models/              # Data, ORM, validations
│   ├── User.php
│   └── Post.php
├── views/               # Templates, UI
│   ├── layouts/
│   └── pages/
├── controllers/         # Request/response logic
│   ├── UserController.php
│   └── PostController.php
├── routes/              # URL definitions
└── config/
```

## Advantages

- Easy to understand
- Well documented (30+ years)
- Mature frameworks
- Established conventions
- Quick onboarding

## Disadvantages

- Fat controllers possible
- View-Model coupling
- Difficult to test in isolation
- Limited horizontal scaling
- Scattered business logic

## Constraints

- Controller = orchestration only
- No business logic in views
- Models = data + validations
- One controller per resource

## Rules

1. Thin controllers (orchestration)
2. Business logic in Models or Services
3. Views without logic (display only)
4. Explicit and RESTful routes
5. No DB access in Controllers

## When to Use

- Traditional websites
- CRUD apps
- CMS, blogs, simple e-commerce
- Junior team
- Rapid prototyping

## When to Avoid

- Complex business logic -> Clean/Hexagonal
- Need to scale -> Sliceable Monolith
- Pure API (no views) -> Layered
- Mobile app backend -> Clean
