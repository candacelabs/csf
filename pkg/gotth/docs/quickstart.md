# Quickstart

At the end of this page you have a live page: a number that lives in a Go
process, a button that changes it, and a WebSocket that repaints the number
without a reload. No JavaScript of yours runs on it.

The application is **20 lines of Go and 11 lines of templ markup**: the two
files below, counting every line that is not blank, not a comment, and not a
`package` or `import` line — where an `import` declaration is one exclusion,
parenthesised block and closing paren included, however your formatter chose to
group it. **Not counted, because you do not write them:** `go.mod`, the
generated `view_templ.go`, and the shell commands on this page. Twelve of the 20
are the seven `Config` fields the library requires — an eighth, `Init`, is
optional and this application does not write it. Everything else on this page is
explanation, and you can skip it and come back.

**That is 31 against the ≤31 this project set itself** (PRD FR-53). It was **39**
until `app.Document` — the library-owned page shell — absorbed the eight lines
of `<!DOCTYPE>`, `<html>`, `<head>` and `<body>` every page of every live
application was writing out by hand. **This page states the measurement and does
not grade it**: the count of record is QA-1's, taken with a timer from these
docs alone, and [`docs/gates/phase-4.md`](gates/phase-4.md) §4.2 is where it
lands.

---

## Before you start

- **Go 1.26 or newer.** `gotth-live/go.mod` declares `go 1.26.0`.
- **Nothing else.** No node, no npm, no bundler, no CDN, no protoc. The client
  runtime is compiled into your binary and served by the same handler that
  serves the WebSocket. The library's own generated code — the protobuf codec
  and the minified client — is committed, so `go build` on a clean clone works
  with no generator installed.
- **`templ`. §3 has you write `view.templ`, so on this page it is required.**
  templ compiles `view.templ` into `view_templ.go`, and you commit the result.
  Install it with `go install github.com/a-h/templ/cmd/templ@v0.3.1020`. Two
  ways out, neither of them the path this page takes: copy the generated
  `view_templ.go` from
  [`docs/guide/_samples/quickstart/`](guide/_samples/quickstart) beside a
  `view.templ` you do not edit, or write your fragments as ordinary Go
  returning a `templ.Component`. The **library** needs no generator on a clean
  clone; your own `.templ` files do.

---

## 1. The module

```text
mkdir counter && cd counter
go mod init example.com/counter
```

Your `go.mod` requires two things:

```text
require (
	github.com/a-h/templ v0.3.1020
	github.com/candacelabs/csf v0.1.0
)
```

> **v0.1 is not published yet.** During this bootstrap, point that module at a
> checkout of the monorepo it is generated from:
>
> ```bash
> go mod edit -replace github.com/candacelabs/csf=/path/to/the/checkout/candace
> ```
>
> It used to take two replace directives, one for the library and one for the
> Liquid Proto runtime it links. They are one module now, and this replace goes
> away when `candacelabs/csf` is published.

---

## 2. The application

Two files, both `package main`, in one directory: `main.go` here and
`view.templ` in §3. Each block below is complete — imports included — and
compiles as printed.

<!-- sample: quickstart/main.go -->
```go
package main

import (
	"log"
	"net/http"

	"github.com/candacelabs/csf/pkg/gotth/live"
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
var app = live.MustNew(live.Config[State, live.AnonymousIdentity]{
	Reduce: func(s State, ev live.Event) (State, []live.Effect[live.AnonymousIdentity]) {
		if ev.Name == EventInc {
			s.N++
		}
		return s, nil
	},
	Fragments:    []live.Fragment[State]{{ID: "count", Render: Count}},
	Events:       []string{EventInc},
	Origins:      []string{"http://127.0.0.1:8080"},
	Authenticate: live.Anonymous,
	Authorize:    live.AllowAll[live.AnonymousIdentity],
	CSRF:         live.NoCSRFCheck,
})

func main() {
	log.Fatal(http.ListenAndServe("127.0.0.1:8080", app.Mux(MountPath, app.PageHandler(Page))))
}
```

**`live.MustNew` is `live.New` with the error turned into a panic.** `New`
returns `(*App[State], error)` and reports a `*live.ConfigError` naming the
field at fault and what to set it to; `MustNew` returns the application or
panics with that same error. It belongs in `main` or in a package-level `var`
like this one, where a `Config` is a literal you wrote and there is nothing to
do with the error but print it and stop — `template.Must`'s bargain. Anywhere
you could act on it, use `New`.

**Why `app` is a package-level `var`.** `app.Document` in §3 is a *method*, so
the view has to be able to reach the application — and `app.PageHandler(Page)`
hands `Page` the state and nothing else. A package-level `var` is the shortest
answer and costs nothing: it is the same line that said `app := live.MustNew(…)`
inside `main`, moved up. If you would rather not have one, pass the app into the
component instead — `templ Page(app *live.App[State], s State)` — which is what
[`examples/gotth/counter`](../../../examples/gotth/counter/README.md) does, because there the
application is built from command-line flags and cannot be a package-level
value.

### The three routes `app.Mux` registers, and why the live handler is registered twice

`app.Mux(MountPath, page)` makes three registrations. They are not decoration,
and the failures the second one prevents are silent — a page that renders
perfectly and does nothing:

| Pattern | What it serves |
|---|---|
| `MountPath` | the **WebSocket upgrade**, at exactly `/live` and nowhere else |
| `MountPath + "/"` | the **subtree**: `/live/gotth-live.min.js`, and — only when `Dev` is on — the inspector client, the dev-reload client and dev reload's build-identity route |
| `"/"` | the page you passed — and, because `"/"` is a **catch-all** in `net/http`, every path the two patterns above do not claim, `/favicon.ico` included |

**`Mux` exists because writing those three by hand has two failure modes and
neither of them says anything.** The catch-all is what makes a missing subtree
registration silent: the runtime's URL is answered by the page, with a `200`,
and the browser hands your HTML to its JavaScript parser. Measured, one variant
against the other, in headless Chromium:

| Probe | As written | Subtree line deleted |
|---|---|---|
| `GET /live/gotth-live.min.js` | `200 text/javascript`, 10,387 B | **`200 text/html`, 301 B** — the page itself |
| `data-gotth-status` on `<html>` | `live` | **absent** — never even `connecting` |
| WebSockets attempted | 1 | **0** |
| Clicking `+1` | `0` → `1` → `2` | **inert, stays `0`** |
| Server-side error | none | **none** |
| Browser console | clean | **one `Uncaught SyntaxError: Unexpected token '<'`** |

There is no `404` in that column and there cannot be one: the catch-all answers
everything. §4 step 2 is written against the **content type** for this reason,
and this is the one failure on the page where the console holds the only
evidence.

**You do not need `http.StripPrefix`, at any prefix.** The handler routes by
path *suffix*, so it works wherever you mount it and never has to be told its
own prefix. `app.Handler()` is still there and is how you mount on a router of
your own — `Mux` is a convenience over it, not a replacement for it. This is
exactly what `Mux` does, written out, so you can put the same two patterns on a
mux you already have:

<!-- sample: mounting/mount.go -->
```go
func Routes(mountPath string, app, page http.Handler) http.Handler {
	mux := http.NewServeMux()
	// The WebSocket upgrade, at exactly this path.
	mux.Handle(mountPath, app)
	// The subtree: gotth-live.min.js, and the dev-only routes.
	mux.Handle(mountPath+"/", app)
	// The catch-all: the page, and every path neither pattern above claims.
	mux.Handle("/", page)
	return mux
}
```

Stripping is not merely unnecessary, it breaks the upgrade — and it breaks it
as a redirect, which a WebSocket client cannot follow. Measured with `curl`
against the same application under four mountings (Go 1.26):

| Mounting | The upgrade, `GET <mount>` | The runtime, `GET <mount>/gotth-live.min.js` |
|---|---|---|
| both patterns, unstripped — the code above, at `/live` | **`101`** | `200 text/javascript` |
| both patterns, unstripped, at `/app/ui` instead | **`101`** | `200 text/javascript` |
| subtree only, `http.StripPrefix` | **`307` → `/live/`** | `200 text/javascript` |
| both patterns, `http.StripPrefix` on each | **`307` → `/`** | `200 text/javascript` |

The third row is what a reader writes who assumes one registration is enough;
the fourth is the repair they reach for next, and it is worse — stripping the
mount path from the exact pattern leaves the empty path, which the live
handler's own mux redirects to `/`, so the upgrade lands on your page. Both
rows are a page that reconnects forever, because the runtime cannot tell a
rejected handshake from an unreachable one.

### The first paint comes from the mount hook, on every request

`app.PageHandler(Page)` renders the page **when a request arrives**: it calls
`Config.Init` for that request, and hands the state it returns to `Page`. So the
bytes a visitor is served and the first snapshot their session receives come
from one function, and there is nothing to keep in step.

**The obvious spelling does not do that, and its failure is silent.**
`templ.Handler(Page(State{}))` builds the component when `main` runs — the
argument is a state *value*, evaluated once — and serves those bytes to every
visitor for the life of the process. That is correct exactly while `Init`
returns `State{}` too. Measured on the version of this page that used it, with
`Init` changed to return `State{N: 41}` and nothing else touched: every response
still carried `<output>0</output>`, corrected to `41` only once the WebSocket
connected, and not at all with JavaScript disabled. `PageHandler` cannot be
given a state value — only the function that renders one — so that edit is not
available through it.

Three things it does per request, in order, all of them worth knowing before you
give `Init` something real to do:

1. **`Config.Authenticate` runs on the page request**, and the identity it
   derives is the one `Init` is given. The page is painted for the identity the
   socket will bind to. If `Authenticate` returns an error the page is `401` —
   the same status that visitor's *upgrade* would get, because a page whose
   socket is going to be refused is a page that cannot work. If you want a
   logged-out visitor to get a page, have `Authenticate` return an anonymous
   identity rather than an error; that is also what makes their upgrade succeed.
2. **`Config.Init` runs, with that identity and the zero session id.** No
   session exists yet and none can — a session is minted at the handshake, and
   this is a different request. `Init` is therefore a **loader**: it is called
   once per page request as well as once per session, so it must be safe to call
   for a read. The effects it returns are discarded here and performed only for
   a real session.
3. **`Page` renders into a buffer**, which is then written. A render that fails
   half way through is a `500`, never a `200` carrying half a document. `Init`
   failing is a `500` too, and in both cases the reason goes to `Config.Logger`
   rather than to the browser — unless `Dev` is set, when it is in the body as
   well.

This is a case of a general rule, and the rule is worth reading before you add
a second fragment:
[the page and the fragments render the same components, from the same state](guide/fragments-and-dirty-tracking.md#the-state-the-page-renders-is-the-state-init-returns).

### What each `Config` field is for

`Config[S]`'s zero value is invalid, and `live.New` returns a `*live.ConfigError`
naming the field at fault and what to set it to — which is the value
`live.MustNew` panics with. Every mistake below is a startup failure, not a
session that misbehaves later.

| Field | Required | What it is |
|---|---|---|
| `Init` | no | The mount hook. Runs once per session before the first snapshot, and once per request through `app.PageHandler`; returns the initial state and any startup effects. **Nil means the zero value of your state, no effects, no error** — which is why this application does not write it. It is the one required-looking field with a defensible default: the zero value is the only total, side-effect-free reading of an unwritten mount hook, and forgetting it is visible on the first run, because the sessions and the page both start empty together. |
| `Reduce` | yes | The pure state transition. No I/O, no clock, no randomness, no goroutines, no mutating its input. |
| `Fragments` | yes, non-empty | The server-owned live regions. Each has a stable `ID` and a `Render` that is a pure function of state. |
| `Events` | yes, non-empty | The event names this application accepts. |
| `Execute` | only if you return effects | Performs one effect at the actor boundary. This application returns none, so it is nil. |
| `Teardown` | no | Runs after the session exits, with final state. Where a mount-time subscription is released. |
| `Origins` | yes | The `Origin` allowlist. |
| `Authenticate` | yes | Derives the session identity from the upgrade request. |
| `Authorize` | yes | Runs before the reducer, for every event — on the connection's read pump, ahead of the mailbox, so it must not block. See "What actually happened". |
| `CSRF` | yes | Validates a token bound to the authenticated application session. |
| `Limits` | no | Resource bounds; a zero field takes its documented default. `live.DefaultLimits()` prints them. |
| `Logger`, `Metrics`, `Tracer` | no | [guide/observability.md](guide/observability.md). |
| `Dev` | no | One switch, three dev-only things: the panic value and its stack go into the error frame a contained panic produces; the [session inspector](guide/inspector.md) is served; and [dev reload](guide/dev-reload.md) is served, so a rebuilt process reloads the open page. **Must be false in production**, and each of the three is gated in both directions rather than by convention. |

### The security defaults, and the four ways out

The four security fields are **required**, and that is the design: there is no
nil that means "off". `live.New` refuses a `Config` that leaves one unset, so
turning a check off is something you write down.

**`Origins` is deny-by-default and has no wildcard.** There is no reflection of
the request's own `Origin`, and a request that sends none is refused — an absent
`Origin` is not an allowed one. The check runs on the upgrade request *before*
any per-session memory is allocated, and a failure is a `403`.

The other three are turned off by naming one of four values. Each is a
**named, greppable** symbol, so auditing every deployment that turned a check
off is one search:

```text
grep -rn 'live\.AnyOrigin\|live\.Anonymous\|live\.AllowAll\|live\.NoCSRFCheck'
```

| Escape hatch | Field | What it does | What replaces it in production |
|---|---|---|---|
| `live.AnyOrigin` | `Origins` | Disables origin validation entirely. | A real allowlist of the origins your page is served from. |
| `live.Anonymous` | `Authenticate` | Binds every session to one anonymous identity. | The session cookie or bearer token your application already trusts, turned into an `IIdentity` whose `Subject()` is stable and non-secret. |
| `live.AllowAll` | `Authorize` | Permits every event. | The check that says which identities may raise which events. |
| `live.NoCSRFCheck` | `CSRF` | Performs no check. | A token bound to the authenticated application session. |

**What a localhost quickstart may legitimately use, and what it may not.** The
application above uses three of the four, and does not use the fourth:

- `live.Anonymous` is honest here: there are no accounts, so there is no
  identity to derive. `live.AllowAll` is honest for the same reason — there is
  no rule about who may count.
- `live.NoCSRFCheck` is safe here **only because `Origins` is a real
  allowlist**. With the origin check in force it is the whole of the CSRF
  posture, and that is a defensible position for a single-origin application.
  Turn the origin check off as well and you have neither.
- **`live.AnyOrigin` is not used, and should not be**, even on localhost. The
  allowlist for a local application is one string you already know. Reach for
  `AnyOrigin` only when you are serving the page from an origin you cannot
  predict — another machine on your network, a tunnel — and take it out again
  when you are done. Its doc comment says "never use it outside local
  development"; this page adds that most local development does not need it
  either.

`Dev` is a fourth thing to remember to turn off, and it is not an escape hatch:
it puts a panic value and its stack in the error frame the browser receives. It
is left false above. Set it while you are learning what the library refuses, and
never in production.

---

## 3. The view

<!-- sample: quickstart/view.templ -->
```templ
package main

// templ itself is not imported here: the generator adds "github.com/a-h/templ"
// to every file it writes, so naming it again is a redeclaration in the
// generated output rather than a missing import.
import (
	"strconv"

	"github.com/candacelabs/csf/pkg/gotth/live"
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

Three helpers, and nothing else:

- **`live.Region("count")`** renders `data-gotth-region="count"`. It marks the
  root of a live fragment, and it is a contract: morph never touches anything
  outside a region, and a patch names this ID. `Fragments[i].ID` and this string
  must be the same.
- **`live.On("click", EventInc)`** renders `data-gotth-on="click:count.inc"`.
  There is no element id, no handler registration, and no JavaScript: one
  delegated listener on the document reads the attribute. That is why a morph
  can replace this markup without destroying a binding. **The bound control has
  to be inside a region** — the runtime resolves an event's fragment by walking
  up to the nearest `data-gotth-region` ancestor, and a control outside every
  region raises nothing at all, silently.
- **`app.Document(MountPath, title, htmlAttrs)`** is the page shell. It writes
  the doctype, the `<html>` element, the `<head>` with the character encoding
  and the title, the `<script>` tag for the runtime the handler serves, and the
  `<body>` around everything you put between its braces. Its first argument is
  **the prefix the live handler is reachable at as the browser sees it** — after
  any rewriting your router, or a proxy in front of it, does. That is knowledge
  only you have: the tag renders on the page request and the handler sees the
  upgrade on a different request entirely, so nothing inside the library can
  compare the two. Get it wrong and the page loads, the script is 404ed or
  answered by your catch-all, and nothing is live — which is why `MountPath` is
  one constant used twice rather than two string literals. The mount itself
  needs no `http.StripPrefix`, whatever the prefix; §2's mounting table has the
  measurements.

**What `Document` decides and what stays yours.** It owns the doctype, the
charset — first in the head, where a browser that has not found one yet is still
guessing — and, in dev, the *order* of the runtime, [session
inspector](guide/inspector.md) and [dev reload](guide/dev-reload.md) tags, which
is the one thing on this page you could get wrong invisibly: the inspector has
to wrap the WebSocket constructor before the runtime opens a socket, and a
deferred script that runs second wraps nothing. You never write those three, so
you cannot order them wrongly.

Everything else is yours and is an argument rather than a default:

- the **title** is required and there is no default for it;
- the **`<html>` attributes** are whatever you pass and nothing else — `lang` is
  a decision about your document, not about your live connection, so the library
  never supplies one;
- a fourth, variadic argument is **extra head content** — a `<meta
  name="viewport">`, your stylesheet, a script of your own. It renders above the
  runtime tags, and a page that needs none, like this one, does not pay for it.
  The one thing it may not carry is `live.Script`: `Document` renders that tag
  itself, below the inspector's, and a second one from up here would land
  *above* the inspector and stop it seeing anything. That is an error rather
  than a quietly broken page — see [the inspector guide](guide/inspector.md).

For a page in a live application that is deliberately **not** live — a login
page with no regions on it — pass `live.NoRuntime` as the mount path and no
script tag is written at all. It is a named value rather than an empty string
because a forgotten mount would otherwise give you a page that loads perfectly
and does nothing.

`Document` refuses a mount path that is not a path: empty, not beginning with
`/`, or containing `//` anywhere, `\`, `?`, `#`, or a control byte — the same
rule `live.Script` applies, by the same function. It also refuses an empty
title. `Render` then returns an error and writes nothing, so you get a 500 on
the page request instead of a blank page or half a document.

The full `data-gotth-*` vocabulary is in
[`client/SIZE.md` §7](../client/SIZE.md); you should not need to write one by
hand.

---

## 4. Build and run

```bash
templ generate          # §3 had you write view.templ, so this step is required
go mod tidy             # writes go.sum, which nothing above this line writes
go run .
```

**`go mod tidy` is not optional and its order is not arbitrary.** §1 gave you a
`go.mod`; nothing gave you a `go.sum`, and without one `go run .` stops before it
compiles a line, with a screen of `missing go.sum entry` errors pointing at
`templ` and at files inside this library. That is what this line prevents, and
it is the only stop on this page. It runs **after** `templ generate` because
`view.templ` is not Go: until the generator has written `view_templ.go`, nothing
in your module imports `github.com/a-h/templ`, and `tidy` would drop the
requirement §1 had you write.

If you meet those errors anyway — a stale `go.sum`, or §4 run out of order — the
error text prints two different remedies and only one of them finishes.
`go get example.com/counter`, on the first line, **works**: it exits cleanly and
leaves a `go.mod` byte-identical to `tidy`'s and a smaller `go.sum` that builds.
`go mod download github.com/a-h/templ`, on most of the other lines, does not: it
succeeds, and the next build fails on the next missing entry.

Open <http://127.0.0.1:8080>.

While you are working, `Dev` plus a watcher will rebuild, restart and reload
the open page on every save — [guide/dev-reload.md](guide/dev-reload.md). It is
off above, and the rest of this page assumes it stays off.

**Verify it, in this order.** Each step fails differently, so the order is the
diagnosis:

1. **The number renders in the page source.** `curl -s http://127.0.0.1:8080`
   shows `<output>0</output>` and a `<script src="/live/gotth-live.min.js"
   data-gotth-url="/live" defer></script>` tag. That first paint is
   server-rendered HTML, not a placeholder — and it is whatever `Config.Init`
   returned for *this request*, rendered by `Page`. If the whole page is
   missing, the render returned an error — check the mount path and the title
   you gave `app.Document`.
2. **The runtime loads, and it is JavaScript.**
   `curl -sI http://127.0.0.1:8080/live/gotth-live.min.js` returns `200` with
   `Content-Type: text/javascript` and about 10 KB
   (`Content-Length: 10387` at HEAD; `client/SIZE.md` §1 is the current figure
   and it moves whenever the runtime does, so **match the order of magnitude,
   not the digits**). **Read the type, not the status.** A `404` here would need a router with no catch-all; with the `"/"`
   registration §2 shows, the failure is `200` with `Content-Type: text/html`
   and the length of your page — the catch-all answering a URL the live
   handler's subtree registration should have claimed. `curl -sI` prints both.
3. **The connection opens.** The `<html>` element gets
   `data-gotth-status="connecting"` and then `"live"`. It is written by the
   runtime and you can select on it from CSS — that is how the examples draw a
   connection dot without a line of script.
4. **Clicking works.** The number changes. Nothing in the browser held it.
5. **Reloading resets it to 0.** That is correct here: with no `Init` written,
   every session mounts at the zero `State`, and a reconnect is a new session
   with a fresh mount. Sharing a value across tabs and reloads is an effect over
   state your application owns —
   [guide/effects-and-server-push.md](guide/effects-and-server-push.md).

**If step 3 never reaches `"live"`, look at the network tab, and look there
rather than at your terminal.** The origin, authentication and CSRF checks all
run on the upgrade **request**, before a WebSocket exists, so a rejection is an
HTTP status on the handshake and not a close code — and **the application above
sets no `Config.Logger`, so it prints nothing at all when it refuses one**. There
is no server-side evidence of any row in this table until you give it a logger;
until then the handshake row in the network tab is the whole of it. Filter by
`WS`, and read the status on the request itself.

| Handshake response | Means |
|---|---|
| `403 forbidden origin` | the `Origin` your browser sent is not in `Config.Origins`. **On this page that is almost always `localhost` against an allowlist that says `127.0.0.1`.** They are two different origins to a browser and to this check, and the address habit types is not the one §2 listed. Open <http://127.0.0.1:8080>, or add `"http://localhost:8080"` to `Origins` beside the entry already there. |
| `401 unauthenticated` | your `Authenticate` hook returned an error. |
| `403 forbidden` | your `CSRF` hook returned an error. |
| `426` | the handshake did not carry the subprotocol this build requires. **That is the condition, not the diagnosis.** Usually it means the page and the binary are different versions; but a request that sends no `Sec-WebSocket-Protocol` header at all — a hand-rolled client, a proxy that strips the header — gets the same `426` from an origin that is otherwise perfectly allowed. |
| `307` or `301`, with a `Location` | your router redirected instead of upgrading, and a WebSocket client cannot follow a redirect on a handshake. Either the mount path is registered only as a subtree (`Location` is your mount path with a trailing slash), or `http.StripPrefix` stripped the exact pattern to nothing (`Location` is `/`). §2's mounting table has both, measured. |

The runtime cannot tell those apart: a failed handshake surfaces as an abnormal
close, which is not in its terminal set, so it will **retry forever with
backoff**. A page reconnecting every few seconds is almost always the origin
allowlist, and the tell is a network tab filling with `403`s — one per retry,
against a silent terminal.

**Set `Config.Logger` when you want the server's side of that.** It is what
turns a refusal into text, and the line it writes for this one names the field
to change — *"refused an upgrade from a disallowed origin: add it to
`Config.Origins` if it is yours"*, with the offending origin attached. What to
give it is [guide/observability.md](guide/observability.md). It is deliberately
not in the application above: this page counts the lines that application costs,
and the budget is the subject of the first paragraph.

**The one failure on this page where the console holds the only evidence** is
step 2's, and it is not a handshake failure at all. The network tab shows
nothing but green `200`s; the sole clue is a single
`Uncaught SyntaxError: Unexpected token '<'`, from the browser parsing your HTML
page as JavaScript. The tell is that `data-gotth-status` is not stuck at
`connecting` but **absent** — no upgrade was ever attempted, because the runtime
never ran.

Close codes proper — the ones a session gets after it was live — are in
[guide/error-handling.md](guide/error-handling.md).

---

## What actually happened

```text
browser              the connection's read pump        the session goroutine
   │
   │ data-gotth-on="click:count.inc"
   │──── Event frame ──▶ rate limit, is the name
   │                     registered, Authorize
   │                             │
   │                             └──── mailbox ──────▶ Reduce(state, ev)  → N = 1
   │                                                   render the fragments Dirty named
   │                                                   hash, compare, and skip an
   │                                                   identical render
   │◀─────────────── Patch frame ─────────────────────  one fragment's markup
   │
   morph
```

**Two goroutines, and the split is where the security property lives.** Every
frame this connection carries is read by one goroutine, and it is the one that
runs `Authorize` — ahead of the mailbox, so an event that is not permitted never
costs the session a slot. Everything after the mailbox is the session's own
goroutine, alone with the state.

Five properties of that path are worth knowing before you build on it:

- **One goroutine owns each session's state and is the only writer.** Your
  reducer never needs a mutex, and no session's state is reachable from another
  session's goroutine.
- **`Authorize` runs on the connection, not on that goroutine**, which makes it
  the sharper of the two blocking hazards. A slow `Reduce` stalls one session's
  mailbox; a slow `Authorize` stops the connection being read **at all** — acks
  and heartbeats included — so the server's liveness clock stops advancing and a
  long enough stall ends the session with close code **4010
  `HEARTBEAT_TIMEOUT`** — a self-inflicted close that looks exactly like a
  network problem. So keep it a decision over data you already have: if it needs
  a database, cache the answer at `Init` and re-check it in `Execute`, where
  blocking is free. [guide/lifecycle-hooks.md](guide/lifecycle-hooks.md) has the
  hook-by-hook table and
  [guide/architecture.md](guide/architecture.md#authorize-is-ahead-of-the-mailbox-not-behind-it)
  has the pipeline and the spec that holds the claim.
- **The reducer is pure, and that is load-bearing rather than stylistic.** An
  event log replays to a byte-identical result, an identical render is
  suppressed instead of sent, and a panic leaves the pre-transition state intact
  and correct.
- **Events are at-most-once; patches are exactly-once and in order.** An
  interaction in flight when the connection dropped is not retried, and the user
  sees server truth after the resync.
- **A session lives exactly as long as its connection.** There is no resume and
  no grace window: a reconnect mounts a fresh session and receives a fresh
  snapshot, which is the same path a deploy takes.

---

## Where to go next

| You want to | Read |
|---|---|
| Bind more than a click — forms, keys, debounce | [guide/events-and-forms.md](guide/events-and-forms.md) |
| Stop re-rendering everything on every event | [guide/fragments-and-dirty-tracking.md](guide/fragments-and-dirty-tracking.md) |
| Share state between tabs, or push from the server | [guide/effects-and-server-push.md](guide/effects-and-server-push.md) |
| Subscribe on mount and clean up on teardown | [guide/lifecycle-hooks.md](guide/lifecycle-hooks.md) |
| Wire metrics, traces, and the provenance log | [guide/observability.md](guide/observability.md) |
| Keep HTMX on the same page | [guide/htmx-interop.md](guide/htmx-interop.md) |
| Test the reducer and the hooks | [guide/testing-your-app.md](guide/testing-your-app.md) |
| Handle failures, denials, and close codes | [guide/error-handling.md](guide/error-handling.md) |
| Stop reaching for the reload button while you work | [guide/dev-reload.md](guide/dev-reload.md) |
| See the causal chain of the session in front of you | [guide/inspector.md](guide/inspector.md) |
| Decide **against** this library, on evidence | [guide/when-not-to-use-this.md](guide/when-not-to-use-this.md) |

A larger version of this application, with the value shared across every tab, is
[`examples/gotth/counter`](../../../examples/gotth/counter/README.md).
