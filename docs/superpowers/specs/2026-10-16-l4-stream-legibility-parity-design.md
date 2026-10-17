# L4 Stream Legibility and L4 Route Parity — Design

**Date:** 2026-10-16
**Status:** Approved 2026-10-17; revised during planning (see "Revisions during planning" at the end)
**Grounded on:** backend-v2 `origin/main` @ `26484c0`, frontend-v2 `origin/main` @ `b7a3603`
**Follow-up (separate spec):** TLSRoute / SNI-keyed L4 routing

## Problem

The owner, running FastGateway on a home-lab cluster with a shared
`shared-envoy-clusterip` template, reported that the L4 Streams UI does not
behave like the Domain/Route UI (screenshots of Create Stream, the stream
list, the stream detail page, and Add L4 Route, 2026-10-11). The owner raised
eight points and then a more basic one: *"stream is only a placeholder pointing to
a gateway template — there is no advantage of abstracting via stream."*

Investigation found two separate problems.

**1. The Stream is not legible.** A Stream looks like an empty container. Its
create form takes only a template, a name and a namespace, and no page ever
shows which Envoy or IP it lands on, whether that IP is shared, or which
ports are free. That hides the one thing that decides what a Stream is.
Merging is a per-template flag (`DomainTemplate.MergeGateways`), so:

| | Template `mergeGateways: false` | Template `mergeGateways: true` |
|---|---|---|
| Envoy, Service, IP | one per Stream (its own Gateway) | one per template, shared by every stream **and** domain on it |
| Listener-port uniqueness | per Stream | per template (all streams' L4 ports + all domains' hostname-routed listener ports) |
| What the Stream really is | its own entry point, like an AWS NLB | a named group of ports on the template's shared entry point |

On a merged template the UI implies the Stream owns its IP, which is false.
That mismatch is why it feels like a placeholder.

**2. L4 routes get less of the route machinery than HTTP routes.** L4 routes
share the HTTP routes' table, model, create input, approval engine and
version snapshots, but the stream path exposes less of it: no manifests view
for the stream, no manifest preview while creating or editing an L4 route,
version history unreachable, an approval diff that returns HTTP 500, and no
L4 route detail page.

### Market check

Paid platforms use three L4 abstractions:

- **AWS NLB:** Load Balancer (owns the IP) → Listener per port → Target Group.
- **Traefik / Gateway API / Gloo:** the platform owns entry points (ports on a
  shared proxy); teams attach routes to them by port or by SNI. No
  user-created container.
- **Cloudflare Spectrum:** one flat application per service (hostname + port
  → origin).

A FastGateway Stream follows the NLB model on a dedicated template and the
platform-entry-point model on a shared template. Each model is coherent on
its own; the UI has to say which one applies.

### The owner's points, resolved

| # | Point | Resolution |
|---|---|---|
| 1 | No port selection on stream create | **Dropped as a field.** Ports belong to routes, as on AWS NLB listeners; domains take no port either. Create now **shows** the free port range instead. |
| 2 | No team selection on stream create | **Dropped.** Owner team is a route-level field for both domains and streams; the L4 route form already requires it. |
| 3 | Stream list differs from domain list | **In scope** (§F3). |
| 4 | No manifests on stream detail | **In scope** (§B3, §F4). |
| 5 | Same experience as domain & route | Covered by the rows below. |
| 6 | No manifest preview or approval on route-on-stream | **In scope** (§B5, §B7, §F5, §F7). Approval already runs; only the diff is broken. |
| 7 | Route versioning for streams | **In scope** (§B6, §F6). Snapshots already exist. |
| 8 | Split "Advanced" into sections like domain route create | **In scope** (§F5): the form moves to tabs. |

## Goal

1. Make a Stream legible as **"a named L4 entry point; each route is a port on
   it"**: show whether it is dedicated or shared, its IP, who shares it, and
   the real port scope, on create, list and detail.
2. Bring L4 routes to parity with HTTP routes for manifests, manifest preview,
   approval diff, and version history, including a proper L4 route detail
   page.

The API changes are additive, nothing needs a schema migration, and nothing
FastGateway applies to a cluster changes.

## Non-Goals

- **TLSRoute / SNI routing.** Separate spec. SNI-keyed services fit a
  Domain-like, hostname-keyed model; the Stream stays port-keyed.
- **Removing or reshaping the Stream** (e.g. attaching L4 routes straight to a
  template). See Alternatives.
- **Port or team fields on stream create** (points 1 and 2).
- **A Preview tab on stream create.** Before any route exists the Gateway is
  only the `l4-placeholder` listener on port 60000, which tells the user
  nothing; create shows the entry point instead (§F2).
- **Labels or description on streams**, server-side stream search, a
  topology or settings tab for streams.
- **Inline approve/reject on stream pages.** The unified Approvals pages stay
  the single place to act.
- **Changing `mergeGateways` mutability** or the concurrent L4 deploy race
  noted in `deployL4`.

## Key facts this design rests on

1. **L4 routes reuse the route machinery.** Same `routes` table, `models.Route`,
   `CreateRouteInput`, and `ApprovalEntityRoute` engine; the parent is a
   `Stream` (`StreamID`) instead of a `Domain` (`DomainID`).
2. **Entry point scope is per template.** One EnvoyProxy + GatewayClass per
   template (`templateplan/envoyproxy.go`, `templateplan/gatewayclass.go`).
   `MergeGateways` (default `false`, immutable after creation) is rendered onto
   that EnvoyProxy. Each Stream adds only its own `str-<name>` Gateway; under
   merge, every member Gateway's `status.addresses` reflects the one shared
   Service.
3. **Port scope rules** are in `StreamService.CheckPortCollision`
   (`stream_port_collision.go`):
   - every port: valid and not reserved (`DefaultReservedPorts` = 19000,
     19001, 60000), and inside the template's TCP/UDP range
     (`Listeners.StreamRange()`, else `ErrPortOutOfRange`);
   - unmerged: unique per stream (`UsedPortsByStream`);
   - merged: unique per template (`UsedL4Ports` + `DomainRepository.UsedPortsByTemplate`
     + the template's hostname-routed listener ports, `templateDomainDefaults`).

   Rejected routes hold no port; every other status does.
4. **L4 YAML rendering already exists.** `routeQuery.generateL4RouteYAML`
   (`route_yaml.go`) renders TCPRoute/UDPRoute through the deploy builders and
   picks `v1`/`v1alpha2` from `CapL4RouteV1`. `GenerateYAMLs`, `PreviewUpdate`
   (`previewUpdateL4`) and `PreviewDelete` already have L4 branches. Under
   `/streams/:streamId/routes` only `GET /:routeId/yaml` is registered.
5. **L4 YAML omits the BackendTrafficPolicy** that `deployL4` applies
   (`applyBackendTrafficPolicy` →
   `routeplan.BuildBackendTrafficPolicyConfigForNamespace(route, stream.Namespace, stream.ID.String(), policy)`).
6. **The stream Gateway's listener set** is computed in `deployL4` as
   `BuildStreamGatewayConfig(*stream, routes)`, where `routes` =
   `routeRepo.ListActiveByStreamID(stream.ID)` (plus or minus the route being
   deployed). The Gateway has no read endpoint.
7. **Versions already exist for L4.** `finishDeploy` writes a `route_versions`
   snapshot on every successful L4 create/update deploy. The snapshot holds
   protocol, `listenerPort`, backends, and the BTP. `RouteVersionHandler` reads
   only `routeId`/`version`/`projectId`, and rollback re-submits through
   `routeWrite.Update`, which already handles L4. The endpoints are mounted
   only under `/domains/:domainId/routes`.
8. **Approvals already work for L4 except the diff.** List, detail and
   approve/reject/cancel handle L4 with no domain assumption. `GetDiff` returns
   `ErrDiffNotAvailableForL4` for every L4 route and the handler turns that into
   500. `ApprovalService` has no stream dependency, and enrichment sets only
   `DomainName`.
9. **Frontend data is mostly already there.** `DomainTemplate.mergeGateways`
   and `listeners` are in `src/types`. `L4RouteForm` already fetches the
   template for the range check (`streamListener` helper). There is no L4 route
   detail page (only `routes/create` and `routes/[routeId]/edit`).
10. **Contract tests.** `TestRouteSpecParity` (`cmd/server/router_test.go`)
    compares the router with the OpenAPI spec. `make mocks-check`,
    `make openapi-check` and `TestNoWiringNilGuardsRemain` gate every change.

## Design

### Concept and wording

One definition, used everywhere in the UI: **"A Stream is an L4 entry point.
Each route is a port on it."**

| Mode | Source | Badge | One-line explanation |
|---|---|---|---|
| `dedicated` | template `mergeGateways: false` | **Dedicated IP** | "This stream has its own entry point and IP." |
| `shared` | template `mergeGateways: true` | **Shared IP** | "This stream is a named group of ports on the template's shared entry point. Ports are unique across everything on it." |

### Backend

#### B1. Entry point endpoints

Two read-only endpoints built on one internal builder, so stream detail and
stream create can't disagree:

- `GET /projects/:projectId/streams/:streamId/entrypoint` (detail)
- `GET /projects/:projectId/domain-templates/:domainTemplateId/entrypoint`
  (create, before a stream exists)

```json
{
  "mode": "shared",
  "templateId": "…",
  "templateName": "shared-envoy",
  "gatewayClass": "shared-envoy-clusterip",
  "address": "10.0.0.5",
  "portRange": { "min": 9000, "max": 9100 },
  "sharedWith": {
    "streams": [{ "id": "…", "name": "cache" }],
    "domains": [{ "id": "…", "name": "api", "hostname": "api.example.com" }]
  },
  "ports": [
    { "transport": "TCP", "port": 5432,
      "owner": { "kind": "stream_route", "streamId": "…", "streamName": "test-stream",
                 "routeId": "…", "routeName": "postgres", "status": "active" } },
    { "transport": "TCP", "port": 443,
      "owner": { "kind": "domain", "domainId": "…", "domainName": "api" } }
  ],
  "reserved": [
    { "transport": "TCP", "port": 19000, "reason": "envoy_internal" },
    { "transport": "UDP", "port": 19000, "reason": "envoy_internal" },
    { "transport": "TCP", "port": 19001, "reason": "envoy_internal" },
    { "transport": "UDP", "port": 19001, "reason": "envoy_internal" },
    { "transport": "TCP", "port": 60000, "reason": "placeholder" },
    { "transport": "UDP", "port": 60000, "reason": "placeholder" },
    { "transport": "TCP", "port": 443,   "reason": "template_listener" }
  ]
}
```

Rules:

- `mode` = `shared` if and only if `template.MergeGateways`.
- `portRange` = `Listeners.StreamRange()`, or `null` when the template has
  none.
- `ports` scope follows Key Fact 3 exactly:
  - **dedicated:** this stream's L4 routes. The template variant returns `[]`,
    since a new stream starts with an empty port space.
  - **shared:** every stream's L4 routes on the template, plus every domain's
    bound listener ports. Rejected routes are excluded.
- `reserved` = `DefaultReservedPorts` on **both** TCP and UDP (validation
  rejects them on both), plus `templateDomainDefaults(tmpl)` as TCP when
  shared. These are the same sources validation uses.
- `sharedWith` is empty when dedicated. When shared, it lists the other
  streams on the template (the stream itself is excluded on the stream
  variant) and the domains on the template.
- `address` is read live from the Gateway's `status.addresses`. The
  backend never stores it: `Stream.loadBalancerAddress` exists only in the
  frontend types and is never set.
  - **dedicated:** this stream's Gateway; the template variant returns `""`.
  - **shared:** this stream's Gateway if it has an address, else any member
    stream's Gateway, else `""`. The UI shows `""` as "assigned on first
    deploy".
  - A missing Gateway or no address gives `""`; any other cluster read error
    is a 500.
- **Failure:** if the template lookup fails, return an error (500). Never
  fall back to a guessed `dedicated`.

New owner-aware repository queries back `ports`. They use the same `WHERE`
clauses as the existing used-port queries, which stay unchanged for
`CheckPortCollision`:

- `StreamRepository.L4PortOwnersByStream(streamID)`
- `StreamRepository.L4PortOwnersByTemplate(templateID)`
- `DomainRepository.PortOwnersByTemplate(templateID)`

**Agreement invariant:** for any port in the template's range, `ports` or
`reserved` lists it if and only if `CheckPortCollision` rejects it for this
stream, and any port outside `portRange` is rejected. A test pins this
(§Testing).

#### B2. Stream list enrichment

`GET /projects/:projectId/streams` returns each stream with these extra
fields:

- `mode` (`dedicated` | `shared`)
- `gatewayTemplateName`
- `routeCount`: non-rejected L4 routes
- `ports`: `[{transport, port}]` of non-rejected L4 routes

These come from one grouped route query and one template lookup per distinct
template; there is no per-stream query.

#### B3. Stream manifests

`GET /projects/:projectId/streams/:streamId/yamls` returns:

```json
{
  "gatewayYaml": "…",
  "routes": [
    { "routeId": "…", "name": "postgres", "protocol": "tcp",
      "routeYaml": "…", "backendTrafficPolicyYaml": "…" }
  ]
}
```

- `gatewayYaml` renders `streamplan.BuildStreamGatewayConfig(*stream, routes)`
  with `routes = routeRepo.ListActiveByStreamID(stream.ID)`, the live set
  `deployL4` uses, through the **same object builder `UpdateGateway`
  applies**. With no live routes this is the placeholder Gateway, matching the
  cluster.
- `routes` covers the same live set. `routeYaml` comes from
  `generateL4RouteYAML`; `backendTrafficPolicyYaml` from B4's helper, and is
  empty when the route has no policy.

#### B4. L4 route manifests include the BackendTrafficPolicy

- Add `generateL4BackendTrafficPolicyYAML(route, stream, policy)` built on
  `BuildBackendTrafficPolicyConfigForNamespace(route, stream.Namespace, stream.ID.String(), policy)`,
  the deploy path's builder.
- Extend the L4 branch of `routeQuery.GenerateYAMLs` to fill
  `BackendTrafficPolicyYAML`. The TCPRoute/UDPRoute stays in `httpRouteYaml`,
  so the response shape is unchanged.
- Register `GET /projects/:projectId/streams/:streamId/routes/:routeId/yamls`
  (`GetYAMLsForStream`): `requireStreamRoute`, then the existing logic.

#### B5. L4 preview endpoints

All three are under `/projects/:projectId/streams/:streamId/routes`, guarded
by stream ownership, with the same permission as their HTTP counterparts.

| Endpoint | Behaviour |
|---|---|
| `POST /preview` (create) | New `PreviewCreateForStream(streamID, input)`. Runs the **same validation as `CreateForStream`**: name, protocol `tcp`/`udp`, port validity/reserved/range/collision through `CheckPortCollision`, protocol-gated policy inputs. Returns `PreviewCreateResult{ProposedYAML, ProposedBackendTrafficPolicyYAML}`. Errors map to the same 400/409 as create, so a preview never shows a manifest that create would reject. |
| `POST /:routeId/preview` (update) | Existing `previewUpdateL4`, plus current/proposed BTP YAML. A changed port re-runs `CheckPortCollision` with `excludeRouteID`. |
| `GET /:routeId/preview-delete` | Existing L4 branch of `PreviewDelete`. |

#### B6. Version endpoints under the stream

Register under `/projects/:projectId/streams/:streamId/routes/:routeId`:

- `GET /versions`
- `GET /versions/:version`
- `POST /versions/:version/rollback`

Each goes through a `*ForStream` wrapper that runs `requireStreamRoute` (a
route from another stream returns 404) and then delegates to the existing
`RouteVersionHandler` method. No service change is needed.

#### B7. Approval diff and stream name

- `ApprovalService` gains two dependencies, wired and called
  unconditionally: a stream lookup, and an L4 manifest renderer (route YAML +
  BTP YAML, the same helpers as B3–B5).
- `GetDiff` replaces the `ErrDiffNotAvailableForL4` early return with an L4
  branch. `ErrDiffNotAvailableForL4` is removed.

  | Action | `CurrentYAML` / `CurrentBackendTrafficPolicyYAML` | `ProposedYAML` / `ProposedBackendTrafficPolicyYAML` |
  |---|---|---|
  | create | — | from the approval snapshot |
  | update | from the approval's previous-config snapshot | from the snapshot |
  | delete | from the live route + stored policy | — |

- `ApprovalDiffResult` gains `routeKind` (`HTTPRoute` | `GRPCRoute` |
  `TCPRoute` | `UDPRoute`) so the UI labels sections correctly.
- Enrichment: when `route.StreamID != nil`, set the new non-persisted fields
  `StreamID` / `StreamName` (`json:"streamId,omitempty"` /
  `json:"streamName,omitempty"`), parallel to `DomainName`.

#### B8. Contract hygiene

- Every new endpoint gets an OpenAPI entry (`make openapi`; `TestRouteSpecParity`
  and `make openapi-check` pass).
- Mocks are regenerated (`make mocks` / `make mocks-check`).
- No `if dep == nil` guards (`TestNoWiringNilGuardsRemain`).

#### Endpoint summary (all new; all read-only except rollback)

| Method | Path |
|---|---|
| GET | `/projects/:projectId/streams/:streamId/entrypoint` |
| GET | `/projects/:projectId/domain-templates/:domainTemplateId/entrypoint` |
| GET | `/projects/:projectId/streams/:streamId/yamls` |
| GET | `/projects/:projectId/streams/:streamId/routes/:routeId/yamls` |
| POST | `/projects/:projectId/streams/:streamId/routes/preview` |
| POST | `/projects/:projectId/streams/:streamId/routes/:routeId/preview` |
| GET | `/projects/:projectId/streams/:streamId/routes/:routeId/preview-delete` |
| GET | `/projects/:projectId/streams/:streamId/routes/:routeId/versions` |
| GET | `/projects/:projectId/streams/:streamId/routes/:routeId/versions/:version` |
| POST | `/projects/:projectId/streams/:streamId/routes/:routeId/versions/:version/rollback` |

Changed (additive fields only): `GET /projects/:projectId/streams` (B2),
`GET …/approvals` and `GET …/approvals/:id` (`streamId`/`streamName`),
`GET …/approvals/:id/diff` (L4 support, `routeKind`).

### Frontend

#### F1. Types, API clients, helpers

- **Types:** `StreamEntrypoint`, the list fields from B2, `StreamYAMLs`,
  approval `streamId`/`streamName`, diff `routeKind`.
- **`streamsApi` additions:** `getEntrypoint`, `getYamls`, `getRouteYamls`,
  `previewCreateRoute`, `previewUpdateRoute`, `previewDeleteRoute`,
  `listRouteVersions`, `getRouteVersion`, `rollbackRouteVersion`.
- **`domainTemplatesApi` addition:** `getEntrypoint`.
- **Pure helpers** in `src/lib/utils/stream-entrypoint.ts`, with a colocated
  test:
  - `entrypointLabel(mode)`
  - `splitPorts(ports, streamId)` → `{ mine, neighbors }`
  - `freePortCount(range, ports, reserved)`
  - `ownerLabel(owner)`
- **`l4route.ts`** gains `tabErrors(errors)` → which form tabs hold errors.

#### F2. Stream create

- Template options show their mode: `shared-envoy — Shared IP` or
  `edge-l4 — Dedicated IP`.
- Selecting a template loads `domainTemplatesApi.getEntrypoint` and shows an
  **Entry point** panel:
  - **Dedicated:** "This stream gets its own entry point and IP. Routes can
    use ports {min}–{max}."
  - **Shared:** "This stream joins the shared entry point `{gatewayClass}`
    (IP `{address}` or "assigned on first deploy"), also used by N streams
    and M domains. {free} of {size} ports free in {min}–{max}." The list of
    who shares it can be expanded.
- No port or team fields, and no Preview tab.

#### F3. Stream list

- **Columns:** Name (link) · Namespace · Entry point (Dedicated/Shared badge
  + template name) · Routes · Ports (first three as `5432/TCP`, then `+N`) ·
  Status. There is no IP column: the address is live Gateway status, and
  reading it for every row would mean one cluster call per stream. The IP
  shows on stream detail and stream create.
- **Toolbar:** status filter (all / active / pending / error) and a debounced
  name search, both client-side.
- The empty state and the New Stream permission gate are unchanged.

#### F4. Stream detail

- The header is unchanged.
- The "Load Balancer Address" and "Ports in use" cards are replaced by an
  **Entry point** panel:
  - mode badge and one-line explanation;
  - IP (or "not assigned yet") and gateway class;
  - **shared:** "Also used by N streams and M domains", expandable, with
    links;
  - **port map:** this stream's ports in full (port/protocol → route link +
    status); neighbours collapsed to "K ports used by other streams/domains",
    expandable with owner labels; reserved ports in small print; the range
    and free count.
- **Tabs:**
  - **Routes:** Name (→ route detail), Protocol, Port, Owner team, Status, and
    the quick Deploy action for `approved`/`pending_deploy`. Edit moves to the
    route detail page.
  - **Metrics:** the existing `L4MetricsCard`.
  - **Manifests:** the Gateway YAML plus each route's YAML and BTP YAML,
    collapsible and read-only, using the domain Manifests tab's viewer.

#### F5. L4 route form → tabs

The form mirrors domain route create. Every field and every validation rule
is unchanged; only the layout changes.

| Tab | Contents |
|---|---|
| **Basic Info** | name, description, owner team, protocol, listener port with the range and collision hints |
| **Backends** | unchanged rows |
| **Traffic Policy** | separate Load balancing / Circuit breaker / Health check sections, gated by protocol as today, with the UDP note |
| **Preview** | on opening, calls `previewCreateRoute` (create) or `previewUpdateRoute` (edit) with the current values and renders the route + BTP in `YamlDiffViewer` (create or update mode). Server 400/409 errors show inline. |

Tab badges flag errors via `tabErrors`. Next/Back buttons work as in the
domain route form. Create and edit use the same component, and submit
behaviour is unchanged.

#### F6. L4 route detail page (new)

Path: `src/app/projects/[projectId]/streams/[streamId]/routes/[routeId]/page.tsx`

- **Header:** back to the stream; name, protocol badge, `:port`, status badge,
  owner team.
- **Actions:**
  - **Edit:** goes to the existing edit page, which returns here after saving.
  - **Deploy:** shown for `approved`/`pending_deploy`.
  - **Delete:** a confirm modal shows the `previewDeleteRoute` diff, then the
    existing delete → approval flow runs.
- **Pending statuses** show a banner, "Waiting for approval — Review in
  Approvals", linking to `/projects/:projectId/approvals`, the same as HTTP
  routes.
- **Tabs:**
  - **Overview:** description, backends table, traffic-policy summary,
    timestamps.
  - **Manifests:** `getRouteYamls`, showing the route and BTP.
  - **History:** the version list; compare two versions with
    `VersionCompareModal`; rollback confirm → `rollbackRouteVersion` →
    redirect to Approvals, as on HTTP routes.

#### F7. Approvals

- The list and detail pages show "Stream: {streamName}" for L4 routes, where
  L7 routes show the domain.
- Diff section titles come from `routeKind` (TCPRoute/UDPRoute), and the BTP
  section renders when present.
- No link changes are needed; all links are already scoped by approval.

### Data flow

```
stream detail ─┬─ GET /streams/:id/entrypoint ─ template + owner-aware port queries
               ├─ GET /streams/:id/yamls ────── BuildStreamGatewayConfig(stream, ListActiveByStreamID)
               │                                + generateL4RouteYAML + L4 BTP YAML
               └─ GET /streams/:id/metrics (existing)

stream create ── GET /domain-templates/:id/entrypoint (same builder)

L4 route form ── POST …/routes/preview | …/routes/:rid/preview
                   └─ same validation as create/update → route + BTP YAML

L4 route detail ─┬─ GET …/routes/:rid/yamls
                 └─ GET …/routes/:rid/versions[/:v] · POST …/rollback → approval

approval detail ── GET /approvals/:id/diff ── L4 branch → route + BTP YAML (current/proposed)
```

## Blast radius (feasibility consult)

The consult was done by read-only mapping of both repos at the SHAs in the
header. The dev agents could not be dispatched from this session. One
correction is on record: an early report said no L4 YAML renderer existed,
but the code has `routeQuery.generateL4RouteYAML` (Key Fact 4).

| Invariant / risk | Effect | Mitigation |
|---|---|---|
| **Byte-identical rendered Gateway** | No apply path changes; every new backend path is a read-only render. The risk is the **view** drifting from what is applied. | The stream manifests use the same route set (`ListActiveByStreamID`) and the same builders as `deployL4`. A test asserts equality, and e2e compares against the live Gateway. |
| **Fail closed on apply** | Not touched. | Read endpoints return errors rather than partial manifests or a guessed mode. |
| **No silent nil-skips** | `ApprovalService` gains two dependencies. | Wired and called unconditionally; `TestNoWiringNilGuardsRemain`. |
| **Reserved ports 19000/19001/60000** | The port map must show them. | Taken from `DefaultReservedPorts` (+ `templateDomainDefaults`), the validation sources. |
| **Port map vs create 409/400** (new) | A map that says "free" when create rejects would mislead users. | Agreement test against `CheckPortCollision`. |
| **Route/OpenAPI parity** | Ten new routes. | OpenAPI entries; `TestRouteSpecParity`. |
| **List performance** | Enrichment could turn into N+1 queries. | One grouped route query, and one template read per distinct template. |

No schema migration. All API changes are additive and backward compatible:
the existing `/routes/:routeId/yaml` stays, and `httpRouteYaml` keeps
carrying the L4 route.

## Alternatives considered

| Alternative | Why it lost |
|---|---|
| **Remove the Stream**; attach L4 routes to a template (Traefik / Gateway API model) | Changes every existing stream's rendered Gateway (breaks the byte-identical invariant; needs a migration) and loses grouping, per-stream metrics and the delete unit. Revisit only if usage shows streams almost always hold one route; the cheaper middle option then is an automatic default stream per shared template. |
| **Dedicated-only templates for streams** (strict NLB model) | Removes the cheap single-IP setup home-lab and cost-conscious users rely on. |
| **Clone domain features onto streams as-is** | Misses the cause: what a Stream is stays invisible. |
| **Domain-style cards for the stream list** | A table shows IPs and ports more densely, and L4 reads as an infrastructure inventory. |
| **Preview tab on stream create** | Before routes exist it renders only the placeholder listener. |
| **Port / team on stream create** | Contradicts the model; domains don't take them either. |
| **Inline approve/reject on stream pages** | Duplicates the unified Approvals UI. |
| **Ship TLSRoute in the same spec** | Large, backend-heavy, with its own product decisions; it would delay the legibility fix. |

## Testing

**Backend unit tests:**

1. **Entry point builder:** table tests for dedicated vs shared, with and
   without domains, `portRange` null, reserved ports (including hostname-routed
   listener ports when shared), `sharedWith` excluding self, the address
   fallback order, and template-lookup failure → error.
2. **Agreement:** on a fixture template (shared and dedicated), for every port
   in `[min-1, max+1]` and both transports, "listed in `ports`/`reserved` or
   outside `portRange`" equals "`CheckPortCollision` returns an error".
3. **Owner-aware queries:** rejected routes excluded; owners correct; parity
   with `UsedPortsByStream` / `UsedL4Ports` / `UsedPortsByTemplate` on the
   same fixtures.
4. **Stream YAMLs:** `gatewayYaml` equals the marshalled object `deployL4`
   would apply for the same live routes; the placeholder appears with zero
   routes; a route's BTP YAML appears only when the route has a policy.
5. **Previews:** `PreviewCreateForStream` returns the same 400/409 as
   `CreateForStream` for a bad name, reserved port, out-of-range port and
   collision; update preview includes the BTP diff and re-checks a changed
   port.
6. **Stream-scoped wrappers:** a route from another stream → 404 on
   `yamls`/preview/versions/rollback.
7. **Approval diff for L4:** create, update and delete each fill the right
   current/proposed fields and `routeKind`; no 500; `streamName` enriched.
8. **Contract checks:** `go test ./... -count=1`, `make mocks-check`,
   `make openapi-check`.

**Backend e2e (L4 suite):** after deploying a TCP route on a stream,
`/streams/:id/yamls` listeners match the live Gateway's listeners,
`…/versions` returns at least one entry, and the approval diff for a
following update returns 200 with TCPRoute YAML.

**Frontend:**

- Helper tests for `stream-entrypoint.ts` and `tabErrors`.
- Component tests:
  - create panel (dedicated vs shared, unassigned address);
  - list filter and search;
  - detail Entry point panel (mine vs neighbours collapse);
  - route form tabs, Preview rendering and inline server error;
  - route detail tabs and the rollback redirect;
  - approvals stream label and `routeKind` titles.
- Gate: `fastgateway-frontend-verify`.

## How we will know it worked

- On a shared template, the stream detail page alone answers: what IP,
  shared or not, with whom, and which ports are taken by what. No visit to the
  template page is needed.
- The port map never disagrees with create (agreement test).
- Every L4 route change shows a manifest preview before submit, and its
  approval shows a diff (no 500).
- Every deployed L4 route shows version history and can roll back through
  approval.
- The stream Manifests Gateway matches what is on the cluster (unit and e2e).

## Rollout and plans

Backend lands first, since the API is the contract; frontend follows. One
plan per repo, next to this spec:

- `2026-10-16-l4-stream-legibility-parity-backend.md`
- `2026-10-16-l4-stream-legibility-parity-frontend.md`

## Global Constraints

- No change to any object FastGateway applies to a cluster.
- No schema migration.
- Additive API only; existing endpoints and fields keep their shape.
- Every new endpoint has an OpenAPI entry; `make openapi-check` and
  `make mocks-check` pass.
- The entry point port map and create-time 400/409 agree.
- Reserved ports 19000, 19001 and 60000 are always shown as reserved.
- Frontend decision logic lives in `src/lib/utils/*.ts` with colocated tests.

## Deferred

All of these are decided as out of scope. None blocks this spec.

- **TLSRoute / SNI-keyed L4:** its own spec. Owner: the owner. Not blocking.
- **Automatic default stream per shared template:** revisit if usage shows
  streams hold one route. Owner: the owner. Not blocking.
- **Per-stream deploy serialization** (the concurrent-deploy race in
  `deployL4`): an existing known follow-up, unchanged here.

## Revisions during planning (2026-10-17)

Writing the plans against the code surfaced these corrections. The spec text
above already includes them.

- **Address source.** The backend never sets `loadBalancerAddress`, which is
  why today's stream list and detail always show "—" / "Not available yet".
  The entry point reads the Gateway's live `status.addresses` instead.
- **No IP column on the stream list.** It would need a cluster read per
  stream. The Entry point badge stays on the list; the IP is on detail and
  create.
- **Reserved ports on both transports.** 19000/19001/60000 are listed for TCP
  and UDP, because validation rejects them on both. Without that, the
  agreement invariant fails for UDP. The UI shows each port once
  ("19000/TCP+UDP").
- **"Current" side of an update diff.** It comes from the approval's
  previous-config snapshot, as for HTTP routes. The live route and stored
  policy already hold the proposed values by the time the diff is read.
