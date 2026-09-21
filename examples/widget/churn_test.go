package main

import (
	"context"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/examples/widget/clusterheartbeats"
	"github.com/candacelabs/csf/examples/widget/raftdemo"
)

// Liveness, asserted at the card rather than at the engine.
//
// The specification beside this one watches the card survive losing its leader
// once. Once is first paint of the second state: it is satisfied by a card that
// renders whatever the stream last said and would keep rendering it forever.
// What a live region has to do is keep converging, so this loses the leader
// twice running — the second time to a leader that was elected moments earlier —
// and asserts the card arrives at a third leader over the survivors, and then
// back at full membership when they recover.
//
// raftdemo has no way to kill a goroutine, deliberately: a node owns its state
// and a crashed one keeps the term and vote it had, which is what a process
// whose state is on disk does when it dies. Crash is therefore the strongest
// fault this engine can be asked for, and losing two leaders inside one election
// timeout is the hardest sequence it can be asked to survive.
var _ = Describe("The card under leadership churn", func() {
	It("converges on a new leader after the leader is lost twice running", func() {
		// Five nodes rather than three: two crashes still leave a majority, so
		// a card that stalls has stalled for its own reasons rather than
		// because the protocol correctly refused to elect.
		cluster, buildError := raftdemo.New(raftdemo.Config{
			Nodes:           5,
			Heartbeat:       25 * time.Millisecond,
			ElectionTimeout: 80 * time.Millisecond,
			ElectionJitter:  60 * time.Millisecond,
			Seed:            20260902,
		})
		Expect(buildError).ToNot(HaveOccurred())

		ctx, cancel := context.WithCancel(context.Background())
		stopped := make(chan error, 1)
		go func() { stopped <- cluster.Run(ctx) }()
		DeferCleanup(func() {
			cancel()
			Eventually(stopped, probePatience).Should(Receive(BeNil()))
		})

		client := dialProbe(probeApp(cluster))
		elected := newLeaderWatch(cluster)

		awaitRaft(client, "the first leader at full membership",
			func(html string) bool { return strings.Contains(html, "5 / 5 voters alive") })

		first := elected.next()
		Expect(cluster.Crash(context.Background(), first)).To(Succeed())
		awaitRaft(client, "the card reporting the first outage",
			func(html string) bool { return strings.Contains(html, "leader pending") })

		second := elected.nextOtherThan(first)
		Expect(cluster.Crash(context.Background(), second)).To(Succeed())

		settled := awaitRaft(client, "the card reporting a third leader over three survivors",
			func(html string) bool {
				return strings.Contains(html, "elected leader") &&
					strings.Contains(html, "3 / 5 voters alive")
			})
		html, _ := settled.Patch.Fragment(clusterheartbeats.ClusterHeartbeatsRegion)
		Expect(html).To(ContainSubstring("Live activity"),
			"three of five is still a majority, so the survivors are live")
		Expect(html).To(ContainSubstring(`data-motion="true"`),
			"and the gate reopens, so the picture starts moving again on its own")

		Expect(cluster.Recover(context.Background(), first)).To(Succeed())
		Expect(cluster.Recover(context.Background(), second)).To(Succeed())
		awaitRaft(client, "the card reporting the recovered fleet",
			func(html string) bool { return strings.Contains(html, "5 / 5 voters alive") })

		// The safety property, over every view the churn produced: a node votes
		// at most once per term and a leader needs a majority, so two leaders of
		// one term would need two majorities overlapping on a node that voted
		// twice.
		//
		// Drained once into a slice: claims() empties the channel, so asking it
		// twice would assert the property over nothing.
		observed := elected.claims()
		Expect(observed).ToNot(BeEmpty(), "no view was observed, so the property was asserted over nothing")
		for _, claiming := range observed {
			Expect(claiming).To(BeNumerically("<=", 1), "two leaders claimed one term")
		}
	})

	It("stops moving and reports the surviving minority when quorum is gone", func() {
		cluster := probeCluster()
		client := dialProbe(probeApp(cluster))
		awaitRaft(client, "a first leader",
			func(html string) bool { return strings.Contains(html, "3 / 3 voters alive") })

		for _, name := range cluster.Names()[:2] {
			Expect(cluster.Crash(context.Background(), name)).To(Succeed())
		}

		stalled := awaitRaft(client, "the card reporting the surviving minority",
			func(html string) bool { return strings.Contains(html, "1 / 3 voters alive") })
		html, _ := stalled.Patch.Fragment(clusterheartbeats.ClusterHeartbeatsRegion)
		Expect(html).To(ContainSubstring(`data-motion="false"`),
			"nothing animates while the cluster cannot elect")
		Expect(html).To(ContainSubstring("leader pending"))
	})
})

// leaderWatch is one subscription to the cluster's fleet view, read by one
// goroutine, so a specification can ask which node is leading without reading a
// node's state from another goroutine — which is the thing raftdemo does not do.
type leaderWatch struct {
	leaders chan string
	claimed chan int
}

// newLeaderWatch subscribes for the rest of the spec.
func newLeaderWatch(cluster *raftdemo.Cluster) *leaderWatch {
	GinkgoHelper()

	tracking, release := context.WithCancel(context.Background())
	DeferCleanup(release)
	views, subscribeError := cluster.Subscribe(tracking)
	Expect(subscribeError).ToNot(HaveOccurred())

	// Buffered and dropping: a view is a complete picture rather than a delta,
	// so a watch that fell behind has lost nothing it needs, and a watch that
	// blocked would hold the cluster it is observing hostage.
	watch := &leaderWatch{leaders: make(chan string, 512), claimed: make(chan int, 512)}
	go func() {
		for view := range views {
			select {
			case watch.claimed <- view.LeaderClaims:
			default:
			}
			if !view.LeaderKnown {
				continue
			}
			select {
			case watch.leaders <- view.Leader:
			default:
			}
		}
	}()
	return watch
}

// next is the next node the fleet view reports as leading.
func (watch *leaderWatch) next() string { return watch.nextOtherThan("") }

// nextOtherThan is the next leader that is not the one already lost.
func (watch *leaderWatch) nextOtherThan(previous string) string {
	GinkgoHelper()

	found := ""
	Eventually(func() string {
		select {
		case name := <-watch.leaders:
			if name != previous {
				found = name
			}
		case <-time.After(20 * time.Millisecond):
		}
		return found
	}, probePatience).ShouldNot(BeEmpty())
	return found
}

// claims is every leader-claim count observed so far, drained without blocking.
func (watch *leaderWatch) claims() []int {
	observed := []int{}
	for {
		select {
		case claiming := <-watch.claimed:
			observed = append(observed, claiming)
		default:
			return observed
		}
	}
}
