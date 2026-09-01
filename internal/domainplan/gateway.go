// Package domainplan builds Kubernetes manifest configuration for domains.
//
// It is the domain-level sibling of internal/routeplan and carries the same
// contract: no database access, no Kubernetes client. Inputs are models
// values, outputs are kubernetes.*Config values.
//
// Before Phase 2F these four builders were private methods on DomainService
// -- a second manifest-assembly path that bypassed the pure layer entirely
// and that none of the 72 route goldens covered. Phase 2D found one of them
// to be the third EnvoyExtensionPolicy assembler in the codebase.
package domainplan

import (
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
)

// BuildGatewayConfig builds a kubernetes.GatewayConfig from a domain, including template annotations.
//
// Template annotations are resolved by the caller and passed in rather than
// looked up here: domainplan performs no I/O. Pass nil when the domain has
// no template, or when the lookup failed -- see the note on error handling
// at the call sites.
func BuildGatewayConfig(domain *models.Domain, templateAnnotations map[string]string) *kubernetes.GatewayConfig {
	config := &kubernetes.GatewayConfig{
		Name:             domain.K8sGatewayName,
		Namespace:        domain.Namespace,
		GatewayClassName: domain.K8sGatewayClass,
		Hostname:         domain.Hostname,
		TLSMode:          domain.TLSMode,
		HTTPPort:         domain.HTTPPort,
		HTTPSPort:        domain.HTTPSPort,
		TLSSecretName:    domain.TLSSecretName,
		TLSPolicy:        string(domain.TLSPolicy),
	}
	// Include annotations from domain template
	if domain.DomainTemplateID != nil {
		config.Annotations = templateAnnotations
	}
	return config
}
