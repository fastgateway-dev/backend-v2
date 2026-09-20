package repository

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/fastgateway-dev/backend-v2/internal/models"
)

// CertificateListFilter carries optional list filters. Zero values mean "no filter".
type CertificateListFilter struct {
	Status        string     // exact ManagedCertStatus match
	IssuerID      *uuid.UUID // exact issuer
	Usage         string     // "server" | "client"
	ExpiresBefore *time.Time // not_after <= t
	ProjectID     *uuid.UUID // fleet only; project view fixes this from the path
}

type ManagedCertificateRepository struct{ db *gorm.DB }

func NewManagedCertificateRepository(db *gorm.DB) *ManagedCertificateRepository {
	return &ManagedCertificateRepository{db: db}
}

func (r *ManagedCertificateRepository) Create(c *models.ManagedCertificate) error {
	return r.db.Create(c).Error
}

func (r *ManagedCertificateRepository) GetByID(id uuid.UUID) (*models.ManagedCertificate, error) {
	var c models.ManagedCertificate
	if err := r.db.Where("id = ?", id).First(&c).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *ManagedCertificateRepository) Update(c *models.ManagedCertificate) error {
	return r.db.Save(c).Error
}

func (r *ManagedCertificateRepository) Delete(id uuid.UUID) error {
	return r.db.Delete(&models.ManagedCertificate{}, "id = ?", id).Error
}

func (r *ManagedCertificateRepository) CountByIssuer(issuerID uuid.UUID) (int64, error) {
	var n int64
	err := r.db.Model(&models.ManagedCertificate{}).Where("issuer_id = ?", issuerID).Count(&n).Error
	return n, err
}

func (r *ManagedCertificateRepository) CountByIssuerAndProject(issuerID, projectID uuid.UUID) (int64, error) {
	var n int64
	err := r.db.Model(&models.ManagedCertificate{}).
		Where("issuer_id = ? AND project_id = ?", issuerID, projectID).Count(&n).Error
	return n, err
}

// ListByProjectFiltered lists certificates within a single project, applying
// the optional filters in f on top of the mandatory project scope.
func (r *ManagedCertificateRepository) ListByProjectFiltered(projectID uuid.UUID, page, limit int, f CertificateListFilter) ([]models.ManagedCertificate, int64, error) {
	var out []models.ManagedCertificate
	var total int64

	query := r.db.Model(&models.ManagedCertificate{}).Where("project_id = ?", projectID)
	query = applyCertificateListFilter(query, f)

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * limit
	if err := query.Order("created_at DESC").Offset(offset).Limit(limit).Find(&out).Error; err != nil {
		return nil, 0, err
	}

	return out, total, nil
}

// ListFleet returns certificates across ALL projects (unless f.ProjectID
// narrows it to one), applying the optional filters in f. Like
// ListByStatuses, this is intentionally cross-project.
func (r *ManagedCertificateRepository) ListFleet(page, limit int, f CertificateListFilter) ([]models.ManagedCertificate, int64, error) {
	var out []models.ManagedCertificate
	var total int64

	query := r.db.Model(&models.ManagedCertificate{})
	if f.ProjectID != nil {
		query = query.Where("project_id = ?", *f.ProjectID)
	}
	query = applyCertificateListFilter(query, f)

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * limit
	if err := query.Order("created_at DESC").Offset(offset).Limit(limit).Find(&out).Error; err != nil {
		return nil, 0, err
	}

	return out, total, nil
}

// applyCertificateListFilter chains the optional status/issuer/usage/expiry
// filters in f onto query. It does not touch project scoping; callers apply
// that separately (mandatory for ListByProjectFiltered, optional for
// ListFleet via f.ProjectID).
func applyCertificateListFilter(query *gorm.DB, f CertificateListFilter) *gorm.DB {
	if f.Status != "" {
		query = query.Where("status = ?", f.Status)
	}
	if f.IssuerID != nil {
		query = query.Where("issuer_id = ?", *f.IssuerID)
	}
	if f.Usage != "" {
		query = query.Where("usage = ?", f.Usage)
	}
	if f.ExpiresBefore != nil {
		query = query.Where("not_after <= ?", *f.ExpiresBefore)
	}
	return query
}

// ListByStatuses returns every ManagedCertificate whose status is one of the
// given statuses, across ALL projects. This is intentionally cross-project
// (unlike the project-scoped list methods): the distribution controller reconciles every
// pending/issuing/ready certificate cluster-wide on each pass, not one
// project's certificates at a time.
func (r *ManagedCertificateRepository) ListByStatuses(statuses []models.ManagedCertStatus) ([]models.ManagedCertificate, error) {
	var out []models.ManagedCertificate
	if err := r.db.Where("status IN ?", statuses).Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}
