package wsx

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/coder/websocket"

	"github.com/candacelabs/csf/pkg/gotth/internal/obs"
	"github.com/candacelabs/csf/pkg/gotth/internal/protocol"
	"github.com/candacelabs/csf/pkg/gotth/internal/session"
)

// AnyOrigin is the sentinel that disables origin validation. It is a
// deliberately greppable string rather than a boolean, so an audit of every
// deployment that turned the check off is one search.
const AnyOrigin = "*"

// Options[I] configure the transport.
type Options[I session.IIdentity] struct {
	// Origins is the allowlist checked on the upgrade request. Deny by
	// default: no wildcard, no reflection of the request's own Origin, and no
	// pass for a request that sends none.
	Origins []string

	// Authenticate derives the session identity from the upgrade request. It
	// runs before any per-session memory is allocated.
	Authenticate func(request *http.Request) (I, error)

	// CSRF validates a token bound to the authenticated application session.
	CSRF func(request *http.Request) error

	// NewApp returns the application behaviour for one connection.
	NewApp func(request *http.Request) session.IApp[I]

	// Limits are the per-session resource bounds, handed to every session this
	// handler starts. Zero fields take their documented defaults, which
	// session.Limits.withDefaults applies once per session rather than here.
	Limits session.Limits

	// Metrics, Tracer and Logger are the instrumentation triple, and each may
	// be nil: nil is the disabled configuration, not a missing dependency.
	// obs.NewMetrics, NewTracer and NewLogger return nil for a nil provider,
	// and every method on all three is nil-receiver safe, so no caller on the
	// connection path branches on presence.
	Metrics *obs.Metrics

	// Tracer starts the connection and session spans. Nil disables tracing;
	// see Metrics.
	Tracer *obs.Tracer

	// Logger writes the structured connection records. Nil disables logging;
	// see Metrics.
	Logger *obs.Logger

	// Dev is developer mode, carried through to every session this handler
	// starts. It widens the message on the Error frame a contained panic
	// produces and nothing else.
	Dev bool

	// MaxSessions bounds the whole process; zero means unbounded, which the
	// documentation tells operators to change.
	MaxSessions int
	// MaxSessionsPerIdentity bounds one subject's concurrent connections.
	MaxSessionsPerIdentity int
}

// Handler serves the live connection.
//
// It is the only package in this module that names a WebSocket library, and an
// architecture test asserts that the session, render and protocol packages
// never reach it. The core talks to a connection through channels and a framer
// function value rather than through an interface with one implementation.
type Handler[I session.IIdentity] struct {
	opts Options[I]

	// mu guards the session registry, which is process infrastructure rather
	// than any session's state. No session's state is reachable through it.
	mu       sync.Mutex
	sessions map[session.ID]*conn[I]
	perID    map[string]int
	draining bool

	// pending counts admissions that have not become registry entries yet.
	//
	// It is the process limit's other half. An admission reserves a slot; a
	// registration converts that reservation into a registry entry; and until
	// it does, the slot is spoken for and invisible in len(sessions). Without
	// it, admit read a map nothing had written yet — every concurrent upgrade
	// between admit and register saw the same stale length, so N simultaneous
	// upgrades all passed a limit of 1 (BR-8). MaxSessionsPerIdentity was
	// reserved correctly and was the only one of the two that held.
	pending int
}

// NewHandler validates the options and returns a handler.
func NewHandler[I session.IIdentity](o Options[I]) (*Handler[I], error) {
	switch {
	case o.Authenticate == nil:
		return nil, fmt.Errorf("gotth-live: no authentication hook: set one, or opt out explicitly")
	case o.CSRF == nil:
		return nil, fmt.Errorf("gotth-live: no CSRF hook: set one, or opt out explicitly")
	case o.NewApp == nil:
		return nil, fmt.Errorf("gotth-live: no application: this is a library bug")
	case len(o.Origins) == 0:
		return nil, fmt.Errorf("gotth-live: no allowed origins: set them, or opt out explicitly")
	}
	o.Limits = o.Limits.Normalize()

	return &Handler[I]{
		opts:     o,
		sessions: make(map[session.ID]*conn[I]),
		perID:    make(map[string]int),
	}, nil
}

// ServeHTTP performs the handshake and then RETURNS, leaving the session
// running on a goroutine this handler owns.
//
// The order is the security property rather than an implementation detail:
// origin, then authentication against the HTTP request, then the CSRF token,
// then subprotocol negotiation, and only then the upgrade. No per-session
// memory is allocated before authentication succeeds, so a rejected origin
// costs an HTTP response and nothing else.
//
// # Why this returns instead of serving the session inline
//
// The obvious shape — and the one every WebSocket library's example uses — is
// to serve the connection here, so that ServeHTTP returns when the session
// ends. It is also, for a session that lives for hours, an expensive shape,
// and the expense was measured rather than suspected
// (docs/bench/g2-baseline.md).
//
// net/http holds a `*conn[I]` for as long as its handler has not returned, and
// that `*conn[I]` holds a 4,096 B `bufio.Reader`, a 4,096 B `bufio.Writer`, the
// `*response` with its own 2,048 B `bufio.Writer` and header map, and the
// `*Request` with its header map and URL. For an ordinary HTTP request all of
// that is scratch that lives for a millisecond and returns to net/http's pools.
// For a hijacked WebSocket held open by a blocking handler it is per-SESSION
// memory — retained for the life of the connection and never returned to those
// pools, because `finishRequest` is the thing that returns them and hijacking
// skips it.
//
// Returning at the upgrade is what lets net/http collect all of it: the hijack
// already untracked the connection from `Server.activeConn`
// (`setState(StateHijacked)`), the transport already reset both buffers off
// net/http's own reader and writer, and once this frame is gone nothing
// references the `*conn[I]` at all.
//
// Two consequences, both deliberate and neither free:
//
//   - The request context is cancelled the moment this returns, so the session
//     runs under `context.WithoutCancel`. Request VALUES — an application's
//     tracing or auth context — still resolve; request CANCELLATION no longer
//     ends the session, which it never did anyway: it fired only when this
//     function returned, which was the end of the session.
//   - net/http's `recover` is no longer behind the read pump. §9 of RFC-0001
//     makes recovery mandatory rather than a nicety, so the session goroutine
//     installs its own; see the guard in serve.
//
// Middleware that wraps this handler now completes at the upgrade rather than
// at the end of the session. That is the honest boundary for a request that
// became a connection, and it is the one that lets a request-scoped logger or
// timeout mean what it says.
func (h *Handler[I]) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if !h.originAllowed(r) {
		h.opts.Logger.Warn(ctx, "gotth-live: refused an upgrade from a disallowed origin: add it to Config.Origins if it is yours",
			obs.Str("origin", r.Header.Get("Origin")))
		h.opts.Metrics.ConnectionClosed(ctx, protocol.CloseForbiddenOrigin.Label())
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}

	// No nil test on the identity, and its absence is the type parameter doing
	// its job. The hook's result type is the APPLICATION's own since 2026-09-03,
	// so "returned no identity and no error" — the shape this used to catch, a
	// hook returning `nil, nil` through an interface result — is no longer
	// expressible. An application that returns a nil of its own pointer type
	// still can, and that is its bug: its own Subject() is what panics.
	identity, err := h.opts.Authenticate(r)
	if err != nil {
		h.opts.Metrics.ConnectionClosed(ctx, protocol.CloseUnauthenticated.Label())
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}

	if err := h.opts.CSRF(r); err != nil {
		h.opts.Metrics.ConnectionClosed(ctx, protocol.CloseForbiddenOrigin.Label())
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if !offersSubprotocol(r) {
		// A fast reject, and deliberately not the source of truth: the
		// negotiated version is re-asserted in band in the first snapshot.
		w.Header().Set("Sec-WebSocket-Protocol", protocol.Subprotocol)
		http.Error(w, "unsupported protocol version", http.StatusUpgradeRequired)
		return
	}

	if err := h.admit(identity); err != nil {
		h.opts.Logger.Warn(ctx, "gotth-live: refused an upgrade at a session limit",
			obs.Str("subject", identity.Subject()), obs.Err(err))
		http.Error(w, "too many sessions", http.StatusServiceUnavailable)
		return
	}

	id, err := mintID()
	if err != nil {
		// crypto/rand does not fail on a healthy machine, so this is the arm
		// that has to be loud rather than the one that has to be pretty: the
		// error was discarded here until the Phase 4 audit, and an upgrade
		// answering 500 with nothing in the log is unattributable.
		h.opts.Logger.Error(ctx, "gotth-live: could not mint a session identifier: no session was created",
			obs.Str("subject", identity.Subject()), obs.Err(err))
		h.releaseAdmission(identity)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// The origin allowlist above is this library's check, and it is stricter
	// than the library's own host-pattern matching, so the transport's
	// duplicate check is disabled rather than layered on top of a different
	// rule that would have to be kept in step.
	//
	// The ResponseWriter is wrapped so that the buffers the transport retains
	// for the connection's life are the ones this library sized, not the ones
	// net/http sized for an HTTP response. See hijack.go.
	c, err := websocket.Accept(rightSized(w), r, &websocket.AcceptOptions{
		Subprotocols:       []string{protocol.Subprotocol},
		InsecureSkipVerify: true,
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		h.releaseAdmission(identity)
		h.opts.Logger.Warn(ctx, "gotth-live: the upgrade failed", obs.Err(err))
		return
	}

	// The application is constructed HERE, on this goroutine, because it is the
	// last thing that needs the *http.Request. Handing `r` to the session
	// goroutine would keep the request — and net/http's memory behind it —
	// alive for the session's life, which is the cost this function returns to
	// avoid.
	app := h.opts.NewApp(r)
	peer := session.Peer[I]{ID: id, Identity: identity}
	sessionCtx := context.WithoutCancel(ctx)

	// Registration happens on THIS goroutine, before the session's, and it can
	// fail. That is C-34.
	//
	// `admit` is the only thing that consulted `h.draining`, and `Close`
	// snapshots `h.sessions` — so a session admitted before the flag was set
	// and registered after the snapshot was taken was invisible to both, and
	// `Close` returned nil over a session it had never touched. L9-1 measured
	// it at 32 of 300 rounds here against 13 of 300 at the parent: pre-existing,
	// and widened ~2.5× by moving registration onto a goroutine `ServeHTTP`
	// does not wait for.
	//
	// Moving it back onto this goroutine narrows the window; it does not close
	// it, because narrow is what it already was. What closes it is that
	// `register` now takes the same lock `Close` takes and refuses while
	// draining, so the two are ordered and there is no third outcome: either
	// this registration wins the lock first and `Close`'s snapshot contains it,
	// or `Close` wins and this registration is refused. A session that is
	// refused is closed here, before any goroutine exists for it.
	sess := h.newConn(c, peer)
	if err := h.register(sessionCtx, sess); err != nil {
		// The error was constructed and then discarded here until the Phase 4
		// error audit. A library-produced error that reaches nobody is FR-58's
		// failure mode with the volume turned to zero: the operator saw a
		// connection close with going-away and had nothing at all to join it to.
		h.opts.Logger.Warn(ctx, "gotth-live: closed a session that could not be registered",
			obs.Str("session_id", id.String()),
			obs.Str("subject", identity.Subject()),
			obs.Err(err))
		h.releaseAdmission(identity)
		h.opts.Metrics.ConnectionClosed(ctx, protocol.CloseGoingAway.Label())
		_ = c.Close(websocket.StatusCode(protocol.CloseGoingAway), "the server is shutting down")
		return
	}

	go func() {
		defer h.release(identity)
		h.serve(sessionCtx, sess, app)
	}()
}

// originAllowed applies the allowlist. Deny by default, and an absent Origin
// is a denial rather than a pass: a request with no Origin is not a request
// from an allowed one.
func (h *Handler[I]) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	for _, allowed := range h.opts.Origins {
		if allowed == AnyOrigin {
			return true
		}
		if origin != "" && strings.EqualFold(origin, allowed) {
			return true
		}
	}
	return false
}

func offersSubprotocol(r *http.Request) bool {
	for _, header := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, token := range strings.Split(header, ",") {
			if strings.TrimSpace(token) == protocol.Subprotocol {
				return true
			}
		}
	}
	return false
}

// admit reserves a session slot against the process and per-identity limits.
//
// Both limits are RESERVED here, in one critical section, rather than checked
// here and taken effect elsewhere. The process limit used to be checked against
// len(h.sessions) alone, which nothing had written yet: registration happens
// after mintID, after websocket.Accept — a network write — and after NewApp, so
// every upgrade racing through that window read the same stale length and all
// of them passed. Counting the reservations closes it, because the count moves
// under the same mutex the check reads.
//
// Exactly one of releaseAdmission or register must follow, and register is what
// converts the reservation into a registry entry.
func (h *Handler[I]) admit(identity I) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	// No session identifier exists in any of the three refusals below: they
	// decide whether a session may be created, and mintID has not run. FR-58's
	// session clause is therefore inapplicable here rather than unmet, and the
	// record these ride on names the subject instead, which is the only
	// identity the refused upgrade has. The next-step clause is not
	// inapplicable, and until the Phase 4 audit all three were missing it.
	if h.draining {
		return fmt.Errorf("gotth-live: the server is draining and is accepting no new sessions: " +
			"this is Handler.Close in progress, so the client should reconnect to another instance " +
			"rather than retry this one")
	}
	if h.opts.MaxSessions > 0 && len(h.sessions)+h.pending >= h.opts.MaxSessions {
		return fmt.Errorf("gotth-live: the process is at its session limit of %d: "+
			"raise Config.Limits.MaxSessions, or add capacity — every slot is a live connection, "+
			"not a queued one", h.opts.MaxSessions)
	}
	subject := identity.Subject()
	if h.opts.MaxSessionsPerIdentity > 0 && h.perID[subject] >= h.opts.MaxSessionsPerIdentity {
		return fmt.Errorf("gotth-live: identity %q is at its session limit of %d: "+
			"raise Config.Limits.MaxSessionsPerIdentity if one person is meant to hold this many "+
			"tabs open, or leave it — a browser that reconnects without closing the old socket "+
			"reaches this legitimately", subject, h.opts.MaxSessionsPerIdentity)
	}
	h.perID[subject]++
	h.pending++
	return nil
}

// releaseAdmission undoes admit for a connection that never registered: the
// upgrade failed, the identifier could not be minted, or the drain won the
// race. Both halves of the reservation go back.
func (h *Handler[I]) releaseAdmission(identity I) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pending--
	h.dropIdentity(identity)
}

// release returns the per-identity slot a REGISTERED session held. Its process
// slot stopped being a reservation when register took it, and deregister is
// what returns that one, so this must not touch pending — a session counted
// once in the registry and once in pending would bound the process at half
// what the operator set.
func (h *Handler[I]) release(identity I) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.dropIdentity(identity)
}

// dropIdentity decrements one subject's count, deleting the entry at zero so
// the map does not grow with every subject the process has ever seen. The
// caller holds mu.
func (h *Handler[I]) dropIdentity(identity I) {
	subject := identity.Subject()
	if h.perID[subject] <= 1 {
		delete(h.perID, subject)
		return
	}
	h.perID[subject]--
}

// register adds a session to the registry, or refuses because the handler is
// draining.
//
// The refusal is the half that matters and it is why this returns an error.
// `Close` sets `draining` and snapshots `h.sessions` under this same mutex, in
// one critical section, so a registration is on exactly one side of it: either
// it completed first and the snapshot contains it, or it runs after and finds
// `draining` set. There is no interleaving in which a session is live and
// absent from the snapshot, which is what let `Close` report a successful drain
// over a session it never touched (C-34).
func (h *Handler[I]) register(ctx context.Context, c *conn[I]) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.draining {
		// Unlike admit's copy this one CAN name the session: the identifier was
		// minted and the transport accepted the upgrade before registration is
		// attempted, so a reader can line this line up with the close the
		// client saw (FR-58).
		return fmt.Errorf("gotth-live: session %s lost the race with Handler.Close and was not registered: "+
			"the connection is closed with going-away and the client reconnects; "+
			"no application state existed yet, so nothing was lost", c.peer.ID)
	}
	// The reservation admit took becomes a registry entry here, in the same
	// critical section, so the process count is never momentarily short by one
	// and never momentarily double (BR-8).
	h.pending--
	h.sessions[c.peer.ID] = c
	h.opts.Metrics.ConnectionOpened(ctx)
	h.opts.Metrics.SessionsActive(ctx, 1)
	return nil
}

// deregister removes a session exactly once.
func (h *Handler[I]) deregister(ctx context.Context, c *conn[I]) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.sessions[c.peer.ID]; !exists {
		return
	}
	delete(h.sessions, c.peer.ID)
	h.opts.Metrics.SessionsActive(ctx, -1)
}

// Close drains every live session, closing each with the going-away code, and
// waits for in-flight work up to the context's deadline.
func (h *Handler[I]) Close(ctx context.Context) error {
	h.mu.Lock()
	h.draining = true
	live := make([]*conn[I], 0, len(h.sessions))
	for _, c := range h.sessions {
		live = append(live, c)
	}
	h.mu.Unlock()

	// The closes are issued concurrently and deliberately. A graceful close
	// writes a close frame and waits for the peer to answer it, so a client
	// that has stopped reading costs the transport's handshake timeout. Issued
	// in series, one such client would serialize the whole drain behind itself;
	// issued together, the drain's only bound is the caller's deadline, which
	// is the operator's to set.
	var closing sync.WaitGroup
	for _, c := range live {
		closing.Add(1)
		go func(c *conn[I]) {
			defer closing.Done()
			c.close(protocol.CloseGoingAway, "the server is shutting down")
		}(c)
	}
	closing.Wait()

	for _, c := range live {
		select {
		case <-c.done:
		case <-ctx.Done():
			return fmt.Errorf("gotth-live: %d sessions had not finished draining: raise the deadline or investigate a stuck effect", len(live))
		}
	}
	return nil
}

// Sessions reports how many sessions are live. It exists for tests and for the
// leak check, and reads the registry rather than a counter that could drift
// from it.
func (h *Handler[I]) Sessions() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.sessions)
}

func mintID() (session.ID, error) {
	var id session.ID
	if _, err := rand.Read(id[:]); err != nil {
		return id, fmt.Errorf("gotth-live: could not mint a session identifier: %w: "+
			"the system's random source is unavailable, which is a host problem rather than a "+
			"library or client one — no session can be created until it is back", err)
	}
	return id, nil
}
