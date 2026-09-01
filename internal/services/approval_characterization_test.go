package services

// CHARACTERIZATION (spent). This file pinned what ApprovalService and
// ClientAttachmentService did before Phase 2D's Tasks 5-8 collapsed them
// into one engine. Task 7 removed the TestAS_* tests and Task 8 the
// TestCAS_* ones: both services' ApproveStage/RejectStage are now pure
// delegation to internal/approval, and every behaviour these tests pinned
// is pinned against the engine itself. Each removal below names its
// surviving counterpart, and says which of the two pre-2D variants SS4.4
// resolved the divergence in favour of.
//
// What remains here are the local repository stubs, which
// client_cascade_test.go still uses (approvalCharTestAttachmentRepo) and
// which Tasks 9-11 are expected to reuse.
//
// internal/mocks depends on internal/services (mock_services.go:12,
// mock_topology_service.go:6 both import internal/services for
// compile-time interface checks), so a package-services internal test
// file cannot import internal/mocks without an import cycle -- see the
// same note in route_approval_internal_test.go and
// metrics_service_test.go. The stubs below mirror the relevant
// internal/mocks types method-for-method. TeamRepositoryInterface is not
// redefined here: routeApprovalTestTeamRepo from
// route_approval_internal_test.go already implements it and lives in
// this same package.

import (
	"encoding/json"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
)

// ---------------------------------------------------------------------
// Local stubs (package services cannot import internal/mocks -- see the
// package comment above).
// ---------------------------------------------------------------------

var _ repository.UnifiedApprovalRepositoryInterface = (*approvalCharTestApprovalRepo)(nil)

type approvalCharTestApprovalRepo struct{ mock.Mock }

func (m *approvalCharTestApprovalRepo) Create(approval *models.Approval) error {
	args := m.Called(approval)
	return args.Error(0)
}

func (m *approvalCharTestApprovalRepo) GetByID(id uuid.UUID) (*models.Approval, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Approval), args.Error(1)
}

func (m *approvalCharTestApprovalRepo) Update(approval *models.Approval) error {
	args := m.Called(approval)
	return args.Error(0)
}

func (m *approvalCharTestApprovalRepo) SetAIReview(id uuid.UUID, aiReview json.RawMessage) error {
	args := m.Called(id, aiReview)
	return args.Error(0)
}

func (m *approvalCharTestApprovalRepo) ListByProjectID(projectID uuid.UUID, page, limit int, status, entityType string) ([]models.Approval, int64, error) {
	args := m.Called(projectID, page, limit, status, entityType)
	return args.Get(0).([]models.Approval), args.Get(1).(int64), args.Error(2)
}

func (m *approvalCharTestApprovalRepo) CountPendingByProjectID(projectID uuid.UUID) (int64, error) {
	args := m.Called(projectID)
	return args.Get(0).(int64), args.Error(1)
}

func (m *approvalCharTestApprovalRepo) GetPendingByEntityID(entityType models.ApprovalEntityType, entityID uuid.UUID) (*models.Approval, error) {
	args := m.Called(entityType, entityID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Approval), args.Error(1)
}

func (m *approvalCharTestApprovalRepo) GetLatestApprovedByEntityID(entityType models.ApprovalEntityType, entityID uuid.UUID) (*models.Approval, error) {
	args := m.Called(entityType, entityID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Approval), args.Error(1)
}

func (m *approvalCharTestApprovalRepo) DeleteByEntityID(entityType models.ApprovalEntityType, entityID uuid.UUID) error {
	args := m.Called(entityType, entityID)
	return args.Error(0)
}

func (m *approvalCharTestApprovalRepo) CreateStage(stage *models.ApprovalStage) error {
	args := m.Called(stage)
	return args.Error(0)
}

func (m *approvalCharTestApprovalRepo) UpdateStage(stage *models.ApprovalStage) error {
	args := m.Called(stage)
	return args.Error(0)
}

func (m *approvalCharTestApprovalRepo) GetStageByID(id uuid.UUID) (*models.ApprovalStage, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.ApprovalStage), args.Error(1)
}

var _ repository.ProjectRepositoryInterface = (*approvalCharTestProjectRepo)(nil)

type approvalCharTestProjectRepo struct{ mock.Mock }

func (m *approvalCharTestProjectRepo) Create(project *models.Project) error {
	args := m.Called(project)
	return args.Error(0)
}

func (m *approvalCharTestProjectRepo) GetByID(id uuid.UUID) (*models.Project, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Project), args.Error(1)
}

func (m *approvalCharTestProjectRepo) GetByIDWithCounts(id uuid.UUID) (*models.Project, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Project), args.Error(1)
}

func (m *approvalCharTestProjectRepo) List(page, limit int) ([]models.Project, int64, error) {
	args := m.Called(page, limit)
	return args.Get(0).([]models.Project), args.Get(1).(int64), args.Error(2)
}

func (m *approvalCharTestProjectRepo) ListByUserAccess(userID uuid.UUID, userRole models.UserRole, page, limit int, search string, labels map[string]string) ([]models.Project, int64, error) {
	args := m.Called(userID, userRole, page, limit, search, labels)
	return args.Get(0).([]models.Project), args.Get(1).(int64), args.Error(2)
}

func (m *approvalCharTestProjectRepo) Update(project *models.Project) error {
	args := m.Called(project)
	return args.Error(0)
}

func (m *approvalCharTestProjectRepo) Delete(id uuid.UUID) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *approvalCharTestProjectRepo) AddAdmin(projectID, userID uuid.UUID) error {
	args := m.Called(projectID, userID)
	return args.Error(0)
}

func (m *approvalCharTestProjectRepo) RemoveAdmin(projectID, userID uuid.UUID) error {
	args := m.Called(projectID, userID)
	return args.Error(0)
}

func (m *approvalCharTestProjectRepo) ListAdmins(projectID uuid.UUID) ([]models.User, error) {
	args := m.Called(projectID)
	return args.Get(0).([]models.User), args.Error(1)
}

func (m *approvalCharTestProjectRepo) IsAdmin(projectID, userID uuid.UUID) (bool, error) {
	args := m.Called(projectID, userID)
	return args.Bool(0), args.Error(1)
}

func (m *approvalCharTestProjectRepo) Count() (int, error) {
	args := m.Called()
	return args.Int(0), args.Error(1)
}

func (m *approvalCharTestProjectRepo) FindByConnectionType(connectionType string) (*models.Project, error) {
	args := m.Called(connectionType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Project), args.Error(1)
}

var _ repository.ClientAttachmentRepositoryInterface = (*approvalCharTestAttachmentRepo)(nil)

type approvalCharTestAttachmentRepo struct{ mock.Mock }

func (m *approvalCharTestAttachmentRepo) Create(attachment *models.ClientRouteAttachment) error {
	args := m.Called(attachment)
	return args.Error(0)
}

func (m *approvalCharTestAttachmentRepo) GetByID(id uuid.UUID) (*models.ClientRouteAttachment, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.ClientRouteAttachment), args.Error(1)
}

func (m *approvalCharTestAttachmentRepo) Update(attachment *models.ClientRouteAttachment) error {
	args := m.Called(attachment)
	return args.Error(0)
}

func (m *approvalCharTestAttachmentRepo) Delete(id uuid.UUID) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *approvalCharTestAttachmentRepo) GetByClientAndRoute(clientID, routeID uuid.UUID) (*models.ClientRouteAttachment, error) {
	args := m.Called(clientID, routeID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.ClientRouteAttachment), args.Error(1)
}

func (m *approvalCharTestAttachmentRepo) ListByClientID(clientID uuid.UUID) ([]models.ClientRouteAttachment, error) {
	args := m.Called(clientID)
	return args.Get(0).([]models.ClientRouteAttachment), args.Error(1)
}

func (m *approvalCharTestAttachmentRepo) ListByRouteID(routeID uuid.UUID) ([]models.ClientRouteAttachment, error) {
	args := m.Called(routeID)
	return args.Get(0).([]models.ClientRouteAttachment), args.Error(1)
}

func (m *approvalCharTestAttachmentRepo) ListActiveByRouteID(routeID uuid.UUID) ([]models.ClientRouteAttachment, error) {
	args := m.Called(routeID)
	return args.Get(0).([]models.ClientRouteAttachment), args.Error(1)
}

func (m *approvalCharTestAttachmentRepo) ListApprovedByRouteID(routeID uuid.UUID) ([]models.ClientRouteAttachment, error) {
	args := m.Called(routeID)
	return args.Get(0).([]models.ClientRouteAttachment), args.Error(1)
}

func (m *approvalCharTestAttachmentRepo) UpdateStatusByRouteID(routeID uuid.UUID, fromStatus, toStatus models.AttachmentStatus) error {
	args := m.Called(routeID, fromStatus, toStatus)
	return args.Error(0)
}

func (m *approvalCharTestAttachmentRepo) CountByClientID(clientID uuid.UUID) (int64, error) {
	args := m.Called(clientID)
	return args.Get(0).(int64), args.Error(1)
}

func (m *approvalCharTestAttachmentRepo) ListActiveByClientIDWithIPAllowlist(clientID uuid.UUID) ([]models.ClientRouteAttachment, error) {
	args := m.Called(clientID)
	return args.Get(0).([]models.ClientRouteAttachment), args.Error(1)
}

func (m *approvalCharTestAttachmentRepo) ListActiveByClientIDWithAPIKey(clientID uuid.UUID) ([]models.ClientRouteAttachment, error) {
	args := m.Called(clientID)
	return args.Get(0).([]models.ClientRouteAttachment), args.Error(1)
}

func (m *approvalCharTestAttachmentRepo) ListActiveByClientIDWithJWT(clientID uuid.UUID) ([]models.ClientRouteAttachment, error) {
	args := m.Called(clientID)
	return args.Get(0).([]models.ClientRouteAttachment), args.Error(1)
}

func (m *approvalCharTestAttachmentRepo) CountMTLSAttachmentsByClientID(clientID uuid.UUID) (int64, error) {
	args := m.Called(clientID)
	return args.Get(0).(int64), args.Error(1)
}

func (m *approvalCharTestAttachmentRepo) CountMTLSAttachmentsByDomainID(domainID uuid.UUID) (int64, error) {
	args := m.Called(domainID)
	return args.Get(0).(int64), args.Error(1)
}

func (m *approvalCharTestAttachmentRepo) GetMTLSClientsForDomain(domainID uuid.UUID) ([]models.Client, error) {
	args := m.Called(domainID)
	return args.Get(0).([]models.Client), args.Error(1)
}

func (m *approvalCharTestAttachmentRepo) ListActiveByClientIDWithMTLS(clientID uuid.UUID) ([]models.ClientRouteAttachment, error) {
	args := m.Called(clientID)
	return args.Get(0).([]models.ClientRouteAttachment), args.Error(1)
}

func (m *approvalCharTestAttachmentRepo) ListActiveByClientIDWithHeaderAuth(clientID uuid.UUID) ([]models.ClientRouteAttachment, error) {
	args := m.Called(clientID)
	return args.Get(0).([]models.ClientRouteAttachment), args.Error(1)
}

// ===========================================================================
// Task 1: team-scope / stage-planning characterization
// ===========================================================================

// The three TestCAS_ResolveTeamScope_* tests are gone:
// ClientAttachmentService.resolveTeamScope was deleted in Phase 2D Task 8
// and internal/approval.resolveTeamScope is the single implementation.
// Each is pinned there instead:
//
//	TestCAS_ResolveTeamScope_SubmitterTeamRepoErrorErrors
//	  -> internal/approval/planning_test.go TestResolveTeamScope_SubmitterTeamFailsClosed
//	TestCAS_ResolveTeamScope_OtherTeamNoOtherTeamStaysOpen
//	  -> internal/approval/planning_test.go TestResolveTeamScope_OtherTeamNoOtherTeamExistsStaysOpen
//	TestCAS_ResolveTeamScope_EmptyScopeIsUnknown
//	  -> internal/approval/planning_test.go TestResolveTeamScope_AnyAndEmptyMeanNoRestriction,
//	     which asserts the OPPOSITE. This is the one INTENDED behaviour
//	     change of the migration: the client copy had no "" case so an
//	     empty team_scope fell to default and errored, while the route copy
//	     treated "" as "any". SS4.4 resolves it in favour of "any", so ""
//	     now means "no team restriction" on the client path too.

// ===========================================================================
// Task 2: approval traversal characterization (ApproveStage / RejectStage)
// ===========================================================================

// --- Baseline behaviours given by the brief, mirrored on both services ---

// TestAS_ApproveStage_RejectsNonPendingApproval is gone: ApprovalService.ApproveStage/RejectStage are now
// pure delegation to internal/approval, and the behaviour this test
// pinned is pinned against the engine itself by
// internal/approval/traversal_test.go TestEngine_ApproveStage_RejectsNonPendingApproval.
// (Phase 2D Task 7.)

// TestAS_ApproveStage_SubmitterCannotSelfApproveByDefault is gone: ApprovalService.ApproveStage/RejectStage are now
// pure delegation to internal/approval, and the behaviour this test
// pinned is pinned against the engine itself by
// internal/approval/traversal_test.go TestEngine_ApproveStage_SubmitterCannotSelfApproveByDefault.
// (Phase 2D Task 7.)

// TestCAS_ApproveStage_RejectsNonPendingApproval is gone:
// ClientAttachmentService.ApproveStage/RejectStage are now pure delegation
// to internal/approval, and the behaviour this test pinned is pinned
// against the engine itself by internal/approval/traversal_test.go
// TestEngine_ApproveStage_RejectsNonPendingApproval. (Phase 2D Task 8.)

// TestCAS_ApproveStage_SubmitterCannotSelfApproveByDefault is gone; covered
// by internal/approval/traversal_test.go
// TestEngine_ApproveStage_SubmitterCannotSelfApproveByDefault. (Task 8.)

// --- Divergence 1: GetByID error handling (approval_service.go:154-157,
// :303-306 vs client_attachment_service.go:470-472, :566-568). AS propagates
// the underlying repository error verbatim; CAS discards it and always
// returns a generic "approval not found", even for e.g. a connection error. ---

// TestAS_ApproveStage_GetByIDErrorPropagatesRaw is gone: ApprovalService.ApproveStage/RejectStage are now
// pure delegation to internal/approval, and the behaviour this test
// pinned is pinned against the engine itself by
// internal/approval/traversal_test.go TestEngine_ApproveStage_GetByIDErrorPropagatesRaw.
// (Phase 2D Task 7.)

// TestCAS_ApproveStage_GetByIDErrorReplacedWithGenericMessage is gone.
// Divergence 1 is resolved in favour of ApprovalService: the repository
// error is now propagated verbatim instead of being masked as the generic
// "approval not found". Pinned by internal/approval/traversal_test.go
// TestEngine_ApproveStage_GetByIDErrorPropagatesRaw, which names this test
// as the counterpart deliberately not carried over. (Task 8.)

// --- Divergence 2: self-approval check runs before stage lookup in CAS,
// after it in AS (approval_service.go:163-174 vs client_attachment_service.go:480,493-494).
// Consequence: a submitter reviewing their own submission with a bogus
// stageID gets "stage not found" from AS but "cannot self-approve" from CAS
// -- CAS never validates the stage exists in that case. ---

// TestAS_ApproveStage_StageNotFoundTakesPriorityOverSelfApproval is gone: ApprovalService.ApproveStage/RejectStage are now
// pure delegation to internal/approval, and the behaviour this test
// pinned is pinned against the engine itself by
// internal/approval/traversal_test.go TestEngine_ApproveStage_StageNotFoundTakesPriorityOverSelfApproval.
// (Phase 2D Task 7.)

// TestCAS_ApproveStage_SelfApprovalCheckedBeforeStageLookup is gone.
// Divergence 2 is resolved in favour of ApprovalService: the stage is
// resolved and validated BEFORE self-review is checked, so a bogus stage ID
// now surfaces on this path too. Pinned by
// internal/approval/traversal_test.go
// TestEngine_ApproveStage_StageNotFoundTakesPriorityOverSelfApproval. (Task 8.)

// --- Divergence 3: permission check is unconditional in AS but skipped
// when RequiredPermission=="" in CAS (approval_service.go:203-209 vs
// client_attachment_service.go:841-855). A stage built with an empty
// RequiredPermission is unreviewable by a non-owner/non-admin under AS
// (the empty-string permission check always fails) but freely reviewable
// under CAS (the check is skipped entirely). ---

// TestAS_ApproveStage_EmptyRequiredPermissionStillDeniesReviewer is gone: ApprovalService.ApproveStage/RejectStage are now
// pure delegation to internal/approval, and the behaviour this test
// pinned is pinned against the engine itself by
// internal/approval/traversal_test.go TestEngine_ApproveStage_EmptyRequiredPermissionStillDeniesReviewer.
// (Phase 2D Task 7.)

// TestCAS_ValidateStageReviewer_EmptyRequiredPermissionSkipsCheck is gone.
// Divergence 3 is resolved in favour of ApprovalService: the permission
// check is now unconditional, so a stage with an empty RequiredPermission is
// unreviewable rather than unguarded. Pinned by
// internal/approval/traversal_test.go
// TestEngine_ApproveStage_EmptyRequiredPermissionStillDeniesReviewer, which
// additionally asserts the lookup actually happened. (Task 8.)

// --- Divergence 4: RejectStage's non-empty-comment requirement
// (approval_service.go:299-301) has no counterpart in CAS
// (client_attachment_service.go:565-634): CAS accepts and stores an empty
// comment without complaint. ---

// TestAS_RejectStage_EmptyCommentErrors is gone: ApprovalService.ApproveStage/RejectStage are now
// pure delegation to internal/approval, and the behaviour this test
// pinned is pinned against the engine itself by
// internal/approval/traversal_test.go TestEngine_RejectStage_EmptyCommentErrors.
// (Phase 2D Task 7.)

// TestCAS_RejectStage_EmptyCommentSucceeds is gone. Divergence 4 is
// resolved in favour of ApprovalService: a rejection comment is now
// REQUIRED on the client path too. Pinned by
// internal/approval/traversal_test.go TestEngine_RejectStage_EmptyCommentErrors.
// (Task 8.)

// --- Divergence 5: post-rejection side-effect errors propagate through AS
// (approval_service.go:400, dispatching to
// ClientAttachmentService.OnApprovalRejected whose own errors surface) but
// are silently swallowed inside CAS's own RejectStage, which inlines the
// same attachment-status update without checking either return value
// (client_attachment_service.go:627-632). ---

// TestCAS_RejectStage_AttachmentUpdateFailureIsSwallowed is gone.
// Divergence 5 is resolved in favour of ApprovalService: the attachment
// update is now ClientAttachmentService.OnRejected's job and its error
// propagates instead of being discarded. Pinned by
// internal/approval/traversal_test.go
// TestEngine_RejectStage_CompleterErrorPropagates, which is this test with
// the assertion inverted. (Task 8.)
