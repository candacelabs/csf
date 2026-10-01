package webui_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	"github.com/candacelabs/csf/services/candaceos/browserroutes"
	"github.com/candacelabs/csf/services/candaceos/httpserver"
	"github.com/candacelabs/csf/services/candaceos/webui"
)

func newNavServer(snapshot *candaceosv1.WebUISnapshot, options ...webui.Option) *httptest.Server {
	GinkgoHelper()
	handler, err := webui.New(
		webui.SnapshotFunc(func(ctx context.Context) (*candaceosv1.WebUISnapshot, error) {
			return snapshot, nil
		}),
		options...,
	)
	Expect(err).NotTo(HaveOccurred())
	router := httpserver.NewEngine()
	handler.Register(router)
	return httptest.NewServer(router)
}

// navAnchors returns the opening <a> tag of every sidebar entry, in render
// order. It reads the served markup rather than the template, so these
// assertions hold for whatever the page actually shipped.
func navAnchors(body string) []string {
	GinkgoHelper()
	_, sidebar, found := strings.Cut(body, `<nav class="primary-nav"`)
	Expect(found).To(BeTrue(), "the page must render the primary navigation")
	sidebar, _, found = strings.Cut(sidebar, "</nav>")
	Expect(found).To(BeTrue(), "the primary navigation must be closed")

	var anchors []string
	remainder := sidebar
	for {
		_, opened, more := strings.Cut(remainder, `<a class="nav-item`)
		if !more {
			return anchors
		}
		tag, rest, closed := strings.Cut(opened, ">")
		Expect(closed).To(BeTrue(), "every navigation anchor must be closed")
		anchors = append(anchors, `<a class="nav-item`+tag+">")
		remainder = rest
	}
}

var _ = Describe("sidebar navigation", func() {
	It("renders the shipped entries from data, with their views and live counts", func() {
		server := newNavServer(richSnapshot())
		defer server.Close()

		_, body := get(server, browserroutes.Index)
		anchors := navAnchors(body)
		Expect(anchors).To(HaveLen(len(webui.DefaultNavItems())))
		Expect(anchors[0]).To(Equal(
			`<a class="nav-item is-active" href="#home" data-nav="home" aria-current="page">`))
		Expect(anchors[1]).To(Equal(`<a class="nav-item" href="#apps" data-nav="apps">`))
		Expect(anchors[2]).To(Equal(`<a class="nav-item" href="#fleet" data-nav="fleet">`))
		Expect(anchors[3]).To(Equal(`<a class="nav-item" href="#activity" data-nav="activity">`))

		Expect(body).To(ContainSubstring(`<span class="nav-count" data-app-count>1</span>`))
		Expect(body).To(ContainSubstring(`<span class="nav-count" data-node-count>1</span>`))
		Expect(strings.Count(body, `aria-current="page"`)).To(Equal(1),
			"exactly one entry may be the current page")
		Expect(body).To(ContainSubstring(`<nav class="primary-nav" aria-label="Primary">`))
	})

	It("appends a registered entry after the shipped ones as a plain link", func() {
		server := newNavServer(richSnapshot(), webui.WithNavItem(webui.NavItem{
			Label: "Receipts",
			Href:  "/receipts",
			Glyph: "§",
		}))
		defer server.Close()

		_, body := get(server, browserroutes.Index)
		anchors := navAnchors(body)
		Expect(anchors).To(HaveLen(len(webui.DefaultNavItems()) + 1))
		injected := anchors[len(anchors)-1]
		Expect(injected).To(Equal(`<a class="nav-item" href="/receipts">`))
		Expect(injected).NotTo(ContainSubstring("data-nav"),
			"an entry that names no rendered view must stay ordinary navigation")
		Expect(injected).NotTo(ContainSubstring("aria-current"))
		Expect(body).To(ContainSubstring(
			`<span class="nav-glyph" aria-hidden="true">§</span><span>Receipts</span>`))
	})

	It("renders every registered entry in registration order", func() {
		server := newNavServer(richSnapshot(),
			webui.WithNavItem(webui.NavItem{Label: "Reports", Href: "/reports"}),
			webui.WithNavItem(webui.NavItem{Label: "Runbooks", Href: "/runbooks"}),
		)
		defer server.Close()

		_, body := get(server, browserroutes.Index)
		anchors := navAnchors(body)
		Expect(anchors).To(HaveLen(len(webui.DefaultNavItems()) + 2))
		Expect(anchors[4]).To(Equal(`<a class="nav-item" href="/reports">`))
		Expect(anchors[5]).To(Equal(`<a class="nav-item" href="/runbooks">`))
	})

	It("lets a registered entry switch to a view the page already renders", func() {
		server := newNavServer(richSnapshot(), webui.WithNavItem(webui.NavItem{
			Label: "All activity",
			Href:  "#" + webui.NavViewActivity,
			View:  webui.NavViewActivity,
		}))
		defer server.Close()

		_, body := get(server, browserroutes.Index)
		anchors := navAnchors(body)
		Expect(anchors[len(anchors)-1]).To(Equal(
			`<a class="nav-item" href="#activity" data-nav="activity">`))
		Expect(strings.Count(body, `aria-current="page"`)).To(Equal(1),
			"a registered entry never steals the current-page marking")
	})

	It("escapes registered text and neutralizes a target the browser must not follow", func() {
		server := newNavServer(richSnapshot(),
			webui.WithNavItem(webui.NavItem{Label: `Reports<script>alert("x")</script>`, Href: "/reports"}),
			webui.WithNavItem(webui.NavItem{Label: "Run it", Href: "javascript:alert(1)"}),
		)
		defer server.Close()

		_, body := get(server, browserroutes.Index)
		Expect(body).NotTo(ContainSubstring("<script>alert"))
		Expect(body).To(ContainSubstring("Reports&lt;script&gt;"))
		Expect(body).NotTo(ContainSubstring("javascript:alert"))
		Expect(navAnchors(body)).To(ContainElement(`<a class="nav-item" href="#ZgotmplZ">`))
	})

	DescribeTable("refuses an entry that cannot be rendered as one labeled link",
		func(item webui.NavItem, because string) {
			handler, err := webui.New(
				webui.SnapshotFunc(func(ctx context.Context) (*candaceosv1.WebUISnapshot, error) {
					return richSnapshot(), nil
				}),
				webui.WithNavItem(item),
			)
			Expect(handler).To(BeNil(), because)
			Expect(err).To(MatchError(webui.ErrInvalidNavItem), because)
		},
		Entry("has no label", webui.NavItem{Href: "/reports"}, "an unlabeled link is unreachable by name"),
		Entry("has no target", webui.NavItem{Label: "Reports"}, "a link needs somewhere to go"),
		Entry("is labeled with whitespace", webui.NavItem{Label: "   ", Href: "/reports"}, "blank is not a label"),
		Entry("carries a control character",
			webui.NavItem{Label: "Rep\torts", Href: "/reports"}, "a control character is not renderable text"),
		Entry("is labeled with an essay",
			webui.NavItem{Label: strings.Repeat("A", 65), Href: "/reports"}, "a label that long reflows the sidebar"),
		Entry("draws a sentence as its glyph",
			webui.NavItem{Label: "Reports", Href: "/reports", Glyph: strings.Repeat("§", 17)}, "a glyph is one mark"),
		Entry("names a view that is not an attribute value",
			webui.NavItem{Label: "Reports", Href: "#a", View: `a" onload="x`}, "a view name is a plain identifier"),
	)

	It("trims a registered entry so padding does not change the markup", func() {
		server := newNavServer(richSnapshot(), webui.WithNavItem(webui.NavItem{
			Label: "  Reports  ", Href: "  /reports  ", Glyph: " § ",
		}))
		defer server.Close()

		_, body := get(server, browserroutes.Index)
		Expect(navAnchors(body)).To(ContainElement(`<a class="nav-item" href="/reports">`))
		Expect(body).To(ContainSubstring(
			`<span class="nav-glyph" aria-hidden="true">§</span><span>Reports</span>`))
	})

	It("keeps the chat page free of the sidebar navigation", func() {
		server := newNavServer(richSnapshot(), webui.WithNavItem(webui.NavItem{
			Label: "Reports", Href: "/reports",
		}))
		defer server.Close()

		response, body := get(server, browserroutes.ClawChatPath("session-42"))
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		Expect(body).NotTo(ContainSubstring(`class="primary-nav"`))
		Expect(body).NotTo(ContainSubstring("/reports"))
	})

	It("hands the browser client the navigation instead of a list it hard-codes", func() {
		server := newNavServer(richSnapshot())
		defer server.Close()

		_, javascript := get(server, browserroutes.AssetPath("app.js"))
		for _, view := range []string{
			webui.NavViewHome, webui.NavViewApps, webui.NavViewFleet, webui.NavViewActivity,
		} {
			Expect(javascript).NotTo(ContainSubstring(`"`+view+`"`),
				"view %s must come from the rendered page, not the client", view)
		}
		Expect(javascript).To(ContainSubstring("function defaultView()"))
		Expect(javascript).To(ContainSubstring("function viewSection(name)"))
		Expect(javascript).To(ContainSubstring(`selectAll("[data-nav]")`))
	})
})
