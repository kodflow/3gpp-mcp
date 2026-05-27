# Test Doubles

> Substitution objects to isolate the code under test.

## Types of Test Doubles

```
┌─────────────────────────────────────────────────────────────────┐
│                      Test Doubles Spectrum                       │
│                                                                  │
│   Dummy ◄──── Stub ◄──── Spy ◄──── Mock ◄──── Fake             │
│     │          │          │          │          │               │
│   Placeholder  Returns    Records    Verifies   Working         │
│   (unused)     canned     calls      behavior   implementation  │
│                values                                            │
└─────────────────────────────────────────────────────────────────┘
```

| Type | Returns | Verifies | Behavior |
|------|---------|----------|----------|
| **Dummy** | Nothing | No | Fills a parameter |
| **Stub** | Fixed values | No | Indirect input control |
| **Spy** | Real values | Calls | Observes without replacing |
| **Mock** | Configured | Interactions | Verifies behavior |
| **Fake** | Real values | No | Simplified implementation |

## Dummy

```go
package calculator_test

import (
	"testing"
)

// Logger interface
type Logger interface {
	Log(message string)
	Error(message string)
	Warn(message string)
}

// DummyLogger - Placeholder for unused dependencies
type DummyLogger struct{}

func (d *DummyLogger) Log(message string)   {}
func (d *DummyLogger) Error(message string) {}
func (d *DummyLogger) Warn(message string)  {}

// Usage
func TestCalculateWithoutLogging(t *testing.T) {
	calc := NewCalculator(&DummyLogger{})
	result := calc.Add(2, 3)

	if result != 5 {
		t.Errorf("Add(2, 3) = %d; want 5", result)
	}
	// Logger is required by constructor but not relevant for this test
}
```

## Stub

```go
package profile_test

import (
	"context"
	"testing"
)

// User domain model
type User struct {
	ID    string
	Name  string
	Email string
}

// UserRepository interface
type UserRepository interface {
	FindByID(ctx context.Context, id string) (*User, error)
	FindByEmail(ctx context.Context, email string) (*User, error)
	Save(ctx context.Context, user *User) error
}

// StubUserRepository - Returns predefined values
type StubUserRepository struct {
	users []*User
}

func (s *StubUserRepository) SetUsers(users []*User) {
	s.users = users
}

func (s *StubUserRepository) FindByID(ctx context.Context, id string) (*User, error) {
	for _, u := range s.users {
		if u.ID == id {
			return u, nil
		}
	}
	return nil, nil
}

func (s *StubUserRepository) FindByEmail(ctx context.Context, email string) (*User, error) {
	for _, u := range s.users {
		if u.Email == email {
			return u, nil
		}
	}
	return nil, nil
}

func (s *StubUserRepository) Save(ctx context.Context, user *User) error {
	s.users = append(s.users, user)
	return nil
}

// Usage
func TestGetProfileWhenUserExists(t *testing.T) {
	stub := &StubUserRepository{}
	stub.SetUsers([]*User{
		{ID: "1", Name: "John", Email: "john@test.com"},
	})

	service := NewProfileService(stub)
	profile, err := service.GetProfile(context.Background(), "1")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if profile.Name != "John" {
		t.Errorf("profile.Name = %q; want %q", profile.Name, "John")
	}
}

// testify/mock alternative
import (
	"github.com/stretchr/testify/mock"
)

type MockUserRepository struct {
	mock.Mock
}

func (m *MockUserRepository) FindByID(ctx context.Context, id string) (*User, error) {
	args := m.Called(ctx, id)
	if user := args.Get(0); user != nil {
		return user.(*User), args.Error(1)
	}
	return nil, args.Error(1)
}

func TestWithTestifyMock(t *testing.T) {
	repo := new(MockUserRepository)
	repo.On("FindByID", mock.Anything, "1").Return(&User{ID: "1", Name: "John"}, nil)

	service := NewProfileService(repo)
	profile, _ := service.GetProfile(context.Background(), "1")

	if profile.Name != "John" {
		t.Errorf("profile.Name = %q; want %q", profile.Name, "John")
	}
}
```

## Spy

```go
package profile_test

import (
	"context"
	"testing"
)

// SpyUserRepository - Records calls while using real implementation
type SpyUserRepository struct {
	RealRepo       UserRepository
	FindByIDCalls  []string
	CallCount      int
}

func (s *SpyUserRepository) FindByID(ctx context.Context, id string) (*User, error) {
	s.FindByIDCalls = append(s.FindByIDCalls, id)
	s.CallCount++
	return s.RealRepo.FindByID(ctx, id)
}

func (s *SpyUserRepository) FindByEmail(ctx context.Context, email string) (*User, error) {
	return s.RealRepo.FindByEmail(ctx, email)
}

func (s *SpyUserRepository) Save(ctx context.Context, user *User) error {
	return s.RealRepo.Save(ctx, user)
}

// Usage
func TestCallsRepositoryWithCorrectID(t *testing.T) {
	realRepo := &StubUserRepository{}
	realRepo.SetUsers([]*User{{ID: "123", Name: "John"}})

	spy := &SpyUserRepository{RealRepo: realRepo}
	service := NewProfileService(spy)

	_, _ = service.GetProfile(context.Background(), "123")

	if spy.CallCount != 1 {
		t.Errorf("CallCount = %d; want 1", spy.CallCount)
	}
	if len(spy.FindByIDCalls) != 1 || spy.FindByIDCalls[0] != "123" {
		t.Errorf("FindByIDCalls = %v; want [\"123\"]", spy.FindByIDCalls)
	}
}

// testify/mock spy alternative
func TestWithTestifySpy(t *testing.T) {
	repo := new(MockUserRepository)
	repo.On("FindByID", mock.Anything, "123").Return(&User{ID: "123", Name: "John"}, nil)

	service := NewProfileService(repo)
	_, _ = service.GetProfile(context.Background(), "123")

	repo.AssertCalled(t, "FindByID", mock.Anything, "123")
	repo.AssertNumberOfCalls(t, "FindByID", 1)
}
```

## Mock

```go
package user_test

import (
	"context"
	"errors"
	"testing"
)

// EmailService interface
type EmailService interface {
	Send(ctx context.Context, to, subject, body string) error
}

// MockEmailService - Verifies interactions
type MockEmailService struct {
	expectations []expectation
	t            *testing.T
}

type expectation struct {
	method string
	args   []interface{}
	called bool
}

func NewMockEmailService(t *testing.T) *MockEmailService {
	return &MockEmailService{t: t}
}

func (m *MockEmailService) ExpectSend(to, subject string) *MockEmailService {
	m.expectations = append(m.expectations, expectation{
		method: "Send",
		args:   []interface{}{to, subject},
		called: false,
	})
	return m
}

func (m *MockEmailService) Send(ctx context.Context, to, subject, body string) error {
	for i := range m.expectations {
		exp := &m.expectations[i]
		if exp.method == "Send" && !exp.called {
			exp.called = true
			return nil
		}
	}
	return nil
}

func (m *MockEmailService) Verify() {
	for _, exp := range m.expectations {
		if !exp.called {
			m.t.Errorf("Unmet expectation: %s with args %v", exp.method, exp.args)
		}
	}
}

// Usage
func TestSendWelcomeEmailOnRegistration(t *testing.T) {
	mockEmail := NewMockEmailService(t)
	mockEmail.ExpectSend("user@test.com", "Welcome!")

	service := NewUserService(mockEmail)
	err := service.Register(context.Background(), &User{Email: "user@test.com", Name: "John"})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mockEmail.Verify() // Throws if expectation not met
}

// testify/mock alternative
func TestWithTestifyMock(t *testing.T) {
	mockEmail := new(MockEmailService)
	mockEmail.On("Send", mock.Anything, "user@test.com", "Welcome!", mock.AnythingOfType("string")).
		Return(nil)

	service := NewUserService(mockEmail)
	_ = service.Register(context.Background(), &User{Email: "user@test.com", Name: "John"})

	mockEmail.AssertExpectations(t)
}
```

## Fake

```go
package user_test

import (
	"context"
	"sync"
	"testing"
)

// FakeUserRepository - Simplified working implementation
type FakeUserRepository struct {
	mu         sync.RWMutex
	users      map[string]*User
	emailIndex map[string]string
}

func NewFakeUserRepository() *FakeUserRepository {
	return &FakeUserRepository{
		users:      make(map[string]*User),
		emailIndex: make(map[string]string),
	}
}

func (f *FakeUserRepository) FindByID(ctx context.Context, id string) (*User, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	user, exists := f.users[id]
	if !exists {
		return nil, nil
	}
	// Return copy to avoid mutation
	userCopy := *user
	return &userCopy, nil
}

func (f *FakeUserRepository) FindByEmail(ctx context.Context, email string) (*User, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	id, exists := f.emailIndex[email]
	if !exists {
		return nil, nil
	}
	user := f.users[id]
	userCopy := *user
	return &userCopy, nil
}

func (f *FakeUserRepository) Save(ctx context.Context, user *User) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	userCopy := *user
	f.users[user.ID] = &userCopy
	f.emailIndex[user.Email] = user.ID
	return nil
}

func (f *FakeUserRepository) Delete(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	user, exists := f.users[id]
	if exists {
		delete(f.emailIndex, user.Email)
		delete(f.users, id)
	}
	return nil
}

// Test helpers
func (f *FakeUserRepository) Clear() {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.users = make(map[string]*User)
	f.emailIndex = make(map[string]string)
}

func (f *FakeUserRepository) Seed(users []*User) error {
	for _, u := range users {
		if err := f.Save(context.Background(), u); err != nil {
			return err
		}
	}
	return nil
}

// Usage - Fake behaves like real implementation
func TestCreateAndRetrieveUser(t *testing.T) {
	fake := NewFakeUserRepository()
	service := NewUserService(fake)

	err := service.CreateUser(context.Background(), &User{ID: "1", Email: "test@test.com", Name: "Test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	user, err := service.GetUser(context.Background(), "1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if user.Email != "test@test.com" {
		t.Errorf("user.Email = %q; want %q", user.Email, "test@test.com")
	}
}
```

## Comparison with testify

```go
package order_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// Complete example with all types
func TestOrderService(t *testing.T) {
	t.Run("should log order creation", func(t *testing.T) {
		stubInventory := new(MockInventoryService)
		stubInventory.On("CheckStock", mock.Anything, "1").Return(true, nil)
		stubInventory.On("Reserve", mock.Anything, "1", 1).Return("reservation-123", nil)

		var loggedMessages []string
		spyLogger := func(msg string) {
			loggedMessages = append(loggedMessages, msg)
		}

		service := NewOrderService(stubInventory, spyLogger)
		_, err := service.CreateOrder(context.Background(), &OrderRequest{ProductID: "1", Quantity: 1})

		assert.NoError(t, err)
		assert.Contains(t, loggedMessages[0], "Order created")
	})

	t.Run("should send confirmation email", func(t *testing.T) {
		mockEmail := new(MockEmailService)
		mockEmail.On("Send", mock.Anything, mock.MatchedBy(func(req EmailRequest) bool {
			return req.Type == "order_confirmation"
		})).Return(nil)

		service := NewOrderServiceWithEmail(mockEmail)
		_, err := service.CreateOrder(context.Background(), &OrderRequest{ProductID: "1", Quantity: 1})

		assert.NoError(t, err)
		mockEmail.AssertExpectations(t)
	})

	t.Run("integration with fake repository", func(t *testing.T) {
		fakeRepo := NewFakeOrderRepository()
		service := NewOrderServiceWithRepo(fakeRepo)

		orderID, err := service.CreateOrder(context.Background(), &OrderRequest{ProductID: "1", Quantity: 1})
		assert.NoError(t, err)

		order, err := service.GetOrder(context.Background(), orderID)
		assert.NoError(t, err)
		assert.Equal(t, "pending", order.Status)
	})
}
```

## Recommended Libraries

| Package | Usage |
|---------|-------|
| `testing` | Built-in testing package |
| `github.com/stretchr/testify/mock` | Mocking framework |
| `github.com/stretchr/testify/assert` | Assertion helpers |
| `github.com/golang/mock/gomock` | Code generation for mocks |
| `github.com/maxbrunsfeld/counterfeiter/v6` | Interface fake generator |

## Common Mistakes

| Mistake | Impact | Solution |
|---------|--------|----------|
| Too many mocks | Fragile tests | Prefer fakes for integration |
| Mock implementation details | Tight coupling | Mock interfaces, not implementations |
| Forgetting verify() | False positives | Always verify expectations |
| Stub without assertion | Useless test | Verify the result |
| Overly complex fake | Heavy maintenance | Keep simple, no bugs |

## When to Use

| Situation | Test Double |
|-----------|-------------|
| Required unused parameter | Dummy |
| Control input data | Stub |
| Observe without modifying | Spy |
| Verify interactions | Mock |
| Integration with state | Fake |

## Related Patterns

- **Fixture**: Test doubles setup
- **Object Mother**: Factory for configured test doubles
- **Dependency Injection**: Enables easy substitution

## Sources

- [xUnit Test Patterns - Gerard Meszaros](http://xunitpatterns.com/)
- [Mocks Aren't Stubs - Martin Fowler](https://martinfowler.com/articles/mocksArentStubs.html)
