// Copyright 2026 Candace Labs

package opsview

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"

	"github.com/a-h/templ"

	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/widget"
)

// The session widget's identity on the wire. Every card is one instance of
// this definition under the board's keyed collection, at region
// opsview.sessions:<assignment>; the board region is the collection's own.
const (
	WidgetName = "SessionCard"
	// EventSession carries one whole card, as JSON in the card field. It is
	// internal: the follow effect emits it, and a browser may not, because a
	// browser posting one would be forging the event log's own truth.
	EventSession = "opsview.session"
	FieldCard    = "card"
	// EventExpand is the one event a browser may send: show or hide the rest
	// of a card's recent events. It reads nothing and changes no session.
	EventExpand = "opsview.expand"
)

// sessionCard is the widget SDK contract for one card: typed state, a pure
// reducer and a pure render. It owns no I/O; the follow effect reads the
// files and delivers cards as events, and an action's effect calls the
// control plane the card was bound to.
type sessionCard struct {
	region     string
	operations IWorkbenchOperations
}

// sessionCardsOver is the keyed collection's factory: it binds the
// definition to one instance's region and to the control plane, nil on a
// read-only page.
func sessionCardsOver(operations IWorkbenchOperations) func(region string) *sessionCard {
	return func(region string) *sessionCard { return &sessionCard{region: region, operations: operations} }
}

var _ widget.IWidget[SessionCard, ViewerIdentity] = (*sessionCard)(nil)

func (card *sessionCard) Register() widget.Registration {
	return widget.Registration{
		Name:     WidgetName,
		Region:   card.region,
		Events:   cardEvents(),
		Internal: []string{EventSession, EventActed},
		Payloads: []widget.EventPayload{
			{Event: EventSession, Fields: []string{FieldCard}},
			{Event: EventSend, Fields: []string{FieldMessage}},
			{Event: EventActed, Fields: []string{FieldNotice}},
			{Event: EventConfirm, Fields: []string{FieldAction}},
		},
	}
}

// Mount is never reached through the collection, whose members take their
// initial state from the host snapshot; it returns the empty card for a
// host that registers the definition on its own.
func (card *sessionCard) Mount(ctx context.Context, session live.Session[ViewerIdentity]) (SessionCard, []live.Effect[ViewerIdentity], error) {
	return SessionCard{}, nil, nil
}

// Reduce replaces the card with the one a session event carries, keeping
// the page's own fields, or applies one of the viewer's choices. A card that
// does not decode leaves the state as it was: the reducer is pure and reports
// nothing, and the follow effect that emitted it is the place that would.
func (card *sessionCard) Reduce(state SessionCard, event live.Event) (SessionCard, []live.Effect[ViewerIdentity]) {
	switch event.Name {
	case EventExpand:
		state.Expanded = !state.Expanded
		if !state.Expanded {
			state.Confirm, state.Composing, state.Internals = "", false, false
		}
		return state, nil
	case EventInternals:
		state.Internals = !state.Internals
		return state, nil
	case EventCompose:
		state.Composing = !state.Composing
		return state, nil
	case EventConfirm:
		state.Confirm = ""
		if action := event.Fields.Get(FieldAction); slices.Contains(confirmedActions, action) {
			state.Confirm = action
		}
		return state, nil
	case EventSession:
		var next SessionCard
		if json.Unmarshal([]byte(event.Fields.Get(FieldCard)), &next) != nil {
			return state, nil
		}
		next.Expanded, next.Notice, next.Internals, next.Composing = state.Expanded, state.Notice, state.Internals, state.Composing
		next.Confirm, next.Pending, next.Landed, next.Forge = state.Confirm, state.Pending, state.Landed, state.Forge
		return next, nil
	case EventActed:
		state.Notice, state.Pending = event.Fields.Get(FieldNotice), ""
		return state, nil
	}
	if _, isAction := cardActions[event.Name]; isAction {
		return card.act(state, event)
	}
	return state, nil
}

func (card *sessionCard) Render(state SessionCard) templ.Component {
	return renderCard(card.region, state, card.operations != nil)
}

func (card *sessionCard) Unmount(ctx context.Context, session live.Session[ViewerIdentity], state SessionCard) {
}

func (card *sessionCard) Snapshot(state SessionCard) widget.Snapshot {
	return widget.Snapshot{Widget: WidgetName, Fields: []widget.SnapshotField{
		{Name: "assignment", Value: state.Assignment},
		{Name: "agent", Value: state.Agent},
		{Name: "branch", Value: state.Branch},
		{Name: "status", Value: string(state.Status)},
		{Name: "model", Value: state.Model},
		{Name: "executor", Value: state.Executor},
		{Name: "turns", Value: strconv.Itoa(state.Turns)},
		{Name: "tool_calls", Value: strconv.Itoa(state.ToolCalls)},
		{Name: "gate_denials", Value: strconv.Itoa(state.GateDenials)},
		{Name: "background", Value: strconv.Itoa(state.Background)},
		{Name: "background_turns", Value: strconv.Itoa(state.BackgroundTurns)},
		{Name: "elapsed", Value: state.Elapsed},
	}}
}

// CardEvent is the event the follow effect emits for one session, addressed
// to that session's own region so the collection routes it to the member.
func CardEvent(region string, card SessionCard) (live.Event, error) {
	encoded, err := json.Marshal(card)
	if err != nil {
		return live.Event{}, err
	}
	return live.Event{Name: EventSession, FragmentID: region, Fields: live.NewFields(map[string]string{FieldCard: string(encoded)})}, nil
}

// act runs one card action as an effect that reports its outcome, or its
// failure, back to the card as a notice. An action that needs confirming runs
// only from the card's confirmation, so a stray tap cannot cancel or merge.
func (card *sessionCard) act(state SessionCard, event live.Event) (SessionCard, []live.Effect[ViewerIdentity]) {
	action := cardActions[event.Name]
	if card.operations == nil {
		state.Notice = "This page is read-only: it was mounted without the control plane."
		return state, nil
	}
	if slices.Contains(confirmedActions, event.Name) && state.Confirm != event.Name {
		state.Confirm = event.Name
		return state, nil
	}
	if state.Pending != "" {
		return state, nil
	}
	pending, call, refusal := action(card.operations, state.Assignment, event.Fields)
	if refusal != "" {
		state.Notice = refusal
		return state, nil
	}
	state.Notice, state.Pending, state.Confirm = pending, event.Name, ""
	if event.Name == EventSend {
		state.Composing = false
	}
	region, worktree := card.region, state.Worktree
	return state, []live.Effect[ViewerIdentity]{{Source: event.Name, Run: func(ctx context.Context, _ live.Session[ViewerIdentity], emit live.Emitter) error {
		notice, err := call(ctx)
		if err != nil {
			notice = actionNames[event.Name] + " failed: " + HumanError(err.Error(), worktree)
		}
		return emit(live.Event{Name: EventActed, FragmentID: region, Fields: live.NewFields(map[string]string{FieldNotice: notice})})
	}}}
}
