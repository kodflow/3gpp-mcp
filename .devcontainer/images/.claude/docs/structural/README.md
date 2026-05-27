# Structural Patterns (GoF)

Object composition patterns.

## Detailed Files

| Pattern | File | Description |
|---------|---------|-------------|
| Adapter | [adapter.md](adapter.md) | Convert incompatible interfaces |
| Bridge | [bridge.md](bridge.md) | Separate abstraction and implementation |
| Composite | [composite.md](composite.md) | Treat simple and composite objects uniformly |
| Decorator | [decorator.md](decorator.md) | Add behaviors dynamically |
| Facade | [facade.md](facade.md) | Simplify complex systems |
| Flyweight | [flyweight.md](flyweight.md) | Share common state between objects |
| Proxy | [proxy.md](proxy.md) | Virtual, Remote, Protection, Cache |

## The 7 Patterns

### 1. Adapter

> Convert one interface into another.

See detailed file: [adapter.md](adapter.md)

```go
package adapter

import "context"

// PaymentProcessor is our target interface.
type PaymentProcessor interface {
    Pay(ctx context.Context, amount float64) error
}

// StripeAPI is the external API we adapt.
type StripeAPI struct{}

func (s *StripeAPI) Charge(amountCents int64, currency string) error {
    // Stripe-specific implementation
    return nil
}

// StripeAdapter adapts StripeAPI to PaymentProcessor.
type StripeAdapter struct {
    stripe *StripeAPI
}

func NewStripeAdapter(stripe *StripeAPI) *StripeAdapter {
    return &StripeAdapter{stripe: stripe}
}

func (a *StripeAdapter) Pay(ctx context.Context, amount float64) error {
    return a.stripe.Charge(int64(amount*100), "EUR")
}
```

**When:** Integrating legacy code or third-party libraries.

---

### 2. Bridge

> Separate abstraction and implementation.

```go
package bridge

// Renderer is the implementation interface.
type Renderer interface {
    Render(shape string)
}

// Shape is the abstraction.
type Shape interface {
    Draw()
}

// Circle is a concrete abstraction.
type Circle struct {
    renderer Renderer
}

func NewCircle(renderer Renderer) *Circle {
    return &Circle{renderer: renderer}
}

func (c *Circle) Draw() {
    c.renderer.Render("circle")
}

// OpenGLRenderer is a concrete implementation.
type OpenGLRenderer struct{}

func (r *OpenGLRenderer) Render(shape string) {
    fmt.Printf("OpenGL rendering: %s\n", shape)
}
```

**When:** Multiple independent dimensions of variation.

---

### 3. Composite

> Treat simple and composite objects uniformly.

```go
package composite

// Component defines the common interface.
type Component interface {
    GetPrice() float64
}

// Product is a leaf component.
type Product struct {
    name  string
    price float64
}

func NewProduct(name string, price float64) *Product {
    return &Product{name: name, price: price}
}

func (p *Product) GetPrice() float64 {
    return p.price
}

// Box is a composite component.
type Box struct {
    items []Component
}

func NewBox() *Box {
    return &Box{items: make([]Component, 0)}
}

func (b *Box) Add(item Component) {
    b.items = append(b.items, item)
}

func (b *Box) GetPrice() float64 {
    var total float64
    for _, item := range b.items {
        total += item.GetPrice()
    }
    return total
}
```

**When:** Tree structures (menus, files, UI).

---

### 4. Decorator

> Add behaviors dynamically.

See detailed file: [decorator.md](decorator.md)

```go
package decorator

import "context"

// HttpClient is the component interface.
type HttpClient interface {
    Do(ctx context.Context, req *Request) (*Response, error)
}

// LoggingDecorator adds logging to HttpClient.
type LoggingDecorator struct {
    client HttpClient
}

func NewLoggingDecorator(client HttpClient) *LoggingDecorator {
    return &LoggingDecorator{client: client}
}

func (d *LoggingDecorator) Do(ctx context.Context, req *Request) (*Response, error) {
    fmt.Printf("Request: %s %s\n", req.Method, req.URL)
    resp, err := d.client.Do(ctx, req)
    fmt.Printf("Response: %d\n", resp.StatusCode)
    return resp, err
}

// Usage: client = NewLoggingDecorator(NewAuthDecorator(baseClient))
```

**When:** Adding responsibilities without modifying the class.

---

### 5. Facade

> Simplified interface for a complex subsystem.

See detailed file: [facade.md](facade.md)

```go
package facade

// VideoPublisher provides a simple API for video publishing.
type VideoPublisher struct {
    videoEncoder *VideoEncoder
    audioEncoder *AudioEncoder
    muxer        *Muxer
    uploader     *Uploader
}

func NewVideoPublisher() *VideoPublisher {
    return &VideoPublisher{
        videoEncoder: &VideoEncoder{},
        audioEncoder: &AudioEncoder{},
        muxer:        &Muxer{},
        uploader:     &Uploader{},
    }
}

func (vp *VideoPublisher) Publish(video, audio string) error {
    v := vp.videoEncoder.Encode(video)
    a := vp.audioEncoder.Encode(audio)
    file := vp.muxer.Mux(v, a)
    return vp.uploader.Upload(file)
}
```

**When:** Simplifying access to a complex system.

---

### 6. Flyweight

> Share common state between objects.

```go
package flyweight

import "sync"

// CharacterFlyweight contains shared state.
type CharacterFlyweight struct {
    font string
    size int
}

// FlyweightFactory manages shared flyweights.
type FlyweightFactory struct {
    cache map[string]*CharacterFlyweight
    mu    sync.RWMutex
}

func NewFlyweightFactory() *FlyweightFactory {
    return &FlyweightFactory{
        cache: make(map[string]*CharacterFlyweight),
    }
}

func (f *FlyweightFactory) Get(font string, size int) *CharacterFlyweight {
    key := fmt.Sprintf("%s-%d", font, size)

    f.mu.RLock()
    if fw, exists := f.cache[key]; exists {
        f.mu.RUnlock()
        return fw
    }
    f.mu.RUnlock()

    f.mu.Lock()
    defer f.mu.Unlock()

    if fw, exists := f.cache[key]; exists {
        return fw
    }

    fw := &CharacterFlyweight{font: font, size: size}
    f.cache[key] = fw
    return fw
}
```

**When:** Many similar objects (games, text editors).

---

### 7. Proxy

> Control access to an object.

See detailed file: [proxy.md](proxy.md)

```go
package proxy

import "sync"

// Image is the subject interface.
type Image interface {
    Display()
}

// RealImage is the real subject.
type RealImage struct {
    filename string
}

func NewRealImage(filename string) *RealImage {
    fmt.Printf("Loading image: %s\n", filename)
    return &RealImage{filename: filename}
}

func (ri *RealImage) Display() {
    fmt.Printf("Displaying: %s\n", ri.filename)
}

// ImageProxy is a virtual proxy.
type ImageProxy struct {
    filename  string
    realImage *RealImage
    once      sync.Once
}

func NewImageProxy(filename string) *ImageProxy {
    return &ImageProxy{filename: filename}
}

func (ip *ImageProxy) Display() {
    ip.once.Do(func() {
        ip.realImage = NewRealImage(ip.filename)
    })
    ip.realImage.Display()
}
```

**Types:** Virtual (lazy), Remote (RPC), Protection (auth), Cache.

---

## Decision Table

| Need | Pattern |
|--------|---------|
| Convert interface | Adapter |
| Two axes of variation | Bridge |
| Tree structure | Composite |
| Add behaviors | Decorator |
| Simplify complex system | Facade |
| Share common state | Flyweight |
| Control access | Proxy |

## Sources

- [Refactoring Guru - Structural Patterns](https://refactoring.guru/design-patterns/structural-patterns)
