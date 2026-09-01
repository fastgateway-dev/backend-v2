package services

import (
	"encoding/json"
	"fmt"

	approvalpkg "github.com/fastgateway-dev/backend-v2/internal/approval"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
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

// OnApproved moves the route to its post-approval state. This is the logic
// that lived in ApprovalService.onRouteApprovalComplete before Phase 2D; it
// belongs with the route, per §6.5 of the master design.
//
// The status mapping is reproduced verbatim: create -> approved,
// update -> pending_deploy, delete -> pending_deploy. create and update also
// apply the approved config snapshot to the route before the status moves,
// exactly as onRouteApprovalComplete did; delete does not.
func (s *RouteService) OnApproved(a *models.Approval) error {
	route, err := s.routeRepo.GetByID(a.EntityID)
	if err != nil {
		return err
	}

	var next models.RouteStatus
	switch a.Action {
	case models.ApprovalActionCreate:
		if err := applyRouteApprovalSnapshot(route, a.ConfigSnapshot); err != nil {
			return err
		}
		next = models.RouteStatusApproved

	case models.ApprovalActionUpdate:
		if err := applyRouteApprovalSnapshot(route, a.ConfigSnapshot); err != nil {
			return err
		}
		next = models.RouteStatusPendingDeploy

	case models.ApprovalActionDelete:
		next = models.RouteStatusPendingDeploy

	default:
		// Unreachable for a route entity, which only ever carries
		// create/update/delete. It fails closed rather than persisting the
		// route with an unchanged status, which is what the pre-2D switch
		// did when it fell through.
		return fmt.Errorf("route approval: unsupported action %q", a.Action)
	}

	// The create and update cases above applied the approved config snapshot
	// to route. routeStateMachine.To owns route.Status and nothing else, and
	// it does not write on a no-op transition (see its CONTRACT comment), so
	// an already-at-target route would apply the snapshot in memory and throw
	// it away. Pre-2D this path did an unconditional routeRepo.Update; persist
	// explicitly here so the approved config survives regardless of whether
	// the status actually moves.
	if route.Status == next {
		return s.routeRepo.Update(route)
	}

	return s.state.To(route, next, fmt.Sprintf("approval %s approved (action %s)", a.ID, a.Action))
}

// OnRejected reverts the route when its approval is rejected. Reproduces
// ApprovalService.onRouteApprovalRejected: create -> rejected,
// update -> active, delete -> active.
func (s *RouteService) OnRejected(a *models.Approval) error {
	route, err := s.routeRepo.GetByID(a.EntityID)
	if err != nil {
		return err
	}

	var next models.RouteStatus
	switch a.Action {
	case models.ApprovalActionCreate:
		next = models.RouteStatusRejected
	case models.ApprovalActionUpdate, models.ApprovalActionDelete:
		next = models.RouteStatusActive
	default:
		return fmt.Errorf("route approval: unsupported action %q", a.Action)
	}

	return s.state.To(route, next, fmt.Sprintf("approval %s rejected (action %s)", a.ID, a.Action))
}

// OnCancelled reverts the route when its approval is withdrawn. Reproduces
// ApprovalService.onRouteApprovalCancelled: a cancelled create deletes the
// route outright (it was never deployed), while a cancelled update or
// delete returns the still-deployed route to active.
func (s *RouteService) OnCancelled(a *models.Approval) error {
	switch a.Action {
	case models.ApprovalActionCreate:
		// Route was never deployed, delete it entirely.
		return s.routeRepo.Delete(a.EntityID)

	case models.ApprovalActionUpdate, models.ApprovalActionDelete:
		// Route is already deployed, revert to active.
		route, err := s.routeRepo.GetByID(a.EntityID)
		if err != nil {
			return err
		}
		next := models.RouteStatusActive
		return s.state.To(route, next, fmt.Sprintf("approval %s cancelled (action %s)", a.ID, a.Action))

	default:
		return fmt.Errorf("route approval: unsupported action %q", a.Action)
	}
}

// CanCancel implements approval.CancelAuthorizer: a member of the route's
// owning team may cancel a route approval on top of the engine's own
// submitter/owner/project-admin rights. This is the route lookup the engine
// deliberately does not own (pre-2D approval_service.go:535-543).
//
// It fails closed: both the route lookup error and the membership lookup
// error mean "not permitted", which is what the pre-2D code did by
// discarding them.
func (s *RouteService) CanCancel(a *models.Approval, user *models.User) bool {
	if a.EntityType != models.ApprovalEntityRoute {
		return false
	}
	route, err := s.routeRepo.GetByID(a.EntityID)
	if err != nil {
		return false
	}
	isMember, err := s.teamRepo.IsMember(route.TeamID, user.ID)
	if err != nil {
		return false
	}
	return isMember
}

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

// GetApprovalIDForEntity returns the most recent approval ID for an entity.
// Checks pending first, then latest approved.
func (s *RouteService) GetApprovalIDForEntity(entityType models.ApprovalEntityType, entityID uuid.UUID) (*uuid.UUID, error) {
	// Try pending first
	pending, err := s.approvalRepo.GetPendingByEntityID(entityType, entityID)
	if err == nil && pending != nil {
		return &pending.ID, nil
	}
	// Fall back to latest approved
	approved, err := s.approvalRepo.GetLatestApprovedByEntityID(entityType, entityID)
	if err == nil && approved != nil {
		return &approved.ID, nil
	}
	return nil, fmt.Errorf("no approval found")
}
