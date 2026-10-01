package obs

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"
)

// ProvenanceLogger is the name every provenance record carries, so an operator
// can route the stream to its own retention without matching on message text.
const ProvenanceLogger = "gotthlive.provenance"

// InfoSampleThreshold is the records-per-second above which Info is sampled,
// and InfoSampleRate is the sampling. A record that survives sampling says so,
// because a sampled stream that does not admit it is a lie about volume.
const (
	InfoSampleThreshold = 100
	InfoSampleRate      = 100
)

// Field is one structured log attribute.
//
// The library's log helpers take Field and nothing else, and that is the
// redaction boundary. There is no helper that accepts an arbitrary value, so
// a frame body, an application state value or an identity cannot be passed
// into a record by a caller who was not thinking about it. Redaction applied
// at the boundary is a property; redaction left to callers is a hope.
type Field struct {
	attr slog.Attr
}

// Str returns a string field.
func Str(key, value string) Field { return Field{slog.String(key, value)} }

// U64 returns an unsigned integer field, which is what every causal
// identifier in this library is.
func U64(key string, value uint64) Field { return Field{slog.Uint64(key, value)} }

// Int returns an integer field.
func Int(key string, value int) Field { return Field{slog.Int(key, value)} }

// Bool returns a boolean field.
func Bool(key string, value bool) Field { return Field{slog.Bool(key, value)} }

// Dur returns a duration field, recorded in milliseconds because that is the
// unit the budgets in this project are stated in.
func Dur(key string, d time.Duration) Field {
	return Field{slog.Float64(key, float64(d)/float64(time.Millisecond))}
}

// Err returns an error field. The error's own text is included; error values
// in this library are constructed to carry a session, a causal identifier and
// an actionable next step, and never a payload.
func Err(err error) Field {
	if err == nil {
		return Field{slog.String("error", "")}
	}
	return Field{slog.String("error", err.Error())}
}

// Logger is the library's log sink. A nil *Logger is the disabled
// configuration; every method tests its receiver.
//
// Leaving it disabled also disables the provenance log, which makes the
// reverse lookup from a captured patch back to its cause unavailable. That is
// a documented consequence rather than a silent one: the audit harness and the
// provenance soak both fail closed when the logger is absent.
type Logger struct {
	log  *slog.Logger
	prov *slog.Logger

	// infoWindow and infoCount implement the Info sampling. They are process
	// state rather than session state, so an atomic is the right tool and not
	// a lock the actor model would object to.
	infoWindow atomic.Int64
	infoCount  atomic.Int64
}

// NewLogger wraps a consumer's logger. A nil logger returns nil.
func NewLogger(l *slog.Logger) *Logger {
	if l == nil {
		return nil
	}
	return &Logger{
		log:  l,
		prov: l.With(slog.String("logger", ProvenanceLogger)),
	}
}

// Enabled reports whether records are being emitted.
func (l *Logger) Enabled() bool { return l != nil }

// Debug emits a per-event or per-patch record. It is off in production by
// default, which is a decision the consumer's handler makes.
func (l *Logger) Debug(ctx context.Context, msg string, fields ...Field) {
	l.emit(ctx, slog.LevelDebug, msg, fields)
}

// Info emits a session-lifecycle record, sampled above the threshold.
func (l *Logger) Info(ctx context.Context, msg string, fields ...Field) {
	if l == nil {
		return
	}
	keep, sampled := l.sampleInfo()
	if !keep {
		return
	}
	if sampled {
		fields = append(fields, Int("sampled_1_in", InfoSampleRate))
	}
	l.emit(ctx, slog.LevelInfo, msg, fields)
}

// Warn emits a degradation record: coalescing engaged, a slow client
// degrading, a rate limit engaging, telemetry dropped.
func (l *Logger) Warn(ctx context.Context, msg string, fields ...Field) {
	l.emit(ctx, slog.LevelWarn, msg, fields)
}

// Error emits a record an operator should act on: a recovered panic, an
// abandoned effect, a protocol violation, a fatal authorization denial, a
// fragment identifier collision. Nothing routine reaches it.
func (l *Logger) Error(ctx context.Context, msg string, fields ...Field) {
	l.emit(ctx, slog.LevelError, msg, fields)
}

// emit writes one record.
//
// It builds []slog.Attr and calls LogAttrs rather than building []any and
// calling Log, and the difference is not stylistic. slog.Attr does not fit in
// an interface word, so every field passed through []any is boxed into its own
// heap allocation before slog immediately unboxes it again. On the G2 idle
// workload that is per-session garbage, and GOGC=100 keeps a second copy of
// everything a session leaves behind (docs/bench/g2-baseline.md §5.3). The
// records emitted are byte-identical: slog's own Log recognises an Attr in its
// arguments and uses it unchanged.
func (l *Logger) emit(ctx context.Context, level slog.Level, msg string, fields []Field) {
	if l == nil || !l.log.Enabled(ctx, level) {
		return
	}
	attrs := make([]slog.Attr, len(fields))
	for i, f := range fields {
		attrs[i] = f.attr
	}
	l.log.LogAttrs(ctx, level, msg, attrs...)
}

// sampleInfo reports whether to keep this Info record, and whether the record
// is one of a sampled stream.
//
// The threshold applies to the current second's running count rather than to
// the previous second's total. Measuring the window that has already closed
// would let an entire burst through unsampled and only start sampling after
// the volume that mattered had passed, which is the wrong way round.
func (l *Logger) sampleInfo() (keep, sampled bool) {
	now := time.Now().Unix()
	if l.infoWindow.Swap(now) != now {
		l.infoCount.Store(0)
	}
	n := l.infoCount.Add(1)

	if n <= InfoSampleThreshold {
		return true, false
	}
	return (n-InfoSampleThreshold)%InfoSampleRate == 1, true
}

// Provenance is one transition's causal row: everything needed to answer
// "which event produced the markup the user is looking at" from a log query
// alone.
//
// A transition that emitted no patch — a suppressed render — still gets a
// record, with a zero patch identifier. Without it, the property that the
// state version rises exactly when state changed would be unverifiable,
// because the transitions that produced nothing would be invisible.
type Provenance struct {
	// SessionID scopes every other identifier here. The rest are uint64
	// counters minted per session, so a value is only globally unique paired
	// with this.
	SessionID string

	// EventID is the causal root: the server-minted identity of the event that
	// caused the transition, zero when the server started it itself.
	EventID uint64

	// ClientRef is the browser's own handle for that event, echoed so a client
	// log and a server log can be joined. It is the one untrusted value in the
	// row.
	ClientRef uint64

	// TransitionID names the reducer invocation, one per invocation including
	// one that changed nothing.
	TransitionID uint64

	// StateVersion rises if and only if the transition changed state. Holding
	// it beside TransitionID is what makes that property checkable from the
	// log alone.
	StateVersion uint64

	// PatchID names the emitted frame, and is zero when the transition emitted
	// none — a suppressed render still gets a row.
	PatchID uint64

	// ServerSeq is the frame's place in the session's outbound order, zero for
	// the same reason as PatchID.
	ServerSeq uint64

	// OriginKind is the category of cause: "client_event", "effect", "timer",
	// "pubsub", "mount" or "resync".
	OriginKind string

	// OriginSource is the specific cause within that category, such as
	// "event:cart.add". It is composed from application-supplied halves and is
	// validated before it reaches a frame.
	OriginSource string

	// FragmentIDs are the regions this transition patched, in wire order.
	// Empty means the render was suppressed.
	FragmentIDs []string

	// ContributingEventIDs are the events whose changes this patch carries but
	// which were not individually patched, because coalescing collapsed them.
	// This field is why coalescing does not lose provenance.
	ContributingEventIDs []uint64

	// SupersededFromSeq and SupersededThroughSeq are the inclusive server_seq
	// range a resync snapshot replaced, zero on everything else. They are the
	// only surviving record that the replaced patches existed.
	SupersededFromSeq uint64

	// SupersededThroughSeq is the upper end of that range. See
	// SupersededFromSeq.
	SupersededThroughSeq uint64
}

// Provenance emits one transition record.
//
// It is exempt from Info sampling by construction: a sampled provenance log
// cannot support a hundred-percent, zero-unknown guarantee, and the guarantee
// is the reason the stream exists.
func (l *Logger) Provenance(ctx context.Context, p Provenance) {
	if l == nil {
		return
	}
	// Sized for the ten fixed fields plus the three conditional ones, so the
	// append below never reallocates. This runs once per transition and, on an
	// idle session, once per mount — which is exactly the per-session garbage
	// the GOGC=100 line of the G2 baseline doubles.
	attrs := make([]slog.Attr, 0, 13)
	attrs = append(attrs,
		slog.String("session_id", p.SessionID),
		slog.Uint64("event_id", p.EventID),
		slog.Uint64("client_ref", p.ClientRef),
		slog.Uint64("transition_id", p.TransitionID),
		slog.Uint64("state_version", p.StateVersion),
		slog.Uint64("patch_id", p.PatchID),
		slog.Uint64("server_seq", p.ServerSeq),
		slog.String("origin_kind", p.OriginKind),
		slog.String("origin_source", p.OriginSource),
		slog.Any("fragment_ids", p.FragmentIDs),
	)
	if len(p.ContributingEventIDs) > 0 {
		attrs = append(attrs, slog.Any("contributing_event_ids", p.ContributingEventIDs))
	}
	if p.SupersededFromSeq != 0 || p.SupersededThroughSeq != 0 {
		attrs = append(attrs,
			slog.Uint64("superseded_from_seq", p.SupersededFromSeq),
			slog.Uint64("superseded_through_seq", p.SupersededThroughSeq))
	}
	l.prov.LogAttrs(ctx, slog.LevelInfo, "transition", attrs...)
}
