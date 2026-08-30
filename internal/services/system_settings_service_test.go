package services_test

import (
	"errors"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/internal/config"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newTestSystemSettingsService() (*services.SystemSettingsService, *mocks.MockSystemSettingsRepository) {
	mockRepo := new(mocks.MockSystemSettingsRepository)
	cfg := &config.Config{
		JWTExpiry:          time.Hour,
		RefreshTokenExpiry: 24 * time.Hour,
		LogLevel:           "info",
		CORSAllowedOrigins: []string{"http://localhost:3000"},
	}
	svc := services.NewSystemSettingsService(mockRepo, cfg)
	return svc, mockRepo
}

func TestSystemSettingsService_Get_Success(t *testing.T) {
	svc, mockRepo := newTestSystemSettingsService()

	expected := &models.SystemSettings{
		JWTExpiry:          "2h",
		RefreshTokenExpiry: "48h",
		LogLevel:           "debug",
	}
	mockRepo.On("Get").Return(expected, nil)

	result, err := svc.Get()

	require.NoError(t, err)
	assert.Equal(t, expected, result)
	mockRepo.AssertExpectations(t)
}

func TestSystemSettingsService_Get_UsesCache(t *testing.T) {
	svc, mockRepo := newTestSystemSettingsService()

	settings := &models.SystemSettings{JWTExpiry: "2h"}
	mockRepo.On("Get").Return(settings, nil).Once()

	// First call - should hit repo
	result1, err := svc.Get()
	require.NoError(t, err)
	assert.Equal(t, settings, result1)

	// Second call - should use cache
	result2, err := svc.Get()
	require.NoError(t, err)
	assert.Equal(t, settings, result2)

	// Repo should only be called once
	mockRepo.AssertNumberOfCalls(t, "Get", 1)
}

func TestSystemSettingsService_Update_Success(t *testing.T) {
	svc, mockRepo := newTestSystemSettingsService()

	existing := &models.SystemSettings{
		JWTExpiry:          "",
		RefreshTokenExpiry: "",
		LogLevel:           "",
	}
	mockRepo.On("Get").Return(existing, nil)
	mockRepo.On("Update", mock.AnythingOfType("*models.SystemSettings")).Return(nil)

	input := services.SystemSettingsInput{
		JWTExpiry:          "2h",
		RefreshTokenExpiry: "48h",
		LogLevel:           "debug",
	}

	result, err := svc.Update(input)

	require.NoError(t, err)
	assert.Equal(t, "2h", result.JWTExpiry)
	assert.Equal(t, "48h", result.RefreshTokenExpiry)
	assert.Equal(t, "debug", result.LogLevel)
	mockRepo.AssertExpectations(t)
}

func TestSystemSettingsService_Update_InvalidJWTExpiry(t *testing.T) {
	svc, _ := newTestSystemSettingsService()

	input := services.SystemSettingsInput{
		JWTExpiry: "not-a-duration",
	}

	result, err := svc.Update(input)

	assert.Nil(t, result)
	assert.EqualError(t, err, "invalid JWT expiry duration format (e.g., 24h, 1h30m)")
}

func TestSystemSettingsService_Update_InvalidLogLevel(t *testing.T) {
	svc, _ := newTestSystemSettingsService()

	input := services.SystemSettingsInput{
		LogLevel: "verbose",
	}

	result, err := svc.Update(input)

	assert.Nil(t, result)
	assert.EqualError(t, err, "invalid log level (must be debug, info, warn, or error)")
}

func TestSystemSettingsService_GetJWTExpiry_FromSettings(t *testing.T) {
	svc, mockRepo := newTestSystemSettingsService()

	settings := &models.SystemSettings{JWTExpiry: "2h"}
	mockRepo.On("Get").Return(settings, nil)

	result := svc.GetJWTExpiry()

	assert.Equal(t, 2*time.Hour, result)
	mockRepo.AssertExpectations(t)
}

func TestSystemSettingsService_GetJWTExpiry_FallbackToConfig(t *testing.T) {
	svc, mockRepo := newTestSystemSettingsService()

	settings := &models.SystemSettings{JWTExpiry: ""}
	mockRepo.On("Get").Return(settings, nil)

	result := svc.GetJWTExpiry()

	assert.Equal(t, time.Hour, result)
	mockRepo.AssertExpectations(t)
}

func TestSystemSettingsService_GetRefreshTokenExpiry_FromSettings(t *testing.T) {
	svc, mockRepo := newTestSystemSettingsService()

	settings := &models.SystemSettings{RefreshTokenExpiry: "168h"}
	mockRepo.On("Get").Return(settings, nil)

	result := svc.GetRefreshTokenExpiry()

	assert.Equal(t, 168*time.Hour, result)
	mockRepo.AssertExpectations(t)
}

func TestSystemSettingsService_GetRefreshTokenExpiry_FallbackToConfig(t *testing.T) {
	svc, mockRepo := newTestSystemSettingsService()

	settings := &models.SystemSettings{RefreshTokenExpiry: ""}
	mockRepo.On("Get").Return(settings, nil)

	result := svc.GetRefreshTokenExpiry()

	assert.Equal(t, 24*time.Hour, result)
	mockRepo.AssertExpectations(t)
}

func TestSystemSettingsService_Update_BaseURLStripsTrailingSlash(t *testing.T) {
	svc, mockRepo := newTestSystemSettingsService()

	existing := &models.SystemSettings{}
	mockRepo.On("Get").Return(existing, nil)
	mockRepo.On("Update", mock.AnythingOfType("*models.SystemSettings")).Return(nil)

	input := services.SystemSettingsInput{
		BaseURL: "https://example.com/",
	}

	result, err := svc.Update(input)

	require.NoError(t, err)
	assert.Equal(t, "https://example.com", result.BaseURL)
	mockRepo.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// GetResponse
// ---------------------------------------------------------------------------

func TestSystemSettingsService_GetResponse_Success(t *testing.T) {
	svc, mockRepo := newTestSystemSettingsService()

	settings := &models.SystemSettings{
		BaseURL:            "https://app.example.com",
		JWTExpiry:          "2h",
		RefreshTokenExpiry: "48h",
		LogLevel:           "debug",
	}
	mockRepo.On("Get").Return(settings, nil)

	result, err := svc.GetResponse()

	require.NoError(t, err)
	assert.Equal(t, "https://app.example.com", result.BaseURL)
	assert.Equal(t, "2h", result.JWTExpiry)
	assert.Equal(t, "48h", result.RefreshTokenExpiry)
	assert.Equal(t, "debug", result.LogLevel)
	// Effective values should use DB values
	assert.Equal(t, "https://app.example.com", result.Effective.BaseURL)
	assert.Equal(t, "2h", result.Effective.JWTExpiry)
	assert.Equal(t, "48h", result.Effective.RefreshTokenExpiry)
	assert.Equal(t, "debug", result.Effective.LogLevel)
	mockRepo.AssertExpectations(t)
}

func TestSystemSettingsService_GetResponse_FallbackToConfig(t *testing.T) {
	svc, mockRepo := newTestSystemSettingsService()

	settings := &models.SystemSettings{} // all empty
	mockRepo.On("Get").Return(settings, nil)

	result, err := svc.GetResponse()

	require.NoError(t, err)
	assert.Empty(t, result.BaseURL)
	assert.Empty(t, result.JWTExpiry)
	// Effective values should fall back to config
	assert.Equal(t, "http://localhost:3000", result.Effective.BaseURL) // from CORSAllowedOrigins
	assert.Equal(t, "1h0m0s", result.Effective.JWTExpiry)
	assert.Equal(t, "24h0m0s", result.Effective.RefreshTokenExpiry)
	assert.Equal(t, "info", result.Effective.LogLevel)
	mockRepo.AssertExpectations(t)
}

func TestSystemSettingsService_GetResponse_Error(t *testing.T) {
	svc, mockRepo := newTestSystemSettingsService()

	mockRepo.On("Get").Return(nil, errors.New("db error"))

	result, err := svc.GetResponse()

	assert.Nil(t, result)
	assert.EqualError(t, err, "db error")
	mockRepo.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// GetBaseURL
// ---------------------------------------------------------------------------

func TestSystemSettingsService_GetBaseURL_FromSettings(t *testing.T) {
	svc, mockRepo := newTestSystemSettingsService()

	settings := &models.SystemSettings{BaseURL: "https://myapp.example.com"}
	mockRepo.On("Get").Return(settings, nil)

	result := svc.GetBaseURL()

	assert.Equal(t, "https://myapp.example.com", result)
	mockRepo.AssertExpectations(t)
}

func TestSystemSettingsService_GetBaseURL_FallbackToCORS(t *testing.T) {
	svc, mockRepo := newTestSystemSettingsService()

	settings := &models.SystemSettings{BaseURL: ""}
	mockRepo.On("Get").Return(settings, nil)

	result := svc.GetBaseURL()

	assert.Equal(t, "http://localhost:3000", result) // from CORSAllowedOrigins
	mockRepo.AssertExpectations(t)
}

func TestSystemSettingsService_GetBaseURL_NoCORSOrigins(t *testing.T) {
	mockRepo := new(mocks.MockSystemSettingsRepository)
	cfg := &config.Config{
		JWTExpiry:          time.Hour,
		RefreshTokenExpiry: 24 * time.Hour,
		CORSAllowedOrigins: []string{},
	}
	svc := services.NewSystemSettingsService(mockRepo, cfg)

	settings := &models.SystemSettings{BaseURL: ""}
	mockRepo.On("Get").Return(settings, nil)

	result := svc.GetBaseURL()

	assert.Empty(t, result)
	mockRepo.AssertExpectations(t)
}

func TestSystemSettingsService_GetBaseURL_Error(t *testing.T) {
	svc, mockRepo := newTestSystemSettingsService()

	mockRepo.On("Get").Return(nil, errors.New("db error"))

	result := svc.GetBaseURL()

	assert.Equal(t, "http://localhost:3000", result) // falls back to CORS origin
	mockRepo.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// GetLogLevel
// ---------------------------------------------------------------------------

func TestSystemSettingsService_GetLogLevel_FromSettings(t *testing.T) {
	svc, mockRepo := newTestSystemSettingsService()

	settings := &models.SystemSettings{LogLevel: "debug"}
	mockRepo.On("Get").Return(settings, nil)

	result := svc.GetLogLevel()

	assert.Equal(t, "debug", result)
	mockRepo.AssertExpectations(t)
}

func TestSystemSettingsService_GetLogLevel_FallbackToConfig(t *testing.T) {
	svc, mockRepo := newTestSystemSettingsService()

	settings := &models.SystemSettings{LogLevel: ""}
	mockRepo.On("Get").Return(settings, nil)

	result := svc.GetLogLevel()

	assert.Equal(t, "info", result) // from config
	mockRepo.AssertExpectations(t)
}

func TestSystemSettingsService_GetLogLevel_Error(t *testing.T) {
	svc, mockRepo := newTestSystemSettingsService()

	mockRepo.On("Get").Return(nil, errors.New("db error"))

	result := svc.GetLogLevel()

	assert.Equal(t, "info", result) // falls back to config
	mockRepo.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// Update - more edge cases
// ---------------------------------------------------------------------------

func TestSystemSettingsService_Update_InvalidRefreshTokenExpiry(t *testing.T) {
	svc, _ := newTestSystemSettingsService()

	input := services.SystemSettingsInput{
		RefreshTokenExpiry: "invalid",
	}

	result, err := svc.Update(input)

	assert.Nil(t, result)
	assert.EqualError(t, err, "invalid refresh token expiry duration format (e.g., 168h, 720h)")
}

func TestSystemSettingsService_Update_LogLevelNormalized(t *testing.T) {
	svc, mockRepo := newTestSystemSettingsService()

	existing := &models.SystemSettings{}
	mockRepo.On("Get").Return(existing, nil)
	mockRepo.On("Update", mock.AnythingOfType("*models.SystemSettings")).Return(nil)

	input := services.SystemSettingsInput{
		LogLevel: "DEBUG",
	}

	result, err := svc.Update(input)

	require.NoError(t, err)
	assert.Equal(t, "debug", result.LogLevel)
	mockRepo.AssertExpectations(t)
}
