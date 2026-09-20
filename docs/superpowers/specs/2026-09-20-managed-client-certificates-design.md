# Managed Client Certificates (mTLS identities) — Design

**Status:** approved design, pre-plan.
**Builds on:** `docs/superpowers/specs/2026-09-19-managed-certificates-design.md` (Phases 1–4, shipped). This spec covers the deferred **client-cert attachment** piece of that design's API contract.

## 1. Background & problem

The managed-certificate feature ships server certs end-to-end (issue → distribute → attach to a Domain → view). The one contract item left deferred is **`usage=client` certificates**: certificate identities that an external caller presents to the gateway during mutual TLS.

Today a `Client` (a team-owned consumer definition) does mTLS as the **verifier's config only**: the team pastes a **CA PEM** (`MTLSCAPem`) plus a **SAN/fingerprint allowlist** (`MTLSSANs`/`MTLSHashes`), and the gateway trusts that CA and pins those identities. The platform never issues or holds a client identity. This spec adds the ability for the platform's **private CA** to issue the client identities themselves, in two key-custody modes, and to bind one to a Client.

## 2. Scope & the certificate taxonomy

A managed certificate is fully classified by three categories (with constraints); everything else is a parameter, status, relationship, or derived:

- **`usage`** — `server` | `client` (stored). Which side of mTLS the cert identifies.
- **`keyMode`** — `managed` | `csr` (**new**, stored). Who holds the private key.
- **trust origin** — private CA (`self_signed_ca`) | public CA (`acme`). **Derived** from the cert's issuer (`IssuerID` → `CertificateIssuer.type`); not a new column.

**Constraints** (only 4 valid kinds):

| # | usage | trust origin | keyMode | meaning |
|---|---|---|---|---|
| 1 | server | public CA | managed | public TLS on a domain (exists) |
| 2 | server | private CA | managed | internal server TLS (exists) |
| 3 | **client** | **private CA** | **managed** | platform mints + holds key; approval-gated export (**new**) |
| 4 | **client** | **private CA** | **csr** | caller holds key; platform signs the CSR (**new**) |

Rules enforced at creation: `client ⟹ private CA` (public ACME cannot issue arbitrary client-auth identities); `csr ⟹ client` (a server's key must be held by the platform so the gateway can terminate TLS); therefore `server ⟹ managed`.

**This spec = rows 3 and 4.** Rows 1–2 are unchanged.

## 3. Ownership (unchanged, applied here)

- **Certificate** → project-owned (`ProjectID`) + `CreatedBy` (audit). Governed by project RBAC (`certificate.view/create/delete/approve`). Identical to a Domain.
- **Client** → team-owned (`TeamID`). Teams get roles in projects via `ProjectTeamRole`.
- **Bridge:** a client cert (project-owned) may be attached to a Client (team-owned) **only if the Client's team has a role in the cert's project.**
- **Approval** (create, and the new export) is by the **cert's project's** `certificate.approve` holders (Admin/Approver presets), "cannot approve your own."

## 4. Certificates are created in the certificate menu; attach is link-only

A client cert is created like any managed cert — in the certificate menu, project-scoped, approval-gated — with its **identity set at creation** (subject/SAN, analogous to `dnsNames` for a server cert). It knows nothing about clients. **Attaching a cert to a client creates NO new cert**; it is pure linkage. Symmetric with server certs:

| | created with… | attach = link to… | attach creates a cert? |
|---|---|---|---|
| server cert | DNS names | a Domain | no |
| client cert | subject / SAN | a Client | no |

Two distinct clients ⇒ two separately-created client certs (each with its own SAN); the 1:1 attach rule prevents pinning one cert to two clients. **The platform imposes no client-specific SAN format** — the SAN is the creator's choice at creation (managed mode) or carried in the uploaded CSR (csr mode).

## 5. Data model changes

- **`ManagedCertificate`** gains **`KeyMode {managed, csr}`** (default `managed`), validated `csr ⟹ usage=client`. The client identity (subject/SAN) uses the existing create-form/`Config` fields. Trust origin stays derived from the issuer.
- **`Client`** gains **`ManagedCertificateID *uuid.UUID`** — nullable, **UNIQUE** (partial unique index where not null), FK `ON DELETE RESTRICT`. This is the client's mTLS identity (1:1). No BYO-vs-managed flag: `ManagedCertificateID != nil` → managed; else `MTLSCAPem != ""` → BYO; else no mTLS (mutually exclusive).
- Migration: next sequential number; adds the `Client` column + unique index + FK.

## 6. Flows

**A. Create (certificate menu, approval-gated).** `usage=client` requires a **private-CA issuer** (the form filters out ACME). Choose `keyMode`:
- **managed:** form = subject/SAN + key params + duration → creation approval → cert-manager `Certificate` → leaf + **key in a control-cluster Secret**. Status from the `Ready` condition.
- **csr:** form = **uploaded CSR** (identity/SAN comes from the CSR) → parse/validate → creation approval → cert-manager `CertificateRequest` signs it → **signed cert only, no key**. Status = Ready when signed.

**B. Get the material.**
- **managed:** the key is behind an **export approval** (`certificate` entity, new `export` action). On approve → a **single-use, short-TTL download** yields the bundle (leaf + key + issuer CA chain); the token is then burned. Creation reveals nothing.
- **csr:** no export (caller holds the key). The **public signed cert** downloads directly (view-gated).

**C. Attach to a client (client menu — link only).** `PUT /clients/:clientId/certificate {certificateId}`. Checks: cert exists, `usage=client`, `ready`; the **client's team has a role in the cert's project**; cert not already attached (**409**); client has no managed cert already (else detach first). On success: set `Client.ManagedCertificateID`; derive the client's mTLS — enable mTLS, trust CA = the cert's **issuer CA** (materialized into the client's CA secret), `MTLSSANs = [the cert's SAN]`; re-apply the `ClientTrafficPolicy` for the client's attached domains. `DELETE .../certificate` → clear the FK, revert to BYO (or no mTLS), re-apply.

**D. Renewal.** managed: cert-manager auto-renews; the SAN pin is unchanged so the gateway doesn't move; caller re-exports for the fresh key. csr: no auto-renew; caller re-issues (new CSR) and re-attaches near expiry.

## 7. Gateway wiring (reuse)

A domain's `ClientTrafficPolicy` already aggregates a **CA bundle** (`collectCASecretRefs`: domain CAs + every attached client's CA) and a **union SAN/hash allowlist**. A managed-bound client feeds the same path: its CA = the cert's **issuer CA** (materialized into the client's mTLS-CA secret, sourced from the platform issuer instead of a pasted PEM), its allowlist entry = the cert's **SAN**. Multiple clients on one domain sharing the private CA → CA deduped to one bundle entry; each client's SAN isolates its identity (CA proves "genuine," SAN proves "who"). No new listener logic.

## 8. Distribution controller change

The Phase-3a controller pushes leaf secrets to tenant clusters for all `ready` certs; it must **skip `usage=client`** (client leaves are identities, not listener secrets — the managed key stays in the control cluster as the export source; csr has no key). The issuer CA reaches the cluster via the client-mTLS-CA-secret path on attach, not via leaf distribution.

## 9. Approval

Reuse the existing engine and the `certificate` entity type. Add one action, **`export`**; its completer (on approve) mints the single-use export download token. Creation approval unchanged. Both gated by `certificate.approve` in the cert's project; "cannot approve your own" applies. **Attach/detach is NOT approval-gated** — it's a client-management permission (linking an already-approved identity), consistent with server-cert→domain attach.

## 10. Security

- **No private key in the DB, ever.** managed: key lives in cert-manager's control-cluster Secret (the design's crown-jewels boundary). csr: the key never touches the platform.
- The **only** response that carries a private key is the one-time managed-mode export bundle, gated by an approval and a single-use short-TTL token.
- CSR mode validates the uploaded CSR before signing (well-formed; the platform records the SAN the signed cert will carry).
- Client isolation under a shared private CA rests on the **per-cert SAN pin** — enforced because each client is a distinct cert with its own SAN, and attach is 1:1.

## 11. Revocation (YAGNI — no CRL/OCSP)

To revoke a client: **detach + delete** the cert. Detach removes its SAN from the gateway allowlist, so the cert stops authenticating immediately; delete removes the identity. Short durations + re-issue cover rotation. Full CRL/OCSP is out of scope.

## 12. Error handling

- Create: `csr`+`server` → 422; `client`+public-CA issuer → 422; malformed CSR → 400; csr-mode without a CSR (or managed-mode with one) → 400.
- Export: on a `csr` cert → 409; token expired/used → 410; not-yet-approved → 403.
- Attach: cert missing / client's team not in the cert's project → 404 / 403; cert already attached → 409; client already has a managed cert → 409; cert not `ready` → 422.
- Delete: deleting a client cert still attached to a Client → **409** (referential guard, mirrors the domain FK).
- No secret material in any response except the one-time export bundle.

## 13. Testing

- **Unit:** `keyMode × usage × trustOrigin` constraint validation; CSR parse + SAN checks; managed (`Certificate`) vs csr (`CertificateRequest`) builders via golden YAML; export-approval completer minting a one-time token; attach 1:1 + team-in-project bridge; CA-secret materialization from the issuer CA; the `ClientTrafficPolicy` SAN pin.
- **Controller:** distributor skips `usage=client`.
- **e2e** (extend the kind + Envoy Gateway + cert-manager suite): private CA → create managed client cert → export → `curl` the domain with it (mTLS accepted) and a non-pinned cert (rejected); csr mode end-to-end; two clients sharing the private CA distinguished by SAN.

## 14. Out of scope

- Public-CA client certs (impossible by design).
- Storing any private key in the application database.
- CRL/OCSP revocation (detach+delete is the revocation story).
- Any client-specific SAN naming scheme imposed by the platform (the SAN is set at cert creation).

## 15. API surface (delta)

- Certificate create (existing) — accept `keyMode`; a **CSR-upload** variant for `keyMode=csr`.
- **`POST /projects/:projectId/certificates/:certificateId/export`** (or equivalent) — open an export approval; on approval, a single-use **download** endpoint returns the bundle. (cert.export/approve gated.)
- **`PUT /clients/:clientId/certificate`** `{certificateId}` — attach (link-only, 1:1). **`DELETE /clients/:clientId/certificate`** — detach. (client-management gated; team↔project bridge enforced.)
- OpenAPI source + bundle updated; route↔operation parity maintained.
