# DNS Records List & Management Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **Note:** Tasks 1–2 (backend) were implemented in a first pass before this plan existed and are uncommitted in the `backend-v2-dns-list` worktree. Treat this plan as the source of truth: reconcile that code against each task (it should match), then commit per the task's commit step. Tasks 3–6 (frontend) are not started.

**Goal:** Add a project-wide DNS records list with edit/delete/refresh, aggregating the existing per-domain records and reusing the per-domain API for mutations.

**Architecture:** A new read-only `GET /projects/:projectId/dns-records` endpoint joins each project's `domain_dns_records` to `domains` (+ hosted zones) and returns rows enriched with the domain hostname and zone name. A new project-scoped frontend page lists them; edit/delete/refresh call the existing per-domain endpoints with each row's `domainId`. The four editable DNS fields are extracted into a shared form component reused by the domain settings page and the list page's edit modal.

**Tech Stack:** Go / Gin / GORM / client-go / testify (backend-v2); Next.js 16 App Router / TypeScript / Tailwind / Jest + @testing-library (frontend-v2).

**Spec:** `docs/superpowers/specs/2026-10-06-dns-records-list-management-design.md`

## Global Constraints

- Repos: `backend-v2` and `frontend-v2`. Isolated worktrees off `main`, local commits, confirm before push.
- Go module path: `github.com/fastgateway-dev/backend-v2`.
- The list endpoint is project-scoped: `GET /projects/:projectId/dns-records`, under `RequireProjectAccess`, gated by `CanManageDomains`, and nil-guarded by `deps.DNSRecordHandler` (in-cluster only) — identical to the per-domain `dns-record` routes.
- `List` is a pure read: it does NOT reconcile against the provider; Status is the last persisted value.
- No standalone records (records stay tied to domains); edit covers only `hostedZoneId`, `recordType` (`auto/A/AAAA/CNAME`), `ttl`, `proxied` — never name or target.
- Backend mocks are generated: run `make mocks` after interface changes; `make mocks-check` must pass. OpenAPI: `make openapi` then `make openapi-check`.
- Backend repository tests are Postgres integration tests gated by `INTEGRATION_DB_URL` (they `t.Skip` without it). To run them: `docker run -d --name fgw-pg -e POSTGRES_USER=fastgateway -e POSTGRES_PASSWORD=fastgateway -e POSTGRES_DB=fastgateway -p 55432:5432 postgres:16-alpine`, then `DATABASE_PORT=55432 go run cmd/migrate/main.go up`, then `INTEGRATION_DB_URL="postgres://fastgateway:fastgateway@localhost:55432/fastgateway?sslmode=disable"`.

## Review Focus

- **Record whose hosted-zone row is gone** → LEFT JOIN must still list it with an empty `zoneName`, not drop it. → Task 1 repo test (`lists a record whose zone row is missing`).
- **List requested without `CanManageDomains`** → 403, not served. → Task 2 handler test (`403 without manage permission`).
- **Empty project (no records)** → returns `[]`, never `null`/500; frontend renders an empty state. → Task 2 handler test (`empty project returns empty array`) + Task 5 page test.
- **List fetch fails in the UI** → the page shows an error state and does not crash. → Task 5 page test (`shows error when list fails`).
- **Delete/refresh a row** → the list re-fetches and reflects the change. → Task 5 page test (`delete removes the row`, `refresh re-fetches`).

---

## File Structure

| File | Responsibility |
|------|----------------|
| `backend-v2/internal/models/domain_dns_record.go` | `DNSRecordListItem` read projection. |
| `backend-v2/internal/repository/domain_dns_record_repository.go` | `ListByProjectID` join query. |
| `backend-v2/internal/repository/interfaces.go` | add `ListByProjectID` to the interface. |
| `backend-v2/internal/mocks/*` | regenerated mocks. |
| `backend-v2/internal/services/dns_record_service.go` | `List` service method. |
| `backend-v2/internal/handlers/dns_record_handler.go` | `List` handler + list response type. |
| `backend-v2/internal/handlers/service_interfaces.go` | add `List` to `DNSRecordServiceInterface`. |
| `backend-v2/cmd/server/main.go` | register `GET /projects/:projectId/dns-records`. |
| `backend-v2/api/openapi.yaml` (+ schemas) | document the endpoint + schema. |
| `frontend-v2/src/lib/api/dns-records.ts` | add `list(projectId)`. |
| `frontend-v2/src/types/index.ts` | `DomainDNSRecordListItem` type. |
| `frontend-v2/src/components/features/dns-record-fields.tsx` | shared controlled DNS fields. |
| `frontend-v2/src/app/projects/[projectId]/dns-records/page.tsx` | the list page + edit modal. |
| `frontend-v2/src/components/features/sidebar.tsx` | "DNS Records" nav entry. |

---

## Task 1: Backend — data layer (`DNSRecordListItem`, repo `ListByProjectID`, service `List`)

**Repo:** `backend-v2` worktree.

**Files:**
- Modify: `internal/models/domain_dns_record.go`
- Modify: `internal/repository/domain_dns_record_repository.go`, `internal/repository/interfaces.go`
- Regenerate: `internal/mocks/mock_repositories.go` (and others) via `make mocks`
- Modify: `internal/services/dns_record_service.go`
- Test: `internal/repository/domain_dns_record_repository_test.go`, `internal/services/dns_record_service_test.go`

**Interfaces:**
- Produces: `models.DNSRecordListItem{ DomainDNSRecord; DomainHostname string; ZoneName string }`; `DomainDNSRecordRepositoryInterface.ListByProjectID(projectID uuid.UUID) ([]models.DNSRecordListItem, error)`; `(*DNSRecordService).List(projectID uuid.UUID) ([]models.DNSRecordListItem, error)`.

- [ ] **Step 1: Add the read projection** — after `func (DomainDNSRecord) TableName()` in `internal/models/domain_dns_record.go`:

```go
// DNSRecordListItem is a read projection for the project-wide DNS records list:
// a domain's managed DNS record joined with its domain hostname (the record's
// name) and the name of the hosted zone it lives in. It is not a table.
type DNSRecordListItem struct {
	DomainDNSRecord
	DomainHostname string `json:"domainHostname"`
	ZoneName       string `json:"zoneName"`
}
```

- [ ] **Step 2: Add `ListByProjectID` to the interface** — in `internal/repository/interfaces.go`, inside `DomainDNSRecordRepositoryInterface`, after `GetByDomainID`:

```go
	ListByProjectID(projectID uuid.UUID) ([]models.DNSRecordListItem, error)
```

- [ ] **Step 3: Write the failing repo test** — add to `internal/repository/domain_dns_record_repository_test.go`, extending the import block with nothing new (uuid/models/assert/require already present):

```go
func TestDomainDNSRecordRepository_ListByProjectID(t *testing.T) {
	db := requirePostgres(t)
	repo := repository.NewDomainDNSRecordRepository(db)

	project1, domain1a, _, user1 := seedProject(t, db)
	domain1b := uuid.New()
	require.NoError(t, db.Exec(`
		INSERT INTO domains (id, project_id, name, hostname, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, NOW(), NOW())`,
		domain1b, project1, "d1b-"+domain1b.String(), "b.example.com", user1).Error)

	project2, domain2, _, user2 := seedProject(t, db)

	cred1 := seedDNSProviderCredential(t, db, user1, "cred-"+uuid.NewString())
	zoneExample := seedDNSHostedZone(t, db, user1, cred1, "example.com")
	cred2 := seedDNSProviderCredential(t, db, user2, "cred-"+uuid.NewString())
	zoneOther := seedDNSHostedZone(t, db, user2, cred2, "other.com")

	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM domain_dns_records WHERE domain_id IN (?, ?, ?)`, domain1a, domain1b, domain2).Error
		_ = db.Exec(`DELETE FROM dns_hosted_zones WHERE id IN (?, ?)`, zoneExample, zoneOther).Error
		_ = db.Exec(`DELETE FROM dns_provider_credentials WHERE id IN (?, ?)`, cred1, cred2).Error
		_ = db.Exec(`DELETE FROM domains WHERE id = ?`, domain1b).Error
	})

	require.NoError(t, repo.Create(&models.DomainDNSRecord{
		DomainID: domain1a, HostedZoneID: zoneExample, RecordType: models.DNSRecordTypeAuto,
		ResolvedTarget: "192.0.2.1", Status: models.DNSRecordStatusReady, CreatedBy: user1,
	}))
	require.NoError(t, repo.Create(&models.DomainDNSRecord{
		DomainID: domain1b, HostedZoneID: zoneExample, RecordType: models.DNSRecordTypeCNAME,
		ResolvedTarget: "gw.example.com", Status: models.DNSRecordStatusPending, CreatedBy: user1,
	}))
	require.NoError(t, repo.Create(&models.DomainDNSRecord{
		DomainID: domain2, HostedZoneID: zoneOther, RecordType: models.DNSRecordTypeA,
		ResolvedTarget: "198.51.100.9", Status: models.DNSRecordStatusReady, CreatedBy: user2,
	}))

	items, err := repo.ListByProjectID(project1)
	require.NoError(t, err)
	require.Len(t, items, 2)
	byHost := map[string]models.DNSRecordListItem{}
	for _, it := range items {
		byHost[it.DomainHostname] = it
		assert.NotEqual(t, domain2, it.DomainID, "project 2's record must not appear")
	}
	b, ok := byHost["b.example.com"]
	require.True(t, ok)
	assert.Equal(t, domain1b, b.DomainID)
	assert.Equal(t, "example.com", b.ZoneName)
	assert.Equal(t, models.DNSRecordTypeCNAME, b.RecordType)
	assert.Equal(t, "gw.example.com", b.ResolvedTarget)
	assert.Equal(t, models.DNSRecordStatusPending, b.Status)

	// Review Focus: a record whose hosted-zone row is gone still lists (LEFT JOIN), empty ZoneName.
	require.NoError(t, db.Exec(`DELETE FROM dns_hosted_zones WHERE id = ?`, zoneExample).Error)
	items, err = repo.ListByProjectID(project1)
	require.NoError(t, err)
	require.Len(t, items, 2)
	for _, it := range items {
		assert.Equal(t, "", it.ZoneName, "zone name empty once the zone row is gone")
	}

	items2, err := repo.ListByProjectID(project2)
	require.NoError(t, err)
	require.Len(t, items2, 1)
	assert.Equal(t, domain2, items2[0].DomainID)
	assert.Equal(t, "other.com", items2[0].ZoneName)
}
```

- [ ] **Step 4: Run it; expect FAIL to compile** — `ListByProjectID` undefined on the concrete repo.
  `INTEGRATION_DB_URL=... go test ./internal/repository/ -run TestDomainDNSRecordRepository_ListByProjectID` → build error.

- [ ] **Step 5: Implement the repo method** — in `internal/repository/domain_dns_record_repository.go`, before `Update`:

```go
// ListByProjectID returns every managed DNS record whose domain belongs to the
// given project, each joined with its domain hostname (the record name) and the
// name of the hosted zone it lives in, ordered by hostname. A record whose
// hosted zone row is missing still appears (LEFT JOIN) with an empty ZoneName.
func (r *DomainDNSRecordRepository) ListByProjectID(projectID uuid.UUID) ([]models.DNSRecordListItem, error) {
	var items []models.DNSRecordListItem
	err := r.db.
		Table("domain_dns_records AS rec").
		Select("rec.*, d.hostname AS domain_hostname, z.name AS zone_name").
		Joins("JOIN domains d ON d.id = rec.domain_id").
		Joins("LEFT JOIN dns_hosted_zones z ON z.id = rec.hosted_zone_id").
		Where("d.project_id = ?", projectID).
		Order("d.hostname ASC").
		Scan(&items).Error
	if err != nil {
		return nil, err
	}
	return items, nil
}
```

- [ ] **Step 6: Regenerate mocks** — `make mocks` (adds `ListByProjectID` to `MockDomainDNSRecordRepository`). Hand-written service-test fakes also need it:
  - `internal/services/dns_record_service_test.go` `fakeRecRepo`: add fields `listResult []models.DNSRecordListItem`, `listErr error`, `listCalled uuid.UUID`, and
    ```go
    func (f *fakeRecRepo) ListByProjectID(projectID uuid.UUID) ([]models.DNSRecordListItem, error) {
    	f.listCalled = projectID
    	return f.listResult, f.listErr
    }
    ```
  - `internal/services/dns_hosted_zone_service_test.go` `fakeZoneRecordRepo`: add
    ```go
    func (f *fakeZoneRecordRepo) ListByProjectID(projectID uuid.UUID) ([]models.DNSRecordListItem, error) { return nil, nil }
    ```

- [ ] **Step 7: Run repo test; expect PASS** — `INTEGRATION_DB_URL=... go test ./internal/repository/ -run TestDomainDNSRecordRepository_ListByProjectID -v` → PASS.

- [ ] **Step 8: Add the service method** — in `internal/services/dns_record_service.go`, before `Get`:

```go
// List returns every managed DNS record whose domain belongs to projectID, each
// enriched with its domain hostname (the record name) and hosted-zone name,
// ordered by hostname. It is a pure read: unlike Get/Refresh it does not
// reconcile against the provider, so Status reflects the last persisted value
// (the per-domain Get/Refresh path owns reconciliation). The project scoping
// lives in the repository query, so no per-domain ownership check is needed.
func (s *DNSRecordService) List(projectID uuid.UUID) ([]models.DNSRecordListItem, error) {
	return s.repo.ListByProjectID(projectID)
}
```

- [ ] **Step 9: Write + run the service tests** — add to `internal/services/dns_record_service_test.go` (add `"github.com/stretchr/testify/assert"` to imports):

```go
func TestList_PassesProjectAndReturnsItems(t *testing.T) {
	h := newRecHarness(t, "app.example.com", "example.com", "203.0.113.5", "faketest", &recDNSClient{})
	h.repo.listResult = []models.DNSRecordListItem{{
		DomainDNSRecord: models.DomainDNSRecord{DomainID: h.domainID, RecordType: models.DNSRecordTypeA, Status: models.DNSRecordStatusReady},
		DomainHostname:  "app.example.com",
		ZoneName:        "example.com",
	}}
	got, err := h.svc.List(h.projectID)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "app.example.com", got[0].DomainHostname)
	assert.Equal(t, "example.com", got[0].ZoneName)
	assert.Equal(t, h.projectID, h.repo.listCalled, "service must pass the project id to the repository")
}

func TestList_PropagatesRepoError(t *testing.T) {
	h := newRecHarness(t, "app.example.com", "example.com", "203.0.113.5", "faketest", &recDNSClient{})
	h.repo.listErr = errors.New("db down")
	_, err := h.svc.List(h.projectID)
	require.Error(t, err)
}
```
Run: `go test ./internal/services/ -run 'TestList_' -v` → PASS. Then `make mocks-check` → clean.

- [ ] **Step 10: Commit**

```bash
git add internal/models/domain_dns_record.go internal/repository/ internal/services/dns_record_service.go internal/services/dns_record_service_test.go internal/services/dns_hosted_zone_service_test.go internal/mocks/
git commit -m "feat(dns): project-wide DNS records list in repo + service"
```

---

## Task 2: Backend — handler, route, OpenAPI

**Repo:** `backend-v2` worktree.

**Files:**
- Modify: `internal/handlers/dns_record_handler.go`, `internal/handlers/service_interfaces.go`
- Regenerate: handler-service mock via `make mocks`
- Modify: `cmd/server/main.go`
- Modify: OpenAPI source for `api/openapi.yaml`
- Test: `internal/handlers/dns_record_handler_test.go`

**Interfaces:**
- Consumes: `(*DNSRecordService).List` (Task 1); `models.DNSRecordListItem`.
- Produces: `GET /projects/:projectId/dns-records` → `200` array of `{…record fields…, domainHostname, zoneName}`.

- [ ] **Step 1: Add `List` to the handler's service interface** — in `internal/handlers/service_interfaces.go`, inside `DNSRecordServiceInterface`, after `Get`:

```go
	List(projectID uuid.UUID) ([]models.DNSRecordListItem, error)
```
Then `make mocks`.

- [ ] **Step 2: Write the failing handler test** — add to `internal/handlers/dns_record_handler_test.go`, mirroring the existing handler-test setup (a gin engine, a `MockDNSRecordService`, a `PermissionChecker` test double, and a user in context). Use the file's existing helpers for router/user/permission setup; the three cases:

```go
func TestDNSRecordHandler_List_OK(t *testing.T) {
	// permission granted; service returns two items
	// GET /projects/:projectId/dns-records -> 200, body length 2,
	// first item has domainHostname + zoneName fields populated.
}

func TestDNSRecordHandler_List_Forbidden(t *testing.T) {
	// CanManageDomains false -> 403, service.List never called.
}

func TestDNSRecordHandler_List_EmptyReturnsArray(t *testing.T) {
	// service returns nil slice -> 200 with body "[]" (not "null").
}
```
Fill each body following the existing `TestDNSRecordHandler_Get_*` cases in this file (same router/user/permission construction; assert `w.Code` and unmarshal `w.Body`). For the empty case, assert `strings.TrimSpace(w.Body.String()) == "[]"`.

- [ ] **Step 3: Run it; expect FAIL** — `go test ./internal/handlers/ -run TestDNSRecordHandler_List` → fails (handler method undefined).

- [ ] **Step 4: Implement the list response + handler** — in `internal/handlers/dns_record_handler.go`, before `Get`:

```go
// dnsRecordListItemResponse is a project-wide list row: every DomainDNSRecord
// field (promoted) plus the record's name (its domain hostname) and the name of
// the hosted zone it lives in, so the list can be shown without a lookup per row.
type dnsRecordListItemResponse struct {
	dnsRecordResponse
	DomainHostname string `json:"domainHostname"`
	ZoneName       string `json:"zoneName"`
}

func toDNSRecordListItemResponse(it models.DNSRecordListItem) dnsRecordListItemResponse {
	return dnsRecordListItemResponse{
		dnsRecordResponse: toDNSRecordResponse(&it.DomainDNSRecord),
		DomainHostname:    it.DomainHostname,
		ZoneName:          it.ZoneName,
	}
}

// List returns every managed DNS record in the project, each enriched with its
// domain hostname and hosted-zone name. Gated by the same canManageDomains
// permission as the per-domain record endpoints.
func (h *DNSRecordHandler) List(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}
	if !h.permChecker.CanManageDomains(projectID, user) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: only project admins can manage DNS records"})
		return
	}
	items, err := h.service.List(projectID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	resp := make([]dnsRecordListItemResponse, 0, len(items))
	for _, it := range items {
		resp = append(resp, toDNSRecordListItemResponse(it))
	}
	c.JSON(http.StatusOK, resp)
}
```
(The `make([]…, 0, len)` guarantees `[]` not `null` for the empty case.)

- [ ] **Step 5: Register the route** — in `cmd/server/main.go`, immediately before `domains := projects.Group("/:projectId/domains")`:

```go
				// Project-wide DNS records list. Aggregates every domain's
				// managed DNS record for the project. Nil-guarded like the
				// per-domain dns-record routes below; the handler enforces
				// canManageDomains, the same permission those routes use.
				if deps.DNSRecordHandler != nil {
					dnsRecords := projects.Group("/:projectId/dns-records")
					dnsRecords.Use(deps.PermChecker.RequireProjectAccess())
					dnsRecords.GET("", deps.DNSRecordHandler.List)
				}
```

- [ ] **Step 6: Run handler tests; expect PASS** — `go test ./internal/handlers/ -run TestDNSRecordHandler_List -v`. Then `go build ./...`.

- [ ] **Step 7: OpenAPI** — add the `GET /projects/{projectId}/dns-records` path and a `DNSRecordListItem` schema (the DNS record fields plus `domainHostname`, `zoneName`) to the OpenAPI source, then `make openapi && make openapi-check`.

- [ ] **Step 8: Commit**

```bash
git add internal/handlers/ cmd/server/main.go internal/mocks/ api/ docs/
git commit -m "feat(dns): GET /projects/:id/dns-records endpoint + OpenAPI"
```

---

## Task 3: Frontend — API client `list` + type

**Repo:** `frontend-v2` worktree.

**Files:**
- Modify: `src/lib/api/dns-records.ts`, `src/types/index.ts`
- Test: `src/lib/api/dns-records.test.ts`

**Interfaces:**
- Produces: `DomainDNSRecordListItem` (type); `dnsRecordsApi.list(projectId: string): Promise<DomainDNSRecordListItem[]>`.

- [ ] **Step 1: Add the type** — in `src/types/index.ts`, after `DomainDNSRecord`:

```ts
export interface DomainDNSRecordListItem extends DomainDNSRecord {
  domainHostname: string;
  zoneName: string;
}
```

- [ ] **Step 2: Write the failing api test** — add to `src/lib/api/dns-records.test.ts`:

```ts
test('list fetches project dns records', async () => {
  (apiClient.get as jest.Mock).mockResolvedValue({ data: [{ id: 'r1', domainHostname: 'a.example.com', zoneName: 'example.com' }] });
  const r = await dnsRecordsApi.list('p1');
  expect(apiClient.get).toHaveBeenCalledWith('/projects/p1/dns-records');
  expect(r).toHaveLength(1);
  expect(r[0].domainHostname).toBe('a.example.com');
});
```

- [ ] **Step 3: Run it; expect FAIL** — `npx jest src/lib/api/dns-records.test.ts` → `dnsRecordsApi.list is not a function`.

- [ ] **Step 4: Implement** — in `src/lib/api/dns-records.ts`, add to the import and the object:

```ts
import type { DomainDNSRecord, DomainDNSRecordListItem, DNSRecordInput } from '@/types';
```
```ts
  list: async (projectId: string): Promise<DomainDNSRecordListItem[]> => {
    const r = await apiClient.get<DomainDNSRecordListItem[]>(`/projects/${projectId}/dns-records`);
    return r.data;
  },
```

- [ ] **Step 5: Run it; expect PASS** — `npx jest src/lib/api/dns-records.test.ts`. Then `npx tsc --noEmit`.

- [ ] **Step 6: Commit**

```bash
git add src/lib/api/dns-records.ts src/lib/api/dns-records.test.ts src/types/index.ts
git commit -m "feat(dns): frontend list() API client + DomainDNSRecordListItem type"
```

---

## Task 4: Frontend — extract shared `<DNSRecordFields>`

**Repo:** `frontend-v2` worktree.

**Files:**
- Create: `src/components/features/dns-record-fields.tsx`
- Modify: `src/app/projects/[projectId]/domains/[domainId]/settings/page.tsx` (replace the inline fields at ~lines 1196–1253 with `<DNSRecordFields …/>`)
- Test: `src/components/features/dns-record-fields.test.tsx`

**Interfaces:**
- Produces: `DNSRecordFields` controlled component:
```ts
interface DNSRecordFieldsProps {
  zoneOptions: { value: string; label: string }[];
  hostedZoneId: string;
  onHostedZoneChange: (id: string) => void;
  recordType: DNSRecordType;
  onRecordTypeChange: (t: DNSRecordType) => void;
  ttl: string;                 // raw input string; '' means Auto
  onTtlChange: (v: string) => void;
  proxied: boolean;
  onProxiedChange: (v: boolean) => void;
  showProxied: boolean;        // true for Cloudflare zones
}
```

- [ ] **Step 1: Write the failing component test** — `src/components/features/dns-record-fields.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react';
import { DNSRecordFields } from './dns-record-fields';

const base = {
  zoneOptions: [{ value: 'z1', label: 'example.com (Cloudflare)' }],
  hostedZoneId: 'z1', onHostedZoneChange: () => {},
  recordType: 'auto' as const, onRecordTypeChange: () => {},
  ttl: '', onTtlChange: () => {},
  proxied: false, onProxiedChange: () => {},
  showProxied: false,
};

test('renders zone, type and ttl; hides proxied unless showProxied', () => {
  const { rerender } = render(<DNSRecordFields {...base} />);
  expect(screen.getByLabelText('Hosted Zone')).toBeInTheDocument();
  expect(screen.getByLabelText('Record Type')).toBeInTheDocument();
  expect(screen.getByLabelText('TTL')).toBeInTheDocument();
  expect(screen.queryByLabelText('Proxied')).not.toBeInTheDocument();
  rerender(<DNSRecordFields {...base} showProxied />);
  expect(screen.getByLabelText('Proxied')).toBeInTheDocument();
});
```

- [ ] **Step 2: Run it; expect FAIL** — module not found.

- [ ] **Step 3: Implement the component** — `src/components/features/dns-record-fields.tsx` (moves the field JSX from the settings page verbatim, now controlled by props):

```tsx
'use client';

import { Input, Select } from '@/components/ui';
import type { DNSRecordType } from '@/types';

interface DNSRecordFieldsProps {
  zoneOptions: { value: string; label: string }[];
  hostedZoneId: string;
  onHostedZoneChange: (id: string) => void;
  recordType: DNSRecordType;
  onRecordTypeChange: (t: DNSRecordType) => void;
  ttl: string;
  onTtlChange: (v: string) => void;
  proxied: boolean;
  onProxiedChange: (v: boolean) => void;
  showProxied: boolean;
}

export function DNSRecordFields(props: DNSRecordFieldsProps) {
  const { zoneOptions, hostedZoneId, onHostedZoneChange, recordType, onRecordTypeChange, ttl, onTtlChange, proxied, onProxiedChange, showProxied } = props;
  return (
    <div className="space-y-3">
      <Select
        id="dnsHostedZone"
        label="Hosted Zone"
        value={hostedZoneId}
        onChange={(e) => onHostedZoneChange(e.target.value)}
        options={zoneOptions}
      />
      <Select
        id="dnsRecordType"
        label="Record Type"
        value={recordType}
        onChange={(e) => onRecordTypeChange(e.target.value as DNSRecordType)}
        options={[
          { value: 'auto', label: 'Auto' },
          { value: 'A', label: 'A' },
          { value: 'AAAA', label: 'AAAA' },
          { value: 'CNAME', label: 'CNAME' },
        ]}
      />
      <div>
        <label htmlFor="dnsTtl" className="block text-sm font-medium text-gray-700 mb-1">TTL</label>
        <Input id="dnsTtl" type="number" min={0} placeholder="Auto" value={ttl} onChange={(e) => onTtlChange(e.target.value)} disabled={proxied} />
        {proxied && <p className="mt-1 text-xs text-gray-500">TTL is managed automatically by Cloudflare when proxied.</p>}
      </div>
      {showProxied && (
        <div className="flex items-center gap-2">
          <input type="checkbox" id="dnsProxied" checked={proxied} onChange={(e) => onProxiedChange(e.target.checked)} className="h-4 w-4 rounded border-gray-300 text-primary-600" />
          <label htmlFor="dnsProxied" className="text-sm font-medium text-gray-700">Proxied</label>
        </div>
      )}
    </div>
  );
}
```

- [ ] **Step 4: Rewire the settings page** — in `settings/page.tsx`, replace the inline `<Select dnsHostedZone/>…proxied checkbox` block (~1196–1253) with:

```tsx
<DNSRecordFields
  zoneOptions={matchingZones.map((zone) => ({ value: zone.id, label: credentialLabelFor(zone) }))}
  hostedZoneId={selectedHostedZoneId ?? ''}
  onHostedZoneChange={handleSelectedHostedZoneChange}
  recordType={dnsRecordType}
  onRecordTypeChange={setDnsRecordType}
  ttl={dnsTtl}
  onTtlChange={setDnsTtl}
  proxied={dnsProxied}
  onProxiedChange={setDnsProxied}
  showProxied={selectedZoneIsCloudflare}
/>
```
Add `import { DNSRecordFields } from '@/components/features/dns-record-fields';`.

- [ ] **Step 5: Run tests + typecheck** — `npx jest src/components/features/dns-record-fields.test.tsx`; `npx tsc --noEmit`; run any existing settings-page test to confirm no regression.

- [ ] **Step 6: Commit**

```bash
git add src/components/features/dns-record-fields.tsx src/components/features/dns-record-fields.test.tsx "src/app/projects/[projectId]/domains/[domainId]/settings/page.tsx"
git commit -m "refactor(dns): extract shared DNSRecordFields component"
```

---

## Task 5: Frontend — DNS Records list page

**Repo:** `frontend-v2` worktree.

**Files:**
- Create: `src/app/projects/[projectId]/dns-records/page.tsx`
- Test: `src/app/projects/[projectId]/dns-records/page.test.tsx`

**Interfaces:**
- Consumes: `dnsRecordsApi.list/update/remove/refresh` (Tasks 3 + existing); `dnsZonesApi.list`; `DNSRecordFields` (Task 4); `DomainDNSRecordListItem`.

- [ ] **Step 1: Write the failing page test** — `src/app/projects/[projectId]/dns-records/page.test.tsx`:

```tsx
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import DNSRecordsPage from './page';

jest.mock('next/navigation', () => ({ useParams: () => ({ projectId: 'p1' }) }));
jest.mock('@/lib/api/dns-records', () => ({
  dnsRecordsApi: {
    list: jest.fn(),
    update: jest.fn().mockResolvedValue({}),
    remove: jest.fn().mockResolvedValue(undefined),
    refresh: jest.fn().mockResolvedValue({}),
  },
}));
jest.mock('@/lib/api/dns-zones', () => ({ dnsZonesApi: { list: jest.fn().mockResolvedValue([]) } }));
import { dnsRecordsApi } from '@/lib/api/dns-records';

const rows = [
  { id: 'r1', domainId: 'd1', hostedZoneId: 'z1', recordType: 'A', proxied: false, status: 'ready', resolvedTarget: '192.0.2.1', domainHostname: 'a.example.com', zoneName: 'example.com', createdAt: '', updatedAt: '' },
];

beforeEach(() => { (dnsRecordsApi.list as jest.Mock).mockResolvedValue(rows); });

test('lists records', async () => {
  render(<DNSRecordsPage />);
  await waitFor(() => expect(screen.getByText('a.example.com')).toBeInTheDocument());
  expect(screen.getByText('example.com')).toBeInTheDocument();
  expect(screen.getByText('192.0.2.1')).toBeInTheDocument();
});

test('shows empty state when there are no records', async () => {
  (dnsRecordsApi.list as jest.Mock).mockResolvedValue([]);
  render(<DNSRecordsPage />);
  await waitFor(() => expect(screen.getByText(/no dns records/i)).toBeInTheDocument());
});

test('shows error when list fails', async () => {
  (dnsRecordsApi.list as jest.Mock).mockRejectedValue(new Error('boom'));
  render(<DNSRecordsPage />);
  await waitFor(() => expect(screen.getByText(/failed to load/i)).toBeInTheDocument());
});

test('delete removes the row', async () => {
  window.confirm = jest.fn(() => true);
  render(<DNSRecordsPage />);
  await waitFor(() => screen.getByText('a.example.com'));
  fireEvent.click(screen.getByRole('button', { name: /delete/i }));
  await waitFor(() => expect(dnsRecordsApi.remove).toHaveBeenCalledWith('p1', 'd1'));
});

test('refresh re-fetches', async () => {
  render(<DNSRecordsPage />);
  await waitFor(() => screen.getByText('a.example.com'));
  fireEvent.click(screen.getByRole('button', { name: /refresh/i }));
  await waitFor(() => expect(dnsRecordsApi.refresh).toHaveBeenCalledWith('p1', 'd1'));
});
```

- [ ] **Step 2: Run it; expect FAIL** — module not found.

- [ ] **Step 3: Implement the page** — `src/app/projects/[projectId]/dns-records/page.tsx`. Client component. Load records via `dnsRecordsApi.list(projectId)` in `useEffect`; hold `isLoading`/`error`. Render a `Card` table: columns Name (`domainHostname`), Type (`recordType`), Target (`resolvedTarget || '—'`), Zone (`zoneName || '—'`), Status (`<Badge>` keyed off `status`: ready→success, pending→warning, error→error), Actions. Empty state: "No DNS records. Enable DNS on a domain to create one." Error state: "Failed to load DNS records." Row actions:
  - **Refresh**: `await dnsRecordsApi.refresh(projectId, row.domainId)` then reload the list.
  - **Delete**: `if (window.confirm(...)) { await dnsRecordsApi.remove(projectId, row.domainId); reload(); }`.
  - **Edit**: open a `Modal`; load hosted zones via `dnsZonesApi.list()`; seed state from the row (`hostedZoneId`, `recordType`, `ttl?toString():''`, `proxied`); render `<DNSRecordFields …/>` with `showProxied` = selected zone's provider is Cloudflare; Save → `dnsRecordsApi.update(projectId, row.domainId, { hostedZoneId, recordType, ttl: ttl===''?undefined:Number(ttl), proxied })` then close + reload. Follow the dns-credentials page for the list/modal/loading/error structure and the `@/components/ui` imports (`Button, Card, CardContent, Badge, Modal`).

- [ ] **Step 4: Run tests + typecheck; expect PASS** — `npx jest src/app/projects/[projectId]/dns-records/page.test.tsx`; `npx tsc --noEmit`.

- [ ] **Step 5: Commit**

```bash
git add "src/app/projects/[projectId]/dns-records/"
git commit -m "feat(dns): project DNS records list page with edit/delete/refresh"
```

---

## Task 6: Frontend — sidebar "DNS Records" entry

**Repo:** `frontend-v2` worktree.

**Files:**
- Modify: `src/components/features/sidebar.tsx`

- [ ] **Step 1: Add the nav item** — in `sidebar.tsx`, right after the Domains push (~line 122):

```tsx
    resourcesNavItems.push({ href: `/projects/${projectId}/dns-records`, icon: Network, label: 'DNS Records' });
```
(`Network` is already imported for Hosted Zones; if lint flags an unused/needed icon, use an imported one such as `Globe`.)

- [ ] **Step 2: Typecheck + lint** — `npx tsc --noEmit`; run the frontend test suite `npx jest` to confirm no regression.

- [ ] **Step 3: Commit**

```bash
git add src/components/features/sidebar.tsx
git commit -m "feat(dns): add DNS Records to the project sidebar"
```

---

## Out of Scope

- Standalone DNS records decoupled from domains; editing a record's name or target. (Spec.)
- Changing the existing per-domain DNS management behavior — Task 4 is a pure extraction with no behavior change.

## Known Unrelated Issue

`internal/repository`'s `TestDomainDNSRecordRepository_CountByZone` fails against a real migrated schema (inserts two records for one `domain_id`, violating `domain_dns_records_domain_id_key`). Pre-existing bug, independent of this change; do not fix here.
