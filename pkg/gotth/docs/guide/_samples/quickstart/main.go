// Command quickstart is the application docs/quickstart.md builds: a number
// that lives in Go, and a button that changes it.
//
//	go run .          # http://127.0.0.1:8080
package main

import (
	"log"
	"net/http"

	"github.com/candacelabs/csf/pkg/gotth/live"
)

// MountPath is where the live handler is mounted. It is one constant because
// two places must agree on it: app.Mux below, which routes with it, and the
// app.Document call in view.templ that tells the browser where to fetch the
// runtime and open the connection. Those happen on different requests, so
// nothing in the library can check that agreement.
const MountPath = "/live"

// EventInc is the one event name this application accepts. An event whose name
// is not in Config.Events is refused with UNKNOWN_EVENT before the reducer
// runs.
const EventInc = "count.inc"

// State is everything one connected tab can see.
//
// It is per session: two tabs count independently, because each session gets
// its own State at mount. Sharing state between sessions is an effect —
// guide/effects-and-server-push.md.
//
// There is no Config.Init here because this application's sessions start at the
// zero value, which is what a nil mount hook means. Give it one the moment the
// initial state comes from somewhere — a row, a cookie, a flag — and the first
// paint follows, because app.PageHandler renders from that same hook.
type State struct{ N int }

// app is the application, and it is a package-level var rather than a local in
// main so that view.templ can reach it: app.Document renders the page shell.
//
// app.Document is a method for the reason the two dev-mode script tags are:
// what it puts in the document's head depends on this Config, and it emits the
// inspector, the runtime and the dev-reload tags in the one order that works,
// so the page never places them itself.
//
// live.MustNew is live.New with the error turned into a panic, and package
// initialisation is one of the two places it is for: this Config is a literal
// in this file, so any failure is a startup mistake in it and there is nothing
// to do with the error but print it and stop.
var app = live.MustNew(live.Config[State, live.AnonymousIdentity]{
	Reduce: func(s State, ev live.Event) (State, []live.Effect[live.AnonymousIdentity]) {
		if ev.Name == EventInc {
			s.N++
		}
		return s, nil
	},
	Fragments:    []live.Fragment[State]{{ID: "count", Render: Count}},
	Events:       []string{EventInc},
	Origins:      []string{"http://127.0.0.1:8080"},
	Authenticate: live.Anonymous,
	Authorize:    live.AllowAll[live.AnonymousIdentity],
	CSRF:         live.NoCSRFCheck,
})

func main() {
	log.Fatal(http.ListenAndServe("127.0.0.1:8080", app.Mux(MountPath, app.PageHandler(Page))))
}
