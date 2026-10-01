// Package hosting is what both widget demo hosts do the same way.
//
// A host owns five decisions the SDK will not make for it — where to listen,
// which Origins a browser may send, which palette to resolve, how to route a
// stylesheet and a page, and when to stop — and every one of them has exactly
// one right answer once the address and the palette are known. All five are
// here, and no host builds a router: two hosts writing out the same three lines
// of mux was two chances to get the mount path, the stylesheet route or the
// ordering wrong.
//
// What is deliberately not here is the interesting half: which widgets to
// register, which sources their declared streams resolve to, and what the page
// says. Two hosts disagree about all three, which is why centralising those
// would be centralising the demo rather than the plumbing.
package hosting

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/a-h/templ"

	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/widget"
)

// shutdownGrace is how long a stopping host waits for live sessions to drain
// before it closes them.
const shutdownGrace = 5 * time.Second

// BrowserOrigins turns a listen address into the Origin allowlist a browser will
// actually send.
//
// Deny by default is the live library's rule and a list is what honouring it
// costs; a request whose Origin is absent from it is refused with 403 before any
// per-session memory is allocated. 0.0.0.0 is a bind address and never an
// Origin, because no browser sends it.
func BrowserOrigins(address string) []string {
	host, port, splitError := net.SplitHostPort(address)
	if splitError != nil {
		return []string{"http://" + address}
	}
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	return []string{"http://" + net.JoinHostPort(host, port), "http://localhost:" + port}
}

// OnePalette resolves the palette every registered widget names.
//
// A page has one stylesheet, so two widgets naming two palettes is a decision a
// host cannot make for them and is reported rather than settled by taking the
// first. An unknown name is refused for the same reason a widget may not mint a
// colour: a fallback renders a plausible-looking widget in the wrong one.
func OnePalette(names ...string) (widget.Palette, error) {
	if len(names) == 0 {
		return widget.Palette{}, errors.New("hosting: no widget named a palette")
	}
	for _, name := range names[1:] {
		if name != names[0] {
			return widget.Palette{}, fmt.Errorf(
				"the registered widgets name two palettes, %q and %q", names[0], name)
		}
	}
	palette, known := widget.PaletteByName(names[0])
	if !known {
		return widget.Palette{}, fmt.Errorf("no palette named %q", names[0])
	}
	return palette, nil
}

// Regions renders every registered widget's live region from the same state and
// the same fragments the live path patches.
//
// The first paint is therefore the bytes a session's first snapshot would
// produce, so the snapshot that arrives a moment later morphs the page to markup
// it already has: no flash, no placeholder, and a page that is meaningful with
// JavaScript disabled — it simply stops updating.
func Regions(
	config live.Config[widget.HostState, live.AnonymousIdentity], state widget.HostState,
) []templ.Component {
	rendered := make([]templ.Component, 0, len(config.Fragments))
	for _, fragment := range config.Fragments {
		rendered = append(rendered, fragment.Render(state))
	}
	return rendered
}

// StylesheetPath is where every widget host serves its stylesheet.
//
// It is one constant because a host's page and its router have to agree on it,
// and a disagreement is a page that loads with no palette and renders every
// widget in the browser's default colours. A page template spells the same
// string in its <link>; that is the one place it is repeated, and it is repeated
// there because a templ attribute is markup rather than Go.
const StylesheetPath = "/widget.css"

// Site is everything a widget host serves: where the live handler is mounted,
// the palette its widgets named, its own page chrome, and its page.
//
// It is a value rather than four parameters because [Serve] takes all four and
// no host has ever wanted three of them.
type Site struct {
	// MountPath is where the live handler is mounted. The runtime script tag in
	// the host's page template must name the same path, and a disagreement is a
	// script tag that 404s and a page that loads and never updates.
	MountPath string

	// Palette is the one palette every registered widget named, already
	// resolved by [OnePalette].
	Palette widget.Palette

	// Chrome is the host's own page CSS, served after the SDK's. A host that
	// kept its own copy of the token mapping, the scene's structure and the
	// motion gate would be hand-maintaining the one projection the SDK derives.
	Chrome []byte

	// Page renders the whole document from one session's state. It is the only
	// field two hosts genuinely disagree about.
	Page func(state widget.HostState) templ.Component
}

// stylesheet is the SDK's own CSS under the palette the widget documents name,
// followed by the host's own page chrome. The order is the argument.
func stylesheet(palette widget.Palette, chrome []byte) []byte {
	return append([]byte(widget.Stylesheet(palette)), chrome...)
}

// serveStylesheet serves one already-assembled stylesheet.
func serveStylesheet(css []byte) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/css; charset=utf-8")
		http.ServeContent(writer, request, "widget.css", time.Time{}, bytes.NewReader(css))
	}
}

// Serve builds the two routes a widget host serves — its stylesheet and its
// page — mounts the live handler in front of them, and runs the result until the
// context ends.
//
// The router is built here rather than by a host, because there is exactly one
// correct arrangement of it and two hosts writing it out was two chances to get
// the mount path, the stylesheet route or the ordering wrong. Nothing is handed
// back: a composed handler returned from here would be an interface leaving a
// package that has no reason to hand one out, and the only thing a caller could
// do with it is what this function already does.
func Serve(
	ctx context.Context, address string,
	app *live.App[widget.HostState, live.AnonymousIdentity], site Site,
) error {
	pages := http.NewServeMux()
	pages.HandleFunc(StylesheetPath, serveStylesheet(stylesheet(site.Palette, site.Chrome)))
	pages.Handle("/", app.PageHandler(site.Page))
	return listen(ctx, address, app, app.Mux(site.MountPath, pages))
}

// listen runs a host until the context ends, then stops accepting connections
// and drains the live sessions.
//
// There is no WriteTimeout on the server: it would cut live connections off
// mid-session, and the library has its own per-write deadline and its own idle
// eviction.
func listen(
	ctx context.Context, address string,
	app *live.App[widget.HostState, live.AnonymousIdentity], handler http.Handler,
) error {
	server := &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	listener, listenError := net.Listen("tcp", address)
	if listenError != nil {
		return listenError
	}
	fmt.Printf("widget: http://%s\n", listener.Addr())

	serving := make(chan error, 1)
	go func() { serving <- server.Serve(listener) }()

	select {
	case serveError := <-serving:
		if errors.Is(serveError, http.ErrServerClosed) {
			return nil
		}
		return serveError
	case <-ctx.Done():
	}

	shutdown, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if shutdownError := server.Shutdown(shutdown); shutdownError != nil {
		fmt.Fprintln(os.Stderr, "widget: the HTTP server did not shut down cleanly:", shutdownError)
	}
	return app.Close(shutdown)
}
