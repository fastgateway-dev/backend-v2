# DNS Management — Design

**Status:** Approved (brainstorming) — ready for implementation planning
**Date:** 2026-10-04
**Author:** zufardhiyaulhaq (with Claude)

## 1. Overview & intent

Add a first-class **DNS management** capability to FastGateway, parallel to
certificate management. A domain can have **one FastGateway-managed DNS
record** that points its hostname at the gateway's external address. When a
domain is created, the user can opt in to having this record created
**automatically**; it is lifecycle-managed (updated as needed, deleted with
the domain) and its live status is visible in the UI.

The actual writing of DNS records is **delegated to external-dns**, exactly as
certificate issuance is delegated to cert-manager and traffic to Envoy
Gateway. FastGateway never calls a DNS provider's API directly.

### Who it's for / success criteria
- A domain owner creating a domain can tick "create DNS automatically", pick a
  DNS provider credential, and have the hostname resolve to the gateway with
  no manual DNS work.
- The DNS record's status (pending / syncing / ready / error) is visible next
  to the domain's TLS certificate.
- Removing the domain (or disabling the record) removes the record from the
  provider.
- Works with the major external-dns providers (Cloudflare, AWS Route53, Google
  Cloud DNS, Azure DNS), one active provider per cluster in v1.

## 2. Scope

### In scope
- One managed DNS record **per domain** (the `hostname → gateway address`
  record), type A/AAAA/CNAME chosen automatically from the resolved target.
- Opt-in auto-create at domain creation; enable/edit/refresh/delete later.
- Delegation to external-dns via a `DNSEndpoint` custom resource.
- Resolving the gateway's external address from the Gateway's `status`.
- Multi-provider credential model + secret rendering for Cloudflare, Route53,
  Google Cloud DNS, Azure DNS.
- Live status surfaced on read (no background reconciler).
- Helm chart RBAC gated behind a new `dns.enabled` value.

### Out of scope (explicit, with rationale)
- **Full/arbitrary DNS record CRUD** (a DNS control panel). The user chose
  "only FastGateway's domain records." Extra records (`www` CNAME, TXT, MX,
  etc.) are not managed.
- **More than one record per domain.**
- **Managing DNS zones** themselves in the provider.
- **Multiple DNS providers / multiple credentials active simultaneously.** v1
  runs one external-dns instance bound to one credential, so one provider is
  active per cluster. Simultaneous multi-provider is a future extension (one
  external-dns per provider, filtered by annotation).
- **Expanding the cert-manager DNS01 solver** beyond Cloudflare. The shared
  credential model gets all four providers; teaching the ACME DNS01 solver the
  other three reuses the same provider abstraction but is a tracked follow-up,
  not part of this feature.
- **Deploying external-dns from the chart.** It is an operator-installed
  prerequisite (like cert-manager / Envoy Gateway); bundling it as a subchart
  is a later option.

## 3. Architecture

### 3.1 Mirror managed certificates
A managed certificate is a first-class object tied to a project whose issuance
is delegated to cert-manager (a `Certificate` CR) and whose status is read live
from Kubernetes on access. A managed DNS record uses the same shape:

| Managed certificate | Managed DNS record |
|---|---|
| tied to project | tied **1:1 to a domain** |
| delegates to cert-manager | delegates to **external-dns** |
| `Certificate` CR (`cert-<id>`) | `DNSEndpoint` CR (`dns-<id>`) |
| status read live from cert-manager conditions | status read live from the `DNSEndpoint` + Gateway status |
| no backend reconciler; status-on-read | no backend reconciler; status-on-read |

### 3.2 Write path
For each domain with DNS enabled, FastGateway creates **one `DNSEndpoint`**
(`externaldns.k8s.io/v1alpha1`) in `fastgateway-system`:

- `endpoints[0].dnsName` = the domain's hostname
- `endpoints[0].recordType` = resolved type (see §3.4)
- `endpoints[0].targets` = the resolved gateway address(es)
- `endpoints[0].recordTTL` = configured TTL (optional)
- Cloudflare "proxied" is expressed via the external-dns annotation
  `external-dns.alpha.kubernetes.io/cloudflare-proxied: "true"` on the
  `DNSEndpoint` when the provider is Cloudflare and proxied is set.

external-dns (`--source=crd`, `--policy=sync`) reconciles the `DNSEndpoint` to
the provider and removes the record when the `DNSEndpoint` is deleted.

**Why `DNSEndpoint` and not the `gateway-httproute` source:** the gateway-route
source only emits a record once a route with that hostname exists and gives no
first-class per-domain object. `DNSEndpoint` lets FastGateway create the record
at domain-creation time (before any routes) and own it as a first-class,
lifecycle-managed resource.

### 3.3 Target resolution (the gateway-address gap)
The record's target is the domain's Gateway external address, read from the
Gateway's `status.addresses[]` (`{type: IPAddress|Hostname, value}`). Because
the load-balancer address is assigned asynchronously and there is **no backend
reconciler**, resolution is **lazy**, on the same cadence as cert status:

- **On domain apply** (`applyGateway`, which already runs on domain create and
  update): attempt to read the Gateway address; if present, create/update the
  `DNSEndpoint`.
- **On read** of the DNS record (GET): re-read the Gateway address and the
  `DNSEndpoint`; if the address is now present (or changed) and the record is
  enabled, reconcile the `DNSEndpoint` and update `resolved_target`.
- **On explicit refresh** (`POST …/dns-record/refresh`): force the above.

This is honest about reality: a correct record cannot exist before the LB
address does, so a brief **Pending** state is expected and shown.

### 3.4 Record type selection (`auto`)
- Gateway address is an IPv4 → `A`
- Gateway address is an IPv6 → `AAAA`
- Gateway address is a hostname → `CNAME`

`record_type` may be forced to `A`/`CNAME` by the user; `auto` (default) uses
the rule above. A forced type that is incompatible with the resolved address
(e.g. `CNAME` forced but the Gateway exposes an IP) yields an `error` status
with a clear message.

### 3.5 Status model
`pending` → `syncing` → `ready`, with `error` reachable from any state:
- **pending** — enabled, but the Gateway address is not yet available, so no
  `DNSEndpoint` has been created (or it has no target yet).
- **syncing** — the `DNSEndpoint` exists with a target; external-dns has not
  yet confirmed the record is live.
- **ready** — external-dns reports the record applied (via the `DNSEndpoint`'s
  external-dns status / TXT registry; see §8 for the exact readiness signal).
- **error** — validation failure, credential/provider mismatch, forced-type
  conflict, or external-dns reported a failure. `status_message` carries detail.

## 4. Provider abstraction (multi-provider)

The `DNSEndpoint` CR and all record/domain code are **provider-agnostic**. Only
credential validation, secret rendering, and the external-dns `--provider` flag
vary. These are isolated behind one interface — the same extensibility point as
the existing `dns01Solver(providerType, …)` switch in `certmanager.go`, promoted
to a proper interface:

```go
type DNSProvider interface {
    Type() string                 // "cloudflare" | "route53" | "google" | "azure"
    RequiredFields() []string     // drives validation + the credential UI form
    Validate(cred models.DNSCredentialData) error
    RenderSecret(cred models.DNSCredentialData) (name string, data map[string][]byte)
    ExternalDNSFlag() string      // the --provider=<x> value
}
```

One small implementation per provider, registered in a map keyed by type.

### 4.1 Credential schemas (per provider)
Stored in the existing encrypted `DNSCredentialData` JSONB map (each value
individually encrypted by the service layer, as today):

| Provider | `provider_type` | Required fields |
|---|---|---|
| Cloudflare | `cloudflare` | `apiToken` |
| AWS Route53 | `route53` | `accessKeyId`, `secretAccessKey` (optional `region`) |
| Google Cloud DNS | `google` | `serviceAccountKey` (JSON), `project` |
| Azure DNS | `azure` | `tenantId`, `subscriptionId`, `resourceGroup`, `clientId`, `clientSecret` |

`supportedDNSProviders` expands from `{cloudflare}` to all four. The backend
validates required fields per type on credential create/update.

### 4.2 Secret rendering (per provider)
`RenderSecret` maps a credential to the Secret shape external-dns expects:
- Cloudflare → key `apiToken` (env `CF_API_TOKEN`)
- Route53 → `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY`
- Google → `credentials.json` file
- Azure → `azure.json` file

Rendered into a documented Secret in `fastgateway-system`, reusing the same
approach FastGateway already uses to render the Cloudflare token Secret for the
cert-manager ACME DNS01 solver.

### 4.3 Active DNS credential (system setting) — resolves the "which provider is external-dns running" question
The backend cannot inspect external-dns's `--provider` flag, so the link
between "the credential FastGateway uses" and "the provider external-dns runs"
is made explicit by an **owner-configured system setting**: the **active DNS
credential**.

- The owner designates one `DNSProviderCredential` as active
  (`dns.activeCredentialId`, stored with the other owner/system settings).
- FastGateway renders **that credential's** provider Secret (§4.2) into
  `fastgateway-system`; the operator configures external-dns's
  `--provider`/credential to match it. This is the single source of truth for
  "the provider external-dns runs".
- In v1 (single provider/credential), a domain DNS record's
  `provider_credential_id` **defaults to and must equal the active credential**;
  a request naming a different credential is rejected with a clear message
  (`400`). The column is retained for the future multi-credential extension,
  where it will select among several active credentials.
- Changing the active credential re-renders the Secret; the operator updates
  external-dns to match. (Switching providers is an operator action, not a
  per-record one.)

## 5. Data model

New table `domain_dns_records`, **1:1 with a domain**:

| Column | Type | Notes |
|---|---|---|
| `id` | uuid PK | |
| `domain_id` | uuid, **unique** FK → `domains` | cascade delete |
| `provider_credential_id` | uuid FK → `dns_provider_credentials` | which provider account |
| `record_type` | text | `auto` \| `A` \| `AAAA` \| `CNAME` (default `auto`) |
| `ttl` | int, nullable | null → provider default |
| `proxied` | bool, default false | Cloudflare orange-cloud |
| `resolved_target` | text, nullable | last resolved LB address, for display + drift detection |
| `status` | text | `pending` \| `syncing` \| `ready` \| `error` |
| `status_message` | text | human-readable detail |
| `endpoint_name` | text | the `DNSEndpoint` CR name, `dns-<id>` |
| `created_by` | uuid | |
| `created_at`, `updated_at` | timestamptz | |

**Not stored:** the hostname (always the domain's hostname — single source of
truth) and the live target (resolved each apply/read; `resolved_target` is a
display cache only).

Migration: `0000NN_add_domain_dns_records.{up,down}.sql` plus the
`supportedDNSProviders` expansion (code-level, no migration).

## 6. API

REST, scoped under the domain, mirroring managed certificates. All require
`canManageDomains` on the project.

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/projects/:pid/domains/:did/dns-record` | the record + live status (404 if none) |
| `POST` | `/projects/:pid/domains/:did/dns-record` | enable/create — body `{ providerCredentialId, recordType?, ttl?, proxied? }` |
| `PUT` | `/projects/:pid/domains/:did/dns-record` | update settings |
| `DELETE` | `/projects/:pid/domains/:did/dns-record` | disable — deletes the `DNSEndpoint` → external-dns removes the record |
| `POST` | `/projects/:pid/domains/:did/dns-record/refresh` | force re-resolve target + re-apply |

Provider credentials continue to use the existing owner-managed
`dns-credentials` endpoints; the record flow consumes that list.

**Auto-create at domain creation:** `CreateDomainInput` gains an optional
block:
```json
"dns": { "enabled": true, "providerCredentialId": "...",
         "recordType": "auto", "ttl": 300, "proxied": false }
```
When `enabled`, after the Gateway applies, FastGateway creates the record row
and attempts the `DNSEndpoint` (Pending until the LB address appears).

### Validation
- `providerCredentialId` is optional; it defaults to the **active DNS
  credential** (§4.3). If supplied in v1 it must equal the active credential —
  any other value is rejected with 400.
- If no active DNS credential is configured, enabling DNS → 400 with a message
  pointing the owner to set one.
- Forced `record_type` incompatible with the resolved address → `error` status
  (not a request-time 400, since the address is resolved asynchronously).

## 7. external-dns configuration, RBAC & chart

### 7.1 external-dns (operator prerequisite)
Documented config (in the site docs + chart README):
- `--source=crd` watching `DNSEndpoint`s in `fastgateway-system`
- `--provider=<cloudflare|aws|google|azure>` (one per instance in v1)
- credential from the Secret FastGateway renders (§4.2)
- `--policy=sync` (so `DNSEndpoint` deletion deletes the record)
- `--txt-owner-id=fastgateway` + TXT registry (only touches records it owns)
- optional `--domain-filter` to restrict zones

### 7.2 RBAC (helm chart `rbac.yaml`)
Gated behind a new `dns.enabled` chart value:
- create/update/delete/get/list on `DNSEndpoint` (`externaldns.k8s.io`)
- create/update/delete/get on the rendered provider Secret in
  `fastgateway-system`
- read `Gateway.status` (extend the existing Gateway read grant)

Clusters with `dns.enabled=false` get none of these extra grants.

### 7.3 Chart
external-dns stays an installed prerequisite; `dns.enabled` only toggles RBAC.
The `DNSEndpoint` CRD is installed with external-dns (its `crd` source).

## 8. Status & readiness details

external-dns does not write status back onto the `DNSEndpoint` by default; it
records ownership in a **TXT registry** record. The readiness signal is
therefore derived, not a single condition:
- **syncing → ready** when the expected record is observed as owned by
  FastGateway's `--txt-owner-id`. v1 derives readiness from the `DNSEndpoint`
  having a target plus elapsed reconcile time; a stricter check (querying the
  provider or the TXT registry) is a possible enhancement.
- This is the one area with external-dns-version sensitivity; the implementation
  plan should pin the external-dns behavior it relies on and, if needed, run a
  small spike to confirm the exact readiness signal on the target version.

## 9. UX

Consistent with the TLS Certificate sections already shipped.

- **Domain creation wizard:** an optional **DNS** block with a toggle
  "Automatically create the DNS record for this domain" (**off by default**).
  When on: pick a provider credential, optional record type (Auto/A/CNAME),
  TTL, and Cloudflare "Proxied". Helper text names the hostname and that it
  will point at the gateway address once assigned.
- **Domain detail → Settings tab (read-only):** a **DNS Record** accordion
  section (next to TLS Certificate) showing hostname, type, target
  (`resolved_target`), a status badge (same mapping as certs), provider, TTL,
  proxied, and `status_message` on error.
- **Domain → Edit Settings:** the same section, editable, with its own action
  buttons (Enable/Create, Edit, Refresh, Delete) — separate from "Save
  Settings", exactly like the TLS Certificate section.
- **Empty/disabled state:** "Not managed by FastGateway — point `<hostname>` at
  `<gateway address>` yourself," surfacing the resolved gateway address so
  manual setup is easy.
- **Credential create/edit form** becomes provider-aware (fields switch by
  provider type). The record flow is provider-agnostic.

## 10. Error handling & edge cases

- **No gateway address yet** → `pending`, no `DNSEndpoint`; resolved later.
- **Gateway address changes** → next apply/read updates `resolved_target` and
  the `DNSEndpoint`; external-dns re-syncs.
- **Credential/provider mismatch** → 400 on create/update.
- **Forced record type vs resolved address conflict** → `error` status.
- **external-dns not installed** → `DNSEndpoint` is created but nothing
  reconciles it; record stays `syncing`. The UI shows a hint that external-dns
  must be installed and `dns.enabled` set. (We do not fail domain creation.)
- **Domain deleted** → cascade removes the record row; FastGateway deletes the
  `DNSEndpoint` first so external-dns removes the provider record.
- **Credential deleted while in use** → block deletion (in-use guard, mirroring
  the certificate in-use delete guard), or surface an error on the dependent
  records.
- **DNS disabled on a domain** → delete the `DNSEndpoint`; record row removed.

## 11. Testing strategy

- **Unit:** provider abstraction (`Validate`, `RenderSecret`, `ExternalDNSFlag`)
  per provider; record-type `auto` selection (IPv4/IPv6/hostname); status
  transitions; credential/provider mismatch validation; in-use delete guard.
- **Service:** `DNSEndpoint` build from a domain + resolved address (golden
  fixture, like the cert-manager golden tests); create/update/delete record
  wiring to the k8s client mock; auto-create path from `CreateDomainInput`;
  lazy resolution on read.
- **Handler:** the five endpoints + the domain-create `dns` block (happy paths,
  permission checks, 404/400 cases).
- **Golden fixtures:** `DNSEndpoint` YAML per provider/record-type, and the
  rendered provider Secret per provider.
- **Frontend:** provider-aware credential form; the DNS section (view + edit)
  states; auto-create toggle in the creation wizard. Typecheck + lint clean.
- **No live provider calls in tests** — external-dns owns provider I/O; tests
  assert the CRs/Secrets FastGateway produces.

## 12. Future extensions (tracked, not in v1)

1. Simultaneous multiple providers / credentials (one external-dns per
   provider, filtered by annotation or namespace).
2. Expand the cert-manager DNS01 solver to all four providers using this same
   `DNSProvider` abstraction.
3. Additional providers (DigitalOcean, etc.) via new `DNSProvider`
   implementations.
4. Optional stricter readiness check (query the provider / TXT registry).
5. Optionally bundle external-dns as a chart subchart.
6. Broader DNS record management (additional records per domain) if the scope
   decision is revisited.
