//go:build e2e

package domain

import (
	"context"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// setupMTLSOptional ports the Python source's function-scoped autouse
// fixture domain_mtls_optional (tests/domain_settings/test_mtls_optional.py),
// re-run per test function just like setupMTLSStrict. It enables OPTIONAL
// mTLS (optional=true) on the shared domain with root-ca-3 as the only
// trusted CA and registers cleanup.
func setupMTLSOptional(t *testing.T, ctx context.Context) {
	t.Helper()
	cleanupDomainSettings(t, false)

	caPEM := loadPEM(t, "root-ca-3", "root-ca.crt")
	if _, err := updateDomainSettings(ctx, env.ProjectID, env.DomainID, services.UpdateDomainSettingsInput{
		MTLS: &models.DomainMTLSConfig{Enabled: true, Optional: true},
	}); err != nil {
		t.Fatalf("mtls optional: enable optional domain mTLS: %v", err)
	}
	if _, err := addDomainMTLSCA(ctx, env.ProjectID, env.DomainID, "Root CA 3", caPEM); err != nil {
		t.Fatalf("mtls optional: add domain mTLS CA: %v", err)
	}
	t.Cleanup(func() { cleanupDomainSettings(t, true) })
}

// mtlsOptionalRoute creates a fixture route to nginx-service (rewritten to
// "/") and returns the gateway path to probe it at. Mirrors
// mtlsStrictRoute in mtls_strict_test.go.
func mtlsOptionalRoute(t *testing.T) (path string) {
	t.Helper()
	name, path := uniquePath(t)
	cfg := services.CreateRouteInput{
		Name:   name,
		TeamID: teamID(t),
		Config: models.RouteConfig{
			RouteType: models.RouteTypeBackend,
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: path}},
			},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: nginxService, Port: nginxPort, Weight: 100},
			},
			URLRewrite: rewriteTo("/"),
		},
	}
	fx := harness.NewFixture(t, env)
	fx.Route(cfg)
	return path
}

// TestMTLSOptionalAcceptsValidCert ports
// test_mtls_optional.py:test_mtls_optional_accepts_valid_cert, replacing
// the tautological "assert resp.status_code in (200, 404)" with a genuine
// 200: a request carrying a certificate signed by the registered CA must
// be accepted.
func TestMTLSOptionalAcceptsValidCert(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+2*time.Minute)
	defer cancel()

	setupMTLSOptional(t, ctx)
	validCertPEM := loadPEM(t, "root-ca-3", "client-1.crt")
	validKeyPEM := loadPEM(t, "root-ca-3", "client-1.key")
	path := mtlsOptionalRoute(t)

	probe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path, harness.WithClientCert(validCertPEM, validKeyPEM))
	}
	if _, err := waitForHTTPStatus(ctx, probe, routeLiveTimeout, 200); err != nil {
		t.Fatalf("mtls optional: valid CA3 client cert: %v", err)
	}
}

// TestMTLSOptionalAllowsNoCert ports
// test_mtls_optional.py:test_mtls_optional_allows_no_cert, replacing the
// tautological "assert resp.status_code in (200, 404)" with a genuine
// 200. Per task-14-brief, BOTH halves of "optional" are asserted
// explicitly across this file's two "positive" tests: this one proves a
// request presenting NO certificate at all still succeeds (optional mode's
// entire point), while TestMTLSOptionalAcceptsValidCert proves a request
// WITH a trusted certificate also succeeds. The valid-cert probe runs
// first here purely as a liveness precondition (route + domain settings
// converged); the actual assertion under test is the no-cert probe that
// follows, checked with requireStatus (no retry-past-unexpected-status)
// since liveness is already established.
func TestMTLSOptionalAllowsNoCert(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+2*time.Minute)
	defer cancel()

	setupMTLSOptional(t, ctx)
	validCertPEM := loadPEM(t, "root-ca-3", "client-1.crt")
	validKeyPEM := loadPEM(t, "root-ca-3", "client-1.key")
	path := mtlsOptionalRoute(t)

	validProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path, harness.WithClientCert(validCertPEM, validKeyPEM))
	}
	if _, err := waitForHTTPStatus(ctx, validProbe, routeLiveTimeout, 200); err != nil {
		t.Fatalf("mtls optional: precondition (valid cert must succeed) failed: %v", err)
	}

	noCertProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path)
	}
	requireStatus(t, ctx, noCertProbe, 200)
}

// TestMTLSOptionalAcceptsUntrustedCert ports
// test_mtls_optional.py:test_mtls_optional_rejects_wrong_cert, but under a
// corrected understanding of how optional domain mTLS actually behaves.
//
// The Python source (and this test, prior to this fix) asserted that a
// certificate signed by an untrusted CA (root-ca-4, never registered on
// this domain) fails the handshake even under "optional" mode. It does
// not, and this test's own setup could never have produced that result:
//
//  1. ClientTrafficPolicy's clientValidation.optional maps to Envoy's
//     trust_chain_verification: ACCEPT_UNTRUSTED. Per Envoy's
//     DefaultCertValidator (source/common/tls/cert_validator/
//     default_validator.cc, verifyCertAndUpdateStatus), the verification
//     result is combined as "return (allow_untrusted_certificate_ ||
//     success);" -- once ACCEPT_UNTRUSTED is set, the handshake is
//     accepted NO MATTER WHAT is presented (missing cert, cert from an
//     unregistered CA, cert that fails a configured SAN or hash check).
//     A handshake failure is not just unlikely but impossible under
//     "optional" mode, for any certificate. (Genuinely STRICT domain
//     mTLS, where handshake failure IS possible, is exercised by
//     mtls_strict_test.go.)
//  2. Rejection of an untrusted cert under "optional" mode is only
//     possible one layer up, at routing: buildMTLSXFCCHeaderMatches
//     (internal/services/route_service.go) matches a per-CLIENT
//     attachment's registered SANs/hashes against the XFCC header on a
//     per-client route, falling through to deny-by-default on a
//     mismatch (see e2e/suites/security/client_mode_mtls_test.go for
//     that exact mechanism in action). This test's route has no
//     SecurityMode, no client, no attachment -- nothing that consumes
//     XFCC -- so that mechanism cannot fire here either.
//  3. models.DomainMTLSConfig also exposes a domain-level, general-mode
//     SANWhitelist/HashWhitelist, which -- unlike the client-attachment
//     path above -- translates directly into this same
//     clientValidation.subjectAltNames/certificateHashes block on the
//     ClientTrafficPolicy (internal/domainplan/clienttrafficpolicy.go's
//     BuildClientTrafficPolicyConfig). But per point 1, Envoy's
//     ACCEPT_UNTRUSTED short-circuits SAN/hash verification failures
//     exactly the same as chain-of-trust failures: configuring a
//     SANWhitelist here would not change this test's outcome, because
//     nothing in this codebase reads that whitelist's match result to
//     make a routing decision when trust_chain_verification is
//     ACCEPT_UNTRUSTED. It remains genuinely untested by e2e today, but
//     exercising it would require STRICT mode (where ACCEPT_UNTRUSTED
//     doesn't neuter the check), which is out of scope for this test.
//
// So the correct assertion is the mirror image of the original: an
// untrusted-CA certificate is ACCEPTED, exactly like TestMTLSOptionalAllowsNoCert
// proves a missing certificate is accepted. This closes a real gap the old,
// backwards assertion masked -- it's easy to assume "optional" means
// "no cert OR a cert from a CA we trust", when it actually means "no cert
// OR literally any cert, trusted or not." The positive probe establishes
// liveness first, exactly as in mtls_strict_test.go.
func TestMTLSOptionalAcceptsUntrustedCert(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+2*time.Minute)
	defer cancel()

	setupMTLSOptional(t, ctx)
	validCertPEM := loadPEM(t, "root-ca-3", "client-1.crt")
	validKeyPEM := loadPEM(t, "root-ca-3", "client-1.key")
	untrustedCertPEM := loadPEM(t, "root-ca-4", "client-1.crt")
	untrustedKeyPEM := loadPEM(t, "root-ca-4", "client-1.key")
	path := mtlsOptionalRoute(t)

	validProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path, harness.WithClientCert(validCertPEM, validKeyPEM))
	}
	if _, err := waitForHTTPStatus(ctx, validProbe, routeLiveTimeout, 200); err != nil {
		t.Fatalf("mtls optional: precondition (valid cert must succeed) failed: %v", err)
	}

	untrustedCertProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path, harness.WithClientCert(untrustedCertPEM, untrustedKeyPEM))
	}
	requireStatus(t, ctx, untrustedCertProbe, 200)
}
