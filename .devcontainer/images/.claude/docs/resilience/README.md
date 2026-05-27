# Resilience Patterns

Patterns for building robust and fault-tolerant systems.

---

## Documented Patterns

| Pattern | File | Usage |
|---------|------|-------|
| Circuit Breaker | [circuit-breaker.md](circuit-breaker.md) | Prevent cascading failures |
| Retry | [retry.md](retry.md) | Retry failed operations |
| Timeout | [timeout.md](timeout.md) | Limit wait time |
| Bulkhead | [bulkhead.md](bulkhead.md) | Isolate resources |
| Rate Limiting | [rate-limiting.md](rate-limiting.md) | Control throughput |
| Health Check | [health-check.md](health-check.md) | Verify service health |

---

## Decision Table

| Problem | Pattern | When to Use |
|---------|---------|-------------|
| Unstable external service | Circuit Breaker | HTTP calls, DB, third-party APIs |
| Transient errors | Retry | Network timeouts, 503, locks |
| Infinite wait | Timeout | Any external call |
| Component overload | Bulkhead | Thread pool isolation |
| Too many requests | Rate Limiting | Public APIs, DoS protection |
| Unknown service state | Health Check | Kubernetes, load balancers |

---

## Recommended Combination

```
Request → Rate Limiter → Timeout → Circuit Breaker → Retry → Service
           (1)            (2)          (3)            (4)
```

### Application Order

1. **Rate Limiter**: Reject excess before any processing
2. **Timeout**: Limit the total operation time
3. **Circuit Breaker**: Fail-fast if service is failing
4. **Retry**: Retry transient errors

---

## Technology Stack

| Language | Recommended Library |
|----------|---------------------|
| Node.js | `cockatiel`, `opossum` |
| Java | Resilience4j |
| Go | `sony/gobreaker`, `avast/retry-go` |
| Python | `tenacity`, `pybreaker` |
| .NET | Polly |

---

## Key Metrics

| Pattern | Metrics to Monitor |
|---------|--------------------|
| Circuit Breaker | open_count, state_changes, rejection_rate |
| Retry | retry_count, final_success_rate, avg_attempts |
| Timeout | timeout_count, p99_latency |
| Bulkhead | queue_size, rejection_count, active_threads |
| Rate Limiting | accepted_rate, rejected_rate, current_tokens |
| Health Check | probe_latency, failure_count, uptime |

---

## Anti-patterns

| Anti-pattern | Problem | Solution |
|--------------|---------|----------|
| Retry without backoff | Overloads the service | Exponential backoff + jitter |
| Timeout too long | Resource exhaustion | Adapt to SLA |
| Circuit never opens | Threshold too high | Calibrate on real metrics |
| No fallback | Error propagated | Graceful degradation |
| Superficial health check | False positives | Deep health check |

---

## Sources

- [Microsoft - Resiliency patterns](https://learn.microsoft.com/en-us/azure/architecture/patterns/category/resiliency)
- [Netflix - Fault Tolerance](https://netflixtechblog.com/fault-tolerance-in-a-high-volume-distributed-system-91ab4faae74a)
- [Release It! - Michael Nygard](https://pragprog.com/titles/mnee2/release-it-second-edition/)
