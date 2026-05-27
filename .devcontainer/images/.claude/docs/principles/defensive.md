# Defensive Programming Patterns

Patterns to protect code against errors and invalid data.

## 1. Guard Clauses (Early Return)

> Validate preconditions at the beginning of a function and return immediately if invalid.

```go
// ❌ BAD - Nested conditions
func processOrder(order *Order, user *User) (float64, error) {
	if order != nil {
		if user != nil {
			if len(order.Items) > 0 {
				if user.IsActive {
					// Logic buried deep
					return calculateTotal(order), nil
				}
				return 0, errors.New("user inactive")
			}
			return 0, errors.New("empty order")
		}
		return 0, errors.New("no user")
	}
	return 0, errors.New("no order")
}

// ✅ GOOD - Guard clauses
func processOrder(order *Order, user *User) (float64, error) {
	// Guards - validate all preconditions first
	if order == nil {
		return 0, errors.New("no order")
	}
	if user == nil {
		return 0, errors.New("no user")
	}
	if len(order.Items) == 0 {
		return 0, errors.New("empty order")
	}
	if !user.IsActive {
		return 0, errors.New("user inactive")
	}

	// Happy path - logic is clear and flat
	return calculateTotal(order), nil
}
```

**Rule:** All validations at the top, business logic at the bottom.

---

## 2. Assertion / Precondition

> Verify invariants with explicit assertions.

```go
// AssertionError is a custom error for assertion failures.
type AssertionError struct {
	Message string
}

func (e *AssertionError) Error() string {
	return e.Message
}

// Assert checks a condition and panics if false (use only for programming errors).
func Assert(condition bool, message string) {
	if !condition {
		panic(&AssertionError{Message: message})
	}
}

// AssertDefined checks if a value is not nil.
func AssertDefined[T any](value *T, name string) {
	if value == nil {
		panic(&AssertionError{Message: fmt.Sprintf("%s must be defined", name)})
	}
}

// AssertPositive checks if a value is positive.
func AssertPositive(value float64, name string) {
	if value <= 0 {
		panic(&AssertionError{Message: fmt.Sprintf("%s must be positive, got %f", name, value)})
	}
}

// Usage
func divide(a, b float64) float64 {
	Assert(b != 0, "cannot divide by zero")
	return a / b
}

func withdraw(account *Account, amount float64) error {
	AssertDefined(account, "account")
	AssertPositive(amount, "amount")

	if account.Balance < amount {
		return errors.New("insufficient funds")
	}

	account.Balance -= amount
	return nil
}
```

---

## 3. Null Object Pattern

> Replace null with a neutral object with default behavior.

```go
// Logger interface.
type Logger interface {
	Log(message string)
	Error(message string)
}

// ConsoleLogger is the real implementation.
type ConsoleLogger struct{}

func (l *ConsoleLogger) Log(message string) {
	fmt.Println(message)
}

func (l *ConsoleLogger) Error(message string) {
	fmt.Fprintln(os.Stderr, message)
}

// NullLogger does nothing but is safe to use.
type NullLogger struct{}

func (l *NullLogger) Log(_ string)   {}
func (l *NullLogger) Error(_ string) {}

// OrderService uses a logger with null object as default.
type OrderService struct {
	logger Logger
}

// NewOrderService creates an OrderService with optional logger.
func NewOrderService(logger Logger) *OrderService {
	if logger == nil {
		logger = &NullLogger{}
	}
	return &OrderService{logger: logger}
}

func (s *OrderService) Process(order *Order) error {
	s.logger.Log(fmt.Sprintf("Processing order %s", order.ID))
	// ... logic
	s.logger.Log("Order processed")
	return nil
}

// Usage - Both work without nil checks
// service := NewOrderService(&ConsoleLogger{})
// service := NewOrderService(nil) // Uses NullLogger
```

**Other examples:**

```go
// Null User
type GuestUser struct{}

func (u *GuestUser) ID() string         { return "guest" }
func (u *GuestUser) Name() string       { return "Guest" }
func (u *GuestUser) HasPermission(perm string) bool { return false }

// Null Money
type ZeroMoney struct{}

func (m *ZeroMoney) Amount() float64                   { return 0 }
func (m *ZeroMoney) Add(other Money) Money             { return other }
func (m *ZeroMoney) Multiply(_ float64) Money          { return m }
```

---

## 4. Optional Chaining & Nullish Coalescing

> Safe access to potentially null properties.

```go
// Go doesn't have optional chaining, but we use safe accessor patterns

// Safe access with default value
func getStreet(user *User) string {
	if user == nil || user.Address == nil {
		return ""
	}
	return user.Address.Street
}

// GetNameOrDefault returns username or default.
func GetNameOrDefault(user *User, defaultName string) string {
	if user == nil || user.Name == "" {
		return defaultName
	}
	return user.Name
}

// UpperOrNA returns uppercase name or N/A.
func UpperOrNA(user *User) string {
	if user == nil || user.Name == "" {
		return "N/A"
	}
	return strings.ToUpper(user.Name)
}

// FirstItemOrNil safely gets first item from slice.
func FirstItemOrNil[T any](items []T) *T {
	if len(items) == 0 {
		return nil
	}
	return &items[0]
}

// Helper for default values
func DefaultInt(value *int, defaultVal int) int {
	if value == nil {
		return defaultVal
	}
	return *value
}

func DefaultString(value *string, defaultVal string) string {
	if value == nil || *value == "" {
		return defaultVal
	}
	return *value
}
```

---

## 5. Default Values Pattern

> Provide safe default values.

```go
// Option pattern for configuration
type ServerOption func(*Server)

// WithTimeout sets the server timeout.
func WithTimeout(d time.Duration) ServerOption {
	return func(s *Server) {
		s.timeout = d
	}
}

// WithLogger sets the server logger.
func WithLogger(l Logger) ServerOption {
	return func(s *Server) {
		s.logger = l
	}
}

// Server with default values.
type Server struct {
	addr    string
	timeout time.Duration
	logger  Logger
}

// NewServer creates a new server with options and defaults.
func NewServer(addr string, opts ...ServerOption) *Server {
	s := &Server{
		addr:    addr,
		timeout: 30 * time.Second,
		logger:  &NullLogger{},
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Config with defaults
type Config struct {
	Timeout time.Duration
	Retries int
	BaseURL string
}

// DefaultConfig returns configuration with safe defaults.
func DefaultConfig() Config {
	return Config{
		Timeout: 5000 * time.Millisecond,
		Retries: 3,
		BaseURL: "https://api.example.com",
	}
}

// MergeConfig merges user config with defaults.
func MergeConfig(userConfig *Config) Config {
	cfg := DefaultConfig()
	if userConfig != nil {
		if userConfig.Timeout != 0 {
			cfg.Timeout = userConfig.Timeout
		}
		if userConfig.Retries != 0 {
			cfg.Retries = userConfig.Retries
		}
		if userConfig.BaseURL != "" {
			cfg.BaseURL = userConfig.BaseURL
		}
	}
	return cfg
}
```

---

## 6. Fail-Fast Pattern

> Fail immediately with a clear message rather than propagating the error.

```go
// ConfigError indicates a configuration error.
type ConfigError struct {
	Message string
}

func (e *ConfigError) Error() string {
	return fmt.Sprintf("configuration error: %s", e.Message)
}

// DatabaseConnection with fail-fast validation.
type DatabaseConnection struct {
	config DbConfig
}

// NewDatabaseConnection validates config and fails fast.
func NewDatabaseConnection(config DbConfig) (*DatabaseConnection, error) {
	// Fail fast - validate everything at construction
	if config.Host == "" {
		return nil, &ConfigError{Message: "database host is required"}
	}
	if config.Port < 1 || config.Port > 65535 {
		return nil, &ConfigError{Message: fmt.Sprintf("invalid port: %d", config.Port)}
	}
	if config.Database == "" {
		return nil, &ConfigError{Message: "database name is required"}
	}

	// If we get here, config is valid
	conn := &DatabaseConnection{config: config}
	if err := conn.connect(); err != nil {
		return nil, fmt.Errorf("connecting to database: %w", err)
	}
	return conn, nil
}

// ValidationError indicates validation failure.
type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation error: %s", e.Message)
}

// CreateUser validates and creates user or fails fast.
func CreateUser(data map[string]interface{}) (*User, error) {
	// Validate and fail fast
	email, ok := data["email"].(string)
	if !ok || email == "" {
		return nil, &ValidationError{Message: "email is required"}
	}
	if !isValidEmail(email) {
		return nil, &ValidationError{Message: fmt.Sprintf("invalid email format: %s", email)}
	}

	// Only create if valid
	return &User{Email: email}, nil
}
```

---

## 7. Input Validation Pattern

> Validate all inputs at system boundaries.

```go
// Validator interface for input validation.
type Validator interface {
	Validate() error
}

// UserInput represents user registration input.
type UserInput struct {
	Email    string
	Password string
	Age      *int
	Role     string
}

// Validate validates user input.
func (u *UserInput) Validate() error {
	var errs []string

	if u.Email == "" {
		errs = append(errs, "email is required")
	} else if !isValidEmail(u.Email) {
		errs = append(errs, "invalid email format")
	}

	if len(u.Password) < 8 || len(u.Password) > 100 {
		errs = append(errs, "password must be between 8 and 100 characters")
	}

	if u.Age != nil && (*u.Age < 0 || *u.Age > 150) {
		errs = append(errs, "age must be between 0 and 150")
	}

	validRoles := map[string]bool{"admin": true, "user": true, "guest": true}
	if u.Role == "" {
		u.Role = "user" // Default
	} else if !validRoles[u.Role] {
		errs = append(errs, "invalid role")
	}

	if len(errs) > 0 {
		return &ValidationError{Message: strings.Join(errs, "; ")}
	}
	return nil
}

// CreateUserFromInput validates and creates user.
func CreateUserFromInput(input *UserInput) (*User, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}
	return &User{
		Email: input.Email,
		Role:  input.Role,
	}, nil
}

// OrderInput with nested validation.
type OrderInput struct {
	Items        []OrderItemInput
	ShippingDate time.Time
}

type OrderItemInput struct {
	ProductID string
	Quantity  int
}

// Validate validates order input.
func (o *OrderInput) Validate() error {
	if len(o.Items) == 0 {
		return &ValidationError{Message: "order must have at least one item"}
	}

	for i, item := range o.Items {
		if item.ProductID == "" {
			return &ValidationError{Message: fmt.Sprintf("item %d: product ID is required", i)}
		}
		if item.Quantity <= 0 {
			return &ValidationError{Message: fmt.Sprintf("item %d: quantity must be positive", i)}
		}
	}

	if o.ShippingDate.Before(time.Now()) {
		return &ValidationError{Message: "shipping date must be in the future"}
	}

	return nil
}
```

---

## 8. Type Narrowing / Type Guards

> Progressively reduce possible types.

```go
// Type assertion and checking patterns in Go

// IsUser checks if value is a User.
func IsUser(value interface{}) (*User, bool) {
	user, ok := value.(*User)
	return user, ok
}

// MustBeUser asserts value is User or panics.
func MustBeUser(value interface{}) *User {
	user, ok := value.(*User)
	if !ok {
		panic("expected *User")
	}
	return user
}

// ProcessValue handles different types safely.
func ProcessValue(value interface{}) {
	switch v := value.(type) {
	case string:
		fmt.Println(strings.ToUpper(v))
	case *User:
		fmt.Println(v.Email)
	case int:
		fmt.Println(v * 2)
	default:
		fmt.Println("unknown type")
	}
}

// Result type with discriminated union pattern
type Result[T any] struct {
	data  *T
	err   error
	isOk  bool
}

// Ok creates a successful result.
func Ok[T any](data T) Result[T] {
	return Result[T]{data: &data, isOk: true}
}

// Err creates an error result.
func Err[T any](err error) Result[T] {
	return Result[T]{err: err, isOk: false}
}

// IsOk returns true if result is successful.
func (r Result[T]) IsOk() bool {
	return r.isOk
}

// Unwrap returns data or panics.
func (r Result[T]) Unwrap() T {
	if !r.isOk {
		panic(fmt.Sprintf("unwrap failed: %v", r.err))
	}
	return *r.data
}

// UnwrapOr returns data or default.
func (r Result[T]) UnwrapOr(defaultVal T) T {
	if !r.isOk {
		return defaultVal
	}
	return *r.data
}

// HandleResult demonstrates pattern matching on Result.
func HandleResult[T any](result Result[T]) {
	if result.IsOk() {
		fmt.Printf("Success: %v\n", result.Unwrap())
	} else {
		fmt.Printf("Error: %v\n", result.err)
	}
}
```

---

## 9. Immutable by Default

> Make data immutable to avoid accidental modifications.

```go
// User with immutable fields (use unexported fields + getters).
type User struct {
	id        string
	email     string
	createdAt time.Time
	name      string // Only name is mutable
}

// NewUser creates a new user (all fields set at construction).
func NewUser(id, email, name string) *User {
	return &User{
		id:        id,
		email:     email,
		createdAt: time.Now(),
		name:      name,
	}
}

// ID returns the immutable user ID.
func (u *User) ID() string { return u.id }

// Email returns the immutable email.
func (u *User) Email() string { return u.email }

// CreatedAt returns the immutable creation time.
func (u *User) CreatedAt() time.Time { return u.createdAt }

// Name returns the mutable name.
func (u *User) Name() string { return u.name }

// SetName updates the mutable name field.
func (u *User) SetName(name string) { u.name = name }

// WithName returns a new User with updated name (immutable update).
func (u *User) WithName(name string) *User {
	return &User{
		id:        u.id,
		email:     u.email,
		createdAt: u.createdAt,
		name:      name,
	}
}

// Config as immutable struct
type Config struct {
	APIUrl  string
	Timeout time.Duration
}

// Global config (frozen)
var DefaultConfig = Config{
	APIUrl:  "https://api.example.com",
	Timeout: 5000 * time.Millisecond,
}

// WithTimeout returns a new Config with updated timeout.
func (c Config) WithTimeout(timeout time.Duration) Config {
	return Config{
		APIUrl:  c.APIUrl,
		Timeout: timeout,
	}
}

// Immutable slice pattern - return copies
func AppendItem[T any](slice []T, item T) []T {
	result := make([]T, len(slice), len(slice)+1)
	copy(result, slice)
	return append(result, item)
}
```

---

## 10. Dependency Validation

> Validate that all dependencies are present and valid at startup.

```go
// DependencyError indicates a missing dependency.
type DependencyError struct {
	Name string
}

func (e *DependencyError) Error() string {
	return fmt.Sprintf("%s is required but was not provided", e.Name)
}

// Application with validated dependencies.
type Application struct {
	db     Database
	cache  Cache
	logger Logger
}

// ApplicationDeps holds all dependencies.
type ApplicationDeps struct {
	DB     Database
	Cache  Cache
	Logger Logger
}

// NewApplication creates an application with validated dependencies.
func NewApplication(deps ApplicationDeps) (*Application, error) {
	app := &Application{}

	// Validate all dependencies at startup
	if deps.DB == nil {
		return nil, &DependencyError{Name: "Database"}
	}
	app.db = deps.DB

	if deps.Cache == nil {
		return nil, &DependencyError{Name: "Cache"}
	}
	app.cache = deps.Cache

	if deps.Logger == nil {
		return nil, &DependencyError{Name: "Logger"}
	}
	app.logger = deps.Logger

	return app, nil
}

// Start starts the application and verifies connections.
func (app *Application) Start(ctx context.Context) error {
	// Verify connections work
	if err := app.verifyDependencies(ctx); err != nil {
		return fmt.Errorf("dependency verification failed: %w", err)
	}
	app.logger.Log("Application started")
	return nil
}

func (app *Application) verifyDependencies(ctx context.Context) error {
	errChan := make(chan error, 2)

	go func() {
		if err := app.db.Ping(ctx); err != nil {
			errChan <- fmt.Errorf("database unavailable: %w", err)
			return
		}
		errChan <- nil
	}()

	go func() {
		if err := app.cache.Ping(ctx); err != nil {
			errChan <- fmt.Errorf("cache unavailable: %w", err)
			return
		}
		errChan <- nil
	}()

	for i := 0; i < 2; i++ {
		if err := <-errChan; err != nil {
			return err
		}
	}
	return nil
}

// ServiceFactory with validation.
type ServiceFactory struct{}

// Create validates and creates a service.
func (f *ServiceFactory) Create(config ServiceConfig) (*Service, error) {
	// Validate config
	if err := f.validateConfig(config); err != nil {
		return nil, err
	}

	// Validate environment
	if err := f.validateEnvironment(); err != nil {
		return nil, err
	}

	// Create with validated dependencies
	return NewService(config), nil
}

func (f *ServiceFactory) validateConfig(config ServiceConfig) error {
	var errs []string

	if config.APIKey == "" {
		errs = append(errs, "API key is required")
	}
	if config.Endpoint == "" {
		errs = append(errs, "endpoint is required")
	}
	if config.Timeout < 0 {
		errs = append(errs, "timeout must be positive")
	}

	if len(errs) > 0 {
		return &ConfigError{Message: strings.Join(errs, ", ")}
	}
	return nil
}

func (f *ServiceFactory) validateEnvironment() error {
	required := []string{"NODE_ENV", "API_SECRET"}
	var missing []string

	for _, key := range required {
		if os.Getenv(key) == "" {
			missing = append(missing, key)
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("missing environment variables: %s", strings.Join(missing, ", "))
	}
	return nil
}
```

---

## 11. Contract / Design by Contract

> Define preconditions, postconditions and invariants.

```go
// PreconditionError indicates a precondition violation.
type PreconditionError struct {
	Message string
}

func (e *PreconditionError) Error() string {
	return fmt.Sprintf("precondition violated: %s", e.Message)
}

// PostconditionError indicates a postcondition violation.
type PostconditionError struct {
	Message string
}

func (e *PostconditionError) Error() string {
	return fmt.Sprintf("postcondition violated: %s", e.Message)
}

// BankAccount with design by contract.
type BankAccount struct {
	balance float64
}

// NewBankAccount creates a new account with initial balance.
func NewBankAccount(initialBalance float64) (*BankAccount, error) {
	// Precondition
	if initialBalance < 0 {
		return nil, &PreconditionError{Message: "initial balance must be non-negative"}
	}
	return &BankAccount{balance: initialBalance}, nil
}

// Balance returns the current balance.
func (a *BankAccount) Balance() float64 {
	return a.balance
}

// Deposit adds money to the account.
func (a *BankAccount) Deposit(amount float64) error {
	// Precondition
	if amount <= 0 {
		return &PreconditionError{Message: "deposit amount must be positive"}
	}

	oldBalance := a.balance
	a.balance += amount

	// Postcondition
	if a.balance != oldBalance+amount {
		return &PostconditionError{Message: "balance should increase by deposit amount"}
	}

	// Invariant
	if err := a.checkInvariant(); err != nil {
		return err
	}

	return nil
}

// Withdraw removes money from the account.
func (a *BankAccount) Withdraw(amount float64) error {
	// Preconditions
	if amount <= 0 {
		return &PreconditionError{Message: "withdrawal amount must be positive"}
	}
	if amount > a.balance {
		return &PreconditionError{Message: fmt.Sprintf("insufficient funds: %.2f > %.2f", amount, a.balance)}
	}

	oldBalance := a.balance
	a.balance -= amount

	// Postcondition
	if a.balance != oldBalance-amount {
		return &PostconditionError{Message: "balance should decrease by withdrawal amount"}
	}

	// Invariant
	if err := a.checkInvariant(); err != nil {
		return err
	}

	return nil
}

// checkInvariant verifies the balance invariant.
func (a *BankAccount) checkInvariant() error {
	if a.balance < 0 {
		return &PostconditionError{Message: "balance invariant violated: balance is negative"}
	}
	return nil
}
```

---

## When to Use

- When processing external data (API, files, user input)
- When a function's preconditions must be explicitly verified
- To protect invariants of an object or system
- When integrating with unreliable third-party systems
- When silent errors could cause serious downstream problems

---

## Decision Table

| Problem | Pattern |
|---------|---------|
| Nested conditions | Guard Clauses |
| Verify invariants | Assertions |
| Avoid null checks | Null Object |
| Access nullable properties | Safe Accessors |
| Missing values | Default Values |
| Silent errors | Fail-Fast |
| External data | Input Validation |
| Unknown types | Type Assertions |
| Accidental modifications | Immutability |
| Missing dependencies | Dependency Validation |
| Formal guarantees | Design by Contract |

## Related Patterns

- [SOLID](./SOLID.md) - DIP facilitates validator injection
- [GRASP](./GRASP.md) - Information Expert for placing validations
- [KISS](./KISS.md) - Guard clauses simplify nested conditions
- [Null Object Pattern](../behavioral/README.md) - Alternative to null checks

## Sources

- [Defensive Programming - Wikipedia](https://en.wikipedia.org/wiki/Defensive_programming)
- [Design by Contract - Bertrand Meyer](https://en.wikipedia.org/wiki/Design_by_contract)
- [Guard Clause - Refactoring Guru](https://refactoring.guru/replace-nested-conditional-with-guard-clauses)
