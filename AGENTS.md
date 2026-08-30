# FastGateway Backend - AI Agent Context

## Project Overview

FastGateway is a web service that abstracts the Kubernetes Gateway API, providing a UI-driven approach to manage Gateway resources. The backend is a Go REST API using Gin framework with PostgreSQL database.

## Tech Stack

- **Language**: Go 1.25+
- **Framework**: Gin (HTTP router)
- **Database**: PostgreSQL with GORM
- **Migrations**: golang-migrate
- **Authentication**: JWT (access + refresh tokens)
- **Kubernetes**: client-go with dynamic client

## Directory Structure

```
.
├── cmd/
│   ├── server/main.go      # Application entry point
│   ├── migrate/            # Migration CLI tool
│   └── seed/               # Database seeding
├── internal/
│   ├── config/             # Configuration loading (env vars)
│   ├── database/           # Database connection and migrations
│   ├── handlers/           # HTTP request handlers (controllers)
│   ├── middleware/         # Auth, CORS, logging middleware
│   ├── models/             # GORM models (database entities)
│   ├── repository/         # Data access layer
│   └── services/           # Business logic layer
├── migrations/             # SQL migration files
└── go.mod
```

## Architecture Pattern

The codebase follows a layered architecture:

1. **Handlers** (`internal/handlers/`) - Handle HTTP requests, parse input, call services
2. **Services** (`internal/services/`) - Business logic, orchestration
3. **Repository** (`internal/repository/`) - Database operations via GORM
4. **Models** (`internal/models/`) - Data structures and GORM models

## Key Concepts

### Kubernetes Integration

- All Gateway API objects (Gateway, HTTPRoute) are deployed to `fastgateway-system` namespace
- The `KubernetesService` (`internal/services/kubernetes_service.go`) handles all K8s operations
- Projects store encrypted K8s tokens for cluster access
- Use the constant `FastGatewayNamespace = "fastgateway-system"` for namespace references

### Authentication & Permissions

- JWT-based authentication with access and refresh tokens
- System roles: `owner` (full access), `user` (team-based permissions)
- 24 granular permissions: route.*, client.*, domain.*, project.*
- Permission presets: Viewer, Editor, Approver, Admin, custom
- Teams assigned to projects with presets via `project_team_roles` + `project_team_presets`
- Middleware in `internal/middleware/auth.go` and `internal/middleware/permissions.go`

### Audit Logging

- All mutations should be logged via `AuditService.LogAction()`
- Audit logs track: user, action, resource type, resource ID, IP address
- Project-scoped logs have projectID; system-wide logs (users) have nil projectID

### Unified Approval System

- Routes and client attachments use unified multi-stage approvals
- Stages defined by: required_permission, team_scope (any, other_team, submitter_team)
- Submitter cannot self-approve; stages must be approved sequentially
- Route statuses: `pending_create`, `pending_update`, `pending_delete`, `approved`, `active`, `rejected`
- Security modes: `general` (route-level auth) vs `client` (per-client attachments)

## Coding Conventions

### Error Handling

```go
if err != nil {
    c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
    return
}
```

### Handler Pattern

```go
func (h *Handler) Create(c *gin.Context) {
    user := middleware.GetCurrentUser(c)  // Get authenticated user

    var input services.CreateInput
    if err := c.ShouldBindJSON(&input); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }

    result, err := h.service.Create(&input)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }

    // Audit log
    h.auditService.LogAction(...)

    c.JSON(http.StatusCreated, result)
}
```

### Service Pattern

```go
type CreateInput struct {
    Name string `json:"name" binding:"required"`
}

func (s *Service) Create(input *CreateInput) (*models.Model, error) {
    // Business logic here
    return s.repo.Create(model)
}
```

## Database Migrations

- Located in `migrations/` directory
- Format: `NNNNNN_description.up.sql` and `NNNNNN_description.down.sql`
- Migrations run automatically on server startup via `database.RunMigrations()`

## Environment Variables

Key configuration (see `internal/config/config.go`):

- `DATABASE_URL` - PostgreSQL connection string
- `JWT_SECRET` - Secret for JWT signing
- `ENCRYPTION_KEY` - 32-byte key for K8s token encryption (AES-256)
- `API_PORT` - Server port (default: 8081)
- `CORS_ALLOWED_ORIGINS` - Comma-separated allowed origins

## Testing

```bash
go test ./...
```

## Building

```bash
go build -o fastgateway ./cmd/server
```

## Common Tasks

### Adding a New Resource

1. Create model in `internal/models/`
2. Create repository in `internal/repository/`
3. Create service in `internal/services/`
4. Create handler in `internal/handlers/`
5. Register routes in `cmd/server/main.go`
6. Add audit logging to mutations

### Adding Audit Logging

```go
h.auditService.LogAction(
    &projectID,           // nil for system-wide resources
    currentUser,          // from middleware.GetCurrentUser(c)
    "create",             // action: create, update, delete
    "resource_type",      // e.g., "user", "project", "route"
    &resourceID,          // UUID of the resource
    resourceName,         // Human-readable name
    map[string]interface{}{...}, // Additional details
    c.ClientIP(),
    c.Request.UserAgent(),
)
```

## Important Files

- `cmd/server/main.go` - Route registration and dependency injection
- `internal/services/kubernetes_service.go` - K8s resource creation (HTTPRoute, GRPCRoute, SecurityPolicy, BackendTrafficPolicy)
- `internal/services/route_service.go` - Route management with security modes
- `internal/services/approval_service.go` - Unified multi-stage approval system
- `internal/services/client_attachment_service.go` - Client attachments with approvals
- `internal/middleware/auth.go` - Authentication middleware
- `internal/middleware/permissions.go` - Permission checking (24 permissions, presets)
- `internal/models/team.go` - All permissions and preset definitions
