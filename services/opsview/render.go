// Copyright 2026 Candace Labs

package opsview

import (
	"bytes"
	"context"
	_ "embed"
	"html/template"
	"io"
	"slices"
	"strings"

	"github.com/a-h/templ"

	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/widget"
)

// The page, the board region and one card. Every render is a pure function
// of state: equal states render byte-identical markup, which is what lets
// the library suppress a patch nobody needs.
//
//go:embed view.html
var pageSource string

var views = template.Must(template.New("opsview").Parse(pageSource))

const (
	pageTemplate  = "page"
	boardTemplate = "board"
	cardTemplate  = "card"
)

// pageView is the document's data: the rendered board and the runtime tag.
type pageView struct {
	Board  template.HTML
	Script template.HTML
}

// boardView is the board region's data: cards already rendered, in display
// order, and the notice.
type boardView struct {
	Region string
	Notice string
	Cards  []template.HTML
}

// cardView is one card's data: its state, its region, the recent events it
// shows, how many it holds back, and the expand control's attributes.
type cardView struct {
	SessionCard
	Region   string
	Shown    []RecentEvent
	Hidden   int
	OnExpand template.HTMLAttr
}

func renderCard(region string, state SessionCard) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error {
		onExpand, err := attributes(live.On("click", EventExpand))
		if err != nil {
			return err
		}
		shown := state.Recent
		if !state.Expanded && len(shown) > recentShown {
			shown = shown[len(shown)-recentShown:]
		}
		return views.ExecuteTemplate(writer, cardTemplate, cardView{
			SessionCard: state, Region: region, Shown: shown,
			Hidden: len(state.Recent) - len(shown), OnExpand: template.HTMLAttr(onExpand),
		})
	})
}

// attributes renders templ attributes as the text an element carries, so the
// template writes the library's own spelling of its wiring.
func attributes(attributes templ.Attributes) (string, error) {
	var buffer bytes.Buffer
	if err := templ.RenderAttributes(context.Background(), &buffer, attributes); err != nil {
		return "", err
	}
	return buffer.String(), nil
}

// render draws the board: every card in display order, each through the
// collection so the markup is the member's own render.
func (view *OpsView) render(state viewState) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error {
		board := boardView{Region: BoardRegion, Notice: state.notice}
		for _, key := range displayOrder(state.cards) {
			var content bytes.Buffer
			if err := view.cards.RenderItem(state.cards, key).Render(ctx, &content); err != nil {
				return err
			}
			// Only markup html/template already escaped crosses this boundary.
			board.Cards = append(board.Cards, template.HTML(content.String()))
		}
		return views.ExecuteTemplate(writer, boardTemplate, board)
	})
}

func (view *OpsView) renderPage(state viewState) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error {
		board, err := renderComponent(ctx, view.render(state))
		if err != nil {
			return err
		}
		script, err := renderComponent(ctx, live.Script(LivePath))
		if err != nil {
			return err
		}
		return views.ExecuteTemplate(writer, pageTemplate, pageView{Board: template.HTML(board), Script: template.HTML(script)})
	})
}

// displayOrder puts running sessions first, then the most recently started.
func displayOrder(cards widget.KeyedState[SessionCard]) []string {
	items := cards.Items()
	slices.SortStableFunc(items, func(a, b widget.KeyedItem[SessionCard]) int {
		if rank := statusRank(a.State.Status) - statusRank(b.State.Status); rank != 0 {
			return rank
		}
		return b.State.StartedAt.Compare(a.State.StartedAt)
	})
	keys := make([]string, 0, len(items))
	for _, item := range items {
		keys = append(keys, item.Key)
	}
	return keys
}

func statusRank(status Status) int {
	switch status {
	case StatusRunning:
		return 0
	case StatusFinished:
		return 1
	case StatusFailed:
		return 2
	}
	return 3
}

// boardChanged reports whether the board's own markup moved: its notice, or
// the set and order of cards. A card's own change is its child's to report.
func boardChanged(previous viewState, next viewState) bool {
	return previous.notice != next.notice ||
		strings.Join(displayOrder(previous.cards), "\n") != strings.Join(displayOrder(next.cards), "\n")
}

func renderComponent(ctx context.Context, component templ.Component) (string, error) {
	var buffer bytes.Buffer
	if err := component.Render(ctx, &buffer); err != nil {
		return "", err
	}
	return buffer.String(), nil
}
