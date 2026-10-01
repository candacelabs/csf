# Effects and server push

At the end of this page you can do I/O from a live application, share state
between sessions, push a change from the server into a browser that did not ask
for it, decide correctly whether a failed effect should be retried, and write an
effect that moves money without moving it twice.

Compiled source: [`_samples/effects`](_samples/effects) and
[`_samples/payments`](_samples/payments).

---

## Why effects are values

A reducer must not perform I/O. It returns **effects** — plain values describing
work — and the library performs them at the actor boundary, on a goroutine it
owns and waits for at shutdown.

```text
Reduce(state, ev) → (state, []Effect)        pure, on the session's goroutine
        │
        ▼  actor boundary
effect.Run(ctx, session, emit)               I/O, on a goroutine the library owns
        │
        └── emit(Event) ─────────────────▶  back into the same session's mailbox
```

`live.Effect` is a **concrete struct** with two fields:

```text
Source string
Run    func(ctx context.Context, session Session, emit Emitter) error
```

`Source` names the effect for provenance and metrics, in the form
`package.action`, and becomes the origin source `effect:<name>` on every patch
the effect causes. It is also the value the failure event carries, and the one
that is safe to render.

`Run` is the behaviour. Operator ruling, 2026-09-03: this used to be a one-method
interface an application implemented, and a reducer handing back a slice of those
handed a caller a slice of abstractions where every element is one named thing the
application decided to do. A framework this repository owns does not get the
pass-through exemption CS-8 grants third-party contracts.

Write an effect as a **constructor returning a value**, closing over whatever it
needs. A zero `Effect` is inert and is dropped; an `Effect` that names itself and
carries no `Run` fails deterministically, because an effect that never runs is a
change that never happens.

<!-- sample: effects/effects.go -->
```go
func (s *Store) ApplyEffect(delta int64, cause uint64) live.Effect[live.AnonymousIdentity] {
	return live.Effect[live.AnonymousIdentity]{
		Source: SourceApply,
		Run: func(ctx context.Context, sess live.Session[live.AnonymousIdentity], emit live.Emitter) error {
			s.apply(delta)
			return nil
		},
	}
}

func (s *Store) WatchEffect() live.Effect[live.AnonymousIdentity] {
	return live.Effect[live.AnonymousIdentity]{
		Source: SourceWatch,
		Run: func(ctx context.Context, sess live.Session[live.AnonymousIdentity], emit live.Emitter) error {
			return s.Pump(ctx, sess.ID(), emit)
		},
	}
}
```

---

## The reducer decides, the store applies

<!-- sample: effects/effects.go -->
```go
func Reducer(store *Store) live.Reducer[State, live.AnonymousIdentity] {
	return func(s State, ev live.Event) (State, []live.Effect[live.AnonymousIdentity]) {
		switch ev.Name {
		case EventInc:
			return s, []live.Effect[live.AnonymousIdentity]{store.ApplyEffect(1, ev.ID)}

		case EventSync:
			return applySync(s, ev), nil

		case live.EffectFailedEvent:
			return s, retryWatch(store, ev)
		}
		return s, nil
	}
}
```

The reducer is a **constructor** over the store, because an effect carries its
own behaviour and the transition that schedules one has to be able to build it.
It still touches the store not at all.

**The reducer never changes the shared value.** It returns an effect and learns
the result the same way every other session does. That is what
"server-authoritative" means concretely, and it is why two tabs cannot disagree.

---

## There is no `Config.Execute`

There was one until 2026-09-03, taking one effect interface and type-switching on
its dynamic type. It went with the interface: `Effect.Run` is where an effect's
behaviour lives, so there is nothing left for a central executor to dispatch on.
An application that had one moves each `case` arm into the constructor that
builds the effect — which is also where the store, broker or pool it needs is
already in scope.

**The `Session` is a parameter of `Run`, not something to fish out of the
context.** An
effect's identity is an input to what the effect does — a message published to a
topic has an author, and the identity `Authorize` permitted the event under is
the identity the effect it scheduled must still act as. A context value would
make that optional at the type level and absent by mistake at runtime.

The `context` is cancelled when the session ends, which is what a long-running
effect selects on.

---

## The `Emitter`: pushing an event into a session

`live.Emitter` is `func(event Event) error`. It injects an event into the session that
spawned the effect, and it is safe to call from the effect's goroutine. It is
how a subscription delivers a value the browser never asked for.

<!-- sample: effects/effects.go -->
```go
func (s *Store) Pump(ctx context.Context, id live.ID, emit live.Emitter) error {
	wake := s.join(id)
	defer s.leave(id)

	refusals := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wake:
			if err := emit(s.syncEvent()); err == nil {
				refusals = 0
				continue
			}
			refusals++
			if refusals >= 5 {
				return live.Retryable(fmt.Errorf("effects: the session refused %d snapshots in a row", refusals))
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
}
```

Four rules govern what you may put on an emitted event, and all four are
enforced rather than documented:

| Field | Rule |
|---|---|
| `Name` | Yours. It does **not** have to be in `Config.Events` — registration is what makes a name sendable *by a browser*. |
| `Fields` | Built with `live.NewFields`, which is the only way to give an emitted event a payload. |
| `ID` | Must be zero. A non-zero value is an **error**, not a silent discard: causal identifiers are minted by the server so mistaken input cannot forge provenance. |
| `At` | Must be zero. The actor boundary stamps it. |
| `Contributing` | At most **64** identifiers, all non-zero, all events of this session. A longer list is rejected with an error naming the count. |

`live.NewFields(map[string]string)` copies the map into a key-sorted slice. The
ordering is part of the contract: `Fields` is compared by value in the replay
harness, so an unordered copy would fail a determinism check that had found
nothing wrong.

**`Emitter` is marked experimental** in
[`docs/api-surface.md` §2](../api-surface.md). Its named consumer is the chat
example; if effects-as-data does not survive a long-lived pubsub subscription,
this is the symbol that changes.

---

## Emission is best-effort, and that shapes your payload

`emit` returns an error when the session's mailbox is full or the session is
closing, so an effect learns about backpressure rather than having its event
vanish. `Limits.MailboxDepth` defaults to **64**.

**Push absolute values, not deltas.** A dropped delta leaves that session
permanently diverged; a dropped snapshot is superseded by the next one. The
sample's `applySync` also drops a snapshot older than the one it holds, so
out-of-order delivery repairs itself:

<!-- sample: effects/effects.go -->
```go
func applySync(s State, ev live.Event) State {
	version, err := strconv.ParseUint(ev.Fields.Get(FieldVersion), 10, 64)
	if err != nil || version < s.Version {
		return s
	}
	value, err := strconv.ParseInt(ev.Fields.Get(FieldValue), 10, 64)
	if err != nil {
		return s
	}
	s.Value, s.Version = value, version
	return s
}
```

There is no `App.Broadcast`. A cross-session write API would put one session's
state in another session's goroutine by construction; server-initiated patches
go through each session's own subscription, which is what the pump above is.

---

## `Event.Contributing`: the provenance edge only you know

The library already carries the edge from the event that scheduled an effect to
the patches that effect produces. `Contributing` is for the edge it cannot know.

An asynchronous fan-out through shared state splits the knowledge in two: the
subscription was scheduled at **mount**, and the value came from somebody's
**click**. The library knows the first; only your application knows the second.

<!-- sample: effects/effects.go -->
```go
func (s *Store) SyncEventFor(cause uint64) live.Event {
	ev := s.syncEvent()
	if cause != 0 {
		ev.Contributing = []uint64{cause}
	}
	return ev
}
```

Naming the click here is what lets an operator holding the patch that changed
the number reach the interaction that changed it. It is a **contributing claim
and never a causal one**: the patch's own cause stays the server-minted origin,
and these identifiers land in the patch's contributing-event list beside any the
library added.

The bound is 64 per event, it is not configurable, and an over-long list is
rejected rather than truncated. The reason is arithmetic: the protocol bounds a
patch's contributing list at 1024, and every identifier you add to one event is
one the library may not coalesce — it is subtracted from the flush headroom that
`Limits.CoalesceFlushAt` (default **512**, range **1–959**) governs.

---

## Retry: the classification is the effect's, not the reducer's

A failed or panicking effect is delivered to the reducer as an ordinary event
named `live.EffectFailedEvent`, carrying three fields:

| Field | Holds |
|---|---|
| `live.EffectFailedSourceField` | the `Source` of the effect that failed — **the value that is safe to render** |
| `live.EffectFailedErrorField` | the error's message, or the panic value, verbatim and unredacted, in production |
| `live.EffectFailedRetryableField` | `"true"` only if the effect marked it with `live.Retryable` |

`live.Retryable(err)` marks an error returned from an effect's `Run` as transient.
`live.IsRetryable(err)` reads the mark back, through `errors.As`, so it survives
`%w` wrapping in either direction and is invisible in the message.
`Retryable(nil)` is nil, so a result can be wrapped unconditionally.

**Unmarked is terminal, deliberately.** An effect may have committed externally
before it failed — the message was published, the row was written — so retrying
a failure nobody classified risks doing it twice. A failure never retried costs a
change that visibly does not happen; a failure retried blindly costs corrupt data
somebody else owns. Between a visible omission and an invisible duplicate, the
default belongs on the omission.

The classification belongs to the code that performed the effect, because
whether an effect is safe to run twice is a property of the effect. The pump
above claims transience for a full mailbox, and it is right to: neither a full
mailbox nor a session mid-shutdown is a property of the subscription.

<!-- sample: effects/effects.go -->
```go
func retryWatch(store *Store, ev live.Event) []live.Effect[live.AnonymousIdentity] {
	retryable, _ := strconv.ParseBool(ev.Fields.Get(live.EffectFailedRetryableField))
	if retryable && ev.Fields.Get(live.EffectFailedSourceField) == SourceWatch {
		return []live.Effect[live.AnonymousIdentity]{store.WatchEffect()}
	}
	return nil
}
```

`strconv.ParseBool` returning an error yields `false`, which is the right
default: an unreadable classification is an unclassified one, and unclassified
is terminal.

**Do not render `EffectFailedErrorField`.** It is whatever an upstream library
chose to put in an error — a connection string, a query, an internal hostname —
and it is not gated by `Config.Dev`. Rendering it into a fragment publishes it
to the browser. Branch on it in the reducer and render the source instead; log
it and count it somewhere that is not the reducer — the effect's own `Run`, or the
`slog.Handler` you give `Config.Logger` — because FR-16 makes logging
application data I/O and a reducer may not perform I/O. This sentence used to
end "log it, count it, branch on it", which is the wording that produced
deviation **E-2** in [`../exceptions.md`](../exceptions.md). See
[error-handling.md](error-handling.md).

---

## Delivery semantics, and the obligation they put on your application

Everything above is about one execution of an effect. This section is about the
second one.

### What a duplicate frame does

This is the library's contract, stated as PRD **FR-77(a)** states it:

> Events are **at-most-once**. The library MUST NOT retry an unacknowledged
> event across a reconnect, and MUST NOT deduplicate an event it receives twice.
> Two byte-identical `Event` frames are **two events**: two transitions, two
> `state_version` increments, two effect runs. A test MUST assert this directly,
> so that adding deduplication goes red rather than silently changing the
> contract.

That test is `test/internal/chaos/case8_replay_test.go`, and it exists so that a
future change which starts collapsing repeated frames fails the build instead of
quietly rewriting this page.

Patches go the other way: **exactly-once and in order**, or the client detects
the gap and asks for a `Snapshot`.

**The non-deduplication is a decision, not an omission.** The client has no send
queue, no pending buffer and no resend — `send()` returns false when the socket
is not `OPEN` and the event is gone — so *the library never emits a duplicate*.
A second identical frame therefore always came from the sender. If it came from
a person who clicked twice, those are **two intents**, and a library that
collapsed them would be silently discarding one; if it came from an attacker
replaying a capture, they can equally send two *different* frames and mint any
nonce a deduplicating design would ask for, so deduplication buys nothing there
either. The replays that are genuinely attacks are refused on other grounds —
another session's event, a backwards acknowledgement, and a flood of resync
requests all close the connection.

### The two ways your application meets a double execution

Only one of them is a duplicate frame, and they need different things from you.

| | **Path 1 — the sender genuinely sent twice** | **Path 2 — the effect committed, the patch never arrived** |
|---|---|---|
| What happened | A double-click, a second tab, a scripted replay | The effect ran and committed externally; the connection dropped before its patch reached the browser |
| How many intents | **Two** | **One** |
| How many executions | Two | Two — the second is the user retrying what looks to them like a failure |
| Is the library wrong | **No.** Two frames are two events, and collapsing them would be a defect | **No.** At-most-once is about *delivery*, and this is a commit that already happened |
| Does at-most-once help | It is the reason there is no *third* execution from a retry the library did on your behalf | **No. This is the case at-most-once does not solve** |

Path 2 is the leak, and it is worth reading slowly. The session dies with its
connection: there is no replay buffer and no resumable circuit, so a reconnect
gets a **fresh session** whose `Init` rebuilds state from your own store. The
customer, meanwhile, saw a spinner and then a reload. They press the button
again. One intent, two executions, and nothing in the protocol was violated.

### The idempotency key belongs in your domain

The at-most-once choice did not remove the idempotence obligation from
application code. It **moved** it — from *every reducer, always* (which is what
at-least-once delivery would have cost) to *every effect that commits outside
this process*. That set is much smaller, and it is predictably the expensive
one: payments, mail, external writes. PRD **R-12** records the trade and records
the risk that goes with it, which is that a smaller obligation reads as no
obligation.

So here is the whole of it, worked, on an effect that moves money.

Compiled source: [`_samples/payments`](_samples/payments), with a spec for each
of the two paths above.

**The key is a function of the order, and of nothing else.**

<!-- sample: payments/payments.go -->
```go
func IdempotencyKey(orderID string, amountCents int64) string {
	sum := sha256.Sum256([]byte(orderID + "\x00" + strconv.FormatInt(amountCents, 10)))
	return "checkout-" + hex.EncodeToString(sum[:16])
}
```

The reducer mints it, through one method, so that every site which schedules a
charge derives the same key:

<!-- sample: payments/payments.go -->
```go
func (s State) charge() ChargeIntent {
	return ChargeIntent{
		OrderID:     s.OrderID,
		AmountCents: s.AmountCents,
		Key:         IdempotencyKey(s.OrderID, s.AmountCents),
	}
}
```

<!-- sample: payments/payments.go -->
```go
	case EventPay:
		if s.Status != StatusOpen {
			return s, nil
		}
			s.Status = StatusCharging
			return s, []live.Effect[live.AnonymousIdentity]{gateway.ChargeEffect(s.charge())}
```

The `Status` guard is **not** the mechanism. It is worth having — within one
session, transitions are serialised on the session's goroutine, so the second
click of a double-click sees `StatusCharging` and schedules nothing, and that
saves a round trip to the payment provider. But it is in-process state. It does
nothing about two tabs, and it does nothing about path 2, where the fresh
session's `Init` legitimately rebuilds the checkout as unpaid because nothing
recorded the charge. Ship only the guard and you have a checkout that is safe in
development and charges twice in production.

The effect passes the key out to the third party:

<!-- sample: payments/payments.go -->
```go
		Run: func(ctx context.Context, sess live.Session[live.AnonymousIdentity], emit live.Emitter) error {
			charge, err := g.Provider.Charge(ctx, ChargeRequest{
				IdempotencyKey: request.Key,
				OrderID:        request.OrderID,
				AmountCents:    request.AmountCents,
			})
			if err != nil {
				return fmt.Errorf("payments: charging order %s: %w", request.OrderID, err)
			}
			if err := emit(live.Event{
				Name: EventCharged,
				Fields: live.NewFields(map[string]string{
					FieldChargeID: charge.ID,
					FieldOrderID:  request.OrderID,
				}),
			}); err != nil {
				return live.Retryable(fmt.Errorf(
					"payments: order %s was charged as %s and the session did not learn: %w",
					request.OrderID, charge.ID, err))
			}
			return nil
		},
```

**Look at the `emit` branch.** The money has moved by the time that line runs, so
an emit that fails *is* path 2 happening inside one process — committed
externally, and the session never learned. The interesting part is the
`live.Retryable`: marking a failure transient is a claim that running the effect
again is safe, and this effect is entitled to make that claim **because it
passed a key**. Delete `IdempotencyKey` and the honest classification here
becomes terminal, and the customer is left looking at a checkout that has
already taken their money. That is the sense in which the key is not defensive
padding: it is what buys the retry.

The same key is what makes the failure path safe to write at all:

<!-- sample: payments/payments.go -->
```go
func failedCharge(gateway *Gateway, s State, ev live.Event) (State, []live.Effect[live.AnonymousIdentity]) {
	if ev.Fields.Get(live.EffectFailedSourceField) != SourceCharge {
		return s, nil
	}
	if retryable, _ := strconv.ParseBool(ev.Fields.Get(live.EffectFailedRetryableField)); retryable {
		return s, []live.Effect[live.AnonymousIdentity]{gateway.ChargeEffect(s.charge())}
	}
	s.Status = StatusOpen
	return s, nil
}
```

A retryable failure re-schedules the same effect, which mints the same key, so
the provider answers with the charge it already made. A terminal failure reopens
the checkout and lets the customer press Pay again — same key, same answer.

### What not to key on, and why each one fails

<!-- sample: none — the three mistakes, stated so they are recognisable -->
```go
Key: strconv.FormatUint(ev.ID, 10)   // the event that scheduled the effect
Key: sess.ID().String()              // the session
Key: strconv.FormatInt(time.Now().UnixNano(), 10)
```

- **The event.** Two clicks are two events with two different `Event.ID`s, so
  this mints two keys and charges twice — for exactly the double-click it looks
  like it is preventing. `_samples/payments` has a spec that watches it do so.
- **The session.** A session dies with its connection, so a reconnect has a new
  one and the key differs across precisely the retry it was supposed to stop.
- **A clock or a random source.** The key is different every time, which is the
  same as having no key. A reducer cannot read either of them anyway — that is
  what makes it pure — so this mistake can only be made in the executor, where
  it is harder to see.

**The rule underneath all three:** the key must be derived from the thing the
user meant to happen *once*. That thing is in your domain — an order, a message,
a transfer — and it is not in the library's, which is why this obligation cannot
be discharged by any amount of protocol.

### One last thing, and it is the reason for the whole section

An application-side check *before* calling out — "is this order already paid?" —
is a check against a row **you** wrote. The idempotency key is a check against
the row that **took the money**. Those are the same row only when nothing went
wrong in between, and the window in which they differ is precisely the window
this contract is about.

If your externally-committing effects cannot be made idempotent and you cannot
supply a key, this library does not make your application safe. See
[when-not-to-use-this.md](when-not-to-use-this.md), which says so in those
words.

---

## What the effect boundary guarantees, and what it does not

- Effects run **after** the transition that returned them, on a goroutine the
  library owns; `App.Close` waits for them up to `Limits.EffectDrainTimeout`
  (default **5 s**) and counts the ones it abandons.
- A panicking effect becomes an `EffectFailedEvent` rather than an error frame,
  because a failure the reducer can see is replayable and one that only reaches
  the wire is not.
- Patches are **exactly-once and ordered**; events are **at-most-once**, and two
  byte-identical `Event` frames are two events.
- **An effect may have executed even though the user never saw its result.** No
  delivery semantics fix that, because the commit already happened. The
  idempotency key goes in your own domain, and the section above is the worked
  example.
