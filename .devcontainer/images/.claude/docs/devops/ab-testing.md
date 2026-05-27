# A/B Testing

> Controlled experimentation to validate hypotheses with metrics.

**Related to:** [Feature Toggles](feature-toggles.md) (technical implementation)

## Principle

```
┌─────────────────────────────────────────────────────────────────┐
│                        A/B TESTING                               │
│                                                                  │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │                   TRAFFIC SPLITTER                        │   │
│  │                         │                                 │   │
│  │         ┌───────────────┴───────────────┐                │   │
│  │         │                               │                │   │
│  │        50%                             50%               │   │
│  │         │                               │                │   │
│  │         ▼                               ▼                │   │
│  │  ┌─────────────┐                 ┌─────────────┐        │   │
│  │  │  CONTROL    │                 │  VARIANT    │        │   │
│  │  │    (A)      │                 │    (B)      │        │   │
│  │  │             │                 │             │        │   │
│  │  │ ┌─────────┐ │                 │ ┌─────────┐ │        │   │
│  │  │ │ Button  │ │                 │ │ Button  │ │        │   │
│  │  │ │  Blue   │ │                 │ │  Green  │ │        │   │
│  │  │ └─────────┘ │                 │ └─────────┘ │        │   │
│  │  └─────────────┘                 └─────────────┘        │   │
│  │         │                               │                │   │
│  │         ▼                               ▼                │   │
│  │  ┌─────────────┐                 ┌─────────────┐        │   │
│  │  │ Conversion  │                 │ Conversion  │        │   │
│  │  │    2.1%     │                 │    2.8%     │        │   │
│  │  └─────────────┘                 └─────────────┘        │   │
│  │                                                          │   │
│  │              Winner: Variant B (+33%)                    │   │
│  └──────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
```

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                      EXPERIMENTATION PLATFORM                    │
│                                                                  │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │                    EXPERIMENT CONFIG                      │   │
│  │  {                                                        │   │
│  │    name: "checkout-redesign",                             │   │
│  │    hypothesis: "Green CTA increases conversion",          │   │
│  │    metric: "purchase_completed",                          │   │
│  │    variants: [                                            │   │
│  │      { name: "control", weight: 50 },                     │   │
│  │      { name: "green_button", weight: 50 }                 │   │
│  │    ],                                                     │   │
│  │    audience: { country: ["US", "CA"], device: "mobile" }  │   │
│  │  }                                                        │   │
│  └──────────────────────────────────────────────────────────┘   │
│                              │                                   │
│                              ▼                                   │
│  ┌──────────┐    ┌──────────────┐    ┌──────────┐              │
│  │ User     │───▶│ Assignment   │───▶│ Variant  │              │
│  │ Request  │    │ Service      │    │ Response │              │
│  └──────────┘    └──────────────┘    └──────────┘              │
│                         │                    │                   │
│                         ▼                    ▼                   │
│                  ┌──────────────┐    ┌──────────────┐           │
│                  │ Tracking     │    │ Analytics    │           │
│                  │ Events       │───▶│ Dashboard    │           │
│                  └──────────────┘    └──────────────┘           │
└─────────────────────────────────────────────────────────────────┘
```

## Implementation

### Experimentation Service

```go
package experiment

import (
	"context"
	"fmt"
	"hash/fnv"
	"sync"
	"time"
)

// ExperimentStatus represents the current state of an experiment.
type ExperimentStatus string

const (
	StatusDraft     ExperimentStatus = "draft"
	StatusRunning   ExperimentStatus = "running"
	StatusPaused    ExperimentStatus = "paused"
	StatusCompleted ExperimentStatus = "completed"
)

// Variant represents a single variant in an experiment.
type Variant struct {
	Name   string                 `json:"name"`
	Weight int                    `json:"weight"` // 0-100
	Config map[string]interface{} `json:"config,omitempty"`
}

// MetricType defines the type of metric being tracked.
type MetricType string

const (
	MetricConversion MetricType = "conversion"
	MetricRevenue    MetricType = "revenue"
	MetricEngagement MetricType = "engagement"
	MetricRetention  MetricType = "retention"
)

// MetricGoal defines whether the metric should increase or decrease.
type MetricGoal string

const (
	GoalIncrease MetricGoal = "increase"
	GoalDecrease MetricGoal = "decrease"
)

// Metric represents a tracked metric for an experiment.
type Metric struct {
	Name string     `json:"name"`
	Type MetricType `json:"type"`
	Goal MetricGoal `json:"goal"`
}

// AudienceRule defines targeting rules for experiments.
type AudienceRule struct {
	Country    []string `json:"country,omitempty"`
	Device     string   `json:"device,omitempty"`
	UserPlan   string   `json:"user_plan,omitempty"`
	Percentage int      `json:"percentage,omitempty"`
}

// Experiment represents a complete A/B test configuration.
type Experiment struct {
	ID         string           `json:"id"`
	Name       string           `json:"name"`
	Hypothesis string           `json:"hypothesis"`
	Variants   []Variant        `json:"variants"`
	Metrics    []Metric         `json:"metrics"`
	Audience   []AudienceRule   `json:"audience,omitempty"`
	StartDate  time.Time        `json:"start_date"`
	EndDate    *time.Time       `json:"end_date,omitempty"`
	Status     ExperimentStatus `json:"status"`
}

// Service manages A/B experiments.
type Service struct {
	mu          sync.RWMutex
	experiments map[string]*Experiment
	assignments map[string]map[string]string // userID -> experimentID -> variant
}

// NewService creates a new experiment service.
func NewService() *Service {
	return &Service{
		experiments: make(map[string]*Experiment),
		assignments: make(map[string]map[string]string),
	}
}

// GetVariant returns the assigned variant for a user in an experiment.
func (s *Service) GetVariant(ctx context.Context, userID, experimentID string) (string, error) {
	s.mu.RLock()
	experiment, exists:= s.experiments[experimentID]
	s.mu.RUnlock()

	if !exists || experiment.Status != StatusRunning {
		return "", nil
	}

	// Check audience targeting
	if !s.matchesAudience(ctx, userID, experiment.Audience) {
		return "", nil
	}

	// Check for existing assignment (sticky)
	if cached:= s.getAssignment(userID, experimentID); cached != "" {
		return cached, nil
	}

	// Hash-based deterministic assignment
	hash:= s.hashUserID(userID, experimentID)
	variant:= s.selectVariant(hash, experiment.Variants)

	s.saveAssignment(userID, experimentID, variant)
	s.trackExposure(ctx, userID, experimentID, variant)

	return variant, nil
}

// hashUserID generates a deterministic hash for user assignment.
func (s *Service) hashUserID(userID, experimentID string) uint32 {
	h:= fnv.New32a()
	_, _ = h.Write([]byte(userID + ":" + experimentID))
	return h.Sum32()
}

// selectVariant selects a variant based on hash and weights.
func (s *Service) selectVariant(hash uint32, variants []Variant) string {
	if len(variants) == 0 {
		return ""
	}

	bucket:= int(hash % 100)
	cumulative:= 0

	for _, variant:= range variants {
		cumulative += variant.Weight
		if bucket < cumulative {
			return variant.Name
		}
	}

	return variants[0].Name
}

// getAssignment retrieves a cached assignment.
func (s *Service) getAssignment(userID, experimentID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if userAssignments, ok:= s.assignments[userID]; ok {
		return userAssignments[experimentID]
	}
	return ""
}

// saveAssignment stores an assignment.
func (s *Service) saveAssignment(userID, experimentID, variant string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok:= s.assignments[userID]; !ok {
		s.assignments[userID] = make(map[string]string)
	}
	s.assignments[userID][experimentID] = variant
}

// matchesAudience checks if user matches audience rules.
func (s *Service) matchesAudience(ctx context.Context, userID string, rules []AudienceRule) bool {
	// Placeholder - implement based on your user context
	return true
}

// trackExposure records user exposure to variant.
func (s *Service) trackExposure(ctx context.Context, userID, experimentID, variant string) {
	// Placeholder - integrate with your analytics system
	fmt.Printf("Exposure: user=%s experiment=%s variant=%s\n", userID, experimentID, variant)
}
```

### Metrics Tracking

```go
package analytics

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"
)

// EventType defines the type of tracking event.
type EventType string

const (
	EventExposure   EventType = "exposure"
	EventConversion EventType = "conversion"
	EventCustom     EventType = "custom"
)

// TrackingEvent represents a single tracking event.
type TrackingEvent struct {
	UserID       string                 `json:"user_id"`
	ExperimentID string                 `json:"experiment_id"`
	Variant      string                 `json:"variant"`
	EventType    EventType              `json:"event_type"`
	EventName    string                 `json:"event_name,omitempty"`
	Value        float64                `json:"value,omitempty"`
	Timestamp    time.Time              `json:"timestamp"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
	SessionID    string                 `json:"session_id,omitempty"`
}

// VariantData holds aggregated metrics for a variant.
type VariantData struct {
	Exposures   int
	Conversions int
}

// VariantResults represents the results for a single variant.
type VariantResults struct {
	Name           string  `json:"name"`
	Exposures      int     `json:"exposures"`
	Conversions    int     `json:"conversions"`
	ConversionRate float64 `json:"conversion_rate"`
	Confidence     float64 `json:"confidence"`
}

// ExperimentResults represents complete experiment results.
type ExperimentResults struct {
	Variants                   []VariantResults `json:"variants"`
	Winner                     string           `json:"winner"`
	StatisticalSignificance    bool             `json:"statistical_significance"`
}

// Service handles analytics tracking and analysis.
type Service struct {
	mu     sync.RWMutex
	events []TrackingEvent
}

// NewService creates a new analytics service.
func NewService() *Service {
	return &Service{
		events: make([]TrackingEvent, 0),
	}
}

// TrackEvent records a tracking event.
func (s *Service) TrackEvent(ctx context.Context, event TrackingEvent) error {
	event.Timestamp = time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()

	s.events = append(s.events, event)
	return nil
}

// GetExperimentResults calculates results for an experiment.
func (s *Service) GetExperimentResults(ctx context.Context, experimentID string) (*ExperimentResults, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	byVariant:= s.groupByVariant(experimentID)

	results:= &ExperimentResults{
		Variants: make([]VariantResults, 0, len(byVariant)),
	}

	controlData, hasControl:= byVariant["control"]

	for variant, data:= range byVariant {
		convRate:= 0.0
		if data.Exposures > 0 {
			convRate = float64(data.Conversions) / float64(data.Exposures)
		}

		confidence:= 0.0
		if hasControl && variant != "control" {
			confidence = s.calculateConfidence(data, controlData)
		}

		results.Variants = append(results.Variants, VariantResults{
			Name:           variant,
			Exposures:      data.Exposures,
			Conversions:    data.Conversions,
			ConversionRate: convRate,
			Confidence:     confidence,
		})
	}

	results.Winner = s.determineWinner(byVariant)
	results.StatisticalSignificance = s.isSignificant(byVariant)

	return results, nil
}

// groupByVariant aggregates events by variant.
func (s *Service) groupByVariant(experimentID string) map[string]VariantData {
	grouped:= make(map[string]VariantData)

	for _, event:= range s.events {
		if event.ExperimentID != experimentID {
			continue
		}

		data:= grouped[event.Variant]

		if event.EventType == EventExposure {
			data.Exposures++
		} else if event.EventType == EventConversion {
			data.Conversions++
		}

		grouped[event.Variant] = data
	}

	return grouped
}

// calculateConfidence performs Z-test for proportions.
func (s *Service) calculateConfidence(variant, control VariantData) float64 {
	if variant.Exposures == 0 || control.Exposures == 0 {
		return 0.0
	}

	p1:= float64(variant.Conversions) / float64(variant.Exposures)
	p2:= float64(control.Conversions) / float64(control.Exposures)
	n1:= float64(variant.Exposures)
	n2:= float64(control.Exposures)

	pooledP:= (float64(variant.Conversions) + float64(control.Conversions)) / (n1 + n2)
	se:= math.Sqrt(pooledP * (1 - pooledP) * (1/n1 + 1/n2))

	if se == 0 {
		return 0.0
	}

	z:= (p1 - p2) / se
	return s.zToConfidence(z)
}

// zToConfidence converts z-score to confidence level.
func (s *Service) zToConfidence(z float64) float64 {
	// Simplified - use proper statistical library for production
	absZ:= math.Abs(z)
	if absZ > 2.58 {
		return 0.99
	} else if absZ > 1.96 {
		return 0.95
	} else if absZ > 1.645 {
		return 0.90
	}
	return 0.50
}

// determineWinner identifies the winning variant.
func (s *Service) determineWinner(byVariant map[string]VariantData) string {
	winner:= ""
	maxRate:= 0.0

	for variant, data:= range byVariant {
		if data.Exposures == 0 {
			continue
		}
		rate:= float64(data.Conversions) / float64(data.Exposures)
		if rate > maxRate {
			maxRate = rate
			winner = variant
		}
	}

	return winner
}

// isSignificant determines if results are statistically significant.
func (s *Service) isSignificant(byVariant map[string]VariantData) bool {
	controlData, hasControl:= byVariant["control"]
	if !hasControl {
		return false
	}

	for variant, data:= range byVariant {
		if variant == "control" {
			continue
		}
		confidence:= s.calculateConfidence(data, controlData)
		if confidence >= 0.95 {
			return true
		}
	}

	return false
}
```

### Client-Side Usage

```go
package client

import (
	"context"
	"fmt"
)

// ExperimentClient wraps experiment service for client use.
type ExperimentClient struct {
	service ExperimentService
}

// ExperimentService defines the interface for experiment operations.
type ExperimentService interface {
	GetVariant(ctx context.Context, userID, experimentID string) (string, error)
}

// NewExperimentClient creates a new client.
func NewExperimentClient(service ExperimentService) *ExperimentClient {
	return &ExperimentClient{
		service: service,
	}
}

// UseExperiment fetches the variant for a user.
func (c *ExperimentClient) UseExperiment(ctx context.Context, userID, experimentID string) (string, bool, error) {
	variant, err:= c.service.GetVariant(ctx, userID, experimentID)
	if err != nil {
		return "", false, fmt.Errorf("getting variant: %w", err)
	}

	isLoading:= variant == ""
	isControl:= variant == "control"

	return variant, isControl && !isLoading, nil
}

// Example usage in HTTP handler
func CheckoutButtonHandler(w http.ResponseWriter, r *http.Request) {
	ctx:= r.Context()
	userID:= getUserID(r) // Your auth logic

	client:= NewExperimentClient(experimentService)
	variant, isControl, err:= client.UseExperiment(ctx, userID, "checkout-button-color")
	if err != nil {
		// Handle error, fallback to control
		variant = "control"
	}

	color:= "blue"
	if variant == "green_button" {
		color = "green"
	}

	// Render button with appropriate color
	renderButton(w, color)
}
```

## Sample Size Calculation

```go
package stats

import (
	"math"
)

// CalculateSampleSize computes required sample size for A/B test.
func CalculateSampleSize(
	baselineConversion float64,
	minimumDetectableEffect float64, // e.g., 0.05 = 5% lift
	power float64,
	significance float64,
) int {
	if power == 0 {
		power = 0.8
	}
	if significance == 0 {
		significance = 0.05
	}

	p1:= baselineConversion
	p2:= baselineConversion * (1 + minimumDetectableEffect)

	zAlpha:= 1.96 // 95% significance
	zBeta:= 0.84  // 80% power

	pooledP:= (p1 + p2) / 2
	effect:= math.Abs(p2 - p1)

	if effect == 0 {
		return 0
	}

	n:= 2 * pooledP * (1 - pooledP) *
		math.Pow((zAlpha+zBeta)/effect, 2)

	return int(math.Ceil(n))
}

// Example: 2% conversion, detect 10% lift
// CalculateSampleSize(0.02, 0.10, 0.8, 0.05) ≈ 15,000 users per variant
```

## When to Use

| Use | Avoid |
|-----|-------|
| Sufficient traffic (>1000/variant) | Low traffic |
| Clear hypothesis | Vague exploration |
| Defined metrics | No tracking |
| Sufficient duration (1-4 weeks) | Need immediate results |
| UI/UX changes | Technical changes |

## Advantages

- **Real data**: Decisions based on facts
- **Risk reduction**: Validate before full deployment
- **Continuous learning**: Data-driven culture
- **Measurable ROI**: Quantifiable impact
- **Avoids opinions**: Data vs intuition

## Disadvantages

- **Time**: Weeks for significant results
- **Volume**: Need substantial traffic
- **Complexity**: Dedicated infrastructure
- **False positives**: Statistical risk
- **Pollution**: Interactions between tests

## Real-World Examples

| Company | Famous Example |
|---------|----------------|
| **Google** | 41 shades of blue (links) |
| **Netflix** | Personalized thumbnails |
| **Amazon** | One-click checkout |
| **Booking** | FOMO messages |
| **Airbnb** | Search design |

## Tools

| Tool | Type | Features |
|------|------|----------|
| **Optimizely** | SaaS | Full-stack, enterprise |
| **LaunchDarkly** | SaaS | Feature flags + A/B |
| **Split.io** | SaaS | Focus experimentation |
| **Google Optimize** | Free | GA integration |
| **Growthbook** | Open-source | Self-hosted |
| **Statsig** | SaaS | Advanced stats |

## Best Practices

1. **One hypothesis per test**: No multiple changes
2. **Sample size**: Calculate before launching
3. **Fixed duration**: Do not stop prematurely
4. **Segmentation**: Analyze by segment
5. **Documentation**: Hypothesis, results, learnings

## Related Patterns

| Pattern | Relation |
|---------|----------|
| Feature Toggles | Technical implementation |
| Canary | A/B on infrastructure |
| Multivariate Testing | Extension with combinations |
| Personalization | A/B + ML |

## Sources

- [Ronny Kohavi - Trustworthy Online Experiments](https://www.exp-platform.com/)
- [Evan Miller - Sample Size Calculator](https://www.evanmiller.org/ab-testing/)
- [Netflix Tech Blog - Experimentation](https://netflixtechblog.com/experimentation-is-a-major-focus-of-data-science-across-netflix-f67f29d0e0bb)
- [Booking.com - Experimentation Culture](https://blog.booking.com/)
