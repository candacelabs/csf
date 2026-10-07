// Copyright 2026 Candace Labs

package opsview

import (
	"context"
	"encoding/json"

	"github.com/candacelabs/csf/pkg/gotth/live"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
)

// The sessions tile on the wire. Both events are internal: the follow effect
// asks for a count whenever a session's status moves, and the count effect
// delivers what the session service reports.
const (
	// EventRefreshSessions asks the session service for its sessions again.
	EventRefreshSessions = "opsview.refresh_sessions"
	// EventSessionCounts carries the counts, as JSON in FieldCounts.
	EventSessionCounts = "opsview.session_counts"
	FieldCounts        = "counts"
	// EventLoaded marks the follow effect's first pass done. Internal.
	EventLoaded = "opsview.loaded"

	sourceSessions = "opsview.sessions"
)

// SessionCounts is the host's sessions, of both kinds. A virtual_session is
// one harness session (one assignment) as the session service holds it, by
// phase. A real_session is the executor process a virtual session runs its
// turns on: one is alive for every virtual session that is starting,
// running, open or canceling and not suspended. A suspended session closed
// its executor while idle and opens a new one on its next message.
type SessionCounts struct {
	Present  bool `json:"present"`
	Virtual  int  `json:"virtual"`
	Running  int  `json:"running"`
	Open     int  `json:"open"`
	Starting int  `json:"starting"`
	Canceled int  `json:"canceled"`
	Failed   int  `json:"failed"`
	Closed   int  `json:"closed"`
	// Suspended counts the open virtual sessions whose real session is
	// closed until their next message.
	Suspended int `json:"suspended"`
	Real      int `json:"real"`
}

// phaseCounters are the phases a count adds up, and whether a session in the
// phase keeps a real_session alive.
var phaseCounters = map[harnessv1.AgentSessionPhase]struct {
	into  func(counts *SessionCounts) *int
	alive bool
}{
	harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_STARTING:  {func(counts *SessionCounts) *int { return &counts.Starting }, true},
	harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_RUNNING:   {func(counts *SessionCounts) *int { return &counts.Running }, true},
	harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_OPEN:      {func(counts *SessionCounts) *int { return &counts.Open }, true},
	harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CANCELING: {func(counts *SessionCounts) *int { return &counts.Canceled }, true},
	harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CANCELED:  {func(counts *SessionCounts) *int { return &counts.Canceled }, false},
	harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_FAILED:    {func(counts *SessionCounts) *int { return &counts.Failed }, false},
	harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CLOSED:    {func(counts *SessionCounts) *int { return &counts.Closed }, false},
}

// CountSessions counts the session service's sessions by phase, and the real
// sessions they keep alive.
func CountSessions(sessions []*harnessv1.AgentSessionState) SessionCounts {
	counts := SessionCounts{Present: true, Virtual: len(sessions)}
	for _, state := range sessions {
		counter, known := phaseCounters[state.GetPhase()]
		if !known {
			continue
		}
		*counter.into(&counts)++
		switch {
		case state.GetSuspended():
			counts.Suspended++
		case counter.alive:
			counts.Real++
		}
	}
	return counts
}

// readSessions is the effect that asks the session service for its sessions
// and delivers their counts. A list the service refuses is logged and leaves
// the tile as it was.
func (view *OpsView) readSessions() live.Effect[ViewerIdentity] {
	return live.Effect[ViewerIdentity]{Source: sourceSessions, Run: func(ctx context.Context, _ live.Session[ViewerIdentity], emit live.Emitter) error {
		response, err := view.operations.ListAgentSessions(ctx, &harnessv1.ListAgentSessionsRequest{})
		if err != nil {
			view.logger.Warn("ops view: sessions not listed", "error", err)
			return nil
		}
		encoded, err := json.Marshal(CountSessions(response.GetSessions()))
		if err != nil {
			return err
		}
		return emit(live.Event{Name: EventSessionCounts, FragmentID: SummaryRegion, Fields: live.NewFields(map[string]string{FieldCounts: string(encoded)})})
	}}
}

// reduceSessions is the sessions tile's transition.
func (view *OpsView) reduceSessions(state viewState, event live.Event) (viewState, []live.Effect[ViewerIdentity]) {
	switch event.Name {
	case EventRefreshSessions:
		if view.operations != nil {
			return state, []live.Effect[ViewerIdentity]{view.readSessions()}
		}
	case EventSessionCounts:
		var counts SessionCounts
		if json.Unmarshal([]byte(event.Fields.Get(FieldCounts)), &counts) == nil {
			state.sessions = counts
		}
	}
	return state, nil
}
