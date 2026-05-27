# Scatter-Gather Pattern

> Distribute a request to multiple services in parallel and aggregate their responses according to a defined strategy.

## Overview

```
                         +-------------+
              +--------->| Service A   |--------+
              |          +-------------+        |
              |                                 v
+----------+  |          +-------------+    +------------+
| Request  |--+--------->| Service B   |--->| Aggregator |---> Response
+----------+  |          +-------------+    +------------+
              |                                 ^
              |          +-------------+        |
              +--------->| Service C   |--------+
                         +-------------+

           SCATTER                      GATHER
```

---

## Base Implementation

```go
package messaging

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

type ScatterGatherConfig struct {
	Destinations          []string
	Timeout               time.Duration
	MinResponses          int
	AggregationStrategy   string // "all", "first", "majority", "best"
}

type ScatterResult[T any] struct {
	Source    string
	Response  *T
	Error     error
	LatencyMs int64
}

type MessageChannel interface {
	Send(ctx context.Context, destination string, message interface{}) error
	Subscribe(ctx context.Context, queue string, handler func(msg interface{})) error
}

type ScatterGather[TRequest any, TResponse any] struct {
	config  *ScatterGatherConfig
	channel MessageChannel
}

func NewScatterGather[TRequest any, TResponse any](
	config *ScatterGatherConfig,
	channel MessageChannel,
) *ScatterGather[TRequest, TResponse] {
	return &ScatterGather[TRequest, TResponse]{
		config:  config,
		channel: channel,
	}
}

func (sg *ScatterGather[TRequest, TResponse]) Scatter(ctx context.Context, request TRequest) ([]ScatterResult[TResponse], error) {
	correlationID:= uuid.New().String()
	results:= make([]ScatterResult[TResponse], len(sg.config.Destinations))
	
	var wg sync.WaitGroup
	resultChan:= make(chan struct {
		index  int
		result ScatterResult[TResponse]
	}, len(sg.config.Destinations))

	ctx, cancel:= context.WithTimeout(ctx, sg.config.Timeout)
	defer cancel()

	for i, dest:= range sg.config.Destinations {
		idxCaptured:= i
		destCaptured:= dest
		wg.Go(func() {
			startTime:= time.Now()
			response, err:= sg.sendAndWait(ctx, destCaptured, request, correlationID)
			latency:= time.Since(startTime).Milliseconds()

			result:= ScatterResult[TResponse]{
				Source:    destCaptured,
				LatencyMs: latency,
			}

			if err != nil {
				result.Error = err
			} else {
				result.Response = response
			}

			select {
			case resultChan <- struct {
				index  int
				result ScatterResult[TResponse]
			}{idxCaptured, result}:
			case <-ctx.Done():
			}
		})
	}

	go func() {
		wg.Wait()
		close(resultChan)
	}()

	for r:= range resultChan {
		results[r.index] = r.result
	}

	return results, nil
}

func (sg *ScatterGather[TRequest, TResponse]) sendAndWait(
	ctx context.Context,
	destination string,
	request TRequest,
	correlationID string,
) (*TResponse, error) {
	responseChan:= make(chan *TResponse, 1)
	errorChan:= make(chan error, 1)
	replyQueue:= fmt.Sprintf("reply.%s", correlationID)

	// Setup temporary reply queue
	go func() {
		sg.channel.Subscribe(ctx, replyQueue, func(msg interface{}) {
			if response, ok:= msg.(*TResponse); ok {
				select {
				case responseChan <- response:
				case <-ctx.Done():
				}
			}
		})
	}()

	// Send request
	requestMsg:= struct {
		Payload       TRequest
		CorrelationID string
		ReplyTo       string
	}{
		Payload:       request,
		CorrelationID: correlationID,
		ReplyTo:       replyQueue,
	}

	if err:= sg.channel.Send(ctx, destination, requestMsg); err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}

	// Wait for response or timeout
	select {
	case response:= <-responseChan:
		return response, nil
	case err:= <-errorChan:
		return nil, err
	case <-ctx.Done():
		return nil, fmt.Errorf("timeout waiting for response from %s", destination)
	}
}
```

---

## Aggregation Strategies

```go
package messaging

import (
	"fmt"
	"sort"
)

type AggregationStrategy[T any, R any] func(results []ScatterResult[T]) (R, error)

type NoValidResponsesError struct{}

func (e *NoValidResponsesError) Error() string {
	return "no valid responses received"
}

type QuorumNotReachedError struct {
	Required int
	Received int
}

func (e *QuorumNotReachedError) Error() string {
	return fmt.Sprintf("quorum not reached: required %d, received %d", e.Required, e.Received)
}

// Best Price - retourne le meilleur resultat
type PriceQuote struct {
	SupplierID string
	Price      float64
	Available  bool
}

func BestPriceStrategy(results []ScatterResult[PriceQuote]) (PriceQuote, error) {
	validResults:= make([]PriceQuote, 0)
	for _, r:= range results {
		if r.Response != nil && r.Error == nil {
			validResults = append(validResults, *r.Response)
		}
	}

	if len(validResults) == 0 {
		return PriceQuote{}, &NoValidResponsesError{}
	}

	bestQuote:= validResults[0]
	for _, quote:= range validResults[1:] {
		if quote.Price < bestQuote.Price {
			bestQuote = quote
		}
	}

	return bestQuote, nil
}

// Combine All - agrege toutes les reponses
type SearchResult struct {
	Items []SearchItem
}

type SearchItem struct {
	ID    string
	Name  string
	Score float64
}

type CombinedResults struct {
	Items        []SearchItem
	Sources      []SourceInfo
	TotalResults int
}

type SourceInfo struct {
	Name      string
	Success   bool
	LatencyMs int64
}

func CombineAllStrategy(results []ScatterResult[SearchResult]) (CombinedResults, error) {
	var allItems []SearchItem
	sources:= make([]SourceInfo, len(results))

	for i, r:= range results {
		sources[i] = SourceInfo{
			Name:      r.Source,
			Success:   r.Error == nil,
			LatencyMs: r.LatencyMs,
		}

		if r.Response != nil {
			allItems = append(allItems, r.Response.Items...)
		}
	}

	return CombinedResults{
		Items:        allItems,
		Sources:      sources,
		TotalResults: len(allItems),
	}, nil
}

// Fastest - premier resultat valide
func FastestStrategy[T any](results []ScatterResult[T]) (*T, error) {
	sorted:= make([]ScatterResult[T], 0, len(results))
	for _, r:= range results {
		if r.Response != nil && r.Error == nil {
			sorted = append(sorted, r)
		}
	}

	if len(sorted) == 0 {
		return nil, &NoValidResponsesError{}
	}

	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].LatencyMs < sorted[j].LatencyMs
	})

	return sorted[0].Response, nil
}

// Quorum - majorite necessaire
func QuorumStrategy[T comparable](
	requiredVotes int,
	compareFn func(a, b T) bool,
) AggregationStrategy[T, T] {
	return func(results []ScatterResult[T]) (T, error) {
		var zero T
		validResults:= make([]T, 0)

		for _, r:= range results {
			if r.Response != nil && r.Error == nil {
				validResults = append(validResults, *r.Response)
			}
		}

		// Count votes for each unique response
		votes:= make(map[int]int)
		uniqueResults:= make([]T, 0)

		for _, result:= range validResults {
			found:= false
			for i, existing:= range uniqueResults {
				if compareFn(existing, result) {
					votes[i]++
					found = true
					break
				}
			}
			if !found {
				uniqueResults = append(uniqueResults, result)
				votes[len(uniqueResults)-1] = 1
			}
		}

		// Find quorum
		for i, result:= range uniqueResults {
			if votes[i] >= requiredVotes {
				return result, nil
			}
		}

		return zero, &QuorumNotReachedError{
			Required: requiredVotes,
			Received: len(validResults),
		}
	}
}
```

---

## Example: Price Comparator

```go
package messaging

import (
	"context"
	"fmt"
	"sort"
	"time"
)

type PriceRequest struct {
	ProductID string
	Quantity  int
}

type FullPriceQuote struct {
	SupplierID   string
	ProductID    string
	UnitPrice    float64
	TotalPrice   float64
	Currency     string
	ValidUntil   time.Time
	InStock      bool
	DeliveryDays int
}

type PriceComparisonResult struct {
	Cheapest      FullPriceQuote
	Fastest       FullPriceQuote
	AllQuotes     []FullPriceQuote
	SupplierStats []SupplierStat
}

type SupplierStat struct {
	Supplier  string
	Responded bool
	LatencyMs int64
	Error     string
}

type NoAvailableSupplierError struct {
	ProductID string
}

func (e *NoAvailableSupplierError) Error() string {
	return fmt.Sprintf("no available supplier for product %s", e.ProductID)
}

type PriceComparator struct {
	scatterGather *ScatterGather[PriceRequest, FullPriceQuote]
}

func NewPriceComparator(suppliers []string, channel MessageChannel) *PriceComparator {
	config:= &ScatterGatherConfig{
		Destinations:        suppliers,
		Timeout:             5 * time.Second,
		MinResponses:        1,
		AggregationStrategy: "all",
	}

	return &PriceComparator{
		scatterGather: NewScatterGather[PriceRequest, FullPriceQuote](config, channel),
	}
}

func (pc *PriceComparator) GetBestPrice(ctx context.Context, request PriceRequest) (*PriceComparisonResult, error) {
	results, err:= pc.scatterGather.Scatter(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("scatter-gather failed: %w", err)
	}

	validQuotes:= make([]FullPriceQuote, 0)
	for _, r:= range results {
		if r.Response != nil && r.Response.InStock {
			validQuotes = append(validQuotes, *r.Response)
		}
	}

	if len(validQuotes) == 0 {
		return nil, &NoAvailableSupplierError{ProductID: request.ProductID}
	}

	sortedByPrice:= make([]FullPriceQuote, len(validQuotes))
	copy(sortedByPrice, validQuotes)
	sort.Slice(sortedByPrice, func(i, j int) bool {
		return sortedByPrice[i].TotalPrice < sortedByPrice[j].TotalPrice
	})

	sortedByDelivery:= make([]FullPriceQuote, len(validQuotes))
	copy(sortedByDelivery, validQuotes)
	sort.Slice(sortedByDelivery, func(i, j int) bool {
		return sortedByDelivery[i].DeliveryDays < sortedByDelivery[j].DeliveryDays
	})

	supplierStats:= make([]SupplierStat, len(results))
	for i, r:= range results {
		stat:= SupplierStat{
			Supplier:  r.Source,
			Responded: r.Response != nil,
			LatencyMs: r.LatencyMs,
		}
		if r.Error != nil {
			stat.Error = r.Error.Error()
		}
		supplierStats[i] = stat
	}

	return &PriceComparisonResult{
		Cheapest:      sortedByPrice[0],
		Fastest:       sortedByDelivery[0],
		AllQuotes:     sortedByPrice,
		SupplierStats: supplierStats,
	}, nil
}
```

---

## With RabbitMQ/Kafka

```go
package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/segmentio/kafka-go"
)

// RabbitMQ - Direct Reply-To
type RabbitMQScatterGather struct {
	channel *amqp.Channel
}

func NewRabbitMQScatterGather(channel *amqp.Channel) *RabbitMQScatterGather {
	return &RabbitMQScatterGather{channel: channel}
}

func (r *RabbitMQScatterGather) Scatter(
	ctx context.Context,
	destinations []string,
	request interface{},
) ([]interface{}, error) {
	correlationID:= uuid.New().String()
	results:= make([]interface{}, 0)
	resultChan:= make(chan interface{}, len(destinations))
	var mu sync.Mutex

	// Consumer sur amq.rabbitmq.reply-to
	msgs, err:= r.channel.Consume(
		"amq.rabbitmq.reply-to",
		"",
		true,  // auto-ack
		false, // exclusive
		false, // no-local
		false, // no-wait
		nil,   // args
	)
	if err != nil {
		return nil, fmt.Errorf("consuming reply queue: %w", err)
	}

	go func() {
		for msg:= range msgs {
			if msg.CorrelationId == correlationID {
				var result interface{}
				json.Unmarshal(msg.Body, &result)
				resultChan <- result
			}
		}
	}()

	// Envoyer a toutes les destinations
	requestBytes, _:= json.Marshal(request)
	for _, dest:= range destinations {
		err:= r.channel.PublishWithContext(
			ctx,
			"",   // exchange
			dest, // routing key
			false,
			false,
			amqp.Publishing{
				CorrelationId: correlationID,
				ReplyTo:       "amq.rabbitmq.reply-to",
				Body:          requestBytes,
			},
		)
		if err != nil {
			return nil, fmt.Errorf("publishing to %s: %w", dest, err)
		}
	}

	// Collect results with timeout
	timeout:= time.After(5 * time.Second)
	for i:= 0; i < len(destinations); i++ {
		select {
		case result:= <-resultChan:
			mu.Lock()
			results = append(results, result)
			mu.Unlock()
		case <-timeout:
			return results, nil
		case <-ctx.Done():
			return results, ctx.Err()
		}
	}

	return results, nil
}

// Kafka - Request-Reply avec topic temporaire
type KafkaScatterGather struct {
	producer *kafka.Writer
	admin    *kafka.Client
}

func NewKafkaScatterGather(brokers []string) *KafkaScatterGather {
	return &KafkaScatterGather{
		producer: &kafka.Writer{
			Addr:     kafka.TCP(brokers...),
			Balancer: &kafka.LeastBytes{},
		},
		admin: &kafka.Client{
			Addr: kafka.TCP(brokers...),
		},
	}
}

func (k *KafkaScatterGather) Scatter(
	ctx context.Context,
	groupTopics []string,
	request interface{},
) ([]interface{}, error) {
	correlationID:= uuid.New().String()
	replyTopic:= fmt.Sprintf("replies.%s", correlationID)

	// Create temporary topic
	_, err:= k.admin.CreateTopics(ctx, &kafka.CreateTopicsRequest{
		Topics: []kafka.TopicConfig{
			{
				Topic:             replyTopic,
				NumPartitions:     1,
				ReplicationFactor: 1,
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("creating reply topic: %w", err)
	}

	defer func() {
		k.admin.DeleteTopics(ctx, &kafka.DeleteTopicsRequest{
			Topics: []string{replyTopic},
		})
	}()

	// Consumer for responses
	reader:= kafka.NewReader(kafka.ReaderConfig{
		Brokers: k.admin.Addr.Network(),
		Topic:   replyTopic,
		GroupID: correlationID,
	})
	defer reader.Close()

	results:= make([]interface{}, 0, len(groupTopics))
	resultChan:= make(chan interface{}, len(groupTopics))

	go func() {
		for i:= 0; i < len(groupTopics); i++ {
			msg, err:= reader.ReadMessage(ctx)
			if err != nil {
				return
			}
			var result interface{}
			json.Unmarshal(msg.Value, &result)
			resultChan <- result
		}
	}()

	// Send requests
	requestBytes, _:= json.Marshal(request)
	messages:= make([]kafka.Message, len(groupTopics))
	for i, topic:= range groupTopics {
		messages[i] = kafka.Message{
			Key:   []byte(correlationID),
			Value: requestBytes,
			Headers: []kafka.Header{
				{Key: "reply-topic", Value: []byte(replyTopic)},
				{Key: "target-topic", Value: []byte(topic)},
			},
		}
	}

	if err:= k.producer.WriteMessages(ctx, messages...); err != nil {
		return nil, fmt.Errorf("writing messages: %w", err)
	}

	// Collect results with timeout
	timeout:= time.After(5 * time.Second)
	for i:= 0; i < len(groupTopics); i++ {
		select {
		case result:= <-resultChan:
			results = append(results, result)
		case <-timeout:
			return results, nil
		case <-ctx.Done():
			return results, ctx.Err()
		}
	}

	return results, nil
}
```

---

## Error Cases

```go
package messaging

import (
	"context"
	"fmt"
)

type CircuitBreaker interface {
	IsOpen(destination string) bool
	RecordFailure(destination string)
	RecordSuccess(destination string)
}

type AllCircuitsOpenError struct{}

func (e *AllCircuitsOpenError) Error() string {
	return "all circuit breakers are open"
}

type ResilientScatterGather[T any, R any] struct {
	*ScatterGather[T, R]
	circuitBreaker CircuitBreaker
}

func NewResilientScatterGather[T any, R any](
	config *ScatterGatherConfig,
	channel MessageChannel,
	circuitBreaker CircuitBreaker,
) *ResilientScatterGather[T, R] {
	return &ResilientScatterGather[T, R]{
		ScatterGather:  NewScatterGather[T, R](config, channel),
		circuitBreaker: circuitBreaker,
	}
}

func (r *ResilientScatterGather[T, R]) ScatterWithFallback(
	ctx context.Context,
	request T,
	fallbackFn func() R,
) (R, error) {
	var zero R

	results, err:= r.Scatter(ctx, request)
	if err != nil {
		return fallbackFn(), nil
	}

	validCount:= 0
	for _, result:= range results {
		if result.Response != nil && result.Error == nil {
			validCount++
		}
	}

	if validCount < r.config.MinResponses {
		fmt.Printf("Insufficient responses: %d/%d\n", validCount, r.config.MinResponses)
		return fallbackFn(), nil
	}

	// Aggregate results using configured strategy
	return zero, nil
}

func (r *ResilientScatterGather[T, R]) ScatterWithCircuitBreaker(
	ctx context.Context,
	request T,
) ([]ScatterResult[R], error) {
	// Filter destinations with open circuit
	healthyDestinations:= make([]string, 0)
	for _, dest:= range r.config.Destinations {
		if !r.circuitBreaker.IsOpen(dest) {
			healthyDestinations = append(healthyDestinations, dest)
		}
	}

	if len(healthyDestinations) == 0 {
		return nil, &AllCircuitsOpenError{}
	}

	// Create temporary config with healthy destinations
	tempConfig:= *r.config
	tempConfig.Destinations = healthyDestinations
	tempSG:= NewScatterGather[T, R](&tempConfig, r.channel)

	results, err:= tempSG.Scatter(ctx, request)
	if err != nil {
		return nil, err
	}

	// Update circuit breakers
	for _, result:= range results {
		if result.Error != nil {
			r.circuitBreaker.RecordFailure(result.Source)
		} else {
			r.circuitBreaker.RecordSuccess(result.Source)
		}
	}

	return results, nil
}
```

---

## When to Use

- Price comparison between multiple suppliers
- Federated search across multiple sources
- Data aggregation from multiple microservices
- Parallel queries with consolidation
- Voting or consensus systems

## Related Patterns

- [Splitter-Aggregator](./splitter-aggregator.md) - Split/recombine
- [Circuit Breaker](../cloud/circuit-breaker.md) - Failure protection
- [Timeout](../resilience/timeout.md) - Limit waiting
- [Producer-Consumer](../concurrency/producer-consumer.md) - Scale consumers
