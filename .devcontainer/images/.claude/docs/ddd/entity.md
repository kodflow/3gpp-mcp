# Entity Pattern

> Domain object with a distinct identity that persists through time and different representations.

## Definition

An **Entity** is a domain object with a distinct identity that runs through time and different representations. Unlike Value Objects, entities are distinguished by their identity, not their attributes.

```
Entity = Identity + State + Behavior + Lifecycle
```

**Key characteristics:**

- **Identity**: Unique identifier that persists across changes
- **Continuity**: Same entity even when attributes change
- **Lifecycle**: Creation, modification, and potentially deletion
- **Mutability**: State can change while identity remains constant

## Go Implementation

```go
package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Entity is a base type for domain entities with identity.
type Entity[TID comparable] struct {
	id TID
}

// NewEntity creates a new entity with the given ID.
func NewEntity[TID comparable](id TID) Entity[TID] {
	return Entity[TID]{id: id}
}

// ID returns the entity's identifier.
func (e Entity[TID]) ID() TID {
	return e.id
}

// Equals checks if two entities have the same identity.
func (e Entity[TID]) Equals(other Entity[TID]) bool {
	return e.id == other.id
}

// UserID is a strongly-typed identifier.
type UserID struct {
	value string
}

// NewUserID generates a new UserID.
func NewUserID() UserID {
	return UserID{value: uuid.New().String()}
}

// UserIDFrom creates a UserID from a string.
func UserIDFrom(value string) (UserID, error) {
	if value == "" {
		return UserID{}, errors.New("userID cannot be empty")
	}
	return UserID{value: value}, nil
}

// Value returns the underlying string value.
func (id UserID) Value() string {
	return id.value
}

// Equals checks UserID equality.
func (id UserID) Equals(other UserID) bool {
	return id.value == other.value
}

// UserStatus represents user account status.
type UserStatus string

const (
	UserStatusActive      UserStatus = "active"
	UserStatusDeactivated UserStatus = "deactivated"
)

// User is a domain entity with identity and behavior.
type User struct {
	Entity[UserID]
	email     Email
	name      Name
	status    UserStatus
	createdAt time.Time
	updatedAt time.Time
}

// NewUser creates a new active user.
func NewUser(email Email, name Name) User {
	id := NewUserID()
	now := time.Now()

	return User{
		Entity:    NewEntity(id),
		email:     email,
		name:      name,
		status:    UserStatusActive,
		createdAt: now,
		updatedAt: now,
	}
}

// Reconstitute recreates a User from persistence.
func ReconstituteUser(
	id UserID,
	email Email,
	name Name,
	status UserStatus,
	createdAt, updatedAt time.Time,
) User {
	return User{
		Entity:    NewEntity(id),
		email:     email,
		name:      name,
		status:    status,
		createdAt: createdAt,
		updatedAt: updatedAt,
	}
}

// ChangeEmail updates the user's email with invariant protection.
func (u *User) ChangeEmail(newEmail Email) error {
	if u.status == UserStatusDeactivated {
		return errors.New("cannot change email of deactivated user")
	}
	u.email = newEmail
	u.updatedAt = time.Now()
	return nil
}

// Deactivate marks the user as deactivated.
func (u *User) Deactivate() error {
	if u.status == UserStatusDeactivated {
		return errors.New("user already deactivated")
	}
	u.status = UserStatusDeactivated
	u.updatedAt = time.Now()
	return nil
}

// Email returns the user's email.
func (u User) Email() Email {
	return u.email
}

// Name returns the user's name.
func (u User) Name() Name {
	return u.name
}

// Status returns the user's status.
func (u User) Status() UserStatus {
	return u.status
}

// IsActive checks if the user is active.
func (u User) IsActive() bool {
	return u.status == UserStatusActive
}

// CreatedAt returns when the user was created.
func (u User) CreatedAt() time.Time {
	return u.createdAt
}

// UpdatedAt returns when the user was last updated.
func (u User) UpdatedAt() time.Time {
	return u.updatedAt
}
```

## OOP vs FP Comparison

| Aspect | OOP Entity | FP Entity |
|--------|-----------|-----------|
| Identity | Encapsulated in class | Separate ID type |
| State | Private mutable fields | Immutable record |
| Behavior | Instance methods | Pure functions |
| Updates | Mutate in place | Return new instance |

```go
// FP-style Entity using immutable patterns

// User is an immutable record.
type User struct {
	ID        UserID
	Email     Email
	Name      Name
	Status    UserStatus
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ChangeEmail returns a new User with updated email.
func ChangeEmail(user User, newEmail Email) (User, error) {
	if user.Status == UserStatusDeactivated {
		return User{}, errors.New("cannot change email of deactivated user")
	}

	return User{
		ID:        user.ID,
		Email:     newEmail,
		Name:      user.Name,
		Status:    user.Status,
		CreatedAt: user.CreatedAt,
		UpdatedAt: time.Now(),
	}, nil
}

// Deactivate returns a new User with deactivated status.
func Deactivate(user User) (User, error) {
	if user.Status == UserStatusDeactivated {
		return User{}, errors.New("user already deactivated")
	}

	return User{
		ID:        user.ID,
		Email:     user.Email,
		Name:      user.Name,
		Status:    UserStatusDeactivated,
		CreatedAt: user.CreatedAt,
		UpdatedAt: time.Now(),
	}, nil
}
```

## Recommended Libraries

| Library | Purpose | Link |
|---------|---------|------|
| **google/uuid** | ID generation | `go get github.com/google/uuid` |
| **oklog/ulid** | Sortable IDs | `go get github.com/oklog/ulid/v2` |
| **rs/xid** | Compact IDs | `go get github.com/rs/xid` |

## Anti-patterns

1. **Anemic Entity**: Entity with only getters/setters, no behavior

   ```go
   // BAD - No domain logic
   type User struct {
       ID     string
       Email  string // Public field!
       Name   string
   }
   ```

2. **Primitive Obsession**: Using primitives instead of Value Objects for identity

   ```go
   // BAD
   type User struct {
       ID string
   }

   // GOOD
   type User struct {
       ID UserID
   }
   ```

3. **Missing Invariant Protection**: Allowing invalid state transitions

   ```go
   // BAD - No validation
   user.Status = UserStatusDeactivated

   // GOOD - Controlled transition
   err := user.Deactivate()
   ```

4. **Identity Confusion**: Comparing entities by attributes instead of ID

   ```go
   // BAD
   user1.Email == user2.Email

   // GOOD
   user1.ID().Equals(user2.ID())
   ```

## When to Use

- The object must be tracked over time
- The object has a lifecycle (creation, modification, deletion)
- Two objects with the same attributes must be distinguishable
- Business operations depend on the object's history

## Related Patterns

- [Value Object](./value-object.md) - For objects defined by attributes
- [Aggregate](./aggregate.md) - For clustering entities
- [Repository](./repository.md) - For entity persistence
