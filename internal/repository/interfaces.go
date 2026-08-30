package repository

import (
	"encoding/json"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
)

// APITokenRepositoryInterface defines the interface for API token repository operations
type APITokenRepositoryInterface interface {
	Create(token *models.APIToken) error
	GetByID(id uuid.UUID) (*models.APIToken, error)
	GetByTokenHash(hash string) (*models.APIToken, error)
	ListByUserID(userID uuid.UUID) ([]models.APIToken, error)
	CountByUserID(userID uuid.UUID) (int64, error)
	UpdateLastUsed(id uuid.UUID) error
	Delete(id uuid.UUID) error
	DeleteExpired() error
}

// ApprovalPolicyRepositoryInterface defines the interface for approval policy repository operations
type ApprovalPolicyRepositoryInterface interface {
	GetByProjectAndEntity(projectID uuid.UUID, entityType string, action *string) (*models.ApprovalPolicy, error)
	GetByID(id uuid.UUID) (*models.ApprovalPolicy, error)
	ListByProjectID(projectID uuid.UUID) ([]models.ApprovalPolicy, error)
	Create(policy *models.ApprovalPolicy) error
	Update(policy *models.ApprovalPolicy) error
	Upsert(policy *models.ApprovalPolicy) error
	Delete(id uuid.UUID) error
	SeedDefaults(projectID uuid.UUID) error
}

// UnifiedApprovalRepositoryInterface defines the interface for unified approval repository operations
type UnifiedApprovalRepositoryInterface interface {
	Create(approval *models.Approval) error
	GetByID(id uuid.UUID) (*models.Approval, error)
	Update(approval *models.Approval) error
	SetAIReview(id uuid.UUID, aiReview json.RawMessage) error
	ListByProjectID(projectID uuid.UUID, page, limit int, status, entityType string) ([]models.Approval, int64, error)
	CountPendingByProjectID(projectID uuid.UUID) (int64, error)
	GetPendingByEntityID(entityType models.ApprovalEntityType, entityID uuid.UUID) (*models.Approval, error)
	GetLatestApprovedByEntityID(entityType models.ApprovalEntityType, entityID uuid.UUID) (*models.Approval, error)
	DeleteByEntityID(entityType models.ApprovalEntityType, entityID uuid.UUID) error
	CreateStage(stage *models.ApprovalStage) error
	UpdateStage(stage *models.ApprovalStage) error
	GetStageByID(id uuid.UUID) (*models.ApprovalStage, error)
}

// AuditLogRepositoryInterface defines the interface for audit log repository operations
type AuditLogRepositoryInterface interface {
	Create(log *models.AuditLog) error
	ListByProjectID(projectID uuid.UUID, page, limit int, resourceType, action string, userID *uuid.UUID) ([]models.AuditLog, int64, error)
	ExportByProjectID(projectID uuid.UUID, resourceType, action string, userID *uuid.UUID) ([]models.AuditLog, error)
	DeleteOlderThan(projectID uuid.UUID, days int) (int64, error)
}

// BackendTrafficPolicyRepositoryInterface defines the interface for backend traffic policy repository operations
type BackendTrafficPolicyRepositoryInterface interface {
	Create(policy *models.BackendTrafficPolicy) error
	GetByID(id uuid.UUID) (*models.BackendTrafficPolicy, error)
	GetByRouteID(routeID uuid.UUID) (*models.BackendTrafficPolicy, error)
	GetByDomainID(domainID uuid.UUID) (*models.BackendTrafficPolicy, error)
	ListByProjectID(projectID uuid.UUID) ([]models.BackendTrafficPolicy, error)
	Update(policy *models.BackendTrafficPolicy) error
	Delete(id uuid.UUID) error
	DeleteByRouteID(routeID uuid.UUID) error
	DeleteByDomainID(domainID uuid.UUID) error
	ExistsByRouteID(routeID uuid.UUID) (bool, error)
	ExistsByDomainID(domainID uuid.UUID) (bool, error)
	Upsert(policy *models.BackendTrafficPolicy) error
}

// ClientAttachmentRepositoryInterface defines the interface for client attachment repository operations
type ClientAttachmentRepositoryInterface interface {
	Create(attachment *models.ClientRouteAttachment) error
	GetByID(id uuid.UUID) (*models.ClientRouteAttachment, error)
	Update(attachment *models.ClientRouteAttachment) error
	Delete(id uuid.UUID) error
	GetByClientAndRoute(clientID, routeID uuid.UUID) (*models.ClientRouteAttachment, error)
	ListByClientID(clientID uuid.UUID) ([]models.ClientRouteAttachment, error)
	ListByRouteID(routeID uuid.UUID) ([]models.ClientRouteAttachment, error)
	ListActiveByRouteID(routeID uuid.UUID) ([]models.ClientRouteAttachment, error)
	ListApprovedByRouteID(routeID uuid.UUID) ([]models.ClientRouteAttachment, error)
	UpdateStatusByRouteID(routeID uuid.UUID, fromStatus, toStatus models.AttachmentStatus) error
	CountByClientID(clientID uuid.UUID) (int64, error)
	ListActiveByClientIDWithIPAllowlist(clientID uuid.UUID) ([]models.ClientRouteAttachment, error)
	ListActiveByClientIDWithAPIKey(clientID uuid.UUID) ([]models.ClientRouteAttachment, error)
	ListActiveByClientIDWithJWT(clientID uuid.UUID) ([]models.ClientRouteAttachment, error)
	CountMTLSAttachmentsByClientID(clientID uuid.UUID) (int64, error)
	CountMTLSAttachmentsByDomainID(domainID uuid.UUID) (int64, error)
	GetMTLSClientsForDomain(domainID uuid.UUID) ([]models.Client, error)
	ListActiveByClientIDWithMTLS(clientID uuid.UUID) ([]models.ClientRouteAttachment, error)
	ListActiveByClientIDWithHeaderAuth(clientID uuid.UUID) ([]models.ClientRouteAttachment, error)
}

// ClientIPRepositoryInterface defines the interface for client IP repository operations
type ClientIPRepositoryInterface interface {
	Create(ip *models.ClientIPAddress) error
	GetByID(id uuid.UUID) (*models.ClientIPAddress, error)
	Delete(id uuid.UUID) error
	ListByClientID(clientID uuid.UUID) ([]models.ClientIPAddress, error)
	CountByClientID(clientID uuid.UUID) (int64, error)
}

// ClientHeaderRepositoryInterface defines the interface for client header repository operations
type ClientHeaderRepositoryInterface interface {
	Create(header *models.ClientHeader) error
	GetByID(id uuid.UUID) (*models.ClientHeader, error)
	Delete(id uuid.UUID) error
	ListByClientID(clientID uuid.UUID) ([]models.ClientHeader, error)
	CountByClientID(clientID uuid.UUID) (int64, error)
}

// ClientRepositoryInterface defines the interface for client repository operations
type ClientRepositoryInterface interface {
	Create(client *models.Client) error
	GetByID(id uuid.UUID) (*models.Client, error)
	Update(client *models.Client) error
	Delete(id uuid.UUID) error
	List(page, limit int, teamID *uuid.UUID) ([]models.Client, int64, error)
	ExistsByName(name string) (bool, error)
	ExistsByNameExcluding(name string, excludeID uuid.UUID) (bool, error)
	ListByTeamIDs(teamIDs []uuid.UUID) ([]models.Client, error)
}

// CommentRepositoryInterface defines the interface for comment repository operations
type CommentRepositoryInterface interface {
	Create(comment *models.ApprovalComment) error
	ListByApprovalID(approvalID uuid.UUID) ([]models.ApprovalComment, error)
	CountByApprovalID(approvalID uuid.UUID) (int64, error)
}

// DomainRepositoryInterface defines the interface for domain repository operations
type DomainRepositoryInterface interface {
	Create(domain *models.Domain) error
	GetByID(id uuid.UUID) (*models.Domain, error)
	GetByIDs(ids []uuid.UUID) ([]models.Domain, error)
	ListByProjectID(projectID uuid.UUID, page, limit int, search string, status string, labels map[string]string) ([]models.Domain, int64, error)
	Update(domain *models.Domain) error
	Delete(id uuid.UUID) error
	ExistsByHostname(projectID uuid.UUID, hostname string) (bool, error)
	ListByTemplateID(templateID uuid.UUID) ([]models.Domain, error)
	CountByProjectID(projectID uuid.UUID) (int, error)
}

// DomainSettingsRepositoryInterface defines the interface for domain settings repository operations
type DomainSettingsRepositoryInterface interface {
	Create(settings *models.DomainSettings) error
	GetByID(id uuid.UUID) (*models.DomainSettings, error)
	GetByDomainID(domainID uuid.UUID) (*models.DomainSettings, error)
	ListByProjectID(projectID uuid.UUID) ([]models.DomainSettings, error)
	Update(settings *models.DomainSettings) error
	Delete(id uuid.UUID) error
	DeleteByDomainID(domainID uuid.UUID) error
	ExistsByDomainID(domainID uuid.UUID) (bool, error)
	Upsert(settings *models.DomainSettings) error
}

// DomainTemplateRepositoryInterface defines the interface for domain template repository operations
type DomainTemplateRepositoryInterface interface {
	Create(dt *models.DomainTemplate) error
	GetByID(id uuid.UUID) (*models.DomainTemplate, error)
	GetByName(projectID uuid.UUID, name string) (*models.DomainTemplate, error)
	ListByProjectID(projectID uuid.UUID, page, limit int) ([]models.DomainTemplate, int64, error)
	ListByExposureType(projectID uuid.UUID, exposureType models.ExposureType) ([]models.DomainTemplate, error)
	Update(dt *models.DomainTemplate) error
	Delete(id uuid.UUID) error
	ExistsByName(projectID uuid.UUID, name string) (bool, error)
}

// EnvoyExtensionPolicyRepositoryInterface defines the interface for envoy extension policy repository operations
type EnvoyExtensionPolicyRepositoryInterface interface {
	Create(policy *models.EnvoyExtensionPolicy) error
	GetByID(id uuid.UUID) (*models.EnvoyExtensionPolicy, error)
	GetByRouteID(routeID uuid.UUID) (*models.EnvoyExtensionPolicy, error)
	GetByDomainID(domainID uuid.UUID) (*models.EnvoyExtensionPolicy, error)
	ListByProjectID(projectID uuid.UUID) ([]models.EnvoyExtensionPolicy, error)
	Update(policy *models.EnvoyExtensionPolicy) error
	Delete(id uuid.UUID) error
	DeleteByRouteID(routeID uuid.UUID) error
	DeleteByDomainID(domainID uuid.UUID) error
	ExistsByRouteID(routeID uuid.UUID) (bool, error)
	ExistsByDomainID(domainID uuid.UUID) (bool, error)
	Upsert(policy *models.EnvoyExtensionPolicy) error
}

// NotificationRepositoryInterface defines the interface for notification repository operations
type NotificationRepositoryInterface interface {
	Create(notification *models.Notification) error
	ListByUserID(userID uuid.UUID, unreadOnly bool, page, limit int) ([]models.Notification, int64, error)
	CountUnread(userID uuid.UUID) (int64, error)
	MarkAsRead(id uuid.UUID, userID uuid.UUID) error
	MarkAllAsRead(userID uuid.UUID) error
}

// PresetRepositoryInterface defines the interface for preset repository operations
type PresetRepositoryInterface interface {
	Create(preset *models.PermissionPreset) error
	GetByID(id uuid.UUID) (*models.PermissionPreset, error)
	GetByProjectAndName(projectID uuid.UUID, name string) (*models.PermissionPreset, error)
	ListByProject(projectID uuid.UUID) ([]models.PermissionPreset, error)
	Update(preset *models.PermissionPreset) error
	Delete(id uuid.UUID) error
	IsPresetInUse(presetID uuid.UUID) (bool, error)
	SeedBuiltinPresets(projectID uuid.UUID) error
}

// ProjectNamespaceRepositoryInterface defines the interface for project namespace repository operations
type ProjectNamespaceRepositoryInterface interface {
	Create(ns *models.ProjectNamespace) error
	GetByID(id uuid.UUID) (*models.ProjectNamespace, error)
	GetByProjectAndNamespace(projectID uuid.UUID, namespace string) (*models.ProjectNamespace, error)
	ListByProjectID(projectID uuid.UUID) ([]models.ProjectNamespace, error)
	ListByCapability(projectID uuid.UUID, capability string) ([]models.ProjectNamespace, error)
	Update(ns *models.ProjectNamespace) error
	Delete(id uuid.UUID) error
	ExistsByProjectAndNamespace(projectID uuid.UUID, namespace string) (bool, error)
}

// ProjectRepositoryInterface defines the interface for project repository operations
type ProjectRepositoryInterface interface {
	Create(project *models.Project) error
	GetByID(id uuid.UUID) (*models.Project, error)
	GetByIDWithCounts(id uuid.UUID) (*models.Project, error)
	List(page, limit int) ([]models.Project, int64, error)
	ListByUserAccess(userID uuid.UUID, userRole models.UserRole, page, limit int, search string, labels map[string]string) ([]models.Project, int64, error)
	Update(project *models.Project) error
	Delete(id uuid.UUID) error
	AddAdmin(projectID, userID uuid.UUID) error
	RemoveAdmin(projectID, userID uuid.UUID) error
	ListAdmins(projectID uuid.UUID) ([]models.User, error)
	IsAdmin(projectID, userID uuid.UUID) (bool, error)
	Count() (int, error)
	FindByConnectionType(connectionType string) (*models.Project, error)
}

// RouteListFilters carries optional filters for project-scoped route listing.
type RouteListFilters struct {
	BackendService   string     // matches config.backends[].service (and mirrors if IncludeMirrors)
	BackendNamespace string     // matches config.backends[].namespace (and mirrors if IncludeMirrors)
	IncludeMirrors   bool       // also match config.mirrors[]
	Statuses         []string   // empty = all statuses
	TeamID           *uuid.UUID // optional team scope
	DomainID         *uuid.UUID // optional further restriction
}

// RouteRepositoryInterface defines the interface for route repository operations
type RouteRepositoryInterface interface {
	Create(route *models.Route) error
	GetByID(id uuid.UUID) (*models.Route, error)
	GetByIDs(ids []uuid.UUID) ([]models.Route, error)
	GetByIDWithApproval(id uuid.UUID) (*models.Route, error)
	ListByDomainID(domainID uuid.UUID, page, limit int, teamID *uuid.UUID, status string, search string, searchField string, labels map[string]string) ([]models.Route, int64, error)
	ListByProjectID(projectID uuid.UUID, page, limit int, filters RouteListFilters) ([]models.Route, int64, error)
	Update(route *models.Route) error
	Delete(id uuid.UUID) error
	ExistsByName(domainID uuid.UUID, name string) (bool, error)
	GetActiveRoutesByDomainID(domainID uuid.UUID) ([]models.Route, error)
	CountByDomainID(domainID uuid.UUID) (int, error)
}

// RouteVersionRepositoryInterface defines the interface for route version repository operations
type RouteVersionRepositoryInterface interface {
	Create(version *models.RouteVersion) error
	GetByRouteIDAndVersion(routeID uuid.UUID, version int) (*models.RouteVersion, error)
	ListByRouteID(routeID uuid.UUID, page, limit int) ([]models.RouteVersion, int64, error)
	GetMaxVersion(routeID uuid.UUID) (int, error)
}

// SecurityPolicyRepositoryInterface defines the interface for security policy repository operations
type SecurityPolicyRepositoryInterface interface {
	Create(policy *models.SecurityPolicy) error
	GetByID(id uuid.UUID) (*models.SecurityPolicy, error)
	GetByRouteID(routeID uuid.UUID) (*models.SecurityPolicy, error)
	ListByProjectID(projectID uuid.UUID) ([]models.SecurityPolicy, error)
	Update(policy *models.SecurityPolicy) error
	Delete(id uuid.UUID) error
	DeleteByRouteID(routeID uuid.UUID) error
	ExistsByRouteID(routeID uuid.UUID) (bool, error)
	Upsert(policy *models.SecurityPolicy) error
}

// SSOConfigRepositoryInterface defines the interface for SSO config repository operations
type SSOConfigRepositoryInterface interface {
	Get() (*models.SSOConfig, error)
	Update(config *models.SSOConfig) error
}

// SystemSettingsRepositoryInterface defines the interface for system settings repository operations
type SystemSettingsRepositoryInterface interface {
	Get() (*models.SystemSettings, error)
	Update(settings *models.SystemSettings) error
}

// TeamEmailInviteRepositoryInterface defines the interface for team email invite repository operations
type TeamEmailInviteRepositoryInterface interface {
	Create(invite *models.TeamEmailInvite) error
	GetByEmail(email string) ([]models.TeamEmailInvite, error)
	ListByTeam(teamID uuid.UUID) ([]models.TeamEmailInvite, error)
	Delete(id uuid.UUID) error
	DeleteByEmail(email string) error
	ListAll() ([]models.TeamEmailInvite, error)
}

// TeamRepositoryInterface defines the interface for team repository operations
type TeamRepositoryInterface interface {
	Create(team *models.Team) error
	GetByID(id uuid.UUID) (*models.Team, error)
	List() ([]models.Team, error)
	Update(team *models.Team) error
	Delete(id uuid.UUID) error
	AddMember(teamID, userID uuid.UUID) error
	RemoveMember(teamID, userID uuid.UUID) error
	ListMembers(teamID uuid.UUID) ([]models.User, error)
	IsMember(teamID, userID uuid.UUID) (bool, error)
	GetTeamsByUserID(userID uuid.UUID) ([]models.Team, error)
	AssignTeamToProject(projectID, teamID uuid.UUID, presetIDs []uuid.UUID) error
	UpdateTeamPresets(projectID, teamID uuid.UUID, presetIDs []uuid.UUID) error
	RemoveTeamFromProject(projectID, teamID uuid.UUID) error
	ListProjectTeams(projectID uuid.UUID) ([]models.ProjectTeamRole, error)
	GetProjectTeamRole(projectID, teamID uuid.UUID) (*models.ProjectTeamRole, error)
	GetUserTeamsInProject(projectID, userID uuid.UUID) ([]models.ProjectTeamRole, error)
	HasPermissionInProject(projectID, userID uuid.UUID, perm models.Permission) (bool, error)
	HasPermissionInAnyProject(userID uuid.UUID, perm models.Permission) (bool, error)
	HasAnyRoleInProject(projectID, userID uuid.UUID) (bool, error)
	GetUserPermissionsInProject(projectID, userID uuid.UUID) ([]string, error)
	ListTeamProjects(teamID uuid.UUID) ([]models.ProjectTeamRole, error)
}

// UserRepositoryInterface defines the interface for user repository operations
type UserRepositoryInterface interface {
	Create(user *models.User) error
	GetByID(id uuid.UUID) (*models.User, error)
	GetByUsername(username string) (*models.User, error)
	GetByEmail(email string) (*models.User, error)
	List(page, limit int, role string) ([]models.User, int64, error)
	Update(user *models.User) error
	Delete(id uuid.UUID) error
	GetByProviderSubject(subject string) (*models.User, error)
	Count() (int, error)
}

// WafPolicyRepositoryInterface defines the interface for WAF policy repository operations
type WafPolicyRepositoryInterface interface {
	Create(policy *models.WafPolicy) error
	GetByID(id uuid.UUID) (*models.WafPolicy, error)
	GetByRouteID(routeID uuid.UUID) (*models.WafPolicy, error)
	ListByProjectID(projectID uuid.UUID) ([]models.WafPolicy, error)
	Update(policy *models.WafPolicy) error
	Delete(id uuid.UUID) error
	DeleteByRouteID(routeID uuid.UUID) error
	ExistsByRouteID(routeID uuid.UUID) (bool, error)
	Upsert(policy *models.WafPolicy) error
}

// ApprovalStageReviewRepositoryInterface handles approval stage review operations
type ApprovalStageReviewRepositoryInterface interface {
	Create(review *models.ApprovalStageReview) error
	CountByStageAndDecision(stageID uuid.UUID, decision string) (int64, error)
	ListByStageID(stageID uuid.UUID) ([]models.ApprovalStageReview, error)
}

// Compile-time interface satisfaction checks
var _ ApprovalStageReviewRepositoryInterface = (*ApprovalStageReviewRepository)(nil)
var _ APITokenRepositoryInterface = (*APITokenRepository)(nil)
var _ ApprovalPolicyRepositoryInterface = (*ApprovalPolicyRepository)(nil)
var _ UnifiedApprovalRepositoryInterface = (*UnifiedApprovalRepository)(nil)
var _ AuditLogRepositoryInterface = (*AuditLogRepository)(nil)
var _ BackendTrafficPolicyRepositoryInterface = (*BackendTrafficPolicyRepository)(nil)
var _ ClientAttachmentRepositoryInterface = (*ClientAttachmentRepository)(nil)
var _ ClientIPRepositoryInterface = (*ClientIPRepository)(nil)
var _ ClientRepositoryInterface = (*ClientRepository)(nil)
var _ CommentRepositoryInterface = (*CommentRepository)(nil)
var _ DomainRepositoryInterface = (*DomainRepository)(nil)
var _ DomainSettingsRepositoryInterface = (*DomainSettingsRepository)(nil)
var _ DomainTemplateRepositoryInterface = (*DomainTemplateRepository)(nil)
var _ EnvoyExtensionPolicyRepositoryInterface = (*EnvoyExtensionPolicyRepository)(nil)
var _ NotificationRepositoryInterface = (*NotificationRepository)(nil)
var _ PresetRepositoryInterface = (*PresetRepository)(nil)
var _ ProjectNamespaceRepositoryInterface = (*ProjectNamespaceRepository)(nil)
var _ ProjectRepositoryInterface = (*ProjectRepository)(nil)
var _ RouteRepositoryInterface = (*RouteRepository)(nil)
var _ RouteVersionRepositoryInterface = (*RouteVersionRepository)(nil)
var _ SecurityPolicyRepositoryInterface = (*SecurityPolicyRepository)(nil)
var _ SSOConfigRepositoryInterface = (*SSOConfigRepository)(nil)
var _ SystemSettingsRepositoryInterface = (*SystemSettingsRepository)(nil)
var _ TeamEmailInviteRepositoryInterface = (*TeamEmailInviteRepository)(nil)
var _ TeamRepositoryInterface = (*TeamRepository)(nil)
var _ UserRepositoryInterface = (*UserRepository)(nil)
var _ WafPolicyRepositoryInterface = (*WafPolicyRepository)(nil)
