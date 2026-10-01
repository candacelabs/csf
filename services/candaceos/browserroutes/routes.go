// Package browserroutes is the single source of truth for CandaceOS Core's
// browser-facing URL space.
//
// The package owns the route patterns, the Gin parameter names embedded in
// them, and the builders that bind those parameters. Handlers, templates, and
// the embedded browser client all read paths from here, so a route cannot be
// changed in one place and missed in another. Callers may rely on these paths
// being stable across releases and on each exported builder producing a path
// that matches the pattern of the same name. The package holds no handlers, no
// transport, and no state.
package browserroutes

import (
	"net/url"
	"strings"
)

// Gin parameter names live beside the route patterns so handlers and URL
// builders cannot drift.
const (
	ParamAssetPath  = "filepath"
	ParamApprovalID = "id"
	ParamRunID      = "runID"
	ParamSessionID  = "sessionID"
)

const (
	Index           = "/"
	Health          = "/healthz"
	Assets          = "/assets/*" + ParamAssetPath
	ClawChat        = "/claws/:" + ParamSessionID + "/chat"
	Snapshot        = "/api/snapshot"
	Events          = "/api/events"
	Prompts         = "/api/prompts"
	CurrentRunAbort = "/api/runs/current/abort"
	Approval        = "/api/approvals/:" + ParamApprovalID
	ClawMessages    = "/api/claws/:" + ParamSessionID + "/messages"
	ClawRunAbort    = "/api/claws/:" + ParamSessionID + "/runs/:" + ParamRunID + "/abort"
)

// BrandStylesheet is the generated palette stylesheet served from the asset
// space. No embedded asset carries this name: the bytes are produced per brand
// and served same-origin, which keeps the page's style-src at 'self' without an
// inline style block.
const BrandStylesheet = "brand.css"

// BrandStylesheetPath returns the public URL of the generated brand stylesheet.
func BrandStylesheetPath() string {
	return AssetPath(BrandStylesheet)
}

// AssetPath returns the public URL for one embedded browser asset.
func AssetPath(filename string) string {
	return bindParameter(Assets, ParamAssetPath, filename)
}

// ApprovalPath returns the mutation URL for one approval. An empty identifier
// deliberately returns the stable prefix used by the browser client.
func ApprovalPath(approvalID string) string {
	return bindParameter(Approval, ParamApprovalID, approvalID)
}

// ClawChatPath returns the live-chat page URL for one harness session.
func ClawChatPath(sessionID string) string {
	return bindParameter(ClawChat, ParamSessionID, sessionID)
}

// ClawMessagePath returns the steering URL for one harness session.
func ClawMessagePath(sessionID string) string {
	return bindParameter(ClawMessages, ParamSessionID, sessionID)
}

// ClawRunAbortPath returns the abort URL for one exact harness run.
func ClawRunAbortPath(sessionID, runID string) string {
	path := bindParameter(ClawRunAbort, ParamSessionID, sessionID)
	return bindParameter(path, ParamRunID, runID)
}

func bindParameter(pattern, parameter, value string) string {
	escaped := url.PathEscape(value)
	pattern = strings.Replace(pattern, ":"+parameter, escaped, 1)
	return strings.Replace(pattern, "*"+parameter, escaped, 1)
}
