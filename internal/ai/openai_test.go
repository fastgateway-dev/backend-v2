package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIProvider_Name(t *testing.T) {
	p := NewOpenAIProvider("test-key", "gpt-4o", 1024, "https://api.openai.com")
	if p.Name() != "openai" {
		t.Errorf("expected 'openai', got %q", p.Name())
	}
}

func TestOpenAIProvider_DefaultModel(t *testing.T) {
	p := NewOpenAIProvider("test-key", "", 1024, "")
	if p.model != "gpt-4o" {
		t.Errorf("expected default model 'gpt-4o', got %q", p.model)
	}
}

func TestOpenAIProvider_DefaultBaseURL(t *testing.T) {
	p := NewOpenAIProvider("test-key", "", 1024, "")
	if p.baseURL != "https://api.openai.com" {
		t.Errorf("expected default baseURL 'https://api.openai.com', got %q", p.baseURL)
	}
}

func TestOpenAIProvider_TrailingSlashTrimmed(t *testing.T) {
	p := NewOpenAIProvider("test-key", "", 1024, "https://example.com/")
	if p.baseURL != "https://example.com" {
		t.Errorf("expected trailing slash trimmed, got %q", p.baseURL)
	}
}

func TestOpenAIProvider_Generate_MockServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("expected /v1/chat/completions, got %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-api-key" {
			t.Errorf("expected Bearer auth header, got %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected application/json content type")
		}

		// Decode and verify request body
		var reqBody openaiRequest
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Errorf("failed to decode request body: %v", err)
		}
		if reqBody.Model != "gpt-4o" {
			t.Errorf("expected model 'gpt-4o', got %q", reqBody.Model)
		}
		if reqBody.Stream {
			t.Error("expected stream=false for Generate")
		}

		// Return mock response
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{
				{
					"message": map[string]interface{}{
						"content": "Hello from mock OpenAI!",
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewOpenAIProvider("test-api-key", "gpt-4o", 1024, server.URL)
	result, err := p.Generate(context.Background(), "You are helpful.", "Say hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Hello from mock OpenAI!" {
		t.Errorf("expected 'Hello from mock OpenAI!', got %q", result)
	}
}

func TestOpenAIProvider_Generate_EmptyAPIKey_NoAuthHeader(t *testing.T) {
	var receivedAuthHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuthHeader = r.Header.Get("Authorization")
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]interface{}{"content": "ok"}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewOpenAIProvider("", "gpt-4o", 1024, server.URL)
	_, err := p.Generate(context.Background(), "system", "user")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receivedAuthHeader != "" {
		t.Errorf("expected no Authorization header with empty API key, got %q", receivedAuthHeader)
	}
}

func TestOpenAIProvider_Generate_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "internal server error"}`))
	}))
	defer server.Close()

	p := NewOpenAIProvider("test-key", "gpt-4o", 1024, server.URL)
	_, err := p.Generate(context.Background(), "system", "user")
	if err == nil {
		t.Error("expected error on server 500, got nil")
	}
}

func TestOpenAIProvider_Generate_EmptyChoices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewOpenAIProvider("test-key", "gpt-4o", 1024, server.URL)
	_, err := p.Generate(context.Background(), "system", "user")
	if err == nil {
		t.Error("expected error for empty choices, got nil")
	}
}

func TestOpenAIProvider_Generate_CancelledContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// This should not be reached with cancelled context
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]interface{}{"content": "ok"}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewOpenAIProvider("test-key", "gpt-4o", 1024, server.URL)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := p.Generate(ctx, "system", "user")
	if err == nil {
		t.Error("expected error with cancelled context, got nil")
	}
}

func TestOpenAIProvider_StreamGenerate_CancelledContext(t *testing.T) {
	p := NewOpenAIProvider("test-key", "gpt-4o", 1024, "https://api.openai.com")

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

func TestOpenAIProvider_StreamGenerate_MockSSE(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("expected /v1/chat/completions, got %s", r.URL.Path)
		}
		// Verify stream=true in request body
		var reqBody openaiRequest
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Errorf("failed to decode request body: %v", err)
		}
		if !reqBody.Stream {
			t.Error("expected stream=true for StreamGenerate")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		// Send SSE events
		events := []string{
			`data: {"choices":[{"delta":{"content":"hello"},"finish_reason":null}]}`,
			`data: {"choices":[{"delta":{"content":" world"},"finish_reason":null}]}`,
			`data: {"choices":[{"delta":{"content":""},"finish_reason":"stop"}]}`,
			`data: [DONE]`,
		}
		for _, event := range events {
			w.Write([]byte(event + "\n"))
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	defer server.Close()

	p := NewOpenAIProvider("test-api-key", "gpt-4o", 1024, server.URL)
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

func TestOpenAIProvider_StreamGenerate_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer server.Close()

	p := NewOpenAIProvider("test-key", "gpt-4o", 1024, server.URL)
	chunks := make(chan StreamChunk, 10)

	err := p.StreamGenerate(context.Background(), GenerateRequest{
		Mode:  ModeNaturalLanguage,
		Input: "test",
	}, chunks)

	if err == nil {
		t.Error("expected error for non-200 response")
	}

	// Channel should be closed, drain and check for error chunk
	var gotErrorChunk bool
	for chunk := range chunks {
		if chunk.Type == ChunkTypeError {
			gotErrorChunk = true
		}
	}
	if !gotErrorChunk {
		t.Error("expected an error chunk before channel close")
	}
}

func TestOpenAIProvider_StreamChat_MockSSE(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody openaiRequest
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Errorf("failed to decode request body: %v", err)
		}
		if !reqBody.Stream {
			t.Error("expected stream=true for StreamChat")
		}
		// Verify messages include system + user messages
		if len(reqBody.Messages) < 2 {
			t.Errorf("expected at least 2 messages (system + user), got %d", len(reqBody.Messages))
		}
		if reqBody.Messages[0].Role != "system" {
			t.Errorf("expected first message role 'system', got %q", reqBody.Messages[0].Role)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		events := []string{
			`data: {"choices":[{"delta":{"content":"Hi"},"finish_reason":null}]}`,
			`data: {"choices":[{"delta":{"content":" there!"},"finish_reason":null}]}`,
			`data: {"choices":[{"delta":{"content":""},"finish_reason":"stop"}]}`,
			`data: [DONE]`,
		}
		for _, event := range events {
			w.Write([]byte(event + "\n"))
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	defer server.Close()

	p := NewOpenAIProvider("test-key", "gpt-4o", 1024, server.URL)
	chunks := make(chan StreamChunk, 20)

	go func() {
		err := p.StreamChat(context.Background(), "You are a helpful assistant", []ChatMessage{
			{Role: "user", Content: "Hello"},
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

func TestOpenAIProvider_StreamChat_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"server error"}`))
	}))
	defer server.Close()

	p := NewOpenAIProvider("test-key", "gpt-4o", 1024, server.URL)
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

func TestOpenAIProvider_Generate_MalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{not valid json`))
	}))
	defer server.Close()

	p := NewOpenAIProvider("test-key", "gpt-4o", 1024, server.URL)
	_, err := p.Generate(context.Background(), "system", "user")
	if err == nil {
		t.Error("expected error for malformed JSON response")
	}
}

func TestOpenAIProvider_Generate_Non200Status(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"message":"bad request","type":"invalid_request_error"}}`))
	}))
	defer server.Close()

	p := NewOpenAIProvider("test-key", "gpt-4o", 1024, server.URL)
	_, err := p.Generate(context.Background(), "system", "user")
	if err == nil {
		t.Error("expected error for 400 status")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("expected error to contain status code 400, got: %v", err)
	}
}
