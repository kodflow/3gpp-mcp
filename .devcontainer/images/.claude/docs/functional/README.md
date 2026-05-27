# Functional Programming Patterns

Functional programming patterns.

## Core Concepts

### 1. Pure Function

> Function without side effects, same input = same output.

```go
// Pure - no side effects, deterministic
func Add(a, b int) int {
    return a + b
}

func CalculateDiscount(price, rate float64) float64 {
    return price * (1 - rate)
}

// Impure - side effects
var total int

func AddToTotal(n int) {
    total += n // Mutation!
}

func GetRandomDiscount(price float64) float64 {
    return price * rand.Float64() // Non-deterministic!
}

func LogPrice(price float64) float64 {
    fmt.Println(price) // Side effect!
    return price
}

// Making impure code pure with dependency injection
type Logger func(msg string)

func CalculateWithLogging(price float64, logger Logger) float64 {
    result := price * 0.9
    logger(fmt.Sprintf("Calculated: %f", result)) // Effect handled by caller
    return result
}
```

**Advantages:** Testable, predictable, parallelizable.
**When:** Always prefer pure functions.

---

### 2. Immutability

> Never modify, always create a new version.

```go
// Mutable - BAD
type User struct {
    Name string
    Age  int
}

user := User{Name: "John", Age: 30}
user.Age = 31 // Mutation!

// Immutable - GOOD
user1 := User{Name: "John", Age: 30}
user2 := User{Name: user1.Name, Age: 31} // New struct

// Immutable slice operations
numbers := []int{1, 2, 3}

// BAD
numbers = append(numbers, 4) // May mutate if capacity allows

// GOOD
newNumbers := append([]int{}, numbers...) // Copy
newNumbers = append(newNumbers, 4)        // Then append

// Deep immutability with helper
type Address struct {
    Street string
    City   string
}

type UserWithAddress struct {
    Name    string
    Address Address
}

func UpdateCity(user UserWithAddress, city string) UserWithAddress {
    return UserWithAddress{
        Name: user.Name,
        Address: Address{
            Street: user.Address.Street,
            City:   city,
        },
    }
}

// Using value receivers ensures immutability
func (u User) WithAge(age int) User {
    return User{Name: u.Name, Age: age}
}
```

**Advantages:** No surprises, time-travel, concurrency safe.
**When:** Shared state, critical data, concurrency.

---

### 3. Higher-Order Functions

> Functions that take/return functions.

```go
// Function that returns a function
func Multiply(factor int) func(int) int {
    return func(n int) int {
        return n * factor
    }
}

double := Multiply(2)
triple := Multiply(3)
fmt.Println(double(5)) // 10
fmt.Println(triple(5)) // 15

// Generic function that takes a function
func Map[T, U any](arr []T, fn func(T) U) []U {
    result := make([]U, len(arr))
    for i, item := range arr {
        result[i] = fn(item)
    }
    return result
}

func Filter[T any](arr []T, predicate func(T) bool) []T {
    result := make([]T, 0, len(arr))
    for _, item := range arr {
        if predicate(item) {
            result = append(result, item)
        }
    }
    return result
}

func Reduce[T, U any](arr []T, fn func(U, T) U, initial U) U {
    result := initial
    for _, item := range arr {
        result = fn(result, item)
    }
    return result
}

// Composition
type User struct {
    Name   string
    Active bool
}

func ProcessUsers(users []User) string {
    return Pipe(
        Filter(users, func(u User) bool { return u.Active }),
        func(users []User) []string {
            return Map(users, func(u User) string { return u.Name })
        },
        func(names []string) string {
            return Reduce(names, func(acc, name string) string {
                if acc == "" {
                    return name
                }
                return acc + ", " + name
            }, "")
        },
    )
}
```

**When:** Behavior abstraction, callbacks, composition.
**Related:** Currying, Composition.

---

### 4. Currying

> Transform f(a, b, c) into f(a)(b)(c).

```go
// Regular function
func Add(a, b, c int) int {
    return a + b + c
}

// Curried version
func CurriedAdd(a int) func(int) func(int) int {
    return func(b int) func(int) int {
        return func(c int) int {
            return a + b + c
        }
    }
}

// Usage - partial application
add5 := CurriedAdd(5)
add5and10 := add5(10)
fmt.Println(add5and10(3)) // 18

// Practical example with HTTP client
type HTTPMethod string

const (
    POST HTTPMethod = "POST"
    GET  HTTPMethod = "GET"
)

func CurriedFetch(method HTTPMethod) func(string) func(any) (*http.Response, error) {
    return func(url string) func(any) (*http.Response, error) {
        return func(body any) (*http.Response, error) {
            data, _ := json.Marshal(body)
            req, _ := http.NewRequest(string(method), url, bytes.NewBuffer(data))
            return http.DefaultClient.Do(req)
        }
    }
}

// Usage
postTo := CurriedFetch(POST)
postToUsers := postTo("https://api.example.com/users")
resp, _ := postToUsers(map[string]string{"name": "John"})

// Functional options pattern (idiomatic Go currying)
type ServerOption func(*Server)

func WithTimeout(d time.Duration) ServerOption {
    return func(s *Server) {
        s.timeout = d
    }
}

func WithLogger(l *slog.Logger) ServerOption {
    return func(s *Server) {
        s.logger = l
    }
}
```

**When:** Partial configuration, composition, functional options.
**Related:** Partial Application.

---

### 5. Function Composition

> Combine simple functions into complex ones.

```go
// Basic composition
func Compose[A, B, C any](f func(B) C, g func(A) B) func(A) C {
    return func(a A) C {
        return f(g(a))
    }
}

// Pipe (left to right) - variadic version
func Pipe[T any](initial T, fns ...func(T) T) T {
    result := initial
    for _, fn := range fns {
        result = fn(result)
    }
    return result
}

// Type-safe pipe builder
type PipeBuilder[T any] struct {
    fns []func(T) T
}

func NewPipe[T any]() *PipeBuilder[T] {
    return &PipeBuilder[T]{fns: make([]func(T) T, 0)}
}

func (p *PipeBuilder[T]) Then(fn func(T) T) *PipeBuilder[T] {
    p.fns = append(p.fns, fn)
    return p
}

func (p *PipeBuilder[T]) Execute(input T) T {
    return Pipe(input, p.fns...)
}

// Usage
type Order struct {
    Total    float64
    Tax      float64
    Discount float64
    Display  string
}

validateOrder := func(o Order) Order { /* ... */ return o }
calculateTax := func(o Order) Order { o.Tax = o.Total * 0.2; return o }
applyDiscount := func(o Order) Order { o.Discount = o.Total * 0.1; return o }
formatForDisplay := func(o Order) Order {
    o.Display = fmt.Sprintf("$%.2f", o.Total+o.Tax-o.Discount)
    return o
}

processOrder := NewPipe[Order]().
    Then(validateOrder).
    Then(calculateTax).
    Then(applyDiscount).
    Then(formatForDisplay)

result := processOrder.Execute(Order{Total: 100})

// Async composition with context
func PipeAsync[T any](ctx context.Context, initial T, fns ...func(context.Context, T) (T, error)) (T, error) {
    result := initial
    for _, fn := range fns {
        var err error
        result, err = fn(ctx, result)
        if err != nil {
            return result, fmt.Errorf("pipe step failed: %w", err)
        }
    }
    return result, nil
}

// Usage
fetchUser := func(ctx context.Context, id string) (User, error) { /* ... */ }
validatePermissions := func(ctx context.Context, u User) (User, error) { /* ... */ }
loadUserData := func(ctx context.Context, u User) (User, error) { /* ... */ }
```

**When:** Data pipelines, transformations, middleware.
**Related:** Higher-Order Functions, Currying.

---

## Algebraic Data Types

### 6. Option/Maybe

> Represent the absence of value in a safe way.

```go
// Option represents an optional value
type Option[T any] interface {
    IsSome() bool
    IsNone() bool
    Unwrap() T
    UnwrapOr(defaultValue T) T
    Map(fn func(T) T) Option[T]
    FlatMap(fn func(T) Option[T]) Option[T]
    Filter(predicate func(T) bool) Option[T]
}

// Some contains a value
type Some[T any] struct {
    value T
}

func (s Some[T]) IsSome() bool                             { return true }
func (s Some[T]) IsNone() bool                             { return false }
func (s Some[T]) Unwrap() T                                { return s.value }
func (s Some[T]) UnwrapOr(_ T) T                           { return s.value }
func (s Some[T]) Map(fn func(T) T) Option[T]               { return NewSome(fn(s.value)) }
func (s Some[T]) FlatMap(fn func(T) Option[T]) Option[T]   { return fn(s.value) }
func (s Some[T]) Filter(predicate func(T) bool) Option[T] {
    if predicate(s.value) {
        return s
    }
    return None[T]()
}

// None represents absence of value
type none[T any] struct{}

func (n none[T]) IsSome() bool                             { return false }
func (n none[T]) IsNone() bool                             { return true }
func (n none[T]) Unwrap() T                                { panic("called Unwrap on None") }
func (n none[T]) UnwrapOr(defaultValue T) T                { return defaultValue }
func (n none[T]) Map(_ func(T) T) Option[T]                { return n }
func (n none[T]) FlatMap(_ func(T) Option[T]) Option[T]    { return n }
func (n none[T]) Filter(_ func(T) bool) Option[T]          { return n }

// Constructors
func NewSome[T any](value T) Option[T] {
    return Some[T]{value: value}
}

func None[T any]() Option[T] {
    return none[T]{}
}

// FromPointer converts *T to Option[T]
func FromPointer[T any](ptr *T) Option[T] {
    if ptr == nil {
        return None[T]()
    }
    return NewSome(*ptr)
}

// Usage
func FindUser(id string) Option[User] {
    user := db.Get(id)
    if user == nil {
        return None[User]()
    }
    return NewSome(*user)
}

userName := FindUser("123").
    Map(func(u User) User { return u }).
    UnwrapOr(User{Name: "Unknown"}).
    Name

// Chaining
email := FindUser("123").
    FlatMap(func(u User) Option[Address] {
        return FindAddress(u.AddressID)
    }).
    Map(func(a Address) Address { return a }).
    UnwrapOr(Address{Email: "no-email@example.com"}).
    Email
```

**When:** Potentially absent values, avoid null checks.
**Related:** Either, Result.

---

### 7. Either

> Represent success or failure with context.

```go
// Either represents a value that can be Left (error) or Right (success)
type Either[L, R any] interface {
    IsLeft() bool
    IsRight() bool
    Left() L
    Right() R
    Map(fn func(R) R) Either[L, R]
    MapLeft(fn func(L) L) Either[L, R]
    FlatMap(fn func(R) Either[L, R]) Either[L, R]
    Fold(onLeft func(L) any, onRight func(R) any) any
}

// Left represents an error
type left[L, R any] struct {
    value L
}

func (l left[L, R]) IsLeft() bool                                    { return true }
func (l left[L, R]) IsRight() bool                                   { return false }
func (l left[L, R]) Left() L                                         { return l.value }
func (l left[L, R]) Right() R                                        { var zero R; return zero }
func (l left[L, R]) Map(_ func(R) R) Either[L, R]                    { return l }
func (l left[L, R]) MapLeft(fn func(L) L) Either[L, R]               { return NewLeft[L, R](fn(l.value)) }
func (l left[L, R]) FlatMap(_ func(R) Either[L, R]) Either[L, R]    { return l }
func (l left[L, R]) Fold(onLeft func(L) any, _ func(R) any) any     { return onLeft(l.value) }

// Right represents success
type right[L, R any] struct {
    value R
}

func (r right[L, R]) IsLeft() bool                                   { return false }
func (r right[L, R]) IsRight() bool                                  { return true }
func (r right[L, R]) Left() L                                        { var zero L; return zero }
func (r right[L, R]) Right() R                                       { return r.value }
func (r right[L, R]) Map(fn func(R) R) Either[L, R]                  { return NewRight[L, R](fn(r.value)) }
func (r right[L, R]) MapLeft(_ func(L) L) Either[L, R]               { return r }
func (r right[L, R]) FlatMap(fn func(R) Either[L, R]) Either[L, R]  { return fn(r.value) }
func (r right[L, R]) Fold(_ func(L) any, onRight func(R) any) any   { return onRight(r.value) }

// Constructors
func NewLeft[L, R any](value L) Either[L, R] {
    return left[L, R]{value: value}
}

func NewRight[L, R any](value R) Either[L, R] {
    return right[L, R]{value: value}
}

// Usage
type ValidationError struct {
    Field   string
    Message string
}

func ValidateEmail(email string) Either[ValidationError, string] {
    if !strings.Contains(email, "@") {
        return NewLeft[ValidationError, string](ValidationError{
            Field:   "email",
            Message: "Invalid email",
        })
    }
    return NewRight[ValidationError, string](email)
}

func ValidateAge(age int) Either[ValidationError, int] {
    if age < 0 || age > 150 {
        return NewLeft[ValidationError, int](ValidationError{
            Field:   "age",
            Message: "Invalid age",
        })
    }
    return NewRight[ValidationError, int](age)
}

// Chaining validations
type ValidUser struct {
    Email string
    Age   int
}

result := ValidateEmail("john@example.com").
    FlatMap(func(email string) Either[ValidationError, ValidUser] {
        return ValidateAge(30).Map(func(age int) int {
            return age
        }).Map(func(age int) ValidUser {
            return ValidUser{Email: email, Age: age}
        }).(Either[ValidationError, ValidUser])
    }).
    Fold(
        func(err ValidationError) any {
            return fmt.Sprintf("Error in %s: %s", err.Field, err.Message)
        },
        func(user ValidUser) any {
            return fmt.Sprintf("Valid user: %s, %d", user.Email, user.Age)
        },
    )
```

**When:** Error handling, validation, results with context.
**Related:** Option, Result.

---

### 8. Result/Try

> Encapsulate operations that may fail.

```go
// Result represents the result of an operation that may fail
type Result[T any] struct {
    value T
    err   error
}

// Ok creates a successful Result
func Ok[T any](value T) Result[T] {
    return Result[T]{value: value, err: nil}
}

// Err creates a failed Result
func Err[T any](err error) Result[T] {
    var zero T
    return Result[T]{value: zero, err: err}
}

// IsOk returns true if the result is successful
func (r Result[T]) IsOk() bool {
    return r.err == nil
}

// IsErr returns true if the result is an error
func (r Result[T]) IsErr() bool {
    return r.err != nil
}

// Unwrap returns the value or panics
func (r Result[T]) Unwrap() T {
    if r.err != nil {
        panic(fmt.Sprintf("called Unwrap on Err: %v", r.err))
    }
    return r.value
}

// UnwrapOr returns the value or a default
func (r Result[T]) UnwrapOr(defaultValue T) T {
    if r.err != nil {
        return defaultValue
    }
    return r.value
}

// Error returns the error or nil
func (r Result[T]) Error() error {
    return r.err
}

// Map transforms the value if Ok
func (r Result[T]) Map(fn func(T) T) Result[T] {
    if r.err != nil {
        return r
    }
    return Ok(fn(r.value))
}

// FlatMap chains operations that return Result
func (r Result[T]) FlatMap(fn func(T) Result[T]) Result[T] {
    if r.err != nil {
        return r
    }
    return fn(r.value)
}

// MapErr transforms the error if Err
func (r Result[T]) MapErr(fn func(error) error) Result[T] {
    if r.err == nil {
        return r
    }
    return Err[T](fn(r.err))
}

// Try wraps a function that may panic
func Try[T any](fn func() T) Result[T] {
    var result T
    var err error
    func() {
        defer func() {
            if r := recover(); r != nil {
                err = fmt.Errorf("panic: %v", r)
            }
        }()
        result = fn()
    }()
    if err != nil {
        return Err[T](err)
    }
    return Ok(result)
}

// TryCatch wraps a function that returns (T, error)
func TryCatch[T any](fn func() (T, error)) Result[T] {
    value, err := fn()
    if err != nil {
        return Err[T](err)
    }
    return Ok(value)
}

// TryAsync wraps an async function
func TryAsync[T any](ctx context.Context, fn func() (T, error)) Result[T] {
    type resultPair struct {
        value T
        err   error
    }

    ch := make(chan resultPair, 1)
    go func() {
        value, err := fn()
        ch <- resultPair{value: value, err: err}
    }()

    select {
    case <-ctx.Done():
        return Err[T](ctx.Err())
    case pair := <-ch:
        if pair.err != nil {
            return Err[T](pair.err)
        }
        return Ok(pair.value)
    }
}

// Usage
parseResult := Try(func() map[string]any {
    var data map[string]any
    if err := json.Unmarshal([]byte(jsonString), &data); err != nil {
        panic(err)
    }
    return data
})

data := parseResult.
    Map(func(j map[string]any) map[string]any {
        if val, ok := j["data"]; ok {
            return val.(map[string]any)
        }
        return map[string]any{"default": true}
    }).
    UnwrapOr(map[string]any{"default": true})

// Chaining with error handling
result := TryCatch(func() (string, error) {
    return readFile("config.json")
}).
    FlatMap(func(content string) Result[Config] {
        return TryCatch(func() (Config, error) {
            return parseConfig(content)
        })
    }).
    FlatMap(func(cfg Config) Result[Config] {
        return TryCatch(func() (Config, error) {
            return validateConfig(cfg)
        })
    })

if result.IsErr() {
    log.Printf("Configuration error: %v", result.Error())
} else {
    config := result.Unwrap()
    // Use config
}
```

**When:** Exceptions, parsing, I/O, risky operations.
**Related:** Either.

---

## Monads

### 9. Monad Pattern

> Container with flatMap for chaining.

```go
// Monad interface (not idiomatic Go, but shown for educational purposes)
type Monad[T any] interface {
    Map(fn func(T) T) Monad[T]
    FlatMap(fn func(T) Monad[T]) Monad[T]
}

// Identity Monad - simple wrapper
type Identity[T any] struct {
    value T
}

func NewIdentity[T any](value T) Identity[T] {
    return Identity[T]{value: value}
}

func (i Identity[T]) Map(fn func(T) T) Identity[T] {
    return Identity[T]{value: fn(i.value)}
}

func (i Identity[T]) FlatMap(fn func(T) Identity[T]) Identity[T] {
    return fn(i.value)
}

func (i Identity[T]) Get() T {
    return i.value
}

// IO Monad - defer side effects (Go-idiomatic version using closures)
type IO[T any] struct {
    effect func() (T, error)
}

func NewIO[T any](effect func() (T, error)) IO[T] {
    return IO[T]{effect: effect}
}

func (io IO[T]) Map(fn func(T) T) IO[T] {
    return IO[T]{
        effect: func() (T, error) {
            value, err := io.effect()
            if err != nil {
                var zero T
                return zero, err
            }
            return fn(value), nil
        },
    }
}

func (io IO[T]) FlatMap(fn func(T) IO[T]) IO[T] {
    return IO[T]{
        effect: func() (T, error) {
            value, err := io.effect()
            if err != nil {
                var zero T
                return zero, err
            }
            return fn(value).Run()
        },
    }
}

func (io IO[T]) Run() (T, error) {
    return io.effect()
}

// Usage with defer pattern
func ReadFile(path string) IO[string] {
    return NewIO(func() (string, error) {
        data, err := os.ReadFile(path)
        if err != nil {
            return "", err
        }
        return string(data), nil
    })
}

func WriteFile(path string, content string) IO[struct{}] {
    return NewIO(func() (struct{}, error) {
        return struct{}{}, os.WriteFile(path, []byte(content), 0644)
    })
}

// Compose IO operations
program := ReadFile("input.txt").
    Map(func(content string) string {
        return strings.ToUpper(content)
    }).
    FlatMap(func(content string) IO[struct{}] {
        return WriteFile("output.txt", content)
    })

// Nothing happens until:
if _, err := program.Run(); err != nil {
    log.Fatal(err)
}

// Go-idiomatic alternative: use context and closures
type Effect func(context.Context) error

func (e Effect) Then(next Effect) Effect {
    return func(ctx context.Context) error {
        if err := e(ctx); err != nil {
            return err
        }
        return next(ctx)
    }
}

func (e Effect) Run(ctx context.Context) error {
    return e(ctx)
}
```

**Monadic laws:**

1. Left identity: `of(a).flatMap(f) === f(a)`
2. Right identity: `m.flatMap(of) === m`
3. Associativity: `m.flatMap(f).flatMap(g) === m.flatMap(x => f(x).flatMap(g))`

**When:** Context chaining, effect composition.
**Related:** Option, Either, IO.

---

### 10. Reader Monad

> Functional dependency injection.

```go
// Reader represents a computation that depends on an environment
type Reader[E, A any] struct {
    run func(E) A
}

// NewReader creates a Reader from a function
func NewReader[E, A any](fn func(E) A) Reader[E, A] {
    return Reader[E, A]{run: fn}
}

// Of creates a Reader that returns a constant value
func Of[E, A any](value A) Reader[E, A] {
    return Reader[E, A]{
        run: func(_ E) A {
            return value
        },
    }
}

// Ask returns a Reader that returns the environment
func Ask[E any]() Reader[E, E] {
    return Reader[E, E]{
        run: func(env E) E {
            return env
        },
    }
}

// Map transforms the result
func (r Reader[E, A]) Map(fn func(A) A) Reader[E, A] {
    return Reader[E, A]{
        run: func(env E) A {
            return fn(r.run(env))
        },
    }
}

// FlatMap chains computations
func (r Reader[E, A]) FlatMap(fn func(A) Reader[E, A]) Reader[E, A] {
    return Reader[E, A]{
        run: func(env E) A {
            a := r.run(env)
            return fn(a).run(env)
        },
    }
}

// Run executes the Reader with an environment
func (r Reader[E, A]) Run(env E) A {
    return r.run(env)
}

// Example: Dependencies
type Env struct {
    Logger *slog.Logger
    DB     *sql.DB
    Config Config
}

type Config struct {
    APIURL string
}

// Functions using Reader
func LogMessage(msg string) Reader[Env, struct{}] {
    return Ask[Env]().Map(func(env Env) struct{} {
        env.Logger.Info(msg)
        return struct{}{}
    })
}

func GetUsers() Reader[Env, []User] {
    return Ask[Env]().Map(func(env Env) []User {
        rows, _ := env.DB.Query("SELECT * FROM users")
        defer rows.Close()

        var users []User
        for rows.Next() {
            var u User
            rows.Scan(&u.ID, &u.Name)
            users = append(users, u)
        }
        return users
    })
}

func FetchFromAPI(path string) Reader[Env, *http.Response] {
    return Ask[Env]().Map(func(env Env) *http.Response {
        resp, _ := http.Get(env.Config.APIURL + path)
        return resp
    })
}

// Compose
program := LogMessage("Starting").FlatMap(func(_ struct{}) Reader[Env, []User] {
    return GetUsers()
}).FlatMap(func(users []User) Reader[Env, struct{}] {
    return Ask[Env]().Map(func(env Env) struct{} {
        env.Logger.Info(fmt.Sprintf("Found %d users", len(users)))
        return struct{}{}
    })
})

// Run with environment
env := Env{
    Logger: slog.Default(),
    DB:     myDatabase,
    Config: Config{APIURL: "https://api.example.com"},
}

program.Run(env)

// Go-idiomatic alternative: use context for dependency injection
type contextKey string

const (
    loggerKey contextKey = "logger"
    dbKey     contextKey = "db"
    configKey contextKey = "config"
)

func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
    return context.WithValue(ctx, loggerKey, logger)
}

func GetLogger(ctx context.Context) *slog.Logger {
    return ctx.Value(loggerKey).(*slog.Logger)
}

// Then use context throughout the application
func ProcessRequest(ctx context.Context) error {
    logger := GetLogger(ctx)
    logger.Info("processing request")
    return nil
}
```

**When:** Configuration, dependency injection, environment.
**Related:** Monad, Dependency Injection.

---

### 11. State Monad

> Manage state in a pure way.

```go
// State represents a stateful computation
type State[S, A any] struct {
    runState func(S) (A, S)
}

// NewState creates a State from a function
func NewState[S, A any](fn func(S) (A, S)) State[S, A] {
    return State[S, A]{runState: fn}
}

// Of creates a State that returns a value without changing state
func StateOf[S, A any](value A) State[S, A] {
    return State[S, A]{
        runState: func(state S) (A, S) {
            return value, state
        },
    }
}

// Get returns the current state as the value
func GetState[S any]() State[S, S] {
    return State[S, S]{
        runState: func(state S) (S, S) {
            return state, state
        },
    }
}

// Put replaces the state
func PutState[S any](newState S) State[S, struct{}] {
    return State[S, struct{}]{
        runState: func(_ S) (struct{}, S) {
            return struct{}{}, newState
        },
    }
}

// Modify transforms the state
func ModifyState[S any](fn func(S) S) State[S, struct{}] {
    return State[S, struct{}]{
        runState: func(state S) (struct{}, S) {
            return struct{}{}, fn(state)
        },
    }
}

// Map transforms the value
func (s State[S, A]) Map(fn func(A) A) State[S, A] {
    return State[S, A]{
        runState: func(state S) (A, S) {
            a, newState := s.runState(state)
            return fn(a), newState
        },
    }
}

// FlatMap chains stateful computations
func (s State[S, A]) FlatMap(fn func(A) State[S, A]) State[S, A] {
    return State[S, A]{
        runState: func(state S) (A, S) {
            a, newState := s.runState(state)
            return fn(a).runState(newState)
        },
    }
}

// Run executes the stateful computation
func (s State[S, A]) Run(initialState S) (A, S) {
    return s.runState(initialState)
}

// Eval returns only the value
func (s State[S, A]) Eval(initialState S) A {
    value, _ := s.Run(initialState)
    return value
}

// Exec returns only the final state
func (s State[S, A]) Exec(initialState S) S {
    _, state := s.Run(initialState)
    return state
}

// Example: Counter
type CounterState struct {
    Count int
    Log   []string
}

func Increment() State[CounterState, int] {
    return GetState[CounterState]().FlatMap(func(state CounterState) State[CounterState, int] {
        newCount := state.Count + 1
        return PutState(CounterState{
            Count: newCount,
            Log:   state.Log,
        }).Map(func(_ struct{}) int {
            return newCount
        })
    })
}

func Log(msg string) State[CounterState, struct{}] {
    return ModifyState(func(state CounterState) CounterState {
        return CounterState{
            Count: state.Count,
            Log:   append(state.Log, msg),
        }
    })
}

program := Log("Starting").
    FlatMap(func(_ struct{}) State[CounterState, int] {
        return Increment()
    }).
    FlatMap(func(n int) State[CounterState, struct{}] {
        return Log(fmt.Sprintf("Count is %d", n))
    }).
    FlatMap(func(_ struct{}) State[CounterState, int] {
        return Increment()
    }).
    FlatMap(func(n int) State[CounterState, struct{}] {
        return Log(fmt.Sprintf("Count is %d", n))
    })

_, finalState := program.Run(CounterState{Count: 0, Log: []string{}})
// finalState = CounterState{Count: 2, Log: []string{"Starting", "Count is 1", "Count is 2"}}

// Go-idiomatic alternative: pass state explicitly
type StateTransform[S, A any] func(S) (A, S)

func Chain[S, A, B any](f StateTransform[S, A], g func(A) StateTransform[S, B]) StateTransform[S, B] {
    return func(state S) (B, S) {
        a, newState := f(state)
        return g(a)(newState)
    }
}
```

**When:** State in pure context, simulations, parsers.
**Related:** Monad.

---

## Advanced Patterns

### 12. Lens

> Immutable access and modification of nested structures.

```go
// Lens provides functional access to nested immutable data
type Lens[S, A any] struct {
    Get func(S) A
    Set func(A) func(S) S
}

// NewLens creates a new Lens
func NewLens[S, A any](get func(S) A, set func(A) func(S) S) Lens[S, A] {
    return Lens[S, A]{
        Get: get,
        Set: set,
    }
}

// Compose combines two lenses
func ComposeLens[S, A, B any](outer Lens[S, A], inner Lens[A, B]) Lens[S, B] {
    return Lens[S, B]{
        Get: func(s S) B {
            return inner.Get(outer.Get(s))
        },
        Set: func(b B) func(S) S {
            return func(s S) S {
                return outer.Set(inner.Set(b)(outer.Get(s)))(s)
            }
        },
    }
}

// Over modifies a value through a lens
func Over[S, A any](l Lens[S, A], fn func(A) A) func(S) S {
    return func(s S) S {
        return l.Set(fn(l.Get(s)))(s)
    }
}

// Example
type Address struct {
    Street string
    City   string
}

type Person struct {
    Name    string
    Address Address
}

var AddressLens = NewLens(
    func(p Person) Address { return p.Address },
    func(a Address) func(Person) Person {
        return func(p Person) Person {
            return Person{Name: p.Name, Address: a}
        }
    },
)

var CityLens = NewLens(
    func(a Address) string { return a.City },
    func(c string) func(Address) Address {
        return func(a Address) Address {
            return Address{Street: a.Street, City: c}
        }
    },
)

var PersonCityLens = ComposeLens(AddressLens, CityLens)

person := Person{
    Name:    "John",
    Address: Address{Street: "123 Main", City: "Paris"},
}

newPerson := PersonCityLens.Set("London")(person)
// Person{Name: "John", Address: Address{Street: "123 Main", City: "London"}}

// Update using Over
upperCity := Over(PersonCityLens, strings.ToUpper)
shoutingPerson := upperCity(person)
// Person{Name: "John", Address: Address{Street: "123 Main", City: "PARIS"}}

// Go-idiomatic alternative: use functional update methods
func (p Person) WithCity(city string) Person {
    return Person{
        Name: p.Name,
        Address: Address{
            Street: p.Address.Street,
            City:   city,
        },
    }
}

func (p Person) UpdateCity(fn func(string) string) Person {
    return p.WithCity(fn(p.Address.City))
}
```

**When:** Deep immutability, complex state, functional updates.
**Related:** Immutability.

---

### 13. Functor

> Container that supports map.

```go
// Functor interface
type Functor[T any] interface {
    Map(fn func(T) T) Functor[T]
}

// Slice is a functor
func MapSlice[T any](slice []T, fn func(T) T) []T {
    result := make([]T, len(slice))
    for i, v := range slice {
        result[i] = fn(v)
    }
    return result
}

// Example: MapSlice([]int{1, 2, 3}, func(x int) int { return x * 2 }) // [2, 4, 6]

// Context with value is a functor (conceptually)
type ContextValue[T any] struct {
    ctx   context.Context
    value T
}

func (cv ContextValue[T]) Map(fn func(T) T) ContextValue[T] {
    return ContextValue[T]{
        ctx:   cv.ctx,
        value: fn(cv.value),
    }
}

// Channel is a functor
func MapChannel[T any](ctx context.Context, input <-chan T, fn func(T) T) <-chan T {
    output := make(chan T)
    go func() {
        defer close(output)
        for {
            select {
            case <-ctx.Done():
                return
            case v, ok := <-input:
                if !ok {
                    return
                }
                select {
                case output <- fn(v):
                case <-ctx.Done():
                    return
                }
            }
        }
    }()
    return output
}

// Box functor
type Box[T any] struct {
    value T
}

func NewBox[T any](value T) Box[T] {
    return Box[T]{value: value}
}

func (b Box[T]) Map(fn func(T) T) Box[T] {
    return Box[T]{value: fn(b.value)}
}

func (b Box[T]) Fold(fn func(T) any) any {
    return fn(b.value)
}

// Usage
result := NewBox(5).
    Map(func(x int) int { return x * 2 }).
    Map(func(x int) int { return x + 1 }).
    Fold(func(x int) any { return fmt.Sprintf("Result: %d", x) })
// "Result: 11"

// Generic functor with Go 1.23+ iter
func MapIter[T any](seq iter.Seq[T], fn func(T) T) iter.Seq[T] {
    return func(yield func(T) bool) {
        for v := range seq {
            if !yield(fn(v)) {
                return
            }
        }
    }
}
```

**Laws:**

1. Identity: `f.map(x => x) === f`
2. Composition: `f.map(g).map(h) === f.map(x => h(g(x)))`

**When:** Value transformation within context.
**Related:** Monad, Applicative.

---

### 14. Applicative

> Functor with application in context.

```go
// Applicative extends Functor with the ability to apply wrapped functions
type Applicative[T any] interface {
    Map(fn func(T) T) Applicative[T]
    Apply(fn Applicative[func(T) T]) Applicative[T]
}

// ApplicativeOption implements Applicative for Option
type ApplicativeOption[T any] struct {
    value Option[T]
}

func NewApplicativeOption[T any](opt Option[T]) ApplicativeOption[T] {
    return ApplicativeOption[T]{value: opt}
}

func ApplicativeOf[T any](value T) ApplicativeOption[T] {
    return ApplicativeOption[T]{value: NewSome(value)}
}

func (a ApplicativeOption[T]) Map(fn func(T) T) ApplicativeOption[T] {
    return ApplicativeOption[T]{value: a.value.Map(fn)}
}

func (a ApplicativeOption[T]) Apply(fn ApplicativeOption[func(T) T]) ApplicativeOption[T] {
    if fn.value.IsNone() || a.value.IsNone() {
        return ApplicativeOption[T]{value: None[T]()}
    }

    f := fn.value.Unwrap()
    return ApplicativeOption[T]{value: NewSome(f(a.value.Unwrap()))}
}

// LiftA2 lifts a binary function to work with Applicatives
func LiftA2[A, B, C any](
    fn func(A) func(B) C,
    fa ApplicativeOption[A],
    fb ApplicativeOption[B],
) ApplicativeOption[C] {
    // fa.Map(fn) gives ApplicativeOption[func(B) C]
    // We need to apply this to fb
    mapped := fa.Map(func(a A) func(B) C {
        return fn(a)
    })

    // Convert to applicative function
    if mapped.value.IsNone() {
        return ApplicativeOption[C]{value: None[C]()}
    }

    fnWrapped := mapped.value.Unwrap()
    if fb.value.IsNone() {
        return ApplicativeOption[C]{value: None[C]()}
    }

    return ApplicativeOption[C]{value: NewSome(fnWrapped(fb.value.Unwrap()))}
}

// Usage - combine two Options
add := func(a int) func(int) int {
    return func(b int) int {
        return a + b
    }
}

result := LiftA2(
    add,
    ApplicativeOf(5),
    ApplicativeOf(3),
) // Some(8)

// Validation with multiple errors (using Either)
type ValidationResult[T any] struct {
    value  T
    errors []string
}

func ValidateForm(name, email string) ValidationResult[ValidUser] {
    var errors []string
    var user ValidUser

    if len(name) < 3 {
        errors = append(errors, "name too short")
    } else {
        user.Name = name
    }

    if !strings.Contains(email, "@") {
        errors = append(errors, "invalid email")
    } else {
        user.Email = email
    }

    return ValidationResult[ValidUser]{
        value:  user,
        errors: errors,
    }
}

// Go-idiomatic alternative: use errgroup for concurrent validation
func ValidateFormConcurrent(ctx context.Context, name, email string) (*ValidUser, error) {
    var g errgroup.Group
    var nameErr, emailErr error

    g.Go(func() error {
        if len(name) < 3 {
            nameErr = errors.New("name too short")
        }
        return nil
    })

    g.Go(func() error {
        if !strings.Contains(email, "@") {
            emailErr = errors.New("invalid email")
        }
        return nil
    })

    g.Wait()

    if nameErr != nil || emailErr != nil {
        return nil, errors.Join(nameErr, emailErr)
    }

    return &ValidUser{Name: name, Email: email}, nil
}
```

**When:** Combining multiple contexts, parallel validation.
**Related:** Functor, Monad.

---

### 15. Transducer

> Composition of reusable transformations.

```go
// Reducer combines an accumulator with a value
type Reducer[A, B any] func(acc A, value B) A

// Transducer transforms one reducer into another
type Transducer[A, B any] func(Reducer[any, B]) Reducer[any, A]

// Map creates a mapping transducer
func MapTransducer[A, B any](fn func(A) B) Transducer[A, B] {
    return func(reducer Reducer[any, B]) Reducer[any, A] {
        return func(acc any, value A) any {
            return reducer(acc, fn(value))
        }
    }
}

// Filter creates a filtering transducer
func FilterTransducer[A any](predicate func(A) bool) Transducer[A, A] {
    return func(reducer Reducer[any, A]) Reducer[any, A] {
        return func(acc any, value A) any {
            if predicate(value) {
                return reducer(acc, value)
            }
            return acc
        }
    }
}

// Take creates a transducer that takes n elements
func TakeTransducer[A any](n int) Transducer[A, A] {
    return func(reducer Reducer[any, A]) Reducer[any, A] {
        taken := 0
        return func(acc any, value A) any {
            if taken < n {
                taken++
                return reducer(acc, value)
            }
            return acc
        }
    }
}

// ComposeTransducers composes two transducers
func ComposeTransducers[A, B, C any](t1 Transducer[A, B], t2 Transducer[B, C]) Transducer[A, C] {
    return func(reducer Reducer[any, C]) Reducer[any, A] {
        return t1(t2(reducer))
    }
}

// Transduce applies a transducer to a collection
func Transduce[A, B any](
    transducer Transducer[A, B],
    reducer Reducer[[]B, B],
    initial []B,
    collection []A,
) []B {
    xf := transducer(func(acc any, value B) any {
        slice := acc.([]B)
        return append(slice, value)
    })

    var acc any = initial
    for _, item := range collection {
        acc = xf(acc, item)
    }
    return acc.([]B)
}

// Usage
transducer := ComposeTransducers(
    FilterTransducer(func(x int) bool { return x%2 == 0 }),
    MapTransducer(func(x int) int { return x * 2 }),
)

result := Transduce(
    transducer,
    func(acc []int, x int) []int { return append(acc, x) },
    []int{},
    []int{1, 2, 3, 4, 5},
)
// [4, 8]

// Go-idiomatic alternative: use channel pipelines
func MapChan[T, U any](ctx context.Context, in <-chan T, fn func(T) U) <-chan U {
    out := make(chan U)
    go func() {
        defer close(out)
        for v := range in {
            select {
            case out <- fn(v):
            case <-ctx.Done():
                return
            }
        }
    }()
    return out
}

func FilterChan[T any](ctx context.Context, in <-chan T, predicate func(T) bool) <-chan T {
    out := make(chan T)
    go func() {
        defer close(out)
        for v := range in {
            if predicate(v) {
                select {
                case out <- v:
                case <-ctx.Done():
                    return
                }
            }
        }
    }()
    return out
}

func TakeChan[T any](ctx context.Context, in <-chan T, n int) <-chan T {
    out := make(chan T)
    go func() {
        defer close(out)
        count := 0
        for v := range in {
            if count >= n {
                return
            }
            select {
            case out <- v:
                count++
            case <-ctx.Done():
                return
            }
        }
    }()
    return out
}

// Pipeline composition
ctx := context.Background()
input := make(chan int)

pipeline := TakeChan(ctx,
    MapChan(ctx,
        FilterChan(ctx, input, func(x int) bool { return x%2 == 0 }),
        func(x int) int { return x * 2 },
    ),
    10,
)

// Using Go 1.23+ iter for lazy evaluation
func MapSeq[T, U any](seq iter.Seq[T], fn func(T) U) iter.Seq[U] {
    return func(yield func(U) bool) {
        for v := range seq {
            if !yield(fn(v)) {
                return
            }
        }
    }
}

func FilterSeq[T any](seq iter.Seq[T], predicate func(T) bool) iter.Seq[T] {
    return func(yield func(T) bool) {
        for v := range seq {
            if predicate(v) {
                if !yield(v) {
                    return
                }
            }
        }
    }
}
```

**When:** Efficient pipelines, streams, infinite collections.
**Related:** Composition, Iterator.

---

## Decision Table

| Need | Pattern |
|--------|---------|
| Avoid null | Option/Maybe |
| Success or error | Either/Result |
| Chain contexts | Monad |
| Dependency injection | Reader, Context |
| Pure state | State |
| Nested modification | Lens |
| Transform in context | Functor |
| Combine contexts | Applicative |
| Efficient pipelines | Transducer, Channels |
| Partial configuration | Currying, Functional Options |
| Combine functions | Composition |

## Sources

- [Professor Frisby's Guide to FP](https://mostly-adequate.gitbook.io/mostly-adequate-guide/)
- [Functional Programming in TypeScript](https://github.com/gcanti/fp-ts)
- [Learn You a Haskell](http://learnyouahaskell.com/)
- Go Generics (1.18+)
- Go iter package (1.23+)
