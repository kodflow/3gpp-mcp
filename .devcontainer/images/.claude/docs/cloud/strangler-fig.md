# Strangler Fig Pattern

> Progressively migrate a legacy system by replacing it incrementally.

## Principle

```
                    ┌─────────────────────────────────────────────┐
                    │              STRANGLER FIG                   │
                    └─────────────────────────────────────────────┘

  Natural inspiration: Strangler fig tree
  - Grows around an existing tree
  - Replaces it progressively
  - The original tree disappears

  Phase 1: COEXISTENCE
  ┌─────────────────────────────────────────────────────────┐
  │                        FACADE                           │
  └──────────────────────────┬──────────────────────────────┘
                             │
           ┌─────────────────┴─────────────────┐
           │                                   │
           ▼                                   ▼
  ┌─────────────────┐               ┌─────────────────┐
  │    LEGACY       │               │      NEW        │
  │   (monolith)    │               │   (services)    │
  │   ████████████  │               │   ░░░░          │
  └─────────────────┘               └─────────────────┘

  Phase 2: PROGRESSIVE MIGRATION
  ┌─────────────────────────────────────────────────────────┐
  │                        FACADE                           │
  └──────────────────────────┬──────────────────────────────┘
                             │
           ┌─────────────────┴─────────────────┐
           │                                   │
           ▼                                   ▼
  ┌─────────────────┐               ┌─────────────────┐
  │    LEGACY       │               │      NEW        │
  │   ████████      │               │   ░░░░░░░░░░░░  │
  └─────────────────┘               └─────────────────┘

  Phase 3: DECOMMISSION
  ┌─────────────────────────────────────────────────────────┐
  │                        FACADE                           │
  └──────────────────────────┬──────────────────────────────┘
                             │
                             ▼
                  ┌─────────────────┐
                  │      NEW        │
                  │   ░░░░░░░░░░░░  │
                  │   (complete)    │
                  └─────────────────┘
```

## Go Implementation

```go
package stranglerfig

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
)

// RoutingConfig defines routing configuration for a feature.
type RoutingConfig struct {
	Feature    string
	UseNew     bool
	Percentage int // For canary deployment
}

// LegacyService defines the legacy service interface.
type LegacyService interface {
	Execute(ctx context.Context, feature, method string, data interface{}) (interface{}, error)
}

// NewService defines the new service interface.
type NewService interface {
	Execute(ctx context.Context, method string, data interface{}) (interface{}, error)
}

// StranglerFacade manages migration from legacy to new services.
type StranglerFacade struct {
	mu            sync.RWMutex
	routingConfig map[string]*RoutingConfig
	legacyService LegacyService
	newServices   map[string]NewService
}

// NewStranglerFacade creates a new StranglerFacade.
func NewStranglerFacade(
	legacyService LegacyService,
	newServices map[string]NewService,
) *StranglerFacade {
	sf := &StranglerFacade{
		routingConfig: make(map[string]*RoutingConfig),
		legacyService: legacyService,
		newServices:   newServices,
	}

	sf.initializeRouting()
	return sf
}

func (sf *StranglerFacade) initializeRouting() {
	// Configuration by feature
	sf.routingConfig["users"] = &RoutingConfig{
		Feature: "users",
		UseNew:  true,
	}
	sf.routingConfig["orders"] = &RoutingConfig{
		Feature:    "orders",
		UseNew:     true,
		Percentage: 50, // Canary: 50% traffic
	}
	sf.routingConfig["inventory"] = &RoutingConfig{
		Feature: "inventory",
		UseNew:  false, // Still legacy
	}
	sf.routingConfig["reports"] = &RoutingConfig{
		Feature: "reports",
		UseNew:  false,
	}
}

// HandleRequest handles a request by routing to legacy or new service.
func (sf *StranglerFacade) HandleRequest(
	ctx context.Context,
	feature, method string,
	data interface{},
) (interface{}, error) {
	sf.mu.RLock()
	config, exists := sf.routingConfig[feature]
	sf.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("unknown feature: %s", feature)
	}

	useNewService := sf.shouldUseNewService(config)

	if useNewService {
		service, exists := sf.newServices[feature]
		if !exists {
			return nil, fmt.Errorf("new service not found for: %s", feature)
		}
		return service.Execute(ctx, method, data)
	}

	return sf.legacyService.Execute(ctx, feature, method, data)
}

func (sf *StranglerFacade) shouldUseNewService(config *RoutingConfig) bool {
	if !config.UseNew {
		return false
	}

	// Canary: percentage of traffic
	if config.Percentage > 0 && config.Percentage < 100 {
		return rand.Intn(100) < config.Percentage
	}

	return true
}

// EnableNewService migrates a feature to the new service.
func (sf *StranglerFacade) EnableNewService(feature string, percentage int) {
	if percentage == 0 {
		percentage = 100
	}

	sf.mu.Lock()
	defer sf.mu.Unlock()

	sf.routingConfig[feature] = &RoutingConfig{
		Feature:    feature,
		UseNew:     true,
		Percentage: percentage,
	}
}

// DisableNewService rolls back to legacy service.
func (sf *StranglerFacade) DisableNewService(feature string) {
	sf.mu.Lock()
	defer sf.mu.Unlock()

	sf.routingConfig[feature] = &RoutingConfig{
		Feature: feature,
		UseNew:  false,
	}
}
```

## Anti-Corruption Layer

```go
// This example follows the same idiomatic Go patterns
// as the main example above.
// Specific implementation based on interfaces and
// standard Go conventions.
```

## Bidirectional sync during migration

```go
// This example follows the same idiomatic Go patterns
// as the main example above.
// Specific implementation based on interfaces and
// standard Go conventions.
```

## Feature flags for migration

```go
// This example follows the same idiomatic Go patterns
// as the main example above.
// Specific implementation based on interfaces and
// standard Go conventions.
```

## Migration Phases

```
┌─────────────────────────────────────────────────────────────────┐
│                    STRANGLER MIGRATION PHASES                   │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  Phase 1: SETUP (2-4 weeks)                                    │
│  ├─ Facade/API Gateway in place                                │
│  ├─ Unified logging/monitoring                                 │
│  └─ First service extracted (the simplest)                     │
│                                                                 │
│  Phase 2: EXTRACT (iterative, months)                          │
│  ├─ Identify bounded contexts                                  │
│  ├─ Extract service by service                                 │
│  ├─ Dual-write during transition                               │
│  └─ Switch traffic progressively                               │
│                                                                 │
│  Phase 3: VALIDATE (per service)                               │
│  ├─ 100% traffic to new service                                │
│  ├─ Soak test period (1-4 weeks)                               │
│  └─ Comparative monitoring                                     │
│                                                                 │
│  Phase 4: CLEANUP                                              │
│  ├─ Remove legacy code                                         │
│  ├─ Remove dual-write                                          │
│  └─ Document                                                   │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

## Migration Metrics

```go
// This example follows the same idiomatic Go patterns
// as the main example above.
// Specific implementation based on interfaces and
// standard Go conventions.
```

## When to Use

| Situation | Recommended |
|-----------|-------------|
| Monolith to microservices | Yes |
| Progressive modernization | Yes |
| Cloud migration | Yes |
| Critical system (zero downtime) | Yes |
| Small simple project | No (overkill) |
| Very short deadline | No (big bang is faster) |

## Related Patterns

| Pattern | Relation |
|---------|----------|
| Anti-Corruption Layer | Translation between domains |
| Branch by Abstraction | Similar alternative |
| Feature Flags | Migration control |
| Facade | Single entry point |

## Sources

- [Microsoft - Strangler Fig](https://learn.microsoft.com/en-us/azure/architecture/patterns/strangler-fig)
- [Martin Fowler - Strangler Fig Application](https://martinfowler.com/bliki/StranglerFigApplication.html)
- [Sam Newman - Monolith to Microservices](https://samnewman.io/books/monolith-to-microservices/)
