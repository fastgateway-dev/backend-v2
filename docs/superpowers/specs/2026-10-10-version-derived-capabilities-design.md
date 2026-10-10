# Version-Derived Capabilities — Design

**Date:** 2026-10-10
**Status:** Design (grilled; pending spec review)
**Supersedes:** `2026-10-10-l4-route-api-version-selection-design.md` (that
narrower spec's L4 `v1`/`v1alpha2` selection is now the first internal
consumer of the capability model below).

## Problem

FastGateway must adapt its behavior to the Envoy Gateway / Gateway API
versions installed on each project's cluster, and this recurs in several
shapes:

- **Whole-feature availability** — L4 streams need Envoy Gateway ≥ 1.8; on
  older clusters the feature cannot work and today the UI offers it anyway.
- **Which API shape to emit** — `TCPRoute`/`UDPRoute` must be written as
  `gateway.networking.k8s.io/v1` on Gateway API ≥ 1.6 and `v1alpha2` below,
  to stop the `v1alpha2` deprecation warning while keeping EG 1.8 working.
- **(Future) a specific CRD field** — an EG/GwAPI field that only exists past
  some version and must be omitted below it.

Each of these is a predicate over the detected cluster versions. Handled
one-off, they scatter version thresholds across the codebase and the
frontend. This design introduces one place that answers "given this
cluster's versions, what can it do / what shape do we emit," and lands the
first two real consumers on it.

## Goal

A small **version-derived capability model**: a declarative registry of named
capabilities, each a predicate over the detected `(EnvoyGateway, GatewayAPI)`
versions FastGateway already probes and caches per project. Capabilities are
consumed two ways:

1. **Exposed** capabilities ride in the existing
   `GET /projects/{id}/capabilities` response so the frontend can gate UI.
2. **Internal** capabilities are queried by backend code to pick behavior.

Ship it with exactly two capabilities — `streams` (exposed) and `l4RouteV1`
(internal) — proving both consumption modes, and nothing speculative.

## Non-Goals

- **Not** a runtime feature-flag engine: no dynamic toggles, remote config,
  per-user/per-request targeting, or an admin UI. Capabilities are pure
  functions of detected cluster versions.
- **Not** a refactor of the existing `rateLimitAvailable` capability. That one
  is **probe-derived** (Redis reachability), not version-derived; it stays
  computed as it is today. The `/capabilities` endpoint simply aggregates it
  with the new version-derived flags.
- **No field-level capabilities yet** — add one only when a real feature needs
  it. Boolean predicates only.
- **E2E coverage** of the L4 `v1`/`v1alpha2` split across the matrix remains a
  documented follow-up (see that section).

## Key facts this design rests on

1. **`v1` is a Gateway API version, not an Envoy Gateway version.** `v1`
   `TCPRoute`/`UDPRoute` exist only in Gateway API ≥ 1.6; EG is the controller
   that reconciles them.
2. **One stored object, one Envoy config.** A CRD is hub-and-spoke (many
   served versions, one storage version, converted on access). There is only
   one stored `TCPRoute`; `v1`/`v1alpha2` are views of it. Envoy Gateway
   watches a single version per release (EG 1.9 → `v1`, EG 1.8 → `v1alpha2`)
   and reconciles the one object into one listener/cluster — no duplicate or
   conflicting proxy config, and delete via any served version removes the one
   object.
3. **`v1` is universal on Gateway API 1.6; `v1alpha2` is not.** `v1` graduated
   to the Standard channel, so it is in both the standard and experimental 1.6
   bundles. `v1alpha2` is served only by the experimental 1.6 bundle. So on a
   1.6 cluster, writing `v1` always works; writing `v1alpha2` only works on an
   experimental-bundle cluster.
4. **FastGateway streams require Envoy Gateway ≥ 1.8.** The L4 feature is
   gated to the verified EG 1.8 line (`e2e/harness/version.go`'s
   `L4Supported`). Gateway API 1.4 (EG 1.6/1.7) has the `v1alpha2` L4 CRD but
   is **not** a streams target; the stream suite skips it. So the two
   streams-supported targets are EG 1.8 / GwAPI 1.5 (`v1alpha2`) and EG 1.9 /
   GwAPI 1.6 (`v1`).
5. **Detection already exists and is cached.** `ProjectVersionService.Get`
   returns per-project `VersionInfo{ EnvoyGateway, GatewayAPI ProbeResult }`
   (EG from the controller image tag, GwAPI from the CRD `bundle-version`
   annotation), TTL-cached. `internal/services/compatibility.go` has
   `majorMinor`.
6. **A capabilities surface already exists end-to-end.** Backend:
   `GET /projects/{id}/capabilities` (`ProjectHandler.GetCapabilities`)
   returns `{ "rateLimitAvailable": bool }` from
   `k8sService.IsRateLimitAvailable`. Frontend: `projectsApi.getCapabilities`
   → `ProjectCapabilities { rateLimitAvailable }`, consumed to gate the
   rate-limit UI (hide the form; show a "could not verify" note on error).
   The idiomatic extension is to add `streamAvailable` to that DTO.

## Design

### Component 1 — capability registry (`internal/capabilities`, new package)

Pure predicates over a plain versions struct — no Kubernetes or services
dependency, trivially unit-testable:

```go
type Versions struct {
    EnvoyGateway string // detected EG version, "" if unknown
    GatewayAPI   string // detected Gateway API version, "" if unknown
}

type Capability struct {
    Name               string
    Predicate          func(Versions) bool
    Exposed            bool // include in GET /projects/{id}/capabilities?
    DefaultWhenUnknown bool // value when the relevant version is "" / unparseable
}

var Registry = []Capability{
    {
        Name:               "streams",
        Predicate:          func(v Versions) bool { return minorAtLeast(v.EnvoyGateway, 1, 8) },
        Exposed:            true,
        DefaultWhenUnknown: true, // optimistic; backend guard is the real net
    },
    {
        Name:               "l4RouteV1",
        Predicate:          func(v Versions) bool { return minorAtLeast(v.GatewayAPI, 1, 6) },
        Exposed:            false,
        DefaultWhenUnknown: true, // default to v1 (matches "write v1 unless proven <1.6")
    },
}
```

`minorAtLeast(ver, maj, min)` parses `major.minor` (reusing the `majorMinor`
logic) and returns whether `ver` is a positively-parsed version `>= maj.min`;
an empty/unparseable `ver` returns `false` — the caller then applies the
capability's `DefaultWhenUnknown`. A helper `Evaluate(v Versions)` returns the
`map[string]bool` of all capabilities (predicate result, or
`DefaultWhenUnknown` when the needed version is absent).

### Component 2 — capability service (wiring, `internal/services`)

A thin service binds the pure registry to the cached version detection:

```go
// GatewayAPIVersionResolver / version source already exists via ProjectVersionService.
func (s *CapabilityService) Has(ctx, projectID, name) (bool, error)        // internal queries
func (s *CapabilityService) Evaluate(ctx, projectID) (map[string]bool, error) // exposed subset for the DTO
```

It reads the cached `VersionInfo` (no new probe), builds a
`capabilities.Versions`, and evaluates the registry. A version-probe error
surfaces as empty versions, so every capability falls back to its
`DefaultWhenUnknown` — a detection hiccup never errors a caller.

### Component 3 — expose on the existing endpoint

Extend `ProjectHandler.GetCapabilities` so the `/projects/{id}/capabilities`
response carries the **Exposed** version-derived capabilities beside the
existing probe-derived one:

```json
{ "rateLimitAvailable": true, "streamAvailable": true }
```

`rateLimitAvailable` keeps coming from `IsRateLimitAvailable` (unchanged);
`streamAvailable` comes from `CapabilityService.Evaluate(...)["streams"]`. The
handler gains the capability service as a dependency.

### Component 4 — internal consumer: L4 route API version (`l4RouteV1`)

This is the former standalone spec, now a capability consumer.

- `internal/kubernetes/gvr.go`: keep `TCPRouteGVR`/`UDPRouteGVR` (`v1alpha2`);
  add `TCPRouteGVRV1`/`UDPRouteGVRV1` (`v1`).
- `route_deploy_service` resolves **once per deploy**: `Has("l4RouteV1")` →
  true selects the `v1` GVR, false the `v1alpha2` GVR, for TCP and UDP.
- The six `L4RouteApplier` methods (`Create/Update/DeleteTCPRoute` + UDP) take
  a uniform `gvr schema.GroupVersionResource` parameter (one real caller;
  regenerates `MockL4RouteApplier`).
- `internal/cluster/route.go` uses the passed GVR for the REST path; the
  `BuildTCPRouteObject`/`BuildUDPRouteObject` builders are parameterized to set
  `TypeMeta.APIVersion` from `gvr.GroupVersion().String()`, keeping the
  existing `gatewayv1alpha2` typed spec struct (the graduation was a straight
  promotion — the spec serializes byte-identically as `v1`). The single
  resolved GVR drives both the path and the body `apiVersion`; they cannot
  diverge.
- `l4RouteV1` default-when-unknown is `true` ⇒ write `v1` unless Gateway API
  is positively detected `< 1.6`. `v1` is safe on any ≥ 1.6 cluster (both
  channels); the only non-`v1` clusters are GwAPI 1.5 (EG 1.8), which we
  positively detect → `v1alpha2`.

### Component 5 — exposed consumer: streams availability (`streams`)

- **Backend guard (correctness):** the stream-create service path
  (`route_service.CreateForStream` / the stream-create service) rejects
  creation when `Has(ctx, projectID, "streams")` is false — i.e. a
  positively-detected EG < 1.8 — with a clear error. Given
  `DefaultWhenUnknown: true`, the guard never fires on a detection hiccup,
  only on a confirmed-old cluster.
- **Frontend gate (UX):** add `streamAvailable` to `ProjectCapabilities`
  (`src/types`), read it in `streams/page.tsx` `loadData` (alongside the
  existing `permissions?.canManageDomains`), and:
  - hide/disable the "New Stream" / "Create Stream" buttons when
    `streamAvailable === false`;
  - on the create page, show an amber "Streams require Envoy Gateway ≥ 1.8"
    branch instead of the form (mirroring the existing "no stream-enabled
    Gateway Templates" branch and the `rateLimitAvailable` notice);
  - on a capabilities fetch error, show a "could not verify" note and fall
    open (optimistic), mirroring the rate-limit pattern.

### Data flow

```
GET /projects/{id}/capabilities
  └─ CapabilityService.Evaluate(projectID)
       └─ ProjectVersionService.Get (cached)  → Versions{EG, GwAPI}
            └─ capabilities.Registry           → { streams: bool, ... exposed }
  + k8sService.IsRateLimitAvailable            → rateLimitAvailable
  = { rateLimitAvailable, streamAvailable }

route_deploy_service.deploy(stream)
  └─ CapabilityService.Has("l4RouteV1")        → v1 (default) | v1alpha2 GVR
       └─ cluster.Create/Update/Delete{TCP,UDP}Route(..., gvr)

stream create (service)
  └─ CapabilityService.Has("streams") == false → reject (clear error)
```

## Fallback and safety

- Every capability has an explicit `DefaultWhenUnknown`; both current ones are
  `true` (optimistic — assume the newer/more-capable cluster when the version
  can't be read). A probe error maps to empty versions → defaults apply →
  no caller errors on detection failure.
- `l4RouteV1` default `true` ⇒ default `v1`; only a positively-detected GwAPI
  `< 1.6` writes `v1alpha2`. Rationale: `v1` is universal on ≥ 1.6 (both
  channels) while `v1alpha2` is experimental-only on 1.6, so defaulting to
  `v1` never downgrades a modern/standard-channel cluster to an unserved
  version.
- Known edge: if detection fails on a genuine GwAPI 1.5 cluster, the default
  `v1` write fails there (no `v1` CRD). Unlikely (version read off an existing
  CRD annotation); accepted trade for never downgrading a modern cluster.
- `streams` default `true` ⇒ the UI offers streams on an undetected cluster;
  the backend guard (which also sees undetected → true) won't block, so a
  genuinely-old undetected cluster would fail at deploy. Accepted: blocking a
  working feature on a detection hiccup is worse, and detection rarely fails.
- Policies unaffected: BackendTrafficPolicy (and all policy) `targetRef` is
  group + kind + name — no apiVersion — so the `v1`/`v1alpha2` switch does not
  touch policy attachment. No stored-object migration (one object; Key Fact 2).

## Testing

**Backend (unit):**
1. **Registry predicates** (`internal/capabilities`): table test for
   `streams` and `l4RouteV1` — e.g. EG `"1.8.4"`/`"1.9.1"` → `streams` true,
   `"1.7.5"` → false; GwAPI `"1.6.x"`/`"1.7.x"`/`"2.0.0"` → `l4RouteV1` true,
   `"1.5.1"`/`"1.4.1"` → false; and `""`/garbage → each capability's
   `DefaultWhenUnknown` (both true).
2. **Capability service**: with a mocked version source (including the
   probe-error → empty case), `Has`/`Evaluate` return the expected values and
   never error on detection failure.
3. **L4 resolution**: deploy-service test (mocked capability service + mocked
   `k8sL4Routes`) asserts the expected GVR is passed — `v1alpha2` only for a
   positively-detected `1.5`/`1.4`, `v1` otherwise.
4. **Cluster write** (fake dynamic client): `CreateTCPRoute`/`CreateUDPRoute`
   create an object whose `apiVersion` equals `gvr.GroupVersion().String()` at
   the matching resource path, for both `v1` and `v1alpha2`.
5. **Stream guard**: stream-create rejects with a clear error when `streams`
   is false (positively-detected EG < 1.8) and proceeds otherwise (including
   the undetected→true case).

Full backend suite (`go test ./... -count=1`) stays green, with regenerated
mocks (`make mocks` / `make mocks-check`) for the widened `L4RouteApplier`.

**Frontend:** gate behavior on `streamAvailable` (button hidden/disabled when
false; amber branch on the create page; optimistic on fetch error), following
the existing `rateLimitAvailable` test pattern.

## Global Constraints

- Capabilities are pure functions of detected versions; no runtime toggles.
- `rateLimitAvailable` stays probe-derived and unchanged; the endpoint
  aggregates it with version-derived flags.
- `SupportedVersionPairs` (multi-version support: EG 1.8 + 1.9) is unchanged;
  this feature reads detected versions, it does not change the support matrix.
- `v1alpha2` remains the path for positively-detected GwAPI < 1.6; `v1` is the
  default.
- Mocks are generated; the widened `L4RouteApplier` must regenerate and
  `make mocks-check` must pass.
