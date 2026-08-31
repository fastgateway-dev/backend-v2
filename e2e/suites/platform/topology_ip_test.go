//go:build e2e

package platform

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// findIPRow returns the first row in rows matching source+refID+cidr, or
// fails t if none does.
func findIPRow(t *testing.T, rows []services.TopologyIPRow, source, refID, cidr string) services.TopologyIPRow {
	t.Helper()
	for _, r := range rows {
		if r.Source == source && r.SourceRef.ID.String() == refID && r.CIDR == cidr {
			return r
		}
	}
	t.Fatalf("no ips row found for source=%s sourceRef.id=%s cidr=%s among %d rows", source, refID, cidr, len(rows))
	return services.TopologyIPRow{}
}

// TestTopologyIPRowsRequiredColumns ports
// topology/test_ip_audit_reach.py:test_ip_rows_have_required_columns.
//
// ADAPTED: the Python original iterated whatever IP rows already existed
// on the project (possibly zero, making its loop body vacuous -- exactly
// the "can't fail no matter what" trap this task exists to eliminate).
// This creates its own general-mode route with a route-level IP allowlist
// (SecurityPolicy.Authorization.AllowedCIDRs, the same construct
// e2e/suites/security/general_mode_ip_allowlisting_test.go already
// exercises for enforcement) first, guaranteeing at least one real
// "route"-source row exists, then checks both the required-column shape
// across every row AND that this specific row is present with the exact
// CIDR configured.
func TestTopologyIPRowsRequiredColumns(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+30*time.Second)
	defer cancel()

	const testCIDR = "203.0.113.0/24" // TEST-NET-3, RFC 5737

	_, _, cfg := simpleRouteConfig(t)
	cfg.SecurityPolicy = &routeplan.SecurityPolicyInput{
		Authorization: &routeplan.AuthorizationInput{AllowedCIDRs: []string{testCIDR}},
	}
	fx := harness.NewFixture(t, env)
	route := fx.Route(cfg)

	resp := getProjectTopology(t, ctx)
	if len(resp.IPs) == 0 {
		t.Fatalf("project topology: ips is empty even though route %s (%s) has an allowlisted CIDR", route.Name, route.ID)
	}
	for i, ip := range resp.IPs {
		if _, _, err := net.ParseCIDR(ip.CIDR); err != nil {
			t.Fatalf("project topology: ips[%d].cidr=%q is not normalized to a CIDR: %v", i, ip.CIDR, err)
		}
		if ip.Source != "route" && ip.Source != "client" {
			t.Fatalf("project topology: ips[%d].source=%q, want \"route\" or \"client\"", i, ip.Source)
		}
		if ip.SourceRef.ID.String() == "" || ip.SourceRef.Name == "" {
			t.Fatalf("project topology: ips[%d].sourceRef=%+v, want both id and name populated", i, ip.SourceRef)
		}
		if ip.Reach.RouteIDs == nil {
			t.Fatalf("project topology: ips[%d].reach.routeIds is nil, want a (possibly empty) list", i)
		}
		if ip.Reach.DomainIDs == nil {
			t.Fatalf("project topology: ips[%d].reach.domainIds is nil, want a (possibly empty) list", i)
		}
	}

	ourRow := findIPRow(t, resp.IPs, "route", route.ID.String(), testCIDR)
	if len(ourRow.Reach.RouteIDs) != 1 || ourRow.Reach.RouteIDs[0].String() != route.ID.String() {
		t.Fatalf("route %s (%s): ips row reach.routeIds=%v, want exactly [%s]", route.Name, route.ID, ourRow.Reach.RouteIDs, route.ID)
	}
}

// TestTopologySameCIDRTwoClientsTwoRows ports
// topology/test_ip_audit_reach.py:test_same_cidr_two_clients_two_rows.
//
// ADAPTED: the Python original scanned the project's existing IP rows for
// a CIDR shared by two different sourceRefs and pytest.skip'd if it never
// found one ("no shared CIDR fixture present") -- another instance of a
// test that can only ever pass or silently skip, never fail. This
// deliberately constructs the shared-CIDR case: two clients, each given
// the SAME CIDR, each attached (with IP-allowlist enabled) to a different
// client-mode route, then asserts both resulting rows are present as
// distinct (source, sourceRef.id) pairs -- proving dedup happens WITHIN a
// (source, sourceRef) pair but never collapses genuinely different
// sourceRefs that happen to share a CIDR.
func TestTopologySameCIDRTwoClientsTwoRows(t *testing.T) {
	t.Parallel()

	const sharedCIDR = "198.51.100.7/32" // TEST-NET-2, RFC 5737

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+90*time.Second)
	defer cancel()

	makeClientModeRoute := func() harness.Route {
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
		return fx.Route(cfg)
	}

	routeA := makeClientModeRoute()
	routeB := makeClientModeRoute()

	clientA, err := createClient(ctx, harness.UniqueName(t), teamID(t))
	if err != nil {
		t.Fatalf("create clientA: %v", err)
	}
	cleanupClient(t, clientA.ID.String())
	if err := addClientIP(ctx, clientA.ID.String(), sharedCIDR, "shared CIDR fixture (A)"); err != nil {
		t.Fatalf("add IP to clientA: %v", err)
	}
	if _, err := attachAndDeploy(ctx, routeA.ID.String(), services.AttachFromRouteInput{ClientID: clientA.ID, EnableIPAllowlist: true}); err != nil {
		t.Fatalf("attach clientA to route %s (%s): %v", routeA.Name, routeA.ID, err)
	}

	clientB, err := createClient(ctx, harness.UniqueName(t), teamID(t))
	if err != nil {
		t.Fatalf("create clientB: %v", err)
	}
	cleanupClient(t, clientB.ID.String())
	if err := addClientIP(ctx, clientB.ID.String(), sharedCIDR, "shared CIDR fixture (B)"); err != nil {
		t.Fatalf("add IP to clientB: %v", err)
	}
	if _, err := attachAndDeploy(ctx, routeB.ID.String(), services.AttachFromRouteInput{ClientID: clientB.ID, EnableIPAllowlist: true}); err != nil {
		t.Fatalf("attach clientB to route %s (%s): %v", routeB.Name, routeB.ID, err)
	}

	resp := getProjectTopology(t, ctx)

	rowA := findIPRow(t, resp.IPs, "client", clientA.ID.String(), sharedCIDR)
	rowB := findIPRow(t, resp.IPs, "client", clientB.ID.String(), sharedCIDR)

	if rowA.SourceRef.ID == rowB.SourceRef.ID {
		t.Fatalf("clientA and clientB rows both report sourceRef.id=%s, want two distinct sourceRefs for the shared CIDR %s", rowA.SourceRef.ID, sharedCIDR)
	}

	dupCount := 0
	for _, r := range resp.IPs {
		if r.CIDR == sharedCIDR && r.Source == "client" {
			dupCount++
		}
	}
	if dupCount != 2 {
		t.Fatalf("project topology: found %d client-source rows for shared CIDR %s, want exactly 2 (one per client)", dupCount, sharedCIDR)
	}
}
