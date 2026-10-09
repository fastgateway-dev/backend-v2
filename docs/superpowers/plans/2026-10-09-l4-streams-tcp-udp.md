# L4 Streams (TCPRoute / UDPRoute) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add port-keyed Layer-4 routing (TCP/UDP) to FastGateway via a new `Stream` resource and Gateway API `TCPRoute`/`UDPRoute`, reusing the existing route pipeline.

**Architecture:** A `Stream` is the L4 twin of `Domain` (owns one K8s `Gateway` + LB, no hostname/TLS). L4 routes are `Route` rows (`protocol` ∈ `tcp`/`udp`, `stream_id` set) whose listener ports project onto the Stream's Gateway; CRDs are emitted as `v1alpha2` `TCPRoute`/`UDPRoute` mirroring the existing GRPCRoute path. The existing "Domain Template" gains `enable_domain`/`enable_stream` capability flags and is reused as the shared GatewayClass+EnvoyProxy infra profile.

**Tech Stack:** Go 1.25 / Gin / GORM / Postgres; `sigs.k8s.io/gateway-api v1.6.1` (`apis/v1alpha2`); Envoy Gateway; mockery; OpenAPI split under `docs/openapi/`; Next.js 16 / TS / Tailwind frontend.

**Spec:** `docs/superpowers/specs/2026-10-09-l4-streams-tcp-udp-design.md` (read it alongside this plan — it carries the design rationale, EG facts, and all decisions).

**Repos / worktrees:** backend in this worktree (`backend-v2-l4-streams`, branch `feat/l4-streams-spec`). Frontend tasks are in a sibling `frontend-v2` worktree the executor must create off `frontend-v2` `main` via `superpowers:using-git-worktrees` before Phase 1's frontend task.

## Global Constraints

- **Gateway API:** `sigs.k8s.io/gateway-api v1.6.1`. `TCPRoute`/`UDPRoute` are `gateway.networking.k8s.io/v1alpha2` (`apis/v1alpha2`); `Gateway` stays `v1`.
- **No TLS anywhere in L4 scope.** No TLSRoute.
- **Backward compatibility is mandatory.** No table renamed; no existing column changes meaning; existing Domains/templates behave identically. `enable_domain` defaults **true**, `enable_stream` defaults **false**.
- **L4 backends are in-cluster Kubernetes Services only.** External FQDN/IP is rejected at one marked seam (EG `Backend` CRD is HTTPRoute/TLSRoute-only).
- **Reuse over reinvention.** Mirror the GRPCRoute path (`internal/routeplan/grpcroute.go`, `internal/kubernetes/grpcroute.go`, `internal/cluster/route.go`) with the `v1alpha2` twist.
- **Naming:** Stream Gateway names are kind-prefixed (`str-<name>`) so they never collide with Domain Gateways. "Domain Template" is relabeled "Gateway Template" in the UI only; table/route/type names stay `domain_templates` / `/domain-templates`.
- **Ports:** listener port is `(protocol, port)`-unique within a Stream, and — when the template merges — across all Gateways on that GatewayClass; HTTP/HTTPS/TLS count as TCP-transport; `tcp:P` and `udp:P` don't collide. Reserve Envoy internal ports (19000/19001), the placeholder port, and (merged+domain) 80/443.
- **Test harness:** integration tests use `requirePostgres(t) *gorm.DB` + `seedProject(t, db)` from `internal/repository/route_repository_test.go` (skip unless `INTEGRATION_DB_URL` set). Run `make test`, `make mocks-check`, `make openapi-check`, and `gofmt -l` before every commit's verify step.
- **Attribution:** commits end with `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`.

## Review Focus

Inputs the spec implies that are most likely to bite; each is pinned by a test in the named task:
1. **Merged-set port collision** — a Stream port equal to a sibling Domain's HTTPS 443 or another Stream's port under `mergeGateways:true` must 409. → Task 19.
2. **TCP vs UDP same number** — `tcp:53` and `udp:53` on one Stream must both succeed (no false collision). → Task 19.
3. **Reserved ports** — Stream port = 80/443 (merged+domain), Envoy internal, or placeholder must be rejected. → Task 20.
4. **L7 fields / external backend on an L4 route** — a `tcp` route carrying a path match, filter, security mode, or FQDN/IP backend must be rejected, not silently ignored. → Task 21.
5. **Exactly-one-owner + enable_stream scoping** — a route with both/neither of `domain_id`/`stream_id`, or a Stream referencing a non-`enable_stream` template, must be rejected. → Task 7 / Task 21.

---

## Execution order (dependency-correct)

The document groups tasks by theme, but **execute in this order** — the data-model tasks must land before their consumers (e.g. the Stream Gateway builder (8) and the L4 CRD builders (14) read the Route-model fields added in Task 13, and the route migration (12) references the `streams` table from Task 5):

1. **Template flags:** 1 → 2 → 3 → 4
2. **Data model:** 5 (streams table) → 6 (Stream model/repo) → 12 (route migration, needs streams table) → 13 (Route model fields)
3. **Streams core:** 8 (streamplan, needs 13) → 7 (StreamService, needs 6) → 9 (deploy, needs 7+8) → 10 (handlers) → 11 (frontend streams)
4. **L4 CRDs/deploy:** 14 (needs 8+13) → 15 → 16 (needs 8+14+15) → 17 → 18 (mocks)
5. **Port collision:** 19 (needs 7+13) → 20
6. **Validation:** 21 → 22
7. **Policy:** 23
8. **Observability/frontend:** 24 → 25 → 26

Each task's **Interfaces → Consumes** names the specific task it depends on; "earlier" means earlier in *this* order, not necessarily in document position.

## File Structure

**Backend — new files**
- `internal/models/stream.go` — `Stream` GORM model.
- `internal/repository/stream_repository.go` — Stream CRUD + `UsedListenerPorts` queries.
- `internal/services/stream_service.go` — Stream business logic (create/update/delete, template scoping, immutability, delete-guard).
- `internal/services/stream_port_collision.go` — `CheckPortCollision` + reserved-port logic.
- `internal/handlers/stream_handler.go` — Stream HTTP handlers.
- `internal/streamplan/gateway.go` — Stream `Gateway` builder (listeners = projection + placeholder).
- `internal/kubernetes/tcproute.go`, `internal/kubernetes/udproute.go` — typed `v1alpha2` CRD builders.
- `internal/routeplan/tcproute.go`, `internal/routeplan/udproute.go` — `BuildTCPRouteConfig` / `BuildUDPRouteConfig`.
- `migrations/NNNNNN_*.{up,down}.sql` — four migrations (template flags, streams table, route columns, indexes).

**Backend — modified files**
- `internal/models/route.go` — `RouteProtocolTCP`/`UDP`, `StreamID`, `ListenerPort`, nullable `DomainID`.
- `internal/models/domain_template.go` — `EnableDomain`/`EnableStream`.
- `internal/kubernetes/gvr.go` / `internal/cluster/route.go` — GVRs + Create/Update/Delete + ReferenceGrant kinds.
- `internal/services/k8s_roles.go` — `RouteApplier` interface + assertion.
- `internal/services/route_deploy_service.go`, `route_assembler_config.go`, `route_yaml.go` — tcp/udp branches.
- `internal/services/route_validation.go`, `route_write_validation.go` — L4 validation.
- `internal/routeplan/input.go` — `GetRouteKind` for tcp/udp.
- `internal/services/domain_template_service.go`, `domain_service.go` — flags + scoping + merged-domain collision.
- `internal/services/metrics_service.go` — L4 PromQL.
- `docs/openapi/**` — streams paths + template fields + route protocol enum.

**Frontend — modified/new files** (in the `frontend-v2` worktree)
- `src/types/index.ts` — `RouteProtocol` union, `Stream`/`CreateStreamInput`, template flags.
- `src/lib/api/streams.ts` (new), `src/lib/api/domainTemplates.ts`, `src/lib/api/routes.ts`.
- `src/app/(authenticated)/.../streams/` — list/create/detail pages + slim L4 route form.
- `src/app/.../domain-templates/create|edit/page.tsx` — capability checkboxes + relabel.
- L4 metrics card component.

---

# PHASE 1 — Gateway Template capability flags

Ships independently: templates gain `enable_domain`/`enable_stream`; existing templates/domains unchanged.

### Task 1: Migration — add template capability flags

**Files:**
- Create: `migrations/<next>_add_template_capability_flags.up.sql`
- Create: `migrations/<next>_add_template_capability_flags.down.sql`

**Interfaces:**
- Produces: `domain_templates.enable_domain BOOLEAN NOT NULL DEFAULT true`, `enable_stream BOOLEAN NOT NULL DEFAULT false`.

- [ ] **Step 1: Find the next migration number.** Run `ls migrations/ | sort | tail -4` and pick the next zero-padded sequence.

- [ ] **Step 2: Write the up migration**
```sql
ALTER TABLE domain_templates
  ADD COLUMN enable_domain BOOLEAN NOT NULL DEFAULT true,
  ADD COLUMN enable_stream BOOLEAN NOT NULL DEFAULT false;
```

- [ ] **Step 3: Write the down migration**
```sql
ALTER TABLE domain_templates
  DROP COLUMN enable_stream,
  DROP COLUMN enable_domain;
```

- [ ] **Step 4: Verify migrations apply** against a scratch DB:
Run: `INTEGRATION_DB_URL set; go run ./cmd/migrate up && go run ./cmd/migrate down 1 && go run ./cmd/migrate up`
Expected: no errors; column present after up.

- [ ] **Step 5: Commit** — `git add migrations/ && git commit -m "feat(l4): migration for template enable_domain/enable_stream flags"`

### Task 2: Model + DTO + "at least one enabled" validation

**Files:**
- Modify: `internal/models/domain_template.go` (struct `DomainTemplate`, around :148-191)
- Modify: `internal/services/domain_template_service.go` (`CreateDomainTemplateInput` :54-82, `UpdateDomainTemplateInput` :85-113, `Create` :116, `Update` :298)
- Test: `internal/services/domain_template_service_test.go`

**Interfaces:**
- Produces: `DomainTemplate.EnableDomain bool`, `DomainTemplate.EnableStream bool`; `CreateDomainTemplateInput.EnableDomain *bool`, `.EnableStream *bool` (pointers so omitted = default); `UpdateDomainTemplateInput.EnableDomain *bool`, `.EnableStream *bool`; a validation error `ErrNoTemplateCapability` when both resolve to false.

- [ ] **Step 1: Write the failing test** (unit, no DB — validate the input-normalization helper):
```go
func TestNormalizeTemplateCapabilities_Defaults(t *testing.T) {
	ed, es, err := services.NormalizeTemplateCapabilities(nil, nil)
	require.NoError(t, err)
	assert.True(t, ed)   // enable_domain defaults true
	assert.False(t, es)  // enable_stream defaults false
}

func TestNormalizeTemplateCapabilities_BothFalse_Error(t *testing.T) {
	f := false
	_, _, err := services.NormalizeTemplateCapabilities(&f, &f)
	assert.ErrorIs(t, err, services.ErrNoTemplateCapability)
}

func TestNormalizeTemplateCapabilities_StreamOnly(t *testing.T) {
	f, tru := false, true
	ed, es, err := services.NormalizeTemplateCapabilities(&f, &tru)
	require.NoError(t, err)
	assert.False(t, ed)
	assert.True(t, es)
}
```

- [ ] **Step 2: Run to verify it fails**
Run: `go test ./internal/services/ -run TestNormalizeTemplateCapabilities -v`
Expected: FAIL (undefined `NormalizeTemplateCapabilities` / `ErrNoTemplateCapability`).

- [ ] **Step 3: Implement.** Add fields to the model:
```go
// in DomainTemplate struct:
EnableDomain bool `gorm:"column:enable_domain;not null;default:true" json:"enableDomain"`
EnableStream bool `gorm:"column:enable_stream;not null;default:false" json:"enableStream"`
```
Add to `CreateDomainTemplateInput` and `UpdateDomainTemplateInput`:
```go
EnableDomain *bool `json:"enableDomain,omitempty"`
EnableStream *bool `json:"enableStream,omitempty"`
```
Add the helper + sentinel in `domain_template_service.go`:
```go
var ErrNoTemplateCapability = errors.New("template must be enabled for at least one of domain or stream")

// NormalizeTemplateCapabilities resolves optional flags to concrete values
// (enable_domain default true, enable_stream default false) and rejects both-false.
func NormalizeTemplateCapabilities(enableDomain, enableStream *bool) (ed, es bool, err error) {
	ed = true
	if enableDomain != nil {
		ed = *enableDomain
	}
	if enableStream != nil {
		es = *enableStream
	}
	if !ed && !es {
		return false, false, ErrNoTemplateCapability
	}
	return ed, es, nil
}
```
Wire into `Create` (set `dt.EnableDomain/EnableStream` from the normalized values) and `Update` (when either pointer is non-nil, re-normalize using current values as the base and reject both-false).

- [ ] **Step 4: Run to verify it passes**
Run: `go test ./internal/services/ -run TestNormalizeTemplateCapabilities -v`
Expected: PASS.

- [ ] **Step 5: Map the handler 4xx.** In `internal/handlers/domain_template_handler.go` Create/Update, map `ErrNoTemplateCapability` → `http.StatusBadRequest` with `{"error": err.Error()}`.

- [ ] **Step 6: Verify + commit**
Run: `gofmt -l internal/ && go build ./... && go test ./internal/services/ -run 'TestNormalizeTemplateCapabilities|TestDomainTemplate' -v`
```bash
git add internal/models/domain_template.go internal/services/domain_template_service.go internal/handlers/domain_template_handler.go internal/services/domain_template_service_test.go
git commit -m "feat(l4): template capability flags (enable_domain/enable_stream) with validation"
```

### Task 3: Template scoping in list/get (filter for pickers)

**Files:**
- Modify: `internal/services/domain_template_service.go` (List)
- Modify: `internal/handlers/domain_template_handler.go` (List — accept `?capability=domain|stream`)
- Modify: `docs/openapi/paths/domain-templates.yaml`
- Test: `internal/services/domain_template_service_test.go`

**Interfaces:**
- Consumes: `DomainTemplate.EnableDomain/EnableStream` (Task 2).
- Produces: `DomainTemplateService.List(projectID uuid.UUID, capability string) ([]models.DomainTemplate, error)` where `capability` ∈ `""|"domain"|"stream"` filters to templates with that flag true.

- [ ] **Step 1: Write the failing test** (integration):
```go
func TestDomainTemplateService_List_CapabilityFilter(t *testing.T) {
	db := requirePostgres(t)
	projectID, _, _, userID := seedProject(t, db)
	// insert: tmplA enable_domain only, tmplB enable_stream only, tmplC both
	// (use the repository/service create path or direct inserts)
	svc := services.NewDomainTemplateService(/* repos */)
	streamTmpls, err := svc.List(projectID, "stream")
	require.NoError(t, err)
	names := templateNames(streamTmpls)
	assert.Contains(t, names, "tmplB")
	assert.Contains(t, names, "tmplC")
	assert.NotContains(t, names, "tmplA")
	_ = userID
}
```

- [ ] **Step 2: Run to verify it fails**
Run: `INTEGRATION_DB_URL=... go test ./internal/services/ -run TestDomainTemplateService_List_CapabilityFilter -v`
Expected: FAIL (List signature mismatch).

- [ ] **Step 3: Implement.** Extend `List` to accept `capability string`; when `"domain"` add `WHERE enable_domain = true`, when `"stream"` add `WHERE enable_stream = true`, else no filter. Update the handler to read `c.Query("capability")` and pass it. Add the `capability` query param to the OpenAPI list path.

- [ ] **Step 4: Run to verify it passes** (same command) — Expected: PASS.

- [ ] **Step 5: Verify + commit**
Run: `make openapi-check && gofmt -l internal/ && go build ./...`
```bash
git add internal/services/domain_template_service.go internal/handlers/domain_template_handler.go docs/openapi/ internal/services/domain_template_service_test.go
git commit -m "feat(l4): template list capability filter (domain|stream)"
```

### Task 4: Frontend — Gateway Template capability checkboxes + relabel

**Files (frontend-v2 worktree):**
- Modify: `src/types/index.ts` (`DomainTemplate`, `CreateDomainTemplateInput`)
- Modify: `src/app/(authenticated)/.../domain-templates/create/page.tsx`
- Modify: `src/app/(authenticated)/.../domain-templates/[domainTemplateId]/edit/page.tsx`
- Modify: `src/lib/api/domainTemplates.ts` (list accepts capability)

**Interfaces:**
- Consumes: backend `enableDomain`/`enableStream` fields + `?capability=` filter (Tasks 2-3).

- [ ] **Step 1: Add the types.** In `src/types/index.ts` add `enableDomain: boolean; enableStream: boolean;` to `DomainTemplate`, and `enableDomain?: boolean; enableStream?: boolean;` to `CreateDomainTemplateInput`.

- [ ] **Step 2: Add the checkboxes.** In the template create form, add two checkboxes near the top: "Enable for Domains" (default checked) and "Enable for Streams" (default unchecked). Add client validation: at least one checked (disable submit + inline error otherwise).

- [ ] **Step 3: Gate the HTTP/TLS section.** Wrap the TLS mode + HTTP/HTTPS port + TLS policy fields so they render only when `enableDomain` is checked.

- [ ] **Step 4: Relabel.** Change user-visible "Domain Template" → "Gateway Template" in the page titles/headings of the create, edit, list, and detail pages. Do **not** change routes, API paths, or variable names.

- [ ] **Step 5: Edit page read-only badge.** Add `enableDomain`/`enableStream` to the read-only "cannot change after creation" badges (these remain editable per backend Update DTO — show as editable checkboxes on edit, matching backend Update accepting the flags).

- [ ] **Step 6: Verify + commit**
Run: `npm run build` (frontend worktree)
```bash
git add src/types/index.ts src/lib/api/domainTemplates.ts "src/app" && git commit -m "feat(l4): Gateway Template capability checkboxes + relabel"
```

---

# PHASE 2 — Streams resource + Gateway lifecycle

### Task 5: Migration — `streams` table

**Files:**
- Create: `migrations/<next>_create_streams.up.sql` / `.down.sql`

**Interfaces:**
- Produces: table `streams` with columns `id, project_id, name, namespace, gateway_template_id, k8s_gateway_name, k8s_gateway_class, status, status_message, created_by, created_at, updated_at`; unique index `(project_id, name)`.

- [ ] **Step 1: Write the up migration**
```sql
CREATE TABLE streams (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name text NOT NULL,
  namespace text NOT NULL,
  gateway_template_id uuid NOT NULL REFERENCES domain_templates(id),
  k8s_gateway_name text NOT NULL,
  k8s_gateway_class text NOT NULL,
  status text NOT NULL DEFAULT 'pending',
  status_message text NOT NULL DEFAULT '',
  created_by uuid,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX idx_stream_project_name ON streams (project_id, name);
```

- [ ] **Step 2: Write the down migration** — `DROP TABLE streams;`

- [ ] **Step 3: Verify up/down/up** (as Task 1 Step 4). Expected: clean.

- [ ] **Step 4: Commit** — `git commit -m "feat(l4): migration for streams table"`

### Task 6: `Stream` model + repository

**Files:**
- Create: `internal/models/stream.go`
- Create: `internal/repository/stream_repository.go`
- Test: `internal/repository/stream_repository_test.go`

**Interfaces:**
- Produces:
```go
type Stream struct {
	ID                uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	ProjectID         uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_stream_project_name" json:"projectId"`
	Name              string    `gorm:"not null;uniqueIndex:idx_stream_project_name" json:"name"`
	Namespace         string    `gorm:"not null" json:"namespace"`
	GatewayTemplateID uuid.UUID `gorm:"type:uuid;not null" json:"gatewayTemplateId"`
	K8sGatewayName    string    `json:"k8sGatewayName"`
	K8sGatewayClass   string    `json:"k8sGatewayClass"`
	Status            string    `gorm:"not null;default:'pending'" json:"status"`
	StatusMessage     string    `json:"statusMessage"`
	CreatedBy         *uuid.UUID `gorm:"type:uuid" json:"createdBy,omitempty"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
}
func (Stream) TableName() string { return "streams" }
```
- `StreamRepository` interface: `Create(*Stream) error`, `GetByID(id uuid.UUID) (*Stream, error)`, `ListByProjectID(projectID uuid.UUID) ([]Stream, error)`, `Update(*Stream) error`, `Delete(id uuid.UUID) error`, `CountByGatewayTemplateID(templateID uuid.UUID) (int64, error)`.

- [ ] **Step 1: Write the failing test** (integration, CRUD round-trip):
```go
func TestStreamRepository_CRUD(t *testing.T) {
	db := requirePostgres(t)
	projectID, _, _, _ := seedProject(t, db)
	tmplID := insertTemplate(t, db, projectID, /*enableStream*/ true)
	repo := repository.NewStreamRepository(db)
	s := &models.Stream{ProjectID: projectID, Name: "db-gw", Namespace: "fastgateway-system",
		GatewayTemplateID: tmplID, K8sGatewayName: "str-db-gw", K8sGatewayClass: "public-lb"}
	require.NoError(t, repo.Create(s))
	got, err := repo.GetByID(s.ID)
	require.NoError(t, err)
	assert.Equal(t, "db-gw", got.Name)
	list, err := repo.ListByProjectID(projectID)
	require.NoError(t, err)
	assert.Len(t, list, 1)
	require.NoError(t, repo.Delete(s.ID))
}
```

- [ ] **Step 2: Run to verify it fails** — `INTEGRATION_DB_URL=... go test ./internal/repository/ -run TestStreamRepository_CRUD -v` → FAIL.

- [ ] **Step 3: Implement** the model and a GORM-backed repository following `internal/repository/domain_repository.go` patterns.

- [ ] **Step 4: Run to verify it passes** — same command → PASS.

- [ ] **Step 5: Register automigration/model** wherever `Domain` is registered (grep for `&models.Domain{}` in `internal/database/` and add `&models.Stream{}`).

- [ ] **Step 6: Verify + commit**
Run: `gofmt -l internal/ && go build ./...`
```bash
git add internal/models/stream.go internal/repository/stream_repository.go internal/repository/stream_repository_test.go internal/database/
git commit -m "feat(l4): Stream model + repository"
```

### Task 7: `StreamService` — create/update/delete with scoping, immutability, delete-guard

**Files:**
- Create: `internal/services/stream_service.go`
- Test: `internal/services/stream_service_test.go`

**Interfaces:**
- Consumes: `StreamRepository` (Task 6), `DomainTemplateRepository`, `RouteRepository` (to count a Stream's routes).
- Produces:
```go
type CreateStreamInput struct {
	Name, Namespace string
	GatewayTemplateID uuid.UUID
}
type UpdateStreamInput struct { Name *string } // template immutable; name only
var (
	ErrTemplateNotStreamEnabled = errors.New("gateway template is not enabled for streams")
	ErrStreamHasRoutes          = errors.New("stream has active routes; remove them before deleting")
	ErrStreamTemplateImmutable  = errors.New("stream gateway template cannot be changed after creation")
)
func (s *StreamService) Create(projectID uuid.UUID, in CreateStreamInput, user *models.User) (*models.Stream, error)
func (s *StreamService) Update(id uuid.UUID, in UpdateStreamInput) (*models.Stream, error)
func (s *StreamService) Delete(id uuid.UUID) error
func StreamGatewayName(name string) string // returns "str-" + sanitized name
```

- [ ] **Step 1: Write the failing tests** (unit where possible; the gateway-name one is pure):
```go
func TestStreamGatewayName_KindPrefixed(t *testing.T) {
	assert.Equal(t, "str-foo", services.StreamGatewayName("foo"))
	assert.NotEqual(t, services.StreamGatewayName("foo"), /*domain scheme*/ "foo") // never collides with a Domain named foo
}
func TestStreamService_Create_RejectsNonStreamTemplate(t *testing.T) {
	// template with enable_stream=false → Create returns ErrTemplateNotStreamEnabled
}
func TestStreamService_Delete_BlockedWithRoutes(t *testing.T) {
	// stream with ≥1 route → Delete returns ErrStreamHasRoutes
}
func TestStreamService_Update_TemplateImmutable(t *testing.T) {
	// UpdateStreamInput has no template field; assert the service never re-homes the template
	// (compile-time: UpdateStreamInput exposes only Name)
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/services/ -run TestStream -v` → FAIL.

- [ ] **Step 3: Implement.** `Create`: load the template, reject if `!EnableStream` (`ErrTemplateNotStreamEnabled`); copy `K8sGatewayClass` from `template.K8sGatewayClassName`; set `K8sGatewayName = StreamGatewayName(in.Name)`; persist. `Delete`: if `routeRepo.CountByStreamID(id) > 0` return `ErrStreamHasRoutes`. `Update`: only `Name`. `StreamGatewayName` = `"str-" + kubeSanitize(name)`.

- [ ] **Step 4: Run to verify it passes** — PASS.

- [ ] **Step 5: Add `CountByStreamID`** to `RouteRepository` (mirror an existing count query) with its own small test.

- [ ] **Step 6: Verify + commit**
Run: `make mocks && gofmt -l internal/ && go build ./... && go test ./internal/services/ -run TestStream -v`
```bash
git add internal/services/stream_service.go internal/services/stream_service_test.go internal/repository/ internal/mocks/
git commit -m "feat(l4): StreamService create/update/delete with template scoping, immutability, delete-guard"
```

### Task 8: `streamplan` — Stream Gateway builder (projection + placeholder)

**Files:**
- Create: `internal/streamplan/gateway.go`
- Test: `internal/streamplan/gateway_test.go`

**Interfaces:**
- Consumes: `models.Stream`, a slice of active L4 routes (`[]models.Route`), and the constant `streamplan.PlaceholderPort` (a reserved port, e.g. `60000`).
- Produces:
```go
type StreamListener struct { Name, Protocol string; Port int } // Protocol "TCP"|"UDP"
func BuildStreamGatewayConfig(stream models.Stream, routes []models.Route) kubernetes.GatewayConfig
func ListenerName(protocol string, port int) string // "l4-tcp-5432"
const PlaceholderPort = 60000
```

- [ ] **Step 1: Write the failing tests**:
```go
func TestBuildStreamGatewayConfig_EmptyUsesPlaceholder(t *testing.T) {
	cfg := streamplan.BuildStreamGatewayConfig(models.Stream{Name: "s", K8sGatewayName: "str-s", K8sGatewayClass: "pub"}, nil)
	require.Len(t, cfg.Listeners, 1)
	assert.Equal(t, "TCP", cfg.Listeners[0].Protocol)
	assert.Equal(t, streamplan.PlaceholderPort, cfg.Listeners[0].Port)
}
func TestBuildStreamGatewayConfig_ProjectsRoutes(t *testing.T) {
	routes := []models.Route{
		{Protocol: models.RouteProtocolTCP, Config: models.RouteConfig{ListenerPort: 5432}},
		{Protocol: models.RouteProtocolUDP, Config: models.RouteConfig{ListenerPort: 53}},
	}
	cfg := streamplan.BuildStreamGatewayConfig(models.Stream{K8sGatewayName: "str-s", K8sGatewayClass: "pub"}, routes)
	ports := listenerSet(cfg.Listeners) // helper → map[string]int by name
	assert.Equal(t, 5432, ports["l4-tcp-5432"])
	assert.Equal(t, 53, ports["l4-udp-53"])
	assert.NotContains(t, ports, "placeholder") // placeholder dropped once real routes exist
}
func TestListenerName(t *testing.T) {
	assert.Equal(t, "l4-tcp-5432", streamplan.ListenerName("TCP", 5432))
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/streamplan/ -v` → FAIL.

- [ ] **Step 3: Implement.** Build a `kubernetes.GatewayConfig` referencing `stream.K8sGatewayClass`, name `stream.K8sGatewayName`. If `routes` empty, emit one placeholder `{Name:"l4-placeholder", Protocol:"TCP", Port:PlaceholderPort}`; else one listener per route `{Name: ListenerName(proto,port), Protocol: upper(proto), Port: route.Config.ListenerPort}`. Reuse/extend `kubernetes.GatewayConfig` + `BuildGatewayObject` to accept L4 listeners (add a `Listeners []StreamListener` path in `internal/kubernetes/gateway.go` that emits `protocol: TCP|UDP`, `port`, `name`, and `allowedRoutes.kinds: [TCPRoute|UDPRoute]`). No hostname on L4 listeners.

- [ ] **Step 4: Run to verify it passes** — PASS.

- [ ] **Step 5: Verify + commit**
Run: `gofmt -l internal/ && go build ./...`
```bash
git add internal/streamplan/ internal/kubernetes/gateway.go && git commit -m "feat(l4): streamplan Gateway builder (route projection + placeholder listener)"
```

### Task 9: Stream deploy wiring + `RouteApplier`/Gateway apply reuse

**Files:**
- Modify: `internal/services/stream_service.go` (deploy on create: apply the Gateway)
- Modify: `internal/services/k8s_roles.go` (confirm `CreateGateway`/`UpdateGateway` are on the applier interface used by streams)
- Test: `internal/services/stream_service_test.go` (with a mocked applier)

**Interfaces:**
- Consumes: `streamplan.BuildStreamGatewayConfig` (Task 8), `kubernetes.BuildGatewayObject`, the cluster applier's `CreateGateway(ctx, cluster, obj)`.
- Produces: `StreamService.Deploy(ctx, stream) error` — builds the Gateway (placeholder when no routes) and applies it; sets `Status=active` on success.

- [ ] **Step 1: Write the failing test** — on `Create`, the mocked applier's `CreateGateway` is called once with a Gateway whose single listener is the placeholder; `Status` becomes `active`.

- [ ] **Step 2: Run to verify it fails** → FAIL.

- [ ] **Step 3: Implement** `Deploy`; call it at the end of `Create`. Use the project's cluster-client resolution the same way `DomainService` does (grep `domain_service.go` for how it obtains the applier/cluster).

- [ ] **Step 4: Run to verify it passes** → PASS.

- [ ] **Step 5: Verify + commit**
Run: `make mocks-check && gofmt -l internal/ && go build ./...`
```bash
git add internal/services/ internal/mocks/ && git commit -m "feat(l4): eager Stream Gateway deploy on create"
```

### Task 10: Stream HTTP handlers + endpoints + OpenAPI

**Files:**
- Create: `internal/handlers/stream_handler.go`
- Modify: `cmd/server/main.go` (register routes under `/projects/:projectId/streams`)
- Create: `docs/openapi/paths/streams.yaml`; Modify: `docs/openapi/openapi.yaml` (add paths)
- Test: `internal/handlers/stream_handler_test.go`

**Interfaces:**
- Consumes: `StreamService` (Tasks 7, 9).
- Produces: `GET/POST /projects/:projectId/streams`, `GET/PATCH/DELETE /projects/:projectId/streams/:streamId`.

- [ ] **Step 1: Write the failing handler test** — POST creates (201), DELETE with routes → 409 (`ErrStreamHasRoutes`), POST referencing non-stream template → 400 (`ErrTemplateNotStreamEnabled`). Mirror `domain_handler_test.go` structure.

- [ ] **Step 2: Run to verify it fails** → FAIL.

- [ ] **Step 3: Implement** the handler (parse input, call service, map sentinels: `ErrTemplateNotStreamEnabled`→400, `ErrStreamHasRoutes`→409), register routes with the same permission middleware Domains use, add OpenAPI paths + schemas.

- [ ] **Step 4: Run to verify it passes** → PASS.

- [ ] **Step 5: Verify + commit**
Run: `make openapi-check && gofmt -l internal/ && go build ./... && go test ./internal/handlers/ -run TestStream -v`
```bash
git add internal/handlers/ cmd/server/main.go docs/openapi/ && git commit -m "feat(l4): Stream handlers + endpoints + OpenAPI"
```

### Task 11: Frontend — Streams section (list / create / detail)

**Files (frontend-v2 worktree):**
- Create: `src/lib/api/streams.ts`; Modify: `src/types/index.ts` (`Stream`, `CreateStreamInput`)
- Create: `src/app/(authenticated)/.../streams/page.tsx` (list), `.../streams/create/page.tsx`, `.../streams/[streamId]/page.tsx` (detail)
- Modify: sidebar/nav to add "Streams"

**Interfaces:**
- Consumes: `/projects/:id/streams` endpoints (Task 10) + `?capability=stream` template list (Task 3).

- [ ] **Step 1:** Add `Stream`/`CreateStreamInput` types + `streamsApi` client (list/get/create/update/delete) mirroring `domains.ts`.
- [ ] **Step 2:** List page (table of streams + status + LB address) mirroring the domains list.
- [ ] **Step 3:** Create page — Name, Namespace, Template picker fetched with `capability=stream`. No hostname/TLS fields.
- [ ] **Step 4:** Detail page — LB address, **in-use ports list**, routes table (empty for now), delete button (surfacing the 409 "remove routes first" message).
- [ ] **Step 5:** Add "Streams" to the project nav (sibling of Domains).
- [ ] **Step 6: Verify + commit** — `npm run build`; `git commit -m "feat(l4): Streams section (list/create/detail)"`

---

# PHASE 3 — Route model changes + L4 CRDs

### Task 12: Migration — route L4 columns + constraints

**Files:**
- Create: `migrations/<next>_add_route_l4_columns.up.sql` / `.down.sql`

**Interfaces:**
- Produces: `routes.stream_id uuid NULL REFERENCES streams(id)`, `routes.domain_id` made nullable, `routes.listener_port int NULL`, CHECK exactly-one-owner, partial unique index.

- [ ] **Step 1: Write the up migration**
```sql
ALTER TABLE routes ADD COLUMN stream_id uuid REFERENCES streams(id) ON DELETE CASCADE;
ALTER TABLE routes ADD COLUMN listener_port integer;
ALTER TABLE routes ALTER COLUMN domain_id DROP NOT NULL;
ALTER TABLE routes ADD CONSTRAINT chk_route_single_owner
  CHECK ((domain_id IS NOT NULL) <> (stream_id IS NOT NULL));
CREATE UNIQUE INDEX idx_route_stream_proto_port
  ON routes (stream_id, protocol, listener_port) WHERE stream_id IS NOT NULL;
```

- [ ] **Step 2: Write the down migration** — drop index, drop constraint, drop columns, re-add `NOT NULL` on `domain_id` (guard: only if no stream rows exist).

- [ ] **Step 3: Verify up/down/up** against scratch DB with an existing HTTP route row present (confirms the CHECK is satisfied by existing `domain_id`-only rows). Expected: clean.

- [ ] **Step 4: Commit** — `git commit -m "feat(l4): migration for route L4 columns + single-owner CHECK + port index"`

### Task 13: `Route` model — protocols, StreamID, ListenerPort

**Files:**
- Modify: `internal/models/route.go` (`RouteProtocol` consts ~:106-112, `Route` struct ~:135-162, `RouteConfig` ~:190-206)
- Test: `internal/models/route_test.go`

**Interfaces:**
- Produces: `RouteProtocolTCP RouteProtocol = "tcp"`, `RouteProtocolUDP = "udp"`; `Route.StreamID *uuid.UUID`; `Route.DomainID *uuid.UUID` (now pointer/nullable); `RouteConfig.ListenerPort int` (`json:"listenerPort,omitempty"`); helper `func (r Route) IsL4() bool { return r.Protocol == RouteProtocolTCP || r.Protocol == RouteProtocolUDP }`; `func (r Route) Transport() string` → `"TCP"`/`"UDP"`.

- [ ] **Step 1: Write the failing test**
```go
func TestRoute_IsL4_Transport(t *testing.T) {
	assert.True(t, (models.Route{Protocol: models.RouteProtocolTCP}).IsL4())
	assert.False(t, (models.Route{Protocol: models.RouteProtocolHTTP}).IsL4())
	assert.Equal(t, "UDP", (models.Route{Protocol: models.RouteProtocolUDP}).Transport())
}
```

- [ ] **Step 2: Run to verify it fails** → FAIL.

- [ ] **Step 3: Implement.** Add the consts, `StreamID *uuid.UUID` column (`gorm:"type:uuid"`), change `DomainID` to `*uuid.UUID` and fix all compile errors where `route.DomainID` is used as a value (grep `\.DomainID` across `internal/` — HTTP paths must guard nil or dereference since HTTP routes always have it). Add `ListenerPort` to `RouteConfig`. Add `IsL4()`/`Transport()`.

- [ ] **Step 4: Run to verify it passes + whole build**
Run: `go build ./... && go test ./internal/models/ -run TestRoute -v`
Expected: PASS (fix every `DomainID` deref the compiler flags).

- [ ] **Step 5: Verify + commit**
Run: `gofmt -l internal/ && go build ./...`
```bash
git add internal/models/route.go internal/models/route_test.go internal/ && git commit -m "feat(l4): Route model tcp/udp protocols, StreamID, ListenerPort, nullable DomainID"
```

### Task 14: `v1alpha2` GVRs + typed TCP/UDP route builders

**Files:**
- Modify: `internal/kubernetes/gvr.go` (add `TCPRouteGVR`, `UDPRouteGVR`) — or the `cluster/route.go` helper pattern (`getGRPCRouteGVR` :17-24) if GVRs live there.
- Create: `internal/kubernetes/tcproute.go`, `internal/kubernetes/udproute.go`
- Create: `internal/routeplan/tcproute.go`, `internal/routeplan/udproute.go`
- Test: `internal/kubernetes/tcproute_test.go`, `internal/routeplan/tcproute_test.go` (+ udp)

**Interfaces:**
- Consumes: `models.Route` (Task 13), `models.Stream`, `streamplan.ListenerName` (Task 8).
- Produces:
```go
// kubernetes
type TCPRouteConfig struct { Name, Namespace, GatewayName, SectionName string; Backends []L4Backend; Labels map[string]string }
type L4Backend struct { Service, Namespace string; Port, Weight int }
func BuildTCPRouteObject(cfg TCPRouteConfig) *gatewayv1alpha2.TCPRoute
func BuildUDPRouteObject(cfg UDPRouteConfig) *gatewayv1alpha2.UDPRoute
var TCPRouteGVR, UDPRouteGVR schema.GroupVersionResource // v1alpha2, resources tcproutes/udproutes
// routeplan
func BuildTCPRouteConfig(route models.Route, stream models.Stream) kubernetes.TCPRouteConfig
func BuildUDPRouteConfig(route models.Route, stream models.Stream) kubernetes.UDPRouteConfig
```

- [ ] **Step 1: Write the failing tests**
```go
func TestBuildTCPRouteObject_ParentRefUsesSectionName(t *testing.T) {
	obj := kubernetes.BuildTCPRouteObject(kubernetes.TCPRouteConfig{
		Name: "r", Namespace: "ns", GatewayName: "str-s", SectionName: "l4-tcp-5432",
		Backends: []kubernetes.L4Backend{{Service: "pg", Namespace: "ns", Port: 5432, Weight: 100}},
	})
	assert.Equal(t, "gateway.networking.k8s.io/v1alpha2", obj.APIVersion)
	assert.Equal(t, "TCPRoute", obj.Kind)
	require.Len(t, obj.Spec.ParentRefs, 1)
	assert.Equal(t, gatewayv1alpha2.SectionName("l4-tcp-5432"), *obj.Spec.ParentRefs[0].SectionName)
	require.Len(t, obj.Spec.Rules, 1)
	assert.Equal(t, int32(100), *obj.Spec.Rules[0].BackendRefs[0].Weight)
}
func TestBuildTCPRouteConfig_WeightedBackends(t *testing.T) {
	// route with two K8s-service backends weights 90/10 → two L4Backend entries
}
```

- [ ] **Step 2: Run to verify it fails** → FAIL.

- [ ] **Step 3: Implement** by mirroring `internal/kubernetes/grpcroute.go` (`BuildGRPCRouteObject`) and `internal/routeplan/grpcroute.go` (`BuildGRPCRouteConfig`) with these deltas: import `gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"`; `TypeMeta` = `v1alpha2`/`TCPRoute`|`UDPRoute`; set `ParentRefs[0].SectionName = &sectionName` (not just Name); `Rules` has only `BackendRefs` (no matches); backends are **K8s Service only** (no `Backend` CRD group — group/kind default to core Service). Add the GVRs. The config's `SectionName` comes from `streamplan.ListenerName(route.Transport(), route.Config.ListenerPort)`.

- [ ] **Step 4: Run to verify it passes** → PASS.

- [ ] **Step 5: Verify + commit**
Run: `gofmt -l internal/ && go build ./... && go test ./internal/kubernetes/ ./internal/routeplan/ -run 'TCP|UDP' -v`
```bash
git add internal/kubernetes/ internal/routeplan/ && git commit -m "feat(l4): v1alpha2 TCPRoute/UDPRoute typed builders + routeplan + GVRs"
```

### Task 15: Cluster apply methods + `RouteApplier` + ReferenceGrant kinds

**Files:**
- Modify: `internal/cluster/route.go` (add `Create/Update/DeleteTCPRoute` + `...UDPRoute`, mirror `CreateGRPCRoute` :131-212; extend ReferenceGrant kinds list :240-246 with `TCPRoute`,`UDPRoute`)
- Modify: `internal/services/k8s_roles.go` (`RouteApplier` interface :46-57 + assertion :251)
- Test: `internal/cluster/route_test.go` (if a fake dynamic client is used there) or rely on service-level mock tests.

**Interfaces:**
- Produces: `RouteApplier` gains `CreateTCPRoute/UpdateTCPRoute/DeleteTCPRoute` and `...UDPRoute` with the same `(ctx, cluster, obj)` shape as the GRPC trio.

- [ ] **Step 1: Write the failing test** — `var _ RouteApplier = (*cluster.Client)(nil)` must still compile after adding the 6 methods to the interface; add a test asserting `DeleteTCPRoute` on a fake client issues a delete on `TCPRouteGVR`.

- [ ] **Step 2: Run to verify it fails** → FAIL (interface not satisfied until methods added).

- [ ] **Step 3: Implement** the six methods mirroring the GRPC trio (unstructured conversion via `runtime.DefaultUnstructuredConverter.ToUnstructured`, Create-falls-back-to-Update on `IsAlreadyExists`, preserve `resourceVersion`+`uid` on update). Add `TCPRoute`/`UDPRoute` to the ReferenceGrant from-kinds list.

- [ ] **Step 4: Run to verify it passes** → PASS.

- [ ] **Step 5: Verify + commit**
Run: `make mocks && gofmt -l internal/ && go build ./...`
```bash
git add internal/cluster/route.go internal/services/k8s_roles.go internal/mocks/ && git commit -m "feat(l4): cluster TCP/UDP route apply + RouteApplier + ReferenceGrant kinds"
```

### Task 16: Deploy branches — recompute Stream Gateway + apply route

**Files:**
- Modify: `internal/services/route_deploy_service.go` (protocol branches at :102-114, :174-186, :253-263)
- Modify: `internal/services/route_assembler_config.go` (add `buildTCPRouteConfig`/`buildUDPRouteConfig` delegations, mirror `buildGRPCRouteConfig` :305-307)
- Test: `internal/services/route_deploy_l4_test.go`

**Interfaces:**
- Consumes: Tasks 8, 14, 15; `routeRepo.ListActiveByStreamID(streamID)`.
- Produces: on create/update/delete of a tcp/udp route, the deploy (a) recomputes the Stream's full listener set from active routes and applies the Gateway, then (b) applies/deletes the route CRD; status → `active` on success.

- [ ] **Step 1: Write the failing test** (mocked applier) — deploying a second TCP route on a Stream calls `CreateGateway` with a Gateway containing **both** listeners (full recompute), then `CreateTCPRoute` for the new route.

- [ ] **Step 2: Run to verify it fails** → FAIL.

- [ ] **Step 3: Implement.** In each approval-action branch add `if route.IsL4() { ... }` before the HTTP/GRPC if-else: fetch active L4 routes for `route.StreamID` via `ListActiveByStreamID`, then — on a **create/update** deploy, add the current route to that set (it is about to be active); on a **delete** deploy, exclude the current route — pass the set to `streamplan.BuildStreamGatewayConfig`, apply the Gateway, then apply (create/update) or delete the `TCPRoute`/`UDPRoute`. Add `ListActiveByStreamID(streamID uuid.UUID) ([]models.Route, error)` to the route repository.

- [ ] **Step 4: Run to verify it passes** → PASS.

- [ ] **Step 5: Verify + commit**
Run: `make mocks-check && gofmt -l internal/ && go build ./... && go test ./internal/services/ -run L4 -v`
```bash
git add internal/services/ internal/repository/ internal/mocks/ && git commit -m "feat(l4): route deploy recomputes Stream Gateway + applies TCP/UDP route"
```

### Task 17: YAML preview + assembler for L4

**Files:**
- Modify: `internal/services/route_yaml.go` (protocol branch :241-252 — add tcp/udp → `BuildTCPRouteObject`/`BuildUDPRouteObject` + the Stream Gateway listener diff)
- Test: `internal/services/route_yaml_test.go`

- [ ] **Step 1: Write the failing test** — YAML preview of a tcp route contains `kind: TCPRoute`, `apiVersion: gateway.networking.k8s.io/v1alpha2`, and `sectionName: l4-tcp-5432`.
- [ ] **Step 2: Run to verify it fails** → FAIL.
- [ ] **Step 3: Implement** the branch.
- [ ] **Step 4: Run to verify it passes** → PASS.
- [ ] **Step 5: Verify + commit** — `gofmt -l internal/ && go build ./...`; `git commit -m "feat(l4): YAML preview for TCP/UDP routes"`

### Task 18: Mocks + build/test gate

- [ ] **Step 1:** Run `make mocks` and commit any regenerated `internal/mocks/`.
- [ ] **Step 2:** Run the full gate: `make mocks-check && make openapi-check && gofmt -l internal/ && go build ./... && go test ./...` (integration tests skip without `INTEGRATION_DB_URL`). Expected: green.
- [ ] **Step 3: Commit** — `git commit -m "chore(l4): regenerate mocks"` (if any changed).

---

# PHASE 4 — Port collision

### Task 19: `CheckPortCollision` — merge-aware

**Files:**
- Create: `internal/services/stream_port_collision.go`
- Modify: `internal/repository/stream_repository.go` (+ `internal/repository/domain_repository.go`) — add `UsedL4Ports(templateID uuid.UUID) ([]PortUse, error)` and within-stream `UsedPortsByStream`.
- Test: `internal/services/stream_port_collision_test.go`

**Interfaces:**
- Produces:
```go
type PortUse struct { Transport string; Port int } // Transport "TCP"|"UDP"
var ErrPortCollision = errors.New("listener port is already in use")
// CheckPortCollision validates (transport, port) for an L4 route, scoped by the
// template's mergeGateways: within the Stream when not merged, across the
// template's Gateways (Domains' HTTP/HTTPS ports as TCP + all Streams' L4 ports)
// when merged. excludeRouteID skips the route being updated.
func (s *StreamService) CheckPortCollision(streamID uuid.UUID, transport string, port int, excludeRouteID *uuid.UUID) error
```

- [ ] **Step 1: Write the failing tests** (integration):
```go
func TestCheckPortCollision_SamePortSameProto_WithinStream(t *testing.T) {
	// stream has tcp:5432; adding another tcp:5432 → ErrPortCollision
}
func TestCheckPortCollision_TCPvsUDP_SameNumber_OK(t *testing.T) {
	// stream has tcp:53; adding udp:53 → no error
}
func TestCheckPortCollision_Merged_HitsDomainHTTPSPort(t *testing.T) {
	// template mergeGateways=true + a Domain on it using HTTPS 443;
	// adding a stream tcp:443 → ErrPortCollision (HTTP/HTTPS count as TCP)
}
func TestCheckPortCollision_NotMerged_OtherStreamSamePort_OK(t *testing.T) {
	// mergeGateways=false: two different streams both tcp:5432 → no error
}
```

- [ ] **Step 2: Run to verify it fails** → FAIL.

- [ ] **Step 3: Implement.** Load the Stream → its template → `mergeGateways`. Not merged: query `UsedPortsByStream(streamID, excludeRouteID)`. Merged: union of (a) all Streams' L4 ports on that template and (b) all Domains' `HTTPPort`/`HTTPSPort` on that template counted as `TCP`. Collision iff an existing `PortUse` has the same `(transport, port)` (HTTP/HTTPS → transport `TCP`). Return `ErrPortCollision` with a message naming the conflicting owner.

- [ ] **Step 4: Run to verify it passes** → PASS.

- [ ] **Step 5: Verify + commit**
Run: `gofmt -l internal/ && go build ./...`
```bash
git add internal/services/stream_port_collision.go internal/repository/ internal/services/stream_port_collision_test.go
git commit -m "feat(l4): merge-aware port-collision check"
```

### Task 20: Reserved ports + enforcement wiring

**Files:**
- Modify: `internal/services/stream_port_collision.go` (reserved-port rule)
- Modify: `internal/services/route_write_validation.go` (call `CheckPortCollision` on L4 create/update)
- Modify: `internal/services/domain_service.go` (Create/Update: when merged, reject a domain port that hits a stream L4 port)
- Modify: `internal/services/domain_template_service.go` (Update: when flipping `mergeGateways`→true or enabling a flag, validate no `(transport,port)` clashes in the newly-merged set)
- Test: `internal/services/stream_port_collision_test.go`, `route_write_validation_test.go`

**Interfaces:**
- Produces: `func ValidateListenerPort(port int, reserved ReservedPorts) error`; `ReservedPorts{ EnvoyInternal: []int{19000,19001}, Placeholder: streamplan.PlaceholderPort, DomainDefaults: []int{...} }`; sentinel `ErrReservedPort`.

- [ ] **Step 1: Write the failing tests**
```go
func TestValidateListenerPort_RejectsEnvoyInternal(t *testing.T) {
	assert.ErrorIs(t, services.ValidateListenerPort(19000, defaultReserved), services.ErrReservedPort)
}
func TestValidateListenerPort_RejectsPlaceholder(t *testing.T) {
	assert.ErrorIs(t, services.ValidateListenerPort(streamplan.PlaceholderPort, defaultReserved), services.ErrReservedPort)
}
func TestValidateListenerPort_RejectsOutOfRange(t *testing.T) {
	assert.Error(t, services.ValidateListenerPort(0, defaultReserved))
	assert.Error(t, services.ValidateListenerPort(70000, defaultReserved))
}
func TestCheckPortCollision_MergedDomainEnabled_Reserves80And443(t *testing.T) {
	// merged + enable_domain template: stream tcp:443 → ErrReservedPort even with no Domain yet
}
```

- [ ] **Step 2: Run to verify it fails** → FAIL.

- [ ] **Step 3: Implement** `ValidateListenerPort` (range 1-65535, not Envoy internal, not placeholder; and when merged+domain-enabled, reserve the template default HTTP/HTTPS ports). Wire the collision + reserved check into `validateRouteShapeAndConflicts` for L4; add the domain-side and template-flip checks.

- [ ] **Step 4: Run to verify it passes** → PASS.

- [ ] **Step 5: Verify + commit**
Run: `gofmt -l internal/ && go build ./... && go test ./internal/services/ -run 'Port|Collision|Reserved' -v`
```bash
git add internal/services/ && git commit -m "feat(l4): reserved-port validation + collision enforcement at route/domain/template"
```

---

# PHASE 5 — Validation (reject L7 + external backend; exactly-one-owner)

### Task 21: L4 route validation

**Files:**
- Modify: `internal/services/route_validation.go` (add `validateL4RouteConfig`; carve L4 out of `validateRouteConfig` :99-131 which requires path matching)
- Modify: `internal/services/route_write_validation.go` (`validateRouteShapeAndConflicts` :140-153 — branch tcp/udp)
- Test: `internal/services/route_validation_test.go`

**Interfaces:**
- Consumes: `models.Route.IsL4()` (Task 13).
- Produces: `func validateL4RouteConfig(route models.Route) error`; sentinels `ErrL4RejectsL7Field`, `ErrL4ExternalBackend`, `ErrL4MissingListenerPort`, `ErrL4MissingBackend`.

- [ ] **Step 1: Write the failing tests**
```go
func TestValidateL4_RejectsPathMatch(t *testing.T) {
	r := models.Route{Protocol: models.RouteProtocolTCP, Config: models.RouteConfig{ListenerPort: 5432,
		Matches: []models.RouteMatch{{Path: &models.PathMatch{Type: "Prefix", Value: "/"}}}}}
	assert.ErrorIs(t, services.ValidateL4RouteConfig(r), services.ErrL4RejectsL7Field)
}
func TestValidateL4_RejectsExternalBackend(t *testing.T) {
	r := models.Route{Protocol: models.RouteProtocolTCP, Config: models.RouteConfig{ListenerPort: 5432,
		Backends: []models.RouteBackend{{Type: "external", AddressType: "fqdn", Address: "db.example.com", Port: 5432}}}}
	assert.ErrorIs(t, services.ValidateL4RouteConfig(r), services.ErrL4ExternalBackend)
}
func TestValidateL4_RequiresListenerPortAndBackend(t *testing.T) {
	assert.ErrorIs(t, services.ValidateL4RouteConfig(models.Route{Protocol: models.RouteProtocolUDP}), services.ErrL4MissingListenerPort)
}
func TestValidateL4_RejectsSecurityMode(t *testing.T) {
	r := models.Route{Protocol: models.RouteProtocolTCP, SecurityMode: models.SecurityModeClient,
		Config: models.RouteConfig{ListenerPort: 5432, Backends: oneK8sBackend()}}
	assert.ErrorIs(t, services.ValidateL4RouteConfig(r), services.ErrL4RejectsL7Field)
}
```

- [ ] **Step 2: Run to verify it fails** → FAIL.

- [ ] **Step 3: Implement** `ValidateL4RouteConfig`: reject non-empty `Matches`, any filter/redirect/directResponse/urlRewrite/header-modifier field, `SecurityMode != ""/general`, WAF, client attachments → `ErrL4RejectsL7Field`; reject any backend with `Type == "external"` → `ErrL4ExternalBackend` (the single seam); require `ListenerPort != 0` and `len(Backends) >= 1`. In `validateRouteConfig`, early-return to the L4 validator when `route.IsL4()` so the path-matching requirement is not applied.

- [ ] **Step 4: Run to verify it passes** → PASS.

- [ ] **Step 5: Map sentinels** in the route handler to 400.

- [ ] **Step 6: Verify + commit**
Run: `gofmt -l internal/ && go build ./... && go test ./internal/services/ -run ValidateL4 -v`
```bash
git add internal/services/ internal/handlers/ && git commit -m "feat(l4): reject L7 fields + external backends on L4 routes; require port+backend"
```

### Task 22: Exactly-one-owner validation

**Files:**
- Modify: `internal/services/route_write_validation.go`
- Test: `internal/services/route_write_validation_test.go`

**Interfaces:**
- Produces: sentinel `ErrRouteOwnerAmbiguous` — a create/update where neither or both of `DomainID`/`StreamID` is set is rejected before persist (defense-in-depth alongside the DB CHECK).

- [ ] **Step 1: Write the failing test** — route with both set → error; with neither → error; with exactly one → ok.
- [ ] **Step 2: Run to verify it fails** → FAIL.
- [ ] **Step 3: Implement** the check (`(DomainID != nil) != (StreamID != nil)`), and verify an L4 create resolves `StreamID` + the stream's project; an HTTP create keeps `DomainID`.
- [ ] **Step 4: Run to verify it passes** → PASS.
- [ ] **Step 5: Verify + commit** — `gofmt -l internal/ && go build ./...`; `git commit -m "feat(l4): exactly-one-owner route validation"`

---

# PHASE 6 — L4 traffic policy (BackendTrafficPolicy subset)

### Task 23: BTP targetRef kind + per-protocol field gating

**Files:**
- Modify: `internal/routeplan/input.go` (`GetRouteKind` :58-63 → return `TCPRoute`/`UDPRoute` for tcp/udp)
- Modify: `internal/routeplan/backendtrafficpolicy.go` (gate fields per protocol)
- Modify: `internal/services/route_validation.go` (reject HTTP-only BTP fields on L4; reject UDP non-LB policy)
- Test: `internal/routeplan/backendtrafficpolicy_test.go`, `internal/services/route_validation_test.go`

**Interfaces:**
- Consumes: `models.Route.Transport()`.
- Produces: BTP for a TCP route emits only `{maxConnections, maxRequestsPerConnection}` CB fields, LB (any algorithm; consistent-hash → source-IP), passive + active-TCP health check, TCP timeouts; for UDP emits LB only. `GetRouteKind` returns `"TCPRoute"`/`"UDPRoute"`.

- [ ] **Step 1: Write the failing tests**
```go
func TestGetRouteKind_L4(t *testing.T) {
	assert.Equal(t, "TCPRoute", routeplan.GetRouteKind(models.RouteProtocolTCP))
	assert.Equal(t, "UDPRoute", routeplan.GetRouteKind(models.RouteProtocolUDP))
}
func TestValidate_TCP_RejectsRequestOrientedCB(t *testing.T) {
	// tcp route with circuitBreaker.maxPendingRequests set → ErrL4RejectsL7Field
}
func TestValidate_UDP_RejectsHealthCheck(t *testing.T) {
	// udp route with any healthCheck set → ErrL4RejectsL7Field
}
func TestValidate_TCP_AllowsMaxConnections(t *testing.T) {
	// tcp route with circuitBreaker.maxConnections=100 → no error
}
```

- [ ] **Step 2: Run to verify it fails** → FAIL.

- [ ] **Step 3: Implement** `GetRouteKind` tcp/udp; in the BTP builder, when the target is L4, emit only the allowed subset (TCP: `maxConnections`/`maxRequestsPerConnection`, LB, passive+active-TCP HC, TCP timeouts; UDP: LB only); in validation, reject HTTP-only BTP fields and the request-oriented CB counters for L4, and reject any non-LB policy for UDP.

- [ ] **Step 4: Run to verify it passes** → PASS.

- [ ] **Step 5: Verify + commit**
Run: `gofmt -l internal/ && go build ./... && go test ./internal/routeplan/ ./internal/services/ -run 'RouteKind|TCP|UDP|L4' -v`
```bash
git add internal/routeplan/ internal/services/ && git commit -m "feat(l4): BTP targetRef kind + per-protocol field gating (TCP subset, UDP LB-only)"
```

---

# PHASE 7 — L4 observability

### Task 24: L4 metrics queries

**Files:**
- Modify: `internal/services/metrics_service.go` (add L4 PromQL: active connections, connection rate, bytes in/out, filtered by stream/listener)
- Modify: `internal/handlers/` (expose under the stream/route metrics endpoint)
- Test: `internal/services/metrics_service_test.go`

**Interfaces:**
- Produces: `MetricsService.StreamL4Metrics(ctx, projectID, streamID string) (L4Metrics, error)` with fields `ActiveConnections, ConnectionRate, BytesIn, BytesOut` per listener.

- [ ] **Step 1: Write the failing test** — with a stubbed Prometheus client returning sample `envoy_tcp_downstream_cx_total` / `_tx_bytes_total` series, `StreamL4Metrics` returns the parsed values. Mirror the existing HTTP metrics test harness.
- [ ] **Step 2: Run to verify it fails** → FAIL.
- [ ] **Step 3: Implement** the PromQL (Envoy `tcp_proxy`/`udp_proxy` stats; e.g. `envoy_tcp_downstream_cx_active`, `rate(envoy_tcp_downstream_cx_total[5m])`, `envoy_tcp_downstream_cx_rx_bytes_total` / `_tx_bytes_total`) and the service method.
- [ ] **Step 4: Run to verify it passes** → PASS.
- [ ] **Step 5: Verify + commit** — `make openapi-check && gofmt -l internal/ && go build ./...`; `git commit -m "feat(l4): L4 metrics (connections/rate/throughput)"`

### Task 25: Frontend — L4 metrics card

**Files (frontend-v2 worktree):**
- Create: an L4 metrics card component; Modify: the Stream detail + L4-route detail pages to render it.

- [ ] **Step 1:** Add the API client call for `StreamL4Metrics`.
- [ ] **Step 2:** Build a card showing active connections, connection rate, bytes in/out (no latency/error cards).
- [ ] **Step 3:** Render on Stream + L4-route detail.
- [ ] **Step 4: Verify + commit** — `npm run build`; `git commit -m "feat(l4): L4 metrics card"`

---

# PHASE 8 — L4 route frontend form

### Task 26: Slim L4 route create/edit form

**Files (frontend-v2 worktree):**
- Modify: `src/types/index.ts` (`RouteProtocol` union → add `'tcp' | 'udp'`; `Route`/`CreateRouteInput` gain `streamId?`, `listenerPort?`)
- Modify: `src/lib/api/routes.ts`
- Create: `src/app/(authenticated)/.../streams/[streamId]/routes/create/page.tsx` (+ edit)

**Interfaces:**
- Consumes: route endpoints (protocol tcp/udp, streamId, listenerPort), the collision 409 from Task 20.

- [ ] **Step 1:** Extend types: `RouteProtocol = 'http' | 'grpc' | 'tcp' | 'udp'`; add `streamId`, `listenerPort` to route inputs.
- [ ] **Step 2:** Build the slim form: protocol radio (TCP/UDP), listener port input with **live collision check** (call a lightweight validate endpoint or catch the 409 on submit and show inline), weighted K8s-Service backends (service/namespace/port/weight, + add backend), advanced L4-policy section gated by protocol (TCP: circuit breaker maxConnections/maxRequestsPerConnection, LB algorithm, health check; UDP: LB only). No hostname/match/filter/security fields.
- [ ] **Step 3:** On submit, surface the mixed-proto-LB **warning** when adding a UDP route to a merged LoadBalancer template.
- [ ] **Step 4:** Wire create/edit into the Stream detail routes table.
- [ ] **Step 5: Verify + commit** — `npm run build`; `git commit -m "feat(l4): slim TCP/UDP route form with live port-collision + protocol-gated policy"`

---

## Final verification (before merge)

- [ ] Backend full gate: `make mocks-check && make openapi-check && gofmt -l internal/ && go build ./... && go test ./...` (and the integration suite with `INTEGRATION_DB_URL` set).
- [ ] Frontend: `npm run build` in the frontend worktree.
- [ ] Manual smoke (optional, against a kind cluster): create a Gateway Template with `enable_stream`, create a Stream, add a `tcp:5432` route + a `udp:53` route, confirm the Gateway lists both listeners and the LB exposes them; confirm a duplicate `tcp:5432` and a `tcp:443` (merged+domain) are rejected.
- [ ] Use `superpowers:finishing-a-development-branch` to open the PR(s): one for backend-v2 (`feat/l4-streams-spec` → rename branch to `feat/l4-streams` before pushing), one for frontend-v2.
