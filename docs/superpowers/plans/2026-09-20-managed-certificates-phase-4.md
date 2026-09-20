# Managed Certificates — Phase 4 Implementation Plan (Visibility / bird's-eye views)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enrich the project certificate list and add an owner-only fleet view, both aggregating existing status/expiry/fingerprint/distribution/domain-reference data — no new model, no migration.

**Architecture:** Read/aggregate only. A shared enrichment builder joins each `ManagedCertificate` with its `CertificateDistribution` (sync status), its referencing `Domain`s, and its issuer's name/type. To avoid an N+1 across the fleet, joins use batch repo methods keyed by a slice of cert IDs (one query per relation, then map in memory). Two endpoints consume it: the project view (`certificate.view`, filterable) enriches the existing `GET /projects/:projectId/certificates`; the fleet view (owner-only, new `GET /certificates`) lists across all projects with the same enrichment plus `projectId`/issuer filters.

**Tech Stack:** Go 1.25, Gin, GORM, mockery v3 (`make mocks`), testify, `//go:embed` OpenAPI bundle (`make openapi`/`make openapi-check`).

**Spec:** `docs/superpowers/specs/2026-09-19-managed-certificates-design.md` §7 (Visibility) + §8 (project view = `certificate.view`; fleet = owner-only). API contract: `docs/superpowers/plans/2026-09-19-managed-certificates-api.md` Phase 4.

## Global Constraints

- No new DB model / migration — Phase 4 reads and aggregates existing data.
- No secret/key material in any response DTO or log (the existing `managedCertificateResponse` already documents this exclusion; new enriched fields must hold only metadata — domain hostnames/ids, distribution status/timestamp/fingerprint hash, issuer name/type).
- **Referencing clients has no data source** (client-cert attach is deferred, no `Client.ManagedCertificateID` exists) — the enriched DTO omits clients entirely (do NOT invent a field). The spec's "referencing domains/clients" is satisfied by domains alone in this phase; note it.
- **"Renewal state"** has no data model (cert-manager owns renewal; we don't store it) — expose `notAfter` (already in the DTO) as the expiry signal and let clients derive urgency. Do NOT invent a renewal-state field.
- Avoid N+1: enrichment joins use batch repo methods (`... IN (?)`), one query per relation for the whole page, never one-query-per-cert.
- House patterns: `Deps`/panic-on-nil where applicable; repo methods on the interface + `var _` assertion; owner-only global route via a new `protected.Group("/certificates")` + `deps.AuthMiddleware.RequireRole("owner")`, nil-guarded like the existing cert handler blocks; project endpoints add a `PermissionChecker.CanViewCertificates` gate delegating to `HasPermission(projectID, user, PermCertificateView)`; tree-wide `gofmt -l .` (excl `docs/superpowers`) before finishing; edit OpenAPI **source** under `docs/openapi/` then `make openapi` + `make openapi-check` + `TestRouteSpecParity`.
- Execution: subagents never commit; the human commits explicitly; commit exactly the enumerated feature files via explicit `git add` (never `-A`).

## File Structure

- `internal/repository/managed_certificate_repository.go` + `interfaces.go` — filtered project list + cross-project fleet list (Task 1).
- `internal/repository/certificate_distribution_repository.go`, `internal/repository/domain_repository.go` + `interfaces.go` — batch-by-cert-ids joins (Task 2).
- `internal/services/managed_certificate_view.go` (new) — the enrichment builder + enriched view types (Task 3).
- `internal/services/managed_certificate_service.go` — `ListProjectCertificatesEnriched` / `ListFleetCertificates` service methods (Tasks 4/5).
- `internal/middleware/permissions.go` — `CanViewCertificates` (Task 4).
- `internal/handlers/managed_certificate_handler.go` — enrich `List`; add fleet handler (Tasks 4/5).
- `cmd/server/main.go` — owner-only `/certificates` group + fleet route (Task 5).
- `docs/openapi/...` — enrich project-list op + new fleet op (Task 6).

## Shared filter type (used by Tasks 1/4/5)

Define in the repository package (Task 1):
```go
// CertificateListFilter carries optional list filters. Zero values mean "no filter".
type CertificateListFilter struct {
	Status       string      // exact ManagedCertStatus match
	IssuerID     *uuid.UUID  // exact issuer
	Usage        string      // "server" | "client"
	ExpiresBefore *time.Time // not_after <= t
	ProjectID    *uuid.UUID  // fleet only; project view fixes this from the path
}
```

---

## Task 1: Filtered project list + cross-project fleet list (repo)

**Files:** Modify `internal/repository/managed_certificate_repository.go`, `internal/repository/interfaces.go`; Test `internal/repository/managed_certificate_repository_test.go`; regenerate `internal/mocks/mock_repositories.go`.

**Interfaces — Produces:**
- `ListByProjectFiltered(projectID uuid.UUID, page, limit int, f CertificateListFilter) ([]models.ManagedCertificate, int64, error)`
- `ListFleet(page, limit int, f CertificateListFilter) ([]models.ManagedCertificate, int64, error)` (cross-project; `f.ProjectID` optional)
Define `CertificateListFilter` (above) in this package.

- [ ] **Step 1: failing tests** (Postgres harness — mirror `managed_certificate_repository_test.go`'s `requirePostgres`/`seedProject`/`seedIssuer`/`seedManagedCertificate`): seed certs across two projects with differing status/usage/issuer/notAfter, assert `ListByProjectFiltered` filters correctly per field and paginates; assert `ListFleet` returns across projects and honors `ProjectID`/status/usage/issuer/`ExpiresBefore` filters. (Tests SKIP cleanly without a DB, same as existing repo tests.)
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: implement.** Build the GORM query the way `DomainRepository.ListByProjectID` does (chain optional `Where`s). For `ListByProjectFiltered`: base `Where("project_id = ?", projectID)`, then optional `status`, `issuer_id`, `usage`, and `not_after <= ?` for `ExpiresBefore`; `Count` then `Order("created_at DESC").Offset().Limit()`. For `ListFleet`: same optional filters, no forced project scope, optional `Where("project_id = ?", *f.ProjectID)`. Add both to the interface. `make mocks`.
- [ ] **Step 4: Run** `go test ./internal/repository/ -run 'ManagedCertificate' -v && go build ./...` → PASS (or clean SKIP without DB).

---

## Task 2: Batch enrichment joins (repo)

**Files:** Modify `internal/repository/certificate_distribution_repository.go`, `internal/repository/domain_repository.go`, `internal/repository/interfaces.go`; Tests in the respective `_test.go`; regenerate mocks.

**Interfaces — Produces:**
- `CertificateDistributionRepositoryInterface.ListByCertificateIDs(certIDs []uuid.UUID) ([]models.CertificateDistribution, error)` — all rows whose `managed_certificate_id IN (?)` (empty input → empty slice, no query).
- `DomainRepositoryInterface.ListByManagedCertificateIDs(certIDs []uuid.UUID) ([]models.Domain, error)` — all domains whose `managed_certificate_id IN (?)` (empty input → empty slice).

- [ ] **Step 1: failing tests** (Postgres harness): seed 2 certs, give one a distribution row + a referencing domain, the other neither; assert `ListByCertificateIDs([both])` returns the one dist row, `ListByManagedCertificateIDs([both])` returns the one domain; assert empty input returns empty + nil.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: implement** both as `if len(certIDs)==0 { return nil, nil }` then `Where("managed_certificate_id IN ?", certIDs).Find(...)`. Add to interfaces. `make mocks`.
- [ ] **Step 4: Run** `go test ./internal/repository/ -run 'Distribution|Domain' -v && go build ./...` → PASS/SKIP.

---

## Task 3: Enrichment builder + enriched view types (service)

**Files:** Create `internal/services/managed_certificate_view.go` (+ `managed_certificate_view_test.go`).

**Interfaces — Produces:** a pure(ish) builder that, given a page of certs, assembles enriched views using the batch repos + issuer map:
```go
type EnrichedCertificate struct {
	Certificate  models.ManagedCertificate
	IssuerName   string
	IssuerType   string
	Distribution *models.CertificateDistribution // nil if never distributed
	Domains      []models.Domain                 // referencing domains (may be empty)
}

// buildEnrichedCertificates joins certs with distributions, referencing domains,
// and issuer name/type using ONE batch query per relation (no N+1).
func buildEnrichedCertificates(certs []models.ManagedCertificate, dists []models.CertificateDistribution, domains []models.Domain, issuers []models.CertificateIssuer) []EnrichedCertificate
```
Keying: map `dists` by `ManagedCertificateID` (1:1), group `domains` by `ManagedCertificateID`, map `issuers` by `ID`.

- [ ] **Step 1: failing unit test** — feed 3 certs (one with dist+2 domains, one with dist only, one with neither), an issuer list, and assert each `EnrichedCertificate` has the right Distribution (nil where absent), Domains (grouped correctly, empty slice where none), and IssuerName/Type resolved (empty when issuer missing from the list — defensive). Pure in-memory, no DB/mocks.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: implement** the maps + assembly.
- [ ] **Step 4: Run** `go test ./internal/services/ -run 'EnrichedCert|buildEnriched' -v && go build ./...` → PASS.

---

## Task 4: Project enriched view endpoint (`certificate.view` + filters)

**Files:** Modify `internal/middleware/permissions.go` (add `CanViewCertificates`), `internal/services/managed_certificate_service.go` (`ListProjectCertificatesEnriched`), `internal/handlers/managed_certificate_handler.go` (enrich `List` + enriched DTO), tests.

**Design rulings (bind this task):**
- **Permission behavior change (flagged):** the current `List` is gated only by the group-level `RequireProjectAccess()`. Per spec §8 / the API contract, the project cert view is `certificate.view`. Add `CanViewCertificates(projectID, user)` to `PermissionChecker` (mirror `CanCreateCertificates`: owner OR project-admin OR `HasPermission(projectID, user, models.PermCertificateView)`) and enforce it at the top of the `List` handler (403 otherwise). `PresetViewer` already includes `PermCertificateView`, so ordinary viewers keep access; a project member in a preset lacking cert.view loses list access — the intended RBAC. Do NOT retroactively change `Get`/`Status`/`Distribution` gating (out of scope).
- Enrich in place (same path `GET /projects/:projectId/certificates`); add filters `status`, `issuerId`, `usage`, `expiresBefore` (RFC3339). Keep existing pagination + response envelope (`{data, pagination}`).

**Interfaces — Produces:** `ManagedCertificateService.ListProjectCertificatesEnriched(projectID uuid.UUID, page, limit int, f repository.CertificateListFilter) ([]services.EnrichedCertificate, int64, error)` — calls `ListByProjectFiltered`, collects cert IDs, calls the two batch repos + issuer `List()`, returns `buildEnrichedCertificates(...)` + total.

- [ ] **Step 1: failing service test** (mocked repos): given a filtered project list + batch dist/domain results + issuer list, returns enriched views + total; assert the batch methods are called with the page's cert IDs (proves no N+1). Add `DistRepo`/`DomainRepo`/`IssuerRepo` deps to the service if not already present — **the service already has `DistRepo` and `Repo`; add `DomainRepo` (added in Phase 3b to this service already?) and `IssuerRepo` (already present)**. VERIFY which deps exist on `ManagedCertificateServiceDeps` before adding; only add what's missing, and if adding, update all construction sites + `make mocks` (dep ripple — the service already took `DomainRepo` in Phase 3b, so likely only nothing-new is needed).
- [ ] **Step 2: failing handler test:** `List` with `certificate.view` → 200 with enriched `{data:[{...distribution, domains, issuerName, issuerType}], pagination}`; without cert.view → 403; filters parsed (`expiresBefore` bad value → 400). Add the enriched DTO (extend/replace `managancedCertificateResponse` assembly with distribution/domains/issuer fields; no secret material — domains carry id+hostname only).
- [ ] **Step 3: Run** → FAIL. `make mocks`.
- [ ] **Step 4: implement** `CanViewCertificates`, the service method, the enriched DTO + handler wiring + filter parsing.
- [ ] **Step 5: Run** `go test ./internal/services/ ./internal/handlers/ ./internal/middleware/ -run 'ManagedCertificate|Certificate|ViewCertificates' -v && go build ./...` → PASS.

---

## Task 5: Owner-only fleet view endpoint

**Files:** Modify `internal/services/managed_certificate_service.go` (`ListFleetCertificates`), `internal/handlers/managed_certificate_handler.go` (fleet handler), `internal/handlers/service_interfaces.go` (if the handler iface needs the method), `cmd/server/main.go` (owner group + route), tests.

**Design rulings (bind this task):**
- New owner-only group: `fleetCerts := protected.Group("/certificates"); fleetCerts.Use(deps.AuthMiddleware.RequireRole("owner"))` with `fleetCerts.GET("", deps.ManagedCertificateHandler.ListFleet)`. This is a SIBLING to the existing `protected.Group("/certificates/issuers")` block (gin routes `/certificates` and `/certificates/issuers` distinctly — do not nest, do not re-register issuers). Nil-guard the whole block with `if deps.ManagedCertificateHandler != nil { ... }` like the existing cert blocks (the handler only exists in-cluster).
- Fleet DTO = the SAME enriched DTO as Task 4 (it already carries `projectId`). Filters: `status`, `expiresBefore`, `issuerId`, `projectId`, `usage`, plus pagination.

**Interfaces — Produces:** `ManagedCertificateService.ListFleetCertificates(page, limit int, f repository.CertificateListFilter) ([]services.EnrichedCertificate, int64, error)` — calls `ListFleet`, then the same batch enrichment as Task 4 (extract a shared private helper `enrich(certs) ([]EnrichedCertificate, error)` so project + fleet share it).

- [ ] **Step 1: failing service test** (mocked repos): fleet list across projects + batch enrichment → enriched views + total; `projectId` filter passes through.
- [ ] **Step 2: failing handler test:** `ListFleet` returns 200 with `{data, pagination}` enriched across projects; the route is owner-gated (a non-owner is 403 — exercised via the middleware in a routed test, or assert the handler is registered under the owner group). Filter parsing incl. `projectId`.
- [ ] **Step 3: Run** → FAIL. `make mocks`.
- [ ] **Step 4: implement** the service method (shared `enrich` helper), the handler, the owner-only route in main.go.
- [ ] **Step 5: Run** `go test ./internal/services/ ./internal/handlers/ ./cmd/server/ -run 'Fleet|ManagedCertificate|Parity' -v && go build ./...` → PASS.

---

## Task 6: OpenAPI source + full gate

**Files:** Modify OpenAPI source under `docs/openapi/` (the project cert-list op — add the new query filters + enriched response schema; and a NEW `GET /certificates` fleet op), rebundle `cmd/server/openapi.yaml` via `make openapi`.

- [ ] **Step 1:** update the existing `GET /projects/{projectId}/certificates` operation in the source (add `issuerId`/`usage`/`expiresBefore` query params + reflect the enriched response schema: each item gains `distribution`, `domains`, `issuerName`, `issuerType`), and add a new owner-only `GET /certificates` fleet operation (query params `status`/`expiresBefore`/`issuerId`/`projectId`/`usage`/pagination; same enriched item schema; security = owner). Register the new path in `docs/openapi/openapi.yaml`. Mirror the sibling cert ops.
- [ ] **Step 2:** `make openapi` + `make openapi-check` (exit 0).
- [ ] **Step 3: full gate — ALL pass:** `go build ./...`; `go vet ./cmd/... ./internal/...`; `gofmt -l .` (excl `docs/superpowers`) empty; `go test ./cmd/server/ -run TestRouteSpecParity -count=1` PASS (proves the new fleet route has a matching op); `go test ./... -count=1` all pass; `make mocks` idempotent.
- [ ] **Step 4: secret-leak grep** — confirm the enriched project + fleet responses expose only metadata (no key/DNS-cred material; domains carry id/hostname only).

---

## Self-Review

**1. Spec coverage:** project cert view (enriched, filterable, `certificate.view`) → Tasks 1/3/4; fleet view (owner-only, cross-project, +projectId/issuer) → Tasks 1/3/5; sync status + referencing domains + issuer name/type → Tasks 2/3; filters incl. expiry → Task 1; OpenAPI/parity → Task 6. Referencing CLIENTS explicitly omitted (no data source — deferred client-cert attach); "renewal state" mapped to existing `notAfter` (no renewal model). Both noted in Global Constraints.

**2. Placeholder scan:** every code step names concrete methods/queries. The one verify-first spot is Task 4 Step 1 (which service deps already exist — `DomainRepo` was added in Phase 3b, `DistRepo`/`IssuerRepo`/`Repo` exist; add only what's missing).

**3. Type consistency:** `CertificateListFilter` (Task 1) is consumed by the service methods (Tasks 4/5); `EnrichedCertificate` + `buildEnrichedCertificates` (Task 3) consumed by both service methods; batch methods `ListByCertificateIDs`/`ListByManagedCertificateIDs` (Task 2) feed the enrich helper; the enriched DTO (Task 4) is reused by the fleet handler (Task 5). No N+1: enrichment is one query per relation per page.

**Design rulings baked in:** no new model/migration; batch joins avoid fleet N+1; project view gated by `certificate.view` (behavior change flagged, viewers unaffected); fleet view = new owner-only `/certificates` sibling group, nil-guarded; clients omitted; renewal-state = `notAfter`. Run tree-wide gofmt + OpenAPI parity before commit.
