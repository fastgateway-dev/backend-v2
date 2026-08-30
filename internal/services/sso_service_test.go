package services_test

import (
	"errors"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/config"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestSSOService creates a SSOService with mocks.
func newTestSSOService() (*services.SSOService, *mocks.MockSSOConfigRepository, *mocks.MockUserRepository, *mocks.MockTeamRepository, *mocks.MockTeamEmailInviteRepository) {
	mockSSORepo := new(mocks.MockSSOConfigRepository)
	mockUserRepo := new(mocks.MockUserRepository)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockInviteRepo := new(mocks.MockTeamEmailInviteRepository)
	cfg := &config.Config{
		EncryptionKey: "test-encryption-key-1234567890ab",
	}

	svc := services.NewSSOService(mockSSORepo, mockUserRepo, mockTeamRepo, mockInviteRepo, cfg)
	return svc, mockSSORepo, mockUserRepo, mockTeamRepo, mockInviteRepo
}

func TestSSOService_GetPublicConfig_Success(t *testing.T) {
	svc, mockSSORepo, _, _, _ := newTestSSOService()

	ssoConfig := &models.SSOConfig{
		ID:             uuid.New(),
		Enabled:        true,
		ProviderName:   "Okta",
		ForceSSO:       true,
		AllowedDomains: []string{"example.com"},
	}
	mockSSORepo.On("Get").Return(ssoConfig, nil)

	result, err := svc.GetPublicConfig()

	require.NoError(t, err)
	assert.True(t, result.Enabled)
	assert.Equal(t, "Okta", result.ProviderName)
	assert.True(t, result.ForceSSO)
	assert.Equal(t, []string{"example.com"}, result.AllowedDomains)
	mockSSORepo.AssertExpectations(t)
}

func TestSSOService_GetPublicConfig_NoConfig(t *testing.T) {
	svc, mockSSORepo, _, _, _ := newTestSSOService()

	mockSSORepo.On("Get").Return(nil, errors.New("not found"))

	result, err := svc.GetPublicConfig()

	require.NoError(t, err)
	assert.False(t, result.Enabled)
	mockSSORepo.AssertExpectations(t)
}

func TestSSOService_ShouldForceSSO_OwnerExempt(t *testing.T) {
	svc, mockSSORepo, _, _, _ := newTestSSOService()

	ssoConfig := &models.SSOConfig{
		Enabled:  true,
		ForceSSO: true,
	}
	_ = ssoConfig
	_ = mockSSORepo

	// Owners should always be exempt, regardless of config
	result := svc.ShouldForceSSO("admin@example.com", models.UserRoleOwner)
	assert.False(t, result)
}

func TestSSOService_ShouldForceSSO_Disabled(t *testing.T) {
	svc, mockSSORepo, _, _, _ := newTestSSOService()

	ssoConfig := &models.SSOConfig{
		Enabled:  false,
		ForceSSO: true,
	}
	mockSSORepo.On("Get").Return(ssoConfig, nil)

	result := svc.ShouldForceSSO("user@example.com", models.UserRoleUser)
	assert.False(t, result)
	mockSSORepo.AssertExpectations(t)
}

func TestSSOService_ShouldForceSSO_NotForced(t *testing.T) {
	svc, mockSSORepo, _, _, _ := newTestSSOService()

	ssoConfig := &models.SSOConfig{
		Enabled:  true,
		ForceSSO: false,
	}
	mockSSORepo.On("Get").Return(ssoConfig, nil)

	result := svc.ShouldForceSSO("user@example.com", models.UserRoleUser)
	assert.False(t, result)
	mockSSORepo.AssertExpectations(t)
}

func TestSSOService_ShouldForceSSO_ForcedAllDomains(t *testing.T) {
	svc, mockSSORepo, _, _, _ := newTestSSOService()

	ssoConfig := &models.SSOConfig{
		Enabled:  true,
		ForceSSO: true,
		// No AllowedDomains = force for everyone
	}
	mockSSORepo.On("Get").Return(ssoConfig, nil)

	result := svc.ShouldForceSSO("user@example.com", models.UserRoleUser)
	assert.True(t, result)
	mockSSORepo.AssertExpectations(t)
}

func TestSSOService_ShouldForceSSO_ForcedMatchingDomain(t *testing.T) {
	svc, mockSSORepo, _, _, _ := newTestSSOService()

	ssoConfig := &models.SSOConfig{
		Enabled:        true,
		ForceSSO:       true,
		AllowedDomains: []string{"example.com"},
	}
	mockSSORepo.On("Get").Return(ssoConfig, nil)

	result := svc.ShouldForceSSO("user@example.com", models.UserRoleUser)
	assert.True(t, result)
	mockSSORepo.AssertExpectations(t)
}

func TestSSOService_ShouldForceSSO_ForcedNonMatchingDomain(t *testing.T) {
	svc, mockSSORepo, _, _, _ := newTestSSOService()

	ssoConfig := &models.SSOConfig{
		Enabled:        true,
		ForceSSO:       true,
		AllowedDomains: []string{"example.com"},
	}
	mockSSORepo.On("Get").Return(ssoConfig, nil)

	result := svc.ShouldForceSSO("user@other.com", models.UserRoleUser)
	assert.False(t, result)
	mockSSORepo.AssertExpectations(t)
}

func TestSSOService_ShouldForceSSO_RepoError(t *testing.T) {
	svc, mockSSORepo, _, _, _ := newTestSSOService()

	mockSSORepo.On("Get").Return(nil, errors.New("db error"))

	result := svc.ShouldForceSSO("user@example.com", models.UserRoleUser)
	assert.False(t, result)
	mockSSORepo.AssertExpectations(t)
}

func TestSSOService_UpdateConfig_NoBaseURL(t *testing.T) {
	svc, _, _, _, _ := newTestSSOService()

	input := services.SSOConfigInput{
		ProviderName: "Okta",
		IssuerURL:    "https://example.okta.com",
		ClientID:     "client-id",
		ClientSecret: "client-secret",
	}

	// SystemSettingsService was never wired in, so UpdateConfig should reject
	// before ever touching the repository.
	result, err := svc.UpdateConfig(input)

	assert.Nil(t, result)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "system settings")
}

// ---------------------------------------------------------------------------
// SetTokenGenerator / SetSystemSettingsService
// ---------------------------------------------------------------------------

func TestSSOService_SetTokenGenerator(t *testing.T) {
	svc, _, _, _, _ := newTestSSOService()

	called := false
	svc.SetTokenGenerator(func(user *models.User) (string, string, error) {
		called = true
		return "access", "refresh", nil
	})
	// Just verify it doesn't panic - the function is stored internally
	assert.False(t, called) // not called yet
}

func TestSSOService_SetSystemSettingsService(t *testing.T) {
	svc, _, _, _, _ := newTestSSOService()

	mockSettingsRepo := new(mocks.MockSystemSettingsRepository)
	settingsSvc := services.NewSystemSettingsService(mockSettingsRepo, &config.Config{})
	// Should not panic
	svc.SetSystemSettingsService(settingsSvc)
}

// ---------------------------------------------------------------------------
// GetConfig
// ---------------------------------------------------------------------------

func TestSSOService_GetConfig_Success(t *testing.T) {
	svc, mockSSORepo, _, _, _ := newTestSSOService()

	ssoConfig := &models.SSOConfig{
		ID:           uuid.New(),
		Enabled:      true,
		ProviderName: "Okta",
	}
	mockSSORepo.On("Get").Return(ssoConfig, nil)

	result, err := svc.GetConfig()

	require.NoError(t, err)
	assert.Equal(t, ssoConfig, result)
	mockSSORepo.AssertExpectations(t)
}

func TestSSOService_GetConfig_ConfigNotFound(t *testing.T) {
	svc, mockSSORepo, _, _, _ := newTestSSOService()

	mockSSORepo.On("Get").Return(nil, errors.New("not found"))

	result, err := svc.GetConfig()
	assert.Nil(t, result)
	assert.Error(t, err)
	mockSSORepo.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// DisableSSO - more paths
// ---------------------------------------------------------------------------

func TestSSOService_DisableSSO_ConfigNotFound(t *testing.T) {
	svc, mockSSORepo, _, _, _ := newTestSSOService()

	mockSSORepo.On("Get").Return(nil, errors.New("not found"))

	err := svc.DisableSSO()
	assert.Error(t, err)
	mockSSORepo.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// ShouldForceSSO - admin role
// ---------------------------------------------------------------------------

func TestSSOService_ShouldForceSSO_UserNotExempt(t *testing.T) {
	svc, mockSSORepo, _, _, _ := newTestSSOService()

	ssoConfig := &models.SSOConfig{
		Enabled:  true,
		ForceSSO: true,
	}
	mockSSORepo.On("Get").Return(ssoConfig, nil)

	// Regular users should be forced (only Owner is exempt)
	result := svc.ShouldForceSSO("user@example.com", models.UserRoleUser)
	assert.True(t, result)
	mockSSORepo.AssertExpectations(t)
}

func TestSSOService_ShouldForceSSO_CaseInsensitiveDomain(t *testing.T) {
	svc, mockSSORepo, _, _, _ := newTestSSOService()

	ssoConfig := &models.SSOConfig{
		Enabled:        true,
		ForceSSO:       true,
		AllowedDomains: []string{"Example.COM"},
	}
	mockSSORepo.On("Get").Return(ssoConfig, nil)

	result := svc.ShouldForceSSO("user@example.com", models.UserRoleUser)
	assert.True(t, result)
	mockSSORepo.AssertExpectations(t)
}
