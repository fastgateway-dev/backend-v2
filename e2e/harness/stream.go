//go:build e2e

package harness

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/fastgateway-dev/backend-v2/internal/models"
)

// CreateStreamTemplate creates a stream-only Gateway Template (a single TCP/UDP
// listener range, no TLS, LoadBalancer) and returns its ID once it reports Active. Admin only.
func (a *API) CreateStreamTemplate(ctx context.Context, projectID, name string) (uuid.UUID, error) {
	body := map[string]any{
		"name":         name,
		"exposureType": "LoadBalancer",
		"listeners": []map[string]any{
			{"name": "tcpudp", "protocol": "TCP", "portRangeMin": 1, "portRangeMax": 65535},
		},
	}
	var dt models.DomainTemplate
	if _, err := a.Do(ctx, http.MethodPost, fmt.Sprintf("/projects/%s/domain-templates", projectID), body, &dt); err != nil {
		return uuid.Nil, err
	}
	if dt.Status != models.DomainTemplateStatusActive {
		return uuid.Nil, fmt.Errorf("stream template %q not active: %s (%s)", name, dt.Status, dt.StatusMessage)
	}
	return dt.ID, nil
}

// CreateStream creates a Stream under a stream-enabled template. Admin only
// (the endpoint requires CanManageDomains).
func (a *API) CreateStream(ctx context.Context, projectID, name, namespace string, templateID uuid.UUID) (models.Stream, error) {
	body := map[string]any{
		"name":              name,
		"namespace":         namespace,
		"gatewayTemplateId": templateID,
	}
	var s models.Stream
	_, err := a.Do(ctx, http.MethodPost, fmt.Sprintf("/projects/%s/streams", projectID), body, &s)
	return s, err
}

// CreateStreamRoute creates an L4 route under a stream. The stream-scoped
// endpoint sets StreamID, so body must not. Returns the created Route.
func (a *API) CreateStreamRoute(ctx context.Context, projectID, streamID string, body any) (Route, error) {
	var r Route
	_, err := a.Do(ctx, http.MethodPost, fmt.Sprintf("/projects/%s/streams/%s/routes", projectID, streamID), body, &r)
	return r, err
}

// DeployStreamRoute deploys an approved L4 route to the cluster.
func (a *API) DeployStreamRoute(ctx context.Context, projectID, streamID, routeID string) error {
	_, err := a.Do(ctx, http.MethodPost, fmt.Sprintf("/projects/%s/streams/%s/routes/%s/deploy", projectID, streamID, routeID), nil, nil)
	return err
}

// DeleteStream deletes a stream (404 tolerated so cleanup is idempotent).
func (a *API) DeleteStream(ctx context.Context, projectID, streamID string) error {
	_, err := a.Do(ctx, http.MethodDelete, fmt.Sprintf("/projects/%s/streams/%s", projectID, streamID), nil, nil)
	if se, ok := err.(*StatusError); ok && se.StatusCode == http.StatusNotFound {
		return nil
	}
	return err
}
