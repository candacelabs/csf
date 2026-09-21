// Package deploying is the compiled source for docs/guide/deploying.md.
//
// Three things a deployment has to get right, and all three are behaviour
// rather than prose, so all three are compiled here and asserted in the spec
// beside this file: the Config knobs that differ between a laptop and a
// production process, the readiness flag a load balancer polls, and the
// shutdown order that decides whether a deploy drops sessions or drains them.
package deploying

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/a-h/templ"

	"github.com/candacelabs/csf/pkg/gotth/live"
)

const (
	// EventTick is the one registered event this sample's application accepts.
	// It exists so live.New has something to validate; nothing on this page is
	// about what it does.
	EventTick = "sizing.tick"

	// FragmentValue is the one live region.
	FragmentValue = "sizing.value"
)

// State is one session's view. It is a value type and comparable, which is what
// lets the library tell a transition that changed state from one that did not.
type State struct{ N int }

// Config is a complete, minimal application, built by a function so the
// production knobs below have something real to be applied to and a spec can
// hand the result to live.New.
func Config(origins []string) live.Config[State, live.AnonymousIdentity] {
	return live.Config[State, live.AnonymousIdentity]{
		Init: func(ctx context.Context, session live.Session[live.AnonymousIdentity]) (State, []live.Effect[live.AnonymousIdentity], error) {
			return State{}, nil, nil
		},
		Reduce: func(s State, ev live.Event) (State, []live.Effect[live.AnonymousIdentity]) { s.N++; return s, nil },
		Fragments: []live.Fragment[State]{{
			ID: FragmentValue,
			Render: func(s State) templ.Component {
				return templ.ComponentFunc(func(_ context.Context, w io.Writer) error {
					_, err := fmt.Fprintf(w, "<span>%d</span>", s.N)
					return err
				})
			},
			Dirty: func(prev, next State) bool { return prev.N != next.N },
		}},
		Events:       []string{EventTick},
		Origins:      origins,
		Authenticate: live.Anonymous,
		Authorize:    live.AllowAll[live.AnonymousIdentity],
		CSRF:         live.NoCSRFCheck,
	}
}

// MaxHeartbeatInterval is the ceiling the protocol refines
// gotthlive.v1.Snapshot.heartbeat_interval_ms to, restated here because the
// constant that carries it lives under internal/ and an application cannot
// import it. Limits.HeartbeatInterval's own doc comment states the same range:
// at least one second, at most five minutes.
const MaxHeartbeatInterval = 5 * time.Minute

// Production sets the Config fields whose right value differs between a laptop
// and a deployed process, and nothing else. Everything not named here keeps
// whatever the caller configured.
//
// proxyIdle is the SHORTEST idle timeout anywhere in the path from the browser
// to this process — the load balancer's, the reverse proxy's, and any NAT or
// service mesh between them. It is the one number the network makes this
// library's business, because a connection that carries no bytes for longer
// than that is closed by something that never told either end.
//
// maxSessions is the process bound. Zero means unlimited, which is
// Limits.MaxSessions's own default and the one default an operator should not
// keep: it is the difference between a memory ceiling and a memory bill.
//
// An idle timeout below three seconds cannot be honoured and is not silently
// adjusted here: the derived interval falls under the protocol's one-second
// floor and live.New refuses the Config naming Limits.HeartbeatInterval. A
// path that closes an idle connection in under three seconds is a path to fix
// rather than a heartbeat to tune.
func Production[S any, I live.IIdentity](cfg live.Config[S, I], proxyIdle time.Duration, maxSessions int) live.Config[S, I] {
	// Dev gates three things — the panic value and stack in an Error frame,
	// the session inspector's route and script tag, and dev reload's route and
	// script tag. All three are developer tools and none of them belongs in
	// front of the public. DevBuildID is read only when Dev is set, so it is
	// cleared with it rather than left as a value nothing reads.
	cfg.Dev = false
	cfg.DevBuildID = ""

	// A third of the idle timeout leaves room for one lost solicitation inside
	// the window, and the 5:2 ratio between timeout and interval is the one
	// the library's own defaults use (20s and 50s). live.New refuses a timeout
	// below two intervals outright, so the pair has to be set together.
	interval := proxyIdle / 3
	if interval > MaxHeartbeatInterval {
		interval = MaxHeartbeatInterval
	}
	cfg.Limits.HeartbeatInterval = interval
	cfg.Limits.HeartbeatTimeout = interval * 5 / 2

	cfg.Limits.MaxSessions = maxSessions
	return cfg
}

// Readiness is the endpoint a load balancer polls, and the one piece of
// shutdown state that has to change BEFORE the process stops accepting.
//
// A liveness probe asks whether the process is alive; this answers whether it
// should be sent new work. The distinction matters here more than it does for
// a stateless service: a session that lands on a process which is about to
// drain gets a connection, a snapshot, and a close a moment later, and the
// user sees a page that connected and then reconnected for no reason they can
// observe.
//
// It reports nothing about the sessions. The library exposes no session count
// on App, deliberately — how many are open is
// gotthlive_sessions_active, which is a metric and not a health signal.
type Readiness struct{ draining atomic.Bool }

// Drain flips the flag. It is safe to call from any goroutine and is
// idempotent.
func (r *Readiness) Drain() { r.draining.Store(true) }

// Draining reports whether Drain has been called.
func (r *Readiness) Draining() bool { return r.draining.Load() }

// ServeHTTP answers 200 while the process wants traffic and 503 once it does
// not. It is deliberately not cached: a cached readiness answer is a load
// balancer routing to a process that stopped saying yes.
func (r *Readiness) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.draining.Load() {
		http.Error(w, "draining", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok\n")
}

// Deployment is one gotth-live process: an HTTP server, the readiness flag in
// front of it, and the live application whose sessions have to be drained
// before the process exits.
type Deployment struct {
	// Server is the HTTP server. Set ReadHeaderTimeout; leave WriteTimeout
	// zero, because it would cut every live connection off mid-session.
	Server *http.Server

	// Listener is what Server serves. It is a field rather than an Addr so
	// that a spec can serve on a port the kernel chose.
	Listener net.Listener

	// Ready is failed before anything else, so the load balancer stops sending
	// new page loads and new upgrades here while the sessions already open are
	// still being drained.
	Ready *Readiness

	// DrainSessions is (*live.App[S, live.AnonymousIdentity]).Close, whose signature this is. It is a
	// function rather than the App itself so that one Deployment can drain
	// several applications and so that this type needs no type parameter.
	DrainSessions func(context.Context) error

	// Grace bounds the whole shutdown: the HTTP server's and the sessions'
	// together. Size it above Limits.EffectDrainTimeout (default five
	// seconds), which is how long App.Close waits for in-flight effects.
	Grace time.Duration
}

// Run serves until ctx is cancelled, then shuts down in the one order that
// drains rather than drops.
//
// The order is the whole content of this function:
//
//  1. Fail readiness. New traffic stops arriving while the sessions in hand
//     are still being served.
//  2. http.Server.Shutdown. It stops accepting, and it finishes the plain HTTP
//     requests in flight — but it does NOT touch a hijacked connection, and
//     every live session is one. So this returns with every session still up,
//     which is exactly why step 3 exists and is not redundant.
//  3. App.Close. It closes every session with 4001 GOING_AWAY and waits for
//     in-flight effects up to the deadline. Every browser then reconnects,
//     against whichever process the load balancer picks, and mounts a fresh
//     session — a new Init, a new snapshot, and no state the process held.
//
// Run returns nil when the server stopped on its own with
// http.ErrServerClosed, which is what Shutdown makes it return.
func (d *Deployment) Run(ctx context.Context) error {
	serving := make(chan error, 1)
	go func() { serving <- d.Server.Serve(d.Listener) }()

	select {
	case err := <-serving:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}

	d.Ready.Drain()

	shutdown, cancel := context.WithTimeout(context.Background(), d.Grace)
	defer cancel()

	if err := d.Server.Shutdown(shutdown); err != nil {
		return fmt.Errorf("gotth-live: the HTTP server did not shut down within the grace period: %w", err)
	}
	return d.DrainSessions(shutdown)
}
