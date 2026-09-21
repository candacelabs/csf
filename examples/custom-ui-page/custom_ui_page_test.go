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

func TestCustomUIPage(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Custom UI Page Example Suite")
}

// mount assembles this example's presentation the way Core does — the sidebar
// entry into the web UI, the service onto the same engine — without the
// database, Warden client, and harness an assembled Core would open.
func mount() *httptest.Server {
	GinkgoHelper()
	ui, err := webui.New(
		webui.SnapshotFunc(func(ctx context.Context) (*candaceosv1.WebUISnapshot, error) {
			return &candaceosv1.WebUISnapshot{}, nil
		}),
		webui.WithNavItem(entry),
	)
	Expect(err).NotTo(HaveOccurred())

	engine := httpserver.NewEngine()
	ui.Register(engine)
	runbooks{}.Register(engine)
	site := httptest.NewServer(engine)
	DeferCleanup(site.Close)
	return site
}

func read(site *httptest.Server, path string) (*http.Response, string) {
	GinkgoHelper()
	response, err := http.Get(site.URL + path)
	Expect(err).NotTo(HaveOccurred())
	defer func() { Expect(response.Body.Close()).To(Succeed()) }()
	body, err := io.ReadAll(response.Body)
	Expect(err).NotTo(HaveOccurred())
	return response, string(body)
}

var _ = Describe("the added sidebar entry", func() {
	It("is one Core accepts", func() {
		// The same validation bootstrap.WithNavItem runs before any
		// infrastructure is opened.
		Expect(entry.Validate()).To(Succeed())
	})

	It("renders as a plain link after the four Core ships", func() {
		site := mount()

		response, index := read(site, browserroutes.Index)
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		Expect(index).To(ContainSubstring(`href="` + runbookPath + `"`))
		Expect(index).To(ContainSubstring("<span>Runbooks</span>"))
		Expect(index).NotTo(ContainSubstring(`href="`+runbookPath+`" data-nav`),
			"an entry with no View is ordinary navigation, not an in-page view switch")
		Expect(strings.Index(index, "Runbooks")).To(BeNumerically(">", strings.Index(index, "Activity")))
	})

	It("leaves the shipped identity and routes alone", func() {
		site := mount()

		_, index := read(site, browserroutes.Index)
		Expect(index).To(ContainSubstring("<title>" + webui.DefaultProductName + "</title>"))
		Expect(index).To(ContainSubstring(webui.DefaultAgentName + ", across your whole fleet"))
		Expect(index).To(ContainSubstring(`data-route-claw-chat="` + browserroutes.ClawChat + `"`))
	})
})

var _ = Describe("the added page", func() {
	It("serves the embedding product's own content", func() {
		site := mount()

		response, page := read(site, runbookPath)
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		Expect(response.Header.Get("Content-Type")).To(HavePrefix("text/html"))
		Expect(page).To(ContainSubstring("<title>Runbooks</title>"))
		for _, wanted := range entries {
			Expect(page).To(ContainSubstring(wanted))
		}
	})

	It("links Core's stylesheets and inherits Core's headers", func() {
		site := mount()

		response, page := read(site, runbookPath)
		Expect(page).To(ContainSubstring(browserroutes.AssetPath("app.css")))
		Expect(page).To(ContainSubstring(browserroutes.BrandStylesheetPath()))
		Expect(page).NotTo(ContainSubstring("<style"))
		Expect(response.Header.Get("Content-Security-Policy")).To(ContainSubstring("default-src 'self'"))
		Expect(response.Header.Get("X-Content-Type-Options")).To(Equal("nosniff"))
	})
})
