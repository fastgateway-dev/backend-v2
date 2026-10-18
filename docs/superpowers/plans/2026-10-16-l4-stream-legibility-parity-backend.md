# L4 Stream Legibility and L4 Route Parity (Backend) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make a Stream legible (an entry point endpoint for streams and templates, an enriched stream list, stream manifests) and bring L4 routes to parity with HTTP routes (L4 route manifests with the BackendTrafficPolicy, L4 manifest previews, version history under the stream, and a working L4 approval diff), all as read-only, additive API.

**Architecture:** One new read-only service, `StreamViewService` (`internal/services/stream_view.go`), builds the entry point for both endpoints and the enriched list. It reads owner-aware copies of the port queries `CheckPortCollision` uses, plus a new read-only Kubernetes role (`GatewayStatusReader`) for the Gateway address. The L4 manifest, preview and diff paths render through the existing deploy builders (`streamplan.BuildStreamGatewayConfig` → `kubernetes.BuildGatewayObject`, `routeplan.Build{TCP,UDP}RouteConfig` → `kubernetes.Build{TCP,UDP}RouteObject`, `routeplan.BuildBackendTrafficPolicyConfigForNamespace` → `kubernetes.BuildBackendTrafficPolicy`). Previews share create's and update's validation, which is extracted into `validateCreate` / `validateUpdate` (a pure extract-function refactor). Nothing that applies objects to a cluster changes.

**Tech Stack:** Go 1.26, gin, gorm (Postgres), `k8s.io/client-go` dynamic client (+ `dynamic/fake`), testify, mockery v3 (`make mocks`), Redocly bundle (`make openapi`).

**Spec:** `docs/superpowers/specs/2026-10-16-l4-stream-legibility-parity-design.md` (Backend sections B1–B8, the backend Testing list, the backend e2e item, Global Constraints, Blast radius). The frontend sections F1–F7 are a separate plan; the JSON field names in **Contract** below are what that plan is written against.

## Global Constraints

From the spec, verbatim:

- No change to any object FastGateway applies to a cluster.
- No schema migration.
- Additive API only; existing endpoints and fields keep their shape.
- Every new endpoint has an OpenAPI entry; `make openapi-check` and `make mocks-check` pass.
- The entry point port map and create-time 400/409 agree.
- Reserved ports 19000, 19001 and 60000 are always shown as reserved.

Repository rules every task also follows:

- **Byte-identical invariant:** no task changes a builder or output used by an apply path (`deployL4`, `UpdateGateway`, `applyBackendTrafficPolicy`, the domain deploy). New code only *calls* those builders. `TestStreamYAMLs_EqualWhatDeployL4Applies` (Task 7) pins the view against what deploy hands the cluster client.
- **Fail closed on reads:** a read endpoint returns an error (404 or 500), never a guessed mode, a partial manifest set, or a policy that was silently dropped. The only best-effort data is approval name enrichment (`streamName`), which matches the existing `domainName` contract (see Ruling R10).
- **No silent nil-skips:** no `if s.<dep> == nil` / `if s.<dep> != nil` on a wired dependency (`TestNoWiringNilGuardsRemain*`). New services take every dependency through a `...Deps` struct, and the constructor panics on a missing one. **Gotcha:** that test's regex is `s\.([a-zA-Z0-9]+) != nil`, so it also matches a *local variable whose name ends in `s`* (`previous.RouteConfig != nil` is flagged). Name locals `previousSnapshot` and similar, as the code below does.
- **Mocks are generated:** `make mocks` after any change to an interface in `internal/repository`, `internal/services` or `internal/handlers` (mockery mocks every interface in those packages, unexported ones included), or to `internal/mocks/roles/roles.go`. Commit `internal/mocks/*`. `make mocks-check` uses `git status --porcelain`, so it reports drift until the regenerated mocks are committed. Run it after the commit.
- **OpenAPI per task:** `TestRouteSpecParity` (`cmd/server/router_test.go`) fails the moment a route is registered without a spec operation, so every task that registers a route adds its spec entry and runs `make openapi` in the same task (Ruling R4).
- **Postgres-gated tests:** repository tests and some service tests call `requirePostgres` / `requireTopologyDB` and **skip** unless `INTEGRATION_DB_URL` is set. A skip is not a pass. Start a throwaway database once:
  ```bash
  docker run -d --name fgw-l4-pg -e POSTGRES_USER=fastgateway -e POSTGRES_PASSWORD=fastgateway -e POSTGRES_DB=fastgateway -p 55432:5432 postgres:16-alpine
  sleep 5 && DATABASE_PORT=55432 go run cmd/migrate/main.go up
  export INTEGRATION_DB_URL="postgres://fastgateway:fastgateway@localhost:55432/fastgateway?sslmode=disable"
  ```
  At `26484c0` the DB-gated suite already fails `TestCheckPortCollision_*` (services) and `TestDomainDNSRecordRepository_CountByZone`, `TestDomainRepository_ListByManagedCertificateID(s)` (repository), with or without this plan. Run the new DB tests **by name**, as the steps below do. Remove the container at the end (`docker rm -f fgw-l4-pg`).
- **Low disk:** if `go test ./...` fails with `no space left on device` while linking, run it with `-p 1`.
- **Before starting:** pull `main` to the latest (`git checkout main && git pull --ff-only origin main`) and branch: `git checkout -b feat/l4-stream-legibility-parity`.
- Run every command from the backend-v2 repo root.
- Commit messages are conventional commits and end with the line `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

These are the inputs most likely to break for a user that the spec implies but its test list does not name. Each one has a test in the task that owns the code:

- **A domain bound to a listener the template no longer declares** (a stale `boundListeners` entry): the port map must ignore it exactly as `UsedPortsByTemplate` does, not show a phantom port. Test: Task 1 `TestDomainRepository_PortOwnersByTemplate_MatchesUsedPortsByTemplate` (binds `"gone"`).
- **Two hostname listeners on the same port** (e.g. HTTP:19443 and TLS:19443 on a merged template): `reserved` lists that TCP port once, not twice. Test: Task 3 `TestStreamEntrypoint_ReservedTemplateListenerPortListedOnce`.
- **A stream whose Gateway was never deployed** (create failed, status `error`): the entry point is a 200 with `address: ""`, not a 500. Test: Task 3 `TestStreamEntrypoint_Dedicated_GatewayNotDeployedYet`.
- **Previewing a route whose name already exists on the stream:** the preview fails with create's 400 (`route name already exists in this stream`) instead of rendering a manifest. Test: Task 8 `TestPreviewCreateForStream_SameErrorsAsCreateForStream` case "name already taken on the stream".
- **An L4 approval whose snapshot JSON is corrupt:** the diff is an error, never a half-filled diff. Test: Task 10 `TestApprovalService_GetDiff_L4CorruptSnapshotIsAnError`.

## Plan rulings

Where the spec and the code disagree, the spec wins on behavior and the code wins on names. Each deviation:

- **R1 — Address source.** The spec says "this stream's `loadBalancerAddress`", but no backend model or field has an address. `loadBalancerAddress` exists only as an optional frontend type field that the backend never sets. The entry point therefore reads the stream Gateway's `status.addresses` live, through a new read-only role `GatewayStatusReader.GetGateway` (Task 2). A Gateway that is NotFound or has no address gives `""`. Any other read error is a 500, which fails closed.
- **R2 — Reserved transports.** `ValidateListenerPort` rejects 19000/19001/60000 for both TCP and UDP. The agreement invariant covers both transports. So `reserved` lists `envoy_internal` and `placeholder` once per transport (six entries), and `template_listener` as TCP only, because `CheckPortCollision` reserves those ports for TCP only. The spec's example shows only TCP rows.
- **R3 — Template-variant errors.** A missing template gives 404 (`ErrStreamTemplateNotFound`). Another project's template gives 404 (`ErrStreamTemplateWrongProject`). Other failures give 500. For the stream variant, any template lookup failure gives 500 ("never fall back to a guessed `dedicated`").
- **R4 — OpenAPI and mocks per task, not batched.** The task shape put all gates in the last task. They are folded into each task instead, because `TestRouteSpecParity` and the mock-dependent tests fail on the first new route or interface. Task 11 re-runs the full gate and adds the e2e test.
- **R5 — Update diff "current" side.** The spec table says update-current comes "from the live route + stored policy". That would show the *proposed* values: `persistUpdatePolicies` overwrites the stored BackendTrafficPolicy when the update is **submitted** (`route_write_approval.go`), and `OnApproved` copies the proposed config onto the route row (`route_approval.go:applyRouteApprovalSnapshot`). Current therefore comes from the approval's `PreviousConfig` snapshot (route config + BTP), which is also what the HTTP diff branch uses. Delete-current is the live route plus the delete approval's `ConfigSnapshot` BTP, which `submitDeleteApproval` fills with the stored policy at submit time.
- **R6 — Shared validation by extraction.** "Same validation as `CreateForStream`" is guaranteed by construction. The validation prefix of `routeWrite.create` and of `routeWrite.Update` is extracted, unchanged, into `validateCreate` / `validateUpdate`, and the previews call the same functions. The update preview skips only Update's "there is already a pending approval" check, because that is not an input check.
- **R7 — Version wrappers.** `requireStreamRoute` lives on `RouteHandler` (it needs `routeService.GetForStream`), and `RouteVersionHandler` has no route service. So the `*ForStream` wrapper is one generic `RouteHandler.ForStreamRoute(next gin.HandlerFunc) gin.HandlerFunc` wrapped around each existing `RouteVersionHandler` method. `NewRouteVersionHandler` is unchanged.
- **R8 — List enrichment home.** `GET /streams` is served by `StreamViewService.ListWithSummary`. `StreamService.List` is untouched. Each list item embeds `models.Stream`, so every existing field stays at the top level. The grouped route query is a third owner query, `StreamRepository.L4PortOwnersByStreamIDs`. The spec names only the per-stream and per-template ones.
- **R9 — L4 delete preview includes the policy.** `PreviewDelete`'s L4 branch now also fills `currentBackendTrafficPolicyYaml`, because the delete deploy removes the policy (`deployL4` → `removeBackendTrafficPolicy`).
- **R10 — Enrichment stays best-effort.** `streamId`/`streamName` follow the existing `domainName` contract (a failed lookup leaves the name empty). `GET /approvals/:id` (`ApprovalService.GetByID`) previously enriched nothing. It now sets `streamId`/`streamName` for L4 route approvals. The diff itself fails closed.
- **R11 — `ErrDiffNotAvailableForL4` is removed** (as the spec says). A non-L4 route without a `DomainID` now returns the error `route <id> has no domain`.
- **R12 — Known limitation, not changed.** For a live route with a *pending* update, the manifests endpoints render the stored policy, which is already the proposed one (see R5). HTTP route manifests behave the same way. B3/B4 mandate the stored policy, and changing that would need its own design.

## Contract (JSON field names the frontend plan uses)

| Endpoint | Body |
|---|---|
| `GET …/streams/:streamId/entrypoint`, `GET …/domain-templates/:domainTemplateId/entrypoint` | `{mode: "dedicated"\|"shared", templateId, templateName, gatewayClass, address, portRange: {min,max}\|null, sharedWith: {streams: [{id,name}], domains: [{id,name,hostname}]}, ports: [{transport: "TCP"\|"UDP", port, owner: {kind: "stream_route", streamId, streamName, routeId, routeName, status} \| {kind: "domain", domainId, domainName}}], reserved: [{transport, port, reason: "envoy_internal"\|"placeholder"\|"template_listener"}]}`. All arrays are `[]`, never `null`. Owner fields of the other kind are omitted. |
| `GET …/streams` | `{data: [ {…every Stream field…, mode, gatewayTemplateName, routeCount, ports: [{transport, port}]} ]}` |
| `GET …/streams/:streamId/yamls` | `{gatewayYaml, routes: [{routeId, name, protocol: "tcp"\|"udp", routeYaml, backendTrafficPolicyYaml}]}`. `backendTrafficPolicyYaml` is `""` when the route has no policy. |
| `GET …/streams/:streamId/routes/:routeId/yamls` | `RouteYAMLs`: the TCPRoute/UDPRoute stays in `httpRouteYaml`, plus `backendTrafficPolicyYaml` (omitted when none). |
| `POST …/streams/:streamId/routes/preview` | `PreviewCreateResult`: `proposedYaml`, `proposedBackendTrafficPolicyYaml` (omitted when none). |
| `POST …/streams/:streamId/routes/:routeId/preview` | `PreviewUpdateResult`: `currentYaml`, `proposedYaml`, `currentBackendTrafficPolicyYaml`, `proposedBackendTrafficPolicyYaml` (the policy fields are omitted when empty). |
| `GET …/streams/:streamId/routes/:routeId/preview-delete` | `PreviewDeleteResult`: `currentYaml`, `currentBackendTrafficPolicyYaml`. |
| `GET/POST …/streams/:streamId/routes/:routeId/versions[/:version[/rollback]]` | unchanged `RouteVersionHandler` bodies (`{data,total,page,limit}`, `RouteVersion`, `Route`). |
| `GET …/approvals`, `GET …/approvals/:approvalId` | the approval gains `streamId`, `streamName` (both omitted unless the route is L4). |
| `GET …/approvals/:approvalId/diff` | gains `routeKind: "HTTPRoute"\|"GRPCRoute"\|"TCPRoute"\|"UDPRoute"` (always present). L4 diffs fill `currentYaml`/`proposedYaml` and `current…`/`proposedBackendTrafficPolicyYaml`. |

## File map

| File | Responsibility | Task |
|---|---|---|
| `internal/repository/stream_repository.go` | owner-aware L4 port queries, `ListByGatewayTemplateID`, `GetByIDs` | 1, 10 |
| `internal/repository/domain_repository.go` | `PortOwnersByTemplate` | 1 |
| `internal/cluster/gateway.go`, `internal/services/k8s_roles.go`, `internal/mocks/roles/roles.go` | read-only `GatewayStatusReader` role | 2 |
| `internal/services/stream_view.go` (new) | entry point builder + enriched list | 3, 5 |
| `internal/handlers/stream_handler.go` | entry point endpoints, list | 4, 5 |
| `internal/services/route_yaml.go`, `internal/services/route_yaml_l4.go` (new) | L4 render helpers, L4 BTP YAML, L4 preview update/delete | 6, 8 |
| `internal/services/stream_yaml.go` (new) | stream manifests | 7 |
| `internal/services/route_write_service.go`, `internal/services/route_preview_l4.go` (new) | `validateCreate`/`validateUpdate`, L4 previews | 8 |
| `internal/handlers/route_stream_handler.go` | stream-scoped yamls, previews, `ForStreamRoute` | 6–9 |
| `internal/services/approval_service.go`, `internal/models/approval.go` | L4 diff, `routeKind`, stream enrichment | 10 |
| `cmd/server/main.go` | wiring + route registration | 4–10 |
| `docs/openapi/**`, `cmd/server/openapi.yaml` (generated) | spec entries | 4–10 |
| `e2e/harness/stream_legibility.go` (new), `e2e/suites/stream/legibility_test.go` (new) | e2e | 11 |

---

### Task 1: Owner-aware port queries (repository)

**Files:**
- Modify: `internal/repository/stream_repository.go` (append after `UsedL4Ports`)
- Modify: `internal/repository/domain_repository.go` (insert before `// CountByProjectID returns the number of domains in a project`)
- Test: `internal/repository/port_owner_repository_test.go` (new)

**Interfaces:**
- Consumes: the existing `PortUse`, `UsedPortsByStream`, `UsedL4Ports`, `UsedPortsByTemplate`; the test helpers `requirePostgres`, `seedProject`, `insertTemplate`, `insertL4Route` (`route_repository_test.go`, `stream_repository_test.go`).
- Produces: `repository.L4PortOwner{Transport string; Port int; StreamID uuid.UUID; StreamName string; RouteID uuid.UUID; RouteName string; Status string}`; `(*StreamRepository).L4PortOwnersByStream(streamID uuid.UUID) ([]L4PortOwner, error)`; `(*StreamRepository).L4PortOwnersByTemplate(templateID uuid.UUID) ([]L4PortOwner, error)`; `(*StreamRepository).L4PortOwnersByStreamIDs(streamIDs []uuid.UUID) ([]L4PortOwner, error)` (empty input → `[]L4PortOwner{}`, no query); `(*StreamRepository).ListByGatewayTemplateID(templateID uuid.UUID) ([]models.Stream, error)` (ordered by name); `repository.DomainPortOwner{Transport string; Port int; DomainID uuid.UUID; DomainName string}`; `(*DomainRepository).PortOwnersByTemplate(templateID uuid.UUID) ([]DomainPortOwner, error)`. Rows are ordered by port, then protocol, then route ID (L4) or by domain name (domains).

The `WHERE` clauses are the existing ones, copied unchanged. The existing queries stay untouched for `CheckPortCollision`.

- [ ] **Step 1: Write the failing test**

Create `internal/repository/port_owner_repository_test.go`:

```go
package repository_test

import (
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// portUsesOf projects owner rows onto the (transport, port) identities the
// collision queries return, so the two can be compared directly.
func portUsesOf(owners []repository.L4PortOwner) []repository.PortUse {
	out := make([]repository.PortUse, 0, len(owners))
	for _, o := range owners {
		out = append(out, repository.PortUse{Transport: o.Transport, Port: o.Port})
	}
	return out
}

func TestStreamRepository_L4PortOwners_MatchUsedPortQueries(t *testing.T) {
	db := requirePostgres(t)
	projectID, _, teamID, userID := seedProject(t, db)
	tmplID := insertTemplate(t, db, projectID, userID, true)
	otherTmplID := insertTemplate(t, db, projectID, userID, true)
	repo := repository.NewStreamRepository(db)
	mk := func(name string, tmpl uuid.UUID) *models.Stream {
		s := &models.Stream{ProjectID: projectID, Name: name, Namespace: "fastgateway-system", GatewayTemplateID: tmpl}
		require.NoError(t, repo.Create(s))
		return s
	}
	a, b, c := mk("owner-a", tmplID), mk("owner-b", tmplID), mk("owner-c", otherTmplID)

	pg := insertL4Route(t, db, a.ID, teamID, userID, "tcp", 5432, "active")
	dns := insertL4Route(t, db, a.ID, teamID, userID, "udp", 53, "pending_create")
	insertL4Route(t, db, a.ID, teamID, userID, "tcp", 9999, "rejected")
	redis := insertL4Route(t, db, b.ID, teamID, userID, "tcp", 6379, "pending_update")
	insertL4Route(t, db, c.ID, teamID, userID, "tcp", 7000, "active")

	// By stream: same rows as UsedPortsByStream, with owners filled in.
	byStream, err := repo.L4PortOwnersByStream(a.ID)
	require.NoError(t, err)
	used, err := repo.UsedPortsByStream(a.ID, nil)
	require.NoError(t, err)
	assert.ElementsMatch(t, used, portUsesOf(byStream), "owner query must match UsedPortsByStream")
	assert.Equal(t, []repository.L4PortOwner{
		{Transport: "UDP", Port: 53, StreamID: a.ID, StreamName: "owner-a", RouteID: dns, RouteName: "r-" + dns.String(), Status: "pending_create"},
		{Transport: "TCP", Port: 5432, StreamID: a.ID, StreamName: "owner-a", RouteID: pg, RouteName: "r-" + pg.String(), Status: "active"},
	}, byStream, "rejected routes hold no port; rows are ordered by port")

	// By template: same rows as UsedL4Ports.
	byTmpl, err := repo.L4PortOwnersByTemplate(tmplID)
	require.NoError(t, err)
	usedTmpl, err := repo.UsedL4Ports(tmplID, nil)
	require.NoError(t, err)
	assert.ElementsMatch(t, usedTmpl, portUsesOf(byTmpl), "owner query must match UsedL4Ports")
	require.Len(t, byTmpl, 3)
	assert.Equal(t, redis, byTmpl[2].RouteID)
	assert.Equal(t, "owner-b", byTmpl[2].StreamName)

	// By stream IDs: one query for many streams.
	byIDs, err := repo.L4PortOwnersByStreamIDs([]uuid.UUID{a.ID, c.ID})
	require.NoError(t, err)
	assert.ElementsMatch(t, []repository.PortUse{{Transport: "UDP", Port: 53}, {Transport: "TCP", Port: 5432}, {Transport: "TCP", Port: 7000}}, portUsesOf(byIDs))

	empty, err := repo.L4PortOwnersByStreamIDs(nil)
	require.NoError(t, err)
	assert.Empty(t, empty)
	assert.NotNil(t, empty)
}

func TestStreamRepository_ListByGatewayTemplateID(t *testing.T) {
	db := requirePostgres(t)
	projectID, _, _, userID := seedProject(t, db)
	tmplID := insertTemplate(t, db, projectID, userID, true)
	otherTmplID := insertTemplate(t, db, projectID, userID, true)
	repo := repository.NewStreamRepository(db)
	for _, s := range []*models.Stream{
		{ProjectID: projectID, Name: "zeta", Namespace: "fastgateway-system", GatewayTemplateID: tmplID},
		{ProjectID: projectID, Name: "alpha", Namespace: "fastgateway-system", GatewayTemplateID: tmplID},
		{ProjectID: projectID, Name: "other", Namespace: "fastgateway-system", GatewayTemplateID: otherTmplID},
	} {
		require.NoError(t, repo.Create(s))
	}

	got, err := repo.ListByGatewayTemplateID(tmplID)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "alpha", got[0].Name)
	assert.Equal(t, "zeta", got[1].Name)
}

func TestDomainRepository_PortOwnersByTemplate_MatchesUsedPortsByTemplate(t *testing.T) {
	db := requirePostgres(t)
	projectID, domainID, _, userID := seedProject(t, db)
	tmplID := insertTemplate(t, db, projectID, userID, false)
	require.NoError(t, db.Exec(`UPDATE domain_templates SET listeners = ? WHERE id = ?`,
		`[{"name":"http","protocol":"HTTP","port":8080},{"name":"https","protocol":"HTTPS","port":8443,"tlsMode":"Terminate"},{"name":"unbound","protocol":"HTTP","port":9090}]`, tmplID).Error)
	// "gone" is a stale binding to a listener the template no longer
	// declares: both queries must ignore it.
	require.NoError(t, db.Exec(`UPDATE domains SET domain_template_id = ?, bound_listeners = ? WHERE id = ?`,
		tmplID, `["http","https","gone"]`, domainID).Error)
	var name string
	require.NoError(t, db.Raw(`SELECT name FROM domains WHERE id = ?`, domainID).Scan(&name).Error)

	repo := repository.NewDomainRepository(db)
	owners, err := repo.PortOwnersByTemplate(tmplID)
	require.NoError(t, err)
	used, err := repo.UsedPortsByTemplate(tmplID)
	require.NoError(t, err)

	got := make([]repository.PortUse, 0, len(owners))
	for _, o := range owners {
		got = append(got, repository.PortUse{Transport: o.Transport, Port: o.Port})
		assert.Equal(t, domainID, o.DomainID)
		assert.Equal(t, name, o.DomainName)
	}
	assert.ElementsMatch(t, used, got, "owner query must match UsedPortsByTemplate")
	assert.ElementsMatch(t, []repository.PortUse{{Transport: "TCP", Port: 8080}, {Transport: "TCP", Port: 8443}}, got,
		"a stale binding holds no port")

	missing, err := repo.PortOwnersByTemplate(uuid.New())
	require.NoError(t, err)
	assert.Empty(t, missing)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run (with `INTEGRATION_DB_URL` exported, see Global Constraints): `go test ./internal/repository/ -run 'TestStreamRepository_L4PortOwners|TestStreamRepository_ListByGatewayTemplateID|TestDomainRepository_PortOwnersByTemplate' -count=1 -v`
Expected: FAIL to build: `repo.L4PortOwnersByStream undefined (type *repository.StreamRepository has no field or method L4PortOwnersByStream)` (and the same for `ListByGatewayTemplateID`, `PortOwnersByTemplate`).

- [ ] **Step 3: Write minimal implementation**

Append to `internal/repository/stream_repository.go` (the file already imports `strings`, `models`, `uuid`, `gorm`):

```go
// ListByGatewayTemplateID lists every stream created from the given gateway
// template, ordered by name. The entry-point view uses it for the streams
// that share a merged template's Gateway.
func (r *StreamRepository) ListByGatewayTemplateID(templateID uuid.UUID) ([]models.Stream, error) {
	var streams []models.Stream
	err := r.db.Where("gateway_template_id = ?", templateID).Order("name ASC").Find(&streams).Error
	return streams, err
}

// L4PortOwner is one listener an L4 route holds, together with the route and
// the stream that own it. Transport is "TCP" or "UDP".
type L4PortOwner struct {
	Transport  string
	Port       int
	StreamID   uuid.UUID
	StreamName string
	RouteID    uuid.UUID
	RouteName  string
	Status     string
}

// l4PortOwnerRow is the scan target for the owner-aware L4 port queries.
type l4PortOwnerRow struct {
	Protocol   string
	Port       int
	StreamID   uuid.UUID
	StreamName string
	RouteID    uuid.UUID
	RouteName  string
	Status     string
}

// l4PortOwners runs one owner-aware L4 port query. where must carry the
// rejected/listenerPort conditions of UsedPortsByStream / UsedL4Ports so the
// owner view and the collision check can never disagree.
func (r *StreamRepository) l4PortOwners(where string, args ...interface{}) ([]L4PortOwner, error) {
	var rows []l4PortOwnerRow
	err := r.db.Table("routes").
		Select("routes.protocol AS protocol, (routes.config->>'listenerPort')::int AS port, "+
			"streams.id AS stream_id, streams.name AS stream_name, "+
			"routes.id AS route_id, routes.name AS route_name, routes.status AS status").
		Joins("JOIN streams ON streams.id = routes.stream_id").
		Where(where, args...).
		Order("port ASC, routes.protocol ASC, routes.id ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	owners := make([]L4PortOwner, 0, len(rows))
	for _, row := range rows {
		owners = append(owners, L4PortOwner{
			Transport:  strings.ToUpper(row.Protocol),
			Port:       row.Port,
			StreamID:   row.StreamID,
			StreamName: row.StreamName,
			RouteID:    row.RouteID,
			RouteName:  row.RouteName,
			Status:     row.Status,
		})
	}
	return owners, nil
}

// L4PortOwnersByStream is UsedPortsByStream with owners: the listener of
// every non-rejected L4 route on the stream. Same WHERE clause.
func (r *StreamRepository) L4PortOwnersByStream(streamID uuid.UUID) ([]L4PortOwner, error) {
	return r.l4PortOwners("routes.stream_id = ? AND routes.protocol IN ? AND routes.status <> ? AND routes.config->>'listenerPort' IS NOT NULL",
		streamID, []string{string(models.RouteProtocolTCP), string(models.RouteProtocolUDP)}, models.RouteStatusRejected)
}

// L4PortOwnersByTemplate is UsedL4Ports with owners: the listener of every
// non-rejected L4 route on every stream of the template. Same WHERE clause.
func (r *StreamRepository) L4PortOwnersByTemplate(templateID uuid.UUID) ([]L4PortOwner, error) {
	return r.l4PortOwners("streams.gateway_template_id = ? AND routes.protocol IN ? AND routes.status <> ? AND routes.config->>'listenerPort' IS NOT NULL",
		templateID, []string{string(models.RouteProtocolTCP), string(models.RouteProtocolUDP)}, models.RouteStatusRejected)
}

// L4PortOwnersByStreamIDs is L4PortOwnersByStream for many streams in one
// query (the stream list uses it, so listing never issues a query per
// stream). An empty input returns an empty result without querying.
func (r *StreamRepository) L4PortOwnersByStreamIDs(streamIDs []uuid.UUID) ([]L4PortOwner, error) {
	if len(streamIDs) == 0 {
		return []L4PortOwner{}, nil
	}
	return r.l4PortOwners("routes.stream_id IN ? AND routes.protocol IN ? AND routes.status <> ? AND routes.config->>'listenerPort' IS NOT NULL",
		streamIDs, []string{string(models.RouteProtocolTCP), string(models.RouteProtocolUDP)}, models.RouteStatusRejected)
}
```

Insert into `internal/repository/domain_repository.go` immediately before `// CountByProjectID returns the number of domains in a project` (the file already imports `errors` and `gorm`):

```go
// DomainPortOwner is one hostname-routed listener port a domain binds on its
// gateway template, with the domain that binds it. Transport is always "TCP"
// (HTTP/HTTPS/TLS listeners run over TCP).
type DomainPortOwner struct {
	Transport  string
	Port       int
	DomainID   uuid.UUID
	DomainName string
}

// PortOwnersByTemplate is UsedPortsByTemplate with owners: the port of each
// template hostname-routed listener every domain on the template binds,
// ordered by domain name. It resolves listeners exactly as
// UsedPortsByTemplate does, so the two can never disagree.
func (r *DomainRepository) PortOwnersByTemplate(templateID uuid.UUID) ([]DomainPortOwner, error) {
	var tmpl models.DomainTemplate
	if err := r.db.Select("id", "listeners").First(&tmpl, "id = ?", templateID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return []DomainPortOwner{}, nil
		}
		return nil, err
	}
	portByName := make(map[string]int)
	for _, l := range tmpl.Listeners.HostnameRouted() {
		portByName[l.Name] = l.Port
	}

	var domains []models.Domain
	if err := r.db.Select("id", "name", "bound_listeners").
		Where("domain_template_id = ?", templateID).Order("name ASC").Find(&domains).Error; err != nil {
		return nil, err
	}
	owners := make([]DomainPortOwner, 0, len(domains)*2)
	for _, d := range domains {
		for _, name := range d.BoundListeners {
			if port, ok := portByName[name]; ok {
				owners = append(owners, DomainPortOwner{Transport: "TCP", Port: port, DomainID: d.ID, DomainName: d.Name})
			}
		}
	}
	return owners, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/repository/ -run 'TestStreamRepository|TestDomainRepository_PortOwnersByTemplate|TestDomainRepository_UsedPortsByTemplate' -count=1 -v`
Expected: PASS for the three new tests and the existing `TestStreamRepository_CRUD`, `TestStreamRepository_UsedPortsByStream`, `TestStreamRepository_UsedL4Ports_AcrossStreamsOfTemplate`, `TestDomainRepository_UsedPortsByTemplate`. (With `INTEGRATION_DB_URL` unset they report `SKIP`, which does not count.) Then `go build ./... && gofmt -l internal/` prints nothing.

- [ ] **Step 5: Commit**

```bash
git add internal/repository/stream_repository.go internal/repository/domain_repository.go internal/repository/port_owner_repository_test.go
git commit -m "feat(repository): owner-aware L4 and domain port queries

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Read-only Gateway status role

**Files:**
- Modify: `internal/cluster/gateway.go` (import + `GetGateway` before `DeleteGateway`)
- Modify: `internal/services/k8s_roles.go` (new role before `GatewayClassApplier`; compile-time assertion)
- Modify: `internal/mocks/roles/roles.go` (add the role to the union)
- Regenerate: `internal/mocks/mock_kubernetes.go`, `internal/mocks/mock_services.go` (`make mocks`)
- Test: `internal/cluster/gateway_read_test.go` (new)

**Interfaces:**
- Consumes: `kubernetes.GatewayGVR`, `(*Client).getClient`, `NewWithClient` (`internal/cluster/versions.go`).
- Produces: `(*cluster.Client).GetGateway(ctx context.Context, projectID uuid.UUID, namespace, name string) (*unstructured.Unstructured, error)`, which returns the API error unwrapped so `k8serrors.IsNotFound` works; `services.GatewayStatusReader` interface with that one method; `mocks.MockGatewayStatusReader` (generated). `MockKubernetesService` also gains `GetGateway`.

- [ ] **Step 1: Write the failing test**

Create `internal/cluster/gateway_read_test.go`:

```go
package cluster

import (
	"context"
	"testing"

	"github.com/google/uuid"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
)

func TestGetGateway_ReadsStatusAndReportsNotFound(t *testing.T) {
	gw := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "Gateway",
		"metadata":   map[string]interface{}{"name": "str-db", "namespace": "ns"},
		"status": map[string]interface{}{
			"addresses": []interface{}{map[string]interface{}{"type": "IPAddress", "value": "10.0.0.5"}},
		},
	}}
	fake := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{kubernetes.GatewayGVR: "GatewayList"})
	if _, err := fake.Resource(kubernetes.GatewayGVR).Namespace("ns").Create(context.Background(), gw, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed gateway: %v", err)
	}
	c := NewWithClient(fake)

	got, err := c.GetGateway(context.Background(), uuid.New(), "ns", "str-db")
	if err != nil {
		t.Fatalf("get existing gateway: %v", err)
	}
	addrs, _, _ := unstructured.NestedSlice(got.Object, "status", "addresses")
	if len(addrs) != 1 || addrs[0].(map[string]interface{})["value"] != "10.0.0.5" {
		t.Fatalf("status.addresses = %v, want one 10.0.0.5 entry", addrs)
	}

	_, err = c.GetGateway(context.Background(), uuid.New(), "ns", "str-missing")
	if !k8serrors.IsNotFound(err) {
		t.Fatalf("missing gateway: want a NotFound error, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cluster/ -run TestGetGateway -count=1`
Expected: FAIL to build: `c.GetGateway undefined (type *Client has no field or method GetGateway)`.

- [ ] **Step 3: Write minimal implementation**

In `internal/cluster/gateway.go`, add `"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"` to the imports (after the `metav1` line), and insert above `// DeleteGateway deletes a Gateway resource from Kubernetes`:

```go
// GetGateway reads a Gateway back from the project's cluster. Read-only: the
// stream entry-point view uses it for status.addresses. The API server's
// error is returned unwrapped, so callers can test it with
// k8serrors.IsNotFound (a Gateway that is not deployed yet).
func (s *Client) GetGateway(ctx context.Context, projectID uuid.UUID, namespace, name string) (*unstructured.Unstructured, error) {
	client, err := s.getClient(projectID)
	if err != nil {
		return nil, err
	}
	return client.Resource(kubernetes.GatewayGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
}
```

In `internal/services/k8s_roles.go`, insert immediately above the `// GatewayClassApplier writes the cluster-scoped pair a domain template owns:` comment:

```go
// GatewayStatusReader reads a Gateway back from a project's cluster so its
// status.addresses can be shown. It never writes.
//
// Consumer: StreamViewService (stream_view.go).
type GatewayStatusReader interface {
	GetGateway(ctx context.Context, projectID uuid.UUID, namespace, name string) (*unstructured.Unstructured, error)
}
```

and in the `var (...)` block of compile-time assertions, below `_ GatewayApplier         = (*cluster.Client)(nil)`, add:

```go
	_ GatewayStatusReader    = (*cluster.Client)(nil)
```

(`TestK8sRoles_EveryRoleIsAsserted` fails without it.)

In `internal/mocks/roles/roles.go`, add `services.GatewayStatusReader` to the `KubernetesService` union directly below `services.GatewayApplier`.

Regenerate the mocks: `make mocks`

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cluster/ -run TestGetGateway -count=1 && go test ./internal/services/ -run 'TestK8sRoles|TestNoWiringNilGuardsRemain' -count=1 && go build ./...`
Expected: `ok` for both packages; build succeeds. `git status --porcelain internal/mocks` lists only `mock_kubernetes.go`, `mock_services.go` and `roles/roles.go`.

- [ ] **Step 5: Commit**

```bash
git add internal/cluster/gateway.go internal/cluster/gateway_read_test.go internal/services/k8s_roles.go internal/mocks/
git commit -m "feat(cluster): read-only GatewayStatusReader role

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
make mocks-check
```

Expected: `make mocks-check` prints no drift.

---

### Task 3: Entry point builder + agreement with `CheckPortCollision`

**Files:**
- Create: `internal/services/stream_view.go`
- Regenerate: `internal/mocks/mock_services.go` (`make mocks`: new interfaces `StreamViewStore`, `DomainPortOwnerReader`)
- Test: `internal/services/stream_view_test.go` (new)

**Interfaces:**
- Consumes: Task 1 `repository.L4PortOwner`, `repository.DomainPortOwner` and the methods listed there; Task 2 `GatewayStatusReader`; existing `StreamTemplateReader`, `DefaultReservedPorts`, `templateDomainDefaults`, `templatePortRange`, `resolveGatewayAddress`, `ErrStreamTemplateNotFound`, `ErrStreamTemplateWrongProject`. Test helpers in `services_test`: `fakeTemplateReader`, `fakeRouteCounter`, `allowAllNamespaces` (`stream_service_test.go`), `mocks.NewMockGatewayApplier`.
- Produces (later tasks rely on these exact names):
  - constants `EntrypointModeDedicated = "dedicated"`, `EntrypointModeShared = "shared"`, `PortOwnerStreamRoute = "stream_route"`, `PortOwnerDomain = "domain"`, `ReservedReasonEnvoyInternal`, `ReservedReasonPlaceholder`, `ReservedReasonTemplateListener`;
  - types `StreamEntrypoint`, `EntrypointPortRange`, `EntrypointSharedWith`, `EntrypointStreamRef`, `EntrypointDomainRef`, `EntrypointPort`, `EntrypointPortOwner`, `EntrypointReservedPort`; aliases `services.L4PortOwner`, `services.DomainPortOwner`;
  - interfaces `StreamViewStore` (`ListByProjectID`, `ListByGatewayTemplateID`, `L4PortOwnersByStream`, `L4PortOwnersByTemplate`, `L4PortOwnersByStreamIDs`; Task 5 uses the two list methods) and `DomainPortOwnerReader` (`ListByTemplateID`, `PortOwnersByTemplate`). `*repository.StreamRepository` and `*repository.DomainRepository` satisfy them;
  - `StreamViewServiceDeps{Streams StreamViewStore; Templates StreamTemplateReader; Domains DomainPortOwnerReader; Gateways GatewayStatusReader}`, `NewStreamViewService(deps) *StreamViewService` (panics `"services.NewStreamViewService: missing required dependency: <Names>"`);
  - `(*StreamViewService).StreamEntrypoint(ctx context.Context, stream *models.Stream) (*StreamEntrypoint, error)` and `(*StreamViewService).TemplateEntrypoint(ctx context.Context, projectID, templateID uuid.UUID) (*StreamEntrypoint, error)`.

Rules the builder implements (spec B1): `mode` is shared iff `MergeGateways`. Dedicated: the stream's own routes (the template variant has `[]`), the stream's own address (the template variant has `""`), and `sharedWith` is empty. Shared: every stream's routes on the template plus every domain's bound ports, `sharedWith` holds the other streams and the domains, and the address is the stream's own, else the first member stream by name that has one. `reserved` = `DefaultReservedPorts` for both transports, plus `templateDomainDefaults` as TCP (R2).

- [ ] **Step 1: Write the failing test**

Create `internal/services/stream_view_test.go`. `portWorld` is one fixture that answers both the collision sources and the owner sources from the same rows, so the agreement test compares the two consumers. It also includes the two Review Focus tests owned here:

```go
package services_test

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// worldRoute is one L4 route row of a portWorld.
type worldRoute struct {
	id        uuid.UUID
	streamID  uuid.UUID
	name      string
	transport string
	port      int
	status    models.RouteStatus
}

// portWorld is one in-memory fixture that answers BOTH the collision
// sources CheckPortCollision reads (StreamStore, StreamPortStore,
// DomainPortReader) and the owner-aware sources the entry point reads
// (StreamViewStore, DomainPortOwnerReader). Every answer is derived from the
// same rows with the repository's rule (rejected routes hold no port), so
// the agreement test compares the two consumers, not two data sets. The
// repository tests pin that the real SQL of the two query families agrees.
type portWorld struct {
	tmpl    *models.DomainTemplate
	streams []models.Stream
	routes  []worldRoute
	domains []models.Domain

	ownerCalls struct{ byStream, byTemplate, byStreamIDs int }
}

func (w *portWorld) streamByID(id uuid.UUID) (*models.Stream, bool) {
	for i := range w.streams {
		if w.streams[i].ID == id {
			cp := w.streams[i]
			return &cp, true
		}
	}
	return nil, false
}

func (w *portWorld) liveRoutes(keep func(worldRoute) bool) []worldRoute {
	var out []worldRoute
	for _, r := range w.routes {
		if r.status != models.RouteStatusRejected && keep(r) {
			out = append(out, r)
		}
	}
	return out
}

func (w *portWorld) owners(rs []worldRoute) []services.L4PortOwner {
	out := []services.L4PortOwner{}
	for _, r := range rs {
		s, _ := w.streamByID(r.streamID)
		out = append(out, services.L4PortOwner{Transport: r.transport, Port: r.port, StreamID: r.streamID,
			StreamName: s.Name, RouteID: r.id, RouteName: r.name, Status: string(r.status)})
	}
	return out
}

func (w *portWorld) onTemplate(r worldRoute) bool {
	s, ok := w.streamByID(r.streamID)
	return ok && s.GatewayTemplateID == w.tmpl.ID
}

// --- services.StreamStore (for NewStreamService) ---

func (w *portWorld) Create(s *models.Stream) error { w.streams = append(w.streams, *s); return nil }
func (w *portWorld) GetByID(id uuid.UUID) (*models.Stream, error) {
	if s, ok := w.streamByID(id); ok {
		return s, nil
	}
	return nil, gorm.ErrRecordNotFound
}
func (w *portWorld) ListByProjectID(projectID uuid.UUID) ([]models.Stream, error) {
	var out []models.Stream
	for _, s := range w.streams {
		if s.ProjectID == projectID {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func (w *portWorld) ExistsByName(uuid.UUID, string) (bool, error) { return false, nil }
func (w *portWorld) Update(*models.Stream) error                  { return nil }
func (w *portWorld) Delete(uuid.UUID) error                       { return nil }

// --- services.StreamPortStore / DomainPortReader (CheckPortCollision) ---

func (w *portWorld) UsedPortsByStream(streamID uuid.UUID, exclude *uuid.UUID) ([]services.PortUse, error) {
	var out []services.PortUse
	for _, r := range w.liveRoutes(func(r worldRoute) bool { return r.streamID == streamID && (exclude == nil || r.id != *exclude) }) {
		out = append(out, services.PortUse{Transport: r.transport, Port: r.port})
	}
	return out, nil
}
func (w *portWorld) UsedL4Ports(_ uuid.UUID, exclude *uuid.UUID) ([]services.PortUse, error) {
	var out []services.PortUse
	for _, r := range w.liveRoutes(func(r worldRoute) bool { return w.onTemplate(r) && (exclude == nil || r.id != *exclude) }) {
		out = append(out, services.PortUse{Transport: r.transport, Port: r.port})
	}
	return out, nil
}
func (w *portWorld) UsedPortsByTemplate(uuid.UUID) ([]services.PortUse, error) {
	var out []services.PortUse
	for _, o := range w.domainOwners() {
		out = append(out, services.PortUse{Transport: o.Transport, Port: o.Port})
	}
	return out, nil
}

// --- services.StreamViewStore / DomainPortOwnerReader (entry point) ---

func (w *portWorld) ListByGatewayTemplateID(templateID uuid.UUID) ([]models.Stream, error) {
	var out []models.Stream
	for _, s := range w.streams {
		if s.GatewayTemplateID == templateID {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func (w *portWorld) L4PortOwnersByStream(streamID uuid.UUID) ([]services.L4PortOwner, error) {
	w.ownerCalls.byStream++
	return w.owners(w.liveRoutes(func(r worldRoute) bool { return r.streamID == streamID })), nil
}
func (w *portWorld) L4PortOwnersByTemplate(uuid.UUID) ([]services.L4PortOwner, error) {
	w.ownerCalls.byTemplate++
	return w.owners(w.liveRoutes(w.onTemplate)), nil
}
func (w *portWorld) L4PortOwnersByStreamIDs(ids []uuid.UUID) ([]services.L4PortOwner, error) {
	w.ownerCalls.byStreamIDs++
	want := map[uuid.UUID]bool{}
	for _, id := range ids {
		want[id] = true
	}
	return w.owners(w.liveRoutes(func(r worldRoute) bool { return want[r.streamID] })), nil
}
func (w *portWorld) ListByTemplateID(uuid.UUID) ([]models.Domain, error) { return w.domains, nil }
func (w *portWorld) PortOwnersByTemplate(uuid.UUID) ([]services.DomainPortOwner, error) {
	return w.domainOwners(), nil
}

func (w *portWorld) domainOwners() []services.DomainPortOwner {
	portByName := map[string]int{}
	for _, l := range w.tmpl.Listeners.HostnameRouted() {
		portByName[l.Name] = l.Port
	}
	out := []services.DomainPortOwner{}
	for _, d := range w.domains {
		for _, name := range d.BoundListeners {
			if p, ok := portByName[name]; ok {
				out = append(out, services.DomainPortOwner{Transport: "TCP", Port: p, DomainID: d.ID, DomainName: d.Name})
			}
		}
	}
	return out
}

// fakeGatewayReader answers GetGateway from a map of stream Gateway name to
// address ("" means the Gateway exists with no address yet). A name in errs
// fails with that error; any other name is NotFound.
type fakeGatewayReader struct {
	addrs map[string]string
	errs  map[string]error
	calls []string
}

func (f *fakeGatewayReader) GetGateway(_ context.Context, _ uuid.UUID, _, name string) (*unstructured.Unstructured, error) {
	f.calls = append(f.calls, name)
	if err, ok := f.errs[name]; ok {
		return nil, err
	}
	addr, ok := f.addrs[name]
	if !ok {
		return nil, k8serrors.NewNotFound(schema.GroupResource{Group: "gateway.networking.k8s.io", Resource: "gateways"}, name)
	}
	obj := &unstructured.Unstructured{Object: map[string]interface{}{"metadata": map[string]interface{}{"name": name}}}
	if addr != "" {
		obj.Object["status"] = map[string]interface{}{"addresses": []interface{}{map[string]interface{}{"type": "IPAddress", "value": addr}}}
	}
	return obj, nil
}

// newPortWorld is the shared fixture: a template with HTTP:80 (outside the
// range), HTTPS:19443 (inside it) and a TCP/UDP range 18990-60010 that also
// covers the reserved 19000, 19001 and 60000; three streams ("test-stream"
// owns routes, "cache" owns routes, "fresh" owns none) and two domains.
func newPortWorld(merge bool) (*portWorld, *models.Stream, *models.Stream, *models.Stream) {
	projectID := uuid.New()
	tmpl := &models.DomainTemplate{
		ID: uuid.New(), ProjectID: projectID, Name: "shared-envoy", K8sGatewayClassName: "shared-envoy-clusterip",
		MergeGateways: merge,
		Listeners: models.Listeners{
			{Name: "http", Protocol: models.ListenerHTTP, Port: 80},
			{Name: "https", Protocol: models.ListenerHTTPS, Port: 19443, TLSMode: models.TLSListenerTerminate},
			{Name: "stream", Protocol: models.ListenerTCP, PortRangeMin: 18990, PortRangeMax: 60010},
		},
	}
	mk := func(name string) models.Stream {
		return models.Stream{ID: uuid.New(), ProjectID: projectID, Name: name, Namespace: "fastgateway-system",
			GatewayTemplateID: tmpl.ID, K8sGatewayName: "str-" + name, K8sGatewayClass: tmpl.K8sGatewayClassName}
	}
	self, cache, fresh := mk("test-stream"), mk("cache"), mk("fresh")
	w := &portWorld{tmpl: tmpl, streams: []models.Stream{self, cache, fresh}}
	route := func(s models.Stream, name, transport string, port int, status models.RouteStatus) {
		w.routes = append(w.routes, worldRoute{id: uuid.New(), streamID: s.ID, name: name, transport: transport, port: port, status: status})
	}
	route(self, "postgres", "TCP", 19100, models.RouteStatusActive)
	route(self, "dns", "UDP", 19100, models.RouteStatusPendingCreate)
	route(self, "old", "TCP", 19101, models.RouteStatusRejected)
	route(cache, "redis", "TCP", 19102, models.RouteStatusActive)
	route(cache, "syslog", "UDP", 19103, models.RouteStatusPendingUpdate)
	w.domains = []models.Domain{
		{ID: uuid.New(), ProjectID: projectID, Name: "api", Hostname: "api.example.com", DomainTemplateID: &tmpl.ID, BoundListeners: models.StringList{"http", "https"}},
		{ID: uuid.New(), ProjectID: projectID, Name: "web", Hostname: "web.example.com", DomainTemplateID: &tmpl.ID, BoundListeners: models.StringList{"https"}},
	}
	return w, &w.streams[0], &w.streams[1], &w.streams[2]
}

func newViewService(w *portWorld, gw *fakeGatewayReader) *services.StreamViewService {
	return services.NewStreamViewService(services.StreamViewServiceDeps{
		Streams: w, Templates: &fakeTemplateReader{tmpl: w.tmpl}, Domains: w, Gateways: gw,
	})
}

func newCollisionService(t *testing.T, w *portWorld) *services.StreamService {
	t.Helper()
	svc := services.NewStreamService(w, &fakeTemplateReader{tmpl: w.tmpl}, &fakeRouteCounter{}, mocks.NewMockGatewayApplier(t), allowAllNamespaces{})
	svc.SetPortSources(w, w)
	return svc
}

func portKeys(ep *services.StreamEntrypoint) []string {
	var out []string
	for _, p := range ep.Ports {
		out = append(out, p.Transport+"/"+strconv.Itoa(p.Port)+" "+p.Owner.Kind)
	}
	return out
}

func TestNewStreamViewService_RequiresEveryDependency(t *testing.T) {
	w, _, _, _ := newPortWorld(false)
	full := func() services.StreamViewServiceDeps {
		return services.StreamViewServiceDeps{Streams: w, Templates: &fakeTemplateReader{tmpl: w.tmpl}, Domains: w, Gateways: &fakeGatewayReader{}}
	}
	require.NotPanics(t, func() { services.NewStreamViewService(full()) })
	for name, breakIt := range map[string]func(*services.StreamViewServiceDeps){
		"Streams":   func(d *services.StreamViewServiceDeps) { d.Streams = nil },
		"Templates": func(d *services.StreamViewServiceDeps) { d.Templates = nil },
		"Domains":   func(d *services.StreamViewServiceDeps) { d.Domains = nil },
		"Gateways":  func(d *services.StreamViewServiceDeps) { d.Gateways = nil },
	} {
		d := full()
		breakIt(&d)
		assert.PanicsWithValue(t, "services.NewStreamViewService: missing required dependency: "+name,
			func() { services.NewStreamViewService(d) })
	}
}

func TestStreamEntrypoint_Dedicated(t *testing.T) {
	w, self, _, _ := newPortWorld(false)
	gw := &fakeGatewayReader{addrs: map[string]string{"str-test-stream": "10.0.0.7", "str-cache": "10.0.0.8"}}

	ep, err := newViewService(w, gw).StreamEntrypoint(context.Background(), self)
	require.NoError(t, err)

	assert.Equal(t, services.EntrypointModeDedicated, ep.Mode)
	assert.Equal(t, w.tmpl.ID, ep.TemplateID)
	assert.Equal(t, "shared-envoy", ep.TemplateName)
	assert.Equal(t, "shared-envoy-clusterip", ep.GatewayClass)
	assert.Equal(t, "10.0.0.7", ep.Address, "dedicated: this stream's own Gateway address")
	assert.Equal(t, &services.EntrypointPortRange{Min: 18990, Max: 60010}, ep.PortRange)
	assert.Empty(t, ep.SharedWith.Streams)
	assert.Empty(t, ep.SharedWith.Domains)
	assert.Equal(t, []string{"TCP/19100 stream_route", "UDP/19100 stream_route"}, portKeys(ep),
		"dedicated: only this stream's routes; the rejected route holds no port")
	assert.Equal(t, []services.EntrypointReservedPort{
		{Transport: "TCP", Port: 19000, Reason: "envoy_internal"}, {Transport: "UDP", Port: 19000, Reason: "envoy_internal"},
		{Transport: "TCP", Port: 19001, Reason: "envoy_internal"}, {Transport: "UDP", Port: 19001, Reason: "envoy_internal"},
		{Transport: "TCP", Port: 60000, Reason: "placeholder"}, {Transport: "UDP", Port: 60000, Reason: "placeholder"},
	}, ep.Reserved, "dedicated: no template_listener reservations")
	assert.Equal(t, []string{"str-test-stream"}, gw.calls, "dedicated reads only its own Gateway")
}

func TestStreamEntrypoint_Shared(t *testing.T) {
	w, self, cache, fresh := newPortWorld(true)
	gw := &fakeGatewayReader{addrs: map[string]string{"str-test-stream": "10.0.0.5"}}

	ep, err := newViewService(w, gw).StreamEntrypoint(context.Background(), self)
	require.NoError(t, err)

	assert.Equal(t, services.EntrypointModeShared, ep.Mode)
	assert.Equal(t, "10.0.0.5", ep.Address)
	assert.Equal(t, []services.EntrypointStreamRef{{ID: cache.ID, Name: "cache"}, {ID: fresh.ID, Name: "fresh"}}, ep.SharedWith.Streams,
		"sharedWith excludes the stream itself")
	require.Len(t, ep.SharedWith.Domains, 2)
	assert.Equal(t, services.EntrypointDomainRef{ID: w.domains[0].ID, Name: "api", Hostname: "api.example.com"}, ep.SharedWith.Domains[0])
	assert.Equal(t, []string{
		"TCP/80 domain",
		"TCP/19100 stream_route", "UDP/19100 stream_route",
		"TCP/19102 stream_route", "UDP/19103 stream_route",
		"TCP/19443 domain", "TCP/19443 domain",
	}, portKeys(ep), "shared: every stream's routes plus every domain's bound listener ports")
	assert.Contains(t, ep.Reserved, services.EntrypointReservedPort{Transport: "TCP", Port: 80, Reason: "template_listener"})
	assert.Contains(t, ep.Reserved, services.EntrypointReservedPort{Transport: "TCP", Port: 19443, Reason: "template_listener"})
	assert.NotContains(t, ep.Reserved, services.EntrypointReservedPort{Transport: "UDP", Port: 19443, Reason: "template_listener"},
		"template listeners are reserved for TCP only, as CheckPortCollision does")

	owner := ep.Ports[1].Owner
	require.NotNil(t, owner.StreamID)
	assert.Equal(t, self.ID, *owner.StreamID)
	assert.Equal(t, "test-stream", owner.StreamName)
	assert.Equal(t, "postgres", owner.RouteName)
	assert.Equal(t, "active", owner.Status)
	assert.Nil(t, owner.DomainID)
}

func TestStreamEntrypoint_SharedAddressFallbackOrder(t *testing.T) {
	cases := []struct {
		name  string
		addrs map[string]string
		want  string
		calls []string
	}{
		{"own address wins", map[string]string{"str-test-stream": "10.0.0.5", "str-cache": "10.0.0.6"}, "10.0.0.5", []string{"str-test-stream"}},
		{"own Gateway has no address yet: first member by name", map[string]string{"str-test-stream": "", "str-cache": "10.0.0.6", "str-fresh": "10.0.0.9"}, "10.0.0.6", []string{"str-test-stream", "str-cache"}},
		{"own Gateway missing: next member with one", map[string]string{"str-fresh": "10.0.0.9"}, "10.0.0.9", []string{"str-test-stream", "str-cache", "str-fresh"}},
		{"nobody has one: empty (assigned on first deploy)", map[string]string{}, "", []string{"str-test-stream", "str-cache", "str-fresh"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, self, _, _ := newPortWorld(true)
			gw := &fakeGatewayReader{addrs: tc.addrs}
			ep, err := newViewService(w, gw).StreamEntrypoint(context.Background(), self)
			require.NoError(t, err)
			assert.Equal(t, tc.want, ep.Address)
			assert.Equal(t, tc.calls, gw.calls)
		})
	}
}

func TestStreamEntrypoint_GatewayReadFailureIsAnError(t *testing.T) {
	w, self, _, _ := newPortWorld(false)
	gw := &fakeGatewayReader{errs: map[string]error{"str-test-stream": errors.New("connection refused")}}

	ep, err := newViewService(w, gw).StreamEntrypoint(context.Background(), self)
	assert.Nil(t, ep)
	assert.ErrorContains(t, err, "connection refused")
}

func TestStreamEntrypoint_TemplateLookupFailureIsAnError(t *testing.T) {
	w, self, _, _ := newPortWorld(true)
	svc := services.NewStreamViewService(services.StreamViewServiceDeps{
		Streams: w, Templates: &fakeTemplateReader{err: errors.New("db down")}, Domains: w, Gateways: &fakeGatewayReader{},
	})

	ep, err := svc.StreamEntrypoint(context.Background(), self)
	assert.Nil(t, ep, "never fall back to a guessed mode")
	assert.ErrorContains(t, err, "db down")
	assert.NotErrorIs(t, err, services.ErrStreamTemplateNotFound, "a broken stream->template link is a 500, not a 404")
}

func TestStreamEntrypoint_NoStreamRange_PortRangeNull(t *testing.T) {
	w, self, _, _ := newPortWorld(false)
	w.tmpl.Listeners = models.Listeners{{Name: "https", Protocol: models.ListenerHTTPS, Port: 443, TLSMode: models.TLSListenerTerminate}}

	ep, err := newViewService(w, &fakeGatewayReader{}).StreamEntrypoint(context.Background(), self)
	require.NoError(t, err)
	assert.Nil(t, ep.PortRange)
	raw, err := json.Marshal(ep)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"portRange":null`)
}

func TestTemplateEntrypoint(t *testing.T) {
	t.Run("dedicated: empty port space, no address", func(t *testing.T) {
		w, _, _, _ := newPortWorld(false)
		gw := &fakeGatewayReader{addrs: map[string]string{"str-test-stream": "10.0.0.7"}}
		ep, err := newViewService(w, gw).TemplateEntrypoint(context.Background(), w.tmpl.ProjectID, w.tmpl.ID)
		require.NoError(t, err)
		assert.Equal(t, services.EntrypointModeDedicated, ep.Mode)
		assert.Equal(t, []services.EntrypointPort{}, ep.Ports)
		assert.Equal(t, "", ep.Address)
		assert.Empty(t, gw.calls)
		assert.Zero(t, w.ownerCalls.byStream+w.ownerCalls.byTemplate)
	})
	t.Run("shared: whole template, every stream in sharedWith, any member address", func(t *testing.T) {
		w, _, _, _ := newPortWorld(true)
		gw := &fakeGatewayReader{addrs: map[string]string{"str-fresh": "10.0.0.9"}}
		ep, err := newViewService(w, gw).TemplateEntrypoint(context.Background(), w.tmpl.ProjectID, w.tmpl.ID)
		require.NoError(t, err)
		assert.Equal(t, services.EntrypointModeShared, ep.Mode)
		assert.Len(t, ep.SharedWith.Streams, 3, "no stream is excluded before one exists")
		assert.Len(t, ep.Ports, 7)
		assert.Equal(t, "10.0.0.9", ep.Address)
	})
	t.Run("missing template is ErrStreamTemplateNotFound", func(t *testing.T) {
		w, _, _, _ := newPortWorld(true)
		svc := services.NewStreamViewService(services.StreamViewServiceDeps{
			Streams: w, Templates: &fakeTemplateReader{err: gorm.ErrRecordNotFound}, Domains: w, Gateways: &fakeGatewayReader{},
		})
		_, err := svc.TemplateEntrypoint(context.Background(), w.tmpl.ProjectID, uuid.New())
		assert.ErrorIs(t, err, services.ErrStreamTemplateNotFound)
	})
	t.Run("other project's template is ErrStreamTemplateWrongProject", func(t *testing.T) {
		w, _, _, _ := newPortWorld(true)
		_, err := newViewService(w, &fakeGatewayReader{}).TemplateEntrypoint(context.Background(), uuid.New(), w.tmpl.ID)
		assert.ErrorIs(t, err, services.ErrStreamTemplateWrongProject)
	})
	t.Run("other template lookup failure is an error", func(t *testing.T) {
		w, _, _, _ := newPortWorld(true)
		svc := services.NewStreamViewService(services.StreamViewServiceDeps{
			Streams: w, Templates: &fakeTemplateReader{err: errors.New("db down")}, Domains: w, Gateways: &fakeGatewayReader{},
		})
		_, err := svc.TemplateEntrypoint(context.Background(), w.tmpl.ProjectID, w.tmpl.ID)
		assert.ErrorContains(t, err, "db down")
		assert.NotErrorIs(t, err, services.ErrStreamTemplateNotFound)
	})
}

// listedOrOutside is the entry point's answer for (transport, port): taken by
// a route/domain, reserved, or outside the template's range.
func listedOrOutside(ep *services.StreamEntrypoint, transport string, port int) bool {
	if ep.PortRange == nil || port < ep.PortRange.Min || port > ep.PortRange.Max {
		return true
	}
	for _, p := range ep.Ports {
		if p.Transport == transport && p.Port == port {
			return true
		}
	}
	for _, r := range ep.Reserved {
		if r.Transport == transport && r.Port == port {
			return true
		}
	}
	return false
}

// TestStreamEntrypoint_AgreesWithCheckPortCollision is the spec's agreement
// invariant: for every port in [min-1, max+1] and both transports, the
// entry point lists the port (or it is outside portRange) if and only if
// CheckPortCollision rejects it for that stream. The template variant is
// checked against a stream with no routes ("fresh"), which is exactly the
// port space a new stream starts with.
func TestStreamEntrypoint_AgreesWithCheckPortCollision(t *testing.T) {
	for _, merge := range []bool{false, true} {
		w, self, _, fresh := newPortWorld(merge)
		views := newViewService(w, &fakeGatewayReader{})
		collisions := newCollisionService(t, w)

		streamEP, err := views.StreamEntrypoint(context.Background(), self)
		require.NoError(t, err)
		templateEP, err := views.TemplateEntrypoint(context.Background(), w.tmpl.ProjectID, w.tmpl.ID)
		require.NoError(t, err)

		for _, check := range []struct {
			name     string
			ep       *services.StreamEntrypoint
			streamID uuid.UUID
		}{
			{"stream variant", streamEP, self.ID},
			{"template variant", templateEP, fresh.ID},
		} {
			lo, hi := check.ep.PortRange.Min, check.ep.PortRange.Max
			var mismatches []string
			for _, transport := range []string{"TCP", "UDP"} {
				for port := lo - 1; port <= hi+1; port++ {
					rejected := collisions.CheckPortCollision(check.streamID, transport, port, nil) != nil
					if listed := listedOrOutside(check.ep, transport, port); listed != rejected {
						mismatches = append(mismatches, transport+"/"+strconv.Itoa(port))
						if len(mismatches) > 10 {
							break
						}
					}
				}
			}
			assert.Emptyf(t, mismatches, "merge=%v %s: entry point and CheckPortCollision disagree on %v", merge, check.name, mismatches)
		}
	}
}

func TestStreamEntrypoint_JSONContract(t *testing.T) {
	streamID, routeID, domainID, tmplID := uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		uuid.MustParse("22222222-2222-2222-2222-222222222222"), uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		uuid.MustParse("44444444-4444-4444-4444-444444444444")
	ep := services.StreamEntrypoint{
		Mode: "shared", TemplateID: tmplID, TemplateName: "shared-envoy", GatewayClass: "shared-envoy-clusterip",
		Address:   "10.0.0.5",
		PortRange: &services.EntrypointPortRange{Min: 9000, Max: 9100},
		SharedWith: services.EntrypointSharedWith{
			Streams: []services.EntrypointStreamRef{{ID: streamID, Name: "cache"}},
			Domains: []services.EntrypointDomainRef{{ID: domainID, Name: "api", Hostname: "api.example.com"}},
		},
		Ports: []services.EntrypointPort{
			{Transport: "TCP", Port: 5432, Owner: services.EntrypointPortOwner{Kind: "stream_route", StreamID: &streamID, StreamName: "test-stream", RouteID: &routeID, RouteName: "postgres", Status: "active"}},
			{Transport: "TCP", Port: 443, Owner: services.EntrypointPortOwner{Kind: "domain", DomainID: &domainID, DomainName: "api"}},
		},
		Reserved: []services.EntrypointReservedPort{{Transport: "TCP", Port: 19000, Reason: "envoy_internal"}},
	}
	raw, err := json.Marshal(ep)
	require.NoError(t, err)
	assert.JSONEq(t, `{
	  "mode": "shared",
	  "templateId": "44444444-4444-4444-4444-444444444444",
	  "templateName": "shared-envoy",
	  "gatewayClass": "shared-envoy-clusterip",
	  "address": "10.0.0.5",
	  "portRange": {"min": 9000, "max": 9100},
	  "sharedWith": {
	    "streams": [{"id": "11111111-1111-1111-1111-111111111111", "name": "cache"}],
	    "domains": [{"id": "33333333-3333-3333-3333-333333333333", "name": "api", "hostname": "api.example.com"}]
	  },
	  "ports": [
	    {"transport": "TCP", "port": 5432, "owner": {"kind": "stream_route", "streamId": "11111111-1111-1111-1111-111111111111",
	      "streamName": "test-stream", "routeId": "22222222-2222-2222-2222-222222222222", "routeName": "postgres", "status": "active"}},
	    {"transport": "TCP", "port": 443, "owner": {"kind": "domain", "domainId": "33333333-3333-3333-3333-333333333333", "domainName": "api"}}
	  ],
	  "reserved": [{"transport": "TCP", "port": 19000, "reason": "envoy_internal"}]
	}`, string(raw))
}

func TestStreamEntrypoint_ReservedTemplateListenerPortListedOnce(t *testing.T) {
	w, self, _, _ := newPortWorld(true)
	w.tmpl.Listeners = models.Listeners{
		{Name: "http", Protocol: models.ListenerHTTP, Port: 19443},
		{Name: "tls", Protocol: models.ListenerTLS, Port: 19443},
		{Name: "stream", Protocol: models.ListenerTCP, PortRangeMin: 18990, PortRangeMax: 60010},
	}

	ep, err := newViewService(w, &fakeGatewayReader{}).StreamEntrypoint(context.Background(), self)
	require.NoError(t, err)

	n := 0
	for _, r := range ep.Reserved {
		if r.Port == 19443 {
			n++
		}
	}
	assert.Equal(t, 1, n, "two hostname listeners on one port reserve it once")
}

func TestStreamEntrypoint_Dedicated_GatewayNotDeployedYet(t *testing.T) {
	w, self, _, _ := newPortWorld(false)

	ep, err := newViewService(w, &fakeGatewayReader{}).StreamEntrypoint(context.Background(), self)
	require.NoError(t, err, "a stream whose Gateway does not exist yet is not an error")
	assert.Equal(t, "", ep.Address, "shown as assigned on first deploy")
	assert.Len(t, ep.Ports, 2)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/services/ -run 'TestNewStreamViewService|TestStreamEntrypoint|TestTemplateEntrypoint' -count=1`
Expected: FAIL to build: `undefined: services.StreamViewService`, `undefined: services.L4PortOwner`, `undefined: services.EntrypointModeDedicated`, ….

- [ ] **Step 3: Write minimal implementation**

Create `internal/services/stream_view.go`:

```go
// StreamViewService: the read-only views that make a Stream legible - its
// entry point (dedicated or shared IP, who shares it, which ports are taken
// by what) and the enriched stream list.
//
// Nothing here writes or applies anything. The entry point reads its port map
// from the same sources CheckPortCollision validates against (the owner-aware
// twins of UsedPortsByStream / UsedL4Ports / UsedPortsByTemplate, plus
// DefaultReservedPorts and templateDomainDefaults), so the map can never say
// "free" for a port that create would reject. TestStreamEntrypoint_AgreesWith
// CheckPortCollision pins that.

package services

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
	"gorm.io/gorm"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
)

// Entry point modes. A template with mergeGateways gives every stream (and
// domain) on it one shared Envoy, Service and IP; otherwise each stream has
// its own.
const (
	EntrypointModeDedicated = "dedicated"
	EntrypointModeShared    = "shared"
)

// Port owner kinds and reserved-port reasons in the entry point port map.
const (
	PortOwnerStreamRoute = "stream_route"
	PortOwnerDomain      = "domain"

	ReservedReasonEnvoyInternal    = "envoy_internal"
	ReservedReasonPlaceholder      = "placeholder"
	ReservedReasonTemplateListener = "template_listener"
)

// L4PortOwner is one listener an L4 route holds, with its route and stream.
type L4PortOwner = repository.L4PortOwner

// DomainPortOwner is one hostname-routed listener port a domain binds.
type DomainPortOwner = repository.DomainPortOwner

// StreamEntrypoint is GET .../streams/:streamId/entrypoint and
// GET .../domain-templates/:domainTemplateId/entrypoint.
type StreamEntrypoint struct {
	Mode         string                   `json:"mode"`
	TemplateID   uuid.UUID                `json:"templateId"`
	TemplateName string                   `json:"templateName"`
	GatewayClass string                   `json:"gatewayClass"`
	Address      string                   `json:"address"`
	PortRange    *EntrypointPortRange     `json:"portRange"`
	SharedWith   EntrypointSharedWith     `json:"sharedWith"`
	Ports        []EntrypointPort         `json:"ports"`
	Reserved     []EntrypointReservedPort `json:"reserved"`
}

// EntrypointPortRange is the template's TCP/UDP range (inclusive).
type EntrypointPortRange struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

// EntrypointSharedWith lists who else is on a shared entry point. Both lists
// are empty for a dedicated one.
type EntrypointSharedWith struct {
	Streams []EntrypointStreamRef `json:"streams"`
	Domains []EntrypointDomainRef `json:"domains"`
}

// EntrypointStreamRef names another stream on the shared entry point.
type EntrypointStreamRef struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// EntrypointDomainRef names a domain on the shared entry point.
type EntrypointDomainRef struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	Hostname string    `json:"hostname"`
}

// EntrypointPort is one taken (transport, port) and what holds it.
type EntrypointPort struct {
	Transport string              `json:"transport"`
	Port      int                 `json:"port"`
	Owner     EntrypointPortOwner `json:"owner"`
}

// EntrypointPortOwner is either a stream route (Kind "stream_route": the
// stream and route fields are set) or a domain (Kind "domain": the domain
// fields are set).
type EntrypointPortOwner struct {
	Kind       string     `json:"kind"`
	StreamID   *uuid.UUID `json:"streamId,omitempty"`
	StreamName string     `json:"streamName,omitempty"`
	RouteID    *uuid.UUID `json:"routeId,omitempty"`
	RouteName  string     `json:"routeName,omitempty"`
	Status     string     `json:"status,omitempty"`
	DomainID   *uuid.UUID `json:"domainId,omitempty"`
	DomainName string     `json:"domainName,omitempty"`
}

// EntrypointReservedPort is a (transport, port) no route may claim, and why.
type EntrypointReservedPort struct {
	Transport string `json:"transport"`
	Port      int    `json:"port"`
	Reason    string `json:"reason"`
}

// StreamViewStore is the slice of StreamRepository the stream views read.
// *repository.StreamRepository satisfies it structurally.
type StreamViewStore interface {
	ListByProjectID(projectID uuid.UUID) ([]models.Stream, error)
	ListByGatewayTemplateID(templateID uuid.UUID) ([]models.Stream, error)
	L4PortOwnersByStream(streamID uuid.UUID) ([]L4PortOwner, error)
	L4PortOwnersByTemplate(templateID uuid.UUID) ([]L4PortOwner, error)
	L4PortOwnersByStreamIDs(streamIDs []uuid.UUID) ([]L4PortOwner, error)
}

// DomainPortOwnerReader is the slice of DomainRepository the entry point
// reads. *repository.DomainRepository satisfies it structurally.
type DomainPortOwnerReader interface {
	ListByTemplateID(templateID uuid.UUID) ([]models.Domain, error)
	PortOwnersByTemplate(templateID uuid.UUID) ([]DomainPortOwner, error)
}

// StreamViewServiceDeps carries everything StreamViewService needs. Every
// field is required.
type StreamViewServiceDeps struct {
	Streams   StreamViewStore
	Templates StreamTemplateReader
	Domains   DomainPortOwnerReader
	Gateways  GatewayStatusReader
}

// StreamViewService builds the read-only stream views.
type StreamViewService struct {
	streams   StreamViewStore
	templates StreamTemplateReader
	domains   DomainPortOwnerReader
	gateways  GatewayStatusReader
}

// NewStreamViewService builds a StreamViewService. It panics if a dependency
// is missing, like NewApprovalService.
func NewStreamViewService(deps StreamViewServiceDeps) *StreamViewService {
	var missing []string
	if deps.Streams == nil {
		missing = append(missing, "Streams")
	}
	if deps.Templates == nil {
		missing = append(missing, "Templates")
	}
	if deps.Domains == nil {
		missing = append(missing, "Domains")
	}
	if deps.Gateways == nil {
		missing = append(missing, "Gateways")
	}
	if len(missing) > 0 {
		panic("services.NewStreamViewService: missing required dependency: " + strings.Join(missing, ", "))
	}
	return &StreamViewService{streams: deps.Streams, templates: deps.Templates, domains: deps.Domains, gateways: deps.Gateways}
}

// entrypointMode is "shared" if and only if the template merges gateways.
func entrypointMode(tmpl *models.DomainTemplate) string {
	if tmpl.MergeGateways {
		return EntrypointModeShared
	}
	return EntrypointModeDedicated
}

// StreamEntrypoint describes the entry point of an existing stream. The
// caller has already checked the stream belongs to the project. A template
// lookup failure is an error: the mode is never guessed.
func (s *StreamViewService) StreamEntrypoint(ctx context.Context, stream *models.Stream) (*StreamEntrypoint, error) {
	tmpl, err := s.templates.GetByID(stream.GatewayTemplateID)
	if err != nil {
		return nil, fmt.Errorf("failed to load gateway template: %w", err)
	}
	return s.buildEntrypoint(ctx, tmpl, stream)
}

// TemplateEntrypoint describes the entry point a new stream on the template
// would get. A missing template, or one in another project, is
// ErrStreamTemplateNotFound / ErrStreamTemplateWrongProject (both 404).
func (s *StreamViewService) TemplateEntrypoint(ctx context.Context, projectID, templateID uuid.UUID) (*StreamEntrypoint, error) {
	tmpl, err := s.templates.GetByID(templateID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrStreamTemplateNotFound
		}
		return nil, fmt.Errorf("failed to load gateway template: %w", err)
	}
	if tmpl.ProjectID != projectID {
		return nil, ErrStreamTemplateWrongProject
	}
	return s.buildEntrypoint(ctx, tmpl, nil)
}

// buildEntrypoint is the one builder behind both endpoints. self is the
// stream being described, or nil for the template variant (a stream that
// does not exist yet, with an empty port space of its own).
func (s *StreamViewService) buildEntrypoint(ctx context.Context, tmpl *models.DomainTemplate, self *models.Stream) (*StreamEntrypoint, error) {
	ep := &StreamEntrypoint{
		Mode:         entrypointMode(tmpl),
		TemplateID:   tmpl.ID,
		TemplateName: tmpl.Name,
		GatewayClass: tmpl.K8sGatewayClassName,
		SharedWith:   EntrypointSharedWith{Streams: []EntrypointStreamRef{}, Domains: []EntrypointDomainRef{}},
		Ports:        []EntrypointPort{},
		Reserved:     reservedPorts(tmpl),
	}
	if lo, hi, ok := templatePortRange(tmpl); ok {
		ep.PortRange = &EntrypointPortRange{Min: lo, Max: hi}
	}

	if ep.Mode == EntrypointModeDedicated {
		if self != nil {
			owners, err := s.streams.L4PortOwnersByStream(self.ID)
			if err != nil {
				return nil, fmt.Errorf("failed to load stream ports: %w", err)
			}
			ep.Ports = appendStreamRoutePorts(ep.Ports, owners)
			addr, err := s.gatewayAddress(ctx, self)
			if err != nil {
				return nil, err
			}
			ep.Address = addr
		}
		sortEntrypointPorts(ep.Ports)
		return ep, nil
	}

	// Shared: the port scope is the whole template, exactly as
	// CheckPortCollision's merged branch.
	owners, err := s.streams.L4PortOwnersByTemplate(tmpl.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load template stream ports: %w", err)
	}
	ep.Ports = appendStreamRoutePorts(ep.Ports, owners)
	domainOwners, err := s.domains.PortOwnersByTemplate(tmpl.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load template domain ports: %w", err)
	}
	for _, o := range domainOwners {
		id := o.DomainID
		ep.Ports = append(ep.Ports, EntrypointPort{Transport: o.Transport, Port: o.Port,
			Owner: EntrypointPortOwner{Kind: PortOwnerDomain, DomainID: &id, DomainName: o.DomainName}})
	}
	sortEntrypointPorts(ep.Ports)

	members, err := s.streams.ListByGatewayTemplateID(tmpl.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load template streams: %w", err)
	}
	others := make([]models.Stream, 0, len(members))
	for _, m := range members {
		if self != nil && m.ID == self.ID {
			continue
		}
		others = append(others, m)
		ep.SharedWith.Streams = append(ep.SharedWith.Streams, EntrypointStreamRef{ID: m.ID, Name: m.Name})
	}
	domains, err := s.domains.ListByTemplateID(tmpl.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load template domains: %w", err)
	}
	for _, d := range domains {
		ep.SharedWith.Domains = append(ep.SharedWith.Domains, EntrypointDomainRef{ID: d.ID, Name: d.Name, Hostname: d.Hostname})
	}

	// Address: this stream's, else the first member stream (by name) that
	// has one. Every member Gateway reflects the one shared Service.
	candidates := others
	if self != nil {
		candidates = append([]models.Stream{*self}, others...)
	}
	for i := range candidates {
		addr, err := s.gatewayAddress(ctx, &candidates[i])
		if err != nil {
			return nil, err
		}
		if addr != "" {
			ep.Address = addr
			break
		}
	}
	return ep, nil
}

// gatewayAddress reads the stream Gateway's first status address. A Gateway
// that does not exist yet, or has no address yet, is "" (assigned on first
// deploy); any other read failure is an error.
func (s *StreamViewService) gatewayAddress(ctx context.Context, stream *models.Stream) (string, error) {
	obj, err := s.gateways.GetGateway(ctx, stream.ProjectID, stream.Namespace, stream.K8sGatewayName)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return "", nil
		}
		return "", fmt.Errorf("failed to read Gateway %s/%s: %w", stream.Namespace, stream.K8sGatewayName, err)
	}
	addr, ok := resolveGatewayAddress(obj)
	if !ok {
		return "", nil
	}
	return addr.Value, nil
}

// reservedPorts lists what CheckPortCollision reserves: DefaultReservedPorts
// for both transports (ValidateListenerPort rejects them for TCP and UDP),
// plus, on a merged template, its hostname-routed listener ports as TCP.
func reservedPorts(tmpl *models.DomainTemplate) []EntrypointReservedPort {
	var out []EntrypointReservedPort
	seen := map[EntrypointReservedPort]bool{}
	add := func(transport string, port int, reason string) {
		r := EntrypointReservedPort{Transport: transport, Port: port, Reason: reason}
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	for _, transport := range []string{"TCP", "UDP"} {
		for _, p := range DefaultReservedPorts.EnvoyInternal {
			add(transport, p, ReservedReasonEnvoyInternal)
		}
		if DefaultReservedPorts.Placeholder != 0 {
			add(transport, DefaultReservedPorts.Placeholder, ReservedReasonPlaceholder)
		}
	}
	for _, p := range templateDomainDefaults(tmpl) {
		add("TCP", p, ReservedReasonTemplateListener)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Port != out[j].Port {
			return out[i].Port < out[j].Port
		}
		return out[i].Transport < out[j].Transport
	})
	return out
}

func appendStreamRoutePorts(ports []EntrypointPort, owners []L4PortOwner) []EntrypointPort {
	for _, o := range owners {
		streamID, routeID := o.StreamID, o.RouteID
		ports = append(ports, EntrypointPort{Transport: o.Transport, Port: o.Port, Owner: EntrypointPortOwner{
			Kind: PortOwnerStreamRoute, StreamID: &streamID, StreamName: o.StreamName,
			RouteID: &routeID, RouteName: o.RouteName, Status: o.Status,
		}})
	}
	return ports
}

// sortEntrypointPorts orders the map by port, then transport, then owner.
func sortEntrypointPorts(ports []EntrypointPort) {
	sort.SliceStable(ports, func(i, j int) bool {
		a, b := ports[i], ports[j]
		if a.Port != b.Port {
			return a.Port < b.Port
		}
		if a.Transport != b.Transport {
			return a.Transport < b.Transport
		}
		if a.Owner.Kind != b.Owner.Kind {
			return a.Owner.Kind < b.Owner.Kind
		}
		return a.Owner.StreamName+a.Owner.RouteName+a.Owner.DomainName < b.Owner.StreamName+b.Owner.RouteName+b.Owner.DomainName
	})
}
```

Regenerate the mocks (the package mocks every interface): `make mocks`

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/services/ -run 'TestNewStreamViewService|TestStreamEntrypoint|TestTemplateEntrypoint' -count=1 -v`
Expected: PASS, including `TestStreamEntrypoint_AgreesWithCheckPortCollision` (about 82k `CheckPortCollision` calls against in-memory fakes, well under a second).

Sanity check that the agreement test bites (do not commit this). Temporarily change `for _, transport := range []string{"TCP", "UDP"} {` in `reservedPorts` to `[]string{"TCP"}` and rerun `-run TestStreamEntrypoint_AgreesWithCheckPortCollision`. Expected: FAIL `disagree on [UDP/19000 UDP/19001 UDP/60000]`. Revert the change.

Then: `go test ./internal/services/ -count=1`. Expected: `ok` (this includes `TestNoWiringNilGuardsRemain*`).

- [ ] **Step 5: Commit**

```bash
git add internal/services/stream_view.go internal/services/stream_view_test.go internal/mocks/
git commit -m "feat(streams): entry point builder agreeing with CheckPortCollision

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
make mocks-check
```

---

### Task 4: Entry point endpoints (stream + template)

**Files:**
- Modify: `internal/handlers/service_interfaces.go` (new `StreamViewServiceInterface` before `TemplateDomainLister`; compile-time assertion)
- Modify: `internal/handlers/stream_handler.go` (struct/constructor; two handlers appended)
- Modify: `internal/handlers/stream_handler_test.go` (`newStreamHandler` passes the new argument)
- Modify: `cmd/server/main.go` (construct `StreamViewService`; `NewStreamHandler` call; two routes)
- Modify: `docs/openapi/schemas/stream.yaml`, `docs/openapi/paths/streams.yaml`, `docs/openapi/paths/domain-templates.yaml`, `docs/openapi/openapi.yaml`; regenerate `cmd/server/openapi.yaml`
- Regenerate: `internal/mocks/mock_handler_services.go`
- Test: `internal/handlers/stream_entrypoint_handler_test.go` (new)

**Interfaces:**
- Consumes: Task 3 `StreamEntrypoint`, `NewStreamViewService`, `StreamViewServiceDeps`; existing `loadStream`, `streamError`, `doStream`, `testUser`, `domainPermChecker`.
- Produces: `handlers.StreamViewServiceInterface{StreamEntrypoint(ctx, *models.Stream); TemplateEntrypoint(ctx, projectID, templateID uuid.UUID)}` (Task 5 adds `ListWithSummary`); `mocks.MockStreamViewService`; `NewStreamHandler(streamService StreamServiceInterface, views StreamViewServiceInterface, auditService AuditServiceInterface, permChecker *middleware.PermissionChecker)`; handlers `(*StreamHandler).GetEntrypoint`, `(*StreamHandler).GetTemplateEntrypoint`. Routes: `GET /projects/:projectId/streams/:streamId/entrypoint` (streams group, `RequireProjectAccess`) and `GET /projects/:projectId/domain-templates/:domainTemplateId/entrypoint` (the `domainTemplatesRead` group, `RequireDomainTemplateReadAccess`, the same access as reading a template).

- [ ] **Step 1: Write the failing test**

Create `internal/handlers/stream_entrypoint_handler_test.go`:

```go
package handlers_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/handlers"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// entrypointRouter mounts the two entry point endpoints the way
// cmd/server/main.go does.
func entrypointRouter(h *handlers.StreamHandler) *gin.Engine {
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("user", testUser()); c.Next() })
	r.GET("/projects/:projectId/streams/:streamId/entrypoint", h.GetEntrypoint)
	r.GET("/projects/:projectId/domain-templates/:domainTemplateId/entrypoint", h.GetTemplateEntrypoint)
	return r
}

func newEntrypointHandler() (*handlers.StreamHandler, *mocks.MockStreamService, *mocks.MockStreamViewService) {
	svc, views := new(mocks.MockStreamService), new(mocks.MockStreamViewService)
	return handlers.NewStreamHandler(svc, views, new(mocks.MockAuditService), domainPermChecker()), svc, views
}

func TestStreamHandler_GetEntrypoint_Success(t *testing.T) {
	h, svc, views := newEntrypointHandler()
	projectID, streamID := uuid.New(), uuid.New()
	stream := &models.Stream{ID: streamID, ProjectID: projectID, Name: "test-stream"}
	svc.On("Get", streamID).Return(stream, nil)
	views.On("StreamEntrypoint", mock.Anything, stream).Return(&services.StreamEntrypoint{
		Mode: services.EntrypointModeShared, Address: "10.0.0.5",
		SharedWith: services.EntrypointSharedWith{Streams: []services.EntrypointStreamRef{}, Domains: []services.EntrypointDomainRef{}},
		Ports:      []services.EntrypointPort{}, Reserved: []services.EntrypointReservedPort{},
	}, nil)

	w := doStream(entrypointRouter(h), "GET", fmt.Sprintf("/projects/%s/streams/%s/entrypoint", projectID, streamID), nil)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "shared", body["mode"])
	assert.Equal(t, "10.0.0.5", body["address"])
	for _, key := range []string{"templateId", "templateName", "gatewayClass", "portRange", "sharedWith", "ports", "reserved"} {
		assert.Contains(t, body, key)
	}
}

func TestStreamHandler_GetEntrypoint_OtherProjectStreamIsNotFound(t *testing.T) {
	h, svc, views := newEntrypointHandler()
	streamID := uuid.New()
	svc.On("Get", streamID).Return(&models.Stream{ID: streamID, ProjectID: uuid.New()}, nil)

	w := doStream(entrypointRouter(h), "GET", fmt.Sprintf("/projects/%s/streams/%s/entrypoint", uuid.New(), streamID), nil)

	assert.Equal(t, http.StatusNotFound, w.Code)
	views.AssertNotCalled(t, "StreamEntrypoint", mock.Anything, mock.Anything)
}

func TestStreamHandler_GetEntrypoint_BuilderFailureIs500(t *testing.T) {
	h, svc, views := newEntrypointHandler()
	projectID, streamID := uuid.New(), uuid.New()
	stream := &models.Stream{ID: streamID, ProjectID: projectID}
	svc.On("Get", streamID).Return(stream, nil)
	views.On("StreamEntrypoint", mock.Anything, stream).Return(nil, errors.New("failed to load gateway template: record not found"))

	w := doStream(entrypointRouter(h), "GET", fmt.Sprintf("/projects/%s/streams/%s/entrypoint", projectID, streamID), nil)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestStreamHandler_GetTemplateEntrypoint_ErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code int
	}{
		{"ok", nil, http.StatusOK},
		{"missing template", services.ErrStreamTemplateNotFound, http.StatusNotFound},
		{"other project's template", services.ErrStreamTemplateWrongProject, http.StatusNotFound},
		{"lookup failure", errors.New("db down"), http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _, views := newEntrypointHandler()
			projectID, templateID := uuid.New(), uuid.New()
			var ep *services.StreamEntrypoint
			if tc.err == nil {
				ep = &services.StreamEntrypoint{Mode: services.EntrypointModeDedicated}
			}
			views.On("TemplateEntrypoint", mock.Anything, projectID, templateID).Return(ep, tc.err)

			w := doStream(entrypointRouter(h), "GET", fmt.Sprintf("/projects/%s/domain-templates/%s/entrypoint", projectID, templateID), nil)

			assert.Equal(t, tc.code, w.Code, w.Body.String())
		})
	}
}

func TestStreamHandler_GetTemplateEntrypoint_BadTemplateID(t *testing.T) {
	h, _, _ := newEntrypointHandler()
	w := doStream(entrypointRouter(h), "GET", fmt.Sprintf("/projects/%s/domain-templates/not-a-uuid/entrypoint", uuid.New()), nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}
```

In `internal/handlers/stream_handler_test.go`, change the return line of `newStreamHandler` to:

```go
	return handlers.NewStreamHandler(svc, new(mocks.MockStreamViewService), audit, domainPermChecker()), svc, audit
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/handlers/ -run TestStreamHandler -count=1`
Expected: FAIL to build: `undefined: mocks.MockStreamViewService`, `too many arguments in call to handlers.NewStreamHandler`, `h.GetEntrypoint undefined (type *handlers.StreamHandler has no field or method GetEntrypoint)`.

- [ ] **Step 3: Write minimal implementation**

In `internal/handlers/service_interfaces.go`, insert above `// TemplateDomainLister is the slice of DomainTemplateService that`:

```go
// StreamViewServiceInterface is the read-only stream views StreamHandler
// serves: the entry point of a stream, and of a template before a stream
// exists. *services.StreamViewService satisfies it.
type StreamViewServiceInterface interface {
	StreamEntrypoint(ctx context.Context, stream *models.Stream) (*services.StreamEntrypoint, error)
	TemplateEntrypoint(ctx context.Context, projectID, templateID uuid.UUID) (*services.StreamEntrypoint, error)
}
```

and below `var _ StreamServiceInterface = (*services.StreamService)(nil)`:

```go
var _ StreamViewServiceInterface = (*services.StreamViewService)(nil)
```

In `internal/handlers/stream_handler.go`, replace the `StreamHandler` struct and `NewStreamHandler` with:

```go
// StreamHandler handles L4 stream endpoints.
type StreamHandler struct {
	streamService StreamServiceInterface
	views         StreamViewServiceInterface
	auditService  AuditServiceInterface
	permChecker   *middleware.PermissionChecker
}

// NewStreamHandler creates a new stream handler.
func NewStreamHandler(streamService StreamServiceInterface, views StreamViewServiceInterface, auditService AuditServiceInterface, permChecker *middleware.PermissionChecker) *StreamHandler {
	return &StreamHandler{streamService: streamService, views: views, auditService: auditService, permChecker: permChecker}
}
```

and append:

```go
// GetEntrypoint returns the stream's entry point: dedicated or shared IP, the
// address, who shares it, and the port map (taken and reserved ports).
func (h *StreamHandler) GetEntrypoint(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}
	stream, ok := h.loadStream(c, projectID)
	if !ok {
		return
	}
	ep, err := h.views.StreamEntrypoint(c.Request.Context(), stream)
	if err != nil {
		streamError(c, err)
		return
	}
	c.JSON(http.StatusOK, ep)
}

// GetTemplateEntrypoint returns the entry point a new stream on the gateway
// template would join (the stream create form shows it before a stream
// exists). A template of another project is reported as not found.
func (h *StreamHandler) GetTemplateEntrypoint(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}
	templateID, err := uuid.Parse(c.Param("domainTemplateId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid domain template ID"})
		return
	}
	ep, err := h.views.TemplateEntrypoint(c.Request.Context(), projectID, templateID)
	if err != nil {
		streamError(c, err)
		return
	}
	c.JSON(http.StatusOK, ep)
}
```

In `cmd/server/main.go`, replace `streamHandler := handlers.NewStreamHandler(streamService, auditService, permChecker)` with:

```go
	streamViewService := services.NewStreamViewService(services.StreamViewServiceDeps{
		Streams:   streamRepo,
		Templates: domainTemplateRepo,
		Domains:   domainRepo,
		Gateways:  k8sService,
	})
	streamHandler := handlers.NewStreamHandler(streamService, streamViewService, auditService, permChecker)
```

In `setupRouter`, below `domainTemplatesRead.GET("/:domainTemplateId/domains", deps.DomainTemplateHandler.ListDomains)` add:

```go
					// The entry point a new stream on this template would join.
					domainTemplatesRead.GET("/:domainTemplateId/entrypoint", deps.StreamHandler.GetTemplateEntrypoint)
```

and below `streams.GET("/:streamId/metrics", deps.MetricsHandler.GetStreamMetrics)` add:

```go
					// Read-only entry point: mode, address, who shares it, port map.
					streams.GET("/:streamId/entrypoint", deps.StreamHandler.GetEntrypoint)
```

OpenAPI. Append to `docs/openapi/schemas/stream.yaml`:

```yaml
StreamEntrypoint:
  type: object
  description: |
    The L4 entry point a stream lands on. `dedicated` (template mergeGateways
    false): the stream has its own Envoy, Service and IP. `shared` (template
    mergeGateways true): the stream is a named group of ports on the
    template's one shared Envoy; ports are unique across every stream and
    domain on it. The port map uses the same sources as the create-time
    port check, so a port absent from `ports` and `reserved` and inside
    `portRange` is accepted by create.
  required: [mode, templateId, templateName, gatewayClass, address, portRange, sharedWith, ports, reserved]
  properties:
    mode:
      type: string
      enum: [dedicated, shared]
    templateId:
      type: string
      format: uuid
    templateName:
      type: string
    gatewayClass:
      type: string
      description: The template's GatewayClass name.
    address:
      type: string
      description: |
        The Gateway's first status address. Dedicated - this stream's;
        shared - this stream's, else any member stream's. Empty when none is
        assigned yet (assigned on first deploy). The template variant is
        always empty when dedicated.
    portRange:
      allOf:
        - $ref: '#/EntrypointPortRange'
      nullable: true
      description: The template's TCP/UDP range, or null when it has none.
    sharedWith:
      $ref: '#/EntrypointSharedWith'
    ports:
      type: array
      description: |
        Taken (transport, port) pairs with their owner, ordered by port.
        Dedicated - this stream's L4 routes (empty for the template variant);
        shared - every stream's L4 routes on the template plus every domain's
        bound listener ports. Rejected routes hold no port.
      items:
        $ref: '#/EntrypointPort'
    reserved:
      type: array
      description: |
        Ports no route may claim. 19000/19001 (envoy_internal) and 60000
        (placeholder) for both TCP and UDP, plus - when shared - the
        template's hostname-routed listener ports as TCP (template_listener).
      items:
        $ref: '#/EntrypointReservedPort'

EntrypointPortRange:
  type: object
  required: [min, max]
  properties:
    min:
      type: integer
    max:
      type: integer

EntrypointSharedWith:
  type: object
  description: Who else is on a shared entry point. Both lists are empty when dedicated; the stream variant excludes the stream itself.
  required: [streams, domains]
  properties:
    streams:
      type: array
      items:
        type: object
        required: [id, name]
        properties:
          id:
            type: string
            format: uuid
          name:
            type: string
    domains:
      type: array
      items:
        type: object
        required: [id, name, hostname]
        properties:
          id:
            type: string
            format: uuid
          name:
            type: string
          hostname:
            type: string

EntrypointPort:
  type: object
  required: [transport, port, owner]
  properties:
    transport:
      type: string
      enum: [TCP, UDP]
    port:
      type: integer
    owner:
      $ref: '#/EntrypointPortOwner'

EntrypointPortOwner:
  type: object
  description: kind stream_route sets the stream/route fields; kind domain sets the domain fields.
  required: [kind]
  properties:
    kind:
      type: string
      enum: [stream_route, domain]
    streamId:
      type: string
      format: uuid
    streamName:
      type: string
    routeId:
      type: string
      format: uuid
    routeName:
      type: string
    status:
      type: string
      description: The route's status (any status except rejected).
    domainId:
      type: string
      format: uuid
    domainName:
      type: string

EntrypointReservedPort:
  type: object
  required: [transport, port, reason]
  properties:
    transport:
      type: string
      enum: [TCP, UDP]
    port:
      type: integer
    reason:
      type: string
      enum: [envoy_internal, placeholder, template_listener]
```

Append to `docs/openapi/paths/streams.yaml`:

```yaml
streams_{streamId}_entrypoint:
  get:
    tags: [Streams]
    summary: Get a stream's entry point (mode, address, who shares it, port map)
    operationId: getStreamEntrypoint
    security:
      - bearerAuth: []
    parameters:
      - $ref: '../components/parameters.yaml#/projectId'
      - $ref: '../components/parameters.yaml#/streamId'
    responses:
      '200':
        description: The stream's entry point
        content:
          application/json:
            schema:
              $ref: '../schemas/stream.yaml#/StreamEntrypoint'
      '404':
        $ref: '../components/responses.yaml#/NotFound'
      '500':
        description: The stream's gateway template, port sources or Gateway could not be read (the mode is never guessed)
        content:
          application/json:
            schema:
              $ref: '../schemas/common.yaml#/Error'
```

Append to `docs/openapi/paths/domain-templates.yaml`:

```yaml
domain-templates_{id}_entrypoint:
  get:
    tags: [Domain Templates]
    summary: Get the entry point a new stream on this template would join
    description: |
      The stream create form's view of a template before a stream exists.
      Same builder as the stream entry point; a dedicated template reports an
      empty port map and no address.
    operationId: getDomainTemplateEntrypoint
    security:
      - bearerAuth: []
    parameters:
      - $ref: '../components/parameters.yaml#/projectId'
      - $ref: '../components/parameters.yaml#/domainTemplateId'
    responses:
      '200':
        description: The template's entry point
        content:
          application/json:
            schema:
              $ref: '../schemas/stream.yaml#/StreamEntrypoint'
      '400':
        $ref: '../components/responses.yaml#/BadRequest'
      '404':
        description: Gateway Template not found (including templates of other projects)
        content:
          application/json:
            schema:
              $ref: '../schemas/common.yaml#/Error'
```

In `docs/openapi/openapi.yaml`, under `paths:` add after the `.../domain-templates/{domainTemplateId}/domains` entry and after the `.../streams/{streamId}/metrics` entry respectively:

```yaml
  /projects/{projectId}/domain-templates/{domainTemplateId}/entrypoint:
    $ref: 'paths/domain-templates.yaml#/domain-templates_{id}_entrypoint'
```

```yaml
  /projects/{projectId}/streams/{streamId}/entrypoint:
    $ref: 'paths/streams.yaml#/streams_{streamId}_entrypoint'
```

and under `components: schemas:` in the `# ============== STREAM ==============` block, after `UpdateStreamRequest`:

```yaml
    StreamEntrypoint:
      $ref: 'schemas/stream.yaml#/StreamEntrypoint'
```

Regenerate: `make mocks && make openapi`

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/handlers/ ./cmd/server/ -count=1 && make openapi-check`
Expected: `ok` for both packages (`TestRouteSpecParity` passes with the two new routes). `openapi-check` reports no difference.

- [ ] **Step 5: Commit**

```bash
git add internal/handlers/ cmd/server/main.go cmd/server/openapi.yaml docs/openapi/ internal/mocks/
git commit -m "feat(api): stream and template entry point endpoints

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
make mocks-check
```

---

### Task 5: Stream list enrichment (no N+1)

**Files:**
- Modify: `internal/services/stream_view.go` (append `StreamListItem`, `StreamPort`, `ListWithSummary`)
- Modify: `internal/handlers/service_interfaces.go` (`ListWithSummary` on `StreamViewServiceInterface`)
- Modify: `internal/handlers/stream_handler.go` (`List`)
- Modify: `internal/handlers/stream_handler_test.go` (replace `TestStreamHandler_List_Success`, add a failure test)
- Modify: `docs/openapi/paths/streams.yaml`, `docs/openapi/schemas/stream.yaml`, `docs/openapi/openapi.yaml`; regenerate `cmd/server/openapi.yaml`
- Regenerate: `internal/mocks/mock_handler_services.go`
- Test: `internal/services/stream_view_test.go` (append)

**Interfaces:**
- Consumes: Task 3 `StreamViewService`, `StreamViewStore.ListByProjectID`, `StreamViewStore.L4PortOwnersByStreamIDs`, `entrypointMode`; the `portWorld` fixture (its `ownerCalls` counters).
- Produces: `services.StreamListItem{models.Stream (embedded); Mode string "mode"; GatewayTemplateName string "gatewayTemplateName"; RouteCount int "routeCount"; Ports []StreamPort "ports"}`, `services.StreamPort{Transport "transport"; Port "port"}`, `(*StreamViewService).ListWithSummary(projectID uuid.UUID) ([]StreamListItem, error)`. `GET /projects/:projectId/streams` returns `{"data": []StreamListItem}`.

- [ ] **Step 1: Write the failing test**

Append to `internal/services/stream_view_test.go`:

```go
// countingTemplates is a StreamTemplateReader that counts reads per template.
type countingTemplates struct {
	byID  map[uuid.UUID]*models.DomainTemplate
	calls map[uuid.UUID]int
}

func (c *countingTemplates) GetByID(id uuid.UUID) (*models.DomainTemplate, error) {
	c.calls[id]++
	if t, ok := c.byID[id]; ok {
		return t, nil
	}
	return nil, gorm.ErrRecordNotFound
}

func TestStreamListWithSummary(t *testing.T) {
	w, self, cache, fresh := newPortWorld(true)
	dedicated := &models.DomainTemplate{ID: uuid.New(), ProjectID: w.tmpl.ProjectID, Name: "edge-l4"}
	solo := models.Stream{ID: uuid.New(), ProjectID: w.tmpl.ProjectID, Name: "solo", GatewayTemplateID: dedicated.ID}
	w.streams = append(w.streams, solo)
	w.routes = append(w.routes, worldRoute{id: uuid.New(), streamID: solo.ID, name: "mqtt", transport: "TCP", port: 1883, status: models.RouteStatusActive})
	templates := &countingTemplates{byID: map[uuid.UUID]*models.DomainTemplate{w.tmpl.ID: w.tmpl, dedicated.ID: dedicated}, calls: map[uuid.UUID]int{}}
	svc := services.NewStreamViewService(services.StreamViewServiceDeps{Streams: w, Templates: templates, Domains: w, Gateways: &fakeGatewayReader{}})

	items, err := svc.ListWithSummary(w.tmpl.ProjectID)
	require.NoError(t, err)

	require.Len(t, items, 4)
	byName := map[string]services.StreamListItem{}
	for _, it := range items {
		byName[it.Name] = it
	}
	assert.Equal(t, []string{"cache", "fresh", "solo", "test-stream"}, []string{items[0].Name, items[1].Name, items[2].Name, items[3].Name})

	mine := byName["test-stream"]
	assert.Equal(t, self.ID, mine.ID)
	assert.Equal(t, services.EntrypointModeShared, mine.Mode)
	assert.Equal(t, "shared-envoy", mine.GatewayTemplateName)
	assert.Equal(t, 2, mine.RouteCount, "the rejected route is not counted")
	assert.Equal(t, []services.StreamPort{{Transport: "TCP", Port: 19100}, {Transport: "UDP", Port: 19100}}, mine.Ports)

	assert.Equal(t, cache.ID, byName["cache"].ID)
	assert.Equal(t, 2, byName["cache"].RouteCount)
	assert.Equal(t, fresh.ID, byName["fresh"].ID)
	assert.Equal(t, 0, byName["fresh"].RouteCount)
	assert.Equal(t, []services.StreamPort{}, byName["fresh"].Ports)
	assert.Equal(t, services.EntrypointModeDedicated, byName["solo"].Mode)
	assert.Equal(t, "edge-l4", byName["solo"].GatewayTemplateName)

	// No N+1: one grouped route query, one read per distinct template.
	assert.Equal(t, 1, w.ownerCalls.byStreamIDs)
	assert.Zero(t, w.ownerCalls.byStream)
	assert.Zero(t, w.ownerCalls.byTemplate)
	assert.Equal(t, map[uuid.UUID]int{w.tmpl.ID: 1, dedicated.ID: 1}, templates.calls)
}

func TestStreamListWithSummary_EmptyProject(t *testing.T) {
	w, _, _, _ := newPortWorld(false)
	items, err := newViewService(w, &fakeGatewayReader{}).ListWithSummary(uuid.New())
	require.NoError(t, err)
	assert.Equal(t, []services.StreamListItem{}, items)
	assert.Zero(t, w.ownerCalls.byStreamIDs, "no streams, no route query")
}

func TestStreamListWithSummary_TemplateFailureIsAnError(t *testing.T) {
	w, _, _, _ := newPortWorld(false)
	svc := services.NewStreamViewService(services.StreamViewServiceDeps{
		Streams: w, Templates: &fakeTemplateReader{err: errors.New("db down")}, Domains: w, Gateways: &fakeGatewayReader{},
	})
	items, err := svc.ListWithSummary(w.tmpl.ProjectID)
	assert.Nil(t, items, "never a list with guessed modes")
	assert.ErrorContains(t, err, "db down")
}

func TestStreamListItem_JSONContract(t *testing.T) {
	id := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	raw, err := json.Marshal(services.StreamListItem{
		Stream: models.Stream{ID: id, Name: "test-stream", Status: "active"},
		Mode:   "shared", GatewayTemplateName: "shared-envoy", RouteCount: 1,
		Ports: []services.StreamPort{{Transport: "TCP", Port: 5432}},
	})
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, "11111111-1111-1111-1111-111111111111", got["id"])
	assert.Equal(t, "test-stream", got["name"])
	assert.Equal(t, "active", got["status"])
	assert.Equal(t, "shared", got["mode"])
	assert.Equal(t, "shared-envoy", got["gatewayTemplateName"])
	assert.Equal(t, float64(1), got["routeCount"])
	assert.Equal(t, []any{map[string]any{"transport": "TCP", "port": float64(5432)}}, got["ports"])
}
```

In `internal/handlers/stream_handler_test.go`, replace `TestStreamHandler_List_Success` with the two tests below. Add `"errors"` to that file's imports if it is not there yet (keep the import block gofmt-sorted):

```go
func TestStreamHandler_List_Success(t *testing.T) {
	views := new(mocks.MockStreamViewService)
	h := handlers.NewStreamHandler(new(mocks.MockStreamService), views, new(mocks.MockAuditService), domainPermChecker())
	projectID := uuid.New()
	views.On("ListWithSummary", projectID).Return([]services.StreamListItem{
		{Stream: models.Stream{ID: uuid.New(), Name: "a", Namespace: "ns"}, Mode: "shared", GatewayTemplateName: "shared-envoy",
			RouteCount: 1, Ports: []services.StreamPort{{Transport: "TCP", Port: 5432}}},
		{Stream: models.Stream{ID: uuid.New(), Name: "b", Namespace: "ns"}, Mode: "dedicated", GatewayTemplateName: "edge-l4",
			Ports: []services.StreamPort{}},
	}, nil)

	w := doStream(streamRouter(h, testUser()), "GET", "/projects/"+projectID.String()+"/streams", nil)

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Data []map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Data, 2)
	first := resp.Data[0]
	assert.Equal(t, "a", first["name"], "existing Stream fields stay at the top level")
	assert.Equal(t, "ns", first["namespace"])
	assert.Equal(t, "shared", first["mode"])
	assert.Equal(t, "shared-envoy", first["gatewayTemplateName"])
	assert.Equal(t, float64(1), first["routeCount"])
	assert.Equal(t, []any{map[string]any{"transport": "TCP", "port": float64(5432)}}, first["ports"])
	assert.Equal(t, []any{}, resp.Data[1]["ports"], "no routes is an empty list, never null")
}

func TestStreamHandler_List_SummaryFailureIs500(t *testing.T) {
	views := new(mocks.MockStreamViewService)
	h := handlers.NewStreamHandler(new(mocks.MockStreamService), views, new(mocks.MockAuditService), domainPermChecker())
	projectID := uuid.New()
	views.On("ListWithSummary", projectID).Return(nil, errors.New("failed to load gateway template"))

	w := doStream(streamRouter(h, testUser()), "GET", "/projects/"+projectID.String()+"/streams", nil)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/services/ -run 'TestStreamList' -count=1`
Expected: FAIL to build: `svc.ListWithSummary undefined (type *services.StreamViewService has no field or method ListWithSummary)`, `undefined: services.StreamListItem`.

- [ ] **Step 3: Write minimal implementation**

Append to `internal/services/stream_view.go`:

```go
// StreamListItem is one row of GET /projects/:projectId/streams: the stream
// plus its entry point mode, template name and the ports its routes hold.
// The embedded Stream keeps every existing field at the top level.
type StreamListItem struct {
	models.Stream
	Mode                string       `json:"mode"`
	GatewayTemplateName string       `json:"gatewayTemplateName"`
	RouteCount          int          `json:"routeCount"`
	Ports               []StreamPort `json:"ports"`
}

// StreamPort is a (transport, port) one of a stream's L4 routes holds.
type StreamPort struct {
	Transport string `json:"transport"`
	Port      int    `json:"port"`
}

// ListWithSummary lists the project's streams, ordered by name, each with
// its mode, template name, non-rejected L4 route count and their ports. It
// issues one grouped route query and one template read per distinct
// template - never a query per stream. A template lookup failure is an
// error: the mode is never guessed.
func (s *StreamViewService) ListWithSummary(projectID uuid.UUID) ([]StreamListItem, error) {
	streams, err := s.streams.ListByProjectID(projectID)
	if err != nil {
		return nil, err
	}
	items := make([]StreamListItem, 0, len(streams))
	if len(streams) == 0 {
		return items, nil
	}

	ids := make([]uuid.UUID, 0, len(streams))
	for _, st := range streams {
		ids = append(ids, st.ID)
	}
	owners, err := s.streams.L4PortOwnersByStreamIDs(ids)
	if err != nil {
		return nil, fmt.Errorf("failed to load stream ports: %w", err)
	}
	portsByStream := make(map[uuid.UUID][]StreamPort, len(streams))
	for _, o := range owners {
		portsByStream[o.StreamID] = append(portsByStream[o.StreamID], StreamPort{Transport: o.Transport, Port: o.Port})
	}

	templates := make(map[uuid.UUID]*models.DomainTemplate)
	for _, st := range streams {
		tmpl, ok := templates[st.GatewayTemplateID]
		if !ok {
			tmpl, err = s.templates.GetByID(st.GatewayTemplateID)
			if err != nil {
				return nil, fmt.Errorf("failed to load gateway template %s for stream %q: %w", st.GatewayTemplateID, st.Name, err)
			}
			templates[st.GatewayTemplateID] = tmpl
		}
		ports := portsByStream[st.ID]
		if ports == nil {
			ports = []StreamPort{}
		}
		sort.Slice(ports, func(i, j int) bool {
			if ports[i].Port != ports[j].Port {
				return ports[i].Port < ports[j].Port
			}
			return ports[i].Transport < ports[j].Transport
		})
		items = append(items, StreamListItem{
			Stream:              st,
			Mode:                entrypointMode(tmpl),
			GatewayTemplateName: tmpl.Name,
			RouteCount:          len(ports),
			Ports:               ports,
		})
	}
	return items, nil
}
```

In `internal/handlers/service_interfaces.go`, add to `StreamViewServiceInterface`:

```go
	ListWithSummary(projectID uuid.UUID) ([]services.StreamListItem, error)
```

In `internal/handlers/stream_handler.go`, replace `List` with:

```go
// List lists streams in a project, each with its entry point mode, template
// name, route count and ports.
func (h *StreamHandler) List(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}
	streams, err := h.views.ListWithSummary(projectID)
	if err != nil {
		streamError(c, err)
		return
	}
	if streams == nil {
		streams = []services.StreamListItem{}
	}
	c.JSON(http.StatusOK, gin.H{"data": streams})
}
```

OpenAPI. In `docs/openapi/paths/streams.yaml`, under `streams: get: responses:`, change the `'200'` item schema from `$ref: '../schemas/stream.yaml#/Stream'` to `$ref: '../schemas/stream.yaml#/StreamListItem'`, change its description to `List of streams, ordered by name, each with its entry point mode, template name, route count and ports`, and add after the `'200'` response:

```yaml
      '500':
        description: A stream's gateway template or the route ports could not be read (modes are never guessed)
        content:
          application/json:
            schema:
              $ref: '../schemas/common.yaml#/Error'
```

Append to `docs/openapi/schemas/stream.yaml`:

```yaml
StreamListItem:
  description: A stream as listed, with the fields the stream list shows. Every Stream field is unchanged.
  allOf:
    - $ref: '#/Stream'
    - type: object
      required: [mode, gatewayTemplateName, routeCount, ports]
      properties:
        mode:
          type: string
          enum: [dedicated, shared]
          description: shared if and only if the stream's gateway template merges gateways.
        gatewayTemplateName:
          type: string
        routeCount:
          type: integer
          description: Non-rejected L4 routes on the stream.
        ports:
          type: array
          description: (transport, port) of each non-rejected L4 route, ordered by port.
          items:
            $ref: '#/StreamPort'

StreamPort:
  type: object
  required: [transport, port]
  properties:
    transport:
      type: string
      enum: [TCP, UDP]
    port:
      type: integer
```

In `docs/openapi/openapi.yaml` `components: schemas:`, after `StreamEntrypoint`:

```yaml
    StreamListItem:
      $ref: 'schemas/stream.yaml#/StreamListItem'
```

Regenerate: `make mocks && make openapi`

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/services/ -run 'TestStreamList' -count=1 -v && go test ./internal/handlers/ ./cmd/server/ -count=1 && make openapi-check`
Expected: PASS (`TestStreamListWithSummary` asserts one `L4PortOwnersByStreamIDs` call, zero per-stream calls, and one template read per distinct template); `openapi-check` clean.

- [ ] **Step 5: Commit**

```bash
git add internal/services/stream_view.go internal/services/stream_view_test.go internal/handlers/ cmd/server/openapi.yaml docs/openapi/ internal/mocks/
git commit -m "feat(streams): list streams with mode, template, route count and ports

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
make mocks-check
```

---

### Task 6: L4 BackendTrafficPolicy YAML + route `/yamls` under the stream

**Files:**
- Modify: `internal/services/route_yaml.go` (`GenerateYAMLs` L4 branch; split `generateL4RouteYAML` into `l4Stream` + `renderL4RouteYAML`)
- Create: `internal/services/route_yaml_l4.go`
- Modify: `internal/handlers/route_stream_handler.go` (`GetYAMLsForStream`)
- Modify: `cmd/server/main.go` (route)
- Modify: `docs/openapi/paths/stream-routes.yaml`, `docs/openapi/openapi.yaml`; regenerate `cmd/server/openapi.yaml`
- Test: `internal/services/route_yaml_l4_test.go` (new), `internal/handlers/route_stream_yamls_handler_test.go` (new)

**Interfaces:**
- Consumes: `routeplan.BuildBackendTrafficPolicyConfigForNamespace` (the deploy path's builder, `route_deploy_policies.go:196`), `kubernetes.BuildBackendTrafficPolicy` (what `cluster.UpdateBackendTrafficPolicy` applies), the existing `routeQuery.streams`, `routeQuery.capabilities`, `routeQuery.backendTrafficPolicyRepo`. Test helpers: `l4YAMLFixture`/`newTestRouteServiceWith` (`route_yaml_test.go`, `route_service_test.go`), `tcpBTPConfig` (`route_deploy_l4_test.go`), `routePermChecker`, `doJSON`.
- Produces (Tasks 7, 8, 10 use these):
  - `func generateL4BackendTrafficPolicyYAML(route *models.Route, stream *models.Stream, policy *models.BackendTrafficPolicy) (string, error)` returns `""` exactly when deploy applies none (the spec names this helper);
  - `func l4PolicyFromConfig(route *models.Route, cfg *models.BackendTrafficPolicyConfig) *models.BackendTrafficPolicy`;
  - `func (q *routeQuery) l4Stream(route *models.Route) (*models.Stream, error)`, `func (q *routeQuery) renderL4RouteYAML(route *models.Route, stream *models.Stream) (string, error)`, `func (q *routeQuery) storedBackendTrafficPolicy(routeID uuid.UUID) (*models.BackendTrafficPolicy, error)` (`gorm.ErrRecordNotFound` or `(nil, nil)` → `nil, nil`, any other error → error), `func (q *routeQuery) generateL4RouteYAMLs(route *models.Route) (*RouteYAMLs, error)`;
  - exported `(*RouteService).RenderL4RouteYAML(route, stream) (string, error)` and `(*RouteService).RenderL4BackendTrafficPolicyYAML(route, stream, cfg *models.BackendTrafficPolicyConfig) (string, error)`;
  - handler `(*RouteHandler).GetYAMLsForStream`; route `GET /projects/:projectId/streams/:streamId/routes/:routeId/yamls`.

- [ ] **Step 1: Write the failing test**

Create `internal/services/route_yaml_l4_test.go`:

```go
package services_test

import (
	"errors"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// newL4YAMLFixtureWithBTP is newL4YAMLFixture with a BackendTrafficPolicy
// repository the test controls.
func newL4YAMLFixtureWithBTP(t *testing.T, btpRepo *mocks.MockBackendTrafficPolicyRepository) *l4YAMLFixture {
	t.Helper()
	f := &l4YAMLFixture{streamRepo: new(mocks.MockStreamReader)}
	f.svc, f.routeRepo, _, _, _, _ = newTestRouteServiceWith(func(d *services.RouteServiceDeps) {
		d.Streams = f.streamRepo
		d.BackendTrafficPolicyRepo = btpRepo
	})
	f.stream = &models.Stream{
		ID: uuid.New(), ProjectID: uuid.New(), Name: "db", Namespace: "fastgateway-system",
		K8sGatewayName: "str-db", K8sGatewayClass: "public-lb",
	}
	f.streamRepo.On("GetByID", f.stream.ID).Return(f.stream, nil)
	return f
}

func TestRouteYAML_L4_GenerateYAMLs_IncludesBackendTrafficPolicy(t *testing.T) {
	btpRepo := new(mocks.MockBackendTrafficPolicyRepository)
	f := newL4YAMLFixtureWithBTP(t, btpRepo)
	route := f.route(models.RouteProtocolTCP, 5432)
	btpRepo.On("GetByRouteID", route.ID).Return(&models.BackendTrafficPolicy{ID: uuid.New(), RouteID: &route.ID, Config: tcpBTPConfig()}, nil)

	yamls, err := f.svc.GenerateYAMLs(route.ID)
	require.NoError(t, err)

	assert.Contains(t, yamls.HTTPRouteYAML, "kind: TCPRoute", "the route stays in httpRouteYaml")
	assert.Contains(t, yamls.BackendTrafficPolicyYAML, "kind: BackendTrafficPolicy")
	assert.Contains(t, yamls.BackendTrafficPolicyYAML, "kind: TCPRoute", "targetRef is the TCPRoute")
	assert.Contains(t, yamls.BackendTrafficPolicyYAML, "name: pg-abcd1234")
	assert.Contains(t, yamls.BackendTrafficPolicyYAML, "namespace: fastgateway-system", "the policy lives in the stream namespace")
	assert.Contains(t, yamls.BackendTrafficPolicyYAML, "maxConnections: 100")
	assert.Empty(t, yamls.SecurityPolicyYAML)
}

func TestRouteYAML_L4_GenerateYAMLs_NoPolicyRecordIsEmpty(t *testing.T) {
	btpRepo := new(mocks.MockBackendTrafficPolicyRepository)
	f := newL4YAMLFixtureWithBTP(t, btpRepo)
	route := f.route(models.RouteProtocolUDP, 5353)
	btpRepo.On("GetByRouteID", route.ID).Return(nil, gorm.ErrRecordNotFound)

	yamls, err := f.svc.GenerateYAMLs(route.ID)
	require.NoError(t, err)
	assert.Contains(t, yamls.HTTPRouteYAML, "kind: UDPRoute")
	assert.Empty(t, yamls.BackendTrafficPolicyYAML)
}

func TestRouteYAML_L4_GenerateYAMLs_PolicyLookupFailureIsAnError(t *testing.T) {
	btpRepo := new(mocks.MockBackendTrafficPolicyRepository)
	f := newL4YAMLFixtureWithBTP(t, btpRepo)
	route := f.route(models.RouteProtocolTCP, 5432)
	btpRepo.On("GetByRouteID", route.ID).Return(nil, errors.New("connection reset"))

	yamls, err := f.svc.GenerateYAMLs(route.ID)
	assert.Nil(t, yamls, "never a manifest set with the policy silently missing")
	assert.ErrorContains(t, err, "connection reset")
}

func TestRouteService_RenderL4BackendTrafficPolicyYAML(t *testing.T) {
	svc, _, _, _, _, _ := newTestRouteService()
	stream := &models.Stream{ID: uuid.New(), Namespace: "edge", K8sGatewayName: "str-edge"}
	route := &models.Route{ID: uuid.New(), Name: "pg", K8sRouteName: "pg-abcd1234", Protocol: models.RouteProtocolTCP}

	none, err := svc.RenderL4BackendTrafficPolicyYAML(route, stream, nil)
	require.NoError(t, err)
	assert.Empty(t, none)

	empty, err := svc.RenderL4BackendTrafficPolicyYAML(route, stream, &models.BackendTrafficPolicyConfig{})
	require.NoError(t, err)
	assert.Empty(t, empty, "an empty policy is not applied, so it is not shown")

	cfg := tcpBTPConfig()
	out, err := svc.RenderL4BackendTrafficPolicyYAML(route, stream, &cfg)
	require.NoError(t, err)
	assert.Contains(t, out, "namespace: edge")
	assert.Contains(t, out, "kind: TCPRoute")

	routeYAML, err := svc.RenderL4RouteYAML(route, stream)
	require.NoError(t, err)
	assert.Contains(t, routeYAML, "kind: TCPRoute")
	assert.Contains(t, routeYAML, "name: str-edge")
}
```

Create `internal/handlers/route_stream_yamls_handler_test.go` with the two `GetYAMLsForStream` tests (Task 7 appends a third test to this file):

```go
package handlers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/handlers"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func routeYAMLsRouter(h *handlers.RouteHandler) *gin.Engine {
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("user", testUser()) })
	router.GET("/projects/:projectId/streams/:streamId/routes/:routeId/yamls", h.GetYAMLsForStream)
	return router
}

func TestRouteHandler_GetYAMLsForStream_Success(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewRouteHandler(mockRoute, new(mocks.MockAuditService), routePermChecker())
	projectID, streamID, routeID := uuid.New(), uuid.New(), uuid.New()
	mockRoute.On("GetForStream", projectID, streamID, routeID).Return(&models.Route{ID: routeID, StreamID: &streamID}, nil)
	mockRoute.On("GenerateYAMLs", routeID).Return(&services.RouteYAMLs{
		HTTPRouteYAML: "kind: TCPRoute\n", BackendTrafficPolicyYAML: "kind: BackendTrafficPolicy\n",
	}, nil)

	w := doJSON(routeYAMLsRouter(h), "GET", fmt.Sprintf("/projects/%s/streams/%s/routes/%s/yamls", projectID, streamID, routeID), nil)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "kind: TCPRoute\n", body["httpRouteYaml"])
	assert.Equal(t, "kind: BackendTrafficPolicy\n", body["backendTrafficPolicyYaml"])
}

func TestRouteHandler_GetYAMLsForStream_RouteOfOtherStreamIsNotFound(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewRouteHandler(mockRoute, new(mocks.MockAuditService), routePermChecker())
	projectID, streamID, routeID := uuid.New(), uuid.New(), uuid.New()
	mockRoute.On("GetForStream", projectID, streamID, routeID).Return(nil, services.ErrRouteNotFound)

	w := doJSON(routeYAMLsRouter(h), "GET", fmt.Sprintf("/projects/%s/streams/%s/routes/%s/yamls", projectID, streamID, routeID), nil)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockRoute.AssertNotCalled(t, "GenerateYAMLs", mock.Anything)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/services/ -run 'TestRouteYAML_L4|TestRouteService_RenderL4' -count=1`
Expected: FAIL to build: `svc.RenderL4BackendTrafficPolicyYAML undefined (type *services.RouteService has no field or method RenderL4BackendTrafficPolicyYAML)`. (Once it builds, `TestRouteYAML_L4_GenerateYAMLs_IncludesBackendTrafficPolicy` fails with an empty `BackendTrafficPolicyYAML`.)

- [ ] **Step 3: Write minimal implementation**

In `internal/services/route_yaml.go`, replace the L4 branch at the top of `GenerateYAMLs`:

```go
	// L4 routes have no Domain and none of the HTTP-only policy resources: the
	// preview is just the TCPRoute/UDPRoute. Must precede the *route.DomainID deref.
	if route.IsL4() {
		l4YAML, err := q.generateL4RouteYAML(route)
		if err != nil {
			return nil, err
		}
		return &RouteYAMLs{HTTPRouteYAML: l4YAML}, nil
	}
```

with:

```go
	// L4 routes have no Domain and none of the HTTP-only policy resources:
	// the manifests are the TCPRoute/UDPRoute (in httpRouteYaml) and the
	// BackendTrafficPolicy deployL4 applies. Must precede the *route.DomainID deref.
	if route.IsL4() {
		return q.generateL4RouteYAMLs(route)
	}
```

and replace the whole of `generateL4RouteYAML` with these three functions. Rendering is unchanged; it only moves into `renderL4RouteYAML`:

```go
// generateL4RouteYAML renders the TCPRoute/UDPRoute (v1 or v1alpha2) for an L4 route.
// The route's Stream (not a Domain) supplies the namespace and parent Gateway.
func (q *routeQuery) generateL4RouteYAML(route *models.Route) (string, error) {
	stream, err := q.l4Stream(route)
	if err != nil {
		return "", err
	}
	return q.renderL4RouteYAML(route, stream)
}

// l4Stream loads the Stream an L4 route belongs to.
func (q *routeQuery) l4Stream(route *models.Route) (*models.Stream, error) {
	if route.StreamID == nil {
		return nil, fmt.Errorf("L4 route %s has no stream", route.ID)
	}
	stream, err := q.streams.GetByID(*route.StreamID)
	if err != nil {
		return nil, fmt.Errorf("failed to load stream for L4 route: %w", err)
	}
	return stream, nil
}

// renderL4RouteYAML renders route's TCPRoute/UDPRoute on stream through the
// deploy builders (routeplan.BuildTCPRouteConfig / BuildUDPRouteConfig and
// kubernetes.BuildTCPRouteObject / BuildUDPRouteObject).
func (q *routeQuery) renderL4RouteYAML(route *models.Route, stream *models.Stream) (string, error) {
	// Match what deploy applies: v1 when the project's cluster supports it.
	apiVersion := "gateway.networking.k8s.io/v1alpha2"
	if q.capabilities.Has(context.Background(), stream.ProjectID, capabilities.CapL4RouteV1) {
		apiVersion = "gateway.networking.k8s.io/v1"
	}

	var obj any
	switch route.Protocol {
	case models.RouteProtocolTCP:
		obj = kubernetes.BuildTCPRouteObject(routeplan.BuildTCPRouteConfig(*route, *stream), apiVersion)
	case models.RouteProtocolUDP:
		obj = kubernetes.BuildUDPRouteObject(routeplan.BuildUDPRouteConfig(*route, *stream), apiVersion)
	default:
		return "", fmt.Errorf("unsupported L4 protocol %q", route.Protocol)
	}

	yamlBytes, err := yaml.Marshal(obj)
	if err != nil {
		return "", fmt.Errorf("marshal L4 route YAML: %w", err)
	}
	return string(yamlBytes), nil
}
```

Create `internal/services/route_yaml_l4.go`:

```go
package services

import (
	"errors"
	"fmt"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"sigs.k8s.io/yaml"
)

// generateL4BackendTrafficPolicyYAML renders the BackendTrafficPolicy
// deployL4 applies for an L4 route: the deploy path's config builder
// (routeplan.BuildBackendTrafficPolicyConfigForNamespace with the stream's
// namespace and ID) and the object builder the cluster client applies
// (kubernetes.BuildBackendTrafficPolicy). It returns "" exactly when
// deployL4 applies none: no policy, or an empty one.
func generateL4BackendTrafficPolicyYAML(route *models.Route, stream *models.Stream, policy *models.BackendTrafficPolicy) (string, error) {
	cfg := routeplan.BuildBackendTrafficPolicyConfigForNamespace(route, stream.Namespace, stream.ID.String(), policy)
	if cfg == nil {
		return "", nil
	}
	obj := kubernetes.BuildBackendTrafficPolicy(cfg)
	if obj == nil {
		return "", nil
	}
	out, err := yaml.Marshal(obj.Object)
	if err != nil {
		return "", fmt.Errorf("marshal L4 BackendTrafficPolicy YAML: %w", err)
	}
	return string(out), nil
}

// l4PolicyFromConfig wraps a policy config (from an approval snapshot or a
// create/update input) in the model the deploy builder takes. nil in, nil out.
func l4PolicyFromConfig(route *models.Route, cfg *models.BackendTrafficPolicyConfig) *models.BackendTrafficPolicy {
	if cfg == nil {
		return nil
	}
	id := route.ID
	return &models.BackendTrafficPolicy{RouteID: &id, Config: *cfg}
}

// storedBackendTrafficPolicy returns the route's persisted policy, nil when
// it has none. Unlike the HTTP previews it does not swallow a lookup
// failure: a read endpoint returns an error rather than a manifest set with
// the policy silently missing.
func (q *routeQuery) storedBackendTrafficPolicy(routeID uuid.UUID) (*models.BackendTrafficPolicy, error) {
	policy, err := q.backendTrafficPolicyRepo.GetByRouteID(routeID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load backend traffic policy for route %s: %w", routeID, err)
	}
	return policy, nil
}

// generateL4RouteYAMLs is GenerateYAMLs for an L4 route: the TCPRoute/UDPRoute
// in HTTPRouteYAML (the response shape is unchanged) and its
// BackendTrafficPolicy.
func (q *routeQuery) generateL4RouteYAMLs(route *models.Route) (*RouteYAMLs, error) {
	stream, err := q.l4Stream(route)
	if err != nil {
		return nil, err
	}
	routeYAML, err := q.renderL4RouteYAML(route, stream)
	if err != nil {
		return nil, err
	}
	policy, err := q.storedBackendTrafficPolicy(route.ID)
	if err != nil {
		return nil, err
	}
	btpYAML, err := generateL4BackendTrafficPolicyYAML(route, stream, policy)
	if err != nil {
		return nil, err
	}
	return &RouteYAMLs{HTTPRouteYAML: routeYAML, BackendTrafficPolicyYAML: btpYAML}, nil
}

// RenderL4RouteYAML renders an L4 route's TCPRoute/UDPRoute on stream through
// the deploy builders. ApprovalService uses it for the L4 approval diff.
func (s *RouteService) RenderL4RouteYAML(route *models.Route, stream *models.Stream) (string, error) {
	return s.query.renderL4RouteYAML(route, stream)
}

// RenderL4BackendTrafficPolicyYAML renders the BackendTrafficPolicy deployL4
// would apply for route on stream with the given policy config ("" for none).
func (s *RouteService) RenderL4BackendTrafficPolicyYAML(route *models.Route, stream *models.Stream, cfg *models.BackendTrafficPolicyConfig) (string, error) {
	return generateL4BackendTrafficPolicyYAML(route, stream, l4PolicyFromConfig(route, cfg))
}
```

Append to `internal/handlers/route_stream_handler.go`:

```go
// GetYAMLsForStream gets an L4 route's manifests: the TCPRoute/UDPRoute (in
// httpRouteYaml) and its BackendTrafficPolicy.
func (h *RouteHandler) GetYAMLsForStream(c *gin.Context) {
	if h.requireStreamRoute(c) {
		h.GetYAMLs(c)
	}
}
```

In `cmd/server/main.go`, below `streamRoutes.GET("/:routeId/yaml", deps.RouteHandler.GetYAMLForStream)`:

```go
						streamRoutes.GET("/:routeId/yamls", deps.RouteHandler.GetYAMLsForStream)
```

Append to `docs/openapi/paths/stream-routes.yaml`:

```yaml
stream-route-yamls:
  get:
    tags: [Streams]
    summary: Get an L4 route's manifests (TCPRoute/UDPRoute and BackendTrafficPolicy)
    description: |
      The TCPRoute or UDPRoute is returned in `httpRouteYaml` (the same
      response shape as the domain route endpoint) and the
      BackendTrafficPolicy deploy applies in `backendTrafficPolicyYaml`
      (absent when the route has no policy). Rendered through the deploy
      builders.
    operationId: getStreamRouteYamls
    security:
      - bearerAuth: []
    parameters:
      - $ref: '../components/parameters.yaml#/projectId'
      - $ref: '../components/parameters.yaml#/streamId'
      - $ref: '../components/parameters.yaml#/routeId'
    responses:
      '200':
        description: Route YAMLs
        content:
          application/json:
            schema:
              $ref: '../schemas/route.yaml#/RouteYAMLsResponse'
      '404':
        $ref: '../components/responses.yaml#/NotFound'
      '500':
        description: The route's stream or policy could not be read
        content:
          application/json:
            schema:
              $ref: '../schemas/common.yaml#/Error'
```

In `docs/openapi/openapi.yaml` `paths:`, after the `.../routes/{routeId}/yaml` stream entry:

```yaml
  /projects/{projectId}/streams/{streamId}/routes/{routeId}/yamls:
    $ref: 'paths/stream-routes.yaml#/stream-route-yamls'
```

Regenerate: `make openapi` (no interface changed; `make mocks` is a no-op here).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/services/ -run 'TestRouteYAML|TestRouteService_RenderL4' -count=1 -v && go test ./internal/handlers/ -run 'GetYAMLsForStream' -count=1 && go test ./cmd/server/ -count=1 && make openapi-check`
Expected: PASS, including the unchanged `TestRouteYAML_L4_TCP_GenerateYAMLs` (no policy → `BackendTrafficPolicyYAML` empty). Then `go test ./internal/services/ -count=1` → `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/services/route_yaml.go internal/services/route_yaml_l4.go internal/services/route_yaml_l4_test.go internal/handlers/route_stream_handler.go internal/handlers/route_stream_yamls_handler_test.go cmd/server/main.go cmd/server/openapi.yaml docs/openapi/
git commit -m "feat(routes): L4 route manifests include the BackendTrafficPolicy

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
make mocks-check
```

---

### Task 7: Stream manifests (`GET /streams/:streamId/yamls`)

**Files:**
- Create: `internal/services/stream_yaml.go`
- Modify: `internal/handlers/service_interfaces.go` (`GenerateStreamYAMLs` on `RoutePreviewer`)
- Modify: `internal/handlers/route_stream_handler.go` (`GetStreamYAMLs`; add `"errors"` import)
- Modify: `cmd/server/main.go` (route)
- Modify: `docs/openapi/paths/streams.yaml`, `docs/openapi/schemas/stream.yaml`, `docs/openapi/openapi.yaml`; regenerate `cmd/server/openapi.yaml`
- Regenerate: `internal/mocks/mock_handler_services.go`
- Test: `internal/services/stream_yaml_test.go` (new), `internal/handlers/route_stream_yamls_handler_test.go` (append)

**Interfaces:**
- Consumes: Task 6 `renderL4RouteYAML`, `storedBackendTrafficPolicy`, `generateL4BackendTrafficPolicyYAML`; existing `routeQuery.streamInProject`, `routeRepo.ListActiveByStreamID` (the live set `deployL4` uses), `streamplan.BuildStreamGatewayConfig`, `kubernetes.BuildGatewayObject` (what `cluster.UpdateGateway` applies). Test fixture `l4Fixture` (`route_deploy_l4_test.go`: `newL4Fixture`, `route`, `withBTP`, `expectDeploy`).
- Produces: `services.StreamYAMLs{GatewayYAML "gatewayYaml"; Routes []StreamRouteYAMLs "routes"}`, `services.StreamRouteYAMLs{RouteID "routeId"; Name "name"; Protocol "protocol"; RouteYAML "routeYaml"; BackendTrafficPolicyYAML "backendTrafficPolicyYaml"}`, `(*RouteService).GenerateStreamYAMLs(projectID, streamID uuid.UUID) (*StreamYAMLs, error)`; handler `(*RouteHandler).GetStreamYAMLs` (`ErrStreamNotFound` → 404, others → 500); route `GET /projects/:projectId/streams/:streamId/yamls`.

- [ ] **Step 1: Write the failing test**

Create `internal/services/stream_yaml_test.go`. The first test is the byte-identical guard for the view: it runs a real `deployL4`, captures what it hands the cluster client, renders that with the client's own object builders, and requires `GenerateStreamYAMLs` to return the same bytes:

```go
package services_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

// TestStreamYAMLs_EqualWhatDeployL4Applies is the byte-identical guard for
// the view: deploy an update through the real deployL4 path, capture the
// GatewayConfig / TCPRouteConfig / BackendTrafficPolicyConfig it hands the
// cluster client, render them with the same object builders the client uses,
// and require GenerateStreamYAMLs to return exactly those bytes for the same
// live route set.
func TestStreamYAMLs_EqualWhatDeployL4Applies(t *testing.T) {
	f := newL4Fixture(t)
	current := f.route("pg", models.RouteProtocolTCP, 5432, models.RouteStatusPendingDeploy)
	other := f.route("dns", models.RouteProtocolUDP, 53, models.RouteStatusActive)
	f.withBTP(current, tcpBTPConfig())
	f.expectDeploy(current, models.ApprovalActionUpdate)
	f.btpRepo.On("GetByRouteID", other.ID).Return(nil, nil).Maybe()
	f.routeRepo.On("ListActiveByStreamID", f.stream.ID).Return([]models.Route{*current, *other}, nil)
	f.routeRepo.On("Update", mock.Anything).Return(nil)

	var gw *kubernetes.GatewayConfig
	f.k8s.On("UpdateGateway", mock.Anything, f.stream.ProjectID, mock.AnythingOfType("*kubernetes.GatewayConfig")).
		Run(func(args mock.Arguments) { gw = args.Get(2).(*kubernetes.GatewayConfig) }).Return(nil)
	var tcp *kubernetes.TCPRouteConfig
	f.k8s.On("UpdateTCPRoute", mock.Anything, f.stream.ProjectID, mock.AnythingOfType("*kubernetes.TCPRouteConfig"), kubernetes.TCPRouteGVR).
		Run(func(args mock.Arguments) { tcp = args.Get(2).(*kubernetes.TCPRouteConfig) }).Return(nil)
	var btp *kubernetes.BackendTrafficPolicyConfig
	f.k8s.On("UpdateBackendTrafficPolicy", mock.Anything, f.stream.ProjectID, mock.AnythingOfType("*kubernetes.BackendTrafficPolicyConfig")).
		Run(func(args mock.Arguments) { btp = args.Get(2).(*kubernetes.BackendTrafficPolicyConfig) }).Return(nil)

	_, err := f.svc.Deploy(current.ID, uuid.New())
	require.NoError(t, err)
	require.NotNil(t, gw)
	require.NotNil(t, tcp)
	require.NotNil(t, btp)

	yamls, err := f.svc.GenerateStreamYAMLs(f.stream.ProjectID, f.stream.ID)
	require.NoError(t, err)

	wantGateway, err := yaml.Marshal(kubernetes.BuildGatewayObject(gw).Object)
	require.NoError(t, err)
	assert.Equal(t, string(wantGateway), yamls.GatewayYAML, "gatewayYaml must be the object UpdateGateway applied")

	require.Len(t, yamls.Routes, 2)
	assert.Equal(t, current.ID, yamls.Routes[0].RouteID)
	assert.Equal(t, "pg", yamls.Routes[0].Name)
	assert.Equal(t, models.RouteProtocolTCP, yamls.Routes[0].Protocol)
	wantRoute, err := yaml.Marshal(kubernetes.BuildTCPRouteObject(*tcp, kubernetes.TCPRouteGVR.GroupVersion().String()))
	require.NoError(t, err)
	assert.Equal(t, string(wantRoute), yamls.Routes[0].RouteYAML, "routeYaml must be the TCPRoute UpdateTCPRoute applied")
	wantBTP, err := yaml.Marshal(kubernetes.BuildBackendTrafficPolicy(btp).Object)
	require.NoError(t, err)
	assert.Equal(t, string(wantBTP), yamls.Routes[0].BackendTrafficPolicyYAML, "the policy must be the one deploy applied")

	assert.Equal(t, other.ID, yamls.Routes[1].RouteID)
	assert.Contains(t, yamls.Routes[1].RouteYAML, "kind: UDPRoute")
	assert.Empty(t, yamls.Routes[1].BackendTrafficPolicyYAML, "no policy, no policy YAML")
}

func TestStreamYAMLs_NoLiveRoutes_PlaceholderGateway(t *testing.T) {
	f := newL4Fixture(t)
	f.routeRepo.On("ListActiveByStreamID", f.stream.ID).Return([]models.Route{}, nil)

	yamls, err := f.svc.GenerateStreamYAMLs(f.stream.ProjectID, f.stream.ID)
	require.NoError(t, err)

	assert.Contains(t, yamls.GatewayYAML, "kind: Gateway")
	assert.Contains(t, yamls.GatewayYAML, "name: l4-placeholder")
	assert.Contains(t, yamls.GatewayYAML, "port: 60000")
	assert.Equal(t, []services.StreamRouteYAMLs{}, yamls.Routes)
	raw, err := json.Marshal(yamls)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"routes":[]`)
}

func TestStreamYAMLs_OtherProjectStreamIsNotFound(t *testing.T) {
	f := newL4Fixture(t)
	_, err := f.svc.GenerateStreamYAMLs(uuid.New(), f.stream.ID)
	assert.ErrorIs(t, err, services.ErrStreamNotFound)
}

func TestStreamYAMLs_ReadFailuresAreErrors(t *testing.T) {
	t.Run("live route listing", func(t *testing.T) {
		f := newL4Fixture(t)
		f.routeRepo.On("ListActiveByStreamID", f.stream.ID).Return(nil, errors.New("db down"))
		yamls, err := f.svc.GenerateStreamYAMLs(f.stream.ProjectID, f.stream.ID)
		assert.Nil(t, yamls)
		assert.ErrorContains(t, err, "db down")
	})
	t.Run("a route's policy", func(t *testing.T) {
		f := newL4Fixture(t)
		route := f.route("pg", models.RouteProtocolTCP, 5432, models.RouteStatusActive)
		f.routeRepo.On("ListActiveByStreamID", f.stream.ID).Return([]models.Route{*route}, nil)
		f.btpRepo.On("GetByRouteID", route.ID).Return(nil, errors.New("connection reset"))
		yamls, err := f.svc.GenerateStreamYAMLs(f.stream.ProjectID, f.stream.ID)
		assert.Nil(t, yamls, "never a partial manifest set")
		assert.ErrorContains(t, err, "connection reset")
	})
}

func TestStreamYAMLs_JSONContract(t *testing.T) {
	id := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	raw, err := json.Marshal(services.StreamYAMLs{
		GatewayYAML: "g",
		Routes: []services.StreamRouteYAMLs{{RouteID: id, Name: "postgres", Protocol: models.RouteProtocolTCP,
			RouteYAML: "r", BackendTrafficPolicyYAML: ""}},
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{"gatewayYaml":"g","routes":[{"routeId":"22222222-2222-2222-2222-222222222222",
		"name":"postgres","protocol":"tcp","routeYaml":"r","backendTrafficPolicyYaml":""}]}`, string(raw))
}
```

Append to `internal/handlers/route_stream_yamls_handler_test.go`:

```go
func TestRouteHandler_GetStreamYAMLs(t *testing.T) {
	for _, tc := range []struct {
		name string
		res  *services.StreamYAMLs
		err  error
		code int
	}{
		{"ok", &services.StreamYAMLs{GatewayYAML: "kind: Gateway\n", Routes: []services.StreamRouteYAMLs{}}, nil, http.StatusOK},
		{"other project's stream", nil, services.ErrStreamNotFound, http.StatusNotFound},
		{"read failure", nil, fmt.Errorf("failed to list active routes for stream: %w", assert.AnError), http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mockRoute := new(mocks.MockRouteService)
			h := handlers.NewRouteHandler(mockRoute, new(mocks.MockAuditService), routePermChecker())
			projectID, streamID := uuid.New(), uuid.New()
			mockRoute.On("GenerateStreamYAMLs", projectID, streamID).Return(tc.res, tc.err)
			router := gin.New()
			router.GET("/projects/:projectId/streams/:streamId/yamls", h.GetStreamYAMLs)

			w := doJSON(router, "GET", fmt.Sprintf("/projects/%s/streams/%s/yamls", projectID, streamID), nil)

			require.Equal(t, tc.code, w.Code, w.Body.String())
			if tc.code == http.StatusOK {
				var body map[string]any
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
				assert.Equal(t, "kind: Gateway\n", body["gatewayYaml"])
				assert.Equal(t, []any{}, body["routes"])
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/services/ -run TestStreamYAMLs -count=1`
Expected: FAIL to build: `f.svc.GenerateStreamYAMLs undefined (type *services.RouteService has no field or method GenerateStreamYAMLs)`, `undefined: services.StreamRouteYAMLs`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/services/stream_yaml.go`:

```go
package services

import (
	"fmt"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/streamplan"
	"github.com/google/uuid"
	"sigs.k8s.io/yaml"
)

// StreamYAMLs is GET /projects/:projectId/streams/:streamId/yamls: the
// stream's Gateway and every live L4 route's manifests.
type StreamYAMLs struct {
	GatewayYAML string             `json:"gatewayYaml"`
	Routes      []StreamRouteYAMLs `json:"routes"`
}

// StreamRouteYAMLs is one live L4 route's manifests. BackendTrafficPolicyYAML
// is "" when the route has no policy.
type StreamRouteYAMLs struct {
	RouteID                  uuid.UUID            `json:"routeId"`
	Name                     string               `json:"name"`
	Protocol                 models.RouteProtocol `json:"protocol"`
	RouteYAML                string               `json:"routeYaml"`
	BackendTrafficPolicyYAML string               `json:"backendTrafficPolicyYaml"`
}

// GenerateStreamYAMLs renders the stream's manifests from exactly what
// deployL4 uses: the live route set (routeRepo.ListActiveByStreamID), the
// listener projection (streamplan.BuildStreamGatewayConfig) and the object
// builder UpdateGateway applies (kubernetes.BuildGatewayObject). With no live
// routes the Gateway is the placeholder one, as on the cluster. Any read
// failure is an error, never a partial manifest set.
func (q *routeQuery) GenerateStreamYAMLs(projectID, streamID uuid.UUID) (*StreamYAMLs, error) {
	stream, err := q.streamInProject(projectID, streamID)
	if err != nil {
		return nil, err
	}
	live, err := q.routeRepo.ListActiveByStreamID(stream.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to list active routes for stream: %w", err)
	}

	gwConfig := streamplan.BuildStreamGatewayConfig(*stream, live)
	gwYAML, err := yaml.Marshal(kubernetes.BuildGatewayObject(&gwConfig).Object)
	if err != nil {
		return nil, fmt.Errorf("marshal stream Gateway YAML: %w", err)
	}

	out := &StreamYAMLs{GatewayYAML: string(gwYAML), Routes: make([]StreamRouteYAMLs, 0, len(live))}
	for i := range live {
		route := &live[i]
		routeYAML, err := q.renderL4RouteYAML(route, stream)
		if err != nil {
			return nil, err
		}
		policy, err := q.storedBackendTrafficPolicy(route.ID)
		if err != nil {
			return nil, err
		}
		btpYAML, err := generateL4BackendTrafficPolicyYAML(route, stream, policy)
		if err != nil {
			return nil, err
		}
		out.Routes = append(out.Routes, StreamRouteYAMLs{
			RouteID:                  route.ID,
			Name:                     route.Name,
			Protocol:                 route.Protocol,
			RouteYAML:                routeYAML,
			BackendTrafficPolicyYAML: btpYAML,
		})
	}
	return out, nil
}

// GenerateStreamYAMLs renders a stream's Gateway and live L4 route manifests
// (streamID must belong to projectID, else ErrStreamNotFound).
func (s *RouteService) GenerateStreamYAMLs(projectID, streamID uuid.UUID) (*StreamYAMLs, error) {
	return s.query.GenerateStreamYAMLs(projectID, streamID)
}
```

In `internal/handlers/service_interfaces.go`, in `RoutePreviewer` below `GenerateYAMLs(...)`:

```go
	GenerateStreamYAMLs(projectID, streamID uuid.UUID) (*services.StreamYAMLs, error)
```

In `internal/handlers/route_stream_handler.go`, add `"errors"` as the first import and append:

```go
// GetStreamYAMLs gets the stream's manifests: its Gateway (rendered from the
// live route set deploy uses) and each live L4 route's TCPRoute/UDPRoute and
// BackendTrafficPolicy. A stream of another project is 404.
func (h *RouteHandler) GetStreamYAMLs(c *gin.Context) {
	projectID, streamID, ok := streamRouteIDs(c)
	if !ok {
		return
	}
	yamls, err := h.routeService.GenerateStreamYAMLs(projectID, streamID)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, services.ErrStreamNotFound) {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, yamls)
}
```

In `cmd/server/main.go`, below the `streams.GET("/:streamId/entrypoint", ...)` line from Task 4:

```go
					// Read-only manifests: the Gateway plus every live route's YAML.
					streams.GET("/:streamId/yamls", deps.RouteHandler.GetStreamYAMLs)
```

Append to `docs/openapi/paths/streams.yaml`:

```yaml
streams_{streamId}_yamls:
  get:
    tags: [Streams]
    summary: Get a stream's manifests (Gateway and every live L4 route)
    description: |
      The Gateway is rendered from the stream's live routes through the same
      builders the deploy path applies; with no live routes it is the
      placeholder Gateway. Each live route carries its TCPRoute/UDPRoute and
      BackendTrafficPolicy (empty when it has none).
    operationId: getStreamYamls
    security:
      - bearerAuth: []
    parameters:
      - $ref: '../components/parameters.yaml#/projectId'
      - $ref: '../components/parameters.yaml#/streamId'
    responses:
      '200':
        description: The stream's manifests
        content:
          application/json:
            schema:
              $ref: '../schemas/stream.yaml#/StreamYAMLs'
      '404':
        $ref: '../components/responses.yaml#/NotFound'
      '500':
        description: The stream's routes or policies could not be read (never a partial manifest set)
        content:
          application/json:
            schema:
              $ref: '../schemas/common.yaml#/Error'
```

Append to `docs/openapi/schemas/stream.yaml`:

```yaml
StreamYAMLs:
  type: object
  required: [gatewayYaml, routes]
  properties:
    gatewayYaml:
      type: string
      description: The stream's Gateway YAML, rendered from its live routes (placeholder listener when none).
    routes:
      type: array
      items:
        $ref: '#/StreamRouteYAMLs'

StreamRouteYAMLs:
  type: object
  required: [routeId, name, protocol, routeYaml, backendTrafficPolicyYaml]
  properties:
    routeId:
      type: string
      format: uuid
    name:
      type: string
    protocol:
      type: string
      enum: [tcp, udp]
    routeYaml:
      type: string
      description: TCPRoute or UDPRoute YAML.
    backendTrafficPolicyYaml:
      type: string
      description: BackendTrafficPolicy YAML, empty when the route has no policy.
```

In `docs/openapi/openapi.yaml`: under `paths:`, after the `.../streams/{streamId}/entrypoint` entry, add

```yaml
  /projects/{projectId}/streams/{streamId}/yamls:
    $ref: 'paths/streams.yaml#/streams_{streamId}_yamls'
```

and under `components: schemas:`, after `StreamListItem`, add

```yaml
    StreamYAMLs:
      $ref: 'schemas/stream.yaml#/StreamYAMLs'
```

Regenerate: `make mocks && make openapi`

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/services/ -run TestStreamYAMLs -count=1 -v && go test ./internal/handlers/ ./cmd/server/ -count=1 && make openapi-check`
Expected: PASS. `TestStreamYAMLs_EqualWhatDeployL4Applies` shows that the Gateway, TCPRoute and BackendTrafficPolicy YAML equal the objects deploy applied.

- [ ] **Step 5: Commit**

```bash
git add internal/services/stream_yaml.go internal/services/stream_yaml_test.go internal/handlers/ cmd/server/main.go cmd/server/openapi.yaml docs/openapi/ internal/mocks/
git commit -m "feat(streams): stream manifests rendered from the live route set

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
make mocks-check
```

---

### Task 8: L4 preview endpoints (create, update, delete)

**Files:**
- Modify: `internal/services/route_write_service.go` (extract `validateCreate` from `create`, `validateUpdate` from `Update`, a behavior-preserving refactor)
- Create: `internal/services/route_preview_l4.go`
- Modify: `internal/services/route_yaml_l4.go` (append `btpConfigFromInput`)
- Modify: `internal/services/route_yaml.go` (`previewUpdateL4` gains the policy; `PreviewDelete` L4 branch gains the policy)
- Modify: `internal/handlers/service_interfaces.go` (`RoutePreviewer`)
- Modify: `internal/handlers/route_stream_handler.go` (three handlers)
- Modify: `cmd/server/main.go` (three routes)
- Modify: `docs/openapi/paths/stream-routes.yaml`, `docs/openapi/openapi.yaml`; regenerate `cmd/server/openapi.yaml`
- Regenerate: `internal/mocks/mock_handler_services.go`
- Test: `internal/services/route_preview_l4_test.go` (new), `internal/handlers/route_stream_preview_handler_test.go` (new)

**Interfaces:**
- Consumes: Task 6 helpers; existing `routeWrite.resolveStream`, `routeWrite.query`, `routeWrite.assembler.newID`, `kubernetes.RouteK8sName`, `models.ValidateLabels`, `routeWriteErrorStatus`. Test fixtures `l4WriteFixture` (`route_write_l4_test.go`: `newL4WriteFixture`, `input`, `existingRoute`, `ports` = `MockL4PortChecker`) and `newL4YAMLFixtureWithBTP` (Task 6).
- Produces: `type createPlan struct{domainPtr *uuid.UUID; isStream bool; projectID uuid.UUID; protocol models.RouteProtocol; securityMode models.SecurityMode}`; `func (w *routeWrite) validateCreate(domainID uuid.UUID, input *CreateRouteInput, wantProject *uuid.UUID) (createPlan, error)`; `func (w *routeWrite) validateUpdate(id uuid.UUID, route *models.Route, input *UpdateRouteInput) (uuid.UUID, error)`; `func btpConfigFromInput(in *routeplan.BackendTrafficPolicyInput) *models.BackendTrafficPolicyConfig`; `(*RouteService).PreviewCreateForStream(projectID, streamID uuid.UUID, input *CreateRouteInput) (*PreviewCreateResult, error)`; `(*RouteService).PreviewUpdateForStream(routeID uuid.UUID, input *UpdateRouteInput) (*PreviewUpdateResult, error)` (a non-L4 route → `ErrRouteNotFound`); handlers `PreviewCreateForStream`, `PreviewUpdateForStream`, `PreviewDeleteForStream`; routes `POST …/streams/:streamId/routes/preview`, `POST …/routes/:routeId/preview`, `GET …/routes/:routeId/preview-delete`. Errors map through `routeWriteErrorStatus` (collision → 409, stream/route not found → 404, everything else → 400), the same as create/update. The permission is the domain previews' (project access only).

- [ ] **Step 1: Write the failing test**

Create `internal/services/route_preview_l4_test.go`:

```go
package services_test

import (
	"fmt"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// tcpBTPInput is tcpBTPConfig as a create/update input.
func tcpBTPInput() *routeplan.BackendTrafficPolicyInput {
	cfg := tcpBTPConfig()
	return &routeplan.BackendTrafficPolicyInput{CircuitBreaker: cfg.CircuitBreaker, Timeout: cfg.Timeout}
}

func TestPreviewCreateForStream_RendersRouteAndPolicyWithoutWriting(t *testing.T) {
	f := newL4WriteFixture(t, true)
	f.routeRepo.On("ExistsByStreamAndName", f.stream.ID, "pg").Return(false, nil)
	f.ports.On("CheckPortCollision", f.stream.ID, "TCP", 9050, (*uuid.UUID)(nil)).Return(nil)
	in := f.input("pg", models.RouteProtocolTCP, 9050)
	in.BackendTrafficPolicy = tcpBTPInput()

	res, err := f.svc.PreviewCreateForStream(f.stream.ProjectID, f.stream.ID, in)
	require.NoError(t, err)

	assert.Contains(t, res.ProposedYAML, "kind: TCPRoute")
	assert.Contains(t, res.ProposedYAML, "sectionName: l4-tcp-9050")
	assert.Contains(t, res.ProposedYAML, "name: pg-", "the K8s name is derived from the route name, as create does")
	assert.Contains(t, res.ProposedBackendTrafficPolicyYAML, "kind: BackendTrafficPolicy")
	assert.Contains(t, res.ProposedBackendTrafficPolicyYAML, "maxConnections: 100")
	assert.Contains(t, res.ProposedBackendTrafficPolicyYAML, "namespace: fastgateway-system")
	f.routeRepo.AssertNotCalled(t, "Create", mock.Anything)
	f.approvalRepo.AssertNotCalled(t, "Create", mock.Anything)
	f.ports.AssertExpectations(t)
}

func TestPreviewCreateForStream_NoPolicyInputMeansNoPolicyYAML(t *testing.T) {
	f := newL4WriteFixture(t, true)
	f.routeRepo.On("ExistsByStreamAndName", f.stream.ID, "dns").Return(false, nil)
	f.ports.On("CheckPortCollision", f.stream.ID, "UDP", 9053, (*uuid.UUID)(nil)).Return(nil)

	res, err := f.svc.PreviewCreateForStream(f.stream.ProjectID, f.stream.ID, f.input("dns", models.RouteProtocolUDP, 9053))
	require.NoError(t, err)
	assert.Contains(t, res.ProposedYAML, "kind: UDPRoute")
	assert.Empty(t, res.ProposedBackendTrafficPolicyYAML)
}

// TestPreviewCreateForStream_SameErrorsAsCreateForStream runs every bad input
// through both PreviewCreateForStream and CreateForStream on fresh, identical
// fixtures and requires the same error (so the handler's routeWriteErrorStatus
// maps both to the same 400/404/409): a preview never shows a manifest that
// create would reject.
func TestPreviewCreateForStream_SameErrorsAsCreateForStream(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(in *services.CreateRouteInput)
		project func(f *l4WriteFixture) uuid.UUID
		checker error
		exists  bool
		is      error
	}{
		{name: "bad name", mutate: func(in *services.CreateRouteInput) { in.Name = "Bad Name" }},
		{name: "not tcp/udp", mutate: func(in *services.CreateRouteInput) { in.Protocol = models.RouteProtocolHTTP }, is: services.ErrRouteProtocolNotL4},
		{name: "reserved port", mutate: func(in *services.CreateRouteInput) { in.Config.ListenerPort = 19000 }, is: services.ErrReservedPort},
		{name: "invalid port", mutate: func(in *services.CreateRouteInput) { in.Config.ListenerPort = 70000 }, is: services.ErrInvalidListenerPort},
		{name: "out of template range", checker: fmt.Errorf("%w: 9050 not in 10000-11000", services.ErrPortOutOfRange), is: services.ErrPortOutOfRange},
		{name: "collision", checker: fmt.Errorf("%w: TCP/9050 is used by a stream route", services.ErrPortCollision), is: services.ErrPortCollision},
		{name: "L7 policy on an L4 route", mutate: func(in *services.CreateRouteInput) { in.SecurityPolicy = &routeplan.SecurityPolicyInput{} }, is: services.ErrL4RejectsL7Field},
		{name: "stream of another project", project: func(*l4WriteFixture) uuid.UUID { return uuid.New() }, is: services.ErrStreamNotFound},
		{name: "name already taken on the stream", exists: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			run := func(call func(f *l4WriteFixture, projectID uuid.UUID, in *services.CreateRouteInput) error) error {
				f := newL4WriteFixture(t, true)
				f.routeRepo.On("ExistsByStreamAndName", f.stream.ID, mock.Anything).Return(tc.exists, nil).Maybe()
				f.ports.On("CheckPortCollision", f.stream.ID, mock.Anything, mock.Anything, (*uuid.UUID)(nil)).Return(tc.checker).Maybe()
				in := f.input("pg", models.RouteProtocolTCP, 9050)
				if tc.mutate != nil {
					tc.mutate(in)
				}
				projectID := f.stream.ProjectID
				if tc.project != nil {
					projectID = tc.project(f)
				}
				err := call(f, projectID, in)
				f.routeRepo.AssertNotCalled(t, "Create", mock.Anything)
				return err
			}
			previewErr := run(func(f *l4WriteFixture, projectID uuid.UUID, in *services.CreateRouteInput) error {
				_, err := f.svc.PreviewCreateForStream(projectID, f.stream.ID, in)
				return err
			})
			createErr := run(func(f *l4WriteFixture, projectID uuid.UUID, in *services.CreateRouteInput) error {
				_, err := f.svc.CreateForStream(projectID, f.stream.ID, in, uuid.New())
				return err
			})

			require.Error(t, createErr)
			require.Error(t, previewErr)
			assert.Equal(t, createErr.Error(), previewErr.Error())
			if tc.is != nil {
				assert.ErrorIs(t, createErr, tc.is)
				assert.ErrorIs(t, previewErr, tc.is)
			}
		})
	}
}

func TestPreviewUpdateForStream_ShowsRouteAndPolicyDiffAndRechecksPort(t *testing.T) {
	f := newL4WriteFixture(t, true)
	route := f.existingRoute(models.RouteStatusActive)
	f.routeRepo.On("GetByID", route.ID).Return(route, nil)
	f.ports.On("CheckPortCollision", f.stream.ID, "TCP", 6432, &route.ID).Return(nil)
	cfg := f.input("pg", models.RouteProtocolTCP, 6432).Config

	res, err := f.svc.PreviewUpdateForStream(route.ID, &services.UpdateRouteInput{Config: cfg, BackendTrafficPolicy: tcpBTPInput()})
	require.NoError(t, err)

	assert.Contains(t, res.CurrentYAML, "sectionName: l4-tcp-5432")
	assert.Contains(t, res.ProposedYAML, "sectionName: l4-tcp-6432")
	assert.Empty(t, res.CurrentBackendTrafficPolicyYAML, "the route has no stored policy yet")
	assert.Contains(t, res.ProposedBackendTrafficPolicyYAML, "maxConnections: 100")
	f.ports.AssertCalled(t, "CheckPortCollision", f.stream.ID, "TCP", 6432, &route.ID)
	f.routeRepo.AssertNotCalled(t, "Update", mock.Anything)
	f.approvalRepo.AssertNotCalled(t, "Create", mock.Anything)
}

func TestPreviewUpdateForStream_SameErrorsAsUpdate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		port    int
		checker error
		is      error
	}{
		{"collision on the new port", 6432, fmt.Errorf("%w: TCP/6432", services.ErrPortCollision), services.ErrPortCollision},
		{"reserved port", 60000, nil, services.ErrReservedPort},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := func(call func(f *l4WriteFixture, routeID uuid.UUID, in *services.UpdateRouteInput) error) error {
				f := newL4WriteFixture(t, true)
				route := f.existingRoute(models.RouteStatusActive)
				f.routeRepo.On("GetByID", route.ID).Return(route, nil)
				f.ports.On("CheckPortCollision", f.stream.ID, "TCP", tc.port, &route.ID).Return(tc.checker).Maybe()
				err := call(f, route.ID, &services.UpdateRouteInput{Config: f.input("pg", models.RouteProtocolTCP, tc.port).Config})
				f.routeRepo.AssertNotCalled(t, "Update", mock.Anything)
				return err
			}
			previewErr := run(func(f *l4WriteFixture, id uuid.UUID, in *services.UpdateRouteInput) error {
				_, err := f.svc.PreviewUpdateForStream(id, in)
				return err
			})
			updateErr := run(func(f *l4WriteFixture, id uuid.UUID, in *services.UpdateRouteInput) error {
				_, err := f.svc.Update(id, in, uuid.New())
				return err
			})
			require.Error(t, updateErr)
			assert.Equal(t, updateErr.Error(), previewErr.Error())
			assert.ErrorIs(t, previewErr, tc.is)
		})
	}
}

func TestPreviewUpdateForStream_NonL4RouteIsNotFound(t *testing.T) {
	f := newL4WriteFixture(t, true)
	domainID := uuid.New()
	route := &models.Route{ID: uuid.New(), DomainID: &domainID, Protocol: models.RouteProtocolHTTP}
	f.routeRepo.On("GetByID", route.ID).Return(route, nil)

	_, err := f.svc.PreviewUpdateForStream(route.ID, &services.UpdateRouteInput{})
	assert.ErrorIs(t, err, services.ErrRouteNotFound)
}

func TestRouteYAML_L4_PreviewUpdate_StoredPolicyIsCurrent(t *testing.T) {
	btpRepo := new(mocks.MockBackendTrafficPolicyRepository)
	f := newL4YAMLFixtureWithBTP(t, btpRepo)
	route := f.route(models.RouteProtocolTCP, 5432)
	btpRepo.On("GetByRouteID", route.ID).Return(&models.BackendTrafficPolicy{ID: uuid.New(), RouteID: &route.ID, Config: tcpBTPConfig()}, nil)

	// The update drops the policy: current shows it, proposed does not.
	res, err := f.svc.PreviewUpdate(route.ID, &services.UpdateRouteInput{Config: route.Config})
	require.NoError(t, err)
	assert.Contains(t, res.CurrentBackendTrafficPolicyYAML, "maxConnections: 100")
	assert.Empty(t, res.ProposedBackendTrafficPolicyYAML)
}

func TestRouteYAML_L4_PreviewDelete_IncludesPolicy(t *testing.T) {
	btpRepo := new(mocks.MockBackendTrafficPolicyRepository)
	f := newL4YAMLFixtureWithBTP(t, btpRepo)
	route := f.route(models.RouteProtocolTCP, 5432)
	btpRepo.On("GetByRouteID", route.ID).Return(&models.BackendTrafficPolicy{ID: uuid.New(), RouteID: &route.ID, Config: tcpBTPConfig()}, nil)

	res, err := f.svc.PreviewDelete(route.ID)
	require.NoError(t, err)
	assert.Contains(t, res.CurrentYAML, "kind: TCPRoute")
	assert.Contains(t, res.CurrentBackendTrafficPolicyYAML, "kind: BackendTrafficPolicy")
}
```

Create `internal/handlers/route_stream_preview_handler_test.go`:

```go
package handlers_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/handlers"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// streamPreviewRouter mounts the three stream-scoped preview endpoints the way
// cmd/server/main.go does.
func streamPreviewRouter(h *handlers.RouteHandler) *gin.Engine {
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("user", testUser()) })
	g := router.Group("/projects/:projectId/streams/:streamId/routes")
	g.POST("/preview", h.PreviewCreateForStream)
	g.POST("/:routeId/preview", h.PreviewUpdateForStream)
	g.GET("/:routeId/preview-delete", h.PreviewDeleteForStream)
	return router
}

func l4PreviewBody() map[string]any {
	return map[string]any{"name": "pg", "teamId": uuid.New().String(), "protocol": "tcp", "config": map[string]any{"listenerPort": 9050}}
}

func TestRouteHandler_PreviewCreateForStream_StatusMapping(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code int
	}{
		{"ok", nil, http.StatusOK},
		{"collision", fmt.Errorf("%w: TCP/9050", services.ErrPortCollision), http.StatusConflict},
		{"reserved", fmt.Errorf("%w: 19000", services.ErrReservedPort), http.StatusBadRequest},
		{"out of range", fmt.Errorf("%w: 9050 not in 10000-11000", services.ErrPortOutOfRange), http.StatusBadRequest},
		{"stream of another project", services.ErrStreamNotFound, http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mockRoute := new(mocks.MockRouteService)
			h := handlers.NewRouteHandler(mockRoute, new(mocks.MockAuditService), routePermChecker())
			projectID, streamID := uuid.New(), uuid.New()
			var res *services.PreviewCreateResult
			if tc.err == nil {
				res = &services.PreviewCreateResult{ProposedYAML: "kind: TCPRoute\n"}
			}
			mockRoute.On("PreviewCreateForStream", projectID, streamID, mock.AnythingOfType("*services.CreateRouteInput")).Return(res, tc.err)

			w := doJSON(streamPreviewRouter(h), "POST", fmt.Sprintf("/projects/%s/streams/%s/routes/preview", projectID, streamID), l4PreviewBody())

			assert.Equal(t, tc.code, w.Code, w.Body.String())
		})
	}
}

func TestRouteHandler_PreviewCreateForStream_BadBodyIs400(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewRouteHandler(mockRoute, new(mocks.MockAuditService), routePermChecker())
	w := doJSON(streamPreviewRouter(h), "POST", fmt.Sprintf("/projects/%s/streams/%s/routes/preview", uuid.New(), uuid.New()), map[string]any{"name": ""})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockRoute.AssertNotCalled(t, "PreviewCreateForStream", mock.Anything, mock.Anything, mock.Anything)
}

func TestRouteHandler_PreviewUpdateForStream(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code int
	}{
		{"ok", nil, http.StatusOK},
		{"collision on the new port", fmt.Errorf("%w: TCP/6432", services.ErrPortCollision), http.StatusConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mockRoute := new(mocks.MockRouteService)
			h := handlers.NewRouteHandler(mockRoute, new(mocks.MockAuditService), routePermChecker())
			projectID, streamID, routeID := uuid.New(), uuid.New(), uuid.New()
			mockRoute.On("GetForStream", projectID, streamID, routeID).Return(&models.Route{ID: routeID, StreamID: &streamID}, nil)
			var res *services.PreviewUpdateResult
			if tc.err == nil {
				res = &services.PreviewUpdateResult{CurrentYAML: "a", ProposedYAML: "b"}
			}
			mockRoute.On("PreviewUpdateForStream", routeID, mock.AnythingOfType("*services.UpdateRouteInput")).Return(res, tc.err)

			w := doJSON(streamPreviewRouter(h), "POST", fmt.Sprintf("/projects/%s/streams/%s/routes/%s/preview", projectID, streamID, routeID),
				map[string]any{"config": map[string]any{"listenerPort": 6432}})

			assert.Equal(t, tc.code, w.Code, w.Body.String())
		})
	}
}

// A route of another stream is 404 on every stream-scoped preview, before
// any preview runs.
func TestRouteHandler_StreamPreviews_RouteOfOtherStreamIsNotFound(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewRouteHandler(mockRoute, new(mocks.MockAuditService), routePermChecker())
	projectID, streamID, routeID := uuid.New(), uuid.New(), uuid.New()
	mockRoute.On("GetForStream", projectID, streamID, routeID).Return(nil, services.ErrRouteNotFound)
	router := streamPreviewRouter(h)
	base := fmt.Sprintf("/projects/%s/streams/%s/routes/%s", projectID, streamID, routeID)

	assert.Equal(t, http.StatusNotFound, doJSON(router, "POST", base+"/preview", map[string]any{"config": map[string]any{}}).Code)
	assert.Equal(t, http.StatusNotFound, doJSON(router, "GET", base+"/preview-delete", nil).Code)
	mockRoute.AssertNotCalled(t, "PreviewUpdateForStream", mock.Anything, mock.Anything)
	mockRoute.AssertNotCalled(t, "PreviewDelete", mock.Anything)
}

func TestRouteHandler_PreviewDeleteForStream_Success(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewRouteHandler(mockRoute, new(mocks.MockAuditService), routePermChecker())
	projectID, streamID, routeID := uuid.New(), uuid.New(), uuid.New()
	mockRoute.On("GetForStream", projectID, streamID, routeID).Return(&models.Route{ID: routeID, StreamID: &streamID}, nil)
	mockRoute.On("PreviewDelete", routeID).Return(&services.PreviewDeleteResult{CurrentYAML: "kind: TCPRoute\n"}, nil)

	w := doJSON(streamPreviewRouter(h), "GET", fmt.Sprintf("/projects/%s/streams/%s/routes/%s/preview-delete", projectID, streamID, routeID), nil)

	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/services/ -run 'TestPreview|TestRouteYAML_L4_Preview' -count=1`
Expected: FAIL to build: `f.svc.PreviewCreateForStream undefined (type *services.RouteService has no field or method PreviewCreateForStream)`.

- [ ] **Step 3: Write minimal implementation**

**3a. Extract create's validation** (`internal/services/route_write_service.go`). Replace everything from the line `func (w *routeWrite) create(domainID uuid.UUID, input *CreateRouteInput, createdBy uuid.UUID, wantProject *uuid.UUID) (*models.Route, error) {` down to (but not including) the comment `	// Generate route UUID first so we can use it for K8s resource name` with the block below. The checks are the existing ones, in the same order, with `return nil, …` turned into `return createPlan{}, …`. Everything in `create` after that comment is unchanged and still uses `domainPtr`, `isStream`, `projectID`, `protocol` and `securityMode`:

```go
// createPlan is what validateCreate resolves for a route create: the owner,
// its project, and the defaulted protocol and security mode.
type createPlan struct {
	domainPtr    *uuid.UUID
	isStream     bool
	projectID    uuid.UUID
	protocol     models.RouteProtocol
	securityMode models.SecurityMode
}

// validateCreate runs every input check of a route create, in create's
// order, and persists nothing. create and PreviewCreateForStream both call
// it, so a preview can never show a manifest that create would reject.
func (w *routeWrite) validateCreate(domainID uuid.UUID, input *CreateRouteInput, wantProject *uuid.UUID) (createPlan, error) {
	var domainPtr *uuid.UUID
	if domainID != uuid.Nil {
		domainPtr = &domainID
	}
	if err := validateRouteOwner(domainPtr, input.StreamID); err != nil {
		return createPlan{}, err
	}
	isStream := input.StreamID != nil

	// Validate route name - no spaces allowed
	if strings.Contains(input.Name, " ") {
		return createPlan{}, errors.New("route name cannot contain spaces")
	}

	// Validate route name format - must be lowercase alphanumeric with dashes
	if !isValidK8sName(input.Name) {
		return createPlan{}, errors.New("route name must be lowercase alphanumeric with dashes only (e.g., 'user-api')")
	}

	// Resolve the owner (domain or stream) and its project, and check the
	// route name is free within it.
	var projectID uuid.UUID
	if isStream {
		if input.Protocol != models.RouteProtocolTCP && input.Protocol != models.RouteProtocolUDP {
			return createPlan{}, ErrRouteProtocolNotL4
		}
		stream, err := w.resolveStream(*input.StreamID, wantProject)
		if err != nil {
			return createPlan{}, err
		}
		projectID = stream.ProjectID

		exists, err := w.routeRepo.ExistsByStreamAndName(stream.ID, input.Name)
		if err != nil {
			return createPlan{}, err
		}
		if exists {
			return createPlan{}, errors.New("route name already exists in this stream")
		}
	} else {
		// Check if route name already exists in domain
		exists, err := w.routeRepo.ExistsByName(domainID, input.Name)
		if err != nil {
			return createPlan{}, err
		}
		if exists {
			return createPlan{}, errors.New("route name already exists in this domain")
		}

		// Verify domain exists
		domain, err := w.domainRepo.GetByID(domainID)
		if err != nil {
			return createPlan{}, errors.New("domain not found")
		}
		projectID = domain.ProjectID
	}

	if err := w.validateRouteTrafficPolicies(&input.Config, input.BackendTrafficPolicy, projectID); err != nil {
		return createPlan{}, err
	}

	if err := validateDirectResponseInput(&input.Config, input.BackendTrafficPolicy); err != nil {
		return createPlan{}, err
	}

	// Verify team exists
	if _, err := w.teamRepo.GetByID(input.TeamID); err != nil {
		return createPlan{}, errors.New("team not found")
	}

	protocol := input.Protocol
	if protocol == "" {
		protocol = models.RouteProtocolHTTP
	}

	// Default security mode
	securityMode := input.SecurityMode
	if securityMode == "" {
		securityMode = models.SecurityModeGeneral
	}

	if err := validateWriteSecurityMode(securityMode, input.SecurityPolicy, true); err != nil {
		return createPlan{}, err
	}

	if protocol == models.RouteProtocolTCP || protocol == models.RouteProtocolUDP {
		if err := validateL4PolicyInputs(protocol, securityMode, input.SecurityPolicy, input.ExtensionPolicy, input.WafPolicy, input.BackendTrafficPolicy); err != nil {
			return createPlan{}, err
		}
	}

	if err := w.validateRouteShapeAndConflicts(&input.Config, input.BackendTrafficPolicy, protocol, domainID, input.StreamID, nil); err != nil {
		return createPlan{}, err
	}

	return createPlan{domainPtr: domainPtr, isStream: isStream, projectID: projectID, protocol: protocol, securityMode: securityMode}, nil
}

func (w *routeWrite) create(domainID uuid.UUID, input *CreateRouteInput, createdBy uuid.UUID, wantProject *uuid.UUID) (*models.Route, error) {
	plan, err := w.validateCreate(domainID, input, wantProject)
	if err != nil {
		return nil, err
	}
	domainPtr, isStream, projectID, protocol, securityMode := plan.domainPtr, plan.isStream, plan.projectID, plan.protocol, plan.securityMode
```

**3b. Extract Update's validation.** In `Update`, replace everything from the comment `	// Resolve the owning project to validate namespaces. An L4 route has a` down to (but not including) `	// Check if there's already a pending approval` with:

```go
	projectID, err := w.validateUpdate(id, route, input)
	if err != nil {
		return nil, err
	}

```

and insert the extracted function immediately above `// Delete requests deletion of a route (submits for approval)`:

```go
// validateUpdate runs every input check of a route update, in Update's
// order, and persists nothing. It returns the route's project. Update and
// PreviewUpdateForStream both call it.
func (w *routeWrite) validateUpdate(id uuid.UUID, route *models.Route, input *UpdateRouteInput) (uuid.UUID, error) {
	// Resolve the owning project to validate namespaces. An L4 route has a
	// Stream and no Domain, so this must precede every *route.DomainID use.
	projectID, _, err := w.routeProjectID(route)
	if err != nil {
		return uuid.Nil, err
	}
	var domainID uuid.UUID // zero for an L4 route, which has no matcher domain
	if route.DomainID != nil {
		domainID = *route.DomainID
	}

	if err := w.validateRouteTrafficPolicies(&input.Config, input.BackendTrafficPolicy, projectID); err != nil {
		return uuid.Nil, err
	}

	if err := validateWriteSecurityMode(route.SecurityMode, input.SecurityPolicy, false); err != nil {
		return uuid.Nil, err
	}

	if route.IsL4() {
		if err := validateL4PolicyInputs(route.Protocol, route.SecurityMode, input.SecurityPolicy, input.ExtensionPolicy, input.WafPolicy, input.BackendTrafficPolicy); err != nil {
			return uuid.Nil, err
		}
	}

	if err := w.validateRouteShapeAndConflicts(&input.Config, input.BackendTrafficPolicy, route.Protocol, domainID, route.StreamID, &id); err != nil {
		return uuid.Nil, err
	}

	if err := validateDirectResponseInput(&input.Config, input.BackendTrafficPolicy); err != nil {
		return uuid.Nil, err
	}

	return projectID, nil
}
```

Run `go test ./internal/services/ -count=1` now. Expected: `ok`. The existing create/update tests (`TestRouteWrite_*`, the golden and characterization suites) pin that the extraction changed nothing.

**3c. The previews.** Append to `internal/services/route_yaml_l4.go`:

```go
// btpConfigFromInput is the policy config a create/update would persist for
// input (the same ten fields persistCreatePolicies/persistUpdatePolicies
// copy), or nil when the input carries no policy.
func btpConfigFromInput(in *routeplan.BackendTrafficPolicyInput) *models.BackendTrafficPolicyConfig {
	if in == nil || !in.HasContent() {
		return nil
	}
	return &models.BackendTrafficPolicyConfig{
		Compression:      in.Compression,
		Retry:            in.Retry,
		LoadBalancer:     in.LoadBalancer,
		CircuitBreaker:   in.CircuitBreaker,
		HealthCheck:      in.HealthCheck,
		FaultInjection:   in.FaultInjection,
		RateLimit:        in.RateLimit,
		RequestBuffer:    in.RequestBuffer,
		ResponseOverride: in.ResponseOverride,
		Timeout:          in.Timeout,
	}
}
```

Create `internal/services/route_preview_l4.go`:

```go
package services

import (
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
)

// PreviewCreateForStream renders the TCPRoute/UDPRoute and BackendTrafficPolicy
// a create under streamID would produce, after running exactly the
// validation CreateForStream runs (validateCreate, then labels). It persists
// nothing; its errors are create's errors, so the handler maps them to the
// same 400/404/409.
func (w *routeWrite) PreviewCreateForStream(projectID, streamID uuid.UUID, input *CreateRouteInput) (*PreviewCreateResult, error) {
	in := *input
	in.StreamID = &streamID
	if _, err := w.validateCreate(uuid.Nil, &in, &projectID); err != nil {
		return nil, err
	}
	if in.Labels != nil {
		if err := models.ValidateLabels(in.Labels); err != nil {
			return nil, err
		}
	}
	stream, err := w.resolveStream(streamID, &projectID)
	if err != nil {
		return nil, err
	}

	// The real create mints the ID at persist time; the preview mints a
	// temporary one, as the domain PreviewCreate does.
	tempID := w.assembler.newID()
	route := &models.Route{
		ID:           tempID,
		StreamID:     &streamID,
		TeamID:       in.TeamID,
		Name:         in.Name,
		Description:  in.Description,
		Protocol:     in.Protocol,
		SecurityMode: models.SecurityModeGeneral,
		Config:       in.Config,
		K8sRouteName: kubernetes.RouteK8sName(in.Name, tempID.String()),
	}
	routeYAML, err := w.query.renderL4RouteYAML(route, stream)
	if err != nil {
		return nil, err
	}
	btpYAML, err := generateL4BackendTrafficPolicyYAML(route, stream, l4PolicyFromConfig(route, btpConfigFromInput(in.BackendTrafficPolicy)))
	if err != nil {
		return nil, err
	}
	return &PreviewCreateResult{ProposedYAML: routeYAML, ProposedBackendTrafficPolicyYAML: btpYAML}, nil
}

// PreviewUpdateForStream renders the current and proposed TCPRoute/UDPRoute
// and BackendTrafficPolicy of an L4 route, after running exactly the input
// validation Update runs (validateUpdate) - including the port re-check
// against the stream with the route itself excluded. It persists nothing. A
// route that is not L4 is ErrRouteNotFound.
func (w *routeWrite) PreviewUpdateForStream(routeID uuid.UUID, input *UpdateRouteInput) (*PreviewUpdateResult, error) {
	route, err := w.routeRepo.GetByID(routeID)
	if err != nil || route == nil || !route.IsL4() {
		return nil, ErrRouteNotFound
	}
	if _, err := w.validateUpdate(routeID, route, input); err != nil {
		return nil, err
	}
	return w.query.previewUpdateL4(route, input)
}

// PreviewCreateForStream previews an L4 route create under a stream with
// CreateForStream's validation (streamID must belong to projectID).
func (s *RouteService) PreviewCreateForStream(projectID, streamID uuid.UUID, input *CreateRouteInput) (*PreviewCreateResult, error) {
	return s.write.PreviewCreateForStream(projectID, streamID, input)
}

// PreviewUpdateForStream previews an L4 route update with Update's validation.
func (s *RouteService) PreviewUpdateForStream(routeID uuid.UUID, input *UpdateRouteInput) (*PreviewUpdateResult, error) {
	return s.write.PreviewUpdateForStream(routeID, input)
}
```

In `internal/services/route_yaml.go`, replace `previewUpdateL4` with:

```go
// previewUpdateL4 compares the current and proposed TCPRoute/UDPRoute and
// BackendTrafficPolicy of an L4 route: current from the route and its stored
// policy, proposed from the input (the policy an update would persist).
func (q *routeQuery) previewUpdateL4(route *models.Route, input *UpdateRouteInput) (*PreviewUpdateResult, error) {
	stream, err := q.l4Stream(route)
	if err != nil {
		return nil, err
	}
	currentYAML, err := q.renderL4RouteYAML(route, stream)
	if err != nil {
		return nil, err
	}
	stored, err := q.storedBackendTrafficPolicy(route.ID)
	if err != nil {
		return nil, err
	}
	currentBTP, err := generateL4BackendTrafficPolicyYAML(route, stream, stored)
	if err != nil {
		return nil, err
	}

	proposed := *route
	proposed.Config = input.Config
	if input.Description != "" {
		proposed.Description = input.Description
	}
	proposedYAML, err := q.renderL4RouteYAML(&proposed, stream)
	if err != nil {
		return nil, err
	}
	proposedBTP, err := generateL4BackendTrafficPolicyYAML(&proposed, stream, l4PolicyFromConfig(&proposed, btpConfigFromInput(input.BackendTrafficPolicy)))
	if err != nil {
		return nil, err
	}

	return &PreviewUpdateResult{
		CurrentYAML:                      currentYAML,
		ProposedYAML:                     proposedYAML,
		CurrentBackendTrafficPolicyYAML:  currentBTP,
		ProposedBackendTrafficPolicyYAML: proposedBTP,
	}, nil
}
```

and in `PreviewDelete` replace the L4 branch:

```go
	// L4 routes have no Domain; must precede the *route.DomainID deref.
	if route.IsL4() {
		currentYAML, err := q.generateL4RouteYAML(route)
		if err != nil {
			return nil, err
		}
		return &PreviewDeleteResult{CurrentYAML: currentYAML}, nil
	}
```

with:

```go
	// L4 routes have no Domain; must precede the *route.DomainID deref. The
	// delete deploy removes the route's BackendTrafficPolicy too, so the
	// preview shows it.
	if route.IsL4() {
		yamls, err := q.generateL4RouteYAMLs(route)
		if err != nil {
			return nil, err
		}
		return &PreviewDeleteResult{CurrentYAML: yamls.HTTPRouteYAML, CurrentBackendTrafficPolicyYAML: yamls.BackendTrafficPolicyYAML}, nil
	}
```

**3d. Handlers and routes.** In `internal/handlers/service_interfaces.go`, add to `RoutePreviewer` below `PreviewDelete(...)`:

```go
	PreviewCreateForStream(projectID, streamID uuid.UUID, input *services.CreateRouteInput) (*services.PreviewCreateResult, error)
	PreviewUpdateForStream(routeID uuid.UUID, input *services.UpdateRouteInput) (*services.PreviewUpdateResult, error)
```

Append to `internal/handlers/route_stream_handler.go`:

```go
// PreviewCreateForStream renders the manifests an L4 route create would
// produce, after the same validation as CreateForStream; its errors map to
// the same statuses (400, 404, 409). Same permission as the domain route
// preview: project access only.
func (h *RouteHandler) PreviewCreateForStream(c *gin.Context) {
	projectID, streamID, ok := streamRouteIDs(c)
	if !ok {
		return
	}
	var input services.CreateRouteInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := h.routeService.PreviewCreateForStream(projectID, streamID, &input)
	if err != nil {
		c.JSON(routeWriteErrorStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}

// PreviewUpdateForStream renders the current and proposed manifests of an L4
// route update, after the same validation as Update (a changed port is
// re-checked); its errors map like the update's.
func (h *RouteHandler) PreviewUpdateForStream(c *gin.Context) {
	if !h.requireStreamRoute(c) {
		return
	}
	routeID, _ := uuid.Parse(c.Param("routeId")) // validated by requireStreamRoute
	var input services.UpdateRouteInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := h.routeService.PreviewUpdateForStream(routeID, &input)
	if err != nil {
		c.JSON(routeWriteErrorStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}

// PreviewDeleteForStream renders what deleting an L4 route of a stream
// removes: its TCPRoute/UDPRoute and BackendTrafficPolicy.
func (h *RouteHandler) PreviewDeleteForStream(c *gin.Context) {
	if h.requireStreamRoute(c) {
		h.PreviewDelete(c)
	}
}
```

In `cmd/server/main.go`, in the `streamRoutes` block, replace `streamRoutes.POST("", deps.RouteHandler.CreateForStream) // Permission check in handler` with:

```go
						streamRoutes.POST("", deps.RouteHandler.CreateForStream)                // Permission check in handler
						streamRoutes.POST("/preview", deps.RouteHandler.PreviewCreateForStream) // Preview create - same validation as create, no write
```

and below the `/:routeId/yamls` line from Task 6 add:

```go
						streamRoutes.POST("/:routeId/preview", deps.RouteHandler.PreviewUpdateForStream)
						streamRoutes.GET("/:routeId/preview-delete", deps.RouteHandler.PreviewDeleteForStream)
```

**3e. OpenAPI.** Append to `docs/openapi/paths/stream-routes.yaml`:

```yaml
stream-routes-preview:
  post:
    tags: [Streams]
    summary: Preview an L4 route create (TCPRoute/UDPRoute and BackendTrafficPolicy, no write)
    description: |
      Runs the same validation as creating the route (name, tcp/udp
      protocol, listener port validity, reserved ports, template range and
      collisions, protocol-gated policy inputs) and returns the manifests the
      create would produce. Errors are the create's errors with the same
      statuses, so a preview never shows a manifest create would reject.
    operationId: previewCreateStreamRoute
    security:
      - bearerAuth: []
    parameters:
      - $ref: '../components/parameters.yaml#/projectId'
      - $ref: '../components/parameters.yaml#/streamId'
    requestBody:
      required: true
      content:
        application/json:
          schema:
            $ref: '../schemas/route.yaml#/CreateStreamRouteRequest'
    responses:
      '200':
        description: Proposed manifests (proposedYaml is the TCPRoute/UDPRoute)
        content:
          application/json:
            schema:
              $ref: '../schemas/route.yaml#/PreviewCreateResponse'
      '400':
        description: Invalid route (same cases as create)
        content:
          application/json:
            schema:
              $ref: '../schemas/common.yaml#/Error'
      '404':
        description: Stream not found (including streams of other projects)
        content:
          application/json:
            schema:
              $ref: '../schemas/common.yaml#/Error'
      '409':
        description: The listener port is already in use in the stream's scope
        content:
          application/json:
            schema:
              $ref: '../schemas/common.yaml#/Error'

stream-route-preview:
  post:
    tags: [Streams]
    summary: Preview an L4 route update (current and proposed TCPRoute/UDPRoute and BackendTrafficPolicy)
    description: |
      Runs the same input validation as updating the route, including the
      listener-port re-check with the route itself excluded.
    operationId: previewUpdateStreamRoute
    security:
      - bearerAuth: []
    parameters:
      - $ref: '../components/parameters.yaml#/projectId'
      - $ref: '../components/parameters.yaml#/streamId'
      - $ref: '../components/parameters.yaml#/routeId'
    requestBody:
      required: true
      content:
        application/json:
          schema:
            $ref: '../schemas/route.yaml#/UpdateRouteRequest'
    responses:
      '200':
        description: Current and proposed manifests
        content:
          application/json:
            schema:
              $ref: '../schemas/route.yaml#/PreviewUpdateResponse'
      '400':
        description: Invalid route configuration
        content:
          application/json:
            schema:
              $ref: '../schemas/common.yaml#/Error'
      '404':
        $ref: '../components/responses.yaml#/NotFound'
      '409':
        description: The new listener port is already in use in the stream's scope
        content:
          application/json:
            schema:
              $ref: '../schemas/common.yaml#/Error'

stream-route-preview-delete:
  get:
    tags: [Streams]
    summary: Preview an L4 route delete (TCPRoute/UDPRoute and BackendTrafficPolicy that will be removed)
    operationId: previewDeleteStreamRoute
    security:
      - bearerAuth: []
    parameters:
      - $ref: '../components/parameters.yaml#/projectId'
      - $ref: '../components/parameters.yaml#/streamId'
      - $ref: '../components/parameters.yaml#/routeId'
    responses:
      '200':
        description: Manifests that the delete removes
        content:
          application/json:
            schema:
              $ref: '../schemas/route.yaml#/PreviewDeleteResponse'
      '400':
        $ref: '../components/responses.yaml#/BadRequest'
      '404':
        $ref: '../components/responses.yaml#/NotFound'
```

In `docs/openapi/openapi.yaml` `paths:` add after the `.../streams/{streamId}/routes` entry:

```yaml
  /projects/{projectId}/streams/{streamId}/routes/preview:
    $ref: 'paths/stream-routes.yaml#/stream-routes-preview'
```

and after the `.../routes/{routeId}/yamls` stream entry:

```yaml
  /projects/{projectId}/streams/{streamId}/routes/{routeId}/preview:
    $ref: 'paths/stream-routes.yaml#/stream-route-preview'
  /projects/{projectId}/streams/{streamId}/routes/{routeId}/preview-delete:
    $ref: 'paths/stream-routes.yaml#/stream-route-preview-delete'
```

Regenerate: `make mocks && make openapi`

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/services/ -run 'TestPreview|TestRouteYAML_L4_Preview' -count=1 -v && go test ./internal/services/ ./internal/handlers/ ./cmd/server/ -count=1 && make openapi-check`
Expected: PASS. `TestPreviewCreateForStream_SameErrorsAsCreateForStream` passes all nine cases: the preview error string equals create's, and `routeRepo.Create` is never called.

- [ ] **Step 5: Commit**

```bash
git add internal/services/route_write_service.go internal/services/route_preview_l4.go internal/services/route_preview_l4_test.go internal/services/route_yaml.go internal/services/route_yaml_l4.go internal/handlers/ cmd/server/main.go cmd/server/openapi.yaml docs/openapi/ internal/mocks/
git commit -m "feat(routes): L4 create/update/delete manifest previews

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
make mocks-check
```

---

### Task 9: Version endpoints under the stream

**Files:**
- Modify: `internal/handlers/route_stream_handler.go` (`ForStreamRoute`)
- Modify: `cmd/server/main.go` (three routes)
- Modify: `docs/openapi/paths/stream-routes.yaml`, `docs/openapi/openapi.yaml`; regenerate `cmd/server/openapi.yaml`
- Test: `internal/handlers/route_stream_versions_handler_test.go` (new)

**Interfaces:**
- Consumes: `requireStreamRoute`; the unchanged `RouteVersionHandler.List/Get/Rollback` (they read only `:projectId`, `:routeId`, `:version`).
- Produces: `func (h *RouteHandler) ForStreamRoute(next gin.HandlerFunc) gin.HandlerFunc` (Ruling R7); routes `GET …/streams/:streamId/routes/:routeId/versions`, `GET …/versions/:version`, `POST …/versions/:version/rollback`. A route of another stream → 404 before the version handler runs. No service change: rollback resubmits through `routeWrite.Update`, which already handles L4.

- [ ] **Step 1: Write the failing test**

Create `internal/handlers/route_stream_versions_handler_test.go`:

```go
package handlers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/handlers"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// streamVersionsRouter mounts the version endpoints under the stream the way
// cmd/server/main.go does: each RouteVersionHandler method behind
// RouteHandler.ForStreamRoute.
func streamVersionsRouter(rh *handlers.RouteHandler, vh *handlers.RouteVersionHandler, user *models.User) *gin.Engine {
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("user", user) })
	g := router.Group("/projects/:projectId/streams/:streamId/routes")
	g.GET("/:routeId/versions", rh.ForStreamRoute(vh.List))
	g.GET("/:routeId/versions/:version", rh.ForStreamRoute(vh.Get))
	g.POST("/:routeId/versions/:version/rollback", rh.ForStreamRoute(vh.Rollback))
	return router
}

func TestRouteHandler_StreamVersions_DelegateForARouteOfTheStream(t *testing.T) {
	mockRoute, mockRV, mockAudit := new(mocks.MockRouteService), new(mocks.MockRouteVersionService), new(mocks.MockAuditService)
	rh := handlers.NewRouteHandler(mockRoute, mockAudit, routePermChecker())
	vh := handlers.NewRouteVersionHandler(mockRV, mockAudit)
	user := testUser()
	projectID, streamID, routeID := uuid.New(), uuid.New(), uuid.New()
	mockRoute.On("GetForStream", projectID, streamID, routeID).Return(&models.Route{ID: routeID, StreamID: &streamID}, nil)
	mockRV.On("ListVersions", routeID, 1, 20).Return([]models.RouteVersion{{ID: uuid.New(), RouteID: routeID, Version: 1}}, int64(1), nil)
	mockRV.On("GetVersion", routeID, 1).Return(&models.RouteVersion{ID: uuid.New(), RouteID: routeID, Version: 1}, nil)
	mockRV.On("Rollback", routeID, 1, user.ID).Return(&models.Route{ID: routeID, Name: "pg"}, nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, "rollback", "route", mock.Anything, "pg", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	router := streamVersionsRouter(rh, vh, user)
	base := fmt.Sprintf("/projects/%s/streams/%s/routes/%s", projectID, streamID, routeID)

	w := doJSON(router, "GET", base+"/versions", nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var list map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &list))
	assert.Len(t, list["data"], 1)

	assert.Equal(t, http.StatusOK, doJSON(router, "GET", base+"/versions/1", nil).Code)
	assert.Equal(t, http.StatusOK, doJSON(router, "POST", base+"/versions/1/rollback", nil).Code)
	mockRV.AssertExpectations(t)
}

func TestRouteHandler_StreamVersions_RouteOfOtherStreamIsNotFound(t *testing.T) {
	mockRoute, mockRV := new(mocks.MockRouteService), new(mocks.MockRouteVersionService)
	rh := handlers.NewRouteHandler(mockRoute, new(mocks.MockAuditService), routePermChecker())
	vh := handlers.NewRouteVersionHandler(mockRV, new(mocks.MockAuditService))
	projectID, streamID, routeID := uuid.New(), uuid.New(), uuid.New()
	mockRoute.On("GetForStream", projectID, streamID, routeID).Return(nil, services.ErrRouteNotFound)
	router := streamVersionsRouter(rh, vh, testUser())
	base := fmt.Sprintf("/projects/%s/streams/%s/routes/%s", projectID, streamID, routeID)

	assert.Equal(t, http.StatusNotFound, doJSON(router, "GET", base+"/versions", nil).Code)
	assert.Equal(t, http.StatusNotFound, doJSON(router, "GET", base+"/versions/1", nil).Code)
	assert.Equal(t, http.StatusNotFound, doJSON(router, "POST", base+"/versions/1/rollback", nil).Code)
	mockRV.AssertNotCalled(t, "ListVersions", mock.Anything, mock.Anything, mock.Anything)
	mockRV.AssertNotCalled(t, "GetVersion", mock.Anything, mock.Anything)
	mockRV.AssertNotCalled(t, "Rollback", mock.Anything, mock.Anything, mock.Anything)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/handlers/ -run StreamVersions -count=1`
Expected: FAIL to build: `rh.ForStreamRoute undefined (type *handlers.RouteHandler has no field or method ForStreamRoute)`.

- [ ] **Step 3: Write minimal implementation**

Append to `internal/handlers/route_stream_handler.go`:

```go
// ForStreamRoute wraps a route-scoped handler that reads only :projectId and
// :routeId (the RouteVersionHandler methods) so it runs only for an L4 route
// of :streamId in :projectId; any other route is 404 before next runs. It is
// how the version history endpoints are mounted under the stream:
//
//	streamRoutes.GET("/:routeId/versions", deps.RouteHandler.ForStreamRoute(deps.RouteVersionHandler.List))
func (h *RouteHandler) ForStreamRoute(next gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if h.requireStreamRoute(c) {
			next(c)
		}
	}
}
```

In `cmd/server/main.go`, below `streamRoutes.POST("/:routeId/deploy", deps.RouteHandler.DeployForStream) // ...`:

```go

						// Version history of an L4 route: the domain route
						// endpoints, guarded so a route of another stream is 404.
						streamRoutes.GET("/:routeId/versions", deps.RouteHandler.ForStreamRoute(deps.RouteVersionHandler.List))
						streamRoutes.GET("/:routeId/versions/:version", deps.RouteHandler.ForStreamRoute(deps.RouteVersionHandler.Get))
						streamRoutes.POST("/:routeId/versions/:version/rollback", deps.RouteHandler.ForStreamRoute(deps.RouteVersionHandler.Rollback))
```

(`setupRouter` runs with zero-value handler structs in `TestRouteSpecParity`. `ForStreamRoute` only builds a closure at registration, so that is safe.)

Append to `docs/openapi/paths/stream-routes.yaml`:

```yaml
stream-route-versions:
  get:
    tags: [Streams]
    summary: List an L4 route's version history
    description: A version is recorded on every successful create/update deploy. A route of another stream is 404.
    operationId: listStreamRouteVersions
    security:
      - bearerAuth: []
    parameters:
      - $ref: '../components/parameters.yaml#/projectId'
      - $ref: '../components/parameters.yaml#/streamId'
      - $ref: '../components/parameters.yaml#/routeId'
      - $ref: '../components/parameters.yaml#/page'
      - $ref: '../components/parameters.yaml#/limit'
    responses:
      '200':
        description: Route version history
        content:
          application/json:
            schema:
              $ref: '../schemas/route-version.yaml#/RouteVersionList'
      '404':
        $ref: '../components/responses.yaml#/NotFound'

stream-route-version:
  get:
    tags: [Streams]
    summary: Get a specific version of an L4 route
    operationId: getStreamRouteVersion
    security:
      - bearerAuth: []
    parameters:
      - $ref: '../components/parameters.yaml#/projectId'
      - $ref: '../components/parameters.yaml#/streamId'
      - $ref: '../components/parameters.yaml#/routeId'
      - name: version
        in: path
        required: true
        schema:
          type: integer
    responses:
      '200':
        description: Route version details
        content:
          application/json:
            schema:
              $ref: '../schemas/route-version.yaml#/RouteVersion'
      '404':
        $ref: '../components/responses.yaml#/NotFound'

stream-route-version-rollback:
  post:
    tags: [Streams]
    summary: Roll an L4 route back to a previous version (submits for approval)
    operationId: rollbackStreamRouteVersion
    security:
      - bearerAuth: []
    parameters:
      - $ref: '../components/parameters.yaml#/projectId'
      - $ref: '../components/parameters.yaml#/streamId'
      - $ref: '../components/parameters.yaml#/routeId'
      - name: version
        in: path
        required: true
        schema:
          type: integer
    responses:
      '200':
        description: Rollback submitted as a route update
        content:
          application/json:
            schema:
              $ref: '../schemas/route.yaml#/Route'
      '400':
        $ref: '../components/responses.yaml#/BadRequest'
      '404':
        $ref: '../components/responses.yaml#/NotFound'
```

In `docs/openapi/openapi.yaml` `paths:`, after the `.../streams/{streamId}/routes/{routeId}/deploy` entry:

```yaml
  /projects/{projectId}/streams/{streamId}/routes/{routeId}/versions:
    $ref: 'paths/stream-routes.yaml#/stream-route-versions'
  /projects/{projectId}/streams/{streamId}/routes/{routeId}/versions/{version}:
    $ref: 'paths/stream-routes.yaml#/stream-route-version'
  /projects/{projectId}/streams/{streamId}/routes/{routeId}/versions/{version}/rollback:
    $ref: 'paths/stream-routes.yaml#/stream-route-version-rollback'
```

Regenerate: `make openapi`

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/handlers/ -run StreamVersions -count=1 -v && go test ./internal/handlers/ ./cmd/server/ -count=1 && make openapi-check`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/handlers/route_stream_handler.go internal/handlers/route_stream_versions_handler_test.go cmd/server/main.go cmd/server/openapi.yaml docs/openapi/
git commit -m "feat(routes): L4 route version history under the stream

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
make mocks-check
```

---

### Task 10: Approval diff for L4 + `routeKind` + `streamId`/`streamName`

**Files:**
- Modify: `internal/repository/stream_repository.go` (`GetByIDs`)
- Modify: `internal/models/approval.go` (two computed fields)
- Modify: `internal/services/approval_service.go` (deps, enrichment, `ApprovalDiffResult.RouteKind`, L4 branch, remove `ErrDiffNotAvailableForL4`)
- Modify: `internal/services/approval_service_test.go` (helpers gain the two deps; delete `TestApprovalService_GetDiff_L4Route_NoPanic`)
- Modify: `cmd/server/main.go` (wire `Streams`, `L4Manifests`)
- Modify: `docs/openapi/schemas/approval.yaml`, `docs/openapi/paths/approvals.yaml`; regenerate `cmd/server/openapi.yaml`
- Regenerate: `internal/mocks/mock_services.go` (`MockApprovalStreamReader`, `MockL4ManifestRenderer`)
- Test: `internal/repository/stream_getbyids_repository_test.go` (new), `internal/services/approval_service_l4_test.go` (new), `internal/handlers/approval_handler_l4_test.go` (new)

**Interfaces:**
- Consumes: Task 6 `(*RouteService).RenderL4RouteYAML`, `(*RouteService).RenderL4BackendTrafficPolicyYAML`; `routeplan.GetRouteKind`; `models.RouteApprovalSnapshot`. Test helpers: `newApprovalEngine`, `newTestApprovalService` (`approval_service_test.go`), `newTestRouteService`, `tcpBTPConfig`.
- Produces: `(*StreamRepository).GetByIDs(ids []uuid.UUID) ([]models.Stream, error)` (empty input → `nil, nil`, no query); `models.Approval.StreamID *uuid.UUID` (`json:"streamId,omitempty"`, `gorm:"-"`) and `models.Approval.StreamName string` (`json:"streamName,omitempty"`, `gorm:"-"`); `services.ApprovalStreamReader{GetByID; GetByIDs}`; `services.L4ManifestRenderer{RenderL4RouteYAML; RenderL4BackendTrafficPolicyYAML}`; `ApprovalServiceDeps.Streams`, `ApprovalServiceDeps.L4Manifests` (both required: the constructor panics with `missing required dependency: Streams` / `L4Manifests`); `ApprovalDiffResult.RouteKind string` (`json:"routeKind"`).

- [ ] **Step 1: Write the failing test**

Create `internal/repository/stream_getbyids_repository_test.go`:

```go
package repository_test

import (
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStreamRepository_GetByIDs(t *testing.T) {
	db := requirePostgres(t)
	projectID, _, _, userID := seedProject(t, db)
	tmplID := insertTemplate(t, db, projectID, userID, true)
	repo := repository.NewStreamRepository(db)
	a := &models.Stream{ProjectID: projectID, Name: "ids-a", Namespace: "fastgateway-system", GatewayTemplateID: tmplID}
	b := &models.Stream{ProjectID: projectID, Name: "ids-b", Namespace: "fastgateway-system", GatewayTemplateID: tmplID}
	require.NoError(t, repo.Create(a))
	require.NoError(t, repo.Create(b))

	got, err := repo.GetByIDs([]uuid.UUID{a.ID, b.ID, uuid.New()})
	require.NoError(t, err)
	names := []string{}
	for _, s := range got {
		names = append(names, s.Name)
	}
	assert.ElementsMatch(t, []string{"ids-a", "ids-b"}, names, "unknown IDs are skipped")

	none, err := repo.GetByIDs(nil)
	require.NoError(t, err)
	assert.Nil(t, none)
}
```

Create `internal/services/approval_service_l4_test.go`. It uses a real `RouteService` as the renderer, so every asserted YAML is the deploy builders' output:

```go
package services_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// l4ApprovalFixture is an ApprovalService whose L4 renderer is a real
// RouteService (so the diff is the deploy builders' output) and whose stream
// reader is a mock.
type l4ApprovalFixture struct {
	svc          *services.ApprovalService
	approvalRepo *mocks.MockUnifiedApprovalRepository
	routeRepo    *mocks.MockRouteRepository
	streams      *mocks.MockApprovalStreamReader
	stream       *models.Stream
}

func newL4ApprovalFixture(t *testing.T) *l4ApprovalFixture {
	t.Helper()
	renderer, _, _, _, _, _ := newTestRouteService()
	f := &l4ApprovalFixture{
		approvalRepo: new(mocks.MockUnifiedApprovalRepository),
		routeRepo:    new(mocks.MockRouteRepository),
		streams:      new(mocks.MockApprovalStreamReader),
		stream: &models.Stream{ID: uuid.New(), ProjectID: uuid.New(), Name: "test-stream", Namespace: "edge",
			K8sGatewayName: "str-test-stream", K8sGatewayClass: "shared-envoy-clusterip"},
	}
	f.svc = services.NewApprovalService(services.ApprovalServiceDeps{
		ApprovalRepo: f.approvalRepo,
		PolicyRepo:   new(mocks.MockApprovalPolicyRepository),
		RouteRepo:    f.routeRepo,
		DomainRepo:   new(mocks.MockDomainRepository), // bare: an L4 diff must never read a domain
		Approvals:    newApprovalEngine(f.approvalRepo, nil, nil, f.routeRepo),
		Streams:      f.streams,
		L4Manifests:  renderer,
	})
	f.streams.On("GetByID", f.stream.ID).Return(f.stream, nil).Maybe()
	return f
}

func (f *l4ApprovalFixture) route(port int) *models.Route {
	sid := f.stream.ID
	r := &models.Route{ID: uuid.New(), StreamID: &sid, Name: "postgres", K8sRouteName: "postgres-abcd1234",
		Protocol: models.RouteProtocolTCP, Status: models.RouteStatusPendingUpdate,
		Config: l4Config(port)}
	f.routeRepo.On("GetByID", r.ID).Return(r, nil)
	return r
}

func l4Config(port int) models.RouteConfig {
	return models.RouteConfig{ListenerPort: port, Backends: []models.RouteBackend{
		{Type: models.BackendTypeKubernetes, Service: "pg", Namespace: "default", Port: 5432},
	}}
}

func snapshotJSON(t *testing.T, cfg *models.RouteConfig, btp *models.BackendTrafficPolicyConfig) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(models.RouteApprovalSnapshot{RouteConfig: cfg, BackendTrafficPolicy: btp})
	require.NoError(t, err)
	return raw
}

func (f *l4ApprovalFixture) approval(route *models.Route, action models.ApprovalAction, snapshot, previous json.RawMessage) uuid.UUID {
	id := uuid.New()
	f.approvalRepo.On("GetByID", id).Return(&models.Approval{
		ID: id, EntityType: models.ApprovalEntityRoute, EntityID: route.ID, Action: action,
		ConfigSnapshot: snapshot, PreviousConfig: previous, ChangeDescription: "bump port",
	}, nil)
	return id
}

func TestApprovalService_GetDiff_L4Create(t *testing.T) {
	f := newL4ApprovalFixture(t)
	route := f.route(5432)
	cfg, btp := l4Config(5432), tcpBTPConfig()
	id := f.approval(route, models.ApprovalActionCreate, snapshotJSON(t, &cfg, &btp), nil)

	diff, err := f.svc.GetDiff(id)
	require.NoError(t, err, "an L4 diff is a 200, not a 500")

	assert.Equal(t, "create", diff.Action)
	assert.Equal(t, "TCPRoute", diff.RouteKind)
	assert.Empty(t, diff.CurrentYAML)
	assert.Contains(t, diff.ProposedYAML, "kind: TCPRoute")
	assert.Contains(t, diff.ProposedYAML, "sectionName: l4-tcp-5432")
	assert.Contains(t, diff.ProposedYAML, "name: str-test-stream")
	assert.Contains(t, diff.ProposedBackendTrafficPolicyYAML, "kind: BackendTrafficPolicy")
	assert.Contains(t, diff.ProposedBackendTrafficPolicyYAML, "namespace: edge")
	assert.Empty(t, diff.CurrentBackendTrafficPolicyYAML)
	assert.Equal(t, "bump port", diff.ChangeDescription)
}

func TestApprovalService_GetDiff_L4Update_CurrentFromPreviousSnapshot(t *testing.T) {
	f := newL4ApprovalFixture(t)
	// The route row already carries the proposed port (as after approval);
	// current must come from the previous snapshot, not the row.
	route := f.route(6432)
	prevCfg, prevBTP := l4Config(5432), tcpBTPConfig()
	newCfg := l4Config(6432)
	id := f.approval(route, models.ApprovalActionUpdate, snapshotJSON(t, &newCfg, nil), snapshotJSON(t, &prevCfg, &prevBTP))

	diff, err := f.svc.GetDiff(id)
	require.NoError(t, err)

	assert.Equal(t, "TCPRoute", diff.RouteKind)
	assert.Contains(t, diff.CurrentYAML, "sectionName: l4-tcp-5432")
	assert.Contains(t, diff.ProposedYAML, "sectionName: l4-tcp-6432")
	assert.Contains(t, diff.CurrentBackendTrafficPolicyYAML, "maxConnections: 100", "the policy the update removes")
	assert.Empty(t, diff.ProposedBackendTrafficPolicyYAML)
}

func TestApprovalService_GetDiff_L4Delete_CurrentFromLiveRoute(t *testing.T) {
	f := newL4ApprovalFixture(t)
	route := f.route(5432)
	route.Protocol = models.RouteProtocolUDP
	btp := models.BackendTrafficPolicyConfig{LoadBalancer: &models.LoadBalancerConfig{Type: models.LoadBalancerTypeRoundRobin}}
	cfg := route.Config
	id := f.approval(route, models.ApprovalActionDelete, snapshotJSON(t, &cfg, &btp), nil)

	diff, err := f.svc.GetDiff(id)
	require.NoError(t, err)

	assert.Equal(t, "delete", diff.Action)
	assert.Equal(t, "UDPRoute", diff.RouteKind)
	assert.Contains(t, diff.CurrentYAML, "kind: UDPRoute")
	assert.Contains(t, diff.CurrentBackendTrafficPolicyYAML, "kind: BackendTrafficPolicy")
	assert.Empty(t, diff.ProposedYAML)
	assert.Empty(t, diff.ProposedBackendTrafficPolicyYAML)
}

func TestApprovalService_GetDiff_L4StreamLookupFailureIsAnError(t *testing.T) {
	f := newL4ApprovalFixture(t)
	missing := uuid.New()
	r := &models.Route{ID: uuid.New(), StreamID: &missing, Protocol: models.RouteProtocolTCP, Config: l4Config(5432)}
	f.routeRepo.On("GetByID", r.ID).Return(r, nil)
	f.streams.On("GetByID", missing).Return(nil, errors.New("record not found"))
	cfg := l4Config(5432)
	id := f.approval(r, models.ApprovalActionCreate, snapshotJSON(t, &cfg, nil), nil)

	diff, err := f.svc.GetDiff(id)
	assert.Nil(t, diff, "never a partial diff")
	assert.ErrorContains(t, err, "failed to load stream for L4 route")
}

func TestApprovalService_GetDiff_RouteKindForDomainRoutes(t *testing.T) {
	for proto, kind := range map[models.RouteProtocol]string{
		models.RouteProtocolHTTP: "HTTPRoute",
		models.RouteProtocolGRPC: "GRPCRoute",
	} {
		approvalRepo, routeRepo, domainRepo := new(mocks.MockUnifiedApprovalRepository), new(mocks.MockRouteRepository), new(mocks.MockDomainRepository)
		svc := newTestApprovalService(approvalRepo, nil, nil, routeRepo, nil, domainRepo, routeplan.WAFConfig{})
		domainID, routeID, approvalID := uuid.New(), uuid.New(), uuid.New()
		cfg := models.RouteConfig{Matches: []models.RouteMatch{{Path: &models.PathMatch{Value: "/x"}}}}
		approvalRepo.On("GetByID", approvalID).Return(&models.Approval{ID: approvalID, EntityType: models.ApprovalEntityRoute,
			EntityID: routeID, Action: models.ApprovalActionCreate, ConfigSnapshot: snapshotJSON(t, &cfg, nil)}, nil)
		routeRepo.On("GetByID", routeID).Return(&models.Route{ID: routeID, DomainID: &domainID, Protocol: proto, K8sRouteName: "r"}, nil)
		domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, Hostname: "example.com", Namespace: "fastgateway-system"}, nil)

		diff, err := svc.GetDiff(approvalID)
		require.NoError(t, err)
		assert.Equal(t, kind, diff.RouteKind)
	}
}

func TestApprovalService_ListByProjectID_EnrichesStream(t *testing.T) {
	f := newL4ApprovalFixture(t)
	projectID, domainID := uuid.New(), uuid.New()
	sid := f.stream.ID
	l4, l7 := uuid.New(), uuid.New()
	f.approvalRepo.On("ListByProjectID", projectID, 1, 20, "", "").Return([]models.Approval{
		{ID: uuid.New(), EntityType: models.ApprovalEntityRoute, EntityID: l4},
		{ID: uuid.New(), EntityType: models.ApprovalEntityRoute, EntityID: l7},
	}, int64(2), nil)
	f.routeRepo.On("GetByIDs", mock.AnythingOfType("[]uuid.UUID")).Return([]models.Route{
		{ID: l4, Name: "postgres", StreamID: &sid, Protocol: models.RouteProtocolTCP},
		{ID: l7, Name: "api", DomainID: &domainID},
	}, nil)
	f.streams.On("GetByIDs", []uuid.UUID{sid}).Return([]models.Stream{*f.stream}, nil)

	// The fixture's domain repo is bare; this one answers the batch lookup the list does.
	svc := services.NewApprovalService(services.ApprovalServiceDeps{
		ApprovalRepo: f.approvalRepo, PolicyRepo: new(mocks.MockApprovalPolicyRepository), RouteRepo: f.routeRepo,
		DomainRepo: domainRepoWithHost(domainID, "api.example.com"), Approvals: newApprovalEngine(f.approvalRepo, nil, nil, f.routeRepo),
		Streams: f.streams, L4Manifests: new(mocks.MockL4ManifestRenderer),
	})
	got, _, err := svc.ListByProjectID(projectID, 1, 20, "", "")
	require.NoError(t, err)

	require.NotNil(t, got[0].StreamID)
	assert.Equal(t, sid, *got[0].StreamID)
	assert.Equal(t, "test-stream", got[0].StreamName)
	assert.Equal(t, "postgres", got[0].EntityName)
	assert.Empty(t, got[0].DomainName)
	assert.Nil(t, got[1].StreamID)
	assert.Equal(t, "api.example.com", got[1].DomainName)
	f.streams.AssertNumberOfCalls(t, "GetByIDs", 1)
}

func domainRepoWithHost(id uuid.UUID, host string) *mocks.MockDomainRepository {
	repo := new(mocks.MockDomainRepository)
	repo.On("GetByIDs", mock.AnythingOfType("[]uuid.UUID")).Return([]models.Domain{{ID: id, Hostname: host}}, nil)
	return repo
}

func TestApprovalService_GetByID_EnrichesStream(t *testing.T) {
	f := newL4ApprovalFixture(t)
	route := f.route(5432)
	id := uuid.New()
	f.approvalRepo.On("GetByID", id).Return(&models.Approval{ID: id, EntityType: models.ApprovalEntityRoute, EntityID: route.ID}, nil)

	got, err := f.svc.GetByID(id)
	require.NoError(t, err)
	require.NotNil(t, got.StreamID)
	assert.Equal(t, f.stream.ID, *got.StreamID)
	assert.Equal(t, "test-stream", got.StreamName)
}

func TestApproval_StreamFields_JSONContract(t *testing.T) {
	sid := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	raw, err := json.Marshal(models.Approval{StreamID: &sid, StreamName: "test-stream"})
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, "11111111-1111-1111-1111-111111111111", got["streamId"])
	assert.Equal(t, "test-stream", got["streamName"])

	raw, err = json.Marshal(models.Approval{})
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "streamId", "omitted for L7 and non-route approvals")
	assert.NotContains(t, string(raw), "streamName")

	raw, err = json.Marshal(services.ApprovalDiffResult{Action: "create", RouteKind: "TCPRoute"})
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"routeKind":"TCPRoute"`)
}

func TestApprovalService_GetDiff_L4CorruptSnapshotIsAnError(t *testing.T) {
	f := newL4ApprovalFixture(t)
	route := f.route(5432)
	id := f.approval(route, models.ApprovalActionCreate, json.RawMessage(`{"routeConfig":`), nil)

	diff, err := f.svc.GetDiff(id)
	assert.Nil(t, diff, "never a partial diff")
	assert.Error(t, err)
}
```

Create `internal/handlers/approval_handler_l4_test.go`:

```go
package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/handlers"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApprovalHandler_GetDiff_L4RouteIs200WithRouteKind(t *testing.T) {
	mockApproval := new(mocks.MockApprovalService)
	h := handlers.NewApprovalHandler(mockApproval, new(mocks.MockAuditService))
	id := uuid.New()
	mockApproval.On("GetDiff", id).Return(&services.ApprovalDiffResult{
		Action: "update", RouteKind: "TCPRoute", CurrentYAML: "kind: TCPRoute\n", ProposedYAML: "kind: TCPRoute\n",
	}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/approvals/"+id.String()+"/diff", nil)
	c.Params = gin.Params{{Key: "approvalId", Value: id.String()}}
	h.GetDiff(c)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "TCPRoute", body["routeKind"])
}

func TestApprovalHandler_Get_CarriesStreamFields(t *testing.T) {
	mockApproval := new(mocks.MockApprovalService)
	h := handlers.NewApprovalHandler(mockApproval, new(mocks.MockAuditService))
	id, sid := uuid.New(), uuid.New()
	mockApproval.On("GetByID", id).Return(&models.Approval{ID: id, EntityType: models.ApprovalEntityRoute, StreamID: &sid, StreamName: "test-stream"}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/approvals/"+id.String(), nil)
	c.Params = gin.Params{{Key: "approvalId", Value: id.String()}}
	h.Get(c)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, sid.String(), body["streamId"])
	assert.Equal(t, "test-stream", body["streamName"])
}
```

In `internal/services/approval_service_test.go`:

1. In `newTestApprovalServiceWithEngine`, add to the `services.ApprovalServiceDeps{...}` literal:
   ```go
   		// Bare mocks: a test that reaches an L4 lookup or render without
   		// saying so fails on the unexpected call. The L4 tests build their
   		// own service (newL4ApprovalFixture).
   		Streams:     new(mocks.MockApprovalStreamReader),
   		L4Manifests: new(mocks.MockL4ManifestRenderer),
   ```
2. In `fullApprovalServiceDeps`, add `Streams: new(mocks.MockApprovalStreamReader),` and `L4Manifests: new(mocks.MockL4ManifestRenderer),`.
3. In `TestNewApprovalService_RequiresEveryDependency`'s `cases` map, add:
   ```go
   		"Streams":      func(d *services.ApprovalServiceDeps) { d.Streams = nil },
   		"L4Manifests":  func(d *services.ApprovalServiceDeps) { d.L4Manifests = nil },
   ```
4. Delete `TestApprovalService_GetDiff_L4Route_NoPanic` and its two-line leading comment. It asserts the removed `ErrDiffNotAvailableForL4`; `TestApprovalService_GetDiff_L4Create` replaces it.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/services/ -run 'TestApprovalService|TestNewApprovalService|TestApproval_' -count=1`
Expected: FAIL to build: `unknown field Streams in struct literal of type services.ApprovalServiceDeps`, `undefined: mocks.MockApprovalStreamReader`, `diff.RouteKind undefined`.

- [ ] **Step 3: Write minimal implementation**

In `internal/repository/stream_repository.go`, insert above `// ListByProjectID lists all streams in a project, ordered by name`:

```go
// GetByIDs gets the streams with the given IDs in one query (approval
// enrichment uses it, so listing approvals never issues a query per stream).
// An empty input returns nil without querying.
func (r *StreamRepository) GetByIDs(ids []uuid.UUID) ([]models.Stream, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var streams []models.Stream
	err := r.db.Where("id IN ?", ids).Find(&streams).Error
	return streams, err
}
```

In `internal/models/approval.go`, replace the two computed fields at the end of `Approval`:

```go
	EntityName string `gorm:"-" json:"entityName,omitempty"`
	DomainName string `gorm:"-" json:"domainName,omitempty"`
```

with:

```go
	EntityName string     `gorm:"-" json:"entityName,omitempty"`
	DomainName string     `gorm:"-" json:"domainName,omitempty"`
	StreamID   *uuid.UUID `gorm:"-" json:"streamId,omitempty"`   // L4 route approvals: the route's stream
	StreamName string     `gorm:"-" json:"streamName,omitempty"` // L4 route approvals: the stream's name
```

In `internal/services/approval_service.go`:

1. Add `"fmt"` to the imports.
2. Replace everything from `// ApprovalService handles approval business logic using the unified approval system` through the end of `GetByID` with:

```go
// ApprovalService handles approval business logic using the unified approval system
type ApprovalService struct {
	approvalRepo repository.UnifiedApprovalRepositoryInterface
	policyRepo   repository.ApprovalPolicyRepositoryInterface
	routeRepo    repository.RouteRepositoryInterface
	domainRepo   repository.DomainRepositoryInterface
	wafConfig    routeplan.WAFConfig

	// streams resolves an L4 route's Stream; l4Manifests renders its
	// TCPRoute/UDPRoute and BackendTrafficPolicy. Both are required.
	streams     ApprovalStreamReader
	l4Manifests L4ManifestRenderer

	// engine owns approval planning and traversal for every entity type.
	// A required constructor dependency since Phase 2E Task 6.
	// See internal/approval.
	engine *approvalpkg.Engine
}

// ApprovalServiceDeps carries everything ApprovalService needs. Every field
// is required.
//
// Phase 2E Task 7 deleted six of them -- K8sService, TeamRepo, ProjectRepo,
// SecurityPolicyRepo, BackendTrafficPolicyRepo and StageReviewRepo. Each was
// stored in a field that nothing in this file ever read (K8sService held a
// full 58-method Kubernetes interface and called none of it). A dead field
// the constructor *requires* is worse than the setter it replaced: main.go
// has to wire something nothing reads, and the RequiresEveryDependency test
// asserts a requirement that does not exist.
type ApprovalServiceDeps struct {
	ApprovalRepo repository.UnifiedApprovalRepositoryInterface
	PolicyRepo   repository.ApprovalPolicyRepositoryInterface
	RouteRepo    repository.RouteRepositoryInterface
	DomainRepo   repository.DomainRepositoryInterface
	WafConfig    routeplan.WAFConfig

	// Approvals owns approval planning and traversal. The engine calls back
	// into the entity services as Completers, so main.go builds the engine
	// first and registers the completers afterwards.
	Approvals *approvalpkg.Engine

	// Streams resolves the Stream of an L4 route approval, for the diff and
	// for the streamId/streamName enrichment. *repository.StreamRepository
	// satisfies it.
	Streams ApprovalStreamReader

	// L4Manifests renders an L4 route's TCPRoute/UDPRoute and
	// BackendTrafficPolicy through the deploy builders, for the L4 diff.
	// *RouteService satisfies it.
	L4Manifests L4ManifestRenderer
}

// ApprovalStreamReader is the slice of StreamRepository ApprovalService uses.
type ApprovalStreamReader interface {
	GetByID(id uuid.UUID) (*models.Stream, error)
	GetByIDs(ids []uuid.UUID) ([]models.Stream, error)
}

// L4ManifestRenderer renders an L4 route's manifests exactly as the deploy
// path builds them. *RouteService satisfies it.
type L4ManifestRenderer interface {
	RenderL4RouteYAML(route *models.Route, stream *models.Stream) (string, error)
	RenderL4BackendTrafficPolicyYAML(route *models.Route, stream *models.Stream, cfg *models.BackendTrafficPolicyConfig) (string, error)
}

// NewApprovalService builds a fully-wired ApprovalService. It panics if a
// required dependency is missing: before Phase 2E these arrived through
// setters after construction, so a forgotten wiring line degraded silently
// at runtime instead of failing at start-up. Master design section 6.6.
func NewApprovalService(deps ApprovalServiceDeps) *ApprovalService {
	var missing []string
	if deps.ApprovalRepo == nil {
		missing = append(missing, "ApprovalRepo")
	}
	if deps.PolicyRepo == nil {
		missing = append(missing, "PolicyRepo")
	}
	if deps.RouteRepo == nil {
		missing = append(missing, "RouteRepo")
	}
	if deps.DomainRepo == nil {
		missing = append(missing, "DomainRepo")
	}
	if deps.Approvals == nil {
		missing = append(missing, "Approvals")
	}
	if deps.Streams == nil {
		missing = append(missing, "Streams")
	}
	if deps.L4Manifests == nil {
		missing = append(missing, "L4Manifests")
	}
	if len(missing) > 0 {
		panic("services.NewApprovalService: missing required dependency: " + strings.Join(missing, ", "))
	}

	return &ApprovalService{
		approvalRepo: deps.ApprovalRepo,
		policyRepo:   deps.PolicyRepo,
		routeRepo:    deps.RouteRepo,
		domainRepo:   deps.DomainRepo,
		wafConfig:    deps.WafConfig,
		engine:       deps.Approvals,
		streams:      deps.Streams,
		l4Manifests:  deps.L4Manifests,
	}
}

// GetByID gets an approval by ID. A route approval for an L4 route also
// carries StreamID/StreamName, as in ListByProjectID; that enrichment is
// best-effort like DomainName (a lookup failure leaves the names empty).
func (s *ApprovalService) GetByID(id uuid.UUID) (*models.Approval, error) {
	approval, err := s.approvalRepo.GetByID(id)
	if err != nil {
		return nil, err
	}
	if approval.EntityType == models.ApprovalEntityRoute {
		if route, err := s.routeRepo.GetByID(approval.EntityID); err == nil && route != nil && route.StreamID != nil {
			sid := *route.StreamID
			approval.StreamID = &sid
			if stream, err := s.streams.GetByID(sid); err == nil && stream != nil {
				approval.StreamName = stream.Name
			}
		}
	}
	return approval, nil
}
```

3. Replace `ListByProjectID` with (new: the `streamIDSet` collection, the one batched `GetByIDs`, and the `StreamID`/`StreamName` assignment):

```go
// ListByProjectID lists approvals for a project with an optional entity type filter
func (s *ApprovalService) ListByProjectID(projectID uuid.UUID, page, limit int, status string, entityType string) ([]models.Approval, int64, error) {
	approvals, total, err := s.approvalRepo.ListByProjectID(projectID, page, limit, status, entityType)
	if err != nil {
		return nil, 0, err
	}

	// Collect unique route entity IDs
	routeIDSet := make(map[uuid.UUID]struct{})
	for i := range approvals {
		if approvals[i].EntityType == models.ApprovalEntityRoute {
			routeIDSet[approvals[i].EntityID] = struct{}{}
		}
	}

	if len(routeIDSet) == 0 {
		return approvals, total, nil
	}

	routeIDs := make([]uuid.UUID, 0, len(routeIDSet))
	for id := range routeIDSet {
		routeIDs = append(routeIDs, id)
	}

	// Batch-fetch routes
	routes, err := s.routeRepo.GetByIDs(routeIDs)
	if err != nil {
		// Non-fatal: return approvals without enrichment
		return approvals, total, nil
	}

	routeMap := make(map[uuid.UUID]models.Route, len(routes))
	domainIDSet := make(map[uuid.UUID]struct{})
	streamIDSet := make(map[uuid.UUID]struct{})
	for _, r := range routes {
		routeMap[r.ID] = r
		if r.DomainID != nil {
			domainIDSet[*r.DomainID] = struct{}{}
		}
		if r.StreamID != nil {
			streamIDSet[*r.StreamID] = struct{}{}
		}
	}

	// Batch-fetch domains
	domainIDs := make([]uuid.UUID, 0, len(domainIDSet))
	for id := range domainIDSet {
		domainIDs = append(domainIDs, id)
	}

	domainMap := make(map[uuid.UUID]models.Domain)
	domains, err := s.domainRepo.GetByIDs(domainIDs)
	if err == nil {
		for _, d := range domains {
			domainMap[d.ID] = d
		}
	}

	// Batch-fetch streams (one query, only when an L4 route is listed)
	streamMap := make(map[uuid.UUID]models.Stream)
	if len(streamIDSet) > 0 {
		streamIDs := make([]uuid.UUID, 0, len(streamIDSet))
		for id := range streamIDSet {
			streamIDs = append(streamIDs, id)
		}
		if streams, err := s.streams.GetByIDs(streamIDs); err == nil {
			for _, st := range streams {
				streamMap[st.ID] = st
			}
		}
	}

	// Enrich approvals
	for i := range approvals {
		if approvals[i].EntityType == models.ApprovalEntityRoute {
			if route, ok := routeMap[approvals[i].EntityID]; ok {
				approvals[i].EntityName = route.Name
				if route.DomainID != nil {
					if domain, ok := domainMap[*route.DomainID]; ok {
						approvals[i].DomainName = domain.Hostname
					}
				}
				if route.StreamID != nil {
					sid := *route.StreamID
					approvals[i].StreamID = &sid
					if stream, ok := streamMap[sid]; ok {
						approvals[i].StreamName = stream.Name
					}
				}
			}
		}
	}

	return approvals, total, nil
}
```

4. Replace `ApprovalDiffResult` and the `ErrDiffNotAvailableForL4` declaration below it with:

```go
// ApprovalDiffResult represents the diff for an approval
type ApprovalDiffResult struct {
	Action                           string          `json:"action"`
	CurrentYAML                      string          `json:"currentYaml,omitempty"`
	ProposedYAML                     string          `json:"proposedYaml,omitempty"`
	CurrentSecurityPolicyYAML        string          `json:"currentSecurityPolicyYaml,omitempty"`
	ProposedSecurityPolicyYAML       string          `json:"proposedSecurityPolicyYaml,omitempty"`
	CurrentBackendTrafficPolicyYAML  string          `json:"currentBackendTrafficPolicyYaml,omitempty"`
	ProposedBackendTrafficPolicyYAML string          `json:"proposedBackendTrafficPolicyYaml,omitempty"`
	CurrentEnvoyExtensionPolicyYAML  string          `json:"currentEnvoyExtensionPolicyYaml,omitempty"`
	ProposedEnvoyExtensionPolicyYAML string          `json:"proposedEnvoyExtensionPolicyYaml,omitempty"`
	CurrentBackendYAML               string          `json:"currentBackendYaml,omitempty"`
	ProposedBackendYAML              string          `json:"proposedBackendYaml,omitempty"`
	ChangeDescription                string          `json:"changeDescription,omitempty"`
	AIReview                         json.RawMessage `json:"aiReview,omitempty"`
	// RouteKind is the Gateway API kind currentYaml/proposedYaml hold:
	// HTTPRoute, GRPCRoute, TCPRoute or UDPRoute.
	RouteKind string `json:"routeKind"`
}
```

5. In `GetDiff`, replace:

```go
	// An L4 route belongs to a Stream and has no Domain; the diff is built
	// from Domain-scoped HTTPRoute/policy YAML, which does not apply to it.
	if route.IsL4() || route.DomainID == nil {
		return nil, ErrDiffNotAvailableForL4
	}

	domain, err := s.domainRepo.GetByID(*route.DomainID)
	if err != nil {
		return nil, err
	}

	result := &ApprovalDiffResult{
		Action: string(approval.Action),
	}
```

with:

```go
	// An L4 route belongs to a Stream and has no Domain: its diff is the
	// TCPRoute/UDPRoute and BackendTrafficPolicy on the stream.
	if route.IsL4() {
		return s.l4Diff(approval, route)
	}
	if route.DomainID == nil {
		return nil, fmt.Errorf("route %s has no domain", route.ID)
	}

	domain, err := s.domainRepo.GetByID(*route.DomainID)
	if err != nil {
		return nil, err
	}

	result := &ApprovalDiffResult{
		Action:    string(approval.Action),
		RouteKind: routeplan.GetRouteKind(route.Protocol),
	}
```

6. Append `l4Diff` to the end of the file. Keep the local name `previousSnapshot`: a local named `previous` makes `previous.RouteConfig != nil` match `TestNoWiringNilGuardsRemain_NegatedForm`.

```go
// l4Diff is GetDiff for an L4 route. Every manifest is rendered through the
// deploy builders (L4ManifestRenderer) on the route's stream:
//
//   - create: proposed from the approval snapshot;
//   - update: current from the approval's previous snapshot (what was live
//     when the change was submitted), proposed from the snapshot. The stored
//     policy and the route row are not used for current: the update submit
//     already overwrote the stored policy with the proposed one, and
//     approving it copies the proposed config onto the row;
//   - delete: current from the live route and the policy the delete snapshot
//     captured.
//
// Any lookup or render failure is an error, never a partial diff.
func (s *ApprovalService) l4Diff(approval *models.Approval, route *models.Route) (*ApprovalDiffResult, error) {
	if route.StreamID == nil {
		return nil, fmt.Errorf("L4 route %s has no stream", route.ID)
	}
	stream, err := s.streams.GetByID(*route.StreamID)
	if err != nil {
		return nil, fmt.Errorf("failed to load stream for L4 route: %w", err)
	}

	var snapshot, previousSnapshot models.RouteApprovalSnapshot
	if approval.ConfigSnapshot != nil {
		if err := json.Unmarshal(approval.ConfigSnapshot, &snapshot); err != nil {
			return nil, err
		}
	}
	if approval.PreviousConfig != nil {
		if err := json.Unmarshal(approval.PreviousConfig, &previousSnapshot); err != nil {
			return nil, err
		}
	}

	render := func(cfg models.RouteConfig, btp *models.BackendTrafficPolicyConfig) (string, string, error) {
		temp := *route
		temp.Config = cfg
		routeYAML, err := s.l4Manifests.RenderL4RouteYAML(&temp, stream)
		if err != nil {
			return "", "", err
		}
		btpYAML, err := s.l4Manifests.RenderL4BackendTrafficPolicyYAML(&temp, stream, btp)
		if err != nil {
			return "", "", err
		}
		return routeYAML, btpYAML, nil
	}

	result := &ApprovalDiffResult{
		Action:            string(approval.Action),
		RouteKind:         routeplan.GetRouteKind(route.Protocol),
		ChangeDescription: approval.ChangeDescription,
		AIReview:          approval.AIReview,
	}
	switch approval.Action {
	case models.ApprovalActionCreate:
		if snapshot.RouteConfig != nil {
			if result.ProposedYAML, result.ProposedBackendTrafficPolicyYAML, err = render(*snapshot.RouteConfig, snapshot.BackendTrafficPolicy); err != nil {
				return nil, err
			}
		}
	case models.ApprovalActionUpdate:
		if previousSnapshot.RouteConfig != nil {
			if result.CurrentYAML, result.CurrentBackendTrafficPolicyYAML, err = render(*previousSnapshot.RouteConfig, previousSnapshot.BackendTrafficPolicy); err != nil {
				return nil, err
			}
		}
		if snapshot.RouteConfig != nil {
			if result.ProposedYAML, result.ProposedBackendTrafficPolicyYAML, err = render(*snapshot.RouteConfig, snapshot.BackendTrafficPolicy); err != nil {
				return nil, err
			}
		}
	case models.ApprovalActionDelete:
		if result.CurrentYAML, result.CurrentBackendTrafficPolicyYAML, err = render(route.Config, snapshot.BackendTrafficPolicy); err != nil {
			return nil, err
		}
	}
	return result, nil
}
```

In `cmd/server/main.go`, add to the `services.NewApprovalService(services.ApprovalServiceDeps{...})` literal (it is built after `routeService` and `streamRepo`):

```go
		Streams:      streamRepo,
		L4Manifests:  routeService,
```

OpenAPI. In `docs/openapi/schemas/approval.yaml`, add under `Approval.properties` after `domainName`:

```yaml
    streamId:
      type: string
      format: uuid
      description: Stream of an L4 (tcp/udp) route approval (computed, not stored; absent otherwise)
    streamName:
      type: string
      description: Name of the stream of an L4 route approval (computed, not stored; absent otherwise)
```

and in `ApprovalDiffResponse`, change `required: [action]` to `required: [action, routeKind]`, add after `action`:

```yaml
    routeKind:
      type: string
      enum: [HTTPRoute, GRPCRoute, TCPRoute, UDPRoute]
      description: Gateway API kind of the route in currentYaml/proposedYaml
```

and change the `currentYaml` / `proposedYaml` descriptions to `Current route YAML - HTTPRoute, GRPCRoute, TCPRoute or UDPRoute per routeKind (for update/delete)` / `Proposed route YAML - HTTPRoute, GRPCRoute, TCPRoute or UDPRoute per routeKind (for create/update)`. In `docs/openapi/paths/approvals.yaml`, under `approval-diff: get: responses:` after `'400'`, add:

```yaml
      '500':
        description: The approval, its route, or the route's domain or stream could not be read, or a snapshot is corrupt (never a partial diff)
        content:
          application/json:
            schema:
              $ref: '../schemas/common.yaml#/Error'
```

Regenerate: `make mocks && make openapi`

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/services/ -run 'TestApprovalService|TestNewApprovalService|TestApproval_|TestNoWiringNilGuardsRemain' -count=1 -v && go test ./internal/handlers/ -run TestApprovalHandler -count=1 && go test ./internal/repository/ -run TestStreamRepository_GetByIDs -count=1 -v && go test ./cmd/server/ -count=1 && make openapi-check && go build ./...`
Expected: PASS. `TestStreamRepository_GetByIDs` needs `INTEGRATION_DB_URL`; a SKIP does not count. `grep -rn ErrDiffNotAvailableForL4 --include='*.go' .` prints nothing.

- [ ] **Step 5: Commit**

```bash
git add internal/repository/stream_repository.go internal/repository/stream_getbyids_repository_test.go internal/models/approval.go internal/services/approval_service.go internal/services/approval_service_test.go internal/services/approval_service_l4_test.go internal/handlers/approval_handler_l4_test.go cmd/server/main.go cmd/server/openapi.yaml docs/openapi/ internal/mocks/
git commit -m "feat(approvals): L4 route diff, routeKind and stream name

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
make mocks-check
```

---

### Task 11: e2e coverage + full verification gate

**Files:**
- Create: `e2e/harness/stream_legibility.go`
- Create: `e2e/suites/stream/legibility_test.go`

**Interfaces:**
- Consumes: every endpoint from Tasks 4–10; harness `API.Do`, `pendingApprovals`, `routeVersionEnvelope` (`e2e/harness/api.go`), `Kube.GetUnstructured`, `NewStreamFixture`/`StreamFixture.Route` (its cleanup rejects a pending approval and then deletes), suite helpers `newStream`, `teamID`, `env`.
- Produces: harness methods `GetStreamEntrypoint`, `GetStreamYAMLs`, `ListStreamRouteVersions`, `UpdateStreamRoute`, `PendingRouteApprovalID`, `GetApprovalDiff`; helpers `GatewayListeners(obj map[string]interface{}) ([]GatewayListener, error)` and `GatewayYAMLListeners(yaml string) ([]GatewayListener, error)`. These compare listener identity `(name, protocol, port)` only, because the API server defaults other listener fields on the live object.

- [ ] **Step 1: Write the e2e harness additions and the test**

Create `e2e/harness/stream_legibility.go`:

```go
//go:build e2e

package harness

import (
	"context"
	"fmt"
	"net/http"
	"sort"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// GetStreamEntrypoint fetches GET /projects/:projectId/streams/:streamId/entrypoint.
func (a *API) GetStreamEntrypoint(ctx context.Context, projectID, streamID string) (services.StreamEntrypoint, error) {
	var out services.StreamEntrypoint
	_, err := a.Do(ctx, http.MethodGet, fmt.Sprintf("/projects/%s/streams/%s/entrypoint", projectID, streamID), nil, &out)
	return out, err
}

// GetStreamYAMLs fetches GET /projects/:projectId/streams/:streamId/yamls.
func (a *API) GetStreamYAMLs(ctx context.Context, projectID, streamID string) (services.StreamYAMLs, error) {
	var out services.StreamYAMLs
	_, err := a.Do(ctx, http.MethodGet, fmt.Sprintf("/projects/%s/streams/%s/yamls", projectID, streamID), nil, &out)
	return out, err
}

// ListStreamRouteVersions lists an L4 route's version history through the
// stream-scoped endpoint (same flat envelope as ListRouteVersions).
func (a *API) ListStreamRouteVersions(ctx context.Context, projectID, streamID, routeID string) ([]models.RouteVersion, int64, error) {
	var env routeVersionEnvelope
	path := fmt.Sprintf("/projects/%s/streams/%s/routes/%s/versions", projectID, streamID, routeID)
	if _, err := a.Do(ctx, http.MethodGet, path, nil, &env); err != nil {
		return nil, 0, err
	}
	return env.Data, env.Total, nil
}

// UpdateStreamRoute submits an update of an L4 route (approval-gated).
func (a *API) UpdateStreamRoute(ctx context.Context, projectID, streamID, routeID string, body any) (Route, error) {
	var out Route
	_, err := a.Do(ctx, http.MethodPut, fmt.Sprintf("/projects/%s/streams/%s/routes/%s", projectID, streamID, routeID), body, &out)
	return out, err
}

// PendingRouteApprovalID returns the ID of the pending approval for routeID.
func (a *API) PendingRouteApprovalID(ctx context.Context, projectID, routeID string) (string, error) {
	approvals, err := a.pendingApprovals(ctx, projectID, "")
	if err != nil {
		return "", err
	}
	for _, appr := range approvals {
		if appr.EntityID.String() == routeID {
			return appr.ID.String(), nil
		}
	}
	return "", fmt.Errorf("no pending approval found for route %s", routeID)
}

// GetApprovalDiff fetches GET /projects/:projectId/approvals/:approvalId/diff.
func (a *API) GetApprovalDiff(ctx context.Context, projectID, approvalID string) (services.ApprovalDiffResult, error) {
	var out services.ApprovalDiffResult
	_, err := a.Do(ctx, http.MethodGet, fmt.Sprintf("/projects/%s/approvals/%s/diff", projectID, approvalID), nil, &out)
	return out, err
}

// GatewayListener is the identity of one Gateway listener.
type GatewayListener struct {
	Name     string
	Protocol string
	Port     int64
}

// GatewayListeners returns spec.listeners of a Gateway object as sorted
// (name, protocol, port) identities. Only the identity is compared: the API
// server defaults other listener fields (e.g. allowedRoutes) on the live
// object, which the rendered manifest does not carry.
func GatewayListeners(obj map[string]interface{}) ([]GatewayListener, error) {
	raw, found, err := unstructured.NestedSlice(obj, "spec", "listeners")
	if err != nil || !found {
		return nil, fmt.Errorf("gateway has no spec.listeners (found=%v): %v", found, err)
	}
	out := make([]GatewayListener, 0, len(raw))
	for _, item := range raw {
		l, ok := item.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("listener is %T, want an object", item)
		}
		name, _, _ := unstructured.NestedString(l, "name")
		proto, _, _ := unstructured.NestedString(l, "protocol")
		port, _, _ := unstructured.NestedFieldNoCopy(l, "port")
		var p int64
		switch v := port.(type) {
		case int64:
			p = v
		case float64:
			p = int64(v)
		case int:
			p = int64(v)
		}
		out = append(out, GatewayListener{Name: name, Protocol: proto, Port: p})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// GatewayYAMLListeners is GatewayListeners for a rendered Gateway YAML.
func GatewayYAMLListeners(gatewayYAML string) ([]GatewayListener, error) {
	var obj map[string]interface{}
	if err := yaml.Unmarshal([]byte(gatewayYAML), &obj); err != nil {
		return nil, fmt.Errorf("parse gateway YAML: %w", err)
	}
	return GatewayListeners(obj)
}
```

Create `e2e/suites/stream/legibility_test.go` (spec "Backend e2e (L4 suite)"; ports 15400/15401 are unused by the other stream tests):

```go
//go:build e2e

package stream

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestStreamLegibility_ManifestsEntrypointVersionsAndDiff deploys a TCP route
// on a stream, then checks the read-only views against the live cluster:
// /yamls renders the Gateway listeners the cluster has, /entrypoint lists the
// route's port, /versions has the deploy's snapshot, and the approval diff of
// a following update is a 200 carrying TCPRoute YAML.
func TestStreamLegibility_ManifestsEntrypointVersionsAndDiff(t *testing.T) {
	s := newStream(t)
	fx := harness.NewStreamFixture(t, env)
	cfg := services.CreateRouteInput{
		Name:     harness.UniqueName(t),
		Protocol: models.RouteProtocolTCP,
		TeamID:   teamID(t),
		Config: models.RouteConfig{
			ListenerPort: 15400,
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: "default", Service: "l4-echo-a", Port: 9100, Weight: 100},
			},
		},
	}
	routeID := fx.Route(s, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	projectID, streamID := env.ProjectID, s.ID.String()

	// 1. The manifests' Gateway listeners are the live Gateway's listeners.
	yamls, err := env.Editor.GetStreamYAMLs(ctx, projectID, streamID)
	if err != nil {
		t.Fatalf("get stream yamls: %v", err)
	}
	rendered, err := harness.GatewayYAMLListeners(yamls.GatewayYAML)
	if err != nil {
		t.Fatalf("parse rendered gateway: %v", err)
	}
	live, err := env.Kube.GetUnstructured(ctx, kubernetes.GatewayGVR, s.Namespace, s.K8sGatewayName)
	if err != nil {
		t.Fatalf("get live gateway: %v", err)
	}
	liveListeners, err := harness.GatewayListeners(live.Object)
	if err != nil {
		t.Fatalf("parse live gateway: %v", err)
	}
	if !reflect.DeepEqual(rendered, liveListeners) {
		t.Fatalf("stream /yamls listeners %v != live Gateway listeners %v", rendered, liveListeners)
	}
	if len(yamls.Routes) != 1 || yamls.Routes[0].RouteID.String() != routeID || !strings.Contains(yamls.Routes[0].RouteYAML, "kind: TCPRoute") {
		t.Fatalf("stream /yamls routes = %+v, want the one TCPRoute %s", yamls.Routes, routeID)
	}

	// 2. The entry point lists the route's port, owned by the route.
	ep, err := env.Editor.GetStreamEntrypoint(ctx, projectID, streamID)
	if err != nil {
		t.Fatalf("get entrypoint: %v", err)
	}
	owned := false
	for _, p := range ep.Ports {
		if p.Transport == "TCP" && p.Port == 15400 && p.Owner.RouteID != nil && p.Owner.RouteID.String() == routeID {
			owned = true
		}
	}
	if !owned {
		t.Fatalf("entrypoint ports %+v do not list TCP/15400 owned by route %s", ep.Ports, routeID)
	}

	// 3. The deploy recorded a version, reachable under the stream.
	_, total, err := env.Editor.ListStreamRouteVersions(ctx, projectID, streamID, routeID)
	if err != nil {
		t.Fatalf("list stream route versions: %v", err)
	}
	if total < 1 {
		t.Fatalf("stream route versions total = %d, want >= 1", total)
	}

	// 4. The approval diff of an update is a 200 with TCPRoute YAML. The
	// fixture's cleanup rejects this pending approval before deleting.
	updated := cfg.Config
	updated.ListenerPort = 15401
	if _, err := env.Editor.UpdateStreamRoute(ctx, projectID, streamID, routeID, services.UpdateRouteInput{Config: updated}); err != nil {
		t.Fatalf("update stream route: %v", err)
	}
	approvalID, err := env.Admin.PendingRouteApprovalID(ctx, projectID, routeID)
	if err != nil {
		t.Fatalf("find update approval: %v", err)
	}
	diff, err := env.Admin.GetApprovalDiff(ctx, projectID, approvalID)
	if err != nil {
		t.Fatalf("approval diff for an L4 update: want 200, got %v", err)
	}
	if diff.RouteKind != "TCPRoute" ||
		!strings.Contains(diff.CurrentYAML, "sectionName: l4-tcp-15400") ||
		!strings.Contains(diff.ProposedYAML, "sectionName: l4-tcp-15401") {
		t.Fatalf("approval diff = routeKind %q current %q proposed %q, want TCPRoute 15400 -> 15401", diff.RouteKind, diff.CurrentYAML, diff.ProposedYAML)
	}
}
```

- [ ] **Step 2: Compile the e2e tree**

Run: `go build -tags e2e ./... && go vet -tags e2e ./e2e/harness/ ./e2e/suites/stream/`
Expected: no output (success).

- [ ] **Step 3: Run the e2e test against a live environment** (when one is available; the suite exits 0 on Envoy Gateway < 1.8)

Run: `go test -tags e2e ./e2e/suites/stream/ -run TestStreamLegibility -count=1 -v`
Expected: `--- PASS: TestStreamLegibility_ManifestsEntrypointVersionsAndDiff`. If no cluster is available, record that it was not run. Do not report it as passing.

- [ ] **Step 4: Full verification gate** (`fastgateway-backend-verify`)

```bash
go build ./...
go test ./... -count=1            # add -p 1 if linking hits "no space left on device"
go build -tags e2e ./...
make mocks && make mocks-check
make openapi && make openapi-check
gofmt -l internal/ cmd/ e2e/
grep -rn ErrDiffNotAvailableForL4 --include='*.go' .
```

Expected: every package `ok`; `mocks-check` and `openapi-check` clean; `gofmt -l` and the grep print nothing. With `INTEGRATION_DB_URL` set, also run the new DB tests by name:
`go test ./internal/repository/ -run 'TestStreamRepository|TestDomainRepository_PortOwnersByTemplate' -count=1 -v` → PASS. The baseline DB failures listed in Global Constraints are not caused by this plan. Check that by confirming they also fail on `main`.

- [ ] **Step 5: Commit**

```bash
git add e2e/harness/stream_legibility.go e2e/suites/stream/legibility_test.go
git commit -m "test(e2e): stream manifests, entry point, versions and L4 approval diff

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
docker rm -f fgw-l4-pg
```

---

## Spec coverage

| Spec item | Task |
|---|---|
| B1 entry point endpoints, rules, failure, owner-aware queries, agreement invariant | 1, 2, 3, 4 |
| B2 stream list enrichment, no N+1 | 1, 5 |
| B3 stream manifests (same route set + object builder; placeholder) | 7 |
| B4 L4 BTP helper, `GenerateYAMLs` L4 branch, `GET …/routes/:routeId/yamls` | 6 |
| B5 previews (create with create's validation, update with BTP + port re-check, delete) | 8 |
| B6 versions under the stream, 404 for another stream's route | 9 |
| B7 L4 diff (create/update/delete), `routeKind`, `streamId`/`streamName`, two required deps, `ErrDiffNotAvailableForL4` removed | 10 |
| B8 OpenAPI, mocks, no nil guards | every task; full gate in 11 |
| Testing 1 (builder table) | 3 |
| Testing 2 (agreement) | 3 |
| Testing 3 (owner queries: rejected excluded, owners, parity) | 1 |
| Testing 4 (stream YAMLs equality, placeholder, BTP only with a policy) | 7 |
| Testing 5 (previews: same 400/409; BTP diff; port re-check) | 8 |
| Testing 6 (wrappers 404 on yamls/preview/versions/rollback) | 6, 8, 9 |
| Testing 7 (approval diff create/update/delete, `routeKind`, no 500, `streamName`) | 10 |
| Testing 8 (`go test ./...`, `mocks-check`, `openapi-check`) | 11 |
| Backend e2e (listeners match live Gateway, versions ≥ 1, diff 200 with TCPRoute) | 11 |
