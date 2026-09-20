package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	approvalpkg "github.com/fastgateway-dev/backend-v2/internal/approval"
	"github.com/fastgateway-dev/backend-v2/internal/config"
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
)

// ErrCertificateInUse is returned by Delete when the certificate is still
// attached to one or more domains. The handler maps this to 409 Conflict --
// deleting the row out from under a domain that references it would leave
// the domain pointing at a certificate that no longer exists in-cluster.
var ErrCertificateInUse = errors.New("certificate is attached to one or more domains")

// TenantSecretDeleter is the narrow tenant-cluster role
// ManagedCertificateService needs to clean up the pushed TLS Secret when a
// certificate is deleted. Satisfied by *cluster.Client (the same client
// passed elsewhere as certdist's TenantWriter).
type TenantSecretDeleter interface {
	DeleteSecret(ctx context.Context, projectID uuid.UUID, namespace, name string) error
}

// CertApprovalSubmitter is the narrow slice of *approvalpkg.Engine that
// ManagedCertificateService needs to submit a certificate create for
// approval. A port rather than a direct *approvalpkg.Engine dependency, so
// tests can fake it without standing up the engine's own five dependencies.
type CertApprovalSubmitter interface {
	Submit(spec approvalpkg.Spec) (*models.Approval, error)
}

var _ CertApprovalSubmitter = (*approvalpkg.Engine)(nil)

// ManagedCertificateService owns the lifecycle of project-scoped managed
// certificates: creating a certificate (subject to project approval),
// issuing the leaf cert-manager Certificate once approved, and reporting its
// live cert-manager status. Unlike RouteService, there is no separate
// deploy step in Phase 2 -- OnApproved issues the leaf Certificate directly;
// distributing the resulting Secret to workload clusters is Phase 3.
type ManagedCertificateService struct {
	repo          repository.ManagedCertificateRepositoryInterface
	issuerRepo    repository.CertificateIssuerRepositoryInterface
	grantRepo     repository.IssuerProjectGrantRepositoryInterface
	projectRepo   repository.ProjectRepositoryInterface
	controlPlane  CertInfraApplier
	approvals     CertApprovalSubmitter
	config        *config.Config
	distRepo      repository.CertificateDistributionRepositoryInterface
	domainRepo    repository.DomainRepositoryInterface
	tenantSecrets TenantSecretDeleter
}

// ManagedCertificateServiceDeps are ManagedCertificateService's required
// dependencies. NewManagedCertificateService panics if any of them is nil,
// following the house pattern (see CertificateIssuerServiceDeps).
type ManagedCertificateServiceDeps struct {
	Repo          repository.ManagedCertificateRepositoryInterface
	IssuerRepo    repository.CertificateIssuerRepositoryInterface
	GrantRepo     repository.IssuerProjectGrantRepositoryInterface
	ProjectRepo   repository.ProjectRepositoryInterface
	ControlPlane  CertInfraApplier
	Approvals     CertApprovalSubmitter
	Config        *config.Config
	DistRepo      repository.CertificateDistributionRepositoryInterface
	DomainRepo    repository.DomainRepositoryInterface // referential guard (ListByManagedCertificateID)
	TenantSecrets TenantSecretDeleter                  // tenant-cluster secret cleanup
}

func NewManagedCertificateService(deps ManagedCertificateServiceDeps) *ManagedCertificateService {
	var missing []string
	if deps.Repo == nil {
		missing = append(missing, "Repo")
	}
	if deps.IssuerRepo == nil {
		missing = append(missing, "IssuerRepo")
	}
	if deps.GrantRepo == nil {
		missing = append(missing, "GrantRepo")
	}
	if deps.ProjectRepo == nil {
		missing = append(missing, "ProjectRepo")
	}
	if deps.ControlPlane == nil {
		missing = append(missing, "ControlPlane")
	}
	if deps.Approvals == nil {
		missing = append(missing, "Approvals")
	}
	if deps.Config == nil {
		missing = append(missing, "Config")
	}
	if deps.DistRepo == nil {
		missing = append(missing, "DistRepo")
	}
	if deps.DomainRepo == nil {
		missing = append(missing, "DomainRepo")
	}
	if deps.TenantSecrets == nil {
		missing = append(missing, "TenantSecrets")
	}
	if len(missing) > 0 {
		panic("services.NewManagedCertificateService: missing required dependency: " + strings.Join(missing, ", "))
	}
	return &ManagedCertificateService{
		repo:          deps.Repo,
		issuerRepo:    deps.IssuerRepo,
		grantRepo:     deps.GrantRepo,
		projectRepo:   deps.ProjectRepo,
		controlPlane:  deps.ControlPlane,
		approvals:     deps.Approvals,
		config:        deps.Config,
		distRepo:      deps.DistRepo,
		domainRepo:    deps.DomainRepo,
		tenantSecrets: deps.TenantSecrets,
	}
}

// Compile-time check: ManagedCertificateService is the certificate entity's
// approval.Completer.
var _ approvalpkg.Completer = (*ManagedCertificateService)(nil)

// CreateCertificateInput is the request body for creating a managed
// certificate. DNSNames applies to server-usage certificates; Subject
// applies to client-usage certificates.
type CreateCertificateInput struct {
	Name         string                  `json:"name" binding:"required"`
	IssuerID     uuid.UUID               `json:"issuerId" binding:"required"`
	Usage        models.ManagedCertUsage `json:"usage" binding:"required"`
	DNSNames     []string                `json:"dnsNames"`
	Subject      string                  `json:"subject"`
	KeyAlgorithm string                  `json:"keyAlgorithm"`
	KeySize      int                     `json:"keySize"`
	DurationDays int                     `json:"durationDays"`
}

// CertStatus is the live cert-manager status of a managed certificate, as
// reported by Status.
type CertStatus struct {
	Status   models.ManagedCertStatus `json:"status"`
	Message  string                   `json:"message,omitempty"`
	NotAfter *string                  `json:"notAfter,omitempty"`
}

// Create validates the request, checks that the issuer is granted to the
// project, and persists the certificate row (with its Config populated)
// before doing any cluster work -- so a failed or rejected create can still
// be cleaned up (Ruling P2-C). If the project has approvals disabled, the
// certificate is issued immediately (fast path); otherwise a create
// approval is submitted and the certificate stays pending until it resolves.
func (s *ManagedCertificateService) Create(projectID uuid.UUID, input *CreateCertificateInput, createdBy uuid.UUID) (*models.ManagedCertificate, *models.Approval, error) {
	if err := validateCreateCertificateInput(input); err != nil {
		return nil, nil, err
	}

	granted, err := s.grantRepo.Exists(input.IssuerID, projectID)
	if err != nil {
		return nil, nil, fmt.Errorf("check issuer grant: %w", err)
	}
	if !granted {
		return nil, nil, errors.New("issuer is not granted to this project")
	}

	issuer, err := s.issuerRepo.GetByID(input.IssuerID)
	if err != nil {
		return nil, nil, fmt.Errorf("load issuer: %w", err)
	}
	if issuer.Config.ClusterIssuerName == "" {
		return nil, nil, errors.New("issuer has no resolved cluster issuer")
	}

	if input.KeyAlgorithm == "" {
		input.KeyAlgorithm = "RSA"
	}
	if input.KeySize == 0 {
		input.KeySize = 2048
	}
	if input.DurationDays == 0 {
		input.DurationDays = 90
	}

	cert := &models.ManagedCertificate{
		ProjectID: projectID,
		Name:      input.Name,
		IssuerID:  input.IssuerID,
		Usage:     input.Usage,
		Status:    models.ManagedCertStatusPending,
		CreatedBy: createdBy,
	}
	if err := s.repo.Create(cert); err != nil {
		return nil, nil, fmt.Errorf("create certificate: %w", err)
	}

	// Persist Config (id-derived names) BEFORE any cluster work, per Ruling
	// P2-C: a failed or rejected create must still be able to find (and
	// clean up) whatever was actually created in-cluster.
	cert.Config = models.ManagedCertConfig{
		DNSNames:        input.DNSNames,
		Subject:         input.Subject,
		SecretName:      "cert-" + cert.ID.String(),
		CertificateName: "cert-" + cert.ID.String(),
		KeyAlgorithm:    input.KeyAlgorithm,
		KeySize:         input.KeySize,
		DurationDays:    input.DurationDays,
	}
	if err := s.repo.Update(cert); err != nil {
		return nil, nil, fmt.Errorf("persist certificate config: %w", err)
	}

	project, err := s.projectRepo.GetByID(projectID)
	if err != nil {
		return nil, nil, fmt.Errorf("load project: %w", err)
	}

	if !project.ApprovalEnabled {
		// Fast path: no approval gate configured for this project, issue
		// immediately.
		if err := s.OnApproved(&models.Approval{
			ProjectID:   projectID,
			EntityType:  models.ApprovalEntityCertificate,
			EntityID:    cert.ID,
			Action:      models.ApprovalActionCreate,
			SubmittedBy: createdBy,
		}); err != nil {
			return nil, nil, err
		}
		cert, err = s.repo.GetByID(cert.ID)
		if err != nil {
			return nil, nil, err
		}
		return cert, nil, nil
	}

	snapshot, err := json.Marshal(input)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal config snapshot: %w", err)
	}
	approval, err := s.approvals.Submit(approvalpkg.Spec{
		ProjectID:      projectID,
		EntityType:     models.ApprovalEntityCertificate,
		EntityID:       cert.ID,
		Action:         models.ApprovalActionCreate,
		SubmittedBy:    createdBy,
		ConfigSnapshot: snapshot,
	})
	if err != nil {
		return nil, nil, err
	}

	return cert, approval, nil
}

// validateCreateCertificateInput enforces the per-usage requirements: a
// server certificate needs at least one DNS name, a client certificate
// needs a subject.
func validateCreateCertificateInput(input *CreateCertificateInput) error {
	if input.IssuerID == uuid.Nil {
		return errors.New("issuerId is required")
	}
	switch input.Usage {
	case models.ManagedCertUsageServer:
		if len(input.DNSNames) == 0 {
			return errors.New("at least one DNS name is required for a server certificate")
		}
	case models.ManagedCertUsageClient:
		if input.Subject == "" {
			return errors.New("subject is required for a client certificate")
		}
	default:
		return fmt.Errorf("unsupported certificate usage: %q", input.Usage)
	}
	return nil
}

// OnApproved issues the leaf cert-manager Certificate for a
// create-certificate approval. Certificates have no separate deploy step in
// Phase 2 (Ruling P2-B): this is called both from the fast path in Create
// (project approvals disabled) and from the approval engine once every
// stage of a submitted approval is approved.
func (s *ManagedCertificateService) OnApproved(a *models.Approval) error {
	cert, err := s.repo.GetByID(a.EntityID)
	if err != nil {
		return err
	}

	issuer, err := s.issuerRepo.GetByID(cert.IssuerID)
	if err != nil {
		return err
	}

	ctx := context.Background()
	obj := kubernetes.LeafCertificate(kubernetes.LeafCertConfig{
		Name:                    cert.Config.CertificateName,
		Namespace:               s.controlPlane.Namespace(),
		SecretName:              cert.Config.SecretName,
		IssuerClusterIssuerName: issuer.Config.ClusterIssuerName,
		DNSNames:                cert.Config.DNSNames,
		CommonName:              cert.Config.Subject,
		KeyAlgorithm:            cert.Config.KeyAlgorithm,
		KeySize:                 cert.Config.KeySize,
		DurationDays:            cert.Config.DurationDays,
	})

	if err := s.controlPlane.ApplyNamespaced(ctx, kubernetes.CertManagerCertificateGVR, obj); err != nil {
		cert.Status = models.ManagedCertStatusError
		cert.StatusMessage = err.Error()
		_ = s.repo.Update(cert)
		return err
	}

	cert.Status = models.ManagedCertStatusIssuing
	cert.StatusMessage = ""
	return s.repo.Update(cert)
}

// OnRejected reverts a rejected certificate. Mirroring
// routeWrite.OnRejected's create case, a rejected create never reached the
// cluster, so the certificate is marked as errored rather than deployed.
// There is only one action a certificate approval can carry in Phase 2
// (create): update/delete approvals for managed certificates are not part
// of this phase.
func (s *ManagedCertificateService) OnRejected(a *models.Approval) error {
	cert, err := s.repo.GetByID(a.EntityID)
	if err != nil {
		return err
	}
	switch a.Action {
	case models.ApprovalActionCreate:
		cert.Status = models.ManagedCertStatusError
		cert.StatusMessage = "certificate creation was rejected"
		return s.repo.Update(cert)
	default:
		return fmt.Errorf("certificate approval: unsupported action %q", a.Action)
	}
}

// OnCancelled reverts a cancelled certificate. Mirroring
// routeWrite.OnCancelled's create case: the certificate was never issued,
// so a cancelled create deletes the row outright.
func (s *ManagedCertificateService) OnCancelled(a *models.Approval) error {
	switch a.Action {
	case models.ApprovalActionCreate:
		return s.repo.Delete(a.EntityID)
	default:
		return fmt.Errorf("certificate approval: unsupported action %q", a.Action)
	}
}

func (s *ManagedCertificateService) GetByID(id uuid.UUID) (*models.ManagedCertificate, error) {
	return s.repo.GetByID(id)
}

func (s *ManagedCertificateService) ListByProject(projectID uuid.UUID, page, limit int, status string) ([]models.ManagedCertificate, int64, error) {
	return s.repo.ListByProject(projectID, page, limit, status)
}

// ListProjectCertificatesEnriched lists a project's certificates with the
// optional filters in f applied, joined with each certificate's distribution
// sync state, referencing domains, and issuer name/type (Task 3's
// EnrichedCertificate). f.ProjectID is ignored -- the project scope comes
// from projectID, not the filter.
func (s *ManagedCertificateService) ListProjectCertificatesEnriched(projectID uuid.UUID, page, limit int, f repository.CertificateListFilter) ([]EnrichedCertificate, int64, error) {
	certs, total, err := s.repo.ListByProjectFiltered(projectID, page, limit, f)
	if err != nil {
		return nil, 0, err
	}

	enriched, err := s.enrich(certs)
	if err != nil {
		return nil, 0, err
	}
	return enriched, total, nil
}

// ListFleetCertificates lists managed certificates across ALL projects
// (owner-only visibility), enriched the same way as
// ListProjectCertificatesEnriched -- resolved issuer name/type, distribution
// sync state, and referencing domains. Unlike the project-scoped method,
// f.ProjectID is honored here: a caller may narrow the fleet view to a
// single project via the filter, since there is no path-derived project
// scope to fall back on.
func (s *ManagedCertificateService) ListFleetCertificates(page, limit int, f repository.CertificateListFilter) ([]EnrichedCertificate, int64, error) {
	certs, total, err := s.repo.ListFleet(page, limit, f)
	if err != nil {
		return nil, 0, err
	}

	enriched, err := s.enrich(certs)
	if err != nil {
		return nil, 0, err
	}
	return enriched, total, nil
}

// enrich joins a page of certificates with their distribution rows,
// referencing domains, and issuers, fetching each relation in ONE batch call
// -- no per-cert N+1 lookups. Shared by ListProjectCertificatesEnriched and
// the fleet-wide visibility method.
func (s *ManagedCertificateService) enrich(certs []models.ManagedCertificate) ([]EnrichedCertificate, error) {
	certIDs := make([]uuid.UUID, len(certs))
	for i, c := range certs {
		certIDs[i] = c.ID
	}

	dists, err := s.distRepo.ListByCertificateIDs(certIDs)
	if err != nil {
		return nil, fmt.Errorf("list certificate distributions: %w", err)
	}

	domains, err := s.domainRepo.ListByManagedCertificateIDs(certIDs)
	if err != nil {
		return nil, fmt.Errorf("list referencing domains: %w", err)
	}

	issuers, err := s.issuerRepo.List()
	if err != nil {
		return nil, fmt.Errorf("list certificate issuers: %w", err)
	}

	return buildEnrichedCertificates(certs, dists, domains, issuers), nil
}

// Delete removes a managed certificate. It refuses to delete a certificate
// that is still attached to one or more domains (ErrCertificateInUse, mapped
// by the handler to 409) -- checked BEFORE any deletion happens, so a
// referenced certificate is left fully intact. Once the referential guard
// passes, the DB row is deleted (the certificate_distributions row cascades
// via its ON DELETE CASCADE FK -- see migration 000042, no explicit
// DistRepo delete needed), followed by best-effort cluster cleanup: the
// cert-manager leaf Certificate CRD and the pushed tenant TLS Secret.
// Cleanup failures are logged, not returned -- the row is already gone and
// the user's delete action succeeded.
func (s *ManagedCertificateService) Delete(id uuid.UUID) error {
	cert, err := s.repo.GetByID(id)
	if err != nil {
		return err
	}

	domains, err := s.domainRepo.ListByManagedCertificateID(id)
	if err != nil {
		return fmt.Errorf("check domain references: %w", err)
	}
	if len(domains) > 0 {
		return ErrCertificateInUse
	}

	if err := s.repo.Delete(id); err != nil {
		return err
	}

	ctx := context.Background()
	if err := s.controlPlane.Delete(ctx, kubernetes.CertManagerCertificateGVR, cert.Config.CertificateName, true); err != nil {
		log.Printf("managed certificate %s: failed to delete leaf Certificate CRD %q: %v", id, cert.Config.CertificateName, err)
	}
	if err := s.tenantSecrets.DeleteSecret(ctx, cert.ProjectID, kubernetes.FastGatewayNamespace, cert.Config.SecretName); err != nil {
		log.Printf("managed certificate %s: failed to delete tenant TLS secret %q: %v", id, cert.Config.SecretName, err)
	}

	return nil
}

// Status reads the live cert-manager Certificate for cert id and reports
// its Ready condition, updating the row's Status/StatusMessage/NotAfter to
// match. It does not touch Fingerprint -- see the comment on the Ready
// branch below.
func (s *ManagedCertificateService) Status(id uuid.UUID) (*CertStatus, error) {
	cert, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}

	obj, err := s.controlPlane.Get(context.Background(), kubernetes.CertManagerCertificateGVR, cert.Config.CertificateName, true)
	if err != nil {
		return nil, fmt.Errorf("get certificate: %w", err)
	}

	result := &CertStatus{Status: models.ManagedCertStatusIssuing}

	ready, message := readyCondition(obj)
	switch ready {
	case "True":
		result.Status = models.ManagedCertStatusReady
		cert.Status = models.ManagedCertStatusReady
		cert.StatusMessage = ""
		if notAfter, ok := nestedString(obj, "status", "notAfter"); ok {
			result.NotAfter = &notAfter
			// Only persist a successfully-parsed expiry -- a bad or
			// unparseable value from cert-manager must not clobber a
			// previously-stored good NotAfter on the row.
			if parsed, parseErr := time.Parse(time.RFC3339, notAfter); parseErr == nil {
				cert.NotAfter = &parsed
			}
		}
		// Fingerprint is deliberately NOT persisted here. cert-manager's
		// status.fingerprint is colon-hex, while the certdist distributor
		// (via SetIssuedMeta) writes bare-hex cluster.CertFingerprint on
		// every push -- storing both formats made the column flip-flop
		// between polls. The distributor is now the sole writer of
		// Fingerprint.
	case "False":
		result.Status = models.ManagedCertStatusError
		result.Message = message
		cert.Status = models.ManagedCertStatusError
		cert.StatusMessage = message
	default:
		result.Status = models.ManagedCertStatusIssuing
		result.Message = message
		cert.Status = models.ManagedCertStatusIssuing
		cert.StatusMessage = message
	}

	_ = s.repo.Update(cert)
	return result, nil
}

// DistributionStatus reads the Phase 3a distribution row for certID,
// reporting how far the certdist controller has gotten pushing this
// certificate's Secret into the project's tenant cluster. The caller (the
// handler) is responsible for the cross-project 404 check via GetByID
// before calling this -- a missing distribution row here just means the
// ticker hasn't pushed this certificate yet, which is reported as a
// pending placeholder rather than an error.
func (s *ManagedCertificateService) DistributionStatus(certID uuid.UUID) (*models.CertificateDistribution, error) {
	dist, err := s.distRepo.GetByCertificateID(certID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &models.CertificateDistribution{
				ManagedCertificateID: certID,
				Status:               models.CertDistStatusPending,
				Message:              "distribution not yet started",
			}, nil
		}
		return nil, err
	}
	return dist, nil
}

// Resync forces the certdist controller to re-push certID's Secret on its
// next tick. NOTIFY is not wired up (Task 5 shipped the ticker path only),
// so this works by flipping the distribution row's Status to pending --
// certdist's needsPush treats any non-synced status as due for a push.
// LastPushedFingerprint and LastSyncedAt are preserved from the existing
// row (rather than left zero) because Upsert is an UpdateAll-on-conflict
// write: a blind minimal Upsert would wipe them.
func (s *ManagedCertificateService) Resync(certID uuid.UUID) error {
	cert, err := s.repo.GetByID(certID)
	if err != nil {
		return err
	}

	existing, err := s.distRepo.GetByCertificateID(certID)
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		return s.distRepo.Upsert(&models.CertificateDistribution{
			ManagedCertificateID: certID,
			ProjectID:            cert.ProjectID,
			Status:               models.CertDistStatusPending,
		})
	}

	existing.Status = models.CertDistStatusPending
	return s.distRepo.Upsert(existing)
}

// IssuersForProject returns the certificate issuers granted to projectID.
//
// IssuerProjectGrantRepositoryInterface only looks grants up by issuer
// (ListByIssuer), not by project, so issuers granted to a project are found
// by scanning every platform-global issuer and checking Exists. This is
// Phase 2 scale (a handful of issuers), so an O(issuers) scan is an
// acceptable trade against adding a new repository method for a single
// call site.
func (s *ManagedCertificateService) IssuersForProject(projectID uuid.UUID) ([]models.CertificateIssuer, error) {
	issuers, err := s.issuerRepo.List()
	if err != nil {
		return nil, err
	}
	var out []models.CertificateIssuer
	for _, iss := range issuers {
		granted, err := s.grantRepo.Exists(iss.ID, projectID)
		if err != nil {
			return nil, err
		}
		if granted {
			out = append(out, iss)
		}
	}
	return out, nil
}

// readyCondition extracts the cert-manager Certificate's status.conditions
// entry with type=Ready, returning its status ("True"/"False"/"Unknown")
// and message. An absent conditions list or Ready entry reports
// ("Unknown", "") -- treated by Status as still issuing.
func readyCondition(obj *unstructured.Unstructured) (status, message string) {
	conditions, found, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if err != nil || !found {
		return "Unknown", ""
	}
	for _, c := range conditions {
		cond, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		if t, _, _ := unstructured.NestedString(cond, "type"); t != "Ready" {
			continue
		}
		s, _, _ := unstructured.NestedString(cond, "status")
		m, _, _ := unstructured.NestedString(cond, "message")
		if s == "" {
			s = "Unknown"
		}
		return s, m
	}
	return "Unknown", ""
}

// nestedString reads a string field from obj at the given path, reporting
// whether it was present and non-empty.
func nestedString(obj *unstructured.Unstructured, fields ...string) (string, bool) {
	v, found, err := unstructured.NestedString(obj.Object, fields...)
	if err != nil || !found || v == "" {
		return "", false
	}
	return v, true
}
