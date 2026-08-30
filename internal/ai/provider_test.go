package ai

import (
	"testing"
)

func TestNewProvider_Anthropic(t *testing.T) {
	p, err := NewProvider("anthropic", "test-key", "claude-sonnet-4-20250514", 1024, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "anthropic" {
		t.Errorf("expected name 'anthropic', got %q", p.Name())
	}
}

func TestNewProvider_OpenAI(t *testing.T) {
	p, err := NewProvider("openai", "test-key", "gpt-4o", 1024, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "openai" {
		t.Errorf("expected name 'openai', got %q", p.Name())
	}
}

func TestNewProvider_Gemini(t *testing.T) {
	p, err := NewProvider("gemini", "test-key", "gemini-2.0-flash", 1024, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "gemini" {
		t.Errorf("expected name 'gemini', got %q", p.Name())
	}
}

func TestNewProvider_Deepseek(t *testing.T) {
	p, err := NewProvider("deepseek", "test-key", "deepseek-chat", 1024, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// deepseek uses OpenAI provider under the hood
	if p.Name() != "openai" {
		t.Errorf("expected name 'openai' (via OpenAI provider), got %q", p.Name())
	}
}

func TestNewProvider_OpenAICompatible(t *testing.T) {
	p, err := NewProvider("openai_compatible", "test-key", "my-model", 1024, "https://my-api.example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "openai" {
		t.Errorf("expected name 'openai' (via OpenAI provider), got %q", p.Name())
	}
}

func TestNewProvider_UnknownDefaultsToAnthropic(t *testing.T) {
	// The code defaults to anthropic for unknown providers (no error returned)
	p, err := NewProvider("unknown_provider", "test-key", "", 1024, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "anthropic" {
		t.Errorf("expected default to 'anthropic', got %q", p.Name())
	}
}

func TestParseAIResponse_ValidJSONWithRoutes(t *testing.T) {
	input := `{
		"summary": "Created an API route",
		"routes": [
			{
				"name": "my-route",
				"description": "A test route",
				"config": {
					"matches": [{"path": {"type": "Prefix", "value": "/api"}}],
					"backends": [{"service": "my-svc", "port": 8080}]
				}
			}
		]
	}`

	routes, err := parseAIResponse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}
	if routes[0].Name != "my-route" {
		t.Errorf("expected route name 'my-route', got %q", routes[0].Name)
	}
}

func TestParseAIResponse_WrappedInMarkdownCodeBlock(t *testing.T) {
	input := "Here is the config:\n```json\n" + `{
		"summary": "test",
		"routes": [
			{
				"name": "wrapped-route",
				"config": {
					"matches": [{"path": {"type": "Prefix", "value": "/"}}]
				}
			}
		]
	}` + "\n```\nDone!"

	routes, err := parseAIResponse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}
	if routes[0].Name != "wrapped-route" {
		t.Errorf("expected 'wrapped-route', got %q", routes[0].Name)
	}
}

func TestParseAIResponse_InvalidJSON(t *testing.T) {
	input := "This is not JSON at all, just plain text."
	_, err := parseAIResponse(input)
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestParseAIResponse_SingleRouteObject(t *testing.T) {
	// parseAIResponse looks for { ... } and tries to unmarshal as {summary, routes}
	// A single route object won't have "routes" key, so it falls through.
	// The function tries array parse next but a single object isn't an array either.
	// This should return empty routes (no "routes" key in the object).
	input := `{
		"name": "single-route",
		"config": {
			"matches": [{"path": {"type": "Prefix", "value": "/test"}}]
		}
	}`

	routes, err := parseAIResponse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The response struct has Routes field which will be nil/empty since the JSON
	// doesn't have a "routes" key. The function returns response.Routes which is nil.
	if routes != nil && len(routes) != 0 {
		t.Errorf("expected empty/nil routes for single object without 'routes' key, got %d", len(routes))
	}
}

func TestParseAIResponse_MultipleRoutes(t *testing.T) {
	input := `{
		"summary": "Two routes",
		"routes": [
			{
				"name": "route-a",
				"config": {"matches": [{"path": {"type": "Prefix", "value": "/a"}}]}
			},
			{
				"name": "route-b",
				"config": {"matches": [{"path": {"type": "Exact", "value": "/b"}}]}
			}
		]
	}`

	routes, err := parseAIResponse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(routes))
	}
	if routes[0].Name != "route-a" {
		t.Errorf("expected 'route-a', got %q", routes[0].Name)
	}
	if routes[1].Name != "route-b" {
		t.Errorf("expected 'route-b', got %q", routes[1].Name)
	}
}

func TestParseAIResponse_NoJSONContent(t *testing.T) {
	input := "No braces here at all"
	_, err := parseAIResponse(input)
	if err == nil {
		t.Error("expected error for content with no JSON")
	}
}
