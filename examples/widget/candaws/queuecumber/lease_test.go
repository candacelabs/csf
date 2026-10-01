package queuecumber

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The cardinality half of the ontology table, which the conservation law does
// not reach.
//
// `docs/fleet.md` says "Worker → Lease `0..1`" and "A worker never holds two
// leases"; `worker.go` says "at most one message in hand at any instant".
// [BrokerView.Conserved] cannot see any of that — a broker handing one worker
// four leases at once conserves every message perfectly — and until these
// specifications existed nothing else could either. An external probe over a
// running broker is what found the promise broken, at eight workers and nine
// messages in flight, so one of these is that probe with an assertion in it.
//
// The unit-level ones drive `serve` directly through [brokerServing]: this
// package's specifications are the producer and the worker, so what the broker
// does with a second ask from one worker is a fact rather than a race.

var _ = Describe("A broker asked twice by one worker", func() {
	It("answers once, and leaves the second ask holding nothing", func() {
		broker, subscriber := brokerServing(specConfig(0))

		broker.submissions <- struct{}{}
		broker.submissions <- struct{}{}
		subscriber.await("both messages accepted",
			func(view BrokerView) bool { return view.Depth == 2 })

		// One worker, one inbox, for the worker's whole life — which is what
		// the broker is handed in the request and what makes a grant it makes
		// after the worker stopped waiting still that worker's to pick up.
		inbox := make(chan lease, 1)
		broker.requests <- leaseRequest{Worker: 0, Reply: inbox}

		var granted lease
		Eventually(inbox, specPatience).Should(Receive(&granted))
		Expect(granted.Worker).To(Equal(0))
		leasing := subscriber.await("the lease in the view",
			func(view BrokerView) bool { return view.InFlight == 1 })

		// The same worker asks again, having acknowledged nothing. Nothing else
		// is running against this broker, so the next view it publishes is its
		// answer to that ask and to nothing else.
		broker.requests <- leaseRequest{Worker: 0, Reply: inbox}
		answered := subscriber.await("the answer to the second ask",
			func(view BrokerView) bool { return view.Sequence > leasing.Sequence })

		Expect(answered.InFlight).To(Equal(1),
			"a second lease to a worker already holding one is how in flight climbs past the pool")
		Expect(answered.Depth).To(Equal(1),
			"the message that was not granted is still in the queue")
		Expect(answered.WorkersUp).To(Equal(1))
		Expect(inbox).ToNot(Receive(),
			"the broker answered the second ask by not answering it")
		Expect(answered.Redelivered).To(Equal(uint64(0)),
			"billing is per message looked at, and nobody looked at anything twice")
	})

	It("grants again once the lease it was holding is acknowledged", func() {
		// The refusal is a wait rather than a wall: the whole engine would stop
		// if a worker that asked twice could never be answered again.
		broker, subscriber := brokerServing(specConfig(0))

		broker.submissions <- struct{}{}
		broker.submissions <- struct{}{}
		subscriber.await("both messages accepted",
			func(view BrokerView) bool { return view.Depth == 2 })

		inbox := make(chan lease, 1)
		broker.requests <- leaseRequest{Worker: 0, Reply: inbox}
		var first lease
		Eventually(inbox, specPatience).Should(Receive(&first))

		broker.acks <- ack{ID: first.Message.ID, Worker: first.Worker}
		subscriber.await("the acknowledgement",
			func(view BrokerView) bool { return view.Acknowledged == 1 })

		broker.requests <- leaseRequest{Worker: 0, Reply: inbox}
		var second lease
		Eventually(inbox, specPatience).Should(Receive(&second))
		Expect(second.Message.ID).ToNot(Equal(first.Message.ID))
		Expect(second.Message.Attempts).To(Equal(1),
			"a message granted for the first time has been looked at once")
	})

	It("ignores an acknowledgement from a worker the lease was not granted to", func() {
		// An ack names the lease it was granted, or it names nothing. Counting
		// somebody else's would take a message out of the in-flight table while
		// the worker actually holding it was still working on it.
		broker, subscriber := brokerServing(specConfig(0))

		broker.submissions <- struct{}{}
		subscriber.await("the message accepted",
			func(view BrokerView) bool { return view.Depth == 1 })

		inbox := make(chan lease, 1)
		broker.requests <- leaseRequest{Worker: 0, Reply: inbox}
		var granted lease
		Eventually(inbox, specPatience).Should(Receive(&granted))
		leasing := subscriber.await("the lease in the view",
			func(view BrokerView) bool { return view.InFlight == 1 })

		broker.acks <- ack{ID: granted.Message.ID, Worker: 1}
		// The stray ack changes nothing, so nothing is published for it. A
		// second worker asking is what produces the next view, and that view is
		// where the message still being in flight is visible.
		broker.requests <- leaseRequest{Worker: 1, Reply: make(chan lease, 1)}
		answered := subscriber.await("a view after the stray acknowledgement",
			func(view BrokerView) bool { return view.Sequence > leasing.Sequence })
		Expect(answered.Acknowledged).To(Equal(uint64(0)))
		Expect(answered.InFlight).To(Equal(1))
	})
})

// probes are the paces the running-broker invariant is checked at.
//
// Three, because a cardinality is broken by a race and a race shows up at some
// speeds and not others. The first is the sharpest and is the one worth reading
// as an experiment: two workers, a queue that always has something in it, and a
// drop rate of one. A worker that drops says nothing and asks again while the
// lease it was granted is still live, so a broker that does not check who is
// holding what hands that same worker a second message, and a third, until the
// first one ages out — and the count the card renders as in flight climbs past
// the number of hands in the fleet. The other two watch the same promise where
// the queue is starved and where nothing is dropped at all.
var probes = []struct {
	what      string
	config    Config
	milestone string
	reached   func(view BrokerView) bool
}{
	{
		what: "when a worker drops what it was given and asks again",
		config: Config{
			Workers: 2, Cadence: time.Millisecond,
			WorkDuration: time.Millisecond, SweepInterval: 5 * time.Millisecond,
			VisibilitySweeps: 2, Capacity: 16, AttemptCeiling: 3,
			DropRate: 1, Seed: 20260903,
		},
		milestone: "a handful of redeliveries",
		reached:   func(view BrokerView) bool { return view.Redelivered >= 5 },
	},
	{
		what: "when the pool is starved",
		config: Config{
			Workers: 8, Cadence: 3 * time.Millisecond,
			WorkDuration: 2 * time.Millisecond, SweepInterval: 3 * time.Millisecond,
			VisibilitySweeps: 2, Capacity: 4, AttemptCeiling: 3,
			DropRate: 0.5, Seed: 20260903,
		},
		milestone: "a handful of redeliveries",
		reached:   func(view BrokerView) bool { return view.Redelivered >= 5 },
	},
	{
		what: "when every worker acknowledges",
		config: Config{
			Workers: 8, Cadence: specCadence,
			WorkDuration: specWork, SweepInterval: specSweep,
			VisibilitySweeps: 2, Capacity: 16, AttemptCeiling: 2,
			DropRate: 0, Seed: 20260903,
		},
		milestone: "a handful of acknowledged messages",
		reached:   func(view BrokerView) bool { return view.Acknowledged >= 8 },
	},
}

var _ = Describe("Every view a running broker hands a subscriber", func() {
	for _, probe := range probes {
		It("reports no more leases than there are hands to hold them, "+probe.what, func() {
			_, subscriber := brokerUnder(probe.config)
			subscriber.await(probe.milestone, probe.reached)

			// await has already checked the conservation law on every one of
			// these; this is the other half of the table. A worker holds nought
			// or one lease, so the messages in flight cannot outnumber the
			// workers the broker has heard from — and cannot outnumber the pool
			// it was configured with either, which is the number the card draws
			// the in-flight count beside.
			for _, view := range subscriber.seen {
				Expect(view.InFlight).To(BeNumerically("<=", view.WorkersUp),
					"more leases than workers that have asked, so somebody is holding two: %+v", view)
				Expect(view.InFlight).To(BeNumerically("<=", probe.config.Workers),
					"more leases than there are workers at all: %+v", view)
			}
		})
	}
})
