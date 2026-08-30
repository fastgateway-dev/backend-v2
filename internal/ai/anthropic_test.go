package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAnthropicProvider_Name(t *testing.T) {
	p := NewAnthropicProvider("test-key", "claude-sonnet-4-20250514", 1024)
	if p.Name() != "anthropic" {
		t.Errorf("expected 'anthropic', got %q", p.Name())
	}
}

func TestAnthropicProvider_DefaultModel(t *testing.T) {
	p := NewAnthropicProvider("test-key", "", 1024)
	if p.model != "claude-sonnet-4-20250514" {
		t.Errorf("expected default model 'claude-sonnet-4-20250514', got %q", p.model)
	}
}

// redirectTransport redirects all requests to a local test server
type redirectTransport struct {
	targetURL string
	inner     http.RoundTripper
}

func (rt *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	newURL := rt.targetURL + req.URL.Path
	if req.URL.RawQuery != "" {
		newURL += "?" + req.URL.RawQuery
	}
	newReq, err := http.NewRequestWithContext(req.Context(), req.Method, newURL, req.Body)
	if err != nil {
		return nil, err
	}
	newReq.Header = req.Header
	return rt.inner.RoundTrip(newReq)
}

func newAnthropicWithMockServer(server *httptest.Server, apiKey, model string, maxTokens int) *AnthropicProvider {
	p := NewAnthropicProvider(apiKey, model, maxTokens)
	p.client = &http.Client{
		Transport: &redirectTransport{
			targetURL: server.URL,
			inner:     http.DefaultTransport,
		},
	}
	return p
}

func TestAnthropicProvider_Generate_MockServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/v1/messages" {
			t.Errorf("expected /v1/messages, got %s", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "test-api-key" {
			t.Errorf("expected x-api-key header, got %q", r.Header.Get("x-api-key"))
		}
		if r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Errorf("expected anthropic-version header")
		}

		var reqBody anthropicRequest
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Errorf("failed to decode request body: %v", err)
		}
		if reqBody.Stream {
			t.Error("expected stream=false for Generate")
		}

		resp := map[string]interface{}{
			"content": []map[string]interface{}{
				{"type": "text", "text": "Hello from mock Anthropic!"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := newAnthropicWithMockServer(server, "test-api-key", "claude-sonnet-4-20250514", 1024)
	result, err := p.Generate(context.Background(), "You are helpful.", "Say hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Hello from mock Anthropic!" {
		t.Errorf("expected 'Hello from mock Anthropic!', got %q", result)
	}
}

func TestAnthropicProvider_Generate_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":{"type":"server_error","message":"internal error"}}`))
	}))
	defer server.Close()

	p := newAnthropicWithMockServer(server, "test-key", "", 1024)
	_, err := p.Generate(context.Background(), "system", "user")
	if err == nil {
		t.Error("expected error for 500 response")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected error to contain 500, got: %v", err)
	}
}

func TestAnthropicProvider_Generate_NoTextContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"content": []map[string]interface{}{
				{"type": "image", "source": "data:image/png;base64,..."},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := newAnthropicWithMockServer(server, "test-key", "", 1024)
	_, err := p.Generate(context.Background(), "system", "user")
	if err == nil {
		t.Error("expected error when no text content blocks")
	}
}

func TestAnthropicProvider_Generate_MalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{not valid json}`))
	}))
	defer server.Close()

	p := newAnthropicWithMockServer(server, "test-key", "", 1024)
	_, err := p.Generate(context.Background(), "system", "user")
	if err == nil {
		t.Error("expected error for malformed JSON response")
	}
}

func TestAnthropicProvider_StreamGenerate_MockSSE(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody anthropicRequest
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Errorf("failed to decode request body: %v", err)
		}
		if !reqBody.Stream {
			t.Error("expected stream=true for StreamGenerate")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		events := []string{
			`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"hello"}}`,
			`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":" world"}}`,
			`data: {"type":"message_stop"}`,
		}
		for _, event := range events {
			w.Write([]byte(event + "\n"))
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	defer server.Close()

	p := newAnthropicWithMockServer(server, "test-key", "", 1024)
	chunks := make(chan StreamChunk, 20)

	go func() {
		err := p.StreamGenerate(context.Background(), GenerateRequest{
			Mode:  ModeNaturalLanguage,
			Input: "test input",
		}, chunks)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	}()

	var contentChunks []string
	var gotDone bool
	for chunk := range chunks {
		switch chunk.Type {
		case ChunkTypeContent:
			contentChunks = append(contentChunks, chunk.Content)
		case ChunkTypeDone:
			gotDone = true
		case ChunkTypeError:
			t.Errorf("unexpected error chunk: %s", chunk.Error)
		}
	}

	if len(contentChunks) != 2 {
		t.Errorf("expected 2 content chunks, got %d: %v", len(contentChunks), contentChunks)
	}
	if len(contentChunks) >= 1 && contentChunks[0] != "hello" {
		t.Errorf("expected first chunk 'hello', got %q", contentChunks[0])
	}
	if len(contentChunks) >= 2 && contentChunks[1] != " world" {
		t.Errorf("expected second chunk ' world', got %q", contentChunks[1])
	}
	if !gotDone {
		t.Error("expected done chunk")
	}
}

func TestAnthropicProvider_StreamGenerate_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"type":"rate_limit_error","message":"rate limited"}}`))
	}))
	defer server.Close()

	p := newAnthropicWithMockServer(server, "test-key", "", 1024)
	chunks := make(chan StreamChunk, 10)

	err := p.StreamGenerate(context.Background(), GenerateRequest{
		Mode:  ModeNaturalLanguage,
		Input: "test",
	}, chunks)

	if err == nil {
		t.Error("expected error for non-200 response")
	}

	var gotErrorChunk bool
	for chunk := range chunks {
		if chunk.Type == ChunkTypeError {
			gotErrorChunk = true
		}
	}
	if !gotErrorChunk {
		t.Error("expected error chunk")
	}
}

func TestAnthropicProvider_StreamChat_MockSSE(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody anthropicRequest
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Errorf("failed to decode request body: %v", err)
		}
		if !reqBody.Stream {
			t.Error("expected stream=true for StreamChat")
		}
		if reqBody.System == "" {
			t.Error("expected system prompt to be set")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		events := []string{
			`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"Sure, "}}`,
			`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"I can help!"}}`,
			`data: {"type":"message_stop"}`,
		}
		for _, event := range events {
			w.Write([]byte(event + "\n"))
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	defer server.Close()

	p := newAnthropicWithMockServer(server, "test-key", "", 1024)
	chunks := make(chan StreamChunk, 20)

	go func() {
		err := p.StreamChat(context.Background(), "You are a helpful assistant", []ChatMessage{
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi!"},
			{Role: "user", Content: "Help me"},
		}, chunks)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	}()

	var contentChunks []string
	var gotDone bool
	for chunk := range chunks {
		switch chunk.Type {
		case ChunkTypeContent:
			contentChunks = append(contentChunks, chunk.Content)
		case ChunkTypeDone:
			gotDone = true
		case ChunkTypeError:
			t.Errorf("unexpected error chunk: %s", chunk.Error)
		}
	}

	if len(contentChunks) != 2 {
		t.Errorf("expected 2 content chunks, got %d", len(contentChunks))
	}
	if !gotDone {
		t.Error("expected done chunk")
	}
}

func TestAnthropicProvider_StreamChat_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"type":"invalid_request","message":"bad request"}}`))
	}))
	defer server.Close()

	p := newAnthropicWithMockServer(server, "test-key", "", 1024)
	chunks := make(chan StreamChunk, 10)

	err := p.StreamChat(context.Background(), "system", []ChatMessage{
		{Role: "user", Content: "test"},
	}, chunks)

	if err == nil {
		t.Error("expected error for non-200 response")
	}

	var gotErrorChunk bool
	for chunk := range chunks {
		if chunk.Type == ChunkTypeError {
			gotErrorChunk = true
		}
	}
	if !gotErrorChunk {
		t.Error("expected error chunk")
	}
}

func TestAnthropicProvider_Generate_CancelledContext(t *testing.T) {
	p := NewAnthropicProvider("test-key", "claude-sonnet-4-20250514", 1024)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := p.Generate(ctx, "system prompt", "user message")
	if err == nil {
		t.Error("expected error with cancelled context, got nil")
	}
}

func TestAnthropicProvider_StreamGenerate_CancelledContext(t *testing.T) {
	p := NewAnthropicProvider("test-key", "claude-sonnet-4-20250514", 1024)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	chunks := make(chan StreamChunk, 10)
	err := p.StreamGenerate(ctx, GenerateRequest{
		Mode:  ModeNaturalLanguage,
		Input: "test",
	}, chunks)

	if err == nil {
		t.Error("expected error with cancelled context, got nil")
	}
}

func TestAnthropicProvider_StreamChat_CancelledContext(t *testing.T) {
	p := NewAnthropicProvider("test-key", "claude-sonnet-4-20250514", 1024)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	chunks := make(chan StreamChunk, 10)
	err := p.StreamChat(ctx, "system", []ChatMessage{
		{Role: "user", Content: "hello"},
	}, chunks)

	if err == nil {
		t.Error("expected error with cancelled context, got nil")
	}
}
