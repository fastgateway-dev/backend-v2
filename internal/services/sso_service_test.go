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

// stubTokenIssuer stands in for *AuthService as SSOService's TokenIssuer.
// Phase 2E Task 4 made it a required constructor parameter; before that it
// arrived as a bound method value through SetTokenGenerator, and every test
// here left it unset.
type stubTokenIssuer struct {
	access  string
	refresh string
	err     error
	calls   int
}

func (s *stubTokenIssuer) GenerateTokensForUser(*models.User) (string, string, error) {
	s.calls++
	return s.access, s.refresh, s.err
}

// stubBaseURLProvider stands in for *SystemSettingsService as SSOService's
// BaseURLProvider. The zero value answers "no base URL configured", which is
// the reachable equivalent of the pre-2E unset systemSettingsService.
type stubBaseURLProvider struct{ baseURL string }

func (s stubBaseURLProvider) GetBaseURL() string { return s.baseURL }

// newTestSSOService creates a SSOService with mocks.
func newTestSSOService() (*services.SSOService, *mocks.MockSSOConfigRepository, *mocks.MockUserRepository, *mocks.MockTeamRepository, *mocks.MockTeamEmailInviteRepository) {
	svc, _, mockSSORepo, mockUserRepo, mockTeamRepo, mockInviteRepo := newTestSSOServiceWithIssuer()
	return svc, mockSSORepo, mockUserRepo, mockTeamRepo, mockInviteRepo
}

// newTestSSOServiceWithIssuer also hands back the TokenIssuer stub, for the
// tests that need to observe it.
func newTestSSOServiceWithIssuer() (*services.SSOService, *stubTokenIssuer, *mocks.MockSSOConfigRepository, *mocks.MockUserRepository, *mocks.MockTeamRepository, *mocks.MockTeamEmailInviteRepository) {
	mockSSORepo := new(mocks.MockSSOConfigRepository)
	mockUserRepo := new(mocks.MockUserRepository)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockInviteRepo := new(mocks.MockTeamEmailInviteRepository)
	cfg := &config.Config{
		EncryptionKey: "test-encryption-key-1234567890ab",
	}
	issuer := &stubTokenIssuer{access: "access", refresh: "refresh"}

	svc := services.NewSSOService(services.SSOServiceDeps{
		SSOConfigRepo:   mockSSORepo,
		UserRepo:        mockUserRepo,
		TeamRepo:        mockTeamRepo,
		EmailInviteRepo: mockInviteRepo,
		Config:          cfg,
		Tokens:          issuer,
		// The zero value answers "" -- see stubBaseURLProvider.
		Settings: stubBaseURLProvider{},
	})
	return svc, issuer, mockSSORepo, mockUserRepo, mockTeamRepo, mockInviteRepo
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

	// FINDING (Phase 2E Task 4): this test used to rely on
	// systemSettingsService being unset, which tripped the guard at
	// sso_service.go:136 ("SSO requires system settings service to be
	// configured"). Settings is a required constructor parameter now, and
	// Phase 2E Task 9 deleted that guard. The helper's
	// stub answers "" instead, which is the reachable state the test name
	// always described: no base URL configured. UpdateConfig must still
	// reject before ever touching the repository.
	result, err := svc.UpdateConfig(input)

	assert.Nil(t, result)
	assert.EqualError(t, err, "SSO requires a base URL to be configured in System Settings")
}

// ---------------------------------------------------------------------------
// TokenIssuer / BaseURLProvider (Phase 2E Task 4: these replaced
// SetTokenGenerator and SetSystemSettingsService)
// ---------------------------------------------------------------------------

func TestSSOService_UsesInjectedTokenIssuer(t *testing.T) {
	_, issuer, _, _, _, _ := newTestSSOServiceWithIssuer()

	// The two assertions that matter are the compile-time ones: they fail to
	// build if TokenIssuer/BaseURLProvider stop existing, or if the real
	// implementations stop satisfying them.
	var _ services.TokenIssuer = issuer
	var _ services.TokenIssuer = (*services.AuthService)(nil)
	var _ services.BaseURLProvider = stubBaseURLProvider{}
	var _ services.BaseURLProvider = (*services.SystemSettingsService)(nil)

	assert.Equal(t, 0, issuer.calls, "the issuer is only used on a callback")
}

func TestNewSSOService_RequiresEveryDependency(t *testing.T) {
	full := func() services.SSOServiceDeps {
		return services.SSOServiceDeps{
			SSOConfigRepo:   new(mocks.MockSSOConfigRepository),
			UserRepo:        new(mocks.MockUserRepository),
			TeamRepo:        new(mocks.MockTeamRepository),
			EmailInviteRepo: new(mocks.MockTeamEmailInviteRepository),
			Config:          &config.Config{},
			Tokens:          &stubTokenIssuer{},
			Settings:        stubBaseURLProvider{},
		}
	}
	require.NotPanics(t, func() { services.NewSSOService(full()) })

	cases := map[string]func(*services.SSOServiceDeps){
		"SSOConfigRepo":   func(d *services.SSOServiceDeps) { d.SSOConfigRepo = nil },
		"UserRepo":        func(d *services.SSOServiceDeps) { d.UserRepo = nil },
		"TeamRepo":        func(d *services.SSOServiceDeps) { d.TeamRepo = nil },
		"EmailInviteRepo": func(d *services.SSOServiceDeps) { d.EmailInviteRepo = nil },
		"Config":          func(d *services.SSOServiceDeps) { d.Config = nil },
		"Tokens":          func(d *services.SSOServiceDeps) { d.Tokens = nil },
		"Settings":        func(d *services.SSOServiceDeps) { d.Settings = nil },
	}
	for name, breakIt := range cases {
		t.Run("nil "+name, func(t *testing.T) {
			d := full()
			breakIt(&d)
			assert.PanicsWithValue(t,
				"services.NewSSOService: missing required dependency: "+name,
				func() { services.NewSSOService(d) })
		})
	}
}

func TestNewForceSSOPolicy_MatchesSSOService(t *testing.T) {
	// ForceSSOPolicy is the force-SSO decision extracted out of SSOService so
	// AuthService can take it as a required constructor parameter.
	var _ services.SSOPolicy = (*services.ForceSSOPolicy)(nil)
	var _ services.SSOPolicy = (*services.SSOService)(nil)

	repo := new(mocks.MockSSOConfigRepository)
	repo.On("Get").Return(&models.SSOConfig{Enabled: true, ForceSSO: true}, nil)
	policy := services.NewForceSSOPolicy(repo)

	assert.True(t, policy.ShouldForceSSO("user@example.com", models.UserRoleUser))
	assert.False(t, policy.ShouldForceSSO("owner@example.com", models.UserRoleOwner),
		"owners are always exempt")

	assert.PanicsWithValue(t,
		"services.NewForceSSOPolicy: missing required dependency: SSOConfigRepo",
		func() { services.NewForceSSOPolicy(nil) })
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
