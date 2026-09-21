# Next.js pessimization audit — output

| Field | Value |
|---|---|
| Source | equivalence-spec §5.4, "Pessimization audit — the Next.js app does not get measured until this passes" |
| Generated | `node scripts/audit.mjs` |
| Result | **6 PASS, 0 FAIL, 2 other** |
| Independent review | **None.** docs/OPERATOR-QUESTIONS.md Q-1: internal control only, disclosed in the report body, not a footnote |

> **This file is regenerated, not edited.** The Phase 5 turn re-runs
> `node scripts/audit.mjs` against the same apps and the result must still
> be all-PASS before a cell is recorded. A hand-edited tick is the exact
> failure mode T-2 describes.

## Checklist

| # | Item (§5.4) | Status |
|---|---|---|
| A-1 | no 'use client' at or near the root | **PASS** |
| A-2 | @next/bundle-analyzer output committed | **PASS** |
| A-3 | depcheck clean (no unused runtime dependency) | **PASS** |
| A-4 | production React confirmed in the built output | **PASS** |
| A-5 | no dev server, no HMR runtime in the bundle | **PASS** |
| A-6 | no artificial await or delay; dynamic routes are §5.5, not a handicap | **REVIEW** |
| A-7 | Lighthouse performance score recorded | **DEFERRED** |
| A-8 | every deviation from the docs' recommended pattern is listed with a reason | **PASS** |

## Detail

### A-1 — no 'use client' at or near the root  — PASS

```json
13 client boundaries, all below the route:
  apps/counter/next/src/components/CounterLive.tsx
  apps/counter/next/src/components/LocalCounter.tsx
  apps/counter/next/src/lib/transport/poll.ts
  apps/counter/next/src/lib/transport/sse.ts
  apps/counter/next/src/lib/transport/ws.ts
  apps/chat/next/src/components/ChatLive.tsx
  apps/chat/next/src/lib/transport/poll.ts
  apps/chat/next/src/lib/transport/sse.ts
  apps/chat/next/src/lib/transport/ws.ts
  apps/dashboard/next/src/components/DashboardLive.tsx
  apps/dashboard/next/src/lib/transport/poll.ts
  apps/dashboard/next/src/lib/transport/sse.ts
  apps/dashboard/next/src/lib/transport/ws.ts
```

### A-2 — @next/bundle-analyzer output committed  — PASS

```json
distilled to audit/bundle-analyzer/{counter,chat,dashboard}.json
```

### A-3 — depcheck clean (no unused runtime dependency)  — PASS

```json
{
  "counter": {
    "unused": [],
    "unusedDev": [
      "@next/bundle-analyzer",
      "@types/react-dom",
      "@types/ws"
    ],
    "explained": [
      "ws (ws-server/relay.mjs, outside the Next graph)"
    ]
  },
  "chat": {
    "unused": [],
    "unusedDev": [
      "@next/bundle-analyzer",
      "@types/react-dom",
      "@types/ws"
    ],
    "explained": [
      "ws (ws-server/relay.mjs, outside the Next graph)"
    ]
  },
  "dashboard": {
    "unused": [],
    "unusedDev": [
      "@next/bundle-analyzer",
      "@types/react-dom",
      "@types/ws"
    ],
    "explained": [
      "ws (ws-server/relay.mjs, outside the Next graph)"
    ]
  }
}
```

### A-4 — production React confirmed in the built output  — PASS

```json
{
  "counter": {
    "chunks": 20,
    "devBuildMarkers": []
  },
  "chat": {
    "chunks": 20,
    "devBuildMarkers": []
  },
  "dashboard": {
    "chunks": 19,
    "devBuildMarkers": []
  }
}
```

### A-5 — no dev server, no HMR runtime in the bundle  — PASS

```json
{
  "counter": {
    "hmrRuntimeMarkers": [],
    "devScripts": [],
    "benignReactRefreshConstant": 1,
    "note": "benignReactRefreshConstant counts files containing the STRING \"react-refresh\" (Next's build-output name table). It is not a dev runtime; hmrRuntimeMarkers is the check."
  },
  "chat": {
    "hmrRuntimeMarkers": [],
    "devScripts": [],
    "benignReactRefreshConstant": 1,
    "note": "benignReactRefreshConstant counts files containing the STRING \"react-refresh\" (Next's build-output name table). It is not a dev runtime; hmrRuntimeMarkers is the check."
  },
  "dashboard": {
    "hmrRuntimeMarkers": [],
    "devScripts": [],
    "benignReactRefreshConstant": 1,
    "note": "benignReactRefreshConstant counts files containing the STRING \"react-refresh\" (Next's build-output name table). It is not a dev runtime; hmrRuntimeMarkers is the check."
  }
}
```

### A-6 — no artificial await or delay; dynamic routes are §5.5, not a handicap  — REVIEW

```json
{
  "note": "REVIEW, not PASS: \"no artificial delay\" is a judgement, and a script that returned PASS for it would be asserting the judgement rather than supporting it. Every scheduled delay in the three apps is listed so a reviewer reads them. `unexplained` entries are multi-line calls whose delay argument is on a following line — open the file and read the three. The force-dynamic list is §5.5's fairness constraint, not a self-inflicted cost: the equivalent gotth-live route renders current session state and cannot be cached, so this one may not be either.",
  "unexplained": [
    {
      "file": "apps/chat/next/src/lib/fixture.ts",
      "line": "this.timer = setTimeout(() => {",
      "expected": null
    },
    {
      "file": "apps/dashboard/next/src/components/DashboardLive.tsx",
      "line": "debounce.current = setTimeout(() => {",
      "expected": null
    },
    {
      "file": "apps/dashboard/next/src/lib/fixture.ts",
      "line": "this.timer = setTimeout(() => {",
      "expected": null
    }
  ],
  "explained": 8,
  "forceDynamicRoutes": [
    "apps/counter/next/src/app/api/counter/presence/route.ts",
    "apps/counter/next/src/app/api/counter/snapshot/route.ts",
    "apps/counter/next/src/app/api/counter/stream/route.ts",
    "apps/counter/next/src/app/counter/page.tsx",
    "apps/counter/next/src/app/counter-local/page.tsx",
    "apps/chat/next/src/app/api/bench/clock/route.ts",
    "apps/chat/next/src/app/api/chat/snapshot/route.ts",
    "apps/chat/next/src/app/api/chat/stream/route.ts",
    "apps/chat/next/src/app/api/chat/typing/route.ts",
    "apps/chat/next/src/app/chat/[room]/page.tsx",
    "apps/dashboard/next/src/app/api/bench/clock/route.ts",
    "apps/dashboard/next/src/app/api/dashboard/snapshot/route.ts",
    "apps/dashboard/next/src/app/api/dashboard/stream/route.ts",
    "apps/dashboard/next/src/app/dashboard/page.tsx"
  ]
}
```

### A-7 — Lighthouse performance score recorded  — DEFERRED

```json
Requires a run against the §3.6 topology, which is a Phase 5 activity. §5.4: "a score materially below what the app's content warrants is treated as evidence of pessimization and investigated before measuring". Not run here, and not ticked.
```

### A-8 — every deviation from the docs' recommended pattern is listed with a reason  — PASS

```json
7 declared
```

## Client chunk sizes, gzip level 6

Informational, not the D1 figure. D1 counts what the PAGE fetches from
navigation start (§3.5), which is a subset of what is on disk, and it is
measured through CDP at the browser rather than by gzipping a directory.
This table exists so an unexpectedly large chunk is visible during
construction rather than at measurement time.

| app | chunk files | raw bytes | gzip-6 bytes |
|---|---:|---:|---:|
| counter | 18 | 808,531 | 250,878 |
| chat | 18 | 809,019 | 250,977 |
| dashboard | 17 | 811,595 | 251,419 |

## Declared deviations from the Next.js docs' recommended patterns (§5.4, last item)

### The typing heartbeat is a Route Handler, not a Server Action.

**Where:** `apps/chat/next/src/app/api/chat/typing/route.ts`  
**Why:** §5.4's table says mutations are Server Actions. React serialises Server Actions, so a keystroke heartbeat would queue in front of the user's Send and CHT-2 — the headline chat latency — would be measuring the heartbeat draining rather than the send. A fire-and-forget keepalive POST is what a competent team ships for a presence ping.

### Next's own gzip is off (compress: false); the proxy compresses.

**Where:** `apps/*/next/next.config.ts`  
**Why:** §3.5 mandates gzip level 6 for the comparison figure on both stacks and calls a mismatch a disqualifying method error. The only place one level can be guaranteed for both is the container they share — the §3.6 proxy. Leaving Next's compressor on as well would double-compress or make the effective level whichever layer won.

### Room switching and the dashboard controls are Server Actions, not navigations.

**Where:** `apps/chat/next/src/app/chat/[room]/actions.ts, apps/dashboard/next/src/app/dashboard/actions.ts`  
**Why:** §2 forbids client-side routing on both sides, and §3.2 requires t_input and t_paint to come from the same page's performance.now() timeline. A document navigation puts them in two timelines and makes CHT-4 unmeasurable under the spec's own definition.

### The dashboard push channel carries patches, not whole views.

**Where:** `apps/dashboard/next/src/lib/core.ts (Patch), lib/patch.ts`  
**Why:** A whole DashView is ~14 KB at perPage 200; pushing one twice a second would be 28 KB/s/session of which ~90 % is unchanged bytes, and §4.6's wire-byte row would be measuring an author's choice rather than a framework. This is what a perf-minded Next.js team ships.

### The store is keyed off a global Symbol rather than a module-level const.

**Where:** `apps/*/next/src/lib/store.ts`  
**Why:** Next bundles route handlers and Server Actions into separate server chunks, so a module-level singleton can be instantiated more than once. This is the recipe the Next docs use for a database client, not a benchmark-specific trick.

### Cookies are set by route handlers, not by middleware.

**Where:** `apps/*/next/src/lib/session.ts`  
**Why:** Middleware would run in its own runtime on every request to the measured route. Paying that on a route that does not need it is a self-inflicted Next.js cost — exactly what this audit exists to catch.

### The filter and rows-per-page controls are buttons, not <select>s.

**Where:** `apps/dashboard/next/src/components/DashboardLive.tsx`  
**Why:** §2.4 writes "select 50 / 100 / 200", which reads as "choose one of". Buttons make DSH-1 and DSH-4 a native pointerdown, which is what §3.2's t_input is defined against; a <select> would put the causal start in a change event the spec does not define.

## Client boundaries (§5.4, first item)

Every `'use client'` in the three apps. None is at or near a route root;
each app has exactly one boundary on its measured route plus its transport
module, because the regions are views of ONE subscription and splitting
them would open one connection per region — an architecture no competent
team ships, and one D3 would then charge Next.js for.

- `apps/counter/next/src/components/CounterLive.tsx`
- `apps/counter/next/src/components/LocalCounter.tsx`
- `apps/counter/next/src/lib/transport/poll.ts`
- `apps/counter/next/src/lib/transport/sse.ts`
- `apps/counter/next/src/lib/transport/ws.ts`
- `apps/chat/next/src/components/ChatLive.tsx`
- `apps/chat/next/src/lib/transport/poll.ts`
- `apps/chat/next/src/lib/transport/sse.ts`
- `apps/chat/next/src/lib/transport/ws.ts`
- `apps/dashboard/next/src/components/DashboardLive.tsx`
- `apps/dashboard/next/src/lib/transport/poll.ts`
- `apps/dashboard/next/src/lib/transport/sse.ts`
- `apps/dashboard/next/src/lib/transport/ws.ts`

