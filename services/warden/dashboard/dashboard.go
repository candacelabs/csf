// Package dashboard renders the operator-facing observability surface of a
// warden node: a server-side-rendered, HTMX-refreshed dashboard, an HTMX
// partial for live cluster refresh, and a JSON status API.
//
// Handlers are fully stateless. Every request reads a fresh, immutable
// snapshot via ViewSource.View() and IncidentLog.Incidents() (each a
// request/reply query into the owning state machine) and renders it — there
// is no caching, no shared mutable state, and no background goroutine. The
// only fields carried on *Dashboard are the parsed templates (immutable after
// New) and injected dependencies.
//
// Templates AND static assets (htmx.min.js, warden.css) are embedded via
// go:embed and served from /assets/, so a warden node ships as a single
// static binary whose dashboard renders fully offline — no CDN, no
// runtime file reads. warden is a fault-tolerance tool: the moment an
// operator opens this page is exactly when WAN access may be broken. This is
// a deliberate, pre-approved deviation from the repo's statictemplates and
// Tailwind-via-CDN conventions (warden.css hand-rolls the same dark slate
// look).
package dashboard

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	core "github.com/candacelabs/csf/pkg/core"
	"github.com/candacelabs/csf/services/warden"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed assets
var assetsFS embed.FS

const (
	templateIndex   = "index.html"
	templatePartial = "cluster_partial.html"
)

// HTTP response header and content types the handlers write. The dashboard
// contract spec pins these exact strings as literal goldens.
const (
	headerContentType = "Content-Type"
	contentTypeHTML   = "text/html; charset=utf-8"
	contentTypeJSON   = "application/json"
)

// StatusResponse is the payload returned by the JSON status API. Incidents is
// always a non-nil slice so it marshals as [] rather than null.
type StatusResponse struct {
	View      warden.ClusterView `json:"view"`
	Incidents []warden.Incident  `json:"incidents"`
}

// Dashboard serves the warden observability endpoints. It is safe for
// concurrent use: after New returns, all of its fields are read-only.
type Dashboard struct {
	view      warden.IViewSource
	incidents warden.IIncidentLog
	version   string
	tmpl      *template.Template
}

// viewData is the immutable model handed to the templates for a single render.
// Now is captured once per request so age calculations are consistent across
// the whole page.
type viewData struct {
	View      warden.ClusterView
	Incidents []warden.Incident
	Version   string
	Now       time.Time
}

// New parses the embedded templates and returns a Dashboard that renders live
// snapshots from view and incidents. version is displayed in the page footer.
func New(view warden.IViewSource, incidents warden.IIncidentLog, version string) (*Dashboard, error) {
	tmpl, err := template.New("warden").Funcs(funcMap()).ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parsing warden dashboard templates: %w", err)
	}
	return &Dashboard{
		view:      view,
		incidents: incidents,
		version:   version,
		tmpl:      tmpl,
	}, nil
}

// Register mounts the dashboard routes on r. The handlers keep their plain
// http.ResponseWriter signatures and are adapted with gin.WrapF/gin.WrapH so
// their response bytes (HTML "text/html; charset=utf-8", the two-space
// pretty-printed bare-"application/json" /api/status body, and asset serving)
// are preserved exactly.
//
// PathDashboard ("/") is a gin static route, which matches the exact root only
// and does not swallow unmatched subpaths (those 404 via NoRoute). Every route
// is GET-only; the engine's HandleMethodNotAllowed answers other methods with
// 405. Asset URL paths mirror the embed FS layout ("/assets/x" -> "assets/x"),
// so http.FileServerFS needs no prefix stripping and 404s unknown asset paths.
func (d *Dashboard) Register(r gin.IRouter) {
	r.GET(warden.PathDashboard, gin.WrapF(d.handleIndex))
	r.GET(warden.PathClusterPartial, gin.WrapF(d.handlePartial))
	r.GET(warden.PathAPIStatus, gin.WrapF(d.handleStatus))
	r.GET("/assets/*filepath", gin.WrapH(http.FileServerFS(assetsFS)))
}

// snapshot reads one fresh, immutable view of the world. Each dependency is
// queried exactly once per request.
func (d *Dashboard) snapshot() viewData {
	return viewData{
		View:      d.view.View(),
		Incidents: d.incidents.Incidents(),
		Version:   d.version,
		Now:       time.Now(),
	}
}

func (d *Dashboard) handleIndex(w http.ResponseWriter, r *http.Request) {
	d.renderHTML(w, templateIndex, d.snapshot())
}

func (d *Dashboard) handlePartial(w http.ResponseWriter, r *http.Request) {
	d.renderHTML(w, templatePartial, d.snapshot())
}

// renderHTML executes a template into a buffer first so a mid-render error
// yields a clean 500 instead of a half-written page.
func (d *Dashboard) renderHTML(w http.ResponseWriter, name string, data viewData) {
	var buf bytes.Buffer
	if err := d.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		core.Logger.Error().Err(err).Str("template", name).Msg("warden dashboard: template execution failed")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set(headerContentType, contentTypeHTML)
	_, _ = buf.WriteTo(w)
}

func (d *Dashboard) handleStatus(w http.ResponseWriter, r *http.Request) {
	incidents := d.incidents.Incidents()
	if incidents == nil {
		// Guarantee "incidents": [] rather than null for operator tooling.
		incidents = []warden.Incident{}
	}
	resp := StatusResponse{
		View:      d.view.View(),
		Incidents: incidents,
	}
	// Pretty-print with two-space indent: operators curl this endpoint.
	body, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		core.Logger.Error().Err(err).Msg("warden dashboard: encoding status response failed")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set(headerContentType, contentTypeJSON)
	_, _ = w.Write(body)
}
