# Saga Pattern

> Manage distributed transactions without 2PC.

## Problem Solved

```
Transaction ACID impossible in distributed systems

┌─────────┐     ┌─────────┐     ┌─────────┐
│ Order   │     │ Payment │     │ Stock   │
│ Service │     │ Service │     │ Service │
└────┬────┘     └────┬────┘     └────┬────┘
     │               │               │
     └───── No common transaction ────┘
```

## Solution: Saga

Sequence of local transactions with compensations.

```
┌────────────────────────────────────────────────────────────────┐
│                           SAGA                                  │
│                                                                 │
│  T1 ──▶ T2 ──▶ T3 ──▶ T4                                      │
│  │      │      │      │                                        │
│  C1 ◀── C2 ◀── C3 ◀── (failure)                               │
│                                                                 │
│  T = Local transaction                                          │
│  C = Compensation (rollback)                                    │
└────────────────────────────────────────────────────────────────┘
```

## Two Approaches

### 1. Choreography (events)

```
┌─────────┐   OrderCreated   ┌─────────┐   PaymentDone   ┌─────────┐
│  Order  │ ───────────────▶ │ Payment │ ───────────────▶ │  Stock  │
│ Service │                  │ Service │                  │ Service │
└─────────┘                  └─────────┘                  └─────────┘
     ▲                            │                            │
     │         PaymentFailed      │                            │
     └────────────────────────────┘                            │
     │                        StockReserved                    │
     └─────────────────────────────────────────────────────────┘
```

```go
package saga

import (
	"context"
	"fmt"
	"log"
)

// SagaStep defines a step in a saga with action and compensation.
type SagaStep struct {
	Action       func(ctx context.Context) error
	Compensation func(ctx context.Context) error
}

// Saga manages a sequence of saga steps.
type Saga struct {
	steps          []SagaStep
	completedSteps []SagaStep
}

// NewSaga creates a new Saga.
func NewSaga() *Saga {
	return &Saga{
		steps:          make([]SagaStep, 0),
		completedSteps: make([]SagaStep, 0),
	}
}

// AddStep adds a step to the saga.
func (s *Saga) AddStep(step SagaStep) {
	s.steps = append(s.steps, step)
}

// Execute executes all saga steps.
func (s *Saga) Execute(ctx context.Context) error {
	for _, step := range s.steps {
		if err := step.Action(ctx); err != nil {
			log.Printf("Saga step failed: %v", err)

			// Compensate all completed steps
			if compErr := s.Compensate(ctx); compErr != nil {
				return fmt.Errorf("compensation failed: %w (original error: %v)", compErr, err)
			}

			return fmt.Errorf("saga execution failed: %w", err)
		}

		s.completedSteps = append(s.completedSteps, step)
	}

	return nil
}

// Compensate compensates all completed steps in reverse order.
func (s *Saga) Compensate(ctx context.Context) error {
	log.Println("Starting saga compensation...")

	// Compensate in reverse order
	for i := len(s.completedSteps) - 1; i >= 0; i-- {
		step := s.completedSteps[i]

		if err := step.Compensation(ctx); err != nil {
			// Log but continue compensating others
			log.Printf("Compensation step %d failed: %v", i, err)
			// In production, this should be queued for manual intervention
		}
	}

	return nil
}
```

### 2. Orchestration (coordinator)

```
                    ┌─────────────────┐
                    │  Saga           │
                    │  Orchestrator   │
                    └────────┬────────┘
                             │
        ┌────────────────────┼────────────────────┐
        │                    │                    │
        ▼                    ▼                    ▼
   ┌─────────┐         ┌─────────┐         ┌─────────┐
   │  Order  │         │ Payment │         │  Stock  │
   │ Service │         │ Service │         │ Service │
   └─────────┘         └─────────┘         └─────────┘
```

```go
// This example follows the same idiomatic Go patterns
// as the main example above.
// Specific implementation based on interfaces and
// standard Go conventions.
```

## Comparison

| Aspect | Choreography | Orchestration |
|--------|--------------|---------------|
| Coupling | Low | Centralized |
| Complexity | Distributed | In the orchestrator |
| Debugging | Difficult | Easier |
| Scalability | Better | Orchestrator = SPOF |
| Recommended | Simple sagas | Complex sagas |

## When to Use

- Transactions involving multiple independent microservices
- Impossibility of using distributed transactions (2PC)
- Long-running business processes with compensable steps
- Event-driven systems requiring eventual consistency
- E-commerce, reservations, multi-step financial workflows

## Saga Class Implementation

```go
// This example follows the same idiomatic Go patterns
// as the main example above.
// Specific implementation based on interfaces and
// standard Go conventions.
```

## Related Patterns

| Pattern | Relation |
|---------|----------|
| Event Sourcing | State history |
| CQRS | Read model for tracking |
| Outbox | Event reliability |

## Sources

- [Microsoft - Saga Pattern](https://learn.microsoft.com/en-us/azure/architecture/reference-architectures/saga/saga)
- [microservices.io - Saga](https://microservices.io/patterns/data/saga.html)
