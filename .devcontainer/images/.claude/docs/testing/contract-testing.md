# Contract Testing

> Verification of API contracts between services via consumer-driven tests.

## Principle

```
┌─────────────────────────────────────────────────────────────────┐
│                    Contract Testing Flow                         │
│                                                                  │
│   ┌──────────────┐         Contract         ┌──────────────┐   │
│   │   Consumer   │ ─────────────────────────►│   Provider   │   │
│   │  (Frontend)  │                           │   (Backend)  │   │
│   └──────────────┘                           └──────────────┘   │
│          │                                          │            │
│          ▼                                          ▼            │
│   1. Write consumer       3. Provider verifies                  │
│      expectations            against contract                    │
│          │                                          │            │
│          └──────────► 2. Publish contract ◄─────────┘            │
│                           (Pact Broker)                          │
└─────────────────────────────────────────────────────────────────┘
```

## Consumer Test (pact-go)

```go
package consumer_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/pact-foundation/pact-go/v2/consumer"
	"github.com/pact-foundation/pact-go/v2/matchers"
)

func TestUserAPIConsumer(t *testing.T) {
	// Create Pact
	pact, err := consumer.NewV2Pact(consumer.MockHTTPProviderConfig{
		Consumer: "OrderService",
		Provider: "UserService",
		Host:     "127.0.0.1",
		Port:     1234,
	})
	if err != nil {
		t.Fatalf("failed to create pact: %v", err)
	}

	// Setup interaction
	err = pact.
		AddInteraction().
		Given("a user with id 123 exists").
		UponReceiving("a request for user 123").
		WithRequest("GET", "/users/123", func(r *consumer.V2RequestBuilder) {
			r.Header("Accept", matchers.String("application/json"))
		}).
		WillRespondWith(200, func(r *consumer.V2ResponseBuilder) {
			r.Header("Content-Type", matchers.String("application/json"))
			r.JSONBody(matchers.MapMatcher{
				"id":    matchers.String("123"),
				"name":  matchers.String("John Doe"),
				"email": matchers.String("john@example.com"),
				"role":  matchers.String("member"),
			})
		}).
		ExecuteTest(t, func(config consumer.MockServerConfig) error {
			// Call the service
			client := NewUserClient(fmt.Sprintf("http://%s:%d", config.Host, config.Port))
			user, err := client.GetUser("123")
			if err != nil {
				return err
			}

			// Assertions
			if user.ID != "123" {
				return fmt.Errorf("user.ID = %q; want %q", user.ID, "123")
			}
			if user.Name != "John Doe" {
				return fmt.Errorf("user.Name = %q; want %q", user.Name, "John Doe")
			}
			return nil
		})

	if err != nil {
		t.Fatalf("test failed: %v", err)
	}
}

func TestUserAPIConsumer_NotFound(t *testing.T) {
	pact, err := consumer.NewV2Pact(consumer.MockHTTPProviderConfig{
		Consumer: "OrderService",
		Provider: "UserService",
		Host:     "127.0.0.1",
		Port:     1234,
	})
	if err != nil {
		t.Fatalf("failed to create pact: %v", err)
	}

	err = pact.
		AddInteraction().
		Given("a user with id 999 does not exist").
		UponReceiving("a request for non-existent user 999").
		WithRequest("GET", "/users/999", func(r *consumer.V2RequestBuilder) {
			r.Header("Accept", matchers.String("application/json"))
		}).
		WillRespondWith(404, func(r *consumer.V2ResponseBuilder) {
			r.Header("Content-Type", matchers.String("application/json"))
			r.JSONBody(matchers.MapMatcher{
				"error": matchers.String("User not found"),
				"code":  matchers.String("USER_NOT_FOUND"),
			})
		}).
		ExecuteTest(t, func(config consumer.MockServerConfig) error {
			client := NewUserClient(fmt.Sprintf("http://%s:%d", config.Host, config.Port))
			_, err := client.GetUser("999")

			if err == nil {
				return fmt.Errorf("expected error; got nil")
			}
			if err.Error() != "User not found" {
				return fmt.Errorf("error = %q; want %q", err.Error(), "User not found")
			}
			return nil
		})

	if err != nil {
		t.Fatalf("test failed: %v", err)
	}
}
```

## Flexible Matching

```go
package consumer_test

import (
	"github.com/pact-foundation/pact-go/v2/matchers"
)

// Type matchers
var userMatcher = matchers.MapMatcher{
	"id":        matchers.String("123"),
	"name":      matchers.String("John"),
	"age":       matchers.Integer(25),
	"active":    matchers.Bool(true),
	"createdAt": matchers.Timestamp("2024-01-01T00:00:00Z"),
	"role":      matchers.Regex("member", "^(admin|member|guest)$"),
}

// Array matcher
var userListMatcher = matchers.MapMatcher{
	"users": matchers.EachLike(userMatcher, 1), // At least one user
	"total": matchers.Integer(10),
	"page":  matchers.Integer(1),
}

// Nested matchers
var orderMatcher = matchers.MapMatcher{
	"id":   matchers.String("order-123"),
	"user": matchers.Like(userMatcher),
	"items": matchers.EachLike(matchers.MapMatcher{
		"productId": matchers.String("prod-1"),
		"quantity":  matchers.Integer(1),
		"price":     matchers.Decimal(29.99),
	}, 1),
	"status": matchers.Regex("pending", "^(pending|confirmed|shipped|delivered)$"),
}
```

## Provider Verification

```go
package provider_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/pact-foundation/pact-go/v2/provider"
)

func TestProviderContract(t *testing.T) {
	// Start the actual provider service
	server := startServer(3000)
	defer server.Close()

	// Setup state handlers
	stateHandlers := provider.StateHandlers{
		"a user with id 123 exists": func(setup bool, state provider.State) (provider.StateResponse, error) {
			if setup {
				// Setup state
				err := db.Users.Create(&User{
					ID:    "123",
					Name:  "John Doe",
					Email: "john@example.com",
					Role:  "member",
				})
				if err != nil {
					return provider.StateResponse{}, err
				}
			} else {
				// Teardown state
				db.Users.Delete("123")
			}
			return provider.StateResponse{}, nil
		},
		"a user with id 999 does not exist": func(setup bool, state provider.State) (provider.StateResponse, error) {
			if setup {
				db.Users.DeleteMany(map[string]interface{}{"id": "999"})
			}
			return provider.StateResponse{}, nil
		},
	}

	// Verify provider against pacts
	verifier := provider.NewVerifier()

	err := verifier.VerifyProvider(t, provider.VerifyRequest{
		Provider:                   "UserService",
		ProviderBaseURL:            "http://localhost:3000",

		// From Pact Broker
		BrokerURL:                  getEnv("PACT_BROKER_URL", ""),
		BrokerToken:                getEnv("PACT_BROKER_TOKEN", ""),
		PublishVerificationResults: getEnv("CI", "") == "true",
		ProviderVersion:            getEnv("GIT_SHA", "dev"),

		// Or from local files
		// PactURLs: []string{"./pacts/orderservice-userservice.json"},

		StateHandlers: stateHandlers,

		// Request filters (add auth headers, etc.)
		BeforeEach: func() error {
			// Setup before each interaction
			return nil
		},
		AfterEach: func() error {
			// Cleanup after each interaction
			return nil
		},
		RequestFilter: func(req *http.Request, res *http.Response, next http.HandlerFunc) {
			req.Header.Set("Authorization", "Bearer test-token")
			next(res, req)
		},
	})

	if err != nil {
		t.Fatalf("verification failed: %v", err)
	}
}

func startServer(port int) *http.Server {
	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: createHandler(),
	}
	go server.ListenAndServe()
	return server
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
```

## CI/CD Integration

```yaml
# .github/workflows/contract-tests.yml
name: Contract Tests

on: [push, pull_request]

jobs:
  consumer-tests:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Setup Go
        uses: actions/setup-go@v5
        with:
          go-version: '1.25.0'

      - name: Run consumer contract tests
        run: go test ./... -tags=contract

      - name: Publish pacts to broker
        run: |
          pact-broker publish ./pacts \
            --broker-base-url=${{ secrets.PACT_BROKER_URL }} \
            --broker-token=${{ secrets.PACT_BROKER_TOKEN }} \
            --consumer-app-version=${{ github.sha }} \
            --tag=${{ github.ref_name }}

  provider-tests:
    runs-on: ubuntu-latest
    needs: consumer-tests
    steps:
      - uses: actions/checkout@v4

      - name: Setup Go
        uses: actions/setup-go@v5
        with:
          go-version: '1.25.0'

      - name: Verify provider against pacts
        env:
          PACT_BROKER_URL: ${{ secrets.PACT_BROKER_URL }}
          PACT_BROKER_TOKEN: ${{ secrets.PACT_BROKER_TOKEN }}
          GIT_SHA: ${{ github.sha }}
        run: go test ./... -tags=provider

      - name: Can I Deploy?
        run: |
          pact-broker can-i-deploy \
            --pacticipant=UserService \
            --version=${{ github.sha }} \
            --to-environment=production
```

## Schema-Based Contracts (Alternative)

```go
package contract_test

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/xeipuuv/gojsonschema"
)

// OpenAPIContractValidator validates against OpenAPI schemas
type OpenAPIContractValidator struct {
	schemas map[string]*gojsonschema.Schema
}

func NewOpenAPIContractValidator() *OpenAPIContractValidator {
	return &OpenAPIContractValidator{
		schemas: make(map[string]*gojsonschema.Schema),
	}
}

func (v *OpenAPIContractValidator) LoadSpec(specPath string) error {
	data, err := os.ReadFile(specPath)
	if err != nil {
		return err
	}

	var spec OpenAPISpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return err
	}

	// Extract response schemas
	for path, methods := range spec.Paths {
		for method, operation := range methods {
			for status, response := range operation.Responses {
				if schema, ok := response.Content["application/json"]; ok {
					key := fmt.Sprintf("%s %s %s", method, path, status)
					compiled, err := gojsonschema.NewSchema(gojsonschema.NewGoLoader(schema.Schema))
					if err != nil {
						return err
					}
					v.schemas[key] = compiled
				}
			}
		}
	}
	return nil
}

func (v *OpenAPIContractValidator) ValidateResponse(method, path, status string, body interface{}) error {
	key := fmt.Sprintf("%s %s %s", method, path, status)
	schema, exists := v.schemas[key]
	if !exists {
		return fmt.Errorf("no schema found for %s", key)
	}

	result, err := schema.Validate(gojsonschema.NewGoLoader(body))
	if err != nil {
		return err
	}

	if !result.Valid() {
		return fmt.Errorf("validation failed: %v", result.Errors())
	}
	return nil
}

// Usage in tests
func TestAPIResponseMatchesSchema(t *testing.T) {
	validator := NewOpenAPIContractValidator()
	if err := validator.LoadSpec("./openapi.yaml"); err != nil {
		t.Fatalf("failed to load spec: %v", err)
	}

	resp, err := http.Get("http://localhost:3000/users/123")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	err = validator.ValidateResponse("GET", "/users/{id}", "200", body)
	if err != nil {
		t.Errorf("validation failed: %v", err)
	}
}
```

## Recommended Libraries

| Package | Usage |
|---------|-------|
| `github.com/pact-foundation/pact-go/v2` | Consumer-driven contracts |
| `github.com/xeipuuv/gojsonschema` | JSON Schema validation |
| `github.com/getkin/kin-openapi` | OpenAPI validation |

## Common Mistakes

| Mistake | Impact | Solution |
|---------|--------|----------|
| Too strict matchers | Fragile tests | Like(), Regex() matchers |
| No state handlers | Provider verification fails | Implement all states |
| Forgetting can-i-deploy | Deploy breaking changes | Mandatory CI gate |
| Pacts not published | Provider cannot see them | Publish in CI |
| Non-isolated state | Flaky tests | Reset DB between states |

## When to Use

| Scenario | Recommended |
|----------|------------|
| Microservices | Yes |
| Public API | Yes |
| Separate Frontend/Backend | Yes |
| Monolith | Not necessary |
| Rapid prototyping | Too much overhead |

## Related Patterns

- **Test Doubles**: Complementary mocks
- **Integration Tests**: End-to-end verification
- **API Versioning**: Contract change management

## Sources

- [Pact Documentation](https://docs.pact.io/)
- [pact-go Repository](https://github.com/pact-foundation/pact-go)
- [Consumer-Driven Contracts - Martin Fowler](https://martinfowler.com/articles/consumerDrivenContracts.html)
- [Contract Testing vs E2E Testing](https://pactflow.io/blog/contract-testing-vs-end-to-end-e2e-testing/)
