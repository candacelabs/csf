package main

import (
	_ "embed"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// The benchmark's own surface: the two scripts the harness needs in the page,
// and nothing else. Everything here is specified in equivalence-spec §2.0 and
// §3.3, is present in both stacks' production builds, and has its byte cost
// counted on both sides.

// ShimRoute and ReadyRoute are the paths the page's <script> tags name. They
// match the Next.js side's paths exactly, because §4 allows the harness one
// per-stack line — the `ready` condition wiring — and a second one for a script
// URL would be a second.
const (
	ShimRoute  = "/bench/shim.js"
	ReadyRoute = "/bench/ready.js"
)

// DefaultShimPath is where harness/shim.js lives relative to this app's own
// directory, which is where `go run .` starts.
//
// The file is READ AT RUN TIME rather than copied in here, and that is the
// whole point: §2.0 requires "one file, byte-identical, served by both apps",
// and a committed copy is a second file that agrees today. The Next.js side
// keeps a gitignored copy under public/ and has `npm run verify:shim` to prove
// the two are equal; serving the original leaves nothing to verify.
const DefaultShimPath = "../../../harness/shim.js"

// readyScript is this stack's §3.3 signal. It is embedded rather than read from
// disk because it is this app's own code — the Next.js side's equivalent is
// compiled into its hydration bundle — and because a built binary should be the
// whole application.
//
//go:embed bench/ready.js
var readyScript []byte

// LoadShim reads harness/shim.js and refuses to start without it.
//
// Refusing is deliberate. A page that 404s the shim still loads, still connects
// and still repaints, so every symptom of the failure is invisible until a
// harness run reports `window.__bench.ready never became true` — twenty minutes
// later, from the other side of a container boundary. The check costs one file
// read at startup and turns that into a message naming the path.
func LoadShim(path string) ([]byte, error) {
	if path == "" {
		return nil, errors.New("counter-bench: no shim path was given; §2.0 requires the harness's shim.js to be served by both stacks")
	}
	b, err := os.ReadFile(path) //nolint:gosec // an operator-supplied path is the point of the flag
	if err != nil {
		return nil, fmt.Errorf(
			"counter-bench: could not read the benchmark shim at %s: %w\n"+
				"\tit is bench/harness/shim.js, served byte-identically by both stacks (equivalence-spec §2.0);\n"+
				"\tpass -shim <path> if this app is not being run from its own directory",
			path, err)
	}
	return b, nil
}

// serveScript serves one JavaScript asset with the headers §3.5's accounting
// assumes: a JavaScript MIME type, so the response is counted as client JS on
// this side exactly as the Next.js chunks are on the other, and no compression
// of its own — gzip level 6 is applied once, in the shared proxy container, so
// that one level is in force for both stacks (§3.5, and the Next.js side's
// `compress: false` for the same reason).
func serveScript(body []byte, name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		http.ServeContent(w, r, name, time.Time{}, strings.NewReader(string(body)))
	}
}

// serveCSS serves the one stylesheet §2.1 allows.
//
// The file beside this one is byte-identical to
// bench/apps/counter/next/src/app/counter.css, and that is asserted by a spec
// (see the stylesheet spec in counter_test.go) rather than promised in a
// comment: two stacks laid out differently are two documents, and E5's element
// bounds would be measuring different pages.
func serveCSS(body []byte, name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		http.ServeContent(w, r, name, time.Time{}, strings.NewReader(string(body)))
	}
}
