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
	// A non-404 status (even an error one, e.g. 500 or 403) is returned
	// immediately without retrying -- 404 alone means "not programmed yet".
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
