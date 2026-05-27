# Function Composition Pattern

> Combining simple functions to build more complex functions - the output of one function becomes the input of the next.

## Definition

**Function Composition** is the act of combining simple functions to build more complex ones. The output of one function becomes the input of another, creating a pipeline of transformations.

```
compose(f, g)(x) = f(g(x))   // Right to left
pipe(f, g)(x) = g(f(x))      // Left to right (more readable)
```

**Key characteristics:**

- **Declarative**: Describes what, not how
- **Reusable**: Small functions combine freely
- **Testable**: Each function tested in isolation
- **Point-free**: Often eliminates intermediate variables
- **Type-safe**: TypeScript infers composed types

## TypeScript Implementation

```go
package composition

// Compose combines two functions right to left: compose(f, g)(x) = f(g(x))
func Compose[A, B, C any](f func(B) C, g func(A) B) func(A) C {
	return func(a A) C {
		return f(g(a))
	}
}

// Pipe combines two functions left to right: pipe(f, g)(x) = g(f(x))
func Pipe[A, B, C any](f func(A) B, g func(B) C) func(A) C {
	return func(a A) C {
		return g(f(a))
	}
}

// Pipe3 chains three functions left to right
func Pipe3[A, B, C, D any](
	f func(A) B,
	g func(B) C,
	h func(C) D,
) func(A) D {
	return func(a A) D {
		return h(g(f(a)))
	}
}

// Pipe4 chains four functions left to right
func Pipe4[A, B, C, D, E any](
	f func(A) B,
	g func(B) C,
	h func(C) D,
	i func(D) E,
) func(A) E {
	return func(a A) E {
		return i(h(g(f(a))))
	}
}

// Flow executes functions in sequence with immediate value
func Flow[A any](initial A, fns ...func(A) A) A {
	result := initial
	for _, fn := range fns {
		result = fn(result)
	}
	return result
}
```

## Usage Examples

```go
package main

import (
	"strings"
)

// Simple transformations
func trim(s string) string {
	return strings.TrimSpace(s)
}

func toLowerCase(s string) string {
	return strings.ToLower(s)
}

func split(sep string) func(string) []string {
	return func(s string) []string {
		return strings.Split(s, sep)
	}
}

func join(sep string) func([]string) string {
	return func(arr []string) string {
		return strings.Join(arr, sep)
	}
}

// Compose transformations
var slugify = Pipe4(
	trim,
	toLowerCase,
	split(" "),
	join("-"),
)

// Usage: slugify("  Hello World  ") // "hello-world"

// Data processing pipeline
type User struct {
	ID    string
	Name  string
	Email string
	Age   int
}

var users = []User{
	{ID: "1", Name: "Alice", Email: "alice@example.com", Age: 30},
	{ID: "2", Name: "Bob", Email: "bob@example.com", Age: 25},
	{ID: "3", Name: "Charlie", Email: "charlie@example.com", Age: 35},
}

// Reusable predicates and transformers
func isAdult(u User) bool {
	return u.Age >= 18
}

func isOver30(u User) bool {
	return u.Age > 30
}

func getName(u User) string {
	return u.Name
}

func toUpperCase(s string) string {
	return strings.ToUpper(s)
}

// Filter helper
func filter[T any](predicate func(T) bool) func([]T) []T {
	return func(slice []T) []T {
		result := make([]T, 0, len(slice))
		for _, item := range slice {
			if predicate(item) {
				result = append(result, item)
			}
		}
		return result
	}
}

// Map helper
func mapSlice[A, B any](f func(A) B) func([]A) []B {
	return func(slice []A) []B {
		result := make([]B, len(slice))
		for i, item := range slice {
			result[i] = f(item)
		}
		return result
	}
}

// Compose into pipeline
var getAdultNamesUppercase = Pipe3(
	filter(isAdult),
	mapSlice(getName),
	mapSlice(toUpperCase),
)

// Point-free style with compose
var getNameUpper = Pipe(getName, toUpperCase)
```

## Using fp-ts

```go
package main

import (
	"strings"
)

// Pipe-style immediate execution
func processingPipeline() string {
	input := "  Hello World  "

	result := input
	result = strings.TrimSpace(result)
	result = strings.ToLower(result)
	parts := strings.Split(result, " ")
	result = strings.Join(parts, "-")

	return result // "hello-world"
}

// Flow - creates a reusable function
func createSlugifier() func(string) string {
	return func(s string) string {
		s = strings.TrimSpace(s)
		s = strings.ToLower(s)
		parts := strings.Split(s, " ")
		return strings.Join(parts, "-")
	}
}

// Array operations with generics
type ExtendedUser struct {
	User
	IsActive bool
	Role     string
}

func getActiveAdminEmails(users []ExtendedUser) []string {
	// Filter active users
	active := filter(func(u ExtendedUser) bool { return u.IsActive })(users)

	// Filter admins
	admins := filter(func(u ExtendedUser) bool { return u.Role == "admin" })(active)

	// Map to emails
	emails := mapSlice(func(u ExtendedUser) string { return u.Email })(admins)

	// Remove duplicates
	return unique(emails)
}

func unique[T comparable](slice []T) []T {
	seen := make(map[T]struct{})
	result := make([]T, 0, len(slice))
	for _, item := range slice {
		if _, exists := seen[item]; !exists {
			seen[item] = struct{}{}
			result = append(result, item)
		}
	}
	return result
}

// Option composition (see option.md for full implementation)
func getFirstAdminEmail(users []ExtendedUser) string {
	for _, u := range users {
		if u.Role == "admin" {
			return u.Email
		}
	}
	return "no-admin@example.com"
}

// Either composition (see either.md for full implementation)
type PaymentResult struct {
	Success      bool
	Confirmation string
	Error        string
}

func processPayment(orderID string, amount float64) PaymentResult {
	if err := validateAmount(amount); err != nil {
		return PaymentResult{Success: false, Error: err.Error()}
	}

	order, err := findOrder(orderID)
	if err != nil {
		return PaymentResult{Success: false, Error: err.Error()}
	}

	payment, err := chargeCustomer(order)
	if err != nil {
		return PaymentResult{Success: false, Error: err.Error()}
	}

	return PaymentResult{Success: true, Confirmation: payment.Confirmation}
}

func validateAmount(amount float64) error { return nil }
func findOrder(id string) (Order, error)  { return Order{}, nil }
func chargeCustomer(o Order) (Payment, error) { return Payment{}, nil }

type Order struct{}
type Payment struct{ Confirmation string }
```

## Using Effect

```go
package main

import (
	"context"
	"fmt"
)

// Effect represents a computation that may fail or require context
type Effect[T any] struct {
	run func(context.Context) (T, error)
}

// Succeed creates an Effect that always succeeds
func Succeed[T any](value T) Effect[T] {
	return Effect[T]{
		run: func(ctx context.Context) (T, error) {
			return value, nil
		},
	}
}

// Map transforms the success value
func (e Effect[T]) Map(f func(T) T) Effect[T] {
	return Effect[T]{
		run: func(ctx context.Context) (T, error) {
			val, err := e.run(ctx)
			if err != nil {
				return val, err
			}
			return f(val), nil
		},
	}
}

// FlatMap chains dependent effects
func (e Effect[T]) FlatMap(f func(T) Effect[T]) Effect[T] {
	return Effect[T]{
		run: func(ctx context.Context) (T, error) {
			val, err := e.run(ctx)
			if err != nil {
				var zero T
				return zero, err
			}
			return f(val).run(ctx)
		},
	}
}

// Run executes the effect
func (e Effect[T]) Run(ctx context.Context) (T, error) {
	return e.run(ctx)
}

// Composing effects
func exampleProgram() Effect[string] {
	return Succeed(10).
		Map(func(n int) int { return n * 2 }).
		FlatMap(func(n int) Effect[int] {
			if n > 15 {
				return Succeed(n)
			}
			return Effect[int]{
				run: func(ctx context.Context) (int, error) {
					return 0, fmt.Errorf("too small")
				},
			}
		}).
		Map(func(n int) int { return n }) // Convert to desired type in real code
}
```

## Composition Patterns

### Currying for Composition

```go
package main

// Curried functions compose better
func add(a int) func(int) int {
	return func(b int) int {
		return a + b
	}
}

func multiply(a int) func(int) int {
	return func(b int) int {
		return a * b
	}
}

func example() {
	add5 := add(5)
	double := multiply(2)

	// (x + 5) * 2
	transform := Pipe(add5, double)
	result := transform(10) // 30
}
```

### Partial Application

```go
package main

// Partially apply for reuse
func filterFunc[A any](predicate func(A) bool) func([]A) []A {
	return func(arr []A) []A {
		result := make([]A, 0, len(arr))
		for _, item := range arr {
			if predicate(item) {
				result = append(result, item)
			}
		}
		return result
	}
}

func mapFunc[A, B any](f func(A) B) func([]A) []B {
	return func(arr []A) []B {
		result := make([]B, len(arr))
		for i, item := range arr {
			result[i] = f(item)
		}
		return result
	}
}

func partialExample() {
	adults := filterFunc(func(u User) bool { return u.Age >= 18 })
	names := mapFunc(func(u User) string { return u.Name })

	getAdultNames := Pipe(adults, names)
}
```

### Kleisli Composition (Monadic)

```go
package main

// Functions returning Option (see option.md for full implementation)
func parseNumber(s string) Option[int] {
	// Implementation
	return None[int]()
}

func half(n int) Option[int] {
	if n%2 == 0 {
		return Some(n / 2)
	}
	return None[int]()
}

// Compose with FlatMap
func parseAndHalf(s string) Option[int] {
	return parseNumber(s).FlatMap(half)
}

// Kleisli composition helper
func KleisliCompose[A, B, C any](
	f func(A) Option[B],
	g func(B) Option[C],
) func(A) Option[C] {
	return func(a A) Option[C] {
		return f(a).FlatMap(g)
	}
}
```

## OOP vs FP Comparison

```go
package main

import "strings"

// OOP - Method chaining (fluent interface)
type StringProcessor struct {
	value string
}

func NewStringProcessor(value string) *StringProcessor {
	return &StringProcessor{value: value}
}

func (sp *StringProcessor) Trim() *StringProcessor {
	return &StringProcessor{value: strings.TrimSpace(sp.value)}
}

func (sp *StringProcessor) ToLower() *StringProcessor {
	return &StringProcessor{value: strings.ToLower(sp.value)}
}

func (sp *StringProcessor) Split(sep string) []string {
	return strings.Split(sp.value, sep)
}

// Usage
func oopExample() {
	result := NewStringProcessor("  Hello World  ").
		Trim().
		ToLower().
		Split(" ")
}

// FP - Function composition
func fpExample() {
	input := "  Hello World  "

	result := input
	result = strings.TrimSpace(result)
	result = strings.ToLower(result)
	parts := strings.Split(result, " ")
}
```

## Recommended Libraries

| Library | Purpose | Link |
|---------|---------|------|
| **fp-ts** | pipe, flow | `npm i fp-ts` |
| **Effect** | Effect composition | `npm i effect` |
| **ramda** | R.pipe, R.compose | `npm i ramda` |
| **lodash/fp** | _.flow | `npm i lodash` |
| **sanctuary** | S.pipe | `npm i sanctuary` |

## Anti-patterns

1. **Long Pipelines**: Hard to debug

   ```go
   // BAD - 20 steps, hard to trace errors
   result := Pipe10(data, f1, f2, f3, f4, f5, f6, f7, f8, f9, f10)

   // GOOD - Break into named stages
   stage1 := Pipe3(data, f1, f2, f3)
   stage2 := Pipe3(stage1, f4, f5, f6)
   ```

2. **Impure Functions in Pipeline**: Side effects break reasoning

   ```go
   // BAD
   impureMap := func(u User) User {
   	fmt.Println(u) // Side effect!
   	return u
   }

   // GOOD - Separate concerns
   users := getUsers()
   for _, u := range users {
   	fmt.Println(u) // Explicit side effect
   }
   filtered := filter(isActive)(users)
   ```

3. **Type Inference Failure**: Missing type annotations

   ```go
   // BAD - Type unclear
   process := func(x interface{}) interface{} {
   	return strings.TrimSpace(x.(string))
   }

   // GOOD - Use generics with clear types
   process := func(x string) string {
   	return strings.TrimSpace(x)
   }
   ```

## When to Use

- Data transformation pipelines
- Building complex operations from simple ones
- Avoiding intermediate variables
- Creating reusable function combinations
- Point-free programming style

## Related Patterns

- [Monad](./monad.md) - Monadic composition with flatMap
- [Either](./either.md) - Composing fallible functions
- [Lens](./lens.md) - Composable optics
