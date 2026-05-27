# Materialized View Pattern

> Pre-compute and store optimized views for frequent queries.

## Principle

```
                    ┌─────────────────────────────────────────────┐
                    │            MATERIALIZED VIEW                 │
                    └─────────────────────────────────────────────┘

  WITHOUT (complex query every time):
  ┌─────────┐   SELECT + JOIN + AGGREGATE   ┌─────────┐
  │  Client │ ──────────────────────────▶   │   DB    │
  └─────────┘        (slow, CPU)            └─────────┘

  WITH (direct read):
  ┌─────────┐                               ┌─────────────────┐
  │  Client │ ───────── SELECT ──────────▶  │Materialized View│
  └─────────┘           (fast)              └────────┬────────┘
                                                     │
                                              Pre-calculated
                                                     │
  ┌─────────┐   Write   ┌─────────┐   Refresh  ┌────▼────┐
  │ Writer  │ ────────▶ │   DB    │ ──────────▶│  View   │
  └─────────┘           └─────────┘            └─────────┘
```

## Refresh Strategies

```
1. COMPLETE REFRESH (recreate)
   ┌────────┐       ┌──────────────┐
   │  Data  │ ────▶ │ DROP + CREATE│
   └────────┘       └──────────────┘
   + Simple
   - Slow, unavailability

2. INCREMENTAL REFRESH (delta)
   ┌────────┐       ┌──────────────┐
   │Changes │ ────▶ │ UPDATE VIEW  │
   └────────┘       └──────────────┘
   + Fast
   - Complex, not always possible

3. ON-DEMAND (lazy)
   - Refresh when query detects stale
   + Always fresh
   - First query latency

4. SCHEDULED (cron)
   - Refresh every X minutes
   + Predictable
   - Potentially stale data
```

## Go Example

```go
package materializedview

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// OrderStats represents aggregated order statistics for a user.
type OrderStats struct {
	UserID            string    `json:"userId"`
	TotalOrders       int       `json:"totalOrders"`
	TotalAmount       float64   `json:"totalAmount"`
	AverageOrderValue float64   `json:"averageOrderValue"`
	LastOrderDate     time.Time `json:"lastOrderDate"`
}

// Database defines database operations.
type Database interface {
	Query(ctx context.Context, query string, args ...interface{}) ([]map[string]interface{}, error)
	Exec(ctx context.Context, query string, args ...interface{}) error
}

// Cache defines cache operations.
type Cache interface {
	HSet(ctx context.Context, key string, values map[string]interface{}) error
	HGetAll(ctx context.Context, key string) (map[string]string, error)
	HIncrBy(ctx context.Context, key, field string, increment int64) error
	HIncrByFloat(ctx context.Context, key, field string, increment float64) error
}

// MaterializedViewService manages materialized views.
type MaterializedViewService struct {
	db    Database
	cache Cache
}

// NewMaterializedViewService creates a new MaterializedViewService.
func NewMaterializedViewService(db Database, cache Cache) *MaterializedViewService {
	return &MaterializedViewService{
		db:    db,
		cache: cache,
	}
}

// RefreshUserOrderStats refreshes user order statistics (complete refresh).
func (mvs *MaterializedViewService) RefreshUserOrderStats(ctx context.Context) error {
	query := `
		SELECT
			user_id,
			COUNT(*) as total_orders,
			SUM(amount) as total_amount,
			AVG(amount) as average_order_value,
			MAX(created_at) as last_order_date
		FROM orders
		WHERE status = 'completed'
		GROUP BY user_id
	`

	rows, err := mvs.db.Query(ctx, query)
	if err != nil {
		return fmt.Errorf("querying order stats: %w", err)
	}

	// Store in cache
	for _, row := range rows {
		userID := row["user_id"].(string)
		key := fmt.Sprintf("user_stats:%s", userID)

		values := map[string]interface{}{
			"totalOrders":       row["total_orders"],
			"totalAmount":       row["total_amount"],
			"averageOrderValue": row["average_order_value"],
			"lastOrderDate":     row["last_order_date"].(time.Time).Format(time.RFC3339),
		}

		if err := mvs.cache.HSet(ctx, key, values); err != nil {
			return fmt.Errorf("caching stats for user %s: %w", userID, err)
		}
	}

	return nil
}

// GetUserStats retrieves user statistics from cache.
func (mvs *MaterializedViewService) GetUserStats(ctx context.Context, userID string) (*OrderStats, error) {
	key := fmt.Sprintf("user_stats:%s", userID)

	data, err := mvs.cache.HGetAll(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("getting stats from cache: %w", err)
	}

	if len(data) == 0 {
		return nil, nil
	}

	lastOrderDate, err := time.Parse(time.RFC3339, data["lastOrderDate"])
	if err != nil {
		return nil, fmt.Errorf("parsing last order date: %w", err)
	}

	stats := &OrderStats{
		UserID:        userID,
		LastOrderDate: lastOrderDate,
	}

	// Parse numeric fields
	fmt.Sscanf(data["totalOrders"], "%d", &stats.TotalOrders)
	fmt.Sscanf(data["totalAmount"], "%f", &stats.TotalAmount)
	fmt.Sscanf(data["averageOrderValue"], "%f", &stats.AverageOrderValue)

	return stats, nil
}

// OnOrderCompleted updates stats incrementally after order completion.
func (mvs *MaterializedViewService) OnOrderCompleted(ctx context.Context, userID string, amount float64, createdAt time.Time) error {
	key := fmt.Sprintf("user_stats:%s", userID)

	// Atomic increment
	if err := mvs.cache.HIncrBy(ctx, key, "totalOrders", 1); err != nil {
		return fmt.Errorf("incrementing total orders: %w", err)
	}

	if err := mvs.cache.HIncrByFloat(ctx, key, "totalAmount", amount); err != nil {
		return fmt.Errorf("incrementing total amount: %w", err)
	}

	// Update last order date
	values := map[string]interface{}{
		"lastOrderDate": createdAt.Format(time.RFC3339),
	}
	if err := mvs.cache.HSet(ctx, key, values); err != nil {
		return fmt.Errorf("updating last order date: %w", err)
	}

	// Recalculate average
	stats, err := mvs.GetUserStats(ctx, userID)
	if err != nil {
		return fmt.Errorf("getting user stats: %w", err)
	}

	if stats != nil && stats.TotalOrders > 0 {
		newAvg := stats.TotalAmount / float64(stats.TotalOrders)
		avgValues := map[string]interface{}{
			"averageOrderValue": fmt.Sprintf("%f", newAvg),
		}
		if err := mvs.cache.HSet(ctx, key, avgValues); err != nil {
			return fmt.Errorf("updating average: %w", err)
		}
	}

	return nil
}
```

## DB Implementation (Go)

```go
// This example follows the same idiomatic Go patterns
// as the main example above.
// Specific implementation based on interfaces and
// standard Go conventions.
```

## Strategy Comparison

| Strategy | Read Latency | Freshness | Complexity |
|----------|--------------|-----------|------------|
| Standard SQL View | High | Real-time | Low |
| Materialized View DB | Low | Depends on refresh | Medium |
| Cache (Redis) | Very low | Depends on TTL | Medium |
| Search Engine (ES) | Low | Depends on sync | High |

## When to Use

| Situation | Recommended |
|-----------|-------------|
| Complex analytical queries | Yes |
| Real-time dashboards | Yes (with refresh) |
| Full-text search | Yes |
| Highly volatile data | With caution |
| ACID transactions required | No |

## Related Patterns

| Pattern | Relation |
|---------|----------|
| CQRS | Read model = materialized view |
| Event Sourcing | Projections |
| Cache-Aside | Simpler alternative |
| ETL | Transformation pipelines |

## Sources

- [Microsoft - Materialized View](https://learn.microsoft.com/en-us/azure/architecture/patterns/materialized-view)
- [PostgreSQL Materialized Views](https://www.postgresql.org/docs/current/rules-materializedviews.html)
- [Martin Fowler - CQRS](https://martinfowler.com/bliki/CQRS.html)
