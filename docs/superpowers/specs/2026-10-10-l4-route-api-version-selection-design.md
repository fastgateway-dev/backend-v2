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

Write `v1` `TCPRoute`/`UDPRoute` when the target cluster's Gateway API version
is ≥ 1.6, and `v1alpha2` otherwise, chosen per-project from the version
FastGateway already detects. This silences the deprecation warning on modern
clusters while keeping older supported clusters (Envoy Gateway 1.8 / Gateway
API 1.5, which have no `v1` L4 types) fully working.

## Key facts this design rests on

1. **`v1` is a Gateway API version, not an Envoy Gateway version.**
   `gateway.networking.k8s.io/v1` vs `/v1alpha2` is the apiVersion of the
   CRD, owned by Gateway API. `v1` `TCPRoute`/`UDPRoute` exist only in
   Gateway API ≥ 1.6.
2. **Envoy Gateway 1.9 reconciles the `v1` L4 types** and requires the
   Gateway API 1.6 CRDs to be installed; if they are absent, TCP/UDP routes
   are silently skipped. Envoy Gateway 1.8 has only `v1alpha2`.
3. **Both versions are served at once on a ≥ 1.6 cluster.** The CRD stores one
   version and serves the object under both `v1` and `v1alpha2`, so:
   - a `v1alpha2` write still works on a 1.6 cluster (just warns), and
   - an object created as `v1alpha2` is readable and deletable via `v1`, and
     vice versa — there is no stored-object migration to perform.
4. **Envoy Gateway's version tracks Gateway API's** for this purpose: EG 1.9
   ⟺ Gateway API 1.6. The authoritative signal for "can this cluster accept a
   `v1` TCPRoute" is the **Gateway API version**, which FastGateway already
   probes from the `gateway.networking.k8s.io/bundle-version` annotation on
   the Gateway API CRDs and caches per-project.

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
(these become the fallback). Add `v1` variants:

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
// gatewayAPIHasL4V1 reports whether a cluster at the given Gateway API version
// serves the v1 TCPRoute/UDPRoute types (Gateway API >= 1.6). An empty or
// unparseable version returns false (fall back to v1alpha2).
func gatewayAPIHasL4V1(gatewayAPIVersion string) bool

// tcpRouteGVRFor / udpRouteGVRFor return the v1 GVR when gatewayAPIHasL4V1 is
// true, else the v1alpha2 GVR.
func tcpRouteGVRFor(gatewayAPIVersion string) schema.GroupVersionResource
func udpRouteGVRFor(gatewayAPIVersion string) schema.GroupVersionResource
```

`gatewayAPIHasL4V1` parses `majorMinor`, then returns true when
`major == 1 && minor >= 6` (and for any future `major > 1`). Everything else —
including `""`, `"1.5.1"`, `"1.4.1"`, and unparseable input — returns false.

### Component C — threading the GVR into `cluster`

1. In `route_deploy_service`, before deploying a stream's routes, resolve the
   project's Gateway API version **once** via
   `ProjectVersionService.Get(ctx, projectID, false)` (cached), read
   `VersionInfo.GatewayAPI.Version`, and compute the TCP and UDP GVRs with the
   Component B resolvers.
2. Extend the `k8sL4Routes` interface's six methods to take the resolved
   `schema.GroupVersionResource` for the route being written/deleted. (The
   deploy service already distinguishes TCP from UDP at each call site, so it
   passes the matching GVR.)
3. In `internal/cluster/route.go`, each method uses the passed GVR for the
   dynamic-client REST path **and** sets the unstructured object body's
   `apiVersion` to `gvr.Group + "/" + gvr.Version`, so the REST path and the
   object's own apiVersion can never diverge.

`route_deploy_service` must be able to reach `ProjectVersionService`. If it is
not already a dependency of that service, add it to the service's
constructor/deps (wiring detail for the plan).

### Data flow

```
route_deploy_service.deploy(stream)
  └─ ProjectVersionService.Get(projectID)        → cached GatewayAPI version
       └─ tcpRouteGVRFor / udpRouteGVRFor(version) → v1 or v1alpha2 GVR
            └─ cluster.Create/Update/DeleteTCPRoute(..., gvr)
            └─ cluster.Create/Update/DeleteUDPRoute(..., gvr)
                 └─ object written at the resolved apiVersion
```

### Fallback and safety

- Detection failure / empty / unparseable Gateway API version → `v1alpha2`.
  That is valid on every supported cluster (and still works on 1.6, where both
  versions are served), so a detection hiccup degrades to the
  deprecated-but-functional path rather than breaking route deployment.
- Delete resolves the GVR the same way. Because a ≥ 1.6 cluster serves the
  object under both versions, an object created under one version is still
  deletable under the other — no version-mismatch failure, no migration step.

### Error handling

No new error classes. The resolver cannot error (it falls back). The existing
create/update/delete error handling in `cluster/route.go` is unchanged; only
the GVR and the object's `apiVersion` field differ by cluster version.

## Testing (unit; product-code-only scope)

1. **Resolver table test** (`internal/services`):
   `"1.6.0"`, `"1.6.1"`, `"1.7.0"`, `"v1.6.1"` → `v1` GVR;
   `"1.5.1"`, `"1.4.1"`, `""`, `"garbage"`, `"1"` → `v1alpha2` GVR; for both
   TCP and UDP.
2. **Deploy-service resolution test** (`internal/services`): with a mocked
   `ProjectVersionService` returning a given Gateway API version and a mocked
   `k8sL4Routes`, assert the deploy passes the expected GVR (v1 vs v1alpha2)
   to the TCP and UDP methods, including the fallback when the version is
   empty/unknown.
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
- **No removal of `v1alpha2`.** It remains the fallback for Gateway API < 1.6
  for as long as those versions are supported.

## Global Constraints

- Keep `internal/services/compatibility.go`'s `SupportedVersionPairs` as the
  source of truth for the version floor; this feature reads detected versions
  but does not change the support matrix.
- `v1alpha2` must remain fully functional on Gateway API < 1.6.
- Mocks are generated (`make mocks`); the widened `k8sL4Routes` interface must
  be regenerated and `make mocks-check` must pass.
