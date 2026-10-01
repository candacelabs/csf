package live

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/a-h/templ"
)

// clientDevReloadFile is the dev-reload client, served and pointed at only
// when Config.Dev is set. It is a THIRD artifact for the reason the inspector
// is a second one: FR-57 is a development-time convenience and NFR-2's
// 12,288-byte ceiling is a production budget, so not one byte of this may be
// spent inside the shipped runtime. client/SIZE.md §2.2 carries the
// measurement and tools/minify holds it to its own ceiling.
const clientDevReloadFile = "gotth-live-dev-reload.min.js"

// devBuildRoute is the path suffix the dev-reload client polls for the
// identity of the running build.
//
// It carries no file extension on purpose. Both JavaScript artifacts are
// routed by ".min.js" suffix, and a fourth name in that family would be a
// fourth chance for the suffix tests to overlap; this one cannot collide with
// any of them however they are renamed.
const devBuildRoute = "gotth-live-dev-build"

// maxDevBuildID bounds Config.DevBuildID.
//
// The value is written into an HTML attribute and returned as an HTTP body, so
// the bound is not a formality: client/dev-reload.js refuses a body longer
// than this rather than treating it as a build identity, because a long body
// on this route is far more likely to be a proxy's error page than a build.
// The two numbers are the same number, and this is the one a developer can
// see fail at startup.
const maxDevBuildID = 128

// clientDevReload is the minified dev-reload client, embedded by exact
// filename for the same reason the other two are.
//
// Embedding is unconditional and SERVING is not, exactly as for the inspector
// — see clientInspector, which makes the whole argument. A production binary
// carries these bytes and hands them to nobody.
//
//go:embed clientjs/gotth-live-dev-reload.min.js
var clientDevReload []byte

// clientDevReloadETag is computed once, at init, like the others'.
var clientDevReloadETag = etagOf(clientDevReload)

// DevReloadScript renders the script tag for the dev-reload client, for an
// application whose handler is mounted at mountPath.
//
// It is the browser half of FR-57: after a Go or templ change is rebuilt and
// the process restarts, the page in front of the developer reloads itself.
// docs/guide/dev-reload.md is the user-facing page.
//
// # What it does, and what the runtime already does
//
// A Go change and a templ change are one event — templ generates Go — so both
// mean a rebuild and a restart. The socket drops either way, and the client
// runtime's reconnect-and-resync brings the live regions back on its own; that
// needs nothing from this tag, and it is why restarting the SAME binary does
// not reload anything here.
//
// What a resync cannot repaint is everything outside a live fragment: the page
// shell, the head, a fragment whose markup changed while its state did not and
// which therefore produced no patch. After a rebuild that markup came from a
// process that no longer exists. This tag polls the build identity below and
// reloads the document when it changes, which is the only thing that fixes it.
//
// # What it does NOT preserve
//
// Server-held session state does not survive the process that held it. The
// reconnect mounts a NEW session against the new build, so Config.Init runs
// again and the session's state is whatever Init produces. That is not
// something this tag could preserve and it does not pretend to: "without
// losing the session where state permits" means the browser re-establishes
// itself with no manual refresh, not that a restarted process remembers.
// Application state kept outside the session — the counter example's store is
// the one to look at — survives exactly as far as its own lifetime allows,
// which for an in-process store is not at all.
//
// # It renders nothing unless Config.Dev is set
//
// With Dev false — the zero value, and what production must run — this writes
// zero bytes and returns nil, the route serving its JavaScript answers 404,
// and so does the build-identity route. Three gates on one switch, all three
// tested in both positions. A production page therefore names no dev asset,
// exposes no build identity, and makes no polling request.
//
// # Order does not matter here
//
// Unlike (*App[S, I]).InspectorScript, this tag may go anywhere: it wraps
// nothing, reads nothing the runtime owns, and talks only to its own route
// over HTTP. Putting all three tags together, inspector first and this one
// last, is the arrangement the guide shows, and only the inspector's position
// in it is load-bearing.
//
// mountPath is validated exactly as Script validates it, by the same function,
// and for the same reason: the prefix as the browser sees it is knowledge only
// the caller has.
func (a *App[S, I]) DevReloadScript(mountPath string) templ.Component {
	return templ.ComponentFunc(func(_ context.Context, w io.Writer) error {
		// Validated even when nothing is written, so a bad mount path is an
		// error in dev and in production alike — the argument InspectorScript
		// makes at length, and the same function makes the decision.
		mount, err := normalizeMount(mountPath)
		if err != nil {
			return err
		}
		if !a.cfg.Dev {
			return nil
		}
		src := mount + "/" + clientDevReloadFile
		if mount == "/" {
			src = mount + clientDevReloadFile
		}
		// The build identity is written into the tag rather than fetched by
		// the client at boot, and that is the correctness of the feature
		// rather than a saved round trip: this is the identity of the build
		// that rendered THIS document. A client that adopted its first fetched
		// value as the baseline would accept a rebuild that landed while the
		// page was loading as "what this page is showing", and never reload.
		_, err = io.WriteString(w,
			`<script src="`+templ.EscapeString(src)+`" `+
				attrDevURL+`="`+templ.EscapeString(mount)+`" `+
				attrDevBuild+`="`+templ.EscapeString(a.devBuildID())+`" defer></script>`)
		return err
	})
}

// devBuildID is the identity of the running build.
//
// Config.DevBuildID wins when it is set. Otherwise it is derived, once per
// process, from the bytes of the running executable — see devBuildID's
// package-level helper below for what that buys and what it costs.
func (a *App[S, I]) devBuildID() string {
	if a.cfg.DevBuildID != "" {
		return a.cfg.DevBuildID
	}
	a.buildOnce.Do(func() { a.buildID = executableBuildID() })
	return a.buildID
}

// executableBuildID hashes the running executable.
//
// # Why the executable, and not a per-process random value
//
// Both change across a rebuild, which is all FR-57 strictly needs. The hash
// additionally does NOT change across a restart that rebuilt nothing, and that
// difference is the whole of "without losing the session where state permits":
// a crash-and-restart, a `docker compose restart`, or a rebuild of source that
// did not actually change all leave the identity alone, so the page does not
// reload and the client runtime's own reconnect restores it in place. Go
// builds are reproducible, so "did not actually change" is a property the
// toolchain gives for free here.
//
// It costs one streaming SHA-256 of the binary, taken lazily on the first poll
// rather than at startup, once per process, in dev only. A 40 MB development
// binary is a few tens of milliseconds on the first request and nothing
// afterwards.
//
// # The fallback, and why it is visibly named
//
// If the executable cannot be found or read — a binary deleted out from under
// a running process, a platform where os.Executable is unsupported — the
// identity becomes a random per-process value prefixed "process-". That is the
// safe direction: every restart then looks like a new build and reloads the
// page, which is a page that reloads more often than it must rather than one
// that shows stale markup. The prefix is there so a developer reading
// docs/guide/dev-reload.md can tell which of the two they are getting.
func executableBuildID() string {
	if path, err := os.Executable(); err == nil {
		if f, err := os.Open(path); err == nil {
			defer f.Close()
			sum := sha256.New()
			if _, err := io.Copy(sum, f); err == nil {
				return hex.EncodeToString(sum.Sum(nil)[:12])
			}
		}
	}
	var b [12]byte
	if _, err := rand.Read(b[:]); err == nil {
		return "process-" + hex.EncodeToString(b[:])
	}
	// rand.Read does not return an error on any supported platform; the arm
	// exists so that this function has no path that returns "".  An empty
	// identity would be rendered into the tag as an empty attribute, which
	// client/dev-reload.js reads as "no baseline" and quietly stops watching.
	return "process-unavailable"
}

// serveClientDevReload serves the embedded dev-reload client.
//
// Reached only from the Dev arm of routes(), and served with the same
// immutable caching as the other two artifacts: it changes only when the
// binary does, and a developer reloading fifty times should not fetch it
// fifty times.
func serveClientDevReload(w http.ResponseWriter, r *http.Request) {
	serveAsset(w, r, clientDevReload, clientDevReloadETag)
}

// serveDevBuild answers with the identity of the running build.
//
// It is the one route in this library that must never be cached, and it is
// deliberately not served through serveAsset: that function's whole job is a
// strong ETag and a year of immutability, which is the exact opposite of what
// this answer needs. A conditional request is not offered either — the answer
// is 12 bytes, and a 304 that a proxy decided to satisfy from a store is a
// page that never reloads.
func (a *App[S, I]) serveDevBuild(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := a.devBuildID()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.Itoa(len(id)))
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.WriteString(w, id)
}

// validateDevBuildID checks a Config.DevBuildID a caller set.
//
// It is checked whatever Dev is set to, for the reason InspectorScript
// validates its mount path in both modes: a field that is only checked in one
// of them is a field that starts failing on the deploy that flips the switch.
func validateDevBuildID(id string) error {
	if id == "" {
		return nil
	}
	if len(id) > maxDevBuildID {
		return &ConfigError{
			Field: "DevBuildID",
			Detail: "the value is " + strconv.Itoa(len(id)) + " bytes and the limit is " +
				strconv.Itoa(maxDevBuildID) + ": it is rendered into a script tag and returned as the " +
				"whole body of the dev-reload poll, and the client refuses a longer answer rather than " +
				"treating it as a build identity — a short opaque token such as a commit hash is what fits",
		}
	}
	for i := 0; i < len(id); i++ {
		if b := id[i]; b < 0x20 || b == 0x7f {
			return &ConfigError{
				Field: "DevBuildID",
				Detail: "the value contains the control byte " + strconv.QuoteRune(rune(b)) +
					": it is returned as a single-line HTTP body and compared verbatim in the browser, " +
					"so a newline or a tab in it makes the comparison depend on how a proxy rewrote it",
			}
		}
	}
	if strings.TrimSpace(id) != id {
		return &ConfigError{
			Field: "DevBuildID",
			Detail: "the value has leading or trailing whitespace: the client trims the body it " +
				"receives before comparing, so this identity can never equal itself — trim it here",
		}
	}
	return nil
}
