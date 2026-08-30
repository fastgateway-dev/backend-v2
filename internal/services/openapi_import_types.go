package services

import "github.com/fastgateway-dev/backend-v2/internal/models"

// OpenAPIImportRequest is the incoming HTTP body for the OpenAPI import endpoint.
type OpenAPIImportRequest struct {
	Spec           string         `json:"spec" binding:"required"`
	DefaultBackend DefaultBackend `json:"defaultBackend" binding:"required"`
}

// DefaultBackend is the user-supplied backend applied to every parsed route.
// Exactly one of (Service+Namespace) or Address must be provided.
type DefaultBackend struct {
	Service   string `json:"service,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Address   string `json:"address,omitempty"`
	Port      int    `json:"port" binding:"required"`
}

// OpenAPIImportResponse is the success response shape.
type OpenAPIImportResponse struct {
	Routes   []ParsedRoute   `json:"routes"`
	Warnings []ImportWarning `json:"warnings"`
	Renames  []Rename        `json:"renames"`
	SpecInfo SpecInfo        `json:"specInfo"`
}

// ParsedRoute mirrors the frontend AIGeneratedRoute shape so the review UI is
// agnostic to the import source.
type ParsedRoute struct {
	Name         string             `json:"name"`
	Description  string             `json:"description,omitempty"`
	Protocol     string             `json:"protocol"`     // always "http" v1
	SecurityMode string             `json:"securityMode"` // always "general" v1
	Config       models.RouteConfig `json:"config"`
	Tag          string             `json:"tag,omitempty"`
}

// ImportWarning surfaces parser-level info or per-operation skips.
type ImportWarning struct {
	Level   string `json:"level"`  // "info" | "warning"
	Source  string `json:"source"` // e.g. "POST /webhook" or "spec"
	Message string `json:"message"`
}

// Rename records when a generated name was changed from its source-derived form.
type Rename struct {
	Original string `json:"original"`
	Final    string `json:"final"`
	Reason   string `json:"reason"` // "duplicate" | "sanitized" | "truncated"
}

// SpecInfo summarises the input spec for UI display.
type SpecInfo struct {
	Title   string `json:"title"`
	Version string `json:"version"`
	Format  string `json:"format"` // "openapi-3.0" | "openapi-3.1"
}
