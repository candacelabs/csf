package live

import (
	"bytes"
	"errors"
	"net/http"
	"strconv"

	"github.com/a-h/templ"

	"github.com/candacelabs/csf/pkg/gotth/internal/obs"
)

// PageHandler returns the http.Handler that serves the first paint: on every
// request it loads state through [Config.Init] and renders page from it.
//
// It exists because the obvious spelling is wrong in a way nothing reports.
// templ.Handler(Page(State{})) builds the component when the argument is
// evaluated — once, at start-up — and serves those bytes to every visitor for
// the life of the process. That is correct exactly while Init returns the zero
// value too, and it stops being correct, silently, the moment Init reads a row,
// a cookie or a feature flag: every response then carries the zero state, the
// browser is corrected only once the WebSocket connects, and with JavaScript
// disabled it is never corrected at all. This handler cannot be given a state
// value, only the function that renders one, so that mistake is not expressible
// through it.
//
// # What it does per request, in order
//
//  1. [Config.Authenticate] derives the identity from the page request.
//  2. [Config.Init] is called with the request's context and a [Session]
//     carrying that identity.
//  3. page renders the state Init returned, into a buffer, which is then
//     written with Content-Type text/html; charset=utf-8.
//
// So the page and the session's first snapshot come from one function, and an
// application that later gives Init something real to do gets a correct first
// paint from the same edit. It is the rule at
// docs/guide/fragments-and-dirty-tracking.md — the page and the fragments
// render the same components, from the same state — held by the type rather
// than by care.
//
// # Init is called once per page request as well as once per session
//
// That is the trade this handler makes, and it is worth stating rather than
// discovering. Init is a loader: it produces state and RETURNS effects as
// values for the library to perform later, so calling it here performs none of
// them — the effects it returns on a page render are discarded, and the
// session's own Init call is what schedules them. What does run twice is
// whatever Init does to produce the state, which for a loader is a read. An
// Init that is not safe to call for a read should not be mounted here; give
// this handler an application whose Init loads, and put anything else in the
// startup effects, which is what they are for.
//
// # The Session a page render sees
//
// [Session.Identity] is the identity Authenticate derived from this request,
// because the page must be painted for the identity the socket will bind to.
// [Session.ID] is the zero [ID]: no session exists yet, and one cannot, because
// a session is minted at the handshake and this is a different request. An Init
// that needs to tell the two calls apart compares against the zero ID.
//
// # What it answers when a step fails
//
// A failure renders no page, because half a document is worse than none:
//
//   - Authenticate returning an error, or no identity, is 401 — the same
//     status the upgrade gives that visitor. A page whose socket is going to be
//     refused is a page that cannot work, so serving it would be the silent
//     failure this library exists to remove rather than a kindness. An
//     application that wants an unauthenticated visitor to get a page wants
//     Authenticate to return an anonymous identity rather than an error, which
//     is also what makes their upgrade succeed.
//   - Init returning an error, or the render failing, is 500.
//
// The body is a fixed generic message and the detail goes to [Config.Logger] at
// error level, on the same rule error frames follow: the browser is not where a
// server-side failure is explained. With [Config.Dev] set the detail is in the
// body as well.
//
// It answers any method, as a page handler mounted on a catch-all must, and
// writes no body for HEAD.
func (a *App[S, I]) PageHandler(page func(state S) templ.Component) http.Handler {
	if page == nil {
		panic("gotth-live: (*live.App).PageHandler was given a nil page: pass the function that " +
			"renders the whole document from state, such as Page")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// No nil test on the identity: the hook's result type is the
		// application's own since 2026-09-03, so "no identity and no error" is
		// not a value it can return. See wsx's upgrade path, which lost the same
		// check for the same reason.
		identity, err := a.cfg.Authenticate(r)
		if err != nil {
			a.logger.Warn(ctx, "gotth-live: refused a page render because Config.Authenticate refused the request: "+
				"the upgrade from this visitor would be refused too", obs.Err(err))
			a.pageError(w, http.StatusUnauthorized, "unauthenticated", err)
			return
		}

		// The effects are discarded deliberately: they belong to the session,
		// and the session's own Init call is what schedules them. See this
		// method's godoc.
		state, _, err := a.cfg.Init(ctx, Session[I]{identity: identity})
		if err != nil {
			a.logger.Error(ctx, "gotth-live: Config.Init failed on a page render, so no page was served",
				obs.Err(err))
			a.pageError(w, http.StatusInternalServerError, "cannot render the page", err)
			return
		}

		component := page(state)
		if component == nil {
			err := errNilPage
			a.logger.Error(ctx, "gotth-live: the page function rendered no component, so no page was served",
				obs.Err(err))
			a.pageError(w, http.StatusInternalServerError, "cannot render the page", err)
			return
		}

		// Rendered to a buffer before anything is written, so a render that
		// fails half way through produces a 500 rather than a 200 carrying a
		// truncated document. live.Script is the failure this actually catches:
		// it returns an error and emits no tag for a mount path a browser would
		// not read as a path, and that error surfaces here.
		var buf bytes.Buffer
		if err := component.Render(ctx, &buf); err != nil {
			a.logger.Error(ctx, "gotth-live: the page render failed, so no page was served", obs.Err(err))
			a.pageError(w, http.StatusInternalServerError, "cannot render the page", err)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
		if r.Method == http.MethodHead {
			return
		}
		_, _ = buf.WriteTo(w)
	})
}

// errNilPage is the one failure of a page render the library authors itself
// rather than receiving from application code. It is a package-level value so
// that the log line and the dev-mode body say the same sentence, and an ordinary
// errors.New so that FR-58's census counts it: a message a human wrote is a
// message the audit grades.
//
// It names no session, because none exists on a page request — that is what
// PageHandler's zero session id is about — and no causal identifier for the
// same reason. It names the next step.
//
// It had a sibling, errNoIdentity, until 2026-09-03: "Config.Authenticate
// returned no identity and no error". Config.Authenticate returns the
// application's OWN identity type now, so `nil, nil` is not a result it can
// produce and the branch that error answered is unreachable. internal/arch's
// census records the removal.
var errNilPage = errors.New("gotth-live: the page function returned no component on a page request: " +
	"return a templ component rather than nil — an empty one for the state that has nothing to show")

// pageError answers a failed page render.
//
// The detail reaches the browser only with Config.Dev set, which is the rule an
// Error frame already follows and for the same reason: an application's own
// loader error may carry a connection string, a query or an internal hostname,
// and the page request is not where an operator reads a server-side failure.
// Production gets the generic message and the log line gets the error.
func (a *App[S, I]) pageError(w http.ResponseWriter, status int, generic string, err error) {
	if a.cfg.Dev && err != nil {
		http.Error(w, generic+": "+err.Error(), status)
		return
	}
	http.Error(w, generic, status)
}

// Mux returns an http.ServeMux with this application and page mounted on it:
// the WebSocket upgrade at exactly mountPath, the client runtime and the
// dev-only routes on the subtree under it, and page on the catch-all.
//
// It is the whole routing of a single-application server in one call, and it
// exists because writing those three registrations by hand has two silent
// failure modes and both of them are measured in docs/quickstart.md §2:
//
//   - Registering only mountPath and not mountPath+"/" leaves the runtime's URL
//     to the catch-all, which answers it with the page. The browser gets 200
//     text/html, hands a document to its JavaScript parser, and never attempts
//     a WebSocket. There is no 404 and no server-side error anywhere; the only
//     evidence is one SyntaxError in the browser console.
//   - Wrapping the handler in http.StripPrefix, the repair a reader reaches for
//     next, turns the upgrade into a 307 to the trailing-slash form — and a
//     WebSocket client cannot follow a redirect on a handshake, so the page
//     reconnects forever.
//
// Neither is expressible through this method. [App.Handler] is still the way to
// mount on a router of your own, and it needs no http.StripPrefix there either;
// docs/guide/_samples/mounting is the same three registrations written out.
//
// mountPath is the prefix as the BROWSER sees it, and it is the same string
// [Script] must be given — this method routes with it, but the tag renders on a
// different request, so nothing here can check that the two agree.
//
// It panics rather than returning an error, on the precedent of the
// http.ServeMux method it calls: a mount path is a constant in the caller's
// source, so a bad one is a startup mistake in a literal rather than a
// condition a running server can be in. It panics when page is nil, when
// mountPath is not a path [Script] would accept — empty, not beginning with
// "/", or containing "//" anywhere, "\", "?", "#", or a control byte — and when
// mountPath is "/", which would put the upgrade and the page on one pattern and
// leave no route for either.
func (a *App[S, I]) Mux(mountPath string, page http.Handler) http.Handler {
	if page == nil {
		panic("gotth-live: (*live.App).Mux was given a nil page handler: pass the handler that serves " +
			"your page, such as app.PageHandler(Page)")
	}
	mount, err := normalizeMountFor("(*live.App).Mux", mountPath)
	if err != nil {
		panic(err)
	}
	if mount == "/" {
		panic(`gotth-live: (*live.App).Mux cannot mount an application at "/": the upgrade and the page ` +
			`would be the same pattern. Mount the application under a prefix of its own, such as "/live", ` +
			"and give this method the page for everything else")
	}

	mux := http.NewServeMux()
	// The exact pattern is the upgrade and the subtree is the assets. Both are
	// this application's own handler, which routes by path suffix and is
	// deliberately not told the prefix.
	mux.Handle(mount, a.routeHandler)
	mux.Handle(mount+"/", a.routeHandler)
	// The catch-all: the page, and every path the two patterns above do not
	// claim — /favicon.ico included.
	mux.Handle("/", page)
	return mux
}
