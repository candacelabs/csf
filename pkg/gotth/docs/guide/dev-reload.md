# Dev reload

At the end of this page, saving a `.go` or `.templ` file rebuilds your
application, restarts it, and the page already open in your browser reloads
itself. You never touch the reload button, and you will know exactly what that
does **not** preserve — because the honest answer is "less than you might
assume", and knowing it is the difference between trusting the loop and being
surprised by it.

Like the [session inspector](inspector.md), it is a development tool that
cannot ship by accident. The mechanism for that is three `if`s, not a promise.

---

## Turn it on

Three things, and two of them you have already done if you use the inspector.

**1. Set `Dev` on the config.**

<!-- sample: none — your application's config, not a sample package. The API is compiled and its rendered output pinned by the FR-57 specs in live/devreload_test.go. -->
```go
app, err := live.New(live.Config[State]{
	// ...
	Dev: os.Getenv("ENV") == "dev",
})
```

**2. Render its tag in your layout — or use `app.Document`, which renders it
for you.**

`app.Document` emits this tag along with the runtime's and the inspector's, so
a page built on the shell needs nothing here.
[`examples/gotth/counter`](../../../../examples/gotth/counter) is that shape: `Page` takes the
`*live.App` and `NewMux` passes it, so the template is the same template in
both environments and in one of them two of the three tags render zero bytes.

For a hand-written layout:

<!-- sample: none — your layout. -->
```templ
templ layout(dev templ.Component) {
	<html>
		<head>
			@dev
			@live.Script("/live")
		</head>
		<body>
			{ children... }
		</body>
	</html>
}
```

…and pass `app.DevReloadScript("/live")` from wherever your page handler has
the app.

The mount path is the prefix your router reaches the live handler at, **as the
browser sees it** — the same string you give `live.Script`.

**Order does not matter.** Unlike `InspectorScript`, this tag wraps nothing and
reads nothing the runtime owns; it talks only to its own route, over HTTP. Put
it wherever you like.

**3. Run your application under a watcher.**

```
cd your-app
go run github.com/candacelabs/csf/pkg/gotth/internal/cmd/gotth-live-dev
```

Arguments for your own application go after `--`:

```
go run github.com/candacelabs/csf/pkg/gotth/internal/cmd/gotth-live-dev -- -addr 0.0.0.0:8080
```

Now edit a file and watch the browser.

### You do not have to use that watcher

Nothing in the browser half knows what rebuilt your process. **Any** tool that
rebuilds and restarts it works identically — `air`, `wgo`, `reflex`,
`entr`, `templ generate --watch` beside a shell loop, your editor's run
configuration. A new executable is a new build identity, and a new build
identity is a reload. The watcher shipped here exists so the feature has a
default that is in the repository, needs nothing installed, and is held by the
same CI as the library.

It lives under `internal/` and is still reachable from your own module: Go's
internal rule governs *imports*, and a `main` package named on the command line
is not imported by anything. (Verified, not assumed: `go build
github.com/candacelabs/csf/pkg/gotth/internal/cmd/gotth-live-dev`
succeeds from `examples/counter`, which is a separate module that merely
requires the library.) The alternative — a package at the module root — would
be a third exported package, which `internal/arch` caps at two.

---

## What happens on a change

A Go change and a templ change are **the same event**. templ generates Go, so
both mean the process is rebuilt and restarted; there is no faster path for one
of them and this page will not pretend there is.

| You change | The watcher runs | The browser |
|---|---|---|
| `.templ` | `templ generate`, `go build`, restart | reloads |
| `.go` | `go build`, restart | reloads |
| anything, and the build **fails** | prints the compiler error | **nothing** — the old build is still serving, and your session is still there |
| nothing (a touched file, an identical rebuild) | `go build`, restart | **nothing** — see below |

That third row is the one you will meet most often, and it is deliberate: a
typo costs you a red line in the terminal, not the session in the browser. Fix
it, save, and the next cycle restarts.

### Why a reload at all, when the runtime already reconnects

The client runtime has its own reconnect-and-resync state machine. When your
process goes away it notices, backs off, reconnects to the new one, and
re-establishes the live fragments — with no help from anything on this page,
and it did that before FR-57 existed.

What a resync **cannot** repaint is everything outside a live fragment:

- the page shell, the `<head>`, the classes on `<body>`, your navigation;
- a fragment whose *markup* changed while its *state* did not — `Dirty` says
  clean, so no patch is sent, so the browser keeps the old markup forever;
- the script tags themselves.

All of that was rendered once, by a process that no longer exists. Only a
document reload fixes it.

### How the browser knows

The server exposes a **build identity** at `<mount>/gotth-live-dev-build`, in
dev mode only. By default it is the first 12 bytes of a SHA-256 of the running
executable, computed lazily and once per process.

`DevReloadScript` stamps that identity **into its own script tag**, so the
baseline is the identity of the build that rendered *the document you are
looking at*. The client polls the route once a second (four times a second
while nobody is answering, which is what a restart looks like from the browser)
and reloads when the answer differs. Polling stops while the tab is hidden and
resumes the moment you come back to it.

The baseline is on the tag rather than fetched at boot on purpose. A client
that adopted its first fetched value as "what this page is showing" would
silently accept a rebuild that landed *while the page was loading* — which is
exactly what happens when you save a file during a reload.

### The one thing it puts on your page

While the server is not answering, a small marker appears in the bottom-right
corner:

```
● gotth-live: waiting for the server
```

It goes away the moment the server answers again, and it is **not there at all**
while everything is fine — the element is created on the first failed poll and
removed on the first successful one. It exists because the difference between
"my rebuild is taking eight seconds" and "my app crashed and nothing is coming"
is otherwise invisible: the page just sits there looking correct.

It is built the way the inspector's panel is built and for the same reason —
`element.style` property writes and a constructed `CSSStyleSheet` adopted by a
shadow root, never a `<style>` element and never a `style=` attribute — so it
opens under a strict CSP with no `'unsafe-inline'` and no nonce to offer. It is
given no server markup, so there is no `innerHTML` anywhere in the file.

Checked in a real browser rather than inferred, because a dev-tool view that
paints nothing is exactly the defect the inspector shipped once and had caught
only by opening it (`0c711b70`). In headless Chromium 151, with a `live`
session, the probe reads:

```
before:     {"mounted":false}
while away: {"mounted":true,"shadow":true,"sheets":1,
             "text":"gotth-live: waiting for the server",
             "dotColour":"rgb(245, 158, 11)","position":"fixed"}
```

### Same binary, no reload

Because the identity is a hash of the executable's bytes, it does **not** move
when the code does not. A crash-and-restart, a `docker compose restart`, or a
rebuild of source you did not really change all leave it alone: no reload, and
the runtime's reconnect brings the page back where it was. Go builds are
reproducible, so "did not really change" is a property the toolchain gives for
free.

If you need a different notion of "build" — a commit hash from `-ldflags`, an
image digest, a counter your own loop increments — set `Config.DevBuildID`. Any
value works as long as it *changes when the code changes and does not change
when it does not*; a constant turns dev reload off without turning `Dev` off.
It is validated at `live.New`: at most 128 bytes, no control bytes, no
surrounding whitespace.

If gotth-live cannot read the running executable at all, the identity becomes a
random per-process value prefixed **`process-`**. Every restart then reloads the
page. That is the safe direction — a page that reloads more often than it must,
rather than one showing stale markup — and the prefix is there so you can tell
which of the two you are getting.

---

## What it does NOT preserve

**Server-held session state does not survive the process that held it.** This
is the important paragraph on the page.

A restart ends every session. When the browser reconnects it mounts a **new**
session against the new build: `Config.Init` runs again, and the session's state
is whatever `Init` produces. Nothing here could preserve it — the state lived in
a process that no longer exists — and nothing here pretends to.

So FR-57's *"without losing the session where state permits"* means, exactly:

- the browser re-establishes itself **automatically**, and you never touch the
  reload button;
- a restart that rebuilt nothing does not even reload the document;
- what a restart *does* cost is what a restart always costs — the in-process
  state of the thing you restarted.

If your application's state lives outside the process — a database, Redis, a
store your `Init` reads from — it survives exactly as far as its own lifetime
allows. `examples/counter`'s store is in memory, so restarting it resets the
number to zero, and that is a property of the example rather than of dev reload.

Two smaller things it does not preserve, for completeness: **scroll position and
focus** are the browser's to restore across a reload and it usually does;
**client-side state your own JavaScript keeps on the page** is gone, because the
document is gone.

---

## The production gates

`Config.Dev` gates three things, and each is asserted in both positions in
`live/devreload_test.go`:

| With `Dev` false | |
|---|---|
| `DevReloadScript` | writes **zero bytes** — the page carries no tag, no mount, and no build identity |
| `GET <mount>/gotth-live-dev-reload.min.js` | `404`, with a body naming `live.Config.Dev` |
| `GET <mount>/gotth-live-dev-build` | `404`, with a body naming `live.Config.Dev` — a production build does not disclose its build identity, and does not compute one |

A production page therefore names no dev asset, exposes no build identity, and
makes no polling request. The template does not change between environments;
the tag simply is not there in one of them.

**What the flag does not do** is keep the bytes out of your binary. The
dev-reload client is embedded like any other asset, the same way the inspector
is; that limit is stated in the godoc on `clientDevReload` rather than implied
away. If it must be absent from a production binary, that is a build-tag change
this module does not currently make.

**It costs the shipped runtime nothing.** `live/clientjs/gotth-live.min.js` did
not change by one byte in the landing that added dev reload — 10,391 minified,
4,429 gzipped, before and after — and `client/test/bundle.test.mjs` holds that
going forward by asserting the shipped runtime contains no occurrence of
`dev-reload`, `devReload` or `gotth-live-dev` at all. The dev-reload client is a
third artifact with its own ceiling (`client/SIZE.md` §2.2).

---

## Evidence: it was run, in a real browser

Not asserted — measured, on 2026-08-05, in `dis-gotth-live-bench:latest`
(Chromium 151.0.7922.71, Go 1.26.5), driving `examples/counter` under
`internal/cmd/gotth-live-dev` over the DevTools protocol. A marker was set on
`window` before each edit; the marker being **gone** is what proves the document
reloaded rather than merely resynced.

```
=== 0. baseline ===
  [gen-0] h1="gotth-live counter" parity="even" build=a5da62db103615db6bc0c84e status=live marker=gen-0

=== 1. a templ change, OUTSIDE every live region ===
  reloaded by itself after 1810 ms; the marker set before the edit is gone
  [gen-1] h1="gotth-live counter [TEMPL CHANGE]" build=396b179a0ad409e2ed0979aa status=live marker=gen-1
  build identity moved: a5da62db103615db6bc0c84e -> 396b179a0ad409e2ed0979aa

=== 2. a Go change ===
  reloaded by itself after 2715 ms; parity now renders the changed Go string
  [gen-2] parity="even [GO CHANGE]" build=9461d36862c637074949f85d status=live marker=gen-2
  build identity moved: 396b179a0ad409e2ed0979aa -> 9461d36862c637074949f85d

=== 3. a rebuild that changes no bytes ===
  after 20 s: marker="gen-2" build=9461d36862c637074949f85d status=live
  no reload: the identity is the executable's hash, and the executable did not change

=== 4. restoring the sources ===
  reloaded after 1210 ms; h1 and parity are back to what is committed
  build identity now a5da62db103615db6bc0c84e (baseline was a5da62db103615db6bc0c84e: equal = true)
```

Four things that run says, and only one of them is the headline:

1. **A templ change reached the browser**, and the change was the `<h1>` —
   which is outside every live region, so no patch could ever have carried it.
   That is the case the reload exists for.
2. **A Go change reached the browser**, in 2.7 s including `go build`.
3. **A rebuild that changed no bytes reloaded nothing.** The process *was*
   restarted (the watcher logged `build ok in 76ms` and `restarted`), the socket
   dropped, the runtime reconnected, `status` came back to `live`, and the
   marker set before the restart was still there. That is the whole of "without
   losing the session where state permits", observed.
4. **Restoring the sources returned the identity to exactly its baseline
   value** — `a5da62db103615db6bc0c84e`, byte-for-byte the same string as at
   step 0. A content hash and a reproducible toolchain are what make that true,
   and it is the strongest evidence available that the identity is derived from
   the build rather than from the run.

The watcher's own output for the same run:

```
gotth-live-dev: watching /workspace/candace/pkg/gotth/examples/counter for [.go .templ .html .css]
gotth-live-dev: 8 source files
gotth-live-dev: templ generate ok
gotth-live-dev: build ok in 1.725s
gotth-live-dev: restarted — the browser reloads when it sees the new build identity
gotth-live-dev: changed: [view.templ]
gotth-live-dev: templ generate ok
gotth-live-dev: build ok in 499ms
gotth-live-dev: restarted — the browser reloads when it sees the new build identity
gotth-live-dev: changed: [counter.go]
gotth-live-dev: build ok in 497ms
gotth-live-dev: restarted — the browser reloads when it sees the new build identity
gotth-live-dev: changed: [counter.go view.templ]        # the mtime-only touch
gotth-live-dev: templ generate ok
gotth-live-dev: build ok in 76ms
gotth-live-dev: restarted — the browser reloads when it sees the new build identity
```

**The harness that produced this is a throwaway and is not committed**, exactly
as the inspector's browser check was. So this section is evidence for one tree
and **not a gate**: nothing in `ci.sh` re-runs it, and a future change could
break the browser half with every spec still green. The standing version belongs
in `test/internal/conformance/`, which already has the CDP client for it; that
is unbuilt work, named here rather than left implied.

---

## What runs in CI, and what does not

| Held by | What it covers |
|---|---|
| `live/devreload_test.go` (Ginkgo v2 + Gomega) | the three `Dev` gates in both positions, the tag's exact bytes and escaping, mount validation in both modes, `DevBuildID` validation, the no-store contract on the build route, and that the derived identity **is** the running executable's SHA-256 prefix |
| `client/test/dev-reload.test.mjs` (node:test) | the decision: same build, different build, sticky reload, a 200 that is not an identity, a missing baseline, the four-step poll cadence at its boundaries, and how a refused connection and a 502 are read |
| `client/test/bundle.test.mjs` (node:test) | the shipped artifact: no `eval`, no remote fetch, strict-CSP-compatible, and the runtime carrying **no dev-reload seam** |
| `internal/cmd/gotth-live-dev` (Ginkgo v2 + Gomega) | the watcher: which files are watched, `.git`/`node_modules` skipped, added/removed/modified all reported, a failed build leaving the running process alone, and the interrupt-then-kill stop |
| **nothing** | the loop end to end in a browser. See the section above |

---

## Next

- [The session inspector](inspector.md) — the other dev-only artifact, and the
  one whose tag order *is* load-bearing.
- [Error handling](error-handling.md) — what a connection that closes looks
  like from the page, which is what you are watching during every restart.
