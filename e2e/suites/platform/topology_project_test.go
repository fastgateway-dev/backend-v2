//go:build e2e

package platform

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// getProjectTopology fetches GET /projects/:projectId/topology. No typed
// harness wrapper exists for it (e2e/harness/api.go has none for any
// topology endpoint), so this goes through API.Do directly, mirroring
// every other suite's handling of endpoints without a typed wrapper.
func getProjectTopology(t *testing.T, ctx context.Context) services.ProjectTopologyResponse {
	t.Helper()
	var resp services.ProjectTopologyResponse
	path := "/projects/" + env.ProjectID + "/topology"
	if _, err := env.Admin.Do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		t.Fatalf("get project topology: %v", err)
	}
	return resp
}

// TestProjectTopologyRequiredKeys ports
// topology/test_project_topology_basic.py:test_project_topology_returns_required_keys.
// Already a real assertion in the Python source (required-key presence,
// no status-membership tautology); ported unchanged in spirit. A nil slice
// in services.ProjectTopologyResponse (unlike an empty-but-present JSON
// array) is what a missing/omitted key from the Python original's
// perspective would look like once decoded into a typed Go struct, so
// checking non-nil is the direct analogue of the source's `assert key in
// body`.
func TestProjectTopologyRequiredKeys(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp := getProjectTopology(t, ctx)
	if resp.Domains == nil {
		t.Fatalf("project topology: domains is nil, want a (possibly empty) list")
	}
	if resp.Clients == nil {
		t.Fatalf("project topology: clients is nil, want a (possibly empty) list")
	}
	if resp.IPs == nil {
		t.Fatalf("project topology: ips is nil, want a (possibly empty) list")
	}
}

// TestProjectTopologyDomainCardShape ports
// topology/test_project_topology_basic.py:test_project_topology_domain_card_shape.
// Already a real assertion in the Python source; ported unchanged in
// spirit. Unlike the Python original (which returns early -- effectively a
// silent pass -- if the project has no domains at all), this asserts
// domains is non-empty first: the seeded e2e project always has at least
// the "default-public" domain (env.DomainID), so an empty list here would
// itself be a genuine regression worth failing on, not a reason to skip
// the rest of the check.
func TestProjectTopologyDomainCardShape(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp := getProjectTopology(t, ctx)
	if len(resp.Domains) == 0 {
		t.Fatalf("project topology: domains is empty, want at least the seeded %q domain", env.Cfg.DomainName)
	}
	d := resp.Domains[0]

	if d.ID.String() == "" {
		t.Fatalf("project topology: domains[0].id is empty")
	}
	if d.Name == "" {
		t.Fatalf("project topology: domains[0].name is empty")
	}
	if d.Hostname == "" {
		t.Fatalf("project topology: domains[0].hostname is empty")
	}
	if d.SecurityMode != "general" && d.SecurityMode != "client" {
		t.Fatalf("project topology: domains[0].securityMode=%q, want \"general\" or \"client\"", d.SecurityMode)
	}
	switch d.GatewayStatus {
	case services.TopologyStatusDeployed, services.TopologyStatusPending, services.TopologyStatusFailed, services.TopologyStatusDraft:
	default:
		t.Fatalf("project topology: domains[0].gatewayStatus=%q, want one of deployed/pending/failed/draft", d.GatewayStatus)
	}
}
