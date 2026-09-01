package services

// CHARACTERIZATION, migrated by Task 11.
//
// Task 4 wrote fifteen tests against the five near-identical
// cascade*ChangeToRoutes functions. Those five collapsed into
// cascadeToAttachedRoutes, which takes the attachment query as a parameter,
// so every test below now drives the single function with the query its
// cascade used. The mapping is recorded in the Task 10+11 report.
//
// Two behaviours deliberately changed and are pinned here rather than in the
// old shape:
//
//   - Errors propagate. The five TestCascade*_UpdateFailureIsSilentToday
//     tests, which existed only to record that a failed write was swallowed,
//     are REPLACED by TestCascadeToAttachedRoutes_ReportsUpdateFailure and
//     TestCascadeToAttachedRoutes_ContinuesAfterOneFailure. Error handling is
//     query-independent, so one pair covers what five copies did.
//   - Active filtering is uniform. cascadeMethodChangeToRoutes was the only
//     copy that filtered attachment.Status in Go; the filter now runs for
//     every query. TestCascadeToAttachedRoutes_SkipsInactiveAttachment pins
//     that it still runs for the unfiltered ListByClientID query.
//
// internal/mocks depends on internal/services (for compile-time interface
// checks), so a package-services internal test file cannot import
// internal/mocks without an import cycle -- see the same note in
// route_approval_internal_test.go and approval_characterization_test.go.
// This file reuses the local stubs already living in this package:
// metricsTestRouteRepo (metrics_service_test.go) satisfies
// repository.RouteRepositoryInterface, and approvalCharTestAttachmentRepo
// (approval_characterization_test.go) satisfies
// repository.ClientAttachmentRepositoryInterface.

import (
	"errors"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// newCascadeTestService wires a ClientService the way SetRouteRepository
// does, including the state machine that is now the sole writer of
// route.Status.
func newCascadeTestService(
	attachRepo repository.ClientAttachmentRepositoryInterface,
	routeRepo repository.RouteRepositoryInterface,
) *ClientService {
	return &ClientService{
		clientAttachmentRepo: attachRepo,
		routeRepo:            routeRepo,
		state:                &routeStateMachine{repo: routeRepo},
	}
}

// ===========================================================================
// IP allowlist cascade -- ListActiveByClientIDWithIPAllowlist
// ===========================================================================

func TestCascadeIPChange_MarksActiveRoutePendingDeploy(t *testing.T) {
	clientID, routeID := uuid.New(), uuid.New()

	attachRepo := new(approvalCharTestAttachmentRepo)
	attachRepo.On("ListActiveByClientIDWithIPAllowlist", clientID).
		Return([]models.ClientRouteAttachment{
			{ClientID: clientID, RouteID: routeID, Status: models.AttachmentStatusActive},
		}, nil)

	routeRepo := new(metricsTestRouteRepo)
	routeRepo.On("GetByID", routeID).
		Return(&models.Route{ID: routeID, Status: models.RouteStatusActive}, nil)
	routeRepo.On("Update", mock.MatchedBy(func(r *models.Route) bool {
		return r.ID == routeID && r.Status == models.RouteStatusPendingDeploy
	})).Return(nil)

	s := newCascadeTestService(attachRepo, routeRepo)

	require.NoError(t, s.cascadeToAttachedRoutes(clientID,
		s.attachmentsWithIPAllowlist, "client ip allowlist changed"))

	routeRepo.AssertExpectations(t)
}

func TestCascadeIPChange_SkipsNonActiveRoute(t *testing.T) {
	clientID, routeID := uuid.New(), uuid.New()

	attachRepo := new(approvalCharTestAttachmentRepo)
	attachRepo.On("ListActiveByClientIDWithIPAllowlist", clientID).
		Return([]models.ClientRouteAttachment{
			{ClientID: clientID, RouteID: routeID, Status: models.AttachmentStatusActive},
		}, nil)

	routeRepo := new(metricsTestRouteRepo)
	routeRepo.On("GetByID", routeID).
		Return(&models.Route{ID: routeID, Status: models.RouteStatusPendingCreate}, nil)

	s := newCascadeTestService(attachRepo, routeRepo)

	require.NoError(t, s.cascadeToAttachedRoutes(clientID,
		s.attachmentsWithIPAllowlist, "client ip allowlist changed"))

	routeRepo.AssertNotCalled(t, "Update", mock.Anything)
}

// ===========================================================================
// Allowed-methods cascade -- ListByClientID (the only unfiltered query)
// ===========================================================================

func TestCascadeMethodChange_MarksActiveRoutePendingDeploy(t *testing.T) {
	clientID, routeID := uuid.New(), uuid.New()

	attachRepo := new(approvalCharTestAttachmentRepo)
	attachRepo.On("ListByClientID", clientID).
		Return([]models.ClientRouteAttachment{
			{ClientID: clientID, RouteID: routeID, Status: models.AttachmentStatusActive},
		}, nil)

	routeRepo := new(metricsTestRouteRepo)
	routeRepo.On("GetByID", routeID).
		Return(&models.Route{ID: routeID, Status: models.RouteStatusActive}, nil)
	routeRepo.On("Update", mock.MatchedBy(func(r *models.Route) bool {
		return r.ID == routeID && r.Status == models.RouteStatusPendingDeploy
	})).Return(nil)

	s := newCascadeTestService(attachRepo, routeRepo)

	require.NoError(t, s.cascadeToAttachedRoutes(clientID,
		s.allAttachments, "client allowed methods changed"))

	routeRepo.AssertExpectations(t)
}

func TestCascadeMethodChange_SkipsNonActiveRoute(t *testing.T) {
	clientID, routeID := uuid.New(), uuid.New()

	attachRepo := new(approvalCharTestAttachmentRepo)
	attachRepo.On("ListByClientID", clientID).
		Return([]models.ClientRouteAttachment{
			{ClientID: clientID, RouteID: routeID, Status: models.AttachmentStatusActive},
		}, nil)

	routeRepo := new(metricsTestRouteRepo)
	routeRepo.On("GetByID", routeID).
		Return(&models.Route{ID: routeID, Status: models.RouteStatusPendingCreate}, nil)

	s := newCascadeTestService(attachRepo, routeRepo)

	require.NoError(t, s.cascadeToAttachedRoutes(clientID,
		s.allAttachments, "client allowed methods changed"))

	routeRepo.AssertNotCalled(t, "Update", mock.Anything)
}

// ===========================================================================
// Header-auth cascade -- ListActiveByClientIDWithHeaderAuth
// ===========================================================================

func TestCascadeHeaderChange_MarksActiveRoutePendingDeploy(t *testing.T) {
	clientID, routeID := uuid.New(), uuid.New()

	attachRepo := new(approvalCharTestAttachmentRepo)
	attachRepo.On("ListActiveByClientIDWithHeaderAuth", clientID).
		Return([]models.ClientRouteAttachment{
			{ClientID: clientID, RouteID: routeID, Status: models.AttachmentStatusActive},
		}, nil)

	routeRepo := new(metricsTestRouteRepo)
	routeRepo.On("GetByID", routeID).
		Return(&models.Route{ID: routeID, Status: models.RouteStatusActive}, nil)
	routeRepo.On("Update", mock.MatchedBy(func(r *models.Route) bool {
		return r.ID == routeID && r.Status == models.RouteStatusPendingDeploy
	})).Return(nil)

	s := newCascadeTestService(attachRepo, routeRepo)

	require.NoError(t, s.cascadeToAttachedRoutes(clientID,
		s.attachmentsWithHeaderAuth, "client header auth changed"))

	routeRepo.AssertExpectations(t)
}

func TestCascadeHeaderChange_SkipsNonActiveRoute(t *testing.T) {
	clientID, routeID := uuid.New(), uuid.New()

	attachRepo := new(approvalCharTestAttachmentRepo)
	attachRepo.On("ListActiveByClientIDWithHeaderAuth", clientID).
		Return([]models.ClientRouteAttachment{
			{ClientID: clientID, RouteID: routeID, Status: models.AttachmentStatusActive},
		}, nil)

	routeRepo := new(metricsTestRouteRepo)
	routeRepo.On("GetByID", routeID).
		Return(&models.Route{ID: routeID, Status: models.RouteStatusPendingCreate}, nil)

	s := newCascadeTestService(attachRepo, routeRepo)

	require.NoError(t, s.cascadeToAttachedRoutes(clientID,
		s.attachmentsWithHeaderAuth, "client header auth changed"))

	routeRepo.AssertNotCalled(t, "Update", mock.Anything)
}

// ===========================================================================
// API key cascade -- ListActiveByClientIDWithAPIKey
// ===========================================================================

func TestCascadeAPIKeyChange_MarksActiveRoutePendingDeploy(t *testing.T) {
	clientID, routeID := uuid.New(), uuid.New()

	attachRepo := new(approvalCharTestAttachmentRepo)
	attachRepo.On("ListActiveByClientIDWithAPIKey", clientID).
		Return([]models.ClientRouteAttachment{
			{ClientID: clientID, RouteID: routeID, Status: models.AttachmentStatusActive},
		}, nil)

	routeRepo := new(metricsTestRouteRepo)
	routeRepo.On("GetByID", routeID).
		Return(&models.Route{ID: routeID, Status: models.RouteStatusActive}, nil)
	routeRepo.On("Update", mock.MatchedBy(func(r *models.Route) bool {
		return r.ID == routeID && r.Status == models.RouteStatusPendingDeploy
	})).Return(nil)

	s := newCascadeTestService(attachRepo, routeRepo)

	require.NoError(t, s.cascadeToAttachedRoutes(clientID,
		s.attachmentsWithAPIKey, "client api key revoked"))

	routeRepo.AssertExpectations(t)
}

func TestCascadeAPIKeyChange_SkipsNonActiveRoute(t *testing.T) {
	clientID, routeID := uuid.New(), uuid.New()

	attachRepo := new(approvalCharTestAttachmentRepo)
	attachRepo.On("ListActiveByClientIDWithAPIKey", clientID).
		Return([]models.ClientRouteAttachment{
			{ClientID: clientID, RouteID: routeID, Status: models.AttachmentStatusActive},
		}, nil)

	routeRepo := new(metricsTestRouteRepo)
	routeRepo.On("GetByID", routeID).
		Return(&models.Route{ID: routeID, Status: models.RouteStatusPendingCreate}, nil)

	s := newCascadeTestService(attachRepo, routeRepo)

	require.NoError(t, s.cascadeToAttachedRoutes(clientID,
		s.attachmentsWithAPIKey, "client api key revoked"))

	routeRepo.AssertNotCalled(t, "Update", mock.Anything)
}

// ===========================================================================
// JWT cascade -- ListActiveByClientIDWithJWT
// ===========================================================================

func TestCascadeJWTChange_MarksActiveRoutePendingDeploy(t *testing.T) {
	clientID, routeID := uuid.New(), uuid.New()

	attachRepo := new(approvalCharTestAttachmentRepo)
	attachRepo.On("ListActiveByClientIDWithJWT", clientID).
		Return([]models.ClientRouteAttachment{
			{ClientID: clientID, RouteID: routeID, Status: models.AttachmentStatusActive},
		}, nil)

	routeRepo := new(metricsTestRouteRepo)
	routeRepo.On("GetByID", routeID).
		Return(&models.Route{ID: routeID, Status: models.RouteStatusActive}, nil)
	routeRepo.On("Update", mock.MatchedBy(func(r *models.Route) bool {
		return r.ID == routeID && r.Status == models.RouteStatusPendingDeploy
	})).Return(nil)

	s := newCascadeTestService(attachRepo, routeRepo)

	require.NoError(t, s.cascadeToAttachedRoutes(clientID,
		s.attachmentsWithJWT, "client jwt removed"))

	routeRepo.AssertExpectations(t)
}

func TestCascadeJWTChange_SkipsNonActiveRoute(t *testing.T) {
	clientID, routeID := uuid.New(), uuid.New()

	attachRepo := new(approvalCharTestAttachmentRepo)
	attachRepo.On("ListActiveByClientIDWithJWT", clientID).
		Return([]models.ClientRouteAttachment{
			{ClientID: clientID, RouteID: routeID, Status: models.AttachmentStatusActive},
		}, nil)

	routeRepo := new(metricsTestRouteRepo)
	routeRepo.On("GetByID", routeID).
		Return(&models.Route{ID: routeID, Status: models.RouteStatusPendingCreate}, nil)

	s := newCascadeTestService(attachRepo, routeRepo)

	require.NoError(t, s.cascadeToAttachedRoutes(clientID,
		s.attachmentsWithJWT, "client jwt removed"))

	routeRepo.AssertNotCalled(t, "Update", mock.Anything)
}

// ===========================================================================
// Behaviour change 1: errors propagate.
//
// These REPLACE the five TestCascade*_UpdateFailureIsSilentToday tests.
// ===========================================================================

func TestCascadeToAttachedRoutes_ReportsUpdateFailure(t *testing.T) {
	clientID, routeID := uuid.New(), uuid.New()

	attachRepo := new(approvalCharTestAttachmentRepo)
	attachRepo.On("ListActiveByClientIDWithAPIKey", clientID).
		Return([]models.ClientRouteAttachment{
			{ClientID: clientID, RouteID: routeID, Status: models.AttachmentStatusActive},
		}, nil)

	routeRepo := new(metricsTestRouteRepo)
	routeRepo.On("GetByID", routeID).
		Return(&models.Route{ID: routeID, Status: models.RouteStatusActive}, nil)
	routeRepo.On("Update", mock.Anything).Return(errors.New("deadlock detected"))

	s := newCascadeTestService(attachRepo, routeRepo)

	err := s.cascadeToAttachedRoutes(clientID, s.attachmentsWithAPIKey, "client api key revoked")

	require.Error(t, err)
	assert.Contains(t, err.Error(), routeID.String(),
		"the error must name the route that failed to update")
	assert.Contains(t, err.Error(), "deadlock detected")
	routeRepo.AssertExpectations(t)
}

func TestCascadeToAttachedRoutes_ContinuesAfterOneFailure(t *testing.T) {
	clientID := uuid.New()
	failID, okID := uuid.New(), uuid.New()

	attachRepo := new(approvalCharTestAttachmentRepo)
	attachRepo.On("ListActiveByClientIDWithAPIKey", clientID).
		Return([]models.ClientRouteAttachment{
			{ClientID: clientID, RouteID: failID, Status: models.AttachmentStatusActive},
			{ClientID: clientID, RouteID: okID, Status: models.AttachmentStatusActive},
		}, nil)

	routeRepo := new(metricsTestRouteRepo)
	routeRepo.On("GetByID", failID).
		Return(&models.Route{ID: failID, Status: models.RouteStatusActive}, nil)
	routeRepo.On("GetByID", okID).
		Return(&models.Route{ID: okID, Status: models.RouteStatusActive}, nil)
	routeRepo.On("Update", mock.MatchedBy(func(r *models.Route) bool { return r.ID == failID })).
		Return(errors.New("deadlock detected"))
	routeRepo.On("Update", mock.MatchedBy(func(r *models.Route) bool { return r.ID == okID })).
		Return(nil)

	s := newCascadeTestService(attachRepo, routeRepo)

	err := s.cascadeToAttachedRoutes(clientID, s.attachmentsWithAPIKey, "client api key revoked")

	require.Error(t, err, "one failure is reported")
	// ...and the second route was still updated: one bad row must not
	// silently skip the rest of the fan-out.
	routeRepo.AssertExpectations(t)
}

func TestCascadeToAttachedRoutes_ReportsGetByIDFailure(t *testing.T) {
	clientID, routeID := uuid.New(), uuid.New()

	attachRepo := new(approvalCharTestAttachmentRepo)
	attachRepo.On("ListActiveByClientIDWithJWT", clientID).
		Return([]models.ClientRouteAttachment{
			{ClientID: clientID, RouteID: routeID, Status: models.AttachmentStatusActive},
		}, nil)

	routeRepo := new(metricsTestRouteRepo)
	routeRepo.On("GetByID", routeID).Return((*models.Route)(nil), errors.New("route gone"))

	s := newCascadeTestService(attachRepo, routeRepo)

	err := s.cascadeToAttachedRoutes(clientID, s.attachmentsWithJWT, "client jwt removed")

	require.Error(t, err)
	assert.Contains(t, err.Error(), routeID.String())
}

func TestCascadeToAttachedRoutes_ReportsListFailure(t *testing.T) {
	clientID := uuid.New()

	attachRepo := new(approvalCharTestAttachmentRepo)
	attachRepo.On("ListActiveByClientIDWithIPAllowlist", clientID).
		Return([]models.ClientRouteAttachment(nil), errors.New("connection refused"))

	routeRepo := new(metricsTestRouteRepo)

	s := newCascadeTestService(attachRepo, routeRepo)

	err := s.cascadeToAttachedRoutes(clientID, s.attachmentsWithIPAllowlist,
		"client ip allowlist changed")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "list attachments")
	routeRepo.AssertNotCalled(t, "GetByID", mock.Anything)
}

// ===========================================================================
// Behaviour change 2: the active-attachment filter is uniform.
// ===========================================================================

func TestCascadeToAttachedRoutes_SkipsInactiveAttachment(t *testing.T) {
	clientID, routeID := uuid.New(), uuid.New()

	attachRepo := new(approvalCharTestAttachmentRepo)
	attachRepo.On("ListByClientID", clientID).
		Return([]models.ClientRouteAttachment{
			{ClientID: clientID, RouteID: routeID, Status: models.AttachmentStatusPendingAttach},
		}, nil)

	routeRepo := new(metricsTestRouteRepo)

	s := newCascadeTestService(attachRepo, routeRepo)

	require.NoError(t, s.cascadeToAttachedRoutes(clientID,
		s.allAttachments, "client allowed methods changed"))

	routeRepo.AssertNotCalled(t, "GetByID", mock.Anything)
}

// TestCascadeToAttachedRoutes_UnwiredRepositoriesAreNotAnError is gone
// (Phase 2E Task 3). It pinned controller ruling R13's guard in
// cascadeToAttachedRoutes, which logged and returned nil when
// clientAttachmentRepo, routeRepo or state was nil. NewClientService now
// panics at construction if ClientAttachmentRepo or RouteRepo is missing, so
// that path can no longer occur; the guard was deleted along with the test
// that pinned it.
