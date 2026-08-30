package services_test

import (
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/internal/config"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func newTestAuthService() (*services.AuthService, *mocks.MockUserRepository, *mocks.MockAPITokenRepository) {
	mockUserRepo := new(mocks.MockUserRepository)
	mockAPITokenRepo := new(mocks.MockAPITokenRepository)
	cfg := &config.Config{
		JWTSecret:          "test-secret-key-for-testing-only",
		JWTExpiry:          time.Hour,
		RefreshTokenExpiry: 24 * time.Hour,
	}
	svc := services.NewAuthService(mockUserRepo, mockAPITokenRepo, cfg)
	return svc, mockUserRepo, mockAPITokenRepo
}

func TestAuthService_Login_Success(t *testing.T) {
	svc, mockUserRepo, _ := newTestAuthService()

	passwordHash, _ := services.HashPassword("correct-password")
	user := &models.User{
		ID:           uuid.New(),
		Username:     "testuser",
		PasswordHash: &passwordHash,
		Role:         models.UserRoleUser,
		IsActive:     true,
		AuthProvider: "local",
	}

	mockUserRepo.On("GetByUsername", "testuser").Return(user, nil)

	resp, err := svc.Login("testuser", "correct-password")

	require.NoError(t, err)
	assert.NotEmpty(t, resp.AccessToken)
	assert.NotEmpty(t, resp.RefreshToken)
	assert.Equal(t, user, resp.User)
	assert.False(t, resp.ExpiresAt.IsZero())
	mockUserRepo.AssertExpectations(t)
}

func TestAuthService_Login_WrongPassword(t *testing.T) {
	svc, mockUserRepo, _ := newTestAuthService()

	passwordHash, _ := services.HashPassword("correct-password")
	user := &models.User{
		ID:           uuid.New(),
		Username:     "testuser",
		PasswordHash: &passwordHash,
		Role:         models.UserRoleUser,
		IsActive:     true,
		AuthProvider: "local",
	}

	mockUserRepo.On("GetByUsername", "testuser").Return(user, nil)

	resp, err := svc.Login("testuser", "wrong-password")

	assert.Nil(t, resp)
	assert.EqualError(t, err, "invalid credentials")
	mockUserRepo.AssertExpectations(t)
}

func TestAuthService_Login_UserNotFound(t *testing.T) {
	svc, mockUserRepo, _ := newTestAuthService()

	mockUserRepo.On("GetByUsername", "nonexistent").Return(nil, gorm.ErrRecordNotFound)

	resp, err := svc.Login("nonexistent", "password")

	assert.Nil(t, resp)
	assert.EqualError(t, err, "invalid credentials")
	mockUserRepo.AssertExpectations(t)
}

func TestAuthService_Login_InactiveUser(t *testing.T) {
	svc, mockUserRepo, _ := newTestAuthService()

	passwordHash, _ := services.HashPassword("password")
	user := &models.User{
		ID:           uuid.New(),
		Username:     "inactive",
		PasswordHash: &passwordHash,
		Role:         models.UserRoleUser,
		IsActive:     false,
		AuthProvider: "local",
	}

	mockUserRepo.On("GetByUsername", "inactive").Return(user, nil)

	resp, err := svc.Login("inactive", "password")

	assert.Nil(t, resp)
	assert.EqualError(t, err, "user account is disabled")
	mockUserRepo.AssertExpectations(t)
}

func TestAuthService_Login_OIDCUser(t *testing.T) {
	svc, mockUserRepo, _ := newTestAuthService()

	user := &models.User{
		ID:           uuid.New(),
		Username:     "oidcuser",
		Role:         models.UserRoleUser,
		IsActive:     true,
		AuthProvider: "oidc",
	}

	mockUserRepo.On("GetByUsername", "oidcuser").Return(user, nil)

	resp, err := svc.Login("oidcuser", "password")

	assert.Nil(t, resp)
	assert.EqualError(t, err, "please use SSO to log in")
	mockUserRepo.AssertExpectations(t)
}

func TestAuthService_ValidateToken_Success(t *testing.T) {
	svc, mockUserRepo, _ := newTestAuthService()

	user := &models.User{
		ID:       uuid.New(),
		Username: "testuser",
		Role:     models.UserRoleUser,
		IsActive: true,
	}

	// Generate a valid token using the public method
	token, _, err := svc.GenerateTokensForUser(user)
	require.NoError(t, err)

	mockUserRepo.On("GetByID", user.ID).Return(user, nil)

	result, err := svc.ValidateToken(token)

	require.NoError(t, err)
	assert.Equal(t, user.ID, result.ID)
	mockUserRepo.AssertExpectations(t)
}

func TestAuthService_ValidateToken_InvalidToken(t *testing.T) {
	svc, _, _ := newTestAuthService()

	result, err := svc.ValidateToken("invalid-token")

	assert.Nil(t, result)
	assert.EqualError(t, err, "invalid token")
}

func TestAuthService_CreateAPIToken_Success(t *testing.T) {
	svc, _, mockAPITokenRepo := newTestAuthService()

	userID := uuid.New()
	mockAPITokenRepo.On("CountByUserID", userID).Return(int64(0), nil)
	mockAPITokenRepo.On("Create", mock.AnythingOfType("*models.APIToken")).Return(nil)

	token, rawToken, err := svc.CreateAPIToken(userID, "my-token", nil)

	require.NoError(t, err)
	assert.NotNil(t, token)
	assert.NotEmpty(t, rawToken)
	assert.Equal(t, "my-token", token.Name)
	assert.Equal(t, userID, token.UserID)
	mockAPITokenRepo.AssertExpectations(t)
}

func TestAuthService_CreateAPIToken_LimitReached(t *testing.T) {
	svc, _, mockAPITokenRepo := newTestAuthService()

	userID := uuid.New()
	mockAPITokenRepo.On("CountByUserID", userID).Return(int64(10), nil)

	token, rawToken, err := svc.CreateAPIToken(userID, "my-token", nil)

	assert.Nil(t, token)
	assert.Empty(t, rawToken)
	assert.EqualError(t, err, "maximum number of API tokens reached (10)")
	mockAPITokenRepo.AssertExpectations(t)
}

func TestAuthService_ChangePassword_Success(t *testing.T) {
	svc, mockUserRepo, _ := newTestAuthService()

	userID := uuid.New()
	passwordHash, _ := services.HashPassword("old-password")
	user := &models.User{
		ID:           userID,
		PasswordHash: &passwordHash,
	}

	mockUserRepo.On("GetByID", userID).Return(user, nil)
	mockUserRepo.On("Update", mock.AnythingOfType("*models.User")).Return(nil)

	err := svc.ChangePassword(userID, "old-password", "new-password-123")

	require.NoError(t, err)
	mockUserRepo.AssertExpectations(t)
}

func TestAuthService_ChangePassword_WrongCurrent(t *testing.T) {
	svc, mockUserRepo, _ := newTestAuthService()

	userID := uuid.New()
	passwordHash, _ := services.HashPassword("correct-password")
	user := &models.User{
		ID:           userID,
		PasswordHash: &passwordHash,
	}

	mockUserRepo.On("GetByID", userID).Return(user, nil)

	err := svc.ChangePassword(userID, "wrong-password", "new-password-123")

	assert.EqualError(t, err, "current password is incorrect")
	mockUserRepo.AssertExpectations(t)
}

func TestAuthService_ChangePassword_TooShort(t *testing.T) {
	svc, mockUserRepo, _ := newTestAuthService()

	userID := uuid.New()
	passwordHash, _ := services.HashPassword("old-password")
	user := &models.User{
		ID:           userID,
		PasswordHash: &passwordHash,
	}

	mockUserRepo.On("GetByID", userID).Return(user, nil)

	err := svc.ChangePassword(userID, "old-password", "short")

	assert.EqualError(t, err, "new password must be at least 8 characters")
	mockUserRepo.AssertExpectations(t)
}

func TestAuthService_ChangePassword_UserNotFound(t *testing.T) {
	svc, mockUserRepo, _ := newTestAuthService()

	userID := uuid.New()
	mockUserRepo.On("GetByID", userID).Return(nil, gorm.ErrRecordNotFound)

	err := svc.ChangePassword(userID, "old", "new-password-123")

	assert.EqualError(t, err, "user not found")
	mockUserRepo.AssertExpectations(t)
}

func TestAuthService_RevokeAPIToken_Success(t *testing.T) {
	svc, _, mockAPITokenRepo := newTestAuthService()

	userID := uuid.New()
	tokenID := uuid.New()
	token := &models.APIToken{ID: tokenID, UserID: userID}

	mockAPITokenRepo.On("GetByID", tokenID).Return(token, nil)
	mockAPITokenRepo.On("Delete", tokenID).Return(nil)

	err := svc.RevokeAPIToken(tokenID, userID)

	require.NoError(t, err)
	mockAPITokenRepo.AssertExpectations(t)
}

func TestAuthService_RevokeAPIToken_WrongUser(t *testing.T) {
	svc, _, mockAPITokenRepo := newTestAuthService()

	ownerID := uuid.New()
	otherID := uuid.New()
	tokenID := uuid.New()
	token := &models.APIToken{ID: tokenID, UserID: ownerID}

	mockAPITokenRepo.On("GetByID", tokenID).Return(token, nil)

	err := svc.RevokeAPIToken(tokenID, otherID)

	assert.EqualError(t, err, "token not found")
	mockAPITokenRepo.AssertExpectations(t)
}

func TestAuthService_RefreshToken_Success(t *testing.T) {
	svc, mockUserRepo, _ := newTestAuthService()

	user := &models.User{
		ID:       uuid.New(),
		Username: "testuser",
		Role:     models.UserRoleUser,
		IsActive: true,
	}

	// Generate a valid refresh token
	_, refreshToken, err := svc.GenerateTokensForUser(user)
	require.NoError(t, err)

	mockUserRepo.On("GetByID", user.ID).Return(user, nil)

	resp, err := svc.RefreshToken(refreshToken)

	require.NoError(t, err)
	assert.NotEmpty(t, resp.AccessToken)
	assert.NotEmpty(t, resp.RefreshToken)
	assert.Equal(t, user.ID, resp.User.ID)
	assert.False(t, resp.ExpiresAt.IsZero())
	mockUserRepo.AssertExpectations(t)
}

func TestAuthService_RefreshToken_InvalidToken(t *testing.T) {
	svc, _, _ := newTestAuthService()

	resp, err := svc.RefreshToken("invalid-token-string")

	assert.Nil(t, resp)
	assert.EqualError(t, err, "invalid refresh token")
}

func TestAuthService_RefreshToken_InactiveUser(t *testing.T) {
	svc, mockUserRepo, _ := newTestAuthService()

	user := &models.User{
		ID:       uuid.New(),
		Username: "testuser",
		Role:     models.UserRoleUser,
		IsActive: true,
	}

	_, refreshToken, err := svc.GenerateTokensForUser(user)
	require.NoError(t, err)

	// Return user as inactive when looked up
	inactiveUser := &models.User{
		ID:       user.ID,
		Username: "testuser",
		Role:     models.UserRoleUser,
		IsActive: false,
	}
	mockUserRepo.On("GetByID", user.ID).Return(inactiveUser, nil)

	resp, err := svc.RefreshToken(refreshToken)

	assert.Nil(t, resp)
	assert.EqualError(t, err, "user account is disabled")
	mockUserRepo.AssertExpectations(t)
}

func TestAuthService_RefreshToken_UserNotFound(t *testing.T) {
	svc, mockUserRepo, _ := newTestAuthService()

	user := &models.User{
		ID:       uuid.New(),
		Username: "testuser",
		Role:     models.UserRoleUser,
		IsActive: true,
	}

	_, refreshToken, err := svc.GenerateTokensForUser(user)
	require.NoError(t, err)

	mockUserRepo.On("GetByID", user.ID).Return(nil, gorm.ErrRecordNotFound)

	resp, err := svc.RefreshToken(refreshToken)

	assert.Nil(t, resp)
	assert.EqualError(t, err, "user not found")
	mockUserRepo.AssertExpectations(t)
}

func TestAuthService_ListAPITokens_Success(t *testing.T) {
	svc, _, mockAPITokenRepo := newTestAuthService()

	userID := uuid.New()
	tokens := []models.APIToken{
		{ID: uuid.New(), UserID: userID, Name: "token-1"},
		{ID: uuid.New(), UserID: userID, Name: "token-2"},
	}
	mockAPITokenRepo.On("ListByUserID", userID).Return(tokens, nil)

	result, err := svc.ListAPITokens(userID)

	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, "token-1", result[0].Name)
	assert.Equal(t, "token-2", result[1].Name)
	mockAPITokenRepo.AssertExpectations(t)
}

func TestAuthService_ListAPITokens_Empty(t *testing.T) {
	svc, _, mockAPITokenRepo := newTestAuthService()

	userID := uuid.New()
	mockAPITokenRepo.On("ListByUserID", userID).Return([]models.APIToken{}, nil)

	result, err := svc.ListAPITokens(userID)

	require.NoError(t, err)
	assert.Empty(t, result)
	mockAPITokenRepo.AssertExpectations(t)
}

func TestAuthService_CountAPITokens_Success(t *testing.T) {
	svc, _, mockAPITokenRepo := newTestAuthService()

	userID := uuid.New()
	mockAPITokenRepo.On("CountByUserID", userID).Return(int64(5), nil)

	count, err := svc.CountAPITokens(userID)

	require.NoError(t, err)
	assert.Equal(t, int64(5), count)
	mockAPITokenRepo.AssertExpectations(t)
}

func TestAuthService_CountAPITokens_Zero(t *testing.T) {
	svc, _, mockAPITokenRepo := newTestAuthService()

	userID := uuid.New()
	mockAPITokenRepo.On("CountByUserID", userID).Return(int64(0), nil)

	count, err := svc.CountAPITokens(userID)

	require.NoError(t, err)
	assert.Equal(t, int64(0), count)
	mockAPITokenRepo.AssertExpectations(t)
}

func TestAuthService_CountAPITokens_Error(t *testing.T) {
	svc, _, mockAPITokenRepo := newTestAuthService()

	userID := uuid.New()
	mockAPITokenRepo.On("CountByUserID", userID).Return(int64(0), gorm.ErrRecordNotFound)

	count, err := svc.CountAPITokens(userID)

	assert.Equal(t, int64(0), count)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	mockAPITokenRepo.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// ValidateAPIToken
// ---------------------------------------------------------------------------

func TestAuthService_ValidateAPIToken_Success(t *testing.T) {
	svc, _, mockAPITokenRepo := newTestAuthService()

	rawToken := "my-api-token-value"
	userID := uuid.New()
	tokenID := uuid.New()
	apiToken := &models.APIToken{
		ID:     tokenID,
		UserID: userID,
		Name:   "my-token",
		User:   models.User{ID: userID, Username: "apiuser", Role: models.UserRoleUser, IsActive: true},
	}

	mockAPITokenRepo.On("GetByTokenHash", mock.AnythingOfType("string")).Return(apiToken, nil)
	mockAPITokenRepo.On("UpdateLastUsed", tokenID).Return(nil)

	user, err := svc.ValidateAPIToken(rawToken)

	require.NoError(t, err)
	assert.Equal(t, userID, user.ID)
	assert.Equal(t, "apiuser", user.Username)
	mockAPITokenRepo.AssertExpectations(t)
}

func TestAuthService_ValidateAPIToken_InvalidHash(t *testing.T) {
	svc, _, mockAPITokenRepo := newTestAuthService()

	mockAPITokenRepo.On("GetByTokenHash", mock.AnythingOfType("string")).Return(nil, gorm.ErrRecordNotFound)

	user, err := svc.ValidateAPIToken("bad-token")

	assert.Nil(t, user)
	assert.EqualError(t, err, "invalid API token")
	mockAPITokenRepo.AssertExpectations(t)
}

func TestAuthService_ValidateAPIToken_Expired(t *testing.T) {
	svc, _, mockAPITokenRepo := newTestAuthService()

	userID := uuid.New()
	expired := time.Now().Add(-1 * time.Hour)
	apiToken := &models.APIToken{
		ID:        uuid.New(),
		UserID:    userID,
		Name:      "expired-token",
		ExpiresAt: &expired,
		User:      models.User{ID: userID, Username: "apiuser"},
	}

	mockAPITokenRepo.On("GetByTokenHash", mock.AnythingOfType("string")).Return(apiToken, nil)

	user, err := svc.ValidateAPIToken("some-token")

	assert.Nil(t, user)
	assert.EqualError(t, err, "API token expired")
	mockAPITokenRepo.AssertExpectations(t)
}

func TestAuthService_ValidateAPIToken_NotExpired(t *testing.T) {
	svc, _, mockAPITokenRepo := newTestAuthService()

	userID := uuid.New()
	tokenID := uuid.New()
	future := time.Now().Add(24 * time.Hour)
	apiToken := &models.APIToken{
		ID:        tokenID,
		UserID:    userID,
		Name:      "valid-token",
		ExpiresAt: &future,
		User:      models.User{ID: userID, Username: "apiuser"},
	}

	mockAPITokenRepo.On("GetByTokenHash", mock.AnythingOfType("string")).Return(apiToken, nil)
	mockAPITokenRepo.On("UpdateLastUsed", tokenID).Return(nil)

	user, err := svc.ValidateAPIToken("some-token")

	require.NoError(t, err)
	assert.Equal(t, userID, user.ID)
	mockAPITokenRepo.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// SetSSOService / SetSystemSettingsService
// ---------------------------------------------------------------------------

func TestAuthService_SetSSOService(t *testing.T) {
	svc, _, _ := newTestAuthService()

	// Should not panic when setting SSO service
	mockSSORepo := new(mocks.MockSSOConfigRepository)
	ssoSvc := services.NewSSOService(mockSSORepo, nil, nil, nil, &config.Config{})
	svc.SetSSOService(ssoSvc)

	// Verify force SSO is now checked during login
	// Login with a user who has force SSO applicable
	mockUserRepo := new(mocks.MockUserRepository)
	cfg := &config.Config{
		JWTSecret:          "test-secret-key-for-testing-only",
		JWTExpiry:          time.Hour,
		RefreshTokenExpiry: 24 * time.Hour,
	}
	svc2 := services.NewAuthService(mockUserRepo, nil, cfg)
	svc2.SetSSOService(ssoSvc)

	passwordHash, _ := services.HashPassword("password")
	user := &models.User{
		ID:           uuid.New(),
		Username:     "testuser",
		Email:        "user@example.com",
		PasswordHash: &passwordHash,
		Role:         models.UserRoleUser,
		IsActive:     true,
		AuthProvider: "local",
	}
	mockUserRepo.On("GetByUsername", "testuser").Return(user, nil)

	// SSO config says force SSO for all
	ssoConfig := &models.SSOConfig{
		Enabled:  true,
		ForceSSO: true,
	}
	mockSSORepo.On("Get").Return(ssoConfig, nil)

	resp, err := svc2.Login("testuser", "password")

	assert.Nil(t, resp)
	assert.EqualError(t, err, "please use SSO to log in")
	mockUserRepo.AssertExpectations(t)
}

func TestAuthService_SetSystemSettingsService(t *testing.T) {
	svc, _, _ := newTestAuthService()
	mockSettingsRepo := new(mocks.MockSystemSettingsRepository)
	settingsSvc := services.NewSystemSettingsService(mockSettingsRepo, &config.Config{
		JWTExpiry:          time.Hour,
		RefreshTokenExpiry: 24 * time.Hour,
	})
	// Should not panic
	svc.SetSystemSettingsService(settingsSvc)
}

func TestAuthService_GenerateAccessToken_WithSystemSettings(t *testing.T) {
	mockUserRepo := new(mocks.MockUserRepository)
	mockAPITokenRepo := new(mocks.MockAPITokenRepository)
	cfg := &config.Config{
		JWTSecret:          "test-secret-key-for-testing-only",
		JWTExpiry:          time.Hour,
		RefreshTokenExpiry: 24 * time.Hour,
	}
	svc := services.NewAuthService(mockUserRepo, mockAPITokenRepo, cfg)

	// Set system settings service with custom JWT expiry
	mockSettingsRepo := new(mocks.MockSystemSettingsRepository)
	settingsSvc := services.NewSystemSettingsService(mockSettingsRepo, cfg)
	svc.SetSystemSettingsService(settingsSvc)

	settings := &models.SystemSettings{JWTExpiry: "30m", RefreshTokenExpiry: "12h"}
	mockSettingsRepo.On("Get").Return(settings, nil)

	user := &models.User{
		ID:       uuid.New(),
		Username: "testuser",
		Role:     models.UserRoleUser,
		IsActive: true,
	}

	accessToken, refreshToken, err := svc.GenerateTokensForUser(user)
	require.NoError(t, err)
	assert.NotEmpty(t, accessToken)
	assert.NotEmpty(t, refreshToken)
}

// ---------------------------------------------------------------------------
// Login - NilPasswordHash
// ---------------------------------------------------------------------------

func TestAuthService_Login_NilPasswordHash(t *testing.T) {
	svc, mockUserRepo, _ := newTestAuthService()

	user := &models.User{
		ID:           uuid.New(),
		Username:     "nopassuser",
		PasswordHash: nil,
		Role:         models.UserRoleUser,
		IsActive:     true,
		AuthProvider: "local",
	}
	mockUserRepo.On("GetByUsername", "nopassuser").Return(user, nil)

	resp, err := svc.Login("nopassuser", "password")

	assert.Nil(t, resp)
	assert.EqualError(t, err, "invalid credentials")
	mockUserRepo.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// ChangePassword - NoPasswordHash
// ---------------------------------------------------------------------------

func TestAuthService_ChangePassword_NoPasswordHash(t *testing.T) {
	svc, mockUserRepo, _ := newTestAuthService()

	userID := uuid.New()
	user := &models.User{
		ID:           userID,
		PasswordHash: nil,
	}
	mockUserRepo.On("GetByID", userID).Return(user, nil)

	err := svc.ChangePassword(userID, "old", "new-password-123")

	assert.EqualError(t, err, "password login not available for this account")
	mockUserRepo.AssertExpectations(t)
}
