// Command dashboard-gotth is the gotth-live side of the benchmark live
// dashboard (equivalence-spec §2.4).
//
// # Running it
//
//	go run .                          # http://127.0.0.1:3000/dashboard
//	go run . -addr 127.0.0.1:3100
//	go run . -fixtures ../../../fixtures -htmx <path to htmx.min.js>
//
// The route is /dashboard because that is what §2.4 specifies and what every
// DSH-* interaction file navigates to. It is a single route: §2 forbids
// client-side routing on both sides.
//
// # The fixture
//
// All five regions are driven by bench/fixtures/dashboard/ticks.jsonl —
// BENCH-1's committed corpus, generated once by bench/fixtures/generate.mjs,
// including its resolution of the §2.5 seed token (`0xG07TH11VE` is not a hex
// literal; it is FNV-1a'd as the ASCII string it is written as, ambiguity Q-B in
// bench/README.md). Neither server generates data; both read the same bytes on
// the same monotonic schedule, tick N at T0 + N × 100 ms (§2.5). The digest of
// the bytes this process read is published on the clock route and asserted
// against the committed .sha256 in a spec.
//
// # The TLS boundary (§3.6, amendment A-1)
//
// This process serves PLAINTEXT HTTP and WebSocket and holds no key, no
// certificate and no TLS listener. TLS is terminated outside the measured
// container, identically for both stacks; the asymmetry is disqualifying in
// either direction and harness/assert-no-tls.mjs proves the absence from
// outside rather than trusting this comment.
//
// # Security posture
//
// Origins is a real allowlist derived from the listen address, not
// live.AnyOrigin, in its localhost-development form; PRODUCTION lists the one
// scheme-and-host the page is served from. live.NoCSRFCheck is safe only
// because of that. Authenticate is live.Anonymous and Authorize is
// live.AllowAll[live.AnonymousIdentity] because a read-only operator dashboard has no accounts — the
// same position examples/dashboard takes, and PRODUCTION behind an SSO proxy
// replaces both. Dev is left false, as it must be outside development.
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

// The one stylesheet §2 allows, byte-identical to
// bench/apps/dashboard/next/src/app/dashboard.css — asserted by a spec, not by
// a comment.
//
//go:embed dashboard.css
var stylesheet []byte

// MountPath is where the live handler is mounted. One constant, because the
// router below and the live.Script call in view.templ must agree and nothing in
// the library can check that they do.
const MountPath = "/dashboard/live"

// Route is the measured route (§2.4: "Route /dashboard").
const Route = "/dashboard"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "dashboard-gotth:", err)
		os.Exit(1)
	}
}

func run() error {
	addr := flag.String("addr", "127.0.0.1:3000", "address to listen on (plaintext; TLS is the proxy's job, §3.6)")
	origin := flag.String("origin", "", "comma-separated extra browser Origins to allow")
	shimPath := flag.String("shim", DefaultShimPath, "path to bench/harness/shim.js (§2.0)")
	htmxPath := flag.String("htmx", DefaultHTMXPath, "path to htmx.min.js; region E is plain HTMX on this stack (§2.4, FR-62)")
	fixtureDir := flag.String("fixtures", DefaultFixtureDir, "path to bench/fixtures (§2.5)")
	tick := flag.Duration("tick", TickMs*time.Millisecond, "fixture replay interval; §2.5's schedule is 100ms")
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
		return err
	}
	htmx, err := LoadHTMX(*htmxPath)
	if err != nil {
		return err
	}
	fixture, err := LoadFixture(*fixtureDir)
	if err != nil {
		return err
	}

	feed := NewFeed(fixture)
	feed.SetInterval(*tick)

	app, err := live.New(Config(feed, allowedOrigins(*addr, *origin)))
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           NewMux(app, feed, shim, htmx),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}

	feed.Start()
	defer feed.Stop()

	fmt.Printf("dashboard-gotth: http://%s%s\n", listener.Addr(), Route)
	fmt.Printf("dashboard-gotth: allowed origins %v\n", allowedOrigins(*addr, *origin))
	fmt.Printf("dashboard-gotth: fixture %d ticks sha256:%s replaying every %s\n",
		len(fixture.Ticks), fixture.SHA256, *tick)
	fmt.Printf("dashboard-gotth: shim %s (%d bytes, §2.0 byte-identical on both stacks)\n", *shimPath, len(shim))
	fmt.Printf("dashboard-gotth: htmx %s at %s (%d bytes, digest verified, counted in D1 per AS-3)\n",
		HTMXVersion, HTMXRoute, len(htmx))
	fmt.Printf("dashboard-gotth: tls=none (§3.6 boundary: TLS is terminated in the proxy container)\n")

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
		slog.Warn("dashboard-gotth: the HTTP server did not shut down cleanly", "error", err)
	}
	return app.Close(shutdown)
}

// NewMux routes the whole app.
func NewMux(app *live.App[State, live.AnonymousIdentity], feed *Feed, shim, htmx []byte) *http.ServeMux {
	mux := http.NewServeMux()

	// WithSID is how Config.Init learns which bench session the page was served
	// for. api-surface §3 deliberately omits Session.Request(); the context
	// derived from the upgrade request is the sanctioned path, and one
	// http.Handler around another is how a value gets into it.
	handler := WithSID(app.Handler())
	mux.Handle(MountPath, handler)
	mux.Handle(MountPath+"/", handler)

	mux.HandleFunc("/dashboard.css", serveCSS(stylesheet, "dashboard.css"))
	mux.HandleFunc(ShimRoute, serveScript(shim, "shim.js"))
	mux.HandleFunc(ReadyRoute, serveScript(readyScript, "ready.js"))
	mux.HandleFunc(HTMXRoute, serveScript(htmx, "htmx.min.js"))
	mux.HandleFunc(ClockRoute, serveClock(feed))
	mux.HandleFunc(PanelRoute, servePanel(feed))

	mux.HandleFunc(Route, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != Route {
			http.NotFound(w, r)
			return
		}

		// The bench session id, minted per page load exactly as the Next.js
		// page mints its sessionKey, and recorded where both the upgrade
		// request and region E's HTMX GET can read it. One tab is one page
		// load, which is the same lifetime as a live session (§3.4).
		sid, err := NewSID()
		if err != nil {
			http.Error(w, "cannot mint a bench session", http.StatusInternalServerError)
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name: SIDCookie, Value: sid, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		})

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// §5.5: the measured route is dynamic on both sides. It renders the
		// current shared frame and cannot be served from a cache.
		w.Header().Set("Cache-Control", "no-store")

		// The panel of a session that has not pressed anything yet, which is
		// every session at page load. It is read rather than created — see
		// Feed.PanelOf — so a document request mints no server-side state and
		// D4's throughput ceiling is not measuring a growing map.
		if err := Page(PageState(feed, sid), feed.PanelOf(sid)).Render(r.Context(), w); err != nil {
			slog.Error("dashboard-gotth: rendering the page failed", "error", err)
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

// PageState projects the feed into the state the components render.
//
// It is the one place the HTTP path and the live path agree on what a browser
// that has not connected yet should see, so the snapshot arriving a moment later
// morphs the page to bytes it already has. The controls are the defaults because
// a session's controls are per connection and this request has none — which is
// also true of the Next.js page, whose snapshot() calls ensure() with the same
// DEFAULT_CONTROLS.
func PageState(feed *Feed, sid string) State {
	frame := feed.Frame()
	return State{
		SID:      sid,
		Shown:    frame,
		Live:     frame,
		Controls: DefaultControls,
		NowMs:    time.Now().UnixMilli(),
	}
}

// allowedOrigins turns the listen address into the Origin allowlist a browser
// will actually send. Deny by default is the library's rule and this is what
// honouring it costs: a list, not a wildcard.
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
