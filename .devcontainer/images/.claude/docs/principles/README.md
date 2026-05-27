# Design Principles

Fundamental principles and defensive programming patterns.

---

## Files

| File | Content | Patterns |
|------|---------|----------|
| [SOLID.md](SOLID.md) | 5 OOP principles | SRP, OCP, LSP, ISP, DIP |
| [DRY.md](DRY.md) | Don't Repeat Yourself | Factorization |
| [KISS.md](KISS.md) | Keep It Simple | Simplicity |
| [YAGNI.md](YAGNI.md) | You Ain't Gonna Need It | Avoid over-engineering |
| [GRASP.md](GRASP.md) | 9 responsibility patterns | Expert, Creator, Controller... |
| [defensive.md](defensive.md) | 11 defensive patterns | Guard Clause, Assertions... |

---

## SOLID (5 principles)

| Principle | Description |
|-----------|-------------|
| **S**ingle Responsibility | One class = one reason to change |
| **O**pen/Closed | Open for extension, closed for modification |
| **L**iskov Substitution | Subtypes must be substitutable |
| **I**nterface Segregation | Specific interfaces > generic ones |
| **D**ependency Inversion | Depend on abstractions |

---

## GRASP (9 patterns)

| Pattern | Question | Answer |
|---------|----------|--------|
| Information Expert | Who does X? | The one who has the data |
| Creator | Who creates X? | The one who contains/uses X |
| Controller | Who receives requests? | A dedicated coordinator |
| Low Coupling | Reduce dependencies? | Interfaces, DI |
| High Cohesion | Keep focus? | One responsibility per class |
| Polymorphism | Avoid switch on type? | Interfaces + implementations |
| Pure Fabrication | Orphan logic? | Dedicated class (Service, Repo) |
| Indirection | Decouple A from B? | Add an intermediary |
| Protected Variations | Isolate changes? | Stable interfaces |

---

## Defensive Programming (11 patterns)

| Pattern | Problem | Solution |
|---------|---------|----------|
| Guard Clause | Nested conditions | Validation early return |
| Assertions | Violated invariants | Verify explicitly |
| Null Object | Repeated null checks | Neutral object |
| Optional Chaining | Nullable properties | `?.` and `??` |
| Default Values | Missing values | Safe defaults |
| Fail-Fast | Silent errors | Fail immediately |
| Input Validation | External data | Validate at boundaries |
| Type Guards | Unknown types | TypeScript narrowing |
| Immutability | Accidental modifications | Immutable data |
| Dependency Validation | Missing dependencies | Verify at startup |
| Design by Contract | Formal guarantees | Pre/post conditions |

---

## Quick Decision Table

| Problem | Principle/Pattern |
|---------|-------------------|
| Class does too many things | SRP, High Cohesion |
| Duplicated code | DRY |
| Overly complex code | KISS |
| "Just in case" feature | YAGNI |
| Null variables everywhere | Guard Clause, Null Object |
| Nested conditions | Guard Clause |
| Tight coupling | Low Coupling, DIP |
| Switch on types | Polymorphism |
| Where to put the logic? | Information Expert |
| Who creates the objects? | Creator |

---

## Application Hierarchy

```
1. SOLID (OOP fundamentals)
       │
2. GRASP (responsibility assignment)
       │
3. Defensive (robustness)
       │
4. GoF Patterns (concrete solutions)
```

**Rule:** Principles guide the choice of patterns.

---

## Sources

- [SOLID - Robert C. Martin](https://en.wikipedia.org/wiki/SOLID)
- [GRASP - Craig Larman](https://en.wikipedia.org/wiki/GRASP_(object-oriented_design))
- [Defensive Programming](https://en.wikipedia.org/wiki/Defensive_programming)
