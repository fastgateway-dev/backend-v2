package services

import (
	"errors"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	// ErrNoHostedZone is returned by Enable when the caller does not supply a
	// hosted zone to write the record into. Under the direct-provider model
	// every record belongs to exactly one registered hosted zone -- there is
	// no implicit "active credential" fallback anymore.
	ErrNoHostedZone = errors.New("hostedZoneId is required")
	// ErrDNSRecordExists is returned by Enable when a DomainDNSRecord already
	// exists for the domain -- there is exactly one per domain. A sentinel
	// (rather than an inline errors.New) so the handler layer can map it with
	// errors.Is.
	ErrDNSRecordExists = errors.New("DNS record already exists for this domain")
	// ErrInvalidRecordType is returned by Enable/Update when the caller
	// supplies a RecordType that isn't one of the types DNSRecordService
	// understands.
	ErrInvalidRecordType = errors.New("invalid record type (allowed: auto, A, AAAA, CNAME)")
)

// isValidRecordType reports whether rt is one of the record types
// DNSRecordService understands.
func isValidRecordType(rt models.DNSRecordType) bool {
	switch rt {
	case models.DNSRecordTypeAuto, models.DNSRecordTypeA, models.DNSRecordTypeAAAA, models.DNSRecordTypeCNAME:
		return true
	default:
		return false
	}
}

// DNSCredentialReader is the narrow role DNSRecordService needs from
// DNSCredentialService: decrypt a stored DNS provider credential by id so
// the (future) direct-provider reconcile can authenticate to the provider.
// Satisfied by *DNSCredentialService.
type DNSCredentialReader interface {
	DecryptedCredentials(id uuid.UUID) (string, map[string]string, error)
}

// Compile-time role satisfaction check.
var _ DNSCredentialReader = (*DNSCredentialService)(nil)

// DNSRecordService reconciles a domain's single DNS record
// (models.DomainDNSRecord) directly against the provider SDK for the
// record's registered hosted zone. reconcile is currently a stub (the
// direct-provider write path is a later task); Enable/Get/Update/Delete
// already expose the hosted-zone-based shape this task establishes.
type DNSRecordService struct {
	repo       repository.DomainDNSRecordRepositoryInterface
	domainRepo repository.DomainRepositoryInterface
	zoneRepo   repository.DNSHostedZoneRepositoryInterface
	creds      DNSCredentialReader
	cp         CertInfraApplier
}

// DNSRecordServiceDeps are DNSRecordService's required dependencies.
// NewDNSRecordService panics if any of them is nil, following the house
// pattern for services with required constructor dependencies.
type DNSRecordServiceDeps struct {
	Repo         repository.DomainDNSRecordRepositoryInterface
	DomainRepo   repository.DomainRepositoryInterface
	ZoneRepo     repository.DNSHostedZoneRepositoryInterface
	Creds        DNSCredentialReader
	ControlPlane CertInfraApplier
}

func NewDNSRecordService(deps DNSRecordServiceDeps) *DNSRecordService {
	if deps.Repo == nil || deps.DomainRepo == nil || deps.ZoneRepo == nil || deps.Creds == nil || deps.ControlPlane == nil {
		panic("services.NewDNSRecordService: missing required dependency")
	}
	return &DNSRecordService{
		repo:       deps.Repo,
		domainRepo: deps.DomainRepo,
		zoneRepo:   deps.ZoneRepo,
		creds:      deps.Creds,
		cp:         deps.ControlPlane,
	}
}

// DNSRecordInput is the create/update payload for a domain's DNS record.
type DNSRecordInput struct {
	HostedZoneID *uuid.UUID
	RecordType   models.DNSRecordType
	TTL          *int
	Proxied      bool
}

// Enable creates the DNS record for a domain and performs a best-effort
// first reconcile. It fails only on validation (missing hosted zone,
// invalid record type, a record already exists for the domain); reconcile
// failures are recorded on the returned record's status rather than
// returned as an error.
func (s *DNSRecordService) Enable(domainID, createdBy uuid.UUID, in DNSRecordInput) (*models.DomainDNSRecord, error) {
	if in.HostedZoneID == nil {
		return nil, ErrNoHostedZone
	}

	if _, err := s.repo.GetByDomainID(domainID); err == nil {
		return nil, ErrDNSRecordExists
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	rt := in.RecordType
	if rt == "" {
		rt = models.DNSRecordTypeAuto
	}
	if !isValidRecordType(rt) {
		return nil, ErrInvalidRecordType
	}

	rec := &models.DomainDNSRecord{
		DomainID:     domainID,
		HostedZoneID: *in.HostedZoneID,
		RecordType:   rt,
		TTL:          in.TTL,
		Proxied:      in.Proxied,
		Status:       models.DNSRecordStatusPending,
		CreatedBy:    createdBy,
	}
	if err := s.repo.Create(rec); err != nil {
		return nil, err
	}
	s.reconcile(rec)
	return rec, nil
}

// Get returns the domain's DNS record after a best-effort reconcile, so
// callers always see live status rather than a stale snapshot
// (status-on-read; there is no background reconciler).
func (s *DNSRecordService) Get(domainID uuid.UUID) (*models.DomainDNSRecord, error) {
	rec, err := s.repo.GetByDomainID(domainID)
	if err != nil {
		return nil, err
	}
	s.reconcile(rec)
	return rec, nil
}

// Refresh re-runs reconcile and returns the fresh record; an explicit alias
// for Get so callers (e.g. a "refresh" handler action) can express intent.
func (s *DNSRecordService) Refresh(domainID uuid.UUID) (*models.DomainDNSRecord, error) {
	return s.Get(domainID)
}

// Update changes the record's type/TTL/proxied settings and re-reconciles.
func (s *DNSRecordService) Update(domainID uuid.UUID, in DNSRecordInput) (*models.DomainDNSRecord, error) {
	rec, err := s.repo.GetByDomainID(domainID)
	if err != nil {
		return nil, err
	}
	if in.RecordType != "" {
		if !isValidRecordType(in.RecordType) {
			return nil, ErrInvalidRecordType
		}
		rec.RecordType = in.RecordType
	}
	rec.TTL = in.TTL
	rec.Proxied = in.Proxied
	if err := s.repo.Update(rec); err != nil {
		return nil, err
	}
	s.reconcile(rec)
	return rec, nil
}

// Delete removes the DomainDNSRecord row. The stub reconcile never writes to
// a provider, so there is nothing live to tear down yet -- a later task
// (the direct-provider write path) adds the provider-side DeleteRecord call
// here, per the design doc's error-handling section (provider delete
// failure keeps the row; this task has no provider call to fail).
func (s *DNSRecordService) Delete(domainID uuid.UUID) error {
	if _, err := s.repo.GetByDomainID(domainID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	return s.repo.DeleteByDomainID(domainID)
}

// reconcile is a STUB: the direct-provider write path (resolving the
// record's hosted zone, decrypting its credential, and calling the
// provider's DNSClient to upsert the record) is a later task. For now it
// just marks the record pending so Enable/Get/Update/Refresh have a
// consistent, compiling status-on-read shape to build on.
func (s *DNSRecordService) reconcile(rec *models.DomainDNSRecord) {
	rec.Status = models.DNSRecordStatusPending
	rec.StatusMessage = "pending (reconcile not yet implemented)"
	_ = s.repo.Update(rec)
}
