// Package htmxinterop is the compiled source for docs/guide/htmx-interop.md.
//
// It shows the two places HTMX may live on a page that also has live regions,
// and the handler that serves an HTMX swap. Neither path touches the live
// session: an hx-get is an ordinary GET to an ordinary http.Handler.
package htmxinterop

import (
	"net/http"
	"time"
)

// Notes is the application data the HTMX island fetches.
var Notes = []string{"disk replaced", "certificate renewed"}

// NotesHandler serves the fragment the island swaps in.
//
// gotth-live neither intercepts nor rewrites this request. It is a plain
// handler returning HTML over HTTP, which is what "plain HTMX" means, and it
// is registered on the application's own router beside the live handler.
func NotesHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = NotesList(Notes, time.Now().UTC().Format(time.TimeOnly)).Render(r.Context(), w)
	})
}
