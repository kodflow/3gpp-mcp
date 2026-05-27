# Property-Based Testing

> Generative tests that verify properties on random data.

## Principle

```
┌─────────────────────────────────────────────────────────────────┐
│                   Property-Based Testing                         │
│                                                                  │
│   Traditional:  test(input1) → expected1                        │
│                 test(input2) → expected2                        │
│                                                                  │
│   Property:     forAll(inputs) → property holds                 │
│                                                                  │
│   Generator ──► Random Input ──► Function ──► Property Check    │
│       │                                            │             │
│       └────────────── Shrinking on failure ◄───────┘             │
└─────────────────────────────────────────────────────────────────┘
```

## gopter Basics

```go
package string_test

import (
	"strings"
	"testing"

	"github.com/leanovate/gopter"
	"github.com/leanovate/gopter/gen"
	"github.com/leanovate/gopter/prop"
)

func TestStringOperations(t *testing.T) {
	properties := gopter.NewProperties(nil)

	// Property: reverse(reverse(s)) === s
	properties.Property("reverse is its own inverse", prop.ForAll(
		func(s string) bool {
			reversed := reverseString(s)
			doubleReversed := reverseString(reversed)
			return doubleReversed == s
		},
		gen.AnyString(),
	))

	// Property: length is preserved
	properties.Property("reverse preserves length", prop.ForAll(
		func(s string) bool {
			reversed := reverseString(s)
			return len(reversed) == len(s)
		},
		gen.AnyString(),
	))

	properties.TestingRun(t)
}

func TestMathOperations(t *testing.T) {
	properties := gopter.NewProperties(nil)

	// Property: addition is commutative
	properties.Property("a + b === b + a", prop.ForAll(
		func(a, b int) bool {
			return a+b == b+a
		},
		gen.Int(),
		gen.Int(),
	))

	// Property: addition is associative
	properties.Property("(a + b) + c === a + (b + c)", prop.ForAll(
		func(a, b, c int) bool {
			return (a+b)+c == a+(b+c)
		},
		gen.Int(),
		gen.Int(),
		gen.Int(),
	))

	// Property: zero is identity
	properties.Property("a + 0 === a", prop.ForAll(
		func(a int) bool {
			return a+0 == a
		},
		gen.Int(),
	))

	properties.TestingRun(t)
}

func reverseString(s string) string {
	runes := []rune(s)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes)
}
```

## Generators

```go
package gen_test

import (
	"testing"

	"github.com/leanovate/gopter"
	"github.com/leanovate/gopter/gen"
	"github.com/leanovate/gopter/prop"
)

func TestGenerators(t *testing.T) {
	properties := gopter.NewProperties(nil)

	// Built-in generators
	_ = gen.Int()                              // Any integer
	_ = gen.IntRange(0, 100)                   // Range
	_ = gen.UInt()                             // Unsigned integers
	_ = gen.Float64()                          // Floating point
	_ = gen.AnyString()                        // Any string
	_ = gen.Identifier()                       // Valid identifiers
	_ = gen.Bool()                             // true/false
	_ = gen.Time()                             // Time values
	_ = gen.UUID()                             // UUIDs

	// Slices and maps
	_ = gen.SliceOf(gen.Int())                 // Slice of integers
	_ = gen.SliceOfN(10, gen.String())         // Fixed size
	_ = gen.MapOf(gen.String(), gen.Int())     // Map

	// Structs
	userGen := gen.Struct(reflect.TypeOf(&User{}), map[string]gopter.Gen{
		"ID":    gen.UUID(),
		"Email": gen.RegexMatch(`[a-z]+@[a-z]+\.[a-z]+`),
		"Name":  gen.Identifier(),
		"Age":   gen.IntRange(0, 120),
		"Role":  gen.OneConstOf("admin", "member", "guest"),
	})

	properties.Property("user has valid age", prop.ForAll(
		func(user *User) bool {
			return user.Age >= 0 && user.Age <= 120
		},
		userGen,
	))

	properties.TestingRun(t)
}

// Custom generators
func genPositiveEven() gopter.Gen {
	return gen.IntRange(1, 1000).
		SuchThat(func(v interface{}) bool {
			return v.(int) > 0
		}).
		Map(func(v interface{}) interface{} {
			return v.(int) * 2
		})
}

func genSliceWithIndex() gopter.Gen {
	return gen.SliceOf(gen.AnyString(), reflect.TypeOf([]string{})).
		SuchThat(func(v interface{}) bool {
			return len(v.([]string)) > 0
		}).
		FlatMap(func(v interface{}) gopter.Gen {
			arr := v.([]string)
			return gen.Struct(reflect.TypeOf(&SliceWithIndex{}), map[string]gopter.Gen{
				"Slice": gen.Const(arr),
				"Index": gen.IntRange(0, len(arr)-1),
			})
		}, reflect.TypeOf(&SliceWithIndex{}))
}

type SliceWithIndex struct {
	Slice []string
	Index int
}
```

## Common Properties

```go
package properties_test

import (
	"encoding/json"
	"testing"

	"github.com/leanovate/gopter"
	"github.com/leanovate/gopter/gen"
	"github.com/leanovate/gopter/prop"
)

func TestCommonProperties(t *testing.T) {
	properties := gopter.NewProperties(nil)

	// 1. Roundtrip / Serialization
	properties.Property("parse(stringify(x)) === x", prop.ForAll(
		func(data map[string]interface{}) bool {
			serialized, err := json.Marshal(data)
			if err != nil {
				return false
			}
			var parsed map[string]interface{}
			if err := json.Unmarshal(serialized, &parsed); err != nil {
				return false
			}
			reserialized, _ := json.Marshal(parsed)
			return string(serialized) == string(reserialized)
		},
		gen.MapOf(gen.Identifier(), gen.OneGenOf(
			gen.Int(),
			gen.String(),
			gen.Bool(),
		)),
	))

	// 2. Idempotence
	properties.Property("sort is idempotent", prop.ForAll(
		func(arr []int) bool {
			sorted1 := make([]int, len(arr))
			copy(sorted1, arr)
			sort.Ints(sorted1)

			sorted2 := make([]int, len(sorted1))
			copy(sorted2, sorted1)
			sort.Ints(sorted2)

			return slicesEqual(sorted1, sorted2)
		},
		gen.SliceOf(gen.Int()),
	))

	// 3. Invariants
	properties.Property("push increases length by 1", prop.ForAll(
		func(arr []int, elem int) bool {
			originalLength := len(arr)
			newArr := append(arr, elem)
			return len(newArr) == originalLength+1
		},
		gen.SliceOf(gen.Int()),
		gen.Int(),
	))

	properties.Property("filter result is subset", prop.ForAll(
		func(arr []int) bool {
			filtered := filterPositive(arr)
			for _, v := range filtered {
				if !contains(arr, v) {
					return false
				}
			}
			return true
		},
		gen.SliceOf(gen.Int()),
	))

	// 4. Oracle / Reference implementation
	properties.Property("binary search finds same as linear", prop.ForAll(
		func(arr []int, target int) bool {
			sorted := make([]int, len(arr))
			copy(sorted, arr)
			sort.Ints(sorted)

			binaryResult := binarySearch(sorted, target)
			linearResult := linearSearch(sorted, target)
			return binaryResult == linearResult
		},
		gen.SliceOf(gen.Int()),
		gen.Int(),
	))

	// 5. Metamorphic testing
	properties.Property("multiply by 2 equals add to self", prop.ForAll(
		func(n int) bool {
			// Avoid overflow
			if n > 100000 || n < -100000 {
				return true
			}
			return n*2 == n+n
		},
		gen.IntRange(-1000, 1000),
	))

	properties.TestingRun(t)
}

func filterPositive(arr []int) []int {
	var result []int
	for _, v := range arr {
		if v > 0 {
			result = append(result, v)
		}
	}
	return result
}

func contains(arr []int, elem int) bool {
	for _, v := range arr {
		if v == elem {
			return true
		}
	}
	return false
}

func slicesEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
```

## Domain-Specific Generators

```go
package ecommerce_test

import (
	"testing"

	"github.com/leanovate/gopter"
	"github.com/leanovate/gopter/gen"
	"github.com/leanovate/gopter/prop"
)

// E-commerce domain
func genProduct() gopter.Gen {
	return gen.Struct(reflect.TypeOf(&Product{}), map[string]gopter.Gen{
		"ID":       gen.UUID(),
		"Name":     gen.Identifier(),
		"Price":    gen.Float64Range(0.01, 10000),
		"Stock":    gen.IntRange(0, 1000),
		"Category": gen.OneConstOf("electronics", "clothing", "food"),
	})
}

func genOrderItem() gopter.Gen {
	return gen.Struct(reflect.TypeOf(&OrderItem{}), map[string]gopter.Gen{
		"ProductID": gen.UUID(),
		"Quantity":  gen.IntRange(1, 100),
		"UnitPrice": gen.Float64Range(0.01, 10000),
	})
}

func genOrder() gopter.Gen {
	return gen.Struct(reflect.TypeOf(&Order{}), map[string]gopter.Gen{
		"ID":        gen.UUID(),
		"UserID":    gen.UUID(),
		"Items":     gen.SliceOfN(5, genOrderItem()),
		"Status":    gen.OneConstOf("pending", "confirmed", "shipped", "delivered"),
		"CreatedAt": gen.Time(),
	})
}

func TestOrderTotal(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("order total equals sum of items", prop.ForAll(
		func(order *Order) bool {
			calculatedTotal := 0.0
			for _, item := range order.Items {
				calculatedTotal += float64(item.Quantity) * item.UnitPrice
			}

			orderTotal := CalculateOrderTotal(order)

			// Allow small floating point differences
			diff := calculatedTotal - orderTotal
			if diff < 0 {
				diff = -diff
			}
			return diff < 0.01
		},
		genOrder(),
	))

	properties.TestingRun(t)
}
```

## Shrinking

```go
package shrink_test

import (
	"testing"

	"github.com/leanovate/gopter"
	"github.com/leanovate/gopter/gen"
	"github.com/leanovate/gopter/prop"
)

func TestShrinking(t *testing.T) {
	properties := gopter.NewProperties(nil)

	// gopter automatically shrinks failing cases
	properties.Property("all elements are non-negative", prop.ForAll(
		func(arr []int) bool {
			// This will fail for arrays with negative numbers
			for _, v := range arr {
				if v < 0 {
					return false
				}
			}
			return true
		},
		gen.SliceOf(gen.Int()),
	))

	// Shrinking tries to find minimal failing case
	// e.g., [1, -1, 2, 3, 4] shrinks to [-1]
	properties.TestingRun(t)
}

func TestWithCustomParameters(t *testing.T) {
	params := gopter.DefaultTestParameters()
	params.MinSuccessfulTests = 1000 // Run 1000 tests
	params.MaxSize = 100              // Max size for collections
	params.Rng.Seed(42)               // Reproducible runs

	properties := gopter.NewProperties(params)

	properties.Property("string length < 10", prop.ForAll(
		func(s string) bool {
			return len(s) < 10
		},
		gen.AnyString(),
	))

	properties.TestingRun(t)
}
```

## rapid Alternative

```go
package rapid_test

import (
	"testing"

	"pgregory.net/rapid"
)

func TestWithRapid(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate random string
		s := rapid.String().Draw(t, "s")

		// Property: reverse is its own inverse
		reversed := reverseString(s)
		doubleReversed := reverseString(reversed)

		if doubleReversed != s {
			t.Fatalf("reverse(reverse(%q)) = %q; want %q", s, doubleReversed, s)
		}
	})
}

func TestDatabaseRoundtrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate random user
		user := &User{
			ID:    rapid.String().Draw(t, "id"),
			Name:  rapid.String().Draw(t, "name"),
			Email: rapid.String().Draw(t, "email"),
			Age:   rapid.IntRange(0, 120).Draw(t, "age"),
		}

		// Test roundtrip
		saved := saveUser(user)
		retrieved := getUser(saved.ID)

		if retrieved.Email != user.Email {
			t.Fatalf("email mismatch: got %q, want %q", retrieved.Email, user.Email)
		}
	})
}
```

## Recommended Libraries

| Package | Usage |
|---------|-------|
| `github.com/leanovate/gopter` | Property-based testing |
| `pgregory.net/rapid` | Alternative, simpler API |
| `github.com/flyingmutant/rapid` | Another alternative |

## Common Mistakes

| Mistake | Impact | Solution |
|---------|--------|----------|
| Too broad generators | Slow tests, false positives | Constrain the domains |
| Ignoring shrinking | Difficult debugging | Analyze minimal cases |
| Too many tests | Slow tests | 100-1000 is often enough |
| Trivial properties | Useless tests | Test real invariants |
| Forgetting edge cases | Missed bugs | Combine with example-based |

## When to Use

| Scenario | Recommended |
|----------|------------|
| Pure algorithms | Yes |
| Serialization/parsing | Yes |
| Data transformations | Yes |
| Complex business logic | Yes |
| UI testing | No |
| Integration with external | With caution |

## Related Patterns

- **Parameterized Tests**: Manual version
- **Fuzzing**: Similar security testing
- **Snapshot Testing**: Complementary for regression

## Sources

- [gopter Documentation](https://github.com/leanovate/gopter)
- [rapid Documentation](https://pkg.go.dev/pgregory.net/rapid)
- [Property-Based Testing Guide](https://fsharpforfunandprofit.com/posts/property-based-testing-2/)
