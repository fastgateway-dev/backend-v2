package models

import (
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// Permission represents a granular permission
type Permission string

const (
	// Route permissions
	PermRouteView    Permission = "route.view"
	PermRouteCreate  Permission = "route.create"
	PermRouteEdit    Permission = "route.edit"
	PermRouteDelete  Permission = "route.delete"
	PermRouteDeploy  Permission = "route.deploy"
	PermRouteApprove Permission = "route.approve"

	// Client permissions
	PermClientView      Permission = "client.view"
	PermClientCreate    Permission = "client.create"
	PermClientEdit      Permission = "client.edit"
	PermClientDelete    Permission = "client.delete"
	PermClientManageIP  Permission = "client.manage_ip"
	PermClientManageKey Permission = "client.manage_apikey"
	PermClientManageJWT Permission = "client.manage_jwt"
	PermClientAttach    Permission = "client.attach"
	PermClientDetach    Permission = "client.detach"
	PermClientApprove   Permission = "client.approve"

	// Domain permissions
	PermDomainView   Permission = "domain.view"
	PermDomainCreate Permission = "domain.create"
	PermDomainEdit   Permission = "domain.edit"
	PermDomainDelete Permission = "domain.delete"

	// Project permissions
	PermProjectSettings       Permission = "project.settings"
	PermProjectTeams          Permission = "project.teams"
	PermProjectApprovalPolicy Permission = "project.approval_policy"

	// Audit permissions
	PermAuditView Permission = "audit.view"
)

// AllPermissions lists every valid permission string
var AllPermissions = []Permission{
	PermRouteView, PermRouteCreate, PermRouteEdit, PermRouteDelete, PermRouteDeploy, PermRouteApprove,
	PermClientView, PermClientCreate, PermClientEdit, PermClientDelete, PermClientManageIP, PermClientManageKey, PermClientManageJWT, PermClientAttach, PermClientDetach, PermClientApprove,
	PermDomainView, PermDomainCreate, PermDomainEdit, PermDomainDelete,
	PermProjectSettings, PermProjectTeams, PermProjectApprovalPolicy,
	PermAuditView,
}

// Permission presets
var PresetViewer = []Permission{PermRouteView, PermClientView, PermDomainView}

var PresetEditor = []Permission{
	PermRouteView, PermRouteCreate, PermRouteEdit, PermRouteDelete, PermRouteDeploy,
	PermClientView, PermClientCreate, PermClientEdit, PermClientManageIP, PermClientManageKey, PermClientManageJWT, PermClientAttach, PermClientDetach,
	PermDomainView, PermDomainCreate, PermDomainEdit,
}

var PresetApprover = []Permission{
	PermRouteView, PermRouteApprove,
	PermClientView, PermClientApprove,
	PermDomainView,
}

var PresetAdmin = AllPermissions

// ResolvePreset converts a preset name to a permissions list. Returns nil if unknown.
func ResolvePreset(preset string) []Permission {
	switch preset {
	case "viewer":
		return PresetViewer
	case "editor":
		return PresetEditor
	case "approver":
		return PresetApprover
	case "admin":
		return PresetAdmin
	default:
		return nil
	}
}

// IsValidPermission checks if a string is a valid permission
func IsValidPermission(perm string) bool {
	for _, p := range AllPermissions {
		if string(p) == perm {
			return true
		}
	}
	return false
}

// Team represents a global team (can be assigned to multiple projects)
type Team struct {
	ID          uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	Name        string    `gorm:"uniqueIndex;not null" json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `gorm:"not null;default:now()" json:"createdAt"`
	UpdatedAt   time.Time `gorm:"not null;default:now()" json:"updatedAt"`

	// Relationships
	Members      []TeamMember      `gorm:"foreignKey:TeamID" json:"-"`
	ProjectRoles []ProjectTeamRole `gorm:"foreignKey:TeamID" json:"-"`
	Routes       []Route           `gorm:"foreignKey:TeamID" json:"-"`

	// Computed fields (not stored in DB)
	MemberCount int `gorm:"-" json:"memberCount,omitempty"`
}

// TableName returns the table name for Team
func (Team) TableName() string {
	return "teams"
}

// TeamMember represents a user's membership in a team
type TeamMember struct {
	ID        uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	TeamID    uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_team_member" json:"teamId"`
	UserID    uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_team_member" json:"userId"`
	CreatedAt time.Time `gorm:"not null;default:now()" json:"createdAt"`

	// Relationships
	Team Team `gorm:"foreignKey:TeamID" json:"-"`
	User User `gorm:"foreignKey:UserID" json:"user,omitempty"`
}

// TableName returns the table name for TeamMember
func (TeamMember) TableName() string {
	return "team_members"
}

// ProjectTeamRole represents a team's assignment to a project with permissions
type ProjectTeamRole struct {
	ID        uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	ProjectID uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_project_team" json:"projectId"`
	TeamID    uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_project_team" json:"teamId"`
	CreatedAt time.Time `gorm:"not null;default:now()" json:"createdAt"`

	// Relationships
	Project Project             `gorm:"foreignKey:ProjectID" json:"-"`
	Team    Team                `gorm:"foreignKey:TeamID" json:"team,omitempty"`
	Presets []ProjectTeamPreset `gorm:"foreignKey:ProjectTeamRoleID" json:"presets,omitempty"`

	// Computed field (not stored in DB)
	EffectivePermissions []string `gorm:"-" json:"effectivePermissions,omitempty"`
}

// TableName returns the table name for ProjectTeamRole
func (ProjectTeamRole) TableName() string {
	return "project_team_roles"
}

// GetEffectivePermissions returns the union of all assigned preset permissions (sorted for deterministic output)
func (ptr *ProjectTeamRole) GetEffectivePermissions() []Permission {
	permSet := make(map[Permission]bool)
	for _, ptp := range ptr.Presets {
		for _, p := range ptp.Preset.Permissions {
			permSet[Permission(p)] = true
		}
	}
	result := make([]Permission, 0, len(permSet))
	for p := range permSet {
		result = append(result, p)
	}
	// Sort for deterministic output
	sort.Slice(result, func(i, j int) bool {
		return string(result[i]) < string(result[j])
	})
	return result
}

// HasPermission checks if the team has a specific permission through any preset
func (ptr *ProjectTeamRole) HasPermission(perm Permission) bool {
	for _, p := range ptr.GetEffectivePermissions() {
		if p == perm {
			return true
		}
	}
	return false
}

// PermissionPreset represents a reusable permission set for a project
type PermissionPreset struct {
	ID          uuid.UUID      `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	ProjectID   uuid.UUID      `gorm:"type:uuid;not null" json:"projectId"`
	Name        string         `gorm:"type:varchar(100);not null" json:"name"`
	Description string         `json:"description"`
	Permissions pq.StringArray `gorm:"type:text[];not null" json:"permissions"`
	IsBuiltin   bool           `gorm:"not null;default:false" json:"isBuiltin"`
	CreatedAt   time.Time      `gorm:"not null;default:now()" json:"createdAt"`
	UpdatedAt   time.Time      `gorm:"not null;default:now()" json:"updatedAt"`

	// Relationships
	Project Project `gorm:"foreignKey:ProjectID" json:"-"`
}

// TableName returns the table name for PermissionPreset
func (PermissionPreset) TableName() string {
	return "permission_presets"
}

// ProjectTeamPreset represents a team's assignment to a preset (many-to-many)
type ProjectTeamPreset struct {
	ID                uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	ProjectTeamRoleID uuid.UUID `gorm:"type:uuid;not null" json:"projectTeamRoleId"`
	PresetID          uuid.UUID `gorm:"type:uuid;not null" json:"presetId"`
	CreatedAt         time.Time `gorm:"not null;default:now()" json:"createdAt"`

	// Relationships
	ProjectTeamRole ProjectTeamRole  `gorm:"foreignKey:ProjectTeamRoleID" json:"-"`
	Preset          PermissionPreset `gorm:"foreignKey:PresetID" json:"preset,omitempty"`
}

// TableName returns the table name for ProjectTeamPreset
func (ProjectTeamPreset) TableName() string {
	return "project_team_presets"
}
