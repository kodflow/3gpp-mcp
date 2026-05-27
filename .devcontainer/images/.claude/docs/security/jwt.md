# JSON Web Tokens (JWT)

> Signed and self-contained tokens for stateless authentication.

## Structure

```
header.payload.signature

eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.
eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4ifQ.
SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c
```

## Standard Claims

| Claim | Name | Description |
|-------|------|-------------|
| `iss` | Issuer | Token issuer |
| `sub` | Subject | Unique user identifier |
| `aud` | Audience | Authorized recipients |
| `exp` | Expiration | Expiration timestamp |
| `nbf` | Not Before | Valid from |
| `iat` | Issued At | Creation date |
| `jti` | JWT ID | Unique token identifier |

## Go Implementation

```go
package jwt

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims represents JWT claims.
type Claims struct {
	UserID      string   `json:"sub"`
	Email       string   `json:"email"`
	Role        string   `json:"role"`
	Permissions []string `json:"permissions"`
	jwt.RegisteredClaims
}

// TokenPair represents access and refresh tokens.
type TokenPair struct {
	AccessToken  string
	RefreshToken string
}

// Service handles JWT operations.
type Service struct {
	accessSecret  []byte
	refreshSecret []byte
	accessTTL     time.Duration
	refreshTTL    time.Duration
	issuer        string
	audience      string
}

// NewService creates a new JWT service.
func NewService(accessSecret, refreshSecret []byte, accessTTL, refreshTTL time.Duration, issuer, audience string) *Service {
	return &Service{
		accessSecret:  accessSecret,
		refreshSecret: refreshSecret,
		accessTTL:     accessTTL,
		refreshTTL:    refreshTTL,
		issuer:        issuer,
		audience:      audience,
	}
}

// User represents a user for token generation.
type User struct {
	ID          string
	Email       string
	Role        string
	Permissions []string
}

// GenerateTokens generates access and refresh tokens.
func (s *Service) GenerateTokens(user User) (*TokenPair, error) {
	now := time.Now()

	// Access token
	accessClaims := &Claims{
		UserID:      user.ID,
		Email:       user.Email,
		Role:        user.Role,
		Permissions: user.Permissions,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.issuer,
			Audience:  jwt.ClaimStrings{s.audience},
			ExpiresAt: jwt.NewNumericDate(now.Add(s.accessTTL)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}

	accessToken := jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims)
	accessString, err := accessToken.SignedString(s.accessSecret)
	if err != nil {
		return nil, fmt.Errorf("signing access token: %w", err)
	}

	// Refresh token
	refreshClaims := &jwt.RegisteredClaims{
		Subject:   user.ID,
		Issuer:    s.issuer,
		ExpiresAt: jwt.NewNumericDate(now.Add(s.refreshTTL)),
		IssuedAt:  jwt.NewNumericDate(now),
	}

	refreshToken := jwt.NewWithClaims(jwt.SigningMethodHS256, refreshClaims)
	refreshString, err := refreshToken.SignedString(s.refreshSecret)
	if err != nil {
		return nil, fmt.Errorf("signing refresh token: %w", err)
	}

	return &TokenPair{
		AccessToken:  accessString,
		RefreshToken: refreshString,
	}, nil
}

var (
	ErrTokenExpired = errors.New("token expired")
	ErrInvalidToken = errors.New("invalid token")
)

// VerifyAccessToken verifies and parses an access token.
func (s *Service) VerifyAccessToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return s.accessSecret, nil
	})

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpired
		}
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrInvalidToken
	}

	// Verify issuer and audience
	if claims.Issuer != s.issuer {
		return nil, fmt.Errorf("%w: invalid issuer", ErrInvalidToken)
	}

	if !claims.VerifyAudience(s.audience, true) {
		return nil, fmt.Errorf("%w: invalid audience", ErrInvalidToken)
	}

	return claims, nil
}

// UserRepository defines user data access.
type UserRepository interface {
	FindByID(ctx context.Context, id string) (*User, error)
}

// RefreshTokens generates new tokens from a refresh token.
func (s *Service) RefreshTokens(ctx context.Context, refreshToken string, userRepo UserRepository) (*TokenPair, error) {
	token, err := jwt.ParseWithClaims(refreshToken, &jwt.RegisteredClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return s.refreshSecret, nil
	})

	if err != nil {
		return nil, fmt.Errorf("parsing refresh token: %w", err)
	}

	claims, ok := token.Claims.(*jwt.RegisteredClaims)
	if !ok || !token.Valid {
		return nil, ErrInvalidToken
	}

	user, err := userRepo.FindByID(ctx, claims.Subject)
	if err != nil {
		return nil, fmt.Errorf("finding user: %w", err)
	}
	if user == nil {
		return nil, ErrInvalidToken
	}

	return s.GenerateTokens(*user)
}
```

## HTTP Middleware

```go
package middleware

import (
	"context"
	"net/http"
	"strings"
)

type contextKey string

const userClaimsKey contextKey = "userClaims"

// AuthMiddleware returns JWT authentication middleware.
func AuthMiddleware(jwtService *jwt.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				http.Error(w, `{"error": "Missing token"}`, http.StatusUnauthorized)
				return
			}

			if !strings.HasPrefix(authHeader, "Bearer ") {
				http.Error(w, `{"error": "Invalid authorization header"}`, http.StatusUnauthorized)
				return
			}

			token := strings.TrimPrefix(authHeader, "Bearer ")

			claims, err := jwtService.VerifyAccessToken(token)
			if err != nil {
				if errors.Is(err, jwt.ErrTokenExpired) {
					http.Error(w, `{"error": "Token expired"}`, http.StatusUnauthorized)
					return
				}
				http.Error(w, `{"error": "Invalid token"}`, http.StatusUnauthorized)
				return
			}

			ctx := context.WithValue(r.Context(), userClaimsKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// GetClaims retrieves JWT claims from context.
func GetClaims(ctx context.Context) *jwt.Claims {
	if claims, ok := ctx.Value(userClaimsKey).(*jwt.Claims); ok {
		return claims
	}
	return nil
}
```

## Refresh Token Rotation

```go
package jwt

import (
	"context"
	"fmt"
	"sync"
)

// RefreshTokenRotation handles refresh token rotation with reuse detection.
type RefreshTokenRotation struct {
	service    *Service
	usedTokens sync.Map
}

// NewRefreshTokenRotation creates a new token rotation handler.
func NewRefreshTokenRotation(service *Service) *RefreshTokenRotation {
	return &RefreshTokenRotation{
		service: service,
	}
}

// Rotate rotates a refresh token, detecting reuse.
func (r *RefreshTokenRotation) Rotate(ctx context.Context, refreshToken string, userRepo UserRepository) (*TokenPair, error) {
	// Extract JTI from token
	jti, err := r.extractJTI(refreshToken)
	if err != nil {
		return nil, fmt.Errorf("extracting JTI: %w", err)
	}

	// Detect token reuse (potential theft)
	if _, exists := r.usedTokens.LoadOrStore(jti, true); exists {
		// Token reused - revoke all user tokens
		if err := r.revokeAllUserTokens(ctx, refreshToken); err != nil {
			return nil, fmt.Errorf("revoking tokens: %w", err)
		}
		return nil, fmt.Errorf("token reuse detected - security violation")
	}

	// Generate new token pair
	return r.service.RefreshTokens(ctx, refreshToken, userRepo)
}

func (r *RefreshTokenRotation) extractJTI(tokenString string) (string, error) {
	token, _, err := jwt.NewParser().ParseUnverified(tokenString, &jwt.RegisteredClaims{})
	if err != nil {
		return "", fmt.Errorf("parsing token: %w", err)
	}

	if claims, ok := token.Claims.(*jwt.RegisteredClaims); ok {
		return claims.ID, nil
	}

	return "", fmt.Errorf("invalid claims")
}

func (r *RefreshTokenRotation) revokeAllUserTokens(ctx context.Context, token string) error {
	// Implementation would revoke all tokens for the user
	return nil
}
```

## Signing Algorithms

| Algo | Type | Recommendation |
|------|------|----------------|
| HS256 | Symmetric (HMAC) | Dev/simple apps |
| RS256 | Asymmetric (RSA) | Production, microservices |
| ES256 | Asymmetric (ECDSA) | Better performance than RSA |
| EdDSA | Asymmetric (Ed25519) | Modern, fast |

## Recommended Libraries

| Package | Usage |
|---------|-------|
| `github.com/golang-jwt/jwt/v5` | Standard JWT library |
| `github.com/lestrrat-go/jwx/v2` | Complete JWT/JWS/JWE/JWK |

## Common Mistakes

| Mistake | Impact | Solution |
|---------|--------|----------|
| Secret too short | Brute force possible | Min 256 bits (32 bytes) |
| Token in localStorage | XSS vulnerability | HttpOnly cookie or memory |
| No expiration | Eternal token if stolen | Always use `exp` claim |
| Sensitive data in payload | Data exposure | Payload = public, min data |
| Verification without `aud`/`iss` | Token confusion | Always verify claims |
| HS256 with predictable secret | Token forgery | Cryptographic random secrets |

## When to Use

| Scenario | Recommended |
|----------|------------|
| Stateless APIs | Yes |
| Microservices | Yes (with RS256/ES256) |
| SPAs | Yes (with refresh rotation) |
| Mobile apps | Yes |
| Long-lived sessions | No (prefer sessions) |
| Sensitive data in token | No |

## Best Practices

```go
package jwt

// Best practices configuration
const (
	// 1. Short-lived access token
	AccessTokenTTL = 15 * time.Minute // Max 1h

	// 2. Long-lived refresh token with rotation
	RefreshTokenTTL = 7 * 24 * time.Hour

	// Minimum secret length
	MinSecretLength = 32 // 256 bits
)

// TokenBlacklist manages revoked tokens.
type TokenBlacklist struct {
	redis *redis.Client
}

// Revoke adds a token to the blacklist.
func (b *TokenBlacklist) Revoke(ctx context.Context, jti string, exp time.Time) error {
	key := "blacklist:" + jti
	ttl := time.Until(exp)

	if err := b.redis.Set(ctx, key, "1", ttl).Err(); err != nil {
		return fmt.Errorf("setting blacklist: %w", err)
	}

	return nil
}

// IsRevoked checks if a token is revoked.
func (b *TokenBlacklist) IsRevoked(ctx context.Context, jti string) (bool, error) {
	key := "blacklist:" + jti
	exists, err := b.redis.Exists(ctx, key).Result()
	if err != nil {
		return false, fmt.Errorf("checking blacklist: %w", err)
	}

	return exists == 1, nil
}

// MinimalClaims demonstrates minimal claim set.
type MinimalClaims struct {
	jwt.RegisteredClaims
	Role string `json:"role"` // For authorization
	// NO: email, name, sensitive data
}
```

## Related Patterns

- **OAuth 2.0**: JWT often used as access token
- **Session-Auth**: Alternative with server state
- **RBAC**: Permissions in claims

## Sources

- [JWT RFC 7519](https://datatracker.ietf.org/doc/html/rfc7519)
- [JWT Best Practices RFC 8725](https://datatracker.ietf.org/doc/html/rfc8725)
- [jwt.io](https://jwt.io/)
