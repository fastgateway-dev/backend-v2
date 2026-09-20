# Managed Certificates — Phase 2 (Issuance + Approval) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a project team request a TLS/mTLS certificate from an issuer granted to their project; every request goes through the existing approval workflow, and on approval the backend issues a leaf cert-manager `Certificate` in the control cluster.

**Architecture:** A new project-scoped `ManagedCertificate` (JSONB config mirroring Phase 1's `CertificateIssuer`) with `certificate.*` RBAC permissions. Creation persists a pending row and submits a `certificate` approval via the existing `internal/approval` engine; the `ManagedCertificateService` implements the engine's `Completer` (OnApproved issues a leaf `Certificate` CRD in the control cluster via the Phase 1 `CertInfraApplier`). A status endpoint reads the cert-manager `Certificate`'s `Ready` condition (first consumer of `ControlPlaneClient.Get`). Closes the two Phase-1-deferred referential guards now that `ManagedCertificate` exists.

**Tech Stack:** Go 1.25, GORM, Gin, cert-manager (`cert-manager.io/v1`), `internal/approval` engine, testify + mockery v3.

**Spec:** `docs/superpowers/specs/2026-09-19-managed-certificates-design.md`
**API contract (Phase 2 section):** `docs/superpowers/plans/2026-09-19-managed-certificates-api.md`
**Phase 1 (committed, mirror these):** `internal/models/certificate_issuer.go`, `internal/repository/certificate_issuer_repository.go`, `internal/services/certificate_issuer_service.go`, `internal/kubernetes/certmanager.go`, `internal/services/certinfra_roles.go`.

## Global Constraints

- **No private keys in the DB.** `ManagedCertificate` stores only metadata (issuer id, dnsNames/subject, resolved control-cluster secret name, fingerprint, notAfter, status). Leaf keys live only in cluster Secrets. No secret material in any response.
- **Approval-gated create.** Every `ManagedCertificate` create goes through the `internal/approval` engine as `EntityType=certificate`, `Action=create`; approver holds `certificate.approve`; honor the `Project.ApprovalEnabled` fast-path (no approval row when disabled). "Cannot approve own" is enforced by the engine.
- **Project-scoped + granted-issuer only.** A cert may only be created in a project from an issuer granted to that project (`IssuerProjectGrant`), and only for a `usage ∈ {server, client}`.
- **Owner/issuer material is Phase 1's; unchanged.** Issuers/DNS creds/grants stay owner-only and untouched except the two guards below.
- **Idempotent, best-effort cluster writes** via the Phase 1 `CertInfraApplier` (`ApplyNamespaced`/`Get`/`Delete`), control namespace `cfg.ControlPlaneNamespace`.
- **Migrations** continue the sequence: next free is **000041**; confirm with `ls migrations/`.
- **gofmt the whole tree before the final commit** (`gofmt -l .`), not just per-file — a Phase 1 lint miss.
- Deferred to Phase 3 (do NOT build): distribution/sync of the issued Secret to tenant clusters; the reconcile controller; domain/client attachment.

---

## File Structure

**Create:**
- `migrations/000041_add_managed_certificates.up.sql` / `.down.sql`
- `internal/models/managed_certificate.go`
- `internal/repository/managed_certificate_repository.go`
- `internal/services/managed_certificate_service.go` (+ its Completer methods)
- `internal/handlers/managed_certificate_handler.go`
- Tests alongside each; a golden test add in `internal/kubernetes/certmanager_test.go`.

**Modify:**
- `internal/models/team.go` — `certificate.*` permission consts + `AllPermissions` + presets.
- `internal/models/approval.go` — `ApprovalEntityCertificate` const.
- `internal/approval/planning.go` — `certificate` arm in `noPolicyFallback`.
- `internal/kubernetes/certmanager.go` — new `LeafCertificate` builder.
- `internal/middleware/permissions.go` — `CanManageCertificates` / `CanApproveCertificates` helpers.
- `internal/repository/interfaces.go` — new repo interface + assertion; add `CountByIssuer`/`CountByIssuerAndProject` needs on the managed-cert repo.
- `internal/handlers/service_interfaces.go` — `ManagedCertificateServiceInterface`.
- `internal/services/certificate_issuer_service.go` — issuer-delete guard (needs new ManagedCert repo dep).
- `internal/services/issuer_grant_service.go` — grant-revoke guard (needs new ManagedCert repo dep).
- `cmd/server/main.go` — construct service/handler, `approvalEngine.Register(certificate, svc)`, project-scoped routes, and pass the ManagedCert repo into the issuer + grant services (dep ripple).
- `docs/openapi/…` + `cmd/server/openapi.yaml` — Phase 2 operations.
- `.mockery.yml` — auto (packages already `all: true`); run `make mocks`.

---

## Task 1: `certificate.*` permissions

**Files:** Modify `internal/models/team.go`, `internal/middleware/permissions.go`; Test `internal/models/team_test.go` (or wherever preset tests live).

**Interfaces — Produces:** `models.PermCertificateView/Create/Edit/Delete/Approve` (`Permission` = `"certificate.view"` etc.); `PermissionChecker.CanManageCertificates(projectID, user) bool`, `CanCreateCertificates(...)`, `CanApproveCertificates(...)`.

- [ ] **Step 1: Write the failing test** — in the models test file:

```go
func TestPresets_IncludeCertificatePerms(t *testing.T) {
	assert.Contains(t, models.PresetViewer, models.PermCertificateView)
	assert.Contains(t, models.PresetEditor, models.PermCertificateCreate)
	assert.Contains(t, models.PresetApprover, models.PermCertificateApprove)
	for _, p := range []models.Permission{
		models.PermCertificateView, models.PermCertificateCreate, models.PermCertificateEdit,
		models.PermCertificateDelete, models.PermCertificateApprove,
	} {
		assert.True(t, models.IsValidPermission(p), "%s should be valid", p)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/models/ -run TestPresets_IncludeCertificatePerms -v` → FAIL (undefined consts).

- [ ] **Step 3: Add the permission group** in `internal/models/team.go` (after the Audit group, before `AllPermissions`):

```go
	// Certificate permissions (managed certificates, project-scoped)
	PermCertificateView    Permission = "certificate.view"
	PermCertificateCreate  Permission = "certificate.create"
	PermCertificateEdit    Permission = "certificate.edit"
	PermCertificateDelete  Permission = "certificate.delete"
	PermCertificateApprove Permission = "certificate.approve"
```
Append all five to `AllPermissions`. Add to presets: `PresetViewer` += `PermCertificateView`; `PresetEditor` += `PermCertificateView, PermCertificateCreate, PermCertificateEdit, PermCertificateDelete`; `PresetApprover` += `PermCertificateView, PermCertificateApprove`. (`PresetAdmin = AllPermissions` — no change.)

- [ ] **Step 4: Add checker helpers** in `internal/middleware/permissions.go` (mirror `CanManageDomains`/`CanApproveRoutes`):

```go
func (p *PermissionChecker) CanCreateCertificates(projectID uuid.UUID, user *models.User) bool {
	if IsOwner(user) || p.IsProjectAdmin(projectID, user.ID) {
		return true
	}
	return p.HasPermission(projectID, user, models.PermCertificateCreate)
}

func (p *PermissionChecker) CanManageCertificates(projectID uuid.UUID, user *models.User) bool {
	if IsOwner(user) || p.IsProjectAdmin(projectID, user.ID) {
		return true
	}
	return p.HasPermission(projectID, user, models.PermCertificateDelete)
}
```

- [ ] **Step 5: Run** `go test ./internal/models/ ./internal/middleware/ -run 'Certificate|Preset' -v && go build ./...` → PASS/clean.

- [ ] **Step 6: (no commit — leave in working tree per the controller's no-commit rule if executed under that constraint; otherwise)** `git add -p` the two files.

---

## Task 2: `ManagedCertificate` model, migration, repository

**Files:** Create `internal/models/managed_certificate.go`, `..._test.go`, `migrations/000041_add_managed_certificates.{up,down}.sql`, `internal/repository/managed_certificate_repository.go`; Modify `internal/repository/interfaces.go`.

**Interfaces — Produces:**
- `models.ManagedCertificate{ ID, ProjectID uuid.UUID; Name string; IssuerID uuid.UUID; Usage ManagedCertUsage; Config ManagedCertConfig (jsonb); Status ManagedCertStatus; StatusMessage string; Fingerprint string; NotAfter *time.Time; CreatedBy uuid.UUID; CreatedAt/UpdatedAt time.Time }`, `TableName()="managed_certificates"`.
- `ManagedCertUsage` (`server`/`client`); `ManagedCertStatus` (`pending`/`issuing`/`ready`/`error`).
- `ManagedCertConfig` JSONB (Value/Scan, mirror `IssuerConfig`): `DNSNames []string`, `Subject string`, `SecretName string` (control-cluster leaf secret), `CertificateName string` (cert-manager Certificate object name), `KeyAlgorithm string`, `KeySize int`, `DurationDays int`.
- `repository.ManagedCertificateRepositoryInterface`: `Create/GetByID/ListByProject(projectID, page, limit, status)/Update/Delete/CountByIssuer(issuerID) (int64,error)/CountByIssuerAndProject(issuerID, projectID) (int64,error)/CountReferencing... ` and `NewManagedCertificateRepository(db)`.

- [ ] **Step 1: Write the failing test** — `internal/models/managed_certificate_test.go` (Value/Scan round-trip incl `DNSNames []string`):

```go
func TestManagedCertConfig_ValueScanRoundTrip(t *testing.T) {
	in := ManagedCertConfig{DNSNames: []string{"a.example.com", "b.example.com"}, SecretName: "fgw-cert-x", CertificateName: "cert-x", KeyAlgorithm: "RSA", KeySize: 2048, DurationDays: 90}
	v, err := in.Value()
	require.NoError(t, err)
	var out ManagedCertConfig
	require.NoError(t, out.Scan(v))
	assert.Equal(t, []string{"a.example.com", "b.example.com"}, out.DNSNames)
	assert.Equal(t, "fgw-cert-x", out.SecretName)
}
```

- [ ] **Step 2: Run** `go test ./internal/models/ -run TestManagedCertConfig -v` → FAIL.

- [ ] **Step 3: Implement the model** — `internal/models/managed_certificate.go`. Mirror `internal/models/certificate_issuer.go` exactly for the `Value`/`Scan` + enum + struct-tag conventions. `ManagedCertConfig` is a struct with `Value()`/`Scan()` (JSON marshal / nil-safe unmarshal — copy the `IssuerConfig` methods verbatim, renamed). The struct:

```go
type ManagedCertUsage string
const (
	ManagedCertUsageServer ManagedCertUsage = "server"
	ManagedCertUsageClient ManagedCertUsage = "client"
)
type ManagedCertStatus string
const (
	ManagedCertStatusPending ManagedCertStatus = "pending"
	ManagedCertStatusIssuing ManagedCertStatus = "issuing"
	ManagedCertStatusReady   ManagedCertStatus = "ready"
	ManagedCertStatusError   ManagedCertStatus = "error"
)

type ManagedCertConfig struct {
	DNSNames        []string `json:"dnsNames,omitempty"`
	Subject         string   `json:"subject,omitempty"`
	SecretName      string   `json:"secretName,omitempty"`
	CertificateName string   `json:"certificateName,omitempty"`
	KeyAlgorithm    string   `json:"keyAlgorithm,omitempty"`
	KeySize         int      `json:"keySize,omitempty"`
	DurationDays    int      `json:"durationDays,omitempty"`
}
// Value()/Scan() copied from IssuerConfig (internal/models/certificate_issuer.go), renamed.

type ManagedCertificate struct {
	ID            uuid.UUID         `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	ProjectID     uuid.UUID         `gorm:"type:uuid;not null;index" json:"projectId"`
	Name          string            `gorm:"not null" json:"name"`
	IssuerID      uuid.UUID         `gorm:"type:uuid;not null;index" json:"issuerId"`
	Usage         ManagedCertUsage  `gorm:"not null" json:"usage"`
	Config        ManagedCertConfig `gorm:"type:jsonb;not null;default:'{}'" json:"config"`
	Status        ManagedCertStatus `gorm:"not null;default:'pending'" json:"status"`
	StatusMessage string            `gorm:"column:status_message" json:"statusMessage,omitempty"`
	Fingerprint   string            `gorm:"column:fingerprint" json:"fingerprint,omitempty"`
	NotAfter      *time.Time        `gorm:"column:not_after" json:"notAfter,omitempty"`
	CreatedBy     uuid.UUID         `gorm:"type:uuid;not null" json:"createdBy"`
	CreatedAt     time.Time         `gorm:"not null;default:now()" json:"createdAt"`
	UpdatedAt     time.Time         `gorm:"not null;default:now()" json:"updatedAt"`
}
func (ManagedCertificate) TableName() string { return "managed_certificates" }
```

- [ ] **Step 4: Migration** `migrations/000041_add_managed_certificates.up.sql`:

```sql
CREATE TABLE managed_certificates (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    issuer_id UUID NOT NULL REFERENCES certificate_issuers(id),
    usage VARCHAR(16) NOT NULL,
    config JSONB NOT NULL DEFAULT '{}',
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    status_message TEXT,
    fingerprint TEXT,
    not_after TIMESTAMP WITH TIME ZONE,
    created_by UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_managed_certificates_project ON managed_certificates(project_id);
CREATE INDEX idx_managed_certificates_issuer ON managed_certificates(issuer_id);
```
`.down.sql`: `DROP TABLE IF EXISTS managed_certificates;`
Note: `issuer_id` FK has **no** CASCADE (issuer delete must be *blocked* by the app-level guard, not silently cascade).

- [ ] **Step 5: Repository** — `internal/repository/managed_certificate_repository.go`, mirror `certificate_issuer_repository.go`. Methods `Create/GetByID/Update/Delete`, `ListByProject(projectID uuid.UUID, page, limit int, status string) ([]models.ManagedCertificate, int64, error)`, and the two guard counts:

```go
func (r *ManagedCertificateRepository) CountByIssuer(issuerID uuid.UUID) (int64, error) {
	var n int64
	err := r.db.Model(&models.ManagedCertificate{}).Where("issuer_id = ?", issuerID).Count(&n).Error
	return n, err
}
func (r *ManagedCertificateRepository) CountByIssuerAndProject(issuerID, projectID uuid.UUID) (int64, error) {
	var n int64
	err := r.db.Model(&models.ManagedCertificate{}).
		Where("issuer_id = ? AND project_id = ?", issuerID, projectID).Count(&n).Error
	return n, err
}
```
Add the interface + `var _ ManagedCertificateRepositoryInterface = (*ManagedCertificateRepository)(nil)` to `internal/repository/interfaces.go`.

- [ ] **Step 6: Run** `go test ./internal/models/ -run TestManagedCertConfig -v && go build ./...` → PASS/clean. Confirm migration number free (`ls migrations/`).

---

## Task 3: `LeafCertificate` cert-manager builder

**Files:** Modify `internal/kubernetes/certmanager.go`; Test `internal/kubernetes/certmanager_test.go` (+ golden `testdata/golden/certmanager/leaf-certificate.yaml`).

**Interfaces — Produces:** `kubernetes.LeafCertificate(cfg kubernetes.LeafCertConfig) *unstructured.Unstructured`; `LeafCertConfig{ Name, Namespace, SecretName, IssuerClusterIssuerName string; DNSNames []string; CommonName string; KeyAlgorithm string; KeySize, DurationDays int }`.

- [ ] **Step 1: Write the failing golden test** (mirror `TestCACertificate_Golden`):

```go
func TestLeafCertificate_Golden(t *testing.T) {
	obj := kubernetes.LeafCertificate(kubernetes.LeafCertConfig{
		Name: "cert-abc", Namespace: "fastgateway-system", SecretName: "cert-abc",
		IssuerClusterIssuerName: "iss-1", DNSNames: []string{"api.example.com"},
		KeyAlgorithm: "RSA", KeySize: 2048, DurationDays: 90,
	})
	assert.Equal(t, "Certificate", obj.Object["kind"])
	spec := obj.Object["spec"].(map[string]interface{})
	assert.NotContains(t, spec, "isCA")
	assertGolden(t, "leaf-certificate", obj)
}
```

- [ ] **Step 2: Run** `go test ./internal/kubernetes/ -run TestLeafCertificate_Golden -v` → FAIL.

- [ ] **Step 3: Implement** in `internal/kubernetes/certmanager.go` (mirror `CACertificate`, drop `isCA`, add `dnsNames`, issuerRef → the granted ClusterIssuer):

```go
type LeafCertConfig struct {
	Name, Namespace, SecretName, IssuerClusterIssuerName, CommonName, KeyAlgorithm string
	DNSNames             []string
	KeySize, DurationDays int
}

func LeafCertificate(cfg LeafCertConfig) *unstructured.Unstructured {
	dnsNames := make([]interface{}, 0, len(cfg.DNSNames))
	for _, n := range cfg.DNSNames {
		dnsNames = append(dnsNames, n)
	}
	spec := map[string]interface{}{
		"secretName": cfg.SecretName,
		"dnsNames":   dnsNames,
		"duration":   hoursDuration(cfg.DurationDays),
		"privateKey": map[string]interface{}{"algorithm": cfg.KeyAlgorithm, "size": int64(cfg.KeySize)},
		"issuerRef": map[string]interface{}{
			"name": cfg.IssuerClusterIssuerName, "kind": "ClusterIssuer", "group": "cert-manager.io",
		},
	}
	if cfg.CommonName != "" {
		spec["commonName"] = cfg.CommonName
	}
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "cert-manager.io/v1", "kind": "Certificate",
		"metadata": map[string]interface{}{"name": cfg.Name, "namespace": cfg.Namespace, "labels": managedByLabels()},
		"spec":     spec,
	}}
}
```

- [ ] **Step 4:** `go test ./internal/kubernetes/ -run Golden -update-golden` then without the flag → PASS; **inspect** `leaf-certificate.yaml` (valid `dnsNames`, `issuerRef.kind: ClusterIssuer`, no `isCA`). `go build ./...`.

---

## Task 4: `ManagedCertificateService` + approval completer + engine wiring

This is the crux. **Files:** Create `internal/services/managed_certificate_service.go`, `..._test.go`; Modify `internal/models/approval.go`, `internal/approval/planning.go`.

**Interfaces — Consumes:** `repository.ManagedCertificateRepositoryInterface`, `repository.CertificateIssuerRepositoryInterface`, `repository.IssuerProjectGrantRepositoryInterface`, `repository.ProjectRepositoryInterface`, `services.CertInfraApplier`, the approval `*approvalpkg.Engine` (as a `CertApprovalSubmitter` port — see below), `*config.Config`.
**Produces:** `ManagedCertificateService` implementing `approval.Completer` (`OnApproved/OnRejected/OnCancelled`); `CreateCertificateInput{ Name string; IssuerID uuid.UUID; Usage models.ManagedCertUsage; DNSNames []string; Subject string; KeyAlgorithm string; KeySize, DurationDays int }`; `Create(projectID, input, createdBy) (*models.ManagedCertificate, *models.Approval, error)`; `GetByID`, `ListByProject`, `Delete`, `Status(id) (*CertStatus, error)`, `IssuersForProject(projectID)`.

- [ ] **Step 1: approval EntityType** — add to `internal/models/approval.go` const block: `ApprovalEntityCertificate ApprovalEntityType = "certificate"`.

- [ ] **Step 2: PlanStages fallback arm** — in `internal/approval/planning.go` `noPolicyFallback`, add before the route fallback (mirror the route single-stage return but with `certificate.approve`):

```go
	if entity == models.ApprovalEntityCertificate {
		return []models.ApprovalStage{{
			StageOrder:         1,
			RequiredPermission: string(models.PermCertificateApprove),
			MinApprovers:       1,
			Status:             models.ApprovalStatusPending,
		}}, nil
	}
```

- [ ] **Step 3: Write the failing service test** — `internal/services/managed_certificate_service_test.go`:

```go
func TestManagedCertificateService_Create_SubmitsApproval(t *testing.T) {
	// project with ApprovalEnabled true; issuer granted to project; expects repo.Create + approvals.Submit
	// asserts returned cert Status == pending and a non-nil approval
}
func TestManagedCertificateService_Create_RejectedWhenIssuerNotGranted(t *testing.T) {
	// grantRepo.Exists(issuerID, projectID) == false -> error, no repo.Create
}
func TestManagedCertificateService_OnApproved_IssuesLeafCertificate(t *testing.T) {
	// OnApproved(approval{EntityType:certificate,Action:create,EntityID:certID}) ->
	// controlPlane.ApplyNamespaced(CertManagerCertificateGVR, ...) called once; status -> issuing
}
```
Use `mocks.MockManagedCertificateRepository`, `mocks.MockCertificateIssuerRepository`, `mocks.MockIssuerProjectGrantRepository`, `mocks.MockProjectRepository`, `mocks.MockCertInfraApplier`, and a stub approval submitter (define a `CertApprovalSubmitter` interface so the engine can be mocked).

- [ ] **Step 4: Run** → FAIL (undefined service). `make mocks` for the new managed-cert repo mock.

- [ ] **Step 5: Implement the service** — `internal/services/managed_certificate_service.go`. Define a narrow submitter port (so the concrete `*approvalpkg.Engine` satisfies it and tests can fake it):

```go
type CertApprovalSubmitter interface {
	Submit(spec approvalpkg.Spec) (*models.Approval, error)
}
```
`Deps` + panic-on-nil constructor (mirror `CertificateIssuerService`). `Create`:
1. Validate `usage`, `IssuerID`, and (server) at least one `DNSNames`; (client) a `Subject`.
2. `grantRepo.Exists(input.IssuerID, projectID)` — false → `errors.New("issuer is not granted to this project")`.
3. Load the issuer (`issuerRepo.GetByID`) → read `Config.ClusterIssuerName` (the cert-manager ClusterIssuer to reference).
4. Persist `ManagedCertificate{Status: pending, Config: {DNSNames, Subject, key params, CertificateName:"cert-"+id, SecretName:"cert-"+id}}` (Update after Create to store id-derived names — **persist Config before any cluster work**, per the Phase 1 I1 lesson).
5. If `!project.ApprovalEnabled`: call `OnApproved` directly (fast-path issue) and return `(cert, nil, nil)`. Else `approvals.Submit(approvalpkg.Spec{ProjectID: projectID, EntityType: models.ApprovalEntityCertificate, EntityID: cert.ID, Action: models.ApprovalActionCreate, SubmittedBy: createdBy, ConfigSnapshot: <json of input>})` and return `(cert, approval, nil)`.

`OnApproved(a)`: load cert by `a.EntityID`; build `kubernetes.LeafCertificate(LeafCertConfig{Name: cfg.CertificateName, Namespace: controlPlane.Namespace(), SecretName: cfg.SecretName, IssuerClusterIssuerName: <issuer.Config.ClusterIssuerName>, DNSNames: cfg.DNSNames, CommonName: cfg.Subject, KeyAlgorithm, KeySize, DurationDays})`; `controlPlane.ApplyNamespaced(ctx, kubernetes.CertManagerCertificateGVR, obj)`; set `Status=issuing`, persist; return nil (on apply error → `Status=error` + message, return the error so the engine surfaces it).
`OnRejected(a)`/`OnCancelled(a)`: for a create, set `Status=error`/delete the pending row (mirror `routeWrite.OnRejected`/`OnCancelled` semantics: cancelled-create deletes the row).

`Status(id)`: `controlPlane.Get(ctx, CertManagerCertificateGVR, cfg.CertificateName, true)`; parse `status.conditions[type=Ready]` → map to ready/error + message; when Ready, read `notAfter`/fingerprint if present. Update the row's `Status`/`NotAfter`/`Fingerprint` and return a `CertStatus{Status, Message, NotAfter}`.

- [ ] **Step 6: Run** `go test ./internal/services/ -run 'ManagedCertificate' -v && go build ./...` → PASS/clean.

---

## Task 5: handler + project routes + engine Register + main.go wiring

**Files:** Create `internal/handlers/managed_certificate_handler.go`; Modify `internal/handlers/service_interfaces.go`, `cmd/server/main.go`.

**Interfaces — Produces:** `handlers.NewManagedCertificateHandler(service ManagedCertificateServiceInterface, permChecker *middleware.PermissionChecker, auditService AuditServiceInterface)` with `List/Create/Get/Delete/Status/IssuersForProject`; `ManagedCertificateServiceInterface` (List/Create/GetByID/Delete/Status/IssuersForProject).

- [ ] **Step 1: Write the failing handler test** — Create returns 201 (or 202 with approvalId) and never leaks key material; Create denied without `certificate.create` (403); List returns `{data,pagination}`. Use `mocks.MockManagedCertificateService`.

- [ ] **Step 2: Run** → FAIL. `make mocks`.

- [ ] **Step 3: Implement the handler** — mirror `dns_credential_handler.go`/`route_handler.go`: parse `projectId`; `middleware.GetCurrentUser`; per-action checks (`permChecker.CanCreateCertificates` for Create, `CanManageCertificates` for Delete, project access for reads); response DTO carries only metadata (id, name, issuerId, usage, dnsNames, status, statusMessage, fingerprint, notAfter, createdAt) — NO secret material. Create returns `{certificate, approvalId}` (approvalId nil when fast-pathed).

- [ ] **Step 4: Wire routes + engine** in `cmd/server/main.go`:
  - Construct `managedCertRepo := repository.NewManagedCertificateRepository(db)`.
  - Construct `managedCertService := services.NewManagedCertificateService(services.ManagedCertificateServiceDeps{ Repo: managedCertRepo, IssuerRepo: certificateIssuerRepo, GrantRepo: issuerProjectGrantRepo, ProjectRepo: projectRepo, ControlPlane: controlPlane, Approvals: approvalEngine, Config: cfg })` — **only when `controlPlane != nil`** (in-cluster), same nil-guard as Phase 1 issuer service; construct the handler and register routes only then.
  - `approvalEngine.Register(models.ApprovalEntityCertificate, managedCertService)` (after construction, beside the route/client_attachment registers) — guard for nil service.
  - Add `ManagedCertificateHandler *handlers.ManagedCertificateHandler` to `RouterDeps`; register a project-scoped group:
```go
if deps.ManagedCertificateHandler != nil {
    certs := projects.Group("/:projectId/certificates")
    certs.Use(deps.PermChecker.RequireProjectAccess())
    {
        certs.GET("", deps.ManagedCertificateHandler.List)               // certificate.view (in handler)
        certs.GET("/issuers", deps.ManagedCertificateHandler.IssuersForProject)
        certs.POST("", deps.ManagedCertificateHandler.Create)            // certificate.create (in handler)
        certs.GET("/:certificateId", deps.ManagedCertificateHandler.Get)
        certs.DELETE("/:certificateId", deps.ManagedCertificateHandler.Delete)
        certs.GET("/:certificateId/status", deps.ManagedCertificateHandler.Status)
    }
}
```
  Note: `:projectId/certificates` sits under the existing `projects` group; ensure no route collision with existing `:projectId/...` groups.

- [ ] **Step 5: Run** `go test ./internal/handlers/ ./internal/services/ -run 'ManagedCertificate' -v && go build ./...` → PASS/clean.

---

## Task 6: close the two Phase-1-deferred guards

**Files:** Modify `internal/services/certificate_issuer_service.go` (+test), `internal/services/issuer_grant_service.go` (+test), `cmd/server/main.go`.

**Dep ripple (like Phase 1's IssuerRepo):** add a required `ManagedCertRepo repository.ManagedCertificateRepositoryInterface` to BOTH `CertificateIssuerServiceDeps` and `IssuerGrantServiceDeps` (with nil-panic checks). This BREAKS their existing tests + main.go construction — you MUST update:
- `internal/services/certificate_issuer_service_test.go` + `internal/services/issuer_grant_service_test.go` — supply `mocks.MockManagedCertificateRepository`.
- `cmd/server/main.go` — pass `ManagedCertRepo: managedCertRepo` into both constructors.

- [ ] **Step 1: Write failing tests:**
```go
func TestCertificateIssuerService_Delete_BlockedWhenCertReferences(t *testing.T) {
	// managedCertRepo.CountByIssuer(issuerID) == 1 -> Delete returns "in use" error, no controlPlane.Delete/repo.Delete
}
func TestIssuerGrantService_Revoke_BlockedWhenCertUsesIssuer(t *testing.T) {
	// managedCertRepo.CountByIssuerAndProject(issuerID, projectID) == 1 -> Revoke errors, no grantRepo.Delete
}
```

- [ ] **Step 2: Run** → FAIL (compile: new dep not in constructors).

- [ ] **Step 3: Implement** — in `CertificateIssuerService.Delete`, replace the `// Phase 2:` comment with:
```go
n, err := s.managedCertRepo.CountByIssuer(id)
if err != nil {
	return err
}
if n > 0 {
	return errors.New("certificate issuer is in use by one or more managed certificates")
}
```
(before the existing cluster/repo delete logic). In `IssuerGrantService.Revoke`:
```go
n, err := s.managedCertRepo.CountByIssuerAndProject(issuerID, projectID)
if err != nil {
	return err
}
if n > 0 {
	return errors.New("issuer is in use by a managed certificate in this project")
}
return s.grantRepo.Delete(issuerID, projectID)
```
Update both `Deps` structs (+ nil checks) and all construction sites (tests + main.go).

- [ ] **Step 4: Run** `go test ./internal/services/ -run 'CertificateIssuer|IssuerGrant|ManagedCertificate' -v && go build ./... && make mocks-check` (mocks-check may show uncommitted-drift under the no-commit rule — that's benign). → PASS.

---

## Task 7: OpenAPI operations + parity + full-phase gate

**Files:** Modify `docs/openapi/paths/certificates.yaml` (+ schemas), `docs/openapi/openapi.yaml`; regenerate `cmd/server/openapi.yaml`; possibly `cmd/server/router_test.go`.

- [ ] **Step 1:** Add the Phase 2 project-scoped operations to `docs/openapi/` matching the API contract and the ACTUAL registered routes (list, get, create, delete, status, issuers-for-project). Add a `ManagedCertificate` response schema (metadata only, no secrets). Register in root `openapi.yaml`.

- [ ] **Step 2:** If `TestRouteSpecParity` needs the project-scoped cert routes registered in its `RouterDeps`, set a non-nil `ManagedCertificateHandler: &handlers.ManagedCertificateHandler{}` there (same technique Phase 1 used for the issuer handler; methods aren't invoked during `Routes()` enumeration).

- [ ] **Step 3: Gate:** `make openapi && make openapi-check` (exit 0); `go test ./cmd/server/ -run TestRouteSpecParity -count=1` (PASS); `go build ./...`; `go test ./... -count=1` (all pass); `gofmt -l .` (clean, excluding `docs/superpowers/`).

- [ ] **Step 4:** Confirm none of the Phase 2 endpoints return secret material (grep the handlers/DTOs).

---

## Self-Review

**1. Spec coverage:**
- Certificate issuance from a granted issuer, project-scoped, approval-gated → Tasks 1,2,4,5. ✅
- Server + client usage → model `Usage` (Task 2), builder `DNSNames`/`CommonName` (Task 3). ✅
- Leaf cert issued in control cluster on approval → Task 4 `OnApproved`. ✅
- Status from cert-manager `Ready` condition → Task 4 `Status` (first `ControlPlane.Get` consumer; also addresses Phase-1 I2 for leaf certs). ✅
- Two deferred guards closed → Task 6. ✅
- `certificate.*` + `certificate.approve` perms; approve auto-enforced by engine → Tasks 1,4. ✅
- OpenAPI + parity → Task 7. ✅
- No secrets in DB/responses → Tasks 2,5,7. ✅
- Distribution/attachment correctly OUT (Phase 3). ✅

**2. Placeholder scan:** No "TBD"/vague error handling. Mechanical mirrors (model/repo/migration) point at the exact committed Phase 1 files with the concrete field/method deltas — real code, not "similar to Task N".

**3. Type consistency:** `ManagedCertConfig.CertificateName`/`SecretName`/`ClusterIssuerName` (from issuer) used consistently across Tasks 2/4; `CountByIssuer`/`CountByIssuerAndProject` defined in Task 2, consumed in Task 6; `ApprovalEntityCertificate` defined Task 4 Step 1, used in service + Register (Task 5); `CertApprovalSubmitter` port satisfied by `*approvalpkg.Engine`; the `controlPlane != nil` nil-guard mirrors Phase 1 so local/dev boots.

**Cross-phase notes:** Task 6 repeats Phase 1's required-dep-ripple pattern — the executor MUST update the two services' tests + main.go, or the build breaks (caught immediately). Persist `ManagedCertificate.Config` (id-derived names) BEFORE any cluster apply (Phase 1 I1 lesson). Run a tree-wide `gofmt -l .` before the final commit (Phase 1 lint miss).
