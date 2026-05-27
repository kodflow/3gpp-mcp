# Bridge

> Decouple an abstraction from its implementation so that they can vary independently.

---

## Principle

The Bridge pattern separates a large class into two separate hierarchies -
abstraction and implementation - that evolve independently.

```text
┌─────────────────┐           ┌─────────────────┐
│   Abstraction   │───────────│  Implementor    │
│  (operation())  │           │  (operationImpl)│
└────────┬────────┘           └────────┬────────┘
         │                             │
┌────────┴────────┐           ┌────────┴────────┐
│RefinedAbstraction│          │ConcreteImpl A/B │
└─────────────────┘           └─────────────────┘
```

---

## Problem Solved

- Combinatorial explosion of subclasses (e.g. Shape x Color x Platform)
- Strong coupling between abstraction and implementation
- Need to extend in two independent dimensions
- Runtime implementation switching

---

## Solution

```go
package main

import "fmt"

// Implementor defines the implementation interface.
type Renderer interface {
    RenderCircle(radius float64)
    RenderSquare(side float64)
}

// Abstraction defines the high-level interface.
type Shape interface {
    Draw()
}

// Circle is a refined abstraction.
type Circle struct {
    renderer Renderer
    radius   float64
}

func NewCircle(renderer Renderer, radius float64) *Circle {
    return &Circle{renderer: renderer, radius: radius}
}

func (c *Circle) Draw() {
    c.renderer.RenderCircle(c.radius)
}

// Concrete implementations
type VectorRenderer struct{}

func (v *VectorRenderer) RenderCircle(radius float64) {
    fmt.Printf("Drawing circle with radius %.2f as vectors\n", radius)
}

func (v *VectorRenderer) RenderSquare(side float64) {
    fmt.Printf("Drawing square with side %.2f as vectors\n", side)
}

type RasterRenderer struct{}

func (r *RasterRenderer) RenderCircle(radius float64) {
    fmt.Printf("Drawing circle with radius %.2f as pixels\n", radius)
}

func (r *RasterRenderer) RenderSquare(side float64) {
    fmt.Printf("Drawing square with side %.2f as pixels\n", side)
}

// Usage:
// renderer := &VectorRenderer{}
// circle := NewCircle(renderer, 5)
// circle.Draw()
```

---

## Complete Example

```go
package main

import (
    "fmt"
    "io"
    "os"
)

// MessageSender is the Implementor.
type MessageSender interface {
    Send(message string) error
}

// Message is the Abstraction.
type Message struct {
    sender  MessageSender
    content string
}

func NewMessage(sender MessageSender, content string) *Message {
    return &Message{sender: sender, content: content}
}

func (m *Message) Send() error {
    return m.sender.Send(m.content)
}

// UrgentMessage is a refined abstraction.
type UrgentMessage struct {
    *Message
    priority int
}

func NewUrgentMessage(
    sender MessageSender, content string, priority int,
) *UrgentMessage {
    return &UrgentMessage{
        Message:  NewMessage(sender, content),
        priority: priority,
    }
}

func (u *UrgentMessage) Send() error {
    urgentContent := fmt.Sprintf("[URGENT P%d] %s", u.priority, u.content)
    return u.sender.Send(urgentContent)
}

// EmailSender is a concrete implementation.
type EmailSender struct {
    to   string
    from string
}

func NewEmailSender(from, to string) *EmailSender {
    return &EmailSender{from: from, to: to}
}

func (e *EmailSender) Send(message string) error {
    fmt.Printf("Email from %s to %s: %s\n", e.from, e.to, message)
    return nil
}

// SMSSender is a concrete implementation.
type SMSSender struct {
    phone string
}

func NewSMSSender(phone string) *SMSSender {
    return &SMSSender{phone: phone}
}

func (s *SMSSender) Send(message string) error {
    fmt.Printf("SMS to %s: %s\n", s.phone, message)
    return nil
}

// SlackSender is a concrete implementation.
type SlackSender struct {
    channel string
    webhook string
}

func NewSlackSender(channel, webhook string) *SlackSender {
    return &SlackSender{channel: channel, webhook: webhook}
}

func (s *SlackSender) Send(message string) error {
    fmt.Printf("Slack #%s: %s\n", s.channel, message)
    return nil
}

func main() {
    // Combine different abstractions with implementations
    emailSender := NewEmailSender("system@example.com", "user@example.com")
    smsSender := NewSMSSender("+1234567890")
    slackSender := NewSlackSender("alerts", "https://hooks.slack.com/...")

    // Normal message via email
    msg1 := NewMessage(emailSender, "Your report is ready")
    msg1.Send()

    // Urgent message via SMS
    msg2 := NewUrgentMessage(smsSender, "Server is down!", 1)
    msg2.Send()

    // Normal message via Slack
    msg3 := NewMessage(slackSender, "Deployment completed")
    msg3.Send()

    // Output:
    // Email from system@example.com to user@example.com: Your report is ready
    // SMS to +1234567890: [URGENT P1] Server is down!
    // Slack #alerts: Deployment completed
}
```

---

## Variants

| Variant | Description | Use Case |
|----------|-------------|----------|
| Simple Bridge | Single abstraction | Implementation separation |
| Multi-level Bridge | Multiple hierarchies | Extensible frameworks |
| Dynamic Bridge | Changeable implementation | Runtime switching |

---

## When to Use

- Avoid permanent binding between abstraction/implementation
- Both abstractions AND implementations are extensible
- Implementation changes transparent to the client
- Sharing implementation between objects

## When NOT to Use

- Only one implementation planned
- Few variations expected
- Complexity not justified by the needs

---

## Advantages / Disadvantages

| Advantages | Disadvantages |
|-----------|---------------|
| Orthogonal variation separation | Increased complexity |
| Single Responsibility Principle | Additional indirection |
| Open/Closed Principle | Possible over-engineering |
| Runtime implementation switching | |

---

## Related Patterns

| Pattern | Relationship |
|---------|----------|
| Adapter | Adapts after design, Bridge designed upfront |
| Strategy | Strategy changes algorithm, Bridge changes implementation |
| Abstract Factory | Can create Bridge implementations |
| Decorator | Enriches without changing structure, Bridge separates hierarchies |

---

## Framework Implementations

| Framework/Lib | Implementation |
|---------------|----------------|
| database/sql | Driver interface (implementation) + DB (abstraction) |
| io.Writer | Interface as bridge to implementations |
| net/http | Handler interface |

---

## Anti-patterns to Avoid

| Anti-pattern | Problem | Solution |
|--------------|----------|----------|
| Premature Bridge | Unnecessary complexity | Wait for the real need |
| Too fine abstraction | Fragmentation | Group responsibilities |
| Leaky implementation | Coupling | Well-defined interface |

---

## Tests

```go
func TestMessage_Send(t *testing.T) {
    sender := NewEmailSender("from@test.com", "to@test.com")
    msg := NewMessage(sender, "Hello")

    err := msg.Send()
    if err != nil {
        t.Errorf("unexpected error: %v", err)
    }
}

func TestUrgentMessage_Send(t *testing.T) {
    sender := NewSMSSender("+1234567890")
    msg := NewUrgentMessage(sender, "Alert", 1)

    err := msg.Send()
    if err != nil {
        t.Errorf("unexpected error: %v", err)
    }
}

func TestBridge_SwitchImplementation(t *testing.T) {
    email := NewEmailSender("a@b.com", "c@d.com")
    sms := NewSMSSender("+1234567890")

    // Same abstraction, different implementations
    msg1 := NewMessage(email, "Test")
    msg2 := NewMessage(sms, "Test")

    if err := msg1.Send(); err != nil {
        t.Error(err)
    }
    if err := msg2.Send(); err != nil {
        t.Error(err)
    }
}
```

---

## Sources

- [Refactoring Guru - Bridge](https://refactoring.guru/design-patterns/bridge)
- [Gang of Four - Design Patterns](https://en.wikipedia.org/wiki/Design_Patterns)
- [Go database/sql as Bridge example](https://pkg.go.dev/database/sql)
