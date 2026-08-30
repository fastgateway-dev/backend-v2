//go:build e2e

package security

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

// The Python client_mode suite drove client management (create, generate
// API key, add IP, configure JWT, configure mTLS, delete) through
// helpers/api.py:FastGatewayAPI, which e2e/harness/api.go does not (yet)
// expose typed wrappers for beyond CreateClient/AttachClient/DetachClient/
// ApproveClientAttachment. Per task-13-brief's constraint against
// modifying e2e/harness, these use API.Do (its documented escape hatch for
// any endpoint without a typed wrapper) directly against the endpoints
// registered in cmd/server/main.go's "/clients" group -- mirroring
// e2e/suites/grpcroute/client_mode_helpers_test.go's identical approach
// for the gRPC client_mode port (task-12), plus configureClientMTLS, which
// grpcroute never needed.

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

// configureClientMTLS mirrors api.py:configure_client_mtls (PUT
// /clients/:clientId/mtls).
//
// Unlike every other client-mutation helper above (which use env.Admin,
// the project owner), this uses env.Editor deliberately: ClientHandler.
// UpdateClientMTLS checks team membership directly (teamRepo.IsMember)
// with NO owner-role bypass, unlike e.g. ClientHandler.Create (which
// explicitly checks `middleware.IsOwner(user)` first). Calling this as
// env.Admin would get a 403 "you must be a member of the client's team"
// unless the admin user happens to also be a "dev"-team member -- exactly
// why regression/tests/client_mode/test_mtls.py:751 uses editor_api (a
// real "dev" team member, per e2e/harness/config.go's EditorUser) instead
// of client_admin_api for this one call.
func configureClientMTLS(ctx context.Context, clientID string, input services.UpdateClientMTLSInput) error {
	_, err := env.Editor.Do(ctx, http.MethodPut, "/clients/"+clientID+"/mtls", input, nil)
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
