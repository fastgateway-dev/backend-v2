//go:build e2e

// Package stream exercises L4 (TCP/UDP) stream routing end-to-end against a
// live FastGateway + Envoy Gateway deployment: it provisions a Stream Gateway,
// adds TCP/UDP routes to l4-echo backends (e2e/deps/l4-echo.yaml, namespace
// "default"), resolves the Stream's own LoadBalancer, and sends raw TCP/UDP
// traffic. The whole suite is gated to the verified Envoy Gateway line
// (>= 1.8) via the ENVOY_GATEWAY_VERSION env var; on older arms TestMain
// exits 0 so the suite is a no-op rather than a failure.
package stream

import (
	"context"
	"log"
	"os"
	"testing"

	"github.com/google/uuid"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
)

// env is the shared harness environment (authenticated API clients, the
// seeded project/team, kube + gateway clients).
var env *harness.Env

// streamTemplateID is a stream-enabled Gateway Template created once for the
// whole suite (leaked like other suites' fixtures — one template per run).
var streamTemplateID uuid.UUID

func TestMain(m *testing.M) {
	ctx := context.Background()
	var err error
	env, err = harness.NewEnv(ctx)
	if err != nil {
		log.Fatalf("stream e2e: build harness env: %v", err)
	}

	if !harness.L4Supported(env.Cfg.EnvoyGatewayVersion) {
		log.Printf("stream e2e: SKIP suite — Envoy Gateway %q is below the verified L4 line (>= 1.8)", env.Cfg.EnvoyGatewayVersion)
		os.Exit(0)
	}

	streamTemplateID, err = env.Admin.CreateStreamTemplate(ctx, env.ProjectID, "e2e-strtmpl-"+uuid.NewString()[:8])
	if err != nil {
		log.Fatalf("stream e2e: create stream template: %v", err)
	}

	os.Exit(m.Run())
}

// teamID returns the seeded "dev" team ID as a uuid.UUID (NewEnv resolved it
// as a string; a parse failure here would be a harness bug).
func teamID(t *testing.T) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(env.TeamID)
	if err != nil {
		t.Fatalf("parse team ID %q: %v", env.TeamID, err)
	}
	return id
}

// newStream creates a Stream under the suite's stream-enabled template in the
// seeded "default" namespace, registering cleanup to delete it after its
// routes are torn down.
func newStream(t *testing.T) models.Stream {
	t.Helper()
	s, err := env.Admin.CreateStream(context.Background(), env.ProjectID, harness.UniqueName(t), "default", streamTemplateID)
	if err != nil {
		t.Fatalf("create stream: %v", err)
	}
	t.Cleanup(func() {
		_ = env.Admin.DeleteStream(context.Background(), env.ProjectID, s.ID.String())
	})
	return s
}
