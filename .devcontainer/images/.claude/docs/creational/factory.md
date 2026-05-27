# Factory Patterns

> Delegate object creation to specialized methods or classes.

## Factory Method

### Intention

Define an interface for creating an object, but let subclasses
decide which class to instantiate.

### Structure

```go
package main

import (
	"context"
	"fmt"
)

// 1. Product interface
type Notification interface {
	Send(ctx context.Context, message string) error
}

// 2. Concrete products
type EmailNotification struct {
	email string
}

// NewEmailNotification creates an email notification.
func NewEmailNotification(email string) *EmailNotification {
	return &EmailNotification{email: email}
}

func (n *EmailNotification) Send(ctx context.Context, message string) error {
	fmt.Printf("Email to %s: %s\n", n.email, message)
	return nil
}

type SMSNotification struct {
	phone string
}

// NewSMSNotification creates an SMS notification.
func NewSMSNotification(phone string) *SMSNotification {
	return &SMSNotification{phone: phone}
}

func (n *SMSNotification) Send(ctx context.Context, message string) error {
	fmt.Printf("SMS to %s: %s\n", n.phone, message)
	return nil
}

type PushNotification struct {
	deviceID string
}

// NewPushNotification creates a push notification.
func NewPushNotification(deviceID string) *PushNotification {
	return &PushNotification{deviceID: deviceID}
}

func (n *PushNotification) Send(ctx context.Context, message string) error {
	fmt.Printf("Push to %s: %s\n", n.deviceID, message)
	return nil
}

// 3. Factory interface
type NotificationFactory interface {
	CreateNotification(recipient string) Notification
	Notify(ctx context.Context, recipient, message string) error
}

// 4. Base factory with template method
type baseFactory struct{}

func (f *baseFactory) Notify(ctx context.Context, factory NotificationFactory, recipient, message string) error {
	notification := factory.CreateNotification(recipient)
	return notification.Send(ctx, message)
}

// 5. Concrete factories
type EmailNotificationFactory struct {
	baseFactory
}

func (f *EmailNotificationFactory) CreateNotification(email string) Notification {
	return NewEmailNotification(email)
}

func (f *EmailNotificationFactory) Notify(ctx context.Context, recipient, message string) error {
	return f.baseFactory.Notify(ctx, f, recipient, message)
}

type SMSNotificationFactory struct {
	baseFactory
}

func (f *SMSNotificationFactory) CreateNotification(phone string) Notification {
	return NewSMSNotification(phone)
}

func (f *SMSNotificationFactory) Notify(ctx context.Context, recipient, message string) error {
	return f.baseFactory.Notify(ctx, f, recipient, message)
}
```

## Abstract Factory

### Intention (Abstract Factory)

Provide an interface for creating families of related objects without specifying
their concrete classes.

### Structure (Abstract Factory)

```go
package main

import "fmt"

// 1. Product interfaces
type Button interface {
	Render() string
	OnClick(handler func())
}

type Input interface {
	Render() string
	GetValue() string
}

type Modal interface {
	Open()
	Close()
}

// 2. Abstract Factory interface
type UIFactory interface {
	CreateButton(label string) Button
	CreateInput(placeholder string) Input
	CreateModal(title string) Modal
}

// 3. Material Design family
type MaterialButton struct {
	label   string
	handler func()
}

func (b *MaterialButton) Render() string {
	return fmt.Sprintf("<md-button>%s</md-button>", b.label)
}

func (b *MaterialButton) OnClick(handler func()) {
	b.handler = handler
}

type MaterialInput struct {
	placeholder string
	value       string
}

func (i *MaterialInput) Render() string {
	return fmt.Sprintf(`<md-input placeholder="%s">`, i.placeholder)
}

func (i *MaterialInput) GetValue() string {
	return i.value
}

type MaterialModal struct {
	title string
}

func (m *MaterialModal) Open() {
	fmt.Printf("Opening Material modal: %s\n", m.title)
}

func (m *MaterialModal) Close() {
	fmt.Println("Closing Material modal")
}

type MaterialUIFactory struct{}

func (f *MaterialUIFactory) CreateButton(label string) Button {
	return &MaterialButton{label: label}
}

func (f *MaterialUIFactory) CreateInput(placeholder string) Input {
	return &MaterialInput{placeholder: placeholder}
}

func (f *MaterialUIFactory) CreateModal(title string) Modal {
	return &MaterialModal{title: title}
}

// 4. Bootstrap family
type BootstrapButton struct {
	label   string
	handler func()
}

func (b *BootstrapButton) Render() string {
	return fmt.Sprintf(`<button class="btn">%s</button>`, b.label)
}

func (b *BootstrapButton) OnClick(handler func()) {
	b.handler = handler
}

type BootstrapInput struct {
	placeholder string
	value       string
}

func (i *BootstrapInput) Render() string {
	return fmt.Sprintf(`<input class="form-control" placeholder="%s">`, i.placeholder)
}

func (i *BootstrapInput) GetValue() string {
	return i.value
}

type BootstrapModal struct {
	title string
}

func (m *BootstrapModal) Open() {
	fmt.Printf("Opening Bootstrap modal: %s\n", m.title)
}

func (m *BootstrapModal) Close() {
	fmt.Println("Closing Bootstrap modal")
}

type BootstrapUIFactory struct{}

func (f *BootstrapUIFactory) CreateButton(label string) Button {
	return &BootstrapButton{label: label}
}

func (f *BootstrapUIFactory) CreateInput(placeholder string) Input {
	return &BootstrapInput{placeholder: placeholder}
}

func (f *BootstrapUIFactory) CreateModal(title string) Modal {
	return &BootstrapModal{title: title}
}
```

## Simple Factory (non-GoF but common)

```go
package main

import (
	"errors"
	"fmt"
)

type NotificationType string

const (
	NotificationEmail NotificationType = "email"
	NotificationSMS   NotificationType = "sms"
	NotificationPush  NotificationType = "push"
)

// CreateNotification creates a notification based on type.
func CreateNotification(notifType NotificationType, recipient string) (Notification, error) {
	switch notifType {
	case NotificationEmail:
		return NewEmailNotification(recipient), nil
	case NotificationSMS:
		return NewSMSNotification(recipient), nil
	case NotificationPush:
		return NewPushNotification(recipient), nil
	default:
		return nil, fmt.Errorf("unknown notification type: %s", notifType)
	}
}

// Usage
func ExampleSimpleFactory() {
	notification, err := CreateNotification(NotificationEmail, "user@example.com")
	if err != nil {
		panic(err)
	}
	_ = notification
}
```

## Modern Variants

### Factory with Registry

```go
package main

import (
	"errors"
	"fmt"
	"sync"
)

// Creator defines a creation function.
type Creator func(...interface{}) Notification

// NotificationRegistry manages a registry of creators.
type NotificationRegistry struct {
	mu       sync.RWMutex
	creators map[string]Creator
}

// NewNotificationRegistry creates a new registry.
func NewNotificationRegistry() *NotificationRegistry {
	return &NotificationRegistry{
		creators: make(map[string]Creator),
	}
}

// Register registers a creator for a given type.
func (r *NotificationRegistry) Register(notifType string, creator Creator) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.creators[notifType] = creator
}

// Create creates a notification based on the registered type.
func (r *NotificationRegistry) Create(notifType string, args ...interface{}) (Notification, error) {
	r.mu.RLock()
	creator, exists := r.creators[notifType]
	r.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("unknown type: %s", notifType)
	}
	return creator(args...), nil
}

// Usage
func ExampleRegistry() {
	registry := NewNotificationRegistry()

	registry.Register("email", func(args ...interface{}) Notification {
		return NewEmailNotification(args[0].(string))
	})

	registry.Register("sms", func(args ...interface{}) Notification {
		return NewSMSNotification(args[0].(string))
	})

	notification, err := registry.Create("email", "user@example.com")
	if err != nil {
		panic(err)
	}
	_ = notification
}
```

### Factory with Dependency Injection

```go
package main

import "context"

// NotificationConfig configures notification creation.
type NotificationConfig struct {
	Type      NotificationType
	Recipient string
}

// NotificationService manages injected factories.
type NotificationService struct {
	emailFactory func(string) Notification
	smsFactory   func(string) Notification
	pushFactory  func(string) Notification
}

// NewNotificationService creates a service with DI.
func NewNotificationService(
	emailFactory func(string) Notification,
	smsFactory func(string) Notification,
	pushFactory func(string) Notification,
) *NotificationService {
	return &NotificationService{
		emailFactory: emailFactory,
		smsFactory:   smsFactory,
		pushFactory:  pushFactory,
	}
}

// Create creates a notification based on config.
func (s *NotificationService) Create(config NotificationConfig) (Notification, error) {
	switch config.Type {
	case NotificationEmail:
		return s.emailFactory(config.Recipient), nil
	case NotificationSMS:
		return s.smsFactory(config.Recipient), nil
	case NotificationPush:
		return s.pushFactory(config.Recipient), nil
	default:
		return nil, errors.New("unknown notification type")
	}
}
```

## Anti-patterns

```go
// BAD: Factory with too many responsibilities
type GodFactory struct{}

func (f *GodFactory) CreateUser() interface{}         { return nil }
func (f *GodFactory) CreateOrder() interface{}        { return nil }
func (f *GodFactory) CreateNotification() interface{} { return nil }
// Violates SRP

// BAD: Business logic in the factory
func BadCreateNotification(notifType string) Notification {
	notification := NewEmailNotification("")
	// No! This is business logic
	// notification.Validate()
	// notification.Save()
	return notification
}

// BAD: Factory that returns interface{} without type
func UnsafeCreate(notifType string) interface{} {
	// Loss of type safety
	return NewEmailNotification("")
}
```

## Modern Alternative: Functions

```go
package main

import "context"

// Factory functions (simpler, same result)
func createEmailNotification(email string) Notification {
	return NewEmailNotification(email)
}

func createSMSNotification(phone string) Notification {
	return NewSMSNotification(phone)
}

// NotificationOptions configures notification options.
type NotificationOptions struct {
	Retries int
	Timeout int
}

// CreateNotificationWithOptions creates a notification with options.
func CreateNotificationWithOptions(
	notifType NotificationType,
	recipient string,
	opts NotificationOptions,
) (Notification, error) {
	creators := map[NotificationType]func(string) Notification{
		NotificationEmail: createEmailNotification,
		NotificationSMS:   createSMSNotification,
		NotificationPush:  func(id string) Notification { return NewPushNotification(id) },
	}

	creator, exists := creators[notifType]
	if !exists {
		return nil, errors.New("unknown notification type")
	}
	return creator(recipient), nil
}
```

## Unit Tests

```go
package main

import (
	"context"
	"testing"
)

func TestEmailNotificationFactory_CreateNotification(t *testing.T) {
	factory := &EmailNotificationFactory{}
	notification := factory.CreateNotification("test@example.com")

	if notification == nil {
		t.Fatal("expected notification, got nil")
	}

	if _, ok := notification.(*EmailNotification); !ok {
		t.Errorf("expected *EmailNotification, got %T", notification)
	}
}

func TestNotificationFactory_Notify(t *testing.T) {
	factory := &SMSNotificationFactory{}
	ctx := context.Background()

	err := factory.Notify(ctx, "+1234567890", "Hello")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNotificationRegistry(t *testing.T) {
	registry := NewNotificationRegistry()

	registry.Register("webhook", func(args ...interface{}) Notification {
		return NewPushNotification(args[0].(string))
	})

	notification, err := registry.Create("webhook", "https://example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if notification == nil {
		t.Error("expected notification, got nil")
	}
}

func TestNotificationRegistry_UnknownType(t *testing.T) {
	registry := NewNotificationRegistry()

	_, err := registry.Create("unknown")
	if err == nil {
		t.Error("expected error for unknown type")
	}
}

func TestUIFactory_CreateConsistentFamily(t *testing.T) {
	factory := &MaterialUIFactory{}

	button := factory.CreateButton("Click")
	input := factory.CreateInput("Type here")

	if button == nil || input == nil {
		t.Fatal("expected UI components, got nil")
	}

	buttonHTML := button.Render()
	inputHTML := input.Render()

	if buttonHTML == "" || inputHTML == "" {
		t.Error("expected rendered HTML")
	}
}
```

## When to Use

### Choose Factory Method

- Creation delegated to subclasses
- Single product with variants

### Choose Abstract Factory

- Families of coherent objects
- Platform/theme independence

### Simple Factory

- Centralized creation logic
- No need for extensibility through inheritance

## Related Patterns

- **Builder**: Complex construction vs type selection
- **Prototype**: Cloning vs instantiation
- **Singleton**: Often combined with Factory

## Sources

- [Refactoring Guru - Factory Method](https://refactoring.guru/design-patterns/factory-method)
- [Refactoring Guru - Abstract Factory](https://refactoring.guru/design-patterns/abstract-factory)
