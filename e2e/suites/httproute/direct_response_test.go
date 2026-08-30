//go:build e2e

package httproute

import (
	"context"
	"testing"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestDirectResponse ports test_direct_response.py. Already had a real
// assertion (status + decoded body) in the Python source; transcribed
// as-is. RouteType "directResponse" has no backend at all -- Envoy
// synthesizes the response itself -- so no urlRewrite is needed.
func TestDirectResponse(t *testing.T) {
	t.Parallel()

	name, path := uniquePath(t)

	cfg := services.CreateRouteInput{
		Name:   name,
		TeamID: teamID(t),
		Config: models.RouteConfig{
			RouteType: models.RouteTypeDirectResponse,
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: path}},
			},
			DirectResponse: &models.DirectResponseConfig{
				StatusCode:  200,
				ContentType: "application/json",
				Body: &models.DirectResponseBody{
					Type:   models.DirectResponseBodyTypeInline,
					Inline: `{"status":"ok"}`,
				},
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
		t.Fatalf("direct response: route never became live: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("direct response: got status %d, want 200", resp.StatusCode)
	}

	var body struct {
		Status string `json:"status"`
	}
	if err := resp.JSON(&body); err != nil {
		t.Fatalf("direct response: decode body %q: %v", resp.Body, err)
	}
	if body.Status != "ok" {
		t.Fatalf("direct response: got status field %q, want %q", body.Status, "ok")
	}
}
