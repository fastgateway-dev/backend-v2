//go:build e2e

package traffic

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// TestRateLimitCapabilities ports rate_limiting/test_capabilities.py:
// test_rate_limit_capabilities. Already a real assertion in the Python
// source (`assert "rateLimitAvailable" in caps`, checking key PRESENCE
// rather than a status-membership tautology); ported using the same
// map-decode shape since ProjectHandler.GetCapabilities
// (internal/handlers/project_handler.go) always includes the key when it
// responds 2xx (ginH{"rateLimitAvailable": ...}), so decoding into
// map[string]any and checking for the key is the direct Go equivalent of
// Python's `in` check -- decoding straight into a typed bool field would
// silently default to false/present even if the backend ever stopped
// sending the key, which is exactly the failure mode the Python assertion
// exists to catch.
func TestRateLimitCapabilities(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var caps map[string]any
	path := "/projects/" + env.ProjectID + "/capabilities"
	if _, err := env.Admin.Do(ctx, http.MethodGet, path, nil, &caps); err != nil {
		t.Fatalf("rate limit capabilities: GET %s: %v", path, err)
	}
	if _, ok := caps["rateLimitAvailable"]; !ok {
		t.Fatalf("rate limit capabilities: response has no \"rateLimitAvailable\" key (got %+v)", caps)
	}
}
