# DNS Records List & Management — Design

**Status:** Draft for review
**Date:** 2026-10-06
**Repos:** `backend-v2`, `frontend-v2`
**Author:** Claude (Opus 4.8), with zufar.dhiyaulhaq

> Note: a first pass of the backend was implemented before this spec was written
> (the task was initially — wrongly — classified as bounded). That code is
> uncommitted and will be reconciled against this spec and the plan that follows
> it, not treated as final.

## Goal

Give operators one place to see every DNS record FastGateway has created across a
project, and to edit or delete those records — without navigating into each domain
one at a time.

## Background

DNS records today are **per-domain and auto-managed**. Each domain has at most one
`DomainDNSRecord` (unique `DomainID`), created when DNS is enabled for the domain and
reconciled against the provider. The record stores `RecordType, TTL, Proxied,
ResolvedTarget, Status`; its **name** is the domain's hostname (not stored on the
record), and the zone it lives in comes from `HostedZoneID`.

CRUD already exists, but only per domain, at
`…/projects/:projectId/domains/:domainId/dns-record` — `Get / Enable / Update /
Delete / Refresh`. There is **no project-wide view**: records are reachable only one
domain at a time. Hosted zones (`/dns/zones`) already have a List endpoint and a list
page, which is the pattern to follow.

## Scope

**In scope**
- A project-wide list of all DNS records (one per domain that has DNS enabled),
  enriched with the domain hostname (the record name) and the hosted-zone name.
- Edit and delete of a listed record, reusing the existing per-domain
  `Update` / `Delete` behavior.
- Refresh of a listed record (re-reconcile status), reusing the existing `Refresh`.

**Out of scope (YAGNI)**
- Standalone DNS records decoupled from domains (no new data model; enabling DNS on a
  domain stays the only way a record is created).
- Editing a record's name or target/value directly. Edit covers only the existing
  configurable fields: hosted zone, record type (`auto/A/AAAA/CNAME`), TTL, proxied.

## Decisions (from scoping)

1. **Target repos:** `backend-v2` + `frontend-v2` (where DNS lives). Isolated worktrees
   off `main`, local commits, confirm before push.
2. **Record model:** aggregate the existing per-domain records into one list; edit/delete
   act on those records through the current per-domain API. No standalone records.
3. **Edit scope:** existing fields only.

## Architecture

### Backend (`backend-v2`)

**Read projection** — `models.DNSRecordListItem`:
```go
type DNSRecordListItem struct {
    DomainDNSRecord          // embedded: all record fields, incl. DomainID
    DomainHostname string `json:"domainHostname"` // the record name
    ZoneName       string `json:"zoneName"`
}
```

**Repository** — `DomainDNSRecordRepository.ListByProjectID(projectID) ([]DNSRecordListItem, error)`:
a single query joining `domain_dns_records → domains` (filter `domains.project_id`) and
LEFT JOIN `dns_hosted_zones` for the zone name, ordered by hostname. One round trip, no
N+1. A record whose zone row is missing still appears, with an empty `ZoneName`. Added to
`DomainDNSRecordRepositoryInterface`; mocks regenerated via `make mocks`.

**Service** — `DNSRecordService.List(projectID) ([]DNSRecordListItem, error)`: a pure
read that delegates to the repo. It does **not** reconcile against the provider (Status is
the last persisted value); per-domain `Get`/`Refresh` remain the reconcile path. Project
scoping lives in the query, so no per-domain ownership check is needed.

**Handler + route** — `GET /projects/:projectId/dns-records` → `DNSRecordHandler.List`,
under `RequireProjectAccess` and gated by `CanManageDomains` (the exact permission the
per-domain DNS routes use). Nil-guarded like the per-domain `dns-record` group (the
handler only exists in-cluster). Response: an array of the record fields (promoted) plus
`domainHostname` and `zoneName` per row.

**OpenAPI:** add the endpoint + the `DNSRecordListItem` schema; `make openapi` +
`make openapi-check`.

### Frontend (`frontend-v2`)

**API client** (`src/lib/api/dns-records.ts`): add `list(projectId)` →
`GET /projects/:projectId/dns-records`; add a `DomainDNSRecordListItem` type (the record
+ `domainId`, `domainHostname`, `zoneName`).

**Shared form** — extract the four DNS fields (hosted-zone picker, record type, TTL,
proxied) that currently live inline in the domain settings page into a reusable
`<DNSRecordForm>`, used by both the domain settings page (no behavior change there) and
the new list page's edit modal.

**List page** — `src/app/projects/[projectId]/dns-records/page.tsx`: a table of
Name (`domainHostname`) · Type · Target (`resolvedTarget`) · Zone · Status (badge) ·
actions. Actions, reusing the existing per-domain API with the row's `domainId`:
- **Edit** → modal with `<DNSRecordForm>` → `dnsRecordsApi.update(projectId, domainId, …)`.
- **Delete** → confirm dialog → `dnsRecordsApi.remove(projectId, domainId)`.
- **Refresh** → `dnsRecordsApi.refresh(projectId, domainId)` to re-reconcile status.

**Navigation** — a "DNS Records" entry in the project sidebar, next to Domains.

## Data Flow

1. User opens the project's **DNS Records** page.
2. Frontend calls `GET /projects/:projectId/dns-records`; the backend joins records →
   domains (+ zones) scoped to the project and returns the enriched list.
3. Each row renders name/type/target/zone/status. Edit/Delete/Refresh call the existing
   per-domain endpoints with the row's `domainId`; the list re-fetches afterward.

## Error Handling

- **List** failure → page shows an error state; the row actions are unavailable.
- **Edit** → the service's existing validation/mapping applies (`ErrNoHostedZone`,
  `ErrInvalidRecordType`, apex/clobber → 400; provider errors → 502; not found → 404).
- **Delete/Refresh** → existing per-domain behavior and status codes.
- **Permissions** → non-admins get 403 on the list endpoint, same as the per-domain routes.

## Security

- The list endpoint is project-scoped and gated by `CanManageDomains` — identical to the
  per-domain DNS routes; a user cannot list another project's records.
- The repository query filters by `domains.project_id`, so cross-project leakage is not
  possible even if a record's domain is passed out of band.

## Testing

- **Backend:** repo integration test (join scoping + enrichment + ordering, excludes other
  projects) against a migrated Postgres; service test (pure read, passes project id,
  propagates repo error); handler test (200 shape, 403 without permission, 400 bad id,
  500 on service error); `make openapi-check` and `make mocks-check`.
- **Frontend:** api-client test (`list` calls the right path); page test (renders rows,
  edit opens the form and calls `update`, delete confirms and calls `remove`, refresh calls
  `refresh`); `<DNSRecordForm>` test; `tsc` clean.

## Known Unrelated Issue

`internal/repository`'s existing `TestDomainDNSRecordRepository_CountByZone` fails against a
real migrated schema (it inserts two records for one `domain_id`, violating the
`domain_dns_records_domain_id_key` unique constraint) — a pre-existing test bug, independent
of this change. Flagged, not fixed here.
