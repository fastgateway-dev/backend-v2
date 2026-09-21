# Managed Certificate E2E Coverage — Design

**Status:** approved design, pre-plan.
**Builds on:** the shipped managed-certificate feature (Phases 1–4 + client certs). Adds the e2e coverage the specs listed as intended but no task implemented.

## 1. Background & the central obstacle

The managed-certificate feature has **zero e2e coverage**. The existing e2e suite tests BYO TLS/mTLS with hand-generated (`step` CLI) PKI; nothing exercises cert-manager issuance, the `/certificates`/issuer APIs, distribution, managed attach, or client-cert export.

The blocker: the e2e backend runs **off-cluster** (`go run ./cmd/server` on the CI runner), so `IsRunningInCluster()` is `false` and the entire managed-cert surface — the control-plane client, the `certdist` controller, and every `/certificates*`/issuer/client-cert route — is compiled out / nil-guarded (`cmd/server/main.go:338`, `:420`). So an e2e call returns 404 today. This matches the design's own note ("the backend must run in-cluster; local/dev/test mock the control-cluster client").

## 2. Approach (chosen: A — in-cluster backend)

A **new `e2e-certificate` CI job + a new `e2e/suites/certificate/` suite** where the backend runs **in-cluster as a pod**, so `IsRunningInCluster()` is true and the real managed-cert surface is active. The existing off-cluster `e2e` job is untouched. Scope: **Tier 1 (server happy path) + Tier 2 (client managed + CSR)**; Tier 3 edge cases and ACME/Pebble are deferred (the harness will already exist, making them cheap add-ons later).

## 3. Topology (single kind cluster = control + tenant)

Control cluster (cert-manager + leaf keys) and tenant cluster (Gateway) collapse into one kind cluster — the design's stated single-cluster e2e limitation.
- **cert-manager** installed via Helm.
- The **backend runs as a `Deployment`** (image from the root `./Dockerfile`, `kind load`ed) with a **ServiceAccount + ClusterRole** granting the control-plane RBAC (`cert-manager.io` issuers/clusterissuers/certificates/certificaterequests + Secrets + the Gateway API / Envoy Gateway CRDs it applies). The SA mount flips `IsRunningInCluster()` true.
- **Project connection = `in_cluster`** (seeded), so the tenant client resolves to the same in-cluster SA → control and tenant are the same cluster. cert-manager writes `cert-<id>` in `fastgateway-system`; the distributor's push is an idempotent same-cluster copy; the Gateway serves it.
- **Postgres** deployed in-cluster (the pod backend can't reach the runner's `localhost` Postgres).
- **The test binary + `e2e-seed` stay off-cluster** on the runner and reach the backend via a **`Service` type LoadBalancer** (cloud-provider-kind, like the Gateway IP) — set `FASTGATEWAY_API_URL` to that IP (no harness code change to repoint).

## 4. CI job provisioning sequence (`.github/workflows/e2e-certificate.yml`)

Adapts the existing job's proven steps; new steps marked:
1. `kind create cluster` + cloud-provider-kind.
2. Gateway API CRDs + Envoy Gateway (Helm, pinned version; minimal values, no rate-limit/Redis).
3. **cert-manager** (Helm, jetstack, `installCRDs=true`) + `rollout status` wait — before the backend starts.
4. **Postgres** `Deployment` + `Service`.
5. **Build backend image** from `./Dockerfile` → `kind load docker-image fastgateway-backend:e2e`.
6. **Apply backend manifests** (SA/RBAC, Deployment, LoadBalancer Service) → `rollout status` + `/health`.
7. **Resolve backend LB IP** → export `FASTGATEWAY_API_URL`.
8. **Seed** (`go run ./e2e/cmd/e2e-seed`, off-cluster) with the project as **`in_cluster`**.
9. Wait for the seeded Gateway LB IP (`GATEWAY_IP`).
10. `go test -tags e2e ./e2e/suites/certificate/... -p 1 -count=1 -v -timeout 30m`.
11. Diagnostics-on-failure + `kind delete cluster`.

## 5. New manifests (`e2e/deps/backend/`)

- **`postgres.yaml`** — ephemeral `postgres:16` `Deployment` + `Service` matching the backend `DATABASE_*`.
- **`backend-rbac.yaml`** — `ServiceAccount fastgateway-backend` (ns `fastgateway-system`) + `ClusterRole` (cert-manager.io CRDs, core secrets/namespaces, Gateway API + Envoy Gateway CRDs) + `ClusterRoleBinding`. Broad for e2e (not the prod least-privilege recommendation).
- **`backend.yaml`** — `Deployment` (image `fastgateway-backend:e2e`, the SA, env mirroring the current `go run` block: `DATABASE_*` → in-cluster Postgres, `API_PORT`, `JWT_SECRET`, `ENCRYPTION_KEY`, `ADMIN_*`, plus `CONTROL_PLANE_NAMESPACE=fastgateway-system` and a **short `CERT_DISTRIBUTOR_INTERVAL` (~2–5s)** for fast distribution) + a `Service` type `LoadBalancer` on `8081`.

## 6. Seed change

Extend `e2e/cmd/e2e-seed` with an in-cluster mode (env var, e.g. `SEED_PROJECT_IN_CLUSTER=true`) that sets `ConnectionType: in_cluster` on the created project and omits the API-URL/token. The in-cluster backend accepts it (`validateInCluster` passes).

## 7. Suite + harness additions (`e2e/suites/certificate/`)

Bootstrap reuses `harness.NewEnv` unchanged (base URL comes from `FASTGATEWAY_API_URL`); logs in owner/editor/approver; resolves the seeded project/domain/team; the off-cluster `Kube` client stays available for cluster peeking.

**New helpers** (thin wrappers over `env.Admin/Editor/Approver.Do`, mirroring existing API wrappers):
- `createSelfSignedCAIssuer` → `POST /certificates/issuers` (owner) → poll `…/issuers/:id/status` until Ready.
- `grantIssuerToProject` → `POST …/issuers/:id/grants`.
- `createManagedCert(usage, keyMode, …)` → `POST /projects/:id/certificates` → `ApproveAllStages` (matches on the cert's `EntityID`) → poll `…/certificates/:id/status` until `ready`.
- `attachServerCertToDomain` → `PUT /projects/:id/domains/:domainId/certificate`.
- `exportManagedBundle(certID)` → `POST …/export` → approve → `GET …/export/download` → parse leaf+key+CA.
- `attachClientCertToClient` → `PUT /clients/:clientId/certificate`.
- `genKeypairAndCSR(subject, uriSAN)` (crypto/x509) for CSR mode.
- **New gateway helper:** a verifying TLS dial (RootCAs from the cluster CA Secret) returning the served peer chain — the existing `GW.tlsConfig` uses `InsecureSkipVerify`, so chain verification needs its own dial. `WithClientCert` already exists for client-cert presentation.

## 8. Test cases

**Tier 1 — server happy path** (`server_managed_cert_test.go`): self-signed CA issuer → grant to project → `usage=server`/`keyMode=managed` cert (`dnsNames=[hostname]`) → approve → ready → attach to domain → wait listener reprogram → verifying TLS handshake to `GATEWAY_IP` (SNI=hostname): served leaf **issued by the platform CA** (root pool from the cluster CA Secret) + SAN matches hostname + a request routes through (200).

**Tier 2 — client certs:**
- **Managed** (`client_managed_cert_test.go`): `usage=client`/`keyMode=managed` cert → approve → ready → export bundle → attach cert to a `Client` (derives mTLS trust=platform CA + pins SAN) → attach Client to the domain (mTLS enforced) → present the exported cert+key (`WithClientCert`) → 200; a cert not from the platform CA / wrong SAN → TLS failure or 403.
- **CSR** (`client_csr_cert_test.go`): generate keypair+CSR → `usage=client`/`keyMode=csr` cert with the CSR → approve → ready → **retrieve the signed leaf from the cluster** (the CertificateRequest's `.status.certificate` via `Kube` — see §10) → attach cert to a Client → present the signed leaf + the test's own key → 200.

## 9. Readiness & flakiness

Serial (`-p 1`), poll-until with generous timeouts: poll issuer + cert `…/status` until Ready; set `CERT_DISTRIBUTOR_INTERVAL` short so distribution ticks fast, then poll `…/distribution` (or the tenant Secret) until `synced`; after attach, poll listener/route readiness and **retry the TLS handshake** until Envoy serves the new cert; approvals are synchronous via `ApproveAllStages`.

## 10. Known product gap (out of scope; flagged)

There is **no API to retrieve a CSR-signed public certificate**: `ExportBundle` is managed-only (csr → 409), and `Status()` returns only status/message/notAfter — the signed leaf lives only in the cert-manager `CertificateRequest.status.certificate`. So a real CSR-mode caller currently has no first-class way to fetch their signed cert via the API. For e2e, the harness reads it from the cluster. Whether production needs a "download signed cert" endpoint for CSR mode is a **possible feature gap** to confirm separately — not addressed by this e2e plan.

## 11. Out of scope

- Tier 3 edge cases (approval-rejection blocks issuance; deletion guard; not-ready export 422; distributor skip) — cheap follow-ups once the harness exists.
- ACME via Pebble + DNS-01 (heavier infra).
- Multi-cluster fan-out (single-cluster e2e only).
- A production CSR-signed-cert download endpoint (§10).
