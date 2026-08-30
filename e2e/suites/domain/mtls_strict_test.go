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

// setupMTLSStrict ports the Python source's function-scoped autouse
// fixture domain_mtls_strict (tests/domain_settings/test_mtls_strict.py):
// pytest re-runs an autouse fixture for EVERY test function that uses it,
// so each of the 3 Go tests below performs its own full enable-CA-cleanup
// cycle, exactly mirroring that per-function isolation. It enables strict
// mTLS (optional=false) on the shared domain with root-ca-3 as the only
// trusted CA and registers cleanup.
func setupMTLSStrict(t *testing.T, ctx context.Context) {
	t.Helper()
	cleanupDomainSettings(t, false)

	caPEM := loadPEM(t, "root-ca-3", "root-ca.crt")
	if _, err := updateDomainSettings(ctx, env.ProjectID, env.DomainID, services.UpdateDomainSettingsInput{
		MTLS: &models.DomainMTLSConfig{Enabled: true, Optional: false},
	}); err != nil {
		t.Fatalf("mtls strict: enable strict domain mTLS: %v", err)
	}
	if _, err := addDomainMTLSCA(ctx, env.ProjectID, env.DomainID, "Root CA 3", caPEM); err != nil {
		t.Fatalf("mtls strict: add domain mTLS CA: %v", err)
	}
	t.Cleanup(func() { cleanupDomainSettings(t, true) })
}

// mtlsStrictRoute creates a fixture route to nginx-service (rewritten to
// "/", so its unique test path resolves to nginx's real 200 page) and
// returns the gateway path to probe it at.
func mtlsStrictRoute(t *testing.T) (path string) {
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

// TestMTLSStrictAcceptsValidCert ports
// test_mtls_strict.py:test_mtls_strict_accepts_valid_cert, replacing the
// tautological "assert resp.status_code in (200, 404)" with a genuine
// 200: waitForHTTPStatus polls until the request -- carrying a
// certificate signed by the one CA configured as trusted -- is actually
// accepted, which can only happen once the route, the domain, and the
// strict-mTLS listener have all converged.
func TestMTLSStrictAcceptsValidCert(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+2*time.Minute)
	defer cancel()

	setupMTLSStrict(t, ctx)
	validCertPEM := loadPEM(t, "root-ca-3", "client-1.crt")
	validKeyPEM := loadPEM(t, "root-ca-3", "client-1.key")
	path := mtlsStrictRoute(t)

	probe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path, harness.WithClientCert(validCertPEM, validKeyPEM))
	}
	if _, err := waitForHTTPStatus(ctx, probe, routeLiveTimeout, 200); err != nil {
		t.Fatalf("mtls strict: valid CA3 client cert: %v", err)
	}
}

// TestMTLSStrictRejectsNoCert ports
// test_mtls_strict.py:test_mtls_strict_rejects_no_cert, already a real
// assertion in the Python source (pytest.raises). The positive probe
// (valid cert -> 200) runs FIRST to prove the route and the strict-mTLS
// listener have converged -- without that, a TLS failure here could mean
// either "correctly rejected" or "domain settings not live yet",
// exactly the ambiguity e2e/suites/security's package doc comment
// describes. Only once that is established is the genuine negative probe
// (no certificate at all) trusted: requireTLSFailure demands a non-nil
// transport error, so a bare 200 (strict mTLS silently not enforced)
// fails the test.
func TestMTLSStrictRejectsNoCert(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+2*time.Minute)
	defer cancel()

	setupMTLSStrict(t, ctx)
	validCertPEM := loadPEM(t, "root-ca-3", "client-1.crt")
	validKeyPEM := loadPEM(t, "root-ca-3", "client-1.key")
	path := mtlsStrictRoute(t)

	validProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path, harness.WithClientCert(validCertPEM, validKeyPEM))
	}
	if _, err := waitForHTTPStatus(ctx, validProbe, routeLiveTimeout, 200); err != nil {
		t.Fatalf("mtls strict: precondition (valid cert must succeed) failed: %v", err)
	}

	noCertProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path)
	}
	requireTLSFailure(t, ctx, noCertProbe)
}

// TestMTLSStrictRejectsWrongCert ports
// test_mtls_strict.py:test_mtls_strict_rejects_wrong_cert, already a real
// assertion in the Python source (pytest.raises). Same "positive first"
// structure as TestMTLSStrictRejectsNoCert; the negative probe here
// presents a certificate signed by root-ca-4, which is never registered as
// a trusted CA on this domain, so the handshake itself must fail.
func TestMTLSStrictRejectsWrongCert(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+2*time.Minute)
	defer cancel()

	setupMTLSStrict(t, ctx)
	validCertPEM := loadPEM(t, "root-ca-3", "client-1.crt")
	validKeyPEM := loadPEM(t, "root-ca-3", "client-1.key")
	wrongCertPEM := loadPEM(t, "root-ca-4", "client-1.crt")
	wrongKeyPEM := loadPEM(t, "root-ca-4", "client-1.key")
	path := mtlsStrictRoute(t)

	validProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path, harness.WithClientCert(validCertPEM, validKeyPEM))
	}
	if _, err := waitForHTTPStatus(ctx, validProbe, routeLiveTimeout, 200); err != nil {
		t.Fatalf("mtls strict: precondition (valid cert must succeed) failed: %v", err)
	}

	wrongCertProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path, harness.WithClientCert(wrongCertPEM, wrongKeyPEM))
	}
	requireTLSFailure(t, ctx, wrongCertProbe)
}
