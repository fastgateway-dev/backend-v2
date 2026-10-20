# Stream Port Claims (Backend) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A stream claims a set of `(transport, port)`; its Gateway is built from the claims and written only on stream create/edit/delete (and a startup reconcile); L4 routes must use a free claimed port, and route deploys never touch the Gateway.

**Architecture:** Claims live in a new `streams.ports` JSONB column (`models.StreamPorts`). Claim validation (`validateStreamPorts`) takes over today's merge-aware `CheckPortCollision` scope; routes get a narrow `CheckRoutePort` (claimed + unused). "Used" counts route rows *and* pending route approvals. `StreamService.ApplyGateway` is the single writer of the stream Gateway. A shared helper removes `status` from rendered typed route YAML.

**Tech Stack:** Go 1.26, gin, gorm (Postgres), golang-migrate SQL migrations, `k8s.io/client-go` dynamic client (+ `dynamic/fake`), testify, mockery v3 (`make mocks`), Redocly (`make openapi`), e2e on kind + Envoy Gateway 1.9.1.

**Spec:** `docs/superpowers/specs/2026-10-19-stream-port-claims-design.md` (Backend B1–B8, "Used means…", Testing, Rollout). The frontend plan is `2026-10-20-stream-port-claims-frontend.md`; it is written against the **Contract** below.

## Global Constraints

From the spec, verbatim:

- A claim is a **port plus a transport**: `5432/TCP`, `53/UDP`. `53/TCP` and `53/UDP` are two claims.
- A stream must claim **at least one port at create**.
- Claims can be **added at any time**. Removing a claim a route still uses is **409**.
- **No approval.** Claims apply immediately on stream create/update.
- Listener name `l4-<tcp|udp>-<port>` (unchanged naming, so migrated routes' `sectionName` stays valid).
- A claim is **used** when a route row of the stream (status ≠ rejected) has that `(transport, listenerPort)`, or a pending approval for a route of the stream proposes it.
- Reserved: Envoy internal 19000/19001. 60000 becomes a normal port.
- Permissions are unchanged: stream create/update/delete need `CanManageDomains`; route endpoints need `CanCreateRoutes`.

Repository rules (same as the last plan):

- **Before starting:** `git checkout main && git pull --ff-only origin main && git checkout -b feat/stream-port-claims`. Run every command from the backend-v2 repo root.
- **Mocks are generated:** `make mocks` after any interface change in `internal/repository`, `internal/services`, `internal/handlers` or `internal/mocks/roles/roles.go`; commit `internal/mocks/*`; `make mocks-check` after the commit.
- **OpenAPI per task:** `TestRouteSpecParity` fails on any request/response shape drift that the spec covers; update `docs/openapi/` and run `make openapi` in the task that changes the shape; `make openapi-check` passes.
- **No silent nil-skips:** no `if s.<dep> == nil` on a wired dependency (`TestNoWiringNilGuardsRemain*`; its regex also flags locals ending in `s`).
- **Postgres-gated tests skip without `INTEGRATION_DB_URL`. A skip is not a pass.**
  ```bash
  docker run -d --name fgw-claims-pg -e POSTGRES_USER=fastgateway -e POSTGRES_PASSWORD=fastgateway -e POSTGRES_DB=fastgateway -p 55432:5432 postgres:16-alpine
  sleep 5 && DATABASE_PORT=55432 go run cmd/migrate/main.go up
  export INTEGRATION_DB_URL="postgres://fastgateway:fastgateway@localhost:55432/fastgateway?sslmode=disable"
  ```
  Ten DB-gated tests already fail on `main` (`TestCheckPortCollision_*`, `TestDomainDNSRecordRepository_CountByZone`, `TestDomainRepository_ListByManagedCertificateID(s)`). Task 2 deletes the `TestCheckPortCollision_*` ones with the function. Run new DB tests **by name**. `docker rm -f fgw-claims-pg` at the end.
- **Low disk:** `go test -p 1 ./...` if linking fails with `no space left on device`.
- Commit messages are conventional commits ending with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Contract (what the frontend plan uses)

```jsonc
// POST /projects/:p/streams
{ "name": "db", "namespace": "default", "gatewayTemplateId": "…",
  "ports": [{ "transport": "TCP", "port": 5432 }] }            // required, ≥1
// PATCH /projects/:p/streams/:id   (both optional; ports = full replacement set)
{ "name": "db2", "ports": [{ "transport": "TCP", "port": 5432 }, { "transport": "UDP", "port": 53 }] }
// models.Stream JSON gains:
"ports": [{ "transport": "TCP", "port": 5432 }]                // sorted TCP before UDP, then by port
// Entry point ports[].owner (stream and template variants):
{ "kind": "stream", "streamId": "…", "streamName": "db",
  "routeId": "…", "routeName": "pg", "status": "active", "pending": false }  // route fields only for the viewed stream's own claims
{ "kind": "domain", "domainId": "…", "domainName": "…" }     // unchanged
// reserved[] reasons: "envoy_internal", "template_listener" (no "placeholder")
```

Errors (`{"error": "<message>"}`): stream create/update — 400 for empty set, bad transport, duplicate, out of 1–65535, reserved, out of template range; 409 for collision with another stream/domain on a shared template, and for dropping a used claim. Route create/update/preview/rollback — 400 `port … is not claimed by stream …`; 409 `… is used by another route on stream …`. Route deploy — 409 when the port is no longer claimed.

## Review Focus

- **A route update that keeps its own port** must not 409 against itself, including the pending-approval branch. Test: Task 4 `TestCheckRoutePort_UpdateKeepingOwnPortIsFree`.
- **The same claim set re-sent in another order or lower case** (`{"transport":"tcp"}`) must be accepted, normalized (upper case, sorted) and not reported as dropping a used claim. Test: Task 2 `TestValidateStreamPorts_NormalizesTransportAndOrder`, Task 3 `TestStreamUpdate_ReorderedClaimsKeepUsedPorts`.
- **A legacy stream with no claims** must still accept a name-only PATCH (the empty-set rule applies only when `ports` is sent). Test: Task 3 `TestStreamUpdate_NameOnlyOnLegacyEmptyStream`.
- **A rejected or cancelled approval frees its proposed port.** Test: Task 1 `TestStreamRepository_UsedPortsByStream_IgnoresRejectedAndCancelledApprovals`.
- **Applying a Gateway for an empty claim set when no Gateway exists** (legacy stream, startup reconcile run twice) is a success, not an error. Test: Task 3 `TestApplyGateway_EmptyClaimsAndGatewayAlreadyGone`.

## Plan rulings

- **R1 — Other streams' claims carry no route detail** in the entry point. The UI no longer lists neighbours; only the viewed stream's own claims get `routeId/routeName/status/pending`. Saves a per-template owner query.
- **R2 — Deploy re-check is "still claimed" only.** Once an approval is applied, the route row holds the port and the unique index `idx_route_stream_proto_port` already rejects a second row on it, so a row-collision re-check at deploy cannot fire.
- **R3 — Deploy on an unclaimed port is 409** (the request is fine; the stream changed under it).
- **R4 — Apply failures are recorded, not returned**, on create *and* update: the stream is returned with `status: "error"` and the message, as create does today.
- **R5 — Empty claims:** `ApplyGateway` deletes the Gateway (NotFound counts as success), sets `status: "active"`, `statusMessage: "No ports claimed; no Gateway deployed"`.

---

### Task 1: Claims column, migration and repository queries

**Files:**
- Create: `internal/models/stream_port.go`, `internal/models/stream_port_test.go`
- Create: `migrations/000052_stream_ports.up.sql`, `migrations/000052_stream_ports.down.sql`
- Modify: `internal/models/stream.go` (field), `internal/repository/stream_repository.go`
- Test: `internal/repository/stream_repository_test.go`

**Interfaces:**
- Produces:
  - `type StreamPort struct { Transport string \`json:"transport"\`; Port int \`json:"port"\` }`
  - `type StreamPorts []StreamPort` with `Value() (driver.Value, error)` (nil → `"[]"`), `Scan(any) error`, `Has(transport string, port int) bool`, `Normalize() StreamPorts` (upper-case transport, sorted TCP<UDP then port; does not dedupe)
  - `models.Stream.Ports StreamPorts \`gorm:"column:ports;type:jsonb;not null;default:'[]'" json:"ports"\``
  - `(*StreamRepository) UsedPortsByStream(streamID uuid.UUID, excludeRouteID *uuid.UUID) ([]PortUse, error)` — now rows **plus** pending approvals
  - `(*StreamRepository) ClaimedPortsByTemplate(templateID uuid.UUID, excludeStreamID *uuid.UUID) ([]PortUse, error)`
  - `(*StreamRepository) PendingL4PortOwnersByStream(streamID uuid.UUID) ([]L4PortOwner, error)`; `L4PortOwner` gains `Pending bool`
  - `(*StreamRepository) ListAll() ([]models.Stream, error)`

- [ ] **Step 1: Write failing model tests** in `stream_port_test.go`: `TestStreamPorts_ValueNilIsEmptyArray` (`Value()` of nil is `[]byte("[]")`), `TestStreamPorts_ScanRoundTrip`, `TestStreamPorts_Has` (`{TCP 53}` has `("TCP",53)`, not `("UDP",53)`), `TestStreamPorts_Normalize` (`[{udp 53} {tcp 9000} {TCP 80}]` → `[{TCP 80} {TCP 9000} {UDP 53}]`).
- [ ] **Step 2: Run** `go test ./internal/models -run StreamPorts -v` → FAIL (undefined).
- [ ] **Step 3: Implement** `stream_port.go` (pattern: `StringList` in `internal/models/client.go`) and add the `Ports` field to `models.Stream`.
- [ ] **Step 4: Write the migration.** Up: `ALTER TABLE streams ADD COLUMN ports JSONB NOT NULL DEFAULT '[]'::jsonb;` then the backfill (an algorithm the spec fixes, so here in full):
  ```sql
  UPDATE streams s SET ports = COALESCE((
    SELECT jsonb_agg(jsonb_build_object('transport', t, 'port', p) ORDER BY t, p)
    FROM (
      SELECT upper(r.protocol) AS t, (r.config->>'listenerPort')::int AS p
        FROM routes r
       WHERE r.stream_id = s.id AND r.protocol IN ('tcp','udp') AND r.status <> 'rejected'
         AND r.config->>'listenerPort' IS NOT NULL
      UNION
      SELECT upper(r.protocol), (a.config_snapshot->'routeConfig'->>'listenerPort')::int
        FROM approvals a JOIN routes r ON r.id = a.entity_id
       WHERE r.stream_id = s.id AND r.protocol IN ('tcp','udp')
         AND a.entity_type = 'route' AND a.status = 'pending' AND a.action IN ('create','update')
         AND a.config_snapshot->'routeConfig'->>'listenerPort' IS NOT NULL
    ) used), '[]'::jsonb);
  ```
  Down: `ALTER TABLE streams DROP COLUMN ports;`. Header comment says why pending approvals are included (spec "Used means…").
- [ ] **Step 5: Write failing repository tests** (Postgres-gated, `requirePostgres`):
  - `TestStreamRepository_UsedPortsByStream_IncludesPendingApprovals`: route A row on TCP/5432 with a pending update approval whose snapshot `routeConfig.listenerPort` is 5433 → result contains TCP/5432 and TCP/5433; with `excludeRouteID = &A` → empty.
  - `TestStreamRepository_UsedPortsByStream_IgnoresRejectedAndCancelledApprovals`: same, approval status `rejected` then `cancelled` → only TCP/5432.
  - `TestStreamRepository_ClaimedPortsByTemplate`: streams S1 `[TCP 80, UDP 53]`, S2 `[TCP 81]` on template T, S3 `[TCP 82]` on another template → T gives the three of S1+S2; `excludeStreamID=&S1` gives `[TCP 81]`.
  - `TestStreamRepository_PendingL4PortOwnersByStream`: returns the 5433 owner with `Pending: true`, route ID/name set.
  - `TestMigration000052_Backfill`: run the up SQL against seeded rows (one routed stream, one with a pending-update approval, one empty) → `[{TCP 5432}]`, `[{TCP 5432},{TCP 5433}]`, `[]`. (Follow how the 000051 backfill is tested; if it is only mirrored in Go, test the SQL directly by executing the file's contents.)
- [ ] **Step 6: Run** `go test ./internal/repository -run 'TestStreamRepository_(UsedPortsByStream|ClaimedPortsByTemplate|PendingL4PortOwnersByStream)|TestMigration000052' -v` → FAIL.
- [ ] **Step 7: Implement** the four repository methods. `UsedPortsByStream`: today's row query `UNION` the pending-approval query from Step 4 (excludeRouteID applied to both). `ClaimedPortsByTemplate`: `jsonb_array_elements(streams.ports)` joined on `gateway_template_id`.
- [ ] **Step 8: Run** the Step 6 command → PASS; `go build ./... && go test ./internal/models ./internal/repository` → PASS (DB tests by name as above).
- [ ] **Step 9: Commit** — `feat(streams): ports claims column, backfill and claim/used-port queries`.

---

### Task 2: Claim validation; domains and templates collide with claims

**Files:**
- Modify: `internal/services/stream_port_collision.go`, `internal/services/stream_service.go` (errors), `internal/services/domain_service.go`, `internal/services/domain_template_service.go`, `internal/services/stream_view.go` (`reservedPorts` only)
- Test: `internal/services/stream_port_collision_test.go`, `internal/services/stream_view_test.go`, domain/template service tests that stub `UsedL4Ports`

**Interfaces:**
- Consumes: Task 1 `StreamPorts`, `UsedPortsByStream`, `ClaimedPortsByTemplate`.
- Produces:
  - `func (s *StreamService) validateStreamPorts(tmpl *models.DomainTemplate, selfID *uuid.UUID, ports models.StreamPorts) (models.StreamPorts, error)` — returns the normalized set
  - `func (s *StreamService) checkUsedClaimsKept(stream *models.Stream, next models.StreamPorts) error`
  - Errors: `ErrNoStreamPorts`, `ErrInvalidStreamPort` (bad transport or duplicate), `ErrStreamPortInUse`; existing `ErrInvalidListenerPort`, `ErrReservedPort`, `ErrPortOutOfRange`, `ErrPortCollision` reused
  - `StreamPortStore` = `{ UsedPortsByStream(...); ClaimedPortsByTemplate(...) }`; `L4PortReader` = `{ ClaimedPortsByTemplate(templateID uuid.UUID, excludeStreamID *uuid.UUID) ([]PortUse, error) }`
  - `ReservedPorts` loses `Placeholder`; `DefaultReservedPorts = {EnvoyInternal: 19000, 19001}`; `ReservedReasonPlaceholder` deleted

- [ ] **Step 1: Write failing tests** (mocks, no DB) in `stream_port_collision_test.go`:
  - `TestValidateStreamPorts_Rules` table: empty → `ErrNoStreamPorts`; `{"SCTP",80}` → `ErrInvalidStreamPort`; `[{TCP 80},{tcp 80}]` → `ErrInvalidStreamPort`; `{TCP 0}` → `ErrInvalidListenerPort`; `{TCP 19000}` → `ErrReservedPort`; `{UDP 60000}` with range 1–65535 → OK; port outside range → `ErrPortOutOfRange`.
  - `TestValidateStreamPorts_SharedCollisions`: merged template; another stream claims TCP/5432 → `ErrPortCollision` (409); a domain on TCP/8443 → `ErrPortCollision`; template hostname-routed TCP/443 → `ErrReservedPort`; UDP/443 → OK; `selfID` excludes the stream's own claims.
  - `TestValidateStreamPorts_DedicatedIgnoresOthers`: unmerged template, another stream claims TCP/5432 → OK.
  - `TestValidateStreamPorts_NormalizesTransportAndOrder`: `[{udp 53},{tcp 9000}]` → returns `[{TCP 9000},{UDP 53}]`.
  - `TestCheckUsedClaimsKept`: used `[TCP 5432]` (row) and `[TCP 5433]` (pending); next set without 5433 → `ErrStreamPortInUse`, message contains `TCP/5433` and the route name and `pending`; next set with both → nil.
  - `TestDomainCreate_CollidesWithUnroutedStreamClaim` (domain service test): merged template, stream claims TCP/8443 with no route → domain bound to 8443 is `ErrPortCollision`.
  - `TestStreamEntrypoint_ReservedHasNoPlaceholder` (stream_view_test): `reserved` has 19000/19001 per transport, no 60000.
- [ ] **Step 2: Run** `go test ./internal/services -run 'TestValidateStreamPorts|TestCheckUsedClaimsKept|TestDomainCreate_CollidesWithUnroutedStreamClaim|TestStreamEntrypoint_ReservedHasNoPlaceholder' -v` → FAIL.
- [ ] **Step 3: Implement.** `validateStreamPorts` reuses `ValidateListenerPort`, `checkPortInRange`, `templateDomainDefaults` and the shared branch of today's `CheckPortCollision`, reading `ClaimedPortsByTemplate(tmpl.ID, selfID)` instead of route ports. `checkUsedClaimsKept` reads `UsedPortsByStream(stream.ID, nil)` and `PendingL4PortOwnersByStream` for the message. Switch `checkDomainPortsAgainstStreams` and `checkMergedTemplateSet` to `ClaimedPortsByTemplate(tmpl.ID, nil)`. Remove the placeholder from `ReservedPorts`, `ValidateListenerPort` and `reservedPorts()`. Leave `CheckPortCollision` in place until Task 4.
- [ ] **Step 4: Fix the tests the interface change breaks** (stubs of `UsedL4Ports`, placeholder assertions in `stream_port_collision_test.go`, `stream_view_test.go`, `route_write_validation_internal_test.go`); `make mocks`.
- [ ] **Step 5: Run** `go build ./... && go vet ./... && go test ./internal/services ./internal/handlers` → PASS.
- [ ] **Step 6: Commit** — `feat(streams): validate port claims; domains and templates collide with claims`.

---

### Task 3: Stream owns its Gateway (builder, create/update/delete, API)

**Files:**
- Modify: `internal/streamplan/gateway.go`, `internal/services/stream_service.go`, `internal/services/stream_yaml.go`, `internal/handlers/stream_handler.go`, `docs/openapi/paths/streams.yaml`, `docs/openapi/schemas/stream.yaml`, `cmd/server/main.go` (only if a constructor changes)
- Test: `internal/streamplan/gateway_test.go`, `internal/services/stream_service_test.go`, `internal/services/stream_yaml_test.go`, `internal/handlers/stream_handler_test.go`

**Interfaces:**
- Consumes: Task 2 `validateStreamPorts`, `checkUsedClaimsKept`.
- Produces:
  - `func BuildStreamGatewayConfig(stream models.Stream) kubernetes.GatewayConfig` — one listener per claim, `Name: ListenerName(t, p)`; no route argument. `PlaceholderPort` and `placeholderListenerName` deleted.
  - `func (s *StreamService) ApplyGateway(ctx context.Context, stream *models.Stream) error` (replaces `Deploy`): non-empty claims → `UpdateGateway` (create-if-missing); empty → `DeleteGateway`, NotFound is success (R5). Records status: `"active"`/`"Gateway applied successfully"` or `"error"`/`"Failed to apply Gateway: <err>"`.
  - `CreateStreamInput.Ports models.StreamPorts`; `UpdateStreamInput.Ports *models.StreamPorts`
  - `CreateStreamRequest.Ports []models.StreamPort \`json:"ports" binding:"required,min=1"\``; `UpdateStreamRequest.Ports *[]models.StreamPort \`json:"ports"\``

- [ ] **Step 1: Write failing tests:**
  - `TestBuildStreamGatewayConfig_ListenersFromClaims`: `[{TCP 5432},{UDP 53}]` → listeners `l4-tcp-5432`/TCP/5432 and `l4-udp-53`/UDP/53, in that order; empty claims → no listeners.
  - `TestStreamCreate_AppliesGatewayFromClaims`: fake applier records `UpdateGateway` with exactly the claim listeners; stream persisted with normalized `Ports`; status active.
  - `TestStreamCreate_RejectsInvalidClaims`: empty ports → `ErrNoStreamPorts`, nothing persisted, no K8s call.
  - `TestStreamUpdate_PortsReapplyGateway`: add UDP/53 → `UpdateGateway` with both listeners.
  - `TestStreamUpdate_DropUsedClaimIs409`: → `ErrStreamPortInUse`, nothing persisted, no K8s call.
  - `TestStreamUpdate_ReorderedClaimsKeepUsedPorts`: used TCP/5432; PATCH `[{UDP 53},{tcp 5432}]` → OK.
  - `TestStreamUpdate_NameOnlyOnLegacyEmptyStream`: stream with `Ports` empty, `UpdateStreamInput{Name: &n}` → OK, no K8s call.
  - `TestStreamUpdate_ApplyFailureRecordedOnStatus` (R4): `UpdateGateway` errors → returned stream has `status: "error"`, message prefix `Failed to apply Gateway:`, no error returned.
  - `TestApplyGateway_EmptyClaimsAndGatewayAlreadyGone`: `DeleteGateway` returns a NotFound → nil error, status active, message `No ports claimed; no Gateway deployed`.
  - `TestStreamDelete_DeletesGateway`: no routes → `DeleteGateway(ns, "str-<name>")` called, then row deleted; a `DeleteGateway` error is logged and the row is still deleted.
  - `TestGenerateStreamYAMLs_GatewayFromClaims` (replaces the placeholder test in `stream_yaml_test.go`).
  - Handler: `TestCreateStream_PortsRequired` (missing `ports` → 400), `TestUpdateStream_ErrorMapping` (`ErrStreamPortInUse` → 409, `ErrNoStreamPorts`/`ErrInvalidStreamPort` → 400, `ErrPortCollision` → 409).
- [ ] **Step 2: Run** `go test ./internal/streamplan ./internal/services ./internal/handlers -run 'StreamGatewayConfig|TestStream(Create|Update|Delete)_|TestApplyGateway_|TestGenerateStreamYAMLs_|TestCreateStream_|TestUpdateStream_' -v` → FAIL.
- [ ] **Step 3: Implement.** Create: validate claims after the template/namespace/name checks and before persisting. Update: when `Ports != nil`, `validateStreamPorts(tmpl, &stream.ID, *in.Ports)` then `checkUsedClaimsKept`, persist, `ApplyGateway`. Delete: `DeleteGateway` (log failure) before `streamRepo.Delete`. `GenerateStreamYAMLs` drops `ListActiveByStreamID` and renders from claims (empty claims → empty Gateway YAML string). Add the new errors to `streamError`. Update the OpenAPI request/response schemas; `make openapi`.
- [ ] **Step 4: Remove every remaining placeholder assertion** (`stream_service_test.go`, `stream_yaml_test.go`, `streamplan/gateway_test.go`); `grep -rn -E 'placeholder|60000' internal/ | grep -v -i 'reason'` is empty apart from unrelated hits. `make mocks`.
- [ ] **Step 5: Run** `go build ./... && go test ./internal/... && make openapi-check` → PASS (DB tests skipped unless by name).
- [ ] **Step 6: Commit** — `feat(streams): stream builds and owns its Gateway from port claims`.

---

### Task 4: Routes use claims; deploy stops writing the Gateway

**Files:**
- Modify: `internal/services/stream_port_collision.go`, `internal/services/route_write_validation.go`, `internal/services/route_deploy_service.go`, `internal/services/route_write_service.go` (wiring name only), `internal/handlers/route_handler.go` (error mapping), `cmd/server/main.go` (`SetL4PortChecker`)
- Test: `internal/services/stream_port_collision_test.go`, `internal/services/route_write_l4_test.go`, `internal/services/route_deploy_l4_test.go`, `internal/services/route_preview_l4_test.go`, `internal/handlers/route_handler_test.go`

**Interfaces:**
- Consumes: Task 1 `UsedPortsByStream`; Task 3 claims on `models.Stream`.
- Produces:
  - `var ErrPortNotClaimed = errors.New("listener port is not claimed by the stream")`
  - `func (s *StreamService) CheckRoutePort(streamID uuid.UUID, transport string, port int, excludeRouteID *uuid.UUID) error` — `ErrPortNotClaimed` (message lists the stream's claims for that transport) or `ErrPortCollision`; `L4PortChecker` interface method becomes `CheckRoutePort`. `CheckPortCollision` and its DB tests are deleted.

- [ ] **Step 1: Write failing tests:**
  - `TestCheckRoutePort`: claims `[TCP 5432, TCP 5433]`; TCP/5434 → `ErrPortNotClaimed` with `TCP/5432, TCP/5433` in the message; UDP/5432 → `ErrPortNotClaimed`; TCP/5432 used by route B → `ErrPortCollision`; TCP/5433 held only by B's pending approval → `ErrPortCollision`.
  - `TestCheckRoutePort_UpdateKeepingOwnPortIsFree`: route A on 5432 with `excludeRouteID=&A` → nil.
  - `TestDeployL4_NoGatewayWrites` (replaces the listener-recompute tests in `route_deploy_l4_test.go`): create, update (port move 5432→5433) and delete deploys make zero `UpdateGateway`/`CreateGateway` calls; the TCPRoute's `sectionName` follows the port.
  - `TestDeployL4_PortNoLongerClaimedFailsBeforeK8s`: stream claims no longer include the route's port → error wraps `ErrPortNotClaimed`, no K8s calls, route status unchanged.
  - `TestRouteHandler_PortNotClaimedMapping`: create/update/preview → 400; deploy → 409 (R3).
  - Preview: `TestPreviewCreateForStream_UnclaimedPortIs400` in `route_preview_l4_test.go`.
  - Rollback: `TestRollbackL4_ToUnclaimedPortIs400` — a version on TCP/5434 after 5434 was unclaimed fails with `ErrPortNotClaimed` and changes nothing.
- [ ] **Step 2: Run** `go test ./internal/services ./internal/handlers -run 'TestCheckRoutePort|TestDeployL4_|PortNotClaimed|UnclaimedPort' -v` → FAIL.
- [ ] **Step 3: Implement.** `validateL4Listener` keeps the static `ValidateListenerPort` check and calls `CheckRoutePort`. `deployL4`: remove `ListActiveByStreamID`, `withL4Route`/`withoutL4Route`, `BuildStreamGatewayConfig`, `UpdateGateway` and the race comment; for create/update first check `stream.Ports.Has(route.Transport(), route.Config.ListenerPort)` (R2). Delete helpers and repo methods that are now unused (`withL4Route`, `withoutL4Route`, `UsedL4Ports`, and `ListActiveByStreamID` if nothing else calls it). Map `ErrPortNotClaimed` (400 on write/preview, 409 on deploy). `make mocks`.
- [ ] **Step 4: Run** `go build ./... && go vet ./... && go test ./internal/... && make mocks-check && make openapi-check` → PASS.
- [ ] **Step 5: Commit** — `feat(l4): routes use stream port claims; route deploy no longer writes the Gateway`.

---

### Task 5: Entry point and stream list from claims

**Files:**
- Modify: `internal/services/stream_view.go`, `docs/openapi/schemas/stream.yaml` (entry point owner)
- Test: `internal/services/stream_view_test.go`, `internal/handlers/stream_handler_test.go`

**Interfaces:**
- Consumes: `models.Stream.Ports`, `L4PortOwnersByStream`, `PendingL4PortOwnersByStream`, `ListByGatewayTemplateID`.
- Produces: `services.StreamPort` (today a separate struct for the list) becomes `type StreamPort = models.StreamPort`; `PortOwnerStream = "stream"` replaces `stream_route`; `EntrypointPortOwner` gains `Pending bool \`json:"pending,omitempty"\``; `StreamListItem.Ports` = claims.

- [ ] **Step 1: Write failing tests:**
  - `TestStreamEntrypoint_Dedicated_ListsClaimsWithRoutes`: claims `[TCP 5432, TCP 5433, UDP 53]`, route `pg` on 5432 (active), route `cache` pending to 5433 → three ports, owner kind `stream`; 5432 has `routeName: "pg"`; 5433 has `routeName: "cache", pending: true`; UDP/53 has no route fields.
  - `TestStreamEntrypoint_Shared_ListsOtherStreamsClaimsAndDomains` (R1): other stream's claim appears with `streamName`, no route fields; domain owners unchanged.
  - `TestTemplateEntrypoint_Shared_ListsAllClaims`: `self = nil` → every stream's claims + domain ports.
  - `TestListWithSummary_PortsAreClaims`: a stream with claims and no routes lists its claims; `routeCount` unchanged.
- [ ] **Step 2: Run** `go test ./internal/services -run 'TestStreamEntrypoint_|TestTemplateEntrypoint_|TestListWithSummary_' -v` → FAIL.
- [ ] **Step 3: Implement** `buildEntrypoint` from claims (`self.Ports`, other members' `Ports`), joining the viewed stream's route owners and pending owners by `(transport, port)`. Replace `appendStreamRoutePorts`. Update OpenAPI owner enum; `make openapi`, `make mocks` if `StreamViewStore` changed. Delete repo methods left unused (`L4PortOwnersByTemplate`, `L4PortOwnersByStreamIDs` if `ListWithSummary` no longer needs them).
- [ ] **Step 4: Run** `go build ./... && go test ./internal/... && make openapi-check && make mocks-check` → PASS.
- [ ] **Step 5: Commit** — `feat(streams): entry point and stream list show port claims`.

---

### Task 6: Startup reconcile of stream Gateways

**Files:**
- Modify: `internal/services/stream_service.go`, `cmd/server/main.go`
- Test: `internal/services/stream_service_test.go`

**Interfaces:**
- Consumes: Task 1 `ListAll`, Task 3 `ApplyGateway`.
- Produces: `func (s *StreamService) ReconcileGateways(ctx context.Context) error` — applies every stream's Gateway; returns an error only when listing fails; per-stream failures are logged and recorded on status.

- [ ] **Step 1: Write failing tests:** `TestReconcileGateways_AppliesEveryStream` (claims → `UpdateGateway`; empty → `DeleteGateway`); `TestReconcileGateways_ContinuesAfterFailure` (first stream's apply fails, second still applied, nil returned); `TestReconcileGateways_Idempotent` (two runs, same calls, no error with Gateway already deleted).
- [ ] **Step 2: Run** `go test ./internal/services -run TestReconcileGateways -v` → FAIL.
- [ ] **Step 3: Implement**, and call it from `main.go` once after the services are wired and before `router.Run`, in a goroutine using the shutdown `ctx`, logging the result. Add `ListAll` to `StreamStore`; `make mocks`.
- [ ] **Step 4: Run** `go build ./... && go test ./internal/services` → PASS.
- [ ] **Step 5: Commit** — `feat(streams): reconcile stream Gateways from claims at startup`.

---

### Task 7: No `status` in rendered route YAML

**Files:**
- Create: `internal/kubernetes/yaml.go`, `internal/kubernetes/yaml_test.go`
- Modify: `internal/routeplan/httproute.go` (2 sites), `internal/services/route_yaml.go` (3 sites), goldens under `internal/services/testdata/golden/`

**Interfaces:**
- Produces: `func MarshalRouteYAML(obj any) ([]byte, error)` — converts with `runtime.DefaultUnstructuredConverter.ToUnstructured`, removes `status` and a null `metadata.creationTimestamp`, then `sigs.k8s.io/yaml` marshals.

- [ ] **Step 1: Write failing tests:** `TestMarshalRouteYAML_DropsStatusAndNullTimestamp` (a `gatewayv1alpha2.TCPRoute` → output contains `kind: TCPRoute` and `sectionName`, not `status:` nor `creationTimestamp`); same for `HTTPRoute`.
- [ ] **Step 2: Run** `go test ./internal/kubernetes -run TestMarshalRouteYAML -v` → FAIL.
- [ ] **Step 3: Implement** and replace the five `yaml.Marshal(<typed route>)` sites. `grep -rn 'yaml.Marshal(' internal/routeplan internal/services | grep -v -E '\.Object\)|_test'` lists no typed route afterwards.
- [ ] **Step 4: Regenerate goldens** with the golden helper's update mode (find it in the golden test helper under `internal/services`); `grep -rl 'parents: null' internal/services/testdata/golden` is empty; review the diff is only `status`/`creationTimestamp` removals.
- [ ] **Step 5: Run** `go test ./internal/...` → PASS.
- [ ] **Step 6: Commit** — `fix(yaml): drop empty status from rendered route manifests`.

---

### Task 8: e2e and full gate

**Files:**
- Modify: `e2e/harness/stream.go`, `e2e/suites/stream/main_test.go`, every `e2e/suites/stream/*_test.go` that creates a route, `e2e/suites/stream/legibility_test.go`
- Create: `e2e/suites/stream/claims_test.go`

**Interfaces:**
- Produces: `(*API) CreateStream(ctx, projectID, name, namespace string, templateID uuid.UUID, ports []models.StreamPort) (models.Stream, error)`; `(*API) UpdateStreamPorts(ctx, projectID, streamID string, ports []models.StreamPort) (models.Stream, error)`; `newStream(t *testing.T, ports ...models.StreamPort) models.Stream`.

- [ ] **Step 1: Update existing tests** so each stream claims the ports its routes use (ports from the table in the spec's e2e list: 15100 TCP, 15101 UDP, 15200, 15300 TCP+UDP, 15400+15401, 15500, 15600, 15700+15701, 15800). `validation_port_collision_test.go` becomes: second route on claimed TCP/15300 → 409; route on unclaimed TCP/15399 → 400; UDP/15300 claimed → OK.
- [ ] **Step 2: Write `claims_test.go`:**
  - `TestStreamClaims_UnroutedListenersAccepted`: claim TCP/15900, UDP/15901 (no routes) and TCP/15902 with a route to `l4-echo-a`; wait for the Gateway's `Accepted=True` and `Programmed=True`; the route on 15902 carries traffic (reuse the TCP traffic helper from `traffic_tcp_test.go`); the live Gateway's listeners and the `/yamls` Gateway listeners both equal the three claims.
  - `TestStreamClaims_UnclaimedPortIs400`.
  - `TestStreamClaims_RemovingUsedClaimIs409`: PATCH dropping 15902 → 409; dropping 15900 → 200 and the live Gateway loses `l4-tcp-15900`.
  - `TestStreamClaims_DeleteRemovesGateway`: stream with claims and no routes; DELETE → the K8s Gateway is gone.
- [ ] **Step 3: Build** `go build -tags e2e ./... && go vet -tags e2e ./e2e/...` → OK. (The suite itself runs in CI's `e2e` workflow on push.)
- [ ] **Step 4: Full gate:** `go build ./... && go test -p 1 ./... && make mocks-check && make openapi-check`; DB-gated tests from Tasks 1–2 run by name with `INTEGRATION_DB_URL`. Only the pre-existing failures listed in Global Constraints may fail.
- [ ] **Step 5: Commit** — `test(e2e): stream port claims`.
