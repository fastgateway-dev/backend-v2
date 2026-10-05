package services_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// --- fakeDNSRecordRepo -------------------------------------------------------
//
// A minimal in-memory repository.DomainDNSRecordRepositoryInterface: the
// production repo is a thin GORM wrapper (Task 6), and the service's own
// behavior -- not persistence plumbing -- is what these tests exercise.

type fakeDNSRecordRepo struct {
	rec *models.DomainDNSRecord
}

func newFakeDNSRecordRepo() *fakeDNSRecordRepo { return &fakeDNSRecordRepo{} }

func (f *fakeDNSRecordRepo) Create(rec *models.DomainDNSRecord) error {
	rec.ID = uuid.New()
	f.rec = rec
	return nil
}

func (f *fakeDNSRecordRepo) GetByDomainID(domainID uuid.UUID) (*models.DomainDNSRecord, error) {
	if f.rec == nil || f.rec.DomainID != domainID {
		return nil, gorm.ErrRecordNotFound
	}
	return f.rec, nil
}

func (f *fakeDNSRecordRepo) Update(rec *models.DomainDNSRecord) error {
	f.rec = rec
	return nil
}

func (f *fakeDNSRecordRepo) DeleteByDomainID(domainID uuid.UUID) error {
	if f.rec != nil && f.rec.DomainID == domainID {
		f.rec = nil
	}
	return nil
}

func (f *fakeDNSRecordRepo) CountByZone(zoneID uuid.UUID) (int64, error) {
	if f.rec != nil && f.rec.HostedZoneID == zoneID {
		return 1, nil
	}
	return 0, nil
}

var _ repository.DomainDNSRecordRepositoryInterface = (*fakeDNSRecordRepo)(nil)

// noopDNSCredReader satisfies services.DNSCredentialReader; the stub
// reconcile never calls it yet, but NewDNSRecordService requires a non-nil
// Creds dependency.
type noopDNSCredReader struct{}

func (noopDNSCredReader) DecryptedCredentials(id uuid.UUID) (string, map[string]string, error) {
	return "", nil, errors.New("noopDNSCredReader: not implemented")
}

// testDNSRecordDeps bundles the fakes/mocks newTestDNSRecordService wires up.
type testDNSRecordDeps struct {
	domainID uuid.UUID
	userID   uuid.UUID
	zoneID   uuid.UUID

	domainRepo *mocks.MockDomainRepository
	zoneRepo   *mocks.MockDNSHostedZoneRepository
	recRepo    *fakeDNSRecordRepo
}

// newTestDNSRecordService builds a services.DNSRecordService wired to a
// mocked DomainRepository, a mocked DNSHostedZoneRepository, a hand-rolled
// in-memory DomainDNSRecord repo, and a no-op credential reader. The
// control-plane applier is a mock too, since the stub reconcile never calls
// it.
func newTestDNSRecordService(t *testing.T) (*services.DNSRecordService, *testDNSRecordDeps) {
	t.Helper()

	domainID := uuid.New()
	userID := uuid.New()
	zoneID := uuid.New()

	domainRepo := new(mocks.MockDomainRepository)
	zoneRepo := new(mocks.MockDNSHostedZoneRepository)
	applier := new(mocks.MockCertInfraApplier)

	svc := services.NewDNSRecordService(services.DNSRecordServiceDeps{
		Repo:         newFakeDNSRecordRepo(),
		DomainRepo:   domainRepo,
		ZoneRepo:     zoneRepo,
		Creds:        noopDNSCredReader{},
		ControlPlane: applier,
	})

	return svc, &testDNSRecordDeps{
		domainID:   domainID,
		userID:     userID,
		zoneID:     zoneID,
		domainRepo: domainRepo,
		zoneRepo:   zoneRepo,
		recRepo:    nil,
	}
}

// --- tests -------------------------------------------------------------------

func TestEnable_RequiresHostedZone(t *testing.T) {
	svc, d := newTestDNSRecordService(t)
	_, err := svc.Enable(d.domainID, d.userID, services.DNSRecordInput{})
	require.ErrorIs(t, err, services.ErrNoHostedZone)
}

func TestEnable_CreatesPending(t *testing.T) {
	svc, d := newTestDNSRecordService(t)
	rec, err := svc.Enable(d.domainID, d.userID, services.DNSRecordInput{HostedZoneID: &d.zoneID})
	require.NoError(t, err)
	require.Equal(t, d.zoneID, rec.HostedZoneID)
	require.Equal(t, models.DNSRecordStatusPending, rec.Status)
}

func TestEnable_InvalidRecordType_Errors(t *testing.T) {
	svc, d := newTestDNSRecordService(t)
	_, err := svc.Enable(d.domainID, d.userID, services.DNSRecordInput{
		HostedZoneID: &d.zoneID,
		RecordType:   models.DNSRecordType("TXT"),
	})
	require.ErrorIs(t, err, services.ErrInvalidRecordType)

	// No record must have been persisted for the rejected input.
	_, err = svc.Get(d.domainID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestEnable_AlreadyExists_Errors(t *testing.T) {
	svc, d := newTestDNSRecordService(t)
	_, err := svc.Enable(d.domainID, d.userID, services.DNSRecordInput{HostedZoneID: &d.zoneID})
	require.NoError(t, err)

	_, err = svc.Enable(d.domainID, d.userID, services.DNSRecordInput{HostedZoneID: &d.zoneID})
	require.ErrorIs(t, err, services.ErrDNSRecordExists)
}

func TestUpdate_ChangesSettings(t *testing.T) {
	svc, d := newTestDNSRecordService(t)
	_, err := svc.Enable(d.domainID, d.userID, services.DNSRecordInput{HostedZoneID: &d.zoneID})
	require.NoError(t, err)

	ttl := 120
	got, err := svc.Update(d.domainID, services.DNSRecordInput{RecordType: models.DNSRecordTypeA, TTL: &ttl, Proxied: true})
	require.NoError(t, err)
	require.Equal(t, models.DNSRecordTypeA, got.RecordType)
	require.NotNil(t, got.TTL)
	require.Equal(t, 120, *got.TTL)
	require.True(t, got.Proxied)
}

func TestUpdate_InvalidRecordType_Errors(t *testing.T) {
	svc, d := newTestDNSRecordService(t)
	_, err := svc.Enable(d.domainID, d.userID, services.DNSRecordInput{HostedZoneID: &d.zoneID})
	require.NoError(t, err)

	_, err = svc.Update(d.domainID, services.DNSRecordInput{RecordType: models.DNSRecordType("foo")})
	require.ErrorIs(t, err, services.ErrInvalidRecordType)
}

func TestDelete_RemovesRow(t *testing.T) {
	svc, d := newTestDNSRecordService(t)
	_, err := svc.Enable(d.domainID, d.userID, services.DNSRecordInput{HostedZoneID: &d.zoneID})
	require.NoError(t, err)

	require.NoError(t, svc.Delete(d.domainID))

	_, err = svc.Get(d.domainID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestDelete_NoRecord_NoOp(t *testing.T) {
	svc, d := newTestDNSRecordService(t)
	require.NoError(t, svc.Delete(d.domainID))
}
