# L4 Stream Legibility and L4 Route Parity (Frontend) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **Repo:** This plan is implemented in `frontend-v2` (Next.js), planned against `main` @ `b7a3603`. It depends on the backend plan (`2026-10-16-l4-stream-legibility-parity-backend.md`) having shipped the ten new endpoints and the additive fields. Unit tests mock the API, so Tasks 1–9 do not need a running backend; the manual check in Task 10 does.

**Goal:** Make a Stream legible as "an L4 entry point; each route is a port on it" (create, list and detail show dedicated vs shared, the IP, who shares it and the port map) and bring L4 routes to parity with HTTP routes: tabbed form with manifest preview, an L4 route detail page with Manifests and History (compare + rollback), and a working, correctly labelled approval diff.

**Architecture:** New types and `streamsApi`/`domainTemplatesApi` methods mirror the backend contract field-for-field. All decision logic (mode wording, port-map split, free-port count, list filter, form tab errors, validation, status badges, diff modes, approval labels, version selection) lives in pure helpers under `src/lib/utils/*.ts` with colocated tests. Pages compose small self-fetching components in `src/components/streams/` (entry point panels, manifests, history, delete modal); because `TabsContent` unmounts inactive tabs, a component placed in a tab fetches only when that tab opens.

**Tech Stack:** Next.js 16 (App Router), TypeScript, Tailwind, axios API client, Jest + React Testing Library (jsdom, SWC via `next/jest`), plain `useState`/`useEffect`.

**Spec:** `../specs/2026-10-16-l4-stream-legibility-parity-design.md` (in backend-v2; sections F1–F7, the Frontend Testing items, Global Constraints).

## Global Constraints

- No change to any object FastGateway applies to a cluster (frontend: read-only views + existing submit paths only).
- Additive API only; existing endpoints and fields keep their shape (`routesApi`, `routeVersionsApi` are untouched).
- The entry point port map and create-time 400/409 agree: the UI never shows "free" for a port create rejects; the server 400/409 stays authoritative and is surfaced inline.
- Reserved ports 19000, 19001 and 60000 are always shown as reserved.
- Frontend decision logic lives in `src/lib/utils/*.ts` with colocated tests.
- Wording, exactly: badge **Dedicated IP** / **Shared IP**; dedicated explanation "This stream has its own entry point and IP."; shared explanation "This stream is a named group of ports on the template's shared entry point. Ports are unique across everything on it."; template option `shared-envoy — Shared IP`; unassigned address "assigned on first deploy" (create) / "not assigned yet" (detail); approval label "Stream: {streamName}"; banner "Waiting for approval" + link "Review in Approvals".
- House rules: `@/` alias for app imports, `./relative` for the unit under test; colocated tests (no `__tests__`); controlled inputs; never reject what the backend accepts.
- Shell: paths contain `[projectId]` etc. — **always quote them** in `git add` (zsh treats unquoted brackets as globs). Jest path patterns are regexes: escape brackets (`'streams/\[streamId\]/page'`).
- Commits: conventional messages ending with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. PR descriptions end with `🤖 Generated with [Claude Code](https://claude.com/claude-code)`.

## Review Focus

- **Clicking Next into the Preview tab** swaps the Next button for the submit button at the same spot; without distinct React `key`s the browser submits the form on that click. Expected: Next only moves to Preview; nothing is created until "Create Route"/"Save Changes" is pressed (test in Task 7: "stepping into Preview with Next does not submit the form").
- **A slow preview response after the user edited the form** (open Preview, go back, change the port, reopen) must not overwrite the newer preview. Expected: only the latest request's result is shown (test in Task 7: "ignores a stale preview response").
- **Switching templates quickly on stream create** — a late entry point response for the previous template must not replace the current one. Expected: the panel always describes the selected template (test in Task 4: "ignores a late entry point response").
- **Entry point load failure on stream detail** (backend fails closed with 500). Expected: an error message in the panel, never a guessed "Dedicated IP"; the rest of the page still works (test in Task 6: "a failed entry point load shows the error and never a guessed mode").
- **Backend `reserved` list missing the platform ports** (older or buggy response). Expected: 19000/19001/60000 still listed as reserved (once per port, "TCP+UDP") and counted as not free (tests in Task 2 `withDefaultReserved` and Task 6 "reserved platform ports are shown even if the response omits them").

## Plan rulings (spec vs code; spec wins on behaviour, code wins on names)

1. **Domain Manifests "viewer" is inline markup**, not a component (`<h3>` + `<pre className="bg-gray-900 text-gray-100 p-4 rounded-lg overflow-x-auto text-sm">` in `domains/[domainId]/page.tsx:890-932`). The `<pre>` is extracted verbatim into `src/components/streams/ManifestYaml.tsx`; stream manifests wrap it in the existing `Accordion` (spec: "collapsible").
2. **HTTP route delete has no preview** (`routesApi.previewDelete` is unused today). The L4 delete modal is new, renders the `previewDeleteRoute` result with `YamlDiffViewer` in `delete` mode, and still allows "Submit Delete Request" if the preview fails (server stays authoritative).
3. **Submit only on the Preview tab**, like the domain route form (`create/page.tsx:6094-6124`) and the spec's "every L4 route change shows a manifest preview before submit". The submit handler is unchanged except: client errors jump to the first tab with errors; a 409 returns to Basic Info. Labels "Create Route"/"Save Changes" are kept.
4. **Validation moved verbatim into `validateL4Form`** (same rules, same messages) so Next can check only the tab being left (domain-form behaviour) and the logic sits in a tested helper.
5. **`freePortCount` counts port numbers conservatively**: a number used on either transport, or reserved, is "taken". Backend reservation is transport-agnostic (`ValidateListenerPort` checks the number only) while collisions are per transport, so this can under-count free UDP-only numbers but never claims free a port create would reject.
6. **`withDefaultReserved`** unions 19000/19001 (`envoy_internal`) and 60000 (`placeholder`) on **both TCP and UDP** client-side (keyed by transport + port), to honour "always shown as reserved" even if a response omits them. The backend lists them on both transports (backend plan ruling R2), so the UI renders them via `reservedSummary`: one line per port, transports merged ("19000/TCP+UDP").
7. **Template option label is exactly `name — Shared IP`** per spec; the old `(exposureType)` suffix is dropped.
8. **Owner team on the stream Routes tab**: `StreamRoute` gains optional `teamId`/`team`. The backend `ListByStreamID` already `Preload("Team")`, so no backend change is needed.
9. **Edit returns to the route detail page** via `L4RouteForm` (`routeId` set → `doneUrl = …/routes/:routeId`); `routes/[routeId]/edit/page.tsx` itself is unchanged.
10. **List-only stream fields are optional** on `Stream` (`GET /streams/:id` does not return them); missing values render `—`.
11. **Status badges extracted** to `l4StatusBadge`; `pending_deploy` now reads "Pending Deploy" (stream page used "Pending Approval"), matching the HTTP route detail page.
12. **Approval detail gains a context line** (`Stream: x` for L4; the domain name for L7, as the list already shows). The detail subtitle falls back to `entityName`/route name when there is no path match (L4 has none).
13. **Named types for inline shapes**: `PreviewDeleteResult` copies `routesApi.previewDelete`'s inline return type; `ApprovalDiff` replaces `approvalsApi.getDiff`'s inline type (adds `routeKind`); `RouteYAMLs` mirrors the backend struct. `RouteVersion`/`RouteVersionListResponse` stay in `src/lib/api/route-versions.ts`, where the code defines them.
14. **Rollback result typed `unknown`**: callers ignore it and redirect to Approvals, as `routeVersionsApi.rollback` (`Promise<any>`) callers do.
15. **No IP column on the stream list** (cross-check with the backend plan, ruling R1): the backend never sets `Stream.loadBalancerAddress`; the real address is the Gateway's live `status.addresses`, read only by the entry point endpoints. Filling a list column would mean one cluster read per stream, so the IP shows on stream detail and stream create (`entrypoint.address`) and the list shows the Entry point badge + template. Nothing in this plan reads `loadBalancerAddress`.

---

## Before Task 1

- [ ] In `frontend-v2`: `git status` must be clean. Then `git checkout main && git pull --ff-only origin main && git checkout -b feat/l4-stream-legibility-parity`.
- [ ] `npm ci` if `node_modules` is missing. Baseline: `npx tsc --noEmit` → no output; `npx jest src/components/streams src/lib/utils/l4route.test.ts src/lib/api/streams.test.ts 'streams/create/page' 'streams/\[streamId\]/page'` → all pass.

---

### Task 1: Types and API client methods

**Files:**
- Modify: `src/types/index.ts:291-329` (Stream comment, `Stream`, `StreamRoute`; add entry point / manifests / preview types after `StreamRoute`)
- Modify: `src/types/index.ts:1412-1431` (`ApprovalRequest` + new `RouteKind`, `ApprovalDiff`)
- Modify: `src/lib/api/streams.ts` (imports; append 9 methods)
- Modify: `src/lib/api/domainTemplates.ts` (import; append `getEntrypoint`)
- Modify: `src/lib/api/approvals.ts` (`getDiff` returns `ApprovalDiff`)
- Test: `src/lib/api/streams.test.ts` (append), `src/lib/api/domainTemplates.test.ts` (create)

**Interfaces:**
- Produces (types, `@/types`): `EntrypointMode = 'dedicated'|'shared'`; `TransportPort {transport, port}`; `EntrypointPortOwner {kind:'stream_route'|'domain', streamId?, streamName?, routeId?, routeName?, status?, domainId?, domainName?}`; `EntrypointPort extends TransportPort {owner}`; `ReservedPortReason`; `ReservedPort extends TransportPort {reason}`; `PortRange {min,max}`; `StreamEntrypoint {mode, templateId, templateName, gatewayClass, address, portRange: PortRange|null, sharedWith:{streams:{id,name}[], domains:{id,name,hostname}[]}, ports, reserved}`; `StreamRouteYAML {routeId, name, protocol, routeYaml, backendTrafficPolicyYaml?}`; `StreamYAMLs {gatewayYaml, routes}`; `RouteYAMLs {httpRouteYaml, …, backendTrafficPolicyYaml?}`; `L4PreviewCreateResult {proposedYaml, proposedBackendTrafficPolicyYaml?}`; `L4PreviewUpdateResult {currentYaml, proposedYaml, currentBackendTrafficPolicyYaml?, proposedBackendTrafficPolicyYaml?}`; `PreviewDeleteResult`; `RouteKind`; `ApprovalDiff`. `Stream` gains optional `mode`, `gatewayTemplateName`, `routeCount`, `ports`. `StreamRoute` gains optional `teamId`, `team`. `ApprovalRequest` gains optional `streamId`, `streamName`.
- Produces (API): `streamsApi.getEntrypoint(projectId, streamId): Promise<StreamEntrypoint>`; `getYamls(projectId, streamId): Promise<StreamYAMLs>`; `getRouteYamls(projectId, streamId, routeId): Promise<RouteYAMLs>`; `previewCreateRoute(projectId, streamId, data: CreateRouteInput): Promise<L4PreviewCreateResult>`; `previewUpdateRoute(projectId, streamId, routeId, data: UpdateRouteInput): Promise<L4PreviewUpdateResult>`; `previewDeleteRoute(projectId, streamId, routeId): Promise<PreviewDeleteResult>`; `listRouteVersions(projectId, streamId, routeId, page = 1, limit = 20): Promise<RouteVersionListResponse>`; `getRouteVersion(projectId, streamId, routeId, version: number): Promise<RouteVersion>`; `rollbackRouteVersion(projectId, streamId, routeId, version: number): Promise<unknown>`; `domainTemplatesApi.getEntrypoint(projectId, domainTemplateId): Promise<StreamEntrypoint>`; `approvalsApi.getDiff(...): Promise<ApprovalDiff>`.

- [ ] **Step 1: Write the failing tests**

Append to `src/lib/api/streams.test.ts`:

```ts
test('entrypoint, manifest and preview methods hit the stream-scoped endpoints', async () => {
  (apiClient.get as jest.Mock).mockResolvedValue({ data: {} });
  (apiClient.post as jest.Mock).mockResolvedValue({ data: {} });
  const body = { name: 'db', teamId: 't1', config: { matches: [], backends: [], listenerPort: 5432 } };
  await streamsApi.getEntrypoint('p1', 's1');
  await streamsApi.getYamls('p1', 's1');
  await streamsApi.getRouteYamls('p1', 's1', 'r1');
  await streamsApi.previewCreateRoute('p1', 's1', body);
  await streamsApi.previewUpdateRoute('p1', 's1', 'r1', { config: body.config });
  await streamsApi.previewDeleteRoute('p1', 's1', 'r1');
  expect(apiClient.get).toHaveBeenCalledWith('/projects/p1/streams/s1/entrypoint');
  expect(apiClient.get).toHaveBeenCalledWith('/projects/p1/streams/s1/yamls');
  expect(apiClient.get).toHaveBeenCalledWith('/projects/p1/streams/s1/routes/r1/yamls');
  expect(apiClient.post).toHaveBeenCalledWith('/projects/p1/streams/s1/routes/preview', body);
  expect(apiClient.post).toHaveBeenCalledWith('/projects/p1/streams/s1/routes/r1/preview', { config: body.config });
  expect(apiClient.get).toHaveBeenCalledWith('/projects/p1/streams/s1/routes/r1/preview-delete');
});

test('version methods hit the stream-scoped version endpoints', async () => {
  (apiClient.get as jest.Mock).mockResolvedValue({ data: { data: [], total: 0 } });
  (apiClient.post as jest.Mock).mockResolvedValue({ data: {} });
  await streamsApi.listRouteVersions('p1', 's1', 'r1');
  await streamsApi.getRouteVersion('p1', 's1', 'r1', 3);
  await streamsApi.rollbackRouteVersion('p1', 's1', 'r1', 3);
  expect(apiClient.get).toHaveBeenCalledWith('/projects/p1/streams/s1/routes/r1/versions', { params: { page: 1, limit: 20 } });
  expect(apiClient.get).toHaveBeenCalledWith('/projects/p1/streams/s1/routes/r1/versions/3');
  expect(apiClient.post).toHaveBeenCalledWith('/projects/p1/streams/s1/routes/r1/versions/3/rollback');
});
```

Create `src/lib/api/domainTemplates.test.ts`:

```ts
import { domainTemplatesApi } from './domainTemplates';
import apiClient from './client';

jest.mock('./client');

test('getEntrypoint GETs the template entry point', async () => {
  (apiClient.get as jest.Mock).mockResolvedValue({ data: { mode: 'shared' } });
  const ep = await domainTemplatesApi.getEntrypoint('p1', 't1');
  expect(apiClient.get).toHaveBeenCalledWith('/projects/p1/domain-templates/t1/entrypoint');
  expect(ep.mode).toBe('shared');
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `npx jest src/lib/api/streams.test.ts src/lib/api/domainTemplates.test.ts`
Expected: FAIL — `TypeError: streamsApi.getEntrypoint is not a function` and `TypeError: domainTemplatesApi.getEntrypoint is not a function`.

- [ ] **Step 3: Add the types**

In `src/types/index.ts`, replace the comment line above `StreamStatus`:

```ts
// L4 Streams (TCP/UDP). A Stream is an L4 entry point; each route is a port on it.
```

In `interface Stream`, replace the tail

```ts
  loadBalancerAddress?: string;
  createdAt?: string;
  updatedAt?: string;
}
```

with

```ts
  loadBalancerAddress?: string;
  createdAt?: string;
  updatedAt?: string;
  // List-only enrichment (GET /projects/:projectId/streams). Absent on GET /streams/:id.
  /** `shared` iff the stream's template has mergeGateways. */
  mode?: EntrypointMode;
  gatewayTemplateName?: string;
  /** Non-rejected L4 routes on the stream. */
  routeCount?: number;
  /** (transport, port) of the stream's non-rejected L4 routes. */
  ports?: TransportPort[];
}
```

Replace `interface StreamRoute` (and add the new types right after it):

```ts
export interface StreamRoute {
  id: string;
  streamId?: string;
  name: string;
  protocol: 'tcp' | 'udp' | string;
  status: string;
  config?: { listenerPort?: number };
  /** Owner team (the stream route list preloads Team). */
  teamId?: string;
  team?: { id: string; name: string };
}

/** Entry point mode of a Stream: template mergeGateways false -> dedicated, true -> shared. */
export type EntrypointMode = 'dedicated' | 'shared';

/** A Gateway listener identity. Transport is "TCP" or "UDP". */
export interface TransportPort {
  transport: 'TCP' | 'UDP' | string;
  port: number;
}

export interface EntrypointPortOwner {
  kind: 'stream_route' | 'domain';
  streamId?: string;
  streamName?: string;
  routeId?: string;
  routeName?: string;
  status?: string;
  domainId?: string;
  domainName?: string;
}

export interface EntrypointPort extends TransportPort {
  owner: EntrypointPortOwner;
}

export type ReservedPortReason = 'envoy_internal' | 'placeholder' | 'template_listener';

export interface ReservedPort extends TransportPort {
  reason: ReservedPortReason;
}

export interface PortRange {
  min: number;
  max: number;
}

/** GET /streams/:streamId/entrypoint and GET /domain-templates/:domainTemplateId/entrypoint. */
export interface StreamEntrypoint {
  mode: EntrypointMode;
  templateId: string;
  templateName: string;
  gatewayClass: string;
  /** "" = not assigned yet. */
  address: string;
  portRange: PortRange | null;
  sharedWith: {
    streams: Array<{ id: string; name: string }>;
    domains: Array<{ id: string; name: string; hostname: string }>;
  };
  ports: EntrypointPort[];
  reserved: ReservedPort[];
}

/** One live L4 route in GET /streams/:streamId/yamls. */
export interface StreamRouteYAML {
  routeId: string;
  name: string;
  protocol: 'tcp' | 'udp' | string;
  routeYaml: string;
  /** Empty or absent when the route has no BackendTrafficPolicy. */
  backendTrafficPolicyYaml?: string;
}

export interface StreamYAMLs {
  gatewayYaml: string;
  routes: StreamRouteYAML[];
}

/** GET .../routes/:routeId/yamls. For an L4 route httpRouteYaml carries the TCPRoute/UDPRoute. */
export interface RouteYAMLs {
  httpRouteYaml: string;
  securityPolicyYaml?: string;
  backendTrafficPolicyYaml?: string;
  envoyExtensionPolicyYaml?: string;
  backendYaml?: string;
  httpRouteFilterYaml?: string;
  configMapYaml?: string;
}

/** POST /streams/:streamId/routes/preview. */
export interface L4PreviewCreateResult {
  proposedYaml: string;
  proposedBackendTrafficPolicyYaml?: string;
}

/** POST /streams/:streamId/routes/:routeId/preview. */
export interface L4PreviewUpdateResult {
  currentYaml: string;
  proposedYaml: string;
  currentBackendTrafficPolicyYaml?: string;
  proposedBackendTrafficPolicyYaml?: string;
}

/** GET .../routes/:routeId/preview-delete (same shape as routesApi.previewDelete). */
export interface PreviewDeleteResult {
  currentYaml: string;
  currentSecurityPolicyYaml?: string;
  currentBackendTrafficPolicyYaml?: string;
  currentEnvoyExtensionPolicyYaml?: string;
  currentBackendYaml?: string;
  currentHttpRouteFilterYaml?: string;
  currentConfigMapYaml?: string;
}
```

In `interface ApprovalRequest` (the one with `routeId`, ~L1412), replace

```ts
  entityName?: string;
  domainName?: string;
  stages?: ApprovalStage[];
  createdAt: string;
  reviewedAt?: string;
}
```

with

```ts
  entityName?: string;
  domainName?: string;
  /** Set for L4 (stream) routes instead of domainName. */
  streamId?: string;
  streamName?: string;
  stages?: ApprovalStage[];
  createdAt: string;
  reviewedAt?: string;
}

export type RouteKind = 'HTTPRoute' | 'GRPCRoute' | 'TCPRoute' | 'UDPRoute';

/** GET /approvals/:approvalId/diff. */
export interface ApprovalDiff {
  action: string;
  /** Kind of the route manifest in currentYaml/proposedYaml; absent from older backends (= HTTPRoute). */
  routeKind?: RouteKind;
  currentYaml?: string;
  proposedYaml?: string;
  currentSecurityPolicyYaml?: string;
  proposedSecurityPolicyYaml?: string;
  currentBackendTrafficPolicyYaml?: string;
  proposedBackendTrafficPolicyYaml?: string;
  currentEnvoyExtensionPolicyYaml?: string;
  proposedEnvoyExtensionPolicyYaml?: string;
  currentBackendYaml?: string;
  proposedBackendYaml?: string;
  changeDescription?: string;
  aiReview?: AIReviewResult;
}
```

(The old-string block occurs exactly once: the other `domainName?:` at ~L1680 belongs to `Approval` and is followed by `changeDescription`, not `stages?`.)

- [ ] **Step 4: Add the API methods**

In `src/lib/api/streams.ts`, replace the import line with:

```ts
import type {
  Stream,
  CreateStreamInput,
  UpdateStreamInput,
  StreamRoute,
  L4Metrics,
  PaginatedResponse,
  Route,
  RouteWithWarnings,
  CreateRouteInput,
  UpdateRouteInput,
  StreamEntrypoint,
  StreamYAMLs,
  RouteYAMLs,
  L4PreviewCreateResult,
  L4PreviewUpdateResult,
  PreviewDeleteResult,
} from '@/types';
import type { RouteVersion, RouteVersionListResponse } from './route-versions';
```

and append these methods after `getMetrics` (before the closing `};`):

```ts
  // Entry point: mode, address, who shares it, and the port map (ports + reserved).
  getEntrypoint: async (projectId: string, streamId: string): Promise<StreamEntrypoint> => {
    const response = await apiClient.get<StreamEntrypoint>(`/projects/${projectId}/streams/${streamId}/entrypoint`);
    return response.data;
  },

  // Gateway YAML plus each live route's TCPRoute/UDPRoute and BackendTrafficPolicy YAML.
  getYamls: async (projectId: string, streamId: string): Promise<StreamYAMLs> => {
    const response = await apiClient.get<StreamYAMLs>(`/projects/${projectId}/streams/${streamId}/yamls`);
    return response.data;
  },

  // httpRouteYaml carries the TCPRoute/UDPRoute.
  getRouteYamls: async (projectId: string, streamId: string, routeId: string): Promise<RouteYAMLs> => {
    const response = await apiClient.get<RouteYAMLs>(
      `/projects/${projectId}/streams/${streamId}/routes/${routeId}/yamls`
    );
    return response.data;
  },

  // Same validation as createRoute: a rejected preview returns the same 400/409.
  previewCreateRoute: async (projectId: string, streamId: string, data: CreateRouteInput): Promise<L4PreviewCreateResult> => {
    const response = await apiClient.post<L4PreviewCreateResult>(
      `/projects/${projectId}/streams/${streamId}/routes/preview`,
      data
    );
    return response.data;
  },

  previewUpdateRoute: async (
    projectId: string,
    streamId: string,
    routeId: string,
    data: UpdateRouteInput
  ): Promise<L4PreviewUpdateResult> => {
    const response = await apiClient.post<L4PreviewUpdateResult>(
      `/projects/${projectId}/streams/${streamId}/routes/${routeId}/preview`,
      data
    );
    return response.data;
  },

  previewDeleteRoute: async (projectId: string, streamId: string, routeId: string): Promise<PreviewDeleteResult> => {
    const response = await apiClient.get<PreviewDeleteResult>(
      `/projects/${projectId}/streams/${streamId}/routes/${routeId}/preview-delete`
    );
    return response.data;
  },

  listRouteVersions: async (
    projectId: string,
    streamId: string,
    routeId: string,
    page = 1,
    limit = 20
  ): Promise<RouteVersionListResponse> => {
    const response = await apiClient.get<RouteVersionListResponse>(
      `/projects/${projectId}/streams/${streamId}/routes/${routeId}/versions`,
      { params: { page, limit } }
    );
    return response.data;
  },

  getRouteVersion: async (projectId: string, streamId: string, routeId: string, version: number): Promise<RouteVersion> => {
    const response = await apiClient.get<RouteVersion>(
      `/projects/${projectId}/streams/${streamId}/routes/${routeId}/versions/${version}`
    );
    return response.data;
  },

  // Re-submits the version's config through the approval flow. Callers ignore the body
  // and redirect to Approvals, as routeVersionsApi.rollback's callers do.
  rollbackRouteVersion: async (projectId: string, streamId: string, routeId: string, version: number): Promise<unknown> => {
    const response = await apiClient.post<unknown>(
      `/projects/${projectId}/streams/${streamId}/routes/${routeId}/versions/${version}/rollback`
    );
    return response.data;
  },
```

In `src/lib/api/domainTemplates.ts`, add `StreamEntrypoint` to the `@/types` import and append after `listDomains`:

```ts
  // Entry point a new stream on this template would get (ports is [] when dedicated).
  getEntrypoint: async (projectId: string, domainTemplateId: string): Promise<StreamEntrypoint> => {
    const response = await apiClient.get<StreamEntrypoint>(
      `/projects/${projectId}/domain-templates/${domainTemplateId}/entrypoint`
    );
    return response.data;
  },
```

In `src/lib/api/approvals.ts`, change the import to `import type { ApprovalRequest, PaginatedResponse, ApprovalDiff } from '@/types';` and replace the whole `getDiff` method with:

```ts
  getDiff: async (projectId: string, approvalId: string): Promise<ApprovalDiff> => {
    const response = await apiClient.get<ApprovalDiff>(`/projects/${projectId}/approvals/${approvalId}/diff`);
    return response.data;
  },
```

- [ ] **Step 5: Run tests and typecheck**

Run: `npx jest src/lib/api/streams.test.ts src/lib/api/domainTemplates.test.ts`
Expected: PASS (7 tests).
Run: `npx tsc --noEmit`
Expected: no output (the approval detail page still compiles: its inline state type accepts an `ApprovalDiff`).

- [ ] **Step 6: Commit**

```bash
git add src/types/index.ts src/lib/api/streams.ts src/lib/api/streams.test.ts src/lib/api/domainTemplates.ts src/lib/api/domainTemplates.test.ts src/lib/api/approvals.ts
git commit -m "feat(streams): entry point, manifest, preview and version API clients

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Stream entry point and list helpers

**Files:**
- Create: `src/lib/utils/stream-entrypoint.ts`, `src/lib/utils/stream-entrypoint.test.ts`
- Create: `src/lib/utils/stream-list.ts`, `src/lib/utils/stream-list.test.ts`

**Interfaces:**
- Consumes: types from Task 1.
- Produces: `entrypointLabel(mode: EntrypointMode): { badge: 'Dedicated IP'|'Shared IP'; explanation: string }`; `templateMode(t: Pick<DomainTemplate,'mergeGateways'>): EntrypointMode`; `templateOptionLabel(t: Pick<DomainTemplate,'name'|'mergeGateways'>): string`; `portLabel(p: TransportPort): string` ("5432/TCP"); `comparePorts(a, b): number`; `splitPorts(ports: EntrypointPort[], streamId?: string): { mine: EntrypointPort[]; neighbors: EntrypointPort[] }`; `rangeSize(range: PortRange): number`; `freePortCount(range: PortRange|null, ports: TransportPort[], reserved: TransportPort[]): number|null`; `withDefaultReserved(reserved: ReservedPort[]): ReservedPort[]`; `reservedLabel(r: ReservedPort): string`; `reservedSummary(reserved: ReservedPort[]): string[]` ("19000/TCP+UDP (Envoy internal)"); `ownerLabel(owner: EntrypointPortOwner): string`; `sharedWithSummary(sharedWith: StreamEntrypoint['sharedWith']): string`. `stream-list.ts`: `type StreamStatusFilter = '' | StreamStatus`; `filterStreams(streams: Stream[], status: StreamStatusFilter, query: string): Stream[]`; `portsSummary(ports: TransportPort[]|undefined, limit = 3): { shown: string[]; more: number }`.

- [ ] **Step 1: Write the failing tests**

Create `src/lib/utils/stream-entrypoint.test.ts`:

```ts
import {
  entrypointLabel,
  freePortCount,
  ownerLabel,
  portLabel,
  rangeSize,
  reservedLabel,
  reservedSummary,
  sharedWithSummary,
  splitPorts,
  templateOptionLabel,
  withDefaultReserved,
} from './stream-entrypoint';
import type { EntrypointPort, ReservedPort } from '@/types';

const mine = (port: number, transport = 'TCP'): EntrypointPort => ({
  transport,
  port,
  owner: { kind: 'stream_route', streamId: 's1', streamName: 'test-stream', routeId: `r${port}`, routeName: `route-${port}`, status: 'active' },
});
const other = (port: number, transport = 'TCP'): EntrypointPort => ({
  transport,
  port,
  owner: { kind: 'stream_route', streamId: 's2', streamName: 'cache', routeId: 'rx', routeName: 'redis', status: 'active' },
});
const domain = (port: number): EntrypointPort => ({
  transport: 'TCP',
  port,
  owner: { kind: 'domain', domainId: 'd1', domainName: 'api' },
});
// The backend lists the platform ports on both transports (validation rejects them on both).
const DEFAULT_RESERVED: ReservedPort[] = [
  { transport: 'TCP', port: 19000, reason: 'envoy_internal' },
  { transport: 'UDP', port: 19000, reason: 'envoy_internal' },
  { transport: 'TCP', port: 19001, reason: 'envoy_internal' },
  { transport: 'UDP', port: 19001, reason: 'envoy_internal' },
  { transport: 'TCP', port: 60000, reason: 'placeholder' },
  { transport: 'UDP', port: 60000, reason: 'placeholder' },
];

describe('entrypointLabel', () => {
  test('dedicated', () => {
    expect(entrypointLabel('dedicated')).toEqual({
      badge: 'Dedicated IP',
      explanation: 'This stream has its own entry point and IP.',
    });
  });
  test('shared', () => {
    expect(entrypointLabel('shared')).toEqual({
      badge: 'Shared IP',
      explanation:
        "This stream is a named group of ports on the template's shared entry point. Ports are unique across everything on it.",
    });
  });
});

test('templateOptionLabel shows the mode', () => {
  expect(templateOptionLabel({ name: 'shared-envoy', mergeGateways: true })).toBe('shared-envoy — Shared IP');
  expect(templateOptionLabel({ name: 'edge-l4', mergeGateways: false })).toBe('edge-l4 — Dedicated IP');
});

describe('splitPorts', () => {
  test('mine are this stream route ports; everything else is a neighbour; both sorted', () => {
    const { mine: m, neighbors: n } = splitPorts([other(9001), mine(9002), domain(443), mine(53, 'UDP'), mine(53)], 's1');
    expect(m.map(portLabel)).toEqual(['53/TCP', '53/UDP', '9002/TCP']);
    expect(n.map(portLabel)).toEqual(['443/TCP', '9001/TCP']);
  });
  test('without a stream id (create) every port is a neighbour', () => {
    const { mine: m, neighbors: n } = splitPorts([mine(9000), domain(443)]);
    expect(m).toEqual([]);
    expect(n).toHaveLength(2);
  });
});

describe('freePortCount / rangeSize', () => {
  test('null range gives null', () => {
    expect(freePortCount(null, [], DEFAULT_RESERVED)).toBeNull();
  });
  test('inverted range gives null', () => {
    expect(freePortCount({ min: 10, max: 5 }, [], [])).toBeNull();
  });
  test('counts port numbers in range that nothing holds', () => {
    expect(rangeSize({ min: 9000, max: 9100 })).toBe(101);
    expect(freePortCount({ min: 9000, max: 9100 }, [mine(9000), other(9001)], DEFAULT_RESERVED)).toBe(99);
  });
  test('ports and reservations outside the range are ignored', () => {
    expect(freePortCount({ min: 9000, max: 9009 }, [domain(443)], DEFAULT_RESERVED)).toBe(10);
  });
  test('a number used on one transport counts once and as taken (conservative)', () => {
    expect(freePortCount({ min: 50, max: 59 }, [mine(53), mine(53, 'UDP')], [])).toBe(9);
  });
  test('a reserved port inside the range is not free', () => {
    expect(freePortCount({ min: 59990, max: 60009 }, [], DEFAULT_RESERVED)).toBe(19);
  });
});

describe('withDefaultReserved (Review Focus: response omits platform ports)', () => {
  test('adds the platform ports the backend left out (both transports), keeps the rest, sorted', () => {
    const out = withDefaultReserved([{ transport: 'TCP', port: 443, reason: 'template_listener' }]);
    expect(out.map(reservedLabel)).toEqual([
      '443/TCP (Template listener)',
      '19000/TCP (Envoy internal)',
      '19000/UDP (Envoy internal)',
      '19001/TCP (Envoy internal)',
      '19001/UDP (Envoy internal)',
      '60000/TCP (Placeholder listener)',
      '60000/UDP (Placeholder listener)',
    ]);
  });
  test('an empty list still yields the platform ports on both transports', () => {
    expect(withDefaultReserved([]).map(portLabel)).toEqual([
      '19000/TCP', '19000/UDP', '19001/TCP', '19001/UDP', '60000/TCP', '60000/UDP',
    ]);
  });
  test('does not duplicate entries the backend already sent (keyed by transport + port)', () => {
    expect(withDefaultReserved(DEFAULT_RESERVED)).toHaveLength(6);
  });
});

describe('reservedSummary', () => {
  test('one line per port and reason, transports merged', () => {
    expect(
      reservedSummary([...DEFAULT_RESERVED, { transport: 'TCP', port: 443, reason: 'template_listener' }])
    ).toEqual([
      '443/TCP (Template listener)',
      '19000/TCP+UDP (Envoy internal)',
      '19001/TCP+UDP (Envoy internal)',
      '60000/TCP+UDP (Placeholder listener)',
    ]);
  });
});

describe('ownerLabel', () => {
  test('stream route', () => {
    expect(ownerLabel(other(9001).owner)).toBe('Stream cache · route redis');
  });
  test('domain', () => {
    expect(ownerLabel(domain(443).owner)).toBe('Domain api');
  });
  test('falls back to ids when names are missing', () => {
    expect(ownerLabel({ kind: 'stream_route', streamId: 's9', routeId: 'r9' })).toBe('Stream s9 · route r9');
    expect(ownerLabel({ kind: 'domain', domainId: 'd9' })).toBe('Domain d9');
  });
});

test('sharedWithSummary pluralises', () => {
  expect(sharedWithSummary({ streams: [{ id: 'a', name: 'a' }], domains: [] })).toBe('1 stream and 0 domains');
  expect(
    sharedWithSummary({
      streams: [{ id: 'a', name: 'a' }, { id: 'b', name: 'b' }],
      domains: [{ id: 'd', name: 'd', hostname: 'd.example.com' }],
    })
  ).toBe('2 streams and 1 domain');
});
```

Create `src/lib/utils/stream-list.test.ts`:

```ts
import { filterStreams, portsSummary } from './stream-list';
import type { Stream } from '@/types';

const s = (id: string, name: string, status: Stream['status']): Stream => ({
  id,
  projectId: 'p1',
  name,
  namespace: 'ns',
  gatewayTemplateId: 't1',
  k8sGatewayName: `str-${name}`,
  k8sGatewayClass: 'gc',
  status,
});
const all = [s('1', 'postgres', 'active'), s('2', 'cache', 'pending'), s('3', 'Cache-EU', 'error')];

describe('filterStreams', () => {
  test('empty status and query keep everything', () => {
    expect(filterStreams(all, '', '')).toHaveLength(3);
  });
  test('status filters exactly', () => {
    expect(filterStreams(all, 'pending', '').map((x) => x.name)).toEqual(['cache']);
  });
  test('query is a trimmed, case-insensitive name substring', () => {
    expect(filterStreams(all, '', '  CACHE ').map((x) => x.name)).toEqual(['cache', 'Cache-EU']);
  });
  test('status and query combine', () => {
    expect(filterStreams(all, 'error', 'cache').map((x) => x.name)).toEqual(['Cache-EU']);
  });
});

describe('portsSummary', () => {
  test('first three sorted by port, then the remainder count', () => {
    expect(
      portsSummary([
        { transport: 'TCP', port: 9000 },
        { transport: 'TCP', port: 6379 },
        { transport: 'UDP', port: 53 },
        { transport: 'TCP', port: 5432 },
      ])
    ).toEqual({ shown: ['53/UDP', '5432/TCP', '6379/TCP'], more: 1 });
  });
  test('no ports', () => {
    expect(portsSummary(undefined)).toEqual({ shown: [], more: 0 });
    expect(portsSummary([])).toEqual({ shown: [], more: 0 });
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `npx jest src/lib/utils/stream-entrypoint.test.ts src/lib/utils/stream-list.test.ts`
Expected: FAIL — `Cannot find module './stream-entrypoint'` and `Cannot find module './stream-list'`.

- [ ] **Step 3: Write the implementation**

Create `src/lib/utils/stream-entrypoint.ts`:

```ts
// Pure helpers for a Stream's entry point: mode wording, the port map split
// (this stream's ports vs neighbours), the free-port count and owner labels.
// Kept free of React so they can be unit-tested directly.
import type {
  DomainTemplate,
  EntrypointMode,
  EntrypointPort,
  EntrypointPortOwner,
  PortRange,
  ReservedPort,
  ReservedPortReason,
  StreamEntrypoint,
  TransportPort,
} from '@/types';

export interface EntrypointLabel {
  badge: 'Dedicated IP' | 'Shared IP';
  explanation: string;
}

export function entrypointLabel(mode: EntrypointMode): EntrypointLabel {
  return mode === 'shared'
    ? {
        badge: 'Shared IP',
        explanation:
          "This stream is a named group of ports on the template's shared entry point. Ports are unique across everything on it.",
      }
    : { badge: 'Dedicated IP', explanation: 'This stream has its own entry point and IP.' };
}

/** mergeGateways true -> shared, false -> dedicated (the backend's rule). */
export function templateMode(t: Pick<DomainTemplate, 'mergeGateways'>): EntrypointMode {
  return t.mergeGateways ? 'shared' : 'dedicated';
}

/** Stream-create template option, e.g. "shared-envoy — Shared IP". */
export function templateOptionLabel(t: Pick<DomainTemplate, 'name' | 'mergeGateways'>): string {
  return `${t.name} — ${entrypointLabel(templateMode(t)).badge}`;
}

/** "5432/TCP". */
export function portLabel(p: TransportPort): string {
  return `${p.port}/${p.transport.toUpperCase()}`;
}

/** By port, then transport (TCP before UDP). */
export function comparePorts(a: TransportPort, b: TransportPort): number {
  return a.port - b.port || a.transport.localeCompare(b.transport);
}

/**
 * mine = this stream's own route ports; neighbors = everything else on the entry
 * point (other streams' routes, domains). With no streamId (stream create) every
 * port is a neighbour.
 */
export function splitPorts(
  ports: EntrypointPort[],
  streamId?: string
): { mine: EntrypointPort[]; neighbors: EntrypointPort[] } {
  const mine: EntrypointPort[] = [];
  const neighbors: EntrypointPort[] = [];
  for (const p of ports) {
    if (streamId && p.owner.kind === 'stream_route' && p.owner.streamId === streamId) mine.push(p);
    else neighbors.push(p);
  }
  return { mine: mine.sort(comparePorts), neighbors: neighbors.sort(comparePorts) };
}

export function rangeSize(range: PortRange): number {
  return Math.max(0, range.max - range.min + 1);
}

/**
 * Port numbers in [min, max] that no listed port and no reservation holds, on any
 * transport. Conservative on purpose: reservations are transport-agnostic on the
 * backend, and a number used on one transport is counted as taken, so the UI
 * never calls a port free that create would reject. Null when there is no range.
 */
export function freePortCount(
  range: PortRange | null,
  ports: TransportPort[],
  reserved: TransportPort[]
): number | null {
  if (!range || range.max < range.min) return null;
  const taken = new Set<number>();
  for (const p of [...ports, ...reserved]) {
    if (p.port >= range.min && p.port <= range.max) taken.add(p.port);
  }
  return rangeSize(range) - taken.size;
}

// Mirrors RESERVED_PORTS in gateway-listeners.ts / the backend DefaultReservedPorts.
// Validation rejects these numbers on both transports, so both are listed.
const PLATFORM_RESERVED: ReservedPort[] = (
  [
    [19000, 'envoy_internal'],
    [19001, 'envoy_internal'],
    [60000, 'placeholder'],
  ] as const
).flatMap(([port, reason]) =>
  (['TCP', 'UDP'] as const).map((transport) => ({ transport, port, reason }))
);

const reservedKey = (r: TransportPort) => `${r.transport}/${r.port}`;

/** The response's reserved list plus any platform entry it left out (keyed by transport + port), sorted. */
export function withDefaultReserved(reserved: ReservedPort[]): ReservedPort[] {
  const have = new Set(reserved.map(reservedKey));
  return [...reserved, ...PLATFORM_RESERVED.filter((r) => !have.has(reservedKey(r)))].sort(comparePorts);
}

/** Display lines: one per (port, reason), transports merged — "19000/TCP+UDP (Envoy internal)". */
export function reservedSummary(reserved: ReservedPort[]): string[] {
  const groups = new Map<string, { port: number; reason: ReservedPortReason; transports: string[] }>();
  for (const r of [...reserved].sort(comparePorts)) {
    const key = `${r.port}|${r.reason}`;
    const g = groups.get(key) ?? { port: r.port, reason: r.reason, transports: [] };
    if (!g.transports.includes(r.transport)) g.transports.push(r.transport);
    groups.set(key, g);
  }
  return [...groups.values()].map(
    (g) => `${g.port}/${g.transports.join('+')} (${REASON_LABELS[g.reason] ?? g.reason})`
  );
}

const REASON_LABELS: Record<ReservedPortReason, string> = {
  envoy_internal: 'Envoy internal',
  placeholder: 'Placeholder listener',
  template_listener: 'Template listener',
};

/** "19000/TCP (Envoy internal)". */
export function reservedLabel(r: ReservedPort): string {
  return `${portLabel(r)} (${REASON_LABELS[r.reason] ?? r.reason})`;
}

/** "Stream cache · route redis" or "Domain api". */
export function ownerLabel(owner: EntrypointPortOwner): string {
  if (owner.kind === 'domain') return `Domain ${owner.domainName || owner.domainId || 'unknown'}`;
  return `Stream ${owner.streamName || owner.streamId || 'unknown'} · route ${owner.routeName || owner.routeId || 'unknown'}`;
}

const plural = (n: number, noun: string) => `${n} ${noun}${n === 1 ? '' : 's'}`;

/** "2 streams and 1 domain". */
export function sharedWithSummary(sharedWith: StreamEntrypoint['sharedWith']): string {
  return `${plural(sharedWith.streams.length, 'stream')} and ${plural(sharedWith.domains.length, 'domain')}`;
}
```

Create `src/lib/utils/stream-list.ts`:

```ts
// Pure helpers for the stream list: client-side status/name filtering and the
// compact Ports column. The list endpoint has no search or status params.
import type { Stream, StreamStatus, TransportPort } from '@/types';
import { comparePorts, portLabel } from './stream-entrypoint';

export type StreamStatusFilter = '' | StreamStatus;

export function filterStreams(streams: Stream[], status: StreamStatusFilter, query: string): Stream[] {
  const q = query.trim().toLowerCase();
  return streams.filter(
    (s) => (status === '' || s.status === status) && (q === '' || s.name.toLowerCase().includes(q))
  );
}

/** First `limit` ports as "5432/TCP" (sorted by port), plus how many more there are. */
export function portsSummary(ports: TransportPort[] | undefined, limit = 3): { shown: string[]; more: number } {
  const sorted = [...(ports ?? [])].sort(comparePorts);
  return { shown: sorted.slice(0, limit).map(portLabel), more: Math.max(0, sorted.length - limit) };
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `npx jest src/lib/utils/stream-entrypoint.test.ts src/lib/utils/stream-list.test.ts`
Expected: PASS (all cases).

- [ ] **Step 5: Commit**

```bash
git add src/lib/utils/stream-entrypoint.ts src/lib/utils/stream-entrypoint.test.ts src/lib/utils/stream-list.ts src/lib/utils/stream-list.test.ts
git commit -m "feat(streams): entry point and stream list helpers

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: L4 route helpers (form tabs, validation, badges, actions, diff mode)

**Files:**
- Modify: `src/lib/utils/l4route.ts` (type import; append helpers)
- Test: `src/lib/utils/l4route.test.ts` (append)

**Interfaces:**
- Consumes: existing `rangeError`, `PortCollision`, `L4BackendForm`, `L4Protocol` in `l4route.ts`.
- Produces: `type L4FormTab = 'basic'|'backends'|'policy'|'preview'`; `L4_FORM_TABS: L4FormTab[]`; `tabForErrorKey(key: string): L4FormTab`; `tabErrors(errors: Record<string, string|null|undefined>): L4FormTab[]` (in tab order); `errorsForTab(errors: Record<string,string>, tab): Record<string,string>`; `mergeTabErrors(prev, tab, next): Record<string,string>`; `interface L4ValidationInput {isEdit, name, teamId, protocol, port: number|null, range: {min,max}|null, collision: PortCollision|null, backends}`; `validateL4Form(v): Record<string,string>`; `l4RouteKind(protocol: string): 'TCPRoute'|'UDPRoute'`; `type DiffMode = 'create'|'update'|'delete'`; `diffMode(current?, proposed?): DiffMode|null`; `type BadgeVariant`; `l4StatusBadge(status: string): {variant: BadgeVariant; label: string}`; `routeActions(status: string, canCreateRoutes: boolean): {canEdit, canDelete, canDeploy, awaitingApproval}`; `policySummary(cfg: BackendTrafficPolicyConfig|undefined): string[]`.

- [ ] **Step 1: Write the failing tests**

In `src/lib/utils/l4route.test.ts`, extend the import block to:

```ts
import {
  buildCreateInput,
  buildPolicy,
  diffMode,
  emptyPolicyForm,
  errorsForTab,
  findPortCollision,
  l4RouteKind,
  l4StatusBadge,
  mergeTabErrors,
  parseListenerPort,
  policyCapabilities,
  policySummary,
  portInRange,
  rangeError,
  routeActions,
  tabErrors,
  tabForErrorKey,
  validateL4Form,
  type L4ValidationInput,
} from './l4route';
```

and append:

```ts
describe('form tabs', () => {
  test('tabForErrorKey maps every validation key to its tab', () => {
    expect(['name', 'team', 'port'].map(tabForErrorKey)).toEqual(['basic', 'basic', 'basic']);
    expect(['backends', 'backend-0', 'backend-12'].map(tabForErrorKey)).toEqual(['backends', 'backends', 'backends']);
    expect(tabForErrorKey('cbMaxConnections')).toBe('policy');
  });
  test('tabErrors lists the tabs that hold a non-empty error, in tab order', () => {
    expect(tabErrors({})).toEqual([]);
    expect(tabErrors({ 'backend-1': 'x', port: 'y' })).toEqual(['basic', 'backends']);
    expect(tabErrors({ port: undefined, name: '' })).toEqual([]);
  });
  test('errorsForTab / mergeTabErrors replace only one tab', () => {
    const errs = { name: 'a', 'backend-0': 'b' };
    expect(errorsForTab(errs, 'basic')).toEqual({ name: 'a' });
    expect(mergeTabErrors(errs, 'basic', {})).toEqual({ 'backend-0': 'b' });
    expect(mergeTabErrors(errs, 'backends', { backends: 'c' })).toEqual({ name: 'a', backends: 'c' });
  });
});

describe('validateL4Form (rules unchanged from the form)', () => {
  const ok = (): L4ValidationInput => ({
    isEdit: false,
    name: 'web',
    teamId: 't1',
    protocol: 'tcp',
    port: 6379,
    range: { min: 5000, max: 7000 },
    collision: null,
    backends: [{ namespace: 'ns', service: 'pg', port: '5432', weight: '100' }],
  });
  test('a valid form has no errors', () => {
    expect(validateL4Form(ok())).toEqual({});
  });
  test('create requires name and team; edit does not', () => {
    expect(validateL4Form({ ...ok(), name: ' ', teamId: '' })).toEqual({
      name: 'Name is required',
      team: 'Owner team is required',
    });
    expect(validateL4Form({ ...ok(), isEdit: true, name: '', teamId: '' })).toEqual({});
  });
  test('port: unparseable, then out of range, then collision', () => {
    expect(validateL4Form({ ...ok(), port: null }).port).toBe('Listener port must be an integer between 1 and 65535');
    expect(validateL4Form({ ...ok(), port: 9000 }).port).toBe('Listener port must be between 5000 and 7000');
    expect(validateL4Form({ ...ok(), port: 9000, range: null }).port).toBeUndefined();
    expect(
      validateL4Form({ ...ok(), collision: { routeId: 'r1', routeName: 'db', port: 6379, protocol: 'tcp' } }).port
    ).toBe('Port 6379/TCP is already used by route "db"');
  });
  test('backends: none, incomplete, bad port, bad weight', () => {
    expect(validateL4Form({ ...ok(), backends: [] }).backends).toBe('At least one backend is required');
    const row = ok().backends[0];
    expect(validateL4Form({ ...ok(), backends: [{ ...row, service: '' }] })['backend-0']).toBe(
      'Namespace and service are required'
    );
    expect(validateL4Form({ ...ok(), backends: [{ ...row, port: '70000' }] })['backend-0']).toBe(
      'Backend port must be between 1 and 65535'
    );
    expect(validateL4Form({ ...ok(), backends: [{ ...row, weight: '101' }] })['backend-0']).toBe(
      'Weight must be between 0 and 100'
    );
    expect(validateL4Form({ ...ok(), backends: [{ ...row, weight: '' }] })).toEqual({});
  });
});

test('l4RouteKind', () => {
  expect(l4RouteKind('tcp')).toBe('TCPRoute');
  expect(l4RouteKind('UDP')).toBe('UDPRoute');
});

test('diffMode', () => {
  expect(diffMode(undefined, 'a')).toBe('create');
  expect(diffMode('a', 'b')).toBe('update');
  expect(diffMode('a', '')).toBe('delete');
  expect(diffMode('', undefined)).toBeNull();
});

test('l4StatusBadge', () => {
  expect(l4StatusBadge('active')).toEqual({ variant: 'success', label: 'Active' });
  expect(l4StatusBadge('approved')).toEqual({ variant: 'info', label: 'Approved - Ready to Deploy' });
  expect(l4StatusBadge('pending_deploy')).toEqual({ variant: 'info', label: 'Pending Deploy' });
  expect(l4StatusBadge('pending_update')).toEqual({ variant: 'warning', label: 'Pending Approval' });
  expect(l4StatusBadge('pending')).toEqual({ variant: 'warning', label: 'Pending' });
  expect(l4StatusBadge('rejected')).toEqual({ variant: 'error', label: 'Rejected' });
  expect(l4StatusBadge('error')).toEqual({ variant: 'error', label: 'Error' });
  expect(l4StatusBadge('weird')).toEqual({ variant: 'default', label: 'weird' });
});

test('routeActions mirrors the HTTP route detail gates', () => {
  expect(routeActions('active', true)).toEqual({ canEdit: true, canDelete: true, canDeploy: false, awaitingApproval: false });
  expect(routeActions('active', false)).toEqual({ canEdit: false, canDelete: false, canDeploy: false, awaitingApproval: false });
  expect(routeActions('pending_update', true)).toEqual({ canEdit: false, canDelete: false, canDeploy: false, awaitingApproval: true });
  expect(routeActions('approved', true)).toEqual({ canEdit: false, canDelete: false, canDeploy: true, awaitingApproval: false });
  expect(routeActions('pending_deploy', false).canDeploy).toBe(true);
  expect(routeActions('rejected', true)).toEqual({ canEdit: true, canDelete: true, canDeploy: false, awaitingApproval: false });
});

test('policySummary', () => {
  expect(policySummary(undefined)).toEqual([]);
  expect(
    policySummary({
      loadBalancer: { type: 'LeastRequest' },
      circuitBreaker: { maxConnections: 100, maxRequestsPerConnection: 10 },
      healthCheck: { active: { type: 'TCP', interval: '10s', timeout: '1s', unhealthyThreshold: 3, healthyThreshold: 2 } },
    })
  ).toEqual([
    'Load balancing: LeastRequest',
    'Circuit breaker: max 100 connections, max 10 requests per connection',
    'Health check: TCP active, every 10s, timeout 1s, unhealthy after 3, healthy after 2',
  ]);
  expect(policySummary({ circuitBreaker: {} })).toEqual(['Circuit breaker: defaults']);
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `npx jest src/lib/utils/l4route.test.ts`
Expected: FAIL — `TypeError: (0 , _l4route.tabForErrorKey) is not a function` (and similar for the other new helpers); the 14 existing tests still pass.

- [ ] **Step 3: Write the implementation**

In `src/lib/utils/l4route.ts`, add `BackendTrafficPolicyConfig` to the `@/types` type import, then append at the end of the file:

```ts
// ---- Tabbed form: Basic Info · Backends · Traffic Policy · Preview ----

export type L4FormTab = 'basic' | 'backends' | 'policy' | 'preview';
export const L4_FORM_TABS: L4FormTab[] = ['basic', 'backends', 'policy', 'preview'];

/** Which tab shows the field a validation key belongs to. */
export function tabForErrorKey(key: string): L4FormTab {
  if (key === 'name' || key === 'team' || key === 'port') return 'basic';
  if (key === 'backends' || key.startsWith('backend-')) return 'backends';
  return 'policy';
}

/** The tabs that hold at least one non-empty error, in tab order (drives the tab badges). */
export function tabErrors(errors: Record<string, string | null | undefined>): L4FormTab[] {
  const hit = new Set<L4FormTab>();
  for (const [key, msg] of Object.entries(errors)) {
    if (msg) hit.add(tabForErrorKey(key));
  }
  return L4_FORM_TABS.filter((t) => hit.has(t));
}

export function errorsForTab(errors: Record<string, string>, tab: L4FormTab): Record<string, string> {
  return Object.fromEntries(Object.entries(errors).filter(([key]) => tabForErrorKey(key) === tab));
}

/** prev with `tab`'s errors replaced by `next` (other tabs' errors are kept). */
export function mergeTabErrors(
  prev: Record<string, string>,
  tab: L4FormTab,
  next: Record<string, string>
): Record<string, string> {
  const kept = Object.fromEntries(Object.entries(prev).filter(([key]) => tabForErrorKey(key) !== tab));
  return { ...kept, ...next };
}

export interface L4ValidationInput {
  isEdit: boolean;
  name: string;
  teamId: string;
  protocol: L4Protocol;
  /** parseListenerPort() result. */
  port: number | null;
  /** Template TCP/UDP range; null = unknown, so no client-side range check. */
  range: { min: number; max: number } | null;
  collision: PortCollision | null;
  backends: L4BackendForm[];
}

/** The L4 route form's client-side rules (unchanged). Keys: name, team, port, backends, backend-<i>. */
export function validateL4Form(v: L4ValidationInput): Record<string, string> {
  const errs: Record<string, string> = {};
  if (!v.isEdit) {
    if (!v.name.trim()) errs.name = 'Name is required';
    if (!v.teamId) errs.team = 'Owner team is required';
  }
  const outOfRange = v.port != null && v.range ? rangeError(v.port, v.range.min, v.range.max) : null;
  if (v.port == null) errs.port = 'Listener port must be an integer between 1 and 65535';
  else if (outOfRange) errs.port = outOfRange;
  else if (v.collision)
    errs.port = `Port ${v.port}/${v.protocol.toUpperCase()} is already used by route "${v.collision.routeName}"`;
  if (v.backends.length === 0) errs.backends = 'At least one backend is required';
  v.backends.forEach((b, i) => {
    if (!b.namespace || !b.service) errs[`backend-${i}`] = 'Namespace and service are required';
    else if (!/^\d+$/.test(b.port) || Number(b.port) < 1 || Number(b.port) > 65535)
      errs[`backend-${i}`] = 'Backend port must be between 1 and 65535';
    else if (b.weight !== '' && (!/^\d+$/.test(b.weight) || Number(b.weight) > 100))
      errs[`backend-${i}`] = 'Weight must be between 0 and 100';
  });
  return errs;
}

// ---- Manifests, status and actions ----

/** Kind of the route manifest an L4 route renders to. */
export function l4RouteKind(protocol: string): 'TCPRoute' | 'UDPRoute' {
  return protocol.toLowerCase() === 'udp' ? 'UDPRoute' : 'TCPRoute';
}

export type DiffMode = 'create' | 'update' | 'delete';

/** YamlDiffViewer mode for one manifest; null when there is nothing on either side. */
export function diffMode(current?: string, proposed?: string): DiffMode | null {
  if (!current && !proposed) return null;
  if (!current) return 'create';
  if (!proposed) return 'delete';
  return 'update';
}

export type BadgeVariant = 'default' | 'success' | 'warning' | 'error' | 'info';

/** Badge for a stream status or an L4 route status. */
export function l4StatusBadge(status: string): { variant: BadgeVariant; label: string } {
  switch (status) {
    case 'active':
      return { variant: 'success', label: 'Active' };
    case 'approved':
      return { variant: 'info', label: 'Approved - Ready to Deploy' };
    case 'pending_deploy':
      return { variant: 'info', label: 'Pending Deploy' };
    case 'pending_create':
    case 'pending_update':
    case 'pending_delete':
      return { variant: 'warning', label: 'Pending Approval' };
    case 'pending':
      return { variant: 'warning', label: 'Pending' };
    case 'error':
      return { variant: 'error', label: 'Error' };
    case 'rejected':
      return { variant: 'error', label: 'Rejected' };
    default:
      return { variant: 'default', label: status };
  }
}

const IN_FLIGHT = ['pending_create', 'pending_update', 'pending_delete', 'approved', 'pending_deploy'];

/** Route detail actions, with the same gates as the HTTP route detail page. */
export function routeActions(
  status: string,
  canCreateRoutes: boolean
): { canEdit: boolean; canDelete: boolean; canDeploy: boolean; awaitingApproval: boolean } {
  const inFlight = IN_FLIGHT.includes(status);
  return {
    canEdit: canCreateRoutes && !inFlight,
    canDelete: canCreateRoutes && !inFlight,
    canDeploy: status === 'approved' || status === 'pending_deploy',
    awaitingApproval: status === 'pending_create' || status === 'pending_update' || status === 'pending_delete',
  };
}

/** One human-readable line per configured L4 policy section (Overview tab). */
export function policySummary(cfg: BackendTrafficPolicyConfig | undefined): string[] {
  if (!cfg) return [];
  const out: string[] = [];
  if (cfg.loadBalancer) out.push(`Load balancing: ${cfg.loadBalancer.type}`);
  if (cfg.circuitBreaker) {
    const parts: string[] = [];
    if (cfg.circuitBreaker.maxConnections != null) parts.push(`max ${cfg.circuitBreaker.maxConnections} connections`);
    if (cfg.circuitBreaker.maxRequestsPerConnection != null)
      parts.push(`max ${cfg.circuitBreaker.maxRequestsPerConnection} requests per connection`);
    out.push(`Circuit breaker: ${parts.length > 0 ? parts.join(', ') : 'defaults'}`);
  }
  const active = cfg.healthCheck?.active;
  if (active) {
    const parts = [`${active.type} active`];
    if (active.interval) parts.push(`every ${active.interval}`);
    if (active.timeout) parts.push(`timeout ${active.timeout}`);
    if (active.unhealthyThreshold != null) parts.push(`unhealthy after ${active.unhealthyThreshold}`);
    if (active.healthyThreshold != null) parts.push(`healthy after ${active.healthyThreshold}`);
    out.push(`Health check: ${parts.join(', ')}`);
  }
  return out;
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `npx jest src/lib/utils/l4route.test.ts`
Expected: PASS (existing 14 + new).

- [ ] **Step 5: Commit**

```bash
git add src/lib/utils/l4route.ts src/lib/utils/l4route.test.ts
git commit -m "feat(streams): L4 route form-tab, validation, badge and action helpers

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Stream create — Entry point panel and mode in template labels

**Files:**
- Create: `src/components/streams/TemplateEntrypointSummary.tsx`
- Modify: `src/app/projects/[projectId]/streams/create/page.tsx` (imports ~L8-9; template option label ~L139-142; panel after the template `Select` ~L145)
- Test: `src/app/projects/[projectId]/streams/create/page.test.tsx` (rewrite)

**Interfaces:**
- Consumes: `domainTemplatesApi.getEntrypoint` (Task 1); `entrypointLabel`, `freePortCount`, `rangeSize`, `sharedWithSummary`, `withDefaultReserved`, `templateOptionLabel` (Task 2).
- Produces: `<TemplateEntrypointSummary projectId templateId />` — root element `data-testid="entrypoint-summary"`; button "Show who shares it" toggles `data-testid="shared-with-list"`.

- [ ] **Step 1: Write the failing test**

Replace `src/app/projects/[projectId]/streams/create/page.test.tsx` with:

```tsx
import { act, render, screen, waitFor, fireEvent } from '@testing-library/react';
import CreateStreamPage from './page';

jest.mock('next/navigation', () => ({
  useParams: () => ({ projectId: 'p1' }),
  useRouter: () => ({ push: jest.fn() }),
}));
jest.mock('@/lib/api', () => ({
  projectsApi: { get: jest.fn().mockResolvedValue({ id: 'p1', name: 'proj' }) },
  domainTemplatesApi: {
    list: jest.fn().mockResolvedValue({
      data: [
        { id: 't1', name: 'tpl', exposureType: 'LoadBalancer', status: 'active', mergeGateways: false },
        { id: 't2', name: 'shared-envoy', exposureType: 'ClusterIP', status: 'active', mergeGateways: true },
      ],
    }),
    getEntrypoint: jest.fn(),
  },
  domainsApi: {
    listAvailableNamespaces: jest.fn().mockResolvedValue({ namespaces: ['fastgateway-system'] }),
  },
  streamsApi: { create: jest.fn() },
}));
import { streamsApi, domainTemplatesApi } from '@/lib/api';

const RESERVED = [
  { transport: 'TCP', port: 19000, reason: 'envoy_internal' },
  { transport: 'TCP', port: 19001, reason: 'envoy_internal' },
  { transport: 'TCP', port: 60000, reason: 'placeholder' },
];
const DEDICATED = {
  mode: 'dedicated',
  templateId: 't1',
  templateName: 'tpl',
  gatewayClass: 'gc-tpl',
  address: '',
  portRange: { min: 9000, max: 9100 },
  sharedWith: { streams: [], domains: [] },
  ports: [],
  reserved: RESERVED,
};
const SHARED = {
  mode: 'shared',
  templateId: 't2',
  templateName: 'shared-envoy',
  gatewayClass: 'shared-envoy-clusterip',
  address: '',
  portRange: { min: 9000, max: 9100 },
  sharedWith: {
    streams: [{ id: 's2', name: 'cache' }],
    domains: [
      { id: 'd1', name: 'api', hostname: 'api.example.com' },
      { id: 'd2', name: 'web', hostname: 'web.example.com' },
    ],
  },
  ports: [
    { transport: 'TCP', port: 9000, owner: { kind: 'stream_route', streamId: 's2', streamName: 'cache', routeId: 'r1', routeName: 'redis', status: 'active' } },
    { transport: 'UDP', port: 9000, owner: { kind: 'stream_route', streamId: 's2', streamName: 'cache', routeId: 'r2', routeName: 'dns', status: 'active' } },
    { transport: 'TCP', port: 443, owner: { kind: 'domain', domainId: 'd1', domainName: 'api' } },
  ],
  reserved: [...RESERVED, { transport: 'TCP', port: 443, reason: 'template_listener' }],
};

beforeEach(() => {
  (domainTemplatesApi.getEntrypoint as jest.Mock).mockReset();
  (domainTemplatesApi.getEntrypoint as jest.Mock).mockImplementation((_p: string, id: string) =>
    Promise.resolve(id === 't2' ? SHARED : DEDICATED)
  );
});

const panel = () => screen.getByTestId('entrypoint-summary');

async function pick(templateId?: string) {
  render(<CreateStreamPage />);
  await waitFor(() => expect(screen.getByLabelText('Gateway Template')).toBeInTheDocument());
  if (templateId) fireEvent.change(screen.getByLabelText('Gateway Template'), { target: { value: templateId } });
}

async function fillAndSubmit() {
  await pick('t1');
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'pg' } });
  fireEvent.click(screen.getByRole('button', { name: 'Create Stream' }));
}

test('fetches templates with capability=stream and has no hostname field', async () => {
  await pick();
  expect(domainTemplatesApi.list).toHaveBeenCalledWith('p1', 1, 100, 'stream');
  expect(screen.queryByLabelText('Hostname')).not.toBeInTheDocument();
});

test.each([
  ['409 name taken', 'stream name already exists'],
  ['400 non-stream template', 'gateway template is not enabled for streams'],
])('surfaces the create error: %s', async (_label, message) => {
  (streamsApi.create as jest.Mock).mockRejectedValue({ response: { data: { error: message } } });
  await fillAndSubmit();
  await waitFor(() => expect(screen.getByText(message)).toBeInTheDocument());
});

test('template options show the entry point mode', async () => {
  await pick();
  expect(screen.getByRole('option', { name: 'tpl — Dedicated IP' })).toBeInTheDocument();
  expect(screen.getByRole('option', { name: 'shared-envoy — Shared IP' })).toBeInTheDocument();
});

test('a dedicated template explains its own IP and the port range', async () => {
  await pick('t1');
  await waitFor(() =>
    expect(panel()).toHaveTextContent('This stream gets its own entry point and IP. Routes can use ports 9000–9100.')
  );
  expect(domainTemplatesApi.getEntrypoint).toHaveBeenCalledWith('p1', 't1');
  expect(panel()).toHaveTextContent('Dedicated IP');
});

test('a shared template shows who shares it, an unassigned address and the free ports', async () => {
  await pick('t2');
  await waitFor(() => expect(panel()).toHaveTextContent('joins the shared entry point shared-envoy-clusterip'));
  expect(panel()).toHaveTextContent('IP assigned on first deploy');
  expect(panel()).toHaveTextContent('also used by 1 stream and 2 domains');
  expect(panel()).toHaveTextContent('100 of 101 ports free in 9000–9100');
  expect(screen.queryByTestId('shared-with-list')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Show who shares it' }));
  expect(screen.getByTestId('shared-with-list')).toHaveTextContent('Stream cache');
  expect(screen.getByTestId('shared-with-list')).toHaveTextContent('Domain api (api.example.com)');
});

test('stream create has no port, team or preview fields', async () => {
  await pick('t2');
  await waitFor(() => expect(panel()).toHaveTextContent('Shared IP'));
  expect(screen.queryByLabelText(/port/i)).not.toBeInTheDocument();
  expect(screen.queryByLabelText(/team/i)).not.toBeInTheDocument();
  expect(screen.queryByRole('tab', { name: /preview/i })).not.toBeInTheDocument();
});

test('a failed entry point load does not block create', async () => {
  (domainTemplatesApi.getEntrypoint as jest.Mock).mockRejectedValue({ response: { data: { error: 'boom' } } });
  await pick('t1');
  await waitFor(() => expect(panel()).toHaveTextContent('Could not load the entry point for this template (boom)'));
  expect(screen.getByRole('button', { name: 'Create Stream' })).toBeEnabled();
});

// Review Focus: switching templates quickly.
test('ignores a late entry point response for a template the user already left', async () => {
  let resolveFirst: (v: unknown) => void = () => {};
  (domainTemplatesApi.getEntrypoint as jest.Mock)
    .mockImplementationOnce(() => new Promise((resolve) => { resolveFirst = resolve; }))
    .mockImplementationOnce(() => Promise.resolve(SHARED));
  await pick('t1');
  fireEvent.change(screen.getByLabelText('Gateway Template'), { target: { value: 't2' } });
  await waitFor(() => expect(panel()).toHaveTextContent('Shared IP'));
  await act(async () => {
    resolveFirst(DEDICATED);
  });
  expect(panel()).toHaveTextContent('Shared IP');
  expect(panel()).not.toHaveTextContent('Dedicated IP');
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx jest 'streams/create/page'`
Expected: FAIL — `Unable to find an accessible element with the role "option" and name "tpl — Dedicated IP"`, and `Unable to find an element by: [data-testid="entrypoint-summary"]` in the panel tests. The first three tests still pass.

- [ ] **Step 3: Create the panel component**

Create `src/components/streams/TemplateEntrypointSummary.tsx`:

```tsx
'use client';

// Stream-create "Entry point" panel: what a new stream on the selected template
// lands on. Read-only; the server's create 400/409 stays authoritative.

import { useEffect, useState } from 'react';
import { AlertTriangle } from 'lucide-react';
import { Badge } from '@/components/ui';
import { domainTemplatesApi } from '@/lib/api';
import type { StreamEntrypoint } from '@/types';
import {
  entrypointLabel,
  freePortCount,
  rangeSize,
  sharedWithSummary,
  withDefaultReserved,
} from '@/lib/utils/stream-entrypoint';

interface Props {
  projectId: string;
  templateId: string;
}

export function TemplateEntrypointSummary({ projectId, templateId }: Props) {
  const [entrypoint, setEntrypoint] = useState<StreamEntrypoint | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [showShared, setShowShared] = useState(false);

  useEffect(() => {
    // The cleanup flag drops a late response for a template the user already left.
    let cancelled = false;
    setEntrypoint(null);
    setError(null);
    setShowShared(false);
    domainTemplatesApi
      .getEntrypoint(projectId, templateId)
      .then((data) => {
        if (!cancelled) setEntrypoint(data);
      })
      .catch((e: unknown) => {
        if (cancelled) return;
        const err = e as { response?: { data?: { error?: string } } };
        setError(err.response?.data?.error || 'request failed');
      });
    return () => {
      cancelled = true;
    };
  }, [projectId, templateId]);

  const box = 'p-4 rounded-lg border border-gray-200 bg-gray-50 text-sm text-gray-700 space-y-2';

  if (error) {
    return (
      <div
        data-testid="entrypoint-summary"
        role="alert"
        className="p-4 rounded-lg border border-amber-200 bg-amber-50 text-sm text-amber-800"
      >
        <div className="flex items-start gap-2">
          <AlertTriangle className="h-4 w-4 flex-shrink-0 mt-0.5" />
          <p>Could not load the entry point for this template ({error}). You can still create the stream.</p>
        </div>
      </div>
    );
  }

  if (!entrypoint) {
    return (
      <div data-testid="entrypoint-summary" className={box}>
        Loading entry point…
      </div>
    );
  }

  const { mode, portRange, sharedWith } = entrypoint;
  const label = entrypointLabel(mode);
  const rangeText = portRange ? `${portRange.min}–${portRange.max}` : null;
  const free = freePortCount(portRange, entrypoint.ports, withDefaultReserved(entrypoint.reserved));
  const othersCount = sharedWith.streams.length + sharedWith.domains.length;

  return (
    <div data-testid="entrypoint-summary" className={box}>
      <div className="flex items-center gap-2">
        <h3 className="font-medium text-gray-900">Entry point</h3>
        <Badge variant={mode === 'shared' ? 'info' : 'default'}>{label.badge}</Badge>
      </div>
      {mode === 'dedicated' ? (
        <p>
          This stream gets its own entry point and IP.
          {rangeText ? ` Routes can use ports ${rangeText}.` : ' This template has no TCP/UDP port range.'}
        </p>
      ) : (
        <p>
          This stream joins the shared entry point <code className="font-mono">{entrypoint.gatewayClass}</code> (IP{' '}
          {entrypoint.address || 'assigned on first deploy'}), also used by {sharedWithSummary(sharedWith)}.
          {portRange && free != null
            ? ` ${free} of ${rangeSize(portRange)} ports free in ${rangeText}.`
            : ' This template has no TCP/UDP port range.'}
        </p>
      )}
      {mode === 'shared' && othersCount > 0 && (
        <div>
          <button
            type="button"
            className="text-primary-600 hover:underline"
            onClick={() => setShowShared((v) => !v)}
          >
            {showShared ? 'Hide who shares it' : 'Show who shares it'}
          </button>
          {showShared && (
            <ul className="mt-1 list-disc pl-5" data-testid="shared-with-list">
              {sharedWith.streams.map((s) => (
                <li key={`s-${s.id}`}>Stream {s.name}</li>
              ))}
              {sharedWith.domains.map((d) => (
                <li key={`d-${d.id}`}>
                  Domain {d.name} ({d.hostname})
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </div>
  );
}
```

- [ ] **Step 4: Wire it into the create page**

In `src/app/projects/[projectId]/streams/create/page.tsx`, add after `import type { Project, DomainTemplate } from '@/types';`:

```tsx
import { TemplateEntrypointSummary } from '@/components/streams/TemplateEntrypointSummary';
import { templateOptionLabel } from '@/lib/utils/stream-entrypoint';
```

Replace

```tsx
                    ...usableTemplates.map((t) => ({
                      value: t.id,
                      label: `${t.name} (${t.exposureType})`,
                    })),
```

with

```tsx
                    ...usableTemplates.map((t) => ({
                      value: t.id,
                      label: templateOptionLabel(t),
                    })),
```

Replace

```tsx
                  error={formErrors.template}
                />

                <Input
                  id="name"
```

with

```tsx
                  error={formErrors.template}
                />

                {selectedTemplateId && (
                  <TemplateEntrypointSummary projectId={projectId} templateId={selectedTemplateId} />
                )}

                <Input
                  id="name"
```

(No port, team or Preview fields are added; `DomainTemplate` already carries `mergeGateways`.)

- [ ] **Step 5: Run tests to verify they pass**

Run: `npx jest 'streams/create/page'`
Expected: PASS (9 tests).

- [ ] **Step 6: Commit**

```bash
git add src/components/streams/TemplateEntrypointSummary.tsx 'src/app/projects/[projectId]/streams/create/page.tsx' 'src/app/projects/[projectId]/streams/create/page.test.tsx'
git commit -m "feat(streams): show the entry point and template mode on stream create

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Stream list — entry point columns, status filter, debounced search

**Files:**
- Modify (full rewrite): `src/app/projects/[projectId]/streams/page.tsx`
- Test: `src/app/projects/[projectId]/streams/page.test.tsx` (create)

**Interfaces:**
- Consumes: `Stream.mode`, `gatewayTemplateName`, `routeCount`, `ports` (Task 1); `entrypointLabel` (Task 2); `filterStreams`, `portsSummary`, `StreamStatusFilter` (Task 2).
- Produces: status select `aria-label="Status filter"`; search input `aria-label="Search streams by name"`; ports cell `data-testid="ports-<streamId>"`.

- [ ] **Step 1: Write the failing test**

Create `src/app/projects/[projectId]/streams/page.test.tsx`:

```tsx
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react';
import StreamsPage from './page';

jest.mock('next/navigation', () => ({ useParams: () => ({ projectId: 'p1' }) }));
jest.mock('@/lib/api', () => ({
  projectsApi: { get: jest.fn().mockResolvedValue({ id: 'p1', name: 'proj' }) },
  permissionsApi: { getProjectPermissions: jest.fn().mockResolvedValue({ canManageDomains: true }) },
  streamsApi: {
    list: jest.fn().mockResolvedValue({
      data: [
        {
          id: 's1', projectId: 'p1', name: 'postgres', namespace: 'ns', gatewayTemplateId: 't1',
          k8sGatewayName: 'str-postgres', k8sGatewayClass: 'gc', status: 'active',
          mode: 'shared', gatewayTemplateName: 'shared-envoy', routeCount: 4,
          ports: [
            { transport: 'TCP', port: 9000 },
            { transport: 'TCP', port: 6379 },
            { transport: 'UDP', port: 53 },
            { transport: 'TCP', port: 5432 },
          ],
        },
        {
          id: 's2', projectId: 'p1', name: 'cache', namespace: 'ns', gatewayTemplateId: 't2',
          k8sGatewayName: 'str-cache', k8sGatewayClass: 'gc2', status: 'pending',
          mode: 'dedicated', gatewayTemplateName: 'edge-l4', routeCount: 0, ports: [],
        },
        {
          id: 's3', projectId: 'p1', name: 'legacy', namespace: 'ns', gatewayTemplateId: 't3',
          k8sGatewayName: 'str-legacy', k8sGatewayClass: 'gc3', status: 'error',
        },
      ],
    }),
  },
}));
import { streamsApi } from '@/lib/api';

beforeEach(() => jest.clearAllMocks());

const rowOf = (name: string) => screen.getByRole('link', { name }).closest('tr') as HTMLElement;

test('shows entry point, route count and the first three ports (no IP column: the address is live Gateway status, shown on detail)', async () => {
  render(<StreamsPage />);
  await screen.findByRole('link', { name: 'postgres' });
  expect(screen.getByRole('columnheader', { name: 'Entry point' })).toBeInTheDocument();
  const pg = rowOf('postgres');
  expect(within(pg).getByText('Shared IP')).toBeInTheDocument();
  expect(within(pg).getByText('shared-envoy')).toBeInTheDocument();
  expect(screen.queryByRole('columnheader', { name: 'IP' })).not.toBeInTheDocument();
  expect(within(pg).getByText('4')).toBeInTheDocument();
  expect(screen.getByTestId('ports-s1')).toHaveTextContent('53/UDP, 5432/TCP, 6379/TCP +1');
  expect(within(rowOf('cache')).getByText('Dedicated IP')).toBeInTheDocument();
  expect(within(rowOf('cache')).getByText('0')).toBeInTheDocument();
  // A stream without the list fields (older backend) still renders, with dashes.
  expect(within(rowOf('legacy')).getAllByText('—').length).toBeGreaterThanOrEqual(4);
});

test('filters by status on the client', async () => {
  render(<StreamsPage />);
  await screen.findByRole('link', { name: 'postgres' });
  fireEvent.change(screen.getByLabelText('Status filter'), { target: { value: 'error' } });
  expect(screen.getByRole('link', { name: 'legacy' })).toBeInTheDocument();
  expect(screen.queryByRole('link', { name: 'postgres' })).not.toBeInTheDocument();
  expect(streamsApi.list).toHaveBeenCalledTimes(1);
});

test('debounces the name search', async () => {
  render(<StreamsPage />);
  await screen.findByRole('link', { name: 'postgres' });
  fireEvent.change(screen.getByLabelText('Search streams by name'), { target: { value: 'CAC' } });
  // Not applied until the debounce elapses.
  expect(screen.getByRole('link', { name: 'postgres' })).toBeInTheDocument();
  await waitFor(() => expect(screen.queryByRole('link', { name: 'postgres' })).not.toBeInTheDocument());
  expect(screen.getByRole('link', { name: 'cache' })).toBeInTheDocument();
  expect(streamsApi.list).toHaveBeenCalledTimes(1);
});

test('a filter that matches nothing offers Clear Filters', async () => {
  render(<StreamsPage />);
  await screen.findByRole('link', { name: 'postgres' });
  fireEvent.change(screen.getByLabelText('Search streams by name'), { target: { value: 'nope' } });
  expect(await screen.findByText('No streams found')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Clear Filters' }));
  expect(screen.getByRole('link', { name: 'postgres' })).toBeInTheDocument();
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx jest 'streams/page.test'`
Expected: FAIL — `Unable to find an accessible element with the role "columnheader" and name "Entry point"` and `Unable to find a label with the text of: Status filter`.

- [ ] **Step 3: Rewrite the list page**

Replace `src/app/projects/[projectId]/streams/page.tsx` with:

```tsx
'use client';

import { useState, useEffect } from 'react';
import { useParams } from 'next/navigation';
import Link from 'next/link';
import { Plus, Search, Waypoints } from 'lucide-react';
import { Button, Card, CardContent, Badge } from '@/components/ui';
import { streamsApi, projectsApi, permissionsApi } from '@/lib/api';
import type { Stream, Project, ProjectPermissions } from '@/types';
import { entrypointLabel } from '@/lib/utils/stream-entrypoint';
import { filterStreams, portsSummary, type StreamStatusFilter } from '@/lib/utils/stream-list';

const SEARCH_DEBOUNCE_MS = 300;

export default function StreamsPage() {
  const params = useParams();
  const projectId = params.projectId as string;

  const [project, setProject] = useState<Project | null>(null);
  const [streams, setStreams] = useState<Stream[]>([]);
  const [permissions, setPermissions] = useState<ProjectPermissions | null>(null);
  const [isLoading, setIsLoading] = useState(true);

  // Client-side filters (the list endpoint has no search/status params).
  const [statusFilter, setStatusFilter] = useState<StreamStatusFilter>('');
  const [searchInput, setSearchInput] = useState('');
  const [searchQuery, setSearchQuery] = useState('');

  useEffect(() => {
    loadData();
  }, [projectId]);

  useEffect(() => {
    const id = setTimeout(() => setSearchQuery(searchInput), SEARCH_DEBOUNCE_MS);
    return () => clearTimeout(id);
  }, [searchInput]);

  const loadData = async () => {
    try {
      const [projectData, streamsData, permsData] = await Promise.all([
        projectsApi.get(projectId),
        streamsApi.list(projectId),
        permissionsApi.getProjectPermissions(projectId),
      ]);
      setProject(projectData);
      setStreams(streamsData.data);
      setPermissions(permsData);
    } catch (error) {
      console.error('Failed to load data:', error);
    } finally {
      setIsLoading(false);
    }
  };

  const clearFilters = () => {
    setStatusFilter('');
    setSearchInput('');
    setSearchQuery('');
  };

  const getStatusBadge = (status: string) => {
    switch (status) {
      case 'active':
        return <Badge variant="success">Active</Badge>;
      case 'pending':
        return <Badge variant="warning">Pending</Badge>;
      case 'error':
        return <Badge variant="error">Error</Badge>;
      default:
        return <Badge>{status}</Badge>;
    }
  };

  const dash = <span className="text-gray-400">—</span>;
  const visibleStreams = filterStreams(streams, statusFilter, searchQuery);

  if (isLoading) {
    return (
      <div className="p-8">
        <div className="animate-pulse space-y-4">
          <div className="h-8 bg-gray-200 rounded w-1/4" />
          <div className="h-32 bg-gray-200 rounded" />
        </div>
      </div>
    );
  }

  return (
    <div className="p-8">
      <div className="flex items-center justify-between mb-6">
        <div>
          <h1 className="text-2xl font-bold text-gray-900">{project?.name}</h1>
          <p className="text-gray-600 mt-1">Manage L4 (TCP/UDP) streams</p>
        </div>
        {permissions?.canManageDomains && (
          <Link href={`/projects/${projectId}/streams/create`}>
            <Button>
              <Plus className="h-4 w-4 mr-2" />
              New Stream
            </Button>
          </Link>
        )}
      </div>

      {streams.length === 0 ? (
        <Card>
          <CardContent className="py-12 text-center">
            <Waypoints className="h-12 w-12 text-gray-400 mx-auto mb-4" />
            <h3 className="text-lg font-medium text-gray-900 mb-2">No streams yet</h3>
            <p className="text-gray-600 mb-4">
              {permissions?.canManageDomains
                ? 'Create your first stream to expose TCP/UDP services'
                : 'No streams have been created for this project yet'}
            </p>
            {permissions?.canManageDomains && (
              <Link href={`/projects/${projectId}/streams/create`}>
                <Button>
                  <Plus className="h-4 w-4 mr-2" />
                  Create Stream
                </Button>
              </Link>
            )}
          </CardContent>
        </Card>
      ) : (
        <>
          <div className="flex items-center gap-2 mb-4 max-w-3xl">
            <select
              aria-label="Status filter"
              value={statusFilter}
              onChange={(e) => setStatusFilter(e.target.value as StreamStatusFilter)}
              className="px-3 py-2 border border-gray-300 rounded-md bg-gray-50 text-sm focus:outline-none focus:ring-2 focus:ring-primary-500 focus:border-primary-500"
            >
              <option value="">All Status</option>
              <option value="active">Active</option>
              <option value="pending">Pending</option>
              <option value="error">Error</option>
            </select>
            <div className="relative flex-1">
              <Search className="absolute left-3 top-1/2 transform -translate-y-1/2 h-4 w-4 text-gray-400" />
              <input
                type="text"
                aria-label="Search streams by name"
                placeholder="Search by name..."
                value={searchInput}
                onChange={(e) => setSearchInput(e.target.value)}
                className="w-full pl-10 pr-4 py-2 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-primary-500 focus:border-primary-500"
              />
            </div>
          </div>

          {visibleStreams.length === 0 ? (
            <Card>
              <CardContent className="py-12 text-center">
                <h3 className="text-lg font-medium text-gray-900 mb-2">No streams found</h3>
                <p className="text-gray-600 mb-4">No streams match your filters.</p>
                <Button variant="secondary" onClick={clearFilters}>
                  Clear Filters
                </Button>
              </CardContent>
            </Card>
          ) : (
            <Card>
              <CardContent className="p-0">
                <table className="w-full text-sm">
                  <thead>
                    <tr className="border-b border-gray-200 text-left text-gray-500">
                      <th className="px-4 py-3 font-medium">Name</th>
                      <th className="px-4 py-3 font-medium">Namespace</th>
                      <th className="px-4 py-3 font-medium">Entry point</th>
                      <th className="px-4 py-3 font-medium">Routes</th>
                      <th className="px-4 py-3 font-medium">Ports</th>
                      <th className="px-4 py-3 font-medium">Status</th>
                    </tr>
                  </thead>
                  <tbody>
                    {visibleStreams.map((stream) => {
                      const ports = portsSummary(stream.ports);
                      return (
                        <tr key={stream.id} className="border-b border-gray-100 last:border-0 hover:bg-gray-50">
                          <td className="px-4 py-3">
                            <Link
                              href={`/projects/${projectId}/streams/${stream.id}`}
                              className="font-semibold text-gray-900 hover:text-primary-600"
                            >
                              {stream.name}
                            </Link>
                          </td>
                          <td className="px-4 py-3 text-gray-600">{stream.namespace}</td>
                          <td className="px-4 py-3">
                            {stream.mode ? (
                              <span className="inline-flex items-center gap-2">
                                <Badge variant={stream.mode === 'shared' ? 'info' : 'default'}>
                                  {entrypointLabel(stream.mode).badge}
                                </Badge>
                                {stream.gatewayTemplateName && (
                                  <span className="text-gray-600">{stream.gatewayTemplateName}</span>
                                )}
                              </span>
                            ) : (
                              dash
                            )}
                          </td>
                          <td className="px-4 py-3 text-gray-600">{stream.routeCount ?? dash}</td>
                          <td className="px-4 py-3 text-gray-600 font-mono" data-testid={`ports-${stream.id}`}>
                            {ports.shown.length === 0 ? (
                              dash
                            ) : (
                              <>
                                {ports.shown.join(', ')}
                                {ports.more > 0 && <span className="text-gray-500"> +{ports.more}</span>}
                              </>
                            )}
                          </td>
                          <td className="px-4 py-3">{getStatusBadge(stream.status)}</td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </CardContent>
            </Card>
          )}
        </>
      )}
    </div>
  );
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npx jest 'streams/page.test'`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add 'src/app/projects/[projectId]/streams/page.tsx' 'src/app/projects/[projectId]/streams/page.test.tsx'
git commit -m "feat(streams): entry point, routes and ports columns with client-side filter and search

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Stream detail — Entry point panel, Routes/Metrics/Manifests tabs

**Files:**
- Create: `src/components/streams/ManifestYaml.tsx`
- Create: `src/components/streams/StreamEntrypointPanel.tsx`, `src/components/streams/StreamEntrypointPanel.test.tsx`
- Create: `src/components/streams/StreamManifests.tsx`, `src/components/streams/StreamManifests.test.tsx`
- Modify (full rewrite): `src/app/projects/[projectId]/streams/[streamId]/page.tsx`
- Test: `src/app/projects/[projectId]/streams/[streamId]/page.test.tsx` (rewrite)

**Interfaces:**
- Consumes: `streamsApi.getEntrypoint`, `streamsApi.getYamls` (Task 1); Task 2 helpers; `l4StatusBadge`, `l4RouteKind` (Task 3); existing `L4MetricsCard`, `Accordion*`, `Tabs*`.
- Produces: `ManifestYaml({ yaml: string })` (the domain Manifests `<pre>`; reused in Task 8); `StreamEntrypointPanel({ projectId, streamId, refreshKey?: number })` — testids `entrypoint-panel`, `mine-ports`, `neighbor-ports`, `shared-with-list`, `reserved-ports`, `port-range`; buttons "Show who shares it" / "Show other ports"; `StreamManifests({ projectId, streamId })`.

- [ ] **Step 1: Write the failing component tests**

Create `src/components/streams/StreamEntrypointPanel.test.tsx`:

```tsx
import { render, screen, fireEvent } from '@testing-library/react';
import { StreamEntrypointPanel } from './StreamEntrypointPanel';

jest.mock('@/lib/api', () => ({ streamsApi: { getEntrypoint: jest.fn() } }));
import { streamsApi } from '@/lib/api';

const RESERVED = [
  { transport: 'TCP', port: 19000, reason: 'envoy_internal' },
  { transport: 'TCP', port: 19001, reason: 'envoy_internal' },
  { transport: 'TCP', port: 60000, reason: 'placeholder' },
];
const MINE = {
  transport: 'TCP', port: 9000,
  owner: { kind: 'stream_route', streamId: 's1', streamName: 'test-stream', routeId: 'r1', routeName: 'postgres', status: 'active' },
};
const SHARED = {
  mode: 'shared',
  templateId: 't1',
  templateName: 'shared-envoy',
  gatewayClass: 'shared-envoy-clusterip',
  address: '10.0.0.5',
  portRange: { min: 9000, max: 9100 },
  sharedWith: { streams: [{ id: 's2', name: 'cache' }], domains: [{ id: 'd1', name: 'api', hostname: 'api.example.com' }] },
  ports: [
    MINE,
    { transport: 'TCP', port: 9001, owner: { kind: 'stream_route', streamId: 's2', streamName: 'cache', routeId: 'r2', routeName: 'redis', status: 'active' } },
    { transport: 'TCP', port: 443, owner: { kind: 'domain', domainId: 'd1', domainName: 'api' } },
  ],
  reserved: [...RESERVED, { transport: 'TCP', port: 443, reason: 'template_listener' }],
};
const DEDICATED = {
  ...SHARED,
  mode: 'dedicated',
  address: '',
  sharedWith: { streams: [], domains: [] },
  ports: [MINE],
  reserved: RESERVED,
};

beforeEach(() => (streamsApi.getEntrypoint as jest.Mock).mockReset());

const renderPanel = () => render(<StreamEntrypointPanel projectId="p1" streamId="s1" />);

test('shared: IP, who shares it, my ports in full and neighbours collapsed', async () => {
  (streamsApi.getEntrypoint as jest.Mock).mockResolvedValue(SHARED);
  renderPanel();
  expect(await screen.findByText('Shared IP')).toBeInTheDocument();
  expect(streamsApi.getEntrypoint).toHaveBeenCalledWith('p1', 's1');
  const panel = screen.getByTestId('entrypoint-panel');
  expect(panel).toHaveTextContent("This stream is a named group of ports on the template's shared entry point.");
  expect(panel).toHaveTextContent('10.0.0.5');
  expect(panel).toHaveTextContent('shared-envoy-clusterip');
  expect(panel).toHaveTextContent('Also used by 1 stream and 1 domain.');
  expect(screen.getByTestId('mine-ports')).toHaveTextContent('9000/TCP');
  expect(screen.getByTestId('mine-ports')).toHaveTextContent('Active');
  expect(screen.getByRole('link', { name: 'postgres' })).toHaveAttribute('href', '/projects/p1/streams/s1/routes/r1');
  expect(panel).toHaveTextContent('2 ports used by other streams/domains.');
  expect(screen.queryByTestId('neighbor-ports')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Show other ports' }));
  expect(screen.getByTestId('neighbor-ports')).toHaveTextContent('443/TCP — Domain api');
  expect(screen.getByTestId('neighbor-ports')).toHaveTextContent('9001/TCP — Stream cache · route redis');
  fireEvent.click(screen.getByRole('button', { name: 'Show who shares it' }));
  expect(screen.getByRole('link', { name: 'cache' })).toHaveAttribute('href', '/projects/p1/streams/s2');
  expect(screen.getByRole('link', { name: 'api' })).toHaveAttribute('href', '/projects/p1/domains/d1');
  expect(screen.getByTestId('reserved-ports')).toHaveTextContent(
    '443/TCP (Template listener), 19000/TCP+UDP (Envoy internal), 19001/TCP+UDP (Envoy internal), 60000/TCP+UDP (Placeholder listener)'
  );
  expect(screen.getByTestId('port-range')).toHaveTextContent('Port range 9000–9100 · 99 of 101 free');
});

test('dedicated: own-IP wording, unassigned address, no sharing or neighbours', async () => {
  (streamsApi.getEntrypoint as jest.Mock).mockResolvedValue(DEDICATED);
  renderPanel();
  expect(await screen.findByText('Dedicated IP')).toBeInTheDocument();
  const panel = screen.getByTestId('entrypoint-panel');
  expect(panel).toHaveTextContent('This stream has its own entry point and IP.');
  expect(panel).toHaveTextContent('not assigned yet');
  expect(panel).not.toHaveTextContent('Also used by');
  expect(panel).not.toHaveTextContent('used by other streams/domains');
});

// Review Focus: backend fails closed (500) on a template lookup failure.
test('a failed entry point load shows the error and never a guessed mode', async () => {
  (streamsApi.getEntrypoint as jest.Mock).mockRejectedValue({ response: { status: 500, data: { error: 'template lookup failed' } } });
  renderPanel();
  expect(await screen.findByRole('alert')).toHaveTextContent('Could not load the entry point: template lookup failed');
  expect(screen.queryByText('Dedicated IP')).not.toBeInTheDocument();
  expect(screen.queryByText('Shared IP')).not.toBeInTheDocument();
});

// Review Focus: response omits the platform reserved ports.
test('reserved platform ports are shown even if the response omits them', async () => {
  (streamsApi.getEntrypoint as jest.Mock).mockResolvedValue({ ...DEDICATED, reserved: [] });
  renderPanel();
  await screen.findByText('Dedicated IP');
  expect(screen.getByTestId('reserved-ports')).toHaveTextContent(
    '19000/TCP+UDP (Envoy internal), 19001/TCP+UDP (Envoy internal), 60000/TCP+UDP (Placeholder listener)'
  );
});
```

Create `src/components/streams/StreamManifests.test.tsx`:

```tsx
import { render, screen, fireEvent } from '@testing-library/react';
import { StreamManifests } from './StreamManifests';

jest.mock('@/lib/api', () => ({ streamsApi: { getYamls: jest.fn() } }));
import { streamsApi } from '@/lib/api';

beforeEach(() => (streamsApi.getYamls as jest.Mock).mockReset());

test('shows the Gateway open and each route collapsed with its BackendTrafficPolicy', async () => {
  (streamsApi.getYamls as jest.Mock).mockResolvedValue({
    gatewayYaml: 'kind: Gateway\nname: str-test',
    routes: [
      { routeId: 'r1', name: 'postgres', protocol: 'tcp', routeYaml: 'kind: TCPRoute\nname: postgres', backendTrafficPolicyYaml: 'kind: BackendTrafficPolicy\nname: postgres' },
      { routeId: 'r2', name: 'dns', protocol: 'udp', routeYaml: 'kind: UDPRoute\nname: dns', backendTrafficPolicyYaml: '' },
    ],
  });
  render(<StreamManifests projectId="p1" streamId="s1" />);
  expect(await screen.findByText(/kind: Gateway/)).toBeInTheDocument();
  expect(streamsApi.getYamls).toHaveBeenCalledWith('p1', 's1');
  expect(screen.queryByText(/kind: TCPRoute/)).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'TCPRoute · postgres' }));
  expect(screen.getByText(/kind: TCPRoute/)).toBeInTheDocument();
  expect(screen.getByText(/kind: BackendTrafficPolicy/)).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'UDPRoute · dns' }));
  expect(screen.getByText(/kind: UDPRoute/)).toBeInTheDocument();
  expect(screen.getAllByText('BackendTrafficPolicy')).toHaveLength(1);
});

test('no live routes explains the placeholder Gateway', async () => {
  (streamsApi.getYamls as jest.Mock).mockResolvedValue({ gatewayYaml: 'kind: Gateway', routes: [] });
  render(<StreamManifests projectId="p1" streamId="s1" />);
  expect(await screen.findByText(/only the placeholder listener/)).toBeInTheDocument();
});

test('shows the load error', async () => {
  (streamsApi.getYamls as jest.Mock).mockRejectedValue({ response: { data: { error: 'boom' } } });
  render(<StreamManifests projectId="p1" streamId="s1" />);
  expect(await screen.findByRole('alert')).toHaveTextContent('boom');
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `npx jest src/components/streams/StreamEntrypointPanel.test.tsx src/components/streams/StreamManifests.test.tsx`
Expected: FAIL — `Cannot find module './StreamEntrypointPanel'` and `Cannot find module './StreamManifests'`.

- [ ] **Step 3: Create the components**

Create `src/components/streams/ManifestYaml.tsx`:

```tsx
// Read-only YAML block: the exact <pre> the domain Manifests tab uses
// (domains/[domainId]/page.tsx), shared by the stream and L4 route pages.
export function ManifestYaml({ yaml }: { yaml: string }) {
  return <pre className="bg-gray-900 text-gray-100 p-4 rounded-lg overflow-x-auto text-sm">{yaml}</pre>;
}
```

Create `src/components/streams/StreamEntrypointPanel.tsx`:

```tsx
'use client';

// Stream detail "Entry point" panel: mode, IP, who shares the entry point, and the
// port map (this stream's ports in full, neighbours collapsed, reserved ports,
// range and free count). Replaces the old "Load Balancer Address"/"Ports in use" cards.

import { useEffect, useState } from 'react';
import Link from 'next/link';
import { AlertTriangle } from 'lucide-react';
import { Badge, Card, CardContent } from '@/components/ui';
import { streamsApi } from '@/lib/api';
import type { StreamEntrypoint } from '@/types';
import {
  entrypointLabel,
  freePortCount,
  ownerLabel,
  portLabel,
  rangeSize,
  reservedSummary,
  sharedWithSummary,
  splitPorts,
  withDefaultReserved,
} from '@/lib/utils/stream-entrypoint';
import { l4StatusBadge } from '@/lib/utils/l4route';

interface Props {
  projectId: string;
  streamId: string;
  /** Bump to re-fetch (e.g. after a deploy changed a route's status). */
  refreshKey?: number;
}

export function StreamEntrypointPanel({ projectId, streamId, refreshKey = 0 }: Props) {
  const [entrypoint, setEntrypoint] = useState<StreamEntrypoint | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [showShared, setShowShared] = useState(false);
  const [showNeighbors, setShowNeighbors] = useState(false);

  useEffect(() => {
    let cancelled = false;
    streamsApi
      .getEntrypoint(projectId, streamId)
      .then((data) => {
        if (cancelled) return;
        setEntrypoint(data);
        setError(null);
      })
      .catch((e: unknown) => {
        if (cancelled) return;
        const err = e as { response?: { data?: { error?: string } } };
        // Never guess a mode: show the failure instead of a partial panel.
        setEntrypoint(null);
        setError(err.response?.data?.error || 'request failed');
      });
    return () => {
      cancelled = true;
    };
  }, [projectId, streamId, refreshKey]);

  if (error) {
    return (
      <Card>
        <CardContent className="py-4">
          <div data-testid="entrypoint-panel" role="alert" className="flex items-start gap-2 text-sm text-red-700">
            <AlertTriangle className="h-4 w-4 flex-shrink-0 mt-0.5" />
            <p>Could not load the entry point: {error}</p>
          </div>
        </CardContent>
      </Card>
    );
  }

  if (!entrypoint) {
    return (
      <Card>
        <CardContent className="py-4">
          <div data-testid="entrypoint-panel" className="h-16 animate-pulse bg-gray-100 rounded" />
        </CardContent>
      </Card>
    );
  }

  const label = entrypointLabel(entrypoint.mode);
  const { mine, neighbors } = splitPorts(entrypoint.ports, streamId);
  const reserved = withDefaultReserved(entrypoint.reserved);
  const range = entrypoint.portRange;
  const free = freePortCount(range, entrypoint.ports, reserved);
  const { streams: sharedStreams, domains: sharedDomains } = entrypoint.sharedWith;

  return (
    <Card>
      <CardContent className="py-4">
        <div data-testid="entrypoint-panel" className="space-y-4 text-sm">
          <div>
            <div className="flex items-center gap-2">
              <h3 className="text-sm font-medium text-gray-500">Entry point</h3>
              <Badge variant={entrypoint.mode === 'shared' ? 'info' : 'default'}>{label.badge}</Badge>
            </div>
            <p className="mt-1 text-gray-700">{label.explanation}</p>
          </div>

          <dl className="grid grid-cols-1 md:grid-cols-3 gap-2">
            <div>
              <dt className="text-gray-500">IP</dt>
              <dd className="font-mono text-gray-900">
                {entrypoint.address || <span className="font-sans text-gray-400">not assigned yet</span>}
              </dd>
            </div>
            <div>
              <dt className="text-gray-500">Gateway class</dt>
              <dd className="font-mono text-gray-900">{entrypoint.gatewayClass}</dd>
            </div>
            <div>
              <dt className="text-gray-500">Template</dt>
              <dd className="text-gray-900">{entrypoint.templateName}</dd>
            </div>
          </dl>

          {entrypoint.mode === 'shared' && (
            <div>
              <p className="text-gray-700">
                Also used by {sharedWithSummary(entrypoint.sharedWith)}.{' '}
                {sharedStreams.length + sharedDomains.length > 0 && (
                  <button
                    type="button"
                    className="text-primary-600 hover:underline"
                    onClick={() => setShowShared((v) => !v)}
                  >
                    {showShared ? 'Hide who shares it' : 'Show who shares it'}
                  </button>
                )}
              </p>
              {showShared && (
                <ul data-testid="shared-with-list" className="mt-1 list-disc pl-5">
                  {sharedStreams.map((s) => (
                    <li key={`s-${s.id}`}>
                      Stream{' '}
                      <Link href={`/projects/${projectId}/streams/${s.id}`} className="text-primary-600 hover:underline">
                        {s.name}
                      </Link>
                    </li>
                  ))}
                  {sharedDomains.map((d) => (
                    <li key={`d-${d.id}`}>
                      Domain{' '}
                      <Link href={`/projects/${projectId}/domains/${d.id}`} className="text-primary-600 hover:underline">
                        {d.name}
                      </Link>{' '}
                      ({d.hostname})
                    </li>
                  ))}
                </ul>
              )}
            </div>
          )}

          <div>
            <h4 className="font-medium text-gray-900">This stream&apos;s ports</h4>
            {mine.length === 0 ? (
              <p className="text-gray-500">No ports yet. Each route you add opens one port.</p>
            ) : (
              <ul data-testid="mine-ports" className="mt-1 space-y-1">
                {mine.map((p) => {
                  const badge = l4StatusBadge(p.owner.status || '');
                  return (
                    <li key={`${p.transport}-${p.port}`} className="flex items-center gap-2">
                      <span className="font-mono">{portLabel(p)}</span>
                      <span className="text-gray-400">→</span>
                      <Link
                        href={`/projects/${projectId}/streams/${streamId}/routes/${p.owner.routeId}`}
                        className="text-primary-600 hover:underline"
                      >
                        {p.owner.routeName || p.owner.routeId}
                      </Link>
                      {p.owner.status && <Badge variant={badge.variant}>{badge.label}</Badge>}
                    </li>
                  );
                })}
              </ul>
            )}
          </div>

          {neighbors.length > 0 && (
            <div>
              <p className="text-gray-700">
                {neighbors.length} {neighbors.length === 1 ? 'port' : 'ports'} used by other streams/domains.{' '}
                <button
                  type="button"
                  className="text-primary-600 hover:underline"
                  onClick={() => setShowNeighbors((v) => !v)}
                >
                  {showNeighbors ? 'Hide other ports' : 'Show other ports'}
                </button>
              </p>
              {showNeighbors && (
                <ul data-testid="neighbor-ports" className="mt-1 space-y-1">
                  {neighbors.map((p) => (
                    <li key={`${p.transport}-${p.port}-${p.owner.routeId ?? p.owner.domainId ?? ''}`}>
                      <span className="font-mono">{portLabel(p)}</span>{' '}
                      <span className="text-gray-600">— {ownerLabel(p.owner)}</span>
                    </li>
                  ))}
                </ul>
              )}
            </div>
          )}

          <p data-testid="reserved-ports" className="text-xs text-gray-500">
            Reserved: {reservedSummary(reserved).join(', ')}
          </p>
          <p data-testid="port-range" className="text-xs text-gray-500">
            {range && free != null
              ? `Port range ${range.min}–${range.max} · ${free} of ${rangeSize(range)} free`
              : 'This template has no TCP/UDP port range.'}
          </p>
        </div>
      </CardContent>
    </Card>
  );
}
```

Create `src/components/streams/StreamManifests.tsx`:

```tsx
'use client';

// Stream Manifests tab: the stream Gateway plus each live route's TCPRoute/UDPRoute
// and BackendTrafficPolicy, collapsible and read-only.

import { useEffect, useState } from 'react';
import { Accordion, AccordionItem, AccordionTrigger, AccordionContent } from '@/components/ui';
import { streamsApi } from '@/lib/api';
import type { StreamYAMLs } from '@/types';
import { l4RouteKind } from '@/lib/utils/l4route';
import { ManifestYaml } from './ManifestYaml';

interface Props {
  projectId: string;
  streamId: string;
}

export function StreamManifests({ projectId, streamId }: Props) {
  const [data, setData] = useState<StreamYAMLs | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    streamsApi
      .getYamls(projectId, streamId)
      .then((d) => {
        if (!cancelled) setData(d);
      })
      .catch((e: unknown) => {
        if (cancelled) return;
        const err = e as { response?: { data?: { error?: string } } };
        setError(err.response?.data?.error || 'Failed to load manifests');
      });
    return () => {
      cancelled = true;
    };
  }, [projectId, streamId]);

  if (error) {
    return (
      <p role="alert" className="text-sm text-red-700">
        {error}
      </p>
    );
  }
  if (!data) return <p className="text-sm text-gray-500">Loading manifests…</p>;

  return (
    <div className="space-y-3">
      <Accordion type="multiple" defaultValue={['gateway']}>
        <AccordionItem value="gateway">
          <AccordionTrigger value="gateway">Gateway</AccordionTrigger>
          <AccordionContent value="gateway">
            <ManifestYaml yaml={data.gatewayYaml} />
          </AccordionContent>
        </AccordionItem>
        {data.routes.map((r) => {
          const key = `route-${r.routeId}`;
          return (
            <AccordionItem key={key} value={key}>
              <AccordionTrigger value={key}>{`${l4RouteKind(r.protocol)} · ${r.name}`}</AccordionTrigger>
              <AccordionContent value={key}>
                <div className="space-y-4">
                  <ManifestYaml yaml={r.routeYaml} />
                  {r.backendTrafficPolicyYaml && (
                    <div>
                      <h4 className="text-sm font-medium text-gray-700 mb-2">BackendTrafficPolicy</h4>
                      <ManifestYaml yaml={r.backendTrafficPolicyYaml} />
                    </div>
                  )}
                </div>
              </AccordionContent>
            </AccordionItem>
          );
        })}
      </Accordion>
      {data.routes.length === 0 && (
        <p className="text-sm text-gray-500">No live routes yet, so the Gateway has only the placeholder listener.</p>
      )}
    </div>
  );
}
```

- [ ] **Step 4: Run component tests to verify they pass**

Run: `npx jest src/components/streams/StreamEntrypointPanel.test.tsx src/components/streams/StreamManifests.test.tsx`
Expected: PASS (7 tests).

- [ ] **Step 5: Write the failing page test**

Replace `src/app/projects/[projectId]/streams/[streamId]/page.test.tsx` with:

```tsx
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import StreamDetailPage from './page';

jest.mock('next/navigation', () => ({
  useParams: () => ({ projectId: 'p1', streamId: 's1' }),
  useRouter: () => ({ push: jest.fn() }),
}));
jest.mock('@/lib/api', () => ({
  streamsApi: {
    get: jest.fn().mockResolvedValue({
      id: 's1', projectId: 'p1', name: 'pg', namespace: 'ns', status: 'active',
      k8sGatewayName: 'gw', k8sGatewayClass: 'gc',
    }),
    listRoutes: jest.fn().mockResolvedValue({
      data: [
        { id: 'r1', name: 'db', protocol: 'tcp', status: 'active', config: { listenerPort: 5432 }, team: { id: 't1', name: 'Team A' } },
        { id: 'r2', name: 'dns', protocol: 'udp', status: 'approved', config: { listenerPort: 53 } },
      ],
    }),
    delete: jest.fn(),
    deployRoute: jest.fn().mockResolvedValue({}),
    getEntrypoint: jest.fn().mockResolvedValue({
      mode: 'dedicated', templateId: 't1', templateName: 'edge-l4', gatewayClass: 'gc', address: '10.0.0.9',
      portRange: { min: 1, max: 65535 }, sharedWith: { streams: [], domains: [] }, ports: [], reserved: [],
    }),
    getYamls: jest.fn().mockResolvedValue({ gatewayYaml: 'kind: Gateway', routes: [] }),
    getMetrics: jest.fn().mockRejectedValue({ response: { status: 400, data: { error: 'not configured' } } }),
  },
  permissionsApi: {
    getProjectPermissions: jest.fn().mockResolvedValue({ canManageDomains: true, canCreateRoutes: true }),
  },
}));
import { streamsApi } from '@/lib/api';

beforeEach(() => jest.clearAllMocks());

test('replaces the LB address and ports cards with the Entry point panel', async () => {
  render(<StreamDetailPage />);
  await waitFor(() => expect(screen.getByTestId('entrypoint-panel')).toHaveTextContent('Dedicated IP'));
  expect(screen.getByTestId('entrypoint-panel')).toHaveTextContent('10.0.0.9');
  expect(streamsApi.getEntrypoint).toHaveBeenCalledWith('p1', 's1');
  expect(screen.queryByText('Load Balancer Address')).not.toBeInTheDocument();
  expect(screen.queryByTestId('ports-in-use')).not.toBeInTheDocument();
});

test('Routes tab links each route to its detail page, shows the owner team and keeps quick Deploy', async () => {
  render(<StreamDetailPage />);
  const link = await screen.findByRole('link', { name: 'db' });
  expect(link).toHaveAttribute('href', '/projects/p1/streams/s1/routes/r1');
  expect(screen.getByText('Team A')).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /Edit/ })).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Deploy' }));
  await waitFor(() => expect(streamsApi.deployRoute).toHaveBeenCalledWith('p1', 's1', 'r2'));
  await waitFor(() => expect(streamsApi.getEntrypoint).toHaveBeenCalledTimes(2));
});

test('Metrics and Manifests are tabs that load on open', async () => {
  render(<StreamDetailPage />);
  await screen.findByRole('link', { name: 'db' });
  expect(streamsApi.getYamls).not.toHaveBeenCalled();
  expect(streamsApi.getMetrics).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole('tab', { name: 'Manifests' }));
  expect(await screen.findByText('kind: Gateway')).toBeInTheDocument();
  expect(streamsApi.getYamls).toHaveBeenCalledWith('p1', 's1');
  fireEvent.click(screen.getByRole('tab', { name: 'Metrics' }));
  await waitFor(() => expect(streamsApi.getMetrics).toHaveBeenCalled());
});

test('surfaces the 409 remove-routes-first error on delete', async () => {
  (streamsApi.delete as jest.Mock).mockRejectedValue({
    response: { data: { error: 'stream still has routes; remove them first' } },
  });
  render(<StreamDetailPage />);
  fireEvent.click(await screen.findByRole('button', { name: /Delete/ }));
  const buttons = await screen.findAllByRole('button', { name: 'Delete' });
  fireEvent.click(buttons[buttons.length - 1]);
  await waitFor(() => expect(screen.getByText(/remove them first/i)).toBeInTheDocument());
});
```

- [ ] **Step 6: Run the page test to verify it fails**

Run: `npx jest 'streams/\[streamId\]/page'`
Expected: FAIL — `Unable to find an element by: [data-testid="entrypoint-panel"]`, the `db` link not found, and no `Manifests` tab. The delete test still passes.

- [ ] **Step 7: Rewrite the stream detail page**

Replace `src/app/projects/[projectId]/streams/[streamId]/page.tsx` with:

```tsx
'use client';

import { useState, useEffect } from 'react';
import { useParams, useRouter } from 'next/navigation';
import Link from 'next/link';
import { ArrowLeft, Trash2, AlertTriangle, Plus, Rocket } from 'lucide-react';
import { Button, Card, CardContent, Badge, Modal, Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui';
import { streamsApi, permissionsApi } from '@/lib/api';
import { L4MetricsCard } from '@/components/metrics/L4MetricsCard';
import { StreamEntrypointPanel } from '@/components/streams/StreamEntrypointPanel';
import { StreamManifests } from '@/components/streams/StreamManifests';
import { l4StatusBadge } from '@/lib/utils/l4route';
import type { Stream, StreamRoute, ProjectPermissions } from '@/types';

export default function StreamDetailPage() {
  const params = useParams();
  const router = useRouter();
  const projectId = params.projectId as string;
  const streamId = params.streamId as string;

  const [stream, setStream] = useState<Stream | null>(null);
  const [routes, setRoutes] = useState<StreamRoute[]>([]);
  const [permissions, setPermissions] = useState<ProjectPermissions | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);

  const [showDeleteModal, setShowDeleteModal] = useState(false);
  const [isDeleting, setIsDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);
  const [deployingRouteId, setDeployingRouteId] = useState<string | null>(null);
  const [routeActionError, setRouteActionError] = useState<string | null>(null);
  // Bumped after a deploy so the Entry point panel re-reads port statuses.
  const [refreshKey, setRefreshKey] = useState(0);

  useEffect(() => {
    loadData();
  }, [projectId, streamId]);

  const loadData = async () => {
    try {
      const [streamData, routesData, permsData] = await Promise.all([
        streamsApi.get(projectId, streamId),
        streamsApi.listRoutes(projectId, streamId).catch(() => ({ data: [] as StreamRoute[] })),
        permissionsApi.getProjectPermissions(projectId),
      ]);
      setStream(streamData);
      setRoutes(routesData.data || []);
      setPermissions(permsData);
    } catch (error: unknown) {
      const err = error as { response?: { data?: { error?: string } } };
      console.error('Failed to load stream:', error);
      setLoadError(err.response?.data?.error || 'Failed to load stream');
    } finally {
      setIsLoading(false);
    }
  };

  const handleDelete = async () => {
    setIsDeleting(true);
    setDeleteError(null);
    try {
      await streamsApi.delete(projectId, streamId);
      router.push(`/projects/${projectId}/streams`);
    } catch (error: unknown) {
      const err = error as { response?: { data?: { error?: string } } };
      // e.g. 409 "remove routes first"
      setDeleteError(err.response?.data?.error || 'Failed to delete stream');
      setShowDeleteModal(false);
    } finally {
      setIsDeleting(false);
    }
  };

  const handleDeployRoute = async (routeId: string) => {
    setDeployingRouteId(routeId);
    setRouteActionError(null);
    try {
      await streamsApi.deployRoute(projectId, streamId, routeId);
      await loadData();
      setRefreshKey((k) => k + 1);
    } catch (error: unknown) {
      const err = error as { response?: { data?: { error?: string } } };
      setRouteActionError(err.response?.data?.error || 'Failed to deploy route');
    } finally {
      setDeployingRouteId(null);
    }
  };

  const canDeployRoute = (route: StreamRoute) => route.status === 'approved' || route.status === 'pending_deploy';

  const statusBadge = (status: string) => {
    const b = l4StatusBadge(status);
    return <Badge variant={b.variant}>{b.label}</Badge>;
  };

  if (isLoading) {
    return (
      <div className="p-8">
        <div className="animate-pulse space-y-4">
          <div className="h-8 bg-gray-200 rounded w-1/4" />
          <div className="h-32 bg-gray-200 rounded" />
        </div>
      </div>
    );
  }

  if (!stream) {
    return (
      <div className="p-8">
        <Link
          href={`/projects/${projectId}/streams`}
          className="flex items-center gap-2 text-gray-600 hover:text-gray-900 mb-4"
        >
          <ArrowLeft className="h-4 w-4" />
          Back to Streams
        </Link>
        <p className="text-red-700">{loadError || 'Stream not found'}</p>
      </div>
    );
  }

  return (
    <div className="p-8">
      <Link
        href={`/projects/${projectId}/streams`}
        className="flex items-center gap-2 text-gray-600 hover:text-gray-900 mb-4"
      >
        <ArrowLeft className="h-4 w-4" />
        Back to Streams
      </Link>

      <div className="flex items-center justify-between mb-6">
        <div>
          <div className="flex items-center gap-3">
            <h1 className="text-2xl font-bold text-gray-900">{stream.name}</h1>
            {statusBadge(stream.status)}
          </div>
          <p className="text-gray-600 mt-1">
            Namespace: {stream.namespace}
            {stream.k8sGatewayName && ` · Gateway: ${stream.k8sGatewayName}`}
            {stream.k8sGatewayClass && ` · Class: ${stream.k8sGatewayClass}`}
          </p>
        </div>
        {permissions?.canManageDomains && (
          <Button variant="danger" onClick={() => setShowDeleteModal(true)}>
            <Trash2 className="h-4 w-4 mr-2" />
            Delete
          </Button>
        )}
      </div>

      {stream.statusMessage && stream.status === 'error' && (
        <div className="mb-4 p-4 rounded-lg bg-red-50 border border-red-200">
          <div className="flex items-start gap-3">
            <AlertTriangle className="h-5 w-5 text-red-600 flex-shrink-0 mt-0.5" />
            <p className="text-sm text-red-700">{stream.statusMessage}</p>
          </div>
        </div>
      )}

      {deleteError && (
        <div className="mb-4 p-4 rounded-lg bg-red-50 border border-red-200">
          <div className="flex items-start gap-3">
            <AlertTriangle className="h-5 w-5 text-red-600 flex-shrink-0 mt-0.5" />
            <div>
              <h4 className="font-medium text-red-800">Failed to delete stream</h4>
              <p className="mt-1 text-sm text-red-700">{deleteError}</p>
            </div>
          </div>
        </div>
      )}

      <div className="mb-6">
        <StreamEntrypointPanel projectId={projectId} streamId={streamId} refreshKey={refreshKey} />
      </div>

      <Card>
        <CardContent className="pt-6">
          <Tabs defaultValue="routes">
            <TabsList>
              <TabsTrigger value="routes">
                Routes
                {routes.length > 0 && <Badge variant="default" className="ml-2 text-xs">{routes.length}</Badge>}
              </TabsTrigger>
              <TabsTrigger value="metrics">Metrics</TabsTrigger>
              <TabsTrigger value="manifests">Manifests</TabsTrigger>
            </TabsList>

            <TabsContent value="routes">
              {permissions?.canCreateRoutes && (
                <div className="flex justify-end mb-4">
                  <Link href={`/projects/${projectId}/streams/${streamId}/routes/create`}>
                    <Button size="sm">
                      <Plus className="h-4 w-4 mr-1" />
                      Add route
                    </Button>
                  </Link>
                </div>
              )}
              {routeActionError && <p className="mb-3 text-sm text-red-700">{routeActionError}</p>}
              {routes.length === 0 ? (
                <p className="text-sm text-gray-500">No routes yet.</p>
              ) : (
                <table className="w-full text-sm">
                  <thead>
                    <tr className="border-b border-gray-200 text-left text-gray-500">
                      <th className="py-2 pr-4 font-medium">Name</th>
                      <th className="py-2 pr-4 font-medium">Protocol</th>
                      <th className="py-2 pr-4 font-medium">Port</th>
                      <th className="py-2 pr-4 font-medium">Owner team</th>
                      <th className="py-2 pr-4 font-medium">Status</th>
                      <th className="py-2 font-medium text-right">Actions</th>
                    </tr>
                  </thead>
                  <tbody>
                    {routes.map((route) => (
                      <tr key={route.id} className="border-b border-gray-100 last:border-0">
                        <td className="py-2 pr-4">
                          <Link
                            href={`/projects/${projectId}/streams/${streamId}/routes/${route.id}`}
                            className="font-medium text-gray-900 hover:text-primary-600"
                          >
                            {route.name}
                          </Link>
                        </td>
                        <td className="py-2 pr-4 text-gray-600">{(route.protocol || '').toUpperCase()}</td>
                        <td className="py-2 pr-4 text-gray-600 font-mono">{route.config?.listenerPort ?? '—'}</td>
                        <td className="py-2 pr-4 text-gray-600">{route.team?.name || '—'}</td>
                        <td className="py-2 pr-4">{statusBadge(route.status)}</td>
                        <td className="py-2">
                          <div className="flex items-center justify-end gap-2">
                            {canDeployRoute(route) && (
                              <Button
                                size="sm"
                                onClick={() => handleDeployRoute(route.id)}
                                disabled={deployingRouteId === route.id}
                              >
                                <Rocket className="h-4 w-4 mr-1" />
                                {deployingRouteId === route.id ? 'Deploying...' : 'Deploy'}
                              </Button>
                            )}
                          </div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </TabsContent>

            <TabsContent value="metrics">
              <L4MetricsCard projectId={projectId} streamId={streamId} />
            </TabsContent>

            <TabsContent value="manifests">
              <StreamManifests projectId={projectId} streamId={streamId} />
            </TabsContent>
          </Tabs>
        </CardContent>
      </Card>

      <Modal isOpen={showDeleteModal} onClose={() => setShowDeleteModal(false)} title="Delete Stream">
        <div className="space-y-4">
          <p className="text-gray-600">
            Are you sure you want to delete the stream <span className="font-semibold">{stream.name}</span>?
          </p>
          <p className="text-sm text-red-600">
            This will remove the stream&apos;s Gateway from your Kubernetes cluster. A stream that still has routes cannot be deleted.
          </p>
          <div className="flex justify-end gap-3 pt-4">
            <Button variant="secondary" onClick={() => setShowDeleteModal(false)}>Cancel</Button>
            <Button variant="danger" onClick={handleDelete} isLoading={isDeleting}>Delete</Button>
          </div>
        </div>
      </Modal>
    </div>
  );
}
```

- [ ] **Step 8: Run tests to verify they pass**

Run: `npx jest 'streams/\[streamId\]/page' src/components/streams src/components/metrics/L4MetricsCard.test.tsx`
Expected: PASS (page 4 tests; components; L4MetricsCard unchanged).

- [ ] **Step 9: Commit**

```bash
git add src/components/streams/ManifestYaml.tsx src/components/streams/StreamEntrypointPanel.tsx src/components/streams/StreamEntrypointPanel.test.tsx src/components/streams/StreamManifests.tsx src/components/streams/StreamManifests.test.tsx 'src/app/projects/[projectId]/streams/[streamId]/page.tsx' 'src/app/projects/[projectId]/streams/[streamId]/page.test.tsx'
git commit -m "feat(streams): entry point panel and Routes/Metrics/Manifests tabs on stream detail

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: L4 route form → tabs with Preview

**Files:**
- Create: `src/components/streams/L4ManifestDiff.tsx`
- Modify (full rewrite): `src/components/streams/L4RouteForm.tsx`
- Test: `src/components/streams/L4RouteForm.test.tsx` (rewrite)

**Interfaces:**
- Consumes: `streamsApi.previewCreateRoute`, `previewUpdateRoute` (Task 1); `L4_FORM_TABS`, `L4FormTab`, `tabErrors`, `errorsForTab`, `mergeTabErrors`, `validateL4Form`, `l4RouteKind`, `diffMode` (Task 3); existing `YamlDiffViewer`, `Tabs*`.
- Produces: `L4ManifestDiff({ routeKind: string; current?: L4ManifestPair; proposed?: L4ManifestPair })`, `type L4ManifestPair = { route?: string; btp?: string }` (reused by Task 8's delete modal); sections `data-testid="manifest-diff-route"` / `"manifest-diff-btp"` with `<h3>` titles `TCPRoute`/`UDPRoute` and `BackendTrafficPolicy`. Form: tabs "Basic Info", "Backends", "Traffic Policy", "Preview"; error badges `data-testid="tab-error-<tab>"`; `data-testid="preview-error"`; edit mode returns to `/projects/:p/streams/:s/routes/:r` (link "Back to Route").

- [ ] **Step 1: Write the failing test**

Replace `src/components/streams/L4RouteForm.test.tsx` with:

```tsx
import { act, render, screen, waitFor, fireEvent } from '@testing-library/react';
import { L4RouteForm } from './L4RouteForm';

const push = jest.fn();
jest.mock('next/navigation', () => ({ useRouter: () => ({ push }) }));
jest.mock('@/lib/api', () => ({
  streamsApi: {
    get: jest.fn(),
    listRoutes: jest.fn(),
    createRoute: jest.fn(),
    updateRoute: jest.fn(),
    getRoute: jest.fn(),
    previewCreateRoute: jest.fn(),
    previewUpdateRoute: jest.fn(),
  },
  domainTemplatesApi: { get: jest.fn() },
  projectTeamsApi: { listMyTeams: jest.fn() },
  projectNamespacesApi: { list: jest.fn() },
  kubernetesApi: { listServices: jest.fn() },
}));
import { streamsApi, domainTemplatesApi, projectTeamsApi, projectNamespacesApi, kubernetesApi } from '@/lib/api';

const ROUTE = {
  id: 'r1', name: 'db', description: 'pg', teamId: 't1', protocol: 'tcp', status: 'active',
  config: { matches: [], listenerPort: 5432, backends: [{ type: 'kubernetes', namespace: 'ns', service: 'pg', port: 5432, weight: 100 }] },
};

beforeEach(() => {
  jest.resetAllMocks();
  (streamsApi.get as jest.Mock).mockResolvedValue({ id: 's1', name: 'pg-stream', gatewayTemplateId: 'gt1' });
  (streamsApi.listRoutes as jest.Mock).mockResolvedValue({
    data: [{ id: 'r1', name: 'db', protocol: 'tcp', status: 'active', config: { listenerPort: 5432 } }],
  });
  (streamsApi.getRoute as jest.Mock).mockResolvedValue(ROUTE);
  (streamsApi.previewCreateRoute as jest.Mock).mockResolvedValue({ proposedYaml: 'kind: TCPRoute\nname: web' });
  (streamsApi.previewUpdateRoute as jest.Mock).mockResolvedValue({
    currentYaml: 'kind: TCPRoute\nport: 5432',
    proposedYaml: 'kind: TCPRoute\nport: 5433',
  });
  (domainTemplatesApi.get as jest.Mock).mockResolvedValue({
    id: 'gt1',
    listeners: [{ name: 'l4', protocol: 'TCP', portRangeMin: 5000, portRangeMax: 7000 }],
  });
  (projectTeamsApi.listMyTeams as jest.Mock).mockResolvedValue([{ team: { id: 't1', name: 'Team' } }]);
  (projectNamespacesApi.list as jest.Mock).mockResolvedValue([{ namespace: 'ns', referenceGrantCreated: true }]);
  (kubernetesApi.listServices as jest.Mock).mockResolvedValue([
    { name: 'pg', namespace: 'ns', ports: [{ name: 'p', port: 5432, protocol: 'TCP' }] },
  ]);
});

const tab = (name: RegExp) => fireEvent.click(screen.getByRole('tab', { name }));

const renderCreate = async () => {
  render(<L4RouteForm projectId="p1" streamId="s1" />);
  await screen.findByLabelText('Name');
  await waitFor(() => expect(kubernetesApi.listServices).toHaveBeenCalledWith('p1', 'ns'));
};

// Fills Basic Info, then picks the backend service; ends on the Backends tab.
const fillValid = async (port: string) => {
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'web' } });
  fireEvent.change(screen.getByLabelText('Listener Port'), { target: { value: port } });
  tab(/^Backends/);
  await screen.findByRole('option', { name: 'pg' });
  fireEvent.change(screen.getByLabelText('Service'), { target: { value: 'pg' } });
};

const submitFromPreview = async (label = 'Create Route') => {
  tab(/^Preview/);
  await waitFor(() => expect(screen.queryByText('Generating preview…')).not.toBeInTheDocument());
  fireEvent.click(screen.getByRole('button', { name: label }));
};

test('lays the form out in four tabs and starts on Basic Info', async () => {
  await renderCreate();
  expect(screen.getAllByRole('tab').map((t) => t.textContent)).toEqual(['Basic Info', 'Backends', 'Traffic Policy', 'Preview']);
  expect(screen.getByLabelText('Listener Port')).toBeInTheDocument();
  expect(screen.queryByLabelText('Service')).not.toBeInTheDocument();
  expect(screen.queryByLabelText('Enable load balancer')).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'Create Route' })).not.toBeInTheDocument();
});

test('gates policy sections by protocol', async () => {
  await renderCreate();
  tab(/^Traffic Policy/);
  expect(screen.getByLabelText('Enable circuit breaker')).toBeInTheDocument();
  expect(screen.getByLabelText('Enable health check')).toBeInTheDocument();
  expect(screen.getByLabelText('Enable load balancer')).toBeInTheDocument();
  tab(/^Basic Info/);
  fireEvent.click(screen.getByRole('radio', { name: 'UDP' }));
  tab(/^Traffic Policy/);
  expect(screen.queryByLabelText('Enable circuit breaker')).not.toBeInTheDocument();
  expect(screen.queryByLabelText('Enable health check')).not.toBeInTheDocument();
  expect(screen.getByLabelText('Enable load balancer')).toBeInTheDocument();
  expect(screen.getByTestId('udp-policy-note')).toBeInTheDocument();
});

test('shows a live collision warning for an in-use port and allows tcp/udp on the same port', async () => {
  await renderCreate();
  fireEvent.change(screen.getByLabelText('Listener Port'), { target: { value: '5432' } });
  expect(screen.getByTestId('port-collision')).toHaveTextContent('"db"');
  fireEvent.click(screen.getByRole('radio', { name: 'UDP' }));
  expect(screen.queryByTestId('port-collision')).not.toBeInTheDocument();
});

test('Next checks only the current tab, then advances; Back returns', async () => {
  await renderCreate();
  fireEvent.click(screen.getByRole('button', { name: 'Next' }));
  expect(await screen.findByText('Name is required')).toBeInTheDocument();
  expect(screen.getByLabelText('Listener Port')).toBeInTheDocument();
  expect(screen.getByTestId('tab-error-basic')).toBeInTheDocument();
  expect(screen.queryByTestId('tab-error-backends')).not.toBeInTheDocument();
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'web' } });
  fireEvent.change(screen.getByLabelText('Listener Port'), { target: { value: '6379' } });
  fireEvent.click(screen.getByRole('button', { name: 'Next' }));
  expect(await screen.findByLabelText('Service')).toBeInTheDocument();
  expect(screen.queryByTestId('tab-error-basic')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Back' }));
  expect(screen.getByLabelText('Name')).toHaveValue('web');
});

// Review Focus: the Next -> submit button swap must not submit the form.
test('stepping into Preview with Next does not submit the form', async () => {
  (streamsApi.createRoute as jest.Mock).mockResolvedValue({ id: 'r9' });
  await renderCreate();
  await fillValid('6379');
  fireEvent.click(screen.getByRole('button', { name: 'Next' }));
  expect(screen.getByLabelText('Enable load balancer')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Next' }));
  expect(await screen.findByText('kind: TCPRoute')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Create Route' })).toBeInTheDocument();
  expect(streamsApi.createRoute).not.toHaveBeenCalled();
});

test('blocks submit on a client-side collision and returns to Basic Info', async () => {
  await renderCreate();
  await fillValid('5432');
  await submitFromPreview();
  expect(await screen.findAllByText(/already used by route "db"/)).not.toHaveLength(0);
  expect(screen.getByTestId('tab-error-basic')).toBeInTheDocument();
  expect(streamsApi.createRoute).not.toHaveBeenCalled();
});

test('a submit with an incomplete backend jumps to Backends and flags it', async () => {
  await renderCreate();
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'web' } });
  fireEvent.change(screen.getByLabelText('Listener Port'), { target: { value: '6379' } });
  await submitFromPreview();
  expect(await screen.findByText('Namespace and service are required')).toBeInTheDocument();
  expect(screen.getByTestId('tab-error-backends')).toBeInTheDocument();
  expect(screen.queryByTestId('tab-error-basic')).not.toBeInTheDocument();
  expect(streamsApi.createRoute).not.toHaveBeenCalled();
});

test('submits listenerPort inside config and returns to the stream', async () => {
  (streamsApi.createRoute as jest.Mock).mockResolvedValue({ id: 'r9', name: 'web' });
  await renderCreate();
  await fillValid('6379');
  await submitFromPreview();
  await waitFor(() => expect(streamsApi.createRoute).toHaveBeenCalled());
  const [, , body] = (streamsApi.createRoute as jest.Mock).mock.calls[0];
  expect(body.config.listenerPort).toBe(6379);
  expect(body.protocol).toBe('tcp');
  expect(body.config.matches).toEqual([]);
  await waitFor(() => expect(push).toHaveBeenCalledWith('/projects/p1/streams/s1'));
});

test('shows the server 409 on the port field back on Basic Info', async () => {
  (streamsApi.createRoute as jest.Mock).mockRejectedValue({
    response: { status: 409, data: { error: 'listener port is already in use: TCP/6379' } },
  });
  await renderCreate();
  await fillValid('6379');
  await submitFromPreview();
  expect(await screen.findByText(/TCP\/6379/)).toBeInTheDocument();
  expect(screen.getByLabelText('Listener Port')).toBeInTheDocument();
  expect(screen.getByTestId('tab-error-basic')).toBeInTheDocument();
  expect(push).not.toHaveBeenCalled();
});

test('shows the allowed range and blocks submit for a port outside it', async () => {
  await renderCreate();
  expect(screen.getByText(/Allowed range: 5000–7000/)).toBeInTheDocument();
  await fillValid('9000');
  await submitFromPreview();
  expect(await screen.findByText('Listener port must be between 5000 and 7000')).toBeInTheDocument();
  expect(streamsApi.createRoute).not.toHaveBeenCalled();
});

test('falls back to the plain port check when the template fetch fails', async () => {
  (domainTemplatesApi.get as jest.Mock).mockRejectedValueOnce(new Error('boom'));
  (streamsApi.createRoute as jest.Mock).mockResolvedValue({ warnings: [] });
  await renderCreate();
  expect(screen.queryByText(/Allowed range/)).not.toBeInTheDocument();
  await fillValid('9000');
  await submitFromPreview();
  await waitFor(() => expect(streamsApi.createRoute).toHaveBeenCalled());
});

test('Preview renders the proposed route and BackendTrafficPolicy from the server', async () => {
  (streamsApi.previewCreateRoute as jest.Mock).mockResolvedValue({
    proposedYaml: 'kind: TCPRoute\nname: web',
    proposedBackendTrafficPolicyYaml: 'kind: BackendTrafficPolicy\nname: web',
  });
  await renderCreate();
  await fillValid('6379');
  tab(/^Preview/);
  expect(await screen.findByText('kind: TCPRoute')).toBeInTheDocument();
  expect(screen.getByRole('heading', { name: 'TCPRoute' })).toBeInTheDocument();
  expect(screen.getByRole('heading', { name: 'BackendTrafficPolicy' })).toBeInTheDocument();
  expect(screen.getByText('kind: BackendTrafficPolicy')).toBeInTheDocument();
  const [p, s, body] = (streamsApi.previewCreateRoute as jest.Mock).mock.calls[0];
  expect([p, s]).toEqual(['p1', 's1']);
  expect(body).toMatchObject({ streamId: 's1', name: 'web', protocol: 'tcp', teamId: 't1', config: { listenerPort: 6379, matches: [] } });
});

test('a UDP route previews as UDPRoute', async () => {
  (streamsApi.previewCreateRoute as jest.Mock).mockResolvedValue({ proposedYaml: 'kind: UDPRoute' });
  await renderCreate();
  fireEvent.click(screen.getByRole('radio', { name: 'UDP' }));
  await fillValid('6379');
  tab(/^Preview/);
  expect(await screen.findByRole('heading', { name: 'UDPRoute' })).toBeInTheDocument();
  expect(screen.queryByRole('heading', { name: 'BackendTrafficPolicy' })).not.toBeInTheDocument();
});

test('Preview shows a server 400/409 inline', async () => {
  (streamsApi.previewCreateRoute as jest.Mock).mockRejectedValue({
    response: { status: 409, data: { error: 'listener port is already in use: TCP/6379' } },
  });
  await renderCreate();
  await fillValid('6379');
  tab(/^Preview/);
  expect(await screen.findByTestId('preview-error')).toHaveTextContent('listener port is already in use: TCP/6379');
  expect(streamsApi.createRoute).not.toHaveBeenCalled();
});

test('Preview asks for a listener port instead of calling the server', async () => {
  await renderCreate();
  tab(/^Preview/);
  expect(await screen.findByTestId('preview-error')).toHaveTextContent('Enter a listener port');
  expect(streamsApi.previewCreateRoute).not.toHaveBeenCalled();
});

// Review Focus: a slow earlier preview must not overwrite a newer one.
test('ignores a stale preview response after the form changed', async () => {
  let resolveFirst: (v: unknown) => void = () => {};
  (streamsApi.previewCreateRoute as jest.Mock)
    .mockImplementationOnce(() => new Promise((resolve) => { resolveFirst = resolve; }))
    .mockResolvedValueOnce({ proposedYaml: 'kind: TCPRoute\nport: 6380' });
  await renderCreate();
  await fillValid('6379');
  tab(/^Preview/);
  tab(/^Basic Info/);
  fireEvent.change(screen.getByLabelText('Listener Port'), { target: { value: '6380' } });
  tab(/^Preview/);
  expect(await screen.findByText('port: 6380')).toBeInTheDocument();
  await act(async () => {
    resolveFirst({ proposedYaml: 'kind: TCPRoute\nport: 6379' });
  });
  expect(screen.queryByText('port: 6379')).not.toBeInTheDocument();
  expect(screen.getByText('port: 6380')).toBeInTheDocument();
});

test('edit previews the update diff and returns to the route detail page', async () => {
  (streamsApi.updateRoute as jest.Mock).mockResolvedValue({ id: 'r1' });
  render(<L4RouteForm projectId="p1" streamId="s1" routeId="r1" />);
  expect(await screen.findByLabelText('Name')).toHaveValue('db');
  expect(screen.getByRole('link', { name: /Back to Route/ })).toHaveAttribute('href', '/projects/p1/streams/s1/routes/r1');
  fireEvent.change(screen.getByLabelText('Listener Port'), { target: { value: '5433' } });
  tab(/^Preview/);
  expect(await screen.findByText('port: 5433')).toBeInTheDocument();
  expect(screen.getByText('port: 5432')).toBeInTheDocument();
  expect(streamsApi.previewUpdateRoute).toHaveBeenCalledWith(
    'p1', 's1', 'r1', expect.objectContaining({ config: expect.objectContaining({ listenerPort: 5433 }) })
  );
  fireEvent.click(screen.getByRole('button', { name: 'Save Changes' }));
  await waitFor(() => expect(push).toHaveBeenCalledWith('/projects/p1/streams/s1/routes/r1'));
});

// Kept last: the success-with-warnings path schedules a delayed redirect.
test('renders backend-returned warnings after a successful submit', async () => {
  (streamsApi.createRoute as jest.Mock).mockResolvedValue({ id: 'r9', name: 'web', warnings: ['mixed protocol LB notice'] });
  await renderCreate();
  await fillValid('6379');
  await submitFromPreview();
  expect(await screen.findByTestId('submit-warnings')).toHaveTextContent('mixed protocol LB notice');
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx jest src/components/streams/L4RouteForm.test.tsx`
Expected: FAIL — e.g. `expect(received).toEqual(expected)` on the tab list (no tabs), `Unable to find an accessible element with the role "tab"`, `Unable to find role="button" and name "Next"`.

- [ ] **Step 3: Create the manifest diff component**

Create `src/components/streams/L4ManifestDiff.tsx`:

```tsx
'use client';

// Route + BackendTrafficPolicy manifests of an L4 route as YamlDiffViewer sections.
// Each section picks create/update/delete from which side is present, and is
// hidden when neither side has YAML. Used by the form Preview tab and the delete modal.

import { YamlDiffViewer } from '@/components/features/yaml-diff-viewer';
import { diffMode, type DiffMode } from '@/lib/utils/l4route';

export interface L4ManifestPair {
  route?: string;
  btp?: string;
}

interface Props {
  /** "TCPRoute" | "UDPRoute" (l4RouteKind). */
  routeKind: string;
  current?: L4ManifestPair;
  proposed?: L4ManifestPair;
}

export function L4ManifestDiff({ routeKind, current, proposed }: Props) {
  const sections = [
    { key: 'route', title: routeKind, cur: current?.route, prop: proposed?.route },
    { key: 'btp', title: 'BackendTrafficPolicy', cur: current?.btp, prop: proposed?.btp },
  ];
  const visible: Array<{ key: string; title: string; cur?: string; prop?: string; mode: DiffMode }> = [];
  for (const s of sections) {
    const mode = diffMode(s.cur, s.prop);
    if (mode) visible.push({ ...s, mode });
  }

  if (visible.length === 0) return <p className="text-sm text-gray-500">No manifests to show.</p>;

  return (
    <div className="space-y-6">
      {visible.map((s) => (
        <section key={s.key} data-testid={`manifest-diff-${s.key}`}>
          <h3 className="text-sm font-semibold text-gray-700 mb-2">{s.title}</h3>
          <YamlDiffViewer currentYaml={s.cur} proposedYaml={s.prop || ''} mode={s.mode} />
        </section>
      ))}
    </div>
  );
}
```

- [ ] **Step 4: Rewrite the form**

Replace `src/components/streams/L4RouteForm.tsx` with:

```tsx
'use client';

// Create/edit form for TCP/UDP routes attached to a Stream, in the same tabs as
// the domain route form: Basic Info · Backends · Traffic Policy · Preview.
// L4 routes have no hostname, path matches, filters or security: just a listener
// port, weighted Kubernetes Service backends and a protocol-gated
// BackendTrafficPolicy subset. Fields and validation rules are unchanged; the
// Preview tab shows the server's manifest preview, and the server's 400/409
// stays authoritative.

import { useEffect, useMemo, useRef, useState } from 'react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { ArrowLeft, AlertTriangle, ChevronLeft, ChevronRight, Plus, Trash2 } from 'lucide-react';
import {
  Button,
  Card,
  CardContent,
  Input,
  Select,
  Checkbox,
  Tabs,
  TabsList,
  TabsTrigger,
  TabsContent,
} from '@/components/ui';
import { streamsApi, projectTeamsApi, projectNamespacesApi, kubernetesApi, domainTemplatesApi } from '@/lib/api';
import type { DomainTemplate, ProjectTeamRole, Stream, StreamRoute, K8sService, LoadBalancerType } from '@/types';
import { L4ManifestDiff, type L4ManifestPair } from './L4ManifestDiff';
import {
  L4_FORM_TABS,
  L4_LB_TYPES,
  buildCreateInput,
  buildUpdateInput,
  emptyPolicyForm,
  errorsForTab,
  findPortCollision,
  l4RouteKind,
  mergeTabErrors,
  parseListenerPort,
  policyCapabilities,
  routeToFormState,
  tabErrors,
  validateL4Form,
  type L4BackendForm,
  type L4FormTab,
  type L4PolicyForm,
  type L4Protocol,
} from '@/lib/utils/l4route';
import { streamListener } from '@/lib/utils/gateway-listeners';

interface Props {
  projectId: string;
  streamId: string;
  /** When set, the form edits this route; otherwise it creates one. */
  routeId?: string;
}

const TAB_LABELS: Record<L4FormTab, string> = {
  basic: 'Basic Info',
  backends: 'Backends',
  policy: 'Traffic Policy',
  preview: 'Preview',
};

const emptyBackend = (): L4BackendForm => ({ namespace: '', service: '', port: '', weight: '100' });

export function L4RouteForm({ projectId, streamId, routeId }: Props) {
  const router = useRouter();
  const isEdit = Boolean(routeId);
  const streamUrl = `/projects/${projectId}/streams/${streamId}`;
  // Edit returns to the L4 route detail page; create returns to the stream.
  const doneUrl = routeId ? `${streamUrl}/routes/${routeId}` : streamUrl;

  const [stream, setStream] = useState<Stream | null>(null);
  const [template, setTemplate] = useState<DomainTemplate | null>(null);
  const [teams, setTeams] = useState<ProjectTeamRole[]>([]);
  const [namespaces, setNamespaces] = useState<string[]>([]);
  const [existingRoutes, setExistingRoutes] = useState<StreamRoute[]>([]);
  const [servicesByNs, setServicesByNs] = useState<Record<string, K8sService[]>>({});
  const requestedNs = useRef<Set<string>>(new Set());

  const [isLoading, setIsLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);

  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [teamId, setTeamId] = useState('');
  const [protocol, setProtocol] = useState<L4Protocol>('tcp');
  const [portInput, setPortInput] = useState('');
  const [backends, setBackends] = useState<L4BackendForm[]>([emptyBackend()]);
  const [policy, setPolicy] = useState<L4PolicyForm>(emptyPolicyForm());

  const [formErrors, setFormErrors] = useState<Record<string, string>>({});
  const [serverPortError, setServerPortError] = useState<string | null>(null);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [warnings, setWarnings] = useState<string[]>([]);
  const [isSubmitting, setIsSubmitting] = useState(false);

  const [activeTab, setActiveTab] = useState<L4FormTab>('basic');
  const [preview, setPreview] = useState<{ current?: L4ManifestPair; proposed: L4ManifestPair } | null>(null);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const [isPreviewLoading, setIsPreviewLoading] = useState(false);
  // Only the latest preview request may write state; a slow earlier one is dropped.
  const previewSeq = useRef(0);

  const caps = policyCapabilities(protocol);
  const port = parseListenerPort(portInput);

  // Allowed listener-port range from the stream template's TCP/UDP listener.
  // Unknown (template fetch failed / no stream listener) => no client-side
  // range check; the server's 400 stays authoritative either way.
  const range = useMemo(() => {
    const sl = streamListener(template?.listeners ?? []);
    return sl && sl.portRangeMin != null && sl.portRangeMax != null
      ? { min: sl.portRangeMin, max: sl.portRangeMax }
      : null;
  }, [template]);

  // Live (client-side) collision check against the ports already on this stream.
  const collision = useMemo(
    () => findPortCollision(existingRoutes, port, protocol, routeId),
    [existingRoutes, port, protocol, routeId]
  );

  const ensureServices = async (ns: string) => {
    if (!ns || requestedNs.current.has(ns)) return;
    requestedNs.current.add(ns);
    try {
      const list = await kubernetesApi.listServices(projectId, ns);
      setServicesByNs((prev) => ({ ...prev, [ns]: list || [] }));
    } catch {
      setServicesByNs((prev) => ({ ...prev, [ns]: [] }));
    }
  };

  useEffect(() => {
    const load = async () => {
      try {
        const [streamData, routesData, nsData, teamsData, routeData] = await Promise.all([
          streamsApi.get(projectId, streamId),
          streamsApi.listRoutes(projectId, streamId).catch(() => ({ data: [] as StreamRoute[] })),
          projectNamespacesApi.list(projectId).catch(() => []),
          projectTeamsApi.listMyTeams(projectId).catch(() => [] as ProjectTeamRole[]),
          routeId ? streamsApi.getRoute(projectId, streamId, routeId) : Promise.resolve(null),
        ]);
        setStream(streamData);
        // Best-effort: the template gives the allowed port range. On failure
        // fall back to the plain 1-65535 check and let the server decide.
        try {
          setTemplate(await domainTemplatesApi.get(projectId, streamData.gatewayTemplateId));
        } catch {
          setTemplate(null);
        }
        setExistingRoutes(routesData.data || []);
        const nsNames = (nsData || []).filter((n) => n.referenceGrantCreated).map((n) => n.namespace);
        setNamespaces(nsNames);
        setTeams(teamsData || []);

        if (routeData) {
          const s = routeToFormState(routeData);
          setName(routeData.name);
          setDescription(routeData.description || '');
          setTeamId(routeData.teamId);
          setProtocol(routeData.protocol === 'udp' ? 'udp' : 'tcp');
          setPortInput(s.listenerPort);
          setBackends(s.backends.length > 0 ? s.backends : [emptyBackend()]);
          setPolicy(s.policy);
          s.backends.forEach((b) => ensureServices(b.namespace));
        } else {
          if (teamsData && teamsData.length > 0) setTeamId(teamsData[0].team.id);
          if (nsNames.length > 0) {
            setBackends([{ ...emptyBackend(), namespace: nsNames[0] }]);
            ensureServices(nsNames[0]);
          }
        }
      } catch (error: unknown) {
        const err = error as { response?: { data?: { error?: string } } };
        setLoadError(err.response?.data?.error || 'Failed to load form data');
      } finally {
        setIsLoading(false);
      }
    };
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projectId, streamId, routeId]);

  const updateBackend = (i: number, patch: Partial<L4BackendForm>) =>
    setBackends((prev) => prev.map((b, idx) => (idx === i ? { ...b, ...patch } : b)));

  const onNamespaceChange = (i: number, ns: string) => {
    updateBackend(i, { namespace: ns, service: '', port: '' });
    ensureServices(ns);
  };

  const onServiceChange = (i: number, svcName: string) => {
    const b = backends[i];
    const svc = (servicesByNs[b.namespace] || []).find((s) => s.name === svcName);
    updateBackend(i, { service: svcName, port: svc?.ports?.[0] ? String(svc.ports[0].port) : b.port });
  };

  const patchPolicy = (patch: Partial<L4PolicyForm>) => setPolicy((p) => ({ ...p, ...patch }));

  const validateAll = () => validateL4Form({ isEdit, name, teamId, protocol, port, range, collision, backends });

  const formValues = (listenerPort: number) => ({
    name: name.trim(),
    description,
    teamId,
    protocol,
    listenerPort,
    backends,
    policy,
  });

  const goNext = () => {
    const idx = L4_FORM_TABS.indexOf(activeTab);
    if (idx >= L4_FORM_TABS.length - 1) return;
    // Like the domain route form, Next checks only the tab being left.
    const current = errorsForTab(validateAll(), activeTab);
    setFormErrors((prev) => mergeTabErrors(prev, activeTab, current));
    if (Object.keys(current).length === 0) setActiveTab(L4_FORM_TABS[idx + 1]);
  };

  const goBack = () => {
    const idx = L4_FORM_TABS.indexOf(activeTab);
    if (idx > 0) setActiveTab(L4_FORM_TABS[idx - 1]);
  };

  const loadPreview = async () => {
    const seq = ++previewSeq.current;
    setPreview(null);
    setPreviewError(null);
    if (port == null) {
      setIsPreviewLoading(false);
      setPreviewError('Enter a listener port between 1 and 65535 on Basic Info to preview the manifests.');
      return;
    }
    setIsPreviewLoading(true);
    try {
      if (isEdit) {
        const r = await streamsApi.previewUpdateRoute(projectId, streamId, routeId as string, buildUpdateInput(formValues(port)));
        if (seq !== previewSeq.current) return;
        setPreview({
          current: { route: r.currentYaml, btp: r.currentBackendTrafficPolicyYaml },
          proposed: { route: r.proposedYaml, btp: r.proposedBackendTrafficPolicyYaml },
        });
      } else {
        const r = await streamsApi.previewCreateRoute(projectId, streamId, buildCreateInput(streamId, formValues(port)));
        if (seq !== previewSeq.current) return;
        setPreview({ proposed: { route: r.proposedYaml, btp: r.proposedBackendTrafficPolicyYaml } });
      }
    } catch (error: unknown) {
      if (seq !== previewSeq.current) return;
      const err = error as { response?: { data?: { error?: string } } };
      // The same 400/409 a submit would get, shown inline on the Preview tab.
      setPreviewError(err.response?.data?.error || 'Failed to generate preview');
    } finally {
      if (seq === previewSeq.current) setIsPreviewLoading(false);
    }
  };

  // Re-run the preview every time the Preview tab opens, with the current values.
  useEffect(() => {
    if (activeTab === 'preview') loadPreview();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeTab]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setSubmitError(null);
    setServerPortError(null);
    setWarnings([]);
    const errs = validateAll();
    setFormErrors(errs);
    const badTabs = tabErrors(errs);
    if (badTabs.length > 0 || port == null) {
      // Take the user to the first tab that needs fixing.
      if (badTabs.length > 0) setActiveTab(badTabs[0]);
      return;
    }

    setIsSubmitting(true);
    try {
      const values = formValues(port);
      const result = isEdit
        ? await streamsApi.updateRoute(projectId, streamId, routeId as string, buildUpdateInput(values))
        : await streamsApi.createRoute(projectId, streamId, buildCreateInput(streamId, values));

      const returned = result?.warnings ?? [];
      if (returned.length > 0) {
        // Surface backend warnings (e.g. mixed-protocol LoadBalancer) before leaving.
        setWarnings(returned);
        setTimeout(() => router.push(doneUrl), 4000);
      } else {
        router.push(doneUrl);
      }
    } catch (error: unknown) {
      const err = error as { response?: { status?: number; data?: { error?: string } } };
      const msg = err.response?.data?.error;
      if (err.response?.status === 409) {
        // Authoritative port-collision error from the backend, shown on the port field.
        setServerPortError(msg || 'Listener port is already in use');
        setActiveTab('basic');
      } else {
        setSubmitError(msg || `Failed to ${isEdit ? 'update' : 'create'} route`);
      }
    } finally {
      setIsSubmitting(false);
    }
  };

  if (isLoading) {
    return (
      <div className="p-8">
        <div className="animate-pulse space-y-4">
          <div className="h-8 bg-gray-200 rounded w-1/4" />
          <div className="h-64 bg-gray-200 rounded" />
        </div>
      </div>
    );
  }

  const backLabel = isEdit ? 'Back to Route' : 'Back to Stream';

  if (loadError || !stream) {
    return (
      <div className="p-8">
        <Link href={doneUrl} className="flex items-center gap-2 text-gray-600 hover:text-gray-900 mb-4">
          <ArrowLeft className="h-4 w-4" />
          {backLabel}
        </Link>
        <p className="text-red-700">{loadError || 'Stream not found'}</p>
      </div>
    );
  }

  const portError = serverPortError || formErrors.port;
  const portWarning = !portError && collision
    ? `Port ${collision.port}/${collision.protocol.toUpperCase()} is already used by route "${collision.routeName}"`
    : null;
  const errorTabs = tabErrors({ ...formErrors, port: portError });

  return (
    <div className="p-8 max-w-3xl">
      <Link href={doneUrl} className="flex items-center gap-2 text-gray-600 hover:text-gray-900 mb-4">
        <ArrowLeft className="h-4 w-4" />
        {backLabel}
      </Link>
      <h1 className="text-2xl font-bold text-gray-900">{isEdit ? `Edit Route: ${name}` : 'Add L4 Route'}</h1>
      <p className="text-gray-600 mt-1 mb-6">Stream: {stream.name}</p>

      <form onSubmit={handleSubmit} noValidate className="space-y-6">
        <Card>
          <CardContent className="pt-6">
            <Tabs value={activeTab} onValueChange={(v) => setActiveTab(v as L4FormTab)} defaultValue="basic">
              <TabsList className="mb-2">
                {L4_FORM_TABS.map((t) => (
                  <TabsTrigger key={t} value={t}>
                    {TAB_LABELS[t]}
                    {errorTabs.includes(t) && (
                      <span data-testid={`tab-error-${t}`} className="ml-1 text-red-500">
                        *
                      </span>
                    )}
                  </TabsTrigger>
                ))}
              </TabsList>

              <TabsContent value="basic">
                <div className="space-y-4">
                  <Input
                    id="name"
                    label="Name"
                    placeholder="e.g., postgres"
                    value={name}
                    disabled={isEdit}
                    onChange={(e) => setName(e.target.value)}
                    error={formErrors.name}
                  />
                  <Input
                    id="description"
                    label="Description"
                    value={description}
                    onChange={(e) => setDescription(e.target.value)}
                  />
                  {!isEdit && (
                    <Select
                      id="teamId"
                      label="Owner Team"
                      value={teamId}
                      onChange={(e) => setTeamId(e.target.value)}
                      options={teams.map((t) => ({ value: t.team.id, label: t.team.name }))}
                      error={formErrors.team}
                    />
                  )}

                  <fieldset>
                    <legend className="block text-sm font-medium text-gray-700 mb-1">Protocol</legend>
                    <div className="flex gap-6">
                      {(['tcp', 'udp'] as const).map((p) => (
                        <label key={p} className="flex items-center gap-2 text-sm text-gray-900">
                          <input
                            type="radio"
                            name="protocol"
                            value={p}
                            checked={protocol === p}
                            disabled={isEdit}
                            onChange={() => setProtocol(p)}
                          />
                          {p.toUpperCase()}
                        </label>
                      ))}
                    </div>
                  </fieldset>

                  <div>
                    <Input
                      id="listenerPort"
                      label="Listener Port"
                      inputMode="numeric"
                      placeholder="e.g., 5432"
                      value={portInput}
                      onChange={(e) => {
                        setPortInput(e.target.value);
                        setServerPortError(null);
                      }}
                      error={portError}
                    />
                    {portWarning && (
                      <p role="alert" data-testid="port-collision" className="mt-1 text-sm text-amber-700">
                        {portWarning}
                      </p>
                    )}
                    <p className="mt-1 text-xs text-gray-500">
                      The Gateway listener port this route is exposed on. A port can be used once per protocol.
                      {range && <> Allowed range: {range.min}–{range.max}.</>}
                    </p>
                  </div>
                </div>
              </TabsContent>

              <TabsContent value="backends">
                <div className="space-y-4">
                  {namespaces.length === 0 && (
                    <p className="text-sm text-amber-700">
                      No project namespaces with a ReferenceGrant are available. Add one under Namespaces first.
                    </p>
                  )}
                  {backends.map((b, i) => (
                    <div key={i} className="border border-gray-200 rounded-lg p-3 space-y-2" data-testid="backend-row">
                      <div className="grid grid-cols-1 md:grid-cols-4 gap-3">
                        <Select
                          id={`backend-ns-${i}`}
                          label="Namespace"
                          value={b.namespace}
                          onChange={(e) => onNamespaceChange(i, e.target.value)}
                          options={[{ value: '', label: 'Select...' }, ...namespaces.map((n) => ({ value: n, label: n }))]}
                        />
                        <Select
                          id={`backend-svc-${i}`}
                          label="Service"
                          value={b.service}
                          onChange={(e) => onServiceChange(i, e.target.value)}
                          options={[
                            { value: '', label: 'Select...' },
                            ...(servicesByNs[b.namespace] || []).map((s) => ({ value: s.name, label: s.name })),
                          ]}
                        />
                        <Input
                          id={`backend-port-${i}`}
                          label="Port"
                          inputMode="numeric"
                          value={b.port}
                          onChange={(e) => updateBackend(i, { port: e.target.value })}
                        />
                        <Input
                          id={`backend-weight-${i}`}
                          label="Weight"
                          inputMode="numeric"
                          value={b.weight}
                          onChange={(e) => updateBackend(i, { weight: e.target.value })}
                        />
                      </div>
                      {formErrors[`backend-${i}`] && <p className="text-sm text-red-600">{formErrors[`backend-${i}`]}</p>}
                      {backends.length > 1 && (
                        <Button
                          type="button"
                          variant="secondary"
                          size="sm"
                          onClick={() => setBackends((prev) => prev.filter((_, idx) => idx !== i))}
                        >
                          <Trash2 className="h-4 w-4 mr-1" />
                          Remove
                        </Button>
                      )}
                    </div>
                  ))}
                  {formErrors.backends && <p className="text-sm text-red-600">{formErrors.backends}</p>}
                  <Button
                    type="button"
                    variant="secondary"
                    size="sm"
                    onClick={() => setBackends((prev) => [...prev, { ...emptyBackend(), namespace: namespaces[0] ?? '' }])}
                  >
                    <Plus className="h-4 w-4 mr-1" />
                    Add backend
                  </Button>
                </div>
              </TabsContent>

              <TabsContent value="policy">
                <div className="space-y-6">
                  {protocol === 'udp' && (
                    <p className="text-sm text-gray-500" data-testid="udp-policy-note">
                      UDP routes support load balancing only. Circuit breaker and health checks do not apply to datagrams.
                    </p>
                  )}

                  {caps.loadBalancer && (
                    <section className="space-y-2">
                      <h3 className="text-sm font-semibold text-gray-900">Load balancing</h3>
                      <label className="flex items-center gap-2 text-sm text-gray-700">
                        <Checkbox
                          aria-label="Enable load balancer"
                          checked={policy.lbEnabled}
                          onChange={(e) => patchPolicy({ lbEnabled: e.target.checked })}
                        />
                        Set a load balancing algorithm
                      </label>
                      {policy.lbEnabled && (
                        <Select
                          id="lbType"
                          aria-label="Load balancer type"
                          value={policy.lbType}
                          onChange={(e) => patchPolicy({ lbType: e.target.value as LoadBalancerType })}
                          options={L4_LB_TYPES.map((t) => ({ value: t, label: t }))}
                        />
                      )}
                    </section>
                  )}

                  {caps.circuitBreaker && (
                    <section className="space-y-2">
                      <h3 className="text-sm font-semibold text-gray-900">Circuit breaker</h3>
                      <label className="flex items-center gap-2 text-sm text-gray-700">
                        <Checkbox
                          aria-label="Enable circuit breaker"
                          checked={policy.cbEnabled}
                          onChange={(e) => patchPolicy({ cbEnabled: e.target.checked })}
                        />
                        Limit connections
                      </label>
                      {policy.cbEnabled && (
                        <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
                          <Input
                            id="cbMaxConnections"
                            label="Max connections"
                            inputMode="numeric"
                            value={policy.cbMaxConnections}
                            onChange={(e) => patchPolicy({ cbMaxConnections: e.target.value })}
                          />
                          <Input
                            id="cbMaxRequestsPerConnection"
                            label="Max requests per connection"
                            inputMode="numeric"
                            value={policy.cbMaxRequestsPerConnection}
                            onChange={(e) => patchPolicy({ cbMaxRequestsPerConnection: e.target.value })}
                          />
                        </div>
                      )}
                    </section>
                  )}

                  {caps.healthCheck && (
                    <section className="space-y-2">
                      <h3 className="text-sm font-semibold text-gray-900">Health check</h3>
                      <label className="flex items-center gap-2 text-sm text-gray-700">
                        <Checkbox
                          aria-label="Enable health check"
                          checked={policy.hcEnabled}
                          onChange={(e) => patchPolicy({ hcEnabled: e.target.checked })}
                        />
                        Active TCP health check
                      </label>
                      {policy.hcEnabled && (
                        <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
                          <Input
                            id="hcInterval"
                            label="Interval"
                            placeholder="e.g., 10s"
                            value={policy.hcInterval}
                            onChange={(e) => patchPolicy({ hcInterval: e.target.value })}
                          />
                          <Input
                            id="hcTimeout"
                            label="Timeout"
                            placeholder="e.g., 1s"
                            value={policy.hcTimeout}
                            onChange={(e) => patchPolicy({ hcTimeout: e.target.value })}
                          />
                          <Input
                            id="hcUnhealthy"
                            label="Unhealthy threshold"
                            inputMode="numeric"
                            value={policy.hcUnhealthyThreshold}
                            onChange={(e) => patchPolicy({ hcUnhealthyThreshold: e.target.value })}
                          />
                          <Input
                            id="hcHealthy"
                            label="Healthy threshold"
                            inputMode="numeric"
                            value={policy.hcHealthyThreshold}
                            onChange={(e) => patchPolicy({ hcHealthyThreshold: e.target.value })}
                          />
                        </div>
                      )}
                    </section>
                  )}
                </div>
              </TabsContent>

              <TabsContent value="preview">
                {isPreviewLoading ? (
                  <p className="text-sm text-gray-500">Generating preview…</p>
                ) : previewError ? (
                  <div
                    role="alert"
                    data-testid="preview-error"
                    className="p-4 bg-red-50 border border-red-200 rounded-lg text-sm text-red-700"
                  >
                    {previewError}
                  </div>
                ) : preview ? (
                  <L4ManifestDiff routeKind={l4RouteKind(protocol)} current={preview.current} proposed={preview.proposed} />
                ) : null}
              </TabsContent>
            </Tabs>

            <div className="flex justify-between items-center mt-6 pt-6 border-t">
              <div>
                {activeTab !== 'basic' && (
                  <Button type="button" variant="secondary" onClick={goBack}>
                    <ChevronLeft className="h-4 w-4 mr-1" />
                    Back
                  </Button>
                )}
              </div>
              <div className="flex gap-3">
                <Link href={doneUrl}>
                  <Button type="button" variant="secondary">
                    Cancel
                  </Button>
                </Link>
                {/* Distinct keys: React must replace (not retype) the button, or the click
                    that opens Preview would submit the form once it becomes type=submit. */}
                {activeTab !== 'preview' ? (
                  <Button key="next" type="button" onClick={goNext}>
                    Next
                    <ChevronRight className="h-4 w-4 ml-1" />
                  </Button>
                ) : (
                  <Button key="submit" type="submit" isLoading={isSubmitting} disabled={warnings.length > 0}>
                    {isEdit ? 'Save Changes' : 'Create Route'}
                  </Button>
                )}
              </div>
            </div>
          </CardContent>
        </Card>

        {warnings.length > 0 && (
          <div className="p-4 rounded-lg bg-amber-50 border border-amber-200" data-testid="submit-warnings">
            <div className="flex items-start gap-3">
              <AlertTriangle className="h-5 w-5 text-amber-600 flex-shrink-0 mt-0.5" />
              <div>
                <h4 className="font-medium text-amber-800">Route {isEdit ? 'updated' : 'created'} with warnings</h4>
                <ul className="mt-1 text-sm text-amber-700 list-disc pl-4">
                  {warnings.map((w, i) => (
                    <li key={i}>{w}</li>
                  ))}
                </ul>
              </div>
            </div>
          </div>
        )}

        {submitError && (
          <div className="p-4 rounded-lg bg-red-50 border border-red-200">
            <div className="flex items-start gap-3">
              <AlertTriangle className="h-5 w-5 text-red-600 flex-shrink-0 mt-0.5" />
              <div>
                <h4 className="font-medium text-red-800">Failed to {isEdit ? 'update' : 'create'} route</h4>
                <p className="mt-1 text-sm text-red-700">{submitError}</p>
              </div>
            </div>
          </div>
        )}
      </form>
    </div>
  );
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `npx jest src/components/streams/L4RouteForm.test.tsx src/lib/utils/l4route.test.ts`
Expected: PASS (form 18 tests; helpers unchanged). If "stepping into Preview with Next does not submit the form" fails with `createRoute` called, the `key="next"`/`key="submit"` props are missing.

- [ ] **Step 6: Commit**

```bash
git add src/components/streams/L4ManifestDiff.tsx src/components/streams/L4RouteForm.tsx src/components/streams/L4RouteForm.test.tsx
git commit -m "feat(streams): tabbed L4 route form with server manifest preview

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: L4 route detail page (Overview / Manifests / History, delete preview)

**Files:**
- Create: `src/lib/utils/version-selection.ts`, `src/lib/utils/version-selection.test.ts`
- Create: `src/components/streams/L4RouteManifests.tsx`
- Create: `src/components/streams/L4RouteHistory.tsx`, `src/components/streams/L4RouteHistory.test.tsx`
- Create: `src/components/streams/L4RouteDeleteModal.tsx`
- Create: `src/app/projects/[projectId]/streams/[streamId]/routes/[routeId]/page.tsx`
- Test: `src/app/projects/[projectId]/streams/[streamId]/routes/[routeId]/page.test.tsx`
- Unchanged: `src/app/projects/[projectId]/streams/[streamId]/routes/[routeId]/edit/page.tsx` (it returns here through `L4RouteForm`, Task 7)

**Interfaces:**
- Consumes: `streamsApi.get`, `getRoute`, `deployRoute`, `deleteRoute`, `previewDeleteRoute`, `getRouteYamls`, `listRouteVersions`, `getRouteVersion`, `rollbackRouteVersion` (Task 1); `l4StatusBadge`, `routeActions`, `policySummary`, `l4RouteKind` (Task 3); `ManifestYaml` (Task 6); `L4ManifestDiff` (Task 7); existing `VersionCompareModal`, `Modal`, `Tabs*`, `RouteVersion` from `@/lib/api/route-versions`.
- Produces: `toggleVersionSelection(selected: number[], version: number): number[]`; `comparePair(selected: number[]): [number, number] | null`; `L4RouteManifests({ projectId, streamId, routeId, protocol })`; `L4RouteHistory({ projectId, streamId, routeId, routeStatus })` (checkbox `aria-label="Select v<n>"`, rows `data-testid="version-v<n>"`); `L4RouteDeleteModal({ isOpen, onClose, onDeleted, projectId, streamId, routeId, routeName, protocol })` (`data-testid="delete-preview-error"`); page default export `L4RouteDetailPage` (banner `data-testid="approval-banner"`, `backends-table`, `policy-summary`).

- [ ] **Step 1: Write the failing helper and component tests**

Create `src/lib/utils/version-selection.test.ts`:

```ts
import { comparePair, toggleVersionSelection } from './version-selection';

test('toggleVersionSelection keeps at most two, newest pick wins', () => {
  expect(toggleVersionSelection([], 3)).toEqual([3]);
  expect(toggleVersionSelection([3], 1)).toEqual([3, 1]);
  expect(toggleVersionSelection([3, 1], 2)).toEqual([1, 2]);
  expect(toggleVersionSelection([3, 1], 3)).toEqual([1]);
});

test('comparePair orders older -> newer and needs exactly two', () => {
  expect(comparePair([3, 1])).toEqual([1, 3]);
  expect(comparePair([1, 3])).toEqual([1, 3]);
  expect(comparePair([1])).toBeNull();
  expect(comparePair([])).toBeNull();
});
```

Create `src/components/streams/L4RouteHistory.test.tsx`:

```tsx
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react';
import { L4RouteHistory } from './L4RouteHistory';

const push = jest.fn();
jest.mock('next/navigation', () => ({ useRouter: () => ({ push }) }));
jest.mock('@/lib/api', () => ({
  streamsApi: { listRouteVersions: jest.fn(), getRouteVersion: jest.fn(), rollbackRouteVersion: jest.fn() },
}));
import { streamsApi } from '@/lib/api';

const version = (n: number) => ({
  id: `v${n}`,
  routeId: 'r1',
  version: n,
  configSnapshot: { routeConfig: { matches: [], backends: [], listenerPort: 5431 + n } },
  routeDescription: '',
  protocol: 'tcp',
  securityMode: 'general',
  changeDescription: `change ${n}`,
  deployedBy: 'u1',
  deployer: { id: 'u1', username: 'alice', email: 'a@example.com' },
  createdAt: '2026-10-01T00:00:00Z',
});

beforeEach(() => {
  jest.resetAllMocks();
  (streamsApi.listRouteVersions as jest.Mock).mockResolvedValue({ data: [version(3), version(2), version(1)], total: 3, page: 1, limit: 20 });
  (streamsApi.getRouteVersion as jest.Mock).mockImplementation((_p: string, _s: string, _r: string, n: number) =>
    Promise.resolve(version(n))
  );
  (streamsApi.rollbackRouteVersion as jest.Mock).mockResolvedValue({});
});

const renderHistory = (routeStatus = 'active') =>
  render(<L4RouteHistory projectId="p1" streamId="s1" routeId="r1" routeStatus={routeStatus} />);

test('lists versions newest first, marks current and offers rollback only on older ones', async () => {
  renderHistory();
  expect(await screen.findByTestId('version-v3')).toHaveTextContent('current');
  expect(streamsApi.listRouteVersions).toHaveBeenCalledWith('p1', 's1', 'r1', 1);
  expect(screen.getAllByRole('button', { name: 'Rollback' })).toHaveLength(2);
  expect(screen.getByTestId('version-v2')).toHaveTextContent('Deployed by alice');
});

test('compares two selected versions in VersionCompareModal', async () => {
  renderHistory();
  await screen.findByTestId('version-v3');
  fireEvent.click(screen.getByLabelText('Select v3'));
  fireEvent.click(screen.getByLabelText('Select v1'));
  fireEvent.click(screen.getByRole('button', { name: 'Compare v1 → v3' }));
  await waitFor(() => expect(streamsApi.getRouteVersion).toHaveBeenCalledTimes(2));
  expect(streamsApi.getRouteVersion).toHaveBeenCalledWith('p1', 's1', 'r1', 1);
  expect(streamsApi.getRouteVersion).toHaveBeenCalledWith('p1', 's1', 'r1', 3);
  expect(await screen.findByText(/Comparing v1/)).toBeInTheDocument();
});

test('rollback confirms, then redirects to Approvals', async () => {
  renderHistory();
  await screen.findByTestId('version-v3');
  fireEvent.click(within(screen.getByTestId('version-v1')).getByRole('button', { name: 'Rollback' }));
  fireEvent.click(screen.getByRole('button', { name: 'Confirm Rollback' }));
  await waitFor(() => expect(streamsApi.rollbackRouteVersion).toHaveBeenCalledWith('p1', 's1', 'r1', 1));
  await waitFor(() => expect(push).toHaveBeenCalledWith('/projects/p1/approvals'));
});

test('a failed rollback stays on the page with the error', async () => {
  (streamsApi.rollbackRouteVersion as jest.Mock).mockRejectedValue({ response: { data: { error: 'route has a pending approval' } } });
  renderHistory();
  await screen.findByTestId('version-v3');
  fireEvent.click(within(screen.getByTestId('version-v1')).getByRole('button', { name: 'Rollback' }));
  fireEvent.click(screen.getByRole('button', { name: 'Confirm Rollback' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('route has a pending approval');
  expect(push).not.toHaveBeenCalled();
});

test('no rollback for a route that is not active', async () => {
  renderHistory('pending_update');
  await screen.findByTestId('version-v3');
  expect(screen.queryByRole('button', { name: 'Rollback' })).not.toBeInTheDocument();
});

test('empty history explains when versions appear', async () => {
  (streamsApi.listRouteVersions as jest.Mock).mockResolvedValue({ data: [], total: 0, page: 1, limit: 20 });
  renderHistory();
  expect(await screen.findByText(/No version history yet/)).toBeInTheDocument();
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `npx jest src/lib/utils/version-selection.test.ts src/components/streams/L4RouteHistory.test.tsx`
Expected: FAIL — `Cannot find module './version-selection'` and `Cannot find module './L4RouteHistory'`.

- [ ] **Step 3: Write the helper and components**

Create `src/lib/utils/version-selection.ts`:

```ts
// Pure helpers for picking two route versions to compare (History tab).

/** Toggle `version`; at most two stay selected and the newest pick replaces the oldest. */
export function toggleVersionSelection(selected: number[], version: number): number[] {
  if (selected.includes(version)) return selected.filter((v) => v !== version);
  if (selected.length >= 2) return [selected[1], version];
  return [...selected, version];
}

/** [older, newer] for exactly two selected versions, else null. */
export function comparePair(selected: number[]): [number, number] | null {
  if (selected.length !== 2) return null;
  const [a, b] = selected;
  return a < b ? [a, b] : [b, a];
}
```

Create `src/components/streams/L4RouteManifests.tsx`:

```tsx
'use client';

// L4 route Manifests tab: the TCPRoute/UDPRoute (carried in httpRouteYaml) and its
// BackendTrafficPolicy, read-only, in the domain Manifests tab's layout.

import { useEffect, useState } from 'react';
import { streamsApi } from '@/lib/api';
import type { RouteYAMLs } from '@/types';
import { l4RouteKind } from '@/lib/utils/l4route';
import { ManifestYaml } from './ManifestYaml';

interface Props {
  projectId: string;
  streamId: string;
  routeId: string;
  protocol: string;
}

export function L4RouteManifests({ projectId, streamId, routeId, protocol }: Props) {
  const [yamls, setYamls] = useState<RouteYAMLs | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    streamsApi
      .getRouteYamls(projectId, streamId, routeId)
      .then((d) => {
        if (!cancelled) setYamls(d);
      })
      .catch((e: unknown) => {
        if (cancelled) return;
        const err = e as { response?: { data?: { error?: string } } };
        setError(err.response?.data?.error || 'Failed to load manifests');
      });
    return () => {
      cancelled = true;
    };
  }, [projectId, streamId, routeId]);

  if (error) {
    return (
      <p role="alert" className="text-sm text-red-700">
        {error}
      </p>
    );
  }
  if (!yamls) return <p className="text-sm text-gray-500">Loading manifests…</p>;
  if (!yamls.httpRouteYaml && !yamls.backendTrafficPolicyYaml) {
    return (
      <div className="text-center py-8 text-gray-500">
        <p>No manifests available.</p>
      </div>
    );
  }

  return (
    <div>
      {yamls.httpRouteYaml && (
        <div className="mb-6">
          <h3 className="text-sm font-medium text-gray-700 mb-2">{l4RouteKind(protocol)}</h3>
          <ManifestYaml yaml={yamls.httpRouteYaml} />
        </div>
      )}
      {yamls.backendTrafficPolicyYaml && (
        <div className="mb-6">
          <h3 className="text-sm font-medium text-gray-700 mb-2">BackendTrafficPolicy</h3>
          <ManifestYaml yaml={yamls.backendTrafficPolicyYaml} />
        </div>
      )}
    </div>
  );
}
```

Create `src/components/streams/L4RouteHistory.tsx`:

```tsx
'use client';

// L4 route History tab: version list, compare two versions, rollback through
// approval. Mirrors the HTTP route detail History tab on the stream-scoped
// version endpoints.

import { useEffect, useState } from 'react';
import { useRouter } from 'next/navigation';
import { GitCompareArrows, History } from 'lucide-react';
import { Badge, Button, Checkbox, Modal } from '@/components/ui';
import { VersionCompareModal } from '@/components/version-compare-modal';
import { streamsApi } from '@/lib/api';
import type { RouteVersion } from '@/lib/api/route-versions';
import { comparePair, toggleVersionSelection } from '@/lib/utils/version-selection';
import { cn } from '@/lib/utils/cn';

interface Props {
  projectId: string;
  streamId: string;
  routeId: string;
  /** Rollback is offered only for an active route, as on HTTP routes. */
  routeStatus: string;
}

type ApiError = { response?: { data?: { error?: string } } };

export function L4RouteHistory({ projectId, streamId, routeId, routeStatus }: Props) {
  const router = useRouter();
  const [versions, setVersions] = useState<RouteVersion[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState<number[]>([]);
  const [compare, setCompare] = useState<{ a: RouteVersion; b: RouteVersion } | null>(null);
  const [isLoadingCompare, setIsLoadingCompare] = useState(false);
  const [rollbackTarget, setRollbackTarget] = useState<number | null>(null);
  const [isRollingBack, setIsRollingBack] = useState(false);

  const load = async (nextPage: number) => {
    setIsLoading(true);
    try {
      const res = await streamsApi.listRouteVersions(projectId, streamId, routeId, nextPage);
      const rows = res.data || [];
      setVersions((prev) => (nextPage === 1 ? rows : [...prev, ...rows]));
      setTotal(res.total);
      setPage(nextPage);
    } catch (e: unknown) {
      setError((e as ApiError).response?.data?.error || 'Failed to load version history');
    } finally {
      setIsLoading(false);
    }
  };

  useEffect(() => {
    load(1);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projectId, streamId, routeId]);

  const pair = comparePair(selected);

  const openCompare = async () => {
    if (!pair) return;
    setIsLoadingCompare(true);
    setError(null);
    try {
      const [a, b] = await Promise.all(pair.map((v) => streamsApi.getRouteVersion(projectId, streamId, routeId, v)));
      setCompare({ a, b });
    } catch {
      setError('Failed to load version details for comparison');
    } finally {
      setIsLoadingCompare(false);
    }
  };

  const confirmRollback = async () => {
    if (rollbackTarget == null) return;
    setIsRollingBack(true);
    setError(null);
    try {
      await streamsApi.rollbackRouteVersion(projectId, streamId, routeId, rollbackTarget);
      // The rollback is a new approval request, as on HTTP routes.
      router.push(`/projects/${projectId}/approvals`);
    } catch (e: unknown) {
      setError((e as ApiError).response?.data?.error || 'Failed to initiate rollback');
      setRollbackTarget(null);
    } finally {
      setIsRollingBack(false);
    }
  };

  return (
    <div className="space-y-3">
      {error && (
        <p role="alert" className="text-sm text-red-700">
          {error}
        </p>
      )}
      {isLoading && versions.length === 0 ? (
        <p className="text-sm text-gray-500 text-center py-8">Loading version history…</p>
      ) : versions.length === 0 ? (
        <div className="text-center py-8">
          <History className="h-8 w-8 text-gray-300 mx-auto mb-3" />
          <p className="text-sm text-gray-500">No version history yet. Versions are created when a route is deployed.</p>
        </div>
      ) : (
        <>
          {pair && (
            <div className="flex justify-end">
              <Button variant="secondary" onClick={openCompare} disabled={isLoadingCompare}>
                <GitCompareArrows className="h-4 w-4 mr-1" />
                {isLoadingCompare ? 'Loading...' : `Compare v${pair[0]} → v${pair[1]}`}
              </Button>
            </div>
          )}
          {versions.map((v, idx) => {
            const isCurrent = idx === 0;
            const isSelected = selected.includes(v.version);
            return (
              <div
                key={v.id}
                data-testid={`version-v${v.version}`}
                className={cn(
                  'flex items-start justify-between p-4 border rounded-lg',
                  isSelected && 'border-primary-300 bg-primary-50/30'
                )}
              >
                <div className="flex items-start gap-3">
                  <Checkbox
                    aria-label={`Select v${v.version}`}
                    checked={isSelected}
                    onChange={() => setSelected((prev) => toggleVersionSelection(prev, v.version))}
                    className="mt-1"
                  />
                  <div className="space-y-1">
                    <div className="flex items-center gap-2">
                      <span className="font-mono font-semibold text-sm">v{v.version}</span>
                      {isCurrent && <Badge variant="success">current</Badge>}
                    </div>
                    <p className="text-sm text-gray-500">
                      Deployed by {v.deployer?.username || 'unknown'} on {new Date(v.createdAt).toLocaleString()}
                    </p>
                    {v.changeDescription && (
                      <p className="text-sm text-gray-700 italic">&ldquo;{v.changeDescription}&rdquo;</p>
                    )}
                  </div>
                </div>
                {!isCurrent && routeStatus === 'active' && (
                  <Button variant="secondary" onClick={() => setRollbackTarget(v.version)} disabled={isRollingBack}>
                    Rollback
                  </Button>
                )}
              </div>
            );
          })}
          {total > versions.length && (
            <div className="flex justify-center pt-2">
              <Button variant="ghost" onClick={() => load(page + 1)} disabled={isLoading}>
                Load More
              </Button>
            </div>
          )}
        </>
      )}

      <Modal
        isOpen={rollbackTarget !== null}
        onClose={() => setRollbackTarget(null)}
        title={`Rollback to version ${rollbackTarget ?? ''}?`}
      >
        <p className="text-gray-600 mb-6">
          This will create a new approval request to revert the route configuration to version {rollbackTarget}. The
          rollback will not take effect until it is approved and deployed.
        </p>
        <div className="flex justify-end gap-3">
          <Button variant="secondary" onClick={() => setRollbackTarget(null)} disabled={isRollingBack}>
            Cancel
          </Button>
          <Button onClick={confirmRollback} isLoading={isRollingBack}>
            Confirm Rollback
          </Button>
        </div>
      </Modal>

      {compare && (
        <VersionCompareModal
          isOpen={true}
          onClose={() => {
            setCompare(null);
            setSelected([]);
          }}
          versionA={compare.a}
          versionB={compare.b}
        />
      )}
    </div>
  );
}
```

Create `src/components/streams/L4RouteDeleteModal.tsx`:

```tsx
'use client';

// Delete confirmation for an L4 route: shows what the delete removes
// (previewDeleteRoute), then submits the delete request for approval. A failed
// preview does not block the request; the server decides.

import { useEffect, useState } from 'react';
import { Button, Modal } from '@/components/ui';
import { streamsApi } from '@/lib/api';
import type { PreviewDeleteResult } from '@/types';
import { l4RouteKind } from '@/lib/utils/l4route';
import { L4ManifestDiff } from './L4ManifestDiff';

interface Props {
  isOpen: boolean;
  onClose: () => void;
  onDeleted: () => void;
  projectId: string;
  streamId: string;
  routeId: string;
  routeName: string;
  protocol: string;
}

type ApiError = { response?: { data?: { error?: string } } };

export function L4RouteDeleteModal({ isOpen, onClose, onDeleted, projectId, streamId, routeId, routeName, protocol }: Props) {
  const [preview, setPreview] = useState<PreviewDeleteResult | null>(null);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const [isDeleting, setIsDeleting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!isOpen) return;
    let cancelled = false;
    setPreview(null);
    setPreviewError(null);
    setError(null);
    streamsApi
      .previewDeleteRoute(projectId, streamId, routeId)
      .then((d) => {
        if (!cancelled) setPreview(d);
      })
      .catch((e: unknown) => {
        if (!cancelled) setPreviewError((e as ApiError).response?.data?.error || 'request failed');
      });
    return () => {
      cancelled = true;
    };
  }, [isOpen, projectId, streamId, routeId]);

  const submit = async () => {
    setIsDeleting(true);
    setError(null);
    try {
      await streamsApi.deleteRoute(projectId, streamId, routeId);
      onDeleted();
    } catch (e: unknown) {
      setError((e as ApiError).response?.data?.error || 'Failed to submit delete request');
    } finally {
      setIsDeleting(false);
    }
  };

  return (
    <Modal isOpen={isOpen} onClose={onClose} title="Delete Route" size="xl">
      <div className="space-y-4">
        <p className="text-gray-600">
          Are you sure you want to delete the route <strong>{routeName}</strong>? This submits a delete request for
          approval; these manifests are removed once it is approved and deployed.
        </p>
        {previewError ? (
          <p role="alert" data-testid="delete-preview-error" className="text-sm text-amber-700">
            Could not load the delete preview ({previewError}). You can still submit the request.
          </p>
        ) : preview ? (
          <L4ManifestDiff
            routeKind={l4RouteKind(protocol)}
            current={{ route: preview.currentYaml, btp: preview.currentBackendTrafficPolicyYaml }}
          />
        ) : (
          <p className="text-sm text-gray-500">Loading preview…</p>
        )}
        {error && (
          <p role="alert" className="text-sm text-red-700">
            {error}
          </p>
        )}
        <div className="flex justify-end gap-3 pt-2">
          <Button variant="secondary" onClick={onClose} disabled={isDeleting}>
            Cancel
          </Button>
          <Button variant="danger" onClick={submit} isLoading={isDeleting}>
            Submit Delete Request
          </Button>
        </div>
      </div>
    </Modal>
  );
}
```

- [ ] **Step 4: Run helper and history tests to verify they pass**

Run: `npx jest src/lib/utils/version-selection.test.ts src/components/streams/L4RouteHistory.test.tsx`
Expected: PASS (2 + 6 tests).

- [ ] **Step 5: Write the failing page test**

Create `src/app/projects/[projectId]/streams/[streamId]/routes/[routeId]/page.test.tsx`:

```tsx
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import L4RouteDetailPage from './page';

const push = jest.fn();
jest.mock('next/navigation', () => ({
  useParams: () => ({ projectId: 'p1', streamId: 's1', routeId: 'r1' }),
  useRouter: () => ({ push }),
}));
jest.mock('@/lib/api', () => ({
  streamsApi: {
    get: jest.fn(),
    getRoute: jest.fn(),
    deployRoute: jest.fn(),
    deleteRoute: jest.fn(),
    previewDeleteRoute: jest.fn(),
    getRouteYamls: jest.fn(),
    listRouteVersions: jest.fn(),
    getRouteVersion: jest.fn(),
    rollbackRouteVersion: jest.fn(),
  },
  permissionsApi: { getProjectPermissions: jest.fn() },
}));
import { streamsApi, permissionsApi } from '@/lib/api';

const ROUTE = {
  id: 'r1', streamId: 's1', teamId: 't1', team: { id: 't1', name: 'Team A' },
  name: 'db', description: 'Postgres primary', protocol: 'tcp', securityMode: 'general', status: 'active',
  config: { matches: [], listenerPort: 5432, backends: [{ type: 'kubernetes', namespace: 'data', service: 'pg', port: 5432, weight: 100 }] },
  backendTrafficPolicy: { id: 'b1', projectId: 'p1', config: { loadBalancer: { type: 'LeastRequest' } }, createdAt: '', updatedAt: '' },
  createdBy: 'u1', createdAt: '2026-10-01T00:00:00Z', updatedAt: '2026-10-02T00:00:00Z',
};
const version = (n: number) => ({
  id: `v${n}`, routeId: 'r1', version: n, configSnapshot: { routeConfig: { listenerPort: 5432 } },
  routeDescription: '', protocol: 'tcp', securityMode: 'general', changeDescription: '',
  deployedBy: 'u1', createdAt: '2026-10-01T00:00:00Z',
});

beforeEach(() => {
  jest.resetAllMocks();
  (streamsApi.get as jest.Mock).mockResolvedValue({ id: 's1', name: 'pg-stream' });
  (streamsApi.getRoute as jest.Mock).mockResolvedValue(ROUTE);
  (permissionsApi.getProjectPermissions as jest.Mock).mockResolvedValue({ canCreateRoutes: true });
  (streamsApi.getRouteYamls as jest.Mock).mockResolvedValue({
    httpRouteYaml: 'kind: TCPRoute\nname: db',
    backendTrafficPolicyYaml: 'kind: BackendTrafficPolicy\nname: db',
  });
  (streamsApi.previewDeleteRoute as jest.Mock).mockResolvedValue({ currentYaml: 'kind: TCPRoute\nname: db' });
  (streamsApi.deleteRoute as jest.Mock).mockResolvedValue({});
  (streamsApi.deployRoute as jest.Mock).mockResolvedValue({});
  (streamsApi.listRouteVersions as jest.Mock).mockResolvedValue({ data: [version(2), version(1)], total: 2, page: 1, limit: 20 });
  (streamsApi.rollbackRouteVersion as jest.Mock).mockResolvedValue({});
});

test('shows the header, actions and the Overview tab', async () => {
  render(<L4RouteDetailPage />);
  expect(await screen.findByRole('heading', { name: 'db' })).toBeInTheDocument();
  expect(screen.getByText('TCP')).toBeInTheDocument();
  expect(screen.getByText(':5432')).toBeInTheDocument();
  expect(screen.getByText('Active')).toBeInTheDocument();
  expect(screen.getByText(/Owner: Team A/)).toBeInTheDocument();
  expect(screen.getByRole('link', { name: 'Back to pg-stream' })).toHaveAttribute('href', '/projects/p1/streams/s1');
  expect(screen.getByRole('link', { name: 'Edit' })).toHaveAttribute('href', '/projects/p1/streams/s1/routes/r1/edit');
  expect(screen.getByText('Postgres primary')).toBeInTheDocument();
  expect(screen.getByTestId('backends-table')).toHaveTextContent('pg');
  expect(screen.getByTestId('policy-summary')).toHaveTextContent('Load balancing: LeastRequest');
  expect(screen.queryByTestId('approval-banner')).not.toBeInTheDocument();
});

test('a pending route shows the approval banner and hides Edit/Delete', async () => {
  (streamsApi.getRoute as jest.Mock).mockResolvedValue({ ...ROUTE, status: 'pending_update' });
  render(<L4RouteDetailPage />);
  expect(await screen.findByText('Waiting for approval')).toBeInTheDocument();
  expect(screen.getByRole('link', { name: 'Review in Approvals' })).toHaveAttribute('href', '/projects/p1/approvals');
  expect(screen.queryByRole('link', { name: 'Edit' })).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'Delete' })).not.toBeInTheDocument();
});

test('Deploy is offered for an approved route', async () => {
  (streamsApi.getRoute as jest.Mock).mockResolvedValue({ ...ROUTE, status: 'approved' });
  render(<L4RouteDetailPage />);
  fireEvent.click(await screen.findByRole('button', { name: 'Deploy' }));
  await waitFor(() => expect(streamsApi.deployRoute).toHaveBeenCalledWith('p1', 's1', 'r1'));
});

test('Manifests tab shows the TCPRoute and its BackendTrafficPolicy', async () => {
  render(<L4RouteDetailPage />);
  fireEvent.click(await screen.findByRole('tab', { name: 'Manifests' }));
  expect(await screen.findByText(/kind: TCPRoute/)).toBeInTheDocument();
  expect(screen.getByRole('heading', { name: 'TCPRoute' })).toBeInTheDocument();
  expect(screen.getByRole('heading', { name: 'BackendTrafficPolicy' })).toBeInTheDocument();
  expect(streamsApi.getRouteYamls).toHaveBeenCalledWith('p1', 's1', 'r1');
});

test('Delete shows the preview-delete diff, then submits the delete request', async () => {
  render(<L4RouteDetailPage />);
  fireEvent.click(await screen.findByRole('button', { name: 'Delete' }));
  expect(await screen.findByText('kind: TCPRoute')).toBeInTheDocument();
  expect(streamsApi.previewDeleteRoute).toHaveBeenCalledWith('p1', 's1', 'r1');
  fireEvent.click(screen.getByRole('button', { name: 'Submit Delete Request' }));
  await waitFor(() => expect(streamsApi.deleteRoute).toHaveBeenCalledWith('p1', 's1', 'r1'));
  await waitFor(() => expect(push).toHaveBeenCalledWith('/projects/p1/streams/s1'));
});

test('a failed delete preview still lets the user submit', async () => {
  (streamsApi.previewDeleteRoute as jest.Mock).mockRejectedValue({ response: { data: { error: 'boom' } } });
  render(<L4RouteDetailPage />);
  fireEvent.click(await screen.findByRole('button', { name: 'Delete' }));
  expect(await screen.findByTestId('delete-preview-error')).toHaveTextContent('boom');
  fireEvent.click(screen.getByRole('button', { name: 'Submit Delete Request' }));
  await waitFor(() => expect(streamsApi.deleteRoute).toHaveBeenCalled());
});

test('History rollback goes to Approvals', async () => {
  render(<L4RouteDetailPage />);
  fireEvent.click(await screen.findByRole('tab', { name: 'History' }));
  fireEvent.click(await screen.findByRole('button', { name: 'Rollback' }));
  fireEvent.click(screen.getByRole('button', { name: 'Confirm Rollback' }));
  await waitFor(() => expect(streamsApi.rollbackRouteVersion).toHaveBeenCalledWith('p1', 's1', 'r1', 1));
  await waitFor(() => expect(push).toHaveBeenCalledWith('/projects/p1/approvals'));
});

test('a route from another stream (404) shows not found', async () => {
  (streamsApi.getRoute as jest.Mock).mockRejectedValue({ response: { status: 404, data: { error: 'route not found' } } });
  render(<L4RouteDetailPage />);
  expect(await screen.findByText('route not found')).toBeInTheDocument();
  expect(screen.getByRole('link', { name: 'Back to Stream' })).toHaveAttribute('href', '/projects/p1/streams/s1');
});
```

- [ ] **Step 6: Run the page test to verify it fails**

Run: `npx jest 'streams/\[streamId\]/routes/\[routeId\]/page'`
Expected: FAIL — `Cannot find module './page'`.

- [ ] **Step 7: Create the page**

Create `src/app/projects/[projectId]/streams/[streamId]/routes/[routeId]/page.tsx`:

```tsx
'use client';

// L4 (TCP/UDP) route detail: header + actions, pending-approval banner, and
// Overview / Manifests / History tabs. The L4 counterpart of the HTTP route
// detail page, on the stream-scoped endpoints.

import { useEffect, useState } from 'react';
import { useParams, useRouter } from 'next/navigation';
import Link from 'next/link';
import { AlertCircle, ArrowLeft, Clock, Pencil, Rocket, Trash2 } from 'lucide-react';
import { Badge, Button, Card, CardContent, Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui';
import { streamsApi, permissionsApi } from '@/lib/api';
import { L4RouteManifests } from '@/components/streams/L4RouteManifests';
import { L4RouteHistory } from '@/components/streams/L4RouteHistory';
import { L4RouteDeleteModal } from '@/components/streams/L4RouteDeleteModal';
import { l4StatusBadge, policySummary, routeActions } from '@/lib/utils/l4route';
import type { ProjectPermissions, Route, Stream } from '@/types';

type ApiError = { response?: { data?: { error?: string } } };

export default function L4RouteDetailPage() {
  const params = useParams();
  const router = useRouter();
  const projectId = params.projectId as string;
  const streamId = params.streamId as string;
  const routeId = params.routeId as string;
  const streamUrl = `/projects/${projectId}/streams/${streamId}`;
  const routeUrl = `${streamUrl}/routes/${routeId}`;

  const [stream, setStream] = useState<Stream | null>(null);
  const [route, setRoute] = useState<Route | null>(null);
  const [permissions, setPermissions] = useState<ProjectPermissions | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [isDeploying, setIsDeploying] = useState(false);
  const [showDelete, setShowDelete] = useState(false);

  const loadData = async () => {
    try {
      const [streamData, routeData, permsData] = await Promise.all([
        streamsApi.get(projectId, streamId),
        streamsApi.getRoute(projectId, streamId, routeId),
        permissionsApi.getProjectPermissions(projectId).catch(() => null),
      ]);
      setStream(streamData);
      setRoute(routeData);
      setPermissions(permsData);
      setLoadError(null);
    } catch (e: unknown) {
      setLoadError((e as ApiError).response?.data?.error || 'Route not found');
    } finally {
      setIsLoading(false);
    }
  };

  useEffect(() => {
    loadData();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projectId, streamId, routeId]);

  const handleDeploy = async () => {
    setIsDeploying(true);
    setActionError(null);
    try {
      await streamsApi.deployRoute(projectId, streamId, routeId);
      await loadData();
    } catch (e: unknown) {
      setActionError((e as ApiError).response?.data?.error || 'Failed to deploy route');
    } finally {
      setIsDeploying(false);
    }
  };

  if (isLoading) {
    return (
      <div className="p-8">
        <div className="animate-pulse space-y-4">
          <div className="h-8 bg-gray-200 rounded w-1/4" />
          <div className="h-64 bg-gray-200 rounded" />
        </div>
      </div>
    );
  }

  if (!route || !stream) {
    return (
      <div className="p-8">
        <Link href={streamUrl} className="flex items-center gap-2 text-gray-600 hover:text-gray-900 mb-4">
          <ArrowLeft className="h-4 w-4" />
          Back to Stream
        </Link>
        <p className="text-red-700">{loadError || 'Route not found'}</p>
      </div>
    );
  }

  const actions = routeActions(route.status, Boolean(permissions?.canCreateRoutes));
  const status = l4StatusBadge(route.status);
  const isDeleteAction = route.pendingApproval?.action === 'delete';
  const backends = route.config?.backends ?? [];
  const policy = policySummary(route.backendTrafficPolicy?.config);

  return (
    <div className="p-8">
      <Link href={streamUrl} className="flex items-center gap-2 text-gray-600 hover:text-gray-900 mb-4">
        <ArrowLeft className="h-4 w-4" />
        Back to {stream.name}
      </Link>

      <div className="flex items-start justify-between mb-6">
        <div>
          <div className="flex items-center gap-3 mb-2">
            <h1 className="text-2xl font-bold text-gray-900">{route.name}</h1>
            <Badge>{route.protocol.toUpperCase()}</Badge>
            {route.config?.listenerPort != null && (
              <span className="font-mono text-gray-700">:{route.config.listenerPort}</span>
            )}
            <Badge variant={status.variant}>{status.label}</Badge>
          </div>
          <p className="text-sm text-gray-500">
            Owner: {route.team?.name || 'Unknown Team'} · Stream: {stream.name}
          </p>
        </div>
        <div className="flex gap-2">
          {actions.canDeploy && (
            <Button onClick={handleDeploy} disabled={isDeploying} variant={isDeleteAction ? 'danger' : 'primary'}>
              <Rocket className="h-4 w-4 mr-1" />
              {isDeploying
                ? isDeleteAction ? 'Destroying...' : 'Deploying...'
                : isDeleteAction ? 'Destroy' : 'Deploy'}
            </Button>
          )}
          {actions.canEdit && (
            <Link href={`${routeUrl}/edit`}>
              <Button variant="secondary">
                <Pencil className="h-4 w-4 mr-1" />
                Edit
              </Button>
            </Link>
          )}
          {actions.canDelete && (
            <Button variant="danger" onClick={() => setShowDelete(true)}>
              <Trash2 className="h-4 w-4 mr-1" />
              Delete
            </Button>
          )}
        </div>
      </div>

      {actionError && (
        <div role="alert" className="mb-6 p-4 bg-red-50 border border-red-200 rounded-lg text-red-700 flex items-start gap-3">
          <AlertCircle className="h-5 w-5 flex-shrink-0 mt-0.5" />
          <div>{actionError}</div>
        </div>
      )}

      {actions.awaitingApproval && (
        <Card className="mb-6 border-yellow-200 bg-yellow-50">
          <CardContent className="py-4">
            <div className="flex items-center justify-between gap-3" data-testid="approval-banner">
              <div className="flex items-center gap-2 text-yellow-800">
                <Clock className="h-5 w-5" />
                <span className="font-medium">Waiting for approval</span>
              </div>
              <Link
                href={`/projects/${projectId}/approvals`}
                className="text-sm font-medium text-yellow-800 underline hover:no-underline whitespace-nowrap"
              >
                Review in Approvals
              </Link>
            </div>
            {route.pendingApproval?.rejectionComment && (
              <p className="mt-2 text-sm text-yellow-700">Rejection reason: {route.pendingApproval.rejectionComment}</p>
            )}
          </CardContent>
        </Card>
      )}

      <Card>
        <CardContent className="pt-6">
          <Tabs defaultValue="overview">
            <TabsList>
              <TabsTrigger value="overview">Overview</TabsTrigger>
              <TabsTrigger value="manifests">Manifests</TabsTrigger>
              <TabsTrigger value="history">History</TabsTrigger>
            </TabsList>

            <TabsContent value="overview">
              <div className="space-y-6 text-sm">
                <section>
                  <h3 className="font-medium text-gray-900 mb-1">Description</h3>
                  <p className="text-gray-600">{route.description || 'No description'}</p>
                </section>
                <section>
                  <h3 className="font-medium text-gray-900 mb-2">Backends</h3>
                  {backends.length === 0 ? (
                    <p className="text-gray-500">No backends.</p>
                  ) : (
                    <table className="w-full" data-testid="backends-table">
                      <thead>
                        <tr className="border-b border-gray-200 text-left text-gray-500">
                          <th className="py-2 pr-4 font-medium">Namespace</th>
                          <th className="py-2 pr-4 font-medium">Service</th>
                          <th className="py-2 pr-4 font-medium">Port</th>
                          <th className="py-2 font-medium">Weight</th>
                        </tr>
                      </thead>
                      <tbody>
                        {backends.map((b, i) => (
                          <tr key={`${b.namespace}-${b.service}-${b.port}-${i}`} className="border-b border-gray-100 last:border-0">
                            <td className="py-2 pr-4 text-gray-700">{b.namespace}</td>
                            <td className="py-2 pr-4 font-mono text-gray-900">{b.service}</td>
                            <td className="py-2 pr-4 font-mono text-gray-700">{b.port}</td>
                            <td className="py-2 text-gray-700">{b.weight ?? 100}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  )}
                </section>
                <section>
                  <h3 className="font-medium text-gray-900 mb-1">Traffic policy</h3>
                  {policy.length === 0 ? (
                    <p className="text-gray-500">No traffic policy; Envoy defaults apply.</p>
                  ) : (
                    <ul className="list-disc pl-5 text-gray-700" data-testid="policy-summary">
                      {policy.map((line) => (
                        <li key={line}>{line}</li>
                      ))}
                    </ul>
                  )}
                </section>
                <section className="text-gray-500">
                  <p>Created {new Date(route.createdAt).toLocaleString()}</p>
                  <p>Updated {new Date(route.updatedAt).toLocaleString()}</p>
                </section>
              </div>
            </TabsContent>

            <TabsContent value="manifests">
              <L4RouteManifests projectId={projectId} streamId={streamId} routeId={routeId} protocol={route.protocol} />
            </TabsContent>

            <TabsContent value="history">
              <L4RouteHistory projectId={projectId} streamId={streamId} routeId={routeId} routeStatus={route.status} />
            </TabsContent>
          </Tabs>
        </CardContent>
      </Card>

      <L4RouteDeleteModal
        isOpen={showDelete}
        onClose={() => setShowDelete(false)}
        onDeleted={() => router.push(streamUrl)}
        projectId={projectId}
        streamId={streamId}
        routeId={routeId}
        routeName={route.name}
        protocol={route.protocol}
      />
    </div>
  );
}
```

- [ ] **Step 8: Run tests to verify they pass**

Run: `npx jest 'streams/\[streamId\]/routes/\[routeId\]/page' src/components/streams`
Expected: PASS (page 8 tests; all `src/components/streams` suites).

- [ ] **Step 9: Commit**

```bash
git add src/lib/utils/version-selection.ts src/lib/utils/version-selection.test.ts src/components/streams/L4RouteManifests.tsx src/components/streams/L4RouteHistory.tsx src/components/streams/L4RouteHistory.test.tsx src/components/streams/L4RouteDeleteModal.tsx 'src/app/projects/[projectId]/streams/[streamId]/routes/[routeId]/page.tsx' 'src/app/projects/[projectId]/streams/[streamId]/routes/[routeId]/page.test.tsx'
git commit -m "feat(streams): L4 route detail page with manifests, history and delete preview

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 9: Approvals — stream label for L4 and titles from routeKind

**Files:**
- Create: `src/lib/utils/approval-labels.ts`, `src/lib/utils/approval-labels.test.ts`
- Modify: `src/app/projects/[projectId]/approvals/page.tsx` (~L8 import; `UnifiedApproval` ~L16; route mapping ~L70; client mapping ~L93; render ~L341-345)
- Modify: `src/app/projects/[projectId]/approvals/[approvalId]/page.tsx` (~L9 import; state ~L27-41; header ~L226-228; titles ~L320, ~L348)
- Test: `src/app/projects/[projectId]/approvals/page.test.tsx`, `src/app/projects/[projectId]/approvals/[approvalId]/page.test.tsx` (create)

**Interfaces:**
- Consumes: `ApprovalRequest.streamName`, `ApprovalDiff.routeKind`, `RouteKind` (Task 1).
- Produces: `approvalContextLabel(a: { streamName?: string; domainName?: string }): string | null`; `diffSectionTitles(routeKind?: RouteKind): { route: string; backendTrafficPolicy: string }`; detail context line `data-testid="approval-context"`.

- [ ] **Step 1: Write the failing tests**

Create `src/lib/utils/approval-labels.test.ts`:

```ts
import { approvalContextLabel, diffSectionTitles } from './approval-labels';

test('approvalContextLabel: stream for L4, domain for L7, nothing otherwise', () => {
  expect(approvalContextLabel({ streamName: 'tcp-stream' })).toBe('Stream: tcp-stream');
  expect(approvalContextLabel({ domainName: 'api.example.com' })).toBe('api.example.com');
  expect(approvalContextLabel({ streamName: 'tcp-stream', domainName: 'x' })).toBe('Stream: tcp-stream');
  expect(approvalContextLabel({})).toBeNull();
});

test('diffSectionTitles follow routeKind; older backends default to HTTPRoute', () => {
  expect(diffSectionTitles('TCPRoute')).toEqual({ route: 'TCPRoute Changes', backendTrafficPolicy: 'BackendTrafficPolicy Changes' });
  expect(diffSectionTitles('UDPRoute').route).toBe('UDPRoute Changes');
  expect(diffSectionTitles('GRPCRoute')).toEqual({
    route: 'GRPCRoute Changes',
    backendTrafficPolicy: 'BackendTrafficPolicy Changes (Compression)',
  });
  expect(diffSectionTitles(undefined)).toEqual({
    route: 'HTTPRoute Changes',
    backendTrafficPolicy: 'BackendTrafficPolicy Changes (Compression)',
  });
});
```

Create `src/app/projects/[projectId]/approvals/page.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react';
import ApprovalsPage from './page';

jest.mock('next/navigation', () => ({ useParams: () => ({ projectId: 'p1' }) }));
jest.mock('@/lib/api', () => ({
  approvalsApi: {
    list: jest.fn().mockResolvedValue({
      data: [
        { id: 'a1', routeId: 'r1', entityType: 'route', action: 'create', status: 'pending', submittedBy: 'u1', entityName: 'postgres', streamId: 's1', streamName: 'tcp-stream', createdAt: '2026-10-01T00:00:00Z' },
        { id: 'a2', routeId: 'r2', entityType: 'route', action: 'update', status: 'pending', submittedBy: 'u1', entityName: 'users', domainName: 'api.example.com', createdAt: '2026-10-02T00:00:00Z' },
      ],
    }),
  },
  clientAttachmentsApi: { listApprovals: jest.fn().mockResolvedValue({ data: [] }) },
}));

test('L4 approvals show their stream, L7 approvals their domain', async () => {
  render(<ApprovalsPage />);
  expect(await screen.findByText('Stream: tcp-stream')).toBeInTheDocument();
  expect(screen.getByText('api.example.com')).toBeInTheDocument();
  expect(screen.getByText('postgres')).toBeInTheDocument();
});
```

Create `src/app/projects/[projectId]/approvals/[approvalId]/page.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react';
import ApprovalReviewPage from './page';

jest.mock('next/navigation', () => ({
  useParams: () => ({ projectId: 'p1', approvalId: 'a1' }),
  useRouter: () => ({ push: jest.fn(), replace: jest.fn() }),
}));
jest.mock('@/components/features/comment-feed', () => ({ CommentFeed: () => null }));
jest.mock('@/lib/api', () => ({
  approvalsApi: { get: jest.fn(), getDiff: jest.fn() },
  aiApi: { reviewApproval: jest.fn() },
}));
import { approvalsApi } from '@/lib/api';

const L4_APPROVAL = {
  id: 'a1', routeId: 'r1', entityType: 'route', action: 'update', status: 'approved', submittedBy: 'u1',
  submitter: { username: 'alice' }, entityName: 'postgres', streamId: 's1', streamName: 'tcp-stream',
  configSnapshot: { matches: [], backends: [], listenerPort: 5433 }, createdAt: '2026-10-01T00:00:00Z',
};

test('an L4 approval shows its stream and TCPRoute / BackendTrafficPolicy titles', async () => {
  (approvalsApi.get as jest.Mock).mockResolvedValue(L4_APPROVAL);
  (approvalsApi.getDiff as jest.Mock).mockResolvedValue({
    action: 'update',
    routeKind: 'TCPRoute',
    currentYaml: 'kind: TCPRoute\nport: 5432',
    proposedYaml: 'kind: TCPRoute\nport: 5433',
    proposedBackendTrafficPolicyYaml: 'kind: BackendTrafficPolicy',
  });
  render(<ApprovalReviewPage />);
  expect(await screen.findByText('TCPRoute Changes')).toBeInTheDocument();
  expect(screen.getByText('BackendTrafficPolicy Changes')).toBeInTheDocument();
  expect(screen.queryByText('HTTPRoute Changes')).not.toBeInTheDocument();
  expect(screen.getByTestId('approval-context')).toHaveTextContent('Stream: tcp-stream');
  expect(screen.getByText('postgres')).toBeInTheDocument();
});

test('an HTTP approval from an older backend keeps the HTTPRoute titles and shows its domain', async () => {
  (approvalsApi.get as jest.Mock).mockResolvedValue({
    ...L4_APPROVAL,
    entityName: 'users',
    streamId: undefined,
    streamName: undefined,
    domainName: 'api.example.com',
    configSnapshot: { matches: [{ path: { type: 'Prefix', value: '/users' } }], backends: [] },
  });
  (approvalsApi.getDiff as jest.Mock).mockResolvedValue({
    action: 'update',
    currentYaml: 'kind: HTTPRoute',
    proposedYaml: 'kind: HTTPRoute\n# changed',
    proposedBackendTrafficPolicyYaml: 'kind: BackendTrafficPolicy',
  });
  render(<ApprovalReviewPage />);
  expect(await screen.findByText('HTTPRoute Changes')).toBeInTheDocument();
  expect(screen.getByText('BackendTrafficPolicy Changes (Compression)')).toBeInTheDocument();
  expect(screen.getByTestId('approval-context')).toHaveTextContent('api.example.com');
  expect(screen.getByText('/users')).toBeInTheDocument();
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `npx jest src/lib/utils/approval-labels.test.ts 'projectId\]/approvals/page' 'approvals/\[approvalId\]/page'`
Expected: FAIL — `Cannot find module './approval-labels'`; list: `Unable to find an element with the text: Stream: tcp-stream`; detail: `Unable to find an element with the text: TCPRoute Changes`. (The pattern `'projectId\]/approvals/page'` avoids matching `client-approvals`.)

- [ ] **Step 3: Write the helper**

Create `src/lib/utils/approval-labels.ts`:

```ts
// Pure helpers for the approvals pages: the parent label (stream for L4 routes,
// domain for L7) and diff section titles from the diff's routeKind.
import type { RouteKind } from '@/types';

/** "Stream: tcp-stream" for an L4 route, the domain name for an L7 one, else null. */
export function approvalContextLabel(a: { streamName?: string; domainName?: string }): string | null {
  if (a.streamName) return `Stream: ${a.streamName}`;
  return a.domainName || null;
}

export interface DiffSectionTitles {
  route: string;
  backendTrafficPolicy: string;
}

/** Older backends send no routeKind: keep today's HTTPRoute titles. */
export function diffSectionTitles(routeKind?: RouteKind): DiffSectionTitles {
  const kind = routeKind ?? 'HTTPRoute';
  const l4 = kind === 'TCPRoute' || kind === 'UDPRoute';
  return {
    route: `${kind} Changes`,
    // L4 policies carry LB / circuit breaker / health check, never compression.
    backendTrafficPolicy: l4 ? 'BackendTrafficPolicy Changes' : 'BackendTrafficPolicy Changes (Compression)',
  };
}
```

- [ ] **Step 4: Update the approvals list page**

In `src/app/projects/[projectId]/approvals/page.tsx`:

Add after `import type { ApprovalRequest, Approval, ApprovalStatus, ApprovalStage } from '@/types';`:

```tsx
import { approvalContextLabel } from '@/lib/utils/approval-labels';
```

In `interface UnifiedApproval`, replace `  domainName?: string;` with:

```tsx
  /** "Stream: x" for L4 routes; the domain for L7 routes and client attachments. */
  contextLabel?: string | null;
```

In the route mapping, replace `          domainName: r.domainName,` with `          contextLabel: approvalContextLabel(r),`.

In the client mapping, replace `        domainName: c.domainName,` with `        contextLabel: c.domainName,`.

Replace the render block

```tsx
                          {approval.domainName && (
                            <p className="text-sm text-gray-600 mb-0.5">
                              {approval.domainName}
                            </p>
                          )}
```

with

```tsx
                          {approval.contextLabel && (
                            <p className="text-sm text-gray-600 mb-0.5">
                              {approval.contextLabel}
                            </p>
                          )}
```

- [ ] **Step 5: Update the approval detail page**

In `src/app/projects/[projectId]/approvals/[approvalId]/page.tsx`:

Replace `import type { ApprovalRequest, ApprovalStage, AIReviewResult } from '@/types';` with:

```tsx
import type { ApprovalRequest, ApprovalStage, ApprovalDiff } from '@/types';
import { approvalContextLabel, diffSectionTitles } from '@/lib/utils/approval-labels';
```

Replace the whole `const [diffData, setDiffData] = useState<{ … } | null>(null);` block (from `  const [diffData, setDiffData] = useState<{` through `  } | null>(null);`) with:

```tsx
  const [diffData, setDiffData] = useState<ApprovalDiff | null>(null);
```

Replace

```tsx
  return (
    <div className="p-8 max-w-5xl mx-auto">
```

with

```tsx
  const contextLabel = approvalContextLabel(approval);
  const titles = diffSectionTitles(diffData?.routeKind);

  return (
    <div className="p-8 max-w-5xl mx-auto">
```

Replace

```tsx
          <p className="text-gray-600">
            {approval.configSnapshot?.matches?.[0]?.path?.value || 'Route'}
          </p>
```

with

```tsx
          <p className="text-gray-600">
            {approval.configSnapshot?.matches?.[0]?.path?.value || approval.entityName || approval.route?.name || 'Route'}
          </p>
          {contextLabel && (
            <p className="text-sm text-gray-600" data-testid="approval-context">
              {contextLabel}
            </p>
          )}
```

Replace `<h2 className="text-lg font-semibold text-gray-900 mb-4">HTTPRoute Changes</h2>` with `<h2 className="text-lg font-semibold text-gray-900 mb-4">{titles.route}</h2>`, and `<h2 className="text-lg font-semibold text-gray-900 mb-4">BackendTrafficPolicy Changes (Compression)</h2>` with `<h2 className="text-lg font-semibold text-gray-900 mb-4">{titles.backendTrafficPolicy}</h2>`. (The BTP section already renders whenever current or proposed BTP YAML is present; no link changes.)

- [ ] **Step 6: Run tests and typecheck**

Run: `npx jest src/lib/utils/approval-labels.test.ts 'projectId\]/approvals/page' 'approvals/\[approvalId\]/page'`
Expected: PASS (2 + 1 + 2 tests).
Run: `npx tsc --noEmit`
Expected: no output (proves no reader of the removed `UnifiedApproval.domainName` or of `AIReviewResult` in the detail page remains).

- [ ] **Step 7: Commit**

```bash
git add src/lib/utils/approval-labels.ts src/lib/utils/approval-labels.test.ts 'src/app/projects/[projectId]/approvals/page.tsx' 'src/app/projects/[projectId]/approvals/page.test.tsx' 'src/app/projects/[projectId]/approvals/[approvalId]/page.tsx' 'src/app/projects/[projectId]/approvals/[approvalId]/page.test.tsx'
git commit -m "feat(approvals): stream label for L4 routes and diff titles from routeKind

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 10: Verification gate (fastgateway-frontend-verify)

**Files:** none (fix-ups only if a gate fails, committed with the owning task's scope in the message).

- [ ] **Step 1: Typecheck**

Run: `npx tsc --noEmit`
Expected: no output (0 errors).

- [ ] **Step 2: Lint**

Run: `npm run lint`
Expected: `0 errors` (pre-existing warnings are acceptable; the count should not grow beyond the `react-hooks/exhaustive-deps` / `set-state-in-effect` warnings this plan's fetch-in-effect components add, which match the codebase's existing pattern). Any `react/no-unescaped-entities` error means an apostrophe/quote in JSX text — use `&apos;`/`&ldquo;`.

- [ ] **Step 3: Production build**

Run: `npx next build`
Expected: build succeeds and lists the new route `/projects/[projectId]/streams/[streamId]/routes/[routeId]`.

- [ ] **Step 4: Full test suite**

Run: `npx jest`
Expected: all suites pass (the 46 pre-existing suites plus the new ones: `domainTemplates.test.ts`, `stream-entrypoint.test.ts`, `stream-list.test.ts`, `version-selection.test.ts`, `approval-labels.test.ts`, `StreamEntrypointPanel.test.tsx`, `StreamManifests.test.tsx`, `L4RouteHistory.test.tsx`, streams list / route detail / approvals list / approval detail page tests).

- [ ] **Step 5: Manual check against a backend with the backend plan deployed**

On a project with a shared (`mergeGateways: true`) template that also serves a domain, and a dedicated template:
- Create Stream: options read `name — Shared IP` / `name — Dedicated IP`; selecting each shows the Entry point panel; shared shows the gateway class, "assigned on first deploy" before the first deploy, "also used by N streams and M domains" and a free count. No port/team/Preview fields.
- Streams list: Entry point badge + template, IP, Routes, Ports (`5432/TCP, … +N`), Status; status filter and name search work without network calls.
- Stream detail: Entry point panel (IP, sharing, my ports linked to route detail, neighbours collapsed, reserved 19000/19001/60000 in small print, range + free count); Routes / Metrics / Manifests tabs; Manifests Gateway listeners match `kubectl get gateway str-<name> -o yaml`.
- Add L4 Route: tabs, Next/Back, tab error badges; Preview shows TCPRoute/UDPRoute + BTP; a taken port shows the server 409 inline on Preview; submit lands on Approvals flow as before.
- Route detail: header, pending banner → Approvals, Overview, Manifests, History (compare two versions, rollback → Approvals), Delete shows the delete preview.
- Approvals: list shows "Stream: x" for L4; detail shows "TCPRoute Changes" and "BackendTrafficPolicy Changes" (no 500).

- [ ] **Step 6: Commit any fix-ups, then hand off**

If a gate needed a fix, commit it (`fix(streams): …` with the `Co-Authored-By` line). Then follow the owner's flow: `code-review` before merge; PR description ends with `🤖 Generated with [Claude Code](https://claude.com/claude-code)`.

---

## Spec coverage

| Spec item | Task |
|---|---|
| F1 types (StreamEntrypoint, list fields, StreamYAMLs, approval streamId/streamName, diff routeKind) | 1 |
| F1 `streamsApi` (9 methods) + `domainTemplatesApi.getEntrypoint` | 1 |
| F1 helpers `entrypointLabel`, `splitPorts`, `freePortCount`, `ownerLabel` | 2 |
| F1 `tabErrors` in `l4route.ts` | 3 |
| F2 stream create (mode labels, Entry point panel, no port/team/Preview) | 4 |
| F3 stream list (columns, status filter, debounced search, empty state/permission unchanged) | 5 |
| F4 stream detail (header unchanged, Entry point panel, Routes/Metrics/Manifests, Edit moved) | 6 |
| F5 L4 route form tabs + Preview + inline 400/409 + badges + Next/Back | 7 |
| F6 L4 route detail (header, actions, delete preview, banner, Overview/Manifests/History, edit returns) | 7 (return), 8 |
| F7 approvals (stream label, routeKind titles, BTP section) | 9 |
| Frontend Testing items + `fastgateway-frontend-verify` gate | 2–9, 10 |
| Global: reserved 19000/19001/60000 always shown; port map never "free" when create rejects | 2 (rulings 5–6), 6 |
