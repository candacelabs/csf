package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

// The benchmark's own surface: the two scripts the harness needs in the page,
// §3.2's clock channel, the vendored HTMX bundle region E is driven by, and the
// one cookie region E is keyed on. Everything here is specified in
// equivalence-spec §2.0, §2.4, §3.2 and §3.3, is present in both stacks'
// production builds, and has its byte cost counted on both sides.

const (
	ShimRoute  = "/bench/shim.js"
	ReadyRoute = "/bench/ready.js"
	ClockRoute = "/api/bench/clock"

	// HTMXRoute is where this application serves the vendored bundle from.
	// Its bytes are client JS on this stack and §3.5 counts them here, which is
	// AS-3's stated price for region E.
	HTMXRoute = "/htmx.min.js"

	// PanelRoute is region E's plain-HTMX endpoint: an ordinary GET returning
	// an ordinary HTML fragment.
	PanelRoute = "/htmx/panel"
)

// PanelNodeID is the id HTMX swaps by outerHTML. It is a constant because three
// places must agree on it — the button's hx-target, the fragment's own id, and
// nothing in HTMX or in this library can check that they do.
const PanelNodeID = "dash-panel"

// DefaultShimPath is where harness/shim.js lives relative to this app's own
// directory. The file is READ AT RUN TIME rather than copied in: §2.0 requires
// "one file, byte-identical, served by both apps", and a committed copy is a
// second file that agrees today.
const DefaultShimPath = "../../../harness/shim.js"

// The vendored HTMX bundle: the same file the conformance suite pins, read from
// its path rather than copied here, with its digest checked at startup.
//
// Two copies of a vendored artifact drift and one copy with an unchecked digest
// is provenance nobody enforces. This application serves somebody else's
// JavaScript to a browser and counts its bytes against gotth-live in D1, so
// "the file at this path" is not good enough: bytes it cannot vouch for are
// refused rather than served.
const (
	DefaultHTMXPath = "../../../../test/internal/conformance/testdata/htmx-2.0.10.min.js"
	HTMXVersion     = "2.0.10"
	HTMXSHA256      = "71ea67185bfa8c98c39d31717c6fce5d852370fcdfd129db4543774d3145c0de"
)

// SIDCookie is the bench session cookie, named exactly as the Next.js side
// names it (src/lib/session.ts).
//
// It is what region E's panel is keyed on. §3.4 also defines a Next.js active
// session as a tab holding the push channel "plus its session cookie", so the
// cookie exists on both stacks for the D3 workload to mean what the spec says —
// but on this stack the live session is the connection, not the cookie, and
// nothing about authorization rests on it. There is nothing to authorize: a
// read-only operator dashboard has no accounts (see Config).
const SIDCookie = "bench_sid"

//go:embed bench/ready.js
var readyScript []byte

// LoadShim reads harness/shim.js and refuses to start without it. A page that
// 404s the shim connects and repaints and looks entirely healthy; the only
// symptom is a harness run failing on window.__bench twenty minutes later.
func LoadShim(path string) ([]byte, error) {
	if path == "" {
		return nil, errors.New("dashboard-gotth: no shim path was given; §2.0 requires the harness's shim.js to be served by both stacks")
	}
	b, err := os.ReadFile(path) //nolint:gosec // an operator-supplied path is the point of the flag
	if err != nil {
		return nil, fmt.Errorf(
			"dashboard-gotth: could not read the benchmark shim at %s: %w\n"+
				"\tit is bench/harness/shim.js, served byte-identically by both stacks (equivalence-spec §2.0);\n"+
				"\tpass -shim <path> if this app is not being run from its own directory",
			path, err)
	}
	return b, nil
}

// LoadHTMX reads the vendored HTMX bundle and verifies its digest.
//
// Unlike examples/dashboard, which degrades to a page that says HTMX is missing,
// this refuses to start. Region E is a MEASURED interaction here (DSH-6), and a
// dashboard whose refresh button silently does nothing is a run that produces no
// sample rather than a run that fails.
func LoadHTMX(path string) ([]byte, error) {
	if path == "" {
		return nil, errors.New("dashboard-gotth: no HTMX bundle path was given; §2.4 gives region E to plain HTMX on this stack")
	}
	b, err := os.ReadFile(path) //nolint:gosec // an operator-supplied path is the point of the flag
	if err != nil {
		return nil, fmt.Errorf(
			"dashboard-gotth: could not read the HTMX bundle at %s: %w\n"+
				"\tregion E is plain HTMX on this stack (§2.4, FR-62, AS-3) and DSH-6 measures it;\n"+
				"\tpass -htmx <path to htmx.min.js> if this app is not being run from its own directory",
			path, err)
	}
	sum := sha256.Sum256(b)
	if got := hex.EncodeToString(sum[:]); got != HTMXSHA256 {
		return nil, fmt.Errorf(
			"dashboard-gotth: the HTMX bundle at %s has digest %s, not the recorded %s for htmx %s: "+
				"this application serves that file to a browser and counts its bytes in D1, "+
				"and will not serve bytes it cannot vouch for",
			path, got, HTMXSHA256, HTMXVersion)
	}
	return b, nil
}

// NewSID mints a bench session id in the Next.js side's shape: 32 hex
// characters, minted on the SERVER, once per page load.
//
// Minting it in the browser would be the obvious alternative and it is not
// available to the other stack — a client-minted key is a hydration mismatch
// there — so it is not taken here either. crypto/rand.Read is documented never
// to return an error, and the page handler has no better answer than to fail if
// it somehow did.
func NewSID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("dashboard-gotth: minting a bench session id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// sidKey is the context key the live handler's wrapper puts the session id
// under.
type sidKey struct{}

// SIDFromContext reads the bench session id a connection mounts with.
//
// api-surface §3 deliberately omits Session.Request(): "values an application
// needs at mount reach Init through the context derived from the upgrade
// request". This is that path — WithSID below is one http.Handler around
// another, which is the idiomatic Go answer and costs the library no API.
func SIDFromContext(ctx context.Context) string {
	if sid, ok := ctx.Value(sidKey{}).(string); ok {
		return sid
	}
	return ""
}

// WithSID wraps the live handler so Init can see which bench session the page
// was served for.
//
// The id reaches the upgrade request as a cookie because the upgrade is to the
// mount path: the URL the browser opens the WebSocket at is the one live.Script
// rendered, and it carries nothing of the page's. The page handler sets the
// cookie, this reads it back, and Init binds region E's panel to the connection
// so the panel outlives neither more nor less than the tab does.
func WithSID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sid := ""
		if c, err := r.Cookie(SIDCookie); err == nil {
			sid = c.Value
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sidKey{}, sid)))
	})
}

// serveClock is §3.2's control channel for push interactions.
//
// "There is no local input; the causal start is the server's emission of tick
// N... estimate the offset between the server's CLOCK_MONOTONIC and the page's
// performance.now() origin with 100 NTP-style exchanges over the harness's
// control channel." This route is that channel, it is never fetched by the page,
// and its shape is the Next.js route's shape field for field — the harness runs
// one estimateClockSkew() against both stacks and §4 allows it no per-stack
// branch. DSH-7, the headline push row, is measured through it.
func serveClock(feed *Feed) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		t0, tick := feed.Clock()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"t0Ms":   t0.UnixMilli(),
			"nowMs":  time.Now().UnixMilli(),
			"tick":   tick,
			"tickMs": TickMs,
			// The Next.js route publishes process.hrtime.bigint(), which is
			// CLOCK_MONOTONIC on Linux, as a string because JSON has no 64-bit
			// integer. Go's monotonic reading is not directly readable, so the
			// same quantity is published as nanoseconds since this process's
			// own start, which is what the estimator uses it for.
			"monotonicNs":   fmt.Sprintf("%d", time.Since(processStart).Nanoseconds()),
			"fixtureSha256": feed.FixtureSHA256(),
			"pid":           os.Getpid(),
		})
	}
}

var processStart = time.Now()

func serveScript(body []byte, name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// A JavaScript MIME type, so §3.5 counts these bytes as client JS on
		// this side exactly as it counts the Next.js chunks on the other. No
		// compression of its own: gzip level 6 is applied once, in the shared
		// proxy container, so one level is in force for both stacks.
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		http.ServeContent(w, r, name, time.Time{}, strings.NewReader(string(body)))
	}
}

func serveCSS(body []byte, name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		http.ServeContent(w, r, name, time.Time{}, strings.NewReader(string(body)))
	}
}

// servePanel is region E's plain-HTMX endpoint.
//
// It is an ordinary handler that knows nothing about the live application: it
// reads the cookie, asks the feed for the next panel, and writes an HTML
// fragment. Nothing on this path touches a session, which is what makes region E
// a genuine plain-HTMX region and not a live fragment wearing hx-* attributes.
//
// It is a GET because hx-get is what "plain HTMX" renders and because the panel
// is a read of shared state. It advances a per-session counter, which a strict
// reading would put behind a POST; the counter exists so DSH-6's repaint is
// provable rather than plausible, the Next.js side advances the same counter
// from a form action, and neither is reachable cross-origin with anything but a
// same-site cookie. Stated so a reviewer does not have to decide whether it was
// noticed.
func servePanel(feed *Feed) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sid := ""
		if c, err := r.Cookie(SIDCookie); err == nil {
			sid = c.Value
		}
		if sid == "" {
			http.Error(w, "no bench session cookie", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if err := PanelText(feed.RefreshPanel(sid)).Render(r.Context(), w); err != nil {
			slog.Error("dashboard-gotth: rendering the panel fragment failed", "error", err)
		}
	}
}
