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

// TestBackendMTLS ports backend_tls/test_backend_mtls.py: a route whose
// external backend requires mTLS (server-certificate verification AND a
// client certificate presented by the gateway itself) must be reachable.
// The Python source's assertion was already real
// (retry_until(accepted_status=[200])); this port changes only the route
// name (harness.UniqueName instead of the fixed "reg-backend-mtls") and
// adds the SNI to the failure message. The backend
// (backend-mtls-server-1.default.svc.cluster.local, which itself requires
// a client cert) and its CA/client-cert secrets (backend-mtls-ca,
// backend-mtls-client in fastgateway-system) are pre-provisioned cluster
// fixtures (see e2e/deps/backend-mtls-server.yaml,
// e2e/deps/create-secrets.sh) -- neither e2e/deps nor e2e/harness is
// touched by this port.
func TestBackendMTLS(t *testing.T) {
	const (
		backendAddress = "backend-mtls-server-1.default.svc.cluster.local"
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
						Mode:                 models.BackendTLSModeMTLS,
						SNI:                  backendAddress,
						CACertificateRefs:    []models.CertificateRef{{Kind: "Secret", Name: "backend-mtls-ca"}},
						ClientCertificateRef: &models.SecretRef{Name: "backend-mtls-client", Namespace: "fastgateway-system"},
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
		t.Fatalf("backend mtls: route never became live (backend SNI %q): %v", backendAddress, err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("backend mtls: got status %d, want 200 (backend SNI %q, body: %s)", resp.StatusCode, backendAddress, truncate(resp.Body, 300))
	}
}
