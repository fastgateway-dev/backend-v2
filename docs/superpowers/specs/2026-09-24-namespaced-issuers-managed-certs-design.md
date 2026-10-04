# Namespaced cert-manager `Issuer`s for Managed Certificates — Design

**Status:** approved design (in chat), pre-plan.
**Applies to:** the managed-certificate feature shipped in v0.1.0.

## 1. Problem

FastGateway's managed certificates are issued by cert-manager. For every
FastGateway issuer, the backend creates cluster-scoped cert-manager
**`ClusterIssuer`** objects:

- a shared `fgw-selfsigned` (`spec.selfSigned: {}`) that bootstraps CA certs;
- a per-issuer `iss-<id>` CA issuer (`spec.ca.secretName: ca-<id>`);
- (for ACME issuers) an `iss-<id>` ACME issuer referencing account-key and
  DNS-solver secrets.

A **`ClusterIssuer` resolves all of its secret references in cert-manager's
`--cluster-resource-namespace`**, which defaults to the namespace
cert-manager itself runs in (typically `cert-manager`). But FastGateway
writes every secret those issuers need — the CA keypair (`ca-<id>`), ACME
account keys, DNS-solver credentials — into its control-plane namespace
`fastgateway-system`. So on any cluster whose cert-manager uses the default
cluster-resource-namespace, the CA `ClusterIssuer` reports:

```
Error getting keypair for CA issuer: secrets "ca-<id>" not found
```

and never becomes Ready, so no leaf certificate can issue.

The only workaround today is setting cert-manager's global
`--cluster-resource-namespace=fastgateway-system`. That is a **single
cluster-wide setting for the entire cert-manager instance**: changing it
also moves where cert-manager looks for the secrets of *every other*
`ClusterIssuer` on the cluster (e.g. an operator's existing ACME/Let's
Encrypt issuer and its DNS credentials), breaking them. It is therefore not
an acceptable requirement to impose on a shared cluster.

## 2. Root observation

Every cert-manager object FastGateway creates for managed certificates
already lives in `fastgateway-system`:

- CA `Certificate` + its `ca-<id>` Secret (`certificate_issuer_service.go`,
  `ApplyNamespaced`, namespace = `controlPlane.Namespace()` = `fastgateway-system`).
- Leaf `Certificate` / `CertificateRequest` + their secrets
  (`managed_certificate_service.go`, `Namespace: s.controlPlane.Namespace()`).
- ACME account-key and DNS-solver secrets (`certificate_issuer_service.go`,
  `ApplyNamespaced`).
- The Gateways/domains that consume the served TLS secret (that is what
  `kubernetes.FastGatewayNamespace = "fastgateway-system"` denotes).

Only the **issuer objects** are cluster-scoped. A namespaced cert-manager
**`Issuer`** resolves its secret references in *its own* namespace. So an
`Issuer` in `fastgateway-system` finds `ca-<id>` (and ACME secrets) natively
— no `--cluster-resource-namespace` dependency, and no effect on any other
issuer on the cluster.

## 3. Goal / non-goals

**Goal:** issue managed certificates from namespaced cert-manager `Issuer`s
in `fastgateway-system` instead of `ClusterIssuer`s, so managed certificates
work against a stock cert-manager and never interfere with other issuers.

**Non-goals:**
- No migration of existing issuers/certificates. This is a **clean break**
  (pre-1.0): after upgrading, operators recreate their FastGateway issuers.
- No change to the two-tier bootstrap→CA structure, to the certdist
  distributor, to export, or to the client-cert/CSR flows beyond the
  issuer-kind switch.

## 4. Design

Switch all three issuer kinds from cluster-scoped `ClusterIssuer` to
namespaced `Issuer` in `fastgateway-system`, and point every certificate's
`issuerRef.kind` at `Issuer`. The shared `fgw-selfsigned` bootstrap issuer
stays a single, reused object — now `Issuer/fgw-selfsigned` in
`fastgateway-system`. Certificate `issuerRef`s carry no namespace (a
Certificate references an Issuer in its own namespace), and every
certificate FastGateway creates is already in `fastgateway-system`, so the
references resolve in-namespace.

### 4.1 cert-manager builders — `internal/kubernetes/certmanager.go`

- `SelfSignedClusterIssuer(name)` → `SelfSignedIssuer(name, namespace)`:
  emit `kind: Issuer` with `metadata.namespace`.
- `CAClusterIssuer(name, caSecretName)` → `CAIssuer(name, namespace, caSecretName)`:
  `kind: Issuer` + `metadata.namespace`.
- `ACMEClusterIssuer(cfg)` → `ACMEIssuer(cfg)`: `kind: Issuer`; add a
  `Namespace` field to `ACMEIssuerConfig` and set `metadata.namespace`.
- In `CACertificate`, `LeafCertificate`, and `CertificateRequestObject`,
  change every `issuerRef.kind` from `"ClusterIssuer"` to `"Issuer"`.

### 4.2 Issuer service — `internal/services/certificate_issuer_service.go`

- Create and delete issuers via `kubernetes.CertManagerIssuerGVR` (already
  defined in `gvr.go`) using `controlPlane.ApplyNamespaced` /
  `controlPlane.Delete` (namespaced), passing `controlPlane.Namespace()`
  (`fastgateway-system`) as the issuer namespace.
- The CA `Certificate`'s `issuerRef` already targets `fgw-selfsigned`; only
  its `kind` changes (via the builder).

### 4.3 Managed-certificate service — `internal/services/managed_certificate_service.go`

- No structural change: the leaf `Certificate` / `CertificateRequest` are
  already created in `fastgateway-system`; their `issuerRef.kind` becomes
  `Issuer` via the builders. Update the field reference (see 4.4).

### 4.4 Data model / naming

- Rename `IssuerConfig.ClusterIssuerName` (json `clusterIssuerName`) →
  `IssuerName` (json `issuerName`) in `internal/models/certificate_issuer.go`,
  and the builder config field `LeafCertConfig.IssuerClusterIssuerName` /
  `CertificateRequestConfig.IssuerClusterIssuerName` → `IssuerName`.
  Update all references. Because this is a clean break, no data-compat shim
  is needed; existing rows are abandoned with their orphaned ClusterIssuers.

### 4.5 RBAC

The backend ClusterRole already grants `cert-manager.io: issuers`. Remove
the now-unused `clusterissuers` grant (`e2e/deps/backend/rbac.yaml`; the
operator-facing RBAC docs, if any, follow the same edit).

### 4.6 e2e — `.github/workflows/e2e-certificate.yml`

**Remove** the `--set "extraArgs={--cluster-resource-namespace=fastgateway-system}"`
line added to the cert-manager Helm install. Running the certificate suite
against a **default** cert-manager is the end-to-end proof of this change:
if leaf issuance succeeds without the flag, namespaced `Issuer`s resolve
their CA secret in-namespace as intended.

## 5. Upgrade / operator note (clean break)

After upgrading, the previously-created `ClusterIssuer`s (`fgw-selfsigned`,
`iss-*`) are inert orphans — FastGateway no longer references them. Operators
recreate their FastGateway issuers (which now materialize as namespaced
`Issuer`s and work on a stock cert-manager), and may remove the orphans:

```
kubectl delete clusterissuer -l app.kubernetes.io/managed-by=fastgateway
```

The selector matches only the old cluster-scoped issuers; the new namespaced
`Issuer`s are a distinct resource, so reusing the `fgw-selfsigned` name does
not collide. This goes in the release notes.

## 6. Testing

- **Unit / golden fixtures:** the cert-manager object golden fixtures and the
  builder/service unit tests assert `kind: Issuer`, a `metadata.namespace`
  of `fastgateway-system`, and `issuerRef.kind: Issuer`. Existing tests that
  assert `ClusterIssuer` are updated.
- **e2e:** the existing `certificate` suite (Tier 1 server + Tier 2 managed
  client + CSR) drives full issuer→cert→attach→serve/mTLS. With the
  cluster-resource-namespace flag removed, a green run proves managed
  certificates issue against a default cert-manager — directly reproducing
  and verifying the fix for the "CA issuer can't read the secret" failure.

## 7. Out of scope

- Migration of existing `ClusterIssuer`s or already-issued certificates.
- Making the control-plane namespace configurable (it remains
  `fastgateway-system`).
- Any change to certificate distribution, export, or the approval flow.
