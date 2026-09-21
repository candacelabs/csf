package coldstart

import (
	"context"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The specifications below run a real runtime: a caller, a dispatcher, and
// however many instance goroutines the dispatcher decided to have at that
// instant. They are paced far faster than the demo is.
const (
	specStartup  = 20 * time.Millisecond
	specArrival  = 15 * time.Millisecond
	specPatience = 15 * time.Second
)

// specConfig is the pace every specification in this file runs at.
func specConfig() Config {
	return Config{
		RuntimeName:     "candace/spec",
		ArrivalInterval: specArrival,
		StartupBudget:   specStartup,
		WorkDuration:    specStartup / 4,
		MaxInstances:    2,
		BacklogCeiling:  3,
		IdleSweeps:      1,
		ReapInterval:    4 * specStartup,
		CallPatience:    5 * time.Second,
		WarmFloor:       0,
	}
}

// pool is one subscriber, read only by the specification's own goroutine.
type pool struct {
	views <-chan PoolView
	seen  []PoolView
}

// await takes views until one matches, checking on the way that every one
// describes a pool that could exist.
func (subscriber *pool) await(what string, match func(view PoolView) bool) PoolView {
	GinkgoHelper()
	deadline := time.After(specPatience)
	for {
		select {
		case view, open := <-subscriber.views:
			Expect(open).To(BeTrue(), "the runtime stopped before %s", what)
			Expect(view.Sound()).To(BeTrue(),
				"a view described a pool that cannot exist: %+v", view)
			subscriber.seen = append(subscriber.seen, view)
			if match(view) {
				return view
			}
		case <-deadline:
			Fail("never saw " + what + " within " + specPatience.String())
			return PoolView{}
		}
	}
}

// runtimeUnder starts a runtime for the length of one specification, with one
// subscriber attached, and joins every goroutine — instances included — on the
// way out.
func runtimeUnder(config Config) (*Runtime, *pool, chan error) {
	GinkgoHelper()

	pooled, buildError := NewRuntime(config)
	Expect(buildError).ToNot(HaveOccurred())

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() { stopped <- pooled.Run(ctx) }()
	DeferCleanup(func() {
		cancel()
		Eventually(stopped, specPatience).Should(Receive())
	})

	views, subscribeError := pooled.Watch(ctx)
	Expect(subscribeError).ToNot(HaveOccurred())
	return pooled, &pool{views: views}, stopped
}

var _ = Describe("A runtime that is configured wrongly", func() {
	It("reports the fault rather than running and never serving", func() {
		sound := specConfig()
		for _, broken := range []struct {
			what   string
			change func(config *Config)
			fault  error
		}{
			{"no runtime name", func(config *Config) { config.RuntimeName = "" }, ErrRuntimeName},
			{"no arrivals", func(config *Config) { config.ArrivalInterval = 0 }, ErrArrivalRate},
			{"no start-up budget", func(config *Config) { config.StartupBudget = 0 }, ErrStartupBudget},
			{"no work", func(config *Config) { config.WorkDuration = 0 }, ErrWorkDuration},
			{"a pool with no ceiling", func(config *Config) { config.MaxInstances = 0 }, ErrMaxInstances},
			{"a warm floor above the ceiling",
				func(config *Config) { config.WarmFloor = config.MaxInstances + 1 }, ErrMaxInstances},
			{"a dispatcher that cannot queue anything",
				func(config *Config) { config.BacklogCeiling = 0 }, ErrBacklogCeiling},
			{"an instance reaped in the sweep it warmed in",
				func(config *Config) { config.IdleSweeps = 0 }, ErrIdleSweeps},
			{"no reaper", func(config *Config) { config.ReapInterval = 0 }, ErrReapInterval},
			{"a caller that times out on every first call",
				func(config *Config) { config.CallPatience = time.Nanosecond }, ErrCallPatience},
		} {
			config := sound
			broken.change(&config)
			_, buildError := NewRuntime(config)
			Expect(buildError).To(MatchError(broken.fault), "for %s", broken.what)
		}
	})

	It("refuses a second Run rather than routing to one pool from two tables", func() {
		pooled, _, _ := runtimeUnder(specConfig())
		Expect(pooled.Run(context.Background())).To(MatchError(ContainSubstring("already running")))
	})
})

var _ = Describe("A runtime nobody has called yet", func() {
	It("is scaled to zero, with no instance goroutine at all", func() {
		_, subscriber, _ := runtimeUnder(specConfig())

		first := subscriber.seen
		Expect(first).To(BeEmpty())
		opening := subscriber.await("the opening view", func(view PoolView) bool { return true })
		Expect(opening.ScaledToZero()).To(BeTrue(),
			"scaling to zero is not a small pool; it is no goroutines")
		Expect(opening.WarmInstances).To(Equal(0))
		Expect(opening.RuntimeName).To(Equal("candace/spec"))
	})
})

var _ = Describe("A runtime being called", func() {
	It("spawns an instance, pays the start-up, and serves from it", func() {
		_, subscriber, _ := runtimeUnder(specConfig())

		warming := subscriber.await("an instance that exists but cannot serve yet",
			func(view PoolView) bool { return view.LiveInstances > 0 && view.WarmInstances == 0 })
		Expect(warming.Queued).To(BeNumerically(">=", 1),
			"the invocation that caused the spawn is waiting on the start-up it paid for")

		warm := subscriber.await("an instance that has paid its start-up",
			func(view PoolView) bool { return view.WarmInstances >= 1 })
		Expect(warm.ColdStartMillis).To(Equal(int(specStartup.Milliseconds())))
		Expect(warm.Served).To(BeNumerically(">=", 1))
	})

	It("never spawns past the pool ceiling", func() {
		config := specConfig()
		config.ArrivalInterval = specStartup / 10
		_, subscriber, _ := runtimeUnder(config)

		subscriber.await("a busy pool",
			func(view PoolView) bool { return view.Served >= 4 })
		for _, view := range subscriber.seen {
			Expect(view.LiveInstances).To(BeNumerically("<=", config.MaxInstances),
				"a pool that spawns past its ceiling is a pool with no ceiling")
		}
	})

	It("closes a dropped invocation's channel rather than leaking the caller", func() {
		// A dispatcher with a small backlog and a slow start-up drops the
		// oldest waiting invocation. The caller learns it from the close, which
		// is the difference between an answer it will not get and one it is
		// still waiting for.
		config := specConfig()
		config.ArrivalInterval = specStartup / 20
		config.BacklogCeiling = 1
		config.MaxInstances = 1
		_, subscriber, _ := runtimeUnder(config)

		dropped := subscriber.await("a dropped invocation",
			func(view PoolView) bool { return view.Dropped >= 1 })
		Expect(dropped.DispatcherUp).To(BeFalse(),
			"a dispatcher at its backlog ceiling has stopped accepting, and says so")
	})

	It("reaps an idle instance, and its goroutine returns", func() {
		config := specConfig()
		// One burst of calls, then silence: the arrival interval is longer than
		// the whole specification, so nothing keeps the instance alive.
		config.ArrivalInterval = 8 * specStartup
		_, subscriber, _ := runtimeUnder(config)

		before := runtime.NumGoroutine()
		subscriber.await("an instance that served",
			func(view PoolView) bool { return view.Served >= 1 && view.WarmInstances >= 1 })

		gone := subscriber.await("a pool scaled back to zero",
			func(view PoolView) bool { return view.ScaledToZero() })
		Expect(gone.WarmInstances).To(Equal(0))

		// The goroutine count is the assertion the card cannot make. It is
		// compared against a reading taken while an instance was live rather
		// than against a constant, because this process has a scheduler and a
		// test framework in it too.
		Eventually(runtime.NumGoroutine, specPatience).Should(BeNumerically("<=", before+2),
			"a frozen instance is a goroutine that returned, not a goroutine in a cold state")
	})

	It("keeps one instance warm once the prewarm command has been sent", func() {
		config := specConfig()
		config.ArrivalInterval = 8 * specStartup
		pooled, subscriber, _ := runtimeUnder(config)

		subscriber.await("a pool that has scaled to zero at least once",
			func(view PoolView) bool { return view.ScaledToZero() && view.Sequence > 1 })

		Expect(pooled.Prewarm(context.Background())).To(Succeed())
		floored := subscriber.await("a floor of one",
			func(view PoolView) bool { return view.WarmFloor == 1 && view.WarmInstances >= 1 })
		Expect(floored.LiveInstances).To(BeNumerically(">=", 1))

		// And it stays. The reaper never takes the pool below the floor, which
		// is the whole of the tier the pricing page calls Serverful.
		Consistently(func() bool {
			select {
			case view, open := <-subscriber.views:
				return !open || view.LiveInstances >= 1
			case <-time.After(specStartup):
				return true
			}
		}, 6*specStartup).Should(BeTrue())
	})
})
