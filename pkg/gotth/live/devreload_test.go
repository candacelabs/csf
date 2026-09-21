package live_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/live"
)

// The specs for FR-57's server half.
//
// Two things are asserted here and they are different in kind.
//
// The first is the production gates, and they are asserted the way
// inspector_test.go asserts NFR-8's: there are exactly three ways a browser
// can come to take part in dev reload — a page that names the client, a
// request for that client, and a request for the build identity — and
// Config.Dev gates all three. Each is asserted in BOTH positions, because a
// gate tested only closed passes just as well when it is welded shut, and one
// tested only open is the one that ships open.
//
// The second is the build identity itself, which is the whole mechanism: a
// value that changes when the code changes and does not change when it does
// not. The "does not change" half is checkable in-process. The "does change"
// half is not — it is a property of two different executables — so it is
// measured against two real binaries built by the Go toolchain, in a spec that
// skips where no toolchain is present rather than asserting something weaker.

func devReloadApp(dev bool) *live.App[counter, user] {
	GinkgoHelper()

	cfg := validConfig()
	cfg.Dev = dev
	app, err := live.New(cfg)
	Expect(err).NotTo(HaveOccurred())
	return app
}

func stampedApp(dev bool, id string) *live.App[counter, user] {
	GinkgoHelper()

	cfg := validConfig()
	cfg.Dev = dev
	cfg.DevBuildID = id
	app, err := live.New(cfg)
	Expect(err).NotTo(HaveOccurred())
	return app
}

// stampedServing mounts an application with an explicit build identity, so a
// route spec can assert the body rather than merely that there is one.
func stampedServing(prefix string, dev bool, id string) *httptest.Server {
	GinkgoHelper()

	cfg := validConfig()
	cfg.Dev = dev
	cfg.DevBuildID = id
	return servingConfig(prefix, cfg)
}

var _ = Describe("DevReloadScript", func() {
	It("renders nothing at all when Dev is false", func() {
		Expect(render(devReloadApp(false).DevReloadScript("/live"))).To(BeEmpty(),
			"a production page must carry no reference to dev reload, and must not disclose a build identity")
	})

	It("renders a deferred tag carrying the mount and the build identity when Dev is true", func() {
		Expect(render(stampedApp(true, "build-7").DevReloadScript("/live"))).To(Equal(
			`<script src="/live/gotth-live-dev-reload.min.js" ` +
				`data-gotth-dev-url="/live" data-gotth-dev-build="build-7" defer></script>`))
	})

	// The baseline is the identity of the build that rendered THIS document,
	// which is the reason it is on the tag rather than fetched at boot. A
	// client that adopted its first fetched value would accept a rebuild that
	// landed while the page was loading as "what this page is showing".
	It("stamps the running build's identity when none was configured", func() {
		tag := render(devReloadApp(true).DevReloadScript("/live"))

		id := scriptAttr(tag, "data-gotth-dev-build")
		Expect(id).NotTo(BeEmpty(), "the tag carried no baseline, so nothing would ever be compared")
		Expect(id).To(Equal(scriptAttr(render(devReloadApp(true).DevReloadScript("/live")),
			"data-gotth-dev-build")),
			"two renders inside one process disagreed about which build is running")
	})

	DescribeTable("addresses the same mount the runtime tag does",
		func(mount string) {
			runtimeTag := render(live.Script(mount))
			reloadTag := render(devReloadApp(true).DevReloadScript(mount))

			Expect(strings.TrimSuffix(scriptSrc(reloadTag), "gotth-live-dev-reload.min.js")).To(
				Equal(strings.TrimSuffix(scriptSrc(runtimeTag), "gotth-live.min.js")),
				"the two tags name different mounts: %q and %q", runtimeTag, reloadTag)
			Expect(scriptAttr(reloadTag, "data-gotth-dev-url")).To(Equal(scriptAttr(runtimeTag, "data-gotth-url")),
				"the poll URL and the socket URL are derived from different mounts")
		},
		Entry("the conventional mount", "/live"),
		Entry("a nested mount", "/app/live"),
		Entry("the root", "/"),
		Entry("a trailing slash", "/app/live/"),
	)

	It("HTML-escapes both the mount path and the build identity", func() {
		Expect(render(stampedApp(true, `x"&y`).DevReloadScript("/reports&sect;ion/live"))).To(Equal(
			`<script src="/reports&amp;sect;ion/live/gotth-live-dev-reload.min.js" ` +
				`data-gotth-dev-url="/reports&amp;sect;ion/live" ` +
				`data-gotth-dev-build="x&#34;&amp;y" defer></script>`))
	})

	DescribeTable("rejects a mount path Script would reject, in either mode",
		func(dev bool) {
			app := devReloadApp(dev)
			for _, bad := range []string{"", "live", "//evil.example", `/live\x`, "/live?x", "/live#x"} {
				var buf strings.Builder
				Expect(app.DevReloadScript(bad).Render(context.Background(), &buf)).
					To(HaveOccurred(), "mount path %q was accepted", bad)
				Expect(buf.String()).To(BeEmpty(), "mount path %q wrote a tag before failing", bad)
			}
		},
		Entry("with Dev set", true),
		Entry("with Dev false", false),
	)
})

var _ = Describe("Config.DevBuildID", func() {
	// Validated whatever Dev is, for the reason InspectorScript validates its
	// mount path in both modes: a field checked in only one of them starts
	// failing on the deploy that flips the mode.
	DescribeTable("is refused at New when it cannot survive the round trip, in either mode",
		func(id, wants string) {
			for _, dev := range []bool{true, false} {
				cfg := validConfig()
				cfg.Dev = dev
				cfg.DevBuildID = id

				_, err := live.New(cfg)
				Expect(err).To(HaveOccurred(), "Dev=%v accepted %q", dev, id)

				var cfgErr *live.ConfigError
				Expect(err).To(BeAssignableToTypeOf(cfgErr))
				Expect(err.Error()).To(ContainSubstring("DevBuildID"))
				Expect(err.Error()).To(ContainSubstring(wants))
			}
		},
		Entry("longer than the client will accept", strings.Repeat("a", 129), "128"),
		Entry("a newline, which the client trims and a proxy may rewrite", "build\n7", "control byte"),
		Entry("a tab", "build\t7", "control byte"),
		Entry("trailing whitespace the client trims away", "build-7 ", "whitespace"),
		Entry("leading whitespace", " build-7", "whitespace"),
	)

	It("accepts the empty value, which is what asks for the derived identity", func() {
		cfg := validConfig()
		cfg.DevBuildID = ""
		_, err := live.New(cfg)
		Expect(err).NotTo(HaveOccurred())
	})

	It("accepts a commit hash, which is what the guide tells people to use", func() {
		cfg := validConfig()
		cfg.DevBuildID = "9e1f0c3a4b5d6e7f8091a2b3c4d5e6f708192a3b"
		_, err := live.New(cfg)
		Expect(err).NotTo(HaveOccurred())
	})
})

var _ = Describe("The dev-reload routes", func() {
	It("serves the dev-reload client when Dev is set", func() {
		resp, err := http.Get(serving("/", true).URL + "/gotth-live-dev-reload.min.js")
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())

		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Expect(body).NotTo(BeEmpty(), "the embedded dev-reload client is empty")
		Expect(resp.Header.Get("Content-Type")).To(HavePrefix("text/javascript"))
		Expect(resp.Header.Get("ETag")).NotTo(BeEmpty())
		Expect(resp.Header.Get("Cache-Control")).To(ContainSubstring("immutable"))
	})

	It("404s the dev-reload client when Dev is false, naming the switch", func() {
		resp, err := http.Get(serving("/", false).URL + "/gotth-live-dev-reload.min.js")
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())

		Expect(resp.StatusCode).To(Equal(http.StatusNotFound))
		Expect(string(body)).To(ContainSubstring("live.Config.Dev"),
			"FR-58: the error names the actionable next step")
	})

	It("answers the build identity when Dev is set, and refuses to let it be cached", func() {
		resp, err := http.Get(stampedServing("/", true, "build-7").URL + "/gotth-live-dev-build")
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())

		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Expect(string(body)).To(Equal("build-7"))
		Expect(resp.Header.Get("Content-Type")).To(HavePrefix("text/plain"))
		// The one route in this library that must never be cached. A 304 or a
		// proxy's stored copy is a page that never reloads, which is the
		// failure this whole feature exists to prevent.
		Expect(resp.Header.Get("Cache-Control")).To(Equal("no-store"))
		Expect(resp.Header.Get("ETag")).To(BeEmpty(),
			"an ETag on this route invites exactly the conditional request that must not be satisfied")
	})

	It("404s the build identity when Dev is false, naming the switch", func() {
		resp, err := http.Get(stampedServing("/", false, "build-7").URL + "/gotth-live-dev-build")
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())

		Expect(resp.StatusCode).To(Equal(http.StatusNotFound))
		Expect(string(body)).NotTo(ContainSubstring("build-7"),
			"a production build disclosed its build identity in the body that refused to disclose it")
		Expect(string(body)).To(ContainSubstring("live.Config.Dev"))
	})

	It("serves both dev routes at whatever prefix the application is mounted under", func() {
		ts := serving("/app/", true)

		for _, path := range []string{"/app/gotth-live-dev-reload.min.js", "/app/gotth-live-dev-build"} {
			resp, err := http.Get(ts.URL + path)
			Expect(err).NotTo(HaveOccurred())
			resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(http.StatusOK), "path %q", path)
		}
	})

	// Four names now end in ".min.js" and the routing is suffix matching, so
	// the thing worth pinning is that no request is answered with another
	// artifact's bytes.
	It("keeps the three JavaScript artifacts distinct", func() {
		ts := serving("/", true)

		etags := map[string]string{}
		for _, file := range []string{"gotth-live.min.js", "gotth-live-inspector.min.js", "gotth-live-dev-reload.min.js"} {
			resp, err := http.Get(ts.URL + "/" + file)
			Expect(err).NotTo(HaveOccurred())
			resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(http.StatusOK), "file %q", file)
			etags[file] = resp.Header.Get("ETag")
		}

		Expect(etags["gotth-live.min.js"]).NotTo(Equal(etags["gotth-live-dev-reload.min.js"]))
		Expect(etags["gotth-live-inspector.min.js"]).NotTo(Equal(etags["gotth-live-dev-reload.min.js"]))
	})

	It("refuses a method other than GET or HEAD on both dev routes", func() {
		ts := serving("/", true)

		for _, path := range []string{"/gotth-live-dev-reload.min.js", "/gotth-live-dev-build"} {
			resp, err := http.Post(ts.URL+path, "text/plain", nil)
			Expect(err).NotTo(HaveOccurred())
			resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(http.StatusMethodNotAllowed), "path %q", path)
			Expect(resp.Header.Get("Allow")).To(Equal("GET, HEAD"))
		}
	})

	It("answers HEAD on the build identity with the length and no body", func() {
		req, err := http.NewRequest(http.MethodHead, stampedServing("/", true, "build-7").URL+"/gotth-live-dev-build", nil)
		Expect(err).NotTo(HaveOccurred())

		resp, err := http.DefaultClient.Do(req)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()

		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Expect(resp.Header.Get("Content-Length")).To(Equal("7"))
	})
})

var _ = Describe("The derived build identity", func() {
	// The half that is checkable in-process: the identity of one running
	// binary does not move. If it did, every poll would look like a new build
	// and the page would reload in a loop.
	It("is stable for the life of a process, and equal across applications in it", func() {
		first := scriptAttr(render(devReloadApp(true).DevReloadScript("/live")), "data-gotth-dev-build")
		second := scriptAttr(render(devReloadApp(true).DevReloadScript("/live")), "data-gotth-dev-build")

		Expect(first).To(Equal(second))
		Expect(first).NotTo(BeEmpty())
	})

	It("is overridden by Config.DevBuildID, which is what -ldflags plugs into", func() {
		derived := scriptAttr(render(devReloadApp(true).DevReloadScript("/live")), "data-gotth-dev-build")
		Expect(scriptAttr(render(stampedApp(true, "abc123").DevReloadScript("/live")), "data-gotth-dev-build")).
			To(And(Equal("abc123"), Not(Equal(derived))))
	})

	// The half no in-process comparison can reach on its own: WHERE the value
	// comes from. It must move when the executable moves and not otherwise,
	// and the way to assert that without building two binaries is to assert
	// the derivation itself — this spec hashes the executable running the
	// suite and demands the rendered tag carry that hash.
	//
	// What follows from it, and is therefore not separately asserted: a
	// rebuild produces different bytes and so a different identity, and a
	// restart of the same binary produces the same bytes and so the same
	// identity. Both are properties of the file, not of this library. The
	// end-to-end demonstration in docs/guide/dev-reload.md is where they are
	// actually observed against a running application and a real browser.
	It("is the running executable's own SHA-256 prefix", func() {
		path, err := os.Executable()
		if err != nil {
			Skip("os.Executable is unsupported here, which is the case the process- fallback exists for")
		}
		f, err := os.Open(path)
		Expect(err).NotTo(HaveOccurred())
		defer f.Close()

		sum := sha256.New()
		_, err = io.Copy(sum, f)
		Expect(err).NotTo(HaveOccurred())
		want := hex.EncodeToString(sum.Sum(nil)[:12])

		Expect(scriptAttr(render(devReloadApp(true).DevReloadScript("/live")), "data-gotth-dev-build")).
			To(Equal(want),
				"the identity is not derived from the executable, so a rebuild need not change it")
	})

	// The identity a production build never computes. Hashing an executable is
	// cheap but it is not free, and a field documented as "read only when Dev
	// is set" that is nevertheless computed at startup is a claim that has
	// stopped being true.
	It("is not disclosed by a production build, on either route or in the tag", func() {
		Expect(render(devReloadApp(false).DevReloadScript("/live"))).To(BeEmpty())

		resp, err := http.Get(serving("/", false).URL + "/gotth-live-dev-build")
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusNotFound))
	})
})
