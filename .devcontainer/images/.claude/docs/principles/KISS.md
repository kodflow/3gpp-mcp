# KISS - Keep It Simple, Stupid

> Simplicity should be a key goal in design.

**Origin:** Kelly Johnson, Lockheed engineer (1960s)

## Principle

Complexity is the enemy of reliability. Simple code is:

- Easier to read
- Easier to maintain
- Easier to test
- Less prone to bugs

## Examples

### Conditional Logic

```go
// ❌ Complex
func GetDiscount(user *User) float64 {
	if user.IsPremium {
		if user.Years > 5 {
			if user.Orders > 100 {
				return 0.25
			} else {
				return 0.20
			}
		} else {
			return 0.15
		}
	} else {
		if user.Orders > 50 {
			return 0.10
		} else {
			return 0.05
		}
	}
}

// ✅ Simple - Guard clauses
func GetDiscount(user *User) float64 {
	if user.IsPremium && user.Years > 5 && user.Orders > 100 {
		return 0.25
	}
	if user.IsPremium && user.Years > 5 {
		return 0.20
	}
	if user.IsPremium {
		return 0.15
	}
	if user.Orders > 50 {
		return 0.10
	}
	return 0.05
}

// ✅✅ Even simpler with a table
type DiscountRule struct {
	Condition func(*User) bool
	Discount  float64
}

var DiscountRules = []DiscountRule{
	{func(u *User) bool { return u.IsPremium && u.Years > 5 && u.Orders > 100 }, 0.25},
	{func(u *User) bool { return u.IsPremium && u.Years > 5 }, 0.20},
	{func(u *User) bool { return u.IsPremium }, 0.15},
	{func(u *User) bool { return u.Orders > 50 }, 0.10},
}

func GetDiscount(user *User) float64 {
	for _, rule := range DiscountRules {
		if rule.Condition(user) {
			return rule.Discount
		}
	}
	return 0.05
}
```

### Architecture

```
❌ Complex (premature)
┌─────────┐    ┌─────────┐    ┌─────────┐
│ Gateway │───▶│ Service │───▶│   DB    │
└─────────┘    └─────────┘    └─────────┘
      │              │              │
      ▼              ▼              ▼
┌─────────┐    ┌─────────┐    ┌─────────┐
│  Cache  │    │  Queue  │    │ Replica │
└─────────┘    └─────────┘    └─────────┘

✅ Simple (to start with)
┌─────────┐    ┌─────────┐
│   App   │───▶│   DB    │
└─────────┘    └─────────┘
```

### Functions

```go
// ❌ Overly "smart" function
type ProcessOptions struct {
	Validate  bool
	Transform bool
	Cache     bool
	Log       bool
	Retry     int
}

func ProcessData(data interface{}, options *ProcessOptions) (interface{}, error) {
	// 100 lines of code covering all cases
	if options == nil {
		options = &ProcessOptions{}
	}
	// ... complexity
	return nil, nil
}

// ✅ Simple and composable functions
func ValidateData(data interface{}) error {
	// Simple validation
	return nil
}

func TransformData(data interface{}) (interface{}, error) {
	// Simple transformation
	return data, nil
}

func CacheData(data interface{}) error {
	// Simple caching
	return nil
}

// Clear composition
func ProcessDataSimple(data interface{}) (interface{}, error) {
	if err := ValidateData(data); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	transformed, err := TransformData(data)
	if err != nil {
		return nil, fmt.Errorf("transformation failed: %w", err)
	}

	if err := CacheData(transformed); err != nil {
		return nil, fmt.Errorf("caching failed: %w", err)
	}

	return transformed, nil
}
```

## Complexity Signals

| Signal | Action |
|--------|--------|
| Function > 20 lines | Split |
| More than 3 indentation levels | Extract |
| Comment saying "it's complicated" | Simplify |
| Hard to explain | Rethink |
| Too many parameters (>3) | Create a config struct |

## When Complexity Is Necessary

KISS does not mean "no complexity". Sometimes it is justified:

- Performance optimization proven by benchmarks
- Genuinely complex business requirements
- Unavoidable technical constraints

In those cases, **document the why**.

## Relationship with Other Principles

| Principle | Relationship |
|-----------|--------------|
| YAGNI | Don't add unnecessary complexity |
| DRY | But not at the cost of readability |
| SOLID | Can add structural complexity |

## Checklist

- [ ] Can someone understand it in 5 minutes?
- [ ] Can you explain it without saying "it's complicated"?
- [ ] Is there a simpler solution?
- [ ] Is this abstraction really necessary?

## When to Use

- During initial design of a module or feature
- When code becomes hard to explain or understand
- During code reviews to identify accidental complexity
- Before adding an abstraction or indirection level
- When refactoring overly complex legacy code

## Related Patterns

- [YAGNI](./YAGNI.md) - Complementary: avoid unnecessary complexity
- [DRY](./DRY.md) - Be careful not to over-abstract in the name of DRY
- [Defensive Programming](./defensive.md) - Guard clauses simplify conditions

## Sources

- [Wikipedia - KISS](https://en.wikipedia.org/wiki/KISS_principle)
- [Simple Made Easy - Rich Hickey](https://www.infoq.com/presentations/Simple-Made-Easy/)
