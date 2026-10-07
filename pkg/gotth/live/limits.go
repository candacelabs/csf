package live

import (
	"fmt"
	"time"

	"github.com/candacelabs/csf/pkg/gotth/internal/protocol"
	"github.com/candacelabs/csf/pkg/gotth/internal/session"
)

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
	// before it counts and logs the overrun. Shutdown then keeps waiting:
	// every effect is joined, so an effect must return once its context is
	// cancelled. Default five seconds.
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
// negativeField names one Limits field that is checked for a negative value.
// It is a named type rather than an anonymous struct because CS-20 refuses an
// unnamed compound, and because naming it documents what the pair of fields is.
type negativeField struct {
	field string
	bad   bool
}

func (l Limits) validate() error {
	// Written out field by field rather than by reflection so that the
	// compiler is what fails when a field is renamed. Completeness is held by
	// live's own suite, which walks Limits by reflection, sets each numeric
	// field negative in turn, and requires New to name it — so a field added
	// without a decision about its range fails there rather than being
	// silently unchecked here.
	negative := []negativeField{
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
