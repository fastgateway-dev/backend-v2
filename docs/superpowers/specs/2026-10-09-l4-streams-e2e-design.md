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

Scope decision (brainstorm + grilling): **Standard** — core traffic + the feature's capabilities,
**excluding metrics** (L4 metrics are PromQL-backed and only mockable in e2e, so they stay
unit-tested; see §9). CI placement: **existing matrix, gated to the verified EG line**
(skip 1.6.6/1.7.5, run 1.8.4/1.9.1). Execution method: **native** (in-session implementation with
one final whole-branch review — test code following established suite patterns).

## 2. Topology

Reuses the existing single kind cluster + cloud-provider-kind (LoadBalancer support) already
stood up by the e2e workflow. Unlike the HTTP suites, which target the one seeded Gateway's
`GATEWAY_IP`, **each Stream provisions its own Gateway → its own LoadBalancer Service**; tests
resolve that per-stream LB ingress address at runtime via the kube client. Streams are created
per-test through the control-plane API (not the seed), then deployed.

## 3. Test-double backend — `e2e/servers/l4-echo`

One small Go binary that listens on **both TCP and UDP** and echoes the received payload back
verbatim — the L4 analogue of nginx/podinfo for the HTTP suites. No instance identifier: the suite
does not assert traffic distribution (§5 weighted-backends matches the HTTP precedent), so a plain
echo is sufficient.

- Built and loaded into kind exactly like the other `e2e/servers/*` doubles (own `go.mod`,
  Dockerfile, `docker build` + `kind load`).
- Deployed as **two distinct Services** (`l4-echo-a`, `l4-echo-b`), each its own Deployment
  (1 replica) from the single `l4-echo` image, each exposing the TCP and UDP ports, in the e2e
  dependency namespace. Two Services (rather than one Service with two replicas) let the
  health-check test fail one backend while the other stays up (§5). Manifests live beside the
  other static deps.
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
| `match_weighted_backends_test.go` | Two weighted K8s-Service backendRefs on an L4 route; assert the route goes live and a dial round-trips (serves). **No distribution assertion** — matches the HTTP/gRPC precedent, which deliberately does not observe weighting. |
| `btp_circuit_breaker_test.go` | TCP `maxConnections`: fire N concurrent connections past the limit, assert **≥1 is rejected** (behavioral-with-tolerance, mirroring grpcroute). |
| `btp_load_balancing_test.go` | TCP + UDP LB algorithm is **Accepted/programmed** on the route (config-level; no distribution assertion, consistent with the weighted-backends decision). |
| `btp_health_check_test.go` | TCP passive + active-TCP health check: fail `l4-echo-b`, assert dials still succeed because the unhealthy backend is ejected (behavioral). |
| `btp_timeout_test.go` | TCP connection/idle timeout applied. |
| `validation_reject_l7_test.go` | An L4 route carrying hostname/matches/filters is rejected (400) at write time. |
| `validation_port_collision_test.go` | A second `tcp:<port>` on the same stream → 409. |

(No `metrics_test.go` — metrics are out of scope for e2e; see §9.)

Each traffic/BTP test provisions its own stream (unique name/port) so cases are independent and the
suite can run with `-p 1` like the others. Readiness: after deploy, wait for the route to be
Accepted and the Stream Gateway LB to have an address before dialing. The behavioral BTP tests
(circuit breaker, health check) fall back to a **config-Accepted** assertion for any individual
signal that proves flaky through the kind LoadBalancer during implementation, rather than being
dropped.

## 6. Version gating

The e2e matrix injects the EG version per arm as the `ENVOY_GATEWAY_VERSION` job env var (verified
present in `main.yml`/`e2e.yml`). `main_test.go` reads it and `t.Skip`s the whole suite when the
version is below the verified line
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
- **L4 metrics entirely** (grilling decision). L4 metrics are PromQL-backed, so an e2e test could
  only validate the stream→cluster-name→PromQL→API plumbing against a `mock-prometheus` fixture
  (as the HTTP metrics e2e does) — it cannot exercise real Envoy L4 metrics. That plumbing is
  already unit-tested; a live-Prometheus L4 metrics check is a separate follow-up. No
  `mock-prometheus` extension is made here.
- Traffic **distribution/weighting** assertions (matches the HTTP/gRPC precedent, which does not
  observe weighting); the weighted-backends test only asserts the route serves.
- TLS/TLSRoute on L4 (the feature is plain TCP/UDP only).
