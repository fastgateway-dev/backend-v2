package services

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/fastgateway-dev/backend-v2/internal/dnsprovider"
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
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
	// ErrHostedZoneMismatch is recorded on a record's status when its domain
	// hostname is neither the zone apex nor a subdomain of the registered
	// hosted zone -- the record cannot live in that zone.
	ErrHostedZoneMismatch = errors.New("the domain hostname is not contained in its hosted zone")
	// ErrApexCNAME is recorded on a record's status when the resolved record
	// type is CNAME at the zone apex, which DNS does not permit.
	ErrApexCNAME = errors.New("CNAME at a zone apex isn't supported; use an IP gateway or a subdomain")
	// ErrRecordClobber is recorded on a record's status when, on the first
	// write, a record already exists at the provider for this hostname that
	// FastGateway does not own -- we refuse to overwrite it.
	ErrRecordClobber = errors.New("a record already exists at the provider for this hostname that FastGateway does not manage")
)

// enablePollInterval is the per-iteration sleep of Enable's bounded wait for
// the gateway load-balancer address. It is a package-level var only so tests
// can shrink it; production keeps the default 1s (5 polls over ~5s).
var enablePollInterval = 1 * time.Second

// assertDomainInProject confirms the domain identified by domainID belongs to
// projectID before any DNS-record operation touches it. Every public method
// takes the path project from the handler (which authorized the caller against
// that project), so without this check a project-A admin could read, tamper
// with, or delete the live DNS record of a domain owned by project B simply by
// putting B's domainID in the URL. This mirrors the ownership guard
// DomainService.AttachCertificate already applies. gorm.ErrRecordNotFound (an
// unknown domain) surfaces unchanged so the handler maps it to 404; a domain
// in a different project is reported as ErrDomainNotFound so a cross-project
// probe is indistinguishable from a missing domain.
func (s *DNSRecordService) assertDomainInProject(domainID, projectID uuid.UUID) error {
	d, err := s.domainRepo.GetByID(domainID)
	if err != nil {
		return err // gorm.ErrRecordNotFound surfaces as 404
	}
	if d.ProjectID != projectID {
		return ErrDomainNotFound
	}
	return nil
}

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

// gatewayNameForDomain returns the name of the Gateway FastGateway created for
// the domain (domain.K8sGatewayName). This service reads that Gateway's status
// to resolve its load-balancer address.
func gatewayNameForDomain(domain *models.Domain) string { return domain.K8sGatewayName }

// DNSCredentialReader is the narrow role DNSRecordService needs from
// DNSCredentialService: decrypt a stored DNS provider credential by id so
// the direct-provider reconcile can authenticate to the provider.
// Satisfied by *DNSCredentialService.
type DNSCredentialReader interface {
	DecryptedCredentials(id uuid.UUID) (string, map[string]string, error)
}

// Compile-time role satisfaction check.
var _ DNSCredentialReader = (*DNSCredentialService)(nil)

// DNSRecordService reconciles a domain's single DNS record
// (models.DomainDNSRecord) directly against the provider SDK for the
// record's registered hosted zone: it resolves the domain's Gateway
// load-balancer address, then upserts an A/AAAA/CNAME at the provider. There
// is no persistent reconciler -- reconciliation is request-driven (on read)
// plus a one-shot deferred retry launched from Enable when the gateway
// address is not yet assigned.
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
// first reconcile, with a bounded ~5s wait for the gateway load-balancer
// address. If the record is still pending after that wait (the gateway has
// no address yet), a one-shot deferred-retry goroutine is launched. It fails
// only on validation (missing hosted zone, invalid record type, a record
// already exists for the domain); reconcile failures are recorded on the
// returned record's status rather than returned as an error.
func (s *DNSRecordService) Enable(domainID, projectID, createdBy uuid.UUID, in DNSRecordInput) (*models.DomainDNSRecord, error) {
	if err := s.assertDomainInProject(domainID, projectID); err != nil {
		return nil, err
	}
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
	// Bounded ~5s wait for the gateway load-balancer address: a freshly
	// provisioned Gateway often gets its address within a few seconds, so a
	// short in-request poll lets Enable return ready instead of pending in
	// the common case.
	for i := 0; i < 5 && rec.Status == models.DNSRecordStatusPending; i++ {
		time.Sleep(enablePollInterval)
		s.reconcile(rec)
	}
	// Still pending after the bounded wait: hand off to a one-shot deferred
	// retry (there is no persistent reconciler).
	if rec.Status == models.DNSRecordStatusPending {
		s.deferredRetry(domainID)
	}
	return rec, nil
}

// deferredRetry launches a one-shot background goroutine that re-reconciles
// the domain's record roughly every 10s for up to 3 minutes, stopping as soon
// as the record leaves pending (ready or error) or disappears. This is the
// only background work in the DNS path -- there is no persistent reconciler.
func (s *DNSRecordService) deferredRetry(domainID uuid.UUID) {
	go func() {
		deadline := time.Now().Add(3 * time.Minute)
		for time.Now().Before(deadline) {
			time.Sleep(10 * time.Second)
			rec, err := s.repo.GetByDomainID(domainID)
			if err != nil {
				return
			}
			if rec.Status != models.DNSRecordStatusPending {
				return
			}
			s.reconcile(rec)
			if rec.Status == models.DNSRecordStatusReady || rec.Status == models.DNSRecordStatusError {
				return
			}
		}
	}()
}

// List returns every managed DNS record whose domain belongs to projectID, each
// enriched with its domain hostname (the record name) and hosted-zone name,
// ordered by hostname. It is a pure read: unlike Get/Refresh it does not
// reconcile against the provider, so Status reflects the last persisted value
// (the per-domain Get/Refresh path owns reconciliation). The project scoping
// lives in the repository query, so no per-domain ownership check is needed.
func (s *DNSRecordService) List(projectID uuid.UUID) ([]models.DNSRecordListItem, error) {
	return s.repo.ListByProjectID(projectID)
}

// Get returns the domain's DNS record, reconciling only when there is work to
// do: when the record is still pending, or when the gateway's current
// load-balancer address has drifted from the one we last wrote
// (resolved_target). A ready record whose address is unchanged is returned
// from the cached row with no provider call. A failed/absent gateway read on
// a non-pending record never flips it to error -- the cached row is returned.
func (s *DNSRecordService) Get(domainID, projectID uuid.UUID) (*models.DomainDNSRecord, error) {
	if err := s.assertDomainInProject(domainID, projectID); err != nil {
		return nil, err
	}
	rec, err := s.repo.GetByDomainID(domainID)
	if err != nil {
		return nil, err
	}
	if rec.Status == models.DNSRecordStatusPending {
		s.reconcile(rec)
		return rec, nil
	}
	if s.gatewayAddressDrifted(rec) {
		s.reconcile(rec)
	}
	return rec, nil
}

// gatewayAddressDrifted reports whether the domain's current gateway
// load-balancer address differs from the record's resolved_target. It is a
// cheap k8s read (no provider call); any failure to resolve the current
// address reports false (no drift) so a transient gateway-read failure never
// triggers a needless reconcile on a healthy record.
func (s *DNSRecordService) gatewayAddressDrifted(rec *models.DomainDNSRecord) bool {
	domain, err := s.domainRepo.GetByID(rec.DomainID)
	if err != nil || domain == nil {
		return false
	}
	// v1 limitation (carried forward from the pre-swap behavior): the applier
	// reads the Gateway from its single fixed namespace, not domain.Namespace.
	gw, err := s.cp.Get(context.Background(), kubernetes.GatewayGVR, gatewayNameForDomain(domain), true)
	if err != nil {
		return false
	}
	addr, ok := resolveGatewayAddress(gw)
	if !ok {
		return false
	}
	return addr.Value != rec.ResolvedTarget
}

// Refresh re-runs Get and returns the fresh record; an explicit alias for Get
// so callers (e.g. a "refresh" handler action) can express intent.
func (s *DNSRecordService) Refresh(domainID, projectID uuid.UUID) (*models.DomainDNSRecord, error) {
	return s.Get(domainID, projectID)
}

// Update changes the record's type/TTL/proxied settings and re-reconciles.
// When the record type changes, the clobber check is skipped because
// resolved_target is already non-empty (FastGateway already owns the record).
func (s *DNSRecordService) Update(domainID, projectID uuid.UUID, in DNSRecordInput) (*models.DomainDNSRecord, error) {
	if err := s.assertDomainInProject(domainID, projectID); err != nil {
		return nil, err
	}
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

// Delete removes the domain's DNS record, deleting it at the provider first
// when FastGateway owns it (resolved_target != ""). A real provider-delete
// failure returns the error and keeps the row so the delete can be retried;
// the provider clients all return nil when the record is already absent, so a
// non-nil error always means a genuine failure. A record that was never
// written to a provider (resolved_target == "") is simply removed.
func (s *DNSRecordService) Delete(domainID, projectID uuid.UUID) error {
	if err := s.assertDomainInProject(domainID, projectID); err != nil {
		return err
	}
	rec, err := s.repo.GetByDomainID(domainID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if rec.ResolvedTarget == "" {
		// Never written to a provider: nothing live to tear down.
		return s.repo.DeleteByDomainID(domainID)
	}

	domain, err := s.domainRepo.GetByID(rec.DomainID)
	if err != nil {
		return err
	}
	zone, err := s.zoneRepo.GetByID(rec.HostedZoneID)
	if err != nil {
		return err
	}
	providerType, creds, err := s.creds.DecryptedCredentials(zone.ProviderCredentialID)
	if err != nil {
		return err
	}
	prov, ok := dnsProviderLookup(providerType)
	if !ok {
		return errors.New("unsupported DNS provider: " + providerType)
	}
	client, err := prov.NewClient(creds)
	if err != nil {
		return err
	}

	// Determine the record type actually written at the provider. rec.RecordType
	// is the user's intent and may still be "auto" (the default), which reconcile
	// resolved to A/AAAA/CNAME only locally and never persisted. Deleting by
	// "auto" would match nothing and silently orphan the live record, so
	// reconstruct the written address from ResolvedTarget (guaranteed non-empty
	// here) and recompute the effective type exactly as reconcile did -- without
	// mutating rec.RecordType, so "auto" stays adaptive to future IP<->hostname
	// gateway changes.
	kind := "hostname"
	if net.ParseIP(rec.ResolvedTarget) != nil {
		kind = "ip"
	}
	rt, err := recordTypeForAddress(GatewayAddress{Value: rec.ResolvedTarget, Kind: kind}, rec.RecordType)
	if err != nil {
		// Can't determine the type to delete: keep the row rather than orphan it.
		return err
	}

	if err := client.DeleteRecord(context.Background(), zone.ProviderZoneID, domain.Hostname, string(rt)); err != nil {
		// Keep the row on a real failure so the delete can be retried.
		return err
	}
	return s.repo.DeleteByDomainID(domainID)
}

// reconcile performs the direct-provider write for one record: resolve the
// domain and its hosted zone, confirm the hostname is contained in the zone,
// read the domain's Gateway load-balancer address, resolve the effective
// record type, guard against an apex CNAME, then (on the first write only)
// refuse to clobber a foreign record and finally upsert the record at the
// provider. Terminal problems set status=error via setErr; not-yet-ready
// conditions (gateway missing / no address) set status=pending. reconcile
// returns nothing; callers read rec.Status.
func (s *DNSRecordService) reconcile(rec *models.DomainDNSRecord) {
	setErr := func(msg string) {
		rec.Status = models.DNSRecordStatusError
		rec.StatusMessage = msg
		_ = s.repo.Update(rec)
	}
	setPending := func(msg string) {
		rec.Status = models.DNSRecordStatusPending
		rec.StatusMessage = msg
		_ = s.repo.Update(rec)
	}

	domain, err := s.domainRepo.GetByID(rec.DomainID)
	if err != nil {
		setErr("domain not found")
		return
	}
	zone, err := s.zoneRepo.GetByID(rec.HostedZoneID)
	if err != nil {
		setErr("hosted zone not found")
		return
	}

	// Hosted-zone containment: the record's hostname must be the zone apex or
	// a subdomain of it.
	if domain.Hostname != zone.Name && !strings.HasSuffix(domain.Hostname, "."+zone.Name) {
		setErr(ErrHostedZoneMismatch.Error())
		return
	}

	// Resolve the gateway load-balancer address.
	// v1 limitation (carried forward from the pre-swap behavior): the applier
	// reads the Gateway from its single fixed namespace, not domain.Namespace.
	gw, err := s.cp.Get(context.Background(), kubernetes.GatewayGVR, gatewayNameForDomain(domain), true)
	if err != nil {
		setPending("waiting for the gateway")
		return
	}
	addr, ok := resolveGatewayAddress(gw)
	if !ok {
		setPending("waiting for the gateway load-balancer address")
		return
	}

	rt, err := recordTypeForAddress(addr, rec.RecordType)
	if err != nil {
		setErr(err.Error())
		return
	}

	// Apex + CNAME guard: a CNAME cannot live at a zone apex. (A/AAAA at apex
	// is fine.)
	if rt == models.DNSRecordTypeCNAME && domain.Hostname == zone.Name {
		setErr(ErrApexCNAME.Error())
		return
	}

	providerType, creds, err := s.creds.DecryptedCredentials(zone.ProviderCredentialID)
	if err != nil {
		setErr("credential error: " + err.Error())
		return
	}
	prov, ok := dnsProviderLookup(providerType)
	if !ok {
		setErr("unsupported DNS provider: " + providerType)
		return
	}
	client, err := prov.NewClient(creds)
	if err != nil {
		setErr(err.Error())
		return
	}

	ctx := context.Background()

	// Clobber check -- first write only. Once we own the record
	// (resolved_target != ""), updates skip GetRecord and upsert directly.
	if rec.ResolvedTarget == "" {
		_, found, gerr := client.GetRecord(ctx, zone.ProviderZoneID, domain.Hostname, string(rt))
		if gerr != nil {
			setErr(gerr.Error())
			return
		}
		if found {
			setErr("a record already exists for " + domain.Hostname + " not managed by FastGateway")
			return
		}
	}

	// Cloudflare forces auto TTL when the record is proxied; proxied is only
	// meaningful for Cloudflare.
	proxied := rec.Proxied && providerType == "cloudflare"
	ttl := rec.TTL
	if proxied {
		ttl = nil
	}

	if err := client.UpsertRecord(ctx, zone.ProviderZoneID, dnsprovider.Record{
		Name:    domain.Hostname,
		Type:    string(rt),
		Target:  addr.Value,
		TTL:     ttl,
		Proxied: proxied,
	}); err != nil {
		setErr(err.Error())
		return
	}

	rec.ResolvedTarget = addr.Value
	rec.Status = models.DNSRecordStatusReady
	rec.StatusMessage = "record written"
	_ = s.repo.Update(rec)
}
