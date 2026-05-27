# Snapshot Testing

> Capture and compare output with a saved reference.

## Principle

```
┌─────────────────────────────────────────────────────────────────┐
│                    Snapshot Testing Flow                         │
│                                                                  │
│   First Run:    Output ──────────► Save as snapshot             │
│                                                                  │
│   Next Runs:    Output ──► Compare ──► Match? ──► Pass          │
│                              │                                   │
│                              └──► Mismatch? ──► Fail or Update   │
└─────────────────────────────────────────────────────────────────┘
```

## Basic Snapshots (cupaloy)

```go
package user_test

import (
	"testing"

	"github.com/bradleyjkemp/cupaloy/v2"
)

func TestUserProfileSnapshot(t *testing.T) {
	user := &User{
		ID:     "123",
		Name:   "John Doe",
		Email:  "john@example.com",
		Avatar: "https://example.com/avatar.jpg",
	}

	profile := RenderUserProfile(user)

	// Snapshot the output
	err := cupaloy.Snapshot(profile)
	if err != nil {
		t.Fatalf("snapshot mismatch: %v", err)
	}
}

func TestUserProfileLoadingState(t *testing.T) {
	profile := RenderUserProfile(nil)

	err := cupaloy.Snapshot(profile)
	if err != nil {
		t.Fatalf("snapshot mismatch: %v", err)
	}
}

func TestUserProfileErrorState(t *testing.T) {
	profile := RenderUserProfileError("Failed to load")

	err := cupaloy.Snapshot(profile)
	if err != nil {
		t.Fatalf("snapshot mismatch: %v", err)
	}
}
```

Generated snapshot file (`.snapshots/TestUserProfileSnapshot`):

```
<div class="profile">
  <img alt="John Doe" src="https://example.com/avatar.jpg">
  <h1>John Doe</h1>
  <p>john@example.com</p>
</div>
```

## Named Snapshots

```go
package format_test

import (
	"testing"
	"time"

	"github.com/bradleyjkemp/cupaloy/v2"
)

func TestFormatDate(t *testing.T) {
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	formatted := FormatDate(date)

	// Named snapshot stored in test file
	snapshotter := cupaloy.New(cupaloy.SnapshotSubdirectory(".snapshots"))
	err := snapshotter.SnapshotWithName("formatted_date", formatted)
	if err != nil {
		t.Fatalf("snapshot mismatch: %v", err)
	}
}

func TestSerializeUser(t *testing.T) {
	user := &User{ID: "1", Name: "John"}
	serialized := SerializeUser(user)

	snapshotter := cupaloy.New(cupaloy.SnapshotSubdirectory(".snapshots"))
	err := snapshotter.SnapshotWithName("serialized_user", serialized)
	if err != nil {
		t.Fatalf("snapshot mismatch: %v", err)
	}
}
```

## API Response Snapshots

```go
package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bradleyjkemp/cupaloy/v2"
)

func TestGetUsersResponse(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/users", nil)
	w := httptest.NewRecorder()

	handler := NewUserHandler()
	handler.ServeHTTP(w, req)

	// Snapshot the response body
	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	snapshotter := cupaloy.New(
		cupaloy.SnapshotSubdirectory(".snapshots"),
	)
	err := snapshotter.Snapshot(response)
	if err != nil {
		t.Fatalf("snapshot mismatch: %v", err)
	}
}

func TestErrorResponse(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/users/invalid", nil)
	w := httptest.NewRecorder()

	handler := NewUserHandler()
	handler.ServeHTTP(w, req)

	var response ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	expected := ErrorResponse{
		Error:  "User not found",
		Code:   "USER_NOT_FOUND",
		Status: 404,
	}

	if response != expected {
		t.Errorf("response = %+v; want %+v", response, expected)
	}
}
```

## Dynamic Value Handling

```go
package user_test

import (
	"testing"
	"time"

	"github.com/bradleyjkemp/cupaloy/v2"
)

func TestUserWithDynamicValues(t *testing.T) {
	user := CreateUser()

	// Use custom matcher to ignore dynamic fields
	snapshotter := cupaloy.New(
		cupaloy.SnapshotSubdirectory(".snapshots"),
		cupaloy.CreateNewAutomatically(true),
	)

	// Normalize dynamic values before snapshot
	normalized := map[string]interface{}{
		"id":        "[DYNAMIC-ID]",          // Mask ID
		"name":      user.Name,
		"email":     user.Email,
		"createdAt": "[DYNAMIC-TIMESTAMP]",   // Mask timestamp
	}

	err := snapshotter.Snapshot(normalized)
	if err != nil {
		t.Fatalf("snapshot mismatch: %v", err)
	}
}

func TestOrderWithDynamicValues(t *testing.T) {
	order := CreateOrder()

	// Test structure, not exact values
	structure := map[string]interface{}{
		"id":        "string",
		"createdAt": "timestamp",
		"items": []map[string]interface{}{
			{
				"id":       "string",
				"quantity": order.Items[0].Quantity,
			},
		},
	}

	snapshotter := cupaloy.New(cupaloy.SnapshotSubdirectory(".snapshots"))
	err := snapshotter.Snapshot(structure)
	if err != nil {
		t.Fatalf("snapshot mismatch: %v", err)
	}
}
```

## Custom Snapshotter Configuration

```go
package config_test

import (
	"testing"

	"github.com/bradleyjkemp/cupaloy/v2"
)

// Global snapshotter with custom config
var snapshotter = cupaloy.New(
	cupaloy.SnapshotSubdirectory(".snapshots"),
	cupaloy.CreateNewAutomatically(false), // Fail if snapshot doesn't exist
	cupaloy.FailOnUpdate(true),            // Fail in CI if update needed
	cupaloy.ShouldUpdate(func() bool {
		return os.Getenv("UPDATE_SNAPSHOTS") == "true"
	}),
)

func TestConfigGeneration(t *testing.T) {
	config := GenerateConfig(ConfigOptions{Env: "production"})

	err := snapshotter.Snapshot(config)
	if err != nil {
		t.Fatalf("snapshot mismatch: %v", err)
	}
}

func TestValidationErrors(t *testing.T) {
	errors := ValidateForm(FormData{
		Email:    "invalid",
		Password: "123",
	})

	expected := []ValidationError{
		{Field: "email", Message: "Invalid email format"},
		{Field: "password", Message: "Password must be at least 8 characters"},
	}

	// Use regular assertion for structured data
	if len(errors) != len(expected) {
		t.Errorf("len(errors) = %d; want %d", len(errors), len(expected))
	}
	for i, err := range errors {
		if err != expected[i] {
			t.Errorf("errors[%d] = %+v; want %+v", i, err, expected[i])
		}
	}
}
```

## Table-Driven Snapshot Tests

```go
package format_test

import (
	"testing"

	"github.com/bradleyjkemp/cupaloy/v2"
)

func TestDateFormatting(t *testing.T) {
	tests := []struct {
		name string
		date time.Time
	}{
		{"simple", time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)},
		{"leap_year", time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)},
		{"end_of_year", time.Date(2024, 12, 31, 23, 59, 59, 0, time.UTC)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			formatted := FormatDate(tt.date)

			snapshotter := cupaloy.New(cupaloy.SnapshotSubdirectory(".snapshots"))
			err := snapshotter.SnapshotWithName(tt.name, formatted)
			if err != nil {
				t.Fatalf("snapshot mismatch: %v", err)
			}
		})
	}
}
```

## go-test-diff Alternative

```go
package user_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
)

func TestUserWithDiff(t *testing.T) {
	user := CreateUser()

	expected := &User{
		ID:    "123",
		Name:  "John Doe",
		Email: "john@example.com",
	}

	// Use go-cmp for detailed diff
	if diff := cmp.Diff(expected, user); diff != "" {
		t.Errorf("user mismatch (-want +got):\n%s", diff)
	}
}

func TestAPIResponseWithAssert(t *testing.T) {
	response := GetAPIResponse()

	expected := APIResponse{
		Status: "success",
		Data: map[string]interface{}{
			"count": 10,
			"items": []string{"a", "b", "c"},
		},
	}

	// Use testify for structured comparison
	assert.Equal(t, expected, response)
}
```

## CI/CD Integration

```yaml
# .github/workflows/test.yml
name: Tests

on: [push, pull_request]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Setup Go
        uses: actions/setup-go@v5
        with:
          go-version: '1.25.0'

      - name: Run tests
        run: go test ./... -v

      - name: Check for uncommitted snapshot changes
        run: |
          if [ -n "$(git status --porcelain **/.snapshots)" ]; then
            echo "Snapshot files have changed. Please update and commit."
            git diff **/.snapshots
            exit 1
          fi
```

## Recommended Libraries

| Package | Usage |
|---------|-------|
| `github.com/bradleyjkemp/cupaloy/v2` | Snapshot testing |
| `github.com/google/go-cmp/cmp` | Deep comparison |
| `github.com/stretchr/testify/assert` | Assertions |
| `github.com/sergi/go-diff/diffmatchpatch` | Diff visualization |

## Common Mistakes

| Mistake | Impact | Solution |
|---------|--------|----------|
| Oversized snapshots | Difficult review | Partial snapshots |
| Update without review | Hidden bugs | Review each update |
| Dynamic data | Flaky tests | Normalize before snapshot |
| Auto-commit updates | Unwanted changes | Strict CI check |
| Too many snapshots | Heavy maintenance | Target stable elements |

## When to Use

| Scenario | Recommended |
|----------|------------|
| Serialization output | Yes |
| API response format | Yes |
| Config/output generation | Yes |
| Highly dynamic content | No |
| Logic testing | No (assertions) |
| Frequent structure changes | With caution |

## Best practices

```go
// 1. Name snapshots clearly
func TestUserProfile_LoggedInWithAvatar(t *testing.T) { ... }

// 2. One concern per snapshot
func TestHeader(t *testing.T) { ... }
func TestContent(t *testing.T) { ... }

// 3. Use table-driven tests for variations
func TestFormatting(t *testing.T) {
	tests := []struct {
		name string
		input string
	}{
		{"simple", "hello"},
		{"unicode", "héllo"},
	}
	// ...
}

// 4. Review every snapshot update
// git diff before committing

// 5. Clean up obsolete snapshots
// Remove unused .snapshots files
```

## Related Patterns

- **Visual Regression**: Screenshot comparison
- **Contract Testing**: API structure validation
- **Golden Master**: Reference output testing

## Sources

- [Cupaloy Documentation](https://github.com/bradleyjkemp/cupaloy)
- [Effective Snapshot Testing](https://kentcdodds.com/blog/effective-snapshot-testing)
- [Go-cmp Guide](https://github.com/google/go-cmp)
