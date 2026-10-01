package live

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/candacelabs/csf/pkg/gotth/internal/protocol"
	"github.com/candacelabs/csf/pkg/gotth/internal/session"
)

// Config declares one live application: its state type, its mount hook, its
// reducer, its fragments, its effect executor, and its security hooks.
//
// The zero value is invalid, and New reports exactly which field is missing
// and what to set it to. It is a struct rather than a set of functional
// options because that is the standard library's shape for this — http.Server,
// tls.Config, net.Dialer — and because it makes the security configuration one
// object a reviewer can read at a glance.
//
// # What S has to be
//
// S is unconstrained at the type level and there is one rule about it that the
// compiler cannot state: Reduce must RETURN the next state rather than modify
// the one it was given. A value type gets this for free. A reference type —
// S = *Foo, a map, a slice — does not, and the failure is quiet in both
// directions:
//
//   - state_version rises exactly when state changed, and the library decides
//     that by comparing prev with next. For a reference S it cannot: == would
//     ask whether the two are the same object, and a reducer that mutated in
//     place and returned the same handle would answer yes. The library
//     therefore treats every transition of a reference S as a change, which
//     costs a render that may be suppressed and keeps the version honest.
//   - Fragment.Dirty is handed prev and next, and if the reducer mutated in
//     place they are the same value. The declaration then compares something
//     against itself, reports no change, and that region is never re-rendered.
//     Nothing can repair this from inside the library.
//
// So a pointer S is allowed and is not refused at construction — used purely it
// is perfectly correct — but it is the shape in which forgetting the rule is
// silent. The determinism helpers in live/livetest are what catch a reducer
// that has forgotten it: replay the same event log twice and the results
// diverge.
type Config[S any, I IIdentity] struct {
	// Init is the mount hook: it produces the session's initial state and any
	// startup effects, such as a pubsub subscription. It runs once per session,
	// as the first transition, before the first snapshot.
	//
	// Optional. Nil means the zero value of S, no startup effects and no error
	// — which is the only total, side-effect-free thing an unwritten mount hook
	// could mean, and it is what an application whose sessions all start empty
	// writes out by hand. Teardown, the hook on the other end of the same
	// session, has always been optional on the same argument.
	//
	// It is the ONE field New fills in rather than refusing, and the line
	// between it and the rest is that the rest cannot be guessed: a reducer, a
	// region, the set of accepted event names and the four security hooks are
	// each something only the application knows, and a library that picked for
	// them would be picking deny-by-default's opposite. Getting this one wrong
	// is also visible on the first run rather than in production — sessions
	// start empty, and so does the page, because [App.PageHandler] renders from
	// this same hook — where a guessed origin allowlist or a guessed
	// authorization rule would not be visible at all.
	//
	// [App.PageHandler] calls it once per page request as well, to render the
	// first paint from the state a session would start at. Init is therefore a
	// loader: it must be safe to call for a read. The effects it returns are
	// performed only for a real session; on a page render they are discarded.
	// See that method.
	Init func(ctx context.Context, session Session[I]) (S, []Effect[I], error)

	// Reduce is the pure state transition. Required.
	Reduce Reducer[S, I]

	// Fragments are the server-owned live regions. Required, non-empty, and
	// every ID must be unique.
	Fragments []Fragment[S]

	// Events are the event names this application accepts. Required.
	//
	// An event whose name is not here is refused with UNKNOWN_EVENT and
	// counted, never dispatched and never ignored: unknown input is
	// default-deny. Declaring the set up front is also what bounds the
	// cardinality of the per-event metric label before the first connection.
	Events []string

	// There is no Execute hook. It was here until 2026-09-03, taking one
	// effect interface and type-switching on its dynamic type to decide what to
	// do, and it went with that interface: [Effect.Run] is where an effect's
	// behaviour lives now, so there is nothing left for a central executor to
	// dispatch on. An application that had one moves each `case` arm into the
	// constructor that builds the effect, which is also where the store, broker
	// or pool it needs is already in scope.
	//
	// Everything that hook's signature carried, Run's carries: the session it
	// acts for, the [Emitter] that injects results back, and the error that
	// reaches the reducer as an [EffectFailedEvent] whose
	// EffectFailedErrorField holds its message verbatim, in production and
	// unredacted.

	// Teardown runs after the session actor exits, with the final state, for
	// unsubscribing. Optional.
	Teardown func(ctx context.Context, session Session[I], state S)

	// Origins is the allowlist of permitted Origin values, checked on the
	// upgrade request before any per-session memory is allocated. Required
	// unless it contains AnyOrigin. Deny by default: there is no wildcard, no
	// reflection of the request's own Origin, and no pass for a request that
	// sends none.
	Origins []string

	// Authenticate derives the session identity from the upgrade request.
	// Required; use Anonymous to opt out.
	Authenticate func(request *http.Request) (I, error)

	// Authorize runs before the reducer for every event, at the single
	// mailbox ingress, so a new event kind cannot skip it. Required; use
	// AllowAll to opt out.
	//
	// Returning nil allows the event. Returning a *DenyError rejects it
	// without closing the connection. Returning a *FatalDenyError rejects it
	// and closes the connection.
	Authorize func(ctx context.Context, session Session[I], event Event) error

	// CSRF validates a token bound to the authenticated application session.
	// Required; use NoCSRFCheck to opt out.
	CSRF func(request *http.Request) error

	// Limits are the resource bounds. Any zero field takes its documented
	// default.
	Limits Limits

	// Logger is the structured log sink. Nil disables library logging and the
	// provenance log with it, which makes the reverse lookup from a captured
	// patch back to its cause unavailable. The frames still carry the causal
	// chain either way; what is lost is the server-side index.
	Logger *slog.Logger

	// Metrics enables the full metric set with one field. Nil disables it, at
	// a cost of one predictable branch per call site.
	Metrics metric.MeterProvider

	// Tracer enables the full trace set with one field. The provider is taken
	// explicitly rather than read from the OpenTelemetry global, which is what
	// lets this library depend on the trace API submodule rather than the root.
	Tracer trace.TracerProvider

	// Dev turns on developer mode. It must be false in production.
	//
	// It does three things, and nothing else.
	//
	// # 1. Panic detail in the Error frame
	//
	// In
	// production such a frame carries a fixed generic message and the causal
	// identifiers, and nothing else; with Dev set, the same frame also carries
	// the panic value and its stack. The full stack is written to Logger at
	// error level in both modes — dev mode only puts it where a developer with
	// a browser open will see it (FR-23, checklist §5.9).
	//
	// It reaches both sites that produce an Error frame: a panicking reducer,
	// and a panicking fragment render or Dirty declaration. The third site
	// FR-23 names, a panicking effect, deliberately becomes an
	// EffectFailedEvent instead of a frame, and EffectFailedErrorField already
	// carries the panic value in production and in dev alike — see that
	// constant, because it is a disclosure path this field does not gate.
	//
	// What reaches the browser is bounded: protocol.md caps an error frame's
	// message at 512 bytes, so a long stack arrives truncated. The frame is a
	// pointer into the log, not a copy of it.
	//
	// # 2. The dev session inspector (FR-44, NFR-8)
	//
	// With Dev set, App.Handler serves the inspector's JavaScript under the
	// mount and (*App).InspectorScript renders the tag that loads it. With Dev
	// false the route answers 404 and the component writes nothing, which is
	// how NFR-8's "MUST NOT load in production builds" is enforced rather than
	// asserted. The inspector is a separate artifact and costs the shipped
	// runtime nothing at all: it reads the session's frames off the WebSocket
	// and there is no seam for it anywhere in client/runtime.js.
	//
	// The library still logs no HTMX ownership violation, in either mode:
	// RFC-0001 §10.3 chose a documented precedence rule over a server-side
	// scan of rendered HTML for hx-* attributes, so there is nothing
	// server-side to detect. Flagging an hx-* element inside an unpreserved
	// live fragment is the inspector's job, in the browser, where the element
	// actually is.
	//
	// # 3. Dev reload (FR-57)
	//
	// With Dev set, App.Handler serves the dev-reload client's JavaScript and
	// the build-identity route under the mount, and (*App).DevReloadScript
	// renders the tag that loads it; with Dev false all three write nothing or
	// answer 404. A rebuilt-and-restarted process then reloads the page by
	// itself, which is the only way a change outside a live fragment — the
	// page shell, the head, a fragment whose markup moved while its state did
	// not — ever reaches a browser that is already connected.
	//
	// It preserves nothing the process itself did not preserve. See
	// DevBuildID and docs/guide/dev-reload.md, both of which say so in more
	// detail than a field comment can.
	Dev bool

	// DevBuildID overrides the identity gotth-live uses to tell one build of
	// this application from another. It is read only when Dev is set.
	//
	// Leave it empty and the identity is derived, once per process and lazily,
	// from a SHA-256 of the running executable. That default is what makes a
	// restart that rebuilt nothing — a crash loop, a `docker compose restart`,
	// a rebuild of source that did not actually change — leave the page alone
	// and let the client runtime's own reconnect restore it, instead of
	// reloading the document out from under a developer who changed nothing.
	//
	// Set it when the derived value cannot work or is not what you mean: a
	// commit hash injected with -ldflags, a container image digest, or a
	// counter your own reload loop increments. Any value works as long as it
	// CHANGES when the code changes and does not change when it does not; a
	// constant string turns dev reload off without turning Dev off.
	//
	// It is validated at New whatever Dev is set to — at most 128 bytes, no
	// control bytes, no leading or trailing whitespace — because a field
	// checked in only one mode is a field that starts failing on the deploy
	// that flips the mode. The bounds are what the value has to survive: it is
	// rendered into a script tag and returned as the entire body of the poll
	// the browser makes.
	DevBuildID string
}

// ConfigError reports an invalid Config, naming the offending field and what
// to set it to.
type ConfigError struct {
	// Field is the Config field at fault.
	Field string
	// Detail says what to set it to.
	Detail string
}

// Error names the field and the fix, in that order, because a construction
// error is read by the person who wrote the Config and has to change one line
// of it.
func (e *ConfigError) Error() string {
	return fmt.Sprintf("gotth-live: Config.%s is invalid: %s", e.Field, e.Detail)
}

// Limits are the per-connection and per-process resource bounds. Any zero
// field takes its documented default.
//
// New validates them, and reports a *ConfigError naming the field rather than
// starting an application whose configuration cannot work. Two kinds of range
// are checked, and the asymmetry between them is deliberate:
//
//   - No field may be negative. Two of them are channel capacities, and a
//     negative capacity is a runtime panic at the first connection rather than
//     a startup error, which is the worst place to find a typo.
//   - Four fields additionally have a range, because four fields have a
//     protocol predicate behind them: CoalesceFlushAt, and the three the mount
//     Snapshot announces to the client as refined wire values —
//     HeartbeatInterval, MaxInboundFrameBytes and AckWindow. The rest do not
//     get invented ones: an operator who sets MailboxDepth to a million has
//     bought a memory bill, which is their decision to make, and a library
//     that capped it would be deciding an operator's capacity for them.
type Limits struct {
	// MaxInboundFrameBytes caps a decoded frame. It is applied to the
	// connection before any payload is allocated, which is what makes it the
	// authoritative inbound limit rather than a check after the fact.
	// Default 65536.
	//
	// It must be between 1024 and 1048576. The mount snapshot announces it to
	// the client, in a field the schema refines to that interval, so a value
	// outside it is a frame this library builds and then refuses to send. New
	// rejects such a value rather than starting a server every session of
	// which dies at establishment (D-23). Zero takes the default.
	MaxInboundFrameBytes int

	// MaxEventsPerSecond and EventBurst are the inbound event token bucket.
	// Defaults 50 and 100.
	MaxEventsPerSecond float64

	// EventBurst is that bucket's depth: how far a flurry of interactions may
	// run ahead of MaxEventsPerSecond before the limiter starts refusing. A
	// keystroke-per-character field is the case it is sized for.
	EventBurst int

	// MailboxDepth bounds the session's mailbox. A full mailbox rejects with a
	// typed error; it never blocks, because blocking the read pump would stall
	// the connection's own liveness detection.
	//
	// It is also a memory parameter. A Go buffered channel allocates its whole
	// backing array at make time, for the life of the channel, occupied or
	// not. Default 64.
	MailboxDepth int

	// AckChannelDepth bounds the acknowledgement channel. A full channel
	// drops, which is lossless because an acknowledgement is a cumulative
	// high-water mark: the next one supersedes the one dropped and the window
	// re-opens a round trip later. Default 32.
	AckChannelDepth int

	// AckWindow is how many unacknowledged patches may be in flight.
	// Default 16.
	//
	// It must be between 1 and 256, for the reason MaxInboundFrameBytes must:
	// the mount snapshot carries it in a refined field. Zero takes the
	// default, so the floor is reachable only as a deliberate 1.
	AckWindow int

	// CoalesceFlushAt is the size of the contributing-event union at which a
	// coalesced patch is emitted immediately rather than coalesced further, so
	// provenance is never truncated. Default 512.
	//
	// It must be between 1 and 959. The protocol bounds a patch's
	// contributing-event list at 1024 (H-4), and the frame this trigger forces
	// carries more than the trigger counted: the transition being emitted at
	// the time, on top of the ones already deferred, plus whatever the
	// application named in that event's Event.Contributing — at most 64, which
	// is the term that makes the headroom 65 rather than 1. Set above 959 the
	// flush constructs a frame the protocol refuses, and the deferred set is
	// gone by then, so the field whose purpose is to keep provenance is what
	// loses it. New rejects such a value rather than quietly substituting a
	// working one: a limit that silently becomes a different limit is not a
	// limit an operator can reason about.
	//
	// Lower is legal and meaningful — it trades more frames for smaller
	// provenance sets. Zero takes the default.
	CoalesceFlushAt int

	// MinResyncInterval and ResyncBurst are the resync budget, deliberately
	// independent of the event bucket. A resync is the one client frame that
	// triggers work proportional to the whole state. Defaults one second and 3.
	MinResyncInterval time.Duration

	// ResyncBurst is that budget's depth. It is small on purpose: a client
	// that legitimately needs a snapshot needs one, not three, and a client
	// asking repeatedly is either looping or hostile.
	ResyncBurst int

	// WriteDeadline bounds one write; exceeding it with a full window evicts.
	// Default five seconds.
	WriteDeadline time.Duration

	// SlowClientGrace is how long the outbound window may stay continuously
	// full before the session is evicted. Default thirty seconds.
	SlowClientGrace time.Duration

	// HeartbeatInterval must be below the shortest idle timeout in the network
	// path. Default twenty seconds.
	//
	// It must also be between one second and five minutes, for the reason
	// MaxInboundFrameBytes must: the mount snapshot carries it in a refined
	// field, in whole milliseconds. A sub-millisecond interval is out of range
	// however it is spelled, because the wire value is what the predicate
	// applies to.
	HeartbeatInterval time.Duration

	// HeartbeatTimeout is peer-dead detection. Default fifty seconds.
	HeartbeatTimeout time.Duration

	// IdleTimeout evicts a session with no inbound frame other than
	// heartbeats. Default thirty minutes.
	IdleTimeout time.Duration

	// EffectDrainTimeout is how long shutdown waits for in-flight effects
	// before abandoning them and counting it. Default five seconds.
	EffectDrainTimeout time.Duration

	// MaxSessionsPerIdentity bounds one subject's concurrent connections.
	// Default 20.
	MaxSessionsPerIdentity int

	// MaxSessions bounds the process. The default is unlimited, and operators
	// should set it.
	MaxSessions int

	// PanicBudget is how many times one site may panic within a session before
	// the session closes. Other sessions are unaffected either way. Default 3.
	PanicBudget int
}

// DefaultLimits returns the defaults, for inspection and for printing.
func DefaultLimits() Limits {
	d := session.DefaultLimits()
	return Limits{
		MaxInboundFrameBytes:   d.MaxInboundFrameBytes,
		MaxEventsPerSecond:     d.MaxEventsPerSecond,
		EventBurst:             d.EventBurst,
		MailboxDepth:           d.MailboxDepth,
		AckChannelDepth:        d.AckChannelDepth,
		AckWindow:              d.AckWindow,
		CoalesceFlushAt:        d.CoalesceFlushAt,
		MinResyncInterval:      d.MinResyncInterval,
		ResyncBurst:            d.ResyncBurst,
		WriteDeadline:          d.WriteDeadline,
		SlowClientGrace:        d.SlowClientGrace,
		HeartbeatInterval:      d.HeartbeatInterval,
		HeartbeatTimeout:       d.HeartbeatTimeout,
		IdleTimeout:            d.IdleTimeout,
		EffectDrainTimeout:     d.EffectDrainTimeout,
		MaxSessionsPerIdentity: 20,
		MaxSessions:            0,
		PanicBudget:            d.PanicBudget,
	}
}

func (l Limits) internal() session.Limits {
	return session.Limits{
		MaxInboundFrameBytes: l.MaxInboundFrameBytes,
		MaxEventsPerSecond:   l.MaxEventsPerSecond,
		EventBurst:           l.EventBurst,
		MailboxDepth:         l.MailboxDepth,
		AckChannelDepth:      l.AckChannelDepth,
		AckWindow:            l.AckWindow,
		CoalesceFlushAt:      l.CoalesceFlushAt,
		MinResyncInterval:    l.MinResyncInterval,
		ResyncBurst:          l.ResyncBurst,
		WriteDeadline:        l.WriteDeadline,
		SlowClientGrace:      l.SlowClientGrace,
		HeartbeatInterval:    l.HeartbeatInterval,
		HeartbeatTimeout:     l.HeartbeatTimeout,
		IdleTimeout:          l.IdleTimeout,
		EffectDrainTimeout:   l.EffectDrainTimeout,
		PanicBudget:          l.PanicBudget,
	}.Normalize()
}

// validate reports the first Limits field an application cannot get working
// behaviour from, as a *ConfigError naming it.
//
// Failing here rather than at the first connection is the direction New
// already takes for a missing hook or a duplicate fragment identifier, and the
// argument is the same one: every value rejected below is a startup mistake,
// and a startup mistake found at startup is a failed deploy instead of a
// session that misbehaves in production.
//
// Rejecting rather than clamping is the other half of that. A clamp would keep
// the process up and make the running limit different from the configured one,
// so the operator's next reading of their own config would be wrong — and this
// project has already ruled, on normalizeMount, against silently rewriting a
// caller's value into a different one.
//
// It runs before Normalize, on the values as given, so that a zero still means
// "take the default" and a negative is still visible as a negative.
func (l Limits) validate() error {
	// Written out field by field rather than by reflection so that the
	// compiler is what fails when a field is renamed. Completeness is held by
	// live's own suite, which walks Limits by reflection, sets each numeric
	// field negative in turn, and requires New to name it — so a field added
	// without a decision about its range fails there rather than being
	// silently unchecked here.
	negative := []struct {
		field string
		bad   bool
	}{
		{"MaxInboundFrameBytes", l.MaxInboundFrameBytes < 0},
		{"MaxEventsPerSecond", l.MaxEventsPerSecond < 0},
		{"EventBurst", l.EventBurst < 0},
		{"MailboxDepth", l.MailboxDepth < 0},
		{"AckChannelDepth", l.AckChannelDepth < 0},
		{"AckWindow", l.AckWindow < 0},
		{"CoalesceFlushAt", l.CoalesceFlushAt < 0},
		{"MinResyncInterval", l.MinResyncInterval < 0},
		{"ResyncBurst", l.ResyncBurst < 0},
		{"WriteDeadline", l.WriteDeadline < 0},
		{"SlowClientGrace", l.SlowClientGrace < 0},
		{"HeartbeatInterval", l.HeartbeatInterval < 0},
		{"HeartbeatTimeout", l.HeartbeatTimeout < 0},
		{"IdleTimeout", l.IdleTimeout < 0},
		{"EffectDrainTimeout", l.EffectDrainTimeout < 0},
		{"MaxSessionsPerIdentity", l.MaxSessionsPerIdentity < 0},
		{"MaxSessions", l.MaxSessions < 0},
		{"PanicBudget", l.PanicBudget < 0},
	}
	for _, c := range negative {
		if c.bad {
			return &ConfigError{
				Field:  "Limits." + c.field,
				Detail: "must not be negative; leave it zero to take the documented default",
			}
		}
	}

	if l.CoalesceFlushAt > session.MaxCoalesceFlushAt {
		return &ConfigError{
			Field: "Limits.CoalesceFlushAt",
			Detail: fmt.Sprintf(
				"%d is above the protocol's ceiling on a patch's contributing-event list, so the flush "+
					"it triggers would build a frame the protocol refuses and the deferred provenance "+
					"would be dropped with it; set it to at most %d, or leave it zero for the default of %d",
				l.CoalesceFlushAt, session.MaxCoalesceFlushAt, session.DefaultLimits().CoalesceFlushAt),
		}
	}

	for _, p := range l.snapshotParams() {
		if !p.set || p.rng.Contains(p.wire) {
			continue
		}
		return &ConfigError{
			Field: "Limits." + p.field,
			Detail: fmt.Sprintf(
				"%s is outside the range the protocol refines gotthlive.v1.Snapshot.%s to (%s), so the "+
					"mount snapshot every session opens with would be a frame this library builds and "+
					"then refuses to send; set it to between %s and %s, or leave it zero for the "+
					"default of %s",
				p.got, p.rng.Field, p.rng.Predicate,
				p.unit(int64(p.rng.Min)), p.unit(int64(p.rng.Max)), p.unit(p.def)),
		}
	}

	return l.validateHeartbeatPair()
}

// heartbeatTimeoutIntervals is how many heartbeat intervals a HeartbeatTimeout
// must span. It is two, and the second one is what a client is allowed to lose.
//
// One interval is the bare correctness bound: a quiet session's ONLY inbound
// frame is the echo of the heartbeat a tick carried (protocol.md §3.4 —
// liveness is the Heartbeat frame and not an RFC 6455 ping), so the deadline is
// refreshed at most once per HeartbeatInterval and a timeout at or below one
// interval can never be met by any client. Two is what makes the bound useful
// rather than merely satisfiable: at a timeout of one interval plus epsilon, a
// single dropped echo — or a round trip longer than epsilon — closes a healthy
// client, and PRD G9 is "survives bad networks". The defaults clear it with
// room to spare: 50 s against 2 × 20 s.
const heartbeatTimeoutIntervals = 2

// validateHeartbeatPair is D-30: two fields each inside their own range and
// fatal together.
//
// D-23's closure validates the three wire-carried fields one at a time, and
// thoroughly. Nothing validated any of them against the field it only makes
// sense next to. `Actor.onTick` samples the liveness deadline on a ticker of
// HeartbeatInterval, so when HeartbeatTimeout is at or below that period the
// FIRST tick finds the deadline already past and closes the session
// 4010 HEARTBEAT_TIMEOUT before sending the solicitation the client would have
// echoed — measured by QA-2 at HeartbeatInterval=2s, HeartbeatTimeout=1s: a
// client echoing every heartbeat closed 4010 after ZERO heartbeats. Every quiet
// session on such a configuration dies on a HeartbeatInterval cycle, for ever,
// while the close reason blames the peer for a value the operator set.
//
// It is reachable from the value D-23's own error message recommends: take the
// 5m ceiling that message names for HeartbeatInterval, leave HeartbeatTimeout
// alone at its 50 s default, and the deadline is 4m10s past due on the first
// tick.
//
// Three notes on the shape, since this is the third time this function has been
// extended for the same class of defect:
//
//   - It compares the EFFECTIVE values, not the values as given, which is the
//     one place this check has to depart from validate's "runs before Normalize"
//     rule. The whole reachable case is an operator setting one field and never
//     mentioning the other, so a relational check that only saw what was written
//     down would miss exactly the configuration QA-2 reproduced. The error says
//     which of the two came from the defaults, because a message quoting a
//     number the operator never typed is the diagnosis problem D-23 was about.
//   - It runs after the range checks, so the pair it judges is a pair the
//     protocol already admits. An out-of-range interval is told it is out of
//     range, which is the more useful of the two errors.
//   - Field names HeartbeatTimeout, of the two, because raising the timeout is
//     always available while lowering the interval is not: HeartbeatInterval
//     leaves this process as a refined session parameter in the mount Snapshot
//     and is a protocol-visible promise to the client, and HeartbeatTimeout is
//     server-side only.
//
// **The evaluation order inside onTick is NOT independently wrong, and was not
// changed.** Sending the heartbeat before evaluating the deadline cannot
// change the outcome of the same tick: the deadline is on INBOUND frames, and
// no echo can arrive within the tick that solicits it — so reversing the order
// would emit one more heartbeat on a session that closes anyway and would move
// nothing else. The sampling behaviour it produces is already a specified,
// asserted property: test/internal/chaos/case6_partition_test.go states and
// measures that dead-peer detection costs at most
// HeartbeatTimeout + HeartbeatInterval, precisely because the deadline is
// sampled on the tick. D-30 is the missing constraint that makes that property
// satisfiable at all, not a defect in the sampling.
func (l Limits) validateHeartbeatPair() error {
	d := session.DefaultLimits()

	interval, intervalIsDefault := l.HeartbeatInterval, false
	if interval == 0 {
		interval, intervalIsDefault = d.HeartbeatInterval, true
	}
	timeout, timeoutIsDefault := l.HeartbeatTimeout, false
	if timeout == 0 {
		timeout, timeoutIsDefault = d.HeartbeatTimeout, true
	}

	required := heartbeatTimeoutIntervals * interval
	if timeout >= required {
		return nil
	}

	// Which of the two the operator never wrote down, named explicitly, so the
	// message cannot quote a number back at somebody who did not set it.
	var source string
	switch {
	case intervalIsDefault && timeoutIsDefault:
		// Unreachable while the defaults are coherent, and a spec holds them
		// to it rather than trusting this comment.
		source = " Both values are this library's defaults, which is a library bug: report it."
	case intervalIsDefault:
		source = fmt.Sprintf(" HeartbeatInterval is not set here and took its default of %s.", interval)
	case timeoutIsDefault:
		source = fmt.Sprintf(" HeartbeatTimeout is not set here and took its default of %s.", timeout)
	}

	return &ConfigError{
		Field: "Limits.HeartbeatTimeout",
		Detail: fmt.Sprintf(
			"%s is not reachable on a HeartbeatInterval of %s. A quiet session's only inbound frame is "+
				"the echo of the heartbeat a tick carries, and the liveness deadline is evaluated on a "+
				"ticker of HeartbeatInterval, so a timeout below %d intervals closes a client that is "+
				"answering every heartbeat with 4010 HEARTBEAT_TIMEOUT — whose reason blames the peer "+
				"for a value set here. Set HeartbeatTimeout to at least %s, which leaves a client one "+
				"solicitation it may lose, or lower HeartbeatInterval; the defaults are %s and %s.%s",
			timeout, interval, heartbeatTimeoutIntervals, required,
			d.HeartbeatInterval, d.HeartbeatTimeout, source),
	}
}

// snapshotParam is one Limits field that leaves this process as a refined
// session parameter in the mount Snapshot.
type snapshotParam struct {
	// field is the Limits field an operator sets, for the error.
	field string
	// set is false when the field is zero and takes its default, which is
	// always in range and must stay acceptable.
	set bool
	// got is the value as the operator wrote it — a Duration reads as "500ms"
	// and not as the 0 whole milliseconds it narrows to.
	got string
	// wire is the value the Snapshot would carry, in the wire field's own
	// units, widened so that the narrowing cannot hide the violation.
	wire int64
	// rng is the interval the schema's refinement admits.
	rng protocol.SessionParamRange
	// unit renders a wire value in the field's units, so the range an operator
	// is told about is in the units they set the field in.
	unit func(wire int64) string
	// def is the documented default's wire value.
	def int64
}

// snapshotParams is the three fields D-23 is about, and the conversion each of
// them undergoes on the way to the wire.
//
// Until D-23 these were checked nowhere. Out of range, New returned no error
// and every session on that configuration died at establishment with
// Error{INTERNAL} "the server could not encode an update", above a log line
// telling the operator the frame "was built by this library, so this is not a
// client problem" — which sends them to the wrong repository for a value they
// set themselves. It is CoalesceFlushAt's defect class three fields wider, and
// it is answered the same way, in the same function: refuse at construction,
// name the field, and say what the range is and whose rule it is.
//
// The check is on the wire value rather than on the field, because the wire is
// where the predicate applies. HeartbeatInterval is a Duration and the
// Snapshot carries whole milliseconds, so 500µs is a legal Duration and an
// illegal heartbeat_interval_ms; and the narrowing to uint32 the actor
// performs would otherwise let 4294987296 ms — 49 days — arrive as a
// perfectly ordinary 20 s.
//
// Actor.emitSnapshot performs these same three conversions, and this is the
// second place that knows them. They are not shared because live validating a
// configuration must not have to construct an actor to do it; they cannot
// drift, because a spec mounts a real session at each end of each range and
// reads the value back off the wire.
func (l Limits) snapshotParams() []snapshotParam {
	d := session.DefaultLimits()
	ms := func(v int64) string { return (time.Duration(v) * time.Millisecond).String() }
	count := func(v int64) string { return fmt.Sprintf("%d", v) }
	return []snapshotParam{
		{
			field: "HeartbeatInterval",
			set:   l.HeartbeatInterval != 0,
			got:   l.HeartbeatInterval.String(),
			wire:  int64(l.HeartbeatInterval / time.Millisecond),
			rng:   protocol.HeartbeatIntervalMSRange,
			unit:  ms,
			def:   int64(d.HeartbeatInterval / time.Millisecond),
		},
		{
			field: "MaxInboundFrameBytes",
			set:   l.MaxInboundFrameBytes != 0,
			got:   fmt.Sprintf("%d", l.MaxInboundFrameBytes),
			wire:  int64(l.MaxInboundFrameBytes),
			rng:   protocol.MaxInboundFrameBytesRange,
			unit:  count,
			def:   int64(d.MaxInboundFrameBytes),
		},
		{
			field: "AckWindow",
			set:   l.AckWindow != 0,
			got:   fmt.Sprintf("%d", l.AckWindow),
			wire:  int64(l.AckWindow),
			rng:   protocol.AckWindowRange,
			unit:  count,
			def:   int64(d.AckWindow),
		},
	}
}

// AnyOrigin is the sentinel for Config.Origins that disables origin
// validation. It is a named, greppable value so that auditing every deployment
// which turned the check off is one search. Never use it outside local
// development.
const AnyOrigin = "*"

// Anonymous is a Config.Authenticate implementation binding every session to a
// single anonymous identity. It is the explicit opt-out from authentication,
// named rather than implied by a nil hook.
func Anonymous(request *http.Request) (AnonymousIdentity, error) { return AnonymousIdentity{}, nil }

// AnonymousIdentity is the identity [Anonymous] produces, and the type an
// application with no accounts instantiates its Config on.
//
// It is a concrete struct rather than the interface, and it is exported for
// that reason alone: since 2026-09-03 a Config carries its identity type as a
// type parameter, so an application that opts out of authentication still has
// to name a type — and the type it names must not be `live.IIdentity`, because
// naming the interface there is the erasure the ruling removed.
type AnonymousIdentity struct{}

// Subject is the one subject every anonymous session shares.
func (AnonymousIdentity) Subject() string { return "anonymous" }

// AllowAll is a Config.Authorize implementation permitting every event. It is
// the explicit opt-out from per-event authorization.
//
// It carries the identity type parameter because the hook it satisfies does,
// and it must be INSTANTIATED at the call site — `live.AllowAll[Member]`, not
// `live.AllowAll`. Go infers type arguments for a generic function assigned to
// a variable of function type but not for one assigned to a composite
// literal's field, which is exactly how a Config is written. Naming the type is
// two words and the alternative is an error message about instantiation.
func AllowAll[I IIdentity](ctx context.Context, session Session[I], event Event) error { return nil }

// NoCSRFCheck is a Config.CSRF implementation performing no check. It is the
// explicit opt-out, and it is only safe when Config.Origins is a real
// allowlist, since the origin check is then the whole of the CSRF posture.
func NoCSRFCheck(request *http.Request) error { return nil }
