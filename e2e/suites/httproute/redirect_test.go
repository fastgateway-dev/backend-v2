//go:build e2e

package httproute

import (
	"context"
	"strings"
	"testing"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestRedirect ports test_redirect.py. Already had a real assertion
// (status + Location header) in the Python source; transcribed as-is.
// RouteType "redirect" has no backend -- the redirect filter synthesizes
// the response -- so no urlRewrite is needed.
func TestRedirect(t *testing.T) {
	t.Parallel()

	name, path := uniquePath(t)

	cfg := services.CreateRouteInput{
		Name:   name,
		TeamID: teamID(t),
		Config: models.RouteConfig{
			RouteType: models.RouteTypeRedirect,
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: path}},
			},
			Redirect: &models.RedirectConfig{
				Scheme:     "https",
				Hostname:   "new.example.com",
				StatusCode: 301,
			},
		},
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout)
	defer cancel()

	probe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path)
	}
	resp, err := harness.WaitForRouteLive(ctx, probe, routeLiveTimeout)
	if err != nil {
		t.Fatalf("redirect: route never became live: %v", err)
	}
	if resp.StatusCode != 301 {
		t.Fatalf("redirect: got status %d, want 301", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); !strings.Contains(loc, "new.example.com") {
		t.Fatalf("redirect: got Location %q, want it to contain %q", loc, "new.example.com")
	}
}
