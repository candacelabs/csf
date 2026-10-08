// Copyright 2026 Candace Labs

package opsview

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/candacelabs/csf/io/inproc"
	"github.com/candacelabs/csf/pkg/gotth/live"
)

// ViewerCookie names the browser session cookie that tells one viewer's view
// from another's. It carries a random identifier and nothing else; it ends
// when the browser session does.
const ViewerCookie = "csf_workbench_viewer"

// viewersKept is how many viewers' views the page remembers. The Workbench is
// one operator's page on a few devices; past this many, the view stored
// longest ago is forgotten and that viewer starts from the default view.
const viewersKept = 64

// ViewerIdentity is who a connection is: the viewer the cookie names, or
// nobody for a client that sent none, whose view is not remembered.
type ViewerIdentity struct {
	Viewer string
}

// Subject is the viewer, for the library's logs.
func (identity ViewerIdentity) Subject() string {
	if identity.Viewer == "" {
		return "anonymous"
	}
	return identity.Viewer
}

// authenticateViewer reads the viewer from the request's cookie. A missing or
// malformed cookie is an anonymous viewer, never a refusal: the page works
// the same, it only forgets the view on reload.
func authenticateViewer(request *http.Request) (ViewerIdentity, error) {
	cookie, err := request.Cookie(ViewerCookie)
	if err != nil {
		return ViewerIdentity{}, nil
	}
	if _, err := uuid.Parse(cookie.Value); err != nil {
		return ViewerIdentity{}, nil
	}
	return ViewerIdentity{Viewer: cookie.Value}, nil
}

// withViewerCookie gives a page request without a viewer one, on the response
// and on the request the page renders from, so the socket that follows
// carries it.
func withViewerCookie(context *gin.Context) {
	if _, err := context.Request.Cookie(ViewerCookie); err == nil {
		return
	}
	cookie := &http.Cookie{Name: ViewerCookie, Value: uuid.NewString(), Path: PagePath, HttpOnly: true, SameSite: http.SameSiteStrictMode}
	http.SetCookie(context.Writer, cookie)
	context.Request.AddCookie(cookie)
}

// ViewPrefs is one viewer's view of the page: which cards are open, which
// show their internals or their composer, the idle list's filter, and which
// sections are folded away. The server owns it, so it survives every patch,
// and it is remembered per viewer, so it survives a reload.
type ViewPrefs struct {
	// Expanded, Internals and Composing are assignments, sorted.
	Expanded  []string
	Internals []string
	Composing []string
	// OthersOpen shows the idle and finished sessions as a list rather than
	// a count; Filter and Show narrow that list.
	OthersOpen bool
	Filter     string
	Show       Status
	// Opened are the lower sections the viewer unfolded, sorted; each is
	// folded to one line until then.
	Opened []string
	// Defined is the tile whose definition is showing, if any.
	Defined string
}

// toggled is the sorted set with key added or removed.
func toggled(set []string, key string, on bool) []string {
	set = slices.DeleteFunc(slices.Clone(set), func(member string) bool { return member == key })
	if on {
		set = append(set, key)
		slices.Sort(set)
	}
	return set
}

// The view events a browser sends. Each changes only the viewer's view.
const (
	// EventInternals shows or hides a card's internals and raw activity.
	EventInternals = "opsview.internals"
	// EventCompose opens or closes a card's composer.
	EventCompose = "opsview.compose"
	// EventConfirm asks a card to confirm FieldAction (the host panel's
	// field), or, empty, to stop
	// asking.
	EventConfirm = "opsview.confirm"
	// EventOthers opens or folds the idle and finished list.
	EventOthers = "opsview.others"
	// EventFilter narrows that list to sessions whose name, branch, ticket or
	// activity contain FieldFilter, and to the status in FieldShow.
	EventFilter = "opsview.filter"
	FieldFilter = "filter"
	FieldShow   = "show"
	// EventSection unfolds or folds the section FieldSection.
	EventSection = "opsview.section"
	FieldSection = "section"
	// EventDefine shows a tile's definition, FieldTile, or hides it when it is
	// already showing.
	EventDefine = "opsview.define"
	FieldTile   = "tile"

	sourceRemember = "opsview.remember"
)

// cardViewEvents are the card events that change the viewer's view, and the
// set each toggles.
var cardViewEvents = map[string]func(prefs *ViewPrefs) *[]string{
	EventExpand:    func(prefs *ViewPrefs) *[]string { return &prefs.Expanded },
	EventInternals: func(prefs *ViewPrefs) *[]string { return &prefs.Internals },
	EventCompose:   func(prefs *ViewPrefs) *[]string { return &prefs.Composing },
}

// reduceView is the page's half of a view event: the viewer's prefs change,
// and the effect that remembers them. ok is false for an event that is not a
// view event.
func (view *OpsView) reduceView(state viewState, event live.Event) (viewState, []live.Effect[ViewerIdentity], bool) {
	prefs := state.prefs
	if set, isCard := cardViewEvents[event.Name]; isCard {
		key, member, exists := view.cards.Lookup(state.cards, event.FragmentID)
		if !exists {
			return state, nil, false
		}
		target := set(&prefs)
		on := !slices.Contains(*target, key)
		*target = toggled(*target, key, on)
		state.prefs = prefs
		effects := []live.Effect[ViewerIdentity]{view.remember(prefs)}
		if event.Name == EventExpand && on && member.Partial {
			effects = append(effects, live.Effect[ViewerIdentity]{Source: sourceOpen, Run: view.openWhole(key)})
		}
		// The card's own state follows too: the collection routes the event on.
		next, cardEffects := view.cards.Reduce(state.cards, event)
		state.cards = next
		return state, append(effects, cardEffects...), true
	}
	switch event.Name {
	case EventOthers:
		prefs.OthersOpen = !prefs.OthersOpen
	case EventFilter:
		prefs.Filter = strings.TrimSpace(event.Fields.Get(FieldFilter))
		if show := Status(event.Fields.Get(FieldShow)); show == "" || slices.Contains(otherStatuses, show) {
			prefs.Show = show
		}
	case EventSection:
		section := event.Fields.Get(FieldSection)
		if !slices.Contains(foldableSections, section) {
			return state, nil, true
		}
		prefs.Opened = toggled(prefs.Opened, section, !slices.Contains(prefs.Opened, section))
	case EventDefine:
		tile := event.Fields.Get(FieldTile)
		if prefs.Defined == tile || !slices.Contains(tileNames, tile) {
			tile = ""
		}
		prefs.Defined = tile
	default:
		return state, nil, false
	}
	state.prefs = prefs
	return state, []live.Effect[ViewerIdentity]{view.remember(prefs)}, true
}

// sourceOpen names the effect that reads an opened card's whole log.
const sourceOpen = "opsview.open"

// remember stores the viewer's prefs, for the next connection from the same
// browser session.
func (view *OpsView) remember(prefs ViewPrefs) live.Effect[ViewerIdentity] {
	return live.Effect[ViewerIdentity]{Source: sourceRemember, Run: func(ctx context.Context, session live.Session[ViewerIdentity], emit live.Emitter) error {
		if viewer := session.Identity().Viewer; viewer != "" {
			view.viewers.Store(viewer, prefs)
		}
		return nil
	}}
}

// recalled is the viewer's remembered prefs, or the default view.
func (view *OpsView) recalled(identity ViewerIdentity) ViewPrefs {
	if identity.Viewer == "" {
		return ViewPrefs{}
	}
	prefs, _ := view.viewers.Load(identity.Viewer)
	return prefs
}

func newViewers() *inproc.Recent[string, ViewPrefs] {
	viewers, _ := inproc.NewRecent[string, ViewPrefs](viewersKept)
	return viewers
}
