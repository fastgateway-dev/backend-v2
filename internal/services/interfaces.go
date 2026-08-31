package services

import (
	"context"
	"time"

	"github.com/fastgateway-dev/backend-v2/internal/ai"
	"github.com/fastgateway-dev/backend-v2/internal/cluster"
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// AIServiceInterface defines the public methods of AIService
type AIServiceInterface interface {
	IsEnabled() bool
	GetStatus() ai.AIStatus
	Generate(ctx context.Context, userID uuid.UUID, req ai.GenerateRequest) (<-chan ai.StreamChunk, error)
	Review(ctx context.Context, userID uuid.UUID, req ai.ReviewRequest) (*ai.ReviewResult, error)
	ReviewApproval(ctx context.Context, userID uuid.UUID, approval *models.Approval, diff *ApprovalDiffResult) (*ai.ReviewResult, error)
	Chat(ctx context.Context, userID uuid.UUID, req ai.ChatRequest) (<-chan ai.StreamChunk, error)
	TestAIConfig(ctx context.Context, provider, apiKey, model string, maxTokens int, baseURL string) error
}

// ApprovalServiceInterface defines the public methods of ApprovalService
type ApprovalServiceInterface interface {
	SetSecurityPolicyRepository(repo repository.SecurityPolicyRepositoryInterface)
	SetBackendTrafficPolicyRepository(repo repository.BackendTrafficPolicyRepositoryInterface)
	SetClientAttachmentService(cas *ClientAttachmentService)
	GetByID(id uuid.UUID) (*models.Approval, error)
	ListByProjectID(projectID uuid.UUID, page, limit int, status string, entityType string) ([]models.Approval, int64, error)
	CountPendingByProjectID(projectID uuid.UUID) (int64, error)
	ApproveStage(approvalID, stageID uuid.UUID, reviewer *models.User) (*models.Approval, error)
	RejectStage(approvalID, stageID uuid.UUID, reviewer *models.User, comment string) (*models.Approval, error)
	CancelApproval(approvalID uuid.UUID, user *models.User) (*models.Approval, error)
	ListPolicies(projectID uuid.UUID) ([]models.ApprovalPolicy, error)
	UpsertPolicy(policy *models.ApprovalPolicy) error
	GetDiff(id uuid.UUID) (*ApprovalDiffResult, error)
	UpdateAIReview(approval *models.Approval) error
}

// AuditServiceInterface defines the public methods of AuditService
type AuditServiceInterface interface {
	LogAction(projectID *uuid.UUID, user *models.User, action string, resourceType string, resourceID *uuid.UUID, resourceName string, details models.AuditDetails, ipAddress string, userAgent string) error
	ListByProjectID(projectID uuid.UUID, page, limit int, resourceType, action string, userID *uuid.UUID) ([]models.AuditLog, int64, error)
	ExportByProjectID(projectID uuid.UUID, resourceType, action string, userID *uuid.UUID) ([]models.AuditLog, error)
	CleanupOlderThan(projectID uuid.UUID, days int) (int64, error)
}

// AuthServiceInterface defines the public methods of AuthService
type AuthServiceInterface interface {
	SetSSOService(sso *SSOService)
	SetSystemSettingsService(ss *SystemSettingsService)
	Login(username, password string) (*LoginResponse, error)
	RefreshToken(refreshToken string) (*LoginResponse, error)
	ValidateToken(tokenString string) (*models.User, error)
	ValidateAPIToken(tokenString string) (*models.User, error)
	CreateAPIToken(userID uuid.UUID, name string, expiresAt *time.Time) (*models.APIToken, string, error)
	ListAPITokens(userID uuid.UUID) ([]models.APIToken, error)
	CountAPITokens(userID uuid.UUID) (int64, error)
	RevokeAPIToken(tokenID, userID uuid.UUID) error
	ChangePassword(userID uuid.UUID, currentPassword, newPassword string) error
	GenerateTokensForUser(user *models.User) (accessToken, refreshToken string, err error)
}

// ClientAttachmentServiceInterface defines the public methods of ClientAttachmentService
type ClientAttachmentServiceInterface interface {
	SetDomainSettingsRepository(repo repository.DomainSettingsRepositoryInterface)
	AttachFromRoute(routeID uuid.UUID, input *AttachFromRouteInput, submittedBy uuid.UUID) (*models.ClientRouteAttachment, error)
	AttachFromClient(clientID uuid.UUID, input *AttachFromClientInput, submittedBy uuid.UUID) (*models.ClientRouteAttachment, error)
	RequestDetach(attachmentID uuid.UUID, submittedBy uuid.UUID) (*models.ClientRouteAttachment, error)
	ApproveStage(approvalID, stageID uuid.UUID, reviewer *models.User) (*models.Approval, error)
	RejectStage(approvalID, stageID uuid.UUID, reviewer *models.User, comment string) (*models.Approval, error)
	GetApproval(id uuid.UUID) (*models.Approval, error)
	ListApprovalsByProjectID(projectID uuid.UUID, page, limit int, status string) ([]models.Approval, int64, error)
	ListByClientID(clientID uuid.UUID) ([]models.ClientRouteAttachment, error)
	ListByRouteID(routeID uuid.UUID) ([]models.ClientRouteAttachment, error)
	GetAttachment(id uuid.UUID) (*models.ClientRouteAttachment, error)
	OnApprovalComplete(approval *models.Approval) error
	OnApprovalRejected(approval *models.Approval) error
}

// ClientServiceInterface defines the public methods of ClientService
type ClientServiceInterface interface {
	SetClientAttachmentRepository(repo repository.ClientAttachmentRepositoryInterface)
	SetRouteRepository(repo repository.RouteRepositoryInterface)
	SetKubernetesService(k8sService KubernetesServiceInterface)
	Create(input *CreateClientInput, createdBy uuid.UUID) (*models.Client, error)
	GetByID(id uuid.UUID) (*models.Client, error)
	Update(id uuid.UUID, input *UpdateClientInput) (*models.Client, error)
	Delete(ctx context.Context, id uuid.UUID) error
	List(page, limit int, teamID *uuid.UUID) ([]models.Client, int64, error)
	AddIP(clientID uuid.UUID, input *CreateClientIPInput, createdBy uuid.UUID) (*models.ClientIPAddress, error)
	RemoveIP(clientID uuid.UUID, ipID uuid.UUID) error
	ListIPs(clientID uuid.UUID) ([]models.ClientIPAddress, error)
	GenerateAPIKey(ctx context.Context, clientID uuid.UUID, input *GenerateAPIKeyInput, createdBy uuid.UUID) (*GenerateAPIKeyResponse, error)
	RevokeAPIKey(ctx context.Context, clientID uuid.UUID) error
	GetAPIKeyForDeploy(ctx context.Context, client *models.Client) (string, error)
	ConfigureJWT(ctx context.Context, clientID uuid.UUID, input *ConfigureJWTInput, createdBy uuid.UUID) (*ConfigureJWTResponse, error)
	RemoveJWT(ctx context.Context, clientID uuid.UUID) error
	UpdateClientMTLS(ctx context.Context, clientID uuid.UUID, input *UpdateClientMTLSInput, updatedBy uuid.UUID) (*models.Client, error)
	AddHeader(clientID uuid.UUID, input *CreateClientHeaderInput, createdBy uuid.UUID) (*models.ClientHeader, error)
	RemoveHeader(clientID uuid.UUID, headerID uuid.UUID) error
	ListHeaders(clientID uuid.UUID) ([]models.ClientHeader, error)
	SetAllowedMethods(clientID uuid.UUID, methods []string) (*models.Client, error)
}

// CommentServiceInterface defines the public methods of CommentService
type CommentServiceInterface interface {
	Create(approvalID uuid.UUID, user *models.User, body string) (*models.ApprovalComment, error)
	ListByApprovalID(approvalID uuid.UUID) ([]models.ApprovalComment, error)
	CountByApprovalID(approvalID uuid.UUID) (int64, error)
}

// DomainServiceInterface defines the public methods of DomainService
type DomainServiceInterface interface {
	SetDomainSettingsRepository(repo repository.DomainSettingsRepositoryInterface)
	SetClientAttachmentRepository(repo repository.ClientAttachmentRepositoryInterface)
	SetBackendTrafficPolicyRepository(repo repository.BackendTrafficPolicyRepositoryInterface)
	SetEnvoyExtensionPolicyRepository(repo repository.EnvoyExtensionPolicyRepositoryInterface)
	SetDomainTemplateService(dts *DomainTemplateService)
	SetAIService(as *AIService)
	SetProjectNamespaceRepository(repo repository.ProjectNamespaceRepositoryInterface)
	Create(projectID uuid.UUID, input *CreateDomainInput, createdBy uuid.UUID) (*models.Domain, error)
	GetByID(id uuid.UUID) (*models.Domain, error)
	ListByProjectID(projectID uuid.UUID, page, limit int, search string, status string, labels map[string]string) ([]models.Domain, int64, error)
	Update(id uuid.UUID, input *UpdateDomainInput) (*models.Domain, error)
	Delete(id uuid.UUID) error
	GetDomainSettings(domainID uuid.UUID) (*models.DomainSettings, error)
	UpdateDomainSettings(domainID uuid.UUID, input *UpdateDomainSettingsInput) (*models.DomainSettings, error)
	GenerateYAMLs(domainID uuid.UUID) (*DomainYAMLs, error)
	PreviewCreate(projectID uuid.UUID, input *DomainCreatePreviewInput, userID uuid.UUID) (*DomainCreatePreviewResult, error)
	PreviewSettingsChanges(domainID uuid.UUID, input *DomainSettingsPreviewInput, userID uuid.UUID) (*DomainSettingsPreviewResult, error)
	AddDomainMTLSCA(ctx context.Context, domainID uuid.UUID, input *AddDomainMTLSCAInput) (*models.DomainSettings, error)
	RemoveDomainMTLSCA(ctx context.Context, domainID uuid.UUID, caID string) (*models.DomainSettings, error)
	ListTLSSecrets(ctx context.Context, projectID uuid.UUID, namespace string) (*ListTLSSecretsResponse, error)
	ListAvailableNamespaces(ctx context.Context, projectID uuid.UUID) ([]string, error)
}

// DomainTemplateServiceInterface defines the public methods of DomainTemplateService
type DomainTemplateServiceInterface interface {
	Create(projectID uuid.UUID, input *CreateDomainTemplateInput, createdBy uuid.UUID) (*models.DomainTemplate, error)
	GetByID(id uuid.UUID) (*models.DomainTemplate, error)
	GetByName(projectID uuid.UUID, name string) (*models.DomainTemplate, error)
	ListByProjectID(projectID uuid.UUID, page, limit int) ([]models.DomainTemplate, int64, error)
	Update(id uuid.UUID, input *UpdateDomainTemplateInput) (*models.DomainTemplate, error)
	Delete(id uuid.UUID) error
	GetManifests(id uuid.UUID) (*DomainTemplateManifests, error)
	PreviewChanges(id uuid.UUID, input *UpdateDomainTemplateInput, userID uuid.UUID, opts *PreviewChangesOptions) (*DomainTemplatePreviewResult, error)
	PreviewCreate(projectID uuid.UUID, input *CreateDomainTemplateInput, userID uuid.UUID, opts *PreviewChangesOptions) (*DomainTemplateCreatePreviewResult, error)
}

// KubernetesServiceInterface defines the public methods of *cluster.Client
type KubernetesServiceInterface interface {
	EnsureNamespace(ctx context.Context, projectID uuid.UUID, namespace string) error
	CreateGateway(ctx context.Context, projectID uuid.UUID, config *kubernetes.GatewayConfig) error
	DeleteGateway(ctx context.Context, projectID uuid.UUID, namespace, name string) error
	CreateHTTPRoute(ctx context.Context, projectID uuid.UUID, config *kubernetes.HTTPRouteConfig) error
	UpdateHTTPRoute(ctx context.Context, projectID uuid.UUID, config *kubernetes.HTTPRouteConfig) error
	DeleteHTTPRoute(ctx context.Context, projectID uuid.UUID, namespace, name string) error
	CreateGRPCRoute(ctx context.Context, projectID uuid.UUID, config *kubernetes.GRPCRouteConfig) error
	UpdateGRPCRoute(ctx context.Context, projectID uuid.UUID, config *kubernetes.GRPCRouteConfig) error
	DeleteGRPCRoute(ctx context.Context, projectID uuid.UUID, namespace, name string) error
	CreateSecurityPolicy(ctx context.Context, projectID uuid.UUID, config *kubernetes.SecurityPolicyConfig) error
	UpdateSecurityPolicy(ctx context.Context, projectID uuid.UUID, config *kubernetes.SecurityPolicyConfig) error
	DeleteSecurityPolicy(ctx context.Context, projectID uuid.UUID, namespace, name string) error
	CreateBackendTrafficPolicy(ctx context.Context, projectID uuid.UUID, config *kubernetes.BackendTrafficPolicyConfig) error
	UpdateBackendTrafficPolicy(ctx context.Context, projectID uuid.UUID, config *kubernetes.BackendTrafficPolicyConfig) error
	DeleteBackendTrafficPolicy(ctx context.Context, projectID uuid.UUID, namespace, name string) error
	CreateEnvoyExtensionPolicy(ctx context.Context, projectID uuid.UUID, policy *unstructured.Unstructured) error
	UpdateEnvoyExtensionPolicy(ctx context.Context, projectID uuid.UUID, policy *unstructured.Unstructured) error
	DeleteEnvoyExtensionPolicy(ctx context.Context, projectID uuid.UUID, namespace, name string) error
	CreateBackend(ctx context.Context, projectID uuid.UUID, config *kubernetes.BackendConfig) error
	UpdateBackend(ctx context.Context, projectID uuid.UUID, config *kubernetes.BackendConfig) error
	DeleteBackend(ctx context.Context, projectID uuid.UUID, namespace, name string) error
	UpdateBackendUnstructured(ctx context.Context, projectID uuid.UUID, backend *unstructured.Unstructured) error
	DeleteBackendsByRoute(ctx context.Context, projectID uuid.UUID, namespace, routeID string) error
	DeleteStaleBackendsByRoute(ctx context.Context, projectID uuid.UUID, namespace, routeID string, expectedNames map[string]bool) error
	TestConnection(ctx context.Context, projectID uuid.UUID) (bool, string, error)
	ListNamespaces(ctx context.Context, projectID uuid.UUID) ([]string, error)
	ListServices(ctx context.Context, projectID uuid.UUID, namespace string) ([]map[string]interface{}, error)
	ListTLSSecrets(ctx context.Context, projectID uuid.UUID, namespace string) ([]cluster.TLSSecretInfo, error)
	ListGatewayClasses(ctx context.Context, projectID uuid.UUID) ([]string, error)
	ValidatePrerequisites(ctx context.Context, apiURL, token string) (*cluster.PrerequisiteCheck, error)
	CreateGatewayClass(ctx context.Context, projectID uuid.UUID, config *kubernetes.GatewayClassConfig) error
	DeleteGatewayClass(ctx context.Context, projectID uuid.UUID, name string) error
	CreateEnvoyProxy(ctx context.Context, projectID uuid.UUID, config *kubernetes.EnvoyProxyConfig) error
	UpdateEnvoyProxy(ctx context.Context, projectID uuid.UUID, config *kubernetes.EnvoyProxyConfig) error
	DeleteEnvoyProxy(ctx context.Context, projectID uuid.UUID, namespace, name string) error
	ValidateEnvoyGatewayInstalled(ctx context.Context, projectID uuid.UUID) (bool, string, error)
	CreateReferenceGrant(ctx context.Context, projectID uuid.UUID, config *cluster.ReferenceGrantConfig) error
	DeleteReferenceGrant(ctx context.Context, projectID uuid.UUID, namespace, name string) error
	GetReferenceGrant(ctx context.Context, projectID uuid.UUID, namespace, name string) (*unstructured.Unstructured, error)
	ReferenceGrantExists(ctx context.Context, projectID uuid.UUID, namespace, name string) (bool, error)
	RecreateReferenceGrant(ctx context.Context, projectID uuid.UUID, config *cluster.ReferenceGrantConfig) error
	ApplyHTTPRouteFilter(ctx context.Context, projectID uuid.UUID, config *kubernetes.HTTPRouteFilterConfig) error
	DeleteHTTPRouteFilter(ctx context.Context, projectID uuid.UUID, namespace, name string) error
	ApplyDirectResponseConfigMap(ctx context.Context, projectID uuid.UUID, config *kubernetes.DirectResponseConfigMapConfig) error
	DeleteDirectResponseConfigMap(ctx context.Context, projectID uuid.UUID, namespace, name string) error
	CreateClientTrafficPolicy(ctx context.Context, projectID uuid.UUID, config *kubernetes.ClientTrafficPolicyConfig) error
	UpdateClientTrafficPolicy(ctx context.Context, projectID uuid.UUID, config *kubernetes.ClientTrafficPolicyConfig) error
	DeleteClientTrafficPolicy(ctx context.Context, projectID uuid.UUID, namespace, name string) error
	GetAPIKeySecretName(clientID uuid.UUID) string
	CreateAPIKeySecret(ctx context.Context, projectID uuid.UUID, clientID uuid.UUID, apiKey string) error
	GetAPIKeyFromSecret(ctx context.Context, projectID uuid.UUID, clientID uuid.UUID) (string, error)
	DeleteAPIKeySecret(ctx context.Context, projectID uuid.UUID, clientID uuid.UUID) error
	CreateOrUpdateSecret(ctx context.Context, projectID uuid.UUID, namespace, name string, data map[string][]byte) error
	DeleteSecret(ctx context.Context, projectID uuid.UUID, namespace, name string) error
	GetSecretData(ctx context.Context, projectID uuid.UUID, namespace, name, key string) ([]byte, error)
	IsRateLimitAvailable(ctx context.Context, projectID uuid.UUID) (bool, error)
	DeleteStaleAPIKeyResources(ctx context.Context, projectID uuid.UUID, namespace, routeID, baseRouteName string, expectedClientPrefixes map[string]bool) error
	DetectVersions(ctx context.Context, projectID uuid.UUID) (*cluster.RawVersions, error)
}

// MetricsServiceInterface defines the public methods of MetricsService.
type MetricsServiceInterface interface {
	TestConnection(ctx context.Context, projectID uuid.UUID) (*TestConnectionResult, error)
	GetRouteMetrics(ctx context.Context, projectID, routeID uuid.UUID, rangeSpec string) (*RouteMetricsResult, error)
	GetDomainMetrics(ctx context.Context, projectID, domainID uuid.UUID, rangeSpec string) (*DomainMetricsResult, error)
}

// NotificationServiceInterface defines the public methods of NotificationService
type NotificationServiceInterface interface {
	List(userID uuid.UUID, unreadOnly bool, page, limit int) ([]models.Notification, int64, error)
	CountUnread(userID uuid.UUID) (int64, error)
	MarkAsRead(notificationID uuid.UUID, userID uuid.UUID) error
	MarkAllAsRead(userID uuid.UUID) error
}

// PresetServiceInterface defines the public methods of PresetService
type PresetServiceInterface interface {
	Create(projectID uuid.UUID, input *CreatePresetInput) (*models.PermissionPreset, error)
	GetByID(id uuid.UUID) (*models.PermissionPreset, error)
	ListByProject(projectID uuid.UUID) ([]models.PermissionPreset, error)
	Update(projectID, id uuid.UUID, input *UpdatePresetInput) (*models.PermissionPreset, error)
	Delete(projectID, id uuid.UUID) error
	SeedBuiltinPresets(projectID uuid.UUID) error
}

// ProjectNamespaceServiceInterface defines the public methods of ProjectNamespaceService
type ProjectNamespaceServiceInterface interface {
	Create(projectID uuid.UUID, input *CreateProjectNamespaceInput) (*models.ProjectNamespace, error)
	Update(id uuid.UUID, input *UpdateProjectNamespaceInput) (*models.ProjectNamespace, error)
	GetByID(id uuid.UUID) (*models.ProjectNamespace, error)
	GetByProjectAndNamespace(projectID uuid.UUID, namespace string) (*models.ProjectNamespace, error)
	ListByProjectID(projectID uuid.UUID) ([]models.ProjectNamespace, error)
	ListByCapability(projectID uuid.UUID, capability string) ([]models.ProjectNamespace, error)
	Delete(id uuid.UUID) error
	IsNamespaceManaged(projectID uuid.UUID, namespace string) (bool, error)
	EnsureReferenceGrant(id uuid.UUID) error
}

// ProjectServiceInterface defines the public methods of ProjectService
type ProjectServiceInterface interface {
	SetKubernetesService(k8sService KubernetesServiceInterface)
	SetApprovalPolicyRepository(repo repository.ApprovalPolicyRepositoryInterface)
	SetPresetRepository(repo repository.PresetRepositoryInterface)
	Create(input *CreateProjectInput, createdBy uuid.UUID) (*models.Project, error)
	GetByID(id uuid.UUID) (*models.Project, error)
	List(userID uuid.UUID, userRole models.UserRole, page, limit int, search string, labels map[string]string) ([]models.Project, int64, error)
	Update(id uuid.UUID, input *UpdateProjectInput) (*models.Project, error)
	Delete(id uuid.UUID) error
	TestConnection(id uuid.UUID) (bool, string, string, error)
	GetDecryptedToken(id uuid.UUID) (string, error)
	GetDecryptedClientKey(id uuid.UUID) (string, error)
	AddAdmin(projectID, userID uuid.UUID) error
	RemoveAdmin(projectID, userID uuid.UUID) error
	ListAdmins(projectID uuid.UUID) ([]models.User, error)
	IsAdmin(projectID, userID uuid.UUID) (bool, error)
}

// RouteServiceInterface defines the public methods of RouteService
type RouteServiceInterface interface {
	SetKubernetesService(k8sService KubernetesServiceInterface)
	SetApprovalPolicyRepository(repo repository.ApprovalPolicyRepositoryInterface)
	SetProjectNamespaceRepository(repo repository.ProjectNamespaceRepositoryInterface)
	SetSecurityPolicyRepository(repo repository.SecurityPolicyRepositoryInterface)
	SetBackendTrafficPolicyRepository(repo repository.BackendTrafficPolicyRepositoryInterface)
	SetEnvoyExtensionPolicyRepository(repo repository.EnvoyExtensionPolicyRepositoryInterface)
	SetWafPolicyRepository(repo repository.WafPolicyRepositoryInterface)
	SetClientAttachmentRepository(repo repository.ClientAttachmentRepositoryInterface)
	SetClientIPRepository(repo repository.ClientIPRepositoryInterface)
	SetClientRepository(repo repository.ClientRepositoryInterface)
	SetDomainService(ds *DomainService)
	SetRouteVersionService(rvs *RouteVersionService)
	Create(domainID uuid.UUID, input *CreateRouteInput, createdBy uuid.UUID) (*models.Route, error)
	GetByID(id uuid.UUID) (*models.Route, error)
	GetSecurityPolicy(routeID uuid.UUID) (*models.SecurityPolicy, error)
	GetBackendTrafficPolicy(routeID uuid.UUID) (*models.BackendTrafficPolicy, error)
	GetEnvoyExtensionPolicy(routeID uuid.UUID) (*models.EnvoyExtensionPolicy, error)
	GetWafPolicy(routeID uuid.UUID) (*models.WafPolicy, error)
	ListByDomainID(domainID uuid.UUID, page, limit int, teamID *uuid.UUID, status string, search string, searchField string, labels map[string]string) ([]models.Route, int64, error)
	ListByProjectID(projectID uuid.UUID, page, limit int, filters repository.RouteListFilters) ([]models.Route, int64, error)
	Update(id uuid.UUID, input *UpdateRouteInput, submittedBy uuid.UUID) (*models.Route, error)
	Delete(id uuid.UUID, submittedBy uuid.UUID) (*models.Route, error)
	Deploy(id uuid.UUID, deployedBy uuid.UUID) (*models.Route, error)
	GetEffectiveIPAllowlist(routeID uuid.UUID) ([]EffectiveIPEntry, error)
	GenerateYAML(id uuid.UUID) (string, error)
	GenerateYAMLs(id uuid.UUID) (*RouteYAMLs, error)
	PreviewCreate(domainID uuid.UUID, input *CreateRouteInput) (*PreviewCreateResult, error)
	PreviewUpdate(routeID uuid.UUID, input *UpdateRouteInput) (*PreviewUpdateResult, error)
	PreviewDelete(routeID uuid.UUID) (*PreviewDeleteResult, error)
	GetDomainName(domainID uuid.UUID) (string, error)
	GetApprovalIDForEntity(entityType models.ApprovalEntityType, entityID uuid.UUID) (*uuid.UUID, error)
	CheckMatcherConflicts(domainID uuid.UUID, match models.RouteMatch, excludeRouteID *uuid.UUID) ([]ConflictResult, error)
}

// RouteVersionServiceInterface defines the public methods of RouteVersionService
type RouteVersionServiceInterface interface {
	SetSecurityPolicyRepo(repo repository.SecurityPolicyRepositoryInterface)
	SetBackendTrafficPolicyRepo(repo repository.BackendTrafficPolicyRepositoryInterface)
	SetEnvoyExtensionPolicyRepo(repo repository.EnvoyExtensionPolicyRepositoryInterface)
	SetWafPolicyRepo(repo repository.WafPolicyRepositoryInterface)
	SetRouteService(svc *RouteService)
	CreateVersion(route *models.Route, approval *models.Approval, deployedBy uuid.UUID) error
	ListVersions(routeID uuid.UUID, page, limit int) ([]models.RouteVersion, int64, error)
	GetVersion(routeID uuid.UUID, version int) (*models.RouteVersion, error)
	Rollback(routeID uuid.UUID, targetVersion int, submittedBy uuid.UUID) (*models.Route, error)
}

// SSOServiceInterface defines the public methods of SSOService
type SSOServiceInterface interface {
	SetTokenGenerator(fn func(*models.User) (string, string, error))
	SetSystemSettingsService(svc *SystemSettingsService)
	GetPublicConfig() (*SSOPublicConfig, error)
	GetConfig() (*models.SSOConfig, error)
	UpdateConfig(input SSOConfigInput) (*models.SSOConfig, error)
	DisableSSO() error
	GetAuthorizeURL(callbackURL string) (string, error)
	HandleCallback(ctx context.Context, code, state, callbackURL string) (*SSOCallbackResult, error)
	ShouldForceSSO(email string, role models.UserRole) bool
}

// SystemSettingsServiceInterface defines the public methods of SystemSettingsService
type SystemSettingsServiceInterface interface {
	Get() (*models.SystemSettings, error)
	GetResponse() (*SystemSettingsResponse, error)
	Update(input SystemSettingsInput) (*SystemSettingsResponse, error)
	GetJWTExpiry() time.Duration
	GetRefreshTokenExpiry() time.Duration
	GetBaseURL() string
	GetLogLevel() string
}

// TeamEmailInviteServiceInterface defines the public methods of TeamEmailInviteService
type TeamEmailInviteServiceInterface interface {
	AddMemberByEmail(teamID uuid.UUID, email string, invitedBy uuid.UUID) (*AddMemberResult, error)
	ListInvites(teamID uuid.UUID) ([]models.TeamEmailInvite, error)
	DeleteInvite(inviteID uuid.UUID) error
	ListAllInvites() ([]models.TeamEmailInvite, error)
}

// TeamServiceInterface defines the public methods of TeamService
type TeamServiceInterface interface {
	Create(input *CreateTeamInput) (*models.Team, error)
	GetByID(id uuid.UUID) (*models.Team, error)
	List() ([]models.Team, error)
	Update(id uuid.UUID, input *UpdateTeamInput) (*models.Team, error)
	Delete(id uuid.UUID) error
	AddMember(teamID, userID uuid.UUID) error
	RemoveMember(teamID, userID uuid.UUID) error
	ListMembers(teamID uuid.UUID) ([]models.User, error)
	AssignTeamToProject(projectID uuid.UUID, input *AssignTeamInput) (*models.ProjectTeamRole, error)
	UpdateTeamPresets(projectID, teamID uuid.UUID, input *UpdateTeamPresetsInput) (*models.ProjectTeamRole, error)
	RemoveTeamFromProject(projectID, teamID uuid.UUID) error
	ListProjectTeams(projectID uuid.UUID) ([]models.ProjectTeamRole, error)
	GetProjectTeamRole(projectID, teamID uuid.UUID) (*models.ProjectTeamRole, error)
	GetUserTeamsInProject(projectID, userID uuid.UUID) ([]models.ProjectTeamRole, error)
	GetUserTeams(userID uuid.UUID) ([]models.Team, error)
	HasPermissionInProject(projectID, userID uuid.UUID, perm models.Permission) (bool, error)
	ListTeamProjects(teamID uuid.UUID) ([]models.ProjectTeamRole, error)
	ListProjectMembers(projectID uuid.UUID, search string) ([]models.User, error)
}

// TopologyServiceInterface is the read-only topology aggregator.
type TopologyServiceInterface interface {
	GetProjectTopology(ctx context.Context, projectID uuid.UUID) (*ProjectTopologyResponse, error)
	GetDomainTopology(ctx context.Context, projectID, domainID uuid.UUID) (*DomainTopologyResponse, error)
}

// UserServiceInterface defines the public methods of UserService
type UserServiceInterface interface {
	Create(input *CreateUserInput) (*models.User, error)
	GetByID(id uuid.UUID) (*models.User, error)
	List(page, limit int, role string) ([]models.User, int64, error)
	Update(id uuid.UUID, input *UpdateUserInput) (*models.User, error)
	Delete(id uuid.UUID) error
	SeedDefaultAdmin(username, password, email string) error
}

// Compile-time interface satisfaction checks
var _ AIServiceInterface = (*AIService)(nil)
var _ ApprovalServiceInterface = (*ApprovalService)(nil)
var _ AuditServiceInterface = (*AuditService)(nil)
var _ AuthServiceInterface = (*AuthService)(nil)
var _ ClientAttachmentServiceInterface = (*ClientAttachmentService)(nil)
var _ ClientServiceInterface = (*ClientService)(nil)
var _ CommentServiceInterface = (*CommentService)(nil)
var _ DomainServiceInterface = (*DomainService)(nil)
var _ DomainTemplateServiceInterface = (*DomainTemplateService)(nil)
var _ KubernetesServiceInterface = (*cluster.Client)(nil)
var _ cluster.ProjectCredentials = (*ProjectService)(nil)
var _ MetricsServiceInterface = (*MetricsService)(nil)
var _ NotificationServiceInterface = (*NotificationService)(nil)
var _ PresetServiceInterface = (*PresetService)(nil)
var _ ProjectNamespaceServiceInterface = (*ProjectNamespaceService)(nil)
var _ ProjectServiceInterface = (*ProjectService)(nil)
var _ RouteServiceInterface = (*RouteService)(nil)
var _ RouteVersionServiceInterface = (*RouteVersionService)(nil)
var _ SSOServiceInterface = (*SSOService)(nil)
var _ SystemSettingsServiceInterface = (*SystemSettingsService)(nil)
var _ TeamEmailInviteServiceInterface = (*TeamEmailInviteService)(nil)
var _ TeamServiceInterface = (*TeamService)(nil)
var _ TopologyServiceInterface = (*TopologyService)(nil)
var _ UserServiceInterface = (*UserService)(nil)
var _ ProjectVersionServiceInterface = (*ProjectVersionService)(nil)

// ProjectVersionServiceInterface defines the public methods of ProjectVersionService.
type ProjectVersionServiceInterface interface {
	Get(ctx context.Context, projectID uuid.UUID, forceRefresh bool) (*VersionInfo, error)
	Invalidate(projectID uuid.UUID)
}
