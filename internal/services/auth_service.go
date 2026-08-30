package services

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/fastgateway-dev/backend-v2/internal/config"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// AuthService handles authentication logic
type AuthService struct {
	userRepo              repository.UserRepositoryInterface
	apiTokenRepo          repository.APITokenRepositoryInterface
	config                *config.Config
	ssoService            *SSOService
	systemSettingsService *SystemSettingsService
}

// SetSSOService sets the SSO service reference (used for force-SSO checks in Login)
func (s *AuthService) SetSSOService(sso *SSOService) {
	s.ssoService = sso
}

// SetSystemSettingsService sets the system settings service reference
func (s *AuthService) SetSystemSettingsService(ss *SystemSettingsService) {
	s.systemSettingsService = ss
}

// NewAuthService creates a new auth service
func NewAuthService(userRepo repository.UserRepositoryInterface, apiTokenRepo repository.APITokenRepositoryInterface, cfg *config.Config) *AuthService {
	return &AuthService{
		userRepo:     userRepo,
		apiTokenRepo: apiTokenRepo,
		config:       cfg,
	}
}

// Claims represents JWT claims
type Claims struct {
	UserID   string `json:"userId"`
	Username string `json:"username"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

// LoginResponse represents the login response
type LoginResponse struct {
	AccessToken  string       `json:"accessToken"`
	RefreshToken string       `json:"refreshToken"`
	ExpiresAt    time.Time    `json:"expiresAt"`
	User         *models.User `json:"user"`
}

// Login authenticates a user and returns tokens
func (s *AuthService) Login(username, password string) (*LoginResponse, error) {
	user, err := s.userRepo.GetByUsername(username)
	if err != nil {
		return nil, errors.New("invalid credentials")
	}

	if !user.IsActive {
		return nil, errors.New("user account is disabled")
	}

	// Check if user is OIDC-only (no password set, SSO provider)
	if user.AuthProvider == "oidc" {
		return nil, errors.New("please use SSO to log in")
	}

	// Check if force SSO applies to this user
	if s.ssoService != nil && s.ssoService.ShouldForceSSO(user.Email, user.Role) {
		return nil, errors.New("please use SSO to log in")
	}

	// Check password is set
	if user.PasswordHash == nil {
		return nil, errors.New("invalid credentials")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(*user.PasswordHash), []byte(password)); err != nil {
		return nil, errors.New("invalid credentials")
	}

	accessToken, expiresAt, err := s.generateAccessToken(user)
	if err != nil {
		return nil, err
	}

	refreshToken, err := s.generateRefreshToken(user)
	if err != nil {
		return nil, err
	}

	return &LoginResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    expiresAt,
		User:         user,
	}, nil
}

// RefreshToken refreshes the access token
func (s *AuthService) RefreshToken(refreshToken string) (*LoginResponse, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(refreshToken, claims, func(token *jwt.Token) (interface{}, error) {
		return []byte(s.config.JWTSecret), nil
	})

	if err != nil || !token.Valid {
		return nil, errors.New("invalid refresh token")
	}

	userID, err := uuid.Parse(claims.UserID)
	if err != nil {
		return nil, errors.New("invalid user ID in token")
	}

	user, err := s.userRepo.GetByID(userID)
	if err != nil {
		return nil, errors.New("user not found")
	}

	if !user.IsActive {
		return nil, errors.New("user account is disabled")
	}

	accessToken, expiresAt, err := s.generateAccessToken(user)
	if err != nil {
		return nil, err
	}

	newRefreshToken, err := s.generateRefreshToken(user)
	if err != nil {
		return nil, err
	}

	return &LoginResponse{
		AccessToken:  accessToken,
		RefreshToken: newRefreshToken,
		ExpiresAt:    expiresAt,
		User:         user,
	}, nil
}

// ValidateToken validates a JWT token and returns the user
func (s *AuthService) ValidateToken(tokenString string) (*models.User, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
		return []byte(s.config.JWTSecret), nil
	})

	if err != nil || !token.Valid {
		return nil, errors.New("invalid token")
	}

	userID, err := uuid.Parse(claims.UserID)
	if err != nil {
		return nil, errors.New("invalid user ID in token")
	}

	return s.userRepo.GetByID(userID)
}

// ValidateAPIToken validates an API token and returns the user
func (s *AuthService) ValidateAPIToken(tokenString string) (*models.User, error) {
	hash := hashToken(tokenString)
	token, err := s.apiTokenRepo.GetByTokenHash(hash)
	if err != nil {
		return nil, errors.New("invalid API token")
	}

	if token.ExpiresAt != nil && token.ExpiresAt.Before(time.Now()) {
		return nil, errors.New("API token expired")
	}

	// Update last used
	_ = s.apiTokenRepo.UpdateLastUsed(token.ID)

	return &token.User, nil
}

// CreateAPIToken creates a new API token
func (s *AuthService) CreateAPIToken(userID uuid.UUID, name string, expiresAt *time.Time) (*models.APIToken, string, error) {
	// Check token limit (max 10 per user)
	count, err := s.apiTokenRepo.CountByUserID(userID)
	if err != nil {
		return nil, "", errors.New("failed to check token count")
	}
	if count >= 10 {
		return nil, "", errors.New("maximum number of API tokens reached (10)")
	}

	// Generate random token
	rawToken, err := generateRandomToken(32)
	if err != nil {
		return nil, "", err
	}

	token := &models.APIToken{
		UserID:    userID,
		Name:      name,
		TokenHash: hashToken(rawToken),
		ExpiresAt: expiresAt,
	}

	if err := s.apiTokenRepo.Create(token); err != nil {
		return nil, "", err
	}

	return token, rawToken, nil
}

// ListAPITokens lists API tokens for a user
func (s *AuthService) ListAPITokens(userID uuid.UUID) ([]models.APIToken, error) {
	return s.apiTokenRepo.ListByUserID(userID)
}

// CountAPITokens counts API tokens for a user
func (s *AuthService) CountAPITokens(userID uuid.UUID) (int64, error) {
	return s.apiTokenRepo.CountByUserID(userID)
}

// RevokeAPIToken revokes an API token
func (s *AuthService) RevokeAPIToken(tokenID, userID uuid.UUID) error {
	token, err := s.apiTokenRepo.GetByID(tokenID)
	if err != nil {
		return err
	}

	if token.UserID != userID {
		return errors.New("token not found")
	}

	return s.apiTokenRepo.Delete(tokenID)
}

// HashPassword hashes a password
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// ChangePassword changes a user's password after validating the current password
func (s *AuthService) ChangePassword(userID uuid.UUID, currentPassword, newPassword string) error {
	user, err := s.userRepo.GetByID(userID)
	if err != nil {
		return errors.New("user not found")
	}

	// Validate current password
	if user.PasswordHash == nil {
		return errors.New("password login not available for this account")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(*user.PasswordHash), []byte(currentPassword)); err != nil {
		return errors.New("current password is incorrect")
	}

	// Validate new password
	if len(newPassword) < 8 {
		return errors.New("new password must be at least 8 characters")
	}

	// Hash new password
	newHash, err := HashPassword(newPassword)
	if err != nil {
		return errors.New("failed to hash password")
	}

	// Update password
	user.PasswordHash = &newHash
	return s.userRepo.Update(user)
}

// GenerateTokensForUser generates access and refresh tokens for a given user.
// This is a public wrapper used by SSOService to generate tokens after SSO authentication.
func (s *AuthService) GenerateTokensForUser(user *models.User) (accessToken, refreshToken string, err error) {
	access, _, err := s.generateAccessToken(user)
	if err != nil {
		return "", "", err
	}

	refresh, err := s.generateRefreshToken(user)
	if err != nil {
		return "", "", err
	}

	return access, refresh, nil
}

func (s *AuthService) generateAccessToken(user *models.User) (string, time.Time, error) {
	jwtExpiry := s.config.JWTExpiry
	if s.systemSettingsService != nil {
		jwtExpiry = s.systemSettingsService.GetJWTExpiry()
	}
	expiresAt := time.Now().Add(jwtExpiry)
	claims := &Claims{
		UserID:   user.ID.String(),
		Username: user.Username,
		Role:     string(user.Role),
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Subject:   user.ID.String(),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(s.config.JWTSecret))
	if err != nil {
		return "", time.Time{}, err
	}

	return tokenString, expiresAt, nil
}

func (s *AuthService) generateRefreshToken(user *models.User) (string, error) {
	refreshExpiry := s.config.RefreshTokenExpiry
	if s.systemSettingsService != nil {
		refreshExpiry = s.systemSettingsService.GetRefreshTokenExpiry()
	}
	expiresAt := time.Now().Add(refreshExpiry)
	claims := &Claims{
		UserID:   user.ID.String(),
		Username: user.Username,
		Role:     string(user.Role),
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Subject:   user.ID.String(),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(s.config.JWTSecret))
}

func generateRandomToken(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func hashToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}
