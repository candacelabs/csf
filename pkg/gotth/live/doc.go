// Package live serves server-driven live user interfaces from Go.
//
// A live application keeps its state and its rendering in the Go process. The
// browser holds one WebSocket per tab; interactions travel up as events and
// re-rendered HTML fragments travel back down, where a morph applies them to
// the DOM in place. No application state is mirrored in the browser and none
// is serialized: what the user sees is a projection of state the server owns.
//
// # The shape of an application
//
// An application is declared as a [Config], validated by New, and mounted as
// an ordinary http.Handler. The three parts a caller supplies are a mount hook
// that produces the initial state, a pure reducer that advances it, and a set
// of fragments that render it.
//
// Purity is not a style preference. Because the reducer is a pure function of
// (state, event) and rendering is a pure function of state, an event log
// replays to a byte-identical result, a render that cannot be sent is skipped
// rather than queued, and a panic leaves the pre-transition state intact and
// correct. Package live/livetest ships the harness that holds callers to it.
//
// # Concurrency
//
// One goroutine owns each session's state and it is the only writer. That
// goroutine has three typed inputs — a bounded mailbox, a bounded
// acknowledgement channel, and a heartbeat ticker — and only the mailbox can
// reach a reducer. Application code therefore never needs a mutex to protect
// session state, and no session's state is reachable from another session's
// goroutine. An [App] is safe for concurrent use; a state value handed to a
// reducer is not, and must not be retained or mutated after the reducer
// returns.
//
// # Delivery semantics
//
// Events are at-most-once: an interaction that was in flight when a connection
// dropped is not retried, and the user sees server truth after the reconnect
// resynchronises. Patches are exactly-once and in order, or the client detects
// the gap and the server answers with a full snapshot. One consequence is
// worth stating plainly: an effect may have executed even though the user
// never saw its result. Applications that need more than that put the
// idempotency key in their own domain.
//
// A session lives exactly as long as its connection. There are no resumable
// sessions and no grace window; a reconnect mounts a fresh session and
// receives a fresh snapshot, which is the same path a deploy or a restart
// takes.
//
// A failed or panicking effect is delivered to the reducer as an ordinary
// event named [EffectFailedEvent], rather than being logged and dropped. One
// thing about it needs saying here rather than being discovered: the
// [EffectFailedErrorField] field carries the error's own message, or the panic
// value, verbatim — in production, unredacted, and ungated by [Config.Dev].
// That is deliberate, because a reducer is server code and an operator-facing
// detail is what makes the failure actionable. It also means the string may
// hold anything an upstream library chose to put in an error: a connection
// string, a query, an internal hostname, a stack-shaped panic value.
//
// The consequence is that rendering that field into a fragment publishes it to
// the browser. Error frames are held to a stricter rule for exactly this
// reason and carry a fixed generic message in production; the failure event is
// a second path to the same disclosure and carries no such discipline, because
// only the application knows what its own effects put in their errors.
// [EffectFailedSourceField] is the value that is safe to render: it is a name
// the application itself chose, from [Effect.Source].
//
// # Error boundaries
//
// A panic in a reducer, in a fragment's render, or in a fragment's Dirty
// declaration is recovered, contained to the session it happened in, logged at
// error level with the causal identifiers and the stack, counted against that
// session's panic budget, and answered on the wire with an Error frame naming
// the event that caused it — or naming nothing, when the server started the
// transition itself. A reducer panic leaves the pre-transition state intact
// and emits no patch; a render panic leaves one region stale and lets every
// other fragment in the same transition patch normally. Neither closes the
// session on its own. A site that panics [Limits.PanicBudget] times in one
// session does close it, and no other session is affected either way.
//
// One Error frame is emitted per render pass, not per broken fragment: the
// message a client receives in production is a fixed string, so repeating it
// once per fragment would add no information, and the per-fragment record is
// the log line and the panic metric, which are still one apiece.
//
// A panicking effect is the exception, by design: it becomes an
// [EffectFailedEvent] rather than an Error frame, because a failure the
// reducer can see is replayable and one that only reaches the wire is not.
//
// [Config.Dev] is what decides how much of a panic reaches the browser, and it
// is the only thing that field does. Everything else about the boundary is the
// same in both modes.
//
// # Status
//
// The server core is implemented: connection lifecycle, the session actor,
// the reducer and render contracts, event dispatch with per-event
// authorization, the acknowledged window and its backpressure stages, resync,
// and the instrumentation catalogue.
//
// So is everything this section used to list as absent. Three examples ship —
// counter, chat and dashboard — and each is built, vetted and race-tested in
// CI. Forms go through the same helpers as any other event, which is what FR-55
// means by first-class and is why there is no form type to look for: [On],
// [OnWith] with [Bind], [OnAll] and [Preserve] are the whole vocabulary, and
// validation feedback is reducer output the application renders. The error
// boundary is the section above, and it is behaviour rather than a component an
// application declares: a panic is contained to its session, told to the client
// as an Error frame or an [EffectFailedEvent] depending on where it happened,
// and counted. No error-boundary component type is planned; if one ever is, it
// will arrive as a requirement before it arrives here.
//
// What is not here is the Phase 5 work: the published bench report and the
// comparison it is measured against.
package live
