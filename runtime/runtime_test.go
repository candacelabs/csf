// Copyright 2026 Candace Labs

package runtime_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/goleak"

	"github.com/candacelabs/csf/pkg/eventually"
	"github.com/candacelabs/csf/runtime"
)

// joinBudget bounds how long a spec waits for goroutines to observe a
// cancellation; generous because the costs are not symmetric (CS-9).
var joinBudget = eventually.Budget{Within: 5 * time.Second}

var errBroken = errors.New("broken")

// recorder is a channel-backed event log: each service and goroutine sends
// what happened, in the order it happened, to one buffered channel.
type recorder chan string

func (events recorder) drain() []string {
	var seen []string
	for {
		select {
		case event := <-events:
			seen = append(seen, event)
		default:
			return seen
		}
	}
}

// ordered is a service that records its start and, from its owning
// goroutine, its stop.
func ordered(events recorder, name string) runtime.ServiceFunc {
	return runtime.ServiceFunc(func(scope *runtime.Scope) error {
		events <- "start " + name
		return scope.GoOwner(name, func(ctx context.Context) error {
			<-ctx.Done()
			events <- "stop " + name
			return nil
		})
	})
}

var _ = Describe("Scope", func() {
	var baseline goleak.Option

	BeforeEach(func() { baseline = goleak.IgnoreCurrent() })
	AfterEach(func() {
		Expect(goleak.Find(baseline)).To(Succeed(), "every goroutine a scope started must join")
	})

	It("joins every goroutine, children included, and reports how many it started", func() {
		scope := runtime.NewScope(context.Background(), "parent")
		child, err := scope.Child("child")
		Expect(err).NotTo(HaveOccurred())
		Expect(child.Owner()).To(Equal("parent/child"))
		for range 3 {
			Expect(scope.Go(func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })).To(Succeed())
			Expect(child.Go(func(ctx context.Context) error { <-ctx.Done(); return nil })).To(Succeed())
		}

		Expect(scope.Close()).To(Succeed(), "context.Canceled after cancellation is not a failure")
		Expect(scope.Goroutines()).To(Equal(int64(4)), "three tasks plus the child's join")
		Expect(child.Goroutines()).To(Equal(int64(3)))
		Expect(child.Context().Err()).To(MatchError(context.Canceled))
	})

	It("collects every error rather than the first, and a failure cancels the scope", func() {
		scope := runtime.NewScope(context.Background(), "collector")
		first, second := errors.New("first"), errors.New("second")
		failed := make(chan struct{})
		Expect(scope.Go(func(_ context.Context) error { defer close(failed); return first })).To(Succeed())
		Expect(scope.Go(func(ctx context.Context) error { <-failed; <-ctx.Done(); return second })).To(Succeed())

		err := scope.Wait()
		Expect(err).To(MatchError(first))
		Expect(err).To(MatchError(second), "the second goroutine's error is collected too")
		Expect(context.Cause(scope.Context())).To(MatchError(first))
	})

	It("fails an owner that stops before its scope is canceled", func() {
		scope := runtime.NewScope(context.Background(), "watcher-host")
		Expect(scope.GoOwner("watcher", func(_ context.Context) error { return nil })).To(Succeed())
		Expect(scope.Wait()).To(MatchError(ContainSubstring("watcher stopped before its scope was canceled")))
	})

	It("does not fail an owner that returns once a callback on its scope's context releases it", func() {
		// context.AfterFunc callbacks and the goroutines' own context are
		// siblings under the scope's context, and cancellation reaches them in
		// no fixed order. Many callbacks keep that notification running long
		// enough for an owner released by one of them to return before its own
		// context is told. The scope was canceled before any callback ran, so
		// that return is after the cancellation and not a failure.
		const rounds, bystanders = 50, 2000
		for range rounds {
			scope := runtime.NewScope(context.Background(), "watched")
			released := make(chan struct{})
			for range bystanders {
				context.AfterFunc(scope.Context(), func() {})
			}
			context.AfterFunc(scope.Context(), func() { close(released) })
			Expect(scope.GoOwner("owner", func(_ context.Context) error {
				<-released
				return nil
			})).To(Succeed())
			Expect(scope.Close()).To(Succeed())
		}
	})

	It("relays a child's failure to its parent", func() {
		scope := runtime.NewScope(context.Background(), "parent")
		child, err := scope.Child("child")
		Expect(err).NotTo(HaveOccurred())
		Expect(child.Go(func(_ context.Context) error { return errBroken })).To(Succeed())
		Expect(scope.Go(func(ctx context.Context) error { <-ctx.Done(); return nil })).To(Succeed())

		Expect(scope.Wait()).To(MatchError(errBroken))
		Expect(context.Cause(scope.Context())).To(MatchError(errBroken))
	})

	It("admits a goroutine only while its join can still see it, under a Go-versus-Close storm", func() {
		const rounds, spawners, attempts = 100, 8, 64
		for range rounds {
			scope := runtime.NewScope(context.Background(), "storm")
			var admitted, finished atomic.Int64
			start := make(chan struct{})
			// The spawners are the racing callers under test, outside any
			// scope on purpose; a WaitGroup is the plain way to join them.
			var callers sync.WaitGroup
			for range spawners {
				callers.Add(1)
				go func() {
					defer callers.Done()
					<-start
					for range attempts {
						if scope.Go(func(_ context.Context) error { finished.Add(1); return nil }) == nil {
							admitted.Add(1)
						}
					}
				}()
			}
			close(start)
			Expect(scope.Close()).To(Succeed())
			joined := finished.Load()
			callers.Wait()
			Expect(joined).To(Equal(admitted.Load()), "every admitted goroutine must have finished when Close returned")
			Expect(finished.Load()).To(Equal(joined), "no goroutine may be admitted after the join began")
		}
	})

	It("refuses new goroutines and children once it is joining", func() {
		scope := runtime.NewScope(context.Background(), "closed")
		Expect(scope.Close()).To(Succeed())
		Expect(scope.Go(func(_ context.Context) error { return nil })).To(MatchError(runtime.ErrScopeClosed))
		_, err := scope.Child("late")
		Expect(err).To(MatchError(runtime.ErrScopeClosed))
		Expect(scope.Go(nil)).To(MatchError(ContainSubstring("nil goroutine")))
	})
})

var _ = Describe("HostRuntime", func() {
	var baseline goleak.Option

	BeforeEach(func() { baseline = goleak.IgnoreCurrent() })
	AfterEach(func() {
		Expect(goleak.Find(baseline)).To(Succeed(), "every goroutine a service started must join on shutdown")
	})

	It("starts services in mount order and stops them in reverse", func() {
		events := make(recorder, 16)
		host, err := runtime.NewHostRuntime(runtime.WithHostName("test-host"))
		Expect(err).NotTo(HaveOccurred())
		for _, name := range []string{"feed", "watcher", "web"} {
			Expect(host.Mount(name, ordered(events, name))).To(Succeed())
		}

		ctx, stop := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- host.Run(ctx) }()
		eventually.Await(GinkgoTB(), "the host to report ready", joinBudget, host.Ready, func(ready bool) bool { return ready })
		Expect(host.Mount("late", ordered(events, "late"))).To(MatchError(ContainSubstring("after Run")))

		stop()
		Eventually(done).WithTimeout(joinBudget.Within).Should(Receive(Succeed()))
		Expect(host.Ready()).To(BeFalse())
		Expect(events.drain()).To(Equal([]string{
			"start feed", "start watcher", "start web",
			"stop web", "stop watcher", "stop feed",
		}))
	})

	It("shuts everything down when one service fails, and returns that failure", func() {
		events := make(recorder, 16)
		host, err := runtime.NewHostRuntime()
		Expect(err).NotTo(HaveOccurred())
		Expect(host.Mount("steady", ordered(events, "steady"))).To(Succeed())
		Expect(host.Mount("fragile", runtime.ServiceFunc(func(scope *runtime.Scope) error {
			return scope.GoOwner("loop", func(_ context.Context) error { return errBroken })
		}))).To(Succeed())

		Expect(host.Run(context.Background())).To(MatchError(errBroken))
		Expect(events.drain()).To(Equal([]string{"start steady", "stop steady"}))
	})

	It("unwinds the services already started when a later one fails to start", func() {
		events := make(recorder, 16)
		host, err := runtime.NewHostRuntime()
		Expect(err).NotTo(HaveOccurred())
		Expect(host.Mount("first", ordered(events, "first"))).To(Succeed())
		Expect(host.Mount("refuses", runtime.ServiceFunc(func(_ *runtime.Scope) error { return errBroken }))).To(Succeed())
		Expect(host.Mount("never", ordered(events, "never"))).To(Succeed())

		Expect(host.Run(context.Background())).To(MatchError(errBroken))
		Expect(events.drain()).To(Equal([]string{"start first", "stop first"}))
	})

	// peersCanceled runs three services whose owners, once canceled, report
	// whether every peer's scope was canceled too. With awaitPeers false an
	// owner looks once, at the moment it wakes: under the reverse order the
	// first service to stop sees its dependencies still running. With
	// awaitPeers true an owner refuses to return until every peer has been
	// canceled, which only an order that cancels every scope before joining
	// any can satisfy; under the reverse order it would never return.
	peersCanceled := func(order runtime.ShutdownOrder, awaitPeers bool) []bool {
		GinkgoHelper()
		host, err := runtime.NewHostRuntime(runtime.WithShutdownOrder(order))
		Expect(err).NotTo(HaveOccurred())
		// Every slot is written by Run before it cancels any scope, and the
		// close of a scope's Done orders those writes before each read.
		var scopes [3]*runtime.Scope
		observed := make(chan bool, 3)
		for index, name := range []string{"feed", "watcher", "web"} {
			Expect(host.Mount(name, runtime.ServiceFunc(func(scope *runtime.Scope) error {
				scopes[index] = scope
				return scope.GoOwner(name, func(ctx context.Context) error {
					<-ctx.Done()
					if awaitPeers {
						for _, peer := range scopes {
							<-peer.Done()
						}
					}
					all := true
					for _, peer := range scopes {
						all = all && peer.Context().Err() != nil
					}
					observed <- all
					return nil
				})
			}))).To(Succeed())
		}
		ctx, stop := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- host.Run(ctx) }()
		eventually.Await(GinkgoTB(), "the host to report ready", joinBudget, host.Ready, func(ready bool) bool { return ready })
		stop()
		Eventually(done).WithTimeout(joinBudget.Within).Should(Receive(Succeed()))
		return []bool{<-observed, <-observed, <-observed}
	}

	It("by default stops a service while the services it depends on still run", func() {
		Expect(peersCanceled(runtime.ReverseMountOrder, false)).To(ContainElement(false))
	})

	It("with the concurrent order cancels every service before joining any", func() {
		Expect(peersCanceled(runtime.Concurrent, true)).To(Equal([]bool{true, true, true}))
	})

	It("collects every service's error under the concurrent order", func() {
		host, err := runtime.NewHostRuntime(runtime.WithShutdownOrder(runtime.Concurrent))
		Expect(err).NotTo(HaveOccurred())
		first, second := errors.New("first drain"), errors.New("second drain")
		for name, failure := range map[string]error{"one": first, "two": second} {
			Expect(host.Mount(name, runtime.ServiceFunc(func(scope *runtime.Scope) error {
				return scope.Go(func(ctx context.Context) error { <-ctx.Done(); return failure })
			}))).To(Succeed())
		}
		ctx, stop := context.WithCancel(context.Background())
		stop()
		err = host.Run(ctx)
		Expect(err).To(MatchError(first))
		Expect(err).To(MatchError(second))
	})

	It("rejects an unknown shutdown order", func() {
		_, err := runtime.NewHostRuntime(runtime.WithShutdownOrder(runtime.ShutdownOrder(99)))
		Expect(err).To(MatchError(ContainSubstring("unknown shutdown order")))
	})

	It("keeps Mount and Run apart when they race", func() {
		for range 50 {
			host, err := runtime.NewHostRuntime()
			Expect(err).NotTo(HaveOccurred())
			ctx, stop := context.WithCancel(context.Background())
			stop()
			mounted := make(chan error, 1)
			go func() { mounted <- host.Mount("late", ordered(make(recorder, 2), "late")) }()
			Expect(host.Run(ctx)).To(Succeed())
			Eventually(mounted).WithTimeout(joinBudget.Within).Should(Receive(Or(Succeed(), MatchError(ContainSubstring("after Run")))))
		}
	})

	It("rejects ambiguous mounts and a second Run", func() {
		host, err := runtime.NewHostRuntime()
		Expect(err).NotTo(HaveOccurred())
		Expect(host.Mount("", ordered(make(recorder, 1), "x"))).NotTo(Succeed())
		Expect(host.Mount("x", nil)).NotTo(Succeed())
		Expect(host.Mount("x", ordered(make(recorder, 2), "x"))).To(Succeed())
		Expect(host.Mount("x", ordered(make(recorder, 2), "x"))).To(MatchError(ContainSubstring("already mounted")))

		ctx, stop := context.WithCancel(context.Background())
		stop()
		Expect(host.Run(ctx)).To(Succeed())
		Expect(host.Run(ctx)).To(MatchError(ContainSubstring("twice")))
		_, err = runtime.NewHostRuntime(runtime.WithHostName(""))
		Expect(err).To(HaveOccurred())
		fresh, err := runtime.NewHostRuntime()
		Expect(err).NotTo(HaveOccurred())
		var missing context.Context
		Expect(fresh.Run(missing)).To(MatchError(ContainSubstring("context is nil")))
	})
})
