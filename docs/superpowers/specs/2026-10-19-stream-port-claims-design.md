# Stream Port Claims — Design

Date: 2026-10-19. Repos: `backend-v2`, `frontend-v2`.

## Problem

An L4 stream's Gateway listeners are derived from its routes. Every L4 route
deploy recomputes the stream's full listener set and calls `UpdateGateway`
(`deployL4`, `internal/services/route_deploy_service.go`). Three things follow
from that:

1. **A placeholder listener.** A Gateway needs at least one listener, so a
   stream with no routes gets `l4-placeholder` on TCP/60000
   (`internal/streamplan/gateway.go`). On a dedicated stream the LoadBalancer
   opens 60000 publicly, connected to nothing; on a shared template every
   empty stream declares the same 60000 listener. 60000 is reserved so
   nobody can use it, and it surprises anyone reading the manifest. This
   shipped in v0.5.0.
2. **A race.** Two deploys of different routes on one stream can each read a
   stale route set; the second `UpdateGateway` wins and drops the other
   route's listener. Documented in `deployL4` as a known follow-up.
3. **Ports are decided by whoever creates a route.** Anyone with route-create
   permission opens a new port on the stream's IP. Port policy belongs with
   the stream, the way a domain owns its 80/443 listeners and routes attach
   to them.

Also raised by the owner while testing on a live cluster:

- The stream Entry point panel's "Also used by N streams and M domains"
  list and "N ports used by other streams/domains" list are noise. The
  second one is also wrong: it says "2 ports" and lists one row per domain
  (6 rows).
- Route previews end with `status: parents: null`.

## Goal

A stream **claims** a set of ports. Its Gateway is built from the claims
and is written only when the stream is created, edited or deleted. A route
picks one of its stream's claimed ports that no other route of the stream
uses. Route deploys never touch the Gateway.

## Non-goals

- Approval for port claims. Streams are not approval-gated today (only
  routes, client attachments and certificates are); claims take effect
  immediately, like domain listeners.
- Multiple routes on one claimed port. TCPRoute and UDPRoute have no
  hostname matching to tell them apart; it stays one route per port.
- Checking that a template's narrowed port range still covers existing
  claims (template update does not check it for routes today either).
  Deferred.
- Cross-stream locking for two concurrent claim edits on one shared
  template. Same app-level check-then-write domains have today. Deferred.
- Template annotations on stream Gateways (domains apply them, streams do
  not). Unchanged.

## Decisions (owner-approved)

1. A claim is a **port plus a transport**: `5432/TCP`, `53/UDP`. `53/TCP`
   and `53/UDP` are two claims.
2. A stream must claim **at least one port at create**.
3. Claims can be **added at any time**. Removing a claim a route still uses
   is **409**.
4. **No approval.** Claims apply immediately on stream create/update.
5. **Existing streams:** claims are backfilled from their routes' current
   ports. Streams with no routes lose the placeholder Gateway and show
   "no ports".
6. **A claimed port with no route** stays a real listener. e2e verifies
   Envoy Gateway accepts a Gateway whose TCP/UDP listeners have no attached
   routes, and that the other listeners keep working.

Also agreed in the same conversation: remove the two Entry point sections
above (stream detail panel and the create page's template summary), and
remove `status` from preview YAML.

## Design

### "Used" means a route row or a pending approval

A route edit does not change the `routes` row: the proposed config waits in
the approval snapshot and the row keeps the deployed config with status
`pending_update`. So a claim is **used** when either:

- a route row of the stream (status ≠ rejected) has that
  `(transport, listenerPort)`, or
- a pending approval for a route of the stream proposes it (create or
  update snapshot, not yet deployed or rejected).

Every "used" rule below means this: removing a claim (B2), the route-port
check (B4), the entry point owner (B5), the backfill (B1), the edit-ports
modal (F4) and the route form's free list (F5). Deploy also re-checks (B4),
as the backstop for anything that slips through. *(My call: both, rather
than only one of them.)*

### Backend

#### B1. Data model and migration (000052)

- `streams.ports jsonb NOT NULL DEFAULT '[]'`: an array of
  `{"transport": "TCP"|"UDP", "port": <int>}`. Model field
  `Ports []StreamPort` on `models.Stream`, JSON `ports`. Same storage
  pattern as `domains.bound_listeners`. *(Builder's call: JSONB column vs a
  `stream_ports` table. JSONB is my call here, to match domains; switch to
  a table only if a query in the plan needs it.)*
- Backfill: for each stream, the distinct `(transport, port)` that are
  **used** (see above): its routes' `listener_port` with
  `stream_id IS NOT NULL AND status <> 'rejected'`, plus the listener ports
  proposed by its routes' pending approvals, using the same transport
  mapping as `Route.Transport()`. Sorted by transport then port. This keeps
  an approval in flight during the upgrade deployable.
- Down migration drops the column.

#### B2. Validation: `ValidateStreamPorts`

Runs on stream create and on update when `ports` is present. Rejects
(400 unless noted):

- empty set (create, and update to empty);
- transport not TCP/UDP, port outside 1–65535;
- duplicates in the set;
- reserved ports (Envoy internal 19000/19001; the placeholder entry is
  removed, see B6);
- ports outside the template's stream range (`StreamRange()`);
- **409** on collision, shared templates only (`MergeGateways`): another
  stream's claim on the same template and transport, a domain port on the
  template (`UsedPortsByTemplate`), or, for TCP, the template's
  hostname-routed ports. Dedicated templates collide with nothing outside
  the stream. This is today's `CheckPortCollision` logic, moved from
  "route ports" to "stream claims";
- **409** `ErrStreamPortInUse` when an update drops a claim that is
  **used**, including by a pending approval. The message names the port
  and the route, and says when it's a pending change.

Domain create/update (`checkDomainPortsAgainstStreams`) and template update
(`checkMergedTemplateSet`) switch from reading route ports to reading stream
claims, so a domain cannot take a port a stream has claimed but not yet
routed.

#### B3. Gateway lifecycle owned by the stream

- `streamplan.BuildStreamGatewayConfig(stream)` builds one listener per
  claim: name `l4-<tcp|udp>-<port>` (unchanged naming, so migrated routes'
  `sectionName` stays valid), protocol, port. No placeholder. No route
  argument.
- **Create:** validate, persist, then apply the Gateway. Use the
  create-if-missing `UpdateGateway`, not `CreateGateway`, so a leftover
  Gateway with the same name doesn't fail the create. Status and
  StatusMessage as today ("active" / "error: Failed to apply Gateway: …").
- **Update:** `UpdateStreamInput` gains `Ports *[]StreamPort` (full
  replacement set). When present: validate, persist, re-apply the Gateway,
  set status like the domain `applyGateway`. Name-only updates don't touch
  K8s, as today.
- **Delete:** still 409 while routes exist. Then `DeleteGateway` (log
  failure, don't abort, as domain delete does), then delete the row. This
  fixes the orphaned Gateway on stream delete.
- **Empty claims** (legacy streams only, since create requires one): no
  Gateway; apply means delete it.
- **One-shot reconcile at startup:** after migrations, for every stream,
  apply its Gateway from its claims (or delete it when claims are empty).
  This removes the placeholder from legacy empty streams and is a no-op
  for streams whose listeners already match. Idempotent; log per-stream
  failures and continue. *(My call; the alternative is leaving the
  placeholder until the stream is next edited.)*
- `GET /streams/:id/yamls` renders the Gateway from claims.

#### B4. Routes use claims

- `validateL4Listener` becomes: `(transport, listenerPort)` must be one of
  the stream's claims (new `ErrPortNotClaimed`, **400**, message lists the
  stream's claims for that transport), and no other route of the stream
  (status ≠ rejected, excluding self on update) **uses** it, counting
  pending approvals (existing `ErrPortCollision`, **409**). So two pending
  moves can't target the same free claim. Cross-stream, domain and range checks are
  removed from the route path; they happen at claim time. The DB unique
  index `idx_route_stream_proto_port` stays as the backstop.
- Previews (`PreviewCreateForStream`, `PreviewUpdateForStream`) inherit it.
- **`deployL4` no longer calls `UpdateGateway`** and no longer reads the
  live route set. It applies or deletes the TCPRoute/UDPRoute and the
  BackendTrafficPolicy only. The race comment goes with it.
- **Deploy re-checks the port** (create/update deploys): the port must
  still be claimed by the stream and not held by another route row. If
  not, the deploy fails before touching K8s, with the same
  `ErrPortNotClaimed` / `ErrPortCollision` message, and the route keeps
  its deployed state.
- Route delete leaves the claim. Changing a route's port moves it to
  another free claimed port; the Gateway is untouched, only the route's
  `sectionName` changes.
- Rollback to a version whose port is no longer claimed (or now used)
  fails validation with the B4 errors.

#### B5. Entry point

- `ports[]` in the stream entry point now lists **claims**:
  - this stream's claims, owner kind `stream` with `streamId`,
    `streamName` and, when a route uses it, `routeId`, `routeName`,
    `status`, plus `pending: true` when the use is only a pending
    approval;
  - in shared mode, other streams' claims on the template (same owner
    shape) and domain ports (unchanged);
  - in dedicated mode, only this stream's claims.
- The template entry point (create form, `self = nil`) lists, in shared
  mode, every stream claim and domain port on the template, so the form
  can tell which ports are taken.
- `reserved[]` drops the placeholder. `sharedWith` stays in the API (the
  UI stops showing it).
- `ListWithSummary`'s per-stream `ports` becomes the stream's claims.
- Owner kind `stream_route` is replaced by `stream`. Update OpenAPI and
  frontend types together.

#### B6. Placeholder removal

Delete `PlaceholderPort`, `placeholderListenerName`,
`DefaultReservedPorts.Placeholder` and `ReservedReasonPlaceholder`, and
their unit-test assertions (`stream_service_test.go`, `stream_yaml_test.go`,
`route_deploy_l4_test.go`, `route_preview_l4_test.go`,
`route_write_validation_internal_test.go`, `stream_port_collision_test.go`,
`stream_view_test.go`, `streamplan/gateway_test.go`). 60000 becomes a normal
port.

#### B7. No `status` in rendered route YAML

Typed route objects (`HTTPRoute`, `GRPCRoute`, `TCPRoute`, `UDPRoute`) are
marshalled with their zero `status` (`status: parents: null`). Strip
`status` (and a null `metadata.creationTimestamp`) in the shared
marshalling path, so it applies to previews, `/yaml(s)` manifests and
approval diffs, HTTP/gRPC as well as L4. Update the goldens under
`internal/services/testdata/golden/`. *(Scope is my call: one helper for all
route kinds, rather than L4 only.)*

#### B8. API summary

| Endpoint | Change |
|---|---|
| `POST /projects/:p/streams` | body gains required `ports: [{transport, port}]` (≥1); 400/409 per B2 |
| `PATCH /projects/:p/streams/:id` | body gains optional `ports` (full set); 400/409 per B2 |
| `GET /projects/:p/streams`, `GET …/:id` | response includes `ports` (claims) |
| `GET …/:id/entrypoint`, template `…/entrypoint` | `ports[]` from claims, owner kind `stream`; no placeholder in `reserved[]` |
| `POST/PUT …/:id/routes(/:rid)`, previews | 400 `ErrPortNotClaimed`; 409 `ErrPortCollision` (same stream only) |
| `DELETE …/:id` | also deletes the K8s Gateway |

Permissions are unchanged: stream create/update/delete need
`CanManageDomains`; route endpoints need `CanCreateRoutes`. So claiming
ports is a stream-manager decision, and route creators pick from what's
claimed.

### Frontend

#### F1. Types and API

`Stream.ports` (claims), `CreateStreamInput.ports`, `UpdateStreamInput.ports?`,
owner kind `stream` in `EntrypointPortOwner`. Remove 60000 from
`PLATFORM_RESERVED`.

#### F2. Port claim picker (new shared component)

Used by stream create and stream edit. A transport toggle (TCP/UDP), a
port number input and an Add button; claims shown as removable chips
(`5432/TCP`). Live checks: range, reserved, duplicate, and in shared mode
"taken by another stream/domain" from the entry point `ports[]`. Shows
"Port range min–max · X of Y free". On edit, a chip whose port a route uses
can't be removed (disabled, tooltip names the route). Server 400/409 errors
show under the picker. *(Visual design is the builder's call; reuse
`Badge`, `Input`, the L4 route form's protocol radio pattern.)*

#### F3. Stream create

Adds the required Ports picker below the template summary. Submit is
disabled until at least one valid claim exists. `TemplateEntrypointSummary`
loses the "Also used by … / Show who shares it" block; keeps mode, gateway
class, IP and the free-port line.

#### F4. Stream detail

- `StreamEntrypointPanel`: remove "Also used by …" and "N ports used by
  other streams/domains". "This stream's ports" lists every claim with the
  route using it (link + status) or "unused". Empty (legacy) state: "No
  ports claimed. Claim a port to create this stream's Gateway." Keeps IP,
  class, template, reserved line, range line.
- An "Edit ports" button (gated on `canManageDomains`) opens a modal with
  the F2 picker, PATCHes `ports`, and refreshes the panel and manifests.

#### F5. L4 route form

Listener Port becomes a `Select` of the stream's claims for the selected
protocol that no other route uses (plus the route's own port on edit).
Changing protocol refilters it. When none are free: "All of this stream's
TCP ports are in use. Claim another port on the stream." with a link to
the stream (shown only to users who can manage the stream; others see the
text without the link). Removes the free-text input, `findPortCollision`
and the range hint. Server 400/409 still maps to the field.

Preview tab is unchanged; with B3/B4 the route and its policy are the
complete set of objects a route deploy writes.

#### F6. Stream list

The Ports column shows claims.

### Data flow

```
stream create/edit ──validate claims──▶ streams.ports ──build──▶ Gateway (listeners = claims)
route create/edit  ──port ∈ claims, unused──▶ route ──deploy──▶ TCPRoute/UDPRoute + BTP
                                                               (sectionName l4-<t>-<port>)
```

## Testing

- **Backend unit:** `ValidateStreamPorts` (each rule, shared vs dedicated);
  a port held only by a pending approval blocks claim removal and another
  route's request; deploy fails cleanly when its port was unclaimed or
  taken meanwhile; backfill includes pending-approval ports;
  stream create/update/delete apply/delete the Gateway with the right
  listeners; update dropping a used port is 409; route create/update with
  an unclaimed port is 400 and with another route's port is 409; `deployL4`
  makes no Gateway calls; entry point lists claims with owner `stream`;
  domain create collides with a stream claim; migration backfill (SQL,
  plus a Go mirror test if the plan adds one, like `template_migrate.go`);
  startup reconcile; B7 goldens.
- **e2e (`e2e/suites/stream`):** `newStream` takes the ports the test needs
  (each test claims its own); new cases: unclaimed port → 400; removing a
  used claim → 409; **a stream with claimed, unrouted TCP and UDP ports has
  a Gateway Envoy Gateway accepts, and a routed port on the same stream
  still carries traffic** (decision 6); `/yamls` listeners equal the
  claims; stream delete removes the Gateway. Update the legibility test's
  15400→15401 move to claim both ports first.
- **Frontend Jest:** picker component; create page requires a port; panel
  without the removed sections and with "unused" rows; edit-ports modal
  (used chip not removable); route form `Select` filtering and the
  "none free" state; API clients.

## How we will know it worked

- A new stream's Gateway has exactly its claimed listeners; no 60000
  anywhere.
- Route deploys never change the Gateway.
- A route can't take an unclaimed port; a stream can't drop a used one.
- After upgrade, existing streams keep serving with unchanged listeners,
  and empty streams have no Gateway.
- Route previews and manifests have no `status`.

## Rollout

One backend change and one frontend change, merged together (backend
first), then v0.6.0 together with the already-merged L4 legibility work.
Breaking API change for stream create (`ports` required); acceptable at
0.x, call it out in the release notes.
