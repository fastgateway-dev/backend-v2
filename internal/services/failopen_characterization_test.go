package services

// CHARACTERIZATION (Phase 2G Task 2), INVERTED (Phase 2G Task 4). Originally
// pinned CURRENT (fail-open) behaviour at the top-severity tier of
// discarded-error sites identified by the phase audit
// (.superpowers/sdd/2026-09-01-backend-v2-phase-2g/audit.md). Task 4 fixed
// sites T2, T3, T4, T5 and T6 (the client-attachment and domain-mTLS
// cluster, in internal/services) and inverted every test covering them below
// to assert the NEW (fail-closed/propagating) behaviour -- see each test's
// own "SINCE Phase 2G / BEFORE Phase 2G" comment. T1 and T8 were Task 3's
// sites (internal/domainplan and templateAnnotations respectively) and were
// already inverted/updated by that task; this file does not touch them
// again.
//
// Two sibling sites in route_deploy.go -- deploySecurityPolicy's and
// deployEnvoyExtensionPolicy's own SecurityPolicy/EnvoyExtensionPolicy/WAF
// lookups -- were ALREADY fixed by commit d68df31 (a security hotfix ahead
// of this phase) and are pinned by
// route_deploy_security_policy_internal_test.go. They are not re-pinned
// here. Their three-arm `switch { case err == nil / errors.Is(err,
// gorm.ErrRecordNotFound) / default: return err }` shape is the model for
// what Task 3/4's fixes to the sites below will look like.
//
// internal/mocks depends on internal/services (for compile-time interface
// checks), so a package-services internal test file cannot import
// internal/mocks without an import cycle -- see the same note in
// approval_characterization_test.go and route_approval_internal_test.go.
// This file reuses two kinds of existing in-package stub: the mock.Mock
// -based approvalCharTestAttachmentRepo (approval_characterization_test.go)
// for repository.ClientAttachmentRepositoryInterface, and the fixed-value,
// panic-on-unexpected-call style of secPolicyLookupTestRepo /
// eepLookupTestRepo (route_deploy_security_policy_internal_test.go) for the
// handful of narrower repository/applier interfaces that have no existing
// stub yet. secPolicyLookupFixtures, secPolicyLookupTestPolicies and
// secPolicyLookupTestBackends from that same file are reused directly below
// rather than redefined.
//
// Site index (file:line measured against the CURRENT tree, per the
// controller's note that domain_mtls.go, route_clients.go,
// route_clients_apikey.go, domain_yaml.go and clienttrafficpolicy.go were
// re-verified today):
//
//	T1  internal/domainplan/clienttrafficpolicy.go:86
//	T2  internal/services/domain_mtls.go:46,63-65   (collectCASecretRefs)
//	T3  internal/services/domain_mtls.go:104-162     (AddDomainMTLSCA)
//	T4  internal/services/route_clients.go:82-98     (countClientAttachments)
//	T5  internal/services/route_clients.go:151,185,222 (collect* trio)
//	T6  internal/services/route_clients_apikey.go:182 (categorizeClientAttachments)
//	T8  internal/services/domain_yaml.go:51-60        (templateAnnotations)

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"log"
	"math/big"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/internal/cluster"
	"github.com/fastgateway-dev/backend-v2/internal/domainplan"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// =========================================================================
// Local stubs not already available in the package
// =========================================================================

// failopenClientRepo answers GetByID with a fixed (client, err) pair. Every
// other method panics: these tests exercise one lookup.
var _ repository.ClientRepositoryInterface = (*failopenClientRepo)(nil)

type failopenClientRepo struct {
	client *models.Client
	err    error
}

func (r *failopenClientRepo) GetByID(id uuid.UUID) (*models.Client, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.client, nil
}
func (r *failopenClientRepo) Create(*models.Client) error { panic("unexpected call: Create") }
func (r *failopenClientRepo) Update(*models.Client) error { panic("unexpected call: Update") }
func (r *failopenClientRepo) Delete(uuid.UUID) error      { panic("unexpected call: Delete") }
func (r *failopenClientRepo) List(page, limit int, teamID *uuid.UUID) ([]models.Client, int64, error) {
	panic("unexpected call: List")
}
func (r *failopenClientRepo) ExistsByName(string) (bool, error) {
	panic("unexpected call: ExistsByName")
}
func (r *failopenClientRepo) ExistsByNameExcluding(string, uuid.UUID) (bool, error) {
	panic("unexpected call: ExistsByNameExcluding")
}
func (r *failopenClientRepo) ListByTeamIDs([]uuid.UUID) ([]models.Client, error) {
	panic("unexpected call: ListByTeamIDs")
}

// failopenClientIPRepo answers ListByClientID with a fixed (ips, err) pair.
var _ repository.ClientIPRepositoryInterface = (*failopenClientIPRepo)(nil)

type failopenClientIPRepo struct {
	ips []models.ClientIPAddress
	err error
}

func (r *failopenClientIPRepo) ListByClientID(uuid.UUID) ([]models.ClientIPAddress, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.ips, nil
}
func (r *failopenClientIPRepo) Create(*models.ClientIPAddress) error {
	panic("unexpected call: Create")
}
func (r *failopenClientIPRepo) GetByID(uuid.UUID) (*models.ClientIPAddress, error) {
	panic("unexpected call: GetByID")
}
func (r *failopenClientIPRepo) Delete(uuid.UUID) error { panic("unexpected call: Delete") }
func (r *failopenClientIPRepo) CountByClientID(uuid.UUID) (int64, error) {
	panic("unexpected call: CountByClientID")
}

// failopenClientHeaderRepo answers ListByClientID with a fixed (headers, err)
// pair.
var _ repository.ClientHeaderRepositoryInterface = (*failopenClientHeaderRepo)(nil)

type failopenClientHeaderRepo struct {
	headers []models.ClientHeader
	err     error
}

func (r *failopenClientHeaderRepo) ListByClientID(uuid.UUID) ([]models.ClientHeader, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.headers, nil
}
func (r *failopenClientHeaderRepo) Create(*models.ClientHeader) error {
	panic("unexpected call: Create")
}
func (r *failopenClientHeaderRepo) GetByID(uuid.UUID) (*models.ClientHeader, error) {
	panic("unexpected call: GetByID")
}
func (r *failopenClientHeaderRepo) Delete(uuid.UUID) error { panic("unexpected call: Delete") }
func (r *failopenClientHeaderRepo) CountByClientID(uuid.UUID) (int64, error) {
	panic("unexpected call: CountByClientID")
}

// failopenDomainRepo answers GetByID with a fixed (domain, err) pair.
var _ repository.DomainRepositoryInterface = (*failopenDomainRepo)(nil)

type failopenDomainRepo struct {
	domain *models.Domain
	err    error
}

func (r *failopenDomainRepo) GetByID(uuid.UUID) (*models.Domain, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.domain, nil
}
func (r *failopenDomainRepo) Create(*models.Domain) error { panic("unexpected call: Create") }
func (r *failopenDomainRepo) GetByIDs([]uuid.UUID) ([]models.Domain, error) {
	panic("unexpected call: GetByIDs")
}
func (r *failopenDomainRepo) ListByProjectID(uuid.UUID, int, int, string, string, map[string]string) ([]models.Domain, int64, error) {
	panic("unexpected call: ListByProjectID")
}
func (r *failopenDomainRepo) Update(*models.Domain) error { panic("unexpected call: Update") }
func (r *failopenDomainRepo) Delete(uuid.UUID) error      { panic("unexpected call: Delete") }
func (r *failopenDomainRepo) ExistsByHostname(uuid.UUID, string) (bool, error) {
	panic("unexpected call: ExistsByHostname")
}
func (r *failopenDomainRepo) ListByTemplateID(uuid.UUID) ([]models.Domain, error) {
	panic("unexpected call: ListByTemplateID")
}
func (r *failopenDomainRepo) CountByProjectID(uuid.UUID) (int, error) {
	panic("unexpected call: CountByProjectID")
}

// failopenSettingsRepo answers GetByDomainID with a fixed error and records
// every Upsert call -- that record is the whole point of the T3 test below.
var _ repository.DomainSettingsRepositoryInterface = (*failopenSettingsRepo)(nil)

type failopenSettingsRepo struct {
	getErr   error
	upserted []*models.DomainSettings
}

func (r *failopenSettingsRepo) GetByDomainID(uuid.UUID) (*models.DomainSettings, error) {
	return nil, r.getErr
}
func (r *failopenSettingsRepo) Upsert(settings *models.DomainSettings) error {
	r.upserted = append(r.upserted, settings)
	return nil
}
func (r *failopenSettingsRepo) Create(*models.DomainSettings) error {
	panic("unexpected call: Create")
}
func (r *failopenSettingsRepo) GetByID(uuid.UUID) (*models.DomainSettings, error) {
	panic("unexpected call: GetByID")
}
func (r *failopenSettingsRepo) ListByProjectID(uuid.UUID) ([]models.DomainSettings, error) {
	panic("unexpected call: ListByProjectID")
}
func (r *failopenSettingsRepo) Update(*models.DomainSettings) error {
	panic("unexpected call: Update")
}
func (r *failopenSettingsRepo) Delete(uuid.UUID) error { panic("unexpected call: Delete") }
func (r *failopenSettingsRepo) DeleteByDomainID(uuid.UUID) error {
	panic("unexpected call: DeleteByDomainID")
}
func (r *failopenSettingsRepo) ExistsByDomainID(uuid.UUID) (bool, error) {
	panic("unexpected call: ExistsByDomainID")
}

// failopenSecrets answers CreateOrUpdateSecret with success. Every other
// method panics.
var _ Secrets = (*failopenSecrets)(nil)

type failopenSecrets struct{}

func (f *failopenSecrets) CreateOrUpdateSecret(ctx context.Context, projectID uuid.UUID, namespace, name string, data map[string][]byte) error {
	return nil
}
func (f *failopenSecrets) DeleteSecret(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	panic("unexpected call: DeleteSecret")
}
func (f *failopenSecrets) ListTLSSecrets(ctx context.Context, projectID uuid.UUID, namespace string) ([]cluster.TLSSecretInfo, error) {
	panic("unexpected call: ListTLSSecrets")
}

// failopenDTLookup answers GetByID with a fixed (template, err) pair.
var _ DomainTemplateLookup = (*failopenDTLookup)(nil)

type failopenDTLookup struct {
	dt  *models.DomainTemplate
	err error
}

func (f *failopenDTLookup) GetByID(uuid.UUID) (*models.DomainTemplate, error) {
	return f.dt, f.err
}

// testSelfSignedCAPEM generates a fresh, minimal self-signed CA certificate
// PEM at test time, so validateCAPEM (domain_mtls.go) has something it will
// actually accept without depending on a fixture certificate's expiry.
func testSelfSignedCAPEM(t *testing.T) string {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "failopen-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)

	var buf bytes.Buffer
	require.NoError(t, pem.Encode(&buf, &pem.Block{Type: "CERTIFICATE", Bytes: der}))
	return buf.String()
}

// =========================================================================
// T1 -- internal/domainplan/clienttrafficpolicy.go:86
//
// BuildClientTrafficPolicyConfig used to guard the entire ClientValidation
// block (plus SAN matchers, certificate hashes, and the XFCC headers block)
// behind `config.MTLS.Enabled && len(caSecretRefs) > 0`. With mTLS enabled
// and zero refs, the Gateway-scoped ClientTrafficPolicy applied successfully
// with NO client-certificate validation at all -- every route behind the
// domain was affected.
// internal/domainplan/testdata/golden/ctp-mtls-enabled-no-ca-refs.yaml
// (formerly ctp-mtls-enabled-no-ca-refs-f3.yaml, byte-identical to
// ctp-empty-settings.yaml) pinned this at the golden level; this test pins
// the same fact directly against the return value, independent of the
// golden file.
//
// SINCE Phase 2G: the guard no longer checks len(caSecretRefs) > 0. The
// ClientValidation and Headers blocks are now emitted whenever mTLS is
// enabled, even with zero refs -- an empty CACertificateRefs list makes
// Envoy reject the policy instead of silently admitting every client.
// =========================================================================

func TestBuildClientTrafficPolicyConfig_MTLSEnabledNoRefsStillEmitsClientValidation(t *testing.T) {
	domain := &models.Domain{
		ID:             uuid.New(),
		Namespace:      "gateway-ns",
		K8sGatewayName: "eg",
	}
	cfg := &models.DomainSettingsConfig{
		MTLS: &models.DomainMTLSConfig{
			Enabled:  true,
			Optional: false, // client cert REQUIRED -- and now enforced even with no refs.
			SANWhitelist: []models.MTLSSANEntry{
				{Type: "DNS", Value: "client.example.com"},
			},
			HashWhitelist: []string{
				"a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
			},
		},
	}

	got := domainplan.BuildClientTrafficPolicyConfig(domain, cfg, nil)

	require.NotNil(t, got)
	require.NotNil(t, got.ClientValidation,
		"SINCE Phase 2G (internal/domainplan/clienttrafficpolicy.go:86; was fail-open "+
			"before): mTLS enabled with zero CA secret refs must still build a "+
			"ClientTrafficPolicy WITH a ClientValidation block -- an empty ref list "+
			"makes Envoy reject the policy instead of admitting every client.")
	assert.Empty(t, got.ClientValidation.CACertificateRefs)
	assert.False(t, got.ClientValidation.Optional)
	assert.NotNil(t, got.Headers,
		"the XFCC headers block rides on the same guard and is emitted too")
}

// =========================================================================
// T2 -- internal/services/domain_mtls.go:46, :63-65 (collectCASecretRefs)
//
// SINCE Phase 2G (S1): GetMTLSClientsForDomain ends in gorm's Find, which
// returns an empty slice with a nil error when there are no rows -- so any
// non-nil error is a genuine repository failure, never absence, and is now
// propagated as an error from collectCASecretRefs instead of being logged
// and swallowed.
//
// BEFORE Phase 2G: the error was logged and swallowed, so the refs list
// silently lost every client-contributed CA (only the domain-level CAs
// configured directly on the DomainSettingsConfig survived). This was
// exactly the empty/short ref slice that fed T1's guard above.
// =========================================================================

func TestCollectCASecretRefs_MTLSClientLookupErrorPropagates(t *testing.T) {
	domain := &models.Domain{ID: uuid.New()}
	cfg := &models.DomainSettingsConfig{
		MTLS: &models.DomainMTLSConfig{
			Enabled: true,
			CACerts: []models.MTLSCACert{
				{ID: "ca-1", Name: "Corp Root", SecretName: "corp-root-ca", SecretKey: "ca.crt"},
			},
		},
	}

	attachRepo := &approvalCharTestAttachmentRepo{}
	attachRepo.On("GetMTLSClientsForDomain", domain.ID).
		Return([]models.Client{}, errors.New("connection refused"))

	svc := &DomainService{clientAttachmentRepo: attachRepo}

	refs, err := svc.collectCASecretRefs(domain, cfg)

	require.Error(t, err,
		"SINCE Phase 2G (domain_mtls.go:46,63-65; was fail-open before): a "+
			"GetMTLSClientsForDomain error must now be propagated instead of "+
			"logged and swallowed -- GetMTLSClientsForDomain ends in gorm's Find, "+
			"so any error is a genuine repository failure, never absence. "+
			"Swallowing it used to silently drop every client-attachment CA from "+
			"the ref list, which could push T1's len(caSecretRefs) > 0 guard to "+
			"exactly zero.")
	assert.Contains(t, err.Error(), "connection refused")
	assert.Nil(t, refs)
}

// =========================================================================
// T3 -- internal/services/domain_mtls.go:104-162 (AddDomainMTLSCA)
//
// SINCE Phase 2G (S2, controller Ruling P2): blank settings are fabricated
// ONLY for genuine absence (gorm.ErrRecordNotFound); any other
// settingsRepo.GetByDomainID error is now propagated and AddDomainMTLSCA
// fails instead of silently overwriting the stored row.
//
// BEFORE Phase 2G: on ANY settingsRepo.GetByDomainID error -- not just
// absence -- fabricated blank settings were substituted and later Upsert'd,
// silently overwriting whatever was actually stored. Adding a CA during a
// database blip was a security-INCREASING admin action that instead wiped
// MTLS.Enabled, SANWhitelist, HashWhitelist, TLS and ClientIPDetection.
// =========================================================================

func TestAddDomainMTLSCA_SettingsLookupErrorFailsInsteadOfWipingStoredConfig(t *testing.T) {
	domainID := uuid.New()
	domain := &models.Domain{ID: domainID, ProjectID: uuid.New(), Namespace: "fastgateway-system"}

	settingsRepo := &failopenSettingsRepo{getErr: errors.New("connection refused")}
	svc := &DomainService{
		domainRepo:   &failopenDomainRepo{domain: domain},
		settingsRepo: settingsRepo,
		k8sSecrets:   &failopenSecrets{},
	}

	got, err := svc.AddDomainMTLSCA(context.Background(), domainID, &AddDomainMTLSCAInput{
		Name:  "corp-root",
		CAPem: testSelfSignedCAPEM(t),
	})

	require.Error(t, err,
		"SINCE Phase 2G (domain_mtls.go, S2, controller Ruling P2; was "+
			"fail-open before): a non-not-found settingsRepo.GetByDomainID "+
			"error must now fail AddDomainMTLSCA instead of silently "+
			"fabricating and Upsert-ing a blank settings row that overwrites "+
			"whatever was actually stored.")
	assert.Contains(t, err.Error(), "connection refused")
	assert.Nil(t, got)
	assert.Empty(t, settingsRepo.upserted,
		"no write must reach Upsert when the settings lookup genuinely failed -- "+
			"that write is exactly what used to overwrite the previously-stored row")
}

// A genuinely absent settings row (gorm.ErrRecordNotFound -- this domain has
// never had settings) must still legitimately fabricate blank settings; this
// chain is NOT the S2 defect and must keep working.
func TestAddDomainMTLSCA_SettingsGenuinelyAbsentStillCreatesSettings(t *testing.T) {
	domainID := uuid.New()
	domain := &models.Domain{ID: domainID, ProjectID: uuid.New(), Namespace: "fastgateway-system"}

	settingsRepo := &failopenSettingsRepo{getErr: gorm.ErrRecordNotFound}
	svc := &DomainService{
		domainRepo:   &failopenDomainRepo{domain: domain},
		settingsRepo: settingsRepo,
		k8sSecrets:   &failopenSecrets{},
	}

	got, err := svc.AddDomainMTLSCA(context.Background(), domainID, &AddDomainMTLSCAInput{
		Name:  "corp-root",
		CAPem: testSelfSignedCAPEM(t),
	})

	require.NoError(t, err,
		"genuine absence of a settings row is legitimate and must keep working")
	require.NotNil(t, got)
	require.Len(t, settingsRepo.upserted, 1)

	saved := settingsRepo.upserted[0]
	assert.Same(t, got, saved)
	require.NotNil(t, saved.Config.MTLS)
	require.Len(t, saved.Config.MTLS.CACerts, 1,
		"the newly-added CA is present on the freshly-created settings row")
	assert.Equal(t, "corp-root", saved.Config.MTLS.CACerts[0].Name)
}

// =========================================================================
// T4 -- internal/services/route_clients.go:82-98 (countClientAttachments)
//
// SINCE Phase 2G (S3): ListActiveByRouteID/ListApprovedByRouteID both end in
// gorm's Find, which returns an empty slice with a nil error when there are
// no rows -- so any non-nil error is a genuine repository failure, never
// absence, and is now propagated instead of yielding 0.
//
// BEFORE Phase 2G: countClientAttachments returned 0 when EITHER
// ListActiveByRouteID or ListApprovedByRouteID errored -- there was no way
// to tell "route genuinely has no client attachments" from "the repository
// failed." Its caller, route_deploy.go:328-331, gates the entire
// DefaultTrafficPolicy block on `clientCount > 0`, so a route configured
// `defaultTrafficPolicy: deny` used to deploy with NO deny rule at all when
// this repository call failed.
// =========================================================================

func TestCountClientAttachments_RepoErrorPropagates(t *testing.T) {
	routeID := uuid.New()
	dbErr := errors.New("connection refused")

	attachRepo := &approvalCharTestAttachmentRepo{}
	attachRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, dbErr)

	svc := &RouteService{clientAttachmentRepo: attachRepo}

	got, err := svc.countClientAttachments(routeID)

	require.Error(t, err,
		"SINCE Phase 2G (route_clients.go:82-98; was fail-open before): a "+
			"repository error on either lookup must now be propagated instead "+
			"of silently reported as 0, which used to be indistinguishable "+
			"from a route with zero real client attachments")
	assert.Contains(t, err.Error(), "connection refused")
	assert.Equal(t, 0, got)
}

// Deploy-level consequence: SINCE Phase 2G, a client-mode route configured
// defaultTrafficPolicy=deny now FAILS the deploy instead of silently
// deploying with no deny rule at all, because countClientAttachments (above)
// now propagates the repository error instead of reporting 0 attachments.
// FIX ROUND 1 (review finding F-3): the original version of this test only
// ever exercised buildClientIPAuthorizationConfig's own ListActiveByRouteID
// call (route_deploy.go:325, called BEFORE countClientAttachments at :328) --
// it stubbed ONLY ListActiveByRouteID to error, which
// buildClientIPAuthorizationConfig's three collectors consume first, so the
// asserted error was actually "build client IP authorization config for
// route ...: list active client attachments ...", never reaching
// countClientAttachments at all. That meant this test would still have
// passed even if S3's fix to countClientAttachments were reverted -- a hole
// in the one safety net this phase cannot afford to leave unpinned.
//
// Fixed by letting ListActiveByRouteID succeed on every call (so
// buildClientIPAuthorizationConfig's three collectors -- and
// countClientAttachments's own first lookup -- all complete cleanly) and
// letting ListApprovedByRouteID succeed for exactly the first three calls
// (consumed by collectClientIPCIDRs, collectClientHeaders,
// collectClientMethods inside buildClientIPAuthorizationConfig) before
// failing on the fourth call, which is countClientAttachments's own
// ListApprovedByRouteID lookup (route_deploy.go:328 -> route_clients.go:96).
// The assertion now names the count operation explicitly, not just the
// driver string, so a regression back to the old
// buildClientIPAuthorizationConfig-only failure would be caught here too.
func TestDeploySecurityPolicy_ClientCountRepoErrorFailsDeployInsteadOfSkippingDenyGate(t *testing.T) {
	route, domain := secPolicyLookupFixtures()
	route.SecurityMode = models.SecurityModeClient
	route.Config.DefaultTrafficPolicy = models.DefaultTrafficPolicyDeny

	dbErr := errors.New("connection refused")
	attachRepo := &approvalCharTestAttachmentRepo{}
	// Succeeds on every call: consumed by buildClientIPAuthorizationConfig's
	// three collectors AND by countClientAttachments's own first lookup.
	attachRepo.On("ListActiveByRouteID", route.ID).Return([]models.ClientRouteAttachment{}, nil)
	// The first three calls are buildClientIPAuthorizationConfig's
	// collectClientIPCIDRs/collectClientHeaders/collectClientMethods, which
	// must all succeed so execution actually reaches countClientAttachments.
	// The fourth call is countClientAttachments's own ListApprovedByRouteID --
	// that one fails, so the propagated error genuinely originates there.
	attachRepo.On("ListApprovedByRouteID", route.ID).Return([]models.ClientRouteAttachment{}, nil).Once()
	attachRepo.On("ListApprovedByRouteID", route.ID).Return([]models.ClientRouteAttachment{}, nil).Once()
	attachRepo.On("ListApprovedByRouteID", route.ID).Return([]models.ClientRouteAttachment{}, nil).Once()
	attachRepo.On("ListApprovedByRouteID", route.ID).Return([]models.ClientRouteAttachment{}, dbErr)

	policies := &secPolicyLookupTestPolicies{}
	backends := &secPolicyLookupTestBackends{}
	svc := &RouteService{
		securityPolicyRepo:   &secPolicyLookupTestRepo{err: gorm.ErrRecordNotFound},
		clientAttachmentRepo: attachRepo,
		k8sPolicies:          policies,
		k8sBackends:          backends,
	}

	err := svc.deploySecurityPolicy(context.Background(), route, domain)

	require.Error(t, err,
		"SINCE Phase 2G: the deploy must now fail loudly when the client-"+
			"attachment repository fails, rather than silently deploying "+
			"defaultTrafficPolicy=deny with no deny rule at all (BEFORE Phase "+
			"2G, countClientAttachments/T4 silently reported 0 attachments and "+
			"the whole gate at route_deploy.go:331 was skipped)")
	assert.Contains(t, err.Error(), "count client attachments",
		"the error must name the COUNT operation specifically -- proving "+
			"execution reached countClientAttachments (route_deploy.go:328) "+
			"rather than failing earlier inside buildClientIPAuthorizationConfig "+
			"(route_deploy.go:325), which is what the pre-fix-round-1 version "+
			"of this test actually (and wrongly) exercised -- see F-3")
	assert.Contains(t, err.Error(), "connection refused")
	assert.Empty(t, policies.updated,
		"no partial/weakened SecurityPolicy is written on this failure path")
	assert.Empty(t, policies.deleteNames,
		"nor is the existing SecurityPolicy deleted -- the deploy fails closed, "+
			"reporting the failure rather than reporting success with either a "+
			"missing or a deleted policy")
}

// FIX ROUND 1 (review finding F-4): the inversion of TestCountClientAttachments_*
// only pinned the FIRST propagation path (ListActiveByRouteID erroring,
// route_clients.go:~90). Nothing pinned the SECOND path -- an approved-list
// error after the active list succeeds (route_clients.go:96) -- at the unit
// level. Added here as a sibling to TestCountClientAttachments_RepoErrorPropagates.
func TestCountClientAttachments_ApprovedListRepoErrorPropagates(t *testing.T) {
	routeID := uuid.New()
	dbErr := errors.New("connection refused")

	attachRepo := &approvalCharTestAttachmentRepo{}
	attachRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{{}}, nil)
	attachRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, dbErr)

	svc := &RouteService{clientAttachmentRepo: attachRepo}

	got, err := svc.countClientAttachments(routeID)

	require.Error(t, err,
		"FIX ROUND 1 (route_clients.go:96, F-4): the SECOND propagation path -- "+
			"an approved-list error after the active list succeeds -- must also "+
			"propagate, not just the first")
	assert.Contains(t, err.Error(), "connection refused")
	assert.Equal(t, 0, got)
}

// =========================================================================
// T5 -- internal/services/route_clients.go collect* trio
//
// SINCE Phase 2G (S4): ListActiveByRouteID/ListApprovedByRouteID/
// clientHeaderRepo.ListByClientID/clientIPRepo.ListByClientID all end in
// gorm's Find, so any non-nil error is a genuine repository failure, never
// absence -- collectClientHeaders (:151), collectClientMethods (:185) and
// collectClientIPCIDRs (:222) now all propagate it instead of returning
// nil/empty.
//
// BEFORE Phase 2G: each swallowed a ListActiveByRouteID error and returned
// nil/empty rather than propagating it. Consequence, per the audit: an empty
// clientMethods left rule.Methods unset (verb restriction gone); an empty
// clientCIDRs made the all-empty check at route_clients.go:105 drop the
// whole Authorization block.
// =========================================================================

func TestCollectClientHeaders_ActiveListErrorPropagates(t *testing.T) {
	routeID := uuid.New()
	attachRepo := &approvalCharTestAttachmentRepo{}
	attachRepo.On("ListActiveByRouteID", routeID).
		Return([]models.ClientRouteAttachment{}, errors.New("connection refused"))
	// Note: ListApprovedByRouteID is deliberately NOT stubbed. The function
	// returns before reaching it (route_clients.go:154), and the mock.Mock
	// stub panics on any unexpected call -- so this test also proves that.

	svc := &RouteService{clientAttachmentRepo: attachRepo}

	got, err := svc.collectClientHeaders(routeID)

	require.Error(t, err,
		"SINCE Phase 2G (route_clients.go:151-154; was fail-open before): a "+
			"ListActiveByRouteID error must now be propagated instead of "+
			"swallowed into nil headers, which used to be indistinguishable "+
			"from a route with no header-auth clients at all")
	assert.Contains(t, err.Error(), "connection refused")
	assert.Nil(t, got)
}

func TestCollectClientMethods_ActiveListErrorPropagates(t *testing.T) {
	routeID := uuid.New()
	attachRepo := &approvalCharTestAttachmentRepo{}
	attachRepo.On("ListActiveByRouteID", routeID).
		Return([]models.ClientRouteAttachment{}, errors.New("connection refused"))

	svc := &RouteService{clientAttachmentRepo: attachRepo}

	got, err := svc.collectClientMethods(routeID)

	require.Error(t, err,
		"SINCE Phase 2G (route_clients.go:185-189; was fail-open before): a "+
			"ListActiveByRouteID error must now be propagated instead of "+
			"swallowed into nil methods, which used to deploy the rule with NO "+
			"verb restriction at all instead of the client's configured "+
			"AllowedMethods")
	assert.Contains(t, err.Error(), "connection refused")
	assert.Nil(t, got)
}

// The clientRepo.GetByID lookup inside collectClientMethods ends in gorm's
// First, so genuine absence (the client was deleted) must stay legal and
// distinct from a real lookup failure.
func TestCollectClientMethods_ClientGenuinelyAbsentIsSkippedNotPropagated(t *testing.T) {
	routeID := uuid.New()
	clientID := uuid.New()
	att := models.ClientRouteAttachment{ClientID: clientID}

	attachRepo := &approvalCharTestAttachmentRepo{}
	attachRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{att}, nil)
	attachRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	svc := &RouteService{
		clientAttachmentRepo: attachRepo,
		clientRepo:           &failopenClientRepo{err: gorm.ErrRecordNotFound},
	}

	got, err := svc.collectClientMethods(routeID)

	require.NoError(t, err,
		"a client that genuinely no longer exists is legitimately skipped, "+
			"not treated as a repository failure")
	assert.Nil(t, got)
}

func TestCollectClientIPCIDRs_ActiveListErrorPropagates(t *testing.T) {
	routeID := uuid.New()
	attachRepo := &approvalCharTestAttachmentRepo{}
	attachRepo.On("ListActiveByRouteID", routeID).
		Return([]models.ClientRouteAttachment{}, errors.New("connection refused"))

	svc := &RouteService{clientAttachmentRepo: attachRepo}

	got, err := svc.collectClientIPCIDRs(routeID)

	require.Error(t, err,
		"SINCE Phase 2G (route_clients.go:222-227; was fail-open before): a "+
			"ListActiveByRouteID error must now be propagated instead of logged "+
			"and swallowed into nil CIDRs. BEFORE Phase 2G, combined with empty "+
			"headers/methods (siblings above), the all-empty check at "+
			"route_clients.go:105 then dropped the WHOLE Authorization block for "+
			"the base route -- SINCE Phase 2G the whole build fails instead.")
	assert.Contains(t, err.Error(), "connection refused")
	assert.Nil(t, got)
}

// =========================================================================
// T6 -- internal/services/route_clients_apikey.go:182 (categorizeClientAttachments)
//
// categorizeClientAttachments already returns an error for its OWN two list
// calls (ListActiveByRouteID / ListApprovedByRouteID) -- these three
// discards are purely local, per-client swallows further down the same
// function.
// =========================================================================

// :223 -- SINCE Phase 2G, a clientIPRepo.ListByClientID error now propagates
// instead of leaving cat.IPCIDRs empty. clientIPRepo.ListByClientID ends in
// gorm's Find, so any error is a genuine repository failure, never absence.
//
// BEFORE Phase 2G: the error left cat.IPCIDRs empty, so a client configured
// for "API key AND source-IP allowlist" (AND logic) silently became "API
// key only".
func TestCategorizeClientAttachments_IPListErrorPropagates(t *testing.T) {
	routeID := uuid.New()
	clientID := uuid.New()
	domain := &models.Domain{ID: uuid.New(), ProjectID: uuid.New()}

	att := models.ClientRouteAttachment{
		ClientID:          clientID,
		EnableIPAllowlist: true,
		EnableAPIKey:      true,
	}
	client := &models.Client{
		ID:                 clientID,
		APIKeyEnabled:      true,
		APIKeyEncrypted:    base64.StdEncoding.EncodeToString([]byte("super-secret-api-key")),
		APIKeyHeaderName:   "x-api-key",
		ClientIDHeaderName: "x-client-id",
	}

	attachRepo := &approvalCharTestAttachmentRepo{}
	attachRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{att}, nil)
	attachRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	svc := &RouteService{
		clientAttachmentRepo: attachRepo,
		clientRepo:           &failopenClientRepo{client: client},
		clientIPRepo:         &failopenClientIPRepo{err: errors.New("connection refused")},
	}

	ipOnly, apiKeyOnly, bothClients, err := svc.categorizeClientAttachments(context.Background(), routeID, domain)

	require.Error(t, err,
		"SINCE Phase 2G (route_clients_apikey.go:223; was fail-open before): a "+
			"clientIPRepo error must now be propagated instead of silently "+
			"leaving IPCIDRs empty, which used to turn \"API key AND "+
			"source-IP\" into \"API key only\" for the deployed per-client route")
	assert.Contains(t, err.Error(), "connection refused")
	assert.Nil(t, ipOnly)
	assert.Nil(t, apiKeyOnly)
	assert.Nil(t, bothClients)
}

// :239 -- SINCE Phase 2G (controller Ruling R13(a)), a base64 decode
// failure on client.APIKeyEncrypted now propagates instead of leaving
// cat.APIKey empty. Unlike the "no encrypted key stored at all" branch
// beside it (deliberately left as log-and-continue -- that is a legitimate
// "not configured" case), a decode failure is a corrupt/failed operation on
// data that IS present.
//
// BEFORE Phase 2G: the error was logged and swallowed, leaving cat.APIKey
// empty while the client could still reach a returned bucket via another
// independently-valid auth method (here, JWT) -- the worst site in the
// tier, because the per-client route still published matching on the
// non-secret X-Client-ID header with no API-key credential enforced at all
// (see TestBuildAPIKeyHTTPRouteConfig_PublishesClientIDMatchEvenWithEmptyAPIKey,
// deliberately NOT inverted -- see its own comment).
func TestCategorizeClientAttachments_APIKeyDecodeFailurePropagates(t *testing.T) {
	routeID := uuid.New()
	clientID := uuid.New()
	domain := &models.Domain{ID: uuid.New(), ProjectID: uuid.New()}

	att := models.ClientRouteAttachment{
		ClientID:     clientID,
		EnableAPIKey: true,
		EnableJWT:    true, // dual-auth attachment per controller Ruling R13(a):
		// the JWT half is independently valid, which is what used to keep this
		// client visible in a returned bucket even though the API key half
		// silently failed below.
	}
	client := &models.Client{
		ID:                 clientID,
		APIKeyEnabled:      true,
		APIKeyEncrypted:    "%%% not valid base64 %%%",
		ClientIDHeaderName: "x-client-id",
		JWTEnabled:         true,
		JWTIssuer:          "https://issuer.example.com",
		JWTJWKSURL:         "https://issuer.example.com/.well-known/jwks.json",
	}

	attachRepo := &approvalCharTestAttachmentRepo{}
	attachRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{att}, nil)
	attachRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	svc := &RouteService{
		clientAttachmentRepo: attachRepo,
		clientRepo:           &failopenClientRepo{client: client},
	}

	ipOnly, apiKeyOnly, bothClients, err := svc.categorizeClientAttachments(context.Background(), routeID, domain)

	require.Error(t, err,
		"SINCE Phase 2G (route_clients_apikey.go:239, controller Ruling "+
			"R13(a); was fail-open before): a base64 decode failure on the "+
			"encrypted API key must now be propagated instead of logged and "+
			"swallowed into an empty cat.APIKey")
	assert.Contains(t, err.Error(), "illegal base64")
	assert.Nil(t, ipOnly)
	assert.Nil(t, apiKeyOnly)
	assert.Nil(t, bothClients)
}

// The consequence of :239, isolated at the builder that makes it the worst
// site in the tier: buildAPIKeyHTTPRouteConfig gates the X-Client-ID header
// match purely on the boolean EnableAPIKey/EnableJWT/EnableMTLS flags, NOT
// on whether the category actually carries a credential. So a client with
// EnableAPIKey=true and an EMPTY decoded APIKey (exactly :239's outcome)
// still gets a published per-client route matching on the plain,
// non-secret X-Client-ID header -- with no APIKeyAuth attached anywhere to
// require the key at all.
func TestBuildAPIKeyHTTPRouteConfig_PublishesClientIDMatchEvenWithEmptyAPIKey(t *testing.T) {
	route := &models.Route{
		ID:           uuid.New(),
		K8sRouteName: "fg-route-test",
		Config: models.RouteConfig{
			Matches: []models.RouteMatch{{Path: &models.PathMatch{Type: "PathPrefix", Value: "/"}}},
		},
	}
	domain := &models.Domain{ID: uuid.New(), Namespace: "fastgateway-system"}
	clientID := uuid.New()

	cat := routeplan.ClientAuthCategory{
		ClientID:           clientID,
		ClientName:         "acme",
		EnableAPIKey:       true,
		APIKey:             "", // :239's outcome: decode failed, no credential at all
		APIKeyHeaderName:   "x-api-key",
		ClientIDHeaderName: "x-client-id",
	}

	svc := &RouteService{}
	got := svc.buildAPIKeyHTTPRouteConfig(route, domain, cat)

	require.Len(t, got.Rules, 1)
	var sawClientIDMatch bool
	for _, h := range got.Rules[0].Headers {
		if h.Name == "x-client-id" && h.Value == clientID.String() {
			sawClientIDMatch = true
		}
	}
	assert.True(t, sawClientIDMatch,
		"CHARACTERIZATION (route_clients_apikey.go:452-461, the consequence of "+
			":239): the per-client route is published matching on the plain "+
			"X-Client-ID header regardless of whether cat.APIKey is populated. "+
			"X-Client-ID is a non-secret the gateway echoes to backends and the "+
			"API displays -- anyone who sends a client's UUID reaches the backend "+
			"with no credential at all.")
}

// :291 -- SINCE Phase 2G, a clientHeaderRepo.ListByClientID error now
// propagates instead of leaving cat.HeaderMatches empty.
// clientHeaderRepo.ListByClientID ends in gorm's Find, so any error is a
// genuine repository failure, never absence.
//
// BEFORE Phase 2G: the error was swallowed, dropping the configured
// header-match authorization requirement for this client's per-client
// route.
func TestCategorizeClientAttachments_HeaderListErrorPropagates(t *testing.T) {
	routeID := uuid.New()
	clientID := uuid.New()
	domain := &models.Domain{ID: uuid.New(), ProjectID: uuid.New()}

	att := models.ClientRouteAttachment{
		ClientID:         clientID,
		EnableAPIKey:     true,
		EnableHeaderAuth: true,
	}
	client := &models.Client{
		ID:                 clientID,
		APIKeyEnabled:      true,
		APIKeyEncrypted:    base64.StdEncoding.EncodeToString([]byte("another-secret-key")),
		ClientIDHeaderName: "x-client-id",
	}

	attachRepo := &approvalCharTestAttachmentRepo{}
	attachRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{att}, nil)
	attachRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	svc := &RouteService{
		clientAttachmentRepo: attachRepo,
		clientRepo:           &failopenClientRepo{client: client},
		clientHeaderRepo:     &failopenClientHeaderRepo{err: errors.New("connection refused")},
	}

	ipOnly, apiKeyOnly, bothClients, err := svc.categorizeClientAttachments(context.Background(), routeID, domain)

	require.Error(t, err,
		"SINCE Phase 2G (route_clients_apikey.go:291; was fail-open before): a "+
			"clientHeaderRepo error must now be propagated instead of silently "+
			"dropping the configured header-match authorization requirement for "+
			"this client's per-client route")
	assert.Contains(t, err.Error(), "connection refused")
	assert.Nil(t, ipOnly)
	assert.Nil(t, apiKeyOnly)
	assert.Nil(t, bothClients)
}

// =========================================================================
// T8 -- internal/services/domain_yaml.go:51-60 (templateAnnotations)
//
// BEFORE Phase 2G: a failed template lookup returned nil (no annotations)
// with nothing logged, so a domain whose template lookup failed deployed a
// Gateway with no annotations at all, silently.
//
// SINCE Phase 2G (F1, ruled FAIL-SOFT -- see task-3-report.md): the
// annotations are still dropped (annotations are not a security control, and
// the two callers cannot return an error here without a signature change),
// but the failure is now logged, distinguishing genuine absence
// (gorm.ErrRecordNotFound -- the template was deleted) from an actual lookup
// failure.
// =========================================================================

func TestTemplateAnnotations_LookupErrorReturnsNilAndLogs(t *testing.T) {
	templateID := uuid.New()
	domainID := uuid.New()
	domain := &models.Domain{ID: domainID, DomainTemplateID: &templateID}

	svc := &DomainService{dtService: &failopenDTLookup{err: errors.New("connection refused")}}

	var logBuf bytes.Buffer
	origOutput := log.Writer()
	origFlags := log.Flags()
	log.SetOutput(&logBuf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(origOutput)
		log.SetFlags(origFlags)
	}()

	got := svc.templateAnnotations(domain)

	assert.Nil(t, got,
		"SINCE Phase 2G (domain_yaml.go, F1): a template lookup error still "+
			"yields nil annotations (FAIL-SOFT, not FAIL-CLOSED -- annotations "+
			"are not a security control).")
	logged := logBuf.String()
	assert.Contains(t, logged, "connection refused",
		"but unlike before Phase 2G, the failure is now logged instead of "+
			"swallowed silently")
	assert.Contains(t, logged, domainID.String())
	assert.Contains(t, logged, templateID.String())
}

// A genuinely absent template (gorm.ErrRecordNotFound -- the template row
// was deleted) is logged too, but distinguished from an actual lookup
// failure so a domain whose template was legitimately removed does not log a
// scary error on every deploy.
func TestTemplateAnnotations_NotFoundReturnsNilAndLogsAbsenceNotFailure(t *testing.T) {
	templateID := uuid.New()
	domain := &models.Domain{DomainTemplateID: &templateID}

	svc := &DomainService{dtService: &failopenDTLookup{err: gorm.ErrRecordNotFound}}

	var logBuf bytes.Buffer
	origOutput := log.Writer()
	origFlags := log.Flags()
	log.SetOutput(&logBuf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(origOutput)
		log.SetFlags(origFlags)
	}()

	got := svc.templateAnnotations(domain)

	assert.Nil(t, got)
	logged := logBuf.String()
	assert.NotEmpty(t, logged, "absence is still logged, just distinguished from failure")
	assert.NotContains(t, logged, "record not found",
		"the log message should describe absence in domain terms, not leak the "+
			"raw gorm error text a real failure log would carry")
}

func TestTemplateAnnotations_NilTemplateReturnsNil(t *testing.T) {
	templateID := uuid.New()
	domain := &models.Domain{DomainTemplateID: &templateID}

	svc := &DomainService{dtService: &failopenDTLookup{dt: nil, err: nil}}

	got := svc.templateAnnotations(domain)

	assert.Nil(t, got,
		"a nil template with no error also yields nil annotations")
}

func TestTemplateAnnotations_SuccessReturnsAnnotations(t *testing.T) {
	templateID := uuid.New()
	domain := &models.Domain{DomainTemplateID: &templateID}

	dt := &models.DomainTemplate{
		ID:          templateID,
		Annotations: models.Annotations{"example.com/owner": "platform-team"},
	}
	svc := &DomainService{dtService: &failopenDTLookup{dt: dt}}

	got := svc.templateAnnotations(domain)

	assert.Equal(t, map[string]string{"example.com/owner": "platform-team"}, got)
}

// =========================================================================
// F-1 (Phase 2G Task 4 fix round 1) --
// internal/services/route_yaml.go:174-244 (generateAPIKeyClientResourceYAMLs)
//
// generateAPIKeyClientResourceYAMLs used to swallow categorizeClientAttachments's
// error into a nil (empty) result. S5 (see task-4-report.md) routed three
// NEW, deterministic error sources into categorizeClientAttachments -- a
// base64 decode failure among them -- so a route with a corrupt encrypted
// API key used to render a preview with NO per-client API-key resources at
// all, while Deploy of the identical route hard-fails on the same error.
// Preview and deploy silently disagreeing is this project's #1 known defect
// class.
//
// FIX ROUND 1: the error now propagates out of
// generateAPIKeyClientResourceYAMLs; its only caller, GenerateYAMLs, already
// returns (*RouteYAMLs, error) and now fails the whole preview instead (see
// TestRouteService_GenerateYAMLs_APIKeyDecodeFailurePropagatesError in
// route_service_test.go for the end-to-end pin at the GenerateYAMLs level).
// =========================================================================

func TestGenerateAPIKeyClientResourceYAMLs_CategorizeErrorPropagates(t *testing.T) {
	routeID := uuid.New()
	clientID := uuid.New()
	route := &models.Route{ID: routeID, K8sRouteName: "fg-route-test"}
	domain := &models.Domain{ID: uuid.New(), ProjectID: uuid.New()}

	att := models.ClientRouteAttachment{
		ClientID:     clientID,
		EnableAPIKey: true,
	}
	client := &models.Client{
		ID:              clientID,
		APIKeyEnabled:   true,
		APIKeyEncrypted: "%%% not valid base64 %%%",
	}

	attachRepo := &approvalCharTestAttachmentRepo{}
	attachRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{att}, nil)
	attachRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	svc := &RouteService{
		clientAttachmentRepo: attachRepo,
		clientRepo:           &failopenClientRepo{client: client},
	}

	got, err := svc.generateAPIKeyClientResourceYAMLs(route, domain)

	require.Error(t, err,
		"FIX ROUND 1 (route_yaml.go:174-244; F-1): a categorizeClientAttachments "+
			"error must now propagate instead of silently yielding nil resources")
	assert.Contains(t, err.Error(), "illegal base64")
	assert.Nil(t, got)
}
