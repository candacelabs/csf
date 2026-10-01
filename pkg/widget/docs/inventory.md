# Widget concept inventory

A flat harvest of the concepts a widget language has to be able to say, taken
from code that already exists rather than from imagination. Every entry cites
the file and line it came from, so a later reader can check that the language
is describing this repository and not a generic diagramming problem.

This document is deliberately **untyped**. It is the raw list; the typing
happens in [`ontology.md`](ontology.md), and entries that cannot survive
typing are cut there with the reason recorded. Nothing here is a commitment to
put a construct in the language.

Four sources were harvested:

| Source | What it contributes |
|---|---|
| `go/services/candace-cloud/internal/homepage/` | The one shipped widget: its markup, its state, its labels, its motion |
| `candace/pkg/gotth/live` | The runtime contract any widget must fit inside |
| `candace/services/candaceos/webui/palette.go` | The validated-token pattern the language should copy rather than reinvent |
| The widget lifecycle | The verbs a host performs on a widget |

Line references are to the tree at the commit that introduces this file.

---

## 1. The shipped widget — markup

Source: `go/services/candace-cloud/internal/homepage/view.templ`.

| # | Concept | Real name in the source | Where |
|---|---|---|---|
| 1.1 | The widget itself, as a renderable unit | `templ ControlPlane(state State)` | view.templ:258 |
| 1.2 | Widget root element | `<aside class="system-card">` | view.templ:259 |
| 1.3 | Live-region marker on the root | `live.Region(FragmentControlPlane)` | view.templ:260 |
| 1.4 | Accessible name of the widget | `aria-labelledby="system-card-title"` | view.templ:261 |
| 1.5 | Monotonic tick exposed to CSS | `data-sequence={ state.SequenceText() }` | view.templ:263 |
| 1.6 | Motion-paused flag exposed to CSS | `data-paused={ state.PausedText() }` | view.templ:264 |
| 1.7 | Animation-eligible flag exposed to CSS | `data-live={ state.LiveText() }` | view.templ:265 |
| 1.8 | Provenance line ("where this data comes from") | `<p class="micro-label">Warden / read-only protobuf stream</p>` | view.templ:269 |
| 1.9 | Widget title | `<h2 id="system-card-title">Raft-style heartbeats</h2>` | view.templ:270 |
| 1.10 | Connection chip: a dot plus bound text | `<span class="observing">…{ state.LinkLabel() }</span>` | view.templ:273 |
| 1.11 | Motion toggle control | `<button class="motion-toggle" …>` | view.templ:274-279 |
| 1.12 | Toggle pressed-state, from widget state | `aria-pressed={ state.PausedText() }` | view.templ:276 |
| 1.13 | DOM-event to server-event binding | `live.On("click", EventToggleMotion)` | view.templ:278 |
| 1.14 | Toggle caption, from widget state | `{ state.MotionAction() }` | view.templ:279 |
| 1.15 | Per-tick element identity (restarts finite animations) | `id={ state.PulseMapID() }` | view.templ:284 |
| 1.16 | The scene | `<div class="system-map" role="img" …>` | view.templ:283-288 |
| 1.17 | Scene text alternative | `aria-label={ state.DiagramLabel() }` | view.templ:287 |
| 1.18 | Decorative orbit rings | `<span class="map-orbit orbit-a" aria-hidden="true">` | view.templ:289-290 |
| 1.19 | Edges between nodes | `<span class="map-line line-a" aria-hidden="true">` | view.templ:291-292 |
| 1.20 | Node with marker, title and caption | `<span class="map-node node-edge"><i/><b>voter 01</b><small>follower</small></span>` | view.templ:293 |
| 1.21 | Node whose title and caption are bound to state | `<b>{ state.LeaderLabel() }</b><small>{ state.LeaderDetail() }</small>` | view.templ:294 |
| 1.22 | A second, differently-classed peer node | `<span class="map-node node-gpu">…<b>voter 02</b>` | view.templ:295 |
| 1.23 | Channel legend | `<span class="flow-key"><i class="heartbeat-key"/>heartbeat <i class="ack-key"/>ack</span>` | view.templ:299 |
| 1.24 | Footer statistics row | `<span class="cluster-stats">` | view.templ:300 |
| 1.25 | Three bound statistics | `state.TermLabel()`, `state.QuorumLabel()`, `state.TelemetryLabel()` | view.templ:301-303 |
| 1.26 | A second, sibling widget in the same document | `templ ReadyTime(state State)` | view.templ:234 |
| 1.27 | Polite live-region announcement | `aria-live="polite"` | view.templ:238 |
| 1.28 | A hidden control that reports a browser measurement | `<input id="gotth-ready-time" type="hidden" … live.On("change", …)>` | view.templ:249-254 |
| 1.29 | The host document the widgets mount into | `@app.Document(MountPath, "…", templ.Attributes{"lang": "en"}, headExtras())` | view.templ:6 |

## 2. The shipped widget — state, labels and events

Source: `go/services/candace-cloud/internal/homepage/app.go`.

| # | Concept | Real name in the source | Where |
|---|---|---|---|
| 2.1 | Host mount path | `MountPath = "/_candace/live"` | app.go:15 |
| 2.2 | Fragment (region) identifiers | `FragmentControlPlane`, `FragmentReadyTime` | app.go:17-18 |
| 2.3 | Declared inbound event names | `EventToggleMotion`, `EventBrowserReady` | app.go:19-20 |
| 2.4 | An event's field name | `fieldReadyMilliseconds = "ready_ms"` | app.go:22 |
| 2.5 | The widget's whole state, as one struct | `type State struct` | app.go:27-39 |
| 2.6 | Monotonic counter field | `Sequence uint64` | app.go:28 |
| 2.7 | Boolean condition fields | `Connected`, `Authoritative`, `LeaderKnown`, `HasQuorum`, `Paused`, `Degraded` | app.go:29-37 |
| 2.8 | Numeric measurement fields | `Term uint64`, `Voters int`, `AliveVoters int`, `ReadyMS uint32` | app.go:33-38 |
| 2.9 | Ordered text decision over state | `LeaderLabel()` — unavailable / elected / pending | app.go:41-49 |
| 2.10 | A second ordered text decision on the same conditions | `LeaderDetail()` | app.go:51-59 |
| 2.11 | Bound accessible description of the scene | `DiagramLabel()` | app.go:61-69 |
| 2.12 | Tick rendered as text | `SequenceText()` | app.go:71 |
| 2.13 | Tick used to mint a DOM identity | `PulseMapID()` | app.go:73-75 |
| 2.14 | A projection of state used only to decide "did this region change" | `controlPlaneState()` | app.go:81-84 |
| 2.15 | A named composite boolean over state | `live() = Connected && Authoritative && LeaderKnown && HasQuorum && !Degraded` | app.go:92-94 |
| 2.16 | Control caption that depends on state | `MotionAction()` — "Pause" / "Resume live pulses" | app.go:96-101 |
| 2.17 | Five-way ordered status text | `LinkLabel()` | app.go:103-118 |
| 2.18 | Four-way ordered telemetry text | `TelemetryLabel()` | app.go:120-131 |
| 2.19 | A numeric field formatted with a fallback | `TermLabel()` — "term —" when disconnected | app.go:133-138 |
| 2.20 | A pair of numeric fields formatted together | `QuorumLabel()` — "n / m voters alive" | app.go:140-145 |
| 2.21 | The state transition function | `Reducer(feed) live.Reducer[State]` | app.go:147-182 |
| 2.22 | A toggle transition | `case EventToggleMotion: state.Paused = !state.Paused` | app.go:149-151 |
| 2.23 | A write-once transition | `case EventBrowserReady:` (ignored when already set) | app.go:153-162 |
| 2.24 | An external-snapshot transition | `case EventSnapshot: applySnapshot(...)` | app.go:164-165 |
| 2.25 | Runtime-minted degradation events | `live.SlowClientEvent`, `live.ClientRecoveredEvent` | app.go:167-172 |
| 2.26 | Failure-driven retry | `case live.EffectFailedEvent:` → re-issue `WatchEffect` | app.go:173-179 |
| 2.27 | Monotonicity guard on inbound data | `if err != nil \|\| sequence <= state.Sequence { return state }` | app.go:186-188 |
| 2.28 | Whole-snapshot validity guard | the joined error check, plus `aliveVoters > voters` | app.go:196-200 |
| 2.29 | Widget wiring, as one config value | `Config(feed, origins, logger, maxSessions) live.Config[State]` | app.go:225-271 |
| 2.30 | Mount hook returning initial state plus startup effects | `Init:` | app.go:227-233 |
| 2.31 | Region list with per-region render and dirty predicate | `Fragments: []live.Fragment[State]{…}` | app.go:235-250 |
| 2.32 | Declared event allowlist | `Events: []string{EventToggleMotion, EventBrowserReady}` | app.go:251 |
| 2.33 | Unmount hook | `Teardown:` → `feed.Leave(session.ID())` | app.go:253-255 |
| 2.34 | Session limits | `Limits: live.Limits{MaxSessions, MaxSessionsPerIdentity}` | app.go:264-267 |

## 3. The shipped widget — the external data stream

Source: `go/services/candace-cloud/internal/homepage/feed.go`.

| # | Concept | Real name in the source | Where |
|---|---|---|---|
| 3.1 | Externally-delivered event name | `EventSnapshot = "home.warden.snapshot"` | feed.go:14 |
| 3.2 | Effect source identifier | `SourceWatch = "home.warden_watch"` | feed.go:15 |
| 3.3 | The snapshot's field vocabulary | `sequence`, `connected`, `authoritative`, `leader_known`, `has_quorum`, `term`, `voters`, `alive_voters` | feed.go:17-24 |
| 3.4 | Fan-out of one upstream stream to many sessions | `type Feed struct { subscribers map[live.ID]chan feedSnapshot }` | feed.go:35-39 |
| 3.5 | Server-side tick increment, one per real upstream emission | `f.current.Sequence++` | feed.go:52 |
| 3.6 | Latest-value-wins delivery (a slow tab skips, never queues) | the nested `select` in `Publish` | feed.go:54-67 |
| 3.7 | Subscribe / unsubscribe | `Join(id)`, `Leave(id)` | feed.go:70-84 |
| 3.8 | A declared long-running effect | `type WatchEffect struct{}` / `EffectSource()` | feed.go:86-88 |
| 3.9 | Effect execution loop that injects events | `Execute(ctx, session, effect, emit)` | feed.go:90-111 |
| 3.10 | Snapshot rendered as a flat string-keyed event | `func (s feedSnapshot) event() live.Event` | feed.go:113-127 |

## 4. The shipped widget — motion and tokens

Source: `go/services/candace-cloud/internal/homepage/home.css`.

| # | Concept | Real name in the source | Where |
|---|---|---|---|
| 4.1 | Pulse travelling from one endpoint to the other | `@keyframes heartbeat-out` | home.css:929-934 |
| 4.2 | Pulse travelling back along the same edge | `@keyframes heartbeat-back` | home.css:936-941 |
| 4.3 | Node emphasis ring | `@keyframes leader-pulse` | home.css:943-946 |
| 4.4 | Ambient decorative drift | `@keyframes orbit-drift-a`, `orbit-drift-b` | home.css:919-927 |
| 4.5 | Entrance animation | `@keyframes field-enter` | home.css:908-917 |
| 4.6 | The three-part gate every animation is behind | `html[data-gotth-status="live"] .system-card[data-live="true"][data-paused="false"]` | home.css:438, 442, 446, 450, 508 |
| 4.7 | Pulse duration | `0.82s` (edges), `0.72s` (node emphasis) | home.css:439, 509 |
| 4.8 | Per-pulse stagger | `0s`, `0.08s`, `0.18s`, `0.26s` | home.css:439-451 |
| 4.9 | Edge geometry, hand-computed from the endpoints | `.line-a { width: 36%; transform: rotate(-137deg); }` | home.css:435-436 |
| 4.10 | Node placement, as percentages of the scene box | `.node-edge { top: 17%; left: 21%; }` | home.css:512-513 |
| 4.11 | Centre placement for the distinguished node | `.node-core { top: 50%; left: 50%; }` | home.css:487-490 |
| 4.12 | Role-dependent node marker size and colour | `.map-node i` vs `.node-core i` | home.css:466-475, 492-498 |
| 4.13 | Channel colours | `.heartbeat-key { background: var(--archive) }`, `.ack-key { … var(--lichen) }` | home.css:544-551 |
| 4.14 | Indicator tone selected by a state flag | `.system-card[data-live="false"] .observing span { background: var(--signal) }` | home.css:371-374 |
| 4.15 | Global reduced-motion damper | `@media (prefers-reduced-motion: reduce)` | home.css:1123-1135 |
| 4.16 | The token vocabulary the widget draws from | `--rule`, `--archive`, `--lichen`, `--sheet`, `--ink`, `--muted-strong`, `--signal` | home.css:386-551 passim |

## 5. The runtime contract — `candace/pkg/gotth/live`

| # | Concept | Real name in the source | Where |
|---|---|---|---|
| 5.1 | Live region marker | `func Region(id string) templ.Attributes` → `data-gotth-region` | live/templ.go:86-93, 29 |
| 5.2 | Region id shape and stability rule | `^[A-Za-z0-9_:.-]{1,64}$`, "changing it is a client-visible change" | live/core.go:34-37 |
| 5.3 | Event binding | `func On(domEvent, eventName string)` → `data-gotth-on` | live/templ.go:95-121, 30 |
| 5.4 | Binding grammar hazards (`:` and `;` are structural, empty name silences) | `On` panics rather than returning | live/templ.go:104-118 |
| 5.5 | Per-binding options | `OnWith(domEvent, eventName, Bind)` — `Fields`, `Debounce`, `Throttle` | live/templ.go:270 |
| 5.6 | Composing bindings on one element | `OnAll(bindings ...templ.Attributes)` | live/templ.go:314 |
| 5.7 | Opting a subtree out of morphing | `func Preserve()` → `data-gotth-preserve`, innermost-wins | live/templ.go:576-584, 31 |
| 5.8 | The host document shell | `(*App[S]).Document(mountPath, title, htmlAttrs, head…)` | live/document.go:146 |
| 5.9 | A page in a live app that is deliberately not live | `NoRuntime` | live/document.go:34 |
| 5.10 | The client's own connection status, readable from CSS | `data-gotth-status` on `<html>` | live/templ.go:20-41 |
| 5.11 | Region declaration: id, render, dirty | `type Fragment[S any]` | live/core.go:32-50 |
| 5.12 | Render purity requirement (byte-identical HTML for equal state) | `Fragment.Render` doc | live/core.go:39-43 |
| 5.13 | Under-declared dirty is a correctness bug | `Fragment.Dirty` doc | live/core.go:45-49 |
| 5.14 | The transition function's type | `type Reducer[S any] func(state S, ev Event) (S, []Effect)` | live/core.go:30 |
| 5.15 | One inbound interaction | `type Event struct { Name, FragmentID, Fields, At, ID, Contributing }` | live/core.go:57 |
| 5.16 | Event payload | `type Fields`, `func NewFields(map[string]string)` | live/core.go:120, 139 |
| 5.17 | A deferred side effect, as a concrete value carrying its own behaviour | `type Effect struct { Source string; Run func(...) error }` | live/core.go:186-260 |
| 5.18 | Injecting an event from inside an effect | `type Emitter func(event Event) error` | live/core.go:203 |
| 5.19 | Failure delivered as an event, not a log line | `EffectFailedEvent`, `EffectFailedSourceField`, `EffectFailedRetryableField` | live/core.go:219, 231, 267 |
| 5.20 | Backpressure delivered as events | `SlowClientEvent`, `ClientRecoveredEvent` | live/core.go:293-294 |
| 5.21 | Session identity | `type Session`, `type ID [16]byte`, `type Identity` | live/core.go:376, 386, 394 |
| 5.22 | The whole application declaration | `type Config[S any]` | live/config.go:50 |
| 5.23 | Mount hook, also used to render first paint | `Config.Init` | live/config.go:51-76 |
| 5.24 | Unknown event names are default-deny | `Config.Events` doc | live/config.go:85-91 |
| 5.25 | Explicit opt-outs rather than defaults | `Anonymous`, `AllowAll`, `NoCSRFCheck` | live/config.go:723, 731, 736 |
| 5.26 | Session limits | `type Limits`, `MaxSessions` | live/config.go:144, 275, 385 |

## 6. The validated-token pattern — `candace/services/candaceos/webui/palette.go`

| # | Concept | Real name in the source | Where |
|---|---|---|---|
| 6.1 | A token value bound is declared, not guessed | `maxPaletteValueLength = 256`, with the reason | palette.go:10-13 |
| 6.2 | One sentinel error the caller can match on | `ErrInvalidPaletteValue` | palette.go:17 |
| 6.3 | One struct field per token, each carrying its CSS name | `type Palette struct { Canvas string // --canvas … }` | palette.go:28-74 |
| 6.4 | Unset means "keep the shipped value", not "empty" | `Palette` doc | palette.go:20-21 |
| 6.5 | A single inventory both validation and rendering read | `func (p Palette) entries() []paletteEntry` | palette.go:82-115 |
| 6.6 | Report **every** failure, not the first | `errors.Join(failures...)` | palette.go:119-127 |
| 6.7 | Validate rather than escape, because the value is substituted as code | `Palette` doc | palette.go:23-27 |
| 6.8 | A forbidden sequence carries its own human reason | `forbiddenPaletteSubstrings []struct{ Sequence, Reason string }` | palette.go:151-164 |
| 6.9 | A forbidden-function list, matched case-folded | `forbiddenPaletteFunctions` = `url(`, `image-set(`, `expression(` | palette.go:168, 193-201 |
| 6.10 | Structural balance check (unclosed group swallows what follows) | `balancedPaletteValue` | palette.go:208-235 |
| 6.11 | Messages name the token and what disqualified it | `"%w: %s contains %q, which %s"` | palette.go:187-190 |
| 6.12 | Rendering is separate from validating, and says so | `Stylesheet()` — "callers must Validate first" | palette.go:129-147 |

## 7. The widget lifecycle

These have no single file to cite because the SDK that owns them is what slice
P1 builds. They are named here from the shape the shipped widget already has,
so the language can be checked against them.

| # | Concept | Grounded in |
|---|---|---|
| 7.1 | **register** — a widget is made known to a host under a stable identity, before any session exists | `Config.Fragments` is a declared list with unique ids (live/config.go:81-83) |
| 7.2 | **mount** — per session: produce initial state, and any startup effects | `Config.Init` (live/config.go:51-76); `feed.Join` (feed.go:70) |
| 7.3 | **event in** — an inbound interaction is authorized, then reduced | `Config.Authorize` then `Config.Reduce` (live/config.go:136, 79) |
| 7.4 | **tick** — an external monotonic advance that is not a user interaction | `Feed.Publish` increments `Sequence` (feed.go:52); `PulseMapID()` turns it into DOM identity (app.go:73) |
| 7.5 | **state out** — a region re-renders when its dirty predicate says it may have changed | `Fragment.Render` / `Fragment.Dirty` (live/core.go:40-50) |
| 7.6 | **effect out** — a transition returns effect values the library performs | `Reducer` returns `[]Effect` (live/core.go:31); `Effect.Run` (live/core.go:252) |
| 7.7 | **unmount** — per session teardown with the final state | `Config.Teardown` (live/config.go:116); `feed.Leave` (feed.go:80) |
| 7.8 | **failure in** — a failed effect arrives back as an ordinary event | `EffectFailedEvent` (live/core.go:219); handled at app.go:173 |

---

## Raw count

135 concepts. That is the number [`ontology.md`](ontology.md) has to reduce:
an inventory entry earns a place in the ontology only by carrying a declared
type, typed relations with cardinality, at least one invariant, and a verb
list. Everything that does not is either merged into an entry that does, or cut
with the reason written down.
