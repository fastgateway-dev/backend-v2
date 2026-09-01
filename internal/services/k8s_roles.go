package services

import (
	"context"

	"github.com/fastgateway-dev/backend-v2/internal/cluster"
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Role interfaces over the Kubernetes cluster client.
//
// Phase 2E Task 7 replaced KubernetesServiceInterface -- one interface with
// 58 methods that ten consumers depended on in full -- with the roles below.
// Membership was derived from the actual call sites recorded in Task 1's
// inventory, not from method-name verbs: the heaviest consumer
// (RouteService) called 21 distinct methods and project_service.go called
// exactly one, yet both declared all 58.
//
// Two rules govern this file:
//
//  1. No role exceeds twelve methods. A role that grows past that has
//     stopped being a role and is drifting back towards the union.
//  2. A consumer names the roles it calls and no others. Taking a role
//     whose methods are never invoked re-encodes the over-declaration this
//     split exists to remove.
//
// The concrete implementation of every role is *cluster.Client -- not a
// services type. The compile-time assertions at the bottom of this file
// replace the single `var _ KubernetesServiceInterface = (*cluster.Client)(nil)`
// that used to live in interfaces.go.
//
// Eleven methods of *cluster.Client appear in no role at all
// (EnsureNamespace, TestConnection, GetSecretData, GetReferenceGrant,
// GetAPIKeyFromSecret, ValidateEnvoyGatewayInstalled,
// UpdateClientTrafficPolicy, CreateBackend, CreateSecurityPolicy,
// CreateBackendTrafficPolicy, CreateEnvoyExtensionPolicy): no consumer in
// internal/ calls them, so nothing declares them.

// RouteApplier writes the Gateway API objects that make up a route: the
// HTTPRoute or GRPCRoute itself, plus the HTTPRouteFilter and ConfigMap that
// back a direct-response route.
//
// Consumer: RouteService (route_deploy.go, route_clients_apikey.go).
type RouteApplier interface {
	CreateHTTPRoute(ctx context.Context, projectID uuid.UUID, config *kubernetes.HTTPRouteConfig) error
	UpdateHTTPRoute(ctx context.Context, projectID uuid.UUID, config *kubernetes.HTTPRouteConfig) error
	DeleteHTTPRoute(ctx context.Context, projectID uuid.UUID, namespace, name string) error
	CreateGRPCRoute(ctx context.Context, projectID uuid.UUID, config *kubernetes.GRPCRouteConfig) error
	UpdateGRPCRoute(ctx context.Context, projectID uuid.UUID, config *kubernetes.GRPCRouteConfig) error
	DeleteGRPCRoute(ctx context.Context, projectID uuid.UUID, namespace, name string) error
	ApplyHTTPRouteFilter(ctx context.Context, projectID uuid.UUID, config *kubernetes.HTTPRouteFilterConfig) error
	DeleteHTTPRouteFilter(ctx context.Context, projectID uuid.UUID, namespace, name string) error
	ApplyDirectResponseConfigMap(ctx context.Context, projectID uuid.UUID, config *kubernetes.DirectResponseConfigMapConfig) error
	DeleteDirectResponseConfigMap(ctx context.Context, projectID uuid.UUID, namespace, name string) error
}

// TrafficPolicyApplier writes the two Envoy Gateway policies that attach to a
// backend rather than to a listener: BackendTrafficPolicy and
// EnvoyExtensionPolicy.
//
// Consumers: DomainService (domain_service.go), and RouteService through
// PolicyApplier.
type TrafficPolicyApplier interface {
	UpdateBackendTrafficPolicy(ctx context.Context, projectID uuid.UUID, config *kubernetes.BackendTrafficPolicyConfig) error
	DeleteBackendTrafficPolicy(ctx context.Context, projectID uuid.UUID, namespace, name string) error
	UpdateEnvoyExtensionPolicy(ctx context.Context, projectID uuid.UUID, policy *unstructured.Unstructured) error
	DeleteEnvoyExtensionPolicy(ctx context.Context, projectID uuid.UUID, namespace, name string) error
}

// PolicyApplier is TrafficPolicyApplier plus the SecurityPolicy operations.
// Only route deployment writes SecurityPolicies, so DomainService takes the
// narrower TrafficPolicyApplier instead of this.
//
// Consumer: RouteService (route_deploy.go, route_clients_apikey.go).
type PolicyApplier interface {
	TrafficPolicyApplier

	UpdateSecurityPolicy(ctx context.Context, projectID uuid.UUID, config *kubernetes.SecurityPolicyConfig) error
	DeleteSecurityPolicy(ctx context.Context, projectID uuid.UUID, namespace, name string) error
}

// BackendApplier writes individual Envoy Gateway Backend resources.
//
// Consumers: RouteService (route_deploy.go), DomainService
// (domain_service.go).
type BackendApplier interface {
	UpdateBackend(ctx context.Context, projectID uuid.UUID, config *kubernetes.BackendConfig) error
	DeleteBackend(ctx context.Context, projectID uuid.UUID, namespace, name string) error
	UpdateBackendUnstructured(ctx context.Context, projectID uuid.UUID, backend *unstructured.Unstructured) error
}

// RouteBackendReaper removes the Backends owned by a route in bulk -- all of
// them on delete, the no-longer-referenced ones on update. Only route
// deployment owns Backends by route, which is why this is separate from
// BackendApplier.
//
// Consumer: RouteService (route_deploy.go).
type RouteBackendReaper interface {
	DeleteBackendsByRoute(ctx context.Context, projectID uuid.UUID, namespace, routeID string) error
	DeleteStaleBackendsByRoute(ctx context.Context, projectID uuid.UUID, namespace, routeID string, expectedNames map[string]bool) error
}

// SecretWriter creates and deletes ordinary Kubernetes Secrets -- client mTLS
// CAs and domain TLS material.
//
// Consumers: RouteService (route_clients_apikey.go), ClientService
// (client_service.go), and DomainService through Secrets.
type SecretWriter interface {
	CreateOrUpdateSecret(ctx context.Context, projectID uuid.UUID, namespace, name string, data map[string][]byte) error
	DeleteSecret(ctx context.Context, projectID uuid.UUID, namespace, name string) error
}

// Secrets is SecretWriter plus the TLS-secret listing the domain UI offers
// when a user picks a certificate.
//
// Consumer: DomainService (domain_service.go).
type Secrets interface {
	SecretWriter

	ListTLSSecrets(ctx context.Context, projectID uuid.UUID, namespace string) ([]cluster.TLSSecretInfo, error)
}

// APIKeySecretApplier writes the per-client API key Secrets a route deploy
// needs, and reaps the ones left behind by clients that are no longer
// attached.
//
// Consumer: RouteService (route_clients_apikey.go).
type APIKeySecretApplier interface {
	GetAPIKeySecretName(clientID uuid.UUID) string
	CreateAPIKeySecret(ctx context.Context, projectID uuid.UUID, clientID uuid.UUID, apiKey string) error
	DeleteStaleAPIKeyResources(ctx context.Context, projectID uuid.UUID, namespace, routeID, baseRouteName string, expectedClientPrefixes map[string]bool) error
}

// APIKeySecretDeleter removes a client's API key Secret when the key is
// revoked or the client is deleted. Deletion is all ClientService does with
// API key secrets; creation happens at deploy time through
// APIKeySecretApplier.
//
// Consumer: ClientService (client_service.go).
type APIKeySecretDeleter interface {
	DeleteAPIKeySecret(ctx context.Context, projectID uuid.UUID, clientID uuid.UUID) error
}

// ReferenceGrantChecker answers whether a ReferenceGrant already exists.
// RouteService only ever asks; it never writes one.
//
// Consumers: RouteService (route_write.go), and ProjectNamespaceService
// through ReferenceGrants.
type ReferenceGrantChecker interface {
	ReferenceGrantExists(ctx context.Context, projectID uuid.UUID, namespace, name string) (bool, error)
}

// ReferenceGrantResetter deletes a ReferenceGrant or replaces it wholesale.
// DomainService does both when a domain's namespace changes, but never
// creates one from scratch.
//
// Consumers: DomainService (domain_service.go), and ProjectNamespaceService
// through ReferenceGrants.
type ReferenceGrantResetter interface {
	DeleteReferenceGrant(ctx context.Context, projectID uuid.UUID, namespace, name string) error
	RecreateReferenceGrant(ctx context.Context, projectID uuid.UUID, config *cluster.ReferenceGrantConfig) error
}

// ReferenceGrants is the full ReferenceGrant lifecycle: check, create,
// delete, recreate. Only the service that owns project namespaces needs all
// four.
//
// Consumer: ProjectNamespaceService (project_namespace_service.go).
type ReferenceGrants interface {
	ReferenceGrantChecker
	ReferenceGrantResetter

	CreateReferenceGrant(ctx context.Context, projectID uuid.UUID, config *cluster.ReferenceGrantConfig) error
}

// GatewayApplier writes the listener-level objects a domain owns: the Gateway
// itself and the ClientTrafficPolicy that carries its mTLS configuration.
//
// Consumer: DomainService (domain_service.go).
type GatewayApplier interface {
	CreateGateway(ctx context.Context, projectID uuid.UUID, config *kubernetes.GatewayConfig) error
	DeleteGateway(ctx context.Context, projectID uuid.UUID, namespace, name string) error
	CreateClientTrafficPolicy(ctx context.Context, projectID uuid.UUID, config *kubernetes.ClientTrafficPolicyConfig) error
	DeleteClientTrafficPolicy(ctx context.Context, projectID uuid.UUID, namespace, name string) error
}

// GatewayClassApplier writes the cluster-scoped pair a domain template owns:
// a GatewayClass and the EnvoyProxy its parametersRef points at. The two
// always travel together, which is why they are one role.
//
// Consumer: DomainTemplateService (domain_template_service.go).
type GatewayClassApplier interface {
	CreateGatewayClass(ctx context.Context, projectID uuid.UUID, config *kubernetes.GatewayClassConfig) error
	DeleteGatewayClass(ctx context.Context, projectID uuid.UUID, name string) error
	CreateEnvoyProxy(ctx context.Context, projectID uuid.UUID, config *kubernetes.EnvoyProxyConfig) error
	UpdateEnvoyProxy(ctx context.Context, projectID uuid.UUID, config *kubernetes.EnvoyProxyConfig) error
	DeleteEnvoyProxy(ctx context.Context, projectID uuid.UUID, namespace, name string) error
}

// NamespaceLister lists the namespaces of a project's cluster.
//
// Consumers: ProjectNamespaceService (project_namespace_service.go), and
// KubernetesHandler through Discovery.
type NamespaceLister interface {
	ListNamespaces(ctx context.Context, projectID uuid.UUID) ([]string, error)
}

// Discovery is the read-only cluster browsing the Kubernetes explorer
// endpoints expose: namespaces, services and gateway classes.
//
// Consumer: KubernetesHandler (internal/handlers/kubernetes_handler.go).
type Discovery interface {
	NamespaceLister

	ListServices(ctx context.Context, projectID uuid.UUID, namespace string) ([]map[string]interface{}, error)
	ListGatewayClasses(ctx context.Context, projectID uuid.UUID) ([]string, error)
}

// Preflight validates that a cluster has what FastGateway needs before a
// project is allowed to point at it.
//
// Consumer: ProjectService (project_service.go).
type Preflight interface {
	ValidatePrerequisites(ctx context.Context, apiURL, token string) (*cluster.PrerequisiteCheck, error)
}

// RateLimitProbe reports whether the cluster's Envoy Gateway installation has
// rate limiting enabled, so the UI can hide the feature when it does not.
//
// Consumer: ProjectHandler (internal/handlers/project_handler.go).
type RateLimitProbe interface {
	IsRateLimitAvailable(ctx context.Context, projectID uuid.UUID) (bool, error)
}

// VersionDetector reads the Kubernetes, Gateway API and Envoy Gateway
// versions installed in a project's cluster.
//
// Consumer: ProjectVersionService (project_version_service.go).
type VersionDetector interface {
	DetectVersions(ctx context.Context, projectID uuid.UUID) (*cluster.RawVersions, error)
}

// Compile-time role satisfaction checks. *cluster.Client is the single
// concrete implementation of every role; these replace the one assertion
// that used to cover KubernetesServiceInterface. A role *cluster.Client
// cannot satisfy fails the build rather than a test.
var (
	_ RouteApplier           = (*cluster.Client)(nil)
	_ TrafficPolicyApplier   = (*cluster.Client)(nil)
	_ PolicyApplier          = (*cluster.Client)(nil)
	_ BackendApplier         = (*cluster.Client)(nil)
	_ RouteBackendReaper     = (*cluster.Client)(nil)
	_ SecretWriter           = (*cluster.Client)(nil)
	_ Secrets                = (*cluster.Client)(nil)
	_ APIKeySecretApplier    = (*cluster.Client)(nil)
	_ APIKeySecretDeleter    = (*cluster.Client)(nil)
	_ ReferenceGrantChecker  = (*cluster.Client)(nil)
	_ ReferenceGrantResetter = (*cluster.Client)(nil)
	_ ReferenceGrants        = (*cluster.Client)(nil)
	_ GatewayApplier         = (*cluster.Client)(nil)
	_ GatewayClassApplier    = (*cluster.Client)(nil)
	_ NamespaceLister        = (*cluster.Client)(nil)
	_ Discovery              = (*cluster.Client)(nil)
	_ Preflight              = (*cluster.Client)(nil)
	_ RateLimitProbe         = (*cluster.Client)(nil)
	_ VersionDetector        = (*cluster.Client)(nil)
)
