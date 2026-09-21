# Events and forms

At the end of this page you can bind any DOM event to a server event, submit a
form, react to a single field as the user types, filter a keyboard binding to
one key, make Enter send while Shift+Enter still inserts a newline, and read the
form values back — including telling an unchecked checkbox apart from an empty
one.

Compiled source: [`_samples/events`](_samples/events) and
[`_samples/keychords`](_samples/keychords).

---

## The vocabulary

Four functions, all returning `templ.Attributes` that you spread into an
element. They are the whole **binding** surface; there is no second attribute
vocabulary and no hand-written JavaScript.

| Helper | Renders | For |
|---|---|---|
| `live.Region(id)` | `data-gotth-region` | Marking the root of a live fragment. Every bound control must be inside one. |
| `live.On(domEvent, eventName)` | `data-gotth-on="<dom>:<event>"` | One binding. |
| `live.OnWith(domEvent, eventName, live.Bind{…})` | `data-gotth-on="<dom>:<event>[:<key>[:<debounceMs>[:<throttleMs>[:<fields>[:<noModifiers>[:<preventDefault>]]]]]]"` | A binding with static fields, a debounce, a throttle, a key filter, a no-modifier restriction, or a `preventDefault`. |
| `live.OnAll(bindings…)` | the bindings joined with `;` | **Several bindings on one element.** |

`live.Bind` has six fields: `Fields map[string]string`, `Debounce
time.Duration`, `Throttle time.Duration`, `Keys []string`, `NoModifiers bool`,
`PreventDefault bool`. **Every one of them belongs to the single binding you
hand it to** — they render inside that binding, not beside it, so composing
bindings never changes what any of them does.

Trailing empty components are trimmed, and an empty component means "not set".
So a binding that asks for nothing is two components long, and the two booleans
render `1` or nothing at all — which is why adding them changed no byte of any
binding written before they existed.

Two more helpers complete the templ surface. Neither binds an event, which is
why each is argued for on the page it matters to — but their spellings belong in
one table with the rest, so that this page is the whole vocabulary and not most
of it:

| Helper | Returns | Renders | Argued in full |
|---|---|---|---|
| `live.Preserve()` | `templ.Attributes` | `data-gotth-preserve` — the element **and its whole subtree**, never morphed again | [htmx-interop.md](htmx-interop.md) |
| `live.Script(mountPath)` | `templ.Component` | `<script src="<mount>/gotth-live.min.js" data-gotth-url="<mount>" defer></script>` | [security.md](security.md) (no nonce parameter), [inspector.md](inspector.md) (tag ordering), [deploying.md](deploying.md) (no fingerprint in the path) |

All of these are marked **experimental** in
[`docs/api-surface.md` §5.2](../api-surface.md): the shape may change before
v1.0. The attribute spellings they emit are fixed in
[`client/SIZE.md` §7](../../client/SIZE.md), which is the contract the client
runtime reads.

---

## One element, several bindings: use `OnAll`

**Two spreads of `live.On` on one element silently loses one of them.** templ
renders each spread separately, both produce `data-gotth-on`, and an HTML
parser keeps the first attribute and discards the second. `live.OnAll` exists
because of that, and it is the only correct way to put more than one binding on
an element.

The client matches the bindings **in the order given, first match wins**, so a
filtered binding must come before an unfiltered one for the same DOM event or
it can never be reached.

**Composing is safe: it changes nothing about any binding.** `Fields`,
`Debounce` and `Throttle` render *inside* the binding that asked for them, so a
binding written into an `OnAll` is byte-identical to that binding written alone.
Two bindings that ask for different intervals get different intervals, and each
holds its own timer. The sample below relies on exactly that — the `Escape`
binding is not debounced and the `input` binding is.

> **This was not true before 2026-08-05, and the sample below was the case that
> broke.** Those three options used to be attributes of the *element*, so every
> binding on it shared one value and one timer. The `Escape` binding here
> inherited the input's 150 ms, and a keystroke inside that window did not delay
> the pending clear — it **destroyed** it, with no error, no console warning and
> nothing on the wire. It went the other way too: pressing `Escape` while a
> debounced draft was pending threw the draft away, so the server never learned
> what had been typed while the browser went on showing it. If you are reading
> an older copy of this page beside a newer library, this paragraph is the
> difference.

---

## The markup

<!-- sample: events/view.templ -->
```templ
templ ComposerRegion(s State) {
	<section { live.Region("compose")... }>
		<form { live.On("submit", EventSubmit)... } autocomplete="off">
			<label for="body">Say something</label>
			<input
				id="body"
				type="text"
				name={ FieldBody }
				value={ s.Draft }
				aria-invalid?={ s.DraftError != "" }
				aria-describedby="body-help"
				{ live.OnAll(
					live.OnWith("keydown", EventClear, live.Bind{Keys: []string{"Escape"}}),
					live.OnWith("input", EventDraft, live.Bind{Debounce: 150 * time.Millisecond}),
				)... }
			/>
			<label>
				<input type="checkbox" name={ FieldUrgent } value="1"/>
				urgent
			</label>
			<button type="submit">Post</button>
		</form>
		if s.DraftError != "" {
			<p id="body-help" role="alert">{ s.DraftError }</p>
		} else {
			<p id="body-help">{ strconv.Itoa(s.Remaining()) } characters left</p>
		}
	</section>
}
```

**On a `<form>`, submit sends the form's fields.** On a named control, the
control's own name and value are sent. It is one code path in the client, which
is why a form and a single control feel the same on the server. The client calls
`preventDefault` on a submit it recognises, so the page does not navigate — and
because it is a real `<form>`, Enter-to-send comes free from the browser rather
than from a key binding.

**`aria-invalid` and `aria-describedby` are written by hand, deliberately.**
The library ships no form vocabulary and no field type. Those attributes are
markup decisions belonging to your design system, not to a live-connection
library, and validation is state you already have.

---

## Key filters

`live.On("keydown", …)` with no filter raises an event on **every** key —
including Tab, Shift, and the arrows. `Bind.Keys` narrows it.

Six things about it, each of which will otherwise surprise you once:

1. **One key per binding.** A list renders as several bindings, which is what
   keeps every printable key value usable: `,` would be the obvious separator
   and `,` is itself a key.
2. **The comparison is exact and case-sensitive**, against the browser's own
   `KeyboardEvent.key`: `"Escape"` not `"Esc"`, `"ArrowUp"` not `"Up"`, `" "`
   not `"Space"`, `"A"` not `"a"` for a shifted letter. Nothing is normalised,
   because `"a"` and `"A"` are different keys.
3. **An unrecognised name is not an error.** It is a filter that matches
   nothing, and it shows up as a binding that never fires on the first
   keypress.
4. **Modifier state is not compared.** A printable key already carries its
   modifiers — Shift and `=` arrive as `+` — and a modifier pressed alone
   arrives as `"Shift"`, `"Control"`, `"Alt"` or `"Meta"` and matches only a
   filter naming it.

   > **Corrected 2026-08-05 (`0b9e32e7`).** True of `Bind.Keys`, and no longer
   > true of the library. `Bind.NoModifiers` compares the modifier state on the
   > binding that asks it to; the sentence above is what a binding that leaves
   > that option unset still does, which is the default and every binding in
   > this repository written before that commit. The *reason* survives intact
   > and is why the new option is spelled "none held" rather than "these held" —
   > see [Modifiers, and taking a key from the
   > browser](#modifiers-and-taking-a-key-from-the-browser).

5. **A key binding never calls `preventDefault`.** Enter on a bound textarea
   raises your event *and* inserts the newline. "Enter sends, Shift+Enter
   newlines" is therefore **not expressible with a key filter alone** — you
   need a `keydown` binding plus your own handling of the resulting state.

   > **Corrected 2026-08-05 (`0b9e32e7`, driven through Chromium at
   > `2311280b`).** The first sentence is false: `Bind.PreventDefault` takes the
   > key where a binding asks it to, per binding, off by default. **The middle
   > sentence is still exactly true** — a key filter *alone* still cannot express
   > it, which is why this cost two options rather than none — and the remedy it
   > proposed is what is now obsolete. "Enter sends, Shift+Enter newlines" is
   > expressible, is two bindings on one element, and needs no handling of the
   > resulting state.

6. **A key filter on an event that carries no key never fires.**
   `OnWith("click", …, Bind{Keys: …})` fires never rather than always. A filter
   filters.

`:` and `;` separate the binding grammar and cannot be key values. Nothing else
is reserved — `,` and `" "` and every other printable key value go through
unchanged, because a key list is emitted as one binding per key rather than as a
separated list. Writing `:` or `;` **panics**; see
[What the helpers refuse](#what-the-helpers-refuse).

---

## Modifiers, and taking a key from the browser

Two options, both `bool`, both **off by default**, added on 2026-08-05
(`0b9e32e7`) for the one interaction the four options above cannot express
between them: *"Enter sends, Shift+Enter inserts a newline."*

| Option | Renders as | Means |
|---|---|---|
| `Bind.NoModifiers` | component **7**, `1` when set and nothing when not | this binding matches only a press with **no** modifier held |
| `Bind.PreventDefault` | component **8**, `1` when set and nothing when not | `preventDefault()` on the browser event when **this** binding matches, and only then |

Both are trailing components under the trimming rule above, so a `Bind` that
sets neither renders the bytes it always rendered.

### The composer

<!-- sample: keychords/keychords.go -->
```go
func Composer() templ.Attributes {
	return live.OnAll(
		live.OnWith("keydown", EventSend, live.Bind{
			Keys: []string{"Enter"}, NoModifiers: true, PreventDefault: true,
		}),
		live.OnWith("input", EventDraft, live.Bind{Debounce: 150 * time.Millisecond}),
	)
}
```

renders

```text
data-gotth-on="keydown:chat.send:Enter::::1:1;input:chat.draft::150"
```

and behaves like this. **Enter**: the send binding matches, your event is
raised, and the newline is suppressed for that press. **Shift+Enter**: the send
binding matches the *key* and fails the *modifier* test, so it is skipped; no
binding behind it names `Enter`; the keypress reaches no binding at all,
**nothing is suppressed**, and the browser inserts the line break it was always
going to insert. That is one element, two bindings, and no second event name for
the newline — the newline is not an event, it is the thing you did not take.

This was driven in Chromium rather than reasoned about: `Enter` raised the send
event and left the textarea's value alone, `Shift+Enter` raised nothing, left
`value="hi\n"`, and the server's copy of the draft was `"hi\n"` too
(`test/internal/conformance/keybinding_modifiers_test.go`).

### `NoModifiers` reads exactly four booleans

`shiftKey`, `ctrlKey`, `altKey`, `metaKey`. Any one of them held is a press this
binding does not match. Two consequences the option's name hides, and both will
reach a real user before they reach you:

- **AltGr sets `ctrlKey` *and* `altKey`.** A printable key that needs AltGr on
  somebody's layout — `@` on many European layouts, `\` and `|` on others — will
  **not** match a binding that names it and sets this option, while the person is
  typing exactly the character the binding asked for. Name such a key **without**
  this option.
- **`CapsLock` and `NumLock` are not read.** They are lock states rather than
  held modifiers, they set none of the four, and a binding filtered here fires
  with either of them on.

### It is not keyboard-only, and it is not a no-op without `Keys`

It is tested **whether or not** the binding has a key filter, so an unfiltered
`keydown` binding that sets it raises its event for every *unmodified* key —
that is a filter, not a no-op. And a `MouseEvent` carries the same four
booleans, so on a click binding it means a **plain** click:

<!-- sample: keychords/keychords.go -->
```go
func PlainClick() templ.Attributes {
	return live.OnWith("click", EventOpen, live.Bind{NoModifiers: true})
}
```

`click:row.open:::::1` — Ctrl+click and Shift+click, the two a browser already
reads as *"open this somewhere else"*, are left to the browser. An event that
carries none of the four at all (`input`, `submit`) has all four absent, so a
binding on one is unaffected.

### A binding it filters out ends nothing

The client goes on to the next binding on the element for the same DOM event.
That is the property the composer above is built on, and it is the reason
`NoModifiers` is spelled *"none held"* rather than *"these held"*: a printable
key already carries its modifiers, `+` **is** Shift and `=` on most layouts, so a
binding that named `+` and silently demanded no modifier would match nothing.

Ordering still decides reachability. The client matches **in the order given,
first match wins**, so the restricted binding must come before an unrestricted
one for the same DOM event.

### `PreventDefault` is not a filter, and its placement is the whole of it

A binding that does not match suppresses nothing — that is what leaves
Shift+Enter to the browser. Three more things about it:

- **It is not called while an IME composition is active.** Enter during a
  composition *commits* the candidate, so a binding that suppressed it would take
  the commit key away from every composer that uses one. Mid-composition the
  client neither sends the event nor suppresses the default; the binding fires on
  the Enter after the commit instead.
- **The library's own two cases are unchanged and are not this option.** A
  recognised form submit and an anchor click have always had their default
  suppressed, and they still are — *above* the composition guard, where they
  belong, so a form cannot submit for real mid-composition.
- **It suppresses even when the event cannot be delivered.** The client
  suppresses the default as soon as the binding matches, which is *before* it
  looks for the enclosing `live.Region`. So a `PreventDefault` binding on an
  element that is outside every region swallows the browser's default and sends
  nothing — silently, in both halves. Every bound control belongs inside a region
  anyway; this is what it costs when one is not.

### What is deliberately absent

**There is no way to require a modifier**, and no `Bind.Modifiers`, bitmask, or
`Ctrl`/`Alt`/`Meta` flag. It is **refused**, not unimplemented, with three
grounds and a pre-registered re-open trigger in
[`docs/reviews/fr-54.md` §13](../reviews/fr-54.md): it costs more than both
accepted options put together, it cannot be two-valued without a sentinel (a
default of "none held" would stop `+` matching anything), and nothing in the
examples, this guide or the benchmark asks for one. A `Ctrl` or `Meta` chord
belongs to the browser and to the operating system; `Shift+Enter` in a textarea
does not, which is the distinction that made these two options the answer and
the third one not.

---

## What the helpers refuse

`On` and `OnWith` **panic** on four arguments, and the panic carries a full
sentence saying which one and why. It is a panic because these functions return
`templ.Attributes` — a `map[string]any` with no error channel — and templ skips a
value it cannot render, so an error put in the map would produce an element with
no binding at all. Each of the four is a literal in your own source, so it fires
on the first render of that view rather than on a visitor's request.

| Refused | Why the grammar cannot carry it |
|---|---|
| `:` or `;` in `domEvent` | it renders as more than one component, so the event name, the key filter and every option behind it land one slot later than the client reads them |
| `:` or `;` in `eventName` | same shift: the tail is read as a key filter, and a declared `Debounce` arrives at the client as a **throttle** |
| an empty `eventName` | the client matches on the DOM event and the key filter and reads the name *afterwards*, so an empty name renders a spec that **matches**, ends the match loop, and silences every binding behind it on the same DOM event |
| `:` or `;` in a `Bind.Keys` entry | it would widen the filter to **every** key and shift every option beside it one slot |

Nothing else is refused. An unrecognised DOM event, an unrecognised key name and
an empty `Bind.Keys` entry are all legal — they are filters that match nothing —
and a static field may contain either separator, because `Fields` is
query-encoded and both characters are escaped in keys and values alike.

**What the panic replaced is why it exists.** Before 2026-08-05 each of these
rendered, and rendering it did something nobody asked for, silently: a stray
separator widened a key filter to every key and turned a declared 150 ms
debounce into a 150 ms throttle.

---

## Reading the event

<!-- sample: events/events.go -->
```go
func Reduce(s State, ev live.Event) (State, []live.Effect[live.AnonymousIdentity]) {
	switch ev.Name {
	case EventDraft:
		s.Draft = ev.Fields.Get(FieldBody)
		s.DraftError = validate(s.Draft)

	case EventSubmit:
		body := strings.TrimSpace(ev.Fields.Get(FieldBody))
		if msg := validate(body); msg != "" {
			s.DraftError = msg
			return s, nil
		}
		// Lookup, not Get. An unchecked checkbox is ABSENT from the form data,
		// not present-and-empty, and Get cannot tell the two apart. Every
		// boolean field read with Get is a bug waiting for its first false.
		_, urgent := ev.Fields.Lookup(FieldUrgent)
		s.Posted++
		s.LastBody = body
		s.LastUrgent = urgent
		s.Draft = ""
		s.DraftError = ""

	case EventClear:
		s.Draft = ""
		s.DraftError = ""
	}
	return s, nil
}
```

`live.Event` carries five things: `Name`, `FragmentID` (the fragment whose
markup raised it), `Fields`, `At` (stamped at the actor boundary — read it here
rather than calling a clock), and `ID` (the server-minted causal identifier).

`Fields` has four methods and no conversions:

| Method | Returns |
|---|---|
| `Get(key)` | the value, or `""` |
| `Lookup(key)` | the value **and whether the key was present** |
| `Len()` | the number of fields |
| `All(func(k, v string) bool)` | an iteration in wire order |

There is no `Fields.Int`, `Fields.Bool` or `Fields.Has`. Use `strconv` and
`Lookup`; they are three symbols for what the standard library already does.

**`Lookup` is not a convenience.** An unchecked checkbox does not appear in the
form data at all, so `Get` returns `""` for "unchecked" and for "checked with an
empty value" alike. A form that flattens the two makes every boolean field
either correct by accident or a bug.

---

## Validation is state

There is no validation vocabulary, no error type, and no `aria-*` helper, and
that is the design rather than a gap. Validation is state your reducer computes
and your fragment renders:

- the reducer sets `DraftError`,
- the fragment renders it and sets `aria-invalid`,
- and a re-render caused by an unrelated event still shows it, because it is in
  the state.

---

## Keeping what the user typed

Two mechanisms, and you need both. Getting one and not the other is how a
half-typed sentence disappears.

### 1. The controlled/uncontrolled rule

The morph reads **attribute presence in the incoming markup**:

| In the patch | Morph does |
|---|---|
| `value="…"` present on an `<input>` | sets the live value from it — the server is controlling this box |
| `value` **absent** | leaves the live value alone — it is the user's |
| `checked` present or absent on a checkbox/radio | sets `checked` to match |
| text content on a `<textarea>` | sets the value from it (a textarea's declared value is its content, not an attribute) |
| anything, while the element is mid-IME-composition | nothing; composition is never interrupted |

So `value={ s.Draft }` makes the **server** authoritative for that box. That is
why the sample also binds `input` — the debounced per-keystroke event is what
keeps `s.Draft` right, so the value the server renders is the value the user
typed.

Leaving `value` out looks like it works, and that is the trap: in the common
case the browser's own uncontrolled-input handling keeps what you typed across a
morph whether or not the server agrees. It stops hiding the omission the moment
the region is genuinely re-rendered — a validation message, a reconnect, a
resync — and then an absent `value` is a sentence deleted out from under
somebody.

### 2. The fragment boundary

The rule above says nothing about *whose* event caused the patch. What keeps
somebody else's activity out of your composer is the fragment: **put the
composer in its own region whose `Dirty` names only that session's own fields.**
Then a message from another member does not put that region in the patch at all,
and the browser is never handed markup for the box somebody is typing into.

A region that mixes a shared feed with an input is the arrangement to avoid.
See [fragments-and-dirty-tracking.md](fragments-and-dirty-tracking.md).

---

## Registering event names

`Config.Events` is the allowlist, and it is **default-deny**: a name that is not
there is refused with `UNKNOWN_EVENT`, counted in
`gotthlive_events_rejected_total{reason="unknown_event"}`, and never dispatched
and never ignored.

Two consequences worth designing around:

- **Prefer one event name per operation over one name with a parameter.** Four
  names bound what a hostile client can ask for; one name and a number bound
  nothing.
- **The set is fixed before the first connection**, which is what bounds the
  cardinality of the per-event metric label.

**Three event names reach your reducer without being registered**, because the
library mints them rather than accepting them from a browser:

| Name | When |
|---|---|
| `live.EffectFailedEvent` (`"gotth.effect_failed"`) | an effect returned an error or panicked |
| `live.SlowClientEvent` (`"timer:slow_client"`) | the outbound window filled — the application half of the degradation story |
| `live.ClientRecoveredEvent` (`"timer:client_recovered"`) | it drained again |

**All three are exported constants — do not type the string.** The values are
given above so you can recognise one in a log, not so you can match on one: a
reducer that matches the wrong literal fails silently, because the `switch`
falls through to its default and the branch simply never runs. That is not
hypothetical. `examples/counter` once hard-coded a name nothing emits and
shipped a failure-handling path that had never once executed, and the two
backpressure names were unexported until `examples/dashboard` made the same
argument a second time.

A reducer that branches on `ev.Name` with no default is safe against all three;
a reducer that acts unconditionally is not, which is why the quickstart's
one-event reducer still checks the name.

---

## Debounce and throttle

`Bind.Debounce` delays sending until *that binding's* DOM event has been quiet
for that long. `Bind.Throttle` sends at most one of *that binding's* events per
interval. Both are rendered as milliseconds inside the binding, and both are
properties of the **binding** and not of the element it sits on: a second
binding on the same element is neither delayed nor rate-limited by them, and
holds its own timer.

They are the first thing to reach for when a binding is noisy, and they are not
a substitute for the server's own limits: `Limits.MaxEventsPerSecond` (default
**50**) and `Limits.EventBurst` (default **100**) are a token bucket on the
inbound side, and exceeding it closes the connection with **4008
`RATE_LIMITED`**. A debounce is politeness; the bucket is the bound.
