package services

import (
	"encoding/json"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/fastgateway-dev/backend-v2/internal/routestate"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// To's CONTRACT: it owns route.Status and nothing else.
//
// OnApproved applies the approved config snapshot to the route BEFORE
// transitioning. To does not write on a no-op transition, so if the route
// already sits at the target status the snapshot would be applied in memory
// and thrown away -- silent data loss, and NOT one of the narrowings this
// task set out to make. OnApproved persists explicitly on that path; these
// tests pin it.
// ---------------------------------------------------------------------------

func routeApprovalSnapshotJSON(t *testing.T, rt models.RouteType) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(models.RouteApprovalSnapshot{
		RouteConfig: &models.RouteConfig{RouteType: rt},
	})
	require.NoError(t, err)
	return raw
}

func TestOnApproved_AtTargetStatus_StillPersistsSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name   string
		action models.ApprovalAction
		status models.RouteStatus
	}{
		{"create already approved", models.ApprovalActionCreate, models.RouteStatusApproved},
		{"update already pending_deploy", models.ApprovalActionUpdate, models.RouteStatusPendingDeploy},
	} {
		t.Run(tc.name, func(t *testing.T) {
			routeID := uuid.New()
			route := &models.Route{
				ID:     routeID,
				Status: tc.status,
				Config: models.RouteConfig{RouteType: models.RouteTypeBackend},
			}

			routeRepo := new(metricsTestRouteRepo)
			routeRepo.On("GetByID", routeID).Return(route, nil)
			routeRepo.On("Update", mock.MatchedBy(func(r *models.Route) bool {
				// The snapshot must have landed, and the status must not have
				// moved: this is a no-op transition, not a narrowing.
				return r.ID == routeID && r.Config.RouteType == models.RouteTypeRedirect && r.Status == tc.status
			})).Return(nil)

			svc := newOnApprovedTestService(routeRepo)

			err := svc.OnApproved(&models.Approval{
				ID:             uuid.New(),
				EntityType:     models.ApprovalEntityRoute,
				EntityID:       routeID,
				Action:         tc.action,
				ConfigSnapshot: routeApprovalSnapshotJSON(t, models.RouteTypeRedirect),
			})

			require.NoError(t, err)
			assert.Equal(t, models.RouteTypeRedirect, route.Config.RouteType, "approved config snapshot must be applied")
			assert.Equal(t, tc.status, route.Status, "a no-op transition must not move the status")
			routeRepo.AssertExpectations(t)
		})
	}
}

// The moving case, for contrast: when the status does change, To's own write
// carries the snapshot and OnApproved must not write twice.
func TestOnApproved_MovingStatus_PersistsSnapshotExactlyOnce(t *testing.T) {
	routeID := uuid.New()
	route := &models.Route{
		ID:     routeID,
		Status: models.RouteStatusPendingUpdate,
		Config: models.RouteConfig{RouteType: models.RouteTypeBackend},
	}

	routeRepo := new(metricsTestRouteRepo)
	routeRepo.On("GetByID", routeID).Return(route, nil)
	routeRepo.On("Update", mock.MatchedBy(func(r *models.Route) bool {
		return r.ID == routeID && r.Config.RouteType == models.RouteTypeRedirect &&
			r.Status == models.RouteStatusPendingDeploy
	})).Return(nil).Once()

	svc := newOnApprovedTestService(routeRepo)

	err := svc.OnApproved(&models.Approval{
		ID:             uuid.New(),
		EntityType:     models.ApprovalEntityRoute,
		EntityID:       routeID,
		Action:         models.ApprovalActionUpdate,
		ConfigSnapshot: routeApprovalSnapshotJSON(t, models.RouteTypeRedirect),
	})

	require.NoError(t, err)
	assert.Equal(t, models.RouteStatusPendingDeploy, route.Status)
	routeRepo.AssertExpectations(t)
}

// newOnApprovedTestService builds a RouteService for the OnApproved tests.
//
// Phase 2E Task 2 made all fifteen repositories required constructor
// parameters, and these tests previously passed nil for approvalRepo,
// policyRepo, domainRepo and teamRepo. This package is `package services`, so
// it cannot import internal/mocks (that package imports internal/services), and
// OnApproved touches nothing but routeRepo and the state machine. The struct
// literal is the same escape hatch golden_httproute_test.go and
// golden_policy_test.go already use for receiver-only helpers.
//
// FINDING for Task 9: these two tests relied on the other fourteen
// dependencies being unset. They do not exercise them; the nil arguments were
// convenience, not a degraded path under test.
func newOnApprovedTestService(routeRepo repository.RouteRepositoryInterface) *routeWrite {
	w := &routeWrite{routeRepo: routeRepo}
	w.state = routestate.New(routeRepo)
	return w
}
