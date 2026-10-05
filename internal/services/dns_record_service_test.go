package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/fastgateway-dev/backend-v2/internal/dnsprovider"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
)

// This is an internal (package services) test so it can install a fake behind
// the dnsProviderLookup seam -- the real dnsprovider registry only holds
// live SDK-backed providers, so a unit test must substitute a fake client.
// mocks.* cannot be used here: internal/mocks imports internal/services, so a
// package-services test importing mocks would be an import cycle. Everything
// is therefore hand-rolled, mostly via interface embedding.

// --- fakeRecRepo -------------------------------------------------------------

type fakeRecRepo struct {
	rec *models.DomainDNSRecord
}

func (f *fakeRecRepo) Create(rec *models.DomainDNSRecord) error {
	rec.ID = uuid.New()
	f.rec = rec
	return nil
}

func (f *fakeRecRepo) GetByDomainID(domainID uuid.UUID) (*models.DomainDNSRecord, error) {
	if f.rec == nil || f.rec.DomainID != domainID {
		return nil, gorm.ErrRecordNotFound
	}
	return f.rec, nil
}

func (f *fakeRecRepo) Update(rec *models.DomainDNSRecord) error {
	f.rec = rec
	return nil
}

func (f *fakeRecRepo) DeleteByDomainID(domainID uuid.UUID) error {
	if f.rec != nil && f.rec.DomainID == domainID {
		f.rec = nil
	}
	return nil
}

func (f *fakeRecRepo) CountByZone(zoneID uuid.UUID) (int64, error) {
	if f.rec != nil && f.rec.HostedZoneID == zoneID {
		return 1, nil
	}
	return 0, nil
}

var _ repository.DomainDNSRecordRepositoryInterface = (*fakeRecRepo)(nil)

// --- recDomainRepo / recZoneRepo (interface-embedding fakes) ----------------

type recDomainRepo struct {
	repository.DomainRepositoryInterface
	domain *models.Domain
	err    error
}

func (r *recDomainRepo) GetByID(uuid.UUID) (*models.Domain, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.domain, nil
}

type recZoneRepo struct {
	repository.DNSHostedZoneRepositoryInterface
	zone *models.DNSHostedZone
	err  error
}

func (r *recZoneRepo) GetByID(uuid.UUID) (*models.DNSHostedZone, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.zone, nil
}

// --- recApplier (CertInfraApplier embedding fake) ---------------------------
//
// Only Get is exercised by DNSRecordService; it returns a Gateway whose
// status.addresses is driven by addrValue (empty => no address assigned).

type recApplier struct {
	CertInfraApplier
	addrValue string // "" => no load-balancer address yet
	getErr    error
}

func (a *recApplier) Get(_ context.Context, _ schema.GroupVersionResource, _ string, _ bool) (*unstructured.Unstructured, error) {
	if a.getErr != nil {
		return nil, a.getErr
	}
	obj := &unstructured.Unstructured{Object: map[string]interface{}{}}
	if a.addrValue != "" {
		_ = unstructured.SetNestedSlice(obj.Object, []interface{}{
			map[string]interface{}{"value": a.addrValue},
		}, "status", "addresses")
	}
	return obj, nil
}

// --- recCredReader ----------------------------------------------------------

type recCredReader struct {
	providerType string
	creds        map[string]string
	err          error
}

func (r recCredReader) DecryptedCredentials(uuid.UUID) (string, map[string]string, error) {
	if r.err != nil {
		return "", nil, r.err
	}
	return r.providerType, r.creds, nil
}

// --- recDNSClient / recDNSProvider (behind dnsProviderLookup) ---------------

type recDNSClient struct {
	getFound  bool
	getErr    error
	upsertErr error
	deleteErr error

	upsertCalls    int
	deleteCalls    int
	getCalls       int
	lastUpsert     dnsprovider.Record
	lastDeleteType string
}

func (c *recDNSClient) FindZone(context.Context, string) (string, bool, error) {
	return "", false, nil
}

func (c *recDNSClient) GetRecord(_ context.Context, _, _, _ string) (dnsprovider.Record, bool, error) {
	c.getCalls++
	if c.getErr != nil {
		return dnsprovider.Record{}, false, c.getErr
	}
	return dnsprovider.Record{}, c.getFound, nil
}

func (c *recDNSClient) UpsertRecord(_ context.Context, _ string, r dnsprovider.Record) error {
	c.upsertCalls++
	c.lastUpsert = r
	return c.upsertErr
}

func (c *recDNSClient) DeleteRecord(_ context.Context, _, _, recordType string) error {
	c.deleteCalls++
	c.lastDeleteType = recordType
	return c.deleteErr
}

type recDNSProvider struct {
	client *recDNSClient
}

func (recDNSProvider) Type() string                     { return "faketest" }
func (recDNSProvider) RequiredFields() []string         { return nil }
func (recDNSProvider) Validate(map[string]string) error { return nil }
func (p recDNSProvider) NewClient(map[string]string) (dnsprovider.DNSClient, error) {
	return p.client, nil
}

// installFakeProvider swaps dnsProviderLookup so the given providerType
// resolves to a provider backed by client, restoring the original on cleanup.
func installFakeProvider(t *testing.T, providerType string, client *recDNSClient) {
	t.Helper()
	orig := dnsProviderLookup
	dnsProviderLookup = func(pt string) (dnsprovider.DNSProvider, bool) {
		if pt == providerType {
			return recDNSProvider{client: client}, true
		}
		return nil, false
	}
	t.Cleanup(func() { dnsProviderLookup = orig })
}

// --- harness -----------------------------------------------------------------

type recTestHarness struct {
	svc      *DNSRecordService
	domainID uuid.UUID
	userID   uuid.UUID
	zoneID   uuid.UUID
	repo     *fakeRecRepo
	applier  *recApplier
	client   *recDNSClient
}

// newRecHarness wires a DNSRecordService with: a domain "app.example.com" in
// hosted zone "example.com", a gateway address driven by addrValue, a
// credential reader returning providerType, and a fake DNS client installed
// behind dnsProviderLookup.
func newRecHarness(t *testing.T, hostname, zoneName, addrValue, providerType string, client *recDNSClient) *recTestHarness {
	t.Helper()
	installFakeProvider(t, providerType, client)

	// Shrink Enable's bounded-wait poll so pending-path tests don't sleep ~5s.
	origInterval := enablePollInterval
	enablePollInterval = 1 * time.Millisecond
	t.Cleanup(func() { enablePollInterval = origInterval })

	domainID := uuid.New()
	zoneID := uuid.New()
	credID := uuid.New()

	repo := &fakeRecRepo{}
	applier := &recApplier{addrValue: addrValue}

	svc := NewDNSRecordService(DNSRecordServiceDeps{
		Repo: repo,
		DomainRepo: &recDomainRepo{domain: &models.Domain{
			Hostname:       hostname,
			K8sGatewayName: "gw-" + domainID.String(),
			Namespace:      "fastgateway-system",
		}},
		ZoneRepo: &recZoneRepo{zone: &models.DNSHostedZone{
			Name:                 zoneName,
			ProviderZoneID:       "zone123",
			ProviderCredentialID: credID,
		}},
		Creds:        recCredReader{providerType: providerType, creds: map[string]string{"token": "x"}},
		ControlPlane: applier,
	})

	return &recTestHarness{
		svc: svc, domainID: domainID, userID: uuid.New(), zoneID: zoneID,
		repo: repo, applier: applier, client: client,
	}
}

// --- tests -------------------------------------------------------------------

func TestEnable_RequiresHostedZone(t *testing.T) {
	h := newRecHarness(t, "app.example.com", "example.com", "203.0.113.5", "faketest", &recDNSClient{})
	_, err := h.svc.Enable(h.domainID, h.userID, DNSRecordInput{})
	require.ErrorIs(t, err, ErrNoHostedZone)
}

func TestEnable_InvalidRecordType_Errors(t *testing.T) {
	h := newRecHarness(t, "app.example.com", "example.com", "203.0.113.5", "faketest", &recDNSClient{})
	_, err := h.svc.Enable(h.domainID, h.userID, DNSRecordInput{HostedZoneID: &h.zoneID, RecordType: models.DNSRecordType("TXT")})
	require.ErrorIs(t, err, ErrInvalidRecordType)
	// Nothing persisted for the rejected input.
	_, err = h.svc.repo.GetByDomainID(h.domainID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestEnable_AlreadyExists_Errors(t *testing.T) {
	h := newRecHarness(t, "app.example.com", "example.com", "203.0.113.5", "faketest", &recDNSClient{})
	_, err := h.svc.Enable(h.domainID, h.userID, DNSRecordInput{HostedZoneID: &h.zoneID})
	require.NoError(t, err)
	_, err = h.svc.Enable(h.domainID, h.userID, DNSRecordInput{HostedZoneID: &h.zoneID})
	require.ErrorIs(t, err, ErrDNSRecordExists)
}

func TestEnable_NoGatewayAddress_PendingNoWrite(t *testing.T) {
	// addrValue "" => the gateway has no load-balancer address yet.
	client := &recDNSClient{}
	h := newRecHarness(t, "app.example.com", "example.com", "", "faketest", client)

	rec, err := h.svc.Enable(h.domainID, h.userID, DNSRecordInput{HostedZoneID: &h.zoneID})
	require.NoError(t, err)
	require.Equal(t, models.DNSRecordStatusPending, rec.Status)
	require.Empty(t, rec.ResolvedTarget)
	require.Equal(t, 0, client.upsertCalls, "no provider write when the address is unresolved")
	require.Equal(t, 0, client.getCalls, "no clobber check when the address is unresolved")
}

func TestEnable_ForeignRecord_ClobberError(t *testing.T) {
	// A record already exists at the provider on first write => clobber.
	client := &recDNSClient{getFound: true}
	h := newRecHarness(t, "app.example.com", "example.com", "203.0.113.5", "faketest", client)

	rec, err := h.svc.Enable(h.domainID, h.userID, DNSRecordInput{HostedZoneID: &h.zoneID})
	require.NoError(t, err)
	require.Equal(t, models.DNSRecordStatusError, rec.Status)
	require.Contains(t, rec.StatusMessage, "not managed by FastGateway")
	require.Equal(t, 1, client.getCalls)
	require.Equal(t, 0, client.upsertCalls, "clobber must not write")
	require.Empty(t, rec.ResolvedTarget)
}

func TestEnable_ApexCNAME_Error(t *testing.T) {
	// Hostname == zone apex and the gateway address is a hostname => CNAME at
	// apex, which is rejected.
	client := &recDNSClient{}
	h := newRecHarness(t, "example.com", "example.com", "lb.example.com", "faketest", client)

	rec, err := h.svc.Enable(h.domainID, h.userID, DNSRecordInput{HostedZoneID: &h.zoneID})
	require.NoError(t, err)
	require.Equal(t, models.DNSRecordStatusError, rec.Status)
	require.Equal(t, ErrApexCNAME.Error(), rec.StatusMessage)
	require.Equal(t, 0, client.upsertCalls)
}

func TestEnable_HostedZoneMismatch_Error(t *testing.T) {
	// Hostname is not contained in the hosted zone.
	client := &recDNSClient{}
	h := newRecHarness(t, "app.other.com", "example.com", "203.0.113.5", "faketest", client)

	rec, err := h.svc.Enable(h.domainID, h.userID, DNSRecordInput{HostedZoneID: &h.zoneID})
	require.NoError(t, err)
	require.Equal(t, models.DNSRecordStatusError, rec.Status)
	require.Equal(t, ErrHostedZoneMismatch.Error(), rec.StatusMessage)
	require.Equal(t, 0, client.upsertCalls)
}

func TestEnable_WithAddress_UpsertsReady(t *testing.T) {
	client := &recDNSClient{} // getFound=false, no errors
	h := newRecHarness(t, "app.example.com", "example.com", "203.0.113.5", "faketest", client)

	rec, err := h.svc.Enable(h.domainID, h.userID, DNSRecordInput{HostedZoneID: &h.zoneID})
	require.NoError(t, err)
	require.Equal(t, models.DNSRecordStatusReady, rec.Status)
	require.Equal(t, "203.0.113.5", rec.ResolvedTarget)
	require.Equal(t, 1, client.upsertCalls, "ready path writes exactly once")
	require.Equal(t, "203.0.113.5", client.lastUpsert.Target)
	require.Equal(t, string(models.DNSRecordTypeA), client.lastUpsert.Type)
	require.Equal(t, "app.example.com", client.lastUpsert.Name)
}

func TestGet_ReadyUnchangedAddress_NoProviderCall(t *testing.T) {
	client := &recDNSClient{}
	h := newRecHarness(t, "app.example.com", "example.com", "203.0.113.5", "faketest", client)

	_, err := h.svc.Enable(h.domainID, h.userID, DNSRecordInput{HostedZoneID: &h.zoneID})
	require.NoError(t, err)
	require.Equal(t, 1, client.upsertCalls)

	// Reading a ready record whose gateway address is unchanged must not call
	// the provider again.
	rec, err := h.svc.Get(h.domainID)
	require.NoError(t, err)
	require.Equal(t, models.DNSRecordStatusReady, rec.Status)
	require.Equal(t, 1, client.upsertCalls, "no re-upsert on steady-state read")
	require.Equal(t, 1, client.getCalls, "no extra clobber check on steady-state read")
}

func TestGet_IPDrift_ReUpserts(t *testing.T) {
	client := &recDNSClient{}
	h := newRecHarness(t, "app.example.com", "example.com", "203.0.113.5", "faketest", client)

	_, err := h.svc.Enable(h.domainID, h.userID, DNSRecordInput{HostedZoneID: &h.zoneID})
	require.NoError(t, err)
	require.Equal(t, 1, client.upsertCalls)

	// Gateway address changes => drift => re-upsert (clobber check skipped as
	// the record is already owned).
	h.applier.addrValue = "203.0.113.99"
	rec, err := h.svc.Get(h.domainID)
	require.NoError(t, err)
	require.Equal(t, models.DNSRecordStatusReady, rec.Status)
	require.Equal(t, "203.0.113.99", rec.ResolvedTarget)
	require.Equal(t, 2, client.upsertCalls, "drift triggers a re-upsert")
	require.Equal(t, 1, client.getCalls, "owned record skips the clobber GetRecord")
}

func TestUpdate_ChangesSettings(t *testing.T) {
	client := &recDNSClient{}
	h := newRecHarness(t, "app.example.com", "example.com", "203.0.113.5", "faketest", client)
	_, err := h.svc.Enable(h.domainID, h.userID, DNSRecordInput{HostedZoneID: &h.zoneID})
	require.NoError(t, err)

	ttl := 120
	got, err := h.svc.Update(h.domainID, DNSRecordInput{RecordType: models.DNSRecordTypeA, TTL: &ttl, Proxied: true})
	require.NoError(t, err)
	require.Equal(t, models.DNSRecordTypeA, got.RecordType)
	require.NotNil(t, got.TTL)
	require.Equal(t, 120, *got.TTL)
	require.True(t, got.Proxied)
}

func TestUpdate_InvalidRecordType_Errors(t *testing.T) {
	h := newRecHarness(t, "app.example.com", "example.com", "203.0.113.5", "faketest", &recDNSClient{})
	_, err := h.svc.Enable(h.domainID, h.userID, DNSRecordInput{HostedZoneID: &h.zoneID})
	require.NoError(t, err)
	_, err = h.svc.Update(h.domainID, DNSRecordInput{RecordType: models.DNSRecordType("foo")})
	require.ErrorIs(t, err, ErrInvalidRecordType)
}

func TestReconcile_CloudflareProxied_ForcesAutoTTL(t *testing.T) {
	client := &recDNSClient{}
	h := newRecHarness(t, "app.example.com", "example.com", "203.0.113.5", "cloudflare", client)

	ttl := 300
	rec, err := h.svc.Enable(h.domainID, h.userID, DNSRecordInput{HostedZoneID: &h.zoneID, Proxied: true, TTL: &ttl})
	require.NoError(t, err)
	require.Equal(t, models.DNSRecordStatusReady, rec.Status)
	require.True(t, client.lastUpsert.Proxied)
	require.Nil(t, client.lastUpsert.TTL, "cloudflare proxied coerces TTL to auto (nil)")
}

func TestReconcile_ProxiedNonCloudflare_Ignored(t *testing.T) {
	client := &recDNSClient{}
	h := newRecHarness(t, "app.example.com", "example.com", "203.0.113.5", "faketest", client)

	ttl := 300
	rec, err := h.svc.Enable(h.domainID, h.userID, DNSRecordInput{HostedZoneID: &h.zoneID, Proxied: true, TTL: &ttl})
	require.NoError(t, err)
	require.Equal(t, models.DNSRecordStatusReady, rec.Status)
	require.False(t, client.lastUpsert.Proxied, "proxied is only honored for cloudflare")
	require.NotNil(t, client.lastUpsert.TTL)
	require.Equal(t, 300, *client.lastUpsert.TTL)
}

func TestDelete_NeverWritten_DeletesRowNoProviderCall(t *testing.T) {
	// ResolvedTarget == "" (never written) => delete the row, no provider call.
	client := &recDNSClient{}
	h := newRecHarness(t, "app.example.com", "example.com", "", "faketest", client)
	_, err := h.svc.Enable(h.domainID, h.userID, DNSRecordInput{HostedZoneID: &h.zoneID})
	require.NoError(t, err)
	require.Empty(t, h.repo.rec.ResolvedTarget)

	require.NoError(t, h.svc.Delete(h.domainID))
	require.Nil(t, h.repo.rec, "row removed")
	require.Equal(t, 0, client.deleteCalls, "nothing to delete at the provider")
}

func TestDelete_ProviderFails_KeepsRow(t *testing.T) {
	client := &recDNSClient{}
	h := newRecHarness(t, "app.example.com", "example.com", "203.0.113.5", "faketest", client)
	_, err := h.svc.Enable(h.domainID, h.userID, DNSRecordInput{HostedZoneID: &h.zoneID})
	require.NoError(t, err)
	require.Equal(t, "203.0.113.5", h.repo.rec.ResolvedTarget) // owned

	client.deleteErr = errors.New("provider boom")
	err = h.svc.Delete(h.domainID)
	require.Error(t, err)
	require.Equal(t, 1, client.deleteCalls)
	require.Equal(t, string(models.DNSRecordTypeA), client.lastDeleteType, "delete targets the resolved type")
	require.NotNil(t, h.repo.rec, "row kept on a real provider-delete failure")
}

func TestDelete_NotFound_DeletesRow(t *testing.T) {
	// Provider DeleteRecord returns nil when the record is absent, so Delete
	// removes the row.
	client := &recDNSClient{} // deleteErr nil
	h := newRecHarness(t, "app.example.com", "example.com", "203.0.113.5", "faketest", client)
	_, err := h.svc.Enable(h.domainID, h.userID, DNSRecordInput{HostedZoneID: &h.zoneID})
	require.NoError(t, err)
	require.Equal(t, "203.0.113.5", h.repo.rec.ResolvedTarget)

	require.NoError(t, h.svc.Delete(h.domainID))
	require.Equal(t, 1, client.deleteCalls)
	require.Nil(t, h.repo.rec, "row deleted after a successful provider delete")
}

func TestDelete_AutoType_DeletesResolvedType(t *testing.T) {
	// Regression: the stored RecordType is the user input, which defaults to
	// "auto". reconcile resolves auto->A/AAAA/CNAME only locally and never
	// persists it, so Delete must recompute the effective type from
	// ResolvedTarget -- deleting by "auto" would match nothing and orphan the
	// live record.
	client := &recDNSClient{}
	h := newRecHarness(t, "app.example.com", "example.com", "203.0.113.5", "faketest", client)

	// Empty/auto input type against an IPv4 gateway.
	rec, err := h.svc.Enable(h.domainID, h.userID, DNSRecordInput{HostedZoneID: &h.zoneID})
	require.NoError(t, err)
	require.Equal(t, models.DNSRecordStatusReady, rec.Status)
	require.Equal(t, models.DNSRecordTypeAuto, rec.RecordType, "user intent stays auto (not mutated to A)")
	require.Equal(t, string(models.DNSRecordTypeA), client.lastUpsert.Type, "upsert wrote type A")

	require.NoError(t, h.svc.Delete(h.domainID))
	require.Equal(t, 1, client.deleteCalls)
	require.Equal(t, string(models.DNSRecordTypeA), client.lastDeleteType,
		"delete must target the resolved type A, not the stored auto")
	require.Nil(t, h.repo.rec, "row removed")
}

func TestDelete_NoRecord_NoOp(t *testing.T) {
	h := newRecHarness(t, "app.example.com", "example.com", "203.0.113.5", "faketest", &recDNSClient{})
	require.NoError(t, h.svc.Delete(h.domainID))
}
