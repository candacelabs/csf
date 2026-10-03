// Copyright 2026 Candace Labs

package runtime_test

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/goleak"
	"github.com/onsi/gomega/types"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/pkg/eventually"
	"github.com/candacelabs/csf/runtime"
)

const (
	// idleTimeout is short so retirement happens inside a spec; every wait
	// for it is an await with a generous budget, never a sleep.
	idleTimeout = 20 * time.Millisecond
	lazyName    = "worker"
	loopName    = "loop"
)

// heldBudget is how long a spec watches a run that must not retire: many
// idle timeouts, so a retirement that should not happen has every chance to.
var heldBudget = eventually.Budget{Within: 15 * idleTimeout, Interval: idleTimeout / 4}

// idleService is a wrapped service that also reports its own idleness.
type idleService struct {
	*MockIService
	*MockIIdleReporter
}

// runsUntilCanceled is a Start that records its scope and starts one owner
// goroutine that lasts the whole scope.
func runsUntilCanceled(scopes chan<- *runtime.Scope) func(scope *runtime.Scope) error {
	return func(scope *runtime.Scope) error {
		scopes <- scope
		return scope.GoOwner(loopName, func(ctx context.Context) error {
			<-ctx.Done()
			return nil
		})
	}
}

func live(scope *runtime.Scope) func() int64 { return scope.Live }

func isZero(count int64) bool { return count == 0 }

func isPositive(count int64) bool { return count > 0 }

func noop[T runtime.IService](_ T) error { return nil }

var _ = Describe("LazyService", func() {
	var (
		baseline   goleak.Option
		controller *gomock.Controller
		service    *MockIService
		parent     *runtime.Scope
		scopes     chan *runtime.Scope
		// parentEnds is what the parent's join must report; a spec whose
		// run fails on purpose replaces it.
		parentEnds types.GomegaMatcher
	)

	BeforeEach(func() {
		baseline = goleak.IgnoreCurrent()
		controller = gomock.NewController(GinkgoT())
		service = NewMockIService(controller)
		parent = runtime.NewScope(context.Background(), "parent")
		scopes = make(chan *runtime.Scope, 8)
		parentEnds = Succeed()
	})
	AfterEach(func() {
		Expect(parent.Close()).To(parentEnds)
		Expect(goleak.Find(baseline)).To(Succeed(), "every run a lazy service started must join")
	})

	mount := func(options ...runtime.LazyServiceOption) *runtime.LazyService[*MockIService] {
		lazy, err := runtime.NewLazyService(service,
			append([]runtime.LazyServiceOption{runtime.WithIdleTimeout(idleTimeout), runtime.WithLazyName(lazyName)}, options...)...)
		Expect(err).NotTo(HaveOccurred())
		Expect(lazy.Start(parent)).To(Succeed())
		return lazy
	}

	Context("construction and misuse", func() {
		It("rejects a missing, zero or negative idle timeout and invalid options", func() {
			_, err := runtime.NewLazyService(service)
			Expect(err).To(MatchError(ContainSubstring("WithIdleTimeout")))
			_, err = runtime.NewLazyService(service, runtime.WithIdleTimeout(0))
			Expect(err).To(MatchError(ContainSubstring("must be positive")))
			_, err = runtime.NewLazyService(service, runtime.WithIdleTimeout(-time.Second))
			Expect(err).To(MatchError(ContainSubstring("must be positive")))
			_, err = runtime.NewLazyService(service, runtime.WithIdleTimeout(time.Second), runtime.WithLazyClock(nil))
			Expect(err).To(MatchError(ContainSubstring("needs a clock")))
			_, err = runtime.NewLazyService(service, runtime.WithIdleTimeout(time.Second), runtime.WithLazyName(""))
			Expect(err).To(MatchError(ContainSubstring("needs a name")))
			_, err = runtime.NewLazyService(service, runtime.WithIdleTimeout(time.Second), nil)
			Expect(err).To(MatchError(ContainSubstring("option 1 is nil")))
			_, err = runtime.NewLazyService[runtime.IService](nil, runtime.WithIdleTimeout(time.Second))
			Expect(err).To(MatchError(ContainSubstring("needs a service")))
		})

		It("refuses a use before it is mounted, a second mount, a nil scope and a nil call", func() {
			lazy, err := runtime.NewLazyService(service, runtime.WithIdleTimeout(idleTimeout))
			Expect(err).NotTo(HaveOccurred())
			Expect(lazy.Use(context.Background(), noop)).To(MatchError(runtime.ErrLazyServiceNotStarted))
			Expect(lazy.Start(nil)).To(MatchError(ContainSubstring("nil scope")))
			Expect(lazy.Start(parent)).To(Succeed())
			Expect(lazy.Start(parent)).To(MatchError(ContainSubstring("already started")))
			Expect(lazy.Use(context.Background(), nil)).To(MatchError(ContainSubstring("nil call")))
			Expect(parent.Live()).To(BeZero(), "a refused use starts nothing")
		})
	})

	It("starts nothing at mount and starts the service on a child scope at first use", func() {
		lazy := mount()
		Expect(parent.Live()).To(BeZero(), "mounting only records the parent")
		service.EXPECT().Start(gomock.Any()).DoAndReturn(runsUntilCanceled(scopes)).Times(1)

		Expect(lazy.Use(context.Background(), func(used *MockIService) error {
			Expect(used).To(BeIdenticalTo(service))
			Expect(parent.Live()).To(BeNumerically(">", 0))
			return nil
		})).To(Succeed())
		run := <-scopes
		Expect(run.Owner()).To(Equal("parent/" + lazyName))
	})

	It("cancels and joins the run once idle, and restarts it on the next use", func() {
		lazy := mount()
		service.EXPECT().Start(gomock.Any()).DoAndReturn(runsUntilCanceled(scopes)).Times(2)

		Expect(lazy.Use(context.Background(), noop)).To(Succeed())
		first := <-scopes
		eventually.Await(GinkgoT(), "the idle run to join", joinBudget, live(parent), isZero)
		Expect(first.Context().Err()).To(MatchError(context.Canceled))
		Expect(first.Live()).To(BeZero())

		Expect(lazy.Use(context.Background(), noop)).To(Succeed())
		second := <-scopes
		Expect(second).NotTo(BeIdenticalTo(first), "a restart runs on a new scope")
		Expect(second.Context().Err()).NotTo(HaveOccurred())
		eventually.Await(GinkgoT(), "the restarted run to retire", joinBudget, live(parent), isZero)
	})

	It("never retires a run while a lease is held", func() {
		lazy := mount()
		service.EXPECT().Start(gomock.Any()).DoAndReturn(runsUntilCanceled(scopes)).Times(1)

		Expect(lazy.Use(context.Background(), func(_ *MockIService) error {
			eventually.Consistently(GinkgoT(), "the leased run to stay up", heldBudget, live(parent), isPositive)
			return nil
		})).To(Succeed())
		eventually.Await(GinkgoT(), "the run to retire once the lease is released", joinBudget, live(parent), isZero)
	})

	It("measures idleness on the injected clock", func() {
		var now atomic.Int64
		lazy := mount(runtime.WithLazyClock(func() time.Time { return time.Unix(0, now.Load()) }))
		service.EXPECT().Start(gomock.Any()).DoAndReturn(runsUntilCanceled(scopes)).Times(1)

		Expect(lazy.Use(context.Background(), noop)).To(Succeed())
		eventually.Consistently(GinkgoT(), "a frozen clock to hold the run", heldBudget, live(parent), isPositive)
		now.Add(int64(idleTimeout))
		eventually.Await(GinkgoT(), "the advanced clock to retire the run", joinBudget, live(parent), isZero)
	})

	It("asks a service that reports idleness before retiring it", func() {
		reporting := idleService{MockIService: service, MockIIdleReporter: NewMockIIdleReporter(controller)}
		var busy atomic.Bool
		busy.Store(true)
		reporting.MockIIdleReporter.EXPECT().Idle().DoAndReturn(func() bool { return !busy.Load() }).MinTimes(1)
		service.EXPECT().Start(gomock.Any()).DoAndReturn(runsUntilCanceled(scopes)).Times(1)
		lazy, err := runtime.NewLazyService(reporting, runtime.WithIdleTimeout(idleTimeout))
		Expect(err).NotTo(HaveOccurred())
		Expect(lazy.Start(parent)).To(Succeed())

		Expect(lazy.Use(context.Background(), noop[idleService])).To(Succeed())
		eventually.Consistently(GinkgoT(), "a busy service to stay up", heldBudget, live(parent), isPositive)
		busy.Store(false)
		eventually.Await(GinkgoT(), "the service to retire once it reports idle", joinBudget, live(parent), isZero)
	})

	It("stops with its parent, even during a use, and refuses later uses", func() {
		lazy := mount()
		service.EXPECT().Start(gomock.Any()).DoAndReturn(runsUntilCanceled(scopes)).Times(1)

		Expect(lazy.Use(context.Background(), func(_ *MockIService) error {
			run := <-scopes
			parent.Cancel()
			<-run.Done()
			return nil
		})).To(Succeed())
		Expect(parent.Wait()).To(Succeed())
		Expect(parent.Live()).To(BeZero())
		Expect(lazy.Use(context.Background(), noop)).To(MatchError(runtime.ErrLazyServiceStopped))
	})

	It("surfaces a start error at use time and retries on the next use", func() {
		lazy := mount()
		errRefused := errors.New("refused")
		gomock.InOrder(
			service.EXPECT().Start(gomock.Any()).Return(errRefused),
			service.EXPECT().Start(gomock.Any()).DoAndReturn(runsUntilCanceled(scopes)),
		)

		Expect(lazy.Use(context.Background(), noop)).To(MatchError(errRefused))
		Expect(lazy.Use(context.Background(), noop)).To(Succeed())
		Expect(parent.Context().Err()).NotTo(HaveOccurred(), "a refused start is the caller's error, not the parent's")
	})

	It("reports a run that failed on its own instead of restarting it", func() {
		lazy := mount()
		errBroke := errors.New("broke")
		breaking := make(chan struct{})
		service.EXPECT().Start(gomock.Any()).DoAndReturn(func(scope *runtime.Scope) error {
			return scope.Go(func(_ context.Context) error {
				<-breaking
				return errBroke
			})
		}).Times(1)

		parentEnds = MatchError(errBroke)
		Expect(lazy.Use(context.Background(), noop)).To(Succeed())
		close(breaking)
		Expect(parent.Wait()).To(MatchError(errBroke), "a failed run fails its parent")
		Expect(lazy.Use(context.Background(), noop)).To(MatchError(runtime.ErrLazyServiceStopped))
	})

	It("lets a caller waiting behind a slow start give up on its context", func() {
		lazy := mount()
		release := make(chan struct{})
		service.EXPECT().Start(gomock.Any()).DoAndReturn(func(scope *runtime.Scope) error {
			<-release
			return runsUntilCanceled(scopes)(scope)
		}).Times(1)
		first := make(chan error, 1)
		Expect(parent.Go(func(_ context.Context) error {
			first <- lazy.Use(context.Background(), noop)
			return nil
		})).To(Succeed())
		eventually.Await(GinkgoT(), "the first use to begin starting", joinBudget, live(parent), func(count int64) bool { return count >= 2 })

		waiting, cancel := context.WithCancel(context.Background())
		cancel()
		Expect(lazy.Use(waiting, noop)).To(MatchError(context.Canceled))
		close(release)
		Expect(<-first).To(Succeed(), "the slow start itself completes")
	})

	It("mounts into a host runtime like any service", func() {
		host, err := runtime.NewHostRuntime()
		Expect(err).NotTo(HaveOccurred())
		lazy, err := runtime.NewLazyService(service, runtime.WithIdleTimeout(idleTimeout))
		Expect(err).NotTo(HaveOccurred())
		Expect(host.Mount("lazy", lazy)).To(Succeed())
		service.EXPECT().Start(gomock.Any()).DoAndReturn(runsUntilCanceled(scopes)).Times(1)

		ctx, stop := context.WithCancel(context.Background())
		done := make(chan error, 1)
		Expect(parent.Go(func(_ context.Context) error { done <- host.Run(ctx); return nil })).To(Succeed())
		eventually.Await(GinkgoT(), "the host to be ready", joinBudget, host.Ready, func(ready bool) bool { return ready })
		Expect(lazy.Use(context.Background(), noop)).To(Succeed())
		stop()
		Expect(<-done).To(Succeed())
		Expect(lazy.Use(context.Background(), noop)).To(MatchError(runtime.ErrLazyServiceStopped))
	})
})
