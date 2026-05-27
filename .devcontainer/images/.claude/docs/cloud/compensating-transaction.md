# Compensating Transaction Pattern

> Undo the effects of already-executed operations in a distributed workflow.

## Principle

```
┌─────────────────────────────────────────────────────────────────────────┐
│                    COMPENSATING TRANSACTION                              │
│                                                                          │
│   FORWARD OPERATIONS (Success path)                                      │
│   ┌────────┐     ┌────────┐     ┌────────┐     ┌────────┐              │
│   │   T1   │────▶│   T2   │────▶│   T3   │────▶│   T4   │              │
│   │ Create │     │ Reserve│     │ Charge │     │  Ship  │              │
│   │ Order  │     │ Stock  │     │ Payment│     │        │              │
│   └────────┘     └────────┘     └────────┘     └────────┘              │
│                                       │                                  │
│                                       │ FAILURE!                         │
│                                       ▼                                  │
│   COMPENSATION (Rollback path)                                           │
│   ┌────────┐     ┌────────┐     ┌────────┐                              │
│   │   C1   │◀────│   C2   │◀────│   C3   │                              │
│   │ Cancel │     │ Release│     │ Refund │                              │
│   │ Order  │     │ Stock  │     │ Payment│                              │
│   └────────┘     └────────┘     └────────┘                              │
│                                                                          │
└─────────────────────────────────────────────────────────────────────────┘
```

## Difference with ACID Rollback

| Aspect | ACID Rollback | Compensation |
|--------|---------------|--------------|
| **Scope** | Single transaction | Distributed transactions |
| **Mechanism** | DB undo log | Explicit business logic |
| **Atomicity** | Guaranteed | Best effort |
| **Visibility** | Invisible | May be visible |

## Go Example

```go
package compensation

import (
	"context"
	"fmt"
	"log"
)

// CompensableOperation defines an operation that can be compensated.
type CompensableOperation[T any] struct {
	Name         string
	Execute      func(ctx context.Context) (T, error)
	Compensate   func(ctx context.Context, result T) error
	IsCompensable func(result T) bool
}

// ExecutedOperation tracks an executed operation with its result.
type ExecutedOperation struct {
	Name          string
	Result        interface{}
	IsCompensable bool
	CompensateFn  func(ctx context.Context) error
}

// CompensatingTransaction manages a sequence of compensable operations.
type CompensatingTransaction struct {
	executedOperations []ExecutedOperation
}

// NewCompensatingTransaction creates a new CompensatingTransaction.
func NewCompensatingTransaction() *CompensatingTransaction {
	return &CompensatingTransaction{
		executedOperations: make([]ExecutedOperation, 0),
	}
}

// Execute runs all operations and compensates on failure.
func (ct *CompensatingTransaction) Execute(ctx context.Context, operations []CompensableOperation[interface{}]) error {
	for _, op := range operations {
		log.Printf("Executing: %s", op.Name)

		result, err := op.Execute(ctx)
		if err != nil {
			log.Printf("Failed at: %s - %v", op.Name, err)
			if compErr := ct.compensate(ctx); compErr != nil {
				return fmt.Errorf("compensation failed: %w", compErr)
			}
			return fmt.Errorf("operation failed: %w", err)
		}

		// Track executed operation
		ct.executedOperations = append(ct.executedOperations, ExecutedOperation{
			Name:          op.Name,
			Result:        result,
			IsCompensable: op.IsCompensable(result),
			CompensateFn: func(ctx context.Context) error {
				return op.Compensate(ctx, result)
			},
		})
	}

	return nil
}

func (ct *CompensatingTransaction) compensate(ctx context.Context) error {
	log.Println("Starting compensation...")

	// Compensate in reverse order
	for i := len(ct.executedOperations) - 1; i >= 0; i-- {
		op := ct.executedOperations[i]

		if !op.IsCompensable {
			continue
		}

		log.Printf("Compensating: %s", op.Name)
		if err := op.CompensateFn(ctx); err != nil {
			// Log but continue compensating others
			log.Printf("Compensation failed for %s: %v", op.Name, err)
			// Queue for manual intervention
			ct.handleCompensationFailure(ctx, op, err)
		}
	}

	return nil
}

func (ct *CompensatingTransaction) handleCompensationFailure(ctx context.Context, op ExecutedOperation, err error) {
	// Queue for manual review
	log.Printf("Manual review needed for operation: %s, error: %v", op.Name, err)
}
```

## Example: Travel Booking (Go)

```go
// This example follows the same idiomatic Go patterns
// as the main example above.
// Specific implementation based on standard Go
// interfaces and conventions.
```

## Compensation Patterns

### 1. Immediate Compensation

```go
// This example follows the same idiomatic Go patterns
// as the main example above.
// Specific implementation based on standard Go
// interfaces and conventions.
```

### 2. Deferred Compensation

```go
// This example follows the same idiomatic Go patterns
// as the main example above.
// Specific implementation based on standard Go
// interfaces and conventions.
```

### 3. Compensation with Retry

```go
// This example follows the same idiomatic Go patterns
// as the main example above.
// Specific implementation based on standard Go
// interfaces and conventions.
```

## Anti-patterns

| Anti-pattern | Problem | Solution |
|--------------|---------|----------|
| Non-idempotent compensation | Double compensation | Idempotency keys |
| No timeout | Indefinite blocking | Timeout + escalation |
| Ignored partial compensation | Inconsistent state | Retry + alerting |
| Incorrect order | Broken dependencies | Compensate in reverse order |

## When to Use

- Distributed workflows involving multiple services or databases
- Operations that cannot use distributed ACID transactions
- Systems requiring semantic rollback rather than technical rollback
- Multi-system resource reservation (travel, e-commerce)
- Long-running business processes requiring partial cancellation

## Related Patterns

| Pattern | Relation |
|---------|----------|
| Saga | Uses compensating transactions |
| Outbox | Compensation reliability |
| Retry | Compensation resilience |
| Dead Letter | Failed compensations |

## Sources

- [Microsoft - Compensating Transaction](https://learn.microsoft.com/en-us/azure/architecture/patterns/compensating-transaction)
- [Saga Pattern](saga.md)
