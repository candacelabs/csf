// Command chat is gotth-live's multi-user example: one room in Go, several
// browsers, and every message reaching every session over a server push.
//
// # Running it
//
//	go run .                       # http://127.0.0.1:8081
//	go run . -addr 127.0.0.1:9001
//	go run . -provenance           # print the causal log for every transition
//
// Open it in two browsers — or two profiles, or one window and one private
// window, so that each holds its own cookie — and sign in as different
// members. Say something in one and the other repaints. Start typing in one
// and send from the other: the half-typed sentence does not move.
//
// # What happens when you send
//
// The form carries data-gotth-on, written by live.On. The client runtime's one
// delegated listener serializes the form and sends an event frame naming the
// event and the fragment it came from. The session's goroutine authorizes it
// against the identity bound at the handshake, calls the reducer, and the
// reducer — which is pure, and cannot reach the room — returns a PostEffect
// carrying the body and no author. The library performs the effect at the
// actor boundary, where the room stamps the author from the session's own
// identity, numbers the message and offers it to every subscribed session,
// including the one that sent it. Each session's subscription pump emits it as
// an event, each reducer folds it in, and the library sends a patch carrying
// only the fragments whose Dirty functions said they moved.
//
// The composer is not one of them, which is the whole point: nothing in that
// path is allowed to touch the box another person is typing into.
//
// # Security posture
//
// Origins is a real allowlist derived from the listen address, not
// live.AnyOrigin. Authenticate is a real hook: the session is bound to a
// member from a cookie, and an upgrade with no cookie is refused before any
// per-session memory is allocated. Authorize is a real hook: an observer may
// not post and only a moderator may clear the room, and both are checked at
// the mailbox ingress rather than in the markup that hides the buttons.
//
// The one escape hatch is live.NoCSRFCheck, and it is commented in chat.go
// with what production puts in its place and why a token is not expressible
// here today.
package main

import (
	"context"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/candacelabs/csf/pkg/gotth/live"
)

// The stylesheet is embedded rather than read from disk so that the built
// binary is the whole example: `go build . && ./chat` works from anywhere.
//
//go:embed chat.css
var stylesheet []byte

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "chat:", err)
		os.Exit(1)
	}
}

func run() error {
	addr := flag.String("addr", "127.0.0.1:8081", "address to listen on")
	origin := flag.String("origin", "",
		"comma-separated extra browser Origins to allow, e.g. http://192.168.1.10:8081")
	provenance := flag.Bool("provenance", false,
		"log every transition's causal row (event_id, transition_id, patch_id) as JSON")
	flag.Parse()

	room := NewRoom()
	dir := DemoDirectory()
	cfg := Config(room, dir, allowedOrigins(*addr, *origin))

	// Config.Logger is what turns the provenance log on. Leaving it nil
	// disables it, and with it the reverse lookup from a captured patch back
	// to the message that caused it — the frames still carry the chain either
	// way, but nothing indexes it.
	if *provenance {
		cfg.Logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}

	// Dev mode puts the panic value and its stack into the Error frames a
	// contained panic produces, which is what makes the three /panic commands
	// worth typing. It must be false in production: the same frames then carry
	// a fixed generic message and the causal identifiers, and the stack goes
	// to the log only.
	cfg.Dev = true

	app, err := live.New(cfg)
	if err != nil {
		// live.New reports a *live.ConfigError naming the field and what to
		// set it to. Every configuration mistake is a startup failure here
		// rather than a session that misbehaves later.
		return err
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           NewMux(app, room, dir),
		ReadHeaderTimeout: 5 * time.Second,
		// No WriteTimeout: it would cut live connections off mid-session. The
		// library has its own per-write deadline (Limits.WriteDeadline) and
		// its own idle eviction, which is the right layer for it.
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	fmt.Printf("chat: http://%s\n", listener.Addr())
	fmt.Printf("chat: allowed origins %v\n", cfg.Origins)
	fmt.Printf("chat: members %v\n", dir.Names())

	serving := make(chan error, 1)
	go func() { serving <- srv.Serve(listener) }()

	select {
	case err := <-serving:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}

	// Order matters. Stop accepting new connections, then drain the live
	// sessions — App.Close sends each a GOING_AWAY close and waits for
	// in-flight effects, including every session's subscription pump, up to
	// the deadline.
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdown); err != nil {
		slog.Warn("chat: the HTTP server did not shut down cleanly", "error", err)
	}
	return app.Close(shutdown)
}

// MountPath is where the live handler is mounted, and it is one constant
// because two places must agree on it: the router lines below, and the
// live.Script call in view.templ that tells the browser where to fetch the
// runtime and open the connection.
//
// It is deliberately NOT "/live". The library used to default the script tag
// to that path, so an application mounted anywhere else served a page whose
// script 404'd — the page loaded, nothing was live, and no error appeared
// anywhere on the server. live.Script takes the path now; mounting this
// example somewhere else is what keeps that fix honest.
const MountPath = "/chat/live"

// NewMux routes the whole example: the login page, the room, the stylesheet,
// and the live handler.
//
// It is a plain *http.ServeMux and the live handler is a plain http.Handler,
// which is the point — mounting a live application is one Handle call under
// whatever router the application already has.
func NewMux(app *live.App[State, Member], room *Room, dir Directory) *http.ServeMux {
	mux := http.NewServeMux()

	// Both patterns are registered because MountPath is the WebSocket endpoint
	// itself and MountPath+"/" is where the client runtime is served from; the
	// handler tells them apart by path suffix, so no StripPrefix is needed.
	mux.Handle(MountPath, app.Handler())
	mux.Handle(MountPath+"/", app.Handler())

	mux.HandleFunc("/chat.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		http.ServeContent(w, r, "chat.css", time.Time{}, strings.NewReader(string(stylesheet)))
	})

	// /login is the whole of this example's sign-in: it names a member, sets
	// the cookie the upgrade request will carry, and sends the browser back to
	// the room. Production replaces it with whatever already knows who is
	// signed in; Config.Authenticate does not care where the cookie came from.
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("user")
		if _, ok := dir[name]; !ok {
			http.Error(w, "chat: unknown member; try /login?user=alice", http.StatusNotFound)
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     IdentityCookie,
			Value:    name,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
		})
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})

	mux.HandleFunc("/logout", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: IdentityCookie, Value: "", Path: "/", MaxAge: -1})
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")

		member, ok := signedIn(r, dir)
		if !ok {
			if err := LoginPage(app, dir.Names()).Render(r.Context(), w); err != nil {
				slog.Error("chat: rendering the login page failed", "error", err)
			}
			return
		}

		// The first paint is server-rendered from the same room the sessions
		// share, using the same components the fragments render. The snapshot
		// that arrives over the WebSocket a moment later therefore morphs the
		// page to bytes it already has: no flash, no placeholder, and a page
		// that is meaningful with JavaScript disabled.
		//
		// It renders the room as an OBSERVER of it, not as a member of it: the
		// roster does not include this browser, because it has not connected
		// yet and Join has not run. The snapshot repairs that a moment later,
		// which is the same path every other roster change takes.
		if err := Page(app, PageState(room.Log(), member)).Render(r.Context(), w); err != nil {
			slog.Error("chat: rendering the page failed", "error", err)
		}
	})

	return mux
}

// signedIn reads the identity cookie the same way Config.Authenticate does.
// The two are separate on purpose: this one decides what to render, and that
// one decides whether a session may exist. A page is not an authorization.
func signedIn(r *http.Request, dir Directory) (Member, bool) {
	cookie, err := r.Cookie(IdentityCookie)
	if err != nil {
		return Member{}, false
	}
	member, ok := dir[cookie.Value]
	return member, ok
}

// PageState projects a room log and an identity into the state the components
// render. It is the one place the HTTP path and the live path agree on what a
// browser that has not connected yet should see.
func PageState(log *Log, member Member) State {
	return State{
		Me:   member.Name,
		Role: member.Role,
		Room: log,
	}
}

// allowedOrigins turns the listen address into the Origin allowlist a browser
// will actually send.
//
// Deny by default is the library's rule and this is what honouring it costs: a
// list, not a wildcard. A request whose Origin is not here is refused with 403
// before any per-session memory is allocated, and a request with no Origin at
// all is refused too — an absent Origin is not an allowed one.
//
// Production replaces all of this with the one scheme-and-host the application
// is served from. The loopback spellings are here because a browser sends the
// host you typed, and "localhost" and "127.0.0.1" are different strings.
//
// 0.0.0.0 is a bind address and never an Origin: no browser sends it. The
// README's container invocation is "-addr 0.0.0.0:8081", so without that arm
// the documented way to run this example produces an allowlist a browser
// cannot match, and every upgrade is refused with 403.
func allowedOrigins(addr, extra string) []string {
	origins := []string{"http://" + addr}

	if host, port, err := net.SplitHostPort(addr); err == nil {
		switch host {
		case "127.0.0.1", "":
			origins = append(origins, "http://localhost:"+port)
		case "localhost":
			origins = append(origins, "http://127.0.0.1:"+port)
		case "0.0.0.0":
			origins = append(origins, "http://127.0.0.1:"+port, "http://localhost:"+port)
		}
	}

	for _, o := range strings.Split(extra, ",") {
		if o = strings.TrimSpace(o); o != "" {
			origins = append(origins, o)
		}
	}
	return origins
}
