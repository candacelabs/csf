package conformance_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/live"
)

// ---------------------------------------------------------------------------
// FR-54 and examples/chat FRICTION.md F-3 — a keyboard binding names its key,
// asserted against a real browser's own KeyboardEvent.
//
// Run:
//
//	docker run --rm -e CHROME_BIN=/usr/bin/chromium -v "$PWD:/w" -w /w/gotth-live \
//	    dis-gotth-live-bench:latest \
//	    bash -c 'go test ./test/internal/conformance/ -count=1 -args -ginkgo.label-filter=browser'
//
// # Why a browser and not the node suite
//
// The property is what `e.key` IS for a given physical key press, and only a
// browser knows. A node spec would build the event object itself, so it would
// assert that the runtime reads the field the spec had just written — the key
// filter would pass against a runtime that compared any field at all. Here the
// press goes in through Chromium's own input pipeline, is routed by real focus,
// and the runtime reads the value the browser produced.
//
// The events are counted SERVER-side and rendered back, so "this key raised no
// event" is asserted against the server's own arrival count rather than against
// the absence of a DOM change. F-3's defect is a frame per keystroke, and a
// frame nobody counts is exactly the thing that goes unnoticed.
//
// # Why the fixture calls the helpers instead of writing the attributes out
//
// dom_preservation_test.go writes data-gotth-* literals and joins them back to
// live.Region/On/Preserve with a separate spec, because its markup varies per
// case. Nothing here varies, so the markup is rendered FROM live.OnAll and
// live.OnWith: the binding a consumer would write is the binding the browser
// receives, and a change to either side of the contract fails here rather than
// producing a page that quietly binds nothing.
// ---------------------------------------------------------------------------

const fragKeys = "kb.panel"

const (
	eventInc   = "kb.increment"
	eventDec   = "kb.decrement"
	eventDraft = "kb.draft"
	eventClear = "kb.clear"
	eventEnter = "kb.enter"
	eventAny   = "kb.any"
	eventNever = "kb.never"
)

// keyState counts every event that reaches the reducer and records the names in
// arrival order, so a spec can assert both that the right event was raised and
// that nothing else was.
type keyState struct {
	Count  int
	Draft  string
	Events int
	Log    string
}

// attrsOf renders a templ.Attributes map into markup, sorted, so the fixture is
// byte-stable across renders and morph is never handed a reordered attribute
// list to churn on.
func attrsOf(a templ.Attributes) string {
	names := make([]string, 0, len(a))
	for name := range a {
		names = append(names, name)
	}
	sort.Strings(names)

	var b strings.Builder
	for _, name := range names {
		switch v := a[name].(type) {
		case string:
			b.WriteString(` ` + name + `="` + templ.EscapeString(v) + `"`)
		case bool:
			if v {
				b.WriteString(` ` + name)
			}
		default:
			b.WriteString(fmt.Sprintf(` %s="%v"`, name, v))
		}
	}
	return b.String()
}

// keyPanelHTML is the fixture. Four bound elements, each one a case:
//
//   - #counter carries TWO key-filtered bindings for the same DOM event on ONE
//     element, which is the shape the benchmark equivalence spec's F-CTR-6
//     needs ("+/- on the focused counter apply +1/−1") and the shape a
//     per-element key attribute cannot express at all.
//   - #draft is the chat composer: bound for input and for two keys at once,
//     which is the case a per-element key attribute would break outright by
//     filtering the input binding with a key an input event does not carry.
//   - #any is an unfiltered keydown binding, which must go on meaning what it
//     has always meant — every key — or every binding written before this
//     option existed changes behaviour.
//   - #never carries a key filter on a click, an event with no key at all.
func keyPanelHTML(s keyState) string {
	var b strings.Builder
	b.WriteString(`<section` + attrsOf(live.Region(fragKeys)) + `>`)
	b.WriteString(`<p id="count">` + strconv.Itoa(s.Count) + `</p>`)
	b.WriteString(`<p id="events">` + strconv.Itoa(s.Events) + `</p>`)
	b.WriteString(`<p id="log">` + templ.EscapeString(s.Log) + `</p>`)
	b.WriteString(`<p id="draftline">` + templ.EscapeString(s.Draft) + `</p>`)

	b.WriteString(`<div id="counter" tabindex="0"` + attrsOf(live.OnAll(
		live.OnWith("keydown", eventInc, live.Bind{Keys: []string{"+", "="}}),
		live.OnWith("keydown", eventDec, live.Bind{Keys: []string{"-"}}),
	)) + `>counter</div>`)

	// Rendered with no text content, so the server never declares the
	// textarea's value and what the user typed is the user's (FR-25). The
	// server's own copy of the draft is #draftline, which is what the clear is
	// asserted against — this file tests the binding, not the morph rule.
	b.WriteString(`<textarea id="draft" name="draft"` + attrsOf(live.OnAll(
		live.OnWith("input", eventDraft, live.Bind{}),
		live.OnWith("keydown", eventClear, live.Bind{Keys: []string{"Escape"}}),
		live.OnWith("keydown", eventEnter, live.Bind{Keys: []string{"Enter"}}),
	)) + `></textarea>`)

	b.WriteString(`<div id="any" tabindex="0"` + attrsOf(live.On("keydown", eventAny)) + `>any</div>`)

	b.WriteString(`<button id="never" type="button"` + attrsOf(
		live.OnWith("click", eventNever, live.Bind{Keys: []string{"Escape"}})) + `>never</button>`)

	b.WriteString(`</section>`)
	return b.String()
}

func keyConfig() live.Config[keyState, qaUser] {
	return live.Config[keyState, qaUser]{
		Init: func(ctx context.Context, session live.Session[qaUser]) (keyState, []live.Effect[qaUser], error) {
			return keyState{}, nil, nil
		},
		// Every accepted event moves Events and Log, so every one of them
		// produces a patch. A spec can therefore wait for the arrival of an
		// event that changes nothing else, which is what makes "no event was
		// raised" assertable rather than merely unobserved.
		Reduce: func(s keyState, ev live.Event) (keyState, []live.Effect[qaUser]) {
			s.Events++
			s.Log = strings.TrimSpace(s.Log + " " + ev.Name)
			switch ev.Name {
			case eventInc:
				s.Count++
			case eventDec:
				s.Count--
			case eventDraft:
				s.Draft = ev.Fields.Get("draft")
			case eventClear:
				s.Draft = ""
			}
			return s, nil
		},
		Fragments: []live.Fragment[keyState]{{
			ID:     fragKeys,
			Render: func(s keyState) templ.Component { return raw(keyPanelHTML(s)) },
			Dirty:  func(prev, next keyState) bool { return prev != next },
		}},
		Events:       []string{eventInc, eventDec, eventDraft, eventClear, eventEnter, eventAny, eventNever},
		Authenticate: func(request *http.Request) (qaUser, error) { return qaUser("qa"), nil },
		Authorize:    live.AllowAll[qaUser],
		CSRF:         live.NoCSRFCheck,
	}
}

func startKeyApp() *httptest.Server {
	GinkgoHelper()
	return serveLive(keyConfig(), map[string]http.HandlerFunc{
		"/": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprint(w, htmlDoc("QA key bindings", scriptTag(), keyPanelHTML(keyState{})))
		},
	})
}

// kbHelpers is the page side. It reads the server's rendered state back as one
// object so that a spec's assertions are all taken from the same instant, and
// it never sleeps: every wait is a poll against a value the server produced.
const kbHelpers = `
window.__kb = {
  text(sel) { return document.querySelector(sel).textContent.trim(); },
  focus(sel) {
    const el = document.querySelector(sel);
    el.focus();
    if (document.activeElement !== el) throw new Error(sel + " did not take focus");
    return true;
  },
  read() {
    return {
      count: +this.text("#count"),
      events: +this.text("#events"),
      log: this.text("#log"),
      draft: this.text("#draftline"),
      value: document.querySelector("#draft").value,
    };
  },
  async waitEvents(n, ms) {
    const deadline = performance.now() + (ms || 15000);
    for (;;) {
      const seen = +this.text("#events");
      if (seen >= n) return seen;
      if (performance.now() > deadline) {
        throw new Error("only " + seen + " events reached the server, wanted " + n +
                        "; log=" + JSON.stringify(this.text("#log")));
      }
      await new Promise(r => setTimeout(r, 15));
    }
  },
};
`

// press dispatches one real key press through the browser's input pipeline, to
// whatever has focus.
//
// Both halves are sent. A keyDown alone leaves the browser believing the key is
// held, and the next press of the same key would arrive as an auto-repeat.
func (c *chrome) press(key, code, text string) {
	GinkgoHelper()
	down := map[string]any{"type": "keyDown", "key": key, "code": code}
	if text != "" {
		// Chromium raises keypress, beforeinput and input for a keyDown that
		// carries text, which is how a press types into a control instead of
		// only being seen by a listener.
		down["text"] = text
	}
	c.call(c.sessionID, "Input.dispatchKeyEvent", down, nil)
	c.call(c.sessionID, "Input.dispatchKeyEvent",
		map[string]any{"type": "keyUp", "key": key, "code": code}, nil)
}

type kbRead struct {
	Count  int    `json:"count"`
	Events int    `json:"events"`
	Log    string `json:"log"`
	Draft  string `json:"draft"`
	Value  string `json:"value"`
}

var _ = Describe("A keyboard binding names its key (FR-54, FRICTION F-3)", Ordered, ContinueOnFailure, Label("browser"), func() {
	var (
		c  *chrome
		ts *httptest.Server
	)

	BeforeAll(func() {
		browserOnly()
		ts = startKeyApp()
		c = launchChrome()
		c.onNewDocument(kbHelpers)
		c.navigate(ts.URL + "/")

		Eventually(func() string {
			return c.evalString(`document.documentElement.getAttribute("data-gotth-status") || ""`)
		}, 30*time.Second, 100*time.Millisecond).Should(Equal("live"),
			"the client runtime never reported a live connection")
	})

	// read takes the server's rendered state in one evaluation.
	read := func() kbRead {
		GinkgoHelper()
		var got kbRead
		c.evalJSON(`window.__kb.read()`, &got)
		return got
	}

	// waitEvents blocks until the server has accepted at least n events.
	waitEvents := func(n int) {
		GinkgoHelper()
		var seen int
		c.evalJSON(fmt.Sprintf(`window.__kb.waitEvents(%d)`, n), &seen)
	}

	focus := func(sel string) {
		GinkgoHelper()
		Expect(c.evalBool(`window.__kb.focus(` + strconv.Quote(sel) + `)`)).To(BeTrue())
	}

	// This is F-3 itself. Before the filter, every one of the four keys below
	// raised an event on a keydown binding — a frame per keystroke, and a
	// message sent the first time somebody moved the caret.
	//
	// "Nothing was raised" is asserted against the server's arrival counter,
	// and the sentinel press afterwards is what makes the absence provable
	// rather than merely un-awaited: frames on one connection are ordered, so
	// if the sentinel's event has arrived and the counter moved by exactly one,
	// the four before it raised nothing.
	It("raises nothing for the keys it does not name, and the sentinel proves it", func() {
		before := read()
		focus("#counter")

		for _, k := range [][3]string{
			{"Tab", "Tab", ""},
			{"Shift", "ShiftLeft", ""},
			{"ArrowLeft", "ArrowLeft", ""},
			{"a", "KeyA", "a"},
		} {
			focus("#counter") // Tab moves focus; every press starts from the same place
			c.press(k[0], k[1], k[2])
		}

		focus("#counter")
		c.press("+", "Equal", "+")
		waitEvents(before.Events + 1)

		after := read()
		Expect(after.Events).To(Equal(before.Events+1),
			"a key the binding does not name raised an event: the log is %q", after.Log)
		Expect(after.Log).To(Equal(strings.TrimSpace(before.Log+" "+eventInc)),
			"the only event this element may raise for these five presses is the one bound to +")
		Expect(after.Count).To(Equal(before.Count + 1))

		AddReportEntry("F-3 key filter", fmt.Sprintf(
			"browser %s: Tab, Shift, ArrowLeft and \"a\" raised nothing on a keydown binding "+
				"filtered to + and =; the following + raised %s", c.version, eventInc))
	})

	// F-CTR-6, and the reason the filter is part of the binding rather than an
	// attribute beside it. Two keys, two DIFFERENT events, one focused element:
	// a per-element key list can only say which keys pass, never which event
	// each one raises.
	It("routes two keys on one focused element to two different events (F-CTR-6)", func() {
		before := read()
		focus("#counter")

		c.press("+", "Equal", "+")
		waitEvents(before.Events + 1)
		c.press("-", "Minus", "-")
		waitEvents(before.Events + 2)

		after := read()
		Expect(after.Count).To(Equal(before.Count),
			"+ then - on one element must apply +1 and then −1")
		Expect(after.Log).To(HaveSuffix(eventInc + " " + eventDec))
		Expect(after.Events).To(Equal(before.Events + 2))
	})

	// A key list is several bindings, one per key, so this asserts the second
	// entry of a list is reachable and not only the first.
	It("matches every key in a list, not only the first", func() {
		before := read()
		focus("#counter")

		c.press("=", "Equal", "=")
		waitEvents(before.Events + 1)

		after := read()
		Expect(after.Count).To(Equal(before.Count+1),
			`"=" is the second entry of the increment binding's key list and it did not match`)
		Expect(after.Log).To(HaveSuffix(eventInc))
	})

	// Backward compatibility, stated as a test because it is a promise: a
	// keydown binding with no Keys means what it has always meant. It is also
	// the control for the spec above — the same four keys that raise nothing
	// through a filter raise an event each through no filter, so the first spec
	// is measuring the filter and not a browser that swallows key events.
	It("still raises an event for every key when the binding names none", func() {
		before := read()

		for _, k := range [][3]string{
			{"Shift", "ShiftLeft", ""},
			{"ArrowLeft", "ArrowLeft", ""},
			{"a", "KeyA", "a"},
		} {
			focus("#any")
			c.press(k[0], k[1], k[2])
		}
		waitEvents(before.Events + 3)

		after := read()
		Expect(after.Events).To(Equal(before.Events+3),
			"an unfiltered keydown binding must raise one event per key, including a modifier pressed alone")
		Expect(after.Log).To(HaveSuffix(strings.Join([]string{eventAny, eventAny, eventAny}, " ")))
	})

	// The composer case, which is F-3's own example: one element bound for
	// input AND for a key. It is here because it is what a per-element key
	// attribute would have broken — the filter would have applied to the input
	// binding too, and an input event carries no key, so the draft would have
	// stopped being sent with no error anywhere.
	It("filters only the binding that asked, on an element bound for input as well", func() {
		before := read()
		focus("#draft")

		c.call(c.sessionID, "Input.insertText", map[string]any{"text": "hello"}, nil)
		waitEvents(before.Events + 1)
		Expect(read().Draft).To(Equal("hello"),
			"the input binding on a key-filtered element stopped sending the draft")

		c.press("Escape", "Escape", "")
		waitEvents(before.Events + 2)

		after := read()
		Expect(after.Draft).To(BeEmpty(), "Escape did not reach the binding that names it")
		Expect(after.Log).To(HaveSuffix(eventDraft + " " + eventClear))
		Expect(after.Events).To(Equal(before.Events + 2))
	})

	// The documented decision about an event with no key, asserted rather than
	// left to be discovered: a filter filters, so a key filter on a click fires
	// never rather than always. The sentinel is again what makes the absence
	// provable.
	It("never fires a key-filtered binding for an event that carries no key", func() {
		before := read()

		Expect(c.evalBool(`(() => { document.querySelector("#never").click(); return true; })()`)).To(BeTrue())

		focus("#counter")
		c.press("+", "Equal", "+")
		waitEvents(before.Events + 1)

		after := read()
		Expect(after.Events).To(Equal(before.Events+1),
			"a click reached a binding whose key filter it cannot possibly satisfy")
		Expect(after.Log).NotTo(ContainSubstring(eventNever))
	})

	// The other half of the modifier decision, and a limitation worth having a
	// spec for rather than a sentence: this runtime calls preventDefault for a
	// recognised submit and an anchor click and for nothing else, so a bound
	// key still does what the browser was going to do with it. Enter raises the
	// event AND inserts the newline.
	//
	// That is why "Enter sends, Shift+Enter newlines" is not expressible with a
	// key filter alone (bench equivalence spec F-CHT-3): the filter chooses
	// which keys raise events, and it does not take a key away from the page.
	//
	// CORRECTED 2026-08-05 — "and for nothing else" was falsified by 0b9e32e7,
	// which added components 7 and 8 of the binding grammar. The paragraph
	// above is kept because it is the record of what this spec could see when
	// it was written; this is what the runtime does now.
	//
	// preventDefault is called in exactly two places, and the ORDER of the two
	// is the whole of the design (docs/reviews/fr-54.md §14, C-9):
	//
	//  1. ABOVE the composition guard, unconditionally, for a recognised
	//     submit and for an anchor click. Unchanged since checkpoint 1 and
	//     unchanged by that landing. It stays above the guard deliberately: a
	//     form must not navigate for real because an IME composition happened
	//     to be active.
	//  2. BELOW the composition guard, for a binding that set component 8 --
	//     live.Bind.PreventDefault, s[7] in the runtime -- and only when THAT
	//     binding is the one that matched. It sits below the guard because
	//     Enter COMMITS the candidate mid-composition, so suppressing it there
	//     would break every CJK composer (FR-26).
	//
	// Folding the two into one call, in either direction, breaks one of those
	// two properties; client/test/binding.test.mjs pins both, and the cheapest
	// spelling anyone measured is a shape that fails the first (client/SIZE.md
	// §1.1.6). So the two calls stay two calls, on opposite sides of the guard.
	//
	// What SURVIVES this correction, and it is most of it: "not expressible
	// with a key filter alone" is still exactly true. It is why F-CHT-3 needed
	// TWO new options rather than none -- a filter chooses which keys raise
	// events and cannot take a key away, so Bind.NoModifiers and
	// Bind.PreventDefault are both required and neither alone would do. The
	// full modifier set is still REFUSED, with a re-open trigger at §13.
	//
	// The spec below is GREEN, correct, correctly named, and untouched. The
	// binding it drives sets neither new option, both default off, so it
	// asserts the promise this file has always asserted: a key a binding named
	// without asking for the default still does what the browser intended.
	// That is C-4's point and it is why C-4 froze this spec; the freeze lifts
	// for comments only (§26) and nothing executable here moves.
	It("does not take the key away from the browser", func() {
		before := read()
		focus("#draft")

		c.press("Enter", "Enter", "\r")
		waitEvents(before.Events + 2) // the keydown binding, and the input the newline caused

		after := read()
		Expect(after.Log).To(ContainSubstring(eventEnter))
		Expect(after.Value).To(ContainSubstring("\n"),
			"a bound key must still do what the browser was going to do with it")

		AddReportEntry("key bindings do not preventDefault", fmt.Sprintf(
			"Enter on a bound textarea raised %s and inserted the newline: value=%q", eventEnter, after.Value))
	})
})
