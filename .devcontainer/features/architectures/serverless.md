# Serverless / FaaS

> Functions as a Service - Pay per execution

## Concept

Code executed on demand, without server management.

## Recommended Languages

| Language | Platform | Suitability |
|---------|----------|-----------|
| **Node.js** | AWS Lambda, Vercel | Excellent |
| **Python** | AWS Lambda, GCP | Excellent |
| **Go** | AWS Lambda, GCP | Very good |
| **Rust** | AWS Lambda | Good |
| **Java** | AWS Lambda | Average (cold start) |

## Structure

```
/src
├── functions/
│   ├── create-user/
│   │   ├── handler.ts
│   │   └── schema.json
│   ├── process-order/
│   │   ├── handler.ts
│   │   └── schema.json
│   └── send-email/
│       └── handler.ts
├── shared/
│   ├── utils/
│   └── types/
└── serverless.yml       # or terraform/
```

## Advantages

- Zero ops (managed)
- Pay per use
- Automatic scaling
- Simple deployment
- Focus on code

## Disadvantages

- Cold starts
- Vendor lock-in
- Stateless mandatory
- Unpredictable cost at scale
- Difficult debugging
- Execution limits

## Constraints

- Stateless (no local state)
- Timeout (15min max AWS)
- Memory limits
- Idempotency required

## Rules

1. One function = one responsibility
2. Always stateless
3. Always idempotent
4. External state (DynamoDB, S3)
5. Fast cold start (small bundle)

## When to Use

- Variable/unpredictable traffic
- Event-driven tasks
- Lightweight APIs
- Variable budget
- Quick POC

## When to Avoid

- Constant high traffic -> EC2/K8s is cheaper
- Strict real-time requirements
- Long-running tasks
- Complex state management
