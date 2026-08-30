package kubernetes

import (
	"fmt"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// BuildSecurityPolicy builds an Envoy Gateway SecurityPolicy from config
// TODO: Migrate CORS to HTTPRoute filters when GEP-1767 is standard (https://gateway-api.sigs.k8s.io/geps/gep-1767/)
func BuildSecurityPolicy(config *SecurityPolicyConfig) *unstructured.Unstructured {
	if config == nil {
		return nil
	}

	spec := map[string]interface{}{
		"targetRef": map[string]interface{}{
			"group": config.TargetRef.Group,
			"kind":  config.TargetRef.Kind,
			"name":  config.TargetRef.Name,
		},
	}

	// Add CORS configuration if present
	if config.CORS != nil {
		corsConfig := buildCORSConfig(config.CORS)
		if corsConfig != nil {
			spec["cors"] = corsConfig
		}
	}

	// Add Authorization configuration if present (IP allowlisting)
	if config.Authorization != nil {
		authConfig := buildAuthorizationConfig(config.Authorization)
		if authConfig != nil {
			spec["authorization"] = authConfig
		}
	}

	// Add API Key Auth configuration if present
	if config.APIKeyAuth != nil {
		apiKeyAuthConfig := buildAPIKeyAuthConfig(config.APIKeyAuth)
		if apiKeyAuthConfig != nil {
			spec["apiKeyAuth"] = apiKeyAuthConfig
		}
	}

	// Add JWT Auth configuration if present
	if config.JWT != nil {
		jwtConfig := buildJWTAuthConfig(config.JWT)
		if jwtConfig != nil {
			spec["jwt"] = jwtConfig
		}
	}

	// Add OIDC configuration if present
	if config.OIDC != nil {
		oidcConfig := buildOIDCConfig(config.OIDC)
		if oidcConfig != nil {
			spec["oidc"] = oidcConfig
		}
	}

	// Add ExtAuth configuration if present
	if config.ExtAuth != nil {
		extAuthConfig := buildExtAuthConfig(config.ExtAuth)
		if extAuthConfig != nil {
			spec["extAuth"] = extAuthConfig
		}
	}

	// Check if any security feature is configured
	_, hasCORS := spec["cors"]
	_, hasAuth := spec["authorization"]
	_, hasAPIKeyAuth := spec["apiKeyAuth"]
	_, hasJWT := spec["jwt"]
	_, hasOIDC := spec["oidc"]
	_, hasExtAuth := spec["extAuth"]
	if !hasCORS && !hasAuth && !hasAPIKeyAuth && !hasJWT && !hasOIDC && !hasExtAuth {
		// No security features configured
		return nil
	}

	securityPolicy := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "gateway.envoyproxy.io/v1alpha1",
			"kind":       "SecurityPolicy",
			"metadata": map[string]interface{}{
				"name":      config.Name,
				"namespace": config.Namespace,
				"labels":    ForRouteInterface(config.GatewayID, config.RouteID),
			},
			"spec": spec,
		},
	}

	return securityPolicy
}

// buildCORSConfig builds the CORS configuration for SecurityPolicy
// Uses Envoy Gateway's SecurityPolicy CORS format
func buildCORSConfig(cors *CORSPolicyConfig) map[string]interface{} {
	if cors == nil {
		return nil
	}

	// Check if CORS has any configuration
	if len(cors.AllowOrigins) == 0 && len(cors.AllowMethods) == 0 && len(cors.AllowHeaders) == 0 {
		return nil
	}

	corsConfig := make(map[string]interface{})

	// allowOrigins - simple string array for Envoy Gateway SecurityPolicy
	if len(cors.AllowOrigins) > 0 {
		corsConfig["allowOrigins"] = stringSliceToInterfaceSlice(cors.AllowOrigins)
	}

	if len(cors.AllowMethods) > 0 {
		corsConfig["allowMethods"] = stringSliceToInterfaceSlice(cors.AllowMethods)
	}
	if len(cors.AllowHeaders) > 0 {
		corsConfig["allowHeaders"] = stringSliceToInterfaceSlice(cors.AllowHeaders)
	}
	if len(cors.ExposeHeaders) > 0 {
		corsConfig["exposeHeaders"] = stringSliceToInterfaceSlice(cors.ExposeHeaders)
	}
	if cors.MaxAge != nil {
		corsConfig["maxAge"] = fmt.Sprintf("%ds", *cors.MaxAge)
	}
	if cors.AllowCredentials != nil {
		corsConfig["allowCredentials"] = *cors.AllowCredentials
	}

	return corsConfig
}

// buildAuthorizationConfig builds the authorization configuration for SecurityPolicy
// Uses Envoy Gateway's SecurityPolicy authorization format for IP allowlisting and JWT claims
func buildAuthorizationConfig(auth *AuthorizationPolicyConfig) map[string]interface{} {
	if auth == nil {
		return nil
	}
	if len(auth.Rules) == 0 && auth.DefaultAction != "Deny" {
		return nil
	}

	rules := make([]interface{}, 0, len(auth.Rules))
	for _, rule := range auth.Rules {
		// Check if rule has any principal or operation
		hasClientCIDRs := len(rule.ClientCIDRs) > 0
		hasJWT := rule.JWT != nil && rule.JWT.Provider != ""
		hasHeaders := len(rule.Headers) > 0
		hasMethods := len(rule.Methods) > 0

		if !hasClientCIDRs && !hasJWT && !hasHeaders && !hasMethods {
			continue
		}

		principal := map[string]interface{}{}

		// Build clientCIDRs if present
		if hasClientCIDRs {
			cidrs := make([]interface{}, 0, len(rule.ClientCIDRs))
			for _, cidr := range rule.ClientCIDRs {
				cidrs = append(cidrs, cidr)
			}
			principal["clientCIDRs"] = cidrs
		}

		// Build JWT principal if present
		if hasJWT {
			jwtPrincipal := map[string]interface{}{
				"provider": rule.JWT.Provider,
			}

			// Add claims if present
			if len(rule.JWT.Claims) > 0 {
				claims := make([]interface{}, 0, len(rule.JWT.Claims))
				for _, claim := range rule.JWT.Claims {
					claimMap := map[string]interface{}{
						"name":   claim.Name,
						"values": stringSliceToInterfaceSlice(claim.Values),
					}
					// Determine the effective valueType for the CRD.
					// Envoy Gateway CRD supports: "String" (default), "StringArray"
					// FastGateway internal types like "StringContains" are not supported by CRD.
					//
					// IMPORTANT: Envoy Gateway's jwt_authn filter normalizes the "scope" claim
					// via normalize_payload_in_metadata.space_delimited_claims, splitting it from
					// a space-delimited string into an array. Therefore, RBAC matching on "scope"
					// must always use "StringArray" regardless of the user-specified valueType.
					effectiveValueType := claim.ValueType
					if claim.Name == "scope" {
						effectiveValueType = "StringArray"
					}
					switch effectiveValueType {
					case "StringArray":
						claimMap["valueType"] = "StringArray"
					}
					// "Exact", "String", "" all use the default (String), so no need to set
					claims = append(claims, claimMap)
				}
				jwtPrincipal["claims"] = claims
			}

			principal["jwt"] = jwtPrincipal
		}

		// Build headers if present
		if hasHeaders {
			headers := make([]interface{}, 0, len(rule.Headers))
			for _, h := range rule.Headers {
				headers = append(headers, map[string]interface{}{
					"name":   h.Name,
					"values": stringSliceToInterfaceSlice(h.Values),
				})
			}
			principal["headers"] = headers
		}

		ruleMap := map[string]interface{}{
			"action":    rule.Action,
			"principal": principal,
		}

		// Build operation.methods if present
		if hasMethods {
			ruleMap["operation"] = map[string]interface{}{
				"methods": stringSliceToInterfaceSlice(rule.Methods),
			}
		}

		rules = append(rules, ruleMap)
	}

	if len(rules) == 0 {
		if auth.DefaultAction == "Deny" {
			// Deny-all with no rules: block everything
			return map[string]interface{}{
				"defaultAction": auth.DefaultAction,
			}
		}
		return nil
	}

	return map[string]interface{}{
		"defaultAction": auth.DefaultAction,
		"rules":         rules,
	}
}

// buildAPIKeyAuthConfig builds the API Key Auth configuration for SecurityPolicy
// Uses Envoy Gateway's SecurityPolicy apiKeyAuth format
func buildAPIKeyAuthConfig(apiKeyAuth *APIKeyAuthPolicyConfig) map[string]interface{} {
	if apiKeyAuth == nil || len(apiKeyAuth.CredentialRefs) == 0 {
		return nil
	}

	config := map[string]interface{}{}

	// Build credentialRefs
	refs := make([]interface{}, 0, len(apiKeyAuth.CredentialRefs))
	for _, ref := range apiKeyAuth.CredentialRefs {
		refMap := map[string]interface{}{
			"name": ref.Name,
		}
		if ref.Namespace != "" {
			refMap["namespace"] = ref.Namespace
		}
		refs = append(refs, refMap)
	}
	config["credentialRefs"] = refs

	// Build extractFrom
	if len(apiKeyAuth.ExtractFrom) > 0 {
		extractFrom := make([]interface{}, 0, len(apiKeyAuth.ExtractFrom))
		for _, ef := range apiKeyAuth.ExtractFrom {
			efMap := map[string]interface{}{}
			if len(ef.Headers) > 0 {
				efMap["headers"] = stringSliceToInterfaceSlice(ef.Headers)
			}
			if len(efMap) > 0 {
				extractFrom = append(extractFrom, efMap)
			}
		}
		if len(extractFrom) > 0 {
			config["extractFrom"] = extractFrom
		}
	}

	return config
}

// buildJWTAuthConfig builds the JWT Auth configuration for SecurityPolicy
// Uses Envoy Gateway's SecurityPolicy jwt format
func buildJWTAuthConfig(jwt *JWTAuthPolicyConfig) map[string]interface{} {
	if jwt == nil || len(jwt.Providers) == 0 {
		return nil
	}

	providers := make([]interface{}, 0, len(jwt.Providers))
	for _, p := range jwt.Providers {
		provider := map[string]interface{}{
			"name":   p.Name,
			"issuer": p.Issuer,
		}

		// Add remoteJWKS
		if p.JWKSURL != "" {
			provider["remoteJWKS"] = map[string]interface{}{
				"uri": p.JWKSURL,
			}
		}

		// Add audiences if present
		if len(p.Audiences) > 0 {
			provider["audiences"] = stringSliceToInterfaceSlice(p.Audiences)
		}

		// Add claimToHeaders if present
		if len(p.ClaimToHeaders) > 0 {
			claimToHeaders := make([]interface{}, 0, len(p.ClaimToHeaders))
			for _, cth := range p.ClaimToHeaders {
				claimToHeaders = append(claimToHeaders, map[string]interface{}{
					"claim":  cth.Claim,
					"header": cth.Header,
				})
			}
			provider["claimToHeaders"] = claimToHeaders
		}

		providers = append(providers, provider)
	}

	return map[string]interface{}{
		"providers": providers,
	}
}

// buildOIDCConfig builds the OIDC configuration for SecurityPolicy
func buildOIDCConfig(oidc *OIDCPolicyConfig) map[string]interface{} {
	if oidc == nil {
		return nil
	}

	config := map[string]interface{}{
		"provider": map[string]interface{}{
			"issuer": oidc.Issuer,
		},
		"clientID": oidc.ClientID,
		"clientSecret": map[string]interface{}{
			"name":      oidc.ClientSecretName,
			"namespace": oidc.ClientSecretNS,
		},
		"redirectURL": oidc.RedirectURL,
		"logoutPath":  oidc.LogoutPath,
	}

	if len(oidc.Scopes) > 0 {
		config["scopes"] = stringSliceToInterfaceSlice(oidc.Scopes)
	}

	if oidc.CookieDomain != "" {
		config["cookieDomain"] = oidc.CookieDomain
	}

	return config
}

// buildExtAuthConfig builds the extAuth configuration for SecurityPolicy
// Uses direct K8s Service reference as per Envoy Gateway documentation
func buildExtAuthConfig(extAuth *models.ExtAuthConfig) map[string]interface{} {
	if extAuth == nil {
		return nil
	}

	extAuthConfig := map[string]interface{}{}

	// Build HTTP or gRPC service config with direct Service reference
	if extAuth.Type == "http" && extAuth.HTTP != nil {
		backendRef := map[string]interface{}{
			"name": extAuth.HTTP.BackendRef.Name,
			"port": extAuth.HTTP.BackendRef.Port,
		}
		// Add namespace for cross-namespace references
		if extAuth.HTTP.BackendRef.Namespace != "" {
			backendRef["namespace"] = extAuth.HTTP.BackendRef.Namespace
		}
		httpConfig := map[string]interface{}{
			"backendRefs": []interface{}{backendRef},
			"path":        extAuth.HTTP.Path,
		}
		if len(extAuth.HTTP.HeadersToBackend) > 0 {
			httpConfig["headersToBackend"] = stringSliceToInterfaceSlice(extAuth.HTTP.HeadersToBackend)
		}
		extAuthConfig["http"] = httpConfig
	} else if extAuth.Type == "grpc" && extAuth.GRPC != nil {
		backendRef := map[string]interface{}{
			"name": extAuth.GRPC.BackendRef.Name,
			"port": extAuth.GRPC.BackendRef.Port,
		}
		// Add namespace for cross-namespace references
		if extAuth.GRPC.BackendRef.Namespace != "" {
			backendRef["namespace"] = extAuth.GRPC.BackendRef.Namespace
		}
		grpcConfig := map[string]interface{}{
			"backendRefs": []interface{}{backendRef},
		}
		extAuthConfig["grpc"] = grpcConfig
	}

	// Common options
	if extAuth.FailOpen != nil {
		extAuthConfig["failOpen"] = *extAuth.FailOpen
	}
	if len(extAuth.HeadersToExtAuth) > 0 {
		extAuthConfig["headersToExtAuth"] = stringSliceToInterfaceSlice(extAuth.HeadersToExtAuth)
	}
	// Note: headersToDownstreamOnDeny, headersToDownstreamOnAllow, headersToUpstreamOnAllow
	// are stored in the DB model but do NOT exist in the EG SecurityPolicy CRD.
	// Only headersToBackend (inside http config) is supported for response header forwarding.

	// Map withRequestBody to EG's bodyToExtAuth with maxRequestBytes
	if extAuth.WithRequestBody != nil {
		extAuthConfig["bodyToExtAuth"] = map[string]interface{}{
			"maxRequestBytes": extAuth.WithRequestBody.MaxBytes,
		}
	}

	return extAuthConfig
}
