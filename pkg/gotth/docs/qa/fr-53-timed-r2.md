# FR-53 timed docs-alone run, round 2

**Date:** 2026-08-05
**Commit:** `679e669507bbf5018204c00acab3a97261957905`
**Held by:** QA-1 (fresh agent, no prior exposure to this library)
**Why re-held:** the quickstart's view was materially rewritten — a hand-written
13-line HTML shell became a 5-line `app.Document` call — so the round-1 timing no
longer covered the page a reader now follows.

**The requirement.** *"A developer following the quickstart from zero MUST reach a
working, live counter in ≤15 minutes and ≤31 lines of application code. Measured
by QA-1 with a timer, from docs alone, without reading library source."*

---

## 1. Elapsed wall-clock time and milestones

| Timestamp | Milestone |
|---|---|
| `2026-08-05T17:26:44+00:00` | **Timer start.** Commit recorded, scratch dir `/tmp/fr53-r2` made. Nothing opened yet. |
| `2026-08-05T17:27:02+00:00` | Finished reading `docs/quickstart.md` end to end. No other document opened, then or later. |
| `2026-08-05T17:27:25+00:00` | `main.go` and `view.templ` written, copied from §2 and §3 as printed. |
| `2026-08-05T17:27:41+00:00` | §1 done: `go mod init`, the two `require`s, the `replace` onto the checkout. |
| `2026-08-05T17:27:50+00:00` | **First `go run .` — FAILED.** `missing go.sum entry`, 9 errors. See friction #2. |
| `2026-08-05T17:28:05+00:00` | `go mod tidy` then `go build` — clean, exit 0. First successful compile. |
| `2026-08-05T17:29:13+00:00` | **Working live counter observed in headless chromium.** `0 → 1 → 2 → 3` on three clicks, `data-gotth-status="live"`, no navigation. **Timer stop.** |

**Elapsed: 2 minutes 29 seconds** (149 s), start to observed live counter.

### What that number is and is not

This run was held by an AI agent, not a human developer. 2m29s is a **floor**, not a
prediction of a human's time: it excludes reading speed, typing speed, and the pauses
a person takes to decide whether they believe a paragraph. What it does measure
honestly is the thing the clause is really about — **how many times the documented
path stops you.** It stopped once (friction #2), for about fifteen seconds, at a
failure whose error text names its own fix. There is no second stop. A human working
from this page has one recoverable snag between `mkdir` and a clicking counter, and
the 12.5-minute margin against the limit is not close.

Work after 17:29:13 (the origin probe in friction #4, the line-count arithmetic in
friction #6) is **off the clock** and marked as such. It is supplementary evidence,
not part of the measured path.

---

## 2. Line count

Counted by the quickstart's own stated rule — *"every line that is not blank, not a
comment, and not a `package` or `import` line"* — across every application file I
authored.

```bash
cd /tmp/fr53-r2/counter && for f in main.go view.templ; do
  awk 'BEGIN{c=0;imp=0}
    /^import[ \t]*\(/{imp=1;next}
    imp && /^\)[ \t]*$/{imp=0;next}
    imp{next}
    /^[ \t]*$/{next}
    /^[ \t]*\/\//{next}
    /^package[ \t]/{next}
    /^import[ \t]/{next}
    {c++}
    END{printf "%-12s %d\n", FILENAME, c}' "$f"
done
```

| File | Lines |
|---|---|
| `main.go` | **20** |
| `view.templ` | **11** |
| **Total** | **31** |

Raw size for reference: 75 physical lines across the two files (`wc -l`), of which 44
are blank, comment, `package` or `import`.

**Not counted, and why.** `go.mod` (15 non-blank lines) is excluded: the quickstart's
rule scopes itself to "the two files below", and every line of `go.mod` was produced
by `go mod init`, `go mod edit` and `go mod tidy` rather than authored. If a future
grader decides `go.mod` counts, the clause fails by 15 — that decision should be
written into the rule rather than left to the grader. `drive.mjs` (77 lines), the
headless-chromium driver in §4 below, is QA instrumentation and not application code:
no reader of the quickstart writes one, because §4 tells them to open a browser.

**Anchoring disclosure.** The quickstart states its own answer — "20 lines of Go and
11 lines of templ markup… That is 31" — in its **first paragraph**, before any reader
can reach the code. So my count was not blind; I had seen 20/11/31 before I counted.
I mitigated by counting mechanically with the `awk` above rather than by eye, and by
deriving the per-file split independently, but the exposure is unavoidable on this
page and is recorded rather than claimed away. A genuinely blind recount would need
the opening paragraph withheld.

---

## 3. Verdicts, clause by clause

| Clause | Verdict | Measured |
|---|---|---|
| **≤15 minutes** | **PASS** | 2 min 29 s. One stop, ~15 s, self-diagnosing. Margin: 12.5 min. |
| **≤31 lines of application code** | **PASS** | Exactly 31. Margin: **zero.** |

**The line clause passes at the boundary with no headroom whatsoever.** 31 against
≤31. One added line of application code — one more `Config` field written out, one
more fragment, a `lang` attribute moved onto its own line — fails this requirement.
Anyone reading this as comfortable is misreading it: it is a pass, and it is a pass
that the next commit to `docs/quickstart.md` can undo without touching a line of
library code. The time clause has room; the line clause has none.

---

## 4. Friction log, in order

### 1. Getting a toolchain — no cost here, unstated in the docs

`Before you start` says "Go 1.25 or newer" and gives the `go install …/templ@v0.3.1020`
line. It says nothing about how to get either, which is correct for a library's
quickstart — but this host has no Go and no node, so I used the project's
`dis-gotth-live:latest` and `dis-gotth-live-bench:latest` images. My brief supplied
them, so the cost to this run was zero. Recorded for completeness, not as a defect.

Minor version note: `go mod init` in the image wrote `go 1.26.5`, and the toolchain is
Go 1.26.5, above the stated floor. No incompatibility surfaced.

### 2. The build does not work from the state §1 leaves you in — the one real stop

**This is the only place the documented path stopped.** §1 shows a `require` block to
put in `go.mod`. §4 says, in full:

```bash
templ generate          # §3 had you write view.templ, so this step is required
go run .
```

`templ generate` succeeded. `go run .` did not:

```
view_templ.go:8:8: missing go.sum entry for module providing package github.com/a-h/templ (imported by example.com/counter); to add:
	go get example.com/counter
view_templ.go:9:8: missing go.sum entry for module providing package github.com/a-h/templ/runtime (imported by example.com/counter); to add:
	go get example.com/counter
/src/internal/obs/metrics.go:9:2: github.com/a-h/templ@v0.3.1020: missing go.sum entry for go.mod file; to add it:
	go mod download github.com/a-h/templ
… (9 errors total)
```

`go mod tidy` fixed it completely and the next `go build` was clean. Cost: ~15 s.

Two things make this worth fixing anyway. First, it is unconditional — it is not a
mistake I made, it is what §1 followed by §4 produces, every time, for every reader.
Second, the Go tool's own suggested remedy in the first two errors is
`go get example.com/counter`, which is the wrong shape of advice (it names the main
module), and the remedy in the other seven, `go mod download github.com/a-h/templ`,
is incomplete. A reader who trusts the error text over their instincts is sent
sideways. Experienced Go developers reach for `go mod tidy` in seconds; a reader new
to modules could lose several minutes here, and this is the single likeliest place a
human run diverges from mine.

**What the docs should say.** §4's block should be three lines, not two:

```bash
go mod tidy             # §1 wrote the requires by hand; this writes go.sum
templ generate
go run .
```

### 3. The two stumbles a previous reviewer pre-flagged — **neither tripped me**

**`templ.Handler` fixed-zero-state pattern: did not trip.** I never reached for it.
§2's code block is complete as printed and already contains
`app.PageHandler(Page)`, so a reader who copies rather than composes cannot make this
mistake. The warning underneath ("The obvious spelling does not do that, and its
failure is silent") is placed *after* the block, which means it functions as
explanation rather than as rescue — I had already copied the right thing before I
read it. That ordering is fine and I would not change it: the section earns its place
by explaining *why* `PageHandler` exists, and its `Init: State{N: 41}` measurement is
the kind of evidence that stops a reader from later "simplifying" it back.

**The `http.Handle` lines: did not trip.** `app.Mux(MountPath, app.PageHandler(Page))`
does all three registrations inside the single `ListenAndServe` line, so I wrote none
of them. The `mounting/mount.go` sample in §2 is unambiguously framed — "This is
exactly what `Mux` does, written out, so you can put the same two patterns on a mux
you already have" — and I correctly read it as an alternative for readers with an
existing mux, not as something to copy. **Neither pre-flagged risk is live on the
page as it now stands**, and the reason is the same in both cases: the §2 code block
is complete and correct, so the failure modes exist only for readers who compose from
prose instead of copying. The prose then catches those readers anyway.

I also did not need `http.StripPrefix`, was never tempted by it, and the four-row
mounting table cost me nothing because the path it warns about is not the path §2's
code takes.

### 4. "403s in the log" is advice the quickstart application cannot follow *(off-clock)*

§4's troubleshooting says: *"A page reconnecting every few seconds with 403s in the
log is almost always the origin allowlist."*

There is no such log. `Config.Logger` is marked not-required and the §2 application
does not set it, so the process writes **nothing at all**. Verified: I drove a full
session plus a deliberately rejected upgrade through the app and `server.log` was
empty, zero bytes, both times.

```
$ curl -si -H 'Origin: http://localhost:8080' … http://127.0.0.1:8080/live
status=403
forbidden origin
$ cat /tmp/server.log
                       # empty — nothing logged, for the whole run
```

The 403 itself is exactly as documented, body and all, so the *diagnosis* in the table
is right — it is the *place to look* that is wrong. This matters because it is the
predictable reader stumble on this page: the allowlist is
`[]string{"http://127.0.0.1:8080"}` and §4 says "Open <http://127.0.0.1:8080>", but a
reader who types `localhost:8080` out of habit gets a page that loads perfectly and a
counter that never goes live, and is then sent to a log that does not exist.

**What the docs should say.** Point at the browser's network tab, which §4 already
does two paragraphs earlier and should do here too — or add `Logger` to the §2
`Config` while debugging. Better still, name the `localhost` vs `127.0.0.1` trap
explicitly next to the 403 row: it is one sentence and it is the most likely way a
reader of this exact page ends up at that row.

### 5. The `426` row is narrower than the behaviour *(off-clock, minor)*

The table glosses `426` as "subprotocol mismatch — the page and the binary are
different versions." My hand-rolled `curl` handshake with a valid, allowlisted
`Origin` and **no** `Sec-WebSocket-Protocol` header at all also got `426`. So `426`
means "no acceptable subprotocol offered", of which version skew is one cause and
"offered none" is another. Nothing a browser-driven reader will hit — the runtime
always offers one — but the row states a cause where it means a condition.

### 6. The counting rule is ambiguous in a way that flips the verdict *(off-clock)*

The rule is *"every line that is not blank, not a comment, and not a `package` or
`import` line."* Both my files use a **parenthesised** import block. The rule does not
say whether the entries inside the parens, and the closing `)`, are "import lines".

It is not a quibble — the two readings give different verdicts:

| Reading | `main.go` | `view.templ` | Total | Against ≤31 |
|---|---|---|---|---|
| The whole `import` declaration is import lines *(intended)* | 20 | 11 | **31** | **PASS**, at the boundary |
| Only lines beginning `import` are import lines *(literal)* | 24 | 14 | **38** | **FAIL by 7** |

The intended reading is obviously the right one and is the one that reproduces the
page's own 20/11 split, so I used it and the clause passes. But a rule whose two
plain readings differ by 7 on a requirement with **zero** margin is a rule that should
be written down more tightly than the grader's good sense.

**What the docs should say.** "…not a `package` line, and not part of an `import`
declaration, including the parenthesised block and its closing paren." And, given §2
above, whether `go.mod` counts.

### 7. Nothing sent me outside the quickstart

Recorded because the gate asks. `docs/quickstart.md` was the **only** document I
opened. It never sent me to `README.md`, `docs/README.md`, or anything under
`docs/guide/**` in order to get the counter working. Its two links into forbidden
territory — `docs/guide/_samples/quickstart/` and `client/SIZE.md §7` — are both
offered as things you do *not* need on this path ("Two ways out, neither of them the
path this page takes"; "you should not need to write one by hand"), and I needed
neither. **The quickstart is self-contained**, which is the strongest single thing
this run found and is worth as much as any of the friction above.

---

## 5. Did I read library source? **No.**

I read exactly one file in this repository: `docs/quickstart.md`. I did not open,
grep, list or `go doc` anything under `live/`, `client/`, `internal/`, `examples/`,
`test/`, `tools/`, `proto/`, `bench/`, or `docs/guide/_samples/`, and no `*_templ.go`
of the library's. I did not read `docs/api-surface.md`, `docs/PRD.md`, `docs/gates/**`,
`docs/reviews/**`, or anything else under `docs/qa/**`. I did not read the round-1
timing record, and I did not look up anyone else's line count — the 20/11/31 in §2 of
this document reached me only through the quickstart's own opening paragraph, as
disclosed above.

Two things to declare rather than leave implicit:

- **The checkout was mounted into the build container**, read-only, at `/src`, so the
  `go mod edit -replace` in §1 could resolve. That is a compile, not a read. The
  library's source was consumed by the Go compiler and never by me.
- **Compiler errors quoted internal paths** — `/src/internal/obs/metrics.go:9:2`,
  `/src/internal/wsx/conn.go:12:2`, and others in friction #2. I read those error
  lines, which the gate permits and expects, and did not open any of the named files.
  Seeing that `internal/wsx/conn.go` exists taught me nothing about the API and
  changed nothing I wrote.

Nothing in this run was blocked for want of source. The two files in §2 and §3 of the
quickstart compiled and ran as printed, once `go mod tidy` had been supplied.

---

## 6. How "working, live counter" was established

Observed, not compiled. Headless chromium in `dis-gotth-live-bench:latest`, driven
over the Chrome DevTools Protocol with Node 24's built-in `WebSocket` and no
dependencies.

**Server-side, §4 steps 1 and 2:**

```
$ curl -s http://127.0.0.1:8080
<!doctype html><html lang="en"><head><meta charset="utf-8"><title>gotth-live quickstart</title><script src="/live/gotth-live.min.js" data-gotth-url="/live" defer></script></head><body><p data-gotth-region="count"><output>0</output> <button data-gotth-on="click:count.inc">+1</button></p></body></html>

$ curl -sI http://127.0.0.1:8080/live/gotth-live.min.js
HTTP/1.1 200 OK
Content-Length: 10391
Content-Type: text/javascript; charset=utf-8
X-Content-Type-Options: nosniff
```

`Content-Type: text/javascript` and `Content-Length: 10391` — the type, not just the
status, and the exact length §4 step 2 predicts.

**In the browser:**

| Probe | Result |
|---|---|
| `data-gotth-status` after load | `"live"` (§4 step 3) |
| `<output>` after load | `0` |
| after click 1 / 2 / 3 | `1` / `2` / `3` (§4 step 4) |
| `performance.getEntriesByType('navigation').length` | `1` before, **`1` after all three clicks** |
| `window.__qaSentinel` set before click 1 | **survived all three clicks** |
| region HTML at the end | `<p data-gotth-region="count"><output>3</output> <button data-gotth-on="click:count.inc">+1</button></p>` |
| browser console | **empty** — no errors, no warnings |
| after `Page.navigate` reload | `0`, sentinel gone (§4 step 5: reload resets, correct with no `Init`) |

The navigation counter staying at 1 and the JS sentinel surviving are what establish
**"without a full page load"**: had the page reloaded, both would have reset. They did
not, and the number still changed. The console being empty also rules out the silent
failure §2 spends a table on — the `Uncaught SyntaxError: Unexpected token '<'` that
a missing subtree registration produces never appeared.

---

## 7. The files I wrote, verbatim

### `main.go` — 20 counted lines

```go
package main

import (
	"log"
	"net/http"

	"github.com/candacelabs/candace/pkg/gotth/live"
)

// MountPath is where the live handler is mounted. It is one constant because
// two places must agree on it: app.Mux below, which routes with it, and the
// app.Document call in view.templ that tells the browser where to fetch the
// runtime and open the connection. Those happen on different requests, so
// nothing in the library can check that agreement.
const MountPath = "/live"

// EventInc is the one event name this application accepts. An event whose name
// is not in Config.Events is refused with UNKNOWN_EVENT before the reducer
// runs.
const EventInc = "count.inc"

type State struct{ N int }

// app is the application, and it is a package-level var rather than a local in
// main so that view.templ can reach it: app.Document renders the page shell.
var app = live.MustNew(live.Config[State]{
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

func main() {
	log.Fatal(http.ListenAndServe("127.0.0.1:8080", app.Mux(MountPath, app.PageHandler(Page))))
}
```

### `view.templ` — 11 counted lines

```templ
package main

// templ itself is not imported here: the generator adds "github.com/a-h/templ"
// to every file it writes, so naming it again is a redeclaration in the
// generated output rather than a missing import.
import (
	"strconv"

	"github.com/candacelabs/candace/pkg/gotth/live"
)

// Count is the one live fragment. Its root element carries live.Region, which
// renders data-gotth-region: morph never touches anything outside a region,
// and a patch names this ID.
templ Count(s State) {
	<p { live.Region("count")... }>
		<output>{ strconv.Itoa(s.N) }</output>
		<button { live.On("click", EventInc)... }>+1</button>
	</p>
}

// Page is the whole document. app.Document writes the doctype, the <html>
// element with the attributes passed to it and none of its own, a <head> with
// the character encoding, the title and the runtime's script tag, and a <body>
// holding whatever is written between its braces — which is the same component
// the fragment renders, from the same state, so the snapshot that arrives over
// the WebSocket morphs the page to bytes it already has.
templ Page(s State) {
	@app.Document(MountPath, "gotth-live quickstart", templ.Attributes{"lang": "en"}) {
		@Count(s)
	}
}
```

Both are byte-identical to the quickstart's §2 and §3 blocks. I changed nothing and
added nothing; the page's claim that each block "is complete — imports included — and
compiles as printed" holds.

### `go.mod` — not counted (see §2)

```text
module example.com/counter

go 1.26.5

require (
	github.com/a-h/templ v0.3.1020
	github.com/candacelabs/candace/pkg/gotth v0.1.0
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/coder/websocket v1.8.15 // indirect
	go.opentelemetry.io/otel v1.45.0 // indirect
	go.opentelemetry.io/otel/metric v1.45.0 // indirect
	go.opentelemetry.io/otel/trace v1.45.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace github.com/candacelabs/candace/pkg/gotth => /src
```

The second `require` block and the version pins were written by `go mod tidy`, not by
me. `/src` is the read-only mount of this checkout inside the build container, standing
in for the `../path/to/gotth-live` §1 tells an unpublished-version reader to use.

---

## 8. Summary for the gate

- **≤15 minutes: PASS** — 2 min 29 s, one ~15 s stop. Reported as a floor, not a human
  estimate; the finding that carries is that the documented path stops **once**.
- **≤31 lines: PASS** — exactly 31, **zero margin**, on a rule whose literal reading
  would give 38.
- **Source read: none.** Quickstart only; compiler errors and browser output only.
- **The counter is live and observed**, not merely compiled: `0 → 1 → 2 → 3` in
  chromium across three clicks with one navigation and a surviving JS sentinel.
- **Worst three frictions:** (1) `go mod tidy` missing from §4, which breaks the
  documented build for every reader; (2) "403s in the log" pointing at a log the
  quickstart application never writes, on the page's most likely reader error;
  (3) the counting rule's silence about parenthesised imports, worth 7 lines against
  a limit with none to spare.
- **Both pre-flagged stumbles — `templ.Handler` and the `http.Handle` lines — did not
  trip this run**, because §2's code block is complete and correct and I copied it.
