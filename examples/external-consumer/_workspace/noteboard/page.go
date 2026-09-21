package noteboard

import (
	"html/template"

	"github.com/gin-gonic/gin"

	"github.com/candacelabs/csf/services/candaceos/browserroutes"
	"github.com/candacelabs/csf/services/candaceos/webui"
)

// Path is where this service's page is served. Core neither knows nor reserves
// it: Core's routes, including the /claws/... paths, are exactly what they were
// before this repository existed.
const Path = "/field-notes"

// NavItem is the sidebar entry pointing at that page, appended after Core's own
// Home, Apps, Fleet, and Activity. It renders with the same markup, keyboard
// behavior, and aria semantics as those four.
//
// It carries no View, so it is a plain link rather than an in-page view switch:
// nothing on the operator page renders a section by that name. Its Href is the
// constant this package registers, because nothing checks that a sidebar entry
// and a route agree — the only thing that can keep them in step is that there
// is one of them.
func NavItem() webui.NavItem {
	return webui.NavItem{Label: "Field Notes", Href: Path, Glyph: "✎"}
}

// Register mounts the page on the one Gin engine Core builds, beside Core's own
// API and web UI. That engine has already applied Core's security headers,
// including its Content-Security-Policy, by the time this handler runs; a
// mounted page inherits that posture and does not get to loosen it, which is
// why the page links its styles rather than inlining them.
//
// Register makes Board the bootstrap.IHTTPService this repository registers.
// Core hands a registered service nothing, so the handler reads the board it is
// a method on and no Core state at all.
func (board *Board) Register(router gin.IRouter) {
	router.GET(Path, func(c *gin.Context) {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.Header("Cache-Control", "no-store")
		if err := page.Execute(c.Writer, view{
			ProductName: board.brand.ProductName,
			AgentName:   board.brand.AgentName,
			Wordmark:    board.brand.Wordmark,
			AppCSS:      browserroutes.AssetPath("app.css"),
			BrandCSS:    browserroutes.BrandStylesheetPath(),
			Home:        browserroutes.Index,
			Ledger:      board.Read(),
			Capacity:    Capacity,
		}); err != nil {
			// The response has already begun, so this can no longer become a
			// status code: the page is truncated either way. Recording it on
			// the context is what leaves a truncated page visible to whatever
			// the engine carries, rather than failing silently.
			_ = c.Error(err)
		}
	})
}

// view is everything the page renders. The brand values are the ones the
// composition root handed Core, so this page cannot drift from the shell it
// sits beside.
type view struct {
	ProductName string
	AgentName   string
	Wordmark    template.HTML
	AppCSS      string
	BrandCSS    string
	Home        string
	Ledger      Ledger
	Capacity    int
}

// page links the same two stylesheets the operator pages link: the shipped
// app.css, and after it the generated brand stylesheet carrying this product's
// palette. That is the whole reason this page looks like the rest of the
// product — the palette is a served same-origin stylesheet, so anything that
// links it is branded, including pages Core has never heard of.
var page = template.Must(template.New("field-notes").Parse(
	`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta name="color-scheme" content="light">
  <title>Field Notes · {{ .ProductName }}</title>
  <link rel="stylesheet" href="{{ .AppCSS }}">
  <link rel="stylesheet" href="{{ .BrandCSS }}">
</head>
<body class="chat-page">
  <header class="chat-topbar">
    <a class="chat-brand" href="{{ .Home }}">{{ .Wordmark }}</a>
    <span></span>
    <a class="chat-back" href="{{ .Home }}"><span aria-hidden="true">&#8592;</span> Dashboard</a>
  </header>
  <main class="chat-main">
    <p class="kicker">Field notes</p>
    <h1>What you have steered</h1>
    <p>This page belongs to {{ .ProductName }}, not to the control plane it is mounted beside. It keeps the newest {{ .Capacity }} things you told {{ .AgentName }} to do.</p>
    <div class="panel" data-field-notes>
    {{- if not .Ledger.Running }}
      <p data-field-notes-state="standing-by">The noteboard has not been started yet.</p>
    {{- else if not .Ledger.Notes }}
      <p data-field-notes-state="empty">Nothing steered yet.</p>
    {{- else }}
      <ol data-field-notes-state="ready">
      {{- range .Ledger.Notes }}
        <li value="{{ .Sequence }}">{{ .Text }}</li>
      {{- end }}
      </ol>
    {{- end }}
    </div>
  </main>
</body>
</html>
`))
