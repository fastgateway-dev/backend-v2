package services

import (
	"encoding/json"

	"github.com/fastgateway-dev/backend-v2/internal/models"
)

func marshalRouteSnapshot(config *models.RouteConfig, sp *models.SecurityPolicyConfig, btp *models.BackendTrafficPolicyConfig, eep *models.EnvoyExtensionPolicyConfig, waf *models.WafPolicyConfig) json.RawMessage {
	out, _ := json.Marshal(models.RouteApprovalSnapshot{
		RouteConfig:          config,
		SecurityPolicy:       sp,
		BackendTrafficPolicy: btp,
		EnvoyExtensionPolicy: eep,
		WafPolicy:            waf,
	})
	return out
}

// updateApprovalSnapshots carries the proposed and previous policy configs
// for submitUpdateApproval by name, so proposed/previous cannot be swapped
// at the call site without the compiler catching a field mismatch.
type updateApprovalSnapshots struct {
	ProposedSecurityPolicy       *models.SecurityPolicyConfig
	ProposedBackendTrafficPolicy *models.BackendTrafficPolicyConfig
	ProposedEnvoyExtensionPolicy *models.EnvoyExtensionPolicyConfig
	ProposedWafPolicy            *models.WafPolicyConfig

	PreviousConfig               models.RouteConfig
	PreviousSecurityPolicy       *models.SecurityPolicyConfig
	PreviousBackendTrafficPolicy *models.BackendTrafficPolicyConfig
	PreviousEnvoyExtensionPolicy *models.EnvoyExtensionPolicyConfig
	PreviousWafPolicy            *models.WafPolicyConfig
}
