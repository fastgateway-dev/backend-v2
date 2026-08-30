# FastGateway E2E Test Tracker

Tracks all validated features. Each entry records what was tested and the result.

Copy this template when testing a new Envoy Gateway / Gateway API version:
```
cp E2E_TEST_TEMPLATE.md E2E_TEST-v<EG_VERSION>-v<GWAPI_VERSION>.md
```

## Test Environment

- **Envoy Gateway Version**: `<EG_VERSION>`
- **Gateway API Version**: `<GWAPI_VERSION>`
- **Kubernetes Version**: `<K8S_VERSION>`
- Domain: `<DOMAIN>`
- Gateway LB IP: `<LB_IP>`
- TLS: TLSv1.3, HTTP/2, self-signed cert
- HTTP Backend: nginx-service (default namespace, port 80)
- gRPC Backend: podinfo (default namespace, port 9999, stefanprodan/podinfo with --grpc-port=9999)
- gRPC Proto Files: e2e/podinfo.proto, e2e/podinfo_version.proto, e2e/podinfo_info.proto, e2e/podinfo_delay.proto, e2e/podinfo_env.proto, e2e/podinfo_header.proto

## Test Checklist

### HTTP Route Features

- [ ] Health Check - Passive
- [ ] Health Check - Active
- [ ] Health Check (Active+Passive Combined)
- [ ] Fault Injection
- [ ] Direct Response
- [ ] Timeout (Route-Level)
- [ ] BTP Timeout (BackendTrafficPolicy-Level)
- [ ] URL Rewrite
- [ ] Compression
- [ ] Mirror
- [ ] Redirect
- [ ] Backend Weight
- [ ] Backend Failover
- [ ] CORS
- [ ] Retry
- [ ] Circuit Breaker
- [ ] Header Modifier
- [ ] Load Balancing
- [ ] Request Buffer
- [ ] Response Override

### Route Matching

- [ ] Route Matching - Headers
- [ ] Route Matching - Method
- [ ] Route Matching - Query Parameters

### Extensions

- [ ] Lua Extension
- [ ] WASM Extension
- [ ] External Processing (ext-proc)

### External Backends

- [ ] External Backend - FQDN
- [ ] External Backend - IP

### Backend TLS

- [ ] Backend TLS
- [ ] Backend mTLS

### Security - General Mode

- [ ] Security - General Mode IP Allowlisting
- [ ] Security - General Mode API Key
- [ ] Security - General Mode JWT
- [ ] Security General Mode - Combined (Multiple Feature Combinations)
- [ ] Security - General Mode OIDC
- [ ] Security - General Mode Header & Method Auth
- [ ] WAF (Web Application Firewall)

### External Authorization

- [ ] External Authorization (ext-auth) - HTTP with Default Headers
- [ ] External Authorization (ext-auth) - HTTP with headersToExtAuth
- [ ] External Authorization (ext-auth) - gRPC

### Rate Limiting

- [ ] Rate Limiting - Basic (General Mode)
- [ ] Rate Limiting - Per-IP (General Mode)
- [ ] Rate Limiting - Header-Based (General Mode)
- [ ] Rate Limit - Capabilities
- [ ] Rate Limit - Validation (Negative Tests)

### Client Mode (Per-Client Auth)

- [ ] Client Mode - API Key
- [ ] Client Mode - IP Allowlisting
- [ ] Client Mode - JWT
- [ ] Client Mode - External Authorization (ext-auth)
- [ ] Client Mode - Per-Client Rate Limiting
- [ ] Client Mode - Combined Auth
- [ ] Client Mode - mTLS
- [ ] Client Mode - Header & Method Auth

### Domain Settings

- [ ] Domain Mutual TLS - Strict
- [ ] Domain Mutual TLS - Optional
- [ ] Domain Mutual TLS - Multiple CA
- [ ] Domain Settings - TCP Keepalive
- [ ] Domain Settings - Client IP Detection

### gRPC - BTP Features

- [ ] gRPC BTP - Retry
- [ ] gRPC BTP - Circuit Breaker
- [ ] gRPC BTP - Compression
- [ ] gRPC BTP - Fault Injection
- [ ] gRPC BTP - Load Balancing
- [ ] gRPC BTP - Health Check (Passive)
- [ ] gRPC BTP - Health Check Active (GRPC)
- [ ] gRPC BTP - Rate Limit
- [ ] gRPC BTP - Request Buffer
- [ ] gRPC BTP - Response Override
- [ ] gRPC BTP - Timeout

### gRPC - Route Matching

- [ ] gRPC Basic - Service Exact Match
- [ ] gRPC Basic - Service + Method Exact Match
- [ ] gRPC Basic - Service Regex Match
- [ ] gRPC Basic - Catchall (Empty Matches)
- [ ] gRPC Basic - Header Match
- [ ] gRPC Basic - Weighted Backends
- [ ] gRPC Basic - Failover Backends

### gRPC - Route Features

- [ ] gRPC Route - Header Modifier
- [ ] gRPC Route - Mirror

### gRPC - Security

- [ ] gRPC Security - CORS
- [ ] gRPC Security - General Mode IP Allowlisting
- [ ] gRPC Security - General Mode API Key
- [ ] gRPC Security - General Mode JWT
- [ ] gRPC Security - External Authorization

### gRPC - Extensions

- [ ] gRPC Extension - Lua Inline Script
- [ ] gRPC Extension - WASM Filter
- [ ] gRPC Extension - WAF (Coraza)
- [ ] gRPC Extension - External Processing (ext-proc)

### gRPC - Client Mode

- [ ] gRPC Client Mode - API Key
- [ ] gRPC Client Mode - JWT
- [ ] gRPC Client Mode - IP Allowlisting
- [ ] gRPC Client Mode - Combined Auth (Multiple Clients)
- [ ] gRPC Client Mode - Rate Limiting
- [ ] gRPC Client Mode - Header & Method Auth

### gRPC - Validation

- [ ] gRPC - Validation (Negative Tests)
- [ ] gRPC - Reject Redirect
- [ ] gRPC - Reject Direct Response
- [ ] gRPC - Reject URL Rewrite
- [ ] gRPC - Reject Path Match
- [ ] gRPC - Reject Route Timeout
- [ ] HTTP - Reject gRPC Service Match

## Completed Tests

<!-- Add test entries below in this format:

### Feature Name

- **Route**: route-name (PathPrefix /path)
- **Config**: key config values
- **E2E**: what was tested
- **Result**: PASS / FAIL

If FAIL, add:
- **Issue**: description of failure
- **Notes**: workaround or root cause

-->
