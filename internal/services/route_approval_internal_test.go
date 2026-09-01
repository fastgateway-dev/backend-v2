package services

import (
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
)

// internal/mocks depends on internal/services (for compile-time interface
// checks), so a package-services internal test file cannot import
// internal/mocks without an import cycle -- see the same note in
// metrics_service_test.go. These local stubs mirror
// mocks.MockTeamRepository and mocks.MockApprovalPolicyRepository exactly.

var _ repository.TeamRepositoryInterface = (*routeApprovalTestTeamRepo)(nil)

type routeApprovalTestTeamRepo struct{ mock.Mock }

func (m *routeApprovalTestTeamRepo) Create(team *models.Team) error {
	args := m.Called(team)
	return args.Error(0)
}

func (m *routeApprovalTestTeamRepo) GetByID(id uuid.UUID) (*models.Team, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Team), args.Error(1)
}

func (m *routeApprovalTestTeamRepo) List() ([]models.Team, error) {
	args := m.Called()
	return args.Get(0).([]models.Team), args.Error(1)
}

func (m *routeApprovalTestTeamRepo) Update(team *models.Team) error {
	args := m.Called(team)
	return args.Error(0)
}

func (m *routeApprovalTestTeamRepo) Delete(id uuid.UUID) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *routeApprovalTestTeamRepo) AddMember(teamID, userID uuid.UUID) error {
	args := m.Called(teamID, userID)
	return args.Error(0)
}

func (m *routeApprovalTestTeamRepo) RemoveMember(teamID, userID uuid.UUID) error {
	args := m.Called(teamID, userID)
	return args.Error(0)
}

func (m *routeApprovalTestTeamRepo) ListMembers(teamID uuid.UUID) ([]models.User, error) {
	args := m.Called(teamID)
	return args.Get(0).([]models.User), args.Error(1)
}

func (m *routeApprovalTestTeamRepo) IsMember(teamID, userID uuid.UUID) (bool, error) {
	args := m.Called(teamID, userID)
	return args.Bool(0), args.Error(1)
}

func (m *routeApprovalTestTeamRepo) GetTeamsByUserID(userID uuid.UUID) ([]models.Team, error) {
	args := m.Called(userID)
	return args.Get(0).([]models.Team), args.Error(1)
}

func (m *routeApprovalTestTeamRepo) AssignTeamToProject(projectID, teamID uuid.UUID, presetIDs []uuid.UUID) error {
	args := m.Called(projectID, teamID, presetIDs)
	return args.Error(0)
}

func (m *routeApprovalTestTeamRepo) UpdateTeamPresets(projectID, teamID uuid.UUID, presetIDs []uuid.UUID) error {
	args := m.Called(projectID, teamID, presetIDs)
	return args.Error(0)
}

func (m *routeApprovalTestTeamRepo) RemoveTeamFromProject(projectID, teamID uuid.UUID) error {
	args := m.Called(projectID, teamID)
	return args.Error(0)
}

func (m *routeApprovalTestTeamRepo) ListProjectTeams(projectID uuid.UUID) ([]models.ProjectTeamRole, error) {
	args := m.Called(projectID)
	return args.Get(0).([]models.ProjectTeamRole), args.Error(1)
}

func (m *routeApprovalTestTeamRepo) GetProjectTeamRole(projectID, teamID uuid.UUID) (*models.ProjectTeamRole, error) {
	args := m.Called(projectID, teamID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.ProjectTeamRole), args.Error(1)
}

func (m *routeApprovalTestTeamRepo) GetUserTeamsInProject(projectID, userID uuid.UUID) ([]models.ProjectTeamRole, error) {
	args := m.Called(projectID, userID)
	return args.Get(0).([]models.ProjectTeamRole), args.Error(1)
}

func (m *routeApprovalTestTeamRepo) HasPermissionInProject(projectID, userID uuid.UUID, perm models.Permission) (bool, error) {
	args := m.Called(projectID, userID, perm)
	return args.Bool(0), args.Error(1)
}

func (m *routeApprovalTestTeamRepo) HasPermissionInAnyProject(userID uuid.UUID, perm models.Permission) (bool, error) {
	args := m.Called(userID, perm)
	return args.Bool(0), args.Error(1)
}

func (m *routeApprovalTestTeamRepo) HasAnyRoleInProject(projectID, userID uuid.UUID) (bool, error) {
	args := m.Called(projectID, userID)
	return args.Bool(0), args.Error(1)
}

func (m *routeApprovalTestTeamRepo) GetUserPermissionsInProject(projectID, userID uuid.UUID) ([]string, error) {
	args := m.Called(projectID, userID)
	return args.Get(0).([]string), args.Error(1)
}

func (m *routeApprovalTestTeamRepo) ListTeamProjects(teamID uuid.UUID) ([]models.ProjectTeamRole, error) {
	args := m.Called(teamID)
	return args.Get(0).([]models.ProjectTeamRole), args.Error(1)
}

var _ repository.ApprovalPolicyRepositoryInterface = (*routeApprovalTestPolicyRepo)(nil)

type routeApprovalTestPolicyRepo struct{ mock.Mock }

func (m *routeApprovalTestPolicyRepo) GetByProjectAndEntity(projectID uuid.UUID, entityType string, action *string) (*models.ApprovalPolicy, error) {
	args := m.Called(projectID, entityType, action)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.ApprovalPolicy), args.Error(1)
}

func (m *routeApprovalTestPolicyRepo) ListByProjectID(projectID uuid.UUID) ([]models.ApprovalPolicy, error) {
	args := m.Called(projectID)
	return args.Get(0).([]models.ApprovalPolicy), args.Error(1)
}

func (m *routeApprovalTestPolicyRepo) Create(policy *models.ApprovalPolicy) error {
	args := m.Called(policy)
	return args.Error(0)
}

func (m *routeApprovalTestPolicyRepo) Update(policy *models.ApprovalPolicy) error {
	args := m.Called(policy)
	return args.Error(0)
}

func (m *routeApprovalTestPolicyRepo) Upsert(policy *models.ApprovalPolicy) error {
	args := m.Called(policy)
	return args.Error(0)
}

func (m *routeApprovalTestPolicyRepo) Delete(id uuid.UUID) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *routeApprovalTestPolicyRepo) GetByID(id uuid.UUID) (*models.ApprovalPolicy, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.ApprovalPolicy), args.Error(1)
}

func (m *routeApprovalTestPolicyRepo) SeedDefaults(projectID uuid.UUID) error {
	args := m.Called(projectID)
	return args.Error(0)
}

// The tests for RouteService.resolveTeamScope and
// RouteService.buildRouteApprovalStages that lived here are gone with the
// functions themselves (Phase 2D Task 7). The same behaviour is now pinned
// against the single surviving implementation in
// internal/approval/planning_test.go. The stubs above stay: they are shared
// with approval_characterization_test.go.
