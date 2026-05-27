# Pipeline

Pattern for processing through sequential, potentially parallel stages.

---

## What is a Pipeline?

> Chain of processing stages where each stage transforms data for the next.

```
+--------------------------------------------------------------+
|                        Pipeline                               |
|                                                               |
|  Input --> [Stage 1] --> [Stage 2] --> [Stage 3] --> Output   |
|             Parse        Transform       Validate             |
|                                                               |
|  Per-stage parallelism:                                       |
|                                                               |
|  Data 1: [S1] -----> [S2] -----> [S3]                         |
|  Data 2:      [S1] -----> [S2] -----> [S3]                    |
|  Data 3:           [S1] -----> [S2] -----> [S3]               |
|                                                               |
|  Each stage can process while others are working              |
|                                                               |
|  Throughput = (N items) / (slowest stage time)                |
+--------------------------------------------------------------+
```

**Why:**

- Decompose complex processing
- Parallelize independent stages
- Better resource utilization

---

## Go Implementation

### Basic Pipeline

```go
package pipeline

import (
	"context"
)

// Stage represents a processing stage.
type Stage[I, O any] func(context.Context, I) (O, error)

// Pipeline chains multiple stages.
type Pipeline[T any] struct {
	stages []func(context.Context, T) (T, error)
}

// New creates a new pipeline.
func New[T any]() *Pipeline[T] {
	return &Pipeline[T]{
		stages: make([]func(context.Context, T) (T, error), 0),
	}
}

// AddStage adds a stage to the pipeline.
func (p *Pipeline[T]) AddStage(stage func(context.Context, T) (T, error)) *Pipeline[T] {
	p.stages = append(p.stages, stage)
	return p
}

// Execute runs the pipeline.
func (p *Pipeline[T]) Execute(ctx context.Context, input T) (T, error) {
	result := input

	for _, stage := range p.stages {
		var err error
		result, err = stage(ctx, result)
		if err != nil {
			return result, err
		}
	}

	return result, nil
}
```

**Usage:**

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
)

type Data struct {
	Raw       string
	Parsed    map[string]interface{}
	Validated bool
}

func main() {
	pipeline := New[Data]().
		AddStage(func(ctx context.Context, d Data) (Data, error) {
			// Parse
			var parsed map[string]interface{}
			if err := json.Unmarshal([]byte(d.Raw), &parsed); err != nil {
				return d, fmt.Errorf("parsing: %w", err)
			}
			d.Parsed = parsed
			return d, nil
		}).
		AddStage(func(ctx context.Context, d Data) (Data, error) {
			// Validate
			if _, ok := d.Parsed["id"]; !ok {
				return d, fmt.Errorf("missing id field")
			}
			d.Validated = true
			return d, nil
		})

	result, err := pipeline.Execute(context.Background(), Data{
		Raw: `{"id": 123, "name": "test"}`,
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("Result: %+v\n", result)
}
```

---

### Pipeline with streaming (channels)

```go
package pipeline

import (
	"context"
	"sync"
)

// Stream processes items through a pipeline of stages.
func Stream[T any](
	ctx context.Context,
	input <-chan T,
	stages ...func(context.Context, T) (T, error),
) <-chan Result[T] {
	output := make(chan Result[T])

	go func() {
		defer close(output)

		for item := range input {
			result := item

			for _, stage := range stages {
				var err error
				result, err = stage(ctx, result)
				if err != nil {
					select {
					case output <- Result[T]{Err: err}:
					case <-ctx.Done():
						return
					}
					goto next
				}
			}

			select {
			case output <- Result[T]{Value: result}:
			case <-ctx.Done():
				return
			}

		next:
		}
	}()

	return output
}

// Result holds either a value or an error.
type Result[T any] struct {
	Value T
	Err   error
}
```

---

## Parallel Pipeline

```go
package pipeline

import (
	"context"
	"sync"
)

// ParallelStage represents a parallelized stage.
type ParallelStage[T any] struct {
	Process     func(context.Context, T) (T, error)
	Concurrency int
}

// ParallelPipeline executes stages in parallel.
func ParallelPipeline[T any](
	ctx context.Context,
	input <-chan T,
	stages []ParallelStage[T],
) <-chan Result[T] {
	current := input

	for _, stage := range stages {
		current = parallelStage(ctx, current, stage)
	}

	return resultify(current)
}

// parallelStage runs a stage with multiple workers.
func parallelStage[T any](
	ctx context.Context,
	input <-chan T,
	stage ParallelStage[T],
) <-chan T {
	output := make(chan T)

	var wg sync.WaitGroup
	for i := 0; i < stage.Concurrency; i++ {
		wg.Go(func() { // Go 1.25: handles Add/Done internally
			for item := range input {
				result, err := stage.Process(ctx, item)
				if err != nil {
					// Log or handle error
					continue
				}

				select {
				case output <- result:
				case <-ctx.Done():
					return
				}
			}
		})
	}

	go func() {
		wg.Wait()
		close(output)
	}()

	return output
}

// resultify converts a channel to Result channel.
func resultify[T any](input <-chan T) <-chan Result[T] {
	output := make(chan Result[T])

	go func() {
		defer close(output)

		for item := range input {
			output <- Result[T]{Value: item}
		}
	}()

	return output
}
```

---

## Pipeline with error handling

```go
package pipeline

import (
	"context"
	"fmt"
)

// StageInfo holds stage metadata.
type StageInfo struct {
	Name    string
	Process func(context.Context, interface{}) (interface{}, error)
}

// RobustPipeline handles errors with context.
type RobustPipeline struct {
	stages []StageInfo
}

// NewRobust creates a robust pipeline.
func NewRobust() *RobustPipeline {
	return &RobustPipeline{
		stages: make([]StageInfo, 0),
	}
}

// AddStage adds a named stage.
func (p *RobustPipeline) AddStage(name string, process func(context.Context, interface{}) (interface{}, error)) *RobustPipeline {
	p.stages = append(p.stages, StageInfo{
		Name:    name,
		Process: process,
	})
	return p
}

// PipelineError contains error context.
type PipelineError struct {
	Stage string
	Err   error
}

func (e *PipelineError) Error() string {
	return fmt.Sprintf("stage %s: %v", e.Stage, e.Err)
}

func (e *PipelineError) Unwrap() error {
	return e.Err
}

// Execute runs the pipeline with error tracking.
func (p *RobustPipeline) Execute(ctx context.Context, input interface{}) (interface{}, error) {
	result := input

	for _, stage := range p.stages {
		var err error
		result, err = stage.Process(ctx, result)
		if err != nil {
			return nil, &PipelineError{
				Stage: stage.Name,
				Err:   err,
			}
		}
	}

	return result, nil
}
```

---

## Fan-Out/Fan-In Pattern

```go
package pipeline

import (
	"context"
	"sync"
)

// FanOut duplicates input to multiple outputs.
func FanOut[T any](ctx context.Context, input <-chan T, n int) []<-chan T {
	outputs := make([]<-chan T, n)

	for i := 0; i < n; i++ {
		ch := make(chan T)
		outputs[i] = ch

		go func(out chan<- T) {
			defer close(out)

			for item := range input {
				select {
				case out <- item:
				case <-ctx.Done():
					return
				}
			}
		}(ch)
	}

	return outputs
}

// FanIn merges multiple inputs into one output.
func FanIn[T any](ctx context.Context, inputs ...<-chan T) <-chan T {
	output := make(chan T)

	var wg sync.WaitGroup
	for _, input := range inputs {
		in := input // Capture for closure
		wg.Go(func() { // Go 1.25: handles Add/Done internally
			for item := range in {
				select {
				case output <- item:
				case <-ctx.Done():
					return
				}
			}
		})
	}

	go func() {
		wg.Wait()
		close(output)
	}()

	return output
}
```

---

## Complexity and Trade-offs

| Aspect | Value |
|--------|-------|
| Latency (1 item) | O(sum of stages) |
| Throughput | O(1 / slowest stage) |
| Memory | O(queue sizes) |

### Advantages

- Separation of responsibilities
- Natural parallelism
- Per-stage testability
- Per-stage monitoring
- Native Go channels

### Disadvantages

- Overhead for small processing
- Debugging complexity
- Backpressure to manage

---

## When to Use

| Situation | Recommended |
|-----------|-------------|
| Multi-stage processing | Yes |
| ETL / Data processing | Yes |
| Image processing | Yes |
| Simple business logic | No |
| Critical latency | Caution |

---

## Related Patterns

| Pattern | Relation |
|---------|----------|
| **Chain of Responsibility** | Similar, focus on handlers |
| **Producer-Consumer** | Queues between stages |
| **Decorator** | Sequential transformation |
| **Stream** | Pipeline on continuous flow |

---

## Sources

- [Go Pipelines](https://go.dev/blog/pipelines)
- [Unix Pipes](https://en.wikipedia.org/wiki/Pipeline_(Unix))
- [Enterprise Integration Patterns - Pipes and Filters](https://www.enterpriseintegrationpatterns.com/patterns/messaging/PipesAndFilters.html)
