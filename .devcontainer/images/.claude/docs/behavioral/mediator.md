# Mediator

> Encapsulate interactions between objects for loose coupling.

---

## Principle

The Mediator pattern reduces coupling between components by making them
communicate via a central intermediary.
Each component only knows the mediator.

```text
┌─────────┐     ┌─────────┐
│Colleague│     │Colleague│
│    A    │     │    B    │
└────┬────┘     └────┬────┘
     │               │
     └───────┬───────┘
             │
      ┌──────▼──────┐
      │  Mediator   │
      │ (coordinate)│
      └─────────────┘
```

---

## Problem Solved

- Tightly coupled components with each other
- Dispersed coordination logic
- Difficulty reusing individual components
- Cascading changes during modifications

---

## Solution

```go
package main

import "fmt"

// Mediator defines the mediation interface.
type Mediator interface {
    Notify(sender Component, event string)
}

// Component is a participant in the mediation.
type Component interface {
    SetMediator(m Mediator)
}

// BaseComponent provides the base implementation.
type BaseComponent struct {
    mediator Mediator
}

func (c *BaseComponent) SetMediator(m Mediator) {
    c.mediator = m
}

// Button is a UI component.
type Button struct {
    BaseComponent
    name string
}

func NewButton(name string) *Button {
    return &Button{name: name}
}

func (b *Button) Click() {
    fmt.Printf("Button '%s' clicked\n", b.name)
    if b.mediator != nil {
        b.mediator.Notify(b, "click")
    }
}

// Dialog is the concrete mediator.
type Dialog struct {
    title      string
    loginBtn   *Button
    cancelBtn  *Button
}

func NewDialog(title string) *Dialog {
    d:= &Dialog{title: title}
    d.loginBtn = NewButton("Login")
    d.cancelBtn = NewButton("Cancel")
    d.loginBtn.SetMediator(d)
    d.cancelBtn.SetMediator(d)
    return d
}

func (d *Dialog) Notify(sender Component, event string) {
    switch sender {
    case d.loginBtn:
        fmt.Println("Dialog: Login requested, validating...")
    case d.cancelBtn:
        fmt.Println("Dialog: Cancelled, closing...")
    }
}
```

---

## Complete Example

```go
package main

import (
    "fmt"
    "time"
)

// ChatMediator coordinates messages between users.
type ChatMediator interface {
    Send(message string, sender *User)
    Register(user *User)
}

// User represents a chat participant.
type User struct {
    name     string
    mediator ChatMediator
}

func NewUser(name string, mediator ChatMediator) *User {
    u:= &User{name: name, mediator: mediator}
    mediator.Register(u)
    return u
}

func (u *User) Send(message string) {
    fmt.Printf("[%s] sends: %s\n", u.name, message)
    u.mediator.Send(message, u)
}

func (u *User) Receive(message string, from string) {
    fmt.Printf("[%s] received from %s: %s\n", u.name, from, message)
}

// ChatRoom is the concrete mediator.
type ChatRoom struct {
    name  string
    users []*User
}

func NewChatRoom(name string) *ChatRoom {
    return &ChatRoom{name: name, users: make([]*User, 0)}
}

func (r *ChatRoom) Register(user *User) {
    r.users = append(r.users, user)
    fmt.Printf("--- %s joined %s ---\n", user.name, r.name)
}

func (r *ChatRoom) Send(message string, sender *User) {
    for _, user:= range r.users {
        if user != sender {
            user.Receive(message, sender.name)
        }
    }
}

// AirTrafficControl coordinates aircraft.
type AirTrafficControl struct {
    flights map[string]*Flight
}

func NewAirTrafficControl() *AirTrafficControl {
    return &AirTrafficControl{flights: make(map[string]*Flight)}
}

type Flight struct {
    id       string
    altitude int
    atc      *AirTrafficControl
}

func (atc *AirTrafficControl) Register(flight *Flight) {
    atc.flights[flight.id] = flight
    fmt.Printf("ATC: %s registered\n", flight.id)
}

func (atc *AirTrafficControl) RequestLanding(flight *Flight) bool {
    // Check if runway is free (simplified logic)
    for id, f:= range atc.flights {
        if id != flight.id && f.altitude < 1000 {
            fmt.Printf("ATC: Landing denied for %s - runway busy\n", flight.id)
            return false
        }
    }
    fmt.Printf("ATC: Landing approved for %s\n", flight.id)
    return true
}

func (atc *AirTrafficControl) Broadcast(message string, sender *Flight) {
    for id, flight:= range atc.flights {
        if id != sender.id {
            fmt.Printf("ATC -> %s: %s\n", flight.id, message)
        }
    }
}

func NewFlight(id string, atc *AirTrafficControl) *Flight {
    f:= &Flight{id: id, altitude: 10000, atc: atc}
    atc.Register(f)
    return f
}

func (f *Flight) RequestLanding() {
    fmt.Printf("%s requesting landing...\n", f.id)
    if f.atc.RequestLanding(f) {
        f.altitude = 0
        f.atc.Broadcast(fmt.Sprintf("%s is landing", f.id), f)
    }
}

func main() {
    // Example 1: Chat Room
    fmt.Println("=== Chat Room Example ===")
    room:= NewChatRoom("General")

    alice:= NewUser("Alice", room)
    bob:= NewUser("Bob", room)
    charlie:= NewUser("Charlie", room)

    alice.Send("Hello everyone!")
    bob.Send("Hi Alice!")

    // Example 2: Air Traffic Control
    fmt.Println("\n=== ATC Example ===")
    atc:= NewAirTrafficControl()

    flight1:= NewFlight("UA123", atc)
    flight2:= NewFlight("BA456", atc)

    flight1.RequestLanding()
    flight2.RequestLanding() // Denied

    // Output:
    // === Chat Room Example ===
    // --- Alice joined General ---
    // --- Bob joined General ---
    // --- Charlie joined General ---
    // [Alice] sends: Hello everyone!
    // [Bob] received from Alice: Hello everyone!
    // [Charlie] received from Alice: Hello everyone!
    // [Bob] sends: Hi Alice!
    // [Alice] received from Bob: Hi Alice!
    // [Charlie] received from Bob: Hi Alice!
    // === ATC Example ===
    // ATC: UA123 registered
    // ATC: BA456 registered
    // UA123 requesting landing...
    // ATC: Landing approved for UA123
    // ATC -> BA456: UA123 is landing
    // BA456 requesting landing...
    // ATC: Landing denied for BA456 - runway busy
}
```

---

## Variants

| Variant | Description | Use Case |
|----------|-------------|-------------|
| Simple Mediator | Single mediator | Simple applications |
| Mediator + Events | Event-driven | UI, reactive systems |
| Mediator + Commands | Combined Command pattern | Undo/redo |

---

## When to Use

- Tightly coupled components
- Difficult component reuse
- Behavior distributed across classes
- N-to-N communication between objects

## When NOT to Use

- Few components (direct coupling acceptable)
- Simple point-to-point communication
- The mediator becomes a "God Object"

---

## Advantages / Disadvantages

| Advantages | Disadvantages |
|-----------|---------------|
| Reduces coupling | Mediator can become complex |
| Single Responsibility | Single point of failure |
| Open/Closed | Can become a God Object |
| Easier reuse | |

---

## Related Patterns

| Pattern | Relation |
|---------|----------|
| Observer | Mediator centralizes, Observer distributes |
| Facade | Facade simplifies, Mediator coordinates |
| Command | Can use Commands for notifications |
| Chain of Responsibility | Alternative for certain cases |

---

## Framework Implementations

| Framework/Lib | Implementation |
|---------------|----------------|
| Event bus | Mediator for events |
| Message brokers | RabbitMQ, Kafka as mediators |
| UI frameworks | Controllers as mediators |

---

## Anti-patterns to Avoid

| Anti-pattern | Problem | Solution |
|--------------|----------|----------|
| God Mediator | Too much centralized logic | Decompose into sub-mediators |
| Notification cycles | Infinite loops | Guards and flags |
| Coupling to mediator | Strong dependency | Abstract interface |

---

## Tests

```go
func TestChatRoom_Broadcast(t *testing.T) {
    room:= NewChatRoom("Test")
    alice:= NewUser("Alice", room)
    bob:= NewUser("Bob", room)

    // Capture output
    received:= make([]string, 0)
    // ... mock Receive method

    alice.Send("Test message")

    if len(room.users) != 2 {
        t.Errorf("expected 2 users, got %d", len(room.users))
    }
}

func TestATC_RequestLanding(t *testing.T) {
    atc:= NewAirTrafficControl()
    flight1:= NewFlight("F1", atc)
    flight2:= NewFlight("F2", atc)

    // First landing should succeed
    flight1.RequestLanding()
    if flight1.altitude != 0 {
        t.Error("expected flight1 to land")
    }

    // Second should be denied
    flight2.RequestLanding()
    if flight2.altitude == 0 {
        t.Error("expected flight2 to be denied")
    }
}
```

---

## Sources

- [Refactoring Guru - Mediator](https://refactoring.guru/design-patterns/mediator)
- [Gang of Four - Design Patterns](https://en.wikipedia.org/wiki/Design_Patterns)
- [Event-Driven Architecture](https://martinfowler.com/articles/201701-event-driven.html)
