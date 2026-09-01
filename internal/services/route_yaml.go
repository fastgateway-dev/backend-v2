package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/google/uuid"
	"sigs.k8s.io/yaml"
)

// GenerateYAML generates the Kubernetes YAML for a route
func (s *RouteService) GenerateYAML(id uuid.UUID) (string, error) {
	route, err := s.routeRepo.GetByID(id)
	if err != nil {
		return "", err
	}

	domain, err := s.domainRepo.GetByID(route.DomainID)
	if err != nil {
		return "", err
	}

	return routeplan.GenerateHTTPRouteYAML(route, domain), nil
}

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

// GenerateYAMLs generates both HTTPRoute and SecurityPolicy YAML for a route
func (s *RouteService) GenerateYAMLs(id uuid.UUID) (*RouteYAMLs, error) {
	route, err := s.routeRepo.GetByID(id)
	if err != nil {
		return nil, err
	}

	domain, err := s.domainRepo.GetByID(route.DomainID)
	if err != nil {
		return nil, err
	}

	result := &RouteYAMLs{
		HTTPRouteYAML: routeplan.GenerateHTTPRouteYAML(route, domain),
	}

	// Generate SecurityPolicy YAML if exists
	// This includes CORS, OIDC, JWT, APIKeyAuth, Authorization from DB
	// plus client IP authorization from attachments
	policy, _ := s.securityPolicyRepo.GetByRouteID(id)

	// Compute authorization from IP-only client attachments
	clientAuthConfig := s.buildClientIPAuthorizationConfig(id)

	// Check if there are per-client auth clients that require deny-all on base route
	// This matches the deploy logic in deploySecurityPolicy
	hasPerClientClients := s.hasAPIKeyClientAttachments(id) || s.hasJWTClientAttachments(id) || s.hasMTLSClientAttachments(id)
	if hasPerClientClients && clientAuthConfig == nil {
		// Create a deny-all authorization (empty CIDR list with default deny)
		// This prevents unauthenticated access through the base HTTPRoute
		clientAuthConfig = &kubernetes.AuthorizationPolicyConfig{
			DefaultAction: "Deny",
			Rules:         []kubernetes.AuthorizationRulePolicyConfig{},
		}
	}

	if policy != nil || clientAuthConfig != nil {
		// Use routeplan.SecurityPolicyConfigFromDB to get full config (CORS, OIDC, JWT, APIKeyAuth, Authorization)
		var mergedConfig *kubernetes.SecurityPolicyConfig
		if policy != nil {
			mergedConfig = routeplan.SecurityPolicyConfigFromDB(route, domain, policy)
		}

		// If no DB policy but we have client auth, create a minimal config.
		// This is identity-only (name + targetRef); it goes through the same
		// assembler so those fields cannot drift away from the deploy path.
		if mergedConfig == nil && clientAuthConfig != nil {
			mergedConfig = routeplan.AssembleSecurityPolicyConfig(routeplan.SecurityPolicyAssembly{
				Route:  route,
				Domain: domain,
			})
		}

		// Merge client IP authorization if present and no DB authorization exists
		// (client mode uses client IPs, general mode uses DB authorization)
		if mergedConfig != nil && clientAuthConfig != nil && mergedConfig.Authorization == nil {
			mergedConfig.Authorization = clientAuthConfig
		}

		if mergedConfig != nil {
			securityPolicy := kubernetes.BuildSecurityPolicy(mergedConfig)
			if securityPolicy != nil {
				yamlBytes, err := yaml.Marshal(securityPolicy.Object)
				if err == nil {
					result.SecurityPolicyYAML = string(yamlBytes)
				}
			}
		}
	}

	// Generate BackendTrafficPolicy YAML if exists
	btpPolicy, _ := s.backendTrafficPolicyRepo.GetByRouteID(id)
	if btpPolicy != nil {
		result.BackendTrafficPolicyYAML = routeplan.GenerateBackendTrafficPolicyYAMLFromDB(route, domain, btpPolicy)
	}

	// Generate EnvoyExtensionPolicy YAML if exists (with WAF support)
	var extPolicy *models.EnvoyExtensionPolicy
	var wafPolicy *models.WafPolicy
	extPolicy, _ = s.envoyExtensionPolicyRepo.GetByRouteID(id)
	wafPolicy, _ = s.wafPolicyRepo.GetByRouteID(id)
	if extPolicy != nil || wafPolicy != nil {
		result.EnvoyExtensionPolicyYAML = s.generateEnvoyExtensionPolicyYAMLFromDBWithWaf(route, domain, extPolicy, wafPolicy)
	}

	// Generate Backend CRD YAML for external backends
	result.BackendYAML = routeplan.GenerateBackendYAMLs(route, domain)

	// Generate HTTPRouteFilter and ConfigMap YAML for direct response routes
	if route.Config.DirectResponse != nil {
		result.HTTPRouteFilterYAML, result.ConfigMapYAML = routeplan.GenerateDirectResponseYAMLs(route, domain)
	}

	// Generate per-client API key resources (with secrets redacted)
	apiKeyClientResources := s.generateAPIKeyClientResourceYAMLs(route, domain)
	if len(apiKeyClientResources) > 0 {
		result.APIKeyClientResources = apiKeyClientResources
	}

	return result, nil
}

// generateAPIKeyClientResourceYAMLs generates YAML for per-client API key resources
// with secrets redacted for display purposes
func (s *RouteService) generateAPIKeyClientResourceYAMLs(route *models.Route, domain *models.Domain) []APIKeyClientResourceYAMLs {
	ctx := context.Background()

	// Categorize client attachments
	_, apiKeyOnlyClients, bothClients, err := s.categorizeClientAttachments(ctx, route.ID, domain)
	if err != nil {
		return nil
	}

	// Combine API key clients
	allAPIKeyClients := append(apiKeyOnlyClients, bothClients...)
	if len(allAPIKeyClients) == 0 {
		return nil
	}

	// Get SecurityPolicy for this route (if any) to copy CORS config to per-client routes
	var secPolicy *models.SecurityPolicy
	secPolicy, _ = s.securityPolicyRepo.GetByRouteID(route.ID)

	var results []APIKeyClientResourceYAMLs
	for _, client := range allAPIKeyClients {
		clientResource := APIKeyClientResourceYAMLs{
			ClientID:   client.ClientID.String(),
			ClientName: client.ClientName,
		}

		// Build route YAML (HTTPRoute or GRPCRoute based on protocol)
		if route.Protocol == models.RouteProtocolGRPC {
			grpcRouteConfig := s.buildAPIKeyGRPCRouteConfig(route, domain, client)
			grpcRoute := kubernetes.BuildGRPCRouteObject(grpcRouteConfig)
			if grpcRoute != nil {
				yamlBytes, err := yaml.Marshal(grpcRoute)
				if err == nil {
					clientResource.HTTPRouteYAML = string(yamlBytes)
				}
			}
		} else {
			httpRouteConfig := s.buildAPIKeyHTTPRouteConfigRedacted(route, domain, client)
			httpRoute := kubernetes.BuildHTTPRouteObject(httpRouteConfig)
			if httpRoute != nil {
				yamlBytes, err := yaml.Marshal(httpRoute)
				if err == nil {
					clientResource.HTTPRouteYAML = string(yamlBytes)
				}
			}
		}

		// Build SecurityPolicy
		requireIP := client.EnableIP
		securityConfig := s.buildAPIKeySecurityPolicyConfig(route, domain, client, requireIP, secPolicy)
		securityPolicy := kubernetes.BuildSecurityPolicy(securityConfig)
		if securityPolicy != nil {
			yamlBytes, err := yaml.Marshal(securityPolicy.Object)
			if err == nil {
				clientResource.SecurityPolicyYAML = string(yamlBytes)
			}
		}

		// Build BackendTrafficPolicy if base BTP exists or client has rate limit
		{
			var btpPolicy *models.BackendTrafficPolicy
			btpPolicy, _ = s.backendTrafficPolicyRepo.GetByRouteID(route.ID)
			if btpPolicy != nil || client.RateLimitConfig != nil {
				routeName := route.K8sRouteName + "-ak-" + client.ClientID.String()[:8]
				clientResource.BackendTrafficPolicyYAML = routeplan.GenerateAPIKeyBackendTrafficPolicyYAML(route, domain, btpPolicy, routeName, client.RateLimitConfig)
			}
		}

		// Build EnvoyExtensionPolicy if base extension policy exists
		{
			var extPolicy *models.EnvoyExtensionPolicy
			extPolicy, _ = s.envoyExtensionPolicyRepo.GetByRouteID(route.ID)
			if extPolicy != nil {
				routeName := route.K8sRouteName + "-ak-" + client.ClientID.String()[:8]
				clientResource.EnvoyExtensionPolicyYAML = routeplan.GenerateAPIKeyEnvoyExtensionPolicyYAML(route, domain, extPolicy, routeName)
			}
		}

		results = append(results, clientResource)
	}

	return results
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

// PreviewCreate generates a preview of what the HTTPRoute YAML would look like for a new route
func (s *RouteService) PreviewCreate(domainID uuid.UUID, input *CreateRouteInput) (*PreviewCreateResult, error) {
	// Validate route name format
	if !isValidK8sName(input.Name) {
		return nil, errors.New("route name must be lowercase alphanumeric with dashes only (e.g., 'user-api')")
	}

	// Verify domain exists
	domain, err := s.domainRepo.GetByID(domainID)
	if err != nil {
		return nil, errors.New("domain not found")
	}

	// Generate a temporary route ID for preview
	tempRouteID := s.newID()
	// Safe only because input.Name was already validated by isValidK8sName above
	// (see the comment on that function) - kubernetes.RouteK8sName sanitizes
	// differently than isValidK8sName rejects, so they only agree on validated input.
	k8sRouteName := kubernetes.RouteK8sName(input.Name, tempRouteID.String())

	protocol := input.Protocol
	if protocol == "" {
		protocol = models.RouteProtocolHTTP
	}

	// Create a temporary route object for YAML generation
	tempRoute := &models.Route{
		ID:           tempRouteID,
		DomainID:     domainID,
		TeamID:       input.TeamID,
		Name:         input.Name,
		Description:  input.Description,
		Protocol:     protocol,
		Config:       input.Config,
		K8sRouteName: k8sRouteName,
	}

	proposedYAML := routeplan.GenerateHTTPRouteYAML(tempRoute, domain)

	// Generate SecurityPolicy YAML if security features are configured
	// Note: For new routes, there are no client attachments yet, so clientCIDRs is nil
	proposedSecurityPolicyYAML := routeplan.GenerateSecurityPolicyYAML(tempRoute, domain, input.SecurityPolicy, nil)

	// Generate BackendTrafficPolicy YAML if configured
	proposedBackendTrafficPolicyYAML := routeplan.GenerateBackendTrafficPolicyYAML(tempRoute, domain, input.BackendTrafficPolicy)

	// Generate Backend CRD YAML for external backends
	proposedBackendYAML := routeplan.GenerateBackendYAMLs(tempRoute, domain)

	// Generate HTTPRouteFilter and ConfigMap YAML for direct response routes
	var proposedHTTPRouteFilterYAML, proposedConfigMapYAML string
	if input.Config.DirectResponse != nil {
		proposedHTTPRouteFilterYAML, proposedConfigMapYAML = routeplan.GenerateDirectResponseYAMLs(tempRoute, domain)
	}

	// Generate EnvoyExtensionPolicy YAML if configured (extension policy and/or WAF)
	proposedEnvoyExtensionPolicyYAML := routeplan.GenerateEnvoyExtensionPolicyYAMLWithWaf(tempRoute, domain, input.ExtensionPolicy, input.WafPolicy, s.wafConfig)

	return &PreviewCreateResult{
		ProposedYAML:                     proposedYAML,
		ProposedSecurityPolicyYAML:       proposedSecurityPolicyYAML,
		ProposedBackendTrafficPolicyYAML: proposedBackendTrafficPolicyYAML,
		ProposedEnvoyExtensionPolicyYAML: proposedEnvoyExtensionPolicyYAML,
		ProposedBackendYAML:              proposedBackendYAML,
		ProposedHTTPRouteFilterYAML:      proposedHTTPRouteFilterYAML,
		ProposedConfigMapYAML:            proposedConfigMapYAML,
	}, nil
}

// PreviewUpdate generates a preview comparing current and proposed HTTPRoute YAML
func (s *RouteService) PreviewUpdate(routeID uuid.UUID, input *UpdateRouteInput) (*PreviewUpdateResult, error) {
	// Get existing route
	route, err := s.routeRepo.GetByID(routeID)
	if err != nil {
		return nil, errors.New("route not found")
	}

	// Get domain
	domain, err := s.domainRepo.GetByID(route.DomainID)
	if err != nil {
		return nil, errors.New("domain not found")
	}

	// Generate current YAML (with existing config)
	currentYAML := routeplan.GenerateHTTPRouteYAML(route, domain)

	// Get current SecurityPolicy from database (if any)
	var currentSecurityPolicyYAML string
	currentPolicy, _ := s.securityPolicyRepo.GetByRouteID(routeID)
	if currentPolicy != nil {
		currentSecurityPolicyYAML = routeplan.GenerateSecurityPolicyYAMLFromDB(route, domain, currentPolicy)
	}

	// Get current BackendTrafficPolicy from database (if any)
	var currentBackendTrafficPolicyYAML string
	currentBtpPolicy, _ := s.backendTrafficPolicyRepo.GetByRouteID(routeID)
	if currentBtpPolicy != nil {
		currentBackendTrafficPolicyYAML = routeplan.GenerateBackendTrafficPolicyYAMLFromDB(route, domain, currentBtpPolicy)
	}

	// Get current EnvoyExtensionPolicy and WafPolicy from database (if any)
	var currentEnvoyExtensionPolicyYAML string
	var currentExtPolicy *models.EnvoyExtensionPolicy
	var currentWafPolicy *models.WafPolicy
	currentExtPolicy, _ = s.envoyExtensionPolicyRepo.GetByRouteID(routeID)
	currentWafPolicy, _ = s.wafPolicyRepo.GetByRouteID(routeID)
	if currentExtPolicy != nil || currentWafPolicy != nil {
		currentEnvoyExtensionPolicyYAML = s.generateEnvoyExtensionPolicyYAMLFromDBWithWaf(route, domain, currentExtPolicy, currentWafPolicy)
	}

	// Create a copy of the route with updated config for proposed YAML
	proposedRoute := &models.Route{
		ID:           route.ID,
		DomainID:     route.DomainID,
		TeamID:       route.TeamID,
		Name:         route.Name,
		Description:  input.Description,
		Protocol:     route.Protocol,
		Config:       input.Config,
		K8sRouteName: route.K8sRouteName,
	}
	if input.Description == "" {
		proposedRoute.Description = route.Description
	}

	proposedYAML := routeplan.GenerateHTTPRouteYAML(proposedRoute, domain)

	// Collect client CIDRs from existing attachments for preview
	clientCIDRs := s.collectClientIPCIDRs(routeID)

	// Generate proposed SecurityPolicy YAML if security features are configured
	// Include client CIDRs to show the full merged result
	proposedSecurityPolicyYAML := routeplan.GenerateSecurityPolicyYAML(proposedRoute, domain, input.SecurityPolicy, clientCIDRs)

	// Generate proposed BackendTrafficPolicy YAML if configured
	proposedBackendTrafficPolicyYAML := routeplan.GenerateBackendTrafficPolicyYAML(proposedRoute, domain, input.BackendTrafficPolicy)

	// Generate Backend CRD YAML for external backends (current and proposed)
	currentBackendYAML := routeplan.GenerateBackendYAMLs(route, domain)
	proposedBackendYAML := routeplan.GenerateBackendYAMLs(proposedRoute, domain)

	// Generate HTTPRouteFilter and ConfigMap YAML for direct response routes (current and proposed)
	var currentHTTPRouteFilterYAML, currentConfigMapYAML string
	if route.Config.DirectResponse != nil {
		currentHTTPRouteFilterYAML, currentConfigMapYAML = routeplan.GenerateDirectResponseYAMLs(route, domain)
	}
	var proposedHTTPRouteFilterYAML, proposedConfigMapYAML string
	if input.Config.DirectResponse != nil {
		proposedHTTPRouteFilterYAML, proposedConfigMapYAML = routeplan.GenerateDirectResponseYAMLs(proposedRoute, domain)
	}

	// Generate proposed EnvoyExtensionPolicy YAML if configured (extension policy and/or WAF)
	proposedEnvoyExtensionPolicyYAML := routeplan.GenerateEnvoyExtensionPolicyYAMLWithWaf(proposedRoute, domain, input.ExtensionPolicy, input.WafPolicy, s.wafConfig)

	return &PreviewUpdateResult{
		CurrentYAML:                      currentYAML,
		ProposedYAML:                     proposedYAML,
		CurrentSecurityPolicyYAML:        currentSecurityPolicyYAML,
		ProposedSecurityPolicyYAML:       proposedSecurityPolicyYAML,
		CurrentBackendTrafficPolicyYAML:  currentBackendTrafficPolicyYAML,
		ProposedBackendTrafficPolicyYAML: proposedBackendTrafficPolicyYAML,
		CurrentEnvoyExtensionPolicyYAML:  currentEnvoyExtensionPolicyYAML,
		ProposedEnvoyExtensionPolicyYAML: proposedEnvoyExtensionPolicyYAML,
		CurrentBackendYAML:               currentBackendYAML,
		ProposedBackendYAML:              proposedBackendYAML,
		CurrentHTTPRouteFilterYAML:       currentHTTPRouteFilterYAML,
		ProposedHTTPRouteFilterYAML:      proposedHTTPRouteFilterYAML,
		CurrentConfigMapYAML:             currentConfigMapYAML,
		ProposedConfigMapYAML:            proposedConfigMapYAML,
	}, nil
}

// PreviewDelete generates a preview of what will be deleted
func (s *RouteService) PreviewDelete(routeID uuid.UUID) (*PreviewDeleteResult, error) {
	// Get existing route
	route, err := s.routeRepo.GetByID(routeID)
	if err != nil {
		return nil, errors.New("route not found")
	}

	// Get domain
	domain, err := s.domainRepo.GetByID(route.DomainID)
	if err != nil {
		return nil, errors.New("domain not found")
	}

	// Generate current YAML
	currentYAML := routeplan.GenerateHTTPRouteYAML(route, domain)

	// Get current SecurityPolicy from database (if any)
	var currentSecurityPolicyYAML string
	currentPolicy, _ := s.securityPolicyRepo.GetByRouteID(routeID)
	if currentPolicy != nil {
		currentSecurityPolicyYAML = routeplan.GenerateSecurityPolicyYAMLFromDB(route, domain, currentPolicy)
	}

	// Get current BackendTrafficPolicy from database (if any)
	var currentBackendTrafficPolicyYAML string
	currentBtpPolicy, _ := s.backendTrafficPolicyRepo.GetByRouteID(routeID)
	if currentBtpPolicy != nil {
		currentBackendTrafficPolicyYAML = routeplan.GenerateBackendTrafficPolicyYAMLFromDB(route, domain, currentBtpPolicy)
	}

	// Get current EnvoyExtensionPolicy and WafPolicy from database (if any)
	var currentEnvoyExtensionPolicyYAML string
	var currentExtPolicy *models.EnvoyExtensionPolicy
	var currentWafPolicy *models.WafPolicy
	currentExtPolicy, _ = s.envoyExtensionPolicyRepo.GetByRouteID(routeID)
	currentWafPolicy, _ = s.wafPolicyRepo.GetByRouteID(routeID)
	if currentExtPolicy != nil || currentWafPolicy != nil {
		currentEnvoyExtensionPolicyYAML = s.generateEnvoyExtensionPolicyYAMLFromDBWithWaf(route, domain, currentExtPolicy, currentWafPolicy)
	}

	// Generate Backend CRD YAML for external backends
	currentBackendYAML := routeplan.GenerateBackendYAMLs(route, domain)

	// Generate HTTPRouteFilter and ConfigMap YAML for direct response routes
	var currentHTTPRouteFilterYAML, currentConfigMapYAML string
	if route.Config.DirectResponse != nil {
		currentHTTPRouteFilterYAML, currentConfigMapYAML = routeplan.GenerateDirectResponseYAMLs(route, domain)
	}

	return &PreviewDeleteResult{
		CurrentYAML:                     currentYAML,
		CurrentSecurityPolicyYAML:       currentSecurityPolicyYAML,
		CurrentBackendTrafficPolicyYAML: currentBackendTrafficPolicyYAML,
		CurrentEnvoyExtensionPolicyYAML: currentEnvoyExtensionPolicyYAML,
		CurrentBackendYAML:              currentBackendYAML,
		CurrentHTTPRouteFilterYAML:      currentHTTPRouteFilterYAML,
		CurrentConfigMapYAML:            currentConfigMapYAML,
	}, nil
}

// generateEnvoyExtensionPolicyYAMLFromDBWithWaf generates EnvoyExtensionPolicy YAML from database models with WAF support
func (s *RouteService) generateEnvoyExtensionPolicyYAMLFromDBWithWaf(route *models.Route, domain *models.Domain, policy *models.EnvoyExtensionPolicy, wafPolicy *models.WafPolicy) string {
	// Use buildEnvoyExtensionPolicyConfig which already handles WAF merging
	config := s.buildEnvoyExtensionPolicyConfig(route, domain, policy, wafPolicy)
	if config == nil {
		return ""
	}

	// Build the EnvoyExtensionPolicy object
	extensionPolicy := kubernetes.BuildEnvoyExtensionPolicy(config)
	if extensionPolicy == nil {
		return ""
	}

	// Marshal to YAML
	yamlBytes, err := yaml.Marshal(extensionPolicy.Object)
	if err != nil {
		return fmt.Sprintf("# Error generating EnvoyExtensionPolicy YAML: %v", err)
	}

	return string(yamlBytes)
}
