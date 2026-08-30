package routeplan

import (
	"fmt"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"sigs.k8s.io/yaml"
)

// SecurityPolicyConfigForDeploy builds the client-mode deploy SecurityPolicyConfig.
//
// Extracted verbatim from deploySecurityPolicy so the assembly sites can be
// diffed against each other. authConfig is the authorization already computed
// from client attachments and route.Config.DefaultTrafficPolicy.
func SecurityPolicyConfigForDeploy(route *models.Route, domain *models.Domain, policy *models.SecurityPolicy, authConfig *kubernetes.AuthorizationPolicyConfig) *kubernetes.SecurityPolicyConfig {
	// Only CORS is taken from the DB policy on this path -- the rest of the
	// general-mode feature set belongs to deployGeneralSecurityPolicy, and
	// pulling it in here would change what the cluster runs in client mode.
	var cors *models.CORSConfig
	if policy != nil {
		cors = policy.Config.CORS
	}

	return AssembleSecurityPolicyConfig(SecurityPolicyAssembly{
		Route:  route,
		Domain: domain,
		CORS:   cors,
		// Authorization: merged direct IPs + IP-only client IPs, or the
		// DefaultTrafficPolicy override, already computed by the caller.
		Authorization: authConfig,
	})
}

// SecurityPolicyAssembly is everything the SecurityPolicy assembler needs.
//
// Each call path gathers this differently -- from the persisted row, from the
// submitted API input, from a single client attachment, or (on the client-mode
// deploy path) from authorization already computed out of live attachments --
// and they then share one pure assembler. Separating gather from assemble is
// what stops the five paths drifting again.
type SecurityPolicyAssembly struct {
	Route  *models.Route
	Domain *models.Domain

	// TargetName is the HTTPRoute/GRPCRoute this policy attaches to, and the
	// stem of the policy's own name. Empty means route.K8sRouteName; only the
	// per-client fan-out overrides it.
	TargetName string

	// CORS is gathered by each path from whichever source it has: the persisted
	// row, the submitted input, or -- on the client-mode deploy and per-client
	// paths -- the route-level policy row. Every path that carries CORS at all
	// mapped it with the same six-field copy.
	CORS *models.CORSConfig

	// Persisted and Input carry the FULL general-mode feature set
	// (authorization, API key, JWT, OIDC, ext-auth). At most one is set.
	//
	// The client-mode deploy path and the per-client path set NEITHER, on
	// purpose: they only ever read CORS out of their policy row, and deriving
	// the rest of the feature set from it here would change what the cluster
	// actually runs on those paths.
	Persisted *models.SecurityPolicy
	Input     *SecurityPolicyInput

	// ClientCIDRs is the client IP allowlist carried alongside Input on the
	// preview path.
	ClientCIDRs []string

	// Authorization, when non-nil, is authorization the caller already computed
	// (client-mode deploy: client attachments merged with
	// route.Config.DefaultTrafficPolicy). It is assigned as-is. No path sets
	// both this and one of Persisted/Input/Client.
	Authorization *kubernetes.AuthorizationPolicyConfig

	// Client, RequireIP and APIKeySecretName drive the per-client fan-out.
	// APIKeySecretName is gathered by the caller because deriving it needs
	// s.k8sService; keeping it out here is what lets the assembler stay pure.
	Client           *ClientAuthCategory
	RequireIP        bool
	APIKeySecretName string
}

// AssembleSecurityPolicyConfig is the single SecurityPolicy assembler.
//
// It replaces five separate construction sites: the client-mode deploy
// assembly, securityPolicyConfigFromDB, the "minimal merge base" that
// GenerateYAMLs used to build inline, generateSecurityPolicyYAML, and
// buildAPIKeySecurityPolicyConfig. The identity block (Name, Namespace,
// GatewayID, RouteID, TargetRef) and the CORS mapping were duplicated across
// those sites and are now owned here exactly once.
//
// Callers keep their own "is there anything worth deploying" guards: this
// function always returns a config, and never inspects a repository to decide.
//
// Pure: no receiver, no repository access, no clock, no environment.
func AssembleSecurityPolicyConfig(in SecurityPolicyAssembly) *kubernetes.SecurityPolicyConfig {
	targetName := in.TargetName
	if targetName == "" {
		targetName = in.Route.K8sRouteName
	}

	config := &kubernetes.SecurityPolicyConfig{
		Name:      kubernetes.SecurityPolicyName(targetName),
		Namespace: in.Domain.Namespace,
		GatewayID: in.Domain.ID.String(),
		RouteID:   in.Route.ID.String(),
		TargetRef: kubernetes.SecurityPolicyTargetRef{
			Group: "gateway.networking.k8s.io",
			Kind:  GetRouteKind(in.Route.Protocol),
			Name:  targetName,
		},
	}

	// CORS -- one mapping for every path that carries it.
	if in.CORS != nil {
		config.CORS = &kubernetes.CORSPolicyConfig{
			AllowOrigins:     in.CORS.AllowOrigins,
			AllowMethods:     in.CORS.AllowMethods,
			AllowHeaders:     in.CORS.AllowHeaders,
			ExposeHeaders:    in.CORS.ExposeHeaders,
			MaxAge:           in.CORS.MaxAge,
			AllowCredentials: in.CORS.AllowCredentials,
		}
	}

	if in.Persisted != nil {
		applyPersistedSecurityFeatures(config, in.Persisted)
	}

	if in.Input != nil || len(in.ClientCIDRs) > 0 {
		applyInputSecurityFeatures(config, in.Input, in.ClientCIDRs)
	}

	if in.Client != nil {
		applyClientSecurityFeatures(config, *in.Client, in.RequireIP, in.APIKeySecretName)
	}

	// Authorization the caller already computed (client-mode deploy) is
	// assigned last and as-is.
	if in.Authorization != nil {
		config.Authorization = in.Authorization
	}

	return config
}

// SecurityPolicyConfigFromDB converts DB security policy model to SecurityPolicyConfig for K8s CRD building.
// Shared by buildSecurityPolicyConfig, deployGeneralSecurityPolicy, and generateSecurityPolicyYAMLFromDB.
func SecurityPolicyConfigFromDB(route *models.Route, domain *models.Domain, policy *models.SecurityPolicy) *kubernetes.SecurityPolicyConfig {
	if policy == nil || !policy.Config.HasAnyConfig() {
		return nil
	}

	return AssembleSecurityPolicyConfig(SecurityPolicyAssembly{
		Route:     route,
		Domain:    domain,
		CORS:      policy.Config.CORS,
		Persisted: policy,
	})
}

// GenerateSecurityPolicyYAML generates SecurityPolicy YAML for CORS and client IP allowlist
// clientCIDRs parameter allows including client IPs in the preview (for accurate representation)
func GenerateSecurityPolicyYAML(route *models.Route, domain *models.Domain, securityInput *SecurityPolicyInput, clientCIDRs []string) string {
	hasCORS := securityInput != nil && securityInput.CORS != nil
	hasClientIPs := len(clientCIDRs) > 0
	hasGeneralAuth := securityInput != nil && securityInput.Authorization != nil
	hasAPIKeyAuth := securityInput != nil && securityInput.APIKeyAuth != nil
	hasJWT := securityInput != nil && securityInput.JWT != nil
	hasOIDC := securityInput != nil && securityInput.OIDC != nil
	hasExtAuth := securityInput != nil && securityInput.ExtAuth != nil

	if !hasCORS && !hasClientIPs && !hasGeneralAuth && !hasAPIKeyAuth && !hasJWT && !hasOIDC && !hasExtAuth {
		return ""
	}

	var cors *models.CORSConfig
	if securityInput != nil {
		cors = securityInput.CORS
	}

	config := AssembleSecurityPolicyConfig(SecurityPolicyAssembly{
		Route:       route,
		Domain:      domain,
		CORS:        cors,
		Input:       securityInput,
		ClientCIDRs: clientCIDRs,
	})

	// Build the SecurityPolicy object
	securityPolicy := kubernetes.BuildSecurityPolicy(config)
	if securityPolicy == nil {
		return ""
	}

	// Marshal to YAML
	yamlBytes, err := yaml.Marshal(securityPolicy.Object)
	if err != nil {
		return fmt.Sprintf("# Error generating SecurityPolicy YAML: %v", err)
	}

	return string(yamlBytes)
}

// GenerateSecurityPolicyYAMLFromDB generates SecurityPolicy YAML from database model
func GenerateSecurityPolicyYAMLFromDB(route *models.Route, domain *models.Domain, policy *models.SecurityPolicy) string {
	config := SecurityPolicyConfigFromDB(route, domain, policy)
	if config == nil {
		return ""
	}

	// NOTE: SecurityPolicyConfig.ExtAuthBackendName is deliberately left unset
	// on every route-level path. This function used to set it here while the
	// deploy path (deployGeneralSecurityPolicy) did not -- a divergence in
	// intent with no output effect, because neither BuildSecurityPolicy nor
	// buildExtAuthConfig ever reads the field: ext-auth backend refs are
	// derived from config.ExtAuth.HTTP/GRPC.BackendRef. Deploy is
	// authoritative, so the field now has exactly one behaviour route-wide
	// (unset) instead of two. Only the per-client path sets it, from
	// ClientAuthCategory.ExtAuthBackendName, which is a genuinely per-client
	// value rather than a second opinion about the same one.

	// Build the SecurityPolicy object
	securityPolicy := kubernetes.BuildSecurityPolicy(config)
	if securityPolicy == nil {
		return ""
	}

	// Marshal to YAML
	yamlBytes, err := yaml.Marshal(securityPolicy.Object)
	if err != nil {
		return fmt.Sprintf("# Error generating SecurityPolicy YAML: %v", err)
	}

	return string(yamlBytes)
}
