package ai

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ChatSystemPrompt is the base system prompt for the AI chat assistant
const ChatSystemPrompt = `You are a FastGateway configuration assistant. You help users understand and configure their routes and domain settings.

## What You Know
FastGateway manages Kubernetes Gateway API resources. Key concepts:

### Route Configuration
- **Matches**: Path (Prefix/Exact/RegularExpression), methods, headers, query params
- **Backends**: Kubernetes services with port and weight
- **Mirrors**: Traffic mirroring to additional backends
- **URL Rewrite**: Hostname and path rewriting
- **Redirect**: HTTP redirects with configurable scheme, host, port, status code
- **Direct Response**: Static responses with status code and body
- **Header Modifiers**: Add/set/remove request and response headers

### Security (General Mode)
- **CORS**: Cross-origin resource sharing (allowOrigins, allowMethods, allowHeaders, maxAge)
- **Client IP**: Allowlist and denylist with CIDR ranges
- **API Key**: API key authentication with configurable header
- **JWT**: JWT validation with JWKS URI, issuer, audiences, claim-to-header mapping
- **OIDC**: OpenID Connect / SSO (general mode only)
- **External Auth**: External authorization service

### Security (Client Mode)
- Per-client attachments with individual IP allowlists, API keys, JWT configs
- Used for multi-tenant APIs where different clients need different access rules

### Traffic Policy
- **Timeout**: TCP connect timeout, HTTP request timeout
- **Retry**: Retry count, per-try timeout, retryable status codes
- **Circuit Breaker**: Max connections, requests, retries, pending requests
- **Health Check**: Active health checking with HTTP/TCP probes
- **Load Balancing**: ConsistentHash, LeastRequest, Random, RoundRobin
- **Compression**: Gzip compression with min bytes and content types
- **Rate Limiting**: Requests per unit (second/minute/hour) with client selectors

### WAF (Web Application Firewall)
- Coraza/ModSecurity engine with OWASP Core Rule Set (CRS)
- **Mode**: "block" (reject requests) or "detect" (log only)
- **Paranoia Level**: 1-4 (higher = more aggressive, more false positives)
- **Anomaly Threshold**: Score threshold before blocking (default: 5, lower = stricter)
- **Disabled Rule IDs**: Specific OWASP CRS rules to skip
- **Custom Directives**: Raw ModSecurity SecRule directives

Common OWASP CRS rules users may need to disable:
- 920170: Invalid HTTP Request Line (false positive on some APIs)
- 920230: Multiple URL Encoding Detected
- 920300: Request Missing Accept Header
- 920350: Host Header is IP Address
- 942100-942450: SQL Injection Detection (false positives on JSON/XML bodies)
- 941100-941160: XSS Detection (false positives on rich text)
- 949110: Inbound Anomaly Score Exceeded
- 980130: Outbound Anomaly Score Exceeded

### Domain Settings
- **Client Connection**: TCP keepalive, proxy protocol, max connections
- **Client IP Detection**: XFF header, custom header, number of trusted hops
- **Timeout**: Request received timeout, HTTP idle timeout
- **HTTP/3**: QUIC/HTTP3 support
- **TLS**: Profile presets (modern/intermediate/compatible/custom), min/max version, ciphers
- **mTLS**: Mutual TLS with CA certificates, SAN whitelist, hash whitelist

### Extensions
- **Lua**: Custom Lua scripts for request/response processing
- **Wasm**: WebAssembly modules for advanced processing

## Response Guidelines
1. Give concise, actionable advice
2. When suggesting WAF rule IDs to disable, explain what the rule does and why it may cause false positives
3. When discussing security settings, mention trade-offs (security vs convenience)
4. Use plain language, not marketing speak
5. If you don't know something specific to FastGateway, say so
6. Format responses in plain text. Use dashes for lists and indentation for structure. Avoid markdown headers (##), bold (**), and code fences
7. When the user's current configuration has potential issues, proactively mention them`

// BuildChatSystemPrompt builds the complete system prompt with context
func BuildChatSystemPrompt(context *ChatContext) string {
	if context == nil {
		return ChatSystemPrompt
	}

	// Write YAML blocks to a temp buffer first to check if any content exists
	var yamlBuf strings.Builder
	switch context.Type {
	case "route":
		if len(context.Route) > 0 {
			writeYAMLBlocks(&yamlBuf, context.Route)
		}
	case "domain":
		if len(context.Domain) > 0 {
			writeYAMLBlocks(&yamlBuf, context.Domain)
		}
	}

	var sb strings.Builder
	sb.WriteString(ChatSystemPrompt)

	if yamlBuf.Len() > 0 {
		sb.WriteString("\n\n## Current Configuration\n")
		sb.WriteString("You have the user's current Kubernetes Gateway API manifests below. These are generated from their form configuration in real time. You CAN see their configuration — never say otherwise.\n\n")
		sb.WriteString(yamlBuf.String())
		sb.WriteString("\nAnalyze these manifests to answer the user's questions. Reference specific fields, values, and potential issues.")
	}

	return sb.String()
}

// yamlBlockOrder defines the deterministic order for rendering YAML blocks.
// Fixed order is important for LLM prompt caching (prefix-match based).
var yamlBlockOrder = []struct {
	key   string
	label string
}{
	{"httpRoute", "HTTPRoute"},
	{"securityPolicy", "SecurityPolicy"},
	{"backendTrafficPolicy", "BackendTrafficPolicy"},
	{"envoyExtensionPolicy", "EnvoyExtensionPolicy"},
	{"backend", "Backend"},
	{"httpRouteFilter", "HTTPRouteFilter"},
	{"configMap", "ConfigMap"},
	{"clientTrafficPolicy", "ClientTrafficPolicy"},
}

// writeYAMLBlocks parses a JSON object with YAML string values and writes each as a labeled block
func writeYAMLBlocks(sb *strings.Builder, raw json.RawMessage) {
	var fields map[string]string
	if err := json.Unmarshal(raw, &fields); err != nil {
		// Fallback: write as raw JSON (backwards compat with old format)
		writeRawJSONFallback(sb, raw)
		return
	}

	// Iterate in deterministic order
	wrote := false
	for _, entry := range yamlBlockOrder {
		yaml := fields[entry.key]
		if yaml == "" {
			continue
		}
		sb.WriteString(fmt.Sprintf("### %s\n```yaml\n%s\n```\n\n", entry.label, yaml))
		wrote = true
	}

	// If the input was non-empty but none of its keys matched the known
	// manifest allow-list (e.g. a domain context like {"hostname":"..."}),
	// fall back to rendering the raw JSON so the context still reaches the
	// model instead of being silently dropped.
	if !wrote && len(fields) > 0 {
		writeRawJSONFallback(sb, raw)
	}
}

// writeRawJSONFallback writes raw as a formatted JSON block. Used both when
// raw isn't a map[string]string at all, and when it is but none of its keys
// are in yamlBlockOrder.
func writeRawJSONFallback(sb *strings.Builder, raw json.RawMessage) {
	formatted, fmtErr := json.MarshalIndent(raw, "", "  ")
	if fmtErr == nil {
		sb.WriteString("Configuration:\n```json\n")
		sb.Write(formatted)
		sb.WriteString("\n```\n")
	}
}

// BuildChatMessages converts ChatRequest history + new message into provider messages
func BuildChatMessages(req ChatRequest) []ChatMessage {
	messages := make([]ChatMessage, 0, len(req.History)+1)
	messages = append(messages, req.History...)
	messages = append(messages, ChatMessage{Role: "user", Content: req.Message})
	return messages
}
