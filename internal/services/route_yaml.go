package services

// RouteYAMLs represents both HTTPRoute and SecurityPolicy YAMLs
type RouteYAMLs struct {
	HTTPRouteYAML            string `json:"httpRouteYaml"`
	SecurityPolicyYAML       string `json:"securityPolicyYaml,omitempty"`
	BackendTrafficPolicyYAML string `json:"backendTrafficPolicyYaml,omitempty"`
	EnvoyExtensionPolicyYAML string `json:"envoyExtensionPolicyYaml,omitempty"`
	BackendYAML              string `json:"backendYaml,omitempty"`
	HTTPRouteFilterYAML      string `json:"httpRouteFilterYaml,omitempty"`
	ConfigMapYAML            string `json:"configMapYaml,omitempty"`
	// Per-client API key resources (with secrets redacted)
	APIKeyClientResources []APIKeyClientResourceYAMLs `json:"apiKeyClientResources,omitempty"`
}

// APIKeyClientResourceYAMLs represents the K8s resources for a single API key client
type APIKeyClientResourceYAMLs struct {
	ClientID                 string `json:"clientId"`
	ClientName               string `json:"clientName"`
	HTTPRouteYAML            string `json:"httpRouteYaml"`
	SecurityPolicyYAML       string `json:"securityPolicyYaml"`
	BackendTrafficPolicyYAML string `json:"backendTrafficPolicyYaml,omitempty"`
	EnvoyExtensionPolicyYAML string `json:"envoyExtensionPolicyYaml,omitempty"`
}

// PreviewCreateResult represents the result of a create preview
type PreviewCreateResult struct {
	ProposedYAML                     string `json:"proposedYaml"`
	ProposedSecurityPolicyYAML       string `json:"proposedSecurityPolicyYaml,omitempty"`
	ProposedBackendTrafficPolicyYAML string `json:"proposedBackendTrafficPolicyYaml,omitempty"`
	ProposedEnvoyExtensionPolicyYAML string `json:"proposedEnvoyExtensionPolicyYaml,omitempty"`
	ProposedBackendYAML              string `json:"proposedBackendYaml,omitempty"`
	ProposedHTTPRouteFilterYAML      string `json:"proposedHttpRouteFilterYaml,omitempty"`
	ProposedConfigMapYAML            string `json:"proposedConfigMapYaml,omitempty"`
}

// PreviewUpdateResult represents the result of an update preview
type PreviewUpdateResult struct {
	CurrentYAML                      string `json:"currentYaml"`
	ProposedYAML                     string `json:"proposedYaml"`
	CurrentSecurityPolicyYAML        string `json:"currentSecurityPolicyYaml,omitempty"`
	ProposedSecurityPolicyYAML       string `json:"proposedSecurityPolicyYaml,omitempty"`
	CurrentBackendTrafficPolicyYAML  string `json:"currentBackendTrafficPolicyYaml,omitempty"`
	ProposedBackendTrafficPolicyYAML string `json:"proposedBackendTrafficPolicyYaml,omitempty"`
	CurrentEnvoyExtensionPolicyYAML  string `json:"currentEnvoyExtensionPolicyYaml,omitempty"`
	ProposedEnvoyExtensionPolicyYAML string `json:"proposedEnvoyExtensionPolicyYaml,omitempty"`
	CurrentBackendYAML               string `json:"currentBackendYaml,omitempty"`
	ProposedBackendYAML              string `json:"proposedBackendYaml,omitempty"`
	CurrentHTTPRouteFilterYAML       string `json:"currentHttpRouteFilterYaml,omitempty"`
	ProposedHTTPRouteFilterYAML      string `json:"proposedHttpRouteFilterYaml,omitempty"`
	CurrentConfigMapYAML             string `json:"currentConfigMapYaml,omitempty"`
	ProposedConfigMapYAML            string `json:"proposedConfigMapYaml,omitempty"`
}

// PreviewDeleteResult represents the result of a delete preview
type PreviewDeleteResult struct {
	CurrentYAML                     string `json:"currentYaml"`
	CurrentSecurityPolicyYAML       string `json:"currentSecurityPolicyYaml,omitempty"`
	CurrentBackendTrafficPolicyYAML string `json:"currentBackendTrafficPolicyYaml,omitempty"`
	CurrentEnvoyExtensionPolicyYAML string `json:"currentEnvoyExtensionPolicyYaml,omitempty"`
	CurrentBackendYAML              string `json:"currentBackendYaml,omitempty"`
	CurrentHTTPRouteFilterYAML      string `json:"currentHttpRouteFilterYaml,omitempty"`
	CurrentConfigMapYAML            string `json:"currentConfigMapYaml,omitempty"`
}
