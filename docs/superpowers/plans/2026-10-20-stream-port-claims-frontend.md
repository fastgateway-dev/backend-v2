# Stream Port Claims (Frontend) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stream managers claim ports when creating or editing a stream; L4 routes pick their port from the stream's free claims; the Entry point panel stops listing neighbours.

**Architecture:** One shared `PortClaimPicker` component serves stream create and an "Edit ports" modal on stream detail. Pure helpers in `src/lib/utils/stream-entrypoint.ts` compute claim problems and free claims, so components stay thin and testable. The L4 route form's free-text port becomes a `Select`.

**Tech Stack:** Next.js (app router), TypeScript, Jest + Testing Library, the in-repo UI kit (`src/components/ui`: `Badge`, `Button`, `Input`, `Select`, `Modal`, `InfoTooltip`).

**Spec:** `backend-v2/docs/superpowers/specs/2026-10-19-stream-port-claims-design.md` (Frontend F1–F6, Testing). The API is the **Contract** section of `backend-v2/docs/superpowers/plans/2026-10-20-stream-port-claims-backend.md`; read it first.

## Global Constraints

From the spec, verbatim:

- A claim is a **port plus a transport**: `5432/TCP`, `53/UDP`.
- A stream must claim **at least one port at create**. Submit is disabled until at least one valid claim exists.
- Removing a claim a route still uses is **409**; in the UI such a chip can't be removed (disabled, tooltip names the route).
- "Edit ports" is gated on `canManageDomains`.
- No-free-port copy: "All of this stream's TCP ports are in use. Claim another port on the stream." (UDP variant with "UDP"); the link to the stream only for users who can manage the stream.
- Legacy empty state: "No ports claimed. Claim a port to create this stream's Gateway."
- Remove "Also used by … / Show who shares it" (panel and `TemplateEntrypointSummary`) and "N ports used by other streams/domains" (panel).
- Remove 60000 from `PLATFORM_RESERVED`.

Repository rules:

- **Before starting:** in frontend-v2, `git checkout main && git pull --ff-only origin main && git checkout -b feat/stream-port-claims`.
- **Gate per task:** `npx tsc --noEmit`, `npm run lint` (0 errors), `npx jest <touched tests>`; full `npx jest` and `npx next build` in Task 6 (the `fastgateway-frontend-verify` skill).
- Commit messages are conventional commits ending with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

- **Typing a port that is already in the list** (same transport) shows "already claimed" and is not added twice; the same port on the other transport is allowed. Test: Task 2 `rejects a duplicate claim but allows the other transport`.
- **A shared-template port taken by the stream being edited itself** must not show as "taken" in the edit modal (the entry point lists the stream's own claims too). Test: Task 1 `claimProblem ignores the stream's own claims`.
- **Editing a route whose own port is the only claim** keeps that port selectable instead of showing the no-free-port message. Test: Task 5 `edit keeps the route's own port`.
- **Switching protocol in the route form** clears a selected port that isn't claimed for the new transport rather than submitting it. Test: Task 5 `protocol switch clears an invalid selection`.
- **Entry point fails to load on create** (amber box today): the picker still works with range/reserved checks only, and the server's 409 shows under the picker. Test: Task 3 `create still works when the entry point fails`.

---

### Task 1: Types, API clients and claim helpers

**Files:**
- Modify: `src/types/index.ts`, `src/lib/api/streams.ts`, `src/lib/utils/stream-entrypoint.ts`
- Test: `src/lib/utils/stream-entrypoint.test.ts`, `src/lib/api/streams.test.ts`

**Interfaces:**
- Produces:
  - `Stream.ports: TransportPort[]` (required now); `CreateStreamInput.ports: TransportPort[]`; `UpdateStreamInput { name?: string; ports?: TransportPort[] }`
  - `EntrypointPortOwner.kind: 'stream' | 'domain'`; `EntrypointPortOwner.pending?: boolean`
  - `type ClaimProblem = 'invalid' | 'reserved' | 'out_of_range' | 'duplicate' | 'taken'`
  - `claimProblem(claim: TransportPort, current: TransportPort[], ctx: { range: PortRange | null; reserved: ReservedPort[]; taken: EntrypointPort[]; selfStreamId?: string }): ClaimProblem | null`
  - `claimProblemMessage(p: ClaimProblem, claim: TransportPort, ctx): string` — copy: `invalid` "Port must be between 1 and 65535", `reserved` "<port>/<T> is reserved", `out_of_range` "Must be within <min>–<max>", `duplicate` "<port>/<T> is already claimed", `taken` "<port>/<T> is used by <owner>" (`ownerLabel`)
  - `myClaims(ports: EntrypointPort[], streamId: string): EntrypointPort[]` (replaces `splitPorts`)
  - `freeClaims(claims: TransportPort[], routes: StreamRoute[], transport: 'TCP'|'UDP', ownRouteId?: string): number[]`
  - Deleted: `splitPorts`, `distinctPortCount`, `sharedWithSummary` (if unused after Task 3/4), 60000 in `PLATFORM_RESERVED`

- [ ] **Step 1: Write failing tests** in `stream-entrypoint.test.ts`: `claimProblem` table (0 → invalid, 19000 → reserved, outside range → out_of_range, `{80,TCP}` in current → duplicate, `{80,UDP}` with `{80,TCP}` in current → null, taken by another stream on the same transport → taken, `60000/UDP` in range → null); `claimProblem ignores the stream's own claims` (taken entry with `owner.streamId === selfStreamId` → null); `freeClaims` (claims TCP 5432/5433, route on 5432 → `[5433]`; with `ownRouteId` of the 5432 route → `[5432, 5433]`; UDP → `[]`); `myClaims` filters `kind === 'stream' && streamId`. In `streams.test.ts`: `create` and `update` send `ports`.
- [ ] **Step 2: Run** `npx jest src/lib/utils/stream-entrypoint.test.ts src/lib/api/streams.test.ts` → FAIL.
- [ ] **Step 3: Implement** types, clients and helpers; fix call sites the type change breaks so `npx tsc --noEmit` passes (temporary `ports: []` where a test fixture builds a `Stream`).
- [ ] **Step 4: Run** the Step 2 command and `npx tsc --noEmit` → PASS.
- [ ] **Step 5: Commit** — `feat(streams): port-claim types, API and helpers`.

---

### Task 2: `PortClaimPicker` component

**Files:**
- Create: `src/components/streams/PortClaimPicker.tsx`, `src/components/streams/PortClaimPicker.test.tsx`

**Interfaces:**
- Consumes: Task 1 `claimProblem`, `claimProblemMessage`, `portLabel`, `comparePorts`, `freePortCount`, `withDefaultReserved`.
- Produces: `PortClaimPicker(props: { value: TransportPort[]; onChange(next: TransportPort[]): void; range: PortRange | null; reserved: ReservedPort[]; taken: EntrypointPort[]; selfStreamId?: string; lockedPorts?: { port: TransportPort; reason: string }[]; error?: string; disabled?: boolean })`.

- [ ] **Step 1: Write failing tests:** adds `5432/TCP` via transport toggle + number input + Add (and Enter), chips sorted with `comparePorts`; `rejects a duplicate claim but allows the other transport`; shows each `claimProblemMessage` inline and keeps the Add button disabled; removing a chip calls `onChange` without it; a `lockedPorts` chip has a disabled remove button and its `reason` as the tooltip; shows `Port range 1000–2000 · N of M free`; renders `error` under the picker.
- [ ] **Step 2: Run** `npx jest src/components/streams/PortClaimPicker.test.tsx` → FAIL.
- [ ] **Step 3: Implement** (visual design is the builder's call; transport toggle follows the protocol radio fieldset in `L4RouteForm.tsx`; chips are `Badge`s with a remove button).
- [ ] **Step 4: Run** the Step 2 command → PASS; `npx tsc --noEmit && npm run lint` → 0 errors.
- [ ] **Step 5: Commit** — `feat(streams): port claim picker`.

---

### Task 3: Stream create claims ports

**Files:**
- Modify: `src/app/projects/[projectId]/streams/create/page.tsx`, `src/components/streams/TemplateEntrypointSummary.tsx`
- Test: `src/app/projects/[projectId]/streams/create/page.test.tsx`

**Interfaces:**
- Consumes: Task 2 `PortClaimPicker`; `domainTemplatesApi.getEntrypoint` (already fetched by `TemplateEntrypointSummary`). Lift the entry point fetch to the page (or pass it via an `onLoaded` callback) so the picker gets `range`, `reserved`, `taken`.

- [ ] **Step 1: Update/write failing tests:** a Ports field appears after a template is picked; Create is disabled with no claims and enabled after adding `5432/TCP`; the POST body has `ports: [{transport:'TCP',port:5432}]`; a 409 response's `error` shows under the picker; the old test `a shared template shows who shares it…` is replaced by one asserting `Show who shares it` is **not** rendered while mode, IP and the free-port line still are; `create still works when the entry point fails` (getEntrypoint rejects → picker still adds an in-range port and submits).
- [ ] **Step 2: Run** `npx jest "src/app/projects/\[projectId\]/streams/create"` → FAIL.
- [ ] **Step 3: Implement.** Changing the template clears claims. Remove the shares block from `TemplateEntrypointSummary`.
- [ ] **Step 4: Run** the Step 2 command, `npx tsc --noEmit`, `npm run lint` → PASS.
- [ ] **Step 5: Commit** — `feat(streams): claim ports when creating a stream`.

---

### Task 4: Stream detail — panel cleanup and "Edit ports"

**Files:**
- Modify: `src/components/streams/StreamEntrypointPanel.tsx`, `src/app/projects/[projectId]/streams/[streamId]/page.tsx`
- Create: `src/components/streams/EditStreamPortsModal.tsx`, `src/components/streams/EditStreamPortsModal.test.tsx`
- Test: `src/components/streams/StreamEntrypointPanel.test.tsx`, `src/app/projects/[projectId]/streams/[streamId]/page.test.tsx`

**Interfaces:**
- Consumes: Task 1 `myClaims`; Task 2 `PortClaimPicker`; `streamsApi.update`, `streamsApi.getEntrypoint`.
- Produces: `EditStreamPortsModal(props: { projectId: string; stream: Stream; entrypoint: StreamEntrypoint; open: boolean; onClose(): void; onSaved(stream: Stream): void })`.

- [ ] **Step 1: Write failing tests:**
  - Panel: no `Also used by`, no `Show who shares it`, no `used by other streams/domains`; "This stream's ports" lists every claim: `5432/TCP → pg` (link + status badge), `5433/TCP → cache` with a `pending` badge, `53/UDP` with `unused`; with zero claims it shows `No ports claimed. Claim a port to create this stream's Gateway.`; the reserved and range lines remain.
  - Modal: starts from `stream.ports`; claims with a route are locked with reason `Used by route pg` (`Used by route cache (pending change)` when pending); Save PATCHes `{ports}` and calls `onSaved`; a 409 shows under the picker and keeps the modal open; Save disabled with zero claims.
  - Page: `Edit ports` button only when `canManageDomains`; after save the panel and the Manifests tab refresh (bump the existing `refreshKey`).
- [ ] **Step 2: Run** `npx jest src/components/streams/StreamEntrypointPanel.test.tsx src/components/streams/EditStreamPortsModal.test.tsx "src/app/projects/\[projectId\]/streams/\[streamId\]/page.test.tsx"` → FAIL.
- [ ] **Step 3: Implement**; delete helpers now unused (`sharedWithSummary`, `distinctPortCount`) and their tests.
- [ ] **Step 4: Run** the Step 2 command, `npx tsc --noEmit`, `npm run lint` → PASS.
- [ ] **Step 5: Commit** — `feat(streams): edit port claims; drop neighbour lists from the entry point panel`.

---

### Task 5: L4 route form picks a claimed port

**Files:**
- Modify: `src/components/streams/L4RouteForm.tsx`, `src/lib/utils/l4route.ts`
- Test: `src/components/streams/L4RouteForm.test.tsx`, `src/lib/utils/l4route.test.ts`

**Interfaces:**
- Consumes: Task 1 `freeClaims`; the stream (already loaded) now carries `ports`; `existingRoutes` (already loaded).
- Produces: `validateL4Form` port rule becomes "a port is selected"; `findPortCollision`, `rangeError` and the template range fetch for the port hint are deleted if nothing else uses them.

- [ ] **Step 1: Write failing tests:** the port field is a `Select` listing `freeClaims(stream.ports, existingRoutes, protocol)` as `5433/TCP`-style options; `protocol switch clears an invalid selection`; `edit keeps the route's own port`; with no free claims it shows `All of this stream's TCP ports are in use. Claim another port on the stream.` with a link to the stream when `canManageDomains`, text only otherwise, and Next/Submit is blocked; a server 400/409 still lands on the port field and jumps to the basic tab.
- [ ] **Step 2: Run** `npx jest src/components/streams/L4RouteForm.test.tsx src/lib/utils/l4route.test.ts` → FAIL.
- [ ] **Step 3: Implement.** The form needs the user's `canManageDomains` (use the same permission hook the stream detail page uses).
- [ ] **Step 4: Run** the Step 2 command, `npx tsc --noEmit`, `npm run lint` → PASS.
- [ ] **Step 5: Commit** — `feat(l4): route form picks a free claimed port`.

---

### Task 6: Stream list and full gate

**Files:**
- Modify: `src/app/projects/[projectId]/streams/page.tsx` (only if the Ports column needs a change), `src/lib/utils/stream-list.ts`
- Test: `src/app/projects/[projectId]/streams/page.test.tsx`, `src/lib/utils/stream-list.test.ts`

- [ ] **Step 1: Update tests** so the Ports column shows claims, including a stream with claims and zero routes (`5432/TCP` shown, Routes `0`).
- [ ] **Step 2: Run** `npx jest "src/app/projects/\[projectId\]/streams/page.test.tsx" src/lib/utils/stream-list.test.ts` → FAIL if the column hid claim-only ports; implement until PASS.
- [ ] **Step 3: Full gate:** `npx tsc --noEmit && npm run lint && npx jest && npx next build` → all pass, lint 0 errors; `grep -rn -E '60000|stream_route|who shares it|used by other streams' src/` is empty.
- [ ] **Step 4: Commit** — `feat(streams): list shows port claims`.
