# Creational Patterns (GoF)

Object creation patterns.

## Detailed Files

| Pattern | File | Description |
|---------|---------|-------------|
| Builder | [builder.md](builder.md) | Complex step-by-step construction |
| Factory Method / Abstract Factory | [factory.md](factory.md) | Creation delegation |
| Prototype | [prototype.md](prototype.md) | Clone existing objects |
| Singleton | [singleton.md](singleton.md) | Unique instance + DI alternatives |

## The 5 Patterns

### 1. Factory Method

> Delegate creation to subclasses.

See detailed file: [factory.md](factory.md)

```go
package factory

// Logger defines the logging interface.
type Logger interface {
    Log(message string)
}

// LoggerFactory creates loggers.
type LoggerFactory interface {
    CreateLogger() Logger
}

// ConsoleLogger logs to console.
type ConsoleLogger struct{}

func (c *ConsoleLogger) Log(message string) {
    fmt.Println(message)
}

// ConsoleLoggerFactory creates console loggers.
type ConsoleLoggerFactory struct{}

func (f *ConsoleLoggerFactory) CreateLogger() Logger {
    return &ConsoleLogger{}
}

// Usage
func LogMessage(factory LoggerFactory, message string) {
    logger := factory.CreateLogger()
    logger.Log(message)
}
```

**When:** Creation delegated to subclasses.

---

### 2. Abstract Factory

> Families of related objects.

See detailed file: [factory.md](factory.md)

```go
package factory

// Button defines button interface.
type Button interface {
    Render() string
}

// Input defines input interface.
type Input interface {
    Render() string
}

// UIFactory creates UI components.
type UIFactory interface {
    CreateButton() Button
    CreateInput() Input
}

// MaterialButton is a material design button.
type MaterialButton struct{}

func (b *MaterialButton) Render() string { return "<material-button/>" }

// MaterialInput is a material design input.
type MaterialInput struct{}

func (i *MaterialInput) Render() string { return "<material-input/>" }

// MaterialUIFactory creates material UI components.
type MaterialUIFactory struct{}

func (f *MaterialUIFactory) CreateButton() Button { return &MaterialButton{} }
func (f *MaterialUIFactory) CreateInput() Input   { return &MaterialInput{} }
```

**When:** Multiple families of coherent objects.

---

### 3. Builder

> Complex step-by-step construction.

See detailed file: [builder.md](builder.md)

```go
package builder

// QueryBuilder builds SQL queries.
type QueryBuilder struct {
    columns []string
    table   string
    where   string
}

func NewQueryBuilder() *QueryBuilder {
    return &QueryBuilder{}
}

func (qb *QueryBuilder) Select(columns []string) *QueryBuilder {
    qb.columns = columns
    return qb
}

func (qb *QueryBuilder) From(table string) *QueryBuilder {
    qb.table = table
    return qb
}

func (qb *QueryBuilder) Where(condition string) *QueryBuilder {
    qb.where = condition
    return qb
}

func (qb *QueryBuilder) Build() string {
    return fmt.Sprintf("SELECT %s FROM %s WHERE %s",
        strings.Join(qb.columns, ", "), qb.table, qb.where)
}

// Usage
// query := NewQueryBuilder().
//     Select([]string{"id", "name"}).
//     From("users").
//     Where("active = true").
//     Build()
```

**When:** Complex objects with many options.

---

### 4. Prototype

> Clone existing objects.

```go
package prototype

// Prototype defines cloneable objects.
type Prototype[T any] interface {
    Clone() T
}

// Document is a cloneable document.
type Document struct {
    Title    string
    Content  string
    Metadata map[string]string
}

func (d *Document) Clone() *Document {
    // Deep copy metadata
    metaCopy := make(map[string]string, len(d.Metadata))
    for k, v := range d.Metadata {
        metaCopy[k] = v
    }

    return &Document{
        Title:    d.Title,
        Content:  d.Content,
        Metadata: metaCopy,
    }
}
```

**When:** High creation cost, copy is more efficient.

---

### 5. Singleton

> Unique global instance.

See detailed file: [singleton.md](singleton.md)

```go
package singleton

import "sync"

// Database represents a database connection.
type Database struct {
    connectionString string
}

// GetDB returns the singleton database instance.
// sync.OnceValue (Go 1.21+) is type-safe and concise.
var GetDB = sync.OnceValue(func() *Database {
    return &Database{
        connectionString: "postgres://localhost:5432/mydb",
    }
})

// Usage
// db1 := GetDB()
// db2 := GetDB()
// fmt.Println(db1 == db2) // true
```

**When:** A single instance required (caution: often an anti-pattern).

---

## Decision Table

| Need | Pattern |
|--------|---------|
| Delegate creation to subclasses | Factory Method |
| Families of coherent objects | Abstract Factory |
| Complex/optional construction | Builder |
| Cloning more efficient than creation | Prototype |
| Unique instance | Singleton |

## Modern Alternatives

| Pattern | Alternative |
|---------|-------------|
| Factory | Dependency Injection |
| Singleton | DI Container (scoped) |
| Builder | Functional Options |

## Sources

- [Refactoring Guru - Creational Patterns](https://refactoring.guru/design-patterns/creational-patterns)
