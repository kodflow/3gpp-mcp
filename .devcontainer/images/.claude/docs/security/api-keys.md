# API Keys Authentication

> Simple secret key authentication for APIs.

## Principle

```
┌─────────────┐                    ┌─────────────┐
│   Client    │  X-API-Key: xxx    │   Server    │
│             │───────────────────►│             │
│             │                    │ - Validate  │
│             │                    │ - Rate limit│
│             │                    │ - Log usage │
└─────────────┘                    └─────────────┘
```

## Go Implementation

```go
package apikey

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// APIKey represents an API key.
type APIKey struct {
	ID         string
	HashedKey  string
	Name       string
	OwnerID    string
	Scopes     []string
	RateLimit  int
	CreatedAt  time.Time
	ExpiresAt  *time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

// Store defines API key storage interface.
type Store interface {
	Save(ctx context.Context, key *APIKey) error
	FindByHash(ctx context.Context, hashedKey string) (*APIKey, error)
	Update(ctx context.Context, id string, updates map[string]interface{}) error
	UpdateLastUsed(ctx context.Context, id string) error
}

// Service manages API keys.
type Service struct {
	store      Store
	prefix     string
	keyLength  int
}

// NewService creates a new API key service.
func NewService(store Store) *Service {
	return &Service{
		store:     store,
		prefix:    "sk_live_",
		keyLength: 32,
	}
}

// GenerateResult holds the generated key.
type GenerateResult struct {
	Key string
	ID  string
}

// Generate creates a new API key.
func (s *Service) Generate(ctx context.Context, ownerID, name string, scopes []string, expiresInDays *int) (*GenerateResult, error) {
	// Generate random key
	rawKey := make([]byte, s.keyLength)
	if _, err := rand.Read(rawKey); err != nil {
		return nil, fmt.Errorf("generating random key: %w", err)
	}

	fullKey := s.prefix + hex.EncodeToString(rawKey)
	hashedKey := s.hash(fullKey)

	var expiresAt *time.Time
	if expiresInDays != nil {
		exp := time.Now().AddDate(0, 0, *expiresInDays)
		expiresAt = &exp
	}

	apiKey := &APIKey{
		ID:        uuid.New().String(),
		HashedKey: hashedKey,
		Name:      name,
		OwnerID:   ownerID,
		Scopes:    scopes,
		RateLimit: 1000, // requests per hour
		CreatedAt: time.Now(),
		ExpiresAt: expiresAt,
	}

	if err := s.store.Save(ctx, apiKey); err != nil {
		return nil, fmt.Errorf("saving API key: %w", err)
	}

	// Key returned ONCE - user must save it
	return &GenerateResult{
		Key: fullKey,
		ID:  apiKey.ID,
	}, nil
}

// Validate validates an API key.
func (s *Service) Validate(ctx context.Context, key string) (*APIKey, error) {
	// Check prefix
	if len(key) < len(s.prefix) || key[:len(s.prefix)] != s.prefix {
		return nil, nil
	}

	hashedKey := s.hash(key)
	apiKey, err := s.store.FindByHash(ctx, hashedKey)
	if err != nil {
		return nil, fmt.Errorf("finding API key: %w", err)
	}

	if apiKey == nil {
		return nil, nil
	}

	if apiKey.RevokedAt != nil {
		return nil, nil
	}

	if apiKey.ExpiresAt != nil && time.Now().After(*apiKey.ExpiresAt) {
		return nil, nil
	}

	// Update last used (async, don't wait)
	go func() {
		if err := s.store.UpdateLastUsed(context.Background(), apiKey.ID); err != nil {
			// Log error
		}
	}()

	return apiKey, nil
}

// Revoke revokes an API key.
func (s *Service) Revoke(ctx context.Context, id string) error {
	now := time.Now()
	updates := map[string]interface{}{
		"revokedAt": &now,
	}

	if err := s.store.Update(ctx, id, updates); err != nil {
		return fmt.Errorf("revoking API key: %w", err)
	}

	return nil
}

func (s *Service) hash(key string) string {
	hash := sha256.Sum256([]byte(key))
	return hex.EncodeToString(hash[:])
}
```

## HTTP Middleware with Rate Limiting

```go
package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// APIKeyMiddleware returns API key authentication middleware.
func APIKeyMiddleware(service *apikey.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			apiKey := r.Header.Get("X-API-Key")
			if apiKey == "" {
				apiKey = r.Header.Get("Authorization")
				if apiKey != "" && len(apiKey) > 7 {
					apiKey = apiKey[7:] // Remove "Bearer "
				}
			}

			if apiKey == "" {
				http.Error(w, `{"error": "API key required", "code": "MISSING_API_KEY"}`,
					http.StatusUnauthorized)
				return
			}

			key, err := service.Validate(ctx, apiKey)
			if err != nil {
				http.Error(w, `{"error": "Internal error"}`, http.StatusInternalServerError)
				return
			}

			if key == nil {
				http.Error(w, `{"error": "Invalid or expired API key", "code": "INVALID_API_KEY"}`,
					http.StatusUnauthorized)
				return
			}

			// Attach to context
			ctx = context.WithValue(ctx, "apiKey", key)
			ctx = context.WithValue(ctx, "ownerID", key.OwnerID)
			ctx = context.WithValue(ctx, "scopes", key.Scopes)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RateLimitData holds rate limit state.
type RateLimitData struct {
	Count   int
	ResetAt time.Time
}

// RateLimiter implements per-key rate limiting.
type RateLimiter struct {
	limits sync.Map
}

// NewRateLimiter creates a new rate limiter.
func NewRateLimiter() *RateLimiter {
	return &RateLimiter{}
}

// CheckResult holds rate limit check result.
type CheckResult struct {
	Allowed   bool
	Remaining int
	ResetAt   time.Time
}

// Check checks if a key is within rate limits.
func (rl *RateLimiter) Check(keyID string, limit int) *CheckResult {
	now := time.Now()
	windowMs := time.Hour

	value, _ := rl.limits.LoadOrStore(keyID, &RateLimitData{
		Count:   0,
		ResetAt: now.Add(windowMs),
	})

	data := value.(*RateLimitData)

	// Reset if window expired
	if now.After(data.ResetAt) {
		data.Count = 0
		data.ResetAt = now.Add(windowMs)
	}

	data.Count++

	return &CheckResult{
		Allowed:   data.Count <= limit,
		Remaining: max(0, limit-data.Count),
		ResetAt:   data.ResetAt,
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// RateLimitedAPIKey combines API key auth with rate limiting.
func RateLimitedAPIKey(service *apikey.Service, limiter *RateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			apiKeyStr := r.Header.Get("X-API-Key")

			key, err := service.Validate(ctx, apiKeyStr)
			if err != nil || key == nil {
				http.Error(w, `{"error": "Invalid API key"}`, http.StatusUnauthorized)
				return
			}

			result := limiter.Check(key.ID, key.RateLimit)

			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(key.RateLimit))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(result.Remaining))
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(result.ResetAt.Unix(), 10))

			if !result.Allowed {
				retryAfter := int(time.Until(result.ResetAt).Seconds())
				http.Error(w, fmt.Sprintf(`{"error": "Rate limit exceeded", "retryAfter": %d}`, retryAfter),
					http.StatusTooManyRequests)
				return
			}

			ctx = context.WithValue(ctx, "apiKey", key)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
```

## Scope Validation

```go
package middleware

import (
	"net/http"
)

// RequireScopes returns middleware that checks for required scopes.
func RequireScopes(requiredScopes ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			keyScopes, ok := r.Context().Value("scopes").([]string)
			if !ok {
				keyScopes = []string{}
			}

			hasAllScopes := true
			for _, required := range requiredScopes {
				found := false
				for _, scope := range keyScopes {
					if scope == required || scope == "*" {
						found = true
						break
					}
				}
				if !found {
					hasAllScopes = false
					break
				}
			}

			if !hasAllScopes {
				w.WriteHeader(http.StatusForbidden)
				fmt.Fprintf(w, `{
					"error": "Insufficient permissions",
					"required": %q,
					"actual": %q
				}`, requiredScopes, keyScopes)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// Usage
func SetupRoutes(mux *http.ServeMux, service *apikey.Service) {
	mux.Handle("/users",
		APIKeyMiddleware(service)(
			RequireScopes("users:read")(http.HandlerFunc(getUsers))))
	mux.Handle("/users/create",
		APIKeyMiddleware(service)(
			RequireScopes("users:write")(http.HandlerFunc(createUser))))
	mux.Handle("/users/delete",
		APIKeyMiddleware(service)(
			RequireScopes("users:delete")(http.HandlerFunc(deleteUser))))
}
```

## Recommended Libraries

| Package | Usage |
|---------|-------|
| `github.com/google/uuid` | Unique ID generation |
| `golang.org/x/time/rate` | Rate limiting |

## Common Mistakes

| Mistake | Impact | Solution |
|---------|--------|----------|
| Store key in plaintext | Breach = total access | Hash with SHA-256 |
| Key in URL params | Leakage via logs/referer | Header `X-API-Key` |
| No rate limiting | DoS, abuse | Limit per key |
| Key without expiration | Permanent compromise | Expiration + rotation |
| No scopes | Over-permission | Granular scopes |
| Key shared between services | Large blast radius | Key per service/usage |

## Best Practices

```go
package apikey

// Identifiable prefixes
const (
	PrefixLive = "sk_live_" // Production
	PrefixTest = "sk_test_" // Development
	PrefixPub  = "pk_"      // Public (limited scope)
)

// KeyRotation handles API key rotation.
type KeyRotation struct {
	service *Service
}

// Rotate rotates an API key with grace period.
func (kr *KeyRotation) Rotate(ctx context.Context, oldKeyID string) (*GenerateResult, error) {
	// Get old key details
	oldKey, err := kr.service.store.FindByID(ctx, oldKeyID)
	if err != nil {
		return nil, fmt.Errorf("finding old key: %w", err)
	}

	// Create new key with same config
	newKey, err := kr.service.Generate(ctx, oldKey.OwnerID,
		oldKey.Name+" (rotated)", oldKey.Scopes, nil)
	if err != nil {
		return nil, fmt.Errorf("generating new key: %w", err)
	}

	// Grace period - old key valid for 24h
	exp := time.Now().Add(24 * time.Hour)
	updates := map[string]interface{}{
		"expiresAt": &exp,
	}

	if err := kr.service.store.Update(ctx, oldKeyID, updates); err != nil {
		return nil, fmt.Errorf("updating old key: %w", err)
	}

	return newKey, nil
}

// UsageLog represents API key usage logging.
type UsageLog struct {
	KeyID      string
	Endpoint   string
	Method     string
	StatusCode int
	Timestamp  time.Time
	IP         string
}
```

## When to Use

| Scenario | Recommended |
|----------|------------|
| Public APIs (third-party) | Yes |
| Simple integrations | Yes |
| Machine-to-machine | Yes (or Client Credentials) |
| User authentication | No (prefer OAuth/sessions) |
| Browser/frontend | No (key exposed) |

## Related Patterns

- **OAuth 2.0 Client Credentials**: More secure alternative for M2M
- **JWT**: Can complement API keys for authorization
- **Rate Limiting**: Essential with API keys

## Sources

- [Stripe API Keys Design](https://stripe.com/docs/keys)
- [GitHub API Token Design](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/creating-a-personal-access-token)
