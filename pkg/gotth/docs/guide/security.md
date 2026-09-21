# Security configuration

At the end of this page you can configure the four security fields for a real
deployment rather than a laptop, write an origin allowlist that a browser can
actually match, send a Content-Security-Policy the client runtime is known to
work under, put an authorization rule in the one place it can be both enforced
and *seen*, and verify — not assume — that the developer tools are off.

Compiled source: [`_samples/security`](_samples/security).

---

## Four fields, none of them nilable

`Origins`, `Authenticate`, `Authorize` and `CSRF` are **required**. `live.New`
refuses a `Config` that leaves any of them unset, so turning a check off is
something you write down, in a named and greppable symbol:

```text
grep -rn 'live\.AnyOrigin\|live\.Anonymous\|live\.AllowAll\|live\.NoCSRFCheck'
```

What each escape hatch does and what production puts in its place is the table
in [`quickstart.md` §2](../quickstart.md#the-security-defaults-and-the-four-ways-out),
and it is not repeated here. This page is about the two of them that are
subtler than the table can be — the allowlist and the authorization hook — and
about the things that are not `Config` fields at all.

**The order of the handshake is itself the security property**, because it puts
every check before any per-session allocation:

| Step | On failure |
|---|---|
| origin allowlist | `403 forbidden origin` |
| `Authenticate` | `401 unauthenticated` |
| `CSRF` | `403 forbidden` |
| session limits | `503 too many sessions` |
| subprotocol `gotth-live.v1` | `426` |
| accept `101` | the session identifier is minted **only now**, and the actor spawned |

---

## Origin allowlisting

**This is the setting this project has already shipped a live defect on, and
the defect was in the documentation.** Read the whole section; the failure mode
is not "insecure", it is "nothing works and the reason is invisible".

### The matching rule, exactly

An entry is compared against the browser's `Origin` header with
`strings.EqualFold` and nothing else. There is no prefix match, no path match,
no wildcard, no normalisation, and no reflection of the request's own origin. A
request that sends **no** `Origin` is refused, because an absent origin is not
an allowed one.

So an entry is exactly `scheme` + `://` + `host`, with the port only when the
URL has one:

<!-- sample: security/security.go -->
```go
func Origins() []string {
	return []string{
		"https://app.example.com",
		// A second entry, not a wildcard, is how a second hostname is allowed.
		"https://www.app.example.com",
	}
}
```

| The browser sends | Against the list above |
|---|---|
| `https://app.example.com` | **allowed** |
| `https://APP.example.com` | **allowed** — `EqualFold`, and a host name is not case-sensitive |
| `https://app.example.com/` | **refused.** A trailing slash is a different string, and no browser sends one |
| `http://app.example.com` | **refused.** The scheme is part of the origin |
| `https://app.example.com:443` | **refused.** A browser omits the default port; if you write it, nothing matches |
| `https://other.app.example.com` | **refused.** There is no wildcard, and a subdomain is a separate entry |
| *(no `Origin` header)* | **refused** |

Every row of that table is a spec in `_samples/security`, driven against a
mounted handler with a real upgrade request. None of them asserts what the
allowlist is *not*; each one sends the header a browser would send and reads
the status back. That is deliberate, and the next two sections are why.

### The `0.0.0.0` trap, which was a live defect here

Three of this repository's own examples documented their containerised
invocation as `-addr 0.0.0.0:PORT`, and each derived its allowlist from that
listen address. **`0.0.0.0` is a bind address; no browser ever sends it as an
`Origin`.** Following the documented instructions therefore produced an
allowlist of exactly `["http://0.0.0.0:PORT"]` — which nothing can match — and
every upgrade from a browser at `http://localhost:PORT` was refused with `403`.
The documented way to run all three examples did not work. It is recorded as
**D-1** in [`docs/reviews/deduplication.md`](../reviews/deduplication.md).

**A listen address is not an origin.** They coincide often enough on a laptop
to look like the same thing and they are not, and the four spellings that
differ are the four that bite:

| You bind | A browser sends | So the list needs |
|---|---|---|
| `0.0.0.0:8080` | `http://localhost:8080` or `http://127.0.0.1:8080` | **both** loopback spellings; never `0.0.0.0` |
| `127.0.0.1:8080` | whichever of the two you typed in the bar | both, because `localhost` and `127.0.0.1` are different strings |
| `:8080` | `http://localhost:8080` or `http://127.0.0.1:8080` | see the residual below |
| a public host | `https://app.example.com` | that, and nothing derived from anything |

**In production, do not derive the allowlist at all.** Write the origins your
page is served from as literals, as the sample above does. A derivation is a
second place for the deployment to be wrong.

### The `:PORT` residual, which is deliberately still open

`-addr :8080` — the ordinary Go spelling for "all interfaces" — is **still
broken in all six of this repository's copies of the helper**, on purpose.
`net.SplitHostPort(":8080")` yields an empty host, so the derived list contains
the meaningless `"http://:8080"`, and the empty-host arm adds `localhost` but
**never `127.0.0.1`**. A developer running `-addr :8080` and browsing to
`http://127.0.0.1:8080` is refused with `403`.

It is filed as **D-10** and left unfixed for a stated reason: the same helper is
written six times across six separate modules, D-1's whole argument is that a
seventh variant is worse than a shared sixfold bug, and fixing it in three
copies would have re-created exactly the divergence D-1 closed. The fix is
specified — one change, all six copies, one commit — and belongs to whoever
lands a shared helper for "derive a dev allowlist from a listen address".

Until then: **if you copy `allowedOrigins` out of an example, add
`127.0.0.1` to the empty-host arm yourself, or bind an explicit host.**

### Why the check runs before anything else

The origin check is the first thing the handshake does, before `Authenticate`,
before any allocation, and before a session identifier exists. That ordering is
what makes a flood of forged upgrades cost a header parse rather than a session.
It also means `403` is the loudest signal you get, and it goes to your access
log rather than to the browser.

**The client cannot tell you.** A refused upgrade reaches the runtime as an
abnormal close, which is not in its terminal set, so it retries forever with
backoff. A page that reconnects every few seconds while your log fills with
`403`s is the origin allowlist, every time.

### Two failure shapes that look like success

- **`live.New` accepts an allowlist that allows nobody.** It checks that
  `Origins` is non-empty; it cannot check that the strings are ones a browser
  will ever send. A `Config` naming `http://0.0.0.0:8080` starts cleanly and
  refuses every upgrade. The sample's spec asserts exactly this, as a warning
  rather than as a feature.
- **A spec asserting the allowlist is not `live.AnyOrigin` proves nothing.**
  That is the assertion that was in this repository while D-1 was live, and it
  passed the whole time. **An allowlist that allows nobody is not the
  wildcard.** If you write one spec about your origins, make it one that sends
  the header your browser sends.

---

## CSRF, and when `NoCSRFCheck` is defensible

`Config.CSRF` validates a token bound to the **authenticated application
session**, on the upgrade request, and a failure is `403`.

`live.NoCSRFCheck` is safe in exactly one shape: when `Config.Origins` is a real
allowlist and your application is single-origin. The origin check is then the
whole of the CSRF posture, which is a defensible position — a cross-origin page
cannot forge an `Origin` header. Turn the origin check off as well and you have
neither. Its own doc comment states that condition, and this page states it
again because the two fields are usually configured a week apart.

If you authenticate with a cookie, you need the token. An ambient credential is
exactly the case the origin check alone was not designed to be the last word on.

**One diagnostic quirk to know before you debug this.** A `CSRF` rejection is
counted under `gotthlive_connections_closed_total{code="forbidden_origin"}` —
the same label as an origin refusal — and both answer `403`. The metric does
not distinguish them. Distinguish them in your own hook's logging, or you will
be reading an origin allowlist that is fine.

---

## Content-Security-Policy

The client runtime is written to function under a strict policy: **no `eval`,
no `new Function`, no inline event-handler attributes, no inline `<style>`.**
That is PRD **FR-49**, and PRD **NFR-4** makes it a CI scan over the shipped,
minified artifact rather than over the sources — a claim about what actually
ships.

**The library sends no security header of its own.** Which headers an
application sends is the application's decision, and a library that set them
would be overriding a policy it cannot see the rest of. So this is yours:

<!-- sample: security/security.go -->
```go
const CSP = "default-src 'none'; " +
	"script-src 'self'; " +
	"style-src 'self'; " +
	"connect-src 'self'; " +
	"img-src 'self' data:; " +
	"object-src 'none'; " +
	"base-uri 'none'; " +
	"frame-ancestors 'none'"
```

<!-- sample: security/security.go -->
```go
func WithCSP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", CSP)
		next.ServeHTTP(w, r)
	})
}
```

**That exact policy is measured, not proposed.** `test/internal/conformance`'s
CP1-13 spec fronts a real application with a proxy that adds this header to
everything — including the upgrade — drives it with a real browser, and asserts
**zero** `securitypolicyviolation` events while the runtime boots, opens its
WebSocket and patches the DOM. The same spec injects an inline script and
asserts it does *not* execute, so a green result cannot mean the policy was
never enforced.

Three clauses worth reading twice:

- **`connect-src 'self'` is what the live WebSocket needs**, and `'self'` means
  the *page's* origin. If your live handler is served from a different origin
  than the page, this clause has to name that origin — the library cannot help,
  because the browser is enforcing a policy about a URL your own page built.
- **`script-src` needs no `'unsafe-eval'`**, and that is the NFR-4 claim.
- **`style-src 'self'` is enough for the runtime and the dev tools.** The
  inspector's panel and the dev-reload badge style through a constructed
  `CSSStyleSheet` adopted by a shadow root and through `element.style` property
  writes — both CSSOM, neither governed by `style-src`. Your own stylesheets are
  your own problem.

**A nonce-based policy does not work today, and this is the honest limit.**
`live.Script` renders `<script src="…" data-gotth-url="…" defer></script>` and
has no parameter for a nonce; neither does `InspectorScript` or
`DevReloadScript`. Under `script-src 'nonce-…'` with no `'self'`, the runtime
tag is blocked and the page never becomes live. A `'self'`-based policy is the
supported shape.

---

## Per-event authorization, and where a denial can be *rendered*

`Config.Authorize` runs **before the reducer, for every event, at the single
mailbox ingress**, so a new event kind cannot skip it and no fast path bypasses
it.

| Return | Effect |
|---|---|
| `nil` | the event is dispatched |
| `*live.DenyError` | the event is rejected, no state changes, the connection stays open |
| `*live.FatalDenyError` | the event is rejected and the connection closes with **4006 `UNAUTHORIZED`** |
| any other error | treated as a `DenyError` |

That last row is the one that matters: treating an unrecognised error as an
allow would give the hook the single failure mode an authorization hook must not
have. **`Reason` on both types is operator-facing** — the client is told the
event was not permitted and nothing more, because an authorization reason is an
authorization input.

### The idiom: enforce in the hook, render in the reducer

**A denial from `Authorize` cannot be shown to anyone.** It rejects the event
*before* the reducer runs, so there is no transition, so there is no render, so
there is nothing for the user to see. There is no patch hook and there is no
denial-render hook — by design — so a refusal that has to be *visible* has to
happen where markup is produced, which is the reducer.

The rule therefore divides by whether the person refused has to be told:

<!-- sample: security/security.go -->
```go
func Authorize(_ context.Context, sess live.Session[Member], ev live.Event) error {
	// No assertion. The session is typed by the identity Authenticate produced,
	// so "the identity is not what I expected" is a compile error rather than a
	// deny this hook has to remember to write.
	member := sess.Identity()

	if ev.Name == EventPurge && member.Role != RoleModerator {
		return &live.DenyError{Reason: member.Name + " is not a moderator and may not purge the room"}
	}
	return nil
}
```

An identity this application does not recognise is a session that should not be
open, and nobody needs to read an explanation of that. Purging is a moderator's
button that is not rendered for anyone else, so an event asking to purge did not
come from a rendered control — dropping it silently is the right answer.

**Posting as an observer is let through here on purpose**, because that refusal
has to be seen, and it is made in the reducer:

<!-- sample: security/security.go -->
```go
	case EventPost:
		if !s.CanPost() {
			s.Notice = ObserverRefusal
			return s, nil
		}
```

`Notice` is rendered by a fragment, so the browser gets a sentence instead of an
event that vanished. The reducer stays pure: it sets state and returns no
effect.

And the same rule is enforced a third time, inside the effect's own `Run`, which
is **not** redundant:

<!-- sample: security/security.go -->
```go
		if member.Role == RoleObserver {
			return fmt.Errorf("room: %s is an observer and may not post", member.Name)
		}
```

The reducer's refusal is what a reader **sees**; this one is what a reader
**cannot get past**, and it is here because an effect is reachable from anywhere
a reducer can be wrong — a new event name, a refactor, a branch nobody replayed.
The identity is a parameter of `Run` rather than something to fish out of a
context, which is what makes an effect that forgot to ask impossible to write.

**Rendering the button is courtesy, not enforcement.** A disabled composer and a
hidden purge button are good UX and no part of the security posture; a browser
that fabricates the event without the control still meets all three checks
above.

---

## What the client is trusted for, and what it is not

**Nothing.** Every byte arriving from a browser crosses a generated refinement
boundary before any application code sees it, and `ParseInbound` is the sole
entry point — there is no exported way to obtain an inbound payload that has not
passed through it.

In order, and a new frame kind cannot skip a step, because a conformance test
walks the payload `oneof` by protoreflect and asserts every member has both a
case in the switch and a generated `Refine` function:

| Check | What it enforces |
|---|---|
| `Conn.SetReadLimit` | `Limits.MaxInboundFrameBytes` (default **65,536**), applied **before any payload is allocated**. This is the authoritative bound — it bounds memory rather than rejecting after the fact |
| protobuf unmarshal | it is an encoded `gotthlive.v1.Frame` or it is a `4002` protocol violation. UTF-8 validity of string fields is checked here |
| envelope refinement | the generated boundary, on the frame envelope |
| version compatibility | major-version equality, in Go, with a human-readable reason. A mismatch is **never** resolved by reinterpreting fields |
| payload refinement | per field, and **per element** for repeated messages, which the generated boundary does not cover on its own |
| enum domain walk | every enum field against its descriptor — an out-of-domain value is not silently the zero one |
| list cardinality | `Event.fields` ≤ **64**, `Patch.updates` ≤ 64, `Snapshot.updates` ≤ 64 |
| cross-field invariants | the hand-checked list the predicate grammar cannot express |

The predicates themselves are in the schema, so they are readable rather than
folklore: an event `name` is at most 64 bytes matching `^[a-z][a-z0-9_.:-]*$`, a
`fragment_id` at most 64 matching `^[A-Za-z0-9_:.-]+$`, a form field's `key` at
most 128 and its `value` at most **8,192** bytes.

Two things this does **not** mean, and both are stated in `protocol.md` §10.3
so that no reader infers a stronger claim:

- **Predicate enforcement is directional.** The generated JavaScript codec
  enforces length predicates and nothing else — no numeric ranges, no `matches`,
  because an RE2 engine is not in the client size budget. That asymmetry is a
  generated, committed artifact you can read
  (`client/predicates.manifest.txt`), and CI fails if it drifts from the
  descriptors. It is also the right way round: the attacker is on the client
  side, where enforcement is total.
- **The sentence to use is "every byte the server accepts crosses a generated
  refinement boundary before any application code sees it."** Not "typed end to
  end in both runtimes." The docs are required to use the former phrasing.

**Client-reported telemetry is untrusted input and is treated as such.** The two
`gotthlive_client_*` histograms are bounded on the wire *and* dropped unless
they name a patch actually sent to that session and still inside the ack window.
They are named `client_*` so no dashboard mistakes them for a server
measurement.

**Rendering escapes by default.** Fragments are templ components and templ
escapes contextually; the raw-HTML path is templ's own explicitly named opt-in.
If a fragment renders something a user typed and you reached for the raw path,
that is the one line in your application worth a second reviewer.

---

## Session identity

| | |
|---|---|
| `Session.ID()` | **sixteen bytes from `crypto/rand`, minted by the server** at the handshake, and only after the `101`. It is server-minted so untrusted input can never name another session, and sixteen bytes wide so one patch frame captured in isolation is resolvable |
| `Session.Identity()` | whatever your `Authenticate` returned, bound at the handshake and **immutable for the connection's life** |
| `Identity.Subject()` | a stable, **non-secret** identifier. It reaches your logs and the provenance log, and it is the key `Limits.MaxSessionsPerIdentity` (default **20**) counts against. **It must not be a token** |

**A session cannot outlive its connection**, so there is no re-authentication
and no privilege change mid-session. A role change, a password reset, or a
revocation takes effect on the *next* connection.

**That has a consequence worth planning for: there is no way to terminate one
session from application code.** `App.Close` drains every session in the
process; nothing on `Session` closes just that one. The levers available for a
revoked user are: return a `*live.FatalDenyError` from `Authorize`, which
closes the connection with `4006` — but only when that session next sends an
event — and `Limits.IdleTimeout` (default **30 minutes**), which evicts a
session that has sent nothing. An idle session belonging to a user you revoked
one minute ago stays open until one of those fires. If your application needs
prompt revocation, check freshness in `Authorize` and treat the idle window as
the bound on how long a revoked session can sit there.

---

## The dev-only routes, and how to check they are off

`Config.Dev` gates three routes, and each has **two** independent gates: the
route answers `404`, and the component that would name it renders nothing. The
second is what covers a page you rendered; the first is what covers a bookmark,
a cached HTML document, or a scanner asking for the path directly.

| Path, under your mount | With `Dev` false |
|---|---|
| `gotth-live-inspector.min.js` | **404** |
| `gotth-live-dev-reload.min.js` | **404** |
| `gotth-live-dev-build` | **404** |
| `gotth-live.min.js` | **200** — the runtime is not a dev route |

Check it on the deployed process, not in your head:

```sh
for f in gotth-live-inspector.min.js gotth-live-dev-reload.min.js gotth-live-dev-build; do
  printf '%s ' "$f"
  curl -s -o /dev/null -w '%{http_code}\n' "https://app.example.com/live/$f"
done
# expect: 404 404 404

curl -s -o /dev/null -w '%{http_code}\n' https://app.example.com/live/gotth-live.min.js
# expect: 200
```

The `404` body names the switch rather than being blank, because the developer
who meets it has almost always forgotten to set `Dev`:

```text
gotth-live: the dev session inspector is served only when live.Config.Dev is true
```

Both directions are asserted in `_samples/security` — `404` with `Dev` false and
`200` with `Dev` true — because a `404` that is a missing route rather than a
gate proves nothing about the gate.

**One disclosed design position, so it is not discovered as a surprise.** The
inspector's and the dev-reload client's JavaScript are `//go:embed`ed into the
`live` package and are therefore *present in every binary*, production included.
They are not served, not referenced and not loadable with `Dev` false, and they
cost the shipped runtime nothing — the inspector reads frames off the WebSocket
and there is no seam for it anywhere in `client/runtime.js`. But the bytes are
in the file. That is stated in three documents and is a decision, not a gap.

---

## A configuration checklist

- [ ] `Config.Origins` is a literal list of the exact origins your page is
      served from — right scheme, no trailing slash, no default port, no
      `0.0.0.0`, no wildcard
- [ ] Nothing in the deployment reaches for `live.AnyOrigin`; one `grep`
      confirms it
- [ ] `Authenticate` derives an identity whose `Subject()` is stable and is
      **not** a token
- [ ] `CSRF` is a real token bound to the application session, unless the
      application is single-origin **and** `Origins` is a real allowlist
- [ ] `Authorize` refuses by default on an identity shape it did not expect
- [ ] Every rule a user must *see* refused is in the reducer, and every rule
      that must not be bypassable is also in `Authorize` or `Execute`
- [ ] The application sends a `Content-Security-Policy`; the library does not
- [ ] `Config.Dev` is false, and the three dev routes answer `404` on the
      deployed process
- [ ] `Limits.MaxSessions` and `Limits.MaxSessionsPerIdentity` are set to
      numbers you chose — see [deploying.md](deploying.md)
