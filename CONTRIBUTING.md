# Contributing to FastGateway

Thanks for your interest in contributing! This document covers how to build, test, and lint the backend, and how the end-to-end (e2e) test suite works.

This repository contains the **backend** only. For frontend contributions, see [`fastgateway-dev/frontend-v2`](https://github.com/fastgateway-dev/frontend-v2).

## Prerequisites

- Go 1.25+
- PostgreSQL (for running the server and repository-layer tests)
- [`golangci-lint`](https://golangci-lint.run/) (for linting)
- A Kubernetes cluster with [Envoy Gateway](https://gateway.envoyproxy.io/) installed (only required for the e2e suite)

## Building

```bash
go build ./...
```

or:

```bash
make build
```

## Running Unit Tests

```bash
go test ./... -count=1
```

or:

```bash
make test
```

This is the same command run in CI (`.github/workflows/ci.yml`).

## Linting

```bash
golangci-lint run ./...
```

or:

```bash
make lint
```

The linter configuration is in `.golangci.yml`. It currently enables a conservative starting set of linters; more will be enabled over time as pre-existing findings are cleaned up. Please don't add new lint issues in your changes.

## Code Style

Run `gofmt` (or `go fmt ./...`) before submitting a change. CI enforces formatting as part of the lint step.

## The E2E Test Suite

The e2e suite lives under `e2e/` and is written in Go, guarded by the `e2e` build tag so it never runs as part of a normal `go build` or `go test ./...`:

```go
//go:build e2e
```

Run it explicitly with:

```bash
go test -tags e2e ./e2e/... -p 1
```

`-p 1` is required, not optional: it forces Go to run e2e packages one at a time instead of its default concurrent-per-package scheduling. Several suites mutate shared in-cluster state (e.g. `e2e/suites/httproute`'s health-check and load-balancing tests scale the shared `podinfo` Deployment to 0 and 3 replicas) and would otherwise interfere with other packages' tests nondeterministically.

The e2e suite requires a real Kubernetes cluster with Envoy Gateway installed — it deploys actual Gateway API resources (Gateways, HTTPRoutes, SecurityPolicies, etc.) and exercises them over the network. It is not run as part of the default unit test job; it has its own `e2e` job in `.github/workflows/ci.yml` (plus a manual `.github/workflows/e2e-version-matrix.yml` covering the full Envoy Gateway/Gateway API compatibility matrix), and its setup requirements are documented separately in `e2e/README.md`. Test data is seeded with `go run ./cmd/e2e-seed` (a Go replacement for the retired `bootstrap.py`).

Test certificates used by the e2e suite live under `e2e/testdata/certificate/` and are generated with [`step`](https://smallstep.com/docs/step-cli/) via `e2e/testdata/certificate/generate.sh`. Only `.crt` files are tracked in git — private keys (`.key`, `.pem`) are gitignored and regenerated locally/in CI as needed.

## Submitting Changes

1. Fork the repository and create a branch for your change.
2. Make your change, with tests where applicable.
3. Ensure `go build ./...`, `go test ./... -count=1`, and `golangci-lint run ./...` all pass locally.
4. Open a pull request describing what changed and why.

## Reporting Bugs and Requesting Features

Please open a GitHub issue. For security vulnerabilities, see [SECURITY.md](SECURITY.md) instead — do not open a public issue.
