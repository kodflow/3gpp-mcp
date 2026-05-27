# Strategy Pattern

> Define a family of interchangeable algorithms.

## Intent

Define a family of algorithms, encapsulate each one and make them
interchangeable. Strategy allows changing the algorithm independently
from the clients that use it.

## Structure

```go
package main

import (
	"fmt"
	"regexp"
	"time"
)

// PaymentResult represents the result of a payment.
type PaymentResult struct {
	Success       bool
	TransactionID string
	Error         string
}

// PaymentStrategy defines the interface for payment strategies.
type PaymentStrategy interface {
	Pay(amount float64) (*PaymentResult, error)
	Validate() bool
}

// CreditCardStrategy implements payment via credit card.
type CreditCardStrategy struct {
	cardNumber string
	cvv        string
	expiryDate string
}

// NewCreditCardStrategy creates a new credit card strategy.
func NewCreditCardStrategy(cardNumber, cvv, expiryDate string) *CreditCardStrategy {
	return &CreditCardStrategy{
		cardNumber: cardNumber,
		cvv:        cvv,
		expiryDate: expiryDate,
	}
}

// Validate checks if card details are valid.
func (c *CreditCardStrategy) Validate() bool {
	return len(c.cardNumber) == 16 &&
		len(c.cvv) == 3 &&
		c.isValidExpiry()
}

func (c *CreditCardStrategy) isValidExpiry() bool {
	// Parse MM/YY format
	var month, year int
	if _, err:= fmt.Sscanf(c.expiryDate, "%d/%d", &month, &year); err != nil {
		return false
	}

	expiry:= time.Date(2000+year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	return expiry.After(time.Now())
}

// Pay processes a credit card payment.
func (c *CreditCardStrategy) Pay(amount float64) (*PaymentResult, error) {
	if !c.Validate() {
		return &PaymentResult{
			Success: false,
			Error:   "Invalid card details",
		}, nil
	}

	// Integration with payment gateway
	fmt.Printf("Processing credit card payment of $%.2f\n", amount)
	return &PaymentResult{
		Success:       true,
		TransactionID: fmt.Sprintf("CC_%d", time.Now().Unix()),
	}, nil
}

// PayPalStrategy implements payment via PayPal.
type PayPalStrategy struct {
	email string
}

// NewPayPalStrategy creates a new PayPal strategy.
func NewPayPalStrategy(email string) *PayPalStrategy {
	return &PayPalStrategy{email: email}
}

// Validate checks if email is valid.
func (p *PayPalStrategy) Validate() bool {
	emailRegex:= regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)
	return emailRegex.MatchString(p.email)
}

// Pay processes a PayPal payment.
func (p *PayPalStrategy) Pay(amount float64) (*PaymentResult, error) {
	if !p.Validate() {
		return &PaymentResult{
			Success: false,
			Error:   "Invalid PayPal email",
		}, nil
	}

	fmt.Printf("Processing PayPal payment of $%.2f to %s\n", amount, p.email)
	return &PaymentResult{
		Success:       true,
		TransactionID: fmt.Sprintf("PP_%d", time.Now().Unix()),
	}, nil
}

// CryptoStrategy implements payment via cryptocurrency.
type CryptoStrategy struct {
	walletAddress string
	currency      string
}

// NewCryptoStrategy creates a new crypto strategy.
func NewCryptoStrategy(walletAddress, currency string) *CryptoStrategy {
	return &CryptoStrategy{
		walletAddress: walletAddress,
		currency:      currency,
	}
}

// Validate checks if wallet address is valid.
func (c *CryptoStrategy) Validate() bool {
	return len(c.walletAddress) >= 26
}

// Pay processes a cryptocurrency payment.
func (c *CryptoStrategy) Pay(amount float64) (*PaymentResult, error) {
	cryptoAmount, err:= c.convertToCrypto(amount)
	if err != nil {
		return nil, err
	}

	fmt.Printf("Sending %.8f %s\n", cryptoAmount, c.currency)
	return &PaymentResult{
		Success:       true,
		TransactionID: fmt.Sprintf("CRYPTO_%d", time.Now().Unix()),
	}, nil
}

func (c *CryptoStrategy) convertToCrypto(usd float64) (float64, error) {
	// API call for conversion
	return usd / 50000, nil // Exemple simplifie
}

// PaymentProcessor is the context that uses a strategy.
type PaymentProcessor struct {
	strategy PaymentStrategy
}

// NewPaymentProcessor creates a new payment processor.
func NewPaymentProcessor(strategy PaymentStrategy) *PaymentProcessor {
	return &PaymentProcessor{strategy: strategy}
}

// SetStrategy changes the payment strategy.
func (p *PaymentProcessor) SetStrategy(strategy PaymentStrategy) {
	p.strategy = strategy
}

// Checkout processes a payment using the current strategy.
func (p *PaymentProcessor) Checkout(amount float64) (*PaymentResult, error) {
	if !p.strategy.Validate() {
		return &PaymentResult{
			Success: false,
			Error:   "Payment method validation failed",
		}, nil
	}
	return p.strategy.Pay(amount)
}
```

## Usage

```go
func main() {
	// Strategy selection at runtime
	processor:= NewPaymentProcessor(
		NewCreditCardStrategy("4111111111111111", "123", "12/25"),
	)

	// Change strategy dynamically
	processor.SetStrategy(NewPayPalStrategy("user@example.com"))

	// Process payment
	result, err:= processor.Checkout(100.00)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	fmt.Printf("Payment result: %+v\n", result)
}

// Factory function to create strategies
func CreatePaymentStrategy(method string, data map[string]string) (PaymentStrategy, error) {
	switch method {
	case "credit_card":
		return NewCreditCardStrategy(
			data["number"],
			data["cvv"],
			data["expiry"],
		), nil
	case "paypal":
		return NewPayPalStrategy(data["email"]), nil
	case "crypto":
		return NewCryptoStrategy(data["wallet"], data["currency"]), nil
	default:
		return nil, fmt.Errorf("unknown payment method: %s", method)
	}
}
```

## Variants

### Strategy with Functions

```go
// SortStrategy is a function type for sorting.
type SortStrategy[T any] func(items []T) []T

// QuickSort implements quick sort algorithm.
func QuickSort[T comparable](items []T) []T {
	if len(items) <= 1 {
		return items
	}
	// Simplified implementation
	return items
}

// MergeSort implements merge sort algorithm.
func MergeSort[T any](items []T) []T {
	if len(items) <= 1 {
		return items
	}
	// Simplified implementation
	return items
}

// Sorter uses a sorting strategy.
type Sorter[T any] struct {
	strategy SortStrategy[T]
}

// NewSorter creates a new sorter.
func NewSorter[T any](strategy SortStrategy[T]) *Sorter[T] {
	return &Sorter[T]{strategy: strategy}
}

// Sort sorts items using the current strategy.
func (s *Sorter[T]) Sort(items []T) []T {
	result:= make([]T, len(items))
	copy(result, items)
	return s.strategy(result)
}

// SetStrategy changes the sorting strategy.
func (s *Sorter[T]) SetStrategy(strategy SortStrategy[T]) {
	s.strategy = strategy
}

// Usage
func sortExample() {
	sorter:= NewSorter(QuickSort[int])
	result:= sorter.Sort([]int{3, 1, 4, 1, 5})
	fmt.Println(result)

	sorter.SetStrategy(MergeSort[int])
	result = sorter.Sort([]int{3, 1, 4, 1, 5})
	fmt.Println(result)
}
```

### Strategy with Map

```go
// CompressionStrategy defines compression operations.
type CompressionStrategy interface {
	Compress(data []byte) ([]byte, error)
	Decompress(data []byte) ([]byte, error)
}

// CompressionContext manages compression strategies.
type CompressionContext struct {
	strategies map[string]CompressionStrategy
}

// NewCompressionContext creates a new compression context.
func NewCompressionContext() *CompressionContext {
	return &CompressionContext{
		strategies: make(map[string]CompressionStrategy),
	}
}

// Register adds a compression strategy.
func (c *CompressionContext) Register(name string, strategy CompressionStrategy) {
	c.strategies[name] = strategy
}

// Compress compresses data using the specified algorithm.
func (c *CompressionContext) Compress(data []byte, algorithm string) ([]byte, error) {
	strategy, ok:= c.strategies[algorithm]
	if !ok {
		return nil, fmt.Errorf("unknown algorithm: %s", algorithm)
	}
	return strategy.Compress(data)
}

// Decompress decompresses data using the specified algorithm.
func (c *CompressionContext) Decompress(data []byte, algorithm string) ([]byte, error) {
	strategy, ok:= c.strategies[algorithm]
	if !ok {
		return nil, fmt.Errorf("unknown algorithm: %s", algorithm)
	}
	return strategy.Decompress(data)
}

// GzipStrategy implements gzip compression.
type GzipStrategy struct{}

func (g *GzipStrategy) Compress(data []byte) ([]byte, error) {
	// Implementation
	return data, nil
}

func (g *GzipStrategy) Decompress(data []byte) ([]byte, error) {
	// Implementation
	return data, nil
}

// BrotliStrategy implements brotli compression.
type BrotliStrategy struct{}

func (b *BrotliStrategy) Compress(data []byte) ([]byte, error) {
	// Implementation
	return data, nil
}

func (b *BrotliStrategy) Decompress(data []byte) ([]byte, error) {
	// Implementation
	return data, nil
}

// Usage
func compressionExample() {
	ctx:= NewCompressionContext()
	ctx.Register("gzip", &GzipStrategy{})
	ctx.Register("brotli", &BrotliStrategy{})

	data:= []byte("hello world")
	compressed, _:= ctx.Compress(data, "gzip")
	_ = compressed
}
```

### Strategy with Validation

```go
// ValidationResult represents validation result.
type ValidationResult struct {
	Valid  bool
	Errors []string
}

// ValidationStrategy defines validation operations.
type ValidationStrategy interface {
	Validate(value interface{}) *ValidationResult
}

// CompositeValidator combines multiple validators.
type CompositeValidator struct {
	strategies []ValidationStrategy
}

// NewCompositeValidator creates a new composite validator.
func NewCompositeValidator(strategies ...ValidationStrategy) *CompositeValidator {
	return &CompositeValidator{strategies: strategies}
}

// Validate runs all validation strategies.
func (c *CompositeValidator) Validate(value interface{}) *ValidationResult {
	errors:= []string{}

	for _, strategy:= range c.strategies {
		result:= strategy.Validate(value)
		if !result.Valid {
			errors = append(errors, result.Errors...)
		}
	}

	return &ValidationResult{
		Valid:  len(errors) == 0,
		Errors: errors,
	}
}

// RequiredValidator validates required fields.
type RequiredValidator struct {
	field string
}

// NewRequiredValidator creates a new required validator.
func NewRequiredValidator(field string) *RequiredValidator {
	return &RequiredValidator{field: field}
}

// Validate checks if field is present.
func (r *RequiredValidator) Validate(value interface{}) *ValidationResult {
	data, ok:= value.(map[string]interface{})
	if !ok {
		return &ValidationResult{Valid: false, Errors: []string{"invalid data type"}}
	}

	if _, exists:= data[r.field]; !exists {
		return &ValidationResult{
			Valid:  false,
			Errors: []string{fmt.Sprintf("%s is required", r.field)},
		}
	}

	return &ValidationResult{Valid: true, Errors: []string{}}
}

// EmailValidator validates email format.
type EmailValidator struct {
	field string
}

// NewEmailValidator creates a new email validator.
func NewEmailValidator(field string) *EmailValidator {
	return &EmailValidator{field: field}
}

// Validate checks if email is valid.
func (e *EmailValidator) Validate(value interface{}) *ValidationResult {
	data, ok:= value.(map[string]interface{})
	if !ok {
		return &ValidationResult{Valid: false, Errors: []string{"invalid data type"}}
	}

	email, ok:= data[e.field].(string)
	if !ok {
		return &ValidationResult{
			Valid:  false,
			Errors: []string{fmt.Sprintf("%s must be a string", e.field)},
		}
	}

	emailRegex:= regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)
	if !emailRegex.MatchString(email) {
		return &ValidationResult{
			Valid:  false,
			Errors: []string{fmt.Sprintf("%s is not a valid email", e.field)},
		}
	}

	return &ValidationResult{Valid: true, Errors: []string{}}
}

// Usage
func validationExample() {
	userValidator:= NewCompositeValidator(
		NewRequiredValidator("email"),
		NewEmailValidator("email"),
		NewRequiredValidator("password"),
	)

	result:= userValidator.Validate(map[string]interface{}{
		"email":    "invalid",
		"password": "",
	})
	fmt.Printf("Valid: %t, Errors: %v\n", result.Valid, result.Errors)
	// Valid: false, Errors: [email is not a valid email password is required]
}
```

## Anti-patterns

```go
// BAD: Strategy with shared state
type StatefulStrategy struct {
	lastTransaction string // Etat = problemes de concurrence
}

func (s *StatefulStrategy) Pay(amount float64) (*PaymentResult, error) {
	s.lastTransaction = fmt.Sprintf("TX_%d", time.Now().Unix())
	return &PaymentResult{
		Success:       true,
		TransactionID: s.lastTransaction,
	}, nil
}

// BAD: Context that knows the implementations
type BadContext struct{}

func (b *BadContext) Checkout(method string, amount float64) error {
	if method == "credit_card" {
		// Credit card specific logic
	} else if method == "paypal" {
		// PayPal specific logic
	}
	// Should use a strategy!
	return nil
}

// BAD: Strategy too granular
type TooGranularStrategy interface {
	Step1() error
	Step2() error
	Step3() error
	// If all steps are always executed together,
	// a single method is sufficient
}
```

## Unit Tests

```go
package main

import (
	"testing"
)

func TestCreditCardStrategy(t *testing.T) {
	t.Run("should validate correct card details", func(t *testing.T) {
		strategy:= NewCreditCardStrategy("4111111111111111", "123", "12/25")
		if !strategy.Validate() {
			t.Error("valid card should pass validation")
		}
	})

	t.Run("should reject invalid card number", func(t *testing.T) {
		strategy:= NewCreditCardStrategy("123", "123", "12/25")
		if strategy.Validate() {
			t.Error("invalid card should fail validation")
		}
	})

	t.Run("should process payment successfully", func(t *testing.T) {
		strategy:= NewCreditCardStrategy("4111111111111111", "123", "12/25")
		result, err:= strategy.Pay(100)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !result.Success {
			t.Error("payment should succeed")
		}

		if result.TransactionID == "" {
			t.Error("transaction ID should be set")
		}
	})
}

func TestPayPalStrategy(t *testing.T) {
	t.Run("should validate correct email", func(t *testing.T) {
		strategy:= NewPayPalStrategy("user@example.com")
		if !strategy.Validate() {
			t.Error("valid email should pass validation")
		}
	})

	t.Run("should reject invalid email", func(t *testing.T) {
		strategy:= NewPayPalStrategy("invalid-email")
		if strategy.Validate() {
			t.Error("invalid email should fail validation")
		}
	})
}

func TestPaymentProcessor(t *testing.T) {
	t.Run("should use the provided strategy", func(t *testing.T) {
		strategy:= NewCreditCardStrategy("4111111111111111", "123", "12/25")
		processor:= NewPaymentProcessor(strategy)

		result, err:= processor.Checkout(100)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !result.Success {
			t.Error("payment should succeed")
		}
	})

	t.Run("should allow strategy change at runtime", func(t *testing.T) {
		strategy1:= NewCreditCardStrategy("4111111111111111", "123", "12/25")
		processor:= NewPaymentProcessor(strategy1)

		result1, _:= processor.Checkout(100)
		if result1.TransactionID[:3] != "CC_" {
			t.Error("first payment should use credit card")
		}

		strategy2:= NewPayPalStrategy("user@example.com")
		processor.SetStrategy(strategy2)

		result2, _:= processor.Checkout(200)
		if result2.TransactionID[:3] != "PP_" {
			t.Error("second payment should use PayPal")
		}
	})

	t.Run("should fail if validation fails", func(t *testing.T) {
		strategy:= NewPayPalStrategy("invalid-email")
		processor:= NewPaymentProcessor(strategy)

		result, _:= processor.Checkout(100)

		if result.Success {
			t.Error("payment should fail with invalid email")
		}
	})
}
```

## When to Use

- Multiple variants of an algorithm
- Avoid multiple conditionals (switch/if-else)
- Families of related algorithms
- Algorithm must vary independently of the client

## Related Patterns

- **State**: Changes behavior based on state (implicit)
- **Template Method**: Fixed algorithm with variable steps
- **Command**: Encapsulates an action, not an algorithm

## Sources

- [Refactoring Guru - Strategy](https://refactoring.guru/design-patterns/strategy)
