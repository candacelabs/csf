package main

import (
	"bytes"
	"html/template"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/candacelabs/csf/services/candaceos/browserroutes"
)

// runbookPath is where this page is served. Core neither knows nor reserves it.
const runbookPath = "/runbooks"

// runbooks is the whole service: one route, one page, no state to keep.
//
// bootstrap.WithHTTPService takes anything that can mount routes on a
// gin.IRouter, so a service may be as large as the embedding product needs. At
// this size the type carries no fields at all.
type runbooks struct{}

// Register mounts the page on Core's engine. That engine has already set
// Core's security headers, including its Content-Security-Policy, so this
// handler adds a content type and its bytes and nothing else.
func (runbooks) Register(router gin.IRouter) {
	router.GET(runbookPath, func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", runbookPage)
	})
}

// runbookPage is rendered once, at startup, because nothing in it varies per
// request. A page that did vary would render inside the handler instead.
var runbookPage = renderRunbookPage()

// entries are what this page is for: the embedding product's own content. Core
// does not hand a registered service its runtime, and a page that needs live
// state reads the read-only snapshot endpoint like any other client rather than
// reaching into Core.
var entries = []string{
	"Rotate the shared fleet credentials",
	"Replace a failed disk without losing quorum",
	"Roll back the last deployment",
}

func renderRunbookPage() []byte {
	page := template.Must(template.New("runbooks").Parse(
		`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta name="color-scheme" content="light">
  <title>Runbooks</title>
  <link rel="stylesheet" href="{{ .AppCSS }}">
  <link rel="stylesheet" href="{{ .BrandCSS }}">
</head>
<body class="chat-page">
  <main class="chat-main">
    <p class="kicker">Runbooks</p>
    <h1>What to do when</h1>
    <ol>
    {{- range .Entries }}
      <li>{{ . }}</li>
    {{- end }}
    </ol>
    <p><a class="chat-back" href="{{ .Home }}"><span aria-hidden="true">&#8592;</span> Dashboard</a></p>
  </main>
</body>
</html>
`))

	// Both stylesheets are linked, and both URLs come from browserroutes rather
	// than a literal. The second one is the generated brand stylesheet: it is
	// empty under the stock identity, which is exactly why linking it costs
	// nothing here and is what keeps this page in step if the product is ever
	// rebranded.
	var rendered bytes.Buffer
	err := page.Execute(&rendered, map[string]any{
		"AppCSS":   browserroutes.AssetPath("app.css"),
		"BrandCSS": browserroutes.BrandStylesheetPath(),
		"Home":     browserroutes.Index,
		"Entries":  entries,
	})
	if err != nil {
		// The template and its data are both constants of this program, so a
		// failure here is a build mistake rather than a runtime condition, and
		// it should stop the binary at startup rather than serve half a page.
		panic("custom-ui-page: rendering the runbook page failed: " + err.Error())
	}
	return rendered.Bytes()
}
