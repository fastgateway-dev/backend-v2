# FastGateway E2E Testing Infrastructure

This directory contains test services and documentation for end-to-end testing of FastGateway features.

## Services

### jwt-server (Port 9000)

A test JWT server that generates RS256-signed tokens for JWT validation testing.

**Endpoints:**
- `GET /jwks` - Returns JWKS public keys
- `POST /token` - Generates a signed JWT token
- `GET /health` - Health check

**Usage:**
```bash
# Generate a token
curl -X POST http://localhost:9000/token \
  -H "Content-Type: application/json" \
  -d '{"aud":"my-api","claims":{"scope":"api:read"}}'
```

### external-auth (Port 9001)

A test external authorization server for ext-auth (SecurityPolicy extAuth) testing.

**Endpoints:**
- `POST /auth` - Checks `x-ext-auth-allow` header. Returns 200 if `true`, 403 otherwise.
- `GET /health` - Health check

**Response Headers:**
- `x-auth-decision: allowed` or `x-auth-decision: denied`
- `x-auth-timestamp: <RFC3339>`

**Usage:**
```bash
# Allow request
curl -X POST http://localhost:9001/auth -H "x-ext-auth-allow: true"
# Response: 200 OK

# Deny request
curl -X POST http://localhost:9001/auth -H "x-ext-auth-allow: false"
# Response: 403 Forbidden

# No header (denied)
curl -X POST http://localhost:9001/auth
# Response: 403 Forbidden
```

### ext-proc-server (Port 9004)

A test gRPC external processing server for Envoy ext-proc (EnvoyExtensionPolicy extProc) testing. Implements the `envoy.service.ext_proc.v3.ExternalProcessor` streaming RPC.

**Behavior:**
- **Request headers** — Logs all headers and continues
- **Response headers** — Adds `x-ext-proc-processed: true` header and continues
- **Request/Response body** — Passes through unchanged

**gRPC Services:**
- `envoy.service.ext_proc.v3.ExternalProcessor/Process` - Bidirectional streaming ext-proc
- `grpc.health.v1.Health` - Health check

**Verification:**
```bash
# Check health via grpcurl
grpcurl -plaintext localhost:9004 grpc.health.v1.Health/Check

# After deploying a route with ext-proc, verify the response header:
curl -s -D - https://example.com/path -k | grep x-ext-proc-processed
# x-ext-proc-processed: true
```

## Running Services

There is no docker-compose setup for these services. They are Go binaries (`e2e/servers/<name>`) built into container images and `kind load`ed into the test cluster as Kubernetes Deployments -- see the manifests under `e2e/deps/` (`jwt-server.yaml`, `external-auth.yaml`, `grpc-external-auth.yaml`, `ext-proc-server.yaml`) and the `e2e` job in `.github/workflows/ci.yml`, which builds and loads them on every run.

To run one locally without a cluster, just `go run` it directly, e.g.:

```bash
cd e2e/servers/jwt-server && PORT=9000 go run .
```

## E2E Test Suite

The e2e test suite itself lives under `e2e/suites/` (Go, guarded by the `e2e` build tag) and is documented in [CONTRIBUTING.md](../CONTRIBUTING.md#the-e2e-test-suite). Run it with:

```bash
go test -tags e2e ./e2e/... -p 1
```

against a Kubernetes cluster with Envoy Gateway installed and a running FastGateway backend seeded via `go run ./cmd/e2e-seed` (see `.github/workflows/ci.yml`'s `e2e` job for the full setup sequence). `-p 1` is required, not optional: some suites mutate shared in-cluster state (e.g. scaling the `podinfo` Deployment) and cannot run concurrently with other packages.

A previous Python/pytest port of this suite (`e2e/regression/`, `e2e/bootstrap.py`) has been retired now that all of it is ported to Go under `e2e/suites/`.

`E2E_TEST-v1.6.2-v1.4.1.md` is a historical record of the last manual, pre-automation test pass against Envoy Gateway 1.6.2 / Gateway API 1.4.1; `E2E_TEST_TEMPLATE.md` is the template it was copied from. Both are kept for reference, but the process they describe is superseded by `.github/workflows/e2e-version-matrix.yml`.
