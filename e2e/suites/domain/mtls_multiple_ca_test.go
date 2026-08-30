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

// DISCREPANCY vs task-14-brief's summary table: the brief lists
// mtls_multiple_ca's assertion as "root-ca-3 cert -> 200; root-ca-4 cert ->
// handshake failure". That contradicts both this test's own Python source
// (tests/domain_settings/test_mtls_multiple_ca.py, whose setup registers
// root-ca-4 as a SECOND trusted CA via add_domain_mtls_ca(...,"Root CA
// 4",...) and whose own test name is
// test_mtls_multiple_ca_accepts_ca4_cert) AND previously-verified,
// real-cluster E2E evidence recorded in
// e2e/E2E_TEST-v1.6.2-v1.4.1.md ("Domain Mutual TLS - Multiple CA":
// "With CA4 client cert: 404 from nginx (routed successfully, second CA
// trusted) - PASS"). This port follows the Python source, the test name,
// and that documented real-cluster behavior -- CA3 and CA4 both succeed,
// only a bare request with no certificate at all is rejected (strict mode)
// -- and treats the brief's table entry as a copy/paste slip (most likely
// confused with test_mtls_optional_rejects_wrong_cert's genuinely-different
// "CA4 is NOT registered, so it must fail" scenario in
// mtls_optional_test.go). Flagged explicitly in task-14-15-report.md.

// setupMTLSMultipleCA ports the Python source's function-scoped autouse
// fixture domain_mtls_multiple_ca
// (tests/domain_settings/test_mtls_multiple_ca.py), re-run per test
// function like setupMTLSStrict/setupMTLSOptional. It enables STRICT mTLS
// (optional=false) on the shared domain with BOTH root-ca-3 and root-ca-4
// registered as trusted CAs, and registers cleanup.
func setupMTLSMultipleCA(t *testing.T, ctx context.Context) {
	t.Helper()
	cleanupDomainSettings(t, false)

	ca3PEM := loadPEM(t, "root-ca-3", "root-ca.crt")
	ca4PEM := loadPEM(t, "root-ca-4", "root-ca.crt")
	if _, err := updateDomainSettings(ctx, env.ProjectID, env.DomainID, services.UpdateDomainSettingsInput{
		MTLS: &models.DomainMTLSConfig{Enabled: true, Optional: false},
	}); err != nil {
		t.Fatalf("mtls multiple ca: enable strict domain mTLS: %v", err)
	}
	if _, err := addDomainMTLSCA(ctx, env.ProjectID, env.DomainID, "Root CA 3", ca3PEM); err != nil {
		t.Fatalf("mtls multiple ca: add root-ca-3: %v", err)
	}
	if _, err := addDomainMTLSCA(ctx, env.ProjectID, env.DomainID, "Root CA 4", ca4PEM); err != nil {
		t.Fatalf("mtls multiple ca: add root-ca-4: %v", err)
	}
	t.Cleanup(func() { cleanupDomainSettings(t, true) })
}

// mtlsMultipleCARoute creates a fixture route to nginx-service (rewritten
// to "/") and returns the gateway path to probe it at. Mirrors
// mtlsStrictRoute in mtls_strict_test.go.
func mtlsMultipleCARoute(t *testing.T) (path string) {
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

// TestMTLSMultipleCAAcceptsCA3Cert ports
// test_mtls_multiple_ca.py:test_mtls_multiple_ca_accepts_ca3_cert,
// replacing the tautological "assert resp.status_code in (200, 404)" with
// a genuine 200.
func TestMTLSMultipleCAAcceptsCA3Cert(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+2*time.Minute)
	defer cancel()

	setupMTLSMultipleCA(t, ctx)
	ca3CertPEM := loadPEM(t, "root-ca-3", "client-1.crt")
	ca3KeyPEM := loadPEM(t, "root-ca-3", "client-1.key")
	path := mtlsMultipleCARoute(t)

	probe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path, harness.WithClientCert(ca3CertPEM, ca3KeyPEM))
	}
	if _, err := waitForHTTPStatus(ctx, probe, routeLiveTimeout, 200); err != nil {
		t.Fatalf("mtls multiple ca: CA3 client cert: %v", err)
	}
}

// TestMTLSMultipleCAAcceptsCA4Cert ports
// test_mtls_multiple_ca.py:test_mtls_multiple_ca_accepts_ca4_cert,
// replacing the tautological "assert resp.status_code in (200, 404)" with
// a genuine 200 -- see this file's top-of-file DISCREPANCY comment for why
// this asserts success (not failure) for root-ca-4 here.
func TestMTLSMultipleCAAcceptsCA4Cert(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+2*time.Minute)
	defer cancel()

	setupMTLSMultipleCA(t, ctx)
	ca4CertPEM := loadPEM(t, "root-ca-4", "client-1.crt")
	ca4KeyPEM := loadPEM(t, "root-ca-4", "client-1.key")
	path := mtlsMultipleCARoute(t)

	probe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path, harness.WithClientCert(ca4CertPEM, ca4KeyPEM))
	}
	if _, err := waitForHTTPStatus(ctx, probe, routeLiveTimeout, 200); err != nil {
		t.Fatalf("mtls multiple ca: CA4 client cert: %v", err)
	}
}

// TestMTLSMultipleCARejectsNoCert ports
// test_mtls_multiple_ca.py:test_mtls_multiple_ca_rejects_no_cert, already
// a real assertion in the Python source (pytest.raises). Strict mode
// (optional=false) rejects any handshake with no certificate at all,
// regardless of how many CAs are trusted. The positive probe (CA3 cert)
// establishes liveness first, exactly as in mtls_strict_test.go.
func TestMTLSMultipleCARejectsNoCert(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+2*time.Minute)
	defer cancel()

	setupMTLSMultipleCA(t, ctx)
	ca3CertPEM := loadPEM(t, "root-ca-3", "client-1.crt")
	ca3KeyPEM := loadPEM(t, "root-ca-3", "client-1.key")
	path := mtlsMultipleCARoute(t)

	validProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path, harness.WithClientCert(ca3CertPEM, ca3KeyPEM))
	}
	if _, err := waitForHTTPStatus(ctx, validProbe, routeLiveTimeout, 200); err != nil {
		t.Fatalf("mtls multiple ca: precondition (CA3 cert must succeed) failed: %v", err)
	}

	noCertProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path)
	}
	requireTLSFailure(t, ctx, noCertProbe)
}
