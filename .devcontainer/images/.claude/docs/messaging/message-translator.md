# Message Translator Patterns

Message transformation and enrichment patterns.

## Overview

```
+------------+     +------------------+     +------------+
|  Format A  |---->| Message Translator|---->|  Format B  |
+------------+     +------------------+     +------------+
                          |
              +-----------+-----------+
              |           |           |
         Envelope    Enricher     Filter
          Wrapper
```

---

## Message Translator

> Converts a message from one format to another.

### Schema

```
+----------------+        +----------------+
|  Legacy Order  |        |  Modern Order  |
|  ORDER_ID: 123 |  --->  |  id: "123"     |
|  CUST_NM: "X"  |        |  customer: {   |
|  TOT_AMT: 1000 |        |    name: "X"   |
+----------------+        |  }             |
                          |  total: 10.00  |
                          +----------------+
```

### Implementation

```go
package translator

import (
	"context"
	"fmt"
)

// MessageTranslator converts messages from source to target format.
type MessageTranslator[S, T any] interface {
	Translate(ctx context.Context, source S) (T, error)
	CanTranslate(source interface{}) bool
}

// ValidatingTranslator translates with validation.
type ValidatingTranslator[S, T any] struct {
	translateFn func(context.Context, S) (T, error)
	validator   func(interface{}) bool
}

// NewValidatingTranslator creates a new validating translator.
func NewValidatingTranslator[S, T any](
	translateFn func(context.Context, S) (T, error),
	validator func(interface{}) bool,
) *ValidatingTranslator[S, T] {
	return &ValidatingTranslator[S, T]{
		translateFn: translateFn,
		validator:   validator,
	}
}

// CanTranslate checks if source can be translated.
func (vt *ValidatingTranslator[S, T]) CanTranslate(source interface{}) bool {
	return vt.validator(source)
}

// Translate converts source to target.
func (vt *ValidatingTranslator[S, T]) Translate(ctx context.Context, source S) (T, error) {
	if !vt.CanTranslate(source) {
		var zero T
		return zero, fmt.Errorf("invalid source format")
	}
	return vt.translateFn(ctx, source)
}

// LegacyOrder represents old format.
type LegacyOrder struct {
	OrderID  string       `json:"ORDER_ID"`
	CustNo   string       `json:"CUST_NO"`
	CustName string       `json:"CUST_NM"`
	TotAmt   int          `json:"TOT_AMT"` // en centimes
	Items    []LegacyItem `json:"ITEMS"`
}

// LegacyItem represents old item format.
type LegacyItem struct {
	ProdID   string `json:"PROD_ID"`
	Qty      int    `json:"QTY"`
	UnitPrc  int    `json:"UNIT_PRC"`
}

// ModernOrder represents new format.
type ModernOrder struct {
	ID       string       `json:"id"`
	Customer Customer     `json:"customer"`
	Total    float64      `json:"total"`
	Items    []ModernItem `json:"items"`
}

// Customer represents customer info.
type Customer struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ModernItem represents new item format.
type ModernItem struct {
	ProductID string  `json:"productId"`
	Quantity  int     `json:"quantity"`
	Price     float64 `json:"price"`
}

// LegacyToModernTranslator converts legacy to modern format.
type LegacyToModernTranslator struct{}

// NewLegacyToModernTranslator creates a new translator.
func NewLegacyToModernTranslator() *LegacyToModernTranslator {
	return &LegacyToModernTranslator{}
}

// CanTranslate checks if source is legacy order.
func (t *LegacyToModernTranslator) CanTranslate(source interface{}) bool {
	_, ok:= source.(LegacyOrder)
	return ok
}

// Translate converts legacy to modern order.
func (t *LegacyToModernTranslator) Translate(ctx context.Context, legacy LegacyOrder) (ModernOrder, error) {
	items:= make([]ModernItem, len(legacy.Items))
	for i, item:= range legacy.Items {
		items[i] = ModernItem{
			ProductID: item.ProdID,
			Quantity:  item.Qty,
			Price:     float64(item.UnitPrc) / 100.0,
		}
	}

	return ModernOrder{
		ID: legacy.OrderID,
		Customer: Customer{
			ID:   legacy.CustNo,
			Name: legacy.CustName,
		},
		Total: float64(legacy.TotAmt) / 100.0,
		Items: items,
	}, nil
}
```

**When:** Legacy integration, migration, multiple formats.
**Related to:** Adapter pattern, Normalizer.

---

## Envelope Wrapper

> Adds transport metadata to the message.

### Envelope Schema

```
+-------------+          +---------------------------+
|  Payload    |          |  Envelope                 |
|  {          |   --->   |  header: {                |
|    data...  |          |    messageId, timestamp,  |
|  }          |          |    source, version...     |
|             |          |  }                        |
+-------------+          |  body: { data... }        |
                         +---------------------------+
```

### Envelope Implementation

```go
package envelope

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// EnvelopeHeader contains message metadata.
type EnvelopeHeader struct {
	MessageID     string    `json:"messageId"`
	CorrelationID string    `json:"correlationId,omitempty"`
	CausationID   string    `json:"causationId,omitempty"`
	Timestamp     time.Time `json:"timestamp"`
	Source        string    `json:"source"`
	Destination   string    `json:"destination,omitempty"`
	Version       string    `json:"version"`
	ContentType   string    `json:"contentType"`
	TTL           int       `json:"ttl,omitempty"`
	Priority      string    `json:"priority,omitempty"` // low, normal, high, urgent
}

// Envelope wraps a message with metadata.
type Envelope[T any] struct {
	Header EnvelopeHeader `json:"header"`
	Body   T              `json:"body"`
}

// EnvelopeWrapper wraps messages with metadata.
type EnvelopeWrapper struct {
	source  string
	version string
}

// NewEnvelopeWrapper creates a new wrapper.
func NewEnvelopeWrapper(source, version string) *EnvelopeWrapper {
	return &EnvelopeWrapper{
		source:  source,
		version: version,
	}
}

// Option configures an envelope.
type Option func(*EnvelopeHeader)

// WithCorrelationID sets correlation ID.
func WithCorrelationID(id string) Option {
	return func(h *EnvelopeHeader) {
		h.CorrelationID = id
	}
}

// WithDestination sets destination.
func WithDestination(dest string) Option {
	return func(h *EnvelopeHeader) {
		h.Destination = dest
	}
}

// WithPriority sets priority.
func WithPriority(priority string) Option {
	return func(h *EnvelopeHeader) {
		h.Priority = priority
	}
}

// Wrap wraps a message in an envelope.
func (ew *EnvelopeWrapper) Wrap[T any](ctx context.Context, message T, opts ...Option) Envelope[T] {
	header:= EnvelopeHeader{
		MessageID:   uuid.New().String(),
		Timestamp:   time.Now(),
		Source:      ew.source,
		Version:     ew.version,
		ContentType: "application/json",
	}

	for _, opt:= range opts {
		opt(&header)
	}

	return Envelope[T]{
		Header: header,
		Body:   message,
	}
}

// Unwrap extracts the message from envelope.
func (ew *EnvelopeWrapper) Unwrap[T any](envelope Envelope[T]) T {
	return envelope.Body
}

// ExtractHeader extracts header for logging/tracing.
func (ew *EnvelopeWrapper) ExtractHeader[T any](envelope Envelope[T]) EnvelopeHeader {
	return envelope.Header
}

// TranslationPipeline translates messages through a channel.
type TranslationPipeline[S, T any] struct {
	translator MessageTranslator[S, T]
	inputCh    <-chan S
	outputCh   chan<- T
	errorCh    chan<- error
}

// NewTranslationPipeline creates a new pipeline.
func NewTranslationPipeline[S, T any](
	translator MessageTranslator[S, T],
	inputCh <-chan S,
	outputCh chan<- T,
	errorCh chan<- error,
) *TranslationPipeline[S, T] {
	return &TranslationPipeline[S, T]{
		translator: translator,
		inputCh:    inputCh,
		outputCh:   outputCh,
		errorCh:    errorCh,
	}
}

// Start begins translation pipeline.
func (tp *TranslationPipeline[S, T]) Start(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok:= <-tp.inputCh:
			if !ok {
				return nil
			}

			translated, err:= tp.translator.Translate(ctx, msg)
			if err != nil {
				select {
				case tp.errorCh <- err:
				case <-ctx.Done():
					return ctx.Err()
				}
				continue
			}

			select {
			case tp.outputCh <- translated:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}
```

**When:** Transport agnostic, tracing, audit.
**Related to:** Correlation Identifier, Message Header.

---

## Content Enricher

> Adds missing data from external sources.

### Enricher Schema

```
+----------------+       +----------------+       +------------------+
| Partial Order  | ----> |    Enricher   | ----> |  Complete Order  |
| customerId: X  |       |       |        |       | customerId: X    |
| items: [...]   |       |       v        |       | customer: {...}  |
+----------------+       | +----------+   |       | items: [...]     |
                         | | Customer |   |       | itemDetails:[...]|
                         | | Service  |   |       +------------------+
                         | +----------+   |
                         | +----------+   |
                         | | Product  |   |
                         | | Service  |   |
                         | +----------+   |
                         +----------------+
```

### Enricher Implementation

```go
package enricher

import (
	"context"
	"fmt"
	"sync"
)

// EnrichmentSource fetches enrichment data.
type EnrichmentSource[K comparable, V any] interface {
	Fetch(ctx context.Context, key K) (V, error)
	FetchBatch(ctx context.Context, keys []K) (map[K]V, error)
}

// ContentEnricher enriches messages with external data.
type ContentEnricher[T any] struct {
	enrichments []enrichment
}

type enrichment struct {
	keyExtractor func(interface{}) interface{}
	source       interface{}
	merger       func(interface{}, interface{}) map[string]interface{}
}

// NewContentEnricher creates a new enricher.
func NewContentEnricher[T any]() *ContentEnricher[T] {
	return &ContentEnricher[T]{
		enrichments: make([]enrichment, 0),
	}
}

// AddEnrichment adds an enrichment source.
func (ce *ContentEnricher[T]) AddEnrichment[K comparable, V any](
	keyExtractor func(T) K,
	source EnrichmentSource[K, V],
	merger func(T, V) map[string]interface{},
) *ContentEnricher[T] {
	ce.enrichments = append(ce.enrichments, enrichment{
		keyExtractor: func(msg interface{}) interface{} {
			return keyExtractor(msg.(T))
		},
		source: source,
		merger: func(msg interface{}, data interface{}) map[string]interface{} {
			return merger(msg.(T), data.(V))
		},
	})
	return ce
}

// Enrich enriches a message.
func (ce *ContentEnricher[T]) Enrich(ctx context.Context, message T) (map[string]interface{}, error) {
	result:= make(map[string]interface{})

	var mu sync.Mutex
	errCh:= make(chan error, len(ce.enrichments))
	var wg sync.WaitGroup

	for _, enr:= range ce.enrichments {
		enrCaptured:= enr
		wg.Go(func() {
			key:= enrCaptured.keyExtractor(message)
			source, ok:= enrCaptured.source.(interface {
				Fetch(context.Context, interface{}) (interface{}, error)
			})
			if !ok {
				errCh <- fmt.Errorf("invalid source type")
				return
			}

			data, err:= source.Fetch(ctx, key)
			if err != nil {
				errCh <- fmt.Errorf("fetching enrichment: %w", err)
				return
			}

			partial:= enrCaptured.merger(message, data)
			mu.Lock()
			for k, v:= range partial {
				result[k] = v
			}
			mu.Unlock()
		})
	}

	wg.Wait()
	close(errCh)

	if err:= <-errCh; err != nil {
		return nil, err
	}

	return result, nil
}

// EnrichmentPipeline enriches messages through channels.
type EnrichmentPipeline[T any] struct {
	enricher *ContentEnricher[T]
	inputCh  <-chan T
	outputCh chan<- map[string]interface{}
	errorCh  chan<- error
}

// NewEnrichmentPipeline creates a new pipeline.
func NewEnrichmentPipeline[T any](
	enricher *ContentEnricher[T],
	inputCh <-chan T,
	outputCh chan<- map[string]interface{},
	errorCh chan<- error,
) *EnrichmentPipeline[T] {
	return &EnrichmentPipeline[T]{
		enricher: enricher,
		inputCh:  inputCh,
		outputCh: outputCh,
		errorCh:  errorCh,
	}
}

// Start begins enrichment pipeline.
func (ep *EnrichmentPipeline[T]) Start(ctx context.Context, concurrency int) error {
	var wg sync.WaitGroup

	for i:= 0; i < concurrency; i++ {
		wg.Go(func() {
			for {
				select {
				case <-ctx.Done():
					return
				case msg, ok:= <-ep.inputCh:
					if !ok {
						return
					}

					enriched, err:= ep.enricher.Enrich(ctx, msg)
					if err != nil {
						select {
						case ep.errorCh <- err:
						case <-ctx.Done():
							return
						}
						continue
					}

					select {
					case ep.outputCh <- enriched:
					case <-ctx.Done():
						return
					}
				}
			}
		}()
	}

	wg.Wait()
	return nil
}
```

**When:** Partial data, aggregation, denormalization.
**Related to:** Content Filter, Aggregator.

---

## Content Filter

> Removes unnecessary or sensitive data.

### Filter Schema

```
+------------------+       +------------------+
| Full Customer    |       | Filtered Output  |
| id, name, email  | ----> | id, name         |
| ssn, creditCard  |       | (sans sensibles) |
| internalNotes    |       |                  |
+------------------+       +------------------+
```

### Filter Implementation

```go
package filter

import (
	"context"
	"regexp"
	"strings"
)

// FilterProjection projects input to output.
type FilterProjection[T, R any] func(context.Context, T) (R, error)

// ContentFilter filters message content.
type ContentFilter[TInput, TOutput any] struct {
	projection FilterProjection[TInput, TOutput]
}

// NewContentFilter creates a new content filter.
func NewContentFilter[TInput, TOutput any](
	projection FilterProjection[TInput, TOutput],
) *ContentFilter[TInput, TOutput] {
	return &ContentFilter[TInput, TOutput]{
		projection: projection,
	}
}

// Filter applies the filter.
func (cf *ContentFilter[TInput, TOutput]) Filter(ctx context.Context, message TInput) (TOutput, error) {
	return cf.projection(ctx, message)
}

// Chain chains two filters together.
func Chain[A, B, C any](
	first *ContentFilter[A, B],
	second *ContentFilter[B, C],
) *ContentFilter[A, C] {
	return NewContentFilter(func(ctx context.Context, input A) (C, error) {
		intermediate, err:= first.Filter(ctx, input)
		if err != nil {
			var zero C
			return zero, err
		}
		return second.Filter(ctx, intermediate)
	})
}

// PIIFilter anonymizes personally identifiable information.
type PIIFilter struct {
	emailRegex *regexp.Regexp
	phoneRegex *regexp.Regexp
}

// NewPIIFilter creates a new PII filter.
func NewPIIFilter() *PIIFilter {
	return &PIIFilter{
		emailRegex: regexp.MustCompile(`^(.{2}).*@`),
		phoneRegex: regexp.MustCompile(`\d(?=\d{4})`),
	}
}

// AnonymizeEmail masks email addresses.
func (pf *PIIFilter) AnonymizeEmail(email string) string {
	return pf.emailRegex.ReplaceAllString(email, "$1***@")
}

// AnonymizePhone masks phone numbers.
func (pf *PIIFilter) AnonymizePhone(phone string) string {
	return pf.phoneRegex.ReplaceAllString(phone, "*")
}

// AnonymizeName masks names.
func (pf *PIIFilter) AnonymizeName(name string) string {
	if len(name) == 0 {
		return ""
	}
	return string(name[0]) + "***"
}

// FilterPipeline filters messages through channels.
type FilterPipeline[TInput, TOutput any] struct {
	filter   *ContentFilter[TInput, TOutput]
	inputCh  <-chan TInput
	outputCh chan<- TOutput
	errorCh  chan<- error
}

// NewFilterPipeline creates a new filter pipeline.
func NewFilterPipeline[TInput, TOutput any](
	filter *ContentFilter[TInput, TOutput],
	inputCh <-chan TInput,
	outputCh chan<- TOutput,
	errorCh chan<- error,
) *FilterPipeline[TInput, TOutput] {
	return &FilterPipeline[TInput, TOutput]{
		filter:   filter,
		inputCh:  inputCh,
		outputCh: outputCh,
		errorCh:  errorCh,
	}
}

// Start begins filter pipeline.
func (fp *FilterPipeline[TInput, TOutput]) Start(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok:= <-fp.inputCh:
			if !ok {
				return nil
			}

			filtered, err:= fp.filter.Filter(ctx, msg)
			if err != nil {
				select {
				case fp.errorCh <- err:
				case <-ctx.Done():
					return ctx.Err()
				}
				continue
			}

			select {
			case fp.outputCh <- filtered:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}
```

**When:** Security, privacy, payload reduction.
**Related to:** Content Enricher, Message Filter.

---

## Decision Table

| Pattern | Use Case | Direction |
|---------|-------------|-----------|
| Translator | Format conversion | A -> B |
| Envelope | Transport metadata | + metadata |
| Enricher | Add data | + data |
| Filter | Remove data | - data |

---

## When to Use

- Integration of legacy systems with different data formats
- Progressive migration between old and new formats
- Message enrichment with external data (customers, products)
- Filtering sensitive data (PII) before transmission
- Normalization of messages from heterogeneous sources

## Related Patterns

- [Pipes and Filters](./pipes-filters.md) - Chain transformations
- [Message Channel](./message-channel.md) - Transport of transformed messages
- [Splitter-Aggregator](./splitter-aggregator.md) - Split and recombine
- [Process Manager](./process-manager.md) - Transformation orchestration

## Complementary Patterns

- **Normalizer** - Multiple formats to canonical
- **Canonical Data Model** - Standard format
- **Claim Check** - Store large payload
- **Pipes and Filters** - Chain transformations
