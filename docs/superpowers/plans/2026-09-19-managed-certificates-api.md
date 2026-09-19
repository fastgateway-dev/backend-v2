# Managed Certificates — API Contract (all phases)

**Spec:** `docs/superpowers/specs/2026-09-19-managed-certificates-design.md`

Complete API surface across all four phases, defined upfront so the contract is
stable. Base path `/api/v1`; all routes are under the authenticated `protected`
group. Conventions match the existing router (`cmd/server/main.go`): owner-only
global resources are top-level groups (e.g. `/users`, `/teams`, and here
`/certificates/issuers`) guarded by `RequireRole("owner")`; project-scoped
resources live under `/projects/:projectId/...` with
`RequireProjectAccess()` plus a per-handler permission check; params are camelCase.

Auth column legend:
- **owner** — `RequireRole("owner")` (platform admin).
- **cert.X** — project RBAC permission `certificate.X` via `PermissionChecker`.
- **domains** — `CanManageDomains` (owner / project-admin / `domain.delete`).
- **team** — `CanAccessTeamResource(client.TeamID, user)`.
- **project** — `RequireProjectAccess` (any project member).

---

## Phase 1 — Trust infrastructure (owner-only, global)

Groups: top-level owner-only resource groups (like `/users`, `/teams`), each `RequireRole("owner")` — `/dns/credentials`, `/certificates/issuers` (+ `/:issuerId/grants`), and the Phase 4 fleet view `/certificates`.

### DNS provider credentials  (`DNSProviderCredential`)
| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/dns/credentials` | owner | List DNS credentials (no secret material in response). |
| POST | `/dns/credentials` | owner | Create. Body: `{name, providerType, credentials{…}}`. Credentials encrypted at rest; never returned. |
| GET | `/dns/credentials/:dnsCredentialId` | owner | Get metadata (masked). |
| PATCH | `/dns/credentials/:dnsCredentialId` | owner | Update name / rotate credentials. |
| DELETE | `/dns/credentials/:dnsCredentialId` | owner | Delete. **409** if referenced by an ACME issuer. |

`providerType ∈ {cloudflare, route53, …}`. Response: `{id, name, providerType, createdAt, updatedAt}` (no `credentials`).

### Certificate issuers  (`CertificateIssuer`: self-signed CA or ACME)
| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/certificates/issuers` | owner | List all issuers. |
| POST | `/certificates/issuers` | owner | Create. Body discriminated by `type` (below). Creates cert-manager CRDs in the control cluster. |
| GET | `/certificates/issuers/:issuerId` | owner | Get. |
| PATCH | `/certificates/issuers/:issuerId` | owner | Update mutable metadata (display name; ACME email). |
| DELETE | `/certificates/issuers/:issuerId` | owner | Delete. **409** if referenced by any `ManagedCertificate`. Removes the cert-manager CRDs. |
| GET | `/certificates/issuers/:issuerId/status` | owner | cert-manager readiness of the underlying Issuer/ClusterIssuer (Ready condition + message). |

Create body — self-signed CA:
`{type:"self_signed_ca", name, commonName, keyAlgorithm, keySize, durationDays}`
Create body — ACME:
`{type:"acme", name, server, email, eab?{keyId,hmacKey}, dnsCredentialId}`
Response: `{id, type, name, status, config{…}, createdAt, updatedAt}` (no key material).

### Issuer → project grants  (`IssuerProjectGrant`)
| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/certificates/issuers/:issuerId/grants` | owner | List projects an issuer is granted to. |
| POST | `/certificates/issuers/:issuerId/grants` | owner | Grant. Body: `{projectId}`. |
| DELETE | `/certificates/issuers/:issuerId/grants/:projectId` | owner | Revoke. **409** if a cert in that project still uses the issuer. |

---

## Phase 2 — Certificate issuance + approval (project-scoped)

Group: `/api/v1/projects/:projectId/certificates`, `RequireProjectAccess()` + per-handler `cert.X`.

| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/projects/:projectId/certificates/issuers` | cert.view | Issuers **granted to this project** (for the create form). |
| POST | `/projects/:projectId/certificates` | cert.create | Create a managed cert → opens an **approval** (returns the pending `ManagedCertificate` + `approvalId`). Body: `{name, issuerId, usage, dnsNames[]  \| subject, keyAlgorithm?, keySize?, durationDays?}`. |
| GET | `/projects/:projectId/certificates/:certificateId` | cert.view | Get one (status, issuer, hostnames, fingerprint, expiry). |
| PATCH | `/projects/:projectId/certificates/:certificateId` | cert.edit | Edit metadata / trigger re-issue (may re-open approval). |
| DELETE | `/projects/:projectId/certificates/:certificateId` | cert.delete | Delete. **409** if referenced by a Domain or Client. Removes the leaf `Certificate` CRD. |
| GET | `/projects/:projectId/certificates/:certificateId/status` | cert.view | Issuance status from the cert-manager `Certificate` `Ready` condition (reason/message). |

`usage ∈ {server, client}`. Approval uses the existing generic endpoints (below) with the new `certificate` entity type — **no new approval paths**.

### Approval (existing, reused via `EntityType=certificate`)
| Method | Path | Auth | Purpose |
|---|---|---|---|
| POST | `/approvals/:approvalId/stages/:stageId/approve` | cert.approve (stage) | Approve a stage (existing engine; "cannot approve own" applies). |
| POST | `/approvals/:approvalId/stages/:stageId/reject` | cert.approve (stage) | Reject. |
| POST | `/approvals/:approvalId/stages/:stageId/cancel` | submitter/owner | Cancel. |
| GET | `/projects/:projectId/approvals?entityType=certificate` | project | List certificate approvals (existing approval listing, filtered). |

---

## Phase 3 — Distribution & attachment

### Distribution status / control  (project-scoped)
| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/projects/:projectId/certificates/:certificateId/distribution` | cert.view | Sync state for the cert's target cluster (last-pushed fingerprint, status, timestamp). |
| POST | `/projects/:projectId/certificates/:certificateId/resync` | cert.edit | Force a reconcile/re-push now (enqueue), bypassing the tick. |

### Attach a server cert to a Domain
| Method | Path | Auth | Purpose |
|---|---|---|---|
| PUT | `/projects/:projectId/domains/:domainId/certificate` | domains | Attach a `usage=server` managed cert. Body: `{certificateId}`. Updates the Gateway listener (via the new `UpdateGateway` path). |
| DELETE | `/projects/:projectId/domains/:domainId/certificate` | domains | Detach; listener reverts to legacy `TLSSecretName` or no-TLS. |

(Equivalent to setting `Domain.managedCertificateId`; exposed as an explicit sub-resource for clarity. Legacy `PATCH /projects/:projectId/domains/:domainId` with `tlsSecretName` remains for BYO.)

### Attach a client cert to a Client (mTLS)
| Method | Path | Auth | Purpose |
|---|---|---|---|
| PUT | `/clients/:clientId/certificate` | team | Attach a `usage=client` managed cert (private-CA issued) as the client's mTLS identity. Body: `{certificateId}`. |
| DELETE | `/clients/:clientId/certificate` | team | Detach. |

---

## Phase 4 — Visibility

### Project cert view  (project-scoped)
| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/projects/:projectId/certificates` | cert.view | List every managed cert in the project: name, usage, issuer, hostnames/subject, status+message, expiry, renewal state, sync status, referencing domains/clients. Query filters: `status`, `expiresBefore`, `issuerId`, `usage`. |

### FastGateway-wide fleet view  (owner-only, global)
| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/certificates` | owner | All managed certs across all projects (adds `projectId` + issuer). Query filters: `status`, `expiresBefore`, `issuerId`, `projectId`, `usage`. For platform expiry/renewal health & drift. |

---

## Error conventions (match existing handlers)
- `400` invalid body / validation; `403` permission denied; `404` not found;
  `409` referential guard (delete/revoke blocked by an in-use reference);
  `422` semantic validation (e.g. ACME issuer requires a `dnsCredentialId`).
- No secret material (private keys, DNS credentials, ACME account keys, EAB HMAC)
  ever appears in any response body.
