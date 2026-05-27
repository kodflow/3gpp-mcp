# Flat / Scripts

> **DEFAULT** for CLI tools, scripts, POC

## Concept

Minimal structure, files at the same level.

## Recommended Languages

| Language | Suitability |
|---------|-----------|
| **Go** | Excellent (single binary) |
| **Python** | Excellent (scripts) |
| **Rust** | Excellent (CLI) |
| **Bash** | Good (system scripts) |
| **Node.js** | Good |

## Structure

```
/src
├── main.go              # Entry point
├── config.go            # Configuration
├── commands.go          # CLI commands
├── utils.go             # Helpers
└── types.go             # Types/structs
```

Or with lightweight subdirectories:

```
/src
├── cmd/
│   └── main.go
├── internal/
│   ├── config/
│   └── utils/
└── pkg/                 # If reusable
```

## Advantages

- Simple
- Quick to start
- Easy to understand
- Little boilerplate
- One file = visible

## Disadvantages

- Does not scale
- No separation
- Difficult refactoring
- Limited tests

## Constraints

- < 10 files ideally
- < 1000 lines per file
- No complex business logic

## Rules

1. One file per responsibility
2. Explicit naming
3. No complex dependencies
4. Migrate to Clean if it grows

## When to Use

- CLI tools
- System scripts
- POC/Prototypes
- Internal tools
- Automation

## When to Avoid

- Business logic -> Clean/Hexagonal
- Web app -> MVC
- Planned scaling -> Sliceable Monolith
- >2000 lines -> Refactor
