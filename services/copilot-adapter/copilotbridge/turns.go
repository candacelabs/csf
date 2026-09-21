package copilotbridge

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"

	"github.com/google/uuid"

	copilotadapter "github.com/candacelabs/csf/services/copilot-adapter"
	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
)

// turnSender serializes acceptance and the SDK invocation. Each scheduling
// lane therefore preserves the order session.Send observes, even when two
// HTTP handlers arrive concurrently.
type turnSender struct {
	mutex  sync.Mutex
	turns  *turnCorrelator
	invoke func(ctx context.Context, prompt copilotadapter.BridgePrompt) error
}

func newTurnSender(turns *turnCorrelator, invoke func(ctx context.Context, prompt copilotadapter.BridgePrompt) error) *turnSender {
	return &turnSender{turns: turns, invoke: invoke}
}

func (sender *turnSender) send(ctx context.Context, prompt copilotadapter.BridgePrompt) (copilotadapter.BridgePromptDelivery, error) {
	sender.mutex.Lock()
	defer sender.mutex.Unlock()
	if delivery, found := sender.turns.delivery(prompt.TurnID); found {
		return delivery, nil
	}
	if sender.turns.hasUnknownDelivery() {
		return copilotadapter.BridgePromptDeliveryRejected,
			errors.New("copilot bridge: an earlier prompt delivery is unresolved")
	}
	enqueue := sender.turns.enqueue
	if prompt.Mode == string(api.Steer) {
		enqueue = sender.turns.enqueueImmediate
	}
	if !enqueue(prompt.TurnID) {
		return copilotadapter.BridgePromptDeliveryRejected, errors.New("copilot bridge: the session is closed")
	}
	if err := sender.invoke(ctx, prompt); err != nil {
		return copilotadapter.BridgePromptDeliveryUnknown, err
	}
	sender.turns.acceptDelivery(prompt.TurnID)
	return copilotadapter.BridgePromptDeliveryAccepted, nil
}

func (sender *turnSender) abort(
	ctx context.Context,
	expectedTurnID uuid.UUID,
	invoke func(ctx context.Context) error,
	waiter *turnTerminationWaiter,
) (uuid.UUID, error) {
	sender.mutex.Lock()
	defer sender.mutex.Unlock()
	target, started, err := sender.turns.beginAbort(expectedTurnID)
	if err != nil {
		return uuid.Nil, err
	}
	if !started {
		return uuid.Nil, copilotadapter.ErrNoActiveTurn
	}
	identifier, cancelTarget, err := waiter.wait(ctx, invoke)
	if err != nil {
		if cancelTarget {
			sender.turns.cancelAbort(target)
		}
		return uuid.Nil, err
	}
	if identifier != target {
		return uuid.Nil, errors.New("copilot bridge: abort event did not match its selected turn")
	}
	return identifier, nil
}

// turnTerminationWaiter pairs one session-global abort RPC with the exact turn
// selected by the subsequent SDK AbortData event. The correlator retains that
// target after any post-invocation ambiguity even when this waiter is gone.
type turnTerminationWaiter struct {
	mutex   sync.Mutex
	waiting chan uuid.UUID
}

func (waiter *turnTerminationWaiter) wait(ctx context.Context, invoke func(ctx context.Context) error) (uuid.UUID, bool, error) {
	// A context that is already done is the one outcome known not to have
	// crossed the SDK boundary: do not invoke, and release the selected target.
	// Once invocation starts, every error is ambiguous because the JSON-RPC
	// request may have been written before its response or context was lost.
	if err := ctx.Err(); err != nil {
		return uuid.Nil, true, err
	}
	result := make(chan uuid.UUID, 1)
	waiter.mutex.Lock()
	waiter.waiting = result
	waiter.mutex.Unlock()
	if err := invoke(ctx); err != nil {
		waiter.clear(result)
		select {
		case identifier := <-result:
			return identifier, false, nil
		default:
			return uuid.Nil, false, err
		}
	}
	select {
	case identifier := <-result:
		return identifier, false, nil
	case <-ctx.Done():
		waiter.clear(result)
		return uuid.Nil, false, ctx.Err()
	}
}

func (waiter *turnTerminationWaiter) publish(identifier uuid.UUID) {
	waiter.mutex.Lock()
	result := waiter.waiting
	waiter.waiting = nil
	waiter.mutex.Unlock()
	if result != nil {
		result <- identifier
	}
}

func (waiter *turnTerminationWaiter) clear(expected chan uuid.UUID) {
	waiter.mutex.Lock()
	if waiter.waiting == expected {
		waiter.waiting = nil
	}
	waiter.mutex.Unlock()
}

type turnCorrelationState struct {
	restored           []uuid.UUID
	immediate          []uuid.UUID
	pending            []uuid.UUID
	delivery           map[uuid.UUID]copilotadapter.BridgePromptDelivery
	prebound           map[string]uuid.UUID
	preboundOrder      []string
	bySDK              map[sdkTurnKey]uuid.UUID
	closedInteractions map[string]bool
	submittedAt        map[uuid.UUID]uint64
	nextSubmission     uint64
	nextAnonymous      uint64
	started            map[uuid.UUID]bool
	terminal           map[uuid.UUID]bool
	current            uuid.UUID
	currentInteraction string
	hasCurrent         bool
	activeSDK          *sdkTurnKey
	abortTarget        *uuid.UUID
}

type sdkTurnKey struct {
	interactionID string
	turnID        string
}

type turnTransition struct {
	completed   []uuid.UUID
	started     []uuid.UUID
	owner       uuid.UUID
	found       bool
	sessionBusy bool
}

const (
	anonymousInteractionPrefix  = "\xffcopilot-adapter-anonymous-"
	userMessageDeliveryIdle     = "idle"
	userMessageDeliveryQueued   = "queued"
	userMessageDeliverySteering = "steering"
)

// turnCorrelator owns accepted adapter prompts and the SDK iterations they
// produce. One SDK interaction is one agent loop, but that loop can contain
// several assistant.turn_start/turn_end pairs. Queue messages begin a distinct
// interaction; steering messages replace the durable owner inside the current
// interaction before subsequent output is projected.
type turnCorrelator struct {
	commands      chan func(state *turnCorrelationState)
	stopRequested chan struct{}
	stopped       chan struct{}
	stopOnce      sync.Once
}

func newTurnCorrelator() *turnCorrelator {
	correlator := &turnCorrelator{
		commands:      make(chan func(state *turnCorrelationState)),
		stopRequested: make(chan struct{}),
		stopped:       make(chan struct{}),
	}
	go correlator.loop()
	return correlator
}

func newRestoredTurnCorrelator(turns []copilotadapter.BridgeRestoredTurn) *turnCorrelator {
	correlator := newTurnCorrelator()
	for _, turn := range turns {
		switch {
		case turn.Status == api.TurnStatusRunning:
			correlator.enqueueRestored(turn.ID)
		case turn.Mode == api.Steer:
			correlator.enqueueImmediate(turn.ID)
		default:
			correlator.enqueue(turn.ID)
		}
		if !turn.DeliveryUnknown {
			correlator.acceptDelivery(turn.ID)
		}
	}
	return correlator
}

func (correlator *turnCorrelator) loop() {
	state := turnCorrelationState{
		delivery:           map[uuid.UUID]copilotadapter.BridgePromptDelivery{},
		prebound:           map[string]uuid.UUID{},
		bySDK:              map[sdkTurnKey]uuid.UUID{},
		closedInteractions: map[string]bool{},
		submittedAt:        map[uuid.UUID]uint64{},
		started:            map[uuid.UUID]bool{},
		terminal:           map[uuid.UUID]bool{},
	}
	defer close(correlator.stopped)
	for {
		select {
		case <-correlator.stopRequested:
			return
		case command := <-correlator.commands:
			command(&state)
		}
	}
}

func (correlator *turnCorrelator) run(command func(state *turnCorrelationState)) bool {
	done := make(chan struct{})
	wrapped := func(state *turnCorrelationState) {
		command(state)
		close(done)
	}
	select {
	case <-correlator.stopRequested:
		return false
	case correlator.commands <- wrapped:
	}
	select {
	case <-done:
		return true
	case <-correlator.stopped:
		return false
	}
}

func (correlator *turnCorrelator) enqueue(identifier uuid.UUID) bool {
	return correlator.run(func(state *turnCorrelationState) {
		state.pending = append(state.pending, identifier)
		recordSubmission(state, identifier)
		state.delivery[identifier] = copilotadapter.BridgePromptDeliveryUnknown
	})
}

func (correlator *turnCorrelator) enqueueRestored(identifier uuid.UUID) bool {
	return correlator.run(func(state *turnCorrelationState) {
		if !state.hasCurrent {
			state.current = identifier
			state.hasCurrent = true
			state.started[identifier] = true
		} else {
			state.restored = append(state.restored, identifier)
		}
		state.delivery[identifier] = copilotadapter.BridgePromptDeliveryUnknown
	})
}

func (correlator *turnCorrelator) enqueueImmediate(identifier uuid.UUID) bool {
	return correlator.run(func(state *turnCorrelationState) {
		state.immediate = append(state.immediate, identifier)
		recordSubmission(state, identifier)
		state.delivery[identifier] = copilotadapter.BridgePromptDeliveryUnknown
	})
}

func (correlator *turnCorrelator) acceptDelivery(identifier uuid.UUID) {
	correlator.run(func(state *turnCorrelationState) {
		if _, found := state.delivery[identifier]; found {
			state.delivery[identifier] = copilotadapter.BridgePromptDeliveryAccepted
		}
	})
}

// acknowledgeDelivery forgets the in-memory at-most-once tombstone only after
// the adapter confirms acceptance is durable. SDK terminal events deliberately
// do not remove it because they can race that database acknowledgement.
func (correlator *turnCorrelator) acknowledgeDelivery(identifier uuid.UUID) {
	correlator.run(func(state *turnCorrelationState) {
		delete(state.delivery, identifier)
	})
}

func (correlator *turnCorrelator) delivery(identifier uuid.UUID) (copilotadapter.BridgePromptDelivery, bool) {
	delivery := copilotadapter.BridgePromptDeliveryUnknown
	found := false
	correlator.run(func(state *turnCorrelationState) {
		delivery, found = state.delivery[identifier]
	})
	return delivery, found
}

func (correlator *turnCorrelator) hasUnknownDelivery() bool {
	found := false
	correlator.run(func(state *turnCorrelationState) {
		for _, delivery := range state.delivery {
			if delivery == copilotadapter.BridgePromptDeliveryUnknown {
				found = true
				return
			}
		}
	})
	return found
}

func (correlator *turnCorrelator) start(interactionID string, sdkTurnID string) turnTransition {
	transition := turnTransition{}
	correlator.run(func(state *turnCorrelationState) {
		hasExplicitInteraction := interactionID != ""
		if interactionID == "" {
			if state.hasCurrent && (!currentUsedSDKTurn(state, sdkTurnID) || len(state.preboundOrder) == 0) {
				interactionID = state.currentInteraction
			} else if inferred, found := firstPreboundInteraction(state); found {
				interactionID = inferred
			} else if state.hasCurrent {
				interactionID = state.currentInteraction
			}
		}
		if interactionID == "" {
			interactionID = nextAnonymousInteraction(state)
		}
		_, interactionPrebound := state.prebound[interactionID]
		if state.hasCurrent && state.currentInteraction == "" && !interactionPrebound {
			state.currentInteraction = interactionID
		}
		key := sdkTurnKey{interactionID: interactionID, turnID: sdkTurnID}
		if identifier, found := state.bySDK[key]; found {
			transition.owner, transition.found = identifier, true
			return
		}
		if state.closedInteractions[interactionID] {
			return
		}
		// A user-message boundary without an interaction id can become current
		// between SDK iterations. Bind that owner when its start supplies the id,
		// unless an exact prebind owns the id or a repeated SDK turn identifies
		// the oldest anonymous prebind as the next interaction boundary.
		if hasExplicitInteraction && state.hasCurrent &&
			isAnonymousInteraction(state.currentInteraction) && !interactionPrebound &&
			!(currentUsedSDKTurn(state, sdkTurnID) && hasAnonymousPrebind(state)) {
			state.currentInteraction = interactionID
		}

		if state.hasCurrent && interactionID != state.currentInteraction {
			identifier, found := takeNextTurn(state, interactionID)
			if !found {
				transition.sessionBusy = hasSessionWork(state)
				return
			}
			completeCurrent(state, &transition, true)
			startCurrent(state, &transition, identifier, interactionID)
		} else if !state.hasCurrent {
			identifier, found := takeNextTurn(state, interactionID)
			if !found {
				transition.sessionBusy = hasSessionWork(state)
				return
			}
			startCurrent(state, &transition, identifier, interactionID)
		}

		state.bySDK[key] = state.current
		state.activeSDK = &key
		transition.owner, transition.found = state.current, true
		transition.sessionBusy = hasSessionWork(state)
	})
	return transition
}

// end closes one SDK model-call iteration, not the durable prompt. The SDK can
// immediately start another iteration with the same interaction after tools.
func (correlator *turnCorrelator) end(sdkTurnID string) {
	correlator.run(func(state *turnCorrelationState) {
		if state.activeSDK != nil && state.activeSDK.turnID == sdkTurnID {
			state.activeSDK = nil
		}
	})
}

func (correlator *turnCorrelator) observeUserMessage(interactionID string, delivery string) turnTransition {
	transition := turnTransition{}
	correlator.run(func(state *turnCorrelationState) {
		if interactionID != "" && state.closedInteractions[interactionID] {
			return
		}
		switch delivery {
		case userMessageDeliverySteering:
			if len(state.immediate) == 0 {
				return
			}
			// InteractionID is optional in the SDK payload. Steering still
			// identifies the current execution and may not be followed by a new
			// turn_start, so inherit the active interaction before rebinding it.
			if interactionID == "" && state.hasCurrent {
				interactionID = state.currentInteraction
			}
			identifier := popSubmittedTurn(state, &state.immediate)
			if !state.hasCurrent {
				startCurrent(state, &transition, identifier, interactionID)
				transition.sessionBusy = hasSessionWork(state)
				return
			}
			completeCurrent(state, &transition, false)
			startCurrent(state, &transition, identifier, interactionID)
			if state.activeSDK != nil {
				state.bySDK[*state.activeSDK] = identifier
			}
		case userMessageDeliveryIdle, userMessageDeliveryQueued:
			if interactionID == "" {
				// InteractionID is optional even for the user-message boundary that
				// begins a queued loop. Give that boundary a private ordered identity;
				// otherwise the following interaction-less turn_start inherits the
				// previous loop and strands this durable prompt forever. The prefix is
				// invalid UTF-8, so it cannot collide with an SDK JSON string.
				interactionID = nextAnonymousInteraction(state)
			}
			if state.hasCurrent && state.currentInteraction == interactionID {
				return
			}
			if _, found := state.prebound[interactionID]; found {
				return
			}
			identifier, found := takeSubmittedTurn(state)
			if found {
				if state.hasCurrent && state.activeSDK == nil {
					completeCurrent(state, &transition, true)
					startCurrent(state, &transition, identifier, interactionID)
				} else {
					prebindInteraction(state, interactionID, identifier)
				}
			}
		}
		transition.sessionBusy = hasSessionWork(state)
	})
	return transition
}

func firstPreboundInteraction(state *turnCorrelationState) (string, bool) {
	if len(state.preboundOrder) == 0 {
		return "", false
	}
	return state.preboundOrder[0], true
}

func (correlator *turnCorrelator) idle() turnTransition {
	transition := turnTransition{}
	correlator.run(func(state *turnCorrelationState) {
		completeCurrent(state, &transition, true)
		state.activeSDK = nil
		transition.sessionBusy = hasSessionWork(state)
		if !transition.sessionBusy {
			// The pinned SDK dispatches one FIFO event stream and emits root idle
			// only after all agent and shell work. At that observed no-work barrier,
			// replay filtering owns late-event rejection and lifecycle correlation
			// history can be compacted. A raced queued prompt retains the history
			// until a later root idle actually observes no work.
			state.bySDK = map[sdkTurnKey]uuid.UUID{}
			state.closedInteractions = map[string]bool{}
			state.started = map[uuid.UUID]bool{}
			state.terminal = map[uuid.UUID]bool{}
		}
	})
	return transition
}

func popTurn(turns *[]uuid.UUID) uuid.UUID {
	identifier := (*turns)[0]
	*turns = (*turns)[1:]
	return identifier
}

func recordSubmission(state *turnCorrelationState, identifier uuid.UUID) {
	state.nextSubmission++
	state.submittedAt[identifier] = state.nextSubmission
}

func nextAnonymousInteraction(state *turnCorrelationState) string {
	state.nextAnonymous++
	return anonymousInteractionPrefix + strconv.FormatUint(state.nextAnonymous, 10)
}

func prebindInteraction(state *turnCorrelationState, interactionID string, identifier uuid.UUID) {
	state.prebound[interactionID] = identifier
	state.preboundOrder = append(state.preboundOrder, interactionID)
}

func isAnonymousInteraction(interactionID string) bool {
	return strings.HasPrefix(interactionID, anonymousInteractionPrefix)
}

func hasAnonymousPrebind(state *turnCorrelationState) bool {
	for _, interactionID := range state.preboundOrder {
		if isAnonymousInteraction(interactionID) {
			return true
		}
	}
	return false
}

func currentUsedSDKTurn(state *turnCorrelationState, sdkTurnID string) bool {
	if !state.hasCurrent {
		return false
	}
	for key, identifier := range state.bySDK {
		if key.turnID == sdkTurnID && identifier == state.current {
			return true
		}
	}
	return false
}

func popSubmittedTurn(state *turnCorrelationState, turns *[]uuid.UUID) uuid.UUID {
	identifier := popTurn(turns)
	delete(state.submittedAt, identifier)
	return identifier
}

func takeSubmittedTurn(state *turnCorrelationState) (uuid.UUID, bool) {
	if len(state.immediate) == 0 && len(state.pending) == 0 {
		return uuid.Nil, false
	}
	if len(state.immediate) == 0 {
		return popSubmittedTurn(state, &state.pending), true
	}
	if len(state.pending) == 0 {
		return popSubmittedTurn(state, &state.immediate), true
	}
	if state.submittedAt[state.pending[0]] < state.submittedAt[state.immediate[0]] {
		return popSubmittedTurn(state, &state.pending), true
	}
	return popSubmittedTurn(state, &state.immediate), true
}

func takeNextTurn(state *turnCorrelationState, interactionID string) (uuid.UUID, bool) {
	if len(state.restored) > 0 {
		return popTurn(&state.restored), true
	}
	if identifier, found := takePreboundTurn(state, interactionID); found {
		return identifier, true
	}
	if interactionID != "" && !isAnonymousInteraction(interactionID) {
		for _, candidate := range state.preboundOrder {
			if isAnonymousInteraction(candidate) {
				return takePreboundTurn(state, candidate)
			}
		}
	}
	return takeSubmittedTurn(state)
}

func takePreboundTurn(state *turnCorrelationState, interactionID string) (uuid.UUID, bool) {
	identifier, found := state.prebound[interactionID]
	if !found {
		return uuid.Nil, false
	}
	delete(state.prebound, interactionID)
	for index, candidate := range state.preboundOrder {
		if candidate == interactionID {
			state.preboundOrder = append(state.preboundOrder[:index], state.preboundOrder[index+1:]...)
			break
		}
	}
	return identifier, true
}

func completeCurrent(state *turnCorrelationState, transition *turnTransition, closeInteraction bool) {
	if !state.hasCurrent {
		return
	}
	identifier := state.current
	if closeInteraction && state.currentInteraction != "" {
		state.closedInteractions[state.currentInteraction] = true
	}
	// An AbortData event is authoritative only while its retained target is
	// still the foreground owner. A distinct owner handoff or root idle is
	// ordinary terminal SDK evidence: complete normally and release an
	// ambiguous abort so it cannot strand this turn or block later aborts.
	if state.abortTarget != nil && *state.abortTarget == identifier {
		state.abortTarget = nil
	}
	if !state.terminal[identifier] {
		state.terminal[identifier] = true
		transition.completed = append(transition.completed, identifier)
	}
	state.current = uuid.Nil
	state.currentInteraction = ""
	state.hasCurrent = false
}

func startCurrent(state *turnCorrelationState, transition *turnTransition, identifier uuid.UUID, interactionID string) {
	state.current = identifier
	state.currentInteraction = interactionID
	state.hasCurrent = true
	if _, tracked := state.delivery[identifier]; tracked {
		state.delivery[identifier] = copilotadapter.BridgePromptDeliveryAccepted
	}
	if !state.started[identifier] {
		state.started[identifier] = true
		transition.started = append(transition.started, identifier)
	}
}

func hasSessionWork(state *turnCorrelationState) bool {
	return state.hasCurrent || len(state.restored) > 0 || len(state.immediate) > 0 ||
		len(state.pending) > 0 || len(state.prebound) > 0
}

func (correlator *turnCorrelator) beginAbort(expectedTurnID uuid.UUID) (uuid.UUID, bool, error) {
	var identifier uuid.UUID
	found := false
	var err error
	correlator.run(func(state *turnCorrelationState) {
		if state.abortTarget != nil {
			err = copilotadapter.ErrAbortPending
			return
		}
		if !state.hasCurrent {
			return
		}
		identifier, found = state.current, true
		if identifier != expectedTurnID {
			err = copilotadapter.ErrAbortTargetMismatch
			found = false
			return
		}
		target := identifier
		state.abortTarget = &target
	})
	return identifier, found, err
}

func (correlator *turnCorrelator) cancelAbort(identifier uuid.UUID) {
	correlator.run(func(state *turnCorrelationState) {
		if state.abortTarget != nil && *state.abortTarget == identifier {
			state.abortTarget = nil
		}
	})
}

// terminateAborted removes the turn selected when the abort RPC began. The
// target survives caller cancellation until either AbortData arrives or
// ordinary terminal evidence completes that owner. It can therefore never
// land on a newer foreground turn.
func (correlator *turnCorrelator) terminateAborted() (uuid.UUID, bool, bool) {
	var identifier uuid.UUID
	found, sessionBusy := false, false
	correlator.run(func(state *turnCorrelationState) {
		if state.abortTarget == nil {
			return
		}
		identifier = *state.abortTarget
		state.abortTarget = nil
		state.terminal[identifier] = true
		found = true
		if state.hasCurrent && state.current == identifier {
			if state.currentInteraction != "" {
				state.closedInteractions[state.currentInteraction] = true
			}
			state.current = uuid.Nil
			state.currentInteraction = ""
			state.hasCurrent = false
			state.activeSDK = nil
		}
		sessionBusy = hasSessionWork(state)
	})
	return identifier, sessionBusy, found
}

func (correlator *turnCorrelator) lookup(interactionID string, sdkTurnID string) (uuid.UUID, bool) {
	var identifier uuid.UUID
	found := false
	correlator.run(func(state *turnCorrelationState) {
		if interactionID != "" {
			explicitKey := sdkTurnKey{interactionID: interactionID, turnID: sdkTurnID}
			identifier, found = state.bySDK[explicitKey]
			if found || state.closedInteractions[interactionID] || state.activeSDK == nil ||
				state.activeSDK.turnID != sdkTurnID ||
				!isAnonymousInteraction(state.activeSDK.interactionID) {
				return
			}
			// A start can omit the interaction id even when later output supplies it.
			// Only enrich the matching active anonymous key: turn ids repeat across
			// interactions, so no historical or explicit-key fallback is safe.
			anonymousKey := *state.activeSDK
			identifier, found = state.bySDK[anonymousKey]
			if !found {
				return
			}
			delete(state.bySDK, anonymousKey)
			state.bySDK[explicitKey] = identifier
			state.activeSDK = &explicitKey
			if state.hasCurrent && state.currentInteraction == anonymousKey.interactionID {
				state.currentInteraction = interactionID
			}
			return
		}
		if state.activeSDK != nil && state.activeSDK.turnID == sdkTurnID {
			identifier, found = state.bySDK[*state.activeSDK]
			return
		}
		for key, candidate := range state.bySDK {
			if key.turnID != sdkTurnID {
				continue
			}
			if found && candidate != identifier {
				identifier, found = uuid.Nil, false
				return
			}
			identifier, found = candidate, true
		}
	})
	return identifier, found
}

// terminateActive removes the durable prompt the SDK currently considers foreground.
// Abort and session-error payloads carry no turn id, so event-time ordering is
// the only exact correlation available. The interaction is tombstoned so late
// SDK events cannot resurrect its terminal durable owner.
func (correlator *turnCorrelator) terminateActive() (uuid.UUID, bool, bool) {
	var identifier uuid.UUID
	found, sessionBusy := false, false
	correlator.run(func(state *turnCorrelationState) {
		if !state.hasCurrent {
			return
		}
		identifier, found = state.current, true
		state.terminal[identifier] = true
		if state.currentInteraction != "" {
			state.closedInteractions[state.currentInteraction] = true
		}
		state.current = uuid.Nil
		state.currentInteraction = ""
		state.hasCurrent = false
		state.activeSDK = nil
		if state.abortTarget != nil && *state.abortTarget == identifier {
			state.abortTarget = nil
		}
		sessionBusy = hasSessionWork(state)
	})
	return identifier, sessionBusy, found
}

func (correlator *turnCorrelator) active() (uuid.UUID, bool) {
	var identifier uuid.UUID
	found := false
	correlator.run(func(state *turnCorrelationState) {
		identifier, found = state.current, state.hasCurrent
	})
	return identifier, found
}

func (correlator *turnCorrelator) stop() {
	correlator.stopOnce.Do(func() { close(correlator.stopRequested) })
	<-correlator.stopped
}
