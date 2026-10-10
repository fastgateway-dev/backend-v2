# Version-Derived Capabilities (Frontend) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **Repo:** This plan is implemented in `frontend-v2` (Next.js). Create a worktree/branch there first. It depends on the backend plan having shipped `streamAvailable` on `GET /projects/{id}/capabilities`.

**Goal:** Gate the stream-creation UI on the backend `streamAvailable` capability — hide/disable "New Stream" and show a clear "requires Envoy Gateway ≥ 1.8" message when streams are unsupported, optimistic on a capabilities-fetch error.

**Architecture:** Add `streamAvailable` to the existing `ProjectCapabilities` type (served by `projectsApi.getCapabilities`), isolate the gating decision in a pure, unit-tested helper, and consume it in the streams list and create pages — mirroring the existing `rateLimitAvailable` gating pattern exactly.

**Tech Stack:** Next.js 16 (app router), TypeScript, axios API client, Jest + React Testing Library, plain `useState`/`useEffect` (no React Query).

**Spec:** `../specs/2026-10-10-version-derived-capabilities-design.md` (in backend-v2; the frontend section, Component 5)

## Global Constraints

- Mirror the existing `rateLimitAvailable` gating pattern (hide form/action when false; "could not verify" + fall-open on fetch error).
- Optimistic on fetch error: if `getCapabilities` fails, treat streams as available (the backend guard remains the real safety net).
- Label copy: "Streams require Envoy Gateway ≥ 1.8".
- PR descriptions end with: `🤖 Generated with [Claude Code](https://claude.com/claude-code)`.

## Review Focus

- **Capabilities fetch error** → stream UI must fall open (optimistic), not be blocked by a failed probe (covered: Task F1 helper test + Task F2 wiring).
- **`streamAvailable` undefined/absent** in the response (older backend) → treated as available, not blocked (covered: Task F1).
- **Existing `permissions.canManageDomains` gate** must still apply — the new gate is additive, not a replacement (covered: Task F2).

---

### Task F1: `streamAvailable` type + pure gating helper

**Files:**
- Modify: `src/types/index.ts:1288-1290` (add `streamAvailable` to `ProjectCapabilities`)
- Create: `src/lib/stream-capability.ts`
- Test: `src/lib/stream-capability.test.ts`

**Interfaces:**
- Produces: `ProjectCapabilities { rateLimitAvailable: boolean; streamAvailable?: boolean }`; `canCreateStream({ canManageDomains, capabilities, capabilitiesError }): boolean`; `streamsUnsupported(capabilities, capabilitiesError): boolean`.

- [ ] **Step 1: Write the failing test**

```ts
import { canCreateStream, streamsUnsupported } from './stream-capability';
import type { ProjectCapabilities } from '@/types';

const caps = (streamAvailable?: boolean): ProjectCapabilities => ({
  rateLimitAvailable: true,
  streamAvailable,
});

describe('stream-capability', () => {
  it('allows creation when supported and permitted', () => {
    expect(canCreateStream({ canManageDomains: true, capabilities: caps(true), capabilitiesError: false })).toBe(true);
  });
  it('blocks creation when streams unsupported', () => {
    expect(canCreateStream({ canManageDomains: true, capabilities: caps(false), capabilitiesError: false })).toBe(false);
  });
  it('still blocks when lacking permission even if supported', () => {
    expect(canCreateStream({ canManageDomains: false, capabilities: caps(true), capabilitiesError: false })).toBe(false);
  });
  it('is optimistic on capabilities fetch error', () => {
    expect(canCreateStream({ canManageDomains: true, capabilities: null, capabilitiesError: true })).toBe(true);
  });
  it('is optimistic when streamAvailable is absent (older backend)', () => {
    expect(canCreateStream({ canManageDomains: true, capabilities: caps(undefined), capabilitiesError: false })).toBe(true);
  });
  it('streamsUnsupported only true on a positive false, not on error/absent', () => {
    expect(streamsUnsupported(caps(false), false)).toBe(true);
    expect(streamsUnsupported(caps(undefined), false)).toBe(false);
    expect(streamsUnsupported(null, true)).toBe(false);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx jest src/lib/stream-capability.test.ts`
Expected: FAIL — cannot find module `./stream-capability`.

- [ ] **Step 3: Write minimal implementation**

Add to `src/types/index.ts` (the `ProjectCapabilities` interface):

```ts
export interface ProjectCapabilities {
  rateLimitAvailable: boolean;
  streamAvailable?: boolean;
}
```

Create `src/lib/stream-capability.ts`:

```ts
import type { ProjectCapabilities } from '@/types';

// streamsUnsupported is true ONLY when the backend positively reports streams
// unavailable. A missing field (older backend) or a fetch error is NOT
// "unsupported" — we fall open (optimistic), matching the rateLimitAvailable
// pattern and the backend's own optimistic default.
export function streamsUnsupported(
  capabilities: ProjectCapabilities | null,
  capabilitiesError: boolean,
): boolean {
  if (capabilitiesError || !capabilities) return false;
  return capabilities.streamAvailable === false;
}

// canCreateStream combines the existing RBAC gate with the capability gate.
export function canCreateStream(args: {
  canManageDomains: boolean;
  capabilities: ProjectCapabilities | null;
  capabilitiesError: boolean;
}): boolean {
  if (!args.canManageDomains) return false;
  return !streamsUnsupported(args.capabilities, args.capabilitiesError);
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npx jest src/lib/stream-capability.test.ts`
Expected: PASS (all cases).

- [ ] **Step 5: Commit**

```bash
git add src/types/index.ts src/lib/stream-capability.ts src/lib/stream-capability.test.ts
git commit -m "feat(streams): streamAvailable capability type + gating helper

🤖 Generated with [Claude Code](https://claude.com/claude-code)"
```

---

### Task F2: Gate the streams list and create pages

**Files:**
- Modify: `src/app/projects/[projectId]/streams/page.tsx` (`loadData` ~L20-39 to also fetch capabilities; the "New Stream" button ~L72-79 and empty-state "Create Stream" ~L92-99 gated via `canCreateStream`)
- Modify: `src/app/projects/[projectId]/streams/create/page.tsx` (`loadData` ~L35 to fetch capabilities; add an amber "unsupported" branch around the form ~L112-127; disable submit ~L190)

**Interfaces:**
- Consumes: `canCreateStream` / `streamsUnsupported` (Task F1); `projectsApi.getCapabilities` (`src/lib/api/projects.ts:49`); existing `permissionsApi.getProjectPermissions`.

- [ ] **Step 1: Write the failing test**

This task is UI wiring; the gating decision is already unit-tested in F1. Verification is typecheck + lint + build + a manual check (no new Jest test — a full app-router page render test would exercise the framework, not the decision). Confirm the baseline is green first:

Run: `npm run lint && npm run build`
Expected: PASS on the current tree (establishes the baseline before edits).

- [ ] **Step 2: Make the change — streams list page**

In `src/app/projects/[projectId]/streams/page.tsx`:
- In `loadData`, add `projectsApi.getCapabilities(projectId)` to the `Promise.all`, storing into `capabilities` state and a `capabilitiesError` boolean on rejection (follow the `rateLimitAvailable` pattern in the route create page). Fetch failure sets `capabilitiesError = true` and leaves `capabilities = null` (optimistic).
- Replace the bare `permissions?.canManageDomains` guard on the "New Stream" (L72-79) and empty-state "Create Stream" (L92-99) buttons with:
  `canCreateStream({ canManageDomains: !!permissions?.canManageDomains, capabilities, capabilitiesError })`.
- When `streamsUnsupported(capabilities, capabilitiesError)` is true, render a small amber note near the header: "Streams require Envoy Gateway ≥ 1.8".

- [ ] **Step 3: Make the change — create page**

In `src/app/projects/[projectId]/streams/create/page.tsx`:
- In `loadData` (L35), add `projectsApi.getCapabilities(projectId)` to the `Promise.all`; store `capabilities` + `capabilitiesError` as above.
- Add a branch: when `streamsUnsupported(capabilities, capabilitiesError)` is true, render an amber panel ("Streams require Envoy Gateway ≥ 1.8. Upgrade Envoy Gateway to create streams.") **instead of** the form — mirroring the existing "No stream-enabled Gateway Templates available" branch (L112-127).
- Disable the "Create Stream" submit button (L190) when `streamsUnsupported(...)` is true.
- On a capabilities fetch error, render a muted "Could not verify Envoy Gateway version" note and fall open (show the form) — mirroring the route create page's `capabilitiesError` handling.

- [ ] **Step 4: Verify**

Run: `npm run lint && npm run build`
Expected: PASS (typecheck + lint + production build clean).

Manual check (dev server against a backend):
- On a project whose cluster is EG ≥ 1.8 (`streamAvailable: true`) → "New Stream" enabled, create form shown.
- Simulate `streamAvailable: false` (e.g. point at an EG 1.7 project or stub the endpoint) → button hidden/disabled, amber "requires Envoy Gateway ≥ 1.8" shown, create page shows the amber branch instead of the form.
- Simulate a `/capabilities` 500 → UI falls open (form shown, muted "could not verify" note).

- [ ] **Step 5: Commit**

```bash
git add src/app/projects/[projectId]/streams/page.tsx src/app/projects/[projectId]/streams/create/page.tsx
git commit -m "feat(streams): gate stream creation UI on streamAvailable capability

🤖 Generated with [Claude Code](https://claude.com/claude-code)"
```
