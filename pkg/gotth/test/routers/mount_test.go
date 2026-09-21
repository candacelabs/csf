package routers

import (
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// FR-33 — "The library MUST expose ordinary http.Handler values and MUST NOT
// require a specific router or framework. Verified by mounting the counter
// example under net/http, chi, and gin in the test suite."
//
// Four properties per router, and the third is the one that makes this a
// mounting suite rather than a static-file suite:
//
//  1. the rendered tag names a path on THIS server — no authority — and a
//     data-gotth-url that is the prefix the handler was actually mounted at;
//  2. that src is answered, by this router, with the client runtime;
//  3. a live SESSION runs through the prefix: the WebSocket the tag names
//     opens, negotiates gotth-live.v1, delivers the Snapshot, and carries an
//     event to a patch;
//  4. and nothing answers at the other two routers' prefixes, because the
//     mount is where the caller said and nowhere else.
//
// The prefixes are /live, /app/live and /ui/gotth: three distinct strings, two
// of which are not /live (L9-1 condition C-23). A suite that mounted all three
// at /live would satisfy FR-33's sentence and prove nothing about prefixes,
// and a hardcoded mount inside the library would sail through it.

var _ = Describe("An application mounted under a router", func() {
	for _, spec := range mounts {
		Describe(spec.router+" at "+spec.prefix, func() {
			It("renders a tag naming a path on this server and the prefix it is mounted at", func() {
				app := spec.mount()
				tag := app.tag()

				src := attr(tag, "src")
				url := attr(tag, "data-gotth-url")

				// HavePrefix("/") alone is the predicate that was wrong, and
				// it was the predicate the FR-33 table asserted: it PASSES for
				// "//evil.example/live/gotth-live.min.js", a src a browser
				// fetches from another origin entirely, opening this session's
				// WebSocket there too. The property FR-33 wants is that the
				// src has no authority, so both halves are asserted (C-27
				// §A.6.3(4)). Removing the second line does not make this spec
				// fail today — normalizeMount's "//" clause is what keeps the
				// input away — which is exactly why it is here: it is the
				// assertion that survives that clause being removed.
				Expect(src).To(HavePrefix("/"))
				Expect(src).NotTo(HavePrefix("//"),
					"a src beginning // names an authority, not a path on this server")

				Expect(url).To(Equal(spec.prefix),
					"data-gotth-url is where the browser opens the session, and %s mounted the handler at %s",
					spec.router, spec.prefix)
				Expect(src).To(Equal(url+"/"+runtimeFile),
					"the two attributes must name one mount; %q and %q disagree", src, url)
			})

			It("answers that src with the client runtime", func() {
				app := spec.mount()
				src := attr(app.tag(), "src")

				resp, body := app.get(src)

				Expect(resp.StatusCode).To(Equal(http.StatusOK),
					"the page tells the browser to fetch %s and the %s mount answers %d there",
					src, spec.router, resp.StatusCode)
				Expect(resp.Header.Get("Content-Type")).To(HavePrefix("text/javascript"))
				Expect(body).NotTo(BeEmpty(), "the embedded runtime is empty")
			})

			// The assertion that makes this suite about mounting. A static
			// file reachable at a prefix proves the ServeMux inside the
			// handler matched a suffix; it does not prove a session can be
			// established there. This opens the WebSocket the page names, at
			// the prefix the page names, and drives one event through it.
			It("runs a live session through the prefix the tag names", func() {
				app := spec.mount()
				url := attr(app.tag(), "data-gotth-url")

				browser := app.open(url)
				Expect(browser.conn.Subprotocol()).To(Equal(subprotocol),
					"the server must select gotth-live.v1, not fall back to no subprotocol")

				snapshot := browser.snapshot(5 * time.Second)
				html, ok := snapshot.Patch.fragment(fragmentBadge)
				Expect(ok).To(BeTrue(), "the Snapshot carried %v", fragmentIDs(snapshot.Patch))
				Expect(html).To(Equal("<b>bumps 0</b>"))

				browser.send(eventBump)

				patch := browser.next(5 * time.Second)
				Expect(patch.Kind).To(Equal("patch"), "got a %s", patch.describe())
				Expect(patch.Patch.Origin.Kind).To(BeEquivalentTo(originClientEvent))
				Expect(patch.Patch.Origin.Source).To(Equal("event:" + eventBump))

				html, ok = patch.Patch.fragment(fragmentBadge)
				Expect(ok).To(BeTrue(), "the patch carried %v", fragmentIDs(patch.Patch))
				Expect(html).To(Equal("<b>bumps 1</b>"),
					"the event reached the reducer and the patch came back, through %s", url)
			})

			It("answers 404 everywhere the application is not mounted", func() {
				app := spec.mount()

				for _, other := range mounts {
					if other.prefix == spec.prefix {
						continue
					}

					resp, _ := app.get(other.prefix)
					Expect(resp.StatusCode).To(Equal(http.StatusNotFound),
						"%s mounted the application at %s and answered %d at %s",
						spec.router, spec.prefix, resp.StatusCode, other.prefix)

					runtime := other.prefix + "/" + runtimeFile
					resp, _ = app.get(runtime)
					Expect(resp.StatusCode).To(Equal(http.StatusNotFound),
						"%s served the runtime at %s, which is not its mount",
						spec.router, runtime)

					Expect(app.dialFails(other.prefix)).To(HaveOccurred(),
						"%s accepted a session at %s, which is not its mount",
						spec.router, other.prefix)
				}
			})
		})
	}

	// The artifact does not vary with the mount. Each router is asked for the
	// src its OWN tag renders, so three different paths are fetched, and the
	// bytes are required to be one file: the runtime is embedded in the
	// library, not assembled per prefix.
	It("serves one embedded artifact regardless of which router or prefix", func() {
		bodies := make(map[string][]byte, len(mounts))
		paths := make(map[string]string, len(mounts))

		for _, spec := range mounts {
			app := spec.mount()
			src := attr(app.tag(), "src")

			resp, body := app.get(src)
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(body).NotTo(BeEmpty())

			bodies[spec.router] = body
			paths[spec.router] = src
		}

		reference := mounts[0].router
		for _, spec := range mounts[1:] {
			Expect(bodies[spec.router]).To(Equal(bodies[reference]),
				"%s served %d bytes at %s and %s served %d at %s",
				spec.router, len(bodies[spec.router]), paths[spec.router],
				reference, len(bodies[reference]), paths[reference])
		}
	})
})
