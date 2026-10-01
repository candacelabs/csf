package conformance_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
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
// The reflected-attribute property — QA-1's checkpoint-2 §6 observation, ruled
// open by PM-1 at the checkpoint-2 gate §5.4 and carried to checkpoint 3.
//
// Run:
//
//	docker run --rm -e CHROME_BIN=/usr/bin/chromium -v "$PWD:/w" -w /w/gotth-live \
//	    dis-gotth-live-bench:latest \
//	    bash -c 'go test -v ./test/internal/conformance/ -count=1 -args -ginkgo.label-filter=browser'
//
// # What the property is, and why D-15 was a symptom of it
//
// Morph's controlled/uncontrolled rule reads attribute presence on the LIVE
// node as "what the server last declared": a value attribute means the server
// owns the value, its absence means the user does. That reading has a premise,
// and the premise is never stated where the rule is applied — that the user
// cannot write the attribute. It holds for checkedness, for selectedness and
// for an input's value, which are separate pieces of state the attribute only
// seeds. It does NOT hold for `details.open`, which is a plain reflected IDL
// attribute: opening a disclosure writes open="" into the DOM, so from then on
// the attribute has two authors and one bit and the server's word is not
// recoverable from it. That is the whole of D-15.
//
// D-15 was fixed for <details> alone. The general shape — any attribute a
// browser writes from user state — was checked nowhere, and <dialog open> and
// any custom element reflecting internal state are the same shape. So the
// property here is stated over the SET rather than over one tag, in three
// parts:
//
//	§1 the census. For every attribute the rule reads, user interaction must
//	   not write it; the runtime is unsound for it the day one does. This is
//	   the assertion that would have caught D-15 before a user did, and it is
//	   the one no suite had.
//	§2 the consequence. For every attribute that IS written by user
//	   interaction, either the runtime keeps the server's word outside the DOM
//	   (today: <details>) or it does not and the user's bit is reverted by the
//	   next patch (today: <dialog>, custom elements). Both are asserted, so the
//	   boundary is a measured line rather than a claim.
//	§3 the remedy. Where the runtime does not keep the server's word,
//	   data-gotth-preserve must be a working escape hatch — otherwise the
//	   documented limitation has no answer.
//
// # Why a browser and not client/test/dom.mjs
//
// Reflection is exactly what a shim does not model. dom.mjs models
// `details.open` as reflected because D-15 taught it to, so a property test
// against it would be asserting the shim's author's beliefs about the platform
// — which is the belief that was wrong in the first place. §1 in particular is
// a claim about what a real browser writes and is unfalsifiable anywhere else.
//
// # Why the unwired rows assert the gap rather than the fix
//
// Asserting that <dialog open> survives a patch would put a red spec in the
// suite for a limitation the project has decided to carry: FR-25 names
// <details> and every wired entry costs NFR-2 bytes. So the unwired rows
// assert what the runtime does today, and their failure messages say what to
// do when they go red — which they will, and should, on the day the wired set
// grows. That is the same instrument D-16's spec uses, and it is a boundary
// nobody can move silently.
// ---------------------------------------------------------------------------

const fragReflect = "ra.panel"

const eventReflectTick = "ra.tick"

type reflectState struct{ Tick int }

// reflectPanelHTML is the fixture.
//
// Everything around the subjects changes on every tick — the heading, a growing
// run of pips, and the body text inside each disclosure — so a patch is a real
// traversal past every one of them rather than a no-op the rule is never asked
// about. The subjects themselves are rendered identically at every tick, with
// one exception (#rdecl), because a server that never mentions an attribute is
// the case the rule is about: silence, not disagreement.
func reflectPanelHTML(s reflectState) string {
	tick := strconv.Itoa(s.Tick)

	var b strings.Builder
	b.WriteString(`<section` + attrsOf(live.Region(fragReflect)) + `>`)
	b.WriteString(`<p id="rline">tick ` + tick + `</p>`)
	for i := 0; i < s.Tick; i++ {
		b.WriteString(`<span class="pip">.</span>`)
	}

	// The census subjects the morph rule reads. Every one of them is rendered
	// WITHOUT the attribute the rule looks for, which is what makes the user's
	// state the only author there is.
	b.WriteString(`<input id="rbox" type="checkbox">`)
	b.WriteString(`<input id="rtext" type="text" data-tick="` + tick + `">`)
	b.WriteString(`<textarea id="rarea"></textarea>`)
	b.WriteString(`<select id="rsel"><option id="ropta" value="a">a</option><option id="roptb" value="b">b</option></select>`)

	// The reflecting set: one wired, two not.
	b.WriteString(`<details id="rdet"><summary>more</summary><p>body ` + tick + `</p></details>`)
	b.WriteString(`<dialog id="rdlg"><form method="dialog"><button id="rdlgclose" type="submit">close</button></form>` +
		`<p>dialog ` + tick + `</p></dialog>`)
	b.WriteString(`<x-toggle id="rtog">toggle ` + tick + `</x-toggle>`)

	// §3's remedy, the same two elements behind the opt-out. The subtree is
	// never morphed, so nothing inside it carries the tick.
	b.WriteString(`<div id="rkeep"` + attrsOf(live.Preserve()) + `>`)
	b.WriteString(`<dialog id="rdlgkept"><p>kept</p></dialog>`)
	b.WriteString(`<x-toggle id="rtogkept">kept</x-toggle>`)
	b.WriteString(`</div>`)

	// The server's half of the wired rule. open= on odd ticks and on no other,
	// so one spec watches a declaration arrive and then be withdrawn over a
	// disclosure the user opened first. Without it, "keep the user's bit" would
	// be satisfied by a runtime that never wrote <details open> at all.
	//
	// Parity rather than one nominated tick, because the specs here share one
	// page and one session: an absolute tick is reachable exactly once, and a
	// spec that waits for it after some other spec has passed it waits for
	// ever.
	declared := ""
	if s.Tick%2 == 1 {
		declared = ` open`
	}
	b.WriteString(`<details id="rdecl"` + declared + `><summary>more</summary><p>decl ` + tick + `</p></details>`)

	b.WriteString(`<button id="rtick" type="button"` + attrsOf(live.On("click", eventReflectTick)) + `>tick</button>`)
	b.WriteString(`</section>`)
	return b.String()
}

func reflectConfig() live.Config[reflectState, qaUser] {
	return live.Config[reflectState, qaUser]{
		Init: func(ctx context.Context, session live.Session[qaUser]) (reflectState, []live.Effect[qaUser], error) {
			return reflectState{}, nil, nil
		},
		Reduce: func(s reflectState, ev live.Event) (reflectState, []live.Effect[qaUser]) {
			if ev.Name == eventReflectTick {
				s.Tick++
			}
			return s, nil
		},
		Fragments: []live.Fragment[reflectState]{{
			ID:     fragReflect,
			Render: func(s reflectState) templ.Component { return raw(reflectPanelHTML(s)) },
			Dirty:  func(prev, next reflectState) bool { return prev != next },
		}},
		Events:       []string{eventReflectTick},
		Authenticate: func(request *http.Request) (qaUser, error) { return qaUser("qa"), nil },
		Authorize:    live.AllowAll[qaUser],
		CSRF:         live.NoCSRFCheck,
	}
}

func startReflectApp() *httptest.Server {
	GinkgoHelper()
	return serveLive(reflectConfig(), map[string]http.HandlerFunc{
		"/": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprint(w, htmlDoc("QA reflected attributes", scriptTag(), reflectPanelHTML(reflectState{})))
		},
	})
}

// raHelpers is the page side.
//
// x-toggle is defined here, in the page, because that is where a custom element
// lives in a real application: the library knows nothing about it, which is
// precisely why it is the third member of the reflecting set. It reflects on a
// real click, the way a toggle button written to the ARIA pattern does.
const raHelpers = `
class XToggle extends HTMLElement {
  connectedCallback() {
    if (this.__wired) return;
    this.__wired = true;
    this.addEventListener("click", () => { this.pressed = !this.pressed; });
  }
  get pressed() { return this.hasAttribute("pressed"); }
  set pressed(v) { if (v) this.setAttribute("pressed", ""); else this.removeAttribute("pressed"); }
}
customElements.define("x-toggle", XToggle);

window.__ra = {
  // mark tags a live node with an expando the server cannot send, so a spec can
  // tell a node that was morphed from one that was replaced. Both look the same
  // in the markup, and only one of them says anything about the rule.
  mark(sel) {
    const el = document.querySelector(sel);
    if (!el) throw new Error("no element for " + sel);
    el.__raMark = "marked";
    return true;
  },
  markOf(sel) {
    const el = document.querySelector(sel);
    return el && el.__raMark ? el.__raMark : "";
  },
  attr(sel, name) {
    const el = document.querySelector(sel);
    if (!el) throw new Error("no element for " + sel);
    const v = el.getAttribute(name);
    return v === null ? "<absent>" : v;
  },
  // tick clicks the bound button and resolves once the region has been
  // patched, which is the only honest signal that a morph has happened.
  async tick(times) {
    const line = () => document.querySelector("#rline").textContent.trim();
    for (let i = 0; i < (times || 1); i++) {
      const before = line();
      document.querySelector("#rtick").click();
      const deadline = performance.now() + 15000;
      for (;;) {
        if (line() !== before) break;
        if (performance.now() > deadline) throw new Error("no patch arrived: still " + before);
        await new Promise(r => setTimeout(r, 15));
      }
      await new Promise(r => setTimeout(r, 40));  // the default inbound budget is 50 events/s
    }
    return line();
  },
};
`

// absent is what raHelpers.attr returns for an attribute that is not there. A
// sentinel rather than an empty string, because open="" is present and empty
// and the difference is the entire subject of this file.
const absent = "<absent>"

// reflectSubject is one row of the census: an attribute the browser might write
// from user interaction, and what the runtime believes about it.
type reflectSubject struct {
	// what the row is called in the report
	name string
	// the element, and the attribute the morph rule reads or would read
	sel, attr string
	// tag is the tagName the morph rule special-cases for this subject, or ""
	// when the rule does not know about it at all. It is what §1.1 holds the
	// census against.
	tag string
	// arrange runs before the attribute is first read, for a subject whose
	// interaction is a CLOSE. A dialog is the only one: it is opened by script
	// in every application there is, and the browser's own write of the
	// attribute is the close, so the row has to start from open.
	arrange func(c *chrome)
	// interact performs the user's half and returns nothing; it must leave the
	// element in a state a spec can observe.
	interact func(c *chrome)
	// live is a JS expression reading the LIVE state the interaction changed —
	// the property, not the attribute. It is the row's own vacuity check: if
	// this did not move, the interaction did nothing and the attribute reading
	// below means nothing.
	live string
	// reflects is the property under test: does the browser write the content
	// attribute when the user changes the live state?
	reflects bool
}

var _ = Describe("Reflected attributes: two authors and one bit (FR-25, D-15's general shape)",
	Ordered, ContinueOnFailure, Label("browser"), func() {
		var (
			c  *chrome
			ts *httptest.Server
		)

		BeforeAll(func() {
			browserOnly()
			ts = startReflectApp()
			c = launchChrome()
			c.onNewDocument(raHelpers)
			c.navigate(ts.URL + "/")

			Eventually(func() string {
				return c.evalString(`document.documentElement.getAttribute("data-gotth-status") || ""`)
			}, 30*time.Second, 100*time.Millisecond).Should(Equal("live"),
				"the client runtime never reported a live connection")
			Eventually(func() bool {
				return c.evalBool(`!!document.querySelector("#rline")`)
			}, 30*time.Second, 100*time.Millisecond).Should(BeTrue())
		})

		attrOf := func(sel, name string) string {
			GinkgoHelper()
			return c.evalString(fmt.Sprintf(`window.__ra.attr(%q, %q)`, sel, name))
		}

		click := func(sel string) {
			GinkgoHelper()
			Expect(c.evalBool(fmt.Sprintf(
				`(() => { document.querySelector(%q).click(); return true; })()`, sel))).To(BeTrue())
		}

		// The census. Every subject the morph rule reads is here, and so is
		// every subject this file claims is reflected; §1.1 checks the first
		// half of that sentence against the runtime source.
		subjects := []reflectSubject{
			{
				name: "an input's checkedness", sel: "#rbox", attr: "checked", tag: "INPUT",
				interact: func(c *chrome) { click("#rbox") },
				live:     `document.querySelector("#rbox").checked`,
				reflects: false,
			},
			{
				name: "an input's value", sel: "#rtext", attr: "value", tag: "INPUT",
				interact: func(c *chrome) {
					Expect(c.evalBool(`(() => { document.querySelector("#rtext").focus(); return true; })()`)).To(BeTrue())
					c.call(c.sessionID, "Input.insertText", map[string]any{"text": "typed"}, nil)
				},
				live:     `document.querySelector("#rtext").value !== ""`,
				reflects: false,
			},
			{
				name: "a textarea's value", sel: "#rarea", attr: "", tag: "TEXTAREA",
				interact: func(c *chrome) {
					Expect(c.evalBool(`(() => { document.querySelector("#rarea").focus(); return true; })()`)).To(BeTrue())
					c.call(c.sessionID, "Input.insertText", map[string]any{"text": "typed"}, nil)
				},
				live:     `document.querySelector("#rarea").value !== ""`,
				reflects: false,
			},
			{
				name: "an option's selectedness", sel: "#roptb", attr: "selected", tag: "OPTION",
				// Set through the property rather than through a native select
				// popup, which headless Chromium does not open. It is the same
				// second author either way: the question the census asks is
				// whether changing the live state writes the content attribute.
				interact: func(c *chrome) {
					Expect(c.evalBool(
						`(() => { document.querySelector("#roptb").selected = true; return true; })()`)).To(BeTrue())
				},
				live:     `document.querySelector("#roptb").selected`,
				reflects: false,
			},
			{
				name: "a disclosure's open state", sel: "#rdet", attr: "open", tag: "DETAILS",
				interact: func(c *chrome) { click("#rdet summary") },
				live:     `document.querySelector("#rdet").open`,
				reflects: true,
			},
			{
				name: "a dialog's open state", sel: "#rdlg", attr: "open", tag: "",
				// Opened by the page, as every dialog is, and CLOSED by the
				// user through the dialog's own form. That close is the
				// browser's own write of the attribute, with no script in it,
				// which is why the row is arranged this way round.
				arrange: func(c *chrome) {
					Expect(c.evalBool(
						`(() => { document.querySelector("#rdlg").show(); return true; })()`)).To(BeTrue())
				},
				interact: func(c *chrome) { click("#rdlgclose") },
				live:     `document.querySelector("#rdlg").open === false`,
				reflects: true,
			},
			{
				name: "a custom element reflecting internal state", sel: "#rtog", attr: "pressed", tag: "",
				interact: func(c *chrome) { click("#rtog") },
				live:     `document.querySelector("#rtog").pressed`,
				reflects: true,
			},
		}

		// -------------------------------------------------------------------
		// §1 — the census
		// -------------------------------------------------------------------

		for _, s := range subjects {
			It(fmt.Sprintf("records whether the browser writes the attribute behind %s", s.name), func() {
				// A textarea's declared value is its child text rather than an
				// attribute, so that row reads the child text.
				readAttr := func() string {
					if s.attr == "" {
						return c.evalString(`(() => {
							const el = document.querySelector("#rarea");
							return el.firstChild ? el.firstChild.nodeValue : "` + absent + `";
						})()`)
					}
					return attrOf(s.sel, s.attr)
				}

				if s.arrange != nil {
					s.arrange(c)
				}

				before := readAttr()
				Expect(c.evalBool(s.live)).To(BeFalse(),
					"the subject already held the state this row is about to establish, so nothing is measured")

				s.interact(c)

				Expect(c.evalBool(s.live)).To(BeTrue(),
					"the interaction changed nothing, so what the attribute did afterwards says nothing")
				after := readAttr()

				if s.reflects {
					Expect(after).NotTo(Equal(before), fmt.Sprintf(
						"%s is documented as REFLECTED and the browser did not write %q. If a browser has "+
							"stopped reflecting it, the runtime is carrying a `declared` record and an "+
							"attribute exclusion for a bit that no longer has two authors", s.name, s.attr))
				} else {
					Expect(after).To(Equal(before), fmt.Sprintf(
						"%s WROTE its content attribute, and morph's controlled/uncontrolled rule reads "+
							"that attribute on the live node as the server's last word. The rule is "+
							"unsound for this subject exactly as it was for <details> before D-15: the "+
							"user's own state is now indistinguishable from a server declaration, and the "+
							"next patch will revert it", s.name))
				}

				AddReportEntry("reflection census", fmt.Sprintf(
					"browser %s: %s — attribute %q %s after the user's interaction (%q -> %q)",
					c.version, s.name, s.attr,
					map[bool]string{true: "CHANGED", false: "unchanged"}[before != after], before, after))
			})
		}

		// §1.1. The census is only exhaustive if it covers every subject the
		// rule actually reads, and the rule is in a JavaScript file no Go type
		// can see. So the tags syncProps branches on are read out of the
		// runtime source and held against the census: a fifth branch added
		// there without a row here fails, which is the "silent growth" failure
		// the size ledger's region check exists for, applied to correctness
		// instead of to bytes.
		It("covers every element the morph rule special-cases", func() {
			src, err := os.ReadFile("../../../client/runtime.js")
			Expect(err).NotTo(HaveOccurred())

			found := map[string]bool{}
			for _, m := range regexp.MustCompile(`t === "([A-Z-]+)"`).FindAllStringSubmatch(string(src), -1) {
				found[m[1]] = true
			}
			// A refactor that spells the comparison differently — a switch, a
			// lookup table — would empty this set and quietly pass, so the
			// count is asserted before the contents.
			Expect(found).To(HaveLen(4),
				"the tags morph special-cases are no longer spelled `t === \"TAG\"` in client/runtime.js, "+
					"so this check found nothing and would have passed forever. Re-derive it against the "+
					"new spelling rather than deleting it")

			covered := map[string]bool{}
			for _, s := range subjects {
				if s.tag != "" {
					covered[s.tag] = true
				}
			}
			Expect(sortedTags(found)).To(Equal(sortedTags(covered)),
				"every element whose attributes the morph rule reads needs a census row saying whether a "+
					"browser writes them, because that premise is what the rule rests on")
		})

		// -------------------------------------------------------------------
		// §2 — the consequence, for the reflecting set
		// -------------------------------------------------------------------

		// The property, per reflecting subject: the server has never mentioned
		// this attribute, the user has changed it, and a patch arrives that
		// does not mention it either. The user's bit must survive — and where
		// it does not, the boundary is recorded rather than assumed.
		for _, s := range subjects {
			if !s.reflects {
				continue
			}
			// Only <details> is wired into the runtime's `declared` record.
			wired := s.tag == "DETAILS"

			It(fmt.Sprintf("says what a patch does to %s the server never mentioned", s.name), func() {
				Expect(c.evalBool(fmt.Sprintf(`window.__ra.mark(%q)`, s.sel))).To(BeTrue())

				// Establish the user's state. The dialog row's census
				// interaction ends CLOSED, so each row states its own "the
				// user's bit is on" rather than reusing the census's.
				set := map[string]string{
					"#rdet": `document.querySelector("#rdet").open = true`,
					"#rdlg": `document.querySelector("#rdlg").show()`,
					"#rtog": `document.querySelector("#rtog").pressed = true`,
				}[s.sel]
				Expect(c.evalBool(`(() => { ` + set + `; return true; })()`)).To(BeTrue())
				Expect(attrOf(s.sel, s.attr)).NotTo(Equal(absent),
					"the user's own state did not reach the attribute, so this spec is about nothing")

				var tick string
				c.evalJSON(`window.__ra.tick(2)`, &tick)

				Expect(c.evalString(fmt.Sprintf(`window.__ra.markOf(%q)`, s.sel))).To(Equal("marked"),
					"the element was REPLACED rather than morphed, so nothing here is about the rule")

				held := attrOf(s.sel, s.attr) != absent
				if wired {
					Expect(held).To(BeTrue(), fmt.Sprintf(
						"a patch that never mentioned %q reverted the user's own state. %s reflects, so "+
							"the live attribute has two authors and the server's word cannot be read back "+
							"off it — it has to be kept where the user cannot write it (see `declared` in "+
							"client/runtime.js). This is D-15, again", s.attr, s.name))
				} else {
					Expect(held).To(BeFalse(), fmt.Sprintf(
						"%s survived a patch, and the runtime does not keep a `declared` record for it. "+
							"If that is because the wired set has grown, this is the fix landing and not a "+
							"regression: move this subject's `wired` to true, add its tag to syncProps' "+
							"census row, and update the note above `declared` in client/runtime.js and "+
							"FR-25, which today names <details> and nothing else", s.name))
				}

				AddReportEntry("reflected attribute under a silent patch", fmt.Sprintf(
					"%s: wired=%v, the user's bit %s two patches that never mentioned %q",
					s.name, wired, map[bool]string{true: "survived", false: "was reverted by"}[held], s.attr))
			})
		}

		// The wired half's other side, and the reason the fix for D-15 is not
		// "stop writing <details open>". A server that CHANGES its declaration
		// is still authoritative, which is the only way a server can close a
		// disclosure it opened.
		It("still follows the server when the server changes its mind", func() {
			var got struct {
				Mark     string `json:"mark"`
				Declared bool   `json:"declared"`
				DeclAttr bool   `json:"declAttr"`
				Withdrew bool   `json:"withdrew"`
				At       string `json:"at"`
			}
			// #rdecl carries open= on odd ticks and on no other, so the server
			// declares and withdraws once per pair whatever the current tick
			// is. Earlier specs in this Ordered container have already driven
			// it, and a spec that waited for one absolute tick would wait for
			// ever — as the first draft of this one did, ticking two thousand
			// times into a CDP timeout.
			c.evalJSON(`(async () => {
				const at = () => +document.querySelector("#rline").textContent.trim().split(" ")[1];
				window.__ra.mark("#rdecl");
				document.querySelector("#rdecl").open = true;   // the user opens it first

				await window.__ra.tick(at() % 2 === 0 ? 1 : 2); // land on a tick the server declares
				const el = () => document.querySelector("#rdecl");
				const declared = el().open, declAttr = el().hasAttribute("open");

				await window.__ra.tick(1);                      // and on one where it does not
				return {mark: window.__ra.markOf("#rdecl"), declared: declared, declAttr: declAttr,
				        withdrew: el().open === false, at: document.querySelector("#rline").textContent.trim()};
			})()`, &got)

			Expect(got.Mark).To(Equal("marked"),
				"the element was REPLACED rather than morphed, so nothing here is about the rule")
			Expect(got.Declared).To(BeTrue(),
				"the server rendered <details open> and the disclosure is not open")
			Expect(got.DeclAttr).To(BeTrue(),
				"the open property followed the server and the attribute it reflects to did not, "+
					"which means syncProps and syncAttrs disagree about who owns this bit")
			Expect(got.Withdrew).To(BeTrue(),
				"the server withdrew its open declaration and morph did not follow it. Keeping the "+
					"server's word must preserve the user's bit against SILENCE, not against the server — "+
					"a record that also outranks a changed declaration is a second defect, not a fix")
		})

		// -------------------------------------------------------------------
		// §3 — the remedy for the set that is not wired
		// -------------------------------------------------------------------

		// A limitation with no answer is a defect. FR-27's opt-out is the
		// answer, and it is asserted rather than assumed: the same two elements
		// that lose the user's bit above keep it behind data-gotth-preserve.
		It("keeps an unwired reflected attribute behind data-gotth-preserve (FR-27)", func() {
			var got struct {
				Mark   string `json:"mark"`
				Dialog bool   `json:"dialog"`
				Toggle bool   `json:"toggle"`
				Tick   string `json:"tick"`
			}
			c.evalJSON(`(async () => {
				window.__ra.mark("#rdlgkept");
				document.querySelector("#rdlgkept").show();
				document.querySelector("#rtogkept").pressed = true;
				const tick = await window.__ra.tick(2);
				return {
					mark: window.__ra.markOf("#rdlgkept"),
					dialog: document.querySelector("#rdlgkept").hasAttribute("open"),
					toggle: document.querySelector("#rtogkept").hasAttribute("pressed"),
					tick: tick,
				};
			})()`, &got)

			Expect(got.Mark).To(Equal("marked"))
			Expect(got.Dialog).To(BeTrue(),
				"a <dialog open> inside data-gotth-preserve was closed by a patch, so the documented "+
					"escape hatch for the unwired reflecting set does not work and the limitation has no answer")
			Expect(got.Toggle).To(BeTrue(),
				"a custom element's reflected state inside data-gotth-preserve did not survive a patch")

			AddReportEntry("the remedy", fmt.Sprintf(
				"browser %s: <dialog open> and a reflecting custom element both survive two patches "+
					"under data-gotth-preserve (%s)", c.version, got.Tick))
		})
	})

// sortedTags returns a set's members in order, for comparison in a failure message
// that reads.
func sortedTags(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
