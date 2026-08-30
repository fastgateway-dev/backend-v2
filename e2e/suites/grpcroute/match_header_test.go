//go:build e2e

package grpcroute

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestGRPCHeaderMatch ports grpc_route_matching/test_header_match.py: a
// route matching grpcService plus a required header ("x-grpc-match: yes")
// only routes traffic when that header is present. This also establishes
// the negative case the Python original never checked: a call without the
// header must NOT reach this route -- it should behave as if the route
// doesn't exist (grpcNotReady's set: Unimplemented/NotFound/Unavailable),
// since it can only ever match this test's OWN catch-none-else
// discriminating header route otherwise; asserting non-OK there is what
// actually proves the header condition is enforced rather than ignored.
func TestGRPCHeaderMatch(t *testing.T) {
	t.Parallel()

	name, match, discriminatorOpt := uniqueMatch(t, "Exact", echoServiceName, "")
	match.Headers = append(match.Headers, models.HeaderMatch{Name: "x-grpc-match", Type: "Exact", Value: "yes"})

	cfg := services.CreateRouteInput{
		Name:     name,
		Protocol: models.RouteProtocolGRPC,
		TeamID:   teamID(t),
		Config: models.RouteConfig{
			RouteType: models.RouteTypeBackend,
			Matches:   []models.RouteMatch{match},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: podinfoService, Port: podinfoGRPCPort, Weight: 100},
			},
		},
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+30*time.Second)
	defer cancel()

	matchHeaderOpt := harness.WithGRPCMetadata("x-grpc-match", "yes")
	const wantBody = "hello-header-match"
	call := func(ctx context.Context) (*harness.GRPCResult, error) {
		res, _, err := echoCall(ctx, wantBody, discriminatorOpt, matchHeaderOpt)
		return res, err
	}
	res, err := waitForGRPCLive(ctx, call, routeLiveTimeout)
	if err != nil {
		t.Fatalf("header match: route never became live: %v", err)
	}
	if res.Code != codes.OK {
		t.Fatalf("header match: with x-grpc-match=yes got code %v, want %v", res.Code, codes.OK)
	}

	res, resp, err := echoCall(ctx, wantBody, discriminatorOpt, matchHeaderOpt)
	if err != nil {
		t.Fatalf("header match: request with header: %v", err)
	}
	if res.Code != codes.OK {
		t.Fatalf("header match: with x-grpc-match=yes got code %v, want %v", res.Code, codes.OK)
	}
	if resp.Body != wantBody {
		t.Fatalf("header match: got echoed body %q, want %q", resp.Body, wantBody)
	}

	// Negative case: without the required header, this route must not
	// match at all (only this test's discriminator header is present).
	res, _, err = echoCall(ctx, wantBody, discriminatorOpt)
	if err != nil {
		t.Fatalf("header match: request without header: %v", err)
	}
	if res.Code != codes.Unimplemented && res.Code != codes.NotFound && res.Code != codes.Unavailable {
		t.Fatalf("header match: without x-grpc-match got code %v, want one of {Unimplemented, NotFound, Unavailable} (route should not match)", res.Code)
	}
}
