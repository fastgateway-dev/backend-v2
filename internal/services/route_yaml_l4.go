package services

import (
	"errors"
	"fmt"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"sigs.k8s.io/yaml"
)

// generateL4BackendTrafficPolicyYAML renders the BackendTrafficPolicy
// deployL4 applies for an L4 route: the deploy path's config builder
// (routeplan.BuildBackendTrafficPolicyConfigForNamespace with the stream's
// namespace and ID) and the object builder the cluster client applies
// (kubernetes.BuildBackendTrafficPolicy). It returns "" exactly when
// deployL4 applies none: no policy, or an empty one.
func generateL4BackendTrafficPolicyYAML(route *models.Route, stream *models.Stream, policy *models.BackendTrafficPolicy) (string, error) {
	cfg := routeplan.BuildBackendTrafficPolicyConfigForNamespace(route, stream.Namespace, stream.ID.String(), policy)
	if cfg == nil {
		return "", nil
	}
	obj := kubernetes.BuildBackendTrafficPolicy(cfg)
	if obj == nil {
		return "", nil
	}
	out, err := yaml.Marshal(obj.Object)
	if err != nil {
		return "", fmt.Errorf("marshal L4 BackendTrafficPolicy YAML: %w", err)
	}
	return string(out), nil
}

// l4PolicyFromConfig wraps a policy config (from an approval snapshot or a
// create/update input) in the model the deploy builder takes. nil in, nil out.
func l4PolicyFromConfig(route *models.Route, cfg *models.BackendTrafficPolicyConfig) *models.BackendTrafficPolicy {
	if cfg == nil {
		return nil
	}
	id := route.ID
	return &models.BackendTrafficPolicy{RouteID: &id, Config: *cfg}
}

// storedBackendTrafficPolicy returns the route's persisted policy, nil when
// it has none. Unlike the HTTP previews it does not swallow a lookup
// failure: a read endpoint returns an error rather than a manifest set with
// the policy silently missing.
func (q *routeQuery) storedBackendTrafficPolicy(routeID uuid.UUID) (*models.BackendTrafficPolicy, error) {
	policy, err := q.backendTrafficPolicyRepo.GetByRouteID(routeID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load backend traffic policy for route %s: %w", routeID, err)
	}
	return policy, nil
}

// generateL4RouteYAMLs is GenerateYAMLs for an L4 route: the TCPRoute/UDPRoute
// in HTTPRouteYAML (the response shape is unchanged) and its
// BackendTrafficPolicy.
func (q *routeQuery) generateL4RouteYAMLs(route *models.Route) (*RouteYAMLs, error) {
	stream, err := q.l4Stream(route)
	if err != nil {
		return nil, err
	}
	routeYAML, err := q.renderL4RouteYAML(route, stream)
	if err != nil {
		return nil, err
	}
	policy, err := q.storedBackendTrafficPolicy(route.ID)
	if err != nil {
		return nil, err
	}
	btpYAML, err := generateL4BackendTrafficPolicyYAML(route, stream, policy)
	if err != nil {
		return nil, err
	}
	return &RouteYAMLs{HTTPRouteYAML: routeYAML, BackendTrafficPolicyYAML: btpYAML}, nil
}

// RenderL4RouteYAML renders an L4 route's TCPRoute/UDPRoute on stream through
// the deploy builders. ApprovalService uses it for the L4 approval diff.
func (s *RouteService) RenderL4RouteYAML(route *models.Route, stream *models.Stream) (string, error) {
	return s.query.renderL4RouteYAML(route, stream)
}

// RenderL4BackendTrafficPolicyYAML renders the BackendTrafficPolicy deployL4
// would apply for route on stream with the given policy config ("" for none).
func (s *RouteService) RenderL4BackendTrafficPolicyYAML(route *models.Route, stream *models.Stream, cfg *models.BackendTrafficPolicyConfig) (string, error) {
	return generateL4BackendTrafficPolicyYAML(route, stream, l4PolicyFromConfig(route, cfg))
}

// btpConfigFromInput is the policy config a create/update would persist for
// input (the same ten fields persistCreatePolicies/persistUpdatePolicies
// copy), or nil when the input carries no policy.
func btpConfigFromInput(in *routeplan.BackendTrafficPolicyInput) *models.BackendTrafficPolicyConfig {
	if in == nil || !in.HasContent() {
		return nil
	}
	return &models.BackendTrafficPolicyConfig{
		Compression:      in.Compression,
		Retry:            in.Retry,
		LoadBalancer:     in.LoadBalancer,
		CircuitBreaker:   in.CircuitBreaker,
		HealthCheck:      in.HealthCheck,
		FaultInjection:   in.FaultInjection,
		RateLimit:        in.RateLimit,
		RequestBuffer:    in.RequestBuffer,
		ResponseOverride: in.ResponseOverride,
		Timeout:          in.Timeout,
	}
}
