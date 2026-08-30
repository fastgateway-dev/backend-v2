//go:build e2e

package security

import (
	"context"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestClientModeMTLS ports client_mode/test_mtls.py, fixing the tautology
// task-13-brief names explicitly, and strengthens the negative case with
// a genuine transport-layer assertion the Python source never attempted.
//
// # Why domain mTLS stays "optional", never "strict"
//
// Domain mTLS is Gateway-listener-scoped (Envoy Gateway's
// ClientTrafficPolicy), not per-route -- it applies to EVERY route on the
// shared "api.fastgateway.local" domain, including every other test in
// this package (running in parallel, per t.Parallel()) and every other
// suite that targets the same domain (httproute, grpcroute -- though
// task-13's own instructions note CI runs those packages serially, so
// only same-package parallelism is a live concern here). Setting it to
// STRICT (optional=false) would make the TLS handshake itself fail for
// every sibling request that doesn't present a client certificate --
// i.e. nearly all of them. This test therefore uses exactly the same
// "optional=true" mode the Python source used: unauthenticated TLS
// handshakes still succeed, so concurrent traffic from every other test
// in this package is unaffected. (Genuinely STRICT domain mTLS is
// exercised by regression/tests/domain_settings/test_mtls_strict.py,
// which is out of scope for task-13 and is not touched by this port.)
//
// # Why the negative case has two parts
//
// task-13-brief's table lists this test's negative case as "no cert ->
// TLS handshake failure". Under "optional" domain mTLS, presenting NO
// certificate at all does NOT fail the handshake -- that is the entire
// point of "optional": the connection succeeds, and enforcement of
// "this specific client requires an mTLS cert" happens one layer up, as
// an HTTP 403 from the client attachment's SecurityPolicy (matching
// exactly what the Python source already asserted, and what
// regression/tests/domain_settings/test_mtls_strict.py's own comments
// confirm is the strict-vs-optional distinction in this codebase).
// A genuine TLS-handshake-level rejection under "optional" mode requires
// presenting a certificate that IS provided but fails verification --
// e.g. one signed by a CA the domain does not trust. This port therefore
// asserts BOTH:
//
//   - no certificate + x-client-id -> HTTP 403 (app-layer denial; the
//     Python source's original design, transcribed as-is)
//   - a certificate signed by an untrusted CA (root-ca-4, verified below
//     to NOT chain to root-ca-3, the CA actually registered on the
//     domain) -> a real TLS handshake failure (non-nil transport error),
//     never a 200 -- the transport-layer assertion task-13's own
//     instructions require, using the harness's existing WithClientCert
//     and the pre-existing root-ca-4 fixture (mirroring
//     regression/tests/domain_settings/test_mtls_strict.py's identical
//     "wrong CA" negative case, which is a proven pattern in this
//     codebase, just previously only exercised under strict mode).
//
// Both are genuine, independently meaningful negative assertions; neither
// supersedes the other.
func TestClientModeMTLS(t *testing.T) {
	t.Parallel()

	// Pre-cleanup: remove any mTLS config left behind by a previous
	// crashed run, mirroring the Python source's unconditional
	// cleanup_domain_mtls(...) call at the top of the test.
	cleanupDomainMTLS(t, false)

	caPEM := loadPEM(t, "root-ca-3", "root-ca.crt")
	validCertPEM := loadPEM(t, "root-ca-3", "client-1.crt")
	validKeyPEM := loadPEM(t, "root-ca-3", "client-1.key")
	untrustedCertPEM := loadPEM(t, "root-ca-4", "client-1.crt")
	untrustedKeyPEM := loadPEM(t, "root-ca-4", "client-1.key")

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+2*time.Minute)
	defer cancel()

	if _, err := updateDomainSettings(ctx, env.ProjectID, env.DomainID, services.UpdateDomainSettingsInput{
		MTLS: &models.DomainMTLSConfig{Enabled: true, Optional: true},
	}); err != nil {
		t.Fatalf("client mode mtls: enable optional domain mTLS: %v", err)
	}
	if _, err := addDomainMTLSCA(ctx, env.ProjectID, env.DomainID, "Root CA 3", caPEM); err != nil {
		t.Fatalf("client mode mtls: add domain mTLS CA: %v", err)
	}
	t.Cleanup(func() { cleanupDomainMTLS(t, true) })

	name, path := uniquePath(t)
	cfg := services.CreateRouteInput{
		Name:         name,
		SecurityMode: models.SecurityModeClient,
		TeamID:       teamID(t),
		Config: models.RouteConfig{
			RouteType:            models.RouteTypeBackend,
			DefaultTrafficPolicy: models.DefaultTrafficPolicyDeny,
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
	route := fx.Route(cfg)

	client, err := createClient(ctx, harness.UniqueName(t), teamID(t))
	if err != nil {
		t.Fatalf("client mode mtls: create client: %v", err)
	}
	cleanupClient(t, client.ID.String())

	// configureClientMTLS uses env.Editor, not env.Admin -- see its own
	// doc comment for why (a real team-membership check with no
	// owner-role bypass).
	if err := configureClientMTLS(ctx, client.ID.String(), services.UpdateClientMTLSInput{
		Enabled: true,
		CAName:  "Root CA 3",
		CAPem:   caPEM,
		SANs:    []models.MTLSSANEntry{{Type: "DNS", Value: "client-1.fastgateway.local"}},
	}); err != nil {
		t.Fatalf("client mode mtls: configure client mTLS: %v", err)
	}

	if _, err := attachAndDeploy(ctx, route.ID.String(), services.AttachFromRouteInput{
		ClientID:   client.ID,
		EnableMTLS: true,
	}); err != nil {
		t.Fatalf("client mode mtls: attach client: %v", err)
	}

	// Positive first: proves the route, the client attachment, AND the
	// domain-level mTLS listener config have all converged before either
	// negative probe is trusted (see the package doc comment).
	allowProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path,
			harness.WithHeader("x-client-id", client.ID.String()),
			harness.WithClientCert(validCertPEM, validKeyPEM),
		)
	}
	if _, err := waitForHTTPStatus(ctx, allowProbe, routeLiveTimeout, 200); err != nil {
		t.Fatalf("client mode mtls: with valid client cert + client id: %v", err)
	}

	// Negative (app layer): no certificate at all, but a real client-id
	// -- denied by the client attachment's SecurityPolicy, not by TLS
	// (domain mTLS is optional; the handshake itself succeeds).
	noCertProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path, harness.WithHeader("x-client-id", client.ID.String()))
	}
	requireStatus(t, ctx, noCertProbe, 403)

	// Negative (transport layer): a certificate IS presented, but signed
	// by an untrusted CA (root-ca-4, not root-ca-3) -- the handshake
	// itself must fail. A nil error (200 included) fails the test; see
	// requireTLSFailure.
	wrongCACertProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path,
			harness.WithHeader("x-client-id", client.ID.String()),
			harness.WithClientCert(untrustedCertPEM, untrustedKeyPEM),
		)
	}
	requireTLSFailure(t, ctx, wrongCACertProbe)
}
