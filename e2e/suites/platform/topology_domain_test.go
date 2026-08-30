//go:build e2e

package platform

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// getDomainTopology fetches GET
// /projects/:projectId/domains/:domainId/topology. No typed harness
// wrapper exists for it, so this goes through API.Do directly.
func getDomainTopology(t *testing.T, ctx context.Context) services.DomainTopologyResponse {
	t.Helper()
	var resp services.DomainTopologyResponse
	path := "/projects/" + env.ProjectID + "/domains/" + env.DomainID + "/topology"
	if _, err := env.Admin.Do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		t.Fatalf("get domain topology: %v", err)
	}
	return resp
}

// TestDomainTopologySecurityModeShape ports
// topology/test_domain_topology_modes.py:test_domain_topology_security_mode_shape.
//
// ADAPTED: the Python original was parametrized over two FIXTURE domains
// named "topology-general-*"/"topology-client-*" and pytest.skip'd
// entirely if neither existed. No such fixture domains are provisioned
// anywhere in this repository's e2e/cmd/e2e-seed (unlike this repo's other
// domain-per-mechanism precedents, creating a second domain here would hit
// e2e/suites/domain/main_test.go's own documented reasons for avoiding a
// second LoadBalancer+TLS domain), and internal/services/topology_service.go
// shows "securityMode" is not even a stored domain field -- it's DERIVED
// per request: general unless ANY route on the domain currently has
// SecurityMode=="client" (see GetDomainTopology's "Infer domain security
// mode from routes" comment). Since e2e/suites/security and
// e2e/suites/grpcroute's own client_mode tests attach client-mode routes
// to this exact shared domain, its securityMode can genuinely be either
// value depending on what else is running -- so rather than requiring one
// specific mode (which would be flaky) or skip entirely (which would
// never really test anything, the same "always passes" trap this whole
// task exists to fix), this checks BOTH branches of the API's actual
// contract against whatever mode is currently observed, in one atomic
// response.
func TestDomainTopologySecurityModeShape(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp := getDomainTopology(t, ctx)

	switch resp.Domain.SecurityMode {
	case "general":
		if len(resp.Clients) != 0 {
			t.Fatalf("domain topology: securityMode=general but clients=%v, want empty", resp.Clients)
		}
		if len(resp.Attachments) != 0 {
			t.Fatalf("domain topology: securityMode=general but attachments=%v, want empty", resp.Attachments)
		}
	case "client":
		if resp.Clients == nil {
			t.Fatalf("domain topology: securityMode=client but clients is nil, want a (possibly empty) list")
		}
		if resp.Attachments == nil {
			t.Fatalf("domain topology: securityMode=client but attachments is nil, want a (possibly empty) list")
		}
	default:
		t.Fatalf("domain topology: securityMode=%q, want \"general\" or \"client\"", resp.Domain.SecurityMode)
	}

	switch resp.Gateway.Status {
	case services.TopologyStatusDeployed, services.TopologyStatusPending, services.TopologyStatusFailed, services.TopologyStatusDraft:
	default:
		t.Fatalf("domain topology: gateway.status=%q, want one of deployed/pending/failed/draft", resp.Gateway.Status)
	}
	for i, r := range resp.Routes {
		switch r.Status {
		case services.TopologyStatusDeployed, services.TopologyStatusPending, services.TopologyStatusFailed, services.TopologyStatusDraft:
		default:
			t.Fatalf("domain topology: routes[%d].status=%q, want one of deployed/pending/failed/draft", i, r.Status)
		}
		if r.Protocol != "http" && r.Protocol != "grpc" {
			t.Fatalf("domain topology: routes[%d].protocol=%q, want \"http\" or \"grpc\"", i, r.Protocol)
		}
	}
}

// TestDomainTopologyBackendDedupHitCount ports
// topology/test_domain_topology_modes.py:test_domain_topology_backend_dedup_hit_count.
//
// ADAPTED: rather than reading whatever backends happen to already exist
// on the shared domain (which, unlike the Python predecessor's isolated
// per-test-run environment, could be zero if this is the only test in the
// whole `go test ./e2e/...` invocation touching this domain -- an empty
// `for` loop that never actually inspects a real row is exactly the kind
// of "can't fail no matter what" test this task exists to eliminate),
// this creates its own fixture route against nginx-service first, so the
// backend it asserts on (dedup + hitCount>=1) is guaranteed to exist and
// is genuinely produced by this test's own action.
func TestDomainTopologyBackendDedupHitCount(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+30*time.Second)
	defer cancel()

	_, _, cfg := simpleRouteConfig(t)
	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	resp := getDomainTopology(t, ctx)

	seen := map[string]bool{}
	var nginxBackend *services.DomainTopologyBackend
	for i := range resp.Backends {
		b := &resp.Backends[i]
		if seen[b.ID] {
			t.Fatalf("domain topology: backend id %q appears more than once, want backends deduped", b.ID)
		}
		seen[b.ID] = true
		if b.HitCount < 1 {
			t.Fatalf("domain topology: backend %q has hitCount=%d, want >=1", b.ID, b.HitCount)
		}
		if b.Service != nil && *b.Service == nginxService {
			nginxBackend = b
		}
	}
	if nginxBackend == nil {
		t.Fatalf("domain topology: no backend entry found for service %q even though a route referencing it was just created and deployed", nginxService)
	}
}
