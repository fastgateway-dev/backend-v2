# DNS Management — Direct Provider Redesign

**Status:** Approved (brainstorming + grilling) — ready for implementation planning
**Date:** 2026-10-05
**Author:** zufardhiyaulhaq (with Claude)
**Supersedes:** the external-dns-based DNS management (`2026-10-04-dns-management-design.md`), merged to `main` but unreleased (no image built, migrations never run on a real DB).

## 1. Overview & intent

Re-architect DNS management so FastGateway writes DNS records **directly via each
provider's official Go SDK** (Cloudflare, AWS Route53, Google Cloud DNS), removing
the external-dns dependency entirely. FastGateway makes outbound HTTPS calls to the
provider; it no longer creates `DNSEndpoint` custom resources or relies on an
in-cluster controller.

DNS is modeled around **registered hosted zones**. The flow:
1. **Create a DNS credential** (the existing shared credentials registry).
2. **Register a hosted zone** — a zone name (e.g. `example.com`) bound to a
   credential; FastGateway validates the zone exists in that account and caches
   its provider zone id.
3. **Enable DNS on a domain** by selecting a registered hosted zone whose name is
   a suffix of the domain's hostname; FastGateway writes the record directly into
   that zone.

### Why
- **No operator burden:** no external-dns to install, no `DNSEndpoint` CRD, no
  policy/owner-id/secret coordination, no helm RBAC for it.
- **Multi-provider / multi-account "just works":** each hosted zone carries its own
  credential, so different domains write to Cloudflare, Route53, or Google — and
  different accounts — simultaneously. The "one active credential / one external-dns
  instance per cluster" limitation is **eliminated**.
- **Explicit + safe:** FastGateway only writes to zones you deliberately registered,
  not "any zone a token can see."
- **Immediate, reliable create/update/delete:** status reflects the real provider
  result, and delete actually deletes.

### Success criteria
- An operator registers a credential + hosted zone once; per domain, picks a
  registered zone and DNS is written to the provider.
- Enabling DNS points the hostname at the gateway's external address; status is
  visible; deleting the domain/record removes it from the provider.
- Works across providers/accounts at once; no external-dns anywhere.

## 2. Scope

### In scope (v1)
- **Three providers: Cloudflare, Route53, Google Cloud DNS** (Azure deferred).
- **Hosted zones** as a first-class, owner-managed entity (zone name + credential +
  validated provider zone id).
- One FastGateway-managed DNS record **per domain** (hostname → gateway address),
  type A/AAAA/CNAME auto-chosen from the resolved target, written into a selected
  hosted zone via the provider's official Go SDK.
- **Per-record hosted-zone** selection (the zone implies the credential/provider).
- **Refuse-to-clobber** ownership: our record row is the ownership source of truth
  (`resolved_target != ""` ⇒ owned); only records FastGateway wrote are updated/deleted.
- **Lazy + deferred-retry** reconciliation (details in §6).
- Reuse of the credential model, the record REST API shape (zone replaces credential
  in the input), and the frontend patterns.

### Out of scope / tracked follow-ups
- **external-dns** and the `DNSEndpoint` CR — removed.
- The **"active DNS credential"** concept, secret rendering, and its endpoints — removed.
- **Azure DNS** provider.
- **Automatic gateway-IP-change propagation** beyond the deferred retry (a persistent
  reconciler / informer).
- **ALIAS/ANAME at a zone apex** (apex + CNAME target is rejected in v1).
- **More than one record per domain**; **e2e** (needs a real account or a mock-provider
  server); **ACME DNS01 for non-Cloudflare** (unchanged, still Cloudflare-only).

## 3. Architecture & write path

### 3.1 `DNSProvider` refocused + a `DNSClient`
Keep `Type`/`RequiredFields`/`Validate`; **drop** `RenderSecret`/`ExternalDNSFlag`/
`SecretName`; **add** a client factory.

```go
type DNSProvider interface {
    Type() string
    RequiredFields() []string
    Validate(cred map[string]string) error
    NewClient(cred map[string]string) (DNSClient, error)
}

type DNSClient interface {
    FindZone(ctx, zoneName string) (providerZoneID string, found bool, err error)  // registration-time validation
    GetRecord(ctx, providerZoneID, name, recordType string) (rec Record, found bool, err error) // first-write clobber check only
    UpsertRecord(ctx, providerZoneID string, r Record) error
    DeleteRecord(ctx, providerZoneID, name, recordType string) error
}
type Record struct { Name, Type, Target string; TTL *int; Proxied bool }
```

Per-provider implementations in `internal/dnsprovider/{cloudflare,route53,google}.go`.

**SDKs (from the fact-find):**
- Cloudflare: `github.com/cloudflare/cloudflare-go` **v0** (simple, battle-tested;
  isolated behind `DNSClient` so a later move to the generated SDK is contained).
  API token alone suffices (Zone:Read + DNS:Edit). `proxied` forces TTL auto (§5).
- Route53: `github.com/aws/aws-sdk-go-v2/service/route53`. Needs a `region` on the
  client config (global service, but SDK requires one) — add an optional `region`
  credential field, default `us-east-1`. Upsert via `ChangeResourceRecordSets`
  (Action=UPSERT); DELETE needs the exact current RRSet values.
- Google: `google.golang.org/api/dns/v1` (the only official client). `serviceAccountKey`
  JSON authenticates; `project` is passed on each call (both already stored). Updates
  via atomic `Changes.Create` (delete old + add new).

### 3.2 Write path (record service reconcile)
For an enabled record whose domain has a resolved gateway address:
1. Load the record's **hosted zone** → its credential + `provider_zone_id`.
2. `providerType, creds := DNSCredentialService.DecryptedCredentials(zone.ProviderCredentialID)`;
   `client := dnsprovider.Get(providerType).NewClient(creds)`.
3. **Apex + CNAME guard:** if the record type resolves to `CNAME` (gateway target is a
   hostname) **and** the record name equals the zone apex (`hostname == zone.Name`) →
   `error` ("CNAME at a zone apex isn't supported; use an IP gateway or a subdomain").
   (An **A/AAAA at apex is fine.**)
4. **Clobber check — first write only** (when `resolved_target == ""`):
   `existing, found := client.GetRecord(provider_zone_id, hostname, type)`. If `found`
   → **foreign** → `error` ("a record already exists for `<host>` not managed by
   FastGateway"); do not touch it. Once owned (`resolved_target != ""`), updates skip
   `GetRecord` and UPSERT directly.
5. `client.UpsertRecord(provider_zone_id, Record{…})` → `ready`; set `resolved_target`
   (also the ownership marker).

**Delete:** build the client → `DeleteRecord(provider_zone_id, hostname, type)` —
only when we own the record (`resolved_target != ""`).

### 3.3 No k8s writes
`internal/kubernetes/externaldns.go` + `DNSEndpointGVR` are removed. DNS touches the
cluster only to **read Gateway `status.addresses`** (already granted). The new
dependency is **outbound HTTPS** to the provider.

## 4. Hosted zones (new entity)

`dns_hosted_zones` — owner-managed, platform-global (parallel to `dns_provider_credentials`):

| Column | Notes |
|---|---|
| `id` | uuid PK |
| `name` | zone name as it exists in the provider, e.g. `example.com` |
| `provider_credential_id` | FK → `dns_provider_credentials` |
| `provider_zone_id` | cached provider zone id, set at registration |
| `status` | `ready` \| `error` |
| `status_message` | text |
| `created_by`, `created_at`, `updated_at` | audit |

- **Registration validates:** on create, FastGateway calls `FindZone(name)` with the
  credential; if found, stores `provider_zone_id` + `ready`; else `error`.
- **Owner-only CRUD** endpoints, mirroring `/dns/credentials`: `/dns/zones`
  (GET list, POST create, GET/:id, DELETE). Optional `POST /dns/zones/:id/refresh`
  to re-validate.
- **Delete guard:** a zone can't be deleted while a `domain_dns_records` row uses it
  → 409.

## 5. Credentials (unchanged registry, referenced by zones)

- The shared `dns_provider_credentials` + `DNSCredentialService` is reused as-is —
  the same registry that backs ACME DNS01 cert issuers. One credential, two consumers
  (cert issuers + hosted zones).
- **Route53 gains an optional `region` field** (default `us-east-1`); the provider-aware
  credential form adds it. `DNSCredentialData` is a free-form encrypted map, so no
  schema migration — just a new optional key + a form field.
- **Removed:** `active_dns_credential_id` (setting + column), `DNSInfraService`
  (secret rendering + active getter/setter), the active-credential endpoints, and the
  `ErrDNSCredentialIsActive` guard.
- **Credential in-use guard** (`DNSCredentialService.Delete`): keep the ACME-issuer
  check; **replace** the direct-record check with a **hosted-zone** check (a credential
  can't be deleted while a hosted zone uses it) → 409. (Records reference zones, not
  credentials directly.)
- Credentials decrypted in-process at call time only — never a k8s Secret, never logged.

## 6. Status & reconciliation (lazy + deferred retry)

Status enum: **`pending` → `ready` → `error`**.
- **pending:** enabled, gateway LB address not resolved yet → nothing written.
- **ready:** record written/confirmed (on successful UPSERT; **no read-back**).
- **error:** no matching registered zone, apex+CNAME, clobber conflict, or provider
  API failure (`status_message` carries detail).

Triggers (request-driven; **no persistent reconciler**):
- **Enable:** **5s bounded wait** for the gateway IP. Resolved → apex-guard + clobber +
  upsert → `ready`. Not resolved → `pending`, **and launch a one-shot deferred retry**:
  a bounded per-record goroutine (~3 min cap) that polls the gateway and writes the
  record the moment the IP lands, then exits. Best-effort per replica (the replica that
  handled enable runs it; idempotent UPSERT ⇒ no coordination; a pod restart falls back
  to reconcile-on-read).
- **Update:** re-upsert with new TTL/proxied/type.
- **Get / Refresh:** re-resolve the gateway IP; if `pending` and now available → write
  it; if `ready` and the IP differs from `resolved_target` → re-upsert. A `Get` calls
  the provider **only** when there's work to do (`pending`, or IP ≠ `resolved_target`);
  a steady `ready` record returns the cached row with no provider call.
- **Delete:** per §3.2.

The synchronous **Enable/Update** path surfaces a provider error to the caller AND
records `error` status. Get-reconcile is best-effort (records status, never fails the
read).

**Accepted limitation:** a gateway-IP change after `ready` isn't auto-propagated until
the record is next read/refreshed (the lazy tradeoff). Refresh is the manual escape hatch.

## 7. Data model & migration (squash)

`domain_dns_records` (1:1 with a domain) — final schema:

| Column | Notes |
|---|---|
| `id` | uuid PK |
| `domain_id` | unique FK → `domains` (cascade) |
| `hosted_zone_id` | FK → `dns_hosted_zones` (**required**; carries credential + provider + zone id) |
| `record_type` | `auto` \| `A` \| `AAAA` \| `CNAME` (default `auto`) |
| `ttl` | int, nullable |
| `proxied` | bool, default false (Cloudflare only) |
| `resolved_target` | text, nullable — last resolved gateway address; also the ownership marker |
| `status` | `pending` \| `ready` \| `error` |
| `status_message` | text |
| `created_by`, `created_at`, `updated_at` | audit |

Changes vs the merged external-dns schema: **`hosted_zone_id` replaces
`provider_credential_id`**; **drop `endpoint_name`**; **no `zone_id`/`zone_name` on the
record** (they live on the zone).

**Migration (decided: squash — the external-dns version is unreleased and un-run):**
- **Edit `000046`** to the final `domain_dns_records` schema (with `hosted_zone_id`).
- **Add a migration for `dns_hosted_zones`** (number it after `000046`).
- **Delete `000047`** (the `active_dns_credential_id` column) and remove the
  `ActiveDNSCredentialID` field from `models.SystemSettings`.

(If any environment turns out to have applied `000046`/`000047`, fall back to forward
migrations instead — the plan assumes squash.)

## 8. Removed / added summary

**Deleted:** `internal/kubernetes/externaldns.go` + `DNSEndpointGVR`;
`internal/services/dns_infra_service.go` (+ test); `DNSActiveCredentialHandler` + its
routes + `Dependencies` fields; the active-credential pieces of `SystemSettingsService`
+ `models.SystemSettings`; migration `000047`; `ErrDNSCredentialIsActive`; the
DNSEndpoint golden tests; the helm `dns.enabled` RBAC block + value; the external-dns
docs/prerequisite.

**Added/changed:** the `DNSClient`/`Record` types + 3 per-provider SDK implementations
(+ SDK deps); `dns_hosted_zones` table + model + repository + service + owner-only
handler/routes; `dns_record_service` reconcile rewritten to the direct-SDK path using
the record's hosted zone; the one-shot deferred-retry goroutine; the Route53 `region`
credential field; frontend Hosted Zones admin page + domain zone picker; rewritten DNS docs.

**Unchanged:** gateway-address resolution (`gateway_address.go`); the record REST routes
(`/projects/:p/domains/:d/dns-record` [+ `/refresh`]); the `dns_provider_credentials`
registry + the provider-aware credential admin page; the `no_tls` un-gating.

## 9. Frontend

- **New "Hosted Zones" admin page** (owner): list / create (zone name + credential
  picker) / delete, with status. Mirrors the DNS Credentials page.
- **Domain DNS section (create wizard + edit settings):** a **hosted-zone picker** —
  the registered zones whose name is a suffix of the domain's hostname, **defaulting to
  the longest-suffix match**, user-overridable. If none match → a hint to register a
  hosted zone for this domain. Replaces the per-record credential picker.
- **Credential admin page:** add the Route53 `region` field (optional).
- **Record controls:** record-type / TTL / proxied. **`proxied` shows only for a
  Cloudflare zone**; **TTL greys out when `proxied` is ticked** (Cloudflare forces auto).
- **Status badge:** `pending` / `ready` / `error`. Read-only detail shows the zone,
  provider, resolved target, TTL/proxied, status.
- **Removed:** the active-credential admin UI + `getActiveCredential`/`setActiveCredential`.

## 10. Error handling & edge cases

- No registered zone suffix-matches the hostname → can't enable (UI gates it; API → 400).
- Apex + CNAME target → `error` (§3.2). A/AAAA at apex is fine.
- Clobber conflict (foreign record on first write) → `error`; no write.
- Provider API failure (auth / rate-limit / network / 5xx) → Enable/Update return the
  error to the caller **and** set `error`; Get-reconcile best-effort; Refresh retries.
- **Record Delete** on provider failure → return the error **and keep the row**
  (retryable). **Domain delete** → best-effort `DeleteRecord` + log (never blocks domain
  teardown).
- Hosted-zone delete while in use → 409; credential delete while a zone/issuer uses it → 409.
- Stale `provider_zone_id` (zone removed at provider) → writes `error`; re-validate via
  the zone Refresh action.
- Retries: SDK defaults; low call volume by design.

## 11. Testing

- **Per-provider `DNSClient`** against a mocked SDK / `httptest` server (no live calls):
  `FindZone` (found/not-found), `GetRecord`, `UpsertRecord` (create + update),
  `DeleteRecord`, Cloudflare `proxied` (TTL coerced to auto), Route53 UPSERT change-batch.
- **Hosted-zone service:** registration validates + caches `provider_zone_id`; status
  mapping; in-use delete guard.
- **`dns_record_service` reconcile** with a fake `DNSClient`: apex guard, clobber-refusal
  (first-write only), upsert→`ready`, update-without-GetRecord, delete→DeleteRecord,
  reconcile-on-read only calls the provider when `pending`/drifted, and the deferred-retry
  writes once the (faked) gateway IP appears.
- **Handlers:** error mapping (no-zone / apex / clobber / provider-error; required
  `hostedZoneId` → 400); owner-only on zone/credential routes; `canManageDomains` on records.
- Remove the DNSEndpoint golden tests. **No live provider calls in tests.**
- **Frontend:** tsc/lint/jest; the hosted-zone picker + zones admin page + status badge.

## 12. Future extensions (tracked, not v1)

1. **Azure DNS** provider (the 4th; `armdns`, service-principal auth, resource-group per
   record — fits the same `DNSClient` seam).
2. **e2e** against a mock-provider HTTP server (or a provider sandbox account).
3. **Automatic gateway-IP-change propagation** (a persistent reconciler / Gateway-status
   informer) — beyond the deferred retry.
4. **ALIAS/ANAME at apex** (per-provider) so a hostname gateway works at a zone apex.
5. **"Test credential"** action; more providers (DigitalOcean, …); ACME DNS01 for the
   non-Cloudflare providers.
