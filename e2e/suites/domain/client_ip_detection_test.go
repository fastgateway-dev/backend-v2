//go:build e2e

package domain

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// echoedHeader decodes podinfo's /headers JSON response (podinfo echoes
// every request header it received) and returns the value of name,
// matched case-insensitively since podinfo (a Go binary) marshals
// net/http.Header, which canonicalizes header names ("X-Forwarded-For"),
// while some deployments report lowercase keys instead. A single value is
// returned regardless of whether podinfo encodes multi-value headers as a
// bare string or a JSON array of strings, since both shapes are used by
// different podinfo versions.
func echoedHeader(body []byte, name string) (string, bool, error) {
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return "", false, fmt.Errorf("decode podinfo /headers response: %w (body: %s)", err, truncate(body, 300))
	}
	for k, v := range decoded {
		if !strings.EqualFold(k, name) {
			continue
		}
		switch val := v.(type) {
		case string:
			return val, true, nil
		case []any:
			parts := make([]string, 0, len(val))
			for _, e := range val {
				parts = append(parts, fmt.Sprintf("%v", e))
			}
			return strings.Join(parts, ", "), true, nil
		default:
			return fmt.Sprintf("%v", val), true, nil
		}
	}
	return "", false, nil
}

// TestClientIPDetection ports
// domain_settings/test_client_ip_detection.py, replacing the tautological
// "assert resp.status_code in (200, 404)" with two genuine assertions per
// task-14-brief: (1) the X-Forwarded-For client-IP-detection setting
// persists via the settings API (already a real assertion in the Python
// source, ported as-is), and (2) traffic actually carries the
// client-supplied X-Forwarded-For value through to the backend -- proven
// by routing to podinfo's header-echoing /headers endpoint (the Python
// source issued no route/traffic assertion beyond bare "/" on the shared
// domain, which is why creating a real fixture route here is a deliberate
// deviation, matching every other test in this package).
//
// UNVERIFIED (no cluster available): the exact wire shape Envoy Gateway
// produces for the X-Forwarded-For header once xForwardedFor.numTrustedHops
// is configured is not confirmed here -- Envoy's XFF handling typically
// APPENDS the immediate downstream peer's address to whatever
// X-Forwarded-For value the client sent, rather than replacing it, so this
// asserts the client-supplied value (203.0.113.9) is a SUBSTRING of the
// header podinfo echoes back rather than an exact match, which should hold
// regardless of what Envoy appends. See task-14-15-report.md.
func TestClientIPDetection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+time.Minute)
	defer cancel()

	cleanupDomainSettings(t, false)
	t.Cleanup(func() { cleanupDomainSettings(t, true) })

	if _, err := updateDomainSettings(ctx, env.ProjectID, env.DomainID, services.UpdateDomainSettingsInput{
		ClientIPDetection: &models.ClientIPDetectionConfig{
			XForwardedFor: &models.XForwardedForConfig{NumTrustedHops: 1},
		},
	}); err != nil {
		t.Fatalf("client ip detection: configure XFF detection: %v", err)
	}

	// Verify settings are persisted (real assertion already in the Python
	// source; ported as-is).
	settings, err := getDomainSettings(ctx, env.ProjectID, env.DomainID)
	if err != nil {
		t.Fatalf("client ip detection: fetch domain settings: %v", err)
	}
	if settings.ClientIPDetection == nil || settings.ClientIPDetection.XForwardedFor == nil {
		t.Fatalf("client ip detection: settings have no clientIPDetection.xForwardedFor after update (got %+v)", settings)
	}
	if got := settings.ClientIPDetection.XForwardedFor.NumTrustedHops; got != 1 {
		t.Fatalf("client ip detection: numTrustedHops = %d, want 1", got)
	}

	// Verify traffic carries the client-supplied XFF value through to the
	// backend.
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
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: podinfoService, Port: podinfoPort, Weight: 100},
			},
			URLRewrite: rewriteTo("/headers"),
		},
	}
	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	const sentXFF = "203.0.113.9"
	probe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path, harness.WithHeader("X-Forwarded-For", sentXFF))
	}
	resp, err := harness.WaitForRouteLive(ctx, probe, routeLiveTimeout)
	if err != nil {
		t.Fatalf("client ip detection: route never became live: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("client ip detection: got status %d, want 200 (body: %s)", resp.StatusCode, truncate(resp.Body, 300))
	}

	got, found, err := echoedHeader(resp.Body, "X-Forwarded-For")
	if err != nil {
		t.Fatalf("client ip detection: %v", err)
	}
	if !found {
		t.Fatalf("client ip detection: podinfo /headers response has no X-Forwarded-For header (body: %s)", truncate(resp.Body, 500))
	}
	if !strings.Contains(got, sentXFF) {
		t.Fatalf("client ip detection: backend saw X-Forwarded-For %q, want it to contain the client-supplied value %q", got, sentXFF)
	}
}
