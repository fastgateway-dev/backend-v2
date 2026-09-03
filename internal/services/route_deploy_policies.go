package services

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"gorm.io/gorm"
)

// deploySecurityPolicy deploys SecurityPolicy to Kubernetes if configured
// This merges CORS config from the DB security policy with authorization
// computed from: (1) direct IP allowlist in security policy, and (2) client attachments
func (s *RouteService) deploySecurityPolicy(ctx context.Context, route *models.Route, domain *models.Domain) error {
	// Get SecurityPolicy from database
	var policy *models.SecurityPolicy
	p, err := s.securityPolicyRepo.GetByRouteID(route.ID)
	switch {
	case err == nil:
		policy = p
	case errors.Is(err, gorm.ErrRecordNotFound):
		// No SecurityPolicy configured for this route. Legitimate: policy
		// stays nil and the caller's cleanup branch removes any stale
		// cluster object.
	default:
		// Any other error is a LOOKUP FAILURE, not an absence. Returning nil
		// policy here would send a general-mode route down
		// deployGeneralSecurityPolicy's config == nil branch, which DELETES
		// the live SecurityPolicy -- stripping OIDC/JWT/API-key/IP
		// authorization from a route that is still serving -- and then
		// report success.
		return fmt.Errorf("load security policy for route %s: %w", route.ID, err)
	}

	// General mode: build SecurityPolicy directly from stored config
	if route.SecurityMode == models.SecurityModeGeneral {
		return s.deployGeneralSecurityPolicy(ctx, route, domain, policy)
	}

	// Client mode: existing logic below
	// Build authorization config from IP-only client attachments
	// (clients with IP allowlisting but NOT API key/JWT - those go to per-client routes)
	authConfig, err := s.buildClientIPAuthorizationConfig(route.ID)
	if err != nil {
		return fmt.Errorf("build client IP authorization config for route %s: %w", route.ID, err)
	}

	// Check if there are any client attachments
	clientCount, err := s.countClientAttachments(route.ID)
	if err != nil {
		return fmt.Errorf("count client attachments for route %s: %w", route.ID, err)
	}

	// When clients are attached, apply DefaultTrafficPolicy to control non-client traffic
	if clientCount > 0 {
		// Check if there are API key/JWT clients (per-client routes handle their own auth)
		hasPerClientAuth := s.hasAPIKeyClientAttachments(route.ID) || s.hasJWTClientAttachments(route.ID) || s.hasMTLSClientAttachments(route.ID)

		switch route.Config.DefaultTrafficPolicy {
		case models.DefaultTrafficPolicyDeny:
			// Deny all non-client traffic, but preserve IP-only client allow rules
			// authConfig from buildClientIPAuthorizationConfig already has DefaultAction: "Deny"
			// with allow rules for IP-only clients. Only create deny-all if no IP-only clients.
			if authConfig == nil {
				authConfig = &kubernetes.AuthorizationPolicyConfig{
					DefaultAction: "Deny",
					Rules:         []kubernetes.AuthorizationRulePolicyConfig{},
				}
			}
			// Ensure default action is Deny (authConfig from IP-only clients already has this)
			authConfig.DefaultAction = "Deny"
		case models.DefaultTrafficPolicyRequireIPAllowlist:
			// Require requests to come from allowed IPs (defaultAllowedCIDRs)
			// Merge with IP-only client CIDRs so registered IP-only clients are also allowed
			var mergedRules []kubernetes.AuthorizationRulePolicyConfig

			// Add defaultAllowedCIDRs
			if len(route.Config.DefaultAllowedCIDRs) > 0 {
				cidrs := make([]string, 0, len(route.Config.DefaultAllowedCIDRs))
				for _, cidr := range route.Config.DefaultAllowedCIDRs {
					cidrs = append(cidrs, routeplan.NormalizeCIDR(cidr))
				}
				mergedRules = append(mergedRules, kubernetes.AuthorizationRulePolicyConfig{
					Action:      "Allow",
					ClientCIDRs: cidrs,
				})
			}

			// Merge IP-only client rules
			if authConfig != nil {
				mergedRules = append(mergedRules, authConfig.Rules...)
			}

			authConfig = &kubernetes.AuthorizationPolicyConfig{
				DefaultAction: "Deny",
				Rules:         mergedRules,
			}
		case models.DefaultTrafficPolicyAllowAll, "":
			// Allow all requests without client header (default behavior)
			// Keep merged auth if it exists (direct IPs + IP-only client IPs)
			// But if there are API key/JWT clients and no merged auth, create deny-all
			// to prevent unauthenticated access through the base HTTPRoute
			if hasPerClientAuth && authConfig == nil {
				authConfig = &kubernetes.AuthorizationPolicyConfig{
					DefaultAction: "Deny",
					Rules:         []kubernetes.AuthorizationRulePolicyConfig{},
				}
			}
		}
	}

	// Build base SecurityPolicy config
	config := routeplan.SecurityPolicyConfigForDeploy(route, domain, policy, authConfig)

	// Check if there's actually anything to deploy
	if config.CORS == nil && config.Authorization == nil {
		// No security features to deploy; delete existing SecurityPolicy if any
		return s.k8sPolicies.DeleteSecurityPolicy(ctx, domain.ProjectID, domain.Namespace, config.Name)
	}

	// Create or update SecurityPolicy in Kubernetes
	return s.k8sPolicies.UpdateSecurityPolicy(ctx, domain.ProjectID, config)
}

// deployGeneralSecurityPolicy deploys SecurityPolicy for general mode routes
// In general mode, all security features (CORS, IP, API key, JWT, OIDC, ExtAuth) come from the DB policy
func (s *RouteService) deployGeneralSecurityPolicy(ctx context.Context, route *models.Route, domain *models.Domain, policy *models.SecurityPolicy) error {
	config := routeplan.SecurityPolicyConfigFromDB(route, domain, policy)
	if config == nil {
		policyName := kubernetes.SecurityPolicyName(route.K8sRouteName)
		// Also clean up ext-auth backend if it exists (legacy cleanup)
		extAuthBackendName := kubernetes.GenerateExtAuthBackendName(route.ID.String(), "")
		_ = s.k8sBackends.DeleteBackend(ctx, domain.ProjectID, domain.Namespace, extAuthBackendName)
		return s.k8sPolicies.DeleteSecurityPolicy(ctx, domain.ProjectID, domain.Namespace, policyName)
	}

	// Note: ExtAuth uses direct K8s Service reference in SecurityPolicy, no Backend CRD needed
	// Clean up any legacy ext-auth Backend CRD that might exist
	if config.ExtAuth != nil {
		extAuthBackendName := kubernetes.GenerateExtAuthBackendName(route.ID.String(), "")
		_ = s.k8sBackends.DeleteBackend(ctx, domain.ProjectID, domain.Namespace, extAuthBackendName)
	}

	return s.k8sPolicies.UpdateSecurityPolicy(ctx, domain.ProjectID, config)
}

// deleteSecurityPolicy deletes SecurityPolicy from Kubernetes
func (s *RouteService) deleteSecurityPolicy(ctx context.Context, route *models.Route, domain *models.Domain) error {
	// Build the security policy name
	securityPolicyName := kubernetes.SecurityPolicyName(route.K8sRouteName)

	// Always delete from Kubernetes (client-mode routes create k8s SecurityPolicies
	// without a DB security_policies record, so we can't gate on DB lookup)
	if err := s.k8sPolicies.DeleteSecurityPolicy(ctx, domain.ProjectID, domain.Namespace, securityPolicyName); err != nil {
		log.Printf("Failed to delete SecurityPolicy %s from Kubernetes: %v", securityPolicyName, err)
	}

	// Delete from database if a record exists
	policy, err := s.securityPolicyRepo.GetByRouteID(route.ID)
	if err == nil {
		return s.securityPolicyRepo.Delete(policy.ID)
	}

	return nil
}

// buildSecurityPolicyConfig builds kubernetes.SecurityPolicyConfig from route, domain and security policy
// Note: This builds from DB only (CORS + stored authorization). For deploy, use deploySecurityPolicy()
// which also computes authorization from active client attachments.
func (s *RouteService) buildSecurityPolicyConfig(route *models.Route, domain *models.Domain, policy *models.SecurityPolicy) *kubernetes.SecurityPolicyConfig {
	return routeplan.SecurityPolicyConfigFromDB(route, domain, policy)
}

// deployBackendTrafficPolicy deploys BackendTrafficPolicy to Kubernetes if configured
func (s *RouteService) deployBackendTrafficPolicy(ctx context.Context, route *models.Route, domain *models.Domain) error {
	// Get BackendTrafficPolicy from database
	policy, err := s.backendTrafficPolicyRepo.GetByRouteID(route.ID)
	if err != nil {
		// No BackendTrafficPolicy configured for this route
		return nil
	}

	// Build BackendTrafficPolicy config for Kubernetes
	btpConfig := routeplan.BuildBackendTrafficPolicyConfig(route, domain, policy)
	if btpConfig == nil {
		return nil
	}

	// Create or update BackendTrafficPolicy in Kubernetes
	return s.k8sPolicies.UpdateBackendTrafficPolicy(ctx, domain.ProjectID, btpConfig)
}

// deleteBackendTrafficPolicy deletes BackendTrafficPolicy from Kubernetes
func (s *RouteService) deleteBackendTrafficPolicy(ctx context.Context, route *models.Route, domain *models.Domain) error {
	// Check if BackendTrafficPolicy exists for this route
	policy, err := s.backendTrafficPolicyRepo.GetByRouteID(route.ID)
	if err != nil {
		// No BackendTrafficPolicy to delete
		return nil
	}

	// Build the backend traffic policy name
	btpName := kubernetes.BackendTrafficPolicyName(route.K8sRouteName)

	// Delete from Kubernetes
	if err := s.k8sPolicies.DeleteBackendTrafficPolicy(ctx, domain.ProjectID, domain.Namespace, btpName); err != nil {
		return err
	}

	// Delete from database
	return s.backendTrafficPolicyRepo.Delete(policy.ID)
}

// deployEnvoyExtensionPolicy deploys EnvoyExtensionPolicy to Kubernetes
func (s *RouteService) deployEnvoyExtensionPolicy(ctx context.Context, route *models.Route, domain *models.Domain) error {
	// Get EnvoyExtensionPolicy from database (may be genuinely absent)
	var policy *models.EnvoyExtensionPolicy
	p, err := s.envoyExtensionPolicyRepo.GetByRouteID(route.ID)
	switch {
	case err == nil:
		policy = p
	case errors.Is(err, gorm.ErrRecordNotFound):
		// No EnvoyExtensionPolicy configured for this route. Legitimate:
		// policy stays nil and the cleanup branch below removes any stale
		// cluster object.
	default:
		// Any other error is a LOOKUP FAILURE, not an absence. Returning a
		// nil policy here would send the route down the extConfig == nil
		// branch, which DELETES the live EnvoyExtensionPolicy -- stripping
		// the route's WAF and ext-proc configuration while it is still
		// serving -- and then report success.
		return fmt.Errorf("load envoy extension policy for route %s: %w", route.ID, err)
	}

	// Get WafPolicy from database (may be genuinely absent)
	var wafPolicy *models.WafPolicy
	wp, err := s.wafPolicyRepo.GetByRouteID(route.ID)
	switch {
	case err == nil:
		wafPolicy = wp
	case errors.Is(err, gorm.ErrRecordNotFound):
		// No WAF policy configured for this route. Same reasoning as above.
	default:
		// Same hazard as the EnvoyExtensionPolicy lookup: a nil wafPolicy on
		// failure deletes the live policy and reports success.
		return fmt.Errorf("load waf policy for route %s: %w", route.ID, err)
	}

	// Handle ext-proc Backend CRD lifecycle
	extProcBackendName := kubernetes.GenerateExtProcBackendName(route.ID.String())
	if policy != nil && policy.Config.ExtProc != nil {
		// Create/update ext-proc Backend CRD
		//
		// Deliberately NOT extracted to a shared builder (Phase 2H, spec §6).
		// The two ExtProcBackendConfig sites differ in owner identity -- this one
		// sets RouteID; the domain path sets DomainID with an empty RouteID -- and
		// object construction is already shared via kubernetes.BuildExtProcBackend.
		// A parameterised builder would encode two owner semantics in one
		// signature for no reduction in size.
		backendConfig := &kubernetes.ExtProcBackendConfig{
			Name:      extProcBackendName,
			Namespace: domain.Namespace,
			GatewayID: domain.ID.String(),
			RouteID:   route.ID.String(),
			Service: kubernetes.ExtProcBackendRefPolicyConfig{
				Name:      policy.Config.ExtProc.BackendRef.Name,
				Namespace: policy.Config.ExtProc.BackendRef.Namespace,
				Port:      policy.Config.ExtProc.BackendRef.Port,
			},
		}
		backend := kubernetes.BuildExtProcBackend(backendConfig)
		if backend != nil {
			if err := s.k8sBackends.UpdateBackendUnstructured(ctx, domain.ProjectID, backend); err != nil {
				return fmt.Errorf("failed to create/update ext-proc Backend: %w", err)
			}
		}
	} else {
		// Delete ext-proc Backend CRD if ext-proc was removed
		_ = s.k8sBackends.DeleteBackend(ctx, domain.ProjectID, domain.Namespace, extProcBackendName)
	}

	// Build EnvoyExtensionPolicy config for Kubernetes (merged)
	extConfig := s.buildEnvoyExtensionPolicyConfig(route, domain, policy, wafPolicy)
	if extConfig == nil {
		// No extensions to deploy - delete any existing policy if present
		eepName := kubernetes.EnvoyExtensionPolicyName(route.K8sRouteName)
		// Return the delete error rather than discarding it, matching how the
		// SecurityPolicy cleanup branch returns its delete.
		return s.k8sPolicies.DeleteEnvoyExtensionPolicy(ctx, domain.ProjectID, domain.Namespace, eepName)
	}

	// Build the unstructured object
	extPolicy := kubernetes.BuildEnvoyExtensionPolicy(extConfig)
	if extPolicy == nil {
		return nil
	}

	// Create or update EnvoyExtensionPolicy in Kubernetes
	return s.k8sPolicies.UpdateEnvoyExtensionPolicy(ctx, domain.ProjectID, extPolicy)
}

// deleteEnvoyExtensionPolicy deletes EnvoyExtensionPolicy from Kubernetes
func (s *RouteService) deleteEnvoyExtensionPolicy(ctx context.Context, route *models.Route, domain *models.Domain) error {
	// Check if EnvoyExtensionPolicy exists for this route
	var policy *models.EnvoyExtensionPolicy
	p, err := s.envoyExtensionPolicyRepo.GetByRouteID(route.ID)
	if err == nil {
		policy = p
	}

	// Check if WafPolicy exists for this route
	var wafPolicy *models.WafPolicy
	w, err := s.wafPolicyRepo.GetByRouteID(route.ID)
	if err == nil {
		wafPolicy = w
	}

	// If neither EnvoyExtensionPolicy nor WafPolicy exists, nothing to delete
	if policy == nil && wafPolicy == nil {
		return nil
	}

	// Delete ext-proc Backend CRD if it exists
	extProcBackendName := kubernetes.GenerateExtProcBackendName(route.ID.String())
	_ = s.k8sBackends.DeleteBackend(ctx, domain.ProjectID, domain.Namespace, extProcBackendName)

	// Build the envoy extension policy name
	eepName := kubernetes.EnvoyExtensionPolicyName(route.K8sRouteName)

	// Delete from Kubernetes (the CRD contains both Lua/Wasm and WAF configurations)
	if err := s.k8sPolicies.DeleteEnvoyExtensionPolicy(ctx, domain.ProjectID, domain.Namespace, eepName); err != nil {
		return err
	}

	// Delete EnvoyExtensionPolicy from database (WAF is deleted by CASCADE on route deletion)
	if policy != nil {
		return s.envoyExtensionPolicyRepo.Delete(policy.ID)
	}

	return nil
}

// buildEnvoyExtensionPolicyConfig builds EnvoyExtensionPolicyK8sConfig from database model.
//
// The guard below decides *whether* to build at all and stays here; assembly
// itself is delegated to routeplan.BuildEnvoyExtensionPolicyK8sConfig, shared
// with the per-client route path in route_clients_apikey.go (Phase 2H).
func (s *RouteService) buildEnvoyExtensionPolicyConfig(route *models.Route, domain *models.Domain, policy *models.EnvoyExtensionPolicy, wafPolicy *models.WafPolicy) *kubernetes.EnvoyExtensionPolicyK8sConfig {
	// Check if we have any extensions to deploy
	hasGenericExtensions := policy != nil && !policy.Config.IsEmpty()
	hasWaf := wafPolicy != nil && !wafPolicy.Config.IsEmpty()

	if !hasGenericExtensions && !hasWaf {
		return nil
	}

	return routeplan.BuildEnvoyExtensionPolicyK8sConfig(route, domain, route.K8sRouteName, policy, wafPolicy, s.wafConfig)
}
