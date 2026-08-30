# E2E Test Certificates

Test PKI for FastGateway TLS/mTLS E2E testing. Generated using [step CLI](https://smallstep.com/docs/step-cli/).

**Nothing in this directory is committed except this file and `generate.sh`.**
All `.crt`/`.key` output is gitignored (see the repo `.gitignore`) and is
regenerated fresh every time: CI installs a pinned `step` CLI and runs
`generate.sh` before creating Kubernetes secrets (`e2e/deps/create-secrets.sh`)
and before the Go e2e suite runs, so both consumers always see the same
run's output. Do the same locally before running the e2e suite or
`create-secrets.sh` against your own cluster.

## Generate

```bash
cd e2e/testdata/certificate
chmod +x generate.sh
./generate.sh
```

## Structure

```
certificate/
├── tls.crt             # Domain TLS cert (self-signed, for Gateway listener)
├── tls.key             # Domain TLS private key
├── tls1.crt/key        # TLS cert for tls1.fastgateway.local (self-signed)
├── tls2.crt/key        # TLS cert for tls2.fastgateway.local (self-signed)
├── tls3.crt/key        # TLS cert for tls3.fastgateway.local (self-signed)
├── root-ca-1/          # Backend TLS testing
├── root-ca-2/          # Backend mTLS testing
├── root-ca-3/          # Domain mTLS / Client mTLS (inbound)
│   ├── ...existing certs...
│   ├── mtls1.crt/key   # mTLS cert for mtls1.fastgateway.local
│   ├── mtls2.crt/key   # mTLS cert for mtls2.fastgateway.local
│   └── mtls3.crt/key   # mTLS cert for mtls3.fastgateway.local
├── root-ca-4/          # Spare / additional testing
│
└── Each CA directory contains:
    ├── root-ca.crt     # CA certificate
    ├── root-ca.key     # CA private key
    ├── server-1.crt/key
    ├── server-2.crt/key
    ├── client-1.crt/key
    └── client-2.crt/key
```

## Certificate Details

### Domain TLS (tls.crt / tls.key)

Self-signed certificate used for the Gateway listener (Envoy frontend TLS termination).

| Field | Value |
|-------|-------|
| Subject | `fastgateway.local` |
| SAN | `api.fastgateway.local` |
| Type | ECDSA P-256, self-signed |
| Validity | 2026-08-29 to 2027-08-29 |

**Usage**: Stored in a K8s TLS Secret referenced by the Gateway resource. All HTTPS routes on `api.fastgateway.local` use this cert. Clients connect with `curl -k` or `--cacert tls.crt` to trust it.

### TLS Domain Certificates (tls1/tls2/tls3)

Individual self-signed certificates for dedicated TLS domains.

| Cert | SAN |
|------|-----|
| tls1.crt/key | `tls1.fastgateway.local` |
| tls2.crt/key | `tls2.fastgateway.local` |
| tls3.crt/key | `tls3.fastgateway.local` |

**Usage**: Same as `tls.crt` — store in K8s TLS Secrets, reference from Gateway listeners for each domain.

### mTLS Domain Certificates (mtls1/mtls2/mtls3)

Signed by Root CA 3. Used for domains that require client certificate validation.

| Cert | SANs |
|------|------|
| root-ca-3/mtls1.crt/key | `mtls1.fastgateway.local`, `localhost` |
| root-ca-3/mtls2.crt/key | `mtls2.fastgateway.local`, `localhost` |
| root-ca-3/mtls3.crt/key | `mtls3.fastgateway.local`, `localhost` |

**Usage**: Store in K8s TLS Secrets for Gateway listeners. Configure domain mTLS settings with CA 3's `root-ca.crt`. Test inbound mTLS with CA 3's client certs:
```bash
curl --cert root-ca-3/client-1.crt --key root-ca-3/client-1.key --cacert root-ca-3/mtls1.crt https://mtls1.fastgateway.local/path
```

---

### Root CA 1 — Backend TLS

For testing TLS connections from gateway to backend (`backend-tls`).

| Cert | SANs |
|------|------|
| server-1 | `backend-tls-server-1`, `backend-tls-server-1.default.svc.cluster.local`, `localhost` |
| server-2 | `backend-tls-server-2`, `backend-tls-server-2.default.svc.cluster.local`, `localhost` |
| client-1 | `gateway-client-1.fastgateway.local` |
| client-2 | `gateway-client-2.fastgateway.local` |

**Usage**: Deploy a TLS backend using `server-1.crt/key`. Configure the route's backend with `caCertificateRefs` pointing to a K8s Secret containing `root-ca.crt`.

### Root CA 2 — Backend mTLS

For testing mTLS connections from gateway to backend (`backend-mtls`).

| Cert | SANs |
|------|------|
| server-1 | `backend-mtls-server-1`, `backend-mtls-server-1.default.svc.cluster.local`, `localhost` |
| server-2 | `backend-mtls-server-2`, `backend-mtls-server-2.default.svc.cluster.local`, `localhost` |
| client-1 | `gateway-client-1.fastgateway.local` |
| client-2 | `gateway-client-2.fastgateway.local` |

**Usage**: Deploy a mTLS backend using `server-1.crt/key` and `root-ca.crt` (to verify gateway's client cert). Configure the route's backend with `caCertificateRefs` (root-ca.crt) and `clientCertificateRef` (client-1.crt + client-1.key).

### Root CA 3 — Domain mTLS / Client mTLS (Inbound)

For testing inbound client certificate validation (`domain-settings-mtls`, `client-mode-mtls`).

| Cert | SANs |
|------|------|
| server-1 | `api.fastgateway.local`, `localhost` |
| server-2 | `api2.fastgateway.local`, `localhost` |
| client-1 | DNS: `client-1.fastgateway.local`, URI: `spiffe://fastgateway.local/client-1` |
| client-2 | DNS: `client-2.fastgateway.local`, URI: `spiffe://fastgateway.local/client-2` |

**Usage**:
1. **Domain mTLS**: Create a K8s Secret with `root-ca.crt`, configure domain settings with mTLS enabled pointing to this CA.
2. **Client mTLS**: Create clients with mTLS enabled. Use client cert SHA-256 hash or SANs for XFCC header matching. Test with:
   ```bash
   curl --cert client-1.crt --key client-1.key --cacert <gateway-ca> https://api.fastgateway.local/path
   ```

### Root CA 4 — Spare

For additional testing scenarios (e.g., cross-CA rejection, CA rotation).

| Cert | SANs |
|------|------|
| server-1 | `spare-server-1.default.svc.cluster.local`, `localhost` |
| server-2 | `spare-server-2.default.svc.cluster.local`, `localhost` |
| client-1 | DNS: `spare-client-1.fastgateway.local`, URI: `spiffe://fastgateway.local/spare-client-1` |
| client-2 | DNS: `spare-client-2.fastgateway.local`, URI: `spiffe://fastgateway.local/spare-client-2` |

**Usage**: Use to test negative cases — e.g., present a CA-4 client cert to a domain that only trusts CA-3. Should be rejected.

## Test Mapping

| E2E Test | Root CA | Certs Used |
|----------|---------|------------|
| `backend-tls` | CA 1 | server-1 (backend), root-ca (gateway trusts backend) |
| `backend-mtls` | CA 2 | server-1 (backend), client-1 (gateway presents to backend), root-ca (both sides) |
| `domain-settings-mtls` | CA 3 | root-ca (domain CA config), client-1/2 (test inbound) |
| `client-mode-mtls` | CA 3 | client-1/2 (per-client XFCC matching via hash/SAN) |
| Negative/cross-CA | CA 4 | client-1 (should be rejected by CA-3 domain) |

## Useful Commands

```bash
# View certificate details
step certificate inspect root-ca-1/server-1.crt

# Get SHA-256 fingerprint (for mTLS hash matching)
step certificate fingerprint root-ca-3/client-1.crt

# Get SANs from a cert
step certificate inspect root-ca-3/client-1.crt --format json | jq '.extensions.subject_alternative_name'

# Verify a cert against its CA
step certificate verify root-ca-1/server-1.crt --roots root-ca-1/root-ca.crt
```
