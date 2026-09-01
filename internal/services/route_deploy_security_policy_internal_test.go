package services

// Regression tests for deploySecurityPolicy's SecurityPolicy lookup.
//
// The defect: the lookup collapsed every repository error into "no policy",
// so a transient DB failure during a redeploy sent a general-mode route down
// deployGeneralSecurityPolicy's config == nil branch, which DELETES the live
// SecurityPolicy (OIDC/JWT/API-key/IP authorization) from the cluster and then
// reports success. Absence (gorm.ErrRecordNotFound) must stay legal; failure
// must not.
//
// internal/mocks depends on internal/services, so a package-services internal
// test file cannot import internal/mocks without an import cycle -- see the
// same note in route_approval_internal_test.go and
// approval_characterization_test.go. The stubs below are local for that
// reason.

import (
	"context"
	"errors"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// ---------------------------------------------------------------------
// Local stubs
// ---------------------------------------------------------------------

var _ repository.SecurityPolicyRepositoryInterface = (*secPolicyLookupTestRepo)(nil)

// secPolicyLookupTestRepo answers GetByRouteID with a fixed (policy, err)
// pair. Every other method panics: this test exercises one lookup.
type secPolicyLookupTestRepo struct {
	policy *models.SecurityPolicy
	err    error
}

func (r *secPolicyLookupTestRepo) GetByRouteID(routeID uuid.UUID) (*models.SecurityPolicy, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.policy, nil
}

func (r *secPolicyLookupTestRepo) Create(policy *models.SecurityPolicy) error {
	panic("unexpected call: Create")
}

func (r *secPolicyLookupTestRepo) GetByID(id uuid.UUID) (*models.SecurityPolicy, error) {
	panic("unexpected call: GetByID")
}

func (r *secPolicyLookupTestRepo) ListByProjectID(projectID uuid.UUID) ([]models.SecurityPolicy, error) {
	panic("unexpected call: ListByProjectID")
}

func (r *secPolicyLookupTestRepo) Update(policy *models.SecurityPolicy) error {
	panic("unexpected call: Update")
}

func (r *secPolicyLookupTestRepo) Delete(id uuid.UUID) error {
	panic("unexpected call: Delete")
}

func (r *secPolicyLookupTestRepo) DeleteByRouteID(routeID uuid.UUID) error {
	panic("unexpected call: DeleteByRouteID")
}

func (r *secPolicyLookupTestRepo) ExistsByRouteID(routeID uuid.UUID) (bool, error) {
	panic("unexpected call: ExistsByRouteID")
}

func (r *secPolicyLookupTestRepo) Upsert(policy *models.SecurityPolicy) error {
	panic("unexpected call: Upsert")
}

var _ PolicyApplier = (*secPolicyLookupTestPolicies)(nil)

// secPolicyLookupTestPolicies records the SecurityPolicy writes it receives.
// deleteNames is what the defect assertion turns on: it must stay empty when
// the lookup fails.
type secPolicyLookupTestPolicies struct {
	deleteNames    []string
	updated        []*kubernetes.SecurityPolicyConfig
	eepDeleteNames []string
	eepUpdated     []*unstructured.Unstructured
}

func (p *secPolicyLookupTestPolicies) UpdateSecurityPolicy(ctx context.Context, projectID uuid.UUID, config *kubernetes.SecurityPolicyConfig) error {
	p.updated = append(p.updated, config)
	return nil
}

func (p *secPolicyLookupTestPolicies) DeleteSecurityPolicy(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	p.deleteNames = append(p.deleteNames, name)
	return nil
}

func (p *secPolicyLookupTestPolicies) UpdateBackendTrafficPolicy(ctx context.Context, projectID uuid.UUID, config *kubernetes.BackendTrafficPolicyConfig) error {
	panic("unexpected call: UpdateBackendTrafficPolicy")
}

func (p *secPolicyLookupTestPolicies) DeleteBackendTrafficPolicy(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	panic("unexpected call: DeleteBackendTrafficPolicy")
}

func (p *secPolicyLookupTestPolicies) UpdateEnvoyExtensionPolicy(ctx context.Context, projectID uuid.UUID, policy *unstructured.Unstructured) error {
	p.eepUpdated = append(p.eepUpdated, policy)
	return nil
}

func (p *secPolicyLookupTestPolicies) DeleteEnvoyExtensionPolicy(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	p.eepDeleteNames = append(p.eepDeleteNames, name)
	return nil
}

var _ BackendApplier = (*secPolicyLookupTestBackends)(nil)

// secPolicyLookupTestBackends records the legacy ext-auth Backend cleanup that
// the config == nil branch performs alongside the SecurityPolicy delete.
type secPolicyLookupTestBackends struct {
	deleteNames []string
}

func (b *secPolicyLookupTestBackends) DeleteBackend(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	b.deleteNames = append(b.deleteNames, name)
	return nil
}

func (b *secPolicyLookupTestBackends) UpdateBackend(ctx context.Context, projectID uuid.UUID, config *kubernetes.BackendConfig) error {
	panic("unexpected call: UpdateBackend")
}

func (b *secPolicyLookupTestBackends) UpdateBackendUnstructured(ctx context.Context, projectID uuid.UUID, backend *unstructured.Unstructured) error {
	panic("unexpected call: UpdateBackendUnstructured")
}

// ---------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------

func secPolicyLookupFixtures() (*models.Route, *models.Domain) {
	route := &models.Route{
		ID:           uuid.New(),
		Protocol:     models.RouteProtocolHTTP,
		SecurityMode: models.SecurityModeGeneral,
		K8sRouteName: "fg-route-test",
	}
	domain := &models.Domain{
		ID:        uuid.New(),
		ProjectID: uuid.New(),
		Namespace: "fastgateway-system",
	}
	return route, domain
}

func secPolicyLookupService(repo repository.SecurityPolicyRepositoryInterface, policies PolicyApplier, backends BackendApplier) *RouteService {
	return &RouteService{
		securityPolicyRepo: repo,
		k8sPolicies:        policies,
		k8sBackends:        backends,
	}
}

// ---------------------------------------------------------------------
// Case 1: lookup FAILURE must not be treated as absence.
// ---------------------------------------------------------------------

func TestDeploySecurityPolicy_LookupFailureDoesNotDeleteLivePolicy(t *testing.T) {
	route, domain := secPolicyLookupFixtures()

	dbErr := errors.New("dial tcp 10.0.0.5:5432: connect: connection refused")
	policies := &secPolicyLookupTestPolicies{}
	backends := &secPolicyLookupTestBackends{}
	svc := secPolicyLookupService(&secPolicyLookupTestRepo{err: dbErr}, policies, backends)

	err := svc.deploySecurityPolicy(context.Background(), route, domain)

	// Deliberately t.Error, not t.Fatal: the delete assertion below is the
	// heart of this test and must still run when this one fails.
	if err == nil {
		t.Error("expected an error when the SecurityPolicy lookup fails, got nil (deploy would report success)")
	} else if !errors.Is(err, dbErr) {
		t.Errorf("expected the repository error to be wrapped, got %v", err)
	}

	// The defect, asserted directly: a transient lookup failure must never
	// reach the delete branch. Deleting here strips OIDC/JWT/API-key/IP
	// authorization from a route that is still serving traffic.
	if len(policies.deleteNames) != 0 {
		t.Errorf("DeleteSecurityPolicy must NOT be called on a lookup failure, but was called for %v", policies.deleteNames)
	}
	if len(policies.updated) != 0 {
		t.Errorf("UpdateSecurityPolicy must not be called on a lookup failure, but was called %d time(s)", len(policies.updated))
	}
	if len(backends.deleteNames) != 0 {
		t.Errorf("DeleteBackend must not be called on a lookup failure, but was called for %v", backends.deleteNames)
	}
}

// ---------------------------------------------------------------------
// Case 2: genuine ABSENCE stays legal, cleanup branch unchanged.
// ---------------------------------------------------------------------

func TestDeploySecurityPolicy_RecordNotFoundStillCleansUp(t *testing.T) {
	route, domain := secPolicyLookupFixtures()

	policies := &secPolicyLookupTestPolicies{}
	backends := &secPolicyLookupTestBackends{}
	svc := secPolicyLookupService(&secPolicyLookupTestRepo{err: gorm.ErrRecordNotFound}, policies, backends)

	if err := svc.deploySecurityPolicy(context.Background(), route, domain); err != nil {
		t.Fatalf("a route with no SecurityPolicy must deploy cleanly, got %v", err)
	}

	wantPolicyName := kubernetes.SecurityPolicyName(route.K8sRouteName)
	if len(policies.deleteNames) != 1 || policies.deleteNames[0] != wantPolicyName {
		t.Errorf("expected the cleanup branch to delete %q, got %v", wantPolicyName, policies.deleteNames)
	}
	if len(policies.updated) != 0 {
		t.Errorf("expected no SecurityPolicy write, got %d", len(policies.updated))
	}

	wantBackendName := kubernetes.GenerateExtAuthBackendName(route.ID.String(), "")
	if len(backends.deleteNames) != 1 || backends.deleteNames[0] != wantBackendName {
		t.Errorf("expected legacy ext-auth Backend cleanup for %q, got %v", wantBackendName, backends.deleteNames)
	}
}

// ---------------------------------------------------------------------
// Case 3: a policy found is deployed, unchanged.
// ---------------------------------------------------------------------

func TestDeploySecurityPolicy_PolicyFoundIsDeployed(t *testing.T) {
	route, domain := secPolicyLookupFixtures()

	policy := &models.SecurityPolicy{
		ID:      uuid.New(),
		RouteID: route.ID,
		Config: models.SecurityPolicyConfig{
			CORS: &models.CORSConfig{AllowOrigins: []string{"https://example.com"}},
		},
	}

	policies := &secPolicyLookupTestPolicies{}
	backends := &secPolicyLookupTestBackends{}
	svc := secPolicyLookupService(&secPolicyLookupTestRepo{policy: policy}, policies, backends)

	if err := svc.deploySecurityPolicy(context.Background(), route, domain); err != nil {
		t.Fatalf("expected a clean deploy, got %v", err)
	}

	if len(policies.deleteNames) != 0 {
		t.Errorf("DeleteSecurityPolicy must not be called when a policy exists, got %v", policies.deleteNames)
	}
	if len(policies.updated) != 1 {
		t.Fatalf("expected exactly one SecurityPolicy write, got %d", len(policies.updated))
	}

	got := policies.updated[0]
	if got.Name != kubernetes.SecurityPolicyName(route.K8sRouteName) {
		t.Errorf("unexpected policy name %q", got.Name)
	}
	if got.CORS == nil || len(got.CORS.AllowOrigins) != 1 || got.CORS.AllowOrigins[0] != "https://example.com" {
		t.Errorf("expected the stored CORS config to reach Kubernetes, got %+v", got.CORS)
	}
}

// =====================================================================
// Second door: deployEnvoyExtensionPolicy (RULING R3)
//
// Identical shape to the SecurityPolicy defect, and worse -- both lookup
// errors were discarded outright (`policy, _ = ...`). Both nil sends
// buildEnvoyExtensionPolicyConfig to nil, which takes the extConfig == nil
// branch and DELETES the route's live EnvoyExtensionPolicy -- its WAF and
// ext-proc configuration -- then returns nil, so Deploy marks the route
// active.
//
// EnvoyExtensionPolicyRepository.GetByRouteID
// (internal/repository/envoy_extension_policy_repository.go:35-42) and
// WafPolicyRepository.GetByRouteID
// (internal/repository/waf_policy_repository.go:37-44) BOTH end in GORM's
// First(...), so genuine absence really does surface as
// gorm.ErrRecordNotFound on both.
// =====================================================================

var _ repository.EnvoyExtensionPolicyRepositoryInterface = (*eepLookupTestRepo)(nil)

type eepLookupTestRepo struct {
	policy *models.EnvoyExtensionPolicy
	err    error
}

func (r *eepLookupTestRepo) GetByRouteID(routeID uuid.UUID) (*models.EnvoyExtensionPolicy, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.policy, nil
}

func (r *eepLookupTestRepo) Create(policy *models.EnvoyExtensionPolicy) error {
	panic("unexpected call: Create")
}

func (r *eepLookupTestRepo) GetByID(id uuid.UUID) (*models.EnvoyExtensionPolicy, error) {
	panic("unexpected call: GetByID")
}

func (r *eepLookupTestRepo) GetByDomainID(domainID uuid.UUID) (*models.EnvoyExtensionPolicy, error) {
	panic("unexpected call: GetByDomainID")
}

func (r *eepLookupTestRepo) ListByProjectID(projectID uuid.UUID) ([]models.EnvoyExtensionPolicy, error) {
	panic("unexpected call: ListByProjectID")
}

func (r *eepLookupTestRepo) Update(policy *models.EnvoyExtensionPolicy) error {
	panic("unexpected call: Update")
}

func (r *eepLookupTestRepo) Delete(id uuid.UUID) error { panic("unexpected call: Delete") }

func (r *eepLookupTestRepo) DeleteByRouteID(routeID uuid.UUID) error {
	panic("unexpected call: DeleteByRouteID")
}

func (r *eepLookupTestRepo) DeleteByDomainID(domainID uuid.UUID) error {
	panic("unexpected call: DeleteByDomainID")
}

func (r *eepLookupTestRepo) ExistsByRouteID(routeID uuid.UUID) (bool, error) {
	panic("unexpected call: ExistsByRouteID")
}

func (r *eepLookupTestRepo) ExistsByDomainID(domainID uuid.UUID) (bool, error) {
	panic("unexpected call: ExistsByDomainID")
}

func (r *eepLookupTestRepo) Upsert(policy *models.EnvoyExtensionPolicy) error {
	panic("unexpected call: Upsert")
}

var _ repository.WafPolicyRepositoryInterface = (*wafLookupTestRepo)(nil)

type wafLookupTestRepo struct {
	policy *models.WafPolicy
	err    error
}

func (r *wafLookupTestRepo) GetByRouteID(routeID uuid.UUID) (*models.WafPolicy, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.policy, nil
}

func (r *wafLookupTestRepo) Create(policy *models.WafPolicy) error {
	panic("unexpected call: Create")
}

func (r *wafLookupTestRepo) GetByID(id uuid.UUID) (*models.WafPolicy, error) {
	panic("unexpected call: GetByID")
}

func (r *wafLookupTestRepo) ListByProjectID(projectID uuid.UUID) ([]models.WafPolicy, error) {
	panic("unexpected call: ListByProjectID")
}

func (r *wafLookupTestRepo) Update(policy *models.WafPolicy) error {
	panic("unexpected call: Update")
}

func (r *wafLookupTestRepo) Delete(id uuid.UUID) error { panic("unexpected call: Delete") }

func (r *wafLookupTestRepo) DeleteByRouteID(routeID uuid.UUID) error {
	panic("unexpected call: DeleteByRouteID")
}

func (r *wafLookupTestRepo) ExistsByRouteID(routeID uuid.UUID) (bool, error) {
	panic("unexpected call: ExistsByRouteID")
}

func (r *wafLookupTestRepo) Upsert(policy *models.WafPolicy) error {
	panic("unexpected call: Upsert")
}

func eepLookupService(eepRepo repository.EnvoyExtensionPolicyRepositoryInterface, wafRepo repository.WafPolicyRepositoryInterface, policies PolicyApplier, backends BackendApplier) *RouteService {
	return &RouteService{
		envoyExtensionPolicyRepo: eepRepo,
		wafPolicyRepo:            wafRepo,
		k8sPolicies:              policies,
		k8sBackends:              backends,
	}
}

// Case 1: EEP lookup FAILURE must not be treated as absence.
func TestDeployEnvoyExtensionPolicy_EEPLookupFailureDoesNotDeleteLivePolicy(t *testing.T) {
	route, domain := secPolicyLookupFixtures()

	dbErr := errors.New("dial tcp 10.0.0.5:5432: connect: connection refused")
	policies := &secPolicyLookupTestPolicies{}
	backends := &secPolicyLookupTestBackends{}
	svc := eepLookupService(
		&eepLookupTestRepo{err: dbErr},
		&wafLookupTestRepo{err: gorm.ErrRecordNotFound},
		policies, backends)

	err := svc.deployEnvoyExtensionPolicy(context.Background(), route, domain)

	// t.Error, not t.Fatal: the delete assertion below is the point.
	if err == nil {
		t.Error("expected an error when the EnvoyExtensionPolicy lookup fails, got nil (deploy would report success)")
	} else if !errors.Is(err, dbErr) {
		t.Errorf("expected the repository error to be wrapped, got %v", err)
	}

	// The defect: a transient lookup failure must never reach the delete
	// branch. Deleting here strips the route's live WAF and ext-proc policy
	// while it is still serving traffic.
	if len(policies.eepDeleteNames) != 0 {
		t.Errorf("DeleteEnvoyExtensionPolicy must NOT be called on a lookup failure, but was called for %v", policies.eepDeleteNames)
	}
	if len(policies.eepUpdated) != 0 {
		t.Errorf("UpdateEnvoyExtensionPolicy must not be called on a lookup failure, but was called %d time(s)", len(policies.eepUpdated))
	}
	if len(backends.deleteNames) != 0 {
		t.Errorf("ext-proc Backend cleanup must not run on a lookup failure, but DeleteBackend was called for %v", backends.deleteNames)
	}
}

// Case 2: WAF lookup FAILURE must not be treated as absence either.
func TestDeployEnvoyExtensionPolicy_WAFLookupFailureDoesNotDeleteLivePolicy(t *testing.T) {
	route, domain := secPolicyLookupFixtures()

	dbErr := errors.New("driver: bad connection")
	policies := &secPolicyLookupTestPolicies{}
	backends := &secPolicyLookupTestBackends{}
	// EEP genuinely absent; only the WAF lookup fails.
	svc := eepLookupService(
		&eepLookupTestRepo{err: gorm.ErrRecordNotFound},
		&wafLookupTestRepo{err: dbErr},
		policies, backends)

	err := svc.deployEnvoyExtensionPolicy(context.Background(), route, domain)

	if err == nil {
		t.Error("expected an error when the WafPolicy lookup fails, got nil (deploy would report success)")
	} else if !errors.Is(err, dbErr) {
		t.Errorf("expected the repository error to be wrapped, got %v", err)
	}

	if len(policies.eepDeleteNames) != 0 {
		t.Errorf("DeleteEnvoyExtensionPolicy must NOT be called on a WAF lookup failure, but was called for %v", policies.eepDeleteNames)
	}
	if len(policies.eepUpdated) != 0 {
		t.Errorf("UpdateEnvoyExtensionPolicy must not be called on a WAF lookup failure, but was called %d time(s)", len(policies.eepUpdated))
	}
	if len(backends.deleteNames) != 0 {
		t.Errorf("ext-proc Backend cleanup must not run on a WAF lookup failure, but DeleteBackend was called for %v", backends.deleteNames)
	}
}

// Case 3: genuine ABSENCE on both stays legal, cleanup branch unchanged.
func TestDeployEnvoyExtensionPolicy_RecordNotFoundStillCleansUp(t *testing.T) {
	route, domain := secPolicyLookupFixtures()

	policies := &secPolicyLookupTestPolicies{}
	backends := &secPolicyLookupTestBackends{}
	svc := eepLookupService(
		&eepLookupTestRepo{err: gorm.ErrRecordNotFound},
		&wafLookupTestRepo{err: gorm.ErrRecordNotFound},
		policies, backends)

	if err := svc.deployEnvoyExtensionPolicy(context.Background(), route, domain); err != nil {
		t.Fatalf("a route with no extension or WAF policy must deploy cleanly, got %v", err)
	}

	wantEEPName := kubernetes.EnvoyExtensionPolicyName(route.K8sRouteName)
	if len(policies.eepDeleteNames) != 1 || policies.eepDeleteNames[0] != wantEEPName {
		t.Errorf("expected the cleanup branch to delete %q, got %v", wantEEPName, policies.eepDeleteNames)
	}
	if len(policies.eepUpdated) != 0 {
		t.Errorf("expected no EnvoyExtensionPolicy write, got %d", len(policies.eepUpdated))
	}

	wantBackendName := kubernetes.GenerateExtProcBackendName(route.ID.String())
	if len(backends.deleteNames) != 1 || backends.deleteNames[0] != wantBackendName {
		t.Errorf("expected ext-proc Backend cleanup for %q, got %v", wantBackendName, backends.deleteNames)
	}
}

// Case 4: both policies found are deployed, unchanged.
func TestDeployEnvoyExtensionPolicy_PoliciesFoundAreDeployed(t *testing.T) {
	route, domain := secPolicyLookupFixtures()
	routeID := route.ID

	eep := &models.EnvoyExtensionPolicy{
		ID:      uuid.New(),
		RouteID: &routeID,
		Config: models.EnvoyExtensionPolicyConfig{
			Lua: &models.LuaExtensionConfig{Type: "Inline", Inline: "function envoy_on_request(h) end"},
		},
	}
	waf := &models.WafPolicy{
		ID:      uuid.New(),
		RouteID: routeID,
		Config:  models.WafPolicyConfig{Mode: "block", Rulesets: []string{"owasp-crs"}},
	}

	policies := &secPolicyLookupTestPolicies{}
	backends := &secPolicyLookupTestBackends{}
	svc := eepLookupService(
		&eepLookupTestRepo{policy: eep},
		&wafLookupTestRepo{policy: waf},
		policies, backends)

	if err := svc.deployEnvoyExtensionPolicy(context.Background(), route, domain); err != nil {
		t.Fatalf("expected a clean deploy, got %v", err)
	}

	if len(policies.eepDeleteNames) != 0 {
		t.Errorf("DeleteEnvoyExtensionPolicy must not be called when policies exist, got %v", policies.eepDeleteNames)
	}
	if len(policies.eepUpdated) != 1 {
		t.Fatalf("expected exactly one EnvoyExtensionPolicy write, got %d", len(policies.eepUpdated))
	}
	if got := policies.eepUpdated[0].GetName(); got != kubernetes.EnvoyExtensionPolicyName(route.K8sRouteName) {
		t.Errorf("unexpected EnvoyExtensionPolicy name %q", got)
	}
}
