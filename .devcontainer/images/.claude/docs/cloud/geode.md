# Geode Pattern (Geodes / Deployment Stamps)

> Deploy identical units across multiple geographic regions.

## Principle

```
┌─────────────────────────────────────────────────────────────────────────┐
│                          GLOBAL TRAFFIC MANAGER                          │
│                         (DNS / Load Balancer)                            │
│                                                                          │
│   Routes to the closest / most performant / available region             │
└──────────────────────────────────┬──────────────────────────────────────┘
                                   │
         ┌─────────────────────────┼─────────────────────────┐
         ▼                         ▼                         ▼
┌─────────────────┐      ┌─────────────────┐      ┌─────────────────┐
│   GEODE EU      │      │   GEODE US      │      │   GEODE ASIA    │
│                 │      │                 │      │                 │
│  ┌───────────┐  │      │  ┌───────────┐  │      │  ┌───────────┐  │
│  │  Service  │  │      │  │  Service  │  │      │  │  Service  │  │
│  │   Stack   │  │      │  │   Stack   │  │      │  │   Stack   │  │
│  └───────────┘  │      │  └───────────┘  │      │  └───────────┘  │
│  ┌───────────┐  │      │  ┌───────────┐  │      │  ┌───────────┐  │
│  │  Database │  │      │  │  Database │  │      │  │  Database │  │
│  │  (local)  │  │      │  │  (local)  │  │      │  │  (local)  │  │
│  └───────────┘  │      │  └───────────┘  │      │  └───────────┘  │
│  ┌───────────┐  │      │  ┌───────────┐  │      │  ┌───────────┐  │
│  │   Cache   │  │      │  │   Cache   │  │      │  │   Cache   │  │
│  └───────────┘  │      │  └───────────┘  │      │  └───────────┘  │
└─────────────────┘      └─────────────────┘      └─────────────────┘
         │                         │                         │
         └─────────────────────────┴─────────────────────────┘
                                   │
                    ┌──────────────┴──────────────┐
                    │     REPLICATION LAYER       │
                    │  (Async / Eventual Consist) │
                    └─────────────────────────────┘
```

## Geode Components

| Component | Description |
|-----------|-------------|
| **Application Stack** | Identical services per region |
| **Local Database** | Local database (replica or partition) |
| **Cache Layer** | Local Redis/Memcached |
| **Message Queue** | Local Kafka/RabbitMQ |
| **Storage** | Regional blob storage |

## Go Example

```go
package geode

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// GeodeConfig defines configuration for a geode.
type GeodeConfig struct {
	Region             string
	Endpoint           string
	IsPrimary          bool
	ReplicationTargets []string
}

// DataItem represents a data item with version and region info.
type DataItem struct {
	ID        string
	Data      interface{}
	Version   int64
	Region    string
	Timestamp time.Time
}

// Database defines database operations.
type Database interface {
	Upsert(ctx context.Context, item *DataItem) error
	Find(ctx context.Context, id string) (*DataItem, error)
}

// ReplicationClient handles replication to other geodes.
type ReplicationClient interface {
	Send(ctx context.Context, target string, item *DataItem) error
	QueueRetry(ctx context.Context, target string, item *DataItem) error
}

// GeodeDataStore manages data with multi-region replication.
type GeodeDataStore struct {
	config            GeodeConfig
	localDB           Database
	replicationClient ReplicationClient
}

// NewGeodeDataStore creates a new GeodeDataStore.
func NewGeodeDataStore(config GeodeConfig, db Database, replClient ReplicationClient) *GeodeDataStore {
	return &GeodeDataStore{
		config:            config,
		localDB:           db,
		replicationClient: replClient,
	}
}

// Write writes data locally and replicates to other geodes.
func (gds *GeodeDataStore) Write(ctx context.Context, id string, data interface{}) (*DataItem, error) {
	item := &DataItem{
		ID:        id,
		Data:      data,
		Version:   time.Now().UnixNano(),
		Region:    gds.config.Region,
		Timestamp: time.Now(),
	}

	// Write local
	if err := gds.localDB.Upsert(ctx, item); err != nil {
		return nil, fmt.Errorf("local write failed: %w", err)
	}

	// Async replication
	go gds.replicateAsync(context.Background(), item)

	return item, nil
}

// Read reads from local geode.
func (gds *GeodeDataStore) Read(ctx context.Context, id string) (*DataItem, error) {
	return gds.localDB.Find(ctx, id)
}

func (gds *GeodeDataStore) replicateAsync(ctx context.Context, item *DataItem) {
	var wg sync.WaitGroup

	for _, target := range gds.config.ReplicationTargets {
		wg.Go(func() {
			if err := gds.replicationClient.Send(ctx, target, item); err != nil {
				fmt.Printf("Replication to %s failed: %v\n", target, err)
				// Queue for retry
				gds.replicationClient.QueueRetry(ctx, target, item)
			}
		})
	}

	wg.Wait()
}

// HandleReplication handles incoming replication from other geodes.
func (gds *GeodeDataStore) HandleReplication(ctx context.Context, item *DataItem) error {
	existing, err := gds.localDB.Find(ctx, item.ID)
	if err != nil {
		// Not found, just insert
		return gds.localDB.Upsert(ctx, item)
	}

	// Conflict resolution: Last Write Wins
	if item.Version > existing.Version {
		return gds.localDB.Upsert(ctx, item)
	}

	return nil
}

// GeodeRouter routes requests to the closest healthy geode.
type GeodeRouter struct {
	geodes map[string]*GeodeConfig
	client *http.Client
}

// NewGeodeRouter creates a new GeodeRouter.
func NewGeodeRouter(geodes map[string]*GeodeConfig) *GeodeRouter {
	return &GeodeRouter{
		geodes: geodes,
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

// Route returns the endpoint for the best geode.
func (gr *GeodeRouter) Route(r *http.Request) (string, error) {
	clientRegion := gr.detectClientRegion(r)
	healthyGeodes, err := gr.getHealthyGeodes()
	if err != nil {
		return "", fmt.Errorf("no healthy geodes: %w", err)
	}

	bestGeode := gr.findClosestGeode(clientRegion, healthyGeodes)
	if bestGeode == nil {
		return "", fmt.Errorf("no suitable geode found")
	}

	return bestGeode.Endpoint, nil
}

func (gr *GeodeRouter) detectClientRegion(r *http.Request) string {
	// Check CloudFlare header
	if region := r.Header.Get("CF-IPCountry"); region != "" {
		return region
	}
	if region := r.Header.Get("X-Client-Region"); region != "" {
		return region
	}
	return "US"
}

func (gr *GeodeRouter) getHealthyGeodes() ([]*GeodeConfig, error) {
	healthy := make([]*GeodeConfig, 0)

	for _, geode := range gr.geodes {
		if gr.checkHealth(geode) {
			healthy = append(healthy, geode)
		}
	}

	if len(healthy) == 0 {
		return nil, fmt.Errorf("no healthy geodes")
	}

	return healthy, nil
}

func (gr *GeodeRouter) findClosestGeode(region string, geodes []*GeodeConfig) *GeodeConfig {
	// Region mapping logic (simplified)
	regionMapping := map[string][]string{
		"FR": {"eu-west-1", "eu-central-1"},
		"DE": {"eu-central-1", "eu-west-1"},
		"US": {"us-east-1", "us-west-2"},
		"JP": {"ap-northeast-1", "ap-southeast-1"},
	}

	preferredRegions := regionMapping[region]
	if preferredRegions == nil {
		preferredRegions = []string{"us-east-1"}
	}

	for _, preferred := range preferredRegions {
		for _, geode := range geodes {
			if geode.Region == preferred {
				return geode
			}
		}
	}

	if len(geodes) > 0 {
		return geodes[0]
	}

	return nil
}

func (gr *GeodeRouter) checkHealth(geode *GeodeConfig) bool {
	resp, err := gr.client.Get(geode.Endpoint + "/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}
```

## Replication Strategies

| Strategy | Latency | Consistency | Use Case |
|----------|---------|-------------|----------|
| **Sync** | High | Strong | Critical data |
| **Async** | Low | Eventual | Most cases |
| **CRDT** | Low | Eventual (auto-merge) | Counters, sets |
| **Event Sourcing** | Low | Eventual + audit | Finance, audit |

## Infrastructure as Code

```hcl
# Terraform - Multi-region deployment
module "geode" {
  for_each = toset(["eu-west-1", "us-east-1", "ap-northeast-1"])

  source = "./modules/geode"

  region           = each.key
  app_version      = var.app_version
  instance_count   = var.instances_per_geode
  database_size    = var.db_size

  replication_targets = [
    for r in toset(["eu-west-1", "us-east-1", "ap-northeast-1"]) :
    r if r != each.key
  ]
}

resource "aws_route53_record" "global" {
  zone_id = var.zone_id
  name    = "api.example.com"
  type    = "A"

  latency_routing_policy {
    region = each.key
  }

  alias {
    name    = module.geode[each.key].alb_dns_name
    zone_id = module.geode[each.key].alb_zone_id
  }
}
```

## Anti-patterns

| Anti-pattern | Problem | Solution |
|--------------|---------|----------|
| Sync replication | Global latency | Async with eventual consistency |
| Non-partitioned data | Frequent conflicts | Partition by region/tenant |
| No conflict resolution | Data loss | LWW or CRDT |
| Non-autonomous geode | Inter-region dependency | Self-contained stack |

## When to Use

- Global applications with users across multiple continents
- Low latency requirements for all regions
- High availability with regional failure tolerance
- Regulatory compliance requiring data residency
- Horizontal scalability by geographic region

## Related Patterns

| Pattern | Relation |
|---------|----------|
| CQRS | Read models per region |
| Event Sourcing | Event-based replication |
| Sharding | Data partitioning |
| Active-Active | HA strategy |

## Sources

- [Microsoft - Geode Pattern](https://learn.microsoft.com/en-us/azure/architecture/patterns/geodes)
- [Microsoft - Deployment Stamps](https://learn.microsoft.com/en-us/azure/architecture/patterns/deployment-stamp)
- [CRDTs](https://crdt.tech/)
