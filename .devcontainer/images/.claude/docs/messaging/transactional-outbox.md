# Transactional Outbox Pattern

> Guarantee message reliability by storing them in an outbox table within the same transaction as business data.

## Overview

```
+------------------+
|   Application    |
+--------+---------+
         |
         | BEGIN TRANSACTION
         |
         v
+--------+---------+     +----------------+
|    Database      |     |   Outbox       |
|                  |     |   Table        |
| INSERT order     |     | INSERT message |
| UPDATE inventory |     |                |
+--------+---------+     +-------+--------+
         |                       |
         | COMMIT                |
         |                       v
         |              +--------+--------+
         |              | Outbox Relay    |
         |              | (Poll/CDC)      |
         +------------->+--------+--------+
                                 |
                                 v
                        +--------+--------+
                        | Message Broker  |
                        | (RabbitMQ/Kafka)|
                        +-----------------+
```

---

## Problem Solved

```
SANS OUTBOX (probleme du dual write):

Transaction 1: UPDATE order SET status='paid'  --> OK
Transaction 2: PUBLISH OrderPaid event         --> FAIL

Resultat: DB mise a jour, mais message perdu!

AVEC OUTBOX:

Transaction atomique:
  - UPDATE order SET status='paid'
  - INSERT INTO outbox (event_type, payload)
COMMIT

Relay separee publie le message --> Fiable!
```

---

## Base Implementation

### Outbox Table Schema

```sql
CREATE TABLE outbox (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  aggregate_type VARCHAR(100) NOT NULL,
  aggregate_id VARCHAR(100) NOT NULL,
  event_type VARCHAR(100) NOT NULL,
  payload JSONB NOT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT NOW(),
  published_at TIMESTAMP NULL,
  retry_count INT NOT NULL DEFAULT 0,
  last_error TEXT NULL,

  INDEX idx_outbox_unpublished (published_at) WHERE published_at IS NULL
);
```

### Application Layer

```go
package outbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

type OutboxMessage struct {
	AggregateType string      `json:"aggregateType"`
	AggregateID   string      `json:"aggregateId"`
	EventType     string      `json:"eventType"`
	Payload       interface{} `json:"payload"`
}

type OutboxRow struct {
	ID            string
	AggregateType string
	AggregateID   string
	EventType     string
	Payload       json.RawMessage
	CreatedAt     time.Time
	PublishedAt   *time.Time
	RetryCount    int
	LastError     *string
}

type OutboxRepository struct {
	db *sql.DB
}

func NewOutboxRepository(db *sql.DB) *OutboxRepository {
	return &OutboxRepository{db: db}
}

func (r *OutboxRepository) SaveMessage(ctx context.Context, message *OutboxMessage, tx *sql.Tx) error {
	payload, err:= json.Marshal(message.Payload)
	if err != nil {
		return fmt.Errorf("marshaling payload: %w", err)
	}

	query:= `INSERT INTO outbox (aggregate_type, aggregate_id, event_type, payload)
              VALUES ($1, $2, $3, $4)`

	executor:= r.getExecutor(tx)
	_, err = executor.ExecContext(ctx, query,
		message.AggregateType,
		message.AggregateID,
		message.EventType,
		payload,
	)
	if err != nil {
		return fmt.Errorf("inserting outbox message: %w", err)
	}

	return nil
}

func (r *OutboxRepository) GetUnpublished(ctx context.Context, limit int) ([]*OutboxRow, error) {
	query:= `SELECT id, aggregate_type, aggregate_id, event_type, payload, created_at, retry_count
              FROM outbox
              WHERE published_at IS NULL
              ORDER BY created_at ASC
              LIMIT $1
              FOR UPDATE SKIP LOCKED`

	rows, err:= r.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("querying unpublished messages: %w", err)
	}
	defer rows.Close()

	var messages []*OutboxRow
	for rows.Next() {
		var msg OutboxRow
		if err:= rows.Scan(
			&msg.ID,
			&msg.AggregateType,
			&msg.AggregateID,
			&msg.EventType,
			&msg.Payload,
			&msg.CreatedAt,
			&msg.RetryCount,
		); err != nil {
			return nil, fmt.Errorf("scanning outbox row: %w", err)
		}
		messages = append(messages, &msg)
	}

	return messages, rows.Err()
}

func (r *OutboxRepository) MarkAsPublished(ctx context.Context, id string) error {
	query:= `UPDATE outbox SET published_at = NOW() WHERE id = $1`
	_, err:= r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("marking as published: %w", err)
	}
	return nil
}

func (r *OutboxRepository) MarkAsFailed(ctx context.Context, id string, errMsg string) error {
	query:= `UPDATE outbox
              SET retry_count = retry_count + 1, last_error = $2
              WHERE id = $1`
	_, err:= r.db.ExecContext(ctx, query, id, errMsg)
	if err != nil {
		return fmt.Errorf("marking as failed: %w", err)
	}
	return nil
}

func (r *OutboxRepository) getExecutor(tx *sql.Tx) interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
} {
	if tx != nil {
		return tx
	}
	return r.db
}

// Usage dans le service
type OrderData struct {
	CustomerID string
	Total      float64
	Items      []OrderItem
}

type OrderItem struct {
	ProductID string
	Quantity  int
	Price     float64
}

type Order struct {
	ID         string
	CustomerID string
	Total      float64
	Items      []OrderItem
}

type OrderRepository interface {
	Create(ctx context.Context, data *OrderData, tx *sql.Tx) (*Order, error)
}

type OrderService struct {
	db              *sql.DB
	orderRepo       OrderRepository
	outboxRepo      *OutboxRepository
}

func NewOrderService(db *sql.DB, orderRepo OrderRepository, outboxRepo *OutboxRepository) *OrderService {
	return &OrderService{
		db:         db,
		orderRepo:  orderRepo,
		outboxRepo: outboxRepo,
	}
}

func (s *OrderService) PlaceOrder(ctx context.Context, orderData *OrderData) (*Order, error) {
	tx, err:= s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	// 1. Creer la commande
	order, err:= s.orderRepo.Create(ctx, orderData, tx)
	if err != nil {
		return nil, fmt.Errorf("creating order: %w", err)
	}

	// 2. Ajouter l'evenement dans l'outbox (meme transaction)
	if err:= s.outboxRepo.SaveMessage(ctx, &OutboxMessage{
		AggregateType: "Order",
		AggregateID:   order.ID,
		EventType:     "OrderCreated",
		Payload: map[string]interface{}{
			"orderId":    order.ID,
			"customerId": order.CustomerID,
			"total":      order.Total,
			"items":      order.Items,
		},
	}, tx); err != nil {
		return nil, fmt.Errorf("saving outbox message: %w", err)
	}

	if err:= tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}

	return order, nil
}
```

---

## Outbox Relay (Polling)

```go
package outbox

import (
	"context"
	"fmt"
	"log"
	"time"
)

type MessageBroker interface {
	Publish(ctx context.Context, topic string, message interface{}) error
}

type OutboxRelay struct {
	running      bool
	pollInterval time.Duration
	outboxRepo   *OutboxRepository
	broker       MessageBroker
	stopChan     chan struct{}
}

func NewOutboxRelay(outboxRepo *OutboxRepository, broker MessageBroker) *OutboxRelay {
	return &OutboxRelay{
		pollInterval: 1 * time.Second,
		outboxRepo:   outboxRepo,
		broker:       broker,
		stopChan:     make(chan struct{}),
	}
}

func (r *OutboxRelay) Start(ctx context.Context) error {
	r.running = true
	return r.poll(ctx)
}

func (r *OutboxRelay) Stop() {
	r.running = false
	close(r.stopChan)
}

func (r *OutboxRelay) poll(ctx context.Context) error {
	ticker:= time.NewTicker(r.pollInterval)
	defer ticker.Stop()

	for r.running {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.stopChan:
			return nil
		case <-ticker.C:
			if err:= r.processMessages(ctx); err != nil {
				log.Printf("Outbox relay error: %v", err)
			}
		}
	}

	return nil
}

func (r *OutboxRelay) processMessages(ctx context.Context) error {
	messages, err:= r.outboxRepo.GetUnpublished(ctx, 100)
	if err != nil {
		return fmt.Errorf("getting unpublished messages: %w", err)
	}

	for _, message:= range messages {
		if err:= r.processMessage(ctx, message); err != nil {
			log.Printf("Error processing message %s: %v", message.ID, err)
		}
	}

	return nil
}

func (r *OutboxRelay) processMessage(ctx context.Context, message *OutboxRow) error {
	topic:= r.getTopicForEvent(message.EventType)

	var payload interface{}
	if err:= json.Unmarshal(message.Payload, &payload); err != nil {
		return r.outboxRepo.MarkAsFailed(ctx, message.ID, err.Error())
	}

	event:= map[string]interface{}{
		"id":          message.ID,
		"type":        message.EventType,
		"aggregateId": message.AggregateID,
		"payload":     payload,
		"timestamp":   message.CreatedAt,
	}

	if err:= r.broker.Publish(ctx, topic, event); err != nil {
		return r.outboxRepo.MarkAsFailed(ctx, message.ID, err.Error())
	}

	return r.outboxRepo.MarkAsPublished(ctx, message.ID)
}

func (r *OutboxRelay) getTopicForEvent(eventType string) string {
	topicMap:= map[string]string{
		"OrderCreated":     "orders.created",
		"OrderShipped":     "orders.shipped",
		"PaymentReceived":  "payments.received",
	}
	if topic, ok:= topicMap[eventType]; ok {
		return topic
	}
	return "events.default"
}
```

---

## Outbox Relay (CDC - Change Data Capture)

```go
/*
Avec Debezium pour PostgreSQL

Debezium capture les INSERT sur la table outbox
et les publie directement vers Kafka.

Configuration Debezium:
{
  "name": "outbox-connector",
  "config": {
    "connector.class": "io.debezium.connector.postgresql.PostgresConnector",
    "database.hostname": "db",
    "database.port": "5432",
    "database.user": "app",
    "database.password": "***",
    "database.dbname": "orders",
    "table.include.list": "public.outbox",
    "transforms": "outbox",
    "transforms.outbox.type": "io.debezium.transforms.outbox.EventRouter",
    "transforms.outbox.table.field.event.key": "aggregate_id",
    "transforms.outbox.table.field.event.type": "event_type",
    "transforms.outbox.table.field.event.payload": "payload"
  }
}

Consumer cote application
*/

package outbox

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/segmentio/kafka-go"
)

type EventHandler interface {
	Handle(ctx context.Context, event *Event) error
}

type Event struct {
	Type        string      `json:"type"`
	AggregateID string      `json:"aggregateId"`
	Payload     interface{} `json:"payload"`
}

type DebeziumEvent struct {
	Before interface{} `json:"before"`
	After  struct {
		EventType     string          `json:"event_type"`
		AggregateID   string          `json:"aggregate_id"`
		Payload       json.RawMessage `json:"payload"`
	} `json:"after"`
}

type DebeziumOutboxConsumer struct {
	reader       *kafka.Reader
	eventHandler EventHandler
}

func NewDebeziumOutboxConsumer(brokers []string, groupID string, handler EventHandler) *DebeziumOutboxConsumer {
	return &DebeziumOutboxConsumer{
		reader: kafka.NewReader(kafka.ReaderConfig{
			Brokers: brokers,
			Topic:   "outbox.events",
			GroupID: groupID,
		}),
		eventHandler: handler,
	}
}

func (c *DebeziumOutboxConsumer) Consume(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			msg, err:= c.reader.FetchMessage(ctx)
			if err != nil {
				return fmt.Errorf("fetching message: %w", err)
			}

			if err:= c.processMessage(ctx, msg); err != nil {
				log.Printf("Error processing message: %v", err)
			}

			c.reader.CommitMessages(ctx, msg)
		}
	}
}

func (c *DebeziumOutboxConsumer) processMessage(ctx context.Context, msg kafka.Message) error {
	var debeziumEvent DebeziumEvent
	if err:= json.Unmarshal(msg.Value, &debeziumEvent); err != nil {
		return fmt.Errorf("unmarshaling debezium event: %w", err)
	}

	var payload interface{}
	if err:= json.Unmarshal(debeziumEvent.After.Payload, &payload); err != nil {
		return fmt.Errorf("unmarshaling payload: %w", err)
	}

	event:= &Event{
		Type:        debeziumEvent.After.EventType,
		AggregateID: debeziumEvent.After.AggregateID,
		Payload:     payload,
	}

	return c.eventHandler.Handle(ctx, event)
}

func (c *DebeziumOutboxConsumer) Close() error {
	return c.reader.Close()
}
```

---

## Outbox with Cleanup

```go
package outbox

import (
	"context"
	"fmt"
	"time"
)

type OutboxCleaner struct {
	db            *sql.DB
	retentionDays int
}

func NewOutboxCleaner(db *sql.DB, retentionDays int) *OutboxCleaner {
	if retentionDays <= 0 {
		retentionDays = 7
	}
	return &OutboxCleaner{
		db:            db,
		retentionDays: retentionDays,
	}
}

// Execute periodically (cron)
func (c *OutboxCleaner) Cleanup(ctx context.Context) (int64, error) {
	query:= fmt.Sprintf(`DELETE FROM outbox
                          WHERE published_at IS NOT NULL
                          AND published_at < NOW() - INTERVAL '%d days'`, c.retentionDays)

	result, err:= c.db.ExecContext(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("deleting old messages: %w", err)
	}

	rowsDeleted, err:= result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("getting rows affected: %w", err)
	}

	return rowsDeleted, nil
}

// Archive before deletion (optional)
func (c *OutboxCleaner) ArchiveAndCleanup(ctx context.Context) error {
	query:= fmt.Sprintf(`
      WITH archived AS (
        INSERT INTO outbox_archive
        SELECT * FROM outbox
        WHERE published_at IS NOT NULL
        AND published_at < NOW() - INTERVAL '%d days'
        RETURNING id
      )
      DELETE FROM outbox WHERE id IN (SELECT id FROM archived)
    `, c.retentionDays)

	_, err:= c.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("archiving and cleaning: %w", err)
	}

	return nil
}

// Scheduler pour cleanup automatique
func (c *OutboxCleaner) StartScheduler(ctx context.Context, interval time.Duration) {
	ticker:= time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			deleted, err:= c.Cleanup(ctx)
			if err != nil {
				log.Printf("Cleanup error: %v", err)
			} else {
				log.Printf("Cleaned up %d outbox messages", deleted)
			}
		}
	}
}
```

---

## Failure Handling

```go
package outbox

import (
	"context"
	"fmt"
	"math"
	"time"
)

type AlertService interface {
	Warn(ctx context.Context, message string, details map[string]interface{}) error
}

type RobustOutboxRelay struct {
	*OutboxRelay
	maxRetries   int
	alertService AlertService
}

func NewRobustOutboxRelay(outboxRepo *OutboxRepository, broker MessageBroker, alertService AlertService) *RobustOutboxRelay {
	return &RobustOutboxRelay{
		OutboxRelay:  NewOutboxRelay(outboxRepo, broker),
		maxRetries:   5,
		alertService: alertService,
	}
}

func (r *RobustOutboxRelay) processMessage(ctx context.Context, message *OutboxRow) error {
	if message.RetryCount >= r.maxRetries {
		if err:= r.moveToDeadLetter(ctx, message); err != nil {
			return fmt.Errorf("moving to dead letter: %w", err)
		}
		return r.outboxRepo.MarkAsPublished(ctx, message.ID)
	}

	// Exponential backoff
	if message.RetryCount > 0 {
		backoff:= time.Duration(math.Pow(2, float64(message.RetryCount))) * time.Second
		timeSinceCreation:= time.Since(message.CreatedAt)
		if timeSinceCreation < backoff {
			return nil // Pas encore temps de retry
		}
	}

	topic:= r.getTopicForEvent(message.EventType)

	var payload interface{}
	if err:= json.Unmarshal(message.Payload, &payload); err != nil {
		return r.outboxRepo.MarkAsFailed(ctx, message.ID, err.Error())
	}

	event:= r.formatMessage(message, payload)

	if err:= r.broker.Publish(ctx, topic, event); err != nil {
		if err:= r.outboxRepo.MarkAsFailed(ctx, message.ID, err.Error()); err != nil {
			return err
		}

		if message.RetryCount >= r.maxRetries-1 {
			return r.alertService.Warn(ctx, "Outbox message max retries", map[string]interface{}{
				"messageId": message.ID,
				"eventType": message.EventType,
			})
		}

		return nil
	}

	return r.outboxRepo.MarkAsPublished(ctx, message.ID)
}

func (r *RobustOutboxRelay) moveToDeadLetter(ctx context.Context, message *OutboxRow) error {
	query:= `INSERT INTO outbox_dead_letter
              SELECT *, NOW() as moved_at FROM outbox WHERE id = $1`
	_, err:= r.outboxRepo.db.ExecContext(ctx, query, message.ID)
	if err != nil {
		return fmt.Errorf("inserting to dead letter: %w", err)
	}
	return nil
}

func (r *RobustOutboxRelay) formatMessage(message *OutboxRow, payload interface{}) map[string]interface{} {
	return map[string]interface{}{
		"id":          message.ID,
		"type":        message.EventType,
		"aggregateId": message.AggregateID,
		"payload":     payload,
		"timestamp":   message.CreatedAt,
	}
}
```

---

## Ordering and Partitioning

```go
package outbox

import (
	"context"
	"fmt"
	"sync"
)

type OrderedOutboxRelay struct {
	*OutboxRelay
}

func NewOrderedOutboxRelay(outboxRepo *OutboxRepository, broker MessageBroker) *OrderedOutboxRelay {
	return &OrderedOutboxRelay{
		OutboxRelay: NewOutboxRelay(outboxRepo, broker),
	}
}

func (r *OrderedOutboxRelay) ProcessMessages(ctx context.Context) error {
	// Group by aggregate to maintain order
	query:= `SELECT DISTINCT ON (aggregate_id) *
              FROM outbox
              WHERE published_at IS NULL
              ORDER BY aggregate_id, created_at ASC`

	rows, err:= r.outboxRepo.db.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("querying messages: %w", err)
	}
	defer rows.Close()

	// Group by aggregate_id
	byAggregate:= make(map[string][]*OutboxRow)
	for rows.Next() {
		var msg OutboxRow
		if err:= rows.Scan(&msg); err != nil {
			return fmt.Errorf("scanning row: %w", err)
		}
		byAggregate[msg.AggregateID] = append(byAggregate[msg.AggregateID], &msg)
	}

	// Process in parallel by aggregate, sequential within aggregate
	var wg sync.WaitGroup
	errChan:= make(chan error, len(byAggregate))

	for aggregateID, messages:= range byAggregate {
		aggIDCaptured:= aggregateID
		msgsCaptured:= messages
		wg.Go(func() {
			if err:= r.processAggregateMessages(ctx, aggIDCaptured, msgsCaptured); err != nil {
				errChan <- err
			}
		})
	}

	wg.Wait()
	close(errChan)

	// Collect errors
	var errs []error
	for err:= range errChan {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return fmt.Errorf("processing errors: %v", errs)
	}

	return nil
}

func (r *OrderedOutboxRelay) processAggregateMessages(ctx context.Context, aggregateID string, messages []*OutboxRow) error {
	// Sequential to maintain order
	for _, message:= range messages {
		if err:= r.processMessage(ctx, message); err != nil {
			return fmt.Errorf("processing message %s for aggregate %s: %w", message.ID, aggregateID, err)
		}
	}
	return nil
}
```

---

## When to Use

- Avoid the dual-write problem (DB + message broker)
- Guarantee consistency between data and events
- Event-driven systems with ACID transactions
- Microservices with asynchronous communication
- Need for audit trail of published events

## Related Patterns

- [Idempotent Receiver](./idempotent-receiver.md) - Consumer side
- [Event Sourcing](../architectural/event-sourcing.md) - Complete alternative
- [Saga](../cloud/saga.md) - Distributed transactions
- [Dead Letter Channel](./dead-letter.md) - Failed messages
