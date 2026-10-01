package livetest

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/candacelabs/csf/pkg/gotth/live"
)

// ReplayN replays an event log against a reducer n times and fails unless the
// resulting state and the emitted effects are identical on every run.
//
// It is the determinism harness the library requires rather than suggests. A
// reducer that reads a clock, a random source, or the iteration order of a map
// fails it, and those three are the whole of what usually goes wrong: nothing
// else in a pure function of two values can differ between runs.
//
// State is compared deeply. Effects are compared by the SEQUENCE OF SOURCES
// they were declared under, because [live.Effect] carries its behaviour in a
// function field and Go cannot compare two function values: two closures built
// by the same line of the same reducer are never equal, so a deep comparison of
// effects would fail every determinism check rather than passing the honest
// ones.
//
// That is a narrowing, and it is worth stating plainly. Before the 2026-09-03
// ruling made the effect concrete, an effect was a comparable struct and this
// harness caught a reducer that scheduled `Change{Delta: 1}` on one run and
// `Change{Delta: 2}` on the next. It now catches a reducer that scheduled a
// DIFFERENT effect, or a different number of them, or them in a different
// order — the shape a clock, a random source or a map range actually produces —
// and no longer catches the same effect carrying a different argument. An
// effect worth telling apart is therefore worth naming apart.
func ReplayN[S any, I live.IIdentity](tb testing.TB, reduce live.Reducer[S, I], initial S, log []live.Event, n int) {
	tb.Helper()

	if n < 2 {
		tb.Fatalf("livetest.ReplayN needs at least 2 replays to compare, got %d", n)
	}
	if len(log) == 0 {
		tb.Fatal("livetest.ReplayN was given an empty event log: replaying nothing proves nothing")
	}

	wantState, wantEffects := fold(reduce, initial, log)

	for run := 2; run <= n; run++ {
		gotState, gotEffects := fold(reduce, initial, log)

		if !reflect.DeepEqual(wantState, gotState) {
			tb.Fatalf("livetest.ReplayN: replay %d produced different state than replay 1.\n"+
				"  replay 1: %#v\n  replay %d: %#v\n"+
				"A reducer must be a pure function of (state, event). The usual causes are a clock, "+
				"a random source, or ranging over a map.", run, wantState, run, gotState)
		}
		if !slices.Equal(wantEffects, gotEffects) {
			tb.Fatalf("livetest.ReplayN: replay %d scheduled different effects than replay 1.\n"+
				"  replay 1: %q\n  replay %d: %q\n"+
				"Effects are compared by the sources they were declared under, because Effect.Run is a "+
				"function value and Go cannot compare two of those.",
				run, wantEffects, run, gotEffects)
		}
	}
}

// fold runs the log once and returns the final state with the sources of every
// effect the reducer scheduled, in order.
//
// The sources rather than the effects, for the reason ReplayN's own doc gives:
// a slice of [live.Effect] holds function values, which compare equal only when
// both are nil.
func fold[S any, I live.IIdentity](reduce live.Reducer[S, I], initial S, log []live.Event) (S, []string) {
	state := initial
	var sources []string
	for _, ev := range log {
		var produced []live.Effect[I]
		state, produced = reduce(state, ev)
		for _, effect := range produced {
			sources = append(sources, effect.Source)
		}
	}
	return state, sources
}

// AssertDirtyComplete replays a log against a configuration and fails if any
// fragment declared itself unchanged while its rendered bytes moved.
//
// Under-declaring is the one rendering mistake that produces a stale region in
// production and nothing at all in development, because the fragment is
// usually re-rendered by some other transition before anybody looks. This is
// what catches it before merge.
//
// Over-declaring is not a failure: a render whose bytes did not change is
// suppressed, so declaring too much costs a comparison and nothing else.
func AssertDirtyComplete[S any, I live.IIdentity](tb testing.TB, cfg live.Config[S, I], initial S, log []live.Event) {
	tb.Helper()

	if len(cfg.Fragments) == 0 {
		tb.Fatal("livetest.AssertDirtyComplete: the configuration declares no fragments")
	}
	if cfg.Reduce == nil {
		tb.Fatal("livetest.AssertDirtyComplete: the configuration declares no reducer")
	}

	ctx := context.Background()
	previous := make([]string, len(cfg.Fragments))
	for i, f := range cfg.Fragments {
		html, err := renderFragment(ctx, f, initial)
		if err != nil {
			tb.Fatalf("livetest.AssertDirtyComplete: rendering fragment %q failed: %v", f.ID, err)
		}
		previous[i] = html
	}

	state := initial
	for step, ev := range log {
		prev := state
		state, _ = cfg.Reduce(state, ev)

		for i, f := range cfg.Fragments {
			html, err := renderFragment(ctx, f, state)
			if err != nil {
				tb.Fatalf("livetest.AssertDirtyComplete: rendering fragment %q failed: %v", f.ID, err)
			}
			changed := html != previous[i]
			previous[i] = html

			if !changed || f.Dirty == nil {
				continue
			}
			if !f.Dirty(prev, state) {
				tb.Fatalf("livetest.AssertDirtyComplete: fragment %q declared itself unchanged at event %d (%q), "+
					"but its markup moved.\n  before: %s\n  after:  %s\n"+
					"Widen the fragment's Dirty function, or set it to nil to re-render on every transition.",
					f.ID, step, ev.Name, previous[i], html)
			}
		}
	}
}

func renderFragment[S any](ctx context.Context, f live.Fragment[S], state S) (string, error) {
	component := f.Render(state)
	if component == nil {
		return "", fmt.Errorf("fragment %q rendered no component: its Render returned nil, and a live "+
			"region has to return a templ component for every state — return an empty one rather "+
			"than nil for the state that has nothing to show", f.ID)
	}
	var buf bytes.Buffer
	if err := component.Render(ctx, &buf); err != nil {
		return "", err
	}
	return buf.String(), nil
}
