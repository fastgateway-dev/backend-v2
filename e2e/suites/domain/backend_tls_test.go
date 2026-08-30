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

// TestBackendTLS ports backend_tls/test_backend_tls.py: a route whose
// external backend requires simple TLS (server-certificate verification
// only, no client certificate) must be reachable. The Python source's
// assertion was already real (retry_until(accepted_status=[200]), so
// "assert status_code == 200" could never pass for the wrong reason); this
// port changes only the route name (harness.UniqueName instead of the
// fixed "reg-backend-tls", per task-14's isolation requirements) and adds
// the SNI to the failure message so a mismatch is diagnosable. The backend
// itself (backend-tls-server-1.default.svc.cluster.local, TLS terminated
// with a Root-CA-1-signed server cert) and its CA secret
// (backend-tls-ca) are pre-provisioned cluster fixtures (see
// e2e/deps/backend-tls-server.yaml, e2e/deps/create-secrets.sh) -- neither
// e2e/deps nor e2e/harness is touched by this port.
func TestBackendTLS(t *testing.T) {
	const (
		backendAddress = "backend-tls-server-1.default.svc.cluster.local"
		backendPort    = 443
	)

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
				{
					Type:        models.BackendTypeExternal,
					AddressType: models.ExternalAddressTypeFQDN,
					Address:     backendAddress,
					Port:        backendPort,
					Weight:      100,
					TLS: &models.BackendTLSConfig{
						Mode:              models.BackendTLSModeSimple,
						SNI:               backendAddress,
						CACertificateRefs: []models.CertificateRef{{Kind: "Secret", Name: "backend-tls-ca"}},
					},
				},
			},
		},
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+time.Minute)
	defer cancel()

	probe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path)
	}
	resp, err := harness.WaitForRouteLive(ctx, probe, routeLiveTimeout)
	if err != nil {
		t.Fatalf("backend tls: route never became live (backend SNI %q): %v", backendAddress, err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("backend tls: got status %d, want 200 (backend SNI %q, body: %s)", resp.StatusCode, backendAddress, truncate(resp.Body, 300))
	}
}
