package services

import (
	"context"
	"time"

	"github.com/fastgateway-dev/backend-v2/internal/ai"
	"github.com/fastgateway-dev/backend-v2/internal/cluster"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
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
	// approval.Completer -- the engine calls exactly one of these when a
	// client_attachment approval reaches a terminal state. Renamed from
	// OnApprovalComplete/OnApprovalRejected in Phase 2D Task 8.
	OnApproved(approval *models.Approval) error
	OnRejected(approval *models.Approval) error
	OnCancelled(approval *models.Approval) error
}

// ClientReader is the slice of ClientService that ClientAttachmentHandler
// uses: it resolves a client by ID so the handler can authorize the caller
// against that client's team before listing or attaching its routes. Named
// for the capability and satisfied structurally, following Task 8's
// RouteApprovalReader -- which sits in the same handler struct. The handler
// declared the 18-method ClientServiceInterface in full to call one method.
type ClientReader interface {
	GetByID(id uuid.UUID) (*models.Client, error)
}

// ClientServiceInterface defines the public methods of ClientService
type ClientServiceInterface interface {
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

// DomainReader is the slice of DomainService that AIHandler uses: it resolves
// a domain by ID to build the ai.DomainContext (id, name, hostname) that every
// generation request carries. Named for the capability and satisfied
// structurally, following Task 8's RouteApprovalReader. The handler declared
// the 14-method DomainServiceInterface in full to call one method.
type DomainReader interface {
	GetByID(id uuid.UUID) (*models.Domain, error)
}

// DomainPolicyReader is the slice of DomainService that DomainHandler uses to
// fill in the policy halves of a domain's settings response. Named for the
// capability and satisfied structurally, following Phase 2E's ClientReader and
// DomainReader.
//
// Phase 2F Task 4: the handler previously held
// repository.BackendTrafficPolicyRepositoryInterface and
// repository.EnvoyExtensionPolicyRepositoryInterface directly -- two whole
// repository interfaces reached across the service layer to call one method
// each.
type DomainPolicyReader interface {
	GetDomainBackendTrafficPolicy(domainID uuid.UUID) (*models.BackendTrafficPolicy, error)
	GetDomainEnvoyExtensionPolicy(domainID uuid.UUID) (*models.EnvoyExtensionPolicy, error)
}

// DomainServiceInterface defines the public methods of DomainService
type DomainServiceInterface interface {
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

// TemplateDomainLister is the slice of DomainTemplateService that
// DomainTemplateHandler uses to answer "which domains use this template".
// Named for the capability and satisfied structurally, following Phase 2E's
// ClientReader and DomainReader.
//
// Phase 2F Task 4: the handler previously held
// repository.DomainRepositoryInterface -- a 15-method repository interface
// reached across the service layer to call one method.
type TemplateDomainLister interface {
	ListDomainsByTemplateID(templateID uuid.UUID) ([]models.Domain, error)
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

// RouteListFilters is the service layer's name for the optional filters a
// project-scoped route listing accepts.
//
// It is an alias, not a copy: RouteService.ListByProjectID hands the value
// straight to the repository, so an alias keeps the two provably identical
// while giving handlers a services-package name to construct. Phase 2F Task 4
// -- route_handler.go imported internal/repository solely to spell this type,
// which is why it was the one handler importing the package without calling a
// repository method.
type RouteListFilters = repository.RouteListFilters

// RouteApprovalReader is the slice of RouteService that
// ClientAttachmentHandler uses: it enriches an attachment response with the
// route's domain name and the approval currently open against it. Phase 2E
// Task 8 split it out of the 32-method RouteServiceInterface, which that
// handler declared in full to call two methods.
type RouteApprovalReader interface {
	GetDomainName(domainID uuid.UUID) (string, error)
	GetApprovalIDForEntity(entityType models.ApprovalEntityType, entityID uuid.UUID) (*uuid.UUID, error)
}

// RouteReader is the read side of RouteService: fetching a route, its four
// attached policies, the lists, the effective IP allowlist, and the matcher
// conflict check that runs before a write.
type RouteReader interface {
	RouteApprovalReader

	GetByID(id uuid.UUID) (*models.Route, error)
	GetSecurityPolicy(routeID uuid.UUID) (*models.SecurityPolicy, error)
	GetBackendTrafficPolicy(routeID uuid.UUID) (*models.BackendTrafficPolicy, error)
	GetEnvoyExtensionPolicy(routeID uuid.UUID) (*models.EnvoyExtensionPolicy, error)
	GetWafPolicy(routeID uuid.UUID) (*models.WafPolicy, error)
	ListByDomainID(domainID uuid.UUID, page, limit int, teamID *uuid.UUID, status string, search string, searchField string, labels map[string]string) ([]models.Route, int64, error)
	ListByProjectID(projectID uuid.UUID, page, limit int, filters RouteListFilters) ([]models.Route, int64, error)
	GetEffectiveIPAllowlist(routeID uuid.UUID) ([]EffectiveIPEntry, error)
	CheckMatcherConflicts(domainID uuid.UUID, match models.RouteMatch, excludeRouteID *uuid.UUID) ([]ConflictResult, error)
}

// RouteWriter is the write side of RouteService. Each of these four opens or
// advances an approval; none of them touches Kubernetes directly except
// Deploy.
type RouteWriter interface {
	Create(domainID uuid.UUID, input *CreateRouteInput, createdBy uuid.UUID) (*models.Route, error)
	Update(id uuid.UUID, input *UpdateRouteInput, submittedBy uuid.UUID) (*models.Route, error)
	Delete(id uuid.UUID, submittedBy uuid.UUID) (*models.Route, error)
	Deploy(id uuid.UUID, deployedBy uuid.UUID) (*models.Route, error)
}

// RoutePreviewer renders what a route would become without writing anything:
// the manifests for an existing route, and the diff for a proposed create,
// update or delete.
type RoutePreviewer interface {
	GenerateYAML(id uuid.UUID) (string, error)
	GenerateYAMLs(id uuid.UUID) (*RouteYAMLs, error)
	PreviewCreate(domainID uuid.UUID, input *CreateRouteInput) (*PreviewCreateResult, error)
	PreviewUpdate(routeID uuid.UUID, input *UpdateRouteInput) (*PreviewUpdateResult, error)
	PreviewDelete(routeID uuid.UUID) (*PreviewDeleteResult, error)
}

// RouteHandlerService is RouteHandler's dependency: the three roles above and
// nothing else. It is consumer-sized rather than type-sized -- the handler
// calls exactly these twenty methods, which is why RouteServiceInterface's
// SetKubernetesService is gone from it rather than merely unused.
type RouteHandlerService interface {
	RouteReader
	RouteWriter
	RoutePreviewer
}

// RouteVersionServiceInterface defines the public methods of RouteVersionService
type RouteVersionServiceInterface interface {
	CreateVersion(route *models.Route, approval *models.Approval, deployedBy uuid.UUID) error
	ListVersions(routeID uuid.UUID, page, limit int) ([]models.RouteVersion, int64, error)
	GetVersion(routeID uuid.UUID, version int) (*models.RouteVersion, error)
	Rollback(routeID uuid.UUID, targetVersion int, submittedBy uuid.UUID) (*models.Route, error)
}

// SSOServiceInterface defines the public methods of SSOService
type SSOServiceInterface interface {
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
var _ ClientReader = (*ClientService)(nil)
var _ CommentServiceInterface = (*CommentService)(nil)
var _ DomainServiceInterface = (*DomainService)(nil)
var _ DomainReader = (*DomainService)(nil)
var _ DomainPolicyReader = (*DomainService)(nil)
var _ DomainTemplateServiceInterface = (*DomainTemplateService)(nil)
var _ TemplateDomainLister = (*DomainTemplateService)(nil)
var _ cluster.ProjectCredentials = (*ProjectService)(nil)
var _ MetricsServiceInterface = (*MetricsService)(nil)
var _ NotificationServiceInterface = (*NotificationService)(nil)
var _ PresetServiceInterface = (*PresetService)(nil)
var _ ProjectNamespaceServiceInterface = (*ProjectNamespaceService)(nil)
var _ ProjectServiceInterface = (*ProjectService)(nil)
var _ RouteHandlerService = (*RouteService)(nil)
var _ RouteApprovalReader = (*RouteService)(nil)
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
