[identifiers genericized for publication - measurements unmodified]

# Checkpoint 2 — the browser evidence: DOM preservation and HTMX coexistence

| | |
|---|---|
| **Author** | QA-1 (Correctness) |
| **Scope** | the two suites the [checkpoint-2 batch](../reviews/checkpoint-2-batch.md) orchestrator log lists as **unbuilt** — DOM preservation across the browser matrix, and HTMX coexistence |
| **Date** | 2026-08-04 |
| **Held against** | [PRD](../PRD.md) FR-25, FR-26, FR-27, FR-28, FR-30, FR-31, FR-32, G8, NFR-7; [RFC-0001](../rfc/001-architecture.md) §10.1–§10.3 |
| **Files added** | `test/internal/conformance/dom_preservation_test.go`, `test/internal/conformance/htmx_test.go`, `test/internal/conformance/testdata/` (htmx 2.0.10 + README), this document |
| **Files changed** | `client/test/dom.mjs`, `client/test/morph.test.mjs` |
| **Browser** | Chromium **151.0.7922.71**, headless, Debian 13 (trixie), x86-64, in `dis-gotth-live-bench:latest` |
| **Verdict** | **19 browser specs, 19 green, 1 pending.** Both suites built and both shown to fail under mutation. Two new defects: **D-15** (MEDIUM, FR-25 `<details>`), **D-16** (LOW-MEDIUM, documentation gap, FR-31/G8). **NFR-7's matrix is one cell of eight**; §5 says which seven were not measured and why |

The orchestrator log's sentence being discharged:

> Unbuilt: the DOM-preservation conformance suite across NFR-7's browser
> matrix (FR-25, FR-26, FR-27, FR-28), HTMX coexistence (FR-30, FR-31, FR-32,
> G8) …

Both are now built. The matrix half of that sentence is **not** discharged and
§5 is the honest statement of how much of it is.

---

## 1. What was asserted, mapped to the requirement text

Every row is a spec that ran in Chromium and passed. The requirement text is
quoted from `docs/PRD.md`, not paraphrased.

### FR-25 — "Morph MUST preserve, for elements that survive the morph: …"

| FR-25 clause | Spec | Measured |
|---|---|---|
| focus | `keeps focus, the caret and the uncommitted value of an uncontrolled input` | `document.activeElement.id == "draft"` after two patches |
| text-selection/caret position | same spec | caret `[4,8)` before and after |
| scroll position (element) | `keeps element scroll offset and document scroll offset` | `#scroller.scrollTop` 240 → 240 |
| scroll position (document) | same spec | `window.scrollY` 400 → 400 |
| uncontrolled input values | `keeps focus, the caret …` and `replaces an uncommitted value …` | `#draft` `"half typed"` kept; `#freeform` textarea `"also still mine"` kept |
| *(the other half of the same rule)* | `replaces an uncommitted value where the server rendered one, and only there` | `#controlled` `"user edit not yet sent"` → `"server-1"` **in the same patch** that kept the two above |
| checkbox/radio state | `keeps checkbox, radio and <select> state the server did not declare` | checkbox `true`, `#radio-b` `true` |
| `<select>` selection | same spec | `"c"`, `selectedIndex 2` |
| `<details>` open state | **`PIt` — D-15, see §4** | **fails**: the disclosure closes |
| media playback position | `keeps media playback position` | `<audio>` `currentTime` 1.250 → 1.250, `readyState 4` |
| in-flight CSS transitions | `does not restart an in-flight CSS transition` | one `CSSTransition`, the **same `Animation` object**, `playState "running"`, `currentTime` 50.0 ms → 116.6 ms |

**Which is controlled and which is not — read out of the requirement, not
guessed.** FR-25 says *uncontrolled input values* are preserved and says
nothing about controlled ones. `client/runtime.js` `syncProps` implements that
as: an attribute PRESENT in the incoming markup means the server is
controlling that property; ABSENT means the value is the user's. Both arms are
asserted, in one patch, in one spec, because either alone is half a rule — a
runtime that preserved everything could not implement a form reset, and one
that overwrote everything would eat what the user is typing.

### FR-26 — "Morph MUST NOT destroy an active IME composition in a focused input"

| Clause | Spec | Measured |
|---|---|---|
| morph does not destroy the composition | `does not overwrite the value of an input with an active composition` | server value attribute moved `"server-0"` → `"server-2"` across two patches; the input still read **`"にほんごserver-0"`** |
| *(the paired rule in `dispatch`)* | `raises no event while a composition is active` | click during composition → `#tickline` unchanged at `"tick 0"`; after the composition ends the same click gives `"tick 1"` |

The composition is a **real** one, driven through CDP `Input.imeSetComposition`
— the browser's own IME entry point, which raises genuine `compositionstart`
and `compositionupdate` and puts the element into a composing state. A spec
that synthesised `CompositionEvent` objects from page script would be checking
that the runtime listens to what the spec dispatches, which is a tautology; the
page's own listener saw `["start"]` and no `"end"`, so the patches really did
land mid-composition.

The first clause needed a patch **nobody on the page asked for**, because the
second clause guarantees a click cannot produce one while composing. The IME
application therefore carries a per-session ticker effect. That is not a
workaround: a change arriving from elsewhere while you are half-way through
typing a word is exactly the situation FR-26 describes.

### FR-27 — "Elements marked `data-gotth-preserve` MUST be left untouched by morph, including their subtree"

| Spec | Measured |
|---|---|
| `leaves a data-gotth-preserve subtree untouched, including content the page wrote` | the server renders `<b>server 2</b>` inside `#vault`; the page had written `<i id="owned">client owned</i>` and it is byte-identical after two patches. Root identity **and** subtree identity both preserved |

### FR-28 — "a morphed subtree MUST remain fully interactive with no re-binding step"

| Spec | Measured |
|---|---|
| `keeps a morphed subtree interactive, and a control the morph inserted works on its first click` | `#alt` is absent from the first paint, is inserted by the patch at tick 2, and works on its **first** click (`note alt`); `#tick` survives two morphs with node identity intact and still raises events |

A control the *morph* inserted is the strong form. Nothing has ever bound to
it, and there is no re-binding step for the application to call, so if it works
the binding cannot be per-node.

### FR-30 — "serve plain-HTMX pages and gotth-live pages from the same server, router, and layout, with no gotth-live JS loaded on the non-live pages"

| Spec | Measured |
|---|---|
| `serves a plain-HTMX page with no gotth-live JavaScript on it` | `GET /plain`: no `gotth-live.min.js`, no `data-gotth-url` (checked **from Go**, so it is a statement about what was served); `window.gotthLive` undefined; **sockets opened `[]`**; HTMX swap still works |
| `serves a live page from the same mux, and both systems boot on it` | same mux, same router, same layout helper: socket `[ws://127.0.0.1:…/live]`, status `"live"`, HTMX swap `"swapped:outside"`, live patch `"tick 1"` |

The socket recorder is installed before any page script and is the **same
instrument** in both specs: it reports one socket on the live page and zero on
the plain one. That is what makes the zero mean something.

### FR-31 — "gotth-live MUST NOT intercept, cancel, or rewrite `hx-*` requests. Regions outside a declared live region MUST NOT be touched by morph."

| Clause | Spec | Measured |
|---|---|---|
| regions outside a live region untouched | `does not touch an HTMX region outside every declared live region` | HTMX swapped `#outside-slot` to `"swapped:outside"`, then two live patches ran; content **and node identity** both intact |
| no interception or cancellation | `neither intercepts nor cancels an hx-* click` | two `hx-*` clicks, one outside a live region and one inside, both reached the server (`[/hx/frag?who=outside /hx/frag?who=owned]`), and **neither was `defaultPrevented`** |
| the session survives HTMX activity | `keeps the live session working across HTMX swaps` | three swaps, then `data-gotth-status` still `"live"` and a patch still lands |

The `defaultPrevented` instrument is a **bubble**-phase listener at the
document. gotth-live's own listener is at the document in the **capture**
phase, so it runs strictly earlier; if it had cancelled the click, the bubble
listener would see it. That ordering is what makes the assertion meaningful,
and it is why mutation **M5** (§3) turns this spec red.

### FR-32 — "Ambiguous ownership MUST be a developer-facing error at render or a documented, tested precedence rule — never undefined behaviour"

RFC-0001 §10.3 chose the precedence rule and wrote it down:

> **Innermost declaration wins.** … An `hx-*` element inside a live fragment
> **without** `data-gotth-preserve` is server-owned: morph will overwrite it,
> and any HTMX swap into it will be reverted by the next patch.

| Arm | Spec | Measured |
|---|---|---|
| unpreserved ⇒ server-owned, swap reverted | `reverts an HTMX swap into an unpreserved element inside a live fragment` | `#owned-slot`: `"server 0"` → HTMX swap `"swapped:owned"` → next patch `"server 1"`. The element itself was **morphed, not replaced**, so the revert is reconciliation rather than demolition |
| preserved ⇒ HTMX-owned, swap survives | `leaves an HTMX swap inside a data-gotth-preserve element alone` | `#vault-slot` inside `data-gotth-preserve`: `"swapped:vault"` survives two live patches |

Asserted as the **documented outcome**, deliberately, and not as a bug. FR-32
forbids *undefined* behaviour; "reverted" is defined, and this is the sequence
RFC §10.3 says "the QA-1 case is exactly".

### G8 — "An app with both plain-HTMX pages and live regions works; live and HTMX regions on the *same page* do not corrupt each other"

Both halves are the specs above: FR-30's two specs are the "both kinds of page
from one app" half, FR-31's three and FR-32's two are the "same page, no
corruption" half. Nothing extra is claimed for G8 beyond those seven specs.

### The seam: HTMX behaviour *after* a morph

| Spec | Measured |
|---|---|
| `keeps an hx-* control that existed at first paint working after a morph` | `#survivor` morphed through two patches, node identity intact, `hx-get` still fires **with no `htmx.process()` call** |
| `does not activate an hx-* control the morph inserted until htmx.process is called (D-16)` | `#newcomer`, inserted by the patch at tick 1: first click → **0 HTMX requests**, target still `"-"`. After `htmx.process(live region)`: swap succeeds, target `"swapped:newcomer"` |

That pair is **D-16** and is the honest answer the brief asked for. It is
reported in §4 as a documentation gap rather than a code defect, with the
reasoning.

---

## 2. What I verified by running it, not by reading it

Everything in §1. The exact invocations and the exact results.

**The suite, in the bench image** — the documented invocation, and the one to
re-run:

```bash
docker run --rm \
    -v /home/dev/worktrees/gotth-live-orchestrator-c3efc4:/repo \
    -w /repo/gotth-live dis-gotth-live-bench:latest \
    bash -c 'go test ./test/internal/conformance/ -count=1 -v -timeout 15m \
             -args -ginkgo.label-filter=browser -ginkgo.v'
```

```
Ran 19 of 154 Specs in 9.624 seconds
SUCCESS! -- 19 Passed | 0 Failed | 1 Pending | 134 Skipped
```

The 19 are the specs in §1. The 1 pending is D-15. The three
`Label("browser","e2e")` specs from checkpoint 1 skip in that run because
`GOTTHLIVE_E2E` is unset; adding `GOTTHLIVE_E2E=1` runs those too and is the
form `browser_test.go`'s own header documents.

**The specs skip, loudly, in the library image** — this is the gating
requirement, and the message names the image to run in:

```bash
~/bin/dis run go test -count=1 -v ./test/internal/conformance/ \
    -args -ginkgo.label-filter=browser -ginkgo.v
```

```
  [SKIPPED] browser: CHROME_BIN is unset — run in dis-gotth-live-bench:latest
  In [BeforeAll] at: /workspace/test/internal/conformance/htmx_test.go:422

Ran 0 of 154 Specs in 0.005 seconds
SUCCESS! -- 0 Passed | 0 Failed | 1 Pending | 153 Skipped
```

`browserOnly()` is called first thing in each `BeforeAll`, before any server or
browser is started, so the library image allocates nothing.

**The library image stays green.** `~/bin/dis run go vet ./...` exit 0.
`~/bin/dis run go test -race -count=1 ./...` exit 0, every package `ok`,
`test/internal/conformance` 153.7 s (that is the pre-existing suite; the 19
browser specs skip). The full `ci.sh` verdict is in §6.

**The node-side suite stays green**, with D-15 now visible in it:

```bash
docker run --rm -v …:/repo -w /repo/gotth-live dis-gotth-live-bench:latest \
    bash -c 'for f in client/test/*.test.mjs; do node --test "$f"; done'
```

`bundle.test.mjs` 2/2, `codec.test.mjs` 34/34, `morph.test.mjs` 15 pass / 0
fail / **1 todo**, every file exit 0.

**The vendored HTMX**, fetched and cross-checked inside the bench image from
three independent origins, all byte-identical:

```
71ea67185bfa8c98c39d31717c6fce5d852370fcdfd129db4543774d3145c0de  unpkg      htmx.org@2.0.10/dist/htmx.min.js
71ea67185bfa8c98c39d31717c6fce5d852370fcdfd129db4543774d3145c0de  jsDelivr   htmx.org@2.0.10/dist/htmx.min.js
71ea67185bfa8c98c39d31717c6fce5d852370fcdfd129db4543774d3145c0de  npm tgz    package/dist/htmx.min.js
sha512-kdeJe7ZVwaS6QMz/ebBIVtZdpwen6L0OQ5GOhPV9MKBb196TCZeZu4yA7ZIQsaLKv7EpXz+So7KSXNuHXhj7Cw==   (matches npm's recorded integrity for 2.0.10)
```

`htmx_test.go` re-checks that SHA-256 on every run, so the digest is enforced
and not merely written down. Provenance and the reproduction command are in
`test/internal/conformance/testdata/README.md`.

### 2.1 The one thing every spec actually rests on

Morph preserves state by never replacing the node that holds it, and **a
replaced node and a morphed node render identical HTML**. So markup proves
nothing here. Every spec tags the live node from the page with an expando the
server cannot send (`__qaMark`) and requires the tag to still be there
afterwards.

The first draft of the scroll spec read that tag off a **captured reference**
rather than a fresh query. A detached node keeps its expando, so it reported
"preserved" for a node the patch had thrown away — the spec passed under
mutation M1, which replaces every node. That is fixed, the fix is a fresh
`querySelector`, and the comment in the file says why, because it is the exact
vacuity the mechanism exists to prevent.

The same spec taught something worth recording: **element scroll offset
survives even a full replace**, because `save`/`restore` in the runtime
explicitly restores `scrollTop` for identified elements. Offset alone is
therefore a weak assertion; node identity is the one that distinguishes morph
from replace.

---

## 3. Non-vacuity, by mutation

Seven mutations. Six change `client/runtime.js` and are rebuilt through
`tools/minify` into `live/clientjs/gotth-live.min.js` — the artifact the
library actually serves — then run and reverted. One changes a fixture in this
suite. **Nothing was committed**; `client/runtime.js`
(`32bfeefae398f842…`) and `live/clientjs/gotth-live.min.js`
(`f4f5d424a91c46b7…`) are byte-identical to their pre-mutation state, verified
after every run.

| | Mutation | Bundle digest | Result |
|---|---|---|---|
| **M1** | `match()` returns `null` unconditionally — morph replaces every node instead of reconciling it | `bdda46510ab3ff03` | **13 of 19 red** |
| **M2** | `preserved()` returns `false` — the FR-27 opt-out is gone | `91302b85223a6680` | **2 red** |
| **M3** | `syncProps` drops the `a !== composing` guard | `af3d3a0fef4a580b` | **1 red** |
| **M4** | `region(id)` returns `document.body` — the ownership boundary is widened to the page | `ccc7362f8d00d461` | **8 red** |
| **M5** | `dispatch` calls `e.preventDefault()` on every event | `0e7a0a5050a45841` | **1 red** |
| **M6** | `dispatch` drops `if (composing) return` | `ca70bd20eacfebea` | **1 red** |
| **M7** | fixture: the plain page loads `live.Script` too | *(no rebuild)* | **1 red** |

Pristine, for comparison: `f4f5d424a91c46b7`, 19 passed, 0 failed.

### M1 — morph replaces instead of morphing

The canonical falsifier for the whole DOM-preservation suite. With `match()`
always failing, every new child is inserted and every old one dropped, so the
resulting tree is *correct* and every node is *new*.

```
Ran 19 of 154 Specs in 30.475 seconds
FAIL! -- 6 Passed | 13 Failed | 1 Pending | 134 Skipped

  [FAIL] DOM state a morph must preserve (FR-25) [It] keeps focus, the caret and the uncommitted value of an uncontrolled input
  [FAIL] DOM state a morph must preserve (FR-25) [It] replaces an uncommitted value where the server rendered one, and only there
  [FAIL] DOM state a morph must preserve (FR-25) [It] keeps element scroll offset and document scroll offset
  [FAIL] DOM state a morph must preserve (FR-25) [It] keeps checkbox, radio and <select> state the server did not declare
  [FAIL] DOM state a morph must preserve (FR-25) [It] keeps media playback position
  [FAIL] DOM state a morph must preserve (FR-25) [It] does not restart an in-flight CSS transition
  [FAIL] Preserve and delegation across a morph (FR-27, FR-28) [It] leaves a data-gotth-preserve subtree untouched, including content the page wrote
  [FAIL] Preserve and delegation across a morph (FR-27, FR-28) [It] keeps a morphed subtree interactive, and a control the morph inserted works on its first click
  [FAIL] The declared ownership boundary (FR-32, R-11) [It] reverts an HTMX swap into an unpreserved element inside a live fragment
  [FAIL] The declared ownership boundary (FR-32, R-11) [It] leaves an HTMX swap inside a data-gotth-preserve element alone
  [FAIL] IME composition safety (FR-26) [It] does not overwrite the value of an input with an active composition
  [FAIL] HTMX behaviour after gotth-live has morphed the node (FR-31, G8) [It] keeps an hx-* control that existed at first paint working after a morph
  [FAIL] A live region and HTMX regions on one page (FR-31, G8) [It] neither intercepts nor cancels an hx-* click
```

**Every FR-25 case is in that list.** The six that survive M1 are the two FR-30
page specs (no morph is involved), FR-26's second clause (about `dispatch`, not
morph), FR-31's outside-the-region and session-survives specs (correct: morph
never leaves its region, so replacing everything *inside* one is invisible from
outside), and D-16 (whose subject is `htmx.process`, orthogonal to how the node
got there).

### M2 — the preserve opt-out removed

```
FAIL! -- 17 Passed | 2 Failed | 1 Pending | 134 Skipped
  [FAIL] The declared ownership boundary (FR-32, R-11) [It] leaves an HTMX swap inside a data-gotth-preserve element alone
  [FAIL] Preserve and delegation across a morph (FR-27, FR-28) [It] leaves a data-gotth-preserve subtree untouched, including content the page wrote
```

Exactly the two specs that claim FR-27, and no others — which also says the
other seventeen are not accidentally depending on the opt-out.

### M3 — morph writes the value of a composing input

```
FAIL! -- 18 Passed | 1 Failed | 1 Pending | 134 Skipped
  [FAIL] IME composition safety (FR-26) [It] does not overwrite the value of an input with an active composition
```

### M4 — the ownership boundary widened to the whole page

`region(id)` returning `document.body` is what FR-31's "regions outside a
declared live region MUST NOT be touched" forbids, expressed as code.

```
FAIL! -- 11 Passed | 8 Failed | 1 Pending | 134 Skipped
  [FAIL] HTMX and gotth-live pages from one server (FR-30, G8) [It] serves a live page from the same mux, and both systems boot on it
  [FAIL] DOM state a morph must preserve (FR-25) [It] keeps element scroll offset and document scroll offset
  [FAIL] HTMX behaviour after gotth-live has morphed the node (FR-31, G8) [It] keeps an hx-* control that existed at first paint working after a morph
  [FAIL] The declared ownership boundary (FR-32, R-11) [It] reverts an HTMX swap into an unpreserved element inside a live fragment
  [FAIL] The declared ownership boundary (FR-32, R-11) [It] leaves an HTMX swap inside a data-gotth-preserve element alone
  [FAIL] A live region and HTMX regions on one page (FR-31, G8) [It] does not touch an HTMX region outside every declared live region
  [FAIL] A live region and HTMX regions on one page (FR-31, G8) [It] neither intercepts nor cancels an hx-* click
  [FAIL] A live region and HTMX regions on one page (FR-31, G8) [It] keeps the live session working across HTMX swaps
```

Five of the seven HTMX specs, plus the document-scroll arm (the spacer block
above the region is destroyed, so the document can no longer scroll — which is
itself a correct detection of "morph touched something outside the region").

Worth stating rather than hiding: the FR-25 checkbox, media and transition
specs still pass under M4. The reason is that after the *first* destructive
apply, `<body>`'s children happen to match the fragment's children by `id`, so
every subsequent morph reconciles them in place and identity is preserved from
then on. M4 is a boundary mutation, not an identity mutation; M1 is the
identity one and those three specs are red under it.

### M5 — the runtime cancels every event it sees

```
FAIL! -- 18 Passed | 1 Failed | 1 Pending | 134 Skipped
  [FAIL] A live region and HTMX regions on one page (FR-31, G8) [It] neither intercepts nor cancels an hx-* click
```

One mutation, one spec, and it is the spec that claims the clause.

### M6 — the mid-composition send guard removed

```
FAIL! -- 18 Passed | 1 Failed | 1 Pending | 134 Skipped
  [FAIL] IME composition safety (FR-26) [It] raises no event while a composition is active

  [FAILED] a click raised a live event while an IME composition was active: the tick advanced from "tick 0" to "tick 1" (FR-26)
  Expected
      <string>: tick 1
  to equal
      <string>: tick 0
```

### M7 — the plain page loads the runtime

The FR-30 spec has no runtime to mutate — its subject is a page with no
gotth-live JavaScript on it — so its falsifier is a fixture change:
`hxPlainPage()` gains `scriptTag()`.

```
  [FAILED] the plain page carries the gotth-live runtime, which FR-30 forbids
  Expected
  not to contain substring
Ran 1 of 154 Specs in 0.176 seconds
FAIL! -- 0 Passed | 1 Failed | 1 Pending | 152 Skipped
```

The Go-side arm fires first. The browser-side arm (the socket recorder) is
shown to work by the neighbouring spec, which uses the same instrument and
reports exactly one socket on the live page.

### The one spec no mutation turns red, and why

**`does not activate an hx-* control the morph inserted until htmx.process is
called (D-16)`.** It asserts the current state of the world on both sides of
the `htmx.process` call, so it is a lock rather than a property: its falsifier
is not a mutation but the *fix*. The day gotth-live closes D-16 the first
assertion goes red and names itself stale in the failure message. That is
deliberate and is stated here so it is not re-discovered as a gap.

### How to reproduce a mutation

The driver was a throwaway script and is not committed. Each mutation is one
edit to `client/runtime.js` followed by:

```bash
docker run --rm -v <worktree>:/repo -w /repo/gotth-live dis-gotth-live-bench:latest bash -c '
  (cd tools && go run ./minify)      # rebuilds live/clientjs/gotth-live.min.js
  go test ./test/internal/conformance/ -count=1 -args -ginkgo.label-filter=browser -ginkgo.v'
```

then `git checkout -- client/runtime.js live/clientjs/gotth-live.min.js`. The
edits are:

| | in `client/runtime.js` |
|---|---|
| M1 | insert `return null;` as the first statement of `match` |
| M2 | `preserved` returns `false` |
| M3 | `else if (b.hasAttribute("value") && a !== composing)` → `else if (b.hasAttribute("value"))` |
| M4 | `region` returns `document.body` |
| M5 | insert `e.preventDefault();` as the first statement of `dispatch` |
| M6 | delete `if (composing) return;` from `dispatch` |

---

## 4. Defects

Numbering continues from D-14 (`docs/qa/checkpoint-2-conformance.md`).

### D-15 — MEDIUM — an unrelated patch closes a `<details>` the user opened (FR-25)

**Owner: DEV-2 (client runtime).** Not fixed here.

FR-25 lists `<details>` open state with no qualifier: *"Morph MUST preserve,
for elements that survive the morph: … `<details>` open state"*. It does not
hold in a browser.

`details.open` is a **reflected** IDL attribute. Opening a disclosure — by
clicking the summary, or by `el.open = true` — writes `open=""` into the DOM.
`checked` and `selected` are *not* reflected (they are checkedness and
selectedness; the attributes are only defaults), so the runtime's rule works
for those and fails for this one.

`syncProps` reads attribute presence as "the server is controlling this":

```js
} else if (t === "DETAILS") {
  if (b.hasAttribute("open") !== a.hasAttribute("open")) a.open = b.hasAttribute("open");
}
```

Live node has `open=""` (the user), incoming markup has none (the server never
mentioned it) ⇒ `a.open = false`. `syncAttrs` then removes the attribute as
well, so it is reverted twice over.

**Repro**, from the spec run as a live `It`:

```
DOM state a morph must preserve (FR-25) [It] keeps a <details> the user opened and the server never mentioned (FR-25, D-15)

  [FAILED] a <details> the user opened was closed by a patch that never mentioned it (FR-25).
  open reflects to the content attribute in a browser, so morph reads the user's own state as a
  server declaration and reverts it
  Expected
      <bool>: false
  to be true
```

The node-identity assertion **passed** on the line before, so the element was
morphed, not replaced: this is the rule doing it, not the traversal.

**Why no suite had caught it.** `client/test/dom.mjs` modelled `open` as a
plain property beside `checked` and `selected`, so `morph.test.mjs`'s *"details
open state is preserved unless the server changes it"* passed while the browser
disagreed. That shim is now accurate (commit `3c9a9a2d`), the arm that is
genuinely true stays a plain passing test, and the FR-25 arm is a node `todo`
carrying the D-15 reason — `node --test` still exits 0, `ci.sh` is unaffected,
and the assertion failure is printed where a reader sees it.

**Fix shape** (DEV-2's call as runtime owner, not QA-1's): the presence
heuristic cannot distinguish the two for a reflected attribute, so it needs a
second input. Two shapes worth weighing — track user-initiated `toggle` on
`<details>` the way `composing` is tracked for IME and skip the sync for those
elements; or invert the comparison to *previous rendered markup* rather than
*live DOM state*, which is the general form of the same problem and costs a
per-fragment memo. If neither is affordable inside NFR-2's budget, the third
option is to amend FR-25 to say `<details>` is server-owned — but that is a
PM-1 amendment, not a silent behaviour, and the requirement as written is what
this spec holds.

Held as a **`PIt`** in `dom_preservation_test.go`, following D-1's and D-14's
precedent: the requirement is executable when someone comes to it rather than a
sentence in a document.

### D-16 — LOW-MEDIUM — an `hx-*` element a morph inserts is inert until `htmx.process` is called, and nothing says so (FR-31, G8)

**Owner: DEV-3 (documentation) with DEV-2 for the runtime option.** Not fixed
here. **This is a documentation gap, not a code defect** — the runtime is doing
nothing wrong, and the honest framing matters because the fix is cheap in one
place and expensive in the other.

HTMX attaches behaviour when it **processes** a node: at load, and after its
own swaps. It does not observe the DOM. gotth-live's morph inserts nodes. So an
`hx-*` element the server begins rendering mid-session is inert.

**Measured**, both sides, in one spec:

```
D-16 — hx-* inserted by a morph needs htmx.process
  #newcomer was inserted by the patch at tick 1.
  first click, no htmx.process: 0 HTMX requests, target still "-"
  after htmx.process(live region): swap succeeded, target "swapped:newcomer"
  htmx 2.0.10, browser Chrome/151.0.7922.71
```

The good half is measured in the same container and is the reason this is
LOW-MEDIUM rather than higher:

```
hx-* survives a morph
  #survivor was morphed through two patches with node identity intact, and its
  hx-get still fired with no htmx.process() call: "swapped:survivor"
```

HTMX keeps its per-node state on the node object, and morph preserves the
object — so FR-28's delegation argument pays off for a third-party library too,
for every node that already existed. Only *newly inserted* `hx-*` markup is
affected.

**Why it matters despite being narrow.** RFC-0001 §10.3 and `live.Preserve`'s
doc comment both tell an HTMX application exactly what to do about ownership,
and neither mentions this. A developer who follows that guidance correctly —
puts HTMX-owned DOM inside `data-gotth-preserve`, leaves server-owned
`hx-*` markup unpreserved — gets a control that silently does nothing the first
time the server renders it. Silent no-ops in the browser with no server-side
error are the failure mode `live.Script`'s doc comment already spends a
paragraph warning about.

**Fix shape**: the cheap one is documentation — one sentence in
`live.Preserve`'s doc comment and in RFC-0001 §10.3 saying that `hx-*` markup a
patch introduces needs `htmx.process` on the region, with the one-line
`gotth-live:patched` listener a consumer would write. The expensive one is a
runtime hook (a `CustomEvent` after each `apply`) which costs NFR-2 bytes and
introduces an extension point RFC-0001 has so far declined to have; that is a
DEV-2 and PM-1 decision, not a QA one.

The spec asserts the current behaviour on both sides of the `process` call, so
it goes red the day this is closed rather than staying silent.

---

## 5. NFR-7's browser matrix — what was measured and what was not

NFR-7, in full:

> **NFR-7 — Browser support.** Latest two stable versions of Chrome, Firefox,
> Safari (macOS), and Safari (iOS). Stated in the README; the DOM conformance
> suite runs against this matrix.

Eight cells. **One was measured.** No cell below is estimated, inferred from
another engine, or assumed from standards conformance.

| Cell | Status | Evidence / reason |
|---|---|---|
| Chrome, latest stable | **MEASURED** | Chromium **151.0.7922.71**, headless, Debian 13, x86-64. Google's version-history API reports Linux stable as `151.0.7922.71` — the same build number as the image's package. 19/19 green |
| Chrome, previous stable (150.x) | **NOT MEASURED** | No 150 build exists in any project image. The bench image installs Debian's `chromium` from a rolling security suite, which carries one version; obtaining a second would mean pinning a download in the image, and the images must not be rebuilt this round |
| Firefox, latest stable | **NOT MEASURED** | Measured obstruction, not a guess: `firefox-esr` **140.13.0esr** installed in a throwaway container announces `WebDriver BiDi listening on ws://127.0.0.1:9222` and `GET /json/version` returns **404**. The harness in `cdp_test.go` speaks Chrome DevTools Protocol and nothing else, so it cannot drive this browser at all. A second harness speaking WebDriver BiDi is a round of work, not a cheap add, and `cdp_test.go` is outside this round's write scope |
| Firefox, previous stable | **NOT MEASURED** | Same obstruction, and no 139/140-pair is available either |
| Safari macOS, latest stable | **NOT MEASURED** | Safari does not exist for Linux. No project image contains WebKit, and this runs on a Linux VM |
| Safari macOS, previous stable | **NOT MEASURED** | Same |
| Safari iOS, latest stable | **NOT MEASURED** | Requires an iOS device or simulator, which is macOS-only |
| Safari iOS, previous stable | **NOT MEASURED** | Same |

**What that means for the checkpoint-2 exit criterion.** The criterion reads
*"DOM conformance suite green across NFR-7's browser matrix for every case in
FR-25, plus IME composition (FR-26) and `data-gotth-preserve` (FR-27)"*. The
**suite** half is now met — every FR-25 case, FR-26 and FR-27 are specs that
run and that fail under mutation. The **matrix** half is met for one cell of
eight. PM-1 and L9-1 should treat this criterion as **partially met** and
decide explicitly, because there are only two honest ways to close it and both
are decisions rather than work QA-1 can do alone:

1. Add a WebDriver BiDi harness and a Firefox to the bench image, which buys
   two more cells and costs an image change plus a second protocol client; or
2. Amend NFR-7 to state the matrix the project can actually run in CI, and say
   in the README which browsers are *supported by intent* versus *verified by
   test*. A README claiming four engines on the strength of one measured engine
   is the same overstatement `docs/PRD.md`'s "which of these numbers is a
   measurement" section was added to prevent.

**What is deliberately not claimed.** The engine-independent parts of these
specs — the protocol, the traversal, node identity — are not evidence about
Gecko or WebKit. The three cases most likely to diverge across engines are
precisely the ones that needed browser evidence in the first place: caret
behaviour on `setSelectionRange` after an attribute write, IME composition
semantics (which are platform-IME dependent even within one engine), and
`Element.getAnimations()` for CSS transitions. Nothing here says anything about
any of them outside Chromium 151.

---

## 6. What this deliberately does not close

**D-15 and D-16 are reported, not fixed.** Both are `client/` and `docs/`
changes owned by other roles this round. Each is executable: D-15 as a `PIt`
plus a node `todo`, D-16 as a spec that inverts when the gap closes.

**FR-26 is measured against the browser's IME plumbing, not a platform IME.**
CDP `Input.imeSetComposition` produces a real composing state and real
composition events, which is what the runtime's rule is written against. It is
not a Japanese, Korean or Chinese input method running on the host, and
`compositionupdate` sequences differ between IMEs. If a platform IME
misbehaves, this spec will not see it.

**The counter and chat examples are not re-checked here.** These suites mount
their own applications, built to hold the state FR-25 names, because
`examples/counter` has one number and four buttons and none of that state
exists in its markup. The examples' own browser evidence stays in
`browser_test.go` (CP1-01, CP1-08, CP1-13).

**Nothing here measures performance.** Morph cost, event→paint and patch size
are elsewhere (CP1-08, `bench/`). Two patches in these specs carry a growing
run of pips and sixty scroll rows; that is fixture bulk chosen to force
traversal work, not a size claim.

**The FR-33 three-router mount suite is not this document's.** It landed
separately as `test/routers/` and is in `ci.sh`.

**One thing worth doing next and not done here.** The runtime's
controlled/uncontrolled rule keys on *attribute presence in the incoming
markup*. D-15 shows that this is unsound for any attribute the browser reflects
from user state. `<details open>` is the case FR-25 names, but the shape is
general — `<dialog open>` has it too, and so would any custom element that
reflects internal state to an attribute. Nothing in this suite or in
`morph.test.mjs` checks the general form. A property test over the reflected-
attribute set would be the right instrument and is a round of its own.

---

## 7. The gate, as run

`~/bin/dis run bash ci.sh` from `gotth-live/`, in `dis-gotth-live:latest`,
against the shared worktree at `3c9a9a2d`. **Exit 0.** Thirteen steps: eleven
ran — build, vet, gofmt, staticcheck, `-race` across the module,
`examples/counter`, `examples/chat`, `test/routers` (FR-33), the gate's own
tests, the FR-65 surface delta and the client size gate — and two announced
themselves as skipped for want of a context this invocation does not have:

```
==> verdict
skipped (needs a context this invocation does not have):
  - codegen reproducibility (FR-7)
  - client runtime suite (NFR-4)
every gate this invocation could run is green
```

The second of those two — the client runtime suite, which is where the D-15
`todo` now lives — was run separately in the bench image and is green (§2).

Plus the browser suite, which `ci.sh` does not run because the library image
has no browser — the same structural absence that keeps node out of it:

```
docker run --rm -v <worktree>:/repo -w /repo/gotth-live dis-gotth-live-bench:latest \
    bash -c 'go test ./test/internal/conformance/ -count=1 -v -timeout 15m \
             -args -ginkgo.label-filter=browser -ginkgo.v'

Ran 19 of 154 Specs in 9.624 seconds
SUCCESS! -- 19 Passed | 0 Failed | 1 Pending | 134 Skipped
```

## 8. Sign-off

Both suites the orchestrator log listed as unbuilt are built, green, and shown
to fail under mutation of the artifact the library actually serves. Two
defects, D-15 and D-16, are reported unfixed and pre-registered as executable
specs. NFR-7's matrix is measured for one cell of eight and §5 names the other
seven and why — that is a partially met exit criterion and it needs a PM-1
decision, not more test code.

— QA-1, 2026-08-04
