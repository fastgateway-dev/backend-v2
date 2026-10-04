package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var (
	ErrNoActiveDNSCredential = errors.New("no active DNS credential is configured")
	ErrCredentialNotActive   = errors.New("providerCredentialId must equal the active DNS credential")
)

// DNSRecordService reconciles a domain's single DNS record
// (models.DomainDNSRecord) to a DNSEndpoint custom resource that
// external-dns watches and syncs to the configured DNS provider. The
// gateway's load-balancer address is resolved lazily (best-effort, on every
// read) rather than through a background reconciler: reconcile never
// returns an error, it records the outcome in the record's own
// status/status_message fields (status-on-read).
type DNSRecordService struct {
	repo       repository.DomainDNSRecordRepositoryInterface
	domainRepo repository.DomainRepositoryInterface
	infra      *DNSInfraService
	cp         CertInfraApplier
}

// DNSRecordServiceDeps are DNSRecordService's required dependencies.
// NewDNSRecordService panics if any of them is nil, following the house
// pattern for services with required constructor dependencies.
type DNSRecordServiceDeps struct {
	Repo         repository.DomainDNSRecordRepositoryInterface
	DomainRepo   repository.DomainRepositoryInterface
	Infra        *DNSInfraService
	ControlPlane CertInfraApplier
}

func NewDNSRecordService(deps DNSRecordServiceDeps) *DNSRecordService {
	if deps.Repo == nil || deps.DomainRepo == nil || deps.Infra == nil || deps.ControlPlane == nil {
		panic("services.NewDNSRecordService: missing required dependency")
	}
	return &DNSRecordService{repo: deps.Repo, domainRepo: deps.DomainRepo, infra: deps.Infra, cp: deps.ControlPlane}
}

// DNSRecordInput is the create/update payload for a domain's DNS record.
type DNSRecordInput struct {
	ProviderCredentialID *uuid.UUID
	RecordType           models.DNSRecordType
	TTL                  *int
	Proxied              bool
}

// gatewayNameForDomain returns the name of the Gateway FastGateway created
// for this domain. It must stay identical to the name domainplan uses when
// building the Gateway/BackendTrafficPolicy/ClientTrafficPolicy/
// EnvoyExtensionPolicy configs for the domain (internal/domainplan/gateway.go
// and friends all key off domain.K8sGatewayName), since this service reads
// that same Gateway's status to resolve its load-balancer address.
func gatewayNameForDomain(domain *models.Domain) string {
	return domain.K8sGatewayName
}

// resolveActiveCredential resolves the DNS provider credential Enable/Update
// must use: the system-wide active credential (there is exactly one at a
// time; see DNSInfraService.GetActiveCredentialID). If the caller supplied
// an explicit ProviderCredentialID it must match the active one -- callers
// confirm the already-active credential, they cannot pick an arbitrary one.
func (s *DNSRecordService) resolveActiveCredential(in DNSRecordInput) (uuid.UUID, error) {
	active, err := s.infra.GetActiveCredentialID()
	if err != nil {
		return uuid.Nil, err
	}
	if active == nil {
		return uuid.Nil, ErrNoActiveDNSCredential
	}
	if in.ProviderCredentialID != nil && *in.ProviderCredentialID != *active {
		return uuid.Nil, ErrCredentialNotActive
	}
	return *active, nil
}

// Enable creates the DNS record for a domain and performs a best-effort
// first reconcile. It fails only on validation (no/mismatched active
// credential, a record already exists for the domain); reconcile failures
// are recorded on the returned record's status rather than returned as an
// error.
func (s *DNSRecordService) Enable(domainID, createdBy uuid.UUID, in DNSRecordInput) (*models.DomainDNSRecord, error) {
	if _, err := s.repo.GetByDomainID(domainID); err == nil {
		return nil, errors.New("DNS record already exists for this domain")
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	cred, err := s.resolveActiveCredential(in)
	if err != nil {
		return nil, err
	}

	rt := in.RecordType
	if rt == "" {
		rt = models.DNSRecordTypeAuto
	}

	rec := &models.DomainDNSRecord{
		DomainID:             domainID,
		ProviderCredentialID: cred,
		RecordType:           rt,
		TTL:                  in.TTL,
		Proxied:              in.Proxied,
		Status:               models.DNSRecordStatusPending,
		CreatedBy:            createdBy,
	}
	if err := s.repo.Create(rec); err != nil {
		return nil, err
	}
	// EndpointName embeds the record's own id, so it is only known once
	// Create has assigned rec.ID. reconcile persists it (along with the
	// first-pass status) via repo.Update.
	rec.EndpointName = "dns-" + rec.ID.String()
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
	if _, err := s.resolveActiveCredential(in); err != nil {
		return nil, err
	}
	if in.RecordType != "" {
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

// Delete removes the DNSEndpoint (so external-dns tears down the live DNS
// record) and then the DomainDNSRecord row. Deleting the DNSEndpoint is
// best-effort: if it errors (e.g. already gone) the row is still removed,
// so a dangling backend row never blocks re-enabling DNS for the domain.
func (s *DNSRecordService) Delete(domainID uuid.UUID) error {
	rec, err := s.repo.GetByDomainID(domainID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	_ = s.cp.Delete(context.Background(), kubernetes.DNSEndpointGVR, rec.EndpointName, true)
	return s.repo.DeleteByDomainID(domainID)
}

// reconcile resolves the domain's Gateway load-balancer address, applies (or
// leaves untouched) the DNSEndpoint for the record, and sets status +
// resolved_target accordingly. It never returns an error -- see the
// package-level DNSRecordService doc comment on status-on-read.
//
// Known v1 limitation: CertInfraApplier.Get reads the Gateway from its fixed
// control-plane namespace (fastgateway-system). A domain deployed to a
// different namespace will never resolve an address here and stays
// "pending" indefinitely. Acceptable for the lazy/best-effort v1 model;
// intentionally not fixed by this task.
func (s *DNSRecordService) reconcile(rec *models.DomainDNSRecord) {
	setErr := func(msg string) {
		rec.Status = models.DNSRecordStatusError
		rec.StatusMessage = msg
		_ = s.repo.Update(rec)
	}

	domain, err := s.domainRepo.GetByID(rec.DomainID)
	if err != nil {
		setErr("domain not found")
		return
	}

	gw, err := s.cp.Get(context.Background(), kubernetes.GatewayGVR, gatewayNameForDomain(domain), true)
	if err != nil {
		rec.Status = models.DNSRecordStatusPending
		rec.StatusMessage = "waiting for the gateway to be created"
		_ = s.repo.Update(rec)
		return
	}

	addr, ok := resolveGatewayAddress(gw)
	if !ok {
		rec.Status = models.DNSRecordStatusPending
		rec.StatusMessage = "waiting for the gateway load-balancer address"
		_ = s.repo.Update(rec)
		return
	}

	rt, err := recordTypeForAddress(addr, rec.RecordType)
	if err != nil {
		setErr(err.Error())
		return
	}

	ep := kubernetes.DNSEndpoint(kubernetes.DNSEndpointConfig{
		Name:              rec.EndpointName,
		Hostname:          domain.Hostname,
		RecordType:        string(rt),
		Targets:           []string{addr.Value},
		TTL:               rec.TTL,
		CloudflareProxied: rec.Proxied,
	})
	if err := s.cp.ApplyNamespaced(context.Background(), kubernetes.DNSEndpointGVR, ep); err != nil {
		setErr(fmt.Sprintf("failed to apply DNSEndpoint: %v", err))
		return
	}

	rec.ResolvedTarget = addr.Value
	// Readiness rule: ready once the live DNSEndpoint carries the desired
	// target; until external-dns (or, in tests, the fake applier) reflects
	// that back, the record is syncing.
	if s.endpointMatches(rec.EndpointName, domain.Hostname, addr.Value) {
		rec.Status = models.DNSRecordStatusReady
		rec.StatusMessage = "record submitted to external-dns"
	} else {
		rec.Status = models.DNSRecordStatusSyncing
		rec.StatusMessage = "applying record via external-dns"
	}
	_ = s.repo.Update(rec)
}

// endpointMatches reports whether the live DNSEndpoint named name already
// carries hostname -> target among its endpoints.
func (s *DNSRecordService) endpointMatches(name, hostname, target string) bool {
	obj, err := s.cp.Get(context.Background(), kubernetes.DNSEndpointGVR, name, true)
	if err != nil {
		return false
	}
	eps, found, err := unstructured.NestedSlice(obj.Object, "spec", "endpoints")
	if err != nil || !found {
		return false
	}
	for _, e := range eps {
		m, ok := e.(map[string]interface{})
		if !ok {
			continue
		}
		if dn, _ := m["dnsName"].(string); dn != hostname {
			continue
		}
		targets, ok := m["targets"].([]interface{})
		if !ok {
			continue
		}
		for _, tgt := range targets {
			if ts, _ := tgt.(string); ts == target {
				return true
			}
		}
	}
	return false
}
