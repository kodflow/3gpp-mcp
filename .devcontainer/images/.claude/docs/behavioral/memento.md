# Memento

> Capture and externalize an object's internal state to be able to restore it later.

---

## Principle

The Memento pattern allows saving and restoring an object's state
without violating its encapsulation. Used to implement undo/redo.

```text
┌────────────────┐    ┌────────────────┐    ┌────────────────┐
│  Originator    │───▶│    Memento     │◀───│   Caretaker    │
│ (createMemento)│    │ (saved state)  │    │ (stores memos) │
│ (restore)      │    └────────────────┘    └────────────────┘
└────────────────┘
```

---

## Problem Solved

- Save an object's state at a given point in time
- Implement undo/redo without exposing internal details
- Create snapshots/checkpoints
- Respect encapsulation

---

## Solution

```go
package main

import "fmt"

// Memento stores the Originator's state.
type Memento struct {
    state string
}

func (m *Memento) GetState() string {
    return m.state
}

// Editor is the Originator.
type Editor struct {
    content string
}

func NewEditor() *Editor {
    return &Editor{}
}

func (e *Editor) Type(text string) {
    e.content += text
}

func (e *Editor) GetContent() string {
    return e.content
}

func (e *Editor) Save() *Memento {
    return &Memento{state: e.content}
}

func (e *Editor) Restore(m *Memento) {
    e.content = m.GetState()
}

// History is the Caretaker.
type History struct {
    mementos []*Memento
}

func NewHistory() *History {
    return &History{mementos: make([]*Memento, 0)}
}

func (h *History) Push(m *Memento) {
    h.mementos = append(h.mementos, m)
}

func (h *History) Pop() *Memento {
    if len(h.mementos) == 0 {
        return nil
    }
    last:= h.mementos[len(h.mementos)-1]
    h.mementos = h.mementos[:len(h.mementos)-1]
    return last
}

// Usage:
// editor:= NewEditor()
// history:= NewHistory()
// editor.Type("Hello")
// history.Push(editor.Save())
// editor.Type(" World")
// editor.Restore(history.Pop()) // Back to "Hello"
```

---

## Complete Example

```go
package main

import (
    "encoding/json"
    "fmt"
    "time"
)

// GameState represents a game's state.
type GameState struct {
    Level      int               `json:"level"`
    Health     int               `json:"health"`
    Position   Position          `json:"position"`
    Inventory  []string          `json:"inventory"`
    SavedAt    time.Time         `json:"saved_at"`
    Checksum   string            `json:"checksum"`
}

type Position struct {
    X, Y int
}

// GameMemento is the memento for the game.
type GameMemento struct {
    state []byte
}

func (m *GameMemento) GetState() []byte {
    return m.state
}

// Game is the Originator.
type Game struct {
    level     int
    health    int
    position  Position
    inventory []string
}

func NewGame() *Game {
    return &Game{
        level:     1,
        health:    100,
        position:  Position{X: 0, Y: 0},
        inventory: make([]string, 0),
    }
}

func (g *Game) Play(action string) {
    switch action {
    case "move":
        g.position.X += 10
        g.position.Y += 5
    case "damage":
        g.health -= 20
    case "heal":
        g.health = min(100, g.health+30)
    case "level_up":
        g.level++
    case "pickup":
        g.inventory = append(g.inventory, "Sword")
    }
}

func (g *Game) Status() string {
    return fmt.Sprintf("Level: %d, Health: %d, Pos: (%d,%d), Items: %v",
        g.level, g.health, g.position.X, g.position.Y, g.inventory)
}

func (g *Game) Save() *GameMemento {
    state:= GameState{
        Level:     g.level,
        Health:    g.health,
        Position:  g.position,
        Inventory: append([]string{}, g.inventory...),
        SavedAt:   time.Now(),
    }
    data, _:= json.Marshal(state)
    return &GameMemento{state: data}
}

func (g *Game) Restore(m *GameMemento) error {
    var state GameState
    if err:= json.Unmarshal(m.GetState(), &state); err != nil {
        return err
    }
    g.level = state.Level
    g.health = state.Health
    g.position = state.Position
    g.inventory = append([]string{}, state.Inventory...)
    return nil
}

// SaveSlot is the Caretaker that manages multiple saves.
type SaveSlot struct {
    name     string
    mementos map[string]*GameMemento
    undoStack []*GameMemento
    redoStack []*GameMemento
}

func NewSaveSlot(name string) *SaveSlot {
    return &SaveSlot{
        name:      name,
        mementos:  make(map[string]*GameMemento),
        undoStack: make([]*GameMemento, 0),
        redoStack: make([]*GameMemento, 0),
    }
}

func (s *SaveSlot) QuickSave(key string, m *GameMemento) {
    s.mementos[key] = m
    fmt.Printf("Quick saved to slot '%s'\n", key)
}

func (s *SaveSlot) QuickLoad(key string) *GameMemento {
    return s.mementos[key]
}

func (s *SaveSlot) SaveForUndo(m *GameMemento) {
    s.undoStack = append(s.undoStack, m)
    s.redoStack = nil // Clear redo on new action
}

func (s *SaveSlot) Undo() *GameMemento {
    if len(s.undoStack) == 0 {
        return nil
    }
    m:= s.undoStack[len(s.undoStack)-1]
    s.undoStack = s.undoStack[:len(s.undoStack)-1]
    return m
}

func main() {
    game:= NewGame()
    slots:= NewSaveSlot("Player1")

    // Play and save
    fmt.Println("Starting game:", game.Status())

    game.Play("move")
    game.Play("pickup")
    slots.SaveForUndo(game.Save())
    fmt.Println("After actions:", game.Status())

    game.Play("damage")
    game.Play("damage")
    slots.SaveForUndo(game.Save())
    fmt.Println("After damage:", game.Status())

    // Quick save
    slots.QuickSave("checkpoint1", game.Save())

    game.Play("damage")
    game.Play("damage")
    fmt.Println("Near death:", game.Status())

    // Undo
    if m:= slots.Undo(); m != nil {
        game.Restore(m)
        fmt.Println("After undo:", game.Status())
    }

    // Quick load
    if m:= slots.QuickLoad("checkpoint1"); m != nil {
        game.Restore(m)
        fmt.Println("After quick load:", game.Status())
    }

    // Output:
    // Starting game: Level: 1, Health: 100, Pos: (0,0), Items: []
    // After actions: Level: 1, Health: 100, Pos: (10,5), Items: [Sword]
    // After damage: Level: 1, Health: 60, Pos: (10,5), Items: [Sword]
    // Quick saved to slot 'checkpoint1'
    // Near death: Level: 1, Health: 20, Pos: (10,5), Items: [Sword]
    // After undo: Level: 1, Health: 60, Pos: (10,5), Items: [Sword]
    // After quick load: Level: 1, Health: 60, Pos: (10,5), Items: [Sword]
}
```

---

## Variants

| Variant | Description | Use Case |
|----------|-------------|-------------|
| Full Memento | Complete state copy | Small objects |
| Incremental | Delta saves | Large objects |
| Serialized | JSON/Gob encoding | Persistence |

---

## When to Use

- Undo/Redo functionality required
- Snapshots/checkpoints needed
- Transactions with rollback
- State history

## When NOT to Use

- Very large state (memory)
- No need for restoration
- Simple state (direct copy is sufficient)

---

## Advantages / Disadvantages

| Advantages | Disadvantages |
|-----------|---------------|
| Preserves encapsulation | Memory cost (many mementos) |
| Simplifies the Originator | Serialization can be costly |
| Complete history | Caretaker must manage the lifecycle |

---

## Related Patterns

| Pattern | Relation |
|---------|----------|
| Command | Combined for perfect undo |
| Prototype | Clone vs Memento |
| Iterator | Traverse history |
| State | Memento saves State |

---

## Framework Implementations

| Framework/Lib | Implementation |
|---------------|----------------|
| encoding/json | State serialization |
| encoding/gob | Binary serialization |
| database/sql | Transactions |

---

## Anti-patterns to Avoid

| Anti-pattern | Problem | Solution |
|--------------|----------|----------|
| Too many mementos | Memory leak | Limit history |
| Mutable memento | Corruption | Deep copy |
| Exposing state | Violates encapsulation | Opaque interface |

---

## Tests

```go
func TestEditor_SaveRestore(t *testing.T) {
    editor:= NewEditor()
    history:= NewHistory()

    editor.Type("Hello")
    history.Push(editor.Save())

    editor.Type(" World")
    if editor.GetContent() != "Hello World" {
        t.Error("expected 'Hello World'")
    }

    editor.Restore(history.Pop())
    if editor.GetContent() != "Hello" {
        t.Error("expected 'Hello' after restore")
    }
}

func TestGame_SaveRestore(t *testing.T) {
    game:= NewGame()

    game.Play("damage")
    original:= game.health
    memento:= game.Save()

    game.Play("damage")
    if game.health >= original {
        t.Error("health should decrease")
    }

    game.Restore(memento)
    if game.health != original {
        t.Errorf("expected health %d, got %d", original, game.health)
    }
}

func TestSaveSlot_Undo(t *testing.T) {
    game:= NewGame()
    slots:= NewSaveSlot("test")

    slots.SaveForUndo(game.Save())
    game.Play("level_up")

    if game.level != 2 {
        t.Error("expected level 2")
    }

    m:= slots.Undo()
    if m == nil {
        t.Fatal("expected memento")
    }

    game.Restore(m)
    if game.level != 1 {
        t.Error("expected level 1 after undo")
    }
}
```

---

## Sources

- [Refactoring Guru - Memento](https://refactoring.guru/design-patterns/memento)
- [Gang of Four - Design Patterns](https://en.wikipedia.org/wiki/Design_Patterns)
- [Command + Memento for Undo](https://sourcemaking.com/design_patterns/memento)
