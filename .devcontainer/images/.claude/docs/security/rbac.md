# Role-Based Access Control (RBAC)

> Permissions based on roles assigned to users.

## Principle

```
┌──────────────────────────────────────────────────────────────┐
│                         RBAC Model                            │
│                                                               │
│   User ──────► Role ──────► Permission ──────► Resource      │
│                                                               │
│   Alice ──────► Admin ─────► create, read,  ──► /articles   │
│                              update, delete                   │
│                                                               │
│   Bob ────────► Editor ────► create, read,  ──► /articles   │
│                              update                           │
│                                                               │
│   Carol ──────► Viewer ────► read           ──► /articles   │
└──────────────────────────────────────────────────────────────┘
```

## Go Implementation

```go
package rbac

// Role represents a user role.
type Role string

const (
	RoleAdmin     Role = "admin"
	RoleEditor    Role = "editor"
	RoleViewer    Role = "viewer"
	RoleModerator Role = "moderator"
)

// Permission represents an action permission.
type Permission string

const (
	PermCreate   Permission = "create"
	PermRead     Permission = "read"
	PermUpdate   Permission = "update"
	PermDelete   Permission = "delete"
	PermPublish  Permission = "publish"
	PermModerate Permission = "moderate"
)

// Resource represents a resource type.
type Resource string

const (
	ResourceArticles  Resource = "articles"
	ResourceUsers     Resource = "users"
	ResourceComments  Resource = "comments"
	ResourceSettings  Resource = "settings"
)

// rolePermissions maps roles to their permissions per resource.
var rolePermissions = map[Role]map[Resource][]Permission{
	RoleAdmin: {
		ResourceArticles:  {PermCreate, PermRead, PermUpdate, PermDelete, PermPublish},
		ResourceUsers:     {PermCreate, PermRead, PermUpdate, PermDelete},
		ResourceComments:  {PermCreate, PermRead, PermUpdate, PermDelete, PermModerate},
		ResourceSettings:  {PermRead, PermUpdate},
	},
	RoleEditor: {
		ResourceArticles: {PermCreate, PermRead, PermUpdate, PermPublish},
		ResourceUsers:    {PermRead},
		ResourceComments: {PermCreate, PermRead, PermUpdate},
		ResourceSettings: {},
	},
	RoleViewer: {
		ResourceArticles: {PermRead},
		ResourceUsers:    {},
		ResourceComments: {PermCreate, PermRead},
		ResourceSettings: {},
	},
	RoleModerator: {
		ResourceArticles: {PermRead},
		ResourceUsers:    {PermRead},
		ResourceComments: {PermRead, PermDelete, PermModerate},
		ResourceSettings: {},
	},
}

// RBAC provides role-based access control.
type RBAC struct{}

// NewRBAC creates a new RBAC instance.
func NewRBAC() *RBAC {
	return &RBAC{}
}

// HasPermission checks if a role has a permission on a resource.
func (r *RBAC) HasPermission(role Role, resource Resource, permission Permission) bool {
	permissions, ok := rolePermissions[role][resource]
	if !ok {
		return false
	}

	for _, p := range permissions {
		if p == permission {
			return true
		}
	}

	return false
}

// GetPermissions returns all permissions for a role on a resource.
func (r *RBAC) GetPermissions(role Role, resource Resource) []Permission {
	return rolePermissions[role][resource]
}

// GetAllPermissions returns all permissions for a role.
func (r *RBAC) GetAllPermissions(role Role) map[Resource][]Permission {
	return rolePermissions[role]
}
```

## Role Hierarchy

```go
package rbac

// roleHierarchy defines role inheritance.
var roleHierarchy = map[Role][]Role{
	RoleAdmin:     {RoleEditor, RoleViewer, RoleModerator},
	RoleEditor:    {RoleViewer},
	RoleViewer:    {},
	RoleModerator: {RoleViewer},
}

// HierarchicalRBAC extends RBAC with role hierarchy.
type HierarchicalRBAC struct {
	*RBAC
}

// NewHierarchicalRBAC creates a new hierarchical RBAC.
func NewHierarchicalRBAC() *HierarchicalRBAC {
	return &HierarchicalRBAC{
		RBAC: NewRBAC(),
	}
}

// getInheritedRoles returns all roles inherited by a role.
func (h *HierarchicalRBAC) getInheritedRoles(role Role) []Role {
	inherited := map[Role]bool{role: true}
	queue := []Role{role}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		parents := roleHierarchy[current]
		for _, parent := range parents {
			if !inherited[parent] {
				inherited[parent] = true
				queue = append(queue, parent)
			}
		}
	}

	result := make([]Role, 0, len(inherited))
	for r := range inherited {
		result = append(result, r)
	}

	return result
}

// HasPermission checks permission with role inheritance.
func (h *HierarchicalRBAC) HasPermission(role Role, resource Resource, permission Permission) bool {
	roles := h.getInheritedRoles(role)
	for _, r := range roles {
		if h.RBAC.HasPermission(r, resource, permission) {
			return true
		}
	}
	return false
}
```

## HTTP Middleware

```go
package middleware

import (
	"fmt"
	"net/http"
)

// User represents an authenticated user.
type User struct {
	ID   string
	Role rbac.Role
}

// Authorize returns a middleware that checks RBAC permissions.
func Authorize(resource rbac.Resource, permission rbac.Permission) func(http.Handler) http.Handler {
	rbacEngine := rbac.NewHierarchicalRBAC()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := GetUser(r.Context())
			if user == nil {
				http.Error(w, "Not authenticated", http.StatusUnauthorized)
				return
			}

			if !rbacEngine.HasPermission(user.Role, resource, permission) {
				w.WriteHeader(http.StatusForbidden)
				fmt.Fprintf(w, `{
					"error": "Forbidden",
					"required": {"resource": "%s", "permission": "%s"},
					"userRole": "%s"
				}`, resource, permission, user.Role)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// Usage example
func SetupRoutes(mux *http.ServeMux) {
	mux.Handle("/articles",
		Authorize(rbac.ResourceArticles, rbac.PermRead)(http.HandlerFunc(listArticles)))
	mux.Handle("/articles/create",
		Authorize(rbac.ResourceArticles, rbac.PermCreate)(http.HandlerFunc(createArticle)))
	mux.Handle("/articles/update",
		Authorize(rbac.ResourceArticles, rbac.PermUpdate)(http.HandlerFunc(updateArticle)))
	mux.Handle("/articles/delete",
		Authorize(rbac.ResourceArticles, rbac.PermDelete)(http.HandlerFunc(deleteArticle)))
}
```

## Database Model

```go
package rbac

import (
	"context"
	"database/sql"
	"fmt"
)

// Permission represents a database permission.
type DBPermission struct {
	ID       string
	Name     string
	Resource string
	Action   string
}

// DBRole represents a database role.
type DBRole struct {
	ID          string
	Name        string
	Permissions []DBPermission
}

// DBUser represents a database user.
type DBUser struct {
	ID    string
	Roles []DBRole
}

// DatabaseRBAC provides database-backed RBAC.
type DatabaseRBAC struct {
	db *sql.DB
}

// NewDatabaseRBAC creates a new database-backed RBAC.
func NewDatabaseRBAC(db *sql.DB) *DatabaseRBAC {
	return &DatabaseRBAC{db: db}
}

// HasPermission checks if a user has a permission.
func (d *DatabaseRBAC) HasPermission(ctx context.Context, userID, resource, action string) (bool, error) {
	query := `
		SELECT 1 FROM user_roles ur
		JOIN role_permissions rp ON ur.role_id = rp.role_id
		JOIN permissions p ON rp.permission_id = p.id
		WHERE ur.user_id = $1
			AND p.resource = $2
			AND p.action = $3
		LIMIT 1
	`

	var exists int
	err := d.db.QueryRowContext(ctx, query, userID, resource, action).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("querying permission: %w", err)
	}

	return true, nil
}

// GetUserPermissions retrieves all permissions for a user.
func (d *DatabaseRBAC) GetUserPermissions(ctx context.Context, userID string) ([]DBPermission, error) {
	query := `
		SELECT DISTINCT p.id, p.name, p.resource, p.action
		FROM permissions p
		JOIN role_permissions rp ON p.id = rp.permission_id
		JOIN user_roles ur ON rp.role_id = ur.role_id
		WHERE ur.user_id = $1
	`

	rows, err := d.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("querying permissions: %w", err)
	}
	defer rows.Close()

	var permissions []DBPermission
	for rows.Next() {
		var p DBPermission
		if err := rows.Scan(&p.ID, &p.Name, &p.Resource, &p.Action); err != nil {
			return nil, fmt.Errorf("scanning permission: %w", err)
		}
		permissions = append(permissions, p)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating permissions: %w", err)
	}

	return permissions, nil
}
```

## Recommended Libraries

| Package | Usage |
|---------|-------|
| `github.com/casbin/casbin/v2` | Flexible RBAC/ABAC |
| `github.com/ory/ladon` | Access control policies |

## Common Mistakes

| Mistake | Impact | Solution |
|---------|--------|----------|
| Role explosion | Difficult maintenance | Hierarchy + granular permissions |
| Hardcoded roles | Inflexible | Store in DB |
| Check role instead of permission | Tight coupling | Always check permissions |
| No resource/action separation | Insufficient granularity | resource:action pattern |
| Roles per feature | Combinatorial explosion | Roles per responsibility |

## When to Use

| Scenario | Recommended |
|----------|------------|
| Applications with clear roles | Yes |
| Backoffice/Admin panels | Yes |
| Simple multi-tenant | Yes |
| Highly granular permissions | No (prefer ABAC) |
| Contextual permissions | No (prefer ABAC) |

## Related Patterns

- **ABAC**: Extension with attributes and context
- **JWT**: Role often in claims
- **Policy-Based**: Declarative, more flexible

## Sources

- [NIST RBAC](https://csrc.nist.gov/projects/role-based-access-control)
- [OWASP Access Control](https://owasp.org/www-community/Access_Control)
