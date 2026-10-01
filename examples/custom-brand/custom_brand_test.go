package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	"github.com/candacelabs/csf/services/candaceos/browserroutes"
	"github.com/candacelabs/csf/services/candaceos/httpserver"
	"github.com/candacelabs/csf/services/candaceos/webui"
)

func TestCustomBrand(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Custom Brand Example Suite")
}

// newExampleServer serves this example's presentation without assembling Core.
//
// An assembled Core opens PostgreSQL, a Warden client, and a harness, so a
// suite that ran the composition root would be an integration test with
// infrastructure — that suite exists, next to bootstrap itself. What is worth
// proving here is narrower and hermetic: that the four values this program
// hands the composition root really do produce the rebranded product. So the
// same values are handed to the web UI and the same engine Core builds, which
// is exactly what bootstrap does with them.
func newExampleServer() *httptest.Server {
	GinkgoHelper()
	handler, err := webui.New(
		webui.SnapshotFunc(func(ctx context.Context) (*candaceosv1.WebUISnapshot, error) {
			// The system carries no name: Core stamps the configured brand into
			// every snapshot it produces, and this spec should see that happen
			// rather than assert on a name it wrote itself.
			return &candaceosv1.WebUISnapshot{
				System: &candaceosv1.WebUISystem{Status: "healthy", Summary: "1 node · quorum healthy"},
			}, nil
		}),
		webui.WithBrand(brand()),
		webui.WithUIOverlay(overlayTree),
		webui.WithNavItem(sidebarEntry()),
	)
	Expect(err).NotTo(HaveOccurred())

	router := httpserver.NewEngine()
	handler.Register(router)
	harborLog{}.Register(router)
	server := httptest.NewServer(router)
	DeferCleanup(server.Close)
	return server
}

func fetch(server *httptest.Server, path string) (*http.Response, string) {
	GinkgoHelper()
	response, err := http.Get(server.URL + path)
	Expect(err).NotTo(HaveOccurred())
	body, err := io.ReadAll(response.Body)
	Expect(err).NotTo(HaveOccurred())
	Expect(response.Body.Close()).To(Succeed())
	return response, string(body)
}

var _ = Describe("the example's configuration", func() {
	It("passes the checks the composition root applies to it", func() {
		// These are the exact validations bootstrap.WithBrand and
		// bootstrap.WithNavItem run before any infrastructure is opened, so a
		// palette value or a label this example got wrong fails here rather
		// than at the operator's first startup.
		Expect(brand().Validate()).To(Succeed())
		Expect(sidebarEntry().Validate()).To(Succeed())
		Expect(seams()).To(HaveLen(4), "every seam this example documents must still be wired")
	})
})

var _ = Describe("the rebranded operator UI", func() {
	It("renders the invented identity rather than the shipped one", func() {
		server := newExampleServer()

		response, index := fetch(server, browserroutes.Index)
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		Expect(index).To(ContainSubstring("<title>Harborlight</title>"))
		Expect(index).To(ContainSubstring(`aria-label="Harborlight navigation"`))
		Expect(index).To(ContainSubstring(`<img src="/assets/wordmark.svg" alt="" width="18" height="18">`),
			"the wordmark fragment is emitted verbatim, glyph and all")
		Expect(index).To(ContainSubstring(`<p class="topbar-title" data-system-name>Harborlight</p>`),
			"an unnamed snapshot must take the configured product name")
		Expect(index).To(ContainSubstring(`"agent_name":"Skiff"`),
			"the browser client reads the agent name out of the first-paint snapshot")
		Expect(index).To(ContainSubstring(`<p class="kicker">Skiff, across your whole fleet</p>`),
			"the agent name is data; the sentence around it stays literal")
		Expect(index).NotTo(ContainSubstring(webui.DefaultProductName))
		Expect(index).NotTo(ContainSubstring(webui.DefaultAgentName),
			"no shipped brand-bearing string survives a full rebrand")
	})

	It("leaves Core's routes where they were", func() {
		server := newExampleServer()

		_, index := fetch(server, browserroutes.Index)
		Expect(index).To(ContainSubstring(
			`data-route-claw-chat="`+browserroutes.ClawChat+`"`),
			"rebranding the agent must not move the /claws/... paths the client posts to")
	})

	It("appends the sidebar entry after the four Core ships", func() {
		server := newExampleServer()

		_, index := fetch(server, browserroutes.Index)
		Expect(index).To(ContainSubstring(`href="` + harborLogPath + `"`))
		Expect(index).To(ContainSubstring("<span>Harbor Log</span>"))
		Expect(strings.Index(index, "Harbor Log")).To(BeNumerically(">", strings.Index(index, "Activity")),
			"a registered entry renders after the shipped ones")
	})
})

var _ = Describe("the brand palette", func() {
	It("arrives as a same-origin stylesheet instead of an inline style block", func() {
		server := newExampleServer()

		response, stylesheet := fetch(server, browserroutes.BrandStylesheetPath())
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		Expect(response.Header.Get("Content-Type")).To(HavePrefix("text/css"))
		Expect(stylesheet).To(ContainSubstring("--forest: #12304f;"))
		Expect(stylesheet).To(ContainSubstring("--canvas: #f2f4f8;"))

		pageResponse, index := fetch(server, browserroutes.Index)
		Expect(index).To(ContainSubstring(
			`<link rel="stylesheet" href="` + browserroutes.BrandStylesheetPath() + `">`))
		Expect(index).NotTo(ContainSubstring("<style"), "a rebrand inlines nothing")
		Expect(pageResponse.Header.Get("Content-Security-Policy")).To(ContainSubstring("style-src 'self'"))
		Expect(pageResponse.Header.Get("Content-Security-Policy")).To(ContainSubstring("script-src 'self'"))
	})
})

var _ = Describe("the UI overlay", func() {
	It("serves the glyph the wordmark points at", func() {
		server := newExampleServer()

		response, glyph := fetch(server, browserroutes.AssetPath(glyphAsset))
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		Expect(glyph).To(ContainSubstring("<svg"))
		Expect(response.Header.Get("Cache-Control")).To(Equal("public, max-age=3600"),
			"an overlay asset keeps the asset route's headers")
	})

	It("keeps shipping every asset it does not name", func() {
		server := newExampleServer()

		response, stylesheet := fetch(server, browserroutes.AssetPath("app.css"))
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		Expect(stylesheet).To(ContainSubstring(".brand {"),
			"the shipped stylesheet is what the palette overrides, so it must still be served")
	})
})

var _ = Describe("the page the sidebar entry links to", func() {
	It("is served by this program and wears the same brand", func() {
		server := newExampleServer()

		response, page := fetch(server, harborLogPath)
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		Expect(response.Header.Get("Content-Type")).To(HavePrefix("text/html"))
		Expect(page).To(ContainSubstring("<title>Harbor Log · Harborlight</title>"))
		Expect(page).To(ContainSubstring(
			`<link rel="stylesheet" href="`+browserroutes.BrandStylesheetPath()+`">`),
			"the palette is a stylesheet, so a page Core never heard of is branded by linking it")
		Expect(page).To(ContainSubstring("Northwind"), "the page renders its own data, not a snapshot")
	})

	It("inherits Core's engine headers", func() {
		server := newExampleServer()

		response, _ := fetch(server, harborLogPath)
		Expect(response.Header.Get("Content-Security-Policy")).To(ContainSubstring("default-src 'self'"))
		Expect(response.Header.Get("X-Content-Type-Options")).To(Equal("nosniff"))
	})
})
