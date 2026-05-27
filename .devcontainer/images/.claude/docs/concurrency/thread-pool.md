# Thread Pool

Pattern for managing a pool of workers to execute tasks in parallel.

---

## What is a Thread Pool?

> Maintain a set of pre-created workers to process tasks without creation overhead.

```
+--------------------------------------------------------------+
|                       Thread Pool                             |
|                                                               |
|  Task Queue                    Workers                        |
|  +--------+                                                   |
|  | Task 1 | ----+          +----------+                       |
|  +--------+     |          | Worker 1 | --> execute Task 1    |
|  | Task 2 | ----+--------->+----------+                       |
|  +--------+     |          | Worker 2 | --> execute Task 2    |
|  | Task 3 | ----+          +----------+                       |
|  +--------+     |          | Worker 3 | --> (idle)            |
|  | Task 4 | ----+          +----------+                       |
|  +--------+                | Worker 4 | --> execute Task 3    |
|  |  ...   |                +----------+                       |
|  +--------+                                                   |
|                                                               |
|  maxWorkers: 4     activeWorkers: 3     queueSize: N          |
+--------------------------------------------------------------+
```

**Why:**

- Avoid thread creation/destruction cost
- Limit concurrency (prevent overload)
- Reuse resources

---

## Go Implementation

### Basic ThreadPool

```go
package pool

import (
	"context"
	"fmt"
	"runtime"
	"sync"
)

// Task represents a unit of work.
type Task func(ctx context.Context) error

// Pool manages a pool of workers.
type Pool struct {
	tasks       chan Task
	workers     int
	wg          sync.WaitGroup
	ctx         context.Context
	cancel      context.CancelFunc
	activeCount int
	mu          sync.Mutex
}

// NewPool creates a new worker pool.
func NewPool(workers int) *Pool {
	if workers <= 0 {
		workers = runtime.NumCPU()
	}

	ctx, cancel := context.WithCancel(context.Background())

	p := &Pool{
		tasks:   make(chan Task, workers*2),
		workers: workers,
		ctx:     ctx,
		cancel:  cancel,
	}

	// Start workers
	for i := 0; i < workers; i++ {
		p.wg.Add(1)
		go p.worker(i)
	}

	return p
}

// worker processes tasks from the queue.
func (p *Pool) worker(id int) {
	defer p.wg.Done()

	for {
		select {
		case <-p.ctx.Done():
			return
		case task, ok := <-p.tasks:
			if !ok {
				return
			}

			p.mu.Lock()
			p.activeCount++
			p.mu.Unlock()

			if err := task(p.ctx); err != nil {
				fmt.Printf("Worker %d error: %v\n", id, err)
			}

			p.mu.Lock()
			p.activeCount--
			p.mu.Unlock()
		}
	}
}

// Submit adds a task to the pool.
func (p *Pool) Submit(task Task) error {
	select {
	case <-p.ctx.Done():
		return fmt.Errorf("pool is closed")
	case p.tasks <- task:
		return nil
	}
}

// Shutdown gracefully stops the pool.
func (p *Pool) Shutdown() {
	close(p.tasks)
	p.wg.Wait()
	p.cancel()
}

// Stats returns pool statistics.
type Stats struct {
	Active  int
	Queued  int
	Workers int
}

// Stats returns current pool statistics.
func (p *Pool) Stats() Stats {
	p.mu.Lock()
	defer p.mu.Unlock()

	return Stats{
		Active:  p.activeCount,
		Queued:  len(p.tasks),
		Workers: p.workers,
	}
}
```

**Usage:**

```go
package main

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

func main() {
	pool := NewPool(4)
	defer pool.Shutdown()

	urls := []string{
		"https://api.example.com/1",
		"https://api.example.com/2",
		"https://api.example.com/3",
	}

	for _, url := range urls {
		url := url // Capture for closure
		pool.Submit(func(ctx context.Context) error {
			resp, err := http.Get(url)
			if err != nil {
				return fmt.Errorf("fetching %s: %w", url, err)
			}
			defer resp.Body.Close()

			fmt.Printf("Fetched %s: %d\n", url, resp.StatusCode)
			return nil
		})
	}

	time.Sleep(5 * time.Second)
	stats := pool.Stats()
	fmt.Printf("Active: %d, Queued: %d\n", stats.Active, stats.Queued)
}
```

---

### ThreadPool with priority

```go
package pool

import (
	"container/heap"
	"context"
	"sync"
)

// PriorityTask represents a task with priority.
type PriorityTask struct {
	Task     Task
	Priority int
	index    int
}

// PriorityQueue implements heap.Interface.
type PriorityQueue []*PriorityTask

func (pq PriorityQueue) Len() int { return len(pq) }

func (pq PriorityQueue) Less(i, j int) bool {
	return pq[i].Priority > pq[j].Priority // Higher priority first
}

func (pq PriorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].index = i
	pq[j].index = j
}

func (pq *PriorityQueue) Push(x interface{}) {
	n := len(*pq)
	item := x.(*PriorityTask)
	item.index = n
	*pq = append(*pq, item)
}

func (pq *PriorityQueue) Pop() interface{} {
	old := *pq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	item.index = -1
	*pq = old[0 : n-1]
	return item
}

// PriorityPool manages tasks with priority.
type PriorityPool struct {
	queue   PriorityQueue
	mu      sync.Mutex
	cond    *sync.Cond
	workers int
	wg      sync.WaitGroup
	ctx     context.Context
	cancel  context.CancelFunc
}

// NewPriorityPool creates a priority-based pool.
func NewPriorityPool(workers int) *PriorityPool {
	ctx, cancel := context.WithCancel(context.Background())

	p := &PriorityPool{
		queue:   make(PriorityQueue, 0),
		workers: workers,
		ctx:     ctx,
		cancel:  cancel,
	}
	p.cond = sync.NewCond(&p.mu)

	heap.Init(&p.queue)

	for i := 0; i < workers; i++ {
		p.wg.Add(1)
		go p.worker()
	}

	return p
}

// worker processes tasks by priority.
func (p *PriorityPool) worker() {
	defer p.wg.Done()

	for {
		p.mu.Lock()
		for p.queue.Len() == 0 {
			select {
			case <-p.ctx.Done():
				p.mu.Unlock()
				return
			default:
				p.cond.Wait()
				if p.ctx.Err() != nil {
					p.mu.Unlock()
					return
				}
			}
		}

		item := heap.Pop(&p.queue).(*PriorityTask)
		p.mu.Unlock()

		if err := item.Task(p.ctx); err != nil {
			// Log error
		}
	}
}

// Submit adds a task with priority.
func (p *PriorityPool) Submit(task Task, priority int) error {
	select {
	case <-p.ctx.Done():
		return fmt.Errorf("pool is closed")
	default:
		p.mu.Lock()
		heap.Push(&p.queue, &PriorityTask{
			Task:     task,
			Priority: priority,
		})
		p.cond.Signal()
		p.mu.Unlock()
		return nil
	}
}

// Shutdown stops the pool.
func (p *PriorityPool) Shutdown() {
	p.cancel()
	p.cond.Broadcast()
	p.wg.Wait()
}
```

---

### ThreadPool with timeout and context

```go
package pool

import (
	"context"
	"fmt"
	"time"
)

// RobustPool handles timeouts and cancellation.
type RobustPool struct {
	*Pool
	timeout time.Duration
}

// NewRobustPool creates a pool with timeout support.
func NewRobustPool(workers int, timeout time.Duration) *RobustPool {
	return &RobustPool{
		Pool:    NewPool(workers),
		timeout: timeout,
	}
}

// SubmitWithTimeout submits a task with a timeout.
func (p *RobustPool) SubmitWithTimeout(ctx context.Context, task Task) error {
	if p.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.timeout)
		defer cancel()
	}

	wrappedTask := func(workerCtx context.Context) error {
		// Create a context that respects both worker and caller contexts
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		done := make(chan error, 1)

		go func() {
			done <- task(ctx)
		}()

		select {
		case <-workerCtx.Done():
			return workerCtx.Err()
		case <-ctx.Done():
			return ctx.Err()
		case err := <-done:
			return err
		}
	}

	return p.Pool.Submit(wrappedTask)
}
```

---

## Complexity and Trade-offs

| Aspect | Value |
|--------|-------|
| Submit task | O(1) |
| Memory | O(maxWorkers + queueSize) |
| Context switch | Reduced vs thread creation |

### Advantages

- Concurrency control
- Worker reuse
- Natural backpressure (queue)

### Disadvantages

- Tricky sizing
- Deadlock if tasks are interdependent
- Unbounded queue = memory leak

---

## When to Use

| Situation | Recommended |
|-----------|-------------|
| Parallel HTTP requests | Yes |
| CPU-intensive computations | Yes |
| Batch processing | Yes |
| Interdependent tasks | Caution (deadlock) |

---

## Related Patterns

| Pattern | Relation |
|---------|----------|
| **Object Pool** | Same concept, objects vs workers |
| **Producer-Consumer** | Queue between producer and pool |
| **Semaphore** | Similar limitation |
| **Fork-Join** | Divide tasks for the pool |

---

## Sources

- [Go Concurrency Patterns](https://go.dev/blog/pipelines)
- [Java ThreadPoolExecutor](https://docs.oracle.com/javase/8/docs/api/java/util/concurrent/ThreadPoolExecutor.html)
- [sync.Pool Documentation](https://pkg.go.dev/sync#Pool)
