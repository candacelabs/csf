# Phase 4 gate — QA-1 builds a working app from the docs alone

**Date:** 2026-08-05
**Tree gated:** `452e1e74c8f2f6c47bba750b100bdefb79332c96` (branch `dev-/gotth-live-orchestrator-c3efc4`)
**Gate box:** PRD §6 Phase 4 — *"QA-1 builds a small, working app from the docs
alone — no reading library source, no asking DEV-1/2/3."*

**Verdict: PASS on the docs-alone box. The FR-53 ≤30-line box is a confirmed
miss at 46. The FR-53 ≤15-minute box passes at 2 m 12 s, with a stated caveat
about who was holding the timer.**

---

## 1. What I read, in the order I read it

The discipline for this gate is that only `docs/**` is readable. **I did not open
a single file outside `docs/`. There were zero breaches — the docs never stranded
me into library source.** That is the headline result and §4 has no breach
entries in it.

| # | File | Why, and when |
|---|---|---|
| 1 | `docs/quickstart.md` | Cold start. Read once, top to bottom, before writing anything. **This is the only file I needed to build the app.** |
| 2 | — | *(app written, generated, built, run and driven in a browser — see §2)* |
| 3 | `docs/README.md` | After the fact, to answer the handoff's question about the guide index from a cold start (§5.5). |
| 4 | `docs/PRD.md` | FR-53's counting rule and the §6 Phase 4 box wording, to check my count against the published one. Provenance, not a build input. |
| 5 | `docs/guide/fragments-and-dirty-tracking.md` §"How many fragments" | Chasing the first-paint agreement rule for §5.1. |
| 6 | `docs/guide/*.md`, `docs/api-surface.md` | `grep` sweeps only, to establish whether a topic is documented **anywhere** before calling it undocumented. Listed inline in §4. |

I listed the filenames under `docs/guide/_samples/` but opened none of them; they
are `.go` and `.templ` files and reading them would have been a breach.

**Tooling note, not a docs hint:** there is no Go and no node on this host.
Everything below ran in `dis-gotth-live:latest` (Go 1.26, templ) and
`dis-gotth-live-bench:latest` (adds node 24 + chromium). The app was built in
`/tmp/docs-alone/app`, outside the repository.

**Provenance caveat, and what I did about it.** This worktree is shared with a
concurrent agent, which landed uncommitted changes to `live/app.go`,
`live/config.go`, `live/templ.go` and new dev-reload files with mtimes from
`10:02:11Z` onward. **The timed run in §2 finished at `09:57:15Z`, before the
earliest of those edits**, so it built against the tree as committed at
`452e1e74`. My later variant experiments (§4 F-1, F-2, F-4) did overlap that
window, so I re-ran every one of them against a pristine
`git archive 452e1e74 | tar -x` export at `/tmp/pristine`, with the module
`replace` repointed at it. **All four results reproduce identically on the clean
gated tree**; the concurrent work changed no finding here:

| Build | `GET /` first paint | runtime URL | WS upgrade `GET /live` |
|---|---|---|---|
| control, quickstart as written | `<output>0</output>` | `200 text/javascript`, 10,391 B | `101` |
| F-1: subtree `http.Handle` deleted | `<output>0</output>` | **`200 text/html`, 301 B** | `101` |
| F-2: `StripPrefix` style | `<output>0</output>` | `200 text/javascript`, 10,391 B | **`307`** |
| F-4: `Init` returns `State{N: 41}` | **`<output>0</output>`** | `200 text/javascript`, 10,391 B | `101` |

---

## 2. The timed run

| Mark | UTC | Elapsed |
|---|---|---|
| Opened `docs/quickstart.md` | `09:55:03Z` | 0:00 |
| `templ generate` + `go mod tidy` + `go build ./...` all green, **first attempt, zero errors** | `09:56:12Z` | 1:09 |
| Working counter confirmed **in real chromium** | `09:57:15Z` | **2:12** |

**Wall clock to a working counter: 2 minutes 12 seconds (132 s).** Against the
FR-53 budget of ≤15 minutes, that **passes with 12+ minutes of margin**.

### "Working" means loaded in a browser and clicked, not "it compiled"

Headless chromium in the bench image, driven over CDP (no puppeteer in the image;
node 24's global `WebSocket` speaks CDP directly). The clicks are
`Input.dispatchMouseEvent` press/release pairs at the button's measured centre —
real trusted input events, not `element.click()`.

```
firstPaintOutput  : "0"          <- server-rendered, present before any JS ran
scriptTag         : <script src="/live/gotth-live.min.js" data-gotth-url="/live" defer>
regionAttr        : "count"      <- data-gotth-region
onAttr            : "click:count.inc"
statusReached     : "live"       <- data-gotth-status
afterClick1       : "1"   (101 ms)
afterClick2       : "2"   (102 ms)
afterClick3       : "3"   (102 ms)
afterReload       : "0"          <- fresh session, fresh mount
consoleErrors     : []
failedRequests    : []
responses         : 200 /, 200 /live/gotth-live.min.js
```

All five of the quickstart's own verification steps pass, in its own order. The
runtime is served `200 text/javascript; charset=utf-8`, 10,391 bytes.

### The honesty caveat on 2 m 12 s

I am not a human developer and I will not pretend the number transfers. I read
the whole quickstart and typed three files in about a minute; a person does not.
**What the measurement actually attests is a property of the document, not of
me:** the quickstart is copy-paste-complete for `main.go` — the Go block plus the
prose line naming its five imports compiles as given, with no edits and no
guessing — and it built on the first attempt with zero compiler errors. The one
thing I had to supply from my own knowledge was `view.templ`'s import block,
which the page never gives (finding **F-3**). A reader without Go fluency stalls
exactly there and nowhere else. **The ≤15-minute box passes on this evidence, but
the margin is the document's, not the timer's.**

---

## 3. The app source, verbatim, and the line count

Three files. `view_templ.go` is generated by `templ generate` and is excluded
from the count, per FR-53's method.

### `main.go` — transcribed from `docs/quickstart.md` §2, imports from its line 98

```go
package main

import (
	"context"
	"log"
	"net/http"

	"github.com/a-h/templ"
	"github.com/candacelabs/candace/pkg/gotth/live"
)

// MountPath is where the live handler is mounted. It is one constant because
// two places must agree on it: the router below, and the live.Script call in
// view.templ that tells the browser where to fetch the runtime and open the
// connection. Nothing in the library can check that agreement.
const MountPath = "/live"

// EventInc is the one event name this application accepts. An event whose name
// is not in Config.Events is refused with UNKNOWN_EVENT before the reducer
// runs.
const EventInc = "count.inc"

type State struct{ N int }

func main() {
	app, err := live.New(live.Config[State]{
		Init: func(context.Context, live.Session) (State, []live.Effect, error) { return State{}, nil, nil },
		Reduce: func(s State, ev live.Event) (State, []live.Effect) {
			if ev.Name == EventInc {
				s.N++
			}
			return s, nil
		},
		Fragments:    []live.Fragment[State]{{ID: "count", Render: Count}},
		Events:       []string{EventInc},
		Origins:      []string{"http://127.0.0.1:8080"},
		Authenticate: live.Anonymous,
		Authorize:    live.AllowAll,
		CSRF:         live.NoCSRFCheck,
	})
	if err != nil {
		log.Fatal(err)
	}

	http.Handle(MountPath, app.Handler())
	http.Handle(MountPath+"/", app.Handler())
	http.Handle("/", templ.Handler(Page(State{})))
	log.Fatal(http.ListenAndServe("127.0.0.1:8080", nil))
}
```

### `view.templ` — transcribed from §3; **the two import lines are mine, not the doc's**

```templ
package main

import "strconv"

import "github.com/candacelabs/candace/pkg/gotth/live"

// Count is the one live fragment. Its root element carries live.Region, which
// renders data-gotth-region: morph never touches anything outside a region,
// and a patch names this ID.
templ Count(s State) {
	<p { live.Region("count")... }>
		<output>{ strconv.Itoa(s.N) }</output>
		<button { live.On("click", EventInc)... }>+1</button>
	</p>
}

// Page is the whole document. It composes the same component the fragment
// renders, from the same state, so the snapshot that arrives over the
// WebSocket morphs the page to bytes it already has.
templ Page(s State) {
	<!DOCTYPE html>
	<html lang="en">
		<head>
			<meta charset="utf-8"/>
			<title>gotth-live quickstart</title>
			@live.Script(MountPath)
		</head>
		<body>
			@Count(s)
		</body>
	</html>
}
```

### `go.mod`

```text
module example.com/counter

go 1.25.0

require (
	github.com/a-h/templ v0.3.1020
	github.com/candacelabs/candace/pkg/gotth v0.1.0
)

replace github.com/candacelabs/candace/pkg/gotth => /workspace/candace/pkg/gotth
```

### The count

Method: FR-53's, which is the quickstart's own — every line that is **not** blank,
**not** a comment, and **not** a `package` or `import` line (including the
individual quoted paths inside an import block and its closing paren). Generated
`*_templ.go` excluded. `go.mod` is not application code and is not counted.

| File | Lines under the rule |
|---|---|
| `main.go` | **27** |
| `view.templ` | **19** |
| **Total (Go + templ)** | **46** |

**The box is judged on the Go+templ number — 46 — because PRD §6 Phase 4 and
FR-53 both fix that scope, and §6 says explicitly it "is not QA-1's to settle at
the gate."** I did not attempt to settle it, and I note that the Go-only reading
would have produced **27 and a green box**, which is precisely why the scope was
pre-registered.

**46 > 30. The ≤30-line box does not close.**

**I confirm the published expectation exactly.** FR-53's note records "the
quickstart is 27 lines of Go and 46 with the templ view" from the v0.6 sweep; my
independent count at `452e1e74` reproduces both numbers to the line. Nothing has
drifted, and the miss is real rather than an artefact of an old measurement.

Twelve of the 27 Go lines are the eight `Config` fields `live.New` requires. As
FR-53 already says, that makes most of the overage an **API** finding, not a
documentation one — closing this box needs the app to shrink, and no docs edit
DEV-3 can make will do it.

---

## 4. Findings

Severity key: **blocker** = I could not build a working app from the docs;
**friction** = I got there, but the docs cost me time or would mislead a reader;
**cosmetic** = wording.

**Breach count: 0.** Nothing here required opening library source.

---

### F-1 — friction (high) — The two `http.Handle` lines are load-bearing and never explained, and the quickstart's stated diagnostic for getting them wrong cannot fire

**What I was trying to do.** Understand why `main.go` registers the live handler
twice:

```go
http.Handle(MountPath, app.Handler())
http.Handle(MountPath+"/", app.Handler())
```

**What the docs said.** Nothing. `grep -rn 'http\.Handle' docs/` returns these two
lines in `docs/quickstart.md` and no prose anywhere that explains the pair. The
only place the phrase appears is `docs/reviews/deduplication.md:458` ("the
`http.Handle` pair"), an internal review document, not a reader-facing page.

**What actually happened.** I built the variant a reader writes when they assume
one registration is enough — the subtree line deleted — and ran it in chromium:

| Probe | Quickstart as written | Second `http.Handle` line deleted |
|---|---|---|
| `GET /live/gotth-live.min.js` | `200 text/javascript`, 10,391 B | **`200 text/html`, 301 B** — the HTML page, served by the `/` catch-all |
| `data-gotth-status` | `live` | **`null`** — never set, not even `connecting` |
| WebSockets attempted | 1 | **0** |
| Click on `+1` | `0`→`1`→`2`→`3` | **inert, stays `0`** |
| Server-side error | none | **none** |
| Browser console | clean | **one `Uncaught SyntaxError: Unexpected token '<'`** |

The page renders correctly and is simply dead.

**Why this is worse than an omission.** The quickstart's verification step 2 says:

> `curl -sI http://127.0.0.1:8080/live/gotth-live.min.js` returns `200` with
> `Content-Type: text/javascript`. **A `404` means the tag and the router
> disagree about the mount path.**

**A `404` can never occur in this application.** `http.Handle("/", ...)` is a
catch-all, so every unmatched path — including the runtime URL and
`/favicon.ico` — returns `200` with the full HTML page. The documented symptom of
the documented failure is unreachable, and a reader who checks the status code as
that sentence invites concludes the mount is fine. The type is the tell, and the
sentence buries it.

Compounding it, step 3 says **"look at the network tab, not the console."** For
this failure that is exactly backwards: the network tab shows three green 200s
and the console holds the only clue.

**Where to change it:** `docs/quickstart.md` §2 (the code block needs a sentence)
and §4 step 2 (the diagnostic is wrong).

---

### F-2 — friction (high) — "Your router strips the prefix" contradicts the quickstart's own example, and following it yields a permanently reconnecting page

**What I was trying to do.** Work out whether `app.Handler()` wants the full path
or a stripped one — i.e. whether I should be using `http.StripPrefix`.

**What the docs said.** Two things that cannot both be true of the same code.
`docs/quickstart.md:220-223`, explaining `live.Script`:

> your router **strips the prefix** before the handler sees a request

`docs/api-surface.md:652` repeats it verbatim. But the quickstart's own example
uses bare `http.Handle` and **does not strip anything**. Meanwhile
`docs/reviews/checkpoint-2-batch.md:47` shows the stripping form —
`mux.Handle("/app/", http.StripPrefix("/app", app.Handler()))` — in a review
document a reader will never open. **No reader-facing page states which mounting
style the handler expects.**

**What actually happened.** I built the StripPrefix form the prose implies. The
runtime serves fine (`200 text/javascript`), and then:

```
WebSocket connection to 'ws://127.0.0.1:8080/live' failed:
  Error during WebSocket handshake: Unexpected response code: 307
  ... x7 and climbing
data-gotth-status : "reconnecting"
value after click : "0"   (inert)
```

`ServeMux` 307-redirects `/live` → `/live/` because only the subtree pattern is
registered; a WebSocket client cannot follow a redirect on an upgrade. Per the
quickstart's own text the runtime then "retries forever with backoff", which is
what the seven-and-counting attempts are.

**And `307` is not in the handshake table.** `docs/quickstart.md:275-278` lists
`403 forbidden origin`, `401 unauthenticated`, `403 forbidden`, `426`. A reader
who does the right thing — opens the network tab as instructed and reads the
handshake status — finds a code the table does not mention.

**Where to change it:** `docs/quickstart.md` §3 (`live.Script` bullet) and the §4
handshake table; `docs/api-surface.md` `Script` row.

---

### F-3 — friction — `view.templ`'s imports are never given, though `main.go`'s are

**What I was trying to do.** Compile the templ file.

**What the docs said.** For the Go file, `docs/quickstart.md:98` is explicit and
complete: *"The imports are `context`, `log`, `net/http`, `github.com/a-h/templ`,
and `github.com/candacelabs/candace/pkg/gotth/live`."* For the templ
file — nothing. The §3 block starts at `templ Count(s State) {` with no `package`
and no `import`, yet its body calls `strconv.Itoa` and three `live.*` helpers.

**What actually happened.** I supplied `import "strconv"` and the `live` import
from knowledge. A reader who copies the block as printed gets `undefined: strconv`
and `undefined: live` from `templ generate`/`go build`. It is recoverable — the
`live` path is on line 99 — but it is the one place in the whole quickstart where
copy-paste does not compile, and it is the single thing that would have cost a
less fluent reader time on the clock in §2.

**Where to change it:** `docs/quickstart.md` §3 — one prose line mirroring
line 98, or a `package`/`import` header on the block itself.

---

### F-4 — friction (high) — `templ.Handler(Page(State{}))` bakes the zero state into every first paint, and the rule that forbids it lives on another page

*(Named in the handoff. Confirmed as a real hazard, demonstrated.)*

**What I was trying to do.** Understand what `templ.Handler(Page(State{}))` means
for an app whose `Init` does not return the zero value.

**What the docs said.** The `Page` doc comment gestures at the invariant — *"from
the same state, so the snapshot ... morphs the page to bytes it already has"* —
but states it as a description of this app, not as a rule. The rule proper exists
only at `docs/guide/fragments-and-dirty-tracking.md:193-197`:

> **The page and the fragments should render the same components.** Compose the
> first paint out of the same components the fragments render, from the same
> state ... Render them differently and the first patch after connecting visibly
> rewrites a page that was already correct.

A quickstart reader does not see that. It is linked from §"Where to go next"
under the promise *"Stop re-rendering everything on every event"* — a performance
errand, not a correctness one. And neither page says how to get `Init`'s state
into the page handler, nor that `Page(State{})` is evaluated **once at startup**
so the zero value is frozen into every response.

**What actually happened.** I changed `Init` to return `State{N: 41}` and changed
nothing else — exactly the edit a reader makes when their real app loads from a
database:

```
$ curl -s http://127.0.0.1:8080/ | grep -o '<output>[0-9]*</output>'
<output>0</output>          <- every visitor receives HTML saying 0

browser, once connected:     41
```

The server-rendered HTML on the wire is wrong for every request, and is corrected
only after the WebSocket connects. That is a visible flash of wrong content on
every page load, and with JS disabled or the socket blocked the page shows `0`
permanently. The quickstart's own step 1 — *"That first paint is server-rendered
HTML, not a placeholder"* — stops being true the moment `Init` does anything.

**Where to change it:** `docs/quickstart.md` §2, on the
`http.Handle("/", templ.Handler(...))` line.

---

### F-5 — friction — There is no top-level `README.md`; a cold start lands nowhere

**What I was trying to do.** Start the way a new developer does, at the repo root.

**What the docs said.** n/a — that is the finding.

**What actually happened.** `candace/pkg/gotth/` contains **no `README.md` and no
top-level markdown at all**. A reader who clones has to guess that `docs/` exists
and that `docs/README.md` is the index. Every "start here" path in the project
assumes you already know the entry point. (The gate brief anticipated a top-level
README the docs point at; there is none to point at.)

**Where to change it:** new `gotth-live/README.md` — a short orientation that
links `docs/quickstart.md` and `docs/README.md`.

---

### F-6 — friction — The reference row for per-symbol contracts sends the reader into `live/doc.go`

**What I was trying to do.** Check the authoritative description of a symbol.

**What the docs said.** `docs/README.md:50`, the Reference table's Godoc row:
*"`go doc github.com/candacelabs/candace/pkg/gotth/live` ... The package
overview in `live/doc.go` is the shortest honest description of the concurrency
and delivery contracts."*

**What actually happened.** With v0.1 unpublished there is no pkg.go.dev, so
"read the godoc" resolves to reading source. I did **not** follow it — I did not
need it, which is why this is friction and not a breach. But the index names the
single best description of the concurrency and delivery contracts and then puts
it somewhere a docs-only reader cannot go. `docs/api-surface.md` is excellent and
should carry that weight, or the `doc.go` overview should be mirrored into
`docs/`.

**Where to change it:** `docs/README.md` Reference table.

---

### F-7 — cosmetic — "templ, only if you edit a `.templ` file" undersells what the quickstart itself requires

`docs/quickstart.md:23` frames templ as conditional, and §4 repeats it
(`templ generate  # only if you wrote view.templ yourself`). But §3 *has the
reader write `view.templ`*, so on the quickstart's own happy path templ is always
required. The escape it offers — *"If you copy the generated file from this
repository"* — never says where that file is (`docs/guide/_samples/quickstart/`).

**Where to change it:** `docs/quickstart.md` §"Before you start" and §4.

---

### F-8 — cosmetic — The `/` catch-all is presented without noting it is a catch-all

`http.Handle("/", templ.Handler(Page(State{})))` serves the full HTML page for
every unmatched path — I observed `GET /favicon.ico` → `200 text/html`. Harmless
here, and it is idiomatic `net/http`, but it is the mechanism that makes F-1
silent, so it is worth one clause.

**Where to change it:** `docs/quickstart.md` §2, same sentence as F-1.

---

## 5. The handoff's named stumbles, answered one by one

### 5.1 The quickstart's `templ.Handler` fixed-zero-state pattern
**A real hazard, confirmed by experiment — see F-4.** It is correct for the
quickstart only because `Init` also returns the zero value, and nothing on the
page says that is why. Changing `Init` to `State{N: 41}` makes every
server-rendered response wrong on the wire. The governing rule exists but is on
`guide/fragments-and-dirty-tracking.md`, linked under a performance promise.

### 5.2 The two `http.Handle` lines
**Undocumented, load-bearing, and the documented diagnostic is unreachable — see
F-1 and F-2.** Deleting the subtree line yields `200 text/html` for the runtime
(never the `404` the docs tell you to look for), a `null` status attribute, zero
WebSocket attempts, and a bare `SyntaxError: Unexpected token '<'` in the console
the docs tell you not to look at. Deleting the exact-path line, or using the
`StripPrefix` form the prose implies, yields a `307` on the upgrade and a page
that reconnects forever — with `307` absent from the handshake table.

### 5.3 Do the docs tell you how to run `templ generate`, and what you need installed?
**Yes, and this is the quickstart's strongest section.** §"Before you start" is
explicit and correct: Go 1.25+; *"Nothing else. No node, no npm, no bundler, no
CDN, no protoc"*; and templ with a pinned install line,
`go install github.com/a-h/templ/cmd/templ@v0.3.1020`. §4 gives
`templ generate` as a step. **I verified the "nothing else" claim holds** — the
build needed no node and no protoc, and the committed generated code meant no
generator was required for the library itself. Only two wrinkles, both minor:
the "only if you edit a `.templ` file" framing (F-7), and — the real gap — the
templ file's imports (F-3). A reader on a bare host is told exactly what to
install, and the instruction is accurate.

### 5.4 Do the docs tell you what `live.Config.Dev` does?
**Yes, clearly, and in more than one place — no finding.** `docs/quickstart.md`
covers it twice: the `Config` table (line 121, *"Puts the panic value and its
stack into the error frame a contained panic produces. **Must be false in
production.**"*) and a dedicated paragraph at 167-170 that distinguishes it from
the four escape hatches. `docs/api-surface.md:100` repeats it,
`guide/error-handling.md:170-172` gives the behavioural detail,
`guide/inspector.md` shows it gating the inspector. This one is well done.

### 5.5 Does `docs/README.md` lead somewhere useful from a cold start?
**Yes — the index itself is good; the problem is reaching it (F-5).** Once open,
it is genuinely well built: every row is phrased as a capability
("What you can do at the end of it") rather than a topic name, the v0.1 caveat is
stated up front including that some `livetest` helpers are ledgered but not
implemented, "Start here" points at exactly one page, and the design record is
fenced off as "none of it is needed to build an application." I checked the guide
links resolve — all ten `guide/*.md` targets exist, including
`when-not-to-use-this.md`. Two blemishes: the Godoc row sends a docs-only reader
into source (F-6), and there is no top-level README pointing at it, so a cold
start never finds it (F-5).

---

## 6. Verdict

### Gate box — "QA-1 builds a small, working app from the docs alone": **PASS**

Reasoning, stated so it can be checked rather than trusted:

- I built the app from `docs/quickstart.md` alone. **The docs stranded me zero
  times.** I opened no library source, no example, no test, no `git log`. §4 has
  no breach entries because there were none.
- It compiled on the **first attempt with zero errors**, and worked end to end in
  real chromium under real trusted mouse input.
- All five of the quickstart's own verification steps passed, in its own order.
- The findings are **friction, not blockers**. F-1, F-2 and F-4 are serious and
  two of them concern documented diagnostics that are wrong — but every one of
  them costs a reader who *deviates from* or *builds on* the quickstart, not a
  reader who copies it. F-3 is the only defect on the happy path, and it is
  recoverable from information on the same page.

**No named blocker exists, so I will not manufacture one.** A PASS with recorded
friction is what this is.

I record the counterweight plainly, because on this project a gate that passes
for the wrong reason is the recurring defect: **this gate measures a document
that is copy-paste-correct, and it does not measure a document that survives
being deviated from.** F-1 and F-2 are both cases where the quickstart's own
troubleshooting text would send a reader the wrong way, and I found them by
deliberately building the wrong variants — not by following the page. A PASS on
"can a reader build the app" is the box that was asked for, and it is met. It is
not evidence that the page diagnoses its own failure modes correctly; measured,
it does not.

### FR-53, ≤15 minutes: **PASS — 2 m 12 s**

Measured wall clock, method in §2, with the caveat in §2 stated rather than
buried: the number is real but I am not a human developer, and what it attests is
that the Go half of the quickstart compiles as printed.

### FR-53, ≤30 lines: **MISS — 46 (27 Go + 19 templ)**

Judged on Go+templ, as PRD §6 and FR-53 fix it. **My independent count at
`452e1e74` reproduces the published 46 exactly**, so I confirm rather than
contradict the expectation. This box stays open. Per §6 it closes only if the app
shrinks, and since 12 of the 27 Go lines are the eight required `Config` fields,
most of the remedy is DEV-1's API surface, not DEV-3's prose. **I did not
consider, and do not propose, any change to the counting rule or the threshold.**

---

## 7. What a fix looks like — DEV-3

Owned by DEV-3 unless noted. Ordered by what a reader hits first.

1. **F-1 + F-8 — `docs/quickstart.md` §2 and §4 step 2.** Add to §2, beside the
   three `http.Handle` lines, a note that the pair is deliberate: the exact-path
   registration serves the WebSocket upgrade at `/live` and the subtree
   registration serves `/live/gotth-live.min.js`, and `http.Handle("/", ...)` is
   a catch-all that will answer any path neither of them claims. Then **rewrite
   the step-2 diagnostic**, which is currently wrong for this app: change *"A
   `404` means the tag and the router disagree"* to key on the **Content-Type**,
   because the catch-all guarantees a `200` and a `404` cannot occur. State the
   real symptom — `200 text/html`, a `data-gotth-status` attribute that is absent
   rather than `connecting`, and `Uncaught SyntaxError: Unexpected token '<'` in
   the console. Soften step 3's *"look at the network tab, not the console"* to
   name the one failure where the console is the only evidence.

2. **F-2 — `docs/quickstart.md` §3 and §4, `docs/api-surface.md` `Script` row.**
   Fix the contradiction: the quickstart mounts **without** `StripPrefix`, so the
   sentence *"your router strips the prefix before the handler sees a request"*
   should be rephrased to say the mount path is whatever prefix the handler is
   reachable at, and that `Script`'s argument must match it **whether or not**
   the router strips. Show the `StripPrefix` form explicitly as the supported
   alternative — with the subtree-and-exact-path pair intact — since it currently
   appears only in a review document. **Add `307` to the handshake table**: "the
   upgrade path is registered only as a subtree, so `ServeMux` redirects; a
   WebSocket cannot follow a redirect."

3. **F-3 — `docs/quickstart.md` §3.** Give `view.templ`'s imports, mirroring what
   line 98 already does for `main.go`: one line naming `strconv` and
   `github.com/candacelabs/candace/pkg/gotth/live`. This is the only
   copy-paste failure on the happy path and it is a one-line fix.

4. **F-4 — `docs/quickstart.md` §2.** One or two sentences on the
   `templ.Handler(Page(State{}))` line: `Page(State{})` is evaluated once at
   startup, so the zero value is frozen into every first paint, and that is
   correct **here only because `Init` returns the zero value too**. Say what to
   do when it does not — render the page per-request from the same state `Init`
   would produce — and link
   `guide/fragments-and-dirty-tracking.md` §"How many fragments" from that
   sentence rather than only from the "Where to go next" table, where it sits
   under a performance promise.

5. **F-5 — new `gotth-live/README.md`.** A short orientation page: what the
   library is, the `go build` prerequisites, and links to `docs/quickstart.md`
   and `docs/README.md`. Nothing else — the docs tree is already good once found.

6. **F-6 — `docs/README.md` Reference table.** Either point the Godoc row at
   `api-surface.md` as the docs-reachable authority, or mirror `live/doc.go`'s
   package overview into `docs/`, so the "shortest honest description of the
   concurrency and delivery contracts" is reachable without a source checkout.

7. **F-7 — `docs/quickstart.md` §"Before you start" and §4.** Say that following
   §3 as written means writing `view.templ`, so templ **is** required on the
   quickstart path; keep the escape but name the location of the pre-generated
   file (`docs/guide/_samples/quickstart/`).

**Not DEV-3's, recorded for routing:** the ≤30-line miss is an API-surface
finding. Twelve of 27 Go lines are the eight required `Config` fields; no docs
edit closes that box.

---

## 8. Remediation — DEV-3, 2026-08-05

*Appended by DEV-3. **Nothing above this line was edited**: §§1–7 are QA-1's
record and the verdict, the numbers and the FR-53 miss stand exactly as filed.
This section is what changed in response, finding by finding, and what was
declined.*

**Method note, because this file's own standard demands one.** Every "measured"
below was re-run by DEV-3 rather than copied from §4. The apps were built in
`/tmp/dev3`, outside the repository, against this worktree through a `replace`
directive, in `dis-gotth-live:latest` (Go 1.26) for the `curl` probes and
`dis-gotth-live-bench:latest` (node 24 + chromium) for the browser ones. The
browser runs drive headless chromium over CDP with `Input.dispatchMouseEvent`
press/release pairs at the button's measured centre — the same discipline §2
used, for the same reason. **All of QA-1's results reproduced**; where a number
below differs from §4 it is because it is a different probe, not a different
answer.

| Finding | Severity | Status |
|---|---|---|
| F-1 | friction (high) | **Fixed**, and verified by rebuilding the failing variant |
| F-2 | friction (high) | **Fixed**, and one claim in it strengthened by measurement — see below |
| F-3 | friction | **Fixed**, and the fix verified by compiling the page's own blocks |
| F-4 | friction (high) | **Documented and given a compiled, measured fix.** The API that invites the mistake is unchanged and routed to DEV-2 |
| F-5 | friction | **Fixed** — `gotth-live/README.md` now exists |
| F-6 | friction | **Fixed** |
| F-7 | cosmetic | **Fixed** |
| F-8 | cosmetic | **Fixed**, in the same sentence as F-1 as suggested |

---

### F-1 + F-8 — the `http.Handle` pair, and the diagnostic that could not fire

**Changed:** `docs/quickstart.md` §2 gains **"The three routes, and why the live
handler is registered twice"** — a table saying what each of the three
registrations serves, including that `http.Handle("/", …)` is a **catch-all**
that answers `/favicon.ico` and everything else unclaimed (F-8), and the
measured before/after of deleting the subtree line. §4 step 2 is rewritten from
*"a `404` means the tag and the router disagree"* to **"The runtime loads, and
it is JavaScript … read the type, not the status"**, naming the real symptom:
`200` with `Content-Type: text/html` and the page's own length. §4's *"look at
the network tab, not the console"* is softened to *"look at the network tab
first"* and followed by a paragraph naming this as the one failure where the
console holds the only evidence, with `data-gotth-status` being **absent**
rather than stuck as the tell.

**Verified by building it, not by reading it.** The subtree registration was
deleted and the app driven in chromium; then the same app was probed with the
exact command §4 step 2 tells a reader to run:

```
curl -sI http://127.0.0.1:8080/live/gotth-live.min.js

  as written                      subtree line deleted
  HTTP/1.1 200 OK                 HTTP/1.1 200 OK
  Content-Length: 10391           Content-Type: text/html; charset=utf-8
  Content-Type: text/javascript   Content-Length: 301
```

Browser, same two builds: `status: "live"` / click `0→1` / clean console,
against `status: null` / click inert at `0` / one
`Uncaught SyntaxError: Unexpected token '<'`. **QA-1's F-1 reproduces exactly**,
and the page now tells the reader what they will actually see.

### F-2 — "your router strips the prefix", and the missing `307`

**Changed:** the sentence is gone. `live.Script`'s bullet in §3 now says its
argument is *the prefix the live handler is reachable at **as the browser sees
it***, and explains the real reason no check can catch a mismatch: the tag
renders on the page request and the handler sees the upgrade on another. §2
carries a compiled `Routes` sample showing the two patterns on a mux of the
reader's own at any prefix, and the handshake table in §4 gains a **`307` or
`301`** row keyed on the `Location` it carries.

**Where this went further than the finding asked, and why.** §7 item 2 asked
for the `StripPrefix` form to be shown "as the supported alternative". Measured,
it is not one. Four mountings of the same application, `curl`, Go 1.26:

| Mounting | upgrade `GET <mount>` | runtime `GET <mount>/gotth-live.min.js` |
|---|---|---|
| both patterns, unstripped, at `/live` | `101` | `200 text/javascript`, 10,391 B |
| both patterns, unstripped, at `/app/ui` | `101` | `200 text/javascript`, 10,391 B |
| subtree only, `http.StripPrefix` | **`307`** → `/live/` | `200 text/javascript` |
| both patterns, `http.StripPrefix` on each | **`307`** → **`/`** | `200 text/javascript` |

Row 3 is QA-1's F-2 reproduced. **Row 4 is new, and it is the finding's tail:**
the repair a reader reaches for after the `307` — registering the exact pattern
too, still stripped — is *worse*. Stripping the mount path from the exact
pattern leaves the empty path, the live handler's own mux redirects that to
`/`, and the upgrade lands on the reader's HTML page. So the page now says
`StripPrefix` is **not needed at any prefix** — the handler routes by path
suffix, which row 2 demonstrates — rather than presenting it as an alternative.
That matches what `test/routers` already asserts under net/http, chi and gin.

### F-3 — `view.templ`'s imports

**Changed:** rather than a prose line mirroring line 98, **both** blocks now
carry their `package` and `import` lines, and the sentence introducing §2 says
so: *"Two files, both `package main`, in one directory … Each block below is
complete — imports included — and compiles as printed."* The §2 prose line
naming five imports is gone, being redundant.

**Verified the strong way.** The two fenced blocks were extracted from
`docs/quickstart.md` **by script**, written to an empty directory with nothing
else but a `go.mod`, and built: `templ generate` and `go build ./...` both
succeed. The binary was then driven in chromium — first paint `0`, status
`live`, click → `1`, no console errors. **The page's own printed bytes now
compile and run with nothing supplied from knowledge.**

**FR-53 is unaffected and the count does not move.** `package` and `import`
lines are excluded by FR-53's counting rule, so showing them changes no number:
the application is still **27 Go + 19 templ = 46**, and the ≤30 box stays open
exactly as §6 records. No code was moved out of the app into a helper.

### F-4 — the frozen first paint

**Changed, in three places.**

1. `docs/quickstart.md` §2 gains **"The first paint is rendered once, at
   start-up"**: `templ.Handler(Page(State{}))` builds the component when `main`
   runs; those bytes serve every visitor for the life of the process; that is
   correct **here only because `Init` returns the zero value too**; and the
   measured consequence when it does not.
2. The fix is given, compiled, on the same page: one `Load`, called by both the
   per-request page handler and `Init`.
3. `docs/guide/fragments-and-dirty-tracking.md` gains
   **"The state the page renders is the state `Init` returns"** under
   "How many fragments", and §2 links it **by anchor from the correctness
   sentence** — not only from "Where to go next", where §4 correctly observed it
   sat under a performance promise.

**Verified by running both halves.** `Init` returning `State{N: 41}` with the
quickstart's own page line: every response carries `<output>0</output>` —
QA-1's F-4, reproduced. The same application with the per-request handler:
first paint **41**, still **41** after `data-gotth-status` reaches `live` (so
there is no rewrite, which was the point), **42** after one trusted click, clean
console.

**What was not done, and why.** The library was not changed. `templ.Handler`
is templ's, and the shape that invites the mistake is `Page(State{})` being a
value rather than a function — a docs-owned page cannot fix that, and this turn
was scoped to `docs/**`. Routed to DEV-2 in the handoff, with a concrete
proposal, rather than left as a comment.

### F-5 — no top-level README

**Changed:** `gotth-live/README.md` now exists. What the library is in a
paragraph; the counter's `main()` and its one templ fragment; what it costs —
10,391 bytes minified and **4,429 gzip -9 against NFR-2's 12,288** (measured by
`tools/minify`, method in `client/SIZE.md`), no npm anywhere a consumer goes,
one WebSocket and one goroutine per tab, at-most-once events, one round trip per
interaction; `guide/when-not-to-use-this.md` linked as a page rather than a
disclaimer; and links to `docs/README.md`, the quickstart, `api-surface.md` and
the examples.

**It states FR-53's miss in its own voice** — 46 against 30, twelve of the 27 Go
lines being required `Config` fields — and points at this file. A front door
that quoted only the passing number would be the failure this project keeps
catching.

**Its two code blocks are drift-checked.** `../../../README.md` joins the
samples suite's `docPages`, so the first code a reader ever sees is held to the
same rule as the guide. Confirmed by breaking one line of the README's `main()`
block on purpose: the suite fails with *"these lines are in the documentation
and not in quickstart/main.go"*, naming `../../../README.md:25`. Reverted.

### F-6 — the Godoc row sent a docs-only reader into source

**Changed:** `docs/README.md`'s Reference table no longer names `live/doc.go` as
the place to read the contracts. It says plainly that v0.1 is unpublished, so
that row is a source checkout rather than a page, and names the three
docs-reachable authorities instead: `api-surface.md` for symbols, `protocol.md`
for delivery and close codes, and the quickstart's own "What actually happened"
for the concurrency, purity, delivery and session-lifetime contracts.

**`live/doc.go` was not mirrored into `docs/`.** A second copy of a package
overview is a two-copy equality invariant that nothing checks, which is the
defect class this repository has already paid for once. The four contracts it
states are in `docs/` already; the row now points at them.

### F-7 — "templ, only if you edit a `.templ` file"

**Changed:** §"Before you start" now reads *"**`templ`. §3 has you write
`view.templ`, so on this page it is required.**"* and names the location of the
pre-generated file (`docs/guide/_samples/quickstart/`) as one of two ways out,
each labelled as not the path this page takes. §4's step comment changes from
`# only if you wrote view.templ yourself` to `# §3 had you write view.templ, so
this step is required`. The distinction §"Before you start" was reaching for is
kept and stated: the **library** needs no generator on a clean clone; your own
`.templ` files do.

---

### What else changed, that no finding asked for

- **`docs/guide/_samples/mounting`** is a new sample package: the `Routes`
  helper and the per-request first-paint pair, with **9 Ginkgo v2 + Gomega
  specs** pinning each claim this remediation makes — the upgrade reaches the
  handler with no redirect, the catch-all answers unclaimed paths, both stripped
  mounts redirect, the page handler re-reads state per request, and the
  once-built handler is frozen at start-up. No mocks: the collaborators are
  `net/http`'s router and templ's handler, and substituting either would test
  the substitute. Prose that is only prose is how F-1 and F-2 happened.
- **Dev reload gets one line in §4**, where the reader is running the app, since
  FR-57 landed after this gate was taken. `docs/README.md`'s index already lists
  all eleven guide pages; that was re-checked, not assumed.
- **`when-not-to-use-this.md` joins the quickstart's "Where to go next" table.**
  It was the one guide page reachable from the index and not from the page a
  reader actually starts on.

### For DEV-2 / DEV-1, not fixable in `docs/**`

1. **F-4's real fix is an API one.** `templ.Handler(Page(State{}))` is a value
   frozen at registration. A `live`-owned page handler that takes the same
   loader `Init` takes — one that cannot be given a state value, only a way to
   get one — would make the mistake unwritable rather than documented.
2. **`live/example_test.go:151` still says "router strips the prefix before the
   handler sees a request".** Same sentence as F-2's, in library test code.
3. **`docs/api-surface.md:665`** repeats it inside the C-23 changelog row. It is
   a historical record of a ruling's reasoning, so the call belongs to that
   file's owner, but a reader who greps will find the claim stated as fact.
4. **`live/doc.go`'s "# Status" section says "The examples … are not here yet".**
   Three examples exist and ship. Not a finding of this gate; noticed while
   answering F-6.
