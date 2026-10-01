# gotth-live v0.1 exported API surface

| | |
|---|---|
| **Status** | Current v0.1 API ledger; Phase 0 review closed |
| **Date** | 2026-08-04; last synced 2026-09-18 |
| **Author** | DEV-1 (Server Core / Go) |
| **Satisfies** | PRD FR-65, FR-66; Phase 0 exit ("draft exported API surface sketched in `docs/api-surface.md`") |
| **Governed by** | [RFC-0001 §14.2](rfc/001-architecture.md) · [review checklist §1.7](review-checklist.md) |

## 0. The rule this document enforces

> Exported symbols are permanent. Unexported is the default. Every exported
> identifier below carries a one-line summary and the **FR it exists to satisfy**.
> A symbol with no FR is a symbol to cut (FR-65).

**Stability marking.** `stable` = intended to survive to v1.0 unchanged.
`experimental` = shape expected to change; each one names the consumer that will
force the change. v0.1 as a whole makes **no** compatibility commitment (BL-30);
`stable` is a statement of intent and a review signal, not a promise.

**Counts.** This table is the FR-65 baseline `tools/apisurface` reads and CI
enforces. It holds measurements and nothing derived. **`live` must match
exactly**, in both directions — a difference either way fails.
**`live/livetest` is a v0.1 target and a ceiling**: some of its ledgered symbols
are still unimplemented, so measured may be below this row and may never exceed
it. The two columns therefore mean different things and are deliberately **not
summed here**; the tool prints every derived total, including the module's true
measured surface.

| | `live` (exact) | `live/livetest` (ceiling) |
|---|---:|---:|
| Exported identifiers (types, funcs, methods, consts, vars) | **60** | 37 |
| Exported struct fields | **55** | 33 |

*The `live` split was corrected from 41/48 to 40/49 when `tools/apisurface`
first measured it: one struct field had been counted in the identifier column
since before checkpoint 1, and every subsequent edit carried the error forward
because the number being checked was the total. That is the argument for this
table holding only what a program reads, and for the derived totals living in
the program's output. Surface changes are recorded in §10.*

**Names.** Since 2026-09-02, `tools/apisurface` also compares the **set of
exported names** in each package against the symbol rows of §1–§6, and CI fails
on a difference. This is not a second baseline and adds no field here: the rows
already name every symbol, and the tool reads what the document states rather
than storing anything derived. It exists because a count is a projection and a
projection is exactly a gate's reach — the P3 style retrofit renamed every
interface in this ledger and then named every parameter in it, and both times
this tool printed *the surface matches the ledger* while the rows were stale,
because neither edit moves a number. `live` is compared in both directions, so
a row left at a symbol's old spelling now fails; `live/livetest` is a ceiling in
names as in counts, so a ledgered symbol nobody has implemented is reported and
does not fail. A rename therefore belongs in the same commit as its rows, with
a §10 entry saying the counts did not move.

*One thing that comparison surfaced and did not change: §6 lists **39** symbols
while the `live/livetest` ceiling above says **37**. The two extra rows are
`Audit` and `Report`, which §6's own closing note says are not implemented — so
the ceiling is currently set at what is built rather than at what is ledgered,
and implementing either one would exceed it. Reconciling that is a change to an
enforced baseline and belongs to whoever rules on this document; the tool
prints the disagreement on every run rather than picking a side.*

## 0.1 Two exported packages — ruled on, and capped at two

This surface has **two** exported packages, `live` and `live/livetest`.
Precedent is `net/http/httptest` and `testing/fstest`: test scaffolding that
must not be linked into production binaries belongs in a sibling package, and
PRD FR-15 *requires* the library to ship a determinism test helper. Keeping it
in `live` would put `testing` — and with it `flag`, `regexp`, `runtime/pprof`
and `runtime/trace` — in every consumer's production import graph.

**L9-1 accepted it as ruling A1 in cycle 2, with three conditions (C-12), all
discharged at module init:** RFC §14.2's "one exported package" was amended in
the PR that created the module; the architecture test in `internal/arch` asserts
that `live` does not transitively import `testing`, so the argument for the
split is checked rather than claimed; and **two is a cap** — a third exported
package requires an L9-1 ruling, because "production code must not link it" does
not generalise to a `live/middleware` or a `live/otel` arriving on convenience
grounds.

FR-65's concern is *surface*, not package count, and the surface is unchanged by
where `livetest`'s symbols live: they are counted in §0 either way. No count is
restated in this sentence, because the one that used to be here said **eight**
and was wrong twice over (REV-DEL finding 8, ruled at
[rulings-review-wave.md](reviews/rulings-review-wave.md) §3). §0's table is what
`tools/apisurface` reads, and it is the only place either number belongs.

---

## 1. Package `live` — configuration and construction

| Symbol | Kind | Summary | Status | Req'd by |
|---|---|---|---|---|
| `Config[S]` | struct | Declares one live application: its state type, mount, reducer, fragments, effect executor, and security hooks. Zero value is invalid; `New` reports exactly which field is missing. | stable | FR-14, FR-18, FR-45–48 |
| `New[S](Config[S]) (*App[S], error)` | func | Validates a `Config` and returns a mounted application. Errors on duplicate fragment IDs, missing security hooks, and unregistered effect handling. **`Config.Init` is the one field it fills in rather than refusing** — see §1.1. | stable | FR-33 |
| `MustNew[S](Config[S]) *App[S]` | func | `New` for a caller with nowhere to put the error: returns the application, or **panics with the `*ConfigError` `New` would have returned**. For `main` and package-level initialisation, where a `Config` is a literal in the source and every failure is a startup mistake in it. `template.Must`'s shape and naming. | stable | **FR-53** |
| `App[S]` | struct | A validated live application. Safe for concurrent use. | stable | FR-33 |
| `(*App[S]).ActiveConnections() int` | method | Reads this application's registered WebSocket count, including cleanup, under the registry lock. Excludes refused handshakes and does not count users or tabs. CSF inspection consumes it without requiring a metrics exporter. | stable | FR-38 |
| `(*App[S]).Handler() http.Handler` | method | Returns the `http.Handler` that serves the live connection and the client runtime. Mountable under any router. **The live route returns at the upgrade** — the session runs on a goroutine the library owns, so wrapping middleware completes at the handshake and the session runs under `context.WithoutCancel` of the request context (`5a2ca417`, C-38). | stable | FR-33 |
| `(*App[S]).PageHandler(page func(state S) templ.Component) http.Handler` | method | Serves the first paint: on every request it loads state through `Config.Init` — with the identity `Config.Authenticate` derives from that request and the zero session `ID` — and renders the given component function from it. **It cannot be given a state value, only the function that renders one**, which is what makes QA-1's F-4 unwritable: `templ.Handler(Page(State{}))` freezes the zero state at registration and contradicts every `Init` that loads anything. `Init`'s effects are discarded on a page render. 401 when `Authenticate` refuses (the status that visitor's upgrade would get), 500 when the load or the render fails, buffered so a half-written document is never a 200. | experimental | **FR-53**, QA-1 F-4, FR-33 |
| `(*App[S]).Mux(mountPath string, page http.Handler) http.Handler` | method | The three registrations of a single-application server in one call: the upgrade at exactly `mountPath`, the runtime and dev routes on the subtree under it, and `page` on the catch-all. Makes the two silent mounting failures `docs/quickstart.md` §2 measures — a missing subtree registration, and the `http.StripPrefix` repair that turns the upgrade into an unfollowable 307 — inexpressible. **Panics** on a nil page, on a `mountPath` `Script` would reject, and on `"/"`, on the precedent of `http.ServeMux.Handle`, which panics for the same class. | experimental | **FR-53**, FR-33 |
| `(*App[S]).Close(context.Context) error` | method | Drains **every** session, closing each with `GOING_AWAY`, and waits for in-flight effects up to the context deadline. A connection admitted but not yet registered when `Close` begins is refused and closed rather than started, so `Close` cannot return `nil` over a session it did not touch (`ed9f73b6`, C-34). Not reusable afterwards. | stable | FR-22, checklist §6.8 |
| `ConfigError` | struct | Reports an invalid `Config`, naming the offending field and what to set it to. | stable | FR-58 |
| `(*ConfigError).Error() string` | method | — | stable | FR-58 |

### 1.1 `Config[S]` fields

| Field | Type | Summary | Status |
|---|---|---|---|
| `Init` | `func(ctx context.Context, session Session[I]) (S, []Effect[I], error)` | Mount hook: produces the session's initial state and any startup effects (e.g. a pubsub subscription). Runs once per session, before the first `Snapshot`, **and once per request through `(*App).PageHandler`**. **Optional since 2026-08-05**: nil means the zero value of `S`, no effects, no error — the only total, side-effect-free reading of an unwritten mount hook, and `Teardown`'s long-standing shape at the other end of the same session. It is the one field of the eight `New` fills in; the rest cannot be guessed. | stable |
| `Reduce` | `Reducer[S]` | The pure state transition. Required. | stable |
| `Fragments` | `[]Fragment[S]` | The server-owned live regions. Required, non-empty. | stable |
| `Events` | `[]string` | The event names this application accepts. Required. An unregistered name is refused with `UNKNOWN_EVENT` and counted, never dispatched and never ignored. | stable |
| `Teardown` | `func(ctx context.Context, session Session[I], state S)` | Runs after the session actor exits, with final state, for unsubscribing. Optional. | stable |
| `Origins` | `[]string` | Allowed `Origin` values. Required unless it contains `AnyOrigin`. | stable |
| `Authenticate` | `func(request *http.Request) (I, error)` | Derives the session identity from the upgrade request, as the application's own type. Required; use `Anonymous` (which produces `AnonymousIdentity`) to opt out. | stable |
| `Authorize` | `func(ctx context.Context, session Session[I], event Event) error` | Runs before the reducer for every event. Required; use `AllowAll` to opt out. | stable |
| `CSRF` | `func(request *http.Request) error` | Validates a token bound to the authenticated application session. Required; use `NoCSRFCheck` to opt out. | stable |
| `Limits` | `Limits` | Resource bounds. Zero fields take documented defaults. | stable |
| `Logger` | `*slog.Logger` | Structured log sink. Nil disables library logging **and the provenance log with it**, which makes FR-41's reverse lookup unavailable (instrumentation §4A.3). | stable *(L9-1 D2 settled)* |
| `Metrics` | `metric.MeterProvider` (from `go.opentelemetry.io/otel/metric`) | Enables the full metric set with one field (FR-38). | stable *(L9-1 D1: Option A settled)* |
| `Tracer` | `trace.TracerProvider` (from `go.opentelemetry.io/otel/trace`) | Enables the full trace set with one field (FR-38). | stable *(L9-1 D1: Option A settled)* |
| `Dev` | `bool` | Dev mode, and the switch three separate gates read: the `Error` frame a contained panic produces carries the panic value and its stack (FR-23, checklist §5.9); the session inspector is served and its tag rendered (FR-44, NFR-8); dev reload is served and its tag rendered (FR-57). Must be false in production. | stable |
| `DevBuildID` | `string` | Overrides the identity gotth-live uses to tell one build of this application from another; read only when `Dev` is set. Empty derives it from a SHA-256 of the running executable, which is what makes a restart that rebuilt nothing leave the page alone. Validated at `New` in both modes: ≤ 128 bytes, no control bytes, no surrounding whitespace. | experimental |

**Why a config struct and not functional options.** `http.Server`, `tls.Config`,
and `net.Dialer` are the stdlib shape for this, it makes the security
configuration one reviewable object, and it removes 14 `WithX` symbols plus an
`Option` type — see §6.1. FR-65 flags "accessor-heavy config structs"; this one
has plain data fields and no accessors.

**`Config.Metrics`/`Tracer` — settled by L9-1 D1 as Option A**, so both fields
stay in `live` and take OTel **API** types. The proposed concrete form is the
`otel/trace` and `otel/metric` **submodules**, never the `otel` root
(dependencies.md §1.4). D1's pre-registered fallback still applies: if enabling
tracing adds more than 8 modules to a consumer's build graph, both fields move to
a `gotth-live/otel` submodule and `Config` loses two fields. Recorded so the
trigger has a visible blast radius.

---

## 2. Package `live` — the functional core

| Symbol | Kind | Summary | Status | Req'd by |
|---|---|---|---|---|
| `Reducer[S, I]` | func type | `func(state S, ev Event) (S, []Effect[I])` — the pure state transition. Must not perform I/O, read clocks or randomness, mutate its input, or start goroutines. | stable | FR-14 |
| `Fragment[S]` | struct | Declares one server-owned live region and how to render it. | stable | FR-18, FR-21 |
| `Event` | struct | One inbound interaction, already past the refinement boundary. | stable | FR-39 |
| `Fields` | struct | The form values carried by an event. Read-only; holds no alias into wire data. | stable | FR-55 |
| `NewFields(map[string]string) Fields` | func | Builds `Fields` an application owns, ordered by key. The only way to give an emitted event a payload, and the only way a determinism test can build an event log that carries form values. | stable | FR-42, **FR-15** |
| `(Fields).Get(string) string` | method | Returns the value for a key, or `""`. | stable | FR-55 |
| `(Fields).Lookup(string) (string, bool)` | method | Returns the value and whether the key was present — the distinction matters for unchecked checkboxes. | stable | FR-55 |
| `(Fields).Len() int` | method | Number of fields. | stable | FR-55 |
| `(Fields).All(func(k, v string) bool)` | method | Iterates fields in wire order. | stable | FR-55 |
| `Effect` | struct | One unit of I/O the library performs at the actor boundary, on a goroutine of its own. Two fields and no methods. `Source string` names it for provenance and metrics, in the form `package.action`; it becomes the origin source `effect:<name>` on every patch the effect causes and is the value `EffectFailedSourceField` carries. `Run func(ctx context.Context, session Session[I], emit Emitter) error` performs it, closing over whatever the application owns. A zero `Effect` is inert and is dropped; an `Effect` with a `Source` and no `Run` fails deterministically, because an effect that never runs is a change that never happens. | stable | FR-16, FR-42 |
| `Emitter` | func type | `func(event Event) error` — injects an event into the session that spawned the effect. Passed to `Effect.Run`. | experimental | FR-42, FR-61 |
| `EffectFailedEvent` | const `string` | The name of the event a failed or panicking effect becomes. Not in `Config.Events` and must not be: the library mints it. | stable | FR-16, FR-58 |
| `EffectFailedSourceField` | const `string` | Field key: the `Source` of the effect that failed. | stable | FR-16 |
| `EffectFailedErrorField` | const `string` | Field key: the error's message, or the panic value. | stable | FR-16, FR-58 |
| `EffectFailedRetryableField` | const `string` | Field key: the transient-or-terminal classification, `"true"` only when the effect claimed it with `Retryable`. Read with `strconv.ParseBool`; an unreadable value is terminal. | stable | FR-16 |
| `SlowClientEvent` | const `string` | `"timer:slow_client"` — the name of the event the library synthesizes into a session's own mailbox when the outbound window fills. Not in `Config.Events` and never accepted from a client. | stable | **FR-51**, FR-62 |
| `ClientRecoveredEvent` | const `string` | `"timer:client_recovered"` — its counterpart, synthesized when an acknowledgement drains the window. Same registration rule. | stable | **FR-51**, FR-62 |
| `Retryable(error) error` | func | Marks an error returned from `Config.Execute` as transient. Unmarked is terminal. `Retryable(nil)` is nil; the mark survives `%w` wrapping and is invisible in the message. | stable | FR-16 |
| `IsRetryable(error) bool` | func | Reads the mark back off an error, through the same `errors.As` the actor uses to fill `EffectFailedRetryableField`. `false` for `nil`, for an unmarked error, and for a plain `%w` wrap of one. | stable | FR-16 |
| `DenyError` | struct | Returned by `Authorize` to reject one event without closing the connection. | stable | FR-47 |
| `(*DenyError).Error() string` | method | — | stable | FR-47 |
| `FatalDenyError` | struct | Returned by `Authorize` to reject an event and close the connection with `UNAUTHORIZED`. | stable | FR-47 |
| `(*FatalDenyError).Error() string` | method | — | stable | FR-47 |

`Emitter` is **experimental**: its named consumer is the chat example (FR-61),
which is the first real test of whether effects-as-data survives a long-lived
pubsub subscription. If it does not, this is the symbol that changes.

**Why the failure contract is four constants and two functions, and not a type.**
The event and its field keys are exported because the alternative is what the
counter example actually shipped: a reducer hard-coding the string, and hard-
coding the wrong one. `Retryable` sets the classification and `IsRetryable`
reads it back. Terminal is the unmarked default: an effect may have committed
externally before it failed, so retrying an unclassified failure risks
duplicating somebody else's data, where not retrying costs a change that
visibly does not happen.

`IsRetryable` was cut here at the checkpoint-2 batch, on the ground that nothing
reads the mark off an *error* — the reader is a reducer, and what a reducer
holds is the event. That was wrong in two ways and L9-1 has restated it as
wrong (checkpoint-2 round §5). The library itself reads the mark off an error:
`internal/session.IsRetryable` is what fills `EffectFailedRetryableField`, and
`live.IsRetryable` is that function rather than a second copy. And an exported
setter whose mark nothing exported can read is a one-way door — a value an
application can create and cannot inspect. The measured cost of the cut is in
§10.

### 2.1 Field detail

| Struct | Field | Type | Summary |
|---|---|---|---|
| `Fragment[S]` | `ID` | `string` | Stable identity matching `^[A-Za-z0-9_:.-]{1,64}$`; must be unique per app (FR-21). |
| | `Render` | `func(state S) templ.Component` | Pure render of this region from state (FR-18). |
| | `Dirty` | `func(prev, next S) bool` | Optional; nil means "re-render on every transition". Over-declaring is safe (identical renders are suppressed); under-declaring is a bug `livetest.AssertDirtyComplete` catches. |
| | `Children` | `func(state S) []Fragment[S]` | Optional ordered projection of child regions from state (FR-18, FR-21). Each child has a unique ID in the parent's `ID + ":"` namespace, a `Render`, and no nested `Children`. The parent's `Render` includes every child in projection order. |
| `Event` | `Name` | `string` | The registered event name. |
| | `FragmentID` | `string` | The fragment whose markup raised it. |
| | `Fields` | `Fields` | Form values. |
| | `At` | `time.Time` | Stamped at the actor boundary, never read inside the reducer's own clock (checklist §2.2). |
| | `ID` | `uint64` | Server-minted causal ID, session-scoped and monotonic (protocol.md §4.1). Read-only in practice: on an event constructed for an `Emitter` it must be zero, and a non-zero value is an error rather than a silent discard. |
| | `Contributing` | `[]uint64` | Events of this session whose state changes an emitted event carries. The one causal field an application sets rather than reads; ignored anywhere but an `Emitter` event. |
| `DenyError` | `Reason` | `string` | Operator-facing reason; a generic message reaches the client in production. |
| `FatalDenyError` | `Reason` | `string` | As above. |
| `ConfigError` | `Field` | `string` | The `Config` field at fault. |
| | `Detail` | `string` | What to set it to. |

---

## 3. Package `live` — identity and session

| Symbol | Kind | Summary | Status | Req'd by |
|---|---|---|---|---|
| `Session[I]` | struct | Identifies one live connection, typed by the application's own identity type. Passed to `Init`, `Authorize`, `Teardown` and every `Effect.Run`. | stable | FR-46 |
| `(Session[I]).ID() ID` | method | The session's 16-byte identifier. | stable | FR-41 |
| `(Session[I]).Identity() I` | method | The identity bound at handshake, immutable for the connection's life, **as the application's own type**. No assertion, and none possible. | stable | FR-46 |
| `ID` | `[16]byte` | The session identifier carried in every frame. | stable | FR-40, FR-41 |
| `(ID).String() string` | method | Lower-case hex. | stable | FR-58 |
| `NewSessionFor[I](livebridge.Token, ID, I) Session[I]` | func | Builds a `Session[I]`, and refuses a Token nobody granted. **Not reachable by a consumer**: the only source of a Token is `internal/livebridge`, whose importers `internal/arch` asserts are exactly `live` and `live/livetest`, so a handler cannot obtain the argument. It exists because a package-level variable cannot be generic and the old bridge was one; see §10's 2026-09-03 identity entry. | stable | **C-25** §6.3, FR-46 |
| `IIdentity` | interface | The CONSTRAINT every generic declaration here carries: one method, `Subject() string`, returning a stable non-secret identifier used for logging and per-identity session limits. It is never a result type and never a field a getter hands back. | stable | FR-46, FR-51 |

**Deliberately absent: `Session.Request()`.** Retaining the upgrade request for a
connection's lifetime is a footgun (and a memory line item against RFC §6).
Values an application needs at mount reach `Init` through the context derived
from the upgrade request — zero API surface, and the idiomatic Go answer.

---

## 4. Package `live` — security escape hatches

Each is a **named, greppable** value, so an audit is `grep -rn 'live\.AnyOrigin\|live\.Anonymous\|live\.AllowAll\|live\.NoCSRFCheck'`. `New` fails
if the corresponding `Config` field is unset and no escape hatch is used
(FR-45's "deny by default").

| Symbol | Kind | Summary | Status | Req'd by |
|---|---|---|---|---|
| `AnyOrigin` | const `string` | Sentinel for `Config.Origins` disabling origin validation. Never use outside local development. | stable | FR-45 |
| `Anonymous` | func | `func(request *http.Request) (AnonymousIdentity, error)` — an `Authenticate` implementation binding every session to an anonymous identity. | stable | FR-46 |
| `AnonymousIdentity` | struct | The concrete identity `Anonymous` produces, and the type an application with no accounts instantiates its `Config` on. Exported because a Config must NAME an identity type, and naming the interface there is the erasure the 2026-09-03 ruling removed. | stable | FR-46 |
| `(AnonymousIdentity).Subject() string` | method | The one subject every anonymous session shares. | stable | FR-46 |
| `AllowAll[I]` | func | `func(ctx context.Context, session Session[I], event Event) error` — an `Authorize` implementation permitting every event. **Instantiate it**: `live.AllowAll[Member]`, because Go infers a generic function's type arguments on assignment to a variable but not to a composite literal's field. | stable | FR-47 |
| `NoCSRFCheck` | func | `func(request *http.Request) error` — a `CSRF` implementation performing no check. | stable | FR-48 |

---

## 5. Package `live` — limits and templ helpers

### 5.1 Limits

| Symbol | Kind | Summary | Status | Req'd by |
|---|---|---|---|---|
| `Limits` | struct | Per-connection and per-process resource bounds. Any zero field takes its documented default. | stable | FR-12, FR-13, FR-22, FR-51 |
| `DefaultLimits() Limits` | func | The defaults, for inspection and printing. | stable | FR-51 |

| Field | Default | Summary |
|---|---|---|
| `MaxInboundFrameBytes` | 65536 | Enforced before payload allocation (FR-13). **Range 1024–1048576**; `New` refuses anything outside it. The mount `Snapshot` announces it to the client in `max_inbound_frame_bytes`, which the schema refines to that interval, so a value outside it is a frame this library builds and then refuses to send (D-23). |
| `MaxEventsPerSecond` | 50 | Token-bucket rate (FR-51). |
| `EventBurst` | 100 | Token-bucket burst. |
| `MailboxDepth` | 64 | Full mailbox rejects with a typed error; it does not block. **Also a memory parameter** — the backing array is allocated eagerly (RFC §3.3). |
| `AckChannelDepth` | 32 | Bounded ack channel; a full channel drops, which is lossless because acks are cumulative (RFC §3.3). |
| `MinResyncInterval` | 1 s | Minimum spacing between `ResyncRequest`s, in a bucket independent of `MaxEventsPerSecond` (RFC §7.6). |
| `ResyncBurst` | 3 | Burst for that bucket. |
| `CoalesceFlushAt` | 512 | Contributing-event union size at which a coalesced patch is flushed rather than coalesced further, so provenance is never truncated (RFC §7.4). **Range 1–959**; `New` refuses anything above. The trigger counts the union the frame will carry, and one more emission can add the deferred transition's own identifier plus the 64 an application may name in `Event.Contributing`, against H-4's ceiling of 1024 (D-14, D-18). |
| `AckWindow` | 16 | Unacknowledged patches in flight (RFC §7.1). **Range 1–256**; `New` refuses anything above, and the floor is reachable only as a deliberate 1 because zero takes the default. Refined on the wire as `ack_window` (D-23). |
| `WriteDeadline` | 5 s | Per-write deadline; exceeding it with a full window evicts. |
| `SlowClientGrace` | 30 s | Continuous window-full duration before eviction. |
| `HeartbeatInterval` | 20 s | Must be below the shortest idle timeout in the path (ADR-001 §4.4). **Range 1 s–5 min**; `New` refuses anything outside it. Refined on the wire as `heartbeat_interval_ms`, in whole milliseconds, so a sub-millisecond interval is out of range however it is spelled (D-23). |
| `HeartbeatTimeout` | 50 s | Peer-dead detection (FR-12). |
| `IdleTimeout` | 30 min | Session eviction on inactivity (FR-22). |
| `EffectDrainTimeout` | 5 s | Shutdown wait for in-flight effects (RFC §3.6). |
| `MaxSessionsPerIdentity` | 20 | FR-51. |
| `MaxSessions` | 0 (unlimited) | FR-51; the docs tell operators to set it. |
| `PanicBudget` | 3 | Panics at one site before the session closes `INTERNAL_ERROR` (RFC §9). |

### 5.2 templ helpers

All eleven are **experimental**. Named consumer: PRD FR-53's *timed*
docs-alone usability gate in Phase 4, which is the first honest test of whether
these read well, plus the three examples (FR-60/61/62). This is where churn is
expected, and marking it now is cheaper than pretending otherwise.

| Symbol | Kind | Summary | Status | Req'd by |
|---|---|---|---|---|
| `Region(id string) templ.Attributes` | func | Marks an element as the root of the named live fragment. Morph never touches anything outside a region. | experimental | FR-21, FR-31 |
| `On(domEvent, eventName string) templ.Attributes` | func | Binds a DOM event to a server event. On a `<form>`, submission sends the form's fields; on a named control, the control's name and value are sent. | experimental | FR-54 |
| `OnWith(domEvent, eventName string, b Bind) templ.Attributes` | func | `On` with any option `Bind` carries — static extra fields, debounce, throttle, a key filter, a no-modifier restriction, or a `preventDefault`. | experimental | FR-54, FR-55, FR-62 |
| `Bind` | struct | Options for `OnWith`: `Fields map[string]string`, `Debounce time.Duration`, `Throttle time.Duration`, `Keys []string`, **`NoModifiers bool`, `PreventDefault bool`**. Every one of them is scoped to the single binding it is given to and travels inside that binding's `data-gotth-on` spec. **`NoModifiers` restricts a key binding to presses with no modifier held; `PreventDefault` calls `preventDefault()` when that binding matches.** Both default to today's behaviour and both render as trailing components, so no binding in the tree changes a byte. Element-scoped until 2026-08-05, which is FR-54 failure 2. | experimental | FR-55, FR-62, **FR-54**, **F-CHT-3** |
| `OnAll(bindings ...templ.Attributes) templ.Attributes` | func | Combines several bindings on one element, in order. The client has always matched several and nothing could emit them: two spreads of `On` render the same attribute twice and an HTML parser keeps the first. **A binding rendered here is byte-identical to that binding rendered alone**; there is nothing left to merge. | experimental | **FR-54** |
| `Preserve() templ.Attributes` | func | Marks an element and its subtree as never morphed — the sanctioned way to host HTMX- or third-party-JS-owned DOM inside a live region. | experimental | FR-27, FR-32 |
| `Script(mountPath string) templ.Component` | func | Renders the `<script>` tag for the embedded client runtime, addressing the prefix the handler is mounted at. `mountPath` is **path-only and same-origin**: one trailing `/` is trimmed, and anything a browser does not read as a path — empty, relative, or containing `//` anywhere, `\`, `?`, `#`, or a byte below `0x20` or `0x7F` — makes `Render` return an error and emit no tag. Attribute values are HTML-escaped. No CDN, no build step. **It also returns an error, and emits no tag, when rendered inside `Document`'s head content** — that component renders this tag itself, below the inspector's, and a second one from the head would land above the inspector and blind it (PS-1). Nowhere else is affected: a hand-written shell renders under a context `Document` never touched. | experimental | NFR-5, NFR-6, FR-33, **FR-44** |
| `(*App[S]).InspectorScript(mountPath string) templ.Component` | method | Renders the `<script>` tag for the dev session inspector, **and renders nothing at all unless `Config.Dev` is set**. Validates `mountPath` through the same function `Script` uses, in both modes. Belongs above `Script`'s tag: both are deferred, and the inspector must wrap the WebSocket constructor before the runtime opens a socket. *(Since `Document`, that ordering is **enforced inside `Document`** — it emits both tags itself and refuses a runtime tag from its head content — and remains **documented only** for a hand-written shell, which is what the four still in this tree use. See the `Document` row below.)* | experimental | **FR-44, NFR-8**, FR-33 |
| `(*App[S]).DevReloadScript(mountPath string) templ.Component` | method | Renders the `<script>` tag for the dev-reload client, **and renders nothing at all unless `Config.Dev` is set**. Stamps the running build's identity into the tag, which is the baseline the client compares against; validates `mountPath` through the same function `Script` uses, in both modes. Position on the page does not matter — it wraps nothing. | experimental | **FR-57**, FR-33 |
| `(*App[S]).Document(mountPath, title string, htmlAttrs templ.Attributes, head ...templ.Component) templ.Component` | method | The library-owned page shell: doctype, `<html>` with the application's attributes and nothing added to them, a `<head>` carrying the charset, the title and the three script tags **in the one order that works**, and a `<body>` holding the templ children. Makes the `InspectorScript`-above-`Script` invariant on the row above **inexpressible** rather than documented, by two mechanisms: head content renders above the three tags, and a runtime tag rendered *from* that head content is **refused** with an error rather than emitted above the inspector (PS-1). It is a method for the reason a package-level shell could not be one: that shell could emit `Script` and would then leave the application placing the inspector relative to a tag it can no longer see. **What is not refused, and is not the same failure:** a `Script` among the children, which lands below the inspector and is a duplicate runtime tag rather than a misordering. `title` is required and has no default; `htmlAttrs` may be nil and **no `lang` is ever supplied**; `head` is variadic so a page that carries none pays nothing. Both `mountPath` and `title` are validated **before any byte is written**, so a refusal is `PageHandler`'s 500 and never a truncated 200. | experimental | **FR-53**, FR-44, FR-57, NFR-8 |
| `NoRuntime` | const `string` | The `mountPath` sentinel for a document that is deliberately **not** live — `examples/chat`'s `LoginPage` is one. `Document` then emits no runtime tag, no inspector and no dev-reload tag. It exists because "omit the runtime" must not be spelled as an absence: a forgotten mount would otherwise render a page that loads perfectly and does nothing, which is the silent failure `Script` refuses a default in order to prevent. Named and greppable, on the precedent of `AnyOrigin` — and, like it, not a value any router would accept. | experimental | **FR-53**, **FR-61** |

**Why `InspectorScript` is a method and `Script` is a function**, at the
standard-library bar. `Script` needs nothing but its argument, so it takes one
argument and is a package function — `http.NewRequest`, not a method on a
client. `InspectorScript` needs one more input, and that input is not the
caller's to pass: NFR-8's "MUST NOT load in production builds" has to be keyed
to something the *application* declared once, not to a boolean at every call
site, because a per-call argument is a per-call opportunity to render the dev
tag in production. `Config.Dev` is that declaration and already carries the
"must be false in production" contract (§1.1), so the tag renderer that consults
it is a method on the validated application, exactly as `(*App).Handler` is the
router that consults the same `Config`. `net/http/httptest` draws the line the
same way: `httptest.NewRequest` is a function, `(*Server).Client` is a method,
and the difference is whether the result depends on configured state.

**Why `Document` is a method as well, and what that makes unwritable.** It is
the same test — *whether the result depends on configured state* — and it
answers the same way twice over: what `Document` emits depends on `Config.Dev`,
and the tag whose position is load-bearing is a method it could not otherwise
call. A package-level `live.Document` could emit `Script` and nothing else, and
would then leave the application to place `InspectorScript` **relative to a tag
it can no longer see** — an ordering that can only be got wrong, against a
marker the component has taken away. As a method it emits all three itself, in
order, and the application places none of them: **no argument to `Document`, in
any order, can put the inspector below a runtime tag.** That takes two
mechanisms, and the second one exists because the first has a hole in it that
L9-1 found by probe (`reviews/page-shell.md` §3.2, condition **PS-1**):

1. Head content renders **above** the block, so an application that passes its
   own `InspectorScript` as head content still lands above `Script`. That half
   falls out of the ordering and needs no mechanism.
2. Head content that renders a *runtime* tag would land **above** the inspector,
   which is the ordering failure itself and not a duplicate: both tags are
   deferred, deferred scripts run in document order, and the runtime would open
   its socket before the inspector wrapped `WebSocket`. So `Document` marks the
   context while it renders head content, `Script` reads the mark and returns an
   error, and the page is `PageHandler`'s 500 with a named reason. A whole
   `Document` nested in the head is refused by the same mark, because its own
   `Script` call renders under it. No exported identifier; one unexported
   context key and one error.

**What remains expressible, stated as what it is.** `Script` among `Document`'s
*children* renders: it lands **below** the inspector, so the ordering holds, and
what is left is a duplicate runtime tag — two sockets on one page, a real defect
with a different shape from the one above. And a document given `NoRuntime`
emits none of the three and sets no mark, so hand-placing `Script` on a page
that has declared itself not live still works and has no inspector to be ordered
against. Both are pinned by specs in `live/document_test.go`.

The route from `PageHandler(page func(state S) templ.Component)` back to the `App` is
the caller's and costs nothing: `docs/quickstart.md` holds the application in a
package-level `var` (the same counted lines it spent on `app :=` inside `main`),
and the three examples pass it into the templ component as a parameter, which
they were already doing for `DevReloadScript`.

**Why not a second package.** A `live/inspector` would put the artifact outside
production import graphs by construction, which is a stronger form of "not in
production builds" than a runtime flag. It is not taken, for two reasons that
both point the same way: §0.1 caps this module at **two** exported packages and
makes a third an L9-1 ruling; and the gate it would buy is the weaker one in
practice, because an import left in `main.go` is invisible while `Config.Dev`
is a field a deploy review already looks at. What the flag does not do is keep
the bytes out of the binary — they are embedded like any other asset — and that
limit is stated in the godoc on `clientInspector` rather than implied away.

**Zero net identifiers elsewhere.** The route that serves the inspector is
inside `(*App).Handler`'s existing mux, the artifact is a package-level
unexported `[]byte`, and the asset-serving helper both files share is
unexported. One method is the whole delta: 49 → **50**.

**`DevReloadScript` follows that ruling rather than re-arguing it.** The same
question — a function or a method — has the same answer for the same reason:
the tag must be keyed to `Config.Dev`, and a per-call boolean is a per-call
opportunity to render a dev tag in production. It costs one method and one
`Config` field, and nothing else: the two routes it needs (the client, and the
build identity) are arms of `(*App).Handler`'s existing mux, the artifact is
another unexported `[]byte` embedded by exact filename, the build identity is
derived by an unexported package function, and the watcher that rebuilds the
process is a `package main` under `internal/cmd/` and therefore not surface at
all. 50 → **51** identifiers, 50 → **51** fields.

---

## 6. Package `live/livetest`

| Symbol | Kind | Summary | Status | Req'd by |
|---|---|---|---|---|
| `ReplayN[S](testing.TB, live.Reducer[S], S, []live.Event, int)` | func | Replays an event log against a reducer N times and fails unless state and effects are identical every time. | stable | **FR-15 (mandatory)** |
| `AssertDirtyComplete[S](testing.TB, live.Config[S], S, []live.Event)` | func | Replays a log and fails if any fragment's `Dirty` returned false while its render output changed. | stable | RFC §5.3 |
| `Client` | struct | Drives a real session over the real protocol against an `http.Handler`: a real dial, a real upgrade, real frames in both directions. It **never acknowledges on its own** — see the note below. | experimental | FR-63 |
| `NewClient(testing.TB, http.Handler, ClientOptions) *Client` | func | Serves the handler from an `httptest.Server`, dials it, and returns once the mount snapshot has arrived. Registers the whole teardown with `tb.Cleanup`. | experimental | FR-63 |
| `ClientOptions` | struct | The dial: `Path string`, `Origin string`, `Header http.Header`, `Timeout time.Duration`. `Path` and `Origin` are required and are the application's, not the library's. | experimental | FR-63, FR-45 |
| `(*Client).Send(name, fragmentID string, fields map[string]string) uint64` | method | Sends one event frame and returns the client reference it used. | experimental | FR-63, FR-54 |
| `(*Client).Ack(serverSeq uint64)` | method | Acknowledges a patch — the whole of the window protocol's client half. | experimental | FR-34, RFC §8 |
| `(*Client).Resync(lastApplied uint64, reason int32)` | method | Requests a snapshot, claiming to hold everything through `lastApplied`. | experimental | FR-36 |
| `(*Client).WriteRaw(payload []byte) error` | method | Sends bytes the client did not build, and returns the error rather than failing — for specs about what the server does with a frame no correct client would send. | experimental | FR-49, protocol.md §9 |
| `(*Client).WaitFor(fragmentID string, pred func(html string) bool) *Frame` | method | Blocks until a patch makes the fragment's markup satisfy the predicate. | experimental | FR-63 |
| `(*Client).Await(what string, timeout time.Duration, pred func(frame *Frame) bool) *Frame` | method | Takes frames until one satisfies `pred`, failing with what it saw instead. `what` is the failure message's subject and is required. | experimental | FR-63, FR-62 |
| `(*Client).Next(timeout time.Duration) *Frame` | method | The next non-heartbeat frame, failing the spec if none arrives. | experimental | FR-63 |
| `(*Client).NextErr(timeout time.Duration) (*Frame, error)` | method | `Next`, returning the error — for the specs where "nothing arrived" is the assertion. The error is the string `Next` would have failed with, prefix and all: `livetest: <name> (session <hex>): …`, so a value a spec stores or logs says which session it is about (FR-58; QA-1's F-1, error-audit.md §3.4). | experimental | FR-63, FR-58 |
| `(*Client).Settle(idle time.Duration) []*Frame` | method | Drains what is in flight and returns once nothing has arrived for the idle period. | experimental | FR-62, FR-55 |
| `(*Client).Received() []*Frame` | method | Every frame decoded so far, heartbeats included, in arrival order. Consumes nothing. | experimental | FR-62 |
| `(*Client).Closed(timeout time.Duration) bool` | method | Whether the server hung up — how a spec asserts that a fatal error *closed* the connection rather than merely arriving on it. | experimental | FR-49 |
| `(*Client).SessionID() []byte` | method | The identifier the handshake bound, cloned per call. | experimental | FR-63 |
| `(*Client).Snapshot() *Frame` | method | The mount snapshot this session opened with. | experimental | FR-63, H-10 |
| `(*Client).Seq() uint64` | method | The highest server sequence seen, which is what an event frame reports as `seen_server_seq`. | experimental | RFC §8 |
| `(*Client).Close() error` | method | Closes the session and releases everything the `Client` owns — connection, read goroutine, server. **Idempotent**: the same release is registered with `tb.Cleanup`, so an explicit `Close` is always followed by a second one. Releasing the server here rather than only at cleanup is what makes a connect/disconnect loop a real one. | experimental | FR-63, FR-46 |
| `Frame` | struct | One decoded frame as a plain value: `Kind FrameKind`, `SessionID []byte`, `Bytes int`, `Patch *Patch`, `Error *Error`, `AckSeq uint64`. | experimental | FR-63, FR-62 |
| `(*Frame).String() string` | method | The rendering a failure message wants — the reason `Await` can say what it saw instead. | experimental | FR-63 |
| `FrameKind` | type | Names the payload a frame carried. A string, because a failure message is where it is read. | experimental | FR-63 |
| `FramePatch`, `FrameSnapshot`, `FrameError`, `FrameHeartbeat`, `FrameAck`, `FrameOther` | consts | The kinds. `FrameOther` is a payload this view does not model — a spec asserting on kinds should fail rather than be silently satisfied. | experimental | FR-63 |
| `Patch` | struct | A decoded Patch or Snapshot: `ServerSeq`, `PatchID`, `TransitionID`, `StateVersion`, `Origin`, `Updates`, `SupersededFrom`, `SupersededThrough`, and the three session parameters a snapshot carries. One type reads both, because their first five fields are identical and only the update field number differs. | experimental | FR-63, FR-36 |
| `(*Patch).Fragment(id string) (string, bool)` | method | The markup this patch carries for one region, and whether it carried any. The two-value form is the point: an empty region and no region are different answers. | experimental | FR-62 |
| `(*Patch).FragmentIDs() []string` | method | The regions this patch carries, in wire order. | experimental | FR-62 |
| `(*Patch).HTMLBytes() (int, map[string]int)` | method | Total rendered markup and the per-fragment split. "A snapshot costs N bytes" reads better beside which region spent them. | experimental | FR-36 |
| `Origin` | struct | What caused a patch: `Kind int32`, `EventID`, `ClientRef`, `Source string`, `Contributing []uint64`. | experimental | FR-58, FR-62 |
| `Update` | struct | One fragment update: `FragmentID string`, `HTML string`. | experimental | FR-63 |
| `Error` | struct | A decoded Error frame: `Code int32`, `Message`, `EventID`, `ClientRef`, `Fatal bool`. | experimental | FR-49 |
| `NewSession[I](testing.TB, live.ID, I) live.Session[I]` | func | Builds the `live.Session[I]` a spec needs to call an application's own `Init`, `Authorize`, `Teardown` or an `Effect.Run` directly. Both values are the caller's. The nil-identity guard is gone with the interface: an identity is the application's own type now. | stable | **FR-15**, FR-45–48 |
| `Audit(testing.TB, http.Handler, func(*Client)) Report` | func | Runs a scripted workload and cross-checks every self-reported metric against an independent, out-of-process measurement. | experimental | checklist §4.5, instrumentation.md §5.2 |
| `Report` | struct | The audit result: per-signal reported value, externally observed value, and whether they agree. | experimental | checklist §4.5 |

`Client` is **implemented**; `Audit` and `Report` are not. Their named consumer
was re-based by [rulings-review-wave.md](reviews/rulings-review-wave.md) §5.1: it
used to be *"what Phase 5's bench harness actually needs"*, and Phase 5's bench
harness landed as `bench/harness/*.mjs` — Node and CDP — which will never call a
Go client. The consumers are now the FR-63 end-to-end example tests and the
instrumentation audit, which had hand-rolled the shape twice already (REV-DUP
D-3, REV-DEL finding 1), and `Client`'s rows above are what those two consumers
turned out to have written.

**`Client`'s surface is 28 rows where the ledger budgeted four, and that is a
correction rather than a discovery.** REV-DUP D-3 specified this work as *"no
new exported symbol and therefore no L9-1 ruling"*, on the strength of the four
rows this section carried. Those four could not have been implemented as
written, for two reasons that only became visible once somebody tried:

- **`Client` had no constructor.** `Send`, `WaitFor` and `Close` are methods; the
  only ledgered function returning a `*Client` was `Audit`'s callback parameter.
  An FR-63 end-to-end test could not obtain one. `examples/chat/FRICTION.md`'s
  F-1 had already written the missing row — `NewClient(tb, h, o)` — as a consumer
  report; it is now `NewClient(testing.TB, http.Handler, ClientOptions) *Client`.
- **The four rows cannot express what the named consumers assert.** FR-62's
  properties are claims about *which* fragments a patch carried, *which* events a
  coalesced patch names, and *how many* patches an unacknowledging client is
  sent; the resync measurement's subject is a frame's **length**. None of that is
  reachable through `WaitFor(fragmentID, func(html string) bool)`. A `Client` at
  the ledgered surface would have left all three suites' decoders in place and
  saved nothing, which is the outcome D-3 exists to prevent.

Every row above names a requirement and a consumer that asserts on it today.
**Nothing was exported speculatively**: `Audit` and `Report` are still
unimplemented precisely because no consumer has written their shape yet.

**Amended: `Client.Frames()` is no longer deliberately absent, and the property
it protected is kept a better way.** This section used to refuse a frame
accessor because *"exposing captured frames would drag `internal/protocol`'s
generated types into the public surface, which is the one thing that would make
the protocol an API-compatibility burden"*. The objection is exactly right about
`*gotthlivepb.Frame` and does not reach `livetest.Frame`, which is a plain
struct this package owns. Decoding happens here, with the library's own
generated types, and the result is projected onto `Frame`/`Patch`/`Origin`/
`Update`/`Error` — so a regenerated schema is not a consumer-visible event, and
a spec can still see the wire. `Origin.Kind` and `Error.Code` are `int32` rather
than the schema's generated enums for the same reason; the values are in
`proto/gotthlive/v1/frame.proto`, the artifact an operator holding a capture
already reads.

**The one behaviour worth knowing before using it: a `Client` never
acknowledges a patch on its own.** "This client stopped acknowledging" is the
condition every backpressure spec is built on, and an auto-ack in the driver
would make the whole ladder unreachable — so `Ack` is explicit, including inside
`WaitFor`. Both hand-rolled harnesses had already reached the same conclusion
and written the same comment.

**Why `NewSession` is in `livetest` and adds nothing to `live`.** A `Session`'s
fields are unexported so that nothing downstream of the handshake can mint an
identity, and exporting a constructor from `live` would put one in every
consumer's production import graph — the trade the handshake exists to refuse.
It is built instead over a var in `gotth-live/internal/livebridge` that `live`
assigns at init and `livetest` reads: `internal/` is unreachable outside this
module, `testing.TB` is the first parameter, and importing `livetest` already
links `testing` into anything that does it. `internal/arch` asserts the bridge's
importers are exactly `live` and `live/livetest`, because the safety argument is
a claim about the import graph.

**Deliberately absent: a `testing.TB` adapter.** `livetest.NewTB` was here from
checkpoint 3 until [rulings-review-wave.md](reviews/rulings-review-wave.md) §1
withdrew it. Its premise — *"a Ginkgo suite has no way to produce a
`testing.TB`"* — is true of `GinkgoT()` and **false of `GinkgoTB()`**, which
Ginkgo ships for exactly this purpose and which this module has required since
it pinned `ginkgo/v2 v2.32.0`. A Ginkgo suite passes `ginkgo.GinkgoTB()`; a
plain `go test` suite passes its `*testing.T`; `livetest` adapts nothing and
imports no framework.

The dependency argument that shaped `NewTB` is unaffected and still stands, so
it stays recorded rather than deleted: an import of `github.com/onsi/ginkgo/v2`
in a non-test file of `livetest` would make Ginkgo a **build** dependency for
every consumer including one whose suites are plain `go test` — measured at
**+17 modules and +3,484,016 B** (dependencies.md §4) — and a
`live/livetest/ginkgotb` leaf is unavailable because §0.1 caps this surface at
**two** exported packages and `internal/arch` asserts the cap. What changed is
only that no third option is needed: `GinkgoTB()` is called from the consumer's
own test file, so `livetest` imports nothing either way. **Zero dependency
difference between the two answers**, and the one that costs no exported
identifier wins.

**Still deliberately absent: any accessor returning `*gotthlivepb.Frame`.** The
generated types are the API-compatibility burden this surface refuses; the
projection above is what replaced the refusal, not a relaxation of it. `Report`,
when it is built, carries counts rather than frames for the same reason.

---

## 7. What was cut, and why

Reported against the natural/RFC-implied shape, per the brief.

| Cut | Instead | Why |
|---|---|---|
| **`Option` type + ~13 `WithX(...)` functions** (`WithOrigins`, `WithAuthenticate`, `WithAuthorize`, `WithCSRF`, `WithMetrics`, `WithTracing`, `WithLogger`, `WithLimits`, `WithDevMode`, `WithHeartbeat`, `WithIdleTimeout`, `AllowAnyOrigin`, `WithAnonymous`) | `Config` struct + 4 named escape-hatch values | ~14 symbols removed. Struct config is the stdlib shape and makes the security config one object a reviewer can read at a glance. |
| **`Transport` interface** | package-boundary isolation, verified by an architecture test | RFC §3.5 / checklist §1.4, §1.6 — a one-implementation interface is speculative abstraction. **PRD v0.2 amended FR-2 to require exactly this** (accepted 2026-08-04), so RFC O1 is closed and no exported transport symbol is needed. |
| **`Executor` interface + registration method** | `Config.Execute` field | One field instead of a type plus a registry method; the app type-switches on its own effect types. |
| **`App.Broadcast(...)`** | pubsub effects, per RFC §8.1 | A cross-session write API would violate checklist §2.9 ("no session's state reachable from another session's goroutine") by construction. Server-initiated patches go through each session's own subscription. |
| **`Session.Request()`** | context values through `Init` | Retaining the request per connection is a footgun and a memory line item. |
| **`Fields.Int` / `Fields.Bool` / `Fields.Has`** | `strconv` + `Fields.Lookup` | 3 symbols for what the stdlib already does. Re-add only if the examples prove them needed. |
| **`Submit()` and `Change()` templ helpers** | `On("submit", …)` / `On("input", …)`, with the client collecting form fields and control values automatically | 2 symbols; the behaviour moves into the client, where it is one code path instead of three attribute vocabularies. |
| **6 error sentinels** (`ErrNoOrigins`, `ErrNoAuthenticate`, …) | one `ConfigError` with `Field` and `Detail` | 5 symbols removed; the error text is more actionable (FR-58) than an `errors.Is` target. |
| **`Client.Frames()`** | `Report` counts | Would make generated protocol types public API. |
| ~~**`IsRetryable(error) bool`**~~ | — | **Re-added, C-32.** The cut pre-registered its own re-add trigger — *"re-add only if something needs to inspect an error it did not produce"* — and the trigger fired in `examples/chat`. Kept in this table struck through rather than deleted, because a cut that was reversed is more useful to the next reviewer than a cut that was quietly forgotten. §2 and §10. |

### 7.1 The FR-56 hook count — reconciled in the requirement, not footnoted here

**FR-56 originally asked for lifecycle hooks at *"session mount, event, patch,
and teardown"*, and this surface ships three.** Mount is `Config.Init`, event is
`Config.Authorize` + `Config.Reduce`, teardown is `Config.Teardown`. There is no
patch hook, because no consumer needs one: FR-56's own sufficiency test is
"subscribe to a pubsub topic on mount and unsubscribe on teardown without
leaking", which mount and teardown satisfy, and patch observability is
`docs/instrumentation.md`'s job — the `patches_sent` counter, the
`gotthlive.encode`/`gotthlive.send` spans, and the provenance log's
per-transition record, none of which require application code. That delegation
is only honest because the provenance log is now specified (instrumentation
§4A); in cycle 1 it would have been a promise.

**L9-1 accepted the reading as ruling A2, having looked for the consumer first
and found none.** Condition **C-13** attached: a requirement and a shipped
surface must not disagree silently. **PM-1 amended FR-56 in PRD v0.3** to
mount/event/teardown with patch observability delegated to instrumentation, and
reworded the Phase 2 exit criterion to match — following the FR-2 precedent of
amending in the open rather than footnoting. So this is no longer a gap the
ledger is flagging; it is the requirement and the surface agreeing.

Adding a `Config.OnPatch` hook would be an export with no named call site, which
FR-65 makes a review rejection. If an application appears that must audit
patches from its own code rather than from telemetry, the hook lands in Phase 2
**with that consumer named in the PR** — the revisit condition PRD v0.3 records.

---

## 8. Godoc and CI obligations

- **FR-66:** every symbol in this document ships with a doc comment stating what
  it does and, where non-obvious, what it guarantees and from which goroutine it
  is safe to call (checklist §9.2). CI fails on any exported symbol without one.
- **FR-65:** CI reports the exported-identifier count delta per PR against §0's
  counts table. The numbers are not restated here: `tools/apisurface` reads that
  table and nothing else, and a second copy of a number a program depends on is
  the failure mode §0 exists to have ended.
- **FR-68:** every exported symbol reachable from the quickstart appears in a
  godoc `Example*` function that compiles and runs under `go test`.
- This document is updated **in the same PR** as any surface change (checklist
  §9.1). A PR that adds an exported identifier without a row here is incomplete.

## 9. Open questions

| # | Question | Owner | Needed by |
|---|---|---|---|
| A1 | *Closed — L9-1 accepted two exported packages in cycle 2, capped at two.* Residue: none. The three conditions (C-12) are discharged — RFC §14.2 amended at module init, and `internal/arch` asserts `live` does not link `testing` (§0.1) | — | done |
| A2 | *Closed — L9-1 accepted the no-patch-hook reading, having looked for the consumer and found none.* PM-1 amended FR-56 in PRD v0.3 and this ledger's §7.1 records the reconciliation (C-13) | — | done |
| A3 | *Closed — L9-1 D1 settled Option A.* The only residue is D1's 8-module fallback trigger, measured in the PR that adds the dependency | DEV-1 | Phase 1 |
| A4 | Is `Config[S]`'s generic parameter acceptable at the stdlib bar, or should state be `any` with a type assertion? This surface chooses generics **specifically to keep `any` out of exported signatures**, which FR-65 names as a rejection trigger | L9-1 | Phase 1 |
| A5 | Whether `Fields` should expose an iterator (`iter.Seq2`) instead of `All(func(k,v string) bool)`. The current floor is **Go 1.26** and `gotth-live/go.mod` declares `go 1.26.0`, so `iter` (landed in 1.23) is available with room to spare. Purely a taste call now | DEV-1 | Phase 1 |


---

## 10. Changelog

### Keyed child regions — added 2026-09-17; ledger corrected 2026-09-18

`Fragment.Children` lets a consumer declare an ordered collection of stable
child regions as a pure projection of its state, satisfying FR-18 and FR-21.
The [Widget SDK's keyed collection](../../widget/keyed.go) uses this field so a
changed child can patch its own region. Membership/order changes, snapshots and
parent structural dirtiness still render the whole parent. Child IDs become
known only after a successful send; reducers must also reject members removed
by a queued state transition.

This records an existing exported field that the ledger omitted: `live` remains
at **60 identifiers**, and its field count is corrected from **54 to 55**.
`live/livetest` remains **37 identifiers / 33 fields**; its documented,
unimplemented `Audit` and `Report` rows remain unchanged. No runtime API is added
by this ledger correction.

### Connection inspection — 2026-09-17

`App.ActiveConnections` exposes the existing connection registry to in-process
dashboards and collectors. One WebSocket counts once regardless of widget or
effect count. A refused handshake never enters the registry; a disconnect remains
counted until its connection cleanup deregisters it. This adds one identifier
and no configuration fields.

### The identity stops being erased — 2026-09-03: `Session` is generic, `Identity()` returns the application's own type

**The framework stopped erasing the two types the application owns.** The state
type had been a type parameter since the first commit; the identity had not, and
`Session.Identity()` returned the `IIdentity` interface every application then
asserted back with `sess.Identity().(Member)`. Operator ruling, 2026-09-03, in
full:

> `func (s Session) Identity() IIdentity { return s.identity }`
> RETURN TYPE IS IIDENTITY FUCK YOU

This was the LAST exemption CS-8 had left standing in the public surface, and it
was the genuine one — the concrete type lives in the caller's package and this
library cannot name it, which is the existential case. Go has no existential
types; it has type parameters, and that is the answer.

| Change | Source |
|---|---|
| **`Session` → `Session[I IIdentity]`, and `(Session[I]).Identity() I`.** No assertion at any call site, in this repository or a consumer's, and none possible: an identity of another shape is a compile error where it used to be a runtime deny somebody had to remember to write | operator ruling 2026-09-03 |
| **The parameterization is minimal but wide, because `Effect.Run` takes a `Session`.** `Effect[I]`, `Reducer[S, I]`, `Config[S, I]` and `App[S, I]` all carry it. Dropping the session from `Run` would have kept them all non-generic and is the option NOT taken: an effect acts on a session's behalf and its identity is an input to what it does, which is the property `Config.Execute` gained a `Session` for in Phase 1 | §10, Phase 1's effect-boundary entry |
| **`Config.Authenticate` returns `I`.** So does `livetest.NewSession`, which is now `NewSession[I]` | — |
| **`Anonymous` returns the new exported `AnonymousIdentity`**, a small concrete struct. An application with no accounts must still NAME an identity type, and the type it names must not be the interface — that would be the erasure the ruling removed, moved to the instantiation | — |
| **`AllowAll[I]` must be instantiated at the call site.** Go infers a generic function's type arguments on assignment to a variable of func type but not on assignment to a composite literal's field, and a `Config` is a composite literal | Go 1.21 inference rules |
| **`livebridge` inverted.** It was a function VARIABLE `live` assigned at init; a package-level variable cannot be generic, so it is now a capability `Token` that `live.NewSessionFor[I]` demands. The containment property is unchanged and better stated: the token is obtainable only from an `internal/` package whose importers `internal/arch` asserts are exactly `live` and `live/livetest`, so a consumer's handler cannot call the constructor because it cannot obtain the argument | **C-25** §6.3, restated |
| **`internal/session` and `internal/wsx` carry `I` too** — `Peer[I]`, `IApp[I]`, `Effect[I]`, `Actor[I]`, `Handler[I]`, `Options[I]` — rather than the adapter asserting the identity back at one erasure site. `IIdentity` survives ONLY as a constraint and as the parameter type of the admission bookkeeping that calls `Subject()` | operator acceptance criterion |
| **Two library errors and four specifications were DELETED as unreachable.** "The authentication hook returned no identity and no error" cannot happen when the hook returns the application's own type; nor can "denies an identity it does not recognise", "refuses an effect for a session whose identity is not a member", "closes the session for an identity that is not a member", or livetest's nil-identity guard. `internal/arch`'s FR-58 census moves 39 → 37 and `docs/error-audit.md` gains revision 7 | FR-58 |
| **A generated widget is generic in its host's identity type** and reads it never: `NewNodeStatus[I]()`. A widget document names no host, no address and no credential, so there is nothing for it to look at — the parameter exists to fit whatever host registers it | `pkg/widget/docs/ontology.md` |

### Effects become concrete — 2026-09-03: `IEffect` dies, `Effect` is a struct, `Config.Execute` is deleted. 56/53 → 56/54

**The framework that taught the rule was the last to obey it.** CS-8 — *return
concrete implementations; interfaces belong in parameters* — was minted, gated
and flipped to blocking on 2026-09-02, in this repository, by the same programme
that owns this library. It read 0 the whole time, because its detector looked at
BARE result types and this library's reducers hand back `[]IEffect`. The
operator's ruling of 2026-09-03, in full:

> `func retryWatch(ev live.Event) []live.IEffect { holy shit i hate you`

and with it the revocation of the shelter the interface had been sitting under:
CS-8's pass-through exemption covers **third-party** contracts only. A framework
this repository owns has a contract this repository chooses.

| Change | Source |
|---|---|
| **`IEffect` (interface) → `Effect` (struct).** Two fields, no methods: `Source string`, which is what `EffectSource()` returned, and `Run func(ctx context.Context, session Session, emit Emitter) error`, which is what `Config.Execute` did for that effect. Framework and application effects alike are now CONSTRUCTOR FUNCTIONS returning a value; a custom effect is a closure over whatever the application owns | operator ruling 2026-09-03 |
| **`Config.Execute` is DELETED.** It took one `IEffect` and type-switched on its dynamic type; with the behaviour on the effect there is nothing left to dispatch on. Its guarantee — an effect that never runs is a change that never happens — moved into the library: an `Effect` with a `Source` and a nil `Run` is refused with an `EffectFailedEvent` before a goroutine is spawned for it. A zero `Effect` is inert and is dropped, exactly as a nil element of the old slice was | operator ruling 2026-09-03, CS-8 |
| **`Reducer[S]` and `Config.Init` return `[]Effect`.** Both hook types change shape, and so does every assignment site in this repository | CS-8, CS-2 |
| **`Emitter` is now passed to `Effect.Run` rather than to `Config.Execute`**; `EffectFailedSourceField` now carries `Effect.Source` rather than `EffectSource()`. Neither value changes | — |
| **`livetest.ReplayN` compares effect SOURCES, not effect values.** `Run` is a function field and Go compares two function values only when both are nil, so a deep comparison would fail every determinism check rather than passing the honest ones. The narrowing is real and is stated at the function: the harness still catches a reducer that scheduled a different effect, a different number of them, or them in a different order — the shapes a clock, a random source or a map range produce — and no longer catches the same effect carrying a different argument. An effect worth telling apart is worth naming apart | FR-15 |
| **The internal seam got smaller.** `internal/session.IEffect` becomes `internal/session.Effect` on the same two fields, and `IApp.Execute` is **removed**: the actor calls `effect.Run` directly, so the one place a `session.IEffect` was asserted back to a `live.IEffect` is gone. `live`'s `toInternalEffects` is the module's single translation between the two vocabularies | CS-7, CS-8 |
| **`(Session).Identity() IIdentity` is untouched by this entry.** It was ruled on separately the same day; see the entry above | — |

### P3 style retrofit — 2026-09-02: every parameter in the surface is named. Surface unchanged at 56/53 and 37/33

**A second spelling-only pass, recorded for the same reason the `I`-prefix pass
above it was.** CS-2 of the house style skill says every parameter in every
signature carries a name — function declarations, function types and interface
methods alike — and the operator ruled on 2026-09-02 that the rule is read
literally, so a one-parameter hook type such as `Config.CSRF` is in scope
exactly as an interface method is.

`tools/apisurface` reads **`live 56/56` and `53/53`, `live/livetest 37/37` and
`33/33`**: a parameter name is not an identifier this ledger counts, so nothing
moves. That is the same blind spot the `I`-prefix entry records, and it is why
the type column below was edited by reading rather than by the tool.

| Change | Source |
|---|---|
| **Every `Config` hook type gains parameter names**, and the names are the ones the field's own godoc already uses in prose: `Init(ctx, session)`, `Execute(ctx, session, effect, emit)`, `Teardown(ctx, session, state)`, `Authenticate(request)`, `Authorize(ctx, session, event)`, `CSRF(request)`. No field is added, removed, retyped or reordered | operator ruling 2026-09-02, CS-2, house style skill |
| **The three escape hatches move with the hooks they implement.** `Anonymous(request *http.Request)`, `AllowAll(ctx, session, event)` and `NoCSRFCheck(request *http.Request)` are declarations rather than types, and CS-2 makes no distinction between the two | CS-2, §4 |
| **`Emitter` becomes `func(event Event) error` and `Fragment.Render` becomes `func(state S) templ.Component`.** Both are single-parameter func types, which is exactly the shape the literal reading of CS-2 covers and a narrower reading would have left alone | operator ruling 2026-09-02 |
| **`(*App[S]).PageHandler(page func(state S) templ.Component)` and `(*Client).Await(..., pred func(frame *Frame) bool)`** are the two methods whose *parameter's* type is itself a func type. The method's own parameters were already named; the nested func type's were not | CS-2, §1, §6 |
| **Nothing changes meaning.** A parameter name in a func type is documentation and nothing else: no call site moves, no implementation is invalidated, and a `Config` written yesterday compiles unchanged today | BL-30, checklist §1.7 |

### P3 style retrofit — 2026-09-02: every interface takes the house `I` prefix. Surface unchanged at 56/53 and 37/33

**This entry exists because the rename contradicts a `stable` marking on two
rows, and it was broken deliberately rather than by drift.** `stable` here means
"intended to survive to v1.0 unchanged" (§0), and `Effect` and `Identity` both
carried it. The operator ruled on 2026-09-02 (Widget Foundry decision 2 and
amendment 5) that every interface in the monorepo carries an `I` prefix,
repo-wide and **including** the published `candacelabs/csf` API, and that
the export gates are the check on that break rather than a veto on it. The
precedent was already set by the `IWidget[S]` refactor in slice P1.

`tools/apisurface` reads **`live 56/56` and `53/53`, `live/livetest 37/37` and
`33/33`**: nothing is added, removed or split, so neither count moves. A rename
is invisible to a counter, which is precisely why it is written down here.

| Change | Source |
|---|---|
| **`Effect` → `IEffect`.** The interface a transition returns for the actor to perform. Every signature that named it moves with it: `Config.Init`, `Config.Execute`, `Reducer[S]`, and the widget SDK's `Mount`, `Reduce` and `Effect` methods. The `EffectSource`, `EffectFailedEvent`, `EffectFailedSourceField`, `EffectFailedErrorField` and `EffectFailedRetryableField` names are **not** interfaces and do not move | operator ruling 2026-09-02, `docs/widget_foundry.md` decision 2 |
| **`Identity` → `IIdentity`.** The application's identity for a session, and the return of `Config.Authenticate`, `live.Anonymous` and `livetest.NewSession`'s third parameter. The `(Session).Identity()` **method** keeps its name — CS-1 renames types, not methods | operator ruling 2026-09-02, amendment 5 |
| **Both were marked `stable`, and this breaks that marking.** Recorded rather than quietly re-marked: the rows still read `stable`, because the intent they describe — no further shape change before v1.0 — is unchanged by a spelling. What changed is the spelling, under a ruling that outranks the marking | **FR-65**, §0's stability definition |
| The internal redeclarations move in the same commit: `internal/session.Effect`, `internal/session.Identity`, `internal/session.App`, `internal/livebridge.Identity` and `internal/protocol.Inbound` become `IEffect`, `IIdentity`, `IApp`, `IIdentity` and `IInbound`. They are exported inside `internal/`, so they take the exported spelling | CS-1, house style skill |

### Phase 4, FR-54 failure 1 — 2026-08-05: `F-CHT-3` becomes expressible. +0 identifiers, +2 fields (51 → 53), +38 gzipped bytes

**`docs/gates/phase-4.md` §5.6 failure 1 sat undecided for three revisions, and
[`reviews/fr-54.md`](reviews/fr-54.md) §12 decided both halves of it.** This is
that ruling landed: `tools/apisurface` reads **`live 56/56` and `53/53`,
`live/livetest 37/37` and `33/33`** — the identifier row does not move.

| Change | Source |
|---|---|
| **`Bind.NoModifiers bool` and `Bind.PreventDefault bool`**, components 7 and 8 of the binding grammar, both defaulting to today's behaviour and both trimmed when unset — every binding the tree renders today is byte-identical. `F-CHT-3` reads `live.Bind{Keys: []string{"Enter"}, NoModifiers: true, PreventDefault: true}` and emits `keydown:chat.send:Enter::::1:1` | **FR-54** clause 3, `gates/phase-4.md` §5.6 failure 1, `reviews/fr-54.md` §12 |
| **The full modifier set is REFUSED** — `Bind.Modifiers []string` or a `Modifier` bitmask. `F-CHT-3` needs "no modifier held" and nothing else; a set must be three-valued (*don't care* / *exactly none* / *exactly these*) because `F-CTR-6`'s `+` **is** `Shift`+`=`, and three-valued costs a sentinel identifier or a `nil`-versus-empty-slice trap. Measured at **+57 gzipped bytes against +34**. Re-open trigger at `reviews/fr-54.md` §13 | **FR-65**, L9-1 |
| **Both refusal arguments of record were aimed at the wrong target and are corrected rather than dropped.** *"A chord belongs to the browser"* is true of `Ctrl`/`Meta`/`Alt` and not of `Shift+Enter` in a textarea, which no browser or OS claims; `KeyboardEvent.key` already folds `Shift` into every printable value, so the gap was only ever "`Shift` on a non-printable key". *"A library that `preventDefault`s on the application's behalf takes over `Ctrl+F`"* is true of a default and describes no opt-in — the runtime already calls it for a declared submit and a declared anchor click | `reviews/fr-54.md` §10 |
| **The suppression sits BELOW the composition guard, and that ordering is the landing's one non-obvious line.** `reviews/fr-54.md` §12.1's prototype folds `s[7]` into the `preventDefault` call **above** `if (composing) return`, and L9-1 caught it in §14 C-9: `Enter` during an IME composition *commits the candidate*, so a binding that suppressed it would take the commit key away from every composer that uses one (FR-26). The submit and anchor cases stay above the guard — moving **them** below it would let a form navigate for real mid-composition, which is a second defect wearing the first one's fix. Three specs, one per direction | **C-9**, `client/test/binding.test.mjs` |
| **`NoModifiers` reads exactly four booleans and its two surprises are documented and asserted.** `AltGr` sets `ctrlKey` **and** `altKey`, so a printable key that needs `AltGr` — `@` on many European layouts — does **not** match a binding that names it and sets this option, while the member types exactly the character the binding asked for. `CapsLock` and `NumLock` set none of the four and filter nothing. A silent non-firing is this requirement's own failure mode, so both are specs rather than sentences | **C-6**, godoc, `binding.test.mjs`, `keybinding_modifiers_test.go` |
| **A binding this filters out does not end the client's match loop**, so the next binding for the same DOM event gets its turn. That is what makes "`Enter` sends, `Shift+Enter` does not" **two bindings on one element** rather than a three-valued modifier field — the shape §13 refuses | consequence, spec |
| **Two corrections to `reviews/fr-54.md` §12.1's own text, found by building it.** (1) The accepted `NoModifiers` godoc said *"It has no effect on a binding with no Keys, and none on an event that carries no key"* — **both clauses are false** under the client the same section specifies: `s[6]` is tested whether or not `s[2]` is set, and a `MouseEvent` carries the same four booleans, so the option on a `click` binding means a plain click and not a `Ctrl+click`. The godoc states what the code does instead. (2) `Bind.Keys`' standing sentence *"this library never calls preventDefault for a key"* is falsified by this landing and is corrected in place rather than left standing | **L9-1 §12.1**, DEV-1 |
| **Client cost: +81 B minified, +38 B gzipped.** 10,306 → 10,387 and 4,421 → 4,459, headroom 7,829 B (63.7 %). **That is 19 B minified and 4 B gzipped above the ceiling `reviews/fr-54.md` §14 C-3 pre-registered**, and the whole of the difference is C-9: L9-1's §11 price of 10,368/4,455 was measured on the prototype whose `preventDefault` placement C-9 then corrected, and it **reproduces exactly** on this tree. No spelling of the corrected shape reaches 4,455 — the measured floor across twelve is **4,456**. Recorded rather than absorbed: `client/SIZE.md` §1.1.6 carries the table and the ruling is L9-1's to make | **NFR-2/NFR-3**, `client/SIZE.md` §1.1.6, **C-3** |
| ⟨**CORRECTED 2026-08-05 — one sentence in the cost row above names a floor that is the number of a shape this project REFUSED. FR54-10, [`reviews/fr-54.md`](reviews/fr-54.md) §18.2 and §28; the row is kept for the record.**⟩ *"No spelling of the corrected shape reaches 4,455 — the measured floor across twelve is **4,456**"* is two claims, and only the first is sound. **`4,456` is not a floor over corrected shapes; it is not a corrected shape at all.** It belongs to the spelling that hoists the composition guard **above** the submit/anchor `preventDefault` and folds everything below it — under which a submit or an anchor click mid-composition is **no longer suppressed and navigates for real**, and which turns exactly one committed spec red (*"a submit still has its default suppressed mid-composition"*, 22 pass / 1 fail). It was built by L9-1 and **rebuilt independently by DEV-2**, reproducing `10,366 / 4,456` and the single red both times. **The floor over shapes that pass is `10,372 / 4,458`** — the folding, which is available, correct, 23/23 green, and refused **on merit** rather than on price ([`client/SIZE.md`](../client/SIZE.md) §1.1.6). Every other figure in the row above is correct and re-measured at HEAD: `10,387 / 4,459`, headroom `7,829 B (63.7 %)`. **The reason this correction is not a stale-number fix:** a later reader handed `4,456` as "the floor" would take it as a target and reimplement the defect, which is the one outcome the C-9 finding exists to prevent | **FR54-10**, L9-1, DEV-2 |
| **§10's `Modifier state is not compared` row below is superseded**, and its *"a finding for PM-1"* is discharged. `bench/README.md`'s three-reason list for `F-CHT-3` loses its third reason to `2ab18690` and its first two to this | FR-72/FR-73 |

### Phase 4, FR-54 failure 2 — 2026-08-05: a binding's options belong to that binding. Surface unchanged at 56, and the artifact got smaller

**`docs/gates/phase-4.md` §5.6 named this a failure of FR-54's "complete" and
was explicit that it was *not* choosing the API shape.** This is the shape, and
the argument for it, at **+0 exported identifiers** — `tools/apisurface` reads
**`live 56/56` and `51/51`, `live/livetest 37/37` and `33/33`**, unmoved. Nothing
here is a new symbol: `Bind.Fields`, `Bind.Debounce` and `Bind.Throttle` were
already the right *declaration*. What was wrong is where the value landed and
what read it.

| Change | Source |
|---|---|
| **The three options move out of the element's attributes and into the binding's own spec.** `data-gotth-fields`, `data-gotth-debounce` and `data-gotth-throttle` no longer exist; `data-gotth-on` is `"<domEvent>:<eventName>[:<key>[:<debounceMs>[:<throttleMs>[:<fields>]]]]"` with trailing empties trimmed, and `dispatch` reads all of it off the spec it matched. The runtime's timer record is keyed by the matched spec inside the element's existing `WeakMap` entry — so is the throttle's last-fired stamp, which had the identical defect one line up | **FR-54** clause 2, **§5.6 failure 2** |
| **The defect, as measured rather than as derived.** QA-1 drove it in Chromium against the real shipped runtime and the real helpers ([`docs/qa/fr-54-debounce-repro.md`](qa/fr-54-debounce-repro.md), verdict **REPRODUCES**, 8 specs, 3 negative controls). On the guide's own composer an `Escape` binding inherited the `input` binding's 150 ms and a keystroke 3 ms later **destroyed** the pending clear — one event reached the server for the pair and it was the draft. Symmetric: an `Escape` inside the window destroyed a pending draft, so the server never learned what was typed while the browser went on showing it. And the key binding was **late even when nothing followed it**: 158.8 ms against 1.3 ms unencumbered | QA-1, §5.6 |
| **Two defects, one cause, and each needs its own half.** A per-binding timer with a per-element interval still delays a key binding for a reason its author never wrote down — QA-1's mutation control measured exactly that and watched the delay survive. A per-binding interval with a per-element timer still loses an event whenever two bindings on one element both debounce. Both halves landed; `client/test/binding.test.mjs` has a spec for each and a mutation control for each | measurement, checklist §8.2 |
| **`OnAll`'s first-wins rule is now vacuous, and the godoc says so rather than leaving the old sentence standing.** That rule existed for a real reason — it is what an HTML parser already did with the duplicate attribute `OnAll` replaces, so moving a page from two spreads to one call could not silently change which debounce was in force for the surviving binding. Per-binding scoping keeps that property and **extends** it: composition now changes nothing about *any* binding, and a binding rendered by `OnAll` is byte-identical to that binding rendered alone. What remains in the code is a defensive carry-through for anything that is not a binding | §5.6's constraint, `templ.go` godoc |
| **`Fields` moved too, and that was a decision rather than a default.** QA-1's §6 recommended leaving it element-scoped, on the ground that `fields(el)` reads the element's form or its own `name` anyway. **Half of that is right and the half that is right is untouched**: what the *element* contributes is still the element's, and `fields()` still reads it. `Bind.Fields` is not that — it is one binding's static payload, and two bindings on one element are entitled to different ones. The natural case is two keys raising the same event with different `dir` values, which is the benchmark counter's two-keys-one-element shape with a payload; under first-wins the second silently sent the first's. Leaving one of three element-scoped would also have made the rule *"an option belongs to its binding"* into a rule with an exception nobody could predict from the type | decision, recorded either way per §5.6 |
| **No new separator, and no key value taken away.** `Bind.Keys` promises exact, un-normalised `KeyboardEvent.key` matching where `"+"` is legal, and the grammar had already spent `:` and `;`. The three new components are further `:` fields of a split that was already happening, so the set of characters a key may not be is **exactly what it was** — still `:` and `;`, and still stated in `Bind.Keys`' godoc. A `,` or `\|` list inside one component would have taken a printable key away; a second element attribute is the defect being fixed | **FR-54**, `Bind.Keys` godoc |
| **Fields go last, and the encoding is the reason that is safe.** `encodeFields` is `net/url`'s query encoding, which escapes `:` and `;` in keys and values alike, so a caller's data cannot split the binding it sits in. That is the only component whose content is a caller's, and it is asserted rather than assumed — `live/binding_test.go` pins `{"at": "12:30", "and": "a;b"}` as `and=a%3Bb&at=12%3A30` | consequence, spec |
| **Client cost: −85 B minified, −8 B gzipped.** 10,391 → 10,306 and 4,429 → 4,421, headroom 7,867 B (64.0 %). The first landing in `client/SIZE.md` §1.1 that costs nothing: three attribute constants, three `getAttribute` calls and their argument strings were replaced by three subscripts into a `split` the dispatch path was already performing, and nothing is stored in the timer map for a binding that neither debounces nor throttles | **NFR-2/NFR-3**, `client/SIZE.md` §1.1.5 |
| **This ledger's own claim that it could not be done is corrected in place.** The checkpoint-3 `OnAll` row said the options *"cannot be per binding without a second timer table in the runtime"*. There is no second table — the entry that already existed grew a key. The same row's *"the consequence is a wart and it is documented in the godoc"* was the source of a wrong attribution PM-1's §5.6 then quoted: **the godoc never used the word**, this row did. Both are marked at that row rather than edited out of it | QA-1, checklist §1.7 |
| **`Script`, `Region`, `Preserve`, `Document` and every `Config` field are untouched.** The only exported behaviour that changed is the markup `On`, `OnWith` and `OnAll` emit, which is `client/SIZE.md` §7's contract and is versioned with the runtime that reads it: both ship in the same binary, embedded by exact filename, so there is no mixed-version window | checklist §1.7 |
| ⟨**CORRECTED 2026-08-05 — the last clause of the row above is false, and the row is kept for the record.** The two artifacts ship in one binary; **they do not arrive at one browser.** `live.Script` renders `src="<mount>/gotth-live.min.js"` for every build of every version — **no fingerprint in the path** — and `serveAsset` answers it `Cache-Control: public, max-age=31536000, immutable` (`live/templ.go:627`), so a browser that fetched the runtime before an upgrade will not revalidate it for a year. **Driven, not derived**, with `client/runtime.js` at `2ab18690^` against markup HEAD renders today: `armed timers: 0 | events on the wire: ["c.draft"]` — a declared 150 ms debounce silently gone, a frame per keystroke — and `event: f.one | fields delivered: []` — `Bind.Fields{room:"alpha"}` silently dropped. No error, no console warning, no `4003`: the version check is on the **wire protocol**, and `client/SIZE.md` §7 is a second contract between the same two parties that it does not cover. `docs/guide/deploying.md:37`–`:60` already documents the immutable cache and names the two levers, but its *"within a protocol major version that is usually harmless"* reasons only about the protocol and is wrong for this upgrade. **The window is real, it is silent, and it is a documentation condition rather than a design one** — no fingerprinted-URL option is asked for here⟩ | **L9-1**, [`reviews/fr-54.md`](reviews/fr-54.md) §4, condition FR54-1 |

### Phase 4, PS-1 — 2026-08-05: the page shell's inexpressibility claim was false, and is now true. Surface unchanged at 56

**This section corrects the one below it rather than rewriting it.** The row
beneath says the shell *"makes it inexpressible, which is `Mux`'s argument rather
than `MustNew`'s"*. **That was not true when it was written**, and L9-1 proved it
by probe at [`reviews/page-shell.md`](reviews/page-shell.md) §3.2, using only
exported API:

```go
app.Document("/live", "t", nil, live.Script("/live"))
// -> runtime, inspector, runtime, dev-reload
```

Both tags are deferred and deferred scripts run in document order, so that first
runtime opened its socket before the inspector wrapped `WebSocket` and the
inspector then showed nothing, silently — **the ordering failure itself**, not
the "duplicate tag with a different shape" the landing called it. A `Document`
nested in the head reached the same place, so it was a class and not a spelling.

| Change | Source |
|---|---|
| **`Script` refuses to render inside `Document`'s head content**, returning an error and emitting no tag. `Document` marks the context while it renders head content; `Script` reads the mark. **No exported identifier**: one unexported context key, one package-level error, `+0` to both columns of §0, which `tools/apisurface` confirms at **56/56 and 51/51**. Route (a) of PS-1's two, taken because the alternative was to weaken the sentence that is the whole argument for spending the identifier — and because a silently blind inspector becoming `PageHandler`'s 500 with a named reason is what this library does everywhere else | **L9-1 PS-1**, checklist §9.7, FR-44, NFR-8 |
| **`Script`'s contract changed, and this is the row that says so.** The section below claims *"Nothing existing changes meaning … `Script` … untouched"*, which was true of that landing and is **not** true of this one: an exported symbol gained a failure mode. It is reachable only from a context `Document` sets, so every hand-written shell — the four left in `bench/apps/*/gotth` and `test/memory` — is unaffected, and `Script`'s row at §5.2 now names the refusal | checklist §1.7, BL-30 |
| **What is deliberately still expressible**, because a claim with an undisclosed hole is what got corrected here. `Script` among `Document`'s **children** renders: it lands *below* the inspector, the ordering holds, and what remains is a duplicate runtime tag — two sockets on one page, a real defect with a genuinely different shape. And `NoRuntime` sets no mark, so hand-placing `Script` on a page that has declared itself not live still works and has no inspector to be ordered against. Both are pinned by specs rather than described | **L9-1 PS-1**, §9.7 |
| **The byte order is pinned as bytes.** `live/document_test.go` asserts the head's exact contents in sequence rather than comparing three `strings.Index` results, which is an assertion that passes on any document containing the three substrings *somewhere*. Nine specs cover PS-1 and PS-2: the composed case, the nested case, production as well as dev, the inspector-in-head case that stays legal, `Script` outside a `Document`, `Script` in the children, and both `NoRuntime` escapes | **L9-1 PS-1**, checklist §8.1 |
| **`NoRuntime`'s FR citation corrected, `NFR-5` → `FR-61`** at §5.2. NFR-5 is *"no npm at runtime, no build step imposed"* and is satisfied identically with or without this symbol; `FR-61` is the chat example, whose `LoginPage` is the consumer the symbol exists for. **The row below still cites NFR-5 and is left standing**, because it is the dated record of what was claimed | **L9-1 PS-3**, §0's *"a symbol with no FR is a symbol to cut"* |
| **The count did not move: still 20 + 11 = 31.** Nothing here touches a counted line — a context key, an error, a doc rewrite and specs | FR-53 |

### Phase 4, FR-53 — 2026-08-05: the page shell, 54 → 56 identifiers

| Change | Source |
|---|---|
| **`(*App[S]).Document(mountPath, title string, htmlAttrs templ.Attributes, head ...templ.Component) templ.Component`**, +1 identifier, +0 struct fields. The library-owned page shell §5.2 describes. **Its first argument is not FR-53.** `api-surface.md`'s `InspectorScript` row has stated an ordering invariant — the inspector above `Script`, because both are deferred and the inspector must wrap the WebSocket constructor before the runtime opens a socket — since checkpoint 2, and until now nothing enforced it; getting it backwards produces an inspector that silently shows nothing. This makes it inexpressible, which is `Mux`'s argument rather than `MustNew`'s | **L9-1** `docs/reviews/fr-53-line-budget.md` §3.2 and §3.3's nine constraints, **FR-53**, FR-44, NFR-8 |
| **`NoRuntime`**, +1 identifier. The `mountPath` sentinel for a page in a live application that is deliberately not live. It is a symbol rather than an empty string because an absence would mean "the author forgot" and would render a page that loads perfectly and does nothing — the failure `Script`'s refusal of a default exists to prevent. Precedent is `AnyOrigin`, which is the same construct: a named, greppable, non-registerable sentinel in a field that otherwise takes real values | **L9-1** §3.3 constraint 6, FR-53, NFR-5 |
| **Consumers in this landing: four call sites in three modules, three of them not the quickstart.** `docs/guide/_samples/quickstart/view.templ`, `examples/counter`, `examples/chat` (`Page` **and** `LoginPage`, the non-live page that is `NoRuntime`'s named consumer) and `examples/dashboard` (whose head extension carries a conditional third-party `<script>`, which is the case a shell that could not extend its head would have failed on) | checklist **§1.4**, L9-1 §3.3 constraint 1 |
| **No `any`, no `interface{}`, no options struct.** Four ordinary parameters and no new type. `templ.Attributes` is a dependency's map type that this package's five attribute helpers already return and the only argument `templ.RenderAttributes` accepts, so it is this surface's existing vocabulary rather than a widening of it | **FR-65**, L9-1 §3.3 constraint 8 |
| **Nothing existing changes meaning.** Additive only. `Script`, `InspectorScript` and `DevReloadScript` are untouched and hand-written shells still work — seven of the tree's shells were hand-written yesterday and four of them are still hand-written today (`bench/apps/*/gotth`, `test/memory`), by a different owner, unbroken | BL-30, checklist §1.7 |
| **What it measured.** The quickstart's counter goes **20 Go + 19 templ = 39** to **20 Go + 11 templ = 31** under PRD v0.6's frozen counting rule, counted over the shipping sample files rather than over the page. The Go half did not move: `app :=` inside `main` became `var app =` above it, which is the same counted line in a different place, and the shell's invocation costs 5 templ lines where the hand-written one cost 13. **This ledger reports that number and does not grade it**: §5.I (e) trigger 1 is PM-1's to fire, and it is the repaired trigger, so a floor above 31 would have withdrawn the amendment rather than moved the budget up to meet it | FR-53, PRD §5.I (e), `docs/pm/fr-53-amendment.md` |

### Phase 4, FR-53 — 2026-08-05: the first app shrinks by seven lines, 51 → 54 identifiers

| Change | Source |
|---|---|
| **`(*App[S]).PageHandler(func(S) templ.Component) http.Handler`**, +1 identifier. This is the API half of QA-1's **F-4**, specified there in one clause — *"a `live`-owned page handler that takes the same loader `Init` takes — one that cannot be given a state value, only a way to get one"*. The loader it takes is `Config.Init` itself, which is the strongest available reading of that clause: the page and the session's first snapshot are not merely written from one function, they **are** one function, so an application that later gives `Init` something real to do gets a correct first paint from that same edit and no second one | **QA-1 F-4**, phase-4.md §4.2, **FR-53** |
| **`(*App[S]).Mux(mountPath string, page http.Handler) http.Handler`**, +1 identifier. The three registrations a single-application server needs, with the two silent failures the quickstart *measures* — a missing subtree registration, which leaves the runtime's URL to the catch-all and produces `200 text/html`, no WebSocket attempt and no server-side error; and the `http.StripPrefix` repair, which turns the upgrade into a 307 a WebSocket client cannot follow — made inexpressible rather than documented. `App.Handler` is unchanged and is still the way onto a router of your own | quickstart §2's two measured tables, **FR-53**, FR-33 |
| **`MustNew[S](Config[S]) *App[S]`**, +1 identifier. **The one addition here whose only argument is economy**, and it is recorded that way so it can be cut on that ground: it removes no class of bug — `New`'s error cannot be silently ignored in Go — and it buys three lines of a four-line idiom in `main`. What it has instead is exact stdlib precedent (`template.Must`, `regexp.MustCompile`, `netip.MustParseAddr`) and a requirement, FR-53, that is measured in lines | **FR-53**, §0's "a symbol with no FR is a symbol to cut" |
| **`Config.Init` becomes optional**, no identifier and no field moves. Nil is the zero value of `S`, no effects, no error. The argument is that it is the only *total, side-effect-free* thing an unwritten mount hook could mean; that `Teardown`, the hook on the other end of the same session, has always been optional on that argument; and that forgetting it is visible on the first run — the sessions start empty and, through `PageHandler`, so does the page — where a guessed `Origins`, `Authenticate`, `Authorize` or `CSRF` would not be visible at all. **Those four stay required and were not touched**: there is no nil that means "off", and a bundle that set them in one line was considered and refused in the same pass | **FR-53**, PRD §9, quickstart §2 |
| **Nothing existing changes meaning.** Additive only: no rename, no signature change, and a `Config` that sets every field today behaves identically. The three examples, the three benchmark applications, the guide samples and the test modules were not edited for this | BL-30, checklist §1.7 |
| **What it measured.** The quickstart's counter goes **27 Go + 19 templ = 46** to **20 Go + 19 templ = 39** under PRD v0.6's frozen counting rule. **FR-53's ≤30 still fails, by 9**, and the two shrinks that would have closed the Go half further were both refused above rather than taken | phase-4.md §4.2, FR-53 |
| **One false sentence deleted while in the file.** `live/example_test.go`'s package overview said *"A router strips it before the handler is reached"* and demonstrated `http.StripPrefix` — the exact mounting the library documents as broken, in the library's own runnable example. QA-1 routed it as item 2 of the F-4 handoff. It now uses `Mux` | **QA-1 F-4 handoff item 2**, FR-66, §7.3 of phase-4.md |

### Livetest wave — 2026-08-04: `livetest.Client` implemented, and the ledger corrected against it

| Change | Source |
|---|---|
| **`Client` shipped**, `live/livetest` ceiling **9 → 37** identifiers and **6 → 33** struct fields. `live` unmoved at 49/49, 50/50 — nothing about this crosses into the production package. The ledger's four `Client` rows became 28: `NewClient` + `ClientOptions` (the constructor the ledger had no row for), the ten retrieval and send methods the two hand-rolled harnesses converged on, and the `Frame`/`Patch`/`Origin`/`Update`/`Error` view with three `Patch` accessors | §6, and the full argument for each row is there |
| **REV-DUP D-3's *"no new exported symbol"* was measurably wrong, and this is the correction rather than an override.** D-3 read the four ledgered rows and concluded the work needed no ruling. Two things are only visible from inside the implementation: `Client` had **no constructor** — the only ledgered function returning one was `Audit`'s callback parameter, so an FR-63 test could not obtain a `*Client` — and `WaitFor(fragmentID, func(html string) bool)` cannot express what the named consumers assert, which is *which* fragments a patch carried, *which* events a coalesced patch names, how many patches an unacknowledging client is sent, and how many **bytes** a resync cost. A `Client` at the ledgered surface would have retired none of the three decoders | REV-DUP **D-3**, `examples/chat/FRICTION.md` F-1, api-surface §6 |
| **The `Client.Frames()` refusal is amended, not dropped.** It refused an accessor because it *"would drag `internal/protocol`'s generated types into the public surface"*. True of `*gotthlivepb.Frame`; false of `livetest.Frame`, a plain struct this package owns. Decoding uses the library's own generated types and projects the result, so a regenerated schema is not a consumer-visible event. `Origin.Kind` and `Error.Code` are `int32` for the same reason. **No accessor returning a generated type exists or is proposed** | §6, REV-DEL finding 1 |
| **`Audit` and `Report` remain unimplemented, and their status rows now say so honestly.** Their shape is still unwritten by any consumer, and exporting a guess at it is the failure FR-65 names. They keep their rows and their `experimental` marking; nothing was added for them | checklist §1.4, FR-65 |
| **Nothing here is speculative surface.** Every row names a requirement and a consumer that asserts on it in this tree today. The measure of that claim is the dedup it unblocks: three suites carrying byte-identical `protowire` decoders and a sixfold-repeated `browser` driver | REV-DEL finding 1, REV-DUP D-3 |

### Review wave, ruling 1 — 2026-08-04: `livetest.NewTB` withdrawn in favour of `GinkgoTB()`

| Change | Source |
|---|---|
| **`livetest.NewTB` removed**, `live/livetest` ceiling **10 → 9** identifiers; struct fields unchanged at 6, and `live` unmoved at 49/50. Ginkgo ships `GinkgoTB()` — *"a wrapper that exactly matches the testing.TB interface … intended to be used as a drop-in replacement with third party libraries that accept testing.TB"* — and this module has required `v2.32.0` since before `NewTB` landed. The adapter's premise, stated as fact in four documents, is *"a Ginkgo suite has no way to produce one"*: true of `GinkgoT()`, **false of `GinkgoTB()`** | REV-DUP **D-2**, verified in-container: `go doc github.com/onsi/ginkgo/v2 GinkgoTB` |
| **The repository already disagreed with itself.** `livetest.NewTB(Fail, GinkgoWriter)` at 16 Go call sites across 7 modules against `GinkgoTB()` at 10, and **four files spell it both ways** — `examples/chat/chat_test.go`, `examples/counter/counter_test.go`, `bench/apps/chat/gotth/chat_test.go`, `bench/apps/counter/gotth/counter_test.go`. `livetest`'s own package is the fifth site and is sharper than a file: `session_test.go` used `GinkgoTB()` exclusively while `tb_test.go` next door existed only to prove the adapter | measured, REV-DUP §2 |
| **The generality has zero consumers, and NFR-10 is why.** What `NewTB` bought over `GinkgoTB()` is a framework that is neither Ginkgo nor `testing` — and **NFR-10 mandates Ginkgo v2 + Gomega** for this repository. §6 cited NFR-10 as a *requirement* for the adapter; it is the requirement that empties it. Checklist §1.4 wants ≥ 2 real call sites for an abstraction and this one has none, which is the condition §1.4 exists to reject | **NFR-10**, checklist **§1.4**, FR-65 |
| **No dependency moves, in either direction.** `NewTB` took a handler so `livetest` need not import Ginkgo; `GinkgoTB()` is called from the consumer's own test file, so `livetest` imports nothing either way. dependencies.md §4's measured **+17 modules / +3,484,016 B** is the cost of a *`livetest` import of Ginkgo*, which neither option pays. That row stays correct, stays on file as the standing rejection, and its naming of `NewTB` as the chosen alternative is corrected | dependencies.md §4 |
| **The nil-`TB` panic property is obsoleted rather than lost, and this is now a spec rather than a claim.** `NewTB` embedded a nil `testing.TB` so an unimplemented method panicked, which its godoc called *"the failure mode to want"*. `GinkgoTB()` implements `Cleanup`, `Helper`, `Name`, `TempDir` and the rest for real — strictly better than stopping. `livetest_test.go` now drives `ReplayN` **and** `AssertDirtyComplete` through `GinkgoTB()` and asserts `Cleanup`/`Helper`/`Name` do not panic; mutated by making the reducer impure, which turns that spec red through `GinkgoTB()`'s own failure path | checklist §8.2, `live/livetest/livetest_test.go` |
| **`Helper()` was the tell.** `NewTB`'s `Helper()` is a no-op and it hard-codes `failCallerSkip = 1` to attribute a failure to the caller. `GinkgoTB()`'s `Helper()` calls `types.MarkAsHelper(1)` — the real mechanism. The adapter reimplemented, worse, the one thing it existed to work around | `ginkgo_t_dsl.go`, v2.32.0 |
| **Withdrawn now because withdrawing later is not available.** §0: exported symbols are permanent. BL-30 makes v0.1 a no-compatibility-commitment release, so this is the last moment the removal costs 16 mechanical edits instead of a deprecation cycle | §0, BL-30 |
| **The 16 call sites are not migrated in this commit** and the migration is specified rather than performed: they belong to the `livetest.Client` wave, and the substitution is verified rather than asserted — applied to a scratch tree, `go build`/`go vet` clean and `go test -race -count=1` green in all 7 modules plus the root | rulings-review-wave.md §1.4 |

### Review wave, ruling 2 — 2026-08-04: the `fragment` render label is deleted, not exported

| Change | Source |
|---|---|
| **No `Config` field is added.** REV-DEL finding 3 offered a fork — delete `obs.Metrics.FragmentLabels` and instrumentation.md's row, or add the `Config` opt-in that makes the row true. The opt-in is refused: it is an exported field with no named consumer, which §7.1 already settled as an FR-65 review rejection for `Config.OnPatch`, on the same ground and with the same revisit condition | **FR-65**, §7.1 precedent, checklist §1.4 |
| **And it would have shipped a metric that lies, which is the ground that settles it rather than the cardinality one.** `gotthlive_render_duration_seconds` is recorded **once per render pass** over all dirty fragments (`actor.go`, one `RenderDuration` call around `renderPass`), and the label was `firstFragment(res.Updates)` — whichever fragment happened to be first in the update slice. Labelling a whole-pass duration with one fragment's identity is misattribution, not opt-in detail. Per-fragment attribution needs the timing moved inside the per-fragment loop first; only then is there anything a label could truthfully name | measured at the call site, rulings-review-wave.md §2 |
| **Cardinality was checked and is *not* the objection**, stated so the next reader does not re-derive it. `fragment` values come from `Config.Fragments`, are fixed before the first connection, and instrumentation §2.1 already bounds them by registration — a fragment ID is a declaration, not a causal ID, so §2.1's no-causal-ids-as-labels rule does not bite. The knob was refusable on FR-65 and on correctness without reaching for it | instrumentation §2.1 |
| **`live`'s surface does not move: 49/49 and 50/50.** `FragmentLabels` is a field of `internal/obs.Metrics` and is deleted there along with `fragmentAttr`, the branch, `firstFragment`, and `RenderDuration`'s third parameter — all internal, none ledgered here. The row exists because the *decision not to export* is the reviewable half | **FR-65** |
| **Re-add trigger, pre-registered.** Split the instrument to per-fragment timing, name the operator who needs the series, and the field lands with that consumer in the PR — the same shape §7.1 records for `Config.OnPatch` and §7 for `IsRetryable`, which is the one that fired | §7, §7.1 |

### Checkpoint 3, D-29 — 2026-08-04: a refused resync retries itself, and the surface does not move

| Change | Source |
|---|---|
| **No exported identifier, no `Limits` field, no protocol change, no new attribute.** The whole of D-29's client half is inside `client/runtime.js`: a refused `ResyncRequest` re-arms on a bounded equal-jitter schedule instead of latching until RFC §7.4's slow-client eviction closes the connection, and a patch discarded because of the gap is still acknowledged at the sequence the client actually holds. It adds **no exported identifier and no struct field**, in `live` or in `live/livetest`, so §0's counts are untouched by this item | QA-2 **D-29**, checkpoint-3-chaos.md §5, **FR-11**, RFC §7.6 |
| **Client cost: +223 gzipped bytes**, 4,137 → 4,360 on a 12,288 B ceiling, headroom 7,928 B (64.5 %). **126 of the 223 are one import**: naming `ErrorCode.RATE_LIMITED` stops the generated enum table being tree-shaken, where a bare `6` measures 4,231 B. The import is kept for the reason `PatchOp` and `ResyncReason` are already imported whole — one generated table is the single source of truth for a value the wire fixes — and the measured alternative is recorded so the trade can be reversed without re-deriving it | **NFR-2/NFR-3**, `client/SIZE.md` §1.1.3 |
| **The wire gap this fix works around, filed rather than closed.** `Error` carries a code, a message, the causal ids and `fatal` and **no retry-after**; the `Snapshot` re-asserts the heartbeat interval, the inbound frame cap and the ack window but **not** the resync budget. So a client cannot be told when to retry or what `MinResyncInterval` is in force, and the schedule's 1 s base is RFC §7.6's documented *default* — a guess that grows until it stops being wrong. Adding either field is a schema change and belongs to whoever owns `proto/`; **no client-side change can close it** | protocol.md §3.3, §3.5; RFC §7.6 |
| **Nor can the client tell which frame a `RATE_LIMITED` refused.** A refused resync's `Error` carries a server-minted `event_id` stood in for `client_ref` too (`ingressResync`), which is indistinguishable from the ids on an error refusing an ordinary `Event`. The runtime keys off its own gap latch instead, which is sound in both directions and means an event flood cannot be turned into a resync flood | consequence, `runtime.js` `refused()` |

### Checkpoint 3 — 2026-08-04: `livetest.NewTB`, and the two-package cap held rather than spent

| Change | Source |
|---|---|
| **`livetest.NewTB` added**, `live/livetest` ceiling **9 → 10** identifiers, fields unchanged at 6. Every helper in the package takes a `testing.TB`, Ginkgo's `GinkgoT()` deliberately is not one, and the operator mandates Ginkgo for this project — so five files carried the identical forty-line embedded-nil-`TB` adapter: `examples/counter`, `examples/chat`, `examples/dashboard`, `docs/guide/_samples/apptest`, and `docs/guide/testing-your-app.md`, whose prose *explained the workaround* and told the reader to paste a sixth. All five are deleted | **FR-15**, **NFR-10**, checklist §8.1 |
| **It takes the failure handler rather than importing Ginkgo, and there is no third package.** The two obvious shapes are both worse: a Ginkgo import in a non-test file of `livetest` makes Ginkgo a *build* dependency for every consumer of the package including non-Ginkgo ones — seven modules of content, against the four §5.1's D7 disclosure promises are metadata only — and a `live/livetest/ginkgotb` leaf would need an L9-1 ruling, because §0.1 caps this surface at two exported packages and `internal/arch` asserts it. `func(message string, callerSkip ...int)` is Gomega's own answer to the same problem in `RegisterFailHandler` | §6, dependencies.md §4 |
| **The panicking-nil-`TB` property survives and is now proved — and the sentence every copy carried about it was wrong.** All five claimed the panic "names the method"; it does not. A promoted method is a generated wrapper that inlines away, so the panic is a plain nil-pointer runtime error and what the stack names is the *caller* — which is the useful half, since in real use that caller is the line in `ReplayN` that reached for the method, but it was never checked. `tb_test.go` asserts the panic, its type, and the caller's name | checklist §9.7 |
| **`Error` and `Errorf` are fatal**, and the godoc says so rather than leaving it to be discovered: a framework failure handler aborts by contract and there is no record-and-continue mode to map them onto. Both arguments are required and `NewTB` panics naming the nil one, because a `testing.TB` whose failures go nowhere makes every helper in the package report success | godoc, spec |

### Checkpoint 3, F-1 — 2026-08-04: the synthesized event names, 47 → 49

| Change | Source |
|---|---|
| **`SlowClientEvent` and `ClientRecoveredEvent` added.** The library has always synthesized two events into a session's own mailbox when the outbound window fills and drains, and the constants naming them lived in `internal/protocol`, where an application cannot reach them. FR-51 asks for a *defined* degradation and the application half of it is a reducer branching on those names, so every application that wants one had to spell the library's private vocabulary out: `examples/dashboard` did, `docs/guide/events-and-forms.md` documented them as bare strings, and a reducer matching the wrong string sees nothing at all — the `switch` falls through to its default and the branch never runs | dashboard **FRICTION.md F-1**, **FR-51**, FR-62 |
| **This is the argument `EffectFailedEvent` already won, and the same failure.** Before that constant existed `examples/counter` hard-coded `"gotthlive.effect_failed"`, a name nothing emits, and shipped a failure path that had never once executed. An example's spec is a poor substitute for a constant, because the next application will not have the spec | this ledger's Phase 1 effect-boundary entry, F-1 |
| **Each constant is declared as the internal one the actor synthesizes**, rather than as a second literal. That diverges from `EffectFailedEvent`, which duplicates its literal so that godoc does not print a name from a package its reader cannot import; the divergence is deliberate, and the godoc cost is paid back in the doc comment, which quotes both values. What is bought is that drift is unconstructable rather than merely tested for | judgement, recorded here rather than only in a commit message |
| **The spec that holds it is behavioural rather than an equality.** With one source of truth, an equality between two constants agrees with itself by construction and proves nothing about what a reducer receives, so `live_test.go` drives a real session over a real socket into the ladder's second stage — `AckWindow` 2, forty unacknowledged transitions — and asserts the name the reducer was handed is `live.SlowClientEvent`, then acknowledges one patch and asserts `live.ClientRecoveredEvent`. Mutated three ways: either constant retyped to a near-miss literal goes red, and so does widening `AckWindow` so that the window never fills, which is what stops the spec passing vacuously | checklist §8.2 |
| The identifier count moves **47 → 49**. Struct fields unchanged at 50 | **FR-65** |

### Checkpoint 3, D-23 — 2026-08-04: the three `Limits` the mount snapshot carries get their range

| Change | Source |
|---|---|
| **`New` validates `HeartbeatInterval`, `MaxInboundFrameBytes` and `AckWindow`.** They are copied into the mount `Snapshot`'s Liquid Proto-validated session parameters, and were checked nowhere. Outside that boundary, `New` returned no error and every session on that configuration died at establishment with `Error{INTERNAL}` *"the server could not encode an update"*, above a log line saying the frame *"was built by this library, so this is not a client problem"* — a startup mistake presenting as a library bug at runtime, reachable by writing `HeartbeatInterval: 500 * time.Millisecond` | QA-2 **D-23**, protocol.md §3.3, **FR-58** |
| **D-14's mechanism, extended; not a second one.** Same function (`Limits.validate`), same ruling — refuse at construction, never clamp — and the same error shape: the field, the offending value as the operator wrote it, the range in the operator's own units, and the default. One addition, because these three ranges are not this library's to assert: the message quotes the refinement predicate and the `gotthlive.v1.Snapshot` field it is declared on, so *"out of range"* arrives with its evidence | **D-14** precedent, checklist §5.4 |
| **One source of truth, and it is checked against the generator's.** `protoc-gen-liquidproto` emits `ValidateSnapshot` and no constants for predicate endpoints, so the interval is named once in `internal/protocol.SessionParamRange` — with the predicate text — and `internal/protocol/sessionparams_test.go` sets each raw `Snapshot` field and calls `ValidateSnapshot`: accepted at `Min` and `Max`, refused one past either end, with `Field` and `Predicate` compared verbatim on the resulting `*liquidproto.Error`. A predicate that moves in the `.proto` turns those specs red rather than leaving the constants quietly wrong | requirement that the numbers not be retyped |
| **The check is on the wire value, widened.** `HeartbeatInterval` is a `Duration` and the wire carries whole milliseconds, so 500 µs is a legal `Duration` and an illegal `heartbeat_interval_ms`; and the actor's narrowing to `uint32` would otherwise let 4294987296 ms — 49 days — arrive as an ordinary 20 s. `SessionParamRange.Contains` takes an `int64` for that reason, and a spec sets all three fields to 2^32 plus their own default | consequence |
| **The class, not the three instances.** A spec walks `Limits` by reflection, sets every field to values chosen by kind rather than by meaning, and requires that **every configuration `New` accepts mounts a real session whose first frame is the `Snapshot`** — the invariant D-23 violated and nothing held. It covers a field added tomorrow on the day it is added. 31 configurations mounted, 5 refused at construction, on the current struct | checklist §2.2, the same argument as D-14's reflection spec |
| **No exported identifier, no new `Limits` field, no protocol change.** The ranges are documented in the three fields' godoc and in the rows above; `internal/protocol` is where they live, and it is internal. `tools/apisurface` reads **`live 49/49` identifiers and `50/50` fields** across this change, unmoved | **FR-65** |

### Checkpoint 3, F-3 — 2026-08-04: a keyboard binding names its key, 46 → 47 and 49 → 50

| Change | Source |
|---|---|
| **`Bind.Keys []string` added.** `live.On("keydown", …)` had no key filter and the client's `dispatch` matched on `e.type` and nothing else, so `data-gotth-on="keydown:chat.send"` raised an event on **every** key including Tab, Shift and the arrows — a frame per keystroke, and a message sent the first time somebody moved the caret. FR-54 requires bindings to be expressible from templ without hand-written JS; this was a binding not expressible at all, and the benchmark equivalence spec's **F-CTR-6** (`+`/`−` on the focused counter apply `+1`/`−1`) is frozen and needs it | chat **FRICTION.md F-3**, checkpoint-2 gate §8, **FR-54** |
| **It is a component of the binding, not an attribute of the element**, which is not the shape that was proposed. A `data-gotth-keys` attribute is read from the element, and an element carries several bindings: a composer bound `input:chat.draft;keydown:chat.clear` would have had its **input** binding filtered by a key an input event does not carry, and the draft would have stopped being sent with no error anywhere. Per element also cannot say which of two keys raises which event, which is exactly F-CTR-6. Measured, not argued: with the filter moved to the element the F-CTR-6 spec, the key-list spec and the composer spec go red and the Escape spec stays green | measurement, `keybinding_test.go` |
| **One key per binding**, so a key list renders as several bindings — `keydown:c.up:+;keydown:c.down:-`. No separator is reserved for a list, which is what keeps every printable key value available: `,` is the obvious separator and `,` is itself a key. `:` and `;` already separate this grammar and are the only two characters a key value cannot be; said so in `Bind.Keys`' godoc and in `client/SIZE.md` §7 | consequence |
| **Unrecognised key names are not an error and are not normalised.** The comparison is exact and case-sensitive against `KeyboardEvent.key`, so `"Esc"` is a filter that matches nothing. Normalising is impossible rather than merely unhelpful — `"a"` and `"A"` are different keys — and an allowlist of names would refuse valid keys the day a browser adds one, because the set belongs to the UI Events specification and not to this library. A typo shows up as a binding that never fires, on the first keypress | **FR-65**, godoc |
| **Modifier state is not compared, and a key binding never calls `preventDefault`.** A printable key already carries its modifiers (Shift and `=` arrive as `+`), a modifier pressed alone is `"Shift"`/`"Control"`/`"Alt"`/`"Meta"` and matches only a filter naming it, and a chord belongs to the browser. The consequence is stated as a spec rather than a sentence: Enter on a bound textarea raises the event **and** inserts the newline, so F-CHT-3's "Enter sends, Shift+Enter newlines" is **not** expressible with a key filter alone | `keybinding_test.go`, and a finding for PM-1 |
| ⟨**SUPERSEDED 2026-08-05 — the row above is true of a key filter alone and is no longer true of the library, and it is kept for the record.** `Bind.NoModifiers` compares the modifier state where a binding asks it to and `Bind.PreventDefault` takes the key where a binding asks it to; both default off, so the row's own spec — *"does not take the key away from the browser"* — is still green and still **unedited**, because the binding it drives sets neither. **The finding for PM-1 is discharged**: `F-CHT-3` is expressible, decided in both halves at [`reviews/fr-54.md`](reviews/fr-54.md) §12 and §13, and driven through Chromium in `test/internal/conformance/keybinding_modifiers_test.go`. What survives unchanged is the row's *reason*: a key filter alone still cannot express it, which is why this took two more fields rather than none⟩ | **FR-54 failure 1**, `reviews/fr-54.md` §12, condition FR54-6 |
| **A key filter on an event with no key never fires** — `OnWith("click", …, Bind{Keys: …})` fires never rather than always. A filter filters | godoc, spec |
| **`OnAll(bindings ...templ.Attributes) templ.Attributes` added, and it is the second symbol this item needs.** The client has matched several bindings per element since checkpoint 1 and **nothing could emit them**: templ renders each spread separately, two spreads of `On` produce `data-gotth-on` twice, and an HTML parser keeps the first and discards the second. So F-3's own example — a composer already bound for `input`, wanting Escape as well — and F-CTR-6's two keys on one focused element were both inexpressible whatever the filter's shape. Without it `Bind.Keys` closes F-3 on paper only | **FR-54**, F-3, F-CTR-6 |
| **`OnAll`'s merge rule is first-wins for everything except the binding list**, because `Fields`, `Debounce` and `Throttle` are attributes of the ELEMENT in the vocabulary the client reads and cannot be per binding without a second timer table in the runtime. First-wins is what the HTML parser already did with the duplicate attribute this function replaces, so moving a page from two spreads to one `OnAll` cannot silently change which debounce is in force. **The consequence is a wart and it is documented in the godoc:** every binding on the element shares one debounce timer | consequence, `templ.go` |
| ⟨**SUPERSEDED 2026-08-05 — the row above is wrong in two places and is kept for the record.** (1) *"cannot be per binding without a second timer table in the runtime"* is **false, and measured**: there is no second table, the entry that already existed grew a key, and the whole landing is **−85 B minified and −8 B gzipped** (`client/SIZE.md` §1.1.5). (2) *"the consequence is a wart and it is documented in the godoc"* — the **godoc never called it a wart**; the word is this row's own, and PM-1's `phase-4.md` §5.6 quoted it back as the godoc's. QA-1 caught that while driving the reproduction. What the consequence actually was: on the guide's own composer a keystroke inside the window **destroyed** the other binding's event, in either direction, with nothing on the wire. Per-binding scoping landed at the section at the top of this changelog⟩ | **FR-54 failure 2**, QA-1 `docs/qa/fr-54-debounce-repro.md` |
| **Client cost: +13 gzipped bytes** on a 12,288 B ceiling, 4,124 → 4,137, headroom 8,151 B (66.3 %). One comparison inside a `split` the dispatch path was already performing: no new attribute, no second `getAttribute`, no list parsing and no allocation per keystroke | **NFR-2/NFR-3**, `client/SIZE.md` §1.1.1 |
| The identifier count moves **46 → 47** (`OnAll`) and the field count **49 → 50** (`Bind.Keys`). `live/livetest` untouched at 9/6 | **FR-65** |

### Checkpoint 2, C-31 — 2026-08-04: `Event.Contributing` is bounded, and the flush trigger counts the frame

| Change | Source |
|---|---|
| **The `Emitter` rejects an over-long `Event.Contributing`**, as a fourth entry beside the three server-minted fields it already refuses. Measured before, on default limits: 1,200 identifiers gave `patches=0 errors=1`, a non-fatal `Error{INTERNAL}`, a state change the client never saw, `gotthlive_outbound_validation_failed_total = 1`, and `emit` returning **`nil`** — the application told nothing about its own mistake. 1,024 *passed*, but only because `unionEdges` deduped the library's `scheduledBy` edge against one the application happened to list, so the observable bound was a function of accidental overlap between two sets neither party could see | L9-1 **C-31**(a), QA-1 **D-18** |
| **The bound is 64, and it is derived twice over.** H-4 bounds every other repeated field in the schema at 64 — `Event.fields`, `Patch.updates`, `Snapshot.updates`; `Origin.contributing_event_ids` is 1024 because it is an accumulator the library fills by coalescing, not because one event may name a thousand causes. And it has to be small independently, because every identifier an application may add to one event is one the library may not coalesce: it is subtracted from the flush headroom | **H-4**, `internal/session.MaxEventContributing` |
| **The flush trigger is evaluated against the union the frame will carry**, not against `len(pendingIDs)`. `deferPatch` folds an application's identifiers into the deferred set, but the origin about to be emitted carries its own, which `unionEdges` merges in and the old count never saw — so a legal per-event `Contributing` plus a legal `CoalesceFlushAt` could still build a frame the schema refuses. The sum of the parts settles the ordinary case without allocating; the exact set is built only once the sum has reached the trigger | **C-31**(b) |
| **`CoalesceFlushAt`'s range narrows from 1–1023 to 1–959**, and this is D-14's arithmetic with the term D-14 could not have. `MaxCoalesceFlushAt` was `ceiling - 1`, where the 1 is the deferred transition's own event identifier; it is now `ceiling - 1 - MaxEventContributing`, because one more emission can add that identifier *and* the application's list. The old constant was the case where an application contributes nothing, which was the only case there was. Refused at `New` rather than clamped, per D-14's ruling; the default 512 is unaffected, and a spec asserts `MaxCoalesceFlushAt + 1 + MaxEventContributing == CoalesceFlushCeiling` so neither constant can move alone | **C-31**(b), D-14 precedent |
| **Four sites stop calling an application's mistake a library bug.** `gotthlive_outbound_validation_failed_total`'s registration and godoc, `session.send`'s log line, `protocol.Framer.OnInvalid` and `InvalidFrameError.Error`. All four now say what is true and useful — the frame was built on this side, so it is never the client's doing — and stop asserting what was false, that any occurrence is a coding defect in this library. An alert that names the wrong repository is worse than the dropped patch, because it sends the person holding the pager to the wrong place. `internal/protocol/limits.go` additionally stops describing the bound in terms of *"the actor emits at half this ceiling"*, which has been the default and not the bound since D-14 | **C-31**(c) |
| **No exported identifier, no `Limits` field, no truncation, no protocol change.** The bound is documented in `live.Event.Contributing`'s godoc and derived in `internal/session`, exactly as D-14's was. Making it configurable would let an operator configure their way back into D-18; truncating to fit would reintroduce the loss the flush trigger exists to prevent. **46 stays 46, 49 fields stay 49** | **FR-65**, §4.3 of the checkpoint-2 round |

### Checkpoint 2, C-32 — 2026-08-04: `IsRetryable` is re-added, 45 → 46

| Change | Source |
|---|---|
| **`live.IsRetryable(error) bool` added.** The checkpoint-2 batch cut it (§7) for having no call site and pre-registered the re-add trigger: *"something needing to inspect an error it did not itself produce"*. `examples/chat` hit it — its subscription pump returns a transient error and a terminal one and the difference decides whether a session repairs itself — and L9-1 ruled the re-add in the checkpoint-2 round §5 | **C-32**, FR-16, chat `FRICTION.md` F-4 |
| **The cut cost a spec that could not fail, and it was measured.** With `live.Retryable` replaced by a plain `fmt.Errorf("%w")` — the mark **gone** — `examples/chat`'s suite stays **green**, including the spec whose failure message reads *"the pump must have wrapped it with live.Retryable"*. The workaround the cut forced, `errors.Unwrap(err) != nil`, tests wrapping and not classification, and the mutation is behaviour-changing: `chat.go:511` re-subscribes only when the classification is `true` | L9-1's check 16 |
| **The predicate, not `livetest.AssertRetryable`.** `Expect(live.IsRetryable(err)).To(BeTrue())` composes with `Eventually`, `ContainElement`, a `DescribeTable` entry and a plain `if`; an assertion helper works in one place and imports a second package to do it. C-25's `testing.TB` guard does not transfer — `NewSession` went to `livetest` because a `Session` constructor in `live` is a forgery route, and a pure predicate over an error the caller already holds has nothing to guard | **C-32** §5.2 |
| **One implementation, not two.** `internal/session.retryable` is exported as `internal/session.IsRetryable` and `live.IsRetryable` calls it. That is the function the actor uses to fill `EffectFailedRetryableField`, so the predicate an application calls and the classification the library records cannot drift; a spec in `live` drives a real failing effect and asserts they agree | consequence |
| The identifier count moves by hand: **45 → 46**. Struct fields unchanged at **49**. `live/livetest` untouched at 9/6 | **FR-65** |

### Checkpoint 2, D-14 — 2026-08-04: `Limits` has a validated range

| Change | Source |
|---|---|
| **`New` validates `Limits`.** It inspected no `Limits` field at all, which is how QA-1's D-14 happened: `CoalesceFlushAt` is `stable` and documented as the reason provenance is never truncated, and set above H-4's ceiling it became the truncation. Measured at 4000 with 1,400 unacknowledged transitions — 1,385 swallowed, 8 identifiers on the wire, 1,377 on no frame at all, and the resync answered with a **non-fatal** `Error{INTERNAL}` so a wire consumer sees a blip rather than loss | QA-1 **D-14**, protocol **H-4**, **FR-43** |
| **The bound is 1023, and it was measured rather than chosen.** The trigger counts deferred transitions; the frame it forces carries one identifier more, because the origin of the transition being emitted at the time is folded in. Over the repro: at 512 the widest union is 513 and all 3,978 swallowed transitions are carried; at 1023 it is 1024 and all 3,982 are carried; at **1024** it is 1,025, the outbound validator refuses the frame, and 907 of 3,982 survive | measurement, recorded at `internal/session.MaxCoalesceFlushAt` |
| **Refused, not clamped.** A clamp keeps the process up and makes the running limit differ from the configured one, so an operator reading their own config reads a number that is not in force. This project has already ruled that way once, on `normalizeMount` — *"silently rewriting more of it would make this function a second, quieter router"* — and checklist §5.4's posture is default-deny | **C-23** precedent, checklist **§5.4** |
| **No new exported identifier.** The bound is named in `CoalesceFlushAt`'s godoc and in the row above rather than exported as a constant. An application that wants the largest legal value is exactly the application that should be reading the paragraph; `internal/session.MaxCoalesceFlushAt` is where the arithmetic lives, and it is internal. **45 stays 45, 49 fields stay 49** | **FR-65** |
| **The whole struct is validated for negatives, not just one field.** Zero already means "take the default", so a negative is meaningless in every field here, and two of them — `MailboxDepth` and `AckChannelDepth` — are channel capacities, where a negative is `panic: makechan: size out of range` at the first connection rather than an error at all. No *upper* bounds were invented for the others: `CoalesceFlushAt` is the only field with a protocol ceiling behind it, and a library that capped `MailboxDepth` would be deciding an operator's capacity for them | QA-1's note that every `Limits` field is unvalidated on the same path |
| Completeness is held by a spec, not by review: `live`'s suite walks `Limits` by reflection, sets each field negative in turn, and requires `New` to name that field — so a field added without a decision about its range fails there. Same mechanism as `protocol`'s H-4 list-bounds table, for the same reason | checklist §2.2 |

### Checkpoint 2, C-27 — 2026-08-04: `Script` accepts a path and only a path

| Change | Source |
|---|---|
| **`mountPath` is restated as a path-only, same-origin reference**, and `normalizeMount` refuses `//` anywhere, `\`, `?`, `#`, and any byte below `0x20` or `0x7F`. C-23's *"empty or does not begin with `/`"* was L9-1's spelling of §4.3(1)'s own sentence — *"the prefix the handler is reachable at as the **browser** sees it"* — and the spelling was wrong, because it was written against RFC 3986 while browsers parse URLs with WHATWG. Measured in Chromium over two loopback origins: `//host`, `///host` and `/\host` all rendered a working tag that fetched the runtime from the other origin **and opened the `gotth-live.v1` WebSocket there**. `net/url` called the second and third same-origin | **L9-1 addendum**, **C-27** §A.6.1 |
| **The reachable input is a mistake, not an attack**: `"/" + prefix + "/live"` with an empty `prefix` yields `//live`, and the browser resolves the runtime and the session host to `live`. That is the silent blank page C-23 exists to abolish, reached by a concatenation | **C-27** §A.2 |
| **A positive rule, not a blocklist.** Each clause names the browser behaviour it prevents, so the next reader can re-derive it rather than pattern-match a list of bad prefixes against a parser this project does not own. Percent-encoding, `..` segments and spaces stay accepted, said so in the code: `%2f` is not a bypass and the rest is the caller's routing decision | **C-27** §A.4(2), §A.6.1 |
| **`Script` writes HTML-escaped attribute values** via `templ.EscapeString` instead of `fmt`'s `%q`, which is Go quoting. This is the module's one hand-rolled markup writer — `Region`, `On`, `OnWith` and `Preserve` return `templ.Attributes` and templ escapes them — and it was the one that did not escape, against checklist §5.8 and FR-50. Not belt-and-braces: `/reports&sect;ion/live` is legal under every clause and the browser silently fetched `/reports§ion/live` | **C-27** §A.6.2 |
| Both attributes are now derived from one normalisation. `Script("/live//")` used to render `src="/live/…"` beside `data-gotth-url="/live/"`, because `src` was trimmed a second time and the URL was not | **C-27** §A.6.2 |
| **Not a security fix, and recorded as such.** CP1-13's strict CSP defeats every case — measured, `script-src 'self'` means the other origin receives no request at all — and a mount path chosen by an attacker is out of gotth-live's threat model. But FR-49 only requires the runtime to *function under* that policy; nothing requires a consumer to *send* one and the library does not emit it, so this is a correctness defect with a security tail | **C-27** §A.3, §A.5 |
| The identifier count does not move: **45 stays 45**, 49 fields stay 49. Everything changed is unexported or a doc comment. The four mount paths in the existing specs — `/live`, `/app/live`, `/`, `/ui` — render byte-identically under the new writer, so no expected-byte assertion moved and FR-7 is untouched. BL-30 covers the behaviour change: `Script` is experimental and v0.1 makes no compatibility commitment, and `examples/counter` mounts at `MountPath = "/live"`, which every clause accepts | consequence, FR-65 |

### Checkpoint 2, C-25 — 2026-08-04: `livetest.NewSession`

| Change | Source |
|---|---|
| **`livetest.NewSession(testing.TB, live.ID, live.Identity) live.Session` added; `live` gains nothing.** The premise, as L9-1 sharpened it by measuring rather than reading: `live.Session{}` *does* compile outside `live` — an empty composite literal names no field — but its `ID()` is all-zero and its `Identity()` is **nil**, and identity is the reason `Init`, `Authorize`, `Teardown` and `Execute` take a `Session` at all. An application can construct a useless `Session` and cannot construct a useful one, so a hook that takes one is testable only through a running server | **L9-1 ruling 5**, **C-25**, **FR-15** |
| The cost was already visible and already paid once: `examples/counter/store.go` carried an `Execute`/`execute` split whose doc comment was a defect report. Both are gone in this change — a comment documenting a missing library feature must not outlive the feature, because that is how a fixed defect gets re-reported | **C-25** §6.4 |
| **The mechanism**: a var in `gotth-live/internal/livebridge` that `live` assigns at `init` and `livetest` reads. Exporting `live.NewSession` would have put an identity constructor in the production package, reachable from any handler. Three cumulative guards — `testing.TB` first, the bridge is `internal/` to this module, and importing `livetest` already links `testing` — and `internal/arch` now asserts the bridge's importers are exactly `live` and `live/livetest`, because the safety argument is a claim about the import graph and an unverified claim is how one stops being true | **C-25** §6.3 |
| **One symbol, not two.** No `NewSessionWithID`, no options struct, no anonymous convenience overload. Both values are the caller's: deriving the identifier from the identity is wrong, because `Limits.MaxSessionsPerIdentity` exists precisely because one subject holds many sessions, and checkpoint 2's chat example — two tabs, one user — is the first test that needs two identifiers for one identity | **C-25** §6.2 |
| `live/livetest`'s identifier ceiling goes **8 → 9**; measured goes 2 → 3, so the tool passes either way, which is exactly why the row is written by hand and reviewed rather than left to CI. `live` is unchanged at 45 and 49. Landed after C-21, per the ruling | consequence |

### Checkpoint 2, C-23 — 2026-08-04: `Script` takes the mount path

| Change | Source |
|---|---|
| **`Script()` becomes `Script(mountPath string)`.** L9-1 measured the defect rather than reading it: mounted at `/app/`, `GET /app/gotth-live.min.js` returns 200 and the rendered tag points at `/live/gotth-live.min.js`, which 404s — with no server-side error on either side. The page loads, the script does not, and nothing is live. The mount path is knowledge only the caller has, and no runtime check inside the library can observe the mismatch: the router strips the prefix before the handler sees a request, and the tag renders on the page request | **L9-1 ruling 3**, **C-23**, FR-33, NFR-5 |
| **Zero net exported identifiers**, which is what settled the shape. A second `ScriptAt` would have left the broken default reachable; a `Config.Mount` field plus an `App.Script()` method would have added a field and a method, relocated the same 404, stored a string the library never routes with, and split the templ helper vocabulary across package funcs and a method on a generic type. After the change there is no way to render the tag without naming the mount | FR-65 |
| **A render error for an empty or non-absolute mount**, which is where the server-side error this design had nowhere to put finally lands: on the page request, where a handler already has an error path. A 500 beats a blank page | **C-23** §4.3(3) |
| `defaultMountPath` is deleted, and with it `Script`'s paragraph telling the reader to hand-write their own tag — a workaround must not outlive its defect. `App.Handler()`'s doc drops "beyond the one Script documents": the handler now holds no routing assumption at all, which is FR-33 stated without a caveat | **C-23** §4.3(4) |
| The identifier count does not move: 45 stays 45, and no struct field changed. Only a signature | consequence |

### Phase 1, the effect boundary — 2026-08-04: the failure contract, reachable and classified

| Change | Source |
|---|---|
| **`EffectFailedEvent` added.** A failed or panicking effect has always become a synthetic event, but the constant naming it lives under `internal/` where an application cannot reach it, so a reducer had to hard-code the string. `examples/counter` hard-coded `"gotthlive.effect_failed"` — a name nothing emits — and its spec passed because the reducer's default branch does nothing, so the example shipped a failure-handling path that had never once run. An exported name is the fix; `live`'s own suite asserts it equals the internal one, because two spellings of one string is a defect waiting for its first refactor | **FR-16**, FR-58, QA-1's D-13 sibling class |
| **`EffectFailedSourceField`, `EffectFailedErrorField`, `EffectFailedRetryableField` added.** The event's payload is as unreachable as its name was. The counter reads source and classification to decide whether a dead subscription is worth re-establishing; the library's own end-to-end spec reads all three | **FR-16** |
| **`Retryable(error) error` added.** A reducer could not tell a transient failure from a permanent one, so it could not decide whether to schedule a retry — and guessing is not available to it, because whether an effect is safe to run twice is a property of the effect. `Retryable` is how the executor says so, and `examples/counter`'s subscription pump is the call site: it bounds its internal retry and hands the decision up rather than hiding a stuck subscription behind an infinite loop | **FR-16**, RFC §3.6 |
| **The unclassified default is terminal**, argued in the doc comment and asserted in `internal/session`. An effect may have committed externally before it failed, so a blind retry risks duplicating data somebody else owns, where a failure never retried costs a change that visibly does not happen. Retry is an explicit claim by the code that knows | checklist §5.4's default-deny reading, applied to failure |
| No `IsRetryable`: §7 records why. Baseline restated: `live` moves from **40** to **45** identifiers and from **89** to **94** including fields | FR-65 |

### Phase 1, the effect boundary — 2026-08-04: `Config.Execute` takes a `Session`

| Change | Source |
|---|---|
| **`Config.Execute` gains a `Session` parameter**, becoming `func(context.Context, Session, Effect, Emitter) error`. `Init`, `Authorize` and `Teardown` all took one; `Execute` was the odd one out, and the gap is not cosmetic. Per-event authorization has an identity at `Authorize` and it is gone by the time the effect that event scheduled actually runs, so an effect that publishes on the session's behalf could not name its publisher. DEV-3 hit it writing the examples: checkpoint 2's chat example cannot be written without it, because a message published to a topic has an author | **FR-45**–**FR-48**, FR-42, FR-61 |
| An explicit parameter rather than a context value. A context value makes the identity optional at the type level and absent by mistake at runtime; a parameter makes an executor that forgot to ask impossible to write. The named call site that forced it, today: `examples/counter`'s `WatchEffect` **loses its `Session` field** and becomes an empty value, because it existed only to smuggle the session identifier past a signature that dropped it | FR-65 — an effect value should carry what the reducer decided, not addressing the library already has |
| No exported identifier was added, removed or renamed, and no struct field was added or dropped: the whole delta is one field's type. §0's counts table is unchanged | consequence |

### Phase 1, counter example — 2026-08-04: `NewFields`

| Change | Source |
|---|---|
| **`NewFields(map[string]string) Fields` added.** Found by building the first real consumer. Two already-shipped symbols did not work without it. `Emitter` takes an `Event`, `Event.Fields` is a `Fields`, and `Fields` had no constructor — so an effect could inject an event's *name* and nothing else, which makes every pubsub push data-free and `Emitter` an export with no working call site (FR-65's own rejection criterion, applied to a symbol already on this list). And `livetest.ReplayN` takes a `[]live.Event` the test must build, so FR-15's **mandatory** determinism helper was usable only by applications whose events carry no form values — the counter's cross-tab sync is neither | **FR-42**, **FR-15**, FR-65 |
| The alternative was tried first and rejected on correctness, not taste. A data-free push can still converge if subscribers replay *operations* rather than absolute values, using one registered event name per operation. It is wrong under backpressure: `Emitter` drops into a full mailbox and returns an error, and a dropped operation leaves that session permanently diverged, where a dropped absolute-value sync is repaired by the next one. Shipping the self-healing shape needs a payload | RFC §7.1's cumulative-ack reasoning, applied to application data |
| Ordering is part of the contract: the map is copied into a key-sorted slice, because `Fields` is compared by value inside `ReplayN` and an unordered copy would fail a determinism check that had found nothing wrong | checklist §2.2 |
| Baseline restated: **49** excluding struct fields, **102** including them | consequence |

No existing identifier was removed or renamed, and no struct field changed.

### Phase 1, module init — 2026-08-04: the two rulings recorded, C-13's ledger half closed

| Change | Source |
|---|---|
| §0.1 rewritten from "one deviation, declared" to the ruling it received: **two exported packages, capped at two**, with C-12's three conditions and where each is discharged. The cap is stated here as well as in RFC §14.2 so a third package is refused by whichever document the author happens to open | **L9-1 ruling A1**, condition **C-12** |
| §7.1 rewritten from "one requirement this surface does not fully meet" to the reconciliation it received. **PM-1 amended FR-56 in PRD v0.3** to mount/event/teardown with patch observability delegated to instrumentation, so the requirement and the surface now agree rather than disagreeing behind a footnote. The Phase 2 revisit condition — a real consumer, named in the PR — is carried | **L9-1 ruling A2**, condition **C-13** (DEV-1 half) |
| §9 A1 and A2 closed and struck rather than carried | above |
| A5's parenthetical corrected from `go 1.24` to the settled **`go 1.25`** floor, which `gotth-live/go.mod` now declares as `go 1.25.0`. `iter` was available either way, so the question is purely a taste call now | condition **C-10** |

No exported identifier was added, removed, or renamed, so the §0 baseline of
**48** excluding struct fields and **100** including them is unchanged.

### Phase 1, checkpoint-1 remediation — 2026-08-04: `Event.Contributing`

| Change | Source |
|---|---|
| **`Event.Contributing []uint64` added**, and `Event.ID`'s row restated. QA-1's D-1 found that the patch a click produces names the effect that emitted it and not the click. Half the fix is internal — the library now carries the identifier of the event that scheduled an effect through to the patches that effect's emissions produce. The other half cannot be: when an effect delivers a value that arrived through shared state, the event that *scheduled* the subscription (the mount) is not the event that *produced* the value (the click), and only the application knows the second. `Contributing` is where it says so. It is a contributing claim and never a causal one — the patch's cause stays the server-minted origin — and identifiers are session-scoped, so it cannot name another session's events | **FR-42**, protocol.md **§4.2**, QA-1 **D-1** |
| `Event.ID` and `Event.At` are **validated on the emit path rather than silently discarded**. QA-1's D-8 called the old behaviour the obvious wrong fix for D-1, and it was: a reader who found a settable `ID` would assume setting it worked. A non-zero `ID`, a non-zero `At`, or a zero entry in `Contributing` on an emitted event is now an error, which reaches the reducer as a deterministic effect failure | QA-1 **D-8** |
| Baseline restated: **49** excluding struct fields, **103** including them | consequence |

### Phase 1, checkpoint 1 — 2026-08-04: `Config.Events`

| Change | Source |
|---|---|
| **`Config.Events []string` added.** The requirement it satisfies was already binding and had no field to live in: RFC §11.3 makes an unregistered event name **default-deny** — refused with `UNKNOWN_EVENT` and counted, never dispatched and never ignored — and instrumentation §2.1 bounds the `event` metric label "by registration", fixed before the first connection. Neither is implementable without the application declaring the set, and this ledger shipped no field that let it. The alternative readings were worse: deriving the set from the `On` helpers is impossible at runtime, and treating an unknown name as a no-op is default-ignore, which checklist §5.4 refuses by name | **FR-47**, checklist **§5.4**, instrumentation **§2.1** |
| Baseline restated: **48** excluding struct fields, **101** including them | consequence |

No exported identifier was added, removed or renamed. The delta is one struct
field, and it is recorded here in the same commit that adds it rather than
discovered by the count check afterwards.

### Cycle 2 — 2026-08-04

Not reviewed in L9-1's cycle 1 (written after it landed), but updated for the
decisions and blockers that cycle produced.

| Change | Source |
|---|---|
| `Limits` gains `AckChannelDepth`, `MinResyncInterval`, `ResyncBurst`, `CoalesceFlushAt`; `MailboxDepth` re-documented as a memory parameter | RFC blockers **B-2** (bounded ack channel), **B-4** (coalescing flush trigger), **B-5** (resync rate limit), **B-1** (eager channel allocation) |
| `Config.Metrics`/`Tracer` promoted from experimental to stable, typed against the OTel **API** modules | **L9-1 D1** — Option A settled. §1.1 records the submodule form and the 8-module fallback trigger |
| `Config.Logger` marked stable, with the consequence of leaving it nil stated | **L9-1 D2** — `log/slog` settled; instrumentation §4A.3 |
| Cut-list row for the `Transport` interface updated to "amendment accepted" | **L9-1 D4** — PRD v0.2 amended FR-2; RFC O1 struck |
| Identifier baseline restated: **48** excluding struct fields, **100** including | consequence of the above |

**Still queued for L9-1's cycle 2, unchanged:** the two rulings requested in
§0.1 (two exported packages, on the `net/http/httptest` precedent) and §7.1 (no
FR-56 patch hook). Neither was raised in cycle 1 because this document post-dates
it.
