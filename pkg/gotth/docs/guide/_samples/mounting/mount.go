// Package mounting is the compiled source for the two things
// docs/quickstart.md §2 explains beside its router: where the live handler is
// mounted, and where the first paint's state comes from.
//
// Both were undocumented when QA-1 built the quickstart from the docs alone
// (docs/qa/phase-4-docs-alone.md, findings F-1, F-2 and F-4), and both failures
// are silent — a page that renders correctly and is dead, or a page that serves
// the wrong number to every visitor. This package exists so the fix is compiled
// and its behaviour is asserted, rather than described in prose that nothing
// checks.
package mounting

import "net/http"

// Routes mounts one live application and one page handler on a net/http mux.
//
// Two patterns for the live handler, and the pair is not redundant: the first
// is the WebSocket endpoint itself, at exactly that path, and the second is the
// subtree the client runtime is served from. Delete either and the failure is
// silent — an upgrade that ServeMux redirects instead of upgrading, or a
// runtime URL that the catch-all answers with the HTML page.
//
// No http.StripPrefix anywhere, and none is needed: the handler routes by path
// SUFFIX, so it works at any prefix and never has to be told what its own
// prefix is. Stripping is worse than unnecessary on the exact pattern — it
// leaves the empty path, and the handler's own mux answers that with a
// redirect the WebSocket client cannot follow.
func Routes(mountPath string, app, page http.Handler) http.Handler {
	mux := http.NewServeMux()
	// The WebSocket upgrade, at exactly this path.
	mux.Handle(mountPath, app)
	// The subtree: gotth-live.min.js, and the dev-only routes.
	mux.Handle(mountPath+"/", app)
	// The catch-all: the page, and every path neither pattern above claims.
	mux.Handle("/", page)
	return mux
}
