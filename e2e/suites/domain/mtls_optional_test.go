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

// TestMTLSOptionalRejectsWrongCert ports
// test_mtls_optional.py:test_mtls_optional_rejects_wrong_cert, already a
// real assertion in the Python source (pytest.raises). Under "optional"
// mode a MISSING certificate is allowed, but a PRESENTED certificate
// signed by an untrusted CA (root-ca-4, never registered on this domain)
// must still fail the handshake -- this is what actually distinguishes
// "optional" from "mTLS disabled". The positive probe establishes
// liveness first, exactly as in mtls_strict_test.go.
func TestMTLSOptionalRejectsWrongCert(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+2*time.Minute)
	defer cancel()

	setupMTLSOptional(t, ctx)
	validCertPEM := loadPEM(t, "root-ca-3", "client-1.crt")
	validKeyPEM := loadPEM(t, "root-ca-3", "client-1.key")
	wrongCertPEM := loadPEM(t, "root-ca-4", "client-1.crt")
	wrongKeyPEM := loadPEM(t, "root-ca-4", "client-1.key")
	path := mtlsOptionalRoute(t)

	validProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path, harness.WithClientCert(validCertPEM, validKeyPEM))
	}
	if _, err := waitForHTTPStatus(ctx, validProbe, routeLiveTimeout, 200); err != nil {
		t.Fatalf("mtls optional: precondition (valid cert must succeed) failed: %v", err)
	}

	wrongCertProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path, harness.WithClientCert(wrongCertPEM, wrongKeyPEM))
	}
	requireTLSFailure(t, ctx, wrongCertProbe)
}
