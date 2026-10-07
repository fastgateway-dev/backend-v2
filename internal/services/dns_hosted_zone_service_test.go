package services

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/fastgateway-dev/backend-v2/internal/dnsprovider"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
)

// This file is an internal (package services) test so it can install a fake
// behind the dnsProviderLookup test seam -- the real dnsprovider registry
// only holds live SDK-backed providers (cloudflare/route53/google), and
// driving FindZone through one of those in a unit test would mean a real
// network call. See dns_hosted_zone_service.go's dnsProviderLookup doc.

// --- fakeHostedZoneRepo ------------------------------------------------------
//
// A minimal in-memory repository.DNSHostedZoneRepositoryInterface: the
// production repo is a thin GORM wrapper (Task 7), and the service's own
// behavior is what these tests exercise.

type fakeHostedZoneRepo struct {
	zones map[uuid.UUID]*models.DNSHostedZone
}

func newFakeHostedZoneRepo() *fakeHostedZoneRepo {
	return &fakeHostedZoneRepo{zones: map[uuid.UUID]*models.DNSHostedZone{}}
}

func (f *fakeHostedZoneRepo) Create(z *models.DNSHostedZone) error {
	if z.ID == uuid.Nil {
		z.ID = uuid.New()
	}
	f.zones[z.ID] = z
	return nil
}

func (f *fakeHostedZoneRepo) GetByID(id uuid.UUID) (*models.DNSHostedZone, error) {
	z, ok := f.zones[id]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	return z, nil
}

func (f *fakeHostedZoneRepo) List() ([]models.DNSHostedZone, error) {
	out := make([]models.DNSHostedZone, 0, len(f.zones))
	for _, z := range f.zones {
		out = append(out, *z)
	}
	return out, nil
}

func (f *fakeHostedZoneRepo) Update(z *models.DNSHostedZone) error {
	f.zones[z.ID] = z
	return nil
}

func (f *fakeHostedZoneRepo) Delete(id uuid.UUID) error {
	delete(f.zones, id)
	return nil
}

func (f *fakeHostedZoneRepo) CountByCredential(credID uuid.UUID) (int64, error) {
	var n int64
	for _, z := range f.zones {
		if z.ProviderCredentialID == credID {
			n++
		}
	}
	return n, nil
}

var _ repository.DNSHostedZoneRepositoryInterface = (*fakeHostedZoneRepo)(nil)

// --- fakeZoneRecordRepo -------------------------------------------------------
//
// A minimal in-memory repository.DomainDNSRecordRepositoryInterface. Only
// CountByZone is exercised by DNSHostedZoneService; the rest satisfy the
// interface with harmless no-ops. CountByZone reads d.recordsForZone live
// (for d.zoneID) so a test can just assign d.recordsForZone = N and call
// Delete, with no separate wiring step.

type fakeZoneRecordRepo struct {
	d *hostedZoneTestDeps
}

func (f *fakeZoneRecordRepo) Create(rec *models.DomainDNSRecord) error { return nil }

func (f *fakeZoneRecordRepo) GetByDomainID(domainID uuid.UUID) (*models.DomainDNSRecord, error) {
	return nil, gorm.ErrRecordNotFound
}

func (f *fakeZoneRecordRepo) ListByProjectID(projectID uuid.UUID) ([]models.DNSRecordListItem, error) {
	return nil, nil
}

func (f *fakeZoneRecordRepo) Update(rec *models.DomainDNSRecord) error { return nil }

func (f *fakeZoneRecordRepo) DeleteByDomainID(domainID uuid.UUID) error { return nil }

func (f *fakeZoneRecordRepo) CountByZone(zoneID uuid.UUID) (int64, error) {
	if zoneID == f.d.zoneID {
		return f.d.recordsForZone, nil
	}
	return 0, nil
}

var _ repository.DomainDNSRecordRepositoryInterface = (*fakeZoneRecordRepo)(nil)

// --- fakeZoneCredReader --------------------------------------------------------

type fakeZoneCredReader struct {
	credID       uuid.UUID
	providerType string
	creds        map[string]string
	err          error
}

func (f *fakeZoneCredReader) DecryptedCredentials(id uuid.UUID) (string, map[string]string, error) {
	if f.err != nil {
		return "", nil, f.err
	}
	if id != f.credID {
		return "", nil, gorm.ErrRecordNotFound
	}
	return f.providerType, f.creds, nil
}

var _ DNSCredentialReader = (*fakeZoneCredReader)(nil)

// --- fake DNS provider/client ---------------------------------------------
//
// fakeZoneProvider/fakeZoneClient stand in for a live dnsprovider.DNSProvider
// so Create's FindZone call can be driven by the test without a real
// provider account. Installed behind dnsProviderLookup for the "faketest"
// provider type only.

type fakeZoneClient struct {
	d *hostedZoneTestDeps
}

func (c fakeZoneClient) FindZone(ctx context.Context, zoneName string) (string, bool, error) {
	return c.d.findZoneID, c.d.findZoneFound, c.d.findZoneErr
}

func (c fakeZoneClient) RecordExistsForName(ctx context.Context, providerZoneID, name string) (bool, error) {
	return false, nil
}

func (c fakeZoneClient) GetRecord(ctx context.Context, providerZoneID, name, recordType string) (dnsprovider.Record, bool, error) {
	return dnsprovider.Record{}, false, errors.New("fakeZoneClient: GetRecord not implemented")
}

func (c fakeZoneClient) UpsertRecord(ctx context.Context, providerZoneID string, r dnsprovider.Record) error {
	return errors.New("fakeZoneClient: UpsertRecord not implemented")
}

func (c fakeZoneClient) DeleteRecord(ctx context.Context, providerZoneID, name, recordType string) error {
	return errors.New("fakeZoneClient: DeleteRecord not implemented")
}

var _ dnsprovider.DNSClient = fakeZoneClient{}

type fakeZoneProvider struct{ client dnsprovider.DNSClient }

func (fakeZoneProvider) Type() string                     { return "faketest" }
func (fakeZoneProvider) RequiredFields() []string         { return nil }
func (fakeZoneProvider) Validate(map[string]string) error { return nil }
func (p fakeZoneProvider) NewClient(map[string]string) (dnsprovider.DNSClient, error) {
	return p.client, nil
}

var _ dnsprovider.DNSProvider = fakeZoneProvider{}

// --- harness -----------------------------------------------------------------

// hostedZoneTestDeps bundles the fakes newTestHostedZoneService wires up,
// plus the mutable knobs each test sets before calling into the service:
// findZoneID/findZoneFound/findZoneErr drive the fake provider's FindZone
// response, recordsForZone drives the record repo's in-use guard, and
// credID/userID/zoneID are fixed ids the tests key off of.
type hostedZoneTestDeps struct {
	findZoneID    string
	findZoneFound bool
	findZoneErr   error

	credID         uuid.UUID
	userID         uuid.UUID
	zoneID         uuid.UUID
	recordsForZone int64

	zoneRepo   *fakeHostedZoneRepo
	recordRepo *fakeZoneRecordRepo
}

// newTestHostedZoneService builds a services.DNSHostedZoneService wired to
// hand-rolled fakes for all three dependencies, and installs a fake
// dnsprovider.DNSProvider (type "faketest") behind the dnsProviderLookup
// test seam so Create's FindZone call reads d.findZoneID/findZoneFound/
// findZoneErr -- restoring the real dnsprovider.Get on test cleanup.
func newTestHostedZoneService(t *testing.T) (*DNSHostedZoneService, *hostedZoneTestDeps) {
	t.Helper()

	d := &hostedZoneTestDeps{
		findZoneFound: true,
		credID:        uuid.New(),
		userID:        uuid.New(),
		zoneID:        uuid.New(),
		zoneRepo:      newFakeHostedZoneRepo(),
	}
	d.recordRepo = &fakeZoneRecordRepo{d: d}

	credReader := &fakeZoneCredReader{
		credID:       d.credID,
		providerType: "faketest",
		creds:        map[string]string{"apiToken": "test"},
	}

	orig := dnsProviderLookup
	dnsProviderLookup = func(providerType string) (dnsprovider.DNSProvider, bool) {
		if providerType != "faketest" {
			return nil, false
		}
		return fakeZoneProvider{client: fakeZoneClient{d: d}}, true
	}
	t.Cleanup(func() { dnsProviderLookup = orig })

	svc := NewDNSHostedZoneService(d.zoneRepo, d.recordRepo, credReader)
	return svc, d
}

// --- tests -------------------------------------------------------------------

func TestHostedZone_Create_ValidatesAndCachesZoneID(t *testing.T) {
	svc, d := newTestHostedZoneService(t)
	d.findZoneID, d.findZoneFound = "cfzone1", true

	z, err := svc.Create("example.com", d.credID, d.userID)
	if err != nil {
		t.Fatal(err)
	}
	if z.ProviderZoneID != "cfzone1" || z.Status != models.DNSZoneStatusReady {
		t.Fatalf("got %+v", z)
	}
	if z.Name != "example.com" || z.ProviderCredentialID != d.credID || z.CreatedBy != d.userID {
		t.Fatalf("got %+v", z)
	}

	// The row must actually be persisted.
	got, err := svc.GetByID(z.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ProviderZoneID != "cfzone1" {
		t.Fatalf("persisted zone = %+v", got)
	}
}

func TestHostedZone_Create_ZoneNotFound_Errors(t *testing.T) {
	svc, d := newTestHostedZoneService(t)
	d.findZoneFound = false

	z, err := svc.Create("missing.com", d.credID, d.userID)
	if err == nil && z.Status != models.DNSZoneStatusError {
		t.Fatal("expected zone-not-found error/status")
	}
}

func TestHostedZone_Create_ZoneNotFound_PersistsErrorRow(t *testing.T) {
	svc, d := newTestHostedZoneService(t)
	d.findZoneFound = false

	z, err := svc.Create("missing.com", d.credID, d.userID)
	if err != nil {
		t.Fatalf("expected a persisted error-status row, got err: %v", err)
	}
	if z.Status != models.DNSZoneStatusError || z.StatusMessage == "" {
		t.Fatalf("got %+v", z)
	}
	if z.ProviderZoneID != "" {
		t.Fatalf("not-found zone must not cache a provider zone id, got %+v", z)
	}

	// The failed zone must still be visible on the list.
	zones, err := svc.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(zones) != 1 || zones[0].ID != z.ID {
		t.Fatalf("List() = %+v, want the failed zone", zones)
	}
}

func TestHostedZone_Create_UnknownCredential_NoRowPersisted(t *testing.T) {
	svc, d := newTestHostedZoneService(t)

	_, err := svc.Create("example.com", uuid.New(), d.userID)
	if err == nil {
		t.Fatal("expected an error for an unknown credential")
	}
	zones, err := svc.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(zones) != 0 {
		t.Fatalf("List() = %+v, want no row persisted", zones)
	}
}

func TestHostedZone_GetByID_NotFound(t *testing.T) {
	svc, _ := newTestHostedZoneService(t)

	_, err := svc.GetByID(uuid.New())
	if !errors.Is(err, ErrHostedZoneNotFound) {
		t.Fatalf("want ErrHostedZoneNotFound, got %v", err)
	}
}

func TestHostedZone_Delete_InUse_Rejected(t *testing.T) {
	svc, d := newTestHostedZoneService(t)
	d.recordsForZone = 1

	if err := svc.Delete(d.zoneID); !errors.Is(err, ErrDNSHostedZoneInUse) {
		t.Fatalf("want ErrDNSHostedZoneInUse, got %v", err)
	}
}

func TestHostedZone_Delete_NotInUse_Succeeds(t *testing.T) {
	svc, d := newTestHostedZoneService(t)
	d.findZoneID, d.findZoneFound = "cfzone1", true

	z, err := svc.Create("example.com", d.credID, d.userID)
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.Delete(z.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := svc.GetByID(z.ID); !errors.Is(err, ErrHostedZoneNotFound) {
		t.Fatalf("zone should be gone, got err=%v", err)
	}
}
