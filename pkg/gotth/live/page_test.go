package live_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/a-h/templ"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/live"
)

// pageState is the state of the applications below. Its zero value is
// distinguishable from every loaded value they use, which is the property the
// whole file turns on: F-4 was a first paint that served the zero value while
// the session served something else, so a spec whose loaded state IS the zero
// value could not tell the fix from the defect.
type pageState struct{ N int }

// pageOf renders one state as bytes a spec can compare exactly.
func pageOf(s pageState) templ.Component {
	return text("<output>%d</output>", s.N)
}

// pageApp is an application whose Init loads from a counter the spec controls,
// so "how many times was the loader called, and with what identity" is
// observable.
type pageApp struct {
	app *live.App[pageState, user]

	loaded    int
	loadedAs  []string
	loadValue int
	loadErr   error

	authIdentity user
	authErr      error
}

func newPageApp(dev bool) *pageApp {
	p := &pageApp{loadValue: 41, authIdentity: user("tester")}
	cfg := live.Config[pageState, user]{
		Init: func(_ context.Context, s live.Session[user]) (pageState, []live.Effect[user], error) {
			p.loaded++
			// No nil test: the session is typed by the identity the hook
			// produced, so Identity() is a value rather than a possibly-nil
			// interface.
			p.loadedAs = append(p.loadedAs, s.Identity().Subject())
			if p.loadErr != nil {
				return pageState{}, nil, p.loadErr
			}
			return pageState{N: p.loadValue}, nil, nil
		},
		Reduce: func(s pageState, ev live.Event) (pageState, []live.Effect[user]) {
			if ev.Name == "count.inc" {
				s.N++
			}
			return s, nil
		},
		Fragments:    []live.Fragment[pageState]{{ID: "count", Render: pageOf}},
		Events:       []string{"count.inc"},
		Origins:      []string{"https://app.example"},
		Authenticate: func(request *http.Request) (user, error) { return p.authIdentity, p.authErr },
		Authorize:    live.AllowAll[user],
		CSRF:         live.NoCSRFCheck,
		Dev:          dev,
	}
	p.app = live.MustNew(cfg)
	return p
}

func (p *pageApp) get(handler http.Handler, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

var _ = Describe("(*App).PageHandler", func() {
	// The reason this method exists, stated as the assertion that fails
	// without it. QA-1's F-4: templ.Handler(Page(State{})) freezes a state
	// VALUE at registration, so an Init that loads anything at all is
	// contradicted by every first paint, silently, for the life of the
	// process.
	It("paints the state the mount hook loads, not the zero value", func() {
		p := newPageApp(false)

		rec := p.get(p.app.PageHandler(pageOf), http.MethodGet, "/")

		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.String()).To(Equal("<output>41</output>"))
		Expect(rec.Header().Get("Content-Type")).To(Equal("text/html; charset=utf-8"))
	})

	// The other half of the same defect, and the half a single request cannot
	// see: the frozen handler is wrong because it stops asking. A loader whose
	// answer changes between two requests must change the page between them.
	It("re-loads on every request rather than freezing the first answer", func() {
		p := newPageApp(false)
		handler := p.app.PageHandler(pageOf)

		first := p.get(handler, http.MethodGet, "/")
		p.loadValue = 42
		second := p.get(handler, http.MethodGet, "/")

		Expect(first.Body.String()).To(Equal("<output>41</output>"))
		Expect(second.Body.String()).To(Equal("<output>42</output>"))
		Expect(p.loaded).To(Equal(2))
	})

	It("gives the mount hook the identity Config.Authenticate derived from the page request", func() {
		p := newPageApp(false)
		p.authIdentity = user("alice")

		p.get(p.app.PageHandler(pageOf), http.MethodGet, "/")

		Expect(p.loadedAs).To(Equal([]string{"alice"}))
	})

	// The session identifier is the one thing a page render cannot have, and
	// the godoc says so rather than leaving an application to find out. A
	// session is minted at the handshake; this is a different request.
	It("gives the mount hook the zero session identifier, because no session exists yet", func() {
		var seen live.ID
		app := live.MustNew(live.Config[pageState, user]{
			Init: func(_ context.Context, s live.Session[user]) (pageState, []live.Effect[user], error) {
				seen = s.ID()
				return pageState{}, nil, nil
			},
			Reduce:       func(s pageState, _ live.Event) (pageState, []live.Effect[user]) { return s, nil },
			Fragments:    []live.Fragment[pageState]{{ID: "count", Render: pageOf}},
			Events:       []string{"count.inc"},
			Origins:      []string{"https://app.example"},
			Authenticate: func(request *http.Request) (user, error) { return user("tester"), nil },
			Authorize:    live.AllowAll[user],
			CSRF:         live.NoCSRFCheck,
		})

		app.PageHandler(pageOf).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

		Expect(seen).To(Equal(live.ID{}))
	})

	// The effects belong to the session. Performing them here would run a
	// mount-time subscription once per page load and leave every one of them
	// with no Teardown, because there is no session to tear down.
	It("discards the startup effects the mount hook returns", func() {
		executed := 0
		count := func(_ context.Context, _ live.Session[user], _ live.Emitter) error {
			executed++
			return nil
		}
		app := live.MustNew(live.Config[pageState, user]{
			Init: func(ctx context.Context, session live.Session[user]) (pageState, []live.Effect[user], error) {
				return pageState{N: 7}, []live.Effect[user]{logEffect(count)}, nil
			},
			Reduce:       func(s pageState, _ live.Event) (pageState, []live.Effect[user]) { return s, nil },
			Fragments:    []live.Fragment[pageState]{{ID: "count", Render: pageOf}},
			Events:       []string{"count.inc"},
			Origins:      []string{"https://app.example"},
			Authenticate: func(request *http.Request) (user, error) { return user("tester"), nil },
			Authorize:    live.AllowAll[user],
			CSRF:         live.NoCSRFCheck,
		})

		rec := httptest.NewRecorder()
		app.PageHandler(pageOf).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

		Expect(rec.Body.String()).To(Equal("<output>7</output>"))
		Expect(executed).To(BeZero())
	})

	// A visitor Authenticate refuses is a visitor whose upgrade would be
	// refused with the same 401. Serving them a live page that can never
	// connect is the silent failure this library is built to remove.
	It("refuses the page with 401 when Config.Authenticate refuses the request", func() {
		p := newPageApp(false)
		p.authErr = errors.New("no session cookie")

		rec := p.get(p.app.PageHandler(pageOf), http.MethodGet, "/")

		Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		Expect(rec.Body.String()).To(ContainSubstring("unauthenticated"))
		Expect(p.loaded).To(BeZero())
	})

	// The "no identity and no error" spec is gone with the value it needed.
	// Config.Authenticate returns the application's OWN identity type since
	// 2026-09-03, so `nil, nil` is not a result it can produce and the 401 this
	// exercised is unreachable. The error that answered it left the library in
	// the same commit; internal/arch's FR-58 census records the removal.

	It("answers 500 and renders no page when the mount hook fails", func() {
		p := newPageApp(false)
		p.loadErr = errors.New("the rows are on fire")

		rec := p.get(p.app.PageHandler(pageOf), http.MethodGet, "/")

		Expect(rec.Code).To(Equal(http.StatusInternalServerError))
		Expect(rec.Body.String()).NotTo(ContainSubstring("<output>"))
	})

	// Production says nothing about why. A loader's error may carry a
	// connection string, a query or an internal hostname, which is the same
	// argument the Error frame's fixed production message rests on.
	It("keeps the loader's error out of the body in production and puts it there in dev", func() {
		prod := newPageApp(false)
		prod.loadErr = errors.New("dial tcp 10.0.0.4:5432: refused")
		dev := newPageApp(true)
		dev.loadErr = errors.New("dial tcp 10.0.0.4:5432: refused")

		prodBody := prod.get(prod.app.PageHandler(pageOf), http.MethodGet, "/").Body.String()
		devBody := dev.get(dev.app.PageHandler(pageOf), http.MethodGet, "/").Body.String()

		Expect(prodBody).NotTo(ContainSubstring("10.0.0.4"))
		Expect(prodBody).To(ContainSubstring("cannot render the page"))
		Expect(devBody).To(ContainSubstring("10.0.0.4"))
	})

	// Buffered, so a render that dies half way through is a 500 rather than a
	// 200 carrying a truncated document. live.Script with a mount path a
	// browser would not read as a path is the reachable version of this.
	It("answers 500 with no partial document when the page render fails part-way", func() {
		p := newPageApp(false)
		half := func(state pageState) templ.Component {
			return templ.ComponentFunc(func(_ context.Context, w io.Writer) error {
				if _, err := io.WriteString(w, "<html><body>the first half"); err != nil {
					return err
				}
				return errors.New("live.Script refused the mount path")
			})
		}

		rec := p.get(p.app.PageHandler(half), http.MethodGet, "/")

		Expect(rec.Code).To(Equal(http.StatusInternalServerError))
		Expect(rec.Body.String()).NotTo(ContainSubstring("the first half"))
	})

	It("answers 500 when the page function returns no component", func() {
		p := newPageApp(false)

		rec := p.get(p.app.PageHandler(func(state pageState) templ.Component { return nil }), http.MethodGet, "/")

		Expect(rec.Code).To(Equal(http.StatusInternalServerError))
	})

	It("answers HEAD with the length and no body", func() {
		p := newPageApp(false)

		rec := p.get(p.app.PageHandler(pageOf), http.MethodHead, "/")

		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.Len()).To(BeZero())
		Expect(rec.Header().Get("Content-Length")).To(Equal("19"))
	})

	It("panics at construction rather than per request when the page function is nil", func() {
		p := newPageApp(false)

		Expect(func() { p.app.PageHandler(nil) }).To(PanicWith(ContainSubstring("nil page")))
	})
})

var _ = Describe("(*App).Mux", func() {
	const mount = "/live"

	// The registration a reader forgets, and the reason the failure is silent:
	// with only the exact pattern registered, the catch-all answers the
	// runtime's URL with the page — 200, text/html, and no WebSocket is ever
	// attempted.
	It("serves the client runtime from under the mount, not the page", func() {
		p := newPageApp(false)

		rec := p.get(p.app.Mux(mount, p.app.PageHandler(pageOf)), http.MethodGet, mount+"/gotth-live.min.js")

		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Header().Get("Content-Type")).To(HavePrefix("text/javascript"))
	})

	It("serves the page from the catch-all, for a path neither live pattern claims", func() {
		p := newPageApp(false)
		mux := p.app.Mux(mount, p.app.PageHandler(pageOf))

		Expect(p.get(mux, http.MethodGet, "/").Body.String()).To(Equal("<output>41</output>"))
		Expect(p.get(mux, http.MethodGet, "/favicon.ico").Code).To(Equal(http.StatusOK))
	})

	// The upgrade lands on the live handler at exactly the mount path. A
	// plain GET there is not a handshake, so what this asserts is that the
	// request reached the live handler at all rather than the page: the
	// handler refuses it, and the page would have rendered.
	It("routes the exact mount path to the live handler rather than to the page", func() {
		p := newPageApp(false)

		rec := p.get(p.app.Mux(mount, p.app.PageHandler(pageOf)), http.MethodGet, mount)

		Expect(rec.Body.String()).NotTo(ContainSubstring("<output>"))
		Expect(rec.Code).To(BeNumerically(">=", http.StatusBadRequest))
	})

	// No redirect, at any prefix. http.StripPrefix is the repair a reader
	// reaches for after forgetting the subtree registration, and it turns the
	// upgrade into a 307 a WebSocket client cannot follow.
	It("answers the exact mount path without a redirect, at a nested prefix too", func() {
		p := newPageApp(false)

		for _, at := range []string{"/live", "/app/ui", "/a/b/c"} {
			rec := p.get(p.app.Mux(at, p.app.PageHandler(pageOf)), http.MethodGet, at)

			Expect(rec.Code).NotTo(Equal(http.StatusTemporaryRedirect), "at %s", at)
			Expect(rec.Code).NotTo(Equal(http.StatusMovedPermanently), "at %s", at)
		}
	})

	It("accepts a mount path with a trailing slash and mounts it at the same place", func() {
		p := newPageApp(false)

		rec := p.get(p.app.Mux("/live/", p.app.PageHandler(pageOf)), http.MethodGet, "/live/gotth-live.min.js")

		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Header().Get("Content-Type")).To(HavePrefix("text/javascript"))
	})

	// A mount path is a constant in the caller's source, so a bad one is a
	// startup mistake in a literal. http.ServeMux.Handle — the method this one
	// calls — already panics for the same class, and the message names this
	// method rather than live.Script.
	DescribeTable("panics on a mount path a browser would not read as a path",
		func(mountPath string) {
			p := newPageApp(false)
			page := p.app.PageHandler(pageOf)

			Expect(func() { p.app.Mux(mountPath, page) }).
				To(PanicWith(MatchError(ContainSubstring("(*live.App).Mux"))))
		},
		Entry("empty", ""),
		Entry("relative", "live"),
		Entry("an authority", "//live"),
		Entry("a backslash", `/\live`),
		Entry("a query", "/live?x=1"),
		Entry("a fragment", "/live#x"),
		Entry("a control byte", "/li\tve"),
	)

	It("panics on the root, where the upgrade and the page would be one pattern", func() {
		p := newPageApp(false)
		page := p.app.PageHandler(pageOf)

		Expect(func() { p.app.Mux("/", page) }).To(PanicWith(ContainSubstring(`cannot mount an application at "/"`)))
	})

	It("panics on a nil page handler", func() {
		p := newPageApp(false)

		Expect(func() { p.app.Mux(mount, nil) }).To(PanicWith(ContainSubstring("nil page handler")))
	})
})

var _ = Describe("MustNew", func() {
	It("returns the application a valid Config describes", func() {
		Expect(live.MustNew(validConfig())).NotTo(BeNil())
	})

	// The value of the helper is that nothing is lost but the choice of what to
	// do next: the *ConfigError naming the field and the fix is the panic
	// value, not a summary of it.
	It("panics with the *ConfigError New would have returned", func() {
		cfg := validConfig()
		cfg.Reduce = nil

		var recovered any
		func() {
			defer func() { recovered = recover() }()
			live.MustNew(cfg)
		}()

		err, ok := recovered.(error)
		Expect(ok).To(BeTrue(), "the panic value was %T, not an error", recovered)
		var cfgErr *live.ConfigError
		Expect(errors.As(err, &cfgErr)).To(BeTrue())
		Expect(cfgErr.Field).To(Equal("Reduce"))
		Expect(err.Error()).To(ContainSubstring("gotth-live: Config.Reduce"))
	})
})

// The quickstart is a measured requirement (FR-53: a working counter in ≤15
// minutes and ≤31 lines — 30 until docs/PRD.md §9's v1.1 amendment moved it to
// the floor this API can express), and the three symbols above exist to shrink
// it. The count itself is asserted in docs/guide/_samples/samples_test.go;
// this file holds the shape rather than the number. This is the application
// that page counts, compiled — so a landing that breaks the shape the
// documentation prints fails here rather than in a reader's terminal.
var _ = Describe("the quickstart application", func() {
	const mountPath = "/live"
	const eventInc = "count.inc"

	type state struct{ N int }

	count := func(s state) templ.Component { return text("<output>%d</output>", s.N) }
	page := func(s state) templ.Component {
		return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
			if _, err := io.WriteString(w, "<!DOCTYPE html><html><head>"); err != nil {
				return err
			}
			if err := live.Script(mountPath).Render(ctx, w); err != nil {
				return err
			}
			if _, err := io.WriteString(w, "</head><body>"); err != nil {
				return err
			}
			if err := count(s).Render(ctx, w); err != nil {
				return err
			}
			_, err := io.WriteString(w, "</body></html>")
			return err
		})
	}

	It("builds, mounts and serves a first paint and the runtime from one Mux", func() {
		app := live.MustNew(live.Config[state, user]{
			Reduce: func(s state, ev live.Event) (state, []live.Effect[user]) {
				if ev.Name == eventInc {
					s.N++
				}
				return s, nil
			},
			Fragments:    []live.Fragment[state]{{ID: "count", Render: count}},
			Events:       []string{eventInc},
			Origins:      []string{"http://127.0.0.1:8080"},
			Authenticate: func(request *http.Request) (user, error) { return user("tester"), nil },
			Authorize:    live.AllowAll[user],
			CSRF:         live.NoCSRFCheck,
		})
		mux := app.Mux(mountPath, app.PageHandler(page))

		first := httptest.NewRecorder()
		mux.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/", nil))
		runtime := httptest.NewRecorder()
		mux.ServeHTTP(runtime, httptest.NewRequest(http.MethodGet, mountPath+"/gotth-live.min.js", nil))

		Expect(first.Code).To(Equal(http.StatusOK))
		Expect(first.Body.String()).To(ContainSubstring("<output>0</output>"))
		Expect(first.Body.String()).To(ContainSubstring(`data-gotth-url="/live"`))
		Expect(runtime.Code).To(Equal(http.StatusOK))
		Expect(runtime.Header().Get("Content-Type")).To(HavePrefix("text/javascript"))
		Expect(strings.Count(first.Body.String(), "<output>")).To(Equal(1))
	})
})
