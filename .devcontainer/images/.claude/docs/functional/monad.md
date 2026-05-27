# Monad Pattern

> Design pattern for structuring programs generically while chaining operations with context (optionality, errors, async).

## Definition

A **Monad** is a design pattern that allows structuring programs generically while chaining operations with context. It wraps values in a computational context (like optionality, errors, or async) and provides a way to compose operations on wrapped values.

```
Monad = Type Constructor + unit (of/return) + flatMap (bind/chain)
```

**Monad Laws:**

1. **Left Identity**: `of(a).flatMap(f)` === `f(a)`
2. **Right Identity**: `m.flatMap(of)` === `m`
3. **Associativity**: `m.flatMap(f).flatMap(g)` === `m.flatMap(x => f(x).flatMap(g))`

## Core Monads

| Monad | Context | Purpose |
|-------|---------|---------|
| **Maybe/Option** | Optionality | Handle null/undefined |
| **Either/Result** | Error handling | Success or failure |
| **IO** | Side effects | Defer execution |
| **Task/Future** | Async | Handle promises |
| **Reader** | Dependencies | Dependency injection |
| **State** | Mutable state | Thread state through |
| **List/Array** | Non-determinism | Multiple values |

## TypeScript Implementation

```go
package monad

import "context"

// Monad interface (simplified - Go doesn't support higher-kinded types)
type Monad[A any] interface {
	FlatMap(func(A) Monad[A]) Monad[A]
	Map(func(A) A) Monad[A]
}

// Maybe Monad
type Maybe[A any] interface {
	FlatMap(func(A) Maybe[A]) Maybe[A]
	Map(func(A) A) Maybe[A]
	GetOrElse(A) A
	IsSome() bool
	IsNone() bool
}

// Some represents a present value
type Some[A any] struct {
	value A
}

func (s Some[A]) FlatMap(f func(A) Maybe[A]) Maybe[A] {
	return f(s.value)
}

func (s Some[A]) Map(f func(A) A) Maybe[A] {
	return OfMaybe(f(s.value))
}

func (s Some[A]) GetOrElse(_ A) A {
	return s.value
}

func (s Some[A]) IsSome() bool { return true }
func (s Some[A]) IsNone() bool { return false }

// None represents an absent value
type None[A any] struct{}

var noneInstance = None[any]{}

func (n None[A]) FlatMap(_ func(A) Maybe[A]) Maybe[A] {
	return NoneMaybe[A]()
}

func (n None[A]) Map(_ func(A) A) Maybe[A] {
	return NoneMaybe[A]()
}

func (n None[A]) GetOrElse(defaultValue A) A {
	return defaultValue
}

func (n None[A]) IsSome() bool { return false }
func (n None[A]) IsNone() bool { return true }

// OfMaybe creates a Maybe from a value
func OfMaybe[A any](value A) Maybe[A] {
	return Some[A]{value: value}
}

// NoneMaybe creates an empty Maybe
func NoneMaybe[A any]() Maybe[A] {
	return None[A]{}
}

// Either Monad
type Either[E, A any] interface {
	FlatMap(func(A) Either[E, A]) Either[E, A]
	Map(func(A) A) Either[E, A]
	MapLeft(func(E) E) Either[E, A]
	IsRight() bool
	IsLeft() bool
}

// Right represents a success
type Right[E, A any] struct {
	value A
}

func (r Right[E, A]) FlatMap(f func(A) Either[E, A]) Either[E, A] {
	return f(r.value)
}

func (r Right[E, A]) Map(f func(A) A) Either[E, A] {
	return NewRight[E, A](f(r.value))
}

func (r Right[E, A]) MapLeft(_ func(E) E) Either[E, A] {
	return r
}

func (r Right[E, A]) IsRight() bool { return true }
func (r Right[E, A]) IsLeft() bool  { return false }

// Left represents a failure
type Left[E, A any] struct {
	error E
}

func (l Left[E, A]) FlatMap(_ func(A) Either[E, A]) Either[E, A] {
	return l
}

func (l Left[E, A]) Map(_ func(A) A) Either[E, A] {
	return l
}

func (l Left[E, A]) MapLeft(f func(E) E) Either[E, A] {
	return NewLeft[E, A](f(l.error))
}

func (l Left[E, A]) IsRight() bool { return false }
func (l Left[E, A]) IsLeft() bool  { return true }

// NewRight creates a Right
func NewRight[E, A any](value A) Either[E, A] {
	return Right[E, A]{value: value}
}

// NewLeft creates a Left
func NewLeft[E, A any](err E) Either[E, A] {
	return Left[E, A]{error: err}
}

// IO Monad - Deferred side effects
type IO[A any] struct {
	effect func() A
}

// OfIO creates an IO from a value
func OfIO[A any](value A) IO[A] {
	return IO[A]{
		effect: func() A { return value },
	}
}

// FromIO creates an IO from an effect
func FromIO[A any](effect func() A) IO[A] {
	return IO[A]{effect: effect}
}

// FlatMap chains IO operations
func (io IO[A]) FlatMap(f func(A) IO[A]) IO[A] {
	return IO[A]{
		effect: func() A {
			return f(io.effect()).Run()
		},
	}
}

// Map transforms the IO result
func (io IO[A]) Map(f func(A) A) IO[A] {
	return IO[A]{
		effect: func() A {
			return f(io.effect())
		},
	}
}

// Run executes the IO
func (io IO[A]) Run() A {
	return io.effect()
}
```

## Usage Examples

```go
package main

// Maybe - handling optional values
type User struct {
	Orders []Order
}

type Order struct {
	Total Money
}

type Money struct {
	amount float64
}

func (m Money) Zero() Money {
	return Money{amount: 0}
}

var users = make(map[string]User)

func findUser(id string) Maybe[User] {
	if user, exists := users[id]; exists {
		return OfMaybe(user)
	}
	return NoneMaybe[User]()
}

func findOrder(user User) Maybe[Order] {
	if len(user.Orders) > 0 {
		return OfMaybe(user.Orders[0])
	}
	return NoneMaybe[Order]()
}

func getOrderTotal(order Order) Maybe[Money] {
	return OfMaybe(order.Total)
}

// Chain operations - short-circuits on None
func getUserOrderTotal() Money {
	return findUser("123").
		FlatMap(func(u User) Maybe[Order] { return findOrder(u) }).
		FlatMap(func(o Order) Maybe[Money] { return getOrderTotal(o) }).
		GetOrElse(Money{}.Zero())
}

// Either - error handling
type ValidationError struct {
	message string
}

func (e ValidationError) Error() string {
	return e.message
}

type Email struct {
	value string
}

func parseEmail(input string) Either[ValidationError, Email] {
	if !strings.Contains(input, "@") {
		return NewLeft[ValidationError, Email](
			ValidationError{message: "Invalid email"},
		)
	}
	return NewRight[ValidationError, Email](Email{value: input})
}

func validateAge(age int) Either[ValidationError, int] {
	if age < 18 {
		return NewLeft[ValidationError, int](
			ValidationError{message: "Must be 18+"},
		)
	}
	return NewRight[ValidationError, int](age)
}

// Compose validations
func registerUser(email string, age int) Either[ValidationError, User] {
	emailResult := parseEmail(email)
	if emailResult.IsLeft() {
		return NewLeft[ValidationError, User](
			emailResult.(Left[ValidationError, Email]).error,
		)
	}

	ageResult := validateAge(age)
	if ageResult.IsLeft() {
		return NewLeft[ValidationError, User](
			ageResult.(Left[ValidationError, int]).error,
		)
	}

	// Create user with validated data
	return NewRight[ValidationError, User](User{})
}

// IO - side effects
func readFile(path string) IO[string] {
	return FromIO(func() string {
		data, _ := os.ReadFile(path)
		return string(data)
	})
}

func writeFile(path, content string) IO[struct{}] {
	return FromIO(func() struct{} {
		os.WriteFile(path, []byte(content), 0644)
		return struct{}{}
	})
}

func ioExample() {
	program := readFile("input.txt").
		Map(strings.ToUpper).
		FlatMap(func(upper string) IO[struct{}] {
			return writeFile("output.txt", upper)
		})

	// Nothing happens until we run
	program.Run()
}
```

## Using fp-ts

```go
package main

import (
	"context"
	"fmt"
)

// Option (Maybe) usage
func findUserOpt(id string) Maybe[User] {
	if user, exists := users[id]; exists {
		return OfMaybe(user)
	}
	return NoneMaybe[User]()
}

func getUserEmail() string {
	return findUserOpt("123").
		Map(func(u User) User { return u }). // Transform if needed
		GetOrElse(User{})                    // Provide default
}

// Either for error handling
func parseNumber(s string) Either[string, int] {
	n, err := strconv.Atoi(s)
	if err != nil {
		return NewLeft[string, int]("Not a number")
	}
	return NewRight[string, int](n)
}

// TaskEither (async + error handling)
type TaskEither[E, A any] struct {
	run func(context.Context) Either[E, A]
}

func fetchUser(id string) TaskEither[error, User] {
	return TaskEither[error, User]{
		run: func(ctx context.Context) Either[error, User] {
			// Simulate HTTP call
			resp, err := http.Get(fmt.Sprintf("/api/users/%s", id))
			if err != nil {
				return NewLeft[error, User](err)
			}
			// Parse response
			var user User
			return NewRight[error, User](user)
		},
	}
}

func taskExample() {
	ctx := context.Background()
	result := fetchUser("123").run(ctx)

	if result.IsRight() {
		user := result.(Right[error, User]).value
		fmt.Println(user)
	} else {
		err := result.(Left[error, User]).error
		fmt.Println("Error:", err)
	}
}
```

## Using Effect

```go
package main

import (
	"context"
	"fmt"
)

// Effect is a powerful monad combining IO, Either, Reader, and more
type Effect[R, E, A any] struct {
	run func(context.Context, R) (A, error)
}

func Succeed[R, E, A any](value A) Effect[R, E, A] {
	return Effect[R, E, A]{
		run: func(ctx context.Context, r R) (A, error) {
			return value, nil
		},
	}
}

func Fail[R, E, A any](err E) Effect[R, E, A] {
	return Effect[R, E, A]{
		run: func(ctx context.Context, r R) (A, error) {
			var zero A
			return zero, fmt.Errorf("%v", err)
		},
	}
}

func (e Effect[R, E, A]) Map(f func(A) A) Effect[R, E, A] {
	return Effect[R, E, A]{
		run: func(ctx context.Context, r R) (A, error) {
			val, err := e.run(ctx, r)
			if err != nil {
				return val, err
			}
			return f(val), nil
		},
	}
}

func (e Effect[R, E, A]) FlatMap(f func(A) Effect[R, E, A]) Effect[R, E, A] {
	return Effect[R, E, A]{
		run: func(ctx context.Context, r R) (A, error) {
			val, err := e.run(ctx, r)
			if err != nil {
				var zero A
				return zero, err
			}
			return f(val).run(ctx, r)
		},
	}
}

func effectExample() {
	program := Succeed[any, error, int](42).
		Map(func(n int) int { return n * 2 }).
		FlatMap(func(n int) Effect[any, error, int] {
			if n > 50 {
				return Fail[any, error, int](fmt.Errorf("too large"))
			}
			return Succeed[any, error, int](n)
		})

	ctx := context.Background()
	result, err := program.run(ctx, nil)
	if err != nil {
		fmt.Println("Error:", err)
	} else {
		fmt.Println("Result:", result)
	}
}

// With dependencies (Reader monad pattern)
type UserService interface {
	GetUser(ctx context.Context, id string) (User, error)
}

type Dependencies struct {
	UserService UserService
}

func getUserName(id string) Effect[Dependencies, error, string] {
	return Effect[Dependencies, error, string]{
		run: func(ctx context.Context, deps Dependencies) (string, error) {
			user, err := deps.UserService.GetUser(ctx, id)
			if err != nil {
				return "", err
			}
			return user.Name, nil
		},
	}
}
```

## Recommended Libraries

| Library | Purpose | Link |
|---------|---------|------|
| **fp-ts** | Comprehensive FP | `npm i fp-ts` |
| **Effect** | Modern FP runtime | `npm i effect` |
| **neverthrow** | Simple Result type | `npm i neverthrow` |
| **purify-ts** | Lightweight FP | `npm i purify-ts` |
| **ts-results** | Rust-like Result | `npm i ts-results` |

## Anti-patterns

1. **Monad Hell**: Too many nested flatMaps

   ```go
   // BAD
   a.FlatMap(func(b B) Maybe[C] {
   	return c.FlatMap(func(d D) Maybe[E] {
   		return e.FlatMap(func(f F) Maybe[G] {
   			// ...
   		})
   	})
   })

   // GOOD - Use sequential composition
   bMaybe := a
   cMaybe := bMaybe.FlatMap(funcB)
   dMaybe := cMaybe.FlatMap(funcC)
   ```

2. **Escaping the Monad**: Unwrapping too early

   ```go
   // BAD - Loses safety
   value := maybe.GetOrElse(nil)
   if value != nil {
   	// ...
   }

   // GOOD - Stay in monad
   maybe.Map(func(value V) V {
   	// Work with value safely
   	return value
   })
   ```

3. **Ignoring Errors**: Not handling Left/None cases

   ```go
   // BAD
   result := either.FlatMap(/* ... */)
   // Never checks if Left

   // GOOD
   if result.IsRight() {
   	handleSuccess(result.(Right[E, A]).value)
   } else {
   	handleError(result.(Left[E, A]).error)
   }
   ```

## When to Use

- Sequential operations that can fail
- Safe null/undefined handling
- Explicit side effect management
- Async operation composition
- Dependency injection (Reader)

## Related Patterns

- [Either](./either.md) - Error handling monad
- [Option](./option.md) - Optional value monad
- [Composition](./composition.md) - Composing monadic functions
