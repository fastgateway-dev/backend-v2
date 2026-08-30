//go:build e2e

package harness

import (
	"context"
	"testing"
	"time"
)

// These exercise WaitForRouteLive's pure control flow against a fake probe
// -- no cluster required, so they can (and must) run before any cluster
// exists. See go test -tags e2e ./e2e/harness/ -run TestWaitForRouteLive -v.

func TestWaitForRouteLive_ReturnsWhenServed(t *testing.T) {
	calls := 0
	probe := func(context.Context) (*Response, error) {
		calls++
		if calls < 3 {
			return &Response{StatusCode: 404}, nil
		}
		return &Response{StatusCode: 200}, nil
	}
	resp, err := WaitForRouteLive(context.Background(), probe, 30*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("got %d, want 200", resp.StatusCode)
	}
	if calls != 3 {
		t.Fatalf("probe called %d times, want 3", calls)
	}
}

func TestWaitForRouteLive_TimesOutOn404(t *testing.T) {
	probe := func(context.Context) (*Response, error) {
		return &Response{StatusCode: 404}, nil
	}
	_, err := WaitForRouteLive(context.Background(), probe, 3*time.Second)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestWaitForRouteLive_ReturnsNonServingStatusImmediately(t *testing.T) {
	// A non-404 status that isn't one of the "backend not resolved yet"
	// codes (500/502/503; see TestWaitForRouteLive_Retries5xxWithinGraceWindow
	// below) is returned immediately without retrying -- 404 alone means
	// "not programmed yet".
	calls := 0
	probe := func(context.Context) (*Response, error) {
		calls++
		return &Response{StatusCode: 403}, nil
	}
	resp, err := WaitForRouteLive(context.Background(), probe, 30*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 403 {
		t.Fatalf("got %d, want 403", resp.StatusCode)
	}
	if calls != 1 {
		t.Fatalf("probe called %d times, want 1", calls)
	}
}

// TestWaitForRouteLive_Retries5xxWithinGraceWindow proves a 500/502/503 is
// treated like a 404 -- retried, not returned -- as long as it keeps
// happening within backendGraceWindow. This is what protects
// traffic/external_backend_fqdn_test.go and external_backend_ip_test.go:
// an external (BackendTypeExternal) backendRef routes through a separate
// Backend CRD that reconciles after the HTTPRoute itself is programmed,
// and Gateway API mandates a 500 for any unresolved backendRef in that
// window.
func TestWaitForRouteLive_Retries5xxWithinGraceWindow(t *testing.T) {
	calls := 0
	probe := func(context.Context) (*Response, error) {
		calls++
		if calls < 3 {
			return &Response{StatusCode: 500}, nil
		}
		return &Response{StatusCode: 200}, nil
	}
	resp, err := WaitForRouteLive(context.Background(), probe, 30*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("got %d, want 200", resp.StatusCode)
	}
	if calls != 3 {
		t.Fatalf("probe called %d times, want 3", calls)
	}
}

// TestWaitForRouteLive_AcceptsPersistent5xxAfterGraceWindow proves that a
// 5xx which never clears is still eventually reported as the route's real,
// final status once backendGraceWindow elapses -- rather than being
// retried for the caller's ENTIRE timeout. This is what
// httproute/retry_test.go (final 503 after retries are exhausted) and
// httproute/fault_injection_test.go (fault-injected 503) rely on: their
// backend's error is genuine and permanent, not a transient
// backend-not-resolved-yet condition, so it must still surface.
func TestWaitForRouteLive_AcceptsPersistent5xxAfterGraceWindow(t *testing.T) {
	old := backendGraceWindow
	backendGraceWindow = 2 * time.Second
	defer func() { backendGraceWindow = old }()

	probe := func(context.Context) (*Response, error) {
		return &Response{StatusCode: 503}, nil
	}
	resp, err := WaitForRouteLive(context.Background(), probe, 10*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 503 {
		t.Fatalf("got %d, want 503 (persistent backend error should eventually be reported as final)", resp.StatusCode)
	}
}

// TestWaitForRouteLive_HandlesNilResponseWithNilError proves a probe that
// returns (nil, nil) is treated as "not ready yet" and retried instead of
// panicking on a nil dereference of resp.StatusCode.
func TestWaitForRouteLive_HandlesNilResponseWithNilError(t *testing.T) {
	calls := 0
	probe := func(context.Context) (*Response, error) {
		calls++
		if calls < 2 {
			return nil, nil
		}
		return &Response{StatusCode: 200}, nil
	}
	resp, err := WaitForRouteLive(context.Background(), probe, 30*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("got %d, want 200", resp.StatusCode)
	}
}

func TestWaitForRouteLive_RetriesOnProbeError(t *testing.T) {
	calls := 0
	probe := func(context.Context) (*Response, error) {
		calls++
		if calls < 2 {
			return nil, errProbeUnreachable
		}
		return &Response{StatusCode: 200}, nil
	}
	resp, err := WaitForRouteLive(context.Background(), probe, 30*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("got %d, want 200", resp.StatusCode)
	}
}

func TestWaitForRouteLive_RespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	probe := func(context.Context) (*Response, error) {
		cancel()
		return &Response{StatusCode: 404}, nil
	}
	_, err := WaitForRouteLive(ctx, probe, 30*time.Second)
	if err == nil {
		t.Fatal("expected an error from a cancelled context, got nil")
	}
}

type probeError struct{ msg string }

func (e *probeError) Error() string { return e.msg }

var errProbeUnreachable = &probeError{msg: "dial tcp: connection refused"}
