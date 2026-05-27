# Message Channel Patterns

Communication channel patterns for messaging.

## Overview

```
+-------------------+     +-------------------+
|    Producer A     |     |    Producer B     |
+--------+----------+     +--------+----------+
         |                         |
         v                         v
+------------------------------------------+
|              MESSAGE CHANNEL              |
|  +------------------------------------+  |
|  |  Point-to-Point  |  Pub/Sub        |  |
|  |    [Queue]       |   [Topic]       |  |
|  +------------------------------------+  |
+------------------------------------------+
         |                         |
         v                         v
+--------+----------+     +--------+----------+
|   Consumer 1      |     |   Consumer 2      |
+-------------------+     +-------------------+
```

---

## Point-to-Point Channel

> A message is consumed by exactly one consumer.

### Schema

```
Producer ---> [  Queue  ] ---> Consumer A
                   |
                   X (not delivered to Consumer B)
```

### RabbitMQ/Kafka Implementation

```go
package messaging

import (
	"context"
	"encoding/json"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
)

type PointToPointConfig struct {
	Queue      string
	Durable    bool
	Exclusive  bool
	AutoDelete bool
}

type PointToPointChannel struct {
	channel *amqp.Channel
	config  *PointToPointConfig
}

func NewPointToPointChannel(channel *amqp.Channel, config *PointToPointConfig) *PointToPointChannel {
	return &PointToPointChannel{
		channel: channel,
		config:  config,
	}
}

func (p *PointToPointChannel) Send(ctx context.Context, message interface{}) error {
	_, err:= p.channel.QueueDeclare(
		p.config.Queue,
		p.config.Durable,
		p.config.AutoDelete,
		p.config.Exclusive,
		false, // no-wait
		nil,   // arguments
	)
	if err != nil {
		return fmt.Errorf("declaring queue: %w", err)
	}

	body, err:= json.Marshal(message)
	if err != nil {
		return fmt.Errorf("marshaling message: %w", err)
	}

	return p.channel.PublishWithContext(
		ctx,
		"",              // exchange
		p.config.Queue,  // routing key
		false,           // mandatory
		false,           // immediate
		amqp.Publishing{
			DeliveryMode: amqp.Persistent,
			ContentType:  "application/json",
			Body:         body,
		},
	)
}

type MessageHandler func(ctx context.Context, msg interface{}) error

func (p *PointToPointChannel) Consume(ctx context.Context, handler MessageHandler) error {
	msgs, err:= p.channel.Consume(
		p.config.Queue,
		"",    // consumer
		false, // auto-ack
		false, // exclusive
		false, // no-local
		false, // no-wait
		nil,   // args
	)
	if err != nil {
		return fmt.Errorf("consuming queue: %w", err)
	}

	go func() {
		for d:= range msgs {
			select {
			case <-ctx.Done():
				return
			default:
				var content interface{}
				if err:= json.Unmarshal(d.Body, &content); err != nil {
					d.Nack(false, true)
					continue
				}

				if err:= handler(ctx, content); err != nil {
					// Requeue on failure
					d.Nack(false, true)
					continue
				}

				d.Ack(false)
			}
		}
	}()

	return nil
}

// Kafka - Consumer Group (simule P2P)
type KafkaPointToPoint struct {
	reader *kafka.Reader
}

func NewKafkaPointToPoint(brokers []string, topic, groupID string) *KafkaPointToPoint {
	return &KafkaPointToPoint{
		reader: kafka.NewReader(kafka.ReaderConfig{
			Brokers: brokers,
			Topic:   topic,
			GroupID: groupID,
		}),
	}
}

func (k *KafkaPointToPoint) Consume(ctx context.Context, handler MessageHandler) error {
	// Each message goes to a single consumer in the group
	for {
		msg, err:= k.reader.FetchMessage(ctx)
		if err != nil {
			return fmt.Errorf("fetching message: %w", err)
		}

		var payload interface{}
		if err:= json.Unmarshal(msg.Value, &payload); err != nil {
			k.reader.CommitMessages(ctx, msg)
			continue
		}

		if err:= handler(ctx, payload); err != nil {
			// Handle error
			continue
		}

		k.reader.CommitMessages(ctx, msg)
	}
}
```

### Error Cases

```go
package messaging

import (
	"context"
	"fmt"
	"math"
	"time"
)

type MaxRetriesExceededError struct {
	MessageID string
}

func (e *MaxRetriesExceededError) Error() string {
	return fmt.Sprintf("max retries exceeded for message %s", e.MessageID)
}

type ResilientP2PChannel struct {
	channel          *PointToPointChannel
	retryCount       int
	deadLetterQueue  string
}

func NewResilientP2PChannel(channel *PointToPointChannel, deadLetterQueue string) *ResilientP2PChannel {
	return &ResilientP2PChannel{
		channel:         channel,
		retryCount:      3,
		deadLetterQueue: deadLetterQueue,
	}
}

func (r *ResilientP2PChannel) ProcessWithRetry(ctx context.Context, message interface{}, handler MessageHandler) error {
	var lastErr error
	
	for attempts:= 0; attempts < r.retryCount; attempts++ {
		if err:= handler(ctx, message); err == nil {
			return nil
		} else {
			lastErr = err
			// Exponential backoff
			backoff:= time.Duration(math.Pow(2, float64(attempts))) * time.Second
			time.Sleep(backoff)
		}
	}

	// Max retries exceeded, send to dead letter queue
	if err:= r.sendToDeadLetter(ctx, message, lastErr); err != nil {
		return fmt.Errorf("sending to dead letter: %w", err)
	}

	return &MaxRetriesExceededError{MessageID: fmt.Sprintf("%v", message)}
}

func (r *ResilientP2PChannel) sendToDeadLetter(ctx context.Context, message interface{}, err error) error {
	// Implementation depends on message broker
	return nil
}
```

**When:** Work queues, job processing, commands.
**Related to:** Competing Consumers, Dead Letter Channel.

---

## Publish-Subscribe Channel

> A message is sent to all active subscribers.

### Pub-Sub Schema

```
Producer ---> [ Topic/Exchange ] ---> Subscriber A
                     |
                     +--------------> Subscriber B
                     |
                     +--------------> Subscriber C
```

### Implementation

```go
package messaging

import (
	"context"
	"encoding/json"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/segmentio/kafka-go"
)

// RabbitMQ - Fanout Exchange
type PubSubChannel struct {
	channel  *amqp.Channel
	exchange string
}

func NewPubSubChannel(channel *amqp.Channel, exchange string) *PubSubChannel {
	return &PubSubChannel{
		channel:  channel,
		exchange: exchange,
	}
}

func (p *PubSubChannel) Publish(ctx context.Context, event interface{}) error {
	if err:= p.channel.ExchangeDeclare(
		p.exchange,
		"fanout", // type
		true,     // durable
		false,    // auto-deleted
		false,    // internal
		false,    // no-wait
		nil,      // arguments
	); err != nil {
		return fmt.Errorf("declaring exchange: %w", err)
	}

	body, err:= json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshaling event: %w", err)
	}

	return p.channel.PublishWithContext(
		ctx,
		p.exchange,
		"",    // routing key ignored for fanout
		false, // mandatory
		false, // immediate
		amqp.Publishing{
			ContentType: "application/json",
			Body:        body,
		},
	)
}

func (p *PubSubChannel) Subscribe(ctx context.Context, handler MessageHandler) error {
	// Each subscriber has its own queue
	q, err:= p.channel.QueueDeclare(
		"",    // name (empty = auto-generated)
		false, // durable
		false, // delete when unused
		true,  // exclusive
		false, // no-wait
		nil,   // arguments
	)
	if err != nil {
		return fmt.Errorf("declaring queue: %w", err)
	}

	if err:= p.channel.QueueBind(
		q.Name,
		"",          // routing key
		p.exchange,
		false,
		nil,
	); err != nil {
		return fmt.Errorf("binding queue: %w", err)
	}

	msgs, err:= p.channel.Consume(
		q.Name,
		"",    // consumer
		false, // auto-ack
		false, // exclusive
		false, // no-local
		false, // no-wait
		nil,   // args
	)
	if err != nil {
		return fmt.Errorf("consuming queue: %w", err)
	}

	go func() {
		for d:= range msgs {
			select {
			case <-ctx.Done():
				return
			default:
				var event interface{}
				if err:= json.Unmarshal(d.Body, &event); err != nil {
					d.Nack(false, false)
					continue
				}

				if err:= handler(ctx, event); err != nil {
					d.Nack(false, false)
					continue
				}

				d.Ack(false)
			}
		}
	}()

	return nil
}

// Kafka - Topic avec multiple consumer groups
type KafkaPubSub struct {
	writer *kafka.Writer
}

func NewKafkaPubSub(brokers []string, topic string) *KafkaPubSub {
	return &KafkaPubSub{
		writer: &kafka.Writer{
			Addr:     kafka.TCP(brokers...),
			Topic:    topic,
			Balancer: &kafka.LeastBytes{},
		},
	}
}

func (k *KafkaPubSub) Publish(ctx context.Context, event interface{}) error {
	body, err:= json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshaling event: %w", err)
	}

	return k.writer.WriteMessages(ctx, kafka.Message{
		Value: body,
	})
}

// Each service uses a different groupID
func (k *KafkaPubSub) Subscribe(ctx context.Context, brokers []string, topic, groupID string, handler MessageHandler) error {
	reader:= kafka.NewReader(kafka.ReaderConfig{
		Brokers:      brokers,
		Topic:        topic,
		GroupID:      groupID,
		StartOffset:  kafka.LastOffset,
	})
	defer reader.Close()

	for {
		msg, err:= reader.FetchMessage(ctx)
		if err != nil {
			return fmt.Errorf("fetching message: %w", err)
		}

		var event interface{}
		if err:= json.Unmarshal(msg.Value, &event); err != nil {
			reader.CommitMessages(ctx, msg)
			continue
		}

		if err:= handler(ctx, event); err != nil {
			// Handle error
			continue
		}

		reader.CommitMessages(ctx, msg)
	}
}
```

### Topic Filtering

```go
package messaging

import (
	"context"
	"encoding/json"
)

// RabbitMQ - Topic Exchange avec routing keys
type TopicPubSub struct {
	channel *amqp.Channel
}

func NewTopicPubSub(channel *amqp.Channel) *TopicPubSub {
	return &TopicPubSub{channel: channel}
}

func (t *TopicPubSub) Publish(ctx context.Context, routingKey string, event interface{}) error {
	if err:= t.channel.ExchangeDeclare(
		"events",
		"topic", // type
		true,    // durable
		false,   // auto-deleted
		false,   // internal
		false,   // no-wait
		nil,     // arguments
	); err != nil {
		return err
	}

	body, _:= json.Marshal(event)
	return t.channel.PublishWithContext(
		ctx,
		"events",
		routingKey,
		false,
		false,
		amqp.Publishing{
			ContentType: "application/json",
			Body:        body,
		},
	)
}

func (t *TopicPubSub) Subscribe(ctx context.Context, pattern string, handler MessageHandler) error {
	q, err:= t.channel.QueueDeclare("", false, false, true, false, nil)
	if err != nil {
		return err
	}

	// Pattern: orders.* or orders.# or orders.created
	if err:= t.channel.QueueBind(q.Name, pattern, "events", false, nil); err != nil {
		return err
	}

	msgs, err:= t.channel.Consume(q.Name, "", false, false, false, false, nil)
	if err != nil {
		return err
	}

	go func() {
		for d:= range msgs {
			var event interface{}
			json.Unmarshal(d.Body, &event)
			handler(ctx, event)
			d.Ack(false)
		}
	}()

	return nil
}

// Usage
func ExampleTopicPubSub() {
	// pubsub.Subscribe("orders.created", handleOrderCreated)
	// pubsub.Subscribe("orders.*", handleAllOrderEvents)
	// pubsub.Subscribe("orders.#", handleOrdersAndSubtopics)
}
```

### Pub-Sub Error Handling

```go
package messaging

import (
	"context"
	"fmt"
)

type SubscriptionStore interface {
	GetLastOffset(ctx context.Context, subscriberID string) (int64, error)
	SaveOffset(ctx context.Context, subscriberID string, offset int64) error
}

type ErrorHandler interface {
	Handle(ctx context.Context, event interface{}, err error) error
}

type ReliablePubSub struct {
	channel           *PubSubChannel
	subscriptionStore SubscriptionStore
	errorHandler      ErrorHandler
}

func NewReliablePubSub(
	channel *PubSubChannel,
	subscriptionStore SubscriptionStore,
	errorHandler ErrorHandler,
) *ReliablePubSub {
	return &ReliablePubSub{
		channel:           channel,
		subscriptionStore: subscriptionStore,
		errorHandler:      errorHandler,
	}
}

func (r *ReliablePubSub) SubscribeWithRecovery(
	ctx context.Context,
	subscriberID string,
	handler MessageHandler,
) error {
	// Save the reading position
	lastOffset, err:= r.subscriptionStore.GetLastOffset(ctx, subscriberID)
	if err != nil {
		return fmt.Errorf("getting last offset: %w", err)
	}

	fmt.Printf("Starting from offset: %d\n", lastOffset)

	return r.channel.Subscribe(ctx, func(ctx context.Context, event interface{}) error {
		// Extract offset from message (implementation depends on broker)
		var offset int64 = 0

		if err:= handler(ctx, event); err != nil {
			// Log but continue to avoid blocking others
			fmt.Printf("Failed to process event at %d: %v\n", offset, err)
			return r.errorHandler.Handle(ctx, event, err)
		}

		return r.subscriptionStore.SaveOffset(ctx, subscriberID, offset)
	})
}
```

**When:** Events, notifications, broadcasting, decoupling.
**Related to:** Observer, Event-Driven Architecture.

---

## Decision Table

| Characteristic | Point-to-Point | Publish-Subscribe |
|-----------------|----------------|-------------------|
| Recipients | Only one | All subscribers |
| Use Case | Commands, Jobs | Events, Notifications |
| Guarantee | Exactly one processing | Each subscriber receives |
| Scaling | Competing consumers | Multiple groups |
| Coupling | Stronger | Weaker |

---

## When to Use

- Decoupling between message producers and consumers
- Asynchronous message distribution between services
- Need for delivery guarantees (at-least-once, at-most-once, exactly-once)
- Broadcasting events to multiple subscribers
- Load balancing messages between multiple consumers

## Related Patterns

- [Dead Letter Channel](./dead-letter.md) - Failed message handling
- [Message Router](./message-router.md) - Dynamic message routing
- [Idempotent Receiver](./idempotent-receiver.md) - Idempotent message processing
- [Transactional Outbox](./transactional-outbox.md) - Publication reliability

## Complementary Patterns

- **Competing Consumers** - Scale P2P horizontally
- **Durable Subscriber** - Pub/Sub with persistence
- **Message Filter** - Filter received messages
- **Dead Letter Channel** - Handle failures
