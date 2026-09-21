// Package webui serves CandaceOS Core's local-first operator interface and is
// the seam where an embedding product supplies its own branding.
//
// The package owns presentation only: the embedded templates and assets, the
// page and asset handlers, and the read-only snapshot endpoint. Agent
// execution, approvals, event publication, and fleet state belong to the
// containing core, which supplies them through ISnapshotProvider — the single
// input this package takes — and which mounts its own action endpoints and
// event stream alongside this handler.
//
// Presentation is the package's one configurable policy, and it is configured
// in three widening steps. WithBrand replaces the product name, the agent name,
// the wordmark, and the design tokens; every other string in the UI stays
// literal. WithNavItem appends entries to the sidebar. WithUIOverlay resolves
// templates and assets against a caller-supplied filesystem before the embedded
// one. The palette is delivered as a served same-origin stylesheet rather than
// an inline style block, so the page's Content-Security-Policy stays 'self' for
// both styles and scripts no matter how far a caller goes.
//
// # Template override points
//
// An overlay template file contributes {{define}} blocks into the same
// namespace the embedded files use, so redefining one of the names below
// replaces exactly that much of the UI. These are the supported names, the
// whole of them, and the data each one receives:
//
//	"index.html"     the operator home page; receives the page data below
//	"chat.html"      the live session page; receives the page data below
//	"primaryNav"     the sidebar navigation list; receives the bound
//	                 navigation items, each carrying the NavItem fields
//	                 (Label, Href, Glyph, View) plus Active, Count, and
//	                 CountAttribute
//	"statusPill"     one status chip; receives the status string
//	"browserRoutes"  the body's data-route-* attributes; receives the route
//	                 table the browser client reads its URLs from
//	"browserEnums"   the body's data-enum-* attributes; receives the enum
//	                 table the browser client compares against
//
// The two page blocks receive .Brand, .Snapshot, .Nav, .Routes, .Enums,
// .InitialJSON, .Unavailable, and .ChatSessionID. Every template helper the
// shipped blocks use is available to an override of them.
//
// Two of these names are load-bearing beyond their markup. "browserRoutes" and
// "browserEnums" are how the browser client learns its URLs and enum spellings;
// an override that drops an attribute silently disables the behavior behind it.
// The embedded client also reads the navigation back out of the rendered DOM,
// so an overridden "primaryNav" keeps in-page view switching as long as its
// entries carry data-nav values naming sections the page rendered.
//
// # Worked examples
//
// Two runnable programs in this module compose these options through the
// composition root, at both ends of the range:
//
//	examples/custom-ui-page   the stock identity, one sidebar entry, and the
//	                          page behind it — the smallest useful extension
//	examples/custom-brand     a full rebrand: both brand-bearing names, a
//	                          wordmark drawn around a glyph its overlay adds, a
//	                          replaced palette, one sidebar entry, and its page
//
// Each carries a hermetic suite that renders the pages without the
// infrastructure an assembled Core opens.
//
// Callers may rely on New failing rather than returning a handler without a
// snapshot source, on Handler being safe for concurrent use once New returns,
// on MarshalSnapshot producing the one canonical JSON encoding the embedded
// browser client parses, on every URL the rendered pages emit coming from
// browserroutes rather than a literal in a template, on the asset URL space and
// its cache headers being the same for overlay and embedded files alike, and on
// an unconfigured handler rendering exactly the stock CandaceOS identity.
package webui

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	core "github.com/candacelabs/csf/pkg/core"
	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	"github.com/candacelabs/csf/services/candaceos/browserroutes"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed assets/*
var assetsFS embed.FS

// ErrNilSnapshotProvider is returned when the UI is constructed without a
// source of immutable display state.
var ErrNilSnapshotProvider = errors.New("webui: nil snapshot provider")

// ISnapshotProvider supplies one immutable, internally consistent view of the
// core. Implementations should honor cancellation from the request context.
type ISnapshotProvider interface {
	Snapshot(ctx context.Context) (*candaceosv1.WebUISnapshot, error)
}

// SnapshotFunc adapts a function to ISnapshotProvider.
type SnapshotFunc func(ctx context.Context) (*candaceosv1.WebUISnapshot, error)

// Snapshot implements ISnapshotProvider.
func (f SnapshotFunc) Snapshot(ctx context.Context) (*candaceosv1.WebUISnapshot, error) {
	return f(ctx)
}

// Handler serves the embedded UI. It is safe for concurrent use after New
// returns.
type Handler struct {
	provider  ISnapshotProvider
	template  *template.Template
	assets    http.Handler
	brand     Brand
	brandCSS  []byte
	brandETag string
	nav       []NavItem
}

// Option changes one public presentation policy.
type Option func(settings *presentationOptions) error

type presentationOptions struct {
	brand    Brand
	navItems []NavItem
	overlay  fs.FS
}

// WithBrand replaces the stock CandaceOS identity. Unset Brand fields keep
// their stock values, and an invalid brand fails New rather than rendering a
// half-branded page.
func WithBrand(brand Brand) Option {
	return func(settings *presentationOptions) error {
		if err := brand.Validate(); err != nil {
			return err
		}
		settings.brand = brand.Resolved()
		return nil
	}
}

// WithNavItem appends one entry to the operator sidebar's primary navigation,
// after the shipped Home, Apps, Fleet, and Activity entries. It is repeatable;
// entries render in registration order. An invalid item fails New rather than
// rendering a broken link.
//
// A registered entry is a plain link: it carries no live count, and unless its
// View names a section the page actually renders, following it is ordinary
// navigation rather than the in-page view switch the built-in entries perform.
func WithNavItem(item NavItem) Option {
	return func(settings *presentationOptions) error {
		if err := item.Validate(); err != nil {
			return err
		}
		settings.navItems = append(settings.navItems, item.trimmed())
		return nil
	}
}

type pageData struct {
	Brand         Brand
	Snapshot      *candaceosv1.WebUISnapshot
	InitialJSON   template.JS
	Unavailable   bool
	ChatSessionID string
	Nav           []navEntry
	Routes        browserRoutes
	Enums         browserEnums
}

type browserRoutes struct {
	AppCSS          string
	AppJS           string
	BrandCSS        string
	Approval        string
	ClawChat        string
	ClawMessages    string
	ClawRunAbort    string
	CurrentRunAbort string
	Events          string
	Index           string
	Prompts         string
	Snapshot        string
}

type browserEnums struct {
	HarnessBackendCopilotCLI            string
	HarnessBackendDemo                  string
	HarnessBackendEmbedded              string
	HarnessBackendOllama                string
	HarnessBackendOpenCode              string
	HarnessCapabilityActiveTurnSteering string
	HarnessDeliveryEnqueue              string
	HarnessDeliveryImmediate            string
}

var routes = browserRoutes{
	AppCSS:          browserroutes.AssetPath("app.css"),
	AppJS:           browserroutes.AssetPath("app.js"),
	BrandCSS:        browserroutes.BrandStylesheetPath(),
	Approval:        browserroutes.Approval,
	ClawChat:        browserroutes.ClawChat,
	ClawMessages:    browserroutes.ClawMessages,
	ClawRunAbort:    browserroutes.ClawRunAbort,
	CurrentRunAbort: browserroutes.CurrentRunAbort,
	Events:          browserroutes.Events,
	Index:           browserroutes.Index,
	Prompts:         browserroutes.Prompts,
	Snapshot:        browserroutes.Snapshot,
}

var enums = browserEnums{
	HarnessBackendCopilotCLI:            candaceosv1.HarnessBackend_HARNESS_BACKEND_COPILOT_CLI.String(),
	HarnessBackendDemo:                  candaceosv1.HarnessBackend_HARNESS_BACKEND_DEMO.String(),
	HarnessBackendEmbedded:              candaceosv1.HarnessBackend_HARNESS_BACKEND_EMBEDDED.String(),
	HarnessBackendOllama:                candaceosv1.HarnessBackend_HARNESS_BACKEND_OLLAMA.String(),
	HarnessBackendOpenCode:              candaceosv1.HarnessBackend_HARNESS_BACKEND_OPENCODE.String(),
	HarnessCapabilityActiveTurnSteering: candaceosv1.HarnessCapability_HARNESS_CAPABILITY_ACTIVE_TURN_STEERING.String(),
	HarnessDeliveryEnqueue:              candaceosv1.HarnessDelivery_HARNESS_DELIVERY_ENQUEUE.String(),
	HarnessDeliveryImmediate:            candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE.String(),
}

// New parses the embedded pages and constructs a handler for pages,
// static assets, and the read-only snapshot API. The containing core should
// mount its POST action endpoints and GET /api/events alongside this handler.
//
// Without options the handler serves the stock CandaceOS identity.
func New(provider ISnapshotProvider, functionalOptions ...Option) (*Handler, error) {
	if provider == nil {
		return nil, ErrNilSnapshotProvider
	}
	settings := presentationOptions{brand: DefaultBrand(), navItems: DefaultNavItems()}
	for index, option := range functionalOptions {
		if option == nil {
			return nil, fmt.Errorf("CandaceOS web UI option %d is nil", index+1)
		}
		if err := option(&settings); err != nil {
			return nil, fmt.Errorf("applying CandaceOS web UI option %d: %w", index+1, err)
		}
	}

	tmpl, err := parseTemplates(settings.overlay)
	if err != nil {
		return nil, err
	}

	stylesheet := []byte(settings.brand.Palette.Stylesheet())
	digest := sha256.Sum256(stylesheet)
	return &Handler{
		provider:  provider,
		template:  tmpl,
		assets:    cacheAssets(http.FileServerFS(assetTree(settings.overlay))),
		brand:     settings.brand,
		brandCSS:  stylesheet,
		brandETag: `"` + base64.RawURLEncoding.EncodeToString(digest[:]) + `"`,
		nav:       settings.navItems,
	}, nil
}

// Register mounts the presentation routes on a caller-owned Gin router.
func (h *Handler) Register(router gin.IRouter) {
	router.GET(browserroutes.Index, gin.WrapF(h.HandleIndex))
	router.GET(browserroutes.ClawChat, func(c *gin.Context) {
		h.HandleChat(c.Writer, c.Request, c.Param(browserroutes.ParamSessionID))
	})
	router.GET(browserroutes.Assets, gin.WrapF(h.HandleAsset))
	router.GET(browserroutes.Snapshot, gin.WrapF(h.HandleSnapshot))
}

// HandleIndex renders the operator home page.
func (h *Handler) HandleIndex(w http.ResponseWriter, r *http.Request) {
	snapshot, err := h.provider.Snapshot(r.Context())
	unavailable := err != nil
	if unavailable {
		snapshot = h.unavailableSnapshot()
	}
	snapshot = normalizeSnapshot(snapshot, h.brand)

	body, marshalErr := MarshalSnapshot(snapshot)
	if marshalErr != nil {
		http.Error(w, "rendering "+h.brand.ProductName, http.StatusInternalServerError)
		return
	}

	data := pageData{
		Brand:    h.brand,
		Snapshot: snapshot,
		// encoding/json escapes '<', '>', '&', U+2028, and U+2029, which keeps
		// provider-controlled text inside this non-executable script element.
		InitialJSON: template.JS(body), // #nosec G203 -- JSON is HTML-escaped above.
		Unavailable: unavailable,
		Nav:         navEntries(h.nav, snapshot),
		Routes:      routes,
		Enums:       enums,
	}

	h.renderPage(w, "index.html", data)
}

// HandleChat renders the current run only when it belongs to sessionID.
func (h *Handler) HandleChat(w http.ResponseWriter, r *http.Request, sessionID string) {
	snapshot, err := h.provider.Snapshot(r.Context())
	if err != nil {
		http.Error(w, h.brand.ProductName+" snapshot unavailable", http.StatusServiceUnavailable)
		return
	}
	snapshot = normalizeSnapshot(snapshot, h.brand)
	if snapshot.Run == nil || snapshot.Run.GetSessionId() != sessionID {
		http.NotFound(w, r)
		return
	}

	body, err := MarshalSnapshot(snapshot)
	if err != nil {
		http.Error(w, "rendering "+h.brand.ProductName, http.StatusInternalServerError)
		return
	}
	h.renderPage(w, "chat.html", pageData{
		Brand:         h.brand,
		Snapshot:      snapshot,
		InitialJSON:   template.JS(body), // #nosec G203 -- JSON is HTML-escaped above.
		ChatSessionID: sessionID,
		Routes:        routes,
		Enums:         enums,
	})
}

func (h *Handler) renderPage(w http.ResponseWriter, name string, data pageData) {
	var rendered bytes.Buffer
	if err := h.template.ExecuteTemplate(&rendered, name, data); err != nil {
		http.Error(w, "rendering "+h.brand.ProductName, http.StatusInternalServerError)
		return
	}

	setPageHeaders(w.Header())
	w.WriteHeader(http.StatusOK)
	_, _ = rendered.WriteTo(w)
}

// templateFuncs is the one function map every page — embedded or overlaid —
// is parsed with, so an override point cannot lose a helper the shipped block
// it replaces was written against.
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"canCreateApps":      canCreateApps,
		"canSteerActiveTurn": canSteerActiveTurn,
		"clawAbortPath":      browserroutes.ClawRunAbortPath,
		"clawChatPath":       browserroutes.ClawChatPath,
		"clawMessagePath":    browserroutes.ClawMessagePath,
		"displayName":        displayName,
		"formatPercent":      formatPercent,
		"harnessBackend":     harnessBackendLabel,
		"harnessRuntime":     harnessRuntimeLabel,
		"initial":            initial,
		"relativeTime":       relativeTime,
		"runBusy":            runBusy,
		"statusLabel":        statusLabel,
		"timestampFormat":    timestampFormat,
		"tone":               tone,
	}
}

func runBusy(run *candaceosv1.WebUIRun) bool {
	if run == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(run.GetStatus())) {
	case "running", "busy", "starting", "working", "waiting", "queued", "aborting":
		return true
	default:
		return false
	}
}

// HandleSnapshot writes the read-only browser snapshot.
func (h *Handler) HandleSnapshot(w http.ResponseWriter, r *http.Request) {
	snapshot, err := h.provider.Snapshot(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, "snapshot unavailable")
		return
	}

	body, err := MarshalSnapshot(normalizeSnapshot(snapshot, h.brand))
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "snapshot encoding failed")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(append(body, '\n'))
}

// HandleAsset serves an embedded browser asset with cache and content headers.
// The brand stylesheet is generated rather than embedded, so it is answered
// here instead of from the embedded filesystem.
func (h *Handler) HandleAsset(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == browserroutes.BrandStylesheetPath() {
		h.handleBrandStylesheet(w, r)
		return
	}
	h.assets.ServeHTTP(w, r)
}

// handleBrandStylesheet serves the palette's :root overrides. The page links it
// after app.css so these declarations win, and it is served same-origin so the
// page needs no inline style block and no relaxed style-src.
//
// The stock brand sets no token and therefore serves an empty stylesheet; the
// link stays in the page either way, so a rebranded core changes only the bytes
// behind a URL the markup already carries.
func (h *Handler) handleBrandStylesheet(w http.ResponseWriter, r *http.Request) {
	header := w.Header()
	header.Set("Content-Type", "text/css; charset=utf-8")
	header.Set("Cache-Control", "public, max-age=3600")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("ETag", h.brandETag)
	http.ServeContent(w, r, browserroutes.BrandStylesheet, time.Time{}, bytes.NewReader(h.brandCSS))
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func setPageHeaders(header http.Header) {
	header.Set("Content-Type", "text/html; charset=utf-8")
	header.Set("Cache-Control", "no-store")
	header.Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data:; script-src 'self'; style-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
}

func cacheAssets(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

// MarshalSnapshot preserves the compact browser JSON contract while using the
// canonical generated message as its only state model. Snapshots reach it
// already carrying the producing core's brand; the stock names fill only the
// fields a producer left empty.
func MarshalSnapshot(snapshot *candaceosv1.WebUISnapshot) ([]byte, error) {
	normalized := normalizeSnapshot(snapshot, DefaultBrand())
	body, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(normalized)
	if err != nil {
		return nil, err
	}

	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var root map[string]any
	if err := decoder.Decode(&root); err != nil {
		return nil, fmt.Errorf("decode generated snapshot JSON: %w", err)
	}
	setJSONDefault(root, "attention", []any{})
	setJSONDefault(root, "run", nil)
	setJSONDefault(root, "apps", []any{})
	setJSONDefault(root, "activity", []any{})

	fleet, ok := root["fleet"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("decode generated fleet JSON: expected object")
	}
	setJSONDefault(fleet, "quorum", map[string]any{})
	setJSONDefault(fleet, "nodes", []any{})
	if term, present := fleet["term"]; present {
		value, ok := term.(string)
		if !ok {
			return nil, fmt.Errorf("decode generated fleet term: expected string")
		}
		if _, err := strconv.ParseUint(value, 10, 64); err != nil {
			return nil, fmt.Errorf("decode generated fleet term: %w", err)
		}
		fleet["term"] = json.Number(value)
	}
	quorum, ok := fleet["quorum"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("decode generated quorum JSON: expected object")
	}
	setJSONDefault(quorum, "healthy", false)
	setJSONDefault(quorum, "online", json.Number("0"))
	setJSONDefault(quorum, "required", json.Number("0"))

	if normalized.Run != nil {
		run, ok := root["run"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("decode generated run JSON: expected object")
		}
		setJSONDefault(run, "can_abort", false)
		setJSONDefault(run, "entries", []any{})
	}
	return json.Marshal(root)
}

func setJSONDefault(object map[string]any, field string, value any) {
	if _, present := object[field]; !present {
		object[field] = value
	}
}

func normalizeSnapshot(snapshot *candaceosv1.WebUISnapshot, brand Brand) *candaceosv1.WebUISnapshot {
	if snapshot == nil {
		snapshot = &candaceosv1.WebUISnapshot{}
	} else {
		snapshot = proto.Clone(snapshot).(*candaceosv1.WebUISnapshot)
	}
	if snapshot.GeneratedAt == nil {
		snapshot.GeneratedAt = zeroTimestamp()
	}
	if snapshot.System == nil {
		snapshot.System = &candaceosv1.WebUISystem{}
	}
	if snapshot.System.Name == "" {
		snapshot.System.Name = brand.ProductName
	}
	if snapshot.System.AgentName == "" {
		snapshot.System.AgentName = brand.AgentName
	}
	if snapshot.System.Status == "" {
		snapshot.System.Status = "unknown"
	}
	if snapshot.Attention == nil {
		snapshot.Attention = []*candaceosv1.WebUIAttention{}
	}
	for _, attention := range snapshot.Attention {
		if attention != nil && attention.RequestedAt == nil {
			attention.RequestedAt = zeroTimestamp()
		}
	}
	if snapshot.Apps == nil {
		snapshot.Apps = []*candaceosv1.WebUIApp{}
	}
	for _, app := range snapshot.Apps {
		if app != nil && app.UpdatedAt == nil {
			app.UpdatedAt = zeroTimestamp()
		}
	}
	if snapshot.Activity == nil {
		snapshot.Activity = []*candaceosv1.WebUIActivity{}
	}
	for _, activity := range snapshot.Activity {
		if activity != nil && activity.At == nil {
			activity.At = zeroTimestamp()
		}
	}
	if snapshot.Fleet == nil {
		snapshot.Fleet = &candaceosv1.WebUIFleet{}
	}
	if snapshot.Fleet.Quorum == nil {
		snapshot.Fleet.Quorum = &candaceosv1.WebUIQuorum{}
	}
	if snapshot.Fleet.Nodes == nil {
		snapshot.Fleet.Nodes = []*candaceosv1.WebUINode{}
	}
	for _, node := range snapshot.Fleet.Nodes {
		if node != nil && node.LastSeen == nil {
			node.LastSeen = zeroTimestamp()
		}
	}
	if snapshot.Run != nil {
		if snapshot.Run.StartedAt == nil {
			snapshot.Run.StartedAt = zeroTimestamp()
		}
		if snapshot.Run.Entries == nil {
			snapshot.Run.Entries = []*candaceosv1.WebUIRunEntry{}
		}
		for _, entry := range snapshot.Run.Entries {
			if entry != nil && entry.At == nil {
				entry.At = zeroTimestamp()
			}
		}
	}
	return snapshot
}

func (h *Handler) unavailableSnapshot() *candaceosv1.WebUISnapshot {
	return &candaceosv1.WebUISnapshot{
		System: &candaceosv1.WebUISystem{
			Name:      h.brand.ProductName,
			AgentName: h.brand.AgentName,
			Status:    "unavailable",
			Summary:   "Waiting for the local control plane",
		},
	}
}

func zeroTimestamp() *timestamppb.Timestamp {
	return timestamppb.New(time.Time{})
}

func displayName(name, fallback string) string {
	if strings.TrimSpace(name) != "" {
		return name
	}
	if strings.TrimSpace(fallback) != "" {
		return fallback
	}
	return "Unnamed"
}

func formatPercent(value float64) string {
	if value < 0 {
		value = 0
	}
	if value > 100 {
		value = 100
	}
	return fmt.Sprintf("%.0f%%", value)
}

func harnessBackendLabel(backend candaceosv1.HarnessBackend) string {
	switch backend {
	case candaceosv1.HarnessBackend_HARNESS_BACKEND_COPILOT_CLI:
		return "Copilot CLI"
	case candaceosv1.HarnessBackend_HARNESS_BACKEND_DEMO:
		return "Demo"
	case candaceosv1.HarnessBackend_HARNESS_BACKEND_OLLAMA:
		return "Ollama"
	case candaceosv1.HarnessBackend_HARNESS_BACKEND_EMBEDDED:
		return "Embedded"
	case candaceosv1.HarnessBackend_HARNESS_BACKEND_OPENCODE:
		return "OpenCode"
	default:
		return "Agent"
	}
}

func harnessRuntimeLabel(system *candaceosv1.WebUISystem) string {
	if system == nil {
		return "Agent"
	}
	label := harnessBackendLabel(system.GetHarnessBackend())
	if system.GetHarnessBackend() == candaceosv1.HarnessBackend_HARNESS_BACKEND_EMBEDDED {
		label = system.GetHarnessImplementation()
	}
	if system.GetHarnessModel() != "" {
		label += " · " + system.GetHarnessModel()
	}
	return label
}

func canCreateApps(system *candaceosv1.WebUISystem) bool {
	return hasHarnessCapability(system, candaceosv1.HarnessCapability_HARNESS_CAPABILITY_WORKSPACE_WRITE)
}

func canSteerActiveTurn(system *candaceosv1.WebUISystem) bool {
	return hasHarnessCapability(system, candaceosv1.HarnessCapability_HARNESS_CAPABILITY_ACTIVE_TURN_STEERING)
}

func hasHarnessCapability(system *candaceosv1.WebUISystem, expected candaceosv1.HarnessCapability) bool {
	if system == nil {
		return false
	}
	for _, capability := range system.GetHarnessCapabilities() {
		if capability == expected {
			return true
		}
	}
	return false
}

func initial(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "?"
	}
	for _, character := range value {
		return strings.ToUpper(string(character))
	}
	return "?"
}

// relativeTime renders a snapshot timestamp as a relative age. An absent or
// zero timestamp is the snapshot's own instant rather than an unobserved one,
// so it reads "now"; everything else is the shared ladder in pkg/core.
func relativeTime(value *timestamppb.Timestamp) string {
	if value == nil || value.AsTime().IsZero() {
		return "now"
	}
	return core.FormatAgo(time.Since(value.AsTime()))
}

func timestampFormat(value *timestamppb.Timestamp, layout string) string {
	if value == nil {
		return ""
	}
	return value.AsTime().Format(layout)
}

func statusLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "Unknown"
	}
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == '_' || r == '-' || r == ' ' })
	for index := range parts {
		if parts[index] == "" {
			continue
		}
		parts[index] = strings.ToUpper(parts[index][:1]) + strings.ToLower(parts[index][1:])
	}
	return strings.Join(parts, " ")
}

func tone(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "alive", "complete", "healthy", "online", "ready", "running", "succeeded", "done", "approved", "leader":
		return "positive"
	case "awaiting_approval", "busy", "working", "deploying", "pending", "queued",
		"steering", "compacted", "truncated", "requested", "starting", "suspect",
		"warning", "candidate":
		return "attention"
	case "canceled", "dead", "failed", "offline", "unavailable", "stopped", "rejected",
		"blocked", "error":
		return "negative"
	default:
		return "neutral"
	}
}
