// Command counter is gotth-live's smallest end-to-end application: a number
// that lives in Go, four buttons that change it, and every open tab kept in
// step by the server.
//
// # Running it
//
//	go run .                       # http://127.0.0.1:8080
//	go run . -addr 127.0.0.1:9000
//	go run . -provenance           # print the causal log for every transition
//
// Open the page in two tabs. Both show the same value, both repaint when
// either changes, and reloading either one keeps the count — none of it is
// stored in the browser.
//
// # What happens when you click
//
// The button carries data-gotth-on, written by live.On. The client runtime's
// one delegated listener sends an event frame naming the event and the
// fragment it came from. The session's goroutine authorizes it, calls the
// reducer, and the reducer — which is pure, and cannot reach the store —
// returns a ChangeEffect. The library performs the effect at the actor
// boundary; the store applies it and pushes a snapshot to every subscribed
// session, including the one that clicked. Each session folds the snapshot in,
// re-renders the fragments its Dirty functions named, and the library sends a
// patch frame carrying only the markup that actually moved. The runtime morphs
// it into place.
//
// Nothing in that path is specific to the tab that clicked, which is why the
// second tab updates for free.
//
// # Security posture
//
// Origins is a real allowlist, derived from the listen address, not
// live.AnyOrigin. The three escape hatches this example does use —
// live.Anonymous, live.AllowAll[live.AnonymousIdentity], live.NoCSRFCheck — are each there because a
// counter demo has no accounts to check against, and each is commented in
// counter.go with what production puts in its place.
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
// binary is the whole example: `go build . && ./counter` works from anywhere,
// which is also what makes the binary-size figure in docs/dependencies.md §D6
// a figure about a complete application.
//
//go:embed counter.css
var stylesheet []byte

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "counter:", err)
		os.Exit(1)
	}
}

func run() error {
	addr := flag.String("addr", "127.0.0.1:8080", "address to listen on")
	origin := flag.String("origin", "",
		"comma-separated extra browser Origins to allow, e.g. http://192.168.1.10:8080")
	provenance := flag.Bool("provenance", false,
		"log every transition's causal row (event_id, transition_id, patch_id) as JSON")
	flag.Parse()

	store := NewStore()
	cfg := Config(store, allowedOrigins(*addr, *origin))

	// Config.Logger is what turns the provenance log on. Leaving it nil
	// disables it, and with it the reverse lookup from a captured patch back
	// to the click that caused it — the frames still carry the chain either
	// way, but nothing indexes it. Off by default here only because a counter
	// at speed is a wall of JSON.
	if *provenance {
		cfg.Logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}

	// Dev mode puts stack traces in error frames and logs ownership
	// violations. It must be false in production; this is an example, and it
	// is the flag you want while learning what the library refuses.
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
		Handler:           NewMux(app, store),
		ReadHeaderTimeout: 5 * time.Second,
		// No WriteTimeout: it would cut live connections off mid-session.
		// The library has its own per-write deadline (Limits.WriteDeadline)
		// and its own idle eviction, which is the right layer for it.
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	fmt.Printf("counter: http://%s\n", listener.Addr())
	fmt.Printf("counter: allowed origins %v\n", cfg.Origins)

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
	// in-flight effects, including this example's per-session subscription
	// pump, up to the deadline.
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdown); err != nil {
		slog.Warn("counter: the HTTP server did not shut down cleanly", "error", err)
	}
	return app.Close(shutdown)
}

// MountPath is where the live handler is mounted, and it is one constant
// because two places must agree on it: the router line below, and the
// live.Script call in view.templ that tells the browser where to fetch the
// runtime and open the connection.
//
// Nothing in the library can check that agreement — the router strips the
// prefix before the handler sees a request, and the script tag renders on the
// page request — so a disagreement is a script tag that 404s and a page that
// loads and never updates. A shared constant is what makes the two sides
// unable to disagree.
const MountPath = "/live"

// NewMux routes the whole example: the page, its stylesheet, and the live
// handler.
//
// It is a plain *http.ServeMux and the live handler is a plain http.Handler,
// which is the point — mounting a live application is one Handle call under
// whatever router the application already has.
func NewMux(app *live.App[State, live.AnonymousIdentity], store *Store) *http.ServeMux {
	mux := http.NewServeMux()

	// The live handler mounts under any prefix. Both patterns are registered
	// because MountPath is the WebSocket endpoint itself and MountPath+"/" is
	// where the client runtime is served from; the handler tells them apart by
	// path suffix, so no StripPrefix is needed.
	mux.Handle(MountPath, app.Handler())
	mux.Handle(MountPath+"/", app.Handler())

	mux.HandleFunc("/counter.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		http.ServeContent(w, r, "counter.css", time.Time{}, strings.NewReader(string(stylesheet)))
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		// The first paint is server-rendered from the same store the sessions
		// share, using the same components the fragments render. The snapshot
		// that arrives over the WebSocket a moment later therefore morphs the
		// page to bytes it already has: no flash, no placeholder, and a page
		// that is meaningful with JavaScript disabled.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// The app is passed to the page because app.Document is a method: it
		// writes the document's shell, and the two dev-only script tags in it
		// render zero bytes unless Config.Dev is set. This example sets Dev,
		// so running it under the watcher —
		//
		//	go run github.com/candacelabs/csf/pkg/gotth/internal/cmd/gotth-live-dev
		//
		// from this directory — makes a change to counter.go or view.templ
		// reach the open browser with no reload button (FR-57), and the
		// session inspector is on the page above the runtime, where it has to
		// be to see anything.
		page := Page(app, PageState(store.Snapshot(), time.Now()))
		if err := page.Render(r.Context(), w); err != nil {
			slog.Error("counter: rendering the page failed", "error", err)
		}
	})

	return mux
}

// PageState projects a store snapshot into the state the components render.
// It is the one place the HTTP path and the live path agree on what a tab that
// has not connected yet should see.
func PageState(snap Snapshot, at time.Time) State {
	return State{
		Value:              snap.Value,
		Version:            snap.Version,
		Tabs:               snap.Tabs,
		ChangedBy:          snap.ChangedBy,
		ChangedAtUnixMilli: snap.ChangedAtUnixMilli,
		Age:                ageAt(snap.ChangedAtUnixMilli, at),
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
// README's container invocation is "-addr 0.0.0.0:8080", so without that arm
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
