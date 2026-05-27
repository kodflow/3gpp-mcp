# Builder Pattern

> Build complex objects step by step with a fluent interface.

## Intention

Separate the construction of a complex object from its representation, allowing
the same construction process to create different representations.

## Structure

```go
package main

import (
	"errors"
	"fmt"
)

// 1. Complex product
type HTTPRequest struct {
	Method  string
	URL     string
	Headers map[string]string
	Body    string
	Timeout int
	Retries int
}

// 2. Builder with chained methods
type RequestBuilder struct {
	request *HTTPRequest
}

// NewRequestBuilder creates a new builder.
func NewRequestBuilder() *RequestBuilder {
	return &RequestBuilder{
		request: &HTTPRequest{
			Headers: make(map[string]string),
		},
	}
}

// SetMethod configures the HTTP method.
func (b *RequestBuilder) SetMethod(method string) *RequestBuilder {
	b.request.Method = method
	return b
}

// SetURL configures the URL.
func (b *RequestBuilder) SetURL(url string) *RequestBuilder {
	b.request.URL = url
	return b
}

// AddHeader adds a header.
func (b *RequestBuilder) AddHeader(key, value string) *RequestBuilder {
	b.request.Headers[key] = value
	return b
}

// SetBody configures the request body.
func (b *RequestBuilder) SetBody(body string) *RequestBuilder {
	b.request.Body = body
	return b
}

// SetTimeout configures the timeout in milliseconds.
func (b *RequestBuilder) SetTimeout(ms int) *RequestBuilder {
	b.request.Timeout = ms
	return b
}

// SetRetries configures the number of retries.
func (b *RequestBuilder) SetRetries(count int) *RequestBuilder {
	b.request.Retries = count
	return b
}

// Build constructs the final request with validation.
func (b *RequestBuilder) Build() (*HTTPRequest, error) {
	if b.request.Method == "" || b.request.URL == "" {
		return nil, errors.New("method and URL are required")
	}
	return b.request, nil
}

// 4. Director (optional)
type RequestDirector struct {
	builder *RequestBuilder
}

// NewRequestDirector creates a new director.
func NewRequestDirector(builder *RequestBuilder) *RequestDirector {
	return &RequestDirector{builder: builder}
}

// BuildGetRequest builds a preconfigured GET request.
func (d *RequestDirector) BuildGetRequest(url string) (*HTTPRequest, error) {
	return d.builder.
		SetMethod("GET").
		SetURL(url).
		SetTimeout(5000).
		Build()
}

// BuildJSONPostRequest builds a preconfigured JSON POST request.
func (d *RequestDirector) BuildJSONPostRequest(url string, data string) (*HTTPRequest, error) {
	return d.builder.
		SetMethod("POST").
		SetURL(url).
		AddHeader("Content-Type", "application/json").
		SetBody(data).
		SetTimeout(10000).
		SetRetries(3).
		Build()
}
```

## Usage

```go
package main

import (
	"encoding/json"
	"fmt"
	"log"
)

func main() {
	// Without Director (fluent interface)
	request, err := NewRequestBuilder().
		SetMethod("POST").
		SetURL("https://api.example.com/users").
		AddHeader("Authorization", "Bearer token").
		AddHeader("Content-Type", "application/json").
		SetBody(`{"name":"John"}`).
		SetTimeout(5000).
		Build()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Request: %+v\n", request)

	// With Director
	director := NewRequestDirector(NewRequestBuilder())
	getRequest, err := director.BuildGetRequest("https://api.example.com/users")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("GET Request: %+v\n", getRequest)

	postRequest, err := director.BuildJSONPostRequest(
		"https://api.example.com/users",
		`{"name":"John"}`,
	)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("POST Request: %+v\n", postRequest)
}
```

## Variants

### Step Builder (validation at each step)

```go
package main

// MethodStep defines available HTTP methods.
type MethodStep interface {
	GET(url string) HeadersStep
	POST(url string) BodyStep
}

// HeadersStep allows adding headers.
type HeadersStep interface {
	WithHeader(key, value string) HeadersStep
	Build() (*HTTPRequest, error)
}

// BodyStep requires a body before headers.
type BodyStep interface {
	WithBody(body string) HeadersStep
}

type stepBuilder struct {
	request *HTTPRequest
}

// NewStepBuilder creates a builder with step-by-step validation.
func NewStepBuilder() MethodStep {
	return &stepBuilder{
		request: &HTTPRequest{
			Headers: make(map[string]string),
		},
	}
}

func (b *stepBuilder) GET(url string) HeadersStep {
	b.request.Method = "GET"
	b.request.URL = url
	return b
}

func (b *stepBuilder) POST(url string) BodyStep {
	b.request.Method = "POST"
	b.request.URL = url
	return b
}

func (b *stepBuilder) WithBody(body string) HeadersStep {
	b.request.Body = body
	return b
}

func (b *stepBuilder) WithHeader(key, value string) HeadersStep {
	b.request.Headers[key] = value
	return b
}

func (b *stepBuilder) Build() (*HTTPRequest, error) {
	if b.request.Method == "" || b.request.URL == "" {
		return nil, errors.New("invalid request configuration")
	}
	return b.request, nil
}
```

### Immutable Builder

```go
package main

// ImmutableRequestBuilder creates new instances on each modification.
type ImmutableRequestBuilder struct {
	config *HTTPRequest
}

// NewImmutableRequestBuilder creates an immutable builder.
func NewImmutableRequestBuilder() *ImmutableRequestBuilder {
	return &ImmutableRequestBuilder{
		config: &HTTPRequest{
			Headers: make(map[string]string),
		},
	}
}

// WithMethod returns a new builder with the method configured.
func (b *ImmutableRequestBuilder) WithMethod(method string) *ImmutableRequestBuilder {
	newHeaders := make(map[string]string)
	for k, v := range b.config.Headers {
		newHeaders[k] = v
	}
	return &ImmutableRequestBuilder{
		config: &HTTPRequest{
			Method:  method,
			URL:     b.config.URL,
			Headers: newHeaders,
			Body:    b.config.Body,
			Timeout: b.config.Timeout,
			Retries: b.config.Retries,
		},
	}
}

// WithURL returns a new builder with the URL configured.
func (b *ImmutableRequestBuilder) WithURL(url string) *ImmutableRequestBuilder {
	newHeaders := make(map[string]string)
	for k, v := range b.config.Headers {
		newHeaders[k] = v
	}
	return &ImmutableRequestBuilder{
		config: &HTTPRequest{
			Method:  b.config.Method,
			URL:     url,
			Headers: newHeaders,
			Body:    b.config.Body,
			Timeout: b.config.Timeout,
			Retries: b.config.Retries,
		},
	}
}

// Build returns a copy of the request.
func (b *ImmutableRequestBuilder) Build() (*HTTPRequest, error) {
	if b.config.Method == "" || b.config.URL == "" {
		return nil, errors.New("method and URL are required")
	}
	newHeaders := make(map[string]string)
	for k, v := range b.config.Headers {
		newHeaders[k] = v
	}
	return &HTTPRequest{
		Method:  b.config.Method,
		URL:     b.config.URL,
		Headers: newHeaders,
		Body:    b.config.Body,
		Timeout: b.config.Timeout,
		Retries: b.config.Retries,
	}, nil
}
```

## Anti-patterns

```go
// BAD: Telescoping constructor
func NewRequest(
	method string,
	url string,
	headers map[string]string,
	body string,
	timeout int,
	retries int,
	// ... 10 other parameters
) *HTTPRequest {
	// Hard to read and maintain
	return &HTTPRequest{}
}

// BAD: Builder without validation
type BadBuilder struct {
	request *HTTPRequest
}

func (b *BadBuilder) Build() *HTTPRequest {
	// Returns a potentially invalid object
	return b.request
}

// BAD: Reused mutable builder
builder := NewRequestBuilder()
req1, _ := builder.SetURL("/a").Build()
req2, _ := builder.SetURL("/b").Build() // req1 also modified!
```

## Modern Alternative: Functional Options

```go
package main

import (
	"errors"
	"time"
)

// Option configures an HTTPRequest.
type Option func(*HTTPRequest)

// WithMethod configures the HTTP method.
func WithMethod(method string) Option {
	return func(r *HTTPRequest) {
		r.Method = method
	}
}

// WithURL configures the URL.
func WithURL(url string) Option {
	return func(r *HTTPRequest) {
		r.URL = url
	}
}

// WithHeader adds a header.
func WithHeader(key, value string) Option {
	return func(r *HTTPRequest) {
		if r.Headers == nil {
			r.Headers = make(map[string]string)
		}
		r.Headers[key] = value
	}
}

// WithTimeout configures the timeout.
func WithTimeout(ms int) Option {
	return func(r *HTTPRequest) {
		r.Timeout = ms
	}
}

// NewHTTPRequest creates a request with functional options.
func NewHTTPRequest(opts ...Option) (*HTTPRequest, error) {
	req := &HTTPRequest{
		Headers: make(map[string]string),
		Timeout: 5000, // Default value
	}
	for _, opt := range opts {
		opt(req)
	}
	if req.Method == "" || req.URL == "" {
		return nil, errors.New("method and URL are required")
	}
	return req, nil
}

// Simple usage for simple cases
func ExampleFunctionalOptions() {
	req, err := NewHTTPRequest(
		WithMethod("GET"),
		WithURL("/api/users"),
		WithHeader("Accept", "application/json"),
	)
	if err != nil {
		panic(err)
	}
	_ = req
}
```

## Unit Tests

```go
package main

import (
	"testing"
)

func TestRequestBuilder_Build(t *testing.T) {
	tests := []struct {
		name    string
		build   func(*RequestBuilder) *RequestBuilder
		wantErr bool
	}{
		{
			name: "valid GET request",
			build: func(b *RequestBuilder) *RequestBuilder {
				return b.SetMethod("GET").SetURL("https://api.example.com")
			},
			wantErr: false,
		},
		{
			name: "missing method",
			build: func(b *RequestBuilder) *RequestBuilder {
				return b.SetURL("/api")
			},
			wantErr: true,
		},
		{
			name: "accumulate headers",
			build: func(b *RequestBuilder) *RequestBuilder {
				return b.
					SetMethod("GET").
					SetURL("/api").
					AddHeader("Accept", "application/json").
					AddHeader("Authorization", "Bearer token")
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := NewRequestBuilder()
			request, err := tt.build(builder).Build()

			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if !tt.wantErr && request == nil {
				t.Error("expected request, got nil")
			}
		})
	}
}

func TestRequestBuilder_FluentChaining(t *testing.T) {
	builder := NewRequestBuilder()
	result := builder.SetMethod("GET")
	if result != builder {
		t.Error("expected fluent chaining to return same builder")
	}
}

func TestRequestDirector_BuildGetRequest(t *testing.T) {
	director := NewRequestDirector(NewRequestBuilder())
	request, err := director.BuildGetRequest("/api/users")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if request.Method != "GET" {
		t.Errorf("expected GET, got %s", request.Method)
	}
	if request.Timeout != 5000 {
		t.Errorf("expected timeout 5000, got %d", request.Timeout)
	}
}
```

## When to Use

- Objects with many optional parameters
- Complex multi-step construction
- Same process for different representations
- Immutability desired during construction

## Related Patterns

- **Abstract Factory**: Can use Builder to create products
- **Prototype**: Alternative when cloning is simpler
- **Fluent Interface**: Technique used by Builder

## Sources

- [Refactoring Guru - Builder](https://refactoring.guru/design-patterns/builder)
- [Effective Java - Item 2](https://www.oreilly.com/library/view/effective-java/9780134686097/)
