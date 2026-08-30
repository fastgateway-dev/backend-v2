package services

import (
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/fastgateway-dev/backend-v2/internal/config"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
)

// EffectiveSettings holds the resolved settings (DB value or env var fallback)
type EffectiveSettings struct {
	BaseURL            string `json:"baseUrl"`
	JWTExpiry          string `json:"jwtExpiry"`
	RefreshTokenExpiry string `json:"refreshTokenExpiry"`
	LogLevel           string `json:"logLevel"`
}

// SystemSettingsResponse is the API response with both DB and effective values
type SystemSettingsResponse struct {
	BaseURL            string            `json:"baseUrl"`
	JWTExpiry          string            `json:"jwtExpiry"`
	RefreshTokenExpiry string            `json:"refreshTokenExpiry"`
	LogLevel           string            `json:"logLevel"`
	Effective          EffectiveSettings `json:"effective"`
}

// SystemSettingsInput is the API input for updating settings
type SystemSettingsInput struct {
	BaseURL            string `json:"baseUrl"`
	JWTExpiry          string `json:"jwtExpiry"`
	RefreshTokenExpiry string `json:"refreshTokenExpiry"`
	LogLevel           string `json:"logLevel"`
}

// SystemSettingsService manages system settings with in-memory cache
type SystemSettingsService struct {
	repo   repository.SystemSettingsRepositoryInterface
	config *config.Config
	mu     sync.RWMutex
	cached *models.SystemSettings
}

// NewSystemSettingsService creates a new system settings service
func NewSystemSettingsService(repo repository.SystemSettingsRepositoryInterface, cfg *config.Config) *SystemSettingsService {
	return &SystemSettingsService{
		repo:   repo,
		config: cfg,
	}
}

// Get returns the raw DB settings
func (s *SystemSettingsService) Get() (*models.SystemSettings, error) {
	s.mu.RLock()
	if s.cached != nil {
		defer s.mu.RUnlock()
		return s.cached, nil
	}
	s.mu.RUnlock()

	return s.loadAndCache()
}

// GetResponse returns the full response with effective values
func (s *SystemSettingsService) GetResponse() (*SystemSettingsResponse, error) {
	settings, err := s.Get()
	if err != nil {
		return nil, err
	}

	return &SystemSettingsResponse{
		BaseURL:            settings.BaseURL,
		JWTExpiry:          settings.JWTExpiry,
		RefreshTokenExpiry: settings.RefreshTokenExpiry,
		LogLevel:           settings.LogLevel,
		Effective:          s.getEffective(settings),
	}, nil
}

// Update updates the system settings and invalidates cache
func (s *SystemSettingsService) Update(input SystemSettingsInput) (*SystemSettingsResponse, error) {
	// Validate JWT expiry if provided
	if input.JWTExpiry != "" {
		if _, err := time.ParseDuration(input.JWTExpiry); err != nil {
			return nil, errors.New("invalid JWT expiry duration format (e.g., 24h, 1h30m)")
		}
	}

	// Validate refresh token expiry if provided
	if input.RefreshTokenExpiry != "" {
		if _, err := time.ParseDuration(input.RefreshTokenExpiry); err != nil {
			return nil, errors.New("invalid refresh token expiry duration format (e.g., 168h, 720h)")
		}
	}

	// Validate log level if provided
	if input.LogLevel != "" {
		validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
		if !validLevels[strings.ToLower(input.LogLevel)] {
			return nil, errors.New("invalid log level (must be debug, info, warn, or error)")
		}
		input.LogLevel = strings.ToLower(input.LogLevel)
	}

	// Validate base URL if provided (strip trailing slash)
	if input.BaseURL != "" {
		input.BaseURL = strings.TrimRight(input.BaseURL, "/")
	}

	settings, err := s.repo.Get()
	if err != nil {
		return nil, err
	}

	settings.BaseURL = input.BaseURL
	settings.JWTExpiry = input.JWTExpiry
	settings.RefreshTokenExpiry = input.RefreshTokenExpiry
	settings.LogLevel = input.LogLevel

	if err := s.repo.Update(settings); err != nil {
		return nil, err
	}

	// Update cache
	s.mu.Lock()
	s.cached = settings
	s.mu.Unlock()

	return &SystemSettingsResponse{
		BaseURL:            settings.BaseURL,
		JWTExpiry:          settings.JWTExpiry,
		RefreshTokenExpiry: settings.RefreshTokenExpiry,
		LogLevel:           settings.LogLevel,
		Effective:          s.getEffective(settings),
	}, nil
}

// GetJWTExpiry returns the effective JWT expiry duration
func (s *SystemSettingsService) GetJWTExpiry() time.Duration {
	settings, err := s.Get()
	if err != nil || settings.JWTExpiry == "" {
		return s.config.JWTExpiry
	}
	d, err := time.ParseDuration(settings.JWTExpiry)
	if err != nil {
		return s.config.JWTExpiry
	}
	return d
}

// GetRefreshTokenExpiry returns the effective refresh token expiry duration
func (s *SystemSettingsService) GetRefreshTokenExpiry() time.Duration {
	settings, err := s.Get()
	if err != nil || settings.RefreshTokenExpiry == "" {
		return s.config.RefreshTokenExpiry
	}
	d, err := time.ParseDuration(settings.RefreshTokenExpiry)
	if err != nil {
		return s.config.RefreshTokenExpiry
	}
	return d
}

// GetBaseURL returns the effective base URL
func (s *SystemSettingsService) GetBaseURL() string {
	settings, err := s.Get()
	if err != nil || settings.BaseURL == "" {
		// Fallback to first CORS origin for backward compatibility
		if len(s.config.CORSAllowedOrigins) > 0 && s.config.CORSAllowedOrigins[0] != "" {
			return s.config.CORSAllowedOrigins[0]
		}
		return ""
	}
	return settings.BaseURL
}

// GetLogLevel returns the effective log level
func (s *SystemSettingsService) GetLogLevel() string {
	settings, err := s.Get()
	if err != nil || settings.LogLevel == "" {
		return s.config.LogLevel
	}
	return settings.LogLevel
}

func (s *SystemSettingsService) loadAndCache() (*models.SystemSettings, error) {
	settings, err := s.repo.Get()
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.cached = settings
	s.mu.Unlock()

	return settings, nil
}

func (s *SystemSettingsService) getEffective(settings *models.SystemSettings) EffectiveSettings {
	effective := EffectiveSettings{}

	if settings.BaseURL != "" {
		effective.BaseURL = settings.BaseURL
	} else if len(s.config.CORSAllowedOrigins) > 0 && s.config.CORSAllowedOrigins[0] != "" {
		effective.BaseURL = s.config.CORSAllowedOrigins[0]
	}

	if settings.JWTExpiry != "" {
		effective.JWTExpiry = settings.JWTExpiry
	} else {
		effective.JWTExpiry = s.config.JWTExpiry.String()
	}

	if settings.RefreshTokenExpiry != "" {
		effective.RefreshTokenExpiry = settings.RefreshTokenExpiry
	} else {
		effective.RefreshTokenExpiry = s.config.RefreshTokenExpiry.String()
	}

	if settings.LogLevel != "" {
		effective.LogLevel = settings.LogLevel
	} else {
		effective.LogLevel = s.config.LogLevel
	}

	return effective
}
