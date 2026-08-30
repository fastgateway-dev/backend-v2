package models

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"time"

	"github.com/google/uuid"
)

// EnvoyExtensionPolicy represents Envoy Gateway's EnvoyExtensionPolicy CRD
type EnvoyExtensionPolicy struct {
	ID        uuid.UUID                  `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	RouteID   *uuid.UUID                 `gorm:"type:uuid;index" json:"routeId,omitempty"`
	DomainID  *uuid.UUID                 `gorm:"type:uuid;index" json:"domainId,omitempty"`
	ProjectID uuid.UUID                  `gorm:"type:uuid;not null;index" json:"projectId"`
	Config    EnvoyExtensionPolicyConfig `gorm:"type:jsonb" json:"config"`
	CreatedAt time.Time                  `gorm:"not null;default:now()" json:"createdAt"`
	UpdatedAt time.Time                  `gorm:"not null;default:now()" json:"updatedAt"`

	// Relationships
	Route   *Route   `gorm:"foreignKey:RouteID" json:"route,omitempty"`
	Domain  *Domain  `gorm:"foreignKey:DomainID" json:"domain,omitempty"`
	Project *Project `gorm:"foreignKey:ProjectID" json:"project,omitempty"`
}

// TableName returns the table name for EnvoyExtensionPolicy
func (EnvoyExtensionPolicy) TableName() string {
	return "envoy_extension_policies"
}

// EnvoyExtensionPolicyConfig holds Lua, Wasm, and ExtProc extension configurations
type EnvoyExtensionPolicyConfig struct {
	Lua     *LuaExtensionConfig     `json:"lua,omitempty"`
	Wasm    *WasmExtensionConfig    `json:"wasm,omitempty"`
	ExtProc *ExtProcExtensionConfig `json:"extProc,omitempty"`
}

// Value implements the driver.Valuer interface
func (c EnvoyExtensionPolicyConfig) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan implements the sql.Scanner interface
func (c *EnvoyExtensionPolicyConfig) Scan(value interface{}) error {
	if value == nil {
		*c = EnvoyExtensionPolicyConfig{}
		return nil
	}
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}
	return json.Unmarshal(bytes, c)
}

// IsEmpty checks if the config has any extensions configured
func (c *EnvoyExtensionPolicyConfig) IsEmpty() bool {
	return c.Lua == nil && c.Wasm == nil && c.ExtProc == nil
}

// HasContent returns true if any extension is configured
func (c *EnvoyExtensionPolicyConfig) HasContent() bool {
	return !c.IsEmpty()
}

// Validate validates the extension policy config
func (c *EnvoyExtensionPolicyConfig) Validate() error {
	if c.Lua != nil {
		if err := c.Lua.Validate(); err != nil {
			return fmt.Errorf("lua: %w", err)
		}
	}
	if c.Wasm != nil {
		if err := c.Wasm.Validate(); err != nil {
			return fmt.Errorf("wasm: %w", err)
		}
	}
	if c.ExtProc != nil {
		if err := c.ExtProc.Validate(); err != nil {
			return fmt.Errorf("ext-proc: %w", err)
		}
	}
	return nil
}

// LuaExtensionConfig represents Lua extension configuration
type LuaExtensionConfig struct {
	Type     string    `json:"type"` // "Inline" or "ValueRef"
	Inline   string    `json:"inline,omitempty"`
	ValueRef *ValueRef `json:"valueRef,omitempty"`
}

// Validate validates the Lua extension config
func (l *LuaExtensionConfig) Validate() error {
	if l.Type != "Inline" && l.Type != "ValueRef" {
		return fmt.Errorf("type must be 'Inline' or 'ValueRef', got %q", l.Type)
	}
	if l.Type == "Inline" && l.Inline == "" {
		return errors.New("inline script is required when type is Inline")
	}
	if l.Type == "ValueRef" {
		if l.ValueRef == nil {
			return errors.New("valueRef is required when type is ValueRef")
		}
		if l.ValueRef.Name == "" {
			return errors.New("valueRef.name is required")
		}
		if l.ValueRef.Kind != "ConfigMap" && l.ValueRef.Kind != "Secret" {
			return fmt.Errorf("valueRef.kind must be 'ConfigMap' or 'Secret', got %q", l.ValueRef.Kind)
		}
	}
	return nil
}

// WasmExtensionConfig represents Wasm extension configuration
type WasmExtensionConfig struct {
	Name   string         `json:"name"`
	RootID string         `json:"rootID,omitempty"`
	Code   WasmCodeSource `json:"code"`
	Config *string        `json:"config,omitempty"` // JSON config for wasm module
}

// Validate validates the Wasm extension config
func (w *WasmExtensionConfig) Validate() error {
	if w.Name == "" {
		return errors.New("name is required")
	}
	// Validate name format (k8s compatible)
	if !regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`).MatchString(w.Name) {
		return fmt.Errorf("name must be lowercase alphanumeric with hyphens, got %q", w.Name)
	}
	if err := w.Code.Validate(); err != nil {
		return fmt.Errorf("code: %w", err)
	}
	return nil
}

// WasmCodeSource represents the source of the Wasm module
type WasmCodeSource struct {
	Type  string           `json:"type"` // "HTTP" or "Image"
	HTTP  *WasmHTTPSource  `json:"http,omitempty"`
	Image *WasmImageSource `json:"image,omitempty"`
}

// Validate validates the Wasm code source
func (w *WasmCodeSource) Validate() error {
	if w.Type != "HTTP" && w.Type != "Image" {
		return fmt.Errorf("type must be 'HTTP' or 'Image', got %q", w.Type)
	}
	if w.Type == "HTTP" {
		if w.HTTP == nil {
			return errors.New("http is required when type is HTTP")
		}
		if err := w.HTTP.Validate(); err != nil {
			return fmt.Errorf("http: %w", err)
		}
	}
	if w.Type == "Image" {
		if w.Image == nil {
			return errors.New("image is required when type is Image")
		}
		if err := w.Image.Validate(); err != nil {
			return fmt.Errorf("image: %w", err)
		}
	}
	return nil
}

// WasmHTTPSource represents HTTP source for Wasm module
type WasmHTTPSource struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

// Validate validates the HTTP source
func (h *WasmHTTPSource) Validate() error {
	if h.URL == "" {
		return errors.New("url is required")
	}
	if _, err := url.ParseRequestURI(h.URL); err != nil {
		return fmt.Errorf("url is not valid: %w", err)
	}
	if h.SHA256 == "" {
		return errors.New("sha256 is required for HTTP source")
	}
	if !regexp.MustCompile(`^[a-fA-F0-9]{64}$`).MatchString(h.SHA256) {
		return errors.New("sha256 must be a 64-character hex string")
	}
	return nil
}

// WasmImageSource represents OCI image source for Wasm module
type WasmImageSource struct {
	URL        string    `json:"url"`
	SHA256     string    `json:"sha256,omitempty"`
	PullSecret *ValueRef `json:"pullSecret,omitempty"`
}

// Validate validates the Image source
func (i *WasmImageSource) Validate() error {
	if i.URL == "" {
		return errors.New("url is required")
	}
	if i.SHA256 != "" && !regexp.MustCompile(`^[a-fA-F0-9]{64}$`).MatchString(i.SHA256) {
		return errors.New("sha256 must be a 64-character hex string")
	}
	return nil
}

// ExtProcExtensionConfig represents external processing extension configuration
type ExtProcExtensionConfig struct {
	BackendRef     ExtProcBackendRef      `json:"backendRef"`
	ProcessingMode *ExtProcProcessingMode `json:"processingMode,omitempty"`
	FailOpen       bool                   `json:"failOpen,omitempty"`
}

// ExtProcBackendRef represents the gRPC service to send traffic to
type ExtProcBackendRef struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Port      int    `json:"port"`
}

// ExtProcProcessingMode controls what request/response parts are sent to the processor
type ExtProcProcessingMode struct {
	Request  *ExtProcBodyMode `json:"request,omitempty"`
	Response *ExtProcBodyMode `json:"response,omitempty"`
}

// ExtProcBodyMode configures body processing for a request or response phase
type ExtProcBodyMode struct {
	Body string `json:"body,omitempty"` // "None", "Buffered", "Streamed"
}

// Validate validates the ext-proc extension configuration
func (c *ExtProcExtensionConfig) Validate() error {
	if err := c.BackendRef.Validate(); err != nil {
		return fmt.Errorf("backendRef: %w", err)
	}
	if c.ProcessingMode != nil {
		if err := c.ProcessingMode.Validate(); err != nil {
			return fmt.Errorf("processingMode: %w", err)
		}
	}
	return nil
}

// Validate validates the ext-proc backend reference
func (r *ExtProcBackendRef) Validate() error {
	if r.Name == "" {
		return fmt.Errorf("name is required")
	}
	if r.Namespace == "" {
		return fmt.Errorf("namespace is required")
	}
	if r.Port <= 0 || r.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	return nil
}

// Validate validates the ext-proc processing mode
func (m *ExtProcProcessingMode) Validate() error {
	if m.Request != nil {
		if err := m.Request.Validate(); err != nil {
			return fmt.Errorf("request: %w", err)
		}
	}
	if m.Response != nil {
		if err := m.Response.Validate(); err != nil {
			return fmt.Errorf("response: %w", err)
		}
	}
	return nil
}

// Validate validates the body processing mode
func (b *ExtProcBodyMode) Validate() error {
	validModes := map[string]bool{"": true, "None": true, "Buffered": true, "Streamed": true}
	if !validModes[b.Body] {
		return fmt.Errorf("body must be one of: None, Buffered, Streamed (got %q)", b.Body)
	}
	return nil
}
