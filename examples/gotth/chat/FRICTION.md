# Friction, from building the chat example

Where the library was insufficient for something Phase 2's exit criteria
require, or for something a chat is simply expected to do. Each item says what
was needed, what the library offers, the workaround taken, the shape I would
propose, and which criterion it blocks.

Nothing here was fixed by editing the library. Each workaround is marked in the
code with a comment naming its item.

**Summary: nothing here blocks a Phase 2 exit criterion outright.** Two of the
seven cost real code (F-1, F-5), one weakens a documented claim (F-2), one is a
question for PM-1 rather than a defect (F-6), and one is an observation about a
CI gate outside this example's scope (O-1).

> **Corrected 2026-08-05.** The last clause is no longer true of the tree: the
> CI gate O-1 observed a hole in now covers it. The sentence stands because the
> count in front of it is what a reader arriving at the item list counts
> against.

**Four are closed.** F-1 asked for an exported way to read a frame;
`live/livetest.Client` is built, and this example's wire specs deleted their
decoder and their driver onto it. F-4 asked for `live.IsRetryable`; L9-1 ruled
the re-add in the checkpoint-2 round and it has landed, so the two specs that
were weakened by its absence now assert on the mark itself. **F-3 asked for
Escape-to-clear; it is implemented in the composer, and driven in a real
browser.** **O-1 observed that `templ`'s committed output sat outside FR-7's
byte-reproducibility gate; it is inside it, and the fix is the one O-1 drew.**
All four headings are kept, so that the numbering of the items around them does
not move and citations land somewhere.

---

## F-1 — *Closed.* `livetest.Client` exists

Kept as a heading, so that the numbering of the items around it does not move
and a reader arriving from a document that cites F-1 lands somewhere.

The item reported that four Phase 2 criteria are claims about what is and is not
on the wire — a server-initiated patch's origin source (FR-42), the `Error`
frame a reducer panic must produce, the one a render panic must produce, and,
the amended criterion, the `Error` frame an effect panic must **not** produce —
and that nothing exported could read a frame. `live/livetest`'s package
documentation described a `Client` and its Status section said it "lands with
the benchmark harness"; it was not there. So `wire_test.go` carried ~260 lines
of reader and writer over `google.golang.org/protobuf/encoding/protowire`, a
public package, against the field numbers in
`pkg/gotth/proto/gotthlive/v1/frame.proto`.

The argument that made the hand-rolled decoder necessary rather than lazy is the
one thing here worth keeping: it deliberately did **not** import
`pkg/gotth/internal/protocol/gotthlivepb`, even though Go's lexical internal
rule permitted it from the example's location at the time — the examples then
sat under the library tree, and it was confirmed by compiling. A consumer's
module is not under that prefix and never could be, so an example that proved
these criteria with the library's private codec would be proving them with a
tool no reader of the example can pick up. *(The examples have since moved out
from under that prefix, to `examples/gotth/`, so the rule no longer permits it
at all — the decision recorded here anticipated the position the tree ended up
in rather than merely the one it was in.)*

`live/livetest.Client` is now built, and it is in the library's **second
exported package**, so it satisfies that constraint by construction rather than
by care: these specs import exactly what a reader of them can import. The
decoder and the WebSocket driver are gone from `wire_test.go`, which now carries
only what is about chat — the identity cookie and the three verbs a member has.
`ClientOptions` carries the `http.Header` this example's cookie identity needs
and the `Origin` the handshake must present, which were the two requirements
this item stated.

**The changes to the proposal**, worth reading before writing the next one.
`Await` takes an explicit timeout and every retrieval method does, because "no
frame arrived within 5s" and "the suite's budget expired" are different failures
and only the first names what was being waited for. `Send` returns the client
reference it sent rather than nothing, which is what makes a spec able to
correlate a patch back to the interaction that caused it. Frames are handed back
as `*Frame` rather than `Frame`, and the decoded payload is a `*Patch`/`*Error`
rather than the flat `Fragments`/`Error` pair proposed here. The `Client` also
never acknowledges on its own — not in this item's proposal at all, and the
property `examples/dashboard`'s backpressure specs are built on.

**What it cost while it was open.** Three exit criteria cost a file, and the
next two examples paid the same bill again — `examples/dashboard`'s F-2 records
the second payment.

---

## F-2 — `Config.CSRF` cannot see a token, because the client sends none

**Needed.** FR-48: "Handshake MUST require a token bound to the authenticated
session." This example authenticates from a cookie, which is exactly the
configuration that clause exists for — the counter, being anonymous, has nothing
to protect.

**What the library offers.** `Config.CSRF func(*http.Request) error`, called on
the upgrade request. The client runtime opens the WebSocket at whatever
`data-gotth-url` says, with no query string, no application-controlled header —
a browser cannot set headers on a WebSocket handshake — and no subprotocol slot
the application owns. So the only thing the hook can read is what the browser
attached by itself, which is the cookie it would be defending. A double-submit
token is not expressible.

`live.Script(mountPath)` cannot carry one either: `normalizeMount` requires an
absolute path and appends `/gotth-live.min.js` to it, so `"/chat/live?t=…"`
renders a script `src` that 404s.

**Workaround.** `live.NoCSRFCheck`, with the counter's comment: it is safe only
because `Config.Origins` is a real allowlist, so the origin check is the whole
CSRF posture. That is the library's own stated condition and it holds. It is
still weaker than what FR-48 asks for, and this example is the first one where
the difference is visible.

**Proposed shape.** Either

```go
live.Script(mountPath, live.ScriptOptions{ConnectToken: token})
```

rendering the token into a data attribute the runtime appends to the connect
URL as a query parameter, or an application-named extra subprotocol. Either
gives `Config.CSRF` something the page put there that a cross-origin page cannot
read. The first costs one attribute read and a string concatenation in the
client.

**Blocks.** No Phase 2 exit criterion. It is FR-48's Phase 1 claim that a
cookie-authenticated example cannot presently demonstrate.

---

## F-3 — *Closed.* Escape-to-clear is implemented, and this item's reason for its absence was false before that

> **Read this item as three dated layers, because that is what it is.** It was
> written when the affordance was absent; it was **corrected on 2026-08-05**
> when its *reason* turned out to have been false since `591c275a`; and it was
> **closed later the same day** when the composition defect underneath it was
> fixed and the affordance landed. Everything between here and the ⟨CLOSED⟩
> block at the end is the first two layers, kept unedited — so sentences below
> saying the affordance is absent, that the clear is destroyed, that no
> shape has been chosen, or that `view.templ`'s composer comment still carries
> the false reason were true when written and are **not** true now. The
> ⟨CLOSED⟩ block is the current state. Nothing is deleted because
> `docs/PRD.md`'s FR-54 quotes this item, and a reader arriving from it is owed
> what it said.

This is the item my earlier friction list named, re-confirmed against the
checkpoint-2 tree and now with a concrete cost.

**Corrected 2026-08-05, and the heading is the correction.** The conclusion has
been true since this item was written. The **reason** stopped being true at
`591c275a` and stayed on the page anyway, which is worse than a wrong number,
because the reason is what a reader takes away. The sentences that carried it
are kept below with the correction beneath them rather than deleted: one of them
is quoted in `docs/PRD.md`'s FR-54 as the reason that requirement has a
population clause (c) at all, and a reader arriving from a document that cites
this item is owed the chance to see what it used to say.

**In one paragraph.** The library *can* express the binding this item asked for
— `live.Bind.Keys` and `live.OnAll` landed at `591c275a`, citing this item by
name. Escape-to-clear is still not implemented here, and the reason is now a
measurement rather than an assertion: composing an `Escape` binding onto this
composer's debounced `input` binding does not delay the clear and does not
reorder it, it **destroys** it — no error, no console warning, no frame on the
wire. The evidence is `docs/qa/fr-54-debounce-repro.md`, driven in Chromium
against the real runtime and the real helpers; the verdict is **REPRODUCES**.

**Needed.** The two keyboard behaviours a chat composer is expected to have:
Enter sends, Escape clears the draft.

**What the library offers.** As written, this item said:

> The client's `dispatch` matches on `e.type` and nothing else — there is no
> reference to `e.key` anywhere in `client/runtime.js`.

**That is false today and has been since `591c275a`.** `live.Bind` carries
`Keys []string`; `OnWith` renders it as the third component of the binding
itself — `keydown:chat.clear:Escape` — and `dispatch` compares it against
`e.key` (`client/runtime.js:632`, one comparison inside a `split` the dispatch
path was already performing). `live.OnAll` landed in the same commit and is the
second half of it: templ renders each attribute spread separately, two spreads
of `On` produce `data-gotth-on` twice, and an HTML parser keeps the first and
discards the second, so before `OnAll` this composer could not have carried a
second binding at all whatever the filter's shape.

The rest of that paragraph stands. A keydown binding with **no** `Keys` still
means every key, so `data-gotth-on="keydown:chat.send"` does raise an event on
Tab, Shift and the arrows — a frame per keystroke, and a message sent the first
time somebody moves the cursor. That is what an unfiltered keydown binding does
today and it is the defect the filter fixed.

**Workaround, and it is a good one for half of it.** Enter-to-send is obtained
for free by making the composer a real `<form>` with
`live.On("submit", EventSend)`: the client calls `preventDefault` on a submit it
recognises, and the browser's own "Enter submits a form" behaviour does the
rest. That is the right answer for this case and the example takes it.

**Escape-to-clear is not implemented, and this is the sentence that was wrong.**
As written:

> There is no non-JS expression for it, and a "clear" button is a different
> interaction rather than a substitute for one, so the affordance is simply
> absent.

The second clause stands. **The first is false**: there is a non-JS expression
for it, and it is the one this item proposed. What is true in its place is worse
and had to be driven in a browser before it could be said.

The old reason has a second home this file cannot reach: `view.templ:62`–`:68`'s
composer comment still says *"live.On has no key filter"* and *"Escape-to-clear
has no expression at all"*, and `view_templ.go:192` carries the generated copy.
Both are the same false reason and both are source files rather than this note,
so they are **reported here and left alone** — the same routing this file has
used since O-1.

**The measured reason.** This composer's input carries
`live.OnWith("input", EventDraft, live.Bind{Debounce: 150 * time.Millisecond})`
(`view.templ:97`). Composing an `Escape` binding onto it through `live.OnAll`
renders one element carrying both:

```
data-gotth-on="keydown:chat.clear:Escape;input:chat.draft" data-gotth-debounce="150"
```

`Debounce` is an attribute of the **element** and not of the binding that asked
for it — `live.OnAll`'s godoc says so, and `docs/guide/events-and-forms.md:48`–`:53`
says so — and the runtime keys **one** timer by the element and calls
`clearTimeout` on every dispatch (`client/runtime.js:648`–`:664`). So the two
bindings share one timer and one interval. What that costs was measured by QA-1
on 2026-08-05 in `docs/qa/fr-54-debounce-repro.md`, on this exact composition,
run of record `8 Passed | 0 Failed`:

- **The clear is destroyed, not delayed.** `Escape`, then a printable key 3.1 ms
  later: one event reaches the server and it is the draft, at 156.2 ms.
  `chat.clear` never arrives at all — there is nothing in the server's log to
  notice and nothing on the wire to see. (Its spec 3, the claim itself.)
- **The interference is symmetric.** `q`, then `Escape` 1.0 ms later: one event,
  and it is the clear. The server is never told about the `q` and the browser
  goes on showing it — divergence, silently. (Spec 6.)
- **The clear is late even when nothing follows it.** `Escape` alone on the
  composed element arrives at **158.8 ms**, against **1.3 ms** for the identical
  binding on an element with no debounce. (Specs 4 and 8.)
- **It is the `input` event, not the keystroke, that cancels.** `ArrowLeft`
  inside the window leaves the clear standing, because `dispatch` returns before
  it reaches the timer when no binding matches. (Spec 5.)
- **The checks can fail.** Against a runtime patched to key the timer per binding
  rather than per element, three of the eight specs go red, including the one
  above — and the 158.8 ms delay stays. Two defects, one cause. (Its §4 and §5.)

There is a second, quieter loss on the way in: writing the new binding as a
**second attribute spread** instead of through `live.OnAll` loses one of the two
bindings at parse time, because both render `data-gotth-on` and the browser
keeps whichever comes first. Two silent losses for one interaction, and neither
raises anything.

That second loss is worth naming for a reason beyond this example.
`client/runtime.js:588`–`:593` argues *for* the shape `Bind.Keys` took by
describing precisely this bug in a different slot: *"an attribute is read from
the ELEMENT and an element carries several bindings … the draft would silently
stop being sent."* That argument was made about the key filter, won, and was not
carried across to the debounce interval sitting beside it.

**The "Proposed shape" is no longer a proposal.** It is the API, and it is cited
as this item's own by name. What this item drew was:

```go
live.OnWith("keydown", "chat.clear", live.Bind{Keys: []string{"Escape"}})
// -> data-gotth-keys="Escape"
```

**The call landed and the comment beneath it did not**, and the reason it did
not is this bug's own reason: emitting the filter as an attribute of the
*element* would have filtered the `input` binding by a key an input event does
not carry, so the draft would have stopped being sent. `591c275a` put the filter
inside the binding instead — `keydown:chat.clear:Escape` — which is why the
proposal was right about the feature, wrong about where it lives, and wrong
about it in exactly the place that is still broken one slot over. Its cost
estimate was pessimistic in the cheap direction too: "one `getAttribute`, one
`split` and one `indexOf`" turned out to be **+13 gzipped bytes** and no new
attribute read at all (`docs/api-surface.md`, checkpoint 3, F-3).

**So what happens if a reader writes it today.** On this composer the call has to
be composed with the binding already on the input, which is `live.OnAll`:

```go
live.OnAll(
	live.OnWith("keydown", "chat.clear", live.Bind{Keys: []string{"Escape"}}),
	live.OnWith("input", EventDraft, live.Bind{Debounce: 150 * time.Millisecond}),
)
```

It compiles. It renders the markup above. The runtime matches the binding by
name on the keypress, reads `150` off the element, arms the timer — and the next
character the member types clears it. The server side is the ordinary two lines
(`chat.clear` added to `Config.Events`, a case in the reducer) and they are not
what stops it. Nothing stops it, which is the problem: it fails by succeeding
quietly, and the only way to notice is to count events on the server, which is
what the repro had to build.

**⟨CLOSED 2026-08-05. Everything above is the record and is unedited; this is
what changed.⟩** The section that stood here refused a *"— Closed."* heading, and
it was right to at the time. It said F-1 and F-4 closed because *"the thing they
asked for arrived **and the specs weakened by its absence now do what they were
written to do**"*, and that here *"the symbol arrived and the affordance did
not: this composer still has no Escape-to-clear, and a reader who writes the
block above gets a binding that loses events either way."* **Both halves of that
test are now met**, which is why the heading has moved and this paragraph is
underneath it rather than replacing what it argued.

**What landed.** `docs/gates/phase-4.md` §5.6's failure 2 was fixed: an option a
binding declares — `Fields`, `Debounce`, `Throttle` — is now a component of that
binding inside `data-gotth-on` rather than an attribute of the element, and the
runtime reads it, and keys its timer, off the matched binding. So the block this
item drew above no longer loses events in either direction. `client/SIZE.md`
§1.1.5 has the measurement; it cost **−85 minified and −8 gzipped bytes** and
`docs/api-surface.md` records **+0 exported identifiers**, which is the answer to
this item's own note that any new surface goes to L9-1 under FR-65: there is
none.

**What this example now does.** `view.templ`'s composer carries

```go
live.OnAll(
	live.OnWith("keydown", EventClear, live.Bind{Keys: []string{"Escape"}}),
	live.OnWith("input", EventDraft, live.Bind{Debounce: 150 * time.Millisecond}),
)
```

rendering `data-gotth-on="keydown:chat.clear:Escape;input:chat.draft::150"` and
**no element-level option attribute at all**, with `chat.clear` added to
`Config.Events` and `applyClear` in the reducer — the ordinary two lines this
item predicted, and they are still not what was stopping it.

**Driven, not assumed**, because this item's whole history is a claim that read
as true and was not. Against the real `NewMux`, a real `/login` cookie set by
the browser, the real embedded runtime and real key presses through Chromium's
input pipeline (Chromium 151, `dis-gotth-live-bench:latest`), six specs pass:
the composed markup carries the interval inside the input binding and no stray
attribute; Escape empties the box and the **server** agrees, which is the half
the browser cannot fake because this input's value is server-declared;
**Escape followed 3 ms later by a character leaves the box holding just that
character** — the clear arrived, where QA-1 measured it being destroyed; a
character followed by Escape leaves the box holding that character, with both
events delivered and the server and browser **agreeing**, where before the
server was never told and the two diverged; Enter still sends from the form;
and a key the filter does not name still raises nothing.

**One consequence worth writing down rather than discovering.** Typing and then
pressing Escape inside 150 ms delivers the clear first and the debounced draft
second, so the character comes back. That is the ordinary arithmetic of a
debounced draft beside an undebounced clear, it is what the driven run measured,
and the property that matters is that the two ends **agree** about it. The
defect was never the ordering; it was that one of the two events did not exist.

**The one question this item said it could not answer** — whether it still sits
in FR-54's population clause (c), *"every binding any document states is absent
because it cannot be expressed"* — is now answerable and the answer is no: no
document states it as absent, because it is not absent. That leaves clause 4 as
the one it was on, and `view.templ`'s comment, which carried the false reason
into a generated file, is corrected in the same landing.

**Blocks.** No Phase 2 exit criterion. As written, this item then said:

> FR-54 requires bindings to be expressible "without hand-written JS"; this is a
> binding that is not expressible at all, which is a different and quieter
> failure than the one FR-54 names.

**Right about the quiet and wrong about the cause.** The binding *is*
expressible; what it is not is composable. Against `docs/PRD.md`'s FR-54, which
now defines "complete" over four properties, this lands on **clause 2** —
*"several bindings on one element behave as each was written"* — and not on
clause 1; and it landed on **clause 4** — *"no document in the repository states
as absent something the set now expresses"* — for as long as the sentences above
stood uncorrected, which is what made it Phase 4's failure 3 rather than only
its failure 2. **One question this file cannot answer and does not:** whether
this item still sits in FR-54's population clause (c), *"every binding any
document states is absent because it cannot be expressed"*, now that the reason
is composition rather than expression. That is PM-1's to say when FR-54's grade
is next written.

---

## F-4 — *Closed.* `live.IsRetryable` exists

Kept as a heading and nothing more, so that the numbering of the items around it
does not move and a reader arriving from a document that cites F-4 lands
somewhere.

The item reported that `live.Retryable` set a mark nothing exported could read
back, so this example's two classification specs asserted `errors.Unwrap(err) !=
nil` instead — wrapping standing in for classification. L9-1 ruled the re-add in
the checkpoint-2 round (§5, C-32) and measured what the workaround was worth:
with `live.Retryable` replaced by a plain `fmt.Errorf("%w")`, the mark gone
entirely, this suite stayed **green**, including the spec whose message says the
pump *"must have wrapped it with live.Retryable"*.

`live.IsRetryable(error) bool` is now the predicate, both specs call it, and a
friction note documenting a missing feature must not outlive the feature — the
same rule that took `examples/counter`'s `Execute`/`execute` comment out under
C-25.

---

## F-5 — Every application re-implements the fan-out pump

**Needed.** Deliver a change in shared state to every session that cares.

**What the library offers.** `Config.Execute` is the only place an application is
handed an `Emitter`, so a subscription has to be a long-running effect scheduled
from `Init`, draining a queue the application also has to write, with its own
backpressure policy on top.

**Measured.** `examples/counter/store.go` spends about 90 lines on
subscriber/wake/pump/retry/refusal-budget. `examples/chat/room.go` spends about
110 on the same thing with a queue instead of a one-slot latch. Two examples out
of two.

**Workaround.** Written again, deliberately. The queue policies genuinely differ
— a counter snapshot is absolute so latest-value-wins loses nothing, and a chat
message is not so it needs a real queue whose overflow is a terminal failure —
so this is not a copy, and a shared hub type would be wrong.

**But the retry half is identical in both**, down to the constants: a refusal
budget, a fixed delay, a `live.Retryable` wrap when the budget runs out, and a
`ctx.Done()` arm in two places.

**Proposed shape.** Not a hub. Either

```go
// package live
func Pump(ctx context.Context, emit Emitter, next func(context.Context) (Event, error),
          p PumpPolicy) error   // PumpPolicy{Refusals int, Delay time.Duration}
```

which takes the refusal budget, the classification and the backoff and leaves
the application its queue — or, cheaper and possibly better, a documented recipe
in the quickstart, since both examples arrived at the same shape independently
and a recipe carries the reasoning a helper would hide.

**Blocks.** Nothing.

---

## F-6 — No form or validation vocabulary, and I am not sure there should be

**Needed.** FR-55 calls form submission, per-field change events and
server-driven validation feedback "first-class".

**What the library offers.** `live.On("submit", …)` plus the client's FormData
path, which is genuinely good: forms and single controls take the same code
path, and an unchecked checkbox arrives *absent* rather than empty, which is
exactly what `Fields.Lookup` exists to report. Beyond that there is nothing
form-shaped. Validation is state the application computes and renders, and the
accessibility attributes (`aria-invalid`, `aria-describedby`) are hand-written.

**Workaround.** `Validate()`, `State.DraftError`, and hand-written ARIA. It is
about twenty lines and it reads well.

**I am not proposing a helper.** A `live.Field` / `live.FormErrors` type would be
a framework growing inside a library, and the mechanism is sufficient without
one. What is actually missing is a documented pattern, and "first-class" is a
word a reasonable reader could take either way.

**Recorded so PM-1 can rule**, rather than so DEV-1 can build. If the answer is
"documented pattern", this example is the documentation and FR-55 is met; if it
is "typed helpers", that is a Phase 4 DX item and FR-55's Phase 2 line should
say so.

> **RULED — PM-1, 2026-08-04, checkpoint-2 gate (PRD v0.5 §9 row 3; FR-55
> amended).** Documented pattern. "First-class" means the mechanism, and FR-55
> now names the five properties it means — including the one this item praised,
> that an absent control arrives absent rather than empty. **No `live.Field`, no
> `live.FormErrors`, no form helper package in v1**: it would be exported surface
> whose only consumer is an example (FR-65), and the ARIA attributes it would
> want to own belong to the application's design system. The re-open trigger is a
> **named application consumer in the PR**, on the FR-56 precedent; BL-33 holds
> it. The half of this item that *is* a gap is documentation, and it is now owed
> by FR-59 rather than by nobody: the docs set must carry a forms-and-validation
> page derived from this example, at Phase 4. This example is the source, and
> "about twenty lines and it reads well" is the thing the page has to preserve.

**The page the ruling owed exists: `docs/guide/events-and-forms.md`, with
`docs/guide/_samples/events` as its compiled source.** It carries the three
things the ruling named — the same composer shape, a *"Validation is state"*
section that says there is no validation vocabulary and why that is the design,
and `Fields.Lookup` written up as *"not a convenience"* because an unchecked
control is absent rather than empty, which is the property this item praised.
The hand-written `aria-invalid` / `aria-describedby` are on the page as a
deliberate omission rather than a gap, in the ruling's own terms.

**This closes the documentation half of this item and rules on nothing.**
Whether it discharges FR-59 is FR-59's gate to say, not this file's; the
proposal half stays refused with **BL-33** holding the re-open trigger — a named
application consumer in the PR — and no `live.Field` and no `live.FormErrors`
exist.

---

## O-1 — *Closed.* Observation, not friction: `_templ.go` **was** outside FR-7's gate

> **Closed 2026-08-05. Everything from here to the ⟨CLOSED⟩ block is the item as
> written, unedited, so a reader arriving from a document that cites it sees
> what it said.** Its "is outside" is now "was outside", in the heading only.

Reported because it is load-bearing for this example and outside my scope to fix.

FR-7 requires generated code to be byte-reproducible and names CI as the gate.
`gen.sh --check` enumerates exactly four generated files and all four are
protobuf:

```
internal/refine/refine.go
internal/protocol/refinepb/refine.pb.go
internal/protocol/gotthlivepb/frame.pb.go
internal/protocol/gotthlivepb/frame_refine.pb.go
```

`view_templ.go` is committed generated code in both examples and is produced by
`templ generate`, run by hand — `examples/counter/README.md` line 221 documents
the command. Neither `ci.sh` nor `.github/workflows/gotth-live-checks.yml` runs
it or checks it.

So a `view.templ` edit committed without regenerating leaves committed generated
code that no run of the generator would produce, which is the failure `gen.sh`'s
own comment says the check exists to catch:

> The failure it exists to catch is mundane and has already happened once: an
> edit to a comment in the .proto that is committed without regenerating.

The fix is small — add `examples/*/view_templ.go` to `gen.sh`'s `generated`
list and a `templ generate` step beside the protoc ones — but `gen.sh` is
outside this example's scope, so it is written down here rather than done.

**⟨CLOSED 2026-08-05. Everything above is the item and is unedited; this is what
changed, and it is the fix this item drew.⟩** `gen.sh` now carries a
`templ_sources` list, derives `foo.templ` → `foo_templ.go` from it, appends
those to `generated`, and runs `templ generate` beside the protoc steps. All
three examples' views are on the list, and so are the guide's five compiled
samples and the three benchmark apps' — which is wider than this item asked for
and is the right width: *"generated code is byte-reproducible"* is a property of
the code and not of the directory it sits in.

**The list did not merely get longer; it got a guard.** `gen.sh` also **walks
the tree** and fails if it finds a `.templ` the list does not name, because an
enumeration maintained by hand is exactly the thing that goes stale — which is
what this item cost. That walk has already caught two files the day they landed.

**Both of this item's named gates now run it.** `ci.sh` has an *FR-7:
byte-reproducible codegen* step that invokes `bash gen.sh --check`, and
`.github/workflows/gotth-live-checks.yml`'s library job runs `ci.sh` **with the
repository root mounted**, which is the condition that check needs. Verified by
running it, from the repository root, rather than by reading the script:
`==> the committed output is byte-identical to a fresh generation`.

**One residual, stated because `ci.sh` states it against itself and a closed
item that hides a caveat is worth less than an open one.** The FR-7 step is
skipped entirely when `research/protobuf-refinement-types/plugin` is not
mounted, and `ci.sh` prints that the skip is **wider than its cause** — the
templ half needs only `templ` and `go`, neither of which needs `research/`, so a
`.templ` edit committed without regenerating would pass a module-only
invocation. It does not pass CI, which mounts the root. Run the command `ci.sh`
prints before trusting a local green.

> **Superseded 2026-08-11.** The block above is preserved as O-1's
> point-in-time evidence. The experimental research tree and all three local
> refinement artifacts it names are now retired. `gen.sh` builds
> `protoc-gen-liquidproto` from canonical `pkg/liquidproto` source, generates
> `frame.pb.go` plus `frame_liquid.pb.go`, and requires the repository root;
> the old research-dependent skip is no longer a live workflow.
