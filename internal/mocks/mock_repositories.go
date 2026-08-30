package mocks

import (
	"encoding/json"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
)

// Compile-time interface satisfaction checks
var _ repository.APITokenRepositoryInterface = (*MockAPITokenRepository)(nil)
var _ repository.ApprovalPolicyRepositoryInterface = (*MockApprovalPolicyRepository)(nil)
var _ repository.UnifiedApprovalRepositoryInterface = (*MockUnifiedApprovalRepository)(nil)
var _ repository.AuditLogRepositoryInterface = (*MockAuditLogRepository)(nil)
var _ repository.BackendTrafficPolicyRepositoryInterface = (*MockBackendTrafficPolicyRepository)(nil)
var _ repository.ClientAttachmentRepositoryInterface = (*MockClientAttachmentRepository)(nil)
var _ repository.ClientIPRepositoryInterface = (*MockClientIPRepository)(nil)
var _ repository.ClientRepositoryInterface = (*MockClientRepository)(nil)
var _ repository.CommentRepositoryInterface = (*MockCommentRepository)(nil)
var _ repository.DomainRepositoryInterface = (*MockDomainRepository)(nil)
var _ repository.DomainSettingsRepositoryInterface = (*MockDomainSettingsRepository)(nil)
var _ repository.DomainTemplateRepositoryInterface = (*MockDomainTemplateRepository)(nil)
var _ repository.EnvoyExtensionPolicyRepositoryInterface = (*MockEnvoyExtensionPolicyRepository)(nil)
var _ repository.NotificationRepositoryInterface = (*MockNotificationRepository)(nil)
var _ repository.PresetRepositoryInterface = (*MockPresetRepository)(nil)
var _ repository.ProjectNamespaceRepositoryInterface = (*MockProjectNamespaceRepository)(nil)
var _ repository.ProjectRepositoryInterface = (*MockProjectRepository)(nil)
var _ repository.RouteRepositoryInterface = (*MockRouteRepository)(nil)
var _ repository.RouteVersionRepositoryInterface = (*MockRouteVersionRepository)(nil)
var _ repository.SecurityPolicyRepositoryInterface = (*MockSecurityPolicyRepository)(nil)
var _ repository.SSOConfigRepositoryInterface = (*MockSSOConfigRepository)(nil)
var _ repository.SystemSettingsRepositoryInterface = (*MockSystemSettingsRepository)(nil)
var _ repository.TeamEmailInviteRepositoryInterface = (*MockTeamEmailInviteRepository)(nil)
var _ repository.TeamRepositoryInterface = (*MockTeamRepository)(nil)
var _ repository.UserRepositoryInterface = (*MockUserRepository)(nil)
var _ repository.WafPolicyRepositoryInterface = (*MockWafPolicyRepository)(nil)

// ============================================================================
// MockAPITokenRepository
// ============================================================================

type MockAPITokenRepository struct {
	mock.Mock
}

func (m *MockAPITokenRepository) Create(token *models.APIToken) error {
	args := m.Called(token)
	return args.Error(0)
}

func (m *MockAPITokenRepository) GetByID(id uuid.UUID) (*models.APIToken, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.APIToken), args.Error(1)
}

func (m *MockAPITokenRepository) GetByTokenHash(hash string) (*models.APIToken, error) {
	args := m.Called(hash)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.APIToken), args.Error(1)
}

func (m *MockAPITokenRepository) ListByUserID(userID uuid.UUID) ([]models.APIToken, error) {
	args := m.Called(userID)
	return args.Get(0).([]models.APIToken), args.Error(1)
}

func (m *MockAPITokenRepository) CountByUserID(userID uuid.UUID) (int64, error) {
	args := m.Called(userID)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockAPITokenRepository) UpdateLastUsed(id uuid.UUID) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *MockAPITokenRepository) Delete(id uuid.UUID) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *MockAPITokenRepository) DeleteExpired() error {
	args := m.Called()
	return args.Error(0)
}

// ============================================================================
// MockApprovalPolicyRepository
// ============================================================================

type MockApprovalPolicyRepository struct {
	mock.Mock
}

func (m *MockApprovalPolicyRepository) GetByProjectAndEntity(projectID uuid.UUID, entityType string, action *string) (*models.ApprovalPolicy, error) {
	args := m.Called(projectID, entityType, action)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.ApprovalPolicy), args.Error(1)
}

func (m *MockApprovalPolicyRepository) ListByProjectID(projectID uuid.UUID) ([]models.ApprovalPolicy, error) {
	args := m.Called(projectID)
	return args.Get(0).([]models.ApprovalPolicy), args.Error(1)
}

func (m *MockApprovalPolicyRepository) Upsert(policy *models.ApprovalPolicy) error {
	args := m.Called(policy)
	return args.Error(0)
}

func (m *MockApprovalPolicyRepository) GetByID(id uuid.UUID) (*models.ApprovalPolicy, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.ApprovalPolicy), args.Error(1)
}

func (m *MockApprovalPolicyRepository) Create(policy *models.ApprovalPolicy) error {
	args := m.Called(policy)
	return args.Error(0)
}

func (m *MockApprovalPolicyRepository) Update(policy *models.ApprovalPolicy) error {
	args := m.Called(policy)
	return args.Error(0)
}

func (m *MockApprovalPolicyRepository) Delete(id uuid.UUID) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *MockApprovalPolicyRepository) SeedDefaults(projectID uuid.UUID) error {
	args := m.Called(projectID)
	return args.Error(0)
}

// ============================================================================
// MockApprovalStageReviewRepository
// ============================================================================

var _ repository.ApprovalStageReviewRepositoryInterface = (*MockApprovalStageReviewRepository)(nil)

type MockApprovalStageReviewRepository struct {
	mock.Mock
}

func (m *MockApprovalStageReviewRepository) Create(review *models.ApprovalStageReview) error {
	args := m.Called(review)
	return args.Error(0)
}

func (m *MockApprovalStageReviewRepository) CountByStageAndDecision(stageID uuid.UUID, decision string) (int64, error) {
	args := m.Called(stageID, decision)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockApprovalStageReviewRepository) ListByStageID(stageID uuid.UUID) ([]models.ApprovalStageReview, error) {
	args := m.Called(stageID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]models.ApprovalStageReview), args.Error(1)
}

// ============================================================================
// MockUnifiedApprovalRepository
// ============================================================================

type MockUnifiedApprovalRepository struct {
	mock.Mock
}

func (m *MockUnifiedApprovalRepository) Create(approval *models.Approval) error {
	args := m.Called(approval)
	return args.Error(0)
}

func (m *MockUnifiedApprovalRepository) GetByID(id uuid.UUID) (*models.Approval, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Approval), args.Error(1)
}

func (m *MockUnifiedApprovalRepository) Update(approval *models.Approval) error {
	args := m.Called(approval)
	return args.Error(0)
}

func (m *MockUnifiedApprovalRepository) SetAIReview(id uuid.UUID, aiReview json.RawMessage) error {
	args := m.Called(id, aiReview)
	return args.Error(0)
}

func (m *MockUnifiedApprovalRepository) ListByProjectID(projectID uuid.UUID, page, limit int, status, entityType string) ([]models.Approval, int64, error) {
	args := m.Called(projectID, page, limit, status, entityType)
	return args.Get(0).([]models.Approval), args.Get(1).(int64), args.Error(2)
}

func (m *MockUnifiedApprovalRepository) CountPendingByProjectID(projectID uuid.UUID) (int64, error) {
	args := m.Called(projectID)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockUnifiedApprovalRepository) GetPendingByEntityID(entityType models.ApprovalEntityType, entityID uuid.UUID) (*models.Approval, error) {
	args := m.Called(entityType, entityID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Approval), args.Error(1)
}

func (m *MockUnifiedApprovalRepository) GetLatestApprovedByEntityID(entityType models.ApprovalEntityType, entityID uuid.UUID) (*models.Approval, error) {
	args := m.Called(entityType, entityID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Approval), args.Error(1)
}

func (m *MockUnifiedApprovalRepository) DeleteByEntityID(entityType models.ApprovalEntityType, entityID uuid.UUID) error {
	args := m.Called(entityType, entityID)
	return args.Error(0)
}

func (m *MockUnifiedApprovalRepository) CreateStage(stage *models.ApprovalStage) error {
	args := m.Called(stage)
	return args.Error(0)
}

func (m *MockUnifiedApprovalRepository) UpdateStage(stage *models.ApprovalStage) error {
	args := m.Called(stage)
	return args.Error(0)
}

func (m *MockUnifiedApprovalRepository) GetStageByID(id uuid.UUID) (*models.ApprovalStage, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.ApprovalStage), args.Error(1)
}

// ============================================================================
// MockAuditLogRepository
// ============================================================================

type MockAuditLogRepository struct {
	mock.Mock
}

func (m *MockAuditLogRepository) Create(log *models.AuditLog) error {
	args := m.Called(log)
	return args.Error(0)
}

func (m *MockAuditLogRepository) ListByProjectID(projectID uuid.UUID, page, limit int, resourceType, action string, userID *uuid.UUID) ([]models.AuditLog, int64, error) {
	args := m.Called(projectID, page, limit, resourceType, action, userID)
	return args.Get(0).([]models.AuditLog), args.Get(1).(int64), args.Error(2)
}

func (m *MockAuditLogRepository) ExportByProjectID(projectID uuid.UUID, resourceType, action string, userID *uuid.UUID) ([]models.AuditLog, error) {
	args := m.Called(projectID, resourceType, action, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]models.AuditLog), args.Error(1)
}

func (m *MockAuditLogRepository) DeleteOlderThan(projectID uuid.UUID, days int) (int64, error) {
	args := m.Called(projectID, days)
	return args.Get(0).(int64), args.Error(1)
}

// ============================================================================
// MockBackendTrafficPolicyRepository
// ============================================================================

type MockBackendTrafficPolicyRepository struct {
	mock.Mock
}

func (m *MockBackendTrafficPolicyRepository) Create(policy *models.BackendTrafficPolicy) error {
	args := m.Called(policy)
	return args.Error(0)
}

func (m *MockBackendTrafficPolicyRepository) GetByID(id uuid.UUID) (*models.BackendTrafficPolicy, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.BackendTrafficPolicy), args.Error(1)
}

func (m *MockBackendTrafficPolicyRepository) GetByRouteID(routeID uuid.UUID) (*models.BackendTrafficPolicy, error) {
	args := m.Called(routeID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.BackendTrafficPolicy), args.Error(1)
}

func (m *MockBackendTrafficPolicyRepository) GetByDomainID(domainID uuid.UUID) (*models.BackendTrafficPolicy, error) {
	args := m.Called(domainID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.BackendTrafficPolicy), args.Error(1)
}

func (m *MockBackendTrafficPolicyRepository) ListByProjectID(projectID uuid.UUID) ([]models.BackendTrafficPolicy, error) {
	args := m.Called(projectID)
	return args.Get(0).([]models.BackendTrafficPolicy), args.Error(1)
}

func (m *MockBackendTrafficPolicyRepository) Update(policy *models.BackendTrafficPolicy) error {
	args := m.Called(policy)
	return args.Error(0)
}

func (m *MockBackendTrafficPolicyRepository) Delete(id uuid.UUID) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *MockBackendTrafficPolicyRepository) DeleteByRouteID(routeID uuid.UUID) error {
	args := m.Called(routeID)
	return args.Error(0)
}

func (m *MockBackendTrafficPolicyRepository) DeleteByDomainID(domainID uuid.UUID) error {
	args := m.Called(domainID)
	return args.Error(0)
}

func (m *MockBackendTrafficPolicyRepository) ExistsByRouteID(routeID uuid.UUID) (bool, error) {
	args := m.Called(routeID)
	return args.Bool(0), args.Error(1)
}

func (m *MockBackendTrafficPolicyRepository) ExistsByDomainID(domainID uuid.UUID) (bool, error) {
	args := m.Called(domainID)
	return args.Bool(0), args.Error(1)
}

func (m *MockBackendTrafficPolicyRepository) Upsert(policy *models.BackendTrafficPolicy) error {
	args := m.Called(policy)
	return args.Error(0)
}

// ============================================================================
// MockClientAttachmentRepository
// ============================================================================

type MockClientAttachmentRepository struct {
	mock.Mock
}

func (m *MockClientAttachmentRepository) Create(attachment *models.ClientRouteAttachment) error {
	args := m.Called(attachment)
	return args.Error(0)
}

func (m *MockClientAttachmentRepository) GetByID(id uuid.UUID) (*models.ClientRouteAttachment, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.ClientRouteAttachment), args.Error(1)
}

func (m *MockClientAttachmentRepository) Update(attachment *models.ClientRouteAttachment) error {
	args := m.Called(attachment)
	return args.Error(0)
}

func (m *MockClientAttachmentRepository) Delete(id uuid.UUID) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *MockClientAttachmentRepository) GetByClientAndRoute(clientID, routeID uuid.UUID) (*models.ClientRouteAttachment, error) {
	args := m.Called(clientID, routeID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.ClientRouteAttachment), args.Error(1)
}

func (m *MockClientAttachmentRepository) ListByClientID(clientID uuid.UUID) ([]models.ClientRouteAttachment, error) {
	args := m.Called(clientID)
	return args.Get(0).([]models.ClientRouteAttachment), args.Error(1)
}

func (m *MockClientAttachmentRepository) ListByRouteID(routeID uuid.UUID) ([]models.ClientRouteAttachment, error) {
	args := m.Called(routeID)
	return args.Get(0).([]models.ClientRouteAttachment), args.Error(1)
}

func (m *MockClientAttachmentRepository) ListActiveByRouteID(routeID uuid.UUID) ([]models.ClientRouteAttachment, error) {
	args := m.Called(routeID)
	return args.Get(0).([]models.ClientRouteAttachment), args.Error(1)
}

func (m *MockClientAttachmentRepository) ListApprovedByRouteID(routeID uuid.UUID) ([]models.ClientRouteAttachment, error) {
	args := m.Called(routeID)
	return args.Get(0).([]models.ClientRouteAttachment), args.Error(1)
}

func (m *MockClientAttachmentRepository) UpdateStatusByRouteID(routeID uuid.UUID, fromStatus, toStatus models.AttachmentStatus) error {
	args := m.Called(routeID, fromStatus, toStatus)
	return args.Error(0)
}

func (m *MockClientAttachmentRepository) CountByClientID(clientID uuid.UUID) (int64, error) {
	args := m.Called(clientID)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockClientAttachmentRepository) ListActiveByClientIDWithIPAllowlist(clientID uuid.UUID) ([]models.ClientRouteAttachment, error) {
	args := m.Called(clientID)
	return args.Get(0).([]models.ClientRouteAttachment), args.Error(1)
}

func (m *MockClientAttachmentRepository) ListActiveByClientIDWithAPIKey(clientID uuid.UUID) ([]models.ClientRouteAttachment, error) {
	args := m.Called(clientID)
	return args.Get(0).([]models.ClientRouteAttachment), args.Error(1)
}

func (m *MockClientAttachmentRepository) ListActiveByClientIDWithJWT(clientID uuid.UUID) ([]models.ClientRouteAttachment, error) {
	args := m.Called(clientID)
	return args.Get(0).([]models.ClientRouteAttachment), args.Error(1)
}

func (m *MockClientAttachmentRepository) CountMTLSAttachmentsByClientID(clientID uuid.UUID) (int64, error) {
	args := m.Called(clientID)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockClientAttachmentRepository) CountMTLSAttachmentsByDomainID(domainID uuid.UUID) (int64, error) {
	args := m.Called(domainID)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockClientAttachmentRepository) GetMTLSClientsForDomain(domainID uuid.UUID) ([]models.Client, error) {
	args := m.Called(domainID)
	return args.Get(0).([]models.Client), args.Error(1)
}

func (m *MockClientAttachmentRepository) ListActiveByClientIDWithMTLS(clientID uuid.UUID) ([]models.ClientRouteAttachment, error) {
	args := m.Called(clientID)
	return args.Get(0).([]models.ClientRouteAttachment), args.Error(1)
}

func (m *MockClientAttachmentRepository) ListActiveByClientIDWithHeaderAuth(clientID uuid.UUID) ([]models.ClientRouteAttachment, error) {
	args := m.Called(clientID)
	return args.Get(0).([]models.ClientRouteAttachment), args.Error(1)
}

// ============================================================================
// MockClientIPRepository
// ============================================================================

type MockClientIPRepository struct {
	mock.Mock
}

func (m *MockClientIPRepository) Create(ip *models.ClientIPAddress) error {
	args := m.Called(ip)
	return args.Error(0)
}

func (m *MockClientIPRepository) GetByID(id uuid.UUID) (*models.ClientIPAddress, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.ClientIPAddress), args.Error(1)
}

func (m *MockClientIPRepository) Delete(id uuid.UUID) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *MockClientIPRepository) ListByClientID(clientID uuid.UUID) ([]models.ClientIPAddress, error) {
	args := m.Called(clientID)
	return args.Get(0).([]models.ClientIPAddress), args.Error(1)
}

func (m *MockClientIPRepository) CountByClientID(clientID uuid.UUID) (int64, error) {
	args := m.Called(clientID)
	return args.Get(0).(int64), args.Error(1)
}

// ============================================================================
// MockClientRepository
// ============================================================================

type MockClientRepository struct {
	mock.Mock
}

func (m *MockClientRepository) Create(client *models.Client) error {
	args := m.Called(client)
	return args.Error(0)
}

func (m *MockClientRepository) GetByID(id uuid.UUID) (*models.Client, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Client), args.Error(1)
}

func (m *MockClientRepository) Update(client *models.Client) error {
	args := m.Called(client)
	return args.Error(0)
}

func (m *MockClientRepository) Delete(id uuid.UUID) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *MockClientRepository) List(page, limit int, teamID *uuid.UUID) ([]models.Client, int64, error) {
	args := m.Called(page, limit, teamID)
	return args.Get(0).([]models.Client), args.Get(1).(int64), args.Error(2)
}

func (m *MockClientRepository) ExistsByName(name string) (bool, error) {
	args := m.Called(name)
	return args.Bool(0), args.Error(1)
}

func (m *MockClientRepository) ExistsByNameExcluding(name string, excludeID uuid.UUID) (bool, error) {
	args := m.Called(name, excludeID)
	return args.Bool(0), args.Error(1)
}

func (m *MockClientRepository) ListByTeamIDs(teamIDs []uuid.UUID) ([]models.Client, error) {
	args := m.Called(teamIDs)
	return args.Get(0).([]models.Client), args.Error(1)
}

// ============================================================================
// MockCommentRepository
// ============================================================================

type MockCommentRepository struct {
	mock.Mock
}

func (m *MockCommentRepository) Create(comment *models.ApprovalComment) error {
	args := m.Called(comment)
	return args.Error(0)
}

func (m *MockCommentRepository) ListByApprovalID(approvalID uuid.UUID) ([]models.ApprovalComment, error) {
	args := m.Called(approvalID)
	return args.Get(0).([]models.ApprovalComment), args.Error(1)
}

func (m *MockCommentRepository) CountByApprovalID(approvalID uuid.UUID) (int64, error) {
	args := m.Called(approvalID)
	return args.Get(0).(int64), args.Error(1)
}

// ============================================================================
// MockDomainRepository
// ============================================================================

type MockDomainRepository struct {
	mock.Mock
}

func (m *MockDomainRepository) Create(domain *models.Domain) error {
	args := m.Called(domain)
	return args.Error(0)
}

func (m *MockDomainRepository) GetByID(id uuid.UUID) (*models.Domain, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Domain), args.Error(1)
}

func (m *MockDomainRepository) GetByIDs(ids []uuid.UUID) ([]models.Domain, error) {
	args := m.Called(ids)
	return args.Get(0).([]models.Domain), args.Error(1)
}

func (m *MockDomainRepository) ListByProjectID(projectID uuid.UUID, page, limit int, search string, status string, labels map[string]string) ([]models.Domain, int64, error) {
	args := m.Called(projectID, page, limit, search, status, labels)
	return args.Get(0).([]models.Domain), args.Get(1).(int64), args.Error(2)
}

func (m *MockDomainRepository) Update(domain *models.Domain) error {
	args := m.Called(domain)
	return args.Error(0)
}

func (m *MockDomainRepository) Delete(id uuid.UUID) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *MockDomainRepository) ExistsByHostname(projectID uuid.UUID, hostname string) (bool, error) {
	args := m.Called(projectID, hostname)
	return args.Bool(0), args.Error(1)
}

func (m *MockDomainRepository) ListByTemplateID(templateID uuid.UUID) ([]models.Domain, error) {
	args := m.Called(templateID)
	return args.Get(0).([]models.Domain), args.Error(1)
}

func (m *MockDomainRepository) CountByProjectID(projectID uuid.UUID) (int, error) {
	args := m.Called(projectID)
	return args.Int(0), args.Error(1)
}

// ============================================================================
// MockDomainSettingsRepository
// ============================================================================

type MockDomainSettingsRepository struct {
	mock.Mock
}

func (m *MockDomainSettingsRepository) Create(settings *models.DomainSettings) error {
	args := m.Called(settings)
	return args.Error(0)
}

func (m *MockDomainSettingsRepository) GetByID(id uuid.UUID) (*models.DomainSettings, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.DomainSettings), args.Error(1)
}

func (m *MockDomainSettingsRepository) GetByDomainID(domainID uuid.UUID) (*models.DomainSettings, error) {
	args := m.Called(domainID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.DomainSettings), args.Error(1)
}

func (m *MockDomainSettingsRepository) ListByProjectID(projectID uuid.UUID) ([]models.DomainSettings, error) {
	args := m.Called(projectID)
	return args.Get(0).([]models.DomainSettings), args.Error(1)
}

func (m *MockDomainSettingsRepository) Update(settings *models.DomainSettings) error {
	args := m.Called(settings)
	return args.Error(0)
}

func (m *MockDomainSettingsRepository) Delete(id uuid.UUID) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *MockDomainSettingsRepository) DeleteByDomainID(domainID uuid.UUID) error {
	args := m.Called(domainID)
	return args.Error(0)
}

func (m *MockDomainSettingsRepository) ExistsByDomainID(domainID uuid.UUID) (bool, error) {
	args := m.Called(domainID)
	return args.Bool(0), args.Error(1)
}

func (m *MockDomainSettingsRepository) Upsert(settings *models.DomainSettings) error {
	args := m.Called(settings)
	return args.Error(0)
}

// ============================================================================
// MockDomainTemplateRepository
// ============================================================================

type MockDomainTemplateRepository struct {
	mock.Mock
}

func (m *MockDomainTemplateRepository) Create(dt *models.DomainTemplate) error {
	args := m.Called(dt)
	return args.Error(0)
}

func (m *MockDomainTemplateRepository) GetByID(id uuid.UUID) (*models.DomainTemplate, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.DomainTemplate), args.Error(1)
}

func (m *MockDomainTemplateRepository) GetByName(projectID uuid.UUID, name string) (*models.DomainTemplate, error) {
	args := m.Called(projectID, name)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.DomainTemplate), args.Error(1)
}

func (m *MockDomainTemplateRepository) ListByProjectID(projectID uuid.UUID, page, limit int) ([]models.DomainTemplate, int64, error) {
	args := m.Called(projectID, page, limit)
	return args.Get(0).([]models.DomainTemplate), args.Get(1).(int64), args.Error(2)
}

func (m *MockDomainTemplateRepository) ListByExposureType(projectID uuid.UUID, exposureType models.ExposureType) ([]models.DomainTemplate, error) {
	args := m.Called(projectID, exposureType)
	return args.Get(0).([]models.DomainTemplate), args.Error(1)
}

func (m *MockDomainTemplateRepository) Update(dt *models.DomainTemplate) error {
	args := m.Called(dt)
	return args.Error(0)
}

func (m *MockDomainTemplateRepository) Delete(id uuid.UUID) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *MockDomainTemplateRepository) ExistsByName(projectID uuid.UUID, name string) (bool, error) {
	args := m.Called(projectID, name)
	return args.Bool(0), args.Error(1)
}

// ============================================================================
// MockEnvoyExtensionPolicyRepository
// ============================================================================

type MockEnvoyExtensionPolicyRepository struct {
	mock.Mock
}

func (m *MockEnvoyExtensionPolicyRepository) Create(policy *models.EnvoyExtensionPolicy) error {
	args := m.Called(policy)
	return args.Error(0)
}

func (m *MockEnvoyExtensionPolicyRepository) GetByID(id uuid.UUID) (*models.EnvoyExtensionPolicy, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.EnvoyExtensionPolicy), args.Error(1)
}

func (m *MockEnvoyExtensionPolicyRepository) GetByRouteID(routeID uuid.UUID) (*models.EnvoyExtensionPolicy, error) {
	args := m.Called(routeID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.EnvoyExtensionPolicy), args.Error(1)
}

func (m *MockEnvoyExtensionPolicyRepository) ListByProjectID(projectID uuid.UUID) ([]models.EnvoyExtensionPolicy, error) {
	args := m.Called(projectID)
	return args.Get(0).([]models.EnvoyExtensionPolicy), args.Error(1)
}

func (m *MockEnvoyExtensionPolicyRepository) Update(policy *models.EnvoyExtensionPolicy) error {
	args := m.Called(policy)
	return args.Error(0)
}

func (m *MockEnvoyExtensionPolicyRepository) Delete(id uuid.UUID) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *MockEnvoyExtensionPolicyRepository) GetByDomainID(domainID uuid.UUID) (*models.EnvoyExtensionPolicy, error) {
	args := m.Called(domainID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.EnvoyExtensionPolicy), args.Error(1)
}

func (m *MockEnvoyExtensionPolicyRepository) DeleteByRouteID(routeID uuid.UUID) error {
	args := m.Called(routeID)
	return args.Error(0)
}

func (m *MockEnvoyExtensionPolicyRepository) DeleteByDomainID(domainID uuid.UUID) error {
	args := m.Called(domainID)
	return args.Error(0)
}

func (m *MockEnvoyExtensionPolicyRepository) ExistsByRouteID(routeID uuid.UUID) (bool, error) {
	args := m.Called(routeID)
	return args.Bool(0), args.Error(1)
}

func (m *MockEnvoyExtensionPolicyRepository) ExistsByDomainID(domainID uuid.UUID) (bool, error) {
	args := m.Called(domainID)
	return args.Bool(0), args.Error(1)
}

func (m *MockEnvoyExtensionPolicyRepository) Upsert(policy *models.EnvoyExtensionPolicy) error {
	args := m.Called(policy)
	return args.Error(0)
}

// ============================================================================
// MockNotificationRepository
// ============================================================================

type MockNotificationRepository struct {
	mock.Mock
}

func (m *MockNotificationRepository) Create(notification *models.Notification) error {
	args := m.Called(notification)
	return args.Error(0)
}

func (m *MockNotificationRepository) ListByUserID(userID uuid.UUID, unreadOnly bool, page, limit int) ([]models.Notification, int64, error) {
	args := m.Called(userID, unreadOnly, page, limit)
	return args.Get(0).([]models.Notification), args.Get(1).(int64), args.Error(2)
}

func (m *MockNotificationRepository) CountUnread(userID uuid.UUID) (int64, error) {
	args := m.Called(userID)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockNotificationRepository) MarkAsRead(id uuid.UUID, userID uuid.UUID) error {
	args := m.Called(id, userID)
	return args.Error(0)
}

func (m *MockNotificationRepository) MarkAllAsRead(userID uuid.UUID) error {
	args := m.Called(userID)
	return args.Error(0)
}

// ============================================================================
// MockPresetRepository
// ============================================================================

type MockPresetRepository struct {
	mock.Mock
}

func (m *MockPresetRepository) Create(preset *models.PermissionPreset) error {
	args := m.Called(preset)
	return args.Error(0)
}

func (m *MockPresetRepository) GetByID(id uuid.UUID) (*models.PermissionPreset, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.PermissionPreset), args.Error(1)
}

func (m *MockPresetRepository) GetByProjectAndName(projectID uuid.UUID, name string) (*models.PermissionPreset, error) {
	args := m.Called(projectID, name)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.PermissionPreset), args.Error(1)
}

func (m *MockPresetRepository) ListByProject(projectID uuid.UUID) ([]models.PermissionPreset, error) {
	args := m.Called(projectID)
	return args.Get(0).([]models.PermissionPreset), args.Error(1)
}

func (m *MockPresetRepository) Update(preset *models.PermissionPreset) error {
	args := m.Called(preset)
	return args.Error(0)
}

func (m *MockPresetRepository) Delete(id uuid.UUID) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *MockPresetRepository) IsPresetInUse(presetID uuid.UUID) (bool, error) {
	args := m.Called(presetID)
	return args.Bool(0), args.Error(1)
}

func (m *MockPresetRepository) SeedBuiltinPresets(projectID uuid.UUID) error {
	args := m.Called(projectID)
	return args.Error(0)
}

// ============================================================================
// MockProjectNamespaceRepository
// ============================================================================

type MockProjectNamespaceRepository struct {
	mock.Mock
}

func (m *MockProjectNamespaceRepository) Create(ns *models.ProjectNamespace) error {
	args := m.Called(ns)
	return args.Error(0)
}

func (m *MockProjectNamespaceRepository) GetByID(id uuid.UUID) (*models.ProjectNamespace, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.ProjectNamespace), args.Error(1)
}

func (m *MockProjectNamespaceRepository) GetByProjectAndNamespace(projectID uuid.UUID, namespace string) (*models.ProjectNamespace, error) {
	args := m.Called(projectID, namespace)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.ProjectNamespace), args.Error(1)
}

func (m *MockProjectNamespaceRepository) ListByProjectID(projectID uuid.UUID) ([]models.ProjectNamespace, error) {
	args := m.Called(projectID)
	return args.Get(0).([]models.ProjectNamespace), args.Error(1)
}

func (m *MockProjectNamespaceRepository) ListByCapability(projectID uuid.UUID, capability string) ([]models.ProjectNamespace, error) {
	args := m.Called(projectID, capability)
	return args.Get(0).([]models.ProjectNamespace), args.Error(1)
}

func (m *MockProjectNamespaceRepository) Update(ns *models.ProjectNamespace) error {
	args := m.Called(ns)
	return args.Error(0)
}

func (m *MockProjectNamespaceRepository) Delete(id uuid.UUID) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *MockProjectNamespaceRepository) ExistsByProjectAndNamespace(projectID uuid.UUID, namespace string) (bool, error) {
	args := m.Called(projectID, namespace)
	return args.Bool(0), args.Error(1)
}

// ============================================================================
// MockProjectRepository
// ============================================================================

type MockProjectRepository struct {
	mock.Mock
}

func (m *MockProjectRepository) Create(project *models.Project) error {
	args := m.Called(project)
	return args.Error(0)
}

func (m *MockProjectRepository) GetByID(id uuid.UUID) (*models.Project, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Project), args.Error(1)
}

func (m *MockProjectRepository) GetByIDWithCounts(id uuid.UUID) (*models.Project, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Project), args.Error(1)
}

func (m *MockProjectRepository) List(page, limit int) ([]models.Project, int64, error) {
	args := m.Called(page, limit)
	return args.Get(0).([]models.Project), args.Get(1).(int64), args.Error(2)
}

func (m *MockProjectRepository) ListByUserAccess(userID uuid.UUID, userRole models.UserRole, page, limit int, search string, labels map[string]string) ([]models.Project, int64, error) {
	args := m.Called(userID, userRole, page, limit, search, labels)
	return args.Get(0).([]models.Project), args.Get(1).(int64), args.Error(2)
}

func (m *MockProjectRepository) Update(project *models.Project) error {
	args := m.Called(project)
	return args.Error(0)
}

func (m *MockProjectRepository) Delete(id uuid.UUID) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *MockProjectRepository) AddAdmin(projectID, userID uuid.UUID) error {
	args := m.Called(projectID, userID)
	return args.Error(0)
}

func (m *MockProjectRepository) RemoveAdmin(projectID, userID uuid.UUID) error {
	args := m.Called(projectID, userID)
	return args.Error(0)
}

func (m *MockProjectRepository) ListAdmins(projectID uuid.UUID) ([]models.User, error) {
	args := m.Called(projectID)
	return args.Get(0).([]models.User), args.Error(1)
}

func (m *MockProjectRepository) IsAdmin(projectID, userID uuid.UUID) (bool, error) {
	args := m.Called(projectID, userID)
	return args.Bool(0), args.Error(1)
}

func (m *MockProjectRepository) Count() (int, error) {
	args := m.Called()
	return args.Int(0), args.Error(1)
}

func (m *MockProjectRepository) FindByConnectionType(connectionType string) (*models.Project, error) {
	args := m.Called(connectionType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Project), args.Error(1)
}

// ============================================================================
// MockRouteRepository
// ============================================================================

type MockRouteRepository struct {
	mock.Mock
}

func (m *MockRouteRepository) Create(route *models.Route) error {
	args := m.Called(route)
	return args.Error(0)
}

func (m *MockRouteRepository) GetByID(id uuid.UUID) (*models.Route, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Route), args.Error(1)
}

func (m *MockRouteRepository) GetByIDs(ids []uuid.UUID) ([]models.Route, error) {
	args := m.Called(ids)
	return args.Get(0).([]models.Route), args.Error(1)
}

func (m *MockRouteRepository) GetByIDWithApproval(id uuid.UUID) (*models.Route, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Route), args.Error(1)
}

func (m *MockRouteRepository) ListByDomainID(domainID uuid.UUID, page, limit int, teamID *uuid.UUID, status string, search string, searchField string, labels map[string]string) ([]models.Route, int64, error) {
	args := m.Called(domainID, page, limit, teamID, status, search, searchField, labels)
	return args.Get(0).([]models.Route), args.Get(1).(int64), args.Error(2)
}

func (m *MockRouteRepository) ListByProjectID(projectID uuid.UUID, page, limit int, filters repository.RouteListFilters) ([]models.Route, int64, error) {
	args := m.Called(projectID, page, limit, filters)
	var routes []models.Route
	if v := args.Get(0); v != nil {
		routes = v.([]models.Route)
	}
	return routes, args.Get(1).(int64), args.Error(2)
}

func (m *MockRouteRepository) Update(route *models.Route) error {
	args := m.Called(route)
	return args.Error(0)
}

func (m *MockRouteRepository) Delete(id uuid.UUID) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *MockRouteRepository) ExistsByName(domainID uuid.UUID, name string) (bool, error) {
	args := m.Called(domainID, name)
	return args.Bool(0), args.Error(1)
}

func (m *MockRouteRepository) GetActiveRoutesByDomainID(domainID uuid.UUID) ([]models.Route, error) {
	args := m.Called(domainID)
	return args.Get(0).([]models.Route), args.Error(1)
}

func (m *MockRouteRepository) CountByDomainID(domainID uuid.UUID) (int, error) {
	args := m.Called(domainID)
	return args.Int(0), args.Error(1)
}

// ============================================================================
// MockRouteVersionRepository
// ============================================================================

type MockRouteVersionRepository struct {
	mock.Mock
}

func (m *MockRouteVersionRepository) Create(version *models.RouteVersion) error {
	args := m.Called(version)
	return args.Error(0)
}

func (m *MockRouteVersionRepository) GetByRouteIDAndVersion(routeID uuid.UUID, version int) (*models.RouteVersion, error) {
	args := m.Called(routeID, version)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.RouteVersion), args.Error(1)
}

func (m *MockRouteVersionRepository) ListByRouteID(routeID uuid.UUID, page, limit int) ([]models.RouteVersion, int64, error) {
	args := m.Called(routeID, page, limit)
	return args.Get(0).([]models.RouteVersion), args.Get(1).(int64), args.Error(2)
}

func (m *MockRouteVersionRepository) GetMaxVersion(routeID uuid.UUID) (int, error) {
	args := m.Called(routeID)
	return args.Int(0), args.Error(1)
}

// ============================================================================
// MockSecurityPolicyRepository
// ============================================================================

type MockSecurityPolicyRepository struct {
	mock.Mock
}

func (m *MockSecurityPolicyRepository) Create(policy *models.SecurityPolicy) error {
	args := m.Called(policy)
	return args.Error(0)
}

func (m *MockSecurityPolicyRepository) GetByID(id uuid.UUID) (*models.SecurityPolicy, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.SecurityPolicy), args.Error(1)
}

func (m *MockSecurityPolicyRepository) GetByRouteID(routeID uuid.UUID) (*models.SecurityPolicy, error) {
	args := m.Called(routeID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.SecurityPolicy), args.Error(1)
}

func (m *MockSecurityPolicyRepository) ListByProjectID(projectID uuid.UUID) ([]models.SecurityPolicy, error) {
	args := m.Called(projectID)
	return args.Get(0).([]models.SecurityPolicy), args.Error(1)
}

func (m *MockSecurityPolicyRepository) Update(policy *models.SecurityPolicy) error {
	args := m.Called(policy)
	return args.Error(0)
}

func (m *MockSecurityPolicyRepository) Delete(id uuid.UUID) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *MockSecurityPolicyRepository) DeleteByRouteID(routeID uuid.UUID) error {
	args := m.Called(routeID)
	return args.Error(0)
}

func (m *MockSecurityPolicyRepository) ExistsByRouteID(routeID uuid.UUID) (bool, error) {
	args := m.Called(routeID)
	return args.Bool(0), args.Error(1)
}

func (m *MockSecurityPolicyRepository) Upsert(policy *models.SecurityPolicy) error {
	args := m.Called(policy)
	return args.Error(0)
}

// ============================================================================
// MockSSOConfigRepository
// ============================================================================

type MockSSOConfigRepository struct {
	mock.Mock
}

func (m *MockSSOConfigRepository) Get() (*models.SSOConfig, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.SSOConfig), args.Error(1)
}

func (m *MockSSOConfigRepository) Update(config *models.SSOConfig) error {
	args := m.Called(config)
	return args.Error(0)
}

// ============================================================================
// MockSystemSettingsRepository
// ============================================================================

type MockSystemSettingsRepository struct {
	mock.Mock
}

func (m *MockSystemSettingsRepository) Get() (*models.SystemSettings, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.SystemSettings), args.Error(1)
}

func (m *MockSystemSettingsRepository) Update(settings *models.SystemSettings) error {
	args := m.Called(settings)
	return args.Error(0)
}

// ============================================================================
// MockTeamEmailInviteRepository
// ============================================================================

type MockTeamEmailInviteRepository struct {
	mock.Mock
}

func (m *MockTeamEmailInviteRepository) Create(invite *models.TeamEmailInvite) error {
	args := m.Called(invite)
	return args.Error(0)
}

func (m *MockTeamEmailInviteRepository) GetByEmail(email string) ([]models.TeamEmailInvite, error) {
	args := m.Called(email)
	return args.Get(0).([]models.TeamEmailInvite), args.Error(1)
}

func (m *MockTeamEmailInviteRepository) ListByTeam(teamID uuid.UUID) ([]models.TeamEmailInvite, error) {
	args := m.Called(teamID)
	return args.Get(0).([]models.TeamEmailInvite), args.Error(1)
}

func (m *MockTeamEmailInviteRepository) Delete(id uuid.UUID) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *MockTeamEmailInviteRepository) DeleteByEmail(email string) error {
	args := m.Called(email)
	return args.Error(0)
}

func (m *MockTeamEmailInviteRepository) ListAll() ([]models.TeamEmailInvite, error) {
	args := m.Called()
	return args.Get(0).([]models.TeamEmailInvite), args.Error(1)
}

// ============================================================================
// MockTeamRepository
// ============================================================================

type MockTeamRepository struct {
	mock.Mock
}

func (m *MockTeamRepository) Create(team *models.Team) error {
	args := m.Called(team)
	return args.Error(0)
}

func (m *MockTeamRepository) GetByID(id uuid.UUID) (*models.Team, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Team), args.Error(1)
}

func (m *MockTeamRepository) List() ([]models.Team, error) {
	args := m.Called()
	return args.Get(0).([]models.Team), args.Error(1)
}

func (m *MockTeamRepository) Update(team *models.Team) error {
	args := m.Called(team)
	return args.Error(0)
}

func (m *MockTeamRepository) Delete(id uuid.UUID) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *MockTeamRepository) AddMember(teamID, userID uuid.UUID) error {
	args := m.Called(teamID, userID)
	return args.Error(0)
}

func (m *MockTeamRepository) RemoveMember(teamID, userID uuid.UUID) error {
	args := m.Called(teamID, userID)
	return args.Error(0)
}

func (m *MockTeamRepository) ListMembers(teamID uuid.UUID) ([]models.User, error) {
	args := m.Called(teamID)
	return args.Get(0).([]models.User), args.Error(1)
}

func (m *MockTeamRepository) IsMember(teamID, userID uuid.UUID) (bool, error) {
	args := m.Called(teamID, userID)
	return args.Bool(0), args.Error(1)
}

func (m *MockTeamRepository) GetTeamsByUserID(userID uuid.UUID) ([]models.Team, error) {
	args := m.Called(userID)
	return args.Get(0).([]models.Team), args.Error(1)
}

func (m *MockTeamRepository) AssignTeamToProject(projectID, teamID uuid.UUID, presetIDs []uuid.UUID) error {
	args := m.Called(projectID, teamID, presetIDs)
	return args.Error(0)
}

func (m *MockTeamRepository) UpdateTeamPresets(projectID, teamID uuid.UUID, presetIDs []uuid.UUID) error {
	args := m.Called(projectID, teamID, presetIDs)
	return args.Error(0)
}

func (m *MockTeamRepository) RemoveTeamFromProject(projectID, teamID uuid.UUID) error {
	args := m.Called(projectID, teamID)
	return args.Error(0)
}

func (m *MockTeamRepository) ListProjectTeams(projectID uuid.UUID) ([]models.ProjectTeamRole, error) {
	args := m.Called(projectID)
	return args.Get(0).([]models.ProjectTeamRole), args.Error(1)
}

func (m *MockTeamRepository) GetProjectTeamRole(projectID, teamID uuid.UUID) (*models.ProjectTeamRole, error) {
	args := m.Called(projectID, teamID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.ProjectTeamRole), args.Error(1)
}

func (m *MockTeamRepository) GetUserTeamsInProject(projectID, userID uuid.UUID) ([]models.ProjectTeamRole, error) {
	args := m.Called(projectID, userID)
	return args.Get(0).([]models.ProjectTeamRole), args.Error(1)
}

func (m *MockTeamRepository) HasPermissionInProject(projectID, userID uuid.UUID, perm models.Permission) (bool, error) {
	args := m.Called(projectID, userID, perm)
	return args.Bool(0), args.Error(1)
}

func (m *MockTeamRepository) HasPermissionInAnyProject(userID uuid.UUID, perm models.Permission) (bool, error) {
	args := m.Called(userID, perm)
	return args.Bool(0), args.Error(1)
}

func (m *MockTeamRepository) HasAnyRoleInProject(projectID, userID uuid.UUID) (bool, error) {
	args := m.Called(projectID, userID)
	return args.Bool(0), args.Error(1)
}

func (m *MockTeamRepository) GetUserPermissionsInProject(projectID, userID uuid.UUID) ([]string, error) {
	args := m.Called(projectID, userID)
	return args.Get(0).([]string), args.Error(1)
}

func (m *MockTeamRepository) ListTeamProjects(teamID uuid.UUID) ([]models.ProjectTeamRole, error) {
	args := m.Called(teamID)
	return args.Get(0).([]models.ProjectTeamRole), args.Error(1)
}

// ============================================================================
// MockUserRepository
// ============================================================================

type MockUserRepository struct {
	mock.Mock
}

func (m *MockUserRepository) Create(user *models.User) error {
	args := m.Called(user)
	return args.Error(0)
}

func (m *MockUserRepository) GetByID(id uuid.UUID) (*models.User, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.User), args.Error(1)
}

func (m *MockUserRepository) GetByUsername(username string) (*models.User, error) {
	args := m.Called(username)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.User), args.Error(1)
}

func (m *MockUserRepository) GetByEmail(email string) (*models.User, error) {
	args := m.Called(email)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.User), args.Error(1)
}

func (m *MockUserRepository) List(page, limit int, role string) ([]models.User, int64, error) {
	args := m.Called(page, limit, role)
	return args.Get(0).([]models.User), args.Get(1).(int64), args.Error(2)
}

func (m *MockUserRepository) Update(user *models.User) error {
	args := m.Called(user)
	return args.Error(0)
}

func (m *MockUserRepository) Delete(id uuid.UUID) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *MockUserRepository) GetByProviderSubject(subject string) (*models.User, error) {
	args := m.Called(subject)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.User), args.Error(1)
}

func (m *MockUserRepository) Count() (int, error) {
	args := m.Called()
	return args.Int(0), args.Error(1)
}

// ============================================================================
// MockWafPolicyRepository
// ============================================================================

type MockWafPolicyRepository struct {
	mock.Mock
}

func (m *MockWafPolicyRepository) Create(policy *models.WafPolicy) error {
	args := m.Called(policy)
	return args.Error(0)
}

func (m *MockWafPolicyRepository) GetByID(id uuid.UUID) (*models.WafPolicy, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.WafPolicy), args.Error(1)
}

func (m *MockWafPolicyRepository) GetByRouteID(routeID uuid.UUID) (*models.WafPolicy, error) {
	args := m.Called(routeID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.WafPolicy), args.Error(1)
}

func (m *MockWafPolicyRepository) ListByProjectID(projectID uuid.UUID) ([]models.WafPolicy, error) {
	args := m.Called(projectID)
	return args.Get(0).([]models.WafPolicy), args.Error(1)
}

func (m *MockWafPolicyRepository) Update(policy *models.WafPolicy) error {
	args := m.Called(policy)
	return args.Error(0)
}

func (m *MockWafPolicyRepository) Delete(id uuid.UUID) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *MockWafPolicyRepository) DeleteByRouteID(routeID uuid.UUID) error {
	args := m.Called(routeID)
	return args.Error(0)
}

func (m *MockWafPolicyRepository) ExistsByRouteID(routeID uuid.UUID) (bool, error) {
	args := m.Called(routeID)
	return args.Bool(0), args.Error(1)
}

func (m *MockWafPolicyRepository) Upsert(policy *models.WafPolicy) error {
	args := m.Called(policy)
	return args.Error(0)
}
