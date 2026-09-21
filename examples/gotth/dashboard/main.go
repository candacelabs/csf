// Command dashboard is gotth-live's resilience example: a simulated metrics
// feed pushing from the server twenty times a second, three live regions that
// are patched independently, and two plain-HTMX regions on the same page.
//
// # Running it
//
//	go run .                        # http://127.0.0.1:8082
//	go run . -addr 127.0.0.1:9002
//	go run . -interval 200ms        # a slower feed, for reading the frames
//	go run . -provenance            # print the causal log for every transition
//	go run . -resync-cost 200       # measure a full resync and exit
//
// The FR-34 backpressure metrics are at /metrics.txt while it runs.
//
// Open it and watch the sample number climb with nothing clicked. Open a second
// tab and press "Clear alerts" in one of them; the other empties. Press "Pause
// this tab" and the feed keeps running for everybody else.
//
// # What this example is for
//
// PRD FR-62 names five properties a live dashboard must demonstrate, and each
// one has a spec in wire_test.go asserting it on the frames rather than on the
// application's own state:
//
//   - high-frequency server-initiated updates, with no client polling;
//   - multiple independent live regions, patched independently;
//   - batching and debounce, where a coalesced patch still names every
//     contributing event (FR-43);
//   - backpressure under a slow client, bounded and with a defined
//     degradation (FR-51);
//   - a plain-HTMX region on the same page (FR-31, FR-32).
//
// README.md says which spec covers which property and what was mutated to prove
// each spec can fail.
//
// # The feed is simulated, and says so
//
// The values are a bounded random walk over three made-up series, produced by a
// seeded generator in this process. Nothing reads a real machine. What is real
// is everything the walk feeds into: a source of change the server owns, at a
// rate no browser asked for, delivered by a push over one connection.
//
// # Security posture
//
// Origins is a real allowlist derived from the listen address, not
// live.AnyOrigin. Authenticate is live.Anonymous and Authorize is live.AllowAll[live.AnonymousIdentity],
// because a read-only demo dashboard has no accounts — examples/chat is where
// the identity and per-event authorization story is told, and a real dashboard
// behind an SSO proxy would do what chat does.
package main

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
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
// binary is the whole example: `go build . && ./dashboard` works from anywhere.
//
//go:embed dashboard.css
var stylesheet []byte

// DefaultHTMXPath is where this repository keeps its one copy of HTMX: the
// artifact the conformance suite vendors, with the digest below recorded beside
// it in pkg/gotth/test/internal/conformance/testdata/README.md.
//
// It is read from that path rather than copied in here, and the digest is
// checked at startup rather than only in a README, because two copies of a
// vendored artifact drift and one copy with an unchecked digest is provenance
// nobody enforces. The path is relative, so it resolves when the example is run
// the way its README says to run it — from this directory — and the flag exists
// for every other case. FRICTION.md item F-3 records what an example that wants
// HTMX has to do today and what would be better.
const (
	DefaultHTMXPath = "../../../pkg/gotth/test/internal/conformance/testdata/htmx-2.0.10.min.js"
	HTMXVersion     = "2.0.10"
	HTMXSHA256      = "71ea67185bfa8c98c39d31717c6fce5d852370fcdfd129db4543774d3145c0de"
)

// HTMXRoute is where this application serves the vendored bundle from, and what
// the page's script tag names when the bundle was found.
const HTMXRoute = "/htmx.min.js"

// MetricsRoute serves the FR-34 backpressure numbers as plain text.
//
// It is text and not the Prometheus exposition format, and the route says
// `.txt` so nobody points a scraper at it. A real deployment gives
// `Config.Metrics` an OTel exporter; this is here so that "the metrics move
// under load" is something a person can watch happen rather than a claim in a
// document. It is unauthenticated because this application has no identities at
// all — see the package comment — and an operations dashboard that did have
// them would put this behind the same rule as everything else.
const MetricsRoute = "/metrics.txt"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "dashboard:", err)
		os.Exit(1)
	}
}

func run() error {
	addr := flag.String("addr", "127.0.0.1:8082", "address to listen on")
	origin := flag.String("origin", "",
		"comma-separated extra browser Origins to allow, e.g. http://192.168.1.10:8082")
	interval := flag.Duration("interval", 50*time.Millisecond,
		"how often the simulated feed takes a sample")
	seed := flag.Uint64("seed", 1, "seed for the simulated feed's random walk")
	provenance := flag.Bool("provenance", false,
		"log every transition's causal row (event_id, transition_id, patch_id) as JSON")
	htmxPath := flag.String("htmx", DefaultHTMXPath,
		"path to htmx.min.js; the page's HTMX regions are inert without it")
	resyncCost := flag.Int("resync-cost", 0,
		"measure the bytes and latency of a full resync this many times, print the report, and exit")
	flag.Parse()

	htmx, htmxErr := LoadHTMX(*htmxPath)
	if htmxErr != nil {
		// Not fatal. The live half of this example is what it is for, and a
		// missing HTMX bundle must not stop it starting — but it is announced
		// here and in the page, because an HTMX region that silently does
		// nothing is the failure this example is partly about.
		fmt.Fprintf(os.Stderr, "dashboard: %v\n", htmxErr)
		fmt.Fprintf(os.Stderr, "dashboard: the two HTMX regions will not swap; pass -htmx <path to htmx.min.js>\n")
	}

	feed := NewFeed(*seed, *interval)

	if *resyncCost > 0 {
		// The measurement path. It builds its own application on its own
		// loopback listener — see resync.go — so it takes the feed and the
		// sample count and nothing else, and the server this function would
		// otherwise start is never started.
		//
		// It is run from the example binary rather than from a spec
		// deliberately: README.md quotes the command, and a measurement whose
		// command is "run this test with these flags" is one nobody re-runs.
		feed.Start()
		defer feed.Stop()
		report, err := MeasureResync(context.Background(), feed, *resyncCost)
		if err != nil {
			return err
		}
		report.Print(os.Stdout)
		return nil
	}

	// The metrics sink. It is always installed rather than sitting behind a
	// flag, because FR-34's backpressure numbers are what this example exists to
	// show and a demonstration nobody switches on demonstrates nothing. It costs
	// one map entry per series and folds every observation in place; metrics.go
	// says why that is bounded and where the honest limits of it are.
	meters := NewMeters()

	cfg := Config(feed, allowedOrigins(*addr, *origin))
	cfg.Metrics = meters

	// Config.Logger is what turns the provenance log on. Leaving it nil
	// disables it, and with it the reverse lookup from a captured patch back to
	// the sample that caused it — the frames still carry the chain either way,
	// but nothing indexes it.
	if *provenance {
		cfg.Logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}

	app, err := live.New(cfg)
	if err != nil {
		// live.New reports a *live.ConfigError naming the field and what to set
		// it to. Every configuration mistake is a startup failure here rather
		// than a session that misbehaves later.
		return err
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           NewMux(app, feed, htmx, meters),
		ReadHeaderTimeout: 5 * time.Second,
		// No WriteTimeout: it would cut live connections off mid-session. The
		// library has its own per-write deadline (Limits.WriteDeadline) and its
		// own idle eviction, which is the right layer for it.
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}

	feed.Start()
	defer feed.Stop()

	fmt.Printf("dashboard: http://%s\n", listener.Addr())
	fmt.Printf("dashboard: allowed origins %v\n", cfg.Origins)
	fmt.Printf("dashboard: feed sampling every %s over %v\n", *interval, Series)
	fmt.Printf("dashboard: backpressure metrics at http://%s%s\n", listener.Addr(), MetricsRoute)
	if htmx != nil {
		fmt.Printf("dashboard: htmx %s served at %s (%d bytes, digest verified)\n",
			HTMXVersion, HTMXRoute, len(htmx))
	}

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
	// in-flight effects, including every session's subscription pump, up to the
	// deadline.
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdown); err != nil {
		slog.Warn("dashboard: the HTTP server did not shut down cleanly", "error", err)
	}
	return app.Close(shutdown)
}

// MountPath is where the live handler is mounted, and it is one constant
// because two places must agree on it: the router lines below, and the
// live.Script call in view.templ that tells the browser where to fetch the
// runtime and open the connection.
//
// It is deliberately NOT "/live", and not the same prefix examples/chat uses
// either. The library used to default the script tag to "/live", so an
// application mounted anywhere else served a page whose script 404'd — the page
// loaded, nothing was live, and no error appeared anywhere on the server.
const MountPath = "/dashboard/live"

// LoadHTMX reads the vendored HTMX bundle and verifies its digest.
//
// The digest check is the point. This example serves somebody else's JavaScript
// to a browser, and "the file at this path" is not provenance — a bundle that
// is not the artifact this repository recorded is refused rather than served,
// which is the same rule the conformance suite applies to the same file.
func LoadHTMX(path string) ([]byte, error) {
	if path == "" {
		return nil, errors.New("dashboard: no HTMX bundle path was given")
	}
	b, err := os.ReadFile(path) //nolint:gosec // an operator-supplied path is the point of the flag
	if err != nil {
		return nil, fmt.Errorf("dashboard: could not read the HTMX bundle at %s: %w", path, err)
	}
	sum := sha256.Sum256(b)
	if got := hex.EncodeToString(sum[:]); got != HTMXSHA256 {
		return nil, fmt.Errorf(
			"dashboard: the HTMX bundle at %s has digest %s, not the recorded %s for htmx %s: "+
				"this example serves that file to a browser and will not serve bytes it cannot vouch for",
			path, got, HTMXSHA256, HTMXVersion)
	}
	return b, nil
}

// NewMux routes the whole example: the page, the stylesheet, the live handler,
// the vendored HTMX bundle, and the two plain-HTMX endpoints.
//
// It is a plain *http.ServeMux and the live handler is a plain http.Handler,
// which is the point — mounting a live application is one Handle call under
// whatever router the application already has, and the HTMX endpoints below are
// ordinary handlers that know nothing about it.
// meters may be nil, in which case no metrics route is registered — that is the
// resync measurement's case, which reads the same sink directly.
func NewMux(app *live.App[State, live.AnonymousIdentity], feed *Feed, htmx []byte, meters *Meters) *http.ServeMux {
	mux := http.NewServeMux()

	if meters != nil {
		mux.HandleFunc(MetricsRoute, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			meters.Report(w)
		})
	}

	// Both patterns are registered because MountPath is the WebSocket endpoint
	// itself and MountPath+"/" is where the client runtime is served from; the
	// handler tells them apart by path suffix, so no StripPrefix is needed.
	mux.Handle(MountPath, app.Handler())
	mux.Handle(MountPath+"/", app.Handler())

	mux.HandleFunc("/dashboard.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		http.ServeContent(w, r, "dashboard.css", time.Time{}, strings.NewReader(string(stylesheet)))
	})

	if htmx != nil {
		mux.HandleFunc(HTMXRoute, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			http.ServeContent(w, r, "htmx.min.js", time.Time{}, strings.NewReader(string(htmx)))
		})
	}

	// The two plain-HTMX endpoints. They are ordinary handlers returning
	// ordinary HTML fragments: HTMX asks for HTML over HTTP and swaps it in,
	// and gotth-live neither intercepts, cancels nor rewrites the request —
	// which is half of what FR-31 requires and is true here by construction,
	// because nothing in this function gives the live handler a chance to see
	// it.
	mux.HandleFunc("/htmx/notes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		notes := []string{
			"runbook: page the on-call after two consecutive cpu alerts",
			"note: the memory series is a simulated walk, not this machine",
		}
		if err := Notes(notes, time.Now().UTC().Format("15:04:05")).Render(r.Context(), w); err != nil {
			slog.Error("dashboard: rendering the notes fragment failed", "error", err)
		}
	})

	mux.HandleFunc("/htmx/deploys", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		deploys := []string{"api 4f21c9 — 3 h ago", "web 90ab12 — yesterday"}
		if err := Deploys(deploys, time.Now().UTC().Format("15:04:05")).Render(r.Context(), w); err != nil {
			slog.Error("dashboard: rendering the deploys fragment failed", "error", err)
		}
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")

		// The first paint is server-rendered from the same feed the sessions
		// share, using the same components the fragments render. The snapshot
		// that arrives over the WebSocket a moment later therefore morphs the
		// page to bytes it already has: no flash, no placeholder, and a page
		// that is meaningful with JavaScript disabled — it simply stops
		// updating.
		src := ""
		if htmx != nil {
			src = HTMXRoute
		}
		if err := Page(app, PageState(feed), src).Render(r.Context(), w); err != nil {
			slog.Error("dashboard: rendering the page failed", "error", err)
		}
	})

	return mux
}

// PageState projects the feed into the state the components render. It is the
// one place the HTTP path and the live path agree on what a browser that has
// not connected yet should see.
func PageState(feed *Feed) State {
	reading := feed.Reading()
	state := State{Meters: reading, Alerts: feed.Alerts()}
	if len(reading.Values) > 0 {
		state.Window = (&History{}).with(reading.Values[0])
	}
	return state
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
// README's container invocation is "-addr 0.0.0.0:8082", so without that arm
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
