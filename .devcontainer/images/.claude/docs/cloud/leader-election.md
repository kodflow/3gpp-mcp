# Leader Election Pattern

> Coordinate actions by electing a leader among distributed instances.

## Principle

```
                    ┌─────────────────────────────────────────────┐
                    │              LEADER ELECTION                 │
                    └─────────────────────────────────────────────┘

  WITHOUT LEADER (chaos):
  ┌─────────┐  ┌─────────┐  ┌─────────┐
  │ Node 1  │  │ Node 2  │  │ Node 3  │
  │ Process │  │ Process │  │ Process │  <-- All execute = duplication
  └─────────┘  └─────────┘  └─────────┘

  WITH LEADER:
  ┌─────────┐  ┌─────────┐  ┌─────────┐
  │ Node 1  │  │ Node 2  │  │ Node 3  │
  │ LEADER  │  │Follower │  │Follower │
  │ Process │  │ Standby │  │ Standby │
  └────┬────┘  └────┬────┘  └────┬────┘
       │            │            │
       └────────────┼────────────┘
                    │
            ┌───────▼───────┐
            │  Coordination │
            │   (etcd/ZK)   │
            └───────────────┘
```

## Election Mechanisms

```
1. BULLY ALGORITHM
   - Highest ID becomes leader
   - Simple but not partition tolerant

2. RAFT CONSENSUS
   ┌─────────────────────────────────────────┐
   │  Follower ──▶ Candidate ──▶ Leader     │
   │      │             │           │        │
   │      │   timeout   │  majority │        │
   │      │   no leader │  votes    │        │
   │      │             │           │        │
   │      └─────────────────────────┘        │
   │              heartbeat                  │
   └─────────────────────────────────────────┘

3. LEASE-BASED (distributed lock)
   - Acquires a lock with TTL
   - Renews before expiration
   - Lock expires = new election
```

## Go Example with Redis

```go
package leaderelection

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// LeaderElection defines leader election operations.
type LeaderElection interface {
	TryBecomeLeader(ctx context.Context) (bool, error)
	IsLeader() bool
	Resign(ctx context.Context) error
	OnLeadershipChange(callback func(isLeader bool))
}

// RedisClient defines Redis operations needed for leader election.
type RedisClient interface {
	SetNX(ctx context.Context, key, value string, expiration time.Duration) (bool, error)
	Get(ctx context.Context, key string) (string, error)
	Eval(ctx context.Context, script string, keys []string, args ...interface{}) (interface{}, error)
}

// RedisLeaderElection implements leader election using Redis.
type RedisLeaderElection struct {
	redis               RedisClient
	lockKey             string
	nodeID              string
	leaseTTL            time.Duration
	renewInterval       time.Duration
	isLeader            bool
	renewalTicker       *time.Ticker
	renewalStop         chan struct{}
	listeners           []func(bool)
	mu                  sync.RWMutex
}

// NewRedisLeaderElection creates a new RedisLeaderElection.
func NewRedisLeaderElection(
	redis RedisClient,
	lockKey string,
	nodeID string,
	leaseTTL time.Duration,
	renewInterval time.Duration,
) *RedisLeaderElection {
	return &RedisLeaderElection{
		redis:         redis,
		lockKey:       lockKey,
		nodeID:        nodeID,
		leaseTTL:      leaseTTL,
		renewInterval: renewInterval,
		listeners:     make([]func(bool), 0),
		renewalStop:   make(chan struct{}),
	}
}

// TryBecomeLeader attempts to acquire leadership.
func (rle *RedisLeaderElection) TryBecomeLeader(ctx context.Context) (bool, error) {
	// SET NX = only if not exists, with expiration
	acquired, err := rle.redis.SetNX(ctx, rle.lockKey, rle.nodeID, rle.leaseTTL)
	if err != nil {
		return false, fmt.Errorf("trying to acquire lock: %w", err)
	}

	if acquired {
		rle.mu.Lock()
		rle.isLeader = true
		rle.mu.Unlock()

		rle.startRenewal()
		rle.notifyListeners(true)
		return true, nil
	}

	// Check if we already own it
	currentLeader, err := rle.redis.Get(ctx, rle.lockKey)
	if err != nil {
		return false, fmt.Errorf("checking current leader: %w", err)
	}

	if currentLeader == rle.nodeID {
		rle.mu.Lock()
		rle.isLeader = true
		rle.mu.Unlock()
		return true, nil
	}

	return false, nil
}

// IsLeader returns whether this node is the leader.
func (rle *RedisLeaderElection) IsLeader() bool {
	rle.mu.RLock()
	defer rle.mu.RUnlock()
	return rle.isLeader
}

// Resign gives up leadership.
func (rle *RedisLeaderElection) Resign(ctx context.Context) error {
	rle.mu.Lock()
	if rle.renewalTicker != nil {
		rle.renewalTicker.Stop()
		close(rle.renewalStop)
	}
	rle.mu.Unlock()

	// Only delete if we own it (Lua script for atomicity)
	script := `
		if redis.call("get", KEYS[1]) == ARGV[1] then
			return redis.call("del", KEYS[1])
		else
			return 0
		end
	`

	_, err := rle.redis.Eval(ctx, script, []string{rle.lockKey}, rle.nodeID)
	if err != nil {
		return fmt.Errorf("resigning leadership: %w", err)
	}

	rle.mu.Lock()
	rle.isLeader = false
	rle.mu.Unlock()

	rle.notifyListeners(false)
	return nil
}

// OnLeadershipChange registers a callback for leadership changes.
func (rle *RedisLeaderElection) OnLeadershipChange(callback func(isLeader bool)) {
	rle.mu.Lock()
	defer rle.mu.Unlock()
	rle.listeners = append(rle.listeners, callback)
}

func (rle *RedisLeaderElection) startRenewal() {
	rle.mu.Lock()
	rle.renewalTicker = time.NewTicker(rle.renewInterval)
	rle.renewalStop = make(chan struct{})
	ticker := rle.renewalTicker
	stop := rle.renewalStop
	rle.mu.Unlock()

	go func() {
		for {
			select {
			case <-ticker.C:
				if err := rle.renewLease(context.Background()); err != nil {
					fmt.Printf("Lease renewal failed: %v
", err)

					rle.mu.Lock()
					rle.isLeader = false
					rle.mu.Unlock()

					rle.notifyListeners(false)
					return
				}
			case <-stop:
				return
			}
		}
	}()
}

func (rle *RedisLeaderElection) renewLease(ctx context.Context) error {
	// Extend TTL only if we own the lock
	script := `
		if redis.call("get", KEYS[1]) == ARGV[1] then
			return redis.call("expire", KEYS[1], ARGV[2])
		else
			return 0
		end
	`

	result, err := rle.redis.Eval(
		ctx,
		script,
		[]string{rle.lockKey},
		rle.nodeID,
		int(rle.leaseTTL.Seconds()),
	)
	if err != nil {
		return fmt.Errorf("renewing lease: %w", err)
	}

	if result == 0 {
		return fmt.Errorf("lost leadership")
	}

	return nil
}

func (rle *RedisLeaderElection) notifyListeners(isLeader bool) {
	rle.mu.RLock()
	listeners := make([]func(bool), len(rle.listeners))
	copy(listeners, rle.listeners)
	rle.mu.RUnlock()

	for _, listener := range listeners {
		listener(isLeader)
	}
}
```

## Usage with periodic task (Go)

```go
// This example follows the same idiomatic Go patterns
// as the main example above.
// Specific implementation based on interfaces and
// standard Go conventions.
```

## Cloud native solutions

| Service | Usage |
|---------|-------|
| **etcd** | Kubernetes, Raft consensus |
| **Consul** | HashiCorp, sessions and locks |
| **ZooKeeper** | Apache, ephemeral znodes |
| **Redis** | Redlock algorithm |
| **DynamoDB** | Conditional writes |

## When to Use

| Situation | Recommended |
|-----------|-------------|
| Distributed cron tasks | Yes |
| Cluster coordination | Yes |
| Master/Replica database | Yes |
| All instances equivalent | No (not needed) |
| Pure stateless | No |

## Related Patterns

| Pattern | Relation |
|---------|----------|
| Singleton (distributed) | Guarantee a single instance |
| Bulkhead | Leader/followers isolation |
| Health Check | Failing leader detection |
| Sharding | Leader per shard |

## Sources

- [Microsoft - Leader Election](https://learn.microsoft.com/en-us/azure/architecture/patterns/leader-election)
- [Raft Consensus Algorithm](https://raft.github.io/)
- [Redis Distributed Locks](https://redis.io/docs/manual/patterns/distributed-locks/)
