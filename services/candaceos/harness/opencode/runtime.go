package opencode

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/candacelabs/csf/pkg/mailbox"
	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	"github.com/candacelabs/csf/services/candaceos/harness"
)

// command is one unit of work run on the runtime's command goroutine. It
// reports true to retire that goroutine.
type command = mailbox.Command[sessionState]

// harnessRuntime is the harness.IRuntime implementation for one OpenCode
// session. Every field below is immutable after construction except the
// channels, which are the mailbox; all session state lives behind the command
// goroutine started by newRuntime.
type harnessRuntime struct {
	config    *candaceosv1.OpenCodeConfig
	workspace string
	host      harness.IHost
	sdk       iProvider
	model     promptModel

	// commands is the session's mailbox: every field of sessionState is read
	// and written only by the command goroutine it feeds.
	commands *mailbox.Mailbox[sessionState]
	// invalidations is a one-slot wakeup for the poller. A send never blocks
	// and a pending wakeup absorbs further ones.
	invalidations  chan struct{}
	shutdown       context.Context
	cancelShutdown context.CancelFunc
	watchers       sync.WaitGroup
}

var _ harness.IRuntime = (*harnessRuntime)(nil)

// newRuntime starts the command goroutine for one session against sdk. The
// caller owns the returned runtime and must Close it, including when Start
// later fails. config must already have passed
// candaceosv1.ValidateOpenCodeConfig; Factory is the validated entry point.
func newRuntime(
	config *candaceosv1.OpenCodeConfig,
	workspace string,
	host harness.IHost,
	sdk iProvider,
) (*harnessRuntime, error) {
	if config == nil {
		return nil, ErrConfigRequired
	}
	if sdk == nil {
		return nil, ErrProviderRequired
	}
	model, err := parsePromptModel(config.GetModel())
	if err != nil {
		return nil, err
	}
	shutdown, cancelShutdown := context.WithCancel(context.Background())
	runtime := &harnessRuntime{
		config:         config,
		workspace:      workspace,
		host:           host,
		sdk:            sdk,
		model:          model,
		commands:       mailbox.New[sessionState](),
		invalidations:  make(chan struct{}, 1),
		shutdown:       shutdown,
		cancelShutdown: cancelShutdown,
	}
	go runtime.run()
	return runtime, nil
}

func (h *harnessRuntime) run() {
	state := sessionState{}
	h.commands.Run(&state)
}

// submit hands one command to the command goroutine, reporting false when the
// caller's context, the supplied cancellation channel, or shutdown won first.
func (h *harnessRuntime) submit(ctx context.Context, canceled <-chan struct{}, work command) bool {
	return h.commands.SubmitContext(ctx, canceled, work)
}

// call runs operation on the command goroutine and returns its error, mapping a
// mailbox that is no longer accepting work to the caller's own cancellation or
// to ErrSessionUnavailable.
func (h *harnessRuntime) call(ctx context.Context, operation func(state *sessionState) error) error {
	reply := make(chan error, 1)
	if !h.submit(ctx, h.shutdown.Done(), func(state *sessionState) bool {
		reply <- operation(state)
		return false
	}) {
		if err := ctx.Err(); err != nil {
			return err
		}
		return ErrSessionUnavailable
	}
	return <-reply
}

// operationContext derives a context from parent that is also canceled when
// lifecycle ends, so a provider call never outlives the session it belongs to.
func operationContext(parent, lifecycle context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	stopLifecycleCancellation := context.AfterFunc(lifecycle, cancel)
	return ctx, func() {
		stopLifecycleCancellation()
		cancel()
	}
}

// Start attaches to exactly one OpenCode session and hydrates it without
// publishing anything. ctx bounds the attach and also parents the session
// lifecycle, so Core must supply the context that governs the session's
// lifetime rather than a per-request one. A second Start reports
// ErrAlreadyStarted.
func (h *harnessRuntime) Start(ctx context.Context) (*candaceosv1.HarnessSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	type startResult struct {
		session *candaceosv1.HarnessSession
		err     error
	}
	reply := make(chan startResult, 1)
	if !h.submit(ctx, h.shutdown.Done(), func(state *sessionState) bool {
		session, err := h.attach(state, ctx)
		reply <- startResult{session: session, err: err}
		return false
	}) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, ErrClosed
	}
	result := <-reply
	return result.session, result.err
}

// Activate publishes the session-started event and begins reconciliation. Core
// calls it once after a successful Start; a second call reports
// ErrAlreadyActivated, and a failed publication leaves the runtime
// unactivated so Core may retry.
func (h *harnessRuntime) Activate(ctx context.Context) error {
	return h.call(ctx, func(state *sessionState) error {
		return h.activate(state, ctx)
	})
}

// Send admits one prompt. It clones prompt, so the caller keeps ownership of
// its protobuf. Delivery follows the prompt's HarnessDelivery: ENQUEUE appends
// to the bounded queue and reports ErrQueueFull at capacity, IMMEDIATE steers
// the active turn. Send returns once the prompt has been admitted, not once
// the turn completes.
func (h *harnessRuntime) Send(ctx context.Context, prompt *candaceosv1.HarnessPrompt) error {
	owned := &candaceosv1.HarnessPrompt{}
	if prompt != nil {
		owned = proto.Clone(prompt).(*candaceosv1.HarnessPrompt)
	}
	return h.call(ctx, func(state *sessionState) error {
		return h.send(state, ctx, owned)
	})
}

// Abort stops the active turn and discards queued guidance. The idle event that
// concludes the aborted turn keeps that turn's run ID. A provider abort that
// the runtime requested is suppressed rather than published as a turn failure.
func (h *harnessRuntime) Abort(ctx context.Context) error {
	return h.call(ctx, func(state *sessionState) error {
		return h.abort(state, ctx)
	})
}

// Close ends the session, cancels every in-flight provider call and host
// publication, retires the command goroutine, and waits for the background
// poller and event watcher. It is idempotent, safe to call concurrently with
// any other method, and valid after a failed Start or Activate. The OpenCode
// server session itself is left intact for a later attach.
func (h *harnessRuntime) Close() error {
	h.cancelShutdown()
	h.submit(context.Background(), nil, func(state *sessionState) bool {
		state.activated = false
		state.turn.pending = nil
		state.turn.active = nil
		if state.cancelLifecycle != nil {
			state.cancelLifecycle()
			state.cancelLifecycle = nil
		}
		return true
	})
	<-h.commands.Stopped()
	h.watchers.Wait()
	return nil
}

// attach runs on the command goroutine: it verifies the server contract, binds
// exactly one workspace-scoped session, and seeds projection state from the
// existing transcript so history is never republished.
func (h *harnessRuntime) attach(
	state *sessionState,
	ctx context.Context,
) (*candaceosv1.HarnessSession, error) {
	if state.lifecycle != nil {
		return nil, ErrAlreadyStarted
	}
	operationCtx, cancel := operationContext(ctx, h.shutdown)
	defer cancel()
	version, err := h.verifyServer(operationCtx)
	if err != nil {
		return nil, err
	}
	session, err := h.bindSession(operationCtx)
	if err != nil {
		return nil, err
	}
	messages, phase, err := h.hydrate(operationCtx, session.ID)
	if err != nil {
		return nil, fmt.Errorf("opencode: hydrating session: %w", err)
	}
	state.lifecycle, state.cancelLifecycle = operationContext(ctx, h.shutdown)
	state.sessionID = session.ID
	state.version = version
	state.turn = newTurnState()
	state.turn.lastPhase = phase
	state.turn.busy = phase.active()
	state.projected = newProjectionState()
	projectMessages(&state.projected, state.turn.runs, messages)
	return &candaceosv1.HarnessSession{Id: session.ID}, nil
}

func (h *harnessRuntime) verifyServer(ctx context.Context) (string, error) {
	healthy, version, err := h.sdk.health(ctx)
	if err != nil {
		return "", fmt.Errorf("opencode: checking health: %w", err)
	}
	if !healthy {
		return "", ErrUnhealthy
	}
	if version != PinnedServerVersion {
		return "", fmt.Errorf("%w: server reports %q, pinned %q", ErrVersionMismatch, version, PinnedServerVersion)
	}
	return version, nil
}

// bindSession attaches to the configured session or creates one, and refuses
// any session that is not scoped to this runtime's workspace.
func (h *harnessRuntime) bindSession(ctx context.Context) (providerSession, error) {
	var (
		bound providerSession
		err   error
	)
	if configured := h.config.GetSessionId(); configured != "" {
		bound, err = h.sdk.session(ctx, configured)
	} else {
		bound, err = h.sdk.createSession(ctx, "CandaceOS Claw")
	}
	if err != nil {
		return providerSession{}, fmt.Errorf("opencode: opening session: %w", err)
	}
	if bound.ID == "" {
		return providerSession{}, ErrEmptySession
	}
	if filepath.Clean(bound.Directory) != filepath.Clean(h.workspace) {
		return providerSession{}, fmt.Errorf(
			"%w: session %q is in %q, not %q", ErrWorkspaceMismatch, bound.ID, bound.Directory, h.workspace,
		)
	}
	return bound, nil
}

// hydrate reads one coherent transcript snapshot, retrying while the session's
// status keeps moving under the read.
func (h *harnessRuntime) hydrate(
	ctx context.Context,
	sessionID string,
) ([]providerMessage, sessionPhase, error) {
	for attempt := 1; attempt <= startupHydrationAttempts; attempt++ {
		messages, phase, coherent, err := h.sdk.rehydrate(ctx, sessionID)
		if err != nil {
			return nil, "", err
		}
		if coherent {
			return messages, phase, nil
		}
		if attempt != startupHydrationAttempts && !wait(ctx, startupHydrationRetryDelay) {
			return nil, "", ctx.Err()
		}
	}
	return nil, "", ErrIncoherentSession
}

// activate runs on the command goroutine. It publishes the attach event first
// and starts the background watchers only once the host has accepted it, so a
// rejected activation leaves nothing running.
func (h *harnessRuntime) activate(state *sessionState, ctx context.Context) error {
	if !state.live() {
		return ErrSessionUnavailable
	}
	if state.activated {
		return ErrAlreadyActivated
	}
	ctx, cancel := operationContext(ctx, state.lifecycle)
	defer cancel()
	state.activated = true
	err := h.host.Publish(ctx, &candaceosv1.HarnessEvent{
		Id: "opencode-session-" + state.sessionID,
		At: timestamppb.Now(),
		Payload: &candaceosv1.HarnessEvent_SessionStarted{
			SessionStarted: &candaceosv1.HarnessSessionStarted{
				Message: "OpenCode " + state.version + " session " + state.sessionID + " is attached.",
			},
		},
	})
	if err != nil {
		state.activated = false
		return err
	}
	h.watchers.Add(2)
	go h.poll(state.lifecycle)
	go h.watchEvents(state.lifecycle, state.sessionID)
	h.invalidate()
	return nil
}

// invalidate schedules one reconciliation. It never blocks, so it is safe from
// the event-stream goroutine and from the command goroutine alike.
func (h *harnessRuntime) invalidate() {
	select {
	case h.invalidations <- struct{}{}:
	default:
	}
}

// wait sleeps for duration, reporting false when ctx ended first.
func wait(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
