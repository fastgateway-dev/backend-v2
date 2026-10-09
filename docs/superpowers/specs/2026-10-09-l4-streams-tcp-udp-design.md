# L4 Streams (TCPRoute / UDPRoute) — Design Spec

**Status:** Draft for review
**Date:** 2026-10-09
**Repos:** `backend-v2` (Go control plane over Envoy Gateway), `frontend-v2` (Next.js UI)
**Author:** zufar.dhiyaulhaq (with Claude)

---

## 1. Overview & intent

FastGateway today exposes only **hostname-keyed** routing (HTTP, gRPC) through the
`Domain` concept. This feature adds **port-keyed Layer-4 routing** — raw TCP and UDP —
so users can expose non-HTTP services (databases, Redis, Kafka, DNS, syslog, game
servers, …) through the same control plane, deployed as Gateway API `TCPRoute` /
`UDPRoute` resources on Envoy Gateway.

The organizing principle that shapes the whole design:

> **Hostname → Domain. Port → Stream.**
> HTTP/gRPC (and, later, TLS passthrough) are hostname-keyed and live on **Domains**.
> TCP/UDP are port-keyed and live on a new sibling concept, **Streams**.

### Goals
- Create, edit, delete **TCP** and **UDP** routes, each mapping a listener **port** to a
  weighted **backend pool** of **in-cluster Kubernetes Services**.
- Reuse the existing route → approval → deploy → state pipeline, audit, and RBAC.
- Expose the L4-applicable `BackendTrafficPolicy` subset (TCP: connection-oriented circuit
  breaker + LB + health check + timeouts; UDP: load-balancing).
- Surface **basic L4 observability** (active connections/sessions, connection rate, throughput).
- Prevent port conflicts deterministically.
- Be backward compatible — existing Domains and templates are untouched.
- Keep an **external-backend seam** so FQDN/IP targets can be added later with a localized change.

### Non-goals (v1)
- **No TLS**: no TLS termination on L4, and **no `TLSRoute`** (SNI passthrough). TLSRoute is
  hostname-keyed and belongs on **Domains** in a future effort (see §13).
- **No external (FQDN/IP) backends.** Envoy Gateway's `Backend` CRD is supported only by
  HTTPRoute/TLSRoute, **not** TCPRoute/UDPRoute (verified; see §11) — so L4 backends are
  in-cluster Kubernetes Services only in v1. The external path (an `ExternalName`/manual-
  EndpointSlice Service) is future work behind the seam above.
- No L7 features on L4 routes (matches, filters, redirect, URL-rewrite, header modifiers,
  CORS, WAF, SecurityPolicy, client attachments, rate limiting, retries, compression).
- No "allowed-ports" governance on templates (future; see §13).
- No full code/API rename of `domain_templates` (only a user-facing label change; see §4).

---

## 2. Terminology

| Term | Meaning |
|---|---|
| **Domain** | Existing hostname-keyed entry point. One hostname = one K8s `Gateway`. Hosts HTTP/gRPC. Unchanged by this feature. |
| **Stream** | **New.** Port-keyed entry point. One Stream = one K8s `Gateway` whose listeners are TCP/UDP ports. Hosts L4 routes. |
| **L4 route** | A `Route` row with `protocol` ∈ {`tcp`,`udp`} attached to a Stream. Maps a listener port → weighted backends. |
| **Gateway Template** | The existing "Domain Template", relabeled. A Gateway **infrastructure** profile that generates a `GatewayClass` + `EnvoyProxy`. Referenced by Domains and/or Streams. |
| **transport** | TCP or UDP at the socket level. HTTP/HTTPS/TLS listeners are **TCP**-transport. |

---

## 3. Global Constraints

- **Gateway API version:** `sigs.k8s.io/gateway-api v1.6.1` (already in `go.mod`). `TCPRoute`
  and `UDPRoute` are `gateway.networking.k8s.io/v1alpha2` (their types live in
  `apis/v1alpha2`); `Gateway` stays `v1`.
- **Envoy Gateway:** the running EG line (currently 1.9.x) must support TCP/UDP routing and
  `BackendTrafficPolicy` targeting TCP/UDP routes. See §11 (verified facts).
- **No TLS anywhere in L4 scope.**
- **Backward compatibility is mandatory.** No existing table is renamed; no existing column
  changes meaning; existing Domains and templates behave identically.
- **Reuse over reinvention.** L4 routes are `Route` rows flowing through the existing
  approval/deploy/audit/state machinery. New code mirrors the GRPCRoute path
  (`internal/routeplan/grpcroute.go`, `internal/kubernetes/grpcroute.go`,
  `internal/cluster/route.go`) with a `v1alpha2` twist.
- **Attribution:** commits end with `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`.

---

## 4. Gateway Template changes (the enabler)

### 4.1 Finding (ground truth)
The "Domain Template" is already, functionally, a **Gateway infrastructure profile**:
- `templateplan.BuildGatewayClassConfig` + `BuildEnvoyProxyConfig` consume only infra fields
  (controller, exposure, LB class, annotations, resources, scaling, telemetry, scheduling,
  PDB, strategy, `mergeGateways`).
- `HTTPPort` / `HTTPSPort` / `TLSMode` / `TLSPolicy` feed **neither** template builder — they
  are **copied onto the `Domain` row at domain-create** (`domain_service.go:379-385`) and the
  Domain's Gateway listeners read them from the Domain (`domainplan/gateway.go:41-46`), not the
  template.
- GatewayClass + EnvoyProxy are created at **template**-create, named per-template
  (`{name}-{exposure}`, `{name}-{exposure}-config`), and **shared by every Domain of that
  template**. Ports/TLS never participate in their naming.

Therefore the infra/listener split already exists at the data and manifest layer; the template
just also authors HTTP/TLS **defaults**.

### 4.2 Design: one type-agnostic template with capability flags
- **Relabel** "Domain Template" → **"Gateway Template"** in the UI only. Keep the table
  `domain_templates`, the API routes `/domain-templates`, and the TS/Go type names unchanged
  (backward compatibility; a full rename is a separate future effort).
- **Add two boolean columns** to `domain_templates`:
  - `enable_domain BOOL NOT NULL DEFAULT true`
  - `enable_stream BOOL NOT NULL DEFAULT false`
  - Validation: **at least one must be true**.
- Existing rows default to `enable_domain=true, enable_stream=false` → identical to today.
- **Field visibility** (frontend): the HTTP/HTTPS port + TLS section shows only when
  `enable_domain`; stream usage requires `enable_stream`. A both-enabled template shows the
  HTTP/TLS section (those remain Domain listener defaults).
- **Scoping:** the Domain create picker lists `enable_domain` templates; the Stream create
  picker lists `enable_stream` templates. A both-enabled template appears in both.
- The flags are **pure scoping + field-visibility**. The generated `GatewayClass` / `EnvoyProxy`
  are produced by the same builders regardless; infra is identical.

### 4.3 Shared deployment via `mergeGateways`
- When a template is **both-enabled** and `mergeGateways: true`, all its Gateways (Domains and
  Streams) collapse into **one Envoy data plane + one LB**, exposing HTTP (80/443) and the
  Streams' TCP/UDP ports together.
- When `mergeGateways: false`, each Gateway (Domain or Stream) gets its own Envoy + LB — exactly
  as Domains behave today.
- **Caveat — mixed TCP+UDP on one LB (warn, don't block):** when `mergeGateways` puts TCP *and*
  UDP listeners on one Gateway, Envoy Gateway generates **one LoadBalancer `Service` with mixed
  TCP+UDP ports**. That requires the Kubernetes **`MixedProtocolLBService`** feature — a gate on
  the **core `Service` resource** (not a CRD), **GA since ~1.26 (always on)** — *and* a cloud LB
  that honors mixed protocols (**AWS NLB ✅**, **DigitalOcean ❌**, GCP/Azure constrained).
  HTTP+TCP (all TCP-transport) is universally fine; only adding **UDP** to the mix needs this.
  The control plane can't reliably detect provider support, so the decision is **warn-and-allow**:
  the UI warns when a Stream would add UDP to a merged LoadBalancer template, and the user
  proceeds (or uses a non-merged template / separate LB for UDP). Merge uniqueness constraint
  (enforced by §7): `(port, protocol, hostname)` must be unique across all merged listeners.
  *(Future: Envoy Gateway now favors `ListenerSet` over `mergeGateways`; out of scope here.)*

---

## 5. Streams resource (new)

### 5.1 Model — `streams` table
```
Stream {
  ID                uuid   PK
  ProjectID         uuid   NOT NULL   (uniqueIndex idx_stream_project_name with Name)
  Name              string NOT NULL   (uniqueIndex idx_stream_project_name)
  Namespace         string NOT NULL
  GatewayTemplateID uuid   NOT NULL   (FK domain_templates.id; must have enable_stream=true)
  K8sGatewayName    string            (generated — kind-prefixed, see below)
  K8sGatewayClass   string            (copied from template, mirrors Domain.K8sGatewayClass)
  Status            string            (reuse the domain/status vocabulary)
  StatusMessage     string
  CreatedBy, CreatedAt, UpdatedAt
}
```
A Stream is the L4 twin of `Domain` **minus** hostname, TLS, cert, DNS. It references a
`enable_stream` Gateway Template for its GatewayClass + EnvoyProxy, exactly as a Domain does.

**Gateway name uniqueness (Q4).** A Stream and a Domain with the same name in the same namespace
must not collide on `K8sGatewayName`. Generated Stream Gateway names are **kind-prefixed**
(e.g. `str-<name>`), disjoint from the Domain naming scheme, so a Stream "foo" and a Domain
"foo" can never clash on the K8s Gateway object.

**Referenced template is immutable (Q8).** A Stream's `GatewayTemplateID` cannot change after
create (changing it would re-home the Gateway onto a different GatewayClass/LB) — mirrors a
Domain's inability to change ports/TLS. Changing infra = delete + recreate the Stream. **Ports
and routes are fully mutable**, however (§5.2).

### 5.2 Stream Gateway lifecycle — "Gateway = projection of routes"
- **Eager creation.** At Stream-create, provision the K8s `Gateway` referencing the template's
  GatewayClass so the LB/IP provisions early (user sees the address), mirroring Domains.
- **Placeholder listener.** Gateway API requires `listeners` `minItems: 1`; a zero-listener
  Gateway is invalid. An empty Stream's Gateway carries **one inert placeholder TCP listener on
  a reserved placeholder port** (nothing routes to it). As soon as the first real L4 route is
  added, listeners become the real set and the placeholder is dropped. The placeholder port is
  on the reserved list (§7).
- **Projection.** The Stream Gateway's `listeners` = the set of its **active** L4 routes, one
  listener per route. On **any** L4 route deploy (create/update/delete), the deploy path
  **recomputes the full listener set from the DB** and applies the whole Gateway — idempotent,
  and it avoids read-modify-write races when routes deploy concurrently.
- **Teardown.** Deleting a Stream deletes its Gateway (and any routes block or cascade per the
  existing route lifecycle rules).

### 5.3 Lifecycle, reuse & documented limits
- **Approvals / audit / state / RBAC** are protocol-agnostic and flow through unchanged.
  Stream CRUD itself is **not** approval-gated (mirrors Domain create, which is direct); L4
  **routes** on a Stream are approval-gated like HTTP routes.
- **"Active" semantics (Q9):** an L4 route is `active` once **deploy succeeds** — the
  `TCPRoute`/`UDPRoute` CRD is applied and the Stream Gateway is accepted/programmed. There is no
  active-probe/readiness gating in v1 (same DB-driven model as HTTP routes).
- **Deletion with active routes (Q3):** deleting a Stream that still has L4 routes is **blocked**
  — routes must be removed first. (No cascade in v1.)
- **Ports/routes are fully mutable (Q8):** users add or remove L4 routes (hence listener ports)
  on a Stream at any time; each goes through approval + deploy + the collision check (§7).
- **Documented limits (Q8):**
  - *Merged-Gateway reconfigure:* adding/removing a listener triggers an Envoy xDS update; on a
    merged template the shared proxy (all Domains + Streams) reconfigures. EG applies this
    incrementally/hitlessly — accepted risk, documented.
  - *~64-listener ceiling per Gateway* (Gateway API / EG): a Stream with many ports, or a busy
    merged Gateway, can hit it. `ListenerSet` is the future escape hatch; v1 documents the cap.

---

## 6. Route model changes (L4 routes)

L4 routes are `Route` rows — reusing the table and pipeline — with these additions:

- `Route.Protocol` enum gains **`tcp`** and **`udp`** (alongside `http`, `grpc`).
- `Route.DomainID` becomes **nullable**; add nullable `Route.StreamID`. **Exactly one** of the
  two is set (validation + a DB CHECK). HTTP/gRPC routes keep a `DomainID`; L4 routes carry a
  `StreamID`.
- `Route.ListenerPort int` — the LB-exposed port for L4 routes (required for `tcp`/`udp`,
  unused for `http`/`grpc`). **Exposed port == listener port (Q7):** the LB exposes exactly this
  port; no external≠target mapping in v1. (The backend's own port can differ.)
- **transport** (TCP/UDP) equals the L4 `protocol` value. HTTP/HTTPS/TLS count as
  TCP-transport only in the cross-Gateway collision check (§7), never in the DB index.
- **Backends — in-cluster Kubernetes Services only (v1).** Reuse the existing `RouteBackend`
  (`name`/`namespace`/`port`/`weight`), with **weighted splitting** across multiple backends
  (Q2) emitted as `rules[].backendRefs` weights. External FQDN/IP via the Envoy `Backend` CRD is
  **not supported by EG for TCP/UDP** (§11), so the L4 **validation + emit** layer rejects
  external backends. The `RouteBackend` external fields remain in the model as a **single marked
  seam** — the future external path (an `ExternalName`/manual-EndpointSlice Service) is a
  localized change there, not a model rewrite (Q1).
- All L7-only `RouteConfig` fields (matches, filters, redirect, URL-rewrite, header modifiers,
  hostnames, security mode) are **unused and rejected** for L4 (see §9).

A migration adds `stream_id` (nullable, FK), makes `domain_id` nullable, adds `listener_port`,
and adds a **partial unique index** `(stream_id, protocol, listener_port)` where `stream_id IS
NOT NULL` — so `tcp:5432` and `udp:5432` on one Stream are distinct and both allowed, while two
`tcp:5432` routes on the same Stream are rejected at the DB level.

---

## 7. Port-collision design

**Listener identity = `(transport, port)`.** HTTP/HTTPS/TLS listeners are **TCP**-transport, so
a Stream's `TCP:443` collides with a Domain's HTTPS `:443`. `TCP:53` and `UDP:53` do **not**
collide (different sockets).

### 7.1 Scope — merge-aware (chosen)
| `mergeGateways` | Reality | Collision scope |
|---|---|---|
| `false` | each Gateway → own Envoy + own LB IP | **within the Stream's own Gateway** (its routes). Two separate Streams may both use `:5432`. |
| `true` | all Gateways on the GatewayClass → one Envoy + one LB | **across all Domains + Streams on that template** (all HTTP/HTTPS ports + all L4 ports). |

### 7.2 Reserved / invalid ports (chosen)
- Enforce `1 ≤ port ≤ 65535`.
- **Reserve Envoy internal ports** (admin/metrics, e.g. `19000`/`19001`) from L4 use.
- **Reserve the Stream Gateway placeholder port** (§5.2).
- When a template is **merged + `enable_domain`**, reserve the template's default HTTP/HTTPS
  ports (80/443, or custom) from L4 use so Streams cannot squat ports Domains need.

### 7.3 Enforcement points (reject with 409, mirroring the DNS `CheckCollision` pattern)
1. **L4 route create/update** → `CheckPortCollision(stream, transport, port, excludeRouteID)`.
2. **Domain create/update** (only when merged, custom port) → a Domain's HTTP/HTTPS port must not
   land on a Stream's L4 port in the merged set.
3. **Template `mergeGateways` flip → true**, or **`enable_stream`/`enable_domain` change** → validate
   the newly-merged/enabled set has no `(transport, port)` clashes; reject the change otherwise.

### 7.4 Layers
- DB **partial unique index** `(stream_id, transport, listener_port)` guarantees the within-Stream
  case cheaply.
- The merged cross-Gateway case is a **service-level query** collecting used `(transport, port)`
  across the template's Gateways — same structure as `HostnameClaimExists`. Here HTTP/HTTPS/TLS
  listener ports count as **TCP**-transport.
- **UX:** live "port in use" check on the port field (like the DNS zone check); the Stream detail
  page lists in-use ports.

---

## 8. L4 traffic policy (BackendTrafficPolicy subset)

Reuse the existing BTP builder, gated **per protocol**:

Verified against EG (§11): `BackendTrafficPolicy` accepts `TCPRoute`/`UDPRoute` targetRefs, but
only a subset of fields render meaningfully at L4.

| BTP feature | TCP | UDP |
|---|---|---|
| Circuit breaker — **`maxConnections`, `maxRequestsPerConnection`** | ✅ | ❌ |
| Circuit breaker — `maxPendingRequests` / `maxParallelRequests` / retry budget | ❌ HTTP-only | ❌ |
| Load-balancing algorithm | ✅ round-robin / least-request / random / consistent-hash **(= source-IP at L4)** | ✅ **LB only** — round-robin / random / source-hash |
| Health check — passive / outlier detection | ✅ | ❌ |
| Health check — active (TCP connect probe) | ✅ | ❌ |
| Timeouts — TCP connect / idle, TCP keepalive | ✅ | ⚠️ session idle timeout only (not in v1) |
| HTTP-only (CORS, rate-limit, retry, compression, fault-injection, request-buffer, response-override, admission-control) | ❌ not rendered | ❌ not rendered |

- **TCP routes:** connection-oriented circuit breaker (**`maxConnections`, `maxRequestsPerConnection`
  only** — the request-oriented counters are HTTP semantics and are excluded), LB (consistent-hash
  is source-IP based at L4), passive + active-TCP health check, and TCP connect/idle timeout +
  keepalive.
- **UDP routes:** **load-balancing algorithm only** (round-robin / random / source-hash).
  Circuit breaking and health checks do not meaningfully apply to UDP datagrams; session idle
  timeout exists in EG but is deferred.
- The BTP `targetRef.Kind` for L4 is `TCPRoute` / `UDPRoute` (extend `routeplan.GetRouteKind`).
  The exact fields that render into the Envoy config should be confirmed empirically per EG
  version during implementation (verify the generated xDS).

---

## 8.5. L4 observability (Q6)

v1 surfaces **basic L4 signals** — not HTTP RPS/latency/error (which don't apply). Source: the
project's existing Prometheus/VictoriaMetrics endpoint, reading Envoy's `tcp_proxy` / `udp_proxy`
stats. **No new infrastructure.**

- **Metrics** (per Stream, and per L4 route/listener where Envoy labels allow):
  - **Active connections** (TCP) / active sessions (UDP)
  - **Connection rate** (new connections per second)
  - **Throughput** — bytes received / sent
- **Where:** a small **L4 metrics card** on the Stream and L4-route detail pages (parallel to the
  HTTP metrics cards, different PromQL). **No** latency / error-rate / p50–p99 cards.
- **Access logs** continue to flow via the template's EnvoyProxy telemetry (unchanged) and cover
  L4 connections.
- **Adds:** L4 PromQL query functions in `metrics_service` + a frontend L4 metrics card. No
  model/CRD change.

## 9. Validation rules

For a route with `protocol ∈ {tcp,udp}`:
- **Require:** `stream_id` (and not `domain_id`), `listener_port` (valid, not reserved, passes
  the merge-aware collision check), ≥1 backend.
- **Backends must be in-cluster Kubernetes Services.** Reject external (FQDN/IP) backends for L4
  (EG limitation, §11) — at the single marked seam, so the future external path is localized.
- **Reject** every L7 field: matches (path/header/method/query/grpc), filters, redirect,
  direct-response, URL-rewrite, header modifiers, hostnames, `SecurityMode`, WAF, client
  attachments, and all HTTP-only BTP features (including the request-oriented circuit-breaker
  counters, which are rejected for TCP too).
- **Per-protocol policy gating:** TCP → full L4 subset; UDP → LB only.
- Wire into `validateRouteShapeAndConflicts` (`route_write_validation.go:140-153`) and carve L4
  out of `validateRouteConfig` (`route_validation.go:99-131`, which currently requires path
  matching for non-grpc/non-directResponse and would otherwise reject L4).

Scoping validation: a Stream may reference only `enable_stream` templates; a Domain only
`enable_domain`. A route has exactly one of `stream_id`/`domain_id`.

---

## 10. API, data & K8s wiring (touch-point map)

**Migrations**
- `streams` table (§5.1); unique index `(project_id, name)`.
- `routes`: add `stream_id` (nullable FK), make `domain_id` nullable, add `listener_port`,
  add CHECK "exactly one of domain_id/stream_id", add partial unique index
  `(stream_id, protocol, listener_port) WHERE stream_id IS NOT NULL`.
- `domain_templates`: add `enable_domain` (default true), `enable_stream` (default false).

**API endpoints** (mirror domains)
- `/projects/:projectId/streams` — list/create/get/update/delete.
- L4 routes: reuse the route endpoints with Stream scoping (routes already carry `protocol`);
  list routes by stream.
- Gateway Template create/update DTOs gain `enableDomain` / `enableStream`.
- OpenAPI: add `streams` paths and the two template fields; `make openapi` bundle; `make
  openapi-check`.

**K8s layer** (mirror GRPCRoute, `v1alpha2`)
- New GVRs `tcproutes`, `udproutes` (`gateway.networking.k8s.io/v1alpha2`) — add to
  `gvr.go` and/or the `cluster/route.go` helper pattern.
- New typed builders `internal/kubernetes/tcproute.go`, `udproute.go` using `apis/v1alpha2`,
  and `internal/routeplan/tcproute.go`, `udproute.go` (BuildTCP/UDPRouteConfig) mirroring
  `grpcroute.go`. parentRef uses **`sectionName`** = the listener name `l4-<proto>-<port>`.
- `RouteApplier` (`k8s_roles.go`) gains Create/Update/Delete for TCP/UDP routes; the Gateway
  apply (`CreateGateway`/`UpdateGateway`) is reused to push recomputed Stream listeners.
- `route_deploy_service.go`: add `tcp`/`udp` branches in all three approval-action cases that
  (a) recompute + apply the Stream Gateway listeners and (b) apply the route CRD.
- `ReferenceGrant` kinds list (`cluster/route.go:240-246`) gains `TCPRoute`, `UDPRoute`.
- `route_yaml.go`: add TCP/UDP preview (route CRD + Stream Gateway listener diff).
- `route_assembler_config.go`: `buildTCPRouteConfig` / `buildUDPRouteConfig` delegation.
- Mockery: regenerate (`make mocks` / `make mocks-check`).

**Stream Gateway builder**
- A new `streamplan` (or extend `domainplan`) builder constructs the Stream `Gateway` with
  listeners = projection of active L4 routes (+ placeholder when none), referencing the
  template's GatewayClass.

**Frontend (`frontend-v2`)**
- **New "Streams" nav section** (sibling of Domains): list; create (name + namespace + template
  picker scoped to `enable_stream`); detail (LB address, **in-use ports**, routes, L4 metrics card).
- **Slim L4 route create/edit form** (new, NOT the ~3300-line HTTP wizard): protocol (TCP/UDP),
  listener port with **live collision check**, weighted K8s-Service backends, advanced L4-policy
  section gated by protocol (§8). No hostname/match/filter/security fields.
- **Gateway Template form:** add the `enable_domain` / `enable_stream` checkboxes (≥1 required);
  HTTP/TLS section conditional on `enable_domain`; relabel "Domain Template" → "Gateway Template"
  (display only). Domain-create template picker scoped to `enable_domain`.
- **L4 metrics card** (§8.5) on Stream + L4-route detail.
- Types/API client: `streams` client + types; `RouteProtocol` union gains `'tcp' | 'udp'`;
  template type gains the two flags; mixed-proto-LB **warning** when adding UDP to a merged
  LoadBalancer template.

---

## 11. Envoy Gateway facts (verified)

Verified against EG docs/source (~1.9.x line, Gateway API v1.6.x). These are **decided**, not
open assumptions; the one residual check is empirical xDS confirmation during implementation.

1. **`Backend` CRD is NOT referenceable from `TCPRoute`/`UDPRoute`.** EG supports the `Backend`
   CRD only for HTTPRoute and TLSRoute. → **L4 backends are in-cluster Kubernetes Services only**
   (drives §1, §6, §9). The `Backend` CRD is also disabled by default and forbids loopback.
2. **`BackendTrafficPolicy` accepts `TCPRoute`/`UDPRoute` targetRefs** (confirmed in EG's CEL
   validation), but field applicability is limited (§8): TCP → `maxConnections` /
   `maxRequestsPerConnection`, LB, passive + active-TCP health check, TCP timeouts; UDP → LB
   (+ session idle timeout, deferred). Request-oriented CB counters, rate-limit, fault-injection,
   compression, admission-control are HTTP-only.
3. **`mergeGateways: true` supports mixed HTTP/TCP/UDP listeners** on one Envoy + one Service; the
   only constraint is `(port, protocol, hostname)` uniqueness across merged listeners (enforced by
   §7). *(EG now nudges toward `ListenerSet`; out of scope.)*
4. **Mixed TCP+UDP on one LoadBalancer** needs Kubernetes `MixedProtocolLBService` (core `Service`
   feature gate, GA ~1.26, always on) **and** a capable cloud LB (AWS NLB ✅, DigitalOcean ❌,
   GCP/Azure constrained) → **warn-and-allow** (§4.3).
5. **Listener attachment is via `parentRefs.sectionName`** (= the listener `name`); a TCP/UDP
   listener does not require `allowedRoutes.kinds` (per-protocol defaults apply).

**Residual empirical check (implementation task 0):** confirm which BTP fields actually render
into the generated Envoy config for TCP and UDP on the exact deployed EG version, and expose only
those. No design decision depends on it — only which policy sub-fields the UI shows.

---

## 12. Backward compatibility

- No table renamed; `domain_templates` keeps its name, routes, and types.
- `enable_domain` defaults true, `enable_stream` false → existing templates behave identically.
- `domain_id` becomes nullable but every existing route keeps its `domain_id`; the new CHECK is
  satisfied by existing rows.
- Domains, HTTP/gRPC routes, approvals, DNS, certs: unchanged.

---

## 13. Future (explicitly out of scope)

- **TLSRoute (SNI passthrough)** — hostname-keyed; extends **Domains** (new `TLS` passthrough
  listener + TLSRoute bound by hostname), and would also tidy today's HTTPS-passthrough stub.
  Never on Streams.
- **Allowed-ports governance** on stream-enabled templates.
- **Full code/API rename** `domain_templates` → `gateway_templates`.
- **Move HTTP/TLS listener fields off the template onto the Domain** (the deeper template
  refactor) — optional cleanup; the data layer already supports it.
- L7 policies / client auth on L4 (not applicable).

---

## 14. Review Focus (inputs the tasks must pin)

Most-likely-to-bite inputs the spec implies but that need explicit tests:
1. **Port collision across the merged set** — a Stream port equal to a sibling Domain's HTTPS
   port (443) or another Stream's port under `mergeGateways: true` must 409.
2. **TCP vs UDP same number** — `TCP:53` and `UDP:53` on one Stream must **both** succeed
   (no false collision).
3. **Reserved ports** — Stream port = 80/443 (merged+domain), Envoy internal, or placeholder
   port must be rejected.
4. **L7 fields on an L4 route** — a `tcp` route carrying a path match / filter / security mode
   must be rejected, not silently ignored.
5. **Exactly-one-owner** — a route with both `domain_id` and `stream_id`, or neither, must be
   rejected; and an L4 route referencing a template without `enable_stream` must be rejected.
6. **Empty Stream** — a Stream with zero routes produces a valid Gateway (placeholder listener)
   and an LB address; adding the first route drops the placeholder.
7. **Concurrent L4 route deploys** on one Stream must converge (full-listener-set recompute),
   not clobber each other's listeners.
8. **External backend on an L4 route** (FQDN/IP) must be **rejected** — L4 is K8s-Service-only;
   the rejection lives at the single marked seam.
9. **Deleting a Stream that still has active routes** must be **blocked** (not cascade), with a
   clear "remove routes first" error.
10. **Changing a Stream's `GatewayTemplateID` after create** must be rejected (template is
    immutable); adding/removing routes/ports must succeed.
