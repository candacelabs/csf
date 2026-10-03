// Copyright 2026 Candace Labs

// Package opsview is the ops view: a gotth-live page that shows every
// harness session on this machine as a live card, read from the run
// directories under the harness state directory. Each card is a widget SDK
// widget in a keyed collection; cards update the moment a line lands in a
// session's event log, because the page follows the files through the
// kernel's change notification rather than polling them.
//
// It is a service: it owns no listener and no process. The harness host app
// grants it the file and watch capabilities over the state directory, mounts
// it into the host runtime and binds the listener.
package opsview

import (
	"errors"
	"log/slog"
	"strings"

	"github.com/gin-gonic/gin"

	ipcfs "github.com/candacelabs/csf/ipc/fs"
	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/widget"
	"github.com/candacelabs/csf/runtime"
)

// Routes: the page and the live mount under it. The live handler serves its
// own runtime script beneath the mount.
const (
	PagePath      = "/"
	LivePath      = "/live"
	liveAssetPath = LivePath + "/*asset"

	// BoardRegion is the parent region every card lives in: the keyed
	// collection's region, so a card's region is BoardRegion:<assignment>.
	BoardRegion = "opsview.sessions"
)

var (
	// ErrNoFiles reports a view built without the file capability.
	ErrNoFiles = errors.New("ops view: the file capability over the state directory is required")
	// ErrNoWatcher reports a view built without the watch capability.
	ErrNoWatcher = errors.New("ops view: the watch capability over the state directory is required")
)

// viewState is one connection's view: the cards by assignment, and a notice
// when the follow effect failed.
type viewState struct {
	cards  widget.KeyedState[SessionCard]
	notice string
}

// OpsView is the live application and its routes.
type OpsView struct {
	files   ipcfs.IFiles
	watcher ipcfs.IWatcher
	cards   *widget.KeyedCollection[SessionCard, live.AnonymousIdentity, *sessionCard]
	live    *live.App[viewState, live.AnonymousIdentity]
	logger  *slog.Logger
}

var _ runtime.IService = (*OpsView)(nil)

// NewOpsView builds the view over the harness state directory: files reads
// it and watcher reports its changes. origins is the browser Origin
// allowlist, one per address the host serves; nothing else is accepted.
func NewOpsView(files ipcfs.IFiles, watcher ipcfs.IWatcher, origins []string, logger *slog.Logger) (*OpsView, error) {
	if files == nil {
		return nil, ErrNoFiles
	}
	if watcher == nil {
		return nil, ErrNoWatcher
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	cards, err := widget.NewKeyedCollection[SessionCard, live.AnonymousIdentity](BoardRegion, newSessionCardAt)
	if err != nil {
		return nil, err
	}
	view := &OpsView{files: files, watcher: watcher, cards: cards, logger: logger}
	board := widget.KeyedFragment(cards, func(state viewState) widget.KeyedState[SessionCard] { return state.cards })
	board.Render, board.Dirty = view.render, boardChanged
	view.live, err = live.New(live.Config[viewState, live.AnonymousIdentity]{
		Init:         view.initialize,
		Reduce:       view.reduce,
		Fragments:    []live.Fragment[viewState]{board},
		Events:       cards.Events(),
		Origins:      origins,
		Authenticate: live.Anonymous,
		Authorize:    live.AllowAll[live.AnonymousIdentity],
		CSRF:         live.NoCSRFCheck,
		Logger:       logger,
	})
	if err != nil {
		return nil, err
	}
	return view, nil
}

// Start starts the live application's connection scope.
func (view *OpsView) Start(scope *runtime.Scope) error { return view.live.Start(scope) }

// Register mounts the page and the live routes on the caller's router.
func (view *OpsView) Register(router gin.IRouter) {
	router.GET(PagePath, gin.WrapH(view.live.PageHandler(view.renderPage)))
	router.GET(LivePath, gin.WrapH(view.live.Handler()))
	router.GET(liveAssetPath, gin.WrapH(view.live.Handler()))
}

// PageURL is the view's page under base, the host's address.
func PageURL(base string) string { return strings.TrimRight(base, "/") + PagePath }

// reduce is the pure transition. A session event for a card not yet on the
// board adds the member first, so the collection can route it; a failed
// effect becomes the board's notice; everything else, the browser's expand
// included, is the collection's to route to the member it names.
func (view *OpsView) reduce(state viewState, event live.Event) (viewState, []live.Effect[live.AnonymousIdentity]) {
	switch event.Name {
	case EventSession:
		key, _, exists := view.cards.Lookup(state.cards, event.FragmentID)
		if !exists {
			added, err := state.cards.Upsert(key, SessionCard{})
			if err != nil {
				return state, nil
			}
			state.cards = added
		}
	case live.EffectFailedEvent:
		state.notice = event.Fields.Get(live.EffectFailedSourceField) + " failed: " + event.Fields.Get(live.EffectFailedErrorField)
		return state, nil
	}
	var effects []live.Effect[live.AnonymousIdentity]
	state.cards, effects = view.cards.Reduce(state.cards, event)
	return state, effects
}
