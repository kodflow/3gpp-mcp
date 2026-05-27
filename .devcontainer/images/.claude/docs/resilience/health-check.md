# Health Check Pattern

> Verify a service's health to enable automatic detection and recovery.

---

## Principle

```
┌─────────────────────────────────────────────────────────────────┐
│                     HEALTH CHECK TYPES                           │
│                                                                  │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐           │
│  │   LIVENESS   │  │  READINESS   │  │   STARTUP    │           │
│  │              │  │              │  │              │           │
│  │  "Am I       │  │  "Can I      │  │  "Am I       │           │
│  │   alive?"    │  │   serve?"    │  │   ready?"    │           │
│  │              │  │              │  │              │           │
│  │  → Restart   │  │  → No traffic│  │  → Wait      │           │
│  └──────────────┘  └──────────────┘  └──────────────┘           │
│                                                                  │
│  Kubernetes:                                                     │
│  livenessProbe     readinessProbe    startupProbe               │
└─────────────────────────────────────────────────────────────────┘
```

---

## Types of Probes

| Type | Question | Action on Failure | Usage |
|------|----------|-------------------|-------|
| **Liveness** | Is the process running? | Restart container | Deadlocks, crashes |
| **Readiness** | Can it receive traffic? | Remove from load balancer | Warmup, dependencies |
| **Startup** | Has it started correctly? | Wait or restart | Slow startup |

---

## Go Implementation

### Health Check Interface

```go
package healthcheck

import (
	"context"
	"time"
)

// Status represents health status.
type Status string

const (
	StatusHealthy   Status = "healthy"
	StatusDegraded  Status = "degraded"
	StatusUnhealthy Status = "unhealthy"
)

// ComponentHealth represents the health of a component.
type ComponentHealth struct {
	Status    Status        `json:"status"`
	Message   string        `json:"message,omitempty"`
	Duration  time.Duration `json:"duration,omitempty"`
	LastCheck time.Time     `json:"last_check,omitempty"`
}

// HealthStatus represents overall system health.
type HealthStatus struct {
	Status    Status                     `json:"status"`
	Timestamp time.Time                  `json:"timestamp"`
	Duration  time.Duration              `json:"duration"`
	Details   map[string]ComponentHealth `json:"details,omitempty"`
}

// HealthCheck defines a health check interface.
type HealthCheck interface {
	Name() string
	Check(context.Context) ComponentHealth
	Critical() bool
}
```

---

### Health Check Manager

```go
package healthcheck

import (
	"context"
	"sync"
	"time"
)

// Manager manages health checks.
type Manager struct {
	mu         sync.RWMutex
	checks     []HealthCheck
	lastStatus *HealthStatus
	interval   time.Duration
	stopCh     chan struct{}
}

// NewManager creates a health check manager.
func NewManager() *Manager {
	return &Manager{
		checks: make([]HealthCheck, 0),
		stopCh: make(chan struct{}),
	}
}

// Register adds a health check.
func (m *Manager) Register(check HealthCheck) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checks = append(m.checks, check)
}

// GetHealth returns current health status.
func (m *Manager) GetHealth(ctx context.Context) HealthStatus {
	start := time.Now()
	details := make(map[string]ComponentHealth)
	overallStatus := StatusHealthy

	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, check := range m.checks {
		wg.Go(func() {
			checkStart := time.Now()
			result := check.Check(ctx)
			result.Duration = time.Since(checkStart)
			result.LastCheck = time.Now()

			mu.Lock()
			defer mu.Unlock()

			details[check.Name()] = result

			// Update overall status
			if result.Status == StatusUnhealthy && check.Critical() {
				overallStatus = StatusUnhealthy
			} else if result.Status == StatusDegraded && overallStatus != StatusUnhealthy {
				overallStatus = StatusDegraded
			}
		})
	}

	wg.Wait()

	status := HealthStatus{
		Status:    overallStatus,
		Timestamp: time.Now(),
		Duration:  time.Since(start),
		Details:   details,
	}

	m.mu.Lock()
	m.lastStatus = &status
	m.mu.Unlock()

	return status
}

// StartPeriodicCheck starts periodic health checking.
func (m *Manager) StartPeriodicCheck(ctx context.Context, interval time.Duration) {
	m.interval = interval
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.GetHealth(ctx)
		case <-m.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

// Stop stops periodic health checking.
func (m *Manager) Stop() {
	close(m.stopCh)
}
```

---

### Specific Health Checks

```go
package healthcheck

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"time"
)

// DatabaseHealthCheck checks database connectivity.
type DatabaseHealthCheck struct {
	db       *sql.DB
	critical bool
}

// NewDatabaseHealthCheck creates a database health check.
func NewDatabaseHealthCheck(db *sql.DB) *DatabaseHealthCheck {
	return &DatabaseHealthCheck{
		db:       db,
		critical: true,
	}
}

func (d *DatabaseHealthCheck) Name() string {
	return "database"
}

func (d *DatabaseHealthCheck) Critical() bool {
	return d.critical
}

func (d *DatabaseHealthCheck) Check(ctx context.Context) ComponentHealth {
	if err := d.db.PingContext(ctx); err != nil {
		return ComponentHealth{
			Status:  StatusUnhealthy,
			Message: fmt.Sprintf("database connection failed: %v", err),
		}
	}
	return ComponentHealth{Status: StatusHealthy}
}

// RedisHealthCheck checks Redis connectivity.
type RedisHealthCheck struct {
	client   RedisClient
	critical bool
}

type RedisClient interface {
	Ping(context.Context) error
}

// NewRedisHealthCheck creates a Redis health check.
func NewRedisHealthCheck(client RedisClient) *RedisHealthCheck {
	return &RedisHealthCheck{
		client:   client,
		critical: false, // Non-critical, fallback possible
	}
}

func (r *RedisHealthCheck) Name() string {
	return "redis"
}

func (r *RedisHealthCheck) Critical() bool {
	return r.critical
}

func (r *RedisHealthCheck) Check(ctx context.Context) ComponentHealth {
	if err := r.client.Ping(ctx); err != nil {
		return ComponentHealth{
			Status:  StatusDegraded,
			Message: fmt.Sprintf("redis unavailable: %v", err),
		}
	}
	return ComponentHealth{Status: StatusHealthy}
}

// ExternalAPIHealthCheck checks external API availability.
type ExternalAPIHealthCheck struct {
	name     string
	url      string
	timeout  time.Duration
	critical bool
}

// NewExternalAPIHealthCheck creates an external API health check.
func NewExternalAPIHealthCheck(name, url string, timeout time.Duration) *ExternalAPIHealthCheck {
	return &ExternalAPIHealthCheck{
		name:     name,
		url:      url,
		timeout:  timeout,
		critical: false,
	}
}

func (e *ExternalAPIHealthCheck) Name() string {
	return e.name
}

func (e *ExternalAPIHealthCheck) Critical() bool {
	return e.critical
}

func (e *ExternalAPIHealthCheck) Check(ctx context.Context) ComponentHealth {
	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", e.url+"/health", nil)
	if err != nil {
		return ComponentHealth{
			Status:  StatusUnhealthy,
			Message: fmt.Sprintf("creating request: %v", err),
		}
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ComponentHealth{
			Status:  StatusUnhealthy,
			Message: err.Error(),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return ComponentHealth{Status: StatusHealthy}
	}

	return ComponentHealth{
		Status:  StatusDegraded,
		Message: fmt.Sprintf("API returned %d", resp.StatusCode),
	}
}

// DiskSpaceHealthCheck checks available disk space.
type DiskSpaceHealthCheck struct {
	path          string
	minFreeBytes  uint64
	warnFreeBytes uint64
	critical      bool
}

// NewDiskSpaceHealthCheck creates a disk space health check.
func NewDiskSpaceHealthCheck(path string, minFreeBytes, warnFreeBytes uint64) *DiskSpaceHealthCheck {
	return &DiskSpaceHealthCheck{
		path:          path,
		minFreeBytes:  minFreeBytes,
		warnFreeBytes: warnFreeBytes,
		critical:      true,
	}
}

func (d *DiskSpaceHealthCheck) Name() string {
	return "disk-space"
}

func (d *DiskSpaceHealthCheck) Critical() bool {
	return d.critical
}

func (d *DiskSpaceHealthCheck) Check(ctx context.Context) ComponentHealth {
	stats, err := getDiskStats(d.path)
	if err != nil {
		return ComponentHealth{
			Status:  StatusUnhealthy,
			Message: fmt.Sprintf("error checking disk space: %v", err),
		}
	}

	if stats.Free < d.minFreeBytes {
		return ComponentHealth{
			Status:  StatusUnhealthy,
			Message: fmt.Sprintf("only %s free (minimum: %s)", formatBytes(stats.Free), formatBytes(d.minFreeBytes)),
		}
	}

	if stats.Free < d.warnFreeBytes {
		return ComponentHealth{
			Status:  StatusDegraded,
			Message: fmt.Sprintf("low disk space: %s free", formatBytes(stats.Free)),
		}
	}

	return ComponentHealth{Status: StatusHealthy}
}

// MemoryHealthCheck checks memory usage.
type MemoryHealthCheck struct {
	maxUsagePercent float64
	critical        bool
}

// NewMemoryHealthCheck creates a memory health check.
func NewMemoryHealthCheck(maxUsagePercent float64) *MemoryHealthCheck {
	return &MemoryHealthCheck{
		maxUsagePercent: maxUsagePercent,
		critical:        false,
	}
}

func (m *MemoryHealthCheck) Name() string {
	return "memory"
}

func (m *MemoryHealthCheck) Critical() bool {
	return m.critical
}

func (m *MemoryHealthCheck) Check(ctx context.Context) ComponentHealth {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	usagePercent := float64(mem.Alloc) / float64(mem.Sys) * 100

	if usagePercent > m.maxUsagePercent {
		return ComponentHealth{
			Status:  StatusDegraded,
			Message: fmt.Sprintf("high memory usage: %.1f%%", usagePercent),
		}
	}

	return ComponentHealth{Status: StatusHealthy}
}
```

---

### HTTP Endpoints

```go
package healthcheck

import (
	"encoding/json"
	"net/http"
)

var startupComplete bool

// SetupHealthEndpoints configures health check HTTP handlers.
func SetupHealthEndpoints(mux *http.ServeMux, manager *Manager) {
	// Liveness - just check if process responds
	mux.HandleFunc("/health/live", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "alive"})
	})

	// Readiness - check dependencies
	mux.HandleFunc("/health/ready", func(w http.ResponseWriter, r *http.Request) {
		health := manager.GetHealth(r.Context())

		statusCode := http.StatusOK
		if health.Status == StatusUnhealthy {
			statusCode = http.StatusServiceUnavailable
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		json.NewEncoder(w).Encode(health)
	})

	// Startup - for slow starts
	mux.HandleFunc("/health/startup", func(w http.ResponseWriter, r *http.Request) {
		if !startupComplete {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"status": "starting"})
			return
		}

		health := manager.GetHealth(r.Context())

		statusCode := http.StatusOK
		if health.Status == StatusUnhealthy {
			statusCode = http.StatusServiceUnavailable
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		json.NewEncoder(w).Encode(health)
	})

	// Detailed health (admin only)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		health := manager.GetHealth(r.Context())

		statusCode := http.StatusOK
		if health.Status == StatusUnhealthy {
			statusCode = http.StatusServiceUnavailable
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		json.NewEncoder(w).Encode(health)
	})
}
```

---

## Kubernetes Configuration

```yaml
apiVersion: v1
kind: Pod
spec:
  containers:
    - name: app
      image: myapp:latest
      ports:
        - containerPort: 3000

      # Liveness: restart on failure
      livenessProbe:
        httpGet:
          path: /health/live
          port: 3000
        initialDelaySeconds: 10
        periodSeconds: 10
        timeoutSeconds: 5
        failureThreshold: 3

      # Readiness: remove from service on failure
      readinessProbe:
        httpGet:
          path: /health/ready
          port: 3000
        initialDelaySeconds: 5
        periodSeconds: 5
        timeoutSeconds: 3
        failureThreshold: 3

      # Startup: for slow starts
      startupProbe:
        httpGet:
          path: /health/startup
          port: 3000
        initialDelaySeconds: 0
        periodSeconds: 5
        timeoutSeconds: 3
        failureThreshold: 30  # 30 * 5s = 2.5min max startup
```

---

## Recommended Configuration

| Probe | initialDelay | period | timeout | failureThreshold |
|-------|--------------|--------|---------|------------------|
| Liveness | 10-30s | 10-15s | 5s | 3 |
| Readiness | 5-10s | 5-10s | 3s | 3 |
| Startup | 0s | 5-10s | 3s | 30 |

---

## When to Use

- Kubernetes orchestration
- Load balancers (AWS ALB, nginx)
- Service mesh (Istio, Linkerd)
- Monitoring and alerting
- Auto-scaling decisions

---

## Best Practices

| Practice | Reason |
|----------|--------|
| Liveness = simple | Avoid false positives |
| Readiness = dependencies | Verify true availability |
| Startup for slow init | Avoid premature kill |
| Cache the checks | Performance |
| Timeout < period | Avoid accumulation |

---

## Related Patterns

| Pattern | Relation |
|---------|----------|
| [Circuit Breaker](circuit-breaker.md) | Health influences the circuit |
| [Retry](retry.md) | Retryable health checks |
| Watchdog | Complementary |
| Self-healing | Recovery foundation |

---

## Sources

- [Kubernetes - Configure Probes](https://kubernetes.io/docs/tasks/configure-pod-container/configure-liveness-readiness-startup-probes/)
- [Microsoft - Health Endpoint Monitoring](https://learn.microsoft.com/en-us/azure/architecture/patterns/health-endpoint-monitoring)
- [Google SRE - Monitoring Distributed Systems](https://sre.google/sre-book/monitoring-distributed-systems/)
