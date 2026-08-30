package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	oidc "github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"golang.org/x/oauth2"

	"github.com/fastgateway-dev/backend-v2/internal/config"
	"github.com/fastgateway-dev/backend-v2/internal/crypto"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
)

// SSOPublicConfig is the SSO config returned to the login page (no secrets)
type SSOPublicConfig struct {
	Enabled        bool     `json:"enabled"`
	ProviderName   string   `json:"providerName"`
	ForceSSO       bool     `json:"forceSSO"`
	AllowedDomains []string `json:"allowedDomains"`
}

// SSOConfigInput is the input for updating SSO config
type SSOConfigInput struct {
	ProviderName   string   `json:"providerName" binding:"required"`
	IssuerURL      string   `json:"issuerUrl" binding:"required"`
	ClientID       string   `json:"clientId" binding:"required"`
	ClientSecret   string   `json:"clientSecret" binding:"required"`
	Scopes         []string `json:"scopes"`
	AllowedDomains []string `json:"allowedDomains"`
	AllowedEmails  []string `json:"allowedEmails"`
	AutoRegister   bool     `json:"autoRegister"`
	ForceSSO       bool     `json:"forceSSO"`
}

// SSOCallbackResult is returned after a successful SSO callback
type SSOCallbackResult struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	IsNewUser    bool   `json:"isNewUser"`
}

// ssoState represents an in-memory OAuth2 state entry with expiry
type ssoState struct {
	createdAt time.Time
}

// SSOService handles SSO/OIDC operations
type SSOService struct {
	ssoConfigRepo         repository.SSOConfigRepositoryInterface
	userRepo              repository.UserRepositoryInterface
	teamRepo              repository.TeamRepositoryInterface
	emailInviteRepo       repository.TeamEmailInviteRepositoryInterface
	config                *config.Config
	systemSettingsService *SystemSettingsService

	// Token generation function (set via setter to avoid circular dependency)
	generateTokens func(user *models.User) (accessToken string, refreshToken string, err error)

	// In-memory state management for OAuth2 CSRF protection
	states   map[string]*ssoState
	statesMu sync.Mutex
}

// NewSSOService creates a new SSO service
func NewSSOService(
	ssoConfigRepo repository.SSOConfigRepositoryInterface,
	userRepo repository.UserRepositoryInterface,
	teamRepo repository.TeamRepositoryInterface,
	emailInviteRepo repository.TeamEmailInviteRepositoryInterface,
	cfg *config.Config,
) *SSOService {
	s := &SSOService{
		ssoConfigRepo:   ssoConfigRepo,
		userRepo:        userRepo,
		teamRepo:        teamRepo,
		emailInviteRepo: emailInviteRepo,
		config:          cfg,
		states:          make(map[string]*ssoState),
	}

	// Start state cleanup goroutine
	go s.cleanupStates()

	return s
}

// SetTokenGenerator sets the function used to generate JWT tokens for a user.
// This is called during wiring to break the circular dependency with AuthService.
func (s *SSOService) SetTokenGenerator(fn func(*models.User) (string, string, error)) {
	s.generateTokens = fn
}

// SetSystemSettingsService injects the system settings service
func (s *SSOService) SetSystemSettingsService(svc *SystemSettingsService) {
	s.systemSettingsService = svc
}

// GetPublicConfig returns the SSO configuration for the login page (no secrets)
func (s *SSOService) GetPublicConfig() (*SSOPublicConfig, error) {
	cfg, err := s.ssoConfigRepo.Get()
	if err != nil {
		// No config row yet - return disabled
		return &SSOPublicConfig{Enabled: false}, nil
	}

	return &SSOPublicConfig{
		Enabled:        cfg.Enabled,
		ProviderName:   cfg.ProviderName,
		ForceSSO:       cfg.ForceSSO,
		AllowedDomains: cfg.AllowedDomains,
	}, nil
}

// GetConfig returns the full SSO configuration for admin display
func (s *SSOService) GetConfig() (*models.SSOConfig, error) {
	cfg, err := s.ssoConfigRepo.Get()
	if err != nil {
		return nil, fmt.Errorf("SSO configuration not found")
	}

	return cfg, nil
}

// UpdateConfig updates the SSO configuration (encrypts client secret)
func (s *SSOService) UpdateConfig(input SSOConfigInput) (*models.SSOConfig, error) {
	// Validate base URL is set and uses HTTPS (required for SSO callback)
	if s.systemSettingsService == nil {
		return nil, fmt.Errorf("SSO requires system settings service to be configured")
	}
	baseURL := s.systemSettingsService.GetBaseURL()
	if baseURL == "" {
		return nil, fmt.Errorf("SSO requires a base URL to be configured in System Settings")
	}
	if !strings.HasPrefix(baseURL, "https://") {
		return nil, fmt.Errorf("SSO requires an HTTPS base URL (current: %s). Update in System Settings", baseURL)
	}

	// Encrypt the client secret
	encryptedSecret, err := crypto.Encrypt(input.ClientSecret, s.config.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt client secret: %w", err)
	}

	cfg, err := s.ssoConfigRepo.Get()
	if err != nil {
		return nil, fmt.Errorf("SSO configuration not found")
	}

	cfg.ProviderName = input.ProviderName
	cfg.IssuerURL = input.IssuerURL
	cfg.ClientID = input.ClientID
	cfg.ClientSecretEncrypted = encryptedSecret
	cfg.AutoRegister = input.AutoRegister
	cfg.ForceSSO = input.ForceSSO
	cfg.Enabled = true

	if len(input.Scopes) > 0 {
		cfg.Scopes = input.Scopes
	} else {
		cfg.Scopes = []string{"openid", "email", "profile"}
	}

	if input.AllowedDomains != nil {
		cfg.AllowedDomains = input.AllowedDomains
	}

	if input.AllowedEmails != nil {
		cfg.AllowedEmails = input.AllowedEmails
	}

	if err := s.ssoConfigRepo.Update(cfg); err != nil {
		return nil, fmt.Errorf("failed to save SSO configuration: %w", err)
	}

	return cfg, nil
}

// DisableSSO sets the SSO configuration to disabled
func (s *SSOService) DisableSSO() error {
	cfg, err := s.ssoConfigRepo.Get()
	if err != nil {
		return fmt.Errorf("SSO configuration not found")
	}

	cfg.Enabled = false
	return s.ssoConfigRepo.Update(cfg)
}

// GetAuthorizeURL builds the OIDC authorization URL for redirecting the user
func (s *SSOService) GetAuthorizeURL(callbackURL string) (string, error) {
	cfg, err := s.ssoConfigRepo.Get()
	if err != nil || !cfg.Enabled {
		return "", errors.New("SSO is not enabled")
	}

	// Decrypt client secret
	clientSecret, err := crypto.Decrypt(cfg.ClientSecretEncrypted, s.config.EncryptionKey)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt client secret: %w", err)
	}

	// Discover OIDC provider
	ctx := context.Background()
	provider, err := oidc.NewProvider(ctx, cfg.IssuerURL)
	if err != nil {
		return "", fmt.Errorf("failed to discover OIDC provider: %w", err)
	}

	// Build OAuth2 config
	oauth2Config := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: clientSecret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  callbackURL,
		Scopes:       cfg.Scopes,
	}

	// Generate state
	state, err := s.generateState()
	if err != nil {
		return "", fmt.Errorf("failed to generate state: %w", err)
	}

	return oauth2Config.AuthCodeURL(state), nil
}

// HandleCallback handles the OIDC callback after the user authenticates
func (s *SSOService) HandleCallback(ctx context.Context, code, state, callbackURL string) (*SSOCallbackResult, error) {
	// Validate state
	if !s.validateState(state) {
		return nil, errors.New("invalid or expired state parameter")
	}

	cfg, err := s.ssoConfigRepo.Get()
	if err != nil || !cfg.Enabled {
		return nil, errors.New("SSO is not enabled")
	}

	// Decrypt client secret
	clientSecret, err := crypto.Decrypt(cfg.ClientSecretEncrypted, s.config.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt client secret: %w", err)
	}

	// Discover OIDC provider
	provider, err := oidc.NewProvider(ctx, cfg.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("failed to discover OIDC provider: %w", err)
	}

	// Build OAuth2 config
	oauth2Config := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: clientSecret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  callbackURL,
		Scopes:       cfg.Scopes,
	}

	// Exchange code for token
	token, err := oauth2Config.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("failed to exchange authorization code: %w", err)
	}

	// Extract and verify ID token
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return nil, errors.New("no id_token in token response")
	}

	verifier := provider.Verifier(&oidc.Config{ClientID: cfg.ClientID})
	idToken, err := verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("failed to verify ID token: %w", err)
	}

	// Extract claims
	var claims struct {
		Sub   string `json:"sub"`
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("failed to parse ID token claims: %w", err)
	}

	if claims.Email == "" {
		return nil, errors.New("email claim is required but not present in ID token")
	}

	// Check allowed domains
	if len(cfg.AllowedDomains) > 0 {
		emailDomain := emailDomain(claims.Email)
		allowed := false
		for _, domain := range cfg.AllowedDomains {
			if strings.EqualFold(emailDomain, domain) {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, fmt.Errorf("email domain %q is not allowed", emailDomain)
		}
	}

	// Check allowed emails (AND logic with domain check)
	if len(cfg.AllowedEmails) > 0 {
		allowed := false
		for _, email := range cfg.AllowedEmails {
			if strings.EqualFold(claims.Email, email) {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, fmt.Errorf("email %q is not in the allowed list", claims.Email)
		}
	}

	// Find or create user
	user, isNew, err := s.findOrCreateUser(cfg, claims.Sub, claims.Email, claims.Name)
	if err != nil {
		return nil, err
	}

	if !user.IsActive {
		return nil, errors.New("user account is disabled")
	}

	// Generate tokens
	if s.generateTokens == nil {
		return nil, errors.New("token generator not configured")
	}

	accessToken, refreshToken, err := s.generateTokens(user)
	if err != nil {
		return nil, fmt.Errorf("failed to generate tokens: %w", err)
	}

	return &SSOCallbackResult{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		IsNewUser:    isNew,
	}, nil
}

// findOrCreateUser finds an existing user or creates a new one based on OIDC claims.
// Priority: 1) by provider_subject, 2) by email (link accounts), 3) create new
func (s *SSOService) findOrCreateUser(cfg *models.SSOConfig, sub, email, name string) (*models.User, bool, error) {
	// 1. Try to find by provider subject
	user, err := s.userRepo.GetByProviderSubject(sub)
	if err == nil {
		// Found by provider subject - update email/name if changed
		return user, false, nil
	}

	// 2. Try to find by email (link existing local account to SSO)
	user, err = s.userRepo.GetByEmail(email)
	if err == nil {
		// Link existing account to SSO provider
		user.AuthProvider = "oidc"
		user.ProviderSubject = &sub
		if err := s.userRepo.Update(user); err != nil {
			return nil, false, fmt.Errorf("failed to link SSO to existing account: %w", err)
		}
		return user, false, nil
	}

	// 3. Create new user if auto-register is enabled
	if !cfg.AutoRegister {
		return nil, false, errors.New("account not found and auto-registration is disabled")
	}

	// Generate a unique username from email prefix
	username := generateUsernameFromEmail(email)

	newUser := &models.User{
		ID:              uuid.New(),
		Username:        username,
		Email:           email,
		Role:            models.UserRoleUser,
		IsActive:        true,
		AuthProvider:    "oidc",
		ProviderSubject: &sub,
	}

	if err := s.userRepo.Create(newUser); err != nil {
		return nil, false, fmt.Errorf("failed to create SSO user: %w", err)
	}

	// Process pending email invites for the new user
	s.processEmailInvites(newUser)

	return newUser, true, nil
}

// processEmailInvites checks for pending team email invites and adds the user to those teams
func (s *SSOService) processEmailInvites(user *models.User) {
	invites, err := s.emailInviteRepo.GetByEmail(user.Email)
	if err != nil || len(invites) == 0 {
		return
	}

	for _, invite := range invites {
		// Add user to the team
		_ = s.teamRepo.AddMember(invite.TeamID, user.ID)
	}

	// Delete all processed invites for this email
	_ = s.emailInviteRepo.DeleteByEmail(user.Email)
}

// ShouldForceSSO checks if a user should be forced to use SSO login.
// Owners are always exempt from force SSO.
func (s *SSOService) ShouldForceSSO(email string, role models.UserRole) bool {
	// Owners are always exempt
	if role == models.UserRoleOwner {
		return false
	}

	cfg, err := s.ssoConfigRepo.Get()
	if err != nil || !cfg.Enabled || !cfg.ForceSSO {
		return false
	}

	// If allowed domains are configured, only force SSO for matching domains
	if len(cfg.AllowedDomains) > 0 {
		domain := emailDomain(email)
		for _, allowed := range cfg.AllowedDomains {
			if strings.EqualFold(domain, allowed) {
				return true
			}
		}
		return false
	}

	// No domain restrictions - force SSO for all non-owner users
	return true
}

// generateState creates a cryptographically random state parameter and stores it
func (s *SSOService) generateState() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}

	state := hex.EncodeToString(bytes)

	s.statesMu.Lock()
	s.states[state] = &ssoState{createdAt: time.Now()}
	s.statesMu.Unlock()

	return state, nil
}

// validateState checks if a state parameter is valid and removes it (one-time use)
func (s *SSOService) validateState(state string) bool {
	s.statesMu.Lock()
	defer s.statesMu.Unlock()

	entry, exists := s.states[state]
	if !exists {
		return false
	}

	// Remove state (one-time use)
	delete(s.states, state)

	// Check if expired (10 minutes)
	if time.Since(entry.createdAt) > 10*time.Minute {
		return false
	}

	return true
}

// cleanupStates periodically removes expired state entries
func (s *SSOService) cleanupStates() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		s.statesMu.Lock()
		for state, entry := range s.states {
			if time.Since(entry.createdAt) > 10*time.Minute {
				delete(s.states, state)
			}
		}
		s.statesMu.Unlock()
	}
}

// emailDomain extracts the domain part from an email address
func emailDomain(email string) string {
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 {
		return ""
	}
	return parts[1]
}

// generateUsernameFromEmail creates a username from an email address.
// Uses the local part and appends random chars to ensure uniqueness.
func generateUsernameFromEmail(email string) string {
	parts := strings.SplitN(email, "@", 2)
	base := parts[0]

	// Clean up the base - keep only alphanumeric, dots, hyphens, underscores
	var cleaned strings.Builder
	for _, c := range base {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '.' || c == '-' || c == '_' {
			cleaned.WriteRune(c)
		}
	}
	base = cleaned.String()

	if base == "" {
		base = "user"
	}

	// Append random suffix to ensure uniqueness
	randBytes := make([]byte, 4)
	_, _ = rand.Read(randBytes)
	suffix := hex.EncodeToString(randBytes)

	return base + "-" + suffix
}
