package services

import (
	"encoding/json"

	approvalpkg "github.com/fastgateway-dev/backend-v2/internal/approval"
	"github.com/fastgateway-dev/backend-v2/internal/models"
)

// RouteService owns what happens to a route when its approval reaches a
// terminal state. Stage planning and traversal now live in
// internal/approval; this file is the route half of that contract.
//
// buildRouteApprovalStages and resolveTeamScope used to live here. They are
// gone: internal/approval.PlanStages and internal/approval.resolveTeamScope
// are the single implementation, exercised by
// internal/approval/planning_test.go.
var (
	_ approvalpkg.Completer        = (*RouteService)(nil)
	_ approvalpkg.CancelAuthorizer = (*RouteService)(nil)
)

// applyRouteApprovalSnapshot copies the approved route config out of an
// approval's snapshot onto the route. A nil snapshot is a no-op; a corrupt
// one is an error. Both match the pre-2D behaviour.
func applyRouteApprovalSnapshot(route *models.Route, raw json.RawMessage) error {
	if raw == nil {
		return nil
	}
	var snapshot models.RouteApprovalSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return err
	}
	if snapshot.RouteConfig != nil {
		route.Config = *snapshot.RouteConfig
	}
	return nil
}
