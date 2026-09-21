package kanban

import (
	"context"
	"fmt"
	"strconv"

	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/google/uuid"

	workv1 "github.com/candacelabs/csf/proto/candace/work/v1"
	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
	"github.com/candacelabs/csf/services/copilot-adapter/kanban/card"
)

func (board *Board) reduce(state boardState, event live.Event) (boardState, []live.Effect[live.AnonymousIdentity]) {
	switch event.Name {
	case eventSnapshot:
		next, err := board.readSnapshot(event)
		if err != nil {
			state.err = err.Error()
			return state, nil
		}
		return next, nil
	case live.EffectFailedEvent, eventSourceError:
		state.err = event.Fields.Get(live.EffectFailedErrorField)
		return state, nil
	case eventRefresh:
		return state, []live.Effect[live.AnonymousIdentity]{board.action(event, card.KanbanCardState{})}
	case eventLink, eventMove:
		_, value, exists := board.cards.Lookup(state.cards, event.FragmentID)
		if exists {
			return state, []live.Effect[live.AnonymousIdentity]{board.action(event, value)}
		}
	}
	state.cards, _ = board.cards.Reduce(state.cards, event)
	return state, nil
}

func (board *Board) action(event live.Event, value card.KanbanCardState) live.Effect[live.AnonymousIdentity] {
	return live.Effect[live.AnonymousIdentity]{Source: event.Name, Run: func(ctx context.Context, session live.Session[live.AnonymousIdentity], emit live.Emitter) error {
		switch event.Name {
		case eventRefresh:
			_, err := board.adapter.RefreshWorkspace(ctx, api.RefreshWorkspaceRequestObject{})
			return err
		case eventLink:
			generation, err := strconv.ParseInt(event.Fields.Get(fieldGeneration), 10, 64)
			if err != nil || generation < 0 || uint64(generation) != value.LinkGeneration {
				return fmt.Errorf("task association changed; refresh before linking")
			}
			identifier, err := uuid.Parse(value.SessionID)
			if err != nil {
				return err
			}
			_, err = board.adapter.LinkWorkspaceTask(ctx, identifier, event.Fields.Get(fieldTaskURL), generation)
			return err
		case eventMove:
			return board.move(ctx, event, value)
		}
		return fmt.Errorf("unknown planning action")
	}}
}

func (board *Board) move(ctx context.Context, event live.Event, value card.KanbanCardState) error {
	status, exists := workv1.WorkStatus_value[event.Fields.Get(fieldStatus)]
	if !exists || status == int32(workv1.WorkStatus_WORK_STATUS_UNSPECIFIED) {
		return fmt.Errorf("choose a declared task status")
	}
	if value.TaskURL == "" {
		return fmt.Errorf("link a task before moving this session")
	}
	identifier, err := uuid.Parse(value.SessionID)
	if err != nil {
		return err
	}
	link := api.WorkspaceTaskLink{SessionId: identifier, TaskUrl: value.TaskURL, Generation: int64(value.LinkGeneration)}
	_, err = board.adapter.MoveWorkspaceTask(ctx, link, event.Fields.Get(fieldCheckpoint), workv1.WorkStatus(status), event.Fields.Get(fieldNextAction), event.Fields.Get(fieldReason))
	return err
}
