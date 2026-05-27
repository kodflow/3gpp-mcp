# Secrets Management

> Secure management of secrets, credentials, and encryption keys.

## Principle

```
┌─────────────────────────────────────────────────────────────────┐
│                    Secrets Lifecycle                             │
│                                                                  │
│   Generate ──► Store ──► Distribute ──► Use ──► Rotate ──► Revoke│
│      │          │           │           │         │          │  │
│      ▼          ▼           ▼           ▼         ▼          ▼  │
│   Strong     Encrypted   Secure      In-memory  Automated   Audit│
│   entropy    at rest     transport   only       schedule     log │
└─────────────────────────────────────────────────────────────────┘
```

## Environment Variables (Basic)

```go
package config

import (
	"fmt"
	"net/url"
	"os"
)

// Config holds the application configuration.
type Config struct {
	Environment        string
	DatabaseURL        string
	JWTSecret          string
	APIKey             string
	AWSAccessKeyID     string
	AWSSecretAccessKey string
}

// Load loads and validates configuration from environment variables.
func Load() (*Config, error) {
	cfg := &Config{
		Environment:        os.Getenv("ENVIRONMENT"),
		DatabaseURL:        os.Getenv("DATABASE_URL"),
		JWTSecret          os.Getenv("JWT_SECRET"),
		APIKey             os.Getenv("API_KEY"),
		AWSAccessKeyID     os.Getenv("AWS_ACCESS_KEY_ID"),
		AWSSecretAccessKey: os.Getenv("AWS_SECRET_ACCESS_KEY"),
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return cfg, nil
}

func (c *Config) validate() error {
	if c.Environment != "development" && c.Environment != "production" && c.Environment != "test" {
		return fmt.Errorf("ENVIRONMENT must be development, production, or test")
	}

	if _, err := url.Parse(c.DatabaseURL); err != nil {
		return fmt.Errorf("invalid DATABASE_URL: %w", err)
	}

	if len(c.JWTSecret) < 32 {
		return fmt.Errorf("JWT_SECRET must be at least 32 characters")
	}

	if len(c.APIKey) < 16 {
		return fmt.Errorf("API_KEY must be at least 16 characters")
	}

	return nil
}

// Usage
func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Configuration error: %v\n", err)
		os.Exit(1)
	}
	secret := cfg.JWTSecret // Type-safe
}
```

## HashiCorp Vault Integration

```go
package vault

import (
	"context"
	"fmt"
	"sync"
	"time"

	vault "github.com/hashicorp/vault/api"
)

// Config holds Vault client configuration.
type Config struct {
	Endpoint string
	Token    string
	RoleID   string
	SecretID string
}

// CachedSecret represents a cached secret with expiration.
type CachedSecret struct {
	Value     map[string]string
	ExpiresAt time.Time
}

// Client wraps the Vault client with caching.
type Client struct {
	client   *vault.Client
	cache    map[string]*CachedSecret
	mu       sync.RWMutex
	cacheTTL time.Duration
}

// NewClient creates a new Vault client.
func NewClient(cfg Config) (*Client, error) {
	vaultCfg := vault.DefaultConfig()
	vaultCfg.Address = cfg.Endpoint

	client, err := vault.NewClient(vaultCfg)
	if err != nil {
		return nil, fmt.Errorf("creating vault client: %w", err)
	}

	if cfg.Token != "" {
		client.SetToken(cfg.Token)
	}

	return &Client{
		client:   client,
		cache:    make(map[string]*CachedSecret),
		cacheTTL: 5 * time.Minute,
	}, nil
}

// Authenticate authenticates using AppRole.
func (c *Client) Authenticate(ctx context.Context, roleID, secretID string) error {
	data := map[string]interface{}{
		"role_id":   roleID,
		"secret_id": secretID,
	}

	resp, err := c.client.Logical().WriteWithContext(ctx, "auth/approle/login", data)
	if err != nil {
		return fmt.Errorf("approle authentication: %w", err)
	}

	if resp.Auth == nil {
		return fmt.Errorf("no auth info in response")
	}

	c.client.SetToken(resp.Auth.ClientToken)
	return nil
}

// GetSecret retrieves a secret from Vault with caching.
func (c *Client) GetSecret(ctx context.Context, path string) (map[string]string, error) {
	// Check cache
	c.mu.RLock()
	if cached, ok := c.cache[path]; ok && time.Now().Before(cached.ExpiresAt) {
		c.mu.RUnlock()
		return cached.Value, nil
	}
	c.mu.RUnlock()

	// Fetch from Vault
	secret, err := c.client.Logical().ReadWithContext(ctx, fmt.Sprintf("secret/data/%s", path))
	if err != nil {
		return nil, fmt.Errorf("reading secret: %w", err)
	}

	if secret == nil || secret.Data == nil {
		return nil, fmt.Errorf("no secret found at path: %s", path)
	}

	data, ok := secret.Data["data"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid secret format")
	}

	// Convert to map[string]string
	result := make(map[string]string)
	for k, v := range data {
		if str, ok := v.(string); ok {
			result[k] = str
		}
	}

	// Cache result
	c.mu.Lock()
	c.cache[path] = &CachedSecret{
		Value:     result,
		ExpiresAt: time.Now().Add(c.cacheTTL),
	}
	c.mu.Unlock()

	return result, nil
}

// SetSecret stores a secret in Vault.
func (c *Client) SetSecret(ctx context.Context, path string, data map[string]string) error {
	payload := map[string]interface{}{
		"data": data,
	}

	_, err := c.client.Logical().WriteWithContext(ctx, fmt.Sprintf("secret/data/%s", path), payload)
	if err != nil {
		return fmt.Errorf("writing secret: %w", err)
	}

	// Invalidate cache
	c.mu.Lock()
	delete(c.cache, path)
	c.mu.Unlock()

	return nil
}

// DeleteSecret deletes a secret from Vault.
func (c *Client) DeleteSecret(ctx context.Context, path string) error {
	_, err := c.client.Logical().DeleteWithContext(ctx, fmt.Sprintf("secret/metadata/%s", path))
	if err != nil {
		return fmt.Errorf("deleting secret: %w", err)
	}

	// Invalidate cache
	c.mu.Lock()
	delete(c.cache, path)
	c.mu.Unlock()

	return nil
}

// Usage
func main() {
	ctx := context.Background()
	
	vaultClient, err := NewClient(Config{
		Endpoint: "https://vault.example.com",
	})
	if err != nil {
		panic(err)
	}

	if err := vaultClient.Authenticate(ctx, os.Getenv("VAULT_ROLE_ID"), os.Getenv("VAULT_SECRET_ID")); err != nil {
		panic(err)
	}

	dbSecrets, err := vaultClient.GetSecret(ctx, "database/credentials")
	if err != nil {
		panic(err)
	}

	dbURL := fmt.Sprintf("postgres://%s:%s@db.example.com/app", 
		dbSecrets["username"], dbSecrets["password"])
}
```

## AWS Secrets Manager

```go
package awssecrets

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

// Manager wraps AWS Secrets Manager with caching.
type Manager struct {
	client   *secretsmanager.Client
	cache    map[string]*CachedSecret
	mu       sync.RWMutex
	cacheTTL time.Duration
}

// CachedSecret represents a cached secret.
type CachedSecret struct {
	Value     map[string]interface{}
	ExpiresAt time.Time
}

// NewManager creates a new AWS Secrets Manager client.
func NewManager(ctx context.Context, region string) (*Manager, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}

	return &Manager{
		client:   secretsmanager.NewFromConfig(cfg),
		cache:    make(map[string]*CachedSecret),
		cacheTTL: 5 * time.Minute,
	}, nil
}

// GetSecret retrieves a secret with caching.
func (m *Manager) GetSecret(ctx context.Context, secretName string) (map[string]interface{}, error) {
	// Check cache
	m.mu.RLock()
	if cached, ok := m.cache[secretName]; ok && time.Now().Before(cached.ExpiresAt) {
		m.mu.RUnlock()
		return cached.Value, nil
	}
	m.mu.RUnlock()

	// Fetch from AWS
	input := &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(secretName),
	}

	result, err := m.client.GetSecretValue(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("getting secret: %w", err)
	}

	var secret map[string]interface{}
	if result.SecretString != nil {
		if err := json.Unmarshal([]byte(*result.SecretString), &secret); err != nil {
			return nil, fmt.Errorf("unmarshaling secret: %w", err)
		}
	} else if result.SecretBinary != nil {
		if err := json.Unmarshal(result.SecretBinary, &secret); err != nil {
			return nil, fmt.Errorf("unmarshaling secret binary: %w", err)
		}
	} else {
		return nil, fmt.Errorf("no secret value found")
	}

	// Cache result
	m.mu.Lock()
	m.cache[secretName] = &CachedSecret{
		Value:     secret,
		ExpiresAt: time.Now().Add(m.cacheTTL),
	}
	m.mu.Unlock()

	return secret, nil
}

// CreateSecret creates a new secret.
func (m *Manager) CreateSecret(ctx context.Context, name string, value map[string]interface{}) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshaling secret: %w", err)
	}

	input := &secretsmanager.CreateSecretInput{
		Name:         aws.String(name),
		SecretString: aws.String(string(data)),
	}

	_, err = m.client.CreateSecret(ctx, input)
	if err != nil {
		return fmt.Errorf("creating secret: %w", err)
	}

	return nil
}

// RotateSecret triggers secret rotation.
func (m *Manager) RotateSecret(ctx context.Context, name string) error {
	input := &secretsmanager.RotateSecretInput{
		SecretId:            aws.String(name),
		RotateImmediately:   aws.Bool(true),
	}

	_, err := m.client.RotateSecret(ctx, input)
	if err != nil {
		return fmt.Errorf("rotating secret: %w", err)
	}

	// Invalidate cache
	m.mu.Lock()
	delete(m.cache, name)
	m.mu.Unlock()

	return nil
}
```

## Secret Rotation

```go
package rotation

import (
	"context"
	"crypto/rand"
	"fmt"
	"time"
)

// RotatableSecret defines the interface for rotatable secrets.
type RotatableSecret interface {
	Name() string
	RotationSchedule() string
	Rotate(ctx context.Context) error
}

// DatabaseCredentialRotation handles database credential rotation.
type DatabaseCredentialRotation struct {
	name             string
	currentVersion   string
	rotationSchedule string
	vault            VaultClient
	db               DatabaseAdmin
}

// Name returns the secret name.
func (r *DatabaseCredentialRotation) Name() string {
	return r.name
}

// RotationSchedule returns the cron schedule.
func (r *DatabaseCredentialRotation) RotationSchedule() string {
	return r.rotationSchedule
}

// Rotate performs the rotation.
func (r *DatabaseCredentialRotation) Rotate(ctx context.Context) error {
	// 1. Generate new password
	newPassword, err := r.generateSecurePassword()
	if err != nil {
		return fmt.Errorf("generating password: %w", err)
	}

	// 2. Update database user password
	if err := r.db.UpdateUserPassword(ctx, "app_user", newPassword); err != nil {
		return fmt.Errorf("updating database password: %w", err)
	}

	// 3. Update in Vault
	secrets := map[string]string{
		"username":  "app_user",
		"password":  newPassword,
		"rotatedAt": time.Now().Format(time.RFC3339),
	}
	if err := r.vault.SetSecret(ctx, "database/credentials", secrets); err != nil {
		return fmt.Errorf("updating vault: %w", err)
	}

	// 4. Notify applications to reload
	if err := r.notifyApplications(ctx); err != nil {
		return fmt.Errorf("notifying applications: %w", err)
	}

	fmt.Printf("Rotated %s successfully\n", r.name)
	return nil
}

func (r *DatabaseCredentialRotation) generateSecurePassword() (string, error) {
	const (
		length = 32
		chars  = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%^&*"
	)

	password := make([]byte, length)
	randomBytes := make([]byte, length)

	if _, err := rand.Read(randomBytes); err != nil {
		return "", fmt.Errorf("reading random bytes: %w", err)
	}

	for i, b := range randomBytes {
		password[i] = chars[int(b)%len(chars)]
	}

	return string(password), nil
}

func (r *DatabaseCredentialRotation) notifyApplications(ctx context.Context) error {
	// Send signal to reload config
	// Could be: Redis pub/sub, webhook, Kubernetes rolling restart
	return nil
}

// Scheduler manages secret rotation schedules.
type Scheduler struct {
	secrets map[string]RotatableSecret
	stop    chan struct{}
}

// NewScheduler creates a new rotation scheduler.
func NewScheduler() *Scheduler {
	return &Scheduler{
		secrets: make(map[string]RotatableSecret),
		stop:    make(chan struct{}),
	}
}

// Schedule schedules a secret for rotation.
func (s *Scheduler) Schedule(ctx context.Context, secret RotatableSecret) {
	interval := s.cronToInterval(secret.RotationSchedule())

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if err := secret.Rotate(ctx); err != nil {
					fmt.Printf("Failed to rotate %s: %v\n", secret.Name(), err)
					// Alert ops team
				}
			case <-s.stop:
				return
			}
		}
	}()
}

func (s *Scheduler) cronToInterval(cron string) time.Duration {
	// Simplified - use a real cron parser like github.com/robfig/cron
	return 7 * 24 * time.Hour // 1 week
}

// Stop stops all rotation schedules.
func (s *Scheduler) Stop() {
	close(s.stop)
}
```

## Encryption at Rest

```go
package encryption

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
)

// Encryptor handles secret encryption/decryption.
type Encryptor struct {
	masterKey []byte
}

// NewEncryptor creates a new encryptor.
func NewEncryptor(masterKey []byte) (*Encryptor, error) {
	if len(masterKey) != 32 {
		return nil, fmt.Errorf("master key must be 32 bytes")
	}

	return &Encryptor{
		masterKey: masterKey,
	}, nil
}

// Encrypt encrypts plaintext using AES-256-GCM.
func (e *Encryptor) Encrypt(plaintext string) (string, error) {
	block, err := aes.NewCipher(e.masterKey)
	if err != nil {
		return "", fmt.Errorf("creating cipher: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("creating GCM: %w", err)
	}

	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generating nonce: %w", err)
	}

	ciphertext := aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt decrypts ciphertext using AES-256-GCM.
func (e *Encryptor) Decrypt(ciphertext string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", fmt.Errorf("decoding ciphertext: %w", err)
	}

	block, err := aes.NewCipher(e.masterKey)
	if err != nil {
		return "", fmt.Errorf("creating cipher: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("creating GCM: %w", err)
	}

	nonceSize := aead.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypting: %w", err)
	}

	return string(plaintext), nil
}
```

## Recommended Libraries

| Package | Usage |
|---------|-------|
| `github.com/hashicorp/vault/api` | HashiCorp Vault client |
| `github.com/aws/aws-sdk-go-v2/service/secretsmanager` | AWS Secrets Manager |
| `cloud.google.com/go/secretmanager` | GCP Secret Manager |
| `github.com/Azure/azure-sdk-for-go/sdk/keyvault` | Azure Key Vault |
| `github.com/joho/godotenv` | Local .env loading |

## Common Mistakes

| Mistake | Impact | Solution |
|---------|--------|----------|
| Secrets in git | Public exposure | .gitignore, git-secrets |
| Secrets in logs | Leakage | Mask in logs |
| No rotation | Persistent breach | Automatic rotation |
| Hardcoded secrets | Hard to change | Always externalize |
| Shared secrets | Large blast radius | Per-service secrets |
| No encryption at rest | Breach if storage accessed | Always encrypt |

## Best Practices

```yaml
# Checklist secrets management
checklist:
  storage:
    - [ ] Never in plaintext in code
    - [ ] Never in git (even private)
    - [ ] Encrypted at rest
    - [ ] Strict access control

  transport:
    - [ ] TLS mandatory
    - [ ] Not in URLs
    - [ ] Not in logs

  lifecycle:
    - [ ] Automatic rotation
    - [ ] Revocation possible
    - [ ] Complete audit trail

  access:
    - [ ] Least privilege
    - [ ] One secret per usage
    - [ ] Expiration when possible
```

## When to Use

- Applications handling database credentials or third-party service credentials
- Microservices requiring securely shared secrets
- Multi-tenant environments with per-client secret isolation
- Systems subject to compliance requirements (PCI-DSS, SOC2, HIPAA)
- Cloud infrastructure with automatic credential rotation

## Related Patterns

- **OAuth 2.0**: Tokens rather than credentials
- **JWT**: Signing keys to protect
- **Encryption**: Master key management
- **API Keys**: Managing API keys as secrets

## Sources

- [OWASP Secrets Management](https://cheatsheetseries.owasp.org/cheatsheets/Secrets_Management_Cheat_Sheet.html)
- [HashiCorp Vault](https://developer.hashicorp.com/vault/docs)
- [AWS Secrets Manager](https://docs.aws.amazon.com/secretsmanager/)
