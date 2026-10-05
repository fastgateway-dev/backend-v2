package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/fastgateway-dev/backend-v2/internal/dnsprovider"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
)

// findZoneTimeout bounds the provider round-trip Create makes while
// validating a newly-registered zone, so a slow/unreachable provider can't
// hang the request indefinitely.
const findZoneTimeout = 5 * time.Second

// dnsProviderLookup resolves a provider type to a dnsprovider.DNSProvider.
// It is a package-level var -- rather than Create calling dnsprovider.Get
// directly -- purely as a test seam: production always runs with the
// default below (the real dnsprovider.Get), and tests substitute a fake
// DNSProvider/DNSClient so Create's provider-validation path can be
// exercised without a live provider account.
var dnsProviderLookup = dnsprovider.Get

// ErrDNSHostedZoneInUse is returned by Delete when the zone still has one or
// more DomainDNSRecords pointing at it -- deleting it out from under them
// would leave those records unable to resolve to a provider zone. A
// sentinel so the handler layer can map it with errors.Is (409).
var ErrDNSHostedZoneInUse = errors.New("DNS hosted zone is in use by one or more DNS records")

// ErrHostedZoneNotFound is returned by GetByID when no hosted zone exists
// for the given id. A sentinel (rather than passing gorm.ErrRecordNotFound
// through directly) so the handler layer doesn't need to depend on gorm to
// map it to a 404.
var ErrHostedZoneNotFound = errors.New("DNS hosted zone not found")

// DNSHostedZoneService registers DNS hosted zones (domain apexes) under a
// DNS provider credential. Create validates the zone against the live
// provider -- resolving and caching the provider's own zone id -- so later
// record writes (DNSRecordService's reconcile, a later task) always have a
// ready-to-use ProviderZoneID instead of re-resolving it on every call.
type DNSHostedZoneService struct {
	zoneRepo   repository.DNSHostedZoneRepositoryInterface
	recordRepo repository.DomainDNSRecordRepositoryInterface
	creds      DNSCredentialReader
}

// NewDNSHostedZoneService builds a DNSHostedZoneService. It panics if any
// dependency is nil, following the house pattern for services with required
// constructor dependencies (see DNSRecordService, DNSCredentialService).
func NewDNSHostedZoneService(zoneRepo repository.DNSHostedZoneRepositoryInterface, recordRepo repository.DomainDNSRecordRepositoryInterface, creds DNSCredentialReader) *DNSHostedZoneService {
	if zoneRepo == nil || recordRepo == nil || creds == nil {
		panic("services.NewDNSHostedZoneService: missing required dependency")
	}
	return &DNSHostedZoneService{zoneRepo: zoneRepo, recordRepo: recordRepo, creds: creds}
}

// Create registers a new hosted zone named name under the DNS provider
// credential credID, then validates it against the live provider:
//
//   - If credID doesn't resolve to a stored credential, Create fails and
//     persists nothing.
//   - If the credential's provider type isn't supported, Create fails and
//     persists nothing.
//   - Otherwise Create builds a provider client and calls FindZone. On a
//     provider/transport error, or when the provider reports the zone
//     doesn't exist, Create still PERSISTS the zone row (Status =
//     DNSZoneStatusError, StatusMessage describing why) and returns it with
//     a nil error -- so the caller sees the failed zone on the list and can
//     delete/retry it, instead of losing the registration attempt. On
//     success the row is persisted with the resolved ProviderZoneID and
//     Status = DNSZoneStatusReady.
func (s *DNSHostedZoneService) Create(name string, credID, createdBy uuid.UUID) (*models.DNSHostedZone, error) {
	providerType, creds, err := s.creds.DecryptedCredentials(credID)
	if err != nil {
		return nil, fmt.Errorf("DNS credential not found: %w", err)
	}

	prov, ok := dnsProviderLookup(providerType)
	if !ok {
		return nil, fmt.Errorf("unsupported DNS provider: %s", providerType)
	}

	client, err := prov.NewClient(creds)
	if err != nil {
		return nil, fmt.Errorf("building DNS provider client: %w", err)
	}

	zone := &models.DNSHostedZone{
		Name:                 name,
		ProviderCredentialID: credID,
		CreatedBy:            createdBy,
	}

	ctx, cancel := context.WithTimeout(context.Background(), findZoneTimeout)
	defer cancel()
	providerZoneID, found, err := client.FindZone(ctx, name)
	switch {
	case err != nil:
		zone.Status = models.DNSZoneStatusError
		zone.StatusMessage = err.Error()
	case !found:
		zone.Status = models.DNSZoneStatusError
		zone.StatusMessage = "zone not found at provider"
	default:
		zone.ProviderZoneID = providerZoneID
		zone.Status = models.DNSZoneStatusReady
	}

	if err := s.zoneRepo.Create(zone); err != nil {
		return nil, err
	}
	return zone, nil
}

// List returns every registered hosted zone.
func (s *DNSHostedZoneService) List() ([]models.DNSHostedZone, error) {
	return s.zoneRepo.List()
}

// GetByID returns the hosted zone by id, mapping a not-found row to
// ErrHostedZoneNotFound.
func (s *DNSHostedZoneService) GetByID(id uuid.UUID) (*models.DNSHostedZone, error) {
	z, err := s.zoneRepo.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrHostedZoneNotFound
		}
		return nil, err
	}
	return z, nil
}

// Delete removes a hosted zone, but refuses when one or more
// DomainDNSRecords still point at it (ErrDNSHostedZoneInUse) -- deleting it
// out from under them would leave those records unable to resolve to a
// provider zone.
func (s *DNSHostedZoneService) Delete(id uuid.UUID) error {
	n, err := s.recordRepo.CountByZone(id)
	if err != nil {
		return err
	}
	if n > 0 {
		return ErrDNSHostedZoneInUse
	}
	return s.zoneRepo.Delete(id)
}
