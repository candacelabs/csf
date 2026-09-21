# gotth-live documentation

gotth-live serves server-driven live user interfaces from Go: your state and
your rendering stay in the Go process, the browser holds one WebSocket per tab,
and interactions travel up as events while re-rendered HTML fragments travel
back down, where a morph applies them to the DOM in place. You write a pure
reducer, some templ fragments, and no JavaScript — the client runtime is
compiled into your binary and served by the same handler that serves the
connection, so there is no CDN, no npm, and no build step. **It is v0.1: the
API makes no compatibility commitment yet, several `livetest` helpers are
ledgered but not implemented, and the pages below say so where it matters.**

---

This is the index. The one-paragraph version of it, and what the library costs,
is [`../README.md`](../README.md).

---

## Start here

| Page | What you can do at the end of it |
|---|---|
| [quickstart.md](quickstart.md) | Have a live page running: 20 lines of Go, 11 of templ, and a verification checklist that fails distinguishably at each step. |

## Guide

One concern per page, in the order most people need them.

| Page | What you can do at the end of it |
|---|---|
| [guide/architecture.md](guide/architecture.md) | Say which goroutine any line you write runs on, what is queued behind it, what happens when that queue fills, and what one session costs — the runtime model as the code runs it, not as it was designed. |
| [guide/events-and-forms.md](guide/events-and-forms.md) | Bind clicks, forms, per-field input, and single keys; read form values, including telling an unchecked checkbox from an empty one. |
| [guide/fragments-and-dirty-tracking.md](guide/fragments-and-dirty-tracking.md) | Split a page into independent live regions and declare what changed each one — without the `==` comparability trap or the `time.Time` hazard. |
| [guide/effects-and-server-push.md](guide/effects-and-server-push.md) | Do I/O, share state between sessions, push a change into a browser that did not ask, classify a failure as retryable or not, and key an effect that moves money so it moves it once. |
| [guide/lifecycle-hooks.md](guide/lifecycle-hooks.md) | Subscribe at mount, authorize each event, release at teardown — and know why there is no patch hook. |
| [guide/observability.md](guide/observability.md) | Turn on metrics, traces and the provenance log with three fields, and answer "where did this patch come from" as a log query. |
| [guide/htmx-interop.md](guide/htmx-interop.md) | Put HTMX on the same page as live regions, in either of the two places it can safely go. |
| [guide/inspector.md](guide/inspector.md) | Open a panel that shows the causal chain of the session in front of you — every event, the state version it moved the server to, the patches it produced — and know why it cannot ship to production by accident. |
| [guide/dev-reload.md](guide/dev-reload.md) | Save a `.go` or `.templ` file and watch the open page reload itself — and know exactly what a restart does not preserve, why a rebuild that changed nothing reloads nothing, and which three gates keep it out of production. |
| [guide/testing-your-app.md](guide/testing-your-app.md) | Hold the reducer to determinism, catch a stale region before merge, and unit-test the hooks that take a `Session`. |
| [guide/error-handling.md](guide/error-handling.md) | Diagnose a config that will not start, a connection that closes, an event that is refused, and an effect that fails — and put the log line where a reducer is still a pure function, which is not where this page used to put it. |
| [guide/security.md](guide/security.md) | Write an origin allowlist a browser can actually match — including the bind-address spelling that already cost this project a live defect, and the one that is still open — send a CSP the runtime is measured under, put a rule where it can be both enforced and *seen*, and verify the dev routes are off rather than assume it. |
| [guide/deploying.md](guide/deploying.md) | Ship it: what the binary contains, what the proxy has to be told about idle timeouts and upgrade headers, the shutdown order that drains sessions instead of dropping them, and what a second replica does and does not break. |
| [guide/when-not-to-use-this.md](guide/when-not-to-use-this.md) | Decide **against** this library on evidence: the at-most-once bound, the interactions that are not buildable, the per-session cost of shared data, and the gaps the benchmark records as Next.js wins. |

Every Go and templ block in those pages is extracted from
[`guide/_samples`](guide/_samples), a separate Go module whose suite asserts
that each sample builds **and** that each block still matches the file it came
from. A page cannot rot into code that no longer compiles without a red test.

## Reference

| Page | What is in it |
|---|---|
| [api-surface.md](api-surface.md) | Every exported symbol, what it is for, whether it is `stable` or `experimental`, and a changelog of surface changes. The authoritative list. |
| [instrumentation.md](instrumentation.md) | The full metric, span and log catalogue, the cardinality rules, the provenance log's format, and how each signal is audited against an independent measurement. |
| [protocol.md](protocol.md) | The wire protocol: the schema, causal identity, the close codes, the client codec contract, and the generated `Validate*` boundaries. The runtime, annotation schema, and generator come from `pkg/liquidproto`, a sibling package in the same Go 1.26 module. |
| Godoc | Per-symbol detail, from a checkout: `go doc github.com/candacelabs/csf/pkg/gotth/live`. Every exported symbol carries a doc comment saying what it does and, where it is not obvious, what it guarantees and from which goroutine it is safe to call. **v0.1 is unpublished, so there is no pkg.go.dev and this row is a source checkout rather than a page** — nothing in this tree depends on it. The contracts are stated here too: [api-surface.md](api-surface.md) is the authoritative symbol list, [protocol.md](protocol.md) has the delivery guarantees and the close codes, and [quickstart.md §What actually happened](quickstart.md#what-actually-happened) states the concurrency, purity, delivery and session-lifetime contracts in four paragraphs. |

## Examples

Three complete applications, packages of this same module, each with its own
README and suite.

| Example | Shows |
|---|---|
| [`examples/gotth/counter`](../../../examples/gotth/counter/README.md) | The smallest complete application, with the value shared across every tab, and a walkthrough of the provenance log from a click to the markup it produced. |
| [`examples/gotth/chat`](../../../examples/gotth/chat) | A form, per-field validation, a long-lived pubsub subscription, and two identities in one room. |
| [`examples/gotth/dashboard`](../../../examples/gotth/dashboard) | Several independent live regions under a fast feed, an HTMX island, and the backpressure story. |

`git clone && go run .` in any of them works with no node, npm, protoc or
generator installed: the generated code is committed.

## For the curious

The design record. None of it is needed to build an application, and all of it
argues rather than instructs.

| Document | What it decides |
|---|---|
| [rfc/001-architecture.md](rfc/001-architecture.md) | The session actor, the render and patch pipeline, backpressure, the HTMX ownership boundary, the client size budget — **why** each went the way it did, argued against the alternatives, with the measurement campaign behind the memory budget. **What** the runtime does today is [guide/architecture.md](guide/architecture.md), which is the page to build from; this is the record of the arguments. |
| [adr/001-transport.md](adr/001-transport.md) | Why WebSocket rather than SSE or long-polling. |
| [rfc/0000-prior-art-teardown.md](rfc/0000-prior-art-teardown.md) | What LiveView, Live components and the rest get right and wrong, in detail. |
| [PRD.md](PRD.md) | The requirements every page here is answerable to, and the phase gates. |
| [dependencies.md](dependencies.md) | Every dependency, what it buys, and what an in-house alternative would cost. |
| [review-checklist.md](review-checklist.md) | The bar a change is held to. |

Friction found while building the examples, and what was done about it, is in
each example's `FRICTION.md` — those files are where a gap between what the
library offers and what an application needs gets written down before it gets
argued about.

## The record

The gate reports in [`gates/`](gates/) are where this project grades itself
against the PRD's own exit criteria, box by box, with every box that did **not**
tick left visible and given an owner. They are the answer to "is any of the
above actually true", and they are indexed here because a set of documents that
records its own failures and then does not link to them has hidden them.

| Report | What it settles |
|---|---|
| [gates/phase-0.md](gates/phase-0.md) | The design package is approved for implementation. No product code existed yet, by design. |
| [gates/checkpoint-2.md](gates/checkpoint-2.md) | The component model, closed with carried debt — and the defect that made a CI suite green by never running it. |
| [gates/checkpoint-3.md](gates/checkpoint-3.md) | Resilience, closed with carried debt. **Phase 3 EXITS** — seventeen of seventeen, on the re-held gate act in its §12 (2026-08-05); the one criterion it left open was re-measured by DEV-3 and the box re-held by PM-1, who ran the measurement rather than reading the commit. It also closes checkpoint 1, which had never had a record. |
| [gates/phase-4.md](gates/phase-4.md) | **The current state of this tree.** Thirteen exit criteria, **twelve met, one open** — FR-54's templ helper set, whose failures 2 and 3 are fixed and whose failure 1 is *decided and not yet built*. Read this one first if you want to know what is not finished, and read its §6 for every carried item with an owner. *(This row read "six met, seven open" from the day it was written until 2026-08-05, revision 5, when it was six boxes stale — in the direction that under-reports. The report it indexes has a section, §7.2, about exactly that defect in the PR body, and the index row carrying it was PM-1's own.)* |
