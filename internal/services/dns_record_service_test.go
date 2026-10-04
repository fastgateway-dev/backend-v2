package services_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// --- fakeCertApplier -------------------------------------------------------
//
// A hand-rolled services.CertInfraApplier fake (rather than the generated
// mocks.MockCertInfraApplier) because this service's tests need behavior the
// generated mock can't express cheaply: Get must branch per-GVR (Gateway vs
// DNSEndpoint) and, for DNSEndpoint, optionally echo back the last-applied
// object so endpointMatches can be exercised (syncing -> ready).

var errFakeNotFound = errors.New("fakeCertApplier: not found")

type fakeCertApplier struct {
	namespace      string
	gatewayAddrsFn func() []map[string]interface{}
	echoApplied    bool

	// deleteErr, when set, is what Delete returns instead of recording a
	// successful delete -- used to simulate a real (non-NotFound) k8s
	// delete failure, or a NotFound the service must tolerate.
	deleteErr error

	appliedByGVR  map[schema.GroupVersionResource]*unstructured.Unstructured
	appliedCounts map[schema.GroupVersionResource]int
	deletedCounts map[schema.GroupVersionResource]int
}

func newFakeCertApplier() *fakeCertApplier {
	return &fakeCertApplier{
		namespace:     "fastgateway-system",
		appliedByGVR:  map[schema.GroupVersionResource]*unstructured.Unstructured{},
		appliedCounts: map[schema.GroupVersionResource]int{},
		deletedCounts: map[schema.GroupVersionResource]int{},
	}
}

func (f *fakeCertApplier) ApplyClusterScoped(ctx context.Context, gvr schema.GroupVersionResource, obj *unstructured.Unstructured) error {
	return f.ApplyNamespaced(ctx, gvr, obj)
}

func (f *fakeCertApplier) ApplyNamespaced(ctx context.Context, gvr schema.GroupVersionResource, obj *unstructured.Unstructured) error {
	f.appliedByGVR[gvr] = obj
	f.appliedCounts[gvr]++
	return nil
}

func (f *fakeCertApplier) Get(ctx context.Context, gvr schema.GroupVersionResource, name string, namespaced bool) (*unstructured.Unstructured, error) {
	if gvr == kubernetes.GatewayGVR {
		var addrs []map[string]interface{}
		if f.gatewayAddrsFn != nil {
			addrs = f.gatewayAddrsFn()
		}
		list := make([]interface{}, len(addrs))
		for i, a := range addrs {
			list[i] = a
		}
		return &unstructured.Unstructured{Object: map[string]interface{}{
			"status": map[string]interface{}{"addresses": list},
		}}, nil
	}
	if gvr == kubernetes.DNSEndpointGVR && f.echoApplied {
		if obj, ok := f.appliedByGVR[gvr]; ok {
			return obj, nil
		}
	}
	return nil, errFakeNotFound
}

func (f *fakeCertApplier) Delete(ctx context.Context, gvr schema.GroupVersionResource, name string, namespaced bool) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deletedCounts[gvr]++
	delete(f.appliedByGVR, gvr)
	return nil
}

func (f *fakeCertApplier) Namespace() string { return f.namespace }

func (f *fakeCertApplier) appliedCount(gvr schema.GroupVersionResource) int {
	return f.appliedCounts[gvr]
}
func (f *fakeCertApplier) deletedCount(gvr schema.GroupVersionResource) int {
	return f.deletedCounts[gvr]
}

var _ services.CertInfraApplier = (*fakeCertApplier)(nil)

// DNSEndpointGVRResource/SecretGVRResource are tiny test-local aliases so
// assertions read as plain GVR lookups without extra imports clutter at call
// sites.
func DNSEndpointGVRResource() schema.GroupVersionResource { return kubernetes.DNSEndpointGVR }
func SecretGVRResource() schema.GroupVersionResource      { return kubernetes.SecretGVR }

// --- fakeDNSRecordRepo ------------------------------------------------------
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

func (f *fakeDNSRecordRepo) CountByCredential(credID uuid.UUID) (int64, error) {
	if f.rec != nil && f.rec.ProviderCredentialID == credID {
		return 1, nil
	}
	return 0, nil
}

var _ repository.DomainDNSRecordRepositoryInterface = (*fakeDNSRecordRepo)(nil)

// --- test harness ------------------------------------------------------------

// testDNSRecordDeps bundles the fakes/mocks newTestDNSRecordService wires
// up, and doubles as the services.SystemSettingsStore DNSInfraService needs
// (GetActiveDNSCredentialID/SetActiveDNSCredentialID below), so tests can
// flip d.activeCredOK directly without a separate settings fake to keep in
// sync.
type testDNSRecordDeps struct {
	domainID   uuid.UUID
	userID     uuid.UUID
	domain     *models.Domain
	domainRepo *mocks.MockDomainRepository
	recRepo    *fakeDNSRecordRepo
	applier    *fakeCertApplier

	activeCredID uuid.UUID
	activeCredOK bool

	// gatewayAddresses is read by the fake applier's Gateway Get; nil means
	// "no address resolved yet" (pending). Set via setGatewayIP.
	gatewayAddresses []map[string]interface{}
}

func (d *testDNSRecordDeps) setGatewayIP(ip string) {
	d.gatewayAddresses = []map[string]interface{}{{"type": "IPAddress", "value": ip}}
}

// GetActiveDNSCredentialID/SetActiveDNSCredentialID satisfy
// services.SystemSettingsStore.
func (d *testDNSRecordDeps) GetActiveDNSCredentialID() (*uuid.UUID, error) {
	if !d.activeCredOK {
		return nil, nil
	}
	id := d.activeCredID
	return &id, nil
}

func (d *testDNSRecordDeps) SetActiveDNSCredentialID(id *uuid.UUID) error {
	d.activeCredOK = id != nil
	if id != nil {
		d.activeCredID = *id
	}
	return nil
}

var _ services.SystemSettingsStore = (*testDNSRecordDeps)(nil)

// noopDNSCredReader satisfies services.DNSCredentialReader; DNSInfraService
// requires a non-nil Creds dependency, but these tests never exercise
// SetActiveCredential (only GetActiveCredentialID, via the settings fake).
type noopDNSCredReader struct{}

func (noopDNSCredReader) DecryptedCredentials(id uuid.UUID) (string, map[string]string, error) {
	return "", nil, errors.New("noopDNSCredReader: not implemented")
}

func randomUUID() uuid.UUID { return uuid.New() }

// newTestDNSRecordService builds a services.DNSRecordService wired to a
// mocked DomainRepository (Task 1/domain), a hand-rolled in-memory
// DomainDNSRecord repo (Task 6), a real DNSInfraService (Task 7, concrete
// per the controller resolution) backed by the same fake applier, and the
// fake CertInfraApplier itself for the gateway/DNSEndpoint control-plane
// calls.
func newTestDNSRecordService(t *testing.T) (*services.DNSRecordService, *testDNSRecordDeps) {
	t.Helper()

	domainID := uuid.New()
	userID := uuid.New()

	domain := &models.Domain{
		ID:             domainID,
		Hostname:       "app.example.com",
		K8sGatewayName: "gw-" + domainID.String()[:8],
	}

	domainRepo := new(mocks.MockDomainRepository)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	applier := newFakeCertApplier()

	deps := &testDNSRecordDeps{
		domainID:     domainID,
		userID:       userID,
		domain:       domain,
		domainRepo:   domainRepo,
		recRepo:      newFakeDNSRecordRepo(),
		applier:      applier,
		activeCredID: uuid.New(),
		activeCredOK: true,
	}
	applier.gatewayAddrsFn = func() []map[string]interface{} { return deps.gatewayAddresses }

	infra := services.NewDNSInfraService(services.DNSInfraServiceDeps{
		Creds:        noopDNSCredReader{},
		Settings:     deps,
		ControlPlane: applier,
	})

	svc := services.NewDNSRecordService(services.DNSRecordServiceDeps{
		Repo:         deps.recRepo,
		DomainRepo:   domainRepo,
		Infra:        infra,
		ControlPlane: applier,
	})

	return svc, deps
}

// --- tests -------------------------------------------------------------------

func TestEnable_NoActiveCredential_Errors(t *testing.T) {
	svc, d := newTestDNSRecordService(t)
	d.activeCredOK = false
	_, err := svc.Enable(d.domainID, d.userID, services.DNSRecordInput{})
	if err == nil {
		t.Fatal("expected ErrNoActiveDNSCredential")
	}
	require.ErrorIs(t, err, services.ErrNoActiveDNSCredential)
}

func TestEnable_CredentialMismatch_Errors(t *testing.T) {
	svc, d := newTestDNSRecordService(t)
	other := randomUUID()
	_, err := svc.Enable(d.domainID, d.userID, services.DNSRecordInput{ProviderCredentialID: &other})
	if err == nil {
		t.Fatal("expected mismatch error (Review Focus #3)")
	}
	require.ErrorIs(t, err, services.ErrCredentialNotActive)
}

func TestEnable_NoGatewayAddress_Pending(t *testing.T) {
	svc, d := newTestDNSRecordService(t)
	d.gatewayAddresses = nil // no address yet
	rec, err := svc.Enable(d.domainID, d.userID, services.DNSRecordInput{})
	if err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if rec.Status != models.DNSRecordStatusPending {
		t.Fatalf("status = %s, want pending (Review Focus #1)", rec.Status)
	}
	if d.applier.appliedCount(DNSEndpointGVRResource()) != 0 {
		t.Fatal("must not create a target-less DNSEndpoint")
	}
}

func TestEnable_WithAddress_SyncingThenReady(t *testing.T) {
	svc, d := newTestDNSRecordService(t)
	d.setGatewayIP("203.0.113.9")
	rec, err := svc.Enable(d.domainID, d.userID, services.DNSRecordInput{})
	if err != nil {
		t.Fatalf("Enable: %v", err)
	}
	// DNSEndpoint applied with the A target; first pass reports syncing
	// (the fake applier doesn't echo it back yet).
	if rec.Status != models.DNSRecordStatusSyncing && rec.Status != models.DNSRecordStatusReady {
		t.Fatalf("status = %s", rec.Status)
	}
	require.Equal(t, 1, d.applier.appliedCount(DNSEndpointGVRResource()))

	// make the fake return the applied endpoint on Get; a subsequent Get => ready
	d.applier.echoApplied = true
	got, err := svc.Get(d.domainID)
	require.NoError(t, err)
	if got.Status != models.DNSRecordStatusReady {
		t.Fatalf("status after reconcile = %s, want ready", got.Status)
	}
	if got.ResolvedTarget != "203.0.113.9" {
		t.Fatalf("resolvedTarget = %q", got.ResolvedTarget)
	}
}

func TestEnable_ForcedCNAMEOnIP_Error(t *testing.T) {
	svc, d := newTestDNSRecordService(t)
	d.setGatewayIP("203.0.113.9")
	rec, err := svc.Enable(d.domainID, d.userID, services.DNSRecordInput{RecordType: models.DNSRecordTypeCNAME})
	require.NoError(t, err, "Enable itself must not fail -- the conflict is recorded on the record's status")
	if rec.Status != models.DNSRecordStatusError {
		t.Fatalf("status = %s, want error (Review Focus #2)", rec.Status)
	}
	require.NotEmpty(t, rec.StatusMessage)
	// No DNSEndpoint should have been applied for an invalid combination.
	require.Equal(t, 0, d.applier.appliedCount(DNSEndpointGVRResource()))
}

func TestDelete_RemovesDNSEndpoint(t *testing.T) {
	svc, d := newTestDNSRecordService(t)
	d.setGatewayIP("203.0.113.9")
	_, err := svc.Enable(d.domainID, d.userID, services.DNSRecordInput{})
	require.NoError(t, err)
	if err := svc.Delete(d.domainID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if d.applier.deletedCount(DNSEndpointGVRResource()) != 1 {
		t.Fatal("DNSEndpoint must be deleted (Review Focus #4)")
	}
	// The record row itself must be gone too.
	_, err = svc.Get(d.domainID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestDelete_NoRecord_NoOp(t *testing.T) {
	svc, d := newTestDNSRecordService(t)
	require.NoError(t, svc.Delete(d.domainID))
	require.Equal(t, 0, d.applier.deletedCount(DNSEndpointGVRResource()))
}

// TestDelete_ApplierFails_KeepsRow guards against orphaning: if the
// DNSEndpoint delete fails for a real reason (not NotFound), Delete must
// return that error and must NOT delete the backend row, so the DB row and
// the live DNSEndpoint stay in sync and the caller can retry.
func TestDelete_ApplierFails_KeepsRow(t *testing.T) {
	svc, d := newTestDNSRecordService(t)
	d.setGatewayIP("203.0.113.9")
	_, err := svc.Enable(d.domainID, d.userID, services.DNSRecordInput{})
	require.NoError(t, err)

	d.applier.deleteErr = errors.New("k8s: delete failed")
	err = svc.Delete(d.domainID)
	require.Error(t, err)
	require.ErrorIs(t, err, d.applier.deleteErr)

	// The row must still be there -- not orphaned.
	require.Equal(t, 0, d.applier.deletedCount(DNSEndpointGVRResource()))
	_, err = svc.Get(d.domainID)
	require.NoError(t, err)
}

// TestDelete_ApplierNotFound_StillDeletesRow is the flip side: a NotFound
// from the DNSEndpoint delete (it was never applied, or already removed)
// must not block removing the backend row.
func TestDelete_ApplierNotFound_StillDeletesRow(t *testing.T) {
	svc, d := newTestDNSRecordService(t)
	d.setGatewayIP("203.0.113.9")
	_, err := svc.Enable(d.domainID, d.userID, services.DNSRecordInput{})
	require.NoError(t, err)

	d.applier.deleteErr = apierrors.NewNotFound(
		schema.GroupResource{Group: "externaldns.k8s.io", Resource: "dnsendpoints"},
		"dns-already-gone",
	)
	require.NoError(t, svc.Delete(d.domainID))

	_, err = svc.Get(d.domainID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestUpdate_ChangesSettingsAndReconciles(t *testing.T) {
	svc, d := newTestDNSRecordService(t)
	d.setGatewayIP("203.0.113.9")
	_, err := svc.Enable(d.domainID, d.userID, services.DNSRecordInput{})
	require.NoError(t, err)

	ttl := 120
	got, err := svc.Update(d.domainID, services.DNSRecordInput{RecordType: models.DNSRecordTypeA, TTL: &ttl, Proxied: true})
	require.NoError(t, err)
	require.Equal(t, models.DNSRecordTypeA, got.RecordType)
	require.NotNil(t, got.TTL)
	require.Equal(t, 120, *got.TTL)
	require.True(t, got.Proxied)
	// Reconcile re-ran and re-applied the DNSEndpoint with the new settings.
	require.GreaterOrEqual(t, d.applier.appliedCount(DNSEndpointGVRResource()), 2)
}

func TestUpdate_CredentialMismatch_Errors(t *testing.T) {
	svc, d := newTestDNSRecordService(t)
	d.setGatewayIP("203.0.113.9")
	_, err := svc.Enable(d.domainID, d.userID, services.DNSRecordInput{})
	require.NoError(t, err)

	other := randomUUID()
	_, err = svc.Update(d.domainID, services.DNSRecordInput{ProviderCredentialID: &other})
	require.ErrorIs(t, err, services.ErrCredentialNotActive)
}
