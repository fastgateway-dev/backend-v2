package models

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrPolicyNotFound reports that no approval policy exists for a
// project/entity/action combination. It distinguishes genuine absence, where
// falling back to a default gate is correct, from a lookup FAILURE, where
// falling back silently replaces the project's real policy with a weaker one.
var ErrPolicyNotFound = errors.New("approval policy not found")

// ApprovalStatus represents the status of an approval
type ApprovalStatus string

const (
	ApprovalStatusPending   ApprovalStatus = "pending"
	ApprovalStatusApproved  ApprovalStatus = "approved"
	ApprovalStatusRejected  ApprovalStatus = "rejected"
	ApprovalStatusCancelled ApprovalStatus = "cancelled"
)

// ApprovalEntityType represents the type of entity being approved
type ApprovalEntityType string

const (
	ApprovalEntityRoute            ApprovalEntityType = "route"
	ApprovalEntityClientAttachment ApprovalEntityType = "client_attachment"
)

// ApprovalAction represents the action being requested
type ApprovalAction string

const (
	ApprovalActionCreate ApprovalAction = "create"
	ApprovalActionUpdate ApprovalAction = "update"
	ApprovalActionDelete ApprovalAction = "delete"
	ApprovalActionAttach ApprovalAction = "attach"
	ApprovalActionDetach ApprovalAction = "detach"
)

// Approval represents a unified approval request
type Approval struct {
	ID                uuid.UUID          `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	ProjectID         uuid.UUID          `gorm:"type:uuid;not null" json:"projectId"`
	EntityType        ApprovalEntityType `gorm:"column:entity_type;not null" json:"entityType"`
	EntityID          uuid.UUID          `gorm:"type:uuid;not null" json:"entityId"`
	Action            ApprovalAction     `gorm:"not null" json:"action"`
	ConfigSnapshot    json.RawMessage    `gorm:"type:jsonb" json:"configSnapshot,omitempty"`
	PreviousConfig    json.RawMessage    `gorm:"type:jsonb" json:"previousConfig,omitempty"`
	SubmittedBy       uuid.UUID          `gorm:"type:uuid;not null" json:"submittedBy"`
	Status            ApprovalStatus     `gorm:"not null;default:'pending'" json:"status"`
	ChangeDescription string             `gorm:"column:change_description" json:"changeDescription,omitempty"`
	AIReview          json.RawMessage    `gorm:"type:jsonb;column:ai_review" json:"aiReview,omitempty"`
	CreatedAt         time.Time          `gorm:"not null;default:now()" json:"createdAt"`

	// Relationships
	Submitter User            `gorm:"foreignKey:SubmittedBy" json:"submitter,omitempty"`
	Stages    []ApprovalStage `gorm:"foreignKey:ApprovalID" json:"stages,omitempty"`

	// Computed (not stored in DB)
	EntityName string `gorm:"-" json:"entityName,omitempty"`
	DomainName string `gorm:"-" json:"domainName,omitempty"`
}

// TableName returns the table name for Approval
func (Approval) TableName() string {
	return "approvals"
}

// ApprovalStage represents one stage in a multi-stage approval
type ApprovalStage struct {
	ID                 uuid.UUID      `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	ApprovalID         uuid.UUID      `gorm:"type:uuid;not null" json:"approvalId"`
	StageOrder         int            `gorm:"column:stage_order;not null" json:"order"`
	RequiredPermission string         `gorm:"not null" json:"requiredPermission"`
	RequiredTeamID     *uuid.UUID     `gorm:"type:uuid" json:"requiredTeamId"`
	ReviewedBy         *uuid.UUID     `gorm:"type:uuid" json:"reviewedBy"`
	Status             ApprovalStatus `gorm:"not null;default:'pending'" json:"status"`
	Comment            string         `json:"comment,omitempty"`
	MinApprovers       int            `gorm:"not null;default:1" json:"minApprovers"`
	ReviewedAt         *time.Time     `json:"reviewedAt,omitempty"`

	// Relationships
	Reviewer *User                 `gorm:"foreignKey:ReviewedBy" json:"reviewer,omitempty"`
	Reviews  []ApprovalStageReview `gorm:"foreignKey:StageID" json:"reviews,omitempty"`

	// Computed (not stored in DB)
	RequiredTeamName string `gorm:"-" json:"requiredTeamName,omitempty"`
}

// TableName returns the table name for ApprovalStage
func (ApprovalStage) TableName() string {
	return "approval_stages"
}

// ApprovalPolicy defines per-project approval stage templates
type ApprovalPolicy struct {
	ID         uuid.UUID          `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	ProjectID  uuid.UUID          `gorm:"type:uuid;not null" json:"projectId"`
	EntityType ApprovalEntityType `gorm:"column:entity_type;not null" json:"entityType"`
	Action     *string            `json:"action,omitempty"`
	Stages     json.RawMessage    `gorm:"type:jsonb;not null" json:"stages"`
	CreatedAt  time.Time          `gorm:"not null;default:now()" json:"createdAt"`
	UpdatedAt  time.Time          `gorm:"not null;default:now()" json:"updatedAt"`
}

// TableName returns the table name for ApprovalPolicy
func (ApprovalPolicy) TableName() string {
	return "approval_policies"
}

// PolicyStageTemplate is one stage definition in an approval policy
type PolicyStageTemplate struct {
	Order              int    `json:"order"`
	RequiredPermission string `json:"required_permission"`
	TeamScope          string `json:"team_scope"` // "any", "other_team", "submitter_team"
	MinApprovers       int    `json:"min_approvers"`
}

// ApprovalStageReview tracks individual reviewer decisions for multi-approver stages
type ApprovalStageReview struct {
	ID         uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	StageID    uuid.UUID `gorm:"type:uuid;not null" json:"stageId"`
	ReviewerID uuid.UUID `gorm:"type:uuid;not null" json:"reviewerId"`
	Decision   string    `gorm:"not null;default:'approved'" json:"decision"` // "approved" or "rejected"
	CreatedAt  time.Time `gorm:"not null;default:now()" json:"createdAt"`

	// Relationships
	Reviewer User `gorm:"foreignKey:ReviewerID" json:"reviewer,omitempty"`
}

// TableName returns the table name for ApprovalStageReview
func (ApprovalStageReview) TableName() string {
	return "approval_stage_reviews"
}

// EffectiveMinApprovers returns MinApprovers, treating <= 0 as 1
func EffectiveMinApprovers(n int) int {
	if n <= 0 {
		return 1
	}
	return n
}

// RouteApprovalSnapshot holds the config snapshots for route approvals
type RouteApprovalSnapshot struct {
	RouteConfig          *RouteConfig                `json:"routeConfig,omitempty"`
	SecurityPolicy       *SecurityPolicyConfig       `json:"securityPolicy,omitempty"`
	BackendTrafficPolicy *BackendTrafficPolicyConfig `json:"backendTrafficPolicy,omitempty"`
	EnvoyExtensionPolicy *EnvoyExtensionPolicyConfig `json:"envoyExtensionPolicy,omitempty"`
	WafPolicy            *WafPolicyConfig            `json:"wafPolicy,omitempty"`
}
