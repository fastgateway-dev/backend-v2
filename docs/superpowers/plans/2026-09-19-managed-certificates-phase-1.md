# Managed Certificates — Phase 1 (Trust Infrastructure) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a FastGateway platform admin (`owner`) create the trust infrastructure — DNS-provider credentials, certificate issuers (self-signed CA and public ACME), and per-project issuer grants — with the cert-manager CRDs materialized in the backend's own (control) cluster. No certificate issuance yet (that is Phase 2).

**Architecture:** New owner-only, platform-global resources (`DNSProviderCredential`, `CertificateIssuer`, `IssuerProjectGrant`) following the existing model→repository→service→handler layering. cert-manager `SelfSigned`/`CA`/`ACME` `ClusterIssuer`s (+ a CA `Certificate` and solver/account Secrets) are applied to the **control cluster** via a new in-cluster dynamic client (the existing `cluster.Client` requires a Project row; the control cluster has none). Secret material (DNS creds, ACME EAB) is encrypted at rest with `internal/crypto`.

**Tech Stack:** Go 1.25, GORM, Gin, `golang-migrate/v4`, cert-manager (`cert-manager.io/v1`), `k8s.io/client-go/dynamic`, `internal/crypto` (AES-256-GCM), testify + mockery v3.

**Spec:** `docs/superpowers/specs/2026-09-19-managed-certificates-design.md`
**API contract:** `docs/superpowers/plans/2026-09-19-managed-certificates-api.md` (Phase 1 section)

## Global Constraints

- **No private keys in the DB.** CA/ACME-account/leaf keys live only in cluster Secrets. The DB stores metadata, fingerprints, status.
- **Only DNS-provider creds + ACME EAB are stored at rest, encrypted** via `crypto.Encrypt(plaintext, cfg.EncryptionKey)`; never returned in any response (`json:"-"`).
- **The backend runs in-cluster** in the control cluster; control-cluster access uses `rest.InClusterConfig()`. Tests inject a fake `dynamic.Interface`.
- **cert-manager is a prerequisite** of the control cluster. Namespaced cert-manager objects and CA/ACME/solver Secrets live in `cfg.ControlPlaneNamespace` (default `fastgateway-system`); the operator must set cert-manager's `--cluster-resource-namespace` to that same namespace so `ClusterIssuer`s resolve their Secrets.
- **All Phase 1 endpoints are `owner`-only** (`RequireRole("owner")`). No `certificate.*` project permissions in this phase (those arrive in Phase 2).
- **cert-manager CRD kinds:** `Certificate`, `Issuer` (namespaced) and `ClusterIssuer` (cluster-scoped), all `cert-manager.io/v1`. Phase 1 uses `ClusterIssuer` for CA + ACME and a namespaced `Certificate` for the CA cert.
- **First DNS provider implemented: Cloudflare** (`providerType: "cloudflare"`, credential `{apiToken}`). Others are additive via the same switch.

---

## File Structure

**Create:**
- `migrations/000038_add_dns_provider_credentials.up.sql` / `.down.sql`
- `migrations/000039_add_certificate_issuers.up.sql` / `.down.sql`
- `migrations/000040_add_issuer_project_grants.up.sql` / `.down.sql`
- `internal/models/dns_provider_credential.go`
- `internal/models/certificate_issuer.go`
- `internal/models/issuer_project_grant.go`
- `internal/repository/dns_provider_credential_repository.go`
- `internal/repository/certificate_issuer_repository.go`
- `internal/repository/issuer_project_grant_repository.go`
- `internal/cluster/controlplane.go` (in-cluster client + cert-manager/Secret apply)
- `internal/kubernetes/certmanager.go` (unstructured CRD builders)
- `internal/services/dns_credential_service.go`
- `internal/services/certificate_issuer_service.go`
- `internal/services/issuer_grant_service.go`
- `internal/services/certinfra_roles.go` (control-plane role interface)
- `internal/handlers/dns_credential_handler.go`
- `internal/handlers/certificate_issuer_handler.go`
- `internal/handlers/issuer_grant_handler.go`
- Tests alongside each (`*_test.go`), plus golden fixtures under `internal/kubernetes/testdata/golden/certmanager/`.

**Modify:**
- `internal/kubernetes/gvr.go` — add cert-manager GVRs.
- `internal/repository/interfaces.go` — add the three repo interfaces + `var _` assertions.
- `internal/handlers/service_interfaces.go` — add handler-facing service interfaces.
- `cmd/server/main.go` — `RouterDeps` fields, handler construction, `setupRouter` owner-only route groups.
- `internal/config/config.go` — add `ControlPlaneNamespace` (env `CONTROL_PLANE_NAMESPACE`, default `fastgateway-system`).
- `.mockery.yml` — add the new interfaces so `make mocks` generates mocks.

---

## Task 1: cert-manager GVRs + control-plane in-cluster client

**Files:**
- Modify: `internal/kubernetes/gvr.go`
- Create: `internal/cluster/controlplane.go`
- Create: `internal/cluster/controlplane_test.go`
- Modify: `internal/config/config.go` (add `ControlPlaneNamespace`)

**Interfaces:**
- Consumes: `k8s.io/client-go/dynamic.Interface`, `k8s.io/apimachinery/pkg/runtime/schema`.
- Produces:
  - GVR vars `kubernetes.CertManagerCertificateGVR`, `kubernetes.CertManagerIssuerGVR`, `kubernetes.CertManagerClusterIssuerGVR`.
  - `cluster.InClusterDynamicClient() (dynamic.Interface, error)`
  - `type ControlPlaneClient struct { dyn dynamic.Interface; namespace string }`
  - `cluster.NewControlPlaneClient(dyn dynamic.Interface, namespace string) *ControlPlaneClient`
  - `(*ControlPlaneClient).ApplyClusterScoped(ctx, gvr, obj) error`
  - `(*ControlPlaneClient).ApplyNamespaced(ctx, gvr, obj) error`
  - `(*ControlPlaneClient).Get(ctx, gvr, name, namespaced bool) (*unstructured.Unstructured, error)`
  - `(*ControlPlaneClient).Delete(ctx, gvr, name string, namespaced bool) error`
  - `(*ControlPlaneClient).Namespace() string`

- [ ] **Step 1: Write the failing test** — `internal/cluster/controlplane_test.go`

```go
package cluster

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/scheme"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
)

func TestControlPlaneClient_ApplyClusterScoped_CreateThenUpdate(t *testing.T) {
	dyn := dynamicfake.NewSimpleDynamicClient(scheme.Scheme)
	cp := NewControlPlaneClient(dyn, "fastgateway-system")
	gvr := kubernetes.CertManagerClusterIssuerGVR

	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "cert-manager.io/v1", "kind": "ClusterIssuer",
		"metadata": map[string]interface{}{"name": "iss-1"},
		"spec":     map[string]interface{}{"selfSigned": map[string]interface{}{}},
	}}
	require.NoError(t, cp.ApplyClusterScoped(context.Background(), gvr, obj))

	// second apply must not error (update path)
	require.NoError(t, cp.ApplyClusterScoped(context.Background(), gvr, obj))

	got, err := cp.Get(context.Background(), gvr, "iss-1", false)
	require.NoError(t, err)
	assert.Equal(t, "iss-1", got.GetName())
}

func TestControlPlaneClient_ApplyNamespaced_UsesConfiguredNamespace(t *testing.T) {
	dyn := dynamicfake.NewSimpleDynamicClient(scheme.Scheme)
	cp := NewControlPlaneClient(dyn, "fastgateway-system")
	gvr := kubernetes.CertManagerCertificateGVR
	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "cert-manager.io/v1", "kind": "Certificate",
		"metadata": map[string]interface{}{"name": "ca-1"},
		"spec":     map[string]interface{}{"isCA": true},
	}}
	require.NoError(t, cp.ApplyNamespaced(context.Background(), gvr, obj))
	got, err := cp.Get(context.Background(), gvr, "ca-1", true)
	require.NoError(t, err)
	assert.Equal(t, "fastgateway-system", got.GetNamespace())

	_ = corev1.Secret{}
	_ = metav1.ObjectMeta{}
	_ = schema.GroupVersionResource{}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cluster/ -run TestControlPlaneClient -v`
Expected: FAIL — undefined `NewControlPlaneClient`, `CertManagerClusterIssuerGVR`.

- [ ] **Step 3: Add GVRs** — append to `internal/kubernetes/gvr.go` (in the same `var (...)` block as `SecurityPolicyGVR`):

```go
	// cert-manager
	CertManagerCertificateGVR = schema.GroupVersionResource{
		Group: "cert-manager.io", Version: "v1", Resource: "certificates",
	}
	CertManagerIssuerGVR = schema.GroupVersionResource{
		Group: "cert-manager.io", Version: "v1", Resource: "issuers",
	}
	CertManagerClusterIssuerGVR = schema.GroupVersionResource{
		Group: "cert-manager.io", Version: "v1", Resource: "clusterissuers",
	}
```

- [ ] **Step 4: Implement the control-plane client** — `internal/cluster/controlplane.go`

```go
package cluster

import (
	"context"
	"fmt"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// InClusterDynamicClient builds a dynamic client against the pod's own cluster,
// with no Project row required. Used for the control (issuer) cluster.
func InClusterDynamicClient() (dynamic.Interface, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get in-cluster config: %w", err)
	}
	return dynamic.NewForConfig(config)
}

// ControlPlaneClient applies cert-manager CRDs and Secrets to the backend's own
// (control) cluster. Namespaced objects go in `namespace`.
type ControlPlaneClient struct {
	dyn       dynamic.Interface
	namespace string
}

func NewControlPlaneClient(dyn dynamic.Interface, namespace string) *ControlPlaneClient {
	return &ControlPlaneClient{dyn: dyn, namespace: namespace}
}

func (c *ControlPlaneClient) Namespace() string { return c.namespace }

func (c *ControlPlaneClient) ApplyClusterScoped(ctx context.Context, gvr schema.GroupVersionResource, obj *unstructured.Unstructured) error {
	return c.apply(ctx, c.dyn.Resource(gvr), obj)
}

func (c *ControlPlaneClient) ApplyNamespaced(ctx context.Context, gvr schema.GroupVersionResource, obj *unstructured.Unstructured) error {
	obj.SetNamespace(c.namespace)
	return c.apply(ctx, c.dyn.Resource(gvr).Namespace(c.namespace), obj)
}

func (c *ControlPlaneClient) apply(ctx context.Context, ri dynamic.ResourceInterface, obj *unstructured.Unstructured) error {
	_, err := ri.Create(ctx, obj, metav1.CreateOptions{})
	if err == nil {
		return nil
	}
	if k8serrors.IsAlreadyExists(err) {
		return updateUnstructuredWithRetry(ctx, ri, obj.GetName(), obj)
	}
	return fmt.Errorf("failed to apply %s/%s: %w", obj.GetKind(), obj.GetName(), err)
}

func (c *ControlPlaneClient) Get(ctx context.Context, gvr schema.GroupVersionResource, name string, namespaced bool) (*unstructured.Unstructured, error) {
	ri := c.dyn.Resource(gvr)
	if namespaced {
		return ri.Namespace(c.namespace).Get(ctx, name, metav1.GetOptions{})
	}
	return ri.Get(ctx, name, metav1.GetOptions{})
}

func (c *ControlPlaneClient) Delete(ctx context.Context, gvr schema.GroupVersionResource, name string, namespaced bool) error {
	ri := c.dyn.Resource(gvr)
	var err error
	if namespaced {
		err = ri.Namespace(c.namespace).Delete(ctx, name, metav1.DeleteOptions{})
	} else {
		err = ri.Delete(ctx, name, metav1.DeleteOptions{})
	}
	if err != nil && !k8serrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete %s: %w", name, err)
	}
	return nil
}
```

(`updateUnstructuredWithRetry` already exists in `internal/cluster/client.go:145`.)

- [ ] **Step 5: Add config field** — in `internal/config/config.go`, add `ControlPlaneNamespace string` to the `Config` struct and load it: `ControlPlaneNamespace: getEnv("CONTROL_PLANE_NAMESPACE", "fastgateway-system")` (match the existing `getEnv` pattern in that file).

- [ ] **Step 6: Run test to verify it passes**

Run: `go test ./internal/cluster/ -run TestControlPlaneClient -v`
Expected: PASS (both subtests).

- [ ] **Step 7: Commit**

```bash
git add internal/kubernetes/gvr.go internal/cluster/controlplane.go internal/cluster/controlplane_test.go internal/config/config.go
git commit -m "feat(certs): cert-manager GVRs + in-cluster control-plane client"
```

---

## Task 2: DNSProviderCredential — model, migration, repository

**Files:**
- Create: `migrations/000038_add_dns_provider_credentials.up.sql`, `.down.sql`
- Create: `internal/models/dns_provider_credential.go`
- Create: `internal/models/dns_provider_credential_test.go`
- Create: `internal/repository/dns_provider_credential_repository.go`
- Modify: `internal/repository/interfaces.go`

**Interfaces:**
- Produces:
  - `models.DNSProviderCredential` (fields below), `models.DNSCredentialData` (JSONB, `Value`/`Scan`).
  - `repository.DNSProviderCredentialRepositoryInterface` with `Create/GetByID/List/Update/Delete`.
  - `repository.NewDNSProviderCredentialRepository(db) *DNSProviderCredentialRepository`.

- [ ] **Step 1: Write the failing test** — `internal/models/dns_provider_credential_test.go` (Value/Scan round-trip, no DB)

```go
package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDNSCredentialData_ValueScanRoundTrip(t *testing.T) {
	in := DNSCredentialData{"apiToken": "cf-secret-123"}
	v, err := in.Value()
	require.NoError(t, err)

	var out DNSCredentialData
	require.NoError(t, out.Scan(v))
	assert.Equal(t, "cf-secret-123", out["apiToken"])
}

func TestDNSCredentialData_ScanNil(t *testing.T) {
	var out DNSCredentialData
	require.NoError(t, out.Scan(nil))
	assert.NotNil(t, out)
	assert.Len(t, out, 0)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/models/ -run TestDNSCredentialData -v`
Expected: FAIL — undefined `DNSCredentialData`.

- [ ] **Step 3: Implement the model** — `internal/models/dns_provider_credential.go`

```go
package models

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// DNSCredentialData is the encrypted, provider-specific credential payload,
// stored as JSONB. For cloudflare: {"apiToken": "<encrypted>"}.
type DNSCredentialData map[string]string

func (d DNSCredentialData) Value() (driver.Value, error) {
	if d == nil {
		return "{}", nil
	}
	b, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

func (d *DNSCredentialData) Scan(value interface{}) error {
	if value == nil {
		*d = make(DNSCredentialData)
		return nil
	}
	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return errors.New("failed to scan DNSCredentialData: unexpected type")
	}
	return json.Unmarshal(bytes, d)
}

// DNSProviderCredential is a platform-global (owner-managed) DNS provider account.
// Standalone by design so a future DNS-management feature can reuse it.
type DNSProviderCredential struct {
	ID           uuid.UUID         `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	Name         string            `gorm:"not null" json:"name"`
	ProviderType string            `gorm:"column:provider_type;not null" json:"providerType"`
	// Values inside are individually encrypted by the service layer; never serialized.
	Credentials  DNSCredentialData `gorm:"type:jsonb;not null;default:'{}'" json:"-"`
	CreatedBy    uuid.UUID         `gorm:"type:uuid;not null" json:"createdBy"`
	CreatedAt    time.Time         `gorm:"not null;default:now()" json:"createdAt"`
	UpdatedAt    time.Time         `gorm:"not null;default:now()" json:"updatedAt"`
}

func (DNSProviderCredential) TableName() string { return "dns_provider_credentials" }
```

- [ ] **Step 4: Write the migration** — `migrations/000038_add_dns_provider_credentials.up.sql`

```sql
CREATE TABLE dns_provider_credentials (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    provider_type VARCHAR(64) NOT NULL,
    credentials JSONB NOT NULL DEFAULT '{}',
    created_by UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
```

`migrations/000038_add_dns_provider_credentials.down.sql`:

```sql
DROP TABLE IF EXISTS dns_provider_credentials;
```

- [ ] **Step 5: Implement the repository** — `internal/repository/dns_provider_credential_repository.go`

```go
package repository

import (
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/fastgateway-dev/backend-v2/internal/models"
)

type DNSProviderCredentialRepository struct{ db *gorm.DB }

func NewDNSProviderCredentialRepository(db *gorm.DB) *DNSProviderCredentialRepository {
	return &DNSProviderCredentialRepository{db: db}
}

func (r *DNSProviderCredentialRepository) Create(c *models.DNSProviderCredential) error {
	return r.db.Create(c).Error
}

func (r *DNSProviderCredentialRepository) GetByID(id uuid.UUID) (*models.DNSProviderCredential, error) {
	var c models.DNSProviderCredential
	if err := r.db.Where("id = ?", id).First(&c).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *DNSProviderCredentialRepository) List() ([]models.DNSProviderCredential, error) {
	var out []models.DNSProviderCredential
	if err := r.db.Order("created_at DESC").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func (r *DNSProviderCredentialRepository) Update(c *models.DNSProviderCredential) error {
	return r.db.Save(c).Error
}

func (r *DNSProviderCredentialRepository) Delete(id uuid.UUID) error {
	return r.db.Delete(&models.DNSProviderCredential{}, "id = ?", id).Error
}
```

- [ ] **Step 6: Add the interface + assertion** — in `internal/repository/interfaces.go`

```go
type DNSProviderCredentialRepositoryInterface interface {
	Create(c *models.DNSProviderCredential) error
	GetByID(id uuid.UUID) (*models.DNSProviderCredential, error)
	List() ([]models.DNSProviderCredential, error)
	Update(c *models.DNSProviderCredential) error
	Delete(id uuid.UUID) error
}

var _ DNSProviderCredentialRepositoryInterface = (*DNSProviderCredentialRepository)(nil)
```

- [ ] **Step 7: Run tests + build**

Run: `go test ./internal/models/ -run TestDNSCredentialData -v && go build ./...`
Expected: PASS, build OK.

- [ ] **Step 8: Commit**

```bash
git add migrations/000038_* internal/models/dns_provider_credential.go internal/models/dns_provider_credential_test.go internal/repository/dns_provider_credential_repository.go internal/repository/interfaces.go
git commit -m "feat(certs): DNSProviderCredential model, migration, repository"
```

---

## Task 3: DNSProviderCredential — service, handler, owner routes

**Files:**
- Create: `internal/services/dns_credential_service.go`, `..._test.go`
- Create: `internal/handlers/dns_credential_handler.go`
- Modify: `internal/handlers/service_interfaces.go`, `cmd/server/main.go`, `.mockery.yml`

**Interfaces:**
- Consumes: `repository.DNSProviderCredentialRepositoryInterface`, `*config.Config`.
- Produces:
  - `services.DNSCredentialServiceDeps{ Repo, Config }` + `services.NewDNSCredentialService(deps) *DNSCredentialService`.
  - `services.CreateDNSCredentialInput{ Name, ProviderType string; Credentials map[string]string }`
  - Methods: `Create(input, createdBy) (*models.DNSProviderCredential, error)`, `List() ([]models.DNSProviderCredential, error)`, `GetByID(id) (*models.DNSProviderCredential, error)`, `Update(id, input) (*models.DNSProviderCredential, error)`, `Delete(id) error`.
  - `handlers.NewDNSCredentialHandler(service DNSCredentialServiceInterface) *DNSCredentialHandler`.
- Note: `Delete`'s "referenced by ACME issuer" guard is added in Task 6 (needs the issuer repo). For now `Delete` just deletes.

- [ ] **Step 1: Write the failing test** — `internal/services/dns_credential_service_test.go`

```go
package services_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fastgateway-dev/backend-v2/internal/config"
	"github.com/fastgateway-dev/backend-v2/internal/crypto"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

func TestDNSCredentialService_Create_EncryptsCredentials(t *testing.T) {
	repo := new(mocks.MockDNSProviderCredentialRepository)
	cfg := &config.Config{EncryptionKey: "test-encryption-key-32-bytes-xx!"}
	svc := services.NewDNSCredentialService(services.DNSCredentialServiceDeps{Repo: repo, Config: cfg})

	var saved *models.DNSProviderCredential
	repo.On("Create", mock_anything(&saved)).Return(nil)

	in := &services.CreateDNSCredentialInput{
		Name: "cf-prod", ProviderType: "cloudflare",
		Credentials: map[string]string{"apiToken": "plain-token"},
	}
	out, err := svc.Create(in, uuid.New())
	require.NoError(t, err)

	// stored value must be ciphertext, not the plaintext
	assert.NotEqual(t, "plain-token", out.Credentials["apiToken"])
	dec, err := crypto.Decrypt(out.Credentials["apiToken"], cfg.EncryptionKey)
	require.NoError(t, err)
	assert.Equal(t, "plain-token", dec)

	repo.AssertExpectations(t)
}

func TestNewDNSCredentialService_PanicsOnNilRepo(t *testing.T) {
	assert.Panics(t, func() {
		services.NewDNSCredentialService(services.DNSCredentialServiceDeps{Config: &config.Config{}})
	})
}
```

Add the small capture helper at the bottom of the test file:

```go
// mock_anything captures the *models.DNSProviderCredential passed to Create.
func mock_anything(dst **models.DNSProviderCredential) interface{} {
	return mock.MatchedBy(func(c *models.DNSProviderCredential) bool { *dst = c; return true })
}
```
(import `"github.com/stretchr/testify/mock"`.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/services/ -run TestDNSCredentialService -v`
Expected: FAIL — undefined `services.NewDNSCredentialService`, `mocks.MockDNSProviderCredentialRepository`.

- [ ] **Step 3: Implement the service** — `internal/services/dns_credential_service.go`

```go
package services

import (
	"strings"

	"github.com/google/uuid"

	"github.com/fastgateway-dev/backend-v2/internal/config"
	"github.com/fastgateway-dev/backend-v2/internal/crypto"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
)

type DNSCredentialService struct {
	repo   repository.DNSProviderCredentialRepositoryInterface
	config *config.Config
}

type DNSCredentialServiceDeps struct {
	Repo   repository.DNSProviderCredentialRepositoryInterface
	Config *config.Config
}

func NewDNSCredentialService(deps DNSCredentialServiceDeps) *DNSCredentialService {
	var missing []string
	if deps.Repo == nil {
		missing = append(missing, "Repo")
	}
	if deps.Config == nil {
		missing = append(missing, "Config")
	}
	if len(missing) > 0 {
		panic("services.NewDNSCredentialService: missing required dependency: " + strings.Join(missing, ", "))
	}
	return &DNSCredentialService{repo: deps.Repo, config: deps.Config}
}

type CreateDNSCredentialInput struct {
	Name         string            `json:"name" binding:"required"`
	ProviderType string            `json:"providerType" binding:"required"`
	Credentials  map[string]string `json:"credentials" binding:"required"`
}

var supportedDNSProviders = map[string]bool{"cloudflare": true}

func (s *DNSCredentialService) Create(input *CreateDNSCredentialInput, createdBy uuid.UUID) (*models.DNSProviderCredential, error) {
	if !supportedDNSProviders[input.ProviderType] {
		return nil, &ValidationError{Msg: "unsupported DNS provider: " + input.ProviderType}
	}
	enc := make(models.DNSCredentialData, len(input.Credentials))
	for k, v := range input.Credentials {
		ct, err := crypto.Encrypt(v, s.config.EncryptionKey)
		if err != nil {
			return nil, err
		}
		enc[k] = ct
	}
	c := &models.DNSProviderCredential{
		Name: input.Name, ProviderType: input.ProviderType,
		Credentials: enc, CreatedBy: createdBy,
	}
	if err := s.repo.Create(c); err != nil {
		return nil, err
	}
	return c, nil
}

func (s *DNSCredentialService) List() ([]models.DNSProviderCredential, error) { return s.repo.List() }
func (s *DNSCredentialService) GetByID(id uuid.UUID) (*models.DNSProviderCredential, error) {
	return s.repo.GetByID(id)
}
func (s *DNSCredentialService) Delete(id uuid.UUID) error { return s.repo.Delete(id) }

// DecryptedCredentials returns the plaintext credential map (service-internal;
// used by the issuer service to build the DNS-01 solver Secret).
func (s *DNSCredentialService) DecryptedCredentials(id uuid.UUID) (string, map[string]string, error) {
	c, err := s.repo.GetByID(id)
	if err != nil {
		return "", nil, err
	}
	out := make(map[string]string, len(c.Credentials))
	for k, v := range c.Credentials {
		pt, err := crypto.Decrypt(v, s.config.EncryptionKey)
		if err != nil {
			return "", nil, err
		}
		out[k] = pt
	}
	return c.ProviderType, out, nil
}
```

If `ValidationError` does not already exist in `internal/services`, add it once (a small typed error returning `.Msg`); otherwise reuse the existing one. (Check `internal/services/errors.go` first; the codebase returns `errors.New(...)` in many places, so `errors.New("unsupported DNS provider: "+input.ProviderType)` is an acceptable substitute if there is no shared type.)

- [ ] **Step 4: Implement the handler** — `internal/handlers/dns_credential_handler.go`

```go
package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/fastgateway-dev/backend-v2/internal/middleware"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

type DNSCredentialHandler struct {
	service DNSCredentialServiceInterface
}

func NewDNSCredentialHandler(service DNSCredentialServiceInterface) *DNSCredentialHandler {
	return &DNSCredentialHandler{service: service}
}

// dnsCredentialResponse never includes credential material.
type dnsCredentialResponse struct {
	ID           uuid.UUID `json:"id"`
	Name         string    `json:"name"`
	ProviderType string    `json:"providerType"`
}

func toDNSCredentialResponse(c *models.DNSProviderCredential) dnsCredentialResponse {
	return dnsCredentialResponse{ID: c.ID, Name: c.Name, ProviderType: c.ProviderType}
}

func (h *DNSCredentialHandler) List(c *gin.Context) {
	items, err := h.service.List()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	resp := make([]dnsCredentialResponse, 0, len(items))
	for i := range items {
		resp = append(resp, toDNSCredentialResponse(&items[i]))
	}
	c.JSON(http.StatusOK, gin.H{"data": resp})
}

func (h *DNSCredentialHandler) Create(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	var input services.CreateDNSCredentialInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	cred, err := h.service.Create(&input, user.ID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, toDNSCredentialResponse(cred))
}

func (h *DNSCredentialHandler) Delete(c *gin.Context) {
	id, err := uuid.Parse(c.Param("dnsCredentialId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid credential ID"})
		return
	}
	if err := h.service.Delete(id); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}
```

- [ ] **Step 5: Add the handler-facing interface** — in `internal/handlers/service_interfaces.go`

```go
type DNSCredentialServiceInterface interface {
	Create(input *services.CreateDNSCredentialInput, createdBy uuid.UUID) (*models.DNSProviderCredential, error)
	List() ([]models.DNSProviderCredential, error)
	GetByID(id uuid.UUID) (*models.DNSProviderCredential, error)
	Delete(id uuid.UUID) error
}
```

- [ ] **Step 6: Register mocks + generate** — add `DNSProviderCredentialRepositoryInterface` and `DNSCredentialServiceInterface` to `.mockery.yml` (same block as the other interfaces), then:

Run: `make mocks`
Expected: `internal/mocks/` gains `MockDNSProviderCredentialRepository`, `MockDNSCredentialService`.

- [ ] **Step 7: Wire routes** — in `cmd/server/main.go`:
  1. Add to `RouterDeps`: `DNSCredentialHandler *handlers.DNSCredentialHandler`.
  2. In `main()` where services/handlers are built: `dnsCredentialService := services.NewDNSCredentialService(services.DNSCredentialServiceDeps{Repo: repository.NewDNSProviderCredentialRepository(db), Config: cfg})` and `dnsCredentialHandler := handlers.NewDNSCredentialHandler(dnsCredentialService)`; pass in the `RouterDeps{...}` literal.
  3. In `setupRouter`, inside `protected`, add the owner-only group:

```go
dnsCreds := protected.Group("/dns/credentials")
dnsCreds.Use(deps.AuthMiddleware.RequireRole("owner"))
{
	dnsCreds.GET("", deps.DNSCredentialHandler.List)
	dnsCreds.POST("", deps.DNSCredentialHandler.Create)
	dnsCreds.DELETE("/:dnsCredentialId", deps.DNSCredentialHandler.Delete)
}
```

(Tasks 6 and 7 add sibling top-level owner-only groups `/certificates/issuers` and its `/grants` sub-routes.)

- [ ] **Step 8: Run tests + build**

Run: `go test ./internal/services/ -run TestDNSCredentialService -v && go build ./... && go test ./cmd/server/ -run TestRouteSpecParity -count=1`
Expected: service tests PASS, build OK. (If `TestRouteSpecParity` fails because the new routes aren't in the OpenAPI spec, that is expected — see the note under Self-Review; add the paths to `docs/openapi` in the OpenAPI task at the end of the phase.)

- [ ] **Step 9: Commit**

```bash
git add internal/services/dns_credential_service.go internal/services/dns_credential_service_test.go internal/handlers/dns_credential_handler.go internal/handlers/service_interfaces.go internal/mocks/ .mockery.yml cmd/server/main.go
git commit -m "feat(certs): DNS credential service, handler, owner-only routes"
```

---

## Task 4: CertificateIssuer — model, migration, repository

**Files:**
- Create: `migrations/000039_add_certificate_issuers.up.sql`, `.down.sql`
- Create: `internal/models/certificate_issuer.go`, `..._test.go`
- Create: `internal/repository/certificate_issuer_repository.go`
- Modify: `internal/repository/interfaces.go`

**Interfaces:**
- Produces:
  - `models.CertificateIssuer` with `Type` (`self_signed_ca`/`acme`), `Status`, `Config models.IssuerConfig` (JSONB, `Value`/`Scan`).
  - `models.IssuerConfig` fields: `CommonName, KeyAlgorithm string; KeySize int; DurationDays int` (CA) and `Server, Email string; EABKeyID string; DNSCredentialID *uuid.UUID` (ACME); plus resolved cert-manager names `ClusterIssuerName, CASecretName string`.
  - `repository.CertificateIssuerRepositoryInterface` + `NewCertificateIssuerRepository(db)`.

- [ ] **Step 1: Write the failing test** — `internal/models/certificate_issuer_test.go`

```go
package models

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIssuerConfig_ValueScanRoundTrip(t *testing.T) {
	id := uuid.New()
	in := IssuerConfig{Server: "https://acme.example/dir", Email: "a@b.com", DNSCredentialID: &id, ClusterIssuerName: "iss-acme-1"}
	v, err := in.Value()
	require.NoError(t, err)
	var out IssuerConfig
	require.NoError(t, out.Scan(v))
	assert.Equal(t, "iss-acme-1", out.ClusterIssuerName)
	require.NotNil(t, out.DNSCredentialID)
	assert.Equal(t, id, *out.DNSCredentialID)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/models/ -run TestIssuerConfig -v`
Expected: FAIL — undefined `IssuerConfig`.

- [ ] **Step 3: Implement the model** — `internal/models/certificate_issuer.go`

```go
package models

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

type IssuerType string

const (
	IssuerTypeSelfSignedCA IssuerType = "self_signed_ca"
	IssuerTypeACME         IssuerType = "acme"
)

type IssuerStatus string

const (
	IssuerStatusPending IssuerStatus = "pending"
	IssuerStatusReady   IssuerStatus = "ready"
	IssuerStatusError   IssuerStatus = "error"
)

// IssuerConfig is the polymorphic JSONB config. CA fields are used when
// Type=self_signed_ca; ACME fields when Type=acme. Resolved cert-manager object
// names are recorded so later phases and deletes can find them.
type IssuerConfig struct {
	// self_signed_ca
	CommonName   string `json:"commonName,omitempty"`
	KeyAlgorithm string `json:"keyAlgorithm,omitempty"`
	KeySize      int    `json:"keySize,omitempty"`
	DurationDays int    `json:"durationDays,omitempty"`
	CASecretName string `json:"caSecretName,omitempty"`

	// acme
	Server          string     `json:"server,omitempty"`
	Email           string     `json:"email,omitempty"`
	EABKeyID        string     `json:"eabKeyId,omitempty"`
	DNSCredentialID *uuid.UUID `json:"dnsCredentialId,omitempty"`

	// resolved cert-manager object names (both types)
	ClusterIssuerName string `json:"clusterIssuerName,omitempty"`
	// for acme: the account key + EAB HMAC secret names
	AccountSecretName string `json:"accountSecretName,omitempty"`
	SolverSecretName  string `json:"solverSecretName,omitempty"`
}

func (c IssuerConfig) Value() (driver.Value, error) { return json.Marshal(c) }
func (c *IssuerConfig) Scan(value interface{}) error {
	if value == nil {
		*c = IssuerConfig{}
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		if s, ok2 := value.(string); ok2 {
			b = []byte(s)
		} else {
			return errors.New("failed to scan IssuerConfig")
		}
	}
	return json.Unmarshal(b, c)
}

type CertificateIssuer struct {
	ID            uuid.UUID    `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	Name          string       `gorm:"not null" json:"name"`
	Type          IssuerType   `gorm:"not null" json:"type"`
	Status        IssuerStatus `gorm:"not null;default:'pending'" json:"status"`
	StatusMessage string       `gorm:"column:status_message" json:"statusMessage,omitempty"`
	Config        IssuerConfig `gorm:"type:jsonb;not null;default:'{}'" json:"config"`
	CreatedBy     uuid.UUID    `gorm:"type:uuid;not null" json:"createdBy"`
	CreatedAt     time.Time    `gorm:"not null;default:now()" json:"createdAt"`
	UpdatedAt     time.Time    `gorm:"not null;default:now()" json:"updatedAt"`
}

func (CertificateIssuer) TableName() string { return "certificate_issuers" }
```

- [ ] **Step 4: Write the migration** — `migrations/000039_add_certificate_issuers.up.sql`

```sql
CREATE TABLE certificate_issuers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    type VARCHAR(32) NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    status_message TEXT,
    config JSONB NOT NULL DEFAULT '{}',
    created_by UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_certificate_issuers_type ON certificate_issuers(type);
```

`.down.sql`:

```sql
DROP TABLE IF EXISTS certificate_issuers;
```

- [ ] **Step 5: Implement the repository** — `internal/repository/certificate_issuer_repository.go` (mirror Task 2's repo: `Create/GetByID/List/Update/Delete`, plus `CountByDNSCredential(dnsCredentialID uuid.UUID) (int64, error)` for the DNS-cred delete guard):

```go
func (r *CertificateIssuerRepository) CountByDNSCredential(dnsCredentialID uuid.UUID) (int64, error) {
	var n int64
	err := r.db.Model(&models.CertificateIssuer{}).
		Where("config->>'dnsCredentialId' = ?", dnsCredentialID.String()).
		Count(&n).Error
	return n, err
}
```

(Full file: same shape as `DNSProviderCredentialRepository` for the CRUD methods.)

- [ ] **Step 6: Add interface + assertion** to `internal/repository/interfaces.go` (`Create/GetByID/List/Update/Delete/CountByDNSCredential`; `var _ CertificateIssuerRepositoryInterface = (*CertificateIssuerRepository)(nil)`).

- [ ] **Step 7: Run tests + build**

Run: `go test ./internal/models/ -run TestIssuerConfig -v && go build ./...`
Expected: PASS, build OK.

- [ ] **Step 8: Commit**

```bash
git add migrations/000039_* internal/models/certificate_issuer.go internal/models/certificate_issuer_test.go internal/repository/certificate_issuer_repository.go internal/repository/interfaces.go
git commit -m "feat(certs): CertificateIssuer model, migration, repository"
```

---

## Task 5: cert-manager CRD builders + control-plane apply methods

**Files:**
- Create: `internal/kubernetes/certmanager.go`, `internal/kubernetes/certmanager_test.go`
- Create golden dir: `internal/kubernetes/testdata/golden/certmanager/`
- Create: `internal/services/certinfra_roles.go` (the role interface the issuer service consumes)
- Modify: `internal/cluster/controlplane.go` (typed apply helpers)

**Interfaces:**
- Produces (pure builders, no I/O):
  - `kubernetes.SelfSignedClusterIssuer(name string) *unstructured.Unstructured`
  - `kubernetes.CACertificate(cfg kubernetes.CACertConfig) *unstructured.Unstructured` where `CACertConfig{Name, Namespace, CommonName, SecretName, SelfSignedIssuerName, KeyAlgorithm string; KeySize, DurationDays int}`
  - `kubernetes.CAClusterIssuer(name, caSecretName string) *unstructured.Unstructured`
  - `kubernetes.ACMEClusterIssuer(cfg kubernetes.ACMEIssuerConfig) *unstructured.Unstructured` where `ACMEIssuerConfig{Name, Server, Email, AccountSecretName, EABKeyID, EABSecretName, ProviderType, SolverSecretName string}`
  - `kubernetes.CloudflareSolverSecret(name, namespace, apiToken string) *unstructured.Unstructured`
- Produces (control-plane role, in `internal/services/certinfra_roles.go`):
  - `type CertInfraApplier interface { ApplyClusterScoped(ctx, gvr, obj) error; ApplyNamespaced(ctx, gvr, obj) error; Get(ctx, gvr, name, namespaced) (*unstructured.Unstructured, error); Delete(ctx, gvr, name, namespaced) error; Namespace() string }` — satisfied by `*cluster.ControlPlaneClient`.

- [ ] **Step 1: Write the failing golden test** — `internal/kubernetes/certmanager_test.go`

```go
package kubernetes_test

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
)

var updateGolden = flag.Bool("update-golden", false, "rewrite golden files")

func assertGolden(t *testing.T, name string, obj any) {
	t.Helper()
	got, err := yaml.Marshal(obj)
	require.NoError(t, err)
	path := filepath.Join("testdata", "golden", "certmanager", name+".yaml")
	if *updateGolden {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, got, 0o644))
		return
	}
	want, err := os.ReadFile(path)
	require.NoErrorf(t, err, "missing golden %s; regenerate with -update-golden", path)
	assert.Equal(t, string(want), string(got))
}

func TestCACertificate_Golden(t *testing.T) {
	obj := kubernetes.CACertificate(kubernetes.CACertConfig{
		Name: "ca-abc", Namespace: "fastgateway-system", CommonName: "FastGateway Root",
		SecretName: "ca-abc", SelfSignedIssuerName: "fgw-selfsigned",
		KeyAlgorithm: "RSA", KeySize: 4096, DurationDays: 3650,
	})
	assert.Equal(t, "cert-manager.io/v1", obj.Object["apiVersion"])
	assert.Equal(t, "Certificate", obj.Object["kind"])
	assertGolden(t, "ca-certificate", obj)
}

func TestACMEClusterIssuer_Golden(t *testing.T) {
	obj := kubernetes.ACMEClusterIssuer(kubernetes.ACMEIssuerConfig{
		Name: "iss-acme", Server: "https://acme-v02.api.letsencrypt.org/directory",
		Email: "ops@example.com", AccountSecretName: "iss-acme-account",
		ProviderType: "cloudflare", SolverSecretName: "iss-acme-cf",
	})
	assert.Equal(t, "ClusterIssuer", obj.Object["kind"])
	assertGolden(t, "acme-clusterissuer", obj)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/kubernetes/ -run 'TestCACertificate_Golden|TestACMEClusterIssuer_Golden' -v`
Expected: FAIL — undefined builders.

- [ ] **Step 3: Implement the builders** — `internal/kubernetes/certmanager.go`

```go
package kubernetes

import "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

func managedByLabels() map[string]interface{} {
	return map[string]interface{}{"app.kubernetes.io/managed-by": "fastgateway"}
}

func SelfSignedClusterIssuer(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "cert-manager.io/v1", "kind": "ClusterIssuer",
		"metadata": map[string]interface{}{"name": name, "labels": managedByLabels()},
		"spec":     map[string]interface{}{"selfSigned": map[string]interface{}{}},
	}}
}

type CACertConfig struct {
	Name, Namespace, CommonName, SecretName, SelfSignedIssuerName, KeyAlgorithm string
	KeySize, DurationDays                                                       int
}

func CACertificate(cfg CACertConfig) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "cert-manager.io/v1", "kind": "Certificate",
		"metadata": map[string]interface{}{"name": cfg.Name, "namespace": cfg.Namespace, "labels": managedByLabels()},
		"spec": map[string]interface{}{
			"isCA":       true,
			"commonName": cfg.CommonName,
			"secretName": cfg.SecretName,
			"duration":   hoursDuration(cfg.DurationDays),
			"privateKey": map[string]interface{}{"algorithm": cfg.KeyAlgorithm, "size": int64(cfg.KeySize)},
			"issuerRef": map[string]interface{}{
				"name": cfg.SelfSignedIssuerName, "kind": "ClusterIssuer", "group": "cert-manager.io",
			},
		},
	}}
}

func CAClusterIssuer(name, caSecretName string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "cert-manager.io/v1", "kind": "ClusterIssuer",
		"metadata": map[string]interface{}{"name": name, "labels": managedByLabels()},
		"spec":     map[string]interface{}{"ca": map[string]interface{}{"secretName": caSecretName}},
	}}
}

type ACMEIssuerConfig struct {
	Name, Server, Email, AccountSecretName, EABKeyID, EABSecretName, ProviderType, SolverSecretName string
}

func ACMEClusterIssuer(cfg ACMEIssuerConfig) *unstructured.Unstructured {
	acme := map[string]interface{}{
		"server": cfg.Server, "email": cfg.Email,
		"privateKeySecretRef": map[string]interface{}{"name": cfg.AccountSecretName},
		"solvers":             []interface{}{dns01Solver(cfg.ProviderType, cfg.SolverSecretName)},
	}
	if cfg.EABKeyID != "" {
		acme["externalAccountBinding"] = map[string]interface{}{
			"keyID":         cfg.EABKeyID,
			"keySecretRef":  map[string]interface{}{"name": cfg.EABSecretName, "key": "hmacKey"},
		}
	}
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "cert-manager.io/v1", "kind": "ClusterIssuer",
		"metadata": map[string]interface{}{"name": cfg.Name, "labels": managedByLabels()},
		"spec":     map[string]interface{}{"acme": acme},
	}}
}

// dns01Solver currently supports cloudflare; extend the switch for more providers.
func dns01Solver(providerType, solverSecretName string) map[string]interface{} {
	switch providerType {
	case "cloudflare":
		return map[string]interface{}{"dns01": map[string]interface{}{
			"cloudflare": map[string]interface{}{
				"apiTokenSecretRef": map[string]interface{}{"name": solverSecretName, "key": "apiToken"},
			},
		}}
	default:
		return map[string]interface{}{"dns01": map[string]interface{}{}}
	}
}

func CloudflareSolverSecret(name, namespace, apiToken string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1", "kind": "Secret",
		"metadata":   map[string]interface{}{"name": name, "namespace": namespace, "labels": managedByLabels()},
		"type":       "Opaque",
		"stringData": map[string]interface{}{"apiToken": apiToken},
	}}
}

func hoursDuration(days int) string {
	// cert-manager accepts Go duration strings; days -> hours.
	return itoa(days*24) + "h"
}
```

Add a tiny `itoa` helper or use `strconv.Itoa` (import `strconv` and replace `itoa(...)` with `strconv.Itoa(...)`).

- [ ] **Step 4: Define the control-plane role interface** — `internal/services/certinfra_roles.go`

```go
package services

import (
	"context"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// CertInfraApplier is the narrow control-plane role the certificate-infra
// services use. Satisfied by *cluster.ControlPlaneClient.
type CertInfraApplier interface {
	ApplyClusterScoped(ctx context.Context, gvr schema.GroupVersionResource, obj *unstructured.Unstructured) error
	ApplyNamespaced(ctx context.Context, gvr schema.GroupVersionResource, obj *unstructured.Unstructured) error
	Get(ctx context.Context, gvr schema.GroupVersionResource, name string, namespaced bool) (*unstructured.Unstructured, error)
	Delete(ctx context.Context, gvr schema.GroupVersionResource, name string, namespaced bool) error
	Namespace() string
}
```

- [ ] **Step 5: Generate golden files + run**

Run: `go test ./internal/kubernetes/ -run 'Golden' -update-golden && go test ./internal/kubernetes/ -run 'Golden' -v && go build ./...`
Expected: golden files written, then PASS, build OK. **Inspect the generated golden YAML** to confirm the CRD shapes are valid cert-manager (isCA, issuerRef, acme.solvers[0].dns01.cloudflare).

- [ ] **Step 6: Commit**

```bash
git add internal/kubernetes/certmanager.go internal/kubernetes/certmanager_test.go internal/kubernetes/testdata/golden/certmanager/ internal/services/certinfra_roles.go
git commit -m "feat(certs): cert-manager CRD builders + control-plane role interface"
```

---

## Task 6: CertificateIssuer service + handler + owner routes (+ DNS-cred delete guard)

**Files:**
- Create: `internal/services/certificate_issuer_service.go`, `..._test.go`
- Create: `internal/handlers/certificate_issuer_handler.go`
- Modify: `internal/services/dns_credential_service.go` (add the referential guard), `internal/handlers/service_interfaces.go`, `cmd/server/main.go`, `.mockery.yml`

**Interfaces:**
- Consumes: `repository.CertificateIssuerRepositoryInterface`, `*DNSCredentialService` (for `DecryptedCredentials`), `CertInfraApplier`, `*config.Config`.
- Produces:
  - `CertificateIssuerServiceDeps{ Repo, DNSCreds, ControlPlane, Config }` + `NewCertificateIssuerService(deps)`.
  - `CreateIssuerInput{ Type; Name; CommonName; KeyAlgorithm; KeySize; DurationDays; Server; Email; EABKeyID; EABHMACKey; DNSCredentialID }`.
  - `Create(input, createdBy) (*models.CertificateIssuer, error)` — self-signed CA flow or ACME flow, applying CRDs via `ControlPlane`.
  - `List`, `GetByID`, `Delete(id) error` (delete removes cert-manager objects + row).
  - `DNSCredentialService.SetIssuerRepo(repo)` or add `IssuerRepo` to `DNSCredentialServiceDeps` so `Delete` can enforce the guard.

- [ ] **Step 1: Write the failing test** — `internal/services/certificate_issuer_service_test.go`

```go
package services_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/fastgateway-dev/backend-v2/internal/config"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

func TestCertificateIssuerService_CreateSelfSignedCA_AppliesCRDs(t *testing.T) {
	repo := new(mocks.MockCertificateIssuerRepository)
	applier := new(mocks.MockCertInfraApplier)
	cfg := &config.Config{EncryptionKey: "test-encryption-key-32-bytes-xx!"}
	dnsSvc := services.NewDNSCredentialService(services.DNSCredentialServiceDeps{Repo: new(mocks.MockDNSProviderCredentialRepository), Config: cfg, IssuerRepo: repo})
	svc := services.NewCertificateIssuerService(services.CertificateIssuerServiceDeps{
		Repo: repo, DNSCreds: dnsSvc, ControlPlane: applier, Config: cfg,
	})

	applier.On("Namespace").Return("fastgateway-system")
	applier.On("ApplyClusterScoped", mock.Anything, mock.AnythingOfType("schema.GroupVersionResource"), mock.AnythingOfType("*unstructured.Unstructured")).Return(nil)
	applier.On("ApplyNamespaced", mock.Anything, mock.AnythingOfType("schema.GroupVersionResource"), mock.AnythingOfType("*unstructured.Unstructured")).Return(nil)
	repo.On("Create", mock.AnythingOfType("*models.CertificateIssuer")).Return(nil)

	out, err := svc.Create(&services.CreateIssuerInput{
		Type: "self_signed_ca", Name: "root", CommonName: "FGW Root",
		KeyAlgorithm: "RSA", KeySize: 4096, DurationDays: 3650,
	}, uuid.New())
	require.NoError(t, err)
	assert.Equal(t, models.IssuerTypeSelfSignedCA, out.Type)
	// self-signed CA applies: SelfSigned ClusterIssuer + CA ClusterIssuer (cluster-scoped) and CA Certificate (namespaced)
	applier.AssertNumberOfCalls(t, "ApplyClusterScoped", 2)
	applier.AssertNumberOfCalls(t, "ApplyNamespaced", 1)
	repo.AssertExpectations(t)

	_ = context.Background()
	_ = unstructured.Unstructured{}
	_ = schema.GroupVersionResource{}
}

func TestDNSCredentialService_Delete_BlockedWhenReferenced(t *testing.T) {
	dnsRepo := new(mocks.MockDNSProviderCredentialRepository)
	issRepo := new(mocks.MockCertificateIssuerRepository)
	cfg := &config.Config{EncryptionKey: "test-encryption-key-32-bytes-xx!"}
	svc := services.NewDNSCredentialService(services.DNSCredentialServiceDeps{Repo: dnsRepo, Config: cfg, IssuerRepo: issRepo})

	id := uuid.New()
	issRepo.On("CountByDNSCredential", id).Return(int64(2), nil)
	err := svc.Delete(id)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "in use")
	dnsRepo.AssertNotCalled(t, "Delete", mock.Anything)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/services/ -run 'TestCertificateIssuerService|TestDNSCredentialService_Delete_Blocked' -v`
Expected: FAIL — undefined service + `IssuerRepo` field + mocks.

- [ ] **Step 3: Add the DNS-cred delete guard** — extend `DNSCredentialServiceDeps` with `IssuerRepo repository.CertificateIssuerRepositoryInterface` (add to the nil-check + struct), and change `Delete`:

```go
func (s *DNSCredentialService) Delete(id uuid.UUID) error {
	n, err := s.issuerRepo.CountByDNSCredential(id)
	if err != nil {
		return err
	}
	if n > 0 {
		return errors.New("DNS credential is in use by an ACME issuer")
	}
	return s.repo.Delete(id)
}
```
(import `errors`; store `issuerRepo` on the struct.)

- [ ] **Step 4: Implement the issuer service** — `internal/services/certificate_issuer_service.go`

```go
package services

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/fastgateway-dev/backend-v2/internal/config"
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
)

type CertificateIssuerService struct {
	repo         repository.CertificateIssuerRepositoryInterface
	dnsCreds     *DNSCredentialService
	controlPlane CertInfraApplier
	config       *config.Config
}

type CertificateIssuerServiceDeps struct {
	Repo         repository.CertificateIssuerRepositoryInterface
	DNSCreds     *DNSCredentialService
	ControlPlane CertInfraApplier
	Config       *config.Config
}

func NewCertificateIssuerService(deps CertificateIssuerServiceDeps) *CertificateIssuerService {
	var missing []string
	if deps.Repo == nil {
		missing = append(missing, "Repo")
	}
	if deps.DNSCreds == nil {
		missing = append(missing, "DNSCreds")
	}
	if deps.ControlPlane == nil {
		missing = append(missing, "ControlPlane")
	}
	if deps.Config == nil {
		missing = append(missing, "Config")
	}
	if len(missing) > 0 {
		panic("services.NewCertificateIssuerService: missing required dependency: " + strings.Join(missing, ", "))
	}
	return &CertificateIssuerService{repo: deps.Repo, dnsCreds: deps.DNSCreds, controlPlane: deps.ControlPlane, config: deps.Config}
}

type CreateIssuerInput struct {
	Type         string     `json:"type" binding:"required"`
	Name         string     `json:"name" binding:"required"`
	CommonName   string     `json:"commonName"`
	KeyAlgorithm string     `json:"keyAlgorithm"`
	KeySize      int        `json:"keySize"`
	DurationDays int        `json:"durationDays"`
	Server       string     `json:"server"`
	Email        string     `json:"email"`
	EABKeyID     string     `json:"eabKeyId"`
	EABHMACKey   string     `json:"eabHmacKey"`
	DNSCredentialID *uuid.UUID `json:"dnsCredentialId"`
}

const selfSignedClusterIssuer = "fgw-selfsigned"

func (s *CertificateIssuerService) Create(input *CreateIssuerInput, createdBy uuid.UUID) (*models.CertificateIssuer, error) {
	iss := &models.CertificateIssuer{Name: input.Name, Type: models.IssuerType(input.Type), CreatedBy: createdBy, Status: models.IssuerStatusPending}
	ctx := context.Background()
	ns := s.controlPlane.Namespace()

	switch models.IssuerType(input.Type) {
	case models.IssuerTypeSelfSignedCA:
		if input.CommonName == "" {
			return nil, errors.New("commonName is required for a self-signed CA")
		}
		if input.KeyAlgorithm == "" {
			input.KeyAlgorithm = "RSA"
		}
		if input.KeySize == 0 {
			input.KeySize = 4096
		}
		if input.DurationDays == 0 {
			input.DurationDays = 3650
		}
		if err := s.repo.Create(iss); err != nil { // persist first for a stable ID-derived name
			return nil, err
		}
		caSecret := "ca-" + iss.ID.String()
		clusterIssuer := "iss-" + iss.ID.String()

		if err := s.controlPlane.ApplyClusterScoped(ctx, kubernetes.CertManagerClusterIssuerGVR, kubernetes.SelfSignedClusterIssuer(selfSignedClusterIssuer)); err != nil {
			return s.markError(iss, err)
		}
		caCert := kubernetes.CACertificate(kubernetes.CACertConfig{
			Name: caSecret, Namespace: ns, CommonName: input.CommonName, SecretName: caSecret,
			SelfSignedIssuerName: selfSignedClusterIssuer, KeyAlgorithm: input.KeyAlgorithm, KeySize: input.KeySize, DurationDays: input.DurationDays,
		})
		if err := s.controlPlane.ApplyNamespaced(ctx, kubernetes.CertManagerCertificateGVR, caCert); err != nil {
			return s.markError(iss, err)
		}
		if err := s.controlPlane.ApplyClusterScoped(ctx, kubernetes.CertManagerClusterIssuerGVR, kubernetes.CAClusterIssuer(clusterIssuer, caSecret)); err != nil {
			return s.markError(iss, err)
		}
		iss.Config = models.IssuerConfig{
			CommonName: input.CommonName, KeyAlgorithm: input.KeyAlgorithm, KeySize: input.KeySize,
			DurationDays: input.DurationDays, CASecretName: caSecret, ClusterIssuerName: clusterIssuer,
		}

	case models.IssuerTypeACME:
		if input.Server == "" || input.Email == "" || input.DNSCredentialID == nil {
			return nil, errors.New("server, email and dnsCredentialId are required for an ACME issuer")
		}
		providerType, creds, err := s.dnsCreds.DecryptedCredentials(*input.DNSCredentialID)
		if err != nil {
			return nil, fmt.Errorf("resolve DNS credential: %w", err)
		}
		if err := s.repo.Create(iss); err != nil {
			return nil, err
		}
		clusterIssuer := "iss-" + iss.ID.String()
		accountSecret := clusterIssuer + "-account"
		solverSecret := clusterIssuer + "-solver"

		if providerType == "cloudflare" {
			solver := kubernetes.CloudflareSolverSecret(solverSecret, ns, creds["apiToken"])
			if err := s.controlPlane.ApplyNamespaced(ctx, kubernetes.SecretGVR(), solver); err != nil {
				return s.markError(iss, err)
			}
		}
		acme := kubernetes.ACMEClusterIssuer(kubernetes.ACMEIssuerConfig{
			Name: clusterIssuer, Server: input.Server, Email: input.Email,
			AccountSecretName: accountSecret, ProviderType: providerType, SolverSecretName: solverSecret,
		})
		if err := s.controlPlane.ApplyClusterScoped(ctx, kubernetes.CertManagerClusterIssuerGVR, acme); err != nil {
			return s.markError(iss, err)
		}
		iss.Config = models.IssuerConfig{
			Server: input.Server, Email: input.Email, EABKeyID: input.EABKeyID, DNSCredentialID: input.DNSCredentialID,
			ClusterIssuerName: clusterIssuer, AccountSecretName: accountSecret, SolverSecretName: solverSecret,
		}
	default:
		return nil, errors.New("unsupported issuer type: " + input.Type)
	}

	iss.Status = models.IssuerStatusReady
	iss.StatusMessage = "Issuer created"
	_ = s.repo.Update(iss)
	return iss, nil
}

func (s *CertificateIssuerService) markError(iss *models.CertificateIssuer, cause error) (*models.CertificateIssuer, error) {
	iss.Status = models.IssuerStatusError
	iss.StatusMessage = cause.Error()
	_ = s.repo.Update(iss)
	return iss, nil // persisted with error status; surfaced to caller as 200 + error status (matches domain_service)
}

func (s *CertificateIssuerService) List() ([]models.CertificateIssuer, error) { return s.repo.List() }
func (s *CertificateIssuerService) GetByID(id uuid.UUID) (*models.CertificateIssuer, error) {
	return s.repo.GetByID(id)
}

func (s *CertificateIssuerService) Delete(id uuid.UUID) error {
	iss, err := s.repo.GetByID(id)
	if err != nil {
		return err
	}
	ctx := context.Background()
	if iss.Config.ClusterIssuerName != "" {
		_ = s.controlPlane.Delete(ctx, kubernetes.CertManagerClusterIssuerGVR, iss.Config.ClusterIssuerName, false)
	}
	if iss.Config.CASecretName != "" {
		_ = s.controlPlane.Delete(ctx, kubernetes.CertManagerCertificateGVR, iss.Config.CASecretName, true)
	}
	return s.repo.Delete(id)
}
```

Add a small `kubernetes.SecretGVR()` helper (or reuse an existing core Secret GVR if present) returning `schema.GroupVersionResource{Version: "v1", Resource: "secrets"}`.

Note: the issuer-delete "referenced by a ManagedCertificate" guard (**409**) is added in **Phase 2** when the `ManagedCertificate` repo exists.

- [ ] **Step 5: Implement the handler** — `internal/handlers/certificate_issuer_handler.go` (mirror the DNS handler: `List`, `Create` (201 with the model — it already omits key material via the model's `json` tags), `Get`, `Delete` (409 on error). Add `Status` reading via the service if desired; the `GET /issuers/:issuerId/status` route can call `service.GetByID` and return `{status, statusMessage}`.)

- [ ] **Step 6: Add handler interface + mocks + wire routes**
  - Add `CertificateIssuerServiceInterface` to `internal/handlers/service_interfaces.go`.
  - Add `CertificateIssuerRepositoryInterface`, `CertInfraApplier`, `CertificateIssuerServiceInterface` to `.mockery.yml`; run `make mocks`.
  - In `cmd/server/main.go`: build the control-plane client once — `cpDyn, err := cluster.InClusterDynamicClient()` (guarded: if `!services.IsRunningInCluster()`, log a warning and skip cert-infra route registration, so local/dev still boots); `controlPlane := cluster.NewControlPlaneClient(cpDyn, cfg.ControlPlaneNamespace)`. Construct `certificateIssuerService` + `dnsCredentialService` (now with `IssuerRepo`) + handlers; add handler fields to `RouterDeps`; register a top-level owner-only `/certificates/issuers` group:

```go
issuers := protected.Group("/certificates/issuers")
issuers.Use(deps.AuthMiddleware.RequireRole("owner"))
{
	issuers.GET("", deps.CertificateIssuerHandler.List)
	issuers.POST("", deps.CertificateIssuerHandler.Create)
	issuers.GET("/:issuerId", deps.CertificateIssuerHandler.Get)
	issuers.DELETE("/:issuerId", deps.CertificateIssuerHandler.Delete)
	issuers.GET("/:issuerId/status", deps.CertificateIssuerHandler.Status)
}
```

- [ ] **Step 7: Run tests + build**

Run: `go test ./internal/services/ -run 'TestCertificateIssuerService|TestDNSCredentialService' -v && go build ./... && make mocks-check`
Expected: PASS, build OK, mock drift check clean.

- [ ] **Step 8: Commit**

```bash
git add internal/services/certificate_issuer_service.go internal/services/certificate_issuer_service_test.go internal/services/dns_credential_service.go internal/handlers/certificate_issuer_handler.go internal/handlers/service_interfaces.go internal/mocks/ .mockery.yml cmd/server/main.go
git commit -m "feat(certs): issuer service (self-signed CA + ACME) + DNS-cred delete guard"
```

---

## Task 7: IssuerProjectGrant — model, migration, repository, service, handler, routes

**Files:**
- Create: `migrations/000040_add_issuer_project_grants.up.sql`, `.down.sql`
- Create: `internal/models/issuer_project_grant.go`
- Create: `internal/repository/issuer_project_grant_repository.go`
- Create: `internal/services/issuer_grant_service.go`, `..._test.go`
- Create: `internal/handlers/issuer_grant_handler.go`
- Modify: `internal/repository/interfaces.go`, `internal/handlers/service_interfaces.go`, `cmd/server/main.go`, `.mockery.yml`

**Interfaces:**
- Produces:
  - `models.IssuerProjectGrant{ ID, IssuerID, ProjectID uuid.UUID, CreatedBy, CreatedAt }`, unique `(issuer_id, project_id)`.
  - `repository.IssuerProjectGrantRepositoryInterface`: `Create`, `ListByIssuer(issuerID) ([]models.IssuerProjectGrant, error)`, `Delete(issuerID, projectID) error`, `Exists(issuerID, projectID) (bool, error)`, `ListProjectIDsForIssuer(issuerID) ([]uuid.UUID, error)`.
  - `IssuerGrantServiceDeps{ GrantRepo, IssuerRepo, ProjectRepo }` + `NewIssuerGrantService`; `Grant(issuerID, projectID, by) error`, `ListGrants(issuerID)`, `Revoke(issuerID, projectID) error`.

- [ ] **Step 1: Write the failing test** — `internal/services/issuer_grant_service_test.go`

```go
package services_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

func TestIssuerGrantService_Grant_ValidatesIssuerAndProject(t *testing.T) {
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	issRepo := new(mocks.MockCertificateIssuerRepository)
	projRepo := new(mocks.MockProjectRepository)
	svc := services.NewIssuerGrantService(services.IssuerGrantServiceDeps{GrantRepo: grantRepo, IssuerRepo: issRepo, ProjectRepo: projRepo})

	issID, projID, by := uuid.New(), uuid.New(), uuid.New()
	issRepo.On("GetByID", issID).Return(&models.CertificateIssuer{ID: issID}, nil)
	projRepo.On("GetByID", projID).Return(&models.Project{ID: projID}, nil)
	grantRepo.On("Exists", issID, projID).Return(false, nil)
	grantRepo.On("Create", mock.AnythingOfType("*models.IssuerProjectGrant")).Return(nil)

	require.NoError(t, svc.Grant(issID, projID, by))
	grantRepo.AssertExpectations(t)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/services/ -run TestIssuerGrantService -v`
Expected: FAIL — undefined service + mocks.

- [ ] **Step 3: Implement model + migration** — `internal/models/issuer_project_grant.go`:

```go
package models

import (
	"time"

	"github.com/google/uuid"
)

type IssuerProjectGrant struct {
	ID        uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	IssuerID  uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_issuer_project" json:"issuerId"`
	ProjectID uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_issuer_project" json:"projectId"`
	CreatedBy uuid.UUID `gorm:"type:uuid;not null" json:"createdBy"`
	CreatedAt time.Time `gorm:"not null;default:now()" json:"createdAt"`
}

func (IssuerProjectGrant) TableName() string { return "issuer_project_grants" }
```

`migrations/000040_add_issuer_project_grants.up.sql`:

```sql
CREATE TABLE issuer_project_grants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    issuer_id UUID NOT NULL REFERENCES certificate_issuers(id) ON DELETE CASCADE,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    created_by UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE (issuer_id, project_id)
);

CREATE INDEX idx_issuer_project_grants_project ON issuer_project_grants(project_id);
```

`.down.sql`:

```sql
DROP TABLE IF EXISTS issuer_project_grants;
```

- [ ] **Step 4: Implement repository + interface** — mirror prior repos; include `Exists`, `ListByIssuer`, `Delete(issuerID, projectID)`, `ListProjectIDsForIssuer`. Add interface + `var _` assertion to `interfaces.go`.

- [ ] **Step 5: Implement service** — `internal/services/issuer_grant_service.go` (panic-on-nil constructor; `Grant` validates issuer + project exist, dedupes via `Exists`, then `Create`; `Revoke` deletes; `ListGrants` returns rows). Revoke's "cert in that project still uses the issuer" **409 guard is deferred to Phase 2** (needs `ManagedCertificate`); add a `// Phase 2: block revoke if a ManagedCertificate in projectID uses issuerID` comment at the revoke site.

- [ ] **Step 6: Implement handler + wire routes** — `internal/handlers/issuer_grant_handler.go` (`ListGrants`, `Grant` (body `{projectId}`), `Revoke`). Register under `issuers`:

```go
issuers.GET("/:issuerId/grants", deps.IssuerGrantHandler.List)
issuers.POST("/:issuerId/grants", deps.IssuerGrantHandler.Grant)
issuers.DELETE("/:issuerId/grants/:projectId", deps.IssuerGrantHandler.Revoke)
```
Add `IssuerGrantServiceInterface` to `service_interfaces.go`; add repo + service interfaces to `.mockery.yml`; `make mocks`.

- [ ] **Step 7: Run tests + build + mock check**

Run: `go test ./internal/services/ -run TestIssuerGrantService -v && go build ./... && make mocks-check`
Expected: PASS, OK, clean.

- [ ] **Step 8: Commit**

```bash
git add migrations/000040_* internal/models/issuer_project_grant.go internal/repository/issuer_project_grant_repository.go internal/repository/interfaces.go internal/services/issuer_grant_service.go internal/services/issuer_grant_service_test.go internal/handlers/issuer_grant_handler.go internal/handlers/service_interfaces.go internal/mocks/ .mockery.yml cmd/server/main.go
git commit -m "feat(certs): issuer-project grants (model, service, owner routes)"
```

---

## Task 8: OpenAPI spec + full-phase verification

**Files:**
- Modify: `docs/openapi/paths/*.yaml` (+ new `paths/certificates.yaml`), `docs/openapi/schemas/*.yaml`, root `docs/openapi/openapi.yaml`
- Regenerate: `cmd/server/openapi.yaml` (bundle)

**Interfaces:** Consumes the Phase 1 API contract (`…-managed-certificates-api.md`, Phase 1 section).

- [ ] **Step 1: Add the 11 Phase 1 paths + schemas** to `docs/openapi/` (DNS credentials ×3, issuers ×5, grants ×3), matching the shapes in the API contract. Register them in the root `openapi.yaml` `paths` via `$ref`, and add response schemas (`DNSProviderCredential`, `CertificateIssuer`, `IssuerProjectGrant`) with **no** secret fields.

- [ ] **Step 2: Rebundle + verify parity**

Run: `make openapi && make openapi-check && go test ./cmd/server/ -run TestRouteSpecParity -count=1`
Expected: bundle fresh, `openapi-check` exit 0, `TestRouteSpecParity` PASS (route count == spec op count).

- [ ] **Step 3: Full phase gate**

Run: `go build ./... && go test ./... && make mocks-check`
Expected: build OK, all tests PASS, mock drift clean.

- [ ] **Step 4: Commit**

```bash
git add docs/openapi cmd/server/openapi.yaml
git commit -m "docs(openapi): add Phase 1 managed-certificate trust-infra endpoints"
```

---

## Self-Review

**1. Spec coverage (Phase 1 slice):**
- DNSProviderCredential (standalone, encrypted, CRUD, owner-only) → Tasks 2, 3. ✅
- CertificateIssuer self-signed CA (SelfSigned+CA ClusterIssuer + CA Certificate) → Tasks 4, 5, 6. ✅
- CertificateIssuer ACME (account + DNS-01 solver + ClusterIssuer) → Tasks 4, 5, 6. ✅
- IssuerProjectGrant (per-project visibility) → Task 7. ✅
- In-cluster control-plane client (no Project row) → Task 1. ✅
- Owner-only gating on every endpoint → Tasks 3, 6, 7 (all top-level owner-only groups, each `RequireRole("owner")`). ✅
- No private keys in DB; DNS creds encrypted → Tasks 2, 3 (`json:"-"`, `crypto.Encrypt`). ✅
- OpenAPI + sync guard → Task 8. ✅
- **Deferred to Phase 2 (documented in-place):** issuer-delete "referenced by ManagedCertificate" 409 guard; grant-revoke "cert uses issuer" 409 guard; the `certificate.*` project permissions (Phase 1 is owner-only). These have no Phase-1 dependency and are correctly out of scope.

**2. Placeholder scan:** No "TBD"/"add error handling here"/"similar to Task N". Cross-phase guards are explicitly marked as Phase 2 with the exact site, not vague. cert-manager CRD shapes are concrete. ✅

**3. Type consistency:** `IssuerConfig.ClusterIssuerName`/`CASecretName` set in Task 6 match the model in Task 4; `CountByDNSCredential` used by the guard (Task 6) is defined on the repo (Task 4); `CertInfraApplier` (Task 5) is consumed by the issuer service (Task 6) and satisfied by `*cluster.ControlPlaneClient` (Task 1); GVR names (`CertManagerClusterIssuerGVR` etc.) are identical across Tasks 1/5/6. ✅

**Known local-dev note:** control-plane client construction requires in-cluster config; `main()` guards registration behind `services.IsRunningInCluster()` so non-cluster runs (tests, docker-compose) still boot — the cert-infra routes are simply absent there. `TestRouteSpecParity` runs `setupRouter` with a constructed dep set; ensure the test builds `RouterDeps` with the cert handlers present (inject a control-plane client backed by a fake `dynamic.Interface`) so the new routes are counted and matched against the OpenAPI spec.
