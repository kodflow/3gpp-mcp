# Option / Maybe Pattern

> Type representing an optional value - either a value exists (Some) or it does not (None), eliminating null errors at compile time.

## Definition

**Option** (also called Maybe) is a type that represents an optional value: either a value exists (Some/Just) or it doesn't (None/Nothing). It eliminates null/undefined errors at compile time.

```
Option<A> = Some<A> | None
Maybe<A> = Just<A> | Nothing
```

**Key characteristics:**

- **Null-safety**: No null pointer exceptions
- **Explicit optionality**: Optional values in type signature
- **Composable**: Chain operations on optional values
- **Short-circuit**: None propagates through chains
- **Forces handling**: Must deal with absence explicitly

## TypeScript Implementation

```go
package option

// Option represents an optional value
type Option[A any] interface {
	IsSome() bool
	IsNone() bool
	Map(func(A) A) Option[A]
	FlatMap(func(A) Option[A]) Option[A]
	Filter(func(A) bool) Option[A]
	GetOrElse(A) A
	GetOrElseF(func() A) A
	OrElse(func() Option[A]) Option[A]
	ToNullable() *A
}

// Some represents a present value
type Some[A any] struct {
	value A
}

func (s Some[A]) IsSome() bool { return true }
func (s Some[A]) IsNone() bool { return false }

func (s Some[A]) Map(f func(A) A) Option[A] {
	return NewSome(f(s.value))
}

func (s Some[A]) FlatMap(f func(A) Option[A]) Option[A] {
	return f(s.value)
}

func (s Some[A]) Filter(predicate func(A) bool) Option[A] {
	if predicate(s.value) {
		return s
	}
	return NewNone[A]()
}

func (s Some[A]) GetOrElse(_ A) A {
	return s.value
}

func (s Some[A]) GetOrElseF(_ func() A) A {
	return s.value
}

func (s Some[A]) OrElse(_ func() Option[A]) Option[A] {
	return s
}

func (s Some[A]) ToNullable() *A {
	return &s.value
}

// None represents an absent value
type None[A any] struct{}

var noneInstance = None[any]{}

func (n None[A]) IsSome() bool { return false }
func (n None[A]) IsNone() bool { return true }

func (n None[A]) Map(_ func(A) A) Option[A] {
	return NewNone[A]()
}

func (n None[A]) FlatMap(_ func(A) Option[A]) Option[A] {
	return NewNone[A]()
}

func (n None[A]) Filter(_ func(A) bool) Option[A] {
	return NewNone[A]()
}

func (n None[A]) GetOrElse(defaultValue A) A {
	return defaultValue
}

func (n None[A]) GetOrElseF(thunk func() A) A {
	return thunk()
}

func (n None[A]) OrElse(alternative func() Option[A]) Option[A] {
	return alternative()
}

func (n None[A]) ToNullable() *A {
	return nil
}

// NewSome creates an Option with a value
func NewSome[A any](value A) Option[A] {
	return Some[A]{value: value}
}

// NewNone creates an empty Option
func NewNone[A any]() Option[A] {
	return None[A]{}
}

// FromNullable creates an Option from a nullable pointer
func FromNullable[A any](value *A) Option[A] {
	if value == nil {
		return NewNone[A]()
	}
	return NewSome(*value)
}

// FromPredicate creates an Option based on a predicate
func FromPredicate[A any](value A, predicate func(A) bool) Option[A] {
	if predicate(value) {
		return NewSome(value)
	}
	return NewNone[A]()
}

// Combine combines multiple Options into one
func Combine[A any](options []Option[A]) Option[[]A] {
	values := make([]A, 0, len(options))
	
	for _, opt := range options {
		if opt.IsNone() {
			return NewNone[[]A]()
		}
		values = append(values, opt.(Some[A]).value)
	}
	
	return NewSome(values)
}
```

## Usage Examples

```go
package main

import "strings"

type User struct {
	Name    string
	Age     int
	Company *Company
}

type Company struct {
	CEO *User
}

var users = make(map[string]User)

// Basic usage
func findUser(id string) Option[User] {
	if user, exists := users[id]; exists {
		return NewSome(user)
	}
	return NewNone[User]()
}

func getUserName(id string) string {
	return findUser(id).
		Map(func(u User) User { return u }).
		GetOrElse(User{Name: "Anonymous"}).
		Name
}

// Chaining operations
func getCeoEmail(user User) Option[string] {
	return FromNullable(user.Company).
		FlatMap(func(c Company) Option[*User] {
			return FromNullable(c.CEO)
		}).
		FlatMap(func(ceo *User) Option[string] {
			if ceo.Email != "" {
				return NewSome(ceo.Email)
			}
			return NewNone[string]()
		})
}

// vs null checks
func getCeoEmailUnsafe(user User) *string {
	if user.Company == nil {
		return nil
	}
	if user.Company.CEO == nil {
		return nil
	}
	if user.Company.CEO.Email == "" {
		return nil
	}
	return &user.Company.CEO.Email
}

// Array operations with Option
func findEven(arr []int) Option[int] {
	for _, n := range arr {
		if n%2 == 0 {
			return NewSome(n)
		}
	}
	return NewNone[int]()
}

func findOdd(arr []int) Option[int] {
	for _, n := range arr {
		if n%2 != 0 {
			return NewSome(n)
		}
	}
	return NewNone[int]()
}

// First matching value
func firstEvenOrOdd(numbers []int) Option[int] {
	return findEven(numbers).OrElse(func() Option[int] {
		return findOdd(numbers)
	})
}

// Filter example
func getAdultUser(id string) Option[User] {
	return findUser(id).Filter(func(u User) bool {
		return u.Age >= 18
	})
}

// Conditional transformation
type Money struct {
	amount float64
}

func applyDiscount(userID string, amount float64) Option[Money] {
	return findUser(userID).
		Filter(func(u User) bool { return u.IsPremium }).
		Map(func(u User) User {
			// Calculate discount
			return u
		})
}
```

## Using fp-ts

```go
package main

import "fmt"

// Basic operations
func findUserFP(id string) Option[User] {
	if user, exists := users[id]; exists {
		return NewSome(user)
	}
	return NewNone[User]()
}

func getUserEmail(id string) string {
	return findUserFP(id).
		Map(func(u User) User { return u }).
		GetOrElse(User{Email: "no-email@example.com"}).
		Email
}

// Chain multiple Options
func getCompanyCeoEmail(user User) Option[string] {
	return FromNullable(user.Company).
		FlatMap(func(c *Company) Option[*User] {
			return FromNullable(c.CEO)
		}).
		FlatMap(func(ceo *User) Option[string] {
			if ceo.Email != "" {
				return NewSome(ceo.Email)
			}
			return NewNone[string]()
		})
}

// Working with arrays
func filterMapUsers(users []User) []string {
	emails := []string{}
	
	for _, u := range users {
		if u.IsPremium {
			if u.Email != "" {
				emails = append(emails, u.Email)
			}
		}
	}
	
	return emails
}

// Applicative - combine Options
func createOrder(userID, productID string) Option[Order] {
	userOpt := findUser(userID)
	productOpt := findProduct(productID)
	
	if userOpt.IsNone() || productOpt.IsNone() {
		return NewNone[Order]()
	}
	
	user := userOpt.(Some[User]).value
	product := productOpt.(Some[Product]).value
	
	return NewSome(NewOrder(user, product))
}

// Alternative patterns
func getConfigValue(key string) Option[string] {
	// Try environment variable
	if val, exists := os.LookupEnv(key); exists {
		return NewSome(val)
	}
	
	// Try config file
	if val, exists := configFile[key]; exists {
		return NewSome(val)
	}
	
	// Try defaults
	if val, exists := defaults[key]; exists {
		return NewSome(val)
	}
	
	return NewNone[string]()
}

// Refinement with type guards
type Admin struct {
	User
	AdminLevel int
}

func isAdmin(user User) bool {
	return user.Role == "admin"
}

func getAdmin(id string) Option[User] {
	return findUser(id).Filter(isAdmin)
}
```

## Using Effect

```go
package main

import (
	"context"
	"fmt"
)

// Basic operations
func findUserEffect(id string) Option[User] {
	return FromNullable(users[id])
}

func getUserNameEffect(id string) string {
	return findUserEffect(id).
		Map(func(u User) User { return u }).
		GetOrElse(User{Name: "Anonymous"}).
		Name
}

// Match pattern
func greetUser(id string) string {
	opt := findUserEffect(id)
	
	if opt.IsSome() {
		user := opt.(Some[User]).value
		return fmt.Sprintf("Hello, %s!", user.Name)
	}
	
	return "Hello, stranger!"
}

// Combining with Effect for errors
type NotFoundError struct {
	Resource string
	ID       string
}

func (e NotFoundError) Error() string {
	return fmt.Sprintf("%s not found: %s", e.Resource, e.ID)
}

type Effect[R, E, A any] struct {
	run func(context.Context, R) (A, error)
}

func getUserOrFail(id string) Effect[any, NotFoundError, User] {
	return Effect[any, NotFoundError, User]{
		run: func(ctx context.Context, r any) (User, error) {
			opt := findUserEffect(id)
			if opt.IsNone() {
				return User{}, NotFoundError{
					Resource: "User",
					ID:       id,
				}
			}
			return opt.(Some[User]).value, nil
		},
	}
}
```

## OOP vs FP Comparison

| Aspect | OOP (null) | FP (Option) |
|--------|-----------|-------------|
| Type safety | Runtime errors | Compile-time safety |
| Documentation | Comments, conventions | Type signature |
| Composition | Null checks | flatMap/chain |
| Default values | ?? operator | getOrElse |
| Conditional | if (x !== null) | map/filter |

```go
package main

// OOP style - null checks
func getOrderTotal(userID string) *float64 {
	user := findUserUnsafe(userID)
	if user == nil {
		return nil
	}
	
	order := user.CurrentOrder
	if order == nil {
		return nil
	}
	
	return &order.Total
}

// FP style - Option
func getOrderTotalFP(userID string) Option[float64] {
	return findUser(userID).
		FlatMap(func(u User) Option[*Order] {
			return FromNullable(u.CurrentOrder)
		}).
		Map(func(o *Order) *Order { return o })
}
```

## Option vs Either

| Use Case | Option | Either |
|----------|--------|--------|
| Value might not exist | Yes | Use when error info needed |
| Need error details | No | Yes |
| Null replacement | Yes | Overkill |
| Validation | No | Yes |
| API errors | No | Yes |

```go
package main

// Option - just absence
func findUserOpt(id string) Option[User] {
	if user, exists := users[id]; exists {
		return NewSome(user)
	}
	return NewNone[User]()
}

// Either - when you need to know why
type Either[E, A any] interface {
	IsLeft() bool
	IsRight() bool
}

func findUserWithError(id string) Either[NotFoundError, User] {
	if user, exists := users[id]; exists {
		return NewRight[NotFoundError, User](user)
	}
	return NewLeft[NotFoundError, User](NotFoundError{
		Resource: "User",
		ID:       id,
	})
}
```

## Recommended Libraries

| Library | Purpose | Link |
|---------|---------|------|
| **fp-ts** | Full Option type | `npm i fp-ts` |
| **Effect** | Modern Option | `npm i effect` |
| **purify-ts** | Simple Maybe | `npm i purify-ts` |
| **true-myth** | Rust-like Option | `npm i true-myth` |

## Anti-patterns

1. **Immediate Unwrapping**: Losing safety

   ```go
   // BAD
   name := findUser(id).GetOrElse(User{}).Name
   if name != "" {
   	// Lost type safety
   }
   
   // GOOD
   findUser(id).Map(func(user User) User {
   	// Safe access to user
   	return user
   })
   ```

2. **Optional Properties Instead**: Missing the point

   ```go
   // BAD - Optional in data model
   type User struct {
   	Email *string // Nullable pointer
   }
   
   // GOOD - Option in operations
   type User struct {
   	Email string
   }
   
   func getUserEmail(id string) Option[string] {
   	return findUser(id).Map(func(u User) User {
   		return u
   	})
   }
   ```

3. **Nested Options**: Over-wrapping

   ```go
   // BAD
   type NestedOption Option[Option[User]]
   
   // GOOD - Use FlatMap
   func getRelatedUser(id string) Option[User] {
   	return findUser(id).FlatMap(findRelatedUser)
   }
   
   func findRelatedUser(u User) Option[User] {
   	// Implementation
   	return NewNone[User]()
   }
   ```

## When to Use

- Replacing null/undefined
- Optional function parameters
- Dictionary/Map lookups
- Array search operations
- Chaining optional operations

## Related Patterns

- [Monad](./monad.md) - Option is a monad
- [Either](./either.md) - When error information needed
- [Lens](./lens.md) - Optional property access
