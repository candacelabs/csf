# HTMX interop

At the end of this page you can put HTMX-driven markup on the same page as live
regions, in either of the two places it can safely go, and you will know exactly
what happens when you put it in the third.

Compiled source: [`_samples/htmxinterop`](_samples/htmxinterop).

---

## The rule

> **Innermost declaration wins.** Inside a declared live fragment, a node marked
> `data-gotth-preserve` and its subtree are never touched by morph. An `hx-*`
> element inside a live fragment **without** `data-gotth-preserve` is
> server-owned: morph will overwrite it, and any HTMX swap into it will be
> reverted by the next patch.

That is a documented, tested precedence rule and **not** a runtime check. The
library does not scan rendered HTML for `hx-*` attributes — that would cost CPU
on every render to catch a developer-time mistake — so nothing warns you. The
failure is visible instead: a swap that works once and disappears at the next
patch.

---

## Place 1: outside every live region

The cheapest arrangement, and the one to prefer when the markup does not have to
sit inside a live region.

<!-- sample: htmxinterop/view.templ -->
```templ
templ DeploysRegion() {
	<section id="deploys">
		<button hx-get="/htmx/deploys" hx-target="#deploys-out" hx-swap="innerHTML">Load</button>
		<div id="deploys-out">—</div>
	</section>
}
```

It is not a fragment, it is not in `Config.Fragments`, and no patch can name it,
so morph never reaches it and `live.Preserve` is not needed. gotth-live neither
intercepts nor rewrites the request its button makes: that is an ordinary `GET`
to an ordinary `http.Handler`.

---

## Place 2: inside a live region, behind `live.Preserve`

<!-- sample: htmxinterop/view.templ -->
```templ
templ ControlsRegion(paused bool) {
	<section { live.Region("controls")... }>
		if paused {
			<button { live.On("click", "feed.resume")... }>Resume</button>
		} else {
			<button { live.On("click", "feed.pause")... }>Pause</button>
		}
		<div { live.Preserve()... } id="notes">
			<button hx-get="/htmx/notes" hx-target="#notes-out" hx-swap="innerHTML">
				Load operator notes
			</button>
			<div id="notes-out">—</div>
		</div>
	</section>
}
```

`live.Preserve()` renders `data-gotth-preserve`. It marks the element **and its
whole subtree** as never morphed, which buys two things at once:

- HTMX's swap into the island survives every patch to the surrounding region;
- and the `hx-*` element HTMX processed at page load is never replaced by markup
  HTMX has not seen, so HTMX never finds itself looking at an attribute it did
  not initialise.

The second is the one that bites without `Preserve`, and it bites *silently*:
morph replaces an `hx-get` button with an identical-looking one, HTMX has no
listener on the new node, and the button stops doing anything.

---

## Place 3: inside a live region, unpreserved — what actually happens

Nothing warns you. In order:

1. The page loads. HTMX processes the `hx-*` element.
2. The user clicks it. HTMX swaps a response into the target.
3. Any transition marks that fragment dirty. The fragment re-renders from
   **server state**, which knows nothing about the swap.
4. The patch morphs the region. The swapped content is gone, and the `hx-*`
   element is a new node HTMX has not processed.

Step 4 is not a bug in either library. The region is server-owned by
declaration, and the server's render is the truth about it.

---

## Serving the HTMX side

<!-- sample: htmxinterop/htmx.go -->
```go
func NotesHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = NotesList(Notes, time.Now().UTC().Format(time.TimeOnly)).Render(r.Context(), w)
	})
}
```

Register it on the same router as the live handler:

```text
mux.Handle("/live", app.Handler())
mux.Handle("/live/", app.Handler())
mux.Handle("/htmx/notes", htmxinterop.NotesHandler())
```

The two paths share nothing. An `hx-get` never touches the live session, and a
live event never reaches an HTMX handler.

---

## Choosing between them

| Question | Answer |
|---|---|
| Does this markup need to change when server state changes? | Make it a live fragment. Do not use HTMX for it. |
| Does it change only when the user asks, and can it live outside every region? | Plain HTMX, outside. No `Preserve`. |
| Does it change only when the user asks, but has to sit inside a live region for layout? | `live.Preserve()`, and accept that the server can never update it. |
| Both — server-pushed *and* user-fetched? | Two elements. One live fragment, one preserved island beside it. There is no way to share ownership of one node. |

---

## Two things to watch

- **`live.Preserve` is a one-way door for that subtree.** The server can never
  patch it again. If you later want the server to own it, the `Preserve` has to
  come off and the HTMX has to move out.
- **Keep explanation out of the markup.** templ renders HTML comments, so a
  paragraph of reasoning inside a live region is bytes in every patch of that
  region. Put it in a Go comment above the `templ` block, where it costs nothing
  at runtime.

`live.Preserve` is marked **experimental** in
[`docs/api-surface.md` §5.2](../api-surface.md), along with the rest of the
templ helpers.
