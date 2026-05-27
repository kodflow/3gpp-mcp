# Cache-Aside Pattern

> Load data into the cache on demand from the data store.

## Principle

```
                    ┌─────────────────────────────────────────────┐
                    │              CACHE-ASIDE FLOW               │
                    └─────────────────────────────────────────────┘

  READ (Cache Hit):
  ┌─────────┐  1. Get    ┌─────────┐
  │  Client │ ─────────▶ │  Cache  │ ──▶ Data found, return
  └─────────┘            └─────────┘

  READ (Cache Miss):
  ┌─────────┐  1. Get    ┌─────────┐  2. Miss
  │  Client │ ─────────▶ │  Cache  │ ─────────┐
  └─────────┘            └─────────┘          │
       ▲                      ▲               ▼
       │                      │          ┌─────────┐
       │   5. Return data     │ 4. Set   │   DB    │
       └──────────────────────┴──────────┴─────────┘
                                  3. Read

  WRITE (Write-Through):
  ┌─────────┐  1. Write  ┌─────────┐  2. Write  ┌─────────┐
  │  Client │ ─────────▶ │  Cache  │ ─────────▶ │   DB    │
  └─────────┘            └─────────┘            └─────────┘

  WRITE (Cache-Aside):
  ┌─────────┐  1. Write  ┌─────────┐
  │  Client │ ─────────▶ │   DB    │
  └─────────┘            └─────────┘
       │  2. Invalidate      │
       └────────────────────▶│
                        ┌─────────┐
                        │  Cache  │ (entry removed)
                        └─────────┘
```

## Variants

| Pattern | Description | Consistency |
|---------|-------------|-------------|
| **Cache-Aside** | App manages the cache manually | Eventual |
| **Read-Through** | Cache loads from DB automatically | Eventual |
| **Write-Through** | Synchronous write to cache + DB | Strong |
| **Write-Behind** | Asynchronous write to DB | Eventual |

## Go Example

```go
package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// CacheService defines cache operations.
type CacheService interface {
	Get(ctx context.Context, key string, dest interface{}) error
	Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
}

// Database represents database operations.
type Database interface {
	FindUserByID(ctx context.Context, id string) (*User, error)
	UpdateUser(ctx context.Context, id string, data map[string]interface{}) (*User, error)
	DeleteUser(ctx context.Context, id string) error
}

// User represents a user entity.
type User struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Email    string    `json:"email"`
	CreateAt time.Time `json:"created_at"`
}

// UserRepository implements cache-aside pattern for users.
type UserRepository struct {
	cache Cache Service
	db    Database
	ttl   time.Duration
}

// NewUserRepository creates a new UserRepository.
func NewUserRepository(cache CacheService, db Database, ttl time.Duration) *UserRepository {
	return &UserRepository{
		cache: cache,
		db:    db,
		ttl:   ttl,
	}
}

// FindByID finds a user by ID using cache-aside pattern.
func (r *UserRepository) FindByID(ctx context.Context, id string) (*User, error) {
	cacheKey := fmt.Sprintf("user:%s", id)

	// 1. Try cache first
	var user User
	err := r.cache.Get(ctx, cacheKey, &user)
	if err == nil {
		return &user, nil // Cache hit
	}

	// 2. Cache miss - load from DB
	dbUser, err := r.db.FindUserByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("finding user from db: %w", err)
	}
	if dbUser == nil {
		return nil, nil
	}

	// 3. Populate cache for next time
	if err := r.cache.Set(ctx, cacheKey, dbUser, r.ttl); err != nil {
		// Log but don't fail - cache is optional
		fmt.Printf("failed to cache user %s: %v
", id, err)
	}

	return dbUser, nil
}

// Update updates a user and invalidates the cache.
func (r *UserRepository) Update(ctx context.Context, id string, data map[string]interface{}) (*User, error) {
	// 1. Update database first
	user, err := r.db.UpdateUser(ctx, id, data)
	if err != nil {
		return nil, fmt.Errorf("updating user: %w", err)
	}

	// 2. Invalidate cache (don't update - avoid race conditions)
	cacheKey := fmt.Sprintf("user:%s", id)
	if err := r.cache.Delete(ctx, cacheKey); err != nil {
		fmt.Printf("failed to invalidate cache for user %s: %v
", id, err)
	}

	return user, nil
}

// Delete deletes a user and invalidates the cache.
func (r *UserRepository) Delete(ctx context.Context, id string) error {
	if err := r.db.DeleteUser(ctx, id); err != nil {
		return fmt.Errorf("deleting user: %w", err)
	}

	cacheKey := fmt.Sprintf("user:%s", id)
	if err := r.cache.Delete(ctx, cacheKey); err != nil {
		fmt.Printf("failed to invalidate cache for user %s: %v
", id, err)
	}

	return nil
}
```

## Redis Implementation (Go)

```go
// This example follows the same idiomatic Go patterns
// as the main example above.
// Specific implementation based on standard Go
// interfaces and conventions.
```

## TTL Strategies

| Data | Recommended TTL | Reason |
|------|-----------------|--------|
| Configuration | 5-15 min | Rarely changes |
| User profile | 1-24 h | Rarely updated |
| Product catalog | 15-60 min | Regular updates |
| Session | 30 min - 24h | Security |
| Real-time data | 1-60 sec | Critical freshness |

## When to Use

| Situation | Recommended |
|-----------|-------------|
| Reads >> Writes | Yes |
| Low-volatility data | Yes |
| Tolerant of eventual consistency | Yes |
| Strict real-time data | No |
| Frequent writes | No (excessive invalidation) |

## Related Patterns

| Pattern | Relation |
|---------|----------|
| Read-Through | Auto-loading cache |
| Write-Through | Strong consistency |
| Refresh-Ahead | Proactive pre-loading |
| Circuit Breaker | Fallback if cache down |

## Sources

- [Microsoft - Cache-Aside](https://learn.microsoft.com/en-us/azure/architecture/patterns/cache-aside)
- [AWS ElastiCache Best Practices](https://docs.aws.amazon.com/AmazonElastiCache/latest/mem-ug/BestPractices.html)
