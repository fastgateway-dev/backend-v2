# Gateway Template Listener Model — Backend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the Gateway Template's fixed TLS fields with a generic listener list, make Domains bind a selected set of listeners, add a per-template TCP/UDP port range that stream routes are validated against, migrate existing data without changing any live Gateway, and keep the OpenAPI bundle in sync.

**Architecture:** `DomainTemplate` gains a `Listeners []TemplateListener` JSONB column (typed by protocol) plus a TCP/UDP range; `Domain` gains `BoundListeners []string`. The old `TLSMode`/`TLSPolicy`/`HTTPPort`/`HTTPSPort`/`EnableDomain`/`EnableStream` columns are backfilled into the new shape by a golang-migrate SQL migration, then dropped (hard cutover, sole consumer is `frontend-v2`). The gateway builder renders listeners from the new model while preserving today's exact listener names/order/TLS so existing domains' generated Gateways stay byte-identical.

**Tech Stack:** Go 1.26, gorm (runtime ORM), golang-migrate v4 (raw SQL migrations in `migrations/`), gin, Redocly CLI (`make openapi`), golden-file tests under `internal/{kubernetes,domainplan,streamplan,templateplan}/testdata`.

**Spec:** `docs/superpowers/specs/2026-10-10-gateway-template-listeners-design.md`

## Global Constraints

- Backend module `github.com/fastgateway-dev/backend-v2`. Commit trailer EXACTLY: `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`.
- **Hard cutover:** old API/model fields (`tlsMode`, `tlsPolicy`, `httpPort`, `httpsPort`, `enableDomain`, `enableStream`) are removed, not shimmed. Sole consumer is `frontend-v2`.
- **Runtime-safety invariant:** after migration, every existing domain's generated Gateway must be **byte-identical** (same listener names `"http"`/`"https"`, order `[http, https]` for the both-case, ports, and `tls` block). Proven by golden equality.
- **Migration must never retroactively invalidate** an existing domain, route, or stream port (default stream range = full valid range).
- Listener names today: HTTP listener `name: "http"`, HTTPS listener `name: "https"` (hardcoded, `internal/kubernetes/gateway.go`). L4 stream listeners: `l4-<proto>-<port>` / placeholder `l4-placeholder` port 60000 (`streamplan`). Reserved ports 19000/19001 (Envoy), 60000 (placeholder) remain rejected.
- HTTP vs gRPC is per-route; a domain binds a *set* of listeners (never forced to one).
- TLS passthrough is **deferred**: the `TLS`/`Passthrough` enum values exist but a create/update with a `TLS` listener is rejected with a clear "not yet supported" error.
- `make mocks` + `make mocks-check` green after interface changes; `make openapi` re-bundled + `make openapi-check` green; full suite `go test ./... -count=1` green.
- Migrations run with CWD at repo root (`file://migrations` is relative). Next migration number: **000051**.

## Review Focus

- **Migrated "both" domain** must still emit `[{name:http}, {name:https}]` in that order with the same `tls` block → covered in Task 9 (golden equality for migrated fixtures).
- **Port conflict on a template** (e.g. HTTP :443 + HTTPS :443, or a TCP/UDP range overlapping an HTTP/HTTPS port, or a reserved port) must be rejected at create/update → covered in Task 4.
- **TLS (passthrough) listener** on create/update must be rejected "not yet supported" (deferred) → covered in Task 4.
- **Stream route port outside the template's range** rejected; inside accepted; reserved still rejected → covered in Task 8.
- **Eligibility inference**: template with only a TCP/UDP range is stream-only (absent from `?capability=domain`); only HTTP/HTTPS/TLS listeners → domain-only; no listeners at all → rejected ("at least one listener") → covered in Tasks 4 and 6.

---

### Task 1: `TemplateListener` + `Listeners` JSONB types

**Files:**
- Create: `internal/models/template_listener.go`
- Test: `internal/models/template_listener_test.go`

**Interfaces:**
- Produces: `models.ListenerProtocol` (`"HTTP"|"HTTPS"|"TLS"|"TCP"|"UDP"`); `models.ListenerTLSMode` (`"Terminate"|"Passthrough"`); `models.TemplateListener` struct; `models.Listeners` slice type with `Value()`/`Scan()`; helpers `(Listeners).HostnameRouted() []TemplateListener` (HTTP/HTTPS/TLS), `(Listeners).StreamRange() (min, max int, ok bool)` (the TCP/UDP entry).

- [ ] **Step 1: Write the failing test**

```go
package models

import "testing"

func TestListenersValueScanRoundtrip(t *testing.T) {
	in := Listeners{
		{Name: "http", Protocol: ListenerHTTP, Port: 80},
		{Name: "https", Protocol: ListenerHTTPS, Port: 443, TLSMode: TLSListenerTerminate},
		{Name: "tcpudp", Protocol: ListenerTCP, PortRangeMin: 9000, PortRangeMax: 9100},
	}
	v, err := in.Value()
	if err != nil {
		t.Fatalf("Value: %v", err)
	}
	var out Listeners
	if err := out.Scan(v); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(out) != 3 || out[1].Name != "https" || out[1].TLSMode != TLSListenerTerminate {
		t.Fatalf("roundtrip mismatch: %+v", out)
	}
	// nil scan => empty, no error (nullable column)
	var n Listeners
	if err := n.Scan(nil); err != nil || n != nil {
		t.Fatalf("nil scan: got %+v err %v", n, err)
	}
}

func TestListenersHelpers(t *testing.T) {
	ls := Listeners{
		{Name: "https", Protocol: ListenerHTTPS, Port: 443},
		{Name: "tcpudp", Protocol: ListenerTCP, PortRangeMin: 9000, PortRangeMax: 9100},
	}
	if got := ls.HostnameRouted(); len(got) != 1 || got[0].Protocol != ListenerHTTPS {
		t.Fatalf("HostnameRouted: %+v", got)
	}
	min, max, ok := ls.StreamRange()
	if !ok || min != 9000 || max != 9100 {
		t.Fatalf("StreamRange: %d-%d ok=%v", min, max, ok)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/models/ -run 'TestListeners' -v`
Expected: FAIL — `undefined: Listeners` / `undefined: ListenerHTTP`.

- [ ] **Step 3: Write minimal implementation**

```go
package models

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
)

type ListenerProtocol string

const (
	ListenerHTTP  ListenerProtocol = "HTTP"
	ListenerHTTPS ListenerProtocol = "HTTPS"
	ListenerTLS   ListenerProtocol = "TLS"
	ListenerTCP   ListenerProtocol = "TCP"
	ListenerUDP   ListenerProtocol = "UDP"
)

type ListenerTLSMode string

const (
	TLSListenerTerminate   ListenerTLSMode = "Terminate"
	TLSListenerPassthrough ListenerTLSMode = "Passthrough"
)

// TemplateListener is one listener a Gateway Template exposes. Hostname-routed
// families (HTTP/HTTPS/TLS) use Port; the port-routed family (TCP/UDP) uses
// PortRangeMin/Max. Name is the Gateway API sectionName and is referenced by
// Domain.BoundListeners.
type TemplateListener struct {
	Name         string           `json:"name"`
	Protocol     ListenerProtocol `json:"protocol"`
	Port         int              `json:"port,omitempty"`
	TLSMode      ListenerTLSMode  `json:"tlsMode,omitempty"`
	PortRangeMin int              `json:"portRangeMin,omitempty"`
	PortRangeMax int              `json:"portRangeMax,omitempty"`
}

// Listeners is a JSONB-stored slice (mirrors internal/models/telemetry.go's
// Value/Scan pattern for slice-bearing JSONB columns).
type Listeners []TemplateListener

func (l Listeners) Value() (driver.Value, error) { return json.Marshal(l) }

func (l *Listeners) Scan(src interface{}) error {
	if src == nil {
		*l = nil
		return nil
	}
	b, ok := src.([]byte)
	if !ok {
		if s, ok := src.(string); ok {
			b = []byte(s)
		} else {
			return errors.New("Listeners.Scan: unsupported source type")
		}
	}
	return json.Unmarshal(b, l)
}

func (l Listeners) HostnameRouted() []TemplateListener {
	var out []TemplateListener
	for _, x := range l {
		switch x.Protocol {
		case ListenerHTTP, ListenerHTTPS, ListenerTLS:
			out = append(out, x)
		}
	}
	return out
}

// StreamRange returns the single shared TCP/UDP range, if present.
func (l Listeners) StreamRange() (min, max int, ok bool) {
	for _, x := range l {
		if x.Protocol == ListenerTCP || x.Protocol == ListenerUDP {
			return x.PortRangeMin, x.PortRangeMax, true
		}
	}
	return 0, 0, false
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/models/ -run 'TestListeners' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/models/template_listener.go internal/models/template_listener_test.go
git commit -m "feat(models): TemplateListener + Listeners JSONB type

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 2: Pure migration mapping function (old fields → listeners/bindings)

Isolate the old→new mapping as a pure, unit-tested Go function. The SQL migration (Task 3) and any Go backfill reuse the same logic; tests pin the byte-identical mapping before any schema changes.

**Files:**
- Create: `internal/models/template_migrate.go`
- Test: `internal/models/template_migrate_test.go`

**Interfaces:**
- Consumes: Task 1 types.
- Produces:
  - `MigrateTemplateListeners(tlsMode string, httpPort, httpsPort int, tlsPolicy string, enableStream bool) Listeners` — builds the listener list from the old template fields (adds a `9000-9100`... no — adds the full-range TCP/UDP entry `1-65535` when `enableStream`, so no existing stream port becomes invalid).
  - `MigrateDomainBoundListeners(tlsMode string) []string` — the listener names an existing domain binds given its old `TLSMode`.

- [ ] **Step 1: Write the failing test**

```go
package models

import (
	"reflect"
	"testing"
)

func TestMigrateTemplateListeners(t *testing.T) {
	// both + terminate + stream-enabled
	got := MigrateTemplateListeners("both", 80, 443, "terminate", true)
	want := Listeners{
		{Name: "http", Protocol: ListenerHTTP, Port: 80},
		{Name: "https", Protocol: ListenerHTTPS, Port: 443, TLSMode: TLSListenerTerminate},
		{Name: "tcpudp", Protocol: ListenerTCP, PortRangeMin: 1, PortRangeMax: 65535},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("both+stream:\n got %+v\nwant %+v", got, want)
	}
	// tls_only + passthrough, no stream
	got = MigrateTemplateListeners("tls_only", 80, 443, "passthrough", false)
	want = Listeners{{Name: "https", Protocol: ListenerHTTPS, Port: 443, TLSMode: TLSListenerPassthrough}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tls_only+passthrough:\n got %+v\nwant %+v", got, want)
	}
	// no_tls
	got = MigrateTemplateListeners("no_tls", 8080, 443, "terminate", false)
	want = Listeners{{Name: "http", Protocol: ListenerHTTP, Port: 8080}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("no_tls:\n got %+v\nwant %+v", got, want)
	}
}

func TestMigrateDomainBoundListeners(t *testing.T) {
	cases := map[string][]string{
		"both":     {"http", "https"},
		"tls_only": {"https"},
		"no_tls":   {"http"},
	}
	for mode, want := range cases {
		if got := MigrateDomainBoundListeners(mode); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: got %v want %v", mode, got, want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/models/ -run 'TestMigrate' -v`
Expected: FAIL — `undefined: MigrateTemplateListeners`.

- [ ] **Step 3: Write minimal implementation**

```go
package models

// MigrateTemplateListeners maps the pre-listener template fields onto the
// listener list, preserving listener NAMES ("http"/"https") so generated
// Gateways stay byte-identical. A stream-enabled template gets the full valid
// TCP/UDP range so no existing stream route port becomes invalid.
func MigrateTemplateListeners(tlsMode string, httpPort, httpsPort int, tlsPolicy string, enableStream bool) Listeners {
	tlsm := TLSListenerTerminate
	if tlsPolicy == "passthrough" {
		tlsm = TLSListenerPassthrough
	}
	http := TemplateListener{Name: "http", Protocol: ListenerHTTP, Port: httpPort}
	https := TemplateListener{Name: "https", Protocol: ListenerHTTPS, Port: httpsPort, TLSMode: tlsm}

	var ls Listeners
	switch tlsMode {
	case "no_tls":
		ls = Listeners{http}
	case "tls_only":
		ls = Listeners{https}
	case "both":
		ls = Listeners{http, https}
	default:
		ls = Listeners{https} // mirror gateway.go default (secret-driven); safe fallback
	}
	if enableStream {
		ls = append(ls, TemplateListener{Name: "tcpudp", Protocol: ListenerTCP, PortRangeMin: 1, PortRangeMax: 65535})
	}
	return ls
}

// MigrateDomainBoundListeners returns the listener names an existing domain
// binds, derived from its old TLSMode.
func MigrateDomainBoundListeners(tlsMode string) []string {
	switch tlsMode {
	case "no_tls":
		return []string{"http"}
	case "both":
		return []string{"http", "https"}
	default: // tls_only and unknown
		return []string{"https"}
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/models/ -run 'TestMigrate' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/models/template_migrate.go internal/models/template_migrate_test.go
git commit -m "feat(models): pure old->listener migration mapping

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 3: SQL migration 000051 (add columns, backfill, drop old)

**Files:**
- Create: `migrations/000051_template_listeners.up.sql`
- Create: `migrations/000051_template_listeners.down.sql`

**Interfaces:**
- Consumes: the mapping logic from Task 2 (re-expressed in SQL; the Go function is the source of truth and the golden tests in Task 9 prove parity).
- Produces: `domain_templates.listeners JSONB`, `domain_templates.bound_*` n/a; `domains.bound_listeners JSONB`; old columns dropped.

- [ ] **Step 1: Write the up migration**

`migrations/000051_template_listeners.up.sql`:
```sql
-- Add new columns (nullable JSONB, matches 000035 telemetry pattern)
ALTER TABLE domain_templates ADD COLUMN listeners JSONB;
ALTER TABLE domains ADD COLUMN bound_listeners JSONB;

-- Backfill domain_templates.listeners from tls_mode/http_port/https_port/tls_policy/enable_stream.
-- Listener names "http"/"https" are preserved verbatim (byte-identical invariant).
UPDATE domain_templates SET listeners = (
  (CASE
     WHEN tls_mode = 'no_tls'  THEN jsonb_build_array(
       jsonb_build_object('name','http','protocol','HTTP','port',http_port))
     WHEN tls_mode = 'both'    THEN jsonb_build_array(
       jsonb_build_object('name','http','protocol','HTTP','port',http_port),
       jsonb_build_object('name','https','protocol','HTTPS','port',https_port,
         'tlsMode', CASE WHEN tls_policy='passthrough' THEN 'Passthrough' ELSE 'Terminate' END))
     ELSE jsonb_build_array(  -- tls_only + unknown
       jsonb_build_object('name','https','protocol','HTTPS','port',https_port,
         'tlsMode', CASE WHEN tls_policy='passthrough' THEN 'Passthrough' ELSE 'Terminate' END))
   END)
  ||
  (CASE WHEN enable_stream THEN jsonb_build_array(
       jsonb_build_object('name','tcpudp','protocol','TCP','portRangeMin',1,'portRangeMax',65535))
     ELSE '[]'::jsonb END)
);

-- Backfill domains.bound_listeners from their tls_mode.
UPDATE domains SET bound_listeners = (CASE
  WHEN tls_mode = 'no_tls' THEN jsonb_build_array('http')
  WHEN tls_mode = 'both'   THEN jsonb_build_array('http','https')
  ELSE jsonb_build_array('https')
END);

-- Make new columns NOT NULL now that they're populated.
ALTER TABLE domain_templates ALTER COLUMN listeners SET NOT NULL;
ALTER TABLE domains ALTER COLUMN bound_listeners SET NOT NULL;

-- Drop the index that references tls_mode, then the old columns (hard cutover).
DROP INDEX IF EXISTS idx_domain_templates_tls_mode;
ALTER TABLE domain_templates
  DROP COLUMN tls_mode, DROP COLUMN tls_policy,
  DROP COLUMN http_port, DROP COLUMN https_port,
  DROP COLUMN enable_domain, DROP COLUMN enable_stream;
ALTER TABLE domains
  DROP COLUMN tls_mode, DROP COLUMN tls_policy,
  DROP COLUMN http_port, DROP COLUMN https_port;
```

- [ ] **Step 2: Write the down migration**

`migrations/000051_template_listeners.down.sql` (reconstruct the old columns from the listeners; mirrors the data-guard style of 000049):
```sql
-- Re-add old columns with their original defaults.
ALTER TABLE domain_templates
  ADD COLUMN tls_mode VARCHAR(50) NOT NULL DEFAULT 'tls_only',
  ADD COLUMN tls_policy VARCHAR(50) NOT NULL DEFAULT 'terminate',
  ADD COLUMN http_port INTEGER NOT NULL DEFAULT 80,
  ADD COLUMN https_port INTEGER NOT NULL DEFAULT 443,
  ADD COLUMN enable_domain BOOLEAN NOT NULL DEFAULT true,
  ADD COLUMN enable_stream BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE domains
  ADD COLUMN tls_mode VARCHAR(50) NOT NULL DEFAULT 'tls_only',
  ADD COLUMN tls_policy VARCHAR(50) NOT NULL DEFAULT 'terminate',
  ADD COLUMN http_port INTEGER NOT NULL DEFAULT 80,
  ADD COLUMN https_port INTEGER NOT NULL DEFAULT 443;

-- Best-effort reconstruction from listeners.
UPDATE domain_templates SET
  enable_stream = (listeners @> '[{"protocol":"TCP"}]' OR listeners @> '[{"protocol":"UDP"}]'),
  enable_domain = (listeners @> '[{"protocol":"HTTP"}]' OR listeners @> '[{"protocol":"HTTPS"}]'),
  http_port  = COALESCE((SELECT (e->>'port')::int FROM jsonb_array_elements(listeners) e WHERE e->>'protocol'='HTTP'  LIMIT 1), 80),
  https_port = COALESCE((SELECT (e->>'port')::int FROM jsonb_array_elements(listeners) e WHERE e->>'protocol'='HTTPS' LIMIT 1), 443),
  tls_mode = CASE
    WHEN listeners @> '[{"protocol":"HTTP"}]' AND listeners @> '[{"protocol":"HTTPS"}]' THEN 'both'
    WHEN listeners @> '[{"protocol":"HTTP"}]' THEN 'no_tls'
    ELSE 'tls_only' END,
  tls_policy = COALESCE((SELECT lower(e->>'tlsMode') FROM jsonb_array_elements(listeners) e WHERE e->>'protocol'='HTTPS' LIMIT 1), 'terminate');

ALTER TABLE domain_templates DROP COLUMN listeners;
ALTER TABLE domains DROP COLUMN bound_listeners;
CREATE INDEX idx_domain_templates_tls_mode ON domain_templates(tls_mode);
```

- [ ] **Step 3: Apply the migration against a scratch DB and verify round-trip**

Run (against a disposable Postgres — the repo's test DB or a throwaway container, with CWD at repo root):
```bash
go run ./cmd/migrate up     # DATABASE_URL pointing at the scratch DB
```
Expected: no error; `\d domain_templates` shows `listeners jsonb not null` and no `tls_mode`; a seeded "both" template row has `listeners = [{name:http...},{name:https...}]`. Then `go run ./cmd/migrate down` restores the old columns without error.

- [ ] **Step 4: Commit**

```bash
git add migrations/000051_template_listeners.up.sql migrations/000051_template_listeners.down.sql
git commit -m "feat(migration): template/domain listener columns (000051)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 4: DomainTemplate model + service (listeners replace TLS fields)

**Files:**
- Modify: `internal/models/domain_template.go:147-201` (remove `TLSMode`/`TLSPolicy`/`HTTPPort`/`HTTPSPort`/`EnableDomain`/`EnableStream`; add `Listeners Listeners gorm:"type:jsonb;not null"`)
- Modify: `internal/services/domain_template_service.go` (inputs + `Create`/`Update` validation; remove `NormalizeTemplateCapabilities`)
- Modify: `internal/models/domain_template.go` enum block (keep `TLSMode` type only if still referenced elsewhere; otherwise remove)
- Test: `internal/services/domain_template_listeners_test.go`

**Interfaces:**
- Consumes: Task 1 types.
- Produces: `CreateDomainTemplateInput.Listeners []TemplateListenerInput` (and `UpdateDomainTemplateInput.Listeners *[]TemplateListenerInput`); `ValidateTemplateListeners(ls models.Listeners) error`; errors `ErrNoListener`, `ErrListenerPortConflict`, `ErrTLSPassthroughNotSupported`, `ErrInvalidListener`.

- [ ] **Step 1: Write the failing test** (validation is the load-bearing new logic)

```go
package services

import (
	"errors"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/models"
)

func TestValidateTemplateListeners(t *testing.T) {
	ok := models.Listeners{
		{Name: "http", Protocol: models.ListenerHTTP, Port: 80},
		{Name: "https", Protocol: models.ListenerHTTPS, Port: 443, TLSMode: models.TLSListenerTerminate},
		{Name: "tcpudp", Protocol: models.ListenerTCP, PortRangeMin: 9000, PortRangeMax: 9100},
	}
	if err := ValidateTemplateListeners(ok); err != nil {
		t.Fatalf("valid listeners rejected: %v", err)
	}
	// empty -> ErrNoListener
	if err := ValidateTemplateListeners(nil); !errors.Is(err, ErrNoListener) {
		t.Fatalf("empty: want ErrNoListener, got %v", err)
	}
	// port conflict: HTTP 443 + HTTPS 443
	conflict := models.Listeners{
		{Name: "http", Protocol: models.ListenerHTTP, Port: 443},
		{Name: "https", Protocol: models.ListenerHTTPS, Port: 443},
	}
	if err := ValidateTemplateListeners(conflict); !errors.Is(err, ErrListenerPortConflict) {
		t.Fatalf("conflict: want ErrListenerPortConflict, got %v", err)
	}
	// range overlaps a fixed port
	overlap := models.Listeners{
		{Name: "https", Protocol: models.ListenerHTTPS, Port: 9050},
		{Name: "tcpudp", Protocol: models.ListenerTCP, PortRangeMin: 9000, PortRangeMax: 9100},
	}
	if err := ValidateTemplateListeners(overlap); !errors.Is(err, ErrListenerPortConflict) {
		t.Fatalf("overlap: want ErrListenerPortConflict, got %v", err)
	}
	// reserved port
	reserved := models.Listeners{{Name: "http", Protocol: models.ListenerHTTP, Port: 19000}}
	if err := ValidateTemplateListeners(reserved); !errors.Is(err, ErrReservedPort) {
		t.Fatalf("reserved: want ErrReservedPort, got %v", err)
	}
	// TLS passthrough deferred
	tls := models.Listeners{{Name: "tls", Protocol: models.ListenerTLS, Port: 8443, TLSMode: models.TLSListenerPassthrough}}
	if err := ValidateTemplateListeners(tls); !errors.Is(err, ErrTLSPassthroughNotSupported) {
		t.Fatalf("tls: want ErrTLSPassthroughNotSupported, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/services/ -run 'TestValidateTemplateListeners' -v`
Expected: FAIL — `undefined: ValidateTemplateListeners`.

- [ ] **Step 3: Write minimal implementation**

Add to `domain_template_service.go` (reuse `DefaultReservedPorts` + `ValidateListenerPort` from `stream_port_collision.go`):

```go
var (
	ErrNoListener                 = errors.New("template must declare at least one listener")
	ErrListenerPortConflict       = errors.New("listener ports conflict")
	ErrTLSPassthroughNotSupported = errors.New("TLS passthrough listeners are not yet supported")
	ErrInvalidListener            = errors.New("invalid listener")
)

// ValidateTemplateListeners enforces: at least one listener; no TLS passthrough
// (deferred); each fixed port and the TCP/UDP range within 1-65535 and not
// reserved; and no port overlaps across listeners (fixed-vs-fixed and
// fixed-vs-range). The range endpoints must satisfy min<=max.
func ValidateTemplateListeners(ls models.Listeners) error {
	if len(ls) == 0 {
		return ErrNoListener
	}
	fixed := map[int]models.ListenerProtocol{} // port -> protocol (HTTP/HTTPS/TLS)
	var rangeMin, rangeMax int
	haveRange := false
	for _, l := range ls {
		switch l.Protocol {
		case models.ListenerTLS:
			return ErrTLSPassthroughNotSupported
		case models.ListenerHTTP, models.ListenerHTTPS:
			if err := ValidateListenerPort(l.Port, DefaultReservedPorts); err != nil {
				return err // ErrInvalidListenerPort / ErrReservedPort
			}
			if _, dup := fixed[l.Port]; dup {
				return fmt.Errorf("%w: port %d used twice", ErrListenerPortConflict, l.Port)
			}
			fixed[l.Port] = l.Protocol
		case models.ListenerTCP, models.ListenerUDP:
			if haveRange {
				return fmt.Errorf("%w: multiple TCP/UDP ranges", ErrInvalidListener)
			}
			if l.PortRangeMin < 1 || l.PortRangeMax > 65535 || l.PortRangeMin > l.PortRangeMax {
				return fmt.Errorf("%w: bad range %d-%d", ErrInvalidListener, l.PortRangeMin, l.PortRangeMax)
			}
			rangeMin, rangeMax, haveRange = l.PortRangeMin, l.PortRangeMax, true
		default:
			return fmt.Errorf("%w: unknown protocol %q", ErrInvalidListener, l.Protocol)
		}
	}
	if haveRange {
		for p := range fixed {
			if p >= rangeMin && p <= rangeMax {
				return fmt.Errorf("%w: port %d overlaps TCP/UDP range %d-%d", ErrListenerPortConflict, p, rangeMin, rangeMax)
			}
		}
	}
	return nil
}
```

Then:
- Replace `CreateDomainTemplateInput`'s `TLSMode/HTTPPort/HTTPSPort/TLSPolicy/EnableDomain/EnableStream` with `Listeners []models.TemplateListener` (JSON `listeners`, `binding:"required"`). Same for `UpdateDomainTemplateInput` (`Listeners *models.Listeners`).
- In `Create`/`Update`: call `ValidateTemplateListeners(input.Listeners)` instead of the TLS-mode/port/capability validation; set `dt.Listeners = input.Listeners`. Delete `NormalizeTemplateCapabilities` and `ErrNoTemplateCapability` (eligibility is now listener-derived). Remove the port-default (80/443) block.
- Remove the `TLSMode`/ports/`TLSPolicy`/`EnableDomain`/`EnableStream` fields from the `models.DomainTemplate` struct; add `Listeners models.Listeners gorm:"type:jsonb;not null" json:"listeners"`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/services/ -run 'TestValidateTemplateListeners' -v`
Expected: PASS. (`go build ./...` will still fail in the other construction sites — Tasks 5–10 — which is expected; verify this task via the package test.)

- [ ] **Step 5: Commit**

```bash
git add internal/models/domain_template.go internal/services/domain_template_service.go internal/services/domain_template_listeners_test.go
git commit -m "feat(templates): listener list replaces TLSMode/ports/capability flags

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 5: Template preview + manifests projection

**Files:**
- Modify: `internal/services/domain_template_manifests.go` — `templateFromCreateInput` (:36-69, copy `Listeners` instead of TLS/ports) and `PreviewCreate`'s synthetic `exampleDomain` (:319-335, build from the template's listeners + an example hostname, binding all hostname-routed listeners).
- Test: `internal/services/domain_template_manifests_test.go` (extend existing or add)

**Interfaces:**
- Consumes: Task 4 inputs; Task 10's `BuildGatewayConfigFromListeners` (define the signature here; Task 10 implements — `func domainplan.BuildGatewayConfig(domain *models.Domain, template *models.DomainTemplate, templateAnnotations Annotations) *kubernetes.GatewayConfig`).

- [ ] **Step 1: Write the failing test**

```go
func TestTemplateFromCreateInput_CopiesListeners(t *testing.T) {
	in := CreateDomainTemplateInput{
		Name: "t", ExposureType: "public",
		Listeners: []models.TemplateListener{{Name: "https", Protocol: models.ListenerHTTPS, Port: 443}},
	}
	dt := templateFromCreateInput(projectID, in) // existing helper signature
	if len(dt.Listeners) != 1 || dt.Listeners[0].Name != "https" {
		t.Fatalf("listeners not copied: %+v", dt.Listeners)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/services/ -run 'TestTemplateFromCreateInput_CopiesListeners' -v` → FAIL (field/arg mismatch).

- [ ] **Step 3: Implement** — in `templateFromCreateInput`, set `dt.Listeners = in.Listeners` (drop TLS/port copying). In `PreviewCreate`, replace the synthetic `models.Domain{TLSMode, HTTPPort, ...}` with a `models.Domain{Hostname: "example.com", BoundListeners: <names of all hostname-routed listeners in the template>}` and call the new `domainplan.BuildGatewayConfig(exampleDomain, dt, dt.Annotations)` signature (Task 10). Pass the template through so the builder can resolve bound listener names → ports/protocol/tls.

- [ ] **Step 4: Run** the test → PASS. (Full build still red until Task 10.)

- [ ] **Step 5: Commit**

```bash
git add internal/services/domain_template_manifests.go internal/services/domain_template_manifests_test.go
git commit -m "feat(templates): preview/manifests use listener model

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 6: Template handler audit keys + capability filter (inferred)

**Files:**
- Modify: `internal/handlers/domain_template_handler.go:98-101` (audit payload: replace `tlsMode`/`httpPort`/`httpsPort` with `listeners` count / summary)
- Modify: `internal/repository/domain_template_repository.go:48-57` (capability filter: `enable_domain = true` → "has a hostname-routed listener"; `enable_stream = true` → "has a TCP/UDP range", using JSONB containment)
- Test: `internal/repository/domain_template_repository_test.go` (filter) — if no DB in unit tests, add a focused SQL-builder test or cover via the service test with a fake repo; otherwise test `capabilityWhere(capability string) (string, []any)` as a pure helper.

**Interfaces:**
- Produces: `capabilityListenerClause(capability string) (clause string, args []any, ok bool)` — pure helper returning the JSONB `WHERE` fragment; `""` capability → no clause.

- [ ] **Step 1: Write the failing test**

```go
func TestCapabilityListenerClause(t *testing.T) {
	c, _, ok := capabilityListenerClause("stream")
	if !ok || !strings.Contains(c, "TCP") || !strings.Contains(c, "UDP") {
		t.Fatalf("stream clause wrong: %q ok=%v", c, ok)
	}
	c, _, ok = capabilityListenerClause("domain")
	if !ok || !strings.Contains(c, "HTTP") {
		t.Fatalf("domain clause wrong: %q ok=%v", c, ok)
	}
	if _, _, ok := capabilityListenerClause(""); ok {
		t.Fatal("empty capability must yield no clause")
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/repository/ -run 'TestCapabilityListenerClause' -v` → FAIL.

- [ ] **Step 3: Implement**

```go
// capabilityListenerClause maps a capability filter to a JSONB WHERE fragment
// over the listeners column. domain => any HTTP/HTTPS/TLS listener; stream =>
// any TCP/UDP listener (the range entry).
func capabilityListenerClause(capability string) (string, []any, bool) {
	switch capability {
	case "domain":
		return `(listeners @> ? OR listeners @> ? OR listeners @> ?)`,
			[]any{`[{"protocol":"HTTP"}]`, `[{"protocol":"HTTPS"}]`, `[{"protocol":"TLS"}]`}, true
	case "stream":
		return `(listeners @> ? OR listeners @> ?)`,
			[]any{`[{"protocol":"TCP"}]`, `[{"protocol":"UDP"}]`}, true
	default:
		return "", nil, false
	}
}
```
Use it in the repo's `ListByProjectID` (`if clause, args, ok := capabilityListenerClause(capability); ok { query = query.Where(clause, args...) }`). Update the handler audit payload to log `listenerCount`/the protocols present.

- [ ] **Step 4: Run** the test → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/repository/domain_template_repository.go internal/handlers/domain_template_handler.go internal/repository/domain_template_repository_test.go
git commit -m "feat(templates): infer domain/stream eligibility from listeners

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 7: Domain model + service — bind selected listeners

**Files:**
- Modify: `internal/models/domain.go:27-59` (remove `HTTPPort`/`HTTPSPort`/`TLSMode`/`TLSPolicy`; add `BoundListeners pq.StringArray`/`models.StringList` JSONB `gorm:"type:jsonb;not null"`)
- Modify: `internal/services/domain_service.go` — `CreateDomainInput` gains `BoundListeners []string binding:"required"`; `Create` (:370-431) validates the names exist on the template's hostname-routed listeners and sets `domain.BoundListeners` instead of copying TLS/ports; keep the "TLS secret required" rule but drive it off "binds an HTTPS terminate listener".
- Test: `internal/services/domain_bind_listeners_test.go`

**Interfaces:**
- Consumes: Task 1/4 (`models.Listeners`, template with `Listeners`).
- Produces: `ValidateBoundListeners(tmpl *models.DomainTemplate, names []string) error` (`ErrUnknownBoundListener`, `ErrNoBoundListener`); `domainNeedsTLSSecret(tmpl, names) bool`.

- [ ] **Step 1: Write the failing test**

```go
func TestValidateBoundListeners(t *testing.T) {
	tmpl := &models.DomainTemplate{Listeners: models.Listeners{
		{Name: "http", Protocol: models.ListenerHTTP, Port: 80},
		{Name: "https", Protocol: models.ListenerHTTPS, Port: 443, TLSMode: models.TLSListenerTerminate},
		{Name: "tcpudp", Protocol: models.ListenerTCP, PortRangeMin: 9000, PortRangeMax: 9100},
	}}
	if err := ValidateBoundListeners(tmpl, []string{"http", "https"}); err != nil {
		t.Fatalf("valid binding rejected: %v", err)
	}
	if err := ValidateBoundListeners(tmpl, []string{"nope"}); !errors.Is(err, ErrUnknownBoundListener) {
		t.Fatalf("unknown: want ErrUnknownBoundListener, got %v", err)
	}
	// cannot bind a TCP/UDP (non-hostname) listener to a domain
	if err := ValidateBoundListeners(tmpl, []string{"tcpudp"}); !errors.Is(err, ErrUnknownBoundListener) {
		t.Fatalf("stream listener bound to domain: want error, got %v", err)
	}
	if err := ValidateBoundListeners(tmpl, nil); !errors.Is(err, ErrNoBoundListener) {
		t.Fatalf("empty: want ErrNoBoundListener, got %v", err)
	}
	if domainNeedsTLSSecret(tmpl, []string{"https"}) != true || domainNeedsTLSSecret(tmpl, []string{"http"}) != false {
		t.Fatal("TLS-secret-needed detection wrong")
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/services/ -run 'TestValidateBoundListeners' -v` → FAIL.

- [ ] **Step 3: Implement** `ValidateBoundListeners` (every name must match a hostname-routed listener on the template; at least one) and `domainNeedsTLSSecret` (true iff any bound listener is HTTPS with `Terminate`). Rework `DomainService.Create`: validate `input.BoundListeners`, set `domain.BoundListeners`, require `TLSSecretName` when `domainNeedsTLSSecret`. Drop the `HTTPPort/HTTPSPort/TLSMode/TLSPolicy` copy.

- [ ] **Step 4: Run** the test → PASS. (Full build red until Task 10.)

- [ ] **Step 5: Commit**

```bash
git add internal/models/domain.go internal/services/domain_service.go internal/services/domain_bind_listeners_test.go
git commit -m "feat(domains): bind a selected set of template listeners

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 8: Stream TCP/UDP port-range enforcement (item ③)

**Files:**
- Modify: `internal/services/stream_port_collision.go` — add `templatePortRange(tmpl *models.DomainTemplate) (min, max int, ok bool)` next to `templateDomainDefaults` (:85-97); enforce it in `CheckPortCollision` (:194-250) right after the template loads.
- Test: `internal/services/stream_port_range_test.go`

**Interfaces:**
- Consumes: `models.Listeners.StreamRange()` (Task 1).
- Produces: `ErrPortOutOfRange`; `templatePortRange`.

- [ ] **Step 1: Write the failing test**

```go
func TestTemplatePortRange(t *testing.T) {
	tmpl := &models.DomainTemplate{Listeners: models.Listeners{
		{Name: "tcpudp", Protocol: models.ListenerUDP, PortRangeMin: 9000, PortRangeMax: 9100},
	}}
	min, max, ok := templatePortRange(tmpl)
	if !ok || min != 9000 || max != 9100 {
		t.Fatalf("range: %d-%d ok=%v", min, max, ok)
	}
	if err := checkPortInRange(9042, tmpl); err != nil {
		t.Fatalf("in-range rejected: %v", err)
	}
	if err := checkPortInRange(8125, tmpl); !errors.Is(err, ErrPortOutOfRange) {
		t.Fatalf("out-of-range: want ErrPortOutOfRange, got %v", err)
	}
	// template with no range (domain-only) => stream route rejected
	noRange := &models.DomainTemplate{Listeners: models.Listeners{{Name: "https", Protocol: models.ListenerHTTPS, Port: 443}}}
	if err := checkPortInRange(9042, noRange); !errors.Is(err, ErrPortOutOfRange) {
		t.Fatalf("no-range template: want ErrPortOutOfRange, got %v", err)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/services/ -run 'TestTemplatePortRange' -v` → FAIL.

- [ ] **Step 3: Implement**

```go
var ErrPortOutOfRange = errors.New("listener port is outside the template's TCP/UDP range")

func templatePortRange(tmpl *models.DomainTemplate) (int, int, bool) {
	return tmpl.Listeners.StreamRange()
}

func checkPortInRange(port int, tmpl *models.DomainTemplate) error {
	min, max, ok := templatePortRange(tmpl)
	if !ok {
		return fmt.Errorf("%w: template has no TCP/UDP range", ErrPortOutOfRange)
	}
	if port < min || port > max {
		return fmt.Errorf("%w: %d not in %d-%d", ErrPortOutOfRange, port, min, max)
	}
	return nil
}
```
In `CheckPortCollision`, after `tmpl` loads and `ValidateListenerPort` passes, call `checkPortInRange(port, tmpl)` and return its error. Map `ErrPortOutOfRange` to HTTP 400 in the stream route handler (follow the existing `streamError` precondition mapping).

- [ ] **Step 4: Run** the test → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/services/stream_port_collision.go internal/services/stream_port_range_test.go internal/handlers/stream_handler.go
git commit -m "feat(streams): enforce template TCP/UDP port range (item 3)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 9: Gateway builder — render listeners, byte-identical (THE invariant)

**Files:**
- Modify: `internal/kubernetes/gateway.go` — `GatewayConfig` carries the resolved hostname-routed listeners (reuse the existing `buildHTTPListener`/`buildHTTPSListener` helpers; drive them from the bound-listener set instead of the `TLSMode` switch, preserving names `"http"`/`"https"` and order `[http, https]`).
- Test: `internal/kubernetes/gateway_byte_identical_test.go` + regenerate affected goldens under `internal/kubernetes/testdata/`.

**Interfaces:**
- Consumes: the `GatewayConfig` populated by Task 10.
- Produces: unchanged Gateway object shape (names/ports/tls preserved).

- [ ] **Step 1: Write the failing (equality) test** — pin that a "both" domain still yields `[http, https]` with the same TLS block as the pre-change builder. Capture the current output as the golden *before* changing the builder:

```go
func TestGateway_BothListeners_ByteIdentical(t *testing.T) {
	cfg := &kubernetes.GatewayConfig{ // populated the NEW way (bound HTTP+HTTPS)
		Name: "gw", Namespace: "ns", GatewayClassName: "gc", Hostname: "api.example.com",
		HostnameListeners: []kubernetes.HostnameListener{
			{Name: "http", Protocol: "HTTP", Port: 80},
			{Name: "https", Protocol: "HTTPS", Port: 443, TLSMode: "Terminate"},
		},
		TLSSecretName: "api-tls",
	}
	obj := kubernetes.BuildGatewayObject(cfg)
	ls := obj["spec"].(map[string]interface{})["listeners"].([]interface{})
	if len(ls) != 2 || ls[0].(map[string]interface{})["name"] != "http" || ls[1].(map[string]interface{})["name"] != "https" {
		t.Fatalf("listener names/order changed: %+v", ls)
	}
	https := ls[1].(map[string]interface{})
	if https["tls"].(map[string]interface{})["mode"] != "Terminate" {
		t.Fatalf("tls mode changed: %+v", https)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/kubernetes/ -run 'ByteIdentical' -v` → FAIL (new fields/shape not present).

- [ ] **Step 3: Implement** — add `HostnameListeners []HostnameListener` (`{Name, Protocol, Port, TLSMode}`) to `GatewayConfig` (with `json:"-"` like the L4 `Listeners`, to protect goldens). Replace the `TLSMode` switch (:110-124) with iteration over `HostnameListeners`, calling `buildHTTPListener`/`buildHTTPSListener` by protocol and setting each listener's `name` from the listener's `Name` (which is `"http"`/`"https"` for migrated domains) and `tls.mode` from its `TLSMode`. Keep the L4 override path unchanged. Remove the old scalar `TLSMode/HTTPPort/HTTPSPort/TLSPolicy` fields from `GatewayConfig`.

- [ ] **Step 4: Run + regenerate goldens**

Run: `go test ./internal/kubernetes/... -run 'ByteIdentical' -v` → PASS. Then regenerate the domain-gateway goldens and **diff them**: the migrated-domain goldens must be unchanged (byte-identical invariant). Run `go test ./internal/kubernetes/... ./internal/domainplan/... -count=1` and inspect any golden diff — a non-empty diff on an existing domain fixture is a FAILURE of the invariant, not a golden to accept.

- [ ] **Step 5: Commit**

```bash
git add internal/kubernetes/gateway.go internal/kubernetes/gateway_byte_identical_test.go internal/kubernetes/testdata/
git commit -m "feat(kubernetes): render Gateway listeners from the listener model (byte-identical)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 10: domainplan — resolve bound listeners → GatewayConfig

**Files:**
- Modify: `internal/domainplan/gateway.go` — `BuildGatewayConfig(domain *models.Domain, template *models.DomainTemplate, templateAnnotations models.Annotations) *kubernetes.GatewayConfig` (new signature: now needs the template to resolve `domain.BoundListeners` names → protocol/port/tls).
- Modify: all callers — `internal/services/domain_service.go` (Create :443, applyGateway :563), `internal/services/domain_template_manifests.go` (PreviewCreate), any other `BuildGatewayConfig` caller.
- Test: `internal/domainplan/gateway_test.go` + goldens under `internal/domainplan/testdata/`.

**Interfaces:**
- Consumes: `domain.BoundListeners`, `template.Listeners`.
- Produces: `kubernetes.GatewayConfig.HostnameListeners` populated from the bound listeners (name/protocol/port/tls), preserving names.

- [ ] **Step 1: Write the failing test**

```go
func TestBuildGatewayConfig_BoundListeners(t *testing.T) {
	tmpl := &models.DomainTemplate{Listeners: models.Listeners{
		{Name: "http", Protocol: models.ListenerHTTP, Port: 80},
		{Name: "https", Protocol: models.ListenerHTTPS, Port: 443, TLSMode: models.TLSListenerTerminate},
	}}
	d := &models.Domain{K8sGatewayName: "gw", Namespace: "ns", K8sGatewayClass: "gc",
		Hostname: "api.example.com", BoundListeners: []string{"http", "https"}, TLSSecretName: "api-tls"}
	cfg := BuildGatewayConfig(d, tmpl, nil)
	if len(cfg.HostnameListeners) != 2 || cfg.HostnameListeners[0].Name != "http" || cfg.HostnameListeners[1].Name != "https" {
		t.Fatalf("bound listeners not resolved in order: %+v", cfg.HostnameListeners)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/domainplan/ -run 'BoundListeners' -v` → FAIL.

- [ ] **Step 3: Implement** — `BuildGatewayConfig` resolves each name in `domain.BoundListeners` against `template.Listeners` (in the template's listener order, so "both" stays `[http, https]`), producing `HostnameListeners`. Carry over the managed-cert override and annotations as today. Update every caller to the new 3-arg signature (pass the already-loaded template; `domain_service.Create` and `applyGateway` both have `dt`).

- [ ] **Step 4: Run** `go test ./internal/domainplan/... -count=1` → PASS; migrated-domain goldens unchanged (invariant). `go build ./...` now succeeds (all callers updated).

- [ ] **Step 5: Commit**

```bash
git add internal/domainplan/gateway.go internal/services/domain_service.go internal/services/domain_template_manifests.go internal/domainplan/testdata/
git commit -m "feat(domainplan): resolve domain BoundListeners into Gateway listeners

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 11: OpenAPI + mocks + full suite

**Files:**
- Modify: `docs/openapi/schemas/domain-template.yaml` (replace `tlsMode`/`httpPort`/`httpsPort`/`tlsPolicy` in `DomainTemplate`/`CreateDomainTemplateRequest`/preview schemas with a `listeners` array schema), `docs/openapi/schemas/domain.yaml` (drop the TLS/port fields from `Domain`; add `boundListeners` to `CreateDomainRequest`), `docs/openapi/paths/domain-templates.yaml` (capability param description).
- Regenerate: `cmd/server/openapi.yaml` via `make openapi`; `internal/mocks/*` via `make mocks` (any interface that changed — domain/template services).

- [ ] **Step 1: Edit the OpenAPI source** to match the new request/response shapes (listener array; `boundListeners`; removed TLS/port/enable fields). Add a `TemplateListener` component schema (`name`, `protocol` enum, `port`, `tlsMode` enum, `portRangeMin`, `portRangeMax`).

- [ ] **Step 2: Regenerate + verify**

Run:
```bash
make openapi        # re-bundle into cmd/server/openapi.yaml
make openapi-check  # diff must be clean
make mocks && make mocks-check
```
Expected: `openapi-check` clean, `mocks-check` clean.

- [ ] **Step 3: Full suite**

Run: `go test ./... -count=1`
Expected: all packages ok. Any failing golden on an existing domain fixture = byte-identical invariant violated → fix the builder, do not accept the golden.

- [ ] **Step 4: Commit**

```bash
git add docs/openapi/ cmd/server/openapi.yaml internal/mocks/
git commit -m "feat(api): OpenAPI + mocks for the listener model

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```
