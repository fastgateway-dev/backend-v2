# Managed Certificates — Phase 3b Implementation Plan (Domain server-cert attachment)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a project attach an issued `usage=server` managed certificate to a Domain, so the Domain's Gateway listener terminates TLS with that cert; support detach; and close the two Phase-3a items parked for 3b (tenant-Secret cleanup on cert delete; fingerprint-format drift).

**Architecture:** A Domain gains a nullable `ManagedCertificateID` FK. The tenant TLS Secret name is deterministic (`cert-<certID>` in `fastgateway-system`) and is already pushed to the project's cluster by the Phase-3a distribution controller for every `ready` cert — so attach does not distribute anything; it resolves the Domain's *effective* TLS secret (managed FK wins over legacy `TLSSecretName`) and re-applies the Gateway listener via a NEW `UpdateGateway` apply path (mirroring the existing `UpdateBackendTrafficPolicy` get-then-update-or-create pattern), which also fills the long-standing `domain_service.go` "TODO: Update Kubernetes resources" gap. Detach nulls the FK and re-applies (listener reverts to legacy secret or no-TLS). Attach/detach are `CanManageDomains`-gated, NOT approval-gated.

**Tech Stack:** Go 1.25, Gin, GORM, golang-migrate, mockery v3 (`make mocks`), testify, dynamic fake clients (`k8s.io/client-go/dynamic/fake`), `//go:embed` OpenAPI bundle (`make openapi` / `make openapi-check`).

**Spec:** `docs/superpowers/specs/2026-09-19-managed-certificates-design.md` (§3 data-model Domain wiring; §5 stable-name ⇒ renewal-never-edits-Gateway + the missing `UpdateGateway`; §9 referential guards). API contract: `docs/superpowers/plans/2026-09-19-managed-certificates-api.md` (Phase 3 "Attach a server cert to a Domain").

## Global Constraints

- No private key material in the DB, in any response DTO, or in logs.
- All cluster writes are create-or-update **idempotent** (Gateway apply must not error on AlreadyExists).
- Managed-cert tenant Secret is `cert-<certID>` in namespace `fastgateway-system` (= `kubernetes.FastGatewayNamespace`); Domain's default namespace is also `fastgateway-system`. When a Domain's own `Namespace` equals the secret namespace, the listener certRef carries NO `namespace` key and needs no ReferenceGrant; only a Domain in a non-default (project-managed) namespace needs the cross-namespace ReferenceGrant.
- House patterns: `Deps` struct + panic-on-nil constructor for required deps; repo interface + `var _ Interface = (*Impl)(nil)`; DTO responses; **every by-ID route does a `resource.ProjectID != projectID → 404` check** (domain by-ID handlers do NOT have this today — the new endpoints must add it); migrations sequential (next is `000043`); run tree-wide `gofmt -l .` (excl `docs/superpowers`) before finishing; edit OpenAPI **source** under `docs/openapi/` then `make openapi` to rebundle and `make openapi-check` + `TestRouteSpecParity`.
- Attach/detach of a server cert to a Domain is `CanManageDomains`-gated and **not** approval-gated (approval covers cert *issuance*, not domain attachment; client-cert mTLS attach remains deferred).
- Execution: subagents never commit; the controller holds changes in the working tree and the human commits explicitly. Commit exactly the enumerated feature files via explicit `git add` (never `-A`).

## File Structure

- `internal/models/domain.go` — add `ManagedCertificateID *uuid.UUID` FK + `ManagedCertificate *models.ManagedCertificate` relationship (Task 1).
- `migrations/000043_add_domain_managed_certificate.{up,down}.sql` — add nullable FK column + index (Task 1).
- `internal/repository/domain_repository.go` + `internal/repository/interfaces.go` — `ListByManagedCertificateID` (Task 2).
- `internal/cluster/gateway.go` + `internal/services/k8s_roles.go` — new `UpdateGateway` (Task 3).
- `internal/domainplan/gateway.go` — effective-TLS-secret resolution when FK set (Task 4).
- `internal/services/domain_service.go` — wire `UpdateGateway` into `Update` (fill the TODO) + `AttachCertificate` / `DetachCertificate` methods (Task 5).
- `internal/handlers/domain_handler.go` + `cmd/server/main.go` — `AttachCertificate` / `DetachCertificate` handlers + routes (Task 6).
- `internal/services/managed_certificate_service.go` — `Delete` referential guard + cluster cleanup; fingerprint normalization (Task 7).
- `docs/openapi/paths/domains.yaml` (or wherever domain paths live) + rebundle; final gate (Task 8).

---

## Task 1: Domain `ManagedCertificateID` FK + migration

**Files:**
- Modify: `internal/models/domain.go` (struct at lines 27-62; mirror `DomainTemplateID *uuid.UUID` at :30 and its `*DomainTemplate` relationship)
- Create: `migrations/000043_add_domain_managed_certificate.up.sql`, `migrations/000043_add_domain_managed_certificate.down.sql`
- Test: `internal/models/domain_test.go` if one exists (else skip model unit test — it's a plain struct field)

**Interfaces — Produces:** `Domain.ManagedCertificateID *uuid.UUID` (json `managedCertificateId,omitempty`, gorm column `managed_certificate_id`), consumed by Tasks 2/4/5.

- [ ] **Step 1: add the field.** In `internal/models/domain.go`, after `DomainTemplateID` (or near the other FKs), add:
```go
	ManagedCertificateID *uuid.UUID `gorm:"type:uuid;column:managed_certificate_id" json:"managedCertificateId,omitempty"`
```
and in the Relationships block (near `DomainTemplate *DomainTemplate`):
```go
	ManagedCertificate *ManagedCertificate `gorm:"foreignKey:ManagedCertificateID" json:"-"`
```

- [ ] **Step 2: write the up migration** `migrations/000043_add_domain_managed_certificate.up.sql`:
```sql
ALTER TABLE domains
    ADD COLUMN managed_certificate_id UUID REFERENCES managed_certificates(id) ON DELETE RESTRICT;

CREATE INDEX idx_domains_managed_certificate_id ON domains(managed_certificate_id);
```
(Use `ON DELETE RESTRICT`: the DB enforces the referential guard as a backstop; the service also returns a friendly 409 in Task 7. Confirm the referenced table is named `managed_certificates` — check `migrations/000039*`/`000040*` or wherever the managed cert table was created.)

- [ ] **Step 3: write the down migration** `migrations/000043_add_domain_managed_certificate.down.sql`:
```sql
DROP INDEX IF EXISTS idx_domains_managed_certificate_id;
ALTER TABLE domains DROP COLUMN IF EXISTS managed_certificate_id;
```

- [ ] **Step 4: verify** `go build ./...` and (if migrations run in CI test harness) that the migration applies cleanly. Confirm the exact managed-cert table name with a grep before finalizing the FK reference.

---

## Task 2: Domain lookup by managed certificate

**Files:**
- Modify: `internal/repository/domain_repository.go`, `internal/repository/interfaces.go` (DomainRepositoryInterface)
- Test: `internal/repository/domain_repository_test.go` (mirror an existing repo test in that file)
- Regenerate: `internal/mocks/mock_repositories.go` via `make mocks`

**Interfaces — Produces:** `DomainRepositoryInterface.ListByManagedCertificateID(certID uuid.UUID) ([]models.Domain, error)` — consumed by Task 7's referential guard. Returns all domains whose `managed_certificate_id = certID` (empty slice, nil error when none).

- [ ] **Step 1: failing repo test.** In `domain_repository_test.go`, add a test that creates two domains (one with `ManagedCertificateID` set to a known UUID, one nil) and asserts `ListByManagedCertificateID(certID)` returns exactly the one. Mirror the existing test setup (in-memory/sqlite or the test DB harness already used in that file).

- [ ] **Step 2: Run** → FAIL (method undefined).

- [ ] **Step 3: implement.** Add to `interfaces.go` `DomainRepositoryInterface`:
```go
	ListByManagedCertificateID(certID uuid.UUID) ([]models.Domain, error)
```
and in `domain_repository.go`:
```go
func (r *DomainRepository) ListByManagedCertificateID(certID uuid.UUID) ([]models.Domain, error) {
	var domains []models.Domain
	if err := r.db.Where("managed_certificate_id = ?", certID).Find(&domains).Error; err != nil {
		return nil, err
	}
	return domains, nil
}
```
Then `make mocks`.

- [ ] **Step 4: Run** `go test ./internal/repository/ -run Domain -v && go build ./...` → PASS.

---

## Task 3: `UpdateGateway` apply path (fills the TODO)

**Files:**
- Modify: `internal/cluster/gateway.go` (add `UpdateGateway`), `internal/services/k8s_roles.go` (add to `GatewayApplier` interface, ~lines 178-185)
- Test: `internal/cluster/gateway_test.go` (mirror the dynamicfake pattern used by `internal/cluster/policy_test.go` for `UpdateBackendTrafficPolicy`)
- Regenerate: `internal/mocks/` via `make mocks` (GatewayApplier mock gains `UpdateGateway`)

**Interfaces — Produces:** `GatewayApplier.UpdateGateway(ctx context.Context, projectID uuid.UUID, config *kubernetes.GatewayConfig) error` — get-existing / preserve `resourceVersion` / `Update`, falling back to `Create` on NotFound (idempotent). Consumed by Task 5.

- [ ] **Step 1: failing test.** In `gateway_test.go`, using a dynamic fake client (mirror `policy_test.go`'s `UpdateBackendTrafficPolicy` test): (a) `UpdateGateway` on a non-existent Gateway creates it; (b) a second `UpdateGateway` with a changed field updates it without an AlreadyExists error and the change is reflected. Assert on the object read back from the fake.

- [ ] **Step 2: Run** → FAIL.

- [ ] **Step 3: implement.** In `internal/cluster/gateway.go`, add (mirror `UpdateBackendTrafficPolicy` at `internal/cluster/policy.go:130-158`):
```go
func (c *Client) UpdateGateway(ctx context.Context, projectID uuid.UUID, config *kubernetes.GatewayConfig) error {
	client, gvr, err := c.gatewayClientFor(projectID) // use whatever CreateGateway uses to get (dynamic client, gvr)
	if err != nil {
		return err
	}
	obj := kubernetes.BuildGatewayObject(config)
	existing, err := client.Resource(gvr).Namespace(config.Namespace).Get(ctx, config.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			_, cerr := client.Resource(gvr).Namespace(config.Namespace).Create(ctx, obj, metav1.CreateOptions{})
			return cerr
		}
		return err
	}
	obj.SetResourceVersion(existing.GetResourceVersion())
	obj.SetUID(existing.GetUID())
	_, uerr := client.Resource(gvr).Namespace(config.Namespace).Update(ctx, obj, metav1.UpdateOptions{})
	return uerr
}
```
Match the exact client/gvr acquisition and imports used by the existing `CreateGateway` (`internal/cluster/gateway.go:14-39`) — do not invent a new client accessor. Add `UpdateGateway` to the `GatewayApplier` interface in `k8s_roles.go`. Then `make mocks`.

- [ ] **Step 4: Run** `go test ./internal/cluster/ -run Gateway -v && go build ./...` → PASS.

---

## Task 4: Effective-TLS-secret resolution when a managed cert is attached

**Files:**
- Modify: `internal/domainplan/gateway.go` (`BuildGatewayConfig`, lines 24-42)
- Test: `internal/domainplan/gateway_test.go` (mirror existing builder tests there)

**Interfaces — Consumes:** `Domain.ManagedCertificateID` (Task 1). **Produces:** `BuildGatewayConfig` now yields a `GatewayConfig` whose `TLSSecretName`/`TLSSecretNamespace` reflect the managed cert when the FK is set.

- [ ] **Step 1: failing test.** In `gateway_test.go`, add cases: (a) domain with `ManagedCertificateID = <uuid>` → resulting `GatewayConfig.TLSSecretName == "cert-<uuid>"` and `TLSSecretNamespace == "fastgateway-system"`, regardless of the domain's legacy `TLSSecretName`; (b) domain with nil FK → uses the legacy `TLSSecretName`/`TLSSecretNamespace` unchanged.

- [ ] **Step 2: Run** → FAIL.

- [ ] **Step 3: implement.** In `BuildGatewayConfig`, before constructing the `GatewayConfig`, resolve the effective secret:
```go
	tlsSecretName := domain.TLSSecretName
	tlsSecretNamespace := domain.TLSSecretNamespace
	if domain.ManagedCertificateID != nil {
		// A managed cert wins over any legacy BYO secret. The distribution
		// controller (Phase 3a) pushes the leaf to cert-<id> in
		// fastgateway-system for every ready cert, so the name is
		// deterministic and needs no cert lookup here.
		tlsSecretName = "cert-" + domain.ManagedCertificateID.String()
		tlsSecretNamespace = kubernetes.FastGatewayNamespace
	}
```
and use `tlsSecretName`/`tlsSecretNamespace` in the `GatewayConfig` literal. Import `kubernetes` if not already imported (it references `kubernetes.GatewayConfig` already, so it is). The existing `internal/kubernetes/gateway.go` listener builder already omits the `namespace` key when `TLSSecretNamespace == Namespace` (line ~55), so the common `fastgateway-system` case stays single-namespace automatically.

- [ ] **Step 4: Run** `go test ./internal/domainplan/ -v && go build ./...` → PASS.

---

## Task 5: DomainService — apply Gateway on Update + Attach/Detach methods

**Files:**
- Modify: `internal/services/domain_service.go` (`Update` at :331-374 to fill the `:371` TODO; add `AttachCertificate` / `DetachCertificate`; add a shared `applyGateway` helper)
- Modify: `internal/services/domain_service.go` deps if a managed-cert repo lookup is needed for validation (see Step 3)
- Test: `internal/services/domain_service_test.go`

**Interfaces — Consumes:** `GatewayApplier.UpdateGateway` (Task 3), `BuildGatewayConfig` (Task 4), and a way to read a `ManagedCertificate` by ID for validation. **Produces:**
- `AttachCertificate(domainID, certID, projectID uuid.UUID) (*models.Domain, error)`
- `DetachCertificate(domainID, projectID uuid.UUID) (*models.Domain, error)`

**Design rulings (bind this task):**
- Attach validates the cert: it must exist, belong to `projectID`, have `usage == server`, and status `ready`. Reject otherwise (return typed errors the handler maps to 400/404/422 — see handler task). Cross-project cert access returns not-found (404), matching the cert handler pattern.
- Attach does NOT distribute the secret. The 3a controller already pushes every ready cert to `fastgateway-system`. Attach only sets the FK and re-applies the Gateway listener.
- `AttachCertificate`/`DetachCertificate` and `Update` share one `applyGateway(domain)` helper that: builds the config via `domainplan.BuildGatewayConfig`, calls `s.k8sGateways.UpdateGateway`, sets `domain.Status`/`StatusMessage` on failure (mirror the Create path at `domain_service.go:295-305`), and calls `s.syncReferenceGrants(...)` when the effective TLS secret namespace differs from the domain's namespace (fixes the "syncReferenceGrants never called from Update" landmine).

- [ ] **Step 1: decide the managed-cert read dependency.** `DomainService` needs to read a `ManagedCertificate` for validation. Prefer a narrow read-only interface dep (house pattern) over the full service — add to `DomainServiceDeps`:
```go
	ManagedCertLookup ManagedCertReader // interface { GetByID(id uuid.UUID) (*models.ManagedCertificate, error) }
```
Define `ManagedCertReader` in `domain_service.go` (or `interfaces.go`), satisfied by `repository.ManagedCertificateRepositoryInterface`. Add the nil-check to `NewDomainService`, and **update all construction sites** (`cmd/server/main.go` domain-service construction + the domain service test helper) — this is a required-dep ripple; update every `NewDomainService`/`DomainServiceDeps{...}` site. `make mocks` if the interface is mockery-managed.

- [ ] **Step 2: failing tests.** In `domain_service_test.go`:
  - `AttachCertificate` happy path: cert exists, same project, usage=server, ready → domain's `ManagedCertificateID` set, `UpdateGateway` called once, returns updated domain.
  - Attach rejects: cert in a different project → not-found error; `usage=client` → validation error; status not ready → validation error. Assert `UpdateGateway` NOT called on rejection and the FK NOT set.
  - `DetachCertificate`: FK cleared, `UpdateGateway` called, returns domain (listener reverts to legacy `TLSSecretName`).
  - Fill-the-TODO regression: `Update` that changes `TLSSecretName` now calls `UpdateGateway` (assert it's invoked; today it isn't).
  Use mocked `GatewayApplier` (now with `UpdateGateway`) and a mocked `ManagedCertReader`.

- [ ] **Step 3: Run** → FAIL. `make mocks`.

- [ ] **Step 4: implement** the `applyGateway` helper, wire it into `Update` (replacing the `// TODO: Update Kubernetes resources` at :371), and add `AttachCertificate`/`DetachCertificate`. Attach sets `domain.ManagedCertificateID = &certID` and persists via `s.domainRepo.Update(domain)` then `applyGateway`. Detach sets it to `nil`. Both re-fetch/validate the domain first.

- [ ] **Step 5: Run** `go test ./internal/services/ -run 'Domain' -v && go build ./...` → PASS.

---

## Task 6: Attach/Detach handlers + routes

**Files:**
- Modify: `internal/handlers/domain_handler.go` (add `AttachCertificate`, `DetachCertificate`); `internal/handlers/service_interfaces.go` if the domain service interface used by the handler must gain the two methods
- Modify: `cmd/server/main.go` (register routes in the `/:projectId/domains` group, ~lines 811-816, alongside the `/settings` sub-routes)
- Test: `internal/handlers/domain_handler_test.go`

**Interfaces — Consumes:** `DomainService.AttachCertificate` / `DetachCertificate` (Task 5). **Produces:** routes `PUT /projects/:projectId/domains/:domainId/certificate` (body `{certificateId}`, auth `CanManageDomains`) and `DELETE /projects/:projectId/domains/:domainId/certificate` (auth `CanManageDomains`).

**Design rulings (bind this task):**
- **Both handlers MUST do the cross-project 404 check** the domain handlers lack today: fetch the domain by `domainId`, and if `domain.ProjectID != projectID` return 404 (mirror `managed_certificate_handler.go:193-200`). Do this before the service call.
- Permission: `if !h.permChecker.CanManageDomains(projectID, user) { 403 }` (mirror the existing domain `Update` handler at `domain_handler.go:163-210`).
- Map service validation errors: cert-not-found / wrong-project → 404; wrong usage or not-ready → 422 (semantic) or 400; success → 200 with the updated domain DTO (reuse the existing domain response DTO; do not leak secret material — the domain DTO already excludes it).
- Audit-log the attach/detach the same way `Update`/`Delete` handlers do.

- [ ] **Step 1: failing handler tests.** In `domain_handler_test.go`: attach success (200, FK reflected in response), attach on a domain in another project (404), attach without `CanManageDomains` (403), attach a nonexistent/other-project cert (404), attach a `usage=client`/not-ready cert (422/400), detach success (200). Use the mocked domain service; assert the cross-project 404 fires before the service is called.

- [ ] **Step 2: Run** → FAIL. `make mocks`.

- [ ] **Step 3: implement** the two handlers + add the two methods to the handler's domain-service interface (`service_interfaces.go`) with the `var _` assertion; register the routes in `main.go`.

- [ ] **Step 4: Run** `go test ./internal/handlers/ -run 'Domain' -v && go build ./...` → PASS.

---

## Task 7: ManagedCertificate Delete — referential guard + cluster cleanup + fingerprint normalization

**Files:**
- Modify: `internal/services/managed_certificate_service.go` (`Delete` at :342-344 — currently a bare repo delete; and `Status` fingerprint format)
- Modify: `internal/services/managed_certificate_service.go` deps if a domain-lookup or secret-delete dep must be added (see below)
- Test: `internal/services/managed_certificate_service_test.go`

**Interfaces — Consumes:** `DomainRepositoryInterface.ListByManagedCertificateID` (Task 2), `CertificateDistributionRepositoryInterface.DeleteByCertificateID` (exists, currently dead code), a tenant-secret delete, and the control-plane `Certificate` CRD delete.

**Design rulings (bind this task):**
- **Referential guard:** `Delete` returns a typed "in use" error (handler → 409) if `ListByManagedCertificateID(id)` is non-empty. This requires adding a domain-repo dep to the managed-cert service — a required-dep ripple; add `DomainRepo repository.DomainRepositoryInterface` (or a narrow reader) to `ManagedCertificateServiceDeps`, nil-check it, and update ALL construction sites (the service test helper + `cmd/server/main.go`). `make mocks`.
- **Cluster cleanup on delete (closes parked 3a nit #4):** after the guard passes and before/after the DB row delete, best-effort remove (a) the cert-manager leaf `Certificate` CRD in the control cluster (`cert.Config.CertificateName`), (b) the pushed tenant Secret `cert-<id>` in `fastgateway-system`, and (c) the `CertificateDistribution` row via `DeleteByCertificateID`. Cleanup failures are logged, not fatal (the row delete is the user-visible action); mirror the error-tolerance the reconcile loop uses. Check whether the control-plane client and the tenant `Secrets`/`TenantWriter` role already expose a delete; if not, add `DeleteTLSSecret(ctx, projectID, namespace, name)` mirroring `CreateOrUpdateTLSSecret`, and a control-plane `DeleteNamespaced` mirroring the Phase-2 `ApplyNamespaced`. Keep additions minimal and interface-fronted.
- **Fingerprint normalization (closes parked 3a nit #3):** `Status()` currently stores cert-manager's colon-hex `status.fingerprint` into `cert.Fingerprint`, while the distributor stores bare-hex `cluster.CertFingerprint` — the column flip-flops. Normalize `Status()` to store the SAME format the distributor uses. Simplest: **stop persisting `cert.Fingerprint` from `Status()`** and let the distributor be the sole writer of `Fingerprint` (it already sets it via `SetIssuedMeta` on every push). Keep `Status()` persisting `Status`/`StatusMessage`/`NotAfter` as before. Add/adjust a test asserting `Status()` no longer overwrites `Fingerprint` with the colon-hex value.

- [ ] **Step 1: failing tests.** In `managed_certificate_service_test.go`:
  - `Delete` with a referencing domain (mock `ListByManagedCertificateID` returns one) → returns the in-use error; repo `Delete` NOT called.
  - `Delete` with no references → repo `Delete` called; best-effort cleanup (Certificate CRD delete, tenant secret delete, `DeleteByCertificateID`) invoked; a cleanup error does not fail the call.
  - `Status()` on Ready no longer writes the colon-hex fingerprint to the model (assert the captured `Update` arg's `Fingerprint` is unchanged / not the colon-hex string).

- [ ] **Step 2: Run** → FAIL. `make mocks`.

- [ ] **Step 3: implement** the guard + cleanup + fingerprint change; add the `DomainRepo` dep and any secret/CRD delete methods; update all construction sites.

- [ ] **Step 4: Run** `go test ./internal/services/ -run 'ManagedCertificate' -v && go build ./...` → PASS.

---

## Task 8: OpenAPI source + full gate

**Files:**
- Modify: OpenAPI source under `docs/openapi/` (find the file holding `/projects/{projectId}/domains/...` paths — likely `docs/openapi/paths/domains.yaml`; mirror how a sibling domain sub-resource like `/settings` is declared), then rebundle `cmd/server/openapi.yaml` via `make openapi`
- Test: none new (parity is the test)

- [ ] **Step 1:** add the two operations to the source: `PUT /projects/{projectId}/domains/{domainId}/certificate` (body `{certificateId}`, requires `domains` / CanManageDomains, responses 200/400/403/404/422) and `DELETE .../certificate` (200/403/404). Response schema = the existing domain schema (no secret material). Set operationIds/tags matching sibling domain ops and the registered routes.

- [ ] **Step 2:** `make openapi` (regenerate bundle) and `make openapi-check` (exit 0).

- [ ] **Step 3: full gate — ALL must pass:**
  - `go build ./...`
  - `go vet ./cmd/... ./internal/...`
  - `gofmt -l .` excluding `docs/superpowers` — must print nothing (run `gofmt -w` on any listed file)
  - `go test ./cmd/server/ -run TestRouteSpecParity -count=1` PASS
  - `go test ./... -count=1` all pass
  - `make mocks` — confirm no stray drift

- [ ] **Step 4: secret-leak grep** — confirm no key/DNS-cred/EAB material in the new domain-certificate response paths or logs.

---

## Self-Review

**1. Spec coverage:** `Domain.ManagedCertificateID` FK → Task 1; effective-secret + listener → Tasks 4/5; the missing `UpdateGateway`/§5 gap → Tasks 3/5; `PUT/DELETE .../domains/:domainId/certificate` → Task 6; referential guard (§9) → Task 7; parked 3a nits (tenant-secret cleanup, fingerprint drift) → Task 7. Client-cert mTLS attach remains OUT (deferred; no consumer model). Fleet view = Phase 4.

**2. Placeholder scan:** every code step has concrete code; the two "check whether X exists, else add it" steps (secret-delete, control-plane delete in Task 7) name the exact method to mirror. The managed-cert table name in Task 1's FK is the one soft spot — the task says to grep-confirm it before finalizing.

**3. Type consistency:** `ManagedCertificateID *uuid.UUID` (Task 1) is read in Tasks 4/5/7; `UpdateGateway(ctx, projectID, *kubernetes.GatewayConfig) error` (Task 3) is called in Task 5; `ListByManagedCertificateID` (Task 2) is called in Task 7; the tenant secret name `cert-<id>` @ `fastgateway-system` is consistent with Phase 3a (`cert.Config.SecretName`, `tenantSecretNamespace`). Two required-dep ripples are flagged explicitly (Task 5 `ManagedCertLookup` into DomainService; Task 7 `DomainRepo` into ManagedCertificateService) — each task says to update ALL construction sites.

**Design rulings baked in:** attach does not distribute (3a controller already pushes every ready cert); attach/detach are `CanManageDomains`-gated, not approval-gated; new endpoints add the cross-project 404 that domain handlers lack today; `UpdateGateway` mirrors the `UpdateBackendTrafficPolicy` get-then-update-or-create idempotent pattern; `Status()` stops writing `Fingerprint` so the distributor is the sole writer. Run tree-wide gofmt + OpenAPI parity before commit.
