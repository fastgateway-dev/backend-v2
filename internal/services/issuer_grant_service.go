package services

import (
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
	grantRepo   repository.IssuerProjectGrantRepositoryInterface
	issuerRepo  repository.CertificateIssuerRepositoryInterface
	projectRepo repository.ProjectRepositoryInterface
}

// IssuerGrantServiceDeps are IssuerGrantService's required dependencies.
// NewIssuerGrantService panics if any of them is nil, following the house
// pattern for services with required constructor dependencies.
type IssuerGrantServiceDeps struct {
	GrantRepo   repository.IssuerProjectGrantRepositoryInterface
	IssuerRepo  repository.CertificateIssuerRepositoryInterface
	ProjectRepo repository.ProjectRepositoryInterface
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
	if len(missing) > 0 {
		panic("services.NewIssuerGrantService: missing required dependency: " + strings.Join(missing, ", "))
	}
	return &IssuerGrantService{grantRepo: deps.GrantRepo, issuerRepo: deps.IssuerRepo, projectRepo: deps.ProjectRepo}
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

// Revoke removes the grant of issuerID from projectID.
//
// Phase 2: block revoke if a ManagedCertificate in projectID still uses
// issuerID (409) -- deferred until the ManagedCertificate repo exists.
func (s *IssuerGrantService) Revoke(issuerID, projectID uuid.UUID) error {
	return s.grantRepo.Delete(issuerID, projectID)
}
