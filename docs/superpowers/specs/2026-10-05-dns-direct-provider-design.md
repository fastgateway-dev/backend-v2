# DNS Management — Direct Provider Redesign

**Status:** Approved (brainstorming) — ready for implementation planning
**Date:** 2026-10-05
**Author:** zufardhiyaulhaq (with Claude)
**Supersedes:** the external-dns-based DNS management (`2026-10-04-dns-management-design.md`), which is merged to `main` but unreleased (no image built, migrations never run on a real DB).

## 1. Overview & intent

Re-architect DNS management so FastGateway writes DNS records **directly via each
provider's official Go SDK** (Cloudflare, AWS Route53, Google Cloud DNS, Azure
DNS), removing the external-dns dependency entirely. FastGateway makes outbound
HTTPS calls to the provider; it no longer creates `DNSEndpoint` custom resources
or relies on an in-cluster controller.

### Why (what the pivot buys)
- **No operator burden:** no external-dns to install, no `DNSEndpoint` CRD, no
  `--policy`/`--txt-owner-id`/`--namespace`/secret coordination, no helm RBAC for it.
- **Multi-provider / multi-account "just works":** each DNS record uses **its own
  credential**, so different domains can target Cloudflare, Route53, Google, or
  Azure — and multiple accounts of the same provider — simultaneously. The
  "one active credential / one external-dns instance per cluster" limitation of
  the external-dns design is **eliminated**.
- **Immediate, reliable create/update/delete:** the API call succeeds or fails;
  status reflects the real provider result, and delete actually deletes.

### Who it's for / success criteria
- An operator registers a DNS provider credential (the same shared credential
  used for ACME DNS01 cert issuance) and, per domain, picks which credential
  writes that domain's record.
- Enabling DNS on a domain creates the record directly in the provider, pointing
  the hostname at the gateway's external address; status is visible.
- Deleting the domain (or the record) removes the record from the provider.
- Works across providers/accounts at once; no external-dns anywhere.

## 2. Scope

### In scope
- One FastGateway-managed DNS record **per domain** (hostname → gateway address),
  type A/AAAA/CNAME auto-chosen from the resolved target.
- Direct record CRUD via the four providers' **official Go SDKs**.
- **Zone auto-discovery** (longest-suffix match among the credential's zones).
- **Per-record credential** selection (shared with cert issuers); **no** global
  "active credential."
- **Refuse-to-clobber** ownership: our `domain_dns_records` row is the ownership
  source of truth; FastGateway only creates/updates/deletes records it created.
- **Lazy, request-driven** reconciliation (resolve-on-enable with a bounded wait,
  reconcile-on-read, explicit Refresh). No background reconciler.
- Reuse of the existing data model, REST API, frontend UI, credential model, and
  the `DNSProvider` abstraction (refocused).

### Out of scope (explicit)
- **external-dns** and the `DNSEndpoint` CR — removed.
- The **"active DNS credential"** concept, its system setting, secret rendering,
  and endpoints — removed.
- **Automatic gateway-IP-change propagation** — a changed LB address is picked up
  on the next read/refresh, not continuously (the accepted lazy tradeoff).
- **More than one managed record per domain**, managing zones themselves, and a
  background reconciler — not in v1.
- **e2e** (needs a real provider account or a mock-provider server) — tracked
  follow-up, not a v1 CI gate.
- **ACME DNS01 for non-Cloudflare providers** — unchanged; still Cloudflare-only
  (guarded). DNS management supports all four; the shared credential is general.

## 3. Architecture & write path

### 3.1 `DNSProvider` refocused + a new `DNSClient`
The provider abstraction moves from "describe external-dns config" to "talk to
the provider." Keep `Type`/`RequiredFields`/`Validate`; **drop**
`RenderSecret`/`ExternalDNSFlag`/`SecretName`; **add** a client factory.

```go
type DNSProvider interface {
    Type() string
    RequiredFields() []string
    Validate(cred map[string]string) error
    NewClient(cred map[string]string) (DNSClient, error) // SDK-backed, per credential
}

type DNSClient interface {
    // FindZone returns the hosted zone whose name is the longest suffix of
    // hostname among the zones this credential can access. found=false => no zone.
    FindZone(ctx context.Context, hostname string) (zone Zone, found bool, err error)
    // GetRecord returns the existing record for (name,type) in the zone, if any.
    GetRecord(ctx context.Context, zone Zone, name, recordType string) (rec Record, found bool, err error)
    UpsertRecord(ctx context.Context, zone Zone, r Record) error
    DeleteRecord(ctx context.Context, zone Zone, name, recordType string) error
}

type Zone struct { ID, Name string }
type Record struct { Name, Type, Target string; TTL *int; Proxied bool }
```

Each provider implements `DNSClient` with its official SDK, in
`internal/dnsprovider/{cloudflare,route53,google,azure}.go` (extending the
existing files). SDKs: `github.com/cloudflare/cloudflare-go`,
`github.com/aws/aws-sdk-go-v2/service/route53`,
`google.golang.org/api/dns/v1`, and the Azure DNS SDK
(`github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns`).

### 3.2 The write path (replaces "build DNSEndpoint + apply")
In `dns_record_service`'s reconcile, for an enabled record with a resolved
gateway address:
1. `providerType, creds := DNSCredentialService.DecryptedCredentials(record.ProviderCredentialID)`
2. `prov := dnsprovider.Get(providerType); client := prov.NewClient(creds)`
3. `zone, found := client.FindZone(hostname)` — not found → `error`.
4. **Clobber check.** The ownership signal is **whether FastGateway has ever
   successfully written this record**, tracked by a non-empty `resolved_target`
   on the row (we set `resolved_target` only after a successful upsert). So:
   `existing, found := client.GetRecord(zone, hostname, type)`.
   - If `found` **and** `resolved_target == ""` (we have never written it) → the
     record is **foreign** → `error` ("a record already exists for `<host>` not
     managed by FastGateway"); do not touch it.
   - If `found` and `resolved_target != ""` → it is **ours** → proceed to update.
   - If not `found` → create.
5. `client.UpsertRecord(zone, Record{hostname, type, target, ttl, proxied})` →
   `ready`; cache `zone.ID`/`zone.Name` and set `resolved_target` on the row (the
   latter also marks the record as FastGateway-owned for future reconciles).

**Delete:** build the client → `client.DeleteRecord(zone, hostname, type)` — our
record only.

### 3.3 No k8s writes
The k8s layer loses `internal/kubernetes/externaldns.go` and `DNSEndpointGVR`.
DNS touches the cluster only to **read Gateway `status.addresses`** (already
granted). The backend's new dependency is **outbound HTTPS to the provider**.

## 4. Credential model (no active credential)

- The shared `dns_provider_credentials` table + `DNSCredentialService` is reused
  **as-is** — the same registry that backs ACME DNS01 cert issuers. A credential
  registered for certs is selectable for DNS records and vice versa.
- `domain_dns_records.provider_credential_id` is the real, **per-record** choice
  (required on enable). Different domains may use different credentials/providers
  simultaneously.
- **Removed:** `active_dns_credential_id` (system setting + column), `DNSInfraService`
  (secret rendering + active getter/setter), `SystemSettingsService.{Get,Set}ActiveDNSCredentialID`,
  `DNSActiveCredentialHandler`, the `/dns/settings/active-credential` routes, and
  the `ErrDNSCredentialIsActive` guard.
- **Validation:** `providerCredentialId` must reference an existing credential
  whose provider is supported (cloudflare/route53/google/azure). No "must equal
  active" rule.
- **Credential in-use guard** (`DNSCredentialService.Delete`): keep the ACME-issuer
  check and the `CountByCredential` (domain-records) check → 409. Drop the
  active-credential check.
- Credentials are decrypted in-process at call time (`DecryptedCredentials`), used
  only to build the SDK client — never written to a k8s Secret, never logged.

## 5. Zone discovery & record mechanics

- **Auto-discovery:** `FindZone(hostname)` lists the credential account's hosted
  zones (CF list zones / Route53 `ListHostedZones` / Google `managedZones.list` /
  Azure zones list) and picks the **longest zone suffix** of the hostname. No
  manual zone selection. No matching zone → `error` with a clear message.
- **Record:** name = the domain's hostname; type/target from the existing
  `recordTypeForAddress` + gateway-address resolution (A/AAAA/CNAME); TTL per-record
  (provider default if unset); **Cloudflare `proxied`** set directly on the record
  via the SDK, ignored for the other three providers.
- **Zone caching:** store the resolved `zone_id`/`zone_name` on the row so routine
  reconciles skip the list-zones call; re-discover only when the cached zone can't
  be found.

## 6. Status & lazy reconciliation

Status enum: **`pending` → `ready` → `error`** (drop `syncing` — writes are now
synchronous within the request).
- **pending:** enabled, gateway LB address not resolved yet → nothing written.
- **ready:** the record is written/confirmed in the provider.
- **error:** no hosted zone, clobber conflict, or provider API failure
  (`status_message` carries detail).

Reconcile triggers (request-driven):
- **Enable:** resolve the gateway IP with a **bounded wait** (poll
  `status.addresses` up to ~15s, best-effort). Resolved → FindZone + clobber-check
  + upsert → `ready`; else → `pending`.
- **Update:** re-upsert with new TTL/proxied/type.
- **Get / Refresh:** re-resolve the gateway IP; if `pending` and the IP is now
  available → write it; if `ready` and the IP differs from `resolved_target` →
  re-upsert. **A `Get` calls the provider ONLY when there's work to do** (status
  `pending`, or resolved IP ≠ `resolved_target`); a steady `ready` record returns
  the cached row with no provider call, keeping reads cheap and under rate limits.
- **Delete:** `DeleteRecord` (our record only).

Reconcile is best-effort on read (records status, never fails the read), mirroring
managed certs. The synchronous **Enable/Update** path, by contrast, surfaces a
provider error to the caller (so the UI shows why) AND records `error` status;
Refresh re-attempts.

**Accepted limitation:** a gateway-IP change isn't auto-propagated until the record
is next read/refreshed. Acceptable for stable clusters; Refresh is the manual
escape hatch.

## 7. Data model & migration (squash)

`domain_dns_records` (1:1 with a domain) — final schema:

| Column | Notes |
|---|---|
| `id` | uuid PK |
| `domain_id` | unique FK → `domains` (cascade) |
| `provider_credential_id` | FK → `dns_provider_credentials` (**required**) |
| `record_type` | `auto` \| `A` \| `AAAA` \| `CNAME` (default `auto`) |
| `ttl` | int, nullable |
| `proxied` | bool, default false |
| `zone_id` | text, nullable — cached resolved zone id |
| `zone_name` | text, nullable — cached resolved zone name |
| `resolved_target` | text, nullable — last resolved gateway address |
| `status` | `pending` \| `ready` \| `error` |
| `status_message` | text |
| `created_by`, `created_at`, `updated_at` | audit |

Changes vs the merged external-dns schema: **drop `endpoint_name`**, **add
`zone_id`/`zone_name`**.

**Migration strategy (decided): squash.** The external-dns version is merged but
unreleased and never run on a real DB, so:
- **Edit `000046_add_domain_dns_records.{up,down}.sql` in place** to the final
  schema above (no `endpoint_name`; with `zone_id`/`zone_name`).
- **Delete migration `000047`** (the `active_dns_credential_id` column) entirely,
  and remove the `ActiveDNSCredentialID` field from `models.SystemSettings`.

(If, before implementation, any environment turns out to have applied `000046`/
`000047`, fall back to forward migrations instead — but the plan assumes squash.)

## 8. Removed / added summary

**Deleted:** `internal/kubernetes/externaldns.go` + `DNSEndpointGVR`;
`internal/services/dns_infra_service.go` (+ test); `DNSActiveCredentialHandler` +
its routes + `Dependencies` fields; the active-credential pieces of
`SystemSettingsService` + `models.SystemSettings`; migration `000047`; the
`ErrDNSCredentialIsActive` guard; the DNSEndpoint golden tests; the helm
`dns.enabled` RBAC block + value; the external-dns docs/prerequisite.

**Added/changed:** the `DNSClient`/`Record`/`Zone` types + per-provider SDK
implementations + SDK deps; `dns_record_service` reconcile rewritten to the
direct-SDK path; `zone_id`/`zone_name` on the row; frontend credential picker;
rewritten DNS docs.

**Unchanged:** gateway-address resolution (`gateway_address.go`); the REST API
routes for the record (`/projects/:p/domains/:d/dns-record` [+ `/refresh`]); the
`DNSRecordInput` shape (now `providerCredentialId` required/used); the
`dns_provider_credentials` registry + the provider-aware credential admin page;
the `no_tls` un-gating.

## 9. Frontend

- **Add** a per-domain **DNS credential picker** (`<Select>` over
  `dnsCredentialsApi.list()`), required when enabling DNS — on the create wizard
  and the edit-settings DNS section.
- **Remove** the active-credential admin UI and the
  `getActiveCredential`/`setActiveCredential` API methods. Keep the DNS Credentials
  admin page (shared registry).
- **Status badge:** `pending`/`ready`/`error`.
- **Read-only detail section:** show the record's credential **name** + provider,
  resolved **zone**, target, TTL/proxied, status (this also resolves the earlier
  "raw credential UUID shown" nit).
- Unchanged: record-type/ttl/proxied controls, Enable/Save/Refresh/Delete.

## 10. Error handling & edge cases

- No hosted zone found → `error` + message naming host/account.
- Clobber conflict (foreign record exists) → `error` + message; no write.
- Provider API failure (auth / rate-limit / network / 5xx) → Enable/Update return
  the error to the caller **and** set `error` status; Refresh re-attempts;
  Get-reconcile stays best-effort.
- Domain delete → best-effort `DeleteRecord` (real outbound call); on failure log +
  proceed (row cascades). A rare provider-delete failure can orphan the provider
  record — future retry could harden this.
- Credential deleted while in use → `CountByCredential` guard → 409.
- Stale zone cache → `FindZone` re-discovers; still none → `error`.
- Low call volume by design (request-driven + reconcile-only-when-needed).

## 11. Testing

- **Per-provider `DNSClient`** against a mocked SDK / `httptest` server (no live
  calls): zone discovery (longest-suffix + no-match), GetRecord, Upsert
  (create + update), Delete, Cloudflare `proxied`.
- **`dns_record_service` reconcile** with an in-package **fake `DNSClient`**: the
  FindZone→clobber→upsert sequence, status transitions, clobber-refusal (foreign →
  `error`, no upsert), delete→DeleteRecord, and that reconcile-on-read only calls
  the provider when `pending`/drifted.
- **Handlers:** error mapping (no-zone / clobber / provider-error; required
  `providerCredentialId` → 400).
- Remove the DNSEndpoint golden tests.
- **Frontend:** tsc/lint/jest; the credential picker + status badge.
- **No live provider calls in tests.**

## 12. Future extensions (tracked, not v1)

1. **e2e** against a mock-provider HTTP server (or a provider sandbox account).
2. **Automatic gateway-IP-change propagation** (a background reconciler or a
   Gateway-status informer) — the piece the lazy model trades away.
3. **Provider-delete retry** so a transient failure on domain delete can't orphan
   a provider record.
4. More providers (DigitalOcean, etc.) via new `DNSClient` implementations.
5. ACME DNS01 for the non-Cloudflare providers (separate, uses the same
   credential registry).
