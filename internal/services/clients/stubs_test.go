package clients

import (
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
)

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

// metricsTestRouteRepo is a local stub satisfying repository.RouteRepositoryInterface.
// Lives here for the same reason as metricsTestProjectRepo:
// backend/internal/mocks depends on internal/services, so test files in
// package services cannot import internal/mocks without an import cycle.
type metricsTestRouteRepo struct{ mock.Mock }

func (m *metricsTestRouteRepo) Create(route *models.Route) error {
	args := m.Called(route)
	return args.Error(0)
}

func (m *metricsTestRouteRepo) GetByID(id uuid.UUID) (*models.Route, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Route), args.Error(1)
}

func (m *metricsTestRouteRepo) GetByIDs(ids []uuid.UUID) ([]models.Route, error) {
	args := m.Called(ids)
	return args.Get(0).([]models.Route), args.Error(1)
}

func (m *metricsTestRouteRepo) GetByIDWithApproval(id uuid.UUID) (*models.Route, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Route), args.Error(1)
}

func (m *metricsTestRouteRepo) ListByDomainID(domainID uuid.UUID, page, limit int, teamID *uuid.UUID, status string, search string, searchField string, labels map[string]string) ([]models.Route, int64, error) {
	args := m.Called(domainID, page, limit, teamID, status, search, searchField, labels)
	return args.Get(0).([]models.Route), args.Get(1).(int64), args.Error(2)
}

func (m *metricsTestRouteRepo) ListByProjectID(projectID uuid.UUID, page, limit int, filters repository.RouteListFilters) ([]models.Route, int64, error) {
	args := m.Called(projectID, page, limit, filters)
	return args.Get(0).([]models.Route), args.Get(1).(int64), args.Error(2)
}

func (m *metricsTestRouteRepo) Update(route *models.Route) error {
	args := m.Called(route)
	return args.Error(0)
}

func (m *metricsTestRouteRepo) Delete(id uuid.UUID) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *metricsTestRouteRepo) ExistsByName(domainID uuid.UUID, name string) (bool, error) {
	args := m.Called(domainID, name)
	return args.Bool(0), args.Error(1)
}

func (m *metricsTestRouteRepo) GetActiveRoutesByDomainID(domainID uuid.UUID) ([]models.Route, error) {
	args := m.Called(domainID)
	return args.Get(0).([]models.Route), args.Error(1)
}

func (m *metricsTestRouteRepo) CountByDomainID(domainID uuid.UUID) (int, error) {
	args := m.Called(domainID)
	return args.Int(0), args.Error(1)
}
