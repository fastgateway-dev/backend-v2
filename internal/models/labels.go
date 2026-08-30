package models

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"regexp"
)

// Labels represents key-value labels stored as JSONB
type Labels map[string]string

// Maximum number of labels per resource
const MaxLabels = 10

// labelKeyRegex validates label keys: 1-63 chars, alphanumeric plus - _ .
// Must start and end with alphanumeric.
var labelKeyRegex = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9._-]{0,61}[a-zA-Z0-9])?$`)

// labelValueRegex validates label values: 0-63 chars, same rules as key.
// Empty string is allowed (handled separately).
var labelValueRegex = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9._-]{0,61}[a-zA-Z0-9])?$`)

// ValidateLabels validates a labels map according to Kubernetes-style rules.
func ValidateLabels(labels Labels) error {
	if len(labels) > MaxLabels {
		return fmt.Errorf("too many labels: maximum %d allowed, got %d", MaxLabels, len(labels))
	}

	for key, value := range labels {
		if key == "" {
			return fmt.Errorf("label key cannot be empty")
		}
		if !labelKeyRegex.MatchString(key) {
			return fmt.Errorf("invalid label key %q: must be 1-63 alphanumeric chars, dashes, underscores, or dots; must start and end with alphanumeric", key)
		}
		if value != "" && !labelValueRegex.MatchString(value) {
			return fmt.Errorf("invalid label value %q for key %q: must be 0-63 alphanumeric chars, dashes, underscores, or dots; must start and end with alphanumeric", value, key)
		}
	}

	return nil
}

// Scan implements the sql.Scanner interface for GORM
func (l *Labels) Scan(value interface{}) error {
	if value == nil {
		*l = make(Labels)
		return nil
	}
	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return fmt.Errorf("failed to unmarshal Labels value: %v", value)
	}
	return json.Unmarshal(bytes, l)
}

// Value implements the driver.Valuer interface for GORM
func (l Labels) Value() (driver.Value, error) {
	if l == nil {
		return "{}", nil
	}
	b, err := json.Marshal(l)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}
