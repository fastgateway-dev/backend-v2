package services

import (
	"path/filepath"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

// fixtureSecurityPolicy is a persisted SecurityPolicy row for the *FromDB paths.
func fixtureSecurityPolicy() *models.SecurityPolicy {
	return &models.SecurityPolicy{
		ID:        uuid.MustParse("44444444-4444-4444-4444-444444444444"),
		RouteID:   uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		ProjectID: uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		Config: models.SecurityPolicyConfig{
			CORS: &models.CORSConfig{
				AllowOrigins: []string{"https://app.example.com"},
				AllowMethods: []string{"GET", "POST"},
				AllowHeaders: []string{"content-type"},
			},
		},
	}
}

func fixtureBackendTrafficPolicy() *models.BackendTrafficPolicy {
	// NOTE: RouteID is *uuid.UUID on this model, not uuid.UUID.
	// Timeout is *BTPTimeoutConfig (NOT models.TimeoutConfig, which is a
	// domain-settings type with an entirely different shape).
	routeID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	return &models.BackendTrafficPolicy{
		ID:        uuid.MustParse("55555555-5555-5555-5555-555555555555"),
		RouteID:   &routeID,
		ProjectID: uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		Config: models.BackendTrafficPolicyConfig{
			Timeout: &models.BTPTimeoutConfig{
				HTTP: &models.BTPHTTPTimeoutConfig{RequestTimeout: "5s"},
			},
		},
	}
}

func TestGoldenSecurityPolicyFromDB(t *testing.T) {
	route, domain := fixtureRoute("secpol"), fixtureDomain()
	cfg := routeplan.SecurityPolicyConfigFromDB(route, domain, fixtureSecurityPolicy())
	assertGolden(t, filepath.Join("securitypolicy-fromdb", "cors"), cfg)
}

// --- SecurityPolicy: broaden coverage beyond CORS (Authorization, APIKeyAuth,
// JWT, ExtAuth), and a genuine cross-path differential ---
//
// Of the four kubernetes.SecurityPolicyConfig construction sites named in review:
//   - inline inside deploySecurityPolicy (route_deploy.go:290): NOT reachable
//     as a pure function. It reads s.securityPolicyRepo (DB), and calls
//     s.buildClientIPAuthorizationConfig/s.countClientAttachments/
//     s.hasAPIKeyClientAttachments/etc, all of which need a live repository.
//     Confirmed by reading route_deploy.go:290-316. No fixture added for it.
//   - routeplan.SecurityPolicyConfigFromDB (internal/routeplan/securitypolicy.go:151): pure, reachable,
//     covered below across 5 families (cors already existed; +4 here).
//   - routeplan.GenerateSecurityPolicyYAML (internal/routeplan/securitypolicy.go:166): pure, reachable,
//     takes a *routeplan.SecurityPolicyInput instead of a persisted *models.SecurityPolicy
//     -- an independently written implementation of the same field mapping.
//     This is the genuine second path compared below.
//   - buildAPIKeySecurityPolicyConfig (internal/services/route_clients_apikey.go:503): pure (its
//     one k8sService call, GetAPIKeySecretName, does not dereference the
//     receiver and is safe on a zero-value RouteService{}). Covered below with
//     its own golden; it targets a different manifest (a per-client route) so
//     there is no independently-implemented counterpart to diff it against.

func fixtureSecurityPolicyAuthorization() *models.SecurityPolicy {
	return &models.SecurityPolicy{
		ID:        uuid.MustParse("44444444-4444-4444-4444-444444444445"),
		RouteID:   uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		ProjectID: uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		Config: models.SecurityPolicyConfig{
			Authorization: &models.AuthorizationConfig{
				DefaultAction: "Deny",
				Rules: []models.AuthorizationRule{
					{
						Action: "Allow",
						Principal: models.AuthorizationPrincipal{
							ClientCIDRs: []string{"10.0.0.0/24"},
							Headers:     []models.AuthorizationHeaderMatch{{Name: "x-tenant", Values: []string{"acme"}}},
						},
						Operation: &models.AuthorizationOperation{Methods: []string{"GET", "POST"}},
					},
				},
			},
		},
	}
}

func fixtureSecurityPolicyAPIKeyAuth() *models.SecurityPolicy {
	return &models.SecurityPolicy{
		ID:        uuid.MustParse("44444444-4444-4444-4444-444444444446"),
		RouteID:   uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		ProjectID: uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		Config: models.SecurityPolicyConfig{
			APIKeyAuth: &models.APIKeyAuthConfig{
				CredentialRefs: []models.SecretRef{{Name: "route-api-key", Namespace: kubernetes.FastGatewayNamespace}},
				ExtractFrom:    []models.APIKeyExtractFrom{{Headers: []string{"x-api-key"}}},
			},
		},
	}
}

func fixtureSecurityPolicyJWT() *models.SecurityPolicy {
	return &models.SecurityPolicy{
		ID:        uuid.MustParse("44444444-4444-4444-4444-444444444447"),
		RouteID:   uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		ProjectID: uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		Config: models.SecurityPolicyConfig{
			JWT: &models.JWTConfig{
				Providers: []models.JWTProvider{
					{
						Name:           "route-jwt",
						Issuer:         "https://issuer.example.com",
						Audiences:      []string{"api"},
						RemoteJWKS:     &models.RemoteJWKS{URI: "https://issuer.example.com/jwks"},
						ClaimToHeaders: []models.SPJWTClaimToHeader{{Claim: "sub", Header: "x-user-id"}},
					},
				},
			},
		},
	}
}

func fixtureSecurityPolicyExtAuth() *models.SecurityPolicy {
	return &models.SecurityPolicy{
		ID:        uuid.MustParse("44444444-4444-4444-4444-444444444448"),
		RouteID:   uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		ProjectID: uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		Config: models.SecurityPolicyConfig{
			ExtAuth: &models.ExtAuthConfig{
				Type: "http",
				HTTP: &models.ExtAuthHTTPConfig{
					BackendRef: models.ExtAuthBackendRef{Name: "authz-svc", Namespace: "default", Port: 9191},
					Path:       "/authorize",
				},
			},
		},
	}
}

// fixtureSecurityPolicyInputFor mirrors each fixtureSecurityPolicy* family as
// a *routeplan.SecurityPolicyInput -- the shape routeplan.GenerateSecurityPolicyYAML consumes.
// Field values are chosen to be the semantic equivalent of the DB fixture of
// the same name so the differential test below is a meaningful comparison,
// not just two empty configs agreeing on nothing.
func fixtureSecurityPolicyInputFor(name string) *routeplan.SecurityPolicyInput {
	switch name {
	case "cors":
		return &routeplan.SecurityPolicyInput{
			CORS: &models.CORSConfig{
				AllowOrigins: []string{"https://app.example.com"},
				AllowMethods: []string{"GET", "POST"},
				AllowHeaders: []string{"content-type"},
			},
		}
	case "authorization":
		return &routeplan.SecurityPolicyInput{
			Authorization: &routeplan.AuthorizationInput{
				AllowedCIDRs: []string{"10.0.0.0/24"},
				Headers:      []models.AuthorizationHeaderMatch{{Name: "x-tenant", Values: []string{"acme"}}},
				Methods:      []string{"GET", "POST"},
			},
		}
	case "apikeyauth":
		return &routeplan.SecurityPolicyInput{
			APIKeyAuth: &routeplan.APIKeyAuthInput{
				SecretName: "route-api-key",
				HeaderName: "x-api-key",
			},
		}
	case "jwt":
		return &routeplan.SecurityPolicyInput{
			JWT: &routeplan.JWTInput{
				Issuer:         "https://issuer.example.com",
				JWKSURL:        "https://issuer.example.com/jwks",
				Audiences:      []string{"api"},
				ClaimToHeaders: []models.SPJWTClaimToHeader{{Claim: "sub", Header: "x-user-id"}},
			},
		}
	case "extauth":
		return &routeplan.SecurityPolicyInput{
			ExtAuth: &models.ExtAuthConfig{
				Type: "http",
				HTTP: &models.ExtAuthHTTPConfig{
					BackendRef: models.ExtAuthBackendRef{Name: "authz-svc", Namespace: "default", Port: 9191},
					Path:       "/authorize",
				},
			},
		}
	default:
		return nil
	}
}

func securityPolicyFamilyFixtures() []struct {
	Name   string
	Policy *models.SecurityPolicy
} {
	return []struct {
		Name   string
		Policy *models.SecurityPolicy
	}{
		{"cors", fixtureSecurityPolicy()},
		{"authorization", fixtureSecurityPolicyAuthorization()},
		{"apikeyauth", fixtureSecurityPolicyAPIKeyAuth()},
		{"jwt", fixtureSecurityPolicyJWT()},
		{"extauth", fixtureSecurityPolicyExtAuth()},
	}
}

func TestGoldenSecurityPolicyFromDBFamilies(t *testing.T) {
	route, domain := fixtureRoute("secpol"), fixtureDomain()
	for _, f := range securityPolicyFamilyFixtures() {
		if f.Name == "cors" {
			continue // already covered by TestGoldenSecurityPolicyFromDB
		}
		t.Run(f.Name, func(t *testing.T) {
			cfg := routeplan.SecurityPolicyConfigFromDB(route, domain, f.Policy)
			assertGolden(t, filepath.Join("securitypolicy-fromdb", f.Name), cfg)
		})
	}
}

// securityPolicyFromDBYAML renders the fromDB path to YAML using the same
// BuildSecurityPolicy + marshal steps routeplan.GenerateSecurityPolicyYAMLFromDB uses
// (that function itself just wraps routeplan.SecurityPolicyConfigFromDB, so calling it
// directly here would be circular). routeplan.GenerateSecurityPolicyYAMLFromDB does
// nothing beyond routeplan.SecurityPolicyConfigFromDB + BuildSecurityPolicy + marshal --
// see the note on routeplan.GenerateSecurityPolicyYAMLFromDB itself
// (internal/routeplan/securitypolicy.go) explaining that ExtAuthBackendName
// is deliberately left unset on every route-level path, so this helper sets
// nothing extra and just reproduces those three steps.
func securityPolicyFromDBYAML(t *testing.T, route *models.Route, domain *models.Domain, policy *models.SecurityPolicy) string {
	t.Helper()
	config := routeplan.SecurityPolicyConfigFromDB(route, domain, policy)
	if config == nil {
		return ""
	}
	obj := kubernetes.BuildSecurityPolicy(config)
	if obj == nil {
		return ""
	}
	b, err := yaml.Marshal(obj.Object)
	require.NoError(t, err)
	return string(b)
}

func securityPolicyInputYAML(t *testing.T, route *models.Route, domain *models.Domain, input *routeplan.SecurityPolicyInput) string {
	t.Helper()
	return routeplan.GenerateSecurityPolicyYAML(route, domain, input, nil)
}

// TestDifferentialSecurityPolicy compares the persisted-policy path
// (routeplan.SecurityPolicyConfigFromDB, used by both deployGeneralSecurityPolicy and
// routeplan.GenerateSecurityPolicyYAMLFromDB) against the pre-persistence input-based
// path (routeplan.GenerateSecurityPolicyYAML, used for approval/preview diffs before a
// SecurityPolicy row exists). Both gather their fixture data differently --
// one from a persisted *models.SecurityPolicy row, the other from a
// submitted routeplan.SecurityPolicyInput -- but funnel it through the single shared
// AssembleSecurityPolicyConfig assembler (internal/routeplan/securitypolicy.go).
// The field mapping itself is no longer duplicated, so this test's remaining
// job is narrower than its name suggests: it guards against the two gather
// paths feeding the shared assembler inconsistent data for equivalent
// fixtures, not against a second hand-written mapping drifting from the
// first.
func TestDifferentialSecurityPolicy(t *testing.T) {
	route, domain := fixtureRoute("secpol"), fixtureDomain()
	for _, f := range securityPolicyFamilyFixtures() {
		t.Run(f.Name, func(t *testing.T) {
			fromDBYAML := securityPolicyFromDBYAML(t, route, domain, f.Policy)
			inputYAML := securityPolicyInputYAML(t, route, domain, fixtureSecurityPolicyInputFor(f.Name))

			require.NotEmpty(t, fromDBYAML, "fixture %q: fromDB path produced empty YAML", f.Name)
			require.NotEmpty(t, inputYAML, "fixture %q: input path produced empty YAML", f.Name)
			require.Equalf(t, fromDBYAML, inputYAML,
				"fromDB and input-based SecurityPolicy assembly disagree for %q", f.Name)
		})
	}
}

// --- buildAPIKeySecurityPolicyConfig: the per-client SecurityPolicy site.
// No independently-implemented counterpart exists to diff it against (it
// targets a distinct per-client manifest name), so it gets golden regression
// coverage only.
//
// Its EnableAPIKey branch is NOT reachable as a pure function: it calls
// s.k8sAPIKeys.GetAPIKeySecretName(...), and k8sAPIKeys is declared as the
// APIKeySecretApplier *interface* type (route_service.go), not a concrete
// *cluster.Client. On a zero-value RouteService{} that field is a nil
// interface, and calling a method through a nil interface panics -- unlike a
// nil concrete pointer receiver, there is no method table to dispatch
// through. Confirmed by running it: panics in route_clients_apikey.go with
// "invalid memory address or nil pointer dereference". A fixture exercising
// EnableAPIKey would need a stub implementing APIKeySecretApplier, which is
// out of scope here. (Before Phase 2E Task 7 the stub would have had to
// implement all 58 methods of KubernetesServiceInterface.)
//
// The JWT branch below does not touch s.k8sAPIKeys at all (verified by
// reading route_clients_apikey.go:520-530: no further use of the receiver once
// past the EnableAPIKey block), so it is reachable and gives real coverage
// of the JWT-with-required-claims + IP-combination logic in this site.
func fixtureJWTClientAuthCategory() routeplan.ClientAuthCategory {
	return routeplan.ClientAuthCategory{
		ClientID:     uuid.MustParse("88888888-8888-8888-8888-888888888888"),
		ClientName:   "acme-client",
		EnableJWT:    true,
		JWTIssuer:    "https://issuer.example.com",
		JWTJWKSURL:   "https://issuer.example.com/jwks",
		JWTAudiences: []string{"api"},
		JWTRequiredClaims: []models.JWTRequiredClaim{
			{Name: "role", Values: []string{"admin"}, ValueType: "Exact"},
		},
		IPCIDRs: []string{"10.0.0.0/24"},
	}
}

func TestGoldenSecurityPolicyAPIKeyClient(t *testing.T) {
	svc := &RouteService{}
	route, domain := fixtureRoute("secpol"), fixtureDomain()
	cfg := svc.buildAPIKeySecurityPolicyConfig(route, domain, fixtureJWTClientAuthCategory(), true, nil)
	assertGolden(t, filepath.Join("securitypolicy-apikey-client", "jwt-required-claims"), cfg)
}

func TestGoldenBackendTrafficPolicyDeploy(t *testing.T) {
	route, domain := fixtureRoute("btp"), fixtureDomain()
	cfg := routeplan.BuildBackendTrafficPolicyConfig(route, domain, fixtureBackendTrafficPolicy())
	assertGolden(t, filepath.Join("backendtrafficpolicy-deploy", "timeout"), cfg)
}

// deployBackendTrafficPolicyYAML renders the deploy-path BackendTrafficPolicy
// object to YAML, using the same BuildBackendTrafficPolicy + marshal steps
// generateBackendTrafficPolicyYAMLFromDB uses at its own end.
// generateBackendTrafficPolicyYAMLFromDB is a thin wrapper that calls
// buildBackendTrafficPolicyConfig (the same function this helper calls) and
// then does exactly the Build+marshal steps below, so
// TestDifferentialBackendTrafficPolicy(Families) below is now a tautology by
// construction rather than an independent cross-check of two separately
// written implementations. Before the collapse, an equivalence harness
// verified that the old, independently-written deploy and preview bodies
// produced identical output across the full fixture set; that check is what
// justified deleting one of the two bodies rather than keeping both.
func deployBackendTrafficPolicyYAML(t *testing.T, route *models.Route, domain *models.Domain, policy *models.BackendTrafficPolicy) string {
	t.Helper()
	config := routeplan.BuildBackendTrafficPolicyConfig(route, domain, policy)
	if config == nil {
		return ""
	}
	obj := kubernetes.BuildBackendTrafficPolicy(config)
	if obj == nil {
		return ""
	}
	b, err := yaml.Marshal(obj.Object)
	require.NoError(t, err)
	return string(b)
}

// TestDifferentialBackendTrafficPolicy compares the deploy assembler against
// the YAML-fromDB assembler for the same persisted policy, at the rendered
// YAML level -- both now share the single buildBackendTrafficPolicyConfig
// assembler (see deployBackendTrafficPolicyYAML above), so this is a
// re-divergence guard rather than a live comparison, same as
// TestDifferentialHTTPRoute in golden_httproute_test.go.
func TestDifferentialBackendTrafficPolicy(t *testing.T) {
	route, domain, policy := fixtureRoute("btp"), fixtureDomain(), fixtureBackendTrafficPolicy()

	deployYAML := deployBackendTrafficPolicyYAML(t, route, domain, policy)
	previewYAML := routeplan.GenerateBackendTrafficPolicyYAMLFromDB(route, domain, policy)

	require.NotEmpty(t, previewYAML, "preview YAML must not be empty for a policy with a timeout")
	assertGolden(t, filepath.Join("backendtrafficpolicy-preview", "timeout"), previewYAML)
	require.Equalf(t, deployYAML, previewYAML,
		"deploy and preview BackendTrafficPolicy assembly disagree for %q", "timeout")
}

// --- additional BackendTrafficPolicy feature families: health-check,
// circuit-breaker, rate-limit, request-buffer, compression,
// response-override, load-balancer, fault-injection, retry ---
//
// fixtureBTPWithConfig shares the route/project/policy IDs of
// fixtureBackendTrafficPolicy so golden output stays stable; only Config
// varies per family below.
func fixtureBTPWithConfig(cfg models.BackendTrafficPolicyConfig) *models.BackendTrafficPolicy {
	routeID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	return &models.BackendTrafficPolicy{
		ID:        uuid.MustParse("55555555-5555-5555-5555-555555555555"),
		RouteID:   &routeID,
		ProjectID: uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		Config:    cfg,
	}
}

func fixtureBTPHealthCheck() *models.BackendTrafficPolicy {
	return fixtureBTPWithConfig(models.BackendTrafficPolicyConfig{
		HealthCheck: &models.HealthCheckConfig{
			Active: &models.ActiveHealthCheckConfig{
				Timeout:            ptrString("3s"),
				Interval:           ptrString("10s"),
				UnhealthyThreshold: ptrUint32(3),
				HealthyThreshold:   ptrUint32(2),
				Type:               "HTTP",
				HTTP: &models.HTTPActiveHealthCheckConfig{
					Path:             "/healthz",
					ExpectedStatuses: []int{200},
				},
			},
		},
	})
}

func fixtureBTPCircuitBreaker() *models.BackendTrafficPolicy {
	return fixtureBTPWithConfig(models.BackendTrafficPolicyConfig{
		CircuitBreaker: &models.CircuitBreakerConfig{
			MaxConnections:      ptrInt64(1024),
			MaxPendingRequests:  ptrInt64(512),
			MaxParallelRequests: ptrInt64(1024),
		},
	})
}

func fixtureBTPRateLimit() *models.BackendTrafficPolicy {
	return fixtureBTPWithConfig(models.BackendTrafficPolicyConfig{
		RateLimit: &models.RateLimitConfig{
			Global: &models.GlobalRateLimitConfig{
				Rules: []models.RateLimitRule{
					{Limit: models.RateLimitValue{Requests: 100, Unit: "Minute"}},
				},
			},
		},
	})
}

func fixtureBTPRequestBuffer() *models.BackendTrafficPolicy {
	return fixtureBTPWithConfig(models.BackendTrafficPolicyConfig{
		RequestBuffer: &models.RequestBufferConfig{Limit: "4Ki"},
	})
}

func fixtureBTPCompression() *models.BackendTrafficPolicy {
	return fixtureBTPWithConfig(models.BackendTrafficPolicyConfig{
		Compression: []models.CompressionConfig{
			{Type: models.CompressionType("Gzip"), Gzip: &models.GzipConfig{}},
		},
	})
}

func fixtureBTPLoadBalancer() *models.BackendTrafficPolicy {
	return fixtureBTPWithConfig(models.BackendTrafficPolicyConfig{
		LoadBalancer: &models.LoadBalancerConfig{
			Type: models.LoadBalancerTypeConsistentHash,
			ConsistentHash: &models.ConsistentHashConfig{
				Type:   models.ConsistentHashTypeHeader,
				Header: &models.ConsistentHashHeader{Name: "X-User-ID"},
			},
		},
	})
}

func fixtureBTPFaultInjection() *models.BackendTrafficPolicy {
	return fixtureBTPWithConfig(models.BackendTrafficPolicyConfig{
		FaultInjection: &models.FaultInjectionConfig{
			Delay: &models.FaultInjectionDelayConfig{
				FixedDelay: "1s",
				Percentage: ptrFloat32(10),
			},
			Abort: &models.FaultInjectionAbortConfig{
				HTTPStatus: ptrInt(503),
				Percentage: ptrFloat32(5),
			},
		},
	})
}

func fixtureBTPRetry() *models.BackendTrafficPolicy {
	return fixtureBTPWithConfig(models.BackendTrafficPolicyConfig{
		Retry: &models.RetryConfig{
			NumRetries: ptrInt32(3),
			RetryOn:    &models.RetryOn{Triggers: []string{"5xx"}, HTTPStatusCodes: []int{502, 503}},
		},
	})
}

func fixtureBTPResponseOverride() *models.BackendTrafficPolicy {
	return fixtureBTPWithConfig(models.BackendTrafficPolicyConfig{
		ResponseOverride: []models.ResponseOverrideRule{
			{
				Match: models.ResponseOverrideMatch{
					StatusCodes: []models.StatusCodeMatch{{Type: "Value", Value: ptrInt(404)}},
				},
				Response: models.ResponseOverrideResponse{
					ContentType: "application/json",
					Body: models.ResponseOverrideBody{
						Type:   "Inline",
						Inline: `{"error":"not found"}`,
					},
				},
			},
		},
	})
}

// btpFamilyFixtures names the extra BackendTrafficPolicy feature families
// covered beyond the minimal case (timeout). The deploy and fromDB
// assemblers that handle all of these fields are now the single shared
// buildBackendTrafficPolicyConfigFromInput (internal/routeplan/backendtrafficpolicy.go).
func btpFamilyFixtures() []struct {
	Name   string
	Policy *models.BackendTrafficPolicy
} {
	return []struct {
		Name   string
		Policy *models.BackendTrafficPolicy
	}{
		{"health-check", fixtureBTPHealthCheck()},
		{"circuit-breaker", fixtureBTPCircuitBreaker()},
		{"rate-limit", fixtureBTPRateLimit()},
		{"request-buffer", fixtureBTPRequestBuffer()},
		{"compression", fixtureBTPCompression()},
		{"response-override", fixtureBTPResponseOverride()},
		{"load-balancer", fixtureBTPLoadBalancer()},
		{"fault-injection", fixtureBTPFaultInjection()},
		{"retry", fixtureBTPRetry()},
	}
}

func TestGoldenBackendTrafficPolicyFamiliesDeploy(t *testing.T) {
	route, domain := fixtureRoute("btp"), fixtureDomain()
	for _, f := range btpFamilyFixtures() {
		t.Run(f.Name, func(t *testing.T) {
			cfg := routeplan.BuildBackendTrafficPolicyConfig(route, domain, f.Policy)
			assertGolden(t, filepath.Join("backendtrafficpolicy-deploy", f.Name), cfg)
		})
	}
}

func TestDifferentialBackendTrafficPolicyFamilies(t *testing.T) {
	route, domain := fixtureRoute("btp"), fixtureDomain()
	for _, f := range btpFamilyFixtures() {
		t.Run(f.Name, func(t *testing.T) {
			deployYAML := deployBackendTrafficPolicyYAML(t, route, domain, f.Policy)
			previewYAML := routeplan.GenerateBackendTrafficPolicyYAMLFromDB(route, domain, f.Policy)

			require.NotEmpty(t, previewYAML, "preview YAML must not be empty for fixture %q", f.Name)
			assertGolden(t, filepath.Join("backendtrafficpolicy-preview", f.Name), previewYAML)
			require.Equalf(t, deployYAML, previewYAML,
				"deploy and preview BackendTrafficPolicy assembly disagree for %q", f.Name)
		})
	}
}

// TestGoldenBackendTrafficPolicyInputFamilies closes a coverage gap:
// routeplan.GenerateBackendTrafficPolicyYAML (the pre-persist,
// routeplan.BackendTrafficPolicyInput-based path -- F2) previously had no golden
// coverage at all, only assert.Contains substring checks in
// route_service_yaml_internal_test.go, and RequestBuffer was untested even
// there. This exercises F2 directly (via the same
// routeplan.MapBackendTrafficPolicyConfigToInput adapter the real approval/preview
// flow uses to turn a persisted policy into a routeplan.BackendTrafficPolicyInput) and
// checks it against the same golden files backendtrafficpolicy-preview (F3)
// already asserts -- F1, F2 and F3 must all agree on identical input.
func TestGoldenBackendTrafficPolicyInputFamilies(t *testing.T) {
	route, domain := fixtureRoute("btp"), fixtureDomain()
	all := append([]struct {
		Name   string
		Policy *models.BackendTrafficPolicy
	}{{"timeout", fixtureBackendTrafficPolicy()}}, btpFamilyFixtures()...)

	for _, f := range all {
		t.Run(f.Name, func(t *testing.T) {
			input := routeplan.MapBackendTrafficPolicyConfigToInput(&f.Policy.Config)
			inputYAML := routeplan.GenerateBackendTrafficPolicyYAML(route, domain, input)
			previewYAML := routeplan.GenerateBackendTrafficPolicyYAMLFromDB(route, domain, f.Policy)

			require.NotEmpty(t, inputYAML, "input-path YAML must not be empty for fixture %q", f.Name)
			assertGolden(t, filepath.Join("backendtrafficpolicy-preview", f.Name), inputYAML)
			require.Equalf(t, previewYAML, inputYAML,
				"input-based (F2) and fromDB (F3) BackendTrafficPolicy assembly disagree for %q", f.Name)
		})
	}
}

// --- EnvoyExtensionPolicy families: WAF, Lua, Wasm, ext-proc ---
//
// buildEnvoyExtensionPolicyConfig (route_service.go) is the deploy-path
// assembler; generateEnvoyExtensionPolicyYAMLFromSnapshot is an independent
// duplicate implementation of the same logic used by the preview/approval
// path. They are two of the sites a future task should collapse.

func fixtureEnvoyExtensionPolicyLua() *models.EnvoyExtensionPolicy {
	return &models.EnvoyExtensionPolicy{
		ID:        uuid.MustParse("66666666-6666-6666-6666-666666666666"),
		ProjectID: uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		Config: models.EnvoyExtensionPolicyConfig{
			Lua: &models.LuaExtensionConfig{
				Type:   "Inline",
				Inline: `function envoy_on_request(request_handle) end`,
			},
		},
	}
}

func fixtureEnvoyExtensionPolicyWasm() *models.EnvoyExtensionPolicy {
	return &models.EnvoyExtensionPolicy{
		ID:        uuid.MustParse("66666666-6666-6666-6666-666666666667"),
		ProjectID: uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		Config: models.EnvoyExtensionPolicyConfig{
			Wasm: &models.WasmExtensionConfig{
				Name: "my-wasm-filter",
				Code: models.WasmCodeSource{
					Type: "HTTP",
					HTTP: &models.WasmHTTPSource{
						URL:    "https://example.com/filter.wasm",
						SHA256: "deadbeef",
					},
				},
			},
		},
	}
}

func fixtureEnvoyExtensionPolicyExtProc() *models.EnvoyExtensionPolicy {
	return &models.EnvoyExtensionPolicy{
		ID:        uuid.MustParse("66666666-6666-6666-6666-666666666668"),
		ProjectID: uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		Config: models.EnvoyExtensionPolicyConfig{
			ExtProc: &models.ExtProcExtensionConfig{
				BackendRef: models.ExtProcBackendRef{Name: "ext-proc-svc", Namespace: "default", Port: 9002},
			},
		},
	}
}

func fixtureWafPolicy() *models.WafPolicy {
	return &models.WafPolicy{
		ID:        uuid.MustParse("77777777-7777-7777-7777-777777777777"),
		RouteID:   uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		ProjectID: uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		Config: models.WafPolicyConfig{
			Mode:     "block",
			Rulesets: []string{"owasp-crs"},
		},
	}
}

func envoyExtensionPolicyFamilyFixtures() []struct {
	Name      string
	Policy    *models.EnvoyExtensionPolicy
	WafPolicy *models.WafPolicy
} {
	return []struct {
		Name      string
		Policy    *models.EnvoyExtensionPolicy
		WafPolicy *models.WafPolicy
	}{
		{"lua", fixtureEnvoyExtensionPolicyLua(), nil},
		{"wasm", fixtureEnvoyExtensionPolicyWasm(), nil},
		{"ext-proc", fixtureEnvoyExtensionPolicyExtProc(), nil},
		{"waf", nil, fixtureWafPolicy()},
	}
}

// deployEnvoyExtensionPolicyYAML renders the deploy-path EnvoyExtensionPolicy
// object to YAML, matching the marshalling generateEnvoyExtensionPolicyYAMLFromDBWithWaf
// and generateEnvoyExtensionPolicyYAMLFromSnapshot use.
func deployEnvoyExtensionPolicyYAML(t *testing.T, route *models.Route, domain *models.Domain, policy *models.EnvoyExtensionPolicy, wafPolicy *models.WafPolicy) string {
	t.Helper()
	svc := &RouteService{}
	config := svc.buildEnvoyExtensionPolicyConfig(route, domain, policy, wafPolicy)
	if config == nil {
		return ""
	}
	obj := kubernetes.BuildEnvoyExtensionPolicy(config)
	if obj == nil {
		return ""
	}
	b, err := yaml.Marshal(obj.Object)
	require.NoError(t, err)
	return string(b)
}

func TestGoldenEnvoyExtensionPolicyDeploy(t *testing.T) {
	route, domain := fixtureRoute("eep"), fixtureDomain()
	for _, f := range envoyExtensionPolicyFamilyFixtures() {
		t.Run(f.Name, func(t *testing.T) {
			got := deployEnvoyExtensionPolicyYAML(t, route, domain, f.Policy, f.WafPolicy)
			require.NotEmpty(t, got, "fixture %q must produce non-empty YAML", f.Name)
			assertGolden(t, filepath.Join("envoyextensionpolicy-deploy", f.Name), got)
		})
	}
}

// TestDifferentialEnvoyExtensionPolicy compares the deploy assembler against
// generateEnvoyExtensionPolicyYAMLFromSnapshot, an independently maintained
// duplicate of the same logic used on the approval/preview path.
func TestDifferentialEnvoyExtensionPolicy(t *testing.T) {
	route, domain := fixtureRoute("eep"), fixtureDomain()
	for _, f := range envoyExtensionPolicyFamilyFixtures() {
		t.Run(f.Name, func(t *testing.T) {
			deployYAML := deployEnvoyExtensionPolicyYAML(t, route, domain, f.Policy, f.WafPolicy)
			snapshotYAML := routeplan.GenerateEnvoyExtensionPolicyYAMLFromSnapshot(route, domain, f.Policy, f.WafPolicy, routeplan.WAFConfig{})

			require.Equalf(t, deployYAML, snapshotYAML,
				"deploy and snapshot EnvoyExtensionPolicy assembly disagree for %q", f.Name)
		})
	}
}
