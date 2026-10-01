package live_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	"github.com/a-h/templ"

	"github.com/candacelabs/csf/pkg/gotth/live"
)

// Example is the whole shape of a live application: a state type, a pure
// reducer, a fragment that renders it, and an ordinary http.Handler to mount.
//
// It is the package overview in runnable form, and the three things it shows
// are the three a reader gets wrong first.
//
// The reducer is a plain function of (state, event). It can be called and
// asserted on with no server, no socket and no browser, which is what the
// determinism helpers in live/livetest are built on.
//
// The mount path is the prefix the BROWSER sees, and it is written once and
// used twice: [App.Mux] routes with it and [Script] renders it into the tag.
// Those two calls happen on different requests, so no check inside the library
// can observe a disagreement between them — which is why both are told the
// prefix rather than deriving it, and why one constant is better than two
// literals.
//
// There is no http.StripPrefix anywhere here, at any prefix. The handler routes
// by path SUFFIX and never learns where it was mounted; stripping turns the
// upgrade into a redirect a WebSocket client cannot follow. This example used
// to demonstrate the opposite, above a sentence saying a router strips the
// prefix before the handler is reached, which was never true (QA-1's F-4
// handoff, item 2).
func Example() {
	type state struct{ Count int }

	// In a .templ file the attribute is written { live.Region("counter")... };
	// spelled out here so the example is one file of ordinary Go.
	render := func(s state) templ.Component {
		return templ.ComponentFunc(func(_ context.Context, w io.Writer) error {
			_, err := fmt.Fprintf(w, `<b data-gotth-region="counter">%d</b>`, s.Count)
			return err
		})
	}

	reduce := func(s state, ev live.Event) (state, []live.Effect[live.AnonymousIdentity]) {
		if ev.Name == "counter.increment" {
			s.Count++
		}
		return s, nil
	}

	app, err := live.New(live.Config[state, live.AnonymousIdentity]{
		Init: func(ctx context.Context, session live.Session[live.AnonymousIdentity]) (state, []live.Effect[live.AnonymousIdentity], error) {
			return state{}, nil, nil
		},
		Reduce: reduce,
		Fragments: []live.Fragment[state]{{
			ID:     "counter",
			Render: render,
			Dirty:  func(prev, next state) bool { return prev != next },
		}},
		Events:  []string{"counter.increment"},
		Origins: []string{"https://app.example"},
		// The three security hooks are required, and these are the
		// deliberately greppable opt-outs. An application that meant them
		// would still have had to write them.
		Authenticate: live.Anonymous,
		Authorize:    live.AllowAll[live.AnonymousIdentity],
		CSRF:         live.NoCSRFCheck,
	})
	if err != nil {
		panic(err)
	}
	defer func() {
		if err := app.Close(context.Background()); err != nil {
			panic(err)
		}
	}()

	mux := app.Mux("/live", app.PageHandler(render))

	next, effects := reduce(state{Count: 41}, live.Event{Name: "counter.increment"})
	fmt.Printf("count=%d effects=%d\n", next.Count, len(effects))

	if err := render(next).Render(context.Background(), os.Stdout); err != nil {
		panic(err)
	}
	fmt.Println()

	if err := live.Script("/live").Render(context.Background(), os.Stdout); err != nil {
		panic(err)
	}
	fmt.Println()

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/live/gotth-live.min.js", nil))
	fmt.Printf("runtime=%d %s\n", rec.Code, rec.Header().Get("Content-Type"))

	// Output:
	// count=42 effects=0
	// <b data-gotth-region="counter">42</b>
	// <script src="/live/gotth-live.min.js" data-gotth-url="/live" defer></script>
	// runtime=200 text/javascript; charset=utf-8
}

// exampleState is the state of the smallest application that can hold a
// fragment.
type exampleState struct{ N int }

// exampleRender is that state's one fragment, named so the examples below can
// hand the same function to Fragment.Render and to App.PageHandler — which is
// the discipline PageHandler exists to enforce: the page and the fragment
// render the same component from the same state.
//
// In a .templ file this is a templ block and the attribute is written
// { live.Region("counter")... }; spelled out here so the examples are ordinary
// Go in one file.
func exampleRender(s exampleState) templ.Component {
	return templ.ComponentFunc(func(_ context.Context, w io.Writer) error {
		_, err := fmt.Fprintf(w, `<b %s="counter">%d</b>`, "data-gotth-region", s.N)
		return err
	})
}

// exampleApp is the minimum valid Config, mounted, so each example below shows
// the one thing it is about instead of re-declaring an application.
//
// The security hooks are the deliberately greppable opt-outs. An application
// that meant them would still have to write them.
func exampleApp(dev bool) *live.App[exampleState, live.AnonymousIdentity] {
	app, err := live.New(live.Config[exampleState, live.AnonymousIdentity]{
		Init: func(ctx context.Context, session live.Session[live.AnonymousIdentity]) (exampleState, []live.Effect[live.AnonymousIdentity], error) {
			return exampleState{}, nil, nil
		},
		Reduce: func(s exampleState, ev live.Event) (exampleState, []live.Effect[live.AnonymousIdentity]) {
			if ev.Name == "counter.increment" {
				s.N++
			}
			return s, nil
		},
		Fragments: []live.Fragment[exampleState]{{
			ID:     "counter",
			Render: exampleRender,
			Dirty:  func(prev, next exampleState) bool { return prev != next },
		}},
		Events:       []string{"counter.increment"},
		Origins:      []string{"https://app.example"},
		Authenticate: live.Anonymous,
		Authorize:    live.AllowAll[live.AnonymousIdentity],
		CSRF:         live.NoCSRFCheck,
		Dev:          dev,
	})
	if err != nil {
		panic(err)
	}
	return app
}

// ExampleScript renders the tag that loads the client runtime. The mount path
// is the prefix the handler is reachable at as the BROWSER sees it, and it is
// a parameter because no check inside the library can observe a mismatch: this
// tag renders on the page request, and the handler that would notice is only
// reached on a different request that may never come.
//
// It is not the handler's own path. [App.Handler] routes by path SUFFIX and
// needs no http.StripPrefix at any prefix, so the handler never learns where it
// was mounted and cannot supply this value.
func ExampleScript() {
	if err := live.Script("/app/live").Render(context.Background(), os.Stdout); err != nil {
		panic(err)
	}

	// Output:
	// <script src="/app/live/gotth-live.min.js" data-gotth-url="/app/live" defer></script>
}

// ExampleApp_InspectorScript renders the same page twice, once from an
// application with Config.Dev set and once from one without.
//
// The template does not change between the two. In dev it carries the
// inspector; in production it carries no reference to it at all, and the route
// that would have served the file answers 404 (PRD NFR-8).
//
// The inspector's tag goes ABOVE live.Script's. Both are deferred, deferred
// scripts run in document order, and the inspector has to wrap the WebSocket
// constructor before the runtime opens a socket with it.
func ExampleApp_InspectorScript() {
	page := func(app *live.App[exampleState, live.AnonymousIdentity], w io.Writer) error {
		if err := app.InspectorScript("/live").Render(context.Background(), w); err != nil {
			return err
		}
		if err := live.Script("/live").Render(context.Background(), w); err != nil {
			return err
		}
		_, err := io.WriteString(w, "\n")
		return err
	}

	if err := page(exampleApp(true), os.Stdout); err != nil {
		panic(err)
	}
	if err := page(exampleApp(false), os.Stdout); err != nil {
		panic(err)
	}

	// Output:
	// <script src="/live/gotth-live-inspector.min.js" defer></script><script src="/live/gotth-live.min.js" data-gotth-url="/live" defer></script>
	// <script src="/live/gotth-live.min.js" data-gotth-url="/live" defer></script>
}

// ExampleApp_PageHandler serves the first paint from the mount hook, and shows
// the defect it exists to make unwritable.
//
// The frozen spelling — templ.Handler(Page(State{})) — evaluates Page once,
// when main runs, and serves those bytes for the life of the process. It is
// correct exactly while Init returns the zero value too, and this example gives
// Init something to load, which is all it takes to make it wrong: the frozen
// handler serves 0 to every visitor, corrected only once the WebSocket
// connects, and never at all with JavaScript off. PageHandler cannot be given a
// state value, only the function that renders one, so it re-loads per request
// and the two answers agree.
func ExampleApp_PageHandler() {
	loaded := 41

	app, err := live.New(live.Config[exampleState, live.AnonymousIdentity]{
		// The loader. Its answer changes; the frozen page's cannot.
		Init: func(ctx context.Context, session live.Session[live.AnonymousIdentity]) (exampleState, []live.Effect[live.AnonymousIdentity], error) {
			return exampleState{N: loaded}, nil, nil
		},
		Reduce: func(s exampleState, _ live.Event) (exampleState, []live.Effect[live.AnonymousIdentity]) {
			return s, nil
		},
		Fragments:    []live.Fragment[exampleState]{{ID: "counter", Render: exampleRender}},
		Events:       []string{"counter.increment"},
		Origins:      []string{"https://app.example"},
		Authenticate: live.Anonymous,
		Authorize:    live.AllowAll[live.AnonymousIdentity],
		CSRF:         live.NoCSRFCheck,
	})
	if err != nil {
		panic(err)
	}

	frozen := templ.Handler(exampleRender(exampleState{}))
	perRequest := app.PageHandler(exampleRender)

	get := func(h http.Handler) string {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		return rec.Body.String()
	}

	fmt.Println("frozen: ", get(frozen))
	fmt.Println("loaded: ", get(perRequest))
	loaded = 42
	fmt.Println("frozen: ", get(frozen))
	fmt.Println("loaded: ", get(perRequest))

	// Output:
	// frozen:  <b data-gotth-region="counter">0</b>
	// loaded:  <b data-gotth-region="counter">41</b>
	// frozen:  <b data-gotth-region="counter">0</b>
	// loaded:  <b data-gotth-region="counter">42</b>
}

// ExampleApp_Mux mounts an application and its page in one call, and shows the
// registration a hand-written mux forgets.
//
// The live handler needs TWO patterns: the exact mount path, which is the
// WebSocket upgrade, and the subtree under it, which is the client runtime and
// the dev-only routes. Register only the first and the catch-all answers the
// runtime's URL with the page — 200, text/html, no error anywhere on the
// server, and the browser hands a document to its JavaScript parser. The second
// column below is that mistake, measured.
func ExampleApp_Mux() {
	app := exampleApp(false)
	page := app.PageHandler(exampleRender)

	byMux := app.Mux("/live", page)

	forgotten := http.NewServeMux()
	forgotten.Handle("/live", app.Handler())
	forgotten.Handle("/", page)

	show := func(label string, h http.Handler) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/live/gotth-live.min.js", nil))
		fmt.Printf("%s %d %s\n", label, rec.Code, rec.Header().Get("Content-Type"))
	}

	show("Mux:      ", byMux)
	show("forgotten:", forgotten)

	// Output:
	// Mux:       200 text/javascript; charset=utf-8
	// forgotten: 200 text/html; charset=utf-8
}

// ExampleMustNew constructs an application from a Config that is a literal in
// the source, which is the only place this helper belongs.
//
// Nothing is lost but the choice of what to do next: the panic value is the
// *ConfigError New would have returned, naming the field and what to set it to.
func ExampleMustNew() {
	app := live.MustNew(live.Config[exampleState, live.AnonymousIdentity]{
		Reduce: func(s exampleState, _ live.Event) (exampleState, []live.Effect[live.AnonymousIdentity]) {
			return s, nil
		},
		Fragments:    []live.Fragment[exampleState]{{ID: "counter", Render: exampleRender}},
		Events:       []string{"counter.increment"},
		Origins:      []string{"https://app.example"},
		Authenticate: live.Anonymous,
		Authorize:    live.AllowAll[live.AnonymousIdentity],
		CSRF:         live.NoCSRFCheck,
	})
	fmt.Println("mounted:", app.Handler() != nil)

	// The same call with a field missing. Recovered here so the example can
	// print the message; in a main there is nothing to recover it and the
	// process stops, which is the point.
	func() {
		defer func() { fmt.Println("refused:", recover()) }()
		live.MustNew(live.Config[exampleState, live.AnonymousIdentity]{})
	}()

	// Output:
	// mounted: true
	// refused: gotth-live: Config.Reduce is invalid: set the reducer that advances state
}

// ExampleRetryable classifies an effect's failure, and shows what the unmarked
// default means.
//
// The default is terminal, and that is the direction worth demonstrating rather
// than asserting: an effect may have committed externally before it failed —
// the message was published, the row was written — so re-running one nobody
// classified risks doing it twice. A failure never retried costs a change that
// does not happen and shows up as a session that stops updating. A failure
// retried blindly costs a change that happens twice and shows up as corrupt
// data somebody else owns.
//
// The mark is invisible in the error's message and survives wrapping in either
// direction, so an executor can classify at the bottom and a reducer can ask at
// the top.
func ExampleRetryable() {
	terminal := errors.New("the card was declined")
	transient := live.Retryable(errors.New("the payment gateway timed out"))

	fmt.Println("unmarked: ", live.IsRetryable(terminal))
	fmt.Println("marked:   ", live.IsRetryable(transient))
	fmt.Println("wrapped:  ", live.IsRetryable(fmt.Errorf("charging the card: %w", transient)))
	fmt.Println("nil:      ", live.Retryable(nil), live.IsRetryable(live.Retryable(nil)))
	fmt.Println("message:  ", transient)

	// Output:
	// unmarked:  false
	// marked:    true
	// wrapped:   true
	// nil:       <nil> false
	// message:   the payment gateway timed out
}

// ExampleOnAll puts two bindings on one element.
//
// It is the case that could not be written before OnAll existed: templ renders
// each attribute spread separately, two spreads of On produce the same
// attribute twice, and an HTML parser keeps the first and discards the second —
// so the second binding vanished with no error anywhere.
//
// Order is load-bearing. The client matches in the order given and the first
// match wins, so the key-filtered binding has to come first or nothing can ever
// reach it.
//
// Debounce, Throttle and Fields belong to the binding that asked for them and
// travel inside it — the components after the key, trailing empties trimmed. So
// the Enter binding here is not debounced and the input binding is, which is
// the point: until 2026-08-05 those were attributes of the ELEMENT, both
// bindings read the same 150 ms, and a keystroke inside the window destroyed
// the pending Enter outright.
func ExampleOnAll() {
	attrs := live.OnAll(
		live.OnWith("keydown", "composer.send", live.Bind{Keys: []string{"Enter"}}),
		live.OnWith("input", "composer.type", live.Bind{
			Fields:   map[string]string{"room": "general"},
			Debounce: 150 * time.Millisecond,
		}),
	)
	fmt.Println(attrs)

	// Output:
	// map[data-gotth-on:keydown:composer.send:Enter;input:composer.type::150::room=general]
}
