// Copyright 2026 Candace Labs

// Package runtime is the process runtime: it owns the lifetimes every
// goroutine in a CSF process starts under.
//
// A process has exactly one [HostRuntime], built by its main function, which
// owns the root lifetime. Services mount into it; each mounted service receives
// its own [Scope]. A scope has one owner, one cancellation and one join: when
// it ends, every goroutine it started has returned and every error they
// reported is collected.
//
// A scope is the easiest way to own a goroutine, not the only permitted one.
// Services may start goroutines themselves; whoever starts one is responsible
// for its cleanup (cancellation and a join), and when goroutines coordinate
// across services, the code that wires them together owns that lifecycle.
// House rule CS-15 locates goroutines that show no such owner.
//
// A service mounted through [NewLazyService] starts on its first use instead
// of at mount, on a child scope that is canceled and joined after an idle
// timeout and started afresh on the next use.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/sourcegraph/conc/pool"
)

// ErrScopeClosed is returned when a goroutine or child scope is requested
// from a scope that has already begun joining.
var ErrScopeClosed = errors.New("runtime: scope is closed")

// goroutineName labels a plain [Scope.Go] task in errors; owners name
// themselves through [Scope.GoOwner].
const goroutineName = "goroutine"

// Scope is one explicit lifetime with one owner, one cancellation and one
// completion. Cancellation requests shutdown; [Scope.Wait] returns once every
// goroutine started through the scope, and every child scope, has exited.
//
// A goroutine that returns an error cancels its scope, and the scope reports
// the failure to whoever owns it — its parent scope, or the [HostRuntime] for a
// mounted service — so one failed owner stops the process instead of leaving it
// half-running. Errors are collected rather than racing for first place:
// [Scope.Wait] returns all of them joined.
//
// The zero value is not usable; scopes come from [NewScope], [Scope.Child] and
// [HostRuntime.Mount].
type Scope struct {
	owner     string
	ctx       context.Context
	cancel    context.CancelCauseFunc
	pool      *pool.ContextPool
	onFailure func(err error)

	// admission makes starting a goroutine and beginning the join mutually
	// exclusive. conc's pool, like a WaitGroup, must not gain a goroutine once
	// its Wait has begun, so start holds the read side across the check and
	// pool.Go, and Wait takes the write side to set joining. It is a leaf
	// critical section (CS-5): one flag, nothing sequenced through it, and the
	// pool's Wait runs outside it.
	admission  sync.RWMutex
	joining    bool
	goroutines atomic.Int64
	live       atomic.Int64

	waitOnce sync.Once
	waitErr  error
}

// NewScope returns a standalone scope whose lifetime is bounded by ctx. It is
// the scope a test or a one-shot tool uses; a long-running process gets its
// scopes from a [HostRuntime] instead.
func NewScope(ctx context.Context, owner string) *Scope {
	return newScope(ctx, owner, nil)
}

func newScope(ctx context.Context, owner string, onFailure func(err error)) *Scope {
	scopeContext, cancel := context.WithCancelCause(ctx)
	return &Scope{
		owner:     owner,
		ctx:       scopeContext,
		cancel:    cancel,
		pool:      pool.New().WithContext(scopeContext),
		onFailure: onFailure,
	}
}

// Owner names the scope in logs and errors.
func (scope *Scope) Owner() string { return scope.owner }

// Context is canceled when the scope is asked to stop. Values the root context
// carried are preserved.
func (scope *Scope) Context() context.Context { return scope.ctx }

// Done is closed when the scope has been asked to stop.
func (scope *Scope) Done() <-chan struct{} { return scope.ctx.Done() }

// Goroutines reports how many goroutines this scope has started, counting a
// child scope's join as one. After [Scope.Wait] returns every one of them has
// exited.
func (scope *Scope) Goroutines() int64 { return scope.goroutines.Load() }

// Live reports how many goroutines this scope started that have not yet
// returned, counting a running child scope's join as one. Unlike
// [Scope.Goroutines], which only grows, Live falls back as goroutines exit: it
// is how a caller observes that a retired lifetime, such as a
// [LazyService]'s idle child, really joined.
func (scope *Scope) Live() int64 { return scope.live.Load() }

// Go starts task on a new goroutine owned by this scope. A task may finish at
// any time; returning nil before the scope is canceled is a normal completion.
// A non-nil error cancels the scope and is reported by [Scope.Wait].
// context.Canceled returned after the scope was canceled is not a failure.
func (scope *Scope) Go(task func(ctx context.Context) error) error {
	return scope.start(goroutineName, task, false)
}

// GoOwner starts owner on a new goroutine that must last the whole scope: a
// server loop, a watcher, a datum's owning goroutine. Returning — even with a
// nil error — before the scope is canceled is reported as a failure, because a
// process whose owner silently stopped is not running.
func (scope *Scope) GoOwner(name string, owner func(ctx context.Context) error) error {
	return scope.start(name, owner, true)
}

func (scope *Scope) start(name string, task func(ctx context.Context) error, untilCanceled bool) error {
	if task == nil {
		return fmt.Errorf("runtime: %s: nil goroutine", scope.owner)
	}
	scope.admission.RLock()
	defer scope.admission.RUnlock()
	if scope.joining {
		return fmt.Errorf("%w: %s", ErrScopeClosed, scope.owner)
	}
	scope.goroutines.Add(1)
	scope.live.Add(1)
	scope.pool.Go(func(ctx context.Context) error {
		defer scope.live.Add(-1)
		err := task(ctx)
		canceled := ctx.Err() != nil
		switch {
		case err == nil && untilCanceled && !canceled:
			err = fmt.Errorf("runtime: %s: %s stopped before its scope was canceled", scope.owner, name)
		case err != nil && canceled && errors.Is(err, context.Canceled):
			err = nil
		case err != nil && name != "":
			err = fmt.Errorf("runtime: %s: %s: %w", scope.owner, name, err)
		}
		if err != nil {
			scope.fail(err)
		}
		return err
	})
	return nil
}

func (scope *Scope) fail(err error) {
	scope.cancel(err)
	if scope.onFailure != nil {
		scope.onFailure(err)
	}
}

// Child returns a scope whose lifetime is nested in this one: canceling the
// parent cancels the child, and the parent's join waits for the child's. A
// child's errors are its parent's errors.
func (scope *Scope) Child(owner string) (*Scope, error) {
	child := newScope(scope.ctx, scope.owner+"/"+owner, nil)
	// The child's errors already carry its owner; the join that relays them
	// adds no name of its own.
	if err := scope.start("", func(_ context.Context) error {
		<-child.Done()
		return child.Wait()
	}, false); err != nil {
		child.cancel(err)
		return nil, err
	}
	return child, nil
}

// Cancel asks every goroutine in the scope, and in its children, to stop. It
// does not wait.
func (scope *Scope) Cancel() { scope.cancel(context.Canceled) }

// Wait blocks until every goroutine the scope started has returned and returns
// their collected errors. It does not cancel the scope; call [Scope.Close] to
// do both. Wait may be called more than once and from more than one goroutine.
func (scope *Scope) Wait() error {
	scope.waitOnce.Do(func() {
		scope.admission.Lock()
		scope.joining = true
		scope.admission.Unlock()
		scope.waitErr = scope.pool.Wait()
	})
	return scope.waitErr
}

// Close cancels the scope and waits for it to join.
func (scope *Scope) Close() error {
	scope.Cancel()
	return scope.Wait()
}
