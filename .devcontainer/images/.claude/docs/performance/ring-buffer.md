# Ring Buffer (Circular Buffer)

High-performance circular data structure for continuous streams.

---

## What is a Ring Buffer?

> Fixed-size buffer that overwrites old data when full.

```
+--------------------------------------------------------------+
|                      Ring Buffer                              |
|                                                               |
|  Capacity: 8                                                  |
|                                                               |
|      0     1     2     3     4     5     6     7              |
|    +-----+-----+-----+-----+-----+-----+-----+-----+          |
|    |  A  |  B  |  C  |  D  |     |     |     |  H  |          |
|    +-----+-----+-----+-----+-----+-----+-----+-----+          |
|            ^                 ^                                |
|          head              tail                               |
|        (read)            (write)                              |
|                                                               |
|  Write: tail advances (modulo capacity)                       |
|  Read:  head advances (modulo capacity)                       |
|                                                               |
|  When tail == head: buffer empty                              |
|  When (tail+1) % cap == head: buffer full                     |
+--------------------------------------------------------------+
```

**Why:**

- Fixed memory allocation (no GC)
- O(1) for read/write
- Ideal for streaming and logs

---

## Go Implementation

### Basic RingBuffer

```go
package ringbuffer

import (
	"errors"
	"sync"
)

var (
	ErrBufferFull  = errors.New("buffer is full")
	ErrBufferEmpty = errors.New("buffer is empty")
)

// RingBuffer is a thread-safe circular buffer.
type RingBuffer[T any] struct {
	buffer   []T
	head     int
	tail     int
	count    int
	capacity int
	mu       sync.RWMutex
}

// New creates a new RingBuffer with the given capacity.
func New[T any](capacity int) *RingBuffer[T] {
	return &RingBuffer[T]{
		buffer:   make([]T, capacity),
		capacity: capacity,
	}
}

// Write adds an item to the buffer.
func (rb *RingBuffer[T]) Write(item T) error {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	if rb.count == rb.capacity {
		return ErrBufferFull
	}

	rb.buffer[rb.tail] = item
	rb.tail = (rb.tail + 1) % rb.capacity
	rb.count++
	return nil
}

// Read removes and returns an item from the buffer.
func (rb *RingBuffer[T]) Read() (T, error) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	var zero T
	if rb.count == 0 {
		return zero, ErrBufferEmpty
	}

	item := rb.buffer[rb.head]
	rb.buffer[rb.head] = zero // Clear reference for GC
	rb.head = (rb.head + 1) % rb.capacity
	rb.count--
	return item, nil
}

// Peek returns the next item without removing it.
func (rb *RingBuffer[T]) Peek() (T, error) {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	var zero T
	if rb.count == 0 {
		return zero, ErrBufferEmpty
	}
	return rb.buffer[rb.head], nil
}

// Size returns the current number of items.
func (rb *RingBuffer[T]) Size() int {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	return rb.count
}

// IsEmpty returns true if the buffer is empty.
func (rb *RingBuffer[T]) IsEmpty() bool {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	return rb.count == 0
}

// IsFull returns true if the buffer is full.
func (rb *RingBuffer[T]) IsFull() bool {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	return rb.count == rb.capacity
}

// Clear empties the buffer.
func (rb *RingBuffer[T]) Clear() {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	var zero T
	for i := range rb.buffer {
		rb.buffer[i] = zero
	}
	rb.head = 0
	rb.tail = 0
	rb.count = 0
}
```

### RingBuffer with Overwrite

```go
package ringbuffer

import "sync"

// OverwriteRingBuffer overwrites old data when full.
type OverwriteRingBuffer[T any] struct {
	buffer   []T
	head     int
	tail     int
	full     bool
	capacity int
	mu       sync.RWMutex
}

// NewOverwrite creates a new overwriting ring buffer.
func NewOverwrite[T any](capacity int) *OverwriteRingBuffer[T] {
	return &OverwriteRingBuffer[T]{
		buffer:   make([]T, capacity),
		capacity: capacity,
	}
}

// Write adds an item, overwriting oldest if full.
func (orb *OverwriteRingBuffer[T]) Write(item T) {
	orb.mu.Lock()
	defer orb.mu.Unlock()

	orb.buffer[orb.tail] = item

	if orb.full {
		orb.head = (orb.head + 1) % orb.capacity
	}

	orb.tail = (orb.tail + 1) % orb.capacity
	orb.full = orb.tail == orb.head
}

// Read removes and returns an item from the buffer.
func (orb *OverwriteRingBuffer[T]) Read() (T, error) {
	orb.mu.Lock()
	defer orb.mu.Unlock()

	var zero T
	if orb.IsEmpty() {
		return zero, ErrBufferEmpty
	}

	item := orb.buffer[orb.head]
	orb.buffer[orb.head] = zero
	orb.head = (orb.head + 1) % orb.capacity
	orb.full = false
	return item, nil
}

// Size returns the current number of items.
func (orb *OverwriteRingBuffer[T]) Size() int {
	orb.mu.RLock()
	defer orb.mu.RUnlock()

	if orb.full {
		return orb.capacity
	}
	if orb.tail >= orb.head {
		return orb.tail - orb.head
	}
	return orb.capacity - orb.head + orb.tail
}

// IsEmpty returns true if the buffer is empty.
func (orb *OverwriteRingBuffer[T]) IsEmpty() bool {
	return !orb.full && orb.head == orb.tail
}

// Items returns all items from oldest to newest.
func (orb *OverwriteRingBuffer[T]) Items() []T {
	orb.mu.RLock()
	defer orb.mu.RUnlock()

	if orb.IsEmpty() {
		return nil
	}

	result := make([]T, 0, orb.Size())
	i := orb.head
	for {
		result = append(result, orb.buffer[i])
		i = (i + 1) % orb.capacity
		if i == orb.tail {
			break
		}
	}
	return result
}
```

---

## Use Cases

### 1. Audio/Video Buffer

```go
package audio

import "log/slog"

// AudioBuffer manages audio sample buffering.
type AudioBuffer struct {
	buffer *ringbuffer.RingBuffer[[]float32]
	logger *slog.Logger
}

// NewAudioBuffer creates a new audio buffer.
func NewAudioBuffer(size int, logger *slog.Logger) *AudioBuffer {
	return &AudioBuffer{
		buffer: ringbuffer.New[[]float32](size),
		logger: logger,
	}
}

// OnAudioData handles incoming audio samples.
func (ab *AudioBuffer) OnAudioData(samples []float32) {
	if err := ab.buffer.Write(samples); err != nil {
		ab.logger.Warn("audio buffer overflow", "error", err)
	}
}

// GetNextChunk returns the next audio chunk.
func (ab *AudioBuffer) GetNextChunk() ([]float32, error) {
	return ab.buffer.Read()
}
```

### 2. Log History

```go
package logging

import "time"

// LogEntry represents a log entry.
type LogEntry struct {
	Timestamp time.Time
	Level     string
	Message   string
}

// LogHistory maintains a rolling log history.
type LogHistory struct {
	logs *ringbuffer.OverwriteRingBuffer[LogEntry]
}

// NewLogHistory creates a new log history with given capacity.
func NewLogHistory(capacity int) *LogHistory {
	return &LogHistory{
		logs: ringbuffer.NewOverwrite[LogEntry](capacity),
	}
}

// Log adds a new log entry.
func (lh *LogHistory) Log(entry LogEntry) {
	lh.logs.Write(entry)
}

// GetRecentLogs returns all recent log entries.
func (lh *LogHistory) GetRecentLogs() []LogEntry {
	return lh.logs.Items()
}

// GetLastN returns the last N log entries.
func (lh *LogHistory) GetLastN(n int) []LogEntry {
	all := lh.logs.Items()
	if len(all) <= n {
		return all
	}
	return all[len(all)-n:]
}
```

### 3. Rolling Window Metrics

```go
package metrics

import "sort"

// RollingAverage calculates statistics over a rolling window.
type RollingAverage struct {
	samples *ringbuffer.OverwriteRingBuffer[float64]
}

// NewRollingAverage creates a new rolling average calculator.
func NewRollingAverage(windowSize int) *RollingAverage {
	return &RollingAverage{
		samples: ringbuffer.NewOverwrite[float64](windowSize),
	}
}

// AddSample adds a new sample value.
func (ra *RollingAverage) AddSample(value float64) {
	ra.samples.Write(value)
}

// GetAverage returns the average of all samples.
func (ra *RollingAverage) GetAverage() float64 {
	values := ra.samples.Items()
	if len(values) == 0 {
		return 0
	}

	var sum float64
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

// GetPercentile returns the Pth percentile.
func (ra *RollingAverage) GetPercentile(p float64) float64 {
	values := ra.samples.Items()
	if len(values) == 0 {
		return 0
	}

	sorted := make([]float64, len(values))
	copy(sorted, values)
	sort.Float64s(sorted)

	index := int(float64(len(sorted)) * p / 100.0)
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}
```

### 4. Limited Undo/Redo

```go
package undo

// LimitedUndoStack provides limited undo/redo functionality.
type LimitedUndoStack[T any] struct {
	undoBuffer *ringbuffer.OverwriteRingBuffer[T]
	redoBuffer *ringbuffer.OverwriteRingBuffer[T]
}

// NewLimitedUndoStack creates a new undo stack with given capacity.
func NewLimitedUndoStack[T any](capacity int) *LimitedUndoStack[T] {
	return &LimitedUndoStack[T]{
		undoBuffer: ringbuffer.NewOverwrite[T](capacity),
		redoBuffer: ringbuffer.NewOverwrite[T](capacity),
	}
}

// Push adds a new state to the undo stack.
func (lus *LimitedUndoStack[T]) Push(state T) {
	lus.undoBuffer.Write(state)
	lus.redoBuffer.Clear()
}

// Undo returns the previous state.
func (lus *LimitedUndoStack[T]) Undo(current T) (T, error) {
	previous, err := lus.undoBuffer.Read()
	if err != nil {
		var zero T
		return zero, err
	}
	lus.redoBuffer.Write(current)
	return previous, nil
}

// Redo returns the next state.
func (lus *LimitedUndoStack[T]) Redo(current T) (T, error) {
	next, err := lus.redoBuffer.Read()
	if err != nil {
		var zero T
		return zero, err
	}
	lus.undoBuffer.Write(current)
	return next, nil
}
```

---

## Variants

### Lock-free Ring Buffer (multi-thread)

```go
package ringbuffer

import "sync/atomic"

// LockFreeRingBuffer is a lock-free ring buffer using atomic operations.
type LockFreeRingBuffer[T any] struct {
	buffer   []T
	head     atomic.Uint64
	tail     atomic.Uint64
	capacity uint64
}

// NewLockFree creates a new lock-free ring buffer.
func NewLockFree[T any](capacity int) *LockFreeRingBuffer[T] {
	return &LockFreeRingBuffer[T]{
		buffer:   make([]T, capacity),
		capacity: uint64(capacity),
	}
}

// Write adds an item to the buffer.
func (lfb *LockFreeRingBuffer[T]) Write(item T) bool {
	currentTail := lfb.tail.Load()
	nextTail := (currentTail + 1) % lfb.capacity

	if nextTail == lfb.head.Load() {
		return false // Full
	}

	lfb.buffer[currentTail] = item
	lfb.tail.Store(nextTail)
	return true
}

// Read removes and returns an item.
func (lfb *LockFreeRingBuffer[T]) Read() (T, bool) {
	currentHead := lfb.head.Load()
	if currentHead == lfb.tail.Load() {
		var zero T
		return zero, false // Empty
	}

	item := lfb.buffer[currentHead]
	nextHead := (currentHead + 1) % lfb.capacity
	lfb.head.Store(nextHead)
	return item, true
}
```

---

## Complexity and Trade-offs

| Operation | Complexity |
|-----------|------------|
| write() | O(1) |
| read() | O(1) |
| peek() | O(1) |
| Memory | O(capacity) fixed |

### Advantages

- No dynamic allocation
- Predictable performance
- Ideal for real-time

### Disadvantages

- Fixed size (must be dimensioned)
- Data loss if overwrite

---

## When to Use

- Real-time data stream processing (audio, video, sensors)
- Logging system with limited retention (keep the last N logs)
- Metrics and statistics over sliding window (rolling average, percentiles)
- Producer-consumer communication with predictable fixed size
- Embedded or real-time applications requiring bounded memory

---

## Related Patterns

| Pattern | Relation |
|---------|----------|
| **Queue** | Ring buffer is an implementation |
| **Producer-Consumer** | Often uses a ring buffer |
| **Double Buffer** | Two alternating buffers vs circular |
| **Object Pool** | Similar memory management |

---

## Sources

- [Wikipedia - Circular Buffer](https://en.wikipedia.org/wiki/Circular_buffer)
- [LMAX Disruptor](https://lmax-exchange.github.io/disruptor/) - High-perf ring buffer
