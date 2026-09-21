package externalconsumer_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	"github.com/candacelabs/csf/services/candaceos/browserroutes"
	"github.com/candacelabs/csf/services/candaceos/component"
	"github.com/candacelabs/csf/services/candaceos/httpserver"
	"github.com/candacelabs/csf/services/candaceos/webui"

	"example.com/candace-external-consumer/composition"
	"example.com/candace-external-consumer/identity"
	"example.com/candace-external-consumer/noteboard"
	"example.com/candace-external-consumer/steering"
)

// These specs prove the presentation half of the composition from outside the
// module: an invented identity, an overlay that redefines one shipped block, a
// sidebar entry, and a page of this repository's own — every value resolved
// through @candace// labels pointing at a downloaded archive.
//
// They need no Core, no PostgreSQL, and no network. An assembled Core opens a
// database, a Warden client, and a harness, so a suite that ran the composition
// root would be an integration test with infrastructure. What is worth proving
// here is narrower: that the values this repository hands the composition root
// really do produce the rebranded product. So the same values are handed to the
// web UI and to the same engine Core builds, which is exactly what bootstrap
// does with them.

// recordingCapabilities stands in for the one typed Core surface a component
// receives. Core namespaces and redacts what a component logs; nothing about
// this composition depends on that, so the stub only has to accept it.
type recordingCapabilities struct{}

func (recordingCapabilities) Log(ctx context.Context, event string, message string) error { return nil }

// runningProduct assembles and starts this repository's own bring-up graph, in
// the order Core would resolve it, and stops it in reverse afterwards. Core
// runs exactly these calls around a registered component; running them here is
// what makes the page below serve live state rather than a fixture.
func runningProduct() *composition.Product {
	GinkgoHelper()
	product, err := composition.New()
	Expect(err).NotTo(HaveOccurred())

	ordered, err := component.Order(product.Components...)
	Expect(err).NotTo(HaveOccurred())
	for _, definition := range ordered {
		Expect(definition.Assemble(context.Background(), recordingCapabilities{})).To(Succeed())
		Expect(definition.Start(context.Background())).To(Succeed())
	}
	DeferCleanup(func() {
		for index := len(ordered) - 1; index >= 0; index-- {
			Expect(ordered[index].Stop(context.Background())).To(Succeed())
		}
	})
	return product
}

// newProductServer serves this repository's presentation on the engine Core
// builds, configured with the values its composition root supplies.
func newProductServer(product *composition.Product) *httptest.Server {
	GinkgoHelper()
	handler, err := webui.New(
		webui.SnapshotFunc(func(ctx context.Context) (*candaceosv1.WebUISnapshot, error) {
			// The system carries no name: Core stamps the configured brand into
			// every snapshot it produces, and these specs should see that
			// happen rather than assert on a name they wrote themselves.
			return &candaceosv1.WebUISnapshot{
				System: &candaceosv1.WebUISystem{
					Status:  "healthy",
					Summary: "2 nodes · quorum healthy",
				},
			}, nil
		}),
		webui.WithBrand(identity.Brand()),
		webui.WithUIOverlay(identity.Overlay()),
		webui.WithNavItem(noteboard.NavItem()),
	)
	Expect(err).NotTo(HaveOccurred())

	router := httpserver.NewEngine()
	handler.Register(router)
	product.Board.Register(router)
	server := httptest.NewServer(router)
	DeferCleanup(server.Close)
	return server
}

func get(server *httptest.Server, path string) (*http.Response, string) {
	GinkgoHelper()
	response, err := http.Get(server.URL + path)
	Expect(err).NotTo(HaveOccurred())
	body, err := io.ReadAll(response.Body)
	Expect(err).NotTo(HaveOccurred())
	Expect(response.Body.Close()).To(Succeed())
	return response, string(body)
}

var _ = Describe("the rebranded operator UI", func() {
	It("renders the invented identity rather than the shipped one", func() {
		server := newProductServer(runningProduct())

		response, index := get(server, browserroutes.Index)
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		Expect(index).To(ContainSubstring("<title>Quillfern</title>"))
		Expect(index).To(ContainSubstring(`aria-label="Quillfern navigation"`))
		Expect(index).To(ContainSubstring(`<span>Quill<span class="brand-os">fern</span></span>`),
			"the wordmark fragment is emitted verbatim")
		Expect(index).To(ContainSubstring(`<p class="topbar-title" data-system-name>Quillfern</p>`),
			"an unnamed snapshot must take the configured product name")
		Expect(index).To(ContainSubstring(`"agent_name":"Bramble"`),
			"the browser client reads the agent name out of the first-paint snapshot")
		Expect(index).To(ContainSubstring(`<p class="kicker">Bramble, across your whole fleet</p>`),
			"the agent name is data; the sentence around it stays literal")
		Expect(index).NotTo(ContainSubstring(webui.DefaultProductName))
		Expect(index).NotTo(ContainSubstring(webui.DefaultAgentName),
			"no shipped brand-bearing string survives a full rebrand")
	})

	It("leaves Core's routes where they were", func() {
		server := newProductServer(runningProduct())

		_, index := get(server, browserroutes.Index)
		Expect(index).To(ContainSubstring(
			`data-route-claw-chat="`+browserroutes.ClawChat+`"`),
			"rebranding the agent must not move the /claws/... paths the client posts to")
	})

	It("appends the sidebar entry after the four Core ships", func() {
		server := newProductServer(runningProduct())

		_, index := get(server, browserroutes.Index)
		Expect(index).To(ContainSubstring(`href="` + noteboard.Path + `"`))
		Expect(index).To(ContainSubstring("<span>Field Notes</span>"))
		Expect(strings.Index(index, "Field Notes")).
			To(BeNumerically(">", strings.Index(index, "Activity")),
				"a registered entry renders after the shipped ones")
	})

	It("serves the palette as a same-origin stylesheet rather than an inline block", func() {
		server := newProductServer(runningProduct())

		response, stylesheet := get(server, browserroutes.BrandStylesheetPath())
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		Expect(response.Header.Get("Content-Type")).To(HavePrefix("text/css"))
		Expect(stylesheet).To(ContainSubstring("--forest: #24402c;"))
		Expect(stylesheet).To(ContainSubstring("--brand-accent: #b8d98a;"),
			"the wordmark's second half is painted by a token, not by markup")

		pageResponse, index := get(server, browserroutes.Index)
		Expect(index).To(ContainSubstring(
			`<link rel="stylesheet" href="` + browserroutes.BrandStylesheetPath() + `">`))
		Expect(index).NotTo(ContainSubstring("<style"), "a rebrand inlines nothing")
		Expect(pageResponse.Header.Get("Content-Security-Policy")).
			To(ContainSubstring("style-src 'self'"))
	})
})

var _ = Describe("the UI overlay", func() {
	It("redefines the shipped block it names and nothing else", func() {
		server := newProductServer(runningProduct())

		_, index := get(server, browserroutes.Index)
		Expect(index).To(ContainSubstring(`data-quillfern-status="idle"`),
			"every status chip renders through the overlay's definition")
		Expect(index).To(ContainSubstring(`<span class="status-pill tone-`),
			"an override keeps the helpers the block it replaces was written against")
	})

	It("keeps shipping every asset it does not name", func() {
		server := newProductServer(runningProduct())

		response, stylesheet := get(server, browserroutes.AssetPath("app.css"))
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		Expect(stylesheet).To(ContainSubstring(".brand {"),
			"the shipped stylesheet is what the palette overrides, so it must still be served")
	})
})

var _ = Describe("the page the sidebar entry links to", func() {
	It("is served by this repository and wears the same brand", func() {
		server := newProductServer(runningProduct())

		response, page := get(server, noteboard.Path)
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		Expect(response.Header.Get("Content-Type")).To(HavePrefix("text/html"))
		Expect(page).To(ContainSubstring("<title>Field Notes · Quillfern</title>"))
		Expect(page).To(ContainSubstring(
			`<link rel="stylesheet" href="`+browserroutes.BrandStylesheetPath()+`">`),
			"the palette is a stylesheet, so a page Core never heard of is branded by linking it")
	})

	It("renders what this repository's own components recorded", func() {
		product := runningProduct()
		server := newProductServer(product)

		// The harness publishes every prompt to the steering service, the
		// service writes it to the store it was ordered after, and the
		// noteboard folds the store's window into its ledger. Nothing on this
		// path is Core's.
		steering.Instance().Observe("drain the west rack before the upgrade")
		steering.Instance().Observe("drain the west rack before the upgrade")
		steering.Instance().Observe("re-run the nightly backup check")

		_, page := get(server, noteboard.Path)
		Expect(page).To(ContainSubstring(`data-field-notes-state="ready"`))
		Expect(page).To(ContainSubstring("drain the west rack before the upgrade"))
		Expect(page).To(ContainSubstring("re-run the nightly backup check"))
		Expect(strings.Count(page, "drain the west rack before the upgrade")).To(Equal(1),
			"a repeated steering input is a retry, not a second note")
	})

	It("inherits Core's engine headers", func() {
		server := newProductServer(runningProduct())

		response, _ := get(server, noteboard.Path)
		Expect(response.Header.Get("Content-Security-Policy")).
			To(ContainSubstring("default-src 'self'"))
		Expect(response.Header.Get("X-Content-Type-Options")).To(Equal("nosniff"))
	})
})
