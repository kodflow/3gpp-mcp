<!-- updated: 2026-04-10T12:00:00Z -->
# Design Patterns Knowledge Base

## Purpose

152 pattern files across 19 categories. **Consult during /plan and /review.**

## Categories

| Category | Files | Use For |
|----------|-------|---------|
| `architectural/` | 9 | MVC, Hexagonal, CQRS, Event-Driven |
| `behavioral/` | 11 | Observer, Strategy, Command |
| `cloud/` | 21 | Circuit Breaker, Saga, Service Mesh |
| `concurrency/` | 8 | Thread Pool, Actor, Mutex |
| `conventions/` | 1 | DTO tags convention |
| `creational/` | 4 | Factory, Builder, Singleton |
| `ddd/` | 8 | Aggregate, Entity, Repository |
| `devops/` | 14 | Feature Toggles, Blue-Green, GitOps |
| `enterprise/` | 12 | PoEAA (Martin Fowler) |
| `functional/` | 5 | Monad, Either, Lens |
| `integration/` | 5 | API Gateway, BFF, Strangler |
| `messaging/` | 10 | EIP patterns, Pub/Sub |
| `performance/` | 8 | Cache, Lazy Load, Pool |
| `principles/` | 6 | SOLID, DRY, KISS |
| `refactoring/` | 1 | Refactoring catalog |
| `resilience/` | 6 | Bulkhead, Retry, Timeout |
| `security/` | 8 | OAuth, JWT, RBAC |
| `structural/` | 7 | Adapter, Decorator, Proxy |
| `testing/` | 8 | Mock, Stub, Fixture |

## Quick Lookup

| Problem | Pattern | File |
|---------|---------|------|
| Complex creation | Builder | `creational/builder.md` |
| Expensive objects | Object Pool | `performance/object-pool.md` |
| Race conditions | Mutex | `concurrency/mutex-semaphore.md` |
| Cascade failures | Circuit Breaker | `cloud/circuit-breaker.md` |
| Authentication | OAuth/JWT | `security/oauth2.md` |

## When to Consult

**During /plan:**
1. Read `README.md` for category index
2. Find applicable patterns (1-3)
3. Include in plan with justification

**During /review:**
1. Identify patterns in code
2. Verify correct implementation
3. Suggest alternatives if needed

## Pattern Format

Each pattern contains:
- Title + one-line description
- Go code example
- When to use
- Related patterns

Templates: `TEMPLATE-PATTERN.md`, `TEMPLATE-README.md`

## Sources

GoF (23), Fowler PoEAA (40+), EIP (65), Azure Patterns, DDD, FP patterns
