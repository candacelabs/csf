package chaos_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/a-h/templ"

	"github.com/candacelabs/csf/pkg/gotth/live"
)

// chaosOrigin is the one origin the application under test accepts.
const chaosOrigin = "https://chaos.example"

// ---------------------------------------------------------------------------
// The application under test
// ---------------------------------------------------------------------------

// ledger is application state that OUTLIVES a session, and it is what makes
// "the DOM converges to server truth" a checkable sentence rather than a
// slogan.
//
// RFC §8.1 decides that a session's lifetime is exactly its connection's: a
// reconnect gets a new actor, a new Mount and a fresh Snapshot, and nothing of
// the old session's state crosses. So the truth a reconnect converges TO cannot
// be session state — it has to live somewhere the connection's death does not
// reach. In a real application that is a database or a pubsub topic; here it is
// this struct, reached by the effect executor, which is the same shape.
//
// It records every commit CALL rather than a count, duplicates preserved, so
// that "no duplicated application effect" is a set-versus-multiset comparison
// and not a hope.
type ledger struct {
	mu       sync.Mutex
	calls    []uint64       // every commit, in order, duplicates preserved
	distinct map[uint64]int // ref -> how many times it committed

	// Session lifecycle, counted from the application's own hooks.
	//
	// The library exposes no live-session count on live.App — internal/wsx has
	// one for its own leak check, and the exported surface has
	// gotthlive_sessions_active, which D-22 shows cannot be trusted for this.
	// Init and Teardown bracket a session exactly, so counting them here is
	// both independent of the library's bookkeeping and the thing an
	// application would actually observe.
	mounts    atomic.Int64
	teardowns atomic.Int64
}

func newLedger() *ledger {
	return &ledger{distinct: map[uint64]int{}}
}

// liveSessions is mounts minus teardowns: the sessions whose actor has started
// and not yet finished.
func (l *ledger) liveSessions() int {
	return int(l.mounts.Load() - l.teardowns.Load())
}

func (l *ledger) commit(ref uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, ref)
	l.distinct[ref]++
}

// total is server truth: how many distinct interactions have committed.
func (l *ledger) total() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.distinct)
}

// callCount is how many times the effect executor ran to completion.
func (l *ledger) callCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.calls)
}

// duplicates returns the refs that committed more than once.
func (l *ledger) duplicates() map[uint64]int {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := map[uint64]int{}
	for ref, n := range l.distinct {
		if n > 1 {
			out[ref] = n
		}
	}
	return out
}

func (l *ledger) committed(ref uint64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.distinct[ref] > 0
}

// board is one session's view. It is comparable, so the actor's own no-change
// detection is exercised rather than bypassed.
type board struct {
	Total int
	Ticks int
	Note  string
}

type chaosUser string

func (u chaosUser) Subject() string { return string(u) }

func text(format string, args ...any) templ.Component {
	return templ.ComponentFunc(func(_ context.Context, w io.Writer) error {
		_, err := fmt.Fprintf(w, format, args...)
		return err
	})
}

// ---------------------------------------------------------------------------
// Effects
// ---------------------------------------------------------------------------

// commitEffect writes one interaction into the ledger. It is the "application
// effect" PRD case 1 asks about: something that happened outside the session
// and therefore survives the session dying.
//
// delay makes the commit outlive a disconnect that arrives during it, which is
// the window RFC §8.5's "an effect that already committed externally stays
// committed" describes and the window a duplicate would appear in.
func commitEffect(led *ledger, ref uint64, delay time.Duration) live.Effect[chaosUser] {
	return live.Effect[chaosUser]{
		Source: "chaos.commit",
		Run: func(ctx context.Context, session live.Session[chaosUser], emit live.Emitter) error {
			if delay > 0 {
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					// Cancelled BEFORE the commit: the interaction is lost,
					// which at-most-once permits. What it must never be is
					// committed twice, and that is what the ledger records.
					return ctx.Err()
				}
			}
			led.commit(ref)
			return nil
		},
	}
}

// tickEffect is the server-initiated update source: FR-62's dashboard shape,
// where the patches are caused by the server rather than by a click.
//
// contributing is what decides whether each emission names a contributing edge.
// It is a parameter rather than always-on because the difference turned out to
// matter to QA3-1: an effect emission carries scheduledBy = 0 and, unless the
// application sets Event.Contributing, nothing at all for the coalescing union
// to accumulate.
func tickEffect(every time.Duration, count int, contributing bool) live.Effect[chaosUser] {
	return live.Effect[chaosUser]{
		Source: "chaos.ticker",
		Run: func(ctx context.Context, session live.Session[chaosUser], emit live.Emitter) error {
			ticker := time.NewTicker(every)
			defer ticker.Stop()
			for i := 0; i < count; i++ {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-ticker.C:
				}
				ev := live.Event{
					Name:   "chaos.tick",
					Fields: live.NewFields(map[string]string{"n": strconv.Itoa(i)}),
				}
				if contributing {
					// One synthetic contributing identifier per emission. The
					// library unions these into the patch's
					// contributing_event_ids, which is the accumulator QA3-1's
					// flush trigger is measured against.
					ev.Contributing = []uint64{uint64(i) + 1}
				}
				if err := emit(ev); err != nil {
					// Saturation is expected under a stalled client; it is not
					// an effect failure worth turning into an event.
					continue
				}
			}
			return nil
		},
	}
}

// ---------------------------------------------------------------------------
// Config
// ---------------------------------------------------------------------------

// chaosConfig returns the application every spec drives unless it says
// otherwise, with its ledger.
//
// Three fragments rather than one: a single-fragment application cannot show
// the difference between a patch that carries one region and a snapshot that
// carries all of them, which is half of what a resync is.
func chaosConfig(led *ledger) live.Config[board, chaosUser] {
	return live.Config[board, chaosUser]{
		Init: func(_ context.Context, _ live.Session[chaosUser]) (board, []live.Effect[chaosUser], error) {
			led.mounts.Add(1)
			// Server truth at mount. This is the line that makes a reconnect
			// converge: a new actor reads the ledger rather than starting at
			// zero, exactly as a real application re-reads its store.
			return board{Total: led.total(), Note: "ok"}, nil, nil
		},
		Teardown: func(_ context.Context, _ live.Session[chaosUser], _ board) {
			led.teardowns.Add(1)
		},
		Reduce: func(state board, ev live.Event) (board, []live.Effect[chaosUser]) {
			switch ev.Name {
			case "chaos.commit":
				ref, _ := strconv.ParseUint(ev.Fields.Get("ref"), 10, 64)
				delay, _ := time.ParseDuration(ev.Fields.Get("delay"))
				state.Total++
				return state, []live.Effect[chaosUser]{commitEffect(led, ref, delay)}
			case "chaos.note":
				state.Note = ev.Fields.Get("note")
			case "chaos.ticks":
				every, _ := time.ParseDuration(ev.Fields.Get("every"))
				count, _ := strconv.Atoi(ev.Fields.Get("count"))
				return state, []live.Effect[chaosUser]{tickEffect(every, count, ev.Fields.Get("contributing") == "true")}
			case "chaos.tick":
				state.Ticks++
			case "chaos.noop":
				// Deliberately changes nothing: the state version must not move.
			}
			return state, nil
		},
		Fragments: []live.Fragment[board]{
			{
				ID:     "total",
				Render: func(s board) templ.Component { return text("<b>%d</b>", s.Total) },
				Dirty:  func(prev, next board) bool { return prev.Total != next.Total },
			},
			{
				ID:     "ticks",
				Render: func(s board) templ.Component { return text("<span>%d</span>", s.Ticks) },
				Dirty:  func(prev, next board) bool { return prev.Ticks != next.Ticks },
			},
			{
				ID:     "note",
				Render: func(s board) templ.Component { return text("<i>%s</i>", s.Note) },
				Dirty:  func(prev, next board) bool { return prev.Note != next.Note },
			},
		},
		Events:       []string{"chaos.commit", "chaos.note", "chaos.noop", "chaos.ticks"},
		Origins:      []string{chaosOrigin},
		Authenticate: func(request *http.Request) (chaosUser, error) { return chaosUser("chaos"), nil },
		Authorize:    live.AllowAll[chaosUser],
		CSRF:         live.NoCSRFCheck,
	}
}

// renderTotal is what the "total" fragment renders for a given ledger, computed
// here rather than read off the wire. Comparing the snapshot's markup against
// this is what makes "converges to SERVER TRUTH" independent of the frames the
// server sent.
func renderTotal(n int) string { return fmt.Sprintf("<b>%d</b>", n) }

// discardLogger is a real slog handler that drops everything, for the specs
// where the log must be ENABLED (the provenance index is part of what is under
// test) but its bytes are not wanted.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))
}
