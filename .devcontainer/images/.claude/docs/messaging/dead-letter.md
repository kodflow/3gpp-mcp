# Dead Letter Channel Pattern

> Handle unprocessable messages via a dedicated queue to capture messages that fail after multiple processing attempts.

## Overview

```
+----------+     +-------------+     +-----------+
| Producer |---->|    Queue    |---->| Consumer  |
+----------+     +------+------+     +-----+-----+
                        |                  |
                        |            FAIL (x3)
                        |                  |
                        v                  v
                 +------+------+    +------+------+
                 |   Expired   |    |   Rejected  |
                 +------+------+    +------+------+
                        |                  |
                        +--------+---------+
                                 |
                                 v
                        +--------+--------+
                        | Dead Letter Queue|
                        |   (DLQ)          |
                        +--------+--------+
                                 |
                                 v
                        +--------+--------+
                        | DLQ Consumer    |
                        | - Alert         |
                        | - Log           |
                        | - Retry         |
                        | - Archive       |
                        +-----------------+
```

---

## Base Implementation

```go
package messaging

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type DeadLetterMessage struct {
	ID              string            `json:"id"`
	OriginalQueue   string            `json:"originalQueue"`
	OriginalMessage interface{}       `json:"originalMessage"`
	Error           ErrorInfo         `json:"error"`
	Attempts        int               `json:"attempts"`
	FirstFailedAt   time.Time         `json:"first_failed_at"`
	LastFailedAt    time.Time         `json:"lastFailedAt"`
	Headers         map[string]string `json:"headers"`
}

type ErrorInfo struct {
	Name    string `json:"name"`
	Message string `json:"message"`
	Stack   string `json:"stack,omitempty"`
}

type AlertService interface {
	Critical(ctx context.Context, message string, details map[string]interface{}) error
}

type MessageQueue interface {
	Send(ctx context.Context, queue string, message interface{}) error
}

type DeadLetterChannel struct {
	dlq          MessageQueue
	alertService AlertService
}

func NewDeadLetterChannel(dlq MessageQueue, alertService AlertService) *DeadLetterChannel {
	return &DeadLetterChannel{
		dlq:          dlq,
		alertService: alertService,
	}
}

func (d *DeadLetterChannel) Send(ctx context.Context, originalQueue string, message interface{}, err error, attempts int) error {
	dlMessage:= &DeadLetterMessage{
		ID:              uuid.New().String(),
		OriginalQueue:   originalQueue,
		OriginalMessage: message,
		Error: ErrorInfo{
			Name:    fmt.Sprintf("%T", err),
			Message: err.Error(),
		},
		Attempts:      attempts,
		FirstFailedAt: time.Now(),
		LastFailedAt:  time.Now(),
		Headers:       extractHeaders(message),
	}

	if err:= d.dlq.Send(ctx, "dead-letter-queue", dlMessage); err != nil {
		return fmt.Errorf("sending to DLQ: %w", err)
	}

	// Alert on critical error
	if d.isCriticalError(err) {
		alertErr:= d.alertService.Critical(ctx, "Message moved to DLQ", map[string]interface{}{
			"queue":     originalQueue,
			"error":     err.Error(),
			"messageId": dlMessage.ID,
		})
		if alertErr != nil {
			return fmt.Errorf("sending alert: %w", alertErr)
		}
	}

	return nil
}

func (d *DeadLetterChannel) isCriticalError(err error) bool {
	_, isPayment:= err.(*PaymentError)
	_, isDataCorruption:= err.(*DataCorruptionError)
	_, isSecurity:= err.(*SecurityError)
	return isPayment || isDataCorruption || isSecurity
}

func extractHeaders(message interface{}) map[string]string {
	type withHeaders interface {
		GetHeaders() map[string]string
	}
	if msg, ok:= message.(withHeaders); ok {
		return msg.GetHeaders()
	}
	return make(map[string]string)
}

// Custom error types
type PaymentError struct{ error }
type DataCorruptionError struct{ error }
type SecurityError struct{ error }
```

---

## Consumer with Retry and DLQ

```go
package messaging

import (
	"context"
	"fmt"
	"math"
	"time"
)

type RetryConfig struct {
	MaxRetries         int
	BackoffMultiplier  float64
	InitialDelayMs     int
	MaxDelayMs         int
}

type MessageMeta struct {
	Queue   string
	Headers map[string]string
}

type MessageHandler func(ctx context.Context, msg interface{}) error

type ResilientConsumer struct {
	queue       MessageQueue
	handler     MessageHandler
	deadLetter  *DeadLetterChannel
	retryConfig RetryConfig
}

func NewResilientConsumer(queue MessageQueue, handler MessageHandler, deadLetter *DeadLetterChannel) *ResilientConsumer {
	return &ResilientConsumer{
		queue:      queue,
		handler:    handler,
		deadLetter: deadLetter,
		retryConfig: RetryConfig{
			MaxRetries:        3,
			BackoffMultiplier: 2,
			InitialDelayMs:    1000,
			MaxDelayMs:        30000,
		},
	}
}

func (rc *ResilientConsumer) Consume(ctx context.Context, messages <-chan Message) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg:= <-messages:
			if err:= rc.processMessage(ctx, msg); err != nil {
				return fmt.Errorf("processing message: %w", err)
			}
		}
	}
}

type Message struct {
	Content interface{}
	Meta    MessageMeta
}

func (rc *ResilientConsumer) processMessage(ctx context.Context, msg Message) error {
	attempts:= rc.getRetryCount(msg.Meta.Headers)

	if err:= rc.handler(ctx, msg.Content); err != nil {
		if attempts >= rc.retryConfig.MaxRetries {
			// Max retries reached -> DLQ
			return rc.deadLetter.Send(ctx, msg.Meta.Queue, msg.Content, err, attempts)
		}

		// Requeue with delay
		delay:= rc.calculateDelay(attempts)
		return rc.requeueWithDelay(ctx, msg.Content, attempts+1, delay)
	}

	return nil
}

func (rc *ResilientConsumer) calculateDelay(attempts int) time.Duration {
	delay:= float64(rc.retryConfig.InitialDelayMs) * math.Pow(rc.retryConfig.BackoffMultiplier, float64(attempts))
	maxDelay:= float64(rc.retryConfig.MaxDelayMs)
	if delay > maxDelay {
		delay = maxDelay
	}
	return time.Duration(delay) * time.Millisecond
}

func (rc *ResilientConsumer) requeueWithDelay(ctx context.Context, message interface{}, attempts int, delay time.Duration) error {
	time.Sleep(delay)
	headers:= map[string]string{
		"x-retry-count": fmt.Sprintf("%d", attempts),
	}
	return rc.queue.Send(ctx, "retry-queue", struct {
		Message interface{}
		Headers map[string]string
	}{message, headers})
}

func (rc *ResilientConsumer) getRetryCount(headers map[string]string) int {
	if count, ok:= headers["x-retry-count"]; ok {
		var attempts int
		fmt.Sscanf(count, "%d", &attempts)
		return attempts
	}
	return 0
}
```

---

## RabbitMQ Dead Letter Configuration

```go
package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

type RabbitMQDeadLetterSetup struct {
	channel *amqp.Channel
}

func NewRabbitMQDeadLetterSetup(channel *amqp.Channel) *RabbitMQDeadLetterSetup {
	return &RabbitMQDeadLetterSetup{channel: channel}
}

func (r *RabbitMQDeadLetterSetup) Setup(ctx context.Context) error {
	// Dead Letter Exchange
	if err:= r.channel.ExchangeDeclare(
		"dlx",     // name
		"direct",  // type
		true,      // durable
		false,     // auto-deleted
		false,     // internal
		false,     // no-wait
		nil,       // arguments
	); err != nil {
		return fmt.Errorf("declaring DLX: %w", err)
	}

	// Dead Letter Queue
	args:= amqp.Table{
		"x-message-ttl": int32(7 * 24 * 60 * 60 * 1000), // 7 jours
	}
	if _, err:= r.channel.QueueDeclare(
		"dead-letter-queue", // name
		true,                // durable
		false,               // delete when unused
		false,               // exclusive
		false,               // no-wait
		args,                // arguments
	); err != nil {
		return fmt.Errorf("declaring DLQ: %w", err)
	}

	if err:= r.channel.QueueBind(
		"dead-letter-queue", // queue name
		"dead-letter",       // routing key
		"dlx",               // exchange
		false,
		nil,
	); err != nil {
		return fmt.Errorf("binding DLQ: %w", err)
	}

	// Main queue with DLX configured
	args = amqp.Table{
		"x-dead-letter-exchange":    "dlx",
		"x-dead-letter-routing-key": "dead-letter",
	}
	if _, err:= r.channel.QueueDeclare(
		"orders",
		true,
		false,
		false,
		false,
		args,
	); err != nil {
		return fmt.Errorf("declaring orders queue: %w", err)
	}

	return nil
}

type DeathInfo struct {
	Queue string    `json:"queue"`
	Reason string   `json:"reason"`
	Count  int      `json:"count"`
	Time   time.Time `json:"time"`
}

func (r *RabbitMQDeadLetterSetup) ConsumeDeadLetters(ctx context.Context) error {
	msgs, err:= r.channel.Consume(
		"dead-letter-queue",
		"",    // consumer
		false, // auto-ack
		false, // exclusive
		false, // no-local
		false, // no-wait
		nil,   // args
	)
	if err != nil {
		return fmt.Errorf("consuming DLQ: %w", err)
	}

	go func() {
		for d:= range msgs {
			select {
			case <-ctx.Done():
				return
			default:
				if err:= r.processDLQMessage(ctx, d); err != nil {
					fmt.Printf("Error processing DLQ message: %v\n", err)
					d.Nack(false, false)
					continue
				}
				d.Ack(false)
			}
		}
	}()

	return nil
}

func (r *RabbitMQDeadLetterSetup) processDLQMessage(ctx context.Context, msg amqp.Delivery) error {
	var deathInfo []DeathInfo
	if xDeath, ok:= msg.Headers["x-death"].([]interface{}); ok && len(xDeath) > 0 {
		if death, ok:= xDeath[0].(amqp.Table); ok {
			info:= DeathInfo{
				Queue:  death["queue"].(string),
				Reason: death["reason"].(string),
				Count:  int(death["count"].(int64)),
			}
			deathInfo = append(deathInfo, info)
		}
	}

	var content interface{}
	if err:= json.Unmarshal(msg.Body, &content); err != nil {
		return fmt.Errorf("unmarshaling message: %w", err)
	}

	fmt.Printf("DLQ Message: %+v, Death Info: %+v\n", content, deathInfo)
	return nil
}
```

---

## Kafka DLQ Pattern

```go
package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/segmentio/kafka-go"
)

type KafkaDeadLetterHandler struct {
	writer   *kafka.Writer
	dlqTopic string
}

func NewKafkaDeadLetterHandler(brokers []string, mainTopic string) *KafkaDeadLetterHandler {
	dlqTopic:= fmt.Sprintf("%s.dlq", mainTopic)
	return &KafkaDeadLetterHandler{
		writer: &kafka.Writer{
			Addr:     kafka.TCP(brokers...),
			Topic:    dlqTopic,
			Balancer: &kafka.LeastBytes{},
		},
		dlqTopic: dlqTopic,
	}
}

func (k *KafkaDeadLetterHandler) SendToDLQ(ctx context.Context, msg kafka.Message, err error, partition int, offset int64) error {
	headers:= make([]kafka.Header, 0, len(msg.Headers)+5)
	headers = append(headers, msg.Headers...)
	headers = append(headers,
		kafka.Header{Key: "x-original-topic", Value: []byte(msg.Topic)},
		kafka.Header{Key: "x-original-partition", Value: []byte(fmt.Sprintf("%d", partition))},
		kafka.Header{Key: "x-original-offset", Value: []byte(fmt.Sprintf("%d", offset))},
		kafka.Header{Key: "x-error-message", Value: []byte(err.Error())},
		kafka.Header{Key: "x-error-type", Value: []byte(fmt.Sprintf("%T", err))},
		kafka.Header{Key: "x-failed-at", Value: []byte(time.Now().Format(time.RFC3339))},
	)

	return k.writer.WriteMessages(ctx, kafka.Message{
		Key:     msg.Key,
		Value:   msg.Value,
		Headers: headers,
	})
}

type MessageHandler func(ctx context.Context, payload interface{}) error

func (k *KafkaDeadLetterHandler) ConsumeWithDLQ(ctx context.Context, topic string, brokers []string, groupID string, handler MessageHandler) error {
	reader:= kafka.NewReader(kafka.ReaderConfig{
		Brokers:  brokers,
		Topic:    topic,
		GroupID:  groupID,
		MinBytes: 10e3, // 10KB
		MaxBytes: 10e6, // 10MB
	})
	defer reader.Close()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			msg, err:= reader.FetchMessage(ctx)
			if err != nil {
				return fmt.Errorf("fetching message: %w", err)
			}

			var payload interface{}
			if err:= json.Unmarshal(msg.Value, &payload); err != nil {
				k.SendToDLQ(ctx, msg, err, msg.Partition, msg.Offset)
				reader.CommitMessages(ctx, msg)
				continue
			}

			if err:= handler(ctx, payload); err != nil {
				k.SendToDLQ(ctx, msg, err, msg.Partition, msg.Offset)
			}

			reader.CommitMessages(ctx, msg)
		}
	}
}

func (k *KafkaDeadLetterHandler) Close() error {
	return k.writer.Close()
}
```

---

## DLQ Consumer and Remediation

```go
package messaging

import (
	"context"
	"fmt"
	"time"
)

type RemediationAction string

const (
	ActionRetry      RemediationAction = "retry"
	ActionFixAndRetry RemediationAction = "fix_and_retry"
	ActionArchive    RemediationAction = "archive"
	ActionDiscard    RemediationAction = "discard"
)

type ArchiveStore interface {
	Store(ctx context.Context, message interface{}) error
}

type DLQRemediator struct {
	dlqConsumer    <-chan DeadLetterMessage
	originalQueues map[string]MessageQueue
	archiveStore   ArchiveStore
}

func NewDLQRemediator(dlqConsumer <-chan DeadLetterMessage, originalQueues map[string]MessageQueue, archiveStore ArchiveStore) *DLQRemediator {
	return &DLQRemediator{
		dlqConsumer:    dlqConsumer,
		originalQueues: originalQueues,
		archiveStore:   archiveStore,
	}
}

func (d *DLQRemediator) ProcessDeadLetters(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case dlMessage:= <-d.dlqConsumer:
			if err:= d.processMessage(ctx, dlMessage); err != nil {
				fmt.Printf("Error processing DLQ message: %v\n", err)
			}
		}
	}
}

func (d *DLQRemediator) processMessage(ctx context.Context, dlMessage DeadLetterMessage) error {
	action:= d.determineAction(dlMessage)

	switch action {
	case ActionRetry:
		return d.retryMessage(ctx, dlMessage)
	case ActionFixAndRetry:
		fixed:= d.fixMessage(dlMessage)
		dlMessage.OriginalMessage = fixed
		return d.retryMessage(ctx, dlMessage)
	case ActionArchive:
		return d.archiveMessage(ctx, dlMessage)
	case ActionDiscard:
		fmt.Printf("Discarding message: %s\n", dlMessage.ID)
		return nil
	default:
		return fmt.Errorf("unknown action: %s", action)
	}
}

func (d *DLQRemediator) determineAction(dlMessage DeadLetterMessage) RemediationAction {
	errorType:= dlMessage.Error.Name

	if errorType == "TransientError" || errorType == "TimeoutError" {
		return ActionRetry
	}
	if errorType == "ValidationError" {
		return ActionFixAndRetry
	}
	if errorType == "PermanentError" {
		return ActionArchive
	}
	if dlMessage.Attempts > 10 {
		return ActionArchive
	}

	return ActionRetry
}

func (d *DLQRemediator) retryMessage(ctx context.Context, dlMessage DeadLetterMessage) error {
	queue, ok:= d.originalQueues[dlMessage.OriginalQueue]
	if !ok {
		return fmt.Errorf("unknown queue: %s", dlMessage.OriginalQueue)
	}

	headers:= map[string]string{
		"x-retry-from-dlq":     "true",
		"x-original-failure":   dlMessage.Error.Message,
	}

	type messageWithHeaders struct {
		Message interface{}
		Headers map[string]string
	}

	return queue.Send(ctx, dlMessage.OriginalQueue, messageWithHeaders{
		Message: dlMessage.OriginalMessage,
		Headers: headers,
	})
}

func (d *DLQRemediator) fixMessage(dlMessage DeadLetterMessage) interface{} {
	message, ok:= dlMessage.OriginalMessage.(map[string]interface{})
	if !ok {
		return dlMessage.OriginalMessage
	}

	// Examples of automatic corrections
	if contains(dlMessage.Error.Message, "missing field") {
		message["missingField"] = "default_value"
	}
	if contains(dlMessage.Error.Message, "invalid date") {
		message["date"] = time.Now().Format(time.RFC3339)
	}

	return message
}

func (d *DLQRemediator) archiveMessage(ctx context.Context, dlMessage DeadLetterMessage) error {
	archiveData:= struct {
		DeadLetterMessage
		ArchivedAt time.Time
	}{
		DeadLetterMessage: dlMessage,
		ArchivedAt:        time.Now(),
	}
	return d.archiveStore.Store(ctx, archiveData)
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && s[:len(substr)] == substr
}
```

---

## Monitoring and Alerting

```go
package messaging

import (
	"context"
	"time"
)

type DLQHealth struct {
	QueueSize               int
	OldestMessageAgeMinutes int
	ErrorTypes              map[string]int
	Status                  string
}

type DLQMonitor struct {
	queue         MessageQueue
	alertService  AlertService
}

func NewDLQMonitor(queue MessageQueue, alertService AlertService) *DLQMonitor {
	return &DLQMonitor{
		queue:        queue,
		alertService: alertService,
	}
}

func (m *DLQMonitor) CheckHealth(ctx context.Context) (*DLQHealth, error) {
	queueSize, err:= m.getQueueSize(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting queue size: %w", err)
	}

	oldestMessageAge, err:= m.getOldestMessageAge(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting oldest message age: %w", err)
	}

	errorBreakdown, err:= m.getErrorBreakdown(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting error breakdown: %w", err)
	}

	health:= &DLQHealth{
		QueueSize:               queueSize,
		OldestMessageAgeMinutes: oldestMessageAge,
		ErrorTypes:              errorBreakdown,
		Status:                  m.determineStatus(queueSize, oldestMessageAge),
	}

	if health.Status == "critical" {
		if err:= m.alertService.Critical(ctx, "DLQ Critical", map[string]interface{}{
			"queueSize":               health.QueueSize,
			"oldestMessageAgeMinutes": health.OldestMessageAgeMinutes,
			"errorTypes":              health.ErrorTypes,
		}); err != nil {
			return health, fmt.Errorf("sending critical alert: %w", err)
		}
	} else if health.Status == "warning" {
		// Send warning alert (implementation omitted for brevity)
	}

	return health, nil
}

func (m *DLQMonitor) determineStatus(size, ageMinutes int) string {
	if size > 1000 || ageMinutes > 60 {
		return "critical"
	}
	if size > 100 || ageMinutes > 30 {
		return "warning"
	}
	return "healthy"
}

func (m *DLQMonitor) getQueueSize(ctx context.Context) (int, error) {
	// Implementation depends on message broker
	return 0, nil
}

func (m *DLQMonitor) getOldestMessageAge(ctx context.Context) (int, error) {
	// Implementation depends on message broker
	return 0, nil
}

func (m *DLQMonitor) getErrorBreakdown(ctx context.Context) (map[string]int, error) {
	// Implementation depends on message broker
	return make(map[string]int), nil
}
```

---

## When to Use

- Messages unprocessable after multiple attempts
- Permanent errors (invalid data, missing resources)
- Need to keep failed messages for analysis
- Remediation or replay system required
- Processing failure audit trail

## Related Patterns

- [Retry Pattern](../resilience/retry.md) - Before DLQ
- [Circuit Breaker](../cloud/circuit-breaker.md) - Prevent overload
- [Idempotent Receiver](./idempotent-receiver.md) - Retry safe
- [Process Manager](./process-manager.md) - Orchestrate remediation
