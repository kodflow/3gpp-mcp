# Package / Library

> **DEFAULT** for reusable libraries

## Concept

Code intended to be imported by other projects.

## Recommended Languages

| Language | Registry | Suitability |
|---------|----------|-----------|
| **Go** | pkg.go.dev | Excellent |
| **Rust** | crates.io | Excellent |
| **Node.js** | npm | Excellent |
| **Python** | PyPI | Very good |
| **Java** | Maven Central | Good |
| **Ruby** | RubyGems | Good |

## Structure

```
/src
├── lib/                 # Public code (exported)
│   ├── client.go
│   ├── types.go
│   └── errors.go
├── internal/            # Private code (not exported)
│   └── helpers.go
├── examples/            # Usage examples
│   └── basic/
├── README.md
├── LICENSE
├── CHANGELOG.md
└── go.mod / package.json / Cargo.toml
```

## Advantages

- Reusable
- Versioned (semver)
- Documented
- Tested
- Maintainable

## Disadvantages

- Public API = commitment
- Sensitive breaking changes
- Documentation mandatory
- Backward compatibility

## Constraints

- Strict semantic versioning
- Stable public API
- Comprehensive documentation
- Tests >90% coverage
- No heavy dependencies

## Rules

1. Minimal public API
2. Internal for implementation
3. Examples mandatory
4. CHANGELOG maintained
5. Breaking = major version

## Conventions

```
v1.0.0 → v1.1.0  # New feature (backward compatible)
v1.1.0 → v1.1.1  # Bug fix
v1.1.1 → v2.0.0  # Breaking change
```

## When to Use

- Code shared between projects
- SDK/Client for API
- Common utilities
- Open source

## When to Avoid

- Final application -> other architecture
- Non-reusable code
- Prototype
