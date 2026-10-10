# Gateway Template Listener Model — Frontend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **Repo:** Implemented in `frontend-v2` (Next.js). Create a worktree/branch there. **Depends on the backend plan having shipped** the listener API (`listeners[]` on templates, `boundListeners[]` on domain create, template TCP/UDP range).

**Goal:** Replace the template form's TLS dropdown + capability checkboxes with a listener list grouped by family (HTTP/gRPC with add-rows, TLS passthrough shown disabled/"planned", TCP/UDP port range); add a per-domain listener multi-select to domain create; and validate stream route ports against the template's range.

**Architecture:** Isolate all decision logic in pure, Jest-tested helpers (`lib/`); the app-router pages (template create/edit, domain create, stream L4 route form) consume the helpers and are verified by typecheck/build + manual check, mirroring the existing `rateLimitAvailable` gating pattern.

**Tech Stack:** Next.js 16 (app router), TypeScript, axios (`lib/api/*`), Jest + React Testing Library, plain `useState`/`useEffect`.

**Spec:** `../specs/2026-10-10-gateway-template-listeners-design.md` (in backend-v2).

## Global Constraints

- PR description footer EXACTLY: `🤖 Generated with [Claude Code](https://claude.com/claude-code)`.
- Hard cutover: remove the old `tlsMode`/`tlsPolicy`/`httpPort`/`httpsPort`/`enableDomain`/`enableStream` fields from the TS types and forms (no back-compat).
- The template create and edit pages currently **duplicate** the form — factor a shared listener-form component so both change once.
- TLS passthrough is **disabled/"planned"** in the UI (visible, not selectable).
- Verify with `npm run lint` + `npm run build` (typecheck) and the Jest helper tests; manual check against a dev backend.

## Review Focus

- **Template with no listeners** → submit disabled + "at least one listener" message (covered: F2).
- **Port conflict in the form** (two listeners same port, or range overlapping a fixed port) → inline error before submit; backend 409/400 surfaced as authoritative (covered: F2, F3).
- **Domain with zero bound listeners** → create disabled (covered: F4).
- **Stream route port outside the template range** → inline rejection + backend 400 surfaced (covered: F4).
- **TLS passthrough** selection → not possible (control disabled) (covered: F3).

---

### Task F1: TypeScript types for the listener model

**Files:**
- Modify: `src/types/index.ts` (or the template/domain type files) — add `TemplateListener`, `ListenerProtocol`, `ListenerTLSMode`; add `listeners: TemplateListener[]` to the template type + `CreateDomainTemplateRequest`; remove `tlsMode`/`tlsPolicy`/`httpPort`/`httpsPort`/`enableDomain`/`enableStream`; add `boundListeners: string[]` to `CreateDomainRequest`; drop the TLS/port fields from the `Domain` type.
- Test: none (type-only; verified by build in later tasks).

**Interfaces:**
- Produces: `TemplateListener { name; protocol; port?; tlsMode?; portRangeMin?; portRangeMax? }`; `ListenerProtocol = 'HTTP'|'HTTPS'|'TLS'|'TCP'|'UDP'`; `ListenerTLSMode = 'Terminate'|'Passthrough'`.

- [ ] **Step 1: Add the types**

```ts
export type ListenerProtocol = 'HTTP' | 'HTTPS' | 'TLS' | 'TCP' | 'UDP';
export type ListenerTLSMode = 'Terminate' | 'Passthrough';

export interface TemplateListener {
  name: string;
  protocol: ListenerProtocol;
  port?: number;            // HTTP/HTTPS/TLS
  tlsMode?: ListenerTLSMode; // HTTPS/TLS
  portRangeMin?: number;    // TCP/UDP
  portRangeMax?: number;    // TCP/UDP
}
```
Add `listeners: TemplateListener[]` to the `GatewayTemplate`/`DomainTemplate` type and `CreateDomainTemplateRequest`; add `boundListeners: string[]` to `CreateDomainRequest`; remove the old fields from all three.

- [ ] **Step 2: Verify it compiles in isolation**

Run: `npx tsc --noEmit` (expect errors only in the pages that still reference removed fields — those are fixed in F3/F4; the types file itself must be well-formed).

- [ ] **Step 3: Commit**

```bash
git add src/types/
git commit -m "feat(types): listener model for gateway templates + domains

🤖 Generated with [Claude Code](https://claude.com/claude-code)"
```

---

### Task F2: Pure listener-form helpers (validation, port conflict)

**Files:**
- Create: `src/lib/gateway-listeners.ts`
- Test: `src/lib/gateway-listeners.test.ts`

**Interfaces:**
- Produces: `listenerPortConflict(listeners: TemplateListener[]): string | null` (returns a human message or null); `canSubmitTemplate(listeners: TemplateListener[]): boolean`; `families(listeners): { http: TemplateListener[]; tls: TemplateListener[]; stream: TemplateListener | null }` (grouping for the form); `RESERVED_PORTS = [19000, 19001, 60000]`.

- [ ] **Step 1: Write the failing test**

```ts
import { listenerPortConflict, canSubmitTemplate } from './gateway-listeners';
import type { TemplateListener } from '@/types';

const L = (p: TemplateListener['protocol'], port?: number, extra: Partial<TemplateListener> = {}): TemplateListener =>
  ({ name: `${p}-${port ?? ''}`, protocol: p, port, ...extra });

describe('gateway-listeners', () => {
  it('no conflict for http:80 + https:443 + tcp range', () => {
    expect(listenerPortConflict([L('HTTP', 80), L('HTTPS', 443), { name: 't', protocol: 'TCP', portRangeMin: 9000, portRangeMax: 9100 }])).toBeNull();
  });
  it('flags two listeners on the same port', () => {
    expect(listenerPortConflict([L('HTTP', 443), L('HTTPS', 443)])).toMatch(/443/);
  });
  it('flags a fixed port inside the TCP/UDP range', () => {
    expect(listenerPortConflict([L('HTTPS', 9050), { name: 't', protocol: 'TCP', portRangeMin: 9000, portRangeMax: 9100 }])).toMatch(/range/i);
  });
  it('flags a reserved port', () => {
    expect(listenerPortConflict([L('HTTP', 19000)])).toMatch(/reserved/i);
  });
  it('canSubmit requires at least one listener', () => {
    expect(canSubmitTemplate([])).toBe(false);
    expect(canSubmitTemplate([L('HTTP', 80)])).toBe(true);
  });
});
```

- [ ] **Step 2: Run** `npx jest src/lib/gateway-listeners.test.ts` → FAIL (module missing).

- [ ] **Step 3: Implement** `gateway-listeners.ts` — grouping by family, port-conflict detection (duplicate fixed port; fixed port within the TCP/UDP range; reserved-port use; range min<=max), and `canSubmitTemplate` (non-empty). Mirror the backend's `ValidateTemplateListeners` rules so client and server agree.

- [ ] **Step 4: Run** `npx jest src/lib/gateway-listeners.test.ts` → PASS.

- [ ] **Step 5: Commit**

```bash
git add src/lib/gateway-listeners.ts src/lib/gateway-listeners.test.ts
git commit -m "feat(templates): pure listener-form helpers (validation, conflict)

🤖 Generated with [Claude Code](https://claude.com/claude-code)"
```

---

### Task F3: Template form — listener list grouped by family (shared component)

**Files:**
- Create: `src/components/features/gateway-template-listener-form.tsx` (the shared listener editor)
- Modify: `src/app/projects/[projectId]/domain-templates/create/page.tsx` (replace TLS menu + capability checkboxes with the shared component; map to `listeners[]` on submit)
- Modify: `src/app/projects/[projectId]/domain-templates/[domainTemplateId]/edit/page.tsx` (same)

**Interfaces:**
- Consumes: F1 types, F2 helpers.

- [ ] **Step 1: Baseline green** — `npm run lint && npm run build` on the current tree (before edits).

- [ ] **Step 2: Build the shared component** — three family sections: **HTTP/gRPC** (list of HTTP/HTTPS rows, each a port + HTTPS carries a Terminate badge; "+ Add HTTP/HTTPS listener"), **TLS passthrough** (a disabled, "PLANNED" section; no add control), **TCP/UDP** (min–max range inputs). Call `listenerPortConflict` live and show the message; disable submit via `canSubmitTemplate`. Emits `TemplateListener[]`.

- [ ] **Step 3: Wire both pages** — replace the `tlsMode`/`tlsPolicy`/`httpPort`/`httpsPort` controls and the enable-domain/enable-stream checkboxes with `<GatewayTemplateListenerForm>`; the submit payload sends `{ ...other fields, listeners }` (no TLS/port/enable fields). The edit page loads `template.listeners` into the component.

- [ ] **Step 4: Verify**

Run: `npm run lint && npm run build`
Expected: PASS. Manual: create a template with HTTP :80 + HTTPS :443 + TCP/UDP 9000–9100 → saved; duplicate-port shows inline error; TLS section visibly disabled.

- [ ] **Step 5: Commit**

```bash
git add src/components/features/gateway-template-listener-form.tsx "src/app/projects/[projectId]/domain-templates/"
git commit -m "feat(templates): listener-list form replaces the TLS menu

🤖 Generated with [Claude Code](https://claude.com/claude-code)"
```

---

### Task F4: Domain listener checklist + stream route range validation

**Files:**
- Create: `src/lib/stream-port-range.ts` + `src/lib/stream-port-range.test.ts` (pure range check)
- Modify: `src/app/projects/[projectId]/domains/create/page.tsx` (add the bound-listener multi-select; send `boundListeners[]`)
- Modify: `src/components/streams/L4RouteForm.tsx` + `src/lib/utils/l4route.ts` (validate the port against the template's range)

**Interfaces:**
- Produces: `portInRange(port: number, min: number, max: number): boolean`; `rangeError(port, min, max): string | null`.

- [ ] **Step 1: Write the failing test**

```ts
import { portInRange, rangeError } from './stream-port-range';
describe('stream-port-range', () => {
  it('accepts in-range', () => { expect(portInRange(9042, 9000, 9100)).toBe(true); });
  it('rejects out-of-range', () => { expect(portInRange(8125, 9000, 9100)).toBe(false); });
  it('rangeError messages out-of-range only', () => {
    expect(rangeError(9042, 9000, 9100)).toBeNull();
    expect(rangeError(8125, 9000, 9100)).toMatch(/9000.*9100/);
  });
});
```

- [ ] **Step 2: Run** `npx jest src/lib/stream-port-range.test.ts` → FAIL.

- [ ] **Step 3: Implement** `stream-port-range.ts` (trivial range check + message). Then:
  - **Domain create:** after choosing a template, render a checklist of its **hostname-routed** listeners (`listeners.filter(l => ['HTTP','HTTPS','TLS'].includes(l.protocol))`, TLS shown disabled); require ≥1 checked; submit `boundListeners: string[]` of the checked names. (Replaces the implicit TLS-mode behavior.)
  - **L4 route form:** read the stream's template range; use `rangeError(port, min, max)` to show an inline error; keep surfacing the backend's authoritative 400 (`ErrPortOutOfRange`).

- [ ] **Step 4: Verify**

Run: `npx jest src/lib/stream-port-range.test.ts && npm run lint && npm run build`
Expected: PASS. Manual: domain create shows the listener checklist (TLS disabled); an L4 route port outside the template range shows the inline error and is rejected by the backend.

- [ ] **Step 5: Commit**

```bash
git add src/lib/stream-port-range.ts src/lib/stream-port-range.test.ts "src/app/projects/[projectId]/domains/create/page.tsx" src/components/streams/L4RouteForm.tsx src/lib/utils/l4route.ts
git commit -m "feat(domains,streams): bound-listener checklist + route port-range check

🤖 Generated with [Claude Code](https://claude.com/claude-code)"
```
