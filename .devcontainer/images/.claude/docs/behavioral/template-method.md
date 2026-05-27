# Template Method

> Define the skeleton of an algorithm, delegating steps to subclasses.

---

## Principle

The Template Method pattern defines the structure of an algorithm in a
base class. Subclasses redefine certain steps without
changing the overall structure.

```text
┌─────────────────────┐
│  AbstractClass      │
│  ──────────────     │
│  templateMethod()   │ ─► calls step1(), step2(), step3()
│  step1()            │
│  step2() (abstract) │
│  step3()            │
└──────────┬──────────┘
           │
     ┌─────┴─────┐
     ▼           ▼
┌─────────┐ ┌─────────┐
│ClassA   │ │ClassB   │
│step2()  │ │step2()  │
└─────────┘ └─────────┘
```

---

## Problem Solved

- Duplicated code in multiple classes with minor variations
- Algorithm with fixed steps and variable steps
- Inversion of control (Hollywood Principle)
- Avoid duplication while allowing customization

---

## Solution

```go
package main

import "fmt"

// DataMiner defines the template.
type DataMiner interface {
    Mine(path string)
    // Methods to implement
    OpenFile(path string)
    ExtractData()
    ParseData()
    AnalyzeData()
    SendReport()
    CloseFile()
}

// BaseDataMiner provides the template implementation.
type BaseDataMiner struct {
    DataMiner
}

// Mine is the template method.
func (b *BaseDataMiner) Mine(path string) {
    b.OpenFile(path)
    b.ExtractData()
    b.ParseData()
    b.AnalyzeData()
    b.SendReport()
    b.CloseFile()
}

// Default implementations (hooks)
func (b *BaseDataMiner) AnalyzeData() {
    fmt.Println("Default analysis...")
}

func (b *BaseDataMiner) SendReport() {
    fmt.Println("Sending report via email...")
}

// PDFDataMiner implements the specific steps.
type PDFDataMiner struct {
    BaseDataMiner
}

func NewPDFDataMiner() *PDFDataMiner {
    m:= &PDFDataMiner{}
    m.DataMiner = m
    return m
}

func (p *PDFDataMiner) OpenFile(path string) {
    fmt.Printf("Opening PDF: %s\n", path)
}

func (p *PDFDataMiner) ExtractData() {
    fmt.Println("Extracting text from PDF...")
}

func (p *PDFDataMiner) ParseData() {
    fmt.Println("Parsing PDF structure...")
}

func (p *PDFDataMiner) CloseFile() {
    fmt.Println("Closing PDF")
}
```

---

## Complete Example

```go
package main

import (
    "fmt"
    "strings"
)

// GameAI defines the template for the game AI.
type GameAI interface {
    Turn()
    // Abstract steps
    CollectResources()
    BuildStructures()
    BuildUnits()
    Attack()
    // Hook
    CanAttack() bool
}

// BaseGameAI provides the template.
type BaseGameAI struct {
    name string
    GameAI
}

func (b *BaseGameAI) Turn() {
    fmt.Printf("\n=== %s's Turn ===\n", b.name)
    b.CollectResources()
    b.BuildStructures()
    b.BuildUnits()
    if b.CanAttack() {
        b.Attack()
    } else {
        fmt.Println("Not ready to attack yet")
    }
}

// Default hook
func (b *BaseGameAI) CanAttack() bool {
    return true
}

// OrcsAI implements an aggressive strategy.
type OrcsAI struct {
    BaseGameAI
    warriors int
}

func NewOrcsAI() *OrcsAI {
    ai:= &OrcsAI{warriors: 0}
    ai.name = "Orcs"
    ai.GameAI = ai
    return ai
}

func (o *OrcsAI) CollectResources() {
    fmt.Println("Orcs: Pillaging nearby villages for gold")
}

func (o *OrcsAI) BuildStructures() {
    fmt.Println("Orcs: Building war camps")
}

func (o *OrcsAI) BuildUnits() {
    o.warriors += 5
    fmt.Printf("Orcs: Training warriors (total: %d)\n", o.warriors)
}

func (o *OrcsAI) Attack() {
    fmt.Println("Orcs: WAAAGH! Charging with all warriors!")
}

func (o *OrcsAI) CanAttack() bool {
    return o.warriors >= 10
}

// HumansAI implements a defensive strategy.
type HumansAI struct {
    BaseGameAI
    knights int
    walls   int
}

func NewHumansAI() *HumansAI {
    ai:= &HumansAI{knights: 0, walls: 0}
    ai.name = "Humans"
    ai.GameAI = ai
    return ai
}

func (h *HumansAI) CollectResources() {
    fmt.Println("Humans: Farming and mining")
}

func (h *HumansAI) BuildStructures() {
    h.walls++
    fmt.Printf("Humans: Building walls (level: %d)\n", h.walls)
}

func (h *HumansAI) BuildUnits() {
    h.knights += 2
    fmt.Printf("Humans: Training knights (total: %d)\n", h.knights)
}

func (h *HumansAI) Attack() {
    fmt.Println("Humans: Launching organized cavalry charge!")
}

func (h *HumansAI) CanAttack() bool {
    return h.walls >= 2 && h.knights >= 4
}

// DocumentProcessor with hooks.
type DocumentProcessor interface {
    Process(content string) string
    // Template steps
    PreProcess(content string) string
    MainProcess(content string) string
    PostProcess(content string) string
    // Hooks
    ShouldLog() bool
}

type BaseDocumentProcessor struct {
    DocumentProcessor
}

func (b *BaseDocumentProcessor) Process(content string) string {
    if b.ShouldLog() {
        fmt.Println("Processing document...")
    }
    result:= b.PreProcess(content)
    result = b.MainProcess(result)
    result = b.PostProcess(result)
    if b.ShouldLog() {
        fmt.Println("Done!")
    }
    return result
}

// Default hook
func (b *BaseDocumentProcessor) ShouldLog() bool {
    return false
}

func (b *BaseDocumentProcessor) PreProcess(content string) string {
    return strings.TrimSpace(content)
}

func (b *BaseDocumentProcessor) PostProcess(content string) string {
    return content
}

// MarkdownProcessor implements Markdown processing.
type MarkdownProcessor struct {
    BaseDocumentProcessor
    verbose bool
}

func NewMarkdownProcessor(verbose bool) *MarkdownProcessor {
    p:= &MarkdownProcessor{verbose: verbose}
    p.DocumentProcessor = p
    return p
}

func (m *MarkdownProcessor) MainProcess(content string) string {
    // Simulate Markdown -> HTML conversion
    result:= strings.ReplaceAll(content, "# ", "<h1>")
    result = strings.ReplaceAll(result, "\n", "</h1>\n")
    return result
}

func (m *MarkdownProcessor) ShouldLog() bool {
    return m.verbose
}

func main() {
    // Example 1: Game AI
    orcs:= NewOrcsAI()
    humans:= NewHumansAI()

    // Simulate multiple turns
    for i:= 0; i < 3; i++ {
        orcs.Turn()
        humans.Turn()
    }

    // Example 2: Document Processor
    fmt.Println("\n=== Document Processing ===")
    processor:= NewMarkdownProcessor(true)
    result:= processor.Process("# Hello World\n# Second Title")
    fmt.Println("Result:", result)

    // Output shows template method controlling the flow
    // while subclasses customize specific steps
}
```

---

## Variants

| Variant | Description | Use Case |
|----------|-------------|-------------|
| Abstract Steps | Required steps | Required behavior |
| Default Steps | Default implementations | Optional behavior |
| Hooks | Extension points | Fine-grained customization |

---

## When to Use

- Algorithm with fixed structure and variable steps
- Avoid code duplication
- Controlled extension points for subclasses
- Inversion of control ("Don't call us, we'll call you")

## When NOT to Use

- Entirely different algorithm per class
- Only one implementation planned
- Too many variations make the template complex

---

## Advantages / Disadvantages

| Advantages | Disadvantages |
|-----------|---------------|
| Eliminates duplication | Inheritance required (less flexible) |
| Clear extension points | Can violate Liskov if poorly designed |
| Inversion of control | Maintenance if template changes |
| | Limited number of steps per template |

---

## Related Patterns

| Pattern | Relation |
|---------|----------|
| Strategy | Strategy uses composition, Template uses inheritance |
| Factory Method | Often a step in the Template |
| Hook | Extension of the Template Method |

---

## Framework Implementations

| Framework/Lib | Implementation |
|---------------|----------------|
| http.Handler | ServeHTTP as template |
| sort.Interface | Len, Less, Swap as steps |
| testing.T | Run as template |

---

## Anti-patterns to Avoid

| Anti-pattern | Problem | Solution |
|--------------|----------|----------|
| Too many steps | Complexity | Limit to 5-7 steps |
| Forced override | Rigidity | Use hooks |
| Deep hierarchy | Fragility | Prefer composition |

---

## Tests

```go
func TestOrcsAI_AttackWhenReady(t *testing.T) {
    orcs:= NewOrcsAI()

    // Not enough warriors
    if orcs.CanAttack() {
        t.Error("orcs should not attack with 0 warriors")
    }

    // Accumulate warriors
    for i:= 0; i < 2; i++ {
        orcs.Turn()
    }

    // Now ready
    if !orcs.CanAttack() {
        t.Error("orcs should be ready to attack")
    }
}

func TestHumansAI_DefensiveStrategy(t *testing.T) {
    humans:= NewHumansAI()

    // First phase: construction
    humans.Turn()

    if humans.walls != 1 {
        t.Errorf("expected 1 wall, got %d", humans.walls)
    }
    if humans.knights != 2 {
        t.Errorf("expected 2 knights, got %d", humans.knights)
    }
}

func TestMarkdownProcessor(t *testing.T) {
    processor:= NewMarkdownProcessor(false)
    result:= processor.Process("# Test")

    if !strings.Contains(result, "<h1>") {
        t.Error("expected HTML h1 tag")
    }
}
```

---

## Sources

- [Refactoring Guru - Template Method](https://refactoring.guru/design-patterns/template-method)
- [Gang of Four - Design Patterns](https://en.wikipedia.org/wiki/Design_Patterns)
- [Hollywood Principle](https://en.wikipedia.org/wiki/Hollywood_principle)
