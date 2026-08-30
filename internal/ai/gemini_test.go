package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGeminiProvider_Name(t *testing.T) {
	p := NewGeminiProvider("test-key", "gemini-2.0-flash", 1024)
	if p.Name() != "gemini" {
		t.Errorf("expected 'gemini', got %q", p.Name())
	}
}

func TestGeminiProvider_DefaultModel(t *testing.T) {
	p := NewGeminiProvider("test-key", "", 1024)
	if p.model != "gemini-2.0-flash" {
		t.Errorf("expected default model 'gemini-2.0-flash', got %q", p.model)
	}
}

func TestGeminiProvider_Generate_MockServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify it hits the right endpoint pattern
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if !strings.Contains(r.URL.Path, "generateContent") {
			t.Errorf("expected generateContent in path, got %s", r.URL.Path)
		}
		// Verify API key in query
		if r.URL.Query().Get("key") != "test-api-key" {
			t.Errorf("expected key=test-api-key, got %q", r.URL.Query().Get("key"))
		}

		// Decode and verify request body
		var reqBody geminiRequest
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Errorf("failed to decode request body: %v", err)
		}
		if reqBody.SystemInstruction == nil {
			t.Error("expected system instruction")
		}
		if len(reqBody.Contents) != 1 {
			t.Errorf("expected 1 content, got %d", len(reqBody.Contents))
		}

		// Return mock Gemini response
		resp := geminiResponse{}
		resp.Candidates = []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		}{
			{
				Content: struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
				}{
					Parts: []struct {
						Text string `json:"text"`
					}{
						{Text: "Hello from mock Gemini!"},
					},
				},
				FinishReason: "STOP",
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewGeminiProvider("test-api-key", "gemini-2.0-flash", 1024)
	p.baseURL = server.URL // override baseURL to point to mock server

	result, err := p.Generate(context.Background(), "You are helpful.", "Say hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Hello from mock Gemini!" {
		t.Errorf("expected 'Hello from mock Gemini!', got %q", result)
	}
}

func TestGeminiProvider_Generate_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": {"message": "internal error", "code": 500}}`))
	}))
	defer server.Close()

	p := NewGeminiProvider("test-key", "gemini-2.0-flash", 1024)
	p.baseURL = server.URL

	_, err := p.Generate(context.Background(), "system", "user")
	if err == nil {
		t.Error("expected error on server 500, got nil")
	}
}

func TestGeminiProvider_Generate_EmptyCandidates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"candidates": []interface{}{},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewGeminiProvider("test-key", "gemini-2.0-flash", 1024)
	p.baseURL = server.URL

	_, err := p.Generate(context.Background(), "system", "user")
	if err == nil {
		t.Error("expected error for empty candidates, got nil")
	}
}

func TestGeminiProvider_Generate_CancelledContext(t *testing.T) {
	p := NewGeminiProvider("test-key", "gemini-2.0-flash", 1024)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := p.Generate(ctx, "system", "user")
	if err == nil {
		t.Error("expected error with cancelled context, got nil")
	}
}

func TestGeminiProvider_StreamGenerate_CancelledContext(t *testing.T) {
	p := NewGeminiProvider("test-key", "gemini-2.0-flash", 1024)

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

func TestGeminiProvider_StreamChat_CancelledContext(t *testing.T) {
	p := NewGeminiProvider("test-key", "gemini-2.0-flash", 1024)

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

func TestGeminiProvider_Generate_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"error": map[string]interface{}{
				"message": "quota exceeded",
				"code":    429,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		// Return 200 but with error in body (Gemini sometimes does this)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewGeminiProvider("test-key", "gemini-2.0-flash", 1024)
	p.baseURL = server.URL

	_, err := p.Generate(context.Background(), "system", "user")
	if err == nil {
		t.Error("expected error for API error in response body, got nil")
	}
}

func TestGeminiProvider_StreamGenerate_MockSSE(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "streamGenerateContent") {
			t.Errorf("expected streamGenerateContent in path, got %s", r.URL.Path)
		}
		if r.URL.Query().Get("alt") != "sse" {
			t.Errorf("expected alt=sse query param, got %q", r.URL.Query().Get("alt"))
		}

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		// Gemini streaming uses SSE format with data: prefix
		events := []string{
			`data: {"candidates":[{"content":{"parts":[{"text":"hello"}]},"finishReason":""}]}`,
			`data: {"candidates":[{"content":{"parts":[{"text":" world"}]},"finishReason":""}]}`,
			`data: {"candidates":[{"content":{"parts":[{"text":""}]},"finishReason":"STOP"}]}`,
		}
		for _, event := range events {
			w.Write([]byte(event + "\n"))
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	defer server.Close()

	p := NewGeminiProvider("test-api-key", "gemini-2.0-flash", 1024)
	p.baseURL = server.URL

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

func TestGeminiProvider_StreamGenerate_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":{"message":"forbidden","code":403}}`))
	}))
	defer server.Close()

	p := NewGeminiProvider("test-key", "gemini-2.0-flash", 1024)
	p.baseURL = server.URL

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

func TestGeminiProvider_StreamChat_MockSSE(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request body has correct role mapping
		var reqBody geminiRequest
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Errorf("failed to decode request body: %v", err)
		}
		// Check that "assistant" role is mapped to "model"
		for _, c := range reqBody.Contents {
			if c.Role == "assistant" {
				t.Error("expected 'assistant' role to be mapped to 'model'")
			}
		}

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		events := []string{
			`data: {"candidates":[{"content":{"parts":[{"text":"I can help"}]},"finishReason":""}]}`,
			`data: {"candidates":[{"content":{"parts":[{"text":" with that!"}]},"finishReason":"STOP"}]}`,
		}
		for _, event := range events {
			w.Write([]byte(event + "\n"))
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	defer server.Close()

	p := NewGeminiProvider("test-key", "gemini-2.0-flash", 1024)
	p.baseURL = server.URL

	chunks := make(chan StreamChunk, 20)

	go func() {
		err := p.StreamChat(context.Background(), "You are helpful", []ChatMessage{
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

func TestGeminiProvider_StreamChat_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":{"message":"internal error","code":500}}`))
	}))
	defer server.Close()

	p := NewGeminiProvider("test-key", "gemini-2.0-flash", 1024)
	p.baseURL = server.URL

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

func TestGeminiProvider_Generate_MalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{this is not json`))
	}))
	defer server.Close()

	p := NewGeminiProvider("test-key", "gemini-2.0-flash", 1024)
	p.baseURL = server.URL

	_, err := p.Generate(context.Background(), "system", "user")
	if err == nil {
		t.Error("expected error for malformed JSON response")
	}
}
