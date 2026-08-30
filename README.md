# FastGateway

FastGateway is a web-based management interface for the Kubernetes Gateway API. It provides a user-friendly way to manage Gateways, HTTPRoutes, and related resources without hand-writing Kubernetes manifests.

This repository contains the **backend** (Go REST API). The frontend lives in a separate repository: [`fastgateway-dev/frontend-v2`](https://github.com/fastgateway-dev/frontend-v2).

## Features

- **Project Management**: Manage multiple Kubernetes clusters from a single interface
- **Domain Templates**: Create reusable templates with exposure types, annotations, TLS policies, and ports
- **Domain Management**: Create and manage Gateway resources with inherited template settings
- **Route Management**: Create HTTPRoutes with path matching, header matching, and backend configuration
- **Team-based Access Control**: Organize users into teams with different permission levels
- **Approval Workflow**: Route changes require approval before being applied to Kubernetes
- **Audit Logging**: Track all changes made through the system
- **Service Discovery**: Automatically discover namespaces and services from your Kubernetes cluster

## Architecture

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│                 │     │                 │     │                 │
│    Frontend     │────▶│    Backend      │────▶│   PostgreSQL    │
│  (frontend-v2)  │     │  (this repo)    │     │                 │
│                 │     │                 │     │                 │
└─────────────────┘     └────────┬────────┘     └─────────────────┘
                                 │
                                 │ Kubernetes API
                                 ▼
                        ┌─────────────────┐
                        │                 │
                        │   Kubernetes    │
                        │    Cluster      │
                        │                 │
                        └─────────────────┘
```

## Prerequisites

- Go 1.25+
- PostgreSQL (a running instance reachable from the backend)
- A Kubernetes cluster with Gateway API CRDs installed
- [Envoy Gateway](https://gateway.envoyproxy.io/) installed in your cluster (used as the gateway controller)

## Building

```bash
go build -o fastgateway ./cmd/server
```

or, via the Makefile:

```bash
make build
```

## Configuration

The server is configured entirely through environment variables (see `internal/config/config.go` for the full list). At minimum, set:

| Variable | Description |
|----------|-------------|
| `DATABASE_HOST`, `DATABASE_PORT`, `DATABASE_USER`, `DATABASE_PASSWORD`, `DATABASE_NAME` | PostgreSQL connection |
| `JWT_SECRET` | Secret used to sign JWT access/refresh tokens (required) |
| `ENCRYPTION_KEY` | 32-byte key used to encrypt stored Kubernetes tokens (required) |
| `API_PORT` | Port the API listens on (default `8081`) |
| `ADMIN_PASSWORD` | Password for the seeded admin account (**required** — there is no default) |
| `ADMIN_USERNAME`, `ADMIN_EMAIL` | Seeded admin account identity (defaults `admin` / `admin@fastgateway.local`) |
| `CORS_ALLOWED_ORIGINS` | Comma-separated list of allowed CORS origins |

Database migrations run automatically on server startup.

## Running Locally

Start a PostgreSQL instance of your own (locally installed, or any container runtime you prefer), then:

```bash
export DATABASE_HOST=localhost
export DATABASE_USER=fastgateway
export DATABASE_PASSWORD=fastgateway
export DATABASE_NAME=fastgateway
export JWT_SECRET=change-me
export ENCRYPTION_KEY=$(openssl rand -hex 16)

go run cmd/server/main.go
```

or, via the Makefile:

```bash
make dev-backend
```

The API listens on `http://localhost:8081` by default. To use the FastGateway UI, run [`frontend-v2`](https://github.com/fastgateway-dev/frontend-v2) separately and point it at this backend.

## Running Tests

```bash
go test ./...
```

or:

```bash
make test
```

End-to-end tests live in `e2e/` and require a real Kubernetes cluster with Envoy Gateway installed — see [CONTRIBUTING.md](CONTRIBUTING.md) for details.

## Registering a Kubernetes Cluster

To register a Kubernetes cluster as a project in FastGateway:

### Step 1: Apply the RBAC Configuration

Create a ServiceAccount, ClusterRole, ClusterRoleBinding, and a long-lived token Secret for FastGateway to use. An example manifest is provided at `e2e/rbac.yaml` — adapt it to your environment and apply it:

```bash
kubectl apply -f e2e/rbac.yaml
```

### Step 2: Create the FastGateway Namespace

FastGateway deploys all Gateway resources to a dedicated namespace:

```bash
kubectl create namespace fastgateway-system
```

### Step 3: Install Envoy Gateway (if not already installed)

FastGateway uses Envoy Gateway as the gateway controller. Install it following the [official guide](https://gateway.envoyproxy.io/docs/tasks/quickstart/):

```bash
helm upgrade --install eg oci://docker.io/envoyproxy/gateway-helm --version v1.8.0 -n envoy-gateway-system --create-namespace
```

### Step 4: Get the API Server URL and Token

Get your Kubernetes API server URL:

```bash
kubectl cluster-info | grep "Kubernetes control plane"
```

Get the ServiceAccount token created in Step 1:

```bash
kubectl get secret fastgateway-token -o jsonpath='{.data.token}' | base64 -d
```

### Step 5: Register the Project

Using the FastGateway UI (or the API directly), create a new project with:

- **Name**: A friendly name for your cluster
- **Kubernetes API URL**: The API server URL from Step 4
- **Kubernetes Token**: The token from Step 4

Then test connectivity and create the project.

## RBAC Permissions Explained

| Resource | Permissions | Purpose |
|----------|-------------|---------|
| `namespaces` | get, list, create | List namespaces and create `fastgateway-system` |
| `services`, `endpoints` | get, list | Service discovery for route backends |
| `secrets` | get, list | Access TLS certificates |
| `pods` | get, list | Validate Envoy Gateway installation |
| `deployments` | get, list | Validate Envoy Gateway installation |
| `gatewayclasses` | full CRUD | Create/manage GatewayClass resources |
| `gateways` | full CRUD | Create/manage Gateway resources |
| `httproutes` | full CRUD | Create/manage HTTPRoute resources |
| `grpcroutes` | full CRUD | gRPC route support |
| `tcproutes` | full CRUD | TCP route support |
| `tlsroutes` | full CRUD | TLS passthrough support |
| `referencegrants` | full CRUD | Cross-namespace references |

## Troubleshooting

### Connection Refused Error

If you get a connection refused error when registering a project, ensure the Kubernetes API server is reachable from wherever the FastGateway backend is running, and that firewall rules allow access to the API server port (typically 6443).

### Envoy Gateway Not Found

If domain template creation fails with "Envoy Gateway not found":

1. Verify Envoy Gateway is installed:
   ```bash
   kubectl get pods -n envoy-gateway-system
   ```
2. Ensure the `envoy-gateway-system` namespace exists and its deployments are running.

### Permission Denied

If operations fail with permission errors, verify the ServiceAccount has the correct ClusterRole binding:

```bash
kubectl get clusterrolebinding fastgateway -o yaml
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for how to build, test, and contribute — including how to run the e2e suite.

## Security

See [SECURITY.md](SECURITY.md) for how to report a vulnerability.

## License

FastGateway is licensed under the [Apache License 2.0](LICENSE).
