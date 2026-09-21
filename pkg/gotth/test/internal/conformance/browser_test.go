package conformance_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"runtime"
	"sort"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/patience"
)

// ---------------------------------------------------------------------------
// The three criteria that only a browser can answer.
//
// CP1-01  the counter working in a real browser: click → morph → visible change
// CP1-08  event→paint latency, as distinct from event→patch
// CP1-13  the runtime functioning under a strict Content-Security-Policy
//
// All three were reported unverifiable in the first gate report because no
// project image had a browser. Chromium is now in the bench image, so they are
// answered here. Run them with:
//
//	docker run --rm -v "$PWD:/workspace" -w /workspace/candace/pkg/gotth \
//	    dis-gotth-live-bench:latest \
//	    bash -c 'GOTTHLIVE_E2E=1 go test ./test/... -v -args -ginkgo.label-filter=browser -ginkgo.v'
//
// They carry both the browser and e2e labels: they need a browser AND they
// compile and run examples/counter.
// ---------------------------------------------------------------------------

// benchMarkers is the instrumentation the counter's own markup already
// carries — data-bench-id on the value and the buttons. QA-1 depends on it
// rather than on class names, because it is the attribute the example commits
// to for exactly this purpose.
const (
	selValue = `[data-bench-id="value"]`
	selInc   = `[data-bench-id="inc"]`
)

// cspBootBudget is how long the boot is watched for a policy violation. It is
// the wall clock the runtime gets to connect, subscribe and take its first
// patch on a shared VM, and it is spent asserting rather than sleeping.
var cspBootBudget = patience.Budget{Within: 2 * time.Second, Interval: 200 * time.Millisecond}

// waitLive blocks until the live session has patched at least once, which is
// the only honest signal that the socket is up and the runtime is driving.
//
// CS-9 verdict: this stays on Ginkgo's own Eventually. Both polls genuinely
// produce a bool — "does the node exist", "did clicking move the number" —
// so patience's typed shell would carry a bool and its failure would print
// `false`, which is exactly what BeTrue already prints. The typed primitive
// earns its place where the poll has a value worth seeing; here there is not
// one, and the descriptions carry the meaning instead.
func waitLive(c *chrome) {
	GinkgoHelper()
	Eventually(func() bool {
		return c.evalBool(`!!document.querySelector(` + jsStr(selValue) + `)`)
	}, 30*time.Second, 100*time.Millisecond).Should(BeTrue(), "the counter page never rendered")

	// The runtime attaches its click bindings after the socket opens. Proving
	// that is one click: fire it until the number moves.
	Eventually(func() bool {
		before := c.evalString(`document.querySelector(` + jsStr(selValue) + `).textContent.trim()`)
		c.evalJSON(`document.querySelector(`+jsStr(selInc)+`).click(), null`, nil)
		// CS-9 keep: pacing inside a poll, not a wait. The poll's own retry is
		// the wait; this is the click's round trip to the server and back, and
		// reading the value in the same breath as the click would compare the
		// number to itself.
		time.Sleep(150 * time.Millisecond)
		after := c.evalString(`document.querySelector(` + jsStr(selValue) + `).textContent.trim()`)
		return after != before
	}, 30*time.Second, 250*time.Millisecond).Should(BeTrue(),
		"clicking never changed the value: the live connection is not driving the DOM")
}

// jsStr renders a Go string as a JavaScript string literal.
//
// It goes through the JSON encoder rather than wrapping in quotes, because
// every selector in this file contains double quotes and the naive version
// produced `"[data-bench-id="inc"]"` — a syntax error the page reported and
// the first run of this suite failed on.
func jsStr(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

var _ = Describe("The counter in a real browser", Label("browser", "e2e"), func() {

	// CP1-01. The whole point of the library, asserted where it actually has
	// to be true: a click leaves the browser, the server re-renders, and the
	// fragment is MORPHED into the live DOM rather than replaced.
	It("carries a click to a visible DOM change, by morph, preserving focus", func() {
		e2eOnly()
		server := startCounter()
		c := launchChrome()

		c.navigate("http://" + server.addr + "/")
		waitLive(c)

		// Node identity is the mechanism behind every FR-25 case. An expando
		// set on a live node survives a morph and cannot survive a replace, so
		// it distinguishes the two without trusting anything the library says
		// about itself.
		c.evalJSON(`(() => {
			const v = document.querySelector(`+jsStr(selValue)+`);
			v.__qaMark = "survived";
			document.querySelector(`+jsStr(selInc)+`).focus();
			return null;
		})()`, nil)

		Expect(c.evalString(`document.activeElement.getAttribute("data-bench-id")`)).To(Equal("inc"))
		before := c.evalString(`document.querySelector(` + jsStr(selValue) + `).textContent.trim()`)

		var result struct {
			After  string `json:"after"`
			Mark   string `json:"mark"`
			Active string `json:"active"`
		}
		c.evalJSON(`(async () => {
			const val = () => document.querySelector(`+jsStr(selValue)+`).textContent.trim();
			const before = val();
			const changed = new Promise(resolve => {
				const obs = new MutationObserver(() => {
					if (val() !== before) { obs.disconnect(); resolve(); }
				});
				obs.observe(document.body, {subtree: true, childList: true, characterData: true});
				setTimeout(() => { obs.disconnect(); resolve(); }, 10000);
			});
			document.querySelector(`+jsStr(selInc)+`).click();
			await changed;
			const v = document.querySelector(`+jsStr(selValue)+`);
			return {
				after: val(),
				mark: v.__qaMark || "",
				active: (document.activeElement && document.activeElement.getAttribute("data-bench-id")) || "",
			};
		})()`, &result)

		Expect(result.After).NotTo(Equal(before),
			"the click produced no visible change in the browser")

		Expect(result.Mark).To(Equal("survived"),
			"the value element was REPLACED, not morphed: an expando set on the live node is gone, "+
				"so focus, caret, scroll and media position could not have survived either (FR-24)")

		Expect(result.Active).To(Equal("inc"),
			"focus moved during the patch: the focused button did not survive the morph (FR-25)")

		AddReportEntry("CP1-01 — counter in a real browser", fmt.Sprintf(
			"browser %s\nvalue %s → %s\nnode identity preserved across morph: yes\nfocus preserved: yes",
			c.version, before, result.After))
	})

	// CP1-08. Event→PAINT, which is the thing event→patch was explicitly not.
	It("measures event to paint over 220 interactions", func() {
		e2eOnly()
		server := startCounter()
		c := launchChrome()

		c.navigate("http://" + server.addr + "/")
		waitLive(c)

		var samples []float64
		c.evalJSON(`(async () => {
			const val = () => document.querySelector(`+jsStr(selValue)+`).textContent.trim();
			const btn = () => document.querySelector(`+jsStr(selInc)+`);
			const out = [];

			// Warm-up, discarded: the first interactions pay for lazily
			// resolved styles and a cold JIT.
			for (let i = 0; i < 20; i++) {
				const b = val();
				btn().click();
				await new Promise(r => {
					const o = new MutationObserver(() => { if (val() !== b) { o.disconnect(); r(); } });
					o.observe(document.body, {subtree:true, childList:true, characterData:true});
					setTimeout(() => { o.disconnect(); r(); }, 5000);
				});
				await new Promise(r => setTimeout(r, 30));
			}

			for (let i = 0; i < 220; i++) {
				const b = val();
				const painted = new Promise(resolve => {
					const o = new MutationObserver(() => {
						if (val() === b) return;
						o.disconnect();
						// rAF runs BEFORE the frame is painted, so the second
						// one is the first callback that can only run after
						// the frame carrying this change was presented. That
						// is the definition of "paint" used here, and it is
						// stated in the report rather than assumed.
						requestAnimationFrame(() => requestAnimationFrame(() => resolve(performance.now())));
					});
					o.observe(document.body, {subtree:true, childList:true, characterData:true});
					setTimeout(() => { o.disconnect(); resolve(NaN); }, 10000);
				});
				const t0 = performance.now();
				btn().click();
				const t1 = await painted;
				if (!Number.isNaN(t1)) out.push(t1 - t0);

				// Pacing, OUTSIDE the timed interval. The library's default
				// inbound budget is 50 events/s and a loopback round trip is
				// far quicker than that, so an unpaced loop would measure the
				// rate limiter refusing clicks.
				await new Promise(r => setTimeout(r, 30));
			}
			return out;
		})()`, &samples)

		Expect(len(samples)).To(BeNumerically(">=", 200),
			"only %d interactions completed; the criterion asks for at least 200", len(samples))

		sort.Float64s(samples)
		p := func(q int) float64 {
			rank := (q*len(samples) + 99) / 100
			if rank < 1 {
				rank = 1
			}
			return samples[rank-1]
		}
		var sum float64
		for _, s := range samples {
			sum += s
		}

		AddReportEntry("CP1-08 — event→paint (INDICATIVE, loopback, NOT PRD G1)", fmt.Sprintf(`
  samples      %d
  browser      %s
  host         %s/%s, %d cpus, %s

  min          %.2f ms
  p50          %.2f ms
  p95          %.2f ms
  p99          %.2f ms
  max          %.2f ms
  mean         %.2f ms

  Method: performance.now() immediately before dispatching a real click on the
  +1 button, to performance.now() in the second requestAnimationFrame callback
  after a MutationObserver saw the counter's text change. rAF runs before the
  frame is painted, so the second one is the first callback that can only run
  after the frame carrying the change was presented. Spans the full loop:
  click → event frame → refine → authorize → reduce → render → patch frame →
  client morph → paint. 30 ms of pacing sits between iterations, outside the
  timed interval, to stay under the library's default 50 events/s budget.

  Loopback, one host, headless chromium. NOT PRD G1, which is a LAN
  measurement gated at 50 ms p50 / 150 ms p99 and belongs to QA-2 in Phase 5.
  Published as checkpoint 1's measured number, per the Phase 1 exit criterion
  that says Phase 1 measures and records while Phase 5 enforces.`,
			len(samples), c.version, runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), runtime.Version(),
			samples[0], p(50), p(95), p(99), samples[len(samples)-1], sum/float64(len(samples))))

		// Not a gate — G1 is Phase 5's. A sanity bound only.
		Expect(p(50)).To(BeNumerically("<", 1000.0),
			"p50 event→paint of %.1f ms on loopback indicates a defect, not a slow network", p(50))
	})
})

// ---------------------------------------------------------------------------
// CP1-13 — strict CSP
// ---------------------------------------------------------------------------

// strictCSP is the policy FR-49 names, with no unsafe-inline and no
// unsafe-eval anywhere. connect-src is stated explicitly because the live
// session is a WebSocket and a reader should not have to work out whether
// default-src covers it.
const strictCSP = "default-src 'none'; " +
	"script-src 'self'; " +
	"style-src 'self'; " +
	"connect-src 'self'; " +
	"img-src 'self' data:; " +
	"object-src 'none'; " +
	"base-uri 'none'; " +
	"frame-ancestors 'none'"

// cspFront is a proxy that adds a strict CSP header to everything the counter
// serves, including the WebSocket upgrade.
//
// The header is added by a proxy rather than by editing examples/counter,
// because the example is DEV-3's and because FR-49 is a property of the
// RUNTIME, not of any one application's middleware. Proving it here proves it
// for every consumer who sets the header themselves.
type cspFront struct {
	server *httptest.Server
	target *string
}

// newCSPFront starts the proxy before its target exists.
//
// The ordering is forced and is worth stating: the browser will send the
// PROXY's origin on the WebSocket handshake, and the counter's allowlist is
// derived from its own listen address, so the counter has to be started with
// the proxy's URL already known. The proxy therefore comes up first with an
// unset target and is pointed at the counter once it is running.
func newCSPFront() *cspFront {
	GinkgoHelper()

	target := new(string)
	f := &cspFront{target: target}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if *target == "" {
			http.Error(w, "the proxy has no target yet", http.StatusServiceUnavailable)
			return
		}
		upstream, err := url.Parse("http://" + *target)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Security-Policy", strictCSP)
		httputil.NewSingleHostReverseProxy(upstream).ServeHTTP(w, r)
	}))
	DeferCleanup(f.server.Close)
	return f
}

func (f *cspFront) pointAt(addr string) { *f.target = addr }

func (f *cspFront) url() string { return f.server.URL }

var _ = Describe("The client runtime under a strict Content-Security-Policy", Label("browser", "e2e"), func() {

	// CP1-13, FR-49. The no-eval half is a static scan and was always green;
	// this is the half that needs a browser, because a CSP violation is
	// something the browser decides and reports.
	It("loads, connects and patches with no policy violation", func() {
		e2eOnly()
		front := newCSPFront()
		server := startCounter(front.url())
		front.pointAt(server.addr)
		c := launchChrome()

		// Installed before any page script, so a violation raised while the
		// runtime is still booting is caught rather than missed.
		c.onNewDocument(`
			window.__cspViolations = [];
			document.addEventListener("securitypolicyviolation", e => {
				window.__cspViolations.push(
					e.violatedDirective + " blocked " + (e.blockedURI || "(inline)"));
			});
		`)

		// The header really is on the document the browser fetched. Checked
		// from Go, because it is the authoritative statement of what was
		// served and cannot be confused with what the page then did.
		resp, err := http.Get(front.url() + "/")
		Expect(err).NotTo(HaveOccurred())
		defer func() { Expect(resp.Body.Close()).To(Succeed()) }()
		served := resp.Header.Get("Content-Security-Policy")
		Expect(served).To(Equal(strictCSP))
		Expect(served).NotTo(ContainSubstring("unsafe-inline"))
		Expect(served).NotTo(ContainSubstring("unsafe-eval"))

		c.navigate(front.url() + "/")

		// Violations are read BEFORE liveness is asserted, and deliberately.
		// If the policy blocks the runtime, the useful failure names the
		// directive that blocked it; asserting liveness first would report
		// only that clicking did nothing.
		//
		// This used to be a two-second sleep and one read, which samples the
		// claim once and at the least informative moment: a violation raised
		// at 200ms and a violation raised at 1.9s are the same failure, and
		// only the second of them survived to be read. Consistently is that
		// same two seconds spent asserting the absence throughout, and it
		// fails at the directive that broke it rather than at whatever the
		// list happened to hold when the sleep ended.
		patience.Consistently(GinkgoTB(),
			fmt.Sprintf("the runtime to boot under %q raising no Content-Security-Policy violation", strictCSP),
			cspBootBudget,
			func() []string {
				var raised []string
				c.evalJSON(`window.__cspViolations`, &raised)
				return raised
			},
			func(raised []string) bool { return len(raised) == 0 })

		waitLive(c)

		// Violations raised while the runtime connected and patched.
		// This is the assertion CP1-13 is actually about.
		var violations []string
		c.evalJSON(`window.__cspViolations`, &violations)
		Expect(violations).To(BeEmpty(),
			"the runtime raised Content-Security-Policy violations under %q: %v", strictCSP, violations)

		// Now prove the policy was being enforced, so the clean result above
		// cannot be a policy the browser ignored.
		//
		// This is done by injecting an inline <script> through the DOM rather
		// than by calling eval through the debugger: CDP's Runtime.evaluate
		// runs in a privileged world that bypasses CSP entirely, so an
		// eval-based probe reports success under any policy and proves
		// nothing. The first draft of this spec used one and was wrong.
		var probe struct {
			InlineRan  bool     `json:"inlineRan"`
			Violations []string `json:"violations"`
		}
		c.evalJSON(`(async () => {
			const before = window.__cspViolations.length;
			const s = document.createElement("script");
			s.textContent = "window.__qaInlineRan = true;";
			document.head.appendChild(s);
			await new Promise(r => setTimeout(r, 250));
			return {
				inlineRan: !!window.__qaInlineRan,
				violations: window.__cspViolations.slice(before),
			};
		})()`, &probe)

		Expect(probe.InlineRan).To(BeFalse(),
			"an injected inline script executed, so script-src 'self' was not enforced "+
				"and the zero-violation result above is vacuous")
		Expect(probe.Violations).NotTo(BeEmpty(),
			"blocking the inline script raised no securitypolicyviolation event, "+
				"so the violation listener this spec relies on does not work")

		// And it still works: the socket connected and the DOM is patching,
		// which waitLive already required, so re-state it as the report line.
		value := c.evalString(`document.querySelector(` + jsStr(selValue) + `).textContent.trim()`)

		AddReportEntry("CP1-13 — strict CSP", fmt.Sprintf(
			"policy: %s\nbrowser: %s\nenforcement proven by a blocked inline script: yes\nruntime violations: 0\nlive value after patching: %s",
			strictCSP, c.version, value))
	})
})
