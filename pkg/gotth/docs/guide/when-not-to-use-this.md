# When not to use this

At the end of this page you can decide **against** gotth-live on evidence rather
than on taste, and you can tell the difference between a bound that follows from
the design and a gap that a later version could close.

A page that only sells the library would fail its own purpose. PRD §1.3 states
the trade — *spend server RAM, server CPU and one network round trip per
interaction; save the entire client state layer, its build toolchain, and its
class of desync bugs* — and then says the trade is bounded and that the bound
gets stated. This is where it gets stated.

Every claim below is one of three things, and each is labelled: **a consequence
of the design**, **measured**, or **an unmeasured parity claim**. Nothing here is
softened, and nothing is invented to look balanced.

---

## 1. Effects that commit outside the process and cannot be made idempotent

**A consequence of the design, and it is the one that costs money.**

> An application whose externally-committing effects are not idempotent and
> which cannot supply its own idempotency key is an application this library
> does not make safe.

That is PRD **FR-77(c)**, quoted rather than paraphrased. The reasoning, both
double-execution paths, and a worked example on an effect that moves money are
in [effects-and-server-push.md](effects-and-server-push.md#delivery-semantics-and-the-obligation-they-put-on-your-application).
The short version is:

- Events are **at-most-once**. The library does not retry an unacknowledged
  event across a reconnect and does not deduplicate an event it receives twice.
  Two byte-identical `Event` frames are two events: two transitions, two
  `state_version` increments, two effect runs.
- An effect can commit externally while its patch never reaches the browser. The
  session dies with the connection, the user sees a failure that was not one,
  and they retry. **One intent, two executions**, and no delivery semantics
  available to any library fix it, because the commit already happened.
- So the idempotency key lives in **your** domain — an order, a message, a
  transfer — and the library cannot supply it, because it does not know what
  "once" means in your application.

If you are integrating with a third party that has no idempotency key, no
conditional write, and no way to ask "did this already happen", then you own
that risk here exactly as you would anywhere else — but this page is the place
that says so out loud rather than leaving you to find it on a customer's
statement.

## 2. Interactions that need feedback faster than a round trip

**A consequence of the design.** Every interaction is an event to the server and
a re-rendered fragment back. Drag, draw, keystroke-driven canvases, games, and
anything with a sub-frame budget are **not buildable on gotth-live** — not slow,
not buildable. There is no client-side reducer (PRD BL-3) and no optimistic
patch application with rollback (BL-4).

The benchmark's equivalence spec carries this as rows **N-1** and **N-2** in its
"what a Next.js app gets that gotth-live v0.1 does not" table, and it makes them
*measured* rows on purpose: `bench/` ships a client-local `useState` counter and
an optimistic send on the Next.js side with no gotth-live equivalent, and
publishes them in the same table with the same typography. Suppressing them is
the strawman the PRD forbids.

## 3. Offline, intermittent, or high-latency connections

**A consequence of the design; the mobile profile is not yet measured.**

- **Offline is excluded** (BL-2). There is no client-side event queue and no
  replay on reconnect — that would require client-side state, which is the thing
  this library exists to delete. A tunnel, a lift, or a flaky radio degrades a
  gotth-live page to unusable where an app with a client store queues and
  reconciles.
- **A send during a network failure is lost, silently to the application.** The
  client's `send()` returns false when the socket is not `OPEN`; there is no
  pending buffer. The user sees server truth after resync and can act again.
- **High RTT is felt on every interaction**, not on some of them. The
  equivalence spec's row **N-10** says a high-RTT user gets a materially worse
  experience, and its mobile profile has not been run yet, so treat that as
  stated-and-unquantified rather than as a number.

If your users are mobile-first on unreliable networks, this is the wrong tool
and the PRD says so in §1.3 rather than only here.

## 4. Anything that needs to scale out across processes

**A consequence of the design, and it is load-bearing rather than incidental.**

One process owns one session. The actor model, the resync story, and the failure
modes all assume it. Multi-node — session migration, cross-node pubsub,
sticky-session routing — is BL-1, and PRD risk **R-14** records the consequence
in plain terms: *any later multi-node work is a redesign, not an extension*, and
that is accepted deliberately rather than deferred quietly.

Practically: capacity is a vertical question here and a horizontal one on a
stateless stack. If "add another instance behind the load balancer" is your
capacity plan, that plan does not apply to gotth-live v0.1.

There is also no durable session state across a process restart (BL-8). A deploy
disconnects every session; each browser reconnects and gets a fresh `Snapshot`,
which is the same path a network blip takes and is therefore continuously
exercised — but any in-memory state your `Init` cannot rebuild from your own
store is gone.

## 5. Per-session memory, and the shared-data duplication that comes with it

**Partly measured, partly not, and the honest split matters.**

The per-idle-connection budget is a gate: **≤46,080 B (45 KiB)** at 1k idle
sessions, PRD goal **G2**. The measured baseline lives in
[`docs/bench/g2-baseline.md`](../bench/g2-baseline.md) and is deliberately not
copied here, because a moving measurement with two homes is how a stale figure
gets quoted. What you need to know before quoting anything from it: the shipping
tree measures **at** that gate rather than clear of it, and the equivalence
spec's driver-validation gate — 10 real browser tabs against 10 synthetic
sessions, mandatory before any 1,000-session figure is quoted — **has never been
run**, so every 1k figure in that document is, in its own words, an assertion
about a synthetic client rather than about sessions.

Separately, and this one is a property of today's API rather than of the
implementation:

**Every session folds its own copy of the shared data it renders.** The bench
chat app keeps all three rooms' logs per session and the bench dashboard keeps
its 200 rows per session, where the equivalent Next.js stores keep **one array**
and derive per-session views from it. The cause is `live.Event.Fields` being
`map[string]string`: an effect cannot hand a session a pointer to a shared
immutable value, and a reducer that reached into a shared store for one would
not be a pure function of `(state, event)`. `bench/README.md` records this as
deviation **G-3** and says the quiet part — it is *a real per-session memory
cost*, it is scheduled to be measured and **has not been measured yet**, and it
follows from an API decision rather than from an oversight.

So: if your live pages are large, shared, and mostly identical across many
concurrent sessions — one big feed watched by thousands — model the memory
before you commit, and do not assume the per-connection budget above covers your
application state. It covers the library's.

## 6. Feature gaps the benchmark records as Next.js wins

**Measured, in the sense that a harness row exists for each and the gotth-live
column reads "no equivalent" rather than a blank.**

| Gap | What it means for you | Where it is recorded |
|---|---|---|
| **Optimistic send** | A send feels like the link, not instant. There is no optimistic UI on this stack by construction, so the bench row is `nextOnly` and skipped here | AS-2 / BL-4; equivalence spec **N-2** |
| **Client-local-only state** | A `useState` counter that never reaches the server has no gotth-live form. The bench ships the Next.js one and reports the absence | §2.2 / BL-3; equivalence spec **N-1** |

**This table had a third row until 2026-08-05, and losing one is worth more to
you than the two that remain.** It is reproduced here rather than deleted,
because a page that quietly drops the thing it told you it could not do is a
page you cannot audit — and because the row was right about the shape of the
problem for as long as it stood.

> | **Enter sends, Shift+Enter newlines** | Not expressible. A composer cannot distinguish the two today, so the bench chat binds Send to the button only | `bench/README.md`, "what §2 asks for that today's library API cannot express" (1) |
>
> The first of those is worth expanding, because it looks like a small thing and is
> actually three independent things, any one of which is enough to break it:
>
> 1. `live.Bind.Keys` compares the **key** and not the modifier state, so
>    `Shift+Enter` arrives as `"Enter"` and would send.
> 2. A key binding **never calls `preventDefault`**, so Enter would insert the
>    newline *as well as* sending.
> 3. `Fields`, `Debounce` and `Throttle` are read from the **element**, not from
>    the binding, so a composer bound for both `input` and `keydown` shares one
>    debounce timer — and the trailing `input` event Enter's own newline produces
>    would cancel the pending send outright.
>
> That is not a bug to route around; it is a shape the current API does not have.
> It is reported as a finding rather than patched, and the second consumer to hit
> it said so in writing.

**All three reasons are closed, in that order, and none of them by a
workaround.** Reason 3 went first, at `2ab18690`: `Fields`, `Debounce` and
`Throttle` moved out of the element's attributes and into the binding that
declares them, so two bindings on one element no longer share a timer or an
interval. Reasons 1 and 2 went together at `0b9e32e7`, as `live.Bind.NoModifiers`
and `live.Bind.PreventDefault` — two `bool` fields, both off by default, each a
trailing component of the binding's own spec. The composer is now two bindings
on one element:

```text
data-gotth-on="keydown:chat.send:Enter::::1:1;input:chat.draft::150"
```

Enter matches the first binding, sends, and has its newline suppressed;
Shift+Enter matches the key and fails the modifier test, so it reaches no
binding at all and the browser inserts the line break. It was driven through
Chromium rather than argued
(`test/internal/conformance/keybinding_modifiers_test.go`), and the benchmark's
own chat app carries the binding (`bench/apps/chat/gotth/bindings.go`) rather
than binding Send to the button only. The full write-up is
[events-and-forms.md](events-and-forms.md#modifiers-and-taking-a-key-from-the-browser).

**What did not change, and is the honest residual.** A key filter *alone* still
cannot express it — that clause of reason 1 was always true and is why this cost
two new options rather than none. And **requiring** a modifier is still
inexpressible: there is no `Bind.Modifiers`, no bitmask and no `Ctrl`/`Alt`/`Meta`
flag, and that is a **refusal** with three grounds and a pre-registered re-open
trigger ([`docs/reviews/fr-54.md` §13](../reviews/fr-54.md)) rather than an
absence. If your interaction needs `Ctrl+K` and not just "no modifier held", this
library does not express it and the refusal names the price at which it would.

## 7. Two more things that will surprise you, and are not v0.1 bugs

- **`hx-*` markup that a morph *inserts* is inert until `htmx.process` runs on
  it.** HTMX scans the document once, at load. An element the server starts
  rendering mid-session arrives in a patch and HTMX has never seen it: clicking
  it does nothing at all — no request, no error, no console message. This is
  defect **D-16**, measured in a real browser, **documented rather than fixed**,
  with a conformance spec written to go red on the day the gap closes. See
  [htmx-interop.md](htmx-interop.md) for the two placements that avoid it.
- **A denial cannot be rendered from `Authorize`.** A `live.DenyError` rejects
  the event before the reducer runs, so there is no transition, so there is no
  render, so there is nothing for the user to see. If your rule needs a *visible*
  error, enforce it in the reducer as well — `bench/README.md` G-8 is the worked
  case, and it enforces the rule twice and renders it once.

## 8. Things that are simply out of scope for v1

Excluded on purpose, each with a backlog line in PRD §4. None of these is a
judgement about whether they are good ideas:

`html/template` and other non-templ engines (BL-5) · non-Go clients (BL-6) ·
file upload over the live connection (BL-7) · client-side routing and SPA
navigation (BL-9) · view-transition orchestration (BL-10) · i18n helpers (BL-11)
· wrapping third-party JS components with lifecycle hooks — the answer today is
preserve-and-ignore (BL-12) · a second transport (BL-13) · per-fragment
differential rendering (BL-14).

And the version itself: **this is v0.1.** The API makes no compatibility
commitment yet, and several `livetest` helpers are documented in
[`docs/api-surface.md`](../api-surface.md) as ledgered but not implemented. If
you need a stable surface this quarter, this is not it.

---

## When it *is* the right tool

Stated as briefly as the bounds above, and with the same rule that nothing
unmeasured gets to sound measured.

Forms, lists, dashboards, chat, admin tools and internal applications — where
the users are on a LAN or in-region, the interactions are server-authoritative
anyway, and the client state layer you would otherwise write exists only to
mirror the server. There you get: no client state to desync, no build step and
no npm for the consumer, one language, a typed wire protocol, per-patch
provenance that answers "why did this element change?" from a captured frame,
and per-connection observability that is on by default.

If the reason you want it is "we would like to delete the client state layer",
that is the reason it was built. If the reason is "it will be faster", measure
first — [`bench/`](../../bench/README.md) exists precisely so that claim has to
be earned, and the head-to-head report is Phase 5 work that has not been
published yet.
