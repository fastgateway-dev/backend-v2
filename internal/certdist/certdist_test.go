package certdist

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/fastgateway-dev/backend-v2/internal/cluster"
	"github.com/fastgateway-dev/backend-v2/internal/config"
	"github.com/fastgateway-dev/backend-v2/internal/models"
)

// =============================================================================
// Fakes
// =============================================================================

type fakeGate struct{ leader bool }

func (f fakeGate) IsLeader() bool { return f.leader }

// scriptedGate returns a scripted sequence of IsLeader results, one per
// call, holding the last entry for any call past the end of the sequence.
// Used to simulate leadership flipping to false mid-Reconcile-pass (e.g. the
// advisory-lock connection dying), which a single fixed fakeGate value
// can't express.
type scriptedGate struct {
	mu      sync.Mutex
	results []bool
	calls   int
}

func (g *scriptedGate) IsLeader() bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	idx := g.calls
	g.calls++

	if len(g.results) == 0 {
		return false
	}
	if idx >= len(g.results) {
		idx = len(g.results) - 1
	}
	return g.results[idx]
}

// fakeLister guards `called`/`calls` with a mutex because TestRun_* below
// drive it from a background goroutine (Distributor.Run) while the test
// goroutine polls it via require.Eventually -- every other test in this file
// only ever calls it synchronously, where the mutex is a no-op.
type fakeLister struct {
	mu sync.Mutex

	certs  []models.ManagedCertificate
	err    error
	called bool
	calls  int
}

func (f *fakeLister) ListByStatuses(statuses []models.ManagedCertStatus) ([]models.ManagedCertificate, error) {
	f.mu.Lock()
	f.called = true
	f.calls++
	certs, err := f.certs, f.err
	f.mu.Unlock()
	return certs, err
}

// callCount returns the number of ListByStatuses calls so far. Reconcile
// calls it exactly once per pass (before the not-leader short-circuit), so
// it doubles as "number of Reconcile passes that got past the leader
// check".
func (f *fakeLister) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type fakeDistRepo struct {
	byCert map[uuid.UUID]*models.CertificateDistribution

	getErr    error
	upsertErr error

	upserts []*models.CertificateDistribution
	touched bool
}

func (f *fakeDistRepo) Upsert(cd *models.CertificateDistribution) error {
	f.touched = true
	f.upserts = append(f.upserts, cd)
	if f.upsertErr != nil {
		return f.upsertErr
	}
	if f.byCert == nil {
		f.byCert = map[uuid.UUID]*models.CertificateDistribution{}
	}
	f.byCert[cd.ManagedCertificateID] = cd
	return nil
}

func (f *fakeDistRepo) GetByCertificateID(certID uuid.UUID) (*models.CertificateDistribution, error) {
	f.touched = true
	if f.getErr != nil {
		return nil, f.getErr
	}
	cd, ok := f.byCert[certID]
	if !ok {
		// Mirrors the real CertificateDistributionRepository, which returns
		// the raw gorm.ErrRecordNotFound from .First when there's genuinely
		// no row yet -- distinct from a transient read error (f.getErr
		// above), which callers must not confuse with "no row".
		return nil, gorm.ErrRecordNotFound
	}
	return cd, nil
}

type fakeSource struct {
	crt, key []byte
	found    bool
	err      error

	calls   []string
	touched bool
}

func (f *fakeSource) ReadLeafSecret(ctx context.Context, name string) ([]byte, []byte, bool, error) {
	f.touched = true
	f.calls = append(f.calls, name)
	return f.crt, f.key, f.found, f.err
}

type tenantCreateCall struct {
	projectID uuid.UUID
	namespace string
	name      string
	crt, key  []byte
}

type fakeTenantWriter struct {
	createCalls []tenantCreateCall
	createErr   error

	// secretData is keyed by "namespace/name/key" and stands in for what's
	// actually present in the tenant cluster right now.
	secretData map[string][]byte
	getErr     error

	touched bool
}

func (f *fakeTenantWriter) CreateOrUpdateTLSSecret(ctx context.Context, projectID uuid.UUID, namespace, name string, crt, key []byte) error {
	f.touched = true
	f.createCalls = append(f.createCalls, tenantCreateCall{projectID: projectID, namespace: namespace, name: name, crt: crt, key: key})
	return f.createErr
}

func (f *fakeTenantWriter) GetSecretData(ctx context.Context, projectID uuid.UUID, namespace, name, key string) ([]byte, error) {
	f.touched = true
	if f.getErr != nil {
		return nil, f.getErr
	}
	data, ok := f.secretData[namespace+"/"+name+"/"+key]
	if !ok {
		return nil, errors.New("secret not found")
	}
	return data, nil
}

type setIssuedMetaCall struct {
	certID      uuid.UUID
	fingerprint string
	notAfter    *time.Time
}

type fakeCertUpdater struct {
	calls   []setIssuedMetaCall
	err     error
	touched bool
}

func (f *fakeCertUpdater) SetIssuedMeta(certID uuid.UUID, fingerprint string, notAfter *time.Time) error {
	f.touched = true
	f.calls = append(f.calls, setIssuedMetaCall{certID: certID, fingerprint: fingerprint, notAfter: notAfter})
	return f.err
}

// =============================================================================
// Test helpers
// =============================================================================

// generateTestCert builds a self-signed leaf certificate/key pair, PEM
// encoded, with the given NotAfter. Truncated to whole seconds so it can be
// compared for exact equality after a PEM/DER round trip (X.509 time fields
// have only second precision).
func generateTestCert(t *testing.T, notAfter time.Time) (crtPEM, keyPEM []byte) {
	t.Helper()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "test.example.com"},
		NotBefore:    time.Now().Add(-time.Hour).Truncate(time.Second),
		NotAfter:     notAfter.Truncate(time.Second),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	require.NoError(t, err)
	crtPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	keyDER, err := x509.MarshalECPrivateKey(priv)
	require.NoError(t, err)
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return crtPEM, keyPEM
}

func testCert(secretName string) models.ManagedCertificate {
	return models.ManagedCertificate{
		ID:        uuid.New(),
		ProjectID: uuid.New(),
		Status:    models.ManagedCertStatusReady,
		Config:    models.ManagedCertConfig{SecretName: secretName},
	}
}

// deps bundles every fake so a test can override just the ones it cares
// about and pass the rest through to New unchanged.
type deps struct {
	gate    fakeGate
	lister  *fakeLister
	dist    *fakeDistRepo
	source  *fakeSource
	tenant  *fakeTenantWriter
	updater *fakeCertUpdater
}

func newDeps() *deps {
	return &deps{
		gate:    fakeGate{leader: true},
		lister:  &fakeLister{},
		dist:    &fakeDistRepo{},
		source:  &fakeSource{},
		tenant:  &fakeTenantWriter{},
		updater: &fakeCertUpdater{},
	}
}

func (d *deps) distributor() *Distributor {
	return New(Deps{
		Gate:         d.gate,
		Lister:       d.lister,
		DistRepo:     d.dist,
		Source:       d.source,
		TenantWriter: d.tenant,
		CertUpdater:  d.updater,
		Config:       &config.Config{},
	})
}

// =============================================================================
// Reconcile behavior
// =============================================================================

func TestReconcile_PushOnNew(t *testing.T) {
	crt, key := generateTestCert(t, time.Now().Add(90*24*time.Hour))
	cert := testCert("cert-abc")
	fp := cluster.CertFingerprint(crt)

	d := newDeps()
	d.lister.certs = []models.ManagedCertificate{cert}
	d.source.crt, d.source.key, d.source.found = crt, key, true
	// No pre-existing distribution row.

	err := d.distributor().Reconcile(context.Background())
	require.NoError(t, err)

	require.Len(t, d.tenant.createCalls, 1)
	call := d.tenant.createCalls[0]
	assert.Equal(t, cert.ProjectID, call.projectID)
	assert.Equal(t, tenantSecretNamespace, call.namespace)
	assert.Equal(t, "cert-abc", call.name)
	assert.Equal(t, crt, call.crt)
	assert.Equal(t, key, call.key)

	require.NotEmpty(t, d.dist.upserts)
	last := d.dist.upserts[len(d.dist.upserts)-1]
	assert.Equal(t, models.CertDistStatusSynced, last.Status)
	assert.Equal(t, fp, last.LastPushedFingerprint)
	assert.NotNil(t, last.LastSyncedAt)

	require.Len(t, d.updater.calls, 1)
	assert.Equal(t, cert.ID, d.updater.calls[0].certID)
	assert.Equal(t, fp, d.updater.calls[0].fingerprint)
	require.NotNil(t, d.updater.calls[0].notAfter)
}

func TestReconcile_NoopWhenInSync(t *testing.T) {
	crt, key := generateTestCert(t, time.Now().Add(90*24*time.Hour))
	cert := testCert("cert-sync")
	fp := cluster.CertFingerprint(crt)

	d := newDeps()
	d.lister.certs = []models.ManagedCertificate{cert}
	d.source.crt, d.source.key, d.source.found = crt, key, true
	d.dist.byCert = map[uuid.UUID]*models.CertificateDistribution{
		cert.ID: {ManagedCertificateID: cert.ID, Status: models.CertDistStatusSynced, LastPushedFingerprint: fp},
	}
	d.tenant.secretData = map[string][]byte{
		tenantSecretNamespace + "/cert-sync/tls.crt": crt,
	}

	err := d.distributor().Reconcile(context.Background())
	require.NoError(t, err)

	assert.Empty(t, d.tenant.createCalls, "already in sync: must not re-push")
	assert.Empty(t, d.updater.calls, "already in sync: must not touch cert meta")
}

func TestReconcile_RenewalRepush(t *testing.T) {
	oldCrt, _ := generateTestCert(t, time.Now().Add(10*24*time.Hour))
	newCrt, newKey := generateTestCert(t, time.Now().Add(90*24*time.Hour))
	cert := testCert("cert-renew")
	oldFP := cluster.CertFingerprint(oldCrt)
	newFP := cluster.CertFingerprint(newCrt)
	require.NotEqual(t, oldFP, newFP)

	d := newDeps()
	d.lister.certs = []models.ManagedCertificate{cert}
	d.source.crt, d.source.key, d.source.found = newCrt, newKey, true
	d.dist.byCert = map[uuid.UUID]*models.CertificateDistribution{
		cert.ID: {ManagedCertificateID: cert.ID, Status: models.CertDistStatusSynced, LastPushedFingerprint: oldFP},
	}
	// Tenant cluster still has the old cert -- irrelevant, the fingerprint
	// mismatch against the distribution row alone should trigger a re-push.
	d.tenant.secretData = map[string][]byte{
		tenantSecretNamespace + "/cert-renew/tls.crt": oldCrt,
	}

	err := d.distributor().Reconcile(context.Background())
	require.NoError(t, err)

	require.Len(t, d.tenant.createCalls, 1)
	assert.Equal(t, newCrt, d.tenant.createCalls[0].crt)

	last := d.dist.upserts[len(d.dist.upserts)-1]
	assert.Equal(t, models.CertDistStatusSynced, last.Status)
	assert.Equal(t, newFP, last.LastPushedFingerprint)

	require.Len(t, d.updater.calls, 1)
	assert.Equal(t, newFP, d.updater.calls[0].fingerprint)
}

func TestReconcile_SelfHealMissingTenantSecret(t *testing.T) {
	crt, key := generateTestCert(t, time.Now().Add(90*24*time.Hour))
	cert := testCert("cert-heal")
	fp := cluster.CertFingerprint(crt)

	d := newDeps()
	d.lister.certs = []models.ManagedCertificate{cert}
	d.source.crt, d.source.key, d.source.found = crt, key, true
	d.dist.byCert = map[uuid.UUID]*models.CertificateDistribution{
		cert.ID: {ManagedCertificateID: cert.ID, Status: models.CertDistStatusSynced, LastPushedFingerprint: fp},
	}
	// Bookkeeping says synced at the right fingerprint, but the tenant
	// Secret is gone (deleted out-of-band) -- GetSecretData errors.
	d.tenant.getErr = errors.New("secrets \"cert-heal\" not found")

	err := d.distributor().Reconcile(context.Background())
	require.NoError(t, err)

	require.Len(t, d.tenant.createCalls, 1, "must self-heal by re-pushing")
	assert.Equal(t, crt, d.tenant.createCalls[0].crt)
	require.Len(t, d.updater.calls, 1)
}

func TestReconcile_SourceMissing(t *testing.T) {
	cert := testCert("cert-issuing")

	d := newDeps()
	d.lister.certs = []models.ManagedCertificate{cert}
	d.source.found = false // still issuing, no Secret yet

	err := d.distributor().Reconcile(context.Background())
	require.NoError(t, err)

	assert.Empty(t, d.tenant.createCalls, "nothing to push yet")
	assert.Empty(t, d.updater.calls)

	require.Len(t, d.dist.upserts, 1)
	last := d.dist.upserts[0]
	assert.Equal(t, models.CertDistStatusPending, last.Status)
	assert.Equal(t, "source not issued yet", last.Message)
	assert.Empty(t, last.LastPushedFingerprint)
}

// TestReconcileOne_TransientReadErrorDoesNotClobberDist proves that a
// transient (non-not-found) error reading the existing distribution row
// aborts reconciliation for that certificate WITHOUT ever calling Upsert.
// Before the fix, reconcileOne discarded the error from GetByCertificateID
// entirely and treated it exactly like "no row" -- so a DB blip on a
// certificate that already had a healthy, synced distribution row would
// fall through to upsertDist(..., currentFingerprint(nil), ...) and the
// repo's OnConflict{UpdateAll} Upsert would overwrite that row's real
// LastPushedFingerprint/LastSyncedAt with zero values.
func TestReconcileOne_TransientReadErrorDoesNotClobberDist(t *testing.T) {
	cert := testCert("cert-blip")

	d := newDeps()
	// A pre-existing, healthy, synced row -- if the bug were present, the
	// error/pending path in reconcileOne would clobber this via Upsert.
	d.dist.byCert = map[uuid.UUID]*models.CertificateDistribution{
		cert.ID: {ManagedCertificateID: cert.ID, Status: models.CertDistStatusSynced, LastPushedFingerprint: "real-fingerprint"},
	}
	d.dist.getErr = errors.New("db down")

	err := d.distributor().reconcileOne(context.Background(), cert)

	require.Error(t, err, "a transient read error must be surfaced, not swallowed")
	assert.Empty(t, d.dist.upserts, "must not upsert on a transient read error -- that would clobber the existing row")

	// The source must never even be consulted: reconcileOne should bail out
	// before doing anything else once it can't confirm the existing state.
	assert.False(t, d.source.touched)
}

// TestReconcileOne_NoExistingRowIsTreatedAsNew proves the fix's error-type
// guard (errors.Is(err, gorm.ErrRecordNotFound)) still lets a genuinely new
// certificate (no distribution row yet) proceed exactly as before --
// distinguishing "no row" from a transient error is the whole point of the
// fix, so both branches need coverage.
func TestReconcileOne_NoExistingRowIsTreatedAsNew(t *testing.T) {
	crt, key := generateTestCert(t, time.Now().Add(90*24*time.Hour))
	cert := testCert("cert-new")
	fp := cluster.CertFingerprint(crt)

	d := newDeps()
	d.source.crt, d.source.key, d.source.found = crt, key, true
	// No pre-existing row: fakeDistRepo.GetByCertificateID returns
	// gorm.ErrRecordNotFound, matching the real repository.

	err := d.distributor().reconcileOne(context.Background(), cert)
	require.NoError(t, err)

	require.Len(t, d.tenant.createCalls, 1, "a new certificate must still be pushed")
	require.NotEmpty(t, d.dist.upserts)
	last := d.dist.upserts[len(d.dist.upserts)-1]
	assert.Equal(t, models.CertDistStatusSynced, last.Status)
	assert.Equal(t, fp, last.LastPushedFingerprint)
}

func TestReconcile_NotLeader(t *testing.T) {
	cert := testCert("cert-x")

	d := newDeps()
	d.gate = fakeGate{leader: false}
	d.lister.certs = []models.ManagedCertificate{cert}
	d.source.crt, d.source.key, d.source.found = []byte("crt"), []byte("key"), true

	err := d.distributor().Reconcile(context.Background())
	require.NoError(t, err)

	assert.False(t, d.lister.called, "non-leader must not even list certificates")
	assert.False(t, d.dist.touched)
	assert.False(t, d.source.touched)
	assert.False(t, d.tenant.touched)
	assert.False(t, d.updater.touched)
}

// TestReconcile_LosesLeadershipMidPass proves Reconcile rechecks
// Gate.IsLeader() before EVERY certificate in the pass, not just once at
// the top. If the advisory-lock connection dies mid-pass, IsLeader flips to
// false and a newly-elected replica may already be running its own pass;
// this replica must stop pushing immediately rather than finish the certs
// it already fetched, or both replicas could co-write to the same tenant
// clusters.
//
// The gate is scripted true, true, false: the first true satisfies the
// top-of-function check, the second true lets the first certificate's
// per-cert check through, and the false trips the per-cert check before the
// second certificate is ever reconciled.
func TestReconcile_LosesLeadershipMidPass(t *testing.T) {
	crt, key := generateTestCert(t, time.Now().Add(90*24*time.Hour))
	first := testCert("cert-first")
	second := testCert("cert-second")

	d := newDeps()
	d.lister.certs = []models.ManagedCertificate{first, second}
	d.source.crt, d.source.key, d.source.found = crt, key, true

	gate := &scriptedGate{results: []bool{true, true, false}}

	dist := New(Deps{
		Gate:         gate,
		Lister:       d.lister,
		DistRepo:     d.dist,
		Source:       d.source,
		TenantWriter: d.tenant,
		CertUpdater:  d.updater,
		Config:       &config.Config{},
	})

	err := dist.Reconcile(context.Background())
	require.NoError(t, err)

	require.Len(t, d.tenant.createCalls, 1, "must stop as soon as leadership is lost, before the second certificate")
	assert.Equal(t, "cert-first", d.tenant.createCalls[0].name)
	assert.Len(t, d.source.calls, 1, "must never even read the second certificate's source secret once leadership is lost")
}

// TestReconcile_PerCertificateIsolation proves that a panic while
// reconciling one certificate (e.g. triggered by a buggy dependency) is
// recovered and does not prevent the rest of the pass from running: a
// second, healthy certificate in the same pass must still be pushed.
func TestReconcile_PerCertificateIsolation(t *testing.T) {
	panicking := testCert("cert-panics")
	healthy := testCert("cert-healthy")

	crt, key := generateTestCert(t, time.Now().Add(90*24*time.Hour))

	d := newDeps()
	d.lister.certs = []models.ManagedCertificate{panicking, healthy}

	// A SourceReader that panics for one specific certificate's secret name,
	// standing in for a dependency bug that only manifests on one cert.
	panicky := &panicSource{okName: healthy.Config.SecretName, crt: crt, key: key}

	dist := New(Deps{
		Gate:         d.gate,
		Lister:       d.lister,
		DistRepo:     d.dist,
		Source:       panicky,
		TenantWriter: d.tenant,
		CertUpdater:  d.updater,
		Config:       &config.Config{},
	})

	require.NotPanics(t, func() {
		err := dist.Reconcile(context.Background())
		require.NoError(t, err)
	})

	require.Len(t, d.tenant.createCalls, 1, "the healthy certificate must still be pushed")
	assert.Equal(t, "cert-healthy", d.tenant.createCalls[0].name)
}

// panicSource is a SourceReader that panics for any secret name other than
// okName, simulating a dependency bug on one specific certificate.
type panicSource struct {
	okName   string
	crt, key []byte
}

func (p *panicSource) ReadLeafSecret(ctx context.Context, name string) ([]byte, []byte, bool, error) {
	if name != p.okName {
		panic("simulated dependency bug reading " + name)
	}
	return p.crt, p.key, true, nil
}

// =============================================================================
// Run
// =============================================================================

// TestRun_TicksAndReconciles proves Run reconciles once immediately at
// start and again on every subsequent tick, using a short tick +
// require.Eventually rather than any fixed sleep so the test isn't flaky
// under load.
func TestRun_TicksAndReconciles(t *testing.T) {
	d := newDeps()
	dist := d.distributor()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		dist.Run(ctx, 5*time.Millisecond, nil)
		close(done)
	}()

	// The immediate reconcile at start, plus at least one tick.
	require.Eventually(t, func() bool {
		return d.lister.callCount() >= 2
	}, time.Second, 5*time.Millisecond, "expected Run to reconcile on start and again on tick")

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not exit after ctx cancellation")
	}
}

// TestRun_NotifyChannelTriggersReconcile proves a notifyCh wake triggers an
// extra Reconcile pass independent of the ticker: tick is set long enough
// that it cannot itself fire within the test's timeout, so any pass beyond
// the initial one must have come from the notify send.
func TestRun_NotifyChannelTriggersReconcile(t *testing.T) {
	d := newDeps()
	dist := d.distributor()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	notifyCh := make(chan struct{}, 1)

	done := make(chan struct{})
	go func() {
		dist.Run(ctx, time.Hour, notifyCh)
		close(done)
	}()

	require.Eventually(t, func() bool {
		return d.lister.callCount() >= 1
	}, time.Second, 5*time.Millisecond, "expected the immediate reconcile at start")

	before := d.lister.callCount()
	notifyCh <- struct{}{}

	require.Eventually(t, func() bool {
		return d.lister.callCount() > before
	}, time.Second, 5*time.Millisecond, "expected a notify send to trigger another reconcile")

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not exit after ctx cancellation")
	}
}

// TestRun_NilNotifyChannelIsSafe proves a nil notifyCh (the ticker-only
// wiring main() uses when LISTEN/NOTIFY isn't set up) never blocks or
// panics Run -- receiving from a nil channel in a select simply never
// fires, leaving the ticker and ctx.Done branches to drive the loop.
func TestRun_NilNotifyChannelIsSafe(t *testing.T) {
	d := newDeps()
	dist := d.distributor()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		dist.Run(ctx, 5*time.Millisecond, nil)
		close(done)
	}()

	require.Eventually(t, func() bool {
		return d.lister.callCount() >= 2
	}, time.Second, 5*time.Millisecond, "expected ticks to keep reconciling with a nil notifyCh")

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not exit after ctx cancellation")
	}
}

// =============================================================================
// New (panic-on-nil deps)
// =============================================================================

func TestNew_PanicsOnMissingDeps(t *testing.T) {
	full := func() Deps {
		return Deps{
			Gate:         fakeGate{leader: true},
			Lister:       &fakeLister{},
			DistRepo:     &fakeDistRepo{},
			Source:       &fakeSource{},
			TenantWriter: &fakeTenantWriter{},
			CertUpdater:  &fakeCertUpdater{},
			Config:       &config.Config{},
		}
	}

	assert.NotPanics(t, func() { New(full()) })

	cases := []func(d *Deps){
		func(d *Deps) { d.Gate = nil },
		func(d *Deps) { d.Lister = nil },
		func(d *Deps) { d.DistRepo = nil },
		func(d *Deps) { d.Source = nil },
		func(d *Deps) { d.TenantWriter = nil },
		func(d *Deps) { d.CertUpdater = nil },
		func(d *Deps) { d.Config = nil },
	}

	for _, mutate := range cases {
		d := full()
		mutate(&d)
		assert.Panics(t, func() { New(d) })
	}
}

// =============================================================================
// parseNotAfter
// =============================================================================

func TestParseNotAfter(t *testing.T) {
	notAfter := time.Now().Add(24 * time.Hour).Truncate(time.Second)
	crtPEM, _ := generateTestCert(t, notAfter)

	got := parseNotAfter(crtPEM)
	require.NotNil(t, got)
	assert.True(t, notAfter.Equal(*got), "expected %v, got %v", notAfter, *got)
}

func TestParseNotAfter_InvalidInput(t *testing.T) {
	assert.Nil(t, parseNotAfter([]byte("not a pem")))
	assert.Nil(t, parseNotAfter(nil))

	badBlock := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("not-real-der")})
	assert.Nil(t, parseNotAfter(badBlock))
}
