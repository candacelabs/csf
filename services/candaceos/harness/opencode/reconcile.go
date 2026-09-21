package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
)

// poll drives reconciliation until the session ends: on the configured
// interval, and immediately whenever something invalidates the runtime's view
// of the session. It is started by Activate and awaited by Close.
func (h *harnessRuntime) poll(ctx context.Context) {
	defer h.watchers.Done()
	ticker := time.NewTicker(pollInterval(h.config))
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-h.invalidations:
		}
		h.reconcile(ctx)
	}
}

// watchEvents collapses the provider's server-sent event stream into
// reconciliation wakeups. The stream is an optimization, never a source of
// truth: every event it delivers only schedules another transcript read, so a
// dropped stream degrades to interval polling. It is started by Activate and
// awaited by Close.
func (h *harnessRuntime) watchEvents(ctx context.Context, sessionID string) {
	defer h.watchers.Done()
	for ctx.Err() == nil {
		_ = h.sdk.streamEvents(ctx, func(event json.RawMessage) {
			if eventAppliesToSession(event, sessionID) {
				h.invalidate()
			}
		})
		if !wait(ctx, pollInterval(h.config)) {
			return
		}
	}
}

// eventAppliesToSession decides whether one raw provider event is worth a
// reconciliation. It reads only the envelope and the session identifiers, so a
// provider event variant this SDK version cannot model still schedules a read
// instead of tearing down the stream.
func eventAppliesToSession(event json.RawMessage, sessionID string) bool {
	var envelope struct {
		Type       string          `json:"type"`
		Properties json.RawMessage `json:"properties"`
	}
	if json.Unmarshal(event, &envelope) != nil {
		return false
	}
	if strings.HasPrefix(envelope.Type, serverEventPrefix) {
		return true
	}
	var properties struct {
		SessionID string `json:"sessionID"`
		Info      struct {
			SessionID string `json:"sessionID"`
		} `json:"info"`
		Part struct {
			SessionID string `json:"sessionID"`
		} `json:"part"`
	}
	if len(envelope.Properties) == 0 || json.Unmarshal(envelope.Properties, &properties) != nil {
		return envelope.Type == sessionErrorEvent
	}
	return properties.SessionID == sessionID ||
		properties.Info.SessionID == sessionID ||
		properties.Part.SessionID == sessionID
}

// reconcile runs one reconciliation on the command goroutine so projection
// stays ordered against admission. The provider reads inside it are bounded by
// the configured request timeout, which is what keeps an unhealthy OpenCode
// from monopolizing the runtime.
func (h *harnessRuntime) reconcile(ctx context.Context) {
	h.submit(ctx, h.shutdown.Done(), func(state *sessionState) bool {
		requestCtx, cancel := context.WithTimeout(ctx, requestTimeout(h.config))
		defer cancel()
		_ = h.reconcileState(state, requestCtx)
		return false
	})
}

// reconcileState projects one coherent transcript snapshot and applies the
// session phase it was read with.
//
// The order is deliberate. Events are published first and each is recorded only
// once the host accepts it, so a rejected publication is retried on the next
// pass. The phase is applied to a copy of the turn state and committed only
// after any idle transition it implies has been published, so the host can
// never see idle before the terminal event it concludes. A returned error means
// the snapshot was not fully applied; the next pass reruns it.
func (h *harnessRuntime) reconcileState(state *sessionState, ctx context.Context) error {
	if !state.attached() {
		return ErrSessionUnavailable
	}
	messages, phase, coherent, err := h.sdk.rehydrate(ctx, state.sessionID)
	if err != nil {
		return err
	}
	if !coherent {
		h.invalidate()
		return nil
	}

	projected := state.projected.clone()
	events, completed := projectMessages(&projected, state.turn.runs, messages)
	if err := h.publishProjected(state, ctx, events); err != nil {
		return err
	}
	state.projected = projected

	discarded := discardQueuedAfterTerminalError(state, events)
	turn := state.turn.clone()
	next, idle := applyPhase(&turn, phase, completed)
	if idle.emit && !discarded {
		if err := h.publishIdle(state, &turn, ctx, idle); err != nil {
			return fmt.Errorf("opencode: publishing idle transition: %w", err)
		}
	}
	state.turn = turn
	if next != nil {
		h.submitQueued(state, ctx, *next)
	}
	return nil
}

// publishProjected delivers the events that belong to the active turn and
// records every event, published or fenced out, against the authoritative
// projection state. It stops at the first rejection so nothing after it is
// recorded as delivered.
func (h *harnessRuntime) publishProjected(
	state *sessionState,
	ctx context.Context,
	events []projectedEvent,
) error {
	for _, event := range events {
		if !state.fencesActiveTurn(event) {
			event.recordInto(&state.projected)
			continue
		}
		if err := h.host.Publish(ctx, event.event); err != nil {
			return fmt.Errorf("opencode: publishing projected event %q: %w", event.event.GetId(), err)
		}
		event.recordInto(&state.projected)
	}
	return nil
}

// discardQueuedAfterTerminalError drops queued guidance once the turn it was
// queued behind failed, and reports whether it did. Queued follow-ups assume
// the work before them succeeded, so running them after a failure would apply
// guidance to a state that never happened.
func discardQueuedAfterTerminalError(state *sessionState, events []projectedEvent) bool {
	for _, event := range events {
		if _, terminal := event.event.GetPayload().(*candaceosv1.HarnessEvent_Error); !terminal {
			continue
		}
		epoch, correlated := state.turn.epochs[event.parentMessageID]
		if correlated && epoch == state.turn.epoch {
			state.turn.pending = nil
			return true
		}
	}
	return false
}

// idleTransition describes the turn boundary a phase change implies.
type idleTransition struct {
	// emit reports that an idle event concluding runID must be published.
	emit  bool
	runID string
	// epoch is the turn epoch the transition was computed against. Publication
	// is skipped if the turn has moved on.
	epoch uint64
}

// applyPhase folds one observed session phase into turn. It reports the queued
// prompt promoted to the active turn, if any, and the idle transition the phase
// implies. It mutates only turn, so the caller can discard the result when the
// idle event it depends on cannot be published.
func applyPhase(
	turn *turnState,
	phase sessionPhase,
	completed map[string]struct{},
) (*queuedPrompt, idleTransition) {
	switch phase {
	case phaseBusy, phaseRetry:
		turn.busy = true
		turn.lastPhase = phase
		return nil, idleTransition{}
	case phaseIdle:
		return applyIdlePhase(turn, completed)
	default:
		return nil, idleTransition{}
	}
}

func applyIdlePhase(turn *turnState, completed map[string]struct{}) (*queuedPrompt, idleTransition) {
	// Active-turn steering passes through idle between the abort and the
	// replacement submission. That is not a turn boundary.
	if turn.interrupting {
		return nil, idleTransition{}
	}
	// An idle phase read alongside a transcript that does not yet carry the
	// active turn's reply is a stale pairing; wait for the reply.
	if turn.active != nil && !turn.awaitingAbort {
		if _, replied := completed[turn.active.messageID]; !replied {
			return nil, idleTransition{}
		}
	}
	wasBusy := turn.busy || turn.lastPhase.active()
	concluded := concludedRunID(turn)
	turn.awaitingAbort = false
	turn.abortRunID = ""
	turn.active = nil
	turn.busy = false
	turn.lastPhase = phaseIdle
	if len(turn.pending) != 0 {
		next := turn.pending[0]
		turn.pending = turn.pending[1:]
		turn.begin(next)
		// A drained queue keeps the session busy, so no idle is published
		// between queued turns.
		return &next, idleTransition{}
	}
	return nil, idleTransition{
		emit:  wasBusy && concluded != "",
		runID: concluded,
		epoch: turn.epoch,
	}
}

func concludedRunID(turn *turnState) string {
	if turn.active != nil {
		return turn.active.runID
	}
	if turn.awaitingAbort {
		return turn.abortRunID
	}
	return ""
}

// publishIdle publishes the idle event that concludes one turn. The guard
// re-checks the turn the transition was computed against, so an idle event is
// never published for a turn that has already been superseded.
func (h *harnessRuntime) publishIdle(
	state *sessionState,
	turn *turnState,
	ctx context.Context,
	idle idleTransition,
) error {
	if turn.epoch != idle.epoch || turn.busy || turn.active != nil || !state.attached() {
		return ErrSessionUnavailable
	}
	return h.host.Publish(ctx, &candaceosv1.HarnessEvent{
		Id:      "opencode-idle-" + uuid.NewString(),
		RunId:   idle.runID,
		At:      timestamppb.Now(),
		Payload: &candaceosv1.HarnessEvent_Idle{Idle: &candaceosv1.HarnessIdle{}},
	})
}
