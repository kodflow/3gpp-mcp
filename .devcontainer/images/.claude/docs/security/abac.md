# Attribute-Based Access Control (ABAC)

> Dynamic permissions based on subject, resource, and context attributes.

## Principle

```
┌─────────────────────────────────────────────────────────────────┐
│                         ABAC Decision                           │
│                                                                  │
│   Subject Attributes    Resource Attributes    Environment      │
│   ├── role: editor      ├── owner: user123     ├── time: 14:30 │
│   ├── dept: marketing   ├── status: draft      ├── ip: internal│
│   └── level: senior     └── sensitivity: low   └── device: corp│
│              │                    │                    │        │
│              └────────────────────┼────────────────────┘        │
│                                   ▼                              │
│                        ┌──────────────────┐                     │
│                        │  Policy Engine   │                     │
│                        │                  │                     │
│                        │  IF conditions   │                     │
│                        │  THEN allow/deny │                     │
│                        └────────┬─────────┘                     │
│                                 ▼                                │
│                         ALLOW or DENY                           │
└─────────────────────────────────────────────────────────────────┘
```

## Go Implementation

```go
package abac

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Subject represents the user or entity requesting access.
type Subject struct {
	ID         string
	Role       string
	Department string
	Level      string
	Teams      []string
}

// Resource represents the resource being accessed.
type Resource struct {
	ID          string
	Type        string
	OwnerID     string
	Department  string
	Sensitivity string
	Status      string
}

// Environment represents the access context.
type Environment struct {
	Time              time.Time
	IP                string
	IsWorkHours       bool
	IsInternalNetwork bool
	DeviceType        string
}

// AccessRequest represents a complete access request.
type AccessRequest struct {
	Subject     Subject
	Resource    Resource
	Action      string
	Environment Environment
}

// Operator represents a comparison operator.
type Operator string

const (
	OpEqual    Operator = "eq"
	OpNotEqual Operator = "neq"
	OpIn       Operator = "in"
	OpContains Operator = "contains"
	OpGreater  Operator = "gt"
	OpLess     Operator = "lt"
	OpBetween  Operator = "between"
)

// Condition represents a policy condition.
type Condition struct {
	Attribute string
	Operator  Operator
	Value     interface{}
}

// Effect represents the policy effect.
type Effect string

const (
	EffectAllow Effect = "allow"
	EffectDeny  Effect = "deny"
)

// Policy represents an access control policy.
type Policy struct {
	ID          string
	Name        string
	Description string
	Effect      Effect
	Actions     []string
	Conditions  []Condition
	Priority    int
}

// Engine evaluates ABAC policies.
type Engine struct {
	policies []Policy
}

// NewEngine creates a new ABAC engine.
func NewEngine(policies []Policy) *Engine {
	// Sort by priority (lower = higher priority)
	sortedPolicies := make([]Policy, len(policies))
	copy(sortedPolicies, policies)

	for i := 0; i < len(sortedPolicies); i++ {
		for j := i + 1; j < len(sortedPolicies); j++ {
			if sortedPolicies[j].Priority < sortedPolicies[i].Priority {
				sortedPolicies[i], sortedPolicies[j] = sortedPolicies[j], sortedPolicies[i]
			}
		}
	}

	return &Engine{
		policies: sortedPolicies,
	}
}

// EvaluationResult represents the result of policy evaluation.
type EvaluationResult struct {
	Allowed bool
	Reason  string
}

// Evaluate evaluates an access request against policies.
func (e *Engine) Evaluate(ctx context.Context, request AccessRequest) EvaluationResult {
	for _, policy := range e.policies {
		// Check if action matches
		actionMatches := false
		for _, action := range policy.Actions {
			if action == "*" || action == request.Action {
				actionMatches = true
				break
			}
		}

		if !actionMatches {
			continue
		}

		// Check if all conditions match
		allMatch := true
		for _, cond := range policy.Conditions {
			if !e.evaluateCondition(cond, request) {
				allMatch = false
				break
			}
		}

		if allMatch {
			return EvaluationResult{
				Allowed: policy.Effect == EffectAllow,
				Reason:  fmt.Sprintf("Policy %q matched", policy.Name),
			}
		}
	}

	return EvaluationResult{
		Allowed: false,
		Reason:  "No matching policy (default deny)",
	}
}

func (e *Engine) evaluateCondition(condition Condition, request AccessRequest) bool {
	value := e.getAttribute(condition.Attribute, request)

	switch condition.Operator {
	case OpEqual:
		return value == condition.Value

	case OpNotEqual:
		return value != condition.Value

	case OpIn:
		if slice, ok := condition.Value.([]interface{}); ok {
			for _, v := range slice {
				if v == value {
					return true
				}
			}
		}
		return false

	case OpContains:
		if slice, ok := value.([]string); ok {
			for _, v := range slice {
				if v == condition.Value {
					return true
				}
			}
		}
		return false

	case OpGreater:
		if v1, ok := value.(int); ok {
			if v2, ok := condition.Value.(int); ok {
				return v1 > v2
			}
		}
		return false

	case OpLess:
		if v1, ok := value.(int); ok {
			if v2, ok := condition.Value.(int); ok {
				return v1 < v2
			}
		}
		return false

	case OpBetween:
		if v, ok := value.(int); ok {
			if bounds, ok := condition.Value.([]int); ok && len(bounds) == 2 {
				return v >= bounds[0] && v <= bounds[1]
			}
		}
		return false

	default:
		return false
	}
}

func (e *Engine) getAttribute(path string, request AccessRequest) interface{} {
	parts := strings.Split(path, ".")
	if len(parts) < 2 {
		return nil
	}

	switch parts[0] {
	case "subject":
		return e.getSubjectAttribute(parts[1:], request.Subject)
	case "resource":
		return e.getResourceAttribute(parts[1:], request.Resource)
	case "environment":
		return e.getEnvironmentAttribute(parts[1:], request.Environment)
	default:
		return nil
	}
}

func (e *Engine) getSubjectAttribute(path []string, subject Subject) interface{} {
	if len(path) == 0 {
		return nil
	}

	switch path[0] {
	case "id":
		return subject.ID
	case "role":
		return subject.Role
	case "department":
		return subject.Department
	case "level":
		return subject.Level
	case "teams":
		return subject.Teams
	default:
		return nil
	}
}

func (e *Engine) getResourceAttribute(path []string, resource Resource) interface{} {
	if len(path) == 0 {
		return nil
	}

	switch path[0] {
	case "id":
		return resource.ID
	case "type":
		return resource.Type
	case "ownerId":
		return resource.OwnerID
	case "department":
		return resource.Department
	case "sensitivity":
		return resource.Sensitivity
	case "status":
		return resource.Status
	default:
		return nil
	}
}

func (e *Engine) getEnvironmentAttribute(path []string, env Environment) interface{} {
	if len(path) == 0 {
		return nil
	}

	switch path[0] {
	case "time":
		return env.Time
	case "ip":
		return env.IP
	case "isWorkHours":
		return env.IsWorkHours
	case "isInternalNetwork":
		return env.IsInternalNetwork
	case "deviceType":
		return env.DeviceType
	default:
		return nil
	}
}
```

## Example Policies

```go
package abac

// ExamplePolicies returns example ABAC policies.
func ExamplePolicies() []Policy {
	return []Policy{
		// Deny all access to secret documents from personal devices
		{
			ID:          "deny-secret-personal",
			Name:        "Block secret access from personal devices",
			Description: "Secret documents only accessible from corporate devices",
			Effect:      EffectDeny,
			Actions:     []string{"*"},
			Conditions: []Condition{
				{Attribute: "resource.sensitivity", Operator: OpEqual, Value: "secret"},
				{Attribute: "environment.deviceType", Operator: OpEqual, Value: "personal"},
			},
			Priority: 1,
		},

		// Allow owners to access their own resources
		{
			ID:          "owner-access",
			Name:        "Owner full access",
			Description: "Resource owners have full access",
			Effect:      EffectAllow,
			Actions:     []string{"read", "update", "delete"},
			Conditions: []Condition{
				{Attribute: "resource.ownerId", Operator: OpEqual, Value: "$subject.id"},
			},
			Priority: 10,
		},

		// Allow same department read access
		{
			ID:          "dept-read",
			Name:        "Department read access",
			Description: "Users can read resources from their department",
			Effect:      EffectAllow,
			Actions:     []string{"read"},
			Conditions: []Condition{
				{Attribute: "subject.department", Operator: OpEqual, Value: "$resource.department"},
				{Attribute: "resource.sensitivity", Operator: OpIn, Value: []interface{}{"public", "internal"}},
			},
			Priority: 20,
		},

		// Senior employees can access confidential during work hours
		{
			ID:          "senior-confidential",
			Name:        "Senior confidential access",
			Description: "Senior employees can access confidential during work hours",
			Effect:      EffectAllow,
			Actions:     []string{"read"},
			Conditions: []Condition{
				{Attribute: "subject.level", Operator: OpIn, Value: []interface{}{"senior", "lead"}},
				{Attribute: "resource.sensitivity", Operator: OpEqual, Value: "confidential"},
				{Attribute: "environment.isWorkHours", Operator: OpEqual, Value: true},
				{Attribute: "environment.isInternalNetwork", Operator: OpEqual, Value: true},
			},
			Priority: 30,
		},
	}
}
```

## Dynamic Attribute Resolution

```go
package abac

// AttributeResolver is a function that resolves dynamic attributes.
type AttributeResolver func(AccessRequest) interface{}

// DynamicEngine extends Engine with dynamic attribute resolution.
type DynamicEngine struct {
	*Engine
	resolvers map[string]AttributeResolver
}

// NewDynamicEngine creates a new dynamic ABAC engine.
func NewDynamicEngine(policies []Policy) *DynamicEngine {
	return &DynamicEngine{
		Engine:    NewEngine(policies),
		resolvers: make(map[string]AttributeResolver),
	}
}

// RegisterResolver registers a dynamic attribute resolver.
func (d *DynamicEngine) RegisterResolver(attribute string, resolver AttributeResolver) {
	d.resolvers[attribute] = resolver
}

// getAttribute overrides the base method to support dynamic resolution.
func (d *DynamicEngine) getAttribute(path string, request AccessRequest) interface{} {
	// Check for dynamic resolvers
	if resolver, ok := d.resolvers[path]; ok {
		return resolver(request)
	}

	// Check for variable references ($subject.id)
	value := d.Engine.getAttribute(path, request)
	if str, ok := value.(string); ok && strings.HasPrefix(str, "$") {
		return d.Engine.getAttribute(strings.TrimPrefix(str, "$"), request)
	}

	return value
}

// Usage
func ExampleDynamicEngine() {
	engine := NewDynamicEngine(ExamplePolicies())

	// Dynamic resolver for team membership
	engine.RegisterResolver("subject.isTeamMember", func(request AccessRequest) interface{} {
		teamID, ok := request.Resource.ID.(string)
		if !ok {
			return false
		}
		for _, team := range request.Subject.Teams {
			if team == teamID {
				return true
			}
		}
		return false
	})
}
```

## HTTP Middleware

```go
package middleware

import (
	"context"
	"net/http"
	"time"
)

// ABACMiddleware returns ABAC authorization middleware.
func ABACMiddleware(engine *abac.Engine) func(string, string) func(http.Handler) http.Handler {
	return func(resourceType, action string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				user := GetUser(r.Context())
				resource := GetResource(r.Context())

				request := abac.AccessRequest{
					Subject:  user,
					Resource: resource,
					Action:   action,
					Environment: abac.Environment{
						Time:              time.Now(),
						IP:                r.RemoteAddr,
						IsWorkHours:       isWorkHours(time.Now()),
						IsInternalNetwork: isInternalIP(r.RemoteAddr),
						DeviceType:        getDeviceType(r),
					},
				}

				result := engine.Evaluate(r.Context(), request)

				if !result.Allowed {
					w.WriteHeader(http.StatusForbidden)
					fmt.Fprintf(w, `{"error": "Access denied", "reason": "%s"}`, result.Reason)
					return
				}

				next.ServeHTTP(w, r)
			})
		}
	}
}

func isWorkHours(t time.Time) bool {
	hour := t.Hour()
	return hour >= 9 && hour < 17 && t.Weekday() != time.Saturday && t.Weekday() != time.Sunday
}

func isInternalIP(ip string) bool {
	// Simplified - check if IP is in internal ranges
	return strings.HasPrefix(ip, "192.168.") || strings.HasPrefix(ip, "10.")
}

func getDeviceType(r *http.Request) string {
	// Simplified - check user agent or custom header
	return "corporate"
}
```

## Recommended Libraries

| Package | Usage |
|---------|-------|
| `github.com/casbin/casbin/v2` | Flexible policy engine |
| `github.com/open-policy-agent/opa/rego` | OPA (Rego language) |

## Common Mistakes

| Mistake | Impact | Solution |
|---------|--------|----------|
| Overly complex policies | Difficult maintenance | Decompose, document |
| No default deny | Security hole | Always default deny |
| Slow evaluation | Performance | Cache, indexing |
| Contradicting policies | Unpredictable behavior | Clear priorities |
| No audit | Compliance issues | Log all decisions |

## When to Use

| Scenario | Recommended |
|----------|------------|
| Contextual permissions | Yes |
| Complex multi-tenant | Yes |
| Compliance (GDPR, HIPAA) | Yes |
| Dynamic rules | Yes |
| Simple permissions | No (RBAC is sufficient) |
| High performance required | With caution (cache) |

## Related Patterns

- **RBAC**: ABAC can include role as an attribute
- **Policy-Based**: Declarative syntax for policies
- **JWT**: Transport attributes in claims

## Sources

- [NIST ABAC Guide](https://nvlpubs.nist.gov/nistpubs/specialpublications/NIST.SP.800-162.pdf)
- [XACML Standard](http://docs.oasis-open.org/xacml/3.0/xacml-3.0-core-spec-os-en.html)
