package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// AnthropicProvider implements the Provider interface for Anthropic Claude
type AnthropicProvider struct {
	apiKey    string
	model     string
	maxTokens int
	client    *http.Client
}

// NewAnthropicProvider creates a new Anthropic provider
func NewAnthropicProvider(apiKey, model string, maxTokens int) *AnthropicProvider {
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}
	return &AnthropicProvider{
		apiKey:    apiKey,
		model:     model,
		maxTokens: maxTokens,
		client:    &http.Client{},
	}
}

// Name returns the provider name
func (p *AnthropicProvider) Name() string {
	return "anthropic"
}

// anthropicRequest represents the request to Anthropic API
type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	Stream    bool               `json:"stream"`
	System    string             `json:"system"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// StreamGenerate implements streaming generation for Anthropic
func (p *AnthropicProvider) StreamGenerate(ctx context.Context, req GenerateRequest, chunks chan<- StreamChunk) error {
	defer close(chunks)

	// Build request
	apiReq := anthropicRequest{
		Model:     p.model,
		MaxTokens: p.maxTokens,
		Stream:    true,
		System:    BuildSystemPrompt(req.FormatHint),
		Messages: []anthropicMessage{
			{Role: "user", Content: BuildUserMessage(req)},
		},
	}

	body, err := json.Marshal(apiReq)
	if err != nil {
		chunks <- StreamChunk{Type: ChunkTypeError, Error: "Failed to build request"}
		return err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	if err != nil {
		chunks <- StreamChunk{Type: ChunkTypeError, Error: "Failed to create request"}
		return err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		chunks <- StreamChunk{Type: ChunkTypeError, Error: "Failed to connect to AI service"}
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		errMsg := fmt.Sprintf("AI service error: %s", string(body))
		chunks <- StreamChunk{Type: ChunkTypeError, Error: errMsg}
		return fmt.Errorf("anthropic API error: %d", resp.StatusCode)
	}

	// Parse SSE stream from Anthropic
	var fullContent strings.Builder
	reader := bufio.NewReader(resp.Body)

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}

		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var event struct {
			Type  string `json:"type"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}

		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		if event.Type == "content_block_delta" && event.Delta.Type == "text_delta" {
			fullContent.WriteString(event.Delta.Text)
			// Stream content chunks for UI display
			chunks <- StreamChunk{Type: ChunkTypeContent, Content: event.Delta.Text}
		}

		if event.Type == "message_stop" {
			break
		}
	}

	// Parse the complete response to extract routes
	content := fullContent.String()
	routes, err := parseAIResponse(content)
	if err != nil {
		chunks <- StreamChunk{Type: ChunkTypeWarning, Warning: &Warning{
			Category: "parse_error",
			Message:  "Could not parse structured response, showing raw output",
			Severity: "warning",
		}}
	} else {
		// Send each route as a separate chunk
		for i, route := range routes {
			chunks <- StreamChunk{
				Type:  ChunkTypeRoute,
				Route: &route,
				Index: i,
			}
		}
	}

	chunks <- StreamChunk{Type: ChunkTypeDone, Total: len(routes)}
	return nil
}

// Generate generates a complete (non-streaming) response from Anthropic
func (p *AnthropicProvider) Generate(ctx context.Context, systemPrompt string, userMessage string) (string, error) {
	apiReq := anthropicRequest{
		Model:     p.model,
		MaxTokens: p.maxTokens,
		Stream:    false,
		System:    systemPrompt,
		Messages: []anthropicMessage{
			{Role: "user", Content: userMessage},
		},
	}

	body, err := json.Marshal(apiReq)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("failed to connect to Anthropic API: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("anthropic API error %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}

	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	for _, block := range result.Content {
		if block.Type == "text" {
			return block.Text, nil
		}
	}

	return "", fmt.Errorf("no text content in Anthropic response")
}

// StreamChat streams a chat response (plain text, no route parsing)
func (p *AnthropicProvider) StreamChat(ctx context.Context, systemPrompt string, messages []ChatMessage, chunks chan<- StreamChunk) error {
	defer close(chunks)

	apiMessages := make([]anthropicMessage, len(messages))
	for i, m := range messages {
		apiMessages[i] = anthropicMessage{Role: m.Role, Content: m.Content}
	}

	apiReq := anthropicRequest{
		Model:     p.model,
		MaxTokens: p.maxTokens,
		Stream:    true,
		System:    systemPrompt,
		Messages:  apiMessages,
	}

	body, err := json.Marshal(apiReq)
	if err != nil {
		chunks <- StreamChunk{Type: ChunkTypeError, Error: "Failed to build request"}
		return err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	if err != nil {
		chunks <- StreamChunk{Type: ChunkTypeError, Error: "Failed to create request"}
		return err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		chunks <- StreamChunk{Type: ChunkTypeError, Error: "Failed to connect to AI service"}
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		errMsg := fmt.Sprintf("AI service error: %s", string(body))
		chunks <- StreamChunk{Type: ChunkTypeError, Error: errMsg}
		return fmt.Errorf("anthropic API error: %d", resp.StatusCode)
	}

	// Parse SSE stream from Anthropic
	reader := bufio.NewReader(resp.Body)

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}

		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var event struct {
			Type  string `json:"type"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}

		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		if event.Type == "content_block_delta" && event.Delta.Type == "text_delta" {
			chunks <- StreamChunk{Type: ChunkTypeContent, Content: event.Delta.Text}
		}

		if event.Type == "message_stop" {
			break
		}
	}

	chunks <- StreamChunk{Type: ChunkTypeDone}
	return nil
}

// parseAIResponse extracts routes from the AI response
func parseAIResponse(content string) ([]GeneratedRoute, error) {
	// Try to find JSON in the response
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")

	if start == -1 || end == -1 || end < start {
		return nil, fmt.Errorf("no JSON found in response")
	}

	jsonStr := content[start : end+1]

	var response struct {
		Summary string           `json:"summary"`
		Routes  []GeneratedRoute `json:"routes"`
	}

	if err := json.Unmarshal([]byte(jsonStr), &response); err != nil {
		// Try parsing as array directly
		var routes []GeneratedRoute
		if err2 := json.Unmarshal([]byte(jsonStr), &routes); err2 != nil {
			return nil, err
		}
		return routes, nil
	}

	return response.Routes, nil
}
