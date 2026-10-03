// Copyright 2026 Candace Labs

package opsview

import (
	"context"
	"encoding/json"
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
// files and delivers cards as events.
type sessionCard struct {
	region string
}

// newSessionCardAt binds the definition to one instance's region; the keyed
// collection calls it for every member.
func newSessionCardAt(region string) *sessionCard { return &sessionCard{region: region} }

var _ widget.IWidget[SessionCard, live.AnonymousIdentity] = (*sessionCard)(nil)

func (card *sessionCard) Register() widget.Registration {
	return widget.Registration{
		Name:     WidgetName,
		Region:   card.region,
		Events:   []string{EventExpand},
		Internal: []string{EventSession},
		Payloads: []widget.EventPayload{{Event: EventSession, Fields: []string{FieldCard}}},
	}
}

// Mount is never reached through the collection, whose members take their
// initial state from the host snapshot; it returns the empty card for a
// host that registers the definition on its own.
func (card *sessionCard) Mount(ctx context.Context, session live.Session[live.AnonymousIdentity]) (SessionCard, []live.Effect[live.AnonymousIdentity], error) {
	return SessionCard{}, nil, nil
}

// Reduce replaces the card with the one a session event carries, keeping
// the browser's own choice, or flips that choice on an expand event. A card
// that does not decode leaves the state as it was: the reducer is pure and
// reports nothing, and the follow effect that emitted it is the place that
// would.
func (card *sessionCard) Reduce(state SessionCard, event live.Event) (SessionCard, []live.Effect[live.AnonymousIdentity]) {
	switch event.Name {
	case EventExpand:
		state.Expanded = !state.Expanded
		return state, nil
	case EventSession:
		var next SessionCard
		if json.Unmarshal([]byte(event.Fields.Get(FieldCard)), &next) != nil {
			return state, nil
		}
		next.Expanded = state.Expanded
		return next, nil
	}
	return state, nil
}

func (card *sessionCard) Render(state SessionCard) templ.Component {
	return renderCard(card.region, state)
}

func (card *sessionCard) Unmount(ctx context.Context, session live.Session[live.AnonymousIdentity], state SessionCard) {
}

func (card *sessionCard) Snapshot(state SessionCard) widget.Snapshot {
	return widget.Snapshot{Widget: WidgetName, Fields: []widget.SnapshotField{
		{Name: "assignment", Value: state.Assignment},
		{Name: "agent", Value: state.Agent},
		{Name: "branch", Value: state.Branch},
		{Name: "status", Value: string(state.Status)},
		{Name: "model", Value: state.Model},
		{Name: "turns", Value: strconv.Itoa(state.Turns)},
		{Name: "tool_calls", Value: strconv.Itoa(state.ToolCalls)},
		{Name: "gate_denials", Value: strconv.Itoa(state.GateDenials)},
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
