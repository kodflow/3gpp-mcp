# Session-Based Authentication

> Stateful server-side authentication with session cookies.

## Principle

```
┌────────┐        ┌────────────┐        ┌──────────────┐
│ Client │◄──────►│   Server   │◄──────►│ Session Store│
└────────┘        └────────────┘        └──────────────┘
     │                  │                      │
     │ 1. Login         │                      │
     ├─────────────────►│                      │
     │                  │ 2. Create session    │
     │                  ├─────────────────────►│
     │                  │                      │
     │ 3. Set-Cookie    │                      │
     │◄─────────────────┤                      │
     │                  │                      │
     │ 4. Request +     │                      │
     │    Cookie        │ 5. Validate session  │
     ├─────────────────►├─────────────────────►│
     │                  │                      │
```

## Go Implementation

```go
package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// Session represents a user session.
type Session struct {
	ID             string
	UserID         string
	Data           map[string]interface{}
	CreatedAt      time.Time
	ExpiresAt      time.Time
	LastAccessedAt time.Time
}

// Store defines the session storage interface.
type Store interface {
	Get(ctx context.Context, id string) (*Session, error)
	Set(ctx context.Context, session *Session) error
	Delete(ctx context.Context, id string) error
	Touch(ctx context.Context, id string) error
}

// Manager manages user sessions.
type Manager struct {
	store         Store
	ttl           time.Duration
	slidingWindow bool
}

// NewManager creates a new session manager.
func NewManager(store Store, ttl time.Duration, slidingWindow bool) *Manager {
	return &Manager{
		store:         store,
		ttl:           ttl,
		slidingWindow: slidingWindow,
	}
}

// Create creates a new session for a user.
func (m *Manager) Create(ctx context.Context, userID string, data map[string]interface{}) (string, error) {
	sessionID, err := generateSessionID()
	if err != nil {
		return "", fmt.Errorf("generating session ID: %w", err)
	}

	now := time.Now()
	session := &Session{
		ID:             sessionID,
		UserID:         userID,
		Data:           data,
		CreatedAt:      now,
		ExpiresAt:      now.Add(m.ttl),
		LastAccessedAt: now,
	}

	if err := m.store.Set(ctx, session); err != nil {
		return "", fmt.Errorf("storing session: %w", err)
	}

	return sessionID, nil
}

// Validate validates a session ID and returns the session.
func (m *Manager) Validate(ctx context.Context, sessionID string) (*Session, error) {
	if sessionID == "" {
		return nil, nil
	}

	session, err := m.store.Get(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("getting session: %w", err)
	}

	if session == nil {
		return nil, nil
	}

	// Check expiration
	if time.Now().After(session.ExpiresAt) {
		if err := m.store.Delete(ctx, sessionID); err != nil {
			return nil, fmt.Errorf("deleting expired session: %w", err)
		}
		return nil, nil
	}

	// Sliding window - extend expiration on access
	if m.slidingWindow {
		if err := m.store.Touch(ctx, sessionID); err != nil {
			return nil, fmt.Errorf("touching session: %w", err)
		}
	}

	return session, nil
}

// Destroy destroys a session.
func (m *Manager) Destroy(ctx context.Context, sessionID string) error {
	if err := m.store.Delete(ctx, sessionID); err != nil {
		return fmt.Errorf("deleting session: %w", err)
	}
	return nil
}

func generateSessionID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("reading random bytes: %w", err)
	}
	return hex.EncodeToString(b), nil
}
```

## Redis Session Store

```go
package session

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisStore implements Store using Redis.
type RedisStore struct {
	client *redis.Client
	prefix string
	ttl    time.Duration
}

// NewRedisStore creates a new Redis-backed session store.
func NewRedisStore(client *redis.Client, prefix string, ttl time.Duration) *RedisStore {
	return &RedisStore{
		client: client,
		prefix: prefix,
		ttl:    ttl,
	}
}

// Get retrieves a session by ID.
func (s *RedisStore) Get(ctx context.Context, id string) (*Session, error) {
	key := s.prefix + id
	data, err := s.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting from redis: %w", err)
	}

	var session Session
	if err := json.Unmarshal([]byte(data), &session); err != nil {
		return nil, fmt.Errorf("unmarshaling session: %w", err)
	}

	return &session, nil
}

// Set stores a session.
func (s *RedisStore) Set(ctx context.Context, session *Session) error {
	key := s.prefix + session.ID
	data, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("marshaling session: %w", err)
	}

	ttl := time.Until(session.ExpiresAt)
	if err := s.client.Set(ctx, key, data, ttl).Err(); err != nil {
		return fmt.Errorf("setting in redis: %w", err)
	}

	return nil
}

// Delete deletes a session.
func (s *RedisStore) Delete(ctx context.Context, id string) error {
	key := s.prefix + id
	if err := s.client.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("deleting from redis: %w", err)
	}
	return nil
}

// Touch updates the last accessed time and extends expiration.
func (s *RedisStore) Touch(ctx context.Context, id string) error {
	session, err := s.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("getting session: %w", err)
	}
	if session == nil {
		return fmt.Errorf("session not found")
	}

	session.LastAccessedAt = time.Now()
	session.ExpiresAt = time.Now().Add(s.ttl)

	if err := s.Set(ctx, session); err != nil {
		return fmt.Errorf("updating session: %w", err)
	}

	return nil
}
```

## HTTP Middleware

```go
package middleware

import (
	"context"
	"net/http"
	"time"
)

// CookieOptions holds cookie configuration.
type CookieOptions struct {
	HTTPOnly bool
	Secure   bool
	SameSite http.SameSite
	MaxAge   int
	Path     string
}

// DefaultCookieOptions returns secure default cookie options.
func DefaultCookieOptions() CookieOptions {
	return CookieOptions{
		HTTPOnly: true,               // Prevent XSS access
		Secure:   true,               // HTTPS only
		SameSite: http.SameSiteStrict, // CSRF protection
		MaxAge:   24 * 60 * 60,       // 24h
		Path:     "/",
	}
}

type contextKey string

const (
	sessionKey contextKey = "session"
	userIDKey  contextKey = "userID"
)

// SessionMiddleware returns a middleware that manages sessions.
func SessionMiddleware(manager *session.Manager, opts CookieOptions) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			// Get session cookie
			cookie, err := r.Cookie("sessionID")
			var sess *session.Session
			if err == nil {
				sess, err = manager.Validate(ctx, cookie.Value)
				if err != nil {
					http.Error(w, "Session validation error", http.StatusInternalServerError)
					return
				}
			}

			// Attach session to context
			if sess != nil {
				ctx = context.WithValue(ctx, sessionKey, sess)
				ctx = context.WithValue(ctx, userIDKey, sess.UserID)
			}

			// Add helper functions to context
			ctx = context.WithValue(ctx, "createSession", func(userID string) (string, error) {
				sessionID, err := manager.Create(ctx, userID, nil)
				if err != nil {
					return "", err
				}

				http.SetCookie(w, &http.Cookie{
					Name:     "sessionID",
					Value:    sessionID,
					HttpOnly: opts.HTTPOnly,
					Secure:   opts.Secure,
					SameSite: opts.SameSite,
					MaxAge:   opts.MaxAge,
					Path:     opts.Path,
				})

				return sessionID, nil
			})

			ctx = context.WithValue(ctx, "destroySession", func() error {
				if sess != nil {
					if err := manager.Destroy(ctx, sess.ID); err != nil {
						return err
					}
					http.SetCookie(w, &http.Cookie{
						Name:   "sessionID",
						Value:  "",
						MaxAge: -1,
						Path:   opts.Path,
					})
				}
				return nil
			})

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// GetSession retrieves the session from context.
func GetSession(ctx context.Context) *session.Session {
	if sess, ok := ctx.Value(sessionKey).(*session.Session); ok {
		return sess
	}
	return nil
}

// GetUserID retrieves the user ID from context.
func GetUserID(ctx context.Context) string {
	if userID, ok := ctx.Value(userIDKey).(string); ok {
		return userID
	}
	return ""
}
```

## Attack Protection

```go
package session

import (
	"context"
	"fmt"
)

// SecureManager extends Manager with security features.
type SecureManager struct {
	*Manager
}

// NewSecureManager creates a new secure session manager.
func NewSecureManager(store Store, ttl time.Duration, slidingWindow bool) *SecureManager {
	return &SecureManager{
		Manager: NewManager(store, ttl, slidingWindow),
	}
}

// Regenerate prevents session fixation by creating a new session ID.
func (m *SecureManager) Regenerate(ctx context.Context, oldSessionID string) (string, error) {
	session, err := m.store.Get(ctx, oldSessionID)
	if err != nil {
		return "", fmt.Errorf("getting old session: %w", err)
	}
	if session == nil {
		return "", fmt.Errorf("session not found")
	}

	// Create new session with same data
	newSessionID, err := m.Create(ctx, session.UserID, session.Data)
	if err != nil {
		return "", fmt.Errorf("creating new session: %w", err)
	}

	// Delete old session
	if err := m.Destroy(ctx, oldSessionID); err != nil {
		return "", fmt.Errorf("destroying old session: %w", err)
	}

	return newSessionID, nil
}

// CreateWithLimit creates a session with concurrent session limiting.
func (m *SecureManager) CreateWithLimit(ctx context.Context, userID string, maxSessions int) (string, error) {
	// Note: This requires extending the Store interface with GetByUserID
	// Implementation would fetch user sessions, remove oldest if limit exceeded
	return m.Create(ctx, userID, nil)
}

// ValidateWithIP validates session with IP binding (optional, can cause issues with mobile).
func (m *SecureManager) ValidateWithIP(ctx context.Context, sessionID, ip string) (*Session, error) {
	session, err := m.Validate(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("validating session: %w", err)
	}

	if session == nil {
		return nil, nil
	}

	if boundIP, ok := session.Data["boundIP"].(string); ok && boundIP != ip {
		if err := m.Destroy(ctx, sessionID); err != nil {
			return nil, fmt.Errorf("destroying session: %w", err)
		}
		return nil, nil
	}

	return session, nil
}
```

## Recommended Libraries

| Package | Usage |
|---------|-------|
| `github.com/gorilla/sessions` | Standard session management |
| `github.com/redis/go-redis/v9` | Redis client |
| `github.com/alexedwards/scs/v2` | Modern session manager |

## Common Mistakes

| Mistake | Impact | Solution |
|---------|--------|----------|
| Predictable session ID | Session hijacking | `crypto/rand` with 32 bytes |
| No `httpOnly` | XSS can steal cookie | Always `httpOnly: true` |
| No `secure` | MITM can intercept | Always `secure: true` in prod |
| `sameSite: 'none'` | CSRF vulnerable | `strict` or `lax` |
| In-memory store | Loss on restart | Redis, PostgreSQL, etc. |
| Session ID in URL | Leakage via Referer | Cookie only |

## When to Use

| Scenario | Recommended |
|----------|------------|
| Monolithic application | Yes |
| Need for instant revocation | Yes |
| Traditional multi-page apps | Yes |
| Stateless APIs | No (prefer JWT) |
| Microservices | No (prefer JWT) |
| SPAs with backend BFF | Yes |

## Related Patterns

- **JWT**: Stateless alternative
- **OAuth 2.0**: Often combines sessions + OAuth
- **CSRF Protection**: Necessary with sessions

## Sources

- [OWASP Session Management](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html)
- [SCS Documentation](https://github.com/alexedwards/scs)
