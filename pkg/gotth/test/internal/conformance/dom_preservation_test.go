package conformance_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/live"
)

// ---------------------------------------------------------------------------
// FR-25, FR-26, FR-27, FR-28 — the DOM state a morph must preserve, asserted
// in a real browser.
//
// Run:
//
//	docker run --rm -v "$PWD:/repo" -w /repo/gotth-live \
//	    dis-gotth-live-bench:latest \
//	    bash -c 'go test ./test/... -count=1 -args -ginkgo.label-filter=browser -ginkgo.v'
//
// The library image has no browser and these specs skip there with the reason
// printed, exactly as the checkpoint-1 browser specs do. They carry Label
// "browser" and NOT "e2e": unlike browser_test.go they compile no second
// module, so GOTTHLIVE_E2E is not required and the specs are cheap enough to
// run on every browser invocation.
//
// # Why this application and not examples/counter
//
// The counter has one number and four buttons. FR-25 names nine distinct
// pieces of state — focus, caret, element scroll, document scroll,
// uncontrolled input values, checkbox/radio, <select>, <details>, media
// position and running transitions — and none of them exists in the counter's
// markup. The application below is built to hold all of them at once, so that
// a single patch is required to preserve every one of them simultaneously,
// which is the situation the requirement is actually about.
//
// # The mechanism every case rests on, and how it is checked
//
// Morph preserves state by never replacing the node that holds it. That is not
// observable from markup: a replaced node and a morphed node render identical
// HTML. So every spec here tags the live node from the PAGE with an expando
// the server never sends (window-side, `__qaMark`), and asserts the tag is
// still there afterwards. A runtime that replaced the node loses the tag even
// though the DOM looks right — which is precisely the failure these specs are
// paid to catch, and is what the mutation evidence in
// docs/qa/checkpoint-2-browser.md §3 exercises.
//
// One spec uses that same instrument INVERTED, and it is the only one that
// does: the D-21 spec asserts the tags are GONE, because the runtime's
// save()/restore() pair only matters when the node holding the state was
// replaced. The tag has to be re-queried from the live document either way — a
// detached node keeps its expando, its scrollTop and its selection, so reading
// any of them off a captured reference reports success for a node the patch
// threw away. That is the vacuity the first draft of the FR-27 scroll spec
// had, and it is why every read below goes back through querySelector.
// ---------------------------------------------------------------------------

// liveMount is where every application in this file mounts its live handler.
// It is deliberately not "/" so that live.Script's mount-path handling is on
// the path the browser actually takes.
const liveMount = "/live"

// raw renders a pre-built HTML string as a templ component.
//
// The fragments here are assembled with a string builder rather than written
// as templ components because the suite must be able to vary the markup per
// case (a pip inserted before the focused input, a control that appears only
// after the second patch) and because a .templ file in test/ would need
// generated code checked in beside it, which gen.sh --check would then police.
func raw(s string) templ.Component {
	return templ.ComponentFunc(func(_ context.Context, w io.Writer) error {
		_, err := io.WriteString(w, s)
		return err
	})
}

// serveLive mounts one live application plus its page routes on a real HTTP
// server, with a real Origin allowlist derived from the listener.
//
// The listener is created before the application on purpose: Config.Origins is
// deny-by-default with no reflection of the request's own Origin, so the
// allowlist has to name the address the browser will send, and that address is
// only knowable once the socket is bound. httptest.NewUnstartedServer binds at
// construction, which is what makes the ordering possible without weakening
// the check to live.AnyOrigin.
func serveLive[S any](cfg live.Config[S, qaUser], routes map[string]http.HandlerFunc) *httptest.Server {
	GinkgoHelper()

	ts := httptest.NewUnstartedServer(nil)
	cfg.Origins = []string{"http://" + ts.Listener.Addr().String()}

	app, err := live.New(cfg)
	Expect(err).NotTo(HaveOccurred())

	mux := http.NewServeMux()
	// Both patterns, for the reason examples/counter states: MountPath is the
	// WebSocket endpoint and MountPath+"/" is where the runtime is served
	// from, and the handler tells them apart by path suffix.
	mux.Handle(liveMount, app.Handler())
	mux.Handle(liveMount+"/", app.Handler())
	for pattern, handler := range routes {
		mux.HandleFunc(pattern, handler)
	}

	ts.Config.Handler = mux
	ts.Start()
	DeferCleanup(func() {
		ts.Close()
		_ = app.Close(context.Background())
	})
	return ts
}

// scriptTag renders live.Script for liveMount.
//
// The real helper is used rather than a hand-written <script> tag so that the
// page the browser loads is the page a consumer following the documentation
// would get, including the data-gotth-url attribute the runtime reads to find
// its own socket.
func scriptTag() string {
	GinkgoHelper()
	var b strings.Builder
	Expect(live.Script(liveMount).Render(context.Background(), &b)).To(Succeed())
	return b.String()
}

// html renders a page body into a complete document.
func htmlDoc(title, head, body string) string {
	return `<!DOCTYPE html><html lang="en"><head><meta charset="utf-8">` +
		`<title>` + title + `</title>` + head + `</head><body>` + body + `</body></html>`
}

// ---------------------------------------------------------------------------
// The application
// ---------------------------------------------------------------------------

const (
	fragPanel = "qa.panel"
	eventTick = "qa.tick"
	eventAlt  = "qa.alt"
)

// domState is the whole server-owned state. Two fields, both rendered, so that
// every patch is caused by something a spec did.
type domState struct {
	Tick int
	Note string
}

// panelHTML is the fragment, and it is the fixture.
//
// Everything FR-25 names is in here at once, and the markup AROUND each of
// them changes on every tick — the heading line, a growing run of pips
// immediately before the focused input, a data attribute on the focused input
// itself, and the <details> body text. A morph that only rewrote the parts
// that changed would still have to walk past every stateful node to do it,
// which is the traversal the requirement is about.
func panelHTML(s domState) string {
	note := s.Note
	if note == "" {
		note = "-"
	}
	tick := strconv.Itoa(s.Tick)

	var b strings.Builder
	b.WriteString(`<section data-gotth-region="` + fragPanel + `">`)
	b.WriteString(`<p id="tickline">tick ` + tick + `</p>`)
	b.WriteString(`<p id="noteline">note ` + note + `</p>`)

	// One more pip per tick, inserted immediately before the focused input, so
	// the morph performs a real structural insertion at the cursor position
	// next to the node whose state must survive.
	for i := 0; i < s.Tick; i++ {
		b.WriteString(`<span class="pip">.</span>`)
	}

	// Uncontrolled: no value attribute, so FR-25's "uncontrolled input values"
	// says what the user typed is the user's. data-qa-tick changes every tick,
	// so syncAttrs really does call setAttribute on the focused element.
	b.WriteString(`<input id="draft" name="draft" type="text" data-qa-tick="` + tick + `">`)

	// Controlled: the server renders a value attribute, so the server is
	// authoritative and an uncommitted edit is replaced. This is the other
	// half of the same rule and it is asserted, not assumed.
	b.WriteString(`<input id="controlled" name="controlled" type="text" value="server-` + tick + `">`)

	// A textarea's declared value is its text content. Rendered empty, so it
	// is uncontrolled.
	b.WriteString(`<textarea id="freeform" name="freeform"></textarea>`)

	b.WriteString(`<input id="box" type="checkbox">`)
	b.WriteString(`<input id="radio-a" type="radio" name="pick" value="a">`)
	b.WriteString(`<input id="radio-b" type="radio" name="pick" value="b">`)
	b.WriteString(`<select id="sel"><option value="a">a</option><option value="b">b</option><option value="c">c</option></select>`)
	// Server-silent: the server never renders open= for this one, at any tick,
	// so its open state can only ever be the user's. This is FR-25's
	// "<details> open state" clause and QA-1's D-15.
	b.WriteString(`<details id="det"><summary>more</summary><p>body ` + tick + `</p></details>`)

	// Server-declared, and the other arm of the same rule. The server renders
	// open= at tick 1 and at no other tick, so a spec can watch it declare the
	// state and then withdraw the declaration. Without this arm, "preserve the
	// user's disclosure" would be satisfied by a runtime that never wrote
	// <details> open at all, and a server would have no way to close one.
	declared := ""
	if s.Tick == 1 {
		declared = ` open`
	}
	b.WriteString(`<details id="serverdet"` + declared + `><summary>more</summary><p>decl ` + tick + `</p></details>`)

	b.WriteString(`<div id="scroller">`)
	for i := 0; i < 60; i++ {
		b.WriteString(`<p class="row">row ` + strconv.Itoa(i) + `</p>`)
	}
	b.WriteString(`</div>`)

	// The replace-and-restore fixture (D-21), and the only place in this file
	// where morph is asked to REPLACE rather than reconcile.
	//
	// The wrapper keeps its id and changes its TAG on every tick. An id match
	// with a changed tag is the one route into morphNode's replaceWith path —
	// without an id, match() refuses a different tag and the new node is
	// inserted beside the old one instead — so this is what a server does when
	// it renders <div id="card"> for one state and <article id="card"> for
	// another. The identified input and scroll container inside it come back as
	// fresh server markup with the same ids and none of the state, which is the
	// one situation the runtime's save()/restore() pair exists for.
	//
	// It sits in the MIDDLE of the fragment rather than at the end,
	// deliberately: the cursor in morphChildren has to survive the replacement
	// for anything after it to be matched at all.
	wrap := "div"
	if s.Tick%2 == 1 {
		wrap = "section"
	}
	b.WriteString(`<` + wrap + ` id="wrap">`)
	// Controlled, so the replacement carries text for a caret to be restored
	// into. A ten-character value that never changes keeps the caret offsets
	// meaningful across the replacement.
	b.WriteString(`<input id="draft2" name="draft2" type="text" value="abcdefghij">`)
	b.WriteString(`<div id="scroller2">`)
	for i := 0; i < 60; i++ {
		b.WriteString(`<p class="row">row ` + strconv.Itoa(i) + `</p>`)
	}
	b.WriteString(`</div>`)
	b.WriteString(`</` + wrap + `>`)

	// The transition target. The class arrives on the first tick and never
	// changes again, so the transition it starts is still running when the
	// second tick morphs the region around it.
	if s.Tick > 0 {
		b.WriteString(`<div id="fader" class="on"></div>`)
	} else {
		b.WriteString(`<div id="fader"></div>`)
	}

	b.WriteString(`<audio id="clip" src="/silence.wav" preload="auto"></audio>`)

	// FR-27: never morphed, never removed, subtree included.
	b.WriteString(`<div id="vault" data-gotth-preserve><b>server ` + tick + `</b></div>`)

	b.WriteString(`<button id="tick" type="button" data-gotth-on="click:` + eventTick + `">tick</button>`)
	// FR-28: a bound control the morph INSERTS. It has never been seen by any
	// binding pass at page load, and it must work on its first click.
	if s.Tick >= 2 {
		b.WriteString(`<button id="alt" type="button" data-gotth-on="click:` + eventAlt + `">alt</button>`)
	}
	b.WriteString(`</section>`)
	return b.String()
}

const domCSS = `
#scroller, #scroller2 { height: 60px; overflow-y: scroll; border: 1px solid #000; }
#fader { width: 40px; height: 40px; background: #000; opacity: 1; transition: opacity 8s linear; }
#fader.on { opacity: 0; }
.spacer { height: 40px; }
`

func domPage(s domState) string {
	return htmlDoc("QA DOM preservation",
		`<style>`+domCSS+`</style>`+scriptTag(),
		// A tall run of spacers before the panel, so the DOCUMENT has somewhere
		// to scroll to. FR-25 names document scroll separately from element
		// scroll and the two are preserved by different means: the element by
		// the runtime's save/restore, the document by nothing at all — it
		// survives only because morph does not replace the nodes above it.
		`<div id="tall">`+strings.Repeat(`<p class="spacer">spacer</p>`, 120)+`</div>`+
			panelHTML(s))
}

func domConfig() live.Config[domState, qaUser] {
	return live.Config[domState, qaUser]{
		Init: func(ctx context.Context, session live.Session[qaUser]) (domState, []live.Effect[qaUser], error) {
			return domState{}, nil, nil
		},
		Reduce: func(s domState, ev live.Event) (domState, []live.Effect[qaUser]) {
			switch ev.Name {
			case eventTick:
				s.Tick++
			case eventAlt:
				s.Note = "alt"
			}
			return s, nil
		},
		Fragments: []live.Fragment[domState]{{
			ID:     fragPanel,
			Render: func(s domState) templ.Component { return raw(panelHTML(s)) },
			Dirty:  func(prev, next domState) bool { return prev != next },
		}},
		Events:       []string{eventTick, eventAlt},
		Authenticate: func(request *http.Request) (qaUser, error) { return qaUser("qa"), nil },
		Authorize:    live.AllowAll[qaUser],
		CSRF:         live.NoCSRFCheck,
	}
}

// silenceWAV is two seconds of 8-bit 8 kHz PCM silence.
//
// It is served as a file rather than inlined as a data: URI so that the src
// attribute is short and identical in every render — a data: URI would put
// twenty kilobytes into every patch frame and make the media case a size test
// by accident.
func silenceWAV(seconds int) []byte {
	const rate = 8000
	n := rate * seconds

	var b bytes.Buffer
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(36+n))
	b.WriteString("WAVEfmt ")
	_ = binary.Write(&b, binary.LittleEndian, uint32(16)) // PCM fmt chunk
	_ = binary.Write(&b, binary.LittleEndian, uint16(1))  // format: PCM
	_ = binary.Write(&b, binary.LittleEndian, uint16(1))  // channels
	_ = binary.Write(&b, binary.LittleEndian, uint32(rate))
	_ = binary.Write(&b, binary.LittleEndian, uint32(rate)) // byte rate
	_ = binary.Write(&b, binary.LittleEndian, uint16(1))    // block align
	_ = binary.Write(&b, binary.LittleEndian, uint16(8))    // bits per sample
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, uint32(n))
	b.Write(bytes.Repeat([]byte{128}, n)) // 8-bit PCM silence is 0x80
	return b.Bytes()
}

// tickEffect is a per-session ticker: a patch the browser did not ask for.
//
// FR-26's first clause can only be checked with one. The runtime refuses to
// raise an event while a composition is active — that is FR-26's second clause
// — so a click-driven patch cannot possibly arrive mid-composition, and a spec
// built on one would time out rather than assert anything. A server-initiated
// patch is the only way to get a morph to land on a composing input, which is
// also the situation the requirement is describing: somebody else's change,
// arriving while you are half-way through typing a word.
func tickEffect(every time.Duration) live.Effect[qaUser] {
	return live.Effect[qaUser]{
		Source: "qa.ticker",
		Run: func(ctx context.Context, session live.Session[qaUser], emit live.Emitter) error {
			ticker := time.NewTicker(every)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					// The library owns this goroutine and waits for it at
					// shutdown, so returning promptly on cancellation is the
					// contract, not politeness.
					return ctx.Err()
				case <-ticker.C:
					if err := emit(live.Event{Name: eventTick}); err != nil {
						// A refused emit means the session is saturated or
						// closing. Neither is this effect's failure and
						// neither is worth an Error frame in a preservation
						// spec.
						return nil
					}
				}
			}
		},
	}
}

// domTickingConfig is domConfig plus that ticker.
func domTickingConfig(every time.Duration) live.Config[domState, qaUser] {
	cfg := domConfig()
	cfg.Init = func(ctx context.Context, session live.Session[qaUser]) (domState, []live.Effect[qaUser], error) {
		return domState{}, []live.Effect[qaUser]{tickEffect(every)}, nil
	}
	return cfg
}

func startDOMApp() *httptest.Server {
	GinkgoHelper()
	return startDOMAppWith(domConfig())
}

// startDOMTickingApp serves the same page against a session that patches
// itself on a timer.
func startDOMTickingApp(every time.Duration) *httptest.Server {
	GinkgoHelper()
	return startDOMAppWith(domTickingConfig(every))
}

func startDOMAppWith(cfg live.Config[domState, qaUser]) *httptest.Server {
	GinkgoHelper()

	wav := silenceWAV(2)
	return serveLive(cfg, map[string]http.HandlerFunc{
		"/silence.wav": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "audio/wav")
			w.Header().Set("Accept-Ranges", "bytes")
			http.ServeContent(w, r, "silence.wav", time.Time{}, bytes.NewReader(wav))
		},
		"/": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, domPage(domState{}))
		},
	})
}

// ---------------------------------------------------------------------------
// The page-side helpers
// ---------------------------------------------------------------------------

// qaHelpers is installed before any page script, on every document, so that a
// spec's evaluated expression is one call rather than an inline event loop.
//
// It is page JavaScript and not library JavaScript: it is injected by the
// debugger and never served, so nothing here is part of what a consumer ships.
const qaHelpers = `
window.__qa = {
  // mark tags a live node with an expando the server cannot send. If a patch
  // REPLACES the node rather than morphing it, the tag is gone — which is the
  // only way to tell morph from replace when both render the same HTML.
  mark(sel, value) {
    const el = document.querySelector(sel);
    if (!el) throw new Error("no element for " + sel);
    el.__qaMark = value || "marked";
    return true;
  },
  markOf(sel) {
    const el = document.querySelector(sel);
    return el && el.__qaMark ? el.__qaMark : "";
  },
  // tick clicks the bound button and resolves once the region has actually
  // been patched, which is the only honest signal that a morph has happened.
  async tick(times) {
    const line = () => document.querySelector("#tickline").textContent.trim();
    for (let i = 0; i < (times || 1); i++) {
      const before = line();
      document.querySelector("#tick").click();
      const deadline = performance.now() + 15000;
      for (;;) {
        if (line() !== before) break;
        if (performance.now() > deadline) {
          throw new Error("no patch arrived: #tickline is still " + JSON.stringify(before));
        }
        await new Promise(r => setTimeout(r, 15));
      }
      // Pacing: the library's default inbound budget is 50 events/s.
      await new Promise(r => setTimeout(r, 40));
    }
    return line();
  },
  // waitPatch waits for a patch NOBODY on this page asked for. It is what the
  // IME specs use, because the runtime deliberately raises no event while a
  // composition is active (FR-26) — so a click-driven tick cannot be the thing
  // that delivers the patch a composition has to survive.
  async waitPatch(ms) {
    const line = () => document.querySelector("#tickline").textContent.trim();
    const before = line();
    const ok = await this.waitFor(() => line() !== before, ms || 15000);
    return {ok: ok, before: before, after: line()};
  },
  // waitFor polls an expression until it is true, so a spec never sleeps a
  // fixed amount and calls it synchronisation.
  async waitFor(fn, ms) {
    const deadline = performance.now() + (ms || 10000);
    for (;;) {
      if (fn()) return true;
      if (performance.now() > deadline) return false;
      await new Promise(r => setTimeout(r, 15));
    }
  },
};
`

// waitLivePanel blocks until the runtime has connected and driven the DOM.
//
// Both halves are asserted. data-gotth-status="live" says the snapshot was
// applied; a completed tick says the event path works end to end. Asserting
// only the first would let a spec run against a page that can never be
// patched, which would make every preservation assertion below vacuous in the
// most embarrassing possible way — nothing to preserve state ACROSS.
func waitLivePanel(c *chrome) {
	GinkgoHelper()

	Eventually(func() string {
		return c.evalString(`document.documentElement.getAttribute("data-gotth-status") || ""`)
	}, 30*time.Second, 100*time.Millisecond).Should(Equal("live"),
		"the client runtime never reported a live connection")

	Eventually(func() bool {
		return c.evalBool(`!!document.querySelector("#tickline")`)
	}, 30*time.Second, 100*time.Millisecond).Should(BeTrue())
}

// ---------------------------------------------------------------------------
// The attribute vocabulary, checked without a browser
// ---------------------------------------------------------------------------

// The fixtures above write data-gotth-* attributes as literals, because they
// are assembled as strings rather than as templ components. That is a second
// spelling of a contract the library already owns, and a second spelling can
// drift. This spec is the join: if live.Region, live.On or live.Preserve ever
// rendered a different attribute name, every browser spec in this file would
// silently stop exercising the runtime and would still pass, because the
// runtime ignores attributes it does not recognise.
//
// It needs no browser, so it also gives the library image something to run.
var _ = Describe("The fixtures in this suite use the library's own attribute vocabulary", func() {
	It("spells data-gotth-region, data-gotth-on and data-gotth-preserve as live.Region, live.On and live.Preserve do", func() {
		region := live.Region(fragPanel)
		Expect(region).To(HaveKeyWithValue("data-gotth-region", fragPanel))
		Expect(panelHTML(domState{})).To(ContainSubstring(`data-gotth-region="` + fragPanel + `"`))

		on := live.On("click", eventTick)
		Expect(on).To(HaveKeyWithValue("data-gotth-on", "click:"+eventTick))
		Expect(panelHTML(domState{})).To(ContainSubstring(`data-gotth-on="click:` + eventTick + `"`))

		preserve := live.Preserve()
		Expect(preserve).To(HaveKey("data-gotth-preserve"))
		Expect(panelHTML(domState{})).To(ContainSubstring(`data-gotth-preserve`))
	})

	It("renders the controls FR-28 needs only once the morph has inserted them", func() {
		Expect(panelHTML(domState{Tick: 0})).NotTo(ContainSubstring(`id="alt"`),
			"the FR-28 control must NOT be in the first paint, or the spec proves nothing about insertion")
		Expect(panelHTML(domState{Tick: 2})).To(ContainSubstring(`id="alt"`))
	})
})

// ---------------------------------------------------------------------------
// FR-25 — DOM state preservation contract
// ---------------------------------------------------------------------------

var _ = Describe("DOM state a morph must preserve (FR-25)", Ordered, ContinueOnFailure, Label("browser"), func() {
	var (
		c  *chrome
		ts *httptest.Server
	)

	BeforeAll(func() {
		// browserOnly is called before anything is started, so the library
		// image reports the reason and allocates nothing.
		browserOnly()
		ts = startDOMApp()
		c = launchChrome()
		c.onNewDocument(qaHelpers)
	})

	BeforeEach(func() {
		browserOnly()
		c.navigate(ts.URL + "/")
		waitLivePanel(c)
	})

	It("keeps focus, the caret and the uncommitted value of an uncontrolled input", func() {
		// The focused element's own attributes change on every tick
		// (data-qa-tick), and a pip is inserted immediately before it, so the
		// morph both rewrites the neighbourhood and touches the node itself.
		c.evalJSON(`(() => {
			window.__qa.mark("#draft", "draft-node");
			const d = document.querySelector("#draft");
			d.focus();
			d.value = "half typed";
			d.setSelectionRange(4, 8);
			return null;
		})()`, nil)

		Expect(c.evalString(`document.activeElement.id`)).To(Equal("draft"))

		var got struct {
			Active string `json:"active"`
			Mark   string `json:"mark"`
			Value  string `json:"value"`
			Start  int    `json:"start"`
			End    int    `json:"end"`
			Tick   string `json:"tick"`
			Pips   int    `json:"pips"`
		}
		c.evalJSON(`(async () => {
			await window.__qa.tick(2);
			const d = document.querySelector("#draft");
			return {
				active: document.activeElement ? document.activeElement.id : "",
				mark: d.__qaMark || "",
				value: d.value,
				start: d.selectionStart,
				end: d.selectionEnd,
				tick: d.getAttribute("data-qa-tick"),
				pips: document.querySelectorAll(".pip").length,
			};
		})()`, &got)

		Expect(got.Pips).To(Equal(2),
			"the morph did not insert the pips, so it never rewrote the region around the focused input")
		Expect(got.Tick).To(Equal("2"),
			"the focused input's own attributes were not synced, so this patch did not touch it")

		Expect(got.Mark).To(Equal("draft-node"),
			"the focused input was REPLACED, not morphed (FR-24): the page-set expando is gone, "+
				"so nothing below this line could have been preserved by the morph")
		Expect(got.Active).To(Equal("draft"), "focus did not survive the patch (FR-25)")
		Expect(got.Value).To(Equal("half typed"),
			"an uncontrolled input's uncommitted value was overwritten (FR-25)")
		Expect(got.Start).To(Equal(4), "the caret start moved (FR-25)")
		Expect(got.End).To(Equal(8), "the selection end moved (FR-25)")

		AddReportEntry("FR-25 focus, caret, uncontrolled value", fmt.Sprintf(
			"browser %s\nfocus %q  caret [%d,%d)  value %q  node identity preserved: yes\n"+
				"pips inserted around it: %d  attribute resync on the focused node: data-qa-tick=%s",
			c.version, got.Active, got.Start, got.End, got.Value, got.Pips, got.Tick))
	})

	It("replaces an uncommitted value where the server rendered one, and only there", func() {
		// The other half of the same rule, and the half a suite is tempted to
		// leave out. FR-25 preserves UNCONTROLLED values; an input the server
		// renders a value attribute for is controlled, and a server that
		// cannot overwrite it cannot implement a form reset.
		c.evalJSON(`(() => {
			window.__qa.mark("#controlled", "controlled-node");
			const el = document.querySelector("#controlled");
			el.value = "user edit not yet sent";
			return null;
		})()`, nil)

		var got struct {
			Mark       string `json:"mark"`
			Controlled string `json:"controlled"`
			Attr       string `json:"attr"`
			Draft      string `json:"draft"`
			Freeform   string `json:"freeform"`
		}
		c.evalJSON(`(async () => {
			document.querySelector("#draft").value = "still mine";
			document.querySelector("#freeform").value = "also still mine";
			await window.__qa.tick(1);
			const el = document.querySelector("#controlled");
			return {
				mark: el.__qaMark || "",
				controlled: el.value,
				attr: el.getAttribute("value"),
				draft: document.querySelector("#draft").value,
				freeform: document.querySelector("#freeform").value,
			};
		})()`, &got)

		Expect(got.Mark).To(Equal("controlled-node"),
			"the controlled input was replaced rather than morphed, so this asserts nothing about the rule")
		Expect(got.Attr).To(Equal("server-1"))
		Expect(got.Controlled).To(Equal("server-1"),
			"the server rendered value=%q and the uncommitted edit was NOT replaced: "+
				"a server that renders a value attribute is authoritative over that field", got.Attr)

		// The same patch, at the same instant, left the uncontrolled ones
		// alone. One patch, two opposite outcomes, which is what makes this a
		// rule rather than a coincidence.
		Expect(got.Draft).To(Equal("still mine"),
			"the same patch that replaced the controlled value also clobbered an uncontrolled one")
		Expect(got.Freeform).To(Equal("also still mine"),
			"a textarea the server rendered empty is uncontrolled and must keep the user's draft")

		AddReportEntry("FR-25 controlled vs uncontrolled", fmt.Sprintf(
			"one patch:\n  #controlled (value= rendered)   %q -> %q   REPLACED, correct\n"+
				"  #draft      (no value attr)      %q            kept, correct\n"+
				"  #freeform   (empty textarea)     %q            kept, correct",
			"user edit not yet sent", got.Controlled, got.Draft, got.Freeform))
	})

	It("keeps element scroll offset and document scroll offset", func() {
		var got struct {
			Mark        string  `json:"mark"`
			ScrollTop   float64 `json:"scrollTop"`
			DocScroll   float64 `json:"docScroll"`
			WantScroll  float64 `json:"wantScroll"`
			WantDocking float64 `json:"wantDoc"`
		}
		c.evalJSON(`(async () => {
			window.__qa.mark("#scroller", "scroller-node");
			const sc = document.querySelector("#scroller");
			sc.scrollTop = 240;
			window.scrollTo(0, 400);
			const wantScroll = sc.scrollTop, wantDoc = window.scrollY;
			await window.__qa.tick(2);
			// Re-queried, deliberately. A detached node keeps its expando, so
			// reading the mark off the captured reference would report
			// "preserved" for a node the patch had thrown away — which is the
			// exact vacuity this whole mechanism exists to avoid, and the first
			// draft of this spec had it.
			return {
				mark: window.__qa.markOf("#scroller"),
				scrollTop: document.querySelector("#scroller").scrollTop,
				docScroll: window.scrollY,
				wantScroll: wantScroll,
				wantDoc: wantDoc,
			};
		})()`, &got)

		Expect(got.WantScroll).To(BeNumerically(">", 0),
			"the container did not scroll at all, so this spec would pass on a runtime that lost scroll")
		Expect(got.WantDocking).To(BeNumerically(">", 0),
			"the document did not scroll at all, so the document arm is vacuous")

		Expect(got.Mark).To(Equal("scroller-node"),
			"the scroll container was replaced rather than morphed. Note that the OFFSET below can "+
				"survive a replace — save/restore in the runtime restores scrollTop for identified "+
				"elements — so node identity is the assertion that distinguishes the two")
		Expect(got.ScrollTop).To(Equal(got.WantScroll),
			"element scroll offset moved across the patch (FR-25)")
		Expect(got.DocScroll).To(Equal(got.WantDocking),
			"document scroll offset moved across the patch (FR-25)")

		AddReportEntry("FR-25 scroll", fmt.Sprintf(
			"element #scroller scrollTop %.0f -> %.0f\ndocument scrollY %.0f -> %.0f",
			got.WantScroll, got.ScrollTop, got.WantDocking, got.DocScroll))
	})

	It("keeps checkbox, radio and <select> state the server did not declare", func() {
		var got struct {
			BoxMark  string `json:"boxMark"`
			SelMark  string `json:"selMark"`
			Box      bool   `json:"box"`
			RadioB   bool   `json:"radioB"`
			Selected string `json:"selected"`
			SelIndex int    `json:"selIndex"`
		}
		c.evalJSON(`(async () => {
			window.__qa.mark("#box", "box-node");
			window.__qa.mark("#sel", "sel-node");
			document.querySelector("#box").checked = true;
			document.querySelector("#radio-b").checked = true;
			document.querySelector("#sel").value = "c";
			await window.__qa.tick(2);
			const sel = document.querySelector("#sel");
			return {
				boxMark: document.querySelector("#box").__qaMark || "",
				selMark: sel.__qaMark || "",
				box: document.querySelector("#box").checked,
				radioB: document.querySelector("#radio-b").checked,
				selected: sel.value,
				selIndex: sel.selectedIndex,
			};
		})()`, &got)

		Expect(got.BoxMark).To(Equal("box-node"))
		Expect(got.SelMark).To(Equal("sel-node"))
		Expect(got.Box).To(BeTrue(),
			"the user's tick was cleared by a patch that never mentioned the checkbox (FR-25)")
		Expect(got.RadioB).To(BeTrue(),
			"the user's radio choice was cleared by a patch that never mentioned it (FR-25)")
		Expect(got.Selected).To(Equal("c"),
			"the <select> selection was reset by a patch that never mentioned it (FR-25)")
		Expect(got.SelIndex).To(Equal(2))

		AddReportEntry("FR-25 checkbox, radio, select", fmt.Sprintf(
			"checkbox %v, radio-b %v, select %q (index %d) — all set by the user, none declared by the server",
			got.Box, got.RadioB, got.Selected, got.SelIndex))
	})

	It("keeps media playback position", func() {
		var got struct {
			Mark      string  `json:"mark"`
			Ready     int     `json:"ready"`
			Before    float64 `json:"before"`
			After     float64 `json:"after"`
			Duration  float64 `json:"duration"`
			NetworkOK bool    `json:"networkOk"`
		}
		c.evalJSON(`(async () => {
			const clip = document.querySelector("#clip");
			window.__qa.mark("#clip", "clip-node");
			// Wait for enough of the media to exist for a seek to mean
			// anything. HAVE_METADATA is 1.
			const loaded = await window.__qa.waitFor(() => clip.readyState >= 1, 20000);
			if (!loaded) {
				return {mark: "", ready: clip.readyState, before: -1, after: -1,
				        duration: clip.duration, networkOk: false};
			}
			clip.currentTime = 1.25;
			await new Promise(r => {
				if (Math.abs(clip.currentTime - 1.25) < 0.001) return r();
				clip.addEventListener("seeked", r, {once: true});
				setTimeout(r, 5000);
			});
			const before = clip.currentTime;
			await window.__qa.tick(2);
			const live = document.querySelector("#clip");
			return {
				mark: live.__qaMark || "",
				ready: live.readyState,
				before: before,
				after: live.currentTime,
				duration: live.duration,
				networkOk: true,
			};
		})()`, &got)

		Expect(got.NetworkOK).To(BeTrue(),
			"the media element never reached HAVE_METADATA (readyState %d), so the media arm of FR-25 "+
				"could not be measured in this browser — report it as unmeasured rather than green", got.Ready)
		Expect(got.Before).To(BeNumerically("~", 1.25, 0.05),
			"the seek did not take, so there is no playback position to preserve")

		Expect(got.Mark).To(Equal("clip-node"),
			"the media element was replaced rather than morphed, which loses playback position by construction")
		Expect(got.After).To(BeNumerically("~", got.Before, 0.05),
			"media playback position moved across the patch: %.3f -> %.3f (FR-25)", got.Before, got.After)

		AddReportEntry("FR-25 media playback position", fmt.Sprintf(
			"element <audio id=clip src=/silence.wav>, duration %.2fs, readyState %d\n"+
				"currentTime %.3f -> %.3f across two patches", got.Duration, got.Ready, got.Before, got.After))
	})

	It("does not restart an in-flight CSS transition", func() {
		var got struct {
			Started     bool    `json:"started"`
			Same        bool    `json:"same"`
			Count       int     `json:"count"`
			Before      float64 `json:"before"`
			After       float64 `json:"after"`
			PlayState   string  `json:"playState"`
			MarkSurvive string  `json:"mark"`
		}
		c.evalJSON(`(async () => {
			// Tick once: the server adds class="on", which is what starts the
			// eight-second opacity transition. Then mark the node and the
			// Animation object, and tick again while it is still running.
			await window.__qa.tick(1);
			const fader = document.querySelector("#fader");
			window.__qa.mark("#fader", "fader-node");
			const running = await window.__qa.waitFor(() => fader.getAnimations().length > 0, 5000);
			if (!running) return {started: false};
			const anim = fader.getAnimations()[0];
			window.__qaAnim = anim;
			const before = anim.currentTime;
			await window.__qa.tick(1);
			const live = document.querySelector("#fader");
			const now = live.getAnimations();
			return {
				started: true,
				same: now.length === 1 && now[0] === window.__qaAnim,
				count: now.length,
				before: before,
				after: now.length ? now[0].currentTime : -1,
				playState: now.length ? now[0].playState : "none",
				mark: live.__qaMark || "",
			};
		})()`, &got)

		Expect(got.Started).To(BeTrue(),
			"no transition ever started, so this spec cannot say anything about preserving one")
		Expect(got.MarkSurvive).To(Equal("fader-node"),
			"the transitioning element was replaced rather than morphed")
		Expect(got.Count).To(Equal(1),
			"after the patch the element has %d animations, not 1: the transition was destroyed or restarted", got.Count)
		Expect(got.Same).To(BeTrue(),
			"the Animation object is a different one after the patch, so the transition restarted rather than continued (FR-25)")
		Expect(got.PlayState).To(Equal("running"))
		Expect(got.After).To(BeNumerically(">", got.Before),
			"the transition's clock went backwards or stood still across the patch: %.1fms -> %.1fms",
			got.Before, got.After)

		AddReportEntry("FR-25 in-flight CSS transition", fmt.Sprintf(
			"one CSSTransition, same Animation object before and after the patch, playState %q\n"+
				"currentTime %.1f ms -> %.1f ms", got.PlayState, got.Before, got.After))
	})

	// FR-25 names "<details> open state" with no qualifier. In a browser the
	// open IDL attribute REFLECTS the open content attribute — opening a
	// disclosure writes open="" into the DOM — and the rule morph applies to
	// checked, selected and value reads the presence of an attribute as "the
	// server is controlling this". The two met badly: the user's own open
	// state looked to morph exactly like a server that had changed its mind,
	// and the next unrelated patch closed it, twice over.
	//
	// That was D-15, held here as a PIt while it was open and un-pended when
	// it was fixed. The fix keeps the server's word about a reflected
	// attribute outside the DOM, where the user cannot write it; the rule is
	// written out above `declared` in client/runtime.js. This spec is the one
	// that named the defect, so it is the one that has to prove it closed.
	It("keeps a <details> the user opened and the server never mentioned (FR-25, D-15)", func() {
		var got struct {
			Mark string `json:"mark"`
			Open bool   `json:"open"`
			Attr bool   `json:"attr"`
			Tick string `json:"tick"`
		}
		c.evalJSON(`(async () => {
			window.__qa.mark("#det", "det-node");
			const det = document.querySelector("#det");
			det.open = true;
			const tick = await window.__qa.tick(2);
			const live = document.querySelector("#det");
			return {mark: live.__qaMark || "", open: live.open,
			        attr: live.hasAttribute("open"), tick: tick};
		})()`, &got)

		Expect(got.Tick).To(Equal("tick 2"),
			"the region was not patched twice, so nothing was asked of the rule")
		Expect(got.Mark).To(Equal("det-node"),
			"the <details> was replaced rather than morphed, so this says nothing about the rule")
		Expect(got.Open).To(BeTrue(),
			"a <details> the user opened was closed by a patch that never mentioned it (FR-25). "+
				"open reflects to the content attribute in a browser, so morph read the user's own "+
				"state as a server declaration and reverted it")
		Expect(got.Attr).To(BeTrue(),
			"the open property survived but the attribute it reflects to did not, which means "+
				"syncProps and syncAttrs disagree about who owns this bit")

		AddReportEntry("FR-25 <details> open state (D-15)", fmt.Sprintf(
			"browser %s\n#det opened by the user, never mentioned by the server, and still open "+
				"after two patches: open=%v attribute=%v, node identity preserved",
			c.version, got.Open, got.Attr))
	})

	// The other half of the same rule, and the reason the fix for D-15 is not
	// "morph stops writing <details> open". A server that CHANGES its
	// declaration is still authoritative — the same bargain the checkbox rule
	// strikes, and the only way a server can close a disclosure at all.
	//
	// #serverdet carries open= at tick 1 and at no other tick, so one spec
	// watches the declaration arrive and then be withdrawn, over a disclosure
	// the user had opened first.
	It("still closes a <details> when the server withdraws its open declaration (FR-25)", func() {
		var got struct {
			Mark        string `json:"mark"`
			DeclOpen    bool   `json:"declOpen"`
			DeclAttr    bool   `json:"declAttr"`
			AfterWithdr bool   `json:"afterWithdraw"`
			Tick        string `json:"tick"`
		}
		c.evalJSON(`(async () => {
			window.__qa.mark("#serverdet", "serverdet-node");
			document.querySelector("#serverdet").open = true;   // the user opens it first

			await window.__qa.tick(1);   // tick 1: the server declares open=
			const declOpen = document.querySelector("#serverdet").open;
			const declAttr = document.querySelector("#serverdet").hasAttribute("open");

			const tick = await window.__qa.tick(1);   // tick 2: the server withdraws it
			const live = document.querySelector("#serverdet");
			return {mark: live.__qaMark || "", declOpen: declOpen, declAttr: declAttr,
			        afterWithdraw: live.open, tick: tick};
		})()`, &got)

		Expect(got.Tick).To(Equal("tick 2"))
		Expect(got.Mark).To(Equal("serverdet-node"),
			"the <details> was replaced rather than morphed, so this says nothing about the rule")
		Expect(got.DeclOpen).To(BeTrue(),
			"the server rendered <details open> and the disclosure is not open")
		Expect(got.DeclAttr).To(BeTrue(),
			"the server rendered <details open> and the attribute is not on the live element")
		Expect(got.AfterWithdr).To(BeFalse(),
			"the server withdrew its open declaration and morph did not follow it. The fix for "+
				"D-15 must preserve the user's disclosure against SILENCE, not against the server")

		AddReportEntry("FR-25 <details>, the server's half", fmt.Sprintf(
			"#serverdet: user opens it, tick 1 declares open= (open=%v, attribute=%v), "+
				"tick 2 withdraws the declaration and morph closes it (open=%v)",
			got.DeclOpen, got.DeclAttr, got.AfterWithdr))
	})

	// D-21. QA-1 measured save()/restore() at 617 minified bytes and found
	// that removing the scroll capture, and then neutering restore() entirely,
	// turned nothing red anywhere in the repository. The reason is that morph
	// preserves these nodes in place, so the FR-25 focus, caret and scroll
	// specs above pass without restore() existing at all.
	//
	// This is the case that does not: an id-matched element whose TAG changed,
	// which morphNode replaces outright and whose subtree comes back as fresh
	// server markup. It is reachable from ordinary server code — <div
	// id="card"> becoming <article id="card"> — and it is the only thing in
	// this runtime that save()/restore() is for.
	//
	// The first two assertions are what stop this spec from becoming a second
	// test of morph: it asserts the nodes were REPLACED before it asserts
	// anything was restored. The FR-27 scroll spec earlier in this project's
	// history read its identity tag off a captured node reference, which
	// survives detachment, and passed under a mutation that replaced every
	// node; the tags here are re-queried from the live document, and their
	// ABSENCE is the precondition.
	It("restores focus, the caret and scroll across a patch that REPLACED the node holding them (FR-25, D-21)", func() {
		var got struct {
			TagBefore  string  `json:"tagBefore"`
			TagAfter   string  `json:"tagAfter"`
			WrapMark   string  `json:"wrapMark"`
			DraftMark  string  `json:"draftMark"`
			ScrollMark string  `json:"scrollMark"`
			Active     string  `json:"active"`
			Start      int     `json:"start"`
			End        int     `json:"end"`
			Value      string  `json:"value"`
			ScrollTop  float64 `json:"scrollTop"`
			WantScroll float64 `json:"wantScroll"`
		}
		c.evalJSON(`(async () => {
			window.__qa.mark("#wrap", "wrap-node");
			window.__qa.mark("#draft2", "draft2-node");
			window.__qa.mark("#scroller2", "scroller2-node");
			const tagBefore = document.querySelector("#wrap").tagName;

			const d = document.querySelector("#draft2");
			d.focus();
			d.setSelectionRange(4, 8);

			const sc = document.querySelector("#scroller2");
			sc.scrollTop = 240;
			const wantScroll = sc.scrollTop;

			await window.__qa.tick(1);

			// Every read is re-queried from the live document. A detached node
			// keeps its expando, its scrollTop and its selection, so reading
			// any of this off the captured reference would report success for
			// a node the patch threw away.
			const live = document.querySelector("#draft2");
			return {
				tagBefore: tagBefore,
				tagAfter: document.querySelector("#wrap").tagName,
				wrapMark: window.__qa.markOf("#wrap"),
				draftMark: window.__qa.markOf("#draft2"),
				scrollMark: window.__qa.markOf("#scroller2"),
				active: document.activeElement ? document.activeElement.id : "",
				start: live.selectionStart,
				end: live.selectionEnd,
				value: live.value,
				scrollTop: document.querySelector("#scroller2").scrollTop,
				wantScroll: wantScroll,
			};
		})()`, &got)

		// Preconditions: the replacement really happened.
		Expect(got.TagAfter).NotTo(Equal(got.TagBefore),
			"the wrapper's tag did not change across the patch, so morph had no reason to replace it")
		Expect(got.WrapMark).To(BeEmpty(),
			"the wrapper was morphed in place despite the tag change, so this spec is exercising "+
				"morph rather than save/restore and proves nothing about D-21")
		Expect(got.DraftMark).To(BeEmpty(),
			"the focused input survived the replacement of its parent, so focus and caret were "+
				"never in danger and this spec is vacuous")
		Expect(got.ScrollMark).To(BeEmpty(),
			"the scroll container survived the replacement of its parent, so its offset was never "+
				"in danger and the scroll arm is vacuous")
		Expect(got.WantScroll).To(BeNumerically(">", 0),
			"the container did not scroll at all, so the scroll arm would pass on a runtime that lost it")
		Expect(got.Value).To(Equal("abcdefghij"),
			"the replacement input does not carry the server's declared value, so a caret offset "+
				"inside it would not mean anything")

		// And the state came back anyway, which is what the 617 bytes buy.
		Expect(got.Active).To(Equal("draft2"),
			"focus was not restored after the node holding it was replaced (FR-25): "+
				"activeElement is %q", got.Active)
		Expect(got.Start).To(Equal(4), "the caret start was not restored across the replacement (FR-25)")
		Expect(got.End).To(Equal(8), "the selection end was not restored across the replacement (FR-25)")
		Expect(got.ScrollTop).To(Equal(got.WantScroll),
			"element scroll offset was not restored across the replacement (FR-25): %.0f -> %.0f",
			got.WantScroll, got.ScrollTop)

		AddReportEntry("FR-25 restore across a replace (D-21)", fmt.Sprintf(
			"browser %s\n#wrap <%s id=wrap> -> <%s id=wrap>: REPLACED, and #draft2 and #scroller2 "+
				"with it (page expandos gone on all three)\nfocus %q  caret [%d,%d)  "+
				"#scroller2 scrollTop %.0f -> %.0f",
			c.version, strings.ToLower(got.TagBefore), strings.ToLower(got.TagAfter),
			got.Active, got.Start, got.End, got.WantScroll, got.ScrollTop))
	})
})

// ---------------------------------------------------------------------------
// FR-26 — IME and composition safety
// ---------------------------------------------------------------------------

var _ = Describe("IME composition safety (FR-26)", Ordered, ContinueOnFailure, Label("browser"), func() {
	var (
		c       *chrome
		ts      *httptest.Server
		ticking *httptest.Server
	)

	BeforeAll(func() {
		browserOnly()
		// Two applications, because the two clauses of FR-26 need opposite
		// conditions. The first needs a patch that arrives WITHOUT the browser
		// asking for one, which only a server-initiated ticker can produce.
		// The second needs a page where a click is the only possible source of
		// a patch, or "nothing happened" would prove nothing.
		ticking = startDOMTickingApp(300 * time.Millisecond)
		ts = startDOMApp()
		c = launchChrome()
		c.onNewDocument(qaHelpers)
	})

	// The composition is driven through CDP's Input.imeSetComposition, which
	// is the browser's own IME entry point: it raises real compositionstart
	// and compositionupdate events and puts the element into a real composing
	// state. That matters because the runtime's rule is expressed entirely in
	// those events — a spec that synthesised CompositionEvent objects from
	// page script would be checking that the runtime listens to what the spec
	// dispatches, which is a tautology.
	It("does not overwrite the value of an input with an active composition", func() {
		c.navigate(ticking.URL + "/")
		waitLivePanel(c)

		c.evalJSON(`(() => {
			window.__qaComposition = [];
			document.addEventListener("compositionstart", e => window.__qaComposition.push("start"));
			document.addEventListener("compositionend", e => window.__qaComposition.push("end"));
			window.__qa.mark("#controlled", "ime-node");
			document.querySelector("#controlled").focus();
			return null;
		})()`, nil)

		Expect(c.evalString(`document.activeElement.id`)).To(Equal("controlled"))

		// A four-character composition, uncommitted.
		c.call(c.sessionID, "Input.imeSetComposition", map[string]any{
			"text":           "にほんご",
			"selectionStart": 4,
			"selectionEnd":   4,
		}, nil)

		var events []string
		Eventually(func() []string {
			c.evalJSON(`window.__qaComposition`, &events)
			return events
		}, 10*time.Second, 100*time.Millisecond).Should(ContainElement("start"),
			"the browser raised no compositionstart, so no composition is active and FR-26 is unexercised")

		var got struct {
			Patched    bool     `json:"patched"`
			Mark       string   `json:"mark"`
			Value      string   `json:"value"`
			AttrBefore string   `json:"attrBefore"`
			AttrAfter  string   `json:"attrAfter"`
			Events     []string `json:"events"`
			Tick       string   `json:"tick"`
		}
		c.evalJSON(`(async () => {
			const attrBefore = document.querySelector("#controlled").getAttribute("value");
			// Two server-initiated patches, neither of them asked for by this
			// page, landing while the composition is open.
			const first = await window.__qa.waitPatch(15000);
			const second = await window.__qa.waitPatch(15000);
			const el = document.querySelector("#controlled");
			return {
				patched: first.ok && second.ok,
				mark: el.__qaMark || "",
				value: el.value,
				attrBefore: attrBefore,
				attrAfter: el.getAttribute("value"),
				events: window.__qaComposition,
				tick: second.after,
			};
		})()`, &got)

		Expect(got.Patched).To(BeTrue(),
			"no server-initiated patch arrived, so nothing was ever morphed over the composition")
		Expect(got.AttrAfter).NotTo(Equal(got.AttrBefore),
			"the patches carried the same server value=%q, so there was nothing for morph to overwrite with",
			got.AttrBefore)
		Expect(got.Events).NotTo(ContainElement("end"),
			"the composition ended before the patch arrived, so the patch did not land mid-composition")
		Expect(got.Mark).To(Equal("ime-node"),
			"the composing input was replaced outright, which destroys the composition by construction (FR-26)")
		Expect(got.Value).To(ContainSubstring("にほんご"),
			"morph overwrote the value of an input with an active IME composition: the composed text is "+
				"gone and the value is %q (FR-26)", got.Value)

		AddReportEntry("FR-26 IME composition", fmt.Sprintf(
			"browser %s\ncomposition driven by CDP Input.imeSetComposition (a real composition, not a synthetic event)\n"+
				"events seen by the page: %v\ntwo server-initiated patches landed mid-composition (%s)\n"+
				"server value attribute %q -> %q\ninput value after the patches: %q  node identity preserved: yes",
			c.version, got.Events, got.Tick, got.AttrBefore, got.AttrAfter, got.Value))
	})

	// The other half of FR-26: the runtime must not SEND mid-composition
	// either, because an event raised from a half-composed field carries a
	// value the user has not committed. runtime.js's dispatch returns early
	// while composing; this is that line, checked from outside.
	It("raises no event while a composition is active", func() {
		c.navigate(ts.URL + "/")
		waitLivePanel(c)

		c.evalJSON(`(() => {
			document.querySelector("#controlled").focus();
			return null;
		})()`, nil)

		c.call(c.sessionID, "Input.imeSetComposition", map[string]any{
			"text": "にほ", "selectionStart": 2, "selectionEnd": 2,
		}, nil)

		var got struct {
			Before string `json:"before"`
			After  string `json:"after"`
		}
		c.evalJSON(`(async () => {
			const line = () => document.querySelector("#tickline").textContent.trim();
			const before = line();
			document.querySelector("#tick").click();
			await new Promise(r => setTimeout(r, 1500));
			return {before: before, after: line()};
		})()`, &got)

		Expect(got.After).To(Equal(got.Before),
			"a click raised a live event while an IME composition was active: the tick advanced from %q to %q (FR-26)",
			got.Before, got.After)

		// And the suppression is temporary, not a wedged session: end the
		// composition and the same click works. Without this arm the spec
		// above would pass on a runtime whose event path was simply broken.
		c.call(c.sessionID, "Input.imeSetComposition", map[string]any{
			"text": "", "selectionStart": 0, "selectionEnd": 0,
		}, nil)

		var after string
		c.evalJSON(`(async () => {
			await new Promise(r => setTimeout(r, 200));
			return await window.__qa.tick(1);
		})()`, &after)
		Expect(after).NotTo(Equal(got.Before),
			"events never resumed after the composition ended, so the suppression above is a wedge, not a rule")

		AddReportEntry("FR-26 no event mid-composition", fmt.Sprintf(
			"during composition: click -> tickline unchanged at %q\nafter composition ends: %q", got.Before, after))
	})
})

// ---------------------------------------------------------------------------
// FR-27 — the explicit preserve opt-out
// FR-28 — event delegation survives morph
// ---------------------------------------------------------------------------

var _ = Describe("Preserve and delegation across a morph (FR-27, FR-28)", Ordered, ContinueOnFailure, Label("browser"), func() {
	var (
		c  *chrome
		ts *httptest.Server
	)

	BeforeAll(func() {
		browserOnly()
		ts = startDOMApp()
		c = launchChrome()
		c.onNewDocument(qaHelpers)
	})

	BeforeEach(func() {
		browserOnly()
		c.navigate(ts.URL + "/")
		waitLivePanel(c)
	})

	It("leaves a data-gotth-preserve subtree untouched, including content the page wrote (FR-27)", func() {
		var got struct {
			Mark  string `json:"mark"`
			Inner string `json:"inner"`
			Child string `json:"childMark"`
			Sib   string `json:"sibling"`
		}
		c.evalJSON(`(async () => {
			window.__qa.mark("#vault", "vault-node");
			const vault = document.querySelector("#vault");
			// Third-party-owned content: the server has never rendered this
			// and never will. FR-27 is the promise that morph does not care.
			vault.innerHTML = '<i id="owned">client owned</i>';
			document.querySelector("#owned").__qaMark = "owned-node";
			await window.__qa.tick(2);
			const live = document.querySelector("#vault");
			const child = document.querySelector("#owned");
			return {
				mark: live.__qaMark || "",
				inner: live.innerHTML,
				childMark: child ? (child.__qaMark || "") : "GONE",
				sibling: document.querySelector("#tickline").textContent.trim(),
			};
		})()`, &got)

		Expect(got.Sib).To(Equal("tick 2"),
			"the region was not patched at all, so nothing was asked of the preserve rule")
		Expect(got.Mark).To(Equal("vault-node"), "the preserved root itself was replaced (FR-27)")
		Expect(got.Inner).To(Equal(`<i id="owned">client owned</i>`),
			"morph rewrote the inside of a data-gotth-preserve element (FR-27): the server renders "+
				"<b>server N</b> there and got %q", got.Inner)
		Expect(got.Child).To(Equal("owned-node"),
			"a node inside the preserved subtree was replaced, so the subtree half of FR-27 does not hold")

		AddReportEntry("FR-27 preserve opt-out", fmt.Sprintf(
			"server renders <b>server 2</b> inside #vault; the page wrote %q and it is unchanged after two patches\n"+
				"root identity preserved: yes, subtree identity preserved: yes", got.Inner))
	})

	It("keeps a morphed subtree interactive, and a control the morph inserted works on its first click (FR-28)", func() {
		// The alt button does not exist at page load — it is rendered from
		// tick 2 onward. Nothing has ever bound to it, and there is no
		// re-binding step for the application to call, so if it works the
		// binding cannot be per-node.
		var got struct {
			AltExisted bool   `json:"altExisted"`
			Note       string `json:"note"`
			TickMark   string `json:"tickMark"`
			TickWorks  bool   `json:"tickWorks"`
		}
		c.evalJSON(`(async () => {
			window.__qa.mark("#tick", "tick-node");
			const altAtLoad = !!document.querySelector("#alt");
			await window.__qa.tick(2);
			const alt = document.querySelector("#alt");
			if (!alt) throw new Error("the morph never inserted #alt");
			const noteBefore = document.querySelector("#noteline").textContent.trim();
			alt.click();
			const changed = await window.__qa.waitFor(
				() => document.querySelector("#noteline").textContent.trim() !== noteBefore, 10000);
			// And the original button, morphed through two patches, still works.
			const lineBefore = document.querySelector("#tickline").textContent.trim();
			document.querySelector("#tick").click();
			const tickWorks = await window.__qa.waitFor(
				() => document.querySelector("#tickline").textContent.trim() !== lineBefore, 10000);
			return {
				altExisted: altAtLoad,
				note: changed ? document.querySelector("#noteline").textContent.trim() : "NO CHANGE",
				tickMark: document.querySelector("#tick").__qaMark || "",
				tickWorks: tickWorks,
			};
		})()`, &got)

		Expect(got.AltExisted).To(BeFalse(),
			"#alt was already in the first paint, so this proves nothing about a morph-inserted control")
		Expect(got.Note).To(Equal("note alt"),
			"a control the morph inserted raised no event on its first click: the binding is per-node "+
				"and the application would need a re-binding step (FR-28)")
		Expect(got.TickMark).To(Equal("tick-node"),
			"the original button was replaced across the morphs")
		Expect(got.TickWorks).To(BeTrue(),
			"the button that survived two morphs stopped raising events: morph destroyed its binding (FR-28)")

		AddReportEntry("FR-28 delegation", fmt.Sprintf(
			"#alt absent at first paint, inserted by the patch at tick 2, worked on its first click: %q\n"+
				"#tick survived two morphs with node identity intact and still raises events: %v",
			got.Note, got.TickWorks))
	})
})
