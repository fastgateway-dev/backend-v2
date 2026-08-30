//go:build e2e

package harness

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// Env bundles everything a test package needs to talk to a seeded
// FastGateway deployment: authenticated API clients for each role, a
// data-plane client, a Kubernetes client, and the seeded project/domain/
// team IDs (created by cmd/e2e-seed, the Go replacement for bootstrap.py).
// Build it once per package -- typically from TestMain -- and share it
// across parallel tests.
type Env struct {
	Cfg      *Config
	Admin    *API
	Editor   *API
	Approver *API
	GW       *Gateway
	Kube     *Kube

	ProjectID string
	DomainID  string
	TeamID    string
}

// NewEnv builds an Env from the ambient environment (see Config.FromEnv):
// it logs in as the admin, editor, and approver users, resolves the seeded
// project/domain/"dev" team by name, and builds the gateway and Kubernetes
// clients. Intended to be called once from a package's TestMain, e.g.:
//
//	var env *harness.Env
//
//	func TestMain(m *testing.M) {
//		var err error
//		env, err = harness.NewEnv(context.Background())
//		if err != nil {
//			log.Fatal(err)
//		}
//		os.Exit(m.Run())
//	}
func NewEnv(ctx context.Context) (*Env, error) {
	cfg, err := FromEnv()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	admin, err := Login(ctx, cfg, cfg.AdminUser, cfg.AdminPass)
	if err != nil {
		return nil, fmt.Errorf("login as admin (%s): %w", cfg.AdminUser, err)
	}
	editor, err := Login(ctx, cfg, cfg.EditorUser, cfg.EditorPass)
	if err != nil {
		return nil, fmt.Errorf("login as editor (%s): %w", cfg.EditorUser, err)
	}
	approver, err := Login(ctx, cfg, cfg.ApproverUser, cfg.ApproverPass)
	if err != nil {
		return nil, fmt.Errorf("login as approver (%s): %w", cfg.ApproverUser, err)
	}

	project, err := admin.GetProjectByName(ctx, cfg.ProjectName)
	if err != nil {
		return nil, fmt.Errorf("resolve project %q: %w", cfg.ProjectName, err)
	}
	domain, err := admin.GetDomainByName(ctx, project.ID.String(), cfg.DomainName)
	if err != nil {
		return nil, fmt.Errorf("resolve domain %q: %w", cfg.DomainName, err)
	}
	team, err := admin.GetTeamByName(ctx, project.ID.String(), "dev")
	if err != nil {
		return nil, fmt.Errorf("resolve team \"dev\": %w", err)
	}

	kube, err := NewKube(cfg)
	if err != nil {
		return nil, fmt.Errorf("build kube client: %w", err)
	}

	return &Env{
		Cfg:       cfg,
		Admin:     admin,
		Editor:    editor,
		Approver:  approver,
		GW:        NewGateway(cfg),
		Kube:      kube,
		ProjectID: project.ID.String(),
		DomainID:  domain.ID.String(),
		TeamID:    team.TeamID.String(),
	}, nil
}

var slugSanitizer = regexp.MustCompile(`[^a-z0-9-]+`)

// UniqueName generates a resource name unique to this test run:
// "e2e-<test-slug>-<8 hex chars>". Every fixture-created resource must use
// it. The predecessor suite used fixed names ("reg-*"), so a crashed run
// left conflicting state behind and the suite could never run in parallel.
func UniqueName(t *testing.T) string {
	t.Helper()

	slug := strings.ToLower(t.Name())
	slug = strings.ReplaceAll(slug, "/", "-")
	slug = strings.ReplaceAll(slug, "_", "-")
	slug = slugSanitizer.ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "test"
	}
	// Kubernetes object names are capped at 63 chars; leave room for the
	// "e2e-" prefix and "-<8hex>" suffix.
	const maxSlugLen = 63 - len("e2e-") - len("-12345678")
	if len(slug) > maxSlugLen {
		slug = slug[:maxSlugLen]
		slug = strings.Trim(slug, "-")
	}

	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		t.Fatalf("UniqueName: read random suffix: %v", err)
	}
	return fmt.Sprintf("e2e-%s-%s", slug, hex.EncodeToString(buf[:]))
}

// Fixture creates routes through the full lifecycle -- submit as editor,
// approve as approver, deploy -- and guarantees teardown even if the test
// fails partway through.
type Fixture struct {
	t   *testing.T
	env *Env
}

// NewFixture returns a Fixture bound to t and env.
func NewFixture(t *testing.T, env *Env) *Fixture {
	t.Helper()
	return &Fixture{t: t, env: env}
}

// Route creates a route from cfg (typically a services.CreateRouteInput
// value, or any value that marshals to the route-creation JSON body),
// approves every pending stage as the approver, deploys it, and registers
// t.Cleanup to tear it down. It fails the test via t.Fatalf if creation,
// approval, or deploy errors.
//
// Cleanup first rejects any still-pending approval on the route (as
// admin), then deletes the route (also an approval-gated action:
// RouteService.Delete always creates a "delete" approval when the project
// has approvals enabled, same as create/update) as editor, approves the
// resulting pending-delete approval if one was created (as approver --
// the submitter and approver must differ or the backend's self-approval
// guard rejects it), then deploys again as editor so the deletion is
// actually pushed to the cluster.
//
// The upfront reject step matters because t.Cleanup is registered here,
// BEFORE the approve/deploy calls below run: if either of them fails the
// test via t.Fatalf, cleanup still runs, but against a route whose CREATE
// approval is still pending. Without rejecting it first, DeleteRoute would
// hit RouteService.Delete's guard ("there is already a pending approval
// for this route") and every step below would be skipped, leaking the
// route, its approval, and any partial K8s objects permanently across
// runs. This mirrors platform/approvals_test.go's
// cleanupPendingOrRejectedRoute, which solves the identical problem for
// routes a test deliberately leaves pending or rejected.
//
// Unlike the predecessor fixture -- which only emitted a warning on
// cleanup failure, letting leaked routes accumulate silently -- every
// step's failure is reported via t.Errorf so a leak fails the test.
func (f *Fixture) Route(cfg any) Route {
	t := f.t
	t.Helper()
	ctx := context.Background()

	route, err := f.env.Editor.CreateRoute(ctx, f.env.ProjectID, f.env.DomainID, cfg)
	if err != nil {
		t.Fatalf("fixture: create route: %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		// Reject any pending approval left open by a test that failed (via
		// t.Fatalf) between CreateRoute above and the ApproveAllStages call
		// below -- see the doc comment above for why this has to run
		// before DeleteRoute. Tolerate "no pending approval found": the
		// common case is a route whose create approval was already fully
		// approved by the successful path below, which is not an error.
		if err := f.env.Admin.RejectApproval(cleanupCtx, f.env.ProjectID, route.ID.String(), "e2e fixture cleanup"); err != nil &&
			!strings.Contains(err.Error(), "no pending approval found") {
			t.Errorf("fixture cleanup: reject pending approval for route %s (%s): %v", route.Name, route.ID, err)
			return
		}

		// Mirror the creation path's role split: the submitter and the
		// approver must be different users, or the backend's self-approval
		// guard (ApprovalService.ApproveStage, "submitter cannot approve
		// their own submission") rejects the approval. Editor submits,
		// Approver approves, Editor deploys.
		if err := f.env.Editor.DeleteRoute(cleanupCtx, f.env.ProjectID, f.env.DomainID, route.ID.String()); err != nil {
			t.Errorf("fixture cleanup: delete route %s (%s): %v", route.Name, route.ID, err)
			return
		}
		if err := f.env.Approver.ApproveAllStages(cleanupCtx, f.env.ProjectID, route.ID.String()); err != nil &&
			!strings.Contains(err.Error(), "no pending approval found") {
			t.Errorf("fixture cleanup: approve delete for route %s (%s): %v", route.Name, route.ID, err)
			return
		}
		if err := f.env.Editor.DeployRoute(cleanupCtx, f.env.ProjectID, f.env.DomainID, route.ID.String()); err != nil {
			t.Errorf("fixture cleanup: deploy delete for route %s (%s): %v", route.Name, route.ID, err)
		}
	})

	if err := f.env.Approver.ApproveAllStages(ctx, f.env.ProjectID, route.ID.String()); err != nil {
		t.Fatalf("fixture: approve route %s (%s): %v", route.Name, route.ID, err)
	}

	if err := f.env.Editor.DeployRoute(ctx, f.env.ProjectID, f.env.DomainID, route.ID.String()); err != nil {
		t.Fatalf("fixture: deploy route %s (%s): %v", route.Name, route.ID, err)
	}

	deployed, err := f.env.Editor.GetRoute(ctx, f.env.ProjectID, f.env.DomainID, route.ID.String())
	if err != nil {
		t.Fatalf("fixture: fetch deployed route %s (%s): %v", route.Name, route.ID, err)
	}

	f.waitConverged(route.ID.String(), cfg)
	return deployed
}

// fixtureConvergeTimeout bounds waitConverged's POLICY gates -- the fatal
// ones, where waiting the full window is worth it because the alternative
// is a false failure.
var fixtureConvergeTimeout = 3 * time.Minute

// fixtureRouteGateTimeout bounds the ADVISORY route gate. It is much
// shorter than fixtureConvergeTimeout because that gate only logs: a route
// that legitimately resolves late (an external Backend CRD, a backend
// scaled to zero) is waited out by the caller's own data-plane probe
// anyway, and a route that never resolves -- grpcroute's mirror test,
// which pins a known Envoy Gateway defect -- would otherwise burn three
// minutes per run to reach a conclusion it already logged.
var fixtureRouteGateTimeout = 45 * time.Second

// waitConverged blocks until the Kubernetes objects this route's cfg asks
// for exist and report Accepted.
//
// DeployRoute returning 200 only means the backend wrote the objects to the
// API server. The route and each of its policies are SEPARATE objects that
// Envoy Gateway reconciles independently, and it programs the route first:
// on a warm kind cluster the route starts serving traffic within ~200ms
// while its SecurityPolicy / BackendTrafficPolicy / EnvoyExtensionPolicy
// lands a few hundred milliseconds later. Every test that gates only on
// "the route answers" (harness.WaitForRouteLive, the suites' own
// waitForGRPCLive) therefore has a window in which it asserts
// policy-dependent behaviour against a route whose policy is not applied
// yet -- which is exactly what made TestExtensionsLua, TestGRPCLua,
// TestCORSActualRequest, TestGRPCBTPCircuitBreaker,
// TestGRPCBTPRequestBuffer and TestGRPCClientModeRateLimit fail
// non-deterministically, each in under 2.5s, while the same tests passed
// whenever they happened to take longer.
//
// Gating here fixes it once for every test instead of per-test. Policy
// gates are fatal: the test explicitly configured that policy, so its
// absence or non-acceptance is a real failure and must not be tolerated
// silently. The route gate is advisory (logged, not fatal) because a route
// legitimately reports ResolvedRefs=False for a while -- external Backend
// CRDs reconcile after the route, and httproute's failover tests scale
// their backend to zero on purpose.
//
// cfg values that are not a services.CreateRouteInput carry no policy
// information, so there is nothing to gate on and waitConverged returns
// immediately.
func (f *Fixture) waitConverged(routeID string, cfg any) {
	t := f.t
	t.Helper()

	in, ok := cfg.(services.CreateRouteInput)
	if !ok {
		return
	}

	ns := f.env.Cfg.Namespace

	routeCtx, cancelRoute := context.WithTimeout(context.Background(), fixtureRouteGateTimeout)
	err := WaitForRouteAccepted(routeCtx, f.env.Kube, RouteGVR(string(in.Protocol)), ns, routeID, fixtureRouteGateTimeout)
	cancelRoute()
	if err != nil {
		t.Logf("fixture: route %s not fully accepted (continuing; some tests deploy routes whose refs resolve later): %v", routeID, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), fixtureConvergeTimeout)
	defer cancel()

	gates := []struct {
		want bool
		gvr  schema.GroupVersionResource
		what string
	}{
		{in.SecurityPolicy != nil, SecurityPolicyGVR, "SecurityPolicy"},
		{in.BackendTrafficPolicy != nil, BackendTrafficPolicyGVR, "BackendTrafficPolicy"},
		{in.ExtensionPolicy != nil || in.WafPolicy != nil, EnvoyExtensionPolicyGVR, "EnvoyExtensionPolicy"},
	}
	for _, g := range gates {
		if !g.want {
			continue
		}
		if err := WaitForPoliciesAccepted(ctx, f.env.Kube, g.gvr, ns, routeID, fixtureConvergeTimeout); err != nil {
			t.Fatalf("fixture: %s for route %s never accepted: %v", g.what, routeID, err)
		}
	}
}
