// Command counter-gotth is the gotth-live side of the benchmark counter
// (equivalence-spec §2.1, app C-B).
//
// # Running it
//
//	go run .                          # http://127.0.0.1:3000/counter
//	go run . -addr 127.0.0.1:3100
//	go run . -shim ../../../harness/shim.js
//
// The route is /counter because that is the route every CTR-* interaction file
// under bench/harness/interactions/ navigates to, and §4 permits the harness
// one per-stack line — the ready-condition wiring — not two.
//
// # What this app is, and what it deliberately is not
//
// It is §2.1's F-CTR-1..7 and nothing else. §2.2's C-A — the client-local
// useState counter — is a Next.js-only row by specification, has no gotth-live
// equivalent by construction (BL-3), and is therefore not implemented here.
// counter.go's package comment says so where a reader looking for it will be.
//
// # The TLS boundary (§3.6, amendment A-1)
//
// This process serves PLAINTEXT HTTP and WebSocket on its own port and holds no
// key, no certificate and no TLS listener. TLS is terminated outside the
// measured container, in the shared proxy, identically for both stacks;
// terminating it inside one stack's container and outside the other's is a
// disqualifying method error in either direction, worth about 18,000 B per
// session, and harness/assert-no-tls.mjs proves the absence from outside before
// any D3 cell is recorded rather than trusting this comment.
//
// # Security posture
//
// Origins is a real allowlist derived from the listen address, not
// live.AnyOrigin, and it is the localhost-development form: the two loopback
// spellings a browser might send, plus whatever -origin adds. A production
// deployment replaces it with the one scheme-and-host the page is served from
// and nothing else. Authenticate is live.Anonymous and Authorize is
// live.AllowAll[live.AnonymousIdentity] because a counter has no accounts and no rule about who may
// count; production replaces the first with the session cookie or bearer token
// it already trusts and the second with the check that says which identities
// may change what. live.NoCSRFCheck is safe here ONLY because Origins above is
// a real allowlist — the origin check is then the whole of the CSRF posture,
// which is the condition the library's own doc comment states — and production
// that authenticates with a cookie adds a token bound to the application
// session. Dev is left false, as it must be outside development: it puts a
// panic value and its stack into the error frame the browser receives.
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

// The one stylesheet §2.1 allows. It is embedded rather than read from disk so
// the built binary is the whole application, and it is byte-identical to
// bench/apps/counter/next/src/app/counter.css — asserted by a spec, not by a
// comment.
//
//go:embed counter.css
var stylesheet []byte

// MountPath is where the live handler is mounted. One constant, used by the
// router below and by the live.Script call in view.templ: the tag renders on a
// different request from the one the handler serves, so nothing inside the
// library can catch a disagreement between the two.
const MountPath = "/counter/live"

// Route is the measured route (§2.1: "Route /counter").
const Route = "/counter"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "counter-gotth:", err)
		os.Exit(1)
	}
}

func run() error {
	addr := flag.String("addr", "127.0.0.1:3000", "address to listen on (plaintext; TLS is the proxy's job, §3.6)")
	origin := flag.String("origin", "", "comma-separated extra browser Origins to allow")
	shimPath := flag.String("shim", DefaultShimPath, "path to bench/harness/shim.js, served byte-identically by both stacks (§2.0)")
	flag.Parse()

	// The container hands this process compose's environment and no argv at
	// all (docker/gotth.Dockerfile's ENTRYPOINT is the binary). See
	// applyEnvFallbacks below for the precedence and for why GOGC and
	// GOMEMLIMIT are deliberately absent from it.
	if err := applyEnvFallbacks(flag.CommandLine, os.Getenv); err != nil {
		return err
	}

	shim, err := LoadShim(*shimPath)
	if err != nil {
		// Fatal, unlike the dashboard example's missing HTMX bundle. A page
		// without the shim connects and repaints and looks entirely healthy;
		// the only symptom is a harness run failing on window.__bench twenty
		// minutes later.
		return err
	}

	store := NewStore()
	app, err := live.New(Config(store, allowedOrigins(*addr, *origin)))
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           NewMux(app, store, shim),
		ReadHeaderTimeout: 5 * time.Second,
		// No WriteTimeout: it would cut live connections off mid-session. The
		// library has its own per-write deadline and its own idle eviction,
		// which is the right layer for it.
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}

	fmt.Printf("counter-gotth: http://%s%s\n", listener.Addr(), Route)
	fmt.Printf("counter-gotth: allowed origins %v\n", allowedOrigins(*addr, *origin))
	fmt.Printf("counter-gotth: shim %s (%d bytes, §2.0 byte-identical on both stacks)\n", *shimPath, len(shim))
	fmt.Printf("counter-gotth: tls=none (§3.6 boundary: TLS is terminated in the proxy container)\n")

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

	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		slog.Warn("counter-gotth: the HTTP server did not shut down cleanly", "error", err)
	}
	return app.Close(shutdown)
}

// NewMux routes the whole app: the measured page, the stylesheet, the two bench
// scripts, and the live handler.
func NewMux(app *live.App[State, live.AnonymousIdentity], store *Store, shim []byte) *http.ServeMux {
	mux := http.NewServeMux()

	// Both patterns: MountPath is the WebSocket endpoint and MountPath+"/" is
	// where the client runtime is served from. The handler tells them apart by
	// path suffix, so no StripPrefix is needed.
	mux.Handle(MountPath, app.Handler())
	mux.Handle(MountPath+"/", app.Handler())

	mux.HandleFunc("/counter.css", serveCSS(stylesheet, "counter.css"))
	mux.HandleFunc(ShimRoute, serveScript(shim, "shim.js"))
	mux.HandleFunc(ReadyRoute, serveScript(readyScript, "ready.js"))

	mux.HandleFunc(Route, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != Route {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// §5.5: the measured route is dynamic on both sides. It renders current
		// shared state and cannot be served from a cache, which is why the
		// Next.js route carries `export const dynamic = 'force-dynamic'`.
		w.Header().Set("Cache-Control", "no-store")

		// The first paint is server-rendered from the same store the sessions
		// share, using the same components the fragments render — so the
		// snapshot that arrives over the WebSocket a moment later morphs the
		// page to bytes it already has. No flash, no placeholder, and the value
		// is in the document rather than fetched by it.
		if err := Page(PageState(store)).Render(r.Context(), w); err != nil {
			slog.Error("counter-gotth: rendering the page failed", "error", err)
		}
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, Route, http.StatusFound)
	})

	return mux
}

// PageState projects the store into the state the components render. It is the
// one place the HTTP path and the live path agree on what a browser that has
// not connected yet should see.
//
// Self is the zero ID, so the first paint says "nobody yet" or "another tab"
// and never "this tab": the session that will own this page does not exist yet
// when this render runs. The Next.js side mints its tab id in the Server
// Component for the same reason — a value invented in the browser would be a
// hydration mismatch there, which is a bug class this stack does not have and
// not one to hand the competitor for free.
func PageState(store *Store) State {
	snap := store.Snapshot()
	return State{
		Value:              snap.Value,
		Version:            snap.Version,
		Tabs:               snap.Tabs,
		ChangedBy:          snap.ChangedBy,
		ChangedAtUnixMilli: snap.ChangedAtUnixMilli,
		Age:                ageAt(snap.ChangedAtUnixMilli, time.Now()),
	}
}

// allowedOrigins turns the listen address into the Origin allowlist a browser
// will actually send.
//
// Deny by default is the library's rule and this is what honouring it costs: a
// list, not a wildcard. A request whose Origin is not here is refused with 403
// before any per-session memory is allocated, and a request with no Origin at
// all is refused too.
//
// PRODUCTION POSTURE: replace all of it with the one scheme-and-host the
// application is served from. The loopback spellings are here because a browser
// sends the host you typed and "localhost" and "127.0.0.1" are different
// strings; behind the bench proxy the origin the browser sends is the proxy's,
// which is what -origin is for.
func allowedOrigins(addr, extra string) []string {
	origins := []string{"http://" + addr}

	if host, port, err := net.SplitHostPort(addr); err == nil {
		switch host {
		case "127.0.0.1", "":
			origins = append(origins, "http://localhost:"+port)
		case "localhost":
			origins = append(origins, "http://127.0.0.1:"+port)
		case "0.0.0.0":
			origins = append(origins,
				"http://127.0.0.1:"+port,
				"http://localhost:"+port)
		}
	}

	for _, o := range strings.Split(extra, ",") {
		if o = strings.TrimSpace(o); o != "" {
			origins = append(origins, o)
		}
	}
	return origins
}

// ---------------------------------------------------------------------------
// The environment the measured container is handed (§5.2's topology)
//
// docker/compose.yaml sets PORT, BENCH_HOST, BENCH_ORIGIN, BENCH_FIXTURE_DIR
// and BENCH_TICK_MS on the `app` service, identically for whichever stack is
// under test. The Next.js image translates them in scripts/start-app.mjs; this
// is the translation on this side, and it exists so docker/gotth.Dockerfile's
// ENTRYPOINT can be the binary and nothing else — no launcher, no wrapper
// script, no `sh -c` expanding a variable into an argv.
//
// PRECEDENCE. An explicit flag WINS, the environment is the fallback, and the
// flag's own default is the fallback of last resort. flag.Visit reports the
// flags actually GIVEN on the command line, so "explicit flag wins" is decided
// by what the operator typed rather than by whether a value happens to differ
// from the default: `-tick 100ms` on the command line with BENCH_TICK_MS=10 in
// the environment resolves to 100ms, and a rule that compared values instead
// would silently resolve it the other way.
//
// GOGC AND GOMEMLIMIT ARE NOT HERE, and they are not dropped. The Go runtime
// reads both itself, before main runs. compose sets them because §3.6 requires
// the GC configuration pinned and disclosed, and the run manifest reads them
// back out of the container's own environment rather than out of this process.
// A process that re-read them here would be a second place they could disagree.
//
// This function and its two helpers are BYTE-IDENTICAL in all three bench apps,
// knowingly. They join LoadShim / serveScript / serveCSS, which
// docs/reviews/deduplication.md records as finding D-7 — SPECIFIED, low
// priority, not extracted — and they are written the same way for the same
// reason: the three apps are three modules with no shared package between them,
// and inventing one is the decision D-7 is the place to take, not this one. The
// table below names every flag the family defines and skips the ones a given
// app does not define, which is what lets the three copies be identical rather
// than merely similar.
func applyEnvFallbacks(fs *flag.FlagSet, getenv func(string) string) error {
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })

	for _, fallback := range []struct{ name, value string }{
		{"addr", envAddr(getenv)},
		{"origin", getenv("BENCH_ORIGIN")},
		{"shim", getenv("BENCH_SHIM_PATH")},
		{"htmx", getenv("BENCH_HTMX_PATH")},
		{"fixtures", getenv("BENCH_FIXTURE_DIR")},
		{"tick", envTick(getenv)},
	} {
		// An unset variable and an empty one are the same thing: compose
		// carries `BENCH_ORIGIN: ${BENCH_ORIGIN:-...}`, and an empty value
		// arriving as "clear the allowlist" would be a 403 on every upgrade
		// with nothing in the log to say why.
		if fallback.value == "" || given[fallback.name] || fs.Lookup(fallback.name) == nil {
			continue
		}
		if err := fs.Set(fallback.name, fallback.value); err != nil {
			return fmt.Errorf("bench: -%s from the environment: %w", fallback.name, err)
		}
	}
	return nil
}

// envAddr is BENCH_HOST and PORT joined, and "" when neither is set.
//
// The two names and their two defaults are scripts/start-app.mjs's, so the same
// compose environment produces the same listen address on both stacks:
// BENCH_HOST rather than Docker's own HOSTNAME, which every container has set
// to its own id, and 0.0.0.0 rather than a loopback address the proxy container
// could not reach. When neither is set — somebody running `go run .` from the
// app's own directory — this returns "" and the flag's 127.0.0.1 default
// stands, which is the address that development wants and the one the topology
// must not get.
func envAddr(getenv func(string) string) string {
	host, port := getenv("BENCH_HOST"), getenv("PORT")
	if host == "" && port == "" {
		return ""
	}
	if host == "" {
		host = "0.0.0.0"
	}
	if port == "" {
		port = "3000"
	}
	return net.JoinHostPort(host, port)
}

// envTick is BENCH_TICK_MS as a duration string.
//
// The variable's name says milliseconds and the value compose carries is the
// bare number §2.5's schedule is written in — "100", and "10" for §2.3's stress
// row (R-7) — so the unit is appended here rather than inferred. A value that
// is not a bare count produces a duration that will not parse and the process
// refuses to start, which is the same answer LoadShim gives a missing shim and
// is given for the same reason: a replay running at the wrong rate is a
// measurement nobody can see is wrong.
func envTick(getenv func(string) string) string {
	ms := strings.TrimSpace(getenv("BENCH_TICK_MS"))
	if ms == "" {
		return ""
	}
	return ms + "ms"
}
