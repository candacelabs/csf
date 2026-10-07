package kanban

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/widget"
	"github.com/google/uuid"

	workv1 "github.com/candacelabs/csf/proto/candace/work/v1"
	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
	"github.com/candacelabs/csf/services/copilot-adapter/kanban/card"
)

func (board *Board) load(ctx context.Context) (boardState, error) {
	snapshot, err := board.adapter.Workspace(ctx)
	if err != nil {
		return boardState{}, err
	}
	links := make(map[uuid.UUID]api.WorkspaceTaskLink, len(snapshot.TaskLinks))
	for _, link := range snapshot.TaskLinks {
		links[link.SessionId] = link
	}
	items := make([]widget.KeyedItem[card.KanbanCardState], 0, len(snapshot.Sessions))
	for _, session := range snapshot.Sessions {
		if session.Id == nil {
			return boardState{}, fmt.Errorf("workspace session has no identity")
		}
		value := card.KanbanCardState{
			Name: session.DisplayName, SessionID: session.Id.String(), Status: string(session.Status),
			Model: session.Model, Worktree: session.WorkingDirectory,
		}
		if link, exists := links[*session.Id]; exists {
			value.TaskURL, value.LinkGeneration = link.TaskUrl, uint64(link.Generation)
			applyCheckpoint(&value, board.adapter.WorkspaceTask(ctx, link.TaskUrl, false))
		}
		items = append(items, widget.KeyedItem[card.KanbanCardState]{Key: value.SessionID, State: value})
	}
	states, err := board.cards.State(items)
	return boardState{cards: states}, err
}

func applyCheckpoint(value *card.KanbanCardState, record *workv1.ResumeRecord) {
	value.Condition = record.GetCondition().String()
	checkpoint := record.GetCheckpoint()
	value.CheckpointID, value.CheckpointURL = checkpoint.GetId(), record.GetCheckpointUrl()
	value.TaskStatus, value.Owner = checkpoint.GetStatus().String(), checkpoint.GetOwner()
	value.NextAction = checkpoint.GetNextAction()
	if evidence := checkpoint.GetEvidence(); len(evidence) > 0 {
		value.EvidenceURL = evidence[0].GetUrl()
	}
}

func columnFor(value card.KanbanCardState) string {
	if value.Condition != workv1.ResumeCondition_RESUME_CONDITION_READY.String() {
		return unassignedColumn
	}
	return value.TaskStatus
}

// Subscribe before reloading: a commit during the reload remains pending.
// The first reload closes the gap after Init. No timer or per-card worker exists.
func (board *Board) watch(ctx context.Context, session live.Session[live.AnonymousIdentity], emit live.Emitter) error {
	subscription := board.adapter.SubscribeWorkspace()
	defer subscription.Close()
	for {
		if err := board.emitSnapshot(ctx, emit); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-subscription.Changed:
		}
	}
}

func (board *Board) emitSnapshot(ctx context.Context, emit live.Emitter) error {
	state, err := board.load(ctx)
	if err != nil {
		return emit(live.Event{Name: eventSourceError, FragmentID: boardRegion,
			Fields: live.NewFields(map[string]string{live.EffectFailedErrorField: err.Error()})})
	}
	values := make([]card.KanbanCardState, 0, len(state.cards.Items()))
	for _, item := range state.cards.Items() {
		values = append(values, item.State)
	}
	data, err := json.Marshal(values)
	if err != nil {
		return err
	}
	return emit(live.Event{Name: eventSnapshot, FragmentID: boardRegion, Fields: live.NewFields(map[string]string{fieldSnapshot: string(data)})})
}

func (board *Board) readSnapshot(event live.Event) (boardState, error) {
	var values []card.KanbanCardState
	if err := json.Unmarshal([]byte(event.Fields.Get(fieldSnapshot)), &values); err != nil {
		return boardState{}, err
	}
	items := make([]widget.KeyedItem[card.KanbanCardState], 0, len(values))
	for _, value := range values {
		items = append(items, widget.KeyedItem[card.KanbanCardState]{Key: value.SessionID, State: value})
	}
	state, err := board.cards.State(items)
	return boardState{cards: state}, err
}
