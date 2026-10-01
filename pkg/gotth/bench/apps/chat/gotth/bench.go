package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// The benchmark's own surface: the two scripts the harness needs in the page,
// §3.2's clock channel, and the two cookies §3.4 and F-CHT-9 need. Everything
// here is specified in equivalence-spec §2.0, §3.2 and §3.3, is present in both
// stacks' production builds, and has its byte cost counted on both sides.

const (
	ShimRoute  = "/bench/shim.js"
	ReadyRoute = "/bench/ready.js"
	ClockRoute = "/api/bench/clock"
)

// DefaultShimPath is where harness/shim.js lives relative to this app's own
// directory. The file is READ AT RUN TIME rather than copied in: §2.0 requires
// "one file, byte-identical, served by both apps", and a committed copy is a
// second file that agrees today.
const DefaultShimPath = "../../../harness/shim.js"

//go:embed bench/ready.js
var readyScript []byte

// The two cookies, both named exactly as the Next.js side names them
// (src/lib/session.ts), because the harness sets one of them itself: CHT-8 does
// `Network.setCookie{name: "bench_who", value: "readonly"}` and then reloads.
//
// bench_who is an identity CLAIM with no authentication behind it, which is
// correct for a bench app and is stated here so a reviewer does not read it as
// an auth bug: the only thing the name can do is make the server refuse MORE,
// never less. PRODUCTION replaces DirectoryAuthenticate with the session cookie
// or bearer token the application already trusts.
const (
	WhoCookie  = "bench_who"
	RoomCookie = "bench_room"
)

// LoadShim reads harness/shim.js and refuses to start without it. A page that
// 404s the shim connects and repaints and looks entirely healthy; the only
// symptom is a harness run failing on window.__bench twenty minutes later.
func LoadShim(path string) ([]byte, error) {
	if path == "" {
		return nil, errors.New("chat-gotth: no shim path was given; §2.0 requires the harness's shim.js to be served by both stacks")
	}
	b, err := os.ReadFile(path) //nolint:gosec // an operator-supplied path is the point of the flag
	if err != nil {
		return nil, fmt.Errorf(
			"chat-gotth: could not read the benchmark shim at %s: %w\n"+
				"\tit is bench/harness/shim.js, served byte-identically by both stacks (equivalence-spec §2.0);\n"+
				"\tpass -shim <path> if this app is not being run from its own directory",
			path, err)
	}
	return b, nil
}

// NormalizeName is the Next.js side's normalizeName, transcribed: short,
// printable, never empty. The character class is also what makes the roster's
// comma-joined wire form unambiguous — a name cannot contain a comma.
func NormalizeName(raw string) string {
	name := strings.TrimSpace(raw)
	if len(name) > 24 {
		name = name[:24]
	}
	if name == "" {
		return DefaultName
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-'
		if !ok {
			return DefaultName
		}
	}
	return name
}

// DirectoryAuthenticate derives the session identity from the upgrade request.
//
// It is Config.Authenticate and it is a real one: live.Anonymous would make
// every tab the same subject, and F-CHT-9 needs a name the room can refuse. The
// check runs on the upgrade REQUEST, before any per-session memory is
// allocated, so a rejection is an HTTP status on the handshake rather than a
// close code.
func DirectoryAuthenticate(r *http.Request) (Member, error) {
	name := DefaultName
	if c, err := r.Cookie(WhoCookie); err == nil {
		name = NormalizeName(c.Value)
	}
	return Member{Name: name, Readonly: name == ReadonlyName}, nil
}

// roomKey is the context key the live handler's wrapper puts the requested room
// under.
type roomKey struct{}

// RoomFromContext reads the room a session mounts into.
//
// api-surface §3 deliberately omits Session.Request(): "values an application
// needs at mount reach Init through the context derived from the upgrade
// request". This is that path — the wrapper below is one http.Handler around
// another, which is the idiomatic Go answer and costs the library no API.
func RoomFromContext(ctx context.Context) string {
	if room, ok := ctx.Value(roomKey{}).(string); ok && RoomIndex(room) >= 0 {
		return room
	}
	return RoomIDs[0]
}

// WithRoom wraps the live handler so Init can see which room the page was
// served for.
//
// The room reaches the upgrade request as a cookie because the upgrade is to
// the mount path and not to /chat/<room>: the URL the browser opens the
// WebSocket at is the one live.Script rendered, and it carries no room. The page
// handler sets the cookie, this reads it back.
func WithRoom(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		room := RoomIDs[0]
		if c, err := r.Cookie(RoomCookie); err == nil && RoomIndex(c.Value) >= 0 {
			room = c.Value
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), roomKey{}, room)))
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
// branch.
func serveClock(rooms *Rooms) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		t0, tick := rooms.Clock()
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
			"fixtureSha256": rooms.FixtureSHA256(),
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
