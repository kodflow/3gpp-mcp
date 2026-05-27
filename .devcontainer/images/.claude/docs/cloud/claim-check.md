# Claim Check Pattern

> Separate the message from its large payload via a reference.

## Principle

```
┌─────────────────────────────────────────────────────────────────────────┐
│                         CLAIM CHECK PATTERN                              │
│                                                                          │
│   PRODUCER                                                               │
│   ┌─────────┐                                                           │
│   │  Data   │──┐                                                        │
│   │  (10MB) │  │                                                        │
│   └─────────┘  │                                                        │
│                │                                                        │
│                ▼                                                        │
│   ┌────────────────────┐         ┌─────────────────────────────────┐   │
│   │   1. Store Data    │────────▶│         BLOB STORAGE            │   │
│   └────────────────────┘         │   ┌─────────────────────────┐   │   │
│                │                 │   │  claim-id-123.json      │   │   │
│                │ claim_id        │   │  (actual data 10MB)     │   │   │
│                ▼                 │   └─────────────────────────┘   │   │
│   ┌────────────────────┐         └─────────────────────────────────┘   │
│   │ 2. Send Claim Only │                        ▲                       │
│   │   { claim: "123" } │                        │                       │
│   └────────────────────┘                        │                       │
│                │                                │                       │
│                ▼                                │                       │
│   ┌────────────────────┐                        │                       │
│   │    MESSAGE QUEUE   │                        │                       │
│   │  (small message)   │                        │                       │
│   └────────────────────┘                        │                       │
│                │                                │                       │
│                ▼                                │                       │
│   ┌────────────────────┐                        │                       │
│   │ 3. Consume Message │                        │                       │
│   └────────────────────┘                        │                       │
│                │                                │                       │
│                ▼                                │                       │
│   ┌────────────────────┐         ┌──────────────┘                       │
│   │ 4. Retrieve Data   │─────────┘                                      │
│   └────────────────────┘                                                │
│                │                                                        │
│                ▼                                                        │
│   CONSUMER                                                              │
│   ┌─────────┐                                                           │
│   │  Data   │                                                           │
│   │  (10MB) │                                                           │
│   └─────────┘                                                           │
└─────────────────────────────────────────────────────────────────────────┘
```

## Problem Solved

| Approach | Message Queue | Latency | Cost |
|----------|---------------|---------|------|
| **Without Claim Check** | 10MB per message | High | High |
| **With Claim Check** | ~100 bytes | Low | Low |

## Go Example

```go
package claimcheck

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// ClaimCheckMessage represents a message with optional inline payload or claim reference.
type ClaimCheckMessage struct {
	ClaimID  string                 `json:"claimId"`
	Metadata ClaimCheckMetadata     `json:"metadata"`
	Payload  interface{}            `json:"payload,omitempty"`
}

// ClaimCheckMetadata contains message metadata.
type ClaimCheckMetadata struct {
	ContentType string    `json:"contentType"`
	Size        int       `json:"size"`
	CreatedAt   time.Time `json:"createdAt"`
	TTL         *int      `json:"ttl,omitempty"`
}

// StorageProvider defines storage operations for claims.
type StorageProvider interface {
	Store(ctx context.Context, data []byte, ttl int) (string, error)
	Retrieve(ctx context.Context, claimID string) ([]byte, error)
	Delete(ctx context.Context, claimID string) error
}

// MessageQueue defines queue operations.
type MessageQueue interface {
	Publish(ctx context.Context, msg ClaimCheckMessage) error
	Consume(ctx context.Context) (*ClaimCheckMessage, error)
}

// ClaimCheckService implements the claim check pattern.
type ClaimCheckService struct {
	storage         StorageProvider
	queue           MessageQueue
	inlineThreshold int
}

// NewClaimCheckService creates a new ClaimCheckService.
func NewClaimCheckService(storage StorageProvider, queue MessageQueue) *ClaimCheckService {
	return &ClaimCheckService{
		storage:         storage,
		queue:           queue,
		inlineThreshold: 1024, // 1KB
	}
}

// Send sends data using claim check pattern.
func (s *ClaimCheckService) Send(ctx context.Context, data interface{}, ttl int) error {
	serialized, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshaling data: %w", err)
	}

	size := len(serialized)
	msg := ClaimCheckMessage{
		Metadata: ClaimCheckMetadata{
			ContentType: "application/json",
			Size:        size,
			CreatedAt:   time.Now(),
		},
	}

	if size <= s.inlineThreshold {
		// Small payload: inline
		msg.Payload = data
	} else {
		// Large payload: claim check
		claimID, err := s.storage.Store(ctx, serialized, ttl)
		if err != nil {
			return fmt.Errorf("storing claim: %w", err)
		}
		msg.ClaimID = claimID
		msg.Metadata.TTL = &ttl
	}

	if err := s.queue.Publish(ctx, msg); err != nil {
		return fmt.Errorf("publishing message: %w", err)
	}

	return nil
}

// Receive receives data using claim check pattern.
func (s *ClaimCheckService) Receive(ctx context.Context) (interface{}, error) {
	msg, err := s.queue.Consume(ctx)
	if err != nil {
		return nil, fmt.Errorf("consuming message: %w", err)
	}

	if msg.Payload != nil {
		// Inline payload
		return msg.Payload, nil
	}

	// Retrieve from storage
	data, err := s.storage.Retrieve(ctx, msg.ClaimID)
	if err != nil {
		return nil, fmt.Errorf("retrieving claim: %w", err)
	}

	var result interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("unmarshaling data: %w", err)
	}

	return result, nil
}

func generateClaimID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return "claim-" + hex.EncodeToString(b)
}
```

## Usage

```go
// This example follows the same idiomatic Go patterns
// as the main example above.
// Specific implementation based on standard Go
// interfaces and conventions.
```

## Lifecycle Management

```go
// This example follows the same idiomatic Go patterns
// as the main example above.
// Specific implementation based on standard Go
// interfaces and conventions.
```

## S3 Lifecycle Configuration

```json
{
  "Rules": [
    {
      "ID": "ClaimCheckCleanup",
      "Status": "Enabled",
      "Filter": {
        "Prefix": "claim-"
      },
      "Expiration": {
        "Days": 1
      }
    }
  ]
}
```

## Use Cases

| Scenario | Typical Size | Benefit |
|----------|--------------|---------|
| **PDF Documents** | 1-50 MB | Lightweight queue |
| **Images/Videos** | 1 MB - 1 GB | Async processing |
| **Reports** | 10-100 MB | Scalability |
| **Backups** | 100+ MB | Decoupling |
| **ETL data** | GB+ | Performance |

## Anti-patterns

| Anti-pattern | Problem | Solution |
|--------------|---------|----------|
| No TTL | Storage accumulation | Mandatory TTL |
| Non-unique claim | Collisions | UUID or hash |
| No retry | Data loss | Retry + DLQ |
| Synchronous cleanup | Latency | Async/lifecycle rules |

## When to Use

- Messages exceeding the broker size limit (typically > 256KB)
- Large file transfer via message queue
- Reducing messaging costs by avoiding large payloads
- Decoupling large data processing
- ETL or batch processing pipelines with massive data

## Related Patterns

| Pattern | Relation |
|---------|----------|
| Content Enricher | Inverse (add data) |
| Message Expiration | Claim TTL |
| Dead Letter | Unconsumed claims |
| Event Sourcing | Storing large events |

## Sources

- [Microsoft - Claim Check](https://learn.microsoft.com/en-us/azure/architecture/patterns/claim-check)
- [Enterprise Integration Patterns](https://www.enterpriseintegrationpatterns.com/StoreInLibrary.html)
