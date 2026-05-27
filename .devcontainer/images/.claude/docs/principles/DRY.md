# DRY - Don't Repeat Yourself

> Every piece of knowledge must have a single, unambiguous representation within a system.

**Authors:** Andrew Hunt & David Thomas (The Pragmatic Programmer, 1999)

## Principle

**DRY is not only about duplicated code, but any form of knowledge duplication:**

- Code
- Documentation
- Configuration
- Data schemas
- Processes

## Examples

### Code

```go
// ❌ WET (Write Everything Twice)
func validateEmail(email string) bool {
	matched, _ := regexp.MatchString(`^[^\s@]+@[^\s@]+\.[^\s@]+$`, email)
	return matched
}

func validateUserEmail(email string) bool {
	matched, _ := regexp.MatchString(`^[^\s@]+@[^\s@]+\.[^\s@]+$`, email) // Duplicated
	return matched
}

// ✅ DRY
var emailRegex = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

func validateEmail(email string) bool {
	return emailRegex.MatchString(email)
}
```

### Configuration

```go
// ❌ WET
type Config struct {
	Development EnvironmentConfig
	Staging     EnvironmentConfig
}

type EnvironmentConfig struct {
	DatabaseHost string
	DatabasePort int  // Duplicated everywhere
	DatabaseName string
}

var config = Config{
	Development: EnvironmentConfig{
		DatabaseHost: "localhost",
		DatabasePort: 5432,
		DatabaseName: "myapp_dev",
	},
	Staging: EnvironmentConfig{
		DatabaseHost: "staging.example.com",
		DatabasePort: 5432, // Duplicated
		DatabaseName: "myapp_staging",
	},
}

// ✅ DRY
const DefaultDatabasePort = 5432

type EnvironmentConfig struct {
	DatabaseHost string
	DatabasePort int
	DatabaseName string
}

func NewEnvironmentConfig(host, name string) EnvironmentConfig {
	return EnvironmentConfig{
		DatabaseHost: host,
		DatabasePort: DefaultDatabasePort,
		DatabaseName: name,
	}
}

var config = Config{
	Development: NewEnvironmentConfig("localhost", "myapp_dev"),
	Staging:     NewEnvironmentConfig("staging.example.com", "myapp_staging"),
}
```

### Documentation

```go
// ❌ WET - Doc and code out of sync
// CalculateTotal calculates the total price with 20% tax
func CalculateTotal(price float64) float64 {
	return price * 1.15 // Bug: doc says 20%, code does 15%
}

// ✅ DRY - Single source of truth
const TaxRate = 0.20

// CalculateTotal calculates the total price with tax.
func CalculateTotal(price float64) float64 {
	return price * (1 + TaxRate)
}
```

## When NOT to Apply DRY

### Accidental Coupling

```go
// ❌ Bad DRY abstraction
func processEntity(entity interface{}) error {
	// Very different logic depending on type
	// → Better to have 3 separate functions
	switch e := entity.(type) {
	case *User:
		// ...
	case *Product:
		// ...
	case *Order:
		// ...
	default:
		return errors.New("unknown entity type")
	}
	return nil
}

// ✅ Acceptable duplication
func processUser(user *User) error {
	// User-specific logic
	return nil
}

func processProduct(product *Product) error {
	// Product-specific logic
	return nil
}

func processOrder(order *Order) error {
	// Order-specific logic
	return nil
}
```

### Rule of Three

> Duplicating twice is acceptable. On the third occurrence, refactor.

Reason: Avoid premature abstractions.

## Anti-pattern: WET

**WET = Write Everything Twice** (or "Waste Everyone's Time")

Symptoms:

- Same bug to fix in multiple places
- Business rule change = multiple modifications
- "I forgot to update the other place"

## Related Patterns

| Pattern | Relationship with DRY |
|---------|----------------------|
| Template Method | Factorize the algorithm skeleton |
| Strategy | Factorize algorithm variations |
| Decorator | Avoid duplication in subclasses |
| Factory | Centralize creation logic |

## When to Use

- When the same business logic appears in multiple places
- When centralizing constants or configurations
- To synchronize documentation and code (single source of truth)
- When a bug needs to be fixed in multiple identical locations
- After the 3rd occurrence of a similar pattern (rule of three)

## Checklist

- [ ] Does this code exist elsewhere?
- [ ] Is this config duplicated?
- [ ] Are the doc and code synchronized?
- [ ] Are the constants centralized?

## Sources

- [The Pragmatic Programmer](https://pragprog.com/titles/tpp20/)
- [Wikipedia - DRY](https://en.wikipedia.org/wiki/Don%27t_repeat_yourself)
