// Copyright 2026 Candace Labs

package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/sourcegraph/conc/pool"
)

// IService is what mounts into a [HostRuntime]. Start runs once, in mount
// order, and receives the service's own scope: every goroutine the service
// needs is started through that scope, and Start returns as soon as they are
// started. The service stops when its scope is canceled, and performs its own
// cleanup before its goroutines return.
type IService interface {
	Start(scope *Scope) error
}

// ServiceFunc adapts a function to [IService].
type ServiceFunc func(scope *Scope) error

// Start calls the function.
func (start ServiceFunc) Start(scope *Scope) error { return start(scope) }

// HostOption configures a [HostRuntime].
type HostOption func(options *hostOptions)

type hostOptions struct {
	name   string
	logger *slog.Logger
	order  ShutdownOrder
}

// ShutdownOrder is how a [HostRuntime] stops its services. Startup is always
// the mount order; only the stop is a choice.
type ShutdownOrder int

const (
	// ReverseMountOrder, the default, cancels and joins one service at a time,
	// last mounted first, so every dependency outlives what uses it.
	ReverseMountOrder ShutdownOrder = iota
	// Concurrent cancels every service's scope at once and joins them all.
	// It is for services with no shutdown dependencies on one another, where
	// the slowest drain, not the sum of them, should bound the stop.
	Concurrent
)

// shutdowns is the registry of stop strategies: each cancels and joins every
// started scope and returns their collected errors.
var shutdowns = map[ShutdownOrder]func(host *HostRuntime, started []*Scope) []error{
	ReverseMountOrder: stopInReverse,
	Concurrent:        stopConcurrently,
}

// WithShutdownOrder chooses how services stop. Unknown orders are rejected by
// [NewHostRuntime].
func WithShutdownOrder(order ShutdownOrder) HostOption {
	return func(options *hostOptions) { options.order = order }
}

const defaultHostName = "host"

// WithHostName names the runtime in its lifecycle logs.
func WithHostName(name string) HostOption {
	return func(options *hostOptions) { options.name = name }
}

// WithLogger receives one structured record per lifecycle transition: each
// service started, shutdown requested, each service joined.
func WithLogger(logger *slog.Logger) HostOption {
	return func(options *hostOptions) { options.logger = logger }
}

type mount struct {
	name    string
	service IService
}

// HostRuntime is the one runtime of one process. Its main function builds it,
// grants capabilities to the services it constructs, mounts them, and calls
// [HostRuntime.Run] with the root context it owns.
//
// Startup is ordered: services start in the order they were mounted, so a
// service may depend on everything mounted before it. Shutdown is, by default,
// the reverse: the last service mounted is canceled and joined first, so a
// dependency outlives everything that uses it ([ReverseMountOrder]);
// [WithShutdownOrder] can choose [Concurrent] instead. A mounted service's
// scope is not canceled by the root context directly — the runtime cancels the
// scopes itself — which is what makes the chosen order real rather than a race.
type HostRuntime struct {
	name   string
	logger *slog.Logger
	order  ShutdownOrder
	// mounting guards mounts and running: Mount appends only before Run, and
	// Run takes its copy of the mounts in the same critical section that marks
	// it running. A leaf critical section (CS-5): nothing waits under it.
	mounting sync.Mutex
	mounts   []mount
	running  bool
	ready    atomic.Bool
}

// NewHostRuntime validates its options and returns an empty runtime.
func NewHostRuntime(options ...HostOption) (*HostRuntime, error) {
	configured := hostOptions{name: defaultHostName}
	for _, option := range options {
		if option != nil {
			option(&configured)
		}
	}
	if configured.name == "" {
		return nil, errors.New("runtime: host name is empty")
	}
	if _, known := shutdowns[configured.order]; !known {
		return nil, fmt.Errorf("runtime: unknown shutdown order %d", configured.order)
	}
	if configured.logger == nil {
		configured.logger = slog.New(slog.DiscardHandler)
	}
	return &HostRuntime{name: configured.name, logger: configured.logger, order: configured.order}, nil
}

// Mount adds a service. It must be called before Run; the mount order is the
// startup order and, by default, the reverse of the shutdown order.
func (host *HostRuntime) Mount(name string, service IService) error {
	host.mounting.Lock()
	defer host.mounting.Unlock()
	if host.running {
		return fmt.Errorf("runtime: %s: cannot mount %q after Run", host.name, name)
	}
	if name == "" || service == nil {
		return fmt.Errorf("runtime: %s: a mount needs a name and a service", host.name)
	}
	for _, existing := range host.mounts {
		if existing.name == name {
			return fmt.Errorf("runtime: %s: %q is already mounted", host.name, name)
		}
	}
	host.mounts = append(host.mounts, mount{name: name, service: service})
	return nil
}

// Ready reports whether every service has started and shutdown has not begun.
// It is the readiness probe's answer.
func (host *HostRuntime) Ready() bool { return host.ready.Load() }

// Run starts every mounted service in order, then blocks until ctx is canceled
// or a service fails, then stops every started service in reverse order and
// joins it. It returns the services' collected errors; a clean shutdown
// returns nil. Run may be called once.
func (host *HostRuntime) Run(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("runtime: %s: context is nil", host.name)
	}
	host.mounting.Lock()
	if host.running {
		host.mounting.Unlock()
		return fmt.Errorf("runtime: %s: Run called twice", host.name)
	}
	host.running = true
	mounts := append([]mount(nil), host.mounts...)
	host.mounting.Unlock()
	// A failing service cancels this context with its error as the cause; the
	// first cause wins, by construction of context.WithCancelCause.
	running, fail := context.WithCancelCause(ctx)
	defer fail(nil)
	// Scopes keep the root's values but not its cancellation: the runtime
	// cancels them itself, one at a time, in reverse order.
	base := context.WithoutCancel(ctx)
	started := make([]*Scope, 0, len(mounts))
	for _, mounted := range mounts {
		scope := newScope(base, mounted.name, fail)
		if err := mounted.service.Start(scope); err != nil {
			startErr := fmt.Errorf("runtime: %s: start %s: %w", host.name, mounted.name, err)
			host.logger.Error("service failed to start", "host", host.name, "service", mounted.name, "error", err)
			return errors.Join(startErr, host.shutdown(append(started, scope)))
		}
		host.logger.Info("service started", "host", host.name, "service", mounted.name, "goroutines", scope.Goroutines())
		started = append(started, scope)
	}
	host.ready.Store(true)
	host.logger.Info("host runtime ready", "host", host.name, "services", len(started))

	<-running.Done()
	// The root's own cancellation (a signal, the caller) is a requested
	// shutdown whatever cause it carries; anything else is a service failure.
	if cause := context.Cause(running); ctx.Err() != nil || errors.Is(cause, context.Canceled) {
		host.logger.Info("shutdown requested", "host", host.name, "cause", cause.Error())
	} else {
		host.logger.Error("service failed; shutting down", "host", host.name, "error", cause)
	}
	return host.shutdown(started)
}

// shutdown stops every started scope in the configured order.
func (host *HostRuntime) shutdown(started []*Scope) error {
	host.ready.Store(false)
	failures := shutdowns[host.order](host, started)
	host.logger.Info("host runtime stopped", "host", host.name, "clean", len(failures) == 0)
	return errors.Join(failures...)
}

// stop cancels and joins one scope and logs what it joined.
func (host *HostRuntime) stop(scope *Scope) error {
	err := scope.Close()
	host.logger.Info("service stopped", "host", host.name, "service", scope.Owner(),
		"goroutines_joined", scope.Goroutines(), "clean", err == nil)
	return err
}

// stopInReverse cancels and joins each scope, last started first.
func stopInReverse(host *HostRuntime, started []*Scope) []error {
	var failures []error
	for index := len(started) - 1; index >= 0; index-- {
		if err := host.stop(started[index]); err != nil {
			failures = append(failures, err)
		}
	}
	return failures
}

// stopConcurrently cancels every scope first, then joins them all on a conc
// error pool, which collects every error rather than the first.
func stopConcurrently(host *HostRuntime, started []*Scope) []error {
	for _, scope := range started {
		scope.Cancel()
	}
	joins := pool.New().WithErrors()
	for _, scope := range started {
		joins.Go(func() error { return host.stop(scope) })
	}
	if err := joins.Wait(); err != nil {
		return []error{err}
	}
	return nil
}
