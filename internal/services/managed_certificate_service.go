package services

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
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
// attached to one or more domains, or to a client (Task 10 -- a Client's
// ManagedCertificateID is a 1:1 FK with ON DELETE RESTRICT, so this guard is
// a friendlier 409 in front of that DB backstop). The handler maps this to
// 409 Conflict -- deleting the row out from under a domain or client that
// references it would leave that reference pointing at a certificate that
// no longer exists in-cluster.
var ErrCertificateInUse = errors.New("certificate is attached to one or more domains")

// ErrCSRRequired is returned by Create when input.KeyMode is csr but no CSR
// PEM was supplied. In csr mode the caller holds the private key and only
// asks cert-manager to sign a CertificateRequest, so a CSR is mandatory.
var ErrCSRRequired = errors.New("a CSR (PEM) is required when keyMode is csr")

// ErrExportNotApplicable is returned by RequestExport for a csr-key-mode
// certificate. In csr mode the caller already supplied the CSR and holds
// the private key itself -- cert-manager never sees it, so there is no
// server-held key material to export.
var ErrExportNotApplicable = errors.New("export is only available for managed-key certificates")

// ErrCertificateNotReadyForExport is returned by RequestExport when the
// certificate hasn't finished issuing yet (Status != Ready). Minting an
// export grant before then would let the holder immediately call
// ExportBundle against a leaf Secret that doesn't exist in-cluster yet,
// which 500s on download instead of failing fast at request time.
var ErrCertificateNotReadyForExport = errors.New("certificate is not ready for export")

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
	clientRepo    repository.ClientRepositoryInterface
	tenantSecrets TenantSecretDeleter
	exportGrants  repository.CertificateExportGrantRepositoryInterface
}

// ManagedCertificateServiceDeps are ManagedCertificateService's required
// dependencies. NewManagedCertificateService panics if any of them is nil,
// following the house pattern (see CertificateIssuerServiceDeps).
type ManagedCertificateServiceDeps struct {
	Repo            repository.ManagedCertificateRepositoryInterface
	IssuerRepo      repository.CertificateIssuerRepositoryInterface
	GrantRepo       repository.IssuerProjectGrantRepositoryInterface
	ProjectRepo     repository.ProjectRepositoryInterface
	ControlPlane    CertInfraApplier
	Approvals       CertApprovalSubmitter
	Config          *config.Config
	DistRepo        repository.CertificateDistributionRepositoryInterface
	DomainRepo      repository.DomainRepositoryInterface                 // referential guard (ListByManagedCertificateID)
	ClientRepo      repository.ClientRepositoryInterface                 // referential guard (GetByManagedCertificateID)
	TenantSecrets   TenantSecretDeleter                                  // tenant-cluster secret cleanup
	ExportGrantRepo repository.CertificateExportGrantRepositoryInterface // issues one-time export grants (ApprovalActionExport)
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
	if deps.ClientRepo == nil {
		missing = append(missing, "ClientRepo")
	}
	if deps.TenantSecrets == nil {
		missing = append(missing, "TenantSecrets")
	}
	if deps.ExportGrantRepo == nil {
		missing = append(missing, "ExportGrantRepo")
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
		clientRepo:    deps.ClientRepo,
		tenantSecrets: deps.TenantSecrets,
		exportGrants:  deps.ExportGrantRepo,
	}
}

// Compile-time check: ManagedCertificateService is the certificate entity's
// approval.Completer.
var _ approvalpkg.Completer = (*ManagedCertificateService)(nil)

// CreateCertificateInput is the request body for creating a managed
// certificate. DNSNames applies to server-usage certificates; Subject
// applies to client-usage certificates.
type CreateCertificateInput struct {
	Name         string                    `json:"name" binding:"required"`
	IssuerID     uuid.UUID                 `json:"issuerId" binding:"required"`
	Usage        models.ManagedCertUsage   `json:"usage" binding:"required"`
	DNSNames     []string                  `json:"dnsNames"`
	Subject      string                    `json:"subject"`
	KeyAlgorithm string                    `json:"keyAlgorithm"`
	KeySize      int                       `json:"keySize"`
	DurationDays int                       `json:"durationDays"`
	KeyMode      models.ManagedCertKeyMode `json:"keyMode"`
	URISANs      []string                  `json:"uriSans"`
	// CSR is the caller-supplied PEM-encoded certificate signing request,
	// required when KeyMode is csr (the caller holds the private key and
	// only asks cert-manager to sign it). Ignored for managed key mode.
	CSR string `json:"csr"`
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

	// keyMode defaults to managed (cert-manager generates and holds the
	// leaf private key). csr mode lets the caller keep the private key
	// server-side-free and only asks cert-manager to sign a CSR.
	keyMode := input.KeyMode
	if keyMode == "" {
		keyMode = models.ManagedCertKeyModeManaged
	}
	if err := ValidateCertificateKind(input.Usage, keyMode, issuer.Type); err != nil {
		return nil, nil, err
	}

	if keyMode == models.ManagedCertKeyModeCSR {
		if input.CSR == "" {
			return nil, nil, ErrCSRRequired
		}
		if err := validateCSRPEM(input.CSR); err != nil {
			return nil, nil, fmt.Errorf("invalid CSR: %w", err)
		}
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
		KeyMode:         keyMode,
		URISANs:         input.URISANs,
	}
	if keyMode == models.ManagedCertKeyModeCSR {
		// Persisted in Config so OnApproved (a fresh row load) can read it
		// back to build the CertificateRequest -- Config is a jsonb DB
		// column (see ManagedCertConfig.Value()), and CSRPEM's
		// json:"csrPem,omitempty" tag (internal/models/managed_certificate.go)
		// is what makes it round-trip through that marshal. Do NOT change
		// that tag to json:"-": this is a public CSR (no private key
		// material), and it is never leaked via the API anyway because
		// managedCertificateResponse excludes Config entirely from
		// responses -- json:"-" here would only silently drop the CSR from
		// persistence and break every CSR-mode certificate (a bug that was
		// already fixed once).
		cert.Config.CSRPEM = input.CSR
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

// RequestExport opens an export approval for certID: a short-lived,
// single-use grant that lets requestedBy download the certificate's private
// key material exactly once. csr-mode certificates return
// ErrExportNotApplicable -- the caller already holds the private key (only
// the CSR was ever handed to cert-manager), so there is nothing server-side
// to export. A certificate that hasn't finished issuing yet (Status !=
// Ready) returns ErrCertificateNotReadyForExport -- its leaf Secret doesn't
// exist in-cluster yet, so a grant minted now would only fail later at
// download time. Mirrors Create's fast-path/approval-gated split: if the
// project has approvals disabled, OnApproved runs immediately (minting the
// grant synchronously) rather than going through the approval engine, and
// this returns a nil approval -- exactly like Create's fast path returns a
// nil *models.Approval.
func (s *ManagedCertificateService) RequestExport(certID, requestedBy uuid.UUID) (*models.Approval, error) {
	cert, err := s.repo.GetByID(certID)
	if err != nil {
		return nil, err
	}

	if cert.Config.KeyMode == models.ManagedCertKeyModeCSR {
		return nil, ErrExportNotApplicable
	}

	if cert.Status != models.ManagedCertStatusReady {
		return nil, ErrCertificateNotReadyForExport
	}

	project, err := s.projectRepo.GetByID(cert.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("load project: %w", err)
	}

	if !project.ApprovalEnabled {
		// Fast path: no approval gate configured for this project, mint the
		// export grant immediately via the same Completer branch the
		// approval engine would otherwise invoke once approved.
		if err := s.OnApproved(&models.Approval{
			ProjectID:   cert.ProjectID,
			EntityType:  models.ApprovalEntityCertificate,
			EntityID:    cert.ID,
			Action:      models.ApprovalActionExport,
			SubmittedBy: requestedBy,
		}); err != nil {
			return nil, err
		}
		return nil, nil
	}

	return s.approvals.Submit(approvalpkg.Spec{
		ProjectID:   cert.ProjectID,
		EntityType:  models.ApprovalEntityCertificate,
		EntityID:    cert.ID,
		Action:      models.ApprovalActionExport,
		SubmittedBy: requestedBy,
	})
}

// ExportBundle consumes userID's export grant for certID (single-use --
// ConsumeForCert marks it consumed atomically before anything else happens,
// so a reused request never reads the Secret at all) and returns the leaf
// certificate, its private key, and the issuer's CA chain, all PEM-encoded.
// Never logs the returned bytes: keyPEM is private key material.
func (s *ManagedCertificateService) ExportBundle(certID, userID uuid.UUID) (leafPEM, keyPEM, caChainPEM []byte, err error) {
	if _, err := s.exportGrants.ConsumeForCert(certID, userID); err != nil {
		return nil, nil, nil, err
	}

	cert, err := s.repo.GetByID(certID)
	if err != nil {
		return nil, nil, nil, err
	}

	issuer, err := s.issuerRepo.GetByID(cert.IssuerID)
	if err != nil {
		return nil, nil, nil, err
	}

	ctx := context.Background()

	leafPEM, keyPEM, err = s.readTLSSecret(ctx, cert.Config.SecretName)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read leaf secret: %w", err)
	}

	caChainPEM, err = s.readCASecret(ctx, issuer.Config.CASecretName)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read issuer CA secret: %w", err)
	}

	return leafPEM, keyPEM, caChainPEM, nil
}

// readTLSSecret reads and base64-decodes the tls.crt/tls.key data of the
// named core Secret in the control-plane cluster. Mirrors
// certdist.ControlPlaneSourceReader.ReadLeafSecret's decode pattern
// (internal/certdist/adapters.go) exactly -- same GVR, same
// unstructured.NestedStringMap extraction, same base64 decode -- but unlike
// that method (which treats a missing Secret as "still issuing, try again
// later"), any failure here is a hard error: ExportBundle is only ever
// called after a grant confirms the certificate has already issued.
func (s *ManagedCertificateService) readTLSSecret(ctx context.Context, name string) (crt, key []byte, err error) {
	obj, err := s.controlPlane.Get(ctx, kubernetes.CoreSecretGVR, name, true)
	if err != nil {
		return nil, nil, fmt.Errorf("get secret %q: %w", name, err)
	}

	data, ok, err := unstructured.NestedStringMap(obj.Object, "data")
	if err != nil || !ok {
		return nil, nil, fmt.Errorf("secret %q has no data", name)
	}

	crtEnc, ok := data["tls.crt"]
	if !ok {
		return nil, nil, fmt.Errorf("secret %q missing tls.crt", name)
	}
	keyEnc, ok := data["tls.key"]
	if !ok {
		return nil, nil, fmt.Errorf("secret %q missing tls.key", name)
	}

	crt, err = base64.StdEncoding.DecodeString(crtEnc)
	if err != nil {
		return nil, nil, fmt.Errorf("decoding tls.crt from secret %q: %w", name, err)
	}
	key, err = base64.StdEncoding.DecodeString(keyEnc)
	if err != nil {
		return nil, nil, fmt.Errorf("decoding tls.key from secret %q: %w", name, err)
	}

	return crt, key, nil
}

// readCASecret reads and base64-decodes an issuer CA Secret's ca.crt,
// falling back to tls.crt when ca.crt is absent -- a self-signed-CA
// issuer's CA Secret is itself a plain tls.crt/tls.key pair (see
// certmanager.go's self-signed-CA setup), with no separate ca.crt key.
func (s *ManagedCertificateService) readCASecret(ctx context.Context, name string) ([]byte, error) {
	obj, err := s.controlPlane.Get(ctx, kubernetes.CoreSecretGVR, name, true)
	if err != nil {
		return nil, fmt.Errorf("get secret %q: %w", name, err)
	}

	data, ok, err := unstructured.NestedStringMap(obj.Object, "data")
	if err != nil || !ok {
		return nil, fmt.Errorf("secret %q has no data", name)
	}

	enc, ok := data["ca.crt"]
	if !ok {
		enc, ok = data["tls.crt"]
		if !ok {
			return nil, fmt.Errorf("secret %q missing both ca.crt and tls.crt", name)
		}
	}

	crt, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return nil, fmt.Errorf("decoding CA certificate from secret %q: %w", name, err)
	}
	return crt, nil
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

// validateCSRPEM checks that csrPEM decodes as a PEM block and parses as a
// well-formed PKCS#10 certificate signing request. Used by Create for
// keyMode=csr, where the caller supplies the CSR and cert-manager only
// signs it.
func validateCSRPEM(csrPEM string) error {
	block, _ := pem.Decode([]byte(csrPEM))
	if block == nil {
		return errors.New("csr is not a valid PEM block")
	}
	if _, err := x509.ParseCertificateRequest(block.Bytes); err != nil {
		return fmt.Errorf("parse certificate request: %w", err)
	}
	return nil
}

// OnApproved issues the leaf cert-manager Certificate for a
// create-certificate approval. Certificates have no separate deploy step in
// Phase 2 (Ruling P2-B): this is called both from the fast path in Create
// (project approvals disabled) and from the approval engine once every
// stage of a submitted approval is approved.
func (s *ManagedCertificateService) OnApproved(a *models.Approval) error {
	if a.Action == models.ApprovalActionExport {
		// Export approvals never touch the certificate row or the cluster --
		// they just grant the requesting user a short-lived, single-use
		// export permission. a.SubmittedBy is the requesting user.
		grant := &models.CertificateExportGrant{
			ManagedCertificateID: a.EntityID,
			GrantedTo:            a.SubmittedBy,
			ExpiresAt:            time.Now().Add(15 * time.Minute),
		}
		return s.exportGrants.Create(grant)
	}

	cert, err := s.repo.GetByID(a.EntityID)
	if err != nil {
		return err
	}

	issuer, err := s.issuerRepo.GetByID(cert.IssuerID)
	if err != nil {
		return err
	}

	ctx := context.Background()

	if cert.Config.KeyMode == models.ManagedCertKeyModeCSR {
		// csr mode: the caller already holds the private key and CSR --
		// only ask cert-manager to sign it via a CertificateRequest. There
		// is no leaf key Secret in this mode (no SecretName is used).
		obj := kubernetes.CertificateRequestObject(kubernetes.CertificateRequestConfig{
			Name:                    cert.Config.CertificateName,
			Namespace:               s.controlPlane.Namespace(),
			IssuerClusterIssuerName: issuer.Config.ClusterIssuerName,
			Request:                 []byte(cert.Config.CSRPEM),
			DurationDays:            cert.Config.DurationDays,
		})
		if err := s.controlPlane.ApplyNamespaced(ctx, kubernetes.CertManagerCertificateRequestGVR, obj); err != nil {
			cert.Status = models.ManagedCertStatusError
			cert.StatusMessage = err.Error()
			_ = s.repo.Update(cert)
			return err
		}
	} else {
		// managed mode (default): cert-manager generates and holds the leaf
		// private key, pushed into SecretName.
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
			Usage:                   cert.Usage,
			URISANs:                 cert.Config.URISANs,
		})
		if err := s.controlPlane.ApplyNamespaced(ctx, kubernetes.CertManagerCertificateGVR, obj); err != nil {
			cert.Status = models.ManagedCertStatusError
			cert.StatusMessage = err.Error()
			_ = s.repo.Update(cert)
			return err
		}
	}

	cert.Status = models.ManagedCertStatusIssuing
	cert.StatusMessage = ""
	return s.repo.Update(cert)
}

// OnRejected reverts a rejected certificate. Mirroring
// routeWrite.OnRejected's create case, a rejected create never reached the
// cluster, so the certificate is marked as errored rather than deployed.
// An export approval mints nothing until OnApproved runs (see OnApproved's
// export branch), so there is nothing to undo here -- a rejected export
// request is a no-op that must NOT touch the certificate row (the approval
// engine persists Status=rejected before calling this, with no wrapping
// transaction, so any error here would strand the approval in a terminal
// state the caller can't retry out of).
func (s *ManagedCertificateService) OnRejected(a *models.Approval) error {
	switch a.Action {
	case models.ApprovalActionCreate:
		cert, err := s.repo.GetByID(a.EntityID)
		if err != nil {
			return err
		}
		cert.Status = models.ManagedCertStatusError
		cert.StatusMessage = "certificate creation was rejected"
		return s.repo.Update(cert)
	case models.ApprovalActionExport:
		return nil
	default:
		return fmt.Errorf("certificate approval: unsupported action %q", a.Action)
	}
}

// OnCancelled reverts a cancelled certificate. Mirroring
// routeWrite.OnCancelled's create case: the certificate was never issued,
// so a cancelled create deletes the row outright. As with OnRejected, a
// cancelled export approval is a no-op -- an export grant is only minted on
// approval, so cancelling before that point has nothing to undo.
func (s *ManagedCertificateService) OnCancelled(a *models.Approval) error {
	switch a.Action {
	case models.ApprovalActionCreate:
		return s.repo.Delete(a.EntityID)
	case models.ApprovalActionExport:
		return nil
	default:
		return fmt.Errorf("certificate approval: unsupported action %q", a.Action)
	}
}

func (s *ManagedCertificateService) GetByID(id uuid.UUID) (*models.ManagedCertificate, error) {
	return s.repo.GetByID(id)
}

// ListProjectCertificatesEnriched lists a project's certificates with the
// optional filters in f applied, joined with each certificate's distribution
// sync state, referencing domains, and issuer name/type (Task 3's
// EnrichedCertificate), plus whether viewerID has a usable export grant for
// each certificate (ExportAvailable). f.ProjectID is ignored -- the project
// scope comes from projectID, not the filter.
func (s *ManagedCertificateService) ListProjectCertificatesEnriched(projectID uuid.UUID, page, limit int, f repository.CertificateListFilter, viewerID uuid.UUID) ([]EnrichedCertificate, int64, error) {
	certs, total, err := s.repo.ListByProjectFiltered(projectID, page, limit, f)
	if err != nil {
		return nil, 0, err
	}

	enriched, err := s.enrich(certs, viewerID)
	if err != nil {
		return nil, 0, err
	}
	return enriched, total, nil
}

// ListFleetCertificates lists managed certificates across ALL projects
// (owner-only visibility), enriched the same way as
// ListProjectCertificatesEnriched -- resolved issuer name/type, distribution
// sync state, referencing domains, and viewerID's per-cert ExportAvailable.
// Unlike the project-scoped method, f.ProjectID is honored here: a caller
// may narrow the fleet view to a single project via the filter, since there
// is no path-derived project scope to fall back on.
func (s *ManagedCertificateService) ListFleetCertificates(page, limit int, f repository.CertificateListFilter, viewerID uuid.UUID) ([]EnrichedCertificate, int64, error) {
	certs, total, err := s.repo.ListFleet(page, limit, f)
	if err != nil {
		return nil, 0, err
	}

	enriched, err := s.enrich(certs, viewerID)
	if err != nil {
		return nil, 0, err
	}
	return enriched, total, nil
}

// enrich joins a page of certificates with their distribution rows,
// referencing domains, and issuers, fetching each relation in ONE batch call
// -- no per-cert N+1 lookups. Shared by ListProjectCertificatesEnriched and
// the fleet-wide visibility method. It also resolves ExportAvailable for
// viewerID via a single batch call to exportGrants.ListUsableGrantCertIDs,
// so a page of N certificates costs one extra query total, not N.
func (s *ManagedCertificateService) enrich(certs []models.ManagedCertificate, viewerID uuid.UUID) ([]EnrichedCertificate, error) {
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

	usableCertIDs, err := s.exportGrants.ListUsableGrantCertIDs(viewerID, certIDs)
	if err != nil {
		return nil, fmt.Errorf("list usable export grants: %w", err)
	}

	enriched := buildEnrichedCertificates(certs, dists, domains, issuers)

	usable := make(map[uuid.UUID]struct{}, len(usableCertIDs))
	for _, id := range usableCertIDs {
		usable[id] = struct{}{}
	}
	for i := range enriched {
		if _, ok := usable[enriched[i].Certificate.ID]; ok {
			enriched[i].ExportAvailable = true
		}
	}

	return enriched, nil
}

// HasUsableExportGrant reports whether userID has an approved, unconsumed,
// unexpired export grant for certID -- the single-certificate counterpart to
// enrich's batch ListUsableGrantCertIDs call, used by the Get handler for
// the one-certificate response.
func (s *ManagedCertificateService) HasUsableExportGrant(certID, userID uuid.UUID) (bool, error) {
	return s.exportGrants.HasUsableGrant(certID, userID)
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

	if _, err := s.clientRepo.GetByManagedCertificateID(id); err == nil {
		return ErrCertificateInUse
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
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

	if cert.Config.KeyMode == models.ManagedCertKeyModeCSR {
		return s.statusCSR(cert)
	}
	return s.statusManaged(cert)
}

// statusManaged reads the live cert-manager Certificate for a managed
// (cert-manager-held-key) certificate. This is the original Status body,
// unchanged aside from taking the already-loaded cert.
func (s *ManagedCertificateService) statusManaged(cert *models.ManagedCertificate) (*CertStatus, error) {
	obj, err := s.controlPlane.Get(context.Background(), kubernetes.CertManagerCertificateGVR, cert.Config.CertificateName, true)
	if err != nil {
		return nil, fmt.Errorf("get certificate: %w", err)
	}

	result := &CertStatus{Status: models.ManagedCertStatusIssuing}

	ready, reason, message := readyCondition(obj)
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
		if isCertManagerTerminalFailure(reason) {
			result.Status = models.ManagedCertStatusError
			result.Message = message
			cert.Status = models.ManagedCertStatusError
			cert.StatusMessage = message
		} else {
			// Ready=False with a non-terminal reason (Pending/Issuing) is the
			// normal in-progress state while cert-manager signs the leaf.
			result.Status = models.ManagedCertStatusIssuing
			result.Message = message
			cert.Status = models.ManagedCertStatusIssuing
			cert.StatusMessage = message
		}
	default:
		result.Status = models.ManagedCertStatusIssuing
		result.Message = message
		cert.Status = models.ManagedCertStatusIssuing
		cert.StatusMessage = message
	}

	_ = s.repo.Update(cert)
	return result, nil
}

// statusCSR reads the live cert-manager CertificateRequest for a csr-mode
// certificate. There is no leaf Certificate object in this mode (the
// caller supplies its own key/CSR), so status is read off the
// CertificateRequest's Ready condition instead; once Ready, its
// status.certificate field carries the signed leaf PEM (base64-encoded --
// cert-manager, like every []byte-typed CRD field, encodes it that way over
// the wire, mirroring how CertificateRequestObject base64-encodes
// spec.request when submitting), from which NotAfter is derived.
func (s *ManagedCertificateService) statusCSR(cert *models.ManagedCertificate) (*CertStatus, error) {
	obj, err := s.controlPlane.Get(context.Background(), kubernetes.CertManagerCertificateRequestGVR, cert.Config.CertificateName, true)
	if err != nil {
		return nil, fmt.Errorf("get certificate request: %w", err)
	}

	result := &CertStatus{Status: models.ManagedCertStatusIssuing}

	ready, reason, message := readyCondition(obj)
	switch ready {
	case "True":
		result.Status = models.ManagedCertStatusReady
		cert.Status = models.ManagedCertStatusReady
		cert.StatusMessage = ""
		if certPEM, ok := nestedString(obj, "status", "certificate"); ok {
			if notAfter := parseNotAfterFromCertificateRequestPEM(certPEM); notAfter != nil {
				formatted := notAfter.UTC().Format(time.RFC3339)
				result.NotAfter = &formatted
				cert.NotAfter = notAfter
			}
		}
	case "False":
		if isCertManagerTerminalFailure(reason) {
			result.Status = models.ManagedCertStatusError
			result.Message = message
			cert.Status = models.ManagedCertStatusError
			cert.StatusMessage = message
		} else {
			// Ready=False with a non-terminal reason (Pending) is the normal
			// in-progress state while cert-manager signs the CertificateRequest.
			result.Status = models.ManagedCertStatusIssuing
			result.Message = message
			cert.Status = models.ManagedCertStatusIssuing
			cert.StatusMessage = message
		}
	default:
		result.Status = models.ManagedCertStatusIssuing
		result.Message = message
		cert.Status = models.ManagedCertStatusIssuing
		cert.StatusMessage = message
	}

	_ = s.repo.Update(cert)
	return result, nil
}

// parseNotAfterFromCertificateRequestPEM decodes a CertificateRequest's
// status.certificate (base64-encoded PEM, falling back to raw PEM defensively
// in case a caller/fake hands it over undecoded) and returns the leaf
// certificate's NotAfter, or nil if it isn't parseable.
func parseNotAfterFromCertificateRequestPEM(raw string) *time.Time {
	crt, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		crt = []byte(raw)
	}

	block, _ := pem.Decode(crt)
	if block == nil {
		return nil
	}

	parsed, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil
	}

	notAfter := parsed.NotAfter
	return &notAfter
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
// entry with type=Ready, returning its status ("True"/"False"/"Unknown"),
// its reason, and its message. An absent conditions list or Ready entry
// reports ("Unknown", "", "") -- treated by Status as still issuing.
func readyCondition(obj *unstructured.Unstructured) (status, reason, message string) {
	conditions, found, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if err != nil || !found {
		return "Unknown", "", ""
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
		r, _, _ := unstructured.NestedString(cond, "reason")
		m, _, _ := unstructured.NestedString(cond, "message")
		if s == "" {
			s = "Unknown"
		}
		return s, r, m
	}
	return "Unknown", "", ""
}

// isCertManagerTerminalFailure reports whether a cert-manager Ready=False
// condition reason represents a genuinely failed issuance. A Ready=False
// condition is the NORMAL in-progress state while cert-manager signs a
// Certificate or CertificateRequest (reason "Pending"/"Issuing"); only
// "Failed"/"Denied" are terminal. Treating every Ready=False as an error
// made a freshly created certificate report "error" during its normal
// issuance window (e.g. before its issuer's ClusterIssuer finished
// reconciling), which callers polling for readiness see as a hard failure.
func isCertManagerTerminalFailure(reason string) bool {
	switch reason {
	case "Failed", "Denied":
		return true
	default:
		return false
	}
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
