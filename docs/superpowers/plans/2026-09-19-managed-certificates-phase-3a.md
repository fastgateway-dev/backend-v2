# Managed Certificates — Phase 3a (Distribution Controller) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A leader-elected background controller that syncs each issued cert-manager leaf TLS Secret from the control cluster to the one tenant cluster that owns the certificate, propagates renewals, self-heals drift, and records per-cert distribution status.

**Architecture:** Because the leaf TLS Secrets cert-manager produces are **unlabeled** (our `LeafCertificate` labels the `Certificate` CR, not the Secret) and the codebase has **no informer/watch infrastructure**, distribution is a **leader-elected DB-poll reconcile** — not a Secret informer. A single leader (Postgres advisory lock; the API layer scales horizontally, only the leader runs the loop) periodically lists `ready`/`issuing` `ManagedCertificate` rows, reads each cert's known control-cluster Secret (`cert-<id>`), fingerprints it, and pushes it to the cert's `ProjectID` tenant cluster when the fingerprint differs or the tenant Secret is missing/drifted. A Postgres `LISTEN/NOTIFY` nudge lets any API replica trigger a prompt reconcile; a slow full pass provides self-heal. Per-cert state lives in a new `CertificateDistribution` row.

**Tech Stack:** Go 1.25, GORM, Gin, cert-manager, `k8s.io/client-go/dynamic`, Postgres advisory locks (`pg_try_advisory_lock`) + `LISTEN/NOTIFY` (via `lib/pq`), `context` + `signal.NotifyContext`.

**Spec:** `docs/superpowers/specs/2026-09-19-managed-certificates-design.md`
**API contract (Phase 3 section):** `docs/superpowers/plans/2026-09-19-managed-certificates-api.md`
**Phase 1/2 (committed, mirror/consume these):** `internal/cluster/controlplane.go`, `internal/cluster/secret.go` (`CreateOrUpdateSecret`, `GetSecretData`), `internal/kubernetes/certmanager.go`, `internal/models/managed_certificate.go`, `internal/repository/managed_certificate_repository.go`, `internal/services/managed_certificate_service.go` (`Status`, `Config.SecretName="cert-<id>"`).

## Global Constraints

- **DB-poll reconcile, not a Secret informer** (secrets are unlabeled; no informer infra exists). Enumerate certs from `ManagedCertificate` rows by status; the source Secret name is the deterministic `cert-<id>` in `cfg.ControlPlaneNamespace`.
- **Single leader via Postgres advisory lock** on a pinned `*sql.Conn` (`db.DB()` → `sqlDB.Conn(ctx)`); never on the pool. Only the leader reconciles. No new leader-election dependency (no client-go/leaderelection).
- **No private keys in the DB.** The controller moves `tls.crt`/`tls.key` cluster→cluster only; `CertificateDistribution` stores a fingerprint (sha256 hex of `tls.crt`), status, timestamp — never key bytes.
- **Distributed Secret is `type: kubernetes.io/tls`** with keys `tls.crt`/`tls.key` — do NOT reuse `CreateOrUpdateSecret` (it hard-codes `type: Opaque` + `fastgateway.dev/type: mtls-ca`); add a TLS-typed writer.
- **In-cluster only.** The controller runs only inside the `if services.IsRunningInCluster()` block (where `*ControlPlaneClient` exists); local/dev/test don't start it. The controller is unit-tested via interfaces (fake control-plane reader, fake tenant writer, fake repos, fake leader gate).
- **Migrations** continue the sequence: next free is **000042**; confirm with `ls migrations/`.
- **Run a tree-wide `gofmt -l .` before the final commit** (Phase 1 lint lesson).
- **Out of scope (later):** domain/client attachment (Phase 3b / client deferred), the Phase 4 fleet view.

---

## File Structure

**Create:**
- `migrations/000042_add_certificate_distributions.up.sql` / `.down.sql`
- `internal/models/certificate_distribution.go`
- `internal/repository/certificate_distribution_repository.go`
- `internal/certdist/` — new package for the distribution controller:
  - `certdist.go` (the `Distributor` + `Reconcile` + the consumed interfaces)
  - `certdist_test.go`
- `internal/leaderlock/leaderlock.go` (+ `_test.go`) — Postgres advisory-lock leader gate.
- `internal/cluster/tls_secret.go` (+ test) — `CreateOrUpdateTLSSecret` + control-cluster leaf-secret read helper + fingerprint util. (Or extend `secret.go`; keep TLS distinct.)
- `internal/handlers/certificate_distribution_handler.go` (or fold into the existing managed-cert handler).

**Modify:**
- `internal/repository/managed_certificate_repository.go` + `interfaces.go` — add `ListByStatuses`.
- `internal/services/managed_certificate_service.go` — persist `NotAfter`; add `DistributionStatus`/`Resync`; emit `NOTIFY` on approve/create.
- `internal/kubernetes/gvr.go` — add a core Secret GVR constant (used by the reader).
- `cmd/server/main.go` — `signal.NotifyContext` shutdown ctx; construct + start the controller in the in-cluster block; distribution routes; `RouterDeps`.
- `docs/openapi/…` + `cmd/server/openapi.yaml` — distribution + resync ops.
- `.mockery.yml` — auto (packages already `all: true`); `make mocks`.

---

## Task 1: `CertificateDistribution` model + migration + repository

**Files:** Create `internal/models/certificate_distribution.go` (+test), `migrations/000042_add_certificate_distributions.{up,down}.sql`, `internal/repository/certificate_distribution_repository.go`; Modify `internal/repository/interfaces.go`.

**Interfaces — Produces:**
- `models.CertificateDistribution{ ID, ManagedCertificateID, ProjectID uuid.UUID; Status CertDistStatus; LastPushedFingerprint string; Message string; LastSyncedAt *time.Time; CreatedAt, UpdatedAt time.Time }`, `TableName()="certificate_distributions"`, unique index on `ManagedCertificateID`.
- `CertDistStatus` (`pending`/`synced`/`error`).
- `repository.CertificateDistributionRepositoryInterface`: `Upsert(*models.CertificateDistribution) error` (insert or update by `managed_certificate_id`), `GetByCertificateID(certID uuid.UUID) (*models.CertificateDistribution, error)`, `DeleteByCertificateID(certID uuid.UUID) error`; `NewCertificateDistributionRepository(db)`.

- [ ] **Step 1: failing model test** — `certificate_distribution_test.go`: construct a `CertificateDistribution`, assert `TableName()=="certificate_distributions"` and zero-value `Status`. (Flat columns — no JSONB — so a value/scan round-trip test isn't needed; a table-name + field test suffices.)

- [ ] **Step 2: Run** `go test ./internal/models/ -run TestCertificateDistribution -v` → FAIL.

- [ ] **Step 3: model** — mirror `internal/models/managed_certificate.go` conventions (uuid PK, timestamps). Flat columns, no JSONB.

- [ ] **Step 4: migration** `000042_add_certificate_distributions.up.sql`:
```sql
CREATE TABLE certificate_distributions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    managed_certificate_id UUID NOT NULL UNIQUE REFERENCES managed_certificates(id) ON DELETE CASCADE,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    last_pushed_fingerprint TEXT,
    message TEXT,
    last_synced_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_certificate_distributions_project ON certificate_distributions(project_id);
```
`.down.sql`: `DROP TABLE IF EXISTS certificate_distributions;`

- [ ] **Step 5: repository** — `Upsert` uses GORM `clause.OnConflict{Columns: [{Name:"managed_certificate_id"}], UpdateAll: true}` (import `gorm.io/gorm/clause`); `GetByCertificateID` (`Where("managed_certificate_id = ?", certID).First`); `DeleteByCertificateID`. Interface + `var _` assertion in `interfaces.go`.

- [ ] **Step 6: Run** `go test ./internal/models/ -run TestCertificateDistribution -v && go build ./...`; confirm migration number free.

---

## Task 2: cross-project cert list + TLS secret writer + source-read/fingerprint helpers

**Files:** Modify `internal/repository/managed_certificate_repository.go` (+interfaces.go), `internal/kubernetes/gvr.go`; Create `internal/cluster/tls_secret.go` (+test).

**Interfaces — Produces:**
- `ManagedCertificateRepository.ListByStatuses(statuses []models.ManagedCertStatus) ([]models.ManagedCertificate, error)` (cross-project: `Where("status IN ?", statuses).Find`).
- `kubernetes.CoreSecretGVR` = `schema.GroupVersionResource{Group:"", Version:"v1", Resource:"secrets"}` (constant in gvr.go, replacing the inline builds).
- `cluster.Client.CreateOrUpdateTLSSecret(ctx, projectID uuid.UUID, namespace, name string, crt, key []byte) error` — builds an `*unstructured.Unstructured` Secret with `type: kubernetes.io/tls`, `data: {tls.crt, tls.key}` (base64), `labels: {app.kubernetes.io/managed-by: fastgateway, fastgateway.dev/type: managed-cert}`, create-or-update-with-retry (reuse `updateUnstructuredWithRetry`).
- `cluster.CertFingerprint(crt []byte) string` — `fmt.Sprintf("%x", sha256.Sum256(crt))`.

- [ ] **Step 1: failing test** — `internal/cluster/tls_secret_test.go`: with a `dynamicfake` client (as in `controlplane_test.go`), `CreateOrUpdateTLSSecret(ctx, projectID, ns, "cert-x", crt, key)` then read it back and assert `type == "kubernetes.io/tls"` and `data["tls.crt"]` decodes to `crt`. Also `TestCertFingerprint` deterministic. (`CreateOrUpdateTLSSecret` is on `*cluster.Client` which needs a project client — if awkward to fake, extract the object-building into a pure `kubernetes.TLSSecretObject(name, ns, crt, key)` builder in `internal/kubernetes` and golden/unit-test THAT; the Client method just applies it. Prefer the pure-builder split so it's cleanly testable — mirror how `certmanager.go` builders are pure.)

- [ ] **Step 2: Run** → FAIL.

- [ ] **Step 3: implement** — add `kubernetes.TLSSecretObject(name, namespace string, crt, key []byte) *unstructured.Unstructured` (pure builder, `type: kubernetes.io/tls`, base64 data, managed labels); `cluster.Client.CreateOrUpdateTLSSecret` calls `getClient(projectID)` then applies `kubernetes.TLSSecretObject(...)` via create-or-update-with-retry (mirror `CreateOrUpdateSecret` body at `secret.go:151` but with the TLS object). Add `ListByStatuses` to the repo + interface. Add `CoreSecretGVR` to gvr.go. `CertFingerprint` in `internal/cluster` (or `internal/kubernetes`).

- [ ] **Step 4: Run** `go test ./internal/cluster/ ./internal/kubernetes/ -run 'TLSSecret|Fingerprint' -v && go build ./...`.

---

## Task 3: leader gate (Postgres advisory lock)

**Files:** Create `internal/leaderlock/leaderlock.go` (+test).

**Interfaces — Produces:**
- `type Gate interface { IsLeader() bool }` — what the controller consumes (fakeable in tests).
- `leaderlock.PostgresGate` implementing `Gate` + `Run(ctx context.Context)` that, on a pinned `*sql.Conn`, loops trying `SELECT pg_try_advisory_lock($1)`; sets an atomic `leader bool` on success; on connection loss clears it and retries; releases (`pg_advisory_unlock`) + closes the conn on ctx cancel.
- `leaderlock.New(sqlDB *sql.DB, key int64, retry time.Duration) *PostgresGate`.

- [ ] **Step 1: failing test** — pure-logic tests that don't need Postgres: a `Gate` fake + assert the controller (Task 4) gates on it; and a `PostgresGate` unit test for the atomic leader flag transitions driven by an injected "try-lock" func (make the SQL call a small injectable `tryLock func(ctx) (bool,error)` / `unlock func()` so the loop logic is unit-testable without a DB). Assert: acquires → IsLeader true; tryLock error → IsLeader false + retry; ctx cancel → unlock called + IsLeader false.

- [ ] **Step 2: Run** → FAIL.

- [ ] **Step 3: implement** — `PostgresGate` with an `atomic.Bool`; `Run` acquires a dedicated `*sql.Conn` (`sqlDB.Conn(ctx)`), runs the try-lock loop on a ticker, and on success holds the session lock (session-level advisory lock stays until unlock/conn close). Keep the actual SQL in a thin method the test overrides via the injectable func. Document the fixed `key` (a constant, e.g. `const CertDistributorLockKey int64 = 0x6667_63_01` — "fgc" distribution) used in main().

- [ ] **Step 4: Run** `go test ./internal/leaderlock/ -v && go build ./...`. (Real-Postgres acquisition is covered incidentally by running in-cluster; the unit tests cover the gate logic.)

---

## Task 4: the distribution reconcile controller (crux)

**Files:** Create `internal/certdist/certdist.go` (+test).

**Interfaces — Consumes (all interfaces, for testability):**
- `CertLister interface { ListByStatuses(statuses []models.ManagedCertStatus) ([]models.ManagedCertificate, error) }` (satisfied by the managed-cert repo).
- `DistRepo interface { Upsert(*models.CertificateDistribution) error; GetByCertificateID(uuid.UUID) (*models.CertificateDistribution, error) }`.
- `SourceReader interface { ReadLeafSecret(ctx, name string) (crt, key []byte, found bool, err error) }` — reads `cfg.ControlPlaneNamespace/<name>` from the control cluster (impl wraps `ControlPlaneClient.Get(ctx, CoreSecretGVR, name, true)` + base64 decode of `data["tls.crt"]/["tls.key"]`).
- `TenantWriter interface { CreateOrUpdateTLSSecret(ctx, projectID uuid.UUID, ns, name string, crt, key []byte) error; GetSecretData(ctx, projectID uuid.UUID, ns, name, key string) ([]byte, error) }` (satisfied by `*cluster.Client`).
- `CertUpdater interface { SetIssuedMeta(certID uuid.UUID, fingerprint string, notAfter *time.Time) error }` (persists fingerprint/notAfter on the ManagedCertificate — a small repo/service method).
- `leaderlock.Gate`, `*config.Config`.
**Produces:** `certdist.New(deps Deps) *Distributor` (panic-on-nil); `(*Distributor).Reconcile(ctx) error` — one full pass; `(*Distributor).reconcileOne(ctx, cert) error`.

- [ ] **Step 1: failing tests** — `certdist_test.go` with fakes:
  - `push_on_new`: cert `ready`, no distribution row, source secret present → `TenantWriter.CreateOrUpdateTLSSecret` called with the tenant ns/`cert-<id>` and the source bytes; distribution Upserted `status=synced` with the source fingerprint; cert `SetIssuedMeta` called with that fingerprint.
  - `noop_when_in_sync`: distribution fingerprint == source fingerprint AND tenant secret present (GetSecretData returns matching crt) → no `CreateOrUpdateTLSSecret`.
  - `renewal_repush`: source fingerprint changed vs distribution → re-push + distribution updated.
  - `self_heal_missing_tenant_secret`: distribution says synced with matching fingerprint but `GetSecretData` returns not-found → re-push.
  - `source_missing`: source secret not found (cert still issuing) → distribution `status=pending`, no push, no error (skip).
  - `not_leader`: `Gate.IsLeader()==false` → `Reconcile` returns immediately, nothing touched.
  Assert real behavior (writer called/not-called, distribution status, cert meta), not just no-error.

- [ ] **Step 2: Run** → FAIL.

- [ ] **Step 3: implement** — `Reconcile`: if `!gate.IsLeader()` return nil. `certs, _ := lister.ListByStatuses([ready, issuing])`. For each, `reconcileOne`: `crt, key, found, err := source.ReadLeafSecret(ctx, cert.Config.SecretName)`; if !found → Upsert distribution `pending` (msg "source not issued yet"), return nil. `fp := cluster.CertFingerprint(crt)`. Load distribution; decide push if `dist == nil || dist.LastPushedFingerprint != fp || dist.Status != synced || tenant secret absent/!= fp` (use `TenantWriter.GetSecretData(ctx, cert.ProjectID, tenantNs, cert.Config.SecretName, "tls.crt")` for the drift check — treat not-found/err as "needs push"). On push: `CreateOrUpdateTLSSecret(ctx, cert.ProjectID, tenantNs, cert.Config.SecretName, crt, key)`; on success Upsert distribution `synced/fp/now`, and `CertUpdater.SetIssuedMeta(cert.ID, fp, notAfterFromCert(crt))` (parse the leaf's NotAfter from the DER/PEM via `x509` — `parseNotAfter(crt []byte) *time.Time`). On push error Upsert distribution `error`+msg, continue to the next cert (isolate failures). Per-cert panics/errors must not abort the whole pass.
  Add `parseNotAfter` (decode PEM block → `x509.ParseCertificate` → `.NotAfter`). Tenant namespace = the Gateway namespace (default `fastgateway-system`; use the same constant domains use — confirm from `domainplan`/models; if per-domain, Phase 3b handles listener wiring — for 3a push to the domain/Gateway namespace the cert will be referenced from, default `fastgateway-system`).

- [ ] **Step 4: Run** `go test ./internal/certdist/ -v && go build ./...`.

---

## Task 5: run loop + main() wiring + LISTEN/NOTIFY + graceful shutdown

**Files:** Modify `cmd/server/main.go`, `internal/certdist/certdist.go` (add `Run`), `internal/services/managed_certificate_service.go` (emit NOTIFY).

**Interfaces — Produces:** `(*Distributor).Run(ctx context.Context, tick time.Duration, notifyCh <-chan struct{})` — loops on `ticker.C`, the `notifyCh`, and `ctx.Done()`; calls `Reconcile` on each wake; logs errors, never exits on a reconcile error.

- [ ] **Step 1:** In `cmd/server/main.go`, introduce a shutdown context: replace the bare `quit := make(chan os.Signal...)` block with `ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM); defer stop()` and block on `<-ctx.Done()` at the end. (Keep the existing `router.Run` goroutine; graceful HTTP shutdown is optional and out of scope — just thread `ctx` to the controller.)

- [ ] **Step 2:** Inside the existing `if services.IsRunningInCluster() { ... }` block (where `controlPlane` + `managedCertService` are built), construct: the dist repo, the leader gate (`leaderlock.New(sqlDB, leaderlock.CertDistributorLockKey, 10*time.Second)`; `sqlDB, _ := db.DB()`), the `SourceReader`/`TenantWriter`/`CertUpdater` adapters (thin structs over `controlPlane`, `k8sService`/`*cluster.Client`, and the managed-cert repo/service), and `distributor := certdist.New(certdist.Deps{...})`. Start two goroutines under `ctx`: `go gate.Run(ctx)` and `go distributor.Run(ctx, cfg.CertDistributorInterval, notifyCh)`. Add `cfg.CertDistributorInterval` (env `CERT_DISTRIBUTOR_INTERVAL`, default `60s`) + `cfg.CertDistributorResync` if a slow full pass is separate (or fold: the tick IS the periodic self-heal).

- [ ] **Step 3: LISTEN/NOTIFY nudge** — add a small `internal/certdist` listener: using `lib/pq`'s `pq.NewListener` (lib/pq is already the Postgres driver — confirm import) on channel `cert_distribution`, forward notifications to `notifyCh`. In `ManagedCertificateService`, after a successful approve-issue (`OnApproved`) and after `Resync`, emit `NOTIFY cert_distribution` (via `db.Exec("NOTIFY cert_distribution")` or `pq.Listener` counterpart). If `pq.NewListener` wiring is heavy, a DB-poll tick alone is acceptable for 3a (NOTIFY is a latency optimization) — implement the ticker path first; add NOTIFY only if straightforward. Document whichever you ship.

- [ ] **Step 4: Run** `go build ./...`, `go vet ./cmd/... ./internal/...`, and boot-smoke: `go test ./cmd/server/ -run TestRouteSpecParity -count=1` still passes; confirm main() compiles with the new ctx + goroutines. Do NOT commit.

---

## Task 6: distribution status endpoints + service + resync

**Files:** Modify `internal/services/managed_certificate_service.go`, `internal/handlers/managed_certificate_handler.go`, `internal/handlers/service_interfaces.go`, `cmd/server/main.go`.

**Interfaces — Produces:** `ManagedCertificateService.DistributionStatus(certID uuid.UUID) (*models.CertificateDistribution, error)` (read the distribution row via the dist repo — add the dist repo as a dep to the service: **required-dep ripple**, update its constructor + tests + main.go construction) and `Resync(certID uuid.UUID) error` (emit `NOTIFY cert_distribution`, or set the distribution `status=pending` to force a re-push next tick). Handler `Distribution` + `Resync` methods; routes `GET /projects/:projectId/certificates/:certificateId/distribution` (cert.view) + `POST .../resync` (cert.edit) inside the existing nil-guarded `/:projectId/certificates` group; both do the cross-project 404 check (mirror Get/Delete/Status).

- [ ] **Step 1: failing handler+service tests** — `DistributionStatus` returns the row (project-scoped 404 on mismatch); `Resync` triggers a nudge and returns 202/200. Use mocks.

- [ ] **Step 2: Run** → FAIL. `make mocks`.

- [ ] **Step 3: implement** — service methods (+ the dist-repo dep ripple: add `DistRepo` to `ManagedCertificateServiceDeps` + nil check + update all construction sites: the service test helper AND `cmd/server/main.go`), handler methods (DTO carries fingerprint/status/lastSyncedAt — no key material), routes.

- [ ] **Step 4: Run** `go test ./internal/services/ ./internal/handlers/ -run 'ManagedCertificate|Distribution' -v && go build ./...`.

---

## Task 7: fingerprint/notAfter persistence + OpenAPI + full gate

**Files:** Modify `internal/services/managed_certificate_service.go` (`Status` persists `NotAfter`; add `SetIssuedMeta`), `docs/openapi/…`, `cmd/server/router_test.go`.

- [ ] **Step 1:** Fix `Status()` to persist `cert.NotAfter` (Phase 2 set it only on the response, never the DB column) and add `SetIssuedMeta(certID, fingerprint, notAfter)` used by the controller (Task 4). Add a test asserting `NotAfter` is written to the row on Ready.
- [ ] **Step 2:** Add the two Phase-3 distribution OpenAPI operations (`GET .../distribution`, `POST .../resync`) to `docs/openapi/`; ensure `router_test.go`'s parity `RouterDeps` already registers the cert group (it does from Phase 2 — the new routes are under the same nil-guard, so `ManagedCertificateHandler` non-nil covers them).
- [ ] **Step 3: full gate:** `make openapi && make openapi-check` (exit 0); `go test ./cmd/server/ -run TestRouteSpecParity -count=1` PASS; `go build ./...`; `go test ./... -count=1` all pass; **`gofmt -l .` (excl docs/superpowers) clean**. `make mocks-check` drift is the benign no-commit artifact.
- [ ] **Step 4:** grep-confirm no secret/key material in distribution DTOs or logs.

---

## Self-Review

**1. Spec coverage:** distribution controller (leader-elected, syncs issued Secret to the tenant cluster, renewal + self-heal) → Tasks 3,4,5; `CertificateDistribution` status → Tasks 1,6; live fingerprint/notAfter → Tasks 4,7; distribution/resync endpoints → Task 6; LISTEN/NOTIFY nudge → Task 5. Domain/client attachment intentionally OUT (Phase 3b / deferred).
**2. Placeholder scan:** mechanical mirrors point at committed Phase 2 files (real code). The one soft spot — the tenant namespace a cert is pushed to — is pinned to the Gateway namespace default (`fastgateway-system`) for 3a, with per-listener wiring deferred to 3b; Task 4 states this explicitly rather than leaving it vague.
**3. Type consistency:** `Config.SecretName="cert-<id>"` (Phase 2) is the source+tenant Secret name throughout; `CertFingerprint`/`parseNotAfter` defined in Task 2/4 and consumed in Task 4; `ListByStatuses` (Task 2) consumed by Task 4; `DistRepo` dep-ripple (Task 6) mirrors Phase 1/2's IssuerRepo/ManagedCertRepo ripples — executor MUST update all construction sites.

**Design rulings baked in (flag at handoff):** DB-poll reconcile over Secret informer (unlabeled secrets + no informer infra); Postgres advisory-lock leader (no new dep); client-cert attachment deferred (no consumer model); domain attachment is Phase 3b. Run tree-wide gofmt before commit.
