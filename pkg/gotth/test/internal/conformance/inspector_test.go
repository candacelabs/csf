package conformance_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/live"
)

// ---------------------------------------------------------------------------
// FR-44 — the session inspector, in a real browser.
//
// This file exists for the reason dev_reload_test.go does: the browser
// evidence for FR-44 came from a harness that was written, run once and thrown
// away, and docs/gates/phase-4.md §6 has carried "DEV-2's browser loop is not
// in CI" as a condition on Phase 4's exit ever since.
//
// # The defect class this is paid to catch
//
// 0c711b70 is the reason this file exists: the inspector shipped a panel that
// was mounted, had its shadow root, had adopted its stylesheet, and was
// PERMANENTLY EMPTY, while every node spec in client/test/inspector.test.mjs
// passed — because the model was fine and the model is what they drive. Only a
// browser could see it.
//
// So the assertion that matters below is not "the panel exists". It is "the
// panel has rows in it", which is BodyChildren and Rows. A panel whose paint
// never runs has a body element with no children at all — not even the "No
// frames yet" placeholder, which the same function paints.
//
// # A correction to that commit's stated cause, since this file had to test it
//
// 0c711b70's message, the comment it left in client/inspector.js, SIZE.md §8
// and gates/phase-4.md §4.5 all attribute the empty panel to render() calling
// requestAnimationFrame through `(globalThis.rAF || setTimeout)(...)`, "which
// invokes it with no receiver" and "throws Illegal invocation". Reproducing
// that was the obvious mutation control for this spec, so it was reproduced —
// twice, once by hand and once by restoring 0c711b70^'s whole inspector.js —
// rebuilt through tools/minify, and run. THE PANEL PAINTED BOTH TIMES.
//
// The mechanism does not hold in the browser it names. requestAnimationFrame
// is a member of the [Global] Window interface, and Web IDL defaults an
// undefined `this` on those to the global object, so a receiverless call is
// legal. Measured directly in the same image, Chromium 151.0.7922.71:
//
//	(globalThis.requestAnimationFrame || setTimeout)(fn, 16)   no-throw
//	  ... the same inside <script type="module">, i.e. strict   no-throw
//	const g = document.querySelector; g("div")                  Illegal invocation
//
// The third line is the contrast: querySelector is on Document, which is not
// [Global], so it really does throw. The empty panel was real; the diagnosis
// was not, and this file does not repeat it. Those four documents are not
// DEV-2's to edit this turn, so the correction is reported rather than applied.
//
// # The mutation control this file is actually held by
//
// The control therefore breaks the paint by a mechanism that does reproduce it:
// render() made to schedule nothing (`repaint = 1`), rebuilt through
// tools/minify. Both specs below go red, with the reading
//
//	mounted=true shadow=true styled=true bodyChildren=0 rows=0
//
// which is 0c711b70's symptom exactly. A second control removes the
// client_ref join in patchRow, leaving a panel that paints: the chain
// assertions go red on their own. The commit body carries both transcripts. A
// check that has never said no has not been shown able to.
//
// Run:
//
//	docker run --rm -v "$PWD:/workspace" -w /workspace/candace/pkg/gotth \
//	    dis-gotth-live-bench:latest \
//	    bash -c 'GOTTHLIVE_E2E=1 go test ./test/internal/conformance/ -count=1 \
//	        -args -ginkgo.label-filter=browser -ginkgo.fail-on-empty'
// ---------------------------------------------------------------------------

const (
	fragInspect = "inspect.panel"
	eventBump   = "inspect.bump"
)

// inspectState is the smallest state that produces a joinable chain.
//
// It matters that the reducer moves the SESSION's own state rather than
// delegating to a store: a transition the browser's own event caused produces
// a patch whose Origin is CLIENT_EVENT and carries client_ref, and
// Origin.client_ref is the one edge the inspector can close on its own. The
// counter example cannot exercise that join — its reducer returns a
// ChangeEffect and its own transition is suppressed, so the patch the browser
// sees is EFFECT-origin with client_ref 0 — which is why this file drives both
// applications and asserts different halves of the chain against each.
type inspectState struct{ N int }

func inspectHTML(s inspectState) string {
	var b strings.Builder
	b.WriteString(`<section` + attrsOf(live.Region(fragInspect)) + `>`)
	b.WriteString(`<b data-bench-id="value">` + strconv.Itoa(s.N) + `</b>`)
	b.WriteString(`<button type="button" data-bench-id="inc"` + attrsOf(live.On("click", eventBump)) + `>+1</button>`)
	b.WriteString(`</section>`)
	return b.String()
}

func inspectConfig() live.Config[inspectState, qaUser] {
	return live.Config[inspectState, qaUser]{
		Dev: true, // the switch that serves the inspector and renders its tag
		Init: func(ctx context.Context, session live.Session[qaUser]) (inspectState, []live.Effect[qaUser], error) {
			return inspectState{}, nil, nil
		},
		Reduce: func(s inspectState, ev live.Event) (inspectState, []live.Effect[qaUser]) {
			if ev.Name == eventBump {
				s.N++
			}
			return s, nil
		},
		Fragments: []live.Fragment[inspectState]{{
			ID:     fragInspect,
			Render: func(s inspectState) templ.Component { return raw(inspectHTML(s)) },
			Dirty:  func(prev, next inspectState) bool { return prev.N != next.N },
		}},
		Events:       []string{eventBump},
		Authenticate: func(request *http.Request) (qaUser, error) { return qaUser("inspector"), nil },
		Authorize:    live.AllowAll[qaUser],
		CSRF:         live.NoCSRFCheck,
	}
}

// serveInspected mounts inspectConfig behind a real server and serves a page
// built by (*App).Document — the one export that places the inspector tag.
//
// It is serveLive plus the App handle. serveLive cannot be reused as it stands
// because it builds the App internally and returns only the server, and the
// inspector tag comes from a METHOD on that App: the page cannot be written
// without it. Hand-writing the <script> tag instead would test a string this
// suite invented rather than the export FR-44 is about — the ordering rule
// (inspector above runtime, so it wraps WebSocket before the runtime opens
// one) is enforced by Document and by nothing a test could reproduce. So this
// duplicates serveLive's listener-before-app ordering, for the same reason
// stated there: Config.Origins is deny-by-default and has to name the address
// the browser will send, which is only knowable once the socket is bound.
func serveInspected() *httptest.Server {
	GinkgoHelper()

	ts := httptest.NewUnstartedServer(nil)

	cfg := inspectConfig()
	cfg.Origins = []string{"http://" + ts.Listener.Addr().String()}

	app, err := live.New(cfg)
	Expect(err).NotTo(HaveOccurred())

	mux := http.NewServeMux()
	mux.Handle(liveMount, app.Handler())
	mux.Handle(liveMount+"/", app.Handler())
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// The children are the first paint: the region has to be in the
		// document the browser loads, because a morph targets an element that
		// is already there.
		ctx := templ.WithChildren(r.Context(), raw(inspectHTML(inspectState{})))
		doc := app.Document(liveMount, "inspector conformance", templ.Attributes{"lang": "en"})
		if err := doc.Render(ctx, w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	ts.Config.Handler = mux
	ts.Start()
	DeferCleanup(func() {
		ts.Close()
		_ = app.Close(context.Background())
	})
	return ts
}

// ---------------------------------------------------------------------------
// Reading the panel
// ---------------------------------------------------------------------------

// inspectorPanel is what the panel is showing, read out of its shadow root.
//
// Errors comes back in the SAME reading rather than from a second evaluation,
// and that is not tidiness. A Gomega failure message's format arguments are
// evaluated when the assertion is constructed, not when it fails, so anything
// fetched separately for the message reports the state before the polling
// started. This file was written with that bug in it: the first mutation
// control's failure printed "mounted=false rows=0", which is the ZERO VALUE of
// the reading and not the last one, and the panel it was describing was in
// fact mounted. A failure message that is wrong about the state is worse than
// no message, so the reading is taken as one value and rendered lazily.
type inspectorPanel struct {
	Mounted      bool     `json:"mounted"`
	Shadow       bool     `json:"shadow"`
	Styled       bool     `json:"styled"`
	Head         string   `json:"head"`
	Placeholder  string   `json:"placeholder"`
	BodyChildren int      `json:"bodyChildren"`
	Rows         []string `json:"rows"`
	Details      []string `json:"details"`
	Errors       []string `json:"errors"`
}

// readPanel takes one reading. The panel is inside an open shadow root, which
// is the only way in — and is also why "the page looks right" cannot be
// substituted for it: nothing the application renders is affected by the
// inspector working or not.
func readPanel(c *chrome) inspectorPanel {
	GinkgoHelper()
	var p inspectorPanel
	c.evalJSON(`(() => {
		const errors = window.__pageErrors || [];
		const host = document.getElementById("gotth-live-inspector");
		if (!host) return {mounted:false, errors:errors};
		const root = host.shadowRoot;
		if (!root) return {mounted:true, shadow:false, errors:errors};
		const head = root.querySelector(".h");
		const body = root.querySelector(".b");
		const empty = body ? body.querySelector(".empty") : null;
		const rows = body ? Array.from(body.querySelectorAll("details.r")) : [];
		const flat = s => (s || "").replace(/\s+/g, " ").trim();
		return {
			mounted: true,
			shadow: true,
			styled: (root.adoptedStyleSheets || []).length > 0,
			head: flat(head ? head.textContent : ""),
			placeholder: flat(empty ? empty.textContent : ""),
			bodyChildren: body ? body.childElementCount : -1,
			rows: rows.map(r => flat(r.querySelector("summary").textContent)),
			details: rows.map(r => flat(r.textContent)),
			errors: errors,
		};
	})()`, &p)
	return p
}

// rowMatching returns the one row whose summary contains every needle.
func rowMatching(p inspectorPanel, needles ...string) (string, string) {
	for i, row := range p.Rows {
		hit := true
		for _, needle := range needles {
			if !strings.Contains(row, needle) {
				hit = false
				break
			}
		}
		if hit {
			return row, p.Details[i]
		}
	}
	return "", ""
}

// panelReport renders a reading for AddReportEntry and for failures.
func panelReport(p inspectorPanel) string {
	var b strings.Builder
	fmt.Fprintf(&b, "mounted=%t shadow=%t styled=%t bodyChildren=%d rows=%d\n",
		p.Mounted, p.Shadow, p.Styled, p.BodyChildren, len(p.Rows))
	fmt.Fprintf(&b, "head: %s\n", p.Head)
	if len(p.Errors) > 0 {
		fmt.Fprintf(&b, "page errors: %v\n", p.Errors)
	}
	for i, row := range p.Rows {
		fmt.Fprintf(&b, "row %d: %s\n", i, row)
		fmt.Fprintf(&b, "        %s\n", p.Details[i])
	}
	return b.String()
}

// waitPanel waits for the panel to have rows and returns the last reading.
//
// The failure message carries whatever the page threw, because the two ways a
// panel ends up with no rows — it threw, or nothing arrived — need different
// fixes and the difference is free to record here and expensive to work out
// later.
func waitPanel(c *chrome, atLeast int) inspectorPanel {
	GinkgoHelper()
	var last inspectorPanel
	Eventually(func() int {
		last = readPanel(c)
		return len(last.Rows)
	}, 30*time.Second, 200*time.Millisecond).Should(BeNumerically(">=", atLeast), func() string {
		// Lazy, so this describes the LAST reading rather than the first. See
		// inspectorPanel.
		return fmt.Sprintf("the inspector panel never rendered %d rows.\nlast reading:\n%s",
			atLeast, panelReport(last))
	})
	return last
}

// catchPageErrors installs the error listeners before any page script runs.
//
// It must be before, not after: the defect class this file is about throws
// while the inspector is booting, which is finished by the time a spec could
// attach a listener from Go.
func catchPageErrors(c *chrome) {
	GinkgoHelper()
	c.onNewDocument(`
		window.__pageErrors = [];
		window.addEventListener("error", e => {
			window.__pageErrors.push(String((e.error && e.error.stack) || e.message));
		});
		window.addEventListener("unhandledrejection", e => {
			window.__pageErrors.push("unhandled rejection: " + String(e.reason));
		});
	`)
}

// clickInc dispatches one real click on the +1 button and waits for the value
// to move, so that everything the panel is then asked about has happened.
func clickInc(c *chrome) {
	GinkgoHelper()
	before := c.evalString(`document.querySelector(` + jsStr(selInc) + `).parentNode.querySelector("[data-bench-id=value]").textContent.trim()`)
	c.evalJSON(`(() => { document.querySelector(`+jsStr(selInc)+`).click(); return null; })()`, nil)
	Eventually(func() string {
		return c.evalString(`document.querySelector("[data-bench-id=value]").textContent.trim()`)
	}, 30*time.Second, 100*time.Millisecond).ShouldNot(Equal(before),
		"the click never reached the DOM, so there is no chain for the inspector to show")
}

var causeRe = regexp.MustCompile(`← #(\d+)`)
var rowNoRe = regexp.MustCompile(`^#(\d+)`)

// ---------------------------------------------------------------------------
// The specs
// ---------------------------------------------------------------------------

var _ = Describe("The session inspector in a real browser (FR-44)", Label("browser", "inspector"), func() {

	// The whole clause, against an application whose own transition is the one
	// the browser caused — so Origin carries CLIENT_EVENT and a client_ref,
	// and the join the panel draws is the join FR-44 names.
	It("mounts and renders the causal chain after one real click", func() {
		browserOnly()

		server := serveInspected()
		c := launchChrome()

		catchPageErrors(c)

		c.navigate(server.URL + "/")

		// The panel is mounted before anything is clicked, and it paints the
		// mount snapshot on its own.
		mounted := waitPanel(c, 1)
		Expect(mounted.Mounted).To(BeTrue(), "no #gotth-live-inspector element was added to the page")
		Expect(mounted.Shadow).To(BeTrue(), "the panel did not attach a shadow root")
		Expect(mounted.Styled).To(BeTrue(),
			"the panel adopted no constructed stylesheet, so it fell back to being unstyled")

		clickInc(c)

		panel := waitPanel(c, 3)

		// The assertion the defect fails. A panel that mounted and then threw
		// out of render() has a body element with NO children — not even the
		// "No frames yet" placeholder, which is painted by the same function.
		Expect(panel.BodyChildren).To(BeNumerically(">", 0),
			"the panel is mounted and its body is empty: nothing painted.\n%s", panelReport(panel))
		Expect(panel.Placeholder).To(BeEmpty(),
			"the panel is showing its empty-state placeholder after a real click:\n%s", panelReport(panel))
		Expect(panel.Errors).To(BeEmpty(), "the page raised errors while the inspector was running")

		// The head line: the name, the session id, the status, the sequence
		// and the state version — the summary a developer reads first. The
		// spans carry no separators, so this is one regexp over the run.
		Expect(panel.Head).To(MatchRegexp(`^gotth-live[0-9a-f]{8}live`),
			"the panel head does not name the session and its status: %q", panel.Head)
		Expect(panel.Head).To(MatchRegexp(`seq \d+`))
		Expect(panel.Head).To(MatchRegexp(`v[1-9]\d*`))

		// The mount snapshot.
		snapshot, _ := rowMatching(panel, "↓ snapshot", "MOUNT")
		Expect(snapshot).NotTo(BeEmpty(), "no MOUNT snapshot row:\n%s", panelReport(panel))

		// The event the browser sent, and — this is the join — the state
		// version it moved the server to and the patch it produced. The event
		// row's tail is written by patchRow()'s join and by nothing else, so
		// asserting its shape asserts the join happened.
		event, eventDetail := rowMatching(panel, "↑ "+eventBump, " ref ")
		Expect(event).NotTo(BeEmpty(), "no row for the event the click sent:\n%s", panelReport(panel))
		Expect(event).To(MatchRegexp(`→ event \d+ · v[1-9]\d* · [1-9]\d* patch`),
			"the event row does not name the state version it moved the server to and the patch it "+
				"produced, so Origin.client_ref did not join:\n%s", panelReport(panel))
		Expect(eventDetail).To(ContainSubstring("client_ref"))

		// The patch that answered it, joined back the other way.
		patch, patchDetail := rowMatching(panel, "↓ patch", "CLIENT_EVENT")
		Expect(patch).NotTo(BeEmpty(), "no CLIENT_EVENT patch row:\n%s", panelReport(panel))
		Expect(patch).To(ContainSubstring("event:" + eventBump))
		Expect(patch).To(ContainSubstring("MORPH " + fragInspect))
		Expect(patch).To(MatchRegexp(`↓ patch [1-9]\d*`), "the patch row carries no patch_id")

		// The two rows name each other. This is the edge itself rather than
		// its shape: the patch's "← #n" must be the event row's own number.
		cause := causeRe.FindStringSubmatch(patch)
		Expect(cause).NotTo(BeNil(), "the patch row names no causing row:\n%s", panelReport(panel))
		number := rowNoRe.FindStringSubmatch(event)
		Expect(number).NotTo(BeNil(), "the event row has no row number:\n%s", panelReport(panel))
		Expect(cause[1]).To(Equal(number[1]),
			"the patch says it was caused by row #%s and the event is row #%s", cause[1], number[1])

		for _, field := range []string{"patch_id", "state_version", "transition_id", "origin.client_ref"} {
			Expect(patchDetail).To(ContainSubstring(field),
				"the patch row does not show %s:\n%s", field, panelReport(panel))
		}

		AddReportEntry("FR-44 — the causal chain after one real click", fmt.Sprintf(
			"browser %s\n%s", c.version, panelReport(panel)))
	})

	// The documented path, which is a different application and a different
	// half of the chain.
	//
	// examples/counter is what docs/guide/inspector.md tells a reader to open,
	// its Dev flag is set in its own main.go, and its page comes from
	// app.Document — so this is the inspector reaching a browser the way a
	// consumer gets it, through a compiled binary this suite did not
	// configure. What it shows is the EFFECT half: the counter's reducer
	// returns a ChangeEffect, its own transition changes no session state and
	// is suppressed, and the visible patch is the store's broadcast — origin
	// EFFECT, client_ref 0, contributing_event_ids naming the click.
	It("populates against examples/counter, the application the guide points at", Label("e2e"), func() {
		e2eOnly()
		browserOnly()

		server := startCounter()
		c := launchChrome()
		catchPageErrors(c)

		c.navigate("http://" + server.addr + "/")
		waitLive(c)

		panel := waitPanel(c, 3)

		Expect(panel.BodyChildren).To(BeNumerically(">", 0),
			"the panel is mounted on the example's page and painted nothing:\n%s", panelReport(panel))
		Expect(panel.Placeholder).To(BeEmpty())

		event, _ := rowMatching(panel, "↑ counter.increment", " ref ")
		Expect(event).NotTo(BeEmpty(), "no row for the click:\n%s", panelReport(panel))

		patch, patchDetail := rowMatching(panel, "↓ patch", "EFFECT")
		Expect(patch).NotTo(BeEmpty(), "no patch row for the effect the click scheduled:\n%s", panelReport(panel))
		Expect(patch).To(ContainSubstring("effect:counter.watch"))
		Expect(patch).To(ContainSubstring("MORPH counter.value"))
		Expect(patchDetail).To(ContainSubstring("contributing_event_ids"),
			"the fan-out patch does not show the event that contributed to it, which is the only "+
				"edge back to the click this application produces:\n%s", panelReport(panel))

		AddReportEntry("FR-44 — examples/counter under the inspector", fmt.Sprintf(
			"browser %s\n%s", c.version, panelReport(panel)))
	})
})
