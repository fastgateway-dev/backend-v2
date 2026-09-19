# Managed Certificates — Design

**Status:** Draft for review
**Date:** 2026-09-19
**Component:** backend-v2 (FastGateway control plane)

**Goal:** Let FastGateway manage TLS certificates itself — issuing from a private
CA or a public ACME CA (Let's Encrypt, ZeroSSL) via cert-manager — instead of
requiring the user to pre-create a Kubernetes TLS Secret and reference it by name.
Issued certificates are synced from a central control cluster to the managed
cluster that serves them.

**Architecture (one paragraph):** cert-manager runs in a single **control cluster**
(the cluster the FastGateway backend is installed in, accessed via its in-cluster
ServiceAccount). All issuers, ACME accounts, DNS-provider credentials, and leaf
`Certificate`s live there — it is the single source of truth for key material.
The backend creates cert-manager CRDs there, cert-manager issues, and a
leader-elected controller syncs each resulting `kubernetes.io/tls` Secret to the
tenant cluster (a Project) that uses it. Tenant clusters never run cert-manager
and never hold CA/ACME/DNS keys — only leaf Secrets.

**Tech stack:** Go 1.25, GORM, Gin, cert-manager (`cert-manager.io/v1`), Kubernetes
Gateway API + Envoy Gateway, `internal/crypto` for at-rest encryption, client-go
informer + workqueue, Kubernetes Lease (or Postgres advisory lock) for leader
election, Postgres `LISTEN/NOTIFY` for cross-replica nudge.

---

## Global Constraints

- **No private keys in the database, ever.** Leaf/CA/ACME-account keys live only in
  Kubernetes Secrets (control cluster + tenant clusters). The DB holds metadata,
  fingerprints, expiry, and status.
- **The only new secret at rest in the backend DB** is DNS-provider credentials and
  ACME EAB secrets, encrypted with the existing `internal/crypto` (same pattern as
  `Project.K8sTokenEncrypted`).
- **The backend must run in-cluster** in the control cluster (it uses
  `rest.InClusterConfig()` for control-cluster access). Local/dev/test runs mock the
  control-cluster client.
- **cert-manager is a prerequisite** of the control cluster (documented install or a
  Helm chart dependency).
- **Public ACME is DNS-01 only** (central issuance can't solve HTTP-01, which needs
  the serving gateway). DNS-01 requires a DNS-provider credential.
- **Additive, no forced migration:** the existing user-supplied `Domain.TLSSecretName`
  (bring-your-own) stays fully supported. A domain is TLS'd by *either* a managed
  certificate *or* a legacy secret name.

---

## 1. Background & problem

Today a `Domain` stores a user-supplied `TLSSecretName`/`TLSSecretNamespace`
(`internal/models/domain.go:37-38`); the Secret must already exist in the target
cluster, and the backend only *references* it in the Gateway listener
(`internal/kubernetes/gateway.go:50-73`). TLS *server-cert* Secrets are the one
Secret type the backend never creates — it only lists them
(`ListTLSSecrets`). There is no cert issuance, no renewal, and no cross-cluster
sync. cert-manager is not used anywhere (greenfield).

A `Project` **is** a cluster (`internal/models/project.go:10`); the backend holds
per-project credentials and talks to each cluster via `cluster.Client` keyed by
`projectID`. A hostname served by multiple clusters is modeled as multiple `Domain`
rows (one per project).

This feature makes FastGateway the certificate manager: create CAs / register public
CAs, issue certs, and get them onto the cluster that serves each domain — with
automatic renewal.

## 2. Architecture & topology

Two planes:

- **Control plane — one cluster (the backend's own).** Runs cert-manager. Holds all
  `SelfSigned`/`CA`/`ACME` Issuers, ACME account keys, DNS-01 solver Secrets, and leaf
  `Certificate`s + their TLS Secrets. **Single source of truth for all key material.**
  The backend reaches it via `rest.InClusterConfig()`; the Helm chart provisions a
  ServiceAccount + (Cluster)Role granting rights over `cert-manager.io` CRDs and
  Secrets, scoped to the control namespace where possible.
- **Data plane — N tenant clusters (Projects).** Receive only *leaf* TLS Secrets,
  pushed by the backend. No cert-manager, no CA/ACME/DNS keys. Gateway listeners
  reference the pushed Secret by name.

Two cluster-access paths result: **control cluster = in-cluster SA (no Project row)**;
**tenant clusters = Projects with stored creds** (unchanged).

**Data flow (one cert):** backend writes a `Certificate` CRD in the control cluster →
cert-manager issues → TLS Secret appears in the control cluster → the leader-elected
controller reads it and upserts it into the cert's tenant cluster → the tenant Gateway
serves it. Renewal follows the same path (cert-manager rotates the source Secret; the
controller notices the new fingerprint and re-pushes).

## 3. Data model

All new tables. Private keys never appear here.

- **`CertificateIssuer`** — polymorphic (type discriminator + JSONB config, mirroring
  `DomainSettings`). Platform-global (no `ProjectID`).
  - `type ∈ {self_signed_ca, acme}`
  - `self_signed_ca`: display name; mapped cert-manager `CA` Issuer name; CA fingerprint/expiry.
  - `acme`: display name; ACME server URL (LE prod/staging, ZeroSSL); account email;
    optional EAB (ZeroSSL); FK to a `DNSProviderCredential`; mapped `ClusterIssuer` name.
- **`DNSProviderCredential`** — **standalone, first-class, knows nothing about certs**
  (so a future DNS-management feature reuses it). `provider_type` (cloudflare, route53,
  …), encrypted credential blob (`internal/crypto`), display name; room to grow (zone
  scoping) later. Platform-global. Referenced by `acme` issuers via nullable FK;
  deleting one still referenced is blocked.
- **`IssuerProjectGrant`** — issuer → project allowlist. An issuer is invisible to a
  project until granted. `(issuer_id, project_id)`. Owner-managed.
- **`ManagedCertificate`** — the leaf. **Project-scoped** (`ProjectID`, like `Domain`).
  - `issuer_id` FK, `usage ∈ {server, client}`, `dnsNames []string` (or subject for
    client certs), control-cluster TLS Secret name, fingerprint, `notAfter`, status
    (`pending/ready/error`), last message. **No key material.**
  - Approval-gated on create (see §8).
- **`CertificateDistribution`** — sync state for the cert's single target cluster:
  last-pushed fingerprint, status, timestamp. (1:1 with `ManagedCertificate` given the
  project-scoped model, but kept as its own row for status/observability and future
  multi-target flexibility.)

**Domain / Client wiring:**
- `Domain` gains a nullable `ManagedCertificateID` FK (server cert). If set, the backend
  ensures the leaf Secret is present in that domain's cluster and points the listener at
  it; legacy `TLSSecretName` remains the alternative.
- `Client` (mTLS) can reference a `ManagedCertificate` of `usage=client` for its client
  identity, issued from the private CA.

## 4. The three flows (cert-manager CRDs in the control cluster)

All `cert-manager.io/v1`. The backend gains these GVRs.

**Flow 1 — Create self-signed root CA (owner):** `SelfSigned` Issuer → CA `Certificate`
(`isCA: true`, commonName, duration) → CA key+cert Secret (`ca-<id>`) → `CA` Issuer
referencing it. Records `CertificateIssuer{type: self_signed_ca}`.

**Flow 2 — Register a public CA (owner):** add a `DNSProviderCredential` (its own
lifecycle) → materialize the DNS-01 solver Secret → create an `ACME` ClusterIssuer
(server + email + `privateKeySecretRef` (cert-manager stores the account key) + `dns01`
solver bound to the provider Secret). Records `CertificateIssuer{type: acme}`. The
issuer is **per-account**; hostnames bind at cert-creation.

**Flow 3 — Create a certificate (project team, approval-gated):** pick a *granted*
issuer, enter `dnsNames`/subject, usage (server/client), key params. Backend creates a
leaf `Certificate` (`issuerRef`, `dnsNames`, `secretName: cert-<id>`) in the control
cluster. cert-manager issues (CA instant; ACME DNS-01 seconds–minutes) → TLS Secret in
the control cluster. Backend drives `ManagedCertificate.status` from the `Certificate`'s
`Ready` condition (reason/message surfaced on failure).

**Usage note:** `usage=server` certs attach to Domain listeners; `usage=client` certs
attach to Clients for mutual TLS and come from the **private CA** (public ACME can't
issue arbitrary client-auth identities). The private CA signs any subject — no
hostname-ownership restriction (see §8).

## 5. Distribution & reconcile

**Project-scoped ⇒ one cert targets exactly one cluster** (its Project). The control
cluster issues; the controller syncs the leaf to that one tenant cluster (1:1). A
hostname served by three clusters = three certs, one per project.

**Sync engine — event-driven controller, leader-elected:**
- **One informer** on the control cluster's TLS Secrets (label-filtered to managed
  certs) — the only place certs are born/renewed, so one watch total.
- **Workqueue keyed by cert** + a worker pool inside the leader. Events (Secret change)
  and backend actions (create/attach/detach) enqueue the cert's key; a worker reads the
  source Secret, compares fingerprint to `CertificateDistribution`, and upserts the leaf
  into the target cluster under a **stable name** (`fgw-cert-<certID>`) only on change.
- **Cross-replica nudge via Postgres `LISTEN/NOTIFY`** (no Redis): a non-leader API
  replica changing a cert↔domain link `NOTIFY`s; the leader enqueues.
- **Slow periodic resync** (e.g. hourly) is the safety net — catches missed events and
  tenant-side drift (a deleted/edited leaf Secret) and re-pushes. It is the only thing
  that touches tenant clusters on a schedule.
- **Level-based & idempotent:** desired state is re-derivable from the informer relist +
  DB; the in-memory queue is not durable state. On leader loss, a new leader relists.

**Leader election** (single writer to tenant Secrets — a feature, not a bottleneck):
a Kubernetes `Lease` in the control cluster (or a Postgres advisory lock). The API/HTTP
layer is stateless and scales horizontally (1→N); the cert-sync controller runs on
exactly one leader replica; the rest stand by. Work is change-proportional, not
inventory-proportional, so a single leader handles large fleets (e.g. 1000 domains / 20
clusters). Existing always-on work (e.g. the JWKS ticker) stays per-replica and is
unaffected.

**Stable name ⇒ renewal never edits the Gateway.** The tenant Secret name is fixed per
cert; renewal only changes Secret bytes. Only attach/detach edits the listener — which
requires implementing the missing `UpdateGateway`/listener-update path (fills the
existing `domain_service.go:371` "TODO: Update Kubernetes resources" gap; there is no
`UpdateGateway` today).

## 6. Cluster migration

No special "move a cert between projects" machinery. Hostname uniqueness is already
per-(project, hostname), and certs are project-scoped, so:
1. Owner grants the issuer to the new project.
2. Create the Domain (same DNS name) + cert in the new project → approve → issue → sync.
3. Both clusters serve a valid cert for the hostname simultaneously (independent certs) —
   a zero-downtime cutover. Flip traffic, then tear down the old project's cert/domain
   (deletion guard requires detaching the domain/client first).

Footnote: public ACME rate limits (LE ~5 duplicate/week, ~50/registered-domain/week) are
irrelevant to normal migration; the private CA has no such limit.

## 7. Visibility (bird's-eye views)

Read/aggregate over existing status/expiry/fingerprint/distribution data — no new model.
- **Project cert view** — project-scoped, `certificate.view`: every `ManagedCertificate`
  in the project with name, usage, issuer, hostnames/subject, status+message, expiry,
  renewal state, sync status, and referencing domains/clients. Filterable by status/expiry.
- **FastGateway-wide fleet view** — `owner`-only: all certs across all projects (plus
  project + issuer), for platform expiry/renewal health, issuer usage, and distribution
  drift. Future hook for expiry alerting.

## 8. Permission model

Two existing layers: system role (`owner` = platform admin, bypasses all checks; `user`)
and per-project RBAC permissions granted to teams via presets (`viewer/editor/approver/admin`).
Approval today covers `route` and `client_attachment`.

**Trust infrastructure — `owner`-only, platform-global** (modeled like `SSOConfig`/
`SystemSettings`: no `ProjectID`, `RequireRole("owner")` router group):
- Create/manage root CAs, ACME accounts, and DNS-provider credentials.
- **Per-project grants:** each issuer is granted to specific projects
  (`IssuerProjectGrant`); invisible to non-granted projects. Example: a Let's Encrypt
  issuer for `zufardhiyaulhaq.com` granted to Project X only — Project Y cannot use it.

**Issuance — project-scoped, approval-gated:**
- New permission group `certificate.view/create/edit/delete` + `certificate.approve`,
  added to `internal/models/team.go` (`AllPermissions` + presets).
- Any team assigned to a granted project may create a certificate (`certificate.create`).
- **Every certificate creation requires approval** — a new `certificate` approval
  `EntityType` wired into the existing engine; the approver holds `certificate.approve`;
  the existing "cannot approve your own" rule (`SelfApprovalAllowed`) applies.
- **No hostname-ownership restriction.** The private CA signs arbitrary subjects by
  design — required for client mTLS certs whose identities are not owned domains.
  Approval is the compensating human control; the per-project grant bounds which projects
  can touch each issuer; public ACME is self-limited by DNS-01 zone control.

**Visibility:** project view = `certificate.view`; fleet view = `owner`-only route group.

**Gating pattern to add** (per the existing model): global endpoints → `RequireRole("owner")`
group; project endpoints → new `certificate.*` constants + a `PermissionChecker.CanX`
method delegating to `HasPermission(projectID, user, PermX)`; approval → new
`ApprovalEntityType` + completer wired into `internal/approval`.

## 9. Security

- No private keys in the DB (§Global Constraints). DNS/EAB creds encrypted via
  `internal/crypto`, decrypted only transiently to build the solver Secret.
- RBAC least-privilege: control-cluster SA scoped to `cert-manager.io` CRDs + Secrets in
  the control namespace; tenant access unchanged. Only the leader writes tenant Secrets.
- **Blast radius, stated plainly:** the control cluster holds the root CA key + ACME
  account keys + DNS creds — crown jewels; must be hardened and access-restricted. Tenant
  clusters hold only leaf Secrets (bounded exposure).
- Referential guards: cannot delete a cert used by a Domain/Client, an issuer used by a
  cert, or a DNS credential used by an issuer.

## 10. Error handling

- Issuance failures surfaced, not swallowed: map the `Certificate` `Ready` condition
  reason/message → `ManagedCertificate.status=error` + actionable message (DNS-01 failure,
  ACME rate limit, invalid EAB, CA misconfig).
- Distribution failures isolated per cluster: the `CertificateDistribution` row carries
  status/error; a failed push retries next reconcile/resync; partial state is observable.
- Renewal safety: if upstream renewal fails, the old cert keeps serving until expiry;
  alert on approaching-expiry-with-failed-renewal. All cluster writes are create-or-update
  (idempotent).

## 11. Testing

- **Unit:** cert-manager CRD builders (SelfSigned/CA/ACME Issuer, leaf `Certificate`) with
  **golden YAML files** (matches the existing `internal/kubernetes` builder+golden pattern);
  DNS-cred encryption round-trip; target-derivation + fingerprint-compare + deletion-guard
  + permission/grant logic.
- **Controller/reconcile:** fake control + tenant clients — push-on-fingerprint-change,
  self-heal on missing tenant Secret, no-op when in sync, per-cluster failure isolation,
  single-writer/leader invariant.
- **e2e:** extend the existing kind + Envoy Gateway suite to install cert-manager. Cases:
  (1) self-signed CA → issue → distribute → Gateway serves it (TLS curl); (2) ACME via
  **Pebble** + a DNS-01 test solver; (3) renewal propagation (short duration / forced
  rotation → reconcile re-pushes); (4) deletion guard; (5) approval gating on cert create.
  Multi-cluster fan-out is out of scope now (1 cert → 1 cluster); note the single-cluster
  e2e limitation.

## 12. Out of scope (YAGNI)

- **DNS zone/record management** — only the `DNSProviderCredential` table + CRUD are built
  now (cert-solving is the sole consumer). The credential is decoupled so a future DNS
  feature reuses it; we do not build DNS management.
- **HTTP-01** ACME (central issuance can't solve it).
- **One cert distributed to many clusters** (project-scoped model makes it one-per-project).
- **Expiry alerting/notifications** — the fleet view exposes the data; alerting is a later hook.

## 13. Open questions

- Exact key parameters/defaults exposed for CA and leaf certs (algorithm, size, duration,
  `renewBefore`) — to be pinned in the plan.
- Leader election mechanism choice (K8s `Lease` vs Postgres advisory lock) — both viable;
  decide in the plan based on operational preference.
- Reconcile resync interval and drift-verify cadence — tuning, pinned in the plan.
