# Cloud Design Patterns

> Patterns for distributed systems, resilience, and cloud scalability.

## Overview

```
                        CLOUD PATTERNS
                              |
      +-----------+-----------+-----------+-----------+
      |           |           |           |           |
  Resilience   Data       Messaging   Security   Migration
      |           |           |           |           |
  Circuit     Cache       Priority    Valet      Strangler
  Breaker     Aside       Queue       Key        Fig
      |           |           |           |           |
  Retry       Sharding    Queue       Static     CQRS
              |           Load        Content
          Materialized   Leveling    Hosting
          View
              |
          Leader
          Election
```

## Decision Table

| Problem | Pattern | File |
|---------|---------|------|
| Cascading failures | Circuit Breaker | [circuit-breaker.md](circuit-breaker.md) |
| Distributed transactions | Saga | [saga.md](saga.md) |
| DB read latency | Cache-Aside | [cache-aside.md](cache-aside.md) |
| Large data volumes | Sharding | [sharding.md](sharding.md) |
| Distributed coordination | Leader Election | [leader-election.md](leader-election.md) |
| Slow complex queries | Materialized View | [materialized-view.md](materialized-view.md) |
| Processing by importance | Priority Queue | [priority-queue.md](priority-queue.md) |
| Traffic spikes | Queue Load Leveling | [queue-load-leveling.md](queue-load-leveling.md) |
| Secure temporary access | Valet Key | [valet-key.md](valet-key.md) |
| Static assets | Static Content Hosting | [static-content-hosting.md](static-content-hosting.md) |
| Progressive migration | Strangler Fig | [strangler-fig.md](strangler-fig.md) |
| Cross-cutting concerns | Ambassador | [ambassador.md](ambassador.md) |
| Large payloads | Claim Check | [claim-check.md](claim-check.md) |
| Transaction cancellation | Compensating Transaction | [compensating-transaction.md](compensating-transaction.md) |
| Resource optimization | Compute Resource Consolidation | [compute-resource-consolidation.md](compute-resource-consolidation.md) |
| External configuration | External Configuration | [external-configuration.md](external-configuration.md) |
| Multiple backend calls | Gateway Aggregation | [gateway-aggregation.md](gateway-aggregation.md) |
| Gateway offloading | Gateway Offloading | [gateway-offloading.md](gateway-offloading.md) |
| Intelligent routing | Gateway Routing | [gateway-routing.md](gateway-routing.md) |
| Geographic deployment | Geode | [geode.md](geode.md) |
| Distributed actions | Scheduler Agent Supervisor | [scheduler-agent-supervisor.md](scheduler-agent-supervisor.md) |

## Categories

### Resilience and Stability

| Pattern | Description | Use Case |
|---------|-------------|----------|
| **Circuit Breaker** | Cuts calls to failing services | Unstable external API |
| **Saga** | Distributed transactions with compensation | Multi-service e-commerce |
| **Retry** | Retries transient operations | Temporary network errors |
| **Bulkhead** | Isolates resources by domain | Prevent contamination |
| **Compensating Transaction** | Cancels distributed operations | Multi-service rollback |
| **Scheduler Agent Supervisor** | Coordinates distributed actions | Complex workflows |

### Data Management

| Pattern | Description | Use Case |
|---------|-------------|----------|
| **Cache-Aside** | On-demand cache with TTL | Read-intensive DB |
| **Sharding** | Horizontal partitioning | Large data volumes |
| **Materialized View** | Pre-computed views | Analytical queries |
| **CQRS** | Read/write separation | Complex domains |
| **Event Sourcing** | Event history | Audit, replay |
| **Claim Check** | Separates message from payload | Large messages |
| **External Configuration** | Externalized configuration | Multi-environments |

### Messaging and Queues

| Pattern | Description | Use Case |
|---------|-------------|----------|
| **Priority Queue** | Processing by priority | Differentiated SLAs |
| **Queue Load Leveling** | Load smoothing | Predictable spikes |
| **Competing Consumers** | Processing parallelization | Horizontal scalability |

### Gateway Patterns

| Pattern | Description | Use Case |
|---------|-------------|----------|
| **Ambassador** | Cross-cutting sidecar proxy | Logging, retry, circuit breaking |
| **Gateway Aggregation** | Aggregates backend requests | Reduce client latency |
| **Gateway Offloading** | Offloads shared functions | SSL, auth, compression |
| **Gateway Routing** | Routes to backends | Microservices facade |

### Infrastructure and Scalability

| Pattern | Description | Use Case |
|---------|-------------|----------|
| **Compute Resource Consolidation** | Optimizes resource utilization | Cloud cost reduction |
| **Geode** | Geographic deployment | Global latency |
| **Leader Election** | Cluster coordination | Single master instance |

### Security and Access

| Pattern | Description | Use Case |
|---------|-------------|----------|
| **Valet Key** | Temporary tokens | Direct upload to S3/Blob |
| **Gatekeeper** | Perimeter validation | API Gateway |
| **Federated Identity** | External SSO | OAuth/OIDC |

### Deployment and Migration

| Pattern | Description | Use Case |
|---------|-------------|----------|
| **Strangler Fig** | Incremental migration | Monolith to microservices |
| **Static Content Hosting** | CDN for assets | Frontend performance |
| **Sidecar** | Auxiliary component | Logging, proxy |

## Common Combinations

```
Resilient API:
  Circuit Breaker + Retry + Cache-Aside + Bulkhead

E-commerce:
  Saga + Event Sourcing + CQRS + Priority Queue

Legacy migration:
  Strangler Fig + Anti-Corruption Layer + CQRS

High availability:
  Leader Election + Sharding + Materialized View
```

## Decision Tree

```
What is your main problem?
|
+-- Read performance? --> Cache-Aside or Materialized View
|
+-- Data volume? --> Sharding
|
+-- Unstable service? --> Circuit Breaker + Retry
|
+-- Multi-service transactions? --> Saga
|
+-- Traffic spikes? --> Queue Load Leveling
|
+-- Legacy migration? --> Strangler Fig
|
+-- Direct file access? --> Valet Key + Static Content Hosting
|
+-- Cluster coordination? --> Leader Election
```

## Reference Sources

- [Azure Architecture Patterns](https://learn.microsoft.com/en-us/azure/architecture/patterns/)
- [AWS Architecture Patterns](https://docs.aws.amazon.com/prescriptive-guidance/latest/cloud-design-patterns/)
- [GCP Architecture Patterns](https://cloud.google.com/architecture)
- [Martin Fowler - Patterns of EAA](https://martinfowler.com/eaaCatalog/)
- [microservices.io](https://microservices.io/patterns/)
