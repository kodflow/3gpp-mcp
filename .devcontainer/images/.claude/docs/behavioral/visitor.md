# Visitor

> Separate an algorithm from objects, allowing new operations to be added.

---

## Principle

The Visitor pattern allows defining new operations on an
object structure without modifying the classes of those objects.
The visitor "visits" each element and performs its operation.

```text
┌────────────┐      ┌────────────┐
│  Element   │◄─────│  Visitor   │
│ Accept(v)  │      │ VisitA()   │
└─────┬──────┘      │ VisitB()   │
      │             └─────┬──────┘
┌─────┴─────┐             │
│           │       ┌─────┴─────┐
▼           ▼       ▼           ▼
ElementA  ElementB  VisitorX  VisitorY
```

---

## Problem Solved

- Add operations to a class hierarchy without modifying them
- Separate algorithms from the data structure
- Group related operations in a single class
- Avoid polluting classes with non-essential operations

---

## Solution

```go
package main

import "fmt"

// Visitor defines operations for each element type.
type Visitor interface {
    VisitCircle(c *Circle)
    VisitRectangle(r *Rectangle)
    VisitTriangle(t *Triangle)
}

// Shape defines the interface accepting a visitor.
type Shape interface {
    Accept(v Visitor)
}

// Circle is a concrete element.
type Circle struct {
    Radius float64
}

func (c *Circle) Accept(v Visitor) {
    v.VisitCircle(c)
}

// Rectangle is a concrete element.
type Rectangle struct {
    Width, Height float64
}

func (r *Rectangle) Accept(v Visitor) {
    v.VisitRectangle(r)
}

// Triangle is a concrete element.
type Triangle struct {
    Base, Height float64
}

func (t *Triangle) Accept(v Visitor) {
    v.VisitTriangle(t)
}

// AreaCalculator is a concrete visitor.
type AreaCalculator struct {
    TotalArea float64
}

func (a *AreaCalculator) VisitCircle(c *Circle) {
    area:= 3.14159 * c.Radius * c.Radius
    a.TotalArea += area
    fmt.Printf("Circle area: %.2f\n", area)
}

func (a *AreaCalculator) VisitRectangle(r *Rectangle) {
    area:= r.Width * r.Height
    a.TotalArea += area
    fmt.Printf("Rectangle area: %.2f\n", area)
}

func (a *AreaCalculator) VisitTriangle(t *Triangle) {
    area:= 0.5 * t.Base * t.Height
    a.TotalArea += area
    fmt.Printf("Triangle area: %.2f\n", area)
}

// Usage:
// shapes:= []Shape{&Circle{5}, &Rectangle{4, 3}, &Triangle{6, 4}}
// calc:= &AreaCalculator{}
// for _, s:= range shapes { s.Accept(calc) }
```

---

## Complete Example

```go
package main

import (
    "fmt"
    "strings"
)

// Node represents an AST node.
type Node interface {
    Accept(v NodeVisitor)
}

// NodeVisitor defines operations on the AST.
type NodeVisitor interface {
    VisitNumber(n *NumberNode)
    VisitBinaryOp(b *BinaryOpNode)
    VisitVariable(v *VariableNode)
    VisitFunction(f *FunctionNode)
}

// NumberNode represents a number.
type NumberNode struct {
    Value float64
}

func (n *NumberNode) Accept(v NodeVisitor) {
    v.VisitNumber(n)
}

// BinaryOpNode represents a binary operation.
type BinaryOpNode struct {
    Left, Right Node
    Operator    string
}

func (b *BinaryOpNode) Accept(v NodeVisitor) {
    v.VisitBinaryOp(b)
}

// VariableNode represents a variable.
type VariableNode struct {
    Name string
}

func (vn *VariableNode) Accept(v NodeVisitor) {
    v.VisitVariable(vn)
}

// FunctionNode represents a function call.
type FunctionNode struct {
    Name string
    Args []Node
}

func (f *FunctionNode) Accept(v NodeVisitor) {
    v.VisitFunction(f)
}

// PrintVisitor displays the AST.
type PrintVisitor struct {
    indent int
    output strings.Builder
}

func (p *PrintVisitor) VisitNumber(n *NumberNode) {
    p.write(fmt.Sprintf("Number(%.2f)", n.Value))
}

func (p *PrintVisitor) VisitBinaryOp(b *BinaryOpNode) {
    p.write(fmt.Sprintf("BinaryOp(%s)", b.Operator))
    p.indent++
    p.write("Left:")
    p.indent++
    b.Left.Accept(p)
    p.indent--
    p.write("Right:")
    p.indent++
    b.Right.Accept(p)
    p.indent -= 2
}

func (p *PrintVisitor) VisitVariable(v *VariableNode) {
    p.write(fmt.Sprintf("Variable(%s)", v.Name))
}

func (p *PrintVisitor) VisitFunction(f *FunctionNode) {
    p.write(fmt.Sprintf("Function(%s)", f.Name))
    p.indent++
    for i, arg:= range f.Args {
        p.write(fmt.Sprintf("Arg[%d]:", i))
        p.indent++
        arg.Accept(p)
        p.indent--
    }
    p.indent--
}

func (p *PrintVisitor) write(s string) {
    p.output.WriteString(strings.Repeat("  ", p.indent))
    p.output.WriteString(s)
    p.output.WriteString("\n")
}

func (p *PrintVisitor) String() string {
    return p.output.String()
}

// EvalVisitor evaluates the expression.
type EvalVisitor struct {
    Variables map[string]float64
    stack     []float64
}

func NewEvalVisitor(vars map[string]float64) *EvalVisitor {
    return &EvalVisitor{
        Variables: vars,
        stack:     make([]float64, 0),
    }
}

func (e *EvalVisitor) push(v float64) {
    e.stack = append(e.stack, v)
}

func (e *EvalVisitor) pop() float64 {
    n:= len(e.stack) - 1
    v:= e.stack[n]
    e.stack = e.stack[:n]
    return v
}

func (e *EvalVisitor) Result() float64 {
    if len(e.stack) > 0 {
        return e.stack[len(e.stack)-1]
    }
    return 0
}

func (e *EvalVisitor) VisitNumber(n *NumberNode) {
    e.push(n.Value)
}

func (e *EvalVisitor) VisitBinaryOp(b *BinaryOpNode) {
    b.Left.Accept(e)
    b.Right.Accept(e)
    right:= e.pop()
    left:= e.pop()

    var result float64
    switch b.Operator {
    case "+":
        result = left + right
    case "-":
        result = left - right
    case "*":
        result = left * right
    case "/":
        result = left / right
    }
    e.push(result)
}

func (e *EvalVisitor) VisitVariable(v *VariableNode) {
    if val, ok:= e.Variables[v.Name]; ok {
        e.push(val)
    } else {
        e.push(0)
    }
}

func (e *EvalVisitor) VisitFunction(f *FunctionNode) {
    // Evaluate arguments
    args:= make([]float64, len(f.Args))
    for i, arg:= range f.Args {
        arg.Accept(e)
        args[i] = e.pop()
    }

    // Built-in functions
    var result float64
    switch f.Name {
    case "max":
        result = args[0]
        for _, a:= range args[1:] {
            if a > result {
                result = a
            }
        }
    case "min":
        result = args[0]
        for _, a:= range args[1:] {
            if a < result {
                result = a
            }
        }
    case "sum":
        for _, a:= range args {
            result += a
        }
    }
    e.push(result)
}

func main() {
    // Build the AST: max(x, y * 2) + 10
    ast:= &BinaryOpNode{
        Operator: "+",
        Left: &FunctionNode{
            Name: "max",
            Args: []Node{
                &VariableNode{Name: "x"},
                &BinaryOpNode{
                    Operator: "*",
                    Left:     &VariableNode{Name: "y"},
                    Right:    &NumberNode{Value: 2},
                },
            },
        },
        Right: &NumberNode{Value: 10},
    }

    // Visitor 1: Display
    printer:= &PrintVisitor{}
    ast.Accept(printer)
    fmt.Println("AST Structure:")
    fmt.Println(printer)

    // Visitor 2: Evaluate
    vars:= map[string]float64{"x": 5, "y": 3}
    eval:= NewEvalVisitor(vars)
    ast.Accept(eval)
    fmt.Printf("Result (x=5, y=3): %.2f\n", eval.Result())
    // max(5, 3*2) + 10 = max(5, 6) + 10 = 6 + 10 = 16
}
```

---

## Variants

| Variant | Description | Use Case |
|----------|-------------|-------------|
| Classic Visitor | Double dispatch | Stable structures |
| Acyclic Visitor | Avoids cyclic dependencies | Complex hierarchies |
| Hierarchical Visitor | Visit with parent context | Trees |

---

## When to Use

- Multiple operations on an object structure
- Add operations without modifying classes
- Group related operations
- Stable structure, variable operations

## When NOT to Use

- Class hierarchy that changes often
- Few different operations
- Double dispatch not necessary

---

## Advantages / Disadvantages

| Advantages | Disadvantages |
|-----------|---------------|
| Open/Closed for operations | Difficult to add new elements |
| Single Responsibility | Can violate encapsulation |
| Easy state accumulation | Complex double dispatch |
| Grouped operations | |

---

## Related Patterns

| Pattern | Relation |
|---------|----------|
| Composite | Visitor can traverse composites |
| Iterator | Alternative for traversal |
| Interpreter | Visitor to evaluate the AST |
| Command | Visitor as command on elements |

---

## Framework Implementations

| Framework/Lib | Implementation |
|---------------|----------------|
| go/ast | ast.Visitor, ast.Walk |
| go/types | types.Object visitors |
| html/template | Tree traversal |

---

## Anti-patterns to Avoid

| Anti-pattern | Problem | Solution |
|--------------|----------|----------|
| Monolithic visitor | Too many responsibilities | Specialized visitors |
| Forgetting Accept | Unvisited elements | Check all types |
| Shared mutable state | Race conditions | Thread-local visitors |

---

## Tests

```go
func TestAreaCalculator(t *testing.T) {
    shapes:= []Shape{
        &Circle{Radius: 2},
        &Rectangle{Width: 3, Height: 4},
    }

    calc:= &AreaCalculator{}
    for _, s:= range shapes {
        s.Accept(calc)
    }

    // Circle: 3.14159 * 4 = 12.57
    // Rectangle: 3 * 4 = 12
    // Total ~= 24.57
    expected:= 24.57
    if calc.TotalArea < 24 || calc.TotalArea > 25 {
        t.Errorf("expected ~%.2f, got %.2f", expected, calc.TotalArea)
    }
}

func TestEvalVisitor(t *testing.T) {
    // Expression: x + y
    ast:= &BinaryOpNode{
        Operator: "+",
        Left:     &VariableNode{Name: "x"},
        Right:    &VariableNode{Name: "y"},
    }

    eval:= NewEvalVisitor(map[string]float64{"x": 10, "y": 5})
    ast.Accept(eval)

    if eval.Result() != 15 {
        t.Errorf("expected 15, got %.2f", eval.Result())
    }
}

func TestPrintVisitor(t *testing.T) {
    ast:= &NumberNode{Value: 42}
    printer:= &PrintVisitor{}
    ast.Accept(printer)

    if !strings.Contains(printer.String(), "42") {
        t.Error("expected number in output")
    }
}
```

---

## Sources

- [Refactoring Guru - Visitor](https://refactoring.guru/design-patterns/visitor)
- [Gang of Four - Design Patterns](https://en.wikipedia.org/wiki/Design_Patterns)
- [Go AST Visitor](https://pkg.go.dev/go/ast#Visitor)
