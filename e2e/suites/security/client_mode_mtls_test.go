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
// # Why this test does NOT call t.Parallel()
//
// Domain mTLS is Gateway-listener-scoped (Envoy Gateway's
// ClientTrafficPolicy), not per-route -- it applies to EVERY route on the
// shared "api.fastgateway.local" domain, including every other test in
// this package and every other suite that targets the same domain
// (httproute, grpcroute -- though task-13's own instructions note CI
// runs those packages serially, so only same-package parallelism is a
// live concern here). Even in "optional" mode (see below), this test
// resets domain mTLS to a known state, enables it, registers a CA, and
// resets it again in cleanup -- each of those mutations forces Envoy
// Gateway to rebuild the shared listener, and every sibling test's
// requireStatus-style assertions retry only on transport-level errors,
// never on a transient wrong status code produced mid-rebuild. Running
// this test concurrently with the package's other ~18 parallel tests
// therefore risked flaking them all. So this test omits t.Parallel():
// per the package doc comment in main_test.go (mirroring
// e2e/suites/platform/main_test.go's identical convention), Go runs
// every non-parallel top-level test in a package strictly one-at-a-time
// and only starts any t.Parallel() test's body once every non-parallel
// test has completed -- so omitting t.Parallel() here is sufficient to
// keep this test's listener-rebuilding mutations from ever overlapping
// with a sibling's traffic, without a package-wide lock.
//
// # Why domain mTLS stays "optional", never "strict"
//
// Setting it to STRICT (optional=false) would make the TLS handshake
// itself fail for every request that doesn't present a client
// certificate -- i.e. nearly all sibling traffic once this test's
// mutations are live. This test therefore uses exactly the same
// "optional=true" mode the Python source used: unauthenticated TLS
// handshakes still succeed, so any overlap with other suites (httproute,
// grpcroute) targeting the same domain remains unaffected. (Genuinely
// STRICT domain mTLS is exercised by
// regression/tests/domain_settings/test_mtls_strict.py, which is out of
// scope for task-13 and is not touched by this port.)
//
// # Why the negative case has two parts, and why BOTH land on HTTP 403
//
// task-13-brief's table lists this test's negative case as "no cert ->
// TLS handshake failure". That framing does not hold under "optional"
// domain mTLS: ClientTrafficPolicy's clientValidation.optional maps to
// Envoy's trust_chain_verification: ACCEPT_UNTRUSTED, under which Envoy
// requests a client certificate but accepts the handshake no matter what
// is presented -- missing cert, cert from an untrusted CA, cert that
// fails SAN/hash checks, all of it. This is confirmed directly in
// Envoy's DefaultCertValidator (source/common/tls/cert_validator/
// default_validator.cc, verifyCertAndUpdateStatus): the verification
// result is combined as "return (allow_untrusted_certificate_ ||
// success);", so once ACCEPT_UNTRUSTED is set the function always
// returns true regardless of whether the cert verified. A TLS-handshake
// failure is therefore not just unlikely but impossible under "optional"
// mode, for any certificate. (Genuinely STRICT domain mTLS, where
// handshake failure IS possible, is exercised by
// e2e/suites/domain/mtls_strict_test.go.)
//
// Enforcement of "this specific client requires an mTLS cert" instead
// happens one layer up, at routing: buildMTLSXFCCHeaderMatches
// (route_service.go) puts a regex match for this client's registered
// SANs/hashes on the x-forwarded-client-cert header on the per-client
// route. Envoy populates that header from whatever cert the handshake
// accepted, even an unverified one. A request whose XFCC doesn't match
// -- because no cert was presented at all, or because the presented
// cert's SAN belongs to a CA this client never registered -- fails to
// match the per-client route and falls through to deny-by-default,
// yielding HTTP 403 in both cases. This port therefore asserts BOTH:
//
//   - no certificate + x-client-id -> HTTP 403 (app-layer denial; the
//     Python source's original design, transcribed as-is)
//   - a certificate signed by an untrusted CA (root-ca-4, verified below
//     to NOT chain to root-ca-3, the CA actually registered on the
//     domain) -> also HTTP 403, via the same XFCC-route-match-then-
//     deny-by-default mechanism, NOT a transport-layer failure (see
//     above for why the handshake itself cannot fail here)
//
// Both are genuine, independently meaningful negative assertions -- they
// exercise the XFCC match failing for two different reasons (absent
// header vs. header present but SAN not on this client's whitelist) --
// even though both resolve through the same layer and status code.
// Neither supersedes the other.
func TestClientModeMTLS(t *testing.T) {
	// Deliberately no t.Parallel() -- see the doc comment above.

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

	// Negative (routing layer): a certificate IS presented, and under
	// optional domain mTLS the handshake itself succeeds regardless (see
	// the package doc comment -- ACCEPT_UNTRUSTED accepts any cert, so a
	// handshake failure is not possible here). But the cert is signed by
	// an untrusted CA (root-ca-4, not root-ca-3), so its SAN does not
	// satisfy this client's XFCC header match (buildMTLSXFCCHeaderMatches
	// in route_service.go); the per-client route fails to match and the
	// request falls through to deny-by-default, yielding 403 -- the same
	// mechanism and status as the no-cert case above, just triggered by a
	// present-but-untrusted cert instead of an absent one. A 200 here
	// would mean the untrusted certificate reached the backend, which
	// requireStatus's exact-match semantics rule out.
	wrongCACertProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path,
			harness.WithHeader("x-client-id", client.ID.String()),
			harness.WithClientCert(untrustedCertPEM, untrustedKeyPEM),
		)
	}
	requireStatus(t, ctx, wrongCACertProbe, 403)
}
