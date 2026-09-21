package webui_test

import (
	"context"
	"html/template"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	"github.com/candacelabs/csf/services/candaceos/browserroutes"
	"github.com/candacelabs/csf/services/candaceos/httpserver"
	"github.com/candacelabs/csf/services/candaceos/webui"
)

func newBrandedServer(brand webui.Brand, snapshot *candaceosv1.WebUISnapshot) *httptest.Server {
	GinkgoHelper()
	handler, err := webui.New(
		webui.SnapshotFunc(func(ctx context.Context) (*candaceosv1.WebUISnapshot, error) {
			return snapshot, nil
		}),
		webui.WithBrand(brand),
	)
	Expect(err).NotTo(HaveOccurred())
	router := httpserver.NewEngine()
	handler.Register(router)
	return httptest.NewServer(router)
}

var _ = Describe("palette values", func() {
	It("accepts the shapes the shipped tokens already use", func() {
		palette := webui.Palette{
			Canvas:      "#f3f5f0",
			Ink:         "rgb(23 34 30 / 90%)",
			Shadow:      "0 1px 2px rgba(23, 34, 30, 0.04), 0 12px 35px rgba(23, 34, 30, 0.055)",
			Radius:      "20px",
			RadiusSmall: "calc(20px - 7px)",
			Mono:        `"SFMono-Regular", Consolas, "Liberation Mono", monospace`,
		}
		Expect(palette.Validate()).To(Succeed())
	})

	DescribeTable("rejects a value that could escape its own declaration",
		func(value, because string) {
			err := webui.Palette{Canvas: value}.Validate()
			Expect(err).To(MatchError(webui.ErrInvalidPaletteValue), because)
			Expect(err.Error()).To(ContainSubstring("--canvas"), "the failure must name the token")
		},
		Entry("closes the rule", "#fff } body { display: none", "a brace ends the :root rule"),
		Entry("ends the declaration", "#fff; position: fixed", "a semicolon starts a second declaration"),
		Entry("fetches a remote resource", "url(https://example.test/pixel.png)", "a URL leaves the origin"),
		Entry("fetches through image-set", `image-set("https://example.test/a.png" 1x)`, "image-set leaves the origin"),
		Entry("uppercases the fetch", "URL(HTTPS://EXAMPLE.TEST/a.png)", "case must not evade the check"),
		Entry("opens an at-rule", `#fff @import "https://example.test/x.css"`, "an at-rule is not a value"),
		Entry("hides behind a comment", "#fff /* }", "a comment can swallow the closing brace"),
		Entry("escapes a brace", `#fff\7d body`, "a CSS escape can spell a brace"),
		Entry("breaks the line", "#fff\nbody { display: none", "a newline is not part of a token value"),
		Entry("leaves a group open", "rgba(23, 34, 30, 0.04", "an unclosed group swallows what follows"),
		Entry("closes a group it never opened", "23, 30)", "an unbalanced group is malformed"),
		Entry("leaves a string open", `"Liberation Mono`, "an unterminated string swallows what follows"),
		Entry("is longer than a token value should be", strings.Repeat("a", 257), "a value that long is a mistake"),
	)

	It("reports every offending token, not just the first", func() {
		err := webui.Palette{Canvas: "#fff;", Ink: "url(https://example.test/a.png)"}.Validate()
		Expect(err.Error()).To(ContainSubstring("--canvas"))
		Expect(err.Error()).To(ContainSubstring("--ink"))
	})

	It("renders set tokens as one :root rule in stylesheet order", func() {
		palette := webui.Palette{
			Canvas:      "#101014",
			Ink:         "#f5f7f5",
			SidebarInk:  "#e7ecf4",
			BrandAccent: "#8ab4ff",
			RadiusSmall: "9px",
		}
		Expect(palette.Stylesheet()).To(Equal(
			":root {\n" +
				"  --canvas: #101014;\n" +
				"  --ink: #f5f7f5;\n" +
				"  --sidebar-ink: #e7ecf4;\n" +
				"  --brand-accent: #8ab4ff;\n" +
				"  --radius-small: 9px;\n" +
				"}\n",
		))
	})

	It("renders nothing when no token is set", func() {
		Expect(webui.Palette{}.Stylesheet()).To(BeEmpty())
	})

	It("names only tokens the shipped stylesheet declares on :root", func() {
		// A palette entry whose token app.css never declares is a field a
		// consumer can set with no effect anywhere on the page, which is worse
		// than not offering it. Fill every field so Stylesheet lists the whole
		// inventory, then hold that inventory against the stylesheet actually
		// served.
		value := reflect.New(reflect.TypeOf(webui.Palette{})).Elem()
		for index := range value.NumField() {
			value.Field(index).SetString("#000000")
		}
		inventory := value.Interface().(webui.Palette).Stylesheet()

		server := newServer(webui.SnapshotFunc(func(ctx context.Context) (*candaceosv1.WebUISnapshot, error) {
			return goldenSnapshot(), nil
		}))
		defer server.Close()
		response, css := get(server, browserroutes.AssetPath("app.css"))
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		Expect(css).To(HavePrefix(":root {\n"), "app.css must open with the token block")
		root := css[:strings.Index(css, "}")]

		tokens := 0
		for _, line := range strings.Split(inventory, "\n") {
			token, _, found := strings.Cut(strings.TrimSpace(line), ":")
			if !found || !strings.HasPrefix(token, "--") {
				continue
			}
			tokens++
			Expect(root).To(ContainSubstring("\n  "+token+": "), token+" is not declared on :root in app.css")
		}
		Expect(tokens).To(Equal(value.NumField()), "every palette field must render one token")
	})
})

var _ = Describe("brand construction", func() {
	It("rejects a name that is not renderable plain text", func() {
		Expect(webui.Brand{ProductName: strings.Repeat("A", 65)}.Validate()).
			To(MatchError(webui.ErrInvalidBrandName))
		Expect(webui.Brand{AgentName: "Sco\tut"}.Validate()).
			To(MatchError(webui.ErrInvalidBrandName))
	})

	It("treats the zero brand as the stock identity", func() {
		Expect(webui.Brand{}.Validate()).To(Succeed())
	})

	It("refuses to build a handler with an invalid brand", func() {
		handler, err := webui.New(
			webui.SnapshotFunc(func(ctx context.Context) (*candaceosv1.WebUISnapshot, error) {
				return &candaceosv1.WebUISnapshot{}, nil
			}),
			webui.WithBrand(webui.Brand{Palette: webui.Palette{Canvas: "#fff; position: fixed"}}),
		)
		Expect(handler).To(BeNil())
		Expect(err).To(MatchError(webui.ErrInvalidPaletteValue))
	})

	It("refuses a nil option", func() {
		handler, err := webui.New(
			webui.SnapshotFunc(func(ctx context.Context) (*candaceosv1.WebUISnapshot, error) {
				return &candaceosv1.WebUISnapshot{}, nil
			}),
			nil,
		)
		Expect(handler).To(BeNil())
		Expect(err).To(MatchError(ContainSubstring("option 1 is nil")))
	})
})

var _ = Describe("the brand stylesheet", func() {
	It("serves an empty same-origin stylesheet for the stock brand", func() {
		server := newServer(webui.SnapshotFunc(func(ctx context.Context) (*candaceosv1.WebUISnapshot, error) {
			return goldenSnapshot(), nil
		}))
		defer server.Close()

		response, body := get(server, browserroutes.BrandStylesheetPath())
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		Expect(response.Header.Get("Content-Type")).To(HavePrefix("text/css"))
		Expect(response.Header.Get("Cache-Control")).To(Equal("public, max-age=3600"))
		Expect(response.Header.Get("X-Content-Type-Options")).To(Equal("nosniff"))
		Expect(response.Header.Get("ETag")).NotTo(BeEmpty())
		Expect(body).To(BeEmpty(), "the stock brand overrides nothing")
	})

	It("serves the palette overrides and keeps the page's style-src at self", func() {
		server := newBrandedServer(
			webui.Brand{
				ProductName: "Atlas",
				Palette:     webui.Palette{Canvas: "#101014", Forest: "#2b1b4d"},
			},
			goldenSnapshot(),
		)
		defer server.Close()

		response, body := get(server, browserroutes.BrandStylesheetPath())
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		Expect(body).To(Equal(":root {\n  --canvas: #101014;\n  --forest: #2b1b4d;\n}\n"))

		pageResponse, page := get(server, browserroutes.Index)
		Expect(pageResponse.Header.Get("Content-Security-Policy")).To(ContainSubstring("style-src 'self'"))
		Expect(page).NotTo(ContainSubstring("<style"), "the palette must not become an inline style block")
		appCSS := strings.Index(page, browserroutes.AssetPath("app.css"))
		brandCSS := strings.Index(page, browserroutes.BrandStylesheetPath())
		Expect(appCSS).To(BeNumerically(">=", 0))
		Expect(brandCSS).To(BeNumerically(">", appCSS), "the palette must be linked after app.css to win")
	})

	It("revalidates with an ETag that follows the palette", func() {
		server := newBrandedServer(webui.Brand{Palette: webui.Palette{Canvas: "#101014"}}, goldenSnapshot())
		defer server.Close()
		stock := newServer(webui.SnapshotFunc(func(ctx context.Context) (*candaceosv1.WebUISnapshot, error) {
			return goldenSnapshot(), nil
		}))
		defer stock.Close()

		response, _ := get(server, browserroutes.BrandStylesheetPath())
		etag := response.Header.Get("ETag")
		stockResponse, _ := get(stock, browserroutes.BrandStylesheetPath())
		Expect(etag).NotTo(Equal(stockResponse.Header.Get("ETag")))

		request, err := http.NewRequest(http.MethodGet, server.URL+browserroutes.BrandStylesheetPath(), nil)
		Expect(err).NotTo(HaveOccurred())
		request.Header.Set("If-None-Match", etag)
		revalidated, err := http.DefaultClient.Do(request)
		Expect(err).NotTo(HaveOccurred())
		defer revalidated.Body.Close()
		Expect(revalidated.StatusCode).To(Equal(http.StatusNotModified))
	})

	It("still serves the embedded assets beside the generated one", func() {
		server := newBrandedServer(webui.Brand{Palette: webui.Palette{Canvas: "#101014"}}, goldenSnapshot())
		defer server.Close()

		response, css := get(server, browserroutes.AssetPath("app.css"))
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		Expect(css).To(ContainSubstring(".prompt-box"))
	})
})

// unbrandedSnapshot is the golden fixture with the two brand-bearing strings
// left empty, exactly as a core that has not stamped its own identity would
// produce it. The handler's brand fills them, which is what makes the same
// fixture render two different products.
func unbrandedSnapshot() *candaceosv1.WebUISnapshot {
	snapshot := goldenSnapshot()
	snapshot.System.Name = ""
	return snapshot
}

var _ = Describe("brand-bearing copy", func() {
	// The custom wordmark deliberately omits the product name, so the counts
	// below compare copy against copy rather than against the lockup.
	custom := webui.Brand{
		ProductName: "Atlas",
		AgentName:   "Scout",
		Wordmark:    template.HTML(`<span class="wayfinder-mark" aria-hidden="true"><span></span></span>`),
	}

	renderPages := func(server *httptest.Server) (string, string) {
		GinkgoHelper()
		_, index := get(server, browserroutes.Index)
		_, chat := get(server, browserroutes.ClawChatPath("session-1"))
		return index, chat
	}

	It("replaces every stock name with the configured one", func() {
		stock := newServer(webui.SnapshotFunc(func(ctx context.Context) (*candaceosv1.WebUISnapshot, error) {
			return unbrandedSnapshot(), nil
		}))
		defer stock.Close()
		rebranded := newBrandedServer(custom, unbrandedSnapshot())
		defer rebranded.Close()

		stockIndex, stockChat := renderPages(stock)
		customIndex, customChat := renderPages(rebranded)

		for _, page := range []struct {
			name   string
			stock  string
			custom string
		}{
			{"home", stockIndex, customIndex},
			{"chat", stockChat, customChat},
		} {
			products := strings.Count(page.stock, webui.DefaultProductName)
			agents := strings.Count(page.stock, webui.DefaultAgentName)
			Expect(products).To(BeNumerically(">", 0), page.name)
			Expect(agents).To(BeNumerically(">", 0), page.name)

			Expect(strings.Count(page.custom, custom.ProductName)).To(Equal(products),
				"the %s page must name the product wherever it named CandaceOS", page.name)
			Expect(strings.Count(page.custom, custom.AgentName)).To(Equal(agents),
				"the %s page must name the agent wherever it named Claw", page.name)
			Expect(page.custom).NotTo(ContainSubstring(webui.DefaultProductName), page.name)
			Expect(page.custom).NotTo(ContainSubstring(webui.DefaultAgentName), page.name)
		}
	})

	It("keeps the /claws/ routes stable under a rebrand", func() {
		rebranded := newBrandedServer(custom, unbrandedSnapshot())
		defer rebranded.Close()

		index, chat := renderPages(rebranded)
		Expect(index).To(ContainSubstring(browserroutes.ClawChatPath("session-1")))
		Expect(index).To(ContainSubstring(`data-route-claw-messages="` + browserroutes.ClawMessages + `"`))
		Expect(chat).To(ContainSubstring(browserroutes.ClawMessagePath("session-1")))
	})

	It("emits the operator-trusted wordmark verbatim on both pages", func() {
		rebranded := newBrandedServer(custom, unbrandedSnapshot())
		defer rebranded.Close()

		index, chat := renderPages(rebranded)
		for _, page := range []string{index, chat} {
			Expect(page).To(ContainSubstring(string(custom.Wordmark)))
			Expect(page).NotTo(ContainSubstring(`class="brand-os"`), "the stock lockup must be gone")
		}
	})

	It("escapes the product name when no wordmark is drawn", func() {
		rebranded := newBrandedServer(webui.Brand{ProductName: "Ink & Iron"}, unbrandedSnapshot())
		defer rebranded.Close()

		index, _ := renderPages(rebranded)
		Expect(index).To(ContainSubstring("Ink &amp; Iron"))
		Expect(index).NotTo(ContainSubstring(`class="brand-os"`))
	})

	It("keeps the browser client's only brand literals in its fallback", func() {
		server := newServer(webui.SnapshotFunc(func(ctx context.Context) (*candaceosv1.WebUISnapshot, error) {
			return unbrandedSnapshot(), nil
		}))
		defer server.Close()

		_, javascript := get(server, browserroutes.AssetPath("app.js"))
		fallback := `var brandFallback = { product: "CandaceOS", agent: "Claw" };`
		Expect(javascript).To(ContainSubstring(fallback))
		Expect(javascript).To(ContainSubstring("agent_name"),
			"the client must read the agent name from the snapshot")

		remainder := strings.Replace(javascript, fallback, "", 1)
		Expect(remainder).NotTo(ContainSubstring(webui.DefaultProductName),
			"every other product-name literal must come from the snapshot")
		Expect(remainder).NotTo(ContainSubstring(webui.DefaultAgentName),
			"every other agent-name literal must come from the snapshot")
	})

	It("publishes both names to the browser client as snapshot data", func() {
		rebranded := newBrandedServer(custom, unbrandedSnapshot())
		defer rebranded.Close()

		response, body := get(rebranded, browserroutes.Snapshot)
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		Expect(body).To(ContainSubstring(`"name":"Atlas"`))
		Expect(body).To(ContainSubstring(`"agent_name":"Scout"`))
	})

	It("names the configured product in the chat page's error bodies", func() {
		handler, err := webui.New(
			webui.SnapshotFunc(func(ctx context.Context) (*candaceosv1.WebUISnapshot, error) {
				return nil, context.DeadlineExceeded
			}),
			webui.WithBrand(custom),
		)
		Expect(err).NotTo(HaveOccurred())
		router := httpserver.NewEngine()
		handler.Register(router)
		server := httptest.NewServer(router)
		defer server.Close()

		response, body := get(server, browserroutes.ClawChatPath("session-1"))
		Expect(response.StatusCode).To(Equal(http.StatusServiceUnavailable))
		Expect(body).To(ContainSubstring("Atlas snapshot unavailable"))
		Expect(body).NotTo(ContainSubstring(webui.DefaultProductName))
	})

	It("names the configured product while the control plane is unavailable", func() {
		handler, err := webui.New(
			webui.SnapshotFunc(func(ctx context.Context) (*candaceosv1.WebUISnapshot, error) {
				return nil, context.DeadlineExceeded
			}),
			webui.WithBrand(custom),
		)
		Expect(err).NotTo(HaveOccurred())
		router := httpserver.NewEngine()
		handler.Register(router)
		server := httptest.NewServer(router)
		defer server.Close()

		_, page := get(server, browserroutes.Index)
		Expect(page).To(ContainSubstring("Waiting for the local control plane"))
		Expect(page).NotTo(ContainSubstring(webui.DefaultProductName))
		Expect(page).NotTo(ContainSubstring(webui.DefaultAgentName))
	})
})
