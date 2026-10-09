# L4 Streams (TCP/UDP) E2E Coverage — Design

**Status:** approved (brainstorm) · **Date:** 2026-10-09
**Feature under test:** L4 Streams — `docs/superpowers/specs/2026-10-09-l4-streams-tcp-udp-design.md`

## 1. Background & the gap

The L4 Streams feature (merged to `main`) adds port-keyed Streams with `TCPRoute`/`UDPRoute`
routing, a per-protocol `BackendTrafficPolicy` subset, and L4 metrics. It ships with unit tests
(`streamplan`, `routeplan/{tcproute,udproute}`, services, validation) and Postgres integration
tests (stream repo, L4 listener-port index, 23505→409), but **no end-to-end test sends real
TCP/UDP traffic through a provisioned Stream Gateway to a backend.** This adds that coverage,
mirroring the existing `e2e/suites/grpcroute` layout and the `e2e/suites/certificate` precedent.

Scope decision (brainstorm): **Standard** — core traffic + the feature's capabilities. CI
placement: **existing matrix, gated to the verified EG line** (skip 1.6.6/1.7.5, run 1.8.4/1.9.1).

## 2. Topology

Reuses the existing single kind cluster + cloud-provider-kind (LoadBalancer support) already
stood up by the e2e workflow. Unlike the HTTP suites, which target the one seeded Gateway's
`GATEWAY_IP`, **each Stream provisions its own Gateway → its own LoadBalancer Service**; tests
resolve that per-stream LB ingress address at runtime via the kube client. Streams are created
per-test through the control-plane API (not the seed), then deployed.

## 3. Test-double backend — `e2e/servers/l4-echo`

One small Go binary that listens on **both TCP and UDP** and echoes the received payload back,
prefixed with an instance identifier read from `POD_NAME` (downward API), so load-balancing
distribution across replicas is assertable — the L4 analogue of nginx/podinfo for the HTTP suites.

- Built and loaded into kind exactly like the other `e2e/servers/*` doubles (own `go.mod`,
  Dockerfile, `docker build` + `kind load`).
- Deployed as a Deployment (**2 replicas**) + a ClusterIP Service exposing the TCP and UDP ports,
  in the e2e dependency namespace. Manifest lives beside the other static deps.
- UDP echo uses a per-datagram read/write loop; TCP echo a per-connection copy loop.

## 4. Harness additions (`e2e/harness/`)

- `StreamGatewayAddr(ctx, streamName|gatewayName) (string, error)` — resolves the stream's own
  Gateway LoadBalancer Service `.status.loadBalancer.ingress[0]` via the existing kube client,
  with a readiness wait (poll until the LB IP is assigned). Distinct from the HTTP `GATEWAY_IP`.
- `DialTCP(ctx, addr, payload) ([]byte, error)` — connect, write, read with deadline.
- `DialUDP(ctx, addr, payload) ([]byte, error)` — send/recv **retry-to-deadline** (UDP is lossy;
  N attempts within a bounded window, success = any echo received).
- API helpers (`e2e/harness/api.go`): `CreateStream`, `CreateStreamRoute`, `DeployStreamRoute`,
  mirroring the existing domain/route helpers (create via API, then `POST .../routes/:id/deploy`).
- A stream-enabled Gateway Template helper (create a template with `enableStream: true`).

## 5. Suite + tests (`e2e/suites/stream/`)

`main_test.go` sets up the shared fixtures (stream-enabled template; echo backend assumed deployed
by CI) and performs **version gating** (see §6). Test files follow the `grpcroute` naming pattern:

| File | Asserts |
|---|---|
| `traffic_tcp_test.go` | `tcp:<port>` route → echo backend; `DialTCP` round-trip returns the sent payload. |
| `traffic_udp_test.go` | `udp:<port>` route → echo backend; `DialUDP` round-trip (retry-to-deadline). |
| `match_weighted_backends_test.go` | Two weighted K8s-Service backends; repeated dials hit >1 echo instance (distribution). |
| `btp_circuit_breaker_test.go` | TCP `maxConnections` enforced (excess concurrent conns rejected/queued). |
| `btp_load_balancing_test.go` | TCP + UDP LB algorithm applied; distribution shifts as configured. |
| `btp_health_check_test.go` | TCP passive + active-TCP health check marks a dead backend unhealthy; traffic avoids it. |
| `btp_timeout_test.go` | TCP connection/idle timeout applied. |
| `validation_reject_l7_test.go` | An L4 route carrying hostname/matches/filters is rejected (400) at write time. |
| `validation_port_collision_test.go` | A second `tcp:<port>` on the same stream → 409. |
| `metrics_test.go` | **Lenient**: the L4 metrics endpoint returns a populated structure whose connection/throughput counters increase after traffic — presence/shape, not exact EG metric names (the EG L4 metric-name mapping is a known open item; strict matching would be brittle). |

Each traffic/BTP test provisions its own stream (unique name/port) so cases are independent and the
suite can run with `-p 1` like the others. Readiness: after deploy, wait for the route to be
Accepted and the Stream Gateway LB to have an address before dialing.

## 6. Version gating

The e2e matrix injects the EG version per arm. `main_test.go` reads it (same env the workflow
already sets) and `t.Skip`s the whole suite when the version is below the verified line
(**skip 1.6.6 and 1.7.5; run 1.8.4 and 1.9.1**), with an explicit skip message naming the reason.
TCP/UDP routing is standard Gateway API, but the BTP subset is version-sensitive, so the gate is
keyed on the feature's verified support, not on raw route support.

## 7. CI wiring (`.github/workflows/main.yml` + `e2e.yml`)

No new workflow — the suite rides the existing matrix. Add, next to the existing
"Build and load the test doubles" and "Deploy static e2e dependencies" steps:
- build + `kind load` the `l4-echo` image;
- apply the echo Deployment + Service manifest.

The existing `go test -tags e2e ./e2e/... -p 1` step then picks up `e2e/suites/stream`
automatically. Both the per-push job (`main.yml`, EG 1.9.1) and the dispatch matrix (`e2e.yml`,
full matrix) gain the coverage; the version gate keeps it a no-op on unsupported arms. RBAC already
grants `tcproutes`/`udproutes` (added with the feature), so no RBAC change is needed.

## 8. Readiness & flakiness

- **Per-stream LB assignment** is the main risk: each stream creates its own LoadBalancer Service,
  and cloud-provider-kind must assign each an IP with the right TCP/UDP ports. Validated early in
  implementation; **fallback** if LB assignment is slow/flaky on kind: resolve via NodePort or a
  `kubectl port-forward` to the Stream Gateway Service instead of the LB IP (harness detail,
  hidden behind `StreamGatewayAddr`).
- **UDP looseness:** all UDP assertions use retry-to-deadline, never a single datagram.
- **Convergence:** reuse the existing suites' "wait for route Accepted + policy converged" helpers
  before asserting traffic, so a slow xDS push doesn't cause a false negative.

## 9. Out of scope

- Merged-template / `mergeGateways` mixed HTTP+TCP+UDP-on-one-LB scenarios, reserved-port rejection,
  and multi-listener-per-stream (the brainstorm "Full" tier — not chosen).
- Exact EG L4 metric-name assertions (kept lenient here; tightening is a follow-up once the EG L4
  metric mapping is validated against a live cluster).
- TLS/TLSRoute on L4 (the feature is plain TCP/UDP only).
