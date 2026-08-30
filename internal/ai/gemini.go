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

// GeminiProvider implements the Provider interface for Google Gemini
type GeminiProvider struct {
	apiKey    string
	model     string
	maxTokens int
	baseURL   string
	client    *http.Client
}

// NewGeminiProvider creates a new Gemini provider
func NewGeminiProvider(apiKey, model string, maxTokens int) *GeminiProvider {
	if model == "" {
		model = "gemini-2.0-flash"
	}
	return &GeminiProvider{
		apiKey:    apiKey,
		model:     model,
		maxTokens: maxTokens,
		baseURL:   "https://generativelanguage.googleapis.com",
		client:    &http.Client{},
	}
}

// Name returns the provider name
func (p *GeminiProvider) Name() string {
	return "gemini"
}

// geminiRequest represents the request body for Gemini API
type geminiRequest struct {
	SystemInstruction *geminiContent  `json:"system_instruction,omitempty"`
	Contents          []geminiContent `json:"contents"`
	GenerationConfig  geminiGenConfig `json:"generationConfig"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiGenConfig struct {
	MaxOutputTokens int `json:"maxOutputTokens"`
}

// geminiResponse represents a Gemini API response (or a streaming chunk)
type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	Error *struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
	} `json:"error"`
}

// Generate generates a complete (non-streaming) response from Gemini
func (p *GeminiProvider) Generate(ctx context.Context, systemPrompt string, userMessage string) (string, error) {
	apiReq := geminiRequest{
		SystemInstruction: &geminiContent{
			Parts: []geminiPart{{Text: systemPrompt}},
		},
		Contents: []geminiContent{
			{Role: "user", Parts: []geminiPart{{Text: userMessage}}},
		},
		GenerationConfig: geminiGenConfig{MaxOutputTokens: p.maxTokens},
	}

	body, err := json.Marshal(apiReq)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s", p.baseURL, p.model, p.apiKey)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("failed to connect to Gemini API: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gemini API error %d: %s", resp.StatusCode, string(respBody))
	}

	var result geminiResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	if result.Error != nil {
		return "", fmt.Errorf("gemini API error: %s", result.Error.Message)
	}

	if len(result.Candidates) == 0 || len(result.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("no content in Gemini response")
	}

	return result.Candidates[0].Content.Parts[0].Text, nil
}

// StreamGenerate implements streaming generation for Gemini
func (p *GeminiProvider) StreamGenerate(ctx context.Context, req GenerateRequest, chunks chan<- StreamChunk) error {
	defer close(chunks)

	apiReq := geminiRequest{
		SystemInstruction: &geminiContent{
			Parts: []geminiPart{{Text: BuildSystemPrompt(req.FormatHint)}},
		},
		Contents: []geminiContent{
			{Role: "user", Parts: []geminiPart{{Text: BuildUserMessage(req)}}},
		},
		GenerationConfig: geminiGenConfig{MaxOutputTokens: p.maxTokens},
	}

	body, err := json.Marshal(apiReq)
	if err != nil {
		chunks <- StreamChunk{Type: ChunkTypeError, Error: "Failed to build request"}
		return err
	}

	url := fmt.Sprintf("%s/v1beta/models/%s:streamGenerateContent?alt=sse&key=%s", p.baseURL, p.model, p.apiKey)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		chunks <- StreamChunk{Type: ChunkTypeError, Error: "Failed to create request"}
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")

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
		return fmt.Errorf("gemini API error: %d", resp.StatusCode)
	}

	// Gemini streaming with alt=sse returns SSE format: "data: {json}\n\n"
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

		var event geminiResponse
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		if len(event.Candidates) > 0 && len(event.Candidates[0].Content.Parts) > 0 {
			content := event.Candidates[0].Content.Parts[0].Text
			if content != "" {
				fullContent.WriteString(content)
				chunks <- StreamChunk{Type: ChunkTypeContent, Content: content}
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
func (p *GeminiProvider) StreamChat(ctx context.Context, systemPrompt string, messages []ChatMessage, chunks chan<- StreamChunk) error {
	defer close(chunks)

	contents := make([]geminiContent, 0, len(messages))
	for _, m := range messages {
		role := m.Role
		if role == "assistant" {
			role = "model" // Gemini uses "model" instead of "assistant"
		}
		contents = append(contents, geminiContent{
			Role:  role,
			Parts: []geminiPart{{Text: m.Content}},
		})
	}

	apiReq := geminiRequest{
		SystemInstruction: &geminiContent{
			Parts: []geminiPart{{Text: systemPrompt}},
		},
		Contents:         contents,
		GenerationConfig: geminiGenConfig{MaxOutputTokens: p.maxTokens},
	}

	body, err := json.Marshal(apiReq)
	if err != nil {
		chunks <- StreamChunk{Type: ChunkTypeError, Error: "Failed to build request"}
		return err
	}

	url := fmt.Sprintf("%s/v1beta/models/%s:streamGenerateContent?alt=sse&key=%s", p.baseURL, p.model, p.apiKey)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		chunks <- StreamChunk{Type: ChunkTypeError, Error: "Failed to create request"}
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")

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
		return fmt.Errorf("gemini API error: %d", resp.StatusCode)
	}

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

		var event geminiResponse
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		if len(event.Candidates) > 0 && len(event.Candidates[0].Content.Parts) > 0 {
			content := event.Candidates[0].Content.Parts[0].Text
			if content != "" {
				chunks <- StreamChunk{Type: ChunkTypeContent, Content: content}
			}
		}
	}

	chunks <- StreamChunk{Type: ChunkTypeDone}
	return nil
}
