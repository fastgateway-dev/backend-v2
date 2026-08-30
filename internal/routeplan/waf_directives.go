package routeplan

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/fastgateway-dev/backend-v2/internal/models"
)

// CorazaDirectivesConfig is the JSON config format for coraza-proxy-wasm
type CorazaDirectivesConfig struct {
	DirectivesMap     map[string][]string `json:"directives_map"`
	DefaultDirectives string              `json:"default_directives"`
}

// BuildCorazaDirectives builds coraza directives JSON from WafPolicyConfig
// Returns empty string if config is nil or empty
func BuildCorazaDirectives(config *models.WafPolicyConfig) (string, error) {
	if config == nil || config.IsEmpty() {
		return "", nil
	}

	directives := []string{}

	// 1. SecRuleEngine based on mode
	if config.Mode == "block" {
		directives = append(directives, "SecRuleEngine On")
	} else {
		directives = append(directives, "SecRuleEngine DetectionOnly")
	}

	// 2. Paranoia level (default 1)
	paranoiaLevel := 1
	if config.ParanoiaLevel != nil {
		paranoiaLevel = *config.ParanoiaLevel
	}
	directives = append(directives, fmt.Sprintf(
		`SecAction "id:900000,phase:1,pass,t:none,nolog,setvar:tx.blocking_paranoia_level=%d"`,
		paranoiaLevel,
	))

	// 3. Anomaly threshold (default 5)
	anomalyThreshold := 5
	if config.AnomalyThreshold != nil {
		anomalyThreshold = *config.AnomalyThreshold
	}
	directives = append(directives, fmt.Sprintf(
		`SecAction "id:900110,phase:1,pass,t:none,nolog,setvar:tx.inbound_anomaly_score_threshold=%d"`,
		anomalyThreshold,
	))

	// 4. OWASP CRS rulesets
	for _, ruleset := range config.Rulesets {
		if ruleset == "owasp-crs" {
			directives = append(directives, "Include @crs-setup-conf")
			directives = append(directives, "Include @owasp_crs/*.conf")
			break
		}
	}

	// 5. Disabled rule IDs
	for _, ruleID := range config.DisabledRuleIDs {
		directives = append(directives, fmt.Sprintf("SecRuleRemoveById %d", ruleID))
	}

	// 6. Custom directives (trimmed, non-empty only)
	for _, directive := range config.CustomDirectives {
		trimmed := strings.TrimSpace(directive)
		if trimmed != "" {
			directives = append(directives, trimmed)
		}
	}

	// Build the coraza config structure
	corazaConfig := CorazaDirectivesConfig{
		DirectivesMap: map[string][]string{
			"default": directives,
		},
		DefaultDirectives: "default",
	}

	// Marshal to JSON
	jsonBytes, err := json.Marshal(corazaConfig)
	if err != nil {
		return "", fmt.Errorf("failed to marshal coraza config: %w", err)
	}

	return string(jsonBytes), nil
}
