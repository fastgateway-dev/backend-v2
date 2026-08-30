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

// OpenAIProvider implements the Provider interface for OpenAI-compatible APIs
type OpenAIProvider struct {
	apiKey    string
	model     string
	maxTokens int
	baseURL   string
	client    *http.Client
}

// NewOpenAIProvider creates a new OpenAI-compatible provider
func NewOpenAIProvider(apiKey, model string, maxTokens int, baseURL string) *OpenAIProvider {
	if model == "" {
		model = "gpt-4o"
	}
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	// Trim trailing slash
	baseURL = strings.TrimRight(baseURL, "/")
	return &OpenAIProvider{
		apiKey:    apiKey,
		model:     model,
		maxTokens: maxTokens,
		baseURL:   baseURL,
		client:    &http.Client{},
	}
}

// Name returns the provider name
func (p *OpenAIProvider) Name() string {
	return "openai"
}

// openaiRequest represents the request to OpenAI API
type openaiRequest struct {
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	Stream    bool            `json:"stream"`
	Messages  []openaiMessage `json:"messages"`
}

type openaiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Generate generates a complete (non-streaming) response from OpenAI
func (p *OpenAIProvider) Generate(ctx context.Context, systemPrompt string, userMessage string) (string, error) {
	apiReq := openaiRequest{
		Model:     p.model,
		MaxTokens: p.maxTokens,
		Stream:    false,
		Messages: []openaiMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userMessage},
		},
	}

	body, err := json.Marshal(apiReq)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("failed to connect to OpenAI API: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openai API error %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no choices in OpenAI response")
	}

	return result.Choices[0].Message.Content, nil
}

// StreamGenerate implements streaming generation for OpenAI
func (p *OpenAIProvider) StreamGenerate(ctx context.Context, req GenerateRequest, chunks chan<- StreamChunk) error {
	defer close(chunks)

	// Build request
	apiReq := openaiRequest{
		Model:     p.model,
		MaxTokens: p.maxTokens,
		Stream:    true,
		Messages: []openaiMessage{
			{Role: "system", Content: BuildSystemPrompt(req.FormatHint)},
			{Role: "user", Content: BuildUserMessage(req)},
		},
	}

	body, err := json.Marshal(apiReq)
	if err != nil {
		chunks <- StreamChunk{Type: ChunkTypeError, Error: "Failed to build request"}
		return err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		chunks <- StreamChunk{Type: ChunkTypeError, Error: "Failed to create request"}
		return err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

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
		return fmt.Errorf("openai API error: %d", resp.StatusCode)
	}

	// Parse SSE stream from OpenAI
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
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		}

		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		if len(event.Choices) > 0 {
			content := event.Choices[0].Delta.Content
			if content != "" {
				fullContent.WriteString(content)
				chunks <- StreamChunk{Type: ChunkTypeContent, Content: content}
			}
			if event.Choices[0].FinishReason == "stop" {
				break
			}
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

// StreamChat streams a chat response (plain text, no route parsing)
func (p *OpenAIProvider) StreamChat(ctx context.Context, systemPrompt string, messages []ChatMessage, chunks chan<- StreamChunk) error {
	defer close(chunks)

	apiMessages := []openaiMessage{
		{Role: "system", Content: systemPrompt},
	}
	for _, m := range messages {
		apiMessages = append(apiMessages, openaiMessage{Role: m.Role, Content: m.Content})
	}

	apiReq := openaiRequest{
		Model:     p.model,
		MaxTokens: p.maxTokens,
		Stream:    true,
		Messages:  apiMessages,
	}

	body, err := json.Marshal(apiReq)
	if err != nil {
		chunks <- StreamChunk{Type: ChunkTypeError, Error: "Failed to build request"}
		return err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		chunks <- StreamChunk{Type: ChunkTypeError, Error: "Failed to create request"}
		return err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

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
		return fmt.Errorf("openai API error: %d", resp.StatusCode)
	}

	// Parse SSE stream from OpenAI
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
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		}

		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		if len(event.Choices) > 0 {
			content := event.Choices[0].Delta.Content
			if content != "" {
				chunks <- StreamChunk{Type: ChunkTypeContent, Content: content}
			}
			if event.Choices[0].FinishReason == "stop" {
				break
			}
		}
	}

	chunks <- StreamChunk{Type: ChunkTypeDone}
	return nil
}
