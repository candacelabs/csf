package conformance_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/live"
)

// ---------------------------------------------------------------------------
// FR-30, FR-31, FR-32, G8 — gotth-live and HTMX on the same server and on the
// same page, asserted with the real HTMX.
//
// Run:
//
//	docker run --rm -v "$PWD:/repo" -w /repo/gotth-live \
//	    dis-gotth-live-bench:latest \
//	    bash -c 'go test ./test/... -count=1 -args -ginkgo.label-filter=browser -ginkgo.v'
//
// # Why the real HTMX and not a stub
//
// The interop question is not "does morph leave hx-* attributes in the DOM" —
// that is a string comparison and the node-level suite already makes it. It is
// "does HTMX still WORK on a node gotth-live has morphed", and HTMX's answer
// depends on internal per-node state it keeps outside the attributes. Only
// HTMX can answer that, so htmx.min.js is vendored beside this file. See
// testdata/README.md for the version, the origin and the digest; the digest is
// re-checked at test time by htmxBundle below, so a suite running against
// different bytes than the ones recorded fails rather than reports.
// ---------------------------------------------------------------------------

// The vendored artifact and its recorded digest. Checking the digest from the
// suite rather than only in a README is the difference between provenance that
// is documented and provenance that is enforced.
const (
	htmxVersion = "2.0.10"
	htmxFile    = "testdata/htmx-2.0.10.min.js"
	htmxSHA256  = "71ea67185bfa8c98c39d31717c6fce5d852370fcdfd129db4543774d3145c0de"
)

// htmxBundle reads the vendored file and verifies its digest.
func htmxBundle() []byte {
	GinkgoHelper()
	path, err := filepath.Abs(htmxFile)
	Expect(err).NotTo(HaveOccurred())
	b, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred(), "the vendored HTMX bundle is missing from %s", path)
	sum := sha256.Sum256(b)
	Expect(hex.EncodeToString(sum[:])).To(Equal(htmxSHA256),
		"the vendored HTMX bundle at %s is not the artifact this suite recorded: "+
			"testdata/README.md names htmx %s with digest %s", path, htmxVersion, htmxSHA256)
	return b
}

// ---------------------------------------------------------------------------
// The application: one live region, several HTMX regions, on one page
// ---------------------------------------------------------------------------

const (
	fragHX      = "hx.live"
	eventHXTick = "hx.tick"
)

type hxState struct{ Tick int }

// hxRegionHTML is the live fragment. It holds four HTMX elements, chosen so
// that each one answers a different clause of FR-31 and FR-32:
//
//   - #owned-slot   hx-* inside the live fragment with NO data-gotth-preserve.
//     FR-32's precedence rule says it is server-owned: morph
//     overwrites it and any swap into it is reverted.
//   - #vault        data-gotth-preserve hosting an HTMX region. The sanctioned
//     way to put HTMX-owned DOM inside a live region (FR-27).
//   - #survivor     an hx-* control present at first paint. HTMX processed it
//     at load; morph must not stop it working.
//   - #newcomer     an hx-* control the MORPH inserts, which HTMX has never
//     seen. This is the case that finds the interop gap.
func hxRegionHTML(s hxState) string {
	tick := strconv.Itoa(s.Tick)

	var b strings.Builder
	b.WriteString(`<section data-gotth-region="` + fragHX + `">`)
	b.WriteString(`<p id="hx-tick">tick ` + tick + `</p>`)

	b.WriteString(`<div id="owned-slot" hx-get="/hx/frag?who=owned" hx-trigger="click" hx-swap="innerHTML">server ` + tick + `</div>`)

	b.WriteString(`<div id="vault" data-gotth-preserve>`)
	b.WriteString(`<div id="vault-slot" hx-get="/hx/frag?who=vault" hx-trigger="click" hx-swap="innerHTML">server ` + tick + `</div>`)
	b.WriteString(`</div>`)

	b.WriteString(`<button id="survivor" type="button" hx-get="/hx/frag?who=survivor" hx-target="#survivor-out" hx-swap="innerHTML">go</button>`)
	b.WriteString(`<div id="survivor-out">-</div>`)

	if s.Tick >= 1 {
		b.WriteString(`<button id="newcomer" type="button" hx-get="/hx/frag?who=newcomer" hx-target="#newcomer-out" hx-swap="innerHTML">go</button>`)
		b.WriteString(`<div id="newcomer-out">-</div>`)
	}

	b.WriteString(`<button id="tick" type="button" data-gotth-on="click:` + eventHXTick + `">tick</button>`)
	b.WriteString(`</section>`)
	return b.String()
}

// outsideHTML is an HTMX region OUTSIDE every declared live region. FR-31's
// last sentence is about exactly this markup: morph must never touch it.
const outsideHTML = `<div id="outside">` +
	`<button id="outside-btn" type="button" hx-get="/hx/frag?who=outside" hx-target="#outside-slot" hx-swap="innerHTML">go</button>` +
	`<div id="outside-slot">initial</div>` +
	`</div>`

func hxScripts() string {
	return `<script src="/htmx.min.js"></script>`
}

// hxLivePage is the page with both systems on it.
func hxLivePage(s hxState) string {
	return htmlDoc("QA htmx coexistence",
		hxScripts()+scriptTag(),
		outsideHTML+hxRegionHTML(s))
}

// hxPlainPage is the FR-30 page: same server, same router, same layout
// helper, HTMX only, and no gotth-live JavaScript at all.
func hxPlainPage() string {
	return htmlDoc("QA htmx plain page",
		hxScripts(),
		`<p id="plain">plain</p>`+outsideHTML)
}

func hxConfig() live.Config[hxState, qaUser] {
	return live.Config[hxState, qaUser]{
		Init: func(ctx context.Context, session live.Session[qaUser]) (hxState, []live.Effect[qaUser], error) {
			return hxState{}, nil, nil
		},
		Reduce: func(s hxState, ev live.Event) (hxState, []live.Effect[qaUser]) {
			if ev.Name == eventHXTick {
				s.Tick++
			}
			return s, nil
		},
		Fragments: []live.Fragment[hxState]{{
			ID:     fragHX,
			Render: func(s hxState) templ.Component { return raw(hxRegionHTML(s)) },
			Dirty:  func(prev, next hxState) bool { return prev != next },
		}},
		Events:       []string{eventHXTick},
		Authenticate: func(request *http.Request) (qaUser, error) { return qaUser("qa"), nil },
		Authorize:    live.AllowAll[qaUser],
		CSRF:         live.NoCSRFCheck,
	}
}

func startHXApp() *httptest.Server {
	GinkgoHelper()

	bundle := htmxBundle()
	return serveLive(hxConfig(), map[string]http.HandlerFunc{
		"/htmx.min.js": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = w.Write(bundle)
		},
		// The HTMX endpoint. It is a plain HTTP route on the same mux as the
		// live handler, which is half of what FR-30 asks for.
		"/hx/frag": func(w http.ResponseWriter, r *http.Request) {
			who := r.URL.Query().Get("who")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, `<span class="hx">swapped:`+templ.EscapeString(who)+`</span>`)
		},
		"/plain": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, hxPlainPage())
		},
		"/": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, hxLivePage(hxState{}))
		},
	})
}

// hxHelpers is the page-side driver, installed before any page script.
//
// The socket wrapper is the FR-30 instrument: it records every WebSocket the
// page opens, so "no gotth-live JS loaded" can be asserted as "nothing
// connected" rather than as "the markup did not contain a script tag".
const hxHelpers = `
window.__hx = {
  sockets: [],
  prevented: [],
  requests: [],
  // swap clicks an hx-* element and resolves once HTMX has finished the
  // request and settled the swap. It listens for htmx's own event rather than
  // polling for text, so a swap that never happens is a timeout with a reason.
  async swap(sel) {
    const el = document.querySelector(sel);
    if (!el) throw new Error("no element for " + sel);
    const done = new Promise(resolve => {
      const onSettle = () => { document.body.removeEventListener("htmx:afterSettle", onSettle); resolve(true); };
      document.body.addEventListener("htmx:afterSettle", onSettle);
      setTimeout(() => { document.body.removeEventListener("htmx:afterSettle", onSettle); resolve(false); }, 4000);
    });
    el.click();
    return await done;
  },
  async tick(times) {
    const line = () => document.querySelector("#hx-tick").textContent.trim();
    for (let i = 0; i < (times || 1); i++) {
      const before = line();
      document.querySelector("#tick").click();
      const deadline = performance.now() + 15000;
      for (;;) {
        if (line() !== before) break;
        if (performance.now() > deadline) throw new Error("no patch arrived: #hx-tick is still " + JSON.stringify(before));
        await new Promise(r => setTimeout(r, 15));
      }
      await new Promise(r => setTimeout(r, 40));
    }
    return line();
  },
  text(sel) {
    const el = document.querySelector(sel);
    return el ? el.textContent.trim() : "MISSING";
  },
  inner(sel) {
    const el = document.querySelector(sel);
    return el ? el.innerHTML : "MISSING";
  },
  mark(sel, value) {
    const el = document.querySelector(sel);
    if (!el) throw new Error("no element for " + sel);
    el.__qaMark = value;
    return true;
  },
  markOf(sel) {
    const el = document.querySelector(sel);
    return el && el.__qaMark ? el.__qaMark : "";
  },
};

// Record every socket the page opens, before any page script can open one.
(() => {
  const Native = window.WebSocket;
  window.WebSocket = function (url, protocols) {
    window.__hx.sockets.push(String(url));
    return protocols === undefined ? new Native(url) : new Native(url, protocols);
  };
  window.WebSocket.prototype = Native.prototype;
  Object.assign(window.WebSocket, {CONNECTING: 0, OPEN: 1, CLOSING: 2, CLOSED: 3});
})();

// A bubble-phase listener at the document. gotth-live's own listener is at the
// document in the CAPTURE phase, so it runs strictly before this one: if it
// ever called preventDefault on a click it does not own, this records it.
document.addEventListener("click", e => {
  window.__hx.prevented.push({
    target: e.target && e.target.id ? e.target.id : "(anonymous)",
    defaultPrevented: e.defaultPrevented,
  });
}, false);

document.addEventListener("htmx:afterRequest", e => {
  window.__hx.requests.push((e.detail && e.detail.pathInfo && e.detail.pathInfo.requestPath) || "?");
});
`

// waitHXLive blocks until both systems are up: HTMX has loaded and the live
// session is driving the DOM.
func waitHXLive(c *chrome) {
	GinkgoHelper()

	Eventually(func() bool {
		return c.evalBool(`typeof window.htmx === "object" && typeof window.htmx.process === "function"`)
	}, 30*time.Second, 100*time.Millisecond).Should(BeTrue(), "HTMX never loaded on the page")

	Eventually(func() string {
		return c.evalString(`document.documentElement.getAttribute("data-gotth-status") || ""`)
	}, 30*time.Second, 100*time.Millisecond).Should(Equal("live"),
		"the client runtime never reported a live connection")
}

// ---------------------------------------------------------------------------
// FR-30 — coexistence on separate pages
// ---------------------------------------------------------------------------

var _ = Describe("HTMX and gotth-live pages from one server (FR-30, G8)", Ordered, ContinueOnFailure, Label("browser"), func() {
	var (
		c  *chrome
		ts *httptest.Server
	)

	BeforeAll(func() {
		browserOnly()
		ts = startHXApp()
		c = launchChrome()
		c.onNewDocument(hxHelpers)
	})

	It("serves a plain-HTMX page with no gotth-live JavaScript on it", func() {
		// The markup half, checked from Go so it is a statement about what was
		// SERVED and cannot be confused with what the page then did.
		resp, err := http.Get(ts.URL + "/plain")
		Expect(err).NotTo(HaveOccurred())
		defer func() { Expect(resp.Body.Close()).To(Succeed()) }()
		body, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).NotTo(ContainSubstring("gotth-live.min.js"),
			"the plain page carries the gotth-live runtime, which FR-30 forbids")
		Expect(string(body)).NotTo(ContainSubstring("data-gotth-url"))
		Expect(string(body)).To(ContainSubstring("htmx.min.js"))

		// And the live page from the SAME server, router and layout does carry
		// it, so the absence above is a property of the page rather than of a
		// broken helper.
		liveResp, err := http.Get(ts.URL + "/")
		Expect(err).NotTo(HaveOccurred())
		defer func() { Expect(liveResp.Body.Close()).To(Succeed()) }()
		liveBody, err := io.ReadAll(liveResp.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(liveBody)).To(ContainSubstring("gotth-live.min.js"))

		// The browser half. No global, no socket, and HTMX still works.
		c.navigate(ts.URL + "/plain")
		Eventually(func() bool {
			return c.evalBool(`typeof window.htmx === "object"`)
		}, 30*time.Second, 100*time.Millisecond).Should(BeTrue())

		var got struct {
			HasGlobal bool     `json:"hasGlobal"`
			Sockets   []string `json:"sockets"`
			Swapped   bool     `json:"swapped"`
			Slot      string   `json:"slot"`
		}
		c.evalJSON(`(async () => {
			const swapped = await window.__hx.swap("#outside-btn");
			return {
				hasGlobal: typeof window.gotthLive !== "undefined",
				sockets: window.__hx.sockets,
				swapped: swapped,
				slot: window.__hx.text("#outside-slot"),
			};
		})()`, &got)

		Expect(got.HasGlobal).To(BeFalse(),
			"window.gotthLive exists on a page that loaded no gotth-live runtime")
		Expect(got.Sockets).To(BeEmpty(),
			"the plain page opened WebSocket(s) %v; FR-30 says a non-live page loads no live JS at all",
			got.Sockets)
		Expect(got.Swapped).To(BeTrue(), "HTMX did not complete a swap on the plain page")
		Expect(got.Slot).To(Equal("swapped:outside"))

		AddReportEntry("FR-30 plain-HTMX page", fmt.Sprintf(
			"GET /plain: no gotth-live.min.js, no data-gotth-url, window.gotthLive undefined, sockets opened %v\n"+
				"HTMX swap on the same page: %q\nGET / from the same mux does carry the runtime",
			got.Sockets, got.Slot))
	})

	It("serves a live page from the same mux, and both systems boot on it", func() {
		c.navigate(ts.URL + "/")
		waitHXLive(c)

		var got struct {
			Sockets []string `json:"sockets"`
			Status  string   `json:"status"`
			Swapped bool     `json:"swapped"`
			Slot    string   `json:"slot"`
			Tick    string   `json:"tick"`
		}
		c.evalJSON(`(async () => {
			const swapped = await window.__hx.swap("#outside-btn");
			const tick = await window.__hx.tick(1);
			return {
				sockets: window.__hx.sockets,
				status: document.documentElement.getAttribute("data-gotth-status"),
				swapped: swapped,
				slot: window.__hx.text("#outside-slot"),
				tick: tick,
			};
		})()`, &got)

		Expect(got.Sockets).To(HaveLen(1), "expected exactly one live socket, got %v", got.Sockets)
		Expect(got.Sockets[0]).To(ContainSubstring(liveMount))
		Expect(got.Swapped).To(BeTrue())
		Expect(got.Slot).To(Equal("swapped:outside"))
		Expect(got.Status).To(Equal("live"))
		Expect(got.Tick).To(Equal("tick 1"),
			"the live session did not patch after an HTMX swap on the same page")

		AddReportEntry("FR-30/G8 both on one server", fmt.Sprintf(
			"live page: socket %v, status %q, HTMX swap %q, live patch %q",
			got.Sockets, got.Status, got.Slot, got.Tick))
	})
})

// ---------------------------------------------------------------------------
// FR-31 — coexistence on the same page
// ---------------------------------------------------------------------------

var _ = Describe("A live region and HTMX regions on one page (FR-31, G8)", Ordered, ContinueOnFailure, Label("browser"), func() {
	var (
		c  *chrome
		ts *httptest.Server
	)

	BeforeAll(func() {
		browserOnly()
		ts = startHXApp()
		c = launchChrome()
		c.onNewDocument(hxHelpers)
	})

	BeforeEach(func() {
		browserOnly()
		c.navigate(ts.URL + "/")
		waitHXLive(c)
	})

	It("does not touch an HTMX region outside every declared live region", func() {
		var got struct {
			Swapped  bool   `json:"swapped"`
			Slot     string `json:"slot"`
			SlotMark string `json:"slotMark"`
			OutMark  string `json:"outMark"`
			Tick     string `json:"tick"`
		}
		c.evalJSON(`(async () => {
			window.__hx.mark("#outside", "outside-root");
			const swapped = await window.__hx.swap("#outside-btn");
			// The swap replaced the slot's children, so mark what HTMX put
			// there: this is the content a stray morph would revert.
			window.__hx.mark("#outside-slot", "outside-slot");
			const tick = await window.__hx.tick(2);
			return {
				swapped: swapped,
				slot: window.__hx.text("#outside-slot"),
				slotMark: window.__hx.markOf("#outside-slot"),
				outMark: window.__hx.markOf("#outside"),
				tick: tick,
			};
		})()`, &got)

		Expect(got.Swapped).To(BeTrue(), "HTMX never swapped, so nothing was at risk")
		Expect(got.Tick).To(Equal("tick 2"), "the live region was not patched, so morph never ran")

		Expect(got.Slot).To(Equal("swapped:outside"),
			"two live patches reverted HTMX-swapped content OUTSIDE every declared region (FR-31)")
		Expect(got.SlotMark).To(Equal("outside-slot"),
			"the node HTMX owns outside the live region was replaced by morph (FR-31)")
		Expect(got.OutMark).To(Equal("outside-root"))

		AddReportEntry("FR-31 outside a region", fmt.Sprintf(
			"HTMX swapped #outside-slot to %q, then two live patches ran; content and node identity both intact",
			got.Slot))
	})

	It("neither intercepts nor cancels an hx-* click", func() {
		var got struct {
			Prevented []struct {
				Target           string `json:"target"`
				DefaultPrevented bool   `json:"defaultPrevented"`
			} `json:"prevented"`
			Requests []string `json:"requests"`
			Slot     string   `json:"slot"`
			Owned    string   `json:"owned"`
		}
		c.evalJSON(`(async () => {
			window.__hx.prevented = [];
			window.__hx.requests = [];
			await window.__hx.swap("#outside-btn");
			await window.__hx.swap("#owned-slot");
			return {
				prevented: window.__hx.prevented,
				requests: window.__hx.requests,
				slot: window.__hx.text("#outside-slot"),
				owned: window.__hx.text("#owned-slot"),
			};
		})()`, &got)

		Expect(got.Requests).To(HaveLen(2),
			"expected two HTMX requests, saw %v — the runtime may have cancelled one", got.Requests)
		Expect(got.Prevented).To(HaveLen(2))
		for _, ev := range got.Prevented {
			Expect(ev.DefaultPrevented).To(BeFalse(),
				"gotth-live's capture-phase document listener called preventDefault on a click "+
					"targeting %q, which it does not own (FR-31)", ev.Target)
		}
		Expect(got.Slot).To(Equal("swapped:outside"))
		Expect(got.Owned).To(Equal("swapped:owned"),
			"an hx-get INSIDE a live region did not reach the server: gotth-live rewrote or cancelled it (FR-31)")

		AddReportEntry("FR-31 no interception", fmt.Sprintf(
			"two hx-* clicks — one outside a live region, one inside — both reached the server (%v) and "+
				"neither was defaultPrevented by gotth-live's capture-phase listener", got.Requests))
	})

	It("keeps the live session working across HTMX swaps", func() {
		var got struct {
			Status string `json:"status"`
			Tick   string `json:"tick"`
			Slot   string `json:"slot"`
		}
		c.evalJSON(`(async () => {
			for (let i = 0; i < 3; i++) { await window.__hx.swap("#outside-btn"); }
			const tick = await window.__hx.tick(1);
			return {
				status: document.documentElement.getAttribute("data-gotth-status"),
				tick: tick,
				slot: window.__hx.text("#outside-slot"),
			};
		})()`, &got)

		Expect(got.Status).To(Equal("live"),
			"the live connection did not survive three HTMX swaps: status is %q", got.Status)
		Expect(got.Tick).To(Equal("tick 1"))
		Expect(got.Slot).To(Equal("swapped:outside"))
	})
})

// ---------------------------------------------------------------------------
// FR-32 — ownership is declared, not inferred
// ---------------------------------------------------------------------------

var _ = Describe("The declared ownership boundary (FR-32, R-11)", Ordered, ContinueOnFailure, Label("browser"), func() {
	var (
		c  *chrome
		ts *httptest.Server
	)

	BeforeAll(func() {
		browserOnly()
		ts = startHXApp()
		c = launchChrome()
		c.onNewDocument(hxHelpers)
	})

	BeforeEach(func() {
		browserOnly()
		c.navigate(ts.URL + "/")
		waitHXLive(c)
	})

	// FR-32 permits a developer-facing error OR a documented, tested
	// precedence rule. RFC-0001 §10.3 chose the rule and wrote it down:
	//
	//   "An hx-* element inside a live fragment WITHOUT data-gotth-preserve is
	//    server-owned: morph will overwrite it, and any HTMX swap into it will
	//    be reverted by the next patch."
	//
	// This is that sentence, executed. It is deliberately asserted as the
	// documented outcome and not as a bug: an undefined outcome is what FR-32
	// forbids, and "reverted" is defined.
	It("reverts an HTMX swap into an unpreserved element inside a live fragment", func() {
		var got struct {
			Swapped     bool   `json:"swapped"`
			AfterSwap   string `json:"afterSwap"`
			AfterPatch  string `json:"afterPatch"`
			Mark        string `json:"mark"`
			InnerBefore string `json:"innerBefore"`
			InnerAfter  string `json:"innerAfter"`
		}
		c.evalJSON(`(async () => {
			window.__hx.mark("#owned-slot", "owned-node");
			const swapped = await window.__hx.swap("#owned-slot");
			const afterSwap = window.__hx.text("#owned-slot");
			const innerBefore = window.__hx.inner("#owned-slot");
			await window.__hx.tick(1);
			return {
				swapped: swapped,
				afterSwap: afterSwap,
				afterPatch: window.__hx.text("#owned-slot"),
				mark: window.__hx.markOf("#owned-slot"),
				innerBefore: innerBefore,
				innerAfter: window.__hx.inner("#owned-slot"),
			};
		})()`, &got)

		Expect(got.Swapped).To(BeTrue())
		Expect(got.AfterSwap).To(Equal("swapped:owned"),
			"HTMX did not manage to swap into the element at all, so the precedence rule was never tested")

		Expect(got.AfterPatch).To(Equal("server 1"),
			"RFC-0001 §10.3's precedence rule says an unpreserved hx-* element inside a live fragment "+
				"is server-owned and the swap is reverted by the next patch; the element still reads %q",
			got.AfterPatch)
		Expect(got.Mark).To(Equal("owned-node"),
			"the element itself was replaced rather than morphed, so the revert happened by demolition")

		AddReportEntry("FR-32 precedence — unpreserved is server-owned", fmt.Sprintf(
			"#owned-slot: server %q -> HTMX swap %q -> next live patch %q\n"+
				"innerHTML %q -> %q; the element itself was morphed, not replaced",
			"server 0", got.AfterSwap, got.AfterPatch, got.InnerBefore, got.InnerAfter))
	})

	It("leaves an HTMX swap inside a data-gotth-preserve element alone", func() {
		var got struct {
			Swapped    bool   `json:"swapped"`
			AfterSwap  string `json:"afterSwap"`
			AfterPatch string `json:"afterPatch"`
			VaultMark  string `json:"vaultMark"`
			SlotMark   string `json:"slotMark"`
			Tick       string `json:"tick"`
		}
		c.evalJSON(`(async () => {
			window.__hx.mark("#vault", "vault-node");
			const swapped = await window.__hx.swap("#vault-slot");
			window.__hx.mark("#vault-slot", "vault-slot-node");
			const afterSwap = window.__hx.text("#vault-slot");
			const tick = await window.__hx.tick(2);
			return {
				swapped: swapped,
				afterSwap: afterSwap,
				afterPatch: window.__hx.text("#vault-slot"),
				vaultMark: window.__hx.markOf("#vault"),
				slotMark: window.__hx.markOf("#vault-slot"),
				tick: tick,
			};
		})()`, &got)

		Expect(got.Swapped).To(BeTrue())
		Expect(got.AfterSwap).To(Equal("swapped:vault"))
		Expect(got.Tick).To(Equal("tick 2"), "the region was not patched, so the preserve rule was not tested")

		Expect(got.AfterPatch).To(Equal("swapped:vault"),
			"a swap inside data-gotth-preserve was reverted by a live patch; that is the escape hatch "+
				"FR-27 exists to provide and FR-32 points HTMX applications at")
		Expect(got.VaultMark).To(Equal("vault-node"))
		Expect(got.SlotMark).To(Equal("vault-slot-node"))

		AddReportEntry("FR-32/FR-27 preserve hosts HTMX", fmt.Sprintf(
			"#vault-slot inside data-gotth-preserve: HTMX swap %q survived two live patches (%s)",
			got.AfterPatch, got.Tick))
	})
})

// ---------------------------------------------------------------------------
// The interop gap: HTMX behaviour after a morph
// ---------------------------------------------------------------------------

var _ = Describe("HTMX behaviour after gotth-live has morphed the node (FR-31, G8)", Ordered, ContinueOnFailure, Label("browser"), func() {
	var (
		c  *chrome
		ts *httptest.Server
	)

	BeforeAll(func() {
		browserOnly()
		ts = startHXApp()
		c = launchChrome()
		c.onNewDocument(hxHelpers)
	})

	BeforeEach(func() {
		browserOnly()
		c.navigate(ts.URL + "/")
		waitHXLive(c)
	})

	// The good case, and the one that makes FR-28's delegation argument pay
	// off for a THIRD-PARTY library as well as for gotth-live's own bindings:
	// HTMX keeps its per-node state on the node object, so a node that morph
	// preserves keeps working with no re-processing.
	It("keeps an hx-* control that existed at first paint working after a morph", func() {
		var got struct {
			Mark    string `json:"mark"`
			Swapped bool   `json:"swapped"`
			Out     string `json:"out"`
			Tick    string `json:"tick"`
		}
		c.evalJSON(`(async () => {
			window.__hx.mark("#survivor", "survivor-node");
			const tick = await window.__hx.tick(2);
			const swapped = await window.__hx.swap("#survivor");
			return {
				mark: window.__hx.markOf("#survivor"),
				swapped: swapped,
				out: window.__hx.text("#survivor-out"),
				tick: tick,
			};
		})()`, &got)

		Expect(got.Tick).To(Equal("tick 2"))
		Expect(got.Mark).To(Equal("survivor-node"),
			"the hx-* control was replaced by the morph, so this says nothing about HTMX surviving one")
		Expect(got.Swapped).To(BeTrue(),
			"HTMX stopped responding to a control that gotth-live morphed, with no re-processing step called")
		Expect(got.Out).To(Equal("swapped:survivor"))

		AddReportEntry("hx-* survives a morph", fmt.Sprintf(
			"#survivor was morphed through two patches with node identity intact, and its hx-get still "+
				"fired with no htmx.process() call: %q", got.Out))
	})

	// The gap. HTMX attaches its behaviour when it PROCESSES a node, and it
	// processes nodes at load and after its own swaps — not when some other
	// library inserts one. gotth-live's morph inserts nodes. So an hx-*
	// element the server starts rendering mid-session is inert until the
	// application calls htmx.process, and nothing in gotth-live's exported
	// documentation says so.
	//
	// Reported as D-16. This spec asserts the CURRENT behaviour on both sides
	// of the htmx.process call, so it is a lock on the state of the world and
	// will go red the day the gap is closed — which is the notification a
	// documentation-only note would not give.
	It("does not activate an hx-* control the morph inserted until htmx.process is called (D-16)", func() {
		var got struct {
			ExistedAtLoad bool   `json:"existedAtLoad"`
			Inserted      bool   `json:"inserted"`
			BeforeProcess string `json:"beforeProcess"`
			Requests      int    `json:"requests"`
			AfterProcess  string `json:"afterProcess"`
			Swapped       bool   `json:"swapped"`
		}
		c.evalJSON(`(async () => {
			const existedAtLoad = !!document.querySelector("#newcomer");
			await window.__hx.tick(1);
			const inserted = !!document.querySelector("#newcomer");
			if (!inserted) return {existedAtLoad: existedAtLoad, inserted: false};

			// Click it exactly as a user would, and give HTMX four seconds.
			window.__hx.requests = [];
			document.querySelector("#newcomer").click();
			await new Promise(r => setTimeout(r, 2000));
			const beforeProcess = window.__hx.text("#newcomer-out");
			const requests = window.__hx.requests.length;

			// Now the documented remedy, applied to the live region.
			window.htmx.process(document.querySelector('[data-gotth-region="hx.live"]'));
			const swapped = await window.__hx.swap("#newcomer");
			return {
				existedAtLoad: existedAtLoad,
				inserted: true,
				beforeProcess: beforeProcess,
				requests: requests,
				afterProcess: window.__hx.text("#newcomer-out"),
				swapped: swapped,
			};
		})()`, &got)

		Expect(got.ExistedAtLoad).To(BeFalse(),
			"#newcomer was in the first paint, so HTMX processed it at load and this proves nothing")
		Expect(got.Inserted).To(BeTrue(), "the morph never inserted #newcomer")

		Expect(got.BeforeProcess).To(Equal("-"),
			"an hx-* control inserted by a morph fired on its first click without htmx.process — "+
				"good news, and this spec is stale: D-16 is closed and the assertion should be inverted")
		Expect(got.Requests).To(Equal(0))

		Expect(got.Swapped).To(BeTrue())
		Expect(got.AfterProcess).To(Equal("swapped:newcomer"),
			"htmx.process on the live region did not activate the inserted control either, "+
				"which would make D-16 worse than a documentation gap")

		AddReportEntry("D-16 — hx-* inserted by a morph needs htmx.process", fmt.Sprintf(
			"#newcomer was inserted by the patch at tick 1.\n"+
				"first click, no htmx.process: %d HTMX requests, target still %q\n"+
				"after htmx.process(live region): swap succeeded, target %q\n"+
				"htmx %s, browser %s",
			got.Requests, got.BeforeProcess, got.AfterProcess, htmxVersion, c.version))
	})
})
