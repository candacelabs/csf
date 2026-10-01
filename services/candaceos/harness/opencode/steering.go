package opencode

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/types/known/timestamppb"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
)

// send admits one prompt on the command goroutine. An idle session submits
// directly; a busy session either queues the prompt or steers the active turn,
// according to its delivery mode.
func (h *harnessRuntime) send(
	state *sessionState,
	ctx context.Context,
	prompt *candaceosv1.HarnessPrompt,
) error {
	if !state.attached() {
		return ErrSessionUnavailable
	}
	request := queuedPrompt{
		runID:     prompt.GetRunId(),
		content:   prompt.GetContent(),
		messageID: newMessageID(),
	}
	ctx, cancel := operationContext(ctx, state.lifecycle)
	defer cancel()
	if !state.turn.busy {
		return h.submitNow(state, ctx, request)
	}
	if prompt.GetDelivery() == candaceosv1.HarnessDelivery_HARNESS_DELIVERY_ENQUEUE {
		return h.enqueue(state, request)
	}
	return h.steerActiveTurn(state, ctx, request)
}

// submitNow opens a turn for request and submits it. The turn is rolled back if
// the provider refuses it, so the session does not appear busy with a prompt
// that never arrived.
func (h *harnessRuntime) submitNow(
	state *sessionState,
	ctx context.Context,
	request queuedPrompt,
) error {
	state.turn.begin(request)
	if err := h.submitPrompt(ctx, state.sessionID, request); err != nil {
		state.turn.rollback(request.messageID)
		return err
	}
	// The user event is best-effort here: the provider already accepted the
	// prompt, so a rejected publication is retried from the transcript rather
	// than failing an admitted turn.
	_ = h.acceptUser(state, request)
	h.invalidate()
	return nil
}

// enqueue admits a follow-up behind the active turn. The prompt is published to
// the host before it is queued, so a prompt the host rejects is reported to the
// caller and leaves no trace instead of surfacing later with no user event.
func (h *harnessRuntime) enqueue(state *sessionState, request queuedPrompt) error {
	if len(state.turn.pending) >= queueCapacity(h.config) {
		return ErrQueueFull
	}
	if err := h.acceptUser(state, request); err != nil {
		return fmt.Errorf("opencode: publishing queued prompt: %w", err)
	}
	state.turn.pending = append(state.turn.pending, request)
	h.invalidate()
	return nil
}

// steerActiveTurn implements active-turn steering: abort what the provider is
// doing, mark that abort as operator-intentional so its provider error is
// suppressed, and submit the replacement in the same command. A failed abort
// leaves the original turn untouched.
func (h *harnessRuntime) steerActiveTurn(
	state *sessionState,
	ctx context.Context,
	request queuedPrompt,
) error {
	state.turn.interrupting = true
	interrupted := state.markIntentionalAbort()
	if err := h.sdk.abort(ctx, state.sessionID); err != nil {
		state.turn.interrupting = false
		state.unmarkIntentionalAbort(interrupted)
		return fmt.Errorf("opencode: interrupting active turn: %w", err)
	}
	state.turn.begin(request)
	state.turn.interrupting = false
	if err := h.submitPrompt(ctx, state.sessionID, request); err != nil {
		state.turn.rollback(request.messageID)
		h.publishSteeringFailure(ctx, request, err)
		return err
	}
	_ = h.acceptUser(state, request)
	h.invalidate()
	return nil
}

// publishSteeringFailure reports a replacement prompt the provider refused
// after its predecessor was already aborted, so the operator sees why the turn
// ended instead of a silent stop. The publication is best-effort: the caller
// already receives the error.
func (h *harnessRuntime) publishSteeringFailure(
	ctx context.Context,
	request queuedPrompt,
	cause error,
) {
	_ = h.host.Publish(ctx, &candaceosv1.HarnessEvent{
		Id:    "opencode-submit-error-" + request.messageID,
		RunId: request.runID,
		At:    timestamppb.Now(),
		Payload: &candaceosv1.HarnessEvent_Error{Error: &candaceosv1.HarnessError{
			Message: "submitting replacement OpenCode prompt: " + cause.Error(),
		}},
	})
}

// abort stops the active turn and drops queued guidance. The queue is restored
// if the provider refuses the abort, so a failed stop is a no-op.
func (h *harnessRuntime) abort(state *sessionState, ctx context.Context) error {
	if !state.attached() {
		return ErrSessionUnavailable
	}
	ctx, cancel := operationContext(ctx, state.lifecycle)
	defer cancel()
	discarded := state.turn.pending
	state.turn.pending = nil
	state.turn.awaitingAbort = true
	state.turn.abortRunID = abortedRunID(state.turn, discarded)
	interrupted := state.markIntentionalAbort()
	if err := h.sdk.abort(ctx, state.sessionID); err != nil {
		state.turn.awaitingAbort = false
		state.turn.abortRunID = ""
		state.unmarkIntentionalAbort(interrupted)
		state.turn.pending = discarded
		return fmt.Errorf("opencode: aborting turn: %w", err)
	}
	// The provider reports idle asynchronously; hold the session busy until
	// reconciliation observes that transition and publishes the fenced idle.
	state.turn.busy = true
	state.turn.lastPhase = phaseBusy
	h.invalidate()
	return nil
}

// abortedRunID picks the run the idle event concluding this abort belongs to:
// the active turn's run, or the head of the discarded queue when an abort
// arrives before the queued guidance ever started.
func abortedRunID(turn turnState, discarded []queuedPrompt) string {
	if turn.active != nil {
		return turn.active.runID
	}
	if len(discarded) != 0 {
		return discarded[0].runID
	}
	return ""
}

// submitQueued submits the follow-up that reconciliation just promoted to the
// active turn. A refusal returns the prompt to the head of the queue so FIFO
// order survives, unless the session is already ending.
func (h *harnessRuntime) submitQueued(state *sessionState, ctx context.Context, prompt queuedPrompt) {
	if state.turn.active == nil || state.turn.active.messageID != prompt.messageID {
		return
	}
	if err := h.submitPrompt(ctx, state.sessionID, prompt); err != nil {
		if state.live() {
			state.turn.pending = append([]queuedPrompt{prompt}, state.turn.pending...)
			state.turn.active = nil
			state.turn.busy = false
			state.turn.lastPhase = phaseIdle
		}
		return
	}
	h.invalidate()
}

func (h *harnessRuntime) submitPrompt(ctx context.Context, sessionID string, prompt queuedPrompt) error {
	err := h.sdk.promptAsync(ctx, sessionID, prompt.messageID, prompt.content, systemInstructions, h.model)
	if err != nil {
		return fmt.Errorf("opencode: submitting prompt: %w", err)
	}
	return nil
}

// acceptUser publishes the operator's own message exactly once. It publishes on
// the session lifecycle rather than the submitting request's context: the
// provider has already accepted the prompt, so the record of it must survive
// the request that delivered it. A rejected publication is unmarked, so the
// transcript projection republishes it once the host recovers.
func (h *harnessRuntime) acceptUser(state *sessionState, prompt queuedPrompt) error {
	if _, published := state.projected.seenUsers[prompt.messageID]; published {
		return nil
	}
	if !state.live() {
		return ErrSessionUnavailable
	}
	state.projected.seenUsers[prompt.messageID] = struct{}{}
	err := h.host.Publish(state.lifecycle, &candaceosv1.HarnessEvent{
		Id:    "opencode-user-" + prompt.messageID,
		RunId: prompt.runID,
		At:    timestamppb.Now(),
		Payload: &candaceosv1.HarnessEvent_UserMessage{
			UserMessage: &candaceosv1.HarnessUserMessage{Content: prompt.content},
		},
	})
	if err != nil {
		delete(state.projected.seenUsers, prompt.messageID)
	}
	return err
}
