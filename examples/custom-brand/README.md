# The custom-brand example

CandaceOS Core wearing another product's identity: a different name, a
different agent, a different mark, a different palette, an extra sidebar entry,
and a page of the embedding product's own.

**Harborlight is invented for this example.** It is not a real product,
company, or service, and nothing here is modelled on one. It exists so that the
rebrand is total enough to be worth reading — a half-rebrand would not show
which strings are data and which are not.

Nothing in Core is edited to get this. Every change is one option handed to the
composition root, and Core's routes — including the `/claws/...` paths — are
exactly what they were.

---

## Run it

This is a complete Core, so it needs what Core needs: a PostgreSQL URL, a
writable data directory and workspace, a Warden URL, and a harness selection.
It reads the same configuration as the stock command and adds no setting of its
own.

```bash
go run ./examples/custom-brand
```

The hermetic suite needs none of that — it drives the same four values through
the web UI and the same engine Core builds, with no database and no container:

```bash
go test ./examples/custom-brand
```

## What it changes

| Seam | Option | What it replaces |
|---|---|---|
| Identity | `bootstrap.WithBrand` | the product name, the agent name, the wordmark, the design tokens |
| Presentation files | `bootstrap.WithUIOverlay` | resolves templates and assets before the embedded ones; here, one glyph |
| Sidebar | `bootstrap.WithNavItem` | appends one entry after Core's Home, Apps, Fleet, Activity |
| Routes | `bootstrap.WithHTTPService` | mounts the page that entry links to |

All four are in [`main.go`](main.go)'s `seams`; the values are in
[`identity.go`](identity.go) and [`harborlog.go`](harborlog.go).

### The two brand-bearing strings

`ProductName` and `AgentName` are the only UI copy the seam makes data. They
replace "CandaceOS" and "Claw" in titles, aria-labels, and the sentences that
name the system or the thing acting for the operator. Everything else stays
literal: the page still says "Harborlight, across your whole fleet" over a
sentence nobody had to translate.

Both names travel in the WebUI snapshot as well as into the markup, so the
browser client reads them as data rather than carrying a second copy. That is
why an unnamed snapshot still renders "Harborlight" in the topbar — Core stamps
the configured brand into every snapshot it produces.

### The wordmark

`Wordmark` is **markup, not text**: the UI emits it verbatim, exactly like a
template the operator wrote. Write it as a reviewed constant of your program and
never assemble one from a browser request, a fleet node, or an agent. It cannot
smuggle a script past the page's Content-Security-Policy, but it can restyle or
deface the shell.

That policy is also why the mark here is an `<img>` with `width` and `height`
attributes rather than a styled `<span>`: presentational attributes are markup,
and an inline `style` would simply not apply under `style-src 'self'`.

The same fragment renders on the dark sidebar and on the light chat topbar, and
the lettering inherits each surface's color from the shipped stylesheet — so a
glyph has to read on both. The one in `overlay/assets/` is a mid-tone blue for
exactly that reason. An SVG loaded through `<img>` is its own document and
cannot see the page's custom properties, so keeping it in step with the palette
is the author's job, not the seam's.

### The palette

`Palette` overrides the design tokens the shipped stylesheet declares on
`:root`. It is delivered as a **generated same-origin stylesheet** linked after
`app.css`, so the overrides win with no CSS rebuild, no inline style block, and
no change to the page's `'self'` Content-Security-Policy.

Values are validated rather than escaped, because a custom property value is
substituted into the stylesheet as CSS. Anything that could end the declaration,
end the rule, open a comment, or fetch a remote resource fails assembly instead
of reaching a page — see [`palette.go`](../../services/candaceos/webui/palette.go)
for the exact rules.

Only the tokens this identity changes are set; an unset token keeps its shipped
value, which is why the shadows, the radii, and the monospace stack are absent
rather than copied. The status colors keep their green/amber/red roles: a
palette that recolored "failed" to something calm would be a rebrand that lies.

### The overlay

`WithUIOverlay` takes one filesystem shaped like the web UI's own —
`templates/*.html` and `assets/*` — resolved overlay-first with embedded
fallback. This example carries a single asset, the wordmark's glyph, and
nothing else: everything the overlay does not name keeps shipping from Core, at
the same asset URLs, with the same cache and `nosniff` headers.

The overlay's other half, redefining named template blocks, is not exercised
here. The block names, the data each receives, and the two the browser client
depends on are listed in the
[`webui` package documentation](../../services/candaceos/webui/webui.go).
Overlay templates are operator-trusted markup on the same footing as the
wordmark.

### The extra page

`WithNavItem` appends a sidebar entry that renders with the same markup,
keyboard behavior, and aria semantics as the shipped four. It carries no `View`,
so it is a plain link rather than an in-page view switch; setting `View` to the
name of a section the page renders would switch in place instead.

`WithHTTPService` mounts the page it links to on the one engine Core builds.
The entry's `Href` and the route read the same constant, because nothing checks
that a sidebar entry and a route agree.

The page links `app.css` and the generated brand stylesheet, which is the whole
reason it looks like the rest of the product: the palette is a served
stylesheet, so anything that links it is branded, including a page Core has
never heard of. Core does not hand a registered service its runtime, so a page
that needs live state reads the snapshot endpoint like any other client.

## What does not move

- **Routes.** `/claws/...`, the API, and the asset URL space are untouched. The
  agent's name is data; the paths the browser client posts to are not.
- **The Content-Security-Policy.** Still `script-src 'self'` and
  `style-src 'self'`, with nothing inlined anywhere in this example.
- **Every other string.** Only the two brand-bearing names are keyed.
- **The default.** Core with no options renders byte-for-byte what it always
  did; the seam's own suite holds it to recorded fixtures.

## See also

- [`examples/custom-ui-page`](../custom-ui-page) — the same UI seams at their
  smallest: stock identity, one entry, one page.
- [`services/candaceos/webui`](../../services/candaceos/webui) — the package
  documentation is the contract: override points, the data each block receives,
  and what a caller may rely on.
- [`candaceos/README.md`](../../candaceos/README.md) — where these options sit
  among Core's other compile-time boundaries.
