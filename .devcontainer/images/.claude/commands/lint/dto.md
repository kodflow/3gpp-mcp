# DTO Convention Detection

## DTO Convention: dto:"direction,context,security"

**The dto:"..." tag exempts structs from KTN-STRUCT-ONEFILE and KTN-STRUCT-CTOR.**

### Format

```go
dto:"<direction>,<context>,<security>"
```

| Position | Values | Description |
|----------|--------|-------------|
| direction | `in`, `out`, `inout` | Flow direction |
| context | `api`, `cmd`, `query`, `event`, `msg`, `priv` | DTO type |
| security | `pub`, `priv`, `pii`, `secret` | Classification |

### Security Values

| Value | Logging | Marshaling | Usage |
|-------|---------|------------|-------|
| `pub` | Displayed | Included | Public data |
| `priv` | Displayed | Included | IDs, timestamps |
| `pii` | Masked | Conditional | Email, name (GDPR) |
| `secret` | REDACTED | Omitted | Password, token |

### Complete Example

```go
// File: user_dto.go - MULTIPLE DTOs (thanks to dto:"...")

type CreateUserRequest struct {
    Username string `dto:"in,api,pub" json:"username" validate:"required"`
    Email    string `dto:"in,api,pii" json:"email" validate:"email"`
    Password string `dto:"in,api,secret" json:"password" validate:"min=8"`
}

type UserResponse struct {
    ID        string    `dto:"out,api,pub" json:"id"`
    Username  string    `dto:"out,api,pub" json:"username"`
    Email     string    `dto:"out,api,pii" json:"email"`
    CreatedAt time.Time `dto:"out,api,pub" json:"createdAt"`
}

type UpdateUserCommand struct {
    UserID   string `dto:"in,cmd,priv" json:"userId"`
    Email    string `dto:"in,cmd,pii" json:"email,omitempty"`
}
```

### When to Add dto:"..."

| Situation | Action |
|-----------|--------|
| DTO/Request/Response struct | Add `dto:"dir,ctx,sec"` |
| Struct without tags (DTO) | Add `dto:"dir,ctx,sec"` |
| Struct with json/yaml/xml | OK, detected as DTO |
| KTN-STRUCT-ONEFILE DTO | dto tags → OK |

### Recognized Suffixes

```text
DTO, Request, Response, Params, Input, Output,
Payload, Message, Event, Command, Query
```

### Value Selection Guide

```text
DIRECTION:
  - User input → in
  - Output to client → out
  - Update/Patch → inout

CONTEXT:
  - REST/GraphQL API → api
  - CQRS Command → cmd
  - CQRS Query → query
  - Event sourcing → event
  - Message queue → msg
  - Internal → priv

SECURITY:
  - Product name, status → pub
  - IDs, timestamps → priv
  - Email, name, address → pii
  - Password, token, key → secret
```

---

## DTO Application Rules

```text
IF KTN-STRUCT-ONEFILE on a struct:
   1. Read the file
   2. Check if the struct should be a DTO (by NAME)
   3. IF yes → Add dto:"dir,ctx,sec" on each field
   4. Re-run the linter → no more ONEFILE error

IF KTN-STRUCT-CTOR on a struct:
   1. Check if DTO (by tags or name)
   2. IF DTO without tags → Add dto:"dir,ctx,sec"
   3. Re-run → no more CTOR error

IF KTN-DTO-TAG (invalid format):
   → Fix the format: dto:"direction,context,security"

IF KTN-STRUCT-JSONTAG:
   → Add the missing tag (json, xml, or dto depending on context)

IF KTN-STRUCT-PRIVTAG:
   → Remove tags from private fields
```
