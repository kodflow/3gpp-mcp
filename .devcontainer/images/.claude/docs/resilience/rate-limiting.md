# Rate Limiting Pattern

> Control request throughput to protect services against overload.

---

## Principle

```
┌─────────────────────────────────────────────────────────────────┐
│                     RATE LIMITING                                │
│                                                                  │
│   Incoming Requests          Rate Limiter           Service     │
│   ─────────────────         ────────────           ─────────    │
│                                                                  │
│   ●●●●●●●●●●●●●● ─────────► [Token Bucket] ─────────► □□□□□     │
│   (100 req/s)                  │    │               (max 50)    │
│                                │    │                            │
│                                │    └──► ✗ Rejected (429)       │
│                                │                                 │
│                                └────► ✓ Allowed (200)           │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

---

## Algorithms

| Algorithm | Description | Usage |
|-----------|-------------|-------|
| **Token Bucket** | Tokens regenerated at constant rate | API rate limiting |
| **Leaky Bucket** | Queue that drains at constant rate | Traffic shaping |
| **Fixed Window** | Counter per fixed time window | Simple, but burst-prone |
| **Sliding Window Log** | Log of request timestamps | Precise, more memory |
| **Sliding Window Counter** | Approximation between windows | Good compromise |

---

## Token Bucket Implementation

```go
package ratelimiting

import (
	"sync"
	"time"
)

// TokenBucket implements token bucket rate limiting.
type TokenBucket struct {
	mu         sync.Mutex
	tokens     float64
	lastRefill time.Time
	capacity   float64  // Max tokens
	refillRate float64  // Tokens per second
}

// NewTokenBucket creates a token bucket rate limiter.
func NewTokenBucket(capacity, refillRate float64) *TokenBucket {
	return &TokenBucket{
		tokens:     capacity,
		lastRefill: time.Now(),
		capacity:   capacity,
		refillRate: refillRate,
	}
}

// TryConsume attempts to consume tokens.
func (tb *TokenBucket) TryConsume(tokensNeeded float64) bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.refill()

	if tb.tokens >= tokensNeeded {
		tb.tokens -= tokensNeeded
		return true
	}

	return false
}

func (tb *TokenBucket) refill() {
	now := time.Now()
	elapsed := now.Sub(tb.lastRefill).Seconds()
	tokensToAdd := elapsed * tb.refillRate

	tb.tokens = min(tb.capacity, tb.tokens+tokensToAdd)
	tb.lastRefill = now
}

// GetAvailableTokens returns available tokens.
func (tb *TokenBucket) GetAvailableTokens() int {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.refill()
	return int(tb.tokens)
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// Usage
func handleRequest(bucket *TokenBucket, req *Request) *Response {
	if !bucket.TryConsume(1) {
		return &Response{
			Status: 429,
			Body:   "Too Many Requests",
		}
	}
	return processRequest(req)
}
```

---

## Sliding Window Implementation

```go
package ratelimiting

import (
	"sync"
	"time"
)

// SlidingWindowRateLimiter implements sliding window rate limiting.
type SlidingWindowRateLimiter struct {
	mu          sync.RWMutex
	requests    map[string][]int64
	windowMs    int64
	maxRequests int
}

// NewSlidingWindowRateLimiter creates a sliding window rate limiter.
func NewSlidingWindowRateLimiter(windowMs int64, maxRequests int) *SlidingWindowRateLimiter {
	return &SlidingWindowRateLimiter{
		requests:    make(map[string][]int64),
		windowMs:    windowMs,
		maxRequests: maxRequests,
	}
}

// IsAllowed checks if a request is allowed.
func (s *SlidingWindowRateLimiter) IsAllowed(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UnixMilli()
	windowStart := now - s.windowMs

	// Get or initialize timestamps
	timestamps := s.requests[key]

	// Filter out old requests
	validTimestamps := make([]int64, 0, len(timestamps))
	for _, ts := range timestamps {
		if ts > windowStart {
			validTimestamps = append(validTimestamps, ts)
		}
	}

	if len(validTimestamps) >= s.maxRequests {
		s.requests[key] = validTimestamps
		return false
	}

	// Add current request
	validTimestamps = append(validTimestamps, now)
	s.requests[key] = validTimestamps

	return true
}

// GetRemainingRequests returns remaining requests for a key.
func (s *SlidingWindowRateLimiter) GetRemainingRequests(key string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now().UnixMilli()
	windowStart := now - s.windowMs

	timestamps := s.requests[key]
	validCount := 0
	for _, ts := range timestamps {
		if ts > windowStart {
			validCount++
		}
	}

	remaining := s.maxRequests - validCount
	if remaining < 0 {
		return 0
	}
	return remaining
}

// GetResetTime returns milliseconds until window resets.
func (s *SlidingWindowRateLimiter) GetResetTime(key string) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	timestamps := s.requests[key]
	if len(timestamps) == 0 {
		return 0
	}

	oldest := timestamps[0]
	for _, ts := range timestamps {
		if ts < oldest {
			oldest = ts
		}
	}

	resetTime := oldest + s.windowMs - time.Now().UnixMilli()
	if resetTime < 0 {
		return 0
	}
	return resetTime
}
```

---

## Multi-level Rate Limiter

```go
package ratelimiting

import (
	"time"
)

// RateLimitConfig defines rate limit configuration.
type RateLimitConfig struct {
	PerSecond int
	PerMinute int
	PerHour   int
	PerDay    int
}

// MultiLevelRateLimiter implements multi-level rate limiting.
type MultiLevelRateLimiter struct {
	limiters []struct {
		limiter  *SlidingWindowRateLimiter
		windowMs int64
		max      int
	}
}

// NewMultiLevelRateLimiter creates a multi-level rate limiter.
func NewMultiLevelRateLimiter(config RateLimitConfig) *MultiLevelRateLimiter {
	ml := &MultiLevelRateLimiter{}

	if config.PerSecond > 0 {
		ml.limiters = append(ml.limiters, struct {
			limiter  *SlidingWindowRateLimiter
			windowMs int64
			max      int
		}{
			limiter:  NewSlidingWindowRateLimiter(1000, config.PerSecond),
			windowMs: 1000,
			max:      config.PerSecond,
		})
	}

	if config.PerMinute > 0 {
		ml.limiters = append(ml.limiters, struct {
			limiter  *SlidingWindowRateLimiter
			windowMs int64
			max      int
		}{
			limiter:  NewSlidingWindowRateLimiter(60000, config.PerMinute),
			windowMs: 60000,
			max:      config.PerMinute,
		})
	}

	if config.PerHour > 0 {
		ml.limiters = append(ml.limiters, struct {
			limiter  *SlidingWindowRateLimiter
			windowMs int64
			max      int
		}{
			limiter:  NewSlidingWindowRateLimiter(3600000, config.PerHour),
			windowMs: 3600000,
			max:      config.PerHour,
		})
	}

	if config.PerDay > 0 {
		ml.limiters = append(ml.limiters, struct {
			limiter  *SlidingWindowRateLimiter
			windowMs int64
			max      int
		}{
			limiter:  NewSlidingWindowRateLimiter(86400000, config.PerDay),
			windowMs: 86400000,
			max:      config.PerDay,
		})
	}

	return ml
}

// RateLimitResult represents rate limit result.
type RateLimitResult struct {
	Allowed    bool
	RetryAfter int64
}

// IsAllowed checks if request is allowed across all levels.
func (ml *MultiLevelRateLimiter) IsAllowed(key string) RateLimitResult {
	for _, limiter := range ml.limiters {
		if !limiter.limiter.IsAllowed(key) {
			return RateLimitResult{
				Allowed:    false,
				RetryAfter: limiter.limiter.GetResetTime(key),
			}
		}
	}
	return RateLimitResult{Allowed: true}
}

// Usage
func handleAPIRequest(limiter *MultiLevelRateLimiter, userID string, req *Request) *Response {
	result := limiter.IsAllowed(userID)

	if !result.Allowed {
		return &Response{
			Status: 429,
			Body:   "Too Many Requests",
			Headers: map[string]string{
				"Retry-After": fmt.Sprintf("%d", result.RetryAfter/1000),
			},
		}
	}

	return processRequest(req)
}
```

---

## Distributed Rate Limiter (Redis)

```go
package ratelimiting

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisRateLimiter implements distributed rate limiting using Redis.
type RedisRateLimiter struct {
	client    *redis.Client
	keyPrefix string
	windowMs  int64
	maxRequests int
}

// NewRedisRateLimiter creates a Redis-based rate limiter.
func NewRedisRateLimiter(client *redis.Client, keyPrefix string, windowMs int64, maxRequests int) *RedisRateLimiter {
	return &RedisRateLimiter{
		client:      client,
		keyPrefix:   keyPrefix,
		windowMs:    windowMs,
		maxRequests: maxRequests,
	}
}

// RedisResult represents rate limit result from Redis.
type RedisResult struct {
	Allowed   bool
	Remaining int
	ResetAt   int64
}

// IsAllowed checks if request is allowed using Redis Lua script.
func (r *RedisRateLimiter) IsAllowed(ctx context.Context, key string) (RedisResult, error) {
	fullKey := fmt.Sprintf("%s:%s", r.keyPrefix, key)
	now := time.Now().UnixMilli()
	windowStart := now - r.windowMs

	script := redis.NewScript(`
		local key = KEYS[1]
		local windowStart = tonumber(ARGV[1])
		local now = tonumber(ARGV[2])
		local maxRequests = tonumber(ARGV[3])
		local windowMs = tonumber(ARGV[4])

		-- Remove old entries
		redis.call('ZREMRANGEBYSCORE', key, '-inf', windowStart)

		-- Count requests in window
		local count = redis.call('ZCARD', key)

		if count < maxRequests then
			-- Add current request
			redis.call('ZADD', key, now, now .. ':' .. math.random())
			redis.call('PEXPIRE', key, windowMs)
			return {1, maxRequests - count - 1, 0}
		else
			-- Find reset time
			local oldest = redis.call('ZRANGE', key, 0, 0, 'WITHSCORES')
			local resetAt = tonumber(oldest[2]) + windowMs - now
			return {0, 0, resetAt}
		end
	`)

	result, err := script.Run(ctx, r.client, []string{fullKey},
		windowStart, now, r.maxRequests, r.windowMs).Result()
	if err != nil {
		return RedisResult{}, fmt.Errorf("executing Lua script: %w", err)
	}

	res := result.([]interface{})
	return RedisResult{
		Allowed:   res[0].(int64) == 1,
		Remaining: int(res[1].(int64)),
		ResetAt:   res[2].(int64),
	}, nil
}
```

---

## HTTP Middleware

```go
package ratelimiting

import (
	"fmt"
	"net/http"
)

// RateLimitMiddleware creates HTTP middleware for rate limiting.
func RateLimitMiddleware(limiter *SlidingWindowRateLimiter, keyExtractor func(*http.Request) string) func(http.Handler) http.Handler {
	if keyExtractor == nil {
		keyExtractor = func(r *http.Request) string {
			return r.RemoteAddr
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := keyExtractor(r)

			if !limiter.IsAllowed(key) {
				remaining := limiter.GetRemainingRequests(key)
				resetTime := limiter.GetResetTime(key)

				w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", limiter.maxRequests))
				w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
				w.Header().Set("X-RateLimit-Reset", fmt.Sprintf("%d", resetTime/1000))
				w.Header().Set("Retry-After", fmt.Sprintf("%d", resetTime/1000))

				w.WriteHeader(http.StatusTooManyRequests)
				w.Write([]byte(`{"error":"Too Many Requests"}`))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// Usage
func setupRouter() *http.ServeMux {
	mux := http.NewServeMux()

	apiLimiter := NewSlidingWindowRateLimiter(60000, 100)

	// Apply rate limiting middleware
	mux.Handle("/api/", RateLimitMiddleware(apiLimiter, nil)(
		http.HandlerFunc(apiHandler),
	))

	// Premium users with higher limits
	premiumLimiter := NewSlidingWindowRateLimiter(60000, 1000)
	mux.Handle("/api/premium/", RateLimitMiddleware(premiumLimiter, func(r *http.Request) string {
		// Extract user ID from request
		userID := r.Header.Get("X-User-ID")
		if userID == "" {
			return r.RemoteAddr
		}
		return userID
	})(
		http.HandlerFunc(premiumAPIHandler),
	))

	return mux
}
```

---

## Algorithm Comparison

| Algorithm | Precision | Memory | Burst | Complexity |
|-----------|-----------|--------|-------|------------|
| Token Bucket | Medium | O(1) | Allows burst | Simple |
| Leaky Bucket | High | O(n) | Smooth | Medium |
| Fixed Window | Low | O(1) | Double burst possible | Simple |
| Sliding Log | High | O(n) | Precise | Complex |
| Sliding Counter | Medium | O(1) | Approximate | Medium |

---

## When to Use

- Public APIs (DoS protection)
- Expensive endpoints (generation, AI)
- Fair usage between users
- Abuse prevention
- Quota per service tier

---

## Related Patterns

| Pattern | Relation |
|---------|----------|
| [Bulkhead](bulkhead.md) | Limits concurrency vs throughput |
| [Circuit Breaker](circuit-breaker.md) | Complementary |
| Throttling | Client-side synonym |
| Backpressure | Producer-side |

---

## Sources

- [Rate Limiting Strategies](https://blog.cloudflare.com/counting-things-a-lot-of-different-things/)
- [Token Bucket Algorithm](https://en.wikipedia.org/wiki/Token_bucket)
- [Google Cloud - Rate Limiting](https://cloud.google.com/architecture/rate-limiting-strategies-techniques)
