//go:build e2e

package domain

import (
	"context"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// TestMTLSEnabledWithNoCARejectsConnections covers the gap Phase 2G left.
//
// Phase 2G made an mTLS-enabled domain with no CA certificates render
// ClientValidation with an EMPTY caCertificateRefs list, so the Gateway
// fails closed instead of silently serving unauthenticated traffic (see
// internal/domainplan/clienttrafficpolicy.go's ClientValidation block,
// deliberately not gated on len(caSecretRefs) > 0). Before 2G that
// configuration rendered byte-identically to a domain with no security
// settings at all. NOTHING has ever tested what Envoy Gateway actually
// does with that manifest -- every other mTLS suite in this package
// (mtls_strict_test.go, mtls_optional_test.go, mtls_multiple_ca_test.go)
// supplies real CAs -- so the operator warning shipped alongside that
// change, which promises the domain "will reject client connections", is
// an unverified claim about Envoy Gateway's actual runtime behavior.
//
// Convergence is established by TRANSITION, not by a positive probe: with
// zero CAs no certificate can possibly be trusted, so the usual "valid
// cert -> 200 first" trick every other test in this package uses (see
// mtls_strict_test.go's TestMTLSStrictRejectsNoCert) is unavailable here --
// there is no certificate this test could present that the resulting
// ClientTrafficPolicy would ever accept. Instead, the route is proven live
// over plain HTTPS FIRST (mTLS still off), establishing that the route
// itself -- as opposed to the domain-level mTLS listener -- has converged.
// THEN mTLS is enabled with no CAs, and the same probe is polled until it
// stops succeeding. A bare TLS failure alone would be ambiguous ("rejected"
// vs. "not live yet"); a 200-then-failure transition is not.
//
// This test may legitimately fail. It is asking a question nobody has
// answered before: if Envoy Gateway rejects the ClientTrafficPolicy as
// invalid (rather than programming a listener that rejects connections),
// the fail-closed guarantee does not hold at the Envoy layer and the
// operator warning is inaccurate. That is a finding for whoever reads this
// suite's CI output next, not a bug in this test -- see
// logClientTrafficPolicyStatus below, which records the policy's own
// Accepted condition precisely so that distinction is visible regardless
// of which way the probe goes, and on both the EG 1.7.5 and 1.8.4 versions
// CI runs this suite against.
//
// UPDATE (post-CI hotfix): this has now run against Envoy Gateway 1.8.4,
// and the answer is a third outcome nobody above predicted. EG 1.8.4 does
// not reject the ClientTrafficPolicy (status.conditions comes back empty --
// see logClientTrafficPolicyStatus -- so the Accepted-condition question
// above is simply inert on this version) and it does not fail open either.
// It accepts the policy, programs the listener, completes the TLS
// handshake, and returns HTTP 500 for the life of the poll window. The
// fail-closed security property this test exists to protect still holds --
// unauthenticated traffic never reaches the backend -- but the *mechanism*
// both this suite (via waitForTLSFailure, which counted only a transport
// error as failure and so treated the 500 as a passing "successful
// response") and the operator warning in
// internal/services/domain_settings.go (which promised a rejected
// connection) got wrong. Both have been corrected: the poll helper below is
// renamed waitForDomainBlocked and now also treats any non-2xx response as
// blocked, and the warning text now describes an HTTP 500 rather than a
// rejected handshake.
func TestMTLSEnabledWithNoCARejectsConnections(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+3*time.Minute)
	defer cancel()

	cleanupDomainSettings(t, false)
	t.Cleanup(func() { cleanupDomainSettings(t, true) })

	name, path := uniquePath(t)
	fx := harness.NewFixture(t, env)
	fx.Route(services.CreateRouteInput{
		Name:   name,
		TeamID: teamID(t),
		Config: models.RouteConfig{
			RouteType: models.RouteTypeBackend,
			Matches:   []models.RouteMatch{{Path: &models.PathMatch{Type: "Prefix", Value: path}}},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: nginxService, Port: nginxPort, Weight: 100},
			},
			URLRewrite: rewriteTo("/"),
		},
	})

	probe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path)
	}

	// 1. Route is live with mTLS OFF. Establishes convergence of the route
	// itself, independent of the domain-level mTLS listener.
	if _, err := waitForHTTPStatus(ctx, probe, routeLiveTimeout, 200); err != nil {
		t.Fatalf("mtls no-ca: route did not become live before enabling mTLS: %v", err)
	}

	// 2. Enable mTLS with NO CA certificates and no mTLS clients attached.
	if _, err := updateDomainSettings(ctx, env.ProjectID, env.DomainID, services.UpdateDomainSettingsInput{
		MTLS: &models.DomainMTLSConfig{Enabled: true, Optional: false},
	}); err != nil {
		t.Fatalf("mtls no-ca: enable mTLS with no CAs: %v", err)
	}
	changeTime := time.Now()

	// Record what Envoy Gateway actually did with the zero-CA manifest,
	// regardless of how the probe below turns out -- see this test's doc
	// comment. Registered via t.Cleanup, rather than called inline after
	// the poll, so it still runs even if waitForDomainBlocked below calls
	// t.Fatalf: that unwinds this goroutine via runtime.Goexit before any
	// later statement in this function would execute, but registered
	// cleanups still run, and LIFO ordering puts this one before the
	// package-standard cleanupDomainSettings(true) cleanup registered
	// above -- so the policy is inspected before that cleanup resets it.
	t.Cleanup(func() {
		logCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		logClientTrafficPolicyStatus(t, logCtx)
	})

	// 3. Poll until the domain stops returning 2xx.
	waitForDomainBlocked(t, ctx, probe, changeTime, routeLiveTimeout)
}

// consecutiveTLSFailuresRequired is how many IN-A-ROW blocked probes
// waitForDomainBlocked demands before it will declare the domain
// fail-closed. Poll interval is 2s, so 3 gives ~6s of sustained blocking.
// "Blocked" means either a transport-level error or a non-2xx HTTP
// response -- see waitForDomainBlocked's doc comment for why both count.
//
// This exists to guard against a single transient blip -- a pod restart
// mid-rollout, a connection reset, a brief control-plane hiccup -- being
// mistaken for the domain genuinely blocking connections. Unlike
// requireTLSFailure's single probe (safe only because its one round trip
// is tightly coupled to a positive probe against the identical
// configuration moments earlier), waitForDomainBlocked polls across a long,
// noisy window (up to routeLiveTimeout, ~180s): accepting the first blocked
// probe anywhere in that window would let one unrelated blip satisfy a test
// that exists specifically to verify the operator warning shipped in
// 826340b (originally "will reject client connections"; corrected by this
// hotfix to describe the HTTP 500 CI actually observed on Envoy Gateway
// 1.8.4 -- see internal/services/domain_settings.go's mtlsNoCAWarning). A
// test that can pass for the wrong reason here would ratify an unverified
// claim, which is worse than having no test at all -- so this constant is
// not superstition, it is the difference between "converged and failing
// closed" and "got unlucky once." Do not lower it back to 1.
const consecutiveTLSFailuresRequired = 3

// mtlsReconcileSettleWindow is the minimum time waitForDomainBlocked waits
// from the moment mTLS is enabled (changeTime) before it will let ANY
// observed blocked probe count toward the consecutiveTLSFailuresRequired
// streak.
//
// Enabling mTLS on a domain triggers an Envoy Gateway listener reconcile,
// and mid-reconcile the listener can be briefly torn down and rebuilt --
// producing connection errors or transient error responses that have
// nothing to do with certificate validation. consecutiveTLSFailuresRequired
// alone does not rule this out: at a 2s poll interval, 3 consecutive
// blocked probes is only ~6s of sustained blocking, and a reconcile window
// can plausibly exceed 6s. Without this guard the test could observe
// exactly 3 consecutive blocked probes during the reconcile transition
// itself, declare the domain fail-closed, and never actually observe the
// converged steady state.
//
// This guards a DIFFERENT hole than consecutiveTLSFailuresRequired: that
// constant rules out a single momentary blip anywhere in the poll window;
// this one rules out the specific, structurally-predictable window of
// churn immediately after the settings change, during which no observed
// blocked probe -- however many in a row -- can be trusted as meaningful.
// 20s is comfortably longer than a plausible listener reconcile, while
// leaving most of the 180s routeLiveTimeout (~160s) for a genuine blocked
// streak to accumulate once the listener has settled.
const mtlsReconcileSettleWindow = 20 * time.Second

// waitForDomainBlocked polls probe (2s-interval loop, mirroring
// waitForHTTPStatus's shape) until it reports the domain BLOCKED
// consecutiveTLSFailuresRequired times IN A ROW -- counting only probes
// observed after mtlsReconcileSettleWindow has elapsed since changeTime --
// or fails t once timeout elapses.
//
// "Blocked" means either a non-nil transport-level error (a genuine TLS
// handshake rejection, which is what the name waitForTLSFailure originally
// assumed was the only possible mechanism) OR a non-2xx HTTP response. Both
// count because CI has now observed, on Envoy Gateway 1.8.4, that an
// mTLS-enabled domain with zero CA certificates does neither of the two
// things the pre-hotfix version of this helper anticipated: it does not
// reject the TLS handshake, and it does not serve traffic normally. It
// completes the handshake and returns HTTP 500 for the full poll window.
// Under the old "err != nil only" definition, that 500 was indistinguishable
// from a normal 200 -- the helper counted it as a "successful response" and
// the test failed with a message claiming the domain "did not fail closed,"
// even though unauthenticated traffic never reached the backend. A non-2xx
// response IS the fail-closed behavior on this version, so it must satisfy
// this check; a transport-level error must ALSO still satisfy it, since a
// genuine handshake rejection is equally valid fail-closed behavior and may
// be what other Envoy Gateway versions do.
//
// This is the polling counterpart to requireTLSFailure: that helper issues
// a single probe and is safe to use only once convergence is already
// established by a prior positive probe against the SAME configuration.
// Here there is no such prior probe against the post-change configuration
// -- the mTLS listener takes time to reconcile after updateDomainSettings
// returns -- so a one-shot check would be racy: an early, still-succeeding
// probe would look identical to a genuine fail-open, AND a single
// transient blocked probe (unrelated to mTLS at all) would look identical
// to a genuine fail-closed. The consecutive-run requirement rules out the
// latter: the streak resets to zero the moment a probe succeeds (2xx)
// again, so an alternating success/blocked pattern -- itself a real signal
// that something other than a converged fail-closed listener is going on
// -- never satisfies this check and the poll keeps going instead of
// declaring victory early. The settle-window requirement above rules out a
// third case neither of those covers: a burst of reconcile-induced blocked
// probes that happens to be sustained enough, and happens to fall entirely
// within the transition, to satisfy the consecutive-run count on its own.
//
// It fails the test if probe is STILL returning 2xx when the deadline
// arrives: a domain that keeps serving 2xx with mTLS enabled and no CA
// certificates configured is exactly the fail-open this test exists to
// catch, so that outcome must never be treated as "not converged yet, keep
// waiting" and silently retried away.
func waitForDomainBlocked(t *testing.T, ctx context.Context, probe func(context.Context) (*harness.Response, error), changeTime time.Time, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	settleDeadline := changeTime.Add(mtlsReconcileSettleWindow)
	sawSuccess := false
	lastStatus := 0
	consecutiveFailures := 0

	for time.Now().Before(deadline) {
		resp, err := probe(ctx)
		blocked := err != nil || resp.StatusCode < 200 || resp.StatusCode >= 300

		if blocked {
			if time.Now().Before(settleDeadline) {
				t.Logf("mtls no-ca: blocked probe within %s post-change settle window, not counting toward streak (possible reconcile transition): err=%v", mtlsReconcileSettleWindow, err)
			} else {
				consecutiveFailures++
				if err != nil {
					t.Logf("mtls no-ca: blocked probe %d/%d consecutive (transport-layer error): %v", consecutiveFailures, consecutiveTLSFailuresRequired, err)
				} else {
					t.Logf("mtls no-ca: blocked probe %d/%d consecutive (non-2xx status %d)", consecutiveFailures, consecutiveTLSFailuresRequired, resp.StatusCode)
				}
				if consecutiveFailures >= consecutiveTLSFailuresRequired {
					t.Logf("mtls no-ca: got %d consecutive blocked probes, treating the domain as failed closed", consecutiveFailures)
					return
				}
			}
		} else {
			if consecutiveFailures > 0 {
				t.Logf("mtls no-ca: probe succeeded again (status %d) after %d consecutive blocked probe(s) -- resetting streak, this was not a converged fail-closed state", resp.StatusCode, consecutiveFailures)
			}
			consecutiveFailures = 0
			sawSuccess = true
			lastStatus = resp.StatusCode
		}

		select {
		case <-ctx.Done():
			t.Fatalf("mtls no-ca: waiting for the domain to fail closed: %v", ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}

	if sawSuccess {
		t.Fatalf("mtls no-ca: still serving successful (2xx) responses (last status %d) after %s -- an mTLS-enabled domain with zero CA certificates did not fail closed", lastStatus, timeout)
	}
	t.Fatalf("mtls no-ca: probe never completed within %s", timeout)
}

// logClientTrafficPolicyStatus fetches the domain's ClientTrafficPolicy --
// named "<K8sGatewayName>-ctp" in the domain's own namespace, per
// internal/domainplan/clienttrafficpolicy.go's BuildClientTrafficPolicyConfig
// and internal/kubernetes/clienttrafficpolicy.go's BuildClientTrafficPolicy
// -- and logs its "Accepted" status condition (type, status, reason,
// message).
//
// Whether Envoy Gateway ACCEPTS this policy with an empty
// caCertificateRefs list, or rejects it as invalid, would have been useful
// evidence for the mechanism behind whatever waitForDomainBlocked above
// observes -- an Accepted=True condition alongside a blocked probe would
// confirm a fail-closed listener, whereas Accepted=False would mean the
// manifest was rejected outright. In practice, on Envoy Gateway 1.8.4 (the
// version CI runs this suite against as of this hotfix), status.conditions
// on this ClientTrafficPolicy comes back empty every time -- see the CI run
// that motivated this hotfix -- so this logging is currently inert on that
// version: it never has an "Accepted" entry to report. It is kept anyway
// because it is harmless and may report something useful on other Envoy
// Gateway versions (it may also start reporting on a future 1.8.x patch).
// It is diagnostic only: this function never fails the test on its own,
// and waitForDomainBlocked's pass/fail verdict does not depend on anything
// this function observes -- a missing policy or unreadable status is
// itself useful information for whoever reads CI output next, not a
// harness bug to panic over. No Kubernetes client dependency is added for
// this: it reuses harness.Kube's existing dynamic client and
// internal/kubernetes' existing GVR constant.
func logClientTrafficPolicyStatus(t *testing.T, ctx context.Context) {
	t.Helper()

	domain, err := env.Admin.GetDomainByName(ctx, env.ProjectID, env.Cfg.DomainName)
	if err != nil {
		t.Logf("mtls no-ca: could not resolve domain %q to look up its ClientTrafficPolicy: %v", env.Cfg.DomainName, err)
		return
	}
	ctpName := domain.K8sGatewayName + "-ctp"

	obj, err := env.Kube.GetUnstructured(ctx, kubernetes.ClientTrafficPolicyGVR, domain.Namespace, ctpName)
	if err != nil {
		t.Logf("mtls no-ca: could not fetch ClientTrafficPolicy %s/%s: %v", domain.Namespace, ctpName, err)
		return
	}

	conditions, found, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if err != nil || !found {
		t.Logf("mtls no-ca: ClientTrafficPolicy %s/%s has no status.conditions yet (found=%v, err=%v)", domain.Namespace, ctpName, found, err)
		return
	}

	for _, c := range conditions {
		cond, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		if condType, _ := cond["type"].(string); condType != "Accepted" {
			continue
		}
		status, _ := cond["status"].(string)
		reason, _ := cond["reason"].(string)
		message, _ := cond["message"].(string)
		t.Logf("mtls no-ca: ClientTrafficPolicy %s/%s Accepted condition: status=%s reason=%s message=%q",
			domain.Namespace, ctpName, status, reason, message)
		return
	}
	t.Logf("mtls no-ca: ClientTrafficPolicy %s/%s has status.conditions but no \"Accepted\" entry: %+v",
		domain.Namespace, ctpName, conditions)
}
