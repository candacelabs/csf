package kanban

import (
	"bytes"
	"context"
	"embed"
	"html/template"
	"io"
	"strings"

	"github.com/a-h/templ"
	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/widget"

	workv1 "github.com/candacelabs/csf/proto/candace/work/v1"
	"github.com/candacelabs/csf/services/copilot-adapter/kanban/card"
)

// These templates are emitted from Mantine components by ui/scripts/gen-kanban.mjs.
//
//go:embed templates/*_cgen.html
var templates embed.FS

const (
	templatePattern = "templates/*_cgen.html"
	cardTemplate    = "card_cgen.html"
	boardTemplate   = "board_cgen.html"
	statusPrefix    = "WORK_STATUS_"
)

var views = template.Must(template.ParseFS(templates, templatePattern))

type cardWidget struct {
	*card.KanbanCard[live.AnonymousIdentity]
}

func newCardWidget(region string) *cardWidget {
	return &cardWidget{KanbanCard: card.NewKanbanCardAt[live.AnonymousIdentity](region)}
}

func (instance *cardWidget) Register() widget.Registration {
	registration := instance.KanbanCard.Register()
	registration.Events = []string{eventLink, eventMove, eventRefresh}
	return registration
}

type statusOption struct {
	Value, Label string
	Selected     bool
}

type cardView struct {
	card.KanbanCardState
	Region   string
	CanMove  bool
	Statuses []statusOption
}

func (instance *cardWidget) Render(state card.KanbanCardState) templ.Component {
	view := cardView{KanbanCardState: state, Region: instance.Register().Region,
		CanMove: state.Condition == workv1.ResumeCondition_RESUME_CONDITION_READY.String()}
	for _, status := range taskStatuses() {
		view.Statuses = append(view.Statuses, statusOption{Value: status.String(), Label: statusLabel(status), Selected: state.TaskStatus == status.String()})
	}
	return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error {
		return views.ExecuteTemplate(writer, cardTemplate, view)
	})
}

func taskStatuses() []workv1.WorkStatus {
	return []workv1.WorkStatus{workv1.WorkStatus_WORK_STATUS_QUEUED, workv1.WorkStatus_WORK_STATUS_ACTIVE,
		workv1.WorkStatus_WORK_STATUS_BLOCKED, workv1.WorkStatus_WORK_STATUS_OPERATOR, workv1.WorkStatus_WORK_STATUS_DONE}
}

func statusLabel(status workv1.WorkStatus) string {
	return strings.ToLower(strings.TrimPrefix(status.String(), statusPrefix))
}

type columnView struct {
	ID, Title string
	Count     int
	Cards     template.HTML
}

type boardView struct {
	Region, Error string
	Columns       []columnView
}

func (board *Board) render(state boardState) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error {
		view := boardView{Region: boardRegion, Error: state.err, Columns: []columnView{{ID: unassignedColumn, Title: "Needs task checkpoint"}}}
		for _, status := range taskStatuses() {
			view.Columns = append(view.Columns, columnView{ID: status.String(), Title: statusLabel(status)})
		}
		for index := range view.Columns {
			column := &view.Columns[index]
			var content bytes.Buffer
			for _, item := range state.cards.Items() {
				if columnFor(item.State) != column.ID {
					continue
				}
				if err := board.cards.RenderItem(state.cards, item.Key).Render(ctx, &content); err != nil {
					return err
				}
				column.Count++
			}
			// Only child markup already escaped by html/template crosses this
			// boundary. User/source strings never become template.HTML directly.
			column.Cards = template.HTML(content.String())
		}
		return views.ExecuteTemplate(writer, boardTemplate, view)
	})
}
