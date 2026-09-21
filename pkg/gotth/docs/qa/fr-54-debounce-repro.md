# FR-54 failure 2, driven — an element-scoped debounce across a composed pair

*(2026-08-05. Driven by QA-1 at commit **`667d3db72cb3f77a544f9ef2af21ea314b97cb75`**, branch
`dev-/gotth-live-orchestrator-c3efc4`. `docs/gates/phase-4.md` §5.6 routed this: **"an observation is
worth more than a derivation and this project has twice found that the browser is where this class of
defect actually lives. It should be driven before it is fixed."** This is the observation. It is not a
fix, and it changes no API: FR-65 leaves that to L9-1.)*

---

## Verdict: **REPRODUCES**

The claim reproduces exactly as §5.6 states it, in a real browser, against the real shipped runtime
and the real `live` helpers, on the guide's own markup. **A keystroke inside the 150 ms window
cancels the pending clear outright: the `clear` event never reaches the server at all.**

Three things the observation adds that §5.6 does not say, and each of them makes the defect worse
rather than smaller:

1. **The interference is symmetric.** An `Escape` inside the window cancels a pending **draft** —
   the server never learns what was typed, and the browser goes on showing it. Divergence, silently.
2. **The key binding is also *delayed* by an interval it never asked for.** `Escape` alone on the
   composed element is delivered at **~150 ms**, against **~1.3 ms** for the identical binding on an
   element with no debounce. The clear is late even when nothing follows it.
3. **It is the `input` event, not the keystroke, that does the cancelling.** A non-printable key
   inside the window — `ArrowLeft` — does **not** cancel: `dispatch` returns before it reaches the
   timer when no binding matches. So §5.6's *"a keystroke inside that window"* is right for the case
   it names (a printable key in a text input) and slightly over-general as a rule.

---

## 1. The claim, as §5.6 states it

`docs/gates/phase-4.md` §5.6, the failure table, row 2 (line 1522), verbatim:

> **`Fields`/`Debounce`/`Throttle` are element-scoped, so composing two bindings changes what one of
> them does.** In the guide's own composer the `Escape` binding inherits the `input` binding's 150 ms
> debounce, and a keystroke inside that window **cancels the pending clear outright**

with the cited sites: `live/templ.go:154` (attribute emitted only when `Debounce > 0`), `:183`–`:207`
(`OnAll` keeps the first **present** value), `client/runtime.js:648`–`:664` (interval read off the
element, timer keyed by the element, `clearTimeout` on each dispatch), against
`docs/guide/_samples/events/view.templ:31`.

---

## 2. The source, as it reads today

**Every citation still lands.** Checked at `667d3db7`, before anything was driven.

| §5.6 cites | Today | Reads as §5.6 says? |
|---|---|---|
| `live/templ.go:154` | `attrs[attrDebounce] = strconv.FormatInt(b.Debounce.Milliseconds(), 10)`, guarded by `:153` `if b.Debounce > 0 {` | **Yes** |
| `live/templ.go:183`–`:207` | `func OnAll` opens at `:183` and closes at `:206`; the first-present-wins merge is `:191`–`:193` (`if _, seen := out[name]; !seen { out[name] = value }`) | **Yes** (`:207` is the blank line after the closing brace — a range end, not a miss) |
| `client/runtime.js:648`–`:664` | `:648` `d = +el.getAttribute(A_DEBOUNCE) \|\| 0,`; `:650` `st = timers.get(el) \|\| {};`; `:657`–`:663` the `if (d) { clearTimeout(st.t); st.t = setTimeout(…, d); timers.set(el, st); return; }` block. `timers` is declared at `:530` as `new WeakMap()` | **Yes** |
| `docs/guide/_samples/events/view.templ:31` | `{ live.OnAll(` opens at `:31`; `:32` is the `Escape` binding, `:33` the `input` binding with `Debounce: 150 * time.Millisecond` | **Yes** |

Two notes on the surrounding record, neither of which affects the finding:

- **What actually executes is not `client/runtime.js`.** `live/templ.go:56` embeds
  `clientjs/gotth-live.min.js` and `Script` serves those bytes; `client/runtime.js` is its source.
  Before driving anything I ran `tools/minify -check` (it passes: shipped 10391 B, ceiling 12288,
  64.0% headroom), so the minified bytes the browser ran are the current build of the file §5.6
  cites. The minified dispatch carries the same shape, with `M` the element-keyed `WeakMap`:
  `…u=+t.getAttribute(he)||0,f=+t.getAttribute(me)||0,h=M.get(t)||{};…if(u){clearTimeout(h.t),h.t=setTimeout(function(){J(r,s,l)},u),M.set(t,h);return}J(r,s,l)`.
- **A wording nit, reported and not fixed.** §5.6 and `docs/PRD.md:1485` both say *"the godoc calls
  the sharing 'a wart'"*. The godoc (`live/templ.go`, `OnAll`) documents the sharing plainly — *"is an
  attribute the client reads from the ELEMENT and not from the binding that asked for it"* — but the
  word **"wart" is `docs/api-surface.md:654`'s**, not the godoc's. `grep -rn wart` returns three hits
  and none is in `live/`. The substance of §5.6's sentence (documented, but silent about the sample
  printed beneath it) is correct: `docs/guide/events-and-forms.md:48`–`:53` states the rule twenty
  lines above the sample it breaks, and `:291`–`:295` restates it, and neither says what it does to
  that sample. These are PM-1's and DEV-1's files; QA does not edit them.

---

## 3. Method

- **Real browser**: Chromium **151.0.7922.71** (Debian trixie), headless, driven over CDP through
  the suite's existing `cdp_test.go` client, in `dis-gotth-live-bench:latest` (image `023cbb5c9884`),
  Go **1.26.5**.
- **Real everything else**: the fixture markup is rendered *from* `live.OnAll` / `live.OnWith` /
  `live.Region` — not a hand-written `data-gotth-*` string anywhere — served by a real `live.New`
  application over a real WebSocket, with the real embedded runtime. Nothing is mocked.
- **The measurement is server-side arrival, timed client-side.** Every accepted event appends its
  name to a rendered log, so *"this event was never raised"* is asserted against the server's own
  arrival count rather than against the absence of a DOM change. A `MutationObserver` plus a 5 ms
  sampler timestamps each change to that log in page time, and the key presses are marked in the same
  clock immediately before and after dispatch — so every reported *gap* is an **over-estimate** and
  every reported *arrival* includes transport and render.
- **Written narrowly.** The harness lives in a copy of the tree at `/tmp/fr54`, never in the
  worktree. It is Ginkgo v2 + Gomega, in `package conformance_test`, reusing that suite's
  `launchChrome`, `serveLive`, `scriptTag`, `attrsOf` and `press` so that the reproduction is the
  project's own harness pointed at one question. **Nothing was committed but this document.**

### Two deliberate simplifications, and the spec that removes them

The panel fixture puts the bound inputs in a fragment that is **never dirty**, and does not declare
their `value`. That is to remove a confound rather than to flatter the result: the runtime's timer
map is a `WeakMap` keyed by the element, so a morph that *replaced* an input would make "the timer
was cancelled" and "the element the timer was keyed by is gone" indistinguishable.

The guide's real page does neither — its input is in the dirty fragment and its `value` is declared
by the server. So spec 7 (**GUIDE SHAPE**) reproduces the whole sequence on a third element with both
simplifications removed. **Same result.** The fixture is not what produces the finding.

---

## 4. The negative controls

Three, because a check that cannot fail is indistinguishable from one that passes.

1. **Undebounced twin (specs 2 and 8).** An identical `OnAll` composition of the identical two DOM
   events on an identical `<input>`, differing in exactly one thing: no `Debounce`. Same page, same
   session, same key sequence, same sub-4 ms timing. **The clear is delivered**, at 2.2 ms — which is
   also the baseline that makes the composed element's ~155 ms a debounce rather than transport.
2. **Non-printable key (spec 5).** `Escape` then `ArrowLeft` inside the window on the *composed*
   element. **The clear survives**, so the composed case is measuring the shared timer and not "any
   second key press loses the first".
3. **Mutation control.** The claim's assertion was run against a runtime patched to key the timer per
   binding instead of per element — `h["t:"+r]` in place of `h.t`, +15 B minified, +9 B gzipped.
   **Three specs go red**, including the claim's, with the clear arriving at 152.9 ms. The check can
   fail, and it fails for the reason it is written to detect.

---

## 5. Observed result

Run of record: 2026-08-05 16:38 UTC, clean tree, `8 Passed | 0 Failed`. Two earlier runs (16:35,
16:36) agreed within a few ms. All times are milliseconds from the mark taken immediately before the
first press of the sequence; "gap" is the over-estimated interval between the two presses.

| # | Spec | Element | Sequence | Gap | Arrivals (name @ ms) | Server state after |
|---|---|---|---|---:|---|---|
| 1 | markup | composed | — | — | `data-gotth-on="keydown:fr54.clear:Escape;input:fr54.draft"`, **`data-gotth-debounce="150"`**; control has **no** debounce attribute | — |
| 2 | **CONTROL** | undebounced | `Escape`, `x` | 3.0 | `cclear` @ **2.2**, `cdraft` @ **11.7** | `cdraft cclear cdraft`, draft `"hellox"` |
| 3 | **CLAIM** | composed | `Escape`, `x` | 3.1 | `draft` @ **156.2** — **and nothing else, ever** | tail is `fr54.draft` alone; draft `"hellox"`; **`clear` never arrived** |
| 4 | CLAIM, 2nd half | composed | `Escape` alone | — | `clear` @ **158.8** | draft `""` — delivered, but 150 ms late |
| 5 | MECHANISM | composed | `Escape`, `ArrowLeft` | 3.6 | `clear` @ **153.4** | exactly one event; the arrow key cancelled nothing |
| 6 | SYMMETRY | composed | `q`, `Escape` | 1.0 | `clear` @ **154.7** | one event; input value `"helloxq"`, **server never told about the `q`** |
| 7 | GUIDE SHAPE | dirty fragment, server-declared `value` | `Escape`, `x` | 2.6 | `gdraft` @ **153.8** — nothing else | tail is `fr54.gdraft` alone; **`gclear` never arrived** |
| 8 | CONTROL, 2nd half | undebounced | `Escape` alone | — | `cclear` @ **1.3** | delivered promptly |

**Row 3 is the finding.** The `Escape` was pressed, the browser routed it, the runtime matched the
`keydown:fr54.clear:Escape` binding by name, and then read `150` off the element and armed a timer
that the next character's `input` event cleared 3 ms later. One event was sent for the pair, and it
was the draft. There is no error, no console warning, no frame on the wire, and nothing in the
server's log to notice: **the clear is not delayed or reordered, it is gone.**

### Mutation-control detail

Against the per-binding-timer patch, the same eight specs give `5 Passed | 3 Failed`:

| Spec | Clean tree | Per-binding timer |
|---|---|---|
| 3 CLAIM | `fr54.draft` only | **`fr54.clear` @ 152.9, `fr54.draft` @ 157.9** → red |
| 6 SYMMETRY | 1 event | **2 events** → red |
| 7 GUIDE SHAPE | `fr54.gdraft` only | **`fr54.gclear` @ 152.9, `fr54.gdraft` @ 156.6** → red |
| 4 CLAIM, 2nd half | `clear` @ 158.8 | `clear` @ **158.6** → still green |

That last row is the useful one: **a per-binding timer fixes the cancellation and does not fix the
inherited interval.** They are two defects sharing one cause, and a fix that addresses only the
`WeakMap` slot leaves the key binding 150 ms late for a reason its author never wrote down.

---

## 6. What was *not* driven

§5.6's row names **`Fields`/`Debounce`/`Throttle`**. This drove **`Debounce`**.

- **`Throttle`** shares the same element-keyed record (`st.l`, `client/runtime.js:651`–`:655`) and is
  merged by the same first-present-wins rule, so the same class of interference is derivable — and
  **derivable is exactly the status this document was written to end.** Not observed.
- **`Fields`** is element-scoped for a further reason that is not a defect: `fields(el)`
  (`client/runtime.js:561`) reads the element's form or its own `name`, and `A_FIELDS` is a static
  attribute. Whatever is decided about the timer, "static fields belong to the element" is defensible
  on its own terms and should not be swept into the same fix by accident. Not observed.
- **Rate limiting.** `Limits.MaxEventsPerSecond` was not exercised; nothing here approaches it.

---

## 7. Recommendation

**A recommendation only. The API decision is L9-1's under FR-65, and §5.6 is explicit that it did not
choose a shape either.** What the measurement contributes is that the choice is now between two
*named* defects rather than one described one.

**The measurement splits the fault in two.** (a) One **timer** per element loses events, silently,
in both directions. (b) One **interval** per element applies an unrelated binding's latency to a key
binding. The mutation control shows they are independent: fixing (a) leaves (b) standing.

**The shape that already exists in this codebase fixes both.** `Bind.Keys` faced precisely this
argument and won it — `binding()` puts the key filter *inside* the `data-gotth-on` spec, and
`client/runtime.js:588`–`:593` says why in as many words: *"an attribute is read from the ELEMENT and
an element carries several bindings … the draft would silently stop being sent."* That paragraph is a
description of this bug, written about a different option, one release early. Extending the
`domEvent:name:key` grammar with the per-binding numbers and reading `d`/`th` **off the matched spec
instead of off the element** would:

- need **no new `Bind` field** and no new Go surface for a consumer to learn — `OnWith` already takes
  `Debounce` and `Throttle`; only where they are *emitted* changes;
- leave `Fields` element-scoped, which is where it belongs (§6);
- keep `OnAll`'s first-present-wins rule for what remains genuinely shared;
- cost, on the evidence of the mutation, **tens of bytes** in a bundle with **64% headroom**, against
  `docs/api-surface.md:654`'s standing assertion that per-binding options *"cannot be per binding
  without a second timer table in the runtime"* — the measured patch needed no second table, only a
  keyed slot in the entry that already exists. **That row is worth re-examining whichever way the
  decision goes.**

**Two alternatives, recorded as considered.** (i) `OnAll` **refuses** to compose bindings when one
carries `Debounce`/`Throttle` and another does not — cheapest, loud instead of silent, but it deletes
the composer the guide prints and `examples/chat` wants. (ii) **Refuse the fix** under FR-54 clause 3,
with an argument and a re-open trigger. That is a legitimate outcome, but it should now have to state
the measured consequence rather than the documented one: not *"bindings share a debounce timer"* but
**"a keystroke inside the window destroys the other binding's event entirely, in either direction,
with no error anywhere"** — and it needs no adversarial timing to happen, because 150 ms is an
ordinary inter-keystroke interval and a user who presses Escape while typing is inside the window by
construction.

**Whatever lands, the guide is a separate fix.** `docs/guide/events-and-forms.md:48`–`:53` states the
rule and then prints, at `:59`–`:60`, the sample the rule breaks. Even a refusal owes that page a
sentence.

**And a proposal rather than a commit:** the eight specs below are Ginkgo v2 + Gomega in the
conformance suite's own idiom and would sit unmodified at
`test/internal/conformance/fr54_debounce_test.go` as a regression gate once a shape is chosen — three
of them are already known to go red against a changed runtime, which is the property a regression test
is supposed to have. Adding them is DEV-2's or QA-1's call after FR-65, not this document's.

---

## 8. The harness, verbatim

Written to `test/internal/conformance/fr54_debounce_repro_test.go` **inside a copy of the tree**, so
that it compiles as part of `package conformance_test` and can reuse that suite's browser client and
server harness. SHA-256 `d97cdd55adabbcdc0359adfa42c407bb8fc60d689079f0f7a5879122a629e79f`.

### Re-running it

```bash
# From the repository root. The worktree is never written to.
rm -rf /tmp/fr54 && mkdir -p /tmp/fr54
rsync -a --exclude='bench/' --exclude='examples/' gotth-live/ /tmp/fr54/gotth-live/
# then write the file below to
#   /tmp/fr54/gotth-live/test/internal/conformance/fr54_debounce_repro_test.go

mkdir -p /tmp/fr54-gomod /tmp/fr54-gocache
docker run --rm -v /tmp/fr54:/workspace \
  -v /tmp/fr54-gomod:/gomod -v /tmp/fr54-gocache:/gocache \
  -e GOMODCACHE=/gomod -e GOCACHE=/gocache \
  -w /workspace/candace/pkg/gotth dis-gotth-live-bench:latest \
  bash -c 'go test -v ./test/internal/conformance/ -count=1 -timeout 600s \
      -args -ginkgo.v -ginkgo.focus="FR-54 failure 2" -ginkgo.no-color'
```

`bash -c`, never `bash -lc`: the login shell strips the Go toolchain from `PATH` in these images. The
run needs no network beyond the module cache and takes about 10 s.

To reproduce the **mutation control**, copy `/tmp/fr54` to `/tmp/fr54-mut`, and in
`live/clientjs/gotth-live.min.js` replace

```
if(u){clearTimeout(h.t),h.t=setTimeout(function(){J(r,s,l)},u),M.set(t,h);return}
```

with

```
if(u){var K="t:"+r;clearTimeout(h[K]),h[K]=setTimeout(function(){J(r,s,l)},u),M.set(t,h);return}
```

then run the same command against `/tmp/fr54-mut`. Expect `5 Passed | 3 Failed`.

### `fr54_debounce_repro_test.go`

```go
package conformance_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/candace/pkg/gotth/live"
)

// ---------------------------------------------------------------------------
// docs/gates/phase-4.md §5.6 failure 2, DRIVEN.
//
// The claim: Fields/Debounce/Throttle are element-scoped, so composing two
// bindings on one element changes what one of them does. Against
// docs/guide/_samples/events/view.templ:31 — the Escape binding inherits the
// input binding's 150 ms debounce, and a keystroke inside that window cancels
// the pending clear outright.
//
// This file is a QA reproduction, not a fix and not a regression suite. It is
// run out of /tmp against a copy of the tree; nothing here is committed.
//
// Run (from the copy's repository root):
//
//	docker run --rm -u "$(id -u):$(id -g)" -e HOME=/tmp -v "$PWD:/workspace" \
//	    -w /workspace/candace/pkg/gotth dis-gotth-live-bench:latest \
//	    bash -c 'go test ./test/internal/conformance/ -count=1 -v \
//	        -args -ginkgo.v -ginkgo.focus="FR-54 failure 2"'
// ---------------------------------------------------------------------------

const (
	fr54FragPanel = "fr54.panel"
	fr54FragOut   = "fr54.out"

	// The composed pair, exactly the guide's shape: a key binding with no
	// debounce of its own, composed with an input binding carrying 150 ms.
	fr54EventClear = "fr54.clear"
	fr54EventDraft = "fr54.draft"

	// The negative control: the SAME composition, the SAME two DOM events,
	// the same element type and name — with no Debounce anywhere.
	fr54EventCClear = "fr54.cclear"
	fr54EventCDraft = "fr54.cdraft"

	// The guide's markup without the two simplifications the panel above
	// makes: this element lives in the DIRTY fragment and its value is
	// declared by the server, so every arriving event re-renders it.
	fr54EventGClear = "fr54.gclear"
	fr54EventGDraft = "fr54.gdraft"
)

type fr54State struct {
	Events int
	Log    string
	Draft  string
	CDraft string
	GDraft string
}

// fr54PanelHTML is the fixture, and it is a SEPARATE fragment from the output
// so that no arriving event can ever re-render the bound elements. The
// element-keyed timer map in the runtime is a WeakMap; a morph that replaced
// an input would be a confound between "the timer was cancelled" and "the
// element the timer was keyed by is gone". This fragment is never dirty.
func fr54PanelHTML() string {
	var b strings.Builder
	b.WriteString(`<section` + attrsOf(live.Region(fr54FragPanel)) + `>`)

	// docs/guide/_samples/events/view.templ:31-34, verbatim as a composition.
	b.WriteString(`<input id="fr54composed" type="text" name="body"` + attrsOf(live.OnAll(
		live.OnWith("keydown", fr54EventClear, live.Bind{Keys: []string{"Escape"}}),
		live.OnWith("input", fr54EventDraft, live.Bind{Debounce: 150 * time.Millisecond}),
	)) + `/>`)

	// The control. One difference from the element above: no Debounce.
	b.WriteString(`<input id="fr54control" type="text" name="body"` + attrsOf(live.OnAll(
		live.OnWith("keydown", fr54EventCClear, live.Bind{Keys: []string{"Escape"}}),
		live.OnWith("input", fr54EventCDraft, live.Bind{}),
	)) + `/>`)

	b.WriteString(`</section>`)
	return b.String()
}

func fr54OutHTML(s fr54State) string {
	return `<section` + attrsOf(live.Region(fr54FragOut)) + `>` +
		`<p id="fr54events">` + strconv.Itoa(s.Events) + `</p>` +
		`<p id="fr54log">` + templ.EscapeString(s.Log) + `</p>` +
		`<p id="fr54draft">` + templ.EscapeString(s.Draft) + `</p>` +
		`<p id="fr54cdraft">` + templ.EscapeString(s.CDraft) + `</p>` +
		`<p id="fr54gdraft">` + templ.EscapeString(s.GDraft) + `</p>` +
		`<input id="fr54guide" type="text" name="body" value="` + templ.EscapeString(s.GDraft) + `"` +
		attrsOf(live.OnAll(
			live.OnWith("keydown", fr54EventGClear, live.Bind{Keys: []string{"Escape"}}),
			live.OnWith("input", fr54EventGDraft, live.Bind{Debounce: 150 * time.Millisecond}),
		)) + `/>` +
		`</section>`
}

func fr54Config() live.Config[fr54State] {
	return live.Config[fr54State]{
		Init: func(context.Context, live.Session) (fr54State, []live.Effect, error) {
			return fr54State{}, nil, nil
		},
		Reduce: func(s fr54State, ev live.Event) (fr54State, []live.Effect) {
			s.Events++
			s.Log = strings.TrimSpace(s.Log + " " + ev.Name)
			switch ev.Name {
			case fr54EventDraft:
				s.Draft = ev.Fields.Get("body")
			case fr54EventClear:
				s.Draft = ""
			case fr54EventCDraft:
				s.CDraft = ev.Fields.Get("body")
			case fr54EventCClear:
				s.CDraft = ""
			case fr54EventGDraft:
				s.GDraft = ev.Fields.Get("body")
			case fr54EventGClear:
				s.GDraft = ""
			}
			return s, nil
		},
		Fragments: []live.Fragment[fr54State]{
			{
				ID:     fr54FragPanel,
				Render: func(fr54State) templ.Component { return raw(fr54PanelHTML()) },
				Dirty:  func(fr54State, fr54State) bool { return false },
			},
			{
				ID:     fr54FragOut,
				Render: func(s fr54State) templ.Component { return raw(fr54OutHTML(s)) },
				Dirty:  func(prev, next fr54State) bool { return prev != next },
			},
		},
		Events: []string{
			fr54EventClear, fr54EventDraft,
			fr54EventCClear, fr54EventCDraft,
			fr54EventGClear, fr54EventGDraft,
		},
		Authenticate: live.Anonymous,
		Authorize:    live.AllowAll,
		CSRF:         live.NoCSRFCheck,
	}
}

func startFR54App() *httptest.Server {
	GinkgoHelper()
	return serveLive(fr54Config(), map[string]http.HandlerFunc{
		"/": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprint(w, htmlDoc("FR-54 failure 2", scriptTag(),
				fr54PanelHTML()+fr54OutHTML(fr54State{})))
		},
	})
}

// fr54Helpers timestamps every change to the server's rendered log, in page
// time, so "when did the event arrive" is measured rather than assumed. The
// MutationObserver is the precise sampler; the interval is a backstop for a
// change the observer's own batching might coalesce.
const fr54Helpers = `
window.__fr54 = {
  arrivals: [], last: null, t0: 0,
  text(sel) { var el = document.querySelector(sel); return el ? el.textContent.trim() : null; },
  sample() {
    var t = this.text("#fr54log");
    if (t === null) return;
    if (t !== this.last) { this.last = t; this.arrivals.push({ log: t, t: performance.now() }); }
  },
  begin() { this.sample(); this.arrivals = []; this.t0 = performance.now(); return true; },
  mark() { return performance.now() - this.t0; },
  dump() { this.sample(); return { t0: this.t0, arrivals: this.arrivals }; },
  focus(sel) {
    var el = document.querySelector(sel);
    el.focus();
    if (document.activeElement !== el) throw new Error(sel + " did not take focus");
    return true;
  },
  state() {
    return {
      events: +this.text("#fr54events"),
      log: this.text("#fr54log"),
      draft: this.text("#fr54draft"),
      cdraft: this.text("#fr54cdraft"),
      composedValue: document.querySelector("#fr54composed").value,
      controlValue: document.querySelector("#fr54control").value,
      gdraft: this.text("#fr54gdraft"),
      guideValue: document.querySelector("#fr54guide").value,
    };
  },
  async waitEvents(n, ms) {
    var deadline = performance.now() + (ms || 15000);
    for (;;) {
      var seen = +this.text("#fr54events");
      if (seen >= n) return seen;
      if (performance.now() > deadline)
        throw new Error("only " + seen + " events arrived, wanted " + n + "; log=" + JSON.stringify(this.text("#fr54log")));
      await new Promise(function (r) { setTimeout(r, 5); });
    }
  },
  // quiesce resolves once the server's log has been unchanged for ms.
  async quiesce(ms) {
    var seen = this.text("#fr54log"), stable = performance.now();
    for (;;) {
      await new Promise(function (r) { setTimeout(r, 10); });
      var now = this.text("#fr54log");
      if (now !== seen) { seen = now; stable = performance.now(); }
      else if (performance.now() - stable >= ms) return true;
    }
  },
  async sleep(ms) { await new Promise(function (r) { setTimeout(r, ms); }); return true; },
};
(function () {
  function install() {
    try {
      new MutationObserver(function () { window.__fr54.sample(); })
        .observe(document.documentElement, { subtree: true, childList: true, characterData: true });
    } catch (e) {}
    setInterval(function () { window.__fr54.sample(); }, 5);
  }
  if (document.documentElement) install();
  else document.addEventListener("DOMContentLoaded", install);
})();
`

type fr54Read struct {
	Events        int    `json:"events"`
	Log           string `json:"log"`
	Draft         string `json:"draft"`
	CDraft        string `json:"cdraft"`
	ComposedValue string `json:"composedValue"`
	ControlValue  string `json:"controlValue"`
	GDraft        string `json:"gdraft"`
	GuideValue    string `json:"guideValue"`
}

type fr54Arrival struct {
	Log string  `json:"log"`
	T   float64 `json:"t"`
}

type fr54Dump struct {
	T0       float64       `json:"t0"`
	Arrivals []fr54Arrival `json:"arrivals"`
}

func fr54Float(c *chrome, expr string) float64 {
	GinkgoHelper()
	var f float64
	c.evalJSON(expr, &f)
	return f
}

var _ = Describe("FR-54 failure 2 — an element-scoped debounce across a composed pair", Ordered, ContinueOnFailure, Label("browser"), func() {
	var (
		c  *chrome
		ts *httptest.Server
	)

	BeforeAll(func() {
		browserOnly()
		ts = startFR54App()
		c = launchChrome()
		c.onNewDocument(fr54Helpers)
		c.navigate(ts.URL + "/")

		Eventually(func() string {
			return c.evalString(`document.documentElement.getAttribute("data-gotth-status") || ""`)
		}, 30*time.Second, 100*time.Millisecond).Should(Equal("live"))
	})

	read := func() fr54Read {
		GinkgoHelper()
		var got fr54Read
		c.evalJSON(`window.__fr54.state()`, &got)
		return got
	}
	dump := func() fr54Dump {
		GinkgoHelper()
		var got fr54Dump
		c.evalJSON(`window.__fr54.dump()`, &got)
		return got
	}
	quiesce := func(ms int) {
		GinkgoHelper()
		var ok bool
		c.evalJSON(fmt.Sprintf(`window.__fr54.quiesce(%d)`, ms), &ok)
	}
	sleep := func(ms int) {
		GinkgoHelper()
		var ok bool
		c.evalJSON(fmt.Sprintf(`window.__fr54.sleep(%d)`, ms), &ok)
	}
	waitEvents := func(n int) {
		GinkgoHelper()
		var seen int
		c.evalJSON(fmt.Sprintf(`window.__fr54.waitEvents(%d)`, n), &seen)
	}
	report := func(label string, v any) {
		b, _ := json.Marshal(v)
		AddReportEntry(label, string(b))
	}

	// -----------------------------------------------------------------------
	// The markup half of §5.6's derivation, checked in the browser's DOM
	// rather than read off templ.go.
	// -----------------------------------------------------------------------
	It("puts ONE debounce attribute on the element carrying BOTH bindings", func() {
		on := c.evalString(`document.querySelector("#fr54composed").getAttribute("data-gotth-on")`)
		deb := c.evalString(`document.querySelector("#fr54composed").getAttribute("data-gotth-debounce") || ""`)
		conOn := c.evalString(`document.querySelector("#fr54control").getAttribute("data-gotth-on")`)
		conDeb := c.evalString(`document.querySelector("#fr54control").getAttribute("data-gotth-debounce") || ""`)

		report("composed markup", map[string]string{"data-gotth-on": on, "data-gotth-debounce": deb})
		report("control markup", map[string]string{"data-gotth-on": conOn, "data-gotth-debounce": conDeb})

		Expect(on).To(Equal("keydown:" + fr54EventClear + ":Escape;input:" + fr54EventDraft))
		Expect(deb).To(Equal("150"),
			"the debounce the input binding asked for is on the ELEMENT, which is what both bindings read")
		Expect(conDeb).To(BeEmpty())
	})

	// -----------------------------------------------------------------------
	// NEGATIVE CONTROL. Same composition, same keys, same order, same timing
	// — no debounce. If the clear does not arrive here either, the composed
	// case below is measuring the browser or the harness, not the defect.
	// -----------------------------------------------------------------------
	It("CONTROL: with no debounce, Escape-then-keystroke delivers the clear", func() {
		var ok bool
		c.evalJSON(`window.__fr54.focus("#fr54control")`, &ok)
		before := read()

		c.call(c.sessionID, "Input.insertText", map[string]any{"text": "hello"}, nil)
		waitEvents(before.Events + 1)
		quiesce(250)

		mid := read()
		Expect(mid.CDraft).To(Equal("hello"))

		c.evalJSON(`window.__fr54.begin()`, &ok)
		escAt := fr54Float(c, `window.__fr54.mark()`)
		c.press("Escape", "Escape", "")
		c.press("x", "KeyX", "x")
		keyAt := fr54Float(c, `window.__fr54.mark()`)

		sleep(1200)
		after := read()
		d := dump()

		report("control: gap between the Escape press and the keystroke (ms, over-estimated)",
			map[string]float64{"escapeMarkedAt": escAt, "keystrokeMarkedBy": keyAt, "gap": keyAt - escAt})
		report("control: arrivals after Escape (ms since begin)", fr54ArrivalsRel(d))
		report("control: state after", after)

		Expect(keyAt - escAt).To(BeNumerically("<", 150),
			"the keystroke did not land inside a 150 ms window; this run proves nothing")
		Expect(after.Log).To(HaveSuffix(fr54EventCDraft+" "+fr54EventCClear+" "+fr54EventCDraft),
			"without a debounce the clear is delivered between the two drafts")
		Expect(after.CDraft).To(Equal("hellox"))
	})

	// -----------------------------------------------------------------------
	// THE CLAIM. Identical sequence, on the element whose input binding asked
	// for 150 ms.
	// -----------------------------------------------------------------------
	It("CLAIM: with the composed 150 ms debounce, a keystroke inside the window", func() {
		var ok bool
		c.evalJSON(`window.__fr54.focus("#fr54composed")`, &ok)
		before := read()

		c.call(c.sessionID, "Input.insertText", map[string]any{"text": "hello"}, nil)
		waitEvents(before.Events + 1)
		quiesce(300)

		mid := read()
		Expect(mid.Draft).To(Equal("hello"))
		logBefore := mid.Log

		c.evalJSON(`window.__fr54.begin()`, &ok)
		escAt := fr54Float(c, `window.__fr54.mark()`)
		c.press("Escape", "Escape", "")
		c.press("x", "KeyX", "x")
		keyAt := fr54Float(c, `window.__fr54.mark()`)

		sleep(1500)
		after := read()
		d := dump()

		tail := strings.TrimSpace(strings.TrimPrefix(after.Log, logBefore))
		report("claim: gap between the Escape press and the keystroke (ms, over-estimated)",
			map[string]float64{"escapeMarkedAt": escAt, "keystrokeMarkedBy": keyAt, "gap": keyAt - escAt})
		report("claim: arrivals after Escape (ms since begin)", fr54ArrivalsRel(d))
		report("claim: events raised by the Escape+keystroke pair", tail)
		report("claim: state after", after)

		Expect(keyAt - escAt).To(BeNumerically("<", 150),
			"the keystroke did not land inside the 150 ms window; this run proves nothing")

		// The observation §5.6 predicts: the clear never reaches the server.
		Expect(tail).NotTo(ContainSubstring(fr54EventClear),
			"§5.6 says the pending clear is cancelled outright; it arrived")
		Expect(tail).To(Equal(fr54EventDraft))
		Expect(after.Draft).To(Equal("hellox"))
	})

	// -----------------------------------------------------------------------
	// The other half of the element-scoped consequence: what the debounce does
	// to Escape when NOTHING follows it. Delivered, but late.
	// -----------------------------------------------------------------------
	It("CLAIM, second half: Escape alone on the composed element is delayed by the input binding's interval", func() {
		var ok bool
		c.evalJSON(`window.__fr54.focus("#fr54composed")`, &ok)
		quiesce(300)
		before := read()

		c.evalJSON(`window.__fr54.begin()`, &ok)
		escAt := fr54Float(c, `window.__fr54.mark()`)
		c.press("Escape", "Escape", "")
		waitEvents(before.Events + 1)
		sleep(400)

		after := read()
		d := dump()
		rel := fr54ArrivalsRel(d)

		report("delay: Escape marked at (ms)", escAt)
		report("delay: arrivals (ms since begin)", rel)
		report("delay: state after", after)

		Expect(after.Log).To(HaveSuffix(fr54EventClear))
		Expect(after.Draft).To(BeEmpty())
		Expect(rel).NotTo(BeEmpty())
		Expect(rel[0].T).To(BeNumerically(">=", 140),
			"an unrelated binding's interval did not delay the key binding")
	})

	// -----------------------------------------------------------------------
	// What exactly does the cancelling? §5.6 says "a keystroke". The runtime
	// returns from dispatch BEFORE it touches the timer when no binding
	// matches, so an unmatched keydown cannot cancel anything: it is the
	// `input` event a printable key causes that reaches the shared timer.
	// A non-printable key inside the window is therefore the discriminating
	// case, and it is also a control on the case above.
	// -----------------------------------------------------------------------
	It("MECHANISM: a NON-printable key inside the window does not cancel the clear", func() {
		var ok bool
		c.evalJSON(`window.__fr54.focus("#fr54composed")`, &ok)
		quiesce(300)
		before := read()

		c.evalJSON(`window.__fr54.begin()`, &ok)
		escAt := fr54Float(c, `window.__fr54.mark()`)
		c.press("Escape", "Escape", "")
		c.press("ArrowLeft", "ArrowLeft", "")
		keyAt := fr54Float(c, `window.__fr54.mark()`)

		sleep(1200)
		after := read()
		rel := fr54ArrivalsRel(dump())

		report("mechanism: gap (ms, over-estimated)", map[string]float64{"gap": keyAt - escAt})
		report("mechanism: arrivals (ms since begin)", rel)
		report("mechanism: state after", after)

		Expect(keyAt - escAt).To(BeNumerically("<", 150))
		Expect(after.Events).To(Equal(before.Events+1),
			"exactly one event: the clear, which an arrow key did not cancel")
		Expect(after.Log).To(HaveSuffix(fr54EventClear))
		Expect(rel).NotTo(BeEmpty())
		Expect(rel[0].T).To(BeNumerically(">=", 140))
	})

	// -----------------------------------------------------------------------
	// The interference is symmetric, and this direction loses server state
	// rather than a keystroke: a draft pending in the shared timer is
	// cancelled by the Escape, so the server never learns what was typed and
	// the input still shows it.
	// -----------------------------------------------------------------------
	It("SYMMETRY: an Escape inside the window cancels the pending DRAFT", func() {
		var ok bool
		c.evalJSON(`window.__fr54.focus("#fr54composed")`, &ok)
		quiesce(300)
		before := read()

		c.evalJSON(`window.__fr54.begin()`, &ok)
		c.press("q", "KeyQ", "q")
		typedAt := fr54Float(c, `window.__fr54.mark()`)
		c.press("Escape", "Escape", "")
		escAt := fr54Float(c, `window.__fr54.mark()`)

		sleep(1200)
		after := read()
		rel := fr54ArrivalsRel(dump())

		report("symmetry: gap (ms, over-estimated)", map[string]float64{"typedBy": typedAt, "escapeBy": escAt, "gap": escAt - typedAt})
		report("symmetry: arrivals (ms since begin)", rel)
		report("symmetry: state after", after)

		Expect(escAt - typedAt).To(BeNumerically("<", 150))
		Expect(after.Events).To(Equal(before.Events + 1))
		Expect(after.Log).To(HaveSuffix(fr54EventClear))
		Expect(after.ComposedValue).To(HaveSuffix("q"),
			"the browser kept the character the server was never told about")
	})

	// -----------------------------------------------------------------------
	// The guide's shape without the fixture's two simplifications: the bound
	// input is in the DIRTY fragment and its value is declared by the server,
	// so a morph touches it on every event. If that changed the outcome, the
	// panel above would be measuring a fixture rather than the guide.
	// -----------------------------------------------------------------------
	It("GUIDE SHAPE: same result with the input in a re-rendering fragment whose value the server declares", func() {
		var ok bool
		c.evalJSON(`window.__fr54.focus("#fr54guide")`, &ok)
		before := read()

		c.call(c.sessionID, "Input.insertText", map[string]any{"text": "hello"}, nil)
		waitEvents(before.Events + 1)
		quiesce(300)

		mid := read()
		Expect(mid.GDraft).To(Equal("hello"))
		Expect(mid.GuideValue).To(Equal("hello"), "the server declared the value and the morph applied it")
		logBefore := mid.Log

		c.evalJSON(`window.__fr54.focus("#fr54guide")`, &ok)
		c.evalJSON(`window.__fr54.begin()`, &ok)
		escAt := fr54Float(c, `window.__fr54.mark()`)
		c.press("Escape", "Escape", "")
		c.press("x", "KeyX", "x")
		keyAt := fr54Float(c, `window.__fr54.mark()`)

		sleep(1500)
		after := read()
		rel := fr54ArrivalsRel(dump())
		tail := strings.TrimSpace(strings.TrimPrefix(after.Log, logBefore))

		report("guide: gap (ms, over-estimated)", map[string]float64{"gap": keyAt - escAt})
		report("guide: arrivals (ms since begin)", rel)
		report("guide: events raised by the Escape+keystroke pair", tail)
		report("guide: state after", after)

		Expect(keyAt - escAt).To(BeNumerically("<", 150))
		Expect(tail).NotTo(ContainSubstring(fr54EventGClear),
			"the clear arrived on the guide-shaped element but not on the panel one")
		Expect(tail).To(Equal(fr54EventGDraft))
		Expect(after.GDraft).To(Equal("hellox"))
	})

	// The control's own second half: Escape alone with no debounce is prompt.
	It("CONTROL, second half: Escape alone on the undebounced element is prompt", func() {
		var ok bool
		c.evalJSON(`window.__fr54.focus("#fr54control")`, &ok)
		c.call(c.sessionID, "Input.insertText", map[string]any{"text": "z"}, nil)
		quiesce(300)
		before := read()

		c.evalJSON(`window.__fr54.begin()`, &ok)
		c.press("Escape", "Escape", "")
		waitEvents(before.Events + 1)
		sleep(300)

		after := read()
		rel := fr54ArrivalsRel(dump())
		report("control delay: arrivals (ms since begin)", rel)
		report("control delay: state after", after)

		Expect(after.Log).To(HaveSuffix(fr54EventCClear))
		Expect(rel).NotTo(BeEmpty())
		Expect(rel[0].T).To(BeNumerically("<", 140),
			"the control's clear was itself slow, so the composed element's delay is not evidence")
	})
})

func fr54ArrivalsRel(d fr54Dump) []fr54Arrival {
	out := make([]fr54Arrival, 0, len(d.Arrivals))
	for _, a := range d.Arrivals {
		out = append(out, fr54Arrival{Log: a.Log, T: a.T - d.T0})
	}
	return out
}
```
