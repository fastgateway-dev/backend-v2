package services

import (
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
)

// IssuerGrantService manages which projects a platform-global
// CertificateIssuer is visible to: an issuer is invisible to a project until
// a grant row exists for (issuerID, projectID). Phase 2's ManagedCertificate
// create flow will consult ListGrants (or ListProjectIDsForIssuer) to decide
// which issuers a project may select.
type IssuerGrantService struct {
	grantRepo       repository.IssuerProjectGrantRepositoryInterface
	issuerRepo      repository.CertificateIssuerRepositoryInterface
	projectRepo     repository.ProjectRepositoryInterface
	managedCertRepo repository.ManagedCertificateRepositoryInterface
}

// IssuerGrantServiceDeps are IssuerGrantService's required dependencies.
// NewIssuerGrantService panics if any of them is nil, following the house
// pattern for services with required constructor dependencies.
type IssuerGrantServiceDeps struct {
	GrantRepo       repository.IssuerProjectGrantRepositoryInterface
	IssuerRepo      repository.CertificateIssuerRepositoryInterface
	ProjectRepo     repository.ProjectRepositoryInterface
	ManagedCertRepo repository.ManagedCertificateRepositoryInterface
}

func NewIssuerGrantService(deps IssuerGrantServiceDeps) *IssuerGrantService {
	var missing []string
	if deps.GrantRepo == nil {
		missing = append(missing, "GrantRepo")
	}
	if deps.IssuerRepo == nil {
		missing = append(missing, "IssuerRepo")
	}
	if deps.ProjectRepo == nil {
		missing = append(missing, "ProjectRepo")
	}
	if deps.ManagedCertRepo == nil {
		missing = append(missing, "ManagedCertRepo")
	}
	if len(missing) > 0 {
		panic("services.NewIssuerGrantService: missing required dependency: " + strings.Join(missing, ", "))
	}
	return &IssuerGrantService{grantRepo: deps.GrantRepo, issuerRepo: deps.IssuerRepo, projectRepo: deps.ProjectRepo, managedCertRepo: deps.ManagedCertRepo}
}

// Grant makes issuerID visible to projectID. It validates both the issuer and
// the project exist, then dedupes via Exists before Create so granting an
// already-granted pair is a harmless no-op rather than a unique-constraint
// error bubbling up from the database.
func (s *IssuerGrantService) Grant(issuerID, projectID, by uuid.UUID) error {
	if _, err := s.issuerRepo.GetByID(issuerID); err != nil {
		return err
	}
	if _, err := s.projectRepo.GetByID(projectID); err != nil {
		return err
	}
	exists, err := s.grantRepo.Exists(issuerID, projectID)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	grant := &models.IssuerProjectGrant{IssuerID: issuerID, ProjectID: projectID, CreatedBy: by}
	return s.grantRepo.Create(grant)
}

// ListGrants returns every project the issuer has been granted to.
func (s *IssuerGrantService) ListGrants(issuerID uuid.UUID) ([]models.IssuerProjectGrant, error) {
	return s.grantRepo.ListByIssuer(issuerID)
}

// Revoke removes the grant of issuerID from projectID, first checking that
// no ManagedCertificate in projectID still uses issuerID -- revoking would
// otherwise leave that certificate pointing at an issuer its project can no
// longer see.
func (s *IssuerGrantService) Revoke(issuerID, projectID uuid.UUID) error {
	n, err := s.managedCertRepo.CountByIssuerAndProject(issuerID, projectID)
	if err != nil {
		return err
	}
	if n > 0 {
		return errors.New("issuer is in use by a managed certificate in this project")
	}
	return s.grantRepo.Delete(issuerID, projectID)
}
