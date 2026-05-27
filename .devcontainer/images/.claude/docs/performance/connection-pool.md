# Connection Pool

Pattern for managing reusable network connections (DB, HTTP, etc.).

---

## What is the Connection Pool?

> Maintain a set of pre-established connections to avoid connection overhead.

```
+--------------------------------------------------------------+
|                    Connection Pool                            |
|                                                               |
|  Application                Pool                   Database   |
|      |                        |                        |      |
|      |-- acquire() --------->|                        |      |
|      |<-- connection --------|                        |      |
|      |                        |                        |      |
|      |== query() ============|== SQL ================>|      |
|      |<= result =============|<= data ================|      |
|      |                        |                        |      |
|      |-- release() --------->|                        |      |
|      |                   [kept alive]                  |      |
|                                                               |
|  +--------+  +--------+  +--------+  +--------+               |
|  | conn 1 |  | conn 2 |  | conn 3 |  | conn 4 |               |
|  | (busy) |  | (idle) |  | (idle) |  | (busy) |               |
|  +--------+  +--------+  +--------+  +--------+               |
+--------------------------------------------------------------+
```

**Why:**

- Avoid TCP/TLS handshake on each request
- Limit the number of connections to the server
- Reduce request latency

---

## Go Implementation

```go
package connpool

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	ErrPoolClosed    = errors.New("pool is closed")
	ErrAcquireTimeout = errors.New("acquire timeout")
)

// PooledConnection represents a poolable connection.
type PooledConnection interface {
	Query(ctx context.Context, sql string, params ...any) (any, error)
	IsAlive(ctx context.Context) bool
	Close() error
}

// Config holds connection pool configuration.
type Config struct {
	MinConnections    int
	MaxConnections    int
	AcquireTimeout    time.Duration
	IdleTimeout       time.Duration
	ConnectionFactory func(context.Context) (PooledConnection, error)
}

// ConnectionPool manages a pool of connections.
type ConnectionPool struct {
	config  Config
	idle    []PooledConnection
	active  map[PooledConnection]struct{}
	waiting []chan PooledConnection
	mu      sync.Mutex
	closed  bool
	done    chan struct{}
}

// New creates a new connection pool.
func New(ctx context.Context, config Config) (*ConnectionPool, error) {
	cp := &ConnectionPool{
		config:  config,
		idle:    make([]PooledConnection, 0, config.MinConnections),
		active:  make(map[PooledConnection]struct{}),
		waiting: make([]chan PooledConnection, 0),
		done:    make(chan struct{}),
	}

	if err := cp.initPool(ctx); err != nil {
		return nil, err
	}

	go cp.idleChecker()
	return cp, nil
}

func (cp *ConnectionPool) initPool(ctx context.Context) error {
	for i := 0; i < cp.config.MinConnections; i++ {
		conn, err := cp.config.ConnectionFactory(ctx)
		if err != nil {
			return err
		}
		cp.idle = append(cp.idle, conn)
	}
	return nil
}

// Acquire gets a connection from the pool.
func (cp *ConnectionPool) Acquire(ctx context.Context) (PooledConnection, error) {
	cp.mu.Lock()

	if cp.closed {
		cp.mu.Unlock()
		return nil, ErrPoolClosed
	}

	// Try to get idle connection
	for len(cp.idle) > 0 {
		conn := cp.idle[len(cp.idle)-1]
		cp.idle = cp.idle[:len(cp.idle)-1]

		if conn.IsAlive(ctx) {
			cp.active[conn] = struct{}{}
			cp.mu.Unlock()
			return conn, nil
		}
		conn.Close()
	}

	// Create new connection if under limit
	if cp.totalConnections() < cp.config.MaxConnections {
		cp.mu.Unlock()
		conn, err := cp.config.ConnectionFactory(ctx)
		if err != nil {
			return nil, err
		}
		cp.mu.Lock()
		cp.active[conn] = struct{}{}
		cp.mu.Unlock()
		return conn, nil
	}

	// Wait for available connection
	waiter := make(chan PooledConnection, 1)
	cp.waiting = append(cp.waiting, waiter)
	cp.mu.Unlock()

	select {
	case conn := <-waiter:
		return conn, nil
	case <-time.After(cp.config.AcquireTimeout):
		cp.mu.Lock()
		cp.removeWaiter(waiter)
		cp.mu.Unlock()
		return nil, ErrAcquireTimeout
	case <-ctx.Done():
		cp.mu.Lock()
		cp.removeWaiter(waiter)
		cp.mu.Unlock()
		return nil, ctx.Err()
	}
}

// Release returns a connection to the pool.
func (cp *ConnectionPool) Release(conn PooledConnection) {
	cp.mu.Lock()
	defer cp.mu.Unlock()

	if !cp.isActive(conn) {
		return
	}

	delete(cp.active, conn)

	// Give to waiter if present
	if len(cp.waiting) > 0 {
		waiter := cp.waiting[0]
		cp.waiting = cp.waiting[1:]
		cp.active[conn] = struct{}{}
		waiter <- conn
		return
	}

	// Return to idle pool
	cp.idle = append(cp.idle, conn)
}

// WithConnection executes a function with a pooled connection.
func (cp *ConnectionPool) WithConnection(
	ctx context.Context,
	fn func(PooledConnection) error,
) error {
	conn, err := cp.Acquire(ctx)
	if err != nil {
		return err
	}
	defer cp.Release(conn)
	return fn(conn)
}

func (cp *ConnectionPool) totalConnections() int {
	return len(cp.idle) + len(cp.active)
}

func (cp *ConnectionPool) isActive(conn PooledConnection) bool {
	_, ok := cp.active[conn]
	return ok
}

func (cp *ConnectionPool) removeWaiter(waiter chan PooledConnection) {
	for i, w := range cp.waiting {
		if w == waiter {
			cp.waiting = append(cp.waiting[:i], cp.waiting[i+1:]...)
			close(waiter)
			break
		}
	}
}

func (cp *ConnectionPool) idleChecker() {
	ticker := time.NewTicker(cp.config.IdleTimeout)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			cp.checkIdleConnections()
		case <-cp.done:
			return
		}
	}
}

func (cp *ConnectionPool) checkIdleConnections() {
	cp.mu.Lock()
	defer cp.mu.Unlock()

	alive := make([]PooledConnection, 0, len(cp.idle))
	ctx := context.Background()

	for _, conn := range cp.idle {
		if conn.IsAlive(ctx) {
			alive = append(alive, conn)
		} else {
			conn.Close()
		}
	}

	cp.idle = alive
}

// Close closes all connections in the pool.
func (cp *ConnectionPool) Close() error {
	cp.mu.Lock()
	defer cp.mu.Unlock()

	if cp.closed {
		return nil
	}

	cp.closed = true
	close(cp.done)

	// Close all idle connections
	for _, conn := range cp.idle {
		conn.Close()
	}

	// Close all active connections
	for conn := range cp.active {
		conn.Close()
	}

	cp.idle = nil
	cp.active = nil

	return nil
}
```

---

## Recommended Configuration

```go
package main

import (
	"context"
	"database/sql"
	"time"
)

type PostgresConnection struct {
	db *sql.DB
}

func (pc *PostgresConnection) Query(ctx context.Context, query string, params ...any) (any, error) {
	return pc.db.QueryContext(ctx, query, params...)
}

func (pc *PostgresConnection) IsAlive(ctx context.Context) bool {
	return pc.db.PingContext(ctx) == nil
}

func (pc *PostgresConnection) Close() error {
	return pc.db.Close()
}

func main() {
	config := connpool.Config{
		MinConnections: 5,
		MaxConnections: 20,
		AcquireTimeout: 30 * time.Second,
		IdleTimeout:    60 * time.Second,
		ConnectionFactory: func(ctx context.Context) (connpool.PooledConnection, error) {
			db, err := sql.Open("postgres", "postgres://localhost/mydb")
			if err != nil {
				return nil, err
			}
			if err := db.PingContext(ctx); err != nil {
				return nil, err
			}
			return &PostgresConnection{db: db}, nil
		},
	}

	ctx := context.Background()
	pool, err := connpool.New(ctx, config)
	if err != nil {
		panic(err)
	}
	defer pool.Close()

	// Usage
	err = pool.WithConnection(ctx, func(conn connpool.PooledConnection) error {
		_, err := conn.Query(ctx, "SELECT * FROM users WHERE active = $1", true)
		return err
	})
	if err != nil {
		panic(err)
	}
}
```

---

## Complexity and Trade-offs

| Aspect | Value |
|--------|-------|
| Acquisition (idle available) | O(1) |
| Acquisition (creation) | O(handshake) |
| Release | O(1) |
| Memory | O(maxConnections) |

### Advantages

- Reduced latency (no handshake)
- Limits load on the DB server
- Automatic dead connection management

### Disadvantages

- Unused connections consume resources
- Configuration complexity (sizing)
- Possible deadlock if pool is too small

---

## When to Use

- Applications with frequent connections to a database
- HTTP/gRPC services requiring persistent connections
- Systems with high connection latency (TLS handshake, authentication)
- High-traffic applications requiring connection limiting
- Microservices communicating with external backends (cache, queue, DB)

---

## Related Patterns

| Pattern | Relation |
|---------|----------|
| **Object Pool** | Generalization |
| **Circuit Breaker** | Protection if server is down |
| **Retry** | Acquisition resilience |
| **Semaphore** | Similar limitation |

---

## Sources

- [HikariCP](https://github.com/brettwooldridge/HikariCP) - High-perf Java pool
- [node-postgres Pool](https://node-postgres.com/features/pooling)
- [Database Connection Pooling Best Practices](https://vladmihalcea.com/connection-pooling/)
