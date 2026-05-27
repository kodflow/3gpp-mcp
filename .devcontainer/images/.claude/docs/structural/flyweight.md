# Flyweight

> Minimize memory by sharing data between similar objects.

---

## Principle

The Flyweight pattern stores a single instance of repetitive data
(intrinsic state) and passes variable state as operation parameters.

```text
┌─────────────────┐
│ FlyweightFactory│
│  cache: map     │────────┐
└────────┬────────┘        │
         │ get()           │
         ▼                 ▼
┌─────────────────┐   ┌─────────────┐
│   Flyweight     │   │  Flyweight  │
│ (intrinsic)     │   │ (shared)    │
└─────────────────┘   └─────────────┘
```

---

## Problem Solved

- Large quantity of similar objects in memory
- Repeated data between objects (e.g. fonts, textures)
- Prohibitive memory cost
- Immutability of shared data

---

## Solution

```go
package main

import (
    "fmt"
    "sync"
)

// TreeType is the flyweight (shared intrinsic state).
type TreeType struct {
    Name    string
    Color   string
    Texture string
}

func (t *TreeType) Draw(x, y int) {
    fmt.Printf("Drawing %s tree at (%d, %d)\n", t.Name, x, y)
}

// TreeFactory manages the flyweight cache.
type TreeFactory struct {
    mu    sync.RWMutex
    cache map[string]*TreeType
}

func NewTreeFactory() *TreeFactory {
    return &TreeFactory{
        cache: make(map[string]*TreeType),
    }
}

func (f *TreeFactory) GetTreeType(name, color, texture string) *TreeType {
    key := name + "_" + color + "_" + texture

    f.mu.RLock()
    if tt, ok := f.cache[key]; ok {
        f.mu.RUnlock()
        return tt
    }
    f.mu.RUnlock()

    f.mu.Lock()
    defer f.mu.Unlock()

    // Double-check
    if tt, ok := f.cache[key]; ok {
        return tt
    }

    tt := &TreeType{Name: name, Color: color, Texture: texture}
    f.cache[key] = tt
    return tt
}

// Tree contains the extrinsic state (unique per instance).
type Tree struct {
    X, Y     int
    TreeType *TreeType // shared flyweight
}

func NewTree(x, y int, treeType *TreeType) *Tree {
    return &Tree{X: x, Y: y, TreeType: treeType}
}

func (t *Tree) Draw() {
    t.TreeType.Draw(t.X, t.Y)
}

// Usage:
// factory := NewTreeFactory()
// oak := factory.GetTreeType("Oak", "green", "bark.png")
// tree1 := NewTree(10, 20, oak)
// tree2 := NewTree(30, 40, oak) // same TreeType
```

---

## Complete Example

```go
package main

import (
    "fmt"
    "sync"
)

// CharacterStyle is the flyweight for text formatting.
type CharacterStyle struct {
    FontFamily string
    FontSize   int
    Bold       bool
    Italic     bool
    Color      string
}

func (s *CharacterStyle) String() string {
    return fmt.Sprintf("%s-%d-%v-%v-%s",
        s.FontFamily, s.FontSize, s.Bold, s.Italic, s.Color)
}

// StyleFactory manages the style cache.
type StyleFactory struct {
    mu     sync.RWMutex
    styles map[string]*CharacterStyle
}

func NewStyleFactory() *StyleFactory {
    return &StyleFactory{
        styles: make(map[string]*CharacterStyle),
    }
}

func (f *StyleFactory) GetStyle(
    family string, size int, bold, italic bool, color string,
) *CharacterStyle {
    key := fmt.Sprintf("%s-%d-%v-%v-%s", family, size, bold, italic, color)

    f.mu.RLock()
    if style, ok := f.styles[key]; ok {
        f.mu.RUnlock()
        return style
    }
    f.mu.RUnlock()

    f.mu.Lock()
    defer f.mu.Unlock()

    if style, ok := f.styles[key]; ok {
        return style
    }

    style := &CharacterStyle{
        FontFamily: family,
        FontSize:   size,
        Bold:       bold,
        Italic:     italic,
        Color:      color,
    }
    f.styles[key] = style
    return style
}

func (f *StyleFactory) Count() int {
    f.mu.RLock()
    defer f.mu.RUnlock()
    return len(f.styles)
}

// Character represents a character with its style (extrinsic state: rune, position).
type Character struct {
    Char     rune
    Position int
    Style    *CharacterStyle // flyweight
}

// Document uses flyweights.
type Document struct {
    characters []*Character
    factory    *StyleFactory
}

func NewDocument(factory *StyleFactory) *Document {
    return &Document{
        characters: make([]*Character, 0),
        factory:    factory,
    }
}

func (d *Document) AddCharacter(
    char rune, family string, size int, bold, italic bool, color string,
) {
    style := d.factory.GetStyle(family, size, bold, italic, color)
    position := len(d.characters)
    d.characters = append(d.characters, &Character{
        Char:     char,
        Position: position,
        Style:    style,
    })
}

func (d *Document) Render() {
    for _, c := range d.characters {
        fmt.Printf("%c", c.Char)
    }
    fmt.Println()
}

func (d *Document) Stats() {
    fmt.Printf("Characters: %d, Unique styles: %d\n", len(d.characters), d.factory.Count())
}

func main() {
    factory := NewStyleFactory()
    doc := NewDocument(factory)

    // Add text with different styles
    text := "Hello, World!"
    for i, char := range text {
        if i < 6 {
            // "Hello," in bold
            doc.AddCharacter(char, "Arial", 12, true, false, "black")
        } else {
            // " World!" in normal
            doc.AddCharacter(char, "Arial", 12, false, false, "black")
        }
    }

    // Add more text
    for _, char := range " This is a test." {
        doc.AddCharacter(char, "Arial", 12, false, false, "black")
    }

    doc.Render()
    doc.Stats()

    // Output:
    // Hello, World! This is a test.
    // Characters: 29, Unique styles: 2
    // (Only 2 shared styles for 29 characters!)
}
```

---

## Variants

| Variant | Description | Use Case |
|----------|-------------|----------|
| Simple Flyweight | Single flyweight type | Basic case |
| Unshared Flyweight | Some objects not shared | Special cases |
| Composite Flyweight | Flyweights in composite structures | Hierarchies |

---

## When to Use

- Huge quantity of similar objects
- Significant memory cost
- Extrinsic state can be calculated/passed
- Object identity not important

## When NOT to Use

- Few objects to create
- Very different objects from each other
- Extrinsic state difficult to externalize
- Unique identity needed per object

---

## Advantages / Disadvantages

| Advantages | Disadvantages |
|-----------|---------------|
| Significant memory savings | Increased complexity |
| Improved performance | CPU cost for extrinsic state calculation |
| Less GC pressure | Less intuitive code |
| | Thread safety required for factory |

---

## Related Patterns

| Pattern | Relationship |
|---------|----------|
| Singleton | Factory is often singleton |
| Composite | Leaves as flyweights |
| State/Strategy | State objects can be flyweights |
| Factory | Used to manage the cache |

---

## Framework Implementations

| Framework/Lib | Implementation |
|---------------|----------------|
| sync.Pool | Reusable object pool |
| string interning | Sharing identical strings |
| image/color | Shared predefined colors |

---

## Anti-patterns to Avoid

| Anti-pattern | Problem | Solution |
|--------------|----------|----------|
| Mutable flyweight | Shared state corruption | Strict immutability |
| Over-optimization | Complexity for little gain | Measure before optimizing |
| Forgetting thread safety | Race conditions | sync.RWMutex or sync.Map |

---

## Tests

```go
func TestTreeFactory_SharedInstance(t *testing.T) {
    factory := NewTreeFactory()

    oak1 := factory.GetTreeType("Oak", "green", "bark.png")
    oak2 := factory.GetTreeType("Oak", "green", "bark.png")

    if oak1 != oak2 {
        t.Error("expected same instance for identical parameters")
    }
}

func TestTreeFactory_DifferentInstances(t *testing.T) {
    factory := NewTreeFactory()

    oak := factory.GetTreeType("Oak", "green", "bark.png")
    pine := factory.GetTreeType("Pine", "green", "pine.png")

    if oak == pine {
        t.Error("expected different instances for different parameters")
    }
}

func TestStyleFactory_Count(t *testing.T) {
    factory := NewStyleFactory()

    factory.GetStyle("Arial", 12, false, false, "black")
    factory.GetStyle("Arial", 12, false, false, "black") // duplicate
    factory.GetStyle("Arial", 14, false, false, "black") // different size

    if factory.Count() != 2 {
        t.Errorf("expected 2 unique styles, got %d", factory.Count())
    }
}

func BenchmarkWithFlyweight(b *testing.B) {
    factory := NewStyleFactory()
    doc := NewDocument(factory)

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        doc.AddCharacter('a', "Arial", 12, false, false, "black")
    }
}
```

---

## Sources

- [Refactoring Guru - Flyweight](https://refactoring.guru/design-patterns/flyweight)
- [Gang of Four - Design Patterns](https://en.wikipedia.org/wiki/Design_Patterns)
- [Go sync.Pool](https://pkg.go.dev/sync#Pool)
