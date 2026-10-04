# Namespaced cert-manager Issuers for Managed Certificates — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Issue managed certificates from namespaced cert-manager `Issuer`s in `fastgateway-system` instead of cluster-scoped `ClusterIssuer`s, so managed certificates work against a stock cert-manager and never interfere with other issuers on the cluster.

**Architecture:** All cert-manager objects FastGateway creates already live in `fastgateway-system`. Switching the three issuer kinds (self-signed bootstrap, CA, ACME) from `ClusterIssuer` to namespaced `Issuer`, and pointing every certificate's `issuerRef.kind` at `Issuer`, lets each issuer resolve its secret references in-namespace — removing the `--cluster-resource-namespace` requirement entirely. Clean break: no migration.

**Tech Stack:** Go 1.25, cert-manager `cert-manager.io/v1` unstructured objects, testify, golden-file fixtures under `internal/kubernetes/testdata/golden/certmanager/`, GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-09-24-namespaced-issuers-managed-certs-design.md`

## Global Constraints

- Control-plane namespace is fixed: `kubernetes.FastGatewayNamespace = "fastgateway-system"` (obtained at runtime via `s.controlPlane.Namespace()`).
- cert-manager objects use `apiVersion: cert-manager.io/v1`. Namespaced issuers are `kind: Issuer`; the GVR is `kubernetes.CertManagerIssuerGVR` (resource `issuers`), which already exists in `internal/kubernetes/gvr.go`.
- Clean break: no migration of existing `ClusterIssuer`s or issued certificates; no data-compat shim for the renamed jsonb field.
- `ControlPlaneClient.ApplyNamespaced(ctx, gvr, obj)` force-sets the object namespace to the configured namespace and creates it namespaced; `ApplyClusterScoped` does not. `Delete(ctx, gvr, name, namespaced bool)` — pass `true` for namespaced resources.
- Every step keeps the tree compiling: `go build ./...` and `go test ./internal/...` must pass at each commit.

## Review Focus

- **A cert builder left with `issuerRef.kind: "ClusterIssuer"`** (any of CA cert, leaf cert, CSR) → the leaf can't resolve its Issuer, issuance fails only on a live cluster. Pinned by the golden fixtures in Task 1 asserting `kind: Issuer` on `ca-certificate.yaml`, `leaf-certificate.yaml`, `leaf-certificate-client-auth.yaml`, `certificate-request.yaml`.
- **An issuer builder missing `metadata.namespace`** → wrong placement/resolution. Pinned by the builder unit tests in Task 1 asserting `metadata.namespace == "fastgateway-system"`.
- **An issuer still applied via `ApplyClusterScoped`/`CertManagerClusterIssuerGVR`** (missed self-signed, CA, or ACME) → a `ClusterIssuer` is created and the original bug persists for that type. Pinned by the issuer-service test in Task 1 asserting the recorded apply GVR is `issuers` for all three.
- **Issuer `Delete` still targeting `CertManagerClusterIssuerGVR`** → the namespaced Issuer is never deleted (orphan) and cleanup silently misses it. Pinned by the delete test in Task 1.
- **The `IssuerConfig` jsonb field rename (`clusterIssuerName` → `issuerName`) not round-tripping through `Value()`/`Scan()`** → the stored issuer name is lost, so Delete/status can't find the issuer. Pinned by the `Value()`→`Scan()` round-trip test in Task 1.

---

### Task 1: Switch managed-certificate issuers to namespaced `Issuer`s

**Files:**
- Modify: `internal/models/certificate_issuer.go` (rename `IssuerConfig.ClusterIssuerName` → `IssuerName`)
- Modify: `internal/kubernetes/certmanager.go` (issuer builders + `issuerRef.kind`)
- Modify: `internal/services/certificate_issuer_service.go` (create/delete via namespaced Issuer)
- Modify: `internal/services/managed_certificate_service.go` (rename references)
- Modify/Test: `internal/kubernetes/certmanager_test.go`, `internal/models/certificate_issuer_test.go`, `internal/services/certificate_issuer_service_test.go`, `internal/services/managed_certificate_service_test.go`
- Modify: golden fixtures under `internal/kubernetes/testdata/golden/certmanager/` — rename `acme-clusterissuer.yaml` → `acme-issuer.yaml`; edit `ca-certificate.yaml`, `leaf-certificate.yaml`, `leaf-certificate-client-auth.yaml`, `certificate-request.yaml`

**Interfaces — Produces (names later code/tests rely on):**
- `kubernetes.SelfSignedIssuer(name, namespace string) *unstructured.Unstructured`
- `kubernetes.CAIssuer(name, namespace, caSecretName string) *unstructured.Unstructured`
- `kubernetes.ACMEIssuer(cfg ACMEIssuerConfig)` where `ACMEIssuerConfig` gains a `Namespace` field
- `CACertConfig.SelfSignedIssuerName`, `LeafCertConfig.IssuerName`, `CertificateRequestConfig.IssuerName` (renamed from `IssuerClusterIssuerName`)
- `models.IssuerConfig.IssuerName` (json `issuerName`), replacing `ClusterIssuerName`

- [ ] **Step 1: Update golden fixtures to expect `Issuer` (failing state).**

Rename `internal/kubernetes/testdata/golden/certmanager/acme-clusterissuer.yaml` → `acme-issuer.yaml`. In it, change `kind: ClusterIssuer` → `kind: Issuer` and add `namespace: fastgateway-system` under `metadata`. In `ca-certificate.yaml`, `leaf-certificate.yaml`, `leaf-certificate-client-auth.yaml`, and `certificate-request.yaml`, change the `issuerRef.kind` value from `ClusterIssuer` to `Issuer`. If `certmanager_test.go` references the fixture path `acme-clusterissuer.yaml`, update it to `acme-issuer.yaml`.

- [ ] **Step 2: Update/author builder unit tests (failing).**

In `internal/kubernetes/certmanager_test.go`, assert the new shapes. Add/adjust:

```go
func TestSelfSignedIssuer_NamespacedIssuer(t *testing.T) {
	obj := SelfSignedIssuer("fgw-selfsigned", "fastgateway-system")
	assert.Equal(t, "Issuer", obj.GetKind())
	assert.Equal(t, "fastgateway-system", obj.GetNamespace())
}

func TestCAIssuer_NamespacedIssuer(t *testing.T) {
	obj := CAIssuer("iss-1", "fastgateway-system", "ca-1")
	assert.Equal(t, "Issuer", obj.GetKind())
	assert.Equal(t, "fastgateway-system", obj.GetNamespace())
	ca, _, _ := unstructured.NestedString(obj.Object, "spec", "ca", "secretName")
	assert.Equal(t, "ca-1", ca)
}
```

For any existing test that calls `SelfSignedClusterIssuer`/`CAClusterIssuer`/`ACMEClusterIssuer` or asserts `issuerRef.kind == "ClusterIssuer"`, update the call/assertion to the new names and `"Issuer"`.

- [ ] **Step 3: Run the builder tests — verify they fail.**

Run: `go test ./internal/kubernetes/... -run 'Issuer|Certificate' -v`
Expected: FAIL (undefined `SelfSignedIssuer`/`CAIssuer`, or `kind` still `ClusterIssuer`, or golden mismatch).

- [ ] **Step 4: Change the issuer builders in `internal/kubernetes/certmanager.go`.**

`SelfSignedClusterIssuer` → `SelfSignedIssuer(name, namespace string)`:

```go
func SelfSignedIssuer(name, namespace string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "cert-manager.io/v1", "kind": "Issuer",
		"metadata": map[string]interface{}{"name": name, "namespace": namespace, "labels": managedByLabels()},
		"spec":     map[string]interface{}{"selfSigned": map[string]interface{}{}},
	}}
}
```

`CAClusterIssuer` → `CAIssuer(name, namespace, caSecretName string)`:

```go
func CAIssuer(name, namespace, caSecretName string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "cert-manager.io/v1", "kind": "Issuer",
		"metadata": map[string]interface{}{"name": name, "namespace": namespace, "labels": managedByLabels()},
		"spec":     map[string]interface{}{"ca": map[string]interface{}{"secretName": caSecretName}},
	}}
}
```

`ACMEClusterIssuer` → `ACMEIssuer`: add `Namespace` to `ACMEIssuerConfig` (`Name, Namespace, Server, Email, ...`), rename the func, set `kind: "Issuer"` and add `"namespace": cfg.Namespace` to `metadata` (the rest of the body is unchanged):

```go
func ACMEIssuer(cfg ACMEIssuerConfig) *unstructured.Unstructured {
	// ... unchanged acme map construction ...
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "cert-manager.io/v1", "kind": "Issuer",
		"metadata": map[string]interface{}{"name": cfg.Name, "namespace": cfg.Namespace, "labels": managedByLabels()},
		"spec":     map[string]interface{}{"acme": acme},
	}}
}
```

- [ ] **Step 5: Change `issuerRef.kind` in the three cert builders (same file).**

In `CACertificate` (issuerRef around the `SelfSignedIssuerName` line), `LeafCertificate`, and `CertificateRequestObject`, change every `"kind": "ClusterIssuer"` to `"kind": "Issuer"`. Rename the config fields `LeafCertConfig.IssuerClusterIssuerName` and `CertificateRequestConfig.IssuerClusterIssuerName` to `IssuerName`, and update the `issuerRef` `"name"` reference (`cfg.IssuerName` / `config.IssuerName`). Leave `CACertConfig.SelfSignedIssuerName` as-is (it already names the self-signed issuer).

- [ ] **Step 6: Rename the model field in `internal/models/certificate_issuer.go`.**

Change `ClusterIssuerName string \`json:"clusterIssuerName,omitempty"\`` → `IssuerName string \`json:"issuerName,omitempty"\``. In `internal/models/certificate_issuer_test.go`, add a jsonb round-trip test (or update an existing Value/Scan test):

```go
func TestIssuerConfig_IssuerName_RoundTrip(t *testing.T) {
	in := IssuerConfig{IssuerName: "iss-abc", CASecretName: "ca-abc"}
	v, err := in.Value()
	require.NoError(t, err)
	var out IssuerConfig
	require.NoError(t, out.Scan(v))
	assert.Equal(t, "iss-abc", out.IssuerName)
	assert.Equal(t, "ca-abc", out.CASecretName)
}
```

- [ ] **Step 7: Update the issuer service `internal/services/certificate_issuer_service.go`.**

Self-signed CA path — replace the three issuer applies:

```go
// self-signed bootstrap issuer (namespaced)
if err := s.controlPlane.ApplyNamespaced(ctx, kubernetes.CertManagerIssuerGVR, kubernetes.SelfSignedIssuer(selfSignedClusterIssuer, ns)); err != nil {
	return s.markError(iss, err)
}
caCert := kubernetes.CACertificate(kubernetes.CACertConfig{
	Name: caSecret, Namespace: ns, CommonName: input.CommonName, SecretName: caSecret,
	SelfSignedIssuerName: selfSignedClusterIssuer, KeyAlgorithm: input.KeyAlgorithm, KeySize: input.KeySize, DurationDays: input.DurationDays,
})
if err := s.controlPlane.ApplyNamespaced(ctx, kubernetes.CertManagerCertificateGVR, caCert); err != nil {
	return s.markError(iss, err)
}
if err := s.controlPlane.ApplyNamespaced(ctx, kubernetes.CertManagerIssuerGVR, kubernetes.CAIssuer(clusterIssuer, ns, caSecret)); err != nil {
	return s.markError(iss, err)
}
```

ACME path — pass the namespace and apply namespaced:

```go
acme := kubernetes.ACMEIssuer(kubernetes.ACMEIssuerConfig{
	Name: clusterIssuer, Namespace: ns, Server: input.Server, Email: input.Email,
	AccountSecretName: accountSecret, ProviderType: providerType, SolverSecretName: solverSecret,
	EABKeyID: input.EABKeyID, EABSecretName: eabSecret,
})
if err := s.controlPlane.ApplyNamespaced(ctx, kubernetes.CertManagerIssuerGVR, acme); err != nil {
	return s.markError(iss, err)
}
```

`Config` assignment: `ClusterIssuerName: clusterIssuer` → `IssuerName: clusterIssuer`. (The local var may stay named `clusterIssuer`; only the struct field changes. Optionally rename the local to `issuerName` for clarity.)

Delete path — namespaced Issuer GVR + namespaced delete:

```go
if iss.Config.IssuerName != "" {
	_ = s.controlPlane.Delete(ctx, kubernetes.CertManagerIssuerGVR, iss.Config.IssuerName, true)
}
```

- [ ] **Step 8: Update `internal/services/managed_certificate_service.go`.**

Wherever the leaf/CSR builders are called, the config field is now `IssuerName` — change `IssuerClusterIssuerName: issuer.Config.ClusterIssuerName` → `IssuerName: issuer.Config.IssuerName` (both the CSR `CertificateRequestConfig` and the managed `LeafCertConfig` call sites). Update any other `issuer.Config.ClusterIssuerName` reads to `.IssuerName`.

- [ ] **Step 9: Update the service tests.**

In `internal/services/certificate_issuer_service_test.go`, ensure the fake/mock control-plane records `(gvr, obj)` applies, and assert that creating a self-signed CA issuer applies **`CertManagerIssuerGVR`** (not `CertManagerClusterIssuerGVR`) for both the bootstrap and CA issuers, and that Delete calls `Delete(..., CertManagerIssuerGVR, iss.Config.IssuerName, true)`. Update every `ClusterIssuerName` reference to `IssuerName`. In `internal/services/managed_certificate_service_test.go`, update `ClusterIssuerName` → `IssuerName` in any issuer `Config` fixtures. If `internal/cluster/controlplane_test.go` builds a `ClusterIssuer` only as a generic apply example, leave it (it tests `ApplyClusterScoped` itself, not FastGateway issuers) — only change it if it references a renamed FastGateway builder.

- [ ] **Step 10: Run the full unit suite — verify green.**

Run: `go build ./... && go test ./internal/... && gofmt -l internal/ | (grep -v '^$' && exit 1 || true)`
Expected: build clean; all packages `ok`; gofmt lists nothing.

- [ ] **Step 11: Commit.**

```bash
git add internal/models/certificate_issuer.go internal/models/certificate_issuer_test.go \
  internal/kubernetes/certmanager.go internal/kubernetes/certmanager_test.go \
  internal/kubernetes/testdata/golden/certmanager/ \
  internal/services/certificate_issuer_service.go internal/services/certificate_issuer_service_test.go \
  internal/services/managed_certificate_service.go internal/services/managed_certificate_service_test.go
git commit -m "refactor(certs): issue managed certificates from namespaced Issuers"
```

---

### Task 2: Operational surface — RBAC, e2e workflow, upgrade note

**Files:**
- Modify: `e2e/deps/backend/rbac.yaml` (drop `clusterissuers`)
- Modify: `.github/workflows/e2e-certificate.yml` (remove `--cluster-resource-namespace`)
- Modify: `README.md` (managed-certs cert-manager note + v0.1.0 upgrade cleanup)

**Interfaces — Consumes:** the namespaced-Issuer behavior from Task 1 (issuers are now created with the `issuers` resource, in `fastgateway-system`).

- [ ] **Step 1: Drop the unused `clusterissuers` RBAC grant.**

In `e2e/deps/backend/rbac.yaml`, in the `cert-manager.io` rule, remove the `- clusterissuers` line from `resources` (keep `- issuers`, `- certificates`, `- certificaterequests`). Update the nearby comment that enumerates the resources to drop `clusterissuers`.

- [ ] **Step 2: Remove the cluster-resource-namespace flag from the e2e cert-manager install.**

In `.github/workflows/e2e-certificate.yml`, in the "Install cert-manager (Helm)" step, delete the line:
```
            --set "extraArgs={--cluster-resource-namespace=fastgateway-system}" \
```
and the explanatory comment block above the `helm install` that justified it. The install now uses default cert-manager settings — a green `e2e-certificate` run then proves managed certs issue against a stock cert-manager.

- [ ] **Step 3: Verify the workflow still lints.**

Run: `actionlint .github/workflows/e2e-certificate.yml`
Expected: no new errors (pre-existing SC2024/SC2034 shellcheck warnings, identical in kind to `e2e.yml`, are acceptable).

- [ ] **Step 4: Add the upgrade note to `README.md`.**

Under the Features/Configuration area, add a short note (a few lines) stating: managed certificates require cert-manager installed in the cluster; **no special cert-manager configuration is needed** (issuers are namespaced `Issuer`s in `fastgateway-system`); and, for installs upgrading from **v0.1.0**, the old cluster-scoped issuers are inert and can be removed with:

```
kubectl delete clusterissuer -l app.kubernetes.io/managed-by=fastgateway
```

- [ ] **Step 5: Commit.**

```bash
git add e2e/deps/backend/rbac.yaml .github/workflows/e2e-certificate.yml README.md
git commit -m "chore(certs): drop ClusterIssuer RBAC + cluster-resource-namespace, document upgrade"
```

---

## Notes for the executor

- The definitive end-to-end proof is the `e2e-certificate` CI job running green **after** Task 2 removes the `--cluster-resource-namespace` flag — that requires pushing the branch; expect a CI-iteration round. Unit tests + golden fixtures are the local gate.
- Do not add a migration path or a compat shim for the renamed jsonb field — the clean break is intentional (spec §3, §5).
