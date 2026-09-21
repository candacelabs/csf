package live_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/live"
)

// The specs for PRD NFR-8's "MUST NOT load in production builds".
//
// The requirement is about a mechanism, so these are about a mechanism. There
// are exactly two ways a browser can come to load the inspector — a page that
// names it, and a request that asks for it — and Config.Dev gates both. Each
// gate is asserted in both positions, because a gate tested only in the closed
// position passes just as well when it is welded shut, and one tested only in
// the open position is the one that ships open.
//
// The third clause of NFR-8, that the inspector does not count against NFR-2,
// is not assertable from Go: it is a property of the built artifacts, and
// tools/minify holds each of the two to its own ceiling on every CI run.

// serving mounts an application at prefix and returns a test server, without
// opening a live connection. The connection-driving helpers in live_test.go
// dial a WebSocket and read a Snapshot before returning, which is exactly
// right for the session specs and is more machinery than an asset route needs.
func serving(prefix string, dev bool) *httptest.Server {
	GinkgoHelper()

	cfg := validConfig()
	cfg.Dev = dev
	return servingConfig(prefix, cfg)
}

// servingConfig is serving for a config the caller has already adjusted. The
// FR-57 route specs need one — the build identity is a Config field, and a
// spec that asserts the body of the build-identity route has to know what it
// should say.
func servingConfig(prefix string, cfg live.Config[counter, user]) *httptest.Server {
	GinkgoHelper()

	app, err := live.New(cfg)
	Expect(err).NotTo(HaveOccurred())

	mux := http.NewServeMux()
	mux.Handle(prefix, http.StripPrefix(strings.TrimSuffix(prefix, "/"), app.Handler()))
	ts := httptest.NewServer(mux)
	DeferCleanup(ts.Close)
	return ts
}

func inspectorApp(dev bool) *live.App[counter, user] {
	GinkgoHelper()

	cfg := validConfig()
	cfg.Dev = dev
	app, err := live.New(cfg)
	Expect(err).NotTo(HaveOccurred())
	return app
}

func render(c interface {
	Render(ctx context.Context, writer io.Writer) error
}) string {
	GinkgoHelper()

	var buf strings.Builder
	Expect(c.Render(context.Background(), &buf)).To(Succeed())
	return buf.String()
}

var _ = Describe("InspectorScript", func() {
	It("renders nothing at all when Dev is false", func() {
		Expect(render(inspectorApp(false).InspectorScript("/live"))).To(BeEmpty(),
			"a production page must carry no reference to the inspector (NFR-8)")
	})

	It("renders a deferred tag naming the inspector artifact when Dev is true", func() {
		Expect(render(inspectorApp(true).InspectorScript("/live"))).To(Equal(
			`<script src="/live/gotth-live-inspector.min.js" defer></script>`))
	})

	// The mount is threaded, not assumed. Script takes the same parameter for
	// the same reason: the prefix as the browser sees it is knowledge only the
	// caller has, and the two tags must address one mount or the inspector
	// 404s beside a runtime that loaded.
	DescribeTable("addresses the same mount the runtime tag does",
		func(mount string) {
			runtimeTag := render(live.Script(mount))
			inspectorTag := render(inspectorApp(true).InspectorScript(mount))

			runtimeSrc := scriptSrc(runtimeTag)
			inspectorSrc := scriptSrc(inspectorTag)
			Expect(strings.TrimSuffix(inspectorSrc, "gotth-live-inspector.min.js")).To(
				Equal(strings.TrimSuffix(runtimeSrc, "gotth-live.min.js")),
				"the two tags name different mounts: %q and %q", runtimeTag, inspectorTag)
		},
		Entry("the conventional mount", "/live"),
		Entry("a nested mount", "/app/live"),
		Entry("the root", "/"),
		Entry("a trailing slash", "/app/live/"),
	)

	It("HTML-escapes the mount path it writes into the tag", func() {
		Expect(render(inspectorApp(true).InspectorScript("/reports&sect;ion/live"))).To(Equal(
			`<script src="/reports&amp;sect;ion/live/gotth-live-inspector.min.js" defer></script>`))
	})

	// Validation does not depend on the mode. A mount path that is rejected in
	// dev and accepted in production would mean the first deploy after Dev was
	// turned off is where the error appears.
	DescribeTable("rejects a mount path Script would reject, in either mode",
		func(dev bool) {
			app := inspectorApp(dev)
			for _, bad := range []string{"", "live", "//evil.example", `/live\x`, "/live?x", "/live#x"} {
				var buf strings.Builder
				Expect(app.InspectorScript(bad).Render(context.Background(), &buf)).
					To(HaveOccurred(), "mount path %q was accepted", bad)
				Expect(buf.String()).To(BeEmpty(), "mount path %q wrote a tag before failing", bad)
			}
		},
		Entry("with Dev set", true),
		Entry("with Dev false", false),
	)
})

var _ = Describe("The inspector route", func() {
	It("serves the inspector when Dev is set", func() {
		resp, err := http.Get(serving("/", true).URL + "/gotth-live-inspector.min.js")
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())

		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Expect(body).NotTo(BeEmpty(), "the embedded inspector is empty")
		Expect(resp.Header.Get("Content-Type")).To(HavePrefix("text/javascript"))
		Expect(resp.Header.Get("ETag")).NotTo(BeEmpty())
		Expect(resp.Header.Get("Cache-Control")).To(ContainSubstring("immutable"))
	})

	It("404s the inspector when Dev is false, naming the switch", func() {
		resp, err := http.Get(serving("/", false).URL + "/gotth-live-inspector.min.js")
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())

		Expect(resp.StatusCode).To(Equal(http.StatusNotFound))
		Expect(string(body)).To(ContainSubstring("live.Config.Dev"),
			"FR-58: the error names the actionable next step")
	})

	// The runtime is not gated. Turning Dev off must take the inspector away
	// and nothing else — a production application that stopped serving its own
	// runtime would be a page that is not live at all.
	It("serves the runtime in both modes", func() {
		for _, dev := range []bool{true, false} {
			resp, err := http.Get(serving("/", dev).URL + "/gotth-live.min.js")
			Expect(err).NotTo(HaveOccurred())
			resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(http.StatusOK), "Dev=%v", dev)
		}
	})

	// The two filenames both end in ".min.js" and one contains most of the
	// other, so the suffix test that routes them is worth pinning: a request
	// for the inspector must never be answered with the runtime.
	It("does not answer an inspector request with the runtime bytes", func() {
		ts := serving("/", true)

		inspector, err := http.Get(ts.URL + "/gotth-live-inspector.min.js")
		Expect(err).NotTo(HaveOccurred())
		defer inspector.Body.Close()
		runtime, err := http.Get(ts.URL + "/gotth-live.min.js")
		Expect(err).NotTo(HaveOccurred())
		defer runtime.Body.Close()

		Expect(inspector.Header.Get("ETag")).NotTo(Equal(runtime.Header.Get("ETag")),
			"the two artifacts served the same bytes")
	})

	It("serves the inspector at whatever prefix the application is mounted under", func() {
		resp, err := http.Get(serving("/app/", true).URL + "/app/gotth-live-inspector.min.js")
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()

		Expect(resp.StatusCode).To(Equal(http.StatusOK))
	})

	It("answers a conditional request without resending the inspector", func() {
		ts := serving("/", true)

		first, err := http.Get(ts.URL + "/gotth-live-inspector.min.js")
		Expect(err).NotTo(HaveOccurred())
		first.Body.Close()

		req, err := http.NewRequest(http.MethodGet, ts.URL+"/gotth-live-inspector.min.js", nil)
		Expect(err).NotTo(HaveOccurred())
		req.Header.Set("If-None-Match", first.Header.Get("ETag"))

		resp, err := http.DefaultClient.Do(req)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()

		Expect(resp.StatusCode).To(Equal(http.StatusNotModified))
	})

	It("refuses a method other than GET or HEAD", func() {
		resp, err := http.Post(serving("/", true).URL+"/gotth-live-inspector.min.js", "text/plain", nil)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()

		Expect(resp.StatusCode).To(Equal(http.StatusMethodNotAllowed))
		Expect(resp.Header.Get("Allow")).To(Equal("GET, HEAD"))
	})
})
