# Refactoring Patterns

Patterns for safely improving and migrating existing code.

---

## Documented Patterns

| Pattern | File | Usage |
|---------|------|-------|
| Branch by Abstraction | [branch-by-abstraction.md](branch-by-abstraction.md) | Progressive migration on trunk |
| Strangler Fig | (cloud/strangler.md) | Legacy system replacement |
| Parallel Run | [branch-by-abstraction.md](branch-by-abstraction.md#parallel-run) | Testing two implementations |
| Dark Launch | [branch-by-abstraction.md](branch-by-abstraction.md#dark-launch) | Invisible feature in production |

---

## 1. Branch by Abstraction

> Migrate an implementation to another without long-lived Git branches.

```go
package payment

import (
	"context"
	"fmt"
)

// Money represents a monetary amount.
type Money struct {
	Amount   int64
	Currency string
}

// Result represents a payment processing result.
type Result struct {
	ID          string
	Status      string
	Amount      Money
	Error       error
}

// Step 1: Create abstraction
type PaymentProcessor interface {
	Charge(ctx context.Context, amount Money) (*Result, error)
}

// Step 2: Old implementation
type StripeProcessor struct {
	apiKey string
}

func NewStripeProcessor(apiKey string) *StripeProcessor {
	return &StripeProcessor{apiKey: apiKey}
}

func (s *StripeProcessor) Charge(ctx context.Context, amount Money) (*Result, error) {
	// Old Stripe logic
	return &Result{
		ID:     "stripe_123",
		Status: "success",
		Amount: amount,
	}, nil
}

// Step 3: New implementation
type AdyenProcessor struct {
	apiKey string
}

func NewAdyenProcessor(apiKey string) *AdyenProcessor {
	return &AdyenProcessor{apiKey: apiKey}
}

func (a *AdyenProcessor) Charge(ctx context.Context, amount Money) (*Result, error) {
	// New Adyen logic
	return &Result{
		ID:     "adyen_456",
		Status: "success",
		Amount: amount,
	}, nil
}

// FeatureToggle represents feature flag configuration.
type FeatureToggle interface {
	IsEnabled(ctx context.Context, feature string) bool
	RolloutPercentage(ctx context.Context, feature string) int
}

// Step 4: Factory for routing
type PaymentFactory struct {
	stripeKey string
	adyenKey  string
	features  FeatureToggle
}

func NewPaymentFactory(stripeKey, adyenKey string, features FeatureToggle) *PaymentFactory {
	return &PaymentFactory{
		stripeKey: stripeKey,
		adyenKey:  adyenKey,
		features:  features,
	}
}

func (f *PaymentFactory) Create(ctx context.Context) PaymentProcessor {
	if f.features.IsEnabled(ctx, "adyen") {
		return NewAdyenProcessor(f.adyenKey)
	}
	return NewStripeProcessor(f.stripeKey)
}

// Step 5: Progressive rollout
// 1% → 10% → 50% → 100%
// Configuration in FeatureToggle

// Step 6: Remove the old implementation
// Once rollout reaches 100%, remove StripeProcessor
```

**When:** Replacing a dependency, refactoring a module, migrating an API.

**Related to:** Feature Toggle, Adapter, Strategy

---

## 2. Strangler Fig

> Progressively replace a legacy system with a new one.

```go
package order

import (
	"context"
	"fmt"
)

// OrderData represents order creation data.
type OrderData struct {
	Region string
	Total  int64
	Items  []string
}

// Order represents an order entity.
type Order struct {
	ID     string
	Data   OrderData
	Status string
}

// LegacyOrderSystem represents the old order system.
type LegacyOrderSystem interface {
	CreateOrder(ctx context.Context, data OrderData) (*Order, error)
}

// NewOrderService represents the new order system.
type NewOrderService interface {
	Create(ctx context.Context, data OrderData) (*Order, error)
}

// FeatureFlags provides feature toggle configuration.
type FeatureFlags interface {
	IsEnabled(ctx context.Context, feature string) bool
}

// Facade that routes to legacy or new
type OrderFacade struct {
	legacySystem LegacyOrderSystem
	newService   NewOrderService
	features     FeatureFlags
}

func NewOrderFacade(
	legacy LegacyOrderSystem,
	newSvc NewOrderService,
	features FeatureFlags,
) *OrderFacade {
	return &OrderFacade{
		legacySystem: legacy,
		newService:   newSvc,
		features:     features,
	}
}

func (o *OrderFacade) CreateOrder(ctx context.Context, data OrderData) (*Order, error) {
	if o.canUseNewSystem(ctx, data) {
		order, err := o.newService.Create(ctx, data)
		if err != nil {
			return nil, fmt.Errorf("new order service: %w", err)
		}
		return order, nil
	}

	order, err := o.legacySystem.CreateOrder(ctx, data)
	if err != nil {
		return nil, fmt.Errorf("legacy order system: %w", err)
	}
	return order, nil
}

func (o *OrderFacade) canUseNewSystem(ctx context.Context, data OrderData) bool {
	// Progressive migration criteria
	return data.Region == "EU" &&
		data.Total < 10000 &&
		o.features.IsEnabled(ctx, "new-order-system")
}
```

**When:** Migrating a monolith, replacing a legacy system.

**Related to:** Branch by Abstraction, Anti-Corruption Layer

---

## 3. Parallel Run

> Run two implementations in parallel and compare results.

```go
package processor

import (
	"context"
	"fmt"
	"log/slog"

	"golang.org/x/sync/errgroup"
)

// Data represents input data for processing.
type Data struct {
	ID      string
	Payload []byte
}

// ProcessResult represents processing result.
type ProcessResult struct {
	ID     string
	Output []byte
	Error  error
}

// Processor defines the processing interface.
type Processor interface {
	Process(ctx context.Context, data Data) (*ProcessResult, error)
}

// Comparator compares two results.
type Comparator interface {
	Compare(ctx context.Context, legacy, modern *ProcessResult)
}

type ParallelProcessor struct {
	legacy  Processor
	modern  Processor
	compare Comparator
	logger  *slog.Logger
}

func NewParallelProcessor(
	legacy, modern Processor,
	comparator Comparator,
	logger *slog.Logger,
) *ParallelProcessor {
	return &ParallelProcessor{
		legacy:  legacy,
		modern:  modern,
		compare: comparator,
		logger:  logger,
	}
}

func (p *ParallelProcessor) Process(ctx context.Context, data Data) (*ProcessResult, error) {
	var legacyResult, modernResult *ProcessResult
	var legacyErr, modernErr error

	g, gctx := errgroup.WithContext(ctx)

	// Run legacy
	g.Go(func() error {
		legacyResult, legacyErr = p.legacy.Process(gctx, data)
		return legacyErr
	})

	// Run modern (do not propagate error)
	g.Go(func() error {
		modernResult, modernErr = p.modern.Process(gctx, data)
		if modernErr != nil {
			p.logger.Error("modern processor failed",
				"error", modernErr,
				"data_id", data.ID)
		}
		return nil // Do not block legacy
	})

	// Wait for both
	if err := g.Wait(); err != nil {
		return nil, fmt.Errorf("legacy processor: %w", err)
	}

	// Compare in the background
	go p.compare.Compare(context.Background(), legacyResult, modernResult)

	// Return the trusted result (legacy)
	return legacyResult, nil
}
```

**When:** Validating a new implementation in production.

---

## 4. Dark Launch

> Activate code in production without exposing the result.

```go
package feature

import (
	"context"
	"log/slog"
)

// Data represents input data.
type Data struct {
	ID      string
	Payload map[string]interface{}
}

// Result represents processing result.
type Result struct {
	Data   Data
	Output interface{}
}

// Processor processes data.
type Processor interface {
	Process(ctx context.Context, data Data) (*Result, error)
}

// MetricsRecorder records metrics.
type MetricsRecorder interface {
	Record(ctx context.Context, result *Result)
}

type DarkLaunchFeature struct {
	legacy  Processor
	modern  Processor
	metrics MetricsRecorder
	logger  *slog.Logger
}

func NewDarkLaunchFeature(
	legacy, modern Processor,
	metrics MetricsRecorder,
	logger *slog.Logger,
) *DarkLaunchFeature {
	return &DarkLaunchFeature{
		legacy:  legacy,
		modern:  modern,
		metrics: metrics,
		logger:  logger,
	}
}

func (d *DarkLaunchFeature) Process(ctx context.Context, data Data) (*Result, error) {
	// Run legacy code (the trusted one)
	result, err := d.legacy.Process(ctx, data)
	if err != nil {
		return nil, err
	}

	// Run the new code without using the result
	// Do not block the response, do not propagate errors
	go func() {
		// Create a new context to avoid cancellation
		bgCtx := context.Background()

		modernResult, modernErr := d.modern.Process(bgCtx, data)
		if modernErr != nil {
			d.logger.Error("dark launch error",
				"error", modernErr,
				"data_id", data.ID)
			return
		}

		// Record metrics
		d.metrics.Record(bgCtx, modernResult)
	}()

	return result, nil
}
```

**When:** Testing load and performance before activation.

---

## Decision Table

| Need | Pattern |
|------|---------|
| Replace a dependency | Branch by Abstraction |
| Migrate a legacy system | Strangler Fig |
| Validate in production | Parallel Run |
| Test the load | Dark Launch |
| Instant rollback | Feature Toggle |
| Database migration | Double-Write + Switch |

---

## Typical Migration Workflow

```
1. Create the abstraction (interface)
       │
2. Implement the new code
       │
3. Double-write (if data)
       │
4. Parallel Run (validation)
       │
5. Feature Toggle (rollout)
       │  0% → 1% → 10% → 50% → 100%
       │
6. Remove the old code
       │
7. Remove the toggle
```

---

## Sources

- [Martin Fowler - Branch by Abstraction](https://martinfowler.com/bliki/BranchByAbstraction.html)
- [Martin Fowler - Strangler Fig](https://martinfowler.com/bliki/StranglerFigApplication.html)
- [Trunk Based Development](https://trunkbaseddevelopment.com/)
