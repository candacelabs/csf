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

// The specs for (*App).Document, the library-owned page shell.
//
// Two of them are the reason the component exists rather than checks on what it
// happens to do, and they are the two that would fail if somebody "simplified"
// it back into a package-level function:
//
//   - the inspector tag is above the runtime tag, and no argument makes it
//     otherwise (api-surface.md:272's ordering invariant, made inexpressible);
//   - a refusal writes nothing at all, so PageHandler's buffered render still
//     turns a broken page into a 500 rather than a truncated 200.
//
// Everything else here is a constraint somebody can regress by accident: a
// hardcoded lang, a defaulted title, a runtime tag that cannot be left off.

// renderDoc renders a component with children, which is how a templ file calls
// this one. render() in inspector_test.go renders without them.
func renderDoc(c templ.Component, children templ.Component) string {
	GinkgoHelper()

	var buf strings.Builder
	ctx := templ.WithChildren(context.Background(), children)
	Expect(c.Render(ctx, &buf)).To(Succeed())
	return buf.String()
}

// failingComponent is a component that renders nothing and fails, for the specs
// that assert an error is passed through rather than swallowed.
func failingComponent(err error) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error { return err })
}

var _ = Describe("(*App).Document", func() {
	Describe("the document it renders", func() {
		It("is a whole document, with the children in the body", func() {
			html := renderDoc(
				inspectorApp(false).Document("/live", "counter", nil),
				text("<p>hello</p>"),
			)

			Expect(html).To(HavePrefix("<!doctype html><html>"))
			Expect(html).To(HaveSuffix("</body></html>"))
			Expect(html).To(ContainSubstring("<body><p>hello</p></body>"))
		})

		It("declares the character encoding first in the head", func() {
			html := renderDoc(
				inspectorApp(false).Document("/live", "counter", nil,
					text(`<link rel="stylesheet" href="/app.css">`)),
				templ.NopComponent,
			)

			Expect(html).To(ContainSubstring(`<head><meta charset="utf-8">`),
				"a browser that has not found a charset is guessing, and it only revises "+
					"the guess within the first kilobyte")
		})

		It("renders the title it was given, escaped, and never a default", func() {
			html := renderDoc(
				inspectorApp(false).Document("/live", `Bob & "Alice" <live>`, nil),
				templ.NopComponent,
			)

			Expect(html).To(ContainSubstring(
				"<title>Bob &amp; &#34;Alice&#34; &lt;live&gt;</title>"))
		})

		It("refuses an empty title rather than rendering a blank one", func() {
			var buf strings.Builder
			err := inspectorApp(false).Document("/live", "", nil).Render(context.Background(), &buf)

			Expect(err).To(MatchError(ContainSubstring("empty title")))
			Expect(err).To(MatchError(ContainSubstring("pass the page's own title")),
				"FR-58: the error an application holds has to name the next step")
			Expect(buf.String()).To(BeEmpty(),
				"a refusal must write nothing: PageHandler's 500 is only honest if the "+
					"buffer is empty when the render fails")
		})
	})

	Describe("the <html> element's attributes", func() {
		It("adds nothing of its own — no lang, no anything", func() {
			html := renderDoc(inspectorApp(false).Document("/live", "counter", nil), templ.NopComponent)

			Expect(html).To(HavePrefix("<!doctype html><html><head>"))
			Expect(html).NotTo(ContainSubstring("lang="),
				"a live-connection library does not choose a document's language, and a "+
					"hardcoded lang is a default nobody asked for")
		})

		It("renders the attributes the application passed, escaped and in a stable order", func() {
			html := renderDoc(
				inspectorApp(false).Document("/live", "counter", templ.Attributes{
					"lang":       "cy",
					"data-theme": `dark" onload="x`,
				}),
				templ.NopComponent,
			)

			Expect(html).To(HavePrefix(
				`<!doctype html><html data-theme="dark&#34; onload=&#34;x" lang="cy">`))
		})
	})

	Describe("the head extension", func() {
		It("renders every component it is given, in order, after the title", func() {
			html := renderDoc(
				inspectorApp(false).Document("/live", "counter", nil,
					text(`<meta name="viewport" content="width=device-width">`),
					text(`<link rel="stylesheet" href="/app.css">`),
				),
				templ.NopComponent,
			)

			Expect(html).To(ContainSubstring(
				`<title>counter</title><meta name="viewport" content="width=device-width">` +
					`<link rel="stylesheet" href="/app.css">`))
		})

		It("costs a page that does not use it nothing at all", func() {
			with := renderDoc(inspectorApp(false).Document("/live", "counter", nil), templ.NopComponent)
			without := renderDoc(
				inspectorApp(false).Document("/live", "counter", nil, templ.NopComponent),
				templ.NopComponent,
			)

			Expect(with).To(Equal(without))
		})

		It("renders above the runtime tags, so an inspector put there is still above the runtime", func() {
			html := renderDoc(
				inspectorApp(true).Document("/live", "counter", nil,
					inspectorApp(true).InspectorScript("/live")),
				templ.NopComponent,
			)

			Expect(strings.Index(html, "gotth-live-inspector.min.js")).
				To(BeNumerically("<", strings.Index(html, "gotth-live.min.js")))
		})

		It("passes a head component's error through rather than swallowing it", func() {
			boom := errors.New("the stylesheet component failed")
			var buf strings.Builder
			err := inspectorApp(false).
				Document("/live", "counter", nil, failingComponent(boom)).
				Render(context.Background(), &buf)

			Expect(err).To(MatchError(boom))
		})
	})

	Describe("the runtime, inspector and dev-reload tags", func() {
		It("renders the runtime tag for the mount path it was given", func() {
			html := renderDoc(inspectorApp(false).Document("/app/live", "counter", nil), templ.NopComponent)

			Expect(html).To(ContainSubstring(
				`<script src="/app/live/gotth-live.min.js" data-gotth-url="/app/live" defer></script>`))
		})

		It("renders one tag in production, because both dev tags write nothing", func() {
			html := renderDoc(inspectorApp(false).Document("/live", "counter", nil), templ.NopComponent)

			Expect(strings.Count(html, "<script")).To(Equal(1))
			Expect(html).NotTo(ContainSubstring("inspector"))
			Expect(html).NotTo(ContainSubstring("dev-reload"))
		})

		// The byte order, pinned as bytes rather than as three index
		// comparisons. An ordering assertion built out of strings.Index passes
		// on any document containing the three substrings somewhere, which is
		// how a claim about order survives a document that no longer has one.
		// This asserts the head's exact contents, in sequence, so the only way
		// to keep it green is to emit those bytes in that order.
		It("pins the head's byte order in dev: charset, title, inspector, runtime, dev-reload", func() {
			app := inspectorApp(true)
			html := renderDoc(app.Document("/live", "counter", nil), templ.NopComponent)

			Expect(html).To(HavePrefix(
				`<!doctype html><html><head><meta charset="utf-8"><title>counter</title>`+
					`<script src="/live/gotth-live-inspector.min.js" defer></script>`+
					`<script src="/live/gotth-live.min.js" data-gotth-url="/live" defer></script>`+
					`<script src="/live/gotth-live-dev-reload.min.js" data-gotth-dev-url="/live" `),
				"the inspector must wrap the WebSocket constructor before the runtime opens "+
					"a socket, and both tags are deferred, so document order is the whole "+
					"mechanism (api-surface.md:272)")
			Expect(html).To(HaveSuffix(` defer></script></head><body></body></html>`))
		})

		It("refuses a mount path a browser would not read as a path, and writes nothing", func() {
			for _, bad := range []string{"", "live", "//live", `/li\ve`, "/live?x", "/live#x", "/li\tve"} {
				var buf strings.Builder
				err := inspectorApp(false).Document(bad, "counter", nil).
					Render(context.Background(), &buf)

				Expect(err).To(HaveOccurred(), "mount path %q was accepted", bad)
				Expect(err).To(MatchError(ContainSubstring("(*live.App).Document")),
					"the error has to name the call that made it, not live.Script")
				Expect(buf.String()).To(BeEmpty(), "mount path %q wrote a partial document", bad)
			}
		})
	})

	// L9-1's PS-1, and the reason this block exists rather than a paragraph
	// saying the hole is narrow. The composed call below rendered
	//
	//   runtime, inspector, runtime, dev-reload
	//
	// before this refusal existed: the first runtime opens its socket, and the
	// inspector wraps WebSocket afterwards and sees nothing, for the whole life
	// of that page. It is the ordering failure api-surface.md:272 describes and
	// not a duplicate tag with the ordering intact, which is what the landing
	// originally called it.
	Describe("a runtime tag rendered from the head content", func() {
		It("is refused, so a blind inspector becomes an error instead", func() {
			var buf strings.Builder
			err := inspectorApp(true).
				Document("/live", "t", nil, live.Script("/live")).
				Render(context.Background(), &buf)

			Expect(err).To(MatchError(ContainSubstring("live.Script was rendered inside the head content")))
			Expect(err).To(MatchError(ContainSubstring("(*live.App).Document")))
			Expect(err).To(MatchError(ContainSubstring("live.NoRuntime")),
				"FR-58: the error has to say what to do instead, and for a page that "+
					"genuinely must place its own tag that is NoRuntime")
			Expect(buf.String()).NotTo(ContainSubstring("gotth-live.min.js"),
				"the buffer may hold a partial head — that is what PageHandler's buffer "+
					"is for — but it must not hold the runtime tag this refused")
		})

		It("is refused in production too, where there is no inspector to blind", func() {
			err := inspectorApp(false).
				Document("/live", "t", nil, live.Script("/live")).
				Render(context.Background(), io.Discard)

			Expect(err).To(HaveOccurred(),
				"Config.Dev is a deploy-time switch: a page that only fails in dev is a "+
					"page whose production build is one flag away from two sockets")
		})

		It("refuses a whole Document nested in the head, which reaches the same place", func() {
			app := inspectorApp(true)
			err := app.Document("/live", "outer", nil, app.Document("/live", "inner", nil)).
				Render(context.Background(), io.Discard)

			Expect(err).To(MatchError(ContainSubstring("live.Script was rendered inside the head content")),
				"the inner document's own Script call renders under the outer's mark, "+
					"which is what makes this a class rather than one spelling")
		})

		It("still renders the inspector from the head content, which is above the runtime anyway", func() {
			app := inspectorApp(true)
			html := renderDoc(
				app.Document("/live", "t", nil, app.InspectorScript("/live")),
				templ.NopComponent,
			)

			Expect(strings.Count(html, "gotth-live-inspector.min.js")).To(Equal(2))
			Expect(strings.Index(html, "gotth-live-inspector.min.js")).
				To(BeNumerically("<", strings.Index(html, `src="/live/gotth-live.min.js"`)),
					"a duplicate inspector is harmless and the ordering still holds; only a "+
						"RUNTIME tag from the head is refused")
		})

		It("does not refuse live.Script anywhere else", func() {
			outside := render(live.Script("/live"))
			Expect(outside).To(ContainSubstring(`src="/live/gotth-live.min.js"`))

			// Among the CHILDREN, which is the residual this refusal
			// deliberately leaves open: the tag lands BELOW the inspector, so
			// the ordering holds and what remains is a duplicate runtime — a
			// different defect, with a different shape, and this time that is
			// the true description of it.
			body := renderDoc(
				inspectorApp(true).Document("/live", "t", nil),
				live.Script("/live"),
			)
			Expect(strings.Count(body, "gotth-live.min.js")).To(Equal(2))
			Expect(strings.Index(body, "gotth-live-inspector.min.js")).
				To(BeNumerically("<", strings.Index(body, `<body>`)))
		})
	})

	Describe("live.NoRuntime", func() {
		It("renders a complete document with no script tag of any kind", func() {
			html := renderDoc(
				inspectorApp(true).Document(live.NoRuntime, "sign in", templ.Attributes{"lang": "en"},
					text(`<link rel="stylesheet" href="/chat.css">`)),
				text("<h1>sign in</h1>"),
			)

			Expect(html).To(HavePrefix(`<!doctype html><html lang="en"><head>`))
			Expect(html).To(ContainSubstring(`<link rel="stylesheet" href="/chat.css">`))
			Expect(html).To(ContainSubstring("<body><h1>sign in</h1></body>"))
			Expect(html).NotTo(ContainSubstring("<script"),
				"a page that is deliberately not live must not open a socket over regions "+
					"it does not have")
		})

		It("is not a path any other mount-taking call would accept", func() {
			Expect(live.Script(live.NoRuntime).Render(context.Background(), io.Discard)).
				To(HaveOccurred())
		})

		// NoRuntime's godoc names this as the way to keep dev reload on a page
		// that is deliberately not live, and until L9-1 ran it nothing asserted
		// it. A documented escape hatch with no spec is a hatch that closes
		// silently, so this is the spec.
		It("still lets the dev-reload tag be placed by hand, which its godoc promises", func() {
			app := inspectorApp(true)
			html := renderDoc(
				app.Document(live.NoRuntime, "sign in", nil, app.DevReloadScript("/live")),
				text("<h1>sign in</h1>"),
			)

			Expect(html).To(ContainSubstring(`src="/live/gotth-live-dev-reload.min.js"`))
			Expect(html).To(ContainSubstring(`data-gotth-dev-url="/live"`))
			Expect(html).To(ContainSubstring(`data-gotth-dev-build="`))
			Expect(html).NotTo(ContainSubstring("gotth-live-inspector.min.js"))
			Expect(html).NotTo(ContainSubstring(`src="/live/gotth-live.min.js"`),
				"the page is still not live: the escape adds the reload tag and nothing else")
			Expect(html).To(ContainSubstring("<body><h1>sign in</h1></body>"))
		})

		// The other half of the same rule, and the reason the mark is keyed to
		// whether THIS document emits the runtime: with NoRuntime there is no
		// inspector for a hand-placed runtime tag to be ordered against, so
		// refusing it would refuse a page that works.
		It("lets a page that declared itself not live place its own runtime tag", func() {
			html := renderDoc(
				inspectorApp(true).Document(live.NoRuntime, "hand-rolled", nil, live.Script("/live")),
				templ.NopComponent,
			)

			Expect(html).To(ContainSubstring(`<script src="/live/gotth-live.min.js" data-gotth-url="/live" defer></script>`))
			Expect(strings.Count(html, "<script")).To(Equal(1))
		})
	})

	Describe("under PageHandler", func() {
		It("serves the whole document, and the runtime tag with it", func() {
			p := newPageApp(false)
			rec := httptest.NewRecorder()
			p.app.PageHandler(func(s pageState) templ.Component {
				return p.app.Document("/live", "counter", nil)
			}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(rec.Body.String()).To(HavePrefix("<!doctype html>"))
			Expect(rec.Body.String()).To(ContainSubstring("gotth-live.min.js"))
		})

		It("turns a document that cannot render into a 500 carrying no markup", func() {
			p := newPageApp(false)
			rec := httptest.NewRecorder()
			p.app.PageHandler(func(s pageState) templ.Component {
				return p.app.Document("//live", "counter", nil)
			}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

			Expect(rec.Code).To(Equal(http.StatusInternalServerError))
			Expect(rec.Body.String()).NotTo(ContainSubstring("<html"),
				"the buffered render is what makes a mid-render failure a 500 rather than "+
					"a 200 carrying half a document (FR-58 rides on this)")
		})
	})
})
