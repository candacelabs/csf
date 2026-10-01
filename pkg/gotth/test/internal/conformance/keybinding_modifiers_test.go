package conformance_test

import (
	"context"
	"fmt"
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
// FR-54 failure 1 and the benchmark equivalence spec's F-CHT-3 — "Enter sends,
// Shift+Enter inserts a newline" — driven through Chromium.
//
// Run:
//
//	docker run --rm -v "$PWD:/workspace" -w /workspace/candace/pkg/gotth \
//	    dis-gotth-live-bench:latest bash -c 'GOTTHLIVE_E2E=1 go test \
//	    ./test/internal/conformance/ -count=1 -args -ginkgo.label-filter=browser'
//
// # Why this file exists at all, rather than four more specs in keybinding_test.go
//
// docs/reviews/fr-54.md §14 C-4 requires keybinding_test.go's "does not take
// the key away from the browser" to stay green AND UNEDITED — it is the spec
// that owns the promise these two new options are allowed to break only where
// a binding asks. C-7 asks for the F-CHT-3 driver to live in "the package whose
// own header argues that only a browser knows what e.key is". That package is
// this one; that file is not the only place in it. So the fixture and the
// specs are here, keybinding_test.go is byte-identical to HEAD, and both
// constraints hold literally rather than one of them by interpretation.
//
// # Why a browser and not the node suite
//
// client/test/binding.test.mjs has the same properties and its evidence is
// weaker in the exact way keybinding_test.go's header describes: a node harness
// BUILDS the event object it then reads, so a spec that supplies shiftKey and
// then asserts the runtime read shiftKey has asserted nothing about a keyboard.
// docs/reviews/fr-54.md §13's T-3 makes that objection against L9-1's own
// evidence for the accepted shape and pre-registers this run as what settles
// it: "if it settles the other way, the refusal re-opens with it."
//
// Here Chromium's own input pipeline produces the event. The runtime reads
// e.key, e.shiftKey, e.ctrlKey and e.altKey off an object this file never
// touched, the browser decides for itself whether a suppressed keydown inserts
// a line break, and the newline — or its absence — is asserted against the
// SERVER's copy of the draft as well as against the box.
// ---------------------------------------------------------------------------

const fragMods = "kbm.panel"

const (
	eventModSend   = "kbm.send"
	eventModDraft  = "kbm.draft"
	eventModStrict = "kbm.strict"
	eventModLoose  = "kbm.loose"
	eventModTick   = "kbm.tick"

	// FR54-8. NoModifiers is tested whether or not the binding has Keys, and a
	// MouseEvent carries the same four *Key booleans a KeyboardEvent does — so
	// the option on a click binding means "a plain click, not a Ctrl+click".
	// Same two-binding shape as strict/loose above, for the same reason.
	eventModClickPlain = "kbm.click.plain"
	eventModClickAny   = "kbm.click.any"
)

// modState is the server's own record. Draft is what the composer's input
// binding delivered, so "the browser inserted a line break" is assertable
// against a value the SERVER produced and not only against the DOM.
type modState struct {
	Events int
	Log    string
	Draft  string
	Sends  int
}

// modPanelHTML is the fixture, and every binding in it is rendered FROM
// live.OnAll and live.OnWith rather than written out as a data-gotth-*
// literal — the binding a consumer would write is the binding the browser
// receives.
//
//   - #composer is F-CHT-3, verbatim from docs/reviews/fr-54.md §12.1: a
//     textarea bound keydown/Enter with NoModifiers AND PreventDefault, beside
//     an input binding with a 150 ms debounce and neither option.
//   - #altgr is C-6. Two bindings for the same key on one element, differing
//     only in NoModifiers, so a press that the strict one refuses is caught by
//     the loose one behind it — which makes "it did not match" an ARRIVAL
//     rather than an absence, and therefore not confusable with a press the
//     browser swallowed.
//   - #tick is the ordering sentinel. Frames on one connection are ordered, so
//     a sentinel that has arrived with the counter at the expected value
//     proves nothing was raised in between.
func modPanelHTML(s modState) string {
	var b strings.Builder
	b.WriteString(`<section` + attrsOf(live.Region(fragMods)) + `>`)
	b.WriteString(`<p id="m-events">` + strconv.Itoa(s.Events) + `</p>`)
	b.WriteString(`<p id="m-sends">` + strconv.Itoa(s.Sends) + `</p>`)
	b.WriteString(`<p id="m-log">` + templ.EscapeString(s.Log) + `</p>`)
	b.WriteString(`<p id="m-draftline">` + templ.EscapeString(s.Draft) + `</p>`)

	// No text content, so the server never declares the textarea's value and
	// what the member typed stays the member's (FR-25). #m-draftline is the
	// server's own copy.
	b.WriteString(`<textarea id="composer" name="draft"` + attrsOf(live.OnAll(
		live.OnWith("keydown", eventModSend, live.Bind{
			Keys: []string{"Enter"}, NoModifiers: true, PreventDefault: true,
		}),
		live.OnWith("input", eventModDraft, live.Bind{Debounce: 150 * time.Millisecond}),
	)) + `></textarea>`)

	b.WriteString(`<div id="altgr" tabindex="0"` + attrsOf(live.OnAll(
		live.OnWith("keydown", eventModStrict, live.Bind{Keys: []string{"@"}, NoModifiers: true}),
		live.OnWith("keydown", eventModLoose, live.Bind{Keys: []string{"@"}}),
	)) + `>altgr</div>`)

	// FR54-8's fixture. A CLICK binding with NoModifiers and no Keys at all —
	// docs/reviews/fr-54.md §21.2's "click:c.plain:::::1", rendered from
	// live.OnWith rather than written out, like every other binding here.
	// The loose binding behind it does the same job it does on #altgr: it
	// turns "the strict one did not match" into an ARRIVAL, so a modified
	// click that reaches nobody cannot be confused with a click the browser
	// never delivered.
	//
	// A <div> and not an <a> or a <button> deliberately. dispatch suppresses
	// the default for a submit and for an anchor click ABOVE the composition
	// guard, and this spec is about the modifier filter and nothing else; an
	// anchor would put a preventDefault into the path under test and a real
	// navigation into the failure mode.
	b.WriteString(`<div id="plainclick" tabindex="0"` + attrsOf(live.OnAll(
		live.OnWith("click", eventModClickPlain, live.Bind{NoModifiers: true}),
		live.OnWith("click", eventModClickAny, live.Bind{}),
	)) + `>plainclick</div>`)

	b.WriteString(`<div id="tick" tabindex="0"` +
		attrsOf(live.OnWith("keydown", eventModTick, live.Bind{Keys: []string{"t"}})) + `>tick</div>`)

	b.WriteString(`</section>`)
	return b.String()
}

func modConfig() live.Config[modState, qaUser] {
	return live.Config[modState, qaUser]{
		Init: func(ctx context.Context, session live.Session[qaUser]) (modState, []live.Effect[qaUser], error) {
			return modState{}, nil, nil
		},
		Reduce: func(s modState, ev live.Event) (modState, []live.Effect[qaUser]) {
			s.Events++
			s.Log = strings.TrimSpace(s.Log + " " + ev.Name)
			switch ev.Name {
			case eventModSend:
				s.Sends++
			case eventModDraft:
				s.Draft = ev.Fields.Get("draft")
			}
			return s, nil
		},
		Fragments: []live.Fragment[modState]{{
			ID:     fragMods,
			Render: func(s modState) templ.Component { return raw(modPanelHTML(s)) },
			Dirty:  func(prev, next modState) bool { return prev != next },
		}},
		Events: []string{
			eventModSend, eventModDraft, eventModStrict, eventModLoose, eventModTick,
			eventModClickPlain, eventModClickAny,
		},
		Authenticate: func(request *http.Request) (qaUser, error) { return qaUser("qa"), nil },
		Authorize:    live.AllowAll[qaUser],
		CSRF:         live.NoCSRFCheck,
	}
}

func startModApp() *httptest.Server {
	GinkgoHelper()
	return serveLive(modConfig(), map[string]http.HandlerFunc{
		"/": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprint(w, htmlDoc("QA modifier bindings", scriptTag(), modPanelHTML(modState{})))
		},
	})
}

// kbmHelpers reads the server's rendered state back as one object, so a spec's
// assertions all come from the same instant, and never sleeps.
const kbmHelpers = `
window.__kbm = {
  text(sel) { return document.querySelector(sel).textContent.trim(); },
  focus(sel) {
    const el = document.querySelector(sel);
    el.focus();
    if (document.activeElement !== el) throw new Error(sel + " did not take focus");
    return true;
  },
  read() {
    return {
      events: +this.text("#m-events"),
      sends:  +this.text("#m-sends"),
      log:    this.text("#m-log"),
      // NOT trimmed, and that is the point: the whole second half of F-CHT-3
      // is a trailing line break, and .trim() is what silently swallowed it
      // the first time these specs ran.
      draft:  document.querySelector("#m-draftline").textContent,
      value:  document.querySelector("#composer").value,
    };
  },
  // compose fires a real CompositionEvent at the textarea. The runtime's
  // document-level compositionstart listener is what sets its guard, and this
  // is the one part of an IME a headless run can reproduce without a real
  // input method: the guard's INPUT is a DOM event, and this is that event.
  compose(on) {
    const el = document.querySelector("#composer");
    el.dispatchEvent(new CompositionEvent(on ? "compositionstart" : "compositionend", { bubbles: true }));
    return true;
  },
  async waitEvents(n, ms) {
    const deadline = performance.now() + (ms || 15000);
    for (;;) {
      const seen = +this.text("#m-events");
      if (seen >= n) return seen;
      if (performance.now() > deadline) {
        throw new Error("only " + seen + " events reached the server, wanted " + n +
                        "; log=" + JSON.stringify(this.text("#m-log")));
      }
      await new Promise(r => setTimeout(r, 15));
    }
  },
};
`

// pressMod is press with the CDP modifier bitmask set — Alt 1, Ctrl 2, Meta 4,
// Shift 8 — which is how a chord goes in through the browser's own input
// pipeline rather than being written onto an event object by the spec.
func (c *chrome) pressMod(key, code, text string, modifiers int) {
	GinkgoHelper()
	down := map[string]any{"type": "keyDown", "key": key, "code": code, "modifiers": modifiers}
	if text != "" {
		down["text"] = text
	}
	c.call(c.sessionID, "Input.dispatchKeyEvent", down, nil)
	c.call(c.sessionID, "Input.dispatchKeyEvent",
		map[string]any{"type": "keyUp", "key": key, "code": code, "modifiers": modifiers}, nil)
}

// clickMod is pressMod's mouse twin, and it exists for the same reason: the
// modifier is held through Chromium's own input pipeline and the browser
// constructs the MouseEvent, so a spec that asserts the runtime read ctrlKey
// has asserted something about a MOUSE and not about an object the spec built.
// That is this file's whole argument (see the header) and it is what makes
// FR54-8 a browser spec rather than a node one.
//
// The coordinates come from the element's own box, so the click lands on the
// element under test rather than on a guess.
func (c *chrome) clickMod(sel string, modifiers int) {
	GinkgoHelper()
	var at struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
	}
	c.evalJSON(`(() => {
	  const r = document.querySelector(`+strconv.Quote(sel)+`).getBoundingClientRect();
	  if (r.width === 0 || r.height === 0) throw new Error(`+strconv.Quote(sel)+` + " has no box to click");
	  return { x: r.left + r.width / 2, y: r.top + r.height / 2 };
	})()`, &at)

	base := map[string]any{
		"x": at.X, "y": at.Y, "button": "left", "clickCount": 1, "modifiers": modifiers,
	}
	down := map[string]any{"type": "mousePressed", "buttons": 1}
	up := map[string]any{"type": "mouseReleased", "buttons": 0}
	for _, phase := range []map[string]any{down, up} {
		for k, v := range base {
			phase[k] = v
		}
		c.call(c.sessionID, "Input.dispatchMouseEvent", phase, nil)
	}
}

// The CDP modifier bits, named so a spec reads as the chord it is pressing.
const (
	modAlt   = 1
	modCtrl  = 2
	modShift = 8
	// AltGr is not a modifier of its own: the browser reports it as Control
	// AND Alt held together, which is the whole of C-6's second sentence.
	modAltGr = modCtrl | modAlt
)

type kbmRead struct {
	Events int    `json:"events"`
	Sends  int    `json:"sends"`
	Log    string `json:"log"`
	Draft  string `json:"draft"`
	Value  string `json:"value"`
}

var _ = Describe("A binding can demand no modifier and can take the key (FR-54 failure 1, F-CHT-3)",
	Ordered, ContinueOnFailure, Label("browser"), func() {
		var (
			c  *chrome
			ts *httptest.Server
		)

		BeforeAll(func() {
			browserOnly()
			ts = startModApp()
			c = launchChrome()
			c.onNewDocument(kbmHelpers)
			c.navigate(ts.URL + "/")

			Eventually(func() string {
				return c.evalString(`document.documentElement.getAttribute("data-gotth-status") || ""`)
			}, 30*time.Second, 100*time.Millisecond).Should(Equal("live"),
				"the client runtime never reported a live connection")
		})

		read := func() kbmRead {
			GinkgoHelper()
			var got kbmRead
			c.evalJSON(`window.__kbm.read()`, &got)
			return got
		}

		waitEvents := func(n int) {
			GinkgoHelper()
			var seen int
			c.evalJSON(fmt.Sprintf(`window.__kbm.waitEvents(%d)`, n), &seen)
		}

		focus := func(sel string) {
			GinkgoHelper()
			Expect(c.evalBool(`window.__kbm.focus(` + strconv.Quote(sel) + `)`)).To(BeTrue())
		}

		// The sentinel: a key on a different element raising a distinct event.
		// Frames on one connection are ordered, so once it has arrived and the
		// counter is where the spec expects, nothing was raised in between.
		tick := func() {
			GinkgoHelper()
			focus("#tick")
			c.press("t", "KeyT", "t")
		}

		// C-7, first half. This is the half a key filter alone cannot do: the
		// event must arrive AND the browser must not insert the newline, and
		// the second clause is why Bind.PreventDefault exists at all.
		It("Enter sends, and the browser does not insert the newline (F-CHT-3)", func() {
			before := read()
			focus("#composer")

			c.call(c.sessionID, "Input.insertText", map[string]any{"text": "hi"}, nil)
			waitEvents(before.Events + 1) // the debounced draft
			Expect(read().Draft).To(Equal("hi"))

			c.press("Enter", "Enter", "\r")
			waitEvents(before.Events + 2)

			mid := read()
			Expect(mid.Sends).To(Equal(before.Sends+1), "Enter did not reach the binding that names it")
			Expect(mid.Value).To(Equal("hi"),
				"the newline was inserted anyway: Bind.PreventDefault did not take the key")

			// The sentinel proves the absence: had the newline gone in, the
			// input binding would have raised a third event before this one.
			tick()
			waitEvents(before.Events + 3)

			after := read()
			Expect(after.Events).To(Equal(before.Events+3),
				"an extra event followed the Enter, which is the suppressed input: log %q", after.Log)
			Expect(after.Log).To(HaveSuffix(eventModDraft + " " + eventModSend + " " + eventModTick))
			Expect(after.Draft).To(Equal("hi"), "the server's copy of the draft gained something")

			AddReportEntry("F-CHT-3 first half", fmt.Sprintf(
				"browser %s: Enter on a NoModifiers+PreventDefault binding raised %s and the "+
					"textarea value stayed %q", c.version, eventModSend, after.Value))
		})

		// C-7, second half, and the one docs/reviews/fr-54.md §13's T-3 says
		// can re-open the refusal. Shift+Enter must reach NOBODY on the server
		// and must still put a line break in the box — asserted against the
		// server's own copy of the draft as well as against the DOM, because a
		// value read from the box alone would not prove the browser did it.
		It("Shift+Enter reaches nobody and the browser inserts the line break (F-CHT-3)", func() {
			before := read()
			focus("#composer")

			c.pressMod("Enter", "Enter", "\r", modShift)
			waitEvents(before.Events + 1) // the debounced draft the line break caused

			tick()
			waitEvents(before.Events + 2)

			after := read()
			Expect(after.Sends).To(Equal(before.Sends),
				"Shift+Enter reached the send binding: the message sends when the member wanted a newline")
			Expect(after.Events).To(Equal(before.Events+2),
				"exactly the draft and the sentinel: log %q", after.Log)
			Expect(after.Log).To(HaveSuffix(eventModDraft + " " + eventModTick))
			Expect(after.Value).To(Equal(before.Value+"\n"),
				"the browser did not insert the line break, so this binding took a key it never asked for")
			Expect(after.Draft).To(Equal(before.Value+"\n"),
				"the line break never reached the server, so the composer's own draft is wrong")

			AddReportEntry("F-CHT-3 second half", fmt.Sprintf(
				"browser %s: Shift+Enter raised no %s and left value=%q, server draft=%q",
				c.version, eventModSend, after.Value, after.Draft))
		})

		// C-9, in the browser. The node spec emits compositionstart and then
		// the Enter; this one does the same with a real CompositionEvent and a
		// real key press, and asserts what a member using an IME would see:
		// the candidate's commit key does what the IME needs, and the binding
		// does not fire and does not suppress.
		It("leaves Enter alone mid-composition, so the IME still commits (FR-26)", func() {
			before := read()
			focus("#composer")
			Expect(c.evalBool(`window.__kbm.compose(true)`)).To(BeTrue())

			c.press("Enter", "Enter", "\r")

			// End the composition before the sentinel, so the guard cannot be
			// what silences the sentinel too.
			Expect(c.evalBool(`window.__kbm.compose(false)`)).To(BeTrue())
			tick()
			waitEvents(before.Events + 1) // the sentinel, and nothing else

			after := read()
			Expect(after.Sends).To(Equal(before.Sends),
				"an event was sent mid-composition, which FR-26 forbids on its own")
			// The value is what settles C-9, and it is asserted as a DELTA
			// rather than as "contains a newline": the box already holds the
			// line break the previous spec's Shift+Enter put there, so a
			// substring match here would be green against a suppressed key.
			Expect(after.Value).To(Equal(before.Value+"\n"),
				"preventDefault fired mid-composition: an IME's commit key was taken away, "+
					"which is the defect docs/reviews/fr-54.md §14 C-9 exists to prevent")
			// The line break's own input event is guarded too, which is FR-26
			// working and is worth pinning rather than being surprised by: the
			// composition guard sits above the send, so mid-composition NOTHING
			// reaches the wire — not the key binding and not the draft beside it.
			Expect(after.Events).To(Equal(before.Events+1),
				"something reached the server mid-composition: log %q", after.Log)
			Expect(after.Log).To(HaveSuffix(eventModTick))

			AddReportEntry("C-9 composition guard", fmt.Sprintf(
				"browser %s: Enter during an active composition raised no %s and was NOT suppressed; "+
					"value=%q", c.version, eventModSend, after.Value))
		})

		// The binding works normally after the commit, which is the other half
		// of the guard: a composer that never fires again is as broken as one
		// that eats the commit.
		It("sends on the Enter after the composition has ended", func() {
			before := read()
			focus("#composer")

			c.press("Enter", "Enter", "\r")
			waitEvents(before.Events + 1)

			after := read()
			Expect(after.Sends).To(Equal(before.Sends + 1))
			Expect(after.Log).To(HaveSuffix(eventModSend))
		})

		// C-6, in a browser, and the reason it is asserted rather than left in
		// a godoc: AltGr is a real key on real keyboards, and a NoModifiers
		// binding that names a character needing it fires NEVER while the
		// member types exactly that character. The loose binding behind it is
		// what turns "did not match" into an arrival — so this also pins the
		// fall-through the whole two-binding shape depends on.
		It("does not match an AltGr press, because AltGr is Ctrl and Alt (C-6)", func() {
			before := read()
			focus("#altgr")

			c.pressMod("@", "Digit2", "@", modAltGr)
			waitEvents(before.Events + 1)

			mid := read()
			Expect(mid.Log).To(HaveSuffix(eventModLoose),
				"AltGr+@ matched the NoModifiers binding, or matched nothing at all")

			focus("#altgr")
			c.press("@", "Digit2", "@")
			waitEvents(before.Events + 2)

			after := read()
			Expect(after.Log).To(HaveSuffix(eventModLoose+" "+eventModStrict),
				"the same key with nothing held must match the NoModifiers binding")
			Expect(after.Events).To(Equal(before.Events + 2))

			AddReportEntry("C-6 AltGr", fmt.Sprintf(
				"browser %s: AltGr+@ fell through to %s and bare @ matched %s",
				c.version, eventModLoose, eventModStrict))
		})

		// Each of the four, one press each, through the browser's own modifier
		// bitmask. A runtime that stopped reading any one of them would raise
		// the strict event for that chord and this goes red naming it.
		DescribeTable("reads all four modifier booleans, not a subset",
			func(mask int, chord string) {
				before := read()
				focus("#altgr")

				c.pressMod("@", "Digit2", "@", mask)
				waitEvents(before.Events + 1)

				after := read()
				Expect(after.Log).To(HaveSuffix(eventModLoose),
					"%s+@ reached the NoModifiers binding, so that modifier is not read", chord)
			},
			Entry("Shift", modShift, "Shift"),
			Entry("Control", modCtrl, "Control"),
			Entry("Alt", modAlt, "Alt"),
			Entry("Meta", 4, "Meta"),
		)

		// C-4, restated from this side. keybinding_test.go's "does not take the
		// key away from the browser" is green and unedited because the binding
		// it drives sets neither option; this asserts the same promise on THIS
		// fixture, for the binding beside the one that does set them. A key a
		// binding named without asking for the default still does what the
		// browser was going to do with it.
		It("takes no key away from a binding that did not ask", func() {
			before := read()
			focus("#altgr")

			// #altgr's bindings set neither option, so a press there must
			// reach the server AND leave the browser's own behaviour alone.
			c.press("@", "Digit2", "@")
			waitEvents(before.Events + 1)

			focus("#composer")
			c.call(c.sessionID, "Input.insertText", map[string]any{"text": "x"}, nil)
			waitEvents(before.Events + 2)

			after := read()
			Expect(after.Draft).To(HaveSuffix("x"),
				"the input binding beside a PreventDefault binding stopped delivering the draft")
		})

		// FR54-8 (docs/reviews/fr-54.md §21.2, §23). NoModifiers is tested
		// whether or not the binding has Keys, and a MouseEvent carries the
		// same four *Key booleans a KeyboardEvent does — so the option on a
		// click binding means "a plain click, not a Ctrl+click".
		//
		// That sentence is in live.Bind's godoc, in client/SIZE.md §7's table
		// and in docs/api-surface.md. Before this spec it was asserted NOWHERE:
		// there was no click binding with NoModifiers in binding.test.mjs or in
		// this file. L9-1 drove it by hand and found it true, and said the
		// finding is that nothing checks it — "replacing an untested false
		// sentence with an untested true one repeats the defect one level
		// down, and the original was wrong precisely because nobody had tested
		// it."
		//
		// Ctrl and Shift are the two chords a member actually produces on a
		// click without meaning to click plainly — Ctrl+click is
		// open-in-new-tab and Shift+click is extend-selection — so they are
		// the two the sentence has to be right about.
		It("filters a Ctrl+click and a Shift+click out of a NoModifiers click binding, and lets a plain click through (FR54-8)", func() {
			before := read()

			// Modified first, plain last. The strict binding is only proved to
			// be a FILTER if the same element, the same button and the same
			// pixel reach it once the modifier is let go — otherwise a runtime
			// that had simply stopped delivering clicks would be green here.
			c.clickMod("#plainclick", modCtrl)
			waitEvents(before.Events + 1)
			Expect(read().Log).To(HaveSuffix(eventModClickAny),
				"Ctrl+click matched the NoModifiers click binding, or reached nobody at all: "+
					"a MouseEvent's ctrlKey is not being read")

			c.clickMod("#plainclick", modShift)
			waitEvents(before.Events + 2)
			Expect(read().Log).To(HaveSuffix(eventModClickAny+" "+eventModClickAny),
				"Shift+click matched the NoModifiers click binding: a MouseEvent's shiftKey is not being read")

			c.clickMod("#plainclick", 0)
			waitEvents(before.Events + 3)

			after := read()
			// ONE event, and the strict one. dispatch breaks out of the match
			// loop on the first binding that matches, so the loose binding
			// behind it does not also fire — a filtered-out binding falls
			// through, a MATCHED one ends the loop. Same shape #altgr pins on
			// a keyboard, and the asymmetry is the whole point of the two-
			// binding idiom: the loose binding is a witness for the modified
			// clicks and is invisible on the plain one.
			Expect(after.Log).To(HaveSuffix(
				eventModClickAny+" "+eventModClickAny+" "+eventModClickPlain),
				"a plain click did not match the NoModifiers click binding, so the option is a "+
					"no-op on a click rather than a filter")
			Expect(after.Events).To(Equal(before.Events+3),
				"exactly one arrival per click, three clicks: log %q", after.Log)

			// The sentinel, on a different element and a different DOM event:
			// nothing was raised in between, so the counts above are the whole
			// of what those three clicks produced.
			tick()
			waitEvents(before.Events + 4)
			Expect(read().Log).To(HaveSuffix(eventModTick))

			AddReportEntry("FR54-8 MouseEvent modifiers", fmt.Sprintf(
				"browser %s: Ctrl+click and Shift+click on a NoModifiers click binding fell through "+
					"to %s; the plain click matched %s. The binding names no key at all",
				c.version, eventModClickAny, eventModClickPlain))
		})
	})
