# Prototype

> Create new objects by cloning an existing instance rather than instantiating it.

---

## Principle

The Prototype pattern allows copying existing objects without depending
on their classes. Useful when creating an object is expensive.

```text
┌─────────────┐         ┌─────────────┐
│  Prototype  │◄────────│   Client    │
│  (Clone())  │         └─────────────┘
└─────────────┘
       ▲
       │
┌──────┴──────┐
│  Concrete   │
│  Prototype  │
└─────────────┘
```

---

## Problem Solved

- Creating complex objects with many parameters
- Duplicating objects without knowing their concrete class
- Avoiding parallel factory hierarchies
- Performance: cloning rather than rebuilding

---

## Solution

```go
package main

import "fmt"

// Cloner defines the cloning interface.
type Cloner interface {
    Clone() Cloner
}

// Document represents a cloneable document.
type Document struct {
    Title    string
    Content  string
    Author   string
    Metadata map[string]string
}

// Clone creates a deep copy of the document.
func (d *Document) Clone() Cloner {
    // Deep copy of the map
    metaCopy := make(map[string]string, len(d.Metadata))
    for k, v := range d.Metadata {
        metaCopy[k] = v
    }

    return &Document{
        Title:    d.Title,
        Content:  d.Content,
        Author:   d.Author,
        Metadata: metaCopy,
    }
}

// Usage:
// template := &Document{Title: "Template", Content: "..."}
// copy := template.Clone().(*Document)
// copy.Title = "New Document"
```

---

## Complete Example

```go
package main

import (
    "encoding/json"
    "fmt"
)

// Shape defines a cloneable shape.
type Shape interface {
    Clone() Shape
    GetInfo() string
}

// Rectangle implements Shape.
type Rectangle struct {
    Width  float64
    Height float64
    Color  string
}

func (r *Rectangle) Clone() Shape {
    return &Rectangle{
        Width:  r.Width,
        Height: r.Height,
        Color:  r.Color,
    }
}

func (r *Rectangle) GetInfo() string {
    return fmt.Sprintf("Rectangle %.2fx%.2f (%s)", r.Width, r.Height, r.Color)
}

// Circle implements Shape.
type Circle struct {
    Radius float64
    Color  string
}

func (c *Circle) Clone() Shape {
    return &Circle{
        Radius: c.Radius,
        Color:  c.Color,
    }
}

func (c *Circle) GetInfo() string {
    return fmt.Sprintf("Circle r=%.2f (%s)", c.Radius, c.Color)
}

// ShapeRegistry manages a cache of prototypes.
type ShapeRegistry struct {
    shapes map[string]Shape
}

func NewShapeRegistry() *ShapeRegistry {
    return &ShapeRegistry{
        shapes: make(map[string]Shape),
    }
}

func (r *ShapeRegistry) Register(name string, shape Shape) {
    r.shapes[name] = shape
}

func (r *ShapeRegistry) Get(name string) (Shape, bool) {
    if shape, ok := r.shapes[name]; ok {
        return shape.Clone(), true
    }
    return nil, false
}

func main() {
    // 1. Create a prototype registry
    registry := NewShapeRegistry()

    // 2. Register prototypes
    registry.Register("red-rect", &Rectangle{Width: 10, Height: 5, Color: "red"})
    registry.Register("blue-circle", &Circle{Radius: 3, Color: "blue"})

    // 3. Clone from the registry
    shape1, _ := registry.Get("red-rect")
    shape2, _ := registry.Get("red-rect")
    shape3, _ := registry.Get("blue-circle")

    // 4. Modify clones independently
    shape1.(*Rectangle).Width = 20

    fmt.Println(shape1.GetInfo()) // Rectangle 20.00x5.00 (red)
    fmt.Println(shape2.GetInfo()) // Rectangle 10.00x5.00 (red) - original dimensions
    fmt.Println(shape3.GetInfo()) // Circle r=3.00 (blue)
}
```

---

## Variants

| Variant | Description | Use Case |
|----------|-------------|-------------|
| Shallow Copy | Copies references | Immutable objects |
| Deep Copy | Recursive copy | Objects with mutable state |
| Registry | Prototype cache | Reusable templates |
| Serialization | Clone via JSON/Gob | Complex objects |

---

## When to Use

- Object creation is expensive (DB, network, computations)
- Need independent copies of complex objects
- Avoid an explosion of factory subclasses
- Template/preset system

## When NOT to Use

- Simple objects with few fields
- No need for copies (pass by value is sufficient)
- Object graphs with complex circular references

---

## Advantages / Disadvantages

| Advantages | Disadvantages |
|-----------|---------------|
| Avoids coupling to classes | Cloning complex objects is difficult |
| Eliminates repetitive init code | Managing circular references |
| Alternative to factories | Deep copy can be expensive |
| Produces preconfigured objects | |

---

## Related Patterns

| Pattern | Relationship |
|---------|----------|
| Factory Method | Alternative: Factory creates, Prototype clones |
| Abstract Factory | Can use Prototype to create products |
| Memento | Similar: state saving vs complete copy |
| Composite | Composites can be cloned recursively |

---

## Framework Implementations

| Framework/Lib | Implementation |
|---------------|----------------|
| Go standard | `encoding/gob` for deep copy |
| copier | `github.com/jinzhu/copier` |
| deepcopy | `github.com/mohae/deepcopy` |

---

## Anti-patterns to Avoid

| Anti-pattern | Problem | Solution |
|--------------|----------|----------|
| Accidental shallow copy | Shared mutation | Explicit deep copy |
| Clone() returns interface{} | Loss of type safety | Return concrete type |
| Forgetting private fields | Incomplete clone | Serialization/reflection |

---

## Tests

```go
func TestDocument_Clone(t *testing.T) {
    original := &Document{
        Title:    "Original",
        Content:  "Content",
        Author:   "Author",
        Metadata: map[string]string{"key": "value"},
    }

    clone := original.Clone().(*Document)

    // Verify copy
    if clone.Title != original.Title {
        t.Errorf("expected %s, got %s", original.Title, clone.Title)
    }

    // Verify independence
    clone.Title = "Modified"
    clone.Metadata["key"] = "modified"

    if original.Title == clone.Title {
        t.Error("clone should be independent")
    }
    if original.Metadata["key"] == clone.Metadata["key"] {
        t.Error("metadata should be deep copied")
    }
}

func TestShapeRegistry(t *testing.T) {
    registry := NewShapeRegistry()
    registry.Register("test", &Rectangle{Width: 10, Height: 5, Color: "red"})

    shape1, ok1 := registry.Get("test")
    shape2, ok2 := registry.Get("test")

    if !ok1 || !ok2 {
        t.Fatal("expected shapes from registry")
    }

    // Modify a clone
    shape1.(*Rectangle).Width = 20

    // The other clone must be unchanged
    if shape2.(*Rectangle).Width != 10 {
        t.Error("clones should be independent")
    }
}
```

---

## Sources

- [Refactoring Guru - Prototype](https://refactoring.guru/design-patterns/prototype)
- [Gang of Four - Design Patterns](https://en.wikipedia.org/wiki/Design_Patterns)
- [Go Patterns - Prototype](https://github.com/tmrts/go-patterns)
