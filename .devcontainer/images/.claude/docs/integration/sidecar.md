# Sidecar Pattern

> Deploy auxiliary components in a separate container to provide cross-cutting features.

---

## Principle

```
┌─────────────────────────────────────────────────────────────────┐
│                       SIDECAR PATTERN                            │
│                                                                  │
│                        Pod / Host                                │
│  ┌───────────────────────────────────────────────────────────┐  │
│  │                                                           │  │
│  │  ┌─────────────────┐         ┌─────────────────┐         │  │
│  │  │                 │         │                 │         │  │
│  │  │   Application   │◄───────►│    Sidecar      │         │  │
│  │  │   Container     │  IPC    │    Container    │         │  │
│  │  │                 │  Volume │                 │         │  │
│  │  │  - Business     │         │  - Logging      │         │  │
│  │  │    Logic        │         │  - Proxy        │         │  │
│  │  │                 │         │  - Monitoring   │         │  │
│  │  │                 │         │  - Security     │         │  │
│  │  └─────────────────┘         └─────────────────┘         │  │
│  │                                                           │  │
│  └───────────────────────────────────────────────────────────┘  │
│                                                                  │
│  Advantages:                                                     │
│  - Separation of responsibilities                                │
│  - Cross-language reuse                                          │
│  - Independent lifecycle                                         │
│  - Failure isolation                                             │
└─────────────────────────────────────────────────────────────────┘
```

---

## Common Use Cases

| Sidecar | Function |
|---------|----------|
| **Proxy** | Envoy, nginx - routing, TLS, retry |
| **Logging** | Fluentd, Filebeat - log collection |
| **Monitoring** | Prometheus exporter, Datadog agent |
| **Security** | Vault agent, OAuth proxy |
| **Config** | Consul agent, config reloader |
| **Service Mesh** | Istio-proxy, Linkerd-proxy |

---

## Kubernetes Implementation

### Logging Sidecar

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: app-with-logging
spec:
  containers:
    # Main application
    - name: app
      image: my-app:latest
      ports:
        - containerPort: 8080
      volumeMounts:
        - name: logs
          mountPath: /var/log/app

    # Logging sidecar
    - name: log-collector
      image: fluent/fluentd:latest
      volumeMounts:
        - name: logs
          mountPath: /var/log/app
          readOnly: true
        - name: fluentd-config
          mountPath: /fluentd/etc
      resources:
        limits:
          memory: 128Mi
          cpu: 100m

  volumes:
    - name: logs
      emptyDir: {}
    - name: fluentd-config
      configMap:
        name: fluentd-config
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: fluentd-config
data:
  fluent.conf: |
    <source>
      @type tail
      path /var/log/app/*.log
      pos_file /var/log/app/app.log.pos
      tag app.logs
      <parse>
        @type json
      </parse>
    </source>
    <match app.**>
      @type elasticsearch
      host elasticsearch
      port 9200
      index_name app-logs
    </match>
```

---

### Proxy Sidecar (Envoy)

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: app-with-proxy
spec:
  containers:
    # Application (unaware of external network)
    - name: app
      image: my-app:latest
      ports:
        - containerPort: 8080
      env:
        - name: UPSTREAM_URL
          value: "http://localhost:9001"  # Talks to the sidecar

    # Envoy Sidecar
    - name: envoy
      image: envoyproxy/envoy:v1.28.0
      ports:
        - containerPort: 9001  # Inbound
        - containerPort: 9901  # Admin
      volumeMounts:
        - name: envoy-config
          mountPath: /etc/envoy
      args:
        - -c
        - /etc/envoy/envoy.yaml

  volumes:
    - name: envoy-config
      configMap:
        name: envoy-config
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: envoy-config
data:
  envoy.yaml: |
    static_resources:
      listeners:
        - name: inbound
          address:
            socket_address:
              address: 0.0.0.0
              port_value: 9001
          filter_chains:
            - filters:
                - name: envoy.filters.network.http_connection_manager
                  typed_config:
                    "@type": type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager
                    stat_prefix: ingress_http
                    route_config:
                      virtual_hosts:
                        - name: backend
                          domains: ["*"]
                          routes:
                            - match:
                                prefix: "/"
                              route:
                                cluster: upstream
                                timeout: 30s
                                retry_policy:
                                  retry_on: 5xx
                                  num_retries: 3
                    http_filters:
                      - name: envoy.filters.http.router

      clusters:
        - name: upstream
          type: STRICT_DNS
          lb_policy: ROUND_ROBIN
          load_assignment:
            cluster_name: upstream
            endpoints:
              - lb_endpoints:
                  - endpoint:
                      address:
                        socket_address:
                          address: backend-service
                          port_value: 8080
```

---

### Vault Agent Sidecar (Secrets)

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: app-with-vault
  annotations:
    vault.hashicorp.com/agent-inject: "true"
    vault.hashicorp.com/role: "my-app"
    vault.hashicorp.com/agent-inject-secret-db-creds: "database/creds/my-role"
    vault.hashicorp.com/agent-inject-template-db-creds: |
      {{- with secret "database/creds/my-role" -}}
      export DB_USER="{{ .Data.username }}"
      export DB_PASSWORD="{{ .Data.password }}"
      {{- end -}}
spec:
  serviceAccountName: my-app
  containers:
    - name: app
      image: my-app:latest
      command: ["/bin/sh", "-c"]
      args:
        - source /vault/secrets/db-creds && ./app
```

---

## Go Implementation

### Local Sidecar for Development

```go
package sidecar

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"time"
)

// SidecarConfig defines sidecar proxy configuration.
type SidecarConfig struct {
	ListenPort   int
	UpstreamHost string
	UpstreamPort int
	Features     Features
}

// Features defines enabled sidecar features.
type Features struct {
	Logging   bool
	Metrics   bool
	Retry     bool
	RateLimit bool
}

// LocalSidecar implements a local development sidecar proxy.
type LocalSidecar struct {
	config  SidecarConfig
	proxy   *httputil.ReverseProxy
	metrics *Metrics
	logger  *slog.Logger
}

// Metrics holds sidecar metrics.
type Metrics struct {
	mu        sync.RWMutex
	requests  int64
	errors    int64
	latencies []int64
}

// NewLocalSidecar creates a new local sidecar proxy.
func NewLocalSidecar(config SidecarConfig, logger *slog.Logger) *LocalSidecar {
	if logger == nil {
		logger = slog.Default()
	}

	target := &url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("%s:%d", config.UpstreamHost, config.UpstreamPort),
	}

	return &LocalSidecar{
		config:  config,
		proxy:   httputil.NewSingleHostReverseProxy(target),
		metrics: &Metrics{latencies: make([]int64, 0, 1000)},
		logger:  logger,
	}
}

// Start starts the sidecar proxy server.
func (s *LocalSidecar) Start(ctx context.Context) error {
	mux := http.NewServeMux()

	// Metrics endpoint
	mux.HandleFunc("/sidecar/metrics", s.handleMetrics)

	// Proxy all other requests
	mux.HandleFunc("/", s.handleProxy)

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", s.config.ListenPort),
		Handler: mux,
	}

	s.logger.Info("sidecar listening", "port", s.config.ListenPort)

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(shutdownCtx)
	}()

	return server.ListenAndServe()
}

func (s *LocalSidecar) handleProxy(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	s.metrics.mu.Lock()
	s.metrics.requests++
	s.metrics.mu.Unlock()

	// Rate limiting
	if s.config.Features.RateLimit && !s.checkRateLimit(r) {
		http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
		return
	}

	// Logging
	if s.config.Features.Logging {
		s.logger.Info("request",
			"method", r.Method,
			"url", r.URL.String(),
			"remote_addr", r.RemoteAddr,
		)
	}

	// Retry logic
	maxRetries := 1
	if s.config.Features.Retry {
		maxRetries = 3
	}

	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		if attempt > 1 {
			time.Sleep(time.Duration(100*attempt) * time.Millisecond)
		}

		if err := s.proxyRequest(w, r); err == nil {
			duration := time.Since(start).Milliseconds()
			s.metrics.mu.Lock()
			s.metrics.latencies = append(s.metrics.latencies, duration)
			s.metrics.mu.Unlock()
			return
		} else {
			lastErr = err
		}
	}

	s.metrics.mu.Lock()
	s.metrics.errors++
	s.metrics.mu.Unlock()

	s.logger.Error("proxy failed", "error", lastErr, "attempts", maxRetries)
	http.Error(w, "Bad Gateway", http.StatusBadGateway)
}

func (s *LocalSidecar) proxyRequest(w http.ResponseWriter, r *http.Request) error {
	errChan := make(chan error, 1)

	s.proxy.ModifyResponse = func(resp *http.Response) error {
		if resp.StatusCode >= 500 {
			errChan <- fmt.Errorf("upstream error: %d", resp.StatusCode)
			return nil
		}
		errChan <- nil
		return nil
	}

	s.proxy.ServeHTTP(w, r)

	select {
	case err := <-errChan:
		return err
	case <-time.After(30 * time.Second):
		return fmt.Errorf("timeout")
	}
}

type rateLimitBucket struct {
	mu         sync.Mutex
	timestamps map[string][]int64
}

var globalRateLimiter = &rateLimitBucket{
	timestamps: make(map[string][]int64),
}

func (s *LocalSidecar) checkRateLimit(r *http.Request) bool {
	key := r.RemoteAddr
	now := time.Now().UnixMilli()
	windowMs := int64(60000)
	maxRequests := 100

	globalRateLimiter.mu.Lock()
	defer globalRateLimiter.mu.Unlock()

	timestamps := globalRateLimiter.timestamps[key]
	windowStart := now - windowMs

	// Remove expired timestamps
	valid := make([]int64, 0, len(timestamps))
	for _, ts := range timestamps {
		if ts > windowStart {
			valid = append(valid, ts)
		}
	}

	if len(valid) >= maxRequests {
		return false
	}

	valid = append(valid, now)
	globalRateLimiter.timestamps[key] = valid

	return true
}

func (s *LocalSidecar) handleMetrics(w http.ResponseWriter, r *http.Request) {
	s.metrics.mu.RLock()
	defer s.metrics.mu.RUnlock()

	var avgLatency int64
	var p99Latency int64

	if len(s.metrics.latencies) > 0 {
		var sum int64
		for _, lat := range s.metrics.latencies {
			sum += lat
		}
		avgLatency = sum / int64(len(s.metrics.latencies))

		// Calculate p99
		sorted := make([]int64, len(s.metrics.latencies))
		copy(sorted, s.metrics.latencies)
		// Simple sort (use sort.Slice in production)
		p99Index := int(float64(len(sorted)) * 0.99)
		if p99Index < len(sorted) {
			p99Latency = sorted[p99Index]
		}
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{
		"requests_total": %d,
		"errors_total": %d,
		"latency_avg_ms": %d,
		"latency_p99_ms": %d
	}`, s.metrics.requests, s.metrics.errors, avgLatency, p99Latency)
}
```

---

### Sidecar Usage

```go
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"example.com/app/sidecar"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	proxy := sidecar.NewLocalSidecar(sidecar.SidecarConfig{
		ListenPort:   9001,
		UpstreamHost: "localhost",
		UpstreamPort: 8080,
		Features: sidecar.Features{
			Logging:   true,
			Metrics:   true,
			Retry:     true,
			RateLimit: true,
		},
	}, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		if err := proxy.Start(ctx); err != nil {
			logger.Error("sidecar error", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("shutting down sidecar")
	cancel()
}
```

---

### Init Container for Config

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: app-with-init
spec:
  initContainers:
    # Init container: fetch config before app starts
    - name: config-fetcher
      image: curlimages/curl:latest
      command:
        - /bin/sh
        - -c
        - |
          curl -s http://config-service/config/my-app > /config/app.json
          echo "Config fetched successfully"
      volumeMounts:
        - name: config
          mountPath: /config

  containers:
    - name: app
      image: my-app:latest
      volumeMounts:
        - name: config
          mountPath: /app/config
          readOnly: true

    # Sidecar: refresh config periodically
    - name: config-refresher
      image: curlimages/curl:latest
      command:
        - /bin/sh
        - -c
        - |
          while true; do
            sleep 60
            curl -s http://config-service/config/my-app > /config/app.json
            # Signal app to reload (optional)
            curl -X POST http://localhost:8080/reload
          done
      volumeMounts:
        - name: config
          mountPath: /config

  volumes:
    - name: config
      emptyDir: {}
```

---

## Comparison with Alternatives

| Approach | Advantages | Disadvantages |
|----------|-----------|---------------|
| **Sidecar** | Isolation, polyglot | Resource overhead |
| **Library** | Performance, simplicity | Coupling, single-language |
| **DaemonSet** | Fewer resources | Less isolated |
| **Service Mesh** | Full-featured | Complexity |

---

## When to Use

- Cross-cutting features (logging, security)
- Polyglot team (Java, Node, Go, Python)
- Need for isolation (failure domains)
- Dynamic configuration
- Proxy and networking

---

## When NOT to Use

- Simple monolithic application
- Strict resource constraints
- Critical latency (<1ms)
- Unjustified complexity

---

## Related Patterns

| Pattern | Relation |
|---------|----------|
| [Service Mesh](service-mesh.md) | Uses sidecars |
| Ambassador | Sidecar variant |
| Adapter | Translation sidecar |
| Init Container | Initialization before app |

---

## Sources

- [Microsoft - Sidecar Pattern](https://learn.microsoft.com/en-us/azure/architecture/patterns/sidecar)
- [Kubernetes - Multi-container Pods](https://kubernetes.io/docs/concepts/workloads/pods/#how-pods-manage-multiple-containers)
- [Envoy Proxy](https://www.envoyproxy.io/docs/envoy/latest/)
