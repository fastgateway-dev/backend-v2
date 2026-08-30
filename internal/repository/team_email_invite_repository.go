package repository

import (
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type TeamEmailInviteRepository struct {
	db *gorm.DB
}

func NewTeamEmailInviteRepository(db *gorm.DB) *TeamEmailInviteRepository {
	return &TeamEmailInviteRepository{db: db}
}

func (r *TeamEmailInviteRepository) Create(invite *models.TeamEmailInvite) error {
	return r.db.Create(invite).Error
}

func (r *TeamEmailInviteRepository) GetByEmail(email string) ([]models.TeamEmailInvite, error) {
	var invites []models.TeamEmailInvite
	err := r.db.Preload("Team").Where("email = ?", email).Find(&invites).Error
	return invites, err
}

func (r *TeamEmailInviteRepository) ListByTeam(teamID uuid.UUID) ([]models.TeamEmailInvite, error) {
	var invites []models.TeamEmailInvite
	err := r.db.Preload("Inviter").Where("team_id = ?", teamID).Order("created_at DESC").Find(&invites).Error
	return invites, err
}

func (r *TeamEmailInviteRepository) Delete(id uuid.UUID) error {
	return r.db.Delete(&models.TeamEmailInvite{}, "id = ?", id).Error
}

func (r *TeamEmailInviteRepository) DeleteByEmail(email string) error {
	return r.db.Where("email = ?", email).Delete(&models.TeamEmailInvite{}).Error
}

func (r *TeamEmailInviteRepository) ListAll() ([]models.TeamEmailInvite, error) {
	var invites []models.TeamEmailInvite
	err := r.db.Preload("Team").Preload("Inviter").Order("created_at DESC").Find(&invites).Error
	return invites, err
}
