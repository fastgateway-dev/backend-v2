// Package approval owns approval stage planning and stage traversal for
// every approvable entity type.
//
// Before Phase 2D this logic existed twice -- once in ApprovalService for
// routes and once in ClientAttachmentService for client attachments -- and
// the copies had drifted: one resolved team scope failing open, the other
// failing closed. This package is the single owner.
//
// It declares its own narrow store interfaces rather than importing
// internal/repository, so the dependency runs one way and the package stays
// testable without a database.
package approval

import (
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
)

// Completer is implemented by the service owning an entity type. The engine
// calls exactly one method when an approval reaches a terminal state.
// Implementations are registered at construction; an unregistered entity
// type is an error, not a no-op.
type Completer interface {
	OnApproved(a *models.Approval) error
	OnRejected(a *models.Approval) error
	OnCancelled(a *models.Approval) error
}

// PolicyStore reads approval policies. action is nil for the default policy.
type PolicyStore interface {
	GetByProjectAndEntity(projectID uuid.UUID, entityType string, action *string) (*models.ApprovalPolicy, error)
}

// TeamLookup resolves team membership and project permissions. The first
// two methods serve stage planning (team_scope resolution); the last two
// serve stage traversal (is this reviewer allowed to act on this stage).
type TeamLookup interface {
	GetUserTeamsInProject(projectID, userID uuid.UUID) ([]models.ProjectTeamRole, error)
	ListProjectTeams(projectID uuid.UUID) ([]models.ProjectTeamRole, error)
	IsMember(teamID, userID uuid.UUID) (bool, error)
	HasPermissionInProject(projectID, userID uuid.UUID, perm models.Permission) (bool, error)
}

// ProjectLookup supplies the self-approval policy and project-admin status.
// It is not optional: the pre-2D route path guarded GetByID behind a nil
// check, which meant an unwired repository silently permitted
// self-approval. (IsAdmin was never nil-guarded in either copy, so both
// would have panicked on the same unwired repository -- see
// traversal-diff.md item 9. A non-optional port removes the question.)
type ProjectLookup interface {
	GetByID(id uuid.UUID) (*models.Project, error)
	IsAdmin(projectID, userID uuid.UUID) (bool, error)
}

// CancelAuthorizer is an OPTIONAL extension of Completer. A completer that
// implements it grants an extra, entity-specific cancellation right on top
// of the engine's own (submitter / owner / project admin).
//
// It exists because the pre-2D ApprovalService.CancelApproval let a member
// of the route's owning team cancel a route approval
// (approval_service.go:527-535), which needs a route lookup the engine has
// no business owning. The engine deliberately does not take a route
// repository; the route completer supplies that check instead.
//
// It returns a bare bool, not (bool, error), because the pre-2D code
// discarded both the route lookup error and the membership lookup error and
// fell through to "not permitted" -- fail closed. Implementations keep that
// contract: any internal failure means false.
type CancelAuthorizer interface {
	CanCancel(a *models.Approval, user *models.User) bool
}

// ApprovalStore persists approvals and their stages. It is the narrow slice
// of repository.UnifiedApprovalRepositoryInterface the engine needs.
type ApprovalStore interface {
	GetByID(id uuid.UUID) (*models.Approval, error)
	Create(a *models.Approval) error
	Update(a *models.Approval) error
	UpdateStage(s *models.ApprovalStage) error
}

// StageReviewStore records individual reviews against a stage, which is how
// MinApprovers > 1 is satisfied.
//
// It is NOT optional. Both pre-2D copies guarded every use behind
// `if s.stageReviewRepo != nil`, so an unwired repository silently
// downgraded every multi-approver stage to a single approver -- a gate that
// widens itself when a dependency is missing, which is exactly what this
// package exists to stop.
type StageReviewStore interface {
	Create(r *models.ApprovalStageReview) error
	ListByStageID(stageID uuid.UUID) ([]models.ApprovalStageReview, error)
	CountByStageAndDecision(stageID uuid.UUID, decision string) (int64, error)
}
