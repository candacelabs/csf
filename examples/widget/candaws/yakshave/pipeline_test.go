package yakshave

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The specifications below run a real pipeline: four stage goroutines, a head,
// a meter, an observer and two feeds, against real timers. They are paced far
// faster than the demo is, because everything here is a question about the
// chain rather than about what a picture looks like.
//
// Nothing asserts when a stage finishes. The seed fixes the draws and not the
// scheduler, so a specification claiming a particular moment would be one that
// fails on a busy machine for no reason anybody can act on.
const (
	specCadence  = 120 * time.Millisecond
	specStage    = 8 * time.Millisecond
	specPatience = 10 * time.Second
)

// specConfig is the pace every specification in this file runs at. The failure
// rate is a parameter because 0 and 1 are the two rates that make the chain
// deterministic, and every specification here wants one of them.
func specConfig(rate float64) Config {
	return Config{
		Cadence:       specCadence,
		StageDuration: specStage,
		QuotaMinutes:  40,
		RetryCeiling:  2,
		FailureRate:   rate,
		Seed:          20260902,
	}
}

// runs is one subscriber to the run stream, read only by the specification's
// own goroutine. It keeps every view, so a specification can wait for a
// condition and then assert something about the whole path it took to get
// there — which is where the ordering invariant lives, since "no view ever
// showed a deploy over a red build" is not a claim about any one view.
type runs struct {
	views <-chan RunView
	seen  []RunView
}

// await takes views until one matches, and fails naming what it was waiting for.
func (subscriber *runs) await(what string, match func(view RunView) bool) RunView {
	GinkgoHelper()
	deadline := time.After(specPatience)
	for {
		select {
		case view, open := <-subscriber.views:
			Expect(open).To(BeTrue(), "the pipeline stopped before %s", what)
			subscriber.seen = append(subscriber.seen, view)
			if match(view) {
				return view
			}
		case <-deadline:
			Fail("never saw " + what + " within " + specPatience.String())
			return RunView{}
		}
	}
}

// pipelineUnder starts a pipeline for the length of one specification, with one
// run subscriber attached, and joins its goroutines on the way out.
func pipelineUnder(config Config) (*Pipeline, *runs) {
	GinkgoHelper()

	pipeline, buildError := NewPipeline(config)
	Expect(buildError).ToNot(HaveOccurred())

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() { stopped <- pipeline.Run(ctx) }()
	DeferCleanup(func() {
		cancel()
		Eventually(stopped, specPatience).Should(Receive())
	})

	views, subscribeError := pipeline.Runs(ctx)
	Expect(subscribeError).ToNot(HaveOccurred())
	return pipeline, &runs{views: views}
}

var _ = Describe("A pipeline that is configured wrongly", func() {
	It("reports the fault rather than running and never shipping", func() {
		for _, broken := range []struct {
			what   string
			config Config
			fault  error
		}{
			{"no cadence", Config{}, ErrCadence},
			{"a chain that does not fit its cadence",
				Config{Cadence: time.Millisecond, StageDuration: time.Second}, ErrStageDuration},
			{"no quota",
				Config{Cadence: time.Second, StageDuration: time.Millisecond}, ErrQuota},
			{"a negative retry ceiling",
				Config{Cadence: time.Second, StageDuration: time.Millisecond,
					QuotaMinutes: 1, RetryCeiling: -1}, ErrRetryCeiling},
			{"a failure rate that is not a probability",
				Config{Cadence: time.Second, StageDuration: time.Millisecond,
					QuotaMinutes: 1, FailureRate: 1.5}, ErrFailureRate},
		} {
			_, buildError := NewPipeline(broken.config)
			Expect(buildError).To(MatchError(broken.fault), "for %s", broken.what)
		}
	})

	It("refuses a second Run rather than starting a second chain", func() {
		pipeline, _ := pipelineUnder(specConfig(0))
		Expect(pipeline.Run(context.Background())).To(MatchError(ContainSubstring("already running")))
	})
})

var _ = Describe("A running pipeline", func() {
	It("moves one artifact through four stages in chain order", func() {
		_, subscriber := pipelineUnder(specConfig(0))

		green := subscriber.await("a run that cleared every stage",
			func(view RunView) bool { return view.Green() })
		Expect(green.Run).To(BeNumerically(">=", 1))
		Expect(green.Retries).To(Equal(0), "nothing failed at a failure rate of zero")

		// The whole path, not the last view: every stage was reported busy, in
		// chain order, and no view along the way ever showed a stage cleared
		// over one that was not.
		var order []string
		for _, view := range subscriber.seen {
			Expect(view.Ordered()).To(BeTrue(),
				"a view showed a stage cleared over a predecessor that was not: %+v", view)
			if len(order) == 0 || order[len(order)-1] != view.Stage {
				order = append(order, view.Stage)
			}
		}
		Expect(order).To(ContainElements("checkout", "build", "test", "deploy"))
		Expect(order).To(HaveExactElements(
			"checkout", "build", "test", "deploy", "idle"),
			"an artifact is in exactly one stage, and it visits them in order")
	})

	It("mints a monotonic sequence that is never reused", func() {
		_, subscriber := pipelineUnder(specConfig(0))
		subscriber.await("a handful of views",
			func(view RunView) bool { return view.Sequence >= 8 })

		for index, view := range subscriber.seen {
			Expect(view.Sequence).To(Equal(uint64(index+1)),
				"the sequence is what the card re-arms its motion on, so a repeat is a pulse that does not")
		}
	})

	It("retries a red build from the head, and stops at the ceiling", func() {
		config := specConfig(1)
		config.RetryCeiling = 1
		_, subscriber := pipelineUnder(config)

		retried := subscriber.await("a retried run",
			func(view RunView) bool { return view.Retries >= 1 })
		Expect(retried.Cleared[stageCheckout]).To(BeFalse(),
			"at a failure rate of one the first stage is where every run stops")

		// The ceiling holds: a run is retried once and then abandoned, so the
		// retry count never reaches two however long this waits.
		subscriber.await("several more views",
			func(view RunView) bool { return view.Sequence >= retried.Sequence+6 })
		for _, view := range subscriber.seen {
			Expect(view.Retries).To(BeNumerically("<=", config.RetryCeiling),
				"a retry past the ceiling is a quota nobody agreed to spend")
		}
	})

	It("stops the whole chain from one close at the head", func() {
		pipeline, _ := pipelineUnder(specConfig(0))

		// Not a cancelled context: the context is still live and every stage is
		// still selecting on it. Closing the first stage's inbound channel is
		// the only signal, and the chain shuts itself down in order from it —
		// each stage closing its own outbound channel after its inbound one
		// closed, which is what reaching the far end proves.
		pipeline.Drain()
		Expect(pipeline.Drain).ToNot(Panic(), "a second drain is a no-op, not a closed channel twice")
		Eventually(pipeline.Drained(), specPatience).Should(BeClosed())
	})
})

var _ = Describe("The meter", func() {
	It("spends a monotonically decreasing quota and never refunds", func() {
		pipeline, _ := pipelineUnder(specConfig(0))

		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		minutes, subscribeError := pipeline.Quota(ctx)
		Expect(subscribeError).ToNot(HaveOccurred())

		var seen []QuotaView
		Eventually(func() int {
			select {
			case view, open := <-minutes:
				if !open {
					return len(seen)
				}
				seen = append(seen, view)
			case <-time.After(specCadence):
			}
			return len(seen)
		}, specPatience).Should(BeNumerically(">=", 4))

		for index := 1; index < len(seen); index++ {
			Expect(seen[index].QuotaMinutes).To(BeNumerically("<=", seen[index-1].QuotaMinutes),
				"consumed minutes never decrease within a billing window")
			Expect(seen[index].QueueMinutes).To(BeNumerically(">=", seen[index-1].QueueMinutes))
		}
		Expect(seen[len(seen)-1].QuotaMinutes).To(BeNumerically("<", seen[0].QuotaMinutes),
			"a pipeline that ran and was billed nothing is not this product")
	})

	It("refuses to start a run on a zero balance", func() {
		config := specConfig(0)
		config.QuotaMinutes = 2
		pipeline, subscriber := pipelineUnder(config)

		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		minutes, subscribeError := pipeline.Quota(ctx)
		Expect(subscribeError).ToNot(HaveOccurred())

		exhausted := func() bool {
			select {
			case view, open := <-minutes:
				return open && view.QuotaMinutes == 0
			case <-time.After(specCadence):
				return false
			}
		}
		Eventually(exhausted, specPatience).Should(BeTrue())

		// Two minutes buys two runs and no more. Whatever the highest run
		// identity reaches, it stops there, because the head asks before it
		// injects and is told no.
		highest := uint64(0)
		for _, view := range subscriber.seen {
			if view.Run > highest {
				highest = view.Run
			}
		}
		Consistently(func() uint64 {
			select {
			case view, open := <-subscriber.views:
				if open && view.Run > highest {
					highest = view.Run
				}
			case <-time.After(specStage):
			}
			return highest
		}, 4*specCadence).Should(BeNumerically("<=", config.QuotaMinutes),
			"a run on a zero balance is a run the customer was not charged for")
	})
})
