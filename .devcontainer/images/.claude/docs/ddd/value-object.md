# Value Object Pattern

> Immutable domain object defined entirely by its attributes, with no conceptual identity.

## Definition

A **Value Object** is an immutable domain object defined entirely by its attributes, with no conceptual identity. Two Value Objects with the same attributes are considered equal.

```
Value Object = Attributes + Immutability + Equality by Value + Self-Validation
```

**Key characteristics:**

- **Immutability**: Cannot be changed after creation
- **Equality**: Compared by attribute values, not reference
- **Self-Validation**: Always valid after construction
- **Side-effect free**: Operations return new instances
- **Replaceability**: Can be freely substituted when equal

## Go Implementation

```go
package domain

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Email is a value object representing an email address.
type Email struct {
	value string
}

var emailRegex = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

// NewEmail creates a validated Email value object.
func NewEmail(value string) (Email, error) {
	if value == "" {
		return Email{}, errors.New("email cannot be empty")
	}

	normalized := strings.ToLower(strings.TrimSpace(value))

	if !emailRegex.MatchString(normalized) {
		return Email{}, errors.New("invalid email format")
	}

	return Email{value: normalized}, nil
}

// Value returns the email value.
func (e Email) Value() string {
	return e.value
}

// Domain returns the domain part of the email.
func (e Email) Domain() string {
	parts := strings.Split(e.value, "@")
	if len(parts) == 2 {
		return parts[1]
	}
	return ""
}

// ChangeDomain returns a new Email with a different domain.
func (e Email) ChangeDomain(newDomain string) (Email, error) {
	localPart := strings.Split(e.value, "@")[0]
	return NewEmail(fmt.Sprintf("%s@%s", localPart, newDomain))
}

// Equals compares two Email value objects by value.
func (e Email) Equals(other Email) bool {
	return e.value == other.value
}

// Money is a value object representing monetary amounts.
type Money struct {
	amount   float64
	currency Currency
}

// NewMoney creates a validated Money value object.
func NewMoney(amount float64, currency Currency) (Money, error) {
	if !isFinite(amount) {
		return Money{}, errors.New("amount must be a finite number")
	}

	// Round to currency precision
	precision := currency.DecimalPlaces()
	multiplier := math.Pow(10, float64(precision))
	rounded := math.Round(amount*multiplier) / multiplier

	return Money{
		amount:   rounded,
		currency: currency,
	}, nil
}

// Zero creates a zero Money value for the given currency.
func Zero(currency Currency) Money {
	return Money{amount: 0, currency: currency}
}

// Amount returns the monetary amount.
func (m Money) Amount() float64 {
	return m.amount
}

// Currency returns the currency.
func (m Money) Currency() Currency {
	return m.currency
}

// Add returns a new Money with the sum of two amounts.
func (m Money) Add(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, errors.New("cannot add different currencies")
	}
	return NewMoney(m.amount+other.amount, m.currency)
}

// Subtract returns a new Money with the difference.
func (m Money) Subtract(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, errors.New("cannot subtract different currencies")
	}
	return NewMoney(m.amount-other.amount, m.currency)
}

// Multiply returns a new Money multiplied by a factor.
func (m Money) Multiply(factor float64) (Money, error) {
	return NewMoney(m.amount*factor, m.currency)
}

// IsPositive checks if amount is positive.
func (m Money) IsPositive() bool {
	return m.amount > 0
}

// IsNegative checks if amount is negative.
func (m Money) IsNegative() bool {
	return m.amount < 0
}

// IsZero checks if amount is zero.
func (m Money) IsZero() bool {
	return m.amount == 0
}

// Equals compares two Money value objects.
func (m Money) Equals(other Money) bool {
	return m.amount == other.amount && m.currency == other.currency
}

// Address is a composite value object.
type Address struct {
	street     string
	city       string
	postalCode string
	country    Country
}

// NewAddress creates a validated Address value object.
func NewAddress(street, city, postalCode string, country Country) (Address, error) {
	var errs []string

	if strings.TrimSpace(street) == "" {
		errs = append(errs, "street is required")
	}
	if strings.TrimSpace(city) == "" {
		errs = append(errs, "city is required")
	}
	if strings.TrimSpace(postalCode) == "" {
		errs = append(errs, "postal code is required")
	}
	if !country.ValidatePostalCode(postalCode) {
		errs = append(errs, "invalid postal code for country")
	}

	if len(errs) > 0 {
		return Address{}, errors.New(strings.Join(errs, "; "))
	}

	return Address{
		street:     strings.TrimSpace(street),
		city:       strings.TrimSpace(city),
		postalCode: strings.TrimSpace(postalCode),
		country:    country,
	}, nil
}

// Street returns the street.
func (a Address) Street() string {
	return a.street
}

// City returns the city.
func (a Address) City() string {
	return a.city
}

// PostalCode returns the postal code.
func (a Address) PostalCode() string {
	return a.postalCode
}

// Country returns the country.
func (a Address) Country() Country {
	return a.country
}

// Format returns a formatted address string.
func (a Address) Format() string {
	return fmt.Sprintf("%s, %s %s, %s",
		a.street, a.city, a.postalCode, a.country.Name())
}

// Equals compares two Address value objects.
func (a Address) Equals(other Address) bool {
	return a.street == other.street &&
		a.city == other.city &&
		a.postalCode == other.postalCode &&
		a.country.Equals(other.country)
}

func isFinite(f float64) bool {
	return !math.IsInf(f, 0) && !math.IsNaN(f)
}
```

## OOP vs FP Comparison

| Aspect | OOP Value Object | FP Value Object |
|--------|-----------------|-----------------|
| Structure | Class with private constructor | Branded type or newtype |
| Validation | Factory method | Smart constructor |
| Operations | Instance methods | Pure functions |
| Composition | Inheritance | Type composition |

```go
// FP-style Value Object using functional patterns

// Email is a branded type (string with compile-time guarantees).
type Email string

// CreateEmail is a smart constructor that validates.
func CreateEmail(value string) (Email, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if !emailRegex.MatchString(normalized) {
		return "", errors.New("invalid email format")
	}
	return Email(normalized), nil
}

// Money with structural equality
type Money struct {
	Amount   float64
	Currency string
}

// Add is a pure function returning a new Money.
func AddMoney(m1, m2 Money) (Money, error) {
	if m1.Currency != m2.Currency {
		return Money{}, errors.New("currency mismatch")
	}
	return Money{
		Amount:   m1.Amount + m2.Amount,
		Currency: m1.Currency,
	}, nil
}

// Equality is automatic with struct comparison
func MoneyEqual(m1, m2 Money) bool {
	return m1 == m2 // Structural equality
}
```

## Recommended Libraries

| Library | Purpose | Link |
|---------|---------|------|
| **google/uuid** | ID generation | `go get github.com/google/uuid` |
| **shopspring/decimal** | Precise decimal math | `go get github.com/shopspring/decimal` |
| **go-playground/validator** | Struct validation | `go get github.com/go-playground/validator/v10` |

## Anti-patterns

1. **Mutable Value Object**: Adding setters breaks immutability

   ```go
   // BAD
   type Email struct {
       value string
   }

   func (e *Email) SetValue(v string) { // Mutation!
       e.value = v
   }
   ```

2. **Invalid Construction**: Allowing invalid state

   ```go
   // BAD - No validation
   email := Email{value: "not-an-email"}

   // GOOD - Factory with validation
   email, err := NewEmail("user@example.com")
   if err != nil {
       // Handle error
   }
   ```

3. **Primitive Obsession**: Using primitives instead of Value Objects

   ```go
   // BAD
   func SendEmail(to string, amount float64, currency string) {}

   // GOOD
   func SendEmail(to Email, amount Money) error {}
   ```

4. **Missing Equality**: Not implementing proper equality

   ```go
   // BAD - Pointer comparison
   &email1 == &email2

   // GOOD - Value comparison
   email1.Equals(email2)
   ```

## When to Use

- Attribute combinations that appear together (Email, Money, Address)
- Concepts defined by their values, not their identity
- Measures, quantities, descriptors
- When you need immutability guarantees

## Related Patterns

- [Entity](./entity.md) - For objects with identity
- [Aggregate](./aggregate.md) - Contains Value Objects
- [Specification](./specification.md) - Uses Value Objects for rules
