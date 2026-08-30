//go:build e2e

package platform

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// requireMockProm skips t when no mock Prometheus endpoint is configured
// (MOCK_PROM_URL / harness.Config.MockPromURL). See the package doc
// comment for why: unlike jwt-server/external-auth (e2e/deps/*.yaml,
// committed to this repo), no mock-prometheus fixture ships anywhere in
// this repository, so this is the only honest option short of guessing at
// an unverifiable in-cluster default.
func requireMockProm(t *testing.T) string {
	t.Helper()
	if env.Cfg.MockPromURL == "" {
		t.Skip("MOCK_PROM_URL not set: no mock-prometheus fixture is provisioned in e2e/deps; skipping (see the package doc comment)")
	}
	return env.Cfg.MockPromURL
}

// setProjectMetricsConfig PATCHes the seeded e2e project's metrics
// endpoint config (project-wide shared mutable state -- see the package
// doc comment) and registers a t.Cleanup that restores whatever values
// were in place before this call, fetched via GetProjectByName first.
// Every test that calls this must NOT call t.Parallel(): Go only starts a
// parallel test's body once every non-parallel test in the package has
// finished, so simply omitting t.Parallel() from every metrics-config-
// touching test serializes them against each other and against every
// other (parallel) test that reads project metrics, with no extra locking
// needed.
func setProjectMetricsConfig(t *testing.T, endpointURL, authType string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	before, err := env.Admin.GetProjectByName(ctx, env.Cfg.ProjectName)
	if err != nil {
		t.Fatalf("fetch project %q baseline metrics config: %v", env.Cfg.ProjectName, err)
	}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		restoreURL, restoreAuth := before.MetricsEndpointURL, before.MetricsAuthType
		input := services.UpdateProjectInput{MetricsEndpointURL: &restoreURL, MetricsAuthType: &restoreAuth}
		if _, err := env.Admin.Do(cleanupCtx, http.MethodPatch, "/projects/"+env.ProjectID, input, nil); err != nil {
			t.Errorf("cleanup: restore project metrics config (endpoint=%q authType=%q): %v", restoreURL, restoreAuth, err)
		}
	})

	url, authT := endpointURL, authType
	input := services.UpdateProjectInput{MetricsEndpointURL: &url, MetricsAuthType: &authT}
	if _, err := env.Admin.Do(ctx, http.MethodPatch, "/projects/"+env.ProjectID, input, nil); err != nil {
		t.Fatalf("set project metrics config (endpoint=%q authType=%q): %v", endpointURL, authType, err)
	}
}
