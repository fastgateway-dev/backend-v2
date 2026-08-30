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

// TestTCPKeepalive ports domain_settings/test_tcp_keepalive.py, replacing
// the tautological "assert resp.status_code in (200, 404)" with a genuine
// 200 per task-14-brief: the setting must persist via the API (already a
// real assertion in the Python source, ported as-is) AND traffic must
// still flow through the domain's listener afterward, proving the
// generated ClientTrafficPolicy did not break it. As with every other
// domain_settings test, the Python source issued no route/traffic
// assertion beyond bare "/" on the shared domain; this port creates a real
// fixture route so "traffic still returns 200" is checkable at all.
func TestTCPKeepalive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+time.Minute)
	defer cancel()

	cleanupDomainSettings(t, false)
	t.Cleanup(func() { cleanupDomainSettings(t, true) })

	if _, err := updateDomainSettings(ctx, env.ProjectID, env.DomainID, services.UpdateDomainSettingsInput{
		ClientConnection: &models.ClientConnectionConfig{
			TCPKeepalive: &models.TCPKeepaliveConfig{
				Probes:   int32Ptr(3),
				IdleTime: strPtr("60s"),
				Interval: strPtr("10s"),
			},
		},
	}); err != nil {
		t.Fatalf("tcp keepalive: configure TCP keepalive: %v", err)
	}

	// Verify settings are persisted (real assertion already in the Python
	// source; ported as-is).
	settings, err := getDomainSettings(ctx, env.ProjectID, env.DomainID)
	if err != nil {
		t.Fatalf("tcp keepalive: fetch domain settings: %v", err)
	}
	if settings.ClientConnection == nil || settings.ClientConnection.TCPKeepalive == nil {
		t.Fatalf("tcp keepalive: settings have no clientConnection.tcpKeepalive after update (got %+v)", settings)
	}
	ka := settings.ClientConnection.TCPKeepalive
	if ka.Probes == nil || *ka.Probes != 3 {
		t.Fatalf("tcp keepalive: probes = %v, want 3", ka.Probes)
	}
	if ka.IdleTime == nil || *ka.IdleTime != "60s" {
		t.Fatalf("tcp keepalive: idleTime = %v, want %q", ka.IdleTime, "60s")
	}
	if ka.Interval == nil || *ka.Interval != "10s" {
		t.Fatalf("tcp keepalive: interval = %v, want %q", ka.Interval, "10s")
	}

	// Verify traffic still flows through the gateway: the
	// ClientTrafficPolicy generated for TCP keepalive must not have broken
	// the listener.
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

	probe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path)
	}
	resp, err := harness.WaitForRouteLive(ctx, probe, routeLiveTimeout)
	if err != nil {
		t.Fatalf("tcp keepalive: route never became live: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("tcp keepalive: got status %d, want 200 (body: %s)", resp.StatusCode, truncate(resp.Body, 300))
	}
}
