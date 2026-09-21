// Package errorhandling is the compiled source for
// docs/guide/error-handling.md.
package errorhandling

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/candacelabs/csf/pkg/gotth/live"
)

// Start reports a configuration failure the way it is worth reporting.
//
// live.New returns a *live.ConfigError naming the field at fault and what to
// set it to. Every configuration mistake is a startup failure rather than a
// session that misbehaves later, so this is a failed deploy and not a pager at
// three in the morning.
func Start[S any, I live.IIdentity](cfg live.Config[S, I]) (*live.App[S, I], error) {
	app, err := live.New(cfg)
	if err == nil {
		return app, nil
	}

	var cfgErr *live.ConfigError
	if errors.As(err, &cfgErr) {
		return nil, fmt.Errorf("live config field %q: %s", cfgErr.Field, cfgErr.Detail)
	}
	return nil, err
}

// State carries what a session is told about its own failures.
type State struct {
	// Notice is what the person at the keyboard is shown.
	Notice string
	// Attempts counts the retries this session has scheduled.
	Attempts int
}

// SourceFetch is the source of the effect whose failure this reducer knows how
// to handle. It is the value live.EffectFailedSourceField carries, which is
// what the reducer matches on.
const SourceFetch = "report.fetch"

// Reduce handles the failure event — and does not log it.
//
// A failed or panicking effect is delivered as an ordinary event named
// live.EffectFailedEvent, carrying three fields. The name is a library
// constant and not a string to type: an application that hard-codes it and
// gets it wrong ships a failure path that never runs, and its tests pass
// because the reducer's default branch does nothing.
//
// What this function may do with those three fields is fixed by FR-14 and
// FR-16 rather than by taste. It may branch on them and it may put them in
// state; it may not log them, because FR-16 names "logging of application
// data" as I/O and a reducer performs no I/O. The reason is replay and not
// tidiness: a log call in here makes the same event log produce a different
// sequence of records on every run, and determinism is the property the
// reducer is written for. Reporter.FetchEffect below is where this
// application's record of the failure is written.
func Reducer(reporter *Reporter) live.Reducer[State, live.AnonymousIdentity] {
	return func(s State, ev live.Event) (State, []live.Effect[live.AnonymousIdentity]) {
		if ev.Name != live.EffectFailedEvent {
			return s, nil
		}

		source := ev.Fields.Get(live.EffectFailedSourceField)

		// Render the SOURCE, never the error.
		//
		// live.EffectFailedErrorField carries the error's own message, or the
		// panic value, verbatim and unredacted, in production, ungated by
		// Config.Dev. That is right for a reducer, which is server code — but it
		// is also whatever an upstream library chose to put in an error: a
		// connection string, a query, an internal hostname. Rendering it into a
		// fragment publishes it to the browser. The source is a name this
		// application chose, so it is the value that is safe to show.
		s.Notice = "could not refresh " + source

		// Branch on the classification, not on the message. The executor is the
		// party in a position to say whether a failure is transient; an absent or
		// unparseable value is false, and unclassified is terminal, because
		// re-running a terminal failure re-runs whatever made it terminal.
		retryable, _ := strconv.ParseBool(ev.Fields.Get(live.EffectFailedRetryableField))

		// Attempts is the count that survives replay: it is state, so replaying
		// this event log reaches the same number. A counter incremented by a log
		// line or a metric call from inside here would not.
		if retryable && source == SourceFetch && s.Attempts < 3 {
			s.Attempts++
			return s, []live.Effect[live.AnonymousIdentity]{reporter.FetchEffect()}
		}
		return s, nil
	}
}

// Reporter performs the effect, and is where its failure is logged.
//
// This is the home the reducer cannot be, and the move is one hop: an effect's
// Run executes on the session actor after the reducer returned, which is
// exactly what FR-16 means by "the actor boundary". It is also the better place
// on the evidence rather than merely the legal one — it holds the error value,
// so it can classify it, unwrap it, or pull structured fields off it with
// errors.As, none of which is available from the flattened string the failure
// event carries.
type Reporter struct {
	// Log is the application's own logger. It may be the same *slog.Logger
	// handed to Config.Logger; the library's records carry their own names.
	Log *slog.Logger
	// Fetch is the I/O this effect performs.
	Fetch func(context.Context) error
}

// FetchEffect is the effect, and the place its failure is logged.
//
// The library writes no record of an error an effect returns: it turns the
// error into the reducer's live.EffectFailedEvent and counts it in
// gotthlive_effects_total{result="error"} when Config.Metrics is set. This log
// line is the only one there will be, which is why it belongs here and not in
// the reducer that reads the event afterwards.
func (r *Reporter) FetchEffect() live.Effect[live.AnonymousIdentity] {
	return live.Effect[live.AnonymousIdentity]{
		Source: SourceFetch,
		Run: func(ctx context.Context, sess live.Session[live.AnonymousIdentity], emit live.Emitter) error {
			err := r.Fetch(ctx)
			if err == nil {
				return nil
			}

			r.Log.ErrorContext(ctx, "effect failed",
				slog.String("session", sess.ID().String()),
				slog.String("source", SourceFetch),
				slog.String("error", err.Error()),
				slog.Bool("retryable", live.IsRetryable(err)))
			return err
		},
	}
}

// WireLogging sets both halves, because the two halves of a failed effect are
// logged by two different parties.
//
// An effect that returns an error is logged by the Run above; the library adds
// nothing. An effect that PANICS never reaches that line — the library recovers
// it, logs it at error level to Config.Logger with the session, the effect
// source, the event that scheduled it and the stack, and synthesizes the same
// failure event, classified terminal. So an application whose effects log their
// own errors and whose Config leaves Logger nil has logged one half of its
// effect failures and dropped the other half silently.
func WireLogging(cfg live.Config[State, live.AnonymousIdentity], r *Reporter, logger *slog.Logger) live.Config[State, live.AnonymousIdentity] {
	cfg.Reduce = Reducer(r)
	cfg.Logger = logger
	return cfg
}

// ExecuteWithClassification shows both sides of the retry mark.
//
// Retryable marks an error returned from an effect's Run as transient. The
// unmarked default is terminal, deliberately: an effect may have committed
// externally before it failed, so retrying a failure nobody classified risks
// doing it twice. Between a visible omission and an invisible duplicate, the
// default belongs on the omission.
func ExecuteWithClassification(transient bool) error {
	if transient {
		return live.Retryable(fmt.Errorf("report.fetch: upstream timed out"))
	}
	return fmt.Errorf("report.fetch: no such report")
}

// WasTransient reads the mark back off an error the caller already holds.
//
// It is the reader for code that holds the error — an executor deciding
// between its own retry and handing the decision up, and the spec that checks
// it decided correctly. The reader for a reducer is the field on the failure
// event, because what a reducer holds is an event.
//
// The mark is found through errors.As, so it survives arbitrary %w wrapping in
// either direction, and it is invisible in the message.
func WasTransient(err error) bool { return live.IsRetryable(fmt.Errorf("wrapped: %w", err)) }
