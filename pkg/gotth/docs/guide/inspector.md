# The session inspector

At the end of this page you can open a panel in the browser that shows, for the
session on the page in front of you, every event you sent, the state version
the server moved to, and the patches each one produced — joined by the causal
identifiers the frames already carry. You will also know exactly what makes it
impossible to ship it by accident.

It is a development tool. It does not load in production, and the mechanism for
that is two lines of `if`, not a promise.

---

## Turn it on

Two changes. Set `Dev` on the config:

<!-- sample: none — your application's config and layout, not a sample package. The API these two blocks use is compiled and run by ExampleApp_InspectorScript in live/example_test.go, which asserts its exact rendered output. -->
```go
app, err := live.New(live.Config[State]{
	// ...
	Dev: os.Getenv("ENV") == "dev",
})
```

**If your page uses `app.Document`, you are already done.** The shell emits the
inspector, the runtime and the dev-reload tags itself, in that order, so there
is nothing to add and nothing to order — skip to
[what the panel shows](#what-the-panel-shows). The rest of this section is for a
hand-written layout.

Two things not to do on a page that uses `Document`, in increasing order of how
much they cost you:

- Adding `@app.InspectorScript(…)` as head content puts a **second inspector**
  on the page. Harmless — it still lands above the runtime — but pointless.
- Adding `@live.Script(…)` as head content is the mistake this whole page is
  about, and it is **refused**: that tag would land *above* the inspector's, and
  a deferred runtime that opens its socket before the inspector wraps
  `WebSocket` leaves the panel showing nothing at all. `Script` returns an error
  there and `Document` renders no page, so you get a `500` naming the problem
  instead of an inspector that is quietly blind. If your page really must place
  its own runtime tag, it is not using `Document`'s: pass `live.NoRuntime` as
  the mount path and `Document` emits none of the three.

Render its tag **above** `live.Script`'s:

<!-- sample: none — your layout. The ordering it demonstrates is asserted in Go by ExampleApp_InspectorScript, which renders both tags and pins the bytes. -->
```templ
templ layout() {
	<html>
		<head>
			@app.InspectorScript("/live")
			@live.Script("/live")
		</head>
		<body>
			{ children... }
		</body>
	</html>
}
```

Both take the same mount path — the prefix your router reaches the live handler
at, as the *browser* sees it. If `live.Script("/live")` works for you,
`InspectorScript("/live")` works for you.

Reload the page. A panel appears in the bottom-right corner.

### The order is not decorative

`InspectorScript` first, `live.Script` second. Both tags are `defer`, deferred
scripts run in document order, and the inspector has to replace the `WebSocket`
constructor *before* the runtime opens a socket with it — which the runtime
does during its own script execution.

Get it backwards and nothing breaks: the page is live, the panel opens, and it
tells you it booted too late and is watching nothing. It will pick up the next
connection. Move the tag and reload.

### With `Dev` false

`InspectorScript` writes **zero bytes**, so the page has no reference to the
inspector at all, and `GET /live/gotth-live-inspector.min.js` answers `404`
with a body naming `live.Config.Dev`. The template does not change between
environments; the tag simply is not there in one of them.

---

## What the panel shows

A header line, then rows, newest first.

```
gotth-live  a1b2c3d4  live  seq 42  v18  84↓ 61↑        pause  clear  copy  ▼
─────────────────────────────────────────────────────────────────────────────
#31 ↓ patch 12  seq 42 · v18  EFFECT effect:chat.broadcast  · APPEND log · 1.9 ms
#30 ↑ chat.send  ref 7 · log  → event 41 · v17 · 1 patch
#29 ↓ snapshot 1  seq 1 · v1  MOUNT mount  · MORPH log, MORPH composer
```

The header is the session: its id, the connection status, the highest
`server_seq` applied, the current `state_version`, and the frame counts in each
direction. `pause` stops recording without closing anything, `clear` empties
the log, `copy` puts the whole thing on the clipboard as JSON for a bug report.

Every row expands. What is behind each one is exactly the frame's own fields —
nothing on this panel is computed from the runtime's internal state, and
nothing is inferred.

| Row | What it is | Expands to |
|---|---|---|
| `↑ <name>` | an event this browser sent | `client_ref`, `seen_server_seq`, the fields, and — once a patch answers it — the `event_id`, `transition_id` and `state_version` the server minted, and the row numbers of the patches it produced |
| `↓ patch` / `↓ snapshot` | a frame the server sent | `server_seq`, `patch_id`, `transition_id`, `state_version`, the whole `Origin`, the supersession range on a resync snapshot, the client-side morph and apply timings, and each `FragmentUpdate` with its op, its byte length, and its markup **as text** |
| `↑ resync request` | the client asking for a snapshot after a gap | `last_applied_seq` and the reason |
| `↓ error` | an error naming nothing this browser sent | the code, the message, `fatal`, and the ids |

**An error that names an event lands on that event's row**, not on a row of its
own, because it is that event's outcome rather than a separate step.

**Acks and heartbeats are counted, not listed.** An ack is the high-water mark
the header already shows, and a heartbeat echo says only that the socket is
alive, which the status says better. They are in the frame counts.

---

## Reading a causal chain

This is the thing the panel is for. One click, followed through:

1. **`#30 ↑ chat.send ref 7`** — the browser sent `Event{client_ref: 7}`. At
   this instant the row says `→ no patch yet`, because the client has not been
   told anything yet.
2. **`#31 ↓ patch 8 seq 41 CLIENT_EVENT event:chat.send ← #30`** — the patch
   that answered it. `Origin.client_ref` is 7, so the panel joins it to row 30;
   `Origin.event_id` is 41, and row 30 now shows `→ event 41 · v17`.
3. **`#33 ↓ patch 12 seq 42 EFFECT effect:chat.broadcast`** — the effect the
   transition scheduled, arriving later with `contributing_event_ids: [41]`.
   Expanded, that reads `event 41 = #30 chat.send`.

That is FR-39 through FR-42 on screen: every patch names its cause, and a
server-initiated patch names the events that contributed to it.

### One thing it cannot do, and why

`contributing_event_ids` holds server-minted `event_id`s. The client learns the
mapping from a `client_ref` to an `event_id` only when a patch carries both —
and an event whose transition produced **no patch** (a suppressed render) is
never announced to the browser at all.

So an id the browser was never told about shows as a bare number:

```
contributing_event_ids: event 41 = #30 chat.send
                        event 99 (not seen by this client)
```

This is not a gap to be worked around in the browser. The **server-side
provenance log** ([instrumentation.md §4A](../instrumentation.md)) has one
record per transition including the suppressed ones, which is the only place
that lookup can be complete. The panel says which ids it could not resolve
rather than quietly dropping them or inventing names.

---

## The HTMX ownership warning

The panel also watches the DOM. After every patch it walks each live fragment
and reports any element carrying an `hx-*` attribute that is **not** inside a
`data-gotth-preserve` subtree:

> `<div#feed>` carries `hx-get` inside fragment "panel" and is not marked
> `data-gotth-preserve`: morph owns it, so the next patch overwrites it
> (RFC-0001 §10.3)

That is the precedence rule from
[guide/htmx-interop.md](htmx-interop.md), enforced where you can see it. The
server deliberately does not scan rendered HTML for `hx-*` — it would cost CPU
on every render for a development-time mistake — so this panel is the only
thing that catches it, and it catches it before the patch that reverts your
HTMX swap arrives minutes later.

Warnings are collapsed: the same element is reported once with a count, not
once per frame.

---

## What it costs, and what it is not

**It costs the production client nothing.** Not "a little": zero bytes. The
runtime has no hook for the inspector, does not know it exists, and did not
change by a single byte in the landing that added it. The inspector reads the
session's frames by wrapping the `WebSocket` constructor and decoding them with
its own copy of the same generated codec. `client/SIZE.md` §2.1 is the
measurement and the argument.

| | Bytes gzipped |
|---|---:|
| `gotth-live.min.js` — every page, always | 4,459 |
| `gotth-live-inspector.min.js` — dev only | 6,211 |
| Its ceiling (PRD NFR-8) | 40,960 |

The first row read **4,429** until 2026-08-05 and has moved twice since the
inspector landed — down to 4,421 for FR-54's per-binding options, then up to
**4,459** for `Bind.NoModifiers` and `Bind.PreventDefault` (`2311280b`).
**Neither landing was this one**, so the "zero bytes" claim above is unaffected:
it is a claim about the inspector, and the inspector still costs the production
runtime nothing. `client/SIZE.md` §1.1 attributes every byte of both moves.

**It is safe to open on a page that is not yours.** The panel builds its DOM
through `textContent`; a patch's markup is shown as text and never parsed, so a
fragment cannot execute anything by being inspected. There is no `eval` in it,
and it works under a strict CSP with no `'unsafe-inline'`: the panel lives in a
shadow root styled by a constructed stylesheet, which is CSSOM rather than an
inline `<style>`.

**It only watches gotth-live sockets.** The wrap is filtered on the
`gotth-live.v1` subprotocol, so any other WebSocket your application holds is
left alone and unread.

**It is a window, not a recording.** The log is a ring of the most recent 500
rows and each fragment's markup is kept to the first 4 KB, so an inspector left
open for an afternoon does not grow without bound. For a real recording — every
transition, including the ones that produced no patch, queryable after the fact
— the server-side provenance log is the answer, and it is one `Config.Logger`
away ([guide/observability.md](observability.md)).

### Honest limits

- **The bytes are in your binary either way.** `Config.Dev` gates *serving* and
  *rendering*, not embedding: the artifact is compiled into the binary like any
  other embedded asset. Nothing hands it to a browser in production, and no
  production page names it, but `strings` will find it.
- **It shows one session** — the one on this page. A list of every live session
  on the server is a different tool, and it is filed as backlog BL-18 rather
  than half-built here.
- **It cannot replay.** Time-travel debugging against your reducer is BL-16.
- **Constructed stylesheets are required for it to look right.** Without them
  the panel still works and is still readable; it is just unstyled, and it says
  so in its own warnings. Adding a `<style>` element as a fallback would need
  the page's CSP relaxed, which is a worse trade for a dev tool.
- **The panel itself has no automated browser test yet.** Its model, its audit
  and its shipped bytes are covered by suites that run on every CI run; the
  rendering was verified by hand in headless Chromium 151 on 2026-08-05 — one
  click on a real session, showing the chain and the hx-* warning above — and
  that check is not committed. It is worth knowing because it is exactly where
  the one defect found during development lived: a `requestAnimationFrame` call
  that threw, which every non-browser test passed straight through.
  `client/SIZE.md` §8 records the same thing beside the suites that do run.

---

## Related

- [guide/observability.md](observability.md) — the server-side provenance log,
  which is where the same chain lives after the tab is closed.
- [guide/htmx-interop.md](htmx-interop.md) — the ownership rule the panel's
  warning enforces.
- [protocol.md](../protocol.md) §3.3 and §4.2 — the frames and the chain, which
  is every field the panel can show.
