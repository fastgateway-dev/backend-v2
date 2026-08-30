package ai

import "context"

// Provider defines the interface for AI providers
type Provider interface {
	// StreamGenerate generates routes and streams the response
	StreamGenerate(ctx context.Context, req GenerateRequest, chunks chan<- StreamChunk) error

	// Generate generates a complete (non-streaming) response
	Generate(ctx context.Context, systemPrompt string, userMessage string) (string, error)

	// StreamChat streams a chat response (plain text, no route parsing)
	StreamChat(ctx context.Context, systemPrompt string, messages []ChatMessage, chunks chan<- StreamChunk) error

	// Name returns the provider name
	Name() string
}

// NewProvider creates an AI provider based on configuration
func NewProvider(providerName, apiKey, model string, maxTokens int, baseURL string) (Provider, error) {
	switch providerName {
	case "anthropic":
		return NewAnthropicProvider(apiKey, model, maxTokens), nil
	case "openai":
		return NewOpenAIProvider(apiKey, model, maxTokens, "https://api.openai.com"), nil
	case "gemini":
		return NewGeminiProvider(apiKey, model, maxTokens), nil
	case "deepseek":
		return NewOpenAIProvider(apiKey, model, maxTokens, "https://api.deepseek.com"), nil
	case "openai_compatible":
		return NewOpenAIProvider(apiKey, model, maxTokens, baseURL), nil
	default:
		return NewAnthropicProvider(apiKey, model, maxTokens), nil
	}
}
