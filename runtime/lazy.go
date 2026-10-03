// Copyright 2026 Candace Labs

package runtime

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrLazyServiceNotStarted is returned by [LazyService.Use] before the lazy
// service has been mounted, that is, before its own Start recorded a parent.
var ErrLazyServiceNotStarted = errors.New("runtime: lazy service has not been started")

// ErrLazyServiceStopped is returned by [LazyService.Use] once the scope the
// lazy service was mounted on has been canceled, or after the wrapped service
// failed on its own: there is nothing left to start it under.
var ErrLazyServiceStopped = errors.New("runtime: lazy service has stopped")

// defaultLazyName names a lazy service's child scope when [WithLazyName] does
// not.
const defaultLazyName = "lazy"

// IIdleReporter is optionally implemented by a service wrapped in a
// [LazyService]. When no caller holds a lease and the idle timeout has
// elapsed, the wrapper asks it before retiring the run: a service with work
// still in hand — queued messages, a blocked receiver — reports false and is
// asked again one idle timeout later. A service that does not implement it is
// idle whenever no caller holds a lease.
type IIdleReporter interface {
	Idle() bool
}

// ILazyService is a service started on first use rather than at mount.
//
// It is mounted like any [IService]; its Start only records the scope it was
// mounted on. The first [ILazyService.Use] starts T on a child of that scope
// and holds a lease for the duration of the call. When the last lease is
// released and the idle timeout elapses, the child scope is canceled and
// joined; the next Use starts T again on a new child scope.
//
// The contract on T: Start may be called more than once, each time on a new
// scope, and only after every goroutine of the previous scope has returned.
// Whatever T must keep between runs it keeps outside the goroutines its scope
// joins. Start errors surface at use time, to the caller of Use, which is why
// lazy start is a composer's visible choice rather than a default.
type ILazyService[T IService] interface {
	IService
	Use(ctx context.Context, call func(service T) error) error
}

// LazyServiceOption configures a [LazyService]; [NewLazyService] validates
// the whole set before building anything.
type LazyServiceOption func(options *lazyOptions) error

type lazyOptions struct {
	idle time.Duration
	now  func() time.Time
	name string
}

// WithIdleTimeout is how long a lazy service may go without a lease before
// its run is retired. It is required, and it must be positive.
func WithIdleTimeout(idle time.Duration) LazyServiceOption {
	return func(options *lazyOptions) error {
		if idle <= 0 {
			return fmt.Errorf("runtime: idle timeout must be positive, got %s", idle)
		}
		options.idle = idle
		return nil
	}
}

// WithLazyClock replaces the clock that measures idleness. The idle check
// still wakes on the real clock; a test that freezes this one holds the
// service running, and advancing it lets the next check retire the run.
func WithLazyClock(now func() time.Time) LazyServiceOption {
	return func(options *lazyOptions) error {
		if now == nil {
			return errors.New("runtime: WithLazyClock needs a clock")
		}
		options.now = now
		return nil
	}
}

// WithLazyName names the child scope each run starts on, under the scope the
// lazy service is mounted on.
func WithLazyName(name string) LazyServiceOption {
	return func(options *lazyOptions) error {
		if name == "" {
			return errors.New("runtime: WithLazyName needs a name")
		}
		options.name = name
		return nil
	}
}

// lazyState is the lazy service's lifecycle. It is passed hand to hand
// through a one-slot channel rather than guarded by a mutex: whoever holds the
// value owns it, and a caller waiting for it can give up on its context, which
// a lock cannot offer. There is deliberately no owning goroutine — a lazy
// service exists to cost nothing while idle.
type lazyState struct {
	parent *Scope
	// child is the current run's scope, or the last run's after retirement;
	// nil before the first run.
	child *Scope
	// retired records that this wrapper canceled child for idleness, as
	// opposed to the child failing on its own.
	retired     bool
	leases      int
	lastRelease time.Time
}

// LazyService wraps T so it starts on first use and stops when idle. See
// [ILazyService] for the lifecycle and the contract on T.
type LazyService[T IService] struct {
	service  T
	reporter IIdleReporter
	idle     time.Duration
	now      func() time.Time
	name     string
	state    chan *lazyState
}

var _ ILazyService[IService] = (*LazyService[IService])(nil)

// NewLazyService validates its options and wraps service without starting
// it. [WithIdleTimeout] is required.
func NewLazyService[T IService](service T, options ...LazyServiceOption) (*LazyService[T], error) {
	if IService(service) == nil {
		return nil, errors.New("runtime: a lazy service needs a service")
	}
	configured := lazyOptions{now: time.Now, name: defaultLazyName}
	for index, option := range options {
		if option == nil {
			return nil, fmt.Errorf("runtime: lazy service option %d is nil", index)
		}
		if err := option(&configured); err != nil {
			return nil, err
		}
	}
	if configured.idle == 0 {
		return nil, errors.New("runtime: a lazy service needs WithIdleTimeout")
	}
	lazy := &LazyService[T]{
		service: service,
		idle:    configured.idle,
		now:     configured.now,
		name:    configured.name,
		state:   make(chan *lazyState, 1),
	}
	// The one assertion: T may or may not report idleness of its own.
	if reporter, reports := IService(service).(IIdleReporter); reports {
		lazy.reporter = reporter
	}
	lazy.state <- &lazyState{}
	return lazy, nil
}

// Start records the scope the lazy service is mounted on. It starts nothing:
// T starts on the first [LazyService.Use], on a child of this scope, and the
// scope's cancellation stops it like any mounted service.
func (lazy *LazyService[T]) Start(scope *Scope) error {
	if scope == nil {
		return fmt.Errorf("runtime: lazy %s: nil scope", lazy.name)
	}
	state := <-lazy.state
	defer func() { lazy.state <- state }()
	if state.parent != nil {
		return fmt.Errorf("runtime: lazy %s: already started", lazy.name)
	}
	state.parent = scope
	return nil
}

// Use runs call with T, starting T first if it is not running, and holds a
// lease for the duration of the call so the run cannot be retired under it.
// The call's error is returned as is.
func (lazy *LazyService[T]) Use(ctx context.Context, call func(service T) error) error {
	if call == nil {
		return fmt.Errorf("runtime: lazy %s: nil call", lazy.name)
	}
	var state *lazyState
	select {
	case state = <-lazy.state:
	case <-ctx.Done():
		return ctx.Err()
	}
	err := lazy.lease(state)
	lazy.state <- state
	if err != nil {
		return err
	}
	defer lazy.release()
	return call(lazy.service)
}

// lease takes one lease, starting a run first if none is live.
func (lazy *LazyService[T]) lease(state *lazyState) error {
	if state.parent == nil {
		return fmt.Errorf("%w: %s", ErrLazyServiceNotStarted, lazy.name)
	}
	if state.parent.Context().Err() != nil {
		return fmt.Errorf("%w: %s: %w", ErrLazyServiceStopped, lazy.name, context.Cause(state.parent.Context()))
	}
	if state.child != nil && state.child.Context().Err() != nil && !state.retired {
		return fmt.Errorf("%w: %s: %w", ErrLazyServiceStopped, lazy.name, context.Cause(state.child.Context()))
	}
	if state.child == nil || state.retired {
		if err := lazy.run(state); err != nil {
			return err
		}
	}
	state.leases++
	return nil
}

// run starts T on a new child scope, after joining the retired one, together
// with the idle watch that will retire it.
func (lazy *LazyService[T]) run(state *lazyState) error {
	if state.child != nil {
		// T's contract: a new Start only after the previous scope joined.
		if err := state.child.Wait(); err != nil {
			return fmt.Errorf("runtime: lazy %s: previous run: %w", lazy.name, err)
		}
		state.child = nil
	}
	child, err := state.parent.Child(lazy.name)
	if err != nil {
		return fmt.Errorf("runtime: lazy %s: %w", lazy.name, err)
	}
	if err := lazy.service.Start(child); err != nil {
		startErr := fmt.Errorf("runtime: lazy %s: start: %w", lazy.name, err)
		if state.parent.Context().Err() != nil {
			// The parent stopped while T was starting; that is why it failed.
			startErr = fmt.Errorf("%w: %w", ErrLazyServiceStopped, startErr)
		}
		return errors.Join(startErr, child.Close())
	}
	if err := child.Go(lazy.watch); err != nil {
		return errors.Join(err, child.Close())
	}
	state.child = child
	state.retired = false
	state.lastRelease = lazy.now()
	return nil
}

// release returns one lease and restarts the idle measurement.
func (lazy *LazyService[T]) release() {
	state := <-lazy.state
	state.leases--
	state.lastRelease = lazy.now()
	lazy.state <- state
}

// watch runs on the child scope it retires: it wakes once per idle timeout,
// or sooner when less idle time remains, and cancels the child when nothing
// holds a lease and the service, if it reports, is idle.
func (lazy *LazyService[T]) watch(ctx context.Context) error {
	timer := time.NewTimer(lazy.idle)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
		wait, retired := lazy.retireIfIdle(ctx)
		if retired {
			return nil
		}
		timer.Reset(wait)
	}
}

// retireIfIdle cancels the current run if it is idle, or returns how long to
// wait before asking again.
func (lazy *LazyService[T]) retireIfIdle(ctx context.Context) (time.Duration, bool) {
	var state *lazyState
	select {
	case state = <-lazy.state:
	case <-ctx.Done():
		return 0, true
	}
	defer func() { lazy.state <- state }()
	if state.leases > 0 {
		return lazy.idle, false
	}
	idleFor := lazy.now().Sub(state.lastRelease)
	if idleFor < lazy.idle {
		return min(lazy.idle-idleFor, lazy.idle), false
	}
	if lazy.reporter != nil && !lazy.reporter.Idle() {
		return lazy.idle, false
	}
	state.retired = true
	state.child.Cancel()
	return 0, true
}
