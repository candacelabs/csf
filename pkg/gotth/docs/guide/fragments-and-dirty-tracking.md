# Fragments and dirty tracking

At the end of this page you can split a page into independent live regions,
declare which transitions could have changed each one, and avoid the two traps
that make a `Dirty` function quietly wrong.

Compiled source: [`_samples/fragments`](_samples/fragments).

---

## What a fragment is

```text
Fragment[S]{
    ID     string                      // stable identity; a patch names it
    Render func(state S) templ.Component // pure function of state
    Dirty  func(prev, next S) bool     // optional; nil means "always"
}
```

`ID` must match `^[A-Za-z0-9_:.-]{1,64}$` and be unique within the application;
`live.New` refuses a duplicate. It is what a patch names, so **changing an ID is
a client-visible change**.

`Render` must be a **pure function of state**: the same state must render
byte-identical HTML, across runs and across processes. That comparison is what
suppresses a patch nobody needs. The known way to break it is ranging over a Go
map in a template — range a sorted slice instead — and the second is reading a
clock in a render, which is why the sample below computes an age at the
transition and renders it as data.

---

## `Dirty`, and when to write one

`Dirty` is optional. **Nil means "re-render on every transition", which is
always correct**, and it is the right first answer. Declare one when a fragment
is expensive to render, or when an unrelated event stream would otherwise
re-render it — a control panel next to a feed sampling twenty times a second.

<!-- sample: fragments/fragments.go -->
```go
func Fragments() []live.Fragment[State] {
	return []live.Fragment[State]{
		{
			ID:     FragmentReading,
			Render: func(s State) templ.Component { return ReadingRegion(s) },
			Dirty: func(prev, next State) bool {
				return prev.Latest != next.Latest || prev.ChangedAtUnixMilli != next.ChangedAtUnixMilli
			},
		},
		{
			ID:     FragmentLog,
			Render: func(s State) templ.Component { return LogRegion(s) },
			Dirty: func(prev, next State) bool { return prev.Paused != next.Paused },
		},
	}
}
```

The two mistakes are not symmetric:

- **Under-declaring is a correctness bug.** The fragment's markup moved and it
  said it had not, so the region goes stale. It shows up in production and not
  in development, because some later transition usually re-renders the fragment
  before anybody looks. `livetest.AssertDirtyComplete` is what catches it before
  merge — [testing-your-app.md](testing-your-app.md).
- **Over-declaring is free in correctness and not free in CPU.** The fragment
  is rendered, the bytes are compared against the last ones sent, and an
  identical render is suppressed before it reaches a frame. Nothing wrong
  reaches the browser. But at twenty samples a second, per session, the render
  is the cost that matters, and `AssertDirtyComplete` will not tell you: it
  treats over-declaring as free.

**The signal for an over-declared `Dirty` is
`gotthlive_patches_suppressed_total`.** It counts exactly this: fragments that
declared themselves dirty, were rendered, and produced bytes the client already
had. A wire assertion cannot see it — a widened `Dirty` leaves every frame
identical — so if you care about the render cost, assert on that counter and not
on the patch.

---

## Trap 1: `Dirty` compares with `==`, and your state must be comparable

The library compares consecutive states with `==` to decide whether the state
version moved. Go cannot compare two arbitrary values without risking a panic,
so **a state type that is not comparable is reported as changed on every
transition**. That is the safe direction — reporting a change that did not
happen costs a suppressed render, where the reverse would freeze the version and
make the provenance property false — but it is not what you want:

- a no-op event bumps `state_version`,
- and every fragment's `Dirty` is asked about a change that did not happen.

One slice or map field is enough to do it. The fix is to hold the collection
behind a pointer to an immutable value:

<!-- sample: fragments/fragments.go -->
```go
type History struct {
	Samples []Sample
}
```

`State.Latest` is a `*History`. One pointer field keeps `State` comparable, and
`prev.Latest != next.Latest` is then pointer identity: a new reading is a new
pointer, and the same reading folded twice is the same pointer.

Immutability is the other half, and it pays for itself twice. **A reducer must
not mutate the state it was given** — that is what makes panic recovery free,
because the pre-transition state is still intact and correct. A slice in a state
struct makes that rule easy to break by accident; an immutable value replaced
wholesale makes it impossible:

<!-- sample: fragments/fragments.go -->
```go
func Fold(s State, sample Sample, atUnixMilli int64) State {
	s.Latest = s.Latest.with(sample, 20)
	s.ChangedAtUnixMilli = atUnixMilli
	return s
}
```

---

## Trap 2: `time.Time` in state

**Do not put a `time.Time` in a state struct a `Dirty` function compares with
`==`.**

A `time.Time` read from `time.Now` carries a monotonic clock reading as well as
a wall clock, and `==` compares both — plus the `*Location` pointer. Two
`time.Time` values naming the same instant can therefore compare unequal. The
consequences are exactly the two this page is about:

- a `Dirty` function written with `==` over a `time.Time` reports a change that
  did not happen, on every transition that touches the field;
- and `State` stops being comparable *in the sense that matters*, so the
  library's own state comparison reports a change too.

`time.Time.Equal` is the correct comparison and `==` is not — but a `Dirty`
function is written by hand and the wrong operator compiles. Store the instant
as an integer and the choice goes away:

<!-- sample: fragments/fragments.go -->
```go
	// ChangedAtUnixMilli is when the reading last moved.
	ChangedAtUnixMilli int64
```

The same reasoning applies to any struct with a hidden pointer or an
implementation-defined field — `net.IP`, a wrapped `*big.Int`, anything holding
a `sync.Mutex` (which is not comparable at all). If a field's `==` means
something subtler than "the same value", keep it out of state and derive it.

---

## Rendering time without reading a clock

A render may not read a clock. Two renders of the same state would produce
different bytes, and the identical-render suppression that compares them would
stop working — every fragment carrying a relative timestamp would patch on every
transition forever.

Compute the derived value **at the transition**, from the event's own `At`
stamp, and render it as data:

<!-- sample: fragments/fragments.go -->
```go
func Age(changedAtUnixMilli int64, at time.Time) time.Duration {
	if changedAtUnixMilli == 0 || at.IsZero() {
		return 0
	}
	if d := at.Sub(time.UnixMilli(changedAtUnixMilli)); d > 0 {
		return d
	}
	return 0
}
```

`Event.At` is stamped at the actor boundary, which is what makes an event log
replayable: the same log replays to the same ages.

---

## How many fragments

One fragment per region that changes for its own reasons. Two rules of thumb:

- **Regions are independent.** Morph never touches anything outside a region, so
  splitting a page is what stops one event repainting all of it.
- **The page and the fragments should render the same components.** Compose the
  first paint out of the same components the fragments render, from the same
  state, so the snapshot that arrives over the WebSocket morphs the page to
  bytes it already has. Render them differently and the first patch after
  connecting visibly rewrites a page that was already correct.

There is no upper bound on fragment count in the library. There is a bound on
what one patch may carry: a patch names at most **64** fragment updates, which
is the protocol's bound on every ordinary repeated field.

### The state the page renders is the state `Init` returns

The second rule above says "from the same state", and there is one way to break
it that costs nothing to write and never announces itself.

`templ.Handler(Page(s))` builds the component **once, where you call it**. Pass
it a state value at start-up — `templ.Handler(Page(State{}))`, which is what
[the quickstart](../quickstart.md) registered before this was measured — and
those bytes are what every visitor receives for the life of the process. That
is correct exactly while `Init` also returns that value. Give `Init` a database
read, a cookie, a feature flag, and the page keeps shipping the start-up state:
measured on the quickstart application with `Init` returning `State{N: 41}` and
nothing else changed, every response carried `<output>0</output>`, corrected to
`41` only after the WebSocket connected — a visible rewrite on every load, and
with JavaScript disabled or the socket blocked, a page that is simply wrong.

**The fix is a method, not a pattern to copy.** `(*live.App).PageHandler` takes
the *function* that renders your document, calls `Config.Init` for each request
that arrives, and renders that state:

<!-- sample: mounting/firstpaint.go -->
```go
func (st *Store) FirstPaint() http.Handler {
	return st.App().PageHandler(Page)
}
```

So the two states are not merely produced by one function, they **are** one
function — whatever `Init` loads for the session is what the page was painted
from:

<!-- sample: mounting/firstpaint.go -->
```go
func (st *Store) Init(ctx context.Context, session live.Session[live.AnonymousIdentity]) (State, []live.Effect[live.AnonymousIdentity], error) {
	s, err := st.Load(ctx)
	return s, nil, err
}
```

`PageHandler` **cannot be given a state value, only the function that renders
one**, so the frozen page above is not expressible through it. Three
consequences, all of them worth knowing before you give `Init` something real to
do, and all of them written out with their measurements in
[the quickstart](../quickstart.md) §2:

- **`Init` runs once per page request as well as once per session.** It is a
  loader: it *returns* effects as values rather than performing them, so a page
  render performs none of them — they are discarded there, and the session's own
  `Init` call is what schedules them. What runs twice is the read.
- **`Config.Authenticate` runs on the page request**, and the identity it
  derives is the one `Init` is given. `Session.ID` is the zero `ID`, because no
  session exists yet; an `Init` that needs to tell the two calls apart compares
  against it.
- **A failed step renders no page at all**: `401` if `Authenticate` refuses —
  the status that visitor's upgrade would get — and `500` if the load or the
  render fails, buffered so a render that fails half way through is never a
  `200` carrying half a document.

Compiled source and the specs that hold both halves to it:
[`_samples/mounting`](_samples/mounting).

Two states can still differ — the page is rendered on one request and the
session mounts on the next, so anything that moved in between moves the region
once, correctly. What this removes is the case where they differ **by
construction**, on every load, forever.
