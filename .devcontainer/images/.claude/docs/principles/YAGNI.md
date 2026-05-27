# YAGNI - You Aren't Gonna Need It

> Never implement something before you actually need it.

**Origin:** Kent Beck, Extreme Programming (XP)

## Principle

Resist the temptation to add features "just in case".

**Cost of premature features:**

- Development time
- Testing time
- Added complexity
- Future maintenance
- Often never used

## Examples

### Code

```go
// ❌ YAGNI violation
type UserService struct {
	repo Repository
}

func (s *UserService) GetUser(id string) (*User, error) { /* ... */ }
func (s *UserService) GetUserWithCache(id string) (*User, error) { /* ... */ }  // "We'll need caching"
func (s *UserService) GetUserAsync(id string) <-chan *User { /* ... */ }       // "Maybe async someday"
func (s *UserService) GetUserBatch(ids []string) ([]*User, error) { /* ... */ } // "Just in case"
func (s *UserService) GetUserWithRetry(id string) (*User, error) { /* ... */ }  // "For resilience"

// ✅ YAGNI
type UserService struct {
	repo Repository
}

func (s *UserService) GetUser(id string) (*User, error) { /* ... */ }
// Add the others WHEN we need them
```

### Configuration

```go
// ❌ YAGNI violation
type Config struct {
	Database DatabaseConfig
}

type DatabaseConfig struct {
	Host              string
	Port              int
	SSL               bool
	PoolSize          int
	MaxRetries        int
	RetryDelay        time.Duration
	ConnectionTimeout time.Duration
	QueryTimeout      time.Duration
	IdleTimeout       time.Duration
	// 20 more options "just in case"
}

// ✅ YAGNI
type Config struct {
	Database DatabaseConfig
}

type DatabaseConfig struct {
	Host string
	Port int
}
// Add SSL WHEN deploying to production
// Add PoolSize WHEN we have performance issues
```

### Architecture

```
❌ YAGNI violation (Day 1 of an MVP)
┌─────────┐  ┌─────────┐  ┌─────────┐  ┌─────────┐
│ Gateway │─▶│ Service │─▶│  Cache  │─▶│   DB    │
└─────────┘  └─────────┘  └─────────┘  └─────────┘
                 │
                 ▼
           ┌─────────┐
           │  Queue  │
           └─────────┘
                 │
                 ▼
           ┌─────────┐
           │ Worker  │
           └─────────┘

✅ YAGNI (Day 1 of an MVP)
┌─────────┐
│   App   │───▶ SQLite
└─────────┘

(Evolve WHEN necessary)
```

## Exceptions

YAGNI does not apply to:

### 1. Security

```go
// ✅ Always include (not YAGNI)
func HashPassword(password string) (string, error) {
	return bcrypt.GenerateFromPassword([]byte(password), 12)
}
```

### 2. Architecture difficult to change

```go
// ✅ Think about it from the start
type Database interface {
	Query(ctx context.Context, sql string, args ...interface{}) (*sql.Rows, error)
}
// Because changing the DB interface later = very costly
```

### 3. Public API contracts

```go
// ✅ Version from the start
// /api/v1/users
// Because changing = breaking change for clients
```

## YAGNI vs Anticipation

| YAGNI (Good) | Anticipation (Acceptable) |
|--------------|---------------------------|
| "We might need MongoDB" | Database interface abstraction |
| "Let's add a Redis cache" | No cache for now |
| "Let's prepare for multi-tenant" | Simple architecture |
| "Let's support 10 languages" | Basic i18n support |

## Workflow

```
1. Need identified
2. Minimal solution
3. Deliver
4. Feedback
5. Iterate if necessary
```

## YAGNI Violation Signals

- "We might need..."
- "Just in case..."
- "For the future..."
- "It would be nice to have..."
- "Someday we'll want..."

## Relationship with Other Principles

| Principle | Relationship |
|-----------|--------------|
| KISS | YAGNI maintains simplicity |
| DRY | Apply DRY to actual needs only |
| SOLID | Apply SOLID progressively |

## Checklist

- [ ] Does this need exist today?
- [ ] Has a user requested it?
- [ ] What happens if we don't do it?
- [ ] Can we add it easily later?

## When to Use

- Before adding a feature "just in case" or "for the future"
- When designing an architecture for an MVP or a prototype
- When hesitating to add extra configuration options
- To evaluate whether an abstraction is truly needed now
- During code reviews to challenge speculative additions

## Related Patterns

- [KISS](./KISS.md) - Complementary: YAGNI avoids unnecessary complexity
- [DRY](./DRY.md) - Apply DRY only to actual needs
- [SOLID](./SOLID.md) - Apply progressively according to needs

## Sources

- [Extreme Programming Explained - Kent Beck](https://www.amazon.com/Extreme-Programming-Explained-Embrace-Change/dp/0321278658)
- [Wikipedia - YAGNI](https://en.wikipedia.org/wiki/You_aren%27t_gonna_need_it)
- [Martin Fowler - YAGNI](https://martinfowler.com/bliki/Yagni.html)
