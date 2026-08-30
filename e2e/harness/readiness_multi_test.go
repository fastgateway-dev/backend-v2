//go:build e2e

package harness

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Tests for WaitForPoliciesAccepted -- the "every matching object must be
// Accepted" gate harness.Fixture uses. Unlike WaitForPolicyAccepted it must
// cope with a route owning SEVERAL policies of one kind (client-mode
// security produces one SecurityPolicy per attached client), where
// GetUnstructuredByLabel errors out with "N found, want exactly 1".

func TestWaitForPoliciesAccepted_ReturnsWhenAllAccepted(t *testing.T) {
	accepted := []map[string]any{{"type": "Accepted", "status": "True", "reason": "Accepted", "message": "ok"}}
	k := newFakeKubeWith(SecurityPolicyGVR, "SecurityPolicyList",
		securityPolicyObject("fastgateway", "route-a-client-1-sp", "route-a", accepted),
		securityPolicyObject("fastgateway", "route-a-client-2-sp", "route-a", accepted),
	)

	if err := WaitForPoliciesAccepted(context.Background(), k, SecurityPolicyGVR, "fastgateway", "route-a", 5*time.Second); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWaitForPoliciesAccepted_TimesOutWhenOneOfManyNotAccepted(t *testing.T) {
	// The single-object helper would return "2 found, want exactly 1"
	// here; this gate has to actually inspect both and refuse to pass
	// while either is unaccepted -- otherwise a client-mode route with one
	// unconverged client's policy reads as ready.
	k := newFakeKubeWith(SecurityPolicyGVR, "SecurityPolicyList",
		securityPolicyObject("fastgateway", "route-a-client-1-sp", "route-a", []map[string]any{
			{"type": "Accepted", "status": "True", "reason": "Accepted", "message": "ok"},
		}),
		securityPolicyObject("fastgateway", "route-a-client-2-sp", "route-a", []map[string]any{
			{"type": "Accepted", "status": "False", "reason": "Invalid", "message": "targetRef not found"},
		}),
	)

	err := WaitForPoliciesAccepted(context.Background(), k, SecurityPolicyGVR, "fastgateway", "route-a", 2*time.Second)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "route-a-client-2-sp") || !strings.Contains(err.Error(), "targetRef not found") {
		t.Errorf("error should name the offending object and its message, got: %v", err)
	}
}

func TestWaitForPoliciesAccepted_TimesOutWhenNoObjectExists(t *testing.T) {
	// An empty list is the state a route is in between DeployRoute
	// returning 200 and the policy object actually being written -- the
	// exact window this gate exists to close. It must not read as "all
	// zero objects are accepted".
	k := newFakeKubeWith(BackendTrafficPolicyGVR, "BackendTrafficPolicyList")

	err := WaitForPoliciesAccepted(context.Background(), k, BackendTrafficPolicyGVR, "fastgateway", "route-missing", 2*time.Second)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "no matching object created yet") {
		t.Errorf("error should say no object exists yet, got: %v", err)
	}
}

// Tests for WaitForResponse -- the data-plane counterpart, used wherever an
// assertion depends on a policy Envoy programs after the route itself.

func TestWaitForResponse_ReturnsOnceConditionHolds(t *testing.T) {
	// First response is the route serving traffic with its
	// EnvoyExtensionPolicy not yet applied (200, no header); the second is
	// the converged one. WaitForRouteLive would have returned the first.
	calls := 0
	probe := func(context.Context) (*Response, error) {
		calls++
		resp := &Response{StatusCode: 200, Header: http.Header{}}
		if calls > 1 {
			resp.Header.Set("x-lua-custom", "FOO")
		}
		return resp, nil
	}

	resp, err := WaitForResponse(context.Background(), probe, func(r *Response) bool {
		return r.Header.Get("x-lua-custom") == "FOO"
	}, 10*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := resp.Header.Get("x-lua-custom"); got != "FOO" {
		t.Errorf("x-lua-custom = %q, want %q", got, "FOO")
	}
	if calls < 2 {
		t.Errorf("expected at least 2 probes, got %d", calls)
	}
}

func TestWaitForResponse_TimesOutAndReturnsLastResponse(t *testing.T) {
	// A feature that never takes effect must still fail the test, and the
	// caller needs the last observed response to say what it got instead.
	probe := func(context.Context) (*Response, error) {
		return &Response{StatusCode: 200, Header: http.Header{}}, nil
	}

	resp, err := WaitForResponse(context.Background(), probe, func(r *Response) bool {
		return r.Header.Get("x-lua-custom") == "FOO"
	}, 2*time.Second)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if resp == nil {
		t.Fatal("expected the last observed response to be returned alongside the error")
	}
	if resp.StatusCode != 200 {
		t.Errorf("last response status = %d, want 200", resp.StatusCode)
	}
}

func TestWaitForResponse_RespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	probe := func(context.Context) (*Response, error) {
		return &Response{StatusCode: 200, Header: http.Header{}}, nil
	}
	_, err := WaitForResponse(ctx, probe, func(*Response) bool { return false }, 30*time.Second)
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
}

func TestRouteGVR_PicksKindFromProtocol(t *testing.T) {
	if got := RouteGVR("grpc"); got.Resource != "grpcroutes" {
		t.Errorf(`RouteGVR("grpc").Resource = %q, want "grpcroutes"`, got.Resource)
	}
	if got := RouteGVR("GRPC"); got.Resource != "grpcroutes" {
		t.Errorf(`RouteGVR("GRPC").Resource = %q, want "grpcroutes"`, got.Resource)
	}
	if got := RouteGVR("http"); got.Resource != "httproutes" {
		t.Errorf(`RouteGVR("http").Resource = %q, want "httproutes"`, got.Resource)
	}
	// An empty protocol is what CreateRouteInput carries when a test
	// leaves it unset, and those routes are HTTPRoutes.
	if got := RouteGVR(""); got.Resource != "httproutes" {
		t.Errorf(`RouteGVR("").Resource = %q, want "httproutes"`, got.Resource)
	}
}

// EnvoyGatewayAtLeast gates the skip in grpcroute/features_mirror_test.go,
// so getting its comparison wrong would silently retire a test on the very
// releases where the feature works.

func TestEnvoyGatewayAtLeast(t *testing.T) {
	cases := []struct {
		version string
		major   int
		minor   int
		want    bool
	}{
		{"1.8.0", 1, 8, true},
		{"v1.8.0", 1, 8, true},
		{"1.8.3", 1, 8, true},
		{"1.9.0", 1, 8, true},
		{"2.0.0", 1, 8, true},
		{"1.7.0", 1, 8, false},
		{"1.6.2", 1, 8, false},
		{"0.9.0", 1, 8, false},
		// Unknown must NOT skip: "we don't know what this cluster runs"
		// has to run the test and report a real failure, never quietly
		// drop coverage.
		{"", 1, 8, true},
		{"garbage", 1, 8, true},
		{"1", 1, 8, true},
		{"x.y.z", 1, 8, true},
	}
	for _, tc := range cases {
		c := &Config{EnvoyGatewayVersion: tc.version}
		if got := c.EnvoyGatewayAtLeast(tc.major, tc.minor); got != tc.want {
			t.Errorf("Config{%q}.EnvoyGatewayAtLeast(%d, %d) = %v, want %v", tc.version, tc.major, tc.minor, got, tc.want)
		}
	}
}
