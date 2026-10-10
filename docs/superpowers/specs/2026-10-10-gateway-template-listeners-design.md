# Gateway Template Listener Model + Stream Port-Range — Design

**Date:** 2026-10-10
**Status:** Design (brainstormed with visual companion; pending spec review)
**Covers:** Review items ② (template protocol rework) and ③ (stream port-range validation), unified.
**Scope:** backend (`backend-v2`) + frontend (`frontend-v2`); includes a data migration.

## Problem

Today a Gateway Template (`models.DomainTemplate`) describes its traffic with a
confusing, non-scalable fixed-slot model:

- `TLSMode` (`no_tls` / `tls_only` / `both`) — a 3-value encoding of "which of two
  fixed listeners (HTTP, HTTPS) exist."
- `TLSPolicy` (`terminate` / `passthrough`) — the HTTPS listener's `tls.mode`.
- `HTTPPort` (80) + `HTTPSPort` (443) — one port each.
- `EnableDomain` / `EnableStream` booleans — the only notion of what a template serves.

This conflates transport with routing, caps a template at one HTTP + one HTTPS
listener, has no representation for gRPC or TLS passthrough, and gives streams
**no port constraint** (ports are entered per-L4-route, validated only as 1–65535
+ a reserved set). The L4/stream side of the Gateway builder (`internal/kubernetes/gateway.go`)
*already* uses a generic listener **list** (`Listeners []L4Listener`), while the
domain side is still the fixed-slot switch — two models side by side.

## Goal

Replace the fixed-slot TLS model with a **generic listener list** that matches
Gateway API's own shape, unify the L7 and L4 sides on it, and add a per-template
**TCP/UDP port range** that constrains stream route ports (item ③). Make the
template declare *what listeners the gateway exposes*, and let each **Domain**
pick *which listeners it binds*.

## Routing-dimension taxonomy (the organizing principle)

Gateway API route types group by **how traffic is routed**:

| Listener protocol | Carries route types | Routed by |
|---|---|---|
| **HTTP / HTTPS** | HTTPRoute **and** GRPCRoute (coexist on one domain) | hostname |
| **TLS** (passthrough) | TLSRoute | SNI (hostname) |
| **TCP / UDP** | TCPRoute / UDPRoute | listener port only |

Two consequences the design must respect:
- **HTTP vs gRPC is a per-route choice, never a template/domain field.** Both ride
  the same HTTP/HTTPS listener; one domain serves HTTPRoutes and GRPCRoutes together.
- Hostname/SNI-routed families (HTTP/HTTPS/TLS) belong to **Domains**; port-routed
  families (TCP/UDP) belong to **Streams**. (TLSRoute is SNI-routed, so it's
  domain-like — not a stream.)

## Design

### 1. Template = a list of typed listeners

A Gateway Template holds a **list of listeners**, each typed by protocol. The UI
groups them into three families, but the underlying model is one list:

```
TemplateListener = {
  Name:     string         // stable, unique within the template; used as the
                           // Gateway API listener sectionName and referenced by
                           // Domain.BoundListeners (e.g. "https-443"). Derivable
                           // from protocol+port, but stored so it's stable.
  Protocol: HTTP | HTTPS | TLS | TCP | UDP
  // hostname-routed families carry a single fixed port:
  Port:     int            // HTTP/HTTPS/TLS
  TLSMode:  Terminate | Passthrough   // HTTPS => Terminate; TLS => Passthrough
  // port-routed family carries a range instead of a single port:
  PortRangeMin, PortRangeMax: int     // TCP/UDP
}
```

- **HTTP / gRPC family** → `HTTP` and/or `HTTPS` listeners (single port each; HTTPS
  is `Terminate`). *Built now.*
- **TLS family** → a `TLS` listener (`Passthrough`). *Reserved in the model, not
  built:* the enum includes `TLS`/`Passthrough`, the UI shows it as a disabled
  "planned" section, and a create/update guard rejects a `TLS` listener with a
  clear "not yet supported" error until the TLSRoute feature ships. *(Deferred.)*
- **TCP / UDP family** → a **single shared port range** (`[min, max]`) that both TCP
  and UDP stream routes draw from. A TCP and a UDP route may share a port number
  (distinct listeners). The template creates no concrete TCP/UDP listener itself —
  those are created per-L4-route today and keep that behavior; the range is a
  *constraint*. (One shared range now; the list could hold per-protocol ranges later
  if ever needed.) *Built now — item ③.*

**Port-conflict validation (template-level):** no two listeners may claim the same
`(port, protocol-at-that-port)`; the TCP/UDP range may not overlap any fixed
HTTP/HTTPS/TLS port; and nothing may use the reserved ports (19000, 19001 Envoy;
60000 placeholder). All validated on template create/update.

**Eligibility is inferred, not toggled** (ruling, confirmed in brainstorming):
a template can back **Domains** iff it has ≥1 hostname-routed listener
(HTTP/HTTPS/TLS); it can back **Streams** iff it has a TCP/UDP range. "At least one
listener" replaces "at least one capability." The `EnableDomain`/`EnableStream`
booleans are **removed**.

### 2. Domain = one hostname + a selected set of listeners

Creating a Domain: pick a template (one with hostname-routed listeners) → enter the
**hostname** → **multi-select which of the template's HTTP/HTTPS/TLS listeners this
domain binds** (option B from brainstorming). The domain's Gateway is built from the
bound listeners with the domain's hostname injected; routes attach to them via
Gateway API `sectionName`.

- This **replaces the per-domain `TLSMode`**: "HTTP only / HTTPS only / both" is now
  "which listeners did you check." A domain can bind HTTP :80 **and** HTTPS :443 for
  the same hostname (same host, different ports, one with TLS), and later a
  terminate HTTPS :443 **and** a passthrough TLS :8443 for the same hostname.
- A domain carries **HTTPRoutes and GRPCRoutes** together on its bound HTTP/HTTPS
  listeners (and, later, a TLSRoute per bound passthrough listener).
- Domain routes do **not** enter a port — the port is the bound listener's.

### 3. Stream = template with a TCP/UDP range; routes validated against it

Creating a Stream is unchanged in shape (pick a template that has a TCP/UDP range →
name → namespace). The new constraint (item ③): every TCP/UDP route's `ListenerPort`
must fall **within the template's range**, enforced at the single existing choke
point `ValidateListenerPort` (`internal/services/stream_port_collision.go`), which
already carries a `ReservedPorts` value and is called from both `CheckPortCollision`
and `validateL4Listener`. The range is sourced from the stream's template. Existing
behavior (1–65535 bound, reserved ports, collision) is unchanged and additive.

### 4. Removed / dissolved / deferred

- **Removed:** `DomainTemplate.{TLSMode, TLSPolicy, HTTPPort, HTTPSPort, EnableDomain,
  EnableStream}` and the equivalent copied fields on `Domain`.
- **Dissolved into:** the listener list (per-listener protocol + port + TLS mode) and
  per-domain listener selection.
- **Deferred (designed, not built):** the `TLS`/`Passthrough` listener and the
  `TLSRoute` route type end-to-end. The model reserves the enum values; the UI shows
  "planned/disabled"; a guard rejects selecting it. A passthrough domain's future
  shape (from brainstorming): **one hostname = one SNI → backends** (weighted LB),
  FastGateway emits one TLSRoute; more SNIs = more domains; no explicit `tls`
  route-creation UX.

## Data model changes (backend)

- `DomainTemplate`: drop the six fields above; add `Listeners []TemplateListener`
  (JSONB column) and, for streams, the TCP/UDP range (either a listener-list entry
  with `PortRangeMin/Max`, or a dedicated `StreamPortRangeMin/Max` pair — plan picks
  the representation; the list-entry form is preferred for uniformity).
- `Domain`: drop its copied `TLSMode/TLSPolicy/HTTPPort/HTTPSPort`; add
  `BoundListeners []string` (the template-listener names this domain binds) +
  keep `Hostname`.
- Gateway builder (`internal/kubernetes/gateway.go`, `internal/domainplan/gateway.go`):
  build domain Gateways from the bound listeners (hostname injected, `sectionName`
  per listener) instead of the `TLSMode` switch. The L4 path is unchanged.
- Validation: template-level port-conflict (§1); domain-level "bound listeners exist
  on the template"; stream route port-in-range (§3).

## Migration (existing templates, domains, streams)

A one-time migration maps old → new so nothing breaks:

- **Templates:**
  - `no_tls` → `[{HTTP, HTTPPort}]`
  - `tls_only` → `[{HTTPS, HTTPSPort, TLSMode=TLSPolicy}]`
  - `both` → `[{HTTP, HTTPPort}, {HTTPS, HTTPSPort, TLSMode=TLSPolicy}]`
  - `EnableStream=true` → assign a **default TCP/UDP range** wide enough that **no
    existing stream route becomes invalid** — default to the full valid range
    (1–65535 minus reserved), which admins can tighten later. (Rationale: migration
    must never retroactively reject a port already in use.)
  - `EnableDomain`/`EnableStream` columns dropped after the data is derived.
- **Domains:** bind the listeners the migration produced for their template that
  match the domain's old `TLSMode` (e.g. an old `both` domain binds the HTTP + HTTPS
  listeners).
- Reversibility: keep the old columns through one release (write-new/read-new,
  old columns nullable) or snapshot before drop — plan decides the exact rollout.

## Frontend changes (`frontend-v2`)

- **Template create/edit form** (`domain-templates/create` + `[id]/edit`, currently
  duplicated — factor a shared component): replace the TLS dropdown + capability
  checkboxes with the **listener list grouped by family** — HTTP/gRPC (add HTTP/HTTPS
  rows), TLS (disabled "planned" section), TCP/UDP (port-range inputs). Live
  port-conflict feedback.
- **Domain create** (`domains/create`): template select (only templates with
  hostname-routed listeners) → hostname → **listener multi-select checklist**.
- **Stream create / L4 route form** (`streams/create`, `components/streams/L4RouteForm.tsx`):
  show the template's allowed TCP/UDP range; validate the port against it client-side
  and surface the backend's authoritative rejection (item ③). `parseListenerPort`
  (`lib/utils/l4route.ts`) gains the range check.

## Testing

- **Backend:** template listener-list validation (port conflict, range-overlap,
  reserved, TLS-guard rejection); eligibility inference; domain binding → Gateway
  listeners with `sectionName`; stream route port in/out of range; the migration
  (old TLSMode/ports/booleans → listeners/bindings/range, incl. the default-range
  rule). Golden-file updates for `domainplan`/`kubernetes` gateway output.
- **Frontend:** the template listener form (add/remove listeners, port-conflict
  surfacing, TLS disabled), the domain listener checklist, the stream route
  range validation (in-range accepted, out-of-range rejected, optimistic on
  capability-fetch error).

## Non-Goals / Deferred

- **TLS passthrough / TLSRoute** end-to-end (built later; model + UI reserve the slot).
- **Per-protocol differentiation of HTTP vs gRPC** at the template/domain level — it's
  per-route and stays that way.
- Arbitrary multi-port beyond what the listener list naturally allows; no new Gateway
  API protocols (the five are stable).

## Global Constraints

- Backend module `github.com/fastgateway-dev/backend-v2`; commit trailer
  `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`; frontend PR footer
  `🤖 Generated with [Claude Code](https://claude.com/claude-code)`.
- The migration must never retroactively invalidate an existing domain, route, or
  stream port.
- `HTTP`/gRPC listeners carry both HTTPRoute and GRPCRoute; a domain binds a *set* of
  listeners (never forced to a single one).
- Reserved ports 19000/19001/60000 remain rejected everywhere.
