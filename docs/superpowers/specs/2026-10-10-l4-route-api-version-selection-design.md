# L4 Route API Version Selection (v1 / v1alpha2) — Design

**Date:** 2026-10-10
**Status:** Design (approved in brainstorming, pending spec review)
**Scope:** Product code only. E2E coverage is an explicit follow-up (see Non-Goals).

## Problem

FastGateway writes `TCPRoute` and `UDPRoute` objects at the
`gateway.networking.k8s.io/v1alpha2` API version, hardcoded in
`internal/kubernetes/gvr.go`:

```go
TCPRouteGVR = schema.GroupVersionResource{
    Group: "gateway.networking.k8s.io", Version: "v1alpha2", Resource: "tcproutes",
}
UDPRouteGVR = schema.GroupVersionResource{
    Group: "gateway.networking.k8s.io", Version: "v1alpha2", Resource: "udproutes",
}
```

Gateway API v1.6 (released 2026-06-30) graduated `TCPRoute` and `UDPRoute` to
the **Standard** channel as `gateway.networking.k8s.io/v1` and **deprecated**
the `v1alpha2` shapes. On a cluster running Gateway API ≥ 1.6 (e.g. Envoy
Gateway 1.9.1 + Gateway API 1.6.1), every `v1alpha2` write now emits:

> Warning: The v1alpha2 version of TCPRoute has been deprecated and will be
> removed in a future release of the API. Please upgrade to v1.

The warning is cosmetic today — the API server serves the same object under
both `v1` and `v1alpha2`, so `v1alpha2` writes still function and Envoy
Gateway still reconciles them — but it is noise, and `v1alpha2` will be
removed in a future Gateway API release.

## Goal

Write `v1` `TCPRoute`/`UDPRoute` by default, and write the deprecated
`v1alpha2` **only when the cluster's Gateway API version is positively
detected as < 1.6** (Envoy Gateway 1.8 / Gateway API 1.5, which have no `v1`
L4 types). The version is read per-project from the detection FastGateway
already does. This silences the deprecation warning on modern clusters while
keeping Envoy Gateway 1.8 working — and, because `v1` is the default, a
cluster whose version cannot be read is treated as modern (`v1`) rather than
downgraded to a `v1alpha2` that a standard-channel 1.6 cluster would reject.

## Key facts this design rests on

1. **`v1` is a Gateway API version, not an Envoy Gateway version.**
   `gateway.networking.k8s.io/v1` vs `/v1alpha2` is the apiVersion of the
   CRD, owned by Gateway API. `v1` `TCPRoute`/`UDPRoute` exist only in
   Gateway API ≥ 1.6.
2. **Envoy Gateway 1.9 reconciles the `v1` L4 types** and requires the
   Gateway API 1.6 CRDs to be installed; if they are absent, TCP/UDP routes
   are silently skipped. Envoy Gateway 1.8 has only `v1alpha2`.
3. **One stored object, one Envoy config — no conflict.** A CRD is
   hub-and-spoke: multiple *served* versions, exactly one *storage* version,
   converted on read/write. There is only ever one stored `TCPRoute` object;
   `v1` and `v1alpha2` are views of it (identity conversion for this straight
   graduation). Envoy Gateway watches a single version per release (EG 1.9 →
   `v1`, EG 1.8 → `v1alpha2`), reconciling the one object into one Envoy
   listener/cluster. Delete via any served version removes the one object.
   No duplicate routes, no conflicting proxy config.
4. **`v1` availability is channel-dependent on Gateway API 1.6.** `v1`
   graduated to the **Standard** channel, so it is present in *both* the
   standard and experimental 1.6 bundles — `v1` is universal on 1.6.
   `v1alpha2`, by contrast, is served only by the **experimental** 1.6 bundle;
   the standard 1.6 bundle drops it. Consequence: on a 1.6 cluster, writing
   `v1` always works, while writing `v1alpha2` works only on an
   experimental-bundle cluster. This is why `v1` is the default and `v1alpha2`
   is reserved for positively-detected < 1.6 clusters.
5. **Envoy Gateway's version tracks Gateway API's** for this purpose: EG 1.9
   ⟺ Gateway API 1.6, EG 1.8 ⟺ Gateway API 1.5. The authoritative signal for
   "can this cluster accept a `v1` TCPRoute" is the **Gateway API version**,
   which FastGateway already probes from the
   `gateway.networking.k8s.io/bundle-version` annotation on the Gateway API
   CRDs and caches per-project.

## Existing building blocks (reused, not rebuilt)

- `internal/cluster/versions.go` — `DetectVersions(projectID)` probes the EG
  image tag and the Gateway API CRD `bundle-version` annotation.
- `internal/services/project_version_service.go` —
  `ProjectVersionService.Get(ctx, projectID, forceRefresh)` returns cached
  `VersionInfo{ EnvoyGateway, GatewayAPI ProbeResult, Status }` per project
  (TTL-cached; no fresh cluster probe on each call).
- `internal/services/compatibility.go` — `majorMinor(v)` extracts the
  `"major.minor"` prefix from a semver-ish string (tolerates a leading `v`).
- `internal/services/route_deploy_service.go` — deploys L4 routes through a
  `k8sL4Routes` interface: `CreateTCPRoute` / `UpdateTCPRoute` /
  `DeleteTCPRoute` and the three UDP equivalents.
- `internal/cluster/route.go` — implements those six methods; each currently
  uses the hardcoded `kubernetes.TCPRouteGVR` / `kubernetes.UDPRouteGVR`.

## Design

### Approach

Resolve the API version in the **service layer** (which already owns the
version cache) and pass the chosen GVR down into the dumb `cluster` executor.
Version *policy* stays in `services`; `cluster` just writes what it is told.
(Rejected alternatives: injecting a version provider into `cluster.Client` —
inverts the layering; `cluster` self-detecting inline — a ~10s cluster probe
on every route operation.)

### Component A — GVR data (`internal/kubernetes/gvr.go`)

Keep `TCPRouteGVR` and `UDPRouteGVR` as the existing `v1alpha2` definitions
(used only for positively-detected < 1.6 clusters). Add `v1` variants, which
become the default:

```go
// TCPRouteGVRV1 is the v1 (Standard-channel, Gateway API >= 1.6) TCPRoute GVR.
TCPRouteGVRV1 = schema.GroupVersionResource{
    Group: "gateway.networking.k8s.io", Version: "v1", Resource: "tcproutes",
}
// UDPRouteGVRV1 is the v1 (Standard-channel, Gateway API >= 1.6) UDPRoute GVR.
UDPRouteGVRV1 = schema.GroupVersionResource{
    Group: "gateway.networking.k8s.io", Version: "v1", Resource: "udproutes",
}
```

Pure data, no logic.

### Component B — resolver (service layer)

Two small pure functions (in `internal/services`, beside `compatibility.go`
so they can reuse `majorMinor`):

```go
// gatewayAPINeedsL4V1alpha2 reports whether a cluster MUST be written the
// deprecated v1alpha2 L4 types: true ONLY when a Gateway API version is
// positively detected AND it is < 1.6 (Envoy Gateway 1.8 / Gateway API 1.5).
// An empty, unparseable, or >= 1.6 version returns false — i.e. default v1.
func gatewayAPINeedsL4V1alpha2(gatewayAPIVersion string) bool

// tcpRouteGVRFor / udpRouteGVRFor return the v1alpha2 GVR when
// gatewayAPINeedsL4V1alpha2 is true, else the v1 GVR (the default).
func tcpRouteGVRFor(gatewayAPIVersion string) schema.GroupVersionResource
func udpRouteGVRFor(gatewayAPIVersion string) schema.GroupVersionResource
```

`gatewayAPINeedsL4V1alpha2` parses `majorMinor`; it returns true **only** for a
positively-detected `major == 1 && minor < 6` (i.e. `"1.5.x"`, `"1.4.x"`).
Everything else — `""`, unparseable input, `"1.6.x"`, `"1.7.x"`, and any future
`major > 1` — returns false, so the resolver yields the `v1` GVR. `v1` is the
default; `v1alpha2` is the positively-detected legacy exception.

### Component C — threading the GVR into `cluster`

1. In `route_deploy_service`, before deploying a stream's routes, resolve the
   project's Gateway API version **once** via a narrow
   `GatewayAPIVersionResolver` dependency (concrete impl wraps
   `ProjectVersionService.Get(ctx, projectID, false)` — cached — and returns
   `VersionInfo.GatewayAPI.Version`, or `""` if the probe errors), and compute
   the TCP and UDP GVRs with the Component B resolvers. A probe error yields
   `""`, which the resolver maps to the `v1` default — a detection hiccup never
   fails the deploy.
2. Extend the `k8sL4Routes` interface's six methods to take the resolved
   `schema.GroupVersionResource` for the route being written/deleted. (The
   deploy service already distinguishes TCP from UDP at each call site, so it
   passes the matching GVR.)
3. In `internal/cluster/route.go`, each method uses the passed GVR for the
   dynamic-client REST path **and** sets the unstructured object body's
   `apiVersion` to `gvr.Group + "/" + gvr.Version`, so the REST path and the
   object's own apiVersion can never diverge.

`routeDeploy` does not hold a version service today (it is composite-literal
built from a `deps` struct in `route_service.go`). Add the narrow
`GatewayAPIVersionResolver` to that `deps` struct and the `routeDeploy`
struct, wired to a concrete adapter over `ProjectVersionService` (wiring
detail for the plan).

### Data flow

```
route_deploy_service.deploy(stream)
  └─ GatewayAPIVersionResolver.GatewayAPIVersion(projectID)  → cached version | ""
       └─ tcpRouteGVRFor / udpRouteGVRFor(version)           → v1 (default) or v1alpha2
            └─ cluster.Create/Update/DeleteTCPRoute(..., gvr)
            └─ cluster.Create/Update/DeleteUDPRoute(..., gvr)
                 └─ object written at the resolved apiVersion
```

### Fallback and safety

- Default is `v1`. We write `v1alpha2` **only** when the Gateway API version
  is positively detected as < 1.6. Detection failure / empty / unparseable /
  ≥ 1.6 → `v1`.
- Why `v1` is the safe default: `v1` is present in *both* the standard and
  experimental Gateway API 1.6 bundles, so it is universal on any ≥ 1.6
  cluster; `v1alpha2` is served only by the experimental 1.6 bundle, so
  defaulting to it would break a standard-channel 1.6 cluster. The only
  clusters without `v1` are Gateway API 1.5 (Envoy Gateway 1.8), which we
  positively detect and route to `v1alpha2`.
- Known edge: if detection *fails* on a genuine Gateway API 1.5 cluster, the
  default `v1` write fails (no `v1` CRD there). This is unlikely — the version
  is read straight off an existing CRD annotation — and is the deliberate
  trade for never downgrading a modern cluster to an unserved `v1alpha2`. No
  extra machinery is added for it.
- Delete resolves the GVR the same way (one stored object; see Key Fact 3).
  On an experimental-bundle ≥ 1.6 cluster an object created under one version
  is deletable under the other; on a single-version cluster the resolver
  picks that cluster's only served version. No version-mismatch failure, no
  migration step.

### Error handling

No new error classes. The resolver cannot error (it falls back). The existing
create/update/delete error handling in `cluster/route.go` is unchanged; only
the GVR and the object's `apiVersion` field differ by cluster version.

## Testing (unit; product-code-only scope)

1. **Resolver table test** (`internal/services`): `v1alpha2` GVR for the
   positively-detected legacy versions only — `"1.5.1"`, `"1.4.1"`; `v1` GVR
   (the default) for `"1.6.0"`, `"1.6.1"`, `"1.7.0"`, `"v1.6.1"`, `""`,
   `"garbage"`, `"1"`, and a future `"2.0.0"`; for both TCP and UDP.
2. **Deploy-service resolution test** (`internal/services`): with a mocked
   `GatewayAPIVersionResolver` returning a given version (and one returning
   `""` for the probe-error case) and a mocked `k8sL4Routes`, assert the deploy
   passes the expected GVR — `v1alpha2` only for a `1.5`/`1.4` reading, `v1`
   for `≥1.6` and for the empty/error case.
3. **Cluster write test** (`internal/cluster`, fake dynamic client): given a
   resolved GVR, `CreateTCPRoute` / `CreateUDPRoute` create an object whose
   `apiVersion` equals `gvr.Group+"/"+gvr.Version` at the matching resource
   path; repeat for `v1` and `v1alpha2`.

The project's full suite (`go test ./... -count=1`) must stay green —
including the regenerated mocks for the widened `k8sL4Routes` interface
(`make mocks` / `make mocks-check`).

## Non-Goals / Follow-ups

- **E2E coverage** proving `v1` on the Envoy Gateway 1.9.1 arm and `v1alpha2`
  on the 1.8.4 arm (the stream harness's readiness GVRs plus assertions). This
  is a deliberate fast-follow; the compatibility matrix already runs both arms,
  so the harness change is additive.
- **No Envoy Gateway / CRD install changes.** CI installs the experimental
  Gateway API bundle on Gateway API ≥ 1.5, which includes the `v1` L4 types on
  1.6; the standard-channel graduation does not require a channel change for
  the existing jobs.
- **No change to policy targeting.** BackendTrafficPolicy (and every other
  policy) targetRef references the route by group + kind + name only — no
  apiVersion — so the v1/v1alpha2 switch does not touch policy attachment.
- **No removal of `v1alpha2`.** It remains the path for positively-detected
  Gateway API < 1.6 (Envoy Gateway 1.8) for as long as that version is
  supported. It is no longer the default — `v1` is.

## Global Constraints

- Keep `internal/services/compatibility.go`'s `SupportedVersionPairs` as the
  source of truth for the version floor; this feature reads detected versions
  but does not change the support matrix.
- `v1alpha2` must remain fully functional on Gateway API < 1.6.
- Mocks are generated (`make mocks`); the widened `k8sL4Routes` interface must
  be regenerated and `make mocks-check` must pass.
