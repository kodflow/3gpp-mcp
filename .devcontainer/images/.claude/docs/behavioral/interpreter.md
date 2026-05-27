# Interpreter

> Define a grammar and an interpreter to evaluate expressions.

---

## Principle

The Interpreter pattern defines a grammar for a simple language and uses
this grammar to interpret expressions.
Each grammar rule becomes a class.

```text
┌─────────────────┐
│   Expression    │
│  Interpret(ctx) │
└────────┬────────┘
         │
    ┌────┴────┐
    │         │
┌───▼───┐ ┌───▼────────┐
│Terminal│ │NonTerminal │
│  Expr  │ │   Expr     │
└────────┘ │ (children) │
           └────────────┘
```

---

## Problem Solved

- Evaluate expressions in a specific language
- Parse and execute queries, rules, or DSL
- Define an interpretable grammar
- Recursively combinable expressions

---

## Solution

```go
package main

import (
    "fmt"
    "strconv"
    "strings"
)

// Context contains global variables.
type Context struct {
    Variables map[string]int
}

func NewContext() *Context {
    return &Context{Variables: make(map[string]int)}
}

// Expression defines the interpretation interface.
type Expression interface {
    Interpret(ctx *Context) int
}

// NumberExpression is a terminal expression.
type NumberExpression struct {
    value int
}

func NewNumberExpression(v int) *NumberExpression {
    return &NumberExpression{value: v}
}

func (n *NumberExpression) Interpret(ctx *Context) int {
    return n.value
}

// VariableExpression is a terminal expression.
type VariableExpression struct {
    name string
}

func NewVariableExpression(name string) *VariableExpression {
    return &VariableExpression{name: name}
}

func (v *VariableExpression) Interpret(ctx *Context) int {
    return ctx.Variables[v.name]
}

// AddExpression is a non-terminal expression.
type AddExpression struct {
    left, right Expression
}

func NewAddExpression(left, right Expression) *AddExpression {
    return &AddExpression{left: left, right: right}
}

func (a *AddExpression) Interpret(ctx *Context) int {
    return a.left.Interpret(ctx) + a.right.Interpret(ctx)
}

// SubtractExpression is a non-terminal expression.
type SubtractExpression struct {
    left, right Expression
}

func NewSubtractExpression(left, right Expression) *SubtractExpression {
    return &SubtractExpression{left: left, right: right}
}

func (s *SubtractExpression) Interpret(ctx *Context) int {
    return s.left.Interpret(ctx) - s.right.Interpret(ctx)
}

// Usage:
// ctx:= NewContext()
// ctx.Variables["x"] = 10
// expr:= NewAddExpression(NewVariableExpression("x"), NewNumberExpression(5))
// result:= expr.Interpret(ctx) // 15
```

---

## Complete Example

```go
package main

import (
    "fmt"
    "regexp"
    "strconv"
    "strings"
)

// BoolContext contains known facts.
type BoolContext struct {
    Facts map[string]bool
}

func NewBoolContext() *BoolContext {
    return &BoolContext{Facts: make(map[string]bool)}
}

// BoolExpression defines a boolean expression.
type BoolExpression interface {
    Interpret(ctx *BoolContext) bool
    String() string
}

// TrueExpression is always true.
type TrueExpression struct{}

func (t *TrueExpression) Interpret(ctx *BoolContext) bool { return true }
func (t *TrueExpression) String() string                  { return "TRUE" }

// FalseExpression is always false.
type FalseExpression struct{}

func (f *FalseExpression) Interpret(ctx *BoolContext) bool { return false }
func (f *FalseExpression) String() string                  { return "FALSE" }

// FactExpression checks a fact.
type FactExpression struct {
    name string
}

func NewFactExpression(name string) *FactExpression {
    return &FactExpression{name: name}
}

func (f *FactExpression) Interpret(ctx *BoolContext) bool {
    return ctx.Facts[f.name]
}

func (f *FactExpression) String() string {
    return f.name
}

// AndExpression is a logical AND.
type AndExpression struct {
    left, right BoolExpression
}

func NewAndExpression(left, right BoolExpression) *AndExpression {
    return &AndExpression{left: left, right: right}
}

func (a *AndExpression) Interpret(ctx *BoolContext) bool {
    return a.left.Interpret(ctx) && a.right.Interpret(ctx)
}

func (a *AndExpression) String() string {
    return fmt.Sprintf("(%s AND %s)", a.left.String(), a.right.String())
}

// OrExpression is a logical OR.
type OrExpression struct {
    left, right BoolExpression
}

func NewOrExpression(left, right BoolExpression) *OrExpression {
    return &OrExpression{left: left, right: right}
}

func (o *OrExpression) Interpret(ctx *BoolContext) bool {
    return o.left.Interpret(ctx) || o.right.Interpret(ctx)
}

func (o *OrExpression) String() string {
    return fmt.Sprintf("(%s OR %s)", o.left.String(), o.right.String())
}

// NotExpression is a logical NOT.
type NotExpression struct {
    expr BoolExpression
}

func NewNotExpression(expr BoolExpression) *NotExpression {
    return &NotExpression{expr: expr}
}

func (n *NotExpression) Interpret(ctx *BoolContext) bool {
    return !n.expr.Interpret(ctx)
}

func (n *NotExpression) String() string {
    return fmt.Sprintf("NOT %s", n.expr.String())
}

// RuleEngine applies rules.
type RuleEngine struct {
    rules map[string]BoolExpression
}

func NewRuleEngine() *RuleEngine {
    return &RuleEngine{rules: make(map[string]BoolExpression)}
}

func (r *RuleEngine) AddRule(name string, expr BoolExpression) {
    r.rules[name] = expr
}

func (r *RuleEngine) Evaluate(name string, ctx *BoolContext) bool {
    if rule, ok:= r.rules[name]; ok {
        return rule.Interpret(ctx)
    }
    return false
}

func (r *RuleEngine) EvaluateAll(ctx *BoolContext) map[string]bool {
    results:= make(map[string]bool)
    for name, rule:= range r.rules {
        results[name] = rule.Interpret(ctx)
    }
    return results
}

// Simple parser for expressions.
func ParseSimple(expr string) BoolExpression {
    expr = strings.TrimSpace(expr)

    // NOT
    if strings.HasPrefix(expr, "NOT ") {
        return NewNotExpression(ParseSimple(expr[4:]))
    }

    // AND
    if idx:= strings.Index(expr, " AND "); idx > 0 {
        return NewAndExpression(
            ParseSimple(expr[:idx]),
            ParseSimple(expr[idx+5:]),
        )
    }

    // OR
    if idx:= strings.Index(expr, " OR "); idx > 0 {
        return NewOrExpression(
            ParseSimple(expr[:idx]),
            ParseSimple(expr[idx+4:]),
        )
    }

    // TRUE/FALSE
    if expr == "TRUE" {
        return &TrueExpression{}
    }
    if expr == "FALSE" {
        return &FalseExpression{}
    }

    // Fact
    return NewFactExpression(expr)
}

func main() {
    // Context with facts
    ctx:= NewBoolContext()
    ctx.Facts["is_admin"] = true
    ctx.Facts["is_logged_in"] = true
    ctx.Facts["has_permission"] = false
    ctx.Facts["is_owner"] = true

    // Build rules
    engine:= NewRuleEngine()

    // Rule: can edit if admin OR (logged in AND owner)
    canEdit:= NewOrExpression(
        NewFactExpression("is_admin"),
        NewAndExpression(
            NewFactExpression("is_logged_in"),
            NewFactExpression("is_owner"),
        ),
    )
    engine.AddRule("can_edit", canEdit)

    // Rule: can delete if admin AND has permission
    canDelete:= NewAndExpression(
        NewFactExpression("is_admin"),
        NewFactExpression("has_permission"),
    )
    engine.AddRule("can_delete", canDelete)

    // Rule: public read
    canRead:= &TrueExpression{}
    engine.AddRule("can_read", canRead)

    // Evaluate rules
    fmt.Println("Rule Evaluation:")
    results:= engine.EvaluateAll(ctx)
    for name, result:= range results {
        fmt.Printf("  %s: %v\n", name, result)
    }

    // Parse a simple expression
    fmt.Println("\nParsed Expression:")
    parsed:= ParseSimple("is_admin AND is_logged_in")
    fmt.Printf("  %s = %v\n", parsed.String(), parsed.Interpret(ctx))

    // Output:
    // Rule Evaluation:
    //   can_edit: true
    //   can_delete: false
    //   can_read: true
    // Parsed Expression:
    //   (is_admin AND is_logged_in) = true
}
```

---

## Variants

| Variant | Description | Use Case |
|----------|-------------|-------------|
| Tree Interpreter | Expression tree | Simple languages |
| Stack-Based | Stack machine | Bytecode |
| Visitor-Based | Visitor on AST | Complex languages |

---

## When to Use

- Simple and well-defined grammar
- Recursively combinable expressions
- DSL (Domain Specific Language)
- Configurable business rules

## When NOT to Use

- Complex grammar (use a parser generator)
- Performance critical
- Language that evolves frequently

---

## Advantages / Disadvantages

| Advantages | Disadvantages |
|-----------|---------------|
| Easy to extend | Complex grammars are difficult |
| Explicit grammar | Limited performance |
| Combinable expressions | Many classes |
| | Maintenance if grammar changes |

---

## Related Patterns

| Pattern | Relation |
|---------|----------|
| Composite | Tree structure of expressions |
| Visitor | Alternative for interpretation |
| Flyweight | Share terminal expressions |
| Iterator | Traverse tokens |

---

## Framework Implementations

| Framework/Lib | Implementation |
|---------------|----------------|
| regexp | Regular expressions |
| text/template | Go templates |
| go/parser | Go parser |

---

## Anti-patterns to Avoid

| Anti-pattern | Problem | Solution |
|--------------|----------|----------|
| Grammar too complex | Difficult maintenance | Parser generator |
| Infinite recursion | Stack overflow | Grammar validation |
| Shared mutable context | Race conditions | Immutable context |

---

## Tests

```go
func TestNumberExpression(t *testing.T) {
    expr:= NewNumberExpression(42)
    ctx:= NewContext()

    if expr.Interpret(ctx) != 42 {
        t.Error("expected 42")
    }
}

func TestVariableExpression(t *testing.T) {
    ctx:= NewContext()
    ctx.Variables["x"] = 10

    expr:= NewVariableExpression("x")
    if expr.Interpret(ctx) != 10 {
        t.Error("expected 10")
    }
}

func TestAddExpression(t *testing.T) {
    ctx:= NewContext()
    expr:= NewAddExpression(
        NewNumberExpression(5),
        NewNumberExpression(3),
    )

    if expr.Interpret(ctx) != 8 {
        t.Error("expected 8")
    }
}

func TestBoolAndExpression(t *testing.T) {
    ctx:= NewBoolContext()
    ctx.Facts["a"] = true
    ctx.Facts["b"] = false

    expr:= NewAndExpression(
        NewFactExpression("a"),
        NewFactExpression("b"),
    )

    if expr.Interpret(ctx) != false {
        t.Error("expected false (true AND false)")
    }
}

func TestBoolOrExpression(t *testing.T) {
    ctx:= NewBoolContext()
    ctx.Facts["a"] = true
    ctx.Facts["b"] = false

    expr:= NewOrExpression(
        NewFactExpression("a"),
        NewFactExpression("b"),
    )

    if expr.Interpret(ctx) != true {
        t.Error("expected true (true OR false)")
    }
}

func TestRuleEngine(t *testing.T) {
    ctx:= NewBoolContext()
    ctx.Facts["is_admin"] = true

    engine:= NewRuleEngine()
    engine.AddRule("test", NewFactExpression("is_admin"))

    if !engine.Evaluate("test", ctx) {
        t.Error("expected rule to pass")
    }
}
```

---

## Sources

- [Refactoring Guru - Interpreter](https://refactoring.guru/design-patterns/interpreter)
- [Gang of Four - Design Patterns](https://en.wikipedia.org/wiki/Design_Patterns)
- [Crafting Interpreters](https://craftinginterpreters.com/)
