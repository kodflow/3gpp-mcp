# Either / Result Pattern

> Type representing a success value (Right) or an error value (Left), providing type-safe error handling without exceptions.

## Definition

**Either** (also known as Result) is a type that represents one of two possible values: a success value (Right) or an error value (Left). It provides type-safe error handling without exceptions.

```
Either<E, A> = Left<E> | Right<A>
Result<T, E> = Ok<T> | Err<E>
```

**Key characteristics:**

- **Explicit errors**: Errors are part of the type signature
- **Composable**: Chain operations that may fail
- **Short-circuit**: First error stops the chain
- **No exceptions**: Errors as values, not control flow
- **Biased**: Right-biased (map/flatMap operate on Right)

## TypeScript Implementation

```go
package either

// Result represents either a success value T or an error E
type Result[T, E any] interface {
	IsOk() bool
	IsErr() bool
	Unwrap() T
	UnwrapOr(defaultValue T) T
	UnwrapErr() E
	Map(f func(T) T) Result[T, E]
	MapErr(f func(E) E) Result[T, E]
	FlatMap(f func(T) Result[T, E]) Result[T, E]
}

// Ok represents a successful result
type Ok[T, E any] struct {
	value T
}

func (o Ok[T, E]) IsOk() bool                              { return true }
func (o Ok[T, E]) IsErr() bool                             { return false }
func (o Ok[T, E]) Unwrap() T                               { return o.value }
func (o Ok[T, E]) UnwrapOr(_ T) T                          { return o.value }
func (o Ok[T, E]) UnwrapErr() E                            { var zero E; return zero }
func (o Ok[T, E]) Map(f func(T) T) Result[T, E]            { return NewOk[T, E](f(o.value)) }
func (o Ok[T, E]) MapErr(_ func(E) E) Result[T, E]         { return o }
func (o Ok[T, E]) FlatMap(f func(T) Result[T, E]) Result[T, E] { return f(o.value) }

// Err represents a failed result
type Err[T, E any] struct {
	error E
}

func (e Err[T, E]) IsOk() bool                       { return false }
func (e Err[T, E]) IsErr() bool                      { return true }
func (e Err[T, E]) Unwrap() T                        { panic("called Unwrap on Err") }
func (e Err[T, E]) UnwrapOr(defaultValue T) T        { return defaultValue }
func (e Err[T, E]) UnwrapErr() E                     { return e.error }
func (e Err[T, E]) Map(_ func(T) T) Result[T, E]     { return e }
func (e Err[T, E]) MapErr(f func(E) E) Result[T, E]  { return NewErr[T, E](f(e.error)) }
func (e Err[T, E]) FlatMap(_ func(T) Result[T, E]) Result[T, E] { return e }

// NewOk creates a successful Result
func NewOk[T, E any](value T) Result[T, E] {
	return Ok[T, E]{value: value}
}

// NewErr creates a failed Result
func NewErr[T, E any](err E) Result[T, E] {
	return Err[T, E]{error: err}
}

// Combine combines multiple Results into one
func Combine[T any, E any](results []Result[T, E]) Result[[]T, E] {
	values := make([]T, 0, len(results))

	for _, result := range results {
		if result.IsErr() {
			return NewErr[[]T, E](result.UnwrapErr())
		}
		values = append(values, result.Unwrap())
	}

	return NewOk[[]T, E](values)
}
```

## Domain Usage Examples

```go
package main

import "fmt"

// Error types
type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

type NotFoundError struct {
	Resource string
	ID       string
}

func (e NotFoundError) Error() string {
	return fmt.Sprintf("%s not found: %s", e.Resource, e.ID)
}

type AuthorizationError struct {
	Action string
}

func (e AuthorizationError) Error() string {
	return fmt.Sprintf("not authorized: %s", e.Action)
}

// DomainError is a union type
type DomainError interface {
	error
	isDomainError()
}

func (ValidationError) isDomainError()    {}
func (NotFoundError) isDomainError()      {}
func (AuthorizationError) isDomainError() {}

// Value objects
type Email struct{ value string }
type Password struct{ value string }
type Age struct{ value int }
type User struct {
	email    Email
	password Password
	age      Age
}

// Validation functions
func validateEmail(email string) Result[Email, ValidationError] {
	if !strings.Contains(email, "@") {
		return NewErr[Email, ValidationError](ValidationError{
			Field:   "email",
			Message: "Invalid email format",
		})
	}
	return NewOk[Email, ValidationError](Email{value: email})
}

func validatePassword(password string) Result[Password, ValidationError] {
	if len(password) < 8 {
		return NewErr[Password, ValidationError](ValidationError{
			Field:   "password",
			Message: "Password too short",
		})
	}
	if !regexp.MustCompile(`[A-Z]`).MatchString(password) {
		return NewErr[Password, ValidationError](ValidationError{
			Field:   "password",
			Message: "Must contain uppercase",
		})
	}
	return NewOk[Password, ValidationError](Password{value: password})
}

func validateAge(age int) Result[Age, ValidationError] {
	if age < 0 || age > 150 {
		return NewErr[Age, ValidationError](ValidationError{
			Field:   "age",
			Message: "Invalid age",
		})
	}
	return NewOk[Age, ValidationError](Age{value: age})
}

// Compose validations
func createUser(email, password string, age int) Result[User, ValidationError] {
	emailResult := validateEmail(email)
	if emailResult.IsErr() {
		return NewErr[User, ValidationError](emailResult.UnwrapErr())
	}

	passwordResult := validatePassword(password)
	if passwordResult.IsErr() {
		return NewErr[User, ValidationError](passwordResult.UnwrapErr())
	}

	ageResult := validateAge(age)
	if ageResult.IsErr() {
		return NewErr[User, ValidationError](ageResult.UnwrapErr())
	}

	return NewOk[User, ValidationError](User{
		email:    emailResult.Unwrap(),
		password: passwordResult.Unwrap(),
		age:      ageResult.Unwrap(),
	})
}

// Alternative: Collect all errors
func createUserValidated(email, password string, age int) Result[User, []ValidationError] {
	emailResult := validateEmail(email)
	passwordResult := validatePassword(password)
	ageResult := validateAge(age)

	errors := []ValidationError{}

	if emailResult.IsErr() {
		errors = append(errors, emailResult.UnwrapErr())
	}
	if passwordResult.IsErr() {
		errors = append(errors, passwordResult.UnwrapErr())
	}
	if ageResult.IsErr() {
		errors = append(errors, ageResult.UnwrapErr())
	}

	if len(errors) > 0 {
		return NewErr[User, []ValidationError](errors)
	}

	return NewOk[User, []ValidationError](User{
		email:    emailResult.Unwrap(),
		password: passwordResult.Unwrap(),
		age:      ageResult.Unwrap(),
	})
}

// Service layer usage
type UserID struct{ value string }

type UserRepository interface {
	FindByID(id UserID) (*User, error)
	Save(user *User) error
}

type UserService struct {
	repository UserRepository
}

func (s *UserService) FindByID(id UserID) Result[*User, NotFoundError] {
	user, err := s.repository.FindByID(id)
	if err != nil {
		return NewErr[*User, NotFoundError](NotFoundError{
			Resource: "User",
			ID:       id.value,
		})
	}
	return NewOk[*User, NotFoundError](user)
}

func (s *UserService) UpdateEmail(
	userID UserID,
	newEmail string,
) Result[*User, DomainError] {
	// Chain multiple operations
	userResult := s.FindByID(userID)
	if userResult.IsErr() {
		return NewErr[*User, DomainError](userResult.UnwrapErr())
	}

	user := userResult.Unwrap()

	if !user.canUpdateEmail {
		return NewErr[*User, DomainError](AuthorizationError{
			Action: "update email",
		})
	}

	emailResult := validateEmail(newEmail)
	if emailResult.IsErr() {
		return NewErr[*User, DomainError](emailResult.UnwrapErr())
	}

	user.email = emailResult.Unwrap()
	return NewOk[*User, DomainError](user)
}
```

## Using fp-ts

```go
package main

import (
	"fmt"
	"strconv"
)

// Basic Either
func parseNumber(s string) Result[int, string] {
	n, err := strconv.Atoi(s)
	if err != nil {
		return NewErr[int, string]("Not a number")
	}
	return NewOk[int, string](n)
}

func divide(a, b int) Result[int, string] {
	if b == 0 {
		return NewErr[int, string]("Division by zero")
	}
	return NewOk[int, string](a / b)
}

// Chaining
func calculate(a, b string) Result[int, string] {
	numAResult := parseNumber(a)
	if numAResult.IsErr() {
		return NewErr[int, string](numAResult.UnwrapErr())
	}

	numBResult := parseNumber(b)
	if numBResult.IsErr() {
		return NewErr[int, string](numBResult.UnwrapErr())
	}

	return divide(numAResult.Unwrap(), numBResult.Unwrap())
}

// Parallel validation (collect all errors)
func validateUserParallel(email, password string) Result[User, []ValidationError] {
	return createUserValidated(email, password, 0)
}

// TaskEither for async operations (simplified)
type TaskResult[T, E any] func() Result[T, E]

func fetchUser(id string) TaskResult[User, error] {
	return func() Result[User, error] {
		// Simulate HTTP call
		// resp, err := http.Get(fmt.Sprintf("/api/users/%s", id))
		// if err != nil { return NewErr[User, error](err) }
		return NewOk[User, error](User{})
	}
}

func updateUserHTTP(user User) TaskResult[User, error] {
	return func() Result[User, error] {
		// Simulate HTTP PUT
		return NewOk[User, error](user)
	}
}

// Chain async operations
func fetchAndUpdate(id, email string) TaskResult[User, error] {
	return func() Result[User, error] {
		userResult := fetchUser(id)()
		if userResult.IsErr() {
			return NewErr[User, error](userResult.UnwrapErr())
		}

		user := userResult.Unwrap()
		user.email = Email{value: email}

		return updateUserHTTP(user)()
	}
}
```

## Using Effect

```go
package main

import (
	"context"
	"fmt"
	"strconv"
)

// Define error types
type ParseError struct {
	input string
}

func (e ParseError) Error() string {
	return fmt.Sprintf("parse error: %s", e.input)
}

type DivisionError struct{}

func (e DivisionError) Error() string {
	return "division by zero"
}

// Effect type that can fail
type Effect[T any] struct {
	run func(context.Context) (T, error)
}

// Functions return Effect with typed errors
func parseNumberEffect(s string) Effect[int] {
	return Effect[int]{
		run: func(ctx context.Context) (int, error) {
			n, err := strconv.Atoi(s)
			if err != nil {
				return 0, ParseError{input: s}
			}
			return n, nil
		},
	}
}

func divideEffect(a, b int) Effect[int] {
	return Effect[int]{
		run: func(ctx context.Context) (int, error) {
			if b == 0 {
				return 0, DivisionError{}
			}
			return a / b, nil
		},
	}
}

// Compose with explicit error handling
func calculateEffect(a, b string) Effect[int] {
	return Effect[int]{
		run: func(ctx context.Context) (int, error) {
			numA, err := parseNumberEffect(a).run(ctx)
			if err != nil {
				return 0, err
			}

			numB, err := parseNumberEffect(b).run(ctx)
			if err != nil {
				return 0, err
			}

			return divideEffect(numA, numB).run(ctx)
		},
	}
}

// Handle errors by type
func handleCalculation(a, b string) string {
	ctx := context.Background()
	result, err := calculateEffect(a, b).run(ctx)

	if err != nil {
		switch e := err.(type) {
		case ParseError:
			return fmt.Sprintf("Invalid input: %s", e.input)
		case DivisionError:
			return "Cannot divide by zero"
		default:
			return "Unknown error"
		}
	}

	return fmt.Sprintf("Result: %d", result)
}
```

## OOP vs FP Comparison

| Aspect | OOP (Exceptions) | FP (Either/Result) |
|--------|-----------------|-------------------|
| Error visibility | Hidden | In type signature |
| Composition | try-catch nesting | flatMap chaining |
| Control flow | throw/catch | Pattern matching |
| Performance | Stack unwinding | No overhead |
| Testing | Mock exceptions | Simple assertions |

```go
package main

// OOP style
func processOrderOOP(order Order) (Order, error) {
	if !order.isValid() {
		return Order{}, ValidationError{Field: "order", Message: "Invalid order"}
	}
	if !inventory.hasStock(order) {
		return Order{}, StockError{Message: "Out of stock"}
	}
	return order.process(), nil
}

// FP style
func processOrderFP(order Order) Result[Order, OrderError] {
	if !order.isValid() {
		return NewErr[Order, OrderError](ValidationError{
			Field:   "order",
			Message: "Invalid order",
		})
	}

	if !inventory.hasStock(order) {
		return NewErr[Order, OrderError](StockError{Message: "Out of stock"})
	}

	return NewOk[Order, OrderError](order.process())
}
```

## Recommended Libraries

| Library | Purpose | Link |
|---------|---------|------|
| **fp-ts** | Full Either type | `npm i fp-ts` |
| **Effect** | Modern Result | `npm i effect` |
| **neverthrow** | Simple Result | `npm i neverthrow` |
| **ts-results** | Rust-like Result | `npm i ts-results` |
| **oxide.ts** | Rust-inspired | `npm i oxide.ts` |

## Anti-patterns

1. **Unwrapping Too Early**: Losing type safety

   ```go
   // BAD
   user := result.Unwrap() // Panics on Err!

   // GOOD
   if result.IsOk() {
   	user := result.Unwrap()
   	handleUser(user)
   } else {
   	handleError(result.UnwrapErr())
   }
   ```

2. **Mixing with Exceptions**: Inconsistent error handling

   ```go
   // BAD
   result := validate(data)
   if result.IsOk() {
   	panic("Something else") // Exception!
   }
   ```

3. **Ignoring Error Types**: Generic error handling

   ```go
   // BAD
   Result[User, error] // Too broad

   // GOOD
   Result[User, DomainError] // Specific error types
   ```

## When to Use

- Functions that can fail predictably
- Validation logic
- API responses
- Domain operations with business errors
- Anywhere exceptions would be caught

## Related Patterns

- [Monad](./monad.md) - Either is a monad
- [Option](./option.md) - For optional values (no error info)
- [Composition](./composition.md) - Composing Result-returning functions
