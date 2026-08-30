package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildUserMessage_NaturalLanguageWithDomain(t *testing.T) {
	req := GenerateRequest{
		Mode:  ModeNaturalLanguage,
		Input: "Create a route for my API",
		Domain: &DomainContext{
			ID:         "domain-1",
			Name:       "example",
			Hostname:   "example.com",
			Namespaces: []string{"default", "api"},
		},
	}

	msg := BuildUserMessage(req)

	if !strings.Contains(msg, "NATURAL_LANGUAGE") {
		t.Error("expected message to contain 'NATURAL_LANGUAGE'")
	}
	if !strings.Contains(msg, "example (example.com)") {
		t.Error("expected message to contain domain context")
	}
	if !strings.Contains(msg, "default") || !strings.Contains(msg, "api") {
		t.Error("expected message to contain namespaces")
	}
	if !strings.Contains(msg, "Create a route for my API") {
		t.Error("expected message to contain the input")
	}
}

func TestBuildUserMessage_ManifestImport(t *testing.T) {
	req := GenerateRequest{
		Mode:  ModeManifestImport,
		Input: "apiVersion: networking.k8s.io/v1\nkind: Ingress\n...",
	}

	msg := BuildUserMessage(req)

	if !strings.Contains(msg, "MANIFEST_IMPORT") {
		t.Error("expected message to contain 'MANIFEST_IMPORT'")
	}
	if !strings.Contains(msg, "apiVersion: networking.k8s.io/v1") {
		t.Error("expected message to contain the manifest input")
	}
	// No domain context
	if strings.Contains(msg, "CONTEXT:") {
		t.Error("expected no CONTEXT section when domain is nil")
	}
}

func TestBuildUserMessage_WithFormatHint(t *testing.T) {
	req := GenerateRequest{
		Mode:       ModeManifestImport,
		Input:      "kind: Ingress...",
		FormatHint: "ingress",
	}
	msg := BuildUserMessage(req)
	if !strings.Contains(msg, "FORMAT: Kubernetes Ingress manifest") {
		t.Error("expected FORMAT hint in user message")
	}
}

func TestBuildUserMessage_WithoutFormatHint(t *testing.T) {
	req := GenerateRequest{
		Mode:  ModeNaturalLanguage,
		Input: "Create a route",
	}
	msg := BuildUserMessage(req)
	if strings.Contains(msg, "FORMAT:") {
		t.Error("expected no FORMAT section for NL mode")
	}
}

func TestBuildUserMessage_NaturalLanguageNoDomain(t *testing.T) {
	req := GenerateRequest{
		Mode:  ModeNaturalLanguage,
		Input: "Simple route please",
	}

	msg := BuildUserMessage(req)

	if !strings.Contains(msg, "NATURAL_LANGUAGE") {
		t.Error("expected NATURAL_LANGUAGE mode")
	}
	if strings.Contains(msg, "CONTEXT:") {
		t.Error("expected no CONTEXT section when domain is nil")
	}
}

func TestBuildSystemPrompt_NoFormatHint(t *testing.T) {
	result := BuildSystemPrompt("")
	if !strings.Contains(result, "FastGateway route configuration assistant") {
		t.Error("expected base prompt")
	}
	if !strings.Contains(result, "Security Modes") {
		t.Error("expected suffix (Security Modes)")
	}
	if strings.Contains(result, "Feature Mapping Guide") {
		t.Error("expected no mapping tables for empty formatHint")
	}
}

func TestBuildSystemPrompt_Ingress(t *testing.T) {
	result := BuildSystemPrompt("ingress")
	if !strings.Contains(result, "Kubernetes Ingress to FastGateway") {
		t.Error("expected Ingress mapping table")
	}
	if !strings.Contains(result, "nginx-ingress Annotations") {
		t.Error("expected nginx-ingress annotations table")
	}
	// Check mapping table headers are NOT present (capabilities section mentions them but that's ok)
	if strings.Contains(result, "### Istio VirtualService to FastGateway") {
		t.Error("expected no Istio mapping table")
	}
	if strings.Contains(result, "### Kong Declarative Config to FastGateway") {
		t.Error("expected no Kong mapping table")
	}
}

func TestBuildSystemPrompt_Istio(t *testing.T) {
	result := BuildSystemPrompt("istio")
	if !strings.Contains(result, "### Istio VirtualService to FastGateway") {
		t.Error("expected Istio VirtualService mapping table")
	}
	if !strings.Contains(result, "Istio AuthorizationPolicy") {
		t.Error("expected Istio AuthorizationPolicy table")
	}
	if strings.Contains(result, "### Kubernetes Ingress to FastGateway") {
		t.Error("expected no Ingress mapping table")
	}
	if strings.Contains(result, "### Kong Declarative Config to FastGateway") {
		t.Error("expected no Kong mapping table")
	}
}

func TestBuildSystemPrompt_Kong(t *testing.T) {
	result := BuildSystemPrompt("kong")
	if !strings.Contains(result, "### Kong Declarative Config to FastGateway") {
		t.Error("expected Kong mapping table")
	}
	if strings.Contains(result, "### Istio VirtualService to FastGateway") {
		t.Error("expected no Istio mapping table")
	}
	if strings.Contains(result, "### Kubernetes Ingress to FastGateway") {
		t.Error("expected no Ingress mapping table")
	}
}

func TestBuildSystemPrompt_UnknownFallsToDefault(t *testing.T) {
	result := BuildSystemPrompt("unknown_format")
	if strings.Contains(result, "Feature Mapping Guide") {
		t.Error("expected no mapping tables for unknown formatHint")
	}
	if !strings.Contains(result, "Security Modes") {
		t.Error("expected suffix still present")
	}
}

func TestBuildChatSystemPrompt_NilContext(t *testing.T) {
	result := BuildChatSystemPrompt(nil)

	if result != ChatSystemPrompt {
		t.Error("expected bare ChatSystemPrompt when context is nil")
	}
}

func TestBuildChatSystemPrompt_RouteContext(t *testing.T) {
	routeJSON := json.RawMessage(`{"name":"my-route","matches":[{"path":"/api"}]}`)
	ctx := &ChatContext{
		Type:  "route",
		Route: routeJSON,
	}

	result := BuildChatSystemPrompt(ctx)

	if !strings.Contains(result, ChatSystemPrompt) {
		t.Error("expected result to contain the base ChatSystemPrompt")
	}
	if !strings.Contains(result, "route") {
		t.Error("expected result to mention 'route'")
	}
	if !strings.Contains(result, "my-route") {
		t.Error("expected result to contain the route JSON data")
	}
}

func TestBuildChatSystemPrompt_DomainContext(t *testing.T) {
	domainJSON := json.RawMessage(`{"hostname":"example.com"}`)
	ctx := &ChatContext{
		Type:   "domain",
		Domain: domainJSON,
	}

	result := BuildChatSystemPrompt(ctx)

	if !strings.Contains(result, "domain") {
		t.Error("expected result to mention 'domain'")
	}
	if !strings.Contains(result, "example.com") {
		t.Error("expected result to contain the domain JSON data")
	}
}

// TestBuildChatSystemPrompt_UnknownKeysFallBackToRawJSON is a regression test
// for a bug where writeYAMLBlocks silently dropped any context whose keys
// were all absent from the fixed yamlBlockOrder allow-list (e.g. a domain
// context like {"hostname":"..."}), because the unmarshal into
// map[string]string succeeded but the allow-list loop matched nothing. The
// fix falls back to rendering the raw JSON whenever the allow-list loop
// wrote nothing but the input was non-empty.
func TestBuildChatSystemPrompt_UnknownKeysFallBackToRawJSON(t *testing.T) {
	domainJSON := json.RawMessage(`{"hostname":"example.com","tlsProfile":"modern"}`)
	ctx := &ChatContext{
		Type:   "domain",
		Domain: domainJSON,
	}

	result := BuildChatSystemPrompt(ctx)

	if !strings.Contains(result, "example.com") {
		t.Error("expected domain JSON data to reach the prompt via the raw JSON fallback")
	}
	if !strings.Contains(result, "```json") {
		t.Error("expected a raw JSON fallback block when no keys match the allow-list")
	}
}

// TestBuildChatSystemPrompt_KnownKeysNoFallback ensures the raw-JSON fallback
// added for unrecognized keys does not fire when the allow-listed manifest
// keys ARE present — the route/manifest path must keep producing only
// labeled YAML blocks in the deterministic yamlBlockOrder, with no raw JSON
// dump appended.
func TestBuildChatSystemPrompt_KnownKeysNoFallback(t *testing.T) {
	routeJSON := json.RawMessage(`{"httpRoute":"apiVersion: gateway.networking.k8s.io/v1\nkind: HTTPRoute","securityPolicy":"apiVersion: gateway.envoyproxy.io/v1alpha1\nkind: SecurityPolicy"}`)
	ctx := &ChatContext{
		Type:  "route",
		Route: routeJSON,
	}

	result := BuildChatSystemPrompt(ctx)

	if !strings.Contains(result, "### HTTPRoute") {
		t.Error("expected labeled HTTPRoute block")
	}
	if !strings.Contains(result, "### SecurityPolicy") {
		t.Error("expected labeled SecurityPolicy block")
	}
	if strings.Contains(result, "```json") {
		t.Error("expected no raw JSON fallback when allow-listed keys are present")
	}
}

func TestBuildChatMessages_WithHistory(t *testing.T) {
	req := ChatRequest{
		Message: "What does CORS do?",
		History: []ChatMessage{
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi! How can I help?"},
		},
	}

	messages := BuildChatMessages(req)

	if len(messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(messages))
	}
	if messages[0].Role != "user" || messages[0].Content != "Hello" {
		t.Error("first message should be history[0]")
	}
	if messages[1].Role != "assistant" || messages[1].Content != "Hi! How can I help?" {
		t.Error("second message should be history[1]")
	}
	if messages[2].Role != "user" || messages[2].Content != "What does CORS do?" {
		t.Error("third message should be the new user message")
	}
}

func TestBuildChatMessages_EmptyHistory(t *testing.T) {
	req := ChatRequest{
		Message: "Hello",
		History: nil,
	}

	messages := BuildChatMessages(req)

	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}
	if messages[0].Content != "Hello" {
		t.Errorf("expected 'Hello', got %q", messages[0].Content)
	}
}

func TestBuildReviewMessage_WithProposedYaml(t *testing.T) {
	req := ReviewRequest{
		Action:      "create",
		Description: "Adding a new API route",
		ProposedYaml: &YamlSet{
			HttpRoute:      "apiVersion: gateway.networking.k8s.io/v1\nkind: HTTPRoute\n...",
			SecurityPolicy: "apiVersion: gateway.envoyproxy.io/v1alpha1\nkind: SecurityPolicy\n...",
		},
	}

	msg := BuildReviewMessage(req)

	if !strings.Contains(msg, "ACTION: create") {
		t.Error("expected ACTION: create")
	}
	if !strings.Contains(msg, "Adding a new API route") {
		t.Error("expected description in message")
	}
	if !strings.Contains(msg, "PROPOSED CONFIGURATION:") {
		t.Error("expected PROPOSED CONFIGURATION section")
	}
	if !strings.Contains(msg, "--- HttpRoute ---") {
		t.Error("expected HttpRoute section")
	}
	if !strings.Contains(msg, "--- SecurityPolicy ---") {
		t.Error("expected SecurityPolicy section")
	}
}

func TestBuildReviewMessage_UpdateWithCurrentAndProposed(t *testing.T) {
	req := ReviewRequest{
		Action: "update",
		CurrentYaml: &YamlSet{
			HttpRoute: "old-config",
		},
		ProposedYaml: &YamlSet{
			HttpRoute: "new-config",
		},
	}

	msg := BuildReviewMessage(req)

	if !strings.Contains(msg, "ACTION: update") {
		t.Error("expected ACTION: update")
	}
	if !strings.Contains(msg, "CURRENT CONFIGURATION:") {
		t.Error("expected CURRENT CONFIGURATION section")
	}
	if !strings.Contains(msg, "PROPOSED CONFIGURATION:") {
		t.Error("expected PROPOSED CONFIGURATION section")
	}
}

func TestBuildReviewMessage_DeleteNoYaml(t *testing.T) {
	req := ReviewRequest{
		Action:      "delete",
		Description: "Removing deprecated route",
	}

	msg := BuildReviewMessage(req)

	if !strings.Contains(msg, "ACTION: delete") {
		t.Error("expected ACTION: delete")
	}
	if !strings.Contains(msg, "Removing deprecated route") {
		t.Error("expected description")
	}
	// No YAML sections
	if strings.Contains(msg, "CURRENT CONFIGURATION:") {
		t.Error("expected no CURRENT CONFIGURATION when currentYaml is nil")
	}
	if strings.Contains(msg, "PROPOSED CONFIGURATION:") {
		t.Error("expected no PROPOSED CONFIGURATION when proposedYaml is nil")
	}
}
