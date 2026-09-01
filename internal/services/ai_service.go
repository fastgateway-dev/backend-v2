package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/fastgateway-dev/backend-v2/internal/ai"
	"github.com/fastgateway-dev/backend-v2/internal/config"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
)

// aiRuntimeConfig is the resolved AI provider configuration used for a single request.
type aiRuntimeConfig struct {
	Provider  string
	APIKey    string
	Model     string
	MaxTokens int
	RateLimit int
	BaseURL   string
}

type AIService struct {
	config       *config.Config
	mu           sync.RWMutex
	provider     ai.Provider
	cachedConfig string
	rateMu       sync.Mutex
	rateLimiter  map[string][]time.Time
	lastCleanup  time.Time
}

func NewAIService(cfg *config.Config) *AIService {
	return &AIService{
		config:      cfg,
		rateLimiter: make(map[string][]time.Time),
		lastCleanup: time.Now(),
	}
}

// IsEnabled reports whether an AI provider has been configured via environment variables.
func (s *AIService) IsEnabled() bool {
	if s.config == nil || s.config.AIProvider == "" {
		return false
	}
	// openai_compatible providers may point at an endpoint that requires no API key.
	if s.config.AIAPIKey == "" && s.config.AIProvider != "openai_compatible" {
		return false
	}
	return true
}

func (s *AIService) GetStatus() ai.AIStatus {
	if !s.IsEnabled() {
		return ai.AIStatus{Enabled: false}
	}
	return ai.AIStatus{
		Enabled:  true,
		Provider: s.config.AIProvider,
	}
}

// currentConfig resolves the active AI configuration, applying defaults, or nil if AI is not configured.
func (s *AIService) currentConfig() *aiRuntimeConfig {
	if !s.IsEnabled() {
		return nil
	}

	model := s.config.AIModel
	if model == "" {
		switch s.config.AIProvider {
		case "anthropic":
			model = "claude-sonnet-4-20250514"
		case "openai":
			model = "gpt-4o"
		case "gemini":
			model = "gemini-2.0-flash"
		case "deepseek":
			model = "deepseek-chat"
		}
	}

	maxTokens := s.config.AIMaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	rateLimit := s.config.AIRateLimit
	if rateLimit <= 0 {
		rateLimit = 20
	}

	return &aiRuntimeConfig{
		Provider:  s.config.AIProvider,
		APIKey:    s.config.AIAPIKey,
		Model:     model,
		MaxTokens: maxTokens,
		RateLimit: rateLimit,
		BaseURL:   s.config.AIBaseURL,
	}
}

func hashAPIKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:8])
}

func (s *AIService) getOrCreateProvider(cfg *aiRuntimeConfig) (ai.Provider, error) {
	configKey := cfg.Provider + ":" + cfg.Model + ":" + hashAPIKey(cfg.APIKey) + ":" + cfg.BaseURL
	s.mu.RLock()
	// s.provider is a lazily-built cache of the last resolved provider, not
	// an injected dependency -- nothing wires it, this method builds it. The
	// nil check is a cache-miss test. Kept by Phase 2E Task 9.
	if s.provider != nil && s.cachedConfig == configKey {
		p := s.provider
		s.mu.RUnlock()
		return p, nil
	}
	s.mu.RUnlock()
	provider, err := ai.NewProvider(cfg.Provider, cfg.APIKey, cfg.Model, cfg.MaxTokens, cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.provider = provider
	s.cachedConfig = configKey
	s.mu.Unlock()
	return provider, nil
}

func (s *AIService) checkRateLimit(userID uuid.UUID, rateLimit int) bool {
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	key := userID.String()
	now := time.Now()
	hourAgo := now.Add(-time.Hour)

	// Periodic cleanup of stale entries from inactive users
	if now.Sub(s.lastCleanup) > 10*time.Minute {
		for k, times := range s.rateLimiter {
			var recent []time.Time
			for _, t := range times {
				if t.After(hourAgo) {
					recent = append(recent, t)
				}
			}
			if len(recent) == 0 {
				delete(s.rateLimiter, k)
			} else {
				s.rateLimiter[k] = recent
			}
		}
		s.lastCleanup = now
	}

	var recent []time.Time
	for _, t := range s.rateLimiter[key] {
		if t.After(hourAgo) {
			recent = append(recent, t)
		}
	}
	s.rateLimiter[key] = recent
	return len(recent) >= rateLimit
}

func (s *AIService) recordRequest(userID uuid.UUID) {
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	key := userID.String()
	s.rateLimiter[key] = append(s.rateLimiter[key], time.Now())
}

func (s *AIService) Generate(ctx context.Context, userID uuid.UUID, req ai.GenerateRequest) (<-chan ai.StreamChunk, error) {
	cfg := s.currentConfig()
	if cfg == nil {
		return nil, errors.New("AI is not configured")
	}
	if s.checkRateLimit(userID, cfg.RateLimit) {
		return nil, errors.New("rate limit exceeded, please try again later")
	}
	provider, err := s.getOrCreateProvider(cfg)
	if err != nil {
		return nil, err
	}
	s.recordRequest(userID)
	chunks := make(chan ai.StreamChunk, 100)
	go func() {
		if err := provider.StreamGenerate(ctx, req, chunks); err != nil {
			log.Printf("AI stream generation error: %v", err)
		}
	}()
	return chunks, nil
}

func (s *AIService) Review(ctx context.Context, userID uuid.UUID, req ai.ReviewRequest) (*ai.ReviewResult, error) {
	cfg := s.currentConfig()
	if cfg == nil {
		return nil, errors.New("AI is not configured")
	}
	if s.checkRateLimit(userID, cfg.RateLimit) {
		return nil, errors.New("rate limit exceeded, please try again later")
	}
	provider, err := s.getOrCreateProvider(cfg)
	if err != nil {
		return nil, err
	}
	s.recordRequest(userID)

	userMessage := ai.BuildReviewMessage(req)
	rawResponse, err := provider.Generate(ctx, ai.ReviewSystemPrompt, userMessage)
	if err != nil {
		return nil, fmt.Errorf("AI review failed: %w", err)
	}

	// Parse the JSON response
	var result ai.ReviewResult
	// Try to extract JSON from the response (AI may wrap in markdown code blocks)
	jsonStr := rawResponse
	if idx := strings.Index(jsonStr, "{"); idx >= 0 {
		if endIdx := strings.LastIndex(jsonStr, "}"); endIdx >= 0 {
			jsonStr = jsonStr[idx : endIdx+1]
		}
	}
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		// If parsing fails, return the raw text as the summary
		return &ai.ReviewResult{
			Summary: rawResponse,
		}, nil
	}

	return &result, nil
}

func (s *AIService) ReviewApproval(ctx context.Context, userID uuid.UUID, approval *models.Approval, diff *ApprovalDiffResult) (*ai.ReviewResult, error) {
	if approval.AIReview != nil && len(approval.AIReview) > 0 {
		return nil, errors.New("AI review already exists for this approval")
	}

	reviewReq := ai.ReviewRequest{
		Action:      diff.Action,
		Description: diff.ChangeDescription,
	}

	if diff.ProposedYAML != "" || diff.ProposedSecurityPolicyYAML != "" || diff.ProposedBackendTrafficPolicyYAML != "" || diff.ProposedEnvoyExtensionPolicyYAML != "" || diff.ProposedBackendYAML != "" {
		reviewReq.ProposedYaml = &ai.YamlSet{
			HttpRoute:            diff.ProposedYAML,
			SecurityPolicy:       diff.ProposedSecurityPolicyYAML,
			BackendTrafficPolicy: diff.ProposedBackendTrafficPolicyYAML,
			EnvoyExtensionPolicy: diff.ProposedEnvoyExtensionPolicyYAML,
			Backend:              diff.ProposedBackendYAML,
		}
	}

	if diff.CurrentYAML != "" || diff.CurrentSecurityPolicyYAML != "" || diff.CurrentBackendTrafficPolicyYAML != "" || diff.CurrentEnvoyExtensionPolicyYAML != "" || diff.CurrentBackendYAML != "" {
		reviewReq.CurrentYaml = &ai.YamlSet{
			HttpRoute:            diff.CurrentYAML,
			SecurityPolicy:       diff.CurrentSecurityPolicyYAML,
			BackendTrafficPolicy: diff.CurrentBackendTrafficPolicyYAML,
			EnvoyExtensionPolicy: diff.CurrentEnvoyExtensionPolicyYAML,
			Backend:              diff.CurrentBackendYAML,
		}
	}

	result, err := s.Review(ctx, userID, reviewReq)
	if err != nil {
		return nil, err
	}

	reviewJSON, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal AI review: %w", err)
	}
	approval.AIReview = reviewJSON

	return result, nil
}

// Chat handles a streaming chat conversation
func (s *AIService) Chat(ctx context.Context, userID uuid.UUID, req ai.ChatRequest) (<-chan ai.StreamChunk, error) {
	cfg := s.currentConfig()
	if cfg == nil {
		return nil, errors.New("AI is not configured")
	}
	if s.checkRateLimit(userID, cfg.RateLimit) {
		return nil, errors.New("rate limit exceeded, please try again later")
	}
	provider, err := s.getOrCreateProvider(cfg)
	if err != nil {
		return nil, err
	}
	s.recordRequest(userID)

	// Build system prompt with context
	systemPrompt := ai.BuildChatSystemPrompt(req.Context)

	// Build messages array (history + new message)
	messages := ai.BuildChatMessages(req)

	chunks := make(chan ai.StreamChunk, 100)

	go func() {
		if err := provider.StreamChat(ctx, systemPrompt, messages, chunks); err != nil {
			log.Printf("AI stream chat error: %v", err)
		}
	}()

	return chunks, nil
}

// TestAIConfig creates a temporary provider and sends a test request
func (s *AIService) TestAIConfig(ctx context.Context, provider, apiKey, model string, maxTokens int, baseURL string) error {
	p, err := ai.NewProvider(provider, apiKey, model, maxTokens, baseURL)
	if err != nil {
		return fmt.Errorf("failed to create provider: %w", err)
	}
	_, err = p.Generate(ctx, "You are a test assistant.", "Reply with exactly: ok")
	if err != nil {
		return fmt.Errorf("connection test failed: %w", err)
	}
	return nil
}
