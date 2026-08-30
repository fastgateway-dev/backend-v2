//go:build e2e

package grpcroute

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// The Python grpc_client_mode suite drove client management (create,
// generate API key, add IP, configure JWT, delete) through
// helpers/api.py:FastGatewayAPI, which e2e/harness/api.go does not (yet)
// expose typed wrappers for beyond CreateClient/AttachClient/DetachClient/
// ApproveClientAttachment. Per task-12-brief's constraint against modifying
// e2e/harness, these use API.Do (its documented escape hatch for any
// endpoint without a typed wrapper) directly against the endpoints
// registered in cmd/server/main.go's "/clients" group.

// createClient mirrors api.py:create_client (POST /clients).
func createClient(ctx context.Context, name string, td uuid.UUID) (harness.Client, error) {
	body := services.CreateClientInput{
		Name:         name,
		Description:  "E2E test",
		TeamID:       td,
		ContactName:  "test",
		ContactEmail: "test@test.com",
	}
	return env.Admin.CreateClient(ctx, body)
}

// deleteClient mirrors api.py:delete_client (DELETE /clients/:clientId).
func deleteClient(ctx context.Context, clientID string) error {
	_, err := env.Admin.Do(ctx, http.MethodDelete, "/clients/"+clientID, nil, nil)
	return err
}

// generateClientAPIKey mirrors api.py:generate_api_key (POST
// /clients/:clientId/api-key). headerName == "" lets the backend default it
// to "x-api-key" (see ClientHandler.GenerateAPIKey).
func generateClientAPIKey(ctx context.Context, clientID, headerName string) (apiKey string, err error) {
	body := services.GenerateAPIKeyInput{HeaderName: headerName}
	var out services.GenerateAPIKeyResponse
	if _, err := env.Admin.Do(ctx, http.MethodPost, "/clients/"+clientID+"/api-key", body, &out); err != nil {
		return "", err
	}
	return out.APIKey, nil
}

// addClientIP mirrors api.py:add_client_ip (POST /clients/:clientId/ips).
func addClientIP(ctx context.Context, clientID, cidr, description string) error {
	body := services.CreateClientIPInput{CIDR: cidr, Description: description}
	_, err := env.Admin.Do(ctx, http.MethodPost, "/clients/"+clientID+"/ips", body, nil)
	return err
}

// configureClientJWT mirrors api.py:configure_client_jwt (POST
// /clients/:clientId/jwt).
func configureClientJWT(ctx context.Context, clientID, issuer, jwksURL string, audiences []string) error {
	body := services.ConfigureJWTInput{Issuer: issuer, JWKSURL: jwksURL, Audiences: audiences}
	_, err := env.Admin.Do(ctx, http.MethodPost, "/clients/"+clientID+"/jwt", body, nil)
	return err
}

// attachAndDeploy mirrors conftest.py:attach_and_deploy: attach a client to
// routeID as editor, approve the resulting pending client approval as
// admin (owner bypasses team-membership checks, same as the Python
// fixture), then redeploy the route as editor so the SecurityPolicy is
// actually applied.
func attachAndDeploy(ctx context.Context, routeID string, input services.AttachFromRouteInput) (models.ClientRouteAttachment, error) {
	attachment, err := env.Editor.AttachClient(ctx, env.ProjectID, env.DomainID, routeID, input)
	if err != nil {
		return models.ClientRouteAttachment{}, fmt.Errorf("attach client: %w", err)
	}
	if err := env.Admin.ApproveClientAttachment(ctx, env.ProjectID, attachment.ID.String()); err != nil {
		return attachment, fmt.Errorf("approve client attachment: %w", err)
	}
	if err := env.Editor.DeployRoute(ctx, env.ProjectID, env.DomainID, routeID); err != nil {
		return attachment, fmt.Errorf("deploy route after attach: %w", err)
	}
	return attachment, nil
}

// cleanupClient registers a t.Cleanup that deletes clientID, reporting (not
// aborting) on failure via t.Errorf -- same convention as
// harness.Fixture.Route's own cleanup. Register this AFTER the route
// fixture is created (fx.Route), so it runs BEFORE the route's own cleanup
// at teardown (t.Cleanup is LIFO): the client is detached implicitly by
// being deleted first, matching the order the Python suite's `finally:
// delete_client(...)` (which always ran before pytest's route_factory
// fixture teardown) actually produced.
func cleanupClient(t *testing.T, clientID string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := deleteClient(ctx, clientID); err != nil {
			t.Errorf("cleanup: delete client %s: %v", clientID, err)
		}
	})
}
