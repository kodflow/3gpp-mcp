# Composite

> Compose objects into trees to represent part-whole hierarchies.

---

## Principle

The Composite pattern allows treating objects and compositions uniformly.
Ideal for tree structures (files, menus, organizations).

```text
        ┌────────────┐
        │ Component  │
        │ operation()│
        └─────┬──────┘
              │
    ┌─────────┴─────────┐
    │                   │
┌───┴───┐         ┌─────┴─────┐
│ Leaf  │         │ Composite │
└───────┘         │ children[]│
                  └───────────┘
```

---

## Problem Solved

- Treat simple and composite objects uniformly
- Represent part-whole hierarchies
- Allow recursive operations on the tree
- Simplify client code (no leaf/branch distinction)

---

## Solution

```go
package main

import "fmt"

// Component defines the common interface.
type Component interface {
    GetSize() int64
    GetName() string
    Print(indent string)
}

// File is a leaf.
type File struct {
    name string
    size int64
}

func NewFile(name string, size int64) *File {
    return &File{name: name, size: size}
}

func (f *File) GetSize() int64   { return f.size }
func (f *File) GetName() string  { return f.name }
func (f *File) Print(indent string) {
    fmt.Printf("%s- %s (%d bytes)\n", indent, f.name, f.size)
}

// Directory is a composite.
type Directory struct {
    name     string
    children []Component
}

func NewDirectory(name string) *Directory {
    return &Directory{name: name, children: []Component{}}
}

func (d *Directory) Add(c Component) {
    d.children = append(d.children, c)
}

func (d *Directory) GetSize() int64 {
    var total int64
    for _, child := range d.children {
        total += child.GetSize()
    }
    return total
}

func (d *Directory) GetName() string { return d.name }

func (d *Directory) Print(indent string) {
    fmt.Printf("%s+ %s/\n", indent, d.name)
    for _, child := range d.children {
        child.Print(indent + "  ")
    }
}

// Usage:
// root := NewDirectory("root")
// root.Add(NewFile("readme.txt", 100))
// root.Add(NewDirectory("src"))
// root.Print("")
```

---

## Complete Example

```go
package main

import (
    "fmt"
    "strings"
)

// Employee represents an employee in an organization.
type Employee interface {
    GetName() string
    GetSalary() float64
    GetSubordinates() []Employee
    Add(e Employee)
    Remove(name string)
    Print(indent int)
}

// Developer is a leaf (no subordinates).
type Developer struct {
    name   string
    salary float64
}

func NewDeveloper(name string, salary float64) *Developer {
    return &Developer{name: name, salary: salary}
}

func (d *Developer) GetName() string                { return d.name }
func (d *Developer) GetSalary() float64             { return d.salary }
func (d *Developer) GetSubordinates() []Employee    { return nil }
func (d *Developer) Add(e Employee)                 {} // no-op
func (d *Developer) Remove(name string)             {} // no-op
func (d *Developer) Print(indent int) {
    fmt.Printf("%s- %s (Dev, $%.0f)\n", strings.Repeat("  ", indent), d.name, d.salary)
}

// Manager is a composite (has subordinates).
type Manager struct {
    name         string
    salary       float64
    subordinates []Employee
}

func NewManager(name string, salary float64) *Manager {
    return &Manager{name: name, salary: salary, subordinates: []Employee{}}
}

func (m *Manager) GetName() string    { return m.name }
func (m *Manager) GetSalary() float64 { return m.salary }
func (m *Manager) GetSubordinates() []Employee {
    return m.subordinates
}

func (m *Manager) Add(e Employee) {
    m.subordinates = append(m.subordinates, e)
}

func (m *Manager) Remove(name string) {
    for i, sub := range m.subordinates {
        if sub.GetName() == name {
            m.subordinates = append(m.subordinates[:i], m.subordinates[i+1:]...)
            return
        }
    }
}

func (m *Manager) Print(indent int) {
    prefix := strings.Repeat("  ", indent)
    fmt.Printf("%s+ %s (Manager, $%.0f)\n", prefix, m.name, m.salary)
    for _, sub := range m.subordinates {
        sub.Print(indent + 1)
    }
}

// GetTotalSalary calculates the total salary recursively.
func GetTotalSalary(e Employee) float64 {
    total := e.GetSalary()
    for _, sub := range e.GetSubordinates() {
        total += GetTotalSalary(sub)
    }
    return total
}

func main() {
    // Build the hierarchy
    ceo := NewManager("Alice (CEO)", 200000)

    techVP := NewManager("Bob (VP Tech)", 150000)
    techVP.Add(NewDeveloper("Charlie", 80000))
    techVP.Add(NewDeveloper("Diana", 85000))

    teamLead := NewManager("Eve (Team Lead)", 100000)
    teamLead.Add(NewDeveloper("Frank", 75000))
    teamLead.Add(NewDeveloper("Grace", 78000))
    techVP.Add(teamLead)

    salesVP := NewManager("Henry (VP Sales)", 140000)
    salesVP.Add(NewDeveloper("Ivy", 70000))

    ceo.Add(techVP)
    ceo.Add(salesVP)

    // Display the organization
    fmt.Println("Organization Chart:")
    ceo.Print(0)

    // Calculate total salary
    fmt.Printf("\nTotal Salary: $%.0f\n", GetTotalSalary(ceo))

    // Output:
    // Organization Chart:
    // + Alice (CEO) (Manager, $200000)
    //   + Bob (VP Tech) (Manager, $150000)
    //     - Charlie (Dev, $80000)
    //     - Diana (Dev, $85000)
    //     + Eve (Team Lead) (Manager, $100000)
    //       - Frank (Dev, $75000)
    //       - Grace (Dev, $78000)
    //   + Henry (VP Sales) (Manager, $140000)
    //     - Ivy (Dev, $70000)
    //
    // Total Salary: $978000
}
```

---

## Variants

| Variant | Description | Use Case |
|----------|-------------|----------|
| Transparent | add/remove methods in Component | Maximum uniformity |
| Safe | add/remove methods in Composite | Type safety |
| Cached | Cache for recursive calculations | Performance |

---

## When to Use

- Represent object hierarchies
- Treat leaves and composites uniformly
- Recursive operations on tree structures
- File systems, menus, UI, organizations

## When NOT to Use

- Flat structures without hierarchy
- Little similarity between leaves and composites
- Critical performance (recursion overhead)

---

## Advantages / Disadvantages

| Advantages | Disadvantages |
|-----------|---------------|
| Simplified client code | Difficult to restrict types |
| Easy addition of components | Generalization complicates design |
| Flexible structure | Overhead for small collections |
| Open/Closed Principle | |

---

## Related Patterns

| Pattern | Relationship |
|---------|----------|
| Decorator | Adds behavior, Composite structures |
| Iterator | Traverse composites |
| Visitor | Operations on the hierarchy |
| Flyweight | Share leaves |

---

## Framework Implementations

| Framework/Lib | Implementation |
|---------------|----------------|
| html/template | Node tree |
| go/ast | AST (Abstract Syntax Tree) |
| encoding/xml | DOM structure |

---

## Anti-patterns to Avoid

| Anti-pattern | Problem | Solution |
|--------------|----------|----------|
| Leaky abstraction | Expose leaf/branch difference | Uniform interface |
| Deep nesting | Performance and complexity | Limit depth |
| Circular references | Infinite loops | Validation on add |

---

## Tests

```go
func TestDirectory_GetSize(t *testing.T) {
    root := NewDirectory("root")
    root.Add(NewFile("a.txt", 100))
    root.Add(NewFile("b.txt", 200))

    sub := NewDirectory("sub")
    sub.Add(NewFile("c.txt", 300))
    root.Add(sub)

    expected := int64(600)
    if got := root.GetSize(); got != expected {
        t.Errorf("expected %d, got %d", expected, got)
    }
}

func TestManager_TotalSalary(t *testing.T) {
    boss := NewManager("Boss", 100000)
    boss.Add(NewDeveloper("Dev1", 50000))
    boss.Add(NewDeveloper("Dev2", 60000))

    expected := 210000.0
    if got := GetTotalSalary(boss); got != expected {
        t.Errorf("expected %.0f, got %.0f", expected, got)
    }
}

func TestComposite_Uniform(t *testing.T) {
    // Both implement Component
    var c1 Component = NewFile("file", 100)
    var c2 Component = NewDirectory("dir")

    // Same interface
    _ = c1.GetSize()
    _ = c2.GetSize()
}
```

---

## Sources

- [Refactoring Guru - Composite](https://refactoring.guru/design-patterns/composite)
- [Gang of Four - Design Patterns](https://en.wikipedia.org/wiki/Design_Patterns)
- [Go AST as Composite example](https://pkg.go.dev/go/ast)
