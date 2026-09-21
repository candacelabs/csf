package main

import (
	"html/template"

	"github.com/gin-gonic/gin"

	"github.com/candacelabs/csf/services/candaceos/browserroutes"
)

// harborLogPath is where this example's own page is served. It is a path Core
// does not use and does not know about: Core's routes, including the
// /claws/... paths, are exactly what they were before this program existed.
const harborLogPath = "/harbor-log"

// harborLog is the embedding product's page, mounted beside Core's API and web
// UI on the one engine Core builds. It is what bootstrap.WithHTTPService takes:
// anything that can mount routes on a gin.IRouter.
//
// It is deliberately self-contained. Core does not hand a registered service
// its runtime, so a page that needs live state reads the read-only snapshot
// endpoint like any other client rather than reaching into Core.
type harborLog struct{}

// Register mounts the page. The engine has already applied Core's security
// headers, including its Content-Security-Policy, by the time this handler
// runs; a mounted page inherits that posture and does not get to loosen it,
// which is why the page below links its styles rather than inlining them.
func (harborLog) Register(router gin.IRouter) {
	router.GET(harborLogPath, func(c *gin.Context) {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.Header("Cache-Control", "no-store")
		if err := harborLogPage.Execute(c.Writer, harborLogView{
			ProductName: productName,
			Wordmark:    wordmark,
			AppCSS:      browserroutes.AssetPath("app.css"),
			BrandCSS:    browserroutes.BrandStylesheetPath(),
			Home:        browserroutes.Index,
			Calls:       portCalls,
		}); err != nil {
			// The response has already begun, so this can no longer become a
			// status code: the page is truncated either way. Recording it on
			// the context is what leaves a truncated page visible to whatever
			// the engine carries, rather than failing silently.
			_ = c.Error(err)
		}
	})
}

// harborLogView is everything the page renders. The two brand values are the
// same ones Core was assembled with, so this page cannot drift from the shell
// it sits beside.
type harborLogView struct {
	ProductName string
	Wordmark    template.HTML
	AppCSS      string
	BrandCSS    string
	Home        string
	Calls       []portCall
}

// portCall is one row of this fictional product's own data. The point of the
// slice is that it is the embedding product's, not Core's: nothing here came
// out of a snapshot.
type portCall struct {
	Vessel string
	Berth  string
	Note   string
}

var portCalls = []portCall{
	{Vessel: "Northwind", Berth: "Berth 1", Note: "Loading, departs on the evening tide."},
	{Vessel: "Kestrel", Berth: "Berth 3", Note: "Waiting on a pilot."},
	{Vessel: "Tern", Berth: "Anchorage", Note: "Holding outside the breakwater."},
}

// harborLogPage links the same two stylesheets the operator pages link: the
// shipped app.css, and after it the generated brand stylesheet carrying this
// example's palette. That is the whole reason this page looks like the rest of
// the product — the palette is a served same-origin stylesheet, so anything
// that links it is branded, including pages Core has never heard of.
var harborLogPage = template.Must(template.New("harbor-log").Parse(
	`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta name="color-scheme" content="light">
  <title>Harbor Log · {{ .ProductName }}</title>
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
    <p class="kicker">Harbor log</p>
    <h1>Everything alongside</h1>
    <p>This page belongs to {{ .ProductName }}, not to the control plane it is mounted beside.</p>
    <div class="panel">
      <ul>
      {{- range .Calls }}
        <li><strong>{{ .Vessel }}</strong> &middot; {{ .Berth }} &mdash; {{ .Note }}</li>
      {{- end }}
      </ul>
    </div>
  </main>
</body>
</html>
`))
