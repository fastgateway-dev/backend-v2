//go:build e2e

package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/fastgateway-dev/backend-v2/internal/models"
)

// defaultTimeout is applied to a request's context when the caller hasn't
// already set a deadline. Override per-call with context.WithTimeout.
const defaultTimeout = 30 * time.Second

// Type aliases: the harness hands back the backend's own request/response
// shapes instead of redeclaring them, so a change to the wire contract
// becomes a compile error here rather than a silently-passing test.
type (
	Project = models.Project
	Domain  = models.Domain
	Team    = models.ProjectTeamRole
	Route   = models.Route
	Client  = models.Client
)

// StatusError is returned for any non-2xx response. It carries both the
// status code and the raw response body so tests can assert on either
// without re-parsing anything.
type StatusError struct {
	Method     string
	Path       string
	StatusCode int
	Body       string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("%s %s: status %d: %s", e.Method, e.Path, e.StatusCode, e.Body)
}

// API is an authenticated FastGateway REST API client. Construct one with
// Login; every method takes a context.Context first so callers can bound
// or cancel individual calls.
type API struct {
	baseURL string
	token   string
	http    *http.Client
}

// Login authenticates against POST /auth/login (which returns an
// "accessToken" field -- see internal/handlers/auth_handler.go and
// internal/services/auth_service.go:LoginResponse) and returns an API
// client bound to the resulting token.
func Login(ctx context.Context, cfg *Config, user, pass string) (*API, error) {
	a := &API{
		baseURL: cfg.APIURL,
		http:    &http.Client{},
	}

	body := map[string]string{"username": user, "password": pass}
	var loginResp struct {
		AccessToken string `json:"accessToken"`
	}
	if _, err := a.Do(ctx, http.MethodPost, "/auth/login", body, &loginResp); err != nil {
		return nil, fmt.Errorf("login as %q: %w", user, err)
	}
	if loginResp.AccessToken == "" {
		return nil, fmt.Errorf("login as %q: response had no accessToken", user)
	}
	a.token = loginResp.AccessToken
	return a, nil
}

// Do is the escape hatch for any endpoint without a typed wrapper below. It
// marshals body as the JSON request payload (nil for no body), decodes a
// 2xx JSON response into out (nil to discard the body), and returns
// *StatusError for any non-2xx response. The returned *http.Response has
// its Body replaced with an in-memory copy so callers may read it again.
func (a *API) Do(ctx context.Context, method, path string, body any, out any) (*http.Response, error) {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultTimeout)
		defer cancel()
	}

	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("%s %s: marshal request body: %w", method, path, err)
		}
		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, a.baseURL+path, reader)
	if err != nil {
		return nil, fmt.Errorf("%s %s: build request: %w", method, path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}

	resp, err := a.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp, fmt.Errorf("%s %s: read response body: %w", method, path, err)
	}
	resp.Body = io.NopCloser(bytes.NewReader(respBody))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, &StatusError{Method: method, Path: path, StatusCode: resp.StatusCode, Body: string(respBody)}
	}

	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return resp, fmt.Errorf("%s %s: decode response: %w (body: %s)", method, path, err, string(respBody))
		}
	}
	return resp, nil
}

// --- Projects ---

type dataEnvelope[T any] struct {
	Data []T `json:"data"`
}

// GetProjectByName mirrors regression/helpers/api.py:get_project_by_name --
// GET /projects, wrapped in {"data": [...]} by ProjectHandler.List.
//
// limit=200 is explicit: ProjectHandler.List defaults to 20 (ordered by
// whatever the repo's default sort is) with no documented upper cap, so a
// project created earlier in a run with many other projects can silently
// fall off the first page and read as "not found".
func (a *API) GetProjectByName(ctx context.Context, name string) (Project, error) {
	var env dataEnvelope[Project]
	if _, err := a.Do(ctx, http.MethodGet, "/projects?limit=200", nil, &env); err != nil {
		return Project{}, err
	}
	for _, p := range env.Data {
		if p.Name == name {
			return p, nil
		}
	}
	return Project{}, fmt.Errorf("project %q not found", name)
}

// --- Domains ---

// GetDomainByName mirrors api.py:get_domain_by_name -- matches on either
// the domain's "name" or "hostname" field (both are top-level on
// models.Domain, unlike the team lookup below).
// limit=200 is explicit for the same reason as GetProjectByName: DomainHandler.List
// defaults to 20 with no documented upper cap.
func (a *API) GetDomainByName(ctx context.Context, projectID, name string) (Domain, error) {
	var env dataEnvelope[Domain]
	path := fmt.Sprintf("/projects/%s/domains?limit=200", projectID)
	if _, err := a.Do(ctx, http.MethodGet, path, nil, &env); err != nil {
		return Domain{}, err
	}
	for _, d := range env.Data {
		if d.Name == name || d.Hostname == name {
			return d, nil
		}
	}
	return Domain{}, fmt.Errorf("domain %q not found in project %s", name, projectID)
}

// --- Teams ---

// GetTeamByName mirrors api.py:get_team_by_name. NOTE: the Go handler
// (TeamHandler.ListProjectTeams) returns a raw array of
// models.ProjectTeamRole, which nests the team under a "team" key and has
// no top-level "name" field -- unlike api.py, which tries both
// t.get("name") and t.get("team", {}).get("name"), only the nested lookup
// can ever match against this backend.
func (a *API) GetTeamByName(ctx context.Context, projectID, name string) (Team, error) {
	var teams []Team
	path := fmt.Sprintf("/projects/%s/teams", projectID)
	if _, err := a.Do(ctx, http.MethodGet, path, nil, &teams); err != nil {
		return Team{}, err
	}
	for _, t := range teams {
		if t.Team.Name == name {
			return t, nil
		}
	}
	return Team{}, fmt.Errorf("team %q not found in project %s", name, projectID)
}

// --- Routes ---

// CreateRoute mirrors api.py:create_route. body is typically a
// services.CreateRouteInput value; it is marshaled as-is. The handler
// (RouteHandler.Create) wraps the created route with a "warnings" field
// (RouteResponse); those extra bytes are ignored when decoding into Route.
func (a *API) CreateRoute(ctx context.Context, projectID, domainID string, body any) (Route, error) {
	var out Route
	path := fmt.Sprintf("/projects/%s/domains/%s/routes", projectID, domainID)
	if _, err := a.Do(ctx, http.MethodPost, path, body, &out); err != nil {
		return Route{}, err
	}
	return out, nil
}

// UpdateRoute mirrors PUT .../routes/:routeId (RouteHandler.Update).
func (a *API) UpdateRoute(ctx context.Context, projectID, domainID, routeID string, body any) (Route, error) {
	var out Route
	path := fmt.Sprintf("/projects/%s/domains/%s/routes/%s", projectID, domainID, routeID)
	if _, err := a.Do(ctx, http.MethodPut, path, body, &out); err != nil {
		return Route{}, err
	}
	return out, nil
}

// GetRoute fetches a single route (RouteHandler.Get). Not in the api.py
// reference client, but needed by Fixture to observe post-deploy status.
func (a *API) GetRoute(ctx context.Context, projectID, domainID, routeID string) (Route, error) {
	var out Route
	path := fmt.Sprintf("/projects/%s/domains/%s/routes/%s", projectID, domainID, routeID)
	if _, err := a.Do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return Route{}, err
	}
	return out, nil
}

// DeleteRoute mirrors api.py:delete_route, which tolerates 404 (already
// gone) as success.
func (a *API) DeleteRoute(ctx context.Context, projectID, domainID, routeID string) error {
	path := fmt.Sprintf("/projects/%s/domains/%s/routes/%s", projectID, domainID, routeID)
	if _, err := a.Do(ctx, http.MethodDelete, path, nil, nil); err != nil {
		var statusErr *StatusError
		if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusNotFound {
			return nil
		}
		return err
	}
	return nil
}

// --- Approvals ---

// pendingApprovals mirrors api.py:get_pending_approvals -- GET
// /projects/:projectId/approvals?status=pending, optionally scoped to an
// entity type.
//
// limit=200 is explicit: ApprovalHandler.List defaults to 20, ordered
// created_at DESC, with no documented upper cap. With many tests running
// in parallel (each creating its own approvals) the one this caller wants
// can fall out of the newest 20 well before it is ever acted on, and
// ApproveAllStages / RejectApproval would then fail with "no pending
// approval found" even though the approval genuinely exists.
func (a *API) pendingApprovals(ctx context.Context, projectID, entityType string) ([]models.Approval, error) {
	path := fmt.Sprintf("/projects/%s/approvals?status=pending&limit=200", projectID)
	if entityType != "" {
		path += "&entityType=" + url.QueryEscape(entityType)
	}
	var env dataEnvelope[models.Approval]
	if _, err := a.Do(ctx, http.MethodGet, path, nil, &env); err != nil {
		return nil, err
	}
	return env.Data, nil
}

// ApproveAllStages mirrors api.py:approve_all_stages: find the pending
// approval for routeID and approve every pending stage.
//
// DISCREPANCY vs api.py: the Python client matches on
// `approval.get("routeId") == route_id or approval.get("entityId") ==
// route_id`. models.Approval (the Go handler's actual response shape) has
// no "routeId" field at all -- only "entityId" -- so the first branch of
// that Python `or` is always false. Matching here is done on EntityID
// only, which is the only field the Go handler actually serves.
func (a *API) ApproveAllStages(ctx context.Context, projectID, routeID string) error {
	approvals, err := a.pendingApprovals(ctx, projectID, "")
	if err != nil {
		return err
	}
	for _, appr := range approvals {
		if appr.EntityID.String() != routeID {
			continue
		}
		for _, stage := range appr.Stages {
			if stage.Status != models.ApprovalStatusPending {
				continue
			}
			path := fmt.Sprintf("/projects/%s/approvals/%s/stages/%s/approve", projectID, appr.ID, stage.ID)
			if _, err := a.Do(ctx, http.MethodPost, path, nil, nil); err != nil {
				return fmt.Errorf("approve stage %s of approval %s: %w", stage.ID, appr.ID, err)
			}
		}
		return nil
	}
	return fmt.Errorf("no pending approval found for route %s", routeID)
}

// RejectApproval finds the pending approval for routeID and rejects its
// first pending stage with reason as the required "comment" field.
//
// api.py has no equivalent helper at all (only approve_stage exists) --
// this is new surface required by the task-7 brief, designed directly
// from ApprovalHandler.Reject (which binds RejectRequest{Comment string
// `json:"comment" binding:"required"`}).
func (a *API) RejectApproval(ctx context.Context, projectID, routeID, reason string) error {
	approvals, err := a.pendingApprovals(ctx, projectID, "")
	if err != nil {
		return err
	}
	for _, appr := range approvals {
		if appr.EntityID.String() != routeID {
			continue
		}
		for _, stage := range appr.Stages {
			if stage.Status != models.ApprovalStatusPending {
				continue
			}
			path := fmt.Sprintf("/projects/%s/approvals/%s/stages/%s/reject", projectID, appr.ID, stage.ID)
			body := map[string]string{"comment": reason}
			if _, err := a.Do(ctx, http.MethodPost, path, body, nil); err != nil {
				return fmt.Errorf("reject stage %s of approval %s: %w", stage.ID, appr.ID, err)
			}
			return nil
		}
		return fmt.Errorf("approval %s for route %s has no pending stage", appr.ID, routeID)
	}
	return fmt.Errorf("no pending approval found for route %s", routeID)
}

// --- Deploy ---

// DeployRoute mirrors api.py:deploy_route.
func (a *API) DeployRoute(ctx context.Context, projectID, domainID, routeID string) error {
	path := fmt.Sprintf("/projects/%s/domains/%s/routes/%s/deploy", projectID, domainID, routeID)
	_, err := a.Do(ctx, http.MethodPost, path, nil, nil)
	return err
}

// --- Route versions ---

// routeVersionEnvelope matches RouteVersionHandler.List's response shape,
// which is flat ({"data","total","page","limit"}) rather than the nested
// {"data","pagination":{...}} shape used by every other list endpoint.
type routeVersionEnvelope struct {
	Data  []models.RouteVersion `json:"data"`
	Total int64                 `json:"total"`
}

// ListRouteVersions lists a route's version history (RouteVersionHandler.List).
func (a *API) ListRouteVersions(ctx context.Context, projectID, domainID, routeID string, page, limit int) ([]models.RouteVersion, int64, error) {
	path := fmt.Sprintf("/projects/%s/domains/%s/routes/%s/versions?page=%d&limit=%d", projectID, domainID, routeID, page, limit)
	var env routeVersionEnvelope
	if _, err := a.Do(ctx, http.MethodGet, path, nil, &env); err != nil {
		return nil, 0, err
	}
	return env.Data, env.Total, nil
}

// RollbackRoute rolls a route back to a previous version (RouteVersionHandler.Rollback).
func (a *API) RollbackRoute(ctx context.Context, projectID, domainID, routeID string, version int) (Route, error) {
	var out Route
	path := fmt.Sprintf("/projects/%s/domains/%s/routes/%s/versions/%d/rollback", projectID, domainID, routeID, version)
	if _, err := a.Do(ctx, http.MethodPost, path, nil, &out); err != nil {
		return Route{}, err
	}
	return out, nil
}

// --- Audit logs ---

type auditLogEnvelope struct {
	Data       []models.AuditLog `json:"data"`
	Pagination struct {
		Total int64 `json:"total"`
	} `json:"pagination"`
}

// ListAuditLogs lists a project's audit log (AuditHandler.List). Not present
// in api.py at all -- new surface required by the task-7 brief.
func (a *API) ListAuditLogs(ctx context.Context, projectID string, page, limit int, resourceType, action string) ([]models.AuditLog, int64, error) {
	path := fmt.Sprintf("/projects/%s/audit?page=%d&limit=%d", projectID, page, limit)
	if resourceType != "" {
		path += "&resourceType=" + url.QueryEscape(resourceType)
	}
	if action != "" {
		path += "&action=" + url.QueryEscape(action)
	}
	var env auditLogEnvelope
	if _, err := a.Do(ctx, http.MethodGet, path, nil, &env); err != nil {
		return nil, 0, err
	}
	return env.Data, env.Pagination.Total, nil
}

// --- API tokens ---

// apiTokenResponse has no backend type to reuse: AuthHandler.CreateAPIToken
// builds an ad hoc gin.H, not a named struct.
type apiTokenResponse struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Token     string     `json:"token"`
	ExpiresAt *time.Time `json:"expiresAt"`
}

// CreateAPIToken creates a personal API token for the currently
// authenticated user (AuthHandler.CreateAPIToken). Returns the token ID and
// the raw token value (shown only once).
func (a *API) CreateAPIToken(ctx context.Context, name string, expiresAt *time.Time) (id, rawToken string, err error) {
	body := struct {
		Name      string     `json:"name"`
		ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	}{Name: name, ExpiresAt: expiresAt}
	var out apiTokenResponse
	if _, err := a.Do(ctx, http.MethodPost, "/auth/tokens", body, &out); err != nil {
		return "", "", err
	}
	return out.ID, out.Token, nil
}

// RevokeAPIToken revokes a previously created API token (AuthHandler.RevokeAPIToken).
func (a *API) RevokeAPIToken(ctx context.Context, tokenID string) error {
	_, err := a.Do(ctx, http.MethodDelete, "/auth/tokens/"+tokenID, nil, nil)
	return err
}

// --- Clients ---

// CreateClient mirrors api.py:create_client (POST /clients). body is
// typically a services.CreateClientInput value.
func (a *API) CreateClient(ctx context.Context, body any) (Client, error) {
	var out Client
	if _, err := a.Do(ctx, http.MethodPost, "/clients", body, &out); err != nil {
		return Client{}, err
	}
	return out, nil
}

// AttachClient mirrors api.py:attach_client -- the route-side attachment
// endpoint (ClientAttachmentHandler.AttachFromRoute). body is typically a
// services.AttachFromRouteInput value.
func (a *API) AttachClient(ctx context.Context, projectID, domainID, routeID string, body any) (models.ClientRouteAttachment, error) {
	var out models.ClientRouteAttachment
	path := fmt.Sprintf("/projects/%s/domains/%s/routes/%s/clients/attach", projectID, domainID, routeID)
	if _, err := a.Do(ctx, http.MethodPost, path, body, &out); err != nil {
		return models.ClientRouteAttachment{}, err
	}
	return out, nil
}

// DetachClient requests detachment of an already-attached client
// (ClientAttachmentHandler.RequestDetachFromRoute). Not present in api.py.
func (a *API) DetachClient(ctx context.Context, projectID, domainID, routeID, attachmentID string) (models.ClientRouteAttachment, error) {
	var out models.ClientRouteAttachment
	path := fmt.Sprintf("/projects/%s/domains/%s/routes/%s/clients/%s/detach", projectID, domainID, routeID, attachmentID)
	if _, err := a.Do(ctx, http.MethodPost, path, nil, &out); err != nil {
		return models.ClientRouteAttachment{}, err
	}
	return out, nil
}

// pendingClientApprovals mirrors api.py:get_pending_client_approvals.
//
// limit=200 for the same reason as pendingApprovals: ClientAttachmentHandler.ListClientApprovals
// defaults to 20 with no documented upper cap.
func (a *API) pendingClientApprovals(ctx context.Context, projectID string) ([]models.Approval, error) {
	path := fmt.Sprintf("/projects/%s/client-approvals?status=pending&limit=200", projectID)
	var env dataEnvelope[models.Approval]
	if _, err := a.Do(ctx, http.MethodGet, path, nil, &env); err != nil {
		return nil, err
	}
	return env.Data, nil
}

// ApproveClientAttachment mirrors api.py:approve_all_client_stages.
//
// DISCREPANCY vs api.py: the Python client matches on
// `approval.get("entityId") == attachment_id or
// approval.get("clientAttachmentId") == attachment_id`. Like the route
// approval case above, models.Approval has no "clientAttachmentId" field --
// only "entityId" -- so only the first branch can ever match.
func (a *API) ApproveClientAttachment(ctx context.Context, projectID, attachmentID string) error {
	approvals, err := a.pendingClientApprovals(ctx, projectID)
	if err != nil {
		return err
	}
	for _, appr := range approvals {
		if appr.EntityID.String() != attachmentID {
			continue
		}
		for _, stage := range appr.Stages {
			if stage.Status != models.ApprovalStatusPending {
				continue
			}
			path := fmt.Sprintf("/projects/%s/client-approvals/%s/stages/%s/approve", projectID, appr.ID, stage.ID)
			if _, err := a.Do(ctx, http.MethodPost, path, nil, nil); err != nil {
				return fmt.Errorf("approve stage %s of client approval %s: %w", stage.ID, appr.ID, err)
			}
		}
		return nil
	}
	return fmt.Errorf("no pending client approval found for attachment %s", attachmentID)
}
