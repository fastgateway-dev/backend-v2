# Managed Client Certificates Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the platform's private CA issue `usage=client` mTLS identity certificates (in a platform-key mode with approval-gated one-time export, and a CSR mode where the key never leaves the caller), and bind one 1:1 to a team-owned `Client` so the gateway trusts it.

**Architecture:** Reuses the managed-cert model, approval engine, issuer/CA infra, and the existing `ClientTrafficPolicy` CA-bundle+SAN aggregation. New: a `KeyMode` category, client-SAN config, `client auth` usages + URI SANs on the leaf builder, a `CertificateRequest` (CSR) builder + issuance path, an approval-gated single-use export-download, a `Client.ManagedCertificateID` 1:1 binding that derives the client's mTLS trust from the cert's issuer CA, a distributor skip for `usage=client`, and a delete referential guard.

**Tech Stack:** Go 1.25, Gin, GORM, golang-migrate, cert-manager (`cert-manager.io/v1`), mockery v3 (`make mocks`), testify, dynamic fake clients, golden-YAML builder tests, `//go:embed` OpenAPI bundle (`make openapi`/`make openapi-check`).

**Spec:** `docs/superpowers/specs/2026-09-20-managed-client-certificates-design.md`

## Global Constraints

- The 4 valid cert kinds; enforce at create: `usage=client ⟹ private-CA issuer` (`self_signed_ca`); `keyMode=csr ⟹ usage=client`; `usage=server ⟹ keyMode=managed`. Reject invalid combos with **422**.
- **No private key in the DB, ever.** Managed key lives only in cert-manager's control-cluster Secret; CSR-mode key never touches the platform. The single-use export bundle is the ONLY response that carries a private key.
- Client isolation under a shared private CA rests on the **per-cert SAN pin**; each client cert has its own SAN(s) set at creation, and attach is **1:1** (a cert binds to at most one Client; a Client holds at most one managed cert).
- **Attach/detach is NOT approval-gated** (client-management action); **create** and **export** ARE approval-gated (`certificate.approve` in the cert's project, "cannot approve your own").
- Certs are **project-owned**; Clients are **team-owned**; a cert may be bound to a Client only if the Client's team has a role in the cert's project.
- House patterns: repo method on interface + `var _` assertion; `Deps`/panic-on-nil where applicable; DTO responses leak no key material; migrations sequential (next is **000044**); builders get golden tests; edit OpenAPI **source** under `docs/openapi/` then `make openapi` + `make openapi-check` + `TestRouteSpecParity`; tree-wide `gofmt -l .` (excl `docs/superpowers`) before finishing.
- Execution: subagents never commit; the human commits explicitly; commit exactly the enumerated feature files via explicit `git add` (never `-A`).

## File Structure

- `internal/models/managed_certificate.go` — `ManagedCertKeyMode` + `Config.KeyMode` + `Config.URISANs` (Task 1).
- `internal/services/managed_certificate_validate.go` (new) — kind-constraint validation (Task 1).
- `internal/models/client.go` + migration `000044` + `internal/repository/client_repository.go` — `Client.ManagedCertificateID` 1:1 FK + lookup (Task 2).
- `internal/kubernetes/certmanager.go` (+golden) — `usages` + `uris` on `LeafCertificate` (Task 3); `CertificateRequest` builder + GVR (Task 4).
- `internal/services/managed_certificate_service.go` — create branching (managed/csr, issuer-type check), CSR read-back (Tasks 5, 4).
- `internal/models/certificate_export_grant.go` + migration `000045` + repo + `internal/models/approval.go` (`ApprovalActionExport`) — one-time export grant (Task 6).
- `internal/services/managed_certificate_service.go` + `internal/handlers/managed_certificate_handler.go` — export request + single-use download (Task 7).
- `internal/services/clients/client_certificate.go` (new) + `internal/handlers/client_handler.go` + `cmd/server/main.go` — attach/detach + issuer-CA materialization (Task 8).
- `internal/certdist/certdist.go` — skip `usage=client` (Task 9).
- `internal/services/managed_certificate_service.go` — delete referential guard (Task 10).
- `docs/openapi/...` — ops + gate (Task 11).

---

## Task 1: KeyMode + client-SAN config + kind validation

**Files:** Modify `internal/models/managed_certificate.go`; Create `internal/services/managed_certificate_validate.go` + `_test.go`.

**Interfaces — Produces:**
- `models.ManagedCertKeyMode` (`ManagedCertKeyModeManaged="managed"`, `ManagedCertKeyModeCSR="csr"`); `Config.KeyMode ManagedCertKeyMode`; `Config.URISANs []string`.
- `services.ValidateCertificateKind(usage models.ManagedCertUsage, keyMode models.ManagedCertKeyMode, issuerType models.IssuerType) error` — returns a typed error for an invalid combo.

- [ ] **Step 1: model additions.** In `managed_certificate.go`, beside `ManagedCertUsage` (L12-17) add:
```go
type ManagedCertKeyMode string
const (
	ManagedCertKeyModeManaged ManagedCertKeyMode = "managed"
	ManagedCertKeyModeCSR     ManagedCertKeyMode = "csr"
)
```
and in `ManagedCertConfig` (L28-36) add `KeyMode ManagedCertKeyMode json:"keyMode,omitempty"` and `URISANs []string json:"uriSans,omitempty"`.

- [ ] **Step 2: failing validation test** (`managed_certificate_validate_test.go`): table test asserting valid combos pass and invalid ones return the typed errors — `server`+`csr` → err; `client`+`acme` → err; `client`+`self_signed_ca`+`csr`/`managed` → ok; `server`+`self_signed_ca`/`acme`+`managed` → ok; `server`+`acme`+`csr` → err.
- [ ] **Step 3: Run** → FAIL.
- [ ] **Step 4: implement** `managed_certificate_validate.go`:
```go
var (
	ErrClientRequiresPrivateCA = errors.New("client certificates require a self-signed (private) CA issuer")
	ErrCSRRequiresClient       = errors.New("CSR key mode is only valid for client certificates")
	ErrServerRequiresManagedKey = errors.New("server certificates must use managed key mode")
)
func ValidateCertificateKind(usage models.ManagedCertUsage, keyMode models.ManagedCertKeyMode, issuerType models.IssuerType) error {
	if usage == models.ManagedCertUsageClient && issuerType != models.IssuerTypeSelfSignedCA {
		return ErrClientRequiresPrivateCA
	}
	if keyMode == models.ManagedCertKeyModeCSR && usage != models.ManagedCertUsageClient {
		return ErrCSRRequiresClient
	}
	if usage == models.ManagedCertUsageServer && keyMode != models.ManagedCertKeyModeManaged {
		return ErrServerRequiresManagedKey
	}
	return nil
}
```
- [ ] **Step 5: Run** `go test ./internal/services/ -run ValidateCertificateKind -v && go build ./...` → PASS.

---

## Task 2: `Client.ManagedCertificateID` 1:1 FK + lookup

**Files:** Modify `internal/models/client.go`; Create `migrations/000044_add_client_managed_certificate.{up,down}.sql`; Modify `internal/repository/client_repository.go` + `internal/repository/interfaces.go`; regenerate mocks.

**Interfaces — Produces:** `Client.ManagedCertificateID *uuid.UUID` (json `managedCertificateId,omitempty`, unique); `ClientRepositoryInterface.GetByManagedCertificateID(certID uuid.UUID) (*models.Client, error)` returning `gorm.ErrRecordNotFound` when none.

- [ ] **Step 1: model field.** In `client.go` mTLS block (~L214-223) add `ManagedCertificateID *uuid.UUID gorm:"type:uuid;column:managed_certificate_id" json:"managedCertificateId,omitempty"` and relationship `ManagedCertificate *ManagedCertificate gorm:"foreignKey:ManagedCertificateID" json:"-"`.
- [ ] **Step 2: up migration** `000044_...up.sql`:
```sql
ALTER TABLE clients ADD COLUMN managed_certificate_id UUID REFERENCES managed_certificates(id) ON DELETE RESTRICT;
CREATE UNIQUE INDEX idx_clients_managed_certificate_id ON clients(managed_certificate_id) WHERE managed_certificate_id IS NOT NULL;
```
- [ ] **Step 3: down migration** `000044_...down.sql`:
```sql
DROP INDEX IF EXISTS idx_clients_managed_certificate_id;
ALTER TABLE clients DROP COLUMN IF EXISTS managed_certificate_id;
```
- [ ] **Step 4: repo method** on `ClientRepository` + interface:
```go
func (r *ClientRepository) GetByManagedCertificateID(certID uuid.UUID) (*models.Client, error) {
	var c models.Client
	if err := r.db.Where("managed_certificate_id = ?", certID).First(&c).Error; err != nil {
		return nil, err
	}
	return &c, nil
}
```
- [ ] **Step 5:** `make mocks`; `go build ./...` → PASS. (Postgres-harness test optional; the partial-unique-index behavior is exercised in Task 8's attach test.)

---

## Task 3: `LeafCertificate` — client-auth usages + URI SANs

**Files:** Modify `internal/kubernetes/certmanager.go`; Test `internal/kubernetes/certmanager_test.go` + `testdata/golden/certmanager/`.

**Interfaces — Consumes:** `models.ManagedCertUsage`. **Produces:** `LeafCertConfig` gains `Usage models.ManagedCertUsage` and `URISANs []string`; the built `Certificate` sets `spec.usages` and `spec.uris` accordingly.

- [ ] **Step 1: failing golden tests.** Add `TestLeafCertificate_ClientAuth_Golden` (usage=client, URISANs=["spiffe://x/y"]) and update `TestLeafCertificate_Golden` (server). Assert (a) `apiVersion: cert-manager.io/v1`, `kind: Certificate`, (b) client cert has `spec.usages` containing `client auth` and `spec.uris: [spiffe://x/y]`, (c) server cert has `spec.usages` containing `server auth`. Regenerate goldens with `-update-golden` after implementing.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: implement.** In `LeafCertConfig` add `Usage models.ManagedCertUsage` and `URISANs []string`. In `LeafCertificate`, after `spec.dnsNames`, set:
```go
if len(config.URISANs) > 0 { spec["uris"] = toIfaceSlice(config.URISANs) }
switch config.Usage {
case models.ManagedCertUsageClient:
	spec["usages"] = []interface{}{"client auth", "digital signature", "key encipherment"}
default:
	spec["usages"] = []interface{}{"server auth", "digital signature", "key encipherment"}
}
```
(Use the file's existing string-slice→`[]interface{}` helper; if none, add a small local one.)
- [ ] **Step 4: Run** `go test ./internal/kubernetes/ -run LeafCertificate -v && go build ./...` → PASS (goldens updated).

---

## Task 4: `CertificateRequest` builder + GVR (CSR mode)

**Files:** Modify `internal/kubernetes/certmanager.go`, `internal/kubernetes/gvr.go`; Test `certmanager_test.go` + golden.

**Interfaces — Produces:** `kubernetes.CertManagerCertificateRequestGVR` (`cert-manager.io/v1, certificaterequests`); `kubernetes.CertificateRequestObject(cfg CertificateRequestConfig) *unstructured.Unstructured` with `CertificateRequestConfig{Name, Namespace, IssuerClusterIssuerName string; Request []byte /*PEM CSR*/; DurationDays int}`.

- [ ] **Step 1: GVR.** In `gvr.go` beside `CertManagerCertificateGVR` (L141) add:
```go
var CertManagerCertificateRequestGVR = schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "certificaterequests"}
```
- [ ] **Step 2: failing golden test** `TestCertificateRequest_Golden`: build with a sample CSR PEM, assert `apiVersion: cert-manager.io/v1`, `kind: CertificateRequest`, `spec.request` is the base64 of the CSR bytes, `spec.issuerRef{name,kind:ClusterIssuer,group:cert-manager.io}`, `spec.usages` contains `client auth`.
- [ ] **Step 3: Run** → FAIL.
- [ ] **Step 4: implement** `CertificateRequestObject`, mirroring `LeafCertificate`'s object shape (`internal/kubernetes/certmanager.go:48-79`): metadata name/namespace + managed-by labels; `spec.request` = base64(config.Request); `spec.issuerRef` = ClusterIssuer ref; `spec.duration` from DurationDays (if >0); `spec.usages` = `["client auth","digital signature","key encipherment"]`. Regenerate golden.
- [ ] **Step 5: Run** `go test ./internal/kubernetes/ -run CertificateRequest -v && go build ./...` → PASS.

---

## Task 5: Create service — issuer-type check, keyMode, managed vs CSR issuance

**Files:** Modify `internal/services/managed_certificate_service.go`; Test its `_test.go`.

**Interfaces — Consumes:** `ValidateCertificateKind` (Task 1), `CertificateRequestObject`/GVR (Task 4), `LeafCertificate` usages (Task 3). **Produces:** `CreateCertificateInput` gains `KeyMode models.ManagedCertKeyMode`, `URISANs []string`, `CSR string` (PEM, csr mode only). Create validates the kind (loads the issuer to get `Type`) and persists `Config.KeyMode/URISANs`; `OnApproved` branches: managed → `LeafCertificate` (with Usage+URISANs) via `ApplyNamespaced(CertManagerCertificateGVR,...)` (existing); csr → decode `CSR`, `CertificateRequestObject` via `ApplyNamespaced(CertManagerCertificateRequestGVR,...)`.

- [ ] **Step 1: failing tests** (mocked repos + ControlPlane): (a) `Create` with `usage=client` + an `acme` issuer → returns `ErrClientRequiresPrivateCA` (422 at handler), no CRD applied; (b) `usage=server`+`csr` → `ErrServerRequiresManagedKey`; (c) `usage=client`+`self_signed_ca`+`managed` → persists `Config.KeyMode=managed`, and `OnApproved` applies a `Certificate` with `client auth` usages; (d) `usage=client`+`csr` with a CSR → `OnApproved` applies a `CertificateRequest` (assert the GVR + base64 request), NOT a `Certificate`, and no leaf key Secret name is required. Assert the issuer is loaded and `ValidateCertificateKind` is consulted.
- [ ] **Step 2: Run** → FAIL. `make mocks` if the ControlPlane mock needs the new GVR call (same `ApplyNamespaced` method — no new mock method).
- [ ] **Step 3: implement.** In `Create` (L163-263): after resolving the issuer (`s.issuerRepo.GetByID`), call `ValidateCertificateKind(input.Usage, keyMode, issuer.Type)` (default `keyMode` to `managed` when empty); persist `cert.Config.KeyMode = keyMode`, `cert.Config.URISANs = input.URISANs`. For csr, store the CSR transiently for `OnApproved` — persist it on `Config` as `Config.CSRPEM string` (add to `ManagedCertConfig`; it's a public CSR, no key) so `OnApproved` (a fresh load) can read it. In `OnApproved` (L292-326): if `cert.Config.KeyMode == csr`, build `CertificateRequestObject{Name: cert.Config.CertificateName, Namespace: s.controlPlane.Namespace(), IssuerClusterIssuerName: issuer.Config.ClusterIssuerName, Request: []byte(cert.Config.CSRPEM), DurationDays: cert.Config.DurationDays}` and `ApplyNamespaced(CertManagerCertificateRequestGVR, obj)`; else build `LeafCertificate` as today plus `Usage: cert.Usage, URISANs: cert.Config.URISANs`. Set `Status=Issuing`.
- [ ] **Step 4: CSR status read-back.** Extend `Status(id)` so that for `KeyMode==csr` it reads the `CertificateRequest` (via `s.controlPlane.Get(CertManagerCertificateRequestGVR, cert.Config.CertificateName, true)`) `Ready` condition + `.status.certificate` (the signed leaf), deriving `NotAfter`/`Fingerprint` from that PEM; for managed keep the existing `Certificate` path. Add a test for the csr status branch (fake control-plane returns a CertificateRequest with a Ready=True + a cert).
- [ ] **Step 5: Run** `go test ./internal/services/ -run 'ManagedCertificate|Create|Status' -v && go build ./...` → PASS.

---

## Task 6: Export approval action + single-use export grant

**Files:** Modify `internal/models/approval.go`; Create `internal/models/certificate_export_grant.go`, `migrations/000045_add_certificate_export_grants.{up,down}.sql`, `internal/repository/certificate_export_grant_repository.go` + interface entry; Modify `internal/services/managed_certificate_service.go` (`OnApproved` branch); regenerate mocks.

**Interfaces — Produces:**
- `models.ApprovalActionExport ApprovalAction = "export"`.
- `models.CertificateExportGrant{ID, ManagedCertificateID uuid.UUID, TokenHash string /*sha256 hex*/, ExpiresAt time.Time, ConsumedAt *time.Time, CreatedAt}` (table `certificate_export_grants`).
- `CertificateExportGrantRepositoryInterface{ Create(*CertificateExportGrant) error; ConsumeByTokenHash(hash string) (*CertificateExportGrant, error) /* returns+marks consumed atomically, error if missing/expired/used */ }`.

- [ ] **Step 1:** add `ApprovalActionExport` to the enum (approval.go:36-45).
- [ ] **Step 2: grant model + migration.** Model as above; migration `000045` creates the table (FK `managed_certificate_id → managed_certificates(id) ON DELETE CASCADE`, `token_hash` unique, `expires_at timestamptz not null`, `consumed_at timestamptz null`).
- [ ] **Step 3: failing repo test** (Postgres harness): `Create` a grant; `ConsumeByTokenHash` returns it once and marks `consumed_at`; a second consume → error; an expired grant → error; unknown hash → error.
- [ ] **Step 4: Run** → FAIL.
- [ ] **Step 5: implement repo.** `ConsumeByTokenHash` in a transaction: `SELECT ... FOR UPDATE WHERE token_hash=? AND consumed_at IS NULL AND expires_at > now()`; if found set `consumed_at=now()` and return; else return a typed `ErrExportGrantUnavailable`.
- [ ] **Step 6: Completer branch.** In `ManagedCertificateService.OnApproved`, branch on `a.Action`: `ApprovalActionExport` → generate a random token (32 bytes hex, mirror `sso_service.go:534`), store its **sha256 hash** in a new grant (`ExpiresAt = now()+15m`), and stash the plaintext token for the requester to fetch (return it via the approval's result path / a `LastExportToken` the handler reads — see Task 7 for delivery). `ApprovalActionCreate` → existing issue path. Add `DistRepo`-style nil-checks only if new deps are added (the grant repo is a new dep on the service — add `ExportGrantRepo` to `ManagedCertificateServiceDeps`, nil-check, update main.go + test helper; `make mocks`).
- [ ] **Step 7: Run** `go test ./internal/services/ ./internal/repository/ -run 'ExportGrant|Export|ManagedCertificate' -v && go build ./...` → PASS.

---

## Task 7: Export request endpoint + single-use download

**Files:** Modify `internal/services/managed_certificate_service.go`, `internal/handlers/managed_certificate_handler.go`, `internal/handlers/service_interfaces.go`, `cmd/server/main.go`; Test handler + service `_test.go`.

**Interfaces — Produces:**
- Service: `RequestExport(certID, requestedBy uuid.UUID) (*models.Approval, error)` (opens an `ApprovalEntityCertificate` + `ApprovalActionExport` approval; managed-mode only — `csr` → `ErrExportNotApplicable`); `ExportBundle(token string) (leafPEM, keyPEM, caChainPEM []byte, err error)` (consume grant → read the leaf Secret's `tls.crt`/`tls.key` from the control cluster + the issuer CA `ca.crt` → return; single-use).
- Handler+routes: `POST /projects/:projectId/certificates/:certificateId/export` (cert.export/approve gated, cross-project 404) → 202 + approval; `GET /projects/:projectId/certificates/:certificateId/export/download?token=...` → 200 bundle (PEM or PKCS#12), or 410 if the grant is unavailable.

- [ ] **Step 1: failing tests.** Service: `RequestExport` on a `csr` cert → `ErrExportNotApplicable`; on managed → opens the export approval. `ExportBundle` with a valid (unconsumed) token → returns leaf+key+CA and marks consumed; a reused token → `ErrExportGrantUnavailable`. Handler: export request 202; download with a good token 200 (body contains a PRIVATE KEY block) and NotContains after re-download (410); cross-project 404; `csr` cert export → 409.
- [ ] **Step 2: Run** → FAIL. `make mocks`.
- [ ] **Step 3: implement.** `ExportBundle`: `grant, err := s.exportGrantRepo.ConsumeByTokenHash(sha256hex(token))`; load cert; read the leaf Secret via `s.controlPlane.Get(kubernetes.CoreSecretGVR, cert.Config.SecretName, true)` → base64-decode `tls.crt`/`tls.key` (mirror `internal/certdist/adapters.go:35-52`); read the issuer CA (`s.controlPlane.Get(CoreSecretGVR, issuer.Config.CASecretName, true)` → `ca.crt`, falling back to `tls.crt`); return the three PEMs. Handler streams them as a bundle. Wire routes in main.go's cert group; add a `certificate.export`/`certificate.approve` check (reuse `CanEditCertificates` or add `CanExportCertificates` mirroring it — decide and note; default: gate the *request* by `certificate.create`-level and rely on the approval for the real gate).
- [ ] **Step 4: Run** `go test ./internal/services/ ./internal/handlers/ -run 'Export' -v && go build ./...` → PASS.

---

## Task 8: Attach/detach client cert + issuer-CA materialization

**Files:** Create `internal/services/clients/client_certificate.go` + `_test.go`; Modify `internal/handlers/client_handler.go`, `internal/handlers/service_interfaces.go` (ClientServiceInterface), `cmd/server/main.go`; regenerate mocks.

**Interfaces — Produces:**
- `ClientService.AttachCertificate(ctx, clientID, certID, actingUser uuid.UUID) (*models.Client, error)` and `DetachCertificate(ctx, clientID, actingUser uuid.UUID) (*models.Client, error)`.
- Routes `PUT /clients/:clientId/certificate {certificateId}` and `DELETE /clients/:clientId/certificate` (sibling to the mtls routes at main.go:718-720; team-membership gated like `UpdateClientMTLS`).

**Design rulings:** attach validates cert `usage=client`, `ready`; the **client's team has a role in the cert's project** (via `ProjectTeamRole`/`ListTeamProjects`); the cert is not already attached (**409**, via `GetByManagedCertificateID`); the client has no managed cert already (**409**, detach first). On attach: read the issuer CA PEM (control-plane `ca-<id>` Secret) → set `client.MTLSEnabled=true, MTLSCAPem=<issuerCAPem>, MTLSCAName=<cert.Name>, MTLSCASecret="fastgateway-client-"+id8+"-mtls-ca", MTLSCASecretKey="ca.crt", MTLSSANs=<from cert DNSNames(Type DNS)+URISANs(Type URI)>, ManagedCertificateID=&certID`; persist; the CA secret + CTP are (re)materialized by the existing deploy path — call `EnsureMTLSClientTrafficPolicy` for the client's attached domains (mirror `route_deploy_clients.go:39-41`). Detach clears the managed fields + FK (revert to no-managed; leave BYO untouched only if it predated — simplest: detach clears mTLS to disabled unless BYO was separately set — clear `ManagedCertificateID` and the derived mTLS fields).

- [ ] **Step 1: failing tests** (mocked client/cert/issuer repos + control-plane + k8s secrets): attach happy path (fields derived, FK set, CTP re-applied); rejections (cert wrong-usage → err; not ready → err; client's team not in cert's project → err; cert already attached → err; client already has managed cert → err) each asserting no mutation; detach clears FK + mTLS.
- [ ] **Step 2: Run** → FAIL. `make mocks`.
- [ ] **Step 3: implement** the service (new file) + add the two methods to `ClientServiceInterface`; add handlers mirroring `UpdateClientMTLS` (`client_auth_handler.go:287-306`: `IsTeamMember` check, audit log) with the sentinel→status mapping (404/409/422); register routes.
- [ ] **Step 4: Run** `go test ./internal/services/clients/ ./internal/handlers/ -run 'Certificate|Client' -v && go build ./...` → PASS.

---

## Task 9: Distributor skips `usage=client`

**Files:** Modify `internal/certdist/certdist.go`; Test `certdist_test.go`.

- [ ] **Step 1: failing test:** `reconcileOne` (or `Reconcile`) given a `usage=client` cert does NOT read its source secret or push (assert the SourceReader/TenantWriter are not called for it), while a `usage=server` cert still reconciles.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: implement:** at the top of `reconcileOne`, `if cert.Usage != models.ManagedCertUsageServer { return nil }` (client leaves are identities, not listener secrets). Add a one-line comment citing the spec.
- [ ] **Step 4: Run** `go test ./internal/certdist/ -run Reconcile -v && go build ./...` → PASS.

---

## Task 10: Delete referential guard (cert bound to a client)

**Files:** Modify `internal/services/managed_certificate_service.go` (`Delete`), `internal/handlers/managed_certificate_handler.go` (already maps `ErrCertificateInUse`→409 from Phase 3b); Test `_test.go`.

**Interfaces — Consumes:** `ClientRepository.GetByManagedCertificateID` (Task 2). **Design:** add a `ClientRepo` (or a narrow reader) dep to the cert service; `Delete` returns `ErrCertificateInUse` (existing sentinel, mapped to 409) if a Client references the cert — in addition to the existing domain guard.

- [ ] **Step 1: failing test:** `Delete` when a client references the cert (`GetByManagedCertificateID` returns one) → `ErrCertificateInUse`, repo delete NOT called; no reference → proceeds.
- [ ] **Step 2: Run** → FAIL. Add the dep; `make mocks`; update main.go + test helper construction sites.
- [ ] **Step 3: implement** the extra guard in `Delete` before the domain guard/delete; distinguish `gorm.ErrRecordNotFound` (no client) from a real error.
- [ ] **Step 4: Run** `go test ./internal/services/ -run 'Delete|ManagedCertificate' -v && go build ./...` → PASS.

---

## Task 11: OpenAPI + full gate

**Files:** Modify OpenAPI source under `docs/openapi/` (create-cert op gains `keyMode`/`uriSans`/`csr`; new export request + download ops; new client attach/detach ops); rebundle `cmd/server/openapi.yaml`.

- [ ] **Step 1:** update `project-certificates.yaml` create op (`keyMode`, `uriSans`, `csr` fields); add `POST .../certificates/{certificateId}/export` + `GET .../export/download`; add `PUT`/`DELETE /clients/{clientId}/certificate` (source file for client ops). Register new paths in `openapi.yaml`. No key material in any response schema except the export-download (document it as an opaque binary/PEM download).
- [ ] **Step 2:** `make openapi` + `make openapi-check` (exit 0).
- [ ] **Step 3: full gate — ALL pass:** `go build ./...`; `go vet ./cmd/... ./internal/...`; `gofmt -l .` excl `docs/superpowers` empty; `go test ./cmd/server/ -run TestRouteSpecParity -count=1` PASS; `go test ./... -count=1` all pass; `make mocks` idempotent.
- [ ] **Step 4: secret-leak grep:** confirm only the export-download path returns key material; all other new responses expose metadata only.

---

## Self-Review

**1. Spec coverage:** taxonomy+`KeyMode` → T1; `Client` 1:1 FK → T2; client-auth leaf (usages/URI SANs) → T3; CSR builder → T4; create branching + issuer-type check + CSR read-back → T5; export approval + one-time grant → T6/T7; attach/detach + issuer-CA materialization + team↔project bridge → T8; distributor skip → T9; delete guard → T10; OpenAPI/gate → T11. Ownership/approval reuse existing engine (`certificate` entity + `certificate.approve`). Revocation = detach+delete (T8/T10). ✅ all spec sections mapped.

**2. Placeholder scan:** every code step has concrete code/commands. The two "decide" notes (export permission gate in T7; whether detach preserves a pre-existing BYO in T8) are explicit rulings with a stated default, not TODOs.

**3. Type consistency:** `ManagedCertKeyMode`/`Config.KeyMode`/`Config.URISANs`/`Config.CSRPEM` (T1/T5) used by T3/T4/T5; `CertificateRequestObject`+GVR (T4) consumed by T5; `Client.ManagedCertificateID` + `GetByManagedCertificateID` (T2) used by T8/T10; `CertificateExportGrant`+`ConsumeByTokenHash` (T6) used by T7; `ApprovalActionExport` (T6) branched in `OnApproved` (T6) + opened by `RequestExport` (T7). Two dep ripples flagged (ExportGrantRepo into the cert service in T6; ClientRepo into the cert service in T10) — each says update all construction sites + `make mocks`.

**Design rulings baked in:** client SANs via `DNSNames`+`URISANs` (no imposed format); `usages` set by usage; CSR via `CertificateRequest` with signed cert read from status (no key); export is a DB-backed single-use hashed-token grant (multi-replica safe); attach derives trust from the issuer CA (control-plane read → tenant secret) and is team-membership gated, not approval-gated; distributor skips `usage=client`; delete guarded for both domains (existing) and clients (new). Tree-wide gofmt + OpenAPI parity before commit.

---

## (Frontend-requested additions — execution order: after Task 10, before Task 11 so OpenAPI documents them last)

### Task 12: Expose keyMode / subject / uriSans in the cert response DTO (P1)

**Files:** Modify `internal/handlers/managed_certificate_handler.go` (`managedCertificateResponse` struct + `toManagedCertificateResponse`); Test `managed_certificate_handler_test.go`.

**Why:** the frontend must distinguish managed vs csr (to show Export only for managed) and display the client identity (subject / URI SANs). These live in `ManagedCertConfig` but aren't serialized. `enrichedCertificateResponse` embeds `managedCertificateResponse`, so adding them here propagates to the enriched list, fleet, single GET, and create response. None are secret.

- [ ] **Step 1: failing test** — assert `toManagedCertificateResponse` output includes `keyMode`, `subject`, `uriSans` from `c.Config`.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: implement** — add to the struct: `KeyMode models.ManagedCertKeyMode json:"keyMode,omitempty"`, `Subject string json:"subject,omitempty"`, `URISANs []string json:"uriSans,omitempty"`; set them in `toManagedCertificateResponse` from `c.Config.KeyMode` / `c.Config.Subject` / `c.Config.URISANs`.
- [ ] **Step 4: Run** `go test ./internal/handlers/ -run 'ManagedCertificate|Certificate' -v && go build ./...` → PASS.

### Task 13: `GET /clients/:clientId/attachable-certificates` (P2.1)

**Files:** Modify `internal/repository/managed_certificate_repository.go` + `interfaces.go` (new query); `internal/services/clients/client_certificate.go` (new method on `ClientCertificateService`); `internal/handlers/client_certificate_handler.go` + `cmd/server/main.go` (route); Tests.

**Interfaces — Produces:**
- Repo: `ListAttachableClientCerts(projectIDs []uuid.UUID) ([]models.ManagedCertificate, error)` — certs with `usage='client' AND status='ready' AND project_id IN (?)` that are NOT already attached to any client (`id NOT IN (SELECT managed_certificate_id FROM clients WHERE managed_certificate_id IS NOT NULL)`), across the given projects. Empty `projectIDs` → empty slice, no query.
- Service: `ClientCertificateService.AttachableCertificates(clientID uuid.UUID) ([]models.ManagedCertificate, error)` — loads the client, gets its team's project IDs via `TeamRepo.ListTeamProjects(client.TeamID)`, calls the repo query. (Requires no new dep — the service already has ClientRepo/TeamRepo/CertRepo; use CertRepo for the query.)

- [ ] **Step 1: failing repo test** (Postgres harness): seed client certs across two projects (some ready, some pending; one already attached to a client); assert only ready+client+unattached in the given projects are returned. Skips cleanly without Postgres.
- [ ] **Step 2: failing service test** (mocked repos): given a client whose team has projects [A], returns the repo result for [A]; asserts the project set passed = the team's projects.
- [ ] **Step 3: failing handler test:** `GET /clients/:clientId/certificate?...` route returns 200 with the list; team-membership 403; client-not-found 404.
- [ ] **Step 4: Run** → FAIL. `make mocks`.
- [ ] **Step 5: implement** the repo query (chain the NOT-IN subquery via gorm), the service method, the handler + route `GET /clients/:clientId/attachable-certificates` (nil-guarded like the other client-cert routes; team-membership gated). Return items as `[]managedCertificateResponse` (reuse the DTO; no secret material).
- [ ] **Step 6: Run** `go test ./internal/repository/ ./internal/services/clients/ ./internal/handlers/ -run 'Attachable|Certificate|Client' -v && go build ./...` → PASS.

### Task 14: `exportAvailable` signal on the cert DTO (P2.2)

**Files:** Modify `internal/repository/certificate_export_grant_repository.go` + `interfaces.go` (non-consuming checks); `internal/services/managed_certificate_view.go` (`EnrichedCertificate` field) + `internal/services/managed_certificate_service.go` (list methods gain a viewer param; `HasUsableExportGrant`); `internal/handlers/managed_certificate_handler.go` (DTOs + Get/List/ListFleet wiring) + `internal/handlers/service_interfaces.go`; `cmd/server/main.go` if signatures change callers; Tests. Regenerate mocks.

**Interfaces — Produces:**
- Repo (non-consuming, additive to `CertificateExportGrantRepositoryInterface`): `HasUsableGrant(certID, userID uuid.UUID) (bool, error)` and `ListUsableGrantCertIDs(userID uuid.UUID, certIDs []uuid.UUID) ([]uuid.UUID, error)` — both `consumed_at IS NULL AND expires_at > now() AND granted_to = ?`; the batch variant filters `managed_certificate_id IN (?)`; empty input → empty, no query. (Do NOT mark anything consumed — read-only.)
- `EnrichedCertificate` gains `ExportAvailable bool`.
- `ManagedCertificateService`: `ListProjectCertificatesEnriched` / `ListFleetCertificates` gain a `viewerID uuid.UUID` param; after `enrich(certs)` they batch `ListUsableGrantCertIDs(viewerID, certIDs)` and set `ExportAvailable` on each. New method `HasUsableExportGrant(certID, userID uuid.UUID) (bool, error)` (delegates to the repo) for the single-cert path.

- [ ] **Step 1: failing repo test** (Postgres harness): a usable grant → `HasUsableGrant` true + `ListUsableGrantCertIDs` includes it; consumed/expired/other-user → false/excluded.
- [ ] **Step 2: failing service + handler tests:** enriched list marks `exportAvailable=true` for certs the viewer has a usable grant on (batch called once with the page's ids — no N+1); single-cert `Get` sets `exportAvailable` from `HasUsableExportGrant`; create response has `exportAvailable=false`.
- [ ] **Step 3: Run** → FAIL. `make mocks`.
- [ ] **Step 4: implement:** repo methods; add `ExportAvailable` to `EnrichedCertificate`; add the `viewerID` param to the two list service methods + set ExportAvailable via the batch; add `HasUsableExportGrant`; add `ExportAvailable bool json:"exportAvailable"` to `managedCertificateResponse` (embedded → propagates) and set it — in `toEnrichedCertificateResponse` from `e.ExportAvailable`, and in the `Get` handler after `HasUsableExportGrant`. Update the `List`/`ListFleet` handlers to pass `middleware.GetCurrentUser(c).ID` as the viewer; update `ManagedCertificateServiceInterface` signatures + mock.
- [ ] **Step 5: Run** `go test ./internal/repository/ ./internal/services/ ./internal/handlers/ -run 'Export|Enriched|ManagedCertificate|Certificate' -v && go build ./... && go vet ./... && gofmt -l internal/ cmd/` → PASS.

**Note:** Task 11 (OpenAPI + full gate) runs LAST — after Tasks 12/13/14 — and its OpenAPI updates must include the new response fields (`keyMode`, `subject`, `uriSans`, `exportAvailable`), the attach/detach + export request/download ops, AND `GET /clients/{clientId}/attachable-certificates`.
