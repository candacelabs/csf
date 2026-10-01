package webui_test

import (
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing/fstest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	"github.com/candacelabs/csf/services/candaceos/browserroutes"
	"github.com/candacelabs/csf/services/candaceos/httpserver"
	"github.com/candacelabs/csf/services/candaceos/webui"
)

func newOverlayServer(overlay fs.FS, options ...webui.Option) *httptest.Server {
	GinkgoHelper()
	handler, err := webui.New(
		webui.SnapshotFunc(func(ctx context.Context) (*candaceosv1.WebUISnapshot, error) {
			return richSnapshot(), nil
		}),
		append([]webui.Option{webui.WithUIOverlay(overlay)}, options...)...,
	)
	Expect(err).NotTo(HaveOccurred())
	router := httpserver.NewEngine()
	handler.Register(router)
	return httptest.NewServer(router)
}

// overlayError constructs a handler with the supplied overlay and returns the
// construction failure, so a spec can name what an unusable overlay costs.
func overlayError(options ...webui.Option) error {
	GinkgoHelper()
	handler, err := webui.New(
		webui.SnapshotFunc(func(ctx context.Context) (*candaceosv1.WebUISnapshot, error) {
			return richSnapshot(), nil
		}),
		options...,
	)
	Expect(handler).To(BeNil())
	return err
}

var _ = Describe("UI overlay templates", func() {
	It("lets an overlay redefine one named block and keep every other page part", func() {
		server := newOverlayServer(fstest.MapFS{
			"templates/pill.html": &fstest.MapFile{Data: []byte(
				`{{- define "statusPill" -}}<b class="pill">{{ statusLabel . }}</b>{{- end -}}`,
			)},
		})
		defer server.Close()

		_, index := get(server, browserroutes.Index)
		Expect(index).To(ContainSubstring(`<b class="pill">Running</b>`))
		Expect(index).NotTo(ContainSubstring(`class="status-pill`))
		Expect(index).To(ContainSubstring("<h1 id=\"home-title\">What do you want?</h1>"),
			"an overlay that redefines a fragment must leave the page around it embedded")

		_, chat := get(server, browserroutes.ClawChatPath("session-42"))
		Expect(chat).To(ContainSubstring(`<b class="pill">Running</b>`),
			"a redefined block applies to every page that uses it")
	})

	It("lets an overlay redefine the sidebar navigation block", func() {
		server := newOverlayServer(fstest.MapFS{
			"templates/nav.html": &fstest.MapFile{Data: []byte(
				`{{- define "primaryNav" -}}<nav class="primary-nav" aria-label="Primary">` +
					`{{ range . }}<a class="nav-item" href="{{ .Href }}"` +
					`{{ if .View }} data-nav="{{ .View }}"{{ end }}>{{ .Label }}</a>{{ end }}` +
					`</nav>{{- end -}}`,
			)},
		}, webui.WithNavItem(webui.NavItem{Label: "Reports", Href: "/reports"}))
		defer server.Close()

		_, index := get(server, browserroutes.Index)
		Expect(index).To(ContainSubstring(
			`<a class="nav-item" href="#home" data-nav="home">Home</a>`))
		Expect(index).To(ContainSubstring(`<a class="nav-item" href="/reports">Reports</a>`),
			"a redefined navigation block still renders the registered entries")
		Expect(index).NotTo(ContainSubstring(`class="nav-glyph"`))
	})

	It("lets an overlay replace a whole page", func() {
		server := newOverlayServer(fstest.MapFS{
			"templates/index.html": &fstest.MapFile{Data: []byte(
				`{{- define "index.html" -}}<!doctype html><title>{{ .Brand.ProductName }}</title>` +
					`<a href="{{ .Routes.AppCSS }}">styles</a>{{- end -}}`,
			)},
		})
		defer server.Close()

		response, index := get(server, browserroutes.Index)
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		Expect(index).To(Equal(
			`<!doctype html><title>CandaceOS</title><a href="/assets/app.css">styles</a>`))

		_, chat := get(server, browserroutes.ClawChatPath("session-42"))
		Expect(chat).To(ContainSubstring("Live Claw session"),
			"replacing one page must leave the other embedded")
	})

	It("keeps the embedded pages when the overlay carries no templates", func() {
		server := newOverlayServer(fstest.MapFS{
			"assets/extra.css": &fstest.MapFile{Data: []byte(".extra{}")},
		})
		defer server.Close()

		_, index := get(server, browserroutes.Index)
		Expect(index).To(ContainSubstring("<h1 id=\"home-title\">What do you want?</h1>"))
		Expect(index).To(ContainSubstring(`<nav class="primary-nav" aria-label="Primary">`))
	})

	It("ignores overlay text outside a definition", func() {
		server := newOverlayServer(fstest.MapFS{
			"templates/stray.html": &fstest.MapFile{Data: []byte(
				"leaked overlay prose\n{{ define \"statusPill\" }}<b>{{ statusLabel . }}</b>{{ end }}",
			)},
		})
		defer server.Close()

		_, index := get(server, browserroutes.Index)
		Expect(index).NotTo(ContainSubstring("leaked overlay prose"))
		Expect(index).To(ContainSubstring("<b>Running</b>"))
	})

	It("fails construction rather than serving an overlay it cannot parse", func() {
		err := overlayError(webui.WithUIOverlay(fstest.MapFS{
			"templates/broken.html": &fstest.MapFile{Data: []byte(`{{ define "statusPill" }}{{ .`)},
		}))
		Expect(err).To(MatchError(webui.ErrInvalidUIOverlay))
		Expect(err.Error()).To(ContainSubstring("templates/broken.html"),
			"the failure must name the file that could not be parsed")
	})

	It("refuses a missing or a second overlay", func() {
		Expect(overlayError(webui.WithUIOverlay(nil))).To(MatchError(webui.ErrInvalidUIOverlay))
		Expect(overlayError(
			webui.WithUIOverlay(fstest.MapFS{}),
			webui.WithUIOverlay(fstest.MapFS{}),
		)).To(MatchError(webui.ErrInvalidUIOverlay))
	})
})

var _ = Describe("UI overlay assets", func() {
	It("serves an overlay asset in place of the embedded one, with the same headers", func() {
		server := newOverlayServer(fstest.MapFS{
			"assets/app.css": &fstest.MapFile{Data: []byte(":root { --canvas: #101014; }\n")},
		})
		defer server.Close()

		response, body := get(server, browserroutes.AssetPath("app.css"))
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		Expect(body).To(Equal(":root { --canvas: #101014; }\n"))
		Expect(body).NotTo(ContainSubstring(".prompt-box"), "the embedded stylesheet must not leak through")
		Expect(response.Header.Get("Content-Type")).To(HavePrefix("text/css"))
		Expect(response.Header.Get("Cache-Control")).To(Equal("public, max-age=3600"))
		Expect(response.Header.Get("X-Content-Type-Options")).To(Equal("nosniff"))
	})

	It("serves an asset the overlay adds", func() {
		server := newOverlayServer(fstest.MapFS{
			"assets/wordmark.svg": &fstest.MapFile{Data: []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)},
		})
		defer server.Close()

		response, body := get(server, browserroutes.AssetPath("wordmark.svg"))
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		Expect(body).To(ContainSubstring("<svg"))
		Expect(response.Header.Get("Cache-Control")).To(Equal("public, max-age=3600"))
	})

	It("falls back to the embedded asset the overlay does not carry", func() {
		server := newOverlayServer(fstest.MapFS{
			"assets/app.css": &fstest.MapFile{Data: []byte(".overlay{}")},
		})
		defer server.Close()

		response, body := get(server, browserroutes.AssetPath("app.js"))
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		Expect(body).To(ContainSubstring("function bindNavigation()"))
		Expect(response.Header.Get("Cache-Control")).To(Equal("public, max-age=3600"))
	})

	It("answers a name neither layer carries with 404", func() {
		server := newOverlayServer(fstest.MapFS{})
		defer server.Close()

		response, _ := get(server, browserroutes.AssetPath("absent.css"))
		Expect(response.StatusCode).To(Equal(http.StatusNotFound))
	})

	It("keeps serving the generated brand stylesheet over an overlay of that name", func() {
		server := newOverlayServer(fstest.MapFS{
			browserroutes.AssetPath(browserroutes.BrandStylesheet)[1:]: &fstest.MapFile{
				Data: []byte("/* impostor */"),
			},
		}, webui.WithBrand(webui.Brand{Palette: webui.Palette{Canvas: "#101014"}}))
		defer server.Close()

		response, body := get(server, browserroutes.BrandStylesheetPath())
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		Expect(body).To(Equal(":root {\n  --canvas: #101014;\n}\n"))
		Expect(response.Header.Get("ETag")).NotTo(BeEmpty())
	})

	It("never reaches an overlay file outside the asset subtree", func() {
		server := newOverlayServer(fstest.MapFS{
			"templates/secret.html": &fstest.MapFile{Data: []byte("overlay template source")},
			"secret.txt":            &fstest.MapFile{Data: []byte("overlay root source")},
		})
		defer server.Close()

		for _, path := range []string{
			"/assets/%2e%2e/templates/secret.html",
			"/assets/../templates/secret.html",
			"/assets/%2e%2e/secret.txt",
			"/assets/templates/secret.html",
		} {
			response, body := get(server, path)
			Expect(response.StatusCode).NotTo(Equal(http.StatusOK), "path %s must not be served", path)
			Expect(strings.Contains(body, "overlay template source")).To(BeFalse(), "path %s leaked", path)
			Expect(strings.Contains(body, "overlay root source")).To(BeFalse(), "path %s leaked", path)
		}
	})
})
