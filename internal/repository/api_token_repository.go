package repository

import (
	"time"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// APITokenRepository handles API token database operations
type APITokenRepository struct {
	db *gorm.DB
}

// NewAPITokenRepository creates a new API token repository
func NewAPITokenRepository(db *gorm.DB) *APITokenRepository {
	return &APITokenRepository{db: db}
}

// Create creates a new API token
func (r *APITokenRepository) Create(token *models.APIToken) error {
	return r.db.Create(token).Error
}

// GetByID gets an API token by ID
func (r *APITokenRepository) GetByID(id uuid.UUID) (*models.APIToken, error) {
	var token models.APIToken
	err := r.db.Where("id = ?", id).First(&token).Error
	if err != nil {
		return nil, err
	}
	return &token, nil
}

// GetByTokenHash gets an API token by token hash
func (r *APITokenRepository) GetByTokenHash(hash string) (*models.APIToken, error) {
	var token models.APIToken
	err := r.db.Preload("User").Where("token_hash = ?", hash).First(&token).Error
	if err != nil {
		return nil, err
	}
	return &token, nil
}

// ListByUserID lists API tokens for a user
func (r *APITokenRepository) ListByUserID(userID uuid.UUID) ([]models.APIToken, error) {
	var tokens []models.APIToken
	err := r.db.Where("user_id = ?", userID).Order("created_at DESC").Find(&tokens).Error
	if err != nil {
		return nil, err
	}
	return tokens, nil
}

// CountByUserID counts API tokens for a user
func (r *APITokenRepository) CountByUserID(userID uuid.UUID) (int64, error) {
	var count int64
	err := r.db.Model(&models.APIToken{}).Where("user_id = ?", userID).Count(&count).Error
	return count, err
}

// UpdateLastUsed updates the last used timestamp
func (r *APITokenRepository) UpdateLastUsed(id uuid.UUID) error {
	now := time.Now()
	return r.db.Model(&models.APIToken{}).Where("id = ?", id).Update("last_used_at", now).Error
}

// Delete deletes an API token
func (r *APITokenRepository) Delete(id uuid.UUID) error {
	return r.db.Delete(&models.APIToken{}, "id = ?", id).Error
}

// DeleteExpired deletes expired API tokens
func (r *APITokenRepository) DeleteExpired() error {
	return r.db.Delete(&models.APIToken{}, "expires_at IS NOT NULL AND expires_at < ?", time.Now()).Error
}
