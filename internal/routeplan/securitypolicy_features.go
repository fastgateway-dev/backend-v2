package routeplan

import (
	"strings"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
)

// applyPersistedSecurityFeatures maps the general-mode feature set off a
// persisted SecurityPolicy row. Body moved verbatim out of
// SecurityPolicyConfigFromDB; CORS is handled by the shared block above.
//
// ExtAuthBackendName is deliberately NOT set here -- see the note on
// GenerateSecurityPolicyYAMLFromDB.
func applyPersistedSecurityFeatures(config *kubernetes.SecurityPolicyConfig, policy *models.SecurityPolicy) {
	// Authorization (IP allowlisting, headers, methods)
	if policy.Config.Authorization != nil {
		authRules := make([]kubernetes.AuthorizationRulePolicyConfig, 0, len(policy.Config.Authorization.Rules))
		for _, rule := range policy.Config.Authorization.Rules {
			policyRule := kubernetes.AuthorizationRulePolicyConfig{
				Action:      rule.Action,
				ClientCIDRs: rule.Principal.ClientCIDRs,
			}
			if len(rule.Principal.Headers) > 0 {
				for _, h := range rule.Principal.Headers {
					policyRule.Headers = append(policyRule.Headers, kubernetes.HeaderMatchPolicyConfig{
						Name:   h.Name,
						Values: h.Values,
					})
				}
			}
			if rule.Operation != nil && len(rule.Operation.Methods) > 0 {
				policyRule.Methods = rule.Operation.Methods
			}
			authRules = append(authRules, policyRule)
		}
		config.Authorization = &kubernetes.AuthorizationPolicyConfig{
			DefaultAction: policy.Config.Authorization.DefaultAction,
			Rules:         authRules,
		}
	}

	// API Key Auth
	if policy.Config.APIKeyAuth != nil {
		credRefs := make([]kubernetes.SecretRefConfig, 0, len(policy.Config.APIKeyAuth.CredentialRefs))
		for _, ref := range policy.Config.APIKeyAuth.CredentialRefs {
			credRefs = append(credRefs, kubernetes.SecretRefConfig{Name: ref.Name, Namespace: ref.Namespace})
		}
		extractFrom := make([]kubernetes.APIKeyExtractFromConfig, 0, len(policy.Config.APIKeyAuth.ExtractFrom))
		for _, ef := range policy.Config.APIKeyAuth.ExtractFrom {
			extractFrom = append(extractFrom, kubernetes.APIKeyExtractFromConfig{Headers: ef.Headers})
		}
		config.APIKeyAuth = &kubernetes.APIKeyAuthPolicyConfig{CredentialRefs: credRefs, ExtractFrom: extractFrom}
	}

	// JWT
	if policy.Config.JWT != nil {
		providers := make([]kubernetes.JWTProviderPolicyConfig, 0, len(policy.Config.JWT.Providers))
		for _, p := range policy.Config.JWT.Providers {
			provider := kubernetes.JWTProviderPolicyConfig{Name: p.Name, Issuer: p.Issuer, Audiences: p.Audiences}
			if p.RemoteJWKS != nil {
				provider.JWKSURL = p.RemoteJWKS.URI
			}
			for _, cth := range p.ClaimToHeaders {
				provider.ClaimToHeaders = append(provider.ClaimToHeaders, kubernetes.JWTClaimToHeaderPolicyConfig{Claim: cth.Claim, Header: cth.Header})
			}
			providers = append(providers, provider)
		}
		config.JWT = &kubernetes.JWTAuthPolicyConfig{Providers: providers}
	}

	// OIDC
	if policy.Config.OIDC != nil {
		config.OIDC = &kubernetes.OIDCPolicyConfig{
			ClientID:     policy.Config.OIDC.ClientID,
			RedirectURL:  policy.Config.OIDC.RedirectURL,
			LogoutPath:   policy.Config.OIDC.LogoutPath,
			Scopes:       policy.Config.OIDC.Scopes,
			CookieDomain: policy.Config.OIDC.CookieDomain,
		}
		if policy.Config.OIDC.Provider != nil {
			config.OIDC.Issuer = policy.Config.OIDC.Provider.Issuer
		}
		if policy.Config.OIDC.ClientSecret != nil {
			config.OIDC.ClientSecretName = policy.Config.OIDC.ClientSecret.Name
			config.OIDC.ClientSecretNS = policy.Config.OIDC.ClientSecret.Namespace
		}
		// Fallback to FastGatewayNamespace if clientSecret namespace is empty
		if config.OIDC.ClientSecretNS == "" {
			config.OIDC.ClientSecretNS = kubernetes.FastGatewayNamespace
		}
	}

	// ExtAuth
	if policy.Config.ExtAuth != nil {
		config.ExtAuth = policy.Config.ExtAuth
	}
}

// applyInputSecurityFeatures maps the general-mode feature set off a submitted
// SecurityPolicyInput, plus the client IP allowlist that path carries. Body
// moved verbatim out of generateSecurityPolicyYAML; CORS is handled by the
// shared block above.
//
// ExtAuthBackendName is deliberately NOT set here -- see the note on
// GenerateSecurityPolicyYAMLFromDB.
func applyInputSecurityFeatures(config *kubernetes.SecurityPolicyConfig, securityInput *SecurityPolicyInput, clientCIDRs []string) {
	hasClientIPs := len(clientCIDRs) > 0
	hasGeneralAuth := securityInput != nil && securityInput.Authorization != nil
	hasAPIKeyAuth := securityInput != nil && securityInput.APIKeyAuth != nil
	hasJWT := securityInput != nil && securityInput.JWT != nil
	hasOIDC := securityInput != nil && securityInput.OIDC != nil
	hasExtAuth := securityInput != nil && securityInput.ExtAuth != nil

	// Build authorization config from client IPs (client mode) - mutually exclusive with general mode auth
	if hasClientIPs && !hasGeneralAuth {
		seen := make(map[string]bool)
		uniqueCIDRs := make([]string, 0, len(clientCIDRs))
		for _, cidr := range clientCIDRs {
			normalized := NormalizeCIDR(cidr)
			if !seen[normalized] {
				seen[normalized] = true
				uniqueCIDRs = append(uniqueCIDRs, normalized)
			}
		}
		config.Authorization = &kubernetes.AuthorizationPolicyConfig{
			DefaultAction: "Deny",
			Rules: []kubernetes.AuthorizationRulePolicyConfig{{
				Action:      "Allow",
				ClientCIDRs: uniqueCIDRs,
			}},
		}
	}

	// General mode: authorization from input (takes precedence over client IPs)
	if hasGeneralAuth {
		rule := kubernetes.AuthorizationRulePolicyConfig{
			Action: "Allow",
		}
		if len(securityInput.Authorization.AllowedCIDRs) > 0 {
			cidrs := make([]string, 0, len(securityInput.Authorization.AllowedCIDRs))
			for _, cidr := range securityInput.Authorization.AllowedCIDRs {
				cidrs = append(cidrs, NormalizeCIDR(cidr))
			}
			rule.ClientCIDRs = cidrs
		}
		if len(securityInput.Authorization.Headers) > 0 {
			for _, h := range securityInput.Authorization.Headers {
				rule.Headers = append(rule.Headers, kubernetes.HeaderMatchPolicyConfig{Name: h.Name, Values: h.Values})
			}
		}
		if len(securityInput.Authorization.Methods) > 0 {
			rule.Methods = securityInput.Authorization.Methods
		}
		config.Authorization = &kubernetes.AuthorizationPolicyConfig{
			DefaultAction: "Deny",
			Rules:         []kubernetes.AuthorizationRulePolicyConfig{rule},
		}
	}

	// General mode: API Key Auth from input
	if hasAPIKeyAuth {
		config.APIKeyAuth = &kubernetes.APIKeyAuthPolicyConfig{
			CredentialRefs: []kubernetes.SecretRefConfig{{
				Name:      securityInput.APIKeyAuth.SecretName,
				Namespace: kubernetes.FastGatewayNamespace,
			}},
			ExtractFrom: []kubernetes.APIKeyExtractFromConfig{{
				Headers: []string{securityInput.APIKeyAuth.HeaderName},
			}},
		}
	}

	// General mode: JWT from input
	if hasJWT {
		provider := kubernetes.JWTProviderPolicyConfig{
			Name:   "route-jwt",
			Issuer: securityInput.JWT.Issuer,
		}
		if securityInput.JWT.JWKSURL != "" {
			provider.JWKSURL = securityInput.JWT.JWKSURL
		}
		if len(securityInput.JWT.Audiences) > 0 {
			provider.Audiences = securityInput.JWT.Audiences
		}
		for _, cth := range securityInput.JWT.ClaimToHeaders {
			provider.ClaimToHeaders = append(provider.ClaimToHeaders, kubernetes.JWTClaimToHeaderPolicyConfig{
				Claim:  cth.Claim,
				Header: cth.Header,
			})
		}
		config.JWT = &kubernetes.JWTAuthPolicyConfig{
			Providers: []kubernetes.JWTProviderPolicyConfig{provider},
		}
	}

	// General mode: OIDC from input
	if hasOIDC {
		config.OIDC = &kubernetes.OIDCPolicyConfig{
			Issuer:           securityInput.OIDC.Issuer,
			ClientID:         securityInput.OIDC.ClientID,
			ClientSecretName: securityInput.OIDC.ClientSecretName,
			ClientSecretNS:   kubernetes.FastGatewayNamespace,
			RedirectURL:      securityInput.OIDC.RedirectURL,
			LogoutPath:       securityInput.OIDC.LogoutPath,
			Scopes:           securityInput.OIDC.Scopes,
			CookieDomain:     securityInput.OIDC.CookieDomain,
		}
	}

	// Both modes: ExtAuth from input
	if hasExtAuth {
		config.ExtAuth = securityInput.ExtAuth
	}
}

// applyClientSecurityFeatures maps the per-client fan-out feature set off a
// single client attachment. Body moved verbatim out of
// buildAPIKeySecurityPolicyConfig, with the one impure step (deriving the API
// key Secret name via s.k8sService) hoisted to the caller and passed in as
// apiKeySecretName; CORS is handled by the shared block above.
func applyClientSecurityFeatures(config *kubernetes.SecurityPolicyConfig, client ClientAuthCategory, requireIP bool, apiKeySecretName string) {
	// Add API key auth config if enabled
	if client.EnableAPIKey && client.APIKey != "" {
		config.APIKeyAuth = &kubernetes.APIKeyAuthPolicyConfig{
			CredentialRefs: []kubernetes.SecretRefConfig{
				{Name: apiKeySecretName, Namespace: kubernetes.FastGatewayNamespace},
			},
			ExtractFrom: []kubernetes.APIKeyExtractFromConfig{
				{Headers: []string{client.APIKeyHeaderName}},
			},
		}
	}

	// Add JWT auth config if enabled
	if client.EnableJWT && client.JWTIssuer != "" {
		providerName := "client-" + client.ClientID.String()[:8]
		provider := kubernetes.JWTProviderPolicyConfig{
			Name:      providerName,
			Issuer:    client.JWTIssuer,
			JWKSURL:   client.JWTJWKSURL,
			Audiences: client.JWTAudiences,
		}

		// Add claim-to-headers if configured
		if len(client.JWTClaimToHeaders) > 0 {
			for _, cth := range client.JWTClaimToHeaders {
				provider.ClaimToHeaders = append(provider.ClaimToHeaders, kubernetes.JWTClaimToHeaderPolicyConfig{
					Claim:  cth.Claim,
					Header: cth.Header,
				})
			}
		}

		config.JWT = &kubernetes.JWTAuthPolicyConfig{
			Providers: []kubernetes.JWTProviderPolicyConfig{provider},
		}

		// Add JWT claim authorization if required claims are set
		if len(client.JWTRequiredClaims) > 0 {
			jwtPrincipal := &kubernetes.JWTPrincipalPolicyConfig{
				Provider: providerName,
			}
			for _, claim := range client.JWTRequiredClaims {
				jwtPrincipal.Claims = append(jwtPrincipal.Claims, kubernetes.JWTClaimRulePolicyConfig{
					Name:      claim.Name,
					Values:    claim.Values,
					ValueType: claim.ValueType,
				})
			}

			// If authorization already exists (from IP), add JWT to it
			// Otherwise create new authorization
			if config.Authorization == nil {
				config.Authorization = &kubernetes.AuthorizationPolicyConfig{
					DefaultAction: "Deny",
					Rules:         []kubernetes.AuthorizationRulePolicyConfig{},
				}
			}

			// Add JWT claim rule
			rule := kubernetes.AuthorizationRulePolicyConfig{
				Action: "Allow",
				JWT:    jwtPrincipal,
			}

			// If IP is also required, add CIDRs to the same rule (AND logic)
			if requireIP && len(client.IPCIDRs) > 0 {
				rule.ClientCIDRs = client.IPCIDRs
			}

			config.Authorization.Rules = append(config.Authorization.Rules, rule)
		} else if requireIP && len(client.IPCIDRs) > 0 {
			// JWT without required claims but with IP check
			config.Authorization = &kubernetes.AuthorizationPolicyConfig{
				DefaultAction: "Deny",
				Rules: []kubernetes.AuthorizationRulePolicyConfig{
					{
						Action:      "Allow",
						ClientCIDRs: client.IPCIDRs,
					},
				},
			}
		}
	} else if requireIP && len(client.IPCIDRs) > 0 {
		// No JWT, just API key with IP check
		config.Authorization = &kubernetes.AuthorizationPolicyConfig{
			DefaultAction: "Deny",
			Rules: []kubernetes.AuthorizationRulePolicyConfig{
				{
					Action:      "Allow",
					ClientCIDRs: client.IPCIDRs,
				},
			},
		}
	}

	// Add header/method authorization to existing rules (AND logic with other principals)
	hasHeaderOrMethod := (client.EnableHeaderAuth && len(client.HeaderMatches) > 0) || len(client.AllowedMethods) > 0
	if hasHeaderOrMethod {
		// Ensure authorization config exists
		if config.Authorization == nil {
			config.Authorization = &kubernetes.AuthorizationPolicyConfig{
				DefaultAction: "Deny",
				Rules:         []kubernetes.AuthorizationRulePolicyConfig{{Action: "Allow"}},
			}
		}

		// Add headers and methods to each rule
		for i := range config.Authorization.Rules {
			if client.EnableHeaderAuth && len(client.HeaderMatches) > 0 {
				for _, h := range client.HeaderMatches {
					config.Authorization.Rules[i].Headers = append(config.Authorization.Rules[i].Headers, kubernetes.HeaderMatchPolicyConfig{
						Name:   h.Name,
						Values: h.Values,
					})
				}
			}
			if len(client.AllowedMethods) > 0 {
				config.Authorization.Rules[i].Methods = client.AllowedMethods
			}
		}
	}

	// Add ExtAuth config if client has ext-auth configured
	if client.ExtAuth != nil && client.ExtAuthBackendName != "" {
		config.ExtAuth = client.ExtAuth
		config.ExtAuthBackendName = client.ExtAuthBackendName
	}
}

// BuildAuthorizationConfigFromInput converts AuthorizationInput to models.AuthorizationConfig
func BuildAuthorizationConfigFromInput(input *AuthorizationInput) *models.AuthorizationConfig {
	if input == nil {
		return nil
	}

	hasCIDRs := len(input.AllowedCIDRs) > 0
	hasHeaders := len(input.Headers) > 0
	hasMethods := len(input.Methods) > 0
	if !hasCIDRs && !hasHeaders && !hasMethods {
		return nil
	}

	rule := models.AuthorizationRule{
		Action: "Allow",
	}

	if hasCIDRs {
		cidrs := make([]string, 0, len(input.AllowedCIDRs))
		for _, cidr := range input.AllowedCIDRs {
			cidrs = append(cidrs, NormalizeCIDR(cidr))
		}
		rule.Principal.ClientCIDRs = cidrs
	}

	if hasHeaders {
		rule.Principal.Headers = input.Headers
	}

	if hasMethods {
		methods := make([]string, 0, len(input.Methods))
		for _, m := range input.Methods {
			methods = append(methods, strings.ToUpper(m))
		}
		rule.Operation = &models.AuthorizationOperation{Methods: methods}
	}

	return &models.AuthorizationConfig{
		DefaultAction: "Deny",
		Rules:         []models.AuthorizationRule{rule},
	}
}

// BuildAPIKeyAuthConfigFromInput converts APIKeyAuthInput to models.APIKeyAuthConfig
func BuildAPIKeyAuthConfigFromInput(input *APIKeyAuthInput) *models.APIKeyAuthConfig {
	if input == nil {
		return nil
	}
	return &models.APIKeyAuthConfig{
		CredentialRefs: []models.SecretRef{{
			Name:      input.SecretName,
			Namespace: kubernetes.FastGatewayNamespace,
		}},
		ExtractFrom: []models.APIKeyExtractFrom{{
			Headers: []string{input.HeaderName},
		}},
	}
}

// BuildJWTConfigFromInput converts JWTInput to models.JWTConfig
func BuildJWTConfigFromInput(input *JWTInput) *models.JWTConfig {
	if input == nil {
		return nil
	}
	return &models.JWTConfig{
		Providers: []models.JWTProvider{{
			Name:   "route-jwt",
			Issuer: input.Issuer,
			RemoteJWKS: &models.RemoteJWKS{
				URI: input.JWKSURL,
			},
			Audiences:      input.Audiences,
			ClaimToHeaders: input.ClaimToHeaders,
		}},
	}
}

// BuildOIDCConfigFromInput converts OIDCInput to models.OIDCConfig
func BuildOIDCConfigFromInput(input *OIDCInput) *models.OIDCConfig {
	if input == nil {
		return nil
	}
	return &models.OIDCConfig{
		Provider: &models.OIDCProvider{
			Issuer: input.Issuer,
		},
		ClientID: input.ClientID,
		ClientSecret: &models.SecretRef{
			Name:      input.ClientSecretName,
			Namespace: kubernetes.FastGatewayNamespace,
		},
		RedirectURL:  input.RedirectURL,
		LogoutPath:   input.LogoutPath,
		Scopes:       input.Scopes,
		CookieDomain: input.CookieDomain,
	}
}
