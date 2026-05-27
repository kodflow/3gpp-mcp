# Decorator Pattern

> Add behaviors to an object dynamically without modifying its class.

## Intent

Attach additional responsibilities to an object dynamically.
Decorators offer a flexible alternative to inheritance for extending
functionality.

## Structure

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"
)

// 1. Component interface
type HTTPClient interface {
	Request(ctx context.Context, config RequestConfig) (*http.Response, error)
}

type RequestConfig struct {
	URL     string
	Method  string
	Headers map[string]string
	Body    io.Reader
}

// 2. Concrete component
type BasicHTTPClient struct {
	client *http.Client
}

func NewBasicHTTPClient() *BasicHTTPClient {
	return &BasicHTTPClient{
		client: http.DefaultClient,
	}
}

func (b *BasicHTTPClient) Request(ctx context.Context, config RequestConfig) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, config.Method, config.URL, config.Body)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	for k, v := range config.Headers {
		req.Header.Set(k, v)
	}

	return b.client.Do(req)
}

// 3. Concrete decorators (no base class in Go - direct composition)

type LoggingDecorator struct {
	client HTTPClient
}

func NewLoggingDecorator(client HTTPClient) *LoggingDecorator {
	return &LoggingDecorator{client: client}
}

func (l *LoggingDecorator) Request(ctx context.Context, config RequestConfig) (*http.Response, error) {
	fmt.Printf("[HTTP] %s %s\n", config.Method, config.URL)
	start := time.Now()

	response, err := l.client.Request(ctx, config)

	if err != nil {
		fmt.Printf("[HTTP] Error: %v\n", err)
		return nil, err
	}

	fmt.Printf("[HTTP] %d (%dms)\n", response.StatusCode, time.Since(start).Milliseconds())
	return response, nil
}

type AuthDecorator struct {
	client        HTTPClient
	tokenProvider func() string
}

func NewAuthDecorator(client HTTPClient, tokenProvider func() string) *AuthDecorator {
	return &AuthDecorator{
		client:        client,
		tokenProvider: tokenProvider,
	}
}

func (a *AuthDecorator) Request(ctx context.Context, config RequestConfig) (*http.Response, error) {
	token := a.tokenProvider()

	if config.Headers == nil {
		config.Headers = make(map[string]string)
	}
	config.Headers["Authorization"] = "Bearer " + token

	return a.client.Request(ctx, config)
}

type RetryDecorator struct {
	client     HTTPClient
	maxRetries int
	delay      time.Duration
}

func NewRetryDecorator(client HTTPClient, maxRetries int, delay time.Duration) *RetryDecorator {
	if maxRetries == 0 {
		maxRetries = 3
	}
	if delay == 0 {
		delay = 1 * time.Second
	}
	return &RetryDecorator{
		client:     client,
		maxRetries: maxRetries,
		delay:      delay,
	}
}

func (r *RetryDecorator) Request(ctx context.Context, config RequestConfig) (*http.Response, error) {
	var lastErr error

	for attempt := 0; attempt <= r.maxRetries; attempt++ {
		response, err := r.client.Request(ctx, config)
		if err == nil && (response.StatusCode < 500 || response.StatusCode == http.StatusOK) {
			return response, nil
		}

		lastErr = err
		if err == nil {
			lastErr = fmt.Errorf("HTTP %d", response.StatusCode)
		}

		if attempt < r.maxRetries {
			backoff := time.Duration(math.Pow(2, float64(attempt))) * r.delay
			time.Sleep(backoff)
		}
	}

	return nil, lastErr
}

type CacheDecorator struct {
	client HTTPClient
	cache  map[string]*cacheEntry
	ttl    time.Duration
}

type cacheEntry struct {
	response *http.Response
	expires  time.Time
}

func NewCacheDecorator(client HTTPClient, ttl time.Duration) *CacheDecorator {
	if ttl == 0 {
		ttl = 60 * time.Second
	}
	return &CacheDecorator{
		client: client,
		cache:  make(map[string]*cacheEntry),
		ttl:    ttl,
	}
}

func (c *CacheDecorator) Request(ctx context.Context, config RequestConfig) (*http.Response, error) {
	if config.Method != "GET" {
		return c.client.Request(ctx, config)
	}

	key := config.URL
	if cached, found := c.cache[key]; found && cached.expires.After(time.Now()) {
		fmt.Printf("[CACHE] Hit: %s\n", key)
		return cached.response, nil
	}

	response, err := c.client.Request(ctx, config)
	if err != nil {
		return nil, err
	}

	c.cache[key] = &cacheEntry{
		response: response,
		expires:  time.Now().Add(c.ttl),
	}

	return response, nil
}
```

## Usage

```go
package main

import (
	"context"
	"fmt"
)

func main() {
	// Decorator composition
	var client HTTPClient = NewBasicHTTPClient()
	client = NewLoggingDecorator(client)
	client = NewAuthDecorator(client, func() string { return "my-token" })
	client = NewRetryDecorator(client, 3, 1*time.Second)
	client = NewCacheDecorator(client, 30*time.Second)

	// Order matters!
	// Cache -> Retry -> Auth -> Logging -> Basic

	// Transparent usage
	response, err := client.Request(context.Background(), RequestConfig{
		URL:    "https://api.example.com/users",
		Method: "GET",
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	defer response.Body.Close()
}
```

## Variants

### Functional Decorator

```go
package main

import (
	"context"
)

type Middleware func(next HandlerFunc) HandlerFunc

type HandlerFunc func(ctx context.Context, config RequestConfig) (*http.Response, error)

func LoggingMiddleware(next HandlerFunc) HandlerFunc {
	return func(ctx context.Context, config RequestConfig) (*http.Response, error) {
		fmt.Printf("Request: %s %s\n", config.Method, config.URL)
		response, err := next(ctx, config)
		if err == nil {
			fmt.Printf("Response: %d\n", response.StatusCode)
		}
		return response, err
	}
}

func AuthMiddleware(token string) Middleware {
	return func(next HandlerFunc) HandlerFunc {
		return func(ctx context.Context, config RequestConfig) (*http.Response, error) {
			if config.Headers == nil {
				config.Headers = make(map[string]string)
			}
			config.Headers["Authorization"] = "Bearer " + token
			return next(ctx, config)
		}
	}
}

// Composition
func Compose(middlewares ...Middleware) Middleware {
	return func(next HandlerFunc) HandlerFunc {
		for i := len(middlewares) - 1; i >= 0; i-- {
			next = middlewares[i](next)
		}
		return next
	}
}

func main() {
	baseHandler := func(ctx context.Context, config RequestConfig) (*http.Response, error) {
		req, _ := http.NewRequestWithContext(ctx, config.Method, config.URL, config.Body)
		return http.DefaultClient.Do(req)
	}

	enhanced := Compose(
		LoggingMiddleware,
		AuthMiddleware("token"),
	)(baseHandler)

	_, _ = enhanced(context.Background(), RequestConfig{
		URL:    "https://api.example.com",
		Method: "GET",
	})
}
```

## Concrete Use Cases

### Stream Decorators

```go
package main

import (
	"compress/gzip"
	"io"
	"os"
)

type OutputStream interface {
	Write(data []byte) (int, error)
	Close() error
}

type FileOutputStream struct {
	file *os.File
}

func NewFileOutputStream(path string) (*FileOutputStream, error) {
	file, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return &FileOutputStream{file: file}, nil
}

func (f *FileOutputStream) Write(data []byte) (int, error) {
	return f.file.Write(data)
}

func (f *FileOutputStream) Close() error {
	return f.file.Close()
}

type BufferedOutputStream struct {
	stream     OutputStream
	buffer     []byte
	bufferSize int
}

func NewBufferedOutputStream(stream OutputStream, bufferSize int) *BufferedOutputStream {
	if bufferSize == 0 {
		bufferSize = 1024
	}
	return &BufferedOutputStream{
		stream:     stream,
		buffer:     make([]byte, 0, bufferSize),
		bufferSize: bufferSize,
	}
}

func (b *BufferedOutputStream) Write(data []byte) (int, error) {
	b.buffer = append(b.buffer, data...)
	if len(b.buffer) >= b.bufferSize {
		return b.flush()
	}
	return len(data), nil
}

func (b *BufferedOutputStream) flush() (int, error) {
	n, err := b.stream.Write(b.buffer)
	b.buffer = b.buffer[:0]
	return n, err
}

func (b *BufferedOutputStream) Close() error {
	if len(b.buffer) > 0 {
		if _, err := b.flush(); err != nil {
			return err
		}
	}
	return b.stream.Close()
}

type CompressedOutputStream struct {
	stream OutputStream
	writer *gzip.Writer
}

func NewCompressedOutputStream(stream OutputStream) *CompressedOutputStream {
	writer := gzip.NewWriter(stream.(io.Writer))
	return &CompressedOutputStream{
		stream: stream,
		writer: writer,
	}
}

func (c *CompressedOutputStream) Write(data []byte) (int, error) {
	return c.writer.Write(data)
}

func (c *CompressedOutputStream) Close() error {
	if err := c.writer.Close(); err != nil {
		return err
	}
	return c.stream.Close()
}

// Usage
func ExampleStreamDecorators() {
	file, _ := NewFileOutputStream("output.txt.gz")
	buffered := NewBufferedOutputStream(file, 1024)
	compressed := NewCompressedOutputStream(buffered)

	compressed.Write([]byte("Hello, World!"))
	compressed.Close()
}
```

## Anti-patterns

```go
// BAD: Decorator that modifies the interface
type BadDecorator struct {
	client HTTPClient
	stats  map[string]int
}

func (b *BadDecorator) Request(ctx context.Context, config RequestConfig) (*http.Response, error) {
	return b.client.Request(ctx, config)
}

// Additional method = pattern violation
func (b *BadDecorator) GetStats() map[string]int {
	return b.stats
}

// BAD: Undocumented decorator order
func BadOrder() {
	var client HTTPClient = NewBasicHTTPClient()
	// Cache before Auth = cached tokens!
	client = NewCacheDecorator(client, 60*time.Second)
	client = NewAuthDecorator(client, func() string { return "token" })
}

// BAD: Decorator with shared state
var globalCount int

type StatefulDecorator struct {
	client HTTPClient
}

func (s *StatefulDecorator) Request(ctx context.Context, config RequestConfig) (*http.Response, error) {
	globalCount++ // Shared state = problems
	return s.client.Request(ctx, config)
}
```

## Unit Tests

```go
package main

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestLoggingDecorator(t *testing.T) {
	mockClient := &mockHTTPClient{
		response: &http.Response{StatusCode: 200},
	}

	decorator := NewLoggingDecorator(mockClient)
	_, err := decorator.Request(context.Background(), RequestConfig{
		URL:    "/api",
		Method: "GET",
	})

	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	if !mockClient.called {
		t.Error("Expected client to be called")
	}
}

func TestRetryDecorator(t *testing.T) {
	attempts := 0
	mockClient := &mockHTTPClient{
		requestFunc: func(ctx context.Context, config RequestConfig) (*http.Response, error) {
			attempts++
			if attempts < 3 {
				return nil, fmt.Errorf("network error")
			}
			return &http.Response{StatusCode: 200}, nil
		},
	}

	decorator := NewRetryDecorator(mockClient, 3, 10*time.Millisecond)
	response, err := decorator.Request(context.Background(), RequestConfig{
		URL:    "/api",
		Method: "GET",
	})

	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	if response.StatusCode != 200 {
		t.Errorf("Expected 200, got %d", response.StatusCode)
	}

	if attempts != 3 {
		t.Errorf("Expected 3 attempts, got %d", attempts)
	}
}

func TestDecoratorComposition(t *testing.T) {
	order := []string{}

	mockClient := &mockHTTPClient{
		requestFunc: func(ctx context.Context, config RequestConfig) (*http.Response, error) {
			order = append(order, "base")
			return &http.Response{StatusCode: 200}, nil
		},
	}

	// Simulate decorators that track order
	var client HTTPClient = mockClient
	client = &orderTrackingDecorator{client: client, name: "first", order: &order}
	client = &orderTrackingDecorator{client: client, name: "second", order: &order}

	_, _ = client.Request(context.Background(), RequestConfig{})

	expected := []string{"second-before", "first-before", "base", "first-after", "second-after"}
	if len(order) != len(expected) {
		t.Errorf("Expected %v, got %v", expected, order)
	}
}

type mockHTTPClient struct {
	response    *http.Response
	err         error
	called      bool
	requestFunc func(context.Context, RequestConfig) (*http.Response, error)
}

func (m *mockHTTPClient) Request(ctx context.Context, config RequestConfig) (*http.Response, error) {
	m.called = true
	if m.requestFunc != nil {
		return m.requestFunc(ctx, config)
	}
	return m.response, m.err
}

type orderTrackingDecorator struct {
	client HTTPClient
	name   string
	order  *[]string
}

func (o *orderTrackingDecorator) Request(ctx context.Context, config RequestConfig) (*http.Response, error) {
	*o.order = append(*o.order, o.name+"-before")
	resp, err := o.client.Request(ctx, config)
	*o.order = append(*o.order, o.name+"-after")
	return resp, err
}
```

## When to Use

- Adding responsibilities without modifying the class
- Dynamically combinable behaviors
- Extension impossible by inheritance (sealed class)
- Cross-cutting concerns (logging, caching, auth)

## Related Patterns

- **Adapter**: Changes the interface vs adds behaviors
- **Composite**: Tree structure vs linear chain
- **Proxy**: Access control vs extension
- **Chain of Responsibility**: Similar pattern for handlers

## Sources

- [Refactoring Guru - Decorator](https://refactoring.guru/design-patterns/decorator)
