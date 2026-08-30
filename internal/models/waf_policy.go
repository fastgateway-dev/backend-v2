package models

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// WafPolicy represents WAF (Web Application Firewall) policy configuration
type WafPolicy struct {
	ID        uuid.UUID       `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	RouteID   uuid.UUID       `gorm:"type:uuid;uniqueIndex;not null" json:"routeId"`
	ProjectID uuid.UUID       `gorm:"type:uuid;not null;index" json:"projectId"`
	Config    WafPolicyConfig `gorm:"type:jsonb" json:"config"`
	CreatedAt time.Time       `gorm:"not null;default:now()" json:"createdAt"`
	UpdatedAt time.Time       `gorm:"not null;default:now()" json:"updatedAt"`

	// Relationships
	Route   *Route   `gorm:"foreignKey:RouteID" json:"route,omitempty"`
	Project *Project `gorm:"foreignKey:ProjectID" json:"project,omitempty"`
}

// TableName returns the table name for WafPolicy
func (WafPolicy) TableName() string {
	return "waf_policies"
}

// WafPolicyConfig holds WAF configuration using coraza-proxy-wasm
type WafPolicyConfig struct {
	Mode             string   `json:"mode"`                       // "block" or "detect"
	Rulesets         []string `json:"rulesets,omitempty"`         // e.g., ["owasp-crs"]
	AnomalyThreshold *int     `json:"anomalyThreshold,omitempty"` // default: 5
	ParanoiaLevel    *int     `json:"paranoiaLevel,omitempty"`    // 1-4, default: 1
	DisabledRuleIDs  []int    `json:"disabledRuleIDs,omitempty"`  // rule IDs to skip
	CustomDirectives []string `json:"customDirectives,omitempty"` // raw SecRule directives
}

// Value implements the driver.Valuer interface
func (c WafPolicyConfig) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan implements the sql.Scanner interface
func (c *WafPolicyConfig) Scan(value interface{}) error {
	if value == nil {
		*c = WafPolicyConfig{}
		return nil
	}
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}
	return json.Unmarshal(bytes, c)
}

// IsEmpty checks if the config has no WAF configuration
func (c *WafPolicyConfig) IsEmpty() bool {
	return c.Mode == ""
}

// HasContent returns true if WAF is configured
func (c *WafPolicyConfig) HasContent() bool {
	return !c.IsEmpty()
}

// Validate validates the WAF policy config
func (c *WafPolicyConfig) Validate() error {
	if c.Mode == "" {
		return errors.New("mode is required")
	}
	if c.Mode != "block" && c.Mode != "detect" {
		return fmt.Errorf("mode must be 'block' or 'detect', got %q", c.Mode)
	}
	if c.ParanoiaLevel != nil {
		if *c.ParanoiaLevel < 1 || *c.ParanoiaLevel > 4 {
			return fmt.Errorf("paranoiaLevel must be between 1 and 4, got %d", *c.ParanoiaLevel)
		}
	}
	if c.AnomalyThreshold != nil {
		if *c.AnomalyThreshold < 1 {
			return fmt.Errorf("anomalyThreshold must be at least 1, got %d", *c.AnomalyThreshold)
		}
	}
	return nil
}
