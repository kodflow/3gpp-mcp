# Security Patterns

Application security patterns.

## Authentication Patterns

### 1. Session-Based Authentication

> Authentication with server-side session.

```go
package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"sync"
	"time"
)

var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrInvalidSession = errors.New("invalid session")
)

// SessionData represents a user session.
type SessionData struct {
	UserID    string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// User represents an authenticated user.
type User struct {
	ID          string
	Email       string
	Role        string
	Permissions []string
}

// SessionAuth manages session-based authentication.
type SessionAuth struct {
	sessions sync.Map
	userRepo UserRepository
}

// UserRepository provides user data access.
type UserRepository interface {
	FindByID(ctx context.Context, id string) (*User, error)
	ValidateCredentials(ctx context.Context, email, password string) (*User, error)
}

// NewSessionAuth creates a new session auth manager.
func NewSessionAuth(userRepo UserRepository) *SessionAuth {
	return &SessionAuth{
		userRepo: userRepo,
	}
}

// Login authenticates a user and creates a session.
func (s *SessionAuth) Login(ctx context.Context, email, password string) (string, error) {
	user, err := s.userRepo.ValidateCredentials(ctx, email, password)
	if err != nil {
		return "", err
	}
	if user == nil {
		return "", ErrUnauthorized
	}

	sessionID, err := generateSessionID()
	if err != nil {
		return "", err
	}

	s.sessions.Store(sessionID, &SessionData{
		UserID:    user.ID,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})

	return sessionID, nil
}

// Validate checks if a session is valid and returns the user.
func (s *SessionAuth) Validate(ctx context.Context, sessionID string) (*User, error) {
	value, ok := s.sessions.Load(sessionID)
	if !ok {
		return nil, nil
	}

	session := value.(*SessionData)
	if time.Now().After(session.ExpiresAt) {
		s.sessions.Delete(sessionID)
		return nil, nil
	}

	return s.userRepo.FindByID(ctx, session.UserID)
}

// Logout removes a session.
func (s *SessionAuth) Logout(sessionID string) {
	s.sessions.Delete(sessionID)
}

func generateSessionID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Middleware protects routes with session authentication.
func (s *SessionAuth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("sessionId")
		if err != nil {
			http.Error(w, `{"error":"Not authenticated"}`, http.StatusUnauthorized)
			return
		}

		user, err := s.Validate(r.Context(), cookie.Value)
		if err != nil {
			http.Error(w, `{"error":"Server error"}`, http.StatusInternalServerError)
			return
		}
		if user == nil {
			http.Error(w, `{"error":"Invalid session"}`, http.StatusUnauthorized)
			return
		}

		// Store user in context
		ctx := context.WithValue(r.Context(), userContextKey{}, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type userContextKey struct{}

// UserFromContext retrieves the user from the request context.
func UserFromContext(ctx context.Context) (*User, bool) {
	user, ok := ctx.Value(userContextKey{}).(*User)
	return user, ok
}
```

**Advantages:** Immediate revocation, server control.
**Disadvantages:** Server state, complex scaling.
**When:** Monolithic applications, need for revocation.

---

### 2. Token-Based Authentication (JWT)

> Stateless authentication with signed tokens.

```go
package auth

import (
	"context"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TokenPayload represents JWT claims.
type TokenPayload struct {
	UserID      string   `json:"userId"`
	Role        string   `json:"role"`
	Permissions []string `json:"permissions"`
	jwt.RegisteredClaims
}

// JWTAuth manages JWT-based authentication.
type JWTAuth struct {
	secret           []byte
	accessTokenTTL   time.Duration
	refreshTokenTTL  time.Duration
	userRepo         UserRepository
}

// NewJWTAuth creates a new JWT auth manager.
func NewJWTAuth(secret string, userRepo UserRepository) *JWTAuth {
	return &JWTAuth{
		secret:          []byte(secret),
		accessTokenTTL:  15 * time.Minute,
		refreshTokenTTL: 7 * 24 * time.Hour,
		userRepo:        userRepo,
	}
}

// TokenPair holds access and refresh tokens.
type TokenPair struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
}

// GenerateTokens creates access and refresh tokens for a user.
func (j *JWTAuth) GenerateTokens(user *User) (*TokenPair, error) {
	now := time.Now()

	// Access token
	accessClaims := &TokenPayload{
		UserID:      user.ID,
		Role:        user.Role,
		Permissions: user.Permissions,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(j.accessTokenTTL)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}

	accessToken := jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims)
	accessTokenString, err := accessToken.SignedString(j.secret)
	if err != nil {
		return nil, err
	}

	// Refresh token
	refreshClaims := &jwt.RegisteredClaims{
		Subject:   user.ID,
		ExpiresAt: jwt.NewNumericDate(now.Add(j.refreshTokenTTL)),
		IssuedAt:  jwt.NewNumericDate(now),
	}

	refreshToken := jwt.NewWithClaims(jwt.SigningMethodHS256, refreshClaims)
	refreshTokenString, err := refreshToken.SignedString(j.secret)
	if err != nil {
		return nil, err
	}

	return &TokenPair{
		AccessToken:  accessTokenString,
		RefreshToken: refreshTokenString,
	}, nil
}

// VerifyAccessToken validates an access token and returns the payload.
func (j *JWTAuth) VerifyAccessToken(tokenString string) (*TokenPayload, error) {
	token, err := jwt.ParseWithClaims(tokenString, &TokenPayload{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("invalid signing method")
		}
		return j.secret, nil
	})

	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(*TokenPayload); ok && token.Valid {
		return claims, nil
	}

	return nil, errors.New("invalid token")
}

// RefreshTokens generates new tokens using a refresh token.
func (j *JWTAuth) RefreshTokens(ctx context.Context, refreshToken string) (*TokenPair, error) {
	token, err := jwt.ParseWithClaims(refreshToken, &jwt.RegisteredClaims{}, func(token *jwt.Token) (interface{}, error) {
		return j.secret, nil
	})

	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*jwt.RegisteredClaims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid refresh token")
	}

	user, err := j.userRepo.FindByID(ctx, claims.Subject)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, ErrUnauthorized
	}

	return j.GenerateTokens(user)
}

// RefreshTokenStore manages refresh token rotation.
type RefreshTokenStore struct {
	tokens sync.Map
}

// TokenData holds refresh token metadata.
type TokenData struct {
	UserID string
	Used   bool
}

// NewRefreshTokenStore creates a new token store.
func NewRefreshTokenStore() *RefreshTokenStore {
	return &RefreshTokenStore{}
}

// Store saves a refresh token.
func (r *RefreshTokenStore) Store(token, userID string) {
	r.tokens.Store(token, &TokenData{
		UserID: userID,
		Used:   false,
	})
}

// Validate checks if a token is valid and marks it as used.
func (r *RefreshTokenStore) Validate(token string) (string, error) {
	value, ok := r.tokens.Load(token)
	if !ok {
		return "", errors.New("token not found")
	}

	data := value.(*TokenData)
	if data.Used {
		// Token reuse detected - potential theft
		r.RevokeAllForUser(data.UserID)
		return "", errors.New("token reuse detected")
	}

	data.Used = true
	return data.UserID, nil
}

// RevokeAllForUser revokes all tokens for a user.
func (r *RefreshTokenStore) RevokeAllForUser(userID string) {
	r.tokens.Range(func(key, value interface{}) bool {
		if data := value.(*TokenData); data.UserID == userID {
			r.tokens.Delete(key)
		}
		return true
	})
}
```

**Advantages:** Stateless, scalable, microservices.
**Disadvantages:** Complex revocation, token size.
**When:** APIs, SPAs, microservices.

---

### 3. OAuth 2.0 / OpenID Connect

> Delegated authentication via external provider.

```go
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OAuth2Provider represents an OAuth 2.0 provider.
type OAuth2Provider struct {
	AuthorizationEndpoint string
	TokenEndpoint         string
	UserInfoEndpoint      string
}

// OAuth2Client handles OAuth 2.0 flows.
type OAuth2Client struct {
	clientID     string
	clientSecret string
	redirectURI  string
	provider     *OAuth2Provider
	httpClient   *http.Client
}

// NewOAuth2Client creates a new OAuth 2.0 client.
func NewOAuth2Client(clientID, clientSecret, redirectURI string, provider *OAuth2Provider) *OAuth2Client {
	return &OAuth2Client{
		clientID:     clientID,
		clientSecret: clientSecret,
		redirectURI:  redirectURI,
		provider:     provider,
		httpClient:   &http.Client{Timeout: 10 * time.Second},
	}
}

// GetAuthorizationURL generates the OAuth authorization URL.
func (o *OAuth2Client) GetAuthorizationURL(state string, scopes []string) string {
	params := url.Values{
		"client_id":     {o.clientID},
		"redirect_uri":  {o.redirectURI},
		"response_type": {"code"},
		"scope":         {strings.Join(scopes, " ")},
		"state":         {state},
	}
	return o.provider.AuthorizationEndpoint + "?" + params.Encode()
}

// TokenResponse represents an OAuth token response.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

// ExchangeCode exchanges an authorization code for tokens.
func (o *OAuth2Client) ExchangeCode(ctx context.Context, code string) (*TokenResponse, error) {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {o.redirectURI},
		"client_id":     {o.clientID},
		"client_secret": {o.clientSecret},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.provider.TokenEndpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := o.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var tokenResp TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, err
	}

	return &tokenResp, nil
}

// UserInfo represents user information from the provider.
type UserInfo struct {
	Sub   string `json:"sub"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

// GetUserInfo retrieves user information using an access token.
func (o *OAuth2Client) GetUserInfo(ctx context.Context, accessToken string) (*UserInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.provider.UserInfoEndpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := o.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var userInfo UserInfo
	if err := json.NewDecoder(resp.Body).Decode(&userInfo); err != nil {
		return nil, err
	}

	return &userInfo, nil
}

// PKCEClient handles PKCE (Proof Key for Code Exchange) for public clients.
type PKCEClient struct{}

// GenerateCodeVerifier creates a random code verifier.
func (p *PKCEClient) GenerateCodeVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64URLEncode(b), nil
}

// GenerateCodeChallenge creates a code challenge from a verifier.
func (p *PKCEClient) GenerateCodeChallenge(verifier string) string {
	hash := sha256.Sum256([]byte(verifier))
	return base64URLEncode(hash[:])
}

func base64URLEncode(data []byte) string {
	return strings.TrimRight(base64.URLEncoding.EncodeToString(data), "=")
}
```

**Flows :**

- **Authorization Code**: Web apps with backend
- **Authorization Code + PKCE**: SPAs, mobile
- **Client Credentials**: Machine-to-machine
- **Implicit**: Deprecated

**When:** Social login, SSO, third-party APIs.

---

### 4. API Key Authentication

> Simple authentication by API key.

```go
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

// ApiKeyData represents API key metadata.
type ApiKeyData struct {
	ID        string
	UserID    string
	HashedKey string
	Scopes    []string
	CreatedAt time.Time
	ExpiresAt *time.Time
	RevokedAt *time.Time
	LastUsed  *time.Time
}

// ApiKeyStore provides API key storage.
type ApiKeyStore interface {
	FindByHash(ctx context.Context, hashedKey string) (*ApiKeyData, error)
	Create(ctx context.Context, data *ApiKeyData) error
	UpdateLastUsed(ctx context.Context, id string) error
}

// ApiKeyAuth manages API key authentication.
type ApiKeyAuth struct {
	keyStore ApiKeyStore
	prefix   string
}

// NewApiKeyAuth creates a new API key auth manager.
func NewApiKeyAuth(keyStore ApiKeyStore, prefix string) *ApiKeyAuth {
	return &ApiKeyAuth{
		keyStore: keyStore,
		prefix:   prefix,
	}
}

// Validate checks if an API key is valid.
func (a *ApiKeyAuth) Validate(ctx context.Context, apiKey string) (*ApiKeyData, error) {
	hashedKey := a.hash(apiKey)
	keyData, err := a.keyStore.FindByHash(ctx, hashedKey)
	if err != nil {
		return nil, err
	}
	if keyData == nil {
		return nil, nil
	}

	if keyData.ExpiresAt != nil && time.Now().After(*keyData.ExpiresAt) {
		return nil, nil
	}
	if keyData.RevokedAt != nil {
		return nil, nil
	}

	// Update last used asynchronously
	go func() {
		_ = a.keyStore.UpdateLastUsed(context.Background(), keyData.ID)
	}()

	return keyData, nil
}

// GenerateKey creates a new API key for a user.
func (a *ApiKeyAuth) GenerateKey(ctx context.Context, userID string, scopes []string) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}

	key := a.prefix + "_" + hex.EncodeToString(b)
	hashedKey := a.hash(key)

	err := a.keyStore.Create(ctx, &ApiKeyData{
		UserID:    userID,
		HashedKey: hashedKey,
		Scopes:    scopes,
		CreatedAt: time.Now(),
	})
	if err != nil {
		return "", err
	}

	return key, nil // Only returned once!
}

func (a *ApiKeyAuth) hash(key string) string {
	hash := sha256.Sum256([]byte(key))
	return hex.EncodeToString(hash[:])
}

// RateLimitedApiKey provides rate limiting per API key.
type RateLimitedApiKey struct {
	mu     sync.RWMutex
	limits map[string]*rateLimitData
}

type rateLimitData struct {
	count   int
	resetAt time.Time
}

// NewRateLimitedApiKey creates a new rate limiter.
func NewRateLimitedApiKey() *RateLimitedApiKey {
	return &RateLimitedApiKey{
		limits: make(map[string]*rateLimitData),
	}
}

// CheckLimit checks if the API key is within rate limits.
func (r *RateLimitedApiKey) CheckLimit(apiKey string, limit int, windowMs int) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	data, exists := r.limits[apiKey]

	if !exists || now.After(data.resetAt) {
		r.limits[apiKey] = &rateLimitData{
			count:   1,
			resetAt: now.Add(time.Duration(windowMs) * time.Millisecond),
		}
		return true
	}

	if data.count >= limit {
		return false
	}

	data.count++
	return true
}
```

**When:** Public APIs, simple integrations.
**Related to:** Rate Limiting.

---

## Authorization Patterns

### 5. Role-Based Access Control (RBAC)

> Role-based permissions.

```go
package auth

import (
	"context"
	"errors"
	"net/http"
)

// Role represents a user role.
type Role string

const (
	RoleAdmin  Role = "admin"
	RoleEditor Role = "editor"
	RoleViewer Role = "viewer"
)

// Permission represents an action permission.
type Permission string

const (
	PermRead   Permission = "read"
	PermWrite  Permission = "write"
	PermDelete Permission = "delete"
	PermAdmin  Permission = "admin"
)

var rolePermissions = map[Role][]Permission{
	RoleAdmin:  {PermRead, PermWrite, PermDelete, PermAdmin},
	RoleEditor: {PermRead, PermWrite},
	RoleViewer: {PermRead},
}

// RBAC manages role-based access control.
type RBAC struct{}

// NewRBAC creates a new RBAC manager.
func NewRBAC() *RBAC {
	return &RBAC{}
}

// HasPermission checks if a user has a specific permission.
func (r *RBAC) HasPermission(user *User, permission Permission) bool {
	permissions := rolePermissions[Role(user.Role)]
	for _, p := range permissions {
		if p == permission {
			return true
		}
	}
	return false
}

// HasRole checks if a user has a specific role.
func (r *RBAC) HasRole(user *User, role Role) bool {
	return Role(user.Role) == role
}

// HasAnyRole checks if a user has any of the specified roles.
func (r *RBAC) HasAnyRole(user *User, roles []Role) bool {
	for _, role := range roles {
		if Role(user.Role) == role {
			return true
		}
	}
	return false
}

// RequirePermission returns middleware that checks for a permission.
func (r *RBAC) RequirePermission(permission Permission) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			user, ok := UserFromContext(req.Context())
			if !ok {
				http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
				return
			}

			if !r.HasPermission(user, permission) {
				http.Error(w, `{"error":"Forbidden"}`, http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, req)
		})
	}
}

// RequireRole returns middleware that checks for a role.
func (r *RBAC) RequireRole(role Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			user, ok := UserFromContext(req.Context())
			if !ok {
				http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
				return
			}

			if !r.HasRole(user, role) {
				http.Error(w, `{"error":"Forbidden"}`, http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, req)
		})
	}
}

// Example usage:
// http.Handle("/articles", rbac.RequirePermission(PermWrite)(articleHandler))
```

**Advantages:** Simple, understandable.
**Disadvantages:** Limited granularity.
**When:** Applications with clear roles.

---

### 6. Attribute-Based Access Control (ABAC)

> Attribute and context-based permissions.

```go
package auth

import (
	"context"
	"strings"
)

// Policy represents an access control policy.
type Policy struct {
	Effect     string      `json:"effect"` // "allow" or "deny"
	Conditions []Condition `json:"conditions"`
}

// Condition represents a policy condition.
type Condition struct {
	Attribute string      `json:"attribute"`
	Operator  string      `json:"operator"` // "eq", "in", "gt", "lt", "contains"
	Value     interface{} `json:"value"`
}

// AccessRequest represents a request for access evaluation.
type AccessRequest struct {
	Subject     map[string]interface{} `json:"subject"`     // User attributes
	Resource    map[string]interface{} `json:"resource"`    // Resource attributes
	Action      string                 `json:"action"`
	Environment map[string]interface{} `json:"environment"` // Time, location, etc.
}

// ABAC manages attribute-based access control.
type ABAC struct {
	policies []Policy
}

// NewABAC creates a new ABAC manager.
func NewABAC(policies []Policy) *ABAC {
	return &ABAC{
		policies: policies,
	}
}

// Evaluate checks if an access request should be allowed.
func (a *ABAC) Evaluate(ctx context.Context, request *AccessRequest) bool {
	for _, policy := range a.policies {
		matches := true
		for _, cond := range policy.Conditions {
			if !a.evaluateCondition(cond, request) {
				matches = false
				break
			}
		}

		if matches {
			return policy.Effect == "allow"
		}
	}
	return false // Default deny
}

func (a *ABAC) evaluateCondition(condition Condition, request *AccessRequest) bool {
	value := a.getAttribute(condition.Attribute, request)

	switch condition.Operator {
	case "eq":
		return value == condition.Value
	case "in":
		if slice, ok := condition.Value.([]interface{}); ok {
			for _, v := range slice {
				if v == value {
					return true
				}
			}
		}
		return false
	case "gt":
		if vFloat, ok := value.(float64); ok {
			if cFloat, ok := condition.Value.(float64); ok {
				return vFloat > cFloat
			}
		}
		return false
	case "lt":
		if vFloat, ok := value.(float64); ok {
			if cFloat, ok := condition.Value.(float64); ok {
				return vFloat < cFloat
			}
		}
		return false
	case "contains":
		if vStr, ok := value.(string); ok {
			if cStr, ok := condition.Value.(string); ok {
				return strings.Contains(vStr, cStr)
			}
		}
		return false
	default:
		return false
	}
}

func (a *ABAC) getAttribute(path string, request *AccessRequest) interface{} {
	parts := strings.Split(path, ".")
	if len(parts) == 0 {
		return nil
	}

	var obj map[string]interface{}
	switch parts[0] {
	case "subject":
		obj = request.Subject
	case "resource":
		obj = request.Resource
	case "environment":
		obj = request.Environment
	case "action":
		return request.Action
	default:
		return nil
	}

	value := interface{}(obj)
	for _, key := range parts[1:] {
		if m, ok := value.(map[string]interface{}); ok {
			value = m[key]
		} else {
			return nil
		}
	}

	return value
}

// Example policies:
/*
policies := []Policy{
	{
		Effect: "allow",
		Conditions: []Condition{
			{Attribute: "subject.role", Operator: "eq", Value: "admin"},
		},
	},
	{
		Effect: "allow",
		Conditions: []Condition{
			{Attribute: "action", Operator: "eq", Value: "read"},
			{Attribute: "resource.isPublic", Operator: "eq", Value: true},
		},
	},
}
*/
```

**Advantages:** Flexible, contextual.
**Disadvantages:** Complex, performance.
**When:** Complex rules, multi-tenant, compliance.

---

### 7. Policy-Based Access Control

> Declarative policies.

```go
package auth

import (
	"context"
	"regexp"
	"strings"
)

// PolicyDocument represents a collection of policy statements.
type PolicyDocument struct {
	Version    string      `json:"version"`
	Statements []Statement `json:"statements"`
}

// Statement represents a policy statement.
type Statement struct {
	Effect     string                 `json:"effect"` // "Allow" or "Deny"
	Actions    []string               `json:"actions"`
	Resources  []string               `json:"resources"`
	Conditions map[string]interface{} `json:"conditions,omitempty"`
}

// PolicyEngine evaluates access policies.
type PolicyEngine struct{}

// NewPolicyEngine creates a new policy engine.
func NewPolicyEngine() *PolicyEngine {
	return &PolicyEngine{}
}

// Evaluate checks if an action on a resource is allowed.
func (p *PolicyEngine) Evaluate(
	ctx context.Context,
	policies []PolicyDocument,
	action string,
	resource string,
	contextData map[string]interface{},
) bool {
	allowed := false

	for _, policy := range policies {
		for _, statement := range policy.Statements {
			if !p.matchesAction(statement.Actions, action) {
				continue
			}
			if !p.matchesResource(statement.Resources, resource) {
				continue
			}
			if !p.matchesConditions(statement.Conditions, contextData) {
				continue
			}

			if statement.Effect == "Deny" {
				return false // Explicit deny
			}
			allowed = true
		}
	}

	return allowed
}

func (p *PolicyEngine) matchesAction(patterns []string, action string) bool {
	for _, pattern := range patterns {
		if p.matches(pattern, action) {
			return true
		}
	}
	return false
}

func (p *PolicyEngine) matchesResource(patterns []string, resource string) bool {
	for _, pattern := range patterns {
		if p.matches(pattern, resource) {
			return true
		}
	}
	return false
}

func (p *PolicyEngine) matches(pattern, value string) bool {
	if pattern == "*" {
		return true
	}

	// Convert wildcard pattern to regex
	regexPattern := "^" + strings.ReplaceAll(regexp.QuoteMeta(pattern), "\\*", ".*") + "$"
	matched, _ := regexp.MatchString(regexPattern, value)
	return matched
}

func (p *PolicyEngine) matchesConditions(conditions map[string]interface{}, contextData map[string]interface{}) bool {
	if conditions == nil {
		return true
	}

	for key, expectedValue := range conditions {
		actualValue, ok := contextData[key]
		if !ok || actualValue != expectedValue {
			return false
		}
	}

	return true
}

// Example policy (AWS IAM style):
/*
policy := PolicyDocument{
	Version: "2024-01-01",
	Statements: []Statement{
		{
			Effect:    "Allow",
			Actions:   []string{"article:read", "article:list"},
			Resources: []string{"*"},
		},
		{
			Effect:     "Allow",
			Actions:    []string{"article:*"},
			Resources:  []string{"article/${user.id}/*"},
			Conditions: map[string]interface{}{"user.emailVerified": true},
		},
		{
			Effect:    "Deny",
			Actions:   []string{"article:delete"},
			Resources: []string{"article/*/protected"},
		},
	},
}
*/
```

**When:** Multi-tenant, cloud resources, fine-grained.
**Related to:** ABAC.

---

## Implementation Security Patterns

### 8. Input Validation & Sanitization

> Validate and sanitize all inputs.

```go
package security

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html"
	"regexp"
	"strings"
)

// UserInput represents validated user data.
type UserInput struct {
	Email    string
	Password string
	Name     string
	Age      int
}

var (
	emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)
	nameRegex  = regexp.MustCompile(`^[a-zA-Z\s]+$`)
)

// ValidateUserInput validates user input data.
func ValidateUserInput(email, password, name string, age int) (*UserInput, error) {
	var errs []string

	if !emailRegex.MatchString(email) {
		errs = append(errs, "invalid email format")
	}

	if len(password) < 8 || len(password) > 100 {
		errs = append(errs, "password must be between 8 and 100 characters")
	}

	if len(name) < 2 || len(name) > 50 {
		errs = append(errs, "name must be between 2 and 50 characters")
	}
	if !nameRegex.MatchString(name) {
		errs = append(errs, "name must contain only letters and spaces")
	}

	if age < 0 || age > 150 {
		errs = append(errs, "age must be between 0 and 150")
	}

	if len(errs) > 0 {
		return nil, fmt.Errorf("validation errors: %s", strings.Join(errs, ", "))
	}

	return &UserInput{
		Email:    email,
		Password: password,
		Name:     name,
		Age:      age,
	}, nil
}

// Sanitizer provides input sanitization methods.
type Sanitizer struct{}

// NewSanitizer creates a new sanitizer.
func NewSanitizer() *Sanitizer {
	return &Sanitizer{}
}

// EscapeHTML escapes HTML special characters to prevent XSS.
func (s *Sanitizer) EscapeHTML(input string) string {
	return html.EscapeString(input)
}

// SanitizePath removes path traversal attempts.
func (s *Sanitizer) SanitizePath(input string) string {
	input = strings.ReplaceAll(input, "..", "")
	input = strings.TrimLeft(input, "/")
	return input
}

// EscapeShell escapes shell special characters (use with caution).
func (s *Sanitizer) EscapeShell(input string) string {
	// Better: avoid shell execution entirely
	return "'" + strings.ReplaceAll(input, "'", "'\\''") + "'"
}

// UserRepository demonstrates parameterized queries.
type UserRepository struct {
	db *sql.DB
}

// FindByEmail uses parameterized query to prevent SQL injection.
func (r *UserRepository) FindByEmail(ctx context.Context, email string) (*User, error) {
	// GOOD - parameterized query
	query := "SELECT id, email, role FROM users WHERE email = $1"
	var user User
	err := r.db.QueryRowContext(ctx, query, email).Scan(&user.ID, &user.Email, &user.Role)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil

	// BAD - string concatenation (never do this!)
	// query := fmt.Sprintf("SELECT * FROM users WHERE email = '%s'", email)
}
```

**When:** ALWAYS for user input.

---

### 9. Password Hashing

> Secure password storage.

```go
package security

import (
	"errors"
	"regexp"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

// PasswordService handles password hashing and validation.
type PasswordService struct {
	bcryptCost int
}

// NewPasswordService creates a new password service.
func NewPasswordService() *PasswordService {
	return &PasswordService{
		bcryptCost: 12,
	}
}

// HashBcrypt hashes a password using bcrypt.
func (p *PasswordService) HashBcrypt(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), p.bcryptCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// VerifyBcrypt verifies a password against a bcrypt hash.
func (p *PasswordService) VerifyBcrypt(password, hash string) (bool, error) {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	if err != nil {
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// HashArgon2 hashes a password using Argon2id (recommended).
func (p *PasswordService) HashArgon2(password string, salt []byte) []byte {
	return argon2.IDKey(
		[]byte(password),
		salt,
		3,      // time cost (iterations)
		64*1024, // memory cost (64 MB)
		4,      // parallelism (threads)
		32,     // key length
	)
}

// PasswordStrength represents password strength validation result.
type PasswordStrength struct {
	Valid  bool     `json:"valid"`
	Errors []string `json:"errors"`
}

// ValidateStrength checks password strength requirements.
func (p *PasswordService) ValidateStrength(password string) *PasswordStrength {
	var errors []string

	if len(password) < 12 {
		errors = append(errors, "Password must be at least 12 characters")
	}
	if matched, _ := regexp.MatchString(`[A-Z]`, password); !matched {
		errors = append(errors, "Password must contain uppercase letter")
	}
	if matched, _ := regexp.MatchString(`[a-z]`, password); !matched {
		errors = append(errors, "Password must contain lowercase letter")
	}
	if matched, _ := regexp.MatchString(`[0-9]`, password); !matched {
		errors = append(errors, "Password must contain number")
	}
	if matched, _ := regexp.MatchString(`[!@#$%^&*]`, password); !matched {
		errors = append(errors, "Password must contain special character")
	}

	return &PasswordStrength{
		Valid:  len(errors) == 0,
		Errors: errors,
	}
}
```

**Recommended algorithms:** Argon2id > bcrypt > PBKDF2.
**When:** ALWAYS for passwords.

---

### 10. CSRF Protection

> Protection against Cross-Site Request Forgery.

```go
package security

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"sync"
	"time"
)

// CSRFProtection manages CSRF tokens.
type CSRFProtection struct {
	mu     sync.RWMutex
	tokens map[string]*csrfToken
}

type csrfToken struct {
	token     string
	expiresAt time.Time
}

// NewCSRFProtection creates a new CSRF protection manager.
func NewCSRFProtection() *CSRFProtection {
	return &CSRFProtection{
		tokens: make(map[string]*csrfToken),
	}
}

// GenerateToken creates a new CSRF token for a session.
func (c *CSRFProtection) GenerateToken(sessionID string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)

	c.mu.Lock()
	c.tokens[sessionID] = &csrfToken{
		token:     token,
		expiresAt: time.Now().Add(1 * time.Hour),
	}
	c.mu.Unlock()

	return token, nil
}

// ValidateToken checks if a CSRF token is valid for a session.
func (c *CSRFProtection) ValidateToken(sessionID, token string) bool {
	c.mu.RLock()
	stored, exists := c.tokens[sessionID]
	c.mu.RUnlock()

	if !exists {
		return false
	}

	if time.Now().After(stored.expiresAt) {
		c.mu.Lock()
		delete(c.tokens, sessionID)
		c.mu.Unlock()
		return false
	}

	// Use constant-time comparison to prevent timing attacks
	return subtle.ConstantTimeCompare([]byte(stored.token), []byte(token)) == 1
}

// Middleware provides CSRF protection for HTTP handlers.
func (c *CSRFProtection) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only check state-changing methods
		if r.Method == http.MethodPost || r.Method == http.MethodPut ||
			r.Method == http.MethodDelete || r.Method == http.MethodPatch {

			cookieToken, err := r.Cookie("csrf-token")
			if err != nil {
				http.Error(w, `{"error":"Invalid CSRF token"}`, http.StatusForbidden)
				return
			}

			headerToken := r.Header.Get("X-CSRF-Token")
			if headerToken == "" || cookieToken.Value != headerToken {
				http.Error(w, `{"error":"Invalid CSRF token"}`, http.StatusForbidden)
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// SetCSRFCookie sets a CSRF token cookie with secure flags.
func SetCSRFCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "csrf-token",
		Value:    token,
		HttpOnly: false, // Must be accessible to JavaScript
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   3600, // 1 hour
	})
}

// SetSessionCookie sets a session cookie with secure flags (SameSite protection).
func SetSessionCookie(w http.ResponseWriter, sessionID string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    sessionID,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode, // or SameSiteLaxMode
		MaxAge:   3600,
	})
}
```

**When:** Forms, state-changing requests.

---

### 11. Rate Limiting

> Limit the number of requests.

```go
package security

import (
	"context"
	"sync"
	"time"
)

// RateLimitConfig configures rate limiting.
type RateLimitConfig struct {
	WindowMs    int `json:"windowMs"`
	MaxRequests int `json:"maxRequests"`
}

// RateLimiter provides in-memory rate limiting.
type RateLimiter struct {
	mu      sync.RWMutex
	config  *RateLimitConfig
	buckets map[string]*bucket
}

type bucket struct {
	count   int
	resetAt int64
}

// NewRateLimiter creates a new rate limiter.
func NewRateLimiter(config *RateLimitConfig) *RateLimiter {
	return &RateLimiter{
		config:  config,
		buckets: make(map[string]*bucket),
	}
}

// RateLimitResult represents the result of a rate limit check.
type RateLimitResult struct {
	Allowed   bool  `json:"allowed"`
	Remaining int   `json:"remaining"`
	ResetAt   int64 `json:"resetAt"`
}

// Check verifies if a request is allowed under rate limits.
func (r *RateLimiter) Check(identifier string) *RateLimitResult {
	now := time.Now().UnixMilli()

	r.mu.Lock()
	defer r.mu.Unlock()

	b, exists := r.buckets[identifier]

	if !exists || now > b.resetAt {
		resetAt := now + int64(r.config.WindowMs)
		r.buckets[identifier] = &bucket{
			count:   1,
			resetAt: resetAt,
		}
		return &RateLimitResult{
			Allowed:   true,
			Remaining: r.config.MaxRequests - 1,
			ResetAt:   resetAt,
		}
	}

	if b.count >= r.config.MaxRequests {
		return &RateLimitResult{
			Allowed:   false,
			Remaining: 0,
			ResetAt:   b.resetAt,
		}
	}

	b.count++
	return &RateLimitResult{
		Allowed:   true,
		Remaining: r.config.MaxRequests - b.count,
		ResetAt:   b.resetAt,
	}
}

// RedisRateLimiter provides distributed rate limiting with Redis.
type RedisRateLimiter struct {
	client RedisClient
}

// RedisClient represents a Redis client interface.
type RedisClient interface {
	ZRemRangeByScore(ctx context.Context, key string, min, max string) error
	ZAdd(ctx context.Context, key string, score float64, member string) error
	ZCard(ctx context.Context, key string) (int64, error)
	Expire(ctx context.Context, key string, expiration time.Duration) error
}

// NewRedisRateLimiter creates a new Redis-based rate limiter.
func NewRedisRateLimiter(client RedisClient) *RedisRateLimiter {
	return &RedisRateLimiter{
		client: client,
	}
}

// Check implements sliding window rate limiting with Redis.
func (r *RedisRateLimiter) Check(ctx context.Context, key string, limit int, windowSeconds int) (bool, error) {
	now := time.Now().UnixMilli()
	windowStart := now - int64(windowSeconds)*1000

	// Remove old entries
	if err := r.client.ZRemRangeByScore(ctx, key, "0", string(rune(windowStart))); err != nil {
		return false, err
	}

	// Add current request
	member := string(rune(now))
	if err := r.client.ZAdd(ctx, key, float64(now), member); err != nil {
		return false, err
	}

	// Count requests in window
	count, err := r.client.ZCard(ctx, key)
	if err != nil {
		return false, err
	}

	// Set TTL
	if err := r.client.Expire(ctx, key, time.Duration(windowSeconds)*time.Second); err != nil {
		return false, err
	}

	return count <= int64(limit), nil
}
```

**When:** APIs, login, resource protection.
**Related to:** Circuit Breaker.

---

### 12. Secrets Management

> Secure secrets management.

```go
package security

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
	"strings"
)

// Config loads configuration from environment variables (basic approach).
type Config struct {
	DBPassword string
	APIKey     string
}

// LoadConfig loads configuration from environment.
func LoadConfig() *Config {
	return &Config{
		DBPassword: os.Getenv("DB_PASSWORD"),
		APIKey:     os.Getenv("API_KEY"),
	}
}

// VaultClient provides access to HashiCorp Vault.
type VaultClient struct {
	vaultAddr  string
	token      string
	httpClient *http.Client
}

// NewVaultClient creates a new Vault client.
func NewVaultClient(vaultAddr, token string) *VaultClient {
	return &VaultClient{
		vaultAddr:  vaultAddr,
		token:      token,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// SecretData represents secret data from Vault.
type SecretData struct {
	Data map[string]string `json:"data"`
}

type vaultResponse struct {
	Data SecretData `json:"data"`
}

// GetSecret retrieves a secret from Vault.
func (v *VaultClient) GetSecret(ctx context.Context, path string) (map[string]string, error) {
	url := fmt.Sprintf("%s/v1/%s", v.vaultAddr, path)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Vault-Token", v.token)

	resp, err := v.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var vaultResp vaultResponse
	if err := json.NewDecoder(resp.Body).Decode(&vaultResp); err != nil {
		return nil, err
	}

	return vaultResp.Data.Data, nil
}

// SetSecret stores a secret in Vault.
func (v *VaultClient) SetSecret(ctx context.Context, path string, data map[string]string) error {
	url := fmt.Sprintf("%s/v1/%s", v.vaultAddr, path)

	payload := map[string]interface{}{
		"data": data,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Vault-Token", v.token)
	req.Header.Set("Content-Type", "application/json")
	req.Body = io.NopCloser(strings.NewReader(string(body)))

	resp, err := v.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("vault request failed: %s", resp.Status)
	}

	return nil
}

// SecretRotation handles credential rotation.
type SecretRotation struct {
	vault  *VaultClient
	db     DatabaseClient
	notify NotificationService
}

// DatabaseClient represents a database client interface.
type DatabaseClient interface {
	Execute(ctx context.Context, query string, args ...interface{}) error
}

// NotificationService notifies applications of secret changes.
type NotificationService interface {
	NotifyApplications(ctx context.Context, message string) error
}

// NewSecretRotation creates a new secret rotation manager.
func NewSecretRotation(vault *VaultClient, db DatabaseClient, notify NotificationService) *SecretRotation {
	return &SecretRotation{
		vault:  vault,
		db:     db,
		notify: notify,
	}
}

// RotateDBCredentials rotates database credentials.
func (s *SecretRotation) RotateDBCredentials(ctx context.Context) error {
	// 1. Generate new credentials
	newPassword, err := generateSecurePassword(32)
	if err != nil {
		return fmt.Errorf("generating password: %w", err)
	}

	// 2. Update in database
	query := "ALTER USER app WITH PASSWORD $1"
	if err := s.db.Execute(ctx, query, newPassword); err != nil {
		return fmt.Errorf("updating database: %w", err)
	}

	// 3. Update in vault
	if err := s.vault.SetSecret(ctx, "database/creds", map[string]string{
		"password": newPassword,
	}); err != nil {
		return fmt.Errorf("updating vault: %w", err)
	}

	// 4. Notify applications to reload
	if err := s.notify.NotifyApplications(ctx, "database credentials rotated"); err != nil {
		return fmt.Errorf("notifying applications: %w", err)
	}

	return nil
}

func generateSecurePassword(length int) (string, error) {
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}
```

**Best practices:**

- Never commit secrets
- Regular rotation
- Least privilege
- Audit logging

---

## Decision Table

| Need | Pattern |
|------|---------|
| User login | Session / JWT |
| Social login | OAuth 2.0 |
| API authentication | API Keys / JWT |
| Simple permissions | RBAC |
| Complex permissions | ABAC / Policy |
| Input validation | Schema validation |
| Password storage | Argon2 / bcrypt |
| Form protection | CSRF tokens |
| Request limiting | Rate Limiting |
| Secrets management | Vault / Env vars |

## Sources

- [OWASP Cheat Sheets](https://cheatsheetseries.owasp.org/)
- [OAuth 2.0 Spec](https://oauth.net/2/)
- [NIST Guidelines](https://pages.nist.gov/800-63-3/)
