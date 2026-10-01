# The custom-ui-page example

Stock CandaceOS Core with one page added: a sidebar entry, and something of
your own behind it.

This is the smallest useful shape of the operator UI's extension seams, and it
is deliberately not a rebrand. The identity, the palette, the templates, and
every string in the shipped pages are exactly Core's — the only difference an
operator sees is a fifth entry in the sidebar.

---

## Run it

A complete Core needs what Core needs: a PostgreSQL URL, a writable data
directory and workspace, a Warden URL, and a harness selection. Nothing here
adds a setting of its own.

```bash
go run ./examples/custom-ui-page
```

The suite needs none of that — it mounts the same two values on the same engine
Core builds, with no database and no container:

```bash
go test ./examples/custom-ui-page
```

## The two options

Three declarations are the whole extension. This is
[`main.go`](main.go) and the two halves of [`runbooks.go`](runbooks.go) it
names, with the comments trimmed:

```go
// the sidebar entry
var entry = webui.NavItem{Label: "Runbooks", Href: runbookPath, Glyph: "▤"}

// the service behind it: no fields, because it keeps no state
type runbooks struct{}

func (runbooks) Register(router gin.IRouter) {
	router.GET(runbookPath, func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", runbookPage)
	})
}

// the composition root
func main() {
	err := bootstrap.Run(version,
		bootstrap.WithNavItem(entry),
		bootstrap.WithHTTPService(runbooks{}),
	)
	...
}
```

**`WithNavItem`** appends one entry to the operator sidebar, after Core's Home,
Apps, Fleet, and Activity. It renders with the same markup, keyboard behavior,
and aria semantics as those four. `Label` and `Href` are required; `Glyph` is
decorative and `aria-hidden`, so it carries nothing the label does not. Both
strings are bounded, control-character free, and escaped, and a target the
browser must not follow is neutralized by the template rather than emitted. An
entry that cannot be rendered as one labeled link fails assembly before any
infrastructure is opened.

`View` is left unset here, which makes this a plain link. The built-in entries
each name a section the operator page renders and switch to it in place;
there is no such section for a page served from somewhere else.

**`WithHTTPService`** mounts anything that can register routes on a
`gin.IRouter` onto the one engine Core builds. That engine has already applied
Core's security headers, so the handler adds a content type and its bytes and
nothing else — a mounted page inherits Core's posture and does not get to
loosen it.

Core's own routes, including the `/claws/...` paths, are untouched by either
option.

## Two details worth copying

**One constant for the link and the route.** The sidebar entry's `Href` and the
registered path are the same constant, because nothing checks that they agree.

**Link the brand stylesheet anyway.** The page links `app.css` and the
generated brand stylesheet, both through
[`browserroutes`](../../services/candaceos/browserroutes) rather than literal
URLs. Under the stock identity that second stylesheet is empty, so linking it
costs nothing — and it is what keeps this page in step on the day the product
is rebranded.

## Next

- [`examples/custom-brand`](../custom-brand) — the same two options plus
  `WithBrand` and `WithUIOverlay`, which between them replace the two
  brand-bearing names, the wordmark, the palette, and any presentation file the
  overlay names.
- [`services/candaceos/webui`](../../services/candaceos/webui) — the package
  documentation is the contract: the overridable template blocks, the data each
  receives, and what a caller may rely on.
