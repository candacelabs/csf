# Lifecycle hooks

At the end of this page you can subscribe a session to something at mount,
authorize each of its events, and release the subscription at teardown without
leaking — and you will know why there is no patch hook.

Compiled source: [`_samples/lifecycle`](_samples/lifecycle).

---

## Three hooks, and the whole session

```text
upgrade  ─▶  Authenticate(*http.Request) (I, error)              before any session memory
             CSRF(*http.Request) error
   │
   ▼
mount    ─▶  Init(ctx, Session) (S, []Effect, error)             once, first transition
   │
   ▼
each event ▶ Authorize(ctx, Session, Event) error               single mailbox ingress
             Reduce(S, Event) (S, []Effect)
   │
   ▼
exit     ─▶  Teardown(ctx, Session, S)                          after the actor has exited
```

A session lives exactly as long as its connection. There is no resume and no
grace window: a reconnect mounts a fresh session and receives a fresh snapshot,
which is the same path a deploy or a restart takes. So `Init` and `Teardown` run
in matched pairs, once each, per connection.

**One caller of `Init` is not a connection.** `(*live.App).PageHandler`, the
handler that serves the first paint —
[fragments-and-dirty-tracking.md](fragments-and-dirty-tracking.md) — calls
`Init` once per **page request** as well, with the identity `Authenticate`
derived from that request and the **zero** `Session.ID`. It discards the effects
that call returns, because effects belong to the session; what runs twice is
whatever `Init` does to produce the state.

That is free for an `Init` that only **loads**, and wrong for one that
**registers**, which is what the mount hook below does. Joining a topic once per
page view, under an ID no session will ever own and that `Teardown` will never
be called for, is a leak. An application in that shape has two honest options:
serve its page from a handler of its own that calls the same *read* `Init` calls
— the rule is that the page and the session render from one function, not that
the function has to be `Init` — or keep `Init` a pure load and register in the
startup effect, which reopens the window the next section argues for closing.
`Teardown` pairs with the session's `Init` call and with nothing else either
way.

---

## Mount

<!-- sample: lifecycle/lifecycle.go -->
```go
func Init(topic *Topic) func(ctx context.Context, session live.Session[live.AnonymousIdentity]) (State, []live.Effect[live.AnonymousIdentity], error) {
	return func(ctx context.Context, sess live.Session[live.AnonymousIdentity]) (State, []live.Effect[live.AnonymousIdentity], error) {
		subject := sess.Identity().Subject()
		topic.Join(sess.ID(), subject)
		return State{Me: sess.ID(), Subject: subject}, []live.Effect[live.AnonymousIdentity]{topic.WatchEffect()}, nil
	}
}
```

`Init` returns two things: the session's initial state, and any startup effects.

**Register synchronously here; return the long-running pump as an effect.** The
order matters. If the pump did the registering, a change published between the
mount and the pump's first loop would be missed, and the session would render a
value that was already stale. Registering in `Init` and pumping in the effect
closes that window.

An error from `Init` fails the mount and the session never starts. `Teardown` is
not called with a state value in that case, because there is no final state to
hand over — so anything `Init` acquired before it failed, it must release
itself.

**There is no `Session.Request()`.** Values a mount needs from the upgrade
request arrive through the `context`, which is derived from that request.
Retaining an `*http.Request` for a connection's lifetime is a footgun and a
memory line item, and a context value is the idiomatic Go answer with zero API
surface.

`Session` carries exactly two things: `ID() live.ID`, sixteen bytes minted by
the server and carried in every frame, and `Identity() I` — the application's
own identity type, not an interface — bound at
the handshake and immutable for the connection's life. There is no
re-authentication and no privilege change mid-session.

---

## Event

<!-- sample: lifecycle/lifecycle.go -->
```go
func Authorize(_ context.Context, sess live.Session[live.AnonymousIdentity], ev live.Event) error {
	if ev.Name == "room.purge" && sess.Identity().Subject() != "admin" {
		return &live.DenyError{Reason: "only an admin may purge the room"}
	}
	if ev.FragmentID == "" {
		return &live.FatalDenyError{Reason: "an event named no fragment, which no binding this application ships can produce"}
	}
	return nil
}
```

`Authorize` runs **before the reducer, for every event, at the single mailbox
ingress**, so a new event kind cannot skip it.

| Return | Effect |
|---|---|
| `nil` | the event is dispatched |
| `*live.DenyError` | the event is rejected, no state changes, the connection stays open |
| `*live.FatalDenyError` | the event is rejected and the connection closes with **4006 `UNAUTHORIZED`** |
| any other error | treated as a `DenyError` |

That last row is deliberate: treating an unrecognised error as an allow would
make a hook that fails open by accident, which is the one failure mode an
authorization hook must not have.

`Reason` on both types is **operator-facing**. A generic message reaches the
client in production, because an authorization reason is an authorization input.

`live.AllowAll` is the named opt-out. Use it when there is genuinely no rule
about who may do what, and grep for it before you ship.

---

## Teardown

<!-- sample: lifecycle/lifecycle.go -->
```go
func Teardown(topic *Topic) func(context.Context, live.Session[live.AnonymousIdentity], State) {
	return func(_ context.Context, sess live.Session[live.AnonymousIdentity], _ State) {
		topic.Leave(sess.ID())
	}
}
```

`Teardown` runs **after the session actor has exited**, with the final state. It
is where a subscription taken at mount is released, and it is the hook whose
absence leaks.

It is optional, and it is the only optional hook of the three. It runs on every
exit path — a clean close, an eviction, a heartbeat timeout, a shutdown drain —
because the actor exiting is what calls it.

`App.Close(ctx)` drains every session, closing each with **4001
`GOING_AWAY`**, and waits for in-flight effects up to the context's deadline.
Call it after `http.Server.Shutdown`, in that order: stop accepting, then drain.

---

## There is no patch hook, and that is a decision

`Config` has mount, event and teardown, and no `OnPatch`.

The sufficiency test for a lifecycle hook set is "subscribe to a topic on mount
and unsubscribe on teardown without leaking", and the mount/teardown pair meets
it. Per-patch visibility is delegated to instrumentation rather than dropped:

| You want | You get |
|---|---|
| how many patches, of what kind | `gotthlive_patches_sent_total{op}` |
| how long encoding and sending took | the `gotthlive.encode` and `gotthlive.send` spans |
| what one patch was caused by | one record per transition in the provenance log — `patch_id`, `origin_kind`, `origin_source`, `fragment_ids` |

None of those requires application code, and all three are turned on by a field
on `Config` — [observability.md](observability.md).

An `OnPatch` field would be an exported symbol with no named call site. If you
have an application that must audit patches from its own code rather than from
telemetry, that is the thing to say out loud: the hook is revisitable **with
that consumer named**, and not on general principle.

---

## What runs where

| Hook | Goroutine | May block? |
|---|---|---|
| `Authenticate`, `CSRF` | the HTTP handler's, before the upgrade | yes, it is an ordinary request |
| `Init` | the session's actor goroutine | **no** — it delays the first snapshot |
| `Authorize` | **the connection's read pump**, before the event reaches the mailbox | **no** — it stalls the whole connection, acks and heartbeats included |
| `Reduce` | the session's actor goroutine | **no** — it stalls that session's mailbox |
| `Execute` | a goroutine the library owns, per effect | yes, that is what it is for |
| `Teardown` | the actor's exit path | briefly |

**That row for `Authorize` said "the session's actor goroutine" until
2026-08-05, and it was wrong.** Authorization runs ahead of the mailbox, which is
the security property — an event is rate-limited, name-checked and authorized
before it costs the session a mailbox slot — and it makes this the sharper of the
two blocking hazards: a slow `Reduce` stalls one mailbox, while a slow
`Authorize` stops the connection being read at all, so the server's liveness
clock stops advancing and a long enough stall ends the session with **4010
`HEARTBEAT_TIMEOUT`**. [architecture.md](architecture.md#authorize-is-ahead-of-the-mailbox-not-behind-it)
has the pipeline it sits in, and the spec that holds the claim.

One goroutine owns each session's state and is the only writer, so your reducer
never needs a mutex. Anything shared *between* sessions — the `Topic` above — is
yours to synchronise, and it is reached from `Execute`, `Init` and `Teardown`,
never from `Reduce`.
