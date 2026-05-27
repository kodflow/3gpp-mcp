# Integration Patterns

Patterns for integrating heterogeneous systems and managing application boundaries.

---

## Documented Patterns

| Pattern | File | Usage |
|---------|------|-------|
| API Gateway | [api-gateway.md](api-gateway.md) | Single entry point for APIs |
| Backend for Frontend | [bff.md](bff.md) | Dedicated API per client type |
| Anti-Corruption Layer | [anti-corruption-layer.md](anti-corruption-layer.md) | Isolate legacy systems |
| Service Mesh | [service-mesh.md](service-mesh.md) | Inter-service communication |
| Sidecar | [sidecar.md](sidecar.md) | Cross-cutting features |

---

## Decision Table

| Problem | Pattern | When to Use |
|---------|---------|-------------|
| Multiple exposed microservices | API Gateway | Unified facade for clients |
| Heterogeneous clients (web, mobile) | BFF | Platform-specific needs |
| Legacy system integration | Anti-Corruption Layer | Protect the new domain |
| Observability, security, retry | Service Mesh | Infrastructure as code |
| Cross-cutting feature | Sidecar | Logging, proxy, monitoring |

---

## Relationship Between Patterns

```
┌─────────────────────────────────────────────────────────────────┐
│                   INTEGRATION ARCHITECTURE                       │
│                                                                  │
│  Clients                                                         │
│  ┌─────┐  ┌─────┐  ┌─────┐                                      │
│  │ Web │  │ iOS │  │ IoT │                                      │
│  └──┬──┘  └──┬──┘  └──┬──┘                                      │
│     │        │        │                                          │
│     ▼        ▼        ▼                                          │
│  ┌─────┐  ┌─────┐  ┌─────┐         Backend for Frontend         │
│  │BFF-W│  │BFF-M│  │BFF-I│                                      │
│  └──┬──┘  └──┬──┘  └──┬──┘                                      │
│     │        │        │                                          │
│     └────────┼────────┘                                          │
│              │                                                   │
│              ▼                                                   │
│       ┌─────────────┐                  API Gateway               │
│       │ API Gateway │                                            │
│       └──────┬──────┘                                            │
│              │                                                   │
│     ┌────────┼────────┐                                          │
│     ▼        ▼        ▼                                          │
│  ┌─────┐  ┌─────┐  ┌─────┐         Service Mesh (Sidecar)       │
│  │ Svc │  │ Svc │  │ ACL │─────► Legacy System                  │
│  │  A  │  │  B  │  │     │       Anti-Corruption Layer          │
│  └─────┘  └─────┘  └─────┘                                      │
└─────────────────────────────────────────────────────────────────┘
```

---

## Common Combinations

| Scenario | Patterns | Justification |
|----------|----------|---------------|
| Multi-tenant SaaS | API Gateway + BFF | Unification + customization |
| Legacy migration | ACL + Strangler Fig | Isolation + progressive replacement |
| Microservices | Service Mesh + Sidecar | Observability + resilience |
| Mobile-first | BFF + API Gateway | Network optimization |

---

## Technologies

| Pattern | Technologies |
|---------|-------------|
| API Gateway | Kong, AWS API Gateway, Apigee, Traefik |
| BFF | Express, NestJS, GraphQL Federation |
| ACL | Adapter pattern, Facade, Translation |
| Service Mesh | Istio, Linkerd, Consul Connect |
| Sidecar | Envoy, Dapr, Ambassador |

---

## Key Metrics

| Pattern | Metrics |
|---------|---------|
| API Gateway | Latency, error rate, requests/sec, auth failures |
| BFF | Response size, cache hit rate, aggregation time |
| ACL | Translation errors, legacy calls, sync lag |
| Service Mesh | mTLS coverage, retry rate, circuit state |
| Sidecar | Resource usage, proxy latency |

---

## Anti-patterns

| Anti-pattern | Problem | Solution |
|--------------|---------|----------|
| Monolithic gateway | SPOF, bottleneck | Multiple specialized gateways |
| Generic BFF | Loses the purpose of BFF | One BFF per client type |
| Too fine-grained ACL | Excessive complexity | Group by bounded context |
| Mesh overhead | Added latency | Evaluate the actual need |
| Too heavy sidecar | Resource consumption | Optimize or consolidate |

---

## Sources

- [Microsoft - API Gateway Pattern](https://learn.microsoft.com/en-us/azure/architecture/microservices/design/gateway)
- [Sam Newman - Building Microservices](https://samnewman.io/books/building_microservices_2nd_edition/)
- [Martin Fowler - BFF](https://samnewman.io/patterns/architectural/bff/)
- [DDD - Anti-Corruption Layer](https://docs.microsoft.com/en-us/azure/architecture/patterns/anti-corruption-layer)
