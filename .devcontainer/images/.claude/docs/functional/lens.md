# Lens Pattern

> Composable getter/setter pair for manipulating nested data structures in a functional and immutable way.

## Definition

A **Lens** is a composable getter/setter pair that provides a functional way to focus on and manipulate nested data structures immutably. It solves the problem of updating deeply nested immutable data.

```
Lens<S, A> = {
  get: (s: S) => A,
  set: (a: A) => (s: S) => S
}
```

**Key characteristics:**

- **Composable**: Lenses can be combined to focus deeper
- **Immutable updates**: Returns new structures, doesn't mutate
- **Bidirectional**: Can both read and write
- **Type-safe**: Full type inference for nested access
- **Reusable**: Same lens for get and set operations

## Lens Laws

1. **Get-Put**: `set(get(s))(s) === s` (setting what you get changes nothing)
2. **Put-Get**: `get(set(a)(s)) === a` (you get what you set)
3. **Put-Put**: `set(a2)(set(a1)(s)) === set(a2)(s)` (setting twice = setting once)

## TypeScript Implementation

```go
package lens

// Lens represents a composable getter/setter pair
type Lens[S, A any] struct {
	Get func(S) A
	Set func(A) func(S) S
}

// NewLens creates a new Lens
func NewLens[S, A any](
	get func(S) A,
	set func(A) func(S) S,
) Lens[S, A] {
	return Lens[S, A]{
		Get: get,
		Set: set,
	}
}

// Modify applies a function through the lens
func Modify[S, A any](lens Lens[S, A], f func(A) A) func(S) S {
	return func(s S) S {
		return lens.Set(f(lens.Get(s)))(s)
	}
}

// Compose combines two lenses
func Compose[A, B, C any](
	outer Lens[A, B],
	inner Lens[B, C],
) Lens[A, C] {
	return NewLens(
		func(a A) C {
			return inner.Get(outer.Get(a))
		},
		func(c C) func(A) A {
			return func(a A) A {
				b := outer.Get(a)
				newB := inner.Set(c)(b)
				return outer.Set(newB)(a)
			}
		},
	)
}

// Prop creates a lens for a struct field using getter/setter functions
func Prop[S, A any](
	get func(*S) A,
	set func(*S, A),
) Lens[*S, A] {
	return NewLens(
		func(s *S) A {
			return get(s)
		},
		func(a A) func(*S) *S {
			return func(s *S) *S {
				// Create a copy
				newS := new(S)
				*newS = *s
				set(newS, a)
				return newS
			}
		},
	)
}

// Index creates a lens for slice element
func Index[A any](i int) Lens[[]A, A] {
	return NewLens(
		func(arr []A) A {
			if i < 0 || i >= len(arr) {
				var zero A
				return zero
			}
			return arr[i]
		},
		func(a A) func([]A) []A {
			return func(arr []A) []A {
				if i < 0 || i >= len(arr) {
					return arr
				}
				newArr := make([]A, len(arr))
				copy(newArr, arr)
				newArr[i] = a
				return newArr
			}
		},
	)
}
```

## Usage Examples

```go
package main

// Domain types
type Address struct {
	Street  string
	City    string
	Country string
}

type Company struct {
	Name    string
	Address Address
}

type User struct {
	ID      string
	Name    string
	Company Company
}

// Create lenses using Prop helper
func userCompanyLens() Lens[*User, Company] {
	return Prop(
		func(u *User) Company { return u.Company },
		func(u *User, c Company) { u.Company = c },
	)
}

func companyAddressLens() Lens[*Company, Address] {
	return Prop(
		func(c *Company) Address { return c.Address },
		func(c *Company, a Address) { c.Address = a },
	)
}

func addressCityLens() Lens[*Address, string] {
	return Prop(
		func(a *Address) string { return a.City },
		func(a *Address, city string) { a.City = city },
	)
}

// Note: For Go, composition of pointer lenses requires wrapper functions
// This is a simplified example - production code would need careful handling

func example() {
	user := &User{
		ID:   "1",
		Name: "Alice",
		Company: Company{
			Name: "Acme",
			Address: Address{
				Street:  "123 Main",
				City:    "Boston",
				Country: "USA",
			},
		},
	}

	// For simple cases in Go, direct field access is more idiomatic
	// But lenses shine when you need reusable, composable accessors

	// Get nested value (direct access)
	city := user.Company.Address.City // "Boston"

	// Set nested value immutably (requires copy)
	updatedUser := *user
	updatedUser.Company.Address.City = "New York"

	// Modify nested value
	modifiedUser := *user
	modifiedUser.Company.Address.City = "BOSTON"
}
```

## Using monocle-ts

```go
package main

import "strings"

// Lens operations in Go style
type UserLens struct{}

func (UserLens) Company() Lens[User, Company] {
	return NewLens(
		func(u User) Company { return u.Company },
		func(c Company) func(User) User {
			return func(u User) User {
				u.Company = c
				return u
			}
		},
	)
}

type CompanyLens struct{}

func (CompanyLens) Address() Lens[Company, Address] {
	return NewLens(
		func(c Company) Address { return c.Address },
		func(a Address) func(Company) Company {
			return func(c Company) Company {
				c.Address = a
				return c
			}
		},
	)
}

type AddressLens struct{}

func (AddressLens) City() Lens[Address, string] {
	return NewLens(
		func(a Address) string { return a.City },
		func(city string) func(Address) Address {
			return func(a Address) Address {
				a.City = city
				return a
			}
		},
	)
}

// Compose lenses
func userCityLens() Lens[User, string] {
	userCompany := UserLens{}.Company()
	companyAddr := CompanyLens{}.Address()
	addrCity := AddressLens{}.City()

	return Compose(Compose(userCompany, companyAddr), addrCity)
}

// Operations
func lensOperations() {
	user := User{
		ID:   "1",
		Name: "Alice",
		Company: Company{
			Name: "Acme",
			Address: Address{
				Street:  "123 Main",
				City:    "Boston",
				Country: "USA",
			},
		},
	}

	cityLens := userCityLens()

	getCity := cityLens.Get(user)                              // "Boston"
	setCity := cityLens.Set("Chicago")(user)                   // New user with Chicago
	modifyCity := Modify(cityLens, strings.ToUpper)(user)      // New user with BOSTON
}

// Optional - for potentially missing values (using pointers)
type Profile struct {
	Nickname *string
}

type Account struct {
	Profile *Profile
}

// Optional lens that handles nil values
type Optional[S, A any] struct {
	GetOption func(S) *A
	Set       func(A) func(S) S
}

func accountProfileOptional() Optional[Account, Profile] {
	return Optional[Account, Profile]{
		GetOption: func(a Account) *Profile {
			return a.Profile
		},
		Set: func(p Profile) func(Account) Account {
			return func(a Account) Account {
				a.Profile = &p
				return a
			}
		},
	}
}

func profileNicknameOptional() Optional[Profile, string] {
	return Optional[Profile, string]{
		GetOption: func(p Profile) *string {
			return p.Nickname
		},
		Set: func(n string) func(Profile) Profile {
			return func(p Profile) Profile {
				p.Nickname = &n
				return p
			}
		},
	}
}

func optionalExample() {
	bob := "bob"
	account := Account{Profile: &Profile{Nickname: &bob}}

	profileOpt := accountProfileOptional()
	if profile := profileOpt.GetOption(account); profile != nil {
		nicknameOpt := profileNicknameOptional()
		if nickname := nicknameOpt.GetOption(*profile); nickname != nil {
			// Use nickname
		}
	}

	emptyAccount := Account{}
	profile := accountProfileOptional().GetOption(emptyAccount) // nil
}

// Prism - for sum types (using type switches)
type Shape interface {
	isShape()
}

type Circle struct {
	Radius float64
}

func (Circle) isShape() {}

type Rectangle struct {
	Width  float64
	Height float64
}

func (Rectangle) isShape() {}

type Prism[S, A any] struct {
	GetOption func(S) *A
	Reverseget func(A) S
}

func circlePrism() Prism[Shape, Circle] {
	return Prism[Shape, Circle]{
		GetOption: func(s Shape) *Circle {
			if c, ok := s.(Circle); ok {
				return &c
			}
			return nil
		},
		Reverseget: func(c Circle) Shape {
			return c
		},
	}
}

func prismExample() {
	shapes := []Shape{
		Circle{Radius: 10},
		Rectangle{Width: 5, Height: 3},
	}

	prism := circlePrism()

	// Get only circles
	circles := []Circle{}
	for _, s := range shapes {
		if c := prism.GetOption(s); c != nil {
			circles = append(circles, *c)
		}
	}
}
```

## Using Effect (Optics)

```go
package main

// Optic-style API in Go
type OpticBuilder[S any] struct {
	value S
}

func ID[S any]() OpticBuilder[S] {
	return OpticBuilder[S]{}
}

// Get field value
func Get[S, A any](lens Lens[S, A]) func(S) A {
	return lens.Get
}

// Replace field value
func Replace[S, A any](lens Lens[S, A]) func(A) func(S) S {
	return lens.Set
}

// Modify field value
func ModifyField[S, A any](lens Lens[S, A]) func(func(A) A) func(S) S {
	return func(f func(A) A) func(S) S {
		return Modify(lens, f)
	}
}

func effectStyleExample() {
	user := User{
		ID:   "1",
		Name: "Alice",
		Company: Company{
			Name: "Acme",
			Address: Address{
				Street:  "123 Main",
				City:    "Boston",
				Country: "USA",
			},
		},
	}

	cityLens := userCityLens()

	// Get value
	cityValue := Get(cityLens)(user)

	// Set value
	updatedUser := Replace(cityLens)("Chicago")(user)

	// Modify value
	modifiedUser := ModifyField(cityLens)(strings.ToUpper)(user)
}

// Optional access with pointers
type Config struct {
	Database *Database
}

type Database struct {
	Host *string
	Port *int
}

func configDatabaseLens() Lens[Config, *Database] {
	return NewLens(
		func(c Config) *Database { return c.Database },
		func(db *Database) func(Config) Config {
			return func(c Config) Config {
				c.Database = db
				return c
			}
		},
	)
}

func optionalAccessExample() {
	localhost := "localhost"
	port := 5432

	config := Config{
		Database: &Database{
			Host: &localhost,
			Port: &port,
		},
	}

	dbLens := configDatabaseLens()
	db := dbLens.Get(config)

	if db != nil && db.Host != nil {
		host := *db.Host // "localhost"
	}
}
```

## Optic Types

| Optic | Get | Set | Use Case |
|-------|-----|-----|----------|
| **Lens** | Always | Always | Product types (objects) |
| **Prism** | Maybe | Always | Sum types (unions) |
| **Optional** | Maybe | Always | Optional fields |
| **Iso** | Always | Always | Isomorphic types |
| **Traversal** | Multiple | Multiple | Collections |

```go
package main

// Traversal - focus on multiple elements
type Traversal[S, A any] struct {
	ModifyAll func(func(A) A) func(S) S
}

type Order struct {
	Items []OrderItem
}

type OrderItem struct {
	Price float64
}

func orderItemsTraversal() Traversal[Order, float64] {
	return Traversal[Order, float64]{
		ModifyAll: func(f func(float64) float64) func(Order) Order {
			return func(o Order) Order {
				newItems := make([]OrderItem, len(o.Items))
				for i, item := range o.Items {
					newItems[i] = OrderItem{Price: f(item.Price)}
				}
				o.Items = newItems
				return o
			}
		},
	}
}

// Modify all prices
func traversalExample() {
	order := Order{
		Items: []OrderItem{
			{Price: 100},
			{Price: 200},
		},
	}

	traversal := orderItemsTraversal()

	// Apply 10% discount
	discountedOrder := traversal.ModifyAll(func(p float64) float64 {
		return p * 0.9
	})(order)
}
```

## Recommended Libraries

| Library | Purpose | Link |
|---------|---------|------|
| **monocle-ts** | Full optics library | `npm i monocle-ts` |
| **Effect** | Built-in optics | `npm i effect` |
| **partial.lenses** | Lightweight lenses | `npm i partial.lenses` |
| **shades** | TypeScript lenses | `npm i shades` |
| **immer** | Alternative (proxies) | `npm i immer` |

## Lens vs Spread Operator

```go
package main

// Without lens - deeply nested update
func updateCity(user User, newCity string) User {
	user.Company.Address.City = newCity
	return user
}

// Note: Go encourages direct field access for simplicity
// Lenses are most valuable when you need:
// - Reusable accessor/mutator logic
// - Composition of nested access
// - Functional programming style

// With lens - clean and reusable
func updateCityWithLens(user User, newCity string) User {
	return userCityLens().Set(newCity)(user)
}

// Lens advantage: reusable for multiple operations
func lensAdvantages(user User) {
	cityLens := userCityLens()

	getCity := cityLens.Get(user)
	setCity := cityLens.Set("NYC")(user)
	upperCity := Modify(cityLens, strings.ToUpper)(user)
}
```

## Anti-patterns

1. **Creating Lenses Inline**: Loses reusability

   ```go
   // BAD - Creating lens inline
   NewLens(
   	func(u User) string { return u.Company.Address.City },
   	func(c string) func(User) User { /* complex copy logic */ },
   )(user)

   // GOOD - Reusable lens
   cityLens := userCityLens()
   city := cityLens.Get(user)
   ```

2. **Over-using for Simple Cases**: Unnecessary complexity

   ```go
   // BAD - Overkill for single property
   nameLens := NewLens(
   	func(u User) string { return u.Name },
   	func(n string) func(User) User {
   		return func(u User) User {
   			u.Name = n
   			return u
   		}
   	},
   )

   // OK for simple cases in Go
   user.Name = "Bob"
   ```

3. **Mutating Through Lens**: Breaking immutability

   ```go
   // BAD
   addr := addressLens.Get(&user)
   addr.City = "NYC" // Mutation!
   ```

## When to Use

- Deeply nested immutable updates
- Reusable accessor/mutator logic
- Complex state management
- Working with immutable data structures
- Redux reducers with nested state

## Related Patterns

- [Composition](./composition.md) - Lenses are composable
- [Option](./option.md) - Optional optics return Option
- [Monad](./monad.md) - Some optics are monadic
