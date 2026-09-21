// Package mailbox serializes ownership of a mutable value onto one goroutine.
//
// A Mailbox is the shape both of CandaceOS's harness runtimes converged on
// independently: state that several callers must read and write, guarded not
// by a lock but by a single goroutine that runs every access in turn. Callers
// hand it a [Command] — a function of a pointer to the state — and the
// goroutine runs commands one at a time, so no field needs a lock and no two
// commands ever observe each other half-applied.
//
// # The contract
//
// Submission is the serialization point, and the channel is unbuffered
// deliberately: a caller whose command was accepted knows its work runs next
// rather than sitting behind an invisible backlog. Submission is also the only
// place a caller learns the mailbox is gone — [Mailbox.Submit] reports false
// rather than blocking forever or panicking, and the caller decides what that
// means in its own vocabulary.
//
// A command returning true retires the goroutine. [Mailbox.Run] then stops the
// mailbox, so a retirement always makes later submissions fail; a command may
// also call [Mailbox.Stop] itself, before it returns, when it has state to
// publish that a caller waiting on [Mailbox.Stopped] must see. Stop is
// idempotent precisely so those two paths can both happen.
//
// # What it is not
//
// It is not a work queue, a supervisor, or a scheduler. It has no buffering,
// no retry, no timeout of its own, and no opinion about what the state is or
// what a command may do with it — including blocking, which stalls every other
// caller and is the one thing a command must not do without meaning to. The
// lifecycle above it (starting the goroutine, deciding what retires it, what a
// failed submission means) stays with the owner, because that is the part the
// two callers do not agree on.
package mailbox

import (
	"context"
	"sync"
)

// Command is one unit of work run on the owning goroutine, holding exclusive
// access to the state for as long as it runs. Returning true retires the
// goroutine after this command completes; returning false leaves the mailbox
// accepting work.
type Command[State any] func(state *State) bool

// Mailbox is the submission side of one serialized state owner. The zero value
// is not usable; construct one with [New].
type Mailbox[State any] struct {
	commands chan Command[State]
	stopped  chan struct{}
	stopOnce sync.Once
}

// New returns a mailbox whose goroutine has not started. The owner starts it
// by calling [Mailbox.Run] in a goroutine of its own, which is what lets the
// owner build the initial state — and anything the state's lifetime is tied
// to, such as a cancellation function to defer — in that goroutine.
func New[State any]() *Mailbox[State] {
	return &Mailbox[State]{
		commands: make(chan Command[State]),
		stopped:  make(chan struct{}),
	}
}

// Run owns state until a command retires the mailbox, then stops it. It blocks,
// and it must be called exactly once, from the one goroutine that is to own the
// state.
func (mailbox *Mailbox[State]) Run(state *State) {
	for command := range mailbox.commands {
		if command(state) {
			break
		}
	}
	mailbox.Stop()
}

// Submit hands one command to the owning goroutine and blocks until it is
// accepted, reporting false when the mailbox stopped first. It does not wait
// for the command to run: a caller that needs the result replies to itself
// through a channel the command closes over.
func (mailbox *Mailbox[State]) Submit(command Command[State]) bool {
	select {
	case mailbox.commands <- command:
		return true
	case <-mailbox.stopped:
		return false
	}
}

// SubmitContext is Submit with two more ways to give up: ctx being done, and
// canceled being closed. A nil canceled channel never fires, which is how a
// caller that has only a context spells "no second cancellation".
//
// It reports false for all three abandonments alike, because the caller
// already holds the ctx and the channel and can tell them apart better than
// this package can name them.
func (mailbox *Mailbox[State]) SubmitContext(
	ctx context.Context,
	canceled <-chan struct{},
	command Command[State],
) bool {
	select {
	case <-ctx.Done():
		return false
	case <-canceled:
		return false
	case <-mailbox.stopped:
		return false
	case mailbox.commands <- command:
		return true
	}
}

// Stop makes every later submission fail and closes the channel [Mailbox.Stopped]
// reports. It is idempotent, and it does not retire the goroutine on its own —
// only a command returning true does that. A command calls it directly when a
// caller waiting on Stopped must see state the command has just published.
func (mailbox *Mailbox[State]) Stop() {
	mailbox.stopOnce.Do(func() { close(mailbox.stopped) })
}

// Stopped is closed once the mailbox no longer accepts work. A caller awaiting
// shutdown selects on it; whatever the owner publishes before calling
// [Mailbox.Stop] is visible to everything this unblocks.
func (mailbox *Mailbox[State]) Stopped() <-chan struct{} { return mailbox.stopped }
