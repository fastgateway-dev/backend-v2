//go:build e2e

package platform

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

// Local copies of the client-management helpers e2e/suites/security and
// e2e/suites/grpcroute each already maintain their own (identical-in-spirit)
// versions of, per task-13/task-12's documented rationale: e2e/harness/api.go
// only exposes typed wrappers for CreateClient/AttachClient/DetachClient/
// ApproveClientAttachment, so anything else goes through API.Do directly
// (harness's documented escape hatch) rather than modifying e2e/harness.
// Used only by topology_ip_test.go's TestTopologySameCIDRTwoClientsTwoRows.

// createClient mirrors e2e/suites/security/client_mode_helpers_test.go's
// createClient (POST /clients).
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

// deleteClient mirrors e2e/suites/security/client_mode_helpers_test.go's
// deleteClient (DELETE /clients/:clientId).
func deleteClient(ctx context.Context, clientID string) error {
	_, err := env.Admin.Do(ctx, http.MethodDelete, "/clients/"+clientID, nil, nil)
	return err
}

// addClientIP mirrors e2e/suites/security/client_mode_helpers_test.go's
// addClientIP (POST /clients/:clientId/ips).
func addClientIP(ctx context.Context, clientID, cidr, description string) error {
	body := services.CreateClientIPInput{CIDR: cidr, Description: description}
	_, err := env.Admin.Do(ctx, http.MethodPost, "/clients/"+clientID+"/ips", body, nil)
	return err
}

// attachAndDeploy mirrors e2e/suites/security/client_mode_helpers_test.go's
// attachAndDeploy: attach a client to routeID as editor, approve the
// resulting pending client approval as admin, then redeploy the route as
// editor so the resulting SecurityPolicy/attachment is actually applied.
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
// aborting) on failure via t.Errorf. Mirrors
// e2e/suites/security/client_mode_helpers_test.go's identical helper.
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
