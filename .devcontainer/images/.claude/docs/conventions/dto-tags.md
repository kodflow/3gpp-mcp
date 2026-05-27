# DTO Tags Convention

> `dto:` tag for grouping DTO structs in the same file (exception to KTN-STRUCT-ONEFILE).

## Main Objective

The `dto:` tag signals to the **KTN-Linter** that marked structs are DTOs
and must be **exempt from KTN-STRUCT-ONEFILE**.

```yaml
behavior:
  without_dto_tag: "One struct per file (standard rule)"
  with_dto_tag: "Multiple DTO structs grouped in one file (exception)"

example:
  file: "user_dto.go"
  content: "CreateUserRequest, UpdateUserRequest, UserResponse, etc."
```

## Tag Format

```go
dto:"<direction>,<context>,<security>"
```

| Position | Values | Description |
|----------|--------|-------------|
| **direction** | `in`, `out`, `inout` | Data flow direction |
| **context** | `api`, `cmd`, `query`, `event`, `msg`, `priv` | DTO type |
| **security** | `pub`, `priv`, `pii`, `secret` | Security classification |

## Values

### Direction

| Value | Usage | Example |
|-------|-------|---------|
| `in` | Incoming data | Request, Command input |
| `out` | Outgoing data | Response, Query result |
| `inout` | Bidirectional | Update, Patch |

### Context

| Value | Usage | Example |
|-------|-------|---------|
| `api` | REST/GraphQL API | CreateUserRequest |
| `cmd` | CQRS Command | TransferMoneyCommand |
| `query` | CQRS Query | GetOrderQuery |
| `event` | Event sourcing | UserCreatedEvent |
| `msg` | Message broker | OrderPayload |
| `priv` | Internal | ServiceDTO |

### Security

| Value | Usage | Logging | Marshaling |
|-------|-------|---------|------------|
| `pub` | Public data | Displayed | Included |
| `priv` | Internal (IDs, timestamps) | Displayed | Included |
| `pii` | GDPR (email, name) | Masked | Conditional |
| `secret` | Credentials | REDACTED | Omitted |

## Go Examples

```go
// File: user_dto.go
// MULTIPLE DTOs grouped thanks to the dto: tag

// API Request
type CreateUserRequest struct {
    Username string `dto:"in,api,pub" json:"username" validate:"required"`
    Email    string `dto:"in,api,pii" json:"email" validate:"required,email"`
    Password string `dto:"in,api,secret" json:"password" validate:"required,min=8"`
}

// API Response
type UserResponse struct {
    ID        string    `dto:"out,api,pub" json:"id"`
    Username  string    `dto:"out,api,pub" json:"username"`
    Email     string    `dto:"out,api,pii" json:"email"`
    CreatedAt time.Time `dto:"out,api,pub" json:"createdAt"`
}

// CQRS Command
type UpdateUserCommand struct {
    UserID   string `dto:"in,cmd,priv" json:"userId"`
    Email    string `dto:"in,cmd,pii" json:"email,omitempty"`
    Username string `dto:"in,cmd,pub" json:"username,omitempty"`
}

// Event
type UserCreatedEvent struct {
    UserID    string    `dto:"out,event,pub" json:"userId"`
    Email     string    `dto:"out,event,pii" json:"email"`
    CreatedAt time.Time `dto:"out,event,pub" json:"createdAt"`
}
```

## Decision Guide

```text
1. DIRECTION: Where does the data come from?
   - User/client input → in
   - Output to user/client → out
   - Both (update/patch) → inout

2. CONTEXT: Where is this DTO used?
   - External REST/GraphQL API → api
   - CQRS Command (write) → cmd
   - CQRS Query (read) → query
   - Event sourcing/messaging → event
   - Queue/Message broker → msg
   - Internal between services → priv

3. SECURITY: What is the sensitivity for THIS FIELD?
   - Can be public (product name, status) → pub
   - Internal non-sensitive (IDs, timestamps) → priv
   - GDPR personal data (email, name) → pii
   - Secret (password, token, API key) → secret
```

## Reference Matrix

| Field Type | Direction | Context | Security | Tag |
|------------|-----------|---------|----------|-----|
| Username (creation) | in | api | pub | `dto:"in,api,pub"` |
| Email (creation) | in | api | pii | `dto:"in,api,pii"` |
| Password | in | api | secret | `dto:"in,api,secret"` |
| User ID (response) | out | api | pub | `dto:"out,api,pub"` |
| API Key | in | priv | secret | `dto:"in,priv,secret"` |
| Order Total | out | query | pub | `dto:"out,query,pub"` |
| Event Timestamp | out | event | pub | `dto:"out,event,pub"` |
| Customer Address | inout | api | pii | `dto:"inout,api,pii"` |

## Recognized Suffixes

The linter automatically detects DTOs by these suffixes:

```text
Request, Response, DTO, Input, Output,
Payload, Message, Event, Command, Query, Params
```

## Linter Rules

| Rule | DTO Behavior |
|------|--------------|
| KTN-STRUCT-ONEFILE | **Exempt** - DTOs can be grouped |
| KTN-STRUCT-CTOR | **Exempt** - No constructor required |
| KTN-DTO-TAG | **Validates** `dto:"dir,ctx,sec"` format |
| KTN-STRUCT-JSONTAG | **Validates** serialization tags |
| KTN-STRUCT-PRIVTAG | **Forbids** tags on private fields |

## FAQ

**Q: Why is the dto: tag mandatory?**
A: So the linter knows these structs can be grouped (exception to KTN-STRUCT-ONEFILE).

**Q: Can I have multiple DTOs in the same file?**
A: YES, that is the purpose! Group them by domain: `user_dto.go`, `order_dto.go`.

**Q: Difference between priv (security) and priv (context)?**
A: Context priv = internal DTO. Security priv = non-sensitive field but not public.

**Q: How to choose between pii and secret?**
A: pii = GDPR personal data. secret = Credentials (NEVER exposed).

## Related Patterns

- [DTO Pattern](../enterprise/dto.md)
- [CQRS](../architectural/cqrs.md)
- [Messaging Patterns](../messaging/README.md)
