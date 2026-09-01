package mocks

import (
	"context"
	"time"

	"github.com/fastgateway-dev/backend-v2/internal/ai"
	"github.com/fastgateway-dev/backend-v2/internal/cluster"
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Compile-time interface satisfaction checks
var _ services.AIServiceInterface = (*MockAIService)(nil)
var _ services.ApprovalServiceInterface = (*MockApprovalService)(nil)
var _ services.AuditServiceInterface = (*MockAuditService)(nil)
var _ services.AuthServiceInterface = (*MockAuthService)(nil)
var _ services.ClientAttachmentServiceInterface = (*MockClientAttachmentService)(nil)
var _ services.ClientServiceInterface = (*MockClientService)(nil)
var _ services.CommentServiceInterface = (*MockCommentService)(nil)
var _ services.DomainServiceInterface = (*MockDomainService)(nil)
var _ services.DomainTemplateServiceInterface = (*MockDomainTemplateService)(nil)
var _ services.KubernetesServiceInterface = (*MockKubernetesService)(nil)
var _ services.MetricsServiceInterface = (*MockMetricsService)(nil)
var _ services.NotificationServiceInterface = (*MockNotificationService)(nil)
var _ services.PresetServiceInterface = (*MockPresetService)(nil)
var _ services.ProjectNamespaceServiceInterface = (*MockProjectNamespaceService)(nil)
var _ services.ProjectServiceInterface = (*MockProjectService)(nil)
var _ services.RouteServiceInterface = (*MockRouteService)(nil)
var _ services.RouteVersionServiceInterface = (*MockRouteVersionService)(nil)
var _ services.SSOServiceInterface = (*MockSSOService)(nil)
var _ services.SystemSettingsServiceInterface = (*MockSystemSettingsService)(nil)
var _ services.TeamEmailInviteServiceInterface = (*MockTeamEmailInviteService)(nil)
var _ services.TeamServiceInterface = (*MockTeamService)(nil)
var _ services.UserServiceInterface = (*MockUserService)(nil)

// ============================================================================
// MockAIService
// ============================================================================

type MockAIService struct {
	mock.Mock
}

func (m *MockAIService) IsEnabled() bool {
	args := m.Called()
	return args.Bool(0)
}

func (m *MockAIService) GetStatus() ai.AIStatus {
	args := m.Called()
	return args.Get(0).(ai.AIStatus)
}

func (m *MockAIService) Generate(ctx context.Context, userID uuid.UUID, req ai.GenerateRequest) (<-chan ai.StreamChunk, error) {
	args := m.Called(ctx, userID, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(<-chan ai.StreamChunk), args.Error(1)
}

func (m *MockAIService) Review(ctx context.Context, userID uuid.UUID, req ai.ReviewRequest) (*ai.ReviewResult, error) {
	args := m.Called(ctx, userID, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ai.ReviewResult), args.Error(1)
}

func (m *MockAIService) ReviewApproval(ctx context.Context, userID uuid.UUID, approval *models.Approval, diff *services.ApprovalDiffResult) (*ai.ReviewResult, error) {
	args := m.Called(ctx, userID, approval, diff)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ai.ReviewResult), args.Error(1)
}

func (m *MockAIService) Chat(ctx context.Context, userID uuid.UUID, req ai.ChatRequest) (<-chan ai.StreamChunk, error) {
	args := m.Called(ctx, userID, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(<-chan ai.StreamChunk), args.Error(1)
}

func (m *MockAIService) TestAIConfig(ctx context.Context, provider, apiKey, model string, maxTokens int, baseURL string) error {
	args := m.Called(ctx, provider, apiKey, model, maxTokens, baseURL)
	return args.Error(0)
}

// ============================================================================
// MockApprovalService
// ============================================================================

type MockApprovalService struct {
	mock.Mock
}

func (m *MockApprovalService) SetSecurityPolicyRepository(repo repository.SecurityPolicyRepositoryInterface) {
	m.Called(repo)
}

func (m *MockApprovalService) SetBackendTrafficPolicyRepository(repo repository.BackendTrafficPolicyRepositoryInterface) {
	m.Called(repo)
}

func (m *MockApprovalService) SetClientAttachmentService(cas *services.ClientAttachmentService) {
	m.Called(cas)
}

func (m *MockApprovalService) GetByID(id uuid.UUID) (*models.Approval, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Approval), args.Error(1)
}

func (m *MockApprovalService) ListByProjectID(projectID uuid.UUID, page, limit int, status string, entityType string) ([]models.Approval, int64, error) {
	args := m.Called(projectID, page, limit, status, entityType)
	return args.Get(0).([]models.Approval), args.Get(1).(int64), args.Error(2)
}

func (m *MockApprovalService) CountPendingByProjectID(projectID uuid.UUID) (int64, error) {
	args := m.Called(projectID)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockApprovalService) ApproveStage(approvalID, stageID uuid.UUID, reviewer *models.User) (*models.Approval, error) {
	args := m.Called(approvalID, stageID, reviewer)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Approval), args.Error(1)
}

func (m *MockApprovalService) RejectStage(approvalID, stageID uuid.UUID, reviewer *models.User, comment string) (*models.Approval, error) {
	args := m.Called(approvalID, stageID, reviewer, comment)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Approval), args.Error(1)
}

func (m *MockApprovalService) CancelApproval(approvalID uuid.UUID, user *models.User) (*models.Approval, error) {
	args := m.Called(approvalID, user)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Approval), args.Error(1)
}

func (m *MockApprovalService) ListPolicies(projectID uuid.UUID) ([]models.ApprovalPolicy, error) {
	args := m.Called(projectID)
	return args.Get(0).([]models.ApprovalPolicy), args.Error(1)
}

func (m *MockApprovalService) UpsertPolicy(policy *models.ApprovalPolicy) error {
	args := m.Called(policy)
	return args.Error(0)
}

func (m *MockApprovalService) GetDiff(id uuid.UUID) (*services.ApprovalDiffResult, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*services.ApprovalDiffResult), args.Error(1)
}

func (m *MockApprovalService) UpdateAIReview(approval *models.Approval) error {
	args := m.Called(approval)
	return args.Error(0)
}

// ============================================================================
// MockAuditService
// ============================================================================

type MockAuditService struct {
	mock.Mock
}

func (m *MockAuditService) LogAction(projectID *uuid.UUID, user *models.User, action string, resourceType string, resourceID *uuid.UUID, resourceName string, details models.AuditDetails, ipAddress string, userAgent string) error {
	args := m.Called(projectID, user, action, resourceType, resourceID, resourceName, details, ipAddress, userAgent)
	return args.Error(0)
}

func (m *MockAuditService) ListByProjectID(projectID uuid.UUID, page, limit int, resourceType, action string, userID *uuid.UUID) ([]models.AuditLog, int64, error) {
	args := m.Called(projectID, page, limit, resourceType, action, userID)
	return args.Get(0).([]models.AuditLog), args.Get(1).(int64), args.Error(2)
}

func (m *MockAuditService) ExportByProjectID(projectID uuid.UUID, resourceType, action string, userID *uuid.UUID) ([]models.AuditLog, error) {
	args := m.Called(projectID, resourceType, action, userID)
	return args.Get(0).([]models.AuditLog), args.Error(1)
}

func (m *MockAuditService) CleanupOlderThan(projectID uuid.UUID, days int) (int64, error) {
	args := m.Called(projectID, days)
	return args.Get(0).(int64), args.Error(1)
}

// ============================================================================
// MockAuthService
// ============================================================================

type MockAuthService struct {
	mock.Mock
}

func (m *MockAuthService) SetSSOService(sso *services.SSOService) {
	m.Called(sso)
}

func (m *MockAuthService) SetSystemSettingsService(ss *services.SystemSettingsService) {
	m.Called(ss)
}

func (m *MockAuthService) Login(username, password string) (*services.LoginResponse, error) {
	args := m.Called(username, password)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*services.LoginResponse), args.Error(1)
}

func (m *MockAuthService) RefreshToken(refreshToken string) (*services.LoginResponse, error) {
	args := m.Called(refreshToken)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*services.LoginResponse), args.Error(1)
}

func (m *MockAuthService) ValidateToken(tokenString string) (*models.User, error) {
	args := m.Called(tokenString)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.User), args.Error(1)
}

func (m *MockAuthService) ValidateAPIToken(tokenString string) (*models.User, error) {
	args := m.Called(tokenString)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.User), args.Error(1)
}

func (m *MockAuthService) CreateAPIToken(userID uuid.UUID, name string, expiresAt *time.Time) (*models.APIToken, string, error) {
	args := m.Called(userID, name, expiresAt)
	if args.Get(0) == nil {
		return nil, args.String(1), args.Error(2)
	}
	return args.Get(0).(*models.APIToken), args.String(1), args.Error(2)
}

func (m *MockAuthService) ListAPITokens(userID uuid.UUID) ([]models.APIToken, error) {
	args := m.Called(userID)
	return args.Get(0).([]models.APIToken), args.Error(1)
}

func (m *MockAuthService) CountAPITokens(userID uuid.UUID) (int64, error) {
	args := m.Called(userID)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockAuthService) RevokeAPIToken(tokenID, userID uuid.UUID) error {
	args := m.Called(tokenID, userID)
	return args.Error(0)
}

func (m *MockAuthService) ChangePassword(userID uuid.UUID, currentPassword, newPassword string) error {
	args := m.Called(userID, currentPassword, newPassword)
	return args.Error(0)
}

func (m *MockAuthService) GenerateTokensForUser(user *models.User) (string, string, error) {
	args := m.Called(user)
	return args.String(0), args.String(1), args.Error(2)
}

// ============================================================================
// MockClientAttachmentService
// ============================================================================

type MockClientAttachmentService struct {
	mock.Mock
}

func (m *MockClientAttachmentService) SetDomainSettingsRepository(repo repository.DomainSettingsRepositoryInterface) {
	m.Called(repo)
}

func (m *MockClientAttachmentService) AttachFromRoute(routeID uuid.UUID, input *services.AttachFromRouteInput, submittedBy uuid.UUID) (*models.ClientRouteAttachment, error) {
	args := m.Called(routeID, input, submittedBy)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.ClientRouteAttachment), args.Error(1)
}

func (m *MockClientAttachmentService) AttachFromClient(clientID uuid.UUID, input *services.AttachFromClientInput, submittedBy uuid.UUID) (*models.ClientRouteAttachment, error) {
	args := m.Called(clientID, input, submittedBy)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.ClientRouteAttachment), args.Error(1)
}

func (m *MockClientAttachmentService) RequestDetach(attachmentID uuid.UUID, submittedBy uuid.UUID) (*models.ClientRouteAttachment, error) {
	args := m.Called(attachmentID, submittedBy)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.ClientRouteAttachment), args.Error(1)
}

func (m *MockClientAttachmentService) ApproveStage(approvalID, stageID uuid.UUID, reviewer *models.User) (*models.Approval, error) {
	args := m.Called(approvalID, stageID, reviewer)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Approval), args.Error(1)
}

func (m *MockClientAttachmentService) RejectStage(approvalID, stageID uuid.UUID, reviewer *models.User, comment string) (*models.Approval, error) {
	args := m.Called(approvalID, stageID, reviewer, comment)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Approval), args.Error(1)
}

func (m *MockClientAttachmentService) GetApproval(id uuid.UUID) (*models.Approval, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Approval), args.Error(1)
}

func (m *MockClientAttachmentService) ListApprovalsByProjectID(projectID uuid.UUID, page, limit int, status string) ([]models.Approval, int64, error) {
	args := m.Called(projectID, page, limit, status)
	return args.Get(0).([]models.Approval), args.Get(1).(int64), args.Error(2)
}

func (m *MockClientAttachmentService) ListByClientID(clientID uuid.UUID) ([]models.ClientRouteAttachment, error) {
	args := m.Called(clientID)
	return args.Get(0).([]models.ClientRouteAttachment), args.Error(1)
}

func (m *MockClientAttachmentService) ListByRouteID(routeID uuid.UUID) ([]models.ClientRouteAttachment, error) {
	args := m.Called(routeID)
	return args.Get(0).([]models.ClientRouteAttachment), args.Error(1)
}

func (m *MockClientAttachmentService) GetAttachment(id uuid.UUID) (*models.ClientRouteAttachment, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.ClientRouteAttachment), args.Error(1)
}

func (m *MockClientAttachmentService) OnApproved(approval *models.Approval) error {
	args := m.Called(approval)
	return args.Error(0)
}

func (m *MockClientAttachmentService) OnRejected(approval *models.Approval) error {
	args := m.Called(approval)
	return args.Error(0)
}

func (m *MockClientAttachmentService) OnCancelled(approval *models.Approval) error {
	args := m.Called(approval)
	return args.Error(0)
}

// ============================================================================
// MockClientService
// ============================================================================

type MockClientService struct {
	mock.Mock
}

func (m *MockClientService) SetClientAttachmentRepository(repo repository.ClientAttachmentRepositoryInterface) {
	m.Called(repo)
}

func (m *MockClientService) SetRouteRepository(repo repository.RouteRepositoryInterface) {
	m.Called(repo)
}

func (m *MockClientService) SetKubernetesService(k8sService services.KubernetesServiceInterface) {
	m.Called(k8sService)
}

func (m *MockClientService) Create(input *services.CreateClientInput, createdBy uuid.UUID) (*models.Client, error) {
	args := m.Called(input, createdBy)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Client), args.Error(1)
}

func (m *MockClientService) GetByID(id uuid.UUID) (*models.Client, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Client), args.Error(1)
}

func (m *MockClientService) Update(id uuid.UUID, input *services.UpdateClientInput) (*models.Client, error) {
	args := m.Called(id, input)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Client), args.Error(1)
}

func (m *MockClientService) Delete(ctx context.Context, id uuid.UUID) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockClientService) List(page, limit int, teamID *uuid.UUID) ([]models.Client, int64, error) {
	args := m.Called(page, limit, teamID)
	return args.Get(0).([]models.Client), args.Get(1).(int64), args.Error(2)
}

func (m *MockClientService) AddIP(clientID uuid.UUID, input *services.CreateClientIPInput, createdBy uuid.UUID) (*models.ClientIPAddress, error) {
	args := m.Called(clientID, input, createdBy)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.ClientIPAddress), args.Error(1)
}

func (m *MockClientService) RemoveIP(clientID uuid.UUID, ipID uuid.UUID) error {
	args := m.Called(clientID, ipID)
	return args.Error(0)
}

func (m *MockClientService) ListIPs(clientID uuid.UUID) ([]models.ClientIPAddress, error) {
	args := m.Called(clientID)
	return args.Get(0).([]models.ClientIPAddress), args.Error(1)
}

func (m *MockClientService) GenerateAPIKey(ctx context.Context, clientID uuid.UUID, input *services.GenerateAPIKeyInput, createdBy uuid.UUID) (*services.GenerateAPIKeyResponse, error) {
	args := m.Called(ctx, clientID, input, createdBy)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*services.GenerateAPIKeyResponse), args.Error(1)
}

func (m *MockClientService) RevokeAPIKey(ctx context.Context, clientID uuid.UUID) error {
	args := m.Called(ctx, clientID)
	return args.Error(0)
}

func (m *MockClientService) GetAPIKeyForDeploy(ctx context.Context, client *models.Client) (string, error) {
	args := m.Called(ctx, client)
	return args.String(0), args.Error(1)
}

func (m *MockClientService) ConfigureJWT(ctx context.Context, clientID uuid.UUID, input *services.ConfigureJWTInput, createdBy uuid.UUID) (*services.ConfigureJWTResponse, error) {
	args := m.Called(ctx, clientID, input, createdBy)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*services.ConfigureJWTResponse), args.Error(1)
}

func (m *MockClientService) RemoveJWT(ctx context.Context, clientID uuid.UUID) error {
	args := m.Called(ctx, clientID)
	return args.Error(0)
}

func (m *MockClientService) UpdateClientMTLS(ctx context.Context, clientID uuid.UUID, input *services.UpdateClientMTLSInput, updatedBy uuid.UUID) (*models.Client, error) {
	args := m.Called(ctx, clientID, input, updatedBy)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Client), args.Error(1)
}

func (m *MockClientService) AddHeader(clientID uuid.UUID, input *services.CreateClientHeaderInput, createdBy uuid.UUID) (*models.ClientHeader, error) {
	args := m.Called(clientID, input, createdBy)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.ClientHeader), args.Error(1)
}

func (m *MockClientService) RemoveHeader(clientID uuid.UUID, headerID uuid.UUID) error {
	args := m.Called(clientID, headerID)
	return args.Error(0)
}

func (m *MockClientService) ListHeaders(clientID uuid.UUID) ([]models.ClientHeader, error) {
	args := m.Called(clientID)
	return args.Get(0).([]models.ClientHeader), args.Error(1)
}

func (m *MockClientService) SetAllowedMethods(clientID uuid.UUID, methods []string) (*models.Client, error) {
	args := m.Called(clientID, methods)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Client), args.Error(1)
}

// ============================================================================
// MockCommentService
// ============================================================================

type MockCommentService struct {
	mock.Mock
}

func (m *MockCommentService) Create(approvalID uuid.UUID, user *models.User, body string) (*models.ApprovalComment, error) {
	args := m.Called(approvalID, user, body)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.ApprovalComment), args.Error(1)
}

func (m *MockCommentService) ListByApprovalID(approvalID uuid.UUID) ([]models.ApprovalComment, error) {
	args := m.Called(approvalID)
	return args.Get(0).([]models.ApprovalComment), args.Error(1)
}

func (m *MockCommentService) CountByApprovalID(approvalID uuid.UUID) (int64, error) {
	args := m.Called(approvalID)
	return args.Get(0).(int64), args.Error(1)
}

// ============================================================================
// MockDomainService
// ============================================================================

type MockDomainService struct {
	mock.Mock
}

func (m *MockDomainService) SetDomainSettingsRepository(repo repository.DomainSettingsRepositoryInterface) {
	m.Called(repo)
}

func (m *MockDomainService) SetClientAttachmentRepository(repo repository.ClientAttachmentRepositoryInterface) {
	m.Called(repo)
}

func (m *MockDomainService) SetDomainTemplateService(dts *services.DomainTemplateService) {
	m.Called(dts)
}

func (m *MockDomainService) SetAIService(as *services.AIService) {
	m.Called(as)
}

func (m *MockDomainService) SetBackendTrafficPolicyRepository(repo repository.BackendTrafficPolicyRepositoryInterface) {
	m.Called(repo)
}

func (m *MockDomainService) SetEnvoyExtensionPolicyRepository(repo repository.EnvoyExtensionPolicyRepositoryInterface) {
	m.Called(repo)
}

func (m *MockDomainService) Create(projectID uuid.UUID, input *services.CreateDomainInput, createdBy uuid.UUID) (*models.Domain, error) {
	args := m.Called(projectID, input, createdBy)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Domain), args.Error(1)
}

func (m *MockDomainService) GetByID(id uuid.UUID) (*models.Domain, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Domain), args.Error(1)
}

func (m *MockDomainService) ListByProjectID(projectID uuid.UUID, page, limit int, search string, status string, labels map[string]string) ([]models.Domain, int64, error) {
	args := m.Called(projectID, page, limit, search, status, labels)
	return args.Get(0).([]models.Domain), args.Get(1).(int64), args.Error(2)
}

func (m *MockDomainService) Update(id uuid.UUID, input *services.UpdateDomainInput) (*models.Domain, error) {
	args := m.Called(id, input)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Domain), args.Error(1)
}

func (m *MockDomainService) Delete(id uuid.UUID) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *MockDomainService) GetDomainSettings(domainID uuid.UUID) (*models.DomainSettings, error) {
	args := m.Called(domainID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.DomainSettings), args.Error(1)
}

func (m *MockDomainService) UpdateDomainSettings(domainID uuid.UUID, input *services.UpdateDomainSettingsInput) (*models.DomainSettings, error) {
	args := m.Called(domainID, input)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.DomainSettings), args.Error(1)
}

func (m *MockDomainService) GenerateYAMLs(domainID uuid.UUID) (*services.DomainYAMLs, error) {
	args := m.Called(domainID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*services.DomainYAMLs), args.Error(1)
}

func (m *MockDomainService) PreviewCreate(projectID uuid.UUID, input *services.DomainCreatePreviewInput, userID uuid.UUID) (*services.DomainCreatePreviewResult, error) {
	args := m.Called(projectID, input, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*services.DomainCreatePreviewResult), args.Error(1)
}

func (m *MockDomainService) PreviewSettingsChanges(domainID uuid.UUID, input *services.DomainSettingsPreviewInput, userID uuid.UUID) (*services.DomainSettingsPreviewResult, error) {
	args := m.Called(domainID, input, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*services.DomainSettingsPreviewResult), args.Error(1)
}

func (m *MockDomainService) AddDomainMTLSCA(ctx context.Context, domainID uuid.UUID, input *services.AddDomainMTLSCAInput) (*models.DomainSettings, error) {
	args := m.Called(ctx, domainID, input)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.DomainSettings), args.Error(1)
}

func (m *MockDomainService) RemoveDomainMTLSCA(ctx context.Context, domainID uuid.UUID, caID string) (*models.DomainSettings, error) {
	args := m.Called(ctx, domainID, caID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.DomainSettings), args.Error(1)
}

func (m *MockDomainService) SetProjectNamespaceRepository(repo repository.ProjectNamespaceRepositoryInterface) {
	m.Called(repo)
}

func (m *MockDomainService) ListTLSSecrets(ctx context.Context, projectID uuid.UUID, namespace string) (*services.ListTLSSecretsResponse, error) {
	args := m.Called(ctx, projectID, namespace)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*services.ListTLSSecretsResponse), args.Error(1)
}

func (m *MockDomainService) ListAvailableNamespaces(ctx context.Context, projectID uuid.UUID) ([]string, error) {
	args := m.Called(ctx, projectID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]string), args.Error(1)
}

// ============================================================================
// MockDomainTemplateService
// ============================================================================

type MockDomainTemplateService struct {
	mock.Mock
}

func (m *MockDomainTemplateService) Create(projectID uuid.UUID, input *services.CreateDomainTemplateInput, createdBy uuid.UUID) (*models.DomainTemplate, error) {
	args := m.Called(projectID, input, createdBy)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.DomainTemplate), args.Error(1)
}

func (m *MockDomainTemplateService) GetByID(id uuid.UUID) (*models.DomainTemplate, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.DomainTemplate), args.Error(1)
}

func (m *MockDomainTemplateService) GetByName(projectID uuid.UUID, name string) (*models.DomainTemplate, error) {
	args := m.Called(projectID, name)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.DomainTemplate), args.Error(1)
}

func (m *MockDomainTemplateService) ListByProjectID(projectID uuid.UUID, page, limit int) ([]models.DomainTemplate, int64, error) {
	args := m.Called(projectID, page, limit)
	return args.Get(0).([]models.DomainTemplate), args.Get(1).(int64), args.Error(2)
}

func (m *MockDomainTemplateService) Update(id uuid.UUID, input *services.UpdateDomainTemplateInput) (*models.DomainTemplate, error) {
	args := m.Called(id, input)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.DomainTemplate), args.Error(1)
}

func (m *MockDomainTemplateService) Delete(id uuid.UUID) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *MockDomainTemplateService) GetManifests(id uuid.UUID) (*services.DomainTemplateManifests, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*services.DomainTemplateManifests), args.Error(1)
}

func (m *MockDomainTemplateService) PreviewChanges(id uuid.UUID, input *services.UpdateDomainTemplateInput, userID uuid.UUID, opts *services.PreviewChangesOptions) (*services.DomainTemplatePreviewResult, error) {
	args := m.Called(id, input, userID, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*services.DomainTemplatePreviewResult), args.Error(1)
}

func (m *MockDomainTemplateService) PreviewCreate(projectID uuid.UUID, input *services.CreateDomainTemplateInput, userID uuid.UUID, opts *services.PreviewChangesOptions) (*services.DomainTemplateCreatePreviewResult, error) {
	args := m.Called(projectID, input, userID, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*services.DomainTemplateCreatePreviewResult), args.Error(1)
}

// ============================================================================
// MockKubernetesService
// ============================================================================

type MockKubernetesService struct {
	mock.Mock
}

func (m *MockKubernetesService) EnsureNamespace(ctx context.Context, projectID uuid.UUID, namespace string) error {
	args := m.Called(ctx, projectID, namespace)
	return args.Error(0)
}

func (m *MockKubernetesService) CreateGateway(ctx context.Context, projectID uuid.UUID, config *kubernetes.GatewayConfig) error {
	args := m.Called(ctx, projectID, config)
	return args.Error(0)
}

func (m *MockKubernetesService) DeleteGateway(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	args := m.Called(ctx, projectID, namespace, name)
	return args.Error(0)
}

func (m *MockKubernetesService) CreateHTTPRoute(ctx context.Context, projectID uuid.UUID, config *kubernetes.HTTPRouteConfig) error {
	args := m.Called(ctx, projectID, config)
	return args.Error(0)
}

func (m *MockKubernetesService) UpdateHTTPRoute(ctx context.Context, projectID uuid.UUID, config *kubernetes.HTTPRouteConfig) error {
	args := m.Called(ctx, projectID, config)
	return args.Error(0)
}

func (m *MockKubernetesService) DeleteHTTPRoute(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	args := m.Called(ctx, projectID, namespace, name)
	return args.Error(0)
}

func (m *MockKubernetesService) CreateGRPCRoute(ctx context.Context, projectID uuid.UUID, config *kubernetes.GRPCRouteConfig) error {
	args := m.Called(ctx, projectID, config)
	return args.Error(0)
}

func (m *MockKubernetesService) UpdateGRPCRoute(ctx context.Context, projectID uuid.UUID, config *kubernetes.GRPCRouteConfig) error {
	args := m.Called(ctx, projectID, config)
	return args.Error(0)
}

func (m *MockKubernetesService) DeleteGRPCRoute(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	args := m.Called(ctx, projectID, namespace, name)
	return args.Error(0)
}

func (m *MockKubernetesService) CreateSecurityPolicy(ctx context.Context, projectID uuid.UUID, config *kubernetes.SecurityPolicyConfig) error {
	args := m.Called(ctx, projectID, config)
	return args.Error(0)
}

func (m *MockKubernetesService) UpdateSecurityPolicy(ctx context.Context, projectID uuid.UUID, config *kubernetes.SecurityPolicyConfig) error {
	args := m.Called(ctx, projectID, config)
	return args.Error(0)
}

func (m *MockKubernetesService) DeleteSecurityPolicy(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	args := m.Called(ctx, projectID, namespace, name)
	return args.Error(0)
}

func (m *MockKubernetesService) CreateBackendTrafficPolicy(ctx context.Context, projectID uuid.UUID, config *kubernetes.BackendTrafficPolicyConfig) error {
	args := m.Called(ctx, projectID, config)
	return args.Error(0)
}

func (m *MockKubernetesService) UpdateBackendTrafficPolicy(ctx context.Context, projectID uuid.UUID, config *kubernetes.BackendTrafficPolicyConfig) error {
	args := m.Called(ctx, projectID, config)
	return args.Error(0)
}

func (m *MockKubernetesService) DeleteBackendTrafficPolicy(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	args := m.Called(ctx, projectID, namespace, name)
	return args.Error(0)
}

func (m *MockKubernetesService) CreateEnvoyExtensionPolicy(ctx context.Context, projectID uuid.UUID, policy *unstructured.Unstructured) error {
	args := m.Called(ctx, projectID, policy)
	return args.Error(0)
}

func (m *MockKubernetesService) UpdateEnvoyExtensionPolicy(ctx context.Context, projectID uuid.UUID, policy *unstructured.Unstructured) error {
	args := m.Called(ctx, projectID, policy)
	return args.Error(0)
}

func (m *MockKubernetesService) DeleteEnvoyExtensionPolicy(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	args := m.Called(ctx, projectID, namespace, name)
	return args.Error(0)
}

func (m *MockKubernetesService) CreateBackend(ctx context.Context, projectID uuid.UUID, config *kubernetes.BackendConfig) error {
	args := m.Called(ctx, projectID, config)
	return args.Error(0)
}

func (m *MockKubernetesService) UpdateBackend(ctx context.Context, projectID uuid.UUID, config *kubernetes.BackendConfig) error {
	args := m.Called(ctx, projectID, config)
	return args.Error(0)
}

func (m *MockKubernetesService) DeleteBackend(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	args := m.Called(ctx, projectID, namespace, name)
	return args.Error(0)
}

func (m *MockKubernetesService) UpdateBackendUnstructured(ctx context.Context, projectID uuid.UUID, backend *unstructured.Unstructured) error {
	args := m.Called(ctx, projectID, backend)
	return args.Error(0)
}

func (m *MockKubernetesService) DeleteBackendsByRoute(ctx context.Context, projectID uuid.UUID, namespace, routeID string) error {
	args := m.Called(ctx, projectID, namespace, routeID)
	return args.Error(0)
}

func (m *MockKubernetesService) DeleteStaleBackendsByRoute(ctx context.Context, projectID uuid.UUID, namespace, routeID string, expectedNames map[string]bool) error {
	args := m.Called(ctx, projectID, namespace, routeID, expectedNames)
	return args.Error(0)
}

func (m *MockKubernetesService) TestConnection(ctx context.Context, projectID uuid.UUID) (bool, string, error) {
	args := m.Called(ctx, projectID)
	return args.Bool(0), args.String(1), args.Error(2)
}

func (m *MockKubernetesService) ListNamespaces(ctx context.Context, projectID uuid.UUID) ([]string, error) {
	args := m.Called(ctx, projectID)
	return args.Get(0).([]string), args.Error(1)
}

func (m *MockKubernetesService) ListServices(ctx context.Context, projectID uuid.UUID, namespace string) ([]map[string]interface{}, error) {
	args := m.Called(ctx, projectID, namespace)
	return args.Get(0).([]map[string]interface{}), args.Error(1)
}

func (m *MockKubernetesService) ListTLSSecrets(ctx context.Context, projectID uuid.UUID, namespace string) ([]cluster.TLSSecretInfo, error) {
	args := m.Called(ctx, projectID, namespace)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]cluster.TLSSecretInfo), args.Error(1)
}

func (m *MockKubernetesService) ListGatewayClasses(ctx context.Context, projectID uuid.UUID) ([]string, error) {
	args := m.Called(ctx, projectID)
	return args.Get(0).([]string), args.Error(1)
}

func (m *MockKubernetesService) ValidatePrerequisites(ctx context.Context, apiURL, token string) (*cluster.PrerequisiteCheck, error) {
	args := m.Called(ctx, apiURL, token)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*cluster.PrerequisiteCheck), args.Error(1)
}

func (m *MockKubernetesService) CreateGatewayClass(ctx context.Context, projectID uuid.UUID, config *kubernetes.GatewayClassConfig) error {
	args := m.Called(ctx, projectID, config)
	return args.Error(0)
}

func (m *MockKubernetesService) DeleteGatewayClass(ctx context.Context, projectID uuid.UUID, name string) error {
	args := m.Called(ctx, projectID, name)
	return args.Error(0)
}

func (m *MockKubernetesService) CreateEnvoyProxy(ctx context.Context, projectID uuid.UUID, config *kubernetes.EnvoyProxyConfig) error {
	args := m.Called(ctx, projectID, config)
	return args.Error(0)
}

func (m *MockKubernetesService) UpdateEnvoyProxy(ctx context.Context, projectID uuid.UUID, config *kubernetes.EnvoyProxyConfig) error {
	args := m.Called(ctx, projectID, config)
	return args.Error(0)
}

func (m *MockKubernetesService) DeleteEnvoyProxy(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	args := m.Called(ctx, projectID, namespace, name)
	return args.Error(0)
}

func (m *MockKubernetesService) ValidateEnvoyGatewayInstalled(ctx context.Context, projectID uuid.UUID) (bool, string, error) {
	args := m.Called(ctx, projectID)
	return args.Bool(0), args.String(1), args.Error(2)
}

func (m *MockKubernetesService) CreateReferenceGrant(ctx context.Context, projectID uuid.UUID, config *cluster.ReferenceGrantConfig) error {
	args := m.Called(ctx, projectID, config)
	return args.Error(0)
}

func (m *MockKubernetesService) DeleteReferenceGrant(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	args := m.Called(ctx, projectID, namespace, name)
	return args.Error(0)
}

func (m *MockKubernetesService) GetReferenceGrant(ctx context.Context, projectID uuid.UUID, namespace, name string) (*unstructured.Unstructured, error) {
	args := m.Called(ctx, projectID, namespace, name)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*unstructured.Unstructured), args.Error(1)
}

func (m *MockKubernetesService) ReferenceGrantExists(ctx context.Context, projectID uuid.UUID, namespace, name string) (bool, error) {
	args := m.Called(ctx, projectID, namespace, name)
	return args.Bool(0), args.Error(1)
}

func (m *MockKubernetesService) RecreateReferenceGrant(ctx context.Context, projectID uuid.UUID, config *cluster.ReferenceGrantConfig) error {
	args := m.Called(ctx, projectID, config)
	return args.Error(0)
}

func (m *MockKubernetesService) ApplyHTTPRouteFilter(ctx context.Context, projectID uuid.UUID, config *kubernetes.HTTPRouteFilterConfig) error {
	args := m.Called(ctx, projectID, config)
	return args.Error(0)
}

func (m *MockKubernetesService) DeleteHTTPRouteFilter(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	args := m.Called(ctx, projectID, namespace, name)
	return args.Error(0)
}

func (m *MockKubernetesService) ApplyDirectResponseConfigMap(ctx context.Context, projectID uuid.UUID, config *kubernetes.DirectResponseConfigMapConfig) error {
	args := m.Called(ctx, projectID, config)
	return args.Error(0)
}

func (m *MockKubernetesService) DeleteDirectResponseConfigMap(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	args := m.Called(ctx, projectID, namespace, name)
	return args.Error(0)
}

func (m *MockKubernetesService) CreateClientTrafficPolicy(ctx context.Context, projectID uuid.UUID, config *kubernetes.ClientTrafficPolicyConfig) error {
	args := m.Called(ctx, projectID, config)
	return args.Error(0)
}

func (m *MockKubernetesService) UpdateClientTrafficPolicy(ctx context.Context, projectID uuid.UUID, config *kubernetes.ClientTrafficPolicyConfig) error {
	args := m.Called(ctx, projectID, config)
	return args.Error(0)
}

func (m *MockKubernetesService) DeleteClientTrafficPolicy(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	args := m.Called(ctx, projectID, namespace, name)
	return args.Error(0)
}

func (m *MockKubernetesService) GetAPIKeySecretName(clientID uuid.UUID) string {
	args := m.Called(clientID)
	return args.String(0)
}

func (m *MockKubernetesService) CreateAPIKeySecret(ctx context.Context, projectID uuid.UUID, clientID uuid.UUID, apiKey string) error {
	args := m.Called(ctx, projectID, clientID, apiKey)
	return args.Error(0)
}

func (m *MockKubernetesService) GetAPIKeyFromSecret(ctx context.Context, projectID uuid.UUID, clientID uuid.UUID) (string, error) {
	args := m.Called(ctx, projectID, clientID)
	return args.String(0), args.Error(1)
}

func (m *MockKubernetesService) DeleteAPIKeySecret(ctx context.Context, projectID uuid.UUID, clientID uuid.UUID) error {
	args := m.Called(ctx, projectID, clientID)
	return args.Error(0)
}

func (m *MockKubernetesService) CreateOrUpdateSecret(ctx context.Context, projectID uuid.UUID, namespace, name string, data map[string][]byte) error {
	args := m.Called(ctx, projectID, namespace, name, data)
	return args.Error(0)
}

func (m *MockKubernetesService) DeleteSecret(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	args := m.Called(ctx, projectID, namespace, name)
	return args.Error(0)
}

func (m *MockKubernetesService) GetSecretData(ctx context.Context, projectID uuid.UUID, namespace, name, key string) ([]byte, error) {
	args := m.Called(ctx, projectID, namespace, name, key)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]byte), args.Error(1)
}

func (m *MockKubernetesService) IsRateLimitAvailable(ctx context.Context, projectID uuid.UUID) (bool, error) {
	args := m.Called(ctx, projectID)
	return args.Bool(0), args.Error(1)
}

func (m *MockKubernetesService) DeleteStaleAPIKeyResources(ctx context.Context, projectID uuid.UUID, namespace, routeID, baseRouteName string, expectedClientPrefixes map[string]bool) error {
	args := m.Called(ctx, projectID, namespace, routeID, baseRouteName, expectedClientPrefixes)
	return args.Error(0)
}

func (m *MockKubernetesService) DetectVersions(ctx context.Context, projectID uuid.UUID) (*cluster.RawVersions, error) {
	args := m.Called(ctx, projectID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*cluster.RawVersions), args.Error(1)
}

// ============================================================================
// MockNotificationService
// ============================================================================

type MockNotificationService struct {
	mock.Mock
}

func (m *MockNotificationService) List(userID uuid.UUID, unreadOnly bool, page, limit int) ([]models.Notification, int64, error) {
	args := m.Called(userID, unreadOnly, page, limit)
	return args.Get(0).([]models.Notification), args.Get(1).(int64), args.Error(2)
}

func (m *MockNotificationService) CountUnread(userID uuid.UUID) (int64, error) {
	args := m.Called(userID)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockNotificationService) MarkAsRead(notificationID uuid.UUID, userID uuid.UUID) error {
	args := m.Called(notificationID, userID)
	return args.Error(0)
}

func (m *MockNotificationService) MarkAllAsRead(userID uuid.UUID) error {
	args := m.Called(userID)
	return args.Error(0)
}

// ============================================================================
// MockPresetService
// ============================================================================

type MockPresetService struct {
	mock.Mock
}

func (m *MockPresetService) Create(projectID uuid.UUID, input *services.CreatePresetInput) (*models.PermissionPreset, error) {
	args := m.Called(projectID, input)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.PermissionPreset), args.Error(1)
}

func (m *MockPresetService) GetByID(id uuid.UUID) (*models.PermissionPreset, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.PermissionPreset), args.Error(1)
}

func (m *MockPresetService) ListByProject(projectID uuid.UUID) ([]models.PermissionPreset, error) {
	args := m.Called(projectID)
	return args.Get(0).([]models.PermissionPreset), args.Error(1)
}

func (m *MockPresetService) Update(projectID, id uuid.UUID, input *services.UpdatePresetInput) (*models.PermissionPreset, error) {
	args := m.Called(projectID, id, input)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.PermissionPreset), args.Error(1)
}

func (m *MockPresetService) Delete(projectID, id uuid.UUID) error {
	args := m.Called(projectID, id)
	return args.Error(0)
}

func (m *MockPresetService) SeedBuiltinPresets(projectID uuid.UUID) error {
	args := m.Called(projectID)
	return args.Error(0)
}

// ============================================================================
// MockProjectNamespaceService
// ============================================================================

type MockProjectNamespaceService struct {
	mock.Mock
}

func (m *MockProjectNamespaceService) Create(projectID uuid.UUID, input *services.CreateProjectNamespaceInput) (*models.ProjectNamespace, error) {
	args := m.Called(projectID, input)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.ProjectNamespace), args.Error(1)
}

func (m *MockProjectNamespaceService) Update(id uuid.UUID, input *services.UpdateProjectNamespaceInput) (*models.ProjectNamespace, error) {
	args := m.Called(id, input)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.ProjectNamespace), args.Error(1)
}

func (m *MockProjectNamespaceService) ListByCapability(projectID uuid.UUID, capability string) ([]models.ProjectNamespace, error) {
	args := m.Called(projectID, capability)
	return args.Get(0).([]models.ProjectNamespace), args.Error(1)
}

func (m *MockProjectNamespaceService) GetByID(id uuid.UUID) (*models.ProjectNamespace, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.ProjectNamespace), args.Error(1)
}

func (m *MockProjectNamespaceService) GetByProjectAndNamespace(projectID uuid.UUID, namespace string) (*models.ProjectNamespace, error) {
	args := m.Called(projectID, namespace)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.ProjectNamespace), args.Error(1)
}

func (m *MockProjectNamespaceService) ListByProjectID(projectID uuid.UUID) ([]models.ProjectNamespace, error) {
	args := m.Called(projectID)
	return args.Get(0).([]models.ProjectNamespace), args.Error(1)
}

func (m *MockProjectNamespaceService) Delete(id uuid.UUID) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *MockProjectNamespaceService) IsNamespaceManaged(projectID uuid.UUID, namespace string) (bool, error) {
	args := m.Called(projectID, namespace)
	return args.Bool(0), args.Error(1)
}

func (m *MockProjectNamespaceService) EnsureReferenceGrant(id uuid.UUID) error {
	args := m.Called(id)
	return args.Error(0)
}

// ============================================================================
// MockProjectService
// ============================================================================

type MockProjectService struct {
	mock.Mock
}

func (m *MockProjectService) SetKubernetesService(k8sService services.KubernetesServiceInterface) {
	m.Called(k8sService)
}

func (m *MockProjectService) SetApprovalPolicyRepository(repo repository.ApprovalPolicyRepositoryInterface) {
	m.Called(repo)
}

func (m *MockProjectService) SetPresetRepository(repo repository.PresetRepositoryInterface) {
	m.Called(repo)
}

func (m *MockProjectService) Create(input *services.CreateProjectInput, createdBy uuid.UUID) (*models.Project, error) {
	args := m.Called(input, createdBy)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Project), args.Error(1)
}

func (m *MockProjectService) GetByID(id uuid.UUID) (*models.Project, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Project), args.Error(1)
}

func (m *MockProjectService) List(userID uuid.UUID, userRole models.UserRole, page, limit int, search string, labels map[string]string) ([]models.Project, int64, error) {
	args := m.Called(userID, userRole, page, limit, search, labels)
	return args.Get(0).([]models.Project), args.Get(1).(int64), args.Error(2)
}

func (m *MockProjectService) Update(id uuid.UUID, input *services.UpdateProjectInput) (*models.Project, error) {
	args := m.Called(id, input)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Project), args.Error(1)
}

func (m *MockProjectService) Delete(id uuid.UUID) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *MockProjectService) TestConnection(id uuid.UUID) (bool, string, string, error) {
	args := m.Called(id)
	return args.Bool(0), args.String(1), args.String(2), args.Error(3)
}

func (m *MockProjectService) GetDecryptedToken(id uuid.UUID) (string, error) {
	args := m.Called(id)
	return args.String(0), args.Error(1)
}

func (m *MockProjectService) GetDecryptedClientKey(id uuid.UUID) (string, error) {
	args := m.Called(id)
	return args.String(0), args.Error(1)
}

func (m *MockProjectService) AddAdmin(projectID, userID uuid.UUID) error {
	args := m.Called(projectID, userID)
	return args.Error(0)
}

func (m *MockProjectService) RemoveAdmin(projectID, userID uuid.UUID) error {
	args := m.Called(projectID, userID)
	return args.Error(0)
}

func (m *MockProjectService) ListAdmins(projectID uuid.UUID) ([]models.User, error) {
	args := m.Called(projectID)
	return args.Get(0).([]models.User), args.Error(1)
}

func (m *MockProjectService) IsAdmin(projectID, userID uuid.UUID) (bool, error) {
	args := m.Called(projectID, userID)
	return args.Bool(0), args.Error(1)
}

// ============================================================================
// MockRouteService
// ============================================================================

type MockRouteService struct {
	mock.Mock
}

func (m *MockRouteService) SetKubernetesService(k8sService services.KubernetesServiceInterface) {
	m.Called(k8sService)
}

func (m *MockRouteService) SetApprovalPolicyRepository(repo repository.ApprovalPolicyRepositoryInterface) {
	m.Called(repo)
}

func (m *MockRouteService) SetProjectNamespaceRepository(repo repository.ProjectNamespaceRepositoryInterface) {
	m.Called(repo)
}

func (m *MockRouteService) SetSecurityPolicyRepository(repo repository.SecurityPolicyRepositoryInterface) {
	m.Called(repo)
}

func (m *MockRouteService) SetBackendTrafficPolicyRepository(repo repository.BackendTrafficPolicyRepositoryInterface) {
	m.Called(repo)
}

func (m *MockRouteService) SetEnvoyExtensionPolicyRepository(repo repository.EnvoyExtensionPolicyRepositoryInterface) {
	m.Called(repo)
}

func (m *MockRouteService) SetWafPolicyRepository(repo repository.WafPolicyRepositoryInterface) {
	m.Called(repo)
}

func (m *MockRouteService) SetClientAttachmentRepository(repo repository.ClientAttachmentRepositoryInterface) {
	m.Called(repo)
}

func (m *MockRouteService) SetClientIPRepository(repo repository.ClientIPRepositoryInterface) {
	m.Called(repo)
}

func (m *MockRouteService) SetClientRepository(repo repository.ClientRepositoryInterface) {
	m.Called(repo)
}

func (m *MockRouteService) SetDomainService(ds *services.DomainService) {
	m.Called(ds)
}

func (m *MockRouteService) SetRouteVersionService(rvs *services.RouteVersionService) {
	m.Called(rvs)
}

func (m *MockRouteService) Create(domainID uuid.UUID, input *services.CreateRouteInput, createdBy uuid.UUID) (*models.Route, error) {
	args := m.Called(domainID, input, createdBy)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Route), args.Error(1)
}

func (m *MockRouteService) GetByID(id uuid.UUID) (*models.Route, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Route), args.Error(1)
}

func (m *MockRouteService) GetSecurityPolicy(routeID uuid.UUID) (*models.SecurityPolicy, error) {
	args := m.Called(routeID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.SecurityPolicy), args.Error(1)
}

func (m *MockRouteService) GetBackendTrafficPolicy(routeID uuid.UUID) (*models.BackendTrafficPolicy, error) {
	args := m.Called(routeID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.BackendTrafficPolicy), args.Error(1)
}

func (m *MockRouteService) GetEnvoyExtensionPolicy(routeID uuid.UUID) (*models.EnvoyExtensionPolicy, error) {
	args := m.Called(routeID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.EnvoyExtensionPolicy), args.Error(1)
}

func (m *MockRouteService) GetWafPolicy(routeID uuid.UUID) (*models.WafPolicy, error) {
	args := m.Called(routeID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.WafPolicy), args.Error(1)
}

func (m *MockRouteService) ListByDomainID(domainID uuid.UUID, page, limit int, teamID *uuid.UUID, status string, search string, searchField string, labels map[string]string) ([]models.Route, int64, error) {
	args := m.Called(domainID, page, limit, teamID, status, search, searchField, labels)
	return args.Get(0).([]models.Route), args.Get(1).(int64), args.Error(2)
}

func (m *MockRouteService) ListByProjectID(projectID uuid.UUID, page, limit int, filters repository.RouteListFilters) ([]models.Route, int64, error) {
	args := m.Called(projectID, page, limit, filters)
	var routes []models.Route
	if v := args.Get(0); v != nil {
		routes = v.([]models.Route)
	}
	return routes, args.Get(1).(int64), args.Error(2)
}

func (m *MockRouteService) Update(id uuid.UUID, input *services.UpdateRouteInput, submittedBy uuid.UUID) (*models.Route, error) {
	args := m.Called(id, input, submittedBy)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Route), args.Error(1)
}

func (m *MockRouteService) Delete(id uuid.UUID, submittedBy uuid.UUID) (*models.Route, error) {
	args := m.Called(id, submittedBy)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Route), args.Error(1)
}

func (m *MockRouteService) Deploy(id uuid.UUID, deployedBy uuid.UUID) (*models.Route, error) {
	args := m.Called(id, deployedBy)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Route), args.Error(1)
}

func (m *MockRouteService) GetEffectiveIPAllowlist(routeID uuid.UUID) ([]services.EffectiveIPEntry, error) {
	args := m.Called(routeID)
	return args.Get(0).([]services.EffectiveIPEntry), args.Error(1)
}

func (m *MockRouteService) GenerateYAML(id uuid.UUID) (string, error) {
	args := m.Called(id)
	return args.String(0), args.Error(1)
}

func (m *MockRouteService) GenerateYAMLs(id uuid.UUID) (*services.RouteYAMLs, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*services.RouteYAMLs), args.Error(1)
}

func (m *MockRouteService) PreviewCreate(domainID uuid.UUID, input *services.CreateRouteInput) (*services.PreviewCreateResult, error) {
	args := m.Called(domainID, input)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*services.PreviewCreateResult), args.Error(1)
}

func (m *MockRouteService) PreviewUpdate(routeID uuid.UUID, input *services.UpdateRouteInput) (*services.PreviewUpdateResult, error) {
	args := m.Called(routeID, input)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*services.PreviewUpdateResult), args.Error(1)
}

func (m *MockRouteService) PreviewDelete(routeID uuid.UUID) (*services.PreviewDeleteResult, error) {
	args := m.Called(routeID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*services.PreviewDeleteResult), args.Error(1)
}

func (m *MockRouteService) GetDomainName(domainID uuid.UUID) (string, error) {
	args := m.Called(domainID)
	return args.String(0), args.Error(1)
}

func (m *MockRouteService) GetApprovalIDForEntity(entityType models.ApprovalEntityType, entityID uuid.UUID) (*uuid.UUID, error) {
	args := m.Called(entityType, entityID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*uuid.UUID), args.Error(1)
}

func (m *MockRouteService) CheckMatcherConflicts(domainID uuid.UUID, match models.RouteMatch, excludeRouteID *uuid.UUID) ([]services.ConflictResult, error) {
	args := m.Called(domainID, match, excludeRouteID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]services.ConflictResult), args.Error(1)
}

// ============================================================================
// MockRouteVersionService
// ============================================================================

type MockRouteVersionService struct {
	mock.Mock
}

func (m *MockRouteVersionService) SetSecurityPolicyRepo(repo repository.SecurityPolicyRepositoryInterface) {
	m.Called(repo)
}

func (m *MockRouteVersionService) SetBackendTrafficPolicyRepo(repo repository.BackendTrafficPolicyRepositoryInterface) {
	m.Called(repo)
}

func (m *MockRouteVersionService) SetEnvoyExtensionPolicyRepo(repo repository.EnvoyExtensionPolicyRepositoryInterface) {
	m.Called(repo)
}

func (m *MockRouteVersionService) SetWafPolicyRepo(repo repository.WafPolicyRepositoryInterface) {
	m.Called(repo)
}

func (m *MockRouteVersionService) SetRouteService(svc *services.RouteService) {
	m.Called(svc)
}

func (m *MockRouteVersionService) CreateVersion(route *models.Route, approval *models.Approval, deployedBy uuid.UUID) error {
	args := m.Called(route, approval, deployedBy)
	return args.Error(0)
}

func (m *MockRouteVersionService) ListVersions(routeID uuid.UUID, page, limit int) ([]models.RouteVersion, int64, error) {
	args := m.Called(routeID, page, limit)
	return args.Get(0).([]models.RouteVersion), args.Get(1).(int64), args.Error(2)
}

func (m *MockRouteVersionService) GetVersion(routeID uuid.UUID, version int) (*models.RouteVersion, error) {
	args := m.Called(routeID, version)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.RouteVersion), args.Error(1)
}

func (m *MockRouteVersionService) Rollback(routeID uuid.UUID, targetVersion int, submittedBy uuid.UUID) (*models.Route, error) {
	args := m.Called(routeID, targetVersion, submittedBy)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Route), args.Error(1)
}

// ============================================================================
// MockSSOService
// ============================================================================

type MockSSOService struct {
	mock.Mock
}

func (m *MockSSOService) SetTokenGenerator(fn func(*models.User) (string, string, error)) {
	m.Called(fn)
}

func (m *MockSSOService) SetSystemSettingsService(svc *services.SystemSettingsService) {
	m.Called(svc)
}

func (m *MockSSOService) GetPublicConfig() (*services.SSOPublicConfig, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*services.SSOPublicConfig), args.Error(1)
}

func (m *MockSSOService) GetConfig() (*models.SSOConfig, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.SSOConfig), args.Error(1)
}

func (m *MockSSOService) UpdateConfig(input services.SSOConfigInput) (*models.SSOConfig, error) {
	args := m.Called(input)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.SSOConfig), args.Error(1)
}

func (m *MockSSOService) DisableSSO() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockSSOService) GetAuthorizeURL(callbackURL string) (string, error) {
	args := m.Called(callbackURL)
	return args.String(0), args.Error(1)
}

func (m *MockSSOService) HandleCallback(ctx context.Context, code, state, callbackURL string) (*services.SSOCallbackResult, error) {
	args := m.Called(ctx, code, state, callbackURL)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*services.SSOCallbackResult), args.Error(1)
}

func (m *MockSSOService) ShouldForceSSO(email string, role models.UserRole) bool {
	args := m.Called(email, role)
	return args.Bool(0)
}

// ============================================================================
// MockSystemSettingsService
// ============================================================================

type MockSystemSettingsService struct {
	mock.Mock
}

func (m *MockSystemSettingsService) Get() (*models.SystemSettings, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.SystemSettings), args.Error(1)
}

func (m *MockSystemSettingsService) GetResponse() (*services.SystemSettingsResponse, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*services.SystemSettingsResponse), args.Error(1)
}

func (m *MockSystemSettingsService) Update(input services.SystemSettingsInput) (*services.SystemSettingsResponse, error) {
	args := m.Called(input)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*services.SystemSettingsResponse), args.Error(1)
}

func (m *MockSystemSettingsService) GetJWTExpiry() time.Duration {
	args := m.Called()
	return args.Get(0).(time.Duration)
}

func (m *MockSystemSettingsService) GetRefreshTokenExpiry() time.Duration {
	args := m.Called()
	return args.Get(0).(time.Duration)
}

func (m *MockSystemSettingsService) GetBaseURL() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockSystemSettingsService) GetLogLevel() string {
	args := m.Called()
	return args.String(0)
}

// ============================================================================
// MockTeamEmailInviteService
// ============================================================================

type MockTeamEmailInviteService struct {
	mock.Mock
}

func (m *MockTeamEmailInviteService) AddMemberByEmail(teamID uuid.UUID, email string, invitedBy uuid.UUID) (*services.AddMemberResult, error) {
	args := m.Called(teamID, email, invitedBy)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*services.AddMemberResult), args.Error(1)
}

func (m *MockTeamEmailInviteService) ListInvites(teamID uuid.UUID) ([]models.TeamEmailInvite, error) {
	args := m.Called(teamID)
	return args.Get(0).([]models.TeamEmailInvite), args.Error(1)
}

func (m *MockTeamEmailInviteService) DeleteInvite(inviteID uuid.UUID) error {
	args := m.Called(inviteID)
	return args.Error(0)
}

func (m *MockTeamEmailInviteService) ListAllInvites() ([]models.TeamEmailInvite, error) {
	args := m.Called()
	return args.Get(0).([]models.TeamEmailInvite), args.Error(1)
}

// ============================================================================
// MockTeamService
// ============================================================================

type MockTeamService struct {
	mock.Mock
}

func (m *MockTeamService) Create(input *services.CreateTeamInput) (*models.Team, error) {
	args := m.Called(input)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Team), args.Error(1)
}

func (m *MockTeamService) GetByID(id uuid.UUID) (*models.Team, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Team), args.Error(1)
}

func (m *MockTeamService) List() ([]models.Team, error) {
	args := m.Called()
	return args.Get(0).([]models.Team), args.Error(1)
}

func (m *MockTeamService) Update(id uuid.UUID, input *services.UpdateTeamInput) (*models.Team, error) {
	args := m.Called(id, input)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Team), args.Error(1)
}

func (m *MockTeamService) Delete(id uuid.UUID) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *MockTeamService) AddMember(teamID, userID uuid.UUID) error {
	args := m.Called(teamID, userID)
	return args.Error(0)
}

func (m *MockTeamService) RemoveMember(teamID, userID uuid.UUID) error {
	args := m.Called(teamID, userID)
	return args.Error(0)
}

func (m *MockTeamService) ListMembers(teamID uuid.UUID) ([]models.User, error) {
	args := m.Called(teamID)
	return args.Get(0).([]models.User), args.Error(1)
}

func (m *MockTeamService) AssignTeamToProject(projectID uuid.UUID, input *services.AssignTeamInput) (*models.ProjectTeamRole, error) {
	args := m.Called(projectID, input)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.ProjectTeamRole), args.Error(1)
}

func (m *MockTeamService) UpdateTeamPresets(projectID, teamID uuid.UUID, input *services.UpdateTeamPresetsInput) (*models.ProjectTeamRole, error) {
	args := m.Called(projectID, teamID, input)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.ProjectTeamRole), args.Error(1)
}

func (m *MockTeamService) RemoveTeamFromProject(projectID, teamID uuid.UUID) error {
	args := m.Called(projectID, teamID)
	return args.Error(0)
}

func (m *MockTeamService) ListProjectTeams(projectID uuid.UUID) ([]models.ProjectTeamRole, error) {
	args := m.Called(projectID)
	return args.Get(0).([]models.ProjectTeamRole), args.Error(1)
}

func (m *MockTeamService) GetProjectTeamRole(projectID, teamID uuid.UUID) (*models.ProjectTeamRole, error) {
	args := m.Called(projectID, teamID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.ProjectTeamRole), args.Error(1)
}

func (m *MockTeamService) GetUserTeamsInProject(projectID, userID uuid.UUID) ([]models.ProjectTeamRole, error) {
	args := m.Called(projectID, userID)
	return args.Get(0).([]models.ProjectTeamRole), args.Error(1)
}

func (m *MockTeamService) GetUserTeams(userID uuid.UUID) ([]models.Team, error) {
	args := m.Called(userID)
	return args.Get(0).([]models.Team), args.Error(1)
}

func (m *MockTeamService) HasPermissionInProject(projectID, userID uuid.UUID, perm models.Permission) (bool, error) {
	args := m.Called(projectID, userID, perm)
	return args.Bool(0), args.Error(1)
}

func (m *MockTeamService) ListTeamProjects(teamID uuid.UUID) ([]models.ProjectTeamRole, error) {
	args := m.Called(teamID)
	return args.Get(0).([]models.ProjectTeamRole), args.Error(1)
}

func (m *MockTeamService) ListProjectMembers(projectID uuid.UUID, search string) ([]models.User, error) {
	args := m.Called(projectID, search)
	return args.Get(0).([]models.User), args.Error(1)
}

// ============================================================================
// MockUserService
// ============================================================================

type MockUserService struct {
	mock.Mock
}

func (m *MockUserService) Create(input *services.CreateUserInput) (*models.User, error) {
	args := m.Called(input)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.User), args.Error(1)
}

func (m *MockUserService) GetByID(id uuid.UUID) (*models.User, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.User), args.Error(1)
}

func (m *MockUserService) List(page, limit int, role string) ([]models.User, int64, error) {
	args := m.Called(page, limit, role)
	return args.Get(0).([]models.User), args.Get(1).(int64), args.Error(2)
}

func (m *MockUserService) Update(id uuid.UUID, input *services.UpdateUserInput) (*models.User, error) {
	args := m.Called(id, input)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.User), args.Error(1)
}

func (m *MockUserService) Delete(id uuid.UUID) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *MockUserService) SeedDefaultAdmin(username, password, email string) error {
	args := m.Called(username, password, email)
	return args.Error(0)
}

// ============================================================================
// MockMetricsService
// ============================================================================

// MockMetricsService is a testify mock for MetricsServiceInterface.
type MockMetricsService struct {
	mock.Mock
}

func (m *MockMetricsService) TestConnection(ctx context.Context, projectID uuid.UUID) (*services.TestConnectionResult, error) {
	args := m.Called(ctx, projectID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*services.TestConnectionResult), args.Error(1)
}

func (m *MockMetricsService) GetRouteMetrics(ctx context.Context, projectID, routeID uuid.UUID, rangeSpec string) (*services.RouteMetricsResult, error) {
	args := m.Called(ctx, projectID, routeID, rangeSpec)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*services.RouteMetricsResult), args.Error(1)
}

func (m *MockMetricsService) GetDomainMetrics(ctx context.Context, projectID, domainID uuid.UUID, rangeSpec string) (*services.DomainMetricsResult, error) {
	args := m.Called(ctx, projectID, domainID, rangeSpec)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*services.DomainMetricsResult), args.Error(1)
}

// ============================================================================
// MockProjectVersionService
// ============================================================================

type MockProjectVersionService struct {
	mock.Mock
}

func (m *MockProjectVersionService) Get(ctx context.Context, projectID uuid.UUID, forceRefresh bool) (*services.VersionInfo, error) {
	args := m.Called(ctx, projectID, forceRefresh)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*services.VersionInfo), args.Error(1)
}

func (m *MockProjectVersionService) Invalidate(projectID uuid.UUID) {
	m.Called(projectID)
}
