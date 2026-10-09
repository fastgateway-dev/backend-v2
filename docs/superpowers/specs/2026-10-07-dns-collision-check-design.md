# DNS Collision Check at Domain Create — Design

**Status:** Draft for review
**Date:** 2026-10-07
**Repos:** `backend-v2` (primary), `frontend-v2` (surface the error)
**Author:** Claude (Opus 4.8), with zufar.dhiyaulhaq

## Goal

When a domain is created with DNS enabled, detect up-front that the hostname's DNS
is already claimed — by **another FastGateway project** (inside) or by a **foreign
record at the provider** (outside) — and **reject the whole create** with a clear,
source-specific error, instead of silently letting the record land in `error`
status later with a misleading message.

## Background (current behavior)

- Domains are unique only **per project** (`idx_domain_project_hostname` on
  `(project_id, hostname)`), so two projects can each register `app.example.com`.
- Hosted zones are **global / owner-managed** (`DNSHostedZone` has no `ProjectID`;
  `/dns/zones` is `RequireRole("owner")`) — a shared pool every project picks from.
- DNS records are per-domain (`DomainDNSRecord`, unique `domain_id`), written to the
  provider by `DNSRecordService.reconcile`. The only collision guard today is the
  **first-write clobber check** inside `reconcile`: `GetRecord(zone, hostname, type)` —
  if a record is found, it sets status `error` with
  *"a record already exists for … not managed by FastGateway"*.
- Net effect: the first project to enable DNS wins; any other project (or a foreign
  record) blocks the second **asynchronously**, in `error` status, with a message that
  is wrong for the cross-project case (it *is* managed by FastGateway, by another
  project) and never surfaces at create time.

## Decisions (from brainstorming)

1. **Feature flag:** reuse the existing per-create opt-in `CreateDomainInput.DNS.Enabled`.
   No new flag, nothing in system settings.
2. **On collision:** **fail the whole domain-create** (no domain, Gateway, or routes
   are created) — the check runs *before* any resource is built.
3. **Distinguish inside vs outside** with separate errors/messages.
4. **Outside check:** a **type-agnostic** provider lookup (does *any* A/AAAA/CNAME
   record exist at the hostname), because at create time the Gateway doesn't exist yet,
   so an `auto` record's eventual type (A/AAAA vs CNAME) is unknown.
5. **Inside-check status scope:** any existing `DomainDNSRecord` row for the hostname+zone
   counts as a claim, **regardless of its status** (including `error`) — the row is the
   owner's standing claim.
6. **Provider unreachable during the outside check** → **fail the create (502)**
   (fail-safe; can't verify ⇒ reject), with a clear "try again" message.
7. **Hostname normalization:** matching is normalized — **lowercase + strip a trailing
   dot** — in the new checks, and the existing `reconcile` containment compare is fixed
   to normalize too, so the two never disagree.
8. **Update stays zone-immutable:** `DNSRecordService.Update` ignores `hostedZoneId`
   today, so a record cannot change zones and needs no collision guard. The collision
   entry points are **domain-create** and **first-time enable** only. (The separate fact
   that the DNS-records edit modal shows a zone picker that is silently dropped on save is
   tracked as its own fix — see Out of Scope.)
9. **Race:** the inside check is read-then-write; we accept the tiny TOCTOU window and
   rely on the provider write as the real serializer (reconcile's first-write clobber
   makes the loser land in `error`) rather than adding a cross-table DB constraint.

## Architecture

### 1. New provider capability — type-agnostic existence

`internal/dnsprovider`: extend `DNSClient` with

```go
// RecordExistsForName reports whether any A, AAAA, or CNAME record exists at
// name in the zone, regardless of type. Used for the create-time collision
// check, where the eventual record type (auto -> A/AAAA/CNAME) is not yet known.
RecordExistsForName(ctx context.Context, providerZoneID, name string) (bool, error)
```

Implement in all three clients. Each already lists records by name:
- **Cloudflare:** `ListDNSRecords(zone, {Name: name})`; true if any result has type in {A, AAAA, CNAME}.
- **Route53:** `ListResourceRecordSets` filtered to the name; true if any A/AAAA/CNAME set exists.
- **Google Cloud DNS:** `ResourceRecordSets.List(zone, name=fqdn)`; true if any A/AAAA/CNAME rrset exists.

(The existing per-type `GetRecord` stays; the reconcile-time clobber check is unchanged.)

### 2. New repository query — cross-project claim (inside)

`internal/repository/domain_dns_record_repository.go`:

```go
// HostnameClaimExists reports whether any DomainDNSRecord exists for the given
// hostname in the given hosted zone, on a domain OTHER than excludeDomainID
// (pass uuid.Nil at create, where there is no domain yet). Joins
// domain_dns_records -> domains on domains.hostname. Because a hostname is
// unique within a project, a match is always a different project.
HostnameClaimExists(hostname string, zoneID, excludeDomainID uuid.UUID) (bool, error)
```

Query: `SELECT EXISTS(SELECT 1 FROM domain_dns_records rec JOIN domains d ON d.id = rec.domain_id WHERE d.hostname = ? AND rec.hosted_zone_id = ? AND rec.domain_id <> ?)`. Added to `DomainDNSRecordRepositoryInterface`; mocks regenerated.

### 3. New service pre-flight — `CheckCollision`

`internal/services/dns_record_service.go`:

```go
// CheckCollision validates that hostname can take a managed DNS record in zone
// without clobbering an existing claim. excludeDomainID is the domain being
// updated (uuid.Nil at create). It runs three checks, in order, and returns a
// typed error for the first that fails:
//   1. containment  -> ErrHostedZoneMismatch   (hostname not apex/subdomain of zone)
//   2. inside (DB)   -> ErrHostnameClaimed      (another FastGateway project owns it)
//   3. outside (API) -> ErrForeignRecordExists  (a provider record FG doesn't manage)
// nil means the hostname is free to use.
func (s *DNSRecordService) CheckCollision(hostname string, zoneID, excludeDomainID uuid.UUID) error
```

All comparisons normalize first: a shared helper `normalizeHostname(s) =
strings.ToLower(strings.TrimSuffix(strings.TrimSpace(s), "."))` is applied to both the
hostname and the zone name.

- **Containment:** the existing apex/subdomain rule, now normalized: `nh == nz ||
  strings.HasSuffix(nh, "."+nz)` where `nh/nz` are the normalized hostname/zone name. The
  same `normalizeHostname` is applied in `reconcile`'s containment compare (L419) so the
  two paths agree.
- **Inside:** `repo.HostnameClaimExists(hostname, zoneID, excludeDomainID)` → `ErrHostnameClaimed`.
  The repo query matches case-insensitively (`LOWER(d.hostname) = LOWER(?)`, trailing dot
  trimmed on both sides) so mixed-case rows still collide.
- **Outside:** decrypt the zone's credential, build the provider client, call
  `RecordExistsForName(zone.ProviderZoneID, hostname)` → `ErrForeignRecordExists` if true.
  A provider/credential error is returned as-is (fail-safe: can't verify ⇒ reject),
  wrapped so the handler maps it to a 502/"could not verify" rather than a collision.

New sentinel errors in the service: `ErrHostnameClaimed`, `ErrForeignRecordExists`.

**Privacy:** `ErrHostnameClaimed`'s message does NOT name the other project (a
project admin must not learn another project's names). Message:
*"DNS for this hostname is already managed by another project in this hosted zone."*
`ErrForeignRecordExists`: *"a DNS record already exists at the provider for this
hostname that FastGateway does not manage."*

### 4. Wiring

- **Domain create (fail the whole create):** `DomainService.Create` (L302), when
  `input.DNS != nil && input.DNS.Enabled && s.dnsRecords != nil`, runs the collision
  check **at the top of the function — before the existing `ExistsByHostname` check and
  before `domainRepo.Create` (L397) or any Gateway call**, so an error returns `nil, err`
  with nothing persisted and no K8s resource created (the function's existing
  fail-before-L397 pattern). `input.DNS.HostedZoneID` (a string) is parsed there; a bad
  UUID is a 400. The check is reached through the existing optional `DNSRecordManager`
  dependency, extended with `CheckCollision(hostname, hostedZoneID uuid.UUID) error` (the
  create variant, `excludeDomainID = uuid.Nil`). When `dnsRecords` is nil (out-of-cluster)
  the check — like the whole DNS-enable tail — is skipped.
- **First-time enable (settings page):** `DNSRecordService.Enable` calls
  `CheckCollision(domain.Hostname, *in.HostedZoneID, domainID)` before creating the record
  row, so enabling DNS later is guarded the same way (its own domain is excluded).
  `Create`'s best-effort DNS tail also goes through `Enable`, so it is covered even though
  the top-of-`Create` pre-flight already verified it.
- **Update is not guarded:** `Update` cannot change the hosted zone, so it cannot
  introduce a collision — no check is added there.

### 5. Error → HTTP mapping

In the domain handler's create path and the DNS record handler:
- `ErrHostnameClaimed` → **409 Conflict**
- `ErrForeignRecordExists` → **409 Conflict**
- `ErrHostedZoneMismatch` → **400 Bad Request** (already exists)
- provider/credential failure during the outside check → **502 Bad Gateway**
  (*"could not verify DNS at the provider; try again"*)

### 6. Frontend (`frontend-v2`)

The domain create page already surfaces the API error from `domainsApi.create`. Verify
the create flow renders the 409 message to the user (and does not leave the form in a
stuck state); add a focused test. No new UI beyond showing the error. The per-domain
DNS enable (settings page) already surfaces `dnsError`.

## Data Flow (create with DNS enabled)

1. User submits domain-create with `dns.enabled` + `hostedZoneId`.
2. `CreateDomain` → `CheckCollision(hostname, zoneId, Nil)`:
   containment → inside DB check → outside provider check.
3. Any failure → create returns 4xx/502; **no domain/Gateway/record created**.
4. Clear → domain + Gateway created, DNS record enabled as today (reconcile still runs
   its own first-write clobber check as a last-line net).

## Error Handling

- Inside/outside collisions → 409 with the source-specific message above.
- Containment mismatch → 400.
- Provider unreachable during the outside check → 502, nothing created (fail-safe per
  the "reject" decision).
- Out-of-cluster (no control plane / `dnsRecords` nil) → the DNS-enable step and its
  collision check are skipped entirely (unchanged behavior).

## Security / Privacy

- The inside-collision message never reveals the other project's identity.
- The check adds no new cross-project data exposure: it answers only a boolean
  ("is this hostname+zone claimed elsewhere"), surfaced only to a caller already
  authorized to create a domain / manage DNS in their own project.

## Testing

- **Provider clients:** `RecordExistsForName` returns true when an A/AAAA/CNAME exists at
  the name, false otherwise, per provider (table tests against each client's fake/mock).
- **Repo:** `HostnameClaimExists` true for another domain's record in the same
  hostname+zone, false when only the excluded domain matches, false across different
  zones — Postgres integration test.
- **Service:** `CheckCollision` returns `ErrHostedZoneMismatch` / `ErrHostnameClaimed` /
  `ErrForeignRecordExists` / nil for the four cases; provider error propagates; and
  matching is case-insensitive / trailing-dot-insensitive (e.g. `App.Example.com.` vs a
  stored `app.example.com` collides, and containment accepts `App.Example.com` in
  `example.com`).
- **Normalization:** `normalizeHostname` lowercases, trims space, strips one trailing dot;
  the fixed `reconcile` containment accepts mixed-case/trailing-dot hostnames.
- **Create path:** `CreateDomain` with DNS enabled and a claimed hostname fails and
  creates nothing (no domain row, no Gateway call); clear hostname proceeds.
- **Handler mapping:** 409 for both collision kinds, 400 for mismatch, 502 for provider
  failure.
- **Frontend:** create page shows the 409 message and recovers.

## Out of Scope (tracked separately)

- **DNS-records edit modal zone picker is a no-op.** `DNSRecordService.Update` ignores
  `hostedZoneId`, so the hosted-zone dropdown in the edit modal (shipped with the DNS
  records list) is silently dropped on save. Fix separately: either make `Update` honor a
  hosted-zone change (and then guard it with `CheckCollision`) or remove the picker from
  the modal. Not folded in here to keep this change's blast radius small.
- Changing domain uniqueness to be global, or making hosted zones project-scoped.
- Allowing coordinated multi-project sharing of a record.
- Transferring ownership when the first owner deletes (today the second can re-enable
  after the first is gone; unchanged).
