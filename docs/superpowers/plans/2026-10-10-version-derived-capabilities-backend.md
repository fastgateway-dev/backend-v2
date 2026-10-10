# Version-Derived Capabilities (Backend) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Introduce a version-derived capability registry and land two consumers — write `v1` TCPRoute/UDPRoute on Gateway API ≥ 1.6 (else `v1alpha2`), and expose a `streamAvailable` capability (EG ≥ 1.8) gated by a stream-create guard.

**Architecture:** A pure `internal/capabilities` registry (predicates over detected `(EG, GatewayAPI)` versions) is evaluated by a `CapabilityService` on top of the existing cached `ProjectVersionService`. Backend code queries it two ways: internal (`Has("l4RouteV1")` picks the L4 route GVR) and exposed (`streamAvailable` rides the existing `/projects/{id}/capabilities` endpoint; a guard in `StreamService.Create` rejects streams on positively-detected EG < 1.8).

**Tech Stack:** Go 1.26, gin, `sigs.k8s.io/gateway-api v1.6.1`, `k8s.io/client-go` dynamic client, mockery-generated mocks.

**Spec:** `docs/superpowers/specs/2026-10-10-version-derived-capabilities-design.md`

## Global Constraints

- Capabilities are pure functions of detected versions — no runtime toggles, no remote config.
- `rateLimitAvailable` stays probe-derived (`IsRateLimitAvailable`) and unchanged; the `/capabilities` endpoint aggregates it with version-derived flags.
- Default is `v1`; write `v1alpha2` **only** for a positively-detected Gateway API `< 1.6`. Empty / unparseable / `≥1.6` / probe-error → `v1`.
- `streams` and `l4RouteV1` both have `DefaultWhenUnknown = true` (optimistic).
- `SupportedVersionPairs` (EG 1.8 + 1.9 support) is unchanged.
- Mocks are generated: after any interface change run `make mocks` and `make mocks-check` must pass.
- Full suite `go test ./... -count=1` must stay green.
- Commit messages end with: `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`

## Review Focus

- **Undetected/empty version** → each capability must fall back to `DefaultWhenUnknown` (true), never crash or error the caller (covered: Task 1, Task 2).
- **Unparseable version string** (`"latest"`, `"1"`, `""`) → treated as unknown → default (covered: Task 1).
- **UpdateTCPRoute/UpdateUDPRoute read-before-write** must `Get` at the **same** GVR it will `Update`, or a 1.6 cluster reads `v1` but writes `v1alpha2` (covered: Task 4).
- **Stream guard must NOT fire on undetected** EG (optimistic) — only on positively-detected EG < 1.8 (covered: Task 6).
- **`/capabilities` aggregation** must still return `rateLimitAvailable` unchanged alongside the new flag (covered: Task 7).

---

### Task 1: Capability registry (pure package)

**Files:**
- Create: `internal/capabilities/capabilities.go`
- Test: `internal/capabilities/capabilities_test.go`

**Interfaces:**
- Produces: `capabilities.Versions{EnvoyGateway, GatewayAPI string}`; `capabilities.Capability`; `capabilities.Registry []Capability`; `capabilities.Evaluate(Versions) map[string]bool`; `capabilities.Lookup(name string) (Capability, bool)`.

- [ ] **Step 1: Write the failing test**

```go
package capabilities

import "testing"

func TestEvaluate(t *testing.T) {
	cases := []struct {
		name     string
		versions Versions
		want     map[string]bool
	}{
		{"eg19 gw16 -> both v1/streams", Versions{"1.9.1", "1.6.1"}, map[string]bool{"streams": true, "l4RouteV1": true}},
		{"eg18 gw15 -> streams yes, l4 alpha", Versions{"1.8.4", "1.5.1"}, map[string]bool{"streams": true, "l4RouteV1": false}},
		{"eg17 gw14 -> neither", Versions{"1.7.5", "1.4.1"}, map[string]bool{"streams": false, "l4RouteV1": false}},
		{"leading v tolerated", Versions{"v1.9.1", "v1.6.1"}, map[string]bool{"streams": true, "l4RouteV1": true}},
		{"future major -> true", Versions{"2.0.0", "2.0.0"}, map[string]bool{"streams": true, "l4RouteV1": true}},
		{"empty -> defaults (both true)", Versions{"", ""}, map[string]bool{"streams": true, "l4RouteV1": true}},
		{"garbage -> defaults (both true)", Versions{"latest", "dev"}, map[string]bool{"streams": true, "l4RouteV1": true}},
		{"bare major -> defaults", Versions{"1", "1"}, map[string]bool{"streams": true, "l4RouteV1": true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Evaluate(c.versions)
			for k, want := range c.want {
				if got[k] != want {
					t.Errorf("%s: cap %q = %v, want %v", c.name, k, got[k], want)
				}
			}
		})
	}
}

func TestLookup(t *testing.T) {
	if c, ok := Lookup("streams"); !ok || !c.Exposed {
		t.Fatalf("streams must exist and be Exposed; got %+v ok=%v", c, ok)
	}
	if c, ok := Lookup("l4RouteV1"); !ok || c.Exposed {
		t.Fatalf("l4RouteV1 must exist and be internal (not Exposed); got %+v ok=%v", c, ok)
	}
	if _, ok := Lookup("nope"); ok {
		t.Fatal("unknown capability must return ok=false")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/capabilities/ -run 'TestEvaluate|TestLookup' -v`
Expected: FAIL — `undefined: Evaluate` / `undefined: Versions` (package has no implementation yet).

- [ ] **Step 3: Write minimal implementation**

```go
package capabilities

import (
	"strconv"
	"strings"
)

// Versions holds the detected cluster versions a capability reasons about.
// An empty field means that version could not be detected.
type Versions struct {
	EnvoyGateway string
	GatewayAPI   string
}

// Capability is a named, version-derived feature decision. Source selects the
// version string the capability reasons about; the capability is true when
// that version parses and is >= MinMajor.MinMinor, and DefaultWhenUnknown when
// the version is absent/unparseable.
type Capability struct {
	Name               string
	Source             func(Versions) string
	MinMajor           int
	MinMinor           int
	Exposed            bool // surface in GET /projects/{id}/capabilities?
	DefaultWhenUnknown bool
}

// Registry is the single source of truth for version-derived capabilities.
var Registry = []Capability{
	{
		Name:               "streams",
		Source:             func(v Versions) string { return v.EnvoyGateway },
		MinMajor:           1,
		MinMinor:           8,
		Exposed:            true,
		DefaultWhenUnknown: true,
	},
	{
		Name:               "l4RouteV1",
		Source:             func(v Versions) string { return v.GatewayAPI },
		MinMajor:           1,
		MinMinor:           6,
		Exposed:            false,
		DefaultWhenUnknown: true,
	},
}

// Evaluate returns every capability's value for v.
func Evaluate(v Versions) map[string]bool {
	out := make(map[string]bool, len(Registry))
	for _, c := range Registry {
		out[c.Name] = evalOne(c, v)
	}
	return out
}

// Lookup returns the capability with the given name.
func Lookup(name string) (Capability, bool) {
	for _, c := range Registry {
		if c.Name == name {
			return c, true
		}
	}
	return Capability{}, false
}

func evalOne(c Capability, v Versions) bool {
	maj, min, ok := parseMajorMinor(c.Source(v))
	if !ok {
		return c.DefaultWhenUnknown
	}
	return maj > c.MinMajor || (maj == c.MinMajor && min >= c.MinMinor)
}

// parseMajorMinor extracts major and minor ints from a semver-ish string,
// tolerating a leading "v". Returns ok=false for empty/unparseable input.
func parseMajorMinor(v string) (maj, min int, ok bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	maj, err1 := strconv.Atoi(parts[0])
	min, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return maj, min, true
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/capabilities/ -v`
Expected: PASS (both tests, all sub-cases).

- [ ] **Step 5: Commit**

```bash
git add internal/capabilities/
git commit -m "feat(capabilities): version-derived capability registry

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 2: CapabilityService (services wiring)

**Files:**
- Create: `internal/services/capability_service.go`
- Test: `internal/services/capability_service_test.go`

**Interfaces:**
- Consumes: `capabilities.Evaluate`, `capabilities.Registry`; the existing `*VersionInfo` with `.EnvoyGateway.Version` / `.GatewayAPI.Version`.
- Produces: `CapabilityService` with `Has(ctx, projectID, name) bool` and `Evaluate(ctx, projectID) map[string]bool`; `NewCapabilityService(versionInfoGetter) *CapabilityService`; interface `versionInfoGetter { Get(ctx, uuid.UUID, bool) (*VersionInfo, error) }`.

- [ ] **Step 1: Write the failing test**

```go
package services

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

type fakeVersionGetter struct {
	info *VersionInfo
	err  error
}

func (f fakeVersionGetter) Get(context.Context, uuid.UUID, bool) (*VersionInfo, error) {
	return f.info, f.err
}

func infoWith(eg, gw string) *VersionInfo {
	return &VersionInfo{
		EnvoyGateway: ProbeResult{Version: eg, Detected: eg != ""},
		GatewayAPI:   ProbeResult{Version: gw, Detected: gw != ""},
	}
}

func TestCapabilityService_Has(t *testing.T) {
	ctx, id := context.Background(), uuid.New()
	tests := []struct {
		name               string
		getter             fakeVersionGetter
		streams, l4RouteV1 bool
	}{
		{"eg19 gw16", fakeVersionGetter{info: infoWith("1.9.1", "1.6.1")}, true, true},
		{"eg18 gw15", fakeVersionGetter{info: infoWith("1.8.4", "1.5.1")}, true, false},
		{"eg17 gw14", fakeVersionGetter{info: infoWith("1.7.5", "1.4.1")}, false, false},
		{"probe error -> defaults", fakeVersionGetter{err: errors.New("unreachable")}, true, true},
		{"nil info -> defaults", fakeVersionGetter{info: nil}, true, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := NewCapabilityService(tc.getter)
			if got := s.Has(ctx, id, "streams"); got != tc.streams {
				t.Errorf("streams = %v, want %v", got, tc.streams)
			}
			if got := s.Has(ctx, id, "l4RouteV1"); got != tc.l4RouteV1 {
				t.Errorf("l4RouteV1 = %v, want %v", got, tc.l4RouteV1)
			}
		})
	}
}

func TestCapabilityService_EvaluateExposedOnly(t *testing.T) {
	s := NewCapabilityService(fakeVersionGetter{info: infoWith("1.9.1", "1.6.1")})
	got := s.Evaluate(context.Background(), uuid.New())
	if _, ok := got["streams"]; !ok {
		t.Error("Evaluate must include exposed capability 'streams'")
	}
	if _, ok := got["l4RouteV1"]; ok {
		t.Error("Evaluate must NOT include internal capability 'l4RouteV1'")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/services/ -run 'TestCapabilityService' -v`
Expected: FAIL — `undefined: NewCapabilityService`.

- [ ] **Step 3: Write minimal implementation**

```go
package services

import (
	"context"

	"github.com/google/uuid"

	"github.com/fastgateway-dev/backend-v2/internal/capabilities"
)

// versionInfoGetter is the slice of ProjectVersionService that CapabilityService
// needs: the cached version read. *ProjectVersionService satisfies it.
type versionInfoGetter interface {
	Get(ctx context.Context, projectID uuid.UUID, forceRefresh bool) (*VersionInfo, error)
}

// CapabilityService answers version-derived capability questions per project,
// reading the cached detected versions. It never errors: a detection failure
// surfaces as unknown versions, so every capability falls back to its
// DefaultWhenUnknown.
type CapabilityService struct {
	versions versionInfoGetter
}

// NewCapabilityService builds a CapabilityService. Panics if versions is nil.
func NewCapabilityService(versions versionInfoGetter) *CapabilityService {
	if versions == nil {
		panic("services.NewCapabilityService: missing required dependency: versions")
	}
	return &CapabilityService{versions: versions}
}

func (s *CapabilityService) versionsFor(ctx context.Context, projectID uuid.UUID) capabilities.Versions {
	info, err := s.versions.Get(ctx, projectID, false)
	if err != nil || info == nil {
		return capabilities.Versions{}
	}
	return capabilities.Versions{
		EnvoyGateway: info.EnvoyGateway.Version,
		GatewayAPI:   info.GatewayAPI.Version,
	}
}

// Has reports a single capability for the project.
func (s *CapabilityService) Has(ctx context.Context, projectID uuid.UUID, name string) bool {
	return capabilities.Evaluate(s.versionsFor(ctx, projectID))[name]
}

// Evaluate returns only the Exposed capabilities for the project (API DTO use).
func (s *CapabilityService) Evaluate(ctx context.Context, projectID uuid.UUID) map[string]bool {
	all := capabilities.Evaluate(s.versionsFor(ctx, projectID))
	out := make(map[string]bool)
	for _, c := range capabilities.Registry {
		if c.Exposed {
			out[c.Name] = all[c.Name]
		}
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/services/ -run 'TestCapabilityService' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/services/capability_service.go internal/services/capability_service_test.go
git commit -m "feat(capabilities): CapabilityService over cached version detection

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 3: v1 GVR consts + parameterized L4 builders

**Files:**
- Modify: `internal/kubernetes/gvr.go` (add `TCPRouteGVRV1`, `UDPRouteGVRV1` beside the existing `v1alpha2` consts at lines 88-100)
- Modify: `internal/kubernetes/tcproute.go:67` (`BuildTCPRouteObject` signature + `TypeMeta`)
- Modify: `internal/kubernetes/udproute.go:20` (`BuildUDPRouteObject` signature + `TypeMeta`)
- Test: `internal/kubernetes/l4route_apiversion_test.go`

**Interfaces:**
- Produces: `kubernetes.TCPRouteGVRV1`, `kubernetes.UDPRouteGVRV1` (`schema.GroupVersionResource`, Version `"v1"`); `BuildTCPRouteObject(cfg TCPRouteConfig, apiVersion string) *gatewayv1alpha2.TCPRoute`; `BuildUDPRouteObject(cfg UDPRouteConfig, apiVersion string) *gatewayv1alpha2.UDPRoute`.

- [ ] **Step 1: Write the failing test**

```go
package kubernetes

import "testing"

func TestBuildTCPRouteObject_APIVersion(t *testing.T) {
	cfg := TCPRouteConfig{Name: "t", Namespace: "ns", GatewayName: "gw", SectionName: "l4-tcp-1"}
	for _, av := range []string{"gateway.networking.k8s.io/v1", "gateway.networking.k8s.io/v1alpha2"} {
		if got := BuildTCPRouteObject(cfg, av).APIVersion; got != av {
			t.Errorf("TCPRoute APIVersion = %q, want %q", got, av)
		}
	}
	if k := BuildTCPRouteObject(cfg, "gateway.networking.k8s.io/v1").Kind; k != "TCPRoute" {
		t.Errorf("Kind = %q, want TCPRoute", k)
	}
}

func TestBuildUDPRouteObject_APIVersion(t *testing.T) {
	cfg := UDPRouteConfig{Name: "u", Namespace: "ns", GatewayName: "gw", SectionName: "l4-udp-1"}
	for _, av := range []string{"gateway.networking.k8s.io/v1", "gateway.networking.k8s.io/v1alpha2"} {
		if got := BuildUDPRouteObject(cfg, av).APIVersion; got != av {
			t.Errorf("UDPRoute APIVersion = %q, want %q", got, av)
		}
	}
}

func TestL4RouteGVRV1Consts(t *testing.T) {
	if TCPRouteGVRV1.Version != "v1" || TCPRouteGVRV1.Resource != "tcproutes" {
		t.Errorf("TCPRouteGVRV1 wrong: %+v", TCPRouteGVRV1)
	}
	if UDPRouteGVRV1.Version != "v1" || UDPRouteGVRV1.Resource != "udproutes" {
		t.Errorf("UDPRouteGVRV1 wrong: %+v", UDPRouteGVRV1)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/kubernetes/ -run 'TestBuild.*APIVersion|TestL4RouteGVRV1' -v`
Expected: FAIL — too many arguments to `BuildTCPRouteObject` / `undefined: TCPRouteGVRV1`.

- [ ] **Step 3: Write minimal implementation**

In `internal/kubernetes/gvr.go`, after the existing `UDPRouteGVR` block (line ~100), add:

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

In `internal/kubernetes/tcproute.go`, change the builder signature and `TypeMeta`:

```go
// BuildTCPRouteObject builds a typed TCPRoute from config at the given
// apiVersion (e.g. "gateway.networking.k8s.io/v1"). The v1alpha2 and v1 spec
// schemas are identical (straight graduation), so the v1alpha2 Go type
// serializes correctly under either apiVersion.
func BuildTCPRouteObject(cfg TCPRouteConfig, apiVersion string) *gatewayv1alpha2.TCPRoute {
	return &gatewayv1alpha2.TCPRoute{
		TypeMeta: metav1.TypeMeta{
			APIVersion: apiVersion,
			Kind:       "TCPRoute",
		},
		// ... ObjectMeta and Spec unchanged ...
```

In `internal/kubernetes/udproute.go`, the same change for `BuildUDPRouteObject(cfg UDPRouteConfig, apiVersion string)` with `APIVersion: apiVersion`, `Kind: "UDPRoute"`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/kubernetes/ -run 'TestBuild.*APIVersion|TestL4RouteGVRV1' -v`
Expected: PASS. (The package will not compile elsewhere yet — `cluster/route.go` still calls the old 1-arg builders. That is fixed in Task 4; this task's package test compiles because the test calls the new 2-arg form.)

Note: `go build ./...` will fail until Task 4 updates the callers — expected and resolved there. Run the package test, not the whole build, to confirm this task.

- [ ] **Step 5: Commit**

```bash
git add internal/kubernetes/gvr.go internal/kubernetes/tcproute.go internal/kubernetes/udproute.go internal/kubernetes/l4route_apiversion_test.go
git commit -m "feat(kubernetes): v1 L4 GVRs + apiVersion-parameterized route builders

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 4: Thread GVR through L4RouteApplier + cluster writes

**Files:**
- Modify: `internal/services/k8s_roles.go:64-71` (the `L4RouteApplier` interface — add a `gvr schema.GroupVersionResource` param to all six methods)
- Modify: `internal/cluster/route.go:216-379` (the six methods use the passed GVR + derive apiVersion)
- Regenerate: `internal/mocks/mock_services.go` (MockL4RouteApplier) via `make mocks`
- Test: `internal/cluster/route_l4_version_test.go`

**Interfaces:**
- Consumes: `kubernetes.TCPRouteGVR`, `kubernetes.TCPRouteGVRV1`, `kubernetes.UDPRouteGVR`, `kubernetes.UDPRouteGVRV1`; the Task 3 builders.
- Produces: `L4RouteApplier` methods now `(..., gvr schema.GroupVersionResource)`:
  `CreateTCPRoute(ctx, projectID, config, gvr)`, `UpdateTCPRoute(...)`, `DeleteTCPRoute(ctx, projectID, namespace, name, gvr)`, and the three UDP equivalents.

- [ ] **Step 1: Write the failing test**

```go
package cluster

import (
	"context"
	"testing"

	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
)

func TestCreateTCPRoute_WritesAtResolvedGVR(t *testing.T) {
	scheme := runtime.NewScheme()
	fake := dynamicfake.NewSimpleDynamicClient(scheme)
	c := NewWithClient(fake)
	cfg := &kubernetes.TCPRouteConfig{Name: "r", Namespace: "ns", GatewayName: "gw", SectionName: "l4-tcp-1"}

	if err := c.CreateTCPRoute(context.Background(), uuid.New(), cfg, kubernetes.TCPRouteGVRV1); err != nil {
		t.Fatalf("create v1: %v", err)
	}
	got, err := fake.Resource(kubernetes.TCPRouteGVRV1).Namespace("ns").Get(context.Background(), "r", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected object at v1 GVR: %v", err)
	}
	if av := got.GetAPIVersion(); av != "gateway.networking.k8s.io/v1" {
		t.Errorf("object apiVersion = %q, want .../v1", av)
	}
}
```

(Note: `NewWithClient` already exists in `internal/cluster/versions.go` and injects the fake dynamic client for all project IDs.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cluster/ -run 'TestCreateTCPRoute_WritesAtResolvedGVR' -v`
Expected: FAIL — not enough arguments in call to `c.CreateTCPRoute` (method still has the old 3-arg signature).

- [ ] **Step 3: Write minimal implementation**

In `internal/services/k8s_roles.go`, widen the interface (import `schema "k8s.io/apimachinery/pkg/runtime/schema"`):

```go
type L4RouteApplier interface {
	CreateTCPRoute(ctx context.Context, projectID uuid.UUID, config *kubernetes.TCPRouteConfig, gvr schema.GroupVersionResource) error
	UpdateTCPRoute(ctx context.Context, projectID uuid.UUID, config *kubernetes.TCPRouteConfig, gvr schema.GroupVersionResource) error
	DeleteTCPRoute(ctx context.Context, projectID uuid.UUID, namespace, name string, gvr schema.GroupVersionResource) error
	CreateUDPRoute(ctx context.Context, projectID uuid.UUID, config *kubernetes.UDPRouteConfig, gvr schema.GroupVersionResource) error
	UpdateUDPRoute(ctx context.Context, projectID uuid.UUID, config *kubernetes.UDPRouteConfig, gvr schema.GroupVersionResource) error
	DeleteUDPRoute(ctx context.Context, projectID uuid.UUID, namespace, name string, gvr schema.GroupVersionResource) error
}
```

In `internal/cluster/route.go`, update all six methods to accept `gvr schema.GroupVersionResource`, replace the `gvr := kubernetes.TCPRouteGVR` / `UDPRouteGVR` lines with use of the parameter, and pass `gvr.GroupVersion().String()` to the builder. For example `CreateTCPRoute`:

```go
func (s *Client) CreateTCPRoute(ctx context.Context, projectID uuid.UUID, config *kubernetes.TCPRouteConfig, gvr schema.GroupVersionResource) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}
	route := kubernetes.BuildTCPRouteObject(*config, gvr.GroupVersion().String())
	if route == nil {
		return fmt.Errorf("failed to build TCPRoute object")
	}
	unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(route)
	if err != nil {
		return fmt.Errorf("failed to convert TCPRoute to unstructured: %w", err)
	}
	obj := &unstructured.Unstructured{Object: unstructuredObj}
	_, err = client.Resource(gvr).Namespace(config.Namespace).Create(ctx, obj, metav1.CreateOptions{})
	if err != nil {
		if k8serrors.IsAlreadyExists(err) {
			return s.UpdateTCPRoute(ctx, projectID, config, gvr)
		}
		return fmt.Errorf("failed to create TCPRoute: %w", err)
	}
	return nil
}
```

Apply the same pattern to `UpdateTCPRoute` (the read-before-write `Get` **must** use the same `gvr`), `DeleteTCPRoute` (`client.Resource(gvr)`), and the three UDP methods. Add `schema "k8s.io/apimachinery/pkg/runtime/schema"` to the imports if not present.

Regenerate the mock:

Run: `make mocks`
Then confirm clean: `make mocks-check`

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cluster/ -run 'TestCreateTCPRoute_WritesAtResolvedGVR' -v && go build ./...`
Expected: test PASS; `go build ./...` still fails only in `route_deploy_service.go` (callers not updated) — that is Task 5. If any OTHER package fails to build, fix it here.

- [ ] **Step 5: Commit**

```bash
git add internal/services/k8s_roles.go internal/cluster/route.go internal/cluster/route_l4_version_test.go internal/mocks/
git commit -m "feat(cluster): L4 route methods take a resolved GVR

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 5: Resolve the L4 GVR in the deploy path

**Files:**
- Modify: `internal/services/route_deploy_service.go:23-49` (add a capability resolver field), `:430-479` (`applyL4Route` / `deleteL4Route` resolve + pass the GVR)
- Modify: `internal/services/route_service.go:84-305` (`RouteServiceDeps` + the `routeDeploy` composite-literal wiring)
- Modify: `cmd/server/main.go` (construct `CapabilityService`, pass into `RouteServiceDeps`)
- Test: `internal/services/route_deploy_l4_version_test.go`

**Interfaces:**
- Consumes: `CapabilityService.Has(ctx, projectID, "l4RouteV1")` (via a narrow interface for mockability); `kubernetes.TCPRouteGVR/GVRV1`, `UDPRouteGVR/GVRV1`; the Task 4 method signatures.
- Produces: `l4VersionResolver interface { Has(ctx, projectID, name) bool }` field on `routeDeploy`; helper `l4RouteGVR(protocol models.RouteProtocol, useV1 bool) schema.GroupVersionResource`.

- [ ] **Step 1: Write the failing test**

```go
package services

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
)

func TestL4RouteGVR(t *testing.T) {
	cases := []struct {
		proto models.RouteProtocol
		useV1 bool
		want  schema.GroupVersionResource
	}{
		{models.RouteProtocolTCP, true, kubernetes.TCPRouteGVRV1},
		{models.RouteProtocolTCP, false, kubernetes.TCPRouteGVR},
		{models.RouteProtocolUDP, true, kubernetes.UDPRouteGVRV1},
		{models.RouteProtocolUDP, false, kubernetes.UDPRouteGVR},
	}
	for _, c := range cases {
		if got := l4RouteGVR(c.proto, c.useV1); got != c.want {
			t.Errorf("l4RouteGVR(%v,%v) = %+v, want %+v", c.proto, c.useV1, got, c.want)
		}
	}
	_ = context.Background
	_ = uuid.New
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/services/ -run 'TestL4RouteGVR' -v`
Expected: FAIL — `undefined: l4RouteGVR`.

- [ ] **Step 3: Write minimal implementation**

Add to `route_deploy_service.go` (import `schema "k8s.io/apimachinery/pkg/runtime/schema"`):

```go
// l4VersionResolver answers whether the project's cluster takes the v1 L4
// route types. *CapabilityService satisfies it.
type l4VersionResolver interface {
	Has(ctx context.Context, projectID uuid.UUID, name string) bool
}

// l4RouteGVR picks the TCPRoute/UDPRoute GVR for the protocol: the v1 GVR when
// useV1, else the v1alpha2 GVR.
func l4RouteGVR(protocol models.RouteProtocol, useV1 bool) schema.GroupVersionResource {
	switch protocol {
	case models.RouteProtocolTCP:
		if useV1 {
			return kubernetes.TCPRouteGVRV1
		}
		return kubernetes.TCPRouteGVR
	case models.RouteProtocolUDP:
		if useV1 {
			return kubernetes.UDPRouteGVRV1
		}
		return kubernetes.UDPRouteGVR
	}
	return schema.GroupVersionResource{}
}
```

Add a `capabilities l4VersionResolver` field to the `routeDeploy` struct (route_deploy_service.go:23-49), add `Capabilities l4VersionResolver` to `RouteServiceDeps` (route_service.go:84), and wire `capabilities: deps.Capabilities` into the `&routeDeploy{...}` literal (route_service.go:282-305).

Rewrite `applyL4Route` and `deleteL4Route` to resolve and pass the GVR. For `applyL4Route`:

```go
func (d *routeDeploy) applyL4Route(ctx context.Context, route *models.Route, stream *models.Stream, create bool) error {
	verb := "update"
	if create {
		verb = "create"
	}
	useV1 := d.capabilities.Has(ctx, stream.ProjectID, "l4RouteV1")
	gvr := l4RouteGVR(route.Protocol, useV1)
	switch route.Protocol {
	case models.RouteProtocolTCP:
		cfg := d.assembler.buildTCPRouteConfig(route, stream)
		apply := d.k8sL4Routes.UpdateTCPRoute
		if create {
			apply = d.k8sL4Routes.CreateTCPRoute
		}
		if err := apply(ctx, stream.ProjectID, cfg, gvr); err != nil {
			log.Printf("Failed to %s TCPRoute in Kubernetes: %v", verb, err)
			return fmt.Errorf("failed to %s TCPRoute in Kubernetes: %w", verb, err)
		}
	case models.RouteProtocolUDP:
		cfg := d.assembler.buildUDPRouteConfig(route, stream)
		apply := d.k8sL4Routes.UpdateUDPRoute
		if create {
			apply = d.k8sL4Routes.CreateUDPRoute
		}
		if err := apply(ctx, stream.ProjectID, cfg, gvr); err != nil {
			log.Printf("Failed to %s UDPRoute in Kubernetes: %v", verb, err)
			return fmt.Errorf("failed to %s UDPRoute in Kubernetes: %w", verb, err)
		}
	default:
		return fmt.Errorf("unsupported L4 protocol %q", route.Protocol)
	}
	return nil
}
```

For `deleteL4Route`, resolve `gvr := l4RouteGVR(route.Protocol, d.capabilities.Has(ctx, stream.ProjectID, "l4RouteV1"))` and pass it to `DeleteTCPRoute(ctx, stream.ProjectID, stream.Namespace, route.K8sRouteName, gvr)` / `DeleteUDPRoute(...)`.

In `cmd/server/main.go`, construct `capabilityService := services.NewCapabilityService(projectVersionService)` (the existing `*ProjectVersionService` instance) and set `Capabilities: capabilityService` in `RouteServiceDeps`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/services/ -run 'TestL4RouteGVR' -v && go build ./...`
Expected: test PASS and `go build ./...` now succeeds (all callers updated).

- [ ] **Step 5: Commit**

```bash
git add internal/services/route_deploy_service.go internal/services/route_service.go cmd/server/main.go internal/services/route_deploy_l4_version_test.go
git commit -m "feat(routes): resolve L4 route apiVersion from l4RouteV1 capability

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 6: Stream-create guard on the `streams` capability

**Files:**
- Modify: `internal/services/stream_service.go:136-184` (`StreamService` gains an optional capability checker + a guard at the top of `Create`)
- Modify: `cmd/server/main.go` (wire the checker into the `StreamService` via a setter)
- Test: `internal/services/stream_service_capability_test.go`

**Interfaces:**
- Consumes: `CapabilityService.Has(ctx, projectID, "streams")`.
- Produces: `StreamCapabilityChecker interface { Has(ctx, projectID, name) bool }`; `(*StreamService).SetCapabilities(StreamCapabilityChecker)`; `ErrStreamsUnsupported` error.

- [ ] **Step 1: Write the failing test**

```go
package services

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/fastgateway-dev/backend-v2/internal/models"
)

type fakeStreamCaps struct{ streams bool }

func (f fakeStreamCaps) Has(context.Context, uuid.UUID, string) bool { return f.streams }

func TestStreamCreate_RejectedWhenStreamsUnsupported(t *testing.T) {
	s := &StreamService{} // zero-value is enough to reach the guard
	s.SetCapabilities(fakeStreamCaps{streams: false})
	_, err := s.Create(uuid.New(), CreateStreamInput{}, &models.User{})
	if !errors.Is(err, ErrStreamsUnsupported) {
		t.Fatalf("expected ErrStreamsUnsupported, got %v", err)
	}
}
```

(If `StreamService`'s zero value cannot safely reach the guard because `Create` dereferences other unset deps first, place the guard as the **first** statement in `Create`, before any other field access, so this test exercises only the guard.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/services/ -run 'TestStreamCreate_RejectedWhenStreamsUnsupported' -v`
Expected: FAIL — `undefined: ErrStreamsUnsupported` / `s.SetCapabilities undefined`.

- [ ] **Step 3: Write minimal implementation**

In `internal/services/stream_service.go`:

```go
// ErrStreamsUnsupported is returned when stream creation is attempted on a
// cluster whose Envoy Gateway version is positively detected below 1.8.
var ErrStreamsUnsupported = errors.New("streams require Envoy Gateway >= 1.8")

// StreamCapabilityChecker reports whether the project's cluster supports a
// capability. *CapabilityService satisfies it.
type StreamCapabilityChecker interface {
	Has(ctx context.Context, projectID uuid.UUID, name string) bool
}

// SetCapabilities wires the capability checker post-construction (mirrors
// SetPortSources). Optional: when nil, the stream guard is skipped.
func (s *StreamService) SetCapabilities(c StreamCapabilityChecker) { s.capabilities = c }
```

Add a `capabilities StreamCapabilityChecker` field to the `StreamService` struct (stream_service.go:136), and as the **first** statement of `Create` (stream_service.go:184):

```go
func (s *StreamService) Create(projectID uuid.UUID, in CreateStreamInput, user *models.User) (*models.Stream, error) {
	if s.capabilities != nil && !s.capabilities.Has(context.Background(), projectID, "streams") {
		return nil, ErrStreamsUnsupported
	}
	// ... existing body ...
```

(Add `"context"` and `"errors"` to the imports if not present. `context.Background()` is used because `Create` has no `ctx` parameter and the capability read is a fast cached lookup.)

In `cmd/server/main.go`, after building the `StreamService` and the `capabilityService`, call `streamService.SetCapabilities(capabilityService)`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/services/ -run 'TestStreamCreate_RejectedWhenStreamsUnsupported' -v`
Expected: PASS. Then `go test ./internal/services/ -count=1` to confirm existing StreamService tests (which don't set a checker → guard skipped) still pass.

- [ ] **Step 5: Commit**

```bash
git add internal/services/stream_service.go internal/services/stream_service_capability_test.go cmd/server/main.go
git commit -m "feat(streams): reject stream create on Envoy Gateway < 1.8

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 7: Expose `streamAvailable` on /capabilities

**Files:**
- Modify: `internal/handlers/project_handler.go:14-26` (add a capability evaluator dep), `:294-311` (`GetCapabilities` merges exposed capabilities)
- Modify: `cmd/server/main.go:292` (`NewProjectHandler` call gains the capability service)
- Test: `internal/handlers/project_handler_capabilities_test.go`

**Interfaces:**
- Consumes: `CapabilityService.Evaluate(ctx, projectID) map[string]bool`; the existing `services.RateLimitProbe`.
- Produces: `CapabilityEvaluator interface { Evaluate(ctx, projectID) map[string]bool }` field on `ProjectHandler`; `GetCapabilities` returns `{rateLimitAvailable, streamAvailable, ...}`.

- [ ] **Step 1: Write the failing test**

```go
package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type stubRateLimit struct{ ok bool }

func (s stubRateLimit) IsRateLimitAvailable(context.Context, uuid.UUID) (bool, error) {
	return s.ok, nil
}

type stubCaps struct{ m map[string]bool }

func (s stubCaps) Evaluate(context.Context, uuid.UUID) map[string]bool { return s.m }

func TestGetCapabilities_IncludesStreamAvailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewProjectHandler(nil, nil, stubRateLimit{ok: true}, stubCaps{m: map[string]bool{"streams": false}})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "projectId", Value: uuid.New().String()}}
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	h.GetCapabilities(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body map[string]bool
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["rateLimitAvailable"] != true {
		t.Errorf("rateLimitAvailable = %v, want true", body["rateLimitAvailable"])
	}
	if _, ok := body["streamAvailable"]; !ok {
		t.Error("response must include streamAvailable")
	}
	if body["streamAvailable"] != false {
		t.Errorf("streamAvailable = %v, want false", body["streamAvailable"])
	}
}
```

Note: the handler must map the registry name `streams` to the DTO field `streamAvailable`. Keep the mapping explicit in the handler (see Step 3) so the exposed registry name and the wire field are decoupled.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/handlers/ -run 'TestGetCapabilities_IncludesStreamAvailable' -v`
Expected: FAIL — too few arguments to `NewProjectHandler` / field missing.

- [ ] **Step 3: Write minimal implementation**

In `internal/handlers/project_handler.go`:

```go
// CapabilityEvaluator returns the exposed version-derived capabilities for a
// project. *services.CapabilityService satisfies it.
type CapabilityEvaluator interface {
	Evaluate(ctx context.Context, projectID uuid.UUID) map[string]bool
}

type ProjectHandler struct {
	projectService ProjectServiceInterface
	auditService   AuditServiceInterface
	k8sService     services.RateLimitProbe
	capabilities   CapabilityEvaluator
}

func NewProjectHandler(projectService ProjectServiceInterface, auditService AuditServiceInterface, k8sService services.RateLimitProbe, capabilities CapabilityEvaluator) *ProjectHandler {
	return &ProjectHandler{
		projectService: projectService,
		auditService:   auditService,
		k8sService:     k8sService,
		capabilities:   capabilities,
	}
}
```

Rewrite `GetCapabilities` to merge, mapping the registry's `streams` to the wire field `streamAvailable`:

```go
func (h *ProjectHandler) GetCapabilities(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid project ID"})
		return
	}

	rateLimitAvailable, err := h.k8sService.IsRateLimitAvailable(c.Request.Context(), projectID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	caps := h.capabilities.Evaluate(c.Request.Context(), projectID)
	c.JSON(http.StatusOK, gin.H{
		"rateLimitAvailable": rateLimitAvailable,
		"streamAvailable":    caps["streams"],
	})
}
```

Add `"context"` to imports if needed. In `cmd/server/main.go:292`, pass `capabilityService` as the new fourth argument to `NewProjectHandler`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/handlers/ -run 'TestGetCapabilities_IncludesStreamAvailable' -v`
Expected: PASS.

- [ ] **Step 5: Commit + full suite**

```bash
go test ./... -count=1
make mocks-check
git add internal/handlers/project_handler.go cmd/server/main.go internal/handlers/project_handler_capabilities_test.go
git commit -m "feat(api): expose streamAvailable on /projects/{id}/capabilities

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

Expected: `go test ./...` all green; `make mocks-check` clean.
