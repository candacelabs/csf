// Package kanban mounts shared task planning into an existing HTTP server.
package kanban

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/widget"
	"github.com/gin-gonic/gin"

	copilotadapter "github.com/candacelabs/csf/services/copilot-adapter"
	"github.com/candacelabs/csf/services/copilot-adapter/kanban/card"
)

const (
	ViewPath         = "/v1/kanban/view"
	LivePath         = "/v1/kanban/live"
	liveAssetPath    = LivePath + "/*asset"
	boardRegion      = "kanban.board"
	unassignedColumn = "unassigned"
	eventLink        = "kanban.link"
	eventMove        = "kanban.move"
	eventRefresh     = "kanban.refresh"
	eventSnapshot    = "kanban.snapshot"
	eventSourceError = "kanban.source-error"
	sourceWorkspace  = "workspace.committed"
	fieldSnapshot    = "snapshot"
	fieldTaskURL     = "task_url"
	fieldGeneration  = "expected_generation"
	fieldCheckpoint  = "expected_checkpoint"
	fieldStatus      = "status"
	fieldNextAction  = "next_action"
	fieldReason      = "reason"
)

type boardState struct {
	cards widget.KeyedState[card.KanbanCardState]
	err   string
}

// Board owns one live handler. Its host mounts it and calls Close during
// shutdown. Each browser owns one subscription; individual cards own no IO.
type Board struct {
	adapter *copilotadapter.CopilotAdapter
	cards   *widget.KeyedCollection[card.KanbanCardState, live.AnonymousIdentity, *cardWidget]
	live    *live.App[boardState, live.AnonymousIdentity]
}

// NewBoard creates handlers only; it opens no listener or process. Authentication
// is inherited from the host's mounted routes; origins must be explicitly listed.
func NewBoard(adapter *copilotadapter.CopilotAdapter, origins []string, logger *slog.Logger) (*Board, error) {
	if adapter == nil {
		return nil, fmt.Errorf("kanban requires an adapter")
	}
	collection, err := widget.NewKeyedCollection[card.KanbanCardState, live.AnonymousIdentity](boardRegion, newCardWidget)
	if err != nil {
		return nil, err
	}
	board := &Board{adapter: adapter, cards: collection}
	fragment := widget.KeyedFragment(collection, func(state boardState) widget.KeyedState[card.KanbanCardState] { return state.cards })
	fragment.Render, fragment.Dirty = board.render, boardStructureChanged
	board.live, err = live.New(live.Config[boardState, live.AnonymousIdentity]{
		Init: board.initialize, Reduce: board.reduce,
		Fragments: []live.Fragment[boardState]{fragment}, Events: collection.Events(),
		Origins: origins, Authenticate: live.Anonymous,
		Authorize: live.AllowAll[live.AnonymousIdentity], CSRF: live.NoCSRFCheck, Logger: logger,
	})
	return board, err
}

func (board *Board) Register(router gin.IRouter) {
	router.GET(ViewPath, gin.WrapH(board.live.PageHandler(board.render)))
	router.GET(LivePath, gin.WrapH(board.live.Handler()))
	router.GET(liveAssetPath, gin.WrapH(board.live.Handler()))
}

func (board *Board) Close(ctx context.Context) error { return board.live.Close(ctx) }

// ViewHandler permits consumer tests and non-Gin hosts to mount the same page.
func (board *Board) ViewHandler() http.Handler { return board.live.PageHandler(board.render) }
func (board *Board) LiveHandler() http.Handler { return board.live.Handler() }

func (board *Board) initialize(ctx context.Context, session live.Session[live.AnonymousIdentity]) (boardState, []live.Effect[live.AnonymousIdentity], error) {
	state, err := board.load(ctx)
	return state, []live.Effect[live.AnonymousIdentity]{{Source: sourceWorkspace, Run: board.watch}}, err
}

func boardStructureChanged(previous, next boardState) bool {
	if previous.err != next.err {
		return true
	}
	before, after := previous.cards.Items(), next.cards.Items()
	if len(before) != len(after) {
		return true
	}
	for index, item := range before {
		if item.Key != after[index].Key || columnFor(item.State) != columnFor(after[index].State) {
			return true
		}
	}
	return false
}
