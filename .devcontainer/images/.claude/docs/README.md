# Design Patterns - Complete Reference

Exhaustive database of design patterns for Claude agents.

---

## Categories

| # | Category | Files | Description |
|---|----------|-------|-------------|
| 1 | [principles/](principles/) | 7 | SOLID, DRY, KISS, YAGNI, GRASP, Defensive |
| 2 | [creational/](creational/) | 4 | Factory, Builder, Singleton, Prototype |
| 3 | [structural/](structural/) | 7 | Adapter, Bridge, Composite, Decorator, Facade, Flyweight, Proxy |
| 4 | [behavioral/](behavioral/) | 11 | Observer, Strategy, Command, State, Chain, Iterator, Mediator, Memento, Template, Visitor, Interpreter |
| 5 | [performance/](performance/) | 9 | Pool, Cache, Lazy, Memoization, Buffer |
| 6 | [concurrency/](concurrency/) | 9 | Thread Pool, Actor, Mutex, Pipeline, COW |
| 7 | [architectural/](architectural/) | 10 | Hexagonal, Microservices, CQRS, Event Sourcing |
| 8 | [enterprise/](enterprise/) | 13 | PoEAA - Transaction Script, Domain Model, DTO |
| 9 | [messaging/](messaging/) | 11 | EIP - Pipes, Router, Aggregator, Outbox |
| 10 | [ddd/](ddd/) | 9 | Entity, Value Object, Aggregate, Repository |
| 11 | [cloud/](cloud/) | 22 | Circuit Breaker, Saga, Sharding, Cache-Aside |
| 12 | [resilience/](resilience/) | 7 | Retry, Timeout, Bulkhead, Rate Limiting |
| 13 | [security/](security/) | 9 | OAuth, JWT, RBAC, ABAC, Secrets |
| 14 | [functional/](functional/) | 6 | Monad, Either, Option, Lens, Composition |
| 15 | [devops/](devops/) | 9 | GitOps, IaC, Blue-Green, Canary, Feature Toggles |
| 16 | [testing/](testing/) | 9 | Mock, Stub, Fixture, Property-Based, Contracts |
| 17 | [refactoring/](refactoring/) | 2 | Branch by Abstraction, Strangler Fig |
| 18 | [integration/](integration/) | 6 | API Gateway, BFF, Service Mesh, Sidecar |

Total: 167 markdown files - 300+ documented patterns

---

## Learned Patterns

Operational guardrails extracted from real sessions — propagated to all
consumers via `/update`. Always shipped (even in `install.sh --minimal`)
because they protect users from concrete past failures.

| Pattern | Description | File |
|---------|-------------|------|
| agent-git-stash-destruction | Forbid `git stash/reset/checkout` in agent prompts (cost: 6h of ktn-linter work) | [learned/agent-git-stash-destruction.md](learned/agent-git-stash-destruction.md) |
| super-claude-auto-mode-fallback | Prefer `--permission-mode auto` over broken bypass on Claude Code v2.1.113+ | [learned/super-claude-auto-mode-fallback.md](learned/super-claude-auto-mode-fallback.md) |

---

## Quick Lookup by Problem

| I need to... | Patterns | Category |
|-------------|----------|----------|
| Create objects flexibly | Factory, Builder, Prototype | creational/ |
| Add behavior without subclassing | Decorator, Strategy, Visitor | structural/, behavioral/ |
| Decouple components | Observer, Mediator, Event Bus | behavioral/, messaging/ |
| Handle distributed transactions | Saga, Two-Phase Commit, Outbox | cloud/, messaging/ |
| Survive service failures | Circuit Breaker, Retry, Bulkhead, Timeout | resilience/ |
| Scale database reads | CQRS, Cache-Aside, Read Replica | cloud/, architectural/ |
| Manage complex state | State Machine, Memento, Event Sourcing | behavioral/, architectural/ |
| Process messages reliably | Pipes & Filters, Dead Letter, Outbox | messaging/ |
| Secure API access | OAuth, JWT, RBAC, ABAC | security/ |
| Deploy without downtime | Blue-Green, Canary, Feature Toggles | devops/ |
| Test isolated components | Mock, Stub, Fixture, Contract Testing | testing/ |
| Improve code readability | Extract Method, Rename, Inline | refactoring/ |
| Model business domains | Entity, Value Object, Aggregate, Repository | ddd/ |
| Handle concurrent access | Mutex, Actor, Thread Pool, COW | concurrency/ |
| Optimize performance | Cache, Pool, Lazy Load, Memoization | performance/ |
| Migrate legacy systems | Strangler Fig, Anti-Corruption Layer, BFF | refactoring/, integration/ |
| Compose functions cleanly | Monad, Either, Lens, Pipe | functional/ |

---

## Complete Alphabetical Index

### A

| Pattern | Category | Usage |
|---------|----------|-------|
| Abstract Factory | creational | Related object families |
| Active Record | enterprise | Object = DB row |
| Actor Model | concurrency | Message-based concurrency |
| Adapter | structural | Convert interfaces |
| Aggregator | messaging | Combine messages |
| Aggregate | ddd | Entity cluster |
| Ambassador | cloud | Proxy helper services |
| Anti-Corruption Layer | integration | Isolate legacy |
| API Gateway | integration | Single entry point |
| Application Controller | enterprise | UI workflow |
| Applicative Functor | functional | Effect sequencing |
| Async/Await | concurrency | Simplified async |

### B

| Pattern | Category | Usage |
|---------|----------|-------|
| Backend for Frontend (BFF) | integration | Per-client backend |
| Barrier | concurrency | Thread synchronization |
| Blue-Green Deployment | devops | Zero-downtime deploy |
| Branch by Abstraction | refactoring | Progressive migration |
| Bridge | structural | Abstraction/implementation |
| Buffer | performance | Temporary storage |
| Builder | creational | Complex construction |
| Bulkhead | resilience | Resource isolation |

### C

| Pattern | Category | Usage |
|---------|----------|-------|
| Cache-Aside | cloud | On-demand cache |
| Canary Deployment | devops | Progressive deployment |
| Chain of Responsibility | behavioral | Handler pipeline |
| Copy-on-Write | concurrency | Deferred write copy |
| Choreography | architectural | Decentralized orchestration |
| Circuit Breaker | resilience | Prevent cascade failures |
| Claim Check | messaging | Message + payload reference |
| Class Table Inheritance | enterprise | Inheritance = tables |
| Client Session State | enterprise | Client-side state |
| Coarse-Grained Lock | concurrency | Group lock |
| Command | behavioral | Encapsulate requests |
| Command Message | messaging | Message = command |
| Competing Consumers | messaging | Parallel consumers |
| Composite | structural | Tree structures |
| Composition | functional | f(g(x)) |
| Concrete Table Inheritance | enterprise | Class = table |
| Content Enricher | messaging | Enrich message |
| Content Filter | messaging | Filter content |
| Content-Based Router | messaging | Route by content |
| Correlation Identifier | messaging | Link request/response |
| CQRS | architectural | Separate read/write |
| Currying | functional | f(a,b) → f(a)(b) |

### D

| Pattern | Category | Usage |
|---------|----------|-------|
| Data Mapper | enterprise | Object-DB mapping |
| Defensive Programming | principles | Defensive validation |
| Design by Contract | principles | Pre/post conditions |
| Data Transfer Object (DTO) | enterprise | Transfer data |
| Dead Letter Channel | messaging | Error messages |
| Debounce | performance | Delay before execution |
| Decorator | structural | Add behaviors |
| Dependency Injection | structural | Invert dependencies |
| Domain Event | ddd | Business event |
| Domain Model | enterprise | Rich business logic |
| Domain Service | ddd | Logic without entity |
| Double-Checked Locking | concurrency | Thread-safe lazy singleton |
| Durable Subscriber | messaging | Persistent subscription |
| Dynamic Router | messaging | Dynamic routing |

### E

| Pattern | Category | Usage |
|---------|----------|-------|
| Embedded Value | enterprise | Value Object as column |
| Entity | ddd | Object with identity |
| Envelope Wrapper | messaging | Wrap message |
| Event Message | messaging | Event notification |
| Event Sourcing | architectural | Event history |
| Event-Driven Architecture | architectural | Event-driven architecture |
| Event-Driven Consumer | messaging | Event-driven consumer |
| External Configuration Store | cloud | Externalized config |

### F

| Pattern | Category | Usage |
|---------|----------|-------|
| Facade | structural | Simplified interface |
| Factory | ddd | Create aggregates |
| Factory Method | creational | Delegate creation |
| Fail-Fast | principles | Fail immediately |
| Feature Toggle | devops | Conditional activation |
| Federated Identity | security | Delegated auth |
| Fixture | testing | Test data |
| Flyweight | structural | Share common state |
| Foreign Key Mapping | enterprise | FK as reference |
| Front Controller | enterprise | Single entry point |
| Functor | functional | map() on container |
| Future/Promise | concurrency | Async value |

### G

| Pattern | Category | Usage |
|---------|----------|-------|
| Gateway | enterprise | External system access |
| GRASP (9 patterns) | principles | Responsibility assignment |
| Guard Clause | principles | Early return validation |
| Gateway Aggregation | cloud | Aggregate requests |
| Gateway Offloading | cloud | Offload gateway |
| Gateway Routing | cloud | Route requests |
| Geode | cloud | Multi-region |
| GitOps | devops | Git = source of truth |
| Guaranteed Delivery | messaging | Guaranteed delivery |

### H

| Pattern | Category | Usage |
|---------|----------|-------|
| Half-Sync/Half-Async | concurrency | Sync + Async combined |
| Health Check | resilience | Check service status |
| Hexagonal Architecture | architectural | Ports & Adapters |

### I

| Pattern | Category | Usage |
|---------|----------|-------|
| Idempotent Receiver | messaging | Single processing |
| Identity Field | enterprise | ID as attribute |
| Identity Map | enterprise | Loaded objects cache |
| Immutable Infrastructure | devops | Replaced, not modified infra |
| Implicit Lock | concurrency | Automatic lock |
| Infrastructure as Code | devops | Infra as code |
| Inheritance Mappers | enterprise | DB inheritance strategies |
| Integration Patterns | enterprise | Integration patterns |
| Interpreter | behavioral | Interpret grammar |
| Invalid Message Channel | messaging | Invalid messages |
| Iterator | behavioral | Collection traversal |

### J-K

| Pattern | Category | Usage |
|---------|----------|-------|
| JWT | security | Self-contained token |

### L

| Pattern | Category | Usage |
|---------|----------|-------|
| Layer Supertype | enterprise | Layer base class |
| Layered Architecture | architectural | Layered architecture |
| Lazy Load | performance | Deferred loading |
| Leader Election | cloud | Elect coordinator |
| Lock | concurrency | Mutual exclusion |

### M

| Pattern | Category | Usage |
|---------|----------|-------|
| Mapper | enterprise | Object conversion |
| Materialized View | cloud | Pre-computed view |
| Mediator | behavioral | Reduce coupling |
| Memento | behavioral | Save state |
| Memoization | performance | Function result cache |
| Message | messaging | Communication unit |
| Message Broker | messaging | Message intermediary |
| Message Bus | messaging | Message bus |
| Message Channel | messaging | Transport channel |
| Message Dispatcher | messaging | Distribute messages |
| Message Endpoint | messaging | Connection point |
| Message Expiration | messaging | Message lifetime |
| Message Filter | messaging | Filter messages |
| Message History | messaging | Routing history |
| Message Router | messaging | Route messages |
| Message Sequence | messaging | Message ordering |
| Message Store | messaging | Store messages |
| Message Translator | messaging | Translate format |
| Messaging Bridge | messaging | Connect systems |
| Messaging Gateway | messaging | Messaging abstraction |
| Messaging Mapper | messaging | Map messages |
| Metadata Mapping | enterprise | Metadata-based mapping |
| Microservices | architectural | Independent services |
| Mock | testing | Simulate behavior |
| Model View Controller (MVC) | enterprise | UI separation |
| Module | ddd | Group concepts |
| Monad | functional | Chaining + context |
| Money | enterprise | Monetary value |
| Monitor | concurrency | Lock + condition |
| Monolith | architectural | Single application |
| Multiton | creational | Singleton pool |
| Mutex | concurrency | Exclusive lock |

### N

| Pattern | Category | Usage |
|---------|----------|-------|
| Normalizer | messaging | Standardize format |
| Null Object | behavioral | Avoid null checks |

### O

| Pattern | Category | Usage |
|---------|----------|-------|
| OAuth | security | Access delegation |
| Object Mother | testing | Test object factory |
| Object Pool | performance | Reuse objects |
| Observer | behavioral | Change notification |
| Optimistic Lock | concurrency | Detect conflicts |
| Optimistic Offline Lock | enterprise | Optimistic lock |
| Outbox | messaging | Event reliability |

### P

| Pattern | Category | Usage |
|---------|----------|-------|
| Page Controller | enterprise | Per-page controller |
| Pessimistic Lock | concurrency | Prevent conflicts |
| Pessimistic Offline Lock | enterprise | Pessimistic lock |
| Pipes and Filters | messaging | Processing pipeline |
| Plugin | enterprise | Dynamic extension |
| Point-to-Point Channel | messaging | One sender, one receiver |
| Polling Consumer | messaging | Message polling |
| Priority Queue | cloud | Priority queue |
| Process Manager | messaging | Orchestrate workflow |
| Producer-Consumer | concurrency | Inter-thread queue |
| Prototype | creational | Clone objects |
| Proxy | structural | Control access |
| Publish-Subscribe | messaging | Multiple subscribers |
| Publisher Confirms | messaging | Publication confirmation |

### Q

| Pattern | Category | Usage |
|---------|----------|-------|
| Quarantine | cloud | Isolate suspect assets |
| Query Object | enterprise | Build queries |
| Queue-Based Load Leveling | cloud | Smooth the load |

### R

| Pattern | Category | Usage |
|---------|----------|-------|
| Rate Limiting | resilience | Limit throughput |
| RBAC | security | Role-based control |
| Read-Through Cache | performance | Transparent read cache |
| Read-Write Lock | concurrency | Read/write lock |
| Recipient List | messaging | Recipient list |
| Record Set | enterprise | Row collection |
| Registry | enterprise | Global object access |
| Remote Facade | enterprise | Simplified remote API |
| Repository | ddd | Aggregate access |
| Request-Reply | messaging | Request/response |
| Resequencer | messaging | Reorder messages |
| Retry | resilience | Retry on error |
| Return Address | messaging | Return address |
| Ring Buffer | performance | Circular buffer |
| Routing Slip | messaging | Message itinerary |
| Row Data Gateway | enterprise | Per-row gateway |

### S

| Pattern | Category | Usage |
|---------|----------|-------|
| Saga | cloud | Distributed transactions |
| Scatter-Gather | messaging | Distribute and collect |
| Scheduler Agent Supervisor | cloud | Coordinate tasks |
| Selective Consumer | messaging | Filter on receipt |
| Semaphore | concurrency | Limit concurrent access |
| Separated Interface | enterprise | Separated interface |
| Serialized LOB | enterprise | Serialize objects |
| Server Session State | enterprise | Server-side state |
| Service Activator | messaging | Activate service |
| Service Layer | enterprise | Service layer |
| Service Locator | enterprise | Locate services |
| Service Stub | enterprise | Service stub |
| Service Mesh | integration | Inter-service communication |
| Sharding | cloud | Partition data |
| Sidecar | integration | Auxiliary container |
| Single Table Inheritance | enterprise | Inheritance = 1 table |
| Singleton | creational | Single instance |
| Smart Proxy | messaging | Intelligent proxy |
| Specification | ddd | Business rule |
| Splitter | messaging | Split message |
| State | behavioral | Behavior by state |
| Static Content Hosting | cloud | Cloud static content |
| Strangler Fig | cloud | Progressive migration |
| Strategy | behavioral | Variable algorithms |
| Stub | testing | Predefined response |
| Supervisor | concurrency | Handle actor errors |

### T

| Pattern | Category | Usage |
|---------|----------|-------|
| Table Data Gateway | enterprise | Per-table gateway |
| Table Module | enterprise | Per-table module |
| Template Method | behavioral | Algorithm skeleton |
| Template View | enterprise | Template-based view |
| Test Double | testing | Test replacement |
| Thread Pool | concurrency | Thread pool |
| Throttling | resilience | Limit consumption |
| Timeout | resilience | Limit duration |
| Transaction Script | enterprise | Per-transaction script |
| Transactional Client | messaging | Transactional client |
| Transactional Outbox | messaging | Transactional outbox |
| Transform View | enterprise | View transformation |
| Two Step View | enterprise | Two-step view |

### U

| Pattern | Category | Usage |
|---------|----------|-------|
| Ubiquitous Language | ddd | Common language |
| Unit of Work | enterprise | Group modifications |

### V

| Pattern | Category | Usage |
|---------|----------|-------|
| Valet Key | cloud | Temporary access |
| Value Object | ddd | Object without identity |
| Virtual Proxy | structural | Lazy load via proxy |
| Visitor | behavioral | Operations on structure |

### W

| Pattern | Category | Usage |
|---------|----------|-------|
| Wire Tap | messaging | Intercept messages |
| Write-Behind Cache | performance | Async write |
| Write-Through Cache | performance | Sync write |

---

## Patterns by Problem

### Validation / Robustness

| Problem | Patterns |
|---------|----------|
| Null/invalid variables | Guard Clause, Null Object |
| Nested conditions | Guard Clause, Early Return |
| Business invariants | Design by Contract, Assertions |
| External data | Input Validation, Type Guards |
| Missing dependencies | Fail-Fast, Dependency Validation |
| Accidental modifications | Immutability, Copy-on-Write |

### Object Creation

| Problem | Patterns |
|---------|----------|
| Complex construction | Builder |
| Object families | Abstract Factory |
| Delegate creation | Factory Method |
| Expensive reusable objects | Object Pool |
| Efficient copy | Prototype |
| Single instance | Singleton, Multiton |

### Performance

| Problem | Patterns |
|---------|----------|
| Expensive objects | Object Pool, Flyweight |
| Frequently accessed data | Cache-Aside, Memoization |
| Slow I/O | Buffer, Lazy Load |
| Repeated calls | Debounce, Throttle |

### Concurrency

| Problem | Patterns |
|---------|----------|
| Expensive threads | Thread Pool |
| Data sharing | Lock, Mutex, Semaphore |
| Inter-thread communication | Producer-Consumer, Actor |
| Simplified async | Future/Promise, Async/Await |

### Resilience

| Problem | Patterns |
|---------|----------|
| Cascade failures | Circuit Breaker |
| Temporary errors | Retry, Timeout |
| Isolation | Bulkhead |
| Monitoring | Health Check |

### Distribution

| Problem | Patterns |
|---------|----------|
| Distributed transactions | Saga, Outbox |
| Communication | Message Queue, Pub/Sub |
| Scalability | Sharding, CQRS |
| Multi-region | Geode |

### Security

| Problem | Patterns |
|---------|----------|
| Authentication | OAuth, JWT, OIDC |
| Authorization | RBAC, ABAC |
| Secrets | Vault, Sealed Secrets |

### Refactoring / Migration

| Problem | Patterns |
|---------|----------|
| Migration without long branches | Branch by Abstraction |
| Legacy system replacement | Strangler Fig |
| Progressive deployment | Feature Toggle, Canary |
| Test new implementation | Parallel Run, Dark Launch |
| Instant rollback | Feature Toggle |

---

## Relationships Between Patterns

```
                    PRINCIPLES
                        │
        ┌───────────────┼───────────────┐
        ▼               ▼               ▼
    CREATIONAL      STRUCTURAL      BEHAVIORAL
        │               │               │
        └───────────────┼───────────────┘
                        │
        ┌───────────────┼───────────────┐
        ▼               ▼               ▼
   ENTERPRISE         DDD           FUNCTIONAL
        │               │               │
        └───────────────┼───────────────┘
                        │
        ┌───────────────┼───────────────┐
        ▼               ▼               ▼
  ARCHITECTURAL     MESSAGING        CLOUD
        │               │               │
        └───────────────┼───────────────┘
                        │
        ┌───────────────┼───────────────┐
        ▼               ▼               ▼
   RESILIENCE       SECURITY        DEVOPS
```

---

## Sources

- [Gang of Four - Design Patterns](https://en.wikipedia.org/wiki/Design_Patterns)
- [Martin Fowler - PoEAA](https://martinfowler.com/eaaCatalog/)
- [Enterprise Integration Patterns](https://www.enterpriseintegrationpatterns.com/)
- [Microsoft Azure Patterns](https://learn.microsoft.com/en-us/azure/architecture/patterns/)
- [microservices.io](https://microservices.io/patterns/)
- [Refactoring Guru](https://refactoring.guru/design-patterns)
