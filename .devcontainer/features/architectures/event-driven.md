# Event-Driven Architecture

> Communication through asynchronous events

## Concept

Decoupled components communicating via events (pub/sub).

## Recommended Languages

| Language | Suitability |
|---------|-----------|
| **Java** | Excellent (Kafka, RabbitMQ) |
| **Go** | Excellent (NATS, Kafka) |
| **Node.js** | Very good |
| **Scala** | Excellent (Akka) |
| **Elixir** | Excellent (native) |
| **Python** | Good |

## Structure

```
/src
├── events/              # Event definitions
│   ├── user_created.go
│   └── order_placed.go
├── producers/           # Emitters
│   └── user_service/
├── consumers/           # Receivers
│   └── notification_service/
├── handlers/            # Event handlers
│   └── on_user_created.go
└── infrastructure/
    └── messaging/       # Kafka, RabbitMQ, NATS
```

## Advantages

- Strong decoupling
- Scalability (async)
- Resilience
- Extensibility (add consumers)
- Natural audit trail

## Disadvantages

- Debugging complexity
- Eventual consistency
- Ordering challenges
- Idempotency required
- Complex infrastructure

## Constraints

- Events = immutable
- Consumers = idempotent
- At-least-once delivery assumed
- Schema evolution managed

## Rules

1. Event = past fact (past tense)
2. One event = one responsibility
3. Consumer independent from producer
4. Retry + dead letter queue
5. Event versioning mandatory

## When to Use

- Async workflows
- System integration
- Audit/compliance
- Horizontal scaling
- Reactivity (notifications)

## When to Avoid

- Need for synchronous response
- Strict ACID transactions
- Junior team
- Simple CRUD
