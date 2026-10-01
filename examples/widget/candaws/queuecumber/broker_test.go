package queuecumber

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/examples/widget/candaws/fleet"
)

// The specifications below run a real broker: a producer, N workers, an expiry
// sweep and a fan-out, against real timers. They are paced far faster than the
// demo is, and the two rates that matter are the two ends of the drop rate: 0 is
// a queue that delivers exactly once and 1 is a queue that delivers forever,
// and the difference between them is the whole product.
const (
	specSweep    = 15 * time.Millisecond
	specWork     = 5 * time.Millisecond
	specCadence  = 10 * time.Millisecond
	specPatience = 15 * time.Second
)

// specConfig is the pace every specification in this file runs at.
func specConfig(dropRate float64) Config {
	return Config{
		Workers:          3,
		Cadence:          specCadence,
		WorkDuration:     specWork,
		SweepInterval:    specSweep,
		VisibilitySweeps: 2,
		Capacity:         16,
		AttemptCeiling:   2,
		DropRate:         dropRate,
		Seed:             20260902,
	}
}

// watcher is one subscriber, read only by the specification's own goroutine. It
// keeps every view, because the conservation law is a claim about all of them
// rather than about any one.
type watcher struct {
	views <-chan BrokerView
	seen  []BrokerView
}

// await takes views until one matches, checking the conservation law on every
// one it passes — so a broker that loses a message fails at the view that lost
// it rather than at whatever assertion happens to run next.
func (subscriber *watcher) await(what string, match func(view BrokerView) bool) BrokerView {
	GinkgoHelper()
	deadline := time.After(specPatience)
	for {
		select {
		case view, open := <-subscriber.views:
			Expect(open).To(BeTrue(), "the broker stopped before %s", what)
			Expect(view.Conserved()).To(BeTrue(),
				"a message stopped being ready, leased or dead-lettered without being "+
					"acknowledged: %+v", view)
			subscriber.seen = append(subscriber.seen, view)
			if match(view) {
				return view
			}
		case <-deadline:
			Fail("never saw " + what + " within " + specPatience.String())
			return BrokerView{}
		}
	}
}

// brokerUnder starts a broker for the length of one specification, with one
// subscriber attached, and joins its goroutines on the way out.
func brokerUnder(config Config) (*Broker, *watcher) {
	GinkgoHelper()

	broker, buildError := NewBroker(config)
	Expect(buildError).ToNot(HaveOccurred())

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() { stopped <- broker.Run(ctx) }()
	DeferCleanup(func() {
		cancel()
		Eventually(stopped, specPatience).Should(Receive())
	})

	views, subscribeError := broker.Watch(ctx)
	Expect(subscribeError).ToNot(HaveOccurred())
	return broker, &watcher{views: views}
}

// brokerServing starts one broker's fan-out and its serve goroutine and nothing
// else — no producer, no expiry sweep, no workers — with a subscriber attached
// before serve can publish anything.
//
// It exists because "the broker's first view" and "the first view a subscriber
// received" are the same view in this order and in no other. [fleet.Feed] hands
// a subscriber that arrives between two views the current one, which is right
// for a browser and useless for a claim about an opening state: attached to a
// broker that is already running, a subscriber's first view is a fact about the
// scheduler. Subscribing while the feed is running and serve is not makes the
// two the same view every time — [fleet.Feed.Subscribe] returns only once the
// feed holds the subscription, and at that instant nothing has been published.
//
// The other thing it buys is a broker nothing else is talking to, so a
// specification can be the producer and the worker itself and watch what the
// broker does with exactly the messages it sent.
func brokerServing(config Config) (*Broker, *watcher) {
	GinkgoHelper()

	broker, buildError := NewBroker(config)
	Expect(buildError).ToNot(HaveOccurred())

	ctx, cancel := context.WithCancel(context.Background())
	var crew fleet.Crew
	crew.Go(ctx, broker.views.Run)

	views, subscribeError := broker.Watch(ctx)
	Expect(subscribeError).ToNot(HaveOccurred())

	crew.Go(ctx, broker.serve)

	joined := make(chan struct{})
	go func() {
		crew.Wait()
		close(joined)
	}()
	DeferCleanup(func() {
		cancel()
		Eventually(joined, specPatience).Should(BeClosed())
	})
	return broker, &watcher{views: views}
}

var _ = Describe("A broker that is configured wrongly", func() {
	It("reports the fault rather than running and never delivering", func() {
		sound := specConfig(0)
		for _, broken := range []struct {
			what   string
			change func(config *Config)
			fault  error
		}{
			{"no workers", func(config *Config) { config.Workers = 0 }, ErrWorkerCount},
			{"more workers than the set can hold",
				func(config *Config) { config.Workers = maxWorkers + 1 }, ErrWorkerCount},
			{"no cadence", func(config *Config) { config.Cadence = 0 }, ErrCadence},
			{"no sweep", func(config *Config) { config.SweepInterval = 0 }, ErrSweep},
			{"a lease that expires while its worker still holds it",
				func(config *Config) { config.VisibilitySweeps = 1 }, ErrVisibility},
			{"a queue that holds nothing", func(config *Config) { config.Capacity = 0 }, ErrCapacity},
			{"a message that may be tried zero times",
				func(config *Config) { config.AttemptCeiling = 0 }, ErrAttemptCeiling},
			{"a drop rate that is not a probability",
				func(config *Config) { config.DropRate = 2 }, ErrDropRate},
		} {
			config := sound
			broken.change(&config)
			_, buildError := NewBroker(config)
			Expect(buildError).To(MatchError(broken.fault), "for %s", broken.what)
		}
	})

	It("refuses a second Run rather than serving one queue from two brokers", func() {
		broker, _ := brokerUnder(specConfig(0))
		Expect(broker.Run(context.Background())).To(MatchError(ContainSubstring("already running")))
	})
})

var _ = Describe("A broker whose workers acknowledge", func() {
	It("delivers each message exactly once and redelivers nothing", func() {
		_, subscriber := brokerUnder(specConfig(0))

		settled := subscriber.await("a handful of acknowledged messages",
			func(view BrokerView) bool { return view.Acknowledged >= 5 })

		Expect(settled.Redelivered).To(Equal(uint64(0)),
			"nothing was dropped, so nothing came back: at-least-once is not at-least-twice")
		Expect(settled.DeadLettered).To(Equal(0))
		Expect(settled.Acknowledged).To(BeNumerically("<=", settled.Submitted),
			"a broker cannot acknowledge a message nobody submitted")
	})

	It("opens with a view in which nobody has asked for anything", func() {
		// A broker is told nothing about its workers, so "no workers leasing"
		// is the honest first thing the card can say. That is a claim about the
		// broker's own first view, and reaching it through a subscriber
		// attached to a running broker was reaching for it through a fan-out
		// that promises a late arrival the *current* view — so the assertion
		// held whenever the workers had not been scheduled yet and failed
		// about one run in fifteen when they had. brokerServing subscribes
		// first and the view stops being a race.
		_, subscriber := brokerServing(specConfig(0))

		opening := subscriber.await("the first view of all",
			func(view BrokerView) bool { return true })
		Expect(opening.Sequence).To(Equal(uint64(1)),
			"the subscriber was attached before serve started, so its first view is the broker's")
		Expect(opening.WorkersUp).To(Equal(0))
		Expect(opening.Depth).To(Equal(0))
		Expect(opening.InFlight).To(Equal(0))
		Expect(opening.DeadLettered).To(Equal(0))
		Expect(opening.Accepting).To(BeTrue(),
			"a queue with nothing in it has room, and says so before anybody submits")
	})

	It("learns its pool size from being asked for work", func() {
		_, subscriber := brokerUnder(specConfig(0))

		staffed := subscriber.await("every worker leasing",
			func(view BrokerView) bool { return view.WorkersUp == 3 })
		Expect(staffed.WorkersUp).To(Equal(3))

		// Learning is one way and it is bounded: a broker counts the workers
		// that have asked it for work, so the count climbs to the pool size and
		// never past it, and no view ever un-learns a worker.
		learned := 0
		for _, view := range subscriber.seen {
			Expect(view.WorkersUp).To(BeNumerically(">=", learned),
				"a broker does not forget a worker that asked: %+v", view)
			Expect(view.WorkersUp).To(BeNumerically("<=", 3),
				"nothing tells a broker its pool size, so it cannot count past what asked: %+v", view)
			learned = view.WorkersUp
		}
	})
})

var _ = Describe("A broker whose workers drop everything", func() {
	It("redelivers rather than losing, and dead-letters rather than dropping", func() {
		// Every worker finishes and says nothing. A message is therefore leased,
		// expires, is made ready again with its attempt count raised, is leased
		// again, and at the ceiling goes to the store — and at no instant is it
		// in none of the three places, which every view above has already been
		// checked for.
		_, subscriber := brokerUnder(specConfig(1))

		redelivered := subscriber.await("a redelivered message",
			func(view BrokerView) bool { return view.Redelivered >= 1 })
		Expect(redelivered.Acknowledged).To(Equal(uint64(0)),
			"nobody acknowledged anything, and the queue still has every message")

		exhausted := subscriber.await("a dead-lettered message",
			func(view BrokerView) bool { return view.DeadLettered >= 1 })
		Expect(exhausted.Acknowledged).To(Equal(uint64(0)))
		Expect(int(exhausted.Submitted)).To(Equal(
			exhausted.Depth+exhausted.InFlight+exhausted.DeadLettered),
			"with nothing acknowledged, every message ever accepted is still somewhere")
	})

	It("moves the dead-letter store back to the queue on a redrive", func() {
		config := specConfig(1)
		config.AttemptCeiling = 1
		broker, subscriber := brokerUnder(config)

		subscriber.await("a few dead letters",
			func(view BrokerView) bool { return view.DeadLettered >= 3 })

		Expect(broker.Redrive(context.Background())).To(Succeed())
		drained := subscriber.await("the store emptied",
			func(view BrokerView) bool { return view.DeadLettered == 0 })
		Expect(drained.Conserved()).To(BeTrue(),
			"a redrive moves messages between two of the three places and creates none")
	})
})

var _ = Describe("A queue that fills", func() {
	It("closes the intake and counts what it refused", func() {
		// One worker that never finishes in time against a producer that never
		// stops: the ready queue reaches its ceiling, and the ceiling is the
		// only thing in this engine that closes an intake.
		config := specConfig(1)
		config.Workers = 1
		config.Capacity = 3
		config.WorkDuration = 20 * specSweep
		_, subscriber := brokerUnder(config)

		full := subscriber.await("a queue that stopped accepting",
			func(view BrokerView) bool { return !view.Accepting })
		Expect(full.Depth).To(BeNumerically(">=", config.Capacity))

		refused := subscriber.await("a refused submission",
			func(view BrokerView) bool { return view.Refused >= 1 })
		Expect(refused.Conserved()).To(BeTrue(),
			"a refused submission was never accepted, so it is in none of the three "+
				"places and is not counted as submitted either")
	})
})

var _ = Describe("The conservation law", func() {
	It("holds of a view where every message is somewhere, and not of one where it is not", func() {
		// The law is what every running specification above is checked against,
		// so it is worth one assertion that it can fail: a check that passes
		// anything measures nothing.
		Expect(BrokerView{Submitted: 5, Acknowledged: 2, Depth: 1, InFlight: 1, DeadLettered: 1}.
			Conserved()).To(BeTrue())
		Expect(BrokerView{Submitted: 5, Acknowledged: 2, Depth: 1, InFlight: 1}.
			Conserved()).To(BeFalse())
	})
})
