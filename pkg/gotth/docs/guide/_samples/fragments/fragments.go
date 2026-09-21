// Package fragments is the compiled source for
// docs/guide/fragments-and-dirty-tracking.md.
package fragments

import (
	"time"

	"github.com/a-h/templ"

	"github.com/candacelabs/csf/pkg/gotth/live"
)

// Fragment identifiers. A patch names one of these, so changing one is a
// client-visible change. They match ^[A-Za-z0-9_:.-]{1,64}$ and are unique
// within the application; New refuses a duplicate.
const (
	FragmentReading = "dash.reading"
	FragmentLog     = "dash.log"
)

// Sample is one reading. It is a value, and the History holding it is never
// mutated after construction.
type Sample struct {
	Seq   uint64
	Value int
}

// History is the recent readings, oldest first. It is IMMUTABLE: nothing
// appends in place, and a transition builds a new History and points at it.
//
// It is behind a pointer in State because the library compares consecutive
// states with == to decide whether the state version moved, and a type that is
// not comparable is reported as changed on EVERY transition — so a no-op event
// bumps the version and every fragment's Dirty is asked about a change that did
// not happen. A slice field makes State uncomparable; one pointer field does
// not.
type History struct {
	Samples []Sample
}

func (h *History) with(s Sample, keep int) *History {
	old := h.samples()
	if len(old) >= keep {
		old = old[len(old)-keep+1:]
	}
	next := make([]Sample, 0, len(old)+1)
	next = append(next, old...)
	return &History{Samples: append(next, s)}
}

func (h *History) samples() []Sample {
	if h == nil {
		return nil
	}
	return h.Samples
}

// Len is derived state the log fragment renders.
func (h *History) Len() int { return len(h.samples()) }

// State is one session's view.
//
// Every field is comparable. That is what lets the Dirty functions below use
// ==, and it is what keeps the library's own state comparison working.
type State struct {
	// Latest is the newest reading. A pointer to an immutable value, so == is
	// pointer identity: a new reading is a new pointer, and the same reading
	// folded twice is the same pointer.
	Latest *History

	// ChangedAtUnixMilli is when the reading last moved.
	//
	// It is an int64 and not a time.Time, and that is the hazard this file
	// exists to name. A time.Time read from time.Now carries a monotonic clock
	// reading as well as a wall clock, and == compares both — plus the
	// *Location pointer. Two time.Time values naming the same instant can
	// therefore compare unequal, so a Dirty function written with == over a
	// time.Time reports a change that did not happen on every transition, and
	// State stops being comparable in the way the paragraph on History
	// assumes. time.Time.Equal is the correct comparison and == is not; storing
	// the instant as an integer removes the choice.
	ChangedAtUnixMilli int64

	// Paused belongs to this session and to nothing else.
	Paused bool
}

// Age is derived at the transition, from the event's own At stamp, and
// rendered as data.
//
// A render may not read a clock: it must be a pure function of state, or two
// renders of the same state produce different bytes and the identical-render
// suppression that compares them breaks.
func Age(changedAtUnixMilli int64, at time.Time) time.Duration {
	if changedAtUnixMilli == 0 || at.IsZero() {
		return 0
	}
	if d := at.Sub(time.UnixMilli(changedAtUnixMilli)); d > 0 {
		return d
	}
	return 0
}

// Fragments declares the two live regions.
//
// Dirty is optional. Nil means "re-render on every transition", which is
// always correct and is the right first answer; declare one when a fragment is
// expensive or when an unrelated event stream would otherwise re-render it.
func Fragments() []live.Fragment[State] {
	return []live.Fragment[State]{
		{
			ID:     FragmentReading,
			Render: func(s State) templ.Component { return ReadingRegion(s) },
			// Names exactly what this region renders. Latest is a pointer to
			// an immutable value, so == is the right comparison for it.
			Dirty: func(prev, next State) bool {
				return prev.Latest != next.Latest || prev.ChangedAtUnixMilli != next.ChangedAtUnixMilli
			},
		},
		{
			ID:     FragmentLog,
			Render: func(s State) templ.Component { return LogRegion(s) },
			// Names only what belongs to this session, which is why a reading
			// arriving twenty times a second does not re-render the controls.
			// Widening this to include Latest would be legal, would pass
			// livetest.AssertDirtyComplete, and would cost a render per
			// sample.
			Dirty: func(prev, next State) bool { return prev.Paused != next.Paused },
		},
	}
}

// Fold is the transition a new reading takes. It replaces the History rather
// than appending to the one it was given: a reducer must not mutate the state
// it was handed, which is what makes panic recovery free.
func Fold(s State, sample Sample, atUnixMilli int64) State {
	s.Latest = s.Latest.with(sample, 20)
	s.ChangedAtUnixMilli = atUnixMilli
	return s
}
