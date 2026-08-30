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

// TestGRPCWAF ports grpc_extensions/test_waf.py.
//
// The Python original tolerated either an allowed or WAF-blocked outcome
// for a plain, benign Echo call (`result["returncode"] == 0 or
// "PermissionDenied" in stderr or "rpc error" in stderr` -- the last
// clause makes this true of nearly any outcome, including one where WAF
// mistakenly blocks all traffic). A benign request should not trip an
// OWASP CRS ruleset at paranoia level 2 with default thresholds, so this
// port asserts the stronger, meaningful claim: the call succeeds. If WAF
// is misconfigured to block everything, this is the assertion that would
// actually catch it.
func TestGRPCWAF(t *testing.T) {
	t.Parallel()

	name, match, callOpt := uniqueMatch(t, "Exact", echoServiceName, "")
	anomalyThreshold := 5
	paranoiaLevel := 2

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
		WafPolicy: &services.WafPolicyInput{
			Mode:             "block",
			Rulesets:         []string{"owasp-crs"},
			ParanoiaLevel:    &paranoiaLevel,
			AnomalyThreshold: &anomalyThreshold,
			DisabledRuleIDs:  []int{920420},
		},
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+30*time.Second)
	defer cancel()

	call := func(ctx context.Context) (*harness.GRPCResult, error) {
		res, _, err := echoCall(ctx, "hello", callOpt)
		return res, err
	}
	res, err := waitForGRPCLive(ctx, call, routeLiveTimeout)
	if err != nil {
		t.Fatalf("waf: route never became live: %v", err)
	}
	if res.Code != codes.OK {
		t.Fatalf("waf: benign request got code %v, want %v", res.Code, codes.OK)
	}
}
