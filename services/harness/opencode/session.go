package opencode

import (
	"context"
	"maps"
	"strings"

	"github.com/google/uuid"
	opencodesdk "github.com/sst/opencode-sdk-go"
)

// queuedPrompt is one admitted unit of guidance. messageID is minted by the
// runtime before submission so the provider transcript can be correlated back
// to the run that produced it.
type queuedPrompt struct {
	runID     string
	content   string
	messageID string
}

// newMessageID mints a provider-shaped message identifier for a prompt the
// runtime is about to submit.
func newMessageID() string {
	return "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")
}

// sessionState is the runtime's entire mutable state. It is owned exclusively
// by the command goroutine: every field is read and written there and nowhere
// else, which is why nothing in it is synchronized.
type sessionState struct {
	// lifecycle is canceled when the session ends, either by Close or by the
	// context Core supplied to Start. It is nil until Start succeeds and is the
	// parent of every provider call and every host publication that must
	// outlive the request that triggered it.
	lifecycle       context.Context
	cancelLifecycle context.CancelFunc
	activated       bool
	sessionID       string
	version         string

	turn      turnState
	projected projectionState
}

// attached reports whether a live session is attached and activated, which is
// the precondition for every operation that touches the provider.
func (state *sessionState) attached() bool {
	return state.activated && state.live()
}

// live reports whether a session is attached and its lifecycle is not canceled.
func (state *sessionState) live() bool {
	return state.lifecycle != nil && state.lifecycle.Err() == nil
}

// turnState is the admission and turn-fencing half of the session. Its clone is
// mutated speculatively during reconciliation and committed only once every
// event it depends on has been published.
type turnState struct {
	busy   bool
	active *queuedPrompt
	// awaitingAbort marks an operator abort whose idle transition has not been
	// observed yet; abortRunID keeps that run's fence so the idle event still
	// carries the run it concluded.
	awaitingAbort bool
	abortRunID    string
	// interrupting suppresses idle while active-turn steering is between the
	// abort and the replacement submission.
	interrupting bool
	// pending is the bounded FIFO queue. It exists because the unbuffered
	// mailbox retains nothing after Send replies, so the command goroutine must
	// hold admitted follow-ups itself.
	pending   []queuedPrompt
	lastPhase sessionPhase
	// epoch increases with every submitted prompt. Together with epochs it is
	// the fence that keeps a late provider message from being attributed to a
	// newer turn.
	epoch  uint64
	epochs map[string]uint64
	// runs maps a provider message ID to the run ID that produced it.
	runs map[string]string
}

func newTurnState() turnState {
	return turnState{epochs: make(map[string]uint64), runs: make(map[string]string)}
}

func (turn turnState) clone() turnState {
	turn.pending = append([]queuedPrompt(nil), turn.pending...)
	turn.epochs = maps.Clone(turn.epochs)
	turn.runs = maps.Clone(turn.runs)
	return turn
}

// begin marks prompt as the active turn and opens a new epoch for it.
func (turn *turnState) begin(prompt queuedPrompt) {
	turn.epoch++
	turn.epochs[prompt.messageID] = turn.epoch
	turn.runs[prompt.messageID] = prompt.runID
	turn.active = &prompt
	turn.busy = true
	turn.lastPhase = phaseBusy
}

// rollback undoes begin for a prompt the provider refused, reporting the
// session idle again so the next prompt is submitted directly.
func (turn *turnState) rollback(messageID string) {
	if turn.active == nil || turn.active.messageID != messageID {
		return
	}
	turn.active = nil
	turn.busy = false
	turn.lastPhase = phaseIdle
}

// projectionState is the delivered-event half of the session: what has already
// been projected to the host, so re-reading the whole transcript on every poll
// produces each event exactly once.
type projectionState struct {
	seenUsers       map[string]struct{}
	assistantText   map[string]string
	finalAssistants map[string]struct{}
	toolStatus      map[string]opencodesdk.ToolPartStateStatus
	seenErrors      map[string]struct{}
	// intentionalAborts holds the parent message IDs whose provider "aborted"
	// error the operator caused, and whose error must therefore be suppressed
	// instead of surfaced as a turn failure.
	intentionalAborts map[string]struct{}
}

func newProjectionState() projectionState {
	return projectionState{
		seenUsers:         make(map[string]struct{}),
		assistantText:     make(map[string]string),
		finalAssistants:   make(map[string]struct{}),
		toolStatus:        make(map[string]opencodesdk.ToolPartStateStatus),
		seenErrors:        make(map[string]struct{}),
		intentionalAborts: make(map[string]struct{}),
	}
}

func (projected projectionState) clone() projectionState {
	projected.seenUsers = maps.Clone(projected.seenUsers)
	projected.assistantText = maps.Clone(projected.assistantText)
	projected.finalAssistants = maps.Clone(projected.finalAssistants)
	projected.toolStatus = maps.Clone(projected.toolStatus)
	projected.seenErrors = maps.Clone(projected.seenErrors)
	projected.intentionalAborts = maps.Clone(projected.intentionalAborts)
	return projected
}

// markIntentionalAbort records the active turn as operator-aborted and reports
// the message ID it marked, so a failed abort RPC can unmark it.
func (state *sessionState) markIntentionalAbort() string {
	if state.turn.active == nil {
		return ""
	}
	messageID := state.turn.active.messageID
	state.projected.intentionalAborts[messageID] = struct{}{}
	return messageID
}

func (state *sessionState) unmarkIntentionalAbort(messageID string) {
	if messageID == "" {
		return
	}
	delete(state.projected.intentionalAborts, messageID)
}

// fencesActiveTurn reports whether a projected event belongs to the turn that
// is active right now. Anything else is historical: it is recorded as seen but
// never published, so a stale provider message cannot be attributed to a newer
// run.
func (state *sessionState) fencesActiveTurn(event projectedEvent) bool {
	epoch, correlated := state.turn.epochs[event.parentMessageID]
	return correlated &&
		epoch == state.turn.epoch &&
		state.turn.active != nil &&
		state.turn.active.messageID == event.parentMessageID &&
		state.turn.active.runID == event.event.GetRunId() &&
		state.attached()
}
