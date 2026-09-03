// Package templateplan builds Kubernetes manifest configs for domain
// templates. It is a sibling to internal/domainplan and internal/routeplan
// and follows the same contract: no k8s.io/client-go, no internal/repository,
// no database. Inputs are models values; outputs are kubernetes.*Config
// values.
//
// It exists as its own package rather than living in domainplan because its
// input type is *models.DomainTemplate, where every domainplan function takes
// *models.Domain. Merging them would produce a package with two unrelated
// input types and no coherent boundary.
package templateplan

import (
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
)

// BuildGatewayClassConfig builds the GatewayClass config for a domain
// template. Before Phase 2H this literal appeared three times in
// DomainTemplateService (Create, GetManifests, PreviewCreate).
func BuildGatewayClassConfig(dt *models.DomainTemplate) *kubernetes.GatewayClassConfig {
	return &kubernetes.GatewayClassConfig{
		Name:              dt.K8sGatewayClassName,
		ControllerName:    dt.ControllerName,
		ParametersRefName: dt.K8sEnvoyProxyName,
	}
}
