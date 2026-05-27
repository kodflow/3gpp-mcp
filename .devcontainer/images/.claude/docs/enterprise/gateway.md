# Gateway

> "An object that encapsulates access to an external system or resource." - Martin Fowler, PoEAA

## Concept

The Gateway is an object that encapsulates access to an external system or resource. It provides a simple and consistent API for interacting with third-party services, databases, or legacy systems.

## Types of Gateways

1. **Table Data Gateway**: Access to a database table
2. **Row Data Gateway**: Access to a database row
3. **Service Gateway**: Access to an external service
4. **Messaging Gateway**: Access to a messaging system

## Service Gateway - Go Implementation

```go
package gateway

import (
	"context"
	"fmt"
	"time"
)

// Money represents a monetary value.
type Money struct {
	Amount   int64  // In cents
	Currency string
}

// PaymentMethod represents a payment method.
type PaymentMethod struct {
	Token string
	Type  string
}

// PaymentResult represents a payment result.
type PaymentResult struct {
	Success       bool
	TransactionID string
	Status        string
	Error         string
	Code          string
	Raw           any
}

// Transaction represents a transaction.
type Transaction struct {
	ID        string
	Amount    Money
	Status    string
	CreatedAt time.Time
	Metadata  map[string]string
}

// PaymentGateway defines the payment gateway interface.
type PaymentGateway interface {
	Charge(ctx context.Context, amount Money, method PaymentMethod) (*PaymentResult, error)
	Refund(ctx context.Context, transactionID string, amount *Money) (*RefundResult, error)
	GetTransaction(ctx context.Context, transactionID string) (*Transaction, error)
}

// RefundResult represents a refund result.
type RefundResult struct {
	Success  bool
	RefundID string
	Amount   Money
}

// StripePaymentGateway implements PaymentGateway for Stripe.
type StripePaymentGateway struct {
	client *stripe.Client
}

// NewStripePaymentGateway creates a new Stripe gateway.
func NewStripePaymentGateway(apiKey string) *StripePaymentGateway {
	return &StripePaymentGateway{
		client: stripe.NewClient(apiKey),
	}
}

// Charge processes a payment.
func (g *StripePaymentGateway) Charge(ctx context.Context, amount Money, method PaymentMethod) (*PaymentResult, error) {
	params := &stripe.PaymentIntentParams{
		Amount:        stripe.Int64(amount.Amount),
		Currency:      stripe.String(amount.Currency),
		PaymentMethod: stripe.String(method.Token),
		Confirm:       stripe.Bool(true),
	}

	intent, err := g.client.PaymentIntents.New(params)
	if err != nil {
		if stripeErr, ok := err.(*stripe.Error); ok {
			return &PaymentResult{
				Success: false,
				Error:   stripeErr.Msg,
				Code:    string(stripeErr.Code),
			}, nil
		}
		return nil, fmt.Errorf("stripe charge: %w", err)
	}

	return &PaymentResult{
		Success:       intent.Status == "succeeded",
		TransactionID: intent.ID,
		Status:        g.mapStatus(intent.Status),
		Raw:           intent,
	}, nil
}

// Refund processes a refund.
func (g *StripePaymentGateway) Refund(ctx context.Context, transactionID string, amount *Money) (*RefundResult, error) {
	params := &stripe.RefundParams{
		PaymentIntent: stripe.String(transactionID),
	}

	if amount != nil {
		params.Amount = stripe.Int64(amount.Amount)
	}

	refund, err := g.client.Refunds.New(params)
	if err != nil {
		return nil, fmt.Errorf("stripe refund: %w", err)
	}

	return &RefundResult{
		Success:  refund.Status == "succeeded",
		RefundID: refund.ID,
		Amount: Money{
			Amount:   refund.Amount,
			Currency: refund.Currency,
		},
	}, nil
}

// GetTransaction retrieves a transaction.
func (g *StripePaymentGateway) GetTransaction(ctx context.Context, transactionID string) (*Transaction, error) {
	intent, err := g.client.PaymentIntents.Get(transactionID, nil)
	if err != nil {
		return nil, fmt.Errorf("get payment intent: %w", err)
	}

	return &Transaction{
		ID:        intent.ID,
		Amount:    Money{Amount: intent.Amount, Currency: intent.Currency},
		Status:    g.mapStatus(intent.Status),
		CreatedAt: time.Unix(intent.Created, 0),
		Metadata:  intent.Metadata,
	}, nil
}

func (g *StripePaymentGateway) mapStatus(status string) string {
	mapping := map[string]string{
		"succeeded":       "completed",
		"processing":      "pending",
		"requires_action": "requires_action",
		"canceled":        "cancelled",
	}
	if mapped, ok := mapping[status]; ok {
		return mapped
	}
	return "unknown"
}

// PayPalPaymentGateway implements PaymentGateway for PayPal.
type PayPalPaymentGateway struct {
	client *paypal.Client
}

// NewPayPalPaymentGateway creates a new PayPal gateway.
func NewPayPalPaymentGateway(clientID, clientSecret string, sandbox bool) *PayPalPaymentGateway {
	client := paypal.NewClient(clientID, clientSecret, sandbox)
	return &PayPalPaymentGateway{client: client}
}

// Charge processes a payment.
func (g *PayPalPaymentGateway) Charge(ctx context.Context, amount Money, method PaymentMethod) (*PaymentResult, error) {
	order, err := g.client.CreateOrder(ctx, paypal.OrderRequest{
		Intent: "CAPTURE",
		PurchaseUnits: []paypal.PurchaseUnit{
			{
				Amount: paypal.Amount{
					Currency: amount.Currency,
					Value:    fmt.Sprintf("%.2f", float64(amount.Amount)/100),
				},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create order: %w", err)
	}

	capture, err := g.client.CaptureOrder(ctx, order.ID)
	if err != nil {
		return nil, fmt.Errorf("capture order: %w", err)
	}

	return &PaymentResult{
		Success:       capture.Status == "COMPLETED",
		TransactionID: capture.ID,
		Status:        g.mapStatus(capture.Status),
	}, nil
}

func (g *PayPalPaymentGateway) mapStatus(status string) string {
	mapping := map[string]string{
		"COMPLETED": "completed",
		"PENDING":   "pending",
		"CANCELED":  "cancelled",
	}
	if mapped, ok := mapping[status]; ok {
		return mapped
	}
	return "unknown"
}

// HTTP Gateway Example
type WeatherGateway interface {
	GetCurrentWeather(ctx context.Context, city string) (*Weather, error)
	GetForecast(ctx context.Context, city string, days int) (*Forecast, error)
}

type Weather struct {
	Temperature float64
	Humidity    int
	Description string
	WindSpeed   float64
	Timestamp   time.Time
}

type Forecast struct {
	City  string
	Days  []DayForecast
}

type DayForecast struct {
	Date        time.Time
	Temperature float64
	Description string
}

type OpenWeatherGateway struct {
	httpClient *http.Client
	cache      *Cache
	apiKey     string
	baseURL    string
}

func NewOpenWeatherGateway(apiKey string) *OpenWeatherGateway {
	return &OpenWeatherGateway{
		httpClient: &http.Client{Timeout: 5 * time.Second},
		cache:      NewCache(10 * time.Minute),
		apiKey:     apiKey,
		baseURL:    "https://api.openweathermap.org/data/2.5",
	}
}

func (g *OpenWeatherGateway) GetCurrentWeather(ctx context.Context, city string) (*Weather, error) {
	cacheKey := fmt.Sprintf("weather:%s", city)

	// Check cache
	if cached, ok := g.cache.Get(cacheKey); ok {
		return cached.(*Weather), nil
	}

	// Make HTTP request
	url := fmt.Sprintf("%s/weather?q=%s&appid=%s&units=metric", g.baseURL, city, g.apiKey)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http status: %d", resp.StatusCode)
	}

	var apiResp openWeatherResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	weather := g.mapToWeather(apiResp)
	g.cache.Set(cacheKey, weather)

	return weather, nil
}

func (g *OpenWeatherGateway) mapToWeather(resp openWeatherResponse) *Weather {
	return &Weather{
		Temperature: resp.Main.Temp,
		Humidity:    resp.Main.Humidity,
		Description: resp.Weather[0].Description,
		WindSpeed:   resp.Wind.Speed,
		Timestamp:   time.Now(),
	}
}

type openWeatherResponse struct {
	Main struct {
		Temp     float64 `json:"temp"`
		Humidity int     `json:"humidity"`
	} `json:"main"`
	Weather []struct {
		Description string `json:"description"`
	} `json:"weather"`
	Wind struct {
		Speed float64 `json:"speed"`
	} `json:"wind"`
}
```

## Comparison with Alternatives

| Aspect | Gateway | Adapter | Facade |
|--------|---------|---------|--------|
| Objective | External access | Compatibility | Simplification |
| Direction | Outbound | Bidirectional | Internal |
| Abstraction | External system | Interface | Subsystem |
| Testability | Mockable | Mockable | Less important |

## When to Use

**Use Gateway when:**

- Integration with external services
- Need to abstract technical details
- Multiple possible implementations (Stripe/PayPal)
- Testability is important (mocking)
- Resilience required (retry, circuit breaker)

**Avoid Gateway when:**

- Simple and direct access is sufficient
- Single external service with no planned changes
- Ultra-critical performance (overhead)

## Related Patterns

- [Remote Facade](./remote-facade.md) - Coarse-grained facade for remote clients
- [Service Layer](./service-layer.md) - Uses Gateway for external access
- [Repository](./repository.md) - Similar abstraction for local data
- [Data Mapper](./data-mapper.md) - Mapping between systems

## Sources

- Martin Fowler, PoEAA, Chapter 18
- [Gateway - martinfowler.com](https://martinfowler.com/eaaCatalog/gateway.html)
