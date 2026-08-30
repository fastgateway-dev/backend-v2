//go:build e2e

package platform

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
)

// NEW (task-17): audit logging had ZERO e2e coverage before this file.
// Per the task brief, "these were licence-gated and the licensing
// subsystem has been deleted" -- both tests here simply call the audit
// endpoints as any other authenticated request, with no licence of any
// kind present anywhere in this test run, which is itself the assertion
// that the feature no longer depends on one: if a licence were still
// silently required, every request below would fail outright rather than
// return real data.

// auditLogEnvelope mirrors e2e/harness/api.go's own (unexported)
// auditLogEnvelope for AuditHandler.List's response shape
// ({"data","pagination":{"total":...}}).
type auditLogEnvelope struct {
	Data       []models.AuditLog `json:"data"`
	Pagination struct {
		Total int64 `json:"total"`
	} `json:"pagination"`
}

// listAuditLogsByActor lists a project's audit log filtered by actor
// (userId) and, optionally, resourceType/action -- the "userId" query
// parameter AuditHandler.List supports (internal/handlers/audit_handler.go)
// but e2e/harness/api.go's ListAuditLogs wrapper does not expose (it only
// forwards resourceType/action). Built directly with API.Do, the
// harness's documented escape hatch for endpoints without a typed wrapper
// (see e.g. e2e/suites/security/client_mode_helpers_test.go's identical
// use of it), since e2e/harness may not be modified.
func listAuditLogsByActor(ctx context.Context, projectID string, actorID, resourceType, action string) ([]models.AuditLog, int64, error) {
	path := "/projects/" + projectID + "/audit?page=1&limit=50"
	if resourceType != "" {
		path += "&resourceType=" + resourceType
	}
	if action != "" {
		path += "&action=" + action
	}
	if actorID != "" {
		path += "&userId=" + actorID
	}
	var env2 auditLogEnvelope
	if _, err := env.Admin.Do(ctx, http.MethodGet, path, nil, &env2); err != nil {
		return nil, 0, err
	}
	return env2.Data, env2.Pagination.Total, nil
}

// findAuditEntry returns the first entry in logs whose ResourceID matches
// resourceID, or fails t if none does.
func findAuditEntry(t *testing.T, logs []models.AuditLog, resourceID string) models.AuditLog {
	t.Helper()
	for _, l := range logs {
		if l.ResourceID != nil && l.ResourceID.String() == resourceID {
			return l
		}
	}
	t.Fatalf("no audit log entry found for resource %s among %d entries", resourceID, len(logs))
	return models.AuditLog{}
}

// TestAuditLogRecordsRouteCreationAndActorFilter ports the audit brief's
// "creating a route writes an entry naming actor and action; the list
// endpoint returns it; filtering by actor ... works" steps into one flow:
// create a route as env.Editor, confirm the resulting "create"/"route"
// entry names the editor as actor, then re-fetch filtered by that same
// actor's ID (taken from the entry itself, since the harness exposes no
// "whoami" endpoint to look it up independently) and confirm both that our
// entry is still present AND that every other entry the filter returned
// also belongs to that same actor -- proving the filter parameter
// genuinely constrains the result set rather than being silently ignored.
func TestAuditLogRecordsRouteCreationAndActorFilter(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+60*time.Second)
	defer cancel()

	_, _, cfg := simpleRouteConfig(t)
	fx := harness.NewFixture(t, env)
	route := fx.Route(cfg)

	// Unfiltered (by resourceType+action only): find our own entry and
	// confirm it names the actor and action.
	unfiltered, _, err := env.Admin.ListAuditLogs(ctx, env.ProjectID, 1, 50, "route", "create")
	if err != nil {
		t.Fatalf("list audit logs (resourceType=route, action=create): %v", err)
	}
	entry := findAuditEntry(t, unfiltered, route.ID.String())
	if entry.Action != "create" {
		t.Fatalf("route %s (%s): audit entry has action=%q, want %q", route.Name, route.ID, entry.Action, "create")
	}
	if entry.ResourceType != "route" {
		t.Fatalf("route %s (%s): audit entry has resourceType=%q, want %q", route.Name, route.ID, entry.ResourceType, "route")
	}
	if entry.Username != env.Cfg.EditorUser {
		t.Fatalf("route %s (%s): audit entry has username=%q, want the actor who created it (%q)", route.Name, route.ID, entry.Username, env.Cfg.EditorUser)
	}
	if entry.UserID == nil {
		t.Fatalf("route %s (%s): audit entry has no userId, cannot exercise the actor filter", route.Name, route.ID)
	}
	actorID := entry.UserID.String()

	// Filtered by that actor: our entry must still be present, and every
	// other entry the filter returns must belong to the same actor.
	filtered, total, err := listAuditLogsByActor(ctx, env.ProjectID, actorID, "", "")
	if err != nil {
		t.Fatalf("list audit logs filtered by actor %s: %v", actorID, err)
	}
	if total == 0 || len(filtered) == 0 {
		t.Fatalf("list audit logs filtered by actor %s: got 0 entries, want at least our own create entry", actorID)
	}
	findAuditEntry(t, filtered, route.ID.String())
	for _, l := range filtered {
		gotActor := ""
		if l.UserID != nil {
			gotActor = l.UserID.String()
		}
		if gotActor != actorID {
			t.Fatalf("actor filter %s: entry %s (action=%s, resource=%s) has userId=%q, want it to equal the filter",
				actorID, l.ID, l.Action, l.ResourceName, gotActor)
		}
	}
}

// TestAuditLogEntryTimestampWithinExpectedWindow ports the audit brief's
// "filtering by ... date range works" step.
//
// KNOWN GAP: AuditHandler.List (internal/handlers/audit_handler.go) and
// AuditService/AuditLogRepository (internal/services/audit_service.go,
// internal/repository/audit_repository.go) support only resourceType,
// action, and userId query filters -- there is no server-side date-range
// parameter anywhere in the audit API (unlike e.g. the metrics endpoints'
// "range" parameter). This is a genuine backend API gap, not a harness
// gap, so per the task instructions ("if something genuinely does not
// exist, report it") it is reported here rather than worked around by
// editing anything -- see task-16-17-report.md.
//
// The closest available proxy given no server-side filter exists: create a
// route, capture wall-clock bounds immediately before and after, fetch the
// resulting audit entry, and confirm its CreatedAt timestamp -- the exact
// field a real date-range filter would operate on -- falls strictly within
// that window. This proves CreatedAt is populated correctly and
// consistently ordered relative to the action it records, which is as much
// of "date range filtering" as the current API surface can exercise.
func TestAuditLogEntryTimestampWithinExpectedWindow(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+60*time.Second)
	defer cancel()

	before := time.Now().Add(-2 * time.Second)

	_, _, cfg := simpleRouteConfig(t)
	fx := harness.NewFixture(t, env)
	route := fx.Route(cfg)

	logs, _, err := env.Admin.ListAuditLogs(ctx, env.ProjectID, 1, 50, "route", "create")
	if err != nil {
		t.Fatalf("list audit logs (resourceType=route, action=create): %v", err)
	}
	after := time.Now().Add(2 * time.Second)

	entry := findAuditEntry(t, logs, route.ID.String())
	if entry.CreatedAt.Before(before) || entry.CreatedAt.After(after) {
		t.Fatalf("route %s (%s): audit entry createdAt=%s, want it within [%s, %s]",
			route.Name, route.ID, entry.CreatedAt, before, after)
	}
}
