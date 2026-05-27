# Input Validation & Sanitization

> Validate and sanitize all user input to prevent injections.

## Principle

```
┌──────────────────────────────────────────────────────────────┐
│                    Input Processing Pipeline                  │
│                                                               │
│   Raw Input ──► Validation ──► Sanitization ──► Safe Input   │
│                     │              │                          │
│                     ▼              ▼                          │
│               Reject if       Remove/Escape                   │
│               invalid         dangerous chars                 │
└──────────────────────────────────────────────────────────────┘
```

## Schema Validation

```go
package validation

import (
	"fmt"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
)

// UserInput represents user registration input.
type UserInput struct {
	Email    string  `json:"email"`
	Password string  `json:"password"`
	Name     string  `json:"name"`
	Age      *int    `json:"age,omitempty"`
	Website  *string `json:"website,omitempty"`
}

// ValidationError represents a validation error.
type ValidationError struct {
	Field   string
	Message string
}

// Validator validates user input.
type Validator struct {
	errors []ValidationError
}

// NewValidator creates a new validator.
func NewValidator() *Validator {
	return &Validator{
		errors: make([]ValidationError, 0),
	}
}

// ValidateUser validates user registration input.
func (v *Validator) ValidateUser(input UserInput) error {
	// Email validation
	if _, err := mail.ParseAddress(input.Email); err != nil {
		v.addError("email", "Invalid email format")
	}

	// Password validation
	if len(input.Password) < 12 {
		v.addError("password", "Password must be at least 12 characters")
	}
	if len(input.Password) > 100 {
		v.addError("password", "Password too long")
	}
	if !regexp.MustCompile(`[A-Z]`).MatchString(input.Password) {
		v.addError("password", "Must contain uppercase")
	}
	if !regexp.MustCompile(`[a-z]`).MatchString(input.Password) {
		v.addError("password", "Must contain lowercase")
	}
	if !regexp.MustCompile(`[0-9]`).MatchString(input.Password) {
		v.addError("password", "Must contain number")
	}
	if !regexp.MustCompile(`[!@#$%^&*]`).MatchString(input.Password) {
		v.addError("password", "Must contain special character")
	}

	// Name validation
	if len(input.Name) < 2 {
		v.addError("name", "Name too short")
	}
	if len(input.Name) > 50 {
		v.addError("name", "Name too long")
	}
	if !regexp.MustCompile(`^[a-zA-Z\s'-]+$`).MatchString(input.Name) {
		v.addError("name", "Name contains invalid characters")
	}

	// Age validation
	if input.Age != nil {
		if *input.Age < 0 || *input.Age > 150 {
			v.addError("age", "Age must be between 0 and 150")
		}
	}

	// Website validation
	if input.Website != nil && *input.Website != "" {
		if _, err := url.ParseRequestURI(*input.Website); err != nil {
			v.addError("website", "Invalid URL")
		}
	}

	if len(v.errors) > 0 {
		return fmt.Errorf("validation failed: %v", v.errors)
	}

	return nil
}

func (v *Validator) addError(field, message string) {
	v.errors = append(v.errors, ValidationError{
		Field:   field,
		Message: message,
	})
}

// Errors returns all validation errors.
func (v *Validator) Errors() []ValidationError {
	return v.errors
}

// ArticleInput represents article input.
type ArticleInput struct {
	Title     string   `json:"title"`
	Slug      string   `json:"slug"`
	Content   string   `json:"content"`
	Tags      []string `json:"tags"`
	PublishAt *string  `json:"publishAt,omitempty"`
}

// ValidateArticle validates article input.
func (v *Validator) ValidateArticle(input ArticleInput) error {
	// Title validation
	input.Title = strings.TrimSpace(input.Title)
	if len(input.Title) == 0 {
		v.addError("title", "Title is required")
	}
	if len(input.Title) > 200 {
		v.addError("title", "Title too long")
	}

	// Slug validation
	if !regexp.MustCompile(`^[a-z0-9-]+$`).MatchString(input.Slug) {
		v.addError("slug", "Slug must be lowercase alphanumeric with hyphens")
	}

	// Content validation
	if len(input.Content) < 10 {
		v.addError("content", "Content too short")
	}
	if len(input.Content) > 50000 {
		v.addError("content", "Content too long")
	}

	// Tags validation
	if len(input.Tags) > 10 {
		v.addError("tags", "Too many tags (max 10)")
	}
	for i, tag := range input.Tags {
		if len(tag) > 30 {
			v.addError(fmt.Sprintf("tags[%d]", i), "Tag too long (max 30)")
		}
	}

	if len(v.errors) > 0 {
		return fmt.Errorf("validation failed")
	}

	return nil
}
```

## Sanitization

```go
package sanitization

import (
	"html"
	"path/filepath"
	"regexp"
	"strings"
)

// Sanitizer provides input sanitization methods.
type Sanitizer struct{}

// NewSanitizer creates a new sanitizer.
func NewSanitizer() *Sanitizer {
	return &Sanitizer{}
}

// HTML sanitizes HTML input (basic - use bluemonday for production).
func (s *Sanitizer) HTML(input string) string {
	// For production, use github.com/microcosm-cc/bluemonday
	// This is a basic example
	allowedTags := regexp.MustCompile(`</?(?:b|i|em|strong|a|p|br)[^>]*>`)
	sanitized := regexp.MustCompile(`<[^>]+>`).ReplaceAllStringFunc(input, func(tag string) string {
		if allowedTags.MatchString(tag) {
			return tag
		}
		return ""
	})
	return sanitized
}

// EscapeHTML escapes HTML entities.
func (s *Sanitizer) EscapeHTML(input string) string {
	return html.EscapeString(input)
}

// EscapeSQL escapes SQL (use parameterized queries instead!).
func (s *Sanitizer) EscapeSQL(input string) string {
	// This is for display/logging only - ALWAYS use parameterized queries
	return strings.ReplaceAll(input, "'", "''")
}

// SanitizePath prevents path traversal.
func (s *Sanitizer) SanitizePath(input string) string {
	// Remove ..
	input = strings.ReplaceAll(input, "..", "")
	// Remove leading /
	input = strings.TrimLeft(input, "/")
	// Remove illegal chars
	input = regexp.MustCompile(`[<>:"|?*]`).ReplaceAllString(input, "")
	return input
}

// SanitizeFilename sanitizes filename.
func (s *Sanitizer) SanitizeFilename(input string) string {
	// Replace invalid chars with underscore
	input = regexp.MustCompile(`[^a-zA-Z0-9._-]`).ReplaceAllString(input, "_")
	// Remove consecutive dots
	input = regexp.MustCompile(`\.{2,}`).ReplaceAllString(input, ".")
	// Limit length
	if len(input) > 255 {
		input = input[:255]
	}
	// Clean path
	return filepath.Clean(input)
}

// EscapeShell escapes shell command arguments.
func (s *Sanitizer) EscapeShell(input string) string {
	// Wrap in single quotes and escape single quotes
	escaped := strings.ReplaceAll(input, "'", "'\\''")
	return "'" + escaped + "'"
}

// SanitizeURL validates and sanitizes URL.
func (s *Sanitizer) SanitizeURL(input string) (string, error) {
	u, err := url.Parse(input)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}

	// Only allow http/https
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("invalid URL scheme: %s", u.Scheme)
	}

	return u.String(), nil
}
```

## SQL Injection Prevention

```go
package database

import (
	"context"
	"database/sql"
	"fmt"
)

// NEVER do this
func badQuery(db *sql.DB, email string) (*sql.Rows, error) {
	query := fmt.Sprintf("SELECT * FROM users WHERE email = '%s'", email)
	return db.Query(query)
	// Vulnerable to: ' OR '1'='1
}

// ALWAYS use parameterized queries
func goodQuery(ctx context.Context, db *sql.DB, email string) (*sql.Rows, error) {
	query := "SELECT * FROM users WHERE email = $1"
	return db.QueryContext(ctx, query, email)
}

// UserFilters represents query filters.
type UserFilters struct {
	Email string
	Role  string
}

// FindUsers builds a safe query with filters.
func FindUsers(ctx context.Context, db *sql.DB, filters UserFilters) (*sql.Rows, error) {
	query := "SELECT * FROM users WHERE 1=1"
	args := make([]interface{}, 0)
	argPos := 1

	if filters.Email != "" {
		query += fmt.Sprintf(" AND email = $%d", argPos)
		args = append(args, filters.Email)
		argPos++
	}

	if filters.Role != "" {
		query += fmt.Sprintf(" AND role = $%d", argPos)
		args = append(args, filters.Role)
		argPos++
	}

	return db.QueryContext(ctx, query, args...)
}
```

## XSS Prevention

```go
package templates

import (
	"html/template"
	"io"
)

// User represents a user for rendering.
type User struct {
	Name string
	Bio  string
}

// RenderUserProfile renders a user profile (safe).
func RenderUserProfile(w io.Writer, user User) error {
	// Go templates auto-escape by default
	tmpl := template.Must(template.New("profile").Parse(`
		<div class="profile">
			<h1>{{.Name}}</h1>
			<p>{{.Bio}}</p>
		</div>
	`))

	return tmpl.Execute(w, user)
}

// RenderUserProfileHTML renders with HTML content (use with caution).
func RenderUserProfileHTML(w io.Writer, user User, sanitizer *sanitization.Sanitizer) error {
	// Sanitize HTML before rendering as HTML
	sanitizedBio := sanitizer.HTML(user.Bio)

	tmpl := template.Must(template.New("profile").Parse(`
		<div class="profile">
			<h1>{{.Name}}</h1>
			<div>{{.Bio}}</div>
		</div>
	`))

	data := struct {
		Name string
		Bio  template.HTML // Mark as safe HTML
	}{
		Name: user.Name,
		Bio:  template.HTML(sanitizedBio),
	}

	return tmpl.Execute(w, data)
}
```

## Request Validation Middleware

```go
package middleware

import (
	"context"
	"encoding/json"
	"net/http"
)

// ValidateBody validates request body against a validation function.
func ValidateBody(validate func(interface{}) error, v interface{}) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewDecoder(r.Body).Decode(v); err != nil {
				http.Error(w, `{"error": "Invalid JSON"}`, http.StatusBadRequest)
				return
			}

			if err := validate(v); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"error":   "Validation failed",
					"details": err.Error(),
				})
				return
			}

			ctx := context.WithValue(r.Context(), "validatedBody", v)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// Usage example
func SetupRoutes(mux *http.ServeMux, validator *validation.Validator) {
	mux.Handle("/users", ValidateBody(
		func(v interface{}) error {
			return validator.ValidateUser(v.(validation.UserInput))
		},
		&validation.UserInput{},
	)(http.HandlerFunc(createUser)))
}
```

## Recommended Libraries

| Package | Usage |
|---------|-------|
| `github.com/go-playground/validator/v10` | Struct validation with tags |
| `github.com/microcosm-cc/bluemonday` | HTML sanitization |
| `github.com/asaskevich/govalidator` | String validation utilities |

## Common Mistakes

| Mistake | Impact | Solution |
|---------|--------|----------|
| Client-side validation only | Easy bypass | Always validate server-side |
| Blacklist instead of whitelist | Possible bypass | Strict whitelist |
| Sanitize without validating | Corrupted data | Validate then sanitize |
| Escape too late | Injection before escape | Escape at output |
| Trust Content-Type | Body parsing attack | Verify and validate |

## When to Use

| Technique | When |
|-----------|------|
| Schema validation | All structured input |
| HTML sanitization | User HTML content |
| SQL parameterization | ALWAYS for SQL |
| Path sanitization | Upload, file access |
| URL validation | User links |

## Related Patterns

- **CSRF Protection**: Validate request origin
- **Rate Limiting**: Limit abuse
- **Content Security Policy**: Additional XSS defense

## Sources

- [OWASP Input Validation](https://cheatsheetseries.owasp.org/cheatsheets/Input_Validation_Cheat_Sheet.html)
- [OWASP XSS Prevention](https://cheatsheetseries.owasp.org/cheatsheets/Cross_Site_Scripting_Prevention_Cheat_Sheet.html)
- [OWASP SQL Injection Prevention](https://cheatsheetseries.owasp.org/cheatsheets/SQL_Injection_Prevention_Cheat_Sheet.html)
