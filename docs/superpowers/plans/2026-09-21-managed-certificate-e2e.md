# Managed Certificate E2E Coverage Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add e2e coverage for the managed-certificate feature (Tier 1 server happy path + Tier 2 client managed/CSR) by running the backend in-cluster in a new `e2e-certificate` CI job.

**Architecture:** A new kind cluster where cert-manager + Postgres + the backend-as-a-pod (SA mount → `IsRunningInCluster()` true → managed-cert surface active) + Envoy Gateway all live; the off-cluster test binary + `e2e-seed` reach the backend via a LoadBalancer Service. Single cluster acts as both control and tenant (project connection = `in_cluster`). A new `e2e/suites/certificate/` suite drives the real HTTP surface and verifies TLS chains / client-cert mTLS through the Gateway.

**Tech Stack:** kind, cloud-provider-kind, Helm (Envoy Gateway + cert-manager), Gateway API, Go 1.25 e2e harness (`-tags e2e`), GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-09-21-managed-certificate-e2e-design.md`

## Global Constraints

- **Verification model differs from code plans:** the e2e suite CANNOT run locally (needs kind + the in-cluster backend). Each task's local gate is: Go compiles under `-tags e2e` (`go build -tags e2e ./e2e/...`) + `go vet` + `gofmt`; manifests pass `kubectl apply --dry-run=client -f <file>` (or `kubeconform`); the workflow passes `actionlint`. **The true end-to-end validation is the `e2e-certificate` CI job going green, which requires pushing to a branch** — the subagent loop builds + statically verifies; the human runs CI and we iterate on failures.
- New job/suite only; do NOT modify the existing off-cluster `e2e` job or `e2e/suites/*` other than shared harness additions (which must not break existing suites — run `go build -tags e2e ./e2e/...` after harness changes).
- Single-cluster: control == tenant; project connection = `in_cluster`; `CONTROL_PLANE_NAMESPACE=fastgateway-system`.
- No secret material in logs; the exported client bundle (leaf+key+CA) is only handled in-test.
- Execution: subagents never commit; the human commits explicitly; commit exactly the enumerated files via explicit `git add` (never `-A`).
- Pinned versions: reuse the exact `GATEWAY_API_VERSION` / `ENVOY_GATEWAY_VERSION` / `CLOUD_PROVIDER_KIND_VERSION` the existing `.github/workflows/e2e.yml` uses; pin a cert-manager chart version (e.g. `v1.16.x`).

## File Structure

- `e2e/deps/backend/postgres.yaml` — in-cluster Postgres (Deployment + Service).
- `e2e/deps/backend/rbac.yaml` — ServiceAccount + ClusterRole + ClusterRoleBinding (control-plane RBAC).
- `e2e/deps/backend/backend.yaml` — backend Deployment + LoadBalancer Service.
- `e2e/cmd/e2e-seed/main.go` — add `in_cluster` project mode (env-gated).
- `e2e/harness/certapi.go` (new) — cert/issuer/export/CSR API wrappers.
- `e2e/harness/gateway.go` — add a verifying-TLS dial (RootCAs) returning the peer chain.
- `e2e/harness/kube.go` — add a "read a Secret's `tls.crt`/`ca.crt`" + "read a CertificateRequest `.status.certificate`" helper.
- `e2e/suites/certificate/main_test.go` — suite `TestMain`.
- `e2e/suites/certificate/server_managed_cert_test.go` — Tier 1.
- `e2e/suites/certificate/client_managed_cert_test.go` — Tier 2 managed.
- `e2e/suites/certificate/client_csr_cert_test.go` — Tier 2 CSR.
- `.github/workflows/e2e-certificate.yml` — the new CI job.

---

## Task 1: In-cluster backend manifests (Postgres, RBAC, Deployment+Service)

**Files:** Create `e2e/deps/backend/postgres.yaml`, `e2e/deps/backend/rbac.yaml`, `e2e/deps/backend/backend.yaml`.

**Interfaces — Produces:** a `fastgateway-backend` Deployment in ns `fastgateway-system` with SA `fastgateway-backend`, reachable via Service `fastgateway-backend` (type LoadBalancer, port 8081); Postgres reachable at `postgres.fastgateway-system.svc:5432`.

- [ ] **Step 1: Postgres manifest.** `postgres.yaml`: a `postgres:16` Deployment (ns `fastgateway-system`) with env `POSTGRES_USER=fastgateway`/`POSTGRES_PASSWORD=fastgateway`/`POSTGRES_DB=fastgateway`, an `emptyDir` volume, a readiness probe (`pg_isready`), and a `Service` named `postgres` on 5432.
- [ ] **Step 2: RBAC manifest.** `rbac.yaml`: `ServiceAccount fastgateway-backend`; a `ClusterRole` with rules for `cert-manager.io` (`issuers`,`clusterissuers`,`certificates`,`certificaterequests` — verbs get/list/watch/create/update/patch/delete), core (`secrets`,`namespaces` — same verbs), `gateway.networking.k8s.io` (`gateways`,`httproutes`,`referencegrants`,`gatewayclasses`), `gateway.envoyproxy.io` (`clienttrafficpolicies`,`backendtrafficpolicies`,`envoyextensionpolicies`,`backends`,`envoypatchpolicies`,`securitypolicies`); a `ClusterRoleBinding` binding the ClusterRole to the SA. (Confirm the exact CRD groups the backend applies by grepping `internal/cluster/*.go` for `schema.GroupVersionResource{` — include every group used.)
- [ ] **Step 3: Backend manifest.** `backend.yaml`: a `fastgateway-backend` Deployment using `image: fastgateway-backend:e2e`, `imagePullPolicy: IfNotPresent`, `serviceAccountName: fastgateway-backend`, env mirroring `.github/workflows/e2e.yml`'s go-run block (`DATABASE_HOST=postgres`, `DATABASE_PORT=5432`, `DATABASE_USER/PASSWORD/NAME=fastgateway`, `API_PORT=8081`, `JWT_SECRET`, `ENCRYPTION_KEY`, `ADMIN_USERNAME/PASSWORD/EMAIL`) plus `CONTROL_PLANE_NAMESPACE=fastgateway-system` and `CERT_DISTRIBUTOR_INTERVAL=3s`; a `/health` readiness probe on 8081; and a `Service fastgateway-backend` type `LoadBalancer` port 8081→8081. (Confirm the Dockerfile's exposed port + entrypoint at `./Dockerfile` and the exact env var names in `internal/config/config.go`.)
- [ ] **Step 4: Verify.** `for f in e2e/deps/backend/*.yaml; do kubectl apply --dry-run=client -f "$f" >/dev/null && echo "$f ok"; done` (client dry-run validates shape without a cluster; if `kubectl` unavailable, run `kubeconform` or at minimum `python -c 'import yaml,sys; [list(yaml.safe_load_all(open(f))) for f in sys.argv[1:]]' e2e/deps/backend/*.yaml`).

---

## Task 2: `e2e-seed` in-cluster project mode

**Files:** Modify `e2e/cmd/e2e-seed/main.go`.

**Interfaces — Consumes:** `services.CreateProjectInput` (has `ConnectionType`). **Produces:** when `SEED_PROJECT_IN_CLUSTER=true`, the seeded project is created with `ConnectionType: "in_cluster"` and no `K8sAPIURL`/`K8sToken`.

- [ ] **Step 1:** In the project-create block (`main.go:75-86`), read `os.Getenv("SEED_PROJECT_IN_CLUSTER")`. When `"true"`, build `services.CreateProjectInput{Name, Description, ConnectionType: services.ConnectionTypeInCluster}` (omit `K8sAPIURL`/`K8sToken`); otherwise keep the existing api-token form. (Confirm `services.ConnectionTypeInCluster`'s value is `"in_cluster"` in `internal/services/project_service.go`.)
- [ ] **Step 2: Verify.** `go build ./e2e/cmd/e2e-seed && go vet ./e2e/cmd/e2e-seed && gofmt -l e2e/cmd/e2e-seed/`. (Runtime is validated by the CI job.)

---

## Task 3: Harness — cert API wrappers + verifying-TLS dial + cluster reads

**Files:** Create `e2e/harness/certapi.go`; Modify `e2e/harness/gateway.go`, `e2e/harness/kube.go`.

**Interfaces — Produces (mirror the existing `API.Do(ctx, method, path, body, out)` and `API` wrapper style in `e2e/harness/api.go`):**
- `(*API) CreateIssuer(ctx, body any) (Issuer, error)` (`POST /certificates/issuers`); `(*API) IssuerStatus(ctx, issuerID) (Status, error)`; `(*API) GrantIssuer(ctx, issuerID, projectID) error`.
- `(*API) CreateCertificate(ctx, projectID string, body any) (CertCreateResp, error)` where `CertCreateResp{Certificate models.ManagedCertificate; ApprovalID string}` (`POST /projects/:id/certificates`); `(*API) CertificateStatus(ctx, projectID, certID) (CertStatus, error)`.
- `(*API) ApproveCertificate(ctx, projectID, certID) error` — reuse `ApproveAllStages` semantics (it matches on the approval's `EntityID`, which is the cert ID).
- `(*API) AttachServerCertToDomain(ctx, projectID, domainID, certID) error` (`PUT …/domains/:domainId/certificate`).
- `(*API) RequestExport(ctx, projectID, certID) (approvalID string, err error)`; `(*API) DownloadExport(ctx, projectID, certID) (bundlePEM []byte, err error)` (`GET …/export/download`).
- `(*API) AttachClientCert(ctx, clientID, certID) error` (`PUT /clients/:clientId/certificate`).
- `harness.GenClientCSR(subject string, uriSAN string) (csrPEM, keyPEM []byte, err error)` (crypto/x509 + crypto/ecdsa).
- `(*Gateway) TLSServedChain(ctx, sni string) ([]*x509.Certificate, error)` — a dial with `InsecureSkipVerify:true` that returns `ConnectionState().PeerCertificates` (so the test verifies the chain itself against a supplied root); and `(*Gateway) VerifyServedBy(ctx, sni string, roots *x509.CertPool) error` that dials with `RootCAs: roots` + `ServerName: sni` and returns the handshake error (nil = chain trusted).
- `(*Kube) ReadSecretKey(ctx, ns, name, key string) ([]byte, error)` (base64-decoded); `(*Kube) ReadCertificateRequestSignedCert(ctx, ns, name string) ([]byte, error)` (reads the `certificaterequests` CR `.status.certificate`, base64-decoded).

- [ ] **Step 1:** Add the cert/issuer/export wrappers in `certapi.go` using `a.Do(...)`. Response structs reuse `models.ManagedCertificate`/`services.CertStatus` where possible (import as the existing wrappers do).
- [ ] **Step 2:** Add `GenClientCSR` (generate an ECDSA P-256 key, `x509.CreateCertificateRequest` with the subject CN + a URI SAN, PEM-encode both).
- [ ] **Step 3:** Add `TLSServedChain` + `VerifyServedBy` to `gateway.go` (mirror the existing `tlsConfig`/dial in `gateway.go:39-52,110-159`, but capture `PeerCertificates` / set `RootCAs`).
- [ ] **Step 4:** Add the two `Kube` read helpers (`kube.go` already builds a dynamic/typed client — mirror how it reads other resources; the CertificateRequest GVR is `cert-manager.io/v1 certificaterequests`).
- [ ] **Step 5: Verify.** `go build -tags e2e ./e2e/... && go vet -tags e2e ./e2e/harness/ && gofmt -l e2e/harness/`. Confirm existing suites still compile (`go build -tags e2e ./e2e/...`).

---

## Task 4: Tier 1 suite — server happy path

**Files:** Create `e2e/suites/certificate/main_test.go`, `e2e/suites/certificate/server_managed_cert_test.go`.

**Interfaces — Consumes:** Task 3 helpers; `harness.NewEnv`, `env.GW.VerifyServedBy`, `env.Kube.ReadSecretKey`.

- [ ] **Step 1: `main_test.go`** — `TestMain` mirroring `e2e/suites/security/main_test.go:207-216` (`env, err = harness.NewEnv(ctx)`; `os.Exit(m.Run())`).
- [ ] **Step 2: the test** `TestServerManagedCertServedByGateway`:
  1. `createSelfSignedCAIssuer` (owner) → poll `IssuerStatus` Ready (timeout ~60s).
  2. `GrantIssuer(issuerID, env.ProjectID)`.
  3. `CreateCertificate(env.ProjectID, {name, issuerId, usage:"server", keyMode:"managed", dnsNames:[env domain hostname]})` → `ApproveCertificate` → poll `CertificateStatus` until `ready`.
  4. `AttachServerCertToDomain(env.ProjectID, env.DomainID, certID)`.
  5. Poll (retry ~60s) `env.Kube.ReadSecretKey(fastgateway-system, "cert-"+certID, "tls.crt")` to confirm the leaf Secret exists (distribution), then read the issuer's CA cert (`ReadSecretKey(fastgateway-system, <issuer CASecretName>, "ca.crt")`), build a `*x509.CertPool`.
  6. Retry-until-success: `env.GW.VerifyServedBy(ctx, hostname, caPool)` returns nil (Gateway serves a leaf chaining to the platform CA); assert the served leaf's SAN/CN includes the hostname (via `TLSServedChain`).
- [ ] **Step 3: Verify.** `go build -tags e2e ./e2e/suites/certificate/ && go vet -tags e2e ./e2e/suites/certificate/ && gofmt -l e2e/suites/certificate/`.

---

## Task 5: Tier 2 — managed client cert mTLS

**Files:** Create `e2e/suites/certificate/client_managed_cert_test.go`.

- [ ] **Step 1: the test** `TestClientManagedCertMTLS`:
  1. Issuer (reuse a per-suite helper that creates+grants a CA issuer, or the Tier 1 one).
  2. `CreateCertificate(usage:"client", keyMode:"managed", subject/uriSans: a client identity)` → approve → ready.
  3. `RequestExport` → approve the export approval (`ApproveAllStages`-style on the export approvalID) → `DownloadExport` → split the bundle into leaf/key/CA PEM.
  4. Create/resolve a `Client`, `AttachClientCert(clientID, certID)` (derives the client's mTLS trust = issuer CA + pins the SAN), attach the Client to `env.DomainID` with mTLS enforced + deploy (mirror `e2e/suites/security/client_mode_mtls_test.go`'s attach/deploy helpers).
  5. Positive: `env.GW.HTTP(ctx, "GET", path, harness.WithClientCert(leafPEM, keyPEM))` → 200 (retry until the CTP is programmed).
  6. Negative: present an unrelated self-signed client cert (generate one in-test) → `requireTLSFailure` or 403.
- [ ] **Step 2: Verify.** `go build -tags e2e ./e2e/suites/certificate/ && go vet -tags e2e ./e2e/suites/certificate/ && gofmt -l e2e/suites/certificate/`.

---

## Task 6: Tier 2 — CSR-mode client cert mTLS

**Files:** Create `e2e/suites/certificate/client_csr_cert_test.go`.

- [ ] **Step 1: the test** `TestClientCSRCertMTLS`:
  1. Issuer (CA).
  2. `harness.GenClientCSR(subject, uriSAN)` → keep `keyPEM`.
  3. `CreateCertificate(usage:"client", keyMode:"csr", csr: csrPEM, subject/uriSans matching the CSR)` → approve → poll `CertificateStatus` until `ready`.
  4. **Retrieve the signed leaf from the cluster:** `env.Kube.ReadCertificateRequestSignedCert(fastgateway-system, "cert-"+certID)` (the CertificateRequest CR name = `cert.Config.CertificateName` = `cert-<id>`; confirm the name convention). This is the §10 gap workaround — add a code comment citing the spec.
  5. Attach the cert to a Client, attach the Client to the domain (mTLS), deploy.
  6. Positive: present `signedLeafPEM` + the test's own `keyPEM` via `WithClientCert` → 200.
- [ ] **Step 2: Verify.** `go build -tags e2e ./e2e/suites/certificate/ && go vet -tags e2e ./e2e/suites/certificate/ && gofmt -l e2e/suites/certificate/`.

---

## Task 7: The `e2e-certificate` CI workflow

**Files:** Create `.github/workflows/e2e-certificate.yml`.

- [ ] **Step 1:** Author the workflow, copying the existing `.github/workflows/e2e.yml` structure and adapting per the spec §4: kind + cloud-provider-kind; Gateway API CRDs + Envoy Gateway (minimal values); **`helm install cert-manager jetstack/cert-manager --namespace cert-manager --create-namespace --set installCRDs=true --version <pinned>` + `kubectl rollout status`**; `kubectl apply -f e2e/deps/backend/postgres.yaml` + wait; `docker build -t fastgateway-backend:e2e . && kind load docker-image fastgateway-backend:e2e --name <cluster>`; `kubectl apply -f e2e/deps/backend/rbac.yaml -f e2e/deps/backend/backend.yaml` + `kubectl rollout status deploy/fastgateway-backend -n fastgateway-system`; resolve the backend LB IP (jsonpath, like GATEWAY_IP) → `echo "FASTGATEWAY_API_URL=http://<ip>:8081/api/v1" >> $GITHUB_ENV`; `SEED_PROJECT_IN_CLUSTER=true go run ./e2e/cmd/e2e-seed`; resolve `GATEWAY_IP`; `go test -tags e2e ./e2e/suites/certificate/... -p 1 -count=1 -v -timeout 30m`; diagnostics-on-failure (`kubectl get/describe`, backend pod logs) + `kind delete cluster`.
- [ ] **Step 2: Verify.** `actionlint .github/workflows/e2e-certificate.yml` (install actionlint if needed) — must be clean. Also confirm the referenced paths/manifests/suite dir exist.

**Note on final validation:** after all tasks, the whole-branch review is static (manifests/YAML/Go correctness). The definitive gate is the `e2e-certificate` job running **green in CI**, which requires pushing the branch — flag this at handoff; expect one or more CI-iteration rounds (image build, RBAC gaps, timing) that can only surface in a real run.

---

## Self-Review

**1. Spec coverage:** in-cluster backend (Tasks 1,7) ✓; cert-manager install (7) ✓; Postgres in-cluster (1,7) ✓; SA/RBAC → IsRunningInCluster (1) ✓; LoadBalancer reach + FASTGATEWAY_API_URL (1,7) ✓; in_cluster seed (2) ✓; harness cert/export/CSR/verifying-TLS/cluster-read helpers (3) ✓; Tier 1 server (4) ✓; Tier 2 managed (5) + CSR (6) ✓; the §10 CSR-retrieval gap workaround (6, read from cluster) ✓; readiness/short distributor interval (1,4,5,6) ✓. Deferred (Tier 3, ACME) correctly absent.

**2. Placeholder scan:** the "confirm X" steps (CRD groups, Dockerfile port, config env names, ConnectionType value, CertificateRequest name convention) are explicit read-and-confirm instructions with the file to check, not TODOs. Verification is compile/dry-run/actionlint because the suite can't run locally (stated in Global Constraints).

**3. Type consistency:** the harness helper names/signatures in Task 3 are consumed verbatim by Tasks 4/5/6; `cert-<id>` leaf/CR name convention is used consistently (matches `cert.Config.SecretName`/`CertificateName` from the shipped feature); `FASTGATEWAY_API_URL` is the existing harness config env (no code change to repoint); `ApproveAllStages` reused for cert + export approvals (matches on EntityID).

**Baked-in rulings:** separate job/suite; single-cluster control==tenant with in_cluster project; backend-as-pod reached via LoadBalancer; short `CERT_DISTRIBUTOR_INTERVAL`; CSR signed-cert read from the cluster (product gap flagged, not fixed here); local gate = compile+vet+dry-run+actionlint, real gate = CI run on push.
