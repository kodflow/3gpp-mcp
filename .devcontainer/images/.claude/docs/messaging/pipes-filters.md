# Pipes and Filters Pattern

> Composable message processing pipeline where each filter performs an independent transformation.

## Overview

```
+--------+     +--------+     +--------+     +--------+
| Filter |---->| Filter |---->| Filter |---->| Filter |
|   A    |     |   B    |     |   C    |     |   D    |
+--------+     +--------+     +--------+     +--------+
    |              |              |              |
    v              v              v              v
  Validate      Enrich       Transform       Route

Input ============= PIPE ============= PIPE =============> Output
```

---

## Fundamental Concepts

```
FILTER: Unite de traitement autonome
  - Input -> Processing -> Output
  - Single Responsibility
  - Stateless (idealement)

PIPE: Connecteur entre filtres
  - Transporte les messages
  - Peut etre synchrone ou asynchrone
  - Peut etre une queue, un channel, un stream
```

---

## Base Implementation

```go
package pipeline

import (
	"context"
	"fmt"
)

// Filter processes messages.
type Filter[TInput, TOutput any] interface {
	Process(ctx context.Context, input TInput) (TOutput, error)
}

// Pipeline chains multiple filters.
type Pipeline[T any] struct {
	filters []Filter[T, T]
}

// NewPipeline creates a new pipeline.
func NewPipeline[T any]() *Pipeline[T] {
	return &Pipeline[T]{
		filters: make([]Filter[T, T], 0),
	}
}

// AddFilter adds a filter to the pipeline.
func (p *Pipeline[T]) AddFilter(filter Filter[T, T]) *Pipeline[T] {
	p.filters = append(p.filters, filter)
	return p
}

// Execute runs the pipeline.
func (p *Pipeline[T]) Execute(ctx context.Context, input T) (T, error) {
	current:= input
	for _, filter:= range p.filters {
		result, err:= filter.Process(ctx, current)
		if err != nil {
			var zero T
			return zero, fmt.Errorf("pipeline filter failed: %w", err)
		}
		current = result
	}
	return current, nil
}

// FilterFunc adapts a function to Filter interface.
type FilterFunc[TInput, TOutput any] func(context.Context, TInput) (TOutput, error)

// Process implements Filter interface.
func (f FilterFunc[TInput, TOutput]) Process(ctx context.Context, input TInput) (TOutput, error) {
	return f(ctx, input)
}

// Example types
type RawOrder struct {
	ID    string
	Items []string
}

type ValidatedOrder struct {
	RawOrder
	Valid bool
}

type EnrichedOrder struct {
	ValidatedOrder
	CustomerName string
}
```

**When:** Composable processing, separation of responsibilities.
**Related to:** Chain of Responsibility, Decorator.

---

## Reusable Filters

```go
package filters

import (
	"context"
	"fmt"
)

// ValidationFilter validates input.
type ValidationFilter[T any] struct {
	validator func(T) error
}

// NewValidationFilter creates a validation filter.
func NewValidationFilter[T any](validator func(T) error) *ValidationFilter[T] {
	return &ValidationFilter[T]{
		validator: validator,
	}
}

// Process validates the input.
func (vf *ValidationFilter[T]) Process(ctx context.Context, input T) (T, error) {
	if err:= vf.validator(input); err != nil {
		var zero T
		return zero, fmt.Errorf("validation failed: %w", err)
	}
	return input, nil
}

// TransformFilter transforms input to output.
type TransformFilter[TInput, TOutput any] struct {
	transformer func(context.Context, TInput) (TOutput, error)
}

// NewTransformFilter creates a transform filter.
func NewTransformFilter[TInput, TOutput any](
	transformer func(context.Context, TInput) (TOutput, error),
) *TransformFilter[TInput, TOutput] {
	return &TransformFilter[TInput, TOutput]{
		transformer: transformer,
	}
}

// Process transforms the input.
func (tf *TransformFilter[TInput, TOutput]) Process(ctx context.Context, input TInput) (TOutput, error) {
	return tf.transformer(ctx, input)
}

// EnrichmentFilter enriches messages with external data.
type EnrichmentFilter[T any] struct {
	enricher func(context.Context, T) (map[string]interface{}, error)
}

// NewEnrichmentFilter creates an enrichment filter.
func NewEnrichmentFilter[T any](
	enricher func(context.Context, T) (map[string]interface{}, error),
) *EnrichmentFilter[T] {
	return &EnrichmentFilter[T]{
		enricher: enricher,
	}
}

// Process enriches the input.
func (ef *EnrichmentFilter[T]) Process(ctx context.Context, input T) (T, error) {
	enrichedData, err:= ef.enricher(ctx, input)
	if err != nil {
		var zero T
		return zero, fmt.Errorf("enrichment failed: %w", err)
	}
	// Merge enriched data into input (implementation specific)
	return input, nil
}

// ConditionalFilter filters based on predicate.
type ConditionalFilter[T any] struct {
	predicate func(T) bool
}

// NewConditionalFilter creates a conditional filter.
func NewConditionalFilter[T any](predicate func(T) bool) *ConditionalFilter[T] {
	return &ConditionalFilter[T]{
		predicate: predicate,
	}
}

// Process filters the input.
func (cf *ConditionalFilter[T]) Process(ctx context.Context, input T) (T, error) {
	if !cf.predicate(input) {
		var zero T
		return zero, fmt.Errorf("message filtered out")
	}
	return input, nil
}
```

**When:** Generic reusable filters.
**Related to:** Strategy, Template Method.

---

## Asynchronous Pipeline with Channels

```go
package async

import (
	"context"
	"fmt"
	"sync"
)

// AsyncFilter processes messages asynchronously.
type AsyncFilter[T any] interface {
	Process(ctx context.Context, input T) (T, error)
}

// AsyncPipeline processes messages through channels.
type AsyncPipeline[T any] struct {
	stages []stage[T]
	mu     sync.RWMutex
}

type stage[T any] struct {
	name      string
	filter    AsyncFilter[T]
	inputCh   chan T
	outputCh  chan T
	workers   int
}

// NewAsyncPipeline creates a new async pipeline.
func NewAsyncPipeline[T any]() *AsyncPipeline[T] {
	return &AsyncPipeline[T]{
		stages: make([]stage[T], 0),
	}
}

// AddStage adds a processing stage.
func (ap *AsyncPipeline[T]) AddStage(name string, filter AsyncFilter[T], bufferSize, workers int) *AsyncPipeline[T] {
	ap.mu.Lock()
	defer ap.mu.Unlock()

	inputCh:= make(chan T, bufferSize)
	outputCh:= make(chan T, bufferSize)

	ap.stages = append(ap.stages, stage[T]{
		name:     name,
		filter:   filter,
		inputCh:  inputCh,
		outputCh: outputCh,
		workers:  workers,
	})

	return ap
}

// Start starts the pipeline.
func (ap *AsyncPipeline[T]) Start(ctx context.Context) error {
	ap.mu.RLock()
	defer ap.mu.RUnlock()

	var wg sync.WaitGroup

	// Connect stages
	for i:= 0; i < len(ap.stages)-1; i++ {
		current:= &ap.stages[i]
		next:= &ap.stages[i+1]
		currentCaptured:= current
		nextCaptured:= next

		wg.Go(func() {
			for msg:= range currentCaptured.outputCh {
				select {
				case nextCaptured.inputCh <- msg:
				case <-ctx.Done():
					return
				}
			}
			close(nextCaptured.inputCh)
		}()
	}

	// Start workers for each stage
	for i:= range ap.stages {
		st:= &ap.stages[i]
		for w:= 0; w < st.workers; w++ {
			wg.Go(func() {
				for {
					select {
					case <-ctx.Done():
						return
					case msg, ok:= <-s.inputCh:
						if !ok {
							return
						}

						result, err:= s.filter.Process(ctx, msg)
						if err != nil {
							// Log error and continue
							continue
						}

						select {
						case s.outputCh <- result:
						case <-ctx.Done():
							return
						}
					}
				}
			})
		}
	}

	wg.Wait()
	return nil
}

// Input returns the input channel of the first stage.
func (ap *AsyncPipeline[T]) Input() chan<- T {
	ap.mu.RLock()
	defer ap.mu.RUnlock()

	if len(ap.stages) == 0 {
		return nil
	}
	return ap.stages[0].inputCh
}

// Output returns the output channel of the last stage.
func (ap *AsyncPipeline[T]) Output() <-chan T {
	ap.mu.RLock()
	defer ap.mu.RUnlock()

	if len(ap.stages) == 0 {
		return nil
	}
	return ap.stages[len(ap.stages)-1].outputCh
}
```

**When:** High-performance asynchronous processing.
**Related to:** Producer-Consumer, Worker Pool.

---

## Parallelization

```go
package parallel

import (
	"context"
	"sync"
)

// ParallelPipeline runs filters in parallel and merges results.
type ParallelPipeline[TInput, TOutput any] struct {
	filters []Filter[TInput, TOutput]
	merger  func([]TOutput) (TOutput, error)
}

// NewParallelPipeline creates a parallel pipeline.
func NewParallelPipeline[TInput, TOutput any](
	filters []Filter[TInput, TOutput],
	merger func([]TOutput) (TOutput, error),
) *ParallelPipeline[TInput, TOutput] {
	return &ParallelPipeline[TInput, TOutput]{
		filters: filters,
		merger:  merger,
	}
}

// Process runs filters in parallel.
func (pp *ParallelPipeline[TInput, TOutput]) Process(ctx context.Context, input TInput) (TOutput, error) {
	results:= make([]TOutput, len(pp.filters))
	errCh:= make(chan error, len(pp.filters))
	var wg sync.WaitGroup

	for i, filter:= range pp.filters {
		wg.Go(func() {
			result, err:= f.Process(ctx, input)
			if err != nil {
				errCh <- err
				return
			}
			results[idx] = result
		})
	}

	wg.Wait()
	close(errCh)

	if err:= <-errCh; err != nil {
		var zero TOutput
		return zero, err
	}

	return pp.merger(results)
}

// Filter interface for parallel processing.
type Filter[TInput, TOutput any] interface {
	Process(ctx context.Context, input TInput) (TOutput, error)
}
```

**When:** Parallel enrichment, fan-out/fan-in.
**Related to:** Scatter-Gather, Fork-Join.

---

## Error Handling

```go
package resilient

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	// ErrPipelineFailed indicates pipeline failure.
	ErrPipelineFailed = errors.New("pipeline failed")
	// ErrTimeout indicates filter timeout.
	ErrTimeout = errors.New("filter timeout")
)

// ResilientPipeline handles errors gracefully.
type ResilientPipeline[T any] struct {
	filters       []Filter[T, T]
	errorHandlers map[string]ErrorHandler
	timeout       time.Duration
}

// Filter processes messages.
type Filter[TInput, TOutput any] interface {
	Process(ctx context.Context, input TInput) (TOutput, error)
}

// ErrorHandler handles errors from filters.
type ErrorHandler interface {
	Handle(ctx context.Context, err error, input interface{}) (interface{}, bool, error)
}

// NewResilientPipeline creates a resilient pipeline.
func NewResilientPipeline[T any](timeout time.Duration) *ResilientPipeline[T] {
	return &ResilientPipeline[T]{
		filters:       make([]Filter[T, T], 0),
		errorHandlers: make(map[string]ErrorHandler),
		timeout:       timeout,
	}
}

// AddFilter adds a filter with error handler.
func (rp *ResilientPipeline[T]) AddFilter(name string, filter Filter[T, T], handler ErrorHandler) *ResilientPipeline[T] {
	rp.filters = append(rp.filters, filter)
	if handler != nil {
		rp.errorHandlers[name] = handler
	}
	return rp
}

// Execute runs the pipeline with resilience.
func (rp *ResilientPipeline[T]) Execute(ctx context.Context, input T) (T, error) {
	current:= input

	for i, filter:= range rp.filters {
		filterName:= fmt.Sprintf("filter_%d", i)

		// Execute with timeout
		result, err:= rp.executeWithTimeout(ctx, filter, current)
		if err != nil {
			handler, exists:= rp.errorHandlers[filterName]
			if !exists {
				var zero T
				return zero, fmt.Errorf("%s failed: %w", filterName, err)
			}

			// Try to recover
			recovered, cont, handlerErr:= handler.Handle(ctx, err, current)
			if handlerErr != nil {
				var zero T
				return zero, fmt.Errorf("error handler failed: %w", handlerErr)
			}

			if !cont {
				var zero T
				return zero, fmt.Errorf("%s failed and recovery not possible: %w", filterName, err)
			}

			current = recovered.(T)
			continue
		}

		current = result
	}

	return current, nil
}

func (rp *ResilientPipeline[T]) executeWithTimeout(
	ctx context.Context,
	filter Filter[T, T],
	input T,
) (T, error) {
	ctx, cancel:= context.WithTimeout(ctx, rp.timeout)
	defer cancel()

	resultCh:= make(chan T, 1)
	errCh:= make(chan error, 1)

	go func() {
		result, err:= filter.Process(ctx, input)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- result
	}()

	select {
	case <-ctx.Done():
		var zero T
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return zero, ErrTimeout
		}
		return zero, ctx.Err()
	case err:= <-errCh:
		var zero T
		return zero, err
	case result:= <-resultCh:
		return result, nil
	}
}
```

**When:** Production, high availability, fault tolerance.
**Related to:** Circuit Breaker, Retry, Bulkhead.

---

## When to Use

- Multi-step message processing with independent and reusable steps
- Flexible composition of transformations with separation of responsibilities
- High-performance asynchronous pipelines with parallelization
- Data stream processing with conditional filters
- Modular architecture allowing adding/removing filters

## Related Patterns

- [Message Translator](./message-translator.md) - Transformation filter
- [Message Router](./message-router.md) - Routing in the pipeline
- [Splitter-Aggregator](./splitter-aggregator.md) - Split and recombine
- [Dead Letter Channel](./dead-letter.md) - Pipeline error handling

## Complementary Patterns

- **Message Router** - Dynamic routing
- **Splitter/Aggregator** - Split and merge
- **Content Enricher** - Filter type
- **Message Filter** - Filter type
