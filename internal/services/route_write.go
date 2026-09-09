package services

import (
	"encoding/json"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/google/uuid"
)

// CreateRouteInput represents input for creating a route
type CreateRouteInput struct {
	Name                 string                               `json:"name" binding:"required,min=1,max=63"`
	Description          string                               `json:"description"`
	Protocol             models.RouteProtocol                 `json:"protocol"`
	SecurityMode         models.SecurityMode                  `json:"securityMode"`
	TeamID               uuid.UUID                            `json:"teamId" binding:"required"`
	Config               models.RouteConfig                   `json:"config" binding:"required"`
	SecurityPolicy       *routeplan.SecurityPolicyInput       `json:"securityPolicy,omitempty"`       // Optional security policy (CORS, auth)
	BackendTrafficPolicy *routeplan.BackendTrafficPolicyInput `json:"backendTrafficPolicy,omitempty"` // Optional backend traffic policy (compression)
	ExtensionPolicy      *routeplan.EnvoyExtensionPolicyInput `json:"extensionPolicy,omitempty"`      // Optional extension policy (Lua, Wasm)
	WafPolicy            *routeplan.WafPolicyInput            `json:"wafPolicy,omitempty"`            // Optional WAF policy
	ChangeDescription    string                               `json:"changeDescription,omitempty"`
	AIReview             json.RawMessage                      `json:"aiReview,omitempty"`
	Labels               models.Labels                        `json:"labels,omitempty"`
}

// UpdateRouteInput represents input for updating a route
type UpdateRouteInput struct {
	Description          string                               `json:"description"`
	Config               models.RouteConfig                   `json:"config" binding:"required"`
	SecurityPolicy       *routeplan.SecurityPolicyInput       `json:"securityPolicy,omitempty"`       // Optional security policy (CORS, auth)
	BackendTrafficPolicy *routeplan.BackendTrafficPolicyInput `json:"backendTrafficPolicy,omitempty"` // Optional backend traffic policy (compression)
	ExtensionPolicy      *routeplan.EnvoyExtensionPolicyInput `json:"extensionPolicy,omitempty"`      // Optional extension policy (Lua, Wasm)
	WafPolicy            *routeplan.WafPolicyInput            `json:"wafPolicy,omitempty"`            // Optional WAF policy
	ChangeDescription    string                               `json:"changeDescription,omitempty"`
	AIReview             json.RawMessage                      `json:"aiReview,omitempty"`
	Labels               models.Labels                        `json:"labels,omitempty"`
}
