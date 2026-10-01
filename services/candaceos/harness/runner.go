package harness

import (
	"context"
	"errors"

	"github.com/candacelabs/csf/pkg/mailbox"
	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	"google.golang.org/protobuf/proto"
)

var (
	// ErrRunnerClosed reports an attempted start after lifecycle shutdown began.
	ErrRunnerClosed = errors.New("harness runner is closed")
	// ErrRunnerStarted reports an attempted second start or adapter installation.
	ErrRunnerStarted = errors.New("harness runner is already started")
	// ErrRuntimeUnavailable reports an operation without an active adapter.
	ErrRuntimeUnavailable = errors.New("harness runtime is unavailable")
)

// Runner serializes one provider session's lifecycle and event activation.
// Its dedicated receiver goroutine owns the installed operations, callback
// buffer, activation state, in-flight count, and close result.
type Runner[Event any] struct {
	mailbox *mailbox.Mailbox[runnerState[Event]]
	stopErr error
	publish func(event Event)
	eventID func(event Event) string
}

type runnerState[Event any] struct {
	lifecycle context.Context
	cancel    context.CancelFunc

	send    func(ctx context.Context, prompt *candaceosv1.HarnessPrompt) error
	abort   func(ctx context.Context) error
	cleanup func() error
	pending []Event

	begun        bool
	installed    bool
	eventsActive bool
	closing      bool
	cleanupDone  bool
	inFlight     int
	closeErr     error
}

// NewRunner starts the lifecycle owner. publish receives replay before
// callbacks accepted prior to Activate; eventID supplies the deduplication key.
// A nil publish discards events and a nil eventID disables deduplication.
func NewRunner[Event any](publish func(event Event), eventID func(event Event) string) *Runner[Event] {
	if publish == nil {
		publish = func(event Event) {}
	}
	if eventID == nil {
		eventID = func(event Event) string { return "" }
	}
	runner := &Runner[Event]{
		mailbox: mailbox.New[runnerState[Event]](),
		publish: publish,
		eventID: eventID,
	}
	go runner.run()
	return runner
}

func (runner *Runner[Event]) run() {
	lifecycle, cancel := context.WithCancel(context.Background())
	state := runnerState[Event]{lifecycle: lifecycle, cancel: cancel}
	defer cancel()
	runner.mailbox.Run(&state)
}

func (runner *Runner[Event]) submit(command mailbox.Command[runnerState[Event]]) bool {
	return runner.mailbox.Submit(command)
}

// BeginStart reserves the runner for one provider session. It must precede
// provider startup so callbacks emitted while the session opens are retained.
func (runner *Runner[Event]) BeginStart() error {
	reply := make(chan error)
	if !runner.submit(func(state *runnerState[Event]) bool {
		switch {
		case state.closing:
			reply <- ErrRunnerClosed
		case state.begun:
			reply <- ErrRunnerStarted
		default:
			state.begun = true
			reply <- nil
		}
		return false
	}) {
		return ErrRunnerClosed
	}
	return <-reply
}

// Install makes native provider operations available after its session opens.
// Nil operations remain unavailable; nil cleanup means no provider cleanup.
// Until Install succeeds, the caller retains cleanup ownership. Installed
// operations must return when their context is canceled or cleanup runs.
func (runner *Runner[Event]) Install(
	send func(ctx context.Context, prompt *candaceosv1.HarnessPrompt) error,
	abort func(ctx context.Context) error,
	cleanup func() error,
) error {
	reply := make(chan error)
	if !runner.submit(func(state *runnerState[Event]) bool {
		switch {
		case state.closing:
			reply <- ErrRunnerClosed
		case state.installed:
			reply <- ErrRunnerStarted
		default:
			state.begun = true
			state.installed = true
			state.send = send
			state.abort = abort
			state.cleanup = cleanup
			reply <- nil
		}
		return false
	}) {
		return ErrRunnerClosed
	}
	return <-reply
}

// Activate publishes replay before buffered callbacks, deduplicating nonempty
// event IDs. A second call is a FIFO barrier and otherwise has no effect.
func (runner *Runner[Event]) Activate(replay []Event) {
	done := make(chan struct{})
	ownedReplay := append([]Event(nil), replay...)
	if !runner.submit(func(state *runnerState[Event]) bool {
		defer close(done)
		if state.closing || state.eventsActive {
			return false
		}
		for _, event := range runner.deduplicate(ownedReplay, state.pending) {
			runner.deliver(event)
		}
		state.pending = nil
		state.eventsActive = true
		return false
	}) {
		return
	}
	<-done
}

// Publish accepts a provider callback. Before Activate it is buffered; after
// Activate it is delivered synchronously by the lifecycle owner.
func (runner *Runner[Event]) Publish(event Event) {
	_ = runner.submit(func(state *runnerState[Event]) bool {
		if state.closing {
			return false
		}
		if !state.eventsActive {
			state.pending = append(state.pending, event)
			return false
		}
		runner.deliver(event)
		return false
	})
}

// deliver hands one event to the host projection. The lifecycle owner is a
// single goroutine, so a panic escaping the host callback would kill it and
// hang every later Send, Abort, and Close. It is contained here; the adapter
// that owns the projection is responsible for reporting the failure.
func (runner *Runner[Event]) deliver(event Event) {
	defer func() { _ = recover() }()
	runner.publish(event)
}

// Send runs one native provider turn without blocking the lifecycle owner.
func (runner *Runner[Event]) Send(
	ctx context.Context,
	prompt *candaceosv1.HarnessPrompt,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var ownedPrompt *candaceosv1.HarnessPrompt
	if prompt != nil {
		ownedPrompt = proto.Clone(prompt).(*candaceosv1.HarnessPrompt)
	}
	reply := make(chan error)
	if !runner.submit(func(state *runnerState[Event]) bool {
		if err := ctx.Err(); err != nil {
			reply <- err
			return false
		}
		if state.closing || !state.installed || state.send == nil {
			reply <- ErrRuntimeUnavailable
			return false
		}
		send := state.send
		runner.startOperation(state, ctx, reply, func(operationContext context.Context) error {
			return send(operationContext, ownedPrompt)
		})
		return false
	}) {
		return ErrRuntimeUnavailable
	}
	return <-reply
}

// Abort runs native provider cancellation without blocking the lifecycle owner.
func (runner *Runner[Event]) Abort(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	reply := make(chan error)
	if !runner.submit(func(state *runnerState[Event]) bool {
		if err := ctx.Err(); err != nil {
			reply <- err
			return false
		}
		if state.closing || !state.installed || state.abort == nil {
			reply <- ErrRuntimeUnavailable
			return false
		}
		runner.startOperation(state, ctx, reply, state.abort)
		return false
	}) {
		return ErrRuntimeUnavailable
	}
	return <-reply
}

func (runner *Runner[Event]) startOperation(
	state *runnerState[Event],
	ctx context.Context,
	reply chan error,
	operation func(ctx context.Context) error,
) {
	operationContext, cancel := context.WithCancel(ctx)
	stopCancellation := context.AfterFunc(state.lifecycle, cancel)
	state.inFlight++
	go func() {
		err := operation(operationContext)
		stopCancellation()
		cancel()
		runner.finishOperation(reply, err)
	}()
}

func (runner *Runner[Event]) finishOperation(reply chan error, err error) {
	if !runner.submit(func(state *runnerState[Event]) bool {
		state.inFlight--
		reply <- err
		return runner.finishIfReady(state)
	}) {
		reply <- err
	}
}

// Close cancels accepted operations, runs provider cleanup once, and waits for
// both. It is idempotent and concurrent callers receive the same cleanup error.
func (runner *Runner[Event]) Close() error {
	_ = runner.submit(func(state *runnerState[Event]) bool {
		return runner.beginClose(state)
	})
	<-runner.mailbox.Stopped()
	return runner.stopErr
}

func (runner *Runner[Event]) beginClose(state *runnerState[Event]) bool {
	if state.closing {
		return false
	}
	state.closing = true
	state.eventsActive = false
	state.pending = nil
	state.cancel()
	cleanup := state.cleanup
	state.send = nil
	state.abort = nil
	state.cleanup = nil
	if cleanup == nil {
		state.cleanupDone = true
		return runner.finishIfReady(state)
	}
	go func() {
		err := cleanup()
		_ = runner.submit(func(state *runnerState[Event]) bool {
			state.cleanupDone = true
			state.closeErr = err
			return runner.finishIfReady(state)
		})
	}()
	return false
}

func (runner *Runner[Event]) finishIfReady(state *runnerState[Event]) bool {
	if !state.closing || !state.cleanupDone || state.inFlight != 0 {
		return false
	}
	runner.stopErr = state.closeErr
	// Stop before returning, not after: Close is already blocked on Stopped
	// and reads stopErr the moment it unblocks, so the publication has to
	// happen on this side of the close. Run stops the mailbox too, which is
	// why Stop is idempotent.
	runner.mailbox.Stop()
	return true
}

func (runner *Runner[Event]) deduplicate(groups ...[]Event) []Event {
	seen := make(map[string]struct{})
	var result []Event
	for _, group := range groups {
		for _, event := range group {
			id := runner.eventID(event)
			if id != "" {
				if _, exists := seen[id]; exists {
					continue
				}
				seen[id] = struct{}{}
			}
			result = append(result, event)
		}
	}
	return result
}
