//go:build e2e

package platform

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
)

// NEW (task-17): API tokens had ZERO e2e coverage before this file --
// e2e/harness/api.go's CreateAPIToken/RevokeAPIToken have no Python
// predecessor (see their doc comments).
//
// Authenticating a raw request with the token needs a plain *http.Client
// call rather than a *harness.API method: API tokens authenticate exactly
// like a login-issued JWT (same "Authorization: Bearer <token>" header,
// same middleware.Authenticate -- internal/middleware/auth.go tries
// ValidateToken first, then falls back to ValidateAPIToken), but
// harness.API's only constructor (Login) always performs a username/
// password login and has no way to instead bind an already-known raw
// token string. Building the request directly here mirrors the same
// pattern e2e/suites/security and e2e/suites/grpcroute already use for
// jwt-server (generateJWTToken issues its own *http.Request rather than
// going through harness.API), rather than modifying e2e/harness to add a
// second constructor.

// authedRequest issues an authenticated GET to path (relative to
// env.Cfg.APIURL) using rawToken as a bearer token, and returns the
// response status code.
func authedRequest(ctx context.Context, rawToken, path string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, env.Cfg.APIURL+path, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+rawToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

// TestAPITokenLifecycle ports the API tokens brief's "create a token;
// authenticate an API call with it; a revoked token returns 401" steps as
// one sequential flow (the three steps are inherently ordered: revocation
// only means something once authentication with the live token has
// already been proven to work).
func TestAPITokenLifecycle(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// harness.UniqueName, not t.Name(), for consistency with the package
	// convention (every other created resource in this package uses it --
	// see main_test.go).
	name := harness.UniqueName(t)
	tokenID, rawToken, err := env.Editor.CreateAPIToken(ctx, name, nil)
	if err != nil {
		t.Fatalf("create API token: %v", err)
	}
	if rawToken == "" {
		t.Fatalf("create API token: response had no raw token value")
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		// Tolerate the token already being gone (RevokeAPIToken already ran
		// as part of the test body itself).
		_ = env.Editor.RevokeAPIToken(cleanupCtx, tokenID)
	})

	// A live token must authenticate a real API call. GET /projects has no
	// permission middleware beyond Authenticate() (cmd/server/main.go), so
	// any authenticated, non-revoked token reaches it successfully.
	status, err := authedRequest(ctx, rawToken, "/projects")
	if err != nil {
		t.Fatalf("authenticated request with live token: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("authenticated request with live token: got status %d, want %d", status, http.StatusOK)
	}

	if err := env.Editor.RevokeAPIToken(ctx, tokenID); err != nil {
		t.Fatalf("revoke API token %s: %v", tokenID, err)
	}

	status, err = authedRequest(ctx, rawToken, "/projects")
	if err != nil {
		t.Fatalf("authenticated request with revoked token: %v", err)
	}
	if status != http.StatusUnauthorized {
		t.Fatalf("authenticated request with revoked token: got status %d, want %d", status, http.StatusUnauthorized)
	}
}
