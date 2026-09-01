// Package roles declares the test-only union of every Kubernetes role
// interface in internal/services.
//
// Phase 2E Task 7 replaced the 58-method KubernetesServiceInterface with
// nineteen roles, each named by the one or two services that call it. No
// production type declares the union -- *cluster.Client satisfies each role
// individually, and every consumer takes only the roles it calls.
//
// The union exists for exactly one reason: internal/mocks/mock_kubernetes.go
// is generated from it, so the single MockKubernetesService the tests share
// keeps implementing all nineteen roles without anyone maintaining its 58
// methods by hand. Adding a method to any role changes this interface's
// method set, which changes the generated mock, which the CI drift check
// notices. That is the class of defect -- SetClientHeaderRepository /
// SetProjectRepository drifting out of the interface unnoticed -- that
// Phase 2E Task 10 exists to make impossible.
//
// Nothing outside internal/mocks may depend on this type.
package roles

import "github.com/fastgateway-dev/backend-v2/internal/services"

// KubernetesService is the union of every role interface in
// internal/services/k8s_roles.go.
type KubernetesService interface {
	services.RouteApplier
	services.TrafficPolicyApplier
	services.PolicyApplier
	services.BackendApplier
	services.RouteBackendReaper
	services.SecretWriter
	services.Secrets
	services.APIKeySecretApplier
	services.APIKeySecretDeleter
	services.ReferenceGrantChecker
	services.ReferenceGrantResetter
	services.ReferenceGrants
	services.GatewayApplier
	services.GatewayClassApplier
	services.NamespaceLister
	services.Discovery
	services.Preflight
	services.RateLimitProbe
	services.VersionDetector
}
