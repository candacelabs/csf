package raftdemo

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The specifications below run a real cluster: N goroutines, a network and a
// fleet view, against real timers. They are paced far faster than the demo is —
// a 20ms heartbeat rather than 900ms — because everything here is a question
// about the protocol rather than about what a picture looks like.
//
// Nothing asserts which node wins. The seed fixes the jitter and not the
// scheduler, so the winner is not a property of the configuration and a
// specification claiming it would be a specification that fails on a busy
// machine for no reason anybody can act on.
const (
	specHeartbeat = 20 * time.Millisecond
	specTimeout   = 60 * time.Millisecond
	specJitter    = 60 * time.Millisecond
	specPatience  = 5 * time.Second
)

// specConfig is the pace every specification in this file runs at.
func specConfig(nodes int) Config {
	return Config{
		Nodes:           nodes,
		Heartbeat:       specHeartbeat,
		ElectionTimeout: specTimeout,
		ElectionJitter:  specJitter,
		Seed:            20260902,
	}
}

// stream is one subscriber, read only by the specification's own goroutine.
//
// It keeps every view it took, so a specification can wait for a condition and
// then assert something about the whole path it took to get there — which is
// where the safety properties live, since "there was never a second leader" is
// not a claim about any one view.
type stream struct {
	views <-chan Snapshot
	seen  []Snapshot
}

// await takes views until one matches, and fails naming what it saw instead.
func (subscriber *stream) await(what string, match func(view Snapshot) bool) Snapshot {
	GinkgoHelper()
	deadline := time.After(specPatience)
	for {
		select {
		case view, open := <-subscriber.views:
			Expect(open).To(BeTrue(), "the cluster stopped before %s", what)
			subscriber.seen = append(subscriber.seen, view)
			if match(view) {
				return view
			}
		case <-deadline:
			Fail("never saw " + what + " within " + specPatience.String())
			return Snapshot{}
		}
	}
}

// drain takes whatever is already queued without waiting for more.
func (subscriber *stream) drain() {
	for {
		select {
		case view, open := <-subscriber.views:
			if !open {
				return
			}
			subscriber.seen = append(subscriber.seen, view)
		default:
			return
		}
	}
}

// runCluster starts a cluster for the length of one specification and returns it
// with one subscriber already attached.
//
// The cleanup waits for Run to return rather than merely cancelling, so a
// goroutine still running when the next specification starts is a failure here
// rather than a race detector report attributed to somebody else.
func runCluster(config Config) (*Cluster, *stream) {
	GinkgoHelper()

	cluster, buildError := New(config)
	Expect(buildError).ToNot(HaveOccurred())

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() { stopped <- cluster.Run(ctx) }()
	DeferCleanup(func() {
		cancel()
		Eventually(stopped, specPatience).Should(Receive(BeNil()))
	})

	views, subscribeError := cluster.Subscribe(ctx)
	Expect(subscribeError).ToNot(HaveOccurred())
	return cluster, &stream{views: views}
}

// elected matches the first view that has a leader with a live majority.
func elected(view Snapshot) bool { return view.LeaderKnown && view.HasQuorum }

var _ = Describe("A running cluster", func() {
	It("elects one leader and reports the fleet view as the leader's own", func() {
		cluster, subscriber := runCluster(specConfig(3))

		view := subscriber.await("a leader with a quorum", elected)
		Expect(view.Term).To(BeNumerically(">=", 1))
		Expect(view.Leader).To(BeElementOf(cluster.Names()))
		Expect(view.LeaderClaims).To(Equal(1))
		Expect(view.Authoritative).To(BeTrue(),
			"a view minted from a leader's own heartbeat is the leader's own")
		Expect(view.Voters).To(Equal(3))

		full := subscriber.await("every voter reachable", func(view Snapshot) bool {
			return view.AliveVoters == 3
		})
		Expect(full.HasQuorum).To(BeTrue())
	})

	It("says so, rather than guessing, while it has no leader to speak for it", func() {
		_, subscriber := runCluster(specConfig(3))

		// The first view is minted by the observer's own timer, before any node
		// has had time to campaign: the stream is flowing and there is nothing
		// authoritative on it yet, which is the state the card draws as an
		// election in progress rather than as a dead connection.
		first := subscriber.await("the first view", func(view Snapshot) bool { return true })
		Expect(first.Sequence).To(Equal(uint64(1)))
		Expect(first.LeaderKnown).To(BeFalse())
		Expect(first.Authoritative).To(BeFalse())
		Expect(first.Leader).To(BeEmpty())
	})

	It("mints one view per heartbeat round once a leader is beating", func() {
		_, subscriber := runCluster(specConfig(3))
		subscriber.await("a leader", elected)
		// The promotion mints a view out of band with the leader's own ticker,
		// so the first interval after an election is short by however far the
		// two were out of phase. The rate being measured is the steady one.
		subscriber.await("the round after the election", func(view Snapshot) bool { return true })

		const window = 20 * specHeartbeat
		opened := time.Now()
		first := subscriber.await("the first round of the window",
			func(view Snapshot) bool { return true })
		last := subscriber.await("the window closing", func(view Snapshot) bool {
			return time.Since(opened) >= window
		})

		// A view arrives per beat and per nothing else, so the sequence advances
		// across a window of N heartbeats by about N. The slack is for the
		// scheduler; it is not slack for a second clock, which is what a fleet
		// view with a ticker of its own would be — drifting past the heartbeat
		// and minting views nothing happened for.
		rounds := int(last.Sequence - first.Sequence)
		Expect(rounds).To(BeNumerically("~", int(window/specHeartbeat), 6),
			"the picture moves once per round, so the rounds in a window are the window")
	})

	It("advances the sequence strictly, so the widget's motion re-arms once per view", func() {
		_, subscriber := runCluster(specConfig(3))
		subscriber.await("several rounds", func(view Snapshot) bool { return view.Sequence >= 6 })

		for index := 1; index < len(subscriber.seen); index++ {
			Expect(subscriber.seen[index].Sequence).To(BeNumerically(">",
				subscriber.seen[index-1].Sequence))
		}
	})
})

var _ = Describe("A cluster that loses its leader", func() {
	It("elects a new one in a higher term, which is the whole demonstration", func() {
		cluster, subscriber := runCluster(specConfig(3))

		first := subscriber.await("a first leader", elected)
		Expect(cluster.Crash(context.Background(), first.Leader)).To(Succeed())

		leaderless := subscriber.await("the leader's absence", func(view Snapshot) bool {
			return !view.LeaderKnown
		})
		Expect(leaderless.Authoritative).To(BeFalse(),
			"with no leader there is nobody entitled to speak for the cluster")

		second := subscriber.await("a second leader", elected)
		Expect(second.Leader).ToNot(Equal(first.Leader))
		Expect(second.Term).To(BeNumerically(">", first.Term))
		Expect(second.AliveVoters).To(Equal(2), "the crashed node acknowledges nothing")
		Expect(second.HasQuorum).To(BeTrue(), "two of three is still a majority")
	})

	It("takes a recovered node back into the fleet view", func() {
		cluster, subscriber := runCluster(specConfig(3))

		first := subscriber.await("a first leader", elected)
		Expect(cluster.Crash(context.Background(), first.Leader)).To(Succeed())
		subscriber.await("a second leader", func(view Snapshot) bool {
			return elected(view) && view.Leader != first.Leader
		})

		Expect(cluster.Recover(context.Background(), first.Leader)).To(Succeed())
		rejoined := subscriber.await("every voter reachable again", func(view Snapshot) bool {
			return view.AliveVoters == 3
		})
		Expect(rejoined.LeaderKnown).To(BeTrue())
	})

	It("never reports two leaders of one term, however the leadership moved", func() {
		cluster, subscriber := runCluster(specConfig(3))

		first := subscriber.await("a first leader", elected)
		Expect(cluster.Crash(context.Background(), first.Leader)).To(Succeed())
		second := subscriber.await("a second leader", func(view Snapshot) bool {
			return elected(view) && view.Leader != first.Leader
		})
		Expect(cluster.Recover(context.Background(), first.Leader)).To(Succeed())
		subscriber.await("the returning node rejoining", func(view Snapshot) bool {
			return view.AliveVoters == 3
		})
		Expect(cluster.Crash(context.Background(), second.Leader)).To(Succeed())
		subscriber.await("a third leader", func(view Snapshot) bool {
			return elected(view) && view.Leader != second.Leader
		})
		subscriber.drain()

		// A node votes at most once per term and a leader needs a majority, so
		// two leaders of one term would need two majorities of one set that
		// overlap on a node which voted twice. That is the argument; this is
		// the observation of it, over every view the whole run produced.
		Expect(subscriber.seen).ToNot(BeEmpty())
		for _, view := range subscriber.seen {
			Expect(view.LeaderClaims).To(BeNumerically("<=", 1),
				"two leaders claimed term %d", view.Term)
		}
	})
})

var _ = Describe("A cluster reduced to a minority", func() {
	It("stays leaderless rather than appointing itself, and says it has no quorum", func() {
		cluster, subscriber := runCluster(specConfig(3))

		subscriber.await("a leader", elected)
		for _, name := range cluster.Names()[:2] {
			Expect(cluster.Crash(context.Background(), name)).To(Succeed())
		}

		degraded := subscriber.await("the quorum going", func(view Snapshot) bool {
			return !view.HasQuorum
		})
		Expect(degraded.LeaderKnown).To(BeFalse())
		Expect(degraded.AliveVoters).To(Equal(1))

		// A minority cannot tell "everyone else died" from "I am cut off", so it
		// must not appoint itself. It keeps campaigning — the term climbs — and
		// it never wins.
		before := degraded.Term
		climbing := subscriber.await("the term climbing without a winner", func(view Snapshot) bool {
			return view.Term > before
		})
		Expect(climbing.LeaderKnown).To(BeFalse())
		for _, view := range subscriber.seen {
			if view.Term > before {
				Expect(view.LeaderKnown).To(BeFalse(),
					"a minority elected a leader in term %d", view.Term)
			}
		}
	})
})

var _ = Describe("A cluster of one", func() {
	It("elects itself, because it is its own majority", func() {
		config := specConfig(1)
		config.ElectionJitter = 0
		_, subscriber := runCluster(config)

		view := subscriber.await("the only node electing itself", elected)
		Expect(view.Leader).To(Equal("node-a"))
		Expect(view.AliveVoters).To(Equal(1))
		Expect(view.LeaderClaims).To(Equal(1))
	})
})

var _ = Describe("Subscribers", func() {
	It("do not hold the cluster hostage by failing to read", func() {
		cluster, reading := runCluster(specConfig(3))

		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		_, subscribeError := cluster.Subscribe(ctx)
		Expect(subscribeError).ToNot(HaveOccurred())

		// The second subscriber never reads a single view. Its buffer fills and
		// the fan-out drops into it, because a view is a whole picture rather
		// than a delta — so a subscriber that missed one has lost nothing, and a
		// cluster that stalled for it would be a protocol hostage to a browser.
		reading.await("a leader, with a subscriber that never reads", elected)
		reading.await("the rounds after that", func(view Snapshot) bool {
			return view.Sequence > uint64(subscriberBuffer)+2
		})
	})

	It("get the current view immediately rather than waiting out a round", func() {
		cluster, first := runCluster(specConfig(3))
		established := first.await("a leader", elected)

		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		views, subscribeError := cluster.Subscribe(ctx)
		Expect(subscribeError).ToNot(HaveOccurred())

		late := &stream{views: views}
		Expect(late.await("the view already in hand", func(view Snapshot) bool {
			return true
		}).Sequence).To(BeNumerically(">=", established.Sequence))
	})
})

var _ = Describe("Building a cluster", func() {
	DescribeTable("refuses a configuration that cannot elect",
		func(broken func(config *Config), expected error) {
			config := specConfig(3)
			broken(&config)
			cluster, buildError := New(config)
			Expect(cluster).To(BeNil())
			Expect(buildError).To(MatchError(expected))
		},
		Entry("no nodes at all", func(config *Config) { config.Nodes = 0 }, ErrNodeCount),
		Entry("more nodes than the liveness masks hold",
			func(config *Config) { config.Nodes = maxNodes + 1 }, ErrNodeCount),
		Entry("a heartbeat that never fires",
			func(config *Config) { config.Heartbeat = 0 }, ErrHeartbeat),
		Entry("an election timeout a healthy leader cannot outrun",
			func(config *Config) { config.ElectionTimeout = config.Heartbeat }, ErrElectionTimeout),
		Entry("equal timers, which split every vote forever",
			func(config *Config) { config.ElectionJitter = 0 }, ErrElectionJitter),
	)

	It("accepts the demo's own pace", func() {
		Expect(DefaultConfig().Validate()).To(Succeed())
		Expect(DefaultConfig().quorum()).To(Equal(2))
	})

	It("runs once, because a second Run would be a second network on one set of inboxes", func() {
		cluster, _ := runCluster(specConfig(3))
		Expect(cluster.Run(context.Background())).To(MatchError(ErrAlreadyRunning))
	})

	It("reports a node it does not have rather than crashing something else", func() {
		cluster, _ := runCluster(specConfig(3))
		Expect(cluster.Crash(context.Background(), "node-zz")).To(MatchError(ErrUnknownNode))
		Expect(cluster.Recover(context.Background(), "node-zz")).To(MatchError(ErrUnknownNode))
	})
})
